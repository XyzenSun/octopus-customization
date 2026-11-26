package privacy

import (
    "encoding/json"
    "strings"
)

func (transform *jsonTransform) responsesRoot(raw json.RawMessage) (json.RawMessage, bool, error) {
    return rewriteObject(raw, func(obj jsonObject) (bool, error) {
        changed := false
        if err := rewriteField(obj, "instructions", nullable(transform.text(Messages)), &changed); err != nil {
            return false, err
        }
        input := func(raw json.RawMessage) (json.RawMessage, bool, error) {
            if rawKind(raw) == '"' {
                return transform.text(Messages)(raw)
            }
            return rewriteArray(raw, transform.responsesItem)
        }
        if err := rewriteField(obj, "input", transform.selected(Messages|ToolCalls|ToolResponses, input), &changed); err != nil {
            return false, err
        }
        if err := rewriteField(obj, "output", transform.selected(Messages|ToolCalls|ToolResponses, arrayOf(transform.responsesItem)), &changed); err != nil {
            return false, err
        }
        if err := rewriteField(obj, "prompt", transform.responsesPrompt, &changed); err != nil {
            return false, err
        }
        if err := rewriteField(obj, "tools", transform.selected(ToolDefinitions, arrayOf(transform.responsesTool)), &changed); err != nil {
            return false, err
        }
        textFormat := func(raw json.RawMessage) (json.RawMessage, bool, error) {
            if !transform.snapshot.Processes(ToolDefinitions) {
                return raw, false, nil
            }
            return rewriteObject(raw, func(obj jsonObject) (bool, error) {
                change := false
                format := func(raw json.RawMessage) (json.RawMessage, bool, error) {
                    return transform.descriptionSchema(raw, "schema")
                }
                err := rewriteField(obj, "format", format, &change)
                return change, err
            })
        }
        if err := rewriteField(obj, "text", textFormat, &changed); err != nil {
            return false, err
        }
        if err := rewriteField(obj, "error", nullable(transform.errorEvent), &changed); err != nil {
            return false, err
        }
        return changed, nil
    })
}

func (transform *jsonTransform) responsesPrompt(raw json.RawMessage) (json.RawMessage, bool, error) {
    if !transform.snapshot.Processes(Messages) {
        return raw, false, nil
    }
    return rewriteObject(raw, func(obj jsonObject) (bool, error) {
        changed := false
        variables := func(raw json.RawMessage) (json.RawMessage, bool, error) {
            return rewriteObject(raw, func(vars jsonObject) (bool, error) {
                change := false
                for name := range vars {
                    if rawKind(vars[name]) != '"' {
                        return false, ErrUnsupportedContent
                    }
                    if err := rewriteField(vars, name, transform.text(Messages), &change); err != nil {
                        return false, err
                    }
                }
                return change, nil
            })
        }
        err := rewriteField(obj, "variables", variables, &changed)
        return changed, err
    })
}

func (transform *jsonTransform) responsesItem(raw json.RawMessage) (json.RawMessage, bool, error) {
    if !transform.snapshot.Processes(Messages | ToolCalls | ToolResponses) {
        return raw, false, nil
    }
    obj, err := decodeObject(raw)
    if err != nil {
        return raw, false, err
    }
    kind, err := jsonTag(obj, "type")
    if err != nil {
        return raw, false, err
    }
    if kind == "input_text" || kind == "output_text" || kind == "text" || kind == "refusal" {
        return transform.responsesPart(raw)
    }
    return rewriteObject(raw, func(obj jsonObject) (bool, error) {
        kind, err := jsonTag(obj, "type")
        if err != nil {
            return false, err
        }
        changed := false
        switch kind {
        case "message":
            err = rewriteField(obj, "content", transform.responsesTextContent(Messages), &changed)
        case "":
            role, roleErr := jsonTag(obj, "role")
            if roleErr != nil {
                return false, roleErr
            }
            if role == "" {
                return false, transform.unsupported(Messages | ToolCalls | ToolResponses)
            }
            err = rewriteField(obj, "content", transform.responsesTextContent(Messages), &changed)
        case "function_call":
            arguments := func(raw json.RawMessage) (json.RawMessage, bool, error) {
                if rawKind(raw) == '"' {
                    return transform.embedded(ToolCalls)(raw)
                }
                return transform.business(ToolCalls)(raw)
            }
            err = rewriteField(obj, "arguments", arguments, &changed)
        case "custom_tool_call":
            err = rewriteField(obj, "input", transform.text(ToolCalls), &changed)
        case "function_call_output", "custom_tool_call_output":
            if transform.snapshot.Processes(ToolResponses) && rawKind(obj["output"]) == '{' {
                return false, ErrUnsupportedContent
            }
            err = rewriteField(obj, "output", transform.responsesTextContent(ToolResponses), &changed)
        case "tool_search_call":
            err = rewriteField(obj, "arguments", transform.business(ToolCalls), &changed)
        case "web_search_call":
            err = rewriteField(obj, "action", transform.responsesSearchAction, &changed)
        case "mcp_list_tools":
            return false, transform.unsupported(ToolResponses)
        case "mcp_approval_request":
            return false, transform.unsupported(ToolCalls)
        case "mcp_approval_response":
            return false, transform.unsupported(ToolResponses)
        case "reasoning", "summary_text", "compaction", "compaction_summary", "input_image", "input_audio", "input_file", "output_audio", "image_generation_call", "item_reference":
            // 即使对话启用工具, reasoning 和媒体也不处理.
        default:
            scope := Messages | ToolCalls | ToolResponses
            if strings.HasSuffix(kind, "_call") {
                scope = ToolCalls
            }
            if strings.HasSuffix(kind, "_output") || strings.HasSuffix(kind, "_result") {
                scope = ToolResponses
            }
            return false, transform.unsupported(scope)
        }
        return changed, err
    })
}

func (transform *jsonTransform) responsesPart(raw json.RawMessage) (json.RawMessage, bool, error) {
    return transform.responsesPartFor(Messages)(raw)
}

func (transform *jsonTransform) responsesPartFor(scope Scope) jsonRewrite {
    return func(raw json.RawMessage) (json.RawMessage, bool, error) {
        if !transform.snapshot.Processes(scope) {
            return raw, false, nil
        }
        return rewriteObject(raw, func(obj jsonObject) (bool, error) {
            kind, err := jsonTag(obj, "type")
            if err != nil {
                return false, err
            }
            changed := false
            switch kind {
            case "input_text", "output_text", "text":
                if _, exists := obj["text"]; !exists {
                    return false, ErrInvalidJSON
                }
                err = rewriteField(obj, "text", transform.text(scope), &changed)
            case "refusal":
                if _, exists := obj["refusal"]; !exists {
                    return false, ErrInvalidJSON
                }
                err = rewriteField(obj, "refusal", transform.text(scope), &changed)
            case "input_image", "input_audio", "input_file", "output_audio", "image", "audio", "file", "summary_text", "reasoning", "compaction", "compaction_summary":
            default:
                return false, ErrUnsupportedContent
            }
            return changed, err
        })
    }
}

func (transform *jsonTransform) responsesTextContent(scope Scope) jsonRewrite {
    return func(raw json.RawMessage) (json.RawMessage, bool, error) {
        if !transform.snapshot.Processes(scope) {
            return raw, false, nil
        }
        if rawKind(raw) == '"' {
            return transform.text(scope)(raw)
        }
        return rewriteArray(raw, transform.responsesPartFor(scope))
    }
}

func (transform *jsonTransform) responsesSearchAction(raw json.RawMessage) (json.RawMessage, bool, error) {
    if !transform.snapshot.Processes(ToolCalls | ToolResponses) {
        return raw, false, nil
    }
    return rewriteObject(raw, func(obj jsonObject) (bool, error) {
        changed := false
        if err := rewriteField(obj, "query", transform.text(ToolCalls), &changed); err != nil {
            return false, err
        }
        if err := rewriteField(obj, "queries", arrayOf(transform.text(ToolCalls)), &changed); err != nil {
            return false, err
        }
        source := func(raw json.RawMessage) (json.RawMessage, bool, error) {
            if !transform.snapshot.Processes(ToolResponses) {
                return raw, false, nil
            }
            return rewriteObject(raw, func(obj jsonObject) (bool, error) {
                change := false
                err := rewriteField(obj, "title", transform.text(ToolResponses), &change)
                return change, err
            })
        }
        if err := rewriteField(obj, "sources", arrayOf(source), &changed); err != nil {
            return false, err
        }
        return changed, nil
    })
}

func (transform *jsonTransform) responsesTool(raw json.RawMessage) (json.RawMessage, bool, error) {
    if !transform.snapshot.Processes(ToolDefinitions) {
        return raw, false, nil
    }
    return rewriteObject(raw, func(obj jsonObject) (bool, error) {
        kind, err := jsonTag(obj, "type")
        if err != nil {
            return false, err
        }
        changed := false
        if err := rewriteField(obj, "description", transform.text(ToolDefinitions), &changed); err != nil {
            return false, err
        }
        if err := rewriteField(obj, "parameters", transform.schema, &changed); err != nil {
            return false, err
        }
        if kind == "namespace" {
            if err := rewriteField(obj, "tools", arrayOf(transform.responsesTool), &changed); err != nil {
                return false, err
            }
        }
        // 自定义 grammar 和原生工具选项不属于 description/schema.
        // 不通用遍历 format/grammar, 约束或 metadata.
        return changed, nil
    })
}
