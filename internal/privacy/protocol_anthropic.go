package privacy

import (
    "encoding/json"
    "strings"
)

func (transform *jsonTransform) anthropicRoot(raw json.RawMessage) (json.RawMessage, bool, error) {
    return rewriteObject(raw, func(obj jsonObject) (bool, error) {
        changed := false
        if err := rewriteField(obj, "system", nullable(transform.anthropicSystem), &changed); err != nil {
            return false, err
        }
        if err := rewriteField(obj, "messages", transform.selected(Messages|ToolCalls|ToolResponses, arrayOf(transform.anthropicMessage)), &changed); err != nil {
            return false, err
        }
        if err := rewriteField(obj, "content", transform.anthropicContent, &changed); err != nil {
            return false, err
        }
        tool := func(raw json.RawMessage) (json.RawMessage, bool, error) {
            return transform.descriptionSchema(raw, "input_schema")
        }
        if err := rewriteField(obj, "tools", transform.selected(ToolDefinitions, arrayOf(tool)), &changed); err != nil {
            return false, err
        }
        if err := rewriteField(obj, "error", nullable(transform.errorEvent), &changed); err != nil {
            return false, err
        }
        return changed, nil
    })
}

func (transform *jsonTransform) anthropicMessage(raw json.RawMessage) (json.RawMessage, bool, error) {
    if !transform.snapshot.Processes(Messages | ToolCalls | ToolResponses) {
        return raw, false, nil
    }
    return rewriteObject(raw, func(obj jsonObject) (bool, error) {
        changed := false
        err := rewriteField(obj, "content", transform.anthropicContent, &changed)
        return changed, err
    })
}

func (transform *jsonTransform) anthropicSystem(raw json.RawMessage) (json.RawMessage, bool, error) {
    if !transform.snapshot.Processes(Messages) {
        return raw, false, nil
    }
    if rawKind(raw) == '"' {
        return transform.text(Messages)(raw)
    }
    part := func(raw json.RawMessage) (json.RawMessage, bool, error) {
        return rewriteObject(raw, func(obj jsonObject) (bool, error) {
            kind, err := jsonTag(obj, "type")
            if err != nil {
                return false, err
            }
            if kind != "text" {
                return false, ErrUnsupportedContent
            }
            if _, exists := obj["text"]; !exists {
                return false, ErrInvalidJSON
            }
            changed := false
            err = rewriteField(obj, "text", transform.text(Messages), &changed)
            return changed, err
        })
    }
    return rewriteArray(raw, part)
}

func (transform *jsonTransform) anthropicContent(raw json.RawMessage) (json.RawMessage, bool, error) {
    if !transform.snapshot.Processes(Messages | ToolCalls | ToolResponses) {
        return raw, false, nil
    }
    if rawKind(raw) == '"' {
        return transform.text(Messages)(raw)
    }
    return rewriteArray(raw, transform.anthropicBlock)
}

func (transform *jsonTransform) anthropicBlock(raw json.RawMessage) (json.RawMessage, bool, error) {
    if !transform.snapshot.Processes(Messages | ToolCalls | ToolResponses) {
        return raw, false, nil
    }
    return rewriteObject(raw, func(obj jsonObject) (bool, error) {
        kind, err := jsonTag(obj, "type")
        if err != nil {
            return false, err
        }
        changed := false
        switch kind {
        case "text":
            if transform.snapshot.Processes(Messages) {
                if _, exists := obj["text"]; !exists {
                    return false, ErrInvalidJSON
                }
            }
            err = rewriteField(obj, "text", transform.text(Messages), &changed)
        case "tool_use", "server_tool_use", "mcp_tool_use":
            err = rewriteField(obj, "input", transform.business(ToolCalls), &changed)
        case "tool_result", "mcp_tool_result", "code_execution_tool_result", "bash_code_execution_tool_result", "text_editor_code_execution_tool_result":
            result := func(raw json.RawMessage) (json.RawMessage, bool, error) {
                // 普通客户端工具结果对象明确属于业务 JSON;
                // 其 type/id/name 键也不是协议判别字段.
                if kind == "tool_result" && rawKind(raw) == '{' {
                    return transform.business(ToolResponses)(raw)
                }
                return transform.anthropicResult(raw, kind != "tool_result", false)
            }
            err = rewriteField(obj, "content", result, &changed)
        case "web_search_tool_result":
            result := func(raw json.RawMessage) (json.RawMessage, bool, error) {
                return transform.anthropicResult(raw, true, true)
            }
            err = rewriteField(obj, "content", result, &changed)
        case "thinking", "redacted_thinking", "image", "document", "audio", "video":
            // 排除整个带签名的 reasoning/媒体块, 包括夹带的文本.
        default:
            scope := Messages | ToolCalls | ToolResponses
            if strings.HasSuffix(kind, "_tool_use") {
                scope = ToolCalls
            }
            if strings.HasSuffix(kind, "_tool_result") {
                scope = ToolResponses
            }
            return false, transform.unsupported(scope)
        }
        return changed, err
    })
}

// 固定版本仅保留任意原生结果字节, 不保证其语义.
// 不将原生封装误判为工具业务 JSON. 普通 tool_result 对象
// 属于业务值. 原生结果对象 (包括无标签对象)
// 必须使用明确的封装白名单; 缺少 type 不代表
// 该对象属于业务 JSON.
func (transform *jsonTransform) anthropicResult(raw json.RawMessage, native, search bool) (json.RawMessage, bool, error) {
    if !transform.snapshot.Processes(ToolResponses) {
        return raw, false, nil
    }
    switch rawKind(raw) {
    case '"':
        return transform.text(ToolResponses)(raw)
    case '[':
        entry := func(raw json.RawMessage) (json.RawMessage, bool, error) {
            return transform.anthropicResult(raw, native, search)
        }
        return rewriteArray(raw, entry)
    case '{':
        obj, err := decodeObject(raw)
        if err != nil {
            return raw, false, err
        }
        // 仅在原生结果/文本片段上下文中将 type 视为协议标签.
        kind, err := jsonTag(obj, "type")
        if err != nil {
            if !native {
                return transform.business(ToolResponses)(raw)
            }
            return raw, false, err
        }
        switch kind {
        case "text":
            if _, exists := obj["text"]; !exists {
                return raw, false, ErrInvalidJSON
            }
            return rewriteObject(raw, func(obj jsonObject) (bool, error) {
                changed := false
                err := rewriteField(obj, "text", transform.text(ToolResponses), &changed)
                return changed, err
            })
        case "image", "document", "audio", "video", "thinking", "redacted_thinking":
            return raw, false, nil
        case "web_search_result":
            if !search {
                return raw, false, ErrUnsupportedContent
            }
            return rewriteObject(raw, func(obj jsonObject) (bool, error) {
                changed := false
                err := rewriteField(obj, "title", transform.text(ToolResponses), &changed)
                return changed, err
            })
        case "web_search_tool_result_error":
            if !search {
                return raw, false, ErrUnsupportedContent
            }
            // 固定版本的搜索错误封装仅含控制字段.
            return raw, false, nil
        case "":
            if native || search {
                return raw, false, ErrUnsupportedContent
            }
            return transform.business(ToolResponses)(raw)
        default:
            if native {
                return raw, false, ErrUnsupportedContent
            }
            return transform.business(ToolResponses)(raw)
        }
    default:
        if native || search {
            return raw, false, ErrInvalidJSON
        }
        // 业务结果数组中的标量保持不变.
        return raw, false, nil
    }
}
