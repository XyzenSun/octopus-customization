package relay

import (
	"encoding/json"
	"testing"

	"github.com/looplj/axonhub/llm"
)

func TestRelayMetricsRequestContentAppliesNullDeletion(t *testing.T) {
	var request llm.Request
	if err := json.Unmarshal([]byte(`{"model":"test","temperature":0.7,"top_p":0.9,"stream":true}`), &request); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}
	metrics := RelayMetrics{
		InternalRequest: &request,
		ParamOverride:   `{"temperature":null,"top_p":null,"max_tokens":128}`,
	}

	var content map[string]any
	if err := json.Unmarshal(metrics.requestContent(), &content); err != nil {
		t.Fatalf("unmarshal request content: %v", err)
	}
	for _, key := range []string{"temperature", "top_p"} {
		if _, exists := content[key]; exists {
			t.Fatalf("%s should be absent from logged request after null override: %#v", key, content)
		}
	}
	if content["max_tokens"] != float64(128) {
		t.Fatalf("max_tokens = %#v, want 128", content["max_tokens"])
	}
	if content["model"] != "test" || content["stream"] != true {
		t.Fatalf("unrelated request fields changed: %#v", content)
	}
}

func TestRelayLogContentExceedsLimit(t *testing.T) {
	const mib int64 = 1024 * 1024

	tests := []struct {
		name     string
		size     int64
		limitMiB int
		exceeded bool
	}{
		{name: "unlimited", size: 100 * mib, limitMiB: -1, exceeded: false},
		{name: "zero limit empty content", size: 0, limitMiB: 0, exceeded: false},
		{name: "zero limit non-empty content", size: 1, limitMiB: 0, exceeded: true},
		{name: "below limit", size: 2*mib - 1, limitMiB: 2, exceeded: false},
		{name: "at limit", size: 2 * mib, limitMiB: 2, exceeded: false},
		{name: "above limit", size: 2*mib + 1, limitMiB: 2, exceeded: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := relayLogContentExceedsLimit(tt.size, tt.limitMiB); got != tt.exceeded {
				t.Fatalf("relayLogContentExceedsLimit(%d, %d) = %t, want %t", tt.size, tt.limitMiB, got, tt.exceeded)
			}
		})
	}
}
