package handlers

import (
    "bytes"
    "encoding/json"
    "errors"
    "flag"
    "fmt"
    stdlog "log"
    "net/http"
    "net/http/httptest"
    "os"
    "os/exec"
    "path/filepath"
    "reflect"
    "strconv"
    "strings"
    "testing"
    "time"

    "github.com/bestruirui/octopus/internal/db"
    "github.com/bestruirui/octopus/internal/model"
    "github.com/bestruirui/octopus/internal/op"
    "github.com/bestruirui/octopus/internal/privacy"
    "github.com/bestruirui/octopus/internal/server/auth"
    "github.com/bestruirui/octopus/internal/server/resp"
    "github.com/bestruirui/octopus/internal/server/router"
    "github.com/gin-gonic/gin"
    "gorm.io/gorm"
    "gorm.io/gorm/logger"
)

// 每次调用使用新进程: RegisterAll 和迁移会消耗各自注册表,
// op 缓存不能超出隔离数据库的生命周期. 本包无需绕过旧 TestMain 初始化.
var privacyHandlersWorker = flag.Bool("octopus.privacy.worker", false, "run isolated privacy handler assertions")

const (
    privacyHandlerSourceCanary = "privacy_handler_synthetic_source_6A19"
    privacyHandlerAliasCanary  = "PRIVACY_HANDLER_ALIAS_CANARY_6A19"
    privacyHandlersWorkerOK    = "OCTOPUS_PRIVACY_WORKER_OK:TestPrivacyHandlers"
)

func TestPrivacyHandlers(t *testing.T) {
    if !*privacyHandlersWorker {
        executable, err := os.Executable()
        if err != nil {
            t.Fatal("locate compiled test executable:", err)
        }
        timeout := flag.Lookup("test.timeout").Value.String()
        if deadline, ok := t.Deadline(); ok {
            timeout = time.Until(deadline).String()
        }
        cmd := exec.CommandContext(t.Context(), executable,
            "-test.run=^TestPrivacyHandlers$", "-test.count=1", "-test.parallel=1",
            "-test.v="+strconv.FormatBool(testing.Verbose()), "-test.timeout="+timeout,
            "-octopus.privacy.worker=true",
        )
        var stdout, stderr bytes.Buffer
        cmd.Stdout, cmd.Stderr = &stdout, &stderr
        err = cmd.Run()
        for _, secret := range []string{privacyHandlerSourceCanary, privacyHandlerAliasCanary} {
            if strings.Contains(stdout.String(), secret) || strings.Contains(stderr.String(), secret) {
                t.Fatal("privacy fixture leaked into worker output")
            }
        }
        t.Logf("handler worker stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
        if err != nil {
            t.Fatal("handler worker failed:", err)
        }
        if !strings.Contains(stdout.String(), privacyHandlersWorkerOK+"\n") ||
            !strings.Contains("\n"+stdout.String(), "\nPASS\n") {
            t.Fatal("worker did not report completed assertions and PASS")
        }
        return
    }

    h := newPrivacyHandlerHarness(t)
    t.Run("AuthAndRequireJSON", h.testAuth)
    t.Run("ConfigThroughExistingSettingRoute", h.testSettings)
    t.Run("InvalidInputs", h.testInvalidInputs)
    t.Run("CRUDAndByteValues", h.testCRUD)
    t.Run("DatabaseErrorsAndRollback", h.testDatabaseErrors)
    t.Run("FixedErrorMapping", testPrivacyHandlerErrorMapping)
    if h.sqlLogs.Len() != 0 {
        t.Fatal("privacy operations emitted SQL or database errors with the ordinary logger enabled")
    }
    if !t.Failed() {
        fmt.Println(privacyHandlersWorkerOK)
    }
}

type privacyHandlerHarness struct {
    engine  *gin.Engine
    jwt     string
    apiKey  string
    sqlLogs *bytes.Buffer
}

type privacyHandlerResponse struct {
    Code    int             `json:"code"`
    Message string          `json:"message"`
    Data    json.RawMessage `json:"data"`
}

func newPrivacyHandlerHarness(t *testing.T) *privacyHandlerHarness {
    t.Helper()
    gin.SetMode(gin.TestMode)
    if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "privacy-handlers.db"), false); err != nil {
        t.Fatal("initialize isolated SQLite:", err)
    }
    t.Cleanup(func() {
        if err := db.Close(); err != nil {
            t.Error("close isolated SQLite:", err)
        }
    })
    if err := op.InitCache(); err != nil {
        t.Fatal("initialize formal caches:", err)
    }
    if err := op.UserInit(); err != nil {
        t.Fatal("initialize isolated test user:", err)
    }
    jwt, _, err := auth.GenerateJWTToken(15)
    if err != nil || jwt == "" {
        t.Fatal("generate test JWT")
    }
    apiKey := auth.GenerateAPIKey()
    if apiKey == "" {
        t.Fatal("generate test API Key")
    }
    key := model.APIKey{Name: "privacy-handler-test-key", APIKey: apiKey, Enabled: true}
    if err := op.APIKeyCreate(&key, t.Context()); err != nil {
        t.Fatal("create formal test API Key")
    }
    engine := gin.New()
    if err := router.RegisterAll(engine); err != nil {
        t.Fatal("register formal handler routes:", err)
    }
    // 保持普通 SQL 日志开启; 隐私操作和样本会话必须丢弃日志.
    logs := &bytes.Buffer{}
    db.GetDB().Config.Logger = logger.New(stdlog.New(logs, "", 0), logger.Config{LogLevel: logger.Info})
    h := &privacyHandlerHarness{engine: engine, jwt: jwt, apiKey: apiKey, sqlLogs: logs}
    h.request(t, http.MethodGet, "/api/v1/apikey/login", nil,
        map[string]string{"Authorization": "Bearer " + apiKey}, http.StatusOK)
    if op.PrivacySnapshot() == nil {
        t.Fatal("formal InitCache did not publish privacy snapshot")
    }
    return h
}

func privacyHandlerConn() *gorm.DB {
    return db.GetDB().Session(&gorm.Session{Logger: logger.Discard})
}

func privacyHandlerJSON(t *testing.T, value any) []byte {
    t.Helper()
    data, err := json.Marshal(value)
    if err != nil {
        t.Fatal("encode test input")
    }
    return data
}

func (h *privacyHandlerHarness) request(t *testing.T, method, path string, body []byte, headers map[string]string, want int) privacyHandlerResponse {
    t.Helper()
    req := httptest.NewRequest(method, path, bytes.NewReader(body))
    for name, value := range headers {
        req.Header.Set(name, value)
    }
    w := httptest.NewRecorder()
    h.engine.ServeHTTP(w, req)
    if w.Code != want {
        t.Fatalf("%s %s status = %d, want %d (response withheld)", method, path, w.Code, want)
    }
    if !strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") {
        t.Fatal("response is not JSON")
    }
    var response privacyHandlerResponse
    var fields map[string]json.RawMessage
    if json.Unmarshal(w.Body.Bytes(), &response) != nil || json.Unmarshal(w.Body.Bytes(), &fields) != nil {
        t.Fatal("invalid response envelope")
    }
    if response.Code != want || response.Message == "" {
        t.Fatal("incorrect response code or missing message")
    }
    if want == http.StatusOK {
        if response.Message != "success" {
            t.Fatal("incorrect success message")
        }
    } else {
        if len(fields) != 2 || response.Data != nil {
            t.Fatal("error response contains unexpected fields or data")
        }
        for _, secret := range []string{privacyHandlerSourceCanary, privacyHandlerAliasCanary,
            "UNIQUE constraint", "SQLSTATE", "privacy_rules", "RAISE", "INSERT", "SELECT"} {
            if bytes.Contains(w.Body.Bytes(), []byte(secret)) {
                t.Fatal("error response leaked database details or sensitive parameters")
            }
        }
    }
    return response
}

func (h *privacyHandlerHarness) admin(t *testing.T, method, path string, body []byte, want int) privacyHandlerResponse {
    t.Helper()
    headers := map[string]string{"Authorization": "Bearer " + h.jwt}
    if method == http.MethodPost {
        headers["Content-Type"] = "application/json"
    }
    return h.request(t, method, path, body, headers, want)
}

func privacyHandlerRule(t *testing.T, raw json.RawMessage) model.PrivacyRule {
    t.Helper()
    var fields map[string]json.RawMessage
    if json.Unmarshal(raw, &fields) != nil || len(fields) != 4 {
        t.Fatal("rule response must contain exactly four fields")
    }
    for _, name := range []string{"id", "source", "alias", "enabled"} {
        if _, present := fields[name]; !present {
            t.Fatal("rule response omitted a required field")
        }
    }
    var rule model.PrivacyRule
    if json.Unmarshal(raw, &rule) != nil || rule.ID <= 0 || rule.Validate() != nil {
        t.Fatal("invalid rule response")
    }
    return rule
}

func (h *privacyHandlerHarness) create(t *testing.T, source, alias string, enabled *bool) model.PrivacyRule {
    t.Helper()
    input := map[string]any{"source": source, "alias": alias}
    if enabled != nil {
        input["enabled"] = *enabled
    }
    response := h.admin(t, http.MethodPost, "/api/v1/privacy/create", privacyHandlerJSON(t, input), http.StatusOK)
    rule := privacyHandlerRule(t, response.Data)
    if rule.Source.String() != source || rule.Alias.String() != alias || rule.Enabled != (enabled == nil || *enabled) {
        t.Fatal("create did not preserve exact string bytes or enabled default/false")
    }
    return rule
}

func (h *privacyHandlerHarness) update(t *testing.T, rule model.PrivacyRule) model.PrivacyRule {
    t.Helper()
    response := h.admin(t, http.MethodPost, "/api/v1/privacy/update", privacyHandlerJSON(t, rule), http.StatusOK)
    got := privacyHandlerRule(t, response.Data)
    if got != rule {
        t.Fatal("update did not return complete saved rule")
    }
    return got
}

func (h *privacyHandlerHarness) remove(t *testing.T, id int) {
    t.Helper()
    response := h.admin(t, http.MethodDelete, "/api/v1/privacy/delete/"+strconv.Itoa(id), nil, http.StatusOK)
    if response.Data != nil {
        t.Fatal("delete success must omit data, not return null")
    }
}

func (h *privacyHandlerHarness) assertRules(t *testing.T, want []model.PrivacyRule) {
    t.Helper()
    response := h.admin(t, http.MethodGet, "/api/v1/privacy/list", nil, http.StatusOK)
    var rawRules []json.RawMessage
    if json.Unmarshal(response.Data, &rawRules) != nil || rawRules == nil {
        t.Fatal("list must return an array, including [] for empty list")
    }
    got := make([]model.PrivacyRule, 0, len(rawRules))
    for _, raw := range rawRules {
        got = append(got, privacyHandlerRule(t, raw))
    }
    if !reflect.DeepEqual(got, want) {
        t.Fatal("list differs from expected exact rules or ascending ID order")
    }
    persisted := make([]model.PrivacyRule, 0)
    if err := privacyHandlerConn().Order("id ASC").Find(&persisted).Error; err != nil {
        t.Fatal("read actual persisted rules")
    }
    if !reflect.DeepEqual(persisted, want) {
        t.Fatal("actual SQLite rows differ from handler result")
    }
}

func (h *privacyHandlerHarness) testAuth(t *testing.T) {
    empty := []model.PrivacyRule{}
    h.assertRules(t, empty)
    before := op.PrivacySnapshot()
    for _, route := range []struct{ method, path string }{
        {http.MethodGet, "/api/v1/privacy/list"},
        {http.MethodPost, "/api/v1/privacy/create"},
        {http.MethodPost, "/api/v1/privacy/update"},
        {http.MethodDelete, "/api/v1/privacy/delete/1"},
    } {
        for _, credential := range []struct {
            name, authorization, xAPIKey, message string
            status                                int
        }{
            {"missing", "", "", resp.ErrBadRequest, 400},
            {"invalid-jwt", "Bearer invalid-test-jwt", "", resp.ErrUnauthorized, 401},
            {"valid-api-key-bearer", "Bearer " + h.apiKey, "", resp.ErrUnauthorized, 401},
            {"valid-api-key-header", "", h.apiKey, resp.ErrBadRequest, 400},
        } {
            t.Run(route.method+route.path+"/"+credential.name, func(t *testing.T) {
                response := h.request(t, route.method, route.path, []byte(`{`), map[string]string{
                    "Authorization": credential.authorization, "x-api-key": credential.xAPIKey,
                    "Content-Type": "text/plain",
                }, credential.status)
                if response.Message != credential.message {
                    t.Fatal("Auth did not reject before JSON/content-type validation")
                }
            })
        }
    }
    for _, path := range []string{"/api/v1/privacy/create", "/api/v1/privacy/update"} {
        for _, contentType := range []string{"", "text/plain", "application/problem+json", "Application/JSON"} {
            response := h.request(t, http.MethodPost, path, []byte(`{`), map[string]string{
                "Authorization": "Bearer " + h.jwt, "Content-Type": contentType,
            }, http.StatusUnsupportedMediaType)
            if response.Message != resp.ErrInvalidJSON {
                t.Fatal("unexpected RequireJSON rejection")
            }
        }
    }
    // GET 和 DELETE 跳过 RequireJSON, JWT Auth 忽略 x-api-key.
    h.request(t, http.MethodGet, "/api/v1/privacy/list", []byte(`not-json`), map[string]string{
        "Authorization": "Bearer " + h.jwt, "x-api-key": h.apiKey, "Content-Type": "text/plain",
    }, http.StatusOK)
    h.request(t, http.MethodDelete, "/api/v1/privacy/delete/999999", []byte(`not-json`), map[string]string{
        "Authorization": "Bearer " + h.jwt, "Content-Type": "text/plain",
    }, http.StatusNotFound)
    if op.PrivacySnapshot() != before {
        t.Fatal("rejected requests changed snapshot")
    }
    h.assertRules(t, empty)
    for index, contentType := range []string{"application/json; charset=utf-8", "text/application/json+fixture"} {
        // 现有中间件仅检查子串, 不严格校验 MIME 类型.
        input := map[string]any{"source": "content-type-fixture-" + strconv.Itoa(index), "alias": "CONTENT_TYPE_" + strconv.Itoa(index)}
        response := h.request(t, http.MethodPost, "/api/v1/privacy/create", privacyHandlerJSON(t, input), map[string]string{
            "Authorization": "Bearer " + h.jwt, "Content-Type": contentType,
        }, http.StatusOK)
        h.remove(t, privacyHandlerRule(t, response.Data).ID)
    }
    h.assertRules(t, empty)
}

// 通过现有配置入口测试, 不修改 setting.go 或新增独立隐私配置 API.
func (h *privacyHandlerHarness) testSettings(t *testing.T) {
    assertConfig := func(want privacy.Config) {
        t.Helper()
        response := h.admin(t, http.MethodGet, "/api/v1/setting/list", nil, http.StatusOK)
        var settings []model.Setting
        if json.Unmarshal(response.Data, &settings) != nil {
            t.Fatal("decode formal setting list")
        }
        values := make(map[model.SettingKey]string)
        for _, setting := range settings {
            if model.IsPrivacySettingKey(setting.Key) {
                values[setting.Key] = setting.Value
                var persisted model.Setting
                if privacyHandlerConn().Where("key = ?", setting.Key).First(&persisted).Error != nil || persisted != setting {
                    t.Fatal("privacy configuration cache and actual SQLite row differ")
                }
            }
        }
        got, err := model.ParsePrivacyConfig(values[model.SettingKeyPrivacyEnabled], values[model.SettingKeyPrivacyScopes], values[model.SettingKeyPrivacyFailurePolicy])
        if err != nil || len(values) != 3 || got != want || op.PrivacySnapshot().Config() != want {
            t.Fatal("setting list, actual SQLite and request snapshot configurations differ")
        }
    }
    assertConfig(privacy.DefaultConfig())
    set := func(key model.SettingKey, value string) {
        t.Helper()
        input := model.Setting{Key: key, Value: value}
        response := h.admin(t, http.MethodPost, "/api/v1/setting/set", privacyHandlerJSON(t, input), http.StatusOK)
        var got model.Setting
        if json.Unmarshal(response.Data, &got) != nil || got != input {
            t.Fatal("setting route did not return submitted string key/value")
        }
    }
    set(model.SettingKeyPrivacyEnabled, "true")
    set(model.SettingKeyPrivacyScopes, "15")
    set(model.SettingKeyPrivacyFailurePolicy, "abort")
    want := privacy.Config{Enabled: true, Scopes: privacy.AllScopes, FailurePolicy: privacy.Abort}
    assertConfig(want)
    for _, tc := range []struct {
        key   model.SettingKey
        value any
    }{
        {model.SettingKeyPrivacyEnabled, "True"}, {model.SettingKeyPrivacyEnabled, "1"},
        {model.SettingKeyPrivacyEnabled, nil}, {model.SettingKeyPrivacyEnabled, true},
        {model.SettingKeyPrivacyScopes, "16"}, {model.SettingKeyPrivacyScopes, "-1"},
        {model.SettingKeyPrivacyScopes, "+1"}, {model.SettingKeyPrivacyScopes, "1.5"},
        {model.SettingKeyPrivacyScopes, nil}, {model.SettingKeyPrivacyScopes, 1},
        {model.SettingKeyPrivacyFailurePolicy, "retry"}, {model.SettingKeyPrivacyFailurePolicy, nil},
    } {
        before := op.PrivacySnapshot()
        h.admin(t, http.MethodPost, "/api/v1/setting/set", privacyHandlerJSON(t, map[string]any{"key": tc.key, "value": tc.value}), http.StatusBadRequest)
        if op.PrivacySnapshot() != before {
            t.Fatal("invalid setting published a request snapshot")
        }
        assertConfig(want)
    }
    set(model.SettingKeyPrivacyScopes, "0")
    assertConfig(privacy.Config{Enabled: true, Scopes: 0, FailurePolicy: privacy.Abort})
    set(model.SettingKeyPrivacyEnabled, "false")
    set(model.SettingKeyPrivacyScopes, "1")
    set(model.SettingKeyPrivacyFailurePolicy, "continue")
    assertConfig(privacy.DefaultConfig())
}

func (h *privacyHandlerHarness) testInvalidInputs(t *testing.T) {
    seed := h.create(t, "validation-seed", "VALIDATION_SEED", nil)
    want := []model.PrivacyRule{seed}
    invalidFields := []struct {
        name, field string
        value       any
        omit        bool
    }{
        {"missing-source", "source", nil, true}, {"null-source", "source", nil, false},
        {"number-source", "source", 42, false}, {"bool-source", "source", true, false},
        {"array-source", "source", []string{"a"}, false}, {"object-source", "source", map[string]any{}, false},
        {"empty-source", "source", "", false}, {"source-513-bytes", "source", strings.Repeat("s", 513), false},
        {"source-unicode-513-bytes", "source", strings.Repeat("张", 171), false},
        {"missing-alias", "alias", nil, true}, {"null-alias", "alias", nil, false},
        {"number-alias", "alias", 42, false}, {"bool-alias", "alias", false, false},
        {"array-alias", "alias", []string{"A"}, false}, {"object-alias", "alias", map[string]any{}, false},
        {"empty-alias", "alias", "", false}, {"long-alias", "alias", strings.Repeat("A", 65), false},
        {"lowercase-alias", "alias", "Alias", false}, {"unicode-alias", "alias", "张三", false},
        {"nul-alias", "alias", "A\x00B", false}, {"space-alias", "alias", "A ", false},
        {"newline-alias", "alias", "A\n", false}, {"punctuation-alias", "alias", "A-B", false},
        {"null-enabled", "enabled", nil, false}, {"string-enabled", "enabled", "false", false},
        {"number-enabled", "enabled", 0, false}, {"array-enabled", "enabled", []bool{false}, false},
        {"object-enabled", "enabled", map[string]any{}, false},
    }
    for _, operation := range []string{"create", "update"} {
        for _, tc := range invalidFields {
            t.Run(operation+"/"+tc.name, func(t *testing.T) {
                input := map[string]any{"source": "valid-source", "alias": "VALID_ALIAS", "enabled": false}
                if operation == "update" {
                    input["id"] = seed.ID
                }
                if tc.omit {
                    delete(input, tc.field)
                } else {
                    input[tc.field] = tc.value
                }
                before := op.PrivacySnapshot()
                h.admin(t, http.MethodPost, "/api/v1/privacy/"+operation, privacyHandlerJSON(t, input), http.StatusBadRequest)
                if op.PrivacySnapshot() != before {
                    t.Fatal("invalid request published a snapshot")
                }
                h.assertRules(t, want)
            })
        }
        for index, raw := range [][]byte{nil, []byte(`{`), []byte(`{}`), []byte(`null`), []byte(`[]`), []byte(`"text"`), []byte(`true`),
            []byte(`{"source":"x","alias":"X",}`),
            []byte(fmt.Sprintf(`{"id":%d,"source":"\ud800","alias":"X","enabled":true}`, seed.ID)),
            []byte(fmt.Sprintf(`{"id":%d,"source":"\udc00","alias":"X","enabled":true}`, seed.ID)),
            []byte(fmt.Sprintf(`{"id":%d,"source":"x","alias":"\ud800","enabled":true}`, seed.ID)),
            []byte(fmt.Sprintf("{\"id\":%d,\"source\":\"\xff\",\"alias\":\"X\",\"enabled\":true}", seed.ID)),
        } {
            t.Run(operation+"/json-"+strconv.Itoa(index), func(t *testing.T) {
                before := op.PrivacySnapshot()
                h.admin(t, http.MethodPost, "/api/v1/privacy/"+operation, raw, http.StatusBadRequest)
                if op.PrivacySnapshot() != before {
                    t.Fatal("invalid JSON changed snapshot")
                }
                h.assertRules(t, want)
            })
        }
    }
    for index, id := range []any{nil, 0, -1, "1", 1.5, true, []int{1}, map[string]any{}, json.RawMessage(strings.Repeat("9", 100))} {
        t.Run("update/id-"+strconv.Itoa(index), func(t *testing.T) {
            input := map[string]any{"id": id, "source": "valid-source", "alias": "VALID_ALIAS", "enabled": true}
            before := op.PrivacySnapshot()
            h.admin(t, http.MethodPost, "/api/v1/privacy/update", privacyHandlerJSON(t, input), http.StatusBadRequest)
            if op.PrivacySnapshot() != before {
                t.Fatal("invalid update ID changed snapshot")
            }
        })
    }
    for _, missing := range []string{"id", "enabled"} {
        input := map[string]any{"id": seed.ID, "source": "valid-source", "alias": "VALID_ALIAS", "enabled": true}
        delete(input, missing)
        h.admin(t, http.MethodPost, "/api/v1/privacy/update", privacyHandlerJSON(t, input), http.StatusBadRequest)
    }
    for _, id := range []string{"0", "-1", "word", "1.5", strings.Repeat("9", 100)} {
        response := h.admin(t, http.MethodDelete, "/api/v1/privacy/delete/"+id, nil, http.StatusBadRequest)
        if response.Message != resp.ErrInvalidParam {
            t.Fatal("incorrect invalid delete ID message")
        }
    }
    h.assertRules(t, want)
    h.remove(t, seed.ID)
    h.assertRules(t, []model.PrivacyRule{})
}

func (h *privacyHandlerHarness) testCRUD(t *testing.T) {
    fixtures := []struct{ source, alias string }{
        {"13969909666", "PHONE_1"}, {"张三", "ZHANG_3"}, {"张三丰", "ZHANG_3_FENG"},
        {"a", "LETTER_A"}, {"ab", "LETTERS_AB"}, {"bc", "LETTERS_BC"}, {"A", "UPPER_A"},
        {"é", "ACCENT"}, {"e\u0301", "COMBINING_ACCENT"}, {"尾随 ", "TRAILING"},
        {"真实换行\n引号\"反斜杠\\", "ESCAPES"}, {"nul\x00source", "NUL_SOURCE"},
        {" ", "SPACE"}, {strings.Repeat("s", 512), strings.Repeat("A", 64)},
        {strings.Repeat("张", 170) + "ab", "UTF8_512"}, {"emoji 😀", "EMOJI"},
    }
    want := make([]model.PrivacyRule, 0, len(fixtures))
    for index, fixture := range fixtures {
        var enabled *bool
        if index%2 != 0 {
            value := false
            enabled = &value
        }
        want = append(want, h.create(t, fixture.source, fixture.alias, enabled))
        h.assertRules(t, want)
    }
    before := op.PrivacySnapshot()
    original := want[0]
    want[0].Source = model.NewPrivacyBytes("更新后原文\n ")
    want[0].Alias = model.NewPrivacyBytes("UPDATED_PHONE")
    want[0].Enabled = false
    h.update(t, want[0])
    h.assertRules(t, want)
    want[0].Enabled = true
    h.update(t, want[0])
    h.update(t, want[0]) // 完整同值更新也应成功.
    h.assertRules(t, want)
    foundOriginal := false
    for index := 0; index < before.RuleCount(); index++ {
        rule, _ := before.Rule(index)
        if rule.ID == original.ID {
            foundOriginal = rule == original.Rule()
        }
    }
    if !foundOriginal || op.PrivacySnapshot() == before {
        t.Fatal("handler update did not publish new state while preserving old snapshot")
    }
    // 禁用规则仍占用两个唯一键; 不允许部分更新.
    for _, field := range []string{"source", "alias"} {
        create := map[string]any{"source": "conflict-new-source", "alias": "CONFLICT_NEW_ALIAS", "enabled": false}
        update := map[string]any{"id": want[0].ID, "source": "conflict-new-source", "alias": "CONFLICT_NEW_ALIAS", "enabled": false}
        if field == "source" {
            create[field], update[field] = want[1].Source.String(), want[1].Source.String()
        } else {
            create[field], update[field] = want[1].Alias.String(), want[1].Alias.String()
        }
        for _, request := range []struct {
            operation string
            input     map[string]any
        }{{"create", create}, {"update", update}} {
            before := op.PrivacySnapshot()
            response := h.admin(t, http.MethodPost, "/api/v1/privacy/"+request.operation, privacyHandlerJSON(t, request.input), http.StatusConflict)
            if response.Message != privacy.ErrRuleConflict.Error() || op.PrivacySnapshot() != before {
                t.Fatal("conflict did not preserve old snapshot or fixed message")
            }
            h.assertRules(t, want)
        }
    }
    missing := model.PrivacyRule{ID: 999999, Source: model.NewPrivacyBytes("not-found"), Alias: model.NewPrivacyBytes("NOT_FOUND"), Enabled: false}
    response := h.admin(t, http.MethodPost, "/api/v1/privacy/update", privacyHandlerJSON(t, missing), http.StatusNotFound)
    if response.Message != privacy.ErrRuleNotFound.Error() {
        t.Fatal("incorrect update not-found message")
    }
    for _, rule := range want {
        h.remove(t, rule.ID)
    }
    h.assertRules(t, []model.PrivacyRule{})
    response = h.admin(t, http.MethodDelete, "/api/v1/privacy/delete/"+strconv.Itoa(want[1].ID), nil, http.StatusNotFound)
    if response.Message != privacy.ErrRuleNotFound.Error() {
        t.Fatal("repeat hard delete did not return fixed not-found message")
    }
    // 硬删除后复用两个唯一键, 并忽略输入 ID 和未知属性.
    input := map[string]any{"id": 50000, "source": want[1].Source.String(), "alias": want[1].Alias.String(), "extra": true, "enabled": false}
    response = h.admin(t, http.MethodPost, "/api/v1/privacy/create", privacyHandlerJSON(t, input), http.StatusOK)
    reused := privacyHandlerRule(t, response.Data)
    if reused.ID == 50000 || reused.ID <= want[len(want)-1].ID || reused.Enabled {
        t.Fatal("create did not use actual SQLite ID, reuse released keys, or preserve false")
    }
    h.assertRules(t, []model.PrivacyRule{reused})
    h.remove(t, reused.ID)
}

func (h *privacyHandlerHarness) testDatabaseErrors(t *testing.T) {
    // 在快照外写入数据行, 触发真实 SQLite UNIQUE 错误,
    // 而非仅测试编译器的候选冲突检查.
    outside := model.PrivacyRule{Source: model.NewPrivacyBytes(privacyHandlerSourceCanary), Alias: model.NewPrivacyBytes(privacyHandlerAliasCanary), Enabled: true}
    if privacyHandlerConn().Create(&outside).Error != nil {
        t.Fatal("insert isolated out-of-snapshot fixture")
    }
    for _, field := range []string{"source", "alias"} {
        input := map[string]any{"source": "db-conflict-source", "alias": "DB_CONFLICT_ALIAS", "enabled": false}
        if field == "source" {
            input[field] = privacyHandlerSourceCanary
        } else {
            input[field] = privacyHandlerAliasCanary
        }
        before := op.PrivacySnapshot()
        response := h.admin(t, http.MethodPost, "/api/v1/privacy/create", privacyHandlerJSON(t, input), http.StatusConflict)
        if response.Message != privacy.ErrRuleConflict.Error() || op.PrivacySnapshot() != before {
            t.Fatal("real SQLite unique error was not sanitized or changed snapshot")
        }
        h.assertRules(t, []model.PrivacyRule{outside})
    }
    if privacyHandlerConn().Delete(&outside).Error != nil {
        t.Fatal("remove isolated out-of-snapshot fixture")
    }
    seed := h.create(t, "rollback-seed", "ROLLBACK_SEED", nil)
    for _, operation := range []string{"INSERT", "UPDATE", "DELETE"} {
        t.Run("sqlite-abort-"+operation, func(t *testing.T) {
            // 真实 SQLite 触发器产生含敏感信息的驱动错误, 并执行真实写入和事务.
            // 不替换匹配器, op, 数据库或鉴权.
            ddl := "CREATE TRIGGER privacy_handler_abort BEFORE " + operation + " ON privacy_rules BEGIN SELECT RAISE(ABORT, '" + privacyHandlerSourceCanary + " " + privacyHandlerAliasCanary + "'); END"
            if privacyHandlerConn().Exec(ddl).Error != nil {
                t.Fatal("create isolated failure trigger")
            }
            t.Cleanup(func() {
                if privacyHandlerConn().Exec("DROP TRIGGER privacy_handler_abort").Error != nil {
                    t.Error("drop isolated failure trigger")
                }
            })
            before := op.PrivacySnapshot()
            input := map[string]any{"source": privacyHandlerSourceCanary, "alias": privacyHandlerAliasCanary, "enabled": false}
            var response privacyHandlerResponse
            switch operation {
            case "INSERT":
                response = h.admin(t, http.MethodPost, "/api/v1/privacy/create", privacyHandlerJSON(t, input), http.StatusInternalServerError)
            case "UPDATE":
                input["id"] = seed.ID
                response = h.admin(t, http.MethodPost, "/api/v1/privacy/update", privacyHandlerJSON(t, input), http.StatusInternalServerError)
            case "DELETE":
                response = h.admin(t, http.MethodDelete, "/api/v1/privacy/delete/"+strconv.Itoa(seed.ID), nil, http.StatusInternalServerError)
            }
            if response.Message != op.ErrPrivacyStorage.Error() || op.PrivacySnapshot() != before {
                t.Fatal("actual failed transaction leaked error or published candidate")
            }
            h.assertRules(t, []model.PrivacyRule{seed})
        })
    }
    h.remove(t, seed.ID)
    // SQLite 仅检查字节长度和类型, 不检查 UTF-8.
    // 无效存储数据属于内部读取错误, 而非客户端输入 400.
    corrupt := model.PrivacyRule{Source: model.NewPrivacyBytes("\xff"), Alias: model.NewPrivacyBytes("CORRUPT_READ"), Enabled: true}
    if privacyHandlerConn().Create(&corrupt).Error != nil {
        t.Fatal("insert isolated invalid UTF-8 fixture")
    }
    before := op.PrivacySnapshot()
    response := h.admin(t, http.MethodGet, "/api/v1/privacy/list", nil, http.StatusInternalServerError)
    if response.Message != op.ErrPrivacyStorage.Error() || op.PrivacySnapshot() != before {
        t.Fatal("invalid stored data was not mapped to fixed internal error")
    }
    if privacyHandlerConn().Delete(&corrupt).Error != nil {
        t.Fatal("remove isolated invalid UTF-8 fixture")
    }
    if privacyHandlerConn().Exec("ALTER TABLE privacy_rules RENAME TO privacy_handler_missing").Error != nil {
        t.Fatal("rename isolated table for actual read failure")
    }
    response = h.admin(t, http.MethodGet, "/api/v1/privacy/list", nil, http.StatusInternalServerError)
    if response.Message != op.ErrPrivacyStorage.Error() || op.PrivacySnapshot() != before {
        t.Fatal("actual SQLite read error was not sanitized")
    }
    if privacyHandlerConn().Exec("ALTER TABLE privacy_handler_missing RENAME TO privacy_rules").Error != nil {
        t.Fatal("restore isolated table")
    }
    h.assertRules(t, []model.PrivacyRule{})
}

// 专门测试错误映射, 不替代真实 API 和数据库测试.
// 不修改匹配器就无法自然触发编译失败.
func testPrivacyHandlerErrorMapping(t *testing.T) {
    for _, tc := range []struct {
        err     error
        status  int
        message string
    }{
        {privacy.ErrInvalidRule, 400, privacy.ErrInvalidRule.Error()},
        {privacy.ErrRuleNotFound, 404, privacy.ErrRuleNotFound.Error()},
        {privacy.ErrRuleConflict, 409, privacy.ErrRuleConflict.Error()},
        {privacy.ErrCompile, 500, privacy.ErrCompile.Error()},
        {errors.New(privacyHandlerSourceCanary), 500, op.ErrPrivacyStorage.Error()},
    } {
        w := httptest.NewRecorder()
        c, _ := gin.CreateTestContext(w)
        privacyRuleError(c, fmt.Errorf("%s %s: %w", privacyHandlerSourceCanary, privacyHandlerAliasCanary, tc.err))
        var response privacyHandlerResponse
        if json.Unmarshal(w.Body.Bytes(), &response) != nil || w.Code != tc.status || response.Code != tc.status ||
            response.Message != tc.message || response.Data != nil {
            t.Fatal("mapper did not recognize wrapped error and return only fixed message")
        }
    }
}
