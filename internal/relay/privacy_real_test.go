package relay

import (
    "bytes"
    "context"
    "encoding/json"
    "errors"
    "flag"
    "fmt"
    "io"
    "math"
    "net/http"
    "net/http/httptest"
    "net/url"
    "os"
    "os/exec"
    "path/filepath"
    "regexp"
    "strconv"
    "strings"
    "testing"
    "time"
    "unicode"
    "unicode/utf8"

    "github.com/bestruirui/octopus/internal/db"
    "github.com/bestruirui/octopus/internal/model"
    "github.com/bestruirui/octopus/internal/op"
    "github.com/bestruirui/octopus/internal/server/auth"
    "github.com/bestruirui/octopus/internal/server/middleware"
    appLog "github.com/bestruirui/octopus/internal/utils/log"
    "github.com/gin-gonic/gin"
    "github.com/looplj/axonhub/llm"
    "go.uber.org/zap"
    "gorm.io/gorm"
    "gorm.io/gorm/logger"
)

const (
    privacyRealGateName       = "OCTOPUS_PRIVACY_REAL_TEST"
    privacyRealUser           = "隐私测试用户甲"
    privacyRealUserMarker     = "[[OCTOPUS_PII:USER_TEST]]"
    privacyRealValue          = privacyRealUser + " | " + privacyRelaySource
    privacyRealMarkedValue    = privacyRealUserMarker + " | " + privacyRelayMarker
    privacyRealRequestTimeout = 45 * time.Second
    privacyRealSuiteTimeout   = 8 * time.Minute
    privacyRealMaxCalls       = 8
    privacyRealEvidenceLimit  = 8 * 1024 * 1024
)

type privacyRealConfig struct {
    base       *url.URL
    key, model string
    format     llm.APIFormat
}

// 注入 getenv 使门禁校准不读取真实环境. 门禁未精确开启时连字段解析也不做.
func privacyRealEnvironment(getenv func(string) string) (privacyRealConfig, bool, error) {
    if getenv(privacyRealGateName) != "1" {
        return privacyRealConfig{}, false, nil
    }
    // 旧的单行命令不能静默扩大为八次外发, 也不允许调试环境改变证据保存位置.
    if getenv("OCTOPUS_PRIVACY_REAL_ONLY") != "" || getenv("OCTOPUS_PRIVACY_REAL_EVIDENCE_DIR") != "" {
        return privacyRealConfig{}, true, errors.New("REAL_DEBUG_OVERRIDE_NOT_ALLOWED")
    }
    base := getenv("OCTOPUS_PRIVACY_UPSTREAM_BASE_URL")
    key := getenv("OCTOPUS_PRIVACY_UPSTREAM_API_KEY")
    name := getenv("OCTOPUS_PRIVACY_UPSTREAM_MODEL")
    format := llm.APIFormat(getenv("OCTOPUS_PRIVACY_UPSTREAM_FORMAT"))
    // 错误只返回固定类别, 不带环境值, URL 或凭证.
    if base == "" || key == "" || name == "" || format == "" {
        return privacyRealConfig{}, true, errors.New("REAL_INPUT_MISSING")
    }
    u, err := url.Parse(base)
    if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.Opaque != "" ||
        u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" ||
        strings.ContainsAny(base, "?#") || strings.TrimSpace(base) != base {
        return privacyRealConfig{}, true, errors.New("REAL_BASE_URL_INVALID")
    }
    if strings.HasSuffix(u.Host, ":") || strings.ContainsAny(u.Host, "\\ \t\r\n") {
        return privacyRealConfig{}, true, errors.New("REAL_BASE_URL_INVALID")
    }
    if port := u.Port(); port != "" {
        n, err := strconv.Atoi(port)
        if err != nil || n < 1 || n > 65535 {
            return privacyRealConfig{}, true, errors.New("REAL_BASE_URL_INVALID")
        }
    }
    for _, b := range []byte(key) {
        if b < 33 || b > 126 {
            return privacyRealConfig{}, true, errors.New("REAL_KEY_INVALID")
        }
    }
    if !utf8.ValidString(name) || strings.IndexFunc(name, func(r rune) bool {
        return unicode.IsControl(r) || unicode.IsSpace(r) || r == ','
    }) >= 0 || strings.Contains(name, key) {
        return privacyRealConfig{}, true, errors.New("REAL_MODEL_INVALID")
    }
    switch format {
    case llm.APIFormatOpenAIChatCompletion, llm.APIFormatOpenAIResponse, llm.APIFormatAnthropicMessage:
    default:
        return privacyRealConfig{}, true, errors.New("REAL_FORMAT_INVALID")
    }
    return privacyRealConfig{base: u, key: key, model: name, format: format}, true, nil
}

type privacyRealCase struct {
    name                               string
    enabled, tool, restoreTool, stream bool
}

// 固定顺序和八行预算, 不用能力探测挑选另一套 profile.
func privacyRealProfile() []privacyRealCase {
    return []privacyRealCase{
        {name: "TextDisabled/JSON"},
        {name: "TextDisabled/SSE", stream: true},
        {name: "TextEnabled/JSON", enabled: true},
        {name: "TextEnabled/SSE", enabled: true, stream: true},
        {name: "ToolCallEnabled/JSON", enabled: true, tool: true, restoreTool: true},
        {name: "ToolCallEnabled/SSE", enabled: true, tool: true, restoreTool: true, stream: true},
        {name: "ToolCallDisabled/JSON", enabled: true, tool: true},
        {name: "ToolCallDisabled/SSE", enabled: true, tool: true, stream: true},
    }
}

func privacyRealPrompt(tc privacyRealCase, marked bool) string {
    instruction := "Return only the following value verbatim, without explanation or extra text. Value:\n"
    if tc.tool {
        instruction = "Call privacy_echo exactly once. Put the following value verbatim into value, without explanation or extra text. Value:\n"
    }
    value := privacyRealValue
    if marked {
        value = privacyRealMarkedValue
    }
    return instruction + value
}

func privacyRealRequest(format llm.APIFormat, requestModel string, tc privacyRealCase) []byte {
    root := map[string]any{}
    // 复用纯请求构造, 不复用旧 worker 的硬编码选择器和 Mock 上游.
    if err := json.Unmarshal(privacyRelayRequest(format, requestModel, privacyRealPrompt(tc, false), tc.stream, tc.tool), &root); err != nil {
        panic("invalid synthetic request")
    }
    if format == llm.APIFormatAnthropicMessage {
        root["metadata"] = map[string]any{"user_id": privacyRealValue}
        // 本格式要求 max_tokens 必填. 4096 是该测试请求的输出上限, 不影响生产请求.
        root["max_tokens"] = 4096
    } else {
        root["metadata"] = map[string]any{"privacy_excluded": privacyRealValue}
        // 不设置 OpenAI 格式的生成长度上限, 避免推理模型把预算全部用于 reasoning 而正文为空.
        delete(root, "max_completion_tokens")
        delete(root, "max_output_tokens")
    }
    if tc.tool {
        tools := privacyObserverArray(root["tools"])
        definition := privacyObserverMap(tools[0])
        schemaKey := "parameters"
        if format == llm.APIFormatOpenAIChatCompletion {
            definition = privacyObserverMap(definition["function"])
        } else if format == llm.APIFormatAnthropicMessage {
            schemaKey = "input_schema"
        }
        // 唯一 string value 必填, 不用 enum/const 或 marker 约束保证模型输出.
        schema := privacyObserverMap(definition[schemaKey])
        schema["required"] = []any{"value"}
        schema["additionalProperties"] = false
        switch format {
        case llm.APIFormatOpenAIChatCompletion:
            root["tool_choice"] = map[string]any{"type": "function", "function": map[string]any{"name": "privacy_echo"}}
        case llm.APIFormatOpenAIResponse:
            root["tool_choice"] = map[string]any{"type": "function", "name": "privacy_echo"}
        case llm.APIFormatAnthropicMessage:
            root["tool_choice"] = map[string]any{"type": "tool", "name": "privacy_echo"}
        }
    }
    return privacyRelayJSON(root)
}

func TestPrivacyRelayReal(t *testing.T) {
    cfg, enabled, err := privacyRealEnvironment(os.Getenv)
    if !enabled {
        t.Skip("real upstream gate is not exactly 1; credentials were not read")
    }
    if err != nil {
        t.Fatal(err)
    }
    if err := privacyRealCheckInvocation(flag.Lookup("test.run").Value.String(), flag.Lookup("test.skip").Value.String(), flag.Lookup("test.count").Value.String(), flag.Lookup("test.cpu").Value.String()); err != nil {
        t.Fatal(err)
    }
    if !*octopusPrivacyWorker {
        privacyRealWorker(t, "TestPrivacyRelayReal", true)
        return
    }
    privacyRealInit(t)
    privacyRealRunProfile(t, cfg, nil)
    if !t.Failed() {
        fmt.Println("OCTOPUS_PRIVACY_REAL_WORKER_OK")
    }
}

func privacyRealCheckInvocation(run, skip, count, cpu string) error {
    // 正式套件应显式选中, 避免普通回归或多组 -cpu 在开启门禁时意外消耗多轮预算.
    exactSuite := run == "^TestPrivacyRelayReal$" || run == "TestPrivacyRelayReal"
    if !exactSuite || skip != "" || count != "1" || strings.Contains(cpu, ",") {
        return errors.New("REAL_PROFILE_REQUIRES_ONE_UNFILTERED_RUN")
    }
    return nil
}

// 正式验收和回环校准共用执行器. 只有八行都通过且实际使用八次预算才算成功.
func privacyRealRunProfile(t *testing.T, cfg privacyRealConfig, transport http.RoundTripper) {
    t.Helper()
    ctx, cancel := context.WithTimeout(t.Context(), privacyRealSuiteTimeout)
    defer cancel()
    budget := new(privacyObserverBudget)
    stop := false
    passedRows := 0
    for _, tc := range privacyRealProfile() {
        if stop || ctx.Err() != nil {
            t.Fatal("REAL_SUITE_STOPPED; no diagnostic or replacement calls")
        }
        t.Run(tc.name, func(t *testing.T) {
            // fixture 的 Fatal 也必须阻止剩余真实调用.
            stop = true
            stop = privacyRealRunCase(t, ctx, cfg, tc, budget, transport)
            if !stop && !t.Failed() {
                passedRows++
            }
        })
    }
    if passedRows != privacyRealMaxCalls || budget.calls.Load() != privacyRealMaxCalls {
        t.Errorf("REAL_PROFILE_INCOMPLETE: passed=%d calls=%d want=%d", passedRows, budget.calls.Load(), privacyRealMaxCalls)
    }
}

// 只执行当前已编译二进制, 沿用 TestMain 的 octopusPrivacyWorker 隔离分支.
// 不启动 go test, 不刷新全局缓存, 不把 Key 放进参数或报告.
func privacyRealWorker(t *testing.T, testName string, real bool) {
    t.Helper()
    executable, err := os.Executable()
    if err != nil {
        t.Fatal("WORKER_EXECUTABLE_UNAVAILABLE")
    }
    ctx, cancel := context.WithTimeout(t.Context(), privacyRealSuiteTimeout)
    defer cancel()
    selector := "^" + testName + "$"
    // 真实 profile 总是八行. 本地 observer 保留调用方子测试选择器.
    if !real {
        if _, suffix, found := strings.Cut(flag.Lookup("test.run").Value.String(), "/"); found {
            selector += "/" + suffix
        }
    }
    cmd := exec.CommandContext(ctx, executable, "-test.run="+selector,
        "-test.v="+strconv.FormatBool(testing.Verbose()), "-test.timeout=8m", "-test.count=1", "-test.parallel=1",
        "-octopus.privacy.worker=true")
    var output bytes.Buffer
    cmd.Stdout, cmd.Stderr = &output, &output
    err = cmd.Run()
    // 子进程已禁用生产日志; 真实配置不串行化, 不读取 Key 来过滤父进程输出.
    t.Logf("isolated %s worker:\n%s", testName, output.String())
    if err != nil {
        t.Fatal("WORKER_FAILED")
    }
    if !strings.Contains(output.String(), "OCTOPUS_PRIVACY_"+map[bool]string{true: "REAL", false: "OBSERVER"}[real]+"_WORKER_OK\n") ||
        !regexp.MustCompile(`(?m)^PASS\r?$`).Match(output.Bytes()) {
        t.Fatal("WORKER_ASSERTIONS_INCOMPLETE")
    }
}

func privacyRealInit(t *testing.T) {
    t.Helper()
    // 独立 worker 内禁用生产日志, 避免上游失败路径打印完整 URL 或响应.
    original := appLog.Logger
    appLog.Logger = zap.NewNop().Sugar()
    t.Cleanup(func() { appLog.Logger = original })
    privacyRelayInit(t)
    privacyRelaySet(t, model.SettingKeyPrivacyEnabled, "false")
    privacyRelayRule(t, privacyRealUser, "USER_TEST", true)
    privacyRelayRule(t, privacyRelaySource, "PHONE_TEST", true)
}

// 每行都是正式单候选 fixture, 不创建或修改其他行的 API Key 冷却状态.
func privacyRealFixture(t *testing.T, cfg privacyRealConfig, tc privacyRealCase, observer *privacyObserver) *privacyRelayHarness {
    t.Helper()
    privacyRelaySet(t, model.SettingKeyPrivacyEnabled, strconv.FormatBool(tc.enabled))
    scopes := "1"
    if tc.restoreTool {
        scopes = "5"
    }
    privacyRelaySet(t, model.SettingKeyPrivacyScopes, scopes)
    privacyRelaySet(t, model.SettingKeyPrivacyFailurePolicy, "continue")
    privacyRelaySet(t, model.SettingKeyPassthroughEnabled, "true")
    if _, err := op.LLMGet(strings.ToLower(cfg.model)); err != nil {
        if err := op.LLMCreate(model.LLMInfo{Name: cfg.model, LLMPrice: model.LLMPrice{
            Input: 2, Output: 3, CacheRead: 0.5, CacheWrite: 1.5,
        }}, t.Context()); err != nil {
            t.Fatal("LOCAL_PRICE_FIXTURE_FAILED")
        }
    }
    id := privacyRelayFixtureID.Add(1)
    h := &privacyRelayHarness{t: t, done: make(chan struct{}, 4)}
    h.channel = model.Channel{Name: fmt.Sprintf("privacy_observed_channel_%d", id), Type: cfg.format,
        Enabled: true, Model: cfg.model, Proxy: false, AutoSync: false, AutoGroup: model.AutoGroupTypeNone,
        BaseUrls: []model.BaseUrl{{URL: observer.server.URL}},
        Keys:     []model.ChannelKey{{Enabled: true, ChannelKey: cfg.key}}}
    if err := op.ChannelCreate(&h.channel, t.Context()); err != nil {
        t.Fatal("FORMAL_CHANNEL_FIXTURE_FAILED")
    }
    h.group = model.Group{Name: fmt.Sprintf("privacy_observed_group_%d", id), Mode: model.GroupModeFailover,
        Items: []model.GroupItem{{ChannelID: h.channel.ID, ModelName: cfg.model}}}
    if !tc.tool {
        if err := op.GroupCreate(&h.group, t.Context()); err != nil {
            t.Fatal("FORMAL_GROUP_FIXTURE_FAILED")
        }
    }
    h.key = model.APIKey{Name: fmt.Sprintf("privacy_observed_key_%d", id), APIKey: auth.GenerateAPIKey(), Enabled: true}
    if h.key.APIKey == "" || op.APIKeyCreate(&h.key, t.Context()) != nil {
        t.Fatal("FORMAL_API_KEY_FIXTURE_FAILED")
    }
    h.totalBefore = op.StatsTotalGet().StatsMetrics
    r := gin.New()
    r.Use(func(c *gin.Context) { defer func() { h.done <- struct{}{} }(); c.Next() })
    v1 := r.Group("/v1", middleware.APIKeyAuth(), middleware.RequireJSON())
    v1.POST("/chat/completions", Handler(llm.APIFormatOpenAIChatCompletion))
    v1.POST("/responses", Handler(llm.APIFormatOpenAIResponse))
    v1.POST("/messages", Handler(llm.APIFormatAnthropicMessage))
    h.entry = httptest.NewServer(r)
    h.client = &http.Client{Transport: privacyObserverLoopbackTransport(), Timeout: privacyRealRequestTimeout,
        CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
    t.Cleanup(func() {
        h.entry.CloseClientConnections()
        h.client.CloseIdleConnections()
        h.entry.Close()
    })
    return h
}

// 返回值只表示必须停止套件的环境或系统失败; 模型往返失败不会自动重跑或 skip.
func privacyRealRunCase(t *testing.T, suite context.Context, cfg privacyRealConfig, tc privacyRealCase,
    budget *privacyObserverBudget, transport http.RoundTripper) bool {
    t.Helper()
    ctx, cancel := context.WithTimeout(suite, privacyRealRequestTimeout)
    defer cancel()
    observer := privacyObserverNew(t, ctx, cfg, tc, budget, transport)
    h := privacyRealFixture(t, cfg, tc, observer)
    a := privacyRealRequest(cfg.format, h.requestModel(tc.tool), tc)
    r, err := http.NewRequestWithContext(ctx, http.MethodPost, h.entry.URL+privacyRelayPath(cfg.format), bytes.NewReader(a))
    if err != nil {
        t.Error("CLIENT_REQUEST_BUILD_FAILED")
        return true
    }
    r.Header.Set("Content-Type", "application/json")
    r.Header.Set("Authorization", "Bearer "+h.key.APIKey)
    r.Header.Set("X-Synthetic-Privacy", privacyRealValue)
    response, err := h.client.Do(r)
    var d []byte
    if err == nil {
        d, err = io.ReadAll(io.LimitReader(response.Body, privacyRealEvidenceLimit+1))
        response.Body.Close()
    }
    if err != nil || len(d) > privacyRealEvidenceLimit {
        cancel()
        privacyRealWaitHandler(t, h)
        t.Error("SYSTEM_NETWORK_OR_RESPONSE_LIMIT_FAILED")
        return true
    }
    privacyRealWaitHandler(t, h)
    b, c, observedStatus, observerCode := observer.evidence()
    if observerCode != "" || budget.stopped.Load() || observedStatus != http.StatusOK {
        t.Errorf("case=%s format=%s model=%s outbound_ok=false system_ok=false", tc.name, cfg.format, cfg.model)
        return true
    }
    upstream, upstreamErr := privacyObserverNative(cfg.format, tc.stream, tc.tool, c)
    client, clientErr := privacyObserverNative(cfg.format, tc.stream, tc.tool, d)
    if upstreamErr != nil && upstreamErr.Error() == "NATIVE_UPSTREAM_ERROR" {
        t.Error("SYSTEM_NATIVE_UPSTREAM_ERROR; remaining calls stopped")
        return true
    }
    if response.StatusCode != http.StatusOK {
        t.Error("RELAY_RESPONSE_FAILED")
    }
    if tc.stream && !strings.Contains(response.Header.Get("Content-Type"), "text/event-stream") {
        t.Error("CLIENT_SSE_CONTENT_TYPE_FAILED")
        return true
    }
    if length := response.Header.Get("Content-Length"); length != "" && length != strconv.Itoa(len(d)) {
        t.Error("CLIENT_CONTENT_LENGTH_FAILED")
    }
    for _, body := range [][]byte{a, b, c, d} {
        if bytes.Contains(body, []byte(cfg.key)) || bytes.Contains(body, []byte(h.key.APIKey)) {
            t.Error("AUTHENTICATION_BODY_LEAK")
            return true
        }
    }
    // 每个子测试仅在自身临时目录保存合成正文, 结束后自动清理; 不保存 Header 或 URL.
    dir := t.TempDir()
    for name, body := range map[string][]byte{"A": a, "B": b, "C": c, "D": d} {
        if os.WriteFile(filepath.Join(dir, name+".body"), body, 0600) != nil {
            t.Error("SYNTHETIC_EVIDENCE_SAVE_FAILED")
        }
    }
    mechanical, strict := privacyObserverVerdict(tc, upstream, client)
    if !mechanical {
        t.Error("RESTORE_CONVERSION_OR_STREAM_EVIDENCE_FAILED")
    }
    if !strict {
        t.Error("MODEL_MARKER_NOT_PRESERVED")
    }
    usageOK := upstreamErr == nil && clientErr == nil && upstream.usage == client.usage
    if !usageOK {
        t.Error("NATIVE_CLIENT_USAGE_FAILED")
    } else {
        usageOK = privacyRealUsage(t, h, upstream.usage, tc.enabled)
    }
    t.Logf("case=%s format=%s model=%s hash_B=%s hash_C=%s hash_D=%s outbound_ok=true mechanical_ok=%t marker_ok=%t usage_ok=%t usage=%+v",
        tc.name, cfg.format, cfg.model, privacyObserverHash(b), privacyObserverHash(c), privacyObserverHash(d), mechanical, strict, usageOK, upstream.usage)
    return false
}

func TestPrivacyRealGate(t *testing.T) {
    for _, gate := range []string{"", "0", "true", "01", " 1", "1 ", "\n1"} {
        t.Run("Closed_"+strconv.Itoa(len(gate))+"_"+privacyObserverHash([]byte(gate))[:8], func(t *testing.T) {
            reads := 0
            _, enabled, err := privacyRealEnvironment(func(name string) string {
                reads++
                if name != privacyRealGateName {
                    t.Fatal("closed gate read an upstream field")
                }
                return gate
            })
            if enabled || err != nil || reads != 1 {
                t.Fatal("gate must be exactly 1 before reading input")
            }
        })
    }
    valid := map[string]string{privacyRealGateName: "1", "OCTOPUS_PRIVACY_UPSTREAM_BASE_URL": "https://synthetic.invalid/prefix/v1",
        "OCTOPUS_PRIVACY_UPSTREAM_API_KEY": privacyRelayUpstreamKey, "OCTOPUS_PRIVACY_UPSTREAM_MODEL": "privacy-actual-model",
        "OCTOPUS_PRIVACY_UPSTREAM_FORMAT": string(llm.APIFormatOpenAIChatCompletion)}
    t.Run("EncodedPathPrefix", func(t *testing.T) {
        for _, prefix := range []string{"/tenant%2F", "/tenant%2f/", "/tenant%252F/", "/tenant%20name/"} {
            cfg, enabled, err := privacyRealEnvironment(func(name string) string {
                if name == "OCTOPUS_PRIVACY_UPSTREAM_BASE_URL" {
                    return "https://synthetic.invalid" + prefix
                }
                return valid[name]
            })
            if !enabled || err != nil || cfg.base.EscapedPath() != prefix {
                t.Fatal("legal encoded prefix was rejected or normalized before rewrite")
            }
        }
    })
    for _, field := range []string{"OCTOPUS_PRIVACY_UPSTREAM_BASE_URL", "OCTOPUS_PRIVACY_UPSTREAM_API_KEY", "OCTOPUS_PRIVACY_UPSTREAM_MODEL", "OCTOPUS_PRIVACY_UPSTREAM_FORMAT"} {
        t.Run("Missing_"+field, func(t *testing.T) {
            _, enabled, err := privacyRealEnvironment(func(name string) string {
                if name == field {
                    return ""
                }
                return valid[name]
            })
            if !enabled || err == nil || strings.Contains(err.Error(), privacyRelayUpstreamKey) {
                t.Fatal("missing input was not rejected safely")
            }
        })
    }
    for index, base := range []string{"http://synthetic.invalid/v1", "/v1", "https://u:p@synthetic.invalid/v1", "https://synthetic.invalid/v1?x=y",
        "https://synthetic.invalid/v1?", "https://synthetic.invalid/v1#x", "https://synthetic.invalid/v1#", " https://synthetic.invalid/v1",
        "https:///v1", "https://synthetic.invalid:0/v1", "https://synthetic.invalid:65536/v1", "https://synthetic.invalid:/v1", "https://synthetic.invalid/%zz"} {
        t.Run("InvalidBase_"+strconv.Itoa(index), func(t *testing.T) {
            _, enabled, err := privacyRealEnvironment(func(name string) string {
                if name == "OCTOPUS_PRIVACY_UPSTREAM_BASE_URL" {
                    return base
                }
                return valid[name]
            })
            if !enabled || err == nil || strings.Contains(err.Error(), base) {
                t.Fatal("unsafe URL accepted or echoed")
            }
        })
    }
    for _, format := range []llm.APIFormat{llm.APIFormatOpenAIChatCompletion, llm.APIFormatOpenAIResponse, llm.APIFormatAnthropicMessage, llm.APIFormatGeminiContents, model.ChannelTypeDoubao, "unknown"} {
        t.Run("Format_"+format.String(), func(t *testing.T) {
            cfg, enabled, err := privacyRealEnvironment(func(name string) string {
                if name == "OCTOPUS_PRIVACY_UPSTREAM_FORMAT" {
                    return string(format)
                }
                return valid[name]
            })
            allowed := format == llm.APIFormatOpenAIChatCompletion || format == llm.APIFormatOpenAIResponse || format == llm.APIFormatAnthropicMessage
            if !enabled || (err == nil) != allowed {
                t.Fatal("format gate did not follow fixed native profile")
            }
            if allowed && cfg.base.Path != "/prefix/v1" {
                t.Fatal("path prefix was not preserved")
            }
        })
    }
    for index, bad := range []struct{ field, value string }{
        {"OCTOPUS_PRIVACY_UPSTREAM_API_KEY", "synthetic\r\nAuthorization: bad"},
        {"OCTOPUS_PRIVACY_UPSTREAM_API_KEY", " synthetic"}, {"OCTOPUS_PRIVACY_UPSTREAM_MODEL", "model\nother"},
        {"OCTOPUS_PRIVACY_UPSTREAM_MODEL", "model,other"}, {"OCTOPUS_PRIVACY_UPSTREAM_MODEL", "model "},
    } {
        t.Run("InvalidField_"+strconv.Itoa(index), func(t *testing.T) {
            _, _, err := privacyRealEnvironment(func(name string) string {
                if name == bad.field {
                    return bad.value
                }
                return valid[name]
            })
            if err == nil {
                t.Fatal("invalid header or model accepted")
            }
        })
    }
    t.Run("RejectDebugOverridesBeforeReadingCredentials", func(t *testing.T) {
        for _, override := range []string{"OCTOPUS_PRIVACY_REAL_ONLY", "OCTOPUS_PRIVACY_REAL_EVIDENCE_DIR"} {
            _, enabled, err := privacyRealEnvironment(func(name string) string {
                switch name {
                case privacyRealGateName:
                    return "1"
                case override:
                    return "synthetic"
                case "OCTOPUS_PRIVACY_REAL_ONLY", "OCTOPUS_PRIVACY_REAL_EVIDENCE_DIR":
                    return ""
                default:
                    t.Fatal("debug invocation read an upstream field")
                    return ""
                }
            })
            if !enabled || err == nil || err.Error() != "REAL_DEBUG_OVERRIDE_NOT_ALLOWED" {
                t.Fatal("debug invocation did not fail closed")
            }
        }
    })
    t.Run("RejectPartialOrRepeatedRuns", func(t *testing.T) {
        if err := privacyRealCheckInvocation("^TestPrivacyRelayReal$", "", "1", ""); err != nil {
            t.Fatal(err)
        }
        for _, args := range [][4]string{
            {"^TestPrivacyRelayReal$/TextEnabled", "", "1", ""},
            {"^TestPrivacyRelayReal$", "ToolCallDisabled", "1", ""},
            {"^TestPrivacyRelayReal$", "", "2", ""},
            {"^TestPrivacyRelayReal$", "", "1", "1,4"},
            {"^TestPrivacy", "", "1", ""},
            {"", "", "1", ""},
        } {
            if privacyRealCheckInvocation(args[0], args[1], args[2], args[3]) == nil {
                t.Fatal("partial or repeated real profile was accepted")
            }
        }
    })
    profile := privacyRealProfile()
    want := []string{"TextDisabled/JSON", "TextDisabled/SSE", "TextEnabled/JSON", "TextEnabled/SSE", "ToolCallEnabled/JSON", "ToolCallEnabled/SSE", "ToolCallDisabled/JSON", "ToolCallDisabled/SSE"}
    if len(profile) != privacyRealMaxCalls || privacyRealRequestTimeout != 45*time.Second || privacyRealSuiteTimeout != 8*time.Minute {
        t.Fatal("fixed profile budget or deadlines changed")
    }
    for i, tc := range profile {
        if tc.name != want[i] || tc.stream != (i%2 == 1) || tc.tool != (i >= 4) || tc.enabled != (i >= 2) || tc.restoreTool != (i == 4 || i == 5) {
            t.Fatal("fixed profile changed")
        }
    }
}

func privacyRealWaitHandler(t *testing.T, h *privacyRelayHarness) {
    t.Helper()
    select {
    case <-h.done:
    case <-time.After(privacyRealRequestTimeout):
        t.Fatal("HANDLER_SHUTDOWN_FAILED")
    }
}

// 独立 C 用量和固定本地单价计算, 不调用被测 RawUsageCapture 或 usageMetrics.
func privacyRealUsage(t *testing.T, h *privacyRelayHarness, u privacyObserverUsage, triggered bool) bool {
    t.Helper()
    valid := true
    rows := h.logs()
    if len(rows) != 1 {
        t.Error("PERSISTED_RELAY_LOG_NOT_ONCE")
        return false
    }
    row := rows[0]
    nonCached := u.Input - u.CacheRead - u.CacheWrite
    if nonCached < 0 {
        nonCached = u.Input
    }
    inputCost := (float64(nonCached)*2 + float64(u.CacheRead)*0.5 + float64(u.CacheWrite)*1.5) * 1e-6
    outputCost := float64(u.Output) * 3e-6
    costOK := func(a, b float64) bool { return math.Abs(a-b) < 1e-12 }
    check := func(label string, s model.StatsMetrics) {
        if s.InputToken != u.Input || s.OutputToken != u.Output || s.RequestSuccess != 1 || s.RequestFailed != 0 ||
            !costOK(s.InputCost, inputCost) || !costOK(s.OutputCost, outputCost) {
            valid = false
            t.Errorf("%s_USAGE_COST_OR_REQUEST_NOT_ONCE", label)
        }
    }
    check("API_KEY", op.StatsAPIKeyGet(h.key.ID).StatsMetrics)
    check("CHANNEL", op.StatsChannelGet(h.channel.ID).StatsMetrics)
    after := op.StatsTotalGet().StatsMetrics
    check("TOTAL", model.StatsMetrics{InputToken: after.InputToken - h.totalBefore.InputToken,
        OutputToken: after.OutputToken - h.totalBefore.OutputToken, InputCost: after.InputCost - h.totalBefore.InputCost,
        OutputCost: after.OutputCost - h.totalBefore.OutputCost, RequestSuccess: after.RequestSuccess - h.totalBefore.RequestSuccess,
        RequestFailed: after.RequestFailed - h.totalBefore.RequestFailed})
    if row.InputTokens != int(u.Input) || row.OutputTokens != int(u.Output) || !costOK(row.Cost, inputCost+outputCost) ||
        row.PrivacyTriggered != triggered || row.ChannelId != h.channel.ID || row.ActualModelName != h.channel.Model ||
        row.TotalAttempts != 1 || len(row.Attempts) != 1 || row.Attempts[0].Status != model.AttemptSuccess {
        valid = false
        t.Error("PERSISTED_RELAY_LOG_EVIDENCE_FAILED")
    }
    if op.StatsSaveDB(t.Context()) != nil || op.ChannelKeySaveDB(t.Context()) != nil {
        t.Error("FORMAL_STATS_PERSISTENCE_FAILED")
        return false
    }
    conn := db.GetDB().Session(&gorm.Session{Logger: logger.Discard})
    var keyStats model.StatsAPIKey
    var channelStats model.StatsChannel
    var total model.StatsTotal
    var channelKey model.ChannelKey
    if conn.First(&keyStats, "api_key_id = ?", h.key.ID).Error != nil ||
        conn.First(&channelStats, "channel_id = ?", h.channel.ID).Error != nil ||
        conn.First(&channelKey, "id = ?", h.channel.Keys[0].ID).Error != nil || conn.First(&total).Error != nil {
        t.Error("SQLITE_USAGE_EVIDENCE_MISSING")
        return false
    }
    check("SQLITE_API_KEY", keyStats.StatsMetrics)
    check("SQLITE_CHANNEL", channelStats.StatsMetrics)
    if total.InputToken != after.InputToken || total.OutputToken != after.OutputToken ||
        !costOK(total.InputCost, after.InputCost) || !costOK(total.OutputCost, after.OutputCost) ||
        total.RequestSuccess != after.RequestSuccess || total.RequestFailed != after.RequestFailed ||
        !costOK(channelKey.TotalCost, inputCost+outputCost) {
        valid = false
        t.Error("SQLITE_TOTAL_OR_CHANNEL_KEY_COST_NOT_ONCE")
    }
    return valid
}
