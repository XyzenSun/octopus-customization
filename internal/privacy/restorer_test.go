package privacy

import (
    "strings"
    "testing"
)

func privacyTestRestoreParts(t *testing.T, snapshot *Snapshot, parts []string, want string, wantChanged bool) {
    t.Helper()
    restorer := NewRestorer(snapshot)
    var output strings.Builder
    changed := false
    for _, part := range parts {
        text, partChanged := restorer.Push(part)
        output.WriteString(text)
        changed = changed || partChanged
    }
    tail, tailChanged := restorer.Finish()
    output.WriteString(tail)
    changed = changed || tailChanged
    if output.String() != want || changed != wantChanged {
        t.Fatalf("restore parts %q = (%q, %t), want (%q, %t)", parts, output.String(), changed, want, wantChanged)
    }
    if restorer.HasPending() {
        t.Fatalf("HasPending() after Finish() for parts %q", parts)
    }
    for repeat := 0; repeat < 2; repeat++ {
        text, changed := restorer.Finish()
        if text != "" || changed {
            t.Fatalf("repeated Finish() = (%q, %t), want empty and false", text, changed)
        }
    }
}

func privacyTestRestoreCuts(t *testing.T, snapshot *Snapshot, input, want string, wantChanged bool) {
    t.Helper()
    privacyTestRestoreParts(t, snapshot, []string{input}, want, wantChanged)
    // 按字节枚举, 允许空片和 UTF-8 字符中间的切点.
    for first := 0; first <= len(input); first++ {
        privacyTestRestoreParts(t, snapshot, []string{input[:first], input[first:]}, want, wantChanged)
        for second := first; second <= len(input); second++ {
            privacyTestRestoreParts(t, snapshot, []string{input[:first], input[first:second], input[second:]}, want, wantChanged)
        }
    }
    parts := []string{""}
    for index := 0; index < len(input); index++ {
        parts = append(parts, input[index:index+1])
    }
    parts = append(parts, "")
    privacyTestRestoreParts(t, snapshot, parts, want, wantChanged)
}

func TestPrivacyRestorer(t *testing.T) {
    snapshot := privacyTestCompile(t, Config{Enabled: true, Scopes: Messages, FailurePolicy: Continue}, []Rule{
        {Source: "13969909666", Alias: "PHONE_1", Enabled: true},
        {Source: "张三", Alias: "NAME", Enabled: true},
        {Source: "张三丰", Alias: "LONG_NAME", Enabled: true},
        {Source: "a", Alias: "LOWER", Enabled: true},
        {Source: "ab", Alias: "AB", Enabled: true},
        {Source: "bc", Alias: "BC", Enabled: true},
        {Source: "A", Alias: "UPPER", Enabled: true},
        {Source: "é", Alias: "NFC", Enabled: true},
        {Source: "e\u0301", Alias: "NFD", Enabled: true},
        {Source: "尾随 ", Alias: "TAIL", Enabled: true},
        {Source: "x\x00y", Alias: "NUL", Enabled: true},
        {Source: "行\"一\n路径\\尾", Alias: "ESCAPED", Enabled: true},
        {Source: "[[OCTOPUS_PII:PHONE_1]]", Alias: "NESTED", Enabled: true},
        {Source: "[[OCTOPUS_PII:SELF]]", Alias: "SELF", Enabled: true},
        {Source: "最大原文", Alias: "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_ABCDEFGHIJKLMNOPQRSTUVWXYZ0", Enabled: true},
        {Source: "停用原文", Alias: "DISABLED", Enabled: false},
    }, 7)
    fixtures := []struct {
        name    string
        input   string
        want    string
        changed bool
    }{
        {"empty", "", "", false},
        {"literal", "前é/e\u0301后\"\n\\", "前é/e\u0301后\"\n\\", false},
        {"phone", "[[OCTOPUS_PII:PHONE_1]]", "13969909666", true},
        {"name", "[[OCTOPUS_PII:NAME]]", "张三", true},
        {"long_name", "[[OCTOPUS_PII:LONG_NAME]]", "张三丰", true},
        {"lower", "[[OCTOPUS_PII:LOWER]]", "a", true},
        {"ab", "[[OCTOPUS_PII:AB]]", "ab", true},
        {"bc", "[[OCTOPUS_PII:BC]]", "bc", true},
        {"upper", "[[OCTOPUS_PII:UPPER]]", "A", true},
        {"nfc", "[[OCTOPUS_PII:NFC]]", "é", true},
        {"nfd", "[[OCTOPUS_PII:NFD]]", "e\u0301", true},
        {"trailing_space", "[[OCTOPUS_PII:TAIL]]", "尾随 ", true},
        {"nul", "[[OCTOPUS_PII:NUL]]", "x\x00y", true},
        {"quotes_newline_backslash_utf8", "前é/[[OCTOPUS_PII:ESCAPED]]/e\u0301后", "前é/行\"一\n路径\\尾/e\u0301后", true},
        {"non_recursive_source", "[[OCTOPUS_PII:NESTED]]", "[[OCTOPUS_PII:PHONE_1]]", true},
        {"equal_source", "[[OCTOPUS_PII:SELF]]", "[[OCTOPUS_PII:SELF]]", false},
        {"maximum_L_minus_1", "[[OCTOPUS_PII:ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_ABCDEFGHIJKLMNOPQRSTUVWXYZ0]", "[[OCTOPUS_PII:ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_ABCDEFGHIJKLMNOPQRSTUVWXYZ0]", false},
        {"maximum_alias", "[[OCTOPUS_PII:ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_ABCDEFGHIJKLMNOPQRSTUVWXYZ0]]", "最大原文", true},
        {"maximum_L_plus_1", "[[OCTOPUS_PII:ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_ABCDEFGHIJKLMNOPQRSTUVWXYZ0]]]", "最大原文]", true},
        {"disabled_alias", "[[OCTOPUS_PII:DISABLED]]", "[[OCTOPUS_PII:DISABLED]]", false},
        {"global_lookup", "原请求未产生:[[OCTOPUS_PII:PHONE_1]]", "原请求未产生:13969909666", true},
        {"repeated_adjacent", "[[OCTOPUS_PII:PHONE_1]][[OCTOPUS_PII:NAME]][[OCTOPUS_PII:PHONE_1]]", "13969909666张三13969909666", true},
        {"equal_and_changed", "[[OCTOPUS_PII:SELF]]/[[OCTOPUS_PII:PHONE_1]]", "[[OCTOPUS_PII:SELF]]/13969909666", true},
        {"unknown", "[[OCTOPUS_PII:UNKNOWN]]", "[[OCTOPUS_PII:UNKNOWN]]", false},
        {"lowercase_alias", "[[OCTOPUS_PII:phone_1]]", "[[OCTOPUS_PII:phone_1]]", false},
        {"empty_alias", "[[OCTOPUS_PII:]]", "[[OCTOPUS_PII:]]", false},
        {"hyphen_alias", "[[OCTOPUS_PII:PHONE-1]]", "[[OCTOPUS_PII:PHONE-1]]", false},
        {"unicode_alias", "[[OCTOPUS_PII:张三]]", "[[OCTOPUS_PII:张三]]", false},
        {"nul_alias", "[[OCTOPUS_PII:PHONE\x001]]", "[[OCTOPUS_PII:PHONE\x001]]", false},
        {"newline_alias", "[[OCTOPUS_PII:PHONE\n1]]", "[[OCTOPUS_PII:PHONE\n1]]", false},
        {"overlong_alias", "[[OCTOPUS_PII:ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_ABCDEFGHIJKLMNOPQRSTUVWXYZ01]]", "[[OCTOPUS_PII:ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_ABCDEFGHIJKLMNOPQRSTUVWXYZ01]]", false},
        {"overlong_then_known", "[[OCTOPUS_PII:ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_ABCDEFGHIJKLMNOPQRSTUVWXYZ01]][[OCTOPUS_PII:PHONE_1]]", "[[OCTOPUS_PII:ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_ABCDEFGHIJKLMNOPQRSTUVWXYZ01]]13969909666", true},
        {"wrong_right", "[[OCTOPUS_PII:PHONE_1]X]]", "[[OCTOPUS_PII:PHONE_1]X]]", false},
        {"partial_left", "前[[OCTOPUS_PI", "前[[OCTOPUS_PI", false},
        {"partial_alias", "[[OCTOPUS_PII:PHONE_", "[[OCTOPUS_PII:PHONE_", false},
        {"partial_right", "[[OCTOPUS_PII:PHONE_1]", "[[OCTOPUS_PII:PHONE_1]", false},
        {"overlapping_brackets", "[[[[OCTOPUS_PII:PHONE_1]]", "[[13969909666", true},
        {"failed_left_then_known", "[[OCTO[[OCTOPUS_PII:PHONE_1]]", "[[OCTO13969909666", true},
        {"invalid_candidate_then_known", "[[OCTOPUS_PII:BAD[[OCTOPUS_PII:PHONE_1]]", "[[OCTOPUS_PII:BAD13969909666", true},
        {"invalid_right_then_known", "[[OCTOPUS_PII:BAD][[OCTOPUS_PII:PHONE_1]]", "[[OCTOPUS_PII:BAD]13969909666", true},
        {"unknown_then_known", "[[OCTOPUS_PII:UNKNOWN]][[OCTOPUS_PII:NAME]]", "[[OCTOPUS_PII:UNKNOWN]]张三", true},
    }
    for _, fixture := range fixtures {
        t.Run(fixture.name, func(t *testing.T) {
            privacyTestRestoreCuts(t, snapshot, fixture.input, fixture.want, fixture.changed)
        })
    }
}

func TestPrivacyRestorerDelimiterFragments(t *testing.T) {
    snapshot := privacyTestCompile(t, Config{Enabled: true, Scopes: Messages, FailurePolicy: Continue}, []Rule{
        {Source: "13969909666", Alias: "PHONE_1", Enabled: true},
    }, 1)
    privacyTestRestoreParts(t, snapshot, []string{"[", "", "[OCT", "OPUS", "_PII", ":PHONE_", "1]", "", "]"}, "13969909666", true)
    privacyTestRestoreParts(t, snapshot, []string{"前[[OCTOPUS_", "PII:PHONE_1", "]", "]后"}, "前13969909666后", true)
}

func TestPrivacyRestorerLengthBoundary(t *testing.T) {
    snapshot := privacyTestCompile(t, Config{Enabled: true, Scopes: Messages, FailurePolicy: Continue}, []Rule{
        {Source: "边界原文", Alias: "MAX", Enabled: true},
        {Source: "短原文", Alias: "S", Enabled: true},
        {Source: "不参与L", Alias: "DISABLED_LONG_ALIAS", Enabled: false},
    }, 1)
    if snapshot.maxMarkerLength != 19 {
        t.Fatalf("snapshot L = %d, want 19", snapshot.maxMarkerLength)
    }
    fixtures := []struct {
        name    string
        input   string
        want    string
        changed bool
    }{
        {"L_minus_1", "[[OCTOPUS_PII:MAX]", "[[OCTOPUS_PII:MAX]", false},
        {"L_known", "[[OCTOPUS_PII:MAX]]", "边界原文", true},
        {"L_plus_1", "[[OCTOPUS_PII:MAX]]]", "边界原文]", true},
        {"L_incomplete_unknown", "[[OCTOPUS_PII:ABCDE", "[[OCTOPUS_PII:ABCDE", false},
        {"L_plus_1_incomplete_unknown", "[[OCTOPUS_PII:ABCDEF", "[[OCTOPUS_PII:ABCDEF", false},
        {"shorter_known", "[[OCTOPUS_PII:S]]", "短原文", true},
        {"shorter_unknown", "[[OCTOPUS_PII:Z]]", "[[OCTOPUS_PII:Z]]", false},
    }
    for _, fixture := range fixtures {
        t.Run(fixture.name, func(t *testing.T) {
            privacyTestRestoreCuts(t, snapshot, fixture.input, fixture.want, fixture.changed)
        })
    }
    t.Run("completion_precedes_L_release", func(t *testing.T) {
        restorer := NewRestorer(snapshot)
        got, changed := restorer.Push("[[OCTOPUS_PII:MAX]")
        if got != "" || changed || !restorer.HasPending() {
            t.Fatalf("L-1 Push() = (%q, %t), pending = %t", got, changed, restorer.HasPending())
        }
        got, changed = restorer.Push("]")
        if got != "边界原文" || !changed || restorer.HasPending() {
            t.Fatalf("L completion = (%q, %t), pending = %t", got, changed, restorer.HasPending())
        }
        if tail, changed := restorer.Finish(); tail != "" || changed {
            t.Fatalf("Finish() = (%q, %t), want empty and false", tail, changed)
        }
    })
    t.Run("incomplete_released_at_L", func(t *testing.T) {
        restorer := NewRestorer(snapshot)
        got, changed := restorer.Push("[[OCTOPUS_PII:ABCD")
        if got != "" || changed || !restorer.HasPending() {
            t.Fatalf("L-1 candidate = (%q, %t), pending = %t", got, changed, restorer.HasPending())
        }
        got, changed = restorer.Push("E")
        if got != "[[OCTOPUS_PII:ABCDE" || changed || restorer.HasPending() {
            t.Fatalf("L release = (%q, %t), pending = %t", got, changed, restorer.HasPending())
        }
        got, changed = restorer.Push("F]]")
        if got != "F]]" || changed || restorer.HasPending() {
            t.Fatalf("L+1 continuation = (%q, %t), pending = %t", got, changed, restorer.HasPending())
        }
        if tail, changed := restorer.Finish(); tail != "" || changed {
            t.Fatalf("Finish() = (%q, %t), want empty and false", tail, changed)
        }
    })
}

func TestPrivacyRestorerPrefixesAndFinish(t *testing.T) {
    snapshot := privacyTestCompile(t, Config{Enabled: true, Scopes: Messages, FailurePolicy: Continue}, []Rule{
        {Source: "13969909666", Alias: "PHONE_1", Enabled: true},
    }, 1)
    // 所有 LEFT 前缀在 EOF 都只能字面释放, 不能补全为 marker.
    left := "[[OCTOPUS_PII:"
    for length := 0; length <= len(left); length++ {
        prefix := left[:length]
        restorer := NewRestorer(snapshot)
        got, changed := restorer.Push(prefix)
        if got != "" || changed || restorer.HasPending() != (length > 0) {
            t.Fatalf("prefix %q Push() = (%q, %t), pending = %t", prefix, got, changed, restorer.HasPending())
        }
        got, changed = restorer.Finish()
        if got != prefix || changed || restorer.HasPending() {
            t.Fatalf("prefix %q Finish() = (%q, %t), pending = %t", prefix, got, changed, restorer.HasPending())
        }
        privacyTestRestoreCuts(t, snapshot, prefix, prefix, false)
    }
    restorer := NewRestorer(snapshot)
    if got, changed := restorer.Push("前[[OCTOPUS_PII:PHONE_1]"); got != "前" || changed || !restorer.HasPending() {
        t.Fatalf("unfinished Push() = (%q, %t), pending = %t", got, changed, restorer.HasPending())
    }
    if got, changed := restorer.Push(""); got != "" || changed || !restorer.HasPending() {
        t.Fatalf("empty Push() lost candidate: (%q, %t), pending = %t", got, changed, restorer.HasPending())
    }
    if got, changed := restorer.Finish(); got != "[[OCTOPUS_PII:PHONE_1]" || changed || restorer.HasPending() {
        t.Fatalf("Finish() = (%q, %t), pending = %t", got, changed, restorer.HasPending())
    }
    // 结束后不重开. 分片拼出完整 marker 也只能逐片字面返回.
    for _, input := range []string{"", "]", "[[OCTOPUS_PII:PHONE_1]]", "[[OCTOPUS_", "PII:PHONE_1]]", "前é\"\n\\后"} {
        got, changed := restorer.Push(input)
        if got != input || changed || restorer.HasPending() {
            t.Fatalf("Push(%q) after Finish() = (%q, %t), pending = %t", input, got, changed, restorer.HasPending())
        }
        if got, changed := restorer.Finish(); got != "" || changed {
            t.Fatalf("Finish() after late Push() = (%q, %t), want empty and false", got, changed)
        }
    }
    t.Run("finish_before_any_input", func(t *testing.T) {
        restorer := NewRestorer(snapshot)
        if got, changed := restorer.Finish(); got != "" || changed || restorer.HasPending() {
            t.Fatalf("initial Finish() = (%q, %t)", got, changed)
        }
        if got, changed := restorer.Push("[[OCTOPUS_PII:PHONE_1]]"); got != "[[OCTOPUS_PII:PHONE_1]]" || changed || restorer.HasPending() {
            t.Fatalf("Push() reopened finished restorer: (%q, %t)", got, changed)
        }
    })
}

func TestPrivacyRestorerInactive(t *testing.T) {
    fixtures := []struct {
        name   string
        config Config
        rules  []Rule
    }{
        {"config_disabled", Config{Scopes: Messages, FailurePolicy: Continue}, []Rule{{Source: "13969909666", Alias: "PHONE_1", Enabled: true}}},
        {"empty_map", Config{Enabled: true, Scopes: Messages, FailurePolicy: Continue}, nil},
        {"disabled_rules", Config{Enabled: true, Scopes: Messages, FailurePolicy: Continue}, []Rule{{Source: "13969909666", Alias: "PHONE_1"}}},
        {"scopes_zero", Config{Enabled: true, FailurePolicy: Continue}, []Rule{{Source: "13969909666", Alias: "PHONE_1", Enabled: true}}},
    }
    for _, fixture := range fixtures {
        t.Run(fixture.name, func(t *testing.T) {
            snapshot := privacyTestCompile(t, fixture.config, fixture.rules, 1)
            restorer := NewRestorer(snapshot)
            for _, input := range []string{"", "[[OCTOPUS_", "PII:PHONE_1]]", "[[OCTOPUS_PII:PHONE_1]]", "前é\"\n\\后"} {
                got, changed := restorer.Push(input)
                if got != input || changed || restorer.HasPending() {
                    t.Fatalf("inactive Push(%q) = (%q, %t), pending = %t", input, got, changed, restorer.HasPending())
                }
            }
            if got, changed := restorer.Finish(); got != "" || changed || restorer.HasPending() {
                t.Fatalf("inactive Finish() = (%q, %t)", got, changed)
            }
            privacyTestRestoreCuts(t, snapshot, "[[OCTOPUS_PII:PHONE_1]]", "[[OCTOPUS_PII:PHONE_1]]", false)
        })
    }
    t.Run("nil_snapshot", func(t *testing.T) {
        privacyTestRestoreCuts(t, nil, "前[[OCTOPUS_PII:PHONE_1]]后", "前[[OCTOPUS_PII:PHONE_1]]后", false)
    })
    t.Run("non_message_scope_is_active", func(t *testing.T) {
        snapshot := privacyTestCompile(t, Config{Enabled: true, Scopes: ToolCalls, FailurePolicy: Abort}, []Rule{
            {Source: "13969909666", Alias: "PHONE_1", Enabled: true},
        }, 1)
        privacyTestRestoreCuts(t, snapshot, "[[OCTOPUS_PII:PHONE_1]]", "13969909666", true)
    })
}
