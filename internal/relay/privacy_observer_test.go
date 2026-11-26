package relay

import (
    "bufio"
    "bytes"
    "compress/gzip"
    "context"
    "crypto/sha256"
    "crypto/tls"
    "encoding/hex"
    "encoding/json"
    "errors"
    "fmt"
    "io"
    "log"
    "math"
    "net"
    "net/http"
    "net/http/httptest"
    "net/http/httputil"
    "net/url"
    "reflect"
    "strconv"
    "strings"
    "sync"
    "sync/atomic"
    "testing"
    "time"
    "unicode/utf8"

    "github.com/looplj/axonhub/llm"
)

type privacyObserverBudget struct {
    calls   atomic.Int32
    stopped atomic.Bool
}

func (b *privacyObserverBudget) reserve(perCase *atomic.Int32) error {
    if b.stopped.Load() || !perCase.CompareAndSwap(0, 1) {
        b.stopped.Store(true)
        return errors.New("OBSERVER_DUPLICATE_OR_STOPPED")
    }
    for {
        n := b.calls.Load()
        if n >= privacyRealMaxCalls {
            b.stopped.Store(true)
            return errors.New("OBSERVER_BUDGET_EXHAUSTED")
        }
        if b.calls.CompareAndSwap(n, n+1) {
            return nil
        }
    }
}

type privacyObserver struct {
    server   *httptest.Server
    cfg      privacyRealConfig
    tc       privacyRealCase
    budget   *privacyObserverBudget
    perCase  atomic.Int32
    mu       sync.Mutex
    b, c     []byte
    status   int
    encoding string
    code     string
}

func privacyObserverHash(body []byte) string {
    sum := sha256.Sum256(body)
    return hex.EncodeToString(sum[:])
}

func privacyObserverObject(body []byte) (map[string]any, error) {
    d := json.NewDecoder(bytes.NewReader(body))
    d.UseNumber()
    var root map[string]any
    if d.Decode(&root) != nil || root == nil || d.Decode(new(any)) != io.EOF {
        return nil, errors.New("NATIVE_JSON_INVALID")
    }
    return root, nil
}

func privacyObserverMap(value any) map[string]any { m, _ := value.(map[string]any); return m }
func privacyObserverArray(value any) []any        { a, _ := value.([]any); return a }
func privacyObserverString(value any) string      { s, _ := value.(string); return s }

func privacyObserverEndpoint(format llm.APIFormat) string {
    switch format {
    case llm.APIFormatOpenAIChatCompletion:
        return "/chat/completions"
    case llm.APIFormatOpenAIResponse:
        return "/responses"
    case llm.APIFormatAnthropicMessage:
        return "/messages"
    }
    panic("unsupported observer format")
}

// 独立字段投影和字面期望, 不调用隐私处理器制造 B 的 oracle.
func privacyObserverEffectiveText(format llm.APIFormat, root map[string]any) (string, error) {
    var content any
    messages := privacyObserverArray(root["messages"])
    if format == llm.APIFormatOpenAIResponse {
        if s, ok := root["input"].(string); ok {
            return s, nil
        }
        messages = privacyObserverArray(root["input"])
    }
    if len(messages) != 1 {
        return "", errors.New("B_MESSAGE_CARDINALITY_INVALID")
    }
    message := privacyObserverMap(messages[0])
    if message["role"] != "user" {
        return "", errors.New("B_MESSAGE_ROLE_INVALID")
    }
    content = message["content"]
    if text, ok := content.(string); ok {
        return text, nil
    }
    parts := privacyObserverArray(content)
    if len(parts) != 1 {
        return "", errors.New("B_CONTENT_CARDINALITY_INVALID")
    }
    part := privacyObserverMap(parts[0])
    kind := "text"
    if format == llm.APIFormatOpenAIResponse {
        kind = "input_text"
    }
    text, ok := part["text"].(string)
    if !ok || part["type"] != kind {
        return "", errors.New("B_CONTENT_SHAPE_INVALID")
    }
    return text, nil
}

func privacyObserverCalibrate(cfg privacyRealConfig, tc privacyRealCase, r *http.Request, body []byte) error {
    fail := func() error { return errors.New("B_CALIBRATION_FAILED") }
    if r.Method != http.MethodPost || r.URL.Path != privacyObserverEndpoint(cfg.format) ||
        r.URL.RawQuery != "" || r.URL.ForceQuery || r.URL.Fragment != "" ||
        (r.ContentLength >= 0 && r.ContentLength != int64(len(body))) ||
        !strings.Contains(r.Header.Get("Content-Type"), "application/json") {
        return fail()
    }
    if r.Header.Get("Idempotency-Key") != "" || r.Header.Get("X-Idempotency-Key") != "" ||
        r.Header.Get("Proxy-Authorization") != "" || r.Header.Get("X-Synthetic-Privacy") != privacyRealValue {
        return fail()
    }
    if cfg.format == llm.APIFormatAnthropicMessage {
        if r.Header.Get("X-Api-Key") != cfg.key || r.Header.Get("Authorization") != "" ||
            r.Header.Get("Anthropic-Version") != "2023-06-01" {
            return fail()
        }
    } else if r.Header.Get("Authorization") != "Bearer "+cfg.key || r.Header.Get("X-Api-Key") != "" {
        return fail()
    }
    root, err := privacyObserverObject(body)
    if err != nil || root["model"] != cfg.model || bytes.Contains(body, []byte(cfg.key)) {
        return fail()
    }
    text, err := privacyObserverEffectiveText(cfg.format, root)
    if err != nil || text != privacyRealPrompt(tc, tc.enabled) {
        return fail()
    }
    // 消息中的两个 source 各出现一次, 因此精确字符串比较同时校准一次替换和未递归替换.
    stream, _ := root["stream"].(bool)
    if stream != tc.stream {
        return fail()
    }
    expected, _ := privacyObserverObject(privacyRealRequest(cfg.format, cfg.model, tc))
    if !reflect.DeepEqual(root["metadata"], expected["metadata"]) {
        return fail()
    }
    // 生成长度字段按构造器的期望逐键比较: OpenAI 格式不出现任何上限; Anthropic 的必填 max_tokens 与构造值一致.
    for _, name := range []string{"max_completion_tokens", "max_output_tokens", "max_tokens"} {
        want, wanted := expected[name]
        got, present := root[name]
        if wanted != present || (wanted && !reflect.DeepEqual(got, want)) {
            return fail()
        }
    }
    if !reflect.DeepEqual(root["tools"], expected["tools"]) ||
        !reflect.DeepEqual(root["tool_choice"], expected["tool_choice"]) {
        return fail()
    }
    if cfg.format == llm.APIFormatOpenAIChatCompletion && tc.stream &&
        !reflect.DeepEqual(root["stream_options"], expected["stream_options"]) {
        return fail()
    }
    return nil
}

func privacyObserverLoopbackTransport() *http.Transport {
    return &http.Transport{Proxy: nil, DisableKeepAlives: true, ForceAttemptHTTP2: false,
        TLSNextProto: map[string]func(string, *tls.Conn) http.RoundTripper{},
        DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
            host, _, err := net.SplitHostPort(address)
            ip := net.ParseIP(host)
            if err != nil || ip == nil || !ip.IsLoopback() {
                return nil, errors.New("NON_LOOPBACK_FORBIDDEN")
            }
            return (&net.Dialer{Timeout: privacyRealRequestTimeout}).DialContext(ctx, network, address)
        }}
}

// 单次新建 HTTP/1 连接且不可重放 Body: Transport 不能透明重试失败连接.
func privacyObserverRealTransport() *http.Transport {
    return &http.Transport{Proxy: nil, DisableKeepAlives: true, ForceAttemptHTTP2: false,
        TLSNextProto:        map[string]func(string, *tls.Conn) http.RoundTripper{},
        DialContext:         (&net.Dialer{Timeout: privacyRealRequestTimeout}).DialContext,
        TLSHandshakeTimeout: privacyRealRequestTimeout, ResponseHeaderTimeout: privacyRealRequestTimeout}
}

func (o *privacyObserver) fail(code string) {
    o.mu.Lock()
    if o.code == "" {
        o.code = code
    }
    o.mu.Unlock()
    o.budget.stopped.Store(true)
}

func (o *privacyObserver) evidence() ([]byte, []byte, int, string) {
    o.mu.Lock()
    defer o.mu.Unlock()
    // 上游可能对非流式响应使用 gzip; relay 客户端会解压, 观测侧只在副本上解压后解析.
    c := bytes.Clone(o.c)
    if strings.Contains(o.encoding, "gzip") || bytes.HasPrefix(c, []byte{0x1f, 0x8b}) {
        if decoded, err := privacyObserverGunzip(c); err == nil {
            c = decoded
        } else {
            return bytes.Clone(o.b), bytes.Clone(o.c), o.status, "C_GZIP_DECODE_FAILED"
        }
    }
    return bytes.Clone(o.b), c, o.status, o.code
}

func privacyObserverGunzip(body []byte) ([]byte, error) {
    reader, err := gzip.NewReader(bytes.NewReader(body))
    if err != nil {
        return nil, err
    }
    defer reader.Close()
    decoded, err := io.ReadAll(io.LimitReader(reader, privacyRealEvidenceLimit+1))
    if err != nil {
        return nil, err
    }
    if len(decoded) > privacyRealEvidenceLimit {
        return nil, errors.New("C_EVIDENCE_LIMIT_FAILED")
    }
    return decoded, nil
}

type privacyObserverTransport struct {
    observer *privacyObserver
    next     http.RoundTripper
}

func (rt privacyObserverTransport) RoundTrip(r *http.Request) (*http.Response, error) {
    o := rt.observer
    // 在 ReverseProxy 完成 URL 和 Body 重建之后, 真正外发前才原子预留预算.
    if r.Context().Err() != nil || r.URL.Scheme != "https" || r.URL.Host != o.cfg.base.Host ||
        r.URL.RawQuery != "" || r.URL.User != nil || r.GetBody != nil || !r.Close {
        o.fail("OBSERVER_OUTBOUND_SAFETY_FAILED")
        return nil, errors.New("OBSERVER_OUTBOUND_SAFETY_FAILED")
    }
    if err := o.budget.reserve(&o.perCase); err != nil {
        o.fail("OBSERVER_BUDGET_FAILED")
        return nil, errors.New("OBSERVER_BUDGET_FAILED")
    }
    response, err := rt.next.RoundTrip(r)
    if err != nil {
        o.fail("SYSTEM_NETWORK_FAILED")
        return nil, errors.New("SYSTEM_NETWORK_FAILED")
    }
    o.mu.Lock()
    o.status = response.StatusCode
    o.encoding = strings.ToLower(response.Header.Get("Content-Encoding"))
    o.mu.Unlock()
    if response.StatusCode >= http.StatusMultipleChoices && response.StatusCode < http.StatusBadRequest {
        // 不能把 3xx/Location 交给真实 relay 的默认 http.Client, 否则它会绕过预算跟随并携带 X-Api-Key.
        response.Body.Close()
        o.fail("SYSTEM_UPSTREAM_REDIRECT_BLOCKED")
        return nil, errors.New("SYSTEM_UPSTREAM_REDIRECT_BLOCKED")
    }
    if response.StatusCode != http.StatusOK {
        o.fail("SYSTEM_HTTP_AUTH_QUOTA_OR_CAPABILITY_FAILED")
    }
    for key, values := range response.Header {
        if strings.EqualFold(key, "Authorization") || strings.EqualFold(key, "X-Api-Key") ||
            strings.Contains(strings.Join(values, ""), o.cfg.key) {
            response.Body.Close()
            o.fail("UPSTREAM_AUTHENTICATION_HEADER_LEAK")
            return nil, errors.New("UPSTREAM_AUTHENTICATION_HEADER_LEAK")
        }
    }
    response.Body = &privacyObserverTee{ReadCloser: response.Body, observer: o}
    return response, nil
}

type privacyObserverTee struct {
    io.ReadCloser
    observer *privacyObserver
}

// 每个 Read 立即复制 C 并返回给 ReverseProxy. 不缓冲完整响应后再转发.
func (tee *privacyObserverTee) Read(p []byte) (int, error) {
    n, err := tee.ReadCloser.Read(p)
    o := tee.observer
    o.mu.Lock()
    overflow := len(o.c)+n > privacyRealEvidenceLimit
    if !overflow {
        o.c = append(o.c, p[:n]...)
    }
    o.mu.Unlock()
    if overflow {
        o.fail("C_EVIDENCE_LIMIT_FAILED")
        return 0, errors.New("C_EVIDENCE_LIMIT_FAILED")
    }
    if err != nil && err != io.EOF {
        o.fail("SYSTEM_STREAM_READ_FAILED")
        return n, errors.New("SYSTEM_STREAM_READ_FAILED")
    }
    return n, err
}

// 只在转义路径上裁掉字面尾斜杠, 再一次解码设置匹配的 Path/RawPath.
// %2F 是用户前缀的一部分, 不能用已解码 Path 的 TrimRight 把它删掉.
func privacyObserverTarget(cfg privacyRealConfig) (*url.URL, error) {
    u := *cfg.base
    escaped := strings.TrimRight(cfg.base.EscapedPath(), "/") + privacyObserverEndpoint(cfg.format)
    path, err := url.PathUnescape(escaped)
    if err != nil {
        return nil, errors.New("OBSERVER_TARGET_PATH_INVALID")
    }
    u.Path, u.RawPath = path, escaped
    return &u, nil
}

func privacyObserverNew(t *testing.T, suite context.Context, cfg privacyRealConfig, tc privacyRealCase,
    budget *privacyObserverBudget, next http.RoundTripper) *privacyObserver {
    t.Helper()
    target, err := privacyObserverTarget(cfg)
    if err != nil {
        t.Fatal("OBSERVER_TARGET_PATH_INVALID")
    }
    if next == nil {
        next = privacyObserverRealTransport()
    }
    o := &privacyObserver{cfg: cfg, tc: tc, budget: budget}
    proxy := &httputil.ReverseProxy{Transport: privacyObserverTransport{observer: o, next: next},
        FlushInterval: -1, ErrorLog: log.New(io.Discard, "", 0),
        ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
            o.fail("OBSERVER_FORWARD_FAILED")
            w.Header().Del("Location")
            http.Error(w, "OBSERVER_FORWARD_FAILED", http.StatusBadGateway)
        },
        Rewrite: func(pr *httputil.ProxyRequest) {
            u := *target
            pr.Out.URL = &u
            pr.Out.Host = u.Host
            pr.Out.GetBody = nil
            pr.Out.Close = true
        }}
    o.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        ctx, cancel := context.WithTimeout(r.Context(), privacyRealRequestTimeout)
        stop := context.AfterFunc(suite, cancel)
        defer func() { stop(); cancel() }()
        r = r.WithContext(ctx)
        body, err := io.ReadAll(io.LimitReader(r.Body, privacyRealEvidenceLimit+1))
        r.Body.Close()
        if err != nil || len(body) > privacyRealEvidenceLimit || privacyObserverCalibrate(cfg, tc, r, body) != nil {
            o.fail("B_CALIBRATION_FAILED")
            http.Error(w, "B_CALIBRATION_FAILED", http.StatusBadGateway)
            return
        }
        o.mu.Lock()
        o.b = bytes.Clone(body)
        o.mu.Unlock()
        r.Body = io.NopCloser(bytes.NewReader(body))
        r.ContentLength = int64(len(body))
        r.Header.Set("Content-Length", strconv.Itoa(len(body)))
        proxy.ServeHTTP(w, r)
    }))
    t.Cleanup(func() {
        o.server.CloseClientConnections()
        o.server.Close()
        if idle, ok := next.(interface{ CloseIdleConnections() }); ok {
            idle.CloseIdleConnections()
        }
    })
    return o
}

// 独立的原生计数投影, 保留标准缓存明细以核对客户端和本地费用.
// 不调用 RawUsageCapture, passthroughUsage, 适配器转换或正文 token 估算.
type privacyObserverUsage struct {
    Input, Output, Total, CacheRead, CacheWrite, CacheWrite5m, CacheWrite1h, Reasoning int64
}

type privacyObserverNativeResult struct {
    value              string
    usage              privacyObserverUsage
    complete, outputOK bool
}

// C 的机械恢复和模型严格保持分别判定. 缺 usage 不得伪装成模型 marker 失败.
func privacyObserverVerdict(tc privacyRealCase, upstream, client privacyObserverNativeResult) (bool, bool) {
    want := upstream.value
    if tc.enabled && (!tc.tool || tc.restoreTool) {
        want = strings.NewReplacer(privacyRealUserMarker, privacyRealUser, privacyRelayMarker, privacyRelaySource).Replace(want)
    }
    mechanical := upstream.outputOK && client.outputOK && upstream.complete && client.complete && client.value == want
    strictValue := privacyRealValue
    if tc.enabled {
        strictValue = privacyRealMarkedValue
    }
    strict := upstream.outputOK && upstream.complete && upstream.value == strictValue
    return mechanical, strict
}

func privacyObserverMergeUsage(dst map[string]int64, value any) error {
    if value == nil {
        return nil
    }
    u := privacyObserverMap(value)
    if u == nil {
        return errors.New("NATIVE_USAGE_INVALID")
    }
    // 明确投影路径, 嵌套值只有缓存/推理标准字段参与计数.
    fields := []string{"prompt_tokens", "completion_tokens", "total_tokens", "input_tokens", "output_tokens",
        "cache_read_input_tokens", "cache_creation_input_tokens", "prompt_tokens_details.cached_tokens",
        "prompt_tokens_details.write_cached_tokens", "completion_tokens_details.reasoning_tokens",
        "input_tokens_details.cached_tokens", "input_tokens_details.cache_write_tokens", "output_tokens_details.reasoning_tokens",
        "cache_creation.ephemeral_5m_input_tokens", "cache_creation.ephemeral_1h_input_tokens"}
    for _, path := range fields {
        var value any = u
        for _, part := range strings.Split(path, ".") {
            value = privacyObserverMap(value)[part]
        }
        if value == nil {
            continue
        }
        number, ok := value.(json.Number)
        if !ok {
            return errors.New("NATIVE_USAGE_INVALID")
        }
        n, err := number.Int64()
        if err != nil || n < 0 {
            return errors.New("NATIVE_USAGE_INVALID")
        }
        dst[path] = n
    }
    return nil
}

func privacyObserverNormalize(format llm.APIFormat, counters map[string]int64) (privacyObserverUsage, error) {
    var u privacyObserverUsage
    input, output := "input_tokens", "output_tokens"
    if format == llm.APIFormatOpenAIChatCompletion {
        input, output = "prompt_tokens", "completion_tokens"
        u.CacheRead = counters["prompt_tokens_details.cached_tokens"]
        u.CacheWrite = counters["prompt_tokens_details.write_cached_tokens"]
        u.Reasoning = counters["completion_tokens_details.reasoning_tokens"]
    } else if format == llm.APIFormatOpenAIResponse {
        u.CacheRead = counters["input_tokens_details.cached_tokens"]
        u.CacheWrite = counters["input_tokens_details.cache_write_tokens"]
        u.Reasoning = counters["output_tokens_details.reasoning_tokens"]
    } else if format == llm.APIFormatAnthropicMessage {
        u.CacheRead = counters["cache_read_input_tokens"]
        u.CacheWrite = counters["cache_creation_input_tokens"]
        u.CacheWrite5m = counters["cache_creation.ephemeral_5m_input_tokens"]
        u.CacheWrite1h = counters["cache_creation.ephemeral_1h_input_tokens"]
    } else {
        return u, errors.New("NATIVE_FORMAT_INVALID")
    }
    var inputOK, outputOK bool
    u.Input, inputOK = counters[input]
    u.Output, outputOK = counters[output]
    if !inputOK || !outputOK {
        return u, errors.New("NATIVE_USAGE_MISSING")
    }
    if format == llm.APIFormatAnthropicMessage {
        if u.Input > math.MaxInt64-u.CacheRead || u.Input+u.CacheRead > math.MaxInt64-u.CacheWrite {
            return u, errors.New("NATIVE_USAGE_OVERFLOW")
        }
        u.Input += u.CacheRead + u.CacheWrite
    }
    if u.Input > math.MaxInt64-u.Output {
        return u, errors.New("NATIVE_USAGE_OVERFLOW")
    }
    u.Total = u.Input + u.Output
    if total, ok := counters["total_tokens"]; ok && format != llm.APIFormatAnthropicMessage {
        u.Total = total
    }
    return u, nil
}

func privacyObserverToolValue(args any) (string, error) {
    if s, ok := args.(string); ok {
        root, err := privacyObserverObject([]byte(s))
        if err != nil {
            return "", errors.New("NATIVE_TOOL_ARGUMENTS_INVALID")
        }
        args = root
    }
    root := privacyObserverMap(args)
    value, ok := root["value"].(string)
    if !ok || len(root) != 1 {
        return "", errors.New("NATIVE_TOOL_ARGUMENTS_INVALID")
    }
    return value, nil
}

func privacyObserverOutput(format llm.APIFormat, tool bool, root map[string]any) (string, bool, error) {
    var text strings.Builder
    var calls []map[string]any
    complete := false
    switch format {
    case llm.APIFormatOpenAIChatCompletion:
        choices := privacyObserverArray(root["choices"])
        if len(choices) != 1 {
            return "", false, errors.New("NATIVE_CHOICE_CARDINALITY_INVALID")
        }
        choice := privacyObserverMap(choices[0])
        finish := privacyObserverString(choice["finish_reason"])
        complete = finish == "stop" || finish == "tool_calls"
        message := privacyObserverMap(choice["message"])
        text.WriteString(privacyObserverString(message["content"]))
        for _, item := range privacyObserverArray(message["tool_calls"]) {
            calls = append(calls, privacyObserverMap(privacyObserverMap(item)["function"]))
        }
    case llm.APIFormatOpenAIResponse:
        complete = root["status"] == "completed"
        for _, item := range privacyObserverArray(root["output"]) {
            m := privacyObserverMap(item)
            if m["type"] == "function_call" {
                calls = append(calls, m)
            }
            if m["type"] == "message" {
                for _, part := range privacyObserverArray(m["content"]) {
                    p := privacyObserverMap(part)
                    if p["type"] == "output_text" {
                        text.WriteString(privacyObserverString(p["text"]))
                    }
                }
            }
        }
    case llm.APIFormatAnthropicMessage:
        complete = root["stop_reason"] == "end_turn" || root["stop_reason"] == "tool_use"
        for _, part := range privacyObserverArray(root["content"]) {
            p := privacyObserverMap(part)
            if p["type"] == "text" {
                text.WriteString(privacyObserverString(p["text"]))
            } else if p["type"] == "tool_use" {
                calls = append(calls, map[string]any{"name": p["name"], "arguments": p["input"]})
            }
        }
    default:
        return "", false, errors.New("NATIVE_FORMAT_INVALID")
    }
    if tool {
        if len(calls) != 1 || calls[0]["name"] != "privacy_echo" || text.Len() != 0 {
            return "", complete, errors.New("NATIVE_FORCED_TOOL_NOT_PRESERVED")
        }
        value, err := privacyObserverToolValue(calls[0]["arguments"])
        return value, complete, err
    }
    if len(calls) != 0 {
        return "", complete, errors.New("NATIVE_UNEXPECTED_TOOL")
    }
    return text.String(), complete, nil
}

// 独立 SSE 语法读取器, 忽略注释心跳, 保留 event 类型优先级和多行 data.
func privacyObserverEvents(body []byte) ([]privacyRelayFrame, error) {
    scanner := bufio.NewScanner(bytes.NewReader(body))
    scanner.Buffer(make([]byte, 4096), privacyRealEvidenceLimit)
    var events []privacyRelayFrame
    var event privacyRelayFrame
    var data []string
    flush := func() {
        if len(data) != 0 {
            event.data = strings.Join(data, "\n")
            events = append(events, event)
        }
        event, data = privacyRelayFrame{}, nil
    }
    for scanner.Scan() {
        line := scanner.Text()
        if line == "" {
            flush()
            continue
        }
        if strings.HasPrefix(line, ":") {
            continue
        }
        key, value, _ := strings.Cut(line, ":")
        value = strings.TrimPrefix(value, " ")
        switch key {
        case "event":
            event.kind = value
        case "id":
            event.id = value
        case "data":
            data = append(data, value)
        }
    }
    if scanner.Err() != nil || len(data) != 0 {
        return nil, errors.New("NATIVE_SSE_TRUNCATED")
    }
    return events, nil
}

// 终态 usage 必须是当前事件实际携带的非 null 对象及必需计数字段.
// 不允许从 start/created 或较早累计快照补齐终态缺失字段, 显式零值仍合法.
func privacyObserverTerminalUsage(value any, required ...string) (map[string]int64, error) {
    u := privacyObserverMap(value)
    if u == nil {
        return nil, errors.New("NATIVE_TERMINAL_USAGE_MISSING")
    }
    for _, field := range required {
        if u[field] == nil {
            return nil, errors.New("NATIVE_TERMINAL_USAGE_MISSING")
        }
    }
    counters := map[string]int64{}
    if err := privacyObserverMergeUsage(counters, u); err != nil {
        return nil, err
    }
    for _, field := range required {
        if _, present := counters[field]; !present {
            return nil, errors.New("NATIVE_TERMINAL_USAGE_MISSING")
        }
    }
    return counters, nil
}

func privacyObserverNative(format llm.APIFormat, stream, tool bool, body []byte) (privacyObserverNativeResult, error) {
    var result privacyObserverNativeResult
    counters := map[string]int64{}
    if !stream {
        root, err := privacyObserverObject(body)
        if err != nil {
            return result, err
        }
        if root["error"] != nil || root["type"] == "error" {
            return result, errors.New("NATIVE_UPSTREAM_ERROR")
        }
        result.value, result.complete, err = privacyObserverOutput(format, tool, root)
        if err != nil {
            return result, err
        }
        result.outputOK = true
        if !result.complete {
            return result, errors.New("NATIVE_TERMINAL_USAGE_MISSING")
        }
        input, output := "input_tokens", "output_tokens"
        if format == llm.APIFormatOpenAIChatCompletion {
            input, output = "prompt_tokens", "completion_tokens"
        }
        counters, err = privacyObserverTerminalUsage(root["usage"], input, output)
        if err != nil {
            return result, err
        }
        result.usage, err = privacyObserverNormalize(format, counters)
        return result, err
    }
    events, err := privacyObserverEvents(body)
    if err != nil {
        return result, err
    }
    var text, arguments strings.Builder
    toolName, toolID := "", ""
    toolCount := 0
    finished, done := false, false
    var terminal map[string]any
    var terminalUsageErr error = errors.New("NATIVE_TERMINAL_USAGE_MISSING")
    for _, event := range events {
        if event.data == "[DONE]" {
            done = true
            continue
        }
        root, err := privacyObserverObject([]byte(event.data))
        if err != nil {
            return result, err
        }
        kind := event.kind
        if kind == "" || kind == "message" {
            kind = privacyObserverString(root["type"])
        }
        if kind == "error" || root["error"] != nil {
            return result, errors.New("NATIVE_UPSTREAM_ERROR")
        }
        switch format {
        case llm.APIFormatOpenAIChatCompletion:
            choices := privacyObserverArray(root["choices"])
            finishEvent := false
            for _, item := range choices {
                choice := privacyObserverMap(item)
                if index, ok := choice["index"]; ok && index != json.Number("0") {
                    return result, errors.New("NATIVE_STREAM_IDENTITY_INVALID")
                }
                delta := privacyObserverMap(choice["delta"])
                text.WriteString(privacyObserverString(delta["content"]))
                for _, call := range privacyObserverArray(delta["tool_calls"]) {
                    m := privacyObserverMap(call)
                    if index, ok := m["index"]; ok && index != json.Number("0") {
                        return result, errors.New("NATIVE_STREAM_IDENTITY_INVALID")
                    }
                    fn := privacyObserverMap(m["function"])
                    if id := privacyObserverString(m["id"]); id != "" {
                        if toolID != "" && toolID != id {
                            return result, errors.New("NATIVE_STREAM_IDENTITY_INVALID")
                        }
                        if toolID == "" {
                            toolCount++
                            toolID = id
                        }
                    }
                    if name := privacyObserverString(fn["name"]); name != "" {
                        toolName += name
                    }
                    arguments.WriteString(privacyObserverString(fn["arguments"]))
                }
                finish := privacyObserverString(choice["finish_reason"])
                if finish == "stop" || finish == "tool_calls" {
                    finished, finishEvent = true, true
                }
            }
            // Chat 用 finish 事件或其后的空 choices usage 终帧, 不采用正文 delta 的早期计数.
            // 部分上游把 usage 放在 finish 之后的非空 choices 块 (仅空 delta); 只接受 finish 之后的 usage, 不取早期计数.
            if !done && finished && (finishEvent || len(choices) == 0 || root["usage"] != nil) {
                counters, terminalUsageErr = privacyObserverTerminalUsage(root["usage"], "prompt_tokens", "completion_tokens")
            }
        case llm.APIFormatOpenAIResponse:
            switch kind {
            case "response.output_item.added":
                item := privacyObserverMap(root["item"])
                if item["type"] == "function_call" {
                    toolCount++
                    toolID = privacyObserverString(item["id"])
                    toolName = privacyObserverString(item["name"])
                    arguments.WriteString(privacyObserverString(item["arguments"]))
                }
            case "response.output_text.delta":
                text.WriteString(privacyObserverString(root["delta"]))
            case "response.function_call_arguments.delta":
                if toolID == "" || root["item_id"] != toolID {
                    return result, errors.New("NATIVE_STREAM_IDENTITY_INVALID")
                }
                arguments.WriteString(privacyObserverString(root["delta"]))
            case "response.completed":
                terminal = privacyObserverMap(root["response"])
                finished = terminal["status"] == "completed"
                done = true
                counters, terminalUsageErr = privacyObserverTerminalUsage(terminal["usage"], "input_tokens", "output_tokens")
            case "response.failed", "response.cancelled":
                return result, errors.New("NATIVE_UPSTREAM_ERROR")
            }
        case llm.APIFormatAnthropicMessage:
            switch kind {
            case "message_start":
                if err := privacyObserverMergeUsage(counters, privacyObserverMap(root["message"])["usage"]); err != nil {
                    return result, err
                }
                // start 的 output_tokens=0 只是占位, 永远不是终态输出证据.
                delete(counters, "output_tokens")
            case "content_block_start":
                block := privacyObserverMap(root["content_block"])
                if block["type"] == "tool_use" {
                    toolCount++
                    toolName = privacyObserverString(block["name"])
                    toolID = fmt.Sprint(root["index"])
                    if input := privacyObserverMap(block["input"]); len(input) > 0 {
                        arguments.Write(privacyRelayJSON(input))
                    }
                } else if block["type"] == "text" {
                    text.WriteString(privacyObserverString(block["text"]))
                }
            case "content_block_delta":
                delta := privacyObserverMap(root["delta"])
                if delta["type"] == "text_delta" {
                    text.WriteString(privacyObserverString(delta["text"]))
                }
                if delta["type"] == "input_json_delta" {
                    if toolID == "" || fmt.Sprint(root["index"]) != toolID {
                        return result, errors.New("NATIVE_STREAM_IDENTITY_INVALID")
                    }
                    arguments.WriteString(privacyObserverString(delta["partial_json"]))
                }
            case "message_delta":
                delta := privacyObserverMap(root["delta"])
                finished = delta["stop_reason"] == "end_turn" || delta["stop_reason"] == "tool_use"
                if finished {
                    var last map[string]int64
                    last, terminalUsageErr = privacyObserverTerminalUsage(root["usage"], "output_tokens")
                    delete(counters, "output_tokens")
                    if terminalUsageErr == nil {
                        for field, count := range last {
                            counters[field] = count
                        }
                    }
                }
            case "message_stop":
                done = true
            }
        default:
            return result, errors.New("NATIVE_FORMAT_INVALID")
        }
    }
    result.complete = finished && done
    if tool {
        if toolCount != 1 || toolName != "privacy_echo" || text.Len() != 0 {
            return result, errors.New("NATIVE_FORCED_TOOL_NOT_PRESERVED")
        }
        result.value, err = privacyObserverToolValue(arguments.String())
        if err != nil {
            return result, err
        }
    } else {
        if toolCount != 0 {
            return result, errors.New("NATIVE_UNEXPECTED_TOOL")
        }
        result.value = text.String()
    }
    // Responses 同时携带增量和终态, 两份输出不能互为补造或重复累加.
    if terminal != nil {
        value, complete, err := privacyObserverOutput(format, tool, terminal)
        if err != nil || value != result.value || !complete {
            return result, errors.New("NATIVE_DELTA_TERMINAL_MISMATCH")
        }
    }
    result.outputOK = true
    if terminalUsageErr != nil {
        return result, terminalUsageErr
    }
    if !result.complete {
        return result, errors.New("NATIVE_TERMINAL_USAGE_MISSING")
    }
    result.usage, err = privacyObserverNormalize(format, counters)
    return result, err
}

// 只将指定 synthetic HTTPS 目标映射到 HTTP 回环服务. 不修改正式 relay 执行器.
// 即使测试配置写错, 下层 DialContext 仍拒绝任何非回环 IP.
type privacyObserverLocalTransport struct {
    base *url.URL
    next *http.Transport
}

func (rt *privacyObserverLocalTransport) RoundTrip(r *http.Request) (*http.Response, error) {
    if r.URL.Scheme != "https" || r.URL.Host != "synthetic.invalid" {
        return nil, errors.New("LOCAL_SYNTHETIC_TARGET_REQUIRED")
    }
    clone := r.Clone(r.Context())
    u := *r.URL
    u.Scheme, u.Host = rt.base.Scheme, rt.base.Host
    clone.URL, clone.Host = &u, u.Host
    clone.GetBody = nil
    clone.Close = true
    return rt.next.RoundTrip(clone)
}

func (rt *privacyObserverLocalTransport) CloseIdleConnections() { rt.next.CloseIdleConnections() }

func privacyObserverSyntheticConfig(t *testing.T, format llm.APIFormat) privacyRealConfig {
    t.Helper()
    // 这里的精确 1 仅存在于纯函数的合成输入, 不设置进程真实门禁.
    cfg, enabled, err := privacyRealEnvironment(func(name string) string {
        return map[string]string{privacyRealGateName: "1", "OCTOPUS_PRIVACY_UPSTREAM_BASE_URL": "https://synthetic.invalid/prefix/v1",
            "OCTOPUS_PRIVACY_UPSTREAM_API_KEY": privacyRelayUpstreamKey, "OCTOPUS_PRIVACY_UPSTREAM_MODEL": "privacy-actual-model",
            "OCTOPUS_PRIVACY_UPSTREAM_FORMAT": string(format)}[name]
    })
    if err != nil || !enabled {
        t.Fatal("synthetic config rejected")
    }
    return cfg
}

func privacyObserverLocalHTTP(t *testing.T, handler http.Handler) (*httptest.Server, *privacyObserverLocalTransport) {
    t.Helper()
    server := httptest.NewServer(handler)
    u, err := url.Parse(server.URL)
    if err != nil {
        t.Fatal("local server URL invalid")
    }
    rt := &privacyObserverLocalTransport{base: u, next: privacyObserverLoopbackTransport()}
    t.Cleanup(func() { server.CloseClientConnections(); server.Close(); rt.CloseIdleConnections() })
    return server, rt
}

// 合成逻辑 delta 必须在 rune 边界切分, 否则 JSON 编码会把半个 UTF-8 字符变为 U+FFFD.
// marker 场景仍在 ASCII marker 内切分, 保留跨事件恢复校准, 不放宽任何 oracle.
func privacyObserverSyntheticPieces(value string) ([]string, error) {
    if !utf8.ValidString(value) {
        return nil, errors.New("SYNTHETIC_UTF8_INVALID")
    }
    runes := []rune(value)
    if len(runes) < 2 {
        return nil, errors.New("SYNTHETIC_DELTA_TOO_SHORT")
    }
    cut := len(runes) / 2
    if index := strings.Index(value, "OCTOPUS"); index >= 0 {
        cut = utf8.RuneCountInString(value[:index]) + 4
    }
    return []string{string(runes[:cut]), string(runes[cut:])}, nil
}

func privacyObserverCalibratedRequest(t *testing.T, cfg privacyRealConfig, tc privacyRealCase, base string) (*http.Request, []byte) {
    t.Helper()
    root, _ := privacyObserverObject(privacyRealRequest(cfg.format, cfg.model, tc))
    if tc.enabled {
        switch cfg.format {
        case llm.APIFormatOpenAIChatCompletion:
            privacyObserverMap(privacyObserverArray(root["messages"])[0])["content"] = privacyRealPrompt(tc, true)
        case llm.APIFormatOpenAIResponse:
            privacyObserverMap(privacyObserverArray(privacyObserverMap(privacyObserverArray(root["input"])[0])["content"])[0])["text"] = privacyRealPrompt(tc, true)
        case llm.APIFormatAnthropicMessage:
            privacyObserverMap(privacyObserverArray(privacyObserverMap(privacyObserverArray(root["messages"])[0])["content"])[0])["text"] = privacyRealPrompt(tc, true)
        }
    }
    body := privacyRelayJSON(root)
    r, err := http.NewRequest(http.MethodPost, base+privacyObserverEndpoint(cfg.format), bytes.NewReader(body))
    if err != nil {
        t.Fatal("synthetic B request invalid")
    }
    r.Header.Set("Content-Type", "application/json")
    r.Header.Set("X-Synthetic-Privacy", privacyRealValue)
    if cfg.format == llm.APIFormatAnthropicMessage {
        r.Header.Set("X-Api-Key", cfg.key)
        r.Header.Set("Anthropic-Version", "2023-06-01")
    } else {
        r.Header.Set("Authorization", "Bearer "+cfg.key)
    }
    return r, body
}

func TestPrivacyObserver(t *testing.T) {
    if !*octopusPrivacyWorker {
        privacyRealWorker(t, "TestPrivacyObserver", false)
        return
    }
    privacyRealInit(t)
    t.Run("Calibration", privacyObserverCalibrationTests)
    t.Run("Budget", privacyObserverBudgetTests)
    t.Run("NativeUsage", privacyObserverNativeTests)
    t.Run("GzipEvidence", func(t *testing.T) {
        // 观测器解析解压后的副本; 实际通过代理的原始响应字节不能因此被改写.
        body := privacyRelayResponse(llm.APIFormatOpenAIChatCompletion, privacyRealValue, false)
        var wire bytes.Buffer
        writer := gzip.NewWriter(&wire)
        if _, err := writer.Write(body); err != nil {
            t.Fatal(err)
        }
        if err := writer.Close(); err != nil {
            t.Fatal(err)
        }
        observer := &privacyObserver{encoding: "gzip", c: bytes.Clone(wire.Bytes()), status: http.StatusOK}
        _, decoded, status, code := observer.evidence()
        if status != http.StatusOK || code != "" || !bytes.Equal(decoded, body) || !bytes.Equal(observer.c, wire.Bytes()) {
            t.Fatal("gzip evidence did not preserve the original response")
        }
        native, err := privacyObserverNative(llm.APIFormatOpenAIChatCompletion, false, false, decoded)
        if err != nil || !native.complete || native.value != privacyRealValue || native.usage.Input != 17 || native.usage.Output != 5 {
            t.Fatal("gzip evidence lost text or native usage")
        }
        observer.c = observer.c[:len(observer.c)-1]
        if _, _, _, code := observer.evidence(); code != "C_GZIP_DECODE_FAILED" {
            t.Fatal("truncated gzip evidence must not count as valid")
        }
    })
    t.Run("ImmediateTeeAndCancel", privacyObserverTeeTests)
    t.Run("NoRedirectRetryAndSystemStop", privacyObserverSystemTests)
    t.Run("GinAnthropicRedirectBlocked", privacyObserverGinRedirectTests)
    t.Run("EncodedTrailingSlash", privacyObserverEncodedPathTests)
    t.Run("TerminalUsagePresence", privacyObserverTerminalUsageTests)
    t.Run("UsageAfterFinishWithChoice", privacyObserverUsageAfterFinishWithChoice)
    t.Run("FormalEightRows", func(t *testing.T) {
        for _, client := range privacyRelayClients {
            t.Run(client.name, func(t *testing.T) {
                cfg := privacyObserverSyntheticConfig(t, client.format)
                var count atomic.Int32
                _, transport := privacyObserverLocalHTTP(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
                    count.Add(1)
                    body, err := io.ReadAll(r.Body)
                    root, parseErr := privacyObserverObject(body)
                    text, textErr := privacyObserverEffectiveText(cfg.format, root)
                    if err != nil || parseErr != nil || textErr != nil || r.URL.Path != "/prefix/v1"+privacyObserverEndpoint(cfg.format) || r.URL.RawQuery != "" {
                        t.Error("local mock received invalid final request")
                        w.WriteHeader(400)
                        return
                    }
                    _, value, found := strings.Cut(text, "Value:\n")
                    if !found {
                        t.Error("local mock missing synthetic value")
                        w.WriteHeader(400)
                        return
                    }
                    tool := len(privacyObserverArray(root["tools"])) != 0
                    stream, _ := root["stream"].(bool)
                    if !stream {
                        w.Header().Set("Content-Type", "application/json")
                        _, _ = w.Write(privacyRelayResponse(cfg.format, value, tool))
                        return
                    }
                    if tool {
                        value = string(privacyRelayJSON(map[string]any{"value": value}))
                    }
                    // 跨标记增量而非只拆 TCP 写入, 使用 pin 原生事件形态.
                    pieces, err := privacyObserverSyntheticPieces(value)
                    if err != nil {
                        t.Error("invalid synthetic logical delta")
                        w.WriteHeader(400)
                        return
                    }
                    w.Header().Set("Content-Type", "text/event-stream")
                    for _, frame := range privacyRelayStreamFrames(cfg.format, pieces, tool, true) {
                        if frame.kind != "" {
                            fmt.Fprintf(w, "event: %s\n", frame.kind)
                        }
                        fmt.Fprintf(w, "data: %s\n\n", frame.data)
                        w.(http.Flusher).Flush()
                    }
                }))
                privacyRealRunProfile(t, cfg, transport)
                if count.Load() != privacyRealMaxCalls {
                    t.Fatal("local profile not exactly eight HTTP calls")
                }
            })
        }
    })
    if !t.Failed() {
        fmt.Println("OCTOPUS_PRIVACY_OBSERVER_WORKER_OK")
    }
}

func privacyObserverCalibrationTests(t *testing.T) {
    for _, client := range privacyRelayClients {
        for _, tc := range privacyRealProfile() {
            t.Run(client.name+"/"+tc.name, func(t *testing.T) {
                cfg := privacyObserverSyntheticConfig(t, client.format)
                // 模型名也含 source, 校准必须保证模型字段排除而不是做全 Body 搜索.
                cfg.model = "synthetic-model-" + privacyRelaySource
                r, body := privacyObserverCalibratedRequest(t, cfg, tc, "http://127.0.0.1")
                if privacyObserverCalibrate(cfg, tc, r, body) != nil {
                    t.Fatal("valid B rejected")
                }
                for _, mutation := range []string{"Model", "Text", "ExcludedMetadata", "Auth", "Idempotency", "Query", "Length", "Schema"} {
                    t.Run(mutation, func(t *testing.T) {
                        bad := r.Clone(t.Context())
                        bad.Header = r.Header.Clone()
                        u := *r.URL
                        bad.URL = &u
                        root, _ := privacyObserverObject(body)
                        switch mutation {
                        case "Model":
                            root["model"] = "wrong-model"
                        case "Text":
                            // 恢复到未处理文本或错误 marker, 两种开关都应拒绝.
                            data := bytes.Replace(body, []byte("Value:"), []byte("WRONG:"), 1)
                            bad.ContentLength = int64(len(data))
                            if privacyObserverCalibrate(cfg, tc, bad, data) == nil {
                                t.Fatal("bad effective text accepted")
                            }
                            return
                        case "ExcludedMetadata":
                            root["metadata"] = map[string]any{"privacy_excluded": privacyRealMarkedValue}
                        case "Auth":
                            bad.Header.Set("Authorization", "Bearer wrong")
                            bad.Header.Set("X-Api-Key", "wrong")
                        case "Idempotency":
                            bad.Header.Set("Idempotency-Key", "synthetic")
                        case "Query":
                            bad.URL.RawQuery = "synthetic=1"
                        case "Length":
                            bad.ContentLength++
                        case "Schema":
                            root["tools"] = []any{map[string]any{"name": "privacy_echo", "parameters": map[string]any{"const": privacyRealMarkedValue}}}
                        }
                        data := privacyRelayJSON(root)
                        if mutation != "Length" {
                            bad.ContentLength = int64(len(data))
                        }
                        if privacyObserverCalibrate(cfg, tc, bad, data) == nil {
                            t.Fatal("unsafe B accepted")
                        }
                    })
                }
            })
        }
    }
    // 校准失败必须真正阻断 HTTP 外发, 不能只在调用完成后报告.
    var calls atomic.Int32
    _, rt := privacyObserverLocalHTTP(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(200) }))
    cfg := privacyObserverSyntheticConfig(t, llm.APIFormatOpenAIChatCompletion)
    tc := privacyRealProfile()[2]
    budget := new(privacyObserverBudget)
    o := privacyObserverNew(t, t.Context(), cfg, tc, budget, rt)
    r, _ := privacyObserverCalibratedRequest(t, cfg, tc, o.server.URL)
    r.Header.Set("Authorization", "Bearer wrong")
    client := &http.Client{Transport: privacyObserverLoopbackTransport(), Timeout: 3 * time.Second}
    defer client.CloseIdleConnections()
    response, err := client.Do(r)
    if err != nil {
        t.Fatal("loopback preflight request failed")
    }
    io.Copy(io.Discard, response.Body)
    response.Body.Close()
    if response.StatusCode != 502 || calls.Load() != 0 || budget.calls.Load() != 0 {
        t.Fatal("B failure did not block external call")
    }
}

func privacyObserverBudgetTests(t *testing.T) {
    budget := new(privacyObserverBudget)
    var workers sync.WaitGroup
    var accepted atomic.Int32
    for i := 0; i < 24; i++ {
        workers.Add(1)
        go func() {
            defer workers.Done()
            if budget.reserve(new(atomic.Int32)) == nil {
                accepted.Add(1)
            }
        }()
    }
    workers.Wait()
    if accepted.Load() != 8 || budget.calls.Load() != 8 || !budget.stopped.Load() {
        t.Fatal("atomic eight-call cap failed")
    }
    budget = new(privacyObserverBudget)
    perCase := new(atomic.Int32)
    if budget.reserve(perCase) != nil || budget.reserve(perCase) == nil || budget.calls.Load() != 1 {
        t.Fatal("same case was counted twice")
    }
}

// 校准上游把 usage 放在 finish 之后、choices 含空 delta 的形态, 避免只接受空 choices 的假设.
func privacyObserverUsageAfterFinishWithChoice(t *testing.T) {
    frames := []privacyRelayFrame{
        {data: `{"choices":[{"index":0,"delta":{"content":"synthetic"}}]}`},
        {data: `{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`},
        {data: `{"choices":[{"index":0,"delta":{}}],"usage":{"prompt_tokens":11,"completion_tokens":3,"total_tokens":14}}`},
        {data: "[DONE]"},
    }
    var body []byte
    for _, frame := range frames {
        body = append(body, []byte("data: "+frame.data+"\n\n")...)
    }
    result, err := privacyObserverNative(llm.APIFormatOpenAIChatCompletion, true, false, body)
    if err != nil || result.usage.Input != 11 || result.usage.Output != 3 {
        t.Fatal("usage after finish with non-empty choices was not read")
    }
}

func privacyObserverNativeTests(t *testing.T) {
    t.Run("SyntheticDeltaUTF8", func(t *testing.T) {
        for _, value := range []string{privacyRealValue, privacyRealMarkedValue, string(privacyRelayJSON(map[string]any{"value": privacyRealValue})), string(privacyRelayJSON(map[string]any{"value": privacyRealMarkedValue}))} {
            pieces, err := privacyObserverSyntheticPieces(value)
            if err != nil || len(pieces) != 2 || strings.Join(pieces, "") != value || !utf8.ValidString(pieces[0]) || !utf8.ValidString(pieces[1]) {
                t.Fatal("synthetic delta changed UTF-8 value")
            }
        }
    })
    for _, client := range privacyRelayClients {
        for _, stream := range []bool{false, true} {
            for _, tool := range []bool{false, true} {
                t.Run(fmt.Sprintf("%s/stream=%t/tool=%t", client.name, stream, tool), func(t *testing.T) {
                    body := privacyRelayResponse(client.format, privacyRealMarkedValue, tool)
                    if stream {
                        value := privacyRealMarkedValue
                        if tool {
                            value = string(privacyRelayJSON(map[string]any{"value": value}))
                        }
                        var wire strings.Builder
                        pieces, err := privacyObserverSyntheticPieces(value)
                        if err != nil {
                            t.Fatal("invalid synthetic native deltas")
                        }
                        for _, event := range privacyRelayStreamFrames(client.format, pieces, tool, true) {
                            if event.kind != "" {
                                fmt.Fprintf(&wire, "event: %s\n", event.kind)
                            }
                            fmt.Fprintf(&wire, "data: %s\n\n", event.data)
                        }
                        body = []byte(wire.String())
                    }
                    result, err := privacyObserverNative(client.format, stream, tool, body)
                    if err != nil || result.value != privacyRealMarkedValue || !result.complete || result.usage.Input != 17 || result.usage.Output != 5 || result.usage.Total != 22 {
                        t.Fatal("independent native evidence failed")
                    }
                    if client.format == llm.APIFormatAnthropicMessage && (result.usage.CacheRead != 4 || result.usage.CacheWrite != 2) {
                        t.Fatal("native cache normalization failed")
                    }
                })
            }
        }
    }
    for index, body := range []string{`{"usage":null}`, `{"usage":{"prompt_tokens":1.5,"completion_tokens":2}}`, `{"usage":{"prompt_tokens":"1","completion_tokens":2}}`, `{"usage":{"prompt_tokens":-1,"completion_tokens":2}}`} {
        t.Run("BadUsage_"+strconv.Itoa(index), func(t *testing.T) {
            root, _ := privacyObserverObject([]byte(body))
            counters := map[string]int64{}
            err := privacyObserverMergeUsage(counters, root["usage"])
            if err == nil {
                _, err = privacyObserverNormalize(llm.APIFormatOpenAIChatCompletion, counters)
            }
            if err == nil {
                t.Fatal("missing or malformed usage accepted")
            }
        })
    }
    // 累计快照覆盖而非相加, 显式零值不能被先前计数覆盖.
    counters := map[string]int64{}
    for _, body := range []string{`{"prompt_tokens":10,"completion_tokens":1}`, `{"prompt_tokens":10,"completion_tokens":7}`, `{"completion_tokens":0}`} {
        u, _ := privacyObserverObject([]byte(body))
        if privacyObserverMergeUsage(counters, u) != nil {
            t.Fatal("cumulative usage rejected")
        }
    }
    u, err := privacyObserverNormalize(llm.APIFormatOpenAIChatCompletion, counters)
    if err != nil || u.Input != 10 || u.Output != 0 {
        t.Fatal("usage was accumulated or zero lost")
    }
    if _, err := privacyObserverEvents([]byte("data: {}\n")); err == nil {
        t.Fatal("truncated SSE accepted")
    }
    for _, tc := range privacyRealProfile() {
        upstream := privacyObserverNativeResult{value: privacyRealValue, complete: true, outputOK: true}
        if tc.enabled {
            upstream.value = privacyRealMarkedValue
        }
        client := upstream
        if tc.enabled && (!tc.tool || tc.restoreTool) {
            client.value = privacyRealValue
        }
        mechanical, strict := privacyObserverVerdict(tc, upstream, client)
        if !mechanical || !strict {
            t.Fatal("valid C/D evidence rejected")
        }
        upstream.value, client.value = "synthetic model omitted marker", "synthetic model omitted marker"
        mechanical, strict = privacyObserverVerdict(tc, upstream, client)
        if !mechanical || strict {
            t.Fatal("model marker loss was not a strict failure independent of mechanical restoration")
        }
        client.value = privacyRealValue
        mechanical, _ = privacyObserverVerdict(tc, upstream, client)
        if mechanical {
            t.Fatal("D invented original source without marker evidence")
        }
    }
    if _, err := privacyObserverNative(llm.APIFormatAnthropicMessage, true, false, []byte("event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"overloaded_error\"}}\n\n")); err == nil || err.Error() != "NATIVE_UPSTREAM_ERROR" {
        t.Fatal("systemic native SSE error not detected")
    }
}

func privacyObserverTeeTests(t *testing.T) {
    cfg := privacyObserverSyntheticConfig(t, llm.APIFormatOpenAIChatCompletion)
    tc := privacyRealProfile()[3]
    for _, cancelled := range []bool{false, true} {
        t.Run(fmt.Sprintf("cancel=%t", cancelled), func(t *testing.T) {
            first := "data: {\"synthetic\":\"first\"}\n\n"
            last := "data: [DONE]\n\n"
            release, finished, cancelObserved := make(chan struct{}), make(chan struct{}), make(chan struct{})
            var once sync.Once
            unblock := func() { once.Do(func() { close(release) }) }
            t.Cleanup(unblock)
            _, rt := privacyObserverLocalHTTP(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
                defer close(finished)
                // net/http 只在请求 Body 消耗到 EOF 后启动断连 background read.
                // 不读合成请求会令本地夹具无法观测客户端取消, 不能用延长等待掩盖.
                if _, err := io.Copy(io.Discard, r.Body); err != nil {
                    t.Error("synthetic tee request body read failed")
                    return
                }
                r.Body.Close()
                w.Header().Set("Content-Type", "text/event-stream")
                io.WriteString(w, first)
                w.(http.Flusher).Flush()
                select {
                case <-release:
                    io.WriteString(w, last)
                case <-r.Context().Done():
                    close(cancelObserved)
                }
            }))
            ctx, cancel := context.WithCancel(t.Context())
            defer cancel()
            o := privacyObserverNew(t, ctx, cfg, tc, new(privacyObserverBudget), rt)
            r, _ := privacyObserverCalibratedRequest(t, cfg, tc, o.server.URL)
            r = r.WithContext(ctx)
            client := &http.Client{Transport: privacyObserverLoopbackTransport(), Timeout: 3 * time.Second}
            defer client.CloseIdleConnections()
            response, err := client.Do(r)
            if err != nil {
                t.Fatal("loopback tee request failed")
            }
            reader := bufio.NewReader(response.Body)
            line, err := reader.ReadString('\n')
            if err != nil || line != strings.Split(first, "\n")[0]+"\n" {
                response.Body.Close()
                t.Fatal("first SSE data was buffered until stream completion")
            }
            _, evidence, _, _ := o.evidence()
            if !bytes.Contains(evidence, []byte("first")) {
                response.Body.Close()
                t.Fatal("C was not teed immediately")
            }
            if cancelled {
                cancel()
                response.Body.Close()
                select {
                case <-cancelObserved:
                case <-time.After(3 * time.Second):
                    unblock()
                    t.Fatal("upstream did not observe cancellation of actual HTTP connection")
                }
            } else {
                unblock()
                rest, err := io.ReadAll(reader)
                response.Body.Close()
                if err != nil || line+string(rest) != first+last {
                    t.Fatal("observer rewrote SSE bytes")
                }
            }
            select {
            case <-finished:
            case <-time.After(3 * time.Second):
                unblock()
                t.Fatal("upstream goroutine did not finish after EOF or cancellation")
            }
        })
    }
}

func privacyObserverSystemTests(t *testing.T) {
    cfg := privacyObserverSyntheticConfig(t, llm.APIFormatOpenAIChatCompletion)
    tc := privacyRealProfile()[0]
    for _, status := range []int{200, 300, 301, 302, 303, 304, 307, 308, 399, 401, 429} {
        t.Run(strconv.Itoa(status), func(t *testing.T) {
            var count atomic.Int32
            _, rt := privacyObserverLocalHTTP(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
                count.Add(1)
                if status >= 300 && status < 400 {
                    w.Header().Set("Location", "https://synthetic.invalid/must-not-follow")
                }
                w.WriteHeader(status)
                io.WriteString(w, "synthetic-response")
            }))
            budget := new(privacyObserverBudget)
            o := privacyObserverNew(t, t.Context(), cfg, tc, budget, rt)
            client := &http.Client{Transport: privacyObserverLoopbackTransport(), Timeout: 3 * time.Second,
                CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
            defer client.CloseIdleConnections()
            for i := 0; i < 2; i++ {
                r, _ := privacyObserverCalibratedRequest(t, cfg, tc, o.server.URL)
                response, err := client.Do(r)
                if err != nil {
                    t.Fatal("loopback system test failed")
                }
                body, err := io.ReadAll(response.Body)
                response.Body.Close()
                wantStatus, wantBody := status, "synthetic-response"
                if i == 1 || (status >= 300 && status < 400) {
                    wantStatus, wantBody = 502, "OBSERVER_FORWARD_FAILED\n"
                }
                if err != nil || response.StatusCode != wantStatus || string(body) != wantBody || response.Header.Get("Location") != "" {
                    t.Fatal("unsafe redirect response or repeated external call")
                }
            }
            if count.Load() != 1 || budget.calls.Load() != 1 {
                t.Fatal("redirect, retry, or duplicate escaped budget")
            }
        })
    }
    // 实际回环 TCP 连接被关闭, 不用 stub Transport 伪造网络失败.
    var count atomic.Int32
    _, rt := privacyObserverLocalHTTP(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        count.Add(1)
        conn, _, err := w.(http.Hijacker).Hijack()
        if err == nil {
            conn.Close()
        }
    }))
    budget := new(privacyObserverBudget)
    o := privacyObserverNew(t, t.Context(), cfg, tc, budget, rt)
    client := &http.Client{Transport: privacyObserverLoopbackTransport(), Timeout: 3 * time.Second}
    defer client.CloseIdleConnections()
    r, _ := privacyObserverCalibratedRequest(t, cfg, tc, o.server.URL)
    response, err := client.Do(r)
    if err != nil {
        t.Fatal("local network failure was not returned by proxy")
    }
    io.Copy(io.Discard, response.Body)
    response.Body.Close()
    if response.StatusCode != 502 || count.Load() != 1 || budget.calls.Load() != 1 || !budget.stopped.Load() {
        t.Fatal("transparent retry or missing system stop")
    }
}

// 走正式 Gin 鉴权和 relay/pipeline, 不给生产出站 http.Client 注入 CheckRedirect.
// 恶意 Location 仅指向 HTTP 回环服务, 旧漏洞也不可能触及外网或真实 Key.
func privacyObserverGinRedirectTests(t *testing.T) {
    for _, status := range []int{301, 302, 303, 307, 308} {
        t.Run(strconv.Itoa(status), func(t *testing.T) {
            cfg := privacyObserverSyntheticConfig(t, llm.APIFormatAnthropicMessage)
            tc := privacyRealProfile()[0]
            var originCalls, evilCalls atomic.Int32
            var evilSawKey atomic.Bool
            evil, _ := privacyObserverLocalHTTP(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
                evilCalls.Add(1)
                if r.Header.Get("X-Api-Key") == cfg.key {
                    evilSawKey.Store(true)
                }
                io.Copy(io.Discard, r.Body)
                r.Body.Close()
                w.Header().Set("Content-Type", "application/json")
                w.Write(privacyRelayResponse(cfg.format, privacyRealValue, false))
            }))
            _, transport := privacyObserverLocalHTTP(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
                originCalls.Add(1)
                io.Copy(io.Discard, r.Body)
                r.Body.Close()
                if r.Method != http.MethodPost || r.Header.Get("X-Api-Key") != cfg.key || r.Header.Get("Authorization") != "" {
                    t.Error("formal Anthropic outbound authentication was not calibrated")
                }
                w.Header().Set("Location", evil.URL+"/synthetic-key-sink")
                w.WriteHeader(status)
                io.WriteString(w, "synthetic redirect body")
            }))
            ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
            defer cancel()
            budget := new(privacyObserverBudget)
            observer := privacyObserverNew(t, ctx, cfg, tc, budget, transport)
            h := privacyRealFixture(t, cfg, tc, observer)
            // 客户端也使用默认重定向行为; 安全必须由 observer 在 relay 收到 3xx 前保证.
            h.client.CheckRedirect = nil
            body := privacyRealRequest(cfg.format, h.requestModel(false), tc)
            r, err := http.NewRequestWithContext(ctx, http.MethodPost, h.entry.URL+privacyRelayPath(cfg.format), bytes.NewReader(body))
            if err != nil {
                t.Fatal("local Gin redirect request build failed")
            }
            r.Header.Set("Content-Type", "application/json")
            r.Header.Set("Authorization", "Bearer "+h.key.APIKey)
            r.Header.Set("X-Synthetic-Privacy", privacyRealValue)
            response, err := h.client.Do(r)
            if err != nil {
                t.Fatal("local Gin redirect request failed")
            }
            data, readErr := io.ReadAll(response.Body)
            response.Body.Close()
            privacyRealWaitHandler(t, h)
            _, c, observedStatus, code := observer.evidence()
            if readErr != nil || response.StatusCode != 502 || response.Header.Get("Location") != "" ||
                bytes.Contains(data, []byte(evil.URL)) || bytes.Contains(data, []byte(cfg.key)) {
                t.Fatal("formal relay received or followed an unsafe redirect response")
            }
            if originCalls.Load() != 1 || evilCalls.Load() != 0 || evilSawKey.Load() || budget.calls.Load() != 1 ||
                observer.perCase.Load() != 1 || !budget.stopped.Load() || observedStatus != status ||
                code != "SYSTEM_UPSTREAM_REDIRECT_BLOCKED" || len(c) != 0 {
                t.Fatal("redirect escaped the one-call budget or exposed synthetic authentication")
            }
            rows := h.logs()
            if len(rows) != 1 || rows[0].TotalAttempts != 1 || rows[0].ChannelId != h.channel.ID || rows[0].Error == "" {
                t.Fatal("formal redirect failure was not audited as one attempt")
            }
        })
    }
}

func privacyObserverEncodedPathTests(t *testing.T) {
    for _, client := range privacyRelayClients {
        for index, prefix := range []string{"/tenant%2F", "/tenant%2f/", "/tenant%252F/", "/tenant%20name/", "/tenant///"} {
            t.Run(client.name+"/"+strconv.Itoa(index), func(t *testing.T) {
                cfg := privacyObserverSyntheticConfig(t, client.format)
                base, err := url.Parse("https://synthetic.invalid" + prefix)
                if err != nil {
                    t.Fatal("synthetic encoded prefix was invalid")
                }
                cfg.base = base
                wantEscaped := strings.TrimRight(prefix, "/") + privacyObserverEndpoint(cfg.format)
                wantPath, err := url.PathUnescape(wantEscaped)
                if err != nil {
                    t.Fatal("synthetic encoded expected path was invalid")
                }
                target, err := privacyObserverTarget(cfg)
                if err != nil || target.Path != wantPath || target.RawPath != wantEscaped || target.EscapedPath() != wantEscaped {
                    t.Fatal("target Path/RawPath lost the encoded trailing slash")
                }
                var calls atomic.Int32
                _, transport := privacyObserverLocalHTTP(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
                    calls.Add(1)
                    io.Copy(io.Discard, r.Body)
                    r.Body.Close()
                    // 验证实际 HTTP request-target, 不只比较内存中的 URL.String.
                    if r.RequestURI != wantEscaped || r.URL.Path != wantPath || r.URL.EscapedPath() != wantEscaped {
                        t.Error("wire request-target changed the encoded prefix")
                    }
                    w.Header().Set("Content-Type", "application/json")
                    w.Write(privacyRelayResponse(cfg.format, privacyRealMarkedValue, false))
                }))
                budget := new(privacyObserverBudget)
                if privacyRealRunCase(t, t.Context(), cfg, privacyRealProfile()[2], budget, transport) || calls.Load() != 1 || budget.calls.Load() != 1 {
                    t.Fatal("encoded prefix did not reach formal Gin pipeline exactly once")
                }
            })
        }
    }
}

func privacyObserverFrameWire(frames []privacyRelayFrame) []byte {
    var wire strings.Builder
    for _, frame := range frames {
        if frame.kind != "" {
            fmt.Fprintf(&wire, "event: %s\n", frame.kind)
        }
        if frame.id != "" {
            fmt.Fprintf(&wire, "id: %s\n", frame.id)
        }
        fmt.Fprintf(&wire, "data: %s\n\n", frame.data)
    }
    return []byte(wire.String())
}

// 先放入完整的早期计数, 再破坏终态 usage; 缺失字段不能靠较早事件补齐.
func privacyObserverTerminalUsageTests(t *testing.T) {
    for _, client := range privacyRelayClients {
        for _, tool := range []bool{false, true} {
            mutations := []string{"Missing", "Null", "Empty", "OutputMissing", "OutputNull", "OutputString", "NoTerminalEvent", "Zero"}
            if client.format != llm.APIFormatAnthropicMessage {
                mutations = append(mutations, "InputMissing", "InputNull")
            }
            for _, mutation := range mutations {
                t.Run(fmt.Sprintf("%s/tool=%t/%s", client.name, tool, mutation), func(t *testing.T) {
                    value := privacyRealMarkedValue
                    if tool {
                        value = string(privacyRelayJSON(map[string]any{"value": value}))
                    }
                    pieces, err := privacyObserverSyntheticPieces(value)
                    if err != nil {
                        t.Fatal("terminal usage synthetic deltas were invalid")
                    }
                    frames := privacyRelayStreamFrames(client.format, pieces, tool, true)
                    var changed []privacyRelayFrame
                    for i, frame := range frames {
                        if frame.data == "[DONE]" {
                            changed = append(changed, frame)
                            continue
                        }
                        root, err := privacyObserverObject([]byte(frame.data))
                        if err != nil {
                            t.Fatal("terminal usage synthetic event was invalid")
                        }
                        var terminal map[string]any
                        input, output := "input_tokens", "output_tokens"
                        switch client.format {
                        case llm.APIFormatOpenAIChatCompletion:
                            input, output = "prompt_tokens", "completion_tokens"
                            if i == 0 {
                                root["usage"] = map[string]any{input: 17, output: 5, "total_tokens": 22}
                            }
                            for _, choice := range privacyObserverArray(root["choices"]) {
                                if privacyObserverString(privacyObserverMap(choice)["finish_reason"]) != "" {
                                    terminal = root
                                }
                            }
                        case llm.APIFormatOpenAIResponse:
                            if root["type"] == "response.created" {
                                privacyObserverMap(root["response"])["usage"] = map[string]any{input: 17, output: 5, "total_tokens": 22}
                            }
                            if root["type"] == "response.completed" {
                                terminal = privacyObserverMap(root["response"])
                            }
                        case llm.APIFormatAnthropicMessage:
                            if root["type"] == "message_start" {
                                privacyObserverMap(privacyObserverMap(root["message"])["usage"])["output_tokens"] = 0
                            }
                            if root["type"] == "message_delta" {
                                // 非终态 delta 也携带 output=5, 仍不能替代实际 stop delta 的计数.
                                changed = append(changed, privacyRelayFrame{kind: "message_delta", data: `{"type":"message_delta","delta":{},"usage":{"output_tokens":5}}`})
                                terminal = root
                            }
                        }
                        if terminal != nil {
                            usage := privacyObserverMap(terminal["usage"])
                            switch mutation {
                            case "Missing":
                                delete(terminal, "usage")
                            case "Null":
                                terminal["usage"] = nil
                            case "Empty":
                                terminal["usage"] = map[string]any{}
                            case "OutputMissing":
                                delete(usage, output)
                            case "OutputNull":
                                usage[output] = nil
                            case "OutputString":
                                usage[output] = "0"
                            case "InputMissing":
                                delete(usage, input)
                            case "InputNull":
                                usage[input] = nil
                            case "NoTerminalEvent":
                                continue
                            case "Zero":
                                usage[output] = 0
                                if client.format != llm.APIFormatAnthropicMessage {
                                    usage["total_tokens"] = 17
                                }
                            }
                        }
                        frame.data = string(privacyRelayJSON(root))
                        changed = append(changed, frame)
                    }
                    result, err := privacyObserverNative(client.format, true, tool, privacyObserverFrameWire(changed))
                    if mutation == "Zero" {
                        if err != nil || !result.complete || result.usage.Input != 17 || result.usage.Output != 0 || result.usage.Total != 17 {
                            t.Fatal("explicit terminal zero was rejected or replaced by earlier usage")
                        }
                    } else {
                        wantError := "NATIVE_TERMINAL_USAGE_MISSING"
                        if mutation == "OutputString" {
                            wantError = "NATIVE_USAGE_INVALID"
                        }
                        if err == nil || err.Error() != wantError {
                            t.Fatal("missing, null, or nonterminal usage was not rejected by the terminal presence oracle")
                        }
                        if mutation != "NoTerminalEvent" && !result.outputOK {
                            t.Fatal("terminal usage failure incorrectly invalidated independent marker output evidence")
                        }
                    }
                })
            }
        }
    }
}
