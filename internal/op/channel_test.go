package op

import (
	"context"
	"testing"

	"github.com/bestruirui/octopus/internal/model"
)

func TestChannelGetByNameUsesExactMemoryMatch(t *testing.T) {
	channelCache.Clear()
	t.Cleanup(channelCache.Clear)

	channelCache.Set(1, model.Channel{ID: 1, Name: "OpenAI"})

	channel, err := ChannelGetByName("OpenAI", context.Background())
	if err != nil {
		t.Fatalf("ChannelGetByName() error = %v", err)
	}
	if channel.ID != 1 {
		t.Fatalf("ChannelGetByName() ID = %d, want 1", channel.ID)
	}

	for _, name := range []string{"openai", " OpenAI", "OpenAI "} {
		if _, err := ChannelGetByName(name, context.Background()); err == nil {
			t.Fatalf("ChannelGetByName(%q) unexpectedly matched", name)
		}
	}
}
