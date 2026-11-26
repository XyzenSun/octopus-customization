package privacy

import "testing"

func TestPrivacyJSONString(t *testing.T) {
    cases := []struct {
        input string
        want  string
        valid bool
    }{
        {`""`, "", true},
        {"\t\"尾随 \"\n", "尾随 ", true},
        {`"a\u0000b\n\"\\"`, "a\x00b\n\"\\", true},
        {`"\uD83D\uDE00\u0301"`, "\U0001f600\u0301", true},
        {`"\\ud800"`, `\ud800`, true},
        {`"\uFFFD"`, "\uFFFD", true},
        {`"\uD800"`, "", false},
        {`"\uDC00"`, "", false},
        {`"\uD800\u0041"`, "", false},
        {`"\uD800\uD800"`, "", false},
        {`"\u123"`, "", false},
        {`"\x61"`, "", false},
        {"\"\xff\"", "", false},
        {"\"\n\"", "", false},
        {`"a" "b"`, "", false},
        {`null`, "", false},
        {`true`, "", false},
        {`[]`, "", false},
    }
    for _, tc := range cases {
        got, err := DecodeJSONString([]byte(tc.input))
        if !tc.valid {
            if got != "" || err != ErrInvalidJSON {
                t.Fatalf("DecodeJSONString(%q): expected fixed invalid JSON error", tc.input)
            }
            continue
        }
        if got != tc.want || err != nil {
            t.Fatalf("DecodeJSONString(%q) = %q, %v; want %q", tc.input, got, err, tc.want)
        }
    }
    if got, err := EncodeJSONString("\xff"); got != nil || err != ErrInvalidJSON {
        t.Fatal("invalid UTF-8 was not rejected on encode")
    }
}
