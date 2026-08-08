package op

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
)

func TestAPIKeyDeleteRemovesStringIndex(t *testing.T) {
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "apikey-delete.db"), false); err != nil {
		t.Fatalf("init database: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	apiKeyCache.Clear()
	apiKeyIDMap.Clear()
	t.Cleanup(func() {
		apiKeyCache.Clear()
		apiKeyIDMap.Clear()
	})

	ctx := context.Background()
	key := model.APIKey{
		Name:    "delete test",
		APIKey:  "sk-octopus-delete-test",
		Enabled: true,
	}
	if err := APIKeyCreate(&key, ctx); err != nil {
		t.Fatalf("create API key: %v", err)
	}

	if _, ok := apiKeyIDMap.Get(key.APIKey); !ok {
		t.Fatalf("API key string index was not populated")
	}

	if err := APIKeyDelete(key.ID, ctx); err != nil {
		t.Fatalf("delete API key: %v", err)
	}

	if _, ok := apiKeyCache.Get(key.ID); ok {
		t.Fatalf("API key object remains in cache")
	}
	if _, ok := apiKeyIDMap.Get(key.APIKey); ok {
		t.Fatalf("API key string index remains in cache")
	}
}
