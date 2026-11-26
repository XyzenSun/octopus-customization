package privacy

import (
    "sort"
    "strings"

    aho "github.com/petar-dambovaliev/aho-corasick"
)

// 编译发生在数据库提交之前. 任意失败都不返回可发布的半成品快照.
func Compile(config Config, rules []Rule, version uint64) (snapshot *Snapshot, err error) {
    defer func() {
        if recover() != nil {
            snapshot = nil
            err = ErrCompile
        }
    }()
    if err = ValidateConfig(config); err != nil {
        return nil, err
    }

    snapshot = &Snapshot{
        config:     config,
        version:    version,
        rules:      append([]Rule(nil), rules...),
        restoreMap: make(map[string]string),
    }
    sources := make(map[string]struct{}, len(rules))
    aliases := make(map[string]struct{}, len(rules))
    enabledRules := make([]Rule, 0, len(rules))
    for _, rule := range snapshot.rules {
        if err = ValidateRule(rule); err != nil {
            return nil, err
        }
        if _, exists := sources[rule.Source]; exists {
            return nil, ErrRuleConflict
        }
        if _, exists := aliases[rule.Alias]; exists {
            return nil, ErrRuleConflict
        }
        sources[rule.Source] = struct{}{}
        aliases[rule.Alias] = struct{}{}
        if rule.Enabled {
            enabledRules = append(enabledRules, rule)
        }
    }

    // 原始字节排序确保编译顺序稳定, 不让数据库 ID 或管理列表顺序决定优先级.
    sort.Slice(enabledRules, func(left, right int) bool {
        return enabledRules[left].Source < enabledRules[right].Source
    })
    for _, rule := range enabledRules {
        marker := MarkerLeft + rule.Alias + MarkerRight
        snapshot.patterns = append(snapshot.patterns, rule.Source)
        snapshot.markers = append(snapshot.markers, marker)
        snapshot.restoreMap[marker] = rule.Source
        if len(marker) > snapshot.maxMarkerLength {
            snapshot.maxMarkerLength = len(marker)
        }
    }
    if len(snapshot.patterns) == 0 {
        return snapshot, nil
    }

    builder := aho.NewAhoCorasickBuilder(aho.Opts{
        AsciiCaseInsensitive: false,
        MatchOnlyWholeWords:  false,
        MatchKind:            aho.LeftMostLongestMatch,
        DFA:                  true,
    })
    matcher := builder.Build(snapshot.patterns)
    snapshot.matcher = &matcher
    return snapshot, nil
}

// CompileForCreate 在插入前编译候选, 插入取得主键后才交出快照.
// 闭包只消费一次, 调用者无法修改已经发布的规则或匹配表.
func CompileForCreate(config Config, rules []Rule, version uint64) (func(int) *Snapshot, error) {
    if len(rules) == 0 || rules[len(rules)-1].ID != 0 {
        return nil, ErrInvalidRule
    }
    pending, err := Compile(config, rules, version)
    if err != nil {
        return nil, err
    }
    return func(id int) *Snapshot {
        if pending == nil || id <= 0 {
            return nil
        }
        snapshot := pending
        pending = nil
        snapshot.rules[len(snapshot.rules)-1].ID = id
        return snapshot
    }, nil
}

// 每份文本只查询一次原始输入. 新生成的 marker 不会再次进入 matcher.
func (snapshot *Snapshot) Replace(input string) (output string, changed bool, err error) {
    output = input
    if !snapshot.Active() {
        return output, false, nil
    }
    defer func() {
        if recover() != nil {
            output = input
            changed = false
            err = ErrMatch
        }
    }()

    matches := snapshot.matcher.FindAll(input)
    if len(matches) == 0 {
        return input, false, nil
    }
    var builder strings.Builder
    cursor := 0
    for _, match := range matches {
        patternIndex := match.Pattern()
        start, end := match.Start(), match.End()
        if patternIndex < 0 || patternIndex >= len(snapshot.patterns) ||
            start < cursor || start >= end || end > len(input) ||
            input[start:end] != snapshot.patterns[patternIndex] {
            return input, false, ErrMatch
        }
        builder.WriteString(input[cursor:start])
        builder.WriteString(snapshot.markers[patternIndex])
        cursor = end
    }
    builder.WriteString(input[cursor:])
    output = builder.String()
    changed = output != input
    if !changed {
        return input, false, nil
    }
    return output, true, nil
}
