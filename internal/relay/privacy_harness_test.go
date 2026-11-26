package relay

import (
    "bytes"
    "context"
    "encoding/json"
    "flag"
    "fmt"
    "io"
    "math"
    "net"
    "net/http"
    "net/http/httptest"
    "os"
    "os/exec"
    "path/filepath"
    "regexp"
    "strconv"
    "strings"
    "sync"
    "sync/atomic"
    "testing"
    "time"

    "github.com/bestruirui/octopus/internal/db"
    "github.com/bestruirui/octopus/internal/model"
    "github.com/bestruirui/octopus/internal/op"
    "github.com/bestruirui/octopus/internal/server/auth"
    "github.com/bestruirui/octopus/internal/server/middleware"
    "github.com/gin-gonic/gin"
    "github.com/looplj/axonhub/llm"
    "github.com/looplj/axonhub/llm/httpclient"
    "gorm.io/gorm"
    "gorm.io/gorm/logger"
)

// 与 op 隔离测试套件共用标志名. TestMain 在旧数据库初始化前解析.
// 子进程复用已编译的测试程序, 包括竞态检测版本.
var octopusPrivacyWorker = flag.Bool("octopus.privacy.worker", false, "run an isolated privacy test worker")
var privacyRelayFixtureID atomic.Uint64

const (
    privacyRelaySource        = "13969909666" // 合成数据, 不含用户数据
    privacyRelayMarker        = "[[OCTOPUS_PII:PHONE_TEST]]"
    privacyRelayEscaped       = "合成行\"一\n路径\\尾"
    privacyRelayEscapedMarker = "[[OCTOPUS_PII:ESCAPED_TEST]]"
    privacyRelayUpstreamKey   = "synthetic-local-upstream-key"
    privacyRelayLargeInteger  = "9007199254740993"
)

func privacyRelayWorker(t *testing.T) {
    t.Helper()
    executable, err := os.Executable()
    if err != nil {
        t.Fatal(err)
    }
    ctx := t.Context()
    timeout := flag.Lookup("test.timeout").Value.String()
    if deadline, ok := t.Deadline(); ok {
        var cancel context.CancelFunc
        ctx, cancel = context.WithDeadline(ctx, deadline)
        defer cancel()
        timeout = time.Until(deadline).String()
    }
    // 保留调用方的子测试选择器, 详细输出及截止时间. 不递归运行 go test,
    // 也不因 Start 成功就宣称 PASS.
    selector := "^TestPrivacyRelayMock$"
    if _, suffix, found := strings.Cut(flag.Lookup("test.run").Value.String(), "/"); found {
        selector += "/" + suffix
    }
    cmd := exec.CommandContext(ctx, executable,
        "-test.run="+selector,
        "-test.v="+strconv.FormatBool(testing.Verbose()),
        "-test.timeout="+timeout, "-test.count=1", "-test.parallel=1",
        "-octopus.privacy.worker=true")
    var stdout, stderr bytes.Buffer
    cmd.Stdout, cmd.Stderr = &stdout, &stderr
    err = cmd.Run()
    t.Logf("isolated relay worker stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
    if err != nil {
        t.Fatalf("isolated relay worker failed: %v", err)
    }
    if !strings.Contains(stdout.String(), "OCTOPUS_PRIVACY_RELAY_WORKER_OK\n") ||
        !regexp.MustCompile(`(?m)^PASS\r?$`).Match(stdout.Bytes()) {
        t.Fatal("worker did not complete assertions and report PASS")
    }
}

func privacyRelayInit(t *testing.T) {
    t.Helper()
    if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "privacy-relay.db"), false); err != nil {
        t.Fatal("isolated SQLite initialization:", err)
    }
    t.Cleanup(func() {
        if err := db.Close(); err != nil {
            t.Error(err)
        }
    })
    if err := op.InitCache(); err != nil {
        t.Fatal("formal InitCache:", err)
    }
    if op.PrivacySnapshot() == nil {
        t.Fatal("InitCache did not publish privacy snapshot")
    }
    gin.SetMode(gin.TestMode)
    privacyRelaySet(t, model.SettingKeyRelayLogKeepEnabled, "true")
    privacyRelaySet(t, model.SettingKeyRelayLogFlushSize, "0")
    privacyRelaySet(t, model.SettingKeyRelayLogMaxContentSizeMB, "-1")
    // 通过正式接口建立本地价格样本, 避免费用断言空转.
    // 不获取价格数据, 不调用供应商计费服务.
    if err := op.LLMCreate(model.LLMInfo{Name: "privacy-actual-model", LLMPrice: model.LLMPrice{
        Input: 2, Output: 3, CacheRead: 0.5, CacheWrite: 1.5,
    }}, t.Context()); err != nil {
        t.Fatal("formal price fixture:", err)
    }
}

func privacyRelaySet(t *testing.T, key model.SettingKey, value string) {
    t.Helper()
    if err := op.SettingSetString(key, value); err != nil {
        t.Fatal("formal setting write:", err)
    }
}

func privacyRelayRule(t *testing.T, source, alias string, enabled bool) model.PrivacyRule {
    t.Helper()
    rule, err := op.PrivacyRuleCreate(t.Context(), model.PrivacyRuleCreate{
        Source: model.NewPrivacyBytes(source), Alias: model.NewPrivacyBytes(alias), Enabled: &enabled,
    })
    if err != nil {
        t.Fatal("formal privacy rule create:", err)
    }
    return rule
}

func privacyRelayUpdateRule(t *testing.T, rule model.PrivacyRule, source, alias string, enabled bool) {
    t.Helper()
    _, err := op.PrivacyRuleUpdate(t.Context(), model.PrivacyRuleUpdate{
        ID: rule.ID, Source: model.NewPrivacyBytes(source), Alias: model.NewPrivacyBytes(alias), Enabled: &enabled,
    })
    if err != nil {
        t.Fatal("formal privacy rule update:", err)
    }
}

// 新样本通过正式写入接口建立, 不使用缓存重置钩子. 仅 InitCache 为进程全局状态.
// 所有末级子测试串行运行, 名称和密钥均唯一.
func privacyRelayReset(t *testing.T) {
    t.Helper()
    privacyRelaySet(t, model.SettingKeyPrivacyEnabled, "false")
    rules, err := op.PrivacyRuleList(t.Context())
    if err != nil {
        t.Fatal(err)
    }
    for _, rule := range rules {
        if err := op.PrivacyRuleDelete(t.Context(), rule.ID); err != nil {
            t.Fatal(err)
        }
    }
    privacyRelaySet(t, model.SettingKeyPrivacyScopes, "1")
    privacyRelaySet(t, model.SettingKeyPrivacyFailurePolicy, "continue")
    privacyRelaySet(t, model.SettingKeyPassthroughEnabled, "true")
    privacyRelayRule(t, privacyRelaySource, "PHONE_TEST", true)
    privacyRelayRule(t, privacyRelayEscaped, "ESCAPED_TEST", true)
    privacyRelaySet(t, model.SettingKeyPrivacyEnabled, "true")
}

func privacyRelayJSON(value any) []byte {
    body, err := json.Marshal(value)
    if err != nil {
        panic(err)
    }
    return body
}

func privacyRelayDocument(t *testing.T, body []byte) any {
    t.Helper()
    decoder := json.NewDecoder(bytes.NewReader(body))
    decoder.UseNumber()
    var value any
    if err := decoder.Decode(&value); err != nil {
        t.Fatalf("invalid JSON: %v; synthetic body=%s", err, body)
    }
    if err := decoder.Decode(new(any)); err != io.EOF {
        t.Fatal("trailing JSON content")
    }
    return value
}

func privacyRelayAt(t *testing.T, value any, path string) any {
    t.Helper()
    for _, part := range strings.Split(path, ".") {
        switch node := value.(type) {
        case map[string]any:
            child, ok := node[part]
            if !ok {
                t.Fatalf("missing fixture path %s", path)
            }
            value = child
        case []any:
            index, err := strconv.Atoi(part)
            if err != nil || index < 0 || index >= len(node) {
                t.Fatalf("invalid fixture path %s", path)
            }
            value = node[index]
        default:
            t.Fatalf("non-container at fixture path %s", path)
        }
    }
    return value
}

func privacyRelayWant(t *testing.T, value any, path string, want any) {
    t.Helper()
    got := privacyRelayAt(t, value, path)
    if fmt.Sprint(got) != fmt.Sprint(want) {
        t.Fatalf("%s = %#v, want %#v", path, got, want)
    }
}

type privacyRelayRecordedRequest struct {
    body          []byte
    headers       http.Header
    path          string
    query         string
    contentLength int64
}

type privacyRelayFrame struct{ kind, id, data string }

type privacyRelayMockServer struct {
    t        *testing.T
    server   *httptest.Server
    format   llm.APIFormat
    mu       sync.Mutex
    requests []privacyRelayRecordedRequest
    // 首次请求前赋值; 处理器仅读取不可变字段.
    status     int
    body       []byte
    frames     []privacyRelayFrame
    tcpStep    int
    pauseAfter int
    paused     chan struct{}
    release    chan struct{}
    closed     chan struct{}
    pauseOnce  sync.Once
    closeOnce  sync.Once
}

func privacyRelayMockUpstream(t *testing.T, format llm.APIFormat, text string) *privacyRelayMockServer {
    t.Helper()
    m := &privacyRelayMockServer{t: t, format: format, status: http.StatusOK,
        body: privacyRelayResponse(format, text, false), pauseAfter: -1,
        paused: make(chan struct{}), release: make(chan struct{}), closed: make(chan struct{})}
    m.server = httptest.NewServer(http.HandlerFunc(m.serve))
    t.Cleanup(func() {
        // 即使断言中止子测试, 也释放阻塞的上游.
        select {
        case <-m.release:
        default:
            close(m.release)
        }
        m.server.Close()
    })
    return m
}

func (m *privacyRelayMockServer) serve(w http.ResponseWriter, r *http.Request) {
    defer m.closeOnce.Do(func() { close(m.closed) })
    body, err := io.ReadAll(io.LimitReader(r.Body, 16*1024*1024))
    if err != nil {
        m.t.Error("read local upstream request:", err)
        return
    }
    m.mu.Lock()
    m.requests = append(m.requests, privacyRelayRecordedRequest{
        body: body, headers: r.Header.Clone(), path: r.URL.Path, query: r.URL.RawQuery, contentLength: r.ContentLength,
    })
    m.mu.Unlock()
    if m.frames == nil {
        w.Header().Set("Content-Type", "application/json")
        w.Header().Set("Content-Length", strconv.Itoa(len(m.body)))
        w.WriteHeader(m.status)
        _, _ = w.Write(m.body)
        return
    }
    w.Header().Set("Content-Type", "text/event-stream")
    w.WriteHeader(m.status)
    for index, frame := range m.frames {
        var wire strings.Builder
        if frame.id != "" {
            fmt.Fprintf(&wire, "id: %s\r\n", frame.id)
        }
        if frame.kind != "" {
            fmt.Fprintf(&wire, "event: %s\r\n", frame.kind)
        }
        fmt.Fprintf(&wire, "data: %s\r\n\r\n", frame.data)
        data := []byte(wire.String())
        step := m.tcpStep
        if step <= 0 {
            step = len(data)
        }
        for len(data) > 0 {
            n := min(step, len(data))
            if _, err := w.Write(data[:n]); err != nil {
                return
            }
            w.(http.Flusher).Flush()
            data = data[n:]
        }
        if index == m.pauseAfter {
            m.pauseOnce.Do(func() { close(m.paused) })
            select {
            case <-m.release:
            case <-r.Context().Done():
                return
            }
        }
    }
}

func (m *privacyRelayMockServer) calls() int {
    m.mu.Lock()
    defer m.mu.Unlock()
    return len(m.requests)
}

func (m *privacyRelayMockServer) request(t *testing.T, index int) privacyRelayRecordedRequest {
    t.Helper()
    m.mu.Lock()
    defer m.mu.Unlock()
    if index >= len(m.requests) {
        t.Fatalf("upstream request %d missing (calls=%d)", index, len(m.requests))
    }
    return m.requests[index]
}

type privacyRelayHarness struct {
    t           *testing.T
    entry       *httptest.Server
    client      *http.Client
    done        chan struct{}
    key         model.APIKey
    channel     model.Channel
    group       model.Group
    upstream    *privacyRelayMockServer
    totalBefore model.StatsMetrics
}

func privacyRelayNewHarness(t *testing.T, upstream *privacyRelayMockServer, direct bool,
    configure func(*model.Channel, *model.Group)) *privacyRelayHarness {
    t.Helper()
    id := privacyRelayFixtureID.Add(1)
    h := &privacyRelayHarness{t: t, done: make(chan struct{}, 64), upstream: upstream}
    h.channel = model.Channel{
        Name: fmt.Sprintf("privacy_channel_%d", id), Type: upstream.format, Enabled: true,
        Model: "privacy-actual-model", Proxy: false, AutoSync: false, AutoGroup: model.AutoGroupTypeNone,
        BaseUrls: []model.BaseUrl{{URL: upstream.server.URL}},
        Keys:     []model.ChannelKey{{Enabled: true, ChannelKey: privacyRelayUpstreamKey}},
    }
    h.group = model.Group{Name: fmt.Sprintf("privacy_group_%d", id), Mode: model.GroupModeFailover,
        SessionKeepTime: 0, FirstTokenTimeOut: 0}
    if configure != nil {
        configure(&h.channel, &h.group)
    }
    if err := op.ChannelCreate(&h.channel, t.Context()); err != nil {
        t.Fatal(err)
    }
    h.group.Items = []model.GroupItem{{ChannelID: h.channel.ID, ModelName: h.channel.Model}}
    if !direct {
        if err := op.GroupCreate(&h.group, t.Context()); err != nil {
            t.Fatal(err)
        }
    }
    h.key = model.APIKey{Name: fmt.Sprintf("privacy_key_%d", id), APIKey: auth.GenerateAPIKey(), Enabled: true}
    if h.key.APIKey == "" {
        t.Fatal("GenerateAPIKey returned empty key")
    }
    if err := op.APIKeyCreate(&h.key, t.Context()); err != nil {
        t.Fatal(err)
    }
    h.totalBefore = op.StatsTotalGet().StatsMetrics
    r := gin.New()
    // 正式 Handler/metrics.Save 后握手, 确定日志持久化边界,
    // 包括 EOF 和取消场景.
    r.Use(func(c *gin.Context) { defer func() { h.done <- struct{}{} }(); c.Next() })
    v1 := r.Group("/v1", middleware.APIKeyAuth())
    v1.POST("/chat/completions", middleware.RequireJSON(), Handler(llm.APIFormatOpenAIChatCompletion))
    v1.POST("/responses", middleware.RequireJSON(), Handler(llm.APIFormatOpenAIResponse))
    v1.POST("/messages", middleware.RequireJSON(), Handler(llm.APIFormatAnthropicMessage))
    v1.POST("/embeddings", middleware.RequireJSON(), Handler(llm.APIFormatOpenAIEmbedding))
    v1.POST("/images/generations", middleware.RequireJSON(), Handler(llm.APIFormatOpenAIImageGeneration))
    v1.POST("/images/edits", Handler(llm.APIFormatOpenAIImageEdit))
    v1.POST("/images/variations", Handler(llm.APIFormatOpenAIImageVariation))
    h.entry = httptest.NewServer(r)
    // 客户端仅访问回环 IP, 不使用环境代理或重定向.
    // 生产出站地址均为上述 httptest 地址.
    transport := &http.Transport{Proxy: nil, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
        host, _, err := net.SplitHostPort(address)
        if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
            return nil, fmt.Errorf("privacy tests forbid non-loopback connections")
        }
        return (&net.Dialer{}).DialContext(ctx, network, address)
    }}
    h.client = &http.Client{Transport: transport, Timeout: 20 * time.Second,
        CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
    t.Cleanup(func() {
        select {
        case <-upstream.release:
        default:
            close(upstream.release)
        }
        h.entry.CloseClientConnections()
        h.client.CloseIdleConnections()
        h.entry.Close()
    })
    return h
}

func (h *privacyRelayHarness) requestModel(direct bool) string {
    if direct {
        return h.channel.Name + "/" + h.channel.Model
    }
    return h.group.Name
}

func privacyRelayPath(format llm.APIFormat) string {
    switch format {
    case llm.APIFormatOpenAIChatCompletion:
        return "/v1/chat/completions"
    case llm.APIFormatOpenAIResponse:
        return "/v1/responses"
    case llm.APIFormatAnthropicMessage:
        return "/v1/messages"
    default:
        panic("unsupported client fixture format")
    }
}

func (h *privacyRelayHarness) start(ctx context.Context, path string, body []byte, key, contentType string) *http.Response {
    h.t.Helper()
    r, err := http.NewRequestWithContext(ctx, http.MethodPost, h.entry.URL+path, bytes.NewReader(body))
    if err != nil {
        h.t.Fatal(err)
    }
    if key != "" {
        r.Header.Set("Authorization", "Bearer "+key)
    }
    r.Header.Set("Content-Type", contentType)
    r.Header.Set("X-Synthetic-Privacy", privacyRelaySource)
    response, err := h.client.Do(r)
    if err != nil {
        h.t.Fatal("local Gin request:", err)
    }
    return response
}

func (h *privacyRelayHarness) wait() {
    h.t.Helper()
    select {
    case <-h.done:
    case <-h.t.Context().Done():
        h.t.Fatal("suite deadline while waiting for Handler")
    case <-time.After(20 * time.Second):
        h.t.Fatal("Handler did not finish")
    }
}

func (h *privacyRelayHarness) exchange(path string, body []byte, key, contentType string) (int, []byte) {
    h.t.Helper()
    response := h.start(h.t.Context(), path, body, key, contentType)
    defer response.Body.Close()
    data, err := io.ReadAll(response.Body)
    if err != nil {
        h.t.Fatal("read Gin response:", err)
    }
    h.wait()
    if length := response.Header.Get("Content-Length"); length != "" && length != strconv.Itoa(len(data)) {
        h.t.Fatal("stale response Content-Length after privacy rewrite")
    }
    return response.StatusCode, data
}

func (h *privacyRelayHarness) json(format llm.APIFormat, body []byte) []byte {
    h.t.Helper()
    status, result := h.exchange(privacyRelayPath(format), body, h.key.APIKey, "application/json")
    if status != http.StatusOK {
        h.t.Fatalf("Gin status %d, synthetic body=%s", status, result)
    }
    privacyRelayDocument(h.t, result)
    return result
}

func (h *privacyRelayHarness) stream(format llm.APIFormat, body []byte) []privacyRelayFrame {
    h.t.Helper()
    frames, err := h.streamOutcome(format, body)
    if err != nil {
        h.t.Fatal("decode client SSE:", err)
    }
    return frames
}

// 中止或错误流可能使 HTTP 响应体读取报错.
// 同时保留事件和错误, 不把异常关闭当作正常完成.
func (h *privacyRelayHarness) streamOutcome(format llm.APIFormat, body []byte) ([]privacyRelayFrame, error) {
    h.t.Helper()
    response := h.start(h.t.Context(), privacyRelayPath(format), body, h.key.APIKey, "application/json")
    if response.StatusCode != http.StatusOK {
        data, _ := io.ReadAll(response.Body)
        response.Body.Close()
        h.wait()
        h.t.Fatalf("Gin SSE status %d: %s", response.StatusCode, data)
    }
    if !strings.Contains(response.Header.Get("Content-Type"), "text/event-stream") {
        h.t.Fatal("not SSE")
    }
    decoder := httpclient.NewDefaultSSEDecoder(h.t.Context(), response.Body)
    defer decoder.Close()
    var frames []privacyRelayFrame
    for decoder.Next() {
        event := decoder.Current()
        frames = append(frames, privacyRelayFrame{kind: event.Type, id: event.LastEventID, data: string(event.Data)})
    }
    err := decoder.Err()
    h.wait()
    return frames, err
}

func (h *privacyRelayHarness) logs() []model.RelayLog {
    h.t.Helper()
    var rows []model.RelayLog
    if err := db.GetDB().Session(&gorm.Session{Logger: logger.Discard}).
        Where("request_api_key_name = ?", h.key.Name).Order("id ASC").Find(&rows).Error; err != nil {
        h.t.Fatal(err)
    }
    return rows
}

func (h *privacyRelayHarness) log(wantTriggered bool) model.RelayLog {
    h.t.Helper()
    rows := h.logs()
    if len(rows) != 1 {
        h.t.Fatalf("persisted RelayLog rows=%d, want 1", len(rows))
    }
    row := rows[0]
    // JSON 契约断言可在并行开发的模型字段落地前编译.
    // 字段缺失必须失败, 不跳过或默认成 false.
    wire := privacyRelayDocument(h.t, privacyRelayJSON(row)).(map[string]any)
    triggered, ok := wire["privacy_triggered"].(bool)
    if !ok {
        h.t.Fatal("model.RelayLog.PrivacyTriggered / privacy_triggered is not ready")
    }
    if triggered != wantTriggered {
        h.t.Fatalf("privacy_triggered=%t, want %t", triggered, wantTriggered)
    }
    var persisted bool
    if err := db.GetDB().Session(&gorm.Session{Logger: logger.Discard}).Model(&model.RelayLog{}).
        Select("privacy_triggered").Where("id = ?", row.ID).Scan(&persisted).Error; err != nil {
        h.t.Fatal("privacy_triggered column was not persisted:", err)
    }
    if persisted != triggered {
        h.t.Fatal("JSON privacy_triggered differs from actual SQLite column")
    }
    return row
}

func (h *privacyRelayHarness) usage(input, output int64) {
    h.t.Helper()
    key := op.StatsAPIKeyGet(h.key.ID).StatsMetrics
    channel := op.StatsChannelGet(h.channel.ID).StatsMetrics
    for label, stats := range map[string]model.StatsMetrics{"APIKey": key, "Channel": channel} {
        if stats.InputToken != input || stats.OutputToken != output {
            h.t.Fatalf("%s usage=(%d,%d), want (%d,%d) once", label, stats.InputToken, stats.OutputToken, input, output)
        }
    }
    rows := h.logs()
    if len(rows) != 1 || rows[0].InputTokens != int(input) || rows[0].OutputTokens != int(output) {
        h.t.Fatalf("persisted log usage not equal to actual upstream evidence: %+v", rows)
    }
    wantInputCost, wantOutputCost := float64(input)*2e-6, float64(output)*3e-6
    if h.upstream.format == llm.APIFormatAnthropicMessage {
        wantInputCost = (float64(input-6)*2 + 4*0.5 + 2*1.5) * 1e-6
    }
    for label, stats := range map[string]model.StatsMetrics{"APIKey": key, "Channel": channel} {
        if math.Abs(stats.InputCost-wantInputCost) > 1e-12 || math.Abs(stats.OutputCost-wantOutputCost) > 1e-12 {
            h.t.Fatalf("%s cost differs from fixture price/raw usage: %+v", label, stats)
        }
    }
    if math.Abs(rows[0].Cost-wantInputCost-wantOutputCost) > 1e-12 {
        h.t.Fatal("persisted log cost differs from raw usage")
    }
    after := op.StatsTotalGet().StatsMetrics
    if after.InputToken-h.totalBefore.InputToken != input || after.OutputToken-h.totalBefore.OutputToken != output {
        h.t.Fatal("StatsTotal raw usage delta was not counted exactly once")
    }
    if err := op.StatsSaveDB(h.t.Context()); err != nil {
        h.t.Fatal(err)
    }
    if err := op.ChannelKeySaveDB(h.t.Context()); err != nil {
        h.t.Fatal(err)
    }
    conn := db.GetDB().Session(&gorm.Session{Logger: logger.Discard})
    var persistedKey model.StatsAPIKey
    var persistedChannel model.StatsChannel
    var persistedUpstreamKey model.ChannelKey
    if err := conn.First(&persistedKey, "api_key_id = ?", h.key.ID).Error; err != nil {
        h.t.Fatal(err)
    }
    if err := conn.First(&persistedChannel, "channel_id = ?", h.channel.ID).Error; err != nil {
        h.t.Fatal(err)
    }
    if err := conn.First(&persistedUpstreamKey, "id = ?", h.channel.Keys[0].ID).Error; err != nil {
        h.t.Fatal(err)
    }
    if persistedKey.InputToken != input || persistedKey.OutputToken != output ||
        persistedChannel.InputToken != input || persistedChannel.OutputToken != output ||
        math.Abs(persistedUpstreamKey.TotalCost-wantInputCost-wantOutputCost) > 1e-12 {
        h.t.Fatal("actual SQLite statistics/key cost differs from captured upstream evidence")
    }
}

func privacyRelayRequest(format llm.APIFormat, modelName, text string, stream, tool bool) []byte {
    var request map[string]any
    schema := map[string]any{"type": "object", "properties": map[string]any{"value": map[string]any{"type": "string"}}}
    switch format {
    case llm.APIFormatOpenAIChatCompletion:
        request = map[string]any{"model": modelName, "messages": []any{map[string]any{"role": "user", "content": text}}, "stream": stream}
        if stream {
            request["stream_options"] = map[string]any{"include_usage": true}
        }
        if tool {
            request["tools"] = []any{map[string]any{"type": "function", "function": map[string]any{"name": "privacy_echo", "description": "synthetic echo", "parameters": schema}}}
        }
    case llm.APIFormatOpenAIResponse:
        request = map[string]any{"model": modelName, "input": []any{map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": text}}}}, "stream": stream}
        if tool {
            request["tools"] = []any{map[string]any{"type": "function", "name": "privacy_echo", "description": "synthetic echo", "parameters": schema}}
        }
    case llm.APIFormatAnthropicMessage:
        request = map[string]any{"model": modelName, "max_tokens": 128, "messages": []any{map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": text}}}}, "stream": stream}
        if tool {
            request["tools"] = []any{map[string]any{"name": "privacy_echo", "description": "synthetic echo", "input_schema": schema}}
        }
    default:
        panic("unsupported client format")
    }
    return privacyRelayJSON(request)
}

func privacyRelayResponse(format llm.APIFormat, text string, tool bool) []byte {
    q := string(privacyRelayJSON(text))
    args := string(privacyRelayJSON(string(privacyRelayJSON(map[string]any{"value": text}))))
    switch format {
    case llm.APIFormatOpenAIChatCompletion, model.ChannelTypeDoubao:
        message := `{"role":"assistant","content":` + q + `}`
        finish := "stop"
        if tool {
            message = `{"role":"assistant","content":null,"tool_calls":[{"index":0,"id":"call_synthetic","type":"function","function":{"name":"privacy_echo","arguments":` + args + `}}]}`
            finish = "tool_calls"
        }
        return []byte(`{"id":"response_synthetic","object":"chat.completion","created":1,"model":"privacy-actual-model","choices":[{"index":0,"message":` + message + `,"finish_reason":"` + finish + `"}],"usage":{"prompt_tokens":17,"completion_tokens":5,"total_tokens":22},"synthetic_integer":` + privacyRelayLargeInteger + `}`)
    case llm.APIFormatOpenAIResponse:
        item := `{"id":"item_synthetic","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":` + q + `,"annotations":[]}]}`
        if tool {
            item = `{"id":"item_synthetic","type":"function_call","call_id":"call_synthetic","name":"privacy_echo","arguments":` + args + `,"status":"completed"}`
        }
        return []byte(`{"id":"response_synthetic","object":"response","created_at":1,"model":"privacy-actual-model","status":"completed","output":[` + item + `],"usage":{"input_tokens":17,"output_tokens":5,"total_tokens":22},"synthetic_integer":` + privacyRelayLargeInteger + `}`)
    case llm.APIFormatAnthropicMessage:
        content := `{"type":"text","text":` + q + `}`
        stop := "end_turn"
        if tool {
            content = `{"type":"tool_use","id":"call_synthetic","name":"privacy_echo","input":{"value":` + q + `}}`
            stop = "tool_use"
        }
        return []byte(`{"id":"response_synthetic","type":"message","role":"assistant","model":"privacy-actual-model","content":[` + content + `],"stop_reason":"` + stop + `","stop_sequence":null,"usage":{"input_tokens":11,"cache_read_input_tokens":4,"cache_creation_input_tokens":2,"output_tokens":5},"synthetic_integer":` + privacyRelayLargeInteger + `}`)
    case llm.APIFormatGeminiContents:
        part := `{"text":` + q + `}`
        if tool {
            part = `{"functionCall":{"name":"privacy_echo","args":{"value":` + q + `}}}`
        }
        return []byte(`{"responseId":"response_synthetic","modelVersion":"privacy-actual-model","candidates":[{"index":0,"content":{"role":"model","parts":[` + part + `]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":17,"candidatesTokenCount":5,"totalTokenCount":22}}`)
    default:
        panic("unsupported outbound format")
    }
}

// 固定依赖版本按入站形态将简单 Responses/Anthropic 消息渲染为字符串或文本块.
// 两者均为规定的线上格式; 拒绝其他形态, 并与独立固定文本预期比较.
func privacyRelayWantOutboundText(t *testing.T, format llm.APIFormat, body []byte, want string) {
    t.Helper()
    root := privacyRelayDocument(t, body)
    path := "messages.0.content"
    switch format {
    case llm.APIFormatGeminiContents:
        privacyRelayWant(t, root, "contents.0.parts.0.text", want)
        return
    case llm.APIFormatOpenAIResponse:
        if _, ok := privacyRelayAt(t, root, "input").(string); ok {
            privacyRelayWant(t, root, "input", want)
            return
        }
        path = "input.0.content"
    }
    content := privacyRelayAt(t, root, path)
    switch content.(type) {
    case string:
        privacyRelayWant(t, root, path, want)
    case []any:
        typeName := "text"
        if format == llm.APIFormatOpenAIResponse {
            typeName = "input_text"
        }
        privacyRelayWant(t, content, "0.type", typeName)
        privacyRelayWant(t, content, "0.text", want)
    default:
        t.Fatal("unexpected basic text wire shape")
    }
}

func privacyRelayTextPath(format llm.APIFormat, outbound bool) string {
    if outbound {
        panic("use explicit outbound text wire assertion")
    }
    switch format {
    case llm.APIFormatOpenAIChatCompletion:
        return "choices.0.message.content"
    case llm.APIFormatOpenAIResponse:
        return "output.0.content.0.text"
    case llm.APIFormatAnthropicMessage:
        return "content.0.text"
    default:
        panic("unsupported format")
    }
}

func privacyRelayToolValue(t *testing.T, format llm.APIFormat, body []byte) any {
    t.Helper()
    root := privacyRelayDocument(t, body)
    switch format {
    case llm.APIFormatOpenAIChatCompletion:
        args := privacyRelayAt(t, root, "choices.0.message.tool_calls.0.function.arguments").(string)
        return privacyRelayAt(t, privacyRelayDocument(t, []byte(args)), "value")
    case llm.APIFormatOpenAIResponse:
        args := privacyRelayAt(t, root, "output.0.arguments").(string)
        return privacyRelayAt(t, privacyRelayDocument(t, []byte(args)), "value")
    case llm.APIFormatAnthropicMessage:
        return privacyRelayAt(t, root, "content.0.input.value")
    default:
        panic("unsupported client format")
    }
}
