package privacy

import (
    "bytes"
    "encoding/json"
    "sort"
    "unicode/utf8"

    "github.com/looplj/axonhub/llm"
)

// DecodeJSONString 严格解码一个完整 JSON 字符串, 不修复非法 Unicode.
// 允许 JSON 空白, 但拒绝 null、其他类型、无效 UTF-8 及孤立 surrogate.
func DecodeJSONString(data []byte) (string, error) {
    data = bytes.Trim(data, " \t\r\n")
    if len(data) < 2 || data[0] != '"' || data[len(data)-1] != '"' || !utf8.Valid(data) {
        return "", ErrInvalidJSON
    }

    // encoding/json 会将孤立 surrogate 替换为 U+FFFD, 因此先检查转义.
    // 其他 JSON 词法约束仍交由标准库验证, 不做第二套完整 JSON parser.
    for index := 1; index < len(data)-1; index++ {
        if data[index] != '\\' {
            continue
        }
        index++
        if index >= len(data)-1 {
            return "", ErrInvalidJSON
        }
        if data[index] != 'u' {
            continue
        }
        code, ok := jsonHexCode(data, index+1)
        if !ok {
            return "", ErrInvalidJSON
        }
        index += 4
        switch {
        case code >= 0xD800 && code <= 0xDBFF:
            if index+6 >= len(data)-1 || data[index+1] != '\\' || data[index+2] != 'u' {
                return "", ErrInvalidJSON
            }
            low, ok := jsonHexCode(data, index+3)
            if !ok || low < 0xDC00 || low > 0xDFFF {
                return "", ErrInvalidJSON
            }
            index += 6
        case code >= 0xDC00 && code <= 0xDFFF:
            return "", ErrInvalidJSON
        }
    }

    var value string
    if err := json.Unmarshal(data, &value); err != nil {
        return "", ErrInvalidJSON
    }
    return value, nil
}

// EncodeJSONString 不允许标准库将无效 UTF-8 静默替换后输出.
func EncodeJSONString(value string) ([]byte, error) {
    if !utf8.ValidString(value) {
        return nil, ErrInvalidJSON
    }
    data, err := json.Marshal(value)
    if err != nil {
        return nil, ErrInvalidJSON
    }
    return data, nil
}

// jsonObject 使用类型别名, 便于 SSE 直接传入 RawMessage 字段映射.
type jsonObject = map[string]json.RawMessage

type jsonRewrite func(json.RawMessage) (json.RawMessage, bool, error)

type jsonTransform struct {
    snapshot  *Snapshot
    restoring bool
}

const (
    selectorChatMessage      = "chat.message"
    selectorResponsesItem    = "responses.item"
    selectorResponsesPart    = "responses.part"
    selectorResponsesRoot    = "responses.root"
    selectorAnthropicMessage = "anthropic.message"
    selectorAnthropicBlock   = "anthropic.block"
    selectorAnthropicRoot    = "anthropic.root"
    selectorErrorEvent       = "error.event"
)

// RewriteRequest 在应用请求选项后处理实际出站协议格式.
// 调用方决定失败策略; 出错时不返回部分改写的请求体.
func (snapshot *Snapshot) RewriteRequest(format llm.APIFormat, raw []byte) ([]byte, bool, error) {
    return snapshot.transformJSON(format, raw, false)
}

func (snapshot *Snapshot) RestoreResponse(format llm.APIFormat, raw []byte) ([]byte, bool, error) {
    return snapshot.transformJSON(format, raw, true)
}

// RestoreError 仅用于未排除的 API; 排除策略由 relay 负责.
func (snapshot *Snapshot) RestoreError(raw []byte) ([]byte, bool, error) {
    out, changed, err := rewriteSnapshot(snapshot, raw, selectorErrorEvent)
    return out, changed, err
}

func (snapshot *Snapshot) transformJSON(format llm.APIFormat, raw []byte, restoring bool) ([]byte, bool, error) {
    // embedding/image API 在解析前直接返回, 包括错误和 multipart.
    switch format {
    case llm.APIFormatOpenAIEmbedding, llm.APIFormatGeminiEmbedding, llm.APIFormatJinaEmbedding,
        llm.APIFormatOpenAIImageGeneration, llm.APIFormatOpenAIImageEdit, llm.APIFormatOpenAIImageVariation:
        return raw, false, nil
    }
    transform := jsonTransform{snapshot: snapshot, restoring: restoring}
    var rewrite jsonRewrite
    switch format {
    case llm.APIFormatOpenAIChatCompletion: // Doubao 的实际 APIFormat 为 Chat.
        if restoring {
            rewrite = transform.chatResponse
        } else {
            rewrite = transform.chatRequest
        }
    case llm.APIFormatOpenAIResponse:
        rewrite = transform.responsesRoot
    case llm.APIFormatAnthropicMessage:
        rewrite = transform.anthropicRoot
    case llm.APIFormatGeminiContents:
        if restoring {
            rewrite = transform.geminiResponse
        } else {
            rewrite = transform.geminiRequest
        }
    default:
        if !snapshot.Active() {
            return raw, false, nil
        }
        return raw, false, ErrUnsupportedContent
    }
    return runJSON(snapshot, raw, rewrite)
}

// rewriteSnapshot 还原独立完整载荷, 不处理 delta.
// start 和 done 载荷必须使用同一协议分类器.
func rewriteSnapshot(snapshot *Snapshot, raw json.RawMessage, selector string) (json.RawMessage, bool, error) {
    transform := jsonTransform{snapshot: snapshot, restoring: true}
    var rewrite jsonRewrite
    switch selector {
    case selectorChatMessage:
        rewrite = transform.chatMessage
    case selectorResponsesItem:
        rewrite = transform.responsesItem
    case selectorResponsesPart:
        rewrite = transform.responsesPart
    case selectorResponsesRoot:
        rewrite = transform.responsesRoot
    case selectorAnthropicMessage:
        rewrite = transform.anthropicMessage
    case selectorAnthropicBlock:
        rewrite = transform.anthropicBlock
    case selectorAnthropicRoot:
        rewrite = transform.anthropicRoot
    case selectorErrorEvent:
        rewrite = transform.errorEvent
    default:
        if !snapshot.Active() {
            return raw, false, nil
        }
        return raw, false, ErrUnsupportedContent
    }
    return runJSON(snapshot, raw, rewrite)
}

func runJSON(snapshot *Snapshot, raw json.RawMessage, rewrite jsonRewrite) (json.RawMessage, bool, error) {
    if !snapshot.Active() {
        return raw, false, nil
    }
    if err := validatePrivacyJSON(raw); err != nil {
        return raw, false, err
    }
    out, changed, err := rewrite(raw)
    if err != nil {
        return raw, false, err
    }
    if !changed || bytes.Equal(out, raw) {
        return raw, false, nil
    }
    return out, true, nil
}

// 校验语法, UTF-8/surrogate 和深度, 不解码数字或键.
// 深度按容器计数 (深度 128 处的标量仍合法).
func validatePrivacyJSON(raw []byte) error {
    depth := 0
    for i := 0; i < len(raw); i++ {
        switch raw[i] {
        case '{', '[':
            depth++
            if depth > MaxJSONDepth {
                return ErrResourceLimit
            }
        case '}', ']':
            depth--
        case '"':
            start := i
            i++
            for i < len(raw) && raw[i] != '"' {
                if raw[i] == '\\' {
                    i++
                }
                i++
            }
            if i >= len(raw) {
                return ErrInvalidJSON
            }
            if _, err := DecodeJSONString(raw[start : i+1]); err != nil {
                return err
            }
        }
    }
    if !json.Valid(raw) {
        return ErrInvalidJSON
    }
    return nil
}

func rawKind(raw json.RawMessage) byte {
    raw = bytes.TrimSpace(raw)
    if len(raw) == 0 {
        return 0
    }
    return raw[0]
}

func decodeObject(raw json.RawMessage) (jsonObject, error) {
    if rawKind(raw) != '{' {
        return nil, ErrInvalidJSON
    }
    var obj jsonObject
    if json.Unmarshal(raw, &obj) != nil {
        return nil, ErrInvalidJSON
    }
    return obj, nil
}

func jsonTag(obj jsonObject, key string) (string, error) {
    raw, exists := obj[key]
    if !exists {
        return "", nil
    }
    return DecodeJSONString(raw)
}

// 与 json.Marshal(RawMessage) 不同, 此处不压缩未修改的子节点.
// 仅重建已修改对象的分隔符和键顺序.
func marshalJSONObject(obj jsonObject) json.RawMessage {
    keys := make([]string, 0, len(obj))
    for key := range obj {
        keys = append(keys, key)
    }
    sort.Strings(keys)
    var out bytes.Buffer
    out.WriteByte('{')
    for i, key := range keys {
        if i > 0 {
            out.WriteByte(',')
        }
        encoded, _ := EncodeJSONString(key)
        out.Write(encoded)
        out.WriteByte(':')
        out.Write(obj[key])
    }
    out.WriteByte('}')
    return out.Bytes()
}

func rewriteObject(raw json.RawMessage, apply func(jsonObject) (bool, error)) (json.RawMessage, bool, error) {
    obj, err := decodeObject(raw)
    if err != nil {
        return raw, false, err
    }
    changed, err := apply(obj)
    if err != nil || !changed {
        return raw, false, err
    }
    return marshalJSONObject(obj), true, nil
}

func rewriteArray(raw json.RawMessage, rewrite jsonRewrite) (json.RawMessage, bool, error) {
    if rawKind(raw) != '[' {
        return raw, false, ErrInvalidJSON
    }
    var items []json.RawMessage
    if json.Unmarshal(raw, &items) != nil {
        return raw, false, ErrInvalidJSON
    }
    changed := false
    for i, item := range items {
        out, change, err := rewrite(item)
        if err != nil {
            return raw, false, err
        }
        if change {
            items[i] = out
            changed = true
        }
    }
    if !changed {
        return raw, false, nil
    }
    var out bytes.Buffer
    out.WriteByte('[')
    for i, item := range items {
        if i > 0 {
            out.WriteByte(',')
        }
        out.Write(item)
    }
    out.WriteByte(']')
    return out.Bytes(), true, nil
}

func rewriteField(obj jsonObject, key string, rewrite jsonRewrite, changed *bool) error {
    raw, exists := obj[key]
    if !exists {
        return nil
    }
    out, change, err := rewrite(raw)
    if err != nil {
        return err
    }
    if change {
        obj[key] = out
        *changed = true
    }
    return nil
}

func nullable(rewrite jsonRewrite) jsonRewrite {
    return func(raw json.RawMessage) (json.RawMessage, bool, error) {
        if rawKind(raw) == 'n' {
            return raw, false, nil
        }
        return rewrite(raw)
    }
}

func arrayOf(rewrite jsonRewrite) jsonRewrite {
    return func(raw json.RawMessage) (json.RawMessage, bool, error) { return rewriteArray(raw, rewrite) }
}

// selected 跳过未选中载荷字段, 不检查其结构.
func (transform *jsonTransform) selected(scope Scope, rewrite jsonRewrite) jsonRewrite {
    return func(raw json.RawMessage) (json.RawMessage, bool, error) {
        if !transform.snapshot.Processes(scope) {
            return raw, false, nil
        }
        return rewrite(raw)
    }
}

func (transform *jsonTransform) text(scope Scope) jsonRewrite {
    return func(raw json.RawMessage) (json.RawMessage, bool, error) {
        if !transform.snapshot.Processes(scope) {
            return raw, false, nil
        }
        text, err := DecodeJSONString(raw)
        if err != nil {
            return raw, false, err
        }
        var out string
        if transform.restoring {
            restorer := NewRestorer(transform.snapshot)
            head, _ := restorer.Push(text)
            tail, _ := restorer.Finish()
            out = head + tail
        } else {
            out, _, err = transform.snapshot.Replace(text)
            if err != nil {
                return raw, false, err
            }
        }
        if out == text {
            return raw, false, nil
        }
        encoded, err := EncodeJSONString(out)
        if err != nil {
            return raw, false, err
        }
        return encoded, true, nil
    }
}

// J: 仅值作为文本处理; id/name/description 均为普通业务键.
func (transform *jsonTransform) business(scope Scope) jsonRewrite {
    var walk jsonRewrite
    walk = func(raw json.RawMessage) (json.RawMessage, bool, error) {
        switch rawKind(raw) {
        case '"':
            return transform.text(scope)(raw)
        case '[':
            return rewriteArray(raw, walk)
        case '{':
            return rewriteObject(raw, func(obj jsonObject) (bool, error) {
                changed := false
                for key := range obj {
                    if err := rewriteField(obj, key, walk, &changed); err != nil {
                        return false, err
                    }
                }
                return changed, nil
            })
        default:
            return raw, false, nil
        }
    }
    return func(raw json.RawMessage) (json.RawMessage, bool, error) {
        if !transform.snapshot.Processes(scope) {
            return raw, false, nil
        }
        if kind := rawKind(raw); kind != '{' && kind != '[' {
            return raw, false, ErrInvalidJSON
        }
        return walk(raw)
    }
}

// EJ: 独立校验完整内层文档; 初始 "" 合法.
func (transform *jsonTransform) embedded(scope Scope) jsonRewrite {
    return func(raw json.RawMessage) (json.RawMessage, bool, error) {
        if !transform.snapshot.Processes(scope) {
            return raw, false, nil
        }
        text, err := DecodeJSONString(raw)
        if err != nil {
            return raw, false, err
        }
        if text == "" {
            return raw, false, nil
        }
        if err := validatePrivacyJSON([]byte(text)); err != nil {
            return raw, false, err
        }
        // 完整 JSON 文档也可以是标量.
        var walk jsonRewrite
        walk = func(value json.RawMessage) (json.RawMessage, bool, error) {
            switch rawKind(value) {
            case '"':
                return transform.text(scope)(value)
            case '[':
                return rewriteArray(value, walk)
            case '{':
                return rewriteObject(value, func(obj jsonObject) (bool, error) {
                    changed := false
                    for key := range obj {
                        if err := rewriteField(obj, key, walk, &changed); err != nil {
                            return false, err
                        }
                    }
                    return changed, nil
                })
            default:
                return value, false, nil
            }
        }
        out, change, err := walk([]byte(text))
        if err != nil || !change {
            return raw, false, err
        }
        encoded, err := EncodeJSONString(string(out))
        if err != nil {
            return raw, false, err
        }
        return encoded, true, nil
    }
}

// D: schema 映射保存具名 schema, 而非实例. 不遍历
// enum/default/const/examples, 也不按任意业务键推断 schema.
func (transform *jsonTransform) schema(raw json.RawMessage) (json.RawMessage, bool, error) {
    if !transform.snapshot.Processes(ToolDefinitions) {
        return raw, false, nil
    }
    if rawKind(raw) == 't' || rawKind(raw) == 'f' {
        return raw, false, nil
    }
    return rewriteObject(raw, func(obj jsonObject) (bool, error) {
        changed := false
        if err := rewriteField(obj, "description", transform.text(ToolDefinitions), &changed); err != nil {
            return false, err
        }
        for _, key := range []string{"properties", "$defs", "definitions", "patternProperties", "dependentSchemas"} {
            schemas := func(raw json.RawMessage) (json.RawMessage, bool, error) {
                return rewriteObject(raw, func(nodes jsonObject) (bool, error) {
                    change := false
                    for name := range nodes {
                        if err := rewriteField(nodes, name, transform.schema, &change); err != nil {
                            return false, err
                        }
                    }
                    return change, nil
                })
            }
            if err := rewriteField(obj, key, schemas, &changed); err != nil {
                return false, err
            }
        }
        for _, key := range []string{"items", "additionalProperties", "contains", "not", "if", "then", "else", "propertyNames", "additionalItems", "unevaluatedItems", "unevaluatedProperties", "contentSchema"} {
            node := transform.schema
            if key == "items" && rawKind(obj[key]) == '[' {
                node = arrayOf(transform.schema)
            }
            if err := rewriteField(obj, key, node, &changed); err != nil {
                return false, err
            }
        }
        for _, key := range []string{"allOf", "anyOf", "oneOf", "prefixItems"} {
            if err := rewriteField(obj, key, arrayOf(transform.schema), &changed); err != nil {
                return false, err
            }
        }
        return changed, nil
    })
}

func (transform *jsonTransform) descriptionSchema(raw json.RawMessage, schemaKey string) (json.RawMessage, bool, error) {
    if !transform.snapshot.Processes(ToolDefinitions) {
        return raw, false, nil
    }
    return rewriteObject(raw, func(obj jsonObject) (bool, error) {
        changed := false
        if err := rewriteField(obj, "description", transform.text(ToolDefinitions), &changed); err != nil {
            return false, err
        }
        if err := rewriteField(obj, schemaKey, transform.schema, &changed); err != nil {
            return false, err
        }
        return changed, nil
    })
}

func (transform *jsonTransform) unsupported(scope Scope) error {
    if transform.snapshot.Processes(scope) {
        return ErrUnsupportedContent
    }
    return nil
}

func (transform *jsonTransform) errorEvent(raw json.RawMessage) (json.RawMessage, bool, error) {
    if !transform.snapshot.Processes(Messages) {
        return raw, false, nil
    }
    return rewriteObject(raw, func(obj jsonObject) (bool, error) {
        changed := false
        if err := rewriteField(obj, "message", transform.text(Messages), &changed); err != nil {
            return false, err
        }
        nested := func(raw json.RawMessage) (json.RawMessage, bool, error) {
            return rewriteObject(raw, func(errorObj jsonObject) (bool, error) {
                change := false
                err := rewriteField(errorObj, "message", transform.text(Messages), &change)
                return change, err
            })
        }
        if err := rewriteField(obj, "error", nullable(nested), &changed); err != nil {
            return false, err
        }
        return changed, nil
    })
}

func jsonHexCode(data []byte, start int) (uint16, bool) {
    if start < 0 || start+4 > len(data) {
        return 0, false
    }
    var code uint16
    for _, digit := range data[start : start+4] {
        code <<= 4
        switch {
        case digit >= '0' && digit <= '9':
            code |= uint16(digit - '0')
        case digit >= 'a' && digit <= 'f':
            code |= uint16(digit - 'a' + 10)
        case digit >= 'A' && digit <= 'F':
            code |= uint16(digit - 'A' + 10)
        default:
            return 0, false
        }
    }
    return code, true
}
