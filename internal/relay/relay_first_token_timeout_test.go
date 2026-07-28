package relay

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/pipeline"
	"github.com/looplj/axonhub/llm/streams"
)

type timeoutMockExecutor struct{}

func (timeoutMockExecutor) Do(ctx context.Context, request *httpclient.Request) (*httpclient.Response, error) {
	return nil, errors.New("unexpected non-stream request")
}

func (timeoutMockExecutor) DoStream(ctx context.Context, request *httpclient.Request) (streams.Stream[*httpclient.StreamEvent], error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

type timeoutMockInbound struct{}

func (timeoutMockInbound) TransformRequest(ctx context.Context, request *httpclient.Request) (*llm.Request, error) {
	stream := true
	return &llm.Request{Model: "mock-model", Stream: &stream, RawRequest: request}, nil
}

func (timeoutMockInbound) TransformResponse(ctx context.Context, response *llm.Response) (*httpclient.Response, error) {
	return nil, errors.New("unexpected non-stream response")
}

func (timeoutMockInbound) TransformStream(ctx context.Context, stream streams.Stream[*llm.Response]) (streams.Stream[*httpclient.StreamEvent], error) {
	return nil, errors.New("stream should time out before inbound transform")
}

func (timeoutMockInbound) TransformError(ctx context.Context, err error) *httpclient.Error {
	return &httpclient.Error{StatusCode: 500, Body: []byte(err.Error())}
}

func (timeoutMockInbound) AggregateStreamChunks(ctx context.Context, chunks []*httpclient.StreamEvent) ([]byte, llm.ResponseMeta, error) {
	return nil, llm.ResponseMeta{}, nil
}

type timeoutMockOutbound struct{}

func (timeoutMockOutbound) APIFormat() llm.APIFormat { return llm.APIFormatOpenAIChatCompletion }

func (timeoutMockOutbound) TransformRequest(ctx context.Context, request *llm.Request) (*httpclient.Request, error) {
	return &httpclient.Request{Method: "POST", URL: "https://example.invalid/mock", Body: []byte(`{}`)}, nil
}

func (timeoutMockOutbound) TransformResponse(ctx context.Context, response *httpclient.Response) (*llm.Response, error) {
	return nil, errors.New("unexpected non-stream response")
}

func (timeoutMockOutbound) TransformStream(ctx context.Context, req *httpclient.Request, stream streams.Stream[*httpclient.StreamEvent]) (streams.Stream[*llm.Response], error) {
	return newBlockingLLMStream(ctx), nil
}

func (timeoutMockOutbound) TransformError(ctx context.Context, err *httpclient.Error) *llm.ResponseError {
	return &llm.ResponseError{StatusCode: err.StatusCode, Detail: llm.ErrorDetail{Message: string(err.Body)}}
}

func (timeoutMockOutbound) AggregateStreamChunks(ctx context.Context, req *httpclient.Request, chunks []*httpclient.StreamEvent) ([]byte, llm.ResponseMeta, error) {
	return nil, llm.ResponseMeta{}, nil
}

type blockingLLMStream struct {
	ctx    context.Context
	closed chan struct{}
}

func newBlockingLLMStream(ctx context.Context) *blockingLLMStream {
	return &blockingLLMStream{ctx: ctx, closed: make(chan struct{})}
}

func (s *blockingLLMStream) Next() bool {
	select {
	case <-s.ctx.Done():
		return false
	case <-s.closed:
		return false
	}
}

func (s *blockingLLMStream) Current() *llm.Response { return nil }
func (s *blockingLLMStream) Err() error             { return s.ctx.Err() }
func (s *blockingLLMStream) Close() error {
	select {
	case <-s.closed:
	default:
		close(s.closed)
	}
	return nil
}

func TestPipelineStreamFirstTokenTimeoutMock(t *testing.T) {
	stream := true
	req := &httpclient.Request{Body: []byte(`{"model":"mock-model","stream":true}`)}

	_, err := pipeline.NewFactory(timeoutMockExecutor{}).
		Pipeline(
			&parsedRequestInbound{Inbound: timeoutMockInbound{}, request: &llm.Request{Model: "mock-model", Stream: &stream, RawRequest: req}},
			timeoutMockOutbound{},
			pipeline.WithResponseTimeouts(10*time.Millisecond, 0),
		).
		Process(context.Background(), req)
	if !errors.Is(err, pipeline.ErrStreamFirstEventTimeout) {
		t.Fatalf("expected ErrStreamFirstEventTimeout, got %v", err)
	}
}
