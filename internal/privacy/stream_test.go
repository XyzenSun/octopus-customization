package privacy

import (
    "bytes"
    "context"
    "encoding/json"
    "errors"
    "fmt"
    "io"
    "net/http"
    "reflect"
    "strconv"
    "strings"
    "testing"

    llm "github.com/looplj/axonhub/llm"
    "github.com/looplj/axonhub/llm/httpclient"
)

const privacyStreamPhone = "[[OCTOPUS_PII:PHONE_1]]"
const privacyStreamName = "[[OCTOPUS_PII:NAME]]"

// 期望字段由 fixture 手写, 不调用恢复器或生产分类器生成 oracle.
// 未列出的字段必须保持完整语义, 数字用 json.Number 避免大整数损失.
type privacyStreamFixture struct {
    kind string
    data string
    want map[string]string
}

func privacyStreamJSON(value any) string {
    data, err := json.Marshal(value)
    if err != nil {
        panic(err)
    }
    return string(data)
}

func privacyStreamEvent(kind, data string, want map[string]string) privacyStreamFixture {
    return privacyStreamFixture{kind: kind, data: data, want: want}
}

func privacyStreamSnapshot(t *testing.T, scopes Scope, policy FailurePolicy) *Snapshot {
    t.Helper()
    snapshot, err := Compile(Config{Enabled: true, Scopes: scopes, FailurePolicy: policy}, []Rule{
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
        {Source: "行\"一\n路径\\尾", Alias: "ESCAPED", Enabled: true},
        {Source: "x\x00y", Alias: "NUL", Enabled: true},
        {Source: privacyStreamPhone, Alias: "NESTED", Enabled: true},
        {Source: "[[OCTOPUS_PII:SELF]]", Alias: "SELF", Enabled: true},
        {Source: "disabled", Alias: "DISABLED", Enabled: false},
    }, 37)
    if err != nil {
        t.Fatal(err)
    }
    return snapshot
}

// 读边界只校准 SSE framing; 逻辑 delta 的切点由各 fixture 独立提供.
// 末尾错误模拟本地上游读取失败, 不建立任何 HTTP 连接.
type privacyStreamReader struct {
    body *strings.Reader
    step int
    tail error
    closes int
}

func (r *privacyStreamReader) Read(p []byte) (int, error) {
    if r.body.Len() == 0 && r.tail != nil {
        return 0, r.tail
    }
    if r.step > 0 && len(p) > r.step {
        p = p[:r.step]
    }
    return r.body.Read(p)
}

func (r *privacyStreamReader) Close() error {
    r.closes++
    return nil
}

func privacyStreamWire(fixtures []privacyStreamFixture) string {
    var wire strings.Builder
    for index, fixture := range fixtures {
        fmt.Fprintf(&wire, ": 合成心跳注释\r\nid: fixture-%d\r\n", index)
        if fixture.kind != "" {
            fmt.Fprintf(&wire, "event: %s\r\n", fixture.kind)
        }
        for _, line := range strings.Split(fixture.data, "\n") {
            fmt.Fprintf(&wire, "data: %s\r\n", line)
        }
        wire.WriteString("\r\n")
    }
    return wire.String()
}

func privacyStreamDecode(t *testing.T, fixtures []privacyStreamFixture, step int) []*httpclient.StreamEvent {
    t.Helper()
    reader := &privacyStreamReader{body: strings.NewReader(privacyStreamWire(fixtures)), step: step}
    decoder := httpclient.NewDefaultSSEDecoder(t.Context(), reader)
    defer func() {
        if err := decoder.Close(); err != nil {
            t.Fatal(err)
        }
        if reader.closes != 1 {
            t.Fatalf("decoder Close 次数 = %d", reader.closes)
        }
    }()
    var events []*httpclient.StreamEvent
    for decoder.Next() {
        event := decoder.Current()
        index := len(events)
        if index >= len(fixtures) || event.LastEventID != fmt.Sprintf("fixture-%d", index) || string(event.Data) != fixtures[index].data {
            t.Fatalf("decoder 事件 %d 与合成 SSE 不符: %+v", index, event)
        }
        // HTTP executor 附加的载体字段无法通过 SSE framing 编码, 在 decoder
        // 之后注入; 各事件都带不同值以检查改写/排队时未丢失或串位.
        event.Headers = http.Header{"X-Fixture": {fmt.Sprint(index), privacyStreamPhone}, "X-Empty": {}}
        event.CleanEOFCompletionEvidence = index%2 == 0
        event.Size = index + 100
        events = append(events, event)
    }
    if err := decoder.Err(); err != nil || len(events) != len(fixtures) {
        t.Fatalf("decoder: 事件数 %d/%d, 错误 %v", len(events), len(fixtures), err)
    }
    if decoder.Next() || decoder.Next() || decoder.Err() != nil {
        t.Fatal("干净 EOF 后重复 Next 不应产生事件或错误")
    }
    return events
}

func privacyStreamDocument(t *testing.T, data []byte) any {
    t.Helper()
    if !json.Valid(data) {
        t.Fatalf("完整外层事件 JSON 非法: %q", data)
    }
    decoder := json.NewDecoder(bytes.NewReader(data))
    decoder.UseNumber()
    var value any
    if err := decoder.Decode(&value); err != nil {
        t.Fatalf("外层事件 JSON 非法: %v; %q", err, data)
    }
    return value
}

func privacyStreamSet(t *testing.T, root any, path, text string) bool {
    t.Helper()
    segments := strings.Split(path, ".")
    current := root
    for position, segment := range segments {
        last := position == len(segments)-1
        switch node := current.(type) {
        case map[string]any:
            child, exists := node[segment]
            if !exists {
                t.Fatalf("fixture 期望路径不存在: %s", path)
            }
            if last {
                node[segment] = text
                return child != text
            }
            current = child
        case []any:
            index, err := strconv.Atoi(segment)
            if err != nil || index < 0 || index >= len(node) {
                t.Fatalf("fixture 数组路径非法: %s", path)
            }
            if last {
                old := node[index]
                node[index] = text
                return old != text
            }
            current = node[index]
        default:
            t.Fatalf("fixture 路径穿过标量: %s", path)
        }
    }
    t.Fatal("空期望路径")
    return false
}

type privacyStreamHarness struct {
    t *testing.T
    restorer *StreamRestorer
    fixtures []privacyStreamFixture
    inputs []*httpclient.StreamEvent
    emitted int
}

func privacyStreamNewHarness(t *testing.T, snapshot *Snapshot, format llm.APIFormat, fixtures []privacyStreamFixture, step int) *privacyStreamHarness {
    t.Helper()
    return &privacyStreamHarness{t: t, restorer: NewStreamRestorer(snapshot, format), fixtures: fixtures, inputs: privacyStreamDecode(t, fixtures, step)}
}

func (h *privacyStreamHarness) check(commits []StreamCommit) {
    h.t.Helper()
    for _, commit := range commits {
        index := h.emitted
        if index >= len(h.inputs) || commit.Event == nil {
            h.t.Fatal("增加了事件或提交了 nil")
        }
        input, fixture := h.inputs[index], h.fixtures[index]
        event := commit.Event
        if event.Type != input.Type || event.LastEventID != input.LastEventID || !reflect.DeepEqual(event.Headers, input.Headers) || event.CleanEOFCompletionEvidence != input.CleanEOFCompletionEvidence || event.Size != input.Size {
            h.t.Fatalf("FIFO/事件元数据损坏, 第 %d 个提交: %+v, 原事件 %+v", index, event, input)
        }
        if string(input.Data) != fixture.data {
            h.t.Fatalf("输入事件 %d 被原地修改", index)
        }
        changed := false
        if len(fixture.want) == 0 {
            if string(event.Data) != fixture.data {
                h.t.Fatalf("未改写事件 %d 应字节相同: got %s, want %s", index, event.Data, fixture.data)
            }
        } else {
            want := privacyStreamDocument(h.t, []byte(fixture.data))
            for path, text := range fixture.want {
                changed = privacyStreamSet(h.t, want, path, text) || changed
            }
            got := privacyStreamDocument(h.t, event.Data)
            if !reflect.DeepEqual(got, want) {
                h.t.Fatalf("事件 %d: got %s\nwant %s", index, event.Data, privacyStreamJSON(want))
            }
            if !changed && string(event.Data) != fixture.data {
                h.t.Fatalf("无变化事件 %d 被重新编码", index)
            }
        }
        if commit.Changed != changed {
            h.t.Fatalf("事件 %d Changed=%t, want %t", index, commit.Changed, changed)
        }
        h.emitted++
    }
}

func (h *privacyStreamHarness) push(index int, wantErr error) {
    h.t.Helper()
    commits, err := h.restorer.Push(h.inputs[index])
    if err != wantErr {
        h.t.Fatalf("Push 事件 %d: got %v, want %v", index, err, wantErr)
    }
    h.check(commits)
}

func (h *privacyStreamHarness) through(want int) {
    h.t.Helper()
    if h.emitted != want {
        h.t.Fatalf("FIFO 累计提交数 %d, want %d", h.emitted, want)
    }
}

func (h *privacyStreamHarness) finish() {
    h.t.Helper()
    commits, err := h.restorer.Finish()
    if err != nil {
        h.t.Fatalf("Finish: %v", err)
    }
    h.check(commits)
    h.through(len(h.inputs))
    if !h.restorer.Closed() {
        h.t.Fatal("Finish 后未关闭")
    }
    for range 2 {
        commits, err := h.restorer.Finish()
        if len(commits) != 0 || err != nil {
            h.t.Fatal("Finish 不幂等或重复提交")
        }
        h.restorer.Cancel()
    }
    if commits, err := h.restorer.Push(h.inputs[0]); len(commits) != 0 || err != ErrStreamIdentity {
        h.t.Fatal("已关闭恢复器接受了事件")
    }
}

func privacyStreamRun(t *testing.T, snapshot *Snapshot, format llm.APIFormat, fixtures []privacyStreamFixture) {
    t.Helper()
    h := privacyStreamNewHarness(t, snapshot, format, fixtures, 0)
    for index := range h.inputs {
        h.push(index, nil)
    }
    h.finish()
}

type privacyStreamProtocol struct {
    name string
    format llm.APIFormat
}

var privacyStreamProtocols = []privacyStreamProtocol{
    {"chat", llm.APIFormatOpenAIChatCompletion},
    {"responses", llm.APIFormatOpenAIResponse},
    {"anthropic", llm.APIFormatAnthropicMessage},
}

func privacyStreamInitial(protocol string, embedded bool) []privacyStreamFixture {
    if protocol != "anthropic" {
        return nil
    }
    block := `{"type":"text","text":""}`
    if embedded {
        block = `{"type":"tool_use","id":"tool-0","name":"privacy_echo","input":{}}`
    }
    return []privacyStreamFixture{
        privacyStreamEvent("message_start", `{"type":"message_start","message":{"id":"message-0","role":"assistant","content":[]}}`, nil),
        privacyStreamEvent("content_block_start", `{"type":"content_block_start","index":0,"content_block":`+block+`}`, nil),
    }
}

func privacyStreamDelta(protocol string, embedded bool, text, want string) privacyStreamFixture {
    encoded := privacyStreamJSON(text)
    switch protocol {
    case "chat":
        if embedded {
            return privacyStreamEvent("", `{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":`+encoded+`}}]}}]}`, map[string]string{"choices.0.delta.tool_calls.0.function.arguments": want})
        }
        return privacyStreamEvent("", `{"choices":[{"index":0,"delta":{"content":`+encoded+`}}]}`, map[string]string{"choices.0.delta.content": want})
    case "responses":
        kind, identity := "response.output_text.delta", `"output_index":0,"content_index":0,`
        if embedded {
            kind, identity = "response.function_call_arguments.delta", `"output_index":0,`
        }
        return privacyStreamEvent(kind, `{"type":"`+kind+`",`+identity+`"delta":`+encoded+`}`, map[string]string{"delta": want})
    case "anthropic":
        kind, field := "text_delta", "text"
        if embedded {
            kind, field = "input_json_delta", "partial_json"
        }
        return privacyStreamEvent("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"`+kind+`","`+field+`":`+encoded+`}}`, map[string]string{"delta."+field: want})
    default:
        panic("未知测试协议")
    }
}

func privacyStreamTerminal(protocol string) []privacyStreamFixture {
    switch protocol {
    case "chat":
        return []privacyStreamFixture{
            privacyStreamEvent("", `{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`, nil),
            privacyStreamEvent("", `[DONE]`, nil),
        }
    case "responses":
        return []privacyStreamFixture{privacyStreamEvent("response.completed", `{"type":"response.completed","response":{"id":"response-0","status":"completed","output":[]}}`, nil)}
    case "anthropic":
        return []privacyStreamFixture{
            privacyStreamEvent("content_block_stop", `{"type":"content_block_stop","index":0}`, nil),
            privacyStreamEvent("message_stop", `{"type":"message_stop"}`, nil),
        }
    default:
        panic("未知测试协议")
    }
}

// 最早非空片段是 marker 起点所属字段, 恢复文本全部写入该字段;
// 后续片段可以变为空字符串, 但事件必须仍存在且只提交一次.
func privacyStreamMarkerParts(t *testing.T, snapshot *Snapshot, protocol privacyStreamProtocol, parts []string) {
    t.Helper()
    fixtures := privacyStreamInitial(protocol.name, false)
    first := true
    for _, part := range parts {
        want := ""
        if part != "" && first {
            want, first = "13969909666", false
        }
        fixtures = append(fixtures, privacyStreamDelta(protocol.name, false, part, want))
    }
    fixtures = append(fixtures, privacyStreamTerminal(protocol.name)...)
    privacyStreamRun(t, snapshot, protocol.format, fixtures)
}

// 独立区间 oracle: 已知 marker 占 [start, end), 替换归属包含 start 的
// 非空片段; 其他字节仍属于各自原片段. 不解析 JSON 或调用生产恢复器.
func privacyStreamOwnedParts(input, replacement string, parts []string, start, end int) []string {
    result := make([]string, len(parts))
    offset := 0
    for index, part := range parts {
        limit := offset + len(part)
        if offset < start {
            result[index] += input[offset:min(limit, start)]
        }
        if offset <= start && start < limit {
            result[index] += replacement
        }
        if limit > end {
            result[index] += input[max(offset, end):limit]
        }
        offset = limit
    }
    return result
}

func privacyStreamTestCuts(t *testing.T) {
    snapshot := privacyStreamSnapshot(t, AllScopes, Continue)
    for _, protocol := range privacyStreamProtocols {
        t.Run(protocol.name, func(t *testing.T) {
            for first := 0; first <= len(privacyStreamPhone); first++ {
                privacyStreamMarkerParts(t, snapshot, protocol, []string{privacyStreamPhone[:first], privacyStreamPhone[first:]})
                for second := first; second <= len(privacyStreamPhone); second++ {
                    privacyStreamMarkerParts(t, snapshot, protocol, []string{privacyStreamPhone[:first], privacyStreamPhone[first:second], privacyStreamPhone[second:]})
                }
            }
            parts := []string{""}
            for index := range len(privacyStreamPhone) {
                parts = append(parts, privacyStreamPhone[index:index+1])
            }
            parts = append(parts, "")
            privacyStreamMarkerParts(t, snapshot, protocol, parts)

            // 工具 JSON 的 unicode escape、surrogate 和中文 escape 按字节
            // 切分. 外层片段必须是有效 UTF-8, 不用 Marshal 偷换非法断字节.
            input := `{"v":"\u005b\u005bOCTOPUS_PII:ESCAPED]]","emoji":"\uD83D\uDE00","\u5b57":"\u5f20"}`
            for cut := 0; cut <= len(input); cut++ {
                prefix, suffix := input[:cut], input[cut:]
                markerStart := strings.Index(input, `\u005b`)
                markerEnd := strings.Index(input, `]]`) + 2
                replacement := `行\"一\n路径\\尾`
                wantPrefix, wantSuffix := "", ""
                switch {
                case cut <= markerStart:
                    wantPrefix = prefix
                    wantSuffix = input[cut:markerStart] + replacement + input[markerEnd:]
                case cut < markerEnd:
                    wantPrefix = input[:markerStart] + replacement
                    wantSuffix = input[markerEnd:]
                default:
                    wantPrefix = input[:markerStart] + replacement + input[markerEnd:cut]
                    wantSuffix = suffix
                }
                fixtures := privacyStreamInitial(protocol.name, true)
                fixtures = append(fixtures, privacyStreamDelta(protocol.name, true, prefix, wantPrefix), privacyStreamDelta(protocol.name, true, suffix, wantSuffix))
                fixtures = append(fixtures, privacyStreamTerminal(protocol.name)...)
                privacyStreamRun(t, snapshot, protocol.format, fixtures)
            }

            // EJ 也枚举全部有序三段切点及空片, 让外层字段分配与内层词法
            // 同时受测; 替换仍只出现在 marker 起点所属的事件.
            inner := `{"v":"[[OCTOPUS_PII:PHONE_1]]"}`
            start := len(`{"v":"`)
            end := start + len(privacyStreamPhone)
            for first := 0; first <= len(inner); first++ {
                for second := first; second <= len(inner); second++ {
                    parts := []string{inner[:first], inner[first:second], inner[second:]}
                    want := privacyStreamOwnedParts(inner, "13969909666", parts, start, end)
                    fixtures := privacyStreamInitial(protocol.name, true)
                    for index, part := range parts {
                        fixtures = append(fixtures, privacyStreamDelta(protocol.name, true, part, want[index]))
                    }
                    fixtures = append(fixtures, privacyStreamTerminal(protocol.name)...)
                    privacyStreamRun(t, snapshot, protocol.format, fixtures)
                }
            }
            // 未知、非法、残缺候选及重叠 LEFT 回退必须原片归属返回.
            for _, literal := range []string{"[[OCTOPUS_PII:UNKNOWN]]", "[[OCTOPUS_PII:bad]]", "[[OCTOPUS_PII:PHONE_1]", "[[[OCTOPUS_"} {
                for cut := 0; cut <= len(literal); cut++ {
                    fixtures := privacyStreamInitial(protocol.name, false)
                    fixtures = append(fixtures,
                        privacyStreamDelta(protocol.name, false, literal[:cut], literal[:cut]),
                        privacyStreamEvent("metadata", `{"metadata":{"text":"[[OCTOPUS_PII:NAME]]"}}`, nil),
                        privacyStreamDelta(protocol.name, false, literal[cut:], literal[cut:]),
                    )
                    fixtures = append(fixtures, privacyStreamTerminal(protocol.name)...)
                    privacyStreamRun(t, snapshot, protocol.format, fixtures)
                }
            }
        })
    }
    t.Run("ResponsesCustomInputAllCuts", func(t *testing.T) {
        for first := 0; first <= len(privacyStreamPhone); first++ {
            for second := first; second <= len(privacyStreamPhone); second++ {
                parts := []string{privacyStreamPhone[:first], privacyStreamPhone[first:second], privacyStreamPhone[second:]}
                want := privacyStreamOwnedParts(privacyStreamPhone, "13969909666", parts, 0, len(privacyStreamPhone))
                var fixtures []privacyStreamFixture
                for index, part := range parts {
                    fixtures = append(fixtures, privacyStreamEvent("response.custom_tool_call_input.delta", `{"type":"response.custom_tool_call_input.delta","output_index":0,"delta":`+privacyStreamJSON(part)+`}`, map[string]string{"delta": want[index]}))
                }
                fixtures = append(fixtures, privacyStreamTerminal("responses")...)
                privacyStreamRun(t, snapshot, llm.APIFormatOpenAIResponse, fixtures)
            }
        }
    })
}

func privacyStreamTestChat(t *testing.T) {
    fixtures := []privacyStreamFixture{
        privacyStreamEvent("chat-carrier", `{"id":"[[OCTOPUS_PII:PHONE_1]]","model":"[[OCTOPUS_PII:NAME]]","sequence_number":900719925474099312345,"choices":[{"index":0,"delta":{"role":"assistant","content":"[[OCTOPUS_PII:PH","refusal":"[[OCTOPUS_PII:NA","reasoning_content":"[[OCTOPUS_PII:PHONE_1]]","tool_calls":[{"index":0,"function":{"name":"[[OCTOPUS_PII:NAME]]","arguments":"{\"v\":\"[[OCTOPUS_PII:PH"}}]}},{"index":1,"delta":{"content":"other","tool_calls":[{"index":0,"function":{"arguments":"{\"v\":\"[[OCTOPUS_PII:NA"}}]}}]}`, map[string]string{
            "choices.0.delta.content": "13969909666", "choices.0.delta.refusal": "张三", "choices.0.delta.tool_calls.0.function.arguments": `{"v":"13969909666`, "choices.1.delta.tool_calls.0.function.arguments": `{"v":"张三`,
        }),
        privacyStreamEvent("heartbeat", `{"choices":[],"usage":{"prompt_tokens":900719925474099312345,"completion_tokens":0},"metadata":{"text":"[[OCTOPUS_PII:PHONE_1]]"}}`, nil),
        // 同一 tool ID 在不同 choice 中合法, 后到别名不能合并词法流.
        privacyStreamEvent("", `{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"same-id","type":"function","function":{"name":"privacy_echo"}}]}},{"index":1,"delta":{"tool_calls":[{"index":0,"id":"same-id","type":"function"}]}}]}`, nil),
        privacyStreamEvent("", `{"choices":[{"index":0,"delta":{"content":"ONE_1]]","refusal":"ME]]","tool_calls":[{"index":0,"function":{"arguments":"ONE_1]]\"}"}}]}}]}`, map[string]string{"choices.0.delta.content": "", "choices.0.delta.refusal": "", "choices.0.delta.tool_calls.0.function.arguments": `"}`}),
        privacyStreamEvent("", `{"choices":[{"index":1,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"ME]]\"}"}}]}}]}`, map[string]string{"choices.0.delta.tool_calls.0.function.arguments": `"}`}),
        // 同 chunk 最后 delta 在 finish_reason 之前处理, 完整 message 不追加.
        privacyStreamEvent("", `{"choices":[{"index":0,"delta":{"content":"[[OCTOPUS_PII:NAME]]"},"finish_reason":"stop","message":{"role":"assistant","content":"[[OCTOPUS_PII:PHONE_1]]","refusal":"[[OCTOPUS_PII:NAME]]","tool_calls":[{"id":"same-id","type":"function","function":{"name":"[[OCTOPUS_PII:NAME]]","arguments":"{\"id\":\"[[OCTOPUS_PII:PHONE_1]]\",\"n\":900719925474099312345}"}}]}}]}`, map[string]string{"choices.0.delta.content": "张三", "choices.0.message.content": "13969909666", "choices.0.message.refusal": "张三", "choices.0.message.tool_calls.0.function.arguments": `{"id":"13969909666","n":900719925474099312345}`}),
        privacyStreamEvent("", `{"choices":[{"index":1,"delta":{},"finish_reason":"tool_calls"}]}`, nil),
        privacyStreamEvent("", `[DONE]`, nil),
        privacyStreamEvent("metadata", `{"metadata":{"content":"[[OCTOPUS_PII:PHONE_1]]"}}`, nil),
    }
    h := privacyStreamNewHarness(t, privacyStreamSnapshot(t, AllScopes, Continue), llm.APIFormatOpenAIChatCompletion, fixtures, 1)
    for index := range h.inputs {
        h.push(index, nil)
        if index < 4 {
            h.through(0)
        } else {
            h.through(index + 1)
        }
    }
    h.finish()
}

func privacyStreamTestResponses(t *testing.T) {
    fixtures := []privacyStreamFixture{
        privacyStreamEvent("response.created", `{"type":"response.created","response":{"id":"response-0","status":"in_progress","output":[]}}`, nil),
        privacyStreamEvent("message", `{"type":"response.output_text.delta","response_id":"response-0","output_index":0,"content_index":0,"delta":"[[OCTOPUS_PII:PH","sequence_number":900719925474099312345}`, map[string]string{"delta": "13969909666"}),
        // output_index 已建立词法状态, 后到 item.id 仅补别名. 非空 added 快照独立恢复.
        privacyStreamEvent("different-carrier", `{"type":"response.output_item.added","output_index":0,"item":{"id":"item-0","type":"message","role":"assistant","content":[{"type":"output_text","text":"[[OCTOPUS_PII:NAME]]"}]}}`, map[string]string{"item.content.0.text": "张三"}),
        privacyStreamEvent("response.content_part.added", `{"type":"response.content_part.added","item_id":"item-0","content_index":0,"part":{"type":"output_text","text":"[[OCTOPUS_PII:PHONE_1]]","annotations":[{"text":"[[OCTOPUS_PII:PHONE_1]]"}]}}`, map[string]string{"part.text": "13969909666"}),
        privacyStreamEvent("response.refusal.delta", `{"type":"response.refusal.delta","output_index":0,"item_id":"item-0","content_index":1,"delta":"[[OCTOPUS_PII:NA"}`, map[string]string{"delta": "张三"}),
        privacyStreamEvent("response.output_item.added", `{"type":"response.output_item.added","output_index":1,"item":{"id":"function-item","call_id":"call-not-item","type":"function_call","name":"[[OCTOPUS_PII:PHONE_1]]","arguments":"{}"}}`, nil),
        privacyStreamEvent("response.function_call_arguments.delta", `{"type":"response.function_call_arguments.delta","output_index":1,"delta":"{\"id\":\"[[OCTOPUS_PII:PH"}`, map[string]string{"delta": `{"id":"13969909666`}),
        privacyStreamEvent("response.output_item.added", `{"type":"response.output_item.added","output_index":2,"item":{"id":"custom-item","call_id":"custom-call","type":"custom_tool_call","name":"custom","input":"[[OCTOPUS_PII:PH"}}`, nil),
        privacyStreamEvent("response.custom_tool_call_input.delta", `{"type":"response.custom_tool_call_input.delta","item_id":"custom-item","delta":"not JSON: [[OCTOPUS_PII:NA"}`, map[string]string{"delta": "not JSON: 张三"}),
        privacyStreamEvent("response.reasoning_summary_text.delta", `{"type":"response.reasoning_summary_text.delta","output_index":8,"delta":"[[OCTOPUS_PII:PHONE_1]]"}`, nil),
        privacyStreamEvent("response.metadata", `{"type":"response.metadata","metadata":{"text":"[[OCTOPUS_PII:NAME]]"},"usage":{"output_tokens":0}}`, nil),
        privacyStreamEvent("response.output_text.delta", `{"type":"response.output_text.delta","item_id":"item-0","content_index":0,"delta":"ONE_1]]"}`, map[string]string{"delta": ""}),
        privacyStreamEvent("response.refusal.delta", `{"type":"response.refusal.delta","item_id":"item-0","content_index":1,"delta":"ME]]"}`, map[string]string{"delta": ""}),
        privacyStreamEvent("response.function_call_arguments.delta", `{"type":"response.function_call_arguments.delta","item_id":"function-item","delta":"ONE_1]]\",\"n\":900719925474099312345}"}`, map[string]string{"delta": `","n":900719925474099312345}`}),
        privacyStreamEvent("response.custom_tool_call_input.delta", `{"type":"response.custom_tool_call_input.delta","output_index":2,"delta":"ME]] / \\u005b\\u005bOCTOPUS_PII:PHONE_1]]"}`, map[string]string{"delta": ` / \u005b\u005bOCTOPUS_PII:PHONE_1]]`}),
        privacyStreamEvent("response.output_text.done", `{"type":"response.output_text.done","item_id":"item-0","content_index":0,"text":"[[OCTOPUS_PII:PHONE_1]]"}`, map[string]string{"text": "13969909666"}),
        privacyStreamEvent("response.refusal.done", `{"type":"response.refusal.done","item_id":"item-0","content_index":1,"refusal":"[[OCTOPUS_PII:NAME]]"}`, map[string]string{"refusal": "张三"}),
        privacyStreamEvent("response.function_call_arguments.done", `{"type":"response.function_call_arguments.done","item_id":"function-item","arguments":"{\"id\":\"[[OCTOPUS_PII:PHONE_1]]\"}"}`, map[string]string{"arguments": `{"id":"13969909666"}`}),
        privacyStreamEvent("response.custom_tool_call_input.done", `{"type":"response.custom_tool_call_input.done","item_id":"custom-item","input":"patch [[OCTOPUS_PII:NAME]]"}`, map[string]string{"input": "patch 张三"}),
        privacyStreamEvent("response.content_part.done", `{"type":"response.content_part.done","item_id":"item-0","content_index":1,"part":{"type":"refusal","refusal":"[[OCTOPUS_PII:NAME]]"}}`, map[string]string{"part.refusal": "张三"}),
        privacyStreamEvent("response.output_item.done", `{"type":"response.output_item.done","output_index":0,"item":{"id":"item-0","type":"message","role":"assistant","content":[{"type":"refusal","refusal":"[[OCTOPUS_PII:NAME]]"}]}}`, map[string]string{"item.content.0.refusal": "张三"}),
        // pin 可能用 completed 携带 cancelled status, 不纠正该实际组合.
        privacyStreamEvent("response.completed", `{"type":"response.completed","response":{"id":"response-0","status":"cancelled","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"[[OCTOPUS_PII:PHONE_1]]"},{"type":"refusal","refusal":"[[OCTOPUS_PII:NAME]]"}]},{"type":"function_call","id":"function-item","call_id":"call-not-item","name":"[[OCTOPUS_PII:NAME]]","arguments":"{\"id\":\"[[OCTOPUS_PII:PHONE_1]]\"}"},{"type":"custom_tool_call","input":"[[OCTOPUS_PII:NAME]]"},{"type":"function_call_output","call_id":"call-not-item","output":"[[OCTOPUS_PII:PHONE_1]]"},{"type":"reasoning","summary":[{"type":"summary_text","text":"[[OCTOPUS_PII:PHONE_1]]"}]}],"usage":{"input_tokens":900719925474099312345,"output_tokens":0},"metadata":{"text":"[[OCTOPUS_PII:PHONE_1]]"}}}`, map[string]string{"response.output.0.content.0.text": "13969909666", "response.output.0.content.1.refusal": "张三", "response.output.1.arguments": `{"id":"13969909666"}`, "response.output.2.input": "张三", "response.output.3.output": "13969909666"}),
    }
    h := privacyStreamNewHarness(t, privacyStreamSnapshot(t, AllScopes, Continue), llm.APIFormatOpenAIResponse, fixtures, 3)
    for index := range h.inputs {
        h.push(index, nil)
        if index < 14 {
            h.through(1)
        } else {
            h.through(index + 1)
        }
    }
    h.finish()
}

func privacyStreamTestAnthropic(t *testing.T) {
    fixtures := []privacyStreamFixture{
        privacyStreamEvent("message_start", `{"type":"message_start","message":{"id":"message-0","model":"[[OCTOPUS_PII:PHONE_1]]","role":"assistant","content":[{"type":"text","text":"[[OCTOPUS_PII:PHONE_1]]"},{"type":"tool_use","id":"tool-1","name":"[[OCTOPUS_PII:NAME]]","input":{"id":"[[OCTOPUS_PII:PHONE_1]]","n":900719925474099312345}},{"type":"tool_result","tool_use_id":"tool-1","content":{"id":"[[OCTOPUS_PII:NAME]]"},"is_error":false}],"usage":{"input_tokens":900719925474099312345,"output_tokens":0}}}`, map[string]string{"message.content.0.text": "13969909666", "message.content.1.input.id": "13969909666", "message.content.2.content.id": "张三"}),
        privacyStreamEvent("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":"[[OCTOPUS_PII:NAME]]","citations":[{"cited_text":"[[OCTOPUS_PII:NAME]]"}]}}`, map[string]string{"content_block.text": "张三"}),
        privacyStreamEvent("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"[[OCTOPUS_PII:PH"}}`, map[string]string{"delta.text": "13969909666"}),
        privacyStreamEvent("content_block_start", `{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"tool-1","name":"[[OCTOPUS_PII:NAME]]","input":{}}}`, nil),
        privacyStreamEvent("content_block_delta", `{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"name\":\"[[OCTOPUS_PII:NA"}}`, map[string]string{"delta.partial_json": `{"name":"张三`}),
        privacyStreamEvent("content_block_start", `{"type":"content_block_start","index":2,"content_block":{"type":"tool_result","tool_use_id":"tool-1","content":"[[OCTOPUS_PII:PHONE_1]]"}}`, map[string]string{"content_block.content": "13969909666"}),
        privacyStreamEvent("content_block_start", `{"type":"content_block_start","index":3,"content_block":{"type":"text","text":"[[OCTOPUS_PII:PHONE_1]]"}}`, map[string]string{"content_block.text": "13969909666"}),
        privacyStreamEvent("content_block_delta", `{"type":"content_block_delta","index":3,"delta":{"type":"text_delta","text":"[[OCTOPUS_PII:NA"}}`, map[string]string{"delta.text": "张三"}),
        privacyStreamEvent("content_block_start", `{"type":"content_block_start","index":4,"content_block":{"type":"thinking","thinking":"[[OCTOPUS_PII:PHONE_1]]","signature":"[[OCTOPUS_PII:NAME]]"}}`, nil),
        privacyStreamEvent("content_block_delta", `{"type":"content_block_delta","index":4,"delta":{"type":"thinking_delta","thinking":"[[OCTOPUS_PII:PHONE_1]]"}}`, nil),
        privacyStreamEvent("content_block_delta", `{"type":"content_block_delta","index":4,"delta":{"type":"signature_delta","signature":"[[OCTOPUS_PII:NAME]]"}}`, nil),
        privacyStreamEvent("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"citations_delta","citation":{"cited_text":"[[OCTOPUS_PII:PHONE_1]]"}}}`, nil),
        privacyStreamEvent("ping", `{"type":"ping"}`, nil),
        privacyStreamEvent("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"ONE_1]]"}}`, map[string]string{"delta.text": ""}),
        privacyStreamEvent("content_block_delta", `{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"ME]]\"}"}}`, map[string]string{"delta.partial_json": `"}`}),
        privacyStreamEvent("content_block_delta", `{"type":"content_block_delta","index":3,"delta":{"type":"text_delta","text":"ME]]"}}`, map[string]string{"delta.text": ""}),
        privacyStreamEvent("content_block_stop", `{"type":"content_block_stop","index":0}`, nil),
        privacyStreamEvent("content_block_stop", `{"type":"content_block_stop","index":1}`, nil),
        privacyStreamEvent("content_block_stop", `{"type":"content_block_stop","index":2}`, nil),
        privacyStreamEvent("content_block_stop", `{"type":"content_block_stop","index":3}`, nil),
        privacyStreamEvent("content_block_stop", `{"type":"content_block_stop","index":4}`, nil),
        privacyStreamEvent("message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":"[[OCTOPUS_PII:PHONE_1]]"},"usage":{"output_tokens":900719925474099312345}}`, nil),
        privacyStreamEvent("message_stop", `{"type":"message_stop"}`, nil),
    }
    h := privacyStreamNewHarness(t, privacyStreamSnapshot(t, AllScopes, Continue), llm.APIFormatAnthropicMessage, fixtures, 1)
    for index := range h.inputs {
        h.push(index, nil)
        if index < 2 {
            h.through(index + 1)
        } else if index < 15 {
            h.through(2)
        } else {
            h.through(index + 1)
        }
    }
    h.finish()
}

// 关联失败使用真实 payload, 不注入分类器或恢复器错误.
func privacyStreamTestIdentity(t *testing.T) {
    type identityCase struct {
        name string
        protocol string
        before []privacyStreamFixture
        bad privacyStreamFixture
    }
    event := func(kind, data string) privacyStreamFixture { return privacyStreamEvent(kind, data, nil) }
    cases := []identityCase{
        {name: "chat_missing_choice", protocol: "chat", bad: event("", `{"choices":[{"delta":{"content":"x"}}]}`)},
        {name: "chat_null_choice", protocol: "chat", bad: event("", `{"choices":[{"index":null,"delta":{"content":"x"}}]}`)},
        {name: "chat_fraction_choice", protocol: "chat", bad: event("", `{"choices":[{"index":0.0,"delta":{"content":"x"}}]}`)},
        {name: "chat_negative_choice", protocol: "chat", bad: event("", `{"choices":[{"index":-1,"delta":{"content":"x"}}]}`)},
        {name: "chat_missing_tool", protocol: "chat", bad: event("", `{"choices":[{"index":0,"delta":{"tool_calls":[{"function":{"arguments":"{}"}}]}}]}`)},
        {name: "chat_tool_id_changed", protocol: "chat", before: []privacyStreamFixture{event("", `{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"first"}]}}]}`)}, bad: event("", `{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"second"}]}}]}`)},
        {name: "chat_tool_id_two_indices", protocol: "chat", before: []privacyStreamFixture{event("", `{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"first"}]}}]}`)}, bad: event("", `{"choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"id":"first"}]}}]}`)},
        {name: "chat_role_changed", protocol: "chat", before: []privacyStreamFixture{event("", `{"choices":[{"index":0,"delta":{"role":"assistant"}}]}`)}, bad: event("", `{"choices":[{"index":0,"delta":{"role":"tool","content":"x"}}]}`)},
        {name: "chat_retired_choice", protocol: "chat", before: []privacyStreamFixture{event("", `{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`)}, bad: event("", `{"choices":[{"index":0,"delta":{"content":"x"}}]}`)},
        {name: "chat_after_done", protocol: "chat", before: []privacyStreamFixture{event("", `[DONE]`)}, bad: event("", `{"choices":[{"index":0,"delta":{"content":"x"}}]}`)},
        {name: "responses_missing_identity", protocol: "responses", bad: event("response.output_text.delta", `{"type":"response.output_text.delta","content_index":0,"delta":"x"}`)},
        {name: "responses_missing_content", protocol: "responses", bad: event("response.output_text.delta", `{"type":"response.output_text.delta","output_index":0,"delta":"x"}`)},
        {name: "responses_call_not_item", protocol: "responses", before: []privacyStreamFixture{event("response.output_item.added", `{"type":"response.output_item.added","output_index":0,"item":{"id":"item","call_id":"call","type":"function_call","arguments":""}}`)}, bad: event("response.function_call_arguments.delta", `{"type":"response.function_call_arguments.delta","item_id":"call","call_id":"call","delta":"{}"}`)},
        {name: "responses_call_only", protocol: "responses", bad: event("response.function_call_arguments.delta", `{"type":"response.function_call_arguments.delta","call_id":"call","delta":"{}"}`)},
        {name: "responses_late_id_contradiction", protocol: "responses", before: []privacyStreamFixture{event("response.output_text.delta", `{"type":"response.output_text.delta","output_index":0,"item_id":"item","content_index":0,"delta":"plain"}`)}, bad: event("response.output_text.delta", `{"type":"response.output_text.delta","output_index":0,"item_id":"other","content_index":0,"delta":"x"}`)},
        {name: "responses_index_contradiction", protocol: "responses", before: []privacyStreamFixture{event("response.output_text.delta", `{"type":"response.output_text.delta","output_index":0,"item_id":"item","content_index":0,"delta":"plain"}`)}, bad: event("response.output_text.delta", `{"type":"response.output_text.delta","output_index":1,"item_id":"item","content_index":0,"delta":"x"}`)},
        {name: "responses_two_existing_states", protocol: "responses", before: []privacyStreamFixture{event("response.output_text.delta", `{"type":"response.output_text.delta","output_index":0,"content_index":0,"delta":"plain"}`), event("response.output_text.delta", `{"type":"response.output_text.delta","item_id":"item","content_index":0,"delta":"plain"}`)}, bad: event("response.output_text.delta", `{"type":"response.output_text.delta","output_index":0,"item_id":"item","content_index":0,"delta":"x"}`)},
        {name: "responses_response_id", protocol: "responses", before: []privacyStreamFixture{event("response.created", `{"type":"response.created","response":{"id":"response-0"}}`)}, bad: event("response.output_text.delta", `{"type":"response.output_text.delta","response_id":"other","output_index":0,"content_index":0,"delta":"x"}`)},
        {name: "responses_item_payload_id", protocol: "responses", bad: event("response.output_item.added", `{"type":"response.output_item.added","output_index":0,"item_id":"a","item":{"id":"b","type":"message","content":[]}}`)},
        {name: "responses_call_payload_id", protocol: "responses", bad: event("response.output_item.added", `{"type":"response.output_item.added","output_index":0,"call_id":"a","item":{"id":"item","call_id":"b","type":"function_call","arguments":""}}`)},
        {name: "responses_part_kind", protocol: "responses", before: []privacyStreamFixture{event("response.content_part.added", `{"type":"response.content_part.added","output_index":0,"content_index":0,"part":{"type":"output_text","text":""}}`)}, bad: event("response.refusal.delta", `{"type":"response.refusal.delta","output_index":0,"content_index":0,"delta":"x"}`)},
        {name: "responses_retired_field", protocol: "responses", before: []privacyStreamFixture{event("response.output_text.done", `{"type":"response.output_text.done","output_index":0,"content_index":0,"text":"plain"}`)}, bad: event("response.output_text.delta", `{"type":"response.output_text.delta","output_index":0,"content_index":0,"delta":"x"}`)},
        {name: "responses_retired_item", protocol: "responses", before: []privacyStreamFixture{event("response.output_item.done", `{"type":"response.output_item.done","output_index":0,"item":{"id":"item","type":"message","content":[]}}`)}, bad: event("response.output_text.delta", `{"type":"response.output_text.delta","item_id":"item","content_index":0,"delta":"x"}`)},
        {name: "responses_terminal", protocol: "responses", before: privacyStreamTerminal("responses"), bad: event("response.output_text.delta", `{"type":"response.output_text.delta","output_index":0,"content_index":0,"delta":"x"}`)},
        {name: "anthropic_missing_message", protocol: "anthropic", bad: event("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`)},
        {name: "anthropic_missing_index", protocol: "anthropic", before: privacyStreamInitial("anthropic", false), bad: event("content_block_delta", `{"type":"content_block_delta","delta":{"type":"text_delta","text":"x"}}`)},
        {name: "anthropic_unknown_block", protocol: "anthropic", before: privacyStreamInitial("anthropic", false), bad: event("content_block_delta", `{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"x"}}`)},
        {name: "anthropic_kind_conflict", protocol: "anthropic", before: privacyStreamInitial("anthropic", false), bad: event("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{}"}}`)},
        {name: "anthropic_duplicate_start", protocol: "anthropic", before: privacyStreamInitial("anthropic", false), bad: event("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`)},
        {name: "anthropic_message_conflict", protocol: "anthropic", before: privacyStreamInitial("anthropic", false), bad: event("message_start", `{"type":"message_start","message":{"id":"other","content":[]}}`)},
        {name: "anthropic_retired_block", protocol: "anthropic", before: append(privacyStreamInitial("anthropic", false), event("content_block_stop", `{"type":"content_block_stop","index":0}`)), bad: event("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"x"}}`)},
        {name: "anthropic_terminal", protocol: "anthropic", before: append(privacyStreamInitial("anthropic", false), privacyStreamTerminal("anthropic")...), bad: event("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"x"}}`)},
    }
    for _, fixture := range cases {
        t.Run(fixture.name, func(t *testing.T) {
            for _, policy := range []FailurePolicy{Continue, Abort} {
                t.Run(string(policy), func(t *testing.T) {
                    var format llm.APIFormat
                    for _, protocol := range privacyStreamProtocols {
                        if protocol.name == fixture.protocol {
                            format = protocol.format
                        }
                    }
                    fixtures := append(append([]privacyStreamFixture(nil), fixture.before...), fixture.bad)
                    h := privacyStreamNewHarness(t, privacyStreamSnapshot(t, AllScopes, policy), format, fixtures, 0)
                    for index := range fixture.before {
                        h.push(index, nil)
                    }
                    h.through(len(fixture.before))
                    h.push(len(fixture.before), ErrStreamIdentity)
                    if policy == Continue {
                        if h.restorer.Closed() {
                            t.Fatal("continue 关联失败不能提前关闭")
                        }
                        h.finish()
                    } else {
                        h.through(len(fixture.before))
                        if !h.restorer.Closed() {
                            t.Fatal("abort 关联失败应关闭")
                        }
                        commits, err := h.restorer.Finish()
                        if len(commits) != 0 || err != nil {
                            t.Fatal("abort 后 Finish 补发了事件")
                        }
                    }
                })
            }
        })
    }

    t.Run("ResponsesIDFirstAndCallNamespaces", func(t *testing.T) {
        fixtures := []privacyStreamFixture{
            event("response.queued", `{"type":"response.queued","response":{"id":"response-0"}}`),
            event("response.in_progress", `{"type":"response.in_progress","response":{"id":"response-0"}}`),
            privacyStreamEvent("response.output_text.delta", `{"type":"response.output_text.delta","item_id":"item-0","content_index":0,"delta":"[[OCTOPUS_PII:PH"}`, map[string]string{"delta": "13969909666"}),
            privacyStreamEvent("response.output_text.delta", `{"type":"response.output_text.delta","output_index":0,"item_id":"item-0","content_index":0,"delta":"ONE_1]]"}`, map[string]string{"delta": ""}),
            event("response.output_item.added", `{"type":"response.output_item.added","output_index":1,"item":{"id":"function","call_id":"shared-call","type":"function_call","arguments":"{}"}}`),
            privacyStreamEvent("response.output_item.added", `{"type":"response.output_item.added","output_index":2,"item":{"id":"output","call_id":"shared-call","type":"function_call_output","output":"[[OCTOPUS_PII:PHONE_1]]"}}`, map[string]string{"item.output": "13969909666"}),
        }
        fixtures = append(fixtures, privacyStreamTerminal("responses")...)
        privacyStreamRun(t, privacyStreamSnapshot(t, AllScopes, Continue), llm.APIFormatOpenAIResponse, fixtures)
    })
}

func privacyStreamTestFailurePolicy(t *testing.T) {
    for _, protocol := range privacyStreamProtocols {
        for _, policy := range []FailurePolicy{Continue, Abort} {
            t.Run(protocol.name+"/"+string(policy), func(t *testing.T) {
                for _, cause := range []string{"identity", "unsupported", "inner_json"} {
                    t.Run(cause, func(t *testing.T) {
                        fixtures := privacyStreamInitial(protocol.name, false)
                        fixtures = append(fixtures, privacyStreamDelta(protocol.name, false, privacyStreamName, "张三"))
                        committed := len(fixtures)
                        // 已完成的改写不能回滚, 但带未决尾部的整份事件改写必须丢弃.
                        dirty := privacyStreamDelta(protocol.name, false, privacyStreamPhone+"[[OCTO", "")
                        dirty.want = nil
                        fixtures = append(fixtures, dirty, privacyStreamEvent("metadata", `{"metadata":{"text":"[[OCTOPUS_PII:PHONE_1]]"}}`, nil))
                        var bad privacyStreamFixture
                        wantErr := error(ErrStreamIdentity)
                        switch cause {
                        case "identity":
                            switch protocol.name {
                            case "chat":
                                bad = privacyStreamEvent("", `{"choices":[{"delta":{"content":"x"}}]}`, nil)
                            case "responses":
                                bad = privacyStreamEvent("response.output_text.delta", `{"type":"response.output_text.delta","output_index":0,"delta":"x"}`, nil)
                            case "anthropic":
                                bad = privacyStreamEvent("content_block_delta", `{"type":"content_block_delta","index":9,"delta":{"type":"text_delta","text":"x"}}`, nil)
                            }
                        case "unsupported":
                            wantErr = ErrUnsupportedContent
                            switch protocol.name {
                            case "chat":
                                bad = privacyStreamEvent("", `{"choices":[{"index":0,"delta":{"function_call":{"arguments":"{}"}}}]}`, nil)
                            case "responses":
                                bad = privacyStreamEvent("response.shell_call.delta", `{"type":"response.shell_call.delta","output_index":7,"arguments":{"text":"[[OCTOPUS_PII:PHONE_1]]"}}`, nil)
                            case "anthropic":
                                bad = privacyStreamEvent("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"future_text_delta","text":"[[OCTOPUS_PII:PHONE_1]]"}}`, nil)
                            }
                        case "inner_json":
                            wantErr = ErrInvalidJSON
                            switch protocol.name {
                            case "chat":
                                bad = privacyStreamDelta("chat", true, `{"v":"\x"}`, "")
                            case "responses":
                                bad = privacyStreamEvent("response.function_call_arguments.delta", `{"type":"response.function_call_arguments.delta","output_index":7,"delta":"{\"v\":\"\\x\"}"}`, nil)
                            case "anthropic":
                                fixtures = append(fixtures, privacyStreamEvent("content_block_start", `{"type":"content_block_start","index":7,"content_block":{"type":"tool_use","id":"tool-7","name":"privacy_echo","input":{}}}`, nil))
                                bad = privacyStreamEvent("content_block_delta", `{"type":"content_block_delta","index":7,"delta":{"type":"input_json_delta","partial_json":"{\"v\":\"\\x\"}"}}`, nil)
                            }
                            bad.want = nil
                        }
                        badIndex := len(fixtures)
                        fixtures = append(fixtures, bad)
                        later := privacyStreamDelta(protocol.name, false, privacyStreamPhone, "")
                        later.want = nil
                        fixtures = append(fixtures, later)
                        h := privacyStreamNewHarness(t, privacyStreamSnapshot(t, AllScopes, policy), protocol.format, fixtures, 0)
                        for index := 0; index < badIndex; index++ {
                            h.push(index, nil)
                            if index >= committed {
                                h.through(committed)
                            }
                        }
                        h.push(badIndex, wantErr)
                        if policy == Continue {
                            h.through(badIndex + 1)
                            if h.restorer.Closed() {
                                t.Fatal("continue 失败后应永久透过, 不关闭")
                            }
                            h.push(badIndex + 1, nil)
                            h.through(len(fixtures))
                            h.finish()
                        } else {
                            h.through(committed)
                            if !h.restorer.Closed() {
                                t.Fatal("abort 失败后未关闭")
                            }
                            if commits, err := h.restorer.Push(h.inputs[badIndex+1]); len(commits) != 0 || err != ErrStreamIdentity {
                                t.Fatal("abort 后仍提交后续事件")
                            }
                            if commits, err := h.restorer.Finish(); len(commits) != 0 || err != nil {
                                t.Fatal("abort 后收尾补发")
                            }
                        }
                    })
                }
            })
        }
    }
}

func privacyStreamTestLifecycle(t *testing.T) {
    upstreamErr := errors.New("合成上游读取失败")
    for _, protocol := range privacyStreamProtocols {
        for _, embedded := range []bool{false, true} {
            for _, readErr := range []error{nil, upstreamErr} {
                t.Run(fmt.Sprintf("%s/EOF/embedded_%t/error_%t", protocol.name, embedded, readErr != nil), func(t *testing.T) {
                    fixtures := privacyStreamInitial(protocol.name, embedded)
                    first, last := "pre[[OCTOPUS_PII:PH", "ONE_1]]post[[OCTO"
                    wantFirst, wantLast := "pre13969909666", "post[[OCTO"
                    if embedded {
                        first, last = `{"v":"[[OCTOPUS_PII:PH`, `ONE_1]]\u0`
                        wantFirst, wantLast = `{"v":"13969909666`, `\u0`
                    }
                    fixtures = append(fixtures, privacyStreamDelta(protocol.name, embedded, first, wantFirst), privacyStreamEvent("metadata", `{"metadata":{"text":"[[OCTOPUS_PII:PHONE_1]]"}}`, nil), privacyStreamDelta(protocol.name, embedded, last, wantLast))
                    h := privacyStreamNewHarness(t, privacyStreamSnapshot(t, AllScopes, Continue), protocol.format, fixtures, 0)
                    reader := &privacyStreamReader{body: strings.NewReader(privacyStreamWire(fixtures)), step: 1, tail: readErr}
                    decoder := httpclient.NewDefaultSSEDecoder(t.Context(), reader)
                    count := 0
                    for decoder.Next() {
                        // 用同一真实 decoder 驱动 Push, executor 元数据由 harness
                        // 输入提供, 仅复制载体附加字段而不改写 Data.
                        event := decoder.Current()
                        event.Headers = h.inputs[count].Headers.Clone()
                        event.CleanEOFCompletionEvidence = h.inputs[count].CleanEOFCompletionEvidence
                        event.Size = h.inputs[count].Size
                        commits, err := h.restorer.Push(event)
                        if err != nil {
                            t.Fatal(err)
                        }
                        h.check(commits)
                        count++
                    }
                    if count != len(fixtures) || !errors.Is(decoder.Err(), readErr) {
                        t.Fatalf("读取结果 %d/%d, got %v, want %v", count, len(fixtures), decoder.Err(), readErr)
                    }
                    h.through(len(privacyStreamInitial(protocol.name, embedded)))
                    // Finish 用于正常 EOF 和读取错误, 不补括号、引号或终止事件.
                    h.finish()
                    if err := decoder.Close(); err != nil || reader.closes != 1 {
                        t.Fatal("读取结束未关闭本地 body")
                    }
                })
            }
        }
        t.Run(protocol.name+"/ErrorEvent", func(t *testing.T) {
            fixtures := privacyStreamInitial(protocol.name, false)
            fixtures = append(fixtures, privacyStreamDelta(protocol.name, false, "[[OCTOPUS_PII:PH", "[[OCTOPUS_PII:PH"))
            data, path := `{"error":{"message":"[[OCTOPUS_PII:NAME]]","type":"upstream_error","code":"[[OCTOPUS_PII:PHONE_1]]"}}`, "error.message"
            if protocol.name == "responses" {
                data, path = `{"type":"error","message":"[[OCTOPUS_PII:NAME]]","code":"[[OCTOPUS_PII:PHONE_1]]","sequence_number":900719925474099312345}`, "message"
            } else if protocol.name == "anthropic" {
                data = `{"type":"error","error":{"type":"overloaded_error","message":"[[OCTOPUS_PII:NAME]]"}}`
            }
            fixtures = append(fixtures, privacyStreamEvent("error", data, map[string]string{path: "张三"}))
            privacyStreamRun(t, privacyStreamSnapshot(t, AllScopes, Continue), protocol.format, fixtures)
        })
        t.Run(protocol.name+"/Cancel", func(t *testing.T) {
            fixtures := privacyStreamInitial(protocol.name, false)
            pending := privacyStreamDelta(protocol.name, false, privacyStreamPhone+"[[OCTO", "")
            pending.want = nil
            fixtures = append(fixtures, pending, privacyStreamDelta(protocol.name, false, privacyStreamPhone, "13969909666"))
            h := privacyStreamNewHarness(t, privacyStreamSnapshot(t, AllScopes, Continue), protocol.format, fixtures, 0)
            ctx, cancel := context.WithCancel(t.Context())
            defer cancel()
            reader := &privacyStreamReader{body: strings.NewReader(privacyStreamWire(fixtures))}
            decoder := httpclient.NewDefaultSSEDecoder(ctx, reader)
            for index := 0; index < len(fixtures)-1; index++ {
                if !decoder.Next() {
                    t.Fatalf("取消前 Next: %v", decoder.Err())
                }
                h.push(index, nil)
            }
            h.through(len(fixtures) - 2)
            cancel()
            if decoder.Next() || !errors.Is(decoder.Err(), context.Canceled) {
                t.Fatalf("取消后 decoder 未报告取消: %v", decoder.Err())
            }
            h.restorer.Cancel()
            h.restorer.Cancel()
            if !h.restorer.Closed() {
                t.Fatal("Cancel 后未关闭")
            }
            if commits, err := h.restorer.Finish(); len(commits) != 0 || err != nil {
                t.Fatal("取消补发了未提交数据")
            }
            if commits, err := h.restorer.Push(h.inputs[len(fixtures)-1]); len(commits) != 0 || err != ErrStreamIdentity {
                t.Fatal("取消后仍接收事件")
            }
            if decoder.Close() != nil || decoder.Close() != nil || reader.closes != 1 {
                t.Fatal("取消后 decoder Close 不幂等")
            }
        })
        t.Run(protocol.name+"/OuterInvalidIsFatal", func(t *testing.T) {
            for _, policy := range []FailurePolicy{Continue, Abort} {
                for _, invalid := range []string{`{"choices":`, `null`, `[]`, "{\"x\":\"\xff\"}"} {
                    fixtures := privacyStreamInitial(protocol.name, false)
                    pending := privacyStreamDelta(protocol.name, false, "[[OCTO", "")
                    pending.want = nil
                    fixtures = append(fixtures, pending, privacyStreamEvent("", invalid, nil))
                    h := privacyStreamNewHarness(t, privacyStreamSnapshot(t, AllScopes, policy), protocol.format, fixtures, 0)
                    for index := 0; index < len(fixtures)-1; index++ {
                        h.push(index, nil)
                    }
                    h.push(len(fixtures)-1, ErrInvalidJSON)
                    h.through(len(fixtures)-2)
                    if !h.restorer.Closed() {
                        t.Fatal("外层非法 JSON 即使 continue 也必须终止")
                    }
                    if commits, err := h.restorer.Finish(); len(commits) != 0 || err != nil {
                        t.Fatal("非法外层事件补发未决队列")
                    }
                }
            }
        })
    }
    t.Run("EmptyAndNil", func(t *testing.T) {
        restorer := NewStreamRestorer(nil, llm.APIFormatOpenAIChatCompletion)
        if commits, err := restorer.Push(nil); len(commits) != 0 || err != ErrStreamIdentity || restorer.Closed() {
            t.Fatal("nil 事件处理契约错误")
        }
        if commits, err := restorer.Finish(); len(commits) != 0 || err != nil || !restorer.Closed() {
            t.Fatal("空流收尾错误")
        }
        // 标准 io.EOF 不应被 decoder.Err 转成上游失败.
        reader := &privacyStreamReader{body: strings.NewReader(""), tail: io.EOF}
        decoder := httpclient.NewDefaultSSEDecoder(t.Context(), reader)
        if decoder.Next() || decoder.Err() != nil || decoder.Close() != nil {
            t.Fatal("空 SSE EOF 错误")
        }
    })
}

func privacyStreamTestLiteralsAndScopes(t *testing.T) {
    literals := []struct {
        input, want string
    }{
        {"前张三/😀/é/é/尾随 "+privacyStreamPhone+"后", "前张三/😀/é/é/尾随 13969909666后"},
        {"[[OCTOPUS_PII:LONG_NAME]]/[[OCTOPUS_PII:LOWER]]/[[OCTOPUS_PII:AB]]/[[OCTOPUS_PII:BC]]/[[OCTOPUS_PII:UPPER]]/[[OCTOPUS_PII:NFC]]/[[OCTOPUS_PII:NFD]]/[[OCTOPUS_PII:TAIL]]", "张三丰/a/ab/bc/A/é/é/尾随 "},
        {"[[OCTOPUS_PII:ESCAPED]]", "行\"一\n路径\\尾"},
        {"[[OCTOPUS_PII:NUL]]", "x\x00y"},
        {"[[OCTOPUS_PII:NESTED]]", privacyStreamPhone},
        {"[[OCTOPUS_PII:SELF]]", "[[OCTOPUS_PII:SELF]]"},
        {"[[OCTOPUS_PII:UNKNOWN]]/[[OCTOPUS_PII:bad]]/[[OCTOPUS_PII:DISABLED]]", "[[OCTOPUS_PII:UNKNOWN]]/[[OCTOPUS_PII:bad]]/[[OCTOPUS_PII:DISABLED]]"},
        {"[[[[OCTOPUS_PII:NAME]]", "[[张三"},
        {"[[OCTOPUS_PII:PH", "[[OCTOPUS_PII:PH"},
        {`\u005b\u005bOCTOPUS_PII:PHONE_1]]`, `\u005b\u005bOCTOPUS_PII:PHONE_1]]`},
    }
    for _, protocol := range privacyStreamProtocols {
        t.Run(protocol.name, func(t *testing.T) {
            for index, literal := range literals {
                t.Run(fmt.Sprint(index), func(t *testing.T) {
                    fixtures := privacyStreamInitial(protocol.name, false)
                    fixtures = append(fixtures, privacyStreamDelta(protocol.name, false, literal.input, literal.want))
                    fixtures = append(fixtures, privacyStreamTerminal(protocol.name)...)
                    h := privacyStreamNewHarness(t, privacyStreamSnapshot(t, AllScopes, Continue), protocol.format, fixtures, 1)
                    for index := range h.inputs {
                        h.push(index, nil)
                    }
                    h.finish()
                })
            }
            // 无规则、范围关及总开关关均原样透过, 不触发 Changed.
            inactive := privacyStreamSnapshot(t, 0, Continue)
            disabled, err := Compile(Config{Enabled: false, Scopes: AllScopes, FailurePolicy: Continue}, []Rule{{Source: "13969909666", Alias: "PHONE_1", Enabled: true}}, 1)
            if err != nil {
                t.Fatal(err)
            }
            empty, err := Compile(Config{Enabled: true, Scopes: AllScopes, FailurePolicy: Continue}, nil, 1)
            if err != nil {
                t.Fatal(err)
            }
            for _, snapshot := range []*Snapshot{nil, inactive, disabled, empty} {
                delta := privacyStreamDelta(protocol.name, false, privacyStreamPhone, privacyStreamPhone)
                privacyStreamRun(t, snapshot, protocol.format, []privacyStreamFixture{delta})
            }
            for _, scopes := range []Scope{ToolDefinitions, ToolCalls, ToolResponses} {
                fixtures := privacyStreamInitial(protocol.name, false)
                fixtures = append(fixtures, privacyStreamDelta(protocol.name, false, privacyStreamPhone, privacyStreamPhone))
                fixtures = append(fixtures, privacyStreamTerminal(protocol.name)...)
                privacyStreamRun(t, privacyStreamSnapshot(t, scopes, Continue), protocol.format, fixtures)
            }
            // 默认 Messages 范围关闭工具参数恢复, 即使工具文档不是合法内层 JSON.
            fixtures := privacyStreamInitial(protocol.name, true)
            raw := `not JSON [[OCTOPUS_PII:PHONE_1]]`
            fixtures = append(fixtures, privacyStreamDelta(protocol.name, true, raw, raw))
            fixtures = append(fixtures, privacyStreamTerminal(protocol.name)...)
            privacyStreamRun(t, privacyStreamSnapshot(t, Messages, Continue), protocol.format, fixtures)
        })
    }
}

func privacyStreamTestIndependentStreams(t *testing.T) {
    t.Run("ChatMultipleToolsAndChoiceFields", func(t *testing.T) {
        fixtures := []privacyStreamFixture{
            privacyStreamEvent("", `{"choices":[{"index":0,"delta":{"content":"[[OCTOPUS_PII:PH","refusal":"ONE_1]]","tool_calls":[{"index":0,"function":{"arguments":"{\"v\":\"[[OCTOPUS_PII:PH"}},{"index":1,"function":{"arguments":"{\"v\":\"[[OCTOPUS_PII:NA"}}]}}]}`, map[string]string{"choices.0.delta.tool_calls.0.function.arguments": `{"v":"13969909666`, "choices.0.delta.tool_calls.1.function.arguments": `{"v":"张三`}),
            privacyStreamEvent("", `{"choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"id":"late-1","function":{"arguments":"ME]]\"}"}},{"index":0,"id":"late-0","function":{"arguments":"ONE_1]]\"}"}}]}}]}`, map[string]string{"choices.0.delta.tool_calls.0.function.arguments": `"}`, "choices.0.delta.tool_calls.1.function.arguments": `"}`}),
        }
        fixtures = append(fixtures, privacyStreamTerminal("chat")...)
        privacyStreamRun(t, privacyStreamSnapshot(t, AllScopes, Continue), llm.APIFormatOpenAIChatCompletion, fixtures)
    })
    t.Run("ResponsesDifferentItemsAndContents", func(t *testing.T) {
        fixtures := []privacyStreamFixture{
            privacyStreamEvent("response.output_text.delta", `{"type":"response.output_text.delta","output_index":0,"content_index":0,"delta":"[[OCTOPUS_PII:PH"}`, nil),
            privacyStreamEvent("response.output_text.delta", `{"type":"response.output_text.delta","output_index":1,"content_index":0,"delta":"ONE_1]]"}`, nil),
            privacyStreamEvent("response.output_text.delta", `{"type":"response.output_text.delta","output_index":0,"content_index":1,"delta":"ONE_1]]"}`, nil),
        }
        fixtures = append(fixtures, privacyStreamTerminal("responses")...)
        privacyStreamRun(t, privacyStreamSnapshot(t, AllScopes, Continue), llm.APIFormatOpenAIResponse, fixtures)
    })
    t.Run("AnthropicDifferentBlocks", func(t *testing.T) {
        fixtures := privacyStreamInitial("anthropic", false)
        fixtures = append(fixtures,
            privacyStreamDelta("anthropic", false, "[[OCTOPUS_PII:PH", "[[OCTOPUS_PII:PH"),
            privacyStreamEvent("content_block_start", `{"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}`, nil),
            privacyStreamEvent("content_block_delta", `{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"ONE_1]]"}}`, nil),
        )
        fixtures = append(fixtures, privacyStreamTerminal("anthropic")...)
        privacyStreamRun(t, privacyStreamSnapshot(t, AllScopes, Continue), llm.APIFormatAnthropicMessage, fixtures)
    })
    // 同一工具的两个业务 value 也不拼 marker, JSON key 不进入恢复器.
    for _, protocol := range privacyStreamProtocols {
        t.Run(protocol.name+"/InnerValuesAndKeys", func(t *testing.T) {
            fixtures := privacyStreamInitial(protocol.name, true)
            input := `{"[[OCTOPUS_PII:PHONE_1]]":"[[OCTOPUS_PII:PH","b":"ONE_1]]","id":"[[OCTOPUS_PII:NAME]]"}`
            want := `{"[[OCTOPUS_PII:PHONE_1]]":"[[OCTOPUS_PII:PH","b":"ONE_1]]","id":"张三"}`
            fixtures = append(fixtures, privacyStreamDelta(protocol.name, true, input, want))
            fixtures = append(fixtures, privacyStreamTerminal(protocol.name)...)
            privacyStreamRun(t, privacyStreamSnapshot(t, AllScopes, Continue), protocol.format, fixtures)
        })
    }
}

func privacyStreamTestSnapshotsAndStops(t *testing.T) {
    for _, terminal := range []string{"response.completed", "response.failed", "response.incomplete", "response.cancelled"} {
        t.Run(terminal, func(t *testing.T) {
            fixtures := []privacyStreamFixture{
                privacyStreamEvent("response.created", `{"type":"response.created","response":{"id":"response-0"}}`, nil),
                privacyStreamEvent("response.output_item.added", `{"type":"response.output_item.added","output_index":0,"item":{"id":"function","call_id":"call","type":"function_call","arguments":"{\"v\":\"[[OCTOPUS_PII:NAME]]\"}"}}`, map[string]string{"item.arguments": `{"v":"张三"}`}),
                privacyStreamEvent("response.function_call_arguments.delta", `{"type":"response.function_call_arguments.delta","item_id":"function","delta":"{\"v\":\"[[OCTOPUS_PII:PH"}`, map[string]string{"delta": `{"v":"13969909666`}),
                privacyStreamEvent("response.function_call_arguments.delta", `{"type":"response.function_call_arguments.delta","item_id":"function","delta":"ONE_1]]\"}"}`, map[string]string{"delta": `"}`}),
                privacyStreamEvent("response.output_item.done", `{"type":"response.output_item.done","output_index":0,"item":{"id":"function","type":"function_call","arguments":"{\"v\":\"[[OCTOPUS_PII:NAME]]\"}"}}`, map[string]string{"item.arguments": `{"v":"张三"}`}),
                privacyStreamEvent(terminal, `{"type":"`+terminal+`","response":{"id":"response-0","status":"failed","output":[{"type":"message","content":[{"type":"refusal","refusal":"[[OCTOPUS_PII:PHONE_1]]"}]}],"error":{"message":"[[OCTOPUS_PII:NAME]]","code":"unchanged"}}}`, map[string]string{"response.output.0.content.0.refusal": "13969909666", "response.error.message": "张三"}),
                // 终态后完整 done 快照仍独立处理, 不需要保留增量状态.
                privacyStreamEvent("response.function_call_arguments.done", `{"type":"response.function_call_arguments.done","arguments":"{\"v\":\"[[OCTOPUS_PII:PHONE_1]]\"}"}`, map[string]string{"arguments": `{"v":"13969909666"}`}),
                privacyStreamEvent("response.refusal.done", `{"type":"response.refusal.done","refusal":"[[OCTOPUS_PII:NAME]]"}`, map[string]string{"refusal": "张三"}),
                privacyStreamEvent("response.output_text.done", `{"type":"response.output_text.done","text":"[[OCTOPUS_PII:PHONE_1]]"}`, map[string]string{"text": "13969909666"}),
                privacyStreamEvent("response.custom_tool_call_input.done", `{"type":"response.custom_tool_call_input.done","input":"patch [[OCTOPUS_PII:NAME]]"}`, map[string]string{"input": "patch 张三"}),
                privacyStreamEvent("response.content_part.done", `{"type":"response.content_part.done","part":{"type":"refusal","refusal":"[[OCTOPUS_PII:NAME]]"}}`, map[string]string{"part.refusal": "张三"}),
                privacyStreamEvent("response.output_item.done", `{"type":"response.output_item.done","item":{"type":"message","content":[{"type":"output_text","text":"[[OCTOPUS_PII:NAME]]"}]}}`, map[string]string{"item.content.0.text": "张三"}),
            }
            privacyStreamRun(t, privacyStreamSnapshot(t, AllScopes, Continue), llm.APIFormatOpenAIResponse, fixtures)
        })
    }
    // 明确 stop/done 不得把非法完整工具文档当作允许残缺 EOF 来补全.
    for _, protocol := range privacyStreamProtocols {
        for _, policy := range []FailurePolicy{Continue, Abort} {
            t.Run(protocol.name+"/StrictStop/"+string(policy), func(t *testing.T) {
                fixtures := privacyStreamInitial(protocol.name, true)
                partial := privacyStreamDelta(protocol.name, true, `{"v":"[[OCTOPUS_PII:PH`, "")
                partial.want = nil
                fixtures = append(fixtures, partial)
                committed := len(fixtures) - 1
                terminal := privacyStreamTerminal(protocol.name)[0]
                if protocol.name == "chat" {
                    // DONE 自身也严格结束工具词法流.
                    terminal = privacyStreamEvent("", `[DONE]`, nil)
                }
                fixtures = append(fixtures, terminal)
                h := privacyStreamNewHarness(t, privacyStreamSnapshot(t, AllScopes, policy), protocol.format, fixtures, 0)
                for index := 0; index < len(fixtures)-1; index++ {
                    h.push(index, nil)
                }
                h.through(committed)
                h.push(len(fixtures)-1, ErrInvalidJSON)
                if policy == Continue {
                    h.finish()
                } else {
                    h.through(committed)
                    if !h.restorer.Closed() {
                        t.Fatal("abort 严格终止失败未关闭")
                    }
                }
            })
        }
        t.Run(protocol.name+"/ErrorFlushesIncompleteTool", func(t *testing.T) {
            fixtures := privacyStreamInitial(protocol.name, true)
            raw := `{"v":"[[OCTOPUS_PII:PH`
            fixtures = append(fixtures, privacyStreamDelta(protocol.name, true, raw, raw), privacyStreamEvent("error", `{"type":"error","error":{"message":"[[OCTOPUS_PII:NAME]]","type":"upstream_error"}}`, map[string]string{"error.message": "张三"}))
            privacyStreamRun(t, privacyStreamSnapshot(t, AllScopes, Continue), protocol.format, fixtures)
        })
    }
}

func privacyStreamTestQueuedCopy(t *testing.T) {
    // Push 之后调用方可复用输入缓冲. 只验证公开提交载体, 队列内部审查由父 agent 完成.
    fixtures := []privacyStreamFixture{
        privacyStreamDelta("chat", false, "[[OCTOPUS_PII:PH", "13969909666"),
        privacyStreamEvent("heartbeat", "", nil),
        privacyStreamEvent("heartbeat", `{ "usage" : { "completion_tokens" : 0 } }`, nil),
        privacyStreamDelta("chat", false, "ONE_1]]", ""),
    }
    h := privacyStreamNewHarness(t, privacyStreamSnapshot(t, AllScopes, Continue), llm.APIFormatOpenAIChatCompletion, fixtures, 0)
    saved := *h.inputs[0]
    saved.Data = bytes.Clone(saved.Data)
    saved.Headers = saved.Headers.Clone()
    h.push(0, nil)
    h.through(0)
    // 故意改写已交入的 Data、Header 和标量, 检查副本而非原指针被保留.
    for index := range h.inputs[0].Data {
        h.inputs[0].Data[index] = 'x'
    }
    h.inputs[0].Headers["X-Fixture"][0] = "mutated"
    h.inputs[0].Type = "mutated"
    h.inputs[0].LastEventID = "mutated"
    h.inputs[0].CleanEOFCompletionEvidence = false
    h.inputs[0].Size = -1
    h.inputs[0] = &saved
    h.push(1, nil)
    h.through(0)
    h.push(2, nil)
    h.through(0)
    h.push(3, nil)
    h.through(4)
    h.finish()
}

func TestPrivacyStream(t *testing.T) {
    t.Run("Cuts", privacyStreamTestCuts)
    t.Run("ChatInterleaved", privacyStreamTestChat)
    t.Run("ResponsesInterleaved", privacyStreamTestResponses)
    t.Run("AnthropicInterleaved", privacyStreamTestAnthropic)
    t.Run("Identity", privacyStreamTestIdentity)
    t.Run("FailurePolicy", privacyStreamTestFailurePolicy)
    t.Run("Lifecycle", privacyStreamTestLifecycle)
    t.Run("LiteralsAndScopes", privacyStreamTestLiteralsAndScopes)
    t.Run("IndependentStreams", privacyStreamTestIndependentStreams)
    t.Run("SnapshotsAndStops", privacyStreamTestSnapshotsAndStops)
    t.Run("QueuedCopy", privacyStreamTestQueuedCopy)
}
