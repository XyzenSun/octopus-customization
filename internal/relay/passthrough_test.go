package relay

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	dbmodel "github.com/bestruirui/octopus/internal/model"
	"github.com/gin-contrib/sse"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/pipeline"
	"github.com/looplj/axonhub/llm/streams"
	"github.com/looplj/axonhub/llm/transformer"
)

type passthroughTestInbound struct{}

func (passthroughTestInbound) TransformRequest(context.Context, *httpclient.Request) (*llm.Request, error) {
	return nil, errors.New("unexpected request transform")
}
func (passthroughTestInbound) TransformResponse(context.Context, *llm.Response) (*httpclient.Response, error) {
	return nil, errors.New("unexpected response transform")
}
func (passthroughTestInbound) TransformStream(context.Context, streams.Stream[*llm.Response]) (streams.Stream[*httpclient.StreamEvent], error) {
	return nil, errors.New("unexpected stream transform")
}
func (passthroughTestInbound) TransformError(context.Context, error) *httpclient.Error {
	return nil
}
func (passthroughTestInbound) AggregateStreamChunks(context.Context, []*httpclient.StreamEvent) ([]byte, llm.ResponseMeta, error) {
	return nil, llm.ResponseMeta{}, nil
}

type passthroughTestOutbound struct {
	format      llm.APIFormat
	usage       *llm.Usage
	responseErr error
}

func (t passthroughTestOutbound) APIFormat() llm.APIFormat { return t.format }
func (passthroughTestOutbound) TransformRequest(context.Context, *llm.Request) (*httpclient.Request, error) {
	return nil, errors.New("unexpected request transform")
}
func (t passthroughTestOutbound) TransformResponse(context.Context, *httpclient.Response) (*llm.Response, error) {
	if t.responseErr != nil {
		return nil, t.responseErr
	}
	return &llm.Response{Usage: t.usage}, nil
}
func (passthroughTestOutbound) TransformStream(context.Context, *httpclient.Request, streams.Stream[*httpclient.StreamEvent]) (streams.Stream[*llm.Response], error) {
	return nil, errors.New("unexpected stream transform")
}
func (passthroughTestOutbound) TransformError(context.Context, *httpclient.Error) *llm.ResponseError {
	return nil
}
func (passthroughTestOutbound) AggregateStreamChunks(context.Context, *httpclient.Request, []*httpclient.StreamEvent) ([]byte, llm.ResponseMeta, error) {
	return nil, llm.ResponseMeta{}, nil
}

func TestRelayAttemptCanPassthrough(t *testing.T) {
	requestBody := []byte(`{"model":"model-a","messages":[{"role":"user","content":"hello"}]}`)
	base := &relayAttempt{
		relayRun: &relayRun{
			inboundType:        llm.APIFormatOpenAIChatCompletion,
			passthroughEnabled: true,
			internalRequest: &llm.Request{Model: "model-a", RawRequest: &httpclient.Request{
				Headers: http.Header{"Content-Type": []string{"application/json"}},
				Body:    requestBody,
			}},
			metrics: &RelayMetrics{RequestModel: "model-a"},
		},
		channel: &dbmodel.Channel{
			Type:     llm.APIFormatOpenAIChatCompletion,
			BaseUrls: []dbmodel.BaseUrl{{URL: "https://example.com/v1"}},
		},
	}

	if !base.canPassthrough() {
		t.Fatal("matching protocol without body modification should passthrough")
	}

	disabled := *base
	disabledRun := *base.relayRun
	disabledRun.passthroughEnabled = false
	disabled.relayRun = &disabledRun
	if disabled.canPassthrough() {
		t.Fatal("disabled global setting must use transformed path")
	}

	mapped := *base
	mappedRequest := *base.internalRequest
	mappedRequest.Model = "model-b"
	mapped.relayRun = &relayRun{
		inboundType:        base.inboundType,
		internalRequest:    &mappedRequest,
		metrics:            base.metrics,
		passthroughEnabled: true,
	}
	if !mapped.canPassthrough() {
		t.Fatal("same-protocol model mapping should rewrite only model and passthrough")
	}

	override := `{"temperature":0}`
	withOverride := *base
	channelWithOverride := *base.channel
	channelWithOverride.ParamOverride = &override
	withOverride.channel = &channelWithOverride
	if withOverride.canPassthrough() {
		t.Fatal("param override must use transformed path")
	}

	withHeader := *base
	channelWithHeader := *base.channel
	channelWithHeader.CustomHeader = []dbmodel.CustomHeader{{HeaderKey: "X-Custom", HeaderValue: customHeaderValue("value")}}
	withHeader.channel = &channelWithHeader
	if withHeader.canPassthrough() {
		t.Fatal("channel custom header must use transformed path")
	}

	groupOnly := *base
	groupOnly.relayRun = &relayRun{
		inboundType:        base.inboundType,
		internalRequest:    base.internalRequest,
		metrics:            base.metrics,
		routeMode:          routeModeGroup,
		group:              dbmodel.Group{},
		passthroughEnabled: true,
	}
	if !groupOnly.canPassthrough() {
		t.Fatal("group route without group or channel request options should passthrough")
	}

	groupOverride := `{"temperature":0}`
	withGroupOverride := *base
	withGroupOverride.relayRun = &relayRun{
		inboundType:        base.inboundType,
		internalRequest:    base.internalRequest,
		metrics:            base.metrics,
		routeMode:          routeModeGroup,
		group:              dbmodel.Group{ParamOverride: &groupOverride},
		passthroughEnabled: true,
	}
	if withGroupOverride.canPassthrough() {
		t.Fatal("group param override must use transformed path")
	}

	withGroupHeader := *base
	withGroupHeader.relayRun = &relayRun{
		inboundType:        base.inboundType,
		internalRequest:    base.internalRequest,
		metrics:            base.metrics,
		routeMode:          routeModeGroup,
		passthroughEnabled: true,
		group: dbmodel.Group{CustomHeader: []dbmodel.CustomHeader{
			{HeaderKey: "X-Group", HeaderValue: customHeaderValue("value")},
		}},
	}
	if withGroupHeader.canPassthrough() {
		t.Fatal("group custom header must use transformed path")
	}
}

func TestPassthroughTransformerPreservesRequest(t *testing.T) {
	body := []byte("{\n  \"model\": \"model-a\",\n  \"extra\": {\"preserve\": true}\n}")
	raw := &httpclient.Request{
		Method: http.MethodPost,
		Headers: http.Header{
			"Content-Type":  []string{"application/json"},
			"Authorization": []string{"Bearer octopus-client-key"},
			"X-Custom":      []string{"keep"},
			"Connection":    []string{"keep-alive"},
		},
		Query: map[string][]string{"api-version": {"2026-01-01"}},
		Body:  body,
	}
	transformer := &passthroughTransformer{
		format:       llm.APIFormatOpenAIChatCompletion,
		baseURL:      "https://example.com/v1",
		apiKey:       "upstream-key",
		rawRequest:   raw,
		requestModel: "model-a",
	}

	got, err := transformer.TransformRequest(context.Background(), &llm.Request{RequestType: llm.RequestTypeChat})
	if err != nil {
		t.Fatalf("TransformRequest() error = %v", err)
	}
	if string(got.Body) != string(body) {
		t.Fatalf("body changed:\n got %q\nwant %q", got.Body, body)
	}
	if got.URL != "https://example.com/v1/chat/completions" {
		t.Fatalf("URL = %q", got.URL)
	}

	mapped, err := transformer.TransformRequest(context.Background(), &llm.Request{Model: "model-b", RequestType: llm.RequestTypeChat})
	if err != nil {
		t.Fatalf("TransformRequest(mapped model) error = %v", err)
	}
	if string(mapped.Body) != "{\n  \"model\": \"model-b\",\n  \"extra\": {\"preserve\": true}\n}" {
		t.Fatalf("mapped body changed unexpectedly:\n%s", mapped.Body)
	}

	anthropicURL, anthropicAuth, err := passthroughEndpoint(llm.APIFormatAnthropicMessage, "https://api.anthropic.com/v1")
	if err != nil {
		t.Fatalf("anthropic passthroughEndpoint() error = %v", err)
	}
	if anthropicURL != "https://api.anthropic.com/v1/messages" || anthropicAuth.Type != httpclient.AuthTypeAPIKey || anthropicAuth.HeaderKey != "X-API-Key" {
		t.Fatalf("unexpected anthropic endpoint/auth: %q %+v", anthropicURL, anthropicAuth)
	}
	if got.Headers.Get("Authorization") != "" {
		t.Fatal("client authorization header leaked to upstream request")
	}
	if got.Headers.Get("Connection") != "" {
		t.Fatal("hop-by-hop header must not be forwarded")
	}
	if got.Headers.Get("X-Custom") != "keep" {
		t.Fatal("custom client header was not preserved")
	}
	if got.Query.Get("api-version") != "2026-01-01" {
		t.Fatal("query parameter was not preserved")
	}
	if got.Auth == nil || got.Auth.APIKey != "upstream-key" || got.Auth.Type != httpclient.AuthTypeBearer {
		t.Fatalf("unexpected auth: %+v", got.Auth)
	}
}

func TestPassthroughTransformerPreservesResponseAndStream(t *testing.T) {
	usage := &llm.Usage{PromptTokens: 3, CompletionTokens: 5, TotalTokens: 8}
	transformer := &passthroughTransformer{
		format:   llm.APIFormatOpenAIChatCompletion,
		inbound:  passthroughTestInbound{},
		outbound: passthroughTestOutbound{format: llm.APIFormatOpenAIChatCompletion, usage: usage},
	}
	body := []byte("{\n  \"id\": \"raw-response\",\n  \"custom\": true,\n  \"usage\": {\"prompt_tokens\": 3, \"completion_tokens\": 5, \"total_tokens\": 8}\n}")
	headers := http.Header{"Content-Type": []string{"application/json"}, "X-Upstream": []string{"keep"}}

	parsed, err := transformer.TransformResponse(context.Background(), &httpclient.Response{StatusCode: http.StatusCreated, Headers: headers, Body: body})
	if err != nil {
		t.Fatalf("TransformResponse() error = %v", err)
	}
	if parsed.Usage == nil || parsed.Usage.PromptTokens != usage.PromptTokens || parsed.Usage.CompletionTokens != usage.CompletionTokens || parsed.Usage.TotalTokens != usage.TotalTokens {
		t.Fatal("usage parser result was not preserved")
	}
	final, err := transformer.TransformResponseInbound(context.Background(), parsed)
	if err != nil {
		t.Fatalf("TransformResponseInbound() error = %v", err)
	}
	if final.StatusCode != http.StatusCreated || string(final.Body) != string(body) || final.Headers.Get("X-Upstream") != "keep" {
		t.Fatalf("response was not preserved: %+v", final)
	}

	event := &httpclient.StreamEvent{LastEventID: "42", Type: "message", Data: []byte(`{"delta":"raw"}`)}
	llmStream, err := transformer.TransformStream(context.Background(), nil, streams.SliceStream([]*httpclient.StreamEvent{event}))
	if err != nil {
		t.Fatalf("TransformStream() error = %v", err)
	}
	clientStream, err := transformer.TransformStreamInbound(context.Background(), llmStream)
	if err != nil {
		t.Fatalf("TransformStreamInbound() error = %v", err)
	}
	if !clientStream.Next() || clientStream.Current() != event {
		t.Fatal("stream event was not passed through by identity")
	}

	done := &httpclient.StreamEvent{Data: []byte("[DONE]")}
	doneStream, err := transformer.TransformStream(context.Background(), nil, streams.SliceStream([]*httpclient.StreamEvent{done}))
	if err != nil {
		t.Fatalf("TransformStream([DONE]) error = %v", err)
	}
	if !doneStream.Next() || doneStream.Current().Object != "[DONE]" {
		t.Fatal("[DONE] must remain a terminal event for empty-response detection")
	}
}

type passthroughTimeoutExecutor struct{}

func (passthroughTimeoutExecutor) Do(context.Context, *httpclient.Request) (*httpclient.Response, error) {
	return nil, errors.New("unexpected non-stream request")
}
func (passthroughTimeoutExecutor) DoStream(ctx context.Context, request *httpclient.Request) (streams.Stream[*httpclient.StreamEvent], error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestPassthroughPreservesEmptyResponse(t *testing.T) {
	transformer := &passthroughTransformer{
		format:   llm.APIFormatOpenAIChatCompletion,
		inbound:  passthroughTestInbound{},
		outbound: passthroughTestOutbound{responseErr: errors.New("empty response")},
	}

	parsed, err := transformer.TransformResponse(context.Background(), &httpclient.Response{
		StatusCode: http.StatusNoContent,
		Headers:    make(http.Header),
		Body:       []byte{},
	})
	if err != nil {
		t.Fatalf("TransformResponse() error = %v", err)
	}
	final, err := transformer.TransformResponseInbound(context.Background(), parsed)
	if err != nil {
		t.Fatalf("TransformResponseInbound() error = %v", err)
	}
	if final.StatusCode != http.StatusNoContent || final.Body != nil && len(final.Body) != 0 {
		t.Fatalf("empty response was not preserved: %+v", final)
	}
}

func TestPassthroughDoesNotAcceptEmptyStreamEvent(t *testing.T) {
	transformer := &passthroughTransformer{format: llm.APIFormatOpenAIChatCompletion}
	stream, err := transformer.TransformStream(context.Background(), nil, streams.SliceStream([]*httpclient.StreamEvent{
		{Data: nil},
	}))
	if err != nil {
		t.Fatalf("TransformStream() error = %v", err)
	}
	if stream.Next() {
		t.Fatal("empty stream event must be filtered before empty-response detection")
	}
}

func TestWritePassthroughSSEPreservesEventID(t *testing.T) {
	var buffer bytes.Buffer
	err := sse.Encode(&buffer, sse.Event{Id: "42", Event: "delta", Data: []byte(`{"x":1}`)})
	if err != nil {
		t.Fatalf("sse.Encode() error = %v", err)
	}
	if !bytes.Contains(buffer.Bytes(), []byte("id:42\n")) {
		t.Fatalf("encoded SSE lost event id: %q", buffer.String())
	}
}

func TestPassthroughPipelinePreservesRawRequestAndResponse(t *testing.T) {
	requestBody := []byte("{\n  \"model\": \"model-a\",\n  \"custom_field\": true\n}")
	responseBody := []byte("{\n  \"id\": \"upstream-id\",\n  \"custom_response\": true\n}")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read upstream request: %v", err)
			return
		}
		if !bytes.Equal(body, requestBody) {
			t.Errorf("upstream request body changed:\n got %q\nwant %q", body, requestBody)
		}
		if r.Header.Get("Authorization") != "Bearer upstream-key" {
			t.Errorf("unexpected upstream authorization: %q", r.Header.Get("Authorization"))
		}
		if r.Header.Get("X-Custom") != "keep" || r.URL.Query().Get("api-version") != "2026-01-01" {
			t.Errorf("request metadata was not preserved: headers=%v query=%v", r.Header, r.URL.Query())
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Upstream", "keep")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write(responseBody)
	}))
	defer upstream.Close()

	raw := &httpclient.Request{
		Method: http.MethodPost,
		Headers: http.Header{
			"Content-Type":  []string{"application/json"},
			"Authorization": []string{"Bearer octopus-key"},
			"X-Custom":      []string{"keep"},
		},
		Query: map[string][]string{"api-version": {"2026-01-01"}},
		Body:  requestBody,
	}
	request := &llm.Request{Model: "model-a", RequestType: llm.RequestTypeChat, RawRequest: raw}
	usage := &llm.Usage{PromptTokens: 2, CompletionTokens: 3, TotalTokens: 5}
	passthrough := &passthroughTransformer{
		format:     llm.APIFormatOpenAIChatCompletion,
		baseURL:    upstream.URL + "/v1",
		apiKey:     "upstream-key",
		rawRequest: raw,
		inbound:    passthroughTestInbound{},
		outbound:   passthroughTestOutbound{format: llm.APIFormatOpenAIChatCompletion, usage: usage},
	}

	result, err := pipeline.NewFactory(httpclient.NewHttpClientWithClient(upstream.Client())).
		Pipeline(&passthroughInbound{transformer: passthrough, request: request}, passthrough).
		Process(context.Background(), raw)
	if err != nil {
		t.Fatalf("Process() error = %v", err)
	}
	if result == nil || result.Response == nil {
		t.Fatal("missing passthrough response")
	}
	if result.Response.StatusCode != http.StatusCreated || !bytes.Equal(result.Response.Body, responseBody) {
		t.Fatalf("raw response changed: %+v", result.Response)
	}
	if result.Response.Headers.Get("X-Upstream") != "keep" {
		t.Fatal("upstream response header was not preserved")
	}
}

func TestPassthroughPipelineKeepsFirstTokenTimeout(t *testing.T) {
	stream := true
	raw := &httpclient.Request{Method: http.MethodPost, Headers: make(http.Header), Body: []byte(`{"model":"model-a","stream":true}`)}
	request := &llm.Request{Model: "model-a", RequestType: llm.RequestTypeChat, Stream: &stream, RawRequest: raw}
	passthrough := &passthroughTransformer{
		format:     llm.APIFormatOpenAIChatCompletion,
		baseURL:    "https://example.com/v1",
		apiKey:     "upstream-key",
		rawRequest: raw,
		inbound:    passthroughTestInbound{},
		outbound:   passthroughTestOutbound{format: llm.APIFormatOpenAIChatCompletion},
	}

	_, err := pipeline.NewFactory(passthroughTimeoutExecutor{}).
		Pipeline(
			&passthroughInbound{transformer: passthrough, request: request},
			passthrough,
			pipeline.WithResponseTimeouts(10*time.Millisecond, 0),
		).
		Process(context.Background(), raw)
	if !errors.Is(err, pipeline.ErrStreamFirstEventTimeout) {
		t.Fatalf("expected ErrStreamFirstEventTimeout, got %v", err)
	}
}

var _ transformer.Inbound = passthroughTestInbound{}
var _ transformer.Outbound = passthroughTestOutbound{}
