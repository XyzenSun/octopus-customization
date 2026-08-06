package relay

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/streams"
	"github.com/looplj/axonhub/llm/transformer"
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
	responseCaptured bool
	responseStatus   int
	responseHeaders  http.Header
	responseBody     []byte
}

func (ra *relayAttempt) canPassthrough() bool {
	if ra == nil || ra.channel == nil || ra.internalRequest == nil || ra.internalRequest.RawRequest == nil {
		return false
	}
	if ra.inboundType != ra.channel.Type || ra.metrics.RequestModel != ra.internalRequest.Model {
		return false
	}
	if ra.channel.ParamOverride != nil && strings.TrimSpace(*ra.channel.ParamOverride) != "" {
		return false
	}
	_, _, err := passthroughEndpoint(ra.inboundType, ra.channel.GetBaseUrl())
	return err == nil
}

func newPassthroughTransformer(ra *relayAttempt) (*passthroughTransformer, error) {
	if ra == nil || ra.internalRequest == nil || ra.internalRequest.RawRequest == nil || ra.channel == nil {
		return nil, errors.New("missing passthrough request")
	}
	if _, _, err := passthroughEndpoint(ra.inboundType, ra.channel.GetBaseUrl()); err != nil {
		return nil, err
	}
	return &passthroughTransformer{
		format:     ra.inboundType,
		baseURL:    ra.channel.GetBaseUrl(),
		apiKey:     ra.usedKey.ChannelKey,
		rawRequest: ra.internalRequest.RawRequest,
		inbound:    ra.inAdapter,
		outbound:   ra.outAdapter,
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

	return &httpclient.Request{
		Method:      t.rawRequest.Method,
		URL:         upstreamURL,
		Query:       query,
		Headers:     headers,
		ContentType: t.rawRequest.ContentType,
		Body:        append([]byte(nil), t.rawRequest.Body...),
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

	parsed, err := t.outbound.TransformResponse(ctx, response)
	if err != nil || parsed == nil {
		return &llm.Response{
			Object:    "passthrough",
			APIFormat: t.format,
			Choices: []llm.Choice{{
				Message: &llm.Message{Content: llm.MessageContent{Content: new("passthrough")}},
			}},
		}, nil
	}
	return parsed, nil
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
