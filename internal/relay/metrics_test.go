package relay

import "testing"

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
