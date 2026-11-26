package op

import (
    "bytes"
    "context"
    "errors"
    "flag"
    "net/url"
    "os"
    "os/exec"
    "reflect"
    "regexp"
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
    mysqlDriver "github.com/go-sql-driver/mysql"
    "github.com/jackc/pgx/v5/pgconn"
    "gorm.io/driver/mysql"
    "gorm.io/driver/postgres"
    "gorm.io/gorm"
    "gorm.io/gorm/logger"
)

const (
    privacyDatabaseGateEnv     = "OCTOPUS_PRIVACY_DB_TEST"
    privacyDatabaseMySQLEnv    = "OCTOPUS_PRIVACY_MYSQL_DSN"
    privacyDatabasePostgresEnv = "OCTOPUS_PRIVACY_POSTGRES_DSN"
)

var privacyDatabaseConfigError = errors.New("privacy database test configuration rejected")

// 配置只持有门禁后的 DSN, 不实现 String, 不作为测试输出参数.
type privacyDatabaseTarget struct {
    driver string
    name   string
    dsn    string
}

type privacyDatabaseTargets struct {
    mysql    privacyDatabaseTarget
    postgres privacyDatabaseTarget
}

// 非精确 1 时不调用读取函数. 两库必须同时通过纯配置校验, 才能启动任一 worker.
func privacyDatabaseLoadConfig(gate string, getenv func(string) string) (privacyDatabaseTargets, bool, error) {
    if gate != "1" {
        return privacyDatabaseTargets{}, false, nil
    }
    targets, err := privacyDatabaseParseConfig(getenv(privacyDatabaseMySQLEnv), getenv(privacyDatabasePostgresEnv))
    return targets, true, err
}

func privacyDatabaseValidName(name string) bool {
    return len(name) <= 63 && regexp.MustCompile(`^octopus_privacy_test_[A-Za-z0-9_]+$`).MatchString(name)
}

// 不调用 pgconn.ParseConfig: 它会读取 PG 环境, service 文件和真实 .pgpass.
// PostgreSQL 接受 URL 或带单引号/反斜杠转义的 keyword DSN, 拒绝重复字段和旁路配置.
func privacyDatabasePostgresFields(dsn string) (map[string]string, error) {
    fields := make(map[string]string)
    put := func(key, value string) bool {
        if key == "dbname" {
            key = "database"
        }
        switch key {
        case "database", "host", "port", "user", "password", "sslmode", "connect_timeout", "application_name":
        case "search_path":
            if value != "public" {
                return false
            }
        case "passfile", "servicefile":
            if value != os.DevNull {
                return false
            }
        case "sslkey", "sslcert", "sslrootcert":
            if value != "" {
                return false
            }
        default:
            return false
        }
        if _, exists := fields[key]; exists || strings.ContainsRune(value, '\x00') {
            return false
        }
        fields[key] = value
        return true
    }
    if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
        parsed, err := url.Parse(dsn)
        if err != nil || parsed.Opaque != "" || parsed.Fragment != "" || strings.Contains(parsed.Host, ",") {
            return nil, privacyDatabaseConfigError
        }
        if parsed.User != nil {
            if !put("user", parsed.User.Username()) {
                return nil, privacyDatabaseConfigError
            }
            password, _ := parsed.User.Password()
            if !put("password", password) {
                return nil, privacyDatabaseConfigError
            }
        }
        if parsed.Hostname() != "" && !put("host", parsed.Hostname()) {
            return nil, privacyDatabaseConfigError
        }
        if parsed.Port() != "" && !put("port", parsed.Port()) {
            return nil, privacyDatabaseConfigError
        }
        if parsed.Path != "" && !put("database", strings.TrimPrefix(parsed.Path, "/")) {
            return nil, privacyDatabaseConfigError
        }
        query, err := url.ParseQuery(parsed.RawQuery)
        if err != nil {
            return nil, privacyDatabaseConfigError
        }
        for key, values := range query {
            if len(values) != 1 || !put(key, values[0]) {
                return nil, privacyDatabaseConfigError
            }
        }
        return fields, nil
    }
    const space = " \t\r\n\v\f"
    for rest := strings.TrimLeft(dsn, space); rest != ""; rest = strings.TrimLeft(rest, space) {
        eq := strings.IndexByte(rest, '=')
        if eq < 1 {
            return nil, privacyDatabaseConfigError
        }
        key := strings.TrimSpace(rest[:eq])
        rest = strings.TrimLeft(rest[eq+1:], space)
        var value strings.Builder
        quoted := strings.HasPrefix(rest, "'")
        if quoted {
            rest = rest[1:]
        }
        closed := !quoted
        for len(rest) > 0 {
            ch := rest[0]
            if ch == '\\' {
                if len(rest) < 2 {
                    return nil, privacyDatabaseConfigError
                }
                value.WriteByte(rest[1])
                rest = rest[2:]
                continue
            }
            if quoted && ch == '\'' {
                rest, closed = rest[1:], true
                break
            }
            if !quoted && strings.ContainsRune(space, rune(ch)) {
                break
            }
            value.WriteByte(ch)
            rest = rest[1:]
        }
        if !closed || (quoted && rest != "" && !strings.ContainsRune(space, rune(rest[0]))) || !put(key, value.String()) {
            return nil, privacyDatabaseConfigError
        }
    }
    return fields, nil
}

func privacyDatabaseParseConfig(mysqlDSN, postgresDSN string) (privacyDatabaseTargets, error) {
    reject := func() (privacyDatabaseTargets, error) { return privacyDatabaseTargets{}, privacyDatabaseConfigError }
    if strings.TrimSpace(mysqlDSN) == "" || strings.TrimSpace(postgresDSN) == "" {
        return reject()
    }
    mc, err := mysqlDriver.ParseDSN(mysqlDSN)
    if err != nil || !privacyDatabaseValidName(mc.DBName) || (mc.Net != "tcp" && mc.Net != "unix") || mc.MultiStatements || mc.AllowAllFiles {
        return reject()
    }
    // 不允许通过任意 SET 参数, 自定义拨号器或文件导入扩大专用库验证范围.
    for key, value := range mc.Params {
        if key != "charset" || value != "utf8mb4" {
            return reject()
        }
    }
    mc.ParseTime = true
    if mc.Timeout == 0 {
        mc.Timeout = 5 * time.Second
    }
    if mc.ReadTimeout == 0 {
        mc.ReadTimeout = 10 * time.Second
    }
    if mc.WriteTimeout == 0 {
        mc.WriteTimeout = 10 * time.Second
    }
    if mc.Params == nil {
        mc.Params = make(map[string]string)
    }
    mc.Params["charset"] = "utf8mb4"
    pf, err := privacyDatabasePostgresFields(postgresDSN)
    if err != nil || !privacyDatabaseValidName(pf["database"]) || pf["host"] == "" || pf["user"] == "" || strings.Contains(pf["host"], ",") {
        return reject()
    }
    if pf["port"] == "" {
        pf["port"] = "5432"
    }
    if port, err := strconv.Atoi(pf["port"]); err != nil || port < 1 || port > 65535 {
        return reject()
    }
    if pf["connect_timeout"] == "" {
        pf["connect_timeout"] = "5"
    }
    if seconds, err := strconv.Atoi(pf["connect_timeout"]); err != nil || seconds < 1 || seconds > 60 {
        return reject()
    }
    if pf["sslmode"] == "" {
        pf["sslmode"] = "require"
    }
    switch pf["sslmode"] {
    case "disable", "require", "verify-ca", "verify-full":
    default:
        return reject()
    }
    // 固定 public 命名空间. 凭证只能来自已授权 DSN, 不读取服务文件或凭证文件.
    if _, present := pf["password"]; !present {
        pf["password"] = ""
    }
    pf["application_name"] = "octopus_privacy_database_test"
    pf["search_path"], pf["passfile"], pf["servicefile"] = "public", os.DevNull, os.DevNull
    pf["sslkey"], pf["sslcert"], pf["sslrootcert"] = "", "", ""
    keys := make([]string, 0, len(pf))
    for key := range pf {
        keys = append(keys, key)
    }
    sort.Strings(keys)
    parts := make([]string, 0, len(keys))
    for _, key := range keys {
        escaped := strings.ReplaceAll(strings.ReplaceAll(pf[key], `\`, `\\`), `'`, `\'`)
        parts = append(parts, key+"='"+escaped+"'")
    }
    return privacyDatabaseTargets{
        mysql:    privacyDatabaseTarget{driver: "mysql", name: mc.DBName, dsn: mc.FormatDSN()},
        postgres: privacyDatabaseTarget{driver: "postgres", name: pf["database"], dsn: strings.Join(parts, " ")},
    }, nil
}

// 本地校准只把 synthetic 值交给纯配置函数. 门禁 1 的测试绝不调用 suite 或 Open.
func TestPrivacyDatabaseGate(t *testing.T) {
    const syntheticMySQL = "synthetic:synthetic@tcp(127.0.0.1:1)/octopus_privacy_test_gate"
    const syntheticPostgres = `host=127.0.0.1 port=1 user=synthetic password='synthetic\'value' dbname=octopus_privacy_test_gate sslmode=disable`
    for index, gate := range []string{"", "0", "true", "01", " 1", "1 ", "1\n"} {
        t.Run("Closed_"+strconv.Itoa(index), func(t *testing.T) {
            _, enabled, err := privacyDatabaseLoadConfig(gate, func(string) string {
                t.Fatal("closed gate read a DSN")
                return ""
            })
            if enabled || err != nil {
                t.Fatal("nonexact gate was not skipped before configuration reads")
            }
        })
    }
    cases := []struct {
        name, mysql, postgres string
        valid                 bool
    }{
        {"MissingBoth", "", "", false},
        {"MissingMySQL", "", syntheticPostgres, false},
        {"MissingPostgres", syntheticMySQL, "", false},
        {"BusinessMySQL", "synthetic@tcp(127.0.0.1:1)/octopus", syntheticPostgres, false},
        {"BusinessPostgres", syntheticMySQL, "host=127.0.0.1 user=synthetic dbname=octopus", false},
        {"PrefixOnly", syntheticMySQL, "host=127.0.0.1 user=synthetic dbname=octopus_privacy_test_", false},
        {"MalformedMySQL", "invalid", syntheticPostgres, false},
        {"MalformedPostgres", syntheticMySQL, "dbname='unterminated", false},
        {"NoPostgresDatabase", syntheticMySQL, "host=127.0.0.1 user=synthetic", false},
        {"IllegalDatabaseName", syntheticMySQL, "host=127.0.0.1 user=synthetic dbname='octopus_privacy_test_x;drop'", false},
        {"LongDatabaseName", syntheticMySQL, "host=127.0.0.1 user=synthetic dbname=octopus_privacy_test_" + strings.Repeat("x", 64), false},
        {"URLBusinessDatabase", syntheticMySQL, "postgres://synthetic:synthetic@127.0.0.1:1/octopus", false},
        {"URLOverride", syntheticMySQL, "postgres://synthetic:synthetic@127.0.0.1:1/octopus_privacy_test_gate?dbname=octopus", false},
        {"KeywordOverride", syntheticMySQL, syntheticPostgres + " dbname=octopus", false},
        {"AlternateDatabaseKey", syntheticMySQL, syntheticPostgres + " database=octopus", false},
        {"ServiceFile", syntheticMySQL, syntheticPostgres + " service=synthetic", false},
        {"PassFile", syntheticMySQL, syntheticPostgres + " passfile=/synthetic", false},
        {"ExternalSearchPath", syntheticMySQL, syntheticPostgres + " search_path=business", false},
        {"MultipleStatements", syntheticMySQL + "?multiStatements=true", syntheticPostgres, false},
        {"KeywordValid", syntheticMySQL, syntheticPostgres, true},
        {"URLValid", syntheticMySQL, "postgresql://synthetic:synthetic@127.0.0.1:1/octopus_privacy_test_gate?sslmode=disable", true},
    }
    for _, tc := range cases {
        t.Run(tc.name, func(t *testing.T) {
            t.Setenv(privacyDatabaseGateEnv, "1")
            t.Setenv(privacyDatabaseMySQLEnv, tc.mysql)
            t.Setenv(privacyDatabasePostgresEnv, tc.postgres)
            targets, enabled, err := privacyDatabaseLoadConfig(os.Getenv(privacyDatabaseGateEnv), os.Getenv)
            if !enabled || (err == nil) != tc.valid {
                t.Fatal("synthetic configuration calibration failed")
            }
            if !tc.valid && err != privacyDatabaseConfigError {
                t.Fatal("configuration rejection was not a fixed safe error")
            }
            if tc.valid {
                if targets.mysql.name != "octopus_privacy_test_gate" || targets.postgres.name != "octopus_privacy_test_gate" {
                    t.Fatal("configuration did not preserve dedicated database names")
                }
                reparsed, err := privacyDatabaseParseConfig(targets.mysql.dsn, targets.postgres.dsn)
                if err != nil || reparsed != targets {
                    t.Fatal("canonical worker configuration did not retain the validated targets")
                }
            }
        })
    }
}

func TestPrivacyDatabaseDrivers(t *testing.T) {
    // 必须是第一道判断, 不能在此前读取 DSN, 解析配置或建立连接.
    if os.Getenv(privacyDatabaseGateEnv) != "1" {
        t.Skip("privacy database tests require explicit authorization and an exact gate of 1")
    }
    targets, _, err := privacyDatabaseLoadConfig("1", os.Getenv)
    if err != nil {
        t.Fatal("privacy database test configuration rejected before connecting either driver")
    }
    if !*octopusPrivacyWorker {
        for _, driver := range []string{"mysql", "postgres"} {
            if !t.Run(driver, func(t *testing.T) { privacyDatabaseRunWorker(t, targets, driver) }) {
                return
            }
        }
        return
    }
    var target privacyDatabaseTarget
    switch *privacyTestPhase {
    case "driver_mysql":
        target = targets.mysql
    case "driver_postgres":
        target = targets.postgres
    default:
        t.Fatal("privacy database worker phase rejected")
    }
    privacyDatabaseInit(t, target)
    scenarios := []struct {
        name string
        run  func(*testing.T)
    }{
        {"SchemaAndDefaults", privacyDatabaseSchema},
        {"FormalCRUDAndFalse", privacyDatabaseCRUD},
        {"BinaryComparisonsAndLengths", privacyDatabaseBytes},
        {"IndependentUniqueConstraints", privacyDatabaseUnique},
        {"ConcurrentDuplicates", privacyDatabaseConcurrent},
        {"ImportRollback", privacyDatabaseRollback},
        {"RetainedImportIDsAndNextCreate", privacyDatabaseImportIDs},
    }
    for _, scenario := range scenarios {
        if !t.Run(scenario.name, scenario.run) {
            return
        }
    }
    privacyTestWorkerOK(t, "TestPrivacyDatabaseDrivers")
}

// 重用已编译二进制和同一 worker flag. 环境白名单隔离 PG 隐式配置及真实上游门禁.
// DSN 不出现在参数中, 不继承其他凭证; stdout/stderr 和退出状态完整返回父测试.
func privacyDatabaseRunWorker(t *testing.T, targets privacyDatabaseTargets, driver string) {
    t.Helper()
    executable, err := os.Executable()
    if err != nil {
        t.Fatal("privacy database worker executable unavailable")
    }
    ctx := t.Context()
    timeout := flag.Lookup("test.timeout").Value.String()
    if deadline, ok := t.Deadline(); ok {
        var cancel context.CancelFunc
        ctx, cancel = context.WithDeadline(ctx, deadline)
        defer cancel()
        timeout = time.Until(deadline).String()
    }
    phase := "driver_" + driver
    cmd := exec.CommandContext(ctx, executable,
        "-test.run=^TestPrivacyDatabaseDrivers$", "-test.v="+strconv.FormatBool(testing.Verbose()),
        "-test.timeout="+timeout, "-test.count=1", "-test.parallel=1",
        "-octopus.privacy.worker=true", "-octopus.privacy.phase="+phase,
    )
    cmd.Env = []string{
        "GOTOOLCHAIN=local", "GOPROXY=off", "GOSUMDB=off", "HOME=" + t.TempDir(),
        "OCTOPUS_PRIVACY_REAL_TEST=0", privacyDatabaseGateEnv + "=1",
        privacyDatabaseMySQLEnv + "=" + targets.mysql.dsn,
        privacyDatabasePostgresEnv + "=" + targets.postgres.dsn,
    }
    var stdout, stderr bytes.Buffer
    cmd.Stdout, cmd.Stderr = &stdout, &stderr
    err = cmd.Run()
    t.Logf("privacy database worker %s stdout:\n%s\nstderr:\n%s", driver, stdout.String(), stderr.String())
    if err != nil {
        exitCode := -1
        if cmd.ProcessState != nil {
            exitCode = cmd.ProcessState.ExitCode()
        }
        t.Fatalf("privacy database worker did not exit successfully (exit code %d)", exitCode)
    }
    marker := "OCTOPUS_PRIVACY_WORKER_OK:TestPrivacyDatabaseDrivers:" + phase + "\n"
    if !strings.Contains(stdout.String(), marker) || !regexp.MustCompile(`(?m)^PASS\r?$`).Match(stdout.Bytes()) {
        t.Fatal("privacy database worker did not complete assertions and report PASS")
    }
}

// 固定正式表清单, 按外键依赖逆序清理. 不使用 DROP DATABASE, CASCADE 或全库对象遍历删除.
var privacyDatabaseOwnedTables = []string{
    "privacy_rules", "relay_logs", "stats_api_keys", "stats_models", "stats_hourlies", "stats_dailies", "stats_totals",
    "group_items", "groups", "channel_keys", "stats_channels", "channels", "api_keys", "llm_infos", "settings", "users", "migration_records",
}

func privacyDatabaseInit(t *testing.T, target privacyDatabaseTarget) {
    t.Helper()
    var dialector gorm.Dialector
    if target.driver == "mysql" {
        // 仅隔离 worker 的驱动日志静默, 避免网络错误绕过 GORM logger 输出原始错误.
        if mysqlDriver.SetLogger(&mysqlDriver.NopLogger{}) != nil {
            t.Fatal("privacy database driver logger initialization failed")
        }
        dialector = mysql.Open(target.dsn)
    } else {
        dialector = postgres.Open(target.dsn)
    }
    // 直接 GORM 静默 Open 只用于元数据预检, 正式 InitDB 尚未运行.
    conn, err := gorm.Open(dialector, &gorm.Config{Logger: logger.Discard})
    if err != nil {
        t.Fatal("privacy database preflight connection failed")
    }
    pool, err := conn.DB()
    if err != nil {
        t.Fatal("privacy database preflight pool unavailable")
    }
    t.Cleanup(func() {
        if pool.Close() != nil {
            t.Error("privacy database preflight close failed")
        }
    })
    conn = conn.WithContext(t.Context())
    var name string
    nameSQL := "SELECT DATABASE()"
    if target.driver == "postgres" {
        nameSQL = "SELECT current_database()"
    }
    if conn.Raw(nameSQL).Scan(&name).Error != nil || name != target.name || !privacyDatabaseValidName(name) {
        t.Fatal("privacy database connected database identity rejected")
    }
    var count int64
    var checks []string
    if target.driver == "mysql" {
        checks = []string{
            "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE()",
            "SELECT COUNT(*) FROM information_schema.routines WHERE routine_schema = DATABASE()",
            "SELECT COUNT(*) FROM information_schema.triggers WHERE trigger_schema = DATABASE()",
            "SELECT COUNT(*) FROM information_schema.events WHERE event_schema = DATABASE()",
        }
    } else {
        checks = []string{
            "SELECT COUNT(*) FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace WHERE n.nspname !~ '^pg_' AND n.nspname <> 'information_schema'",
            "SELECT COUNT(*) FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace WHERE n.nspname !~ '^pg_' AND n.nspname <> 'information_schema'",
            "SELECT COUNT(*) FROM pg_namespace WHERE nspname !~ '^pg_' AND nspname NOT IN ('public', 'information_schema')",
            "SELECT COUNT(*) FROM pg_type t JOIN pg_namespace n ON n.oid = t.typnamespace WHERE n.nspname !~ '^pg_' AND n.nspname <> 'information_schema'",
            "SELECT COUNT(*) FROM pg_largeobject_metadata",
            "SELECT COUNT(*) FROM pg_foreign_server",
        }
    }
    for _, query := range checks {
        count = 0
        if conn.Raw(query).Scan(&count).Error != nil {
            t.Fatal("privacy database empty-database metadata check failed")
        }
        if count != 0 {
            t.Fatal("privacy database is not empty; refusing initialization without clearing existing data")
        }
    }
    // 只有专用库身份和空库检查都成功, 才登记清理. 初始化部分失败也仅清理固定正式表.
    // 清理使用独立预检连接和独立超时, 不因测试 context 取消而删除其他对象或跳过关闭.
    t.Cleanup(func() {
        ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
        defer cancel()
        cleanup := conn.Session(&gorm.Session{Context: ctx, Logger: logger.Discard})
        var tables []string
        query := "SELECT table_name FROM information_schema.tables WHERE table_schema = DATABASE() AND table_type = 'BASE TABLE'"
        if target.driver == "postgres" {
            query = "SELECT tablename FROM pg_tables WHERE schemaname = 'public'"
        }
        if cleanup.Raw(query).Scan(&tables).Error != nil {
            t.Error("privacy database owned-table cleanup metadata failed")
            return
        }
        present := make(map[string]bool, len(tables))
        for _, table := range tables {
            present[table] = true
        }
        for _, table := range privacyDatabaseOwnedTables {
            if !present[table] {
                continue
            }
            drop := "DROP TABLE IF EXISTS `" + table + "`"
            if target.driver == "postgres" {
                drop = `DROP TABLE IF EXISTS "public"."` + table + `"`
            }
            // PostgreSQL 的 GORM DropTable 会添加 CASCADE, 所以只执行固定标识符的显式 DDL.
            if cleanup.Exec(drop).Error != nil {
                t.Error("privacy database owned-table cleanup failed")
            }
        }
    })
    err = db.InitDB(target.driver, target.dsn, false)
    t.Cleanup(func() {
        if db.GetDB() != nil && db.Close() != nil {
            t.Error("privacy database formal connection close failed")
        }
    })
    if err != nil {
        t.Fatal("privacy database formal initialization failed")
    }
    if InitCache() != nil || PrivacySnapshot() == nil {
        t.Fatal("privacy database formal cache initialization failed")
    }
}

func privacyDatabaseConn(t *testing.T) *gorm.DB {
    t.Helper()
    return db.GetDB().Session(&gorm.Session{Logger: logger.Discard, Context: t.Context()})
}

func privacyDatabaseCreate(t *testing.T, source, alias string, enabled *bool) model.PrivacyRule {
    t.Helper()
    row, err := PrivacyRuleCreate(t.Context(), privacyTestCreateInput(source, alias, enabled))
    if err != nil || row.ID <= 0 || row.Source.String() != source || row.Alias.String() != alias || row.Enabled != (enabled == nil || *enabled) {
        t.Fatal("privacy database formal create failed or returned incorrect fields")
    }
    var stored model.PrivacyRule
    if privacyDatabaseConn(t).First(&stored, "id = ?", row.ID).Error != nil || stored != row {
        t.Fatal("privacy database formal create was not persisted exactly")
    }
    return row
}

// 安全版状态捕获, 任何查询失败都不输出底层错误. 不复用会打印 SQLite 错误的 helper.
func privacyDatabaseState(t *testing.T) privacyTestState {
    t.Helper()
    snapshot := PrivacySnapshot()
    if snapshot == nil {
        t.Fatal("privacy database snapshot missing")
    }
    state := privacyTestState{
        snapshot: snapshot, config: snapshot.Config(), version: snapshot.Version(), snapshotRules: privacyTestSnapshotRules(snapshot),
        settings: make(map[model.SettingKey]string), cached: make(map[model.SettingKey]string),
    }
    var rows []model.PrivacyRule
    if privacyDatabaseConn(t).Order("id ASC").Find(&rows).Error != nil {
        t.Fatal("privacy database persisted rules read failed")
    }
    state.rows = privacyTestNeutralRules(rows)
    for _, key := range privacyTestSettingKeys {
        var setting model.Setting
        cached, err := SettingGetString(key)
        if err != nil || privacyDatabaseConn(t).First(&setting, "key = ?", key).Error != nil {
            t.Fatal("privacy database persisted or cached setting read failed")
        }
        state.settings[key], state.cached[key] = setting.Value, cached
    }
    return state
}

func privacyDatabasePreserved(t *testing.T, before privacyTestState) {
    t.Helper()
    after := privacyDatabaseState(t)
    if before.snapshot != after.snapshot || !reflect.DeepEqual(before, after) {
        t.Fatal("privacy database rejected operation changed persisted or published privacy state")
    }
}

func privacyDatabaseError(t *testing.T, before privacyTestState, got, want error) {
    t.Helper()
    if !errors.Is(got, want) {
        t.Fatal("privacy database operation returned an incorrect safe error category")
    }
    privacyDatabasePreserved(t, before)
}

func privacyDatabaseSchema(t *testing.T) {
    conn := privacyDatabaseConn(t)
    if migrate.ValidatePrivacySchema(conn) != nil {
        t.Fatal("privacy database binary columns or independent full unique indexes rejected")
    }
    var record migrate.MigrationRecord
    if conn.First(&record, "version = ?", 6).Error != nil || record.Status != migrate.MigrationRecordStatusSuccess {
        t.Fatal("privacy database migration 006 success record missing")
    }
    rows, err := PrivacyRuleList(t.Context())
    if err != nil || rows == nil || len(rows) != 0 || PrivacySnapshot().Config() != privacy.DefaultConfig() || PrivacySnapshot().Active() {
        t.Fatal("privacy database empty formal defaults incorrect")
    }
    state := privacyDatabaseState(t)
    for key, value := range map[model.SettingKey]string{
        model.SettingKeyPrivacyEnabled: "false", model.SettingKeyPrivacyScopes: "1", model.SettingKeyPrivacyFailurePolicy: "continue",
    } {
        if state.settings[key] != value || state.cached[key] != value {
            t.Fatal("privacy database default settings differ between database and cache")
        }
    }
    // 不指定 enabled 的真实 SQL 插入必须采用 true 默认值, 事务最后实际回滚.
    rollback := errors.New("privacy database default fixture rollback")
    err = conn.Transaction(func(tx *gorm.DB) error {
        if tx.Exec("INSERT INTO privacy_rules(source, alias) VALUES (?, ?)", model.NewPrivacyBytes("sql default fixture"), model.NewPrivacyBytes("SQL_DEFAULT")).Error != nil {
            t.Fatal("privacy database default fixture insert failed")
        }
        var row model.PrivacyRule
        if tx.First(&row, "source = ?", model.NewPrivacyBytes("sql default fixture")).Error != nil || !row.Enabled {
            t.Fatal("privacy database SQL enabled default is not true")
        }
        return rollback
    })
    if err != rollback {
        t.Fatal("privacy database default fixture transaction failed")
    }
    privacyDatabasePreserved(t, state)
}

func privacyDatabaseCRUD(t *testing.T) {
    if PrivacySetSetting(t.Context(), model.SettingKeyPrivacyEnabled, "true") != nil {
        t.Fatal("privacy database formal switch write failed")
    }
    first := privacyDatabaseCreate(t, "13969909666", "DRIVER_PHONE", nil)
    disabled := privacyDatabaseCreate(t, "driver disabled synthetic source", "DRIVER_DISABLED", privacyTestBool(false))
    before := privacyDatabaseState(t)
    _, err := PrivacyRuleCreate(t.Context(), privacyTestCreateInput(disabled.Source.String(), "DRIVER_OTHER", nil))
    privacyDatabaseError(t, before, err, privacy.ErrRuleConflict)
    _, err = PrivacyRuleCreate(t.Context(), privacyTestCreateInput("driver other source", disabled.Alias.String(), nil))
    privacyDatabaseError(t, before, err, privacy.ErrRuleConflict)
    old := PrivacySnapshot()
    input := privacyTestUpdateInput(first.ID, "张三丰", "DRIVER_PERSON", privacyTestBool(false))
    updated, err := PrivacyRuleUpdate(t.Context(), input)
    var stored model.PrivacyRule
    if err != nil || updated.ID != first.ID || updated.Enabled || updated.Source.String() != "张三丰" || updated.Alias.String() != "DRIVER_PERSON" ||
        privacyDatabaseConn(t).First(&stored, "id = ?", first.ID).Error != nil || stored != updated {
        t.Fatal("privacy database full update lost ID, bytes or explicit false")
    }
    if PrivacySnapshot() == old || PrivacySnapshot().Version() <= old.Version() {
        t.Fatal("privacy database update did not publish a newer snapshot")
    }
    privacyTestAssertText(t, old, "13969909666", "[[OCTOPUS_PII:DRIVER_PHONE]]", true)
    privacyTestAssertText(t, PrivacySnapshot(), "张三丰", "张三丰", false)
    // MySQL 同值更新可能 RowsAffected 为 0, 正式更新仍必须成功.
    if _, err := PrivacyRuleUpdate(t.Context(), input); err != nil {
        t.Fatal("privacy database unchanged full update incorrectly reported not found")
    }
    before = privacyDatabaseState(t)
    _, err = PrivacyRuleUpdate(t.Context(), privacyTestUpdateInput(first.ID, disabled.Source.String(), "DRIVER_UPDATE_OTHER", privacyTestBool(true)))
    privacyDatabaseError(t, before, err, privacy.ErrRuleConflict)
    if PrivacyRuleDelete(t.Context(), first.ID) != nil {
        t.Fatal("privacy database formal hard delete failed")
    }
    before = privacyDatabaseState(t)
    privacyDatabaseError(t, before, PrivacyRuleDelete(t.Context(), first.ID), privacy.ErrRuleNotFound)
    _, err = PrivacyRuleUpdate(t.Context(), input)
    privacyDatabaseError(t, before, err, privacy.ErrRuleNotFound)
    privacyDatabaseCreate(t, "张三丰", "DRIVER_PERSON", nil)
    privacyDatabaseCreate(t, "13969909666", "DRIVER_PHONE", nil)
    state := privacyDatabaseState(t)
    listed, err := PrivacyRuleList(t.Context())
    if err != nil || !reflect.DeepEqual(privacyTestNeutralRules(listed), state.rows) || !reflect.DeepEqual(state.rows, state.snapshotRules) {
        t.Fatal("privacy database formal list, database and snapshot disagree after CRUD")
    }
}

func privacyDatabaseBytes(t *testing.T) {
    fixtures := []string{"a", "A", "ab", "bc", "张三", "é", "e\u0301", "尾随", "尾随 ", "line\nquote\"slash\\", "nul\x00value", strings.Repeat("界", 170) + "ab"}
    for index, source := range fixtures {
        alias := "DRIVER_BYTE_" + strconv.Itoa(index)
        row := privacyDatabaseCreate(t, source, alias, nil)
        var found []model.PrivacyRule
        conn := privacyDatabaseConn(t)
        if conn.Where("source = ? AND alias = ?", model.NewPrivacyBytes(source), model.NewPrivacyBytes(alias)).Find(&found).Error != nil || len(found) != 1 || found[0] != row {
            t.Fatal("privacy database binary equality collapsed distinct source bytes")
        }
        var lengths struct{ SourceLength, AliasLength int }
        if conn.Raw("SELECT OCTET_LENGTH(source) AS source_length, OCTET_LENGTH(alias) AS alias_length FROM privacy_rules WHERE id = ?", row.ID).Scan(&lengths).Error != nil ||
            lengths.SourceLength != len(source) || lengths.AliasLength != len(alias) {
            t.Fatal("privacy database binary lengths do not match UTF-8 byte lengths")
        }
    }
    privacyDatabaseCreate(t, "minimum alias source", "Z", nil)
    privacyDatabaseCreate(t, "maximum alias source", strings.Repeat("Z", 64), nil)
    for _, input := range []model.PrivacyRuleCreate{
        privacyTestCreateInput("", "DRIVER_EMPTY", nil),
        privacyTestCreateInput(strings.Repeat("x", 513), "DRIVER_LONG", nil),
        privacyTestCreateInput(string([]byte{0xff}), "DRIVER_UTF8", nil),
        privacyTestCreateInput("driver empty alias source", "", nil),
        privacyTestCreateInput("driver long alias source", strings.Repeat("Z", 65), nil),
        privacyTestCreateInput("driver lowercase alias source", "lowercase", nil),
        privacyTestCreateInput("driver nul alias source", "BAD\x00ALIAS", nil),
    } {
        before := privacyDatabaseState(t)
        _, err := PrivacyRuleCreate(t.Context(), input)
        privacyDatabaseError(t, before, err, privacy.ErrInvalidRule)
    }
    // 非法业务 alias 仅作 SQL 二进制比较对照, 不发布缓存, 最后真实回滚.
    // 两列都必须区分大小写, 重音, 组合 Unicode 和尾空格, 而非文本 collation.
    before := privacyDatabaseState(t)
    rollback := errors.New("privacy database alias comparison rollback")
    err := privacyDatabaseConn(t).Transaction(func(tx *gorm.DB) error {
        for index, alias := range []string{"BINARY_A", "binary_a", "BINARY_TAIL", "BINARY_TAIL ", "É", "E\u0301"} {
            if tx.Exec("INSERT INTO privacy_rules(source, alias, enabled) VALUES (?, ?, ?)", model.NewPrivacyBytes("alias comparison "+strconv.Itoa(index)), model.NewPrivacyBytes(alias), false).Error != nil {
                t.Fatal("privacy database binary alias distinction insert failed")
            }
            var count int64
            if tx.Model(&model.PrivacyRule{}).Where("alias = ?", model.NewPrivacyBytes(alias)).Count(&count).Error != nil || count != 1 {
                t.Fatal("privacy database binary alias equality collapsed distinct bytes")
            }
        }
        return rollback
    })
    if err != rollback {
        t.Fatal("privacy database alias comparison transaction failed")
    }
    privacyDatabasePreserved(t, before)
    if privacyDatabaseConn(t).Dialector.Name() == "mysql" {
        // VARBINARY 的真实存储上限不能依靠 Go 校验或静默截断代替.
        for _, pair := range [][2]string{
            {strings.Repeat("x", 513), "DRIVER_SQL_LONG_SOURCE"},
            {"driver SQL long alias source", strings.Repeat("Q", 65)},
        } {
            before := privacyDatabaseState(t)
            err := privacyDatabaseConn(t).Transaction(func(tx *gorm.DB) error {
                err := tx.Exec("INSERT INTO privacy_rules(source, alias, enabled) VALUES (?, ?, ?)", model.NewPrivacyBytes(pair[0]), model.NewPrivacyBytes(pair[1]), false).Error
                var coded *mysqlDriver.MySQLError
                if !errors.As(err, &coded) || coded.Number != 1406 {
                    t.Fatal("privacy database MySQL binary length limit did not reject oversized bytes")
                }
                return rollback
            })
            if err != rollback {
                t.Fatal("privacy database MySQL binary length fixture transaction failed")
            }
            privacyDatabasePreserved(t, before)
        }
    }
}

func privacyDatabaseUniqueCode(t *testing.T, err error) {
    t.Helper()
    var mysqlError *mysqlDriver.MySQLError
    var postgresError *pgconn.PgError
    switch privacyDatabaseConn(t).Dialector.Name() {
    case "mysql":
        if !errors.As(err, &mysqlError) || mysqlError.Number != 1062 {
            t.Fatal("privacy database MySQL independent unique constraint did not reject duplicate")
        }
    case "postgres":
        if !errors.As(err, &postgresError) || postgresError.Code != "23505" {
            t.Fatal("privacy database PostgreSQL independent unique constraint did not reject duplicate")
        }
    default:
        t.Fatal("privacy database unique assertion driver rejected")
    }
}

func privacyDatabaseUnique(t *testing.T) {
    base := privacyDatabaseCreate(t, "driver SQL unique source", "DRIVER_SQL_UNIQUE", privacyTestBool(false))
    for _, tc := range []struct {
        id            int
        source, alias string
    }{
        {0, base.Source.String(), "DRIVER_SQL_OTHER"},
        {0, "driver SQL other source", base.Alias.String()},
        {base.ID, "driver SQL duplicate ID source", "DRIVER_SQL_DUPLICATE_ID"},
    } {
        before := privacyDatabaseState(t)
        query := "INSERT INTO privacy_rules(source, alias, enabled) VALUES (?, ?, ?)"
        args := []any{model.NewPrivacyBytes(tc.source), model.NewPrivacyBytes(tc.alias), false}
        if tc.id != 0 {
            query = "INSERT INTO privacy_rules(id, source, alias, enabled) VALUES (?, ?, ?, ?)"
            args = append([]any{tc.id}, args...)
        }
        privacyDatabaseUniqueCode(t, privacyDatabaseConn(t).Exec(query, args...).Error)
        privacyDatabasePreserved(t, before)
    }
    // 带外插入不在候选快照中, 正式 CRUD 必须由实际驱动错误码转换为固定冲突类别.
    source, alias := "driver external synthetic source", "DRIVER_EXTERNAL"
    if privacyDatabaseConn(t).Exec("INSERT INTO privacy_rules(source, alias, enabled) VALUES (?, ?, ?)", model.NewPrivacyBytes(source), model.NewPrivacyBytes(alias), false).Error != nil {
        t.Fatal("privacy database external constraint fixture insert failed")
    }
    defer func() {
        if privacyDatabaseConn(t).Where("source = ?", model.NewPrivacyBytes(source)).Delete(&model.PrivacyRule{}).Error != nil {
            t.Error("privacy database external constraint fixture cleanup failed")
        }
    }()
    before := privacyDatabaseState(t)
    for _, input := range []model.PrivacyRuleCreate{
        privacyTestCreateInput(source, "DRIVER_EXTERNAL_OTHER", nil),
        privacyTestCreateInput("driver external other source", alias, nil),
    } {
        _, err := PrivacyRuleCreate(t.Context(), input)
        privacyDatabaseError(t, before, err, privacy.ErrRuleConflict)
    }
    for _, input := range []model.PrivacyRuleUpdate{
        privacyTestUpdateInput(base.ID, source, "DRIVER_EXTERNAL_UPDATE", privacyTestBool(true)),
        privacyTestUpdateInput(base.ID, "driver external update source", alias, privacyTestBool(true)),
    } {
        _, err := PrivacyRuleUpdate(t.Context(), input)
        privacyDatabaseError(t, before, err, privacy.ErrRuleConflict)
    }
}

func privacyDatabaseConcurrent(t *testing.T) {
    const count = 16
    for _, kind := range []string{"Source", "Alias", "Both"} {
        if !t.Run(kind, func(t *testing.T) {
            start := make(chan struct{})
            results := make(chan error, count)
            var wg sync.WaitGroup
            for index := 0; index < count; index++ {
                wg.Add(1)
                go func(index int) {
                    defer wg.Done()
                    source, alias := "driver concurrent "+kind, "DRIVER_CONCURRENT_"+strings.ToUpper(kind)
                    if kind == "Source" {
                        alias += "_" + strconv.Itoa(index)
                    }
                    if kind == "Alias" {
                        source += " " + strconv.Itoa(index)
                    }
                    <-start
                    _, err := PrivacyRuleCreate(t.Context(), privacyTestCreateInput(source, alias, nil))
                    results <- err
                }(index)
            }
            close(start)
            wg.Wait()
            close(results)
            successes := 0
            for err := range results {
                if err == nil {
                    successes++
                } else if !errors.Is(err, privacy.ErrRuleConflict) {
                    t.Fatal("privacy database concurrent duplicate returned an unexpected error category")
                }
            }
            if successes != 1 {
                t.Fatal("privacy database concurrent duplicate did not have exactly one successful create")
            }
            state := privacyDatabaseState(t)
            if !reflect.DeepEqual(state.rows, state.snapshotRules) {
                t.Fatal("privacy database concurrent snapshot and persisted rules disagree")
            }
            found := 0
            for _, rule := range state.rows {
                if strings.HasPrefix(rule.Alias, "DRIVER_CONCURRENT_"+strings.ToUpper(kind)) {
                    found++
                }
            }
            if found != 1 {
                t.Fatal("privacy database concurrent duplicate persisted an incorrect row count")
            }
        }) {
            return
        }
    }
}

func privacyDatabaseBusinessAbsent(t *testing.T) {
    t.Helper()
    for _, table := range []string{"channels", "channel_keys", "groups", "group_items", "llm_infos", "api_keys", "stats_totals", "stats_dailies", "stats_hourlies", "stats_models", "stats_channels", "stats_api_keys", "relay_logs"} {
        var count int64
        if privacyDatabaseConn(t).Table(table).Count(&count).Error != nil || count != 0 {
            t.Fatal("privacy database failed import left business rows")
        }
    }
    var setting model.Setting
    value, err := SettingGetString(model.SettingKeyRelayLogKeepPeriod)
    if err != nil || value != "7" || privacyDatabaseConn(t).First(&setting, "key = ?", model.SettingKeyRelayLogKeepPeriod).Error != nil || setting.Value != "7" {
        t.Fatal("privacy database failed import changed an ordinary persisted or cached setting")
    }
}

func privacyDatabaseRollback(t *testing.T) {
    conn := privacyDatabaseConn(t)
    // 最后的真实日志写入才被数据库拒绝, 不是 mock 事务或注入 Commit 错误.
    create := "CREATE TRIGGER privacy_driver_reject_log BEFORE INSERT ON relay_logs FOR EACH ROW SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'privacy_driver_write_rejected'"
    drop := "DROP TRIGGER privacy_driver_reject_log"
    if conn.Dialector.Name() == "postgres" {
        create = "ALTER TABLE relay_logs ADD CONSTRAINT privacy_driver_reject_log CHECK (id <> 5000)"
        drop = "ALTER TABLE relay_logs DROP CONSTRAINT privacy_driver_reject_log"
    }
    if conn.Exec(create).Error != nil {
        t.Fatal("privacy database real late-write rejection fixture creation failed")
    }
    defer func() {
        if privacyDatabaseConn(t).Exec(drop).Error != nil {
            t.Error("privacy database late-write rejection fixture cleanup failed")
        }
    }()
    // 只观察真实 GORM 写入结果, 不替换 SQL, 连接或事务.
    const observer = "privacy_database_observe_import_writes"
    writes := make(map[string]int)
    lateRejected := false
    callbacks := db.GetDB().Callback().Create()
    if callbacks.After("gorm:create").Register(observer, func(tx *gorm.DB) {
        if tx.Statement.Schema == nil {
            return
        }
        table := tx.Statement.Schema.Table
        if tx.Error == nil {
            writes[table]++
        } else if table == "relay_logs" {
            var mc *mysqlDriver.MySQLError
            var pc *pgconn.PgError
            lateRejected = (errors.As(tx.Error, &mc) && mc.Number == 1644) ||
                (errors.As(tx.Error, &pc) && pc.Code == "23514" && pc.ConstraintName == "privacy_driver_reject_log")
        }
    }) != nil {
        t.Fatal("privacy database real-write observer registration failed")
    }
    defer func() {
        if callbacks.Remove(observer) != nil {
            t.Error("privacy database real-write observer cleanup failed")
        }
    }()
    before := privacyDatabaseState(t)
    result, err := DBImportIncremental(t.Context(), privacyImportTestBusinessDump())
    if result != nil || err != ErrBackupStorage || !lateRejected {
        t.Fatal("privacy database real late-write failure did not return a fixed rollback error")
    }
    for _, table := range []string{"privacy_rules", "channels", "channel_keys", "groups", "group_items", "llm_infos", "api_keys", "settings", "stats_totals", "stats_dailies", "stats_hourlies", "stats_models", "stats_channels", "stats_api_keys"} {
        if writes[table] == 0 {
            t.Fatal("privacy database rollback did not follow real writes to every preceding import table")
        }
    }
    privacyDatabasePreserved(t, before)
    privacyDatabaseBusinessAbsent(t)
    // 快照未知的带外行强制由数据库拒绝导入的 source, alias 和主键冲突.
    external := privacyImportTestRow(6000, "driver import external source", "DRIVER_IMPORT_EXTERNAL", false)
    if conn.Exec("INSERT INTO privacy_rules(id, source, alias, enabled) VALUES (?, ?, ?, ?)", external.ID, external.Source, external.Alias, false).Error != nil {
        t.Fatal("privacy database import constraint fixture insert failed")
    }
    defer func() {
        if privacyDatabaseConn(t).Delete(&model.PrivacyRule{}, external.ID).Error != nil {
            t.Error("privacy database import constraint fixture cleanup failed")
        }
    }()
    for _, row := range []model.PrivacyRule{
        privacyImportTestRow(6001, external.Source.String(), "DRIVER_IMPORT_OTHER", true),
        privacyImportTestRow(6001, "driver import other source", external.Alias.String(), true),
        privacyImportTestRow(external.ID, "driver import other source", "DRIVER_IMPORT_OTHER", true),
    } {
        before := privacyDatabaseState(t)
        dump := privacyImportTestBusinessDump()
        dump.PrivacyRules = append(dump.PrivacyRules, row)
        result, err := DBImportIncremental(t.Context(), dump)
        if result != nil {
            t.Fatal("privacy database rejected import returned a success result")
        }
        privacyDatabaseError(t, before, err, privacy.ErrRuleConflict)
        privacyDatabaseBusinessAbsent(t)
    }
}

func privacyDatabaseImportIDs(t *testing.T) {
    retained := privacyDatabaseCreate(t, "driver import retained source", "DRIVER_IMPORT_RETAINED", privacyTestBool(false))
    for _, tc := range []struct {
        rows     []model.PrivacyRule
        settings []model.Setting
        want     error
    }{
        {[]model.PrivacyRule{privacyImportTestRow(70000, retained.Source.String(), "DRIVER_IMPORT_CONFLICT", true)}, nil, privacy.ErrRuleConflict},
        {[]model.PrivacyRule{privacyImportTestRow(70000, "driver import conflict source", retained.Alias.String(), true)}, nil, privacy.ErrRuleConflict},
        {[]model.PrivacyRule{privacyImportTestRow(retained.ID, "driver import conflict source", "DRIVER_IMPORT_CONFLICT", true)}, nil, privacy.ErrRuleConflict},
        {[]model.PrivacyRule{privacyImportTestRow(70000, string([]byte{0xff}), "DRIVER_IMPORT_INVALID", false)}, nil, privacy.ErrInvalidRule},
        {[]model.PrivacyRule{privacyImportTestRow(70000, "driver import invalid alias", "BAD\x00ALIAS", false)}, nil, privacy.ErrInvalidRule},
        {nil, []model.Setting{{Key: model.SettingKeyPrivacyScopes, Value: "16"}}, privacy.ErrInvalidConfig},
    } {
        before := privacyDatabaseState(t)
        dump := privacyImportTestBusinessDump()
        dump.PrivacyRules, dump.Settings = tc.rows, tc.settings
        result, err := DBImportIncremental(t.Context(), dump)
        if result != nil {
            t.Fatal("privacy database invalid import returned a successful result")
        }
        privacyDatabaseError(t, before, err, tc.want)
        privacyDatabaseBusinessAbsent(t)
    }
    before := privacyDatabaseState(t)
    maxID := 0
    for _, row := range before.rows {
        if row.ID > maxID {
            maxID = row.ID
        }
    }
    importID := maxID + 70000
    dump := &model.DBDump{Version: 1, Settings: privacyImportTestSettings(), PrivacyRules: []model.PrivacyRule{
        privacyImportTestRow(importID, retained.Source.String(), retained.Alias.String(), true),
        privacyImportTestRow(importID+1, "driver import retained ID source\x00 ", "DRIVER_IMPORT_ID", false),
        privacyImportTestRow(importID+2, "driver import next source", "DRIVER_IMPORT_NEXT", true),
    }}
    result, err := DBImportIncremental(t.Context(), dump)
    if err != nil || result == nil || result.RowsAffected["privacy_rules"] != 2 {
        t.Fatal("privacy database explicit-ID import failed")
    }
    // 成功保留 ID 导入后立即调用正式 Create, 必须严格高于库内最大 ID, 不修复序列再测.
    created, err := PrivacyRuleCreate(t.Context(), privacyTestCreateInput("driver post-import auto ID source", "DRIVER_POST_IMPORT_ID", nil))
    if err != nil || created.ID <= importID+2 {
        t.Fatal("privacy database automatic ID after retained-ID import did not exceed maximum imported ID")
    }
    state := privacyDatabaseState(t)
    if state.version != before.version+2 || !reflect.DeepEqual(state.rows, state.snapshotRules) || !reflect.DeepEqual(state.settings, state.cached) ||
        state.config != (privacy.Config{Enabled: true, Scopes: privacy.AllScopes, FailurePolicy: privacy.Abort}) {
        t.Fatal("privacy database successful import and create did not publish consistent versions and configuration")
    }
    var rows []model.PrivacyRule
    if privacyDatabaseConn(t).Where("id IN ?", []int{retained.ID, importID + 1, importID + 2, created.ID}).Order("id ASC").Find(&rows).Error != nil || len(rows) != 4 ||
        rows[0] != retained || rows[1] != dump.PrivacyRules[1] || rows[2] != dump.PrivacyRules[2] || rows[3] != created {
        t.Fatal("privacy database import did not preserve target identity, explicit false, bytes or new automatic ID")
    }
    result, err = DBImportIncremental(t.Context(), dump)
    if err != nil || result == nil || result.RowsAffected["privacy_rules"] != 0 {
        t.Fatal("privacy database same-mapping import was not idempotent")
    }
    after := privacyDatabaseState(t)
    if !reflect.DeepEqual(state.rows, after.rows) || after.version != state.version+1 {
        t.Fatal("privacy database idempotent import changed IDs or enabled states")
    }
}
