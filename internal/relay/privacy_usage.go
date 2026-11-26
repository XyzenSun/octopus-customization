package relay

import (
    "context"
    "encoding/json"
    "sync"

    "github.com/bestruirui/octopus/internal/model"
    "github.com/looplj/axonhub/llm"
    "github.com/looplj/axonhub/llm/httpclient"
    "github.com/looplj/axonhub/llm/streams"
)

// RawUsageCapture 在转换或隐私还原前, 按尝试观察实际出站协议的用量.
// 仅保留用量计数, 字段是否存在及模态明细, 不保存响应体或事件.
// 观察方法与 Usage 可并发调用, 包括取消期间. 首次使用后不可复制.
type RawUsageCapture struct {
    mu                                     sync.Mutex
    format                                 llm.APIFormat
    seen                                   bool
    counts                                 [rawUsageCounterCount]rawUsageCounter
    promptModalities, completionModalities []llm.ModalityTokenCount
}

type rawUsageCounter struct {
    value   int64
    present bool
}

const (
    rawUsagePrompt = iota
    rawUsageCompletion
    rawUsageTotal
    rawUsageCached
    rawUsageWriteCached
    rawUsageWrite5Min
    rawUsageWrite1Hour
    rawUsagePromptAudio
    rawUsageCompletionAudio
    rawUsageReasoning
    rawUsageAcceptedPrediction
    rawUsageRejectedPrediction
    rawUsageFallbackCached
    rawUsageFallbackReasoning
    rawUsageToolPrompt
    rawUsageThoughts
    rawUsageCounterCount
)

func newRawUsageCapture(format llm.APIFormat) *RawUsageCapture {
    return &RawUsageCapture{format: format}
}

// ObserveJSON 观察完整的上游非流式 JSON 响应. 缺失或 null 用量表示不可用, 而非零.
// 格式错误的用量整体忽略. 此观察器仅用于计费, 不改变协议错误处理.
func (c *RawUsageCapture) ObserveJSON(body []byte) {
    if c == nil {
        return
    }
    var envelope struct {
        Usage json.RawMessage `json:"usage"`
    }
    switch c.format {
    case llm.APIFormatGeminiContents:
        var gemini struct {
            Usage json.RawMessage `json:"usageMetadata"`
        }
        if json.Unmarshal(body, &gemini) == nil {
            c.observeUsage(gemini.Usage)
        }
    case llm.APIFormatOpenAIChatCompletion, model.ChannelTypeDoubao,
        llm.APIFormatOpenAIResponse, llm.APIFormatAnthropicMessage:
        if json.Unmarshal(body, &envelope) == nil {
            c.observeUsage(envelope.Usage)
        }
    }
}

// ObserveEvent 仅读取原始上游 Data. 非默认 SSE 类型优先于 JSON 类型.
// Responses 失败, 取消或未完成时仍观察原生生命周期用量. 后续错误或取消阻止
// message_stop 时, Anthropic start/delta 计数仍可读取. 不伪造完成状态.
func (c *RawUsageCapture) ObserveEvent(event *httpclient.StreamEvent) {
    if c == nil || event == nil || len(event.Data) == 0 || event.IsBinaryAudioChunk() {
        return
    }
    switch c.format {
    case llm.APIFormatOpenAIChatCompletion, model.ChannelTypeDoubao, llm.APIFormatGeminiContents:
        c.ObserveJSON(event.Data)
    case llm.APIFormatOpenAIResponse:
        var envelope struct {
            Type     string `json:"type"`
            Response *struct {
                Usage json.RawMessage `json:"usage"`
            } `json:"response"`
        }
        if json.Unmarshal(event.Data, &envelope) != nil || envelope.Response == nil {
            return
        }
        typ := event.Type
        if typ == "" || typ == "message" {
            typ = envelope.Type
        }
        switch typ {
        case "response.created", "response.in_progress", "response.completed", "response.incomplete", "response.failed", "response.cancelled":
            c.observeUsage(envelope.Response.Usage)
        }
    case llm.APIFormatAnthropicMessage:
        var envelope struct {
            Type    string          `json:"type"`
            Usage   json.RawMessage `json:"usage"`
            Message *struct {
                Usage json.RawMessage `json:"usage"`
            } `json:"message"`
        }
        if json.Unmarshal(event.Data, &envelope) != nil {
            return
        }
        typ := event.Type
        if typ == "" || typ == "message" {
            typ = envelope.Type
        }
        switch typ {
        case "message_start":
            if envelope.Message != nil {
                c.observeUsage(envelope.Message.Usage)
            }
        case "message_delta":
            c.observeUsage(envelope.Usage)
        }
    }
}

// 指针字段区分缺失或 null 与显式零值. 仅投影白名单字段, 不保留未知用量字段,
// 包括供应商费用和任意内容. 所有计数均为累计快照.
type rawUsageObject struct {
    PromptTokens     *int64 `json:"prompt_tokens"`
    CompletionTokens *int64 `json:"completion_tokens"`
    TotalTokens      *int64 `json:"total_tokens"`
    InputTokens      *int64 `json:"input_tokens"`
    OutputTokens     *int64 `json:"output_tokens"`
    CachedTokens     *int64 `json:"cached_tokens"`
    ReasoningTokens  *int64 `json:"reasoning_tokens"`
    PromptDetails    *struct {
        Audio  *int64 `json:"audio_tokens"`
        Cached *int64 `json:"cached_tokens"`
        Write  *int64 `json:"write_cached_tokens"`
    } `json:"prompt_tokens_details"`
    CompletionDetails *struct {
        Audio     *int64 `json:"audio_tokens"`
        Reasoning *int64 `json:"reasoning_tokens"`
        Accepted  *int64 `json:"accepted_prediction_tokens"`
        Rejected  *int64 `json:"rejected_prediction_tokens"`
    } `json:"completion_tokens_details"`
    InputDetails *struct {
        Cached *int64 `json:"cached_tokens"`
        Write  *int64 `json:"cache_write_tokens"`
    } `json:"input_tokens_details"`
    OutputDetails *struct {
        Reasoning *int64 `json:"reasoning_tokens"`
    } `json:"output_tokens_details"`
    CacheCreationInput *int64 `json:"cache_creation_input_tokens"`
    CacheReadInput     *int64 `json:"cache_read_input_tokens"`
    CacheCreation      *struct {
        FiveMin *int64 `json:"ephemeral_5m_input_tokens"`
        OneHour *int64 `json:"ephemeral_1h_input_tokens"`
    } `json:"cache_creation"`
    GeminiPrompt            *int64                   `json:"promptTokenCount"`
    GeminiToolPrompt        *int64                   `json:"toolUsePromptTokenCount"`
    GeminiCompletion        *int64                   `json:"candidatesTokenCount"`
    GeminiTotal             *int64                   `json:"totalTokenCount"`
    GeminiCached            *int64                   `json:"cachedContentTokenCount"`
    GeminiThoughts          *int64                   `json:"thoughtsTokenCount"`
    GeminiPromptDetails     *[]rawUsageModalityCount `json:"promptTokensDetails"`
    GeminiCompletionDetails *[]rawUsageModalityCount `json:"candidatesTokensDetails"`
}

type rawUsageModalityCount struct {
    Modality string `json:"modality"`
    Tokens   int64  `json:"tokenCount"`
}

func (c *RawUsageCapture) observeUsage(body []byte) {
    var u *rawUsageObject
    if len(body) == 0 || json.Unmarshal(body, &u) != nil || u == nil {
        return
    }
    c.mu.Lock()
    defer c.mu.Unlock()
    c.seen = true
    set := func(field int, value *int64) {
        if value != nil {
            c.counts[field] = rawUsageCounter{value: *value, present: true}
        }
    }
    switch c.format {
    case llm.APIFormatOpenAIChatCompletion, model.ChannelTypeDoubao:
        set(rawUsagePrompt, u.PromptTokens)
        set(rawUsageCompletion, u.CompletionTokens)
        set(rawUsageTotal, u.TotalTokens)
        set(rawUsageFallbackCached, u.CachedTokens)
        set(rawUsageFallbackReasoning, u.ReasoningTokens)
        if d := u.PromptDetails; d != nil {
            set(rawUsageCached, d.Cached)
            set(rawUsageWriteCached, d.Write)
            set(rawUsagePromptAudio, d.Audio)
        }
        if d := u.CompletionDetails; d != nil {
            set(rawUsageCompletionAudio, d.Audio)
            set(rawUsageReasoning, d.Reasoning)
            set(rawUsageAcceptedPrediction, d.Accepted)
            set(rawUsageRejectedPrediction, d.Rejected)
        }
    case llm.APIFormatOpenAIResponse:
        set(rawUsagePrompt, u.InputTokens)
        set(rawUsageCompletion, u.OutputTokens)
        set(rawUsageTotal, u.TotalTokens)
        if d := u.InputDetails; d != nil {
            set(rawUsageCached, d.Cached)
            set(rawUsageWriteCached, d.Write)
        }
        if d := u.OutputDetails; d != nil {
            set(rawUsageReasoning, d.Reasoning)
        }
    case llm.APIFormatAnthropicMessage:
        set(rawUsagePrompt, u.InputTokens)
        set(rawUsageCompletion, u.OutputTokens)
        set(rawUsageCached, u.CacheReadInput)
        set(rawUsageWriteCached, u.CacheCreationInput)
        set(rawUsageFallbackCached, u.CachedTokens)
        if d := u.CacheCreation; d != nil {
            set(rawUsageWrite5Min, d.FiveMin)
            set(rawUsageWrite1Hour, d.OneHour)
        }
    case llm.APIFormatGeminiContents:
        set(rawUsagePrompt, u.GeminiPrompt)
        set(rawUsageToolPrompt, u.GeminiToolPrompt)
        set(rawUsageCompletion, u.GeminiCompletion)
        set(rawUsageTotal, u.GeminiTotal)
        set(rawUsageCached, u.GeminiCached)
        set(rawUsageThoughts, u.GeminiThoughts)
        if u.GeminiPromptDetails != nil {
            c.promptModalities = rawUsageModalities(*u.GeminiPromptDetails)
        }
        if u.GeminiCompletionDetails != nil {
            c.completionModalities = rawUsageModalities(*u.GeminiCompletionDetails)
        }
    }
}

func rawUsageModalities(details []rawUsageModalityCount) []llm.ModalityTokenCount {
    if len(details) == 0 {
        return nil
    }
    result := make([]llm.ModalityTokenCount, len(details))
    for i, d := range details {
        result[i] = llm.ModalityTokenCount{Modality: d.Modality, TokenCount: d.Tokens}
    }
    return result
}

// Usage 返回独立快照, 未观察到非 null 用量对象时返回 nil.
// 空用量或显式零值不同于用量不可用. 此结果不代表成功或完成;
// 本地隐私处理失败或客户端取消时仍可保存已观察计数. 不按文本估算用量.
func (c *RawUsageCapture) Usage() *llm.Usage {
    if c == nil {
        return nil
    }
    c.mu.Lock()
    defer c.mu.Unlock()
    if !c.seen {
        return nil
    }
    v := func(field int) int64 { return c.counts[field].value }
    u := &llm.Usage{
        PromptTokens: v(rawUsagePrompt), CompletionTokens: v(rawUsageCompletion), TotalTokens: v(rawUsageTotal),
    }
    cached, write := v(rawUsageCached), v(rawUsageWriteCached)
    switch c.format {
    case llm.APIFormatOpenAIChatCompletion, model.ChannelTypeDoubao:
        if !c.counts[rawUsageCached].present && v(rawUsageFallbackCached) > 0 {
            cached = v(rawUsageFallbackCached)
        }
        if cached != 0 || write != 0 || v(rawUsagePromptAudio) != 0 {
            u.PromptTokensDetails = &llm.PromptTokensDetails{
                CachedTokens: cached, WriteCachedTokens: write, AudioTokens: v(rawUsagePromptAudio),
            }
        }
        reasoning := v(rawUsageReasoning)
        if !c.counts[rawUsageReasoning].present {
            reasoning = v(rawUsageFallbackReasoning)
        }
        d := llm.CompletionTokensDetails{
            ReasoningTokens: reasoning, AudioTokens: v(rawUsageCompletionAudio),
            AcceptedPredictionTokens: v(rawUsageAcceptedPrediction), RejectedPredictionTokens: v(rawUsageRejectedPrediction),
        }
        if d != (llm.CompletionTokensDetails{}) {
            u.CompletionTokensDetails = &d
        }
    case llm.APIFormatOpenAIResponse:
        u.PromptTokensDetails = &llm.PromptTokensDetails{CachedTokens: cached, WriteCachedTokens: write}
        u.CompletionTokensDetails = &llm.CompletionTokensDetails{ReasoningTokens: v(rawUsageReasoning)}
    case llm.APIFormatAnthropicMessage:
        // 与固定依赖版本的默认 Anthropic 平台一致. 此观察器仅识别协议格式,
        // 不像平台适配器那样识别平台, 不得根据 URL 猜测 Moonshot.
        if !c.counts[rawUsageCached].present && v(rawUsageFallbackCached) > 0 && write == 0 {
            cached = v(rawUsageFallbackCached)
        }
        u.PromptTokens += cached + write
        u.TotalTokens = u.PromptTokens + u.CompletionTokens
        u.CompletionTokensDetails = &llm.CompletionTokensDetails{}
        if cached > 0 || write > 0 || v(rawUsageWrite5Min) > 0 || v(rawUsageWrite1Hour) > 0 {
            u.PromptTokensDetails = &llm.PromptTokensDetails{
                CachedTokens: cached, WriteCachedTokens: write,
                WriteCached5MinTokens: v(rawUsageWrite5Min), WriteCached1HourTokens: v(rawUsageWrite1Hour),
            }
        }
    case llm.APIFormatGeminiContents:
        u.PromptTokens += v(rawUsageToolPrompt)
        u.CompletionTokens += v(rawUsageThoughts)
        if cached > 0 {
            u.PromptTokensDetails = &llm.PromptTokensDetails{CachedTokens: cached}
        }
        if v(rawUsageThoughts) > 0 {
            u.CompletionTokensDetails = &llm.CompletionTokensDetails{ReasoningTokens: v(rawUsageThoughts)}
        }
        u.PromptModalityTokenDetails = append([]llm.ModalityTokenCount(nil), c.promptModalities...)
        u.CompletionModalityTokenDetails = append([]llm.ModalityTokenCount(nil), c.completionModalities...)
    }
    return u
}

// Wrap 在 Next 消费上游事件时观察一次, 而非调用 Current 时.
// 保留原始指针, 字节, 顺序, 错误, Close 语义及可选传输能力.
// 不预读, 不排空, 不合成事件, Close 时不清除已观察用量.
func (c *RawUsageCapture) Wrap(stream streams.Stream[*httpclient.StreamEvent]) streams.Stream[*httpclient.StreamEvent] {
    if c == nil || stream == nil {
        return stream
    }
    wrapped := &rawUsageStream{Stream: stream, capture: c}
    return httpclient.WithResponseHeaders(wrapped, httpclient.GetResponseHeaders(stream))
}

type rawUsageStream struct {
    streams.Stream[*httpclient.StreamEvent]
    capture *RawUsageCapture
    current *httpclient.StreamEvent
}

func (s *rawUsageStream) Next() bool {
    if !s.Stream.Next() {
        return false
    }
    s.current = s.Stream.Current()
    s.capture.ObserveEvent(s.current)
    return true
}

func (s *rawUsageStream) Current() *httpclient.StreamEvent { return s.current }

func (s *rawUsageStream) AcknowledgeDelivery() {
    if observer, ok := s.Stream.(streams.DeliveryAcknowledger); ok {
        observer.AcknowledgeDelivery()
    }
}

func (s *rawUsageStream) DeliveryAcknowledged() bool {
    observer, ok := s.Stream.(streams.DeliveryAcknowledger)
    return ok && observer.DeliveryAcknowledged()
}

func (s *rawUsageStream) WaitForFinalization() {
    if waiter, ok := s.Stream.(streams.FinalizationWaiter); ok {
        waiter.WaitForFinalization()
    }
}

func (s *rawUsageStream) Deliver(ctx context.Context, write func() (bool, error)) error {
    if boundary, ok := s.Stream.(streams.DeliveryBoundary); ok {
        return boundary.Deliver(ctx, write)
    }
    terminal, err := write()
    if err == nil && terminal {
        s.AcknowledgeDelivery()
    }
    return err
}

func (s *rawUsageStream) ExpectedStreamChoices() int {
    if choices, ok := s.Stream.(interface{ ExpectedStreamChoices() int }); ok {
        return choices.ExpectedStreamChoices()
    }
    return 0
}
