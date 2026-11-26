package relay

import (
    "bytes"
    "context"
    "fmt"
    "io"
    "mime/multipart"
    "net/http"
    "net/textproto"
    "reflect"
    "strings"
    "testing"
    "time"

    "github.com/bestruirui/octopus/internal/db"
    "github.com/bestruirui/octopus/internal/model"
    "github.com/bestruirui/octopus/internal/op"
    "github.com/bestruirui/octopus/internal/server/auth"
    "github.com/looplj/axonhub/llm"
    "github.com/looplj/axonhub/llm/httpclient"
)

var privacyRelayClients = []struct {
    name   string
    format llm.APIFormat
}{
    {"Chat", llm.APIFormatOpenAIChatCompletion},
    {"Responses", llm.APIFormatOpenAIResponse},
    {"Anthropic", llm.APIFormatAnthropicMessage},
}
var privacyRelayOutbounds = []struct {
    name   string
    format llm.APIFormat
}{
    {"Chat", llm.APIFormatOpenAIChatCompletion},
    {"Responses", llm.APIFormatOpenAIResponse},
    {"Anthropic", llm.APIFormatAnthropicMessage},
    {"Gemini", llm.APIFormatGeminiContents},
    {"Doubao", model.ChannelTypeDoubao},
}

// 仅模拟 HTTP 上游. 不替换匹配器, 鉴权, 数据库, 执行器,
// 转换器, relayRun 或 relayAttempt.
func TestPrivacyRelayMock(t *testing.T) {
    if !*octopusPrivacyWorker {
        privacyRelayWorker(t)
        return
    }
    privacyRelayInit(t)
    t.Run("AuthRouting", privacyRelayAuthRouting)
    t.Run("SwitchAndTrigger", privacyRelaySwitchAndTrigger)
    t.Run("RequestOptions", privacyRelayRequestOptions)
    t.Run("Scopes", privacyRelayScopes)
    t.Run("Excluded", privacyRelayExcluded)
    t.Run("JSONRestore", privacyRelayJSONRestore)
    t.Run("SSERestore", privacyRelaySSERestore)
    t.Run("Lifecycle", privacyRelayLifecycle)
    t.Run("Snapshot", privacyRelaySnapshot)
    t.Run("FailurePolicy", privacyRelayFailurePolicy)
    t.Run("AttemptsUsageLog", privacyRelayAttemptsUsageLog)
    t.Run("RawHTTPErrorUsage", privacyRelayHTTPErrorUsage)
    t.Run("EmptyHeartbeatFIFO", privacyRelayHeartbeatFIFO)
    if !t.Failed() {
        fmt.Println("OCTOPUS_PRIVACY_RELAY_WORKER_OK")
    }
}

func privacyRelayAuthRouting(t *testing.T) {
    for _, name := range []string{"MissingKey", "WrongKey", "UnknownGeneratedKey", "DisabledKey", "ExpiredKey", "OverCostKey", "ContentType", "RestrictedDirect", "RestrictedWrongGroup"} {
        t.Run(name, func(t *testing.T) {
            privacyRelayReset(t)
            up := privacyRelayMockUpstream(t, llm.APIFormatOpenAIChatCompletion, "ok")
            h := privacyRelayNewHarness(t, up, false, nil)
            key, contentType, requestModel, wantStatus := h.key.APIKey, "application/json", h.group.Name, http.StatusUnauthorized
            switch name {
            case "MissingKey":
                key = ""
            case "WrongKey":
                key = "invalid-synthetic-key"
            case "UnknownGeneratedKey":
                key = auth.GenerateAPIKey()
                if key == "" {
                    t.Fatal("empty generated key")
                }
            case "DisabledKey":
                h.key.Enabled = false
            case "ExpiredKey":
                h.key.ExpireAt = time.Now().Add(-time.Hour).Unix()
            case "OverCostKey":
                h.key.MaxCost = 1
                op.StatsAPIKeyUpdate(h.key.ID, model.StatsMetrics{InputCost: 2})
            case "ContentType":
                contentType, wantStatus = "text/plain", http.StatusUnsupportedMediaType
            case "RestrictedDirect":
                h.key.SupportedModels, requestModel, wantStatus = h.channel.Name+"/"+h.channel.Model, h.channel.Name+"/"+h.channel.Model, http.StatusBadRequest
            case "RestrictedWrongGroup":
                h.key.SupportedModels, wantStatus = "another-synthetic-group", http.StatusBadRequest
            }
            if err := op.APIKeyUpdate(&h.key, t.Context()); err != nil {
                t.Fatal(err)
            }
            status, _ := h.exchange("/v1/chat/completions", privacyRelayRequest(llm.APIFormatOpenAIChatCompletion, requestModel, "ok", false, false), key, contentType)
            if status != wantStatus || up.calls() != 0 {
                t.Fatalf("status=%d want=%d, upstream calls=%d want=0", status, wantStatus, up.calls())
            }
        })
    }
    t.Run("SameNameGroupPriorityAndRestrictedGroupAllowed", func(t *testing.T) {
        privacyRelayReset(t)
        up := privacyRelayMockUpstream(t, llm.APIFormatOpenAIChatCompletion, "ok")
        h := privacyRelayNewHarness(t, up, false, func(channel *model.Channel, group *model.Group) { group.Name = channel.Name + "/" + channel.Model })
        h.key.SupportedModels = h.group.Name
        if err := op.APIKeyUpdate(&h.key, t.Context()); err != nil {
            t.Fatal(err)
        }
        h.json(llm.APIFormatOpenAIChatCompletion, privacyRelayRequest(llm.APIFormatOpenAIChatCompletion, h.group.Name, "ok", false, false))
        if up.calls() != 1 {
            t.Fatal("restricted key did not reach same-name group exactly once")
        }
        rows := h.logs()
        if len(rows) != 1 || rows[0].Attempts[0].ModelName != h.channel.Model {
            t.Fatal("group routing log missing")
        }
    })
}

func privacyRelaySwitchAndTrigger(t *testing.T) {
    cases := []struct {
        name, request, response string
        triggered               bool
        setup                   func(*testing.T)
    }{
        {"Disabled", privacyRelaySource, privacyRelayMarker, false, func(t *testing.T) { privacyRelaySet(t, model.SettingKeyPrivacyEnabled, "false") }},
        {"NoEnabledRules", privacyRelaySource, privacyRelayMarker, false, func(t *testing.T) {
            rules, err := op.PrivacyRuleList(t.Context())
            if err != nil {
                t.Fatal(err)
            }
            for _, rule := range rules {
                privacyRelayUpdateRule(t, rule, rule.Source.String(), rule.Alias.String(), false)
            }
        }},
        {"AllScopesOff", privacyRelaySource, privacyRelayMarker, false, func(t *testing.T) { privacyRelaySet(t, model.SettingKeyPrivacyScopes, "0") }},
        {"NoChange", "ordinary synthetic text", "ordinary synthetic reply", false, nil},
        {"OutboundOnly", privacyRelaySource, "ordinary synthetic reply", true, nil},
        {"WritebackOnly", "ordinary synthetic text", privacyRelayMarker, true, nil},
    }
    for _, tc := range cases {
        t.Run(tc.name, func(t *testing.T) {
            privacyRelayReset(t)
            if tc.setup != nil {
                tc.setup(t)
            }
            up := privacyRelayMockUpstream(t, llm.APIFormatOpenAIChatCompletion, tc.response)
            h := privacyRelayNewHarness(t, up, false, nil)
            body := h.json(llm.APIFormatOpenAIChatCompletion, privacyRelayRequest(llm.APIFormatOpenAIChatCompletion, h.group.Name, tc.request, false, false))
            wantRequest, wantResponse := tc.request, tc.response
            if tc.name == "OutboundOnly" {
                wantRequest = privacyRelayMarker
            }
            if tc.name == "WritebackOnly" {
                wantResponse = privacyRelaySource
            }
            privacyRelayWant(t, privacyRelayDocument(t, up.request(t, 0).body), "messages.0.content", wantRequest)
            privacyRelayWant(t, privacyRelayDocument(t, body), "choices.0.message.content", wantResponse)
            h.log(tc.triggered)
        })
    }
}

func privacyRelayRequestOptions(t *testing.T) {
    for _, direct := range []bool{false, true} {
        t.Run(fmt.Sprintf("Direct=%t", direct), func(t *testing.T) {
            privacyRelayReset(t)
            up := privacyRelayMockUpstream(t, llm.APIFormatOpenAIChatCompletion, "ok")
            h := privacyRelayNewHarness(t, up, direct, func(channel *model.Channel, group *model.Group) {
                group.ParamOverride = new(`{"messages":[{"role":"user","content":"group synthetic"}],"temperature":0.7,"group_only":true,"delete_me":123}`)
                channel.ParamOverride = new(`{"messages":[{"role":"user","content":"13969909666"}],"temperature":0,"delete_me":null}`)
                group.CustomHeader = []model.CustomHeader{{HeaderKey: "X-Order", HeaderValue: new("group")}, {HeaderKey: "X-Delete", HeaderValue: new("group")}}
                channel.CustomHeader = []model.CustomHeader{{HeaderKey: "x-order", HeaderValue: new(privacyRelaySource)}, {HeaderKey: "X-Delete", HeaderValue: nil}}
            })
            h.json(llm.APIFormatOpenAIChatCompletion, privacyRelayRequest(llm.APIFormatOpenAIChatCompletion, h.requestModel(direct), "original synthetic", false, false))
            r := up.request(t, 0)
            root := privacyRelayDocument(t, r.body).(map[string]any)
            privacyRelayWant(t, root, "messages.0.content", privacyRelayMarker)
            privacyRelayWant(t, root, "temperature", 0)
            if _, ok := root["delete_me"]; ok {
                t.Fatal("null override was not deleted")
            }
            if _, ok := root["group_only"]; ok == direct {
                t.Fatal("group options were not applied only to group route")
            }
            if r.headers.Get("X-Order") != privacyRelaySource || r.headers.Get("X-Delete") != "" || r.headers.Get("Authorization") != "Bearer "+privacyRelayUpstreamKey {
                t.Fatal("headers changed or override order incorrect")
            }
            if r.contentLength >= 0 && r.contentLength != int64(len(r.body)) {
                t.Fatal("stale request Content-Length")
            }
            h.log(true)
        })
    }
}

func privacyRelayScopes(t *testing.T) {
    t.Run("DefaultToolCallScopeOffJSONAndSSE", func(t *testing.T) {
        for _, client := range privacyRelayClients {
            for _, stream := range []bool{false, true} {
                t.Run(fmt.Sprintf("%s/Stream=%t", client.name, stream), func(t *testing.T) {
                    privacyRelayReset(t) // 默认仅启用 Messages, 明确关闭 ToolCalls
                    up := privacyRelayMockUpstream(t, client.format, "unused")
                    up.body = privacyRelayResponse(client.format, privacyRelayMarker, true)
                    if stream {
                        args := string(privacyRelayJSON(map[string]any{"value": privacyRelayMarker}))
                        up.frames = privacyRelayStreamFrames(client.format, []string{args[:15], args[15:]}, true, true)
                    }
                    h := privacyRelayNewHarness(t, up, true, nil)
                    request := privacyRelayRequest(client.format, h.requestModel(true), privacyRelaySource, stream, true)
                    if stream {
                        frames := h.stream(client.format, request)
                        privacyRelayWant(t, privacyRelayDocument(t, []byte(privacyRelayDeltaText(t, client.format, frames, true))), "value", privacyRelayMarker)
                        privacyRelayWantStreamSnapshots(t, client.format, frames, privacyRelayMarker, true)
                    } else if privacyRelayToolValue(t, client.format, h.json(client.format, request)) != privacyRelayMarker {
                        t.Fatal("default scope unexpectedly restored tool arguments")
                    }
                    privacyRelayWantOutboundText(t, client.format, up.request(t, 0).body, privacyRelayMarker)
                    h.log(true)
                    h.usage(17, 5)
                })
            }
        }
    })
    for _, scope := range []int{1, 2, 4, 8, 15} {
        t.Run(fmt.Sprintf("Mask%d", scope), func(t *testing.T) {
            privacyRelayReset(t)
            privacyRelaySet(t, model.SettingKeyPrivacyScopes, fmt.Sprint(scope))
            up := privacyRelayMockUpstream(t, llm.APIFormatOpenAIChatCompletion, privacyRelayMarker)
            up.body = []byte(`{"id":"13969909666","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"[[OCTOPUS_PII:PHONE_TEST]]","tool_calls":[{"id":"13969909666","type":"function","function":{"name":"13969909666","arguments":"{\"value\":\"[[OCTOPUS_PII:PHONE_TEST]]\",\"name\":\"[[OCTOPUS_PII:PHONE_TEST]]\"}"}}]},"finish_reason":"tool_calls"},{"index":1,"message":{"role":"tool","content":"[[OCTOPUS_PII:PHONE_TEST]]"},"finish_reason":"stop"}],"usage":{"prompt_tokens":17,"completion_tokens":5,"total_tokens":22}}`)
            h := privacyRelayNewHarness(t, up, false, nil)
            request := []byte(fmt.Sprintf(`{"model":%s,"messages":[{"role":"user","content":"13969909666"},{"role":"assistant","tool_calls":[{"id":"13969909666","type":"function","function":{"name":"13969909666","arguments":"{\"value\":\"13969909666\",\"name\":\"13969909666\"}"}}]},{"role":"tool","tool_call_id":"13969909666","content":"13969909666"}],"tools":[{"type":"function","function":{"name":"13969909666","description":"13969909666","parameters":{"type":"object","description":"13969909666","properties":{"value":{"type":"string","description":"13969909666","enum":["13969909666"],"default":"13969909666"}},"examples":[{"description":"13969909666"}]}}}]}`, privacyRelayJSON(h.group.Name)))
            body := h.json(llm.APIFormatOpenAIChatCompletion, request)
            out := privacyRelayDocument(t, up.request(t, 0).body)
            in := privacyRelayDocument(t, body)
            for _, check := range []struct {
                bit                       int
                requestPath, responsePath string
            }{
                {1, "messages.0.content", "choices.0.message.content"},
                {8, "messages.2.content", "choices.1.message.content"},
            } {
                wantRequest, wantResponse := privacyRelaySource, privacyRelayMarker
                if scope&check.bit != 0 {
                    wantRequest, wantResponse = privacyRelayMarker, privacyRelaySource
                }
                privacyRelayWant(t, out, check.requestPath, wantRequest)
                privacyRelayWant(t, in, check.responsePath, wantResponse)
            }
            wantDefinition := privacyRelaySource
            if scope&2 != 0 {
                wantDefinition = privacyRelayMarker
            }
            for _, path := range []string{"tools.0.function.description", "tools.0.function.parameters.description", "tools.0.function.parameters.properties.value.description"} {
                privacyRelayWant(t, out, path, wantDefinition)
            }
            for _, path := range []string{"tools.0.function.parameters.properties.value.enum.0", "tools.0.function.parameters.properties.value.default", "tools.0.function.parameters.examples.0.description", "tools.0.function.name", "messages.1.tool_calls.0.id"} {
                privacyRelayWant(t, out, path, privacyRelaySource)
            }
            outArgs := privacyRelayDocument(t, []byte(privacyRelayAt(t, out, "messages.1.tool_calls.0.function.arguments").(string)))
            inArgs := privacyRelayDocument(t, []byte(privacyRelayAt(t, in, "choices.0.message.tool_calls.0.function.arguments").(string)))
            wantCallOut, wantCallIn := privacyRelaySource, privacyRelayMarker
            if scope&4 != 0 {
                wantCallOut, wantCallIn = privacyRelayMarker, privacyRelaySource
            }
            for _, path := range []string{"value", "name"} {
                privacyRelayWant(t, outArgs, path, wantCallOut)
                privacyRelayWant(t, inArgs, path, wantCallIn)
            }
            privacyRelayWant(t, in, "id", privacyRelaySource)
            h.log(true)
        })
    }
}

func privacyRelayExcluded(t *testing.T) {
    t.Run("ProtocolModelIDAndHeaderAreNotText", func(t *testing.T) {
        privacyRelayReset(t)
        up := privacyRelayMockUpstream(t, llm.APIFormatOpenAIChatCompletion, privacyRelayMarker)
        root := privacyRelayDocument(t, up.body).(map[string]any)
        root["id"], root["model"] = privacyRelayMarker, privacyRelayMarker
        up.body = privacyRelayJSON(root)
        h := privacyRelayNewHarness(t, up, true, func(channel *model.Channel, _ *model.Group) { channel.Model = privacyRelaySource })
        body := h.json(llm.APIFormatOpenAIChatCompletion, privacyRelayRequest(up.format, h.requestModel(true), privacyRelaySource, false, false))
        request := up.request(t, 0)
        privacyRelayWant(t, privacyRelayDocument(t, request.body), "model", privacyRelaySource)
        privacyRelayWant(t, privacyRelayDocument(t, request.body), "messages.0.content", privacyRelayMarker)
        privacyRelayWant(t, privacyRelayDocument(t, body), "id", privacyRelayMarker)
        privacyRelayWant(t, privacyRelayDocument(t, body), "model", privacyRelayMarker)
        if request.headers.Get("X-Synthetic-Privacy") != privacyRelaySource {
            t.Fatal("privacy scanned a protocol header")
        }
        h.log(true)
    })
    t.Run("ConversationRawExcludedFields", func(t *testing.T) {
        privacyRelayReset(t)
        privacyRelaySet(t, model.SettingKeyPrivacyScopes, "15")
        up := privacyRelayMockUpstream(t, llm.APIFormatOpenAIChatCompletion, privacyRelayMarker)
        root := privacyRelayDocument(t, up.body).(map[string]any)
        message := privacyRelayAt(t, root, "choices.0.message").(map[string]any)
        for _, key := range []string{"reasoning_content", "reasoning", "thought_signature"} {
            message[key] = privacyRelayMarker
        }
        message["annotations"] = []any{map[string]any{"text": privacyRelayMarker, "offset": privacyRelayLargeInteger}}
        message["audio"] = map[string]any{"transcript": privacyRelayMarker, "data": privacyRelayMarker}
        up.body = privacyRelayJSON(root)
        h := privacyRelayNewHarness(t, up, false, nil)
        request := []byte(fmt.Sprintf(`{"model":%s,"messages":[{"role":"user","content":[{"type":"text","text":"13969909666"},{"type":"image_url","image_url":{"url":"https://synthetic.invalid/13969909666"}}],"reasoning_content":"13969909666","thought_signature":"13969909666"}],"metadata":{"id":"13969909666"},"synthetic_integer":9007199254740993}`, privacyRelayJSON(h.group.Name)))
        body := h.json(llm.APIFormatOpenAIChatCompletion, request)
        out, in := privacyRelayDocument(t, up.request(t, 0).body), privacyRelayDocument(t, body)
        privacyRelayWant(t, out, "messages.0.content.0.text", privacyRelayMarker)
        privacyRelayWant(t, out, "messages.0.content.1.image_url.url", "https://synthetic.invalid/"+privacyRelaySource)
        privacyRelayWant(t, out, "messages.0.thought_signature", privacyRelaySource)
        privacyRelayWant(t, out, "synthetic_integer", privacyRelayLargeInteger)
        for _, key := range []string{"reasoning_content", "reasoning", "thought_signature"} {
            privacyRelayWant(t, in, "choices.0.message."+key, privacyRelayMarker)
        }
        privacyRelayWant(t, in, "choices.0.message.audio.transcript", privacyRelayMarker)
        privacyRelayWant(t, in, "synthetic_integer", privacyRelayLargeInteger)
        h.log(true)
    })
    // 开关前后使用相同上游样本, 比较实际载荷,
    // 而非转换器可合理修改的入站字节.
    for _, api := range []string{"Embedding", "ImageJSON", "ImageEditMultipart", "ImageVariationMultipart"} {
        for _, failure := range []bool{false, true} {
            t.Run(fmt.Sprintf("%s/Error=%t", api, failure), func(t *testing.T) {
                privacyRelayReset(t)
                var baselineRequest, baselineResponse any
                var fixtures []*privacyRelayHarness
                for _, enabled := range []bool{false, true} {
                    privacyRelaySet(t, model.SettingKeyPrivacyEnabled, fmt.Sprint(enabled))
                    format, path := llm.APIFormatOpenAIImageGeneration, "/v1/images/generations"
                    if api == "Embedding" {
                        format, path = llm.APIFormatOpenAIEmbedding, "/v1/embeddings"
                    }
                    if api == "ImageEditMultipart" {
                        format, path = llm.APIFormatOpenAIImageEdit, "/v1/images/edits"
                    }
                    if api == "ImageVariationMultipart" {
                        format, path = llm.APIFormatOpenAIImageVariation, "/v1/images/variations"
                    }
                    // 渠道保持 Chat 类型; 正式 newOutbound 选择向量或图像协议.
                    up := privacyRelayMockUpstream(t, llm.APIFormatOpenAIChatCompletion, "unused")
                    if format == llm.APIFormatOpenAIEmbedding {
                        up.body = []byte(`{"object":"list","model":"privacy-actual-model","data":[{"object":"embedding","index":0,"embedding":[0.25,0.5]}],"usage":{"prompt_tokens":17,"total_tokens":17},"synthetic_text":"[[OCTOPUS_PII:PHONE_TEST]]"}`)
                    } else {
                        up.body = []byte(`{"created":1,"data":[{"url":"https://synthetic.invalid/[[OCTOPUS_PII:PHONE_TEST]]","revised_prompt":"[[OCTOPUS_PII:PHONE_TEST]]"}]}`)
                    }
                    if failure {
                        up.status = http.StatusBadRequest
                        up.body = []byte(`{"error":{"message":"[[OCTOPUS_PII:PHONE_TEST]]","type":"invalid_request_error","code":"synthetic_error"}}`)
                    }
                    h := privacyRelayNewHarness(t, up, true, nil)
                    name := h.requestModel(true)
                    body := privacyRelayJSON(map[string]any{"model": name, "prompt": privacyRelaySource})
                    contentType := "application/json"
                    if api == "Embedding" {
                        body = privacyRelayJSON(map[string]any{"model": name, "input": privacyRelaySource})
                    }
                    if strings.Contains(api, "Multipart") {
                        var buffer bytes.Buffer
                        writer := multipart.NewWriter(&buffer)
                        if err := writer.WriteField("model", name); err != nil {
                            t.Fatal(err)
                        }
                        if api == "ImageEditMultipart" {
                            if err := writer.WriteField("prompt", privacyRelaySource); err != nil {
                                t.Fatal(err)
                            }
                        }
                        part, err := writer.CreatePart(textproto.MIMEHeader{
                            "Content-Disposition": {`form-data; name="image"; filename="synthetic.png"`},
                            "Content-Type":        {"image/png"},
                        })
                        if err != nil {
                            t.Fatal(err)
                        }
                        _, err = part.Write([]byte("\x89PNG\r\n\x1a\nsynthetic-image-bytes-" + privacyRelaySource))
                        if err != nil {
                            t.Fatal(err)
                        }
                        if err := writer.Close(); err != nil {
                            t.Fatal(err)
                        }
                        contentType, body = writer.FormDataContentType(), buffer.Bytes()
                    }
                    status, response := h.exchange(path, body, h.key.APIKey, contentType)
                    wantStatus := http.StatusOK
                    if failure {
                        wantStatus = http.StatusBadRequest
                    }
                    if status != wantStatus || up.calls() != 1 {
                        t.Fatalf("excluded status=%d want=%d calls=%d, synthetic response=%s", status, wantStatus, up.calls(), response)
                    }
                    var payload any
                    r := up.request(t, 0)
                    if strings.Contains(api, "Multipart") {
                        // 规范化边界和模型; 保留二进制内容本身.
                        fake, err := http.NewRequest(http.MethodPost, "http://127.0.0.1", bytes.NewReader(r.body))
                        if err != nil {
                            t.Fatal(err)
                        }
                        fake.Header.Set("Content-Type", r.headers.Get("Content-Type"))
                        if err := fake.ParseMultipartForm(1 << 20); err != nil {
                            t.Fatal(err)
                        }
                        defer fake.MultipartForm.RemoveAll()
                        file, _, err := fake.FormFile("image")
                        if err != nil {
                            t.Fatal(err)
                        }
                        data, err := io.ReadAll(file)
                        file.Close()
                        if err != nil {
                            t.Fatal(err)
                        }
                        payload = map[string]any{"prompt": fake.FormValue("prompt"), "image": string(data)}
                    } else {
                        payload = privacyRelayDocument(t, r.body)
                    }
                    clientPayload := privacyRelayDocument(t, response)
                    if !enabled {
                        baselineRequest, baselineResponse = payload, clientPayload
                    } else if !reflect.DeepEqual(baselineRequest, payload) || !reflect.DeepEqual(baselineResponse, clientPayload) {
                        t.Fatal("excluded API differs with privacy enabled")
                    }
                    fixtures = append(fixtures, h)
                }
                for _, h := range fixtures {
                    h.log(false)
                }
            })
        }
    }
}

func privacyRelayJSONRestore(t *testing.T) {
    t.Run("PipelineBaseline3x5", privacyRelayPipelineBaseline)
    t.Run("GlobalNonrecursiveUnknownAndEscapes", func(t *testing.T) {
        privacyRelayReset(t)
        privacyRelayRule(t, privacyRelayMarker, "NESTED_TEST", true)
        text := privacyRelayMarker + " / [[OCTOPUS_PII:NESTED_TEST]] / [[OCTOPUS_PII:UNKNOWN]] / " + privacyRelayEscapedMarker
        up := privacyRelayMockUpstream(t, llm.APIFormatOpenAIChatCompletion, text)
        h := privacyRelayNewHarness(t, up, false, nil)
        body := h.json(llm.APIFormatOpenAIChatCompletion, privacyRelayRequest(llm.APIFormatOpenAIChatCompletion, h.group.Name, "no matching input", false, false))
        privacyRelayWant(t, privacyRelayDocument(t, body), "choices.0.message.content", privacyRelaySource+" / "+privacyRelayMarker+" / [[OCTOPUS_PII:UNKNOWN]] / "+privacyRelayEscaped)
        h.log(true)
    })
    // 覆盖要求的 3 种客户端 x 5 种实际出站协议.
    // 预期值为手写原文和占位符, 不由隐私处理器生成.
    t.Run("OutboundMatrix3x5", func(t *testing.T) {
        for _, client := range privacyRelayClients {
            for _, outbound := range privacyRelayOutbounds {
                t.Run(client.name+"To"+outbound.name, func(t *testing.T) {
                    privacyRelayReset(t)
                    up := privacyRelayMockUpstream(t, outbound.format, privacyRelayMarker)
                    h := privacyRelayNewHarness(t, up, false, nil)
                    body := h.json(client.format, privacyRelayRequest(client.format, h.group.Name, privacyRelaySource, false, false))
                    privacyRelayWantOutboundText(t, outbound.format, up.request(t, 0).body, privacyRelayMarker)
                    privacyRelayWant(t, privacyRelayDocument(t, body), privacyRelayTextPath(client.format, false), privacyRelaySource)
                    if up.calls() != 1 {
                        t.Fatal("basic matrix request was retried")
                    }
                    h.log(true)
                    h.usage(17, 5)
                })
            }
        }
    })
    t.Run("SameProtocolPassthroughOnOff", func(t *testing.T) {
        for _, client := range privacyRelayClients {
            for _, passthrough := range []bool{false, true} {
                t.Run(fmt.Sprintf("%s/Enabled=%t", client.name, passthrough), func(t *testing.T) {
                    privacyRelayReset(t)
                    privacyRelaySet(t, model.SettingKeyPassthroughEnabled, fmt.Sprint(passthrough))
                    up := privacyRelayMockUpstream(t, client.format, privacyRelayMarker)
                    h := privacyRelayNewHarness(t, up, true, nil)
                    body := h.json(client.format, privacyRelayRequest(client.format, h.requestModel(true), privacyRelaySource, false, false))
                    privacyRelayWant(t, privacyRelayDocument(t, body), privacyRelayTextPath(client.format, false), privacyRelaySource)
                    if passthrough {
                        privacyRelayWant(t, privacyRelayDocument(t, body), "synthetic_integer", privacyRelayLargeInteger)
                    }
                    h.log(true)
                    h.usage(17, 5)
                })
            }
        }
    })
    t.Run("CrossProtocolToolJSON", func(t *testing.T) {
        for i, client := range privacyRelayClients {
            t.Run(client.name, func(t *testing.T) {
                privacyRelayReset(t)
                privacyRelaySet(t, model.SettingKeyPrivacyScopes, "5")
                outbound := privacyRelayClients[(i+1)%len(privacyRelayClients)].format
                up := privacyRelayMockUpstream(t, outbound, privacyRelayMarker+privacyRelayEscapedMarker)
                up.body = privacyRelayResponse(outbound, privacyRelayMarker+privacyRelayEscapedMarker, true)
                h := privacyRelayNewHarness(t, up, true, nil)
                body := h.json(client.format, privacyRelayRequest(client.format, h.requestModel(true), privacyRelaySource, false, true))
                if privacyRelayToolValue(t, client.format, body) != privacyRelaySource+privacyRelayEscaped {
                    t.Fatal("cross protocol tool JSON not restored with correct escaping")
                }
                h.log(true)
                h.usage(17, 5)
            })
        }
    })
    t.Run("RawRefusalAndNativeReplay", func(t *testing.T) {
        for _, client := range privacyRelayClients[1:] {
            t.Run(client.name, func(t *testing.T) {
                privacyRelayReset(t)
                privacyRelaySet(t, model.SettingKeyPrivacyScopes, "15")
                up := privacyRelayMockUpstream(t, client.format, privacyRelayMarker)
                if client.format == llm.APIFormatOpenAIResponse {
                    root := privacyRelayDocument(t, up.body).(map[string]any)
                    root["output"] = []any{map[string]any{"type": "message", "role": "assistant", "id": "item_synthetic", "content": []any{map[string]any{"type": "refusal", "refusal": privacyRelayMarker}}}, map[string]any{"type": "custom_tool_call", "id": "custom_synthetic", "call_id": "call_synthetic", "name": "privacy_echo", "input": privacyRelayMarker}}
                    up.body = privacyRelayJSON(root)
                } else {
                    root := privacyRelayDocument(t, up.body).(map[string]any)
                    root["content"] = []any{map[string]any{"type": "text", "text": privacyRelayMarker}, map[string]any{"type": "server_tool_use", "id": "call_synthetic", "name": "web_search", "input": map[string]any{"query": privacyRelayMarker}}}
                    up.body = privacyRelayJSON(root)
                }
                h := privacyRelayNewHarness(t, up, true, nil)
                body := h.json(client.format, privacyRelayRequest(client.format, h.requestModel(true), "ordinary synthetic", false, false))
                root := privacyRelayDocument(t, body)
                if client.format == llm.APIFormatOpenAIResponse {
                    privacyRelayWant(t, root, "output.0.content.0.refusal", privacyRelaySource)
                    privacyRelayWant(t, root, "output.1.input", privacyRelaySource)
                } else {
                    privacyRelayWant(t, root, "content.1.input.query", privacyRelaySource)
                }
                h.log(true)
            })
        }
    })
}

// 隐私功能就绪前, 这些基线先用固定依赖版本的真实管线验证上游样本.
// 用于区分引擎信息丢失与隐私回归, 不替代验收测试.
func privacyRelayPipelineBaseline(t *testing.T) {
    for _, client := range privacyRelayClients {
        for _, outbound := range privacyRelayOutbounds {
            t.Run(client.name+"To"+outbound.name, func(t *testing.T) {
                privacyRelayReset(t)
                privacyRelaySet(t, model.SettingKeyPrivacyEnabled, "false")
                up := privacyRelayMockUpstream(t, outbound.format, privacyRelayMarker)
                h := privacyRelayNewHarness(t, up, false, nil)
                body := h.json(client.format, privacyRelayRequest(client.format, h.group.Name, privacyRelaySource, false, false))
                privacyRelayWantOutboundText(t, outbound.format, up.request(t, 0).body, privacyRelaySource)
                privacyRelayWant(t, privacyRelayDocument(t, body), privacyRelayTextPath(client.format, false), privacyRelayMarker)
                h.usage(17, 5)
                if up.calls() != 1 {
                    t.Fatal("pipeline baseline retried")
                }
                if _, ok := privacyRelayDocument(t, body).(map[string]any)["synthetic_integer"]; !ok {
                    t.Log("AXONHUB_INFORMATION_LOSS: unknown synthetic_integer metadata omitted by conversion; privacy must not reconstruct it")
                }
            })
        }
    }
}

// 生成完整逻辑 SSE 事件; tcpStep 独立控制线上写入分段.
// 仅拆分 TCP 写入不算跨增量测试.
func privacyRelayStreamFrames(format llm.APIFormat, pieces []string, tool bool, terminal bool) []privacyRelayFrame {
    var frames []privacyRelayFrame
    add := func(kind string, payload map[string]any) {
        index := len(frames)
        payload["sequence_number"] = index
        frames = append(frames, privacyRelayFrame{kind: kind, id: fmt.Sprintf("synthetic-%d", index), data: string(privacyRelayJSON(payload))})
    }
    full := strings.Join(pieces, "")
    switch format {
    case llm.APIFormatOpenAIChatCompletion, model.ChannelTypeDoubao:
        for i, piece := range pieces {
            delta := map[string]any{"content": piece}
            if tool {
                fn := map[string]any{"arguments": piece}
                call := map[string]any{"index": 0, "function": fn}
                if i == 0 {
                    call["id"] = "call_synthetic"
                    call["type"] = "function"
                    fn["name"] = "privacy_echo"
                }
                delta = map[string]any{"tool_calls": []any{call}}
            }
            add("", map[string]any{"id": "response_synthetic", "object": "chat.completion.chunk", "model": "privacy-actual-model", "choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": nil}}})
        }
        if terminal {
            finish := "stop"
            if tool {
                finish = "tool_calls"
            }
            add("", map[string]any{"id": "response_synthetic", "object": "chat.completion.chunk", "choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": finish}}, "usage": map[string]any{"prompt_tokens": 17, "completion_tokens": 5, "total_tokens": 22}})
            frames = append(frames, privacyRelayFrame{id: fmt.Sprintf("synthetic-%d", len(frames)), data: "[DONE]"})
        }
    case llm.APIFormatOpenAIResponse:
        add("response.created", map[string]any{"type": "response.created", "response": map[string]any{"id": "response_synthetic", "object": "response", "status": "in_progress", "output": []any{}}})
        item := map[string]any{"id": "item_synthetic", "type": "message", "role": "assistant", "content": []any{}, "status": "in_progress"}
        if tool {
            item = map[string]any{"id": "item_synthetic", "type": "function_call", "call_id": "call_synthetic", "name": "privacy_echo", "arguments": "", "status": "in_progress"}
        }
        add("response.output_item.added", map[string]any{"type": "response.output_item.added", "output_index": 0, "item": item})
        if !tool {
            add("response.content_part.added", map[string]any{"type": "response.content_part.added", "output_index": 0, "item_id": "item_synthetic", "content_index": 0, "part": map[string]any{"type": "output_text", "text": "", "annotations": []any{}}})
        }
        kind := "response.output_text.delta"
        if tool {
            kind = "response.function_call_arguments.delta"
        }
        for _, piece := range pieces {
            add(kind, map[string]any{"type": kind, "output_index": 0, "item_id": "item_synthetic", "content_index": 0, "delta": piece})
        }
        if terminal {
            kind := "response.output_text.done"
            field := "text"
            if tool {
                kind, field = "response.function_call_arguments.done", "arguments"
            }
            add(kind, map[string]any{"type": kind, "output_index": 0, "item_id": "item_synthetic", "content_index": 0, field: full})
            if tool {
                item["arguments"] = full
            } else {
                part := map[string]any{"type": "output_text", "text": full, "annotations": []any{}}
                add("response.content_part.done", map[string]any{"type": "response.content_part.done", "output_index": 0, "item_id": "item_synthetic", "content_index": 0, "part": part})
                item["content"] = []any{part}
            }
            item["status"] = "completed"
            add("response.output_item.done", map[string]any{"type": "response.output_item.done", "output_index": 0, "item": item})
            add("response.completed", map[string]any{"type": "response.completed", "response": map[string]any{"id": "response_synthetic", "object": "response", "status": "completed", "model": "privacy-actual-model", "output": []any{item}, "usage": map[string]any{"input_tokens": 17, "output_tokens": 5, "total_tokens": 22}}})
        }
    case llm.APIFormatAnthropicMessage:
        add("message_start", map[string]any{"type": "message_start", "message": map[string]any{"id": "response_synthetic", "type": "message", "role": "assistant", "model": "privacy-actual-model", "content": []any{}, "usage": map[string]any{"input_tokens": 11, "cache_read_input_tokens": 4, "cache_creation_input_tokens": 2, "output_tokens": 0}}})
        block := map[string]any{"type": "text", "text": ""}
        if tool {
            block = map[string]any{"type": "tool_use", "id": "call_synthetic", "name": "privacy_echo", "input": map[string]any{}}
        }
        add("content_block_start", map[string]any{"type": "content_block_start", "index": 0, "content_block": block})
        for _, piece := range pieces {
            delta := map[string]any{"type": "text_delta", "text": piece}
            if tool {
                delta = map[string]any{"type": "input_json_delta", "partial_json": piece}
            }
            add("content_block_delta", map[string]any{"type": "content_block_delta", "index": 0, "delta": delta})
        }
        if terminal {
            add("content_block_stop", map[string]any{"type": "content_block_stop", "index": 0})
            stop := "end_turn"
            if tool {
                stop = "tool_use"
            }
            add("message_delta", map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": stop, "stop_sequence": nil}, "usage": map[string]any{"output_tokens": 5}})
            add("message_stop", map[string]any{"type": "message_stop"})
        }
    case llm.APIFormatGeminiContents:
        if tool {
            panic("Gemini tool streaming is not used by this fixture")
        }
        for index, piece := range pieces {
            candidate := map[string]any{"index": 0, "content": map[string]any{"role": "model", "parts": []any{map[string]any{"text": piece}}}}
            payload := map[string]any{"responseId": "response_synthetic", "modelVersion": "privacy-actual-model", "candidates": []any{candidate}}
            if terminal && index == len(pieces)-1 {
                candidate["finishReason"] = "STOP"
                payload["usageMetadata"] = map[string]any{"promptTokenCount": 17, "candidatesTokenCount": 5, "totalTokenCount": 22}
            }
            add("", payload)
        }
    default:
        panic("unsupported stream fixture format")
    }
    return frames
}

func privacyRelayDeltaText(t *testing.T, format llm.APIFormat, frames []privacyRelayFrame, tool bool) string {
    t.Helper()
    var text strings.Builder
    for _, frame := range frames {
        if frame.data == "[DONE]" {
            continue
        }
        root := privacyRelayDocument(t, []byte(frame.data)).(map[string]any)
        switch format {
        case llm.APIFormatOpenAIChatCompletion:
            choices, _ := root["choices"].([]any)
            for _, value := range choices {
                choice := value.(map[string]any)
                delta, _ := choice["delta"].(map[string]any)
                if tool {
                    calls, _ := delta["tool_calls"].([]any)
                    for _, call := range calls {
                        function, _ := call.(map[string]any)["function"].(map[string]any)
                        if s, ok := function["arguments"].(string); ok {
                            text.WriteString(s)
                        }
                    }
                } else if s, ok := delta["content"].(string); ok {
                    text.WriteString(s)
                }
            }
        case llm.APIFormatOpenAIResponse:
            kind, _ := root["type"].(string)
            if (!tool && kind == "response.output_text.delta") || (tool && kind == "response.function_call_arguments.delta") {
                text.WriteString(root["delta"].(string))
            }
        case llm.APIFormatAnthropicMessage:
            if root["type"] == "content_block_delta" {
                delta := root["delta"].(map[string]any)
                if !tool && delta["type"] == "text_delta" {
                    text.WriteString(delta["text"].(string))
                }
                if tool && delta["type"] == "input_json_delta" {
                    text.WriteString(delta["partial_json"].(string))
                }
            }
        }
    }
    return text.String()
}

func privacyRelaySameFrameIdentity(t *testing.T, want, got []privacyRelayFrame) {
    t.Helper()
    if len(got) != len(want) {
        t.Fatalf("SSE count=%d want=%d (no added/deleted events)", len(got), len(want))
    }
    for i := range want {
        if got[i].kind != want[i].kind || got[i].id != want[i].id {
            t.Fatalf("SSE FIFO/ID changed at %d: %+v want %+v", i, got[i], want[i])
        }
        if got[i].data == "[DONE]" || want[i].data == "[DONE]" {
            if got[i].data != want[i].data {
                t.Fatal("DONE changed")
            }
            continue
        }
        root := privacyRelayDocument(t, []byte(got[i].data))
        privacyRelayWant(t, root, "sequence_number", i)
    }
}

func privacyRelaySSERestore(t *testing.T) {
    t.Run("OutboundMatrix3x5", func(t *testing.T) {
        for _, client := range privacyRelayClients {
            for _, outbound := range privacyRelayOutbounds {
                t.Run(client.name+"To"+outbound.name, func(t *testing.T) {
                    for _, enabled := range []bool{false, true} {
                        t.Run(fmt.Sprintf("Privacy=%t", enabled), func(t *testing.T) {
                            privacyRelayReset(t)
                            privacyRelaySet(t, model.SettingKeyPrivacyEnabled, fmt.Sprint(enabled))
                            up := privacyRelayMockUpstream(t, outbound.format, "unused")
                            up.frames = privacyRelayStreamFrames(outbound.format, []string{privacyRelayMarker[:14], privacyRelayMarker[14:]}, false, true)
                            up.tcpStep = 11
                            h := privacyRelayNewHarness(t, up, false, nil)
                            frames := h.stream(client.format, privacyRelayRequest(client.format, h.group.Name, privacyRelaySource, true, false))
                            wantRequest, wantResponse := privacyRelaySource, privacyRelayMarker
                            if enabled {
                                wantRequest, wantResponse = privacyRelayMarker, privacyRelaySource
                            }
                            privacyRelayWantOutboundText(t, outbound.format, up.request(t, 0).body, wantRequest)
                            if privacyRelayDeltaText(t, client.format, frames, false) != wantResponse {
                                t.Fatal("SSE outbound matrix text mismatch")
                            }
                            if up.calls() != 1 {
                                t.Fatal("SSE matrix retried upstream")
                            }
                            h.usage(17, 5)
                            if enabled {
                                h.log(true)
                            }
                        })
                    }
                })
            }
        }
    })
    t.Run("InterleavedResponsesAndAnthropic", privacyRelayInterleavedNativeStreams)
    t.Run("PipelineBaseline", func(t *testing.T) {
        for _, client := range privacyRelayClients {
            for _, passthrough := range []bool{false, true} {
                t.Run(fmt.Sprintf("%s/Passthrough=%t", client.name, passthrough), func(t *testing.T) {
                    privacyRelayReset(t)
                    privacyRelaySet(t, model.SettingKeyPrivacyEnabled, "false")
                    privacyRelaySet(t, model.SettingKeyPassthroughEnabled, fmt.Sprint(passthrough))
                    up := privacyRelayMockUpstream(t, client.format, "unused")
                    up.frames = privacyRelayStreamFrames(client.format, []string{privacyRelayMarker[:14], privacyRelayMarker[14:]}, false, true)
                    up.tcpStep = 3
                    h := privacyRelayNewHarness(t, up, true, nil)
                    frames := h.stream(client.format, privacyRelayRequest(client.format, h.requestModel(true), privacyRelaySource, true, false))
                    if privacyRelayDeltaText(t, client.format, frames, false) != privacyRelayMarker {
                        t.Fatal("pipeline did not preserve basic text delta bytes")
                    }
                    if passthrough {
                        privacyRelaySameFrameIdentity(t, up.frames, frames)
                    }
                    h.usage(17, 5)
                })
            }
        }
    })
    t.Run("SameProtocolPassthroughOnOff", func(t *testing.T) {
        for _, client := range privacyRelayClients {
            for _, passthrough := range []bool{false, true} {
                t.Run(fmt.Sprintf("%s/Enabled=%t", client.name, passthrough), func(t *testing.T) {
                    privacyRelayReset(t)
                    privacyRelaySet(t, model.SettingKeyPassthroughEnabled, fmt.Sprint(passthrough))
                    up := privacyRelayMockUpstream(t, client.format, "unused")
                    up.frames = privacyRelayStreamFrames(client.format, []string{privacyRelayMarker[:1], privacyRelayMarker[1:15], privacyRelayMarker[15:]}, false, true)
                    up.tcpStep = 7
                    h := privacyRelayNewHarness(t, up, true, nil)
                    frames := h.stream(client.format, privacyRelayRequest(client.format, h.requestModel(true), privacyRelaySource, true, false))
                    if privacyRelayDeltaText(t, client.format, frames, false) != privacyRelaySource {
                        t.Fatal("SSE marker not restored across logical deltas")
                    }
                    if passthrough {
                        privacyRelaySameFrameIdentity(t, up.frames, frames)
                    }
                    privacyRelayWantStreamSnapshots(t, client.format, frames, privacyRelaySource, false)
                    h.log(true)
                    h.usage(17, 5)
                })
            }
        }
    })
    t.Run("AllMarkerCuts", func(t *testing.T) {
        for _, client := range privacyRelayClients {
            for cut := 0; cut <= len(privacyRelayMarker); cut++ {
                t.Run(fmt.Sprintf("%s/Cut%d", client.name, cut), func(t *testing.T) {
                    privacyRelayReset(t)
                    up := privacyRelayMockUpstream(t, client.format, "unused")
                    up.frames = privacyRelayStreamFrames(client.format, []string{privacyRelayMarker[:cut], privacyRelayMarker[cut:]}, false, true)
                    h := privacyRelayNewHarness(t, up, true, nil)
                    frames := h.stream(client.format, privacyRelayRequest(client.format, h.requestModel(true), "ordinary synthetic", true, false))
                    privacyRelaySameFrameIdentity(t, up.frames, frames)
                    if privacyRelayDeltaText(t, client.format, frames, false) != privacyRelaySource {
                        t.Fatal("logical delta cut not restored")
                    }
                    h.log(true)
                })
            }
        }
    })
    t.Run("CrossProtocolToolSSE", func(t *testing.T) {
        for i, client := range privacyRelayClients {
            t.Run(client.name, func(t *testing.T) {
                privacyRelayReset(t)
                privacyRelaySet(t, model.SettingKeyPrivacyScopes, "5")
                outbound := privacyRelayClients[(i+1)%3].format
                args := string(privacyRelayJSON(map[string]any{"value": privacyRelayMarker + privacyRelayEscapedMarker}))
                var pieces []string
                for i := 0; i < len(args); i++ {
                    pieces = append(pieces, args[i:i+1])
                }
                up := privacyRelayMockUpstream(t, outbound, "unused")
                up.frames = privacyRelayStreamFrames(outbound, pieces, true, true)
                h := privacyRelayNewHarness(t, up, true, nil)
                frames := h.stream(client.format, privacyRelayRequest(client.format, h.requestModel(true), privacyRelaySource, true, true))
                text := privacyRelayDeltaText(t, client.format, frames, true)
                privacyRelayWant(t, privacyRelayDocument(t, []byte(text)), "value", privacyRelaySource+privacyRelayEscaped)
                h.log(true)
                h.usage(17, 5)
            })
        }
    })
    t.Run("InterleavedChoicesToolsAndUsageFIFO", func(t *testing.T) {
        privacyRelayReset(t)
        privacyRelaySet(t, model.SettingKeyPrivacyScopes, "5")
        up := privacyRelayMockUpstream(t, llm.APIFormatOpenAIChatCompletion, "unused")
        add := func(payload any) {
            i := len(up.frames)
            root := payload.(map[string]any)
            root["sequence_number"] = i
            up.frames = append(up.frames, privacyRelayFrame{id: fmt.Sprintf("synthetic-%d", i), data: string(privacyRelayJSON(root))})
        }
        chunk := func(index int, delta map[string]any) map[string]any {
            return map[string]any{"id": "response_synthetic", "object": "chat.completion.chunk", "choices": []any{map[string]any{"index": index, "delta": delta, "finish_reason": nil}}}
        }
        add(chunk(0, map[string]any{"content": privacyRelayMarker[:14]}))
        add(chunk(1, map[string]any{"content": "ordinary-other-choice", "reasoning_content": privacyRelayMarker}))
        add(map[string]any{"id": "response_synthetic", "object": "chat.completion.chunk", "choices": []any{}, "usage": map[string]any{"prompt_tokens": 17, "completion_tokens": 2, "total_tokens": 19}})
        add(chunk(0, map[string]any{"tool_calls": []any{map[string]any{"index": 0, "function": map[string]any{"name": "privacy_echo", "arguments": `{"value":"` + privacyRelayMarker[:10]}}}}))
        add(chunk(0, map[string]any{"content": privacyRelayMarker[14:]}))
        add(chunk(0, map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "call_late", "function": map[string]any{"arguments": privacyRelayMarker[10:] + `"}`}}}}))
        add(map[string]any{"id": "response_synthetic", "object": "chat.completion.chunk", "choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "tool_calls"}, map[string]any{"index": 1, "delta": map[string]any{}, "finish_reason": "stop"}}, "usage": map[string]any{"prompt_tokens": 17, "completion_tokens": 5, "total_tokens": 22}})
        up.frames = append(up.frames, privacyRelayFrame{id: fmt.Sprintf("synthetic-%d", len(up.frames)), data: "[DONE]"})
        h := privacyRelayNewHarness(t, up, true, nil)
        frames := h.stream(llm.APIFormatOpenAIChatCompletion, privacyRelayRequest(llm.APIFormatOpenAIChatCompletion, h.requestModel(true), "ordinary", true, true))
        privacyRelaySameFrameIdentity(t, up.frames, frames)
        privacyRelayWant(t, privacyRelayDocument(t, []byte(frames[0].data)), "choices.0.delta.content", privacyRelaySource)
        privacyRelayWant(t, privacyRelayDocument(t, []byte(frames[4].data)), "choices.0.delta.content", "")
        privacyRelayWant(t, privacyRelayDocument(t, []byte(frames[1].data)), "choices.0.delta.reasoning_content", privacyRelayMarker)
        privacyRelayWant(t, privacyRelayDocument(t, []byte(privacyRelayDeltaText(t, llm.APIFormatOpenAIChatCompletion, frames, true))), "value", privacyRelaySource)
        h.log(true)
        h.usage(17, 5)
    })
}

func privacyRelayWantStreamSnapshots(t *testing.T, format llm.APIFormat, frames []privacyRelayFrame, want string, tool bool) {
    t.Helper()
    if format != llm.APIFormatOpenAIResponse {
        return
    }
    seen := 0
    for _, frame := range frames {
        root := privacyRelayDocument(t, []byte(frame.data)).(map[string]any)
        var payload any
        var path string
        switch root["type"] {
        case "response.output_text.done":
            if tool {
                continue
            }
            payload, path = root, "text"
        case "response.function_call_arguments.done":
            if !tool {
                continue
            }
            payload, path = root, "arguments"
        case "response.content_part.done":
            if tool {
                continue
            }
            payload, path = root, "part.text"
        case "response.output_item.done":
            payload, path = root, "item.content.0.text"
            if tool {
                path = "item.arguments"
            }
        case "response.completed":
            payload, path = root, "response.output.0.content.0.text"
            if tool {
                path = "response.output.0.arguments"
            }
        default:
            continue
        }
        text := privacyRelayAt(t, payload, path).(string)
        if tool {
            privacyRelayWant(t, privacyRelayDocument(t, []byte(text)), "value", want)
        } else if text != want {
            t.Fatalf("independent final snapshot %s was not restored", path)
        }
        seen++
    }
    wantCount := 4
    if tool {
        wantCount = 3
    }
    if seen != wantCount {
        t.Fatalf("Responses final snapshot coverage=%d want=%d", seen, wantCount)
    }
}

func privacyRelayInterleavedNativeStreams(t *testing.T) {
    for _, client := range privacyRelayClients[1:] {
        t.Run(client.name, func(t *testing.T) {
            privacyRelayReset(t)
            privacyRelaySet(t, model.SettingKeyPrivacyScopes, "5")
            up := privacyRelayMockUpstream(t, client.format, "unused")
            var checks []map[string]string
            add := func(kind string, payload map[string]any, want map[string]string) {
                index := len(up.frames)
                payload["sequence_number"] = index
                up.frames = append(up.frames, privacyRelayFrame{kind: kind, id: fmt.Sprintf("synthetic-%d", index), data: string(privacyRelayJSON(payload))})
                checks = append(checks, want)
            }
            if client.format == llm.APIFormatOpenAIResponse {
                add("response.created", map[string]any{"type": "response.created", "response": map[string]any{"id": "response_synthetic", "status": "in_progress", "output": []any{}}}, nil)
                for index, id := range []string{"item_zero", "item_one"} {
                    add("response.output_item.added", map[string]any{"type": "response.output_item.added", "output_index": index, "item": map[string]any{"id": id, "type": "message", "role": "assistant", "content": []any{}}}, nil)
                    add("response.content_part.added", map[string]any{"type": "response.content_part.added", "output_index": index, "item_id": id, "content_index": 0, "part": map[string]any{"type": "output_text", "text": privacyRelayMarker}}, map[string]string{"part.text": privacyRelaySource})
                }
                add("response.output_text.delta", map[string]any{"type": "response.output_text.delta", "output_index": 0, "content_index": 0, "delta": privacyRelayMarker[:14]}, map[string]string{"delta": privacyRelaySource})
                add("response.output_text.delta", map[string]any{"type": "response.output_text.delta", "output_index": 1, "item_id": "item_one", "content_index": 0, "delta": privacyRelayMarker[:14]}, map[string]string{"delta": privacyRelaySource})
                // 元数据不能越过待处理的 FIFO 队首. ID 别名在第二个增量到达时,
                // 不重置稳定的 output_index 状态.
                add("response.in_progress", map[string]any{"type": "response.in_progress", "response": map[string]any{"id": "response_synthetic", "status": "in_progress"}}, nil)
                add("response.output_text.delta", map[string]any{"type": "response.output_text.delta", "item_id": "item_zero", "output_index": 0, "content_index": 0, "delta": privacyRelayMarker[14:]}, map[string]string{"delta": ""})
                add("response.output_text.delta", map[string]any{"type": "response.output_text.delta", "item_id": "item_one", "output_index": 1, "content_index": 0, "delta": privacyRelayMarker[14:]}, map[string]string{"delta": ""})
                // 固定依赖版本的类型化 StreamEvent 不表示原始拒绝内容.
                add("response.refusal.delta", map[string]any{"type": "response.refusal.delta", "item_id": "item_zero", "output_index": 0, "content_index": 1, "delta": privacyRelayMarker}, map[string]string{"delta": privacyRelaySource})
                add("response.refusal.done", map[string]any{"type": "response.refusal.done", "item_id": "item_zero", "output_index": 0, "content_index": 1, "refusal": privacyRelayMarker}, map[string]string{"refusal": privacyRelaySource})
                for index, id := range []string{"item_zero", "item_one"} {
                    add("response.output_text.done", map[string]any{"type": "response.output_text.done", "output_index": index, "item_id": id, "content_index": 0, "text": privacyRelayMarker}, map[string]string{"text": privacyRelaySource})
                    add("response.output_item.done", map[string]any{"type": "response.output_item.done", "output_index": index, "item": map[string]any{"id": id, "type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": privacyRelayMarker}}}}, map[string]string{"item.content.0.text": privacyRelaySource})
                }
                add("response.completed", map[string]any{"type": "response.completed", "response": map[string]any{"id": "response_synthetic", "status": "completed", "output": []any{}, "usage": map[string]any{"input_tokens": 17, "output_tokens": 5, "total_tokens": 22}}}, nil)
            } else {
                base := privacyRelayStreamFrames(client.format, nil, false, false)[0]
                add(base.kind, privacyRelayDocument(t, []byte(base.data)).(map[string]any), nil)
                add("content_block_start", map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "text", "text": privacyRelayMarker}}, map[string]string{"content_block.text": privacyRelaySource})
                add("content_block_start", map[string]any{"type": "content_block_start", "index": 1, "content_block": map[string]any{"type": "tool_use", "id": "call_synthetic", "name": "privacy_echo", "input": map[string]any{}}}, nil)
                add("content_block_start", map[string]any{"type": "content_block_start", "index": 2, "content_block": map[string]any{"type": "thinking", "thinking": privacyRelayMarker, "signature": privacyRelayMarker}}, nil)
                add("content_block_delta", map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "text_delta", "text": privacyRelayMarker[:14]}}, map[string]string{"delta.text": privacyRelaySource})
                add("content_block_delta", map[string]any{"type": "content_block_delta", "index": 1, "delta": map[string]any{"type": "input_json_delta", "partial_json": `{"value":"` + privacyRelayMarker[:14]}}, map[string]string{"delta.partial_json": `{"value":"` + privacyRelaySource})
                add("ping", map[string]any{"type": "ping"}, nil)
                add("content_block_delta", map[string]any{"type": "content_block_delta", "index": 2, "delta": map[string]any{"type": "signature_delta", "signature": privacyRelayMarker}}, nil)
                add("content_block_delta", map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "text_delta", "text": privacyRelayMarker[14:]}}, map[string]string{"delta.text": ""})
                add("content_block_delta", map[string]any{"type": "content_block_delta", "index": 1, "delta": map[string]any{"type": "input_json_delta", "partial_json": privacyRelayMarker[14:] + `"}`}}, map[string]string{"delta.partial_json": `"}`})
                for i := 0; i < 3; i++ {
                    add("content_block_stop", map[string]any{"type": "content_block_stop", "index": i}, nil)
                }
                add("message_delta", map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": "tool_use"}, "usage": map[string]any{"output_tokens": 5}}, nil)
                add("message_stop", map[string]any{"type": "message_stop"}, nil)
            }
            h := privacyRelayNewHarness(t, up, true, nil)
            frames := h.stream(client.format, privacyRelayRequest(client.format, h.requestModel(true), "ordinary", true, true))
            privacyRelaySameFrameIdentity(t, up.frames, frames)
            for index, expected := range checks {
                got := privacyRelayDocument(t, []byte(frames[index].data))
                want := privacyRelayDocument(t, []byte(up.frames[index].data))
                for path, value := range expected {
                    privacyRelayWant(t, got, path, value)
                    // 仅调整明确选中的文本, 将其余协议和元数据字段
                    // 与未修改的上游事件逐一比较.
                    parts := strings.Split(path, ".")
                    parent := want.(map[string]any)
                    if len(parts) > 1 {
                        parent = privacyRelayAt(t, want, strings.Join(parts[:len(parts)-1], ".")).(map[string]any)
                    }
                    parent[parts[len(parts)-1]] = value
                }
                if !reflect.DeepEqual(got, want) {
                    t.Fatalf("unselected native SSE field changed at event %d", index)
                }
            }
            h.log(true)
            h.usage(17, 5)
        })
    }
}

func privacyRelayResponseFailureUsage(t *testing.T) {
    for _, policy := range []string{"continue", "abort"} {
        t.Run(policy, func(t *testing.T) {
            privacyRelayReset(t)
            privacyRelaySet(t, model.SettingKeyPrivacyScopes, "5")
            privacyRelaySet(t, model.SettingKeyPrivacyFailurePolicy, policy)
            up := privacyRelayMockUpstream(t, llm.APIFormatOpenAIChatCompletion, "unused")
            root := privacyRelayDocument(t, privacyRelayResponse(up.format, privacyRelayMarker, true))
            fn := privacyRelayAt(t, root, "choices.0.message.tool_calls.0.function").(map[string]any)
            fn["arguments"] = strings.Repeat("[", 129) + string(privacyRelayJSON(privacyRelayMarker)) + strings.Repeat("]", 129)
            up.body = privacyRelayJSON(root)
            h := privacyRelayNewHarness(t, up, true, nil)
            status, body := h.exchange("/v1/chat/completions", privacyRelayRequest(up.format, h.requestModel(true), "ordinary", false, true), h.key.APIKey, "application/json")
            if up.calls() != 1 {
                t.Fatal("response privacy failure retried upstream")
            }
            if policy == "continue" {
                if status != http.StatusOK || !reflect.DeepEqual(privacyRelayDocument(t, body), root) {
                    t.Fatal("continue submitted partially restored response")
                }
            } else if status != http.StatusBadGateway {
                t.Fatal("non-stream response privacy abort did not return 502")
            }
            h.log(false)
            h.usage(17, 5)
            if op.StatsChannelGet(h.channel.ID).RequestFailed != 0 {
                t.Fatal("successful upstream counted as failed due to local response privacy error")
            }
        })
    }
}

func privacyRelayAwait(t *testing.T, ch <-chan struct{}, label string) {
    t.Helper()
    select {
    case <-ch:
    case <-t.Context().Done():
        t.Fatal(label, "suite deadline")
    case <-time.After(20 * time.Second):
        t.Fatal(label, "handshake timed out")
    }
}

func privacyRelayLifecycle(t *testing.T) {
    t.Run("CancelAfterUsageAndCommittedRestore", func(t *testing.T) {
        privacyRelayReset(t)
        up := privacyRelayMockUpstream(t, llm.APIFormatAnthropicMessage, "unused")
        up.frames = privacyRelayStreamFrames(up.format, []string{privacyRelayMarker}, false, false)
        up.pauseAfter = len(up.frames) - 1
        h := privacyRelayNewHarness(t, up, true, nil)
        ctx, cancel := context.WithCancel(t.Context())
        defer cancel()
        response := h.start(ctx, privacyRelayPath(up.format), privacyRelayRequest(up.format, h.requestModel(true), "ordinary", true, false), h.key.APIKey, "application/json")
        decoder := httpclient.NewDefaultSSEDecoder(ctx, response.Body)
        defer decoder.Close()
        for index := range up.frames {
            if !decoder.Next() {
                t.Fatalf("cancel fixture stopped before committed event %d: %v", index, decoder.Err())
            }
            if index == len(up.frames)-1 {
                privacyRelayWant(t, privacyRelayDocument(t, decoder.Current().Data), "delta.text", privacyRelaySource)
            }
        }
        privacyRelayAwait(t, up.paused, "cancel usage pause")
        cancel()
        decoder.Close()
        privacyRelayAwait(t, up.closed, "cancel usage upstream close")
        h.wait()
        row := h.log(true)
        if row.Error == "" || up.calls() != 1 {
            t.Fatal("cancel after usage was successful/retried")
        }
        h.usage(17, 0)
    })
    for _, mode := range []string{"Normal", "BareEOF", "Error", "Cancel"} {
        t.Run(mode, func(t *testing.T) {
            privacyRelayReset(t)
            up := privacyRelayMockUpstream(t, llm.APIFormatOpenAIChatCompletion, "unused")
            pieces := []string{privacyRelayMarker[:14], privacyRelayMarker[14:]}
            if mode == "BareEOF" {
                pieces = []string{privacyRelayMarker[:14]}
            }
            up.frames = privacyRelayStreamFrames(llm.APIFormatOpenAIChatCompletion, pieces, false, mode == "Normal")
            if mode == "Error" {
                up.frames = append(up.frames, privacyRelayFrame{id: "synthetic-error", data: `{"error":{"message":"[[OCTOPUS_PII:PHONE_TEST]]","type":"synthetic_error"}}`})
            }
            h := privacyRelayNewHarness(t, up, true, nil)
            if mode == "Cancel" {
                up.pauseAfter = 0
                ctx, cancel := context.WithCancel(t.Context())
                defer cancel()
                // 在没有待处理文本的事件后暂停, 使客户端能收到响应头.
                up.frames = privacyRelayStreamFrames(llm.APIFormatOpenAIChatCompletion, []string{"ordinary synthetic"}, false, false)
                response := h.start(ctx, "/v1/chat/completions", privacyRelayRequest(llm.APIFormatOpenAIChatCompletion, h.requestModel(true), "ordinary", true, false), h.key.APIKey, "application/json")
                privacyRelayAwait(t, up.paused, "upstream pause")
                cancel()
                response.Body.Close()
                privacyRelayAwait(t, up.closed, "upstream cancel close")
                h.wait()
                if up.calls() != 1 {
                    t.Fatal("cancel retried upstream")
                }
                rows := h.logs()
                if len(rows) != 1 || rows[0].Error == "" {
                    t.Fatal("cancel was logged as successful or audit was lost")
                }
                h.log(false)
                return
            }
            frames, readErr := h.streamOutcome(llm.APIFormatOpenAIChatCompletion, privacyRelayRequest(llm.APIFormatOpenAIChatCompletion, h.requestModel(true), "ordinary", true, false))
            if mode != "Error" && readErr != nil {
                t.Fatal("normal/EOF SSE read:", readErr)
            }
            if len(frames) != len(up.frames) {
                t.Fatal("termination synthesized or original error lost")
            }
            want := privacyRelaySource
            if mode == "BareEOF" {
                want = privacyRelayMarker[:14]
            }
            if privacyRelayDeltaText(t, llm.APIFormatOpenAIChatCompletion, frames, false) != want {
                t.Fatal("EOF/error tail was not handled literally")
            }
            if mode == "Error" {
                privacyRelayWant(t, privacyRelayDocument(t, []byte(frames[len(frames)-1].data)), "error.message", privacyRelaySource)
                rows := h.logs()
                if len(rows) != 1 || rows[0].Error == "" {
                    t.Fatal("actual error event was converted to success")
                }
            }
            h.log(mode != "BareEOF")
        })
    }
}

func privacyRelaySnapshot(t *testing.T) {
    for _, change := range []string{"Update", "Delete", "DisableRule", "SwitchOff"} {
        t.Run(change, func(t *testing.T) {
            privacyRelayReset(t)
            rules, err := op.PrivacyRuleList(t.Context())
            if err != nil {
                t.Fatal(err)
            }
            var rule model.PrivacyRule
            for _, candidate := range rules {
                if candidate.Source.String() == privacyRelaySource {
                    rule = candidate
                }
            }
            up := privacyRelayMockUpstream(t, llm.APIFormatOpenAIChatCompletion, "unused")
            up.frames = privacyRelayStreamFrames(llm.APIFormatOpenAIChatCompletion, []string{"lead " + privacyRelayMarker[:14], privacyRelayMarker[14:]}, false, true)
            up.pauseAfter = 0
            h := privacyRelayNewHarness(t, up, true, nil)
            result := make(chan []privacyRelayFrame, 1)
            // 并发消费真实 HTTP/SSE, 同时通过正式 op 发布新快照.
            // 使用暂停通道而非睡眠确定请求顺序.
            go func() {
                request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, h.entry.URL+"/v1/chat/completions", bytes.NewReader(privacyRelayRequest(llm.APIFormatOpenAIChatCompletion, h.requestModel(true), "ordinary", true, false)))
                if err != nil {
                    t.Error(err)
                    result <- nil
                    return
                }
                request.Header.Set("Authorization", "Bearer "+h.key.APIKey)
                request.Header.Set("Content-Type", "application/json")
                response, err := h.client.Do(request)
                if err != nil {
                    t.Error(err)
                    result <- nil
                    return
                }
                decoder := httpclient.NewDefaultSSEDecoder(t.Context(), response.Body)
                defer decoder.Close()
                var frames []privacyRelayFrame
                for decoder.Next() {
                    event := decoder.Current()
                    frames = append(frames, privacyRelayFrame{kind: event.Type, id: event.LastEventID, data: string(event.Data)})
                }
                if err := decoder.Err(); err != nil {
                    t.Error(err)
                }
                result <- frames
            }()
            privacyRelayAwait(t, up.paused, "snapshot pause")
            switch change {
            case "Update":
                privacyRelayUpdateRule(t, rule, "synthetic-new-source", "PHONE_TEST", true)
            case "Delete":
                if err := op.PrivacyRuleDelete(t.Context(), rule.ID); err != nil {
                    t.Fatal(err)
                }
            case "DisableRule":
                privacyRelayUpdateRule(t, rule, rule.Source.String(), rule.Alias.String(), false)
            case "SwitchOff":
                privacyRelaySet(t, model.SettingKeyPrivacyEnabled, "false")
            }
            close(up.release)
            var frames []privacyRelayFrame
            select {
            case frames = <-result:
            case <-time.After(20 * time.Second):
                t.Fatal("old stream did not finish")
            }
            h.wait()
            if privacyRelayDeltaText(t, llm.APIFormatOpenAIChatCompletion, frames, false) != "lead "+privacyRelaySource {
                t.Fatal("old request observed new snapshot mid-stream")
            }
            h.log(true)
            // 第二个独立配置的样本读取正式发布的新版本.
            up2 := privacyRelayMockUpstream(t, llm.APIFormatOpenAIChatCompletion, privacyRelayMarker)
            h2 := privacyRelayNewHarness(t, up2, true, nil)
            body := h2.json(llm.APIFormatOpenAIChatCompletion, privacyRelayRequest(llm.APIFormatOpenAIChatCompletion, h2.requestModel(true), "ordinary", false, false))
            want := privacyRelayMarker
            if change == "Update" {
                want = "synthetic-new-source"
            }
            privacyRelayWant(t, privacyRelayDocument(t, body), "choices.0.message.content", want)
            h2.log(change == "Update")
        })
    }
}

func privacyRelayFailurePolicy(t *testing.T) {
    t.Run("ResponseFailurePreservesUsage", privacyRelayResponseFailureUsage)
    for _, invalid := range []string{"Depth129", "UnsupportedNative"} {
        for _, policy := range []string{"continue", "abort"} {
            t.Run(invalid+"/"+policy, func(t *testing.T) {
                privacyRelayReset(t)
                privacyRelaySet(t, model.SettingKeyPrivacyScopes, "15")
                privacyRelaySet(t, model.SettingKeyPrivacyFailurePolicy, policy)
                up := privacyRelayMockUpstream(t, llm.APIFormatOpenAIChatCompletion, "ordinary")
                h := privacyRelayNewHarness(t, up, false, nil)
                root := privacyRelayDocument(t, privacyRelayRequest(llm.APIFormatOpenAIChatCompletion, h.group.Name, privacyRelaySource, false, true)).(map[string]any)
                if invalid == "Depth129" {
                    args := strings.Repeat("[", 129) + string(privacyRelayJSON(privacyRelaySource)) + strings.Repeat("]", 129)
                    root["messages"] = []any{map[string]any{"role": "user", "content": privacyRelaySource}, map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{"id": "call_synthetic", "type": "function", "function": map[string]any{"name": "privacy_echo", "arguments": args}}}}}
                } else {
                    root["tools"] = []any{map[string]any{"type": "custom", "custom": map[string]any{"name": "privacy_echo", "description": privacyRelaySource}}}
                }
                status, body := h.exchange("/v1/chat/completions", privacyRelayJSON(root), h.key.APIKey, "application/json")
                if policy == "abort" {
                    if status != http.StatusBadGateway || up.calls() != 0 {
                        t.Fatalf("local privacy abort status=%d calls=%d", status, up.calls())
                    }
                    if !strings.Contains(strings.ToLower(string(body)), "privacy") {
                        t.Fatal("abort omitted fixed privacy failure message")
                    }
                    channel, err := op.ChannelGet(h.channel.ID, t.Context())
                    if err != nil {
                        t.Fatal(err)
                    }
                    if channel.Keys[0].StatusCode != 0 || op.StatsChannelGet(channel.ID).RequestFailed != 0 {
                        t.Fatal("local privacy failure cooled key or charged channel failure")
                    }
                } else {
                    if status != http.StatusOK || up.calls() != 1 {
                        t.Fatalf("continue status=%d calls=%d", status, up.calls())
                    }
                    privacyRelayWant(t, privacyRelayDocument(t, up.request(t, 0).body), "messages.0.content", privacyRelaySource)
                }
                h.log(false)
            })
        }
    }
    for _, policy := range []string{"continue", "abort"} {
        t.Run("PendingEvent1025/"+policy, func(t *testing.T) {
            privacyRelayReset(t)
            privacyRelaySet(t, model.SettingKeyPrivacyFailurePolicy, policy)
            up := privacyRelayMockUpstream(t, llm.APIFormatOpenAIChatCompletion, "unused")
            up.frames = privacyRelayStreamFrames(llm.APIFormatOpenAIChatCompletion, []string{"already committed", privacyRelayMarker[:14]}, false, false)
            for i := 0; i < 1024; i++ {
                up.frames = append(up.frames, privacyRelayFrame{id: fmt.Sprintf("pending-%d", i), data: `{"id":"response_synthetic","object":"chat.completion.chunk","choices":[],"usage":{"prompt_tokens":17,"completion_tokens":5,"total_tokens":22}}`})
            }
            up.frames = append(up.frames, privacyRelayFrame{id: "tail", data: `{"id":"response_synthetic","choices":[{"index":0,"delta":{"content":"[[OCTOPUS_PII:PHONE_TEST]]"},"finish_reason":null}]}`})
            h := privacyRelayNewHarness(t, up, true, nil)
            frames, readErr := h.streamOutcome(llm.APIFormatOpenAIChatCompletion, privacyRelayRequest(llm.APIFormatOpenAIChatCompletion, h.requestModel(true), "ordinary", true, false))
            if policy == "continue" {
                if readErr != nil {
                    t.Fatal("continue SSE read:", readErr)
                }
                if len(frames) != len(up.frames) {
                    t.Fatal("continue did not flush original FIFO")
                }
                if !strings.HasSuffix(privacyRelayDeltaText(t, llm.APIFormatOpenAIChatCompletion, frames, false), privacyRelayMarker) {
                    t.Fatal("continue restarted restoration after resource failure")
                }
            } else {
                if readErr == nil {
                    t.Fatal("abort SSE ended without a client-visible read error")
                }
                if len(frames) != 1 {
                    t.Fatalf("abort committed pending events or fabricated terminal: %d", len(frames))
                }
                rows := h.logs()
                if len(rows) != 1 || rows[0].Error == "" {
                    t.Fatal("SSE abort not recorded as failure")
                }
            }
            h.log(false)
        })
    }
}

func privacyRelayAttemptsUsageLog(t *testing.T) {
    t.Run("FailoverRetryBooleanORAndUsageOnce", func(t *testing.T) {
        privacyRelayReset(t)
        first := privacyRelayMockUpstream(t, llm.APIFormatOpenAIChatCompletion, "unused")
        first.status = http.StatusInternalServerError
        first.body = []byte(`{"error":{"message":"synthetic upstream failure","type":"server_error"}}`)
        second := privacyRelayMockUpstream(t, llm.APIFormatOpenAIChatCompletion, "ordinary success")
        h := privacyRelayNewHarness(t, first, false, nil)
        channel := h.channel
        channel.ID = 0
        channel.Name += "_fallback"
        channel.BaseUrls = []model.BaseUrl{{URL: second.server.URL}}
        channel.Keys = []model.ChannelKey{{Enabled: true, ChannelKey: privacyRelayUpstreamKey}}
        // 本次尝试的参数将匹配消息替换为不匹配文本;
        // 仅失败的首次出站尝试贡献日志的 OR 标志.
        channel.ParamOverride = new(`{"messages":[{"role":"user","content":"ordinary fallback"}]}`)
        if err := op.ChannelCreate(&channel, t.Context()); err != nil {
            t.Fatal(err)
        }
        firstItem := h.group.Items[0]
        if _, err := op.GroupUpdate(&model.GroupUpdateRequest{ID: h.group.ID, ItemsToUpdate: []model.GroupItemUpdateRequest{{ID: firstItem.ID, Priority: 0}}, ItemsToAdd: []model.GroupItemAddRequest{{ChannelID: channel.ID, ModelName: channel.Model, Priority: 1}}}, t.Context()); err != nil {
            t.Fatal(err)
        }
        before := op.StatsTotalGet().StatsMetrics
        h.json(llm.APIFormatOpenAIChatCompletion, privacyRelayRequest(llm.APIFormatOpenAIChatCompletion, h.group.Name, privacyRelaySource, false, false))
        if first.calls() != 1 || second.calls() != 1 {
            t.Fatal("failover attempt counts incorrect")
        }
        privacyRelayWant(t, privacyRelayDocument(t, first.request(t, 0).body), "messages.0.content", privacyRelayMarker)
        privacyRelayWant(t, privacyRelayDocument(t, second.request(t, 0).body), "messages.0.content", "ordinary fallback")
        row := h.log(true)
        if row.TotalAttempts != 2 || row.ChannelId != channel.ID || row.Attempts[0].Status != model.AttemptFailed || row.Attempts[1].Status != model.AttemptSuccess {
            t.Fatal("failover audit did not preserve two attempts/final channel")
        }
        if op.StatsChannelGet(h.channel.ID).InputToken != 0 || op.StatsChannelGet(channel.ID).InputToken != 17 || op.StatsAPIKeyGet(h.key.ID).InputToken != 17 {
            t.Fatal("usage attributed to failed attempt or counted twice")
        }
        after := op.StatsTotalGet().StatsMetrics
        if after.InputToken-before.InputToken != 17 || after.OutputToken-before.OutputToken != 5 || after.RequestSuccess-before.RequestSuccess != 1 {
            t.Fatal("global usage/request count not once")
        }
    })
    t.Run("DirectExactlyOnceErrorRestored", func(t *testing.T) {
        privacyRelayReset(t)
        up := privacyRelayMockUpstream(t, llm.APIFormatOpenAIChatCompletion, "unused")
        up.status = http.StatusTooManyRequests
        up.body = []byte(`{"error":{"message":"[[OCTOPUS_PII:PHONE_TEST]]","type":"rate_limit_error","code":"synthetic_code"}}`)
        h := privacyRelayNewHarness(t, up, true, nil)
        status, body := h.exchange("/v1/chat/completions", privacyRelayRequest(llm.APIFormatOpenAIChatCompletion, h.requestModel(true), "ordinary", false, false), h.key.APIKey, "application/json")
        if status != http.StatusTooManyRequests || up.calls() != 1 {
            t.Fatal("direct error status or single-attempt invariant broken")
        }
        privacyRelayWant(t, privacyRelayDocument(t, body), "error.message", privacyRelaySource)
        row := h.log(true)
        if row.TotalAttempts != 1 {
            t.Fatal("direct attempted multiple candidates")
        }
    })
    t.Run("WrittenErrorStopsRetry", func(t *testing.T) {
        privacyRelayReset(t)
        up := privacyRelayMockUpstream(t, llm.APIFormatAnthropicMessage, "unused")
        up.frames = privacyRelayStreamFrames(llm.APIFormatAnthropicMessage, []string{privacyRelayMarker}, false, false)
        up.frames = append(up.frames, privacyRelayFrame{kind: "error", data: `{"type":"error","error":{"type":"overloaded_error","message":"[[OCTOPUS_PII:PHONE_TEST]]"}}`})
        h := privacyRelayNewHarness(t, up, false, nil)
        fallback := privacyRelayMockUpstream(t, llm.APIFormatAnthropicMessage, "ordinary")
        channel := h.channel
        channel.ID = 0
        channel.Name += "_fallback"
        channel.BaseUrls = []model.BaseUrl{{URL: fallback.server.URL}}
        channel.Keys = []model.ChannelKey{{Enabled: true, ChannelKey: privacyRelayUpstreamKey}}
        if err := op.ChannelCreate(&channel, t.Context()); err != nil {
            t.Fatal(err)
        }
        if _, err := op.GroupUpdate(&model.GroupUpdateRequest{ID: h.group.ID, ItemsToAdd: []model.GroupItemAddRequest{{ChannelID: channel.ID, ModelName: channel.Model, Priority: 1}}}, t.Context()); err != nil {
            t.Fatal(err)
        }
        frames, _ := h.streamOutcome(llm.APIFormatAnthropicMessage, privacyRelayRequest(llm.APIFormatAnthropicMessage, h.group.Name, "ordinary", true, false))
        if fallback.calls() != 0 || up.calls() != 1 {
            t.Fatal("written error caused fallback retry")
        }
        last := frames[len(frames)-1]
        privacyRelayWant(t, privacyRelayDocument(t, []byte(last.data)), "error.message", privacyRelaySource)
        row := h.log(true)
        if row.Error == "" {
            t.Fatal("upstream error was hidden as success")
        }
    })
    // 同时将统计和密钥缓存写入 SQLite; 上述审计断言读取实际数据行,
    // 而非订阅通知或内存日志列表.
    if err := op.StatsSaveDB(t.Context()); err != nil {
        t.Fatal(err)
    }
    if err := op.ChannelKeySaveDB(t.Context()); err != nil {
        t.Fatal(err)
    }
    var count int64
    if err := db.GetDB().Model(&model.RelayLog{}).Count(&count).Error; err != nil || count == 0 {
        t.Fatal("no persisted relay log evidence")
    }
}
