package relay

import (
	"encoding/json"
	"net/http"
	"testing"

	dbmodel "github.com/bestruirui/octopus/internal/model"
	"github.com/looplj/axonhub/llm/httpclient"
)

func TestApplyRequestOptionsMergesGroupAndChannel(t *testing.T) {
	groupOverride := `{"temperature":0.2,"top_p":0.8,"metadata":{"source":"group"}}`
	channelOverride := `{"temperature":0.7,"max_tokens":128}`
	attempt := &relayAttempt{
		relayRun: &relayRun{
			routeMode: routeModeGroup,
			group: dbmodel.Group{
				ParamOverride: &groupOverride,
				CustomHeader: []dbmodel.CustomHeader{
					{HeaderKey: "X-Group", HeaderValue: "group"},
					{HeaderKey: "X-Shared", HeaderValue: "group"},
				},
			},
			metrics: &RelayMetrics{},
		},
		channel: &dbmodel.Channel{
			ParamOverride: &channelOverride,
			CustomHeader: []dbmodel.CustomHeader{
				{HeaderKey: "x-shared", HeaderValue: "channel"},
				{HeaderKey: "X-Channel", HeaderValue: "channel"},
			},
		},
	}
	request := &httpclient.Request{
		Headers: http.Header{"Content-Type": []string{"application/json"}},
		Body:    []byte(`{"model":"test","temperature":1,"presence_penalty":0.1}`),
	}

	attempt.applyRequestOptions(request)

	var body map[string]any
	if err := json.Unmarshal(request.Body, &body); err != nil {
		t.Fatalf("unmarshal modified body: %v", err)
	}
	if body["temperature"] != 0.7 {
		t.Fatalf("temperature = %v, want channel override", body["temperature"])
	}
	if body["top_p"] != 0.8 || body["max_tokens"] != float64(128) || body["presence_penalty"] != 0.1 {
		t.Fatalf("merged body = %#v", body)
	}
	metadata, ok := body["metadata"].(map[string]any)
	if !ok || metadata["source"] != "group" {
		t.Fatalf("metadata = %#v", body["metadata"])
	}
	if request.Headers.Get("X-Group") != "group" {
		t.Fatalf("group header = %q", request.Headers.Get("X-Group"))
	}
	if request.Headers.Get("X-Shared") != "channel" {
		t.Fatalf("shared header = %q, want channel", request.Headers.Get("X-Shared"))
	}
	if request.Headers.Get("X-Channel") != "channel" {
		t.Fatalf("channel header = %q", request.Headers.Get("X-Channel"))
	}

	var mergedOverride map[string]any
	if err := json.Unmarshal([]byte(attempt.metrics.ParamOverride), &mergedOverride); err != nil {
		t.Fatalf("unmarshal metrics override: %v", err)
	}
	if mergedOverride["temperature"] != 0.7 || mergedOverride["top_p"] != 0.8 || mergedOverride["max_tokens"] != float64(128) {
		t.Fatalf("metrics override = %#v", mergedOverride)
	}
}

func TestApplyRequestOptionsNullRemovesParam(t *testing.T) {
	// null 删除参数，同时确认 0/false/""/[]/{} 这些"看起来像空"的值不会被误删。
	channelOverride := `{"temperature":null,"top_p":0,"stream":false,"user":"","stop":[],"metadata":{}}`
	attempt := &relayAttempt{
		relayRun: &relayRun{
			routeMode: routeModeDirectChannel,
			metrics:   &RelayMetrics{},
		},
		channel: &dbmodel.Channel{ParamOverride: &channelOverride},
	}
	request := &httpclient.Request{
		Headers: http.Header{"Content-Type": []string{"application/json"}},
		Body:    []byte(`{"model":"test","temperature":0.7,"max_tokens":100}`),
	}

	attempt.applyRequestOptions(request)

	var body map[string]any
	if err := json.Unmarshal(request.Body, &body); err != nil {
		t.Fatalf("unmarshal modified body: %v", err)
	}
	if _, exists := body["temperature"]; exists {
		t.Fatalf("temperature should be removed by null override, got %#v", body)
	}
	if body["max_tokens"] != float64(100) {
		t.Fatalf("max_tokens = %v, want untouched", body["max_tokens"])
	}
	for key, want := range map[string]any{"top_p": float64(0), "stream": false, "user": ""} {
		if got, exists := body[key]; !exists || got != want {
			t.Fatalf("%s = %#v (exists=%v), want %#v kept as a normal value", key, got, exists, want)
		}
	}
	if stop, ok := body["stop"].([]any); !ok || len(stop) != 0 {
		t.Fatalf("stop = %#v, want empty array kept", body["stop"])
	}
	if metadata, ok := body["metadata"].(map[string]any); !ok || len(metadata) != 0 {
		t.Fatalf("metadata = %#v, want empty object kept", body["metadata"])
	}
}

func TestApplyRequestOptionsChannelRestoresGroupNulledParam(t *testing.T) {
	// 两级叠加：channel 的具体值能"救回"被 group 置 null 的参数；反向则最终删除。
	groupOverride := `{"temperature":null,"top_p":0.8}`
	channelOverride := `{"temperature":0.5,"top_p":null}`
	attempt := &relayAttempt{
		relayRun: &relayRun{
			routeMode: routeModeGroup,
			group:     dbmodel.Group{ParamOverride: &groupOverride},
			metrics:   &RelayMetrics{},
		},
		channel: &dbmodel.Channel{ParamOverride: &channelOverride},
	}
	request := &httpclient.Request{
		Headers: http.Header{"Content-Type": []string{"application/json"}},
		Body:    []byte(`{"model":"test","temperature":0.7,"top_p":0.9}`),
	}

	attempt.applyRequestOptions(request)

	var body map[string]any
	if err := json.Unmarshal(request.Body, &body); err != nil {
		t.Fatalf("unmarshal modified body: %v", err)
	}
	if body["temperature"] != 0.5 {
		t.Fatalf("temperature = %#v, want channel value to win over group null", body["temperature"])
	}
	if _, exists := body["top_p"]; exists {
		t.Fatalf("top_p should be removed by channel null, got %#v", body)
	}
}

func TestApplyRequestOptionsKeepsSensitiveAuthenticationHeader(t *testing.T) {
	attempt := &relayAttempt{
		relayRun: &relayRun{
			routeMode: routeModeGroup,
			group: dbmodel.Group{CustomHeader: []dbmodel.CustomHeader{
				{HeaderKey: "Authorization", HeaderValue: "group-token"},
			}},
			metrics: &RelayMetrics{},
		},
		channel: &dbmodel.Channel{CustomHeader: []dbmodel.CustomHeader{
			{HeaderKey: "authorization", HeaderValue: "channel-token"},
		}},
	}
	request := &httpclient.Request{Headers: http.Header{
		"Authorization": []string{"Bearer upstream-key"},
	}}

	attempt.applyRequestOptions(request)

	if request.Headers.Get("Authorization") != "Bearer upstream-key" {
		t.Fatalf("authorization = %q", request.Headers.Get("Authorization"))
	}
}

func TestDirectChannelRequestOptionsIgnoreGroup(t *testing.T) {
	groupOverride := `{"temperature":0.2,"top_p":0.3}`
	channelOverride := `{"temperature":0.9,"max_tokens":64}`
	attempt := &relayAttempt{
		relayRun: &relayRun{
			routeMode: routeModeDirectChannel,
			group: dbmodel.Group{
				ParamOverride: &groupOverride,
				CustomHeader:  []dbmodel.CustomHeader{{HeaderKey: "X-Group", HeaderValue: "group"}},
			},
			metrics: &RelayMetrics{},
		},
		channel: &dbmodel.Channel{
			ParamOverride: &channelOverride,
			CustomHeader:  []dbmodel.CustomHeader{{HeaderKey: "X-Channel", HeaderValue: "channel"}},
		},
	}
	request := &httpclient.Request{
		Headers: http.Header{"Content-Type": []string{"application/json"}},
		Body:    []byte(`{"model":"test"}`),
	}

	attempt.applyRequestOptions(request)

	var body map[string]any
	if err := json.Unmarshal(request.Body, &body); err != nil {
		t.Fatalf("unmarshal modified body: %v", err)
	}
	if _, exists := body["top_p"]; exists {
		t.Fatalf("direct channel body unexpectedly contains group override: %#v", body)
	}
	if body["temperature"] != 0.9 || body["max_tokens"] != float64(64) {
		t.Fatalf("direct channel body = %#v", body)
	}
	if request.Headers.Get("X-Group") != "" || request.Headers.Get("X-Channel") != "channel" {
		t.Fatalf("direct channel headers = %#v", request.Headers)
	}
}
