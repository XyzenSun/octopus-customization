package relay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/streams"
	"github.com/looplj/axonhub/llm/transformer"
	"github.com/tidwall/sjson"
)

const passthroughMetadataKey = "octopus_passthrough_event"

// passthroughTransformer 只替换 pipeline 的协议转换阶段。
// HTTP 执行、首 token 超时、空响应检测、错误处理和流生命周期仍由现有 pipeline 负责。
type passthroughTransformer struct {
	format           llm.APIFormat
	baseURL          string
	apiKey           string
	rawRequest       *httpclient.Request
	inbound          transformer.Inbound
	outbound         transformer.Outbound
	requestModel     string
	responseCaptured bool
	responseStatus   int
	responseHeaders  http.Header
	responseBody     []byte
}

func (ra *relayAttempt) canPassthrough() bool {
	if ra == nil || ra.relayRun == nil || !ra.passthroughEnabled {
		return false
	}
	if ra.channel == nil || ra.internalRequest == nil || ra.internalRequest.RawRequest == nil || ra.metrics == nil {
		return false
	}
	if ra.inboundType != ra.channel.Type {
		return false
	}
	if ra.hasRequestOptions() {
		return false
	}
	if ra.metrics.RequestModel != ra.internalRequest.Model && !isPassthroughJSONRequest(ra.internalRequest.RawRequest) {
		return false
	}
	_, _, err := passthroughEndpoint(ra.inboundType, ra.channel.GetBaseUrl())
	return err == nil
}

func (ra *relayAttempt) hasRequestOptions() bool {
	hasParamOverride := func(raw *string) bool {
		return raw != nil && strings.TrimSpace(*raw) != ""
	}
	hasCustomHeader := func(headers []model.CustomHeader) bool {
		for _, header := range headers {
			if strings.TrimSpace(header.HeaderKey) != "" {
				return true
			}
		}
		return false
	}

	if ra.routeMode == routeModeGroup && (hasParamOverride(ra.group.ParamOverride) || hasCustomHeader(ra.group.CustomHeader)) {
		return true
	}
	return hasParamOverride(ra.channel.ParamOverride) || hasCustomHeader(ra.channel.CustomHeader)
}

func isPassthroughJSONRequest(request *httpclient.Request) bool {
	if request == nil {
		return false
	}
	contentType := request.ContentType
	if contentType == "" && request.Headers != nil {
		contentType = request.Headers.Get("Content-Type")
	}
	return strings.Contains(strings.ToLower(contentType), "application/json")
}

func newPassthroughTransformer(ra *relayAttempt) (*passthroughTransformer, error) {
	if ra == nil || ra.internalRequest == nil || ra.internalRequest.RawRequest == nil || ra.channel == nil {
		return nil, errors.New("missing passthrough request")
	}
	if _, _, err := passthroughEndpoint(ra.inboundType, ra.channel.GetBaseUrl()); err != nil {
		return nil, err
	}
	return &passthroughTransformer{
		format:       ra.inboundType,
		baseURL:      ra.channel.GetBaseUrl(),
		apiKey:       ra.usedKey.ChannelKey,
		rawRequest:   ra.internalRequest.RawRequest,
		requestModel: ra.metrics.RequestModel,
		inbound:      ra.inAdapter,
		outbound:     ra.outAdapter,
	}, nil
}

func (t *passthroughTransformer) APIFormat() llm.APIFormat {
	return t.format
}

func (t *passthroughTransformer) TransformRequest(ctx context.Context, request *llm.Request) (*httpclient.Request, error) {
	upstreamURL, auth, err := passthroughEndpoint(t.format, t.baseURL)
	if err != nil {
		return nil, err
	}
	auth.APIKey = t.apiKey

	headers := make(http.Header, len(t.rawRequest.Headers))
	for key, values := range t.rawRequest.Headers {
		if httpclient.IsSensitiveHeader(key) || !shouldForwardPassthroughHeader(key) {
			continue
		}
		headers[key] = append([]string(nil), values...)
	}
	query := make(map[string][]string, len(t.rawRequest.Query))
	for key, values := range t.rawRequest.Query {
		query[key] = append([]string(nil), values...)
	}
	applyPassthroughDefaults(t.format, headers)

	body := append([]byte(nil), t.rawRequest.Body...)
	if t.requestModel != "" && request.Model != "" && request.Model != t.requestModel {
		body, err = sjson.SetBytes(body, "model", request.Model)
		if err != nil {
			return nil, fmt.Errorf("failed to rewrite passthrough model: %w", err)
		}
	}

	return &httpclient.Request{
		Method:      t.rawRequest.Method,
		URL:         upstreamURL,
		Query:       query,
		Headers:     headers,
		ContentType: t.rawRequest.ContentType,
		Body:        body,
		Auth:        auth,
		RequestType: request.RequestType.String(),
		APIFormat:   t.format.String(),
		// 原始 query/header 已复制，阻止 pipeline 再次合并 query；敏感 header 仍由 AuthConfig 覆盖。
		SkipInboundQueryMerge: true,
	}, nil
}

func shouldForwardPassthroughHeader(key string) bool {
	switch http.CanonicalHeaderKey(key) {
	case "Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization", "Proxy-Connection",
		"Te", "Trailer", "Transfer-Encoding", "Upgrade", "Content-Length", "Host":
		return false
	default:
		return true
	}
}

func (t *passthroughTransformer) TransformResponse(ctx context.Context, response *httpclient.Response) (*llm.Response, error) {
	t.responseCaptured = true
	t.responseStatus = response.StatusCode
	t.responseHeaders = response.Headers.Clone()
	t.responseBody = append([]byte(nil), response.Body...)

	return &llm.Response{
		Object:    "passthrough",
		APIFormat: t.format,
		Usage:     passthroughUsage(t.format, response.Body),
		Choices: []llm.Choice{{
			Message: &llm.Message{Content: llm.MessageContent{Content: new("passthrough")}},
		}},
	}, nil
}

func passthroughUsage(format llm.APIFormat, body []byte) *llm.Usage {
	var envelope struct {
		Usage json.RawMessage `json:"usage"`
	}
	if json.Unmarshal(body, &envelope) != nil || len(envelope.Usage) == 0 || string(envelope.Usage) == "null" {
		return nil
	}

	switch format {
	case llm.APIFormatAnthropicMessage:
		var usage struct {
			InputTokens              int64 `json:"input_tokens"`
			OutputTokens             int64 `json:"output_tokens"`
			CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
			CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
			CacheCreation            struct {
				Ephemeral5mInputTokens int64 `json:"ephemeral_5m_input_tokens"`
				Ephemeral1hInputTokens int64 `json:"ephemeral_1h_input_tokens"`
			} `json:"cache_creation"`
		}
		if json.Unmarshal(envelope.Usage, &usage) != nil {
			return nil
		}
		promptTokens := usage.InputTokens + usage.CacheCreationInputTokens + usage.CacheReadInputTokens
		result := &llm.Usage{
			PromptTokens:     promptTokens,
			CompletionTokens: usage.OutputTokens,
			TotalTokens:      promptTokens + usage.OutputTokens,
		}
		if usage.CacheCreationInputTokens > 0 || usage.CacheReadInputTokens > 0 {
			result.PromptTokensDetails = &llm.PromptTokensDetails{
				CachedTokens:           usage.CacheReadInputTokens,
				WriteCachedTokens:      usage.CacheCreationInputTokens,
				WriteCached5MinTokens:  usage.CacheCreation.Ephemeral5mInputTokens,
				WriteCached1HourTokens: usage.CacheCreation.Ephemeral1hInputTokens,
			}
		}
		return result
	case llm.APIFormatOpenAIResponse:
		var usage struct {
			InputTokens       int64 `json:"input_tokens"`
			OutputTokens      int64 `json:"output_tokens"`
			TotalTokens       int64 `json:"total_tokens"`
			InputTokenDetails struct {
				CachedTokens int64 `json:"cached_tokens"`
			} `json:"input_tokens_details"`
			OutputTokenDetails struct {
				ReasoningTokens int64 `json:"reasoning_tokens"`
			} `json:"output_tokens_details"`
		}
		if json.Unmarshal(envelope.Usage, &usage) != nil {
			return nil
		}
		return &llm.Usage{
			PromptTokens:     usage.InputTokens,
			CompletionTokens: usage.OutputTokens,
			TotalTokens:      usage.TotalTokens,
			PromptTokensDetails: &llm.PromptTokensDetails{
				CachedTokens: usage.InputTokenDetails.CachedTokens,
			},
			CompletionTokensDetails: &llm.CompletionTokensDetails{
				ReasoningTokens: usage.OutputTokenDetails.ReasoningTokens,
			},
		}
	default:
		var usage llm.Usage
		if json.Unmarshal(envelope.Usage, &usage) != nil {
			return nil
		}
		return &usage
	}
}

func (t *passthroughTransformer) TransformStream(ctx context.Context, request *httpclient.Request, stream streams.Stream[*httpclient.StreamEvent]) (streams.Stream[*llm.Response], error) {
	stream = streams.Filter(stream, func(event *httpclient.StreamEvent) bool {
		return event != nil && len(event.Data) > 0
	})
	return streams.Map(stream, func(event *httpclient.StreamEvent) *llm.Response {
		object := "passthrough"
		var choices []llm.Choice
		switch {
		case event == nil || len(event.Data) == 0:
		case string(event.Data) == "[DONE]":
			object = "[DONE]"
		default:
			// 非空占位只用于通过 pipeline 的首事件/空响应检测，客户端收到的仍是 metadata 中的原始 SSE 事件。
			choices = []llm.Choice{{
				Delta: &llm.Message{Content: llm.MessageContent{Content: new("passthrough")}},
			}}
		}
		return &llm.Response{
			Object:              object,
			APIFormat:           t.format,
			Choices:             choices,
			TransformerMetadata: map[string]any{passthroughMetadataKey: event},
		}
	}), nil
}

func (t *passthroughTransformer) TransformError(ctx context.Context, err *httpclient.Error) *llm.ResponseError {
	return t.outbound.TransformError(ctx, err)
}

func (t *passthroughTransformer) AggregateStreamChunks(ctx context.Context, request *httpclient.Request, chunks []*httpclient.StreamEvent) ([]byte, llm.ResponseMeta, error) {
	return t.outbound.AggregateStreamChunks(ctx, request, chunks)
}

func (t *passthroughTransformer) TransformResponseInbound(ctx context.Context, response *llm.Response) (*httpclient.Response, error) {
	if t.responseCaptured {
		statusCode := t.responseStatus
		if statusCode == 0 {
			statusCode = http.StatusOK
		}
		return &httpclient.Response{
			StatusCode: statusCode,
			Headers:    t.responseHeaders.Clone(),
			Body:       append([]byte(nil), t.responseBody...),
		}, nil
	}
	return t.inbound.TransformResponse(ctx, response)
}

func (t *passthroughTransformer) TransformStreamInbound(ctx context.Context, stream streams.Stream[*llm.Response]) (streams.Stream[*httpclient.StreamEvent], error) {
	return streams.MapErr(stream, func(response *llm.Response) (*httpclient.StreamEvent, error) {
		if response == nil || response.TransformerMetadata == nil {
			return nil, errors.New("missing passthrough stream event")
		}
		event, ok := response.TransformerMetadata[passthroughMetadataKey].(*httpclient.StreamEvent)
		if !ok || event == nil {
			return nil, errors.New("invalid passthrough stream event")
		}
		return event, nil
	}), nil
}

func (t *passthroughTransformer) TransformErrorInbound(ctx context.Context, err error) *httpclient.Error {
	return t.inbound.TransformError(ctx, err)
}

func (t *passthroughTransformer) AggregateStreamChunksInbound(ctx context.Context, chunks []*httpclient.StreamEvent) ([]byte, llm.ResponseMeta, error) {
	return t.inbound.AggregateStreamChunks(ctx, chunks)
}

type passthroughInbound struct {
	transformer *passthroughTransformer
	request     *llm.Request
}

func (in *passthroughInbound) TransformRequest(ctx context.Context, request *httpclient.Request) (*llm.Request, error) {
	if in.request == nil {
		return nil, errors.New("missing parsed request")
	}
	in.request.RawRequest = request
	return in.request, nil
}

func (in *passthroughInbound) TransformResponse(ctx context.Context, response *llm.Response) (*httpclient.Response, error) {
	return in.transformer.TransformResponseInbound(ctx, response)
}

func (in *passthroughInbound) TransformStream(ctx context.Context, stream streams.Stream[*llm.Response]) (streams.Stream[*httpclient.StreamEvent], error) {
	return in.transformer.TransformStreamInbound(ctx, stream)
}

func (in *passthroughInbound) TransformError(ctx context.Context, err error) *httpclient.Error {
	return in.transformer.TransformErrorInbound(ctx, err)
}

func (in *passthroughInbound) AggregateStreamChunks(ctx context.Context, chunks []*httpclient.StreamEvent) ([]byte, llm.ResponseMeta, error) {
	return in.transformer.AggregateStreamChunksInbound(ctx, chunks)
}
