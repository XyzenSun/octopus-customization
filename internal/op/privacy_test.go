package op

import (
    "bytes"
    "context"
    "database/sql"
    "encoding/json"
    "errors"
    "flag"
    "fmt"
    "os"
    "os/exec"
    "path/filepath"
    "reflect"
    "regexp"
    "runtime"
    "sort"
    "strconv"
    "strings"
    "sync"
    "testing"
    "time"

    "github.com/bestruirui/octopus/internal/db"
    "github.com/bestruirui/octopus/internal/db/migrate"
    "github.com/bestruirui/octopus/internal/model"
    "github.com/bestruirui/octopus/internal/privacy"
    "github.com/glebarez/sqlite"
    "gorm.io/gorm"
    "gorm.io/gorm/logger"
)

// These flags are consumed only by tests. TestMain checks worker before legacy
// initialization: migration registries and package caches must be fresh per suite.
var (
    octopusPrivacyWorker = flag.Bool("octopus.privacy.worker", false, "run an isolated privacy test worker")
    privacyTestPhase     = flag.String("octopus.privacy.phase", "", "isolated privacy test phase")
    privacyTestDBPath    = flag.String("octopus.privacy.db", "", "parent-owned temporary SQLite file")
)

const (
    privacyTestSecretSource = "privacy_test_synthetic_sensitive_source_7C2E"
    privacyTestSecretAlias  = "PRIVACY_TEST_SILENCE_CANARY_7C2E"
)

var privacyTestSettingKeys = []model.SettingKey{
    model.SettingKeyPrivacyEnabled,
    model.SettingKeyPrivacyScopes,
    model.SettingKeyPrivacyFailurePolicy,
}

// Reuse the already compiled binary, including its race instrumentation. Never
// recursively invoke go test, inspect a DSN, or use an application data directory.
func privacyTestRunWorker(t *testing.T, suite, phase, path string) {
    t.Helper()
    executable, err := os.Executable()
    if err != nil {
        t.Fatal("locate test executable:", err)
    }
    ctx := t.Context()
    timeout := flag.Lookup("test.timeout").Value.String()
    if deadline, ok := t.Deadline(); ok {
        var cancel context.CancelFunc
        ctx, cancel = context.WithDeadline(ctx, deadline)
        defer cancel()
        timeout = time.Until(deadline).String()
    }
    cmd := exec.CommandContext(ctx, executable,
        "-test.run=^"+regexp.QuoteMeta(suite)+"$",
        "-test.v="+strconv.FormatBool(testing.Verbose()),
        "-test.timeout="+timeout, "-test.count=1", "-test.parallel=1",
        "-octopus.privacy.worker=true", "-octopus.privacy.phase="+phase,
        "-octopus.privacy.db="+path,
    )
    var stdout, stderr bytes.Buffer
    cmd.Stdout, cmd.Stderr = &stdout, &stderr
    err = cmd.Run()
    // Retain all output in the parent result, not just a subprocess-start signal.
    t.Logf("worker %s/%s stdout:\n%s\nstderr:\n%s", suite, phase, stdout.String(), stderr.String())
    if err != nil {
        t.Fatalf("worker %s/%s did not exit successfully: %v", suite, phase, err)
    }
    marker := "OCTOPUS_PRIVACY_WORKER_OK:" + suite + ":" + phase
    if !strings.Contains(stdout.String(), marker+"\n") ||
        !regexp.MustCompile(`(?m)^PASS\r?$`).Match(stdout.Bytes()) {
        t.Fatalf("worker %s/%s did not report completed assertions and PASS", suite, phase)
    }
    for _, secret := range []string{privacyTestSecretSource, privacyTestSecretAlias} {
        if strings.Contains(stdout.String(), secret) || strings.Contains(stderr.String(), secret) {
            t.Fatal("privacy fixture leaked into worker output")
        }
    }
}

func privacyTestWorkerOK(t *testing.T, suite string) {
    t.Helper()
    if !t.Failed() {
        fmt.Printf("OCTOPUS_PRIVACY_WORKER_OK:%s:%s\n", suite, *privacyTestPhase)
    }
}

func privacyTestInit(t *testing.T, path string) {
    t.Helper()
    if path == "" {
        path = filepath.Join(t.TempDir(), "privacy.db")
    }
    if err := db.InitDB("sqlite", path, false); err != nil {
        t.Fatal("initialize isolated SQLite:", err)
    }
    t.Cleanup(func() {
        if err := db.Close(); err != nil {
            t.Error("close isolated SQLite:", err)
        }
    })
    if err := InitCache(); err != nil {
        t.Fatal("initialize formal caches:", err)
    }
    if PrivacySnapshot() == nil {
        t.Fatal("InitCache did not publish a privacy snapshot")
    }
}

func privacyTestConn() *gorm.DB {
    return db.GetDB().Session(&gorm.Session{Logger: logger.Discard})
}

// GORM 会展开 VALUES (?) 中的 []byte. 用 Valuer 保留单个二进制参数.
func privacyTestRawExec(query string, args ...any) *gorm.DB {
    for index, value := range args {
        if data, ok := value.([]byte); ok {
            args[index] = model.NewPrivacyBytes(string(data))
        }
    }
    return privacyTestConn().Exec(query, args...)
}

func privacyTestExec(t *testing.T, query string, args ...any) {
    t.Helper()
    if err := privacyTestRawExec(query, args...).Error; err != nil {
        t.Fatal("execute isolated fixture SQL:", err)
    }
}

func privacyTestBool(value bool) *bool { return &value }

func privacyTestCreateInput(source, alias string, enabled *bool) model.PrivacyRuleCreate {
    return model.PrivacyRuleCreate{
        Source: model.NewPrivacyBytes(source), Alias: model.NewPrivacyBytes(alias), Enabled: enabled,
    }
}

func privacyTestUpdateInput(id int, source, alias string, enabled *bool) model.PrivacyRuleUpdate {
    return model.PrivacyRuleUpdate{
        ID: id, Source: model.NewPrivacyBytes(source), Alias: model.NewPrivacyBytes(alias), Enabled: enabled,
    }
}

func privacyTestCreate(t *testing.T, source, alias string, enabled *bool) model.PrivacyRule {
    t.Helper()
    rule, err := PrivacyRuleCreate(t.Context(), privacyTestCreateInput(source, alias, enabled))
    if err != nil {
        t.Fatal("formal privacy create:", err)
    }
    if rule.ID <= 0 || rule.Source.String() != source || rule.Alias.String() != alias ||
        rule.Enabled != (enabled == nil || *enabled) {
        t.Fatal("create returned an incorrect ID, byte value, or enabled state")
    }
    return rule
}

func privacyTestSet(t *testing.T, key model.SettingKey, value string) {
    t.Helper()
    if err := PrivacySetSetting(t.Context(), key, value); err != nil {
        t.Fatal("formal privacy setting write:", err)
    }
    cached, err := SettingGetString(key)
    if err != nil || cached != value {
        t.Fatal("privacy setting cache was not published with the write")
    }
    var persisted model.Setting
    if err := privacyTestConn().First(&persisted, "key = ?", key).Error; err != nil || persisted.Value != value {
        t.Fatal("privacy setting write was not persisted")
    }
}

func privacyTestNeutralRules(rows []model.PrivacyRule) []privacy.Rule {
    rules := make([]privacy.Rule, len(rows))
    for i, row := range rows {
        rules[i] = privacy.Rule{ID: row.ID, Source: row.Source.String(), Alias: row.Alias.String(), Enabled: row.Enabled}
    }
    return rules
}

func privacyTestSnapshotRules(snapshot *privacy.Snapshot) []privacy.Rule {
    rules := make([]privacy.Rule, snapshot.RuleCount())
    for i := range rules {
        rules[i], _ = snapshot.Rule(i)
    }
    // Snapshot enumeration order is not a public ordering contract; compare
    // management state by ID, independently of matcher source-byte ordering.
    sort.Slice(rules, func(i, j int) bool { return rules[i].ID < rules[j].ID })
    return rules
}

type privacyTestState struct {
    snapshot      *privacy.Snapshot
    config        privacy.Config
    version       uint64
    snapshotRules []privacy.Rule
    rows          []privacy.Rule
    settings      map[model.SettingKey]string
    cached        map[model.SettingKey]string
}

func privacyTestCapture(t *testing.T) privacyTestState {
    t.Helper()
    snapshot := PrivacySnapshot()
    if snapshot == nil {
        t.Fatal("missing privacy snapshot")
    }
    state := privacyTestState{
        snapshot: snapshot, config: snapshot.Config(), version: snapshot.Version(),
        snapshotRules: privacyTestSnapshotRules(snapshot),
        settings:      make(map[model.SettingKey]string), cached: make(map[model.SettingKey]string),
    }
    var rows []model.PrivacyRule
    if err := privacyTestConn().Order("id ASC").Find(&rows).Error; err != nil {
        t.Fatal("read persisted privacy state:", err)
    }
    state.rows = privacyTestNeutralRules(rows)
    for _, key := range privacyTestSettingKeys {
        var setting model.Setting
        if err := privacyTestConn().First(&setting, "key = ?", key).Error; err != nil {
            t.Fatal("read persisted privacy setting:", err)
        }
        state.settings[key] = setting.Value
        value, err := SettingGetString(key)
        if err != nil {
            t.Fatal("read cached privacy setting:", err)
        }
        state.cached[key] = value
    }
    return state
}

func privacyTestPreserved(t *testing.T, before privacyTestState) {
    t.Helper()
    after := privacyTestCapture(t)
    if before.snapshot != after.snapshot || !reflect.DeepEqual(before, after) {
        t.Fatal("failed operation changed snapshot identity/version/rules/config, database, or setting cache")
    }
}

func privacyTestExpectError(t *testing.T, before privacyTestState, got, want error) {
    t.Helper()
    if !errors.Is(got, want) {
        t.Fatalf("error classification = %v, want %v", got, want)
    }
    privacyTestPreserved(t, before)
}

// Fresh/restart/corrupt are three different OS processes sharing only a file in
// the parent's t.TempDir. A second InitDB in a migrated process is not a restart.
func TestPrivacyDB(t *testing.T) {
    if !*octopusPrivacyWorker {
        path := filepath.Join(t.TempDir(), "privacy.db")
        for _, phase := range []string{"fresh", "restart", "corrupt"} {
            if !t.Run(phase, func(t *testing.T) { privacyTestRunWorker(t, "TestPrivacyDB", phase, path) }) {
                return
            }
        }
        // Also reject a legacy TEXT table before any migration can repair it.
        t.Run("legacy_text", func(t *testing.T) { privacyTestRunWorker(t, "TestPrivacyDB", "legacy_text", "") })
        return
    }
    if (*privacyTestPhase == "fresh" || *privacyTestPhase == "restart" || *privacyTestPhase == "corrupt") && *privacyTestDBPath == "" {
        t.Fatal("DB worker requires the parent's temporary database path")
    }
    switch *privacyTestPhase {
    case "fresh":
        privacyTestInit(t, *privacyTestDBPath)
        for _, scenario := range []struct {
            name string
            run  func(*testing.T)
        }{
            {"SchemaAndDefaults", privacyTestSchemaAndDefaults},
            {"CRUDAndExplicitFalse", privacyTestCRUD},
            {"ByteDistinctions", privacyTestBytes},
            {"SQLiteConstraints", privacyTestConstraints},
            {"ConcurrentDuplicate", privacyTestConcurrentDuplicate},
            {"ValidationAndSettings", privacyTestValidation},
            {"DatabaseUniqueDefense", privacyTestDatabaseDefense},
            {"WriteFailure", privacyTestWriteFailure},
            {"CommitFailure", privacyTestCommitFailure},
            {"RefreshRejectsInvalidStoredRule", privacyTestBadStoredRule},
            {"SilentPrivateOperations", privacyTestSilent},
        } {
            if !t.Run(scenario.name, scenario.run) {
                return
            }
        }
        privacyTestSaveRestartFixture(t, *privacyTestDBPath)
    case "restart":
        privacyTestInit(t, *privacyTestDBPath)
        privacyTestCheckRestart(t, *privacyTestDBPath)
    case "corrupt", "legacy_text":
        privacyTestRejectSchema(t, *privacyTestDBPath, *privacyTestPhase)
    default:
        t.Fatal("unknown privacy DB worker phase")
    }
    privacyTestWorkerOK(t, "TestPrivacyDB")
}

func privacyTestSchemaAndDefaults(t *testing.T) {
    var sqliteVersion string
    if err := privacyTestConn().Raw("SELECT sqlite_version()").Scan(&sqliteVersion).Error; err != nil || sqliteVersion == "" {
        t.Fatal("read isolated SQLite version")
    }
    t.Logf("isolated SQLite version: %s", sqliteVersion)
    if err := migrate.ValidatePrivacySchema(privacyTestConn()); err != nil {
        t.Fatal("validate new privacy schema:", err)
    }
    var migration migrate.MigrationRecord
    if err := privacyTestConn().First(&migration, "version = ?", 6).Error; err != nil || migration.Status != migrate.MigrationRecordStatusSuccess {
        t.Fatal("migration 006 is not recorded successful")
    }
    listed, err := PrivacyRuleList(t.Context())
    if err != nil || listed == nil || len(listed) != 0 {
        t.Fatal("empty formal list must be a non-nil empty slice")
    }
    if PrivacySnapshot().Config() != privacy.DefaultConfig() || PrivacySnapshot().Active() {
        t.Fatal("new database did not publish disabled default privacy configuration")
    }
    state := privacyTestCapture(t)
    for key, value := range map[model.SettingKey]string{
        model.SettingKeyPrivacyEnabled: "false", model.SettingKeyPrivacyScopes: "1", model.SettingKeyPrivacyFailurePolicy: "continue",
    } {
        if state.settings[key] != value || state.cached[key] != value {
            t.Fatal("new database privacy defaults differ between database and cache")
        }
    }
}

func privacyTestCRUD(t *testing.T) {
    privacyTestSet(t, model.SettingKeyPrivacyEnabled, "true")
    first := privacyTestCreate(t, "13969909666", "PHONE_1", nil)
    disabled := privacyTestCreate(t, "disabled synthetic value", "DISABLED_1", privacyTestBool(false))
    var persisted model.PrivacyRule
    if err := privacyTestConn().First(&persisted, "id = ?", disabled.ID).Error; err != nil || persisted.Enabled {
        t.Fatal("explicit false was replaced by the database/GORM default true")
    }
    before := privacyTestCapture(t)
    _, err := PrivacyRuleCreate(t.Context(), privacyTestCreateInput(disabled.Source.String(), "OTHER_DISABLED", nil))
    privacyTestExpectError(t, before, err, privacy.ErrRuleConflict)
    _, err = PrivacyRuleCreate(t.Context(), privacyTestCreateInput("other disabled source", disabled.Alias.String(), nil))
    privacyTestExpectError(t, before, err, privacy.ErrRuleConflict)

    old := PrivacySnapshot()
    updated, err := PrivacyRuleUpdate(t.Context(), privacyTestUpdateInput(first.ID, "张三丰", "PERSON_1", privacyTestBool(false)))
    if err != nil || updated.ID != first.ID || updated.Enabled || updated.Source.String() != "张三丰" || updated.Alias.String() != "PERSON_1" {
        t.Fatal("formal full update did not preserve ID and explicit false")
    }
    persisted = model.PrivacyRule{}
    if err := privacyTestConn().First(&persisted, "id = ?", first.ID).Error; err != nil || persisted.Enabled || persisted.Source.String() != "张三丰" {
        t.Fatal("formal false update did not persist all fields")
    }
    if PrivacySnapshot() == old || PrivacySnapshot().Version() <= old.Version() {
        t.Fatal("successful update did not publish a newer snapshot")
    }
    privacyTestAssertText(t, old, "13969909666", "[[OCTOPUS_PII:PHONE_1]]", true)
    privacyTestAssertText(t, PrivacySnapshot(), "张三丰", "张三丰", false)
    privacyTestAssertRestore(t, PrivacySnapshot(), "[[OCTOPUS_PII:PERSON_1]]", "[[OCTOPUS_PII:PERSON_1]]")
    listed, err := PrivacyRuleList(t.Context())
    if err != nil || len(listed) != 2 || listed[0].ID != first.ID || listed[1].ID != disabled.ID || listed[0].Enabled || listed[1].Enabled {
        t.Fatal("formal list did not include disabled rules in ascending ID order")
    }
    if err := PrivacyRuleDelete(t.Context(), first.ID); err != nil {
        t.Fatal("formal delete:", err)
    }
    before = privacyTestCapture(t)
    privacyTestExpectError(t, before, PrivacyRuleDelete(t.Context(), first.ID), privacy.ErrRuleNotFound)
    _, err = PrivacyRuleUpdate(t.Context(), privacyTestUpdateInput(first.ID, "missing", "MISSING", privacyTestBool(true)))
    privacyTestExpectError(t, before, err, privacy.ErrRuleNotFound)
    privacyTestCreate(t, "张三丰", "PERSON_1", nil)        // hard delete releases both keys
    privacyTestCreate(t, "13969909666", "PHONE_1", nil) // full update released the original keys
}

func privacyTestBytes(t *testing.T) {
    fixtures := []string{"a", "A", "ab", "bc", "张三", "é", "e\u0301", "尾随", "尾随 ", "line\nquote\"slash\\", "nul\x00value", strings.Repeat("界", 170) + "ab"}
    for i, source := range fixtures {
        alias := fmt.Sprintf("BYTE_%d", i)
        rule := privacyTestCreate(t, source, alias, nil)
        var rows []model.PrivacyRule
        if err := privacyTestConn().Where("source = ? AND alias = ?", model.NewPrivacyBytes(source), []byte(alias)).Find(&rows).Error; err != nil ||
            len(rows) != 1 || rows[0].ID != rule.ID || rows[0].Source.String() != source {
            t.Fatal("binary equality query did not preserve the exact source bytes")
        }
        var types struct {
            SourceType string
            AliasType  string
            ByteLength int
        }
        if err := privacyTestConn().Raw("SELECT typeof(source) AS source_type, typeof(alias) AS alias_type, length(source) AS byte_length FROM privacy_rules WHERE id = ?", rule.ID).Scan(&types).Error; err != nil ||
            types.SourceType != "blob" || types.AliasType != "blob" || types.ByteLength != len(source) {
            t.Fatal("formal insert was not BLOB-bound or length was not measured in bytes")
        }
    }
    privacyTestCreate(t, "maximum alias synthetic source", strings.Repeat("Z", 64), nil)
}

// Assert driver error codes, never infer conflicts by parsing SQL error text.
func privacyTestSQLiteCode(t *testing.T, err error, want int) {
    t.Helper()
    var coded interface{ Code() int }
    if !errors.As(err, &coded) {
        t.Fatalf("SQLite error lacks Code() (type %T)", err)
    }
    if got := coded.Code(); got != want {
        t.Fatalf("SQLite error code = %d, want %d (synthetic fixture: %v)", got, want, err)
    }
}

func privacyTestConstraints(t *testing.T) {
    const (
        unique     = 2067
        primaryKey = 1555
        check      = 275
        notNull    = 1299
    )
    base := privacyTestCreate(t, "sql unique synthetic source", "SQL_UNIQUE_BASE", privacyTestBool(false))
    cases := []struct {
        name                   string
        source, alias, enabled any
        code                   int
    }{
        {"UniqueSourceEvenDisabled", []byte(base.Source.String()), []byte("SQL_NEW_ALIAS"), 0, unique},
        {"UniqueAliasEvenDisabled", []byte("sql another synthetic source"), []byte(base.Alias.String()), 0, unique},
        {"TextSourceCannotBypassBlobUnique", base.Source.String(), []byte("SQL_TEXT_SOURCE"), 1, check},
        {"TextAliasCannotBypassBlobUnique", []byte("sql text alias source"), base.Alias.String(), 1, check},
        {"EmptySource", []byte{}, []byte("SQL_EMPTY_SOURCE"), 1, check},
        {"OversizeSource", []byte(strings.Repeat("x", 513)), []byte("SQL_LONG_SOURCE"), 1, check},
        {"EmptyAlias", []byte("sql empty alias source"), []byte{}, 1, check},
        {"OversizeAlias", []byte("sql long alias source"), []byte(strings.Repeat("Q", 65)), 1, check},
        {"LowercaseAlias", []byte("sql lowercase alias source"), []byte("invalid"), 1, check},
        {"NonASCIIAlias", []byte("sql nonascii alias source"), []byte("É"), 1, check},
        {"NulAlias", []byte("sql nul alias source"), []byte("SQL\x00NUL"), 1, check},
        {"NullSource", nil, []byte("SQL_NULL_SOURCE"), 1, notNull},
        {"NullAlias", []byte("sql null alias source"), nil, 1, notNull},
        {"NullEnabled", []byte("sql null enabled source"), []byte("SQL_NULL_ENABLED"), nil, notNull},
        {"EnabledTwo", []byte("sql enabled two source"), []byte("SQL_ENABLED_TWO"), 2, check},
        {"EnabledNegative", []byte("sql enabled negative source"), []byte("SQL_ENABLED_NEGATIVE"), -1, check},
        {"EnabledReal", []byte("sql enabled real source"), []byte("SQL_ENABLED_REAL"), 0.5, check},
        {"EnabledText", []byte("sql enabled text source"), []byte("SQL_ENABLED_TEXT"), "true", check},
    }
    for _, tc := range cases {
        t.Run(tc.name, func(t *testing.T) {
            before := privacyTestCapture(t)
            query := "INSERT INTO privacy_rules(source, alias, enabled) VALUES (?, ?, ?)"
            args := []any{tc.source, tc.alias, tc.enabled}
            // 驱动将空 []byte 绑定成 NULL, 显式 SQL 字面量才测试零字节 BLOB.
            switch tc.name {
            case "EmptySource":
                query = "INSERT INTO privacy_rules(source, alias, enabled) VALUES (X'', ?, ?)"
                args = args[1:]
            case "EmptyAlias":
                query = "INSERT INTO privacy_rules(source, alias, enabled) VALUES (?, X'', ?)"
                args = []any{tc.source, tc.enabled}
            }
            err := privacyTestRawExec(query, args...).Error
            privacyTestSQLiteCode(t, err, tc.code)
            privacyTestPreserved(t, before)
        })
    }
    before := privacyTestCapture(t)
    err := privacyTestRawExec("INSERT INTO privacy_rules(id, source, alias, enabled) VALUES (?, ?, ?, 1)", base.ID, []byte("sql duplicate id source"), []byte("SQL_DUP_ID")).Error
    privacyTestSQLiteCode(t, err, primaryKey)
    privacyTestPreserved(t, before)
    // SQLite's CHECK does not validate UTF-8; the official op/load layer must.
}

func privacyTestConcurrentDuplicate(t *testing.T) {
    const count = 16
    start := make(chan struct{})
    results := make(chan error, count)
    var wg sync.WaitGroup
    for i := 0; i < count; i++ {
        wg.Add(1)
        go func() {
            defer wg.Done()
            <-start
            _, err := PrivacyRuleCreate(t.Context(), privacyTestCreateInput("concurrent duplicate synthetic source", "CONCURRENT_DUPLICATE", nil))
            results <- err
        }()
    }
    close(start)
    wg.Wait()
    close(results)
    successes := 0
    for err := range results {
        if err == nil {
            successes++
        } else if !errors.Is(err, privacy.ErrRuleConflict) {
            t.Fatal("unexpected concurrent create failure:", err)
        }
    }
    if successes != 1 {
        t.Fatalf("concurrent duplicate successes = %d, want 1", successes)
    }
    var persisted int64
    if err := privacyTestConn().Model(&model.PrivacyRule{}).Where("source = ?", []byte("concurrent duplicate synthetic source")).Count(&persisted).Error; err != nil || persisted != 1 {
        t.Fatal("concurrent duplicate did not leave exactly one database row")
    }
    listed, err := PrivacyRuleList(t.Context())
    if err != nil {
        t.Fatal("list after concurrent duplicate:", err)
    }
    found := 0
    for _, row := range listed {
        if row.Alias.String() == "CONCURRENT_DUPLICATE" {
            found++
        }
    }
    if found != 1 {
        t.Fatal("concurrent duplicate snapshot/list disagrees with database")
    }
}

func privacyTestValidation(t *testing.T) {
    for _, tc := range []struct{ name, source, alias string }{
        {"EmptySource", "", "INVALID_EMPTY"}, {"LongSource", strings.Repeat("x", 513), "INVALID_LONG"},
        {"InvalidUTF8", string([]byte{0xff}), "INVALID_UTF8"}, {"EmptyAlias", "invalid empty alias", ""},
        {"LowercaseAlias", "invalid lowercase alias", "lowercase"}, {"NulAlias", "invalid nul alias", "BAD\x00ALIAS"},
        {"LongAlias", "invalid long alias", strings.Repeat("K", 65)},
    } {
        t.Run(tc.name, func(t *testing.T) {
            before := privacyTestCapture(t)
            _, err := PrivacyRuleCreate(t.Context(), privacyTestCreateInput(tc.source, tc.alias, nil))
            privacyTestExpectError(t, before, err, privacy.ErrInvalidRule)
        })
    }
    rule := privacyTestCreate(t, "update validation synthetic source", "UPDATE_VALIDATION", nil)
    for _, input := range []model.PrivacyRuleUpdate{
        privacyTestUpdateInput(0, "valid", "VALID", privacyTestBool(true)),
        privacyTestUpdateInput(-1, "valid", "VALID", privacyTestBool(true)),
        privacyTestUpdateInput(rule.ID, "valid", "VALID", nil),
        privacyTestUpdateInput(rule.ID, "", "VALID", privacyTestBool(true)),
        privacyTestUpdateInput(rule.ID, "valid", "bad", privacyTestBool(true)),
    } {
        before := privacyTestCapture(t)
        _, err := PrivacyRuleUpdate(t.Context(), input)
        privacyTestExpectError(t, before, err, privacy.ErrInvalidRule)
    }
    for _, tc := range []struct {
        key    model.SettingKey
        values []string
    }{
        {model.SettingKeyPrivacyEnabled, []string{"TRUE", "1", "null", " true", ""}},
        {model.SettingKeyPrivacyScopes, []string{"-1", "16", "256", "null", "one", ""}},
        {model.SettingKeyPrivacyFailurePolicy, []string{"ABORT", "ignore", "null", ""}},
    } {
        for _, value := range tc.values {
            before := privacyTestCapture(t)
            privacyTestExpectError(t, before, PrivacySetSetting(t.Context(), tc.key, value), privacy.ErrInvalidConfig)
        }
    }
    // Legacy public setters must use the same validation/publication path.
    before := privacyTestCapture(t)
    privacyTestExpectError(t, before, SettingSetString(model.SettingKeyPrivacyEnabled, "1"), privacy.ErrInvalidConfig)
    privacyTestExpectError(t, before, SettingSetInt(model.SettingKeyPrivacyScopes, 16), privacy.ErrInvalidConfig)
    if err := SettingSetInt(model.SettingKeyPrivacyScopes, 15); err != nil {
        t.Fatal("generic integer privacy setter:", err)
    }
    if PrivacySnapshot() == before.snapshot || PrivacySnapshot().Config().Scopes != privacy.AllScopes {
        t.Fatal("generic integer setter bypassed snapshot publication")
    }
    if err := SettingSetString(model.SettingKeyPrivacyFailurePolicy, "abort"); err != nil {
        t.Fatal("generic string privacy setter:", err)
    }
    if PrivacySnapshot().Config().FailurePolicy != privacy.Abort {
        t.Fatal("generic string setter bypassed snapshot configuration")
    }
    privacyTestSet(t, model.SettingKeyPrivacyScopes, "0")
    if PrivacySnapshot().Active() {
        t.Fatal("zero scopes must make privacy inactive")
    }
    privacyTestSet(t, model.SettingKeyPrivacyScopes, "1")
    privacyTestSet(t, model.SettingKeyPrivacyFailurePolicy, "continue")
}

func privacyTestDatabaseDefense(t *testing.T) {
    // Direct external SQL is only a fixture: it is intentionally absent from the
    // candidate snapshot so these conflicts must reach SQLite's final defense.
    privacyTestExec(t, "INSERT INTO privacy_rules(source, alias, enabled) VALUES (?, ?, 0)", []byte("external synthetic source"), []byte("EXTERNAL_ALIAS"))
    t.Cleanup(func() {
        privacyTestExec(t, "DELETE FROM privacy_rules WHERE source = ?", []byte("external synthetic source"))
    })
    before := privacyTestCapture(t)
    _, err := PrivacyRuleCreate(t.Context(), privacyTestCreateInput("external synthetic source", "EXTERNAL_OTHER_ALIAS", nil))
    privacyTestExpectError(t, before, err, privacy.ErrRuleConflict)
    _, err = PrivacyRuleCreate(t.Context(), privacyTestCreateInput("external other source", "EXTERNAL_ALIAS", nil))
    privacyTestExpectError(t, before, err, privacy.ErrRuleConflict)
    rule := privacyTestCreate(t, "db defense update source", "DB_DEFENSE_UPDATE", nil)
    before = privacyTestCapture(t)
    _, err = PrivacyRuleUpdate(t.Context(), privacyTestUpdateInput(rule.ID, "external synthetic source", "DB_DEFENSE_NEW", privacyTestBool(true)))
    privacyTestExpectError(t, before, err, privacy.ErrRuleConflict)
    _, err = PrivacyRuleUpdate(t.Context(), privacyTestUpdateInput(rule.ID, "db defense new source", "EXTERNAL_ALIAS", privacyTestBool(true)))
    privacyTestExpectError(t, before, err, privacy.ErrRuleConflict)
}

func privacyTestFailedMutations(t *testing.T, rule model.PrivacyRule, check func(*testing.T, func() error)) {
    t.Helper()
    mutations := []struct {
        name string
        run  func() error
    }{
        {"Create", func() error {
            _, err := PrivacyRuleCreate(t.Context(), privacyTestCreateInput("failed create synthetic source", "FAILED_CREATE", nil))
            return err
        }},
        {"Update", func() error {
            _, err := PrivacyRuleUpdate(t.Context(), privacyTestUpdateInput(rule.ID, "failed update synthetic source", "FAILED_UPDATE", privacyTestBool(false)))
            return err
        }},
        {"Delete", func() error { return PrivacyRuleDelete(t.Context(), rule.ID) }},
        {"Enabled", func() error { return PrivacySetSetting(t.Context(), model.SettingKeyPrivacyEnabled, "false") }},
        {"Scopes", func() error { return PrivacySetSetting(t.Context(), model.SettingKeyPrivacyScopes, "15") }},
        {"Policy", func() error { return PrivacySetSetting(t.Context(), model.SettingKeyPrivacyFailurePolicy, "abort") }},
        {"GenericStringSetter", func() error { return SettingSetString(model.SettingKeyPrivacyEnabled, "false") }},
        {"GenericIntSetter", func() error { return SettingSetInt(model.SettingKeyPrivacyScopes, 15) }},
    }
    for _, mutation := range mutations {
        t.Run(mutation.name, func(t *testing.T) { check(t, mutation.run) })
    }
}

func privacyTestWriteFailure(t *testing.T) {
    rule := privacyTestCreate(t, "write failure retained source", "WRITE_FAILURE_RETAINED", nil)
    // Real SQLite rejects the actual production write, not a stubbed op function.
    for _, table := range []string{"privacy_rules", "settings"} {
        for _, event := range []string{"INSERT", "UPDATE", "DELETE"} {
            name := "privacy_test_abort_" + table + "_" + strings.ToLower(event)
            privacyTestExec(t, "CREATE TRIGGER "+name+" BEFORE "+event+" ON "+table+" BEGIN SELECT RAISE(ABORT, 'privacy_test_write_rejected'); END")
            t.Cleanup(func() { privacyTestExec(t, "DROP TRIGGER "+name) })
        }
    }
    privacyTestFailedMutations(t, rule, func(t *testing.T, run func() error) {
        before := privacyTestCapture(t)
        if err := run(); err == nil {
            t.Fatal("formal mutation ignored a SQLite write failure")
        }
        privacyTestPreserved(t, before)
    })
}

var privacyTestCommitError = errors.New("privacy test injected commit failure")

// A test-only transaction boundary decorator. All reads/writes/BEGIN use the
// real SQLite driver; only COMMIT is fault-injected. Roll back the underlying
// transaction before returning the fault so no test connection remains poisoned.
// This is not evidence for any real MySQL/PostgreSQL/driver commit failure mode.
type privacyTestCommitPool struct {
    gorm.ConnPool
    sqlDB    *sql.DB
    attempts int
    writes   int
}

func (pool *privacyTestCommitPool) BeginTx(ctx context.Context, opts *sql.TxOptions) (gorm.ConnPool, error) {
    tx, err := pool.sqlDB.BeginTx(ctx, opts)
    if err != nil {
        return nil, err
    }
    return &privacyTestCommitTx{Tx: tx, pool: pool}, nil
}

func (pool *privacyTestCommitPool) GetDBConn() (*sql.DB, error) { return pool.sqlDB, nil }

type privacyTestCommitTx struct {
    *sql.Tx
    pool   *privacyTestCommitPool
    writes int
}

func privacyTestIsWrite(query string) bool {
    upper := strings.ToUpper(strings.TrimSpace(query))
    return strings.HasPrefix(upper, "INSERT ") || strings.HasPrefix(upper, "UPDATE ") || strings.HasPrefix(upper, "DELETE ")
}

func (tx *privacyTestCommitTx) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
    result, err := tx.Tx.ExecContext(ctx, query, args...)
    if err == nil && privacyTestIsWrite(query) {
        tx.writes++
    }
    return result, err
}

func (tx *privacyTestCommitTx) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
    rows, err := tx.Tx.QueryContext(ctx, query, args...)
    // SQLite INSERT ... RETURNING runs through QueryContext in GORM.
    if err == nil && privacyTestIsWrite(query) {
        tx.writes++
    }
    return rows, err
}

func (tx *privacyTestCommitTx) Commit() error {
    tx.pool.attempts++
    tx.pool.writes += tx.writes
    if err := tx.Tx.Rollback(); err != nil {
        return err
    }
    return privacyTestCommitError
}

func privacyTestCommitFailure(t *testing.T) {
    rule := privacyTestCreate(t, "commit failure retained source", "COMMIT_FAILURE_RETAINED", nil)
    privacyTestFailedMutations(t, rule, func(t *testing.T, run func() error) {
        before := privacyTestCapture(t)
        conn := db.GetDB()
        sqlDB, err := conn.DB()
        if err != nil {
            t.Fatal("get actual SQL pool:", err)
        }
        original, statementOriginal := conn.ConnPool, conn.Statement.ConnPool
        pool := &privacyTestCommitPool{ConnPool: original, sqlDB: sqlDB}
        conn.ConnPool, conn.Statement.ConnPool = pool, pool
        restore := func() { conn.ConnPool, conn.Statement.ConnPool = original, statementOriginal }
        defer restore()
        err = run()
        restore()
        if err == nil || pool.attempts != 1 || pool.writes == 0 {
            t.Fatal("mutation did not perform real SQL writes and propagate the injected Commit failure")
        }
        privacyTestPreserved(t, before)
    })
    // The failed candidate did not reserve either key or poison subsequent work.
    recovered := privacyTestCreate(t, "failed create synthetic source", "FAILED_CREATE", nil)
    if err := PrivacyRuleDelete(t.Context(), recovered.ID); err != nil {
        t.Fatal("write after Commit failure:", err)
    }
}

func privacyTestBadStoredRule(t *testing.T) {
    rule := privacyTestCreate(t, "refresh retained source", "REFRESH_RETAINED", nil)
    privacyTestExec(t, "UPDATE privacy_rules SET source = ? WHERE id = ?", []byte{0xff, 0xfe}, rule.ID)
    t.Cleanup(func() {
        privacyTestExec(t, "UPDATE privacy_rules SET source = ? WHERE id = ?", []byte(rule.Source.String()), rule.ID)
    })
    // Capture after fixture corruption: refresh must not publish malformed data
    // and must not overwrite any cached setting or mutate the old rule container.
    before := privacyTestCapture(t)
    privacyTestExpectError(t, before, PrivacyRefresh(t.Context()), privacy.ErrInvalidRule)
}

// Count even Trace calls: a parameter-redacting logger is insufficient; every
// privacy query/transaction is required to explicitly use logger.Discard.
type privacyTestLogger struct {
    mu    sync.Mutex
    calls int
}

func (l *privacyTestLogger) LogMode(logger.LogLevel) logger.Interface                        { return l }
func (l *privacyTestLogger) hit()                                                            { l.mu.Lock(); l.calls++; l.mu.Unlock() }
func (l *privacyTestLogger) Info(context.Context, string, ...any)                            { l.hit() }
func (l *privacyTestLogger) Warn(context.Context, string, ...any)                            { l.hit() }
func (l *privacyTestLogger) Error(context.Context, string, ...any)                           { l.hit() }
func (l *privacyTestLogger) Trace(context.Context, time.Time, func() (string, int64), error) { l.hit() }
func (l *privacyTestLogger) count() int                                                      { l.mu.Lock(); defer l.mu.Unlock(); return l.calls }

func privacyTestSilent(t *testing.T) {
    conn := db.GetDB()
    original := conn.Logger
    spy := &privacyTestLogger{}
    conn.Logger = spy
    defer func() { conn.Logger = original }()
    if err := migrate.ValidatePrivacySchema(conn); err != nil {
        t.Fatal("schema validation failed")
    }
    if _, err := migrate.PrivacySchemaExists(conn); err != nil {
        t.Fatal("schema existence check failed")
    }
    rule, err := PrivacyRuleCreate(t.Context(), privacyTestCreateInput(privacyTestSecretSource, privacyTestSecretAlias, nil))
    if err != nil {
        t.Fatal("silent create failed")
    }
    if _, err := PrivacyRuleList(t.Context()); err != nil {
        t.Fatal("silent list failed")
    }
    if err := PrivacyRefresh(t.Context()); err != nil {
        t.Fatal("silent refresh failed")
    }
    if _, err := PrivacyRuleUpdate(t.Context(), privacyTestUpdateInput(rule.ID, privacyTestSecretSource, privacyTestSecretAlias, privacyTestBool(false))); err != nil {
        t.Fatal("silent update failed")
    }
    if _, err := PrivacyRuleCreate(t.Context(), privacyTestCreateInput(privacyTestSecretSource, "SILENT_CONFLICT", nil)); !errors.Is(err, privacy.ErrRuleConflict) {
        t.Fatal("silent conflict classification failed")
    }
    if err := PrivacySetSetting(t.Context(), model.SettingKeyPrivacyScopes, "15"); err != nil {
        t.Fatal("silent setting write failed")
    }
    if err := PrivacyRuleDelete(t.Context(), rule.ID); err != nil {
        t.Fatal("silent delete failed")
    }
    // Exercise both actual SQLite error paths, not just candidate conflicts.
    privacyTestExec(t, "INSERT INTO privacy_rules(source, alias, enabled) VALUES (?, ?, 0)", []byte(privacyTestSecretSource), []byte(privacyTestSecretAlias))
    if _, err := PrivacyRuleCreate(t.Context(), privacyTestCreateInput(privacyTestSecretSource, "SILENT_DB_CONFLICT", nil)); !errors.Is(err, privacy.ErrRuleConflict) {
        t.Fatal("silent database conflict classification failed")
    }
    privacyTestExec(t, "DELETE FROM privacy_rules WHERE source = ?", []byte(privacyTestSecretSource))
    privacyTestExec(t, "CREATE TRIGGER privacy_test_silent_abort BEFORE INSERT ON privacy_rules BEGIN SELECT RAISE(ABORT, 'privacy_test_write_rejected'); END")
    _, writeErr := PrivacyRuleCreate(t.Context(), privacyTestCreateInput(privacyTestSecretSource, privacyTestSecretAlias, nil))
    privacyTestExec(t, "DROP TRIGGER privacy_test_silent_abort")
    if writeErr == nil {
        t.Fatal("silent SQLite write rejection was ignored")
    }
    if strings.Contains(writeErr.Error(), privacyTestSecretSource) || strings.Contains(writeErr.Error(), privacyTestSecretAlias) || strings.Contains(writeErr.Error(), "privacy_test_write_rejected") {
        t.Fatal("privacy op returned raw database error details")
    }
    if err := PrivacySetSetting(t.Context(), model.SettingKeyPrivacyScopes, "1"); err != nil {
        t.Fatal("silent setting restore failed")
    }
    if spy.count() != 0 {
        t.Fatal("a private SQL operation reached the nonprivacy logger")
    }
    var one int
    if err := conn.Raw("SELECT 1").Scan(&one).Error; err != nil || one != 1 || spy.count() == 0 {
        t.Fatal("privacy silence was implemented by globally suppressing other SQL logging")
    }
}

// Persist an independent, literal restart expectation beside the temporary DB.
// It contains only synthetic fixture data, never application backups or DSNs.
type privacyTestManifest struct {
    Rules      []privacy.Rule
    Config     privacy.Config
    Settings   map[model.SettingKey]string
    Schema     []privacyTestSchemaRow
    Migrations []migrate.MigrationRecord
}

type privacyTestSchemaRow struct {
    Type string
    Name string
    SQL  string
}

func privacyTestSchemaRows(t *testing.T, conn *gorm.DB) []privacyTestSchemaRow {
    t.Helper()
    var rows []privacyTestSchemaRow
    if err := conn.Raw("SELECT type, name, sql FROM sqlite_master WHERE tbl_name = 'privacy_rules' AND sql IS NOT NULL ORDER BY type, name").Scan(&rows).Error; err != nil {
        t.Fatal("read privacy SQLite definitions:", err)
    }
    return rows
}

func privacyTestMigrations(t *testing.T, conn *gorm.DB) []migrate.MigrationRecord {
    t.Helper()
    var rows []migrate.MigrationRecord
    if err := conn.Order("version ASC").Find(&rows).Error; err != nil {
        t.Fatal("read migration records:", err)
    }
    return rows
}

func privacyTestSaveRestartFixture(t *testing.T, path string) {
    listed, err := PrivacyRuleList(t.Context())
    if err != nil {
        t.Fatal("list before restart fixture:", err)
    }
    for _, rule := range listed {
        if err := PrivacyRuleDelete(t.Context(), rule.ID); err != nil {
            t.Fatal("remove isolated fixture rule:", err)
        }
    }
    privacyTestCreate(t, "13969909666", "PHONE_RESTART", nil)
    privacyTestCreate(t, "张三\x00 ", strings.Repeat("R", 64), privacyTestBool(false))
    privacyTestSet(t, model.SettingKeyPrivacyEnabled, "true")
    privacyTestSet(t, model.SettingKeyPrivacyScopes, "15")
    privacyTestSet(t, model.SettingKeyPrivacyFailurePolicy, "abort")
    state := privacyTestCapture(t)
    if !reflect.DeepEqual(state.rows, state.snapshotRules) || !reflect.DeepEqual(state.settings, state.cached) {
        t.Fatal("restart fixture is not consistently published")
    }
    manifest := privacyTestManifest{Rules: state.rows, Config: state.config, Settings: state.settings,
        Schema: privacyTestSchemaRows(t, privacyTestConn()), Migrations: privacyTestMigrations(t, privacyTestConn())}
    data, err := json.Marshal(manifest)
    if err != nil {
        t.Fatal("encode temporary restart expectation:", err)
    }
    if err := os.WriteFile(path+".expected.json", data, 0600); err != nil {
        t.Fatal("write temporary restart expectation:", err)
    }
}

func privacyTestCheckRestart(t *testing.T, path string) {
    data, err := os.ReadFile(path + ".expected.json")
    if err != nil {
        t.Fatal("read temporary restart expectation:", err)
    }
    var expected privacyTestManifest
    if err := json.Unmarshal(data, &expected); err != nil {
        t.Fatal("decode temporary restart expectation:", err)
    }
    state := privacyTestCapture(t)
    if !reflect.DeepEqual(state.rows, expected.Rules) || !reflect.DeepEqual(state.snapshotRules, expected.Rules) ||
        state.config != expected.Config || !reflect.DeepEqual(state.settings, expected.Settings) || !reflect.DeepEqual(state.cached, expected.Settings) {
        t.Fatal("independent restart changed IDs, bytes, explicit false, rules, or configuration")
    }
    if !reflect.DeepEqual(privacyTestSchemaRows(t, privacyTestConn()), expected.Schema) ||
        !reflect.DeepEqual(privacyTestMigrations(t, privacyTestConn()), expected.Migrations) {
        t.Fatal("restart rewrote the privacy schema or migration records")
    }
    privacyTestAssertText(t, PrivacySnapshot(), "13969909666", "[[OCTOPUS_PII:PHONE_RESTART]]", true)
    privacyTestAssertRestore(t, PrivacySnapshot(), "[[OCTOPUS_PII:PHONE_RESTART]]", "13969909666")
    privacyTestAssertRestore(t, PrivacySnapshot(), "[[OCTOPUS_PII:"+strings.Repeat("R", 64)+"]]", "[[OCTOPUS_PII:"+strings.Repeat("R", 64)+"]]")
    previous := PrivacySnapshot()
    created := privacyTestCreate(t, "restart next auto id source", "RESTART_NEXT_ID", nil)
    for _, rule := range expected.Rules {
        if created.ID <= rule.ID {
            t.Fatal("post-restart autoincrement did not exceed persisted IDs")
        }
    }
    if PrivacySnapshot().Version() <= previous.Version() {
        t.Fatal("post-restart create did not publish a newer version")
    }
}

func privacyTestRejectSchema(t *testing.T, path, phase string) {
    if path == "" {
        path = filepath.Join(t.TempDir(), "bad-privacy.db")
    }
    conn, err := gorm.Open(sqlite.Open(path), &gorm.Config{Logger: logger.Discard})
    if err != nil {
        t.Fatal("open isolated schema fixture:", err)
    }
    sqlDB, err := conn.DB()
    if err != nil {
        t.Fatal("get isolated schema fixture pool:", err)
    }
    defer sqlDB.Close()
    if phase == "corrupt" {
        // Same index name, unique, correct column, but partial: do not let
        // AutoMigrate replace it and silently restore a different constraint.
        if err := conn.Exec("DROP INDEX ux_privacy_rules_alias").Error; err != nil {
            t.Fatal("corrupt isolated index:", err)
        }
        if err := conn.Exec("CREATE UNIQUE INDEX ux_privacy_rules_alias ON privacy_rules(alias) WHERE enabled = 1").Error; err != nil {
            t.Fatal("create partial isolated index:", err)
        }
    } else {
        if err := conn.Exec("CREATE TABLE privacy_rules(id INTEGER PRIMARY KEY, source TEXT, alias TEXT, enabled NUMERIC)").Error; err != nil {
            t.Fatal("create incompatible isolated table:", err)
        }
        if err := conn.Exec("INSERT INTO privacy_rules VALUES (17, 'legacy synthetic text', 'LEGACY_TEXT', 0)").Error; err != nil {
            t.Fatal("seed incompatible isolated table:", err)
        }
    }
    beforeSchema := privacyTestSchemaRows(t, conn)
    var beforeData []struct {
        ID      int
        Source  []byte
        Alias   []byte
        Enabled int
    }
    if err := conn.Raw("SELECT id, CAST(source AS BLOB) AS source, CAST(alias AS BLOB) AS alias, enabled FROM privacy_rules ORDER BY id").Scan(&beforeData).Error; err != nil {
        t.Fatal("read bad fixture rows:", err)
    }
    var beforeMigrations []migrate.MigrationRecord
    if phase == "corrupt" {
        beforeMigrations = privacyTestMigrations(t, conn)
    }
    // This is the only InitDB call in this worker; its fixed error must survive
    // wrapping. No InitCache call is appropriate after startup rejection.
    err = db.InitDB("sqlite", path, true)
    t.Cleanup(func() {
        if db.GetDB() != nil {
            if err := db.Close(); err != nil {
                t.Error("close rejected initialization:", err)
            }
        }
    })
    if !errors.Is(err, migrate.ErrPrivacySchema) {
        t.Fatalf("incompatible startup error = %v, want ErrPrivacySchema", err)
    }
    if PrivacySnapshot() != nil {
        t.Fatal("rejected startup unexpectedly published a snapshot")
    }
    if !reflect.DeepEqual(beforeSchema, privacyTestSchemaRows(t, conn)) {
        t.Fatal("startup repaired/rewrote an incompatible privacy schema before rejecting it")
    }
    var afterData []struct {
        ID      int
        Source  []byte
        Alias   []byte
        Enabled int
    }
    if err := conn.Raw("SELECT id, CAST(source AS BLOB) AS source, CAST(alias AS BLOB) AS alias, enabled FROM privacy_rules ORDER BY id").Scan(&afterData).Error; err != nil || !reflect.DeepEqual(beforeData, afterData) {
        t.Fatal("rejected startup modified existing fixture data")
    }
    if phase == "corrupt" {
        if !reflect.DeepEqual(beforeMigrations, privacyTestMigrations(t, conn)) {
            t.Fatal("rejected startup ran/rewrote migration records")
        }
    } else if conn.Migrator().HasTable(&migrate.MigrationRecord{}) {
        t.Fatal("incompatible existing table was rejected only after migrations started")
    }
}

func privacyTestAssertText(t *testing.T, snapshot *privacy.Snapshot, input, expected string, changed bool) {
    t.Helper()
    output, gotChanged, err := snapshot.Replace(input)
    if err != nil || output != expected || gotChanged != changed {
        t.Fatal("snapshot replacement differs from literal expectation")
    }
}

func privacyTestRestore(snapshot *privacy.Snapshot, input string) string {
    restorer := privacy.NewRestorer(snapshot)
    output, _ := restorer.Push(input)
    tail, _ := restorer.Finish()
    return output + tail
}

func privacyTestAssertRestore(t *testing.T, snapshot *privacy.Snapshot, input, expected string) {
    t.Helper()
    if privacyTestRestore(snapshot, input) != expected {
        t.Fatal("snapshot reverse lookup differs from literal expectation")
    }
}

// A fresh isolated suite, no cache-reset API and no t.Parallel. Long alias makes
// private reverse-map/L immutability observable through the public restorer.
func TestPrivacySnapshot(t *testing.T) {
    if !*octopusPrivacyWorker {
        privacyTestRunWorker(t, "TestPrivacySnapshot", "snapshot", "")
        return
    }
    if *privacyTestPhase != "snapshot" {
        t.Fatal("unknown snapshot worker phase")
    }
    privacyTestInit(t, "")
    privacyTestSet(t, model.SettingKeyPrivacyEnabled, "true")
    privacyTestSet(t, model.SettingKeyPrivacyScopes, "15")
    oldAlias := strings.Repeat("X", 64)
    oldMarker := "[[OCTOPUS_PII:" + oldAlias + "]]"
    rule := privacyTestCreate(t, "13969909666", oldAlias, nil)
    old := PrivacySnapshot()
    oldConfig, oldVersion, oldRules := old.Config(), old.Version(), privacyTestSnapshotRules(old)
    // Request/flow already returned but only its prefix consumed. It must not
    // lose its reverse table or longest-marker boundary during management writes.
    pending := privacy.NewRestorer(old)
    prefix, changed := pending.Push(oldMarker[:len(oldMarker)-1])
    if prefix != "" || changed || !pending.HasPending() {
        t.Fatal("old longest-marker prefix was not retained")
    }

    const readers = 8
    type pulse struct {
        snapshot *privacy.Snapshot
        ack      chan error
    }
    pulses := make([]chan pulse, readers)
    stop := make(chan struct{})
    failures := make(chan error, readers)
    var wg sync.WaitGroup
    t.Cleanup(func() {
        close(stop)
        wg.Wait()
        close(failures)
        for err := range failures {
            t.Error("concurrent snapshot reader:", err)
        }
    })
    checkOld := func() error {
        if old.Config() != oldConfig || old.Version() != oldVersion || !reflect.DeepEqual(privacyTestSnapshotRules(old), oldRules) {
            return errors.New("old snapshot metadata mutated")
        }
        output, changed, err := old.Replace("13969909666")
        if err != nil || output != oldMarker || !changed || privacyTestRestore(old, oldMarker) != "13969909666" {
            return errors.New("old snapshot matcher/reverse table mutated")
        }
        if privacyTestRestore(old, "[[OCTOPUS_PII:N]]") != "[[OCTOPUS_PII:N]]" {
            return errors.New("new alias entered old reverse table")
        }
        return nil
    }
    for i := range pulses {
        pulses[i] = make(chan pulse)
        wg.Add(1)
        go func(commands <-chan pulse) {
            defer wg.Done()
            for {
                select {
                case <-stop:
                    return
                case command := <-commands:
                    err := checkOld()
                    if err == nil && PrivacySnapshot() != command.snapshot {
                        err = errors.New("new request did not see the published snapshot")
                    }
                    if err == nil {
                        err = privacyTestCheckSnapshot(command.snapshot)
                    }
                    command.ack <- err
                default:
                    if err := checkOld(); err != nil {
                        failures <- err
                        return
                    }
                    if err := privacyTestCheckSnapshot(PrivacySnapshot()); err != nil {
                        failures <- err
                        return
                    }
                    runtime.Gosched()
                }
            }
        }(pulses[i])
    }
    previous := old
    published := func() {
        t.Helper()
        current := PrivacySnapshot()
        if current == previous || current.Version() <= previous.Version() {
            t.Fatal("formal mutation did not publish a new snapshot/version")
        }
        for _, commands := range pulses {
            ack := make(chan error, 1)
            select {
            case commands <- pulse{snapshot: current, ack: ack}:
            case err := <-failures:
                t.Fatal("concurrent snapshot reader:", err)
            case <-t.Context().Done():
                t.Fatal("snapshot worker canceled")
            }
            select {
            case err := <-ack:
                if err != nil {
                    t.Fatal("published snapshot reader:", err)
                }
            case err := <-failures:
                t.Fatal("concurrent snapshot reader:", err)
            case <-t.Context().Done():
                t.Fatal("snapshot worker canceled")
            }
        }
        previous = current
    }
    var intermediate *privacy.Snapshot
    for round := 0; round < 24; round++ {
        updated, err := PrivacyRuleUpdate(t.Context(), privacyTestUpdateInput(rule.ID, "张三丰", "N", privacyTestBool(true)))
        if err != nil {
            t.Fatal("concurrent formal update:", err)
        }
        rule = updated
        published()
        if round == 0 {
            intermediate = PrivacySnapshot()
            shortBoundary := privacy.NewRestorer(intermediate)
            literal, changed := shortBoundary.Push(oldMarker[:len(oldMarker)-1])
            if literal != oldMarker[:len(oldMarker)-1] || changed || shortBoundary.HasPending() {
                t.Fatal("new snapshot retained the old longest-marker boundary")
            }
            shortBoundary.Finish()
        }
        privacyTestAssertText(t, PrivacySnapshot(), "张三丰", "[[OCTOPUS_PII:N]]", true)
        privacyTestAssertRestore(t, PrivacySnapshot(), oldMarker, oldMarker)
        updated, err = PrivacyRuleUpdate(t.Context(), privacyTestUpdateInput(rule.ID, "张三丰", "N", privacyTestBool(false)))
        if err != nil {
            t.Fatal("concurrent formal disable:", err)
        }
        rule = updated
        published()
        privacyTestAssertText(t, PrivacySnapshot(), "张三丰", "张三丰", false)
        privacyTestAssertRestore(t, PrivacySnapshot(), "[[OCTOPUS_PII:N]]", "[[OCTOPUS_PII:N]]")
        if err := PrivacyRuleDelete(t.Context(), rule.ID); err != nil {
            t.Fatal("concurrent formal delete:", err)
        }
        published()
        if PrivacySnapshot().RuleCount() != 0 {
            t.Fatal("hard delete left a rule in the new snapshot")
        }
        rule = privacyTestCreate(t, "张三", "N", nil) // alias reuse changes only new requests
        published()
        privacyTestAssertRestore(t, PrivacySnapshot(), "[[OCTOPUS_PII:N]]", "张三")
        privacyTestSet(t, model.SettingKeyPrivacyEnabled, "false")
        published()
        privacyTestSet(t, model.SettingKeyPrivacyScopes, "0")
        published()
        privacyTestSet(t, model.SettingKeyPrivacyFailurePolicy, "abort")
        published()
        privacyTestSet(t, model.SettingKeyPrivacyEnabled, "true")
        published()
        privacyTestSet(t, model.SettingKeyPrivacyScopes, "15")
        published()
        privacyTestSet(t, model.SettingKeyPrivacyFailurePolicy, "continue")
        published()
    }
    if err := checkOld(); err != nil {
        t.Fatal(err)
    }
    privacyTestAssertRestore(t, intermediate, "[[OCTOPUS_PII:N]]", "张三丰")
    suffix, changed := pending.Push(oldMarker[len(oldMarker)-1:])
    tail, tailChanged := pending.Finish()
    if suffix+tail != "13969909666" || !changed || tailChanged || pending.HasPending() {
        t.Fatal("held old flow lost its map/L or was released before consumption finished")
    }
    select {
    case err := <-failures:
        t.Fatal("concurrent snapshot reader:", err)
    default:
    }
    privacyTestWorkerOK(t, "TestPrivacySnapshot")
}

// Independent literal expectation for a single-rule snapshot captured by a
// reader. Never obtain expected text by running the matcher/restorer under test.
func privacyTestCheckSnapshot(snapshot *privacy.Snapshot) error {
    if snapshot == nil {
        return errors.New("reader observed a nil published snapshot")
    }
    if snapshot.RuleCount() == 0 {
        output, changed, err := snapshot.Replace("13969909666")
        if snapshot.Active() || err != nil || changed || output != "13969909666" {
            return errors.New("empty snapshot is active or changes text")
        }
        return nil
    }
    if snapshot.RuleCount() != 1 {
        return errors.New("single-rule snapshot was partially published")
    }
    rule, ok := snapshot.Rule(0)
    if !ok {
        return errors.New("published rule disappeared")
    }
    marker := "[[OCTOPUS_PII:" + rule.Alias + "]]"
    active := snapshot.Config().Enabled && snapshot.Config().Scopes != 0 && rule.Enabled
    expectedText, expectedRestore := rule.Source, marker
    if active {
        expectedText, expectedRestore = marker, rule.Source
    }
    output, changed, err := snapshot.Replace(rule.Source)
    if snapshot.Active() != active || err != nil || output != expectedText || changed != active || privacyTestRestore(snapshot, marker) != expectedRestore {
        return errors.New("captured snapshot mixes rule, matcher, reverse map, or configuration versions")
    }
    return nil
}
