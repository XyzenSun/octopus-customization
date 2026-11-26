package privacy

import (
    "errors"
    "fmt"
    "reflect"
    "strings"
    "testing"
)

func TestPrivacyTypesContract(t *testing.T) {
    if Messages != 1 || ToolDefinitions != 2 || ToolCalls != 4 || ToolResponses != 8 || AllScopes != 15 {
        t.Fatal("scope bit values changed")
    }
    if Continue != "continue" || Abort != "abort" {
        t.Fatal("failure policy values changed")
    }
    if MarkerLeft != "[[OCTOPUS_PII:" || MarkerRight != "]]" {
        t.Fatal("marker syntax changed")
    }
    if MaxSourceBytes != 512 || MaxAliasBytes != 64 || MaxMarkerBytes != 80 {
        t.Fatal("source, alias or marker byte bounds changed")
    }
    want := Config{Enabled: false, Scopes: Messages, FailurePolicy: Continue}
    if got := DefaultConfig(); got != want {
        t.Fatalf("DefaultConfig() = %+v, want %+v", got, want)
    }
    if err := ValidateConfig(DefaultConfig()); err != nil {
        t.Fatalf("default config is invalid: %v", err)
    }
}

func TestPrivacyTypesRuleValidation(t *testing.T) {
    fixtures := []struct {
        name   string
        source string
        alias  string
        valid  bool
    }{
        {"source_one_byte", "a", "A", true},
        {"source_511_bytes", strings.Repeat("x", 511), "A", true},
        {"source_512_bytes", strings.Repeat("x", 512), "A", true},
        {"source_513_bytes", strings.Repeat("x", 513), "A", false},
        {"source_utf8_512_bytes", strings.Repeat("é", 256), "A", true},
        {"source_utf8_513_bytes", strings.Repeat("é", 256) + "x", "A", false},
        {"source_empty", "", "A", false},
        {"source_nul", "\x00", "A", true},
        {"source_embedded_nul", "前\x00后", "A", true},
        {"source_whitespace_not_trimmed", " \t\n", "A", true},
        {"source_quotes_backslash", "行\"一\n路径\\尾", "A", true},
        {"source_nfc", "é", "A", true},
        {"source_nfd", "e\u0301", "A", true},
        {"source_utf8_replacement_rune", "\uFFFD", "A", true},
        {"source_invalid_utf8_byte", "private-\xff-source", "A", false},
        {"source_utf8_truncated", "private-\xe5\xbc", "A", false},
        {"source_utf8_overlong", "\xc0\x80", "A", false},
        {"source_utf8_surrogate", "\xed\xa0\x80", "A", false},
        {"alias_one_byte_digit", "secret", "0", true},
        {"alias_one_byte_underscore", "secret", "_", true},
        {"alias_alphabet", "secret", "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_", true},
        {"alias_63_bytes", "secret", strings.Repeat("A", 63), true},
        {"alias_64_bytes", "secret", strings.Repeat("A", 64), true},
        {"alias_65_bytes", "secret", strings.Repeat("A", 65), false},
        {"alias_empty", "secret", "", false},
        {"alias_lowercase", "secret", "PHONE_a", false},
        {"alias_space", "secret", "PHONE 1", false},
        {"alias_leading_space", "secret", " PHONE", false},
        {"alias_trailing_space", "secret", "PHONE ", false},
        {"alias_newline", "secret", "PHONE\n", false},
        {"alias_hyphen", "secret", "PHONE-1", false},
        {"alias_colon", "secret", "PHONE:1", false},
        {"alias_bracket", "secret", "PHONE]", false},
        {"alias_quotes_backslash", "secret", "PHONE\"\\", false},
        {"alias_non_ascii", "secret", "张三", false},
        {"alias_nul", "secret", "PHONE\x001", false},
        {"alias_invalid_utf8", "secret", "PHONE\xff", false},
    }
    for _, fixture := range fixtures {
        t.Run(fixture.name, func(t *testing.T) {
            // 停用不会免除校验, 避免坏数据进入管理候选快照.
            for _, enabled := range []bool{false, true} {
                rule := Rule{ID: 1, Source: fixture.source, Alias: fixture.alias, Enabled: enabled}
                err := ValidateRule(rule)
                if fixture.valid {
                    if err != nil {
                        t.Fatalf("ValidateRule(enabled=%t) error = %v", enabled, err)
                    }
                    continue
                }
                if !errors.Is(err, ErrInvalidRule) || err.Error() != "invalid privacy rule" {
                    t.Fatalf("ValidateRule(enabled=%t) error = %v, want fixed ErrInvalidRule", enabled, err)
                }
                snapshot, compileErr := Compile(Config{Enabled: true, Scopes: Messages, FailurePolicy: Continue}, []Rule{rule}, 1)
                if snapshot != nil || !errors.Is(compileErr, ErrInvalidRule) || compileErr.Error() != "invalid privacy rule" {
                    t.Fatalf("invalid Compile(enabled=%t) = (%v, %v), want nil and fixed ErrInvalidRule", enabled, snapshot, compileErr)
                }
            }
        })
    }
}

func TestPrivacyTypesConfigValidation(t *testing.T) {
    for scopes := Scope(0); scopes <= AllScopes; scopes++ {
        for _, enabled := range []bool{false, true} {
            for _, policy := range []FailurePolicy{Continue, Abort} {
                config := Config{Enabled: enabled, Scopes: scopes, FailurePolicy: policy}
                if err := ValidateConfig(config); err != nil {
                    t.Fatalf("ValidateConfig(%+v) error = %v", config, err)
                }
                snapshot := privacyTestCompile(t, config, nil, 5)
                if snapshot.Config() != config || snapshot.Version() != 5 || snapshot.Active() {
                    t.Fatalf("empty snapshot does not preserve config/version or is active: %+v", config)
                }
            }
        }
    }
    invalid := []Config{
        {Enabled: true, Scopes: 16, FailurePolicy: Continue},
        {Enabled: true, Scopes: AllScopes | 16, FailurePolicy: Continue},
        {Scopes: 128, FailurePolicy: Abort},
        {Scopes: Messages},
        {Scopes: Messages, FailurePolicy: "CONTINUE"},
        {Scopes: Messages, FailurePolicy: "continue "},
        {Scopes: Messages, FailurePolicy: "private-policy"},
    }
    for _, config := range invalid {
        err := ValidateConfig(config)
        if !errors.Is(err, ErrInvalidConfig) || err.Error() != "invalid privacy configuration" {
            t.Fatalf("ValidateConfig(%+v) error = %v, want fixed ErrInvalidConfig", config, err)
        }
        snapshot, err := Compile(config, nil, 1)
        if snapshot != nil || !errors.Is(err, ErrInvalidConfig) || err.Error() != "invalid privacy configuration" {
            t.Fatalf("invalid config Compile() = (%v, %v), want nil and fixed ErrInvalidConfig", snapshot, err)
        }
    }
}

func TestPrivacyTypesDoubleUnique(t *testing.T) {
    for _, firstEnabled := range []bool{false, true} {
        for _, secondEnabled := range []bool{false, true} {
            fixtures := []struct {
                name  string
                rules []Rule
            }{
                {"source", []Rule{
                    {ID: 1, Source: "private-source", Alias: "FIRST", Enabled: firstEnabled},
                    {ID: 2, Source: "private-source", Alias: "SECOND", Enabled: secondEnabled},
                }},
                {"alias", []Rule{
                    {ID: 1, Source: "private-source", Alias: "SAME", Enabled: firstEnabled},
                    {ID: 2, Source: "second-private-source", Alias: "SAME", Enabled: secondEnabled},
                }},
                {"both", []Rule{
                    {ID: 1, Source: "private-source", Alias: "SAME", Enabled: firstEnabled},
                    {ID: 2, Source: "private-source", Alias: "SAME", Enabled: secondEnabled},
                }},
            }
            for _, fixture := range fixtures {
                t.Run(fmt.Sprintf("%s_%t_%t", fixture.name, firstEnabled, secondEnabled), func(t *testing.T) {
                    // 全表双唯一也在功能关闭和范围关闭时检查.
                    for _, config := range []Config{
                        {Enabled: true, Scopes: Messages, FailurePolicy: Continue},
                        {Scopes: Messages, FailurePolicy: Continue},
                        {Enabled: true, FailurePolicy: Continue},
                    } {
                        snapshot, err := Compile(config, fixture.rules, 1)
                        if snapshot != nil || !errors.Is(err, ErrRuleConflict) || err.Error() != "privacy rule conflict" {
                            t.Fatalf("duplicate Compile() = (%v, %v), want nil and fixed ErrRuleConflict", snapshot, err)
                        }
                    }
                })
            }
        }
    }
    t.Run("raw_byte_distinctions", func(t *testing.T) {
        rules := []Rule{
            {Source: "a", Alias: "LOWER", Enabled: true},
            {Source: "A", Alias: "UPPER", Enabled: true},
            {Source: "é", Alias: "NFC", Enabled: true},
            {Source: "e\u0301", Alias: "NFD", Enabled: true},
            {Source: "尾随", Alias: "NO_SPACE", Enabled: false},
            {Source: "尾随 ", Alias: "WITH_SPACE", Enabled: false},
            {Source: "\x00", Alias: "NUL", Enabled: false},
        }
        snapshot := privacyTestCompile(t, Config{Enabled: true, Scopes: Messages, FailurePolicy: Continue}, rules, 1)
        if snapshot.RuleCount() != 7 {
            t.Fatalf("RuleCount() = %d, want 7", snapshot.RuleCount())
        }
        privacyTestReplace(t, snapshot, "a/A/é/e\u0301/尾随 /\x00", "[[OCTOPUS_PII:LOWER]]/[[OCTOPUS_PII:UPPER]]/[[OCTOPUS_PII:NFC]]/[[OCTOPUS_PII:NFD]]/尾随 /\x00", true)
    })
}

func TestPrivacyTypesSnapshotCopies(t *testing.T) {
    config := Config{Enabled: true, Scopes: Messages | ToolResponses, FailurePolicy: Continue}
    rules := []Rule{
        {ID: 2, Source: "13969909666", Alias: "PHONE_1", Enabled: true},
        {ID: 1, Source: "张三", Alias: "LONG_NAME", Enabled: false},
    }
    old := privacyTestCompile(t, config, rules, 73)
    returnedConfig := old.Config()
    returnedConfig.Enabled = false
    returnedConfig.Scopes = 0
    returnedConfig.FailurePolicy = Abort
    returnedRule, ok := old.Rule(0)
    if !ok {
        t.Fatal("Rule(0) not found")
    }
    returnedRule.ID = 999
    returnedRule.Source = "外部修改"
    returnedRule.Alias = "OUTSIDE"
    returnedRule.Enabled = false
    config = Config{Enabled: true, Scopes: ToolCalls, FailurePolicy: Abort}
    rules[0] = Rule{ID: 200, Source: "新原文", Alias: "NEW", Enabled: true}
    rules[1].Enabled = true
    rules = append(rules, Rule{ID: 300, Source: "另一个", Alias: "OTHER", Enabled: true})
    newer := privacyTestCompile(t, config, rules, 74)

    wantConfig := Config{Enabled: true, Scopes: Messages | ToolResponses, FailurePolicy: Continue}
    if old.Config() != wantConfig || old.Version() != 73 || old.RuleCount() != 2 {
        t.Fatalf("old snapshot changed: config=%+v, version=%d, rules=%d", old.Config(), old.Version(), old.RuleCount())
    }
    wantRules := []Rule{
        {ID: 2, Source: "13969909666", Alias: "PHONE_1", Enabled: true},
        {ID: 1, Source: "张三", Alias: "LONG_NAME", Enabled: false},
    }
    for index, want := range wantRules {
        got, ok := old.Rule(index)
        if !ok || got != want {
            t.Fatalf("old Rule(%d) = (%+v, %t), want (%+v, true)", index, got, ok, want)
        }
    }
    for _, index := range []int{-1, 2, 100} {
        if got, ok := old.Rule(index); ok || got != (Rule{}) {
            t.Fatalf("Rule(%d) = (%+v, %t), want zero and false", index, got, ok)
        }
    }
    if !reflect.DeepEqual(old.patterns, []string{"13969909666"}) ||
        !reflect.DeepEqual(old.markers, []string{"[[OCTOPUS_PII:PHONE_1]]"}) ||
        !reflect.DeepEqual(old.restoreMap, map[string]string{"[[OCTOPUS_PII:PHONE_1]]": "13969909666"}) || old.maxMarkerLength != 23 {
        t.Fatal("old snapshot matcher tables, restore map or L changed")
    }
    if !old.Active() || !old.Processes(Messages) || !old.Processes(ToolResponses) || old.Processes(ToolCalls) {
        t.Fatal("old snapshot scope activation changed")
    }
    privacyTestReplace(t, old, "13969909666/新原文/张三", "[[OCTOPUS_PII:PHONE_1]]/新原文/张三", true)
    privacyTestRestoreParts(t, old, []string{"[[OCTOPUS_PII:PHONE_1]]/[[OCTOPUS_PII:LONG_NAME]]"}, "13969909666/[[OCTOPUS_PII:LONG_NAME]]", true)
    if newer.Config() != config || newer.Version() != 74 || newer.RuleCount() != 3 || !newer.Processes(ToolCalls) || newer.Processes(Messages) {
        t.Fatal("new snapshot did not retain its own config, version and rules")
    }
    privacyTestReplace(t, newer, "13969909666/新原文/张三/另一个", "13969909666/[[OCTOPUS_PII:NEW]]/[[OCTOPUS_PII:LONG_NAME]]/[[OCTOPUS_PII:OTHER]]", true)
    privacyTestRestoreParts(t, newer, []string{"[[OCTOPUS_PII:PHONE_1]]/[[OCTOPUS_PII:NEW]]"}, "[[OCTOPUS_PII:PHONE_1]]/新原文", true)
    if returnedConfig.Enabled || returnedRule.Enabled || returnedRule.ID != 999 {
        t.Fatal("test did not mutate returned value copies")
    }
}

func TestPrivacyTypesActiveAndProcesses(t *testing.T) {
    var nilSnapshot *Snapshot
    if nilSnapshot.Active() || nilSnapshot.Processes(Messages) || nilSnapshot.Processes(AllScopes) {
        t.Fatal("nil snapshot is active or processes a scope")
    }
    rules := []Rule{{Source: "13969909666", Alias: "PHONE_1", Enabled: true}}
    for scopes := Scope(0); scopes <= AllScopes; scopes++ {
        snapshot := privacyTestCompile(t, Config{Enabled: true, Scopes: scopes, FailurePolicy: Continue}, rules, 1)
        if snapshot.Active() != (scopes != 0) {
            t.Fatalf("scopes %d Active() = %t", scopes, snapshot.Active())
        }
        for _, selected := range []Scope{0, Messages, ToolDefinitions, ToolCalls, ToolResponses, Messages | ToolCalls, AllScopes, 16} {
            want := scopes != 0 && scopes&selected != 0
            if got := snapshot.Processes(selected); got != want {
                t.Fatalf("scopes %d Processes(%d) = %t, want %t", scopes, selected, got, want)
            }
        }
    }
    empty := privacyTestCompile(t, DefaultConfig(), nil, 0)
    if got, ok := empty.Rule(0); ok || got != (Rule{}) || empty.RuleCount() != 0 {
        t.Fatalf("empty Rule(0) = (%+v, %t), count = %d", got, ok, empty.RuleCount())
    }
}

func TestPrivacyTypesFixedErrors(t *testing.T) {
    fixtures := []struct {
        err  error
        kind ErrorKind
        want string
    }{
        {ErrCompile, ErrorCompile, "privacy processing failed: compile"},
        {ErrInvalidJSON, ErrorInvalidJSON, "privacy processing failed: invalid_json"},
        {ErrUnsupportedContent, ErrorUnsupportedContent, "privacy processing failed: unsupported_content"},
        {ErrMatch, ErrorMatch, "privacy processing failed: match"},
        {ErrStreamIdentity, ErrorStreamIdentity, "privacy processing failed: stream_identity"},
        {ErrResourceLimit, ErrorResourceLimit, "privacy processing failed: resource_limit"},
    }
    for _, fixture := range fixtures {
        if fixture.err.Error() != fixture.want {
            t.Fatalf("error = %q, want %q", fixture.err.Error(), fixture.want)
        }
        wrapped := fmt.Errorf("component: %w", fixture.err)
        var privacyError *PrivacyError
        if !errors.Is(wrapped, fixture.err) || !errors.As(wrapped, &privacyError) || privacyError.Kind() != fixture.kind {
            t.Fatalf("fixed error identity/kind not preserved: %v", wrapped)
        }
        for _, sensitive := range []string{"13969909666", "张三", "private-source", "PHONE_1"} {
            if strings.Contains(fixture.err.Error(), sensitive) {
                t.Fatalf("error contains sensitive fixture: %q", fixture.err.Error())
            }
        }
    }
    for _, fixture := range []struct {
        err  error
        want string
    }{
        {ErrInvalidRule, "invalid privacy rule"},
        {ErrInvalidConfig, "invalid privacy configuration"},
        {ErrRuleConflict, "privacy rule conflict"},
        {ErrRuleNotFound, "privacy rule not found"},
    } {
        if fixture.err.Error() != fixture.want {
            t.Fatalf("error = %q, want %q", fixture.err.Error(), fixture.want)
        }
    }
}
