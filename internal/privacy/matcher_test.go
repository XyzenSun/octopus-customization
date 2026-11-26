package privacy

import (
    "errors"
    "reflect"
    "strings"
    "testing"
)

func privacyTestCompile(t *testing.T, config Config, rules []Rule, version uint64) *Snapshot {
    t.Helper()
    snapshot, err := Compile(config, rules, version)
    if err != nil {
        t.Fatalf("Compile() error = %v", err)
    }
    if snapshot == nil {
        t.Fatal("Compile() returned a nil snapshot without an error")
    }
    return snapshot
}

func privacyTestReplace(t *testing.T, snapshot *Snapshot, input, want string, wantChanged bool) {
    t.Helper()
    got, changed, err := snapshot.Replace(input)
    if err != nil {
        t.Fatalf("Replace() error = %v", err)
    }
    if got != want || changed != wantChanged {
        t.Fatalf("Replace(%q) = (%q, %t), want (%q, %t)", input, got, changed, want, wantChanged)
    }
}

func TestPrivacyMatcher(t *testing.T) {
    rules := []Rule{
        {ID: 90, Source: "13969909666", Alias: "PHONE_1", Enabled: true},
        {ID: 80, Source: "张三", Alias: "NAME", Enabled: true},
        {ID: 70, Source: "张三丰", Alias: "LONG_NAME", Enabled: true},
        {ID: 60, Source: "a", Alias: "LOWER", Enabled: true},
        {ID: 50, Source: "ab", Alias: "AB", Enabled: true},
        {ID: 40, Source: "bc", Alias: "BC", Enabled: true},
        {ID: 30, Source: "A", Alias: "UPPER", Enabled: true},
        {ID: 20, Source: "é", Alias: "NFC", Enabled: true},
        {ID: 10, Source: "e\u0301", Alias: "NFD", Enabled: true},
        {ID: 9, Source: "尾随 ", Alias: "TAIL", Enabled: true},
        {ID: 8, Source: "x\x00y", Alias: "NUL", Enabled: true},
        {ID: 7, Source: "行\"一\n路径\\尾", Alias: "ESCAPED", Enabled: true},
    }
    snapshot := privacyTestCompile(t, Config{Enabled: true, Scopes: Messages, FailurePolicy: Continue}, rules, 42)

    // 期望按原始区间手写, 不用另一遍处理器构造 oracle.
    fixtures := []struct {
        name    string
        input   string
        want    string
        changed bool
    }{
        {"empty", "", "", false},
        {"no_match", "xyz 1396990966", "xyz 1396990966", false},
        {"first", "13969909666|xyz", "[[OCTOPUS_PII:PHONE_1]]|xyz", true},
        {"last", "xyz|13969909666", "xyz|[[OCTOPUS_PII:PHONE_1]]", true},
        {"both_ends", "13969909666|xyz|13969909666", "[[OCTOPUS_PII:PHONE_1]]|xyz|[[OCTOPUS_PII:PHONE_1]]", true},
        {"repeated", "13969909666/13969909666", "[[OCTOPUS_PII:PHONE_1]]/[[OCTOPUS_PII:PHONE_1]]", true},
        {"not_whole_words", "xabz", "x[[OCTOPUS_PII:AB]]z", true},
        {"same_start_longest", "ababc", "[[OCTOPUS_PII:AB]][[OCTOPUS_PII:AB]]c", true},
        {"crossing_overlap", "abc", "[[OCTOPUS_PII:AB]]c", true},
        {"earliest_before_later", "bca", "[[OCTOPUS_PII:BC]][[OCTOPUS_PII:LOWER]]", true},
        {"adjacent_nonoverlap", "abbc", "[[OCTOPUS_PII:AB]][[OCTOPUS_PII:BC]]", true},
        {"utf8_longest", "前张三丰/张三后", "前[[OCTOPUS_PII:LONG_NAME]]/[[OCTOPUS_PII:NAME]]后", true},
        {"case_sensitive", "aA", "[[OCTOPUS_PII:LOWER]][[OCTOPUS_PII:UPPER]]", true},
        {"unicode_not_normalized", "é|e\u0301", "[[OCTOPUS_PII:NFC]]|[[OCTOPUS_PII:NFD]]", true},
        {"trailing_space_not_trimmed", "尾随 |尾随|尾随  ", "[[OCTOPUS_PII:TAIL]]|尾随|[[OCTOPUS_PII:TAIL]] ", true},
        {"valid_nul", "前x\x00y后x\x00y", "前[[OCTOPUS_PII:NUL]]后[[OCTOPUS_PII:NUL]]", true},
        {"quotes_newline_backslash", "前行\"一\n路径\\尾后", "前[[OCTOPUS_PII:ESCAPED]]后", true},
    }
    for _, fixture := range fixtures {
        t.Run(fixture.name, func(t *testing.T) {
            privacyTestReplace(t, snapshot, fixture.input, fixture.want, fixture.changed)
        })
    }
}

func TestPrivacyMatcherNonRecursive(t *testing.T) {
    snapshot := privacyTestCompile(t, Config{Enabled: true, Scopes: Messages, FailurePolicy: Continue}, []Rule{
        {ID: 1, Source: "13969909666", Alias: "PHONE_1", Enabled: true},
        {ID: 2, Source: "PHONE_1", Alias: "ALIAS_TEXT", Enabled: true},
        {ID: 3, Source: "[[OCTOPUS_PII:", Alias: "PREFIX", Enabled: true},
    }, 1)
    privacyTestReplace(t, snapshot, "13969909666", "[[OCTOPUS_PII:PHONE_1]]", true)
    // 已有 marker 仍在原输入内机械匹配, 但新生成内容不再匹配.
    privacyTestReplace(t, snapshot, "[[OCTOPUS_PII:PHONE_1]]", "[[OCTOPUS_PII:PREFIX]][[OCTOPUS_PII:ALIAS_TEXT]]]]", true)
}

func TestPrivacyMatcherEqualOutput(t *testing.T) {
    snapshot := privacyTestCompile(t, Config{Enabled: true, Scopes: Messages, FailurePolicy: Continue}, []Rule{
        {ID: 1, Source: "[[OCTOPUS_PII:SELF]]", Alias: "SELF", Enabled: true},
        {ID: 2, Source: "13969909666", Alias: "PHONE_1", Enabled: true},
    }, 1)
    privacyTestReplace(t, snapshot, "前[[OCTOPUS_PII:SELF]][[OCTOPUS_PII:SELF]]后", "前[[OCTOPUS_PII:SELF]][[OCTOPUS_PII:SELF]]后", false)
    privacyTestReplace(t, snapshot, "[[OCTOPUS_PII:SELF]]/13969909666", "[[OCTOPUS_PII:SELF]]/[[OCTOPUS_PII:PHONE_1]]", true)
}

func TestPrivacyMatcherActivation(t *testing.T) {
    fixtures := []struct {
        name   string
        config Config
        rules  []Rule
        active bool
        want   string
    }{
        {"disabled_config", Config{Scopes: Messages, FailurePolicy: Continue}, []Rule{{Source: "13969909666", Alias: "PHONE_1", Enabled: true}}, false, "13969909666"},
        {"empty_rules", Config{Enabled: true, Scopes: Messages, FailurePolicy: Continue}, nil, false, "13969909666"},
        {"disabled_rule", Config{Enabled: true, Scopes: Messages, FailurePolicy: Continue}, []Rule{{Source: "13969909666", Alias: "PHONE_1"}}, false, "13969909666"},
        {"no_scopes", Config{Enabled: true, FailurePolicy: Continue}, []Rule{{Source: "13969909666", Alias: "PHONE_1", Enabled: true}}, false, "13969909666"},
        {"enabled", Config{Enabled: true, Scopes: Messages, FailurePolicy: Continue}, []Rule{{Source: "13969909666", Alias: "PHONE_1", Enabled: true}}, true, "[[OCTOPUS_PII:PHONE_1]]"},
        {"other_scope", Config{Enabled: true, Scopes: ToolCalls, FailurePolicy: Abort}, []Rule{{Source: "13969909666", Alias: "PHONE_1", Enabled: true}}, true, "[[OCTOPUS_PII:PHONE_1]]"},
    }
    for _, fixture := range fixtures {
        t.Run(fixture.name, func(t *testing.T) {
            snapshot := privacyTestCompile(t, fixture.config, fixture.rules, 1)
            if snapshot.Active() != fixture.active {
                t.Fatalf("Active() = %t, want %t", snapshot.Active(), fixture.active)
            }
            privacyTestReplace(t, snapshot, "13969909666", fixture.want, fixture.active)
        })
    }
    t.Run("mixed_rules", func(t *testing.T) {
        snapshot := privacyTestCompile(t, Config{Enabled: true, Scopes: Messages, FailurePolicy: Continue}, []Rule{
            {Source: "张三", Alias: "NAME", Enabled: true},
            {Source: "张三丰", Alias: "LONG_NAME", Enabled: false},
        }, 1)
        privacyTestReplace(t, snapshot, "张三丰", "[[OCTOPUS_PII:NAME]]丰", true)
        if _, ok := snapshot.restoreMap["[[OCTOPUS_PII:LONG_NAME]]"]; ok {
            t.Fatal("disabled rule entered restoreMap")
        }
    })
    t.Run("nil_snapshot", func(t *testing.T) {
        privacyTestReplace(t, nil, "13969909666", "13969909666", false)
    })
}

func TestPrivacyMatcherSourceOrdering(t *testing.T) {
    sets := [][]Rule{
        {
            {ID: 1, Source: "bc", Alias: "BC", Enabled: true},
            {ID: 900, Source: "ab", Alias: "AB", Enabled: true},
            {ID: 2, Source: "a", Alias: "LOWER", Enabled: true},
            {ID: 3, Source: "A", Alias: "DISABLED"},
        },
        {
            {ID: 900, Source: "a", Alias: "LOWER", Enabled: true},
            {ID: 2, Source: "bc", Alias: "BC", Enabled: true},
            {ID: 1, Source: "ab", Alias: "AB", Enabled: true},
        },
    }
    for index, rules := range sets {
        snapshot := privacyTestCompile(t, Config{Enabled: true, Scopes: Messages, FailurePolicy: Continue}, rules, uint64(index))
        if !reflect.DeepEqual(snapshot.patterns, []string{"a", "ab", "bc"}) {
            t.Fatalf("set %d patterns = %q, want source byte ordering", index, snapshot.patterns)
        }
        if !reflect.DeepEqual(snapshot.markers, []string{"[[OCTOPUS_PII:LOWER]]", "[[OCTOPUS_PII:AB]]", "[[OCTOPUS_PII:BC]]"}) {
            t.Fatalf("set %d markers do not follow source ordering: %q", index, snapshot.markers)
        }
        privacyTestReplace(t, snapshot, "abcab", "[[OCTOPUS_PII:AB]]c[[OCTOPUS_PII:AB]]", true)
    }
}

func TestPrivacyMatcherFixedFailure(t *testing.T) {
    fixtures := []struct {
        name    string
        corrupt func(*Snapshot)
        input   string
    }{
        {"nil_matcher_panic", func(snapshot *Snapshot) { snapshot.matcher = nil }, "private-source"},
        {"pattern_index_out_of_range", func(snapshot *Snapshot) { snapshot.patterns = snapshot.patterns[:1] }, "second-secret"},
        {"source_interval_mismatch", func(snapshot *Snapshot) { snapshot.patterns[0] = "not-the-original-source" }, "private-source"},
        {"missing_marker_panic", func(snapshot *Snapshot) { snapshot.markers = nil }, "private-source"},
        {"partial_result_discarded", func(snapshot *Snapshot) { snapshot.markers = snapshot.markers[:1] }, "private-source|second-secret"},
    }
    for _, fixture := range fixtures {
        t.Run(fixture.name, func(t *testing.T) {
            snapshot := privacyTestCompile(t, Config{Enabled: true, Scopes: Messages, FailurePolicy: Continue}, []Rule{
                {Source: "private-source", Alias: "FIRST", Enabled: true},
                {Source: "second-secret", Alias: "SECOND", Enabled: true},
            }, 1)
            // 仅破坏私有快照表, 保留真实第三方 matcher 及其匹配结果.
            fixture.corrupt(snapshot)
            got, changed, err := snapshot.Replace(fixture.input)
            if got != fixture.input || changed {
                t.Fatalf("failed Replace() = (%q, %t), want original input and false", got, changed)
            }
            if !errors.Is(err, ErrMatch) {
                t.Fatalf("Replace() error = %v, want ErrMatch", err)
            }
            var privacyError *PrivacyError
            if !errors.As(err, &privacyError) || privacyError.Kind() != ErrorMatch {
                t.Fatalf("Replace() error is not a match PrivacyError: %v", err)
            }
            if err.Error() != "privacy processing failed: match" || strings.Contains(err.Error(), fixture.input) {
                t.Fatalf("Replace() error is not fixed: %q", err.Error())
            }
        })
    }
}
