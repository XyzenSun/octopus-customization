package helper

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/transformer"
)

// testPrompt 模型测试使用的固定 prompt。后端硬编码,不接受前端覆盖,避免被滥用。
const testPrompt = "translate `octopus` to Español"

// testMaxTokens 各协议的最大输出 tokens 值。
// Anthropic 协议强制要求 max_tokens,所以全部协议都传入相同的语义值;
// 各协议的具体字段名(max_tokens / max_output_tokens / maxOutputTokens)在 build 函数里区分。
const testMaxTokens = 1024

// TestModelResult 单个模型的测试结果。
// 仅作为本次测试的瞬时信号,不持久化;前端会话内管理。
type TestModelResult struct {
	Model      string `json:"model"`
	StatusCode int    `json:"status_code"` // 上游 HTTP 状态码;网络错误/超时(没拿到响应)时为 0
	DelayMS    int64  `json:"delay_ms"`
	Error      string `json:"error,omitempty"`
}

// TestChannelModel 用最小 chat 请求测试渠道上的指定模型,返回延迟与状态。
// 不走 relay pipeline(没有重试/熔断/统计),仅做一次性真实 API 调用。
//
// channel 可以是已保存的(有 ID),也可以是新建弹窗里未保存的临时对象。
// keyIndex 是 channel.Keys 数组下标,默认 0;
// 由调用方保证 0 <= keyIndex < len(channel.Keys)。
//
// NOTE(security): channel 中的 ChannelKey 可能是明文,本函数不外传到响应,
// 仅用于构造对上游的 Authorization header。审计日常不写入日志。
// 已知问题:`GET /api/v1/channel/list` 本就回传明文 key,本函数不恶化但也不修复该面,
// 完整整改见 /workspace/octopus安全泄露问题.md。
func TestChannelModel(
	ctx context.Context,
	channel *model.Channel,
	modelName string,
	keyIndex int,
	timeout time.Duration,
) (*TestModelResult, error) {
	result := &TestModelResult{Model: modelName}

	// 校验请求
	if channel == nil {
		return nil, errors.New("channel is nil")
	}
	if modelName == "" {
		return nil, errors.New("model is empty")
	}
	if channel.Type == llm.APIFormatOpenAIEmbedding {
		return nil, fmt.Errorf("channel type %s does not support model testing", channel.Type)
	}
	baseURL := channel.GetBaseUrl()
	if baseURL == "" {
		return nil, errors.New("no valid base url")
	}
	if keyIndex < 0 || keyIndex >= len(channel.Keys) {
		return nil, fmt.Errorf("key_index %d out of range (have %d keys)", keyIndex, len(channel.Keys))
	}
	key := strings.TrimSpace(channel.Keys[keyIndex].ChannelKey)
	if key == "" {
		return nil, errors.New("selected key is empty")
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	// 按上游协议构造请求
	req, err := buildTestRequest(ctx, channel.Type, baseURL, key, modelName)
	if err != nil {
		return nil, err
	}
	// 自定义 Header 最后应用，允许测试与实际转发使用相同的新增、覆盖、空值和删除语义。
	ApplyCustomHeaders(req.Header, channel.CustomHeader)

	// 发送
	httpClient, err := ChannelHttpClient(channel)
	if err != nil {
		return nil, fmt.Errorf("build http client: %w", err)
	}

	start := time.Now()
	resp, err := httpClient.Do(req)
	result.DelayMS = time.Since(start).Milliseconds()
	if err != nil {
		// 网络错误 / DNS 失败 / 超时等:拿不到 HTTP 响应,StatusCode 保持 0
		result.Error = truncateErr(err.Error(), 200)
		return result, nil
	}
	defer resp.Body.Close()
	result.StatusCode = resp.StatusCode

	// 读 body 用于判断;非 2xx 时把上游错误摘要放进 Error
	bodyBytes, readErr := io.ReadAll(io.LimitReader(resp.Body, 32*1024))
	if readErr != nil {
		result.Error = truncateErr(fmt.Sprintf("read body: %v", readErr), 200)
		return result, nil
	}

	if resp.StatusCode >= 400 {
		result.Error = summarizeUpstreamError(resp.StatusCode, bodyBytes)
	}
	return result, nil
}

// buildTestRequest 按上游协议构造最小的 chat 请求,只含必填 + max_tokens 字段。
func buildTestRequest(
	ctx context.Context,
	channelType llm.APIFormat,
	baseURL, key, modelName string,
) (*http.Request, error) {
	switch channelType {
	case llm.APIFormatOpenAIChatCompletion:
		// POST {url}/v1/chat/completions
		// body: {model, messages:[{role:user, content}], max_tokens}
		url := transformer.NormalizeBaseURL(baseURL, "v1") + "/chat/completions"
		body := map[string]any{
			"model": modelName,
			"messages": []map[string]string{
				{"role": "user", "content": testPrompt},
			},
			"max_tokens": testMaxTokens,
		}
		return newJSONRequest(ctx, http.MethodPost, url, body, func(r *http.Request) {
			r.Header.Set("Authorization", "Bearer "+key)
		})

	case llm.APIFormatOpenAIResponse:
		// POST {url}/v1/responses
		// body: {model, input, max_output_tokens}
		url := transformer.NormalizeBaseURL(baseURL, "v1") + "/responses"
		body := map[string]any{
			"model":             modelName,
			"input":             testPrompt,
			"max_output_tokens": testMaxTokens,
		}
		return newJSONRequest(ctx, http.MethodPost, url, body, func(r *http.Request) {
			r.Header.Set("Authorization", "Bearer "+key)
		})

	case llm.APIFormatAnthropicMessage:
		// POST {url}/v1/messages
		// body: {model, messages:[{role:user, content}], max_tokens}
		// headers: x-api-key, anthropic-version (必填)
		url := transformer.NormalizeBaseURL(baseURL, "v1") + "/messages"
		body := map[string]any{
			"model": modelName,
			"messages": []map[string]string{
				{"role": "user", "content": testPrompt},
			},
			"max_tokens": testMaxTokens,
		}
		return newJSONRequest(ctx, http.MethodPost, url, body, func(r *http.Request) {
			r.Header.Set("X-Api-Key", key)
			r.Header.Set("Anthropic-Version", "2023-06-01")
		})

	case llm.APIFormatGeminiContents:
		// POST {url}/v1beta/models/{model}:generateContent
		// headers: X-Goog-Api-Key
		// body: {contents:[{parts:[{text}]}], generationConfig:{maxOutputTokens}}
		// Gemini transformer 处理过显式 /v1 后缀时不再拼 v1beta,这里保持一致。
		var url string
		if strings.HasSuffix(strings.TrimRight(baseURL, "/"), "/v1") {
			url = transformer.NormalizeBaseURL(baseURL, "") + "/models/" + modelName + ":generateContent"
		} else {
			url = transformer.NormalizeBaseURL(baseURL, "v1beta") + "/models/" + modelName + ":generateContent"
		}
		body := map[string]any{
			"contents": []map[string]any{
				{"parts": []map[string]string{{"text": testPrompt}}},
			},
			"generationConfig": map[string]any{
				"maxOutputTokens": testMaxTokens,
			},
		}
		return newJSONRequest(ctx, http.MethodPost, url, body, func(r *http.Request) {
			r.Header.Set("X-Goog-Api-Key", key)
		})

	case model.ChannelTypeDoubao:
		// POST {url}/v3/chat/completions  豆包协议同 OpenAI Chat Completions,但 base 默认 /v3
		url := transformer.NormalizeBaseURL(baseURL, "v3") + "/chat/completions"
		body := map[string]any{
			"model": modelName,
			"messages": []map[string]string{
				{"role": "user", "content": testPrompt},
			},
			"max_tokens": testMaxTokens,
		}
		return newJSONRequest(ctx, http.MethodPost, url, body, func(r *http.Request) {
			r.Header.Set("Authorization", "Bearer "+key)
		})

	default:
		return nil, fmt.Errorf("unsupported channel type: %s", channelType)
	}
}

// newJSONRequest 把 body 序列化为 JSON,构造 POST 请求,Content-Type 自动设置。
// extraHeaders 用于按协议分支补充鉴权等 header。
func newJSONRequest(
	ctx context.Context,
	method, url string,
	body any,
	extraHeaders func(*http.Request),
) (*http.Request, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal body: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if extraHeaders != nil {
		extraHeaders(req)
	}
	return req, nil
}

// summarizeUpstreamError 从非 2xx 响应 body 里提取简要错误,返回不超过 200 字符的描述。
// 通用兜底:状态码 + body 头几个字符,避免泄露过多上游敏感信息。
func summarizeUpstreamError(statusCode int, body []byte) string {
	trimmed := strings.TrimSpace(string(body))
	if len(trimmed) == 0 {
		return fmt.Sprintf("upstream returned %d with empty body", statusCode)
	}
	// 尝试解析通用 error message 结构(OpenAI / Anthropic / Gemini 大体是 {error: {message:...}} 的某种变体)
	var probe struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
		Message string `json:"message"` // Gemini 风格 {message: ..., code: ...}
	}
	if jsonErr := json.Unmarshal(body, &probe); jsonErr == nil {
		msg := probe.Error.Message
		if msg == "" {
			msg = probe.Message
		}
		if msg != "" {
			return truncateErr(fmt.Sprintf("%d: %s", statusCode, msg), 200)
		}
	}
	// 解析不出来就截断 raw body
	return truncateErr(fmt.Sprintf("%d: %s", statusCode, trimmed), 200)
}

func truncateErr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
