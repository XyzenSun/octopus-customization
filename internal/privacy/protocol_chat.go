package privacy

import "encoding/json"

func (transform *jsonTransform) chatRequest(raw json.RawMessage) (json.RawMessage, bool, error) {
    return rewriteObject(raw, func(obj jsonObject) (bool, error) {
        changed := false
        if _, exists := obj["functions"]; exists {
            if err := transform.unsupported(ToolDefinitions); err != nil {
                return false, err
            }
        }
        if _, exists := obj["function_call"]; exists {
            if err := transform.unsupported(ToolCalls); err != nil {
                return false, err
            }
        }
        if err := rewriteField(obj, "messages", transform.selected(Messages|ToolCalls|ToolResponses, arrayOf(transform.chatMessage)), &changed); err != nil {
            return false, err
        }
        if err := rewriteField(obj, "tools", transform.selected(ToolDefinitions, arrayOf(transform.chatTool)), &changed); err != nil {
            return false, err
        }
        format := func(raw json.RawMessage) (json.RawMessage, bool, error) {
            if !transform.snapshot.Processes(ToolDefinitions) {
                return raw, false, nil
            }
            return rewriteObject(raw, func(obj jsonObject) (bool, error) {
                change := false
                schema := func(raw json.RawMessage) (json.RawMessage, bool, error) {
                    return transform.descriptionSchema(raw, "schema")
                }
                err := rewriteField(obj, "json_schema", schema, &change)
                return change, err
            })
        }
        if err := rewriteField(obj, "response_format", format, &changed); err != nil {
            return false, err
        }
        return changed, nil
    })
}

func (transform *jsonTransform) chatResponse(raw json.RawMessage) (json.RawMessage, bool, error) {
    return rewriteObject(raw, func(obj jsonObject) (bool, error) {
        changed := false
        choice := func(raw json.RawMessage) (json.RawMessage, bool, error) {
            return rewriteObject(raw, func(choice jsonObject) (bool, error) {
                change := false
                err := rewriteField(choice, "message", transform.chatMessage, &change)
                return change, err
            })
        }
        if err := rewriteField(obj, "choices", transform.selected(Messages|ToolCalls|ToolResponses, arrayOf(choice)), &changed); err != nil {
            return false, err
        }
        if err := rewriteField(obj, "error", nullable(transform.errorEvent), &changed); err != nil {
            return false, err
        }
        return changed, nil
    })
}

func (transform *jsonTransform) chatMessage(raw json.RawMessage) (json.RawMessage, bool, error) {
    if !transform.snapshot.Processes(Messages | ToolCalls | ToolResponses) {
        return raw, false, nil
    }
    return rewriteObject(raw, func(obj jsonObject) (bool, error) {
        role, err := jsonTag(obj, "role")
        if err != nil {
            return false, err
        }
        scope := Messages
        if role == "tool" {
            scope = ToolResponses
        }
        changed := false
        if _, exists := obj["function_call"]; exists {
            if err := transform.unsupported(ToolCalls); err != nil {
                return false, err
            }
        }
        if err := rewriteField(obj, "content", nullable(transform.chatContent(scope)), &changed); err != nil {
            return false, err
        }
        if err := rewriteField(obj, "refusal", nullable(transform.text(Messages)), &changed); err != nil {
            return false, err
        }
        if transform.snapshot.Processes(ToolCalls) {
            if err := rewriteField(obj, "tool_calls", arrayOf(transform.chatCall), &changed); err != nil {
                return false, err
            }
        }
        return changed, nil
    })
}

func (transform *jsonTransform) chatContent(scope Scope) jsonRewrite {
    return func(raw json.RawMessage) (json.RawMessage, bool, error) {
        if !transform.snapshot.Processes(scope) {
            return raw, false, nil
        }
        if rawKind(raw) == '"' {
            return transform.text(scope)(raw)
        }
        part := func(raw json.RawMessage) (json.RawMessage, bool, error) {
            return rewriteObject(raw, func(obj jsonObject) (bool, error) {
                kind, err := jsonTag(obj, "type")
                if err != nil {
                    return false, err
                }
                changed := false
                switch kind {
                case "text":
                    if _, exists := obj["text"]; !exists {
                        return false, ErrInvalidJSON
                    }
                    err = rewriteField(obj, "text", transform.text(scope), &changed)
                case "image_url", "video_url", "input_audio", "file", "audio", "reasoning", "reasoning_text", "thinking", "redacted_thinking":
                default:
                    return false, ErrUnsupportedContent
                }
                return changed, err
            })
        }
        return rewriteArray(raw, part)
    }
}

func (transform *jsonTransform) chatCall(raw json.RawMessage) (json.RawMessage, bool, error) {
    return rewriteObject(raw, func(obj jsonObject) (bool, error) {
        kind, err := jsonTag(obj, "type")
        if err != nil {
            return false, err
        }
        if kind != "" && kind != "function" {
            return false, ErrUnsupportedContent
        }
        if _, exists := obj["custom"]; exists {
            return false, ErrUnsupportedContent
        }
        changed := false
        function := func(raw json.RawMessage) (json.RawMessage, bool, error) {
            return rewriteObject(raw, func(obj jsonObject) (bool, error) {
                change := false
                err := rewriteField(obj, "arguments", transform.embedded(ToolCalls), &change)
                return change, err
            })
        }
        err = rewriteField(obj, "function", function, &changed)
        return changed, err
    })
}

func (transform *jsonTransform) chatTool(raw json.RawMessage) (json.RawMessage, bool, error) {
    if !transform.snapshot.Processes(ToolDefinitions) {
        return raw, false, nil
    }
    return rewriteObject(raw, func(obj jsonObject) (bool, error) {
        kind, err := jsonTag(obj, "type")
        if err != nil {
            return false, err
        }
        if kind != "" && kind != "function" {
            return false, ErrUnsupportedContent
        }
        if _, exists := obj["custom"]; exists {
            return false, ErrUnsupportedContent
        }
        changed := false
        function := func(raw json.RawMessage) (json.RawMessage, bool, error) {
            return transform.descriptionSchema(raw, "parameters")
        }
        err = rewriteField(obj, "function", function, &changed)
        return changed, err
    })
}
