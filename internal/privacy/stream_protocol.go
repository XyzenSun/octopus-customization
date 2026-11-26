package privacy

import (
    "encoding/json"
    "strings"

    "github.com/looplj/axonhub/llm"
)

// 分类器管理协议身份, 不管理文本/JSON 解析器或 FIFO.
// Responses 规范键始终不变, 即使仅有 ID 的 item 后续
// 绑定到 output_index. call_id 使用独立命名空间,
// 不能替代 item_id.
//
// streamKey 未使用的索引为 -1. group 为 chat/responses/anthropic;
// field 为 content/refusal/arguments/input/text. 每个 StreamRestorer 独享分类器.
// 零值可直接使用.
type streamClassifier struct {
    chat        map[int]*streamChatChoice
    chatRetired map[int]Scope

    responseID string
    items      map[int]*streamResponseItem
    itemIndex  map[int]int
    itemID     map[string]int
    callID     map[streamCallIdentity]int
    nextItem   int

    messageID string
    blocks    map[int]*streamAnthropicBlock
    blockGone map[int]Scope

    active map[streamKey]struct{}
    closed map[streamKey]struct{}
}

type streamChatChoice struct {
    role  string
    tools map[int]string
    ids   map[string]int
}

type streamCallIdentity struct {
    family string // call 和 output 可以合法引用同一 call_id.
    id     string
}

type streamResponseItem struct {
    key     int
    index   int
    indexed bool
    id      string
    call    string
    kind    string
    begun   bool
    retired bool
    parts   map[int]*streamResponsePart
}

type streamResponsePart struct {
    kind    string
    begun   bool
    retired bool
}

type streamAnthropicBlock struct {
    kind  string
    id    string
    scope Scope
    begun bool
}

func (c *streamClassifier) classify(s *StreamRestorer, e *streamEntry, root streamObject) error {
    if !s.snapshot.Active() {
        return nil
    }
    // 即使消息 scope 未启用, 错误也代表语义终止.
    // Chat 无事件判别字段: 任意顶层 metadata
    // 不得视为 Responses/Anthropic 身份信息.
    kind := e.original.Type
    var err error
    if s.format != llm.APIFormatOpenAIChatCompletion {
        kind, err = c.eventType(e, root)
    }
    if err != nil {
        return err
    }
    if kind == "error" || rawKind(root["error"]) == '{' {
        if err := c.terminate(s, true); err != nil {
            return err
        }
        return s.snapshotField(e, nil, e.original.Data, "error.event")
    }
    switch s.format {
    case llm.APIFormatOpenAIChatCompletion:
        return c.classifyChat(s, e, root)
    case llm.APIFormatOpenAIResponse:
        return c.classifyResponses(s, e, root, kind)
    case llm.APIFormatAnthropicMessage:
        return c.classifyAnthropic(s, e, root, kind)
    default:
        return ErrUnsupportedContent
    }
}

func (c *streamClassifier) eventType(e *streamEntry, root streamObject) (string, error) {
    // Chat chunk 无 JSON 事件判别字段. SSE 默认 "message"
    // 载体不得遮蔽 Responses/Anthropic 的 data.type.
    kind, err := jsonTag(root, "type")
    if err != nil {
        return "", err
    }
    carrier := e.original.Type
    if carrier == "" || carrier == "message" {
        return kind, nil
    }
    // 保持原载体不变. JSON 判别字段描述载荷;
    // 传输 metadata 不属于逻辑流身份.
    if kind != "" {
        return kind, nil
    }
    return carrier, nil
}

// pendingBytes 计入固定身份/墓碑记录, map 键和所有
// 保留的身份字符串 (含别名 map 引用的副本).
// 常量保守计入 map 条目及其标量字段;
// 上层另计原始事件, 候选及解析器/片段缓冲区.
func (c *streamClassifier) pendingBytes() int {
    n := len(c.responseID) + len(c.messageID)
    n += 32 * (len(c.chatRetired) + len(c.blockGone))
    for _, choice := range c.chat {
        n += 96 + len(choice.role)
        for _, id := range choice.tools {
            n += 48 + len(id)
        }
        for id := range choice.ids {
            n += 48 + len(id)
        }
    }
    for _, item := range c.items {
        n += 160 + len(item.id) + len(item.call) + len(item.kind)
        for _, part := range item.parts {
            n += 64 + len(part.kind)
        }
    }
    n += 48 * len(c.itemIndex)
    for id := range c.itemID {
        n += 48 + len(id)
    }
    for id := range c.callID {
        n += 64 + len(id.id) + len(id.family)
    }
    for _, block := range c.blocks {
        n += 80 + len(block.id) + len(block.kind)
    }
    for key := range c.active {
        n += 96 + len(key.group) + len(key.field)
    }
    for key := range c.closed {
        n += 96 + len(key.group) + len(key.field)
    }
    return n
}

func (c *streamClassifier) push(s *StreamRestorer, e *streamEntry, path streamPath, key streamKey, raw json.RawMessage, embedded bool) error {
    if s.terminal {
        return ErrStreamIdentity
    }
    if _, retired := c.closed[key]; retired {
        return ErrStreamIdentity
    }
    text, err := DecodeJSONString(raw)
    if err != nil {
        return err
    }
    if c.active == nil {
        c.active = make(map[streamKey]struct{})
    }
    c.active[key] = struct{}{}
    return s.delta(e, path, key, text, embedded)
}

func (c *streamClassifier) finish(s *StreamRestorer, predicate func(streamKey) bool, allowIncomplete bool) error {
    if err := s.finishStreams(predicate, allowIncomplete); err != nil {
        return err
    }
    for key := range c.active {
        if predicate == nil || predicate(key) {
            c.closeKey(key)
            delete(c.active, key)
        }
    }
    if len(c.active) == 0 {
        c.active = nil
    }
    return nil
}

func (c *streamClassifier) closeKey(key streamKey) {
    if c.closed == nil {
        c.closed = make(map[streamKey]struct{})
    }
    c.closed[key] = struct{}{}
}

func (c *streamClassifier) forgetClosed(predicate func(streamKey) bool) {
    for key := range c.closed {
        if predicate(key) {
            delete(c.closed, key)
        }
    }
    if len(c.closed) == 0 {
        c.closed = nil
    }
}

func (c *streamClassifier) terminate(s *StreamRestorer, allowIncomplete bool) error {
    if err := s.finishStreams(nil, allowIncomplete); err != nil {
        return err
    }
    s.terminal = true
    // terminal 拦截所有选中的迟到 delta, 因此整个响应结束后
    // 无需保留别名或墓碑. metadata 和 done 快照仍可通过.
    *c = streamClassifier{}
    return nil
}

func streamIndex(obj streamObject, name string) (int, bool, error) {
    raw, exists := obj[name]
    if !exists {
        return 0, false, nil
    }
    var index int
    if rawKind(raw) != '0' && (rawKind(raw) < '1' || rawKind(raw) > '9') {
        return 0, true, ErrStreamIdentity
    }
    if json.Unmarshal(raw, &index) != nil || index < 0 {
        return 0, true, ErrStreamIdentity
    }
    return index, true, nil
}

func streamRequiredIndex(obj streamObject, name string) (int, error) {
    index, exists, err := streamIndex(obj, name)
    if err != nil || !exists {
        return 0, ErrStreamIdentity
    }
    return index, nil
}

func streamID(obj streamObject, name string) (string, error) {
    raw, exists := obj[name]
    if !exists || rawKind(raw) == 'n' {
        return "", nil
    }
    id, err := DecodeJSONString(raw)
    if err != nil {
        return "", ErrStreamIdentity
    }
    return id, nil
}

func streamArray(raw json.RawMessage) ([]json.RawMessage, error) {
    if rawKind(raw) != '[' {
        return nil, ErrInvalidJSON
    }
    var array []json.RawMessage
    if json.Unmarshal(raw, &array) != nil {
        return nil, ErrInvalidJSON
    }
    return array, nil
}

func streamPresent(obj streamObject, name string) bool {
    raw, exists := obj[name]
    return exists && rawKind(raw) != 'n'
}

func streamChatKey(choice, tool int, field string) streamKey {
    return streamKey{group: "chat", choice: choice, item: -1, content: -1, tool: tool, field: field}
}

func (c *streamClassifier) classifyChat(s *StreamRestorer, e *streamEntry, root streamObject) error {
    raw, exists := root["choices"]
    if !exists || !s.snapshot.Processes(Messages|ToolCalls|ToolResponses) {
        return nil // 包括仅含 usage 的 chunk.
    }
    choices, err := streamArray(raw)
    if err != nil {
        return err
    }
    for position, rawChoice := range choices {
        choice, err := decodeObject(rawChoice)
        if err != nil {
            return err
        }
        if err := c.chatChoiceDelta(s, e, choice, position); err != nil {
            return err
        }
        // 依次处理 delta, choice 结束, 独立完整消息快照.
        // 单独的快照无需 choice 索引.
        if streamPresent(choice, "message") {
            if err := s.snapshotField(e, streamPath{"choices", position, "message"}, choice["message"], "chat.message"); err != nil {
                return err
            }
        }
    }
    return nil
}

func (c *streamClassifier) chatChoiceDelta(s *StreamRestorer, e *streamEntry, choice streamObject, position int) error {
    var delta streamObject
    var err error
    if streamPresent(choice, "delta") {
        delta, err = decodeObject(choice["delta"])
        if err != nil {
            return err
        }
    }
    role := ""
    if s.snapshot.Processes(Messages | ToolResponses) {
        role, err = jsonTag(delta, "role")
        if err != nil {
            return err
        }
    }
    explicitRole := role != ""
    index, hasIndex, indexErr := streamIndex(choice, "index")
    var identity *streamChatChoice
    if hasIndex && indexErr == nil {
        identity = c.chat[index]
    }
    if role == "" && identity != nil {
        role = identity.role
    }
    contentScope := Messages
    if role == "tool" {
        contentScope = ToolResponses
    } else if role == "" {
        if retiredScope, retired := c.chatRetired[index]; hasIndex && indexErr == nil && retired {
            contentScope = retiredScope
        }
    }
    selectedContent := streamPresent(delta, "content") && s.snapshot.Processes(contentScope)
    selectedRefusal := streamPresent(delta, "refusal") && s.snapshot.Processes(Messages)
    selectedTools := streamPresent(delta, "tool_calls") && s.snapshot.Processes(ToolCalls)
    if s.snapshot.Processes(ToolCalls) && streamPresent(delta, "function_call") {
        if s.terminal || hasIndex && indexErr == nil && c.chatChoiceRetired(index) {
            return ErrStreamIdentity
        }
        return ErrUnsupportedContent
    }
    if s.terminal || hasIndex && indexErr == nil && c.chatChoiceRetired(index) {
        if selectedContent || selectedRefusal {
            return ErrStreamIdentity
        }
        if selectedTools {
            // 结束后的工具 ID/name metadata 无害; arguments 不允许.
            return c.chatTools(s, e, delta["tool_calls"], position, index, nil)
        }
        return nil
    }
    finishing := streamPresent(choice, "finish_reason")
    selected := selectedContent || selectedRefusal || selectedTools
    if !selected && !explicitRole && !finishing {
        return nil
    }
    if indexErr != nil || !hasIndex {
        if selected || explicitRole && s.snapshot.Processes(contentScope) {
            return ErrStreamIdentity
        }
        return nil
    }
    if selected || explicitRole {
        if identity == nil {
            if c.chat == nil {
                c.chat = make(map[int]*streamChatChoice)
            }
            identity = &streamChatChoice{}
            c.chat[index] = identity
        }
        if role != "" {
            if identity.role != "" && identity.role != role {
                return ErrStreamIdentity
            }
            // 即使 role 未启用也记录其类别, 避免后续
            // 无 role 的 delta 被误判为普通消息.
            identity.role = role
        }
    }
    if selectedContent {
        if rawKind(delta["content"]) != '"' {
            return ErrUnsupportedContent
        }
        path := streamPath{"choices", position, "delta", "content"}
        if err := c.push(s, e, path, streamChatKey(index, -1, "content"), delta["content"], false); err != nil {
            return err
        }
    }
    if selectedRefusal {
        path := streamPath{"choices", position, "delta", "refusal"}
        if err := c.push(s, e, path, streamChatKey(index, -1, "refusal"), delta["refusal"], false); err != nil {
            return err
        }
    }
    if selectedTools {
        if err := c.chatTools(s, e, delta["tool_calls"], position, index, identity); err != nil {
            return err
        }
    }
    if finishing {
        predicate := func(key streamKey) bool { return key.group == "chat" && key.choice == index }
        if err := c.finish(s, predicate, false); err != nil {
            return err
        }
        if c.chatRetired == nil {
            c.chatRetired = make(map[int]Scope)
        }
        c.chatRetired[index] = contentScope
        delete(c.chat, index)
        c.forgetClosed(predicate)
    }
    return nil
}

func (c *streamClassifier) chatChoiceRetired(index int) bool {
    _, exists := c.chatRetired[index]
    return exists
}

func (c *streamClassifier) chatTools(s *StreamRestorer, e *streamEntry, raw json.RawMessage, position, choice int, identity *streamChatChoice) error {
    tools, err := streamArray(raw)
    if err != nil {
        return err
    }
    for positionTool, rawTool := range tools {
        tool, err := decodeObject(rawTool)
        if err != nil {
            return err
        }
        if identity == nil {
            if streamPresent(tool, "function") {
                function, err := decodeObject(tool["function"])
                if err != nil {
                    return err
                }
                if streamPresent(function, "arguments") {
                    return ErrStreamIdentity
                }
            }
            if streamPresent(tool, "custom") {
                return ErrStreamIdentity
            }
            continue
        }
        kind, err := jsonTag(tool, "type")
        if err != nil {
            return err
        }
        if kind != "" && kind != "function" || streamPresent(tool, "custom") {
            return ErrUnsupportedContent
        }
        index, err := streamRequiredIndex(tool, "index")
        if err != nil {
            return err
        }
        id, err := streamID(tool, "id")
        if err != nil {
            return err
        }
        if identity.tools == nil {
            identity.tools = make(map[int]string)
            identity.ids = make(map[string]int)
        }
        if previous := identity.tools[index]; previous != "" && id != "" && previous != id {
            return ErrStreamIdentity
        }
        if id != "" {
            if previous, exists := identity.ids[id]; exists && previous != index {
                return ErrStreamIdentity
            }
            identity.tools[index] = id
            identity.ids[id] = index
        } else if _, exists := identity.tools[index]; !exists {
            identity.tools[index] = ""
        }
        if !streamPresent(tool, "function") {
            continue
        }
        function, err := decodeObject(tool["function"])
        if err != nil {
            return err
        }
        if streamPresent(function, "arguments") {
            path := streamPath{"choices", position, "delta", "tool_calls", positionTool, "function", "arguments"}
            if err := c.push(s, e, path, streamChatKey(choice, index, "arguments"), function["arguments"], true); err != nil {
                return err
            }
        }
    }
    return nil
}

// item scope 由协议判别字段决定, 不使用业务键.
func streamResponseScope(kind string) Scope {
    switch kind {
    case "message", "input_text", "output_text", "text", "refusal":
        return Messages
    case "function_call", "custom_tool_call", "tool_search_call":
        return ToolCalls
    case "function_call_output", "custom_tool_call_output":
        return ToolResponses
    case "web_search_call":
        return ToolCalls | ToolResponses
    case "reasoning", "summary_text", "compaction", "compaction_summary", "input_image", "input_audio", "input_file", "output_audio", "image", "audio", "file", "video", "image_generation_call", "item_reference":
        return 0
    default:
        if strings.HasSuffix(kind, "_call") {
            return ToolCalls
        }
        if strings.HasSuffix(kind, "_output") || strings.HasSuffix(kind, "_result") {
            return ToolResponses
        }
        return Messages | ToolCalls | ToolResponses
    }
}

func streamResponseKey(item, content int, field string) streamKey {
    return streamKey{group: "responses", choice: -1, item: item, content: content, tool: -1, field: field}
}

func (c *streamClassifier) responseIdentity(root, payload streamObject, kind string) (*streamResponseItem, error) {
    responseID, err := streamID(root, "response_id")
    if err != nil {
        return nil, err
    }
    if responseID != "" {
        if c.responseID != "" && c.responseID != responseID {
            return nil, ErrStreamIdentity
        }
        c.responseID = responseID
    }
    index, hasIndex, err := streamIndex(root, "output_index")
    if err != nil {
        return nil, err
    }
    id, err := streamID(root, "item_id")
    if err != nil {
        return nil, err
    }
    payloadID, err := streamID(payload, "id")
    if err != nil {
        return nil, err
    }
    if id != "" && payloadID != "" && id != payloadID {
        return nil, ErrStreamIdentity
    }
    if payloadID != "" {
        id = payloadID
    }
    if !hasIndex && id == "" {
        return nil, ErrStreamIdentity
    }
    call, err := streamID(root, "call_id")
    if err != nil {
        return nil, err
    }
    payloadCall, err := streamID(payload, "call_id")
    if err != nil {
        return nil, err
    }
    if call != "" && payloadCall != "" && call != payloadCall {
        return nil, ErrStreamIdentity
    }
    if payloadCall != "" {
        call = payloadCall
    }
    var item *streamResponseItem
    if hasIndex {
        if key, exists := c.itemIndex[index]; exists {
            item = c.items[key]
        }
    }
    if id != "" {
        if key, exists := c.itemID[id]; exists {
            byID := c.items[key]
            if item != nil && item != byID {
                // 不合并两个已有词法状态, 即使其中一个
                // 是由仅含 ID 的事件临时创建.
                return nil, ErrStreamIdentity
            }
            item = byID
        }
    }
    if item == nil {
        if c.items == nil {
            c.items = make(map[int]*streamResponseItem)
            c.itemIndex = make(map[int]int)
            c.itemID = make(map[string]int)
            c.callID = make(map[streamCallIdentity]int)
        }
        key := index
        if !hasIndex {
            if c.nextItem == 0 {
                c.nextItem = -1
            }
            key = c.nextItem
            // pendingBytes 会提前限制合成身份数量,
            // 防止计数器达到平台 int 最小值.
            c.nextItem--
        }
        item = &streamResponseItem{key: key}
        c.items[key] = item
    }
    if hasIndex {
        if item.indexed && item.index != index {
            return nil, ErrStreamIdentity
        }
        item.index, item.indexed = index, true
        c.itemIndex[index] = item.key
    }
    if id != "" {
        if item.id != "" && item.id != id {
            return nil, ErrStreamIdentity
        }
        item.id = id
        c.itemID[id] = item.key
    }
    if kind != "" {
        if item.kind != "" && item.kind != kind {
            return nil, ErrStreamIdentity
        }
        item.kind = kind
    }
    if call != "" {
        if item.call != "" && item.call != call {
            return nil, ErrStreamIdentity
        }
        family := "call"
        if strings.HasSuffix(item.kind, "_output") || strings.HasSuffix(item.kind, "_result") {
            family = "output"
        }
        alias := streamCallIdentity{family: family, id: call}
        if previous, exists := c.callID[alias]; exists && previous != item.key {
            return nil, ErrStreamIdentity
        }
        item.call = call
        c.callID[alias] = item.key
    }
    return item, nil
}

func (c *streamClassifier) responsePart(item *streamResponseItem, index int, kind string, begin, delta bool) (*streamResponsePart, error) {
    if kind == "input_text" || kind == "output_text" {
        kind = "text"
    }
    if delta && item.retired || begin && item.retired {
        return nil, ErrStreamIdentity
    }
    if item.parts == nil {
        item.parts = make(map[int]*streamResponsePart)
    }
    part := item.parts[index]
    if part == nil {
        part = &streamResponsePart{kind: kind}
        item.parts[index] = part
    }
    if part.kind != "" && kind != "" && part.kind != kind {
        return nil, ErrStreamIdentity
    }
    if kind != "" {
        part.kind = kind
    }
    if delta && part.retired || begin && (part.begun || part.retired) {
        return nil, ErrStreamIdentity
    }
    if begin {
        part.begun = true
    }
    return part, nil
}

func (c *streamClassifier) classifyResponses(s *StreamRestorer, e *streamEntry, root streamObject, kind string) error {
    switch kind {
    case "response.created", "response.in_progress", "response.queued":
        if !s.snapshot.Processes(Messages|ToolCalls|ToolResponses) || s.terminal {
            return nil
        }
        response, err := decodeObject(root["response"])
        if err != nil {
            return err
        }
        id, err := streamID(response, "id")
        if err != nil {
            return err
        }
        if id == "" || c.responseID != "" && c.responseID != id {
            return ErrStreamIdentity
        }
        c.responseID = id
        return nil
    case "response.completed", "response.failed", "response.incomplete", "response.cancelled":
        if streamPresent(root, "response") && s.snapshot.Processes(Messages|ToolCalls|ToolResponses) && !s.terminal {
            response, err := decodeObject(root["response"])
            if err != nil {
                return err
            }
            id, err := streamID(response, "id")
            if err != nil {
                return err
            }
            if id != "" && c.responseID != "" && c.responseID != id {
                return ErrStreamIdentity
            }
        }
        if err := c.terminate(s, false); err != nil {
            return err
        }
        if streamPresent(root, "response") {
            return s.snapshotField(e, streamPath{"response"}, root["response"], "responses.root")
        }
        return nil
    case "response.output_item.added", "response.output_item.done":
        return c.responseItemEvent(s, e, root, kind == "response.output_item.added")
    case "response.content_part.added", "response.content_part.done":
        return c.responsePartEvent(s, e, root, kind == "response.content_part.added")
    case "response.output_text.delta", "response.output_text.done":
        return c.responseTextEvent(s, e, root, "text", "output_text", false, strings.HasSuffix(kind, ".delta"))
    case "response.refusal.delta", "response.refusal.done":
        return c.responseTextEvent(s, e, root, "refusal", "refusal", false, strings.HasSuffix(kind, ".delta"))
    case "response.function_call_arguments.delta", "response.function_call_arguments.done":
        return c.responseTextEvent(s, e, root, "arguments", "function_call", true, strings.HasSuffix(kind, ".delta"))
    case "response.custom_tool_call_input.delta", "response.custom_tool_call_input.done":
        return c.responseTextEvent(s, e, root, "input", "custom_tool_call", false, strings.HasSuffix(kind, ".delta"))
    default:
        // 无载体, metadata, usage, annotations, reasoning 和图像
        // 生成事件均不含选中的 delta 载荷.
        if streamResponseExcludedEvent(kind) {
            return nil
        }
        // 不递归扫描任意载荷来静默支持新的原生工具/文本 delta.
        // 普通未知 metadata 直接通过.
        scope := Scope(0)
        isDelta := strings.HasSuffix(kind, ".delta")
        isDone := strings.HasSuffix(kind, ".done")
        if isDelta && streamPresent(root, "delta") || isDone && (streamPresent(root, "text") || streamPresent(root, "refusal")) {
            if strings.Contains(kind, "text") || strings.Contains(kind, "refusal") {
                scope = Messages
            }
            if strings.Contains(kind, "tool") || strings.Contains(kind, "arguments") {
                scope = ToolCalls
            }
        }
        nativeTool := strings.Contains(kind, "mcp") || strings.Contains(kind, "shell") || strings.Contains(kind, "computer") || strings.Contains(kind, "tool")
        if nativeTool && (streamPresent(root, "arguments") || streamPresent(root, "input")) {
            scope |= ToolCalls
        }
        if nativeTool && (streamPresent(root, "output") || streamPresent(root, "content")) {
            scope |= ToolResponses
        }
        if s.snapshot.Processes(scope) {
            if isDelta && s.terminal {
                return ErrStreamIdentity
            }
            return ErrUnsupportedContent
        }
        return nil
    }
}

func streamResponseExcludedEvent(kind string) bool {
    for _, prefix := range []string{"response.reasoning", "response.image", "response.audio", "response.output_audio", "response.compaction", "response.summary"} {
        if strings.HasPrefix(kind, prefix) {
            return true
        }
    }
    for _, segment := range []string{".annotation", ".citation", ".logprobs", ".usage", ".metadata"} {
        if strings.Contains(kind, segment) {
            return true
        }
    }
    return false
}

// 仅记录类别, 防止文本形态的 delta 重新解释
// 已排除/未启用的 item. 忽略此类 start 中的无效身份:
// 未启用的 scope 不得引入 stream_identity 或能力错误.
func (c *streamClassifier) rememberResponseCategory(root, payload streamObject, kind string, begin bool) {
    _, indexed, err := streamIndex(root, "output_index")
    if err != nil {
        return
    }
    id, err := streamID(payload, "id")
    if err != nil {
        return
    }
    alias, err := streamID(root, "item_id")
    if err != nil || id != "" && alias != "" && id != alias {
        return
    }
    if id == "" {
        id = alias
    }
    if !indexed && id == "" {
        return
    }
    binding := streamObject{}
    if indexed {
        binding["output_index"] = root["output_index"]
    }
    if id != "" {
        binding["item_id"], _ = json.Marshal(id)
    }
    // 不因无关的未选中载荷而更改已选中 item 的 type.
    // 其已有流保持原类别.
    if known := c.responseKnown(binding); known != nil && known.kind != "" && known.kind != kind {
        return
    }
    item, err := c.responseIdentity(binding, nil, kind)
    if err != nil {
        return
    }
    if begin {
        item.begun = true
    } else {
        item.retired = true
        item.parts = nil
    }
}

func (c *streamClassifier) responseKnown(root streamObject) *streamResponseItem {
    index, indexed, err := streamIndex(root, "output_index")
    if err == nil && indexed {
        if key, exists := c.itemIndex[index]; exists {
            return c.items[key]
        }
    }
    id, err := streamID(root, "item_id")
    if err == nil && id != "" {
        if key, exists := c.itemID[id]; exists {
            return c.items[key]
        }
    }
    return nil
}

func (c *streamClassifier) responseItemEvent(s *StreamRestorer, e *streamEntry, root streamObject, begin bool) error {
    payload, err := decodeObject(root["item"])
    if err != nil {
        if !s.snapshot.Processes(Messages | ToolCalls | ToolResponses) {
            return nil
        }
        return err
    }
    kind, err := jsonTag(payload, "type")
    if err != nil {
        return err
    }
    if !s.snapshot.Processes(streamResponseScope(kind)) {
        if !s.terminal && s.snapshot.Processes(Messages|ToolCalls|ToolResponses) {
            c.rememberResponseCategory(root, payload, kind, begin)
        }
        return nil
    }
    // 最终快照相互独立, 可以在全局终止后到达.
    if s.terminal {
        if begin {
            return ErrStreamIdentity
        }
        return s.snapshotField(e, streamPath{"item"}, root["item"], "responses.item")
    }
    item, err := c.responseIdentity(root, payload, kind)
    if err != nil {
        return err
    }
    if begin {
        if item.begun || item.retired {
            return ErrStreamIdentity
        }
        item.begun = true
        // 初始 arguments/input 不追加到 EJ/S delta 状态.
        return s.snapshotField(e, streamPath{"item"}, root["item"], "responses.item")
    }
    predicate := func(key streamKey) bool { return key.group == "responses" && key.item == item.key }
    if err := c.finish(s, predicate, false); err != nil {
        return err
    }
    item.retired = true
    item.parts = nil
    c.forgetClosed(predicate)
    return s.snapshotField(e, streamPath{"item"}, root["item"], "responses.item")
}

func (c *streamClassifier) responsePartEvent(s *StreamRestorer, e *streamEntry, root streamObject, begin bool) error {
    if !s.snapshot.Processes(Messages) {
        return nil
    }
    if known := c.responseKnown(root); known != nil && !s.snapshot.Processes(streamResponseScope(known.kind)) {
        return nil
    }
    payload, err := decodeObject(root["part"])
    if err != nil {
        return err
    }
    kind, err := jsonTag(payload, "type")
    if err != nil {
        return err
    }
    if streamResponseScope(kind) == 0 {
        return nil
    }
    if s.terminal {
        if begin {
            return ErrStreamIdentity
        }
        return s.snapshotField(e, streamPath{"part"}, root["part"], "responses.part")
    }
    item, err := c.responseIdentity(root, nil, "")
    if err != nil {
        return err
    }
    if item.kind != "" && streamResponseScope(item.kind) != Messages {
        return ErrStreamIdentity
    }
    if item.retired && !begin {
        return s.snapshotField(e, streamPath{"part"}, root["part"], "responses.part")
    }
    index, err := streamRequiredIndex(root, "content_index")
    if err != nil {
        return err
    }
    part, err := c.responsePart(item, index, kind, begin, false)
    if err != nil {
        return err
    }
    if !begin {
        predicate := func(key streamKey) bool {
            return key.group == "responses" && key.item == item.key && key.content == index
        }
        if err := c.finish(s, predicate, false); err != nil {
            return err
        }
        part.retired = true
        c.forgetClosed(predicate)
    }
    return s.snapshotField(e, streamPath{"part"}, root["part"], "responses.part")
}

func (c *streamClassifier) responseTextEvent(s *StreamRestorer, e *streamEntry, root streamObject, field, payloadKind string, embedded, delta bool) error {
    scope := Messages
    itemKind := ""
    if field == "arguments" || field == "input" {
        scope = ToolCalls
        itemKind = payloadKind
    }
    if !s.snapshot.Processes(scope) {
        return nil
    }
    if known := c.responseKnown(root); known != nil && !s.snapshot.Processes(streamResponseScope(known.kind)) {
        return nil
    }
    if s.terminal {
        if delta {
            return ErrStreamIdentity
        }
        return s.completeText(e, streamPath{field}, root[field], embedded)
    }
    item, err := c.responseIdentity(root, nil, itemKind)
    if err != nil {
        return err
    }
    if scope == Messages && item.kind != "" && streamResponseScope(item.kind) != Messages {
        return ErrStreamIdentity
    }
    if item.retired {
        if delta {
            return ErrStreamIdentity
        }
        return s.completeText(e, streamPath{field}, root[field], embedded)
    }
    content := -1
    if scope == Messages {
        content, err = streamRequiredIndex(root, "content_index")
        if err != nil {
            return err
        }
        if _, err := c.responsePart(item, content, payloadKind, false, delta); err != nil {
            return err
        }
    }
    keyField := field
    if field == "text" {
        keyField = "content"
    }
    key := streamResponseKey(item.key, content, keyField)
    if delta {
        return c.push(s, e, streamPath{"delta"}, key, root["delta"], embedded)
    }
    if err := c.finish(s, func(other streamKey) bool { return other == key }, false); err != nil {
        return err
    }
    c.closeKey(key) // 即使没有先前 delta, done 也禁止重开.
    return s.completeText(e, streamPath{field}, root[field], embedded)
}

func streamAnthropicScope(kind string) Scope {
    switch kind {
    case "text":
        return Messages
    case "tool_use", "server_tool_use", "mcp_tool_use":
        return ToolCalls
    case "tool_result", "mcp_tool_result", "code_execution_tool_result", "bash_code_execution_tool_result", "text_editor_code_execution_tool_result", "web_search_tool_result":
        return ToolResponses
    case "thinking", "redacted_thinking", "image", "document", "audio", "video":
        return 0
    default:
        if strings.HasSuffix(kind, "_tool_use") {
            return ToolCalls
        }
        if strings.HasSuffix(kind, "_tool_result") {
            return ToolResponses
        }
        return Messages | ToolCalls | ToolResponses
    }
}

func streamAnthropicKey(index int, field string) streamKey {
    return streamKey{group: "anthropic", choice: -1, item: index, content: -1, tool: -1, field: field}
}

func (c *streamClassifier) classifyAnthropic(s *StreamRestorer, e *streamEntry, root streamObject, kind string) error {
    switch kind {
    case "message_start":
        if !s.snapshot.Processes(Messages | ToolCalls | ToolResponses) {
            return nil
        }
        message, err := decodeObject(root["message"])
        if err != nil {
            return err
        }
        id, err := streamID(message, "id")
        if err != nil {
            return err
        }
        if s.terminal || id == "" || c.messageID != "" {
            return ErrStreamIdentity
        }
        c.messageID = id
        // message_start 的 content 是完整快照. 数组位置
        // 确立块类别; 后续 block_start 可再次确认.
        if streamPresent(message, "content") && rawKind(message["content"]) == '[' {
            blocks, err := streamArray(message["content"])
            if err != nil {
                return err
            }
            for index, rawBlock := range blocks {
                if err := c.anthropicBegin(s, index, rawBlock, false); err != nil {
                    return err
                }
            }
        }
        return s.snapshotField(e, streamPath{"message"}, root["message"], "anthropic.root")
    case "content_block_start":
        if !s.snapshot.Processes(Messages | ToolCalls | ToolResponses) {
            return nil
        }
        block, err := decodeObject(root["content_block"])
        if err != nil {
            return err
        }
        blockKind, err := jsonTag(block, "type")
        if err != nil {
            return err
        }
        selected := s.snapshot.Processes(streamAnthropicScope(blockKind))
        index, indexed, err := streamIndex(root, "index")
        if err != nil || !indexed {
            if selected {
                return ErrStreamIdentity
            }
            return nil
        }
        if s.terminal && !selected {
            return nil
        }
        if err := c.anthropicBegin(s, index, root["content_block"], true); err != nil {
            return err
        }
        return s.snapshotField(e, streamPath{"content_block"}, root["content_block"], "anthropic.block")
    case "content_block_delta":
        return c.anthropicDelta(s, e, root)
    case "content_block_stop":
        if !s.snapshot.Processes(Messages|ToolCalls|ToolResponses) || s.terminal {
            return nil
        }
        index, indexed, err := streamIndex(root, "index")
        if err != nil || !indexed {
            // stop 没有选中的活动块时, 无范围可结束.
            // 不得凭空创建块 0 或影响其他范围.
            for _, block := range c.blocks {
                if s.snapshot.Processes(block.scope) {
                    return ErrStreamIdentity
                }
            }
            return nil
        }
        block := c.blocks[index]
        if block == nil {
            // 固定版本的生成器可能为空响应发送无 start 的 stop,
            // 也可能重复发送 stop. 仅记录退役状态,
            // 不凭空创建解析器或关闭其他块.
            if c.blockGone == nil {
                c.blockGone = make(map[int]Scope)
            }
            if _, retired := c.blockGone[index]; !retired {
                c.blockGone[index] = Messages | ToolCalls | ToolResponses
            }
            return nil
        }
        predicate := func(key streamKey) bool { return key.group == "anthropic" && key.item == index }
        if err := c.finish(s, predicate, false); err != nil {
            return err
        }
        if c.blockGone == nil {
            c.blockGone = make(map[int]Scope)
        }
        c.blockGone[index] = block.scope
        delete(c.blocks, index)
        c.forgetClosed(predicate)
        return nil
    case "message_stop":
        return c.terminate(s, false)
    case "message_delta", "ping":
        return nil
    default:
        return nil
    }
}

func (c *streamClassifier) anthropicBegin(s *StreamRestorer, index int, raw json.RawMessage, explicit bool) error {
    obj, err := decodeObject(raw)
    if err != nil {
        return err
    }
    kind, err := jsonTag(obj, "type")
    if err != nil {
        return err
    }
    scope := streamAnthropicScope(kind)
    selected := s.snapshot.Processes(scope)
    id := ""
    if selected {
        id, err = streamID(obj, "id")
        if err != nil {
            return err
        }
        if s.terminal || c.messageID == "" {
            return ErrStreamIdentity
        }
        if _, retired := c.blockGone[index]; retired {
            return ErrStreamIdentity
        }
    }
    if previous := c.blocks[index]; previous != nil {
        if selected && (explicit && previous.begun || previous.kind != kind || previous.id != "" && id != "" && previous.id != id) {
            return ErrStreamIdentity
        }
        // 非空 message_start content 可被 block_start 重复发送.
        // 不得重置文本/JSON 词法状态或拼接快照.
        if previous.id == "" {
            previous.id = id
        }
        previous.begun = previous.begun || explicit
        return nil
    }
    if c.blocks == nil {
        c.blocks = make(map[int]*streamAnthropicBlock)
    }
    c.blocks[index] = &streamAnthropicBlock{kind: kind, id: id, scope: scope, begun: explicit}
    return nil
}

func (c *streamClassifier) anthropicDelta(s *StreamRestorer, e *streamEntry, root streamObject) error {
    if !s.snapshot.Processes(Messages | ToolCalls | ToolResponses) {
        return nil
    }
    delta, err := decodeObject(root["delta"])
    if err != nil {
        return err
    }
    kind, err := jsonTag(delta, "type")
    if err != nil {
        return err
    }
    // 此类事件始终排除, 即使块身份无效或缺失.
    if kind == "thinking_delta" || kind == "signature_delta" || kind == "citations_delta" {
        return nil
    }
    scope, field, embedded := Messages, "text", false
    switch kind {
    case "text_delta":
    case "input_json_delta":
        scope, field, embedded = ToolCalls, "partial_json", true
    default:
        // 未知 delta 判别字段不意味着整个载荷都是文本.
        // 仅对选中块中的已知文本/工具载体报错.
        index, indexed, indexErr := streamIndex(root, "index")
        block := c.blocks[index]
        if indexErr == nil && indexed && block != nil && !s.snapshot.Processes(block.scope) {
            return nil
        }
        if streamPresent(delta, "partial_json") && s.snapshot.Processes(ToolCalls) || streamPresent(delta, "text") && s.snapshot.Processes(Messages) {
            if s.terminal {
                return ErrStreamIdentity
            }
            return ErrUnsupportedContent
        }
        return nil
    }
    if !s.snapshot.Processes(scope) {
        return nil
    }
    if s.terminal {
        return ErrStreamIdentity
    }
    index, err := streamRequiredIndex(root, "index")
    if err != nil {
        return err
    }
    block := c.blocks[index]
    if block == nil {
        if oldScope, retired := c.blockGone[index]; retired && !s.snapshot.Processes(oldScope) {
            return nil
        }
        return ErrStreamIdentity
    }
    if !s.snapshot.Processes(block.scope) {
        return nil // 不重新解释 thinking/媒体或未启用的工具内容.
    }
    if c.messageID == "" || scope != block.scope {
        return ErrStreamIdentity
    }
    if kind == "text_delta" && block.kind != "text" || kind == "input_json_delta" && block.kind != "tool_use" && block.kind != "server_tool_use" && block.kind != "mcp_tool_use" {
        return ErrUnsupportedContent
    }
    keyField := "text"
    if embedded {
        keyField = "input"
    }
    return c.push(s, e, streamPath{"delta", field}, streamAnthropicKey(index, keyField), delta[field], embedded)
}
