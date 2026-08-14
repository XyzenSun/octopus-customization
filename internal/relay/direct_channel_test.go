package relay

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/bestruirui/octopus/internal/db"
	dbmodel "github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/bestruirui/octopus/internal/relay/balancer"
	"github.com/gin-gonic/gin"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/pipeline"
	"github.com/looplj/axonhub/llm/streams"
	"github.com/looplj/axonhub/llm/transformer"
)

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "octopus-direct-channel-test-")
	if err != nil {
		panic(err)
	}
	if err := db.InitDB("sqlite", filepath.Join(dir, "direct-channel.db"), false); err != nil {
		panic(err)
	}
	if err := op.InitCache(); err != nil {
		panic(err)
	}
	code := m.Run()
	_ = db.Close()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

func TestSplitDirectChannelModel(t *testing.T) {
	tests := []struct {
		requestModel string
		channelName  string
		modelName    string
		ok           bool
	}{
		{requestModel: "channel/model", channelName: "channel", modelName: "model", ok: true},
		{requestModel: "channel/model/variant", channelName: "channel", modelName: "model/variant", ok: true},
		{requestModel: "/model", channelName: "", modelName: "model", ok: true},
		{requestModel: "channel/", channelName: "channel", modelName: "", ok: true},
		{requestModel: "model", channelName: "model", modelName: "", ok: false},
	}

	for _, tt := range tests {
		t.Run(tt.requestModel, func(t *testing.T) {
			channelName, modelName, ok := splitDirectChannelModel(tt.requestModel)
			if channelName != tt.channelName || modelName != tt.modelName || ok != tt.ok {
				t.Fatalf("splitDirectChannelModel(%q) = (%q, %q, %t), want (%q, %q, %t)",
					tt.requestModel, channelName, modelName, ok, tt.channelName, tt.modelName, tt.ok)
			}
		})
	}
}

func TestValidDirectChannelBaseURL(t *testing.T) {
	tests := []struct {
		baseURL string
		valid   bool
	}{
		{baseURL: "https://example.com/v1", valid: true},
		{baseURL: "http://127.0.0.1:8080", valid: true},
		{baseURL: ""},
		{baseURL: "   "},
		{baseURL: "/relative"},
		{baseURL: "://invalid"},
		{baseURL: "ftp://example.com"},
	}
	for _, tt := range tests {
		t.Run(tt.baseURL, func(t *testing.T) {
			if got := validDirectChannelBaseURL(tt.baseURL); got != tt.valid {
				t.Fatalf("validDirectChannelBaseURL(%q) = %t, want %t", tt.baseURL, got, tt.valid)
			}
		})
	}
}

func TestChannelSupportsModelUsesConfiguredTrimOnly(t *testing.T) {
	if !channelSupportsModel(" model-a,model/b ", "custom-c", "model/b") {
		t.Fatal("configured model with slash was not matched")
	}
	if !channelSupportsModel("model-a", " custom-c ", "custom-c") {
		t.Fatal("custom model was not matched")
	}
	for _, target := range []string{"MODEL-A", " model-a", "model-a "} {
		if channelSupportsModel("model-a", "", target) {
			t.Fatalf("target %q unexpectedly matched", target)
		}
	}
}

func TestValidateDirectChannelModelAccess(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name            string
		supportedModels string
		wantStatus      int
		wantErr         bool
	}{
		{name: "unrestricted", wantStatus: http.StatusOK},
		{name: "restricted", supportedModels: "channel/model", wantStatus: http.StatusBadRequest, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Set("supported_models", tt.supportedModels)

			err := validateDirectChannelModelAccess(c)
			if (err != nil) != tt.wantErr {
				t.Fatalf("validateDirectChannelModelAccess() error = %v", err)
			}
			if recorder.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", recorder.Code, tt.wantStatus)
			}
		})
	}
}

func TestNewRelayRunRouteResolution(t *testing.T) {
	gin.SetMode(gin.TestMode)
	channel := dbmodel.Channel{
		Name:        "channel",
		Type:        llm.APIFormatOpenAIChatCompletion,
		Enabled:     true,
		BaseUrls:    []dbmodel.BaseUrl{{URL: "https://example.com/v1"}},
		Model:       "model/variant",
		CustomModel: "custom,model/other",
		Keys: []dbmodel.ChannelKey{{
			Enabled:    true,
			ChannelKey: "upstream-key",
		}},
	}
	if err := op.ChannelCreate(&channel, context.Background()); err != nil {
		t.Fatalf("ChannelCreate() error = %v", err)
	}
	t.Cleanup(func() {
		if err := op.ChannelDel(channel.ID, context.Background()); err != nil {
			t.Errorf("ChannelDel() error = %v", err)
		}
	})
	group := dbmodel.Group{
		Name: "channel/model/variant",
		Mode: dbmodel.GroupModeFailover,
		Items: []dbmodel.GroupItem{{
			ChannelID: channel.ID,
			ModelName: "group-target",
		}},
	}
	if err := op.GroupCreate(&group, context.Background()); err != nil {
		t.Fatalf("GroupCreate() error = %v", err)
	}
	t.Cleanup(func() {
		if err := op.GroupDel(group.ID, context.Background()); err != nil {
			t.Errorf("GroupDel() error = %v", err)
		}
	})
	models, err := op.GroupListModel(context.Background())
	if err != nil {
		t.Fatalf("GroupListModel() error = %v", err)
	}
	foundGroup := false
	for _, modelName := range models {
		if modelName == group.Name {
			foundGroup = true
		}
		if modelName == "channel/model/other" || modelName == "channel/custom" {
			t.Fatalf("GroupListModel() exposed direct channel model %q: %v", modelName, models)
		}
	}
	if !foundGroup {
		t.Fatalf("GroupListModel() = %v, missing group %q", models, group.Name)
	}

	tests := []struct {
		name            string
		requestModel    string
		supportedModels string
		wantMode        routeMode
		wantStatus      int
		wantErr         bool
		wantActualModel string
	}{
		{
			name:            "group containing slash has priority",
			requestModel:    "channel/model/variant",
			supportedModels: "channel/model/variant",
			wantMode:        routeModeGroup,
			wantStatus:      http.StatusOK,
			wantActualModel: "channel/model/variant",
		},
		{
			name:            "multiple slashes split on first",
			requestModel:    "channel/model/other",
			wantMode:        routeModeDirectChannel,
			wantStatus:      http.StatusOK,
			wantActualModel: "model/other",
		},
		{
			name:            "restricted key cannot use direct route",
			requestModel:    "channel/model/other",
			supportedModels: "channel/model/other",
			wantStatus:      http.StatusBadRequest,
			wantErr:         true,
		},
		{
			name:            "restricted key missing group without slash remains not found",
			requestModel:    "restricted-missing-model",
			supportedModels: "allowed-group",
			wantStatus:      http.StatusNotFound,
			wantErr:         true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			c, _ := gin.CreateTestContext(recorder)
			c.Request = request
			c.Set("supported_models", tt.supportedModels)

			inbound := routeTestInbound{request: &llm.Request{
				Model:       tt.requestModel,
				RequestType: llm.RequestTypeChat,
			}}
			run, err := newRelayRun(c, llm.APIFormatOpenAIChatCompletion, inbound)
			if (err != nil) != tt.wantErr {
				t.Fatalf("newRelayRun() error = %v", err)
			}
			if recorder.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", recorder.Code, tt.wantStatus, recorder.Body.String())
			}
			if tt.wantErr {
				return
			}
			if run.routeMode != tt.wantMode {
				t.Fatalf("routeMode = %d, want %d", run.routeMode, tt.wantMode)
			}
			if run.metrics.ActualModel != tt.wantActualModel {
				t.Fatalf("ActualModel = %q, want %q", run.metrics.ActualModel, tt.wantActualModel)
			}
		})
	}
}

type directErrorInbound struct{}

type routeTestInbound struct {
	request *llm.Request
}

func (in routeTestInbound) TransformRequest(context.Context, *httpclient.Request) (*llm.Request, error) {
	request := *in.request
	return &request, nil
}
func (routeTestInbound) TransformResponse(context.Context, *llm.Response) (*httpclient.Response, error) {
	return nil, errors.New("unexpected response transform")
}
func (routeTestInbound) TransformStream(context.Context, streams.Stream[*llm.Response]) (streams.Stream[*httpclient.StreamEvent], error) {
	return nil, errors.New("unexpected stream transform")
}
func (routeTestInbound) TransformError(context.Context, error) *httpclient.Error {
	return nil
}
func (routeTestInbound) AggregateStreamChunks(context.Context, []*httpclient.StreamEvent) ([]byte, llm.ResponseMeta, error) {
	return nil, llm.ResponseMeta{}, nil
}

func (directErrorInbound) TransformRequest(context.Context, *httpclient.Request) (*llm.Request, error) {
	return nil, errors.New("unexpected request transform")
}
func (directErrorInbound) TransformResponse(context.Context, *llm.Response) (*httpclient.Response, error) {
	return nil, errors.New("unexpected response transform")
}
func (directErrorInbound) TransformStream(context.Context, streams.Stream[*llm.Response]) (streams.Stream[*httpclient.StreamEvent], error) {
	return nil, errors.New("unexpected stream transform")
}
func (directErrorInbound) TransformError(_ context.Context, err error) *httpclient.Error {
	var responseErr *llm.ResponseError
	if !errors.As(err, &responseErr) {
		return &httpclient.Error{StatusCode: http.StatusInternalServerError}
	}
	return &httpclient.Error{
		StatusCode: responseErr.StatusCode,
		Headers:    http.Header{"Content-Type": []string{"application/problem+json"}},
		Body:       []byte(`{"error":{"message":"converted"}}`),
	}
}
func (directErrorInbound) AggregateStreamChunks(context.Context, []*httpclient.StreamEvent) ([]byte, llm.ResponseMeta, error) {
	return nil, llm.ResponseMeta{}, nil
}

func TestWriteDirectChannelUpstreamError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	attempt := &relayAttempt{relayRun: &relayRun{
		c:         c,
		routeMode: routeModeDirectChannel,
		metrics:   &RelayMetrics{},
	}}
	upstreamErr := pipeline.WrapUpstreamError(&llm.ResponseError{
		StatusCode: http.StatusTooManyRequests,
		Detail:     llm.ErrorDetail{Message: "rate limited", Type: "rate_limit_error"},
	})

	statusCode, written := attempt.writeDirectChannelUpstreamError(context.Background(), directErrorInbound{}, upstreamErr)
	if !written || statusCode != http.StatusTooManyRequests {
		t.Fatalf("writeDirectChannelUpstreamError() = (%d, %t)", statusCode, written)
	}
	if recorder.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d", recorder.Code)
	}
	if recorder.Header().Get("Content-Type") != "application/problem+json" {
		t.Fatalf("Content-Type = %q", recorder.Header().Get("Content-Type"))
	}
	if recorder.Body.String() != `{"error":{"message":"converted"}}` {
		t.Fatalf("body = %q", recorder.Body.String())
	}
	if string(attempt.metrics.InternalResponse) != recorder.Body.String() {
		t.Fatal("converted error body was not recorded in metrics")
	}
}

func TestWriteDirectChannelUpstreamErrorRejectsNetworkError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	attempt := &relayAttempt{relayRun: &relayRun{
		c:         c,
		routeMode: routeModeDirectChannel,
		metrics:   &RelayMetrics{},
	}}

	if statusCode, written := attempt.writeDirectChannelUpstreamError(
		context.Background(), directErrorInbound{}, pipeline.WrapUpstreamError(errors.New("dial failed")),
	); written || statusCode != 0 {
		t.Fatalf("network error unexpectedly written: status=%d written=%t", statusCode, written)
	}
	if recorder.Code != http.StatusOK || recorder.Body.Len() != 0 {
		t.Fatalf("unexpected response: status=%d body=%q", recorder.Code, recorder.Body.String())
	}
}

func TestNewDirectChannelRelayRunValidation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	channels := []dbmodel.Channel{
		{
			Name:        "disabled",
			Type:        llm.APIFormatOpenAIChatCompletion,
			Enabled:     false,
			Model:       "configured-model",
			CustomModel: "",
		},
		{
			Name:        "enabled",
			Type:        llm.APIFormatOpenAIChatCompletion,
			Enabled:     true,
			Model:       "configured-model",
			CustomModel: "",
		},
	}
	createdChannelIDs := make([]int, 0, len(channels))
	for i := range channels {
		if err := op.ChannelCreate(&channels[i], context.Background()); err != nil {
			t.Fatalf("ChannelCreate(%q) error = %v", channels[i].Name, err)
		}
		createdChannelIDs = append(createdChannelIDs, channels[i].ID)
		if channels[i].Name == "disabled" {
			if err := op.ChannelEnabled(channels[i].ID, false, context.Background()); err != nil {
				t.Fatalf("ChannelEnabled(%q) error = %v", channels[i].Name, err)
			}
		}
	}
	t.Cleanup(func() {
		for i := len(createdChannelIDs) - 1; i >= 0; i-- {
			if err := op.ChannelDel(createdChannelIDs[i], context.Background()); err != nil {
				t.Errorf("ChannelDel(%d) error = %v", createdChannelIDs[i], err)
			}
		}
	})

	tests := []struct {
		name        string
		channelName string
		modelName   string
		wantStatus  int
	}{
		{name: "empty channel name", channelName: "", modelName: "configured-model", wantStatus: http.StatusNotFound},
		{name: "channel missing", channelName: "missing", modelName: "configured-model", wantStatus: http.StatusNotFound},
		{name: "channel name case sensitive", channelName: "Enabled", modelName: "configured-model", wantStatus: http.StatusNotFound},
		{name: "disabled channel hides model", channelName: "disabled", modelName: "missing-model", wantStatus: http.StatusServiceUnavailable},
		{name: "model missing", channelName: "enabled", modelName: "missing-model", wantStatus: http.StatusNotFound},
		{name: "model name case sensitive", channelName: "enabled", modelName: "Configured-Model", wantStatus: http.StatusNotFound},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			requestModel := tt.channelName + "/" + tt.modelName
			_, err := newDirectChannelRelayRun(c, llm.APIFormatOpenAIChatCompletion, routeTestInbound{}, &llm.Request{Model: requestModel}, tt.channelName, tt.modelName, false)
			if err == nil {
				t.Fatal("newDirectChannelRelayRun() error = nil")
			}
			if recorder.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", recorder.Code, tt.wantStatus, recorder.Body.String())
			}
		})
	}
}

func TestPrepareDirectChannelAttemptUnavailable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	channels := []dbmodel.Channel{
		{
			Name:     "no-key",
			Type:     llm.APIFormatOpenAIChatCompletion,
			Enabled:  true,
			BaseUrls: []dbmodel.BaseUrl{{URL: "https://example.com/v1"}},
			Model:    "model",
		},
		{
			Name:    "no-base-url",
			Type:    llm.APIFormatOpenAIChatCompletion,
			Enabled: true,
			Model:   "model",
			Keys: []dbmodel.ChannelKey{{
				Enabled:    true,
				ChannelKey: "upstream-key",
			}},
		},
		{
			Name:     "invalid-proxy",
			Type:     llm.APIFormatOpenAIChatCompletion,
			Enabled:  true,
			Proxy:    true,
			BaseUrls: []dbmodel.BaseUrl{{URL: "https://example.com/v1"}},
			Model:    "model",
			Keys: []dbmodel.ChannelKey{{
				Enabled:    true,
				ChannelKey: "upstream-key",
			}},
		},
	}
	createdChannelIDs := make([]int, 0, len(channels))
	for i := range channels {
		if err := op.ChannelCreate(&channels[i], context.Background()); err != nil {
			t.Fatalf("ChannelCreate(%q) error = %v", channels[i].Name, err)
		}
		createdChannelIDs = append(createdChannelIDs, channels[i].ID)
	}
	t.Cleanup(func() {
		for i := len(createdChannelIDs) - 1; i >= 0; i-- {
			if err := op.ChannelDel(createdChannelIDs[i], context.Background()); err != nil {
				t.Errorf("ChannelDel(%d) error = %v", createdChannelIDs[i], err)
			}
		}
	})

	for _, channel := range channels {
		t.Run(channel.Name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			run, err := newDirectChannelRelayRun(c, llm.APIFormatOpenAIChatCompletion, routeTestInbound{}, &llm.Request{Model: channel.Name + "/model", RequestType: llm.RequestTypeChat}, channel.Name, "model", false)
			if err != nil {
				t.Fatalf("newDirectChannelRelayRun() error = %v", err)
			}
			run.run()
			if recorder.Code != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, want 503; body=%s", recorder.Code, recorder.Body.String())
			}
			attempts := run.iter.Attempts()
			if len(attempts) != 1 || attempts[0].Status != dbmodel.AttemptSkipped {
				t.Fatalf("attempts = %+v", attempts)
			}
		})
	}
}

func TestPrepareDirectChannelAttemptCircuitBreak(t *testing.T) {
	gin.SetMode(gin.TestMode)
	balancer.ResetAll()
	t.Cleanup(balancer.ResetAll)

	channel := dbmodel.Channel{
		Name:     "circuit",
		Type:     llm.APIFormatOpenAIChatCompletion,
		Enabled:  true,
		BaseUrls: []dbmodel.BaseUrl{{URL: "https://example.com/v1"}},
		Model:    "model",
		Keys: []dbmodel.ChannelKey{{
			Enabled:    true,
			ChannelKey: "upstream-key",
		}},
	}
	if err := op.ChannelCreate(&channel, context.Background()); err != nil {
		t.Fatalf("ChannelCreate() error = %v", err)
	}
	t.Cleanup(func() {
		if err := op.ChannelDel(channel.ID, context.Background()); err != nil {
			t.Errorf("ChannelDel() error = %v", err)
		}
	})
	key := channel.GetChannelKey(true)
	for range 5 {
		balancer.RecordFailure(channel.ID, key.ID, "model")
	}

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	run, err := newDirectChannelRelayRun(c, llm.APIFormatOpenAIChatCompletion, routeTestInbound{}, &llm.Request{Model: "circuit/model", RequestType: llm.RequestTypeChat}, "circuit", "model", false)
	if err != nil {
		t.Fatalf("newDirectChannelRelayRun() error = %v", err)
	}
	run.run()
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body=%s", recorder.Code, recorder.Body.String())
	}
	attempts := run.iter.Attempts()
	if len(attempts) != 1 || attempts[0].Status != dbmodel.AttemptCircuitBreak {
		t.Fatalf("attempts = %+v", attempts)
	}
}

func TestDirectChannelPipelineSuccess(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var requestBody struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
			t.Errorf("decode upstream request: %v", err)
		}
		if requestBody.Model != "actual/model" {
			t.Errorf("upstream model = %q, want actual/model", requestBody.Model)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"response","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`))
	}))
	defer upstream.Close()

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	rawRequest := &httpclient.Request{
		Method:      http.MethodPost,
		Headers:     http.Header{"Content-Type": []string{"application/json"}},
		ContentType: "application/json",
		Body:        []byte(`{"model":"channel/actual/model","messages":[{"role":"user","content":"hello"}]}`),
	}
	internalRequest := &llm.Request{
		Model:       "actual/model",
		RequestType: llm.RequestTypeChat,
		Messages: []llm.Message{{
			Role:    "user",
			Content: llm.MessageContent{Content: new("hello")},
		}},
		RawRequest: rawRequest,
	}
	inbound := newInbound(llm.APIFormatOpenAIChatCompletion)
	outbound, err := newOutbound(llm.APIFormatOpenAIChatCompletion, internalRequest, upstream.URL, "upstream-key")
	if err != nil {
		t.Fatalf("newOutbound() error = %v", err)
	}
	attempt := &relayAttempt{
		relayRun: &relayRun{
			c:               c,
			inboundType:     llm.APIFormatOpenAIChatCompletion,
			inAdapter:       inbound,
			internalRequest: internalRequest,
			metrics: &RelayMetrics{
				RequestModel: "channel/actual/model",
				ActualModel:  "actual/model",
			},
			routeMode: routeModeDirectChannel,
		},
		outAdapter: outbound,
		channel: &dbmodel.Channel{
			Type:     llm.APIFormatOpenAIChatCompletion,
			BaseUrls: []dbmodel.BaseUrl{{URL: upstream.URL}},
		},
		usedKey: dbmodel.ChannelKey{ChannelKey: "upstream-key"},
	}

	statusCode, err := attempt.forward()
	if err != nil {
		t.Fatalf("forward() error = %v", err)
	}
	if statusCode != http.StatusOK || recorder.Code != http.StatusOK {
		t.Fatalf("status = (%d, %d); body=%s", statusCode, recorder.Code, recorder.Body.String())
	}
	if calls.Load() != 1 {
		t.Fatalf("upstream calls = %d, want 1", calls.Load())
	}
	if len(attempt.metrics.InternalResponse) == 0 {
		t.Fatal("successful response was not recorded")
	}
}

func TestDirectChannelPipelineErrors(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name        string
		channelType llm.APIFormat
		status      int
		body        string
		assertBody  func(*testing.T, []byte)
	}{
		{
			name:        "same protocol passthrough 401",
			channelType: llm.APIFormatOpenAIChatCompletion,
			status:      http.StatusUnauthorized,
			body:        `{"error":{"message":"openai upstream error","type":"authentication_error"}}`,
			assertBody: func(t *testing.T, body []byte) {
				var envelope struct {
					Error llm.ErrorDetail `json:"error"`
				}
				if err := json.Unmarshal(body, &envelope); err != nil {
					t.Fatalf("decode OpenAI error: %v; body=%s", err, body)
				}
				if envelope.Error.Message != "openai upstream error" || envelope.Error.Type != "authentication_error" {
					t.Fatalf("unexpected OpenAI error: %+v", envelope.Error)
				}
			},
		},
		{
			name:        "same protocol passthrough 500",
			channelType: llm.APIFormatOpenAIChatCompletion,
			status:      http.StatusInternalServerError,
			body:        `{"error":{"message":"openai server error","type":"server_error"}}`,
			assertBody: func(t *testing.T, body []byte) {
				var envelope struct {
					Error llm.ErrorDetail `json:"error"`
				}
				if err := json.Unmarshal(body, &envelope); err != nil {
					t.Fatalf("decode OpenAI error: %v; body=%s", err, body)
				}
				if envelope.Error.Message != "openai server error" || envelope.Error.Type != "server_error" {
					t.Fatalf("unexpected OpenAI error: %+v", envelope.Error)
				}
			},
		},
		{
			name:        "cross protocol anthropic to openai 429",
			channelType: llm.APIFormatAnthropicMessage,
			status:      http.StatusTooManyRequests,
			body:        `{"type":"error","request_id":"req_test","error":{"type":"overloaded_error","message":"anthropic upstream error"}}`,
			assertBody: func(t *testing.T, body []byte) {
				var envelope struct {
					Error llm.ErrorDetail `json:"error"`
				}
				if err := json.Unmarshal(body, &envelope); err != nil {
					t.Fatalf("decode converted OpenAI error: %v; body=%s", err, body)
				}
				if envelope.Error.Message != "anthropic upstream error" || envelope.Error.Type != "api_error" || envelope.Error.RequestID != "req_test" {
					t.Fatalf("unexpected converted error: %+v", envelope.Error)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var calls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				var requestBody struct {
					Model string `json:"model"`
				}
				if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
					t.Errorf("decode upstream request: %v", err)
				}
				if requestBody.Model != "actual/model" {
					t.Errorf("upstream model = %q, want actual/model", requestBody.Model)
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer upstream.Close()

			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			c, _ := gin.CreateTestContext(recorder)
			c.Request = request

			rawRequest := &httpclient.Request{
				Method:      http.MethodPost,
				Headers:     http.Header{"Content-Type": []string{"application/json"}},
				ContentType: "application/json",
				Body:        []byte(`{"model":"channel/actual/model","messages":[{"role":"user","content":"hello"}]}`),
			}
			internalRequest := &llm.Request{
				Model:       "actual/model",
				RequestType: llm.RequestTypeChat,
				Messages: []llm.Message{{
					Role:    "user",
					Content: llm.MessageContent{Content: new("hello")},
				}},
				RawRequest: rawRequest,
			}
			inbound := newInbound(llm.APIFormatOpenAIChatCompletion)
			outbound, err := newOutbound(tt.channelType, internalRequest, upstream.URL, "upstream-key")
			if err != nil {
				t.Fatalf("newOutbound() error = %v", err)
			}
			attempt := &relayAttempt{
				relayRun: &relayRun{
					c:               c,
					inboundType:     llm.APIFormatOpenAIChatCompletion,
					inAdapter:       inbound,
					internalRequest: internalRequest,
					metrics: &RelayMetrics{
						RequestModel: "channel/actual/model",
						ActualModel:  "actual/model",
					},
					routeMode: routeModeDirectChannel,
				},
				outAdapter: outbound,
				channel: &dbmodel.Channel{
					Type:     tt.channelType,
					BaseUrls: []dbmodel.BaseUrl{{URL: upstream.URL}},
				},
				usedKey: dbmodel.ChannelKey{ChannelKey: "upstream-key"},
			}

			statusCode, err := attempt.forward()
			if err == nil {
				t.Fatal("forward() error = nil")
			}
			if statusCode != tt.status || recorder.Code != tt.status {
				t.Fatalf("status = (%d, %d), want %d; body=%s", statusCode, recorder.Code, tt.status, recorder.Body.String())
			}
			if calls.Load() != 1 {
				t.Fatalf("upstream calls = %d, want 1", calls.Load())
			}
			tt.assertBody(t, recorder.Body.Bytes())
			if string(attempt.metrics.InternalResponse) != recorder.Body.String() {
				t.Fatal("converted pipeline error was not recorded")
			}
		})
	}
}

func TestDirectChannelFailureStatus(t *testing.T) {
	if got := directChannelFailureStatus([]dbmodel.ChannelAttempt{{Status: dbmodel.AttemptFailed}}); got != http.StatusBadGateway {
		t.Fatalf("failed attempt status = %d", got)
	}
	if got := directChannelFailureStatus([]dbmodel.ChannelAttempt{{Status: dbmodel.AttemptSkipped}}); got != http.StatusServiceUnavailable {
		t.Fatalf("skipped attempt status = %d", got)
	}
	if got := directChannelFailureStatus([]dbmodel.ChannelAttempt{{Status: dbmodel.AttemptCircuitBreak}}); got != http.StatusServiceUnavailable {
		t.Fatalf("circuit attempt status = %d", got)
	}
}

var _ transformer.Inbound = directErrorInbound{}
