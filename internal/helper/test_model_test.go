package helper

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/looplj/axonhub/llm"
)

// TestTestChannelModel_UnhappyPaths 覆盖基本校验路径,不需要真实上游。
func TestTestChannelModel_UnhappyPaths(t *testing.T) {
	ctx := context.Background()

	t.Run("nil channel", func(t *testing.T) {
		if _, err := TestChannelModel(ctx, nil, "model", 0, 0); err == nil {
			t.Fatal("expected error")
		}
	})
	t.Run("empty model", func(t *testing.T) {
		ch := &model.Channel{BaseUrls: []model.BaseUrl{{URL: "http://x", Delay: 1}}, Keys: []model.ChannelKey{{ChannelKey: "k"}}}
		if _, err := TestChannelModel(ctx, ch, "", 0, 0); err == nil {
			t.Fatal("expected error")
		}
	})
	t.Run("embedding not supported", func(t *testing.T) {
		ch := &model.Channel{
			Type:     llm.APIFormatOpenAIEmbedding,
			BaseUrls: []model.BaseUrl{{URL: "http://x", Delay: 1}},
			Keys:     []model.ChannelKey{{ChannelKey: "k"}},
		}
		if _, err := TestChannelModel(ctx, ch, "m", 0, 0); err == nil || !strings.Contains(err.Error(), "not support") {
			t.Fatalf("expected embedding not supported error, got %v", err)
		}
	})
	t.Run("key index out of range", func(t *testing.T) {
		ch := &model.Channel{
			Type:     llm.APIFormatOpenAIChatCompletion,
			BaseUrls: []model.BaseUrl{{URL: "http://x", Delay: 1}},
			Keys:     []model.ChannelKey{{ChannelKey: "k"}},
		}
		if _, err := TestChannelModel(ctx, ch, "m", 5, 0); err == nil {
			t.Fatal("expected error")
		}
	})
	t.Run("empty key value", func(t *testing.T) {
		ch := &model.Channel{
			Type:     llm.APIFormatOpenAIChatCompletion,
			BaseUrls: []model.BaseUrl{{URL: "http://x", Delay: 1}},
			Keys:     []model.ChannelKey{{ChannelKey: ""}},
		}
		if _, err := TestChannelModel(ctx, ch, "m", 0, 0); err == nil {
			t.Fatal("expected error")
		}
	})
	t.Run("no base url", func(t *testing.T) {
		ch := &model.Channel{
			Type: llm.APIFormatOpenAIChatCompletion,
			Keys: []model.ChannelKey{{ChannelKey: "k"}},
		}
		if _, err := TestChannelModel(ctx, ch, "m", 0, 0); err == nil {
			t.Fatal("expected error")
		}
	})
}

// TestTestChannelModel_OpenAIChat 用 httptest 模拟 OpenAI 上游。
func TestTestChannelModel_OpenAIChat(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("unexpected method %s", r.Method)
		}
		if !strings.HasSuffix(r.URL.Path, "/v1/chat/completions") {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("bad auth header: %q", got)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode body: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if body["model"] != "gpt-4o" {
			t.Errorf("bad model: %v", body["model"])
		}
		if _, ok := body["messages"]; !ok {
			t.Error("messages missing")
		}
		if body["max_tokens"] != float64(1024) {
			t.Errorf("max_tokens wrong: %v", body["max_tokens"])
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"x","choices":[{"message":{"role":"assistant","content":"octopus"}}]}`))
	}))
	defer srv.Close()

	ch := &model.Channel{
		Type:     llm.APIFormatOpenAIChatCompletion,
		BaseUrls: []model.BaseUrl{{URL: srv.URL, Delay: 1}},
		Keys:     []model.ChannelKey{{ChannelKey: "test-key"}},
	}
	res, err := TestChannelModel(context.Background(), ch, "gpt-4o", 0, 0)
	if err != nil {
		t.Fatalf("TestChannelModel: %v", err)
	}
	if res.StatusCode != http.StatusOK {
		t.Errorf("status_code = %d, want 200", res.StatusCode)
	}
	if res.Error != "" {
		t.Errorf("error = %q, want empty", res.Error)
	}
	if res.DelayMS < 0 {
		t.Errorf("delay_ms negative: %d", res.DelayMS)
	}
}

// TestTestChannelModel_Anthropic 用 httptest 模拟 Anthropic 上游,校验必要的 header。
func TestTestChannelModel_Anthropic(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/v1/messages") {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if got := r.Header.Get("X-Api-Key"); got != "test-key" {
			t.Errorf("bad x-api-key: %q", got)
		}
		if got := r.Header.Get("Anthropic-Version"); got == "" {
			t.Error("missing anthropic-version header")
		}
		w.Write([]byte(`{"id":"x","content":[{"type":"text","text":"octopus"}]}`))
	}))
	defer srv.Close()

	ch := &model.Channel{
		Type:     llm.APIFormatAnthropicMessage,
		BaseUrls: []model.BaseUrl{{URL: srv.URL, Delay: 1}},
		Keys:     []model.ChannelKey{{ChannelKey: "test-key"}},
	}
	res, err := TestChannelModel(context.Background(), ch, "claude-3-7", 0, 0)
	if err != nil {
		t.Fatalf("TestChannelModel: %v", err)
	}
	if res.StatusCode != 200 {
		t.Errorf("status_code = %d", res.StatusCode)
	}
}

// TestTestChannelModel_Gemini 校验 Gemini URL 拼接与字段名。
func TestTestChannelModel_Gemini(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, ":generateContent") {
			t.Errorf("path missing generateContent: %s", r.URL.Path)
		}
		if !strings.Contains(r.URL.Path, "/v1beta/models/gemini-2.0-flash:") {
			t.Errorf("bad path %s", r.URL.Path)
		}
		if got := r.Header.Get("X-Goog-Api-Key"); got != "test-key" {
			t.Errorf("bad x-goog-api-key: %q", got)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode body: %v", err)
		}
		if cfg, ok := body["generationConfig"].(map[string]any); !ok {
			t.Error("generationConfig missing")
		} else if cfg["maxOutputTokens"] != float64(1024) {
			t.Errorf("maxOutputTokens wrong: %v", cfg["maxOutputTokens"])
		}
		w.Write([]byte(`{"candidates":[{"content":{"parts":[{"text":"octopus"}]}}]}`))
	}))
	defer srv.Close()

	ch := &model.Channel{
		Type:     llm.APIFormatGeminiContents,
		BaseUrls: []model.BaseUrl{{URL: srv.URL, Delay: 1}},
		Keys:     []model.ChannelKey{{ChannelKey: "test-key"}},
	}
	res, err := TestChannelModel(context.Background(), ch, "gemini-2.0-flash", 0, 0)
	if err != nil {
		t.Fatalf("TestChannelModel: %v", err)
	}
	if res.StatusCode != 200 {
		t.Errorf("status_code = %d", res.StatusCode)
	}
}

// TestTestChannelModel_UpstreamError 验证 4xx 错误时把上游 message 摘要填到 Error 字段。
func TestTestChannelModel_UpstreamError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":{"message":"invalid api key"}}`))
	}))
	defer srv.Close()

	ch := &model.Channel{
		Type:     llm.APIFormatOpenAIChatCompletion,
		BaseUrls: []model.BaseUrl{{URL: srv.URL, Delay: 1}},
		Keys:     []model.ChannelKey{{ChannelKey: "test-key"}},
	}
	res, err := TestChannelModel(context.Background(), ch, "gpt-4o", 0, 0)
	if err != nil {
		t.Fatalf("TestChannelModel: %v", err)
	}
	if res.StatusCode != 401 {
		t.Errorf("status_code = %d", res.StatusCode)
	}
	if !strings.Contains(res.Error, "invalid api key") {
		t.Errorf("error missing upstream message: %q", res.Error)
	}
}

// TestTestChannelModel_Doubao 校验豆包 base path 用 v3。
func TestTestChannelModel_Doubao(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/v3/chat/completions") {
			t.Errorf("bad path for doubao: %s", r.URL.Path)
		}
		w.Write([]byte(`{"id":"x"}`))
	}))
	defer srv.Close()

	ch := &model.Channel{
		Type:     model.ChannelTypeDoubao,
		BaseUrls: []model.BaseUrl{{URL: srv.URL, Delay: 1}},
		Keys:     []model.ChannelKey{{ChannelKey: "test-key"}},
	}
	res, err := TestChannelModel(context.Background(), ch, "doubao-pro-32k", 0, 0)
	if err != nil {
		t.Fatalf("TestChannelModel: %v", err)
	}
	if res.StatusCode != 200 {
		t.Errorf("status_code = %d", res.StatusCode)
	}
}
