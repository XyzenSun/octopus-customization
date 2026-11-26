package privacy

import (
    "encoding/json"
    "strings"
    "testing"
    "unicode/utf8"
)

func privacyJSONStreamSnapshot(t *testing.T) *Snapshot {
    t.Helper()
    return privacyTestCompile(t, Config{Enabled: true, Scopes: ToolCalls, FailurePolicy: Continue}, []Rule{
        {Source: "13969909666", Alias: "PHONE_1", Enabled: true},
        {Source: "张三", Alias: "NAME", Enabled: true},
        {Source: "张三丰", Alias: "LONG_NAME", Enabled: true},
        {Source: "行\"一\n路径\\尾", Alias: "ESCAPED", Enabled: true},
        {Source: "x\x00y", Alias: "NUL", Enabled: true},
        {Source: "[[OCTOPUS_PII:PHONE_1]]", Alias: "NESTED", Enabled: true},
        {Source: "[[OCTOPUS_PII:SELF]]", Alias: "SELF", Enabled: true},
        {Source: "disabled", Alias: "DISABLED", Enabled: false},
    }, 1)
}

// Owners deliberately have nil events: only the streamField's output builder
// is needed here. Event/FIFO reference accounting is exercised by the parent
// stream tests; these tests inspect the parser's own terminal references too.
func privacyJSONStreamParts(t *testing.T, snapshot *Snapshot, parts []string, want string, allowIncomplete bool, wantParts []string) {
    t.Helper()
    s := newJSONStream(snapshot)
    owners := make([]*streamField, len(parts))
    for i, part := range parts {
        owners[i] = &streamField{}
        if err := s.push(part, owners[i]); err != nil {
            t.Fatalf("push part %d of %q: %v", i, parts, err)
        }
    }
    if err := s.finish(allowIncomplete); err != nil {
        t.Fatalf("finish(%t), parts %q: %v", allowIncomplete, parts, err)
    }
    var out strings.Builder
    for i, owner := range owners {
        got := owner.output.String()
        if wantParts != nil && got != wantParts[i] {
            t.Fatalf("owner %d = %q, want %q; input parts %q", i, got, wantParts[i], parts)
        }
        out.WriteString(got)
    }
    if out.String() != want {
        t.Fatalf("parts %q = %q, want %q", parts, out.String(), want)
    }
    if s.pendingBytes() != 0 || s.restorer != nil || s.snapshot != nil || s.pieces != nil || s.stack != nil {
        t.Fatal("finished JSON stream retained pending data/references")
    }
    for i := 0; i < 2; i++ {
        if err := s.finish(allowIncomplete); err != nil || s.pendingBytes() != 0 {
            t.Fatalf("repeated finish = %v, pending %d", err, s.pendingBytes())
        }
    }
    if s.sawBytes && !allowIncomplete && !json.Valid([]byte(want)) {
        t.Fatalf("complete output is not valid JSON: %q", want)
    }
}

func privacyJSONStreamCuts(t *testing.T, snapshot *Snapshot, input, want string, threeCuts bool) {
    t.Helper()
    check := func(parts []string) {
        var wantParts []string
        if input == want {
            wantParts = parts // Even split UTF-8/escapes must keep their owners.
        }
        privacyJSONStreamParts(t, snapshot, parts, want, false, wantParts)
    }
    check([]string{input})
    for first := 0; first <= len(input); first++ {
        check([]string{input[:first], input[first:]})
        if threeCuts {
            for second := first; second <= len(input); second++ {
                check([]string{input[:first], input[first:second], input[second:]})
            }
        }
    }
    parts := []string{""}
    for i := range len(input) {
        parts = append(parts, input[i:i+1])
    }
    parts = append(parts, "")
    check(parts)
}

func TestPrivacyJSONStream(t *testing.T) {
    snapshot := privacyJSONStreamSnapshot(t)
    fixtures := []struct {
        name, input, want string
        threeCuts         bool
    }{
        {"key_value", `{"[[OCTOPUS_PII:PHONE_1]]":"[[OCTOPUS_PII:PHONE_1]]","id":"[[OCTOPUS_PII:NAME]]","name":"[[OCTOPUS_PII:LONG_NAME]]","n":9007199254740993}`, `{"[[OCTOPUS_PII:PHONE_1]]":"13969909666","id":"张三","name":"张三丰","n":9007199254740993}`, false},
        {"escaped_marker", `{"v":"\u005b\u005BOCTOPUS_PII\u003aPHONE_1\u005d\u005D"}`, `{"v":"13969909666"}`, true},
        {"escaped_replacement", `{"v":"前[[OCTOPUS_PII:ESCAPED]]后"}`, `{"v":"前行\"一\n路径\\尾后"}`, true},
        {"surrogate_and_utf8", `{"\uD83D\uDE00":"张\uD83d\ude00é/é[[OCTOPUS_PII:NAME]]"}`, `{"\uD83D\uDE00":"张\uD83d\ude00é/é张三"}`, true},
        {"all_escapes_literal", `{"\"\\\/\b\f\n\r\t\u0000":"\"\\\/\b\f\n\r\t\u0000\uFFFF\uFFFD😀"}`, `{"\"\\\/\b\f\n\r\t\u0000":"\"\\\/\b\f\n\r\t\u0000\uFFFF\uFFFD😀"}`, true},
        {"unknown_lexical", `{"\u005b[OCTOPUS_PII:NAME]]":"\u005b[OCTOPUS_PII:UNKNOWN]]/[[OCTOPUS_PII:bad]]/[[OCTOPUS_PII:DISABLED]]"}`, `{"\u005b[OCTOPUS_PII:NAME]]":"\u005b[OCTOPUS_PII:UNKNOWN]]/[[OCTOPUS_PII:bad]]/[[OCTOPUS_PII:DISABLED]]"}`, false},
        {"independent_values", `["[[OCTOPUS_","PII:PHONE_1]]",{"a":"[[OCTOPUS_PII:PH","b":"ONE_1]]"},"[[OCTOPUS_PII:NAME]]"]`, `["[[OCTOPUS_","PII:PHONE_1]]",{"a":"[[OCTOPUS_PII:PH","b":"ONE_1]]"},"张三"]`, false},
        {"partial_at_quote", `["[[OCTOPUS_PII:PHONE_1]","[[OCTOPUS_PII:NAME]]"]`, `["[[OCTOPUS_PII:PHONE_1]","张三"]`, true},
        {"nonrecursive", `"[[OCTOPUS_PII:NESTED]]"`, `"[[OCTOPUS_PII:PHONE_1]]"`, true},
        {"equal_source", `"[[OCTOPUS_PII:SELF]]"`, `"[[OCTOPUS_PII:SELF]]"`, false},
        {"nul", `"[[OCTOPUS_PII:NUL]]"`, `"x\u0000y"`, true},
        {"overlapping_prefix", `"[[[[OCTOPUS_PII:NAME]]"`, `"[[张三"`, true},
        {"escaped_slash_does_not_decode_again", `"\\u005b\\u005bOCTOPUS_PII:PHONE_1]]"`, `"\\u005b\\u005bOCTOPUS_PII:PHONE_1]]"`, true},
    }
    for _, fixture := range fixtures {
        t.Run(fixture.name, func(t *testing.T) {
            privacyJSONStreamCuts(t, snapshot, fixture.input, fixture.want, fixture.threeCuts)
        })
    }
}

func TestPrivacyJSONStreamGrammar(t *testing.T) {
    snapshot := privacyJSONStreamSnapshot(t)
    valid := []string{
        "", `{}`, `[]`, `""`, `true`, `false`, `null`, `0`, `-0`, `1`, `-123`, `0.0`, `-0.123`,
        `1e0`, `1E+2`, `-1.2e-30`, `1234567890123456789012345678901234567890`,
        `[0,-0,12,-34,0.12,1e2,1E+2,1E-2,true,false,null,"x",{},[]]`,
        `{"a":[],"a":{},"b":[{"c":{"d":false}},null],"empty":""}`,
        "{\"\u0080\u07ff\u0800\ud7ff\ue000\ufffd\U00010000\U0010ffff\":\"\u0080\u07ff\u0800\ud7ff\ue000\ufffd\U00010000\U0010ffff\"}",
        " \t\r\n{ \"a\" \t: [ 1 , true , { } ] }\r\n ",
    }
    for _, input := range valid {
        t.Run(input, func(t *testing.T) {
            privacyJSONStreamCuts(t, snapshot, input, input, false)
        })
    }
}

func TestPrivacyJSONStreamOwners(t *testing.T) {
    snapshot := privacyJSONStreamSnapshot(t)
    privacyJSONStreamParts(t, snapshot,
        []string{`{"v":"pre\u00`, `5b[OCTOPUS_PII:PH`, `ONE_1]]post"}`},
        `{"v":"pre13969909666post"}`, false,
        []string{`{"v":"pre13969909666`, "", `post"}`})
    privacyJSONStreamParts(t, snapshot,
        []string{`{"v":"[[OCTOPUS_`, `PII:ESC`, `APED]]","n":1}`},
        `{"v":"行\"一\n路径\\尾","n":1}`, false,
        []string{`{"v":"行\"一\n路径\\尾`, "", `","n":1}`})
    for _, parts := range [][]string{
        {`{"v":"\u00`, `5b[OCTOPUS_PII:UNKNOWN`, `]]"}`},
        {`{"v":"\uD8`, `3D\u`, `DE00"}`},
        {"{\"v\":\"\xe5", "\xbc", "\xa0\"}"},
        {`{"\uD8`, `3D\u`, `DE00":true}`},
    } {
        privacyJSONStreamParts(t, snapshot, parts, strings.Join(parts, ""), false, parts)
    }
    // Multiple pushes can refer to the same field; temporary retain/release
    // must not change the order within that owner's builder.
    s := newJSONStream(snapshot)
    owner := &streamField{}
    for _, part := range []string{`["\u00`, `5b[OCTOPUS_`, `PII:PHONE_1]]","\uD83D`, `\uDE00"]`} {
        if err := s.push(part, owner); err != nil {
            t.Fatal(err)
        }
    }
    if err := s.finish(false); err != nil || owner.output.String() != `["13969909666","\uD83D\uDE00"]` {
        t.Fatalf("same owner output %q, error %v", owner.output.String(), err)
    }
}

func TestPrivacyJSONStreamIncomplete(t *testing.T) {
    snapshot := privacyJSONStreamSnapshot(t)
    prefixes := []string{
        "", " \t", `{`, `[`, `{"a"`, `{"a":`, `{"a":1,`, `[true,`,
        `t`, `tr`, `tru`, `fals`, `nul`, `-`, `1.`, `1e`, `1e+`, `1e-`,
        `"`, `"x`, `"\`, `"\u`, `"\u0`, `"\u00`, `"\u005`,
        `"\uD800`, `"\uD800\`, `"\uD800\u`, `"\uD800\uD`, `"\uD800\uDC`, `"\uD800\uDC0`,
        "\"\xe5", "\"\xe5\xbc", "\"\xf0\x9f\x98",
        `{"v":"[[OCTOPUS_PII:PHONE_1]`, `{"v":"[[OCTOPUS_PII:PHO\u0`,
    }
    for _, input := range prefixes {
        t.Run(input, func(t *testing.T) {
            for cut := 0; cut <= len(input); cut++ {
                parts := []string{input[:cut], "", input[cut:]}
                privacyJSONStreamParts(t, snapshot, parts, input, true, parts)
                s := newJSONStream(snapshot)
                for _, part := range parts {
                    if err := s.push(part, &streamField{}); err != nil {
                        t.Fatalf("legal prefix push: %v", err)
                    }
                }
                wantErr := error(ErrInvalidJSON)
                if input == "" {
                    wantErr = nil
                }
                if err := s.finish(false); err != wantErr {
                    t.Fatalf("strict finish %q = %v, want %v", input, err, wantErr)
                }
                if err := s.finish(false); err != wantErr || s.pendingBytes() != 0 {
                    t.Fatalf("repeated strict finish = %v, pending %d", err, s.pendingBytes())
                }
            }
        })
    }
    // Restored complete markers before EOF stay restored; no closing quote is
    // synthesized and the trailing unfinished escape remains at its owner.
    privacyJSONStreamParts(t, snapshot,
        []string{`{"v":"[[OCTOPUS_PII:PH`, `ONE_1]]\u0`},
        `{"v":"13969909666\u0`, true,
        []string{`{"v":"13969909666`, `\u0`})
}

func TestPrivacyJSONStreamInvalid(t *testing.T) {
    snapshot := privacyJSONStreamSnapshot(t)
    invalid := []string{
        `}{`, `]`, `{]`, `[}`, `{"a" 1}`, `{"a":}`, `{:1}`, `{1:2}`, `{"a"}`, `{"a":1,}`, `[1,]`,
        `[,1]`, `[1 2]`, `{"a":1 "b":2}`, `{}[]`, `truefalse`, `"a" "b"`, `null x`,
        `[+1]`, `[01]`, `[-01]`, `[-]`, `[.1]`, `[1.]`, `[1e]`, `[1e+]`, `[1e-]`, `[1e+-2]`, `[1..2]`,
        `[True]`, `[FALSE]`, `[undefined]`, `[NaN]`, `[Infinity]`, `[truX]`,
        `/* comment */{}`, `{}// comment`, "\ufeff{}", "{}\u00a0",
        `"\x61"`, `"\a"`, `"\u00x0"`, `"\uDC00"`, `"\uD800"`, `"\uD800x"`,
        `"\uD800\n"`, `"\uD800\u0041"`, `"\uD800\uD800"`, `"\uD800\uDBFF"`,
        `{"\uDC00":0}`, `{"\uD800":0}`, `{"\x61":0}`,
        // Impossible prefixes must fail even at EOF/error, not only at a quote.
        `"\uDC`, `"\uD800\u0`, `"\uD800\uD8`,
        "\"a\n\"", "\"\x00\"", "\"\x1f\"", "{\"\xff\":0}", "\"\xff\"",
        "\"\xc0\x80\"", "\"\xc1\xbf\"", "\"\x80\"", "\"\xe0\x80\x80\"", "\"\xed\xa0\x80\"",
        "\"\xf0\x80\x80\x80\"", "\"\xf4\x90\x80\x80\"", "\"\xf5\x80\x80\x80\"", "\"\xe5x\"",
        "\"\xe0\x80", "\"\xed\xa0", "\"\xf4\x90", "\xff",
    }
    for _, input := range invalid {
        t.Run(input, func(t *testing.T) {
            for cut := 0; cut <= len(input); cut++ {
                for _, incomplete := range []bool{false, true} {
                    s := newJSONStream(snapshot)
                    err := s.push(input[:cut], &streamField{})
                    if err == nil {
                        err = s.push(input[cut:], &streamField{})
                    }
                    if err == nil {
                        err = s.finish(incomplete)
                    }
                    if err != ErrInvalidJSON {
                        t.Fatalf("cut %d, incomplete %t: got %v, want fixed invalid JSON", cut, incomplete, err)
                    }
                    if s.finish(incomplete) != ErrInvalidJSON || s.push("", &streamField{}) != ErrInvalidJSON || s.pendingBytes() != 0 {
                        t.Fatal("failed stream did not remain terminal or release pending bytes")
                    }
                    if s.restorer != nil || s.pieces != nil || s.snapshot != nil || s.stack != nil {
                        t.Fatal("failed stream retained parser/owner references")
                    }
                }
            }
        })
    }
}

func TestPrivacyJSONStreamDepth(t *testing.T) {
    snapshot := privacyJSONStreamSnapshot(t)
    for _, middle := range []string{`0`, `"[[OCTOPUS_PII:NAME]]"`, `{}`} {
        depth := MaxJSONDepth
        wantMiddle := middle
        if middle == `{}` {
            depth-- // The innermost object itself counts as a container.
        } else if middle[0] == '"' {
            wantMiddle = `"张三"`
        }
        input := strings.Repeat("[", depth) + middle + strings.Repeat("]", depth)
        want := strings.Repeat("[", depth) + wantMiddle + strings.Repeat("]", depth)
        privacyJSONStreamCuts(t, snapshot, input, want, false)
    }
    for _, input := range []string{strings.Repeat("[", MaxJSONDepth+1), strings.Repeat("[", MaxJSONDepth) + `{`} {
        s := newJSONStream(snapshot)
        var err error
        for i := range len(input) {
            err = s.push(input[i:i+1], &streamField{})
            if i < len(input)-1 && err != nil {
                t.Fatalf("depth failed before 129: byte %d, %v", i, err)
            }
        }
        if err != ErrResourceLimit || s.finish(true) != ErrResourceLimit || s.pendingBytes() != 0 {
            t.Fatalf("depth 129: push %v, pending %d", err, s.pendingBytes())
        }
    }
}

func TestPrivacyJSONStreamPending(t *testing.T) {
    snapshot := privacyJSONStreamSnapshot(t)
    for _, prefix := range []string{`"\uD83D\uDE0`, `{"\uD83D\uDE0`, "\"\xf0\x9f\x98"} {
        s := newJSONStream(snapshot)
        for i := range len(prefix) {
            if err := s.push(prefix[i:i+1], &streamField{}); err != nil {
                t.Fatal(err)
            }
        }
        want := 11
        if prefix[len(prefix)-1] == 0x98 {
            want = 3
        }
        if got := s.pendingBytes(); got != want {
            t.Fatalf("prefix %q: pending %d, want %d", prefix, got, want)
        }
        if err := s.finish(true); err != nil || s.pendingBytes() != 0 {
            t.Fatalf("EOF release: %v, pending %d", err, s.pendingBytes())
        }
    }
    s := newJSONStream(snapshot)
    if err := s.push(`"[[OCTOPUS_\u0`, &streamField{}); err != nil {
        t.Fatal(err)
    }
    // 标记前缀同时保留解析状态和逐片归属, escape 只保留原片段.
    if got := s.pendingBytes(); got != len(`[[OCTOPUS_\u0`)+len(`[[OCTOPUS_`) {
        t.Fatalf("marker plus escape pending = %d", got)
    }
    if err := s.finish(true); err != nil || s.pendingBytes() != 0 {
        t.Fatal("marker/escape did not release at EOF")
    }
    // Length of numbers/literals or already emitted syntax is not pending:
    // no later rewrite needs those event owners.
    for _, input := range []string{strings.Repeat("9", 65536), `[tru`, `{"a":`} {
        s := newJSONStream(snapshot)
        if err := s.push(input, &streamField{}); err != nil || s.pendingBytes() != 0 {
            t.Fatalf("non-string token buffered: %v, pending %d", err, s.pendingBytes())
        }
        if err := s.finish(true); err != nil {
            t.Fatal(err)
        }
    }
}

func TestPrivacyJSONStreamInactive(t *testing.T) {
    for _, snapshot := range []*Snapshot{
        nil,
        privacyTestCompile(t, Config{Enabled: true, Scopes: ToolCalls, FailurePolicy: Continue}, nil, 1),
    } {
        input := `{"\u005b[OCTOPUS_PII:PHONE_1]]":"\u005b[OCTOPUS_PII:PHONE_1]]/\uD83D\uDE00/张三"}`
        privacyJSONStreamCuts(t, snapshot, input, input, false)
        s := newJSONStream(snapshot)
        if err := s.push(`"\uDC00"`, &streamField{}); err != ErrInvalidJSON {
            t.Fatalf("inactive snapshot skipped string validation: %v", err)
        }
    }
}

func TestPrivacyJSONStreamTerminal(t *testing.T) {
    s := newJSONStream(privacyJSONStreamSnapshot(t))
    owner := &streamField{}
    if s.finish(false) != nil || s.finish(false) != nil {
        t.Fatal("empty arguments should finish idempotently")
    }
    if err := s.push(`"[[OCTOPUS_PII:PHONE_1]]"`, owner); err != ErrStreamIdentity || owner.output.String() != "" {
        t.Fatal("finished JSON stream reopened or wrote a late delta")
    }
}

func TestPrivacyJSONStreamCustomText(t *testing.T) {
    // Custom input belongs to S, not EJ: it is allowed to be non-JSON and
    // literal backslash-u is not interpreted as an inner JSON escape.
    snapshot := privacyJSONStreamSnapshot(t)
    input := `not JSON: \u005b\u005bOCTOPUS_PII:PHONE_1]] / [[OCTOPUS_PII:NAME]]`
    want := `not JSON: \u005b\u005bOCTOPUS_PII:PHONE_1]] / 张三`
    r := newOwnedRestorer(snapshot, false)
    var owners []*streamField
    for len(input) > 0 {
        _, size := utf8.DecodeRuneInString(input)
        text := input[:size]
        input = input[size:]
        owner := &streamField{}
        owners = append(owners, owner)
        r.pushRune(text, []ownedPiece{{field: owner, text: text}})
    }
    r.finish()
    var out strings.Builder
    for _, owner := range owners {
        out.WriteString(owner.output.String())
    }
    if out.String() != want || r.pendingBytes() != 0 {
        t.Fatalf("custom text = %q, want %q; pending %d", out.String(), want, r.pendingBytes())
    }
}
