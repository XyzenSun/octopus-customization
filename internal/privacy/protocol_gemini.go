package privacy

import "encoding/json"

func (transform *jsonTransform) geminiRequest(raw json.RawMessage) (json.RawMessage, bool, error) {
    return rewriteObject(raw, func(obj jsonObject) (bool, error) {
        changed := false
        // snake_case 原始格式不是本项目的出站协议. 拒绝已知的
        // 替代载荷字段, 不静默宣称支持.
        if _, exists := obj["system_instruction"]; exists {
            if err := transform.unsupported(Messages); err != nil {
                return false, err
            }
        }
        if err := rewriteField(obj, "systemInstruction", transform.selected(Messages, transform.geminiContent), &changed); err != nil {
            return false, err
        }
        if err := rewriteField(obj, "contents", transform.selected(Messages|ToolCalls|ToolResponses, arrayOf(transform.geminiContent)), &changed); err != nil {
            return false, err
        }
        tool := func(raw json.RawMessage) (json.RawMessage, bool, error) {
            if !transform.snapshot.Processes(ToolDefinitions) {
                return raw, false, nil
            }
            return rewriteObject(raw, func(obj jsonObject) (bool, error) {
                change := false
                if _, exists := obj["function_declarations"]; exists {
                    return false, ErrUnsupportedContent
                }
                err := rewriteField(obj, "functionDeclarations", arrayOf(transform.geminiDeclaration), &change)
                return change, err
            })
        }
        if err := rewriteField(obj, "tools", transform.selected(ToolDefinitions, arrayOf(tool)), &changed); err != nil {
            return false, err
        }
        config := func(raw json.RawMessage) (json.RawMessage, bool, error) {
            if !transform.snapshot.Processes(ToolDefinitions) {
                return raw, false, nil
            }
            return rewriteObject(raw, func(obj jsonObject) (bool, error) {
                change := false
                for _, key := range []string{"responseSchema", "responseJsonSchema"} {
                    if err := rewriteField(obj, key, transform.schema, &change); err != nil {
                        return false, err
                    }
                }
                return change, nil
            })
        }
        if _, exists := obj["generation_config"]; exists {
            if err := transform.unsupported(ToolDefinitions); err != nil {
                return false, err
            }
        }
        if err := rewriteField(obj, "generationConfig", config, &changed); err != nil {
            return false, err
        }
        return changed, nil
    })
}

func (transform *jsonTransform) geminiResponse(raw json.RawMessage) (json.RawMessage, bool, error) {
    return rewriteObject(raw, func(obj jsonObject) (bool, error) {
        changed := false
        candidate := func(raw json.RawMessage) (json.RawMessage, bool, error) {
            return rewriteObject(raw, func(obj jsonObject) (bool, error) {
                change := false
                err := rewriteField(obj, "content", transform.geminiContent, &change)
                return change, err
            })
        }
        if err := rewriteField(obj, "candidates", transform.selected(Messages|ToolCalls|ToolResponses, arrayOf(candidate)), &changed); err != nil {
            return false, err
        }
        if err := rewriteField(obj, "error", nullable(transform.errorEvent), &changed); err != nil {
            return false, err
        }
        return changed, nil
    })
}

func (transform *jsonTransform) geminiContent(raw json.RawMessage) (json.RawMessage, bool, error) {
    if !transform.snapshot.Processes(Messages | ToolCalls | ToolResponses) {
        return raw, false, nil
    }
    return rewriteObject(raw, func(obj jsonObject) (bool, error) {
        changed := false
        err := rewriteField(obj, "parts", arrayOf(transform.geminiPart), &changed)
        return changed, err
    })
}

func (transform *jsonTransform) geminiPart(raw json.RawMessage) (json.RawMessage, bool, error) {
    return rewriteObject(raw, func(obj jsonObject) (bool, error) {
        if thought, exists := obj["thought"]; exists {
            var value bool
            if rawKind(thought) == 'n' || json.Unmarshal(thought, &value) != nil {
                return false, ErrInvalidJSON
            }
            if value {
                return false, nil
            }
        }
        changed := false
        for key, scope := range map[string]Scope{
            "function_call": ToolCalls, "function_response": ToolResponses,
            "executableCode": ToolCalls, "codeExecutionResult": ToolResponses,
        } {
            if _, exists := obj[key]; exists {
                if err := transform.unsupported(scope); err != nil {
                    return false, err
                }
            }
        }
        if err := rewriteField(obj, "text", transform.text(Messages), &changed); err != nil {
            return false, err
        }
        call := func(raw json.RawMessage) (json.RawMessage, bool, error) {
            if !transform.snapshot.Processes(ToolCalls) {
                return raw, false, nil
            }
            return rewriteObject(raw, func(obj jsonObject) (bool, error) {
                change := false
                err := rewriteField(obj, "args", transform.business(ToolCalls), &change)
                return change, err
            })
        }
        response := func(raw json.RawMessage) (json.RawMessage, bool, error) {
            if !transform.snapshot.Processes(ToolResponses) {
                return raw, false, nil
            }
            return rewriteObject(raw, func(obj jsonObject) (bool, error) {
                change := false
                err := rewriteField(obj, "response", transform.business(ToolResponses), &change)
                return change, err
            })
        }
        if err := rewriteField(obj, "functionCall", call, &changed); err != nil {
            return false, err
        }
        if err := rewriteField(obj, "functionResponse", response, &changed); err != nil {
            return false, err
        }
        return changed, nil
    })
}

func (transform *jsonTransform) geminiDeclaration(raw json.RawMessage) (json.RawMessage, bool, error) {
    if !transform.snapshot.Processes(ToolDefinitions) {
        return raw, false, nil
    }
    return rewriteObject(raw, func(obj jsonObject) (bool, error) {
        changed := false
        if err := rewriteField(obj, "description", transform.text(ToolDefinitions), &changed); err != nil {
            return false, err
        }
        for _, key := range []string{"parameters", "parametersJsonSchema", "response", "responseJsonSchema"} {
            if err := rewriteField(obj, key, transform.schema, &changed); err != nil {
                return false, err
            }
        }
        return changed, nil
    })
}
