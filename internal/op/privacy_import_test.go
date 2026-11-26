package op

import (
    "context"
    "database/sql"
    "encoding/json"
    "errors"
    "fmt"
    "reflect"
    "strings"
    "sync"
    "testing"
    "time"

    "github.com/bestruirui/octopus/internal/db"
    "github.com/bestruirui/octopus/internal/model"
    "github.com/bestruirui/octopus/internal/privacy"
    "gorm.io/gorm"
    "gorm.io/gorm/logger"
)

// 每阶段使用已编译测试程序, 配合新本地 SQLite 文件, 正式迁移和 InitCache.
// 不使用旧数据库, 不重置缓存, 不访问网络.
func TestPrivacyImport(t *testing.T) {
    scenarios := []struct {
        name string
        run  func(*testing.T)
    }{
        {"RoundTripAndSequence", privacyImportTestRoundTrip},
        {"OldBackups", privacyImportTestOldBackups},
        {"ConflictsAndValidation", privacyImportTestConflicts},
        {"StrictJSON", privacyImportTestJSON},
        {"AllTablesRollback", privacyImportTestRollback},
        {"CommitFailure", privacyImportTestCommitFailure},
        {"CommittedRefreshFailure", privacyImportTestRefreshFailure},
        {"SharedLockAndSilentExport", privacyImportTestBoundaries},
    }
    if !*octopusPrivacyWorker {
        for _, scenario := range scenarios {
            if !t.Run(scenario.name, func(t *testing.T) {
                privacyTestRunWorker(t, "TestPrivacyImport", scenario.name, "")
            }) {
                return
            }
        }
        return
    }
    for _, scenario := range scenarios {
        if scenario.name == *privacyTestPhase {
            privacyTestInit(t, "")
            scenario.run(t)
            privacyTestWorkerOK(t, "TestPrivacyImport")
            return
        }
    }
    t.Fatal("unknown privacy import worker phase")
}

func privacyImportTestRow(id int, source, alias string, enabled bool) model.PrivacyRule {
    return model.PrivacyRule{ID: id, Source: model.NewPrivacyBytes(source), Alias: model.NewPrivacyBytes(alias), Enabled: enabled}
}

func privacyImportTestSettings() []model.Setting {
    return []model.Setting{
        {Key: model.SettingKeyPrivacyEnabled, Value: "true"},
        {Key: model.SettingKeyPrivacyScopes, Value: "15"},
        {Key: model.SettingKeyPrivacyFailurePolicy, Value: "abort"},
        {Key: model.SettingKeyRelayLogKeepPeriod, Value: "13"},
    }
}

func privacyImportTestImport(t *testing.T, dump *model.DBDump, count int64) *model.DBImportResult {
    t.Helper()
    old := PrivacySnapshot()
    result, err := DBImportIncremental(t.Context(), dump)
    if err != nil || result == nil || result.RowsAffected["privacy_rules"] != count {
        t.Fatalf("import result = %v, error = %v, want privacy row count %d", result, err, count)
    }
    state := privacyTestCapture(t)
    if state.snapshot == old || state.version != old.Version()+1 ||
        !reflect.DeepEqual(state.rows, state.snapshotRules) || !reflect.DeepEqual(state.settings, state.cached) {
        t.Fatal("import did not publish exactly once with matching database, settings and rules")
    }
    return result
}

func privacyImportTestRoundTrip(t *testing.T) {
    empty, err := DBExportAll(t.Context(), false, false)
    if err != nil || empty.Version != 1 || empty.PrivacyRules == nil || len(empty.PrivacyRules) != 0 {
        t.Fatal("empty export must contain a version-1 privacy array")
    }
    encoded, err := json.Marshal(empty)
    if err != nil || !strings.Contains(string(encoded), `"privacy_rules":[]`) {
        t.Fatal("empty export omitted privacy_rules or did not encode an array")
    }
    retained := privacyTestCreate(t, "13969909666", "PHONE_1", privacyTestBool(false))
    // 相同映射故意使用不同 ID 和启用状态, 保留目标已有值.
    dump := &model.DBDump{Version: 1, Settings: privacyImportTestSettings(), PrivacyRules: []model.PrivacyRule{
        privacyImportTestRow(900, retained.Source.String(), retained.Alias.String(), true),
        privacyImportTestRow(1000, "张三\x00 ", "PERSON_1", false),
        privacyImportTestRow(1001, "line\nquote\"slash\\", "ESCAPED_1", true),
    }}
    result := privacyImportTestImport(t, dump, 2)
    if result.RowsAffected["settings"] != 4 {
        t.Fatal("import did not report privacy and ordinary settings upserts")
    }
    state := privacyTestCapture(t)
    if state.config != (privacy.Config{Enabled: true, Scopes: privacy.AllScopes, FailurePolicy: privacy.Abort}) ||
        len(state.rows) != 3 || state.rows[0].ID != retained.ID || state.rows[0].Enabled ||
        state.rows[1].ID != 1000 || state.rows[1].Enabled {
        t.Fatal("import changed target identity, explicit false, bytes or configuration")
    }
    if value, err := SettingGetString(model.SettingKeyRelayLogKeepPeriod); err != nil || value != "13" {
        t.Fatal("ordinary settings cache was not refreshed")
    }
    privacyTestAssertRestore(t, PrivacySnapshot(), "[[OCTOPUS_PII:ESCAPED_1]]", "line\nquote\"slash\\")
    privacyTestAssertRestore(t, PrivacySnapshot(), "[[OCTOPUS_PII:PHONE_1]]", "[[OCTOPUS_PII:PHONE_1]]")
    privacyImportTestImport(t, dump, 0)
    after := privacyTestCapture(t)
    if !reflect.DeepEqual(state.rows, after.rows) {
        t.Fatal("idempotent import changed stored enabled state or IDs")
    }
    exported, err := DBExportAll(t.Context(), true, true)
    if err != nil || !exported.IncludeLogs || !exported.IncludeStats || len(exported.PrivacyRules) != 3 {
        t.Fatal("full export lost privacy rules or flags")
    }
    encoded, err = json.Marshal(exported)
    if err != nil {
        t.Fatal("marshal full backup:", err)
    }
    var roundTrip model.DBDump
    if err := json.Unmarshal(encoded, &roundTrip); err != nil ||
        !reflect.DeepEqual(exported.PrivacyRules, roundTrip.PrivacyRules) {
        t.Fatal("backup JSON did not preserve source strings, binary values or false")
    }
    privacyImportTestImport(t, &roundTrip, 0)
    // 新映射缺失 enabled 时默认 true; 显式 false 保持 false.
    var defaults model.DBDump
    if err := json.Unmarshal([]byte(`{"version":1,"privacy_rules":[{"id":2000,"source":"default-enabled","alias":"DEFAULT_ENABLED"},{"id":2001,"source":"explicit-disabled","alias":"EXPLICIT_DISABLED","enabled":false}]}`), &defaults); err != nil {
        t.Fatal("decode import defaults:", err)
    }
    privacyImportTestImport(t, &defaults, 2)
    var rows []model.PrivacyRule
    if err := privacyTestConn().Where("id IN ?", []int{2000, 2001}).Order("id").Find(&rows).Error; err != nil ||
        len(rows) != 2 || !rows[0].Enabled || rows[1].Enabled {
        t.Fatal("import defaults were rewritten by GORM")
    }
    created := privacyTestCreate(t, "post-import auto id", "POST_IMPORT_ID", nil)
    if created.ID <= 2001 {
        t.Fatal("formal create after explicit-ID import collided or sequence moved backwards")
    }
}

func privacyImportTestOldBackups(t *testing.T) {
    retained := privacyTestCreate(t, "old-backup retained", "OLD_RETAINED", privacyTestBool(false))
    privacyTestSet(t, model.SettingKeyPrivacyEnabled, "true")
    privacyTestSet(t, model.SettingKeyPrivacyFailurePolicy, "abort")
    for _, body := range []string{
        `{"settings":[{"key":"relay_log_keep_period","value":"12"}]}`,
        `{"version":1,"privacy_rules":[],"settings":[{"key":"two_factor_enabled","value":"true"}]}`,
        `{"version":1,"privacy_rules":null}`,
        `{"version":0,"include_logs":true,"relay_logs":[{"id":12345,"request_content":"synthetic old log"}]}`,
    } {
        var dump model.DBDump
        if err := json.Unmarshal([]byte(body), &dump); err != nil {
            t.Fatal("decode old backup:", err)
        }
        privacyImportTestImport(t, &dump, 0)
        state := privacyTestCapture(t)
        if len(state.rows) != 1 || state.rows[0] != retained.Rule() ||
            !state.config.Enabled || state.config.FailurePolicy != privacy.Abort {
            t.Fatal("old backup removed a rule or reset omitted privacy settings")
        }
    }
    var oldLog model.RelayLog
    if err := privacyTestConn().First(&oldLog, "id = ?", 12345).Error; err != nil || oldLog.RequestContent != "synthetic old log" {
        t.Fatal("old log backup was not imported")
    }
    if value, err := SettingGetString(model.SettingKeyTwoFactorEnabled); err != nil || value != "false" {
        t.Fatal("old backup imported the two-factor switch without its secret")
    }
    before := privacyTestCapture(t)
    _, err := DBImportIncremental(t.Context(), &model.DBDump{Version: 2})
    if err == nil {
        t.Fatal("unsupported backup version was accepted")
    }
    privacyTestPreserved(t, before)
    _, err = DBImportIncremental(t.Context(), nil)
    if err == nil {
        t.Fatal("nil backup was accepted")
    }
    privacyTestPreserved(t, before)
}

func privacyImportTestConflicts(t *testing.T) {
    base := privacyTestCreate(t, "reserved synthetic source", "RESERVED_ALIAS", privacyTestBool(false))
    type importCase struct {
        name     string
        rows     []model.PrivacyRule
        settings []model.Setting
        want     error
    }
    cases := []importCase{
        {name: "Source", rows: []model.PrivacyRule{privacyImportTestRow(100, base.Source.String(), "OTHER_ALIAS", true)}, want: privacy.ErrRuleConflict},
        {name: "Alias", rows: []model.PrivacyRule{privacyImportTestRow(100, "other synthetic source", base.Alias.String(), true)}, want: privacy.ErrRuleConflict},
        {name: "PrimaryKey", rows: []model.PrivacyRule{privacyImportTestRow(base.ID, "other synthetic source", "OTHER_ALIAS", true)}, want: privacy.ErrRuleConflict},
        {name: "DuplicateSourceInDump", rows: []model.PrivacyRule{privacyImportTestRow(100, "duplicate", "DUP_A", true), privacyImportTestRow(101, "duplicate", "DUP_B", false)}, want: privacy.ErrRuleConflict},
        {name: "DuplicateAliasInDump", rows: []model.PrivacyRule{privacyImportTestRow(100, "duplicate A", "DUP", true), privacyImportTestRow(101, "duplicate B", "DUP", true)}, want: privacy.ErrRuleConflict},
        {name: "DuplicateIDInDump", rows: []model.PrivacyRule{privacyImportTestRow(100, "duplicate A", "DUP_A", true), privacyImportTestRow(100, "duplicate B", "DUP_B", true)}, want: privacy.ErrRuleConflict},
        {name: "ZeroID", rows: []model.PrivacyRule{privacyImportTestRow(0, "valid source", "VALID_ALIAS", true)}, want: privacy.ErrInvalidRule},
        {name: "NegativeID", rows: []model.PrivacyRule{privacyImportTestRow(-1, "valid source", "VALID_ALIAS", true)}, want: privacy.ErrInvalidRule},
        {name: "BadUTF8", rows: []model.PrivacyRule{privacyImportTestRow(100, string([]byte{0xff}), "VALID_ALIAS", false)}, want: privacy.ErrInvalidRule},
        {name: "LongSource", rows: []model.PrivacyRule{privacyImportTestRow(100, strings.Repeat("x", 513), "VALID_ALIAS", true)}, want: privacy.ErrInvalidRule},
        {name: "BadAlias", rows: []model.PrivacyRule{privacyImportTestRow(100, "valid source", "invalid", false)}, want: privacy.ErrInvalidRule},
        {name: "NullByteAlias", rows: []model.PrivacyRule{privacyImportTestRow(100, "valid source", "BAD\x00ALIAS", true)}, want: privacy.ErrInvalidRule},
    }
    for _, key := range privacyTestSettingKeys {
        cases = append(cases, importCase{name: string(key), settings: []model.Setting{{Key: key, Value: "invalid"}}, want: privacy.ErrInvalidConfig})
    }
    cases = append(cases, importCase{name: "InvalidThenValidSetting", settings: []model.Setting{{Key: model.SettingKeyPrivacyScopes, Value: "16"}, {Key: model.SettingKeyPrivacyScopes, Value: "1"}}, want: privacy.ErrInvalidConfig})
    for _, tc := range cases {
        t.Run(tc.name, func(t *testing.T) {
            before := privacyTestCapture(t)
            dump := privacyImportTestBusinessDump()
            dump.PrivacyRules, dump.Settings = tc.rows, tc.settings
            result, err := DBImportIncremental(t.Context(), dump)
            if result != nil {
                t.Fatal("precommit failure returned a successful result")
            }
            privacyTestExpectError(t, before, err, tc.want)
            privacyImportTestBusinessAbsent(t)
        })
    }
    // 完整候选校验也覆盖普通设置, 不仅限于隐私设置.
    before := privacyTestCapture(t)
    _, err := DBImportIncremental(t.Context(), &model.DBDump{Version: 1, Settings: []model.Setting{{Key: model.SettingKeyRelayLogKeepPeriod, Value: "not-an-integer"}}})
    if err == nil {
        t.Fatal("backup bypassed ordinary Setting.Validate")
    }
    privacyTestPreserved(t, before)
    // 备份内完全相同的重复映射也保持幂等.
    privacyImportTestImport(t, &model.DBDump{Version: 1, PrivacyRules: []model.PrivacyRule{
        privacyImportTestRow(100, "same pair", "SAME_PAIR", false), privacyImportTestRow(101, "same pair", "SAME_PAIR", true),
    }}, 1)
}

func privacyImportTestJSON(t *testing.T) {
    before := privacyTestCapture(t)
    for _, key := range privacyTestSettingKeys {
        for _, value := range []string{`null`, `true`, `1`, `{}`, `[]`, `"\ud800"`, `"\udc00"`} {
            body := fmt.Sprintf(`{"version":1,"settings":[{"key":%q,"value":%s}]}`, key, value)
            var dump model.DBDump
            if err := json.Unmarshal([]byte(body), &dump); !errors.Is(err, privacy.ErrInvalidConfig) {
                t.Fatalf("privacy backup setting did not return fixed input error: %v", err)
            }
        }
        var dump model.DBDump
        body := fmt.Sprintf(`{"settings":[{"key":%q}]}`, key)
        if err := json.Unmarshal([]byte(body), &dump); !errors.Is(err, privacy.ErrInvalidConfig) {
            t.Fatal("missing privacy setting value was accepted")
        }
    }
    for _, fields := range []string{
        `"source":null,"alias":"VALID"`, `"source":123,"alias":"VALID"`,
        `"source":"\ud800","alias":"VALID"`, `"source":"valid","alias":null`,
        `"source":"valid","alias":"VALID","enabled":null`,
        `"source":"valid","alias":"VALID","enabled":"false"`,
        `"source":"valid","alias":"VALID","enabled":1`,
        `"source":"` + string([]byte{0xff}) + `","alias":"VALID"`,
    } {
        var dump model.DBDump
        if err := json.Unmarshal([]byte(`{"version":1,"privacy_rules":[{"id":100,`+fields+`}]}`), &dump); !errors.Is(err, privacy.ErrInvalidRule) {
            t.Fatalf("privacy rule JSON did not return fixed input error: %v", err)
        }
    }
    privacyTestPreserved(t, before)
}

// 所有业务表均为真实表. 最后的中继日志插入触发器在规则, 渠道, 分组,
// 价格, 密钥, 设置和统计写入后才使操作失败.
func privacyImportTestBusinessDump() *model.DBDump {
    return &model.DBDump{
        Version: 1, IncludeStats: true, IncludeLogs: true,
        PrivacyRules: []model.PrivacyRule{privacyImportTestRow(5000, "rollback new source", "ROLLBACK_NEW", false)},
        Settings:     privacyImportTestSettings(),
        Channels:     []model.Channel{{ID: 5000, Name: "privacy-import-test-channel"}},
        ChannelKeys:  []model.ChannelKey{{ID: 5000, ChannelID: 5000, ChannelKey: "synthetic-local-key"}},
        Groups:       []model.Group{{ID: 5000, Name: "privacy-import-test-group", Mode: model.GroupModeFailover}},
        GroupItems:   []model.GroupItem{{ID: 5000, GroupID: 5000, ChannelID: 5000, ModelName: "privacy-import-test-model"}},
        LLMInfos:     []model.LLMInfo{{Name: "privacy-import-test-model", LLMPrice: model.LLMPrice{Input: 9}}},
        APIKeys:      []model.APIKey{{ID: 5000, Name: "privacy-import-test-key", APIKey: "synthetic-management-unused"}},
        StatsTotal:   []model.StatsTotal{{ID: 1, StatsMetrics: model.StatsMetrics{InputToken: 99}}},
        StatsDaily:   []model.StatsDaily{{Date: "20010101", StatsMetrics: model.StatsMetrics{InputToken: 99}}},
        StatsHourly:  []model.StatsHourly{{Hour: 1, Date: "20010101", StatsMetrics: model.StatsMetrics{InputToken: 99}}},
        StatsModel:   []model.StatsModel{{ID: 5000, Name: "privacy-import-test-model", ChannelID: 5000}},
        StatsChannel: []model.StatsChannel{{ChannelID: 5000}},
        StatsAPIKey:  []model.StatsAPIKey{{APIKeyID: 5000}},
        RelayLogs:    []model.RelayLog{{ID: 5000, RequestContent: "synthetic local log"}},
    }
}

func privacyImportTestBusinessAbsent(t *testing.T) {
    t.Helper()
    for _, table := range []string{"channels", "channel_keys", "groups", "group_items", "llm_infos", "api_keys", "stats_totals", "stats_dailies", "stats_hourlies", "stats_models", "stats_channels", "stats_api_keys", "relay_logs"} {
        var count int64
        if err := privacyTestConn().Table(table).Count(&count).Error; err != nil || count != 0 {
            t.Fatalf("failed import left rows in %s (count=%d, error=%v)", table, count, err)
        }
    }
    if value, err := SettingGetString(model.SettingKeyRelayLogKeepPeriod); err != nil || value != "7" {
        t.Fatal("failed import changed ordinary setting cache")
    }
    var setting model.Setting
    if err := privacyTestConn().First(&setting, "key = ?", model.SettingKeyRelayLogKeepPeriod).Error; err != nil || setting.Value != "7" {
        t.Fatal("failed import changed ordinary persisted setting")
    }
}

func privacyImportTestRollback(t *testing.T) {
    privacyTestCreate(t, "rollback retained source", "ROLLBACK_RETAINED", nil)
    before := privacyTestCapture(t)
    privacyTestExec(t, "CREATE TRIGGER privacy_import_reject_log BEFORE INSERT ON relay_logs BEGIN SELECT RAISE(ABORT, 'synthetic_private_failure_detail'); END")
    result, err := DBImportIncremental(t.Context(), privacyImportTestBusinessDump())
    privacyTestExec(t, "DROP TRIGGER privacy_import_reject_log")
    if result != nil || err != ErrBackupStorage {
        t.Fatalf("write failure did not return the fixed precommit error: %v", err)
    }
    privacyTestPreserved(t, before)
    privacyImportTestBusinessAbsent(t)
    // 带外写入样本不在已发布候选中; 以下三种冲突必须由真实 SQLite
    // 强制检查, 而非 Go 映射.
    external := privacyImportTestRow(6000, privacyTestSecretSource, privacyTestSecretAlias, false)
    privacyTestExec(t, "INSERT INTO privacy_rules(id, source, alias, enabled) VALUES (?, ?, ?, 0)", external.ID, external.Source, external.Alias)
    for _, row := range []model.PrivacyRule{
        privacyImportTestRow(6001, privacyTestSecretSource, "EXTERNAL_OTHER_ALIAS", true),
        privacyImportTestRow(6001, "external other source", privacyTestSecretAlias, true),
        privacyImportTestRow(6000, "external other source", "EXTERNAL_OTHER_ALIAS", true),
    } {
        before := privacyTestCapture(t)
        dump := privacyImportTestBusinessDump()
        dump.PrivacyRules = append(dump.PrivacyRules, row)
        result, err := DBImportIncremental(t.Context(), dump)
        if result != nil {
            t.Fatal("database conflict returned success")
        }
        privacyTestExpectError(t, before, err, privacy.ErrRuleConflict)
        privacyImportTestBusinessAbsent(t)
    }
    privacyTestExec(t, "DELETE FROM privacy_rules WHERE id = ?", external.ID)
    // 回滚后两个唯一键均可复用, 正常导入仍可成功.
    privacyImportTestImport(t, privacyImportTestBusinessDump(), 1)
    var stats model.StatsTotal
    if err := privacyTestConn().First(&stats).Error; err != nil || stats.InputToken != 99 {
        t.Fatal("successful retry did not import the business tables")
    }
}

func privacyImportTestCommitFailure(t *testing.T) {
    privacyTestCreate(t, "commit retained source", "COMMIT_RETAINED", nil)
    before := privacyTestCapture(t)
    conn := db.GetDB()
    sqlDB, err := conn.DB()
    if err != nil {
        t.Fatal("get local pool:", err)
    }
    original, statementOriginal := conn.ConnPool, conn.Statement.ConnPool
    pool := &privacyTestCommitPool{ConnPool: original, sqlDB: sqlDB}
    conn.ConnPool, conn.Statement.ConnPool = pool, pool
    defer func() { conn.ConnPool, conn.Statement.ConnPool = original, statementOriginal }()
    result, err := DBImportIncremental(t.Context(), privacyImportTestBusinessDump())
    conn.ConnPool, conn.Statement.ConnPool = original, statementOriginal
    if err != ErrBackupStorage || result != nil || pool.attempts != 1 || pool.writes < 10 {
        t.Fatal("Commit failure was ignored, published, or did not follow real business writes")
    }
    privacyTestPreserved(t, before)
    privacyImportTestBusinessAbsent(t)
}

func privacyImportTestRefreshFailure(t *testing.T) {
    before := privacyTestCapture(t)
    conn := db.GetDB()
    const callback = "privacy_import_test_refresh_failure"
    queries := 0
    if err := conn.Callback().Query().Before("gorm:query").Register(callback, func(tx *gorm.DB) {
        if tx.Statement.Schema != nil && tx.Statement.Schema.Table == "channels" {
            queries++
            tx.AddError(errors.New("synthetic_nonprivacy_refresh_failure"))
        }
    }); err != nil {
        t.Fatal("register refresh fault:", err)
    }
    defer conn.Callback().Query().Remove(callback)
    result, err := DBImportIncremental(t.Context(), privacyImportTestBusinessDump())
    if err != ErrImportCommittedCacheRefresh || result == nil || result.RowsAffected["privacy_rules"] != 1 || queries != 1 {
        t.Fatal("postcommit refresh failure was ignored or reported as a rollback")
    }
    state := privacyTestCapture(t)
    if state.snapshot == before.snapshot || state.version != before.version+1 || len(state.rows) != 1 ||
        !reflect.DeepEqual(state.rows, state.snapshotRules) || !reflect.DeepEqual(state.settings, state.cached) || !state.config.Enabled {
        t.Fatal("postcommit cache failure discarded or recompiled the published privacy snapshot")
    }
    var log model.RelayLog
    if err := privacyTestConn().First(&log, "id = ?", 5000).Error; err != nil {
        t.Fatal("postcommit error lost committed business data")
    }
    privacyTestAssertRestore(t, before.snapshot, "[[OCTOPUS_PII:ROLLBACK_NEW]]", "[[OCTOPUS_PII:ROLLBACK_NEW]]")
    if err := conn.Callback().Query().Remove(callback); err != nil {
        t.Fatal("remove refresh fault:", err)
    }
    old := PrivacySnapshot()
    if err := refreshImportedCaches(t.Context()); err != nil || PrivacySnapshot() != old {
        t.Fatal("ordinary-cache recovery unexpectedly refreshed privacy")
    }
    privacyTestCreate(t, "post-refresh-failure source", "POST_REFRESH_FAILURE", nil)
}

// 观察真实 SQLite 事务选项, 不替换数据库写入或提交.
type privacyImportTestPool struct {
    gorm.ConnPool
    sqlDB    *sql.DB
    begins   int
    readOnly bool
}

func (pool *privacyImportTestPool) BeginTx(ctx context.Context, opts *sql.TxOptions) (gorm.ConnPool, error) {
    pool.begins++
    pool.readOnly = opts != nil && opts.ReadOnly
    return pool.sqlDB.BeginTx(ctx, opts)
}

func (pool *privacyImportTestPool) GetDBConn() (*sql.DB, error) { return pool.sqlDB, nil }

func privacyImportTestBoundaries(t *testing.T) {
    rule := privacyTestCreate(t, privacyTestSecretSource, privacyTestSecretAlias, nil)
    conn := db.GetDB()
    originalLogger := conn.Logger
    spy := &privacyTestLogger{}
    conn.Logger = spy
    defer func() { conn.Logger = originalLogger }()
    sqlDB, err := conn.DB()
    if err != nil {
        t.Fatal("get actual SQLite pool:", err)
    }
    original, statementOriginal := conn.ConnPool, conn.Statement.ConnPool
    pool := &privacyImportTestPool{ConnPool: original, sqlDB: sqlDB}
    conn.ConnPool, conn.Statement.ConnPool = pool, pool
    defer func() { conn.ConnPool, conn.Statement.ConnPool = original, statementOriginal }()
    const callback = "privacy_import_test_boundary"
    privateWrites := 0
    before := PrivacySnapshot()
    if err := conn.Callback().Create().Before("gorm:create").Register(callback, func(tx *gorm.DB) {
        if tx.Statement.Schema == nil || tx.Statement.Schema.Table != "privacy_rules" {
            return
        }
        privateWrites++
        if privacyWriteMu.TryLock() {
            privacyWriteMu.Unlock()
            t.Error("import did not hold the shared privacy write lock")
        }
        if tx.Logger != logger.Discard || PrivacySnapshot() != before {
            t.Error("import SQL was not silent or published before Commit")
        }
        if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
            t.Error("privacy import write was not inside the business transaction")
        }
    }); err != nil {
        t.Fatal("register boundary observer:", err)
    }
    defer conn.Callback().Create().Remove(callback)
    exported, err := DBExportAll(t.Context(), true, true)
    if err != nil || len(exported.PrivacyRules) != 1 || pool.begins != 1 || !pool.readOnly || spy.count() != 0 {
        t.Fatal("export did not use a single silent read-only transaction")
    }
    dump := &model.DBDump{Version: 1, PrivacyRules: []model.PrivacyRule{privacyImportTestRow(100, "boundary new source", "BOUNDARY_NEW", false)}}
    privacyImportTestImport(t, dump, 1)
    if privateWrites != 1 || pool.begins != 2 || pool.readOnly {
        t.Fatal("import did not use exactly one write transaction")
    }
    // 导出在事务内暂停. 正式管理写入必须共用其锁;
    // 请求仍可读取已发布的旧快照.
    entered, release := make(chan struct{}), make(chan struct{})
    var releaseOnce sync.Once
    releaseExport := func() { releaseOnce.Do(func() { close(release) }) }
    t.Cleanup(releaseExport)
    const queryCallback = "privacy_export_test_pause"
    if err := conn.Callback().Query().Before("gorm:query").Register(queryCallback, func(tx *gorm.DB) {
        if tx.Statement.Schema != nil && tx.Statement.Schema.Table == "channels" {
            close(entered)
            <-release
        }
    }); err != nil {
        t.Fatal("register export pause:", err)
    }
    done := make(chan error, 1)
    go func() { _, err := DBExportAll(t.Context(), false, false); done <- err }()
    select {
    case <-entered:
    case <-time.After(5 * time.Second):
        t.Fatal("export failed to enter its read transaction")
    }
    if privacyWriteMu.TryLock() {
        privacyWriteMu.Unlock()
        t.Error("export did not hold the shared privacy lock")
    }
    held := PrivacySnapshot()
    privacyTestAssertRestore(t, held, "[[OCTOPUS_PII:"+privacyTestSecretAlias+"]]", "[[OCTOPUS_PII:"+privacyTestSecretAlias+"]]")
    releaseExport()
    if err := <-done; err != nil {
        t.Fatal("paused export:", err)
    }
    if err := conn.Callback().Query().Remove(queryCallback); err != nil {
        t.Fatal("remove export pause:", err)
    }
    before = PrivacySnapshot()
    // 随后更新, 验证两个操作都已释放共享锁.
    if err := PrivacyRuleDelete(t.Context(), rule.ID); err != nil {
        t.Fatal("formal write after export:", err)
    }
    if spy.count() == 0 {
        t.Fatal("backup silence globally disabled unrelated resource-query logging")
    }
}
