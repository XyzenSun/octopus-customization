package relay

import (
    "context"
    "errors"
    "io"
    "net/http"
    "reflect"
    "strings"
    "sync"
    "testing"

    "github.com/bestruirui/octopus/internal/model"
    "github.com/looplj/axonhub/llm"
    "github.com/looplj/axonhub/llm/httpclient"
    "github.com/looplj/axonhub/llm/streams"
    "github.com/looplj/axonhub/llm/transformer"
    "github.com/looplj/axonhub/llm/transformer/anthropic"
    "github.com/looplj/axonhub/llm/transformer/doubao"
    "github.com/looplj/axonhub/llm/transformer/gemini"
    "github.com/looplj/axonhub/llm/transformer/openai"
    "github.com/looplj/axonhub/llm/transformer/openai/responses"
)

func TestPrivacyRawUsage(t *testing.T) {
    t.Run("JSONAndPinnedAdapters", privacyRawUsageJSON)
    t.Run("PresenceZeroAndMalformed", privacyRawUsagePresence)
    t.Run("CumulativeMerge", privacyRawUsageMerge)
    t.Run("NativeStreams", privacyRawUsageNativeStreams)
    t.Run("ErrorCancelAndClose", privacyRawUsageFailure)
    t.Run("TransportCapabilities", privacyRawUsageTransport)
    t.Run("ConcurrentSnapshots", privacyRawUsageConcurrent)
}

func privacyAssertRawUsage(t *testing.T, got, want *llm.Usage) {
    t.Helper()
    if !reflect.DeepEqual(got, want) {
        t.Fatalf("usage = %#v, want %#v", got, want)
    }
}

func privacyRawUsageJSON(t *testing.T) {
    chat := `{"id":"chat","model":"test","choices":[{"index":0,"message":{"role":"assistant","content":"13969909666"},"finish_reason":"stop"}],"usage":{"prompt_tokens":100,"completion_tokens":20,"total_tokens":120,"prompt_tokens_details":{"cached_tokens":30,"write_cached_tokens":7,"audio_tokens":2},"completion_tokens_details":{"reasoning_tokens":4,"audio_tokens":1,"accepted_prediction_tokens":3,"rejected_prediction_tokens":2},"cost":{"amount":"0.2"},"private_text":"张三"}}`
    chatWant := &llm.Usage{
        PromptTokens: 100, CompletionTokens: 20, TotalTokens: 120,
        PromptTokensDetails:     &llm.PromptTokensDetails{CachedTokens: 30, WriteCachedTokens: 7, AudioTokens: 2},
        CompletionTokensDetails: &llm.CompletionTokensDetails{ReasoningTokens: 4, AudioTokens: 1, AcceptedPredictionTokens: 3, RejectedPredictionTokens: 2},
    }
    response := `{"id":"resp","model":"test","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"张三"}]}],"usage":{"input_tokens":100,"output_tokens":20,"total_tokens":120,"input_tokens_details":{"cached_tokens":30,"cache_write_tokens":7},"output_tokens_details":{"reasoning_tokens":4},"cost":0.2}}`
    anthropicBody := `{"id":"msg","type":"message","model":"test","role":"assistant","content":[{"type":"text","text":"张三"}],"stop_reason":"end_turn","usage":{"input_tokens":100,"output_tokens":20,"cache_read_input_tokens":30,"cache_creation_input_tokens":7,"cache_creation":{"ephemeral_5m_input_tokens":5,"ephemeral_1h_input_tokens":2},"cost":0.2}}`
    geminiBody := `{"modelVersion":"test","candidates":[{"content":{"role":"model","parts":[{"text":"张三"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":100,"toolUsePromptTokenCount":10,"candidatesTokenCount":20,"thoughtsTokenCount":4,"totalTokenCount":134,"cachedContentTokenCount":30,"promptTokensDetails":[{"modality":"TEXT","tokenCount":100}],"candidatesTokensDetails":[{"modality":"TEXT","tokenCount":20}],"cost":0.2}}`
    tests := []struct {
        name    string
        format  llm.APIFormat
        body    string
        want    *llm.Usage
        adapter func(string, string) (transformer.Outbound, error)
    }{
        {"Chat", llm.APIFormatOpenAIChatCompletion, chat, chatWant, openai.NewOutboundTransformer},
        {"Doubao", model.ChannelTypeDoubao, chat, chatWant, doubao.NewOutboundTransformer},
        {"Responses", llm.APIFormatOpenAIResponse, response, &llm.Usage{
            PromptTokens: 100, CompletionTokens: 20, TotalTokens: 120,
            PromptTokensDetails:     &llm.PromptTokensDetails{CachedTokens: 30, WriteCachedTokens: 7},
            CompletionTokensDetails: &llm.CompletionTokensDetails{ReasoningTokens: 4},
        }, func(url, key string) (transformer.Outbound, error) { return responses.NewOutboundTransformer(url, key) }},
        {"Anthropic", llm.APIFormatAnthropicMessage, anthropicBody, &llm.Usage{
            PromptTokens: 137, CompletionTokens: 20, TotalTokens: 157,
            PromptTokensDetails:     &llm.PromptTokensDetails{CachedTokens: 30, WriteCachedTokens: 7, WriteCached5MinTokens: 5, WriteCached1HourTokens: 2},
            CompletionTokensDetails: &llm.CompletionTokensDetails{},
        }, anthropic.NewOutboundTransformer},
        {"Gemini", llm.APIFormatGeminiContents, geminiBody, &llm.Usage{
            PromptTokens: 110, CompletionTokens: 24, TotalTokens: 134,
            PromptTokensDetails:            &llm.PromptTokensDetails{CachedTokens: 30},
            CompletionTokensDetails:        &llm.CompletionTokensDetails{ReasoningTokens: 4},
            PromptModalityTokenDetails:     []llm.ModalityTokenCount{{Modality: "TEXT", TokenCount: 100}},
            CompletionModalityTokenDetails: []llm.ModalityTokenCount{{Modality: "TEXT", TokenCount: 20}},
        }, gemini.NewOutboundTransformer},
    }
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            capture := newRawUsageCapture(tt.format)
            body := []byte(tt.body)
            capture.ObserveJSON(body)
            privacyAssertRawUsage(t, capture.Usage(), tt.want)
            adapter, err := tt.adapter("https://upstream.invalid", "synthetic-key")
            if err != nil {
                t.Fatal(err)
            }
            converted, err := adapter.TransformResponse(t.Context(), &httpclient.Response{StatusCode: 200, Body: body})
            if err != nil {
                t.Fatal(err)
            }
            privacyAssertRawUsage(t, capture.Usage(), converted.Usage)
            // 返回的明细, 切片及已观察字节缓冲区均不属于观察器内部状态.
            u := capture.Usage()
            u.PromptTokens = -1
            if u.PromptTokensDetails != nil {
                u.PromptTokensDetails.CachedTokens = -1
            }
            if u.CompletionTokensDetails != nil {
                u.CompletionTokensDetails.ReasoningTokens = -1
            }
            if len(u.PromptModalityTokenDetails) > 0 {
                u.PromptModalityTokenDetails[0].TokenCount = -1
            }
            if len(u.CompletionModalityTokenDetails) > 0 {
                u.CompletionModalityTokenDetails[0].TokenCount = -1
            }
            for i := range body {
                body[i] = 'x'
            }
            privacyAssertRawUsage(t, capture.Usage(), tt.want)
        })
    }
    t.Run("ChatFallbacks", func(t *testing.T) {
        c := newRawUsageCapture(llm.APIFormatOpenAIChatCompletion)
        c.ObserveJSON([]byte(`{"usage":{"prompt_tokens":10,"completion_tokens":4,"cached_tokens":3,"reasoning_tokens":2}}`))
        privacyAssertRawUsage(t, c.Usage(), &llm.Usage{PromptTokens: 10, CompletionTokens: 4,
            PromptTokensDetails: &llm.PromptTokensDetails{CachedTokens: 3}, CompletionTokensDetails: &llm.CompletionTokensDetails{ReasoningTokens: 2}})
        c.ObserveJSON([]byte(`{"usage":{"prompt_tokens_details":{"cached_tokens":1},"completion_tokens_details":{"reasoning_tokens":1}}}`))
        privacyAssertRawUsage(t, c.Usage(), &llm.Usage{PromptTokens: 10, CompletionTokens: 4,
            PromptTokensDetails: &llm.PromptTokensDetails{CachedTokens: 1}, CompletionTokensDetails: &llm.CompletionTokensDetails{ReasoningTokens: 1}})
    })
    t.Run("ChatFallbackExplicitZero", func(t *testing.T) {
        for _, format := range []llm.APIFormat{llm.APIFormatOpenAIChatCompletion, model.ChannelTypeDoubao} {
            t.Run(format.String(), func(t *testing.T) {
                c := newRawUsageCapture(format)
                c.ObserveEvent(&httpclient.StreamEvent{Data: []byte(`{"usage":{"prompt_tokens":10,"completion_tokens":4,"cached_tokens":3,"reasoning_tokens":2}}`)})
                privacyAssertRawUsage(t, c.Usage(), &llm.Usage{PromptTokens: 10, CompletionTokens: 4,
                    PromptTokensDetails: &llm.PromptTokensDetails{CachedTokens: 3}, CompletionTokensDetails: &llm.CompletionTokensDetails{ReasoningTokens: 2}})
                c.ObserveEvent(&httpclient.StreamEvent{Data: []byte(`{"usage":{"prompt_tokens_details":{"cached_tokens":0}}}`)})
                privacyAssertRawUsage(t, c.Usage(), &llm.Usage{PromptTokens: 10, CompletionTokens: 4,
                    CompletionTokensDetails: &llm.CompletionTokensDetails{ReasoningTokens: 2}})
                c.ObserveEvent(&httpclient.StreamEvent{Data: []byte(`{"usage":{"completion_tokens_details":{"reasoning_tokens":0}}}`)})
                privacyAssertRawUsage(t, c.Usage(), &llm.Usage{PromptTokens: 10, CompletionTokens: 4})
                c.ObserveEvent(&httpclient.StreamEvent{Data: []byte(`{"usage":{"cached_tokens":7,"reasoning_tokens":6,"prompt_tokens_details":{"cached_tokens":null},"completion_tokens_details":{"reasoning_tokens":null}}}`)})
                privacyAssertRawUsage(t, c.Usage(), &llm.Usage{PromptTokens: 10, CompletionTokens: 4})
            })
        }
    })
    t.Run("AnthropicFallbackExplicitZero", func(t *testing.T) {
        capture := newRawUsageCapture(llm.APIFormatAnthropicMessage)
        capture.ObserveJSON([]byte(`{"usage":{"input_tokens":10,"cached_tokens":20}}`))
        capture.ObserveEvent(&httpclient.StreamEvent{Type: "message_delta", Data: []byte(`{"type":"message_delta","usage":{"cache_read_input_tokens":0,"output_tokens":4}}`)})
        usage := capture.Usage()
        if usage.PromptTokens != 10 || usage.CompletionTokens != 4 || usage.PromptTokensDetails != nil {
            t.Fatal("explicit zero cache read was replaced by an old fallback")
        }
    })
    t.Run("AnthropicTTLOnly", func(t *testing.T) {
        c := newRawUsageCapture(llm.APIFormatAnthropicMessage)
        c.ObserveJSON([]byte(`{"usage":{"input_tokens":10,"output_tokens":4,"cache_creation":{"ephemeral_5m_input_tokens":3}}}`))
        privacyAssertRawUsage(t, c.Usage(), &llm.Usage{PromptTokens: 10, CompletionTokens: 4, TotalTokens: 14,
            PromptTokensDetails: &llm.PromptTokensDetails{WriteCached5MinTokens: 3}, CompletionTokensDetails: &llm.CompletionTokensDetails{}})
    })
}

func privacyRawUsagePresence(t *testing.T) {
    for _, format := range []llm.APIFormat{llm.APIFormatOpenAIChatCompletion, model.ChannelTypeDoubao, llm.APIFormatOpenAIResponse, llm.APIFormatAnthropicMessage, llm.APIFormatGeminiContents} {
        t.Run(format.String(), func(t *testing.T) {
            c := newRawUsageCapture(format)
            for _, body := range []string{`{}`, `{"usage":null,"usageMetadata":null}`, `{"usage":[],"usageMetadata":[]}`, `{"usage":"text","usageMetadata":"text"}`, `{"usage":{"prompt_tokens":1.5},"usageMetadata":{"promptTokenCount":1.5}}`, `{"usage":{"output_tokens":"4"},"usageMetadata":{"candidatesTokenCount":"4"}}`, `{"usage":{"prompt_tokens":9223372036854775808},"usageMetadata":{"promptTokenCount":9223372036854775808}}`, `{"usage":{"prompt_tokens":5}`, `[DONE]`, `{"content":{"usage":{"prompt_tokens":99}}}`} {
                c.ObserveJSON([]byte(body))
                privacyAssertRawUsage(t, c.Usage(), nil)
            }
            c.ObserveEvent(nil)
            c.ObserveEvent(&httpclient.StreamEvent{})
            privacyAssertRawUsage(t, c.Usage(), nil)
            c.ObserveJSON([]byte(`{"usage":{"prompt_tokens":0,"input_tokens":0,"completion_tokens":0,"output_tokens":0,"total_tokens":0},"usageMetadata":{"promptTokenCount":0,"candidatesTokenCount":0,"totalTokenCount":0}}`))
            if c.Usage() == nil {
                t.Fatal("explicit zero usage is unavailable")
            }
            if !c.counts[rawUsagePrompt].present || !c.counts[rawUsageCompletion].present || c.counts[rawUsageCached].present {
                t.Fatal("counter presence was not preserved")
            }
            before := c.Usage()
            c.ObserveJSON([]byte(`{"usage":null,"usageMetadata":null}`))
            privacyAssertRawUsage(t, c.Usage(), before)
        })
    }
    for _, format := range []llm.APIFormat{llm.APIFormatOpenAIEmbedding, llm.APIFormatOpenAIImageGeneration, llm.APIFormatOpenAIImageEdit, llm.APIFormatOpenAIImageVariation, llm.APIFormatGeminiEmbedding, "unknown"} {
        c := newRawUsageCapture(format)
        c.ObserveJSON([]byte(`{"usage":{"prompt_tokens":100},"usageMetadata":{"promptTokenCount":100}}`))
        c.ObserveEvent(&httpclient.StreamEvent{Data: []byte(`{"usage":{"prompt_tokens":100}}`)})
        privacyAssertRawUsage(t, c.Usage(), nil)
    }
    c := newRawUsageCapture(llm.APIFormatOpenAIChatCompletion)
    c.ObserveJSON([]byte(`{"usage":{}}`))
    privacyAssertRawUsage(t, c.Usage(), &llm.Usage{})
    c.ObserveJSON([]byte(`{"usage":{"prompt_tokens":9007199254740993}}`))
    privacyAssertRawUsage(t, c.Usage(), &llm.Usage{PromptTokens: 9007199254740993})
    c.ObserveJSON([]byte(`{"usage":{"prompt_tokens":3,"completion_tokens":"bad"}}`))
    privacyAssertRawUsage(t, c.Usage(), &llm.Usage{PromptTokens: 9007199254740993})
}

func privacyRawUsageMerge(t *testing.T) {
    tests := []struct {
        format llm.APIFormat
        bodies []string
        want   *llm.Usage
    }{
        {llm.APIFormatOpenAIChatCompletion, []string{
            `{"usage":{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12,"prompt_tokens_details":{"cached_tokens":4,"write_cached_tokens":2}}}`,
            `{"usage":{"completion_tokens":5,"total_tokens":15,"prompt_tokens_details":{"write_cached_tokens":0}}}`,
            `{"usage":{"completion_tokens":0,"prompt_tokens":null}}`,
        }, &llm.Usage{PromptTokens: 10, TotalTokens: 15, PromptTokensDetails: &llm.PromptTokensDetails{CachedTokens: 4}}},
        {llm.APIFormatOpenAIResponse, []string{
            `{"usage":{"input_tokens":10,"output_tokens":2,"total_tokens":12,"input_tokens_details":{"cached_tokens":4,"cache_write_tokens":2}}}`,
            `{"usage":{"output_tokens":5,"total_tokens":15,"input_tokens_details":{"cache_write_tokens":0}}}`,
            `{"usage":{"output_tokens":0,"input_tokens":null}}`,
        }, &llm.Usage{PromptTokens: 10, TotalTokens: 15, PromptTokensDetails: &llm.PromptTokensDetails{CachedTokens: 4}, CompletionTokensDetails: &llm.CompletionTokensDetails{}}},
        {llm.APIFormatAnthropicMessage, []string{
            `{"usage":{"input_tokens":10,"output_tokens":2,"cache_read_input_tokens":4,"cache_creation_input_tokens":2,"cache_creation":{"ephemeral_5m_input_tokens":2}}}`,
            `{"usage":{"output_tokens":5,"cache_creation_input_tokens":0,"cache_creation":{"ephemeral_5m_input_tokens":0}}}`,
            `{"usage":{"output_tokens":0,"input_tokens":null}}`,
        }, &llm.Usage{PromptTokens: 14, TotalTokens: 14, PromptTokensDetails: &llm.PromptTokensDetails{CachedTokens: 4}, CompletionTokensDetails: &llm.CompletionTokensDetails{}}},
        {llm.APIFormatGeminiContents, []string{
            `{"usageMetadata":{"promptTokenCount":10,"toolUsePromptTokenCount":2,"candidatesTokenCount":2,"thoughtsTokenCount":1,"totalTokenCount":15,"cachedContentTokenCount":4,"promptTokensDetails":[{"modality":"TEXT","tokenCount":10}]}}`,
            `{"usageMetadata":{"candidatesTokenCount":5,"thoughtsTokenCount":0,"toolUsePromptTokenCount":0,"totalTokenCount":15}}`,
            `{"usageMetadata":{"candidatesTokenCount":0,"promptTokenCount":null,"promptTokensDetails":[]}}`,
        }, &llm.Usage{PromptTokens: 10, TotalTokens: 15, PromptTokensDetails: &llm.PromptTokensDetails{CachedTokens: 4}}},
    }
    for _, tt := range tests {
        t.Run(tt.format.String(), func(t *testing.T) {
            c := newRawUsageCapture(tt.format)
            for _, body := range tt.bodies {
                c.ObserveJSON([]byte(body))
                c.ObserveJSON([]byte(body))
            }
            privacyAssertRawUsage(t, c.Usage(), tt.want)
        })
    }
}

func privacyRawUsageNativeStreams(t *testing.T) {
    chat := "data: {\"choices\":[{\"delta\":{\"content\":\"张三\"},\"finish_reason\":\"stop\"}]}\n\ndata: {\"choices\":[],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":0,\"total_tokens\":10}}\n\ndata: [DONE]\n\n"
    tests := []struct {
        format llm.APIFormat
        wire   string
        want   *llm.Usage
    }{
        {llm.APIFormatOpenAIChatCompletion, chat, &llm.Usage{PromptTokens: 10, TotalTokens: 10}},
        {model.ChannelTypeDoubao, chat, &llm.Usage{PromptTokens: 10, TotalTokens: 10}},
        {llm.APIFormatOpenAIResponse, "event: response.created\ndata: {\"response\":{\"usage\":null}}\n\nevent: response.completed\ndata: {\"response\":{\"usage\":{\"input_tokens\":10,\"output_tokens\":3,\"total_tokens\":13,\"input_tokens_details\":{\"cached_tokens\":4}}}}\n\n", &llm.Usage{PromptTokens: 10, CompletionTokens: 3, TotalTokens: 13, PromptTokensDetails: &llm.PromptTokensDetails{CachedTokens: 4}, CompletionTokensDetails: &llm.CompletionTokensDetails{}}},
        {llm.APIFormatAnthropicMessage, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":10,\"output_tokens\":0,\"cache_read_input_tokens\":4,\"cache_creation_input_tokens\":2}}}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":3}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n", &llm.Usage{PromptTokens: 16, CompletionTokens: 3, TotalTokens: 19, PromptTokensDetails: &llm.PromptTokensDetails{CachedTokens: 4, WriteCachedTokens: 2}, CompletionTokensDetails: &llm.CompletionTokensDetails{}}},
        {llm.APIFormatGeminiContents, "data: {\"usageMetadata\":{\"promptTokenCount\":10,\"candidatesTokenCount\":1,\"totalTokenCount\":11}}\n\ndata: {\"candidates\":[{\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"candidatesTokenCount\":3,\"thoughtsTokenCount\":2,\"totalTokenCount\":15}}\n\n", &llm.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15, CompletionTokensDetails: &llm.CompletionTokensDetails{ReasoningTokens: 2}}},
    }
    for _, tt := range tests {
        t.Run(tt.format.String(), func(t *testing.T) {
            // 使用固定依赖版本的真实 SSE 解码器, 仅在内存中运行, 不访问 HTTP 或网络.
            decoder := httpclient.NewDefaultSSEDecoder(t.Context(), io.NopCloser(strings.NewReader(tt.wire)))
            c := newRawUsageCapture(tt.format)
            wrapped := c.Wrap(decoder)
            for wrapped.Next() {
                original := decoder.Current()
                if wrapped.Current() != original || wrapped.Current() != original {
                    t.Fatal("wrapper did not return original event pointer")
                }
            }
            if err := wrapped.Err(); err != nil {
                t.Fatal(err)
            }
            if err := wrapped.Close(); err != nil {
                t.Fatal(err)
            }
            privacyAssertRawUsage(t, c.Usage(), tt.want)
        })
    }
    // 显式 SSE 类型优先; 生成内容或未知事件不算用量证据.
    for _, format := range []llm.APIFormat{llm.APIFormatOpenAIResponse, llm.APIFormatAnthropicMessage} {
        c := newRawUsageCapture(format)
        c.ObserveEvent(&httpclient.StreamEvent{Type: "content_block_delta", Data: []byte(`{"type":"message_delta","usage":{"output_tokens":99},"response":{"usage":{"input_tokens":99}}}`)})
        privacyAssertRawUsage(t, c.Usage(), nil)
    }
    // 默认 SSE 类型 message 使用 JSON 类型, 并保留 start/delta 用量合并.
    for _, eventType := range []string{"", "message"} {
        t.Run("AnthropicJSONType/"+eventType, func(t *testing.T) {
            c := newRawUsageCapture(llm.APIFormatAnthropicMessage)
            c.ObserveEvent(&httpclient.StreamEvent{Type: eventType, Data: []byte(`{"type":"message_start","message":{"usage":{"input_tokens":10,"output_tokens":0,"cache_read_input_tokens":4}}}`)})
            privacyAssertRawUsage(t, c.Usage(), &llm.Usage{PromptTokens: 14, TotalTokens: 14,
                PromptTokensDetails: &llm.PromptTokensDetails{CachedTokens: 4}, CompletionTokensDetails: &llm.CompletionTokensDetails{}})
            c.ObserveEvent(&httpclient.StreamEvent{Type: eventType, Data: []byte(`{"type":"message_delta","usage":{"input_tokens":0,"output_tokens":0,"cache_read_input_tokens":0}}`)})
            privacyAssertRawUsage(t, c.Usage(), &llm.Usage{CompletionTokensDetails: &llm.CompletionTokensDetails{}})
        })
    }
}

type privacyRawUsageSource struct {
    streams.Stream[*httpclient.StreamEvent]
    err, closeErr error
    closes        int
    ack, waited   bool
    boundaryCalls int
}

func (s *privacyRawUsageSource) Err() error                 { return s.err }
func (s *privacyRawUsageSource) Close() error               { s.closes++; return s.closeErr }
func (s *privacyRawUsageSource) AcknowledgeDelivery()       { s.ack = true }
func (s *privacyRawUsageSource) DeliveryAcknowledged() bool { return s.ack }
func (s *privacyRawUsageSource) WaitForFinalization()       { s.waited = true }
func (s *privacyRawUsageSource) ExpectedStreamChoices() int { return 2 }
func (s *privacyRawUsageSource) Deliver(_ context.Context, write func() (bool, error)) error {
    s.boundaryCalls++
    terminal, err := write()
    if terminal && err == nil {
        s.AcknowledgeDelivery()
    }
    return err
}

func privacyRawUsageFailure(t *testing.T) {
    for _, terminalErr := range []error{errors.New("upstream failure"), context.Canceled} {
        for _, policy := range []string{"continue", "abort", "cancel"} {
            t.Run(policy+"/"+terminalErr.Error(), func(t *testing.T) {
                closeErr := errors.New("close failure")
                event := &httpclient.StreamEvent{LastEventID: "original-id", Data: []byte(`{"usage":{"prompt_tokens":10,"completion_tokens":2}}`), CleanEOFCompletionEvidence: true}
                source := &privacyRawUsageSource{Stream: streams.SliceStream([]*httpclient.StreamEvent{nil, event}), err: terminalErr, closeErr: closeErr}
                c := newRawUsageCapture(llm.APIFormatOpenAIChatCompletion)
                wrapped := c.Wrap(source)
                if c.Usage() != nil {
                    t.Fatal("Wrap pre-read the source")
                }
                if !wrapped.Next() || wrapped.Current() != nil {
                    t.Fatal("nil event was filtered")
                }
                if !wrapped.Next() {
                    t.Fatal("usage event missing")
                }
                // 在 Next 时观察, 即使取消导致无法调用 Current 或交付事件.
                privacyAssertRawUsage(t, c.Usage(), &llm.Usage{PromptTokens: 10, CompletionTokens: 2})
                c.ObserveJSON([]byte(`{"usage":{"completion_tokens":3}}`))
                if wrapped.Current() != event || wrapped.Current() != event {
                    t.Fatal("original event changed")
                }
                if string(event.Data) != `{"usage":{"prompt_tokens":10,"completion_tokens":2}}` || event.LastEventID != "original-id" || !event.CleanEOFCompletionEvidence {
                    t.Fatal("original event bytes or metadata changed")
                }
                if c.Usage().CompletionTokens != 3 {
                    t.Fatal("Current re-observed an older cumulative value")
                }
                if wrapped.Next() {
                    t.Fatal("synthetic terminal event")
                }
                if wrapped.Err() != terminalErr || wrapped.Close() != closeErr || source.closes != 1 {
                    t.Fatal("source error/Close semantics changed")
                }
                privacyAssertRawUsage(t, c.Usage(), &llm.Usage{PromptTokens: 10, CompletionTokens: 3})
            })
        }
    }
    for _, terminal := range []string{"response.failed", "response.incomplete", "response.cancelled"} {
        for _, eventType := range []string{"", "message", terminal} {
            t.Run(terminal+"/"+eventType, func(t *testing.T) {
                c := newRawUsageCapture(llm.APIFormatOpenAIResponse)
                c.ObserveEvent(&httpclient.StreamEvent{Type: "response.in_progress", Data: []byte(`{"response":{"usage":{"input_tokens":8,"output_tokens":1,"total_tokens":9,"input_tokens_details":{"cached_tokens":4},"output_tokens_details":{"reasoning_tokens":3}}}}`)})
                privacyAssertRawUsage(t, c.Usage(), &llm.Usage{PromptTokens: 8, CompletionTokens: 1, TotalTokens: 9,
                    PromptTokensDetails: &llm.PromptTokensDetails{CachedTokens: 4}, CompletionTokensDetails: &llm.CompletionTokensDetails{ReasoningTokens: 3}})
                c.ObserveEvent(&httpclient.StreamEvent{Type: eventType, Data: []byte(`{"type":"` + terminal + `","response":{"usage":{"input_tokens":10,"output_tokens":2,"total_tokens":12,"input_tokens_details":{"cached_tokens":0},"output_tokens_details":{"reasoning_tokens":0}}}}`)})
                privacyAssertRawUsage(t, c.Usage(), &llm.Usage{PromptTokens: 10, CompletionTokens: 2, TotalTokens: 12,
                    PromptTokensDetails: &llm.PromptTokensDetails{}, CompletionTokensDetails: &llm.CompletionTokensDetails{}})
            })
        }
    }
    c := newRawUsageCapture(llm.APIFormatAnthropicMessage)
    c.ObserveEvent(&httpclient.StreamEvent{Type: "message_start", Data: []byte(`{"message":{"usage":{"input_tokens":10,"output_tokens":0}}}`)})
    c.ObserveEvent(&httpclient.StreamEvent{Type: "error", Data: []byte(`{"type":"error","error":{"message":"private"}}`)})
    privacyAssertRawUsage(t, c.Usage(), &llm.Usage{PromptTokens: 10, TotalTokens: 10, CompletionTokensDetails: &llm.CompletionTokensDetails{}})
}

func privacyRawUsageTransport(t *testing.T) {
    source := &privacyRawUsageSource{Stream: streams.SliceStream([]*httpclient.StreamEvent{})}
    original := httpclient.WithResponseHeaders(source, http.Header{httpclient.ReasoningIncludedHeader: []string{"true"}})
    c := newRawUsageCapture(llm.APIFormatOpenAIChatCompletion)
    wrapped := c.Wrap(original)
    if !reflect.DeepEqual(httpclient.GetResponseHeaders(wrapped), httpclient.GetResponseHeaders(original)) {
        t.Fatal("response metadata was lost")
    }
    if wrapped.(interface{ ExpectedStreamChoices() int }).ExpectedStreamChoices() != 2 {
        t.Fatal("expected choice count was lost")
    }
    wrapped.(streams.FinalizationWaiter).WaitForFinalization()
    if !source.waited {
        t.Fatal("finalization waiter not forwarded")
    }
    if err := wrapped.(streams.DeliveryBoundary).Deliver(t.Context(), func() (bool, error) { return true, nil }); err != nil {
        t.Fatal(err)
    }
    if source.boundaryCalls != 1 || !wrapped.(streams.DeliveryAcknowledger).DeliveryAcknowledged() {
        t.Fatal("delivery boundary not forwarded")
    }
    if c.Wrap(nil) != nil {
        t.Fatal("nil stream changed")
    }
    var nilCapture *RawUsageCapture
    if nilCapture.Wrap(original) != original || nilCapture.Usage() != nil {
        t.Fatal("nil capture not transparent")
    }
    nilCapture.ObserveJSON(nil)
    nilCapture.ObserveEvent(nil)
}

func privacyRawUsageConcurrent(t *testing.T) {
    c := newRawUsageCapture(llm.APIFormatGeminiContents)
    body := []byte(`{"usageMetadata":{"promptTokenCount":10,"toolUsePromptTokenCount":2,"candidatesTokenCount":3,"thoughtsTokenCount":1,"totalTokenCount":16,"cachedContentTokenCount":4,"promptTokensDetails":[{"modality":"TEXT","tokenCount":10}]}}`)
    var wg sync.WaitGroup
    for i := 0; i < 8; i++ {
        wg.Add(1)
        go func() {
            defer wg.Done()
            for j := 0; j < 200; j++ {
                c.ObserveJSON(body)
                c.ObserveEvent(&httpclient.StreamEvent{Data: body})
                u := c.Usage()
                if u.PromptTokens != 12 || u.CompletionTokens != 4 || u.TotalTokens != 16 {
                    t.Error("non-atomic usage snapshot")
                    return
                }
                u.PromptTokensDetails.CachedTokens = -1
                u.CompletionTokensDetails.ReasoningTokens = -1
                u.PromptModalityTokenDetails[0].TokenCount = -1
            }
        }()
    }
    wg.Wait()
    privacyAssertRawUsage(t, c.Usage(), &llm.Usage{PromptTokens: 12, CompletionTokens: 4, TotalTokens: 16,
        PromptTokensDetails: &llm.PromptTokensDetails{CachedTokens: 4}, CompletionTokensDetails: &llm.CompletionTokensDetails{ReasoningTokens: 1},
        PromptModalityTokenDetails: []llm.ModalityTokenCount{{Modality: "TEXT", TokenCount: 10}}})
}
