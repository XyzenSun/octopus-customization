package migrate

import (
    "database/sql"
    "errors"
    "regexp"
    "strings"

    "gorm.io/gorm"
    "gorm.io/gorm/logger"
)

// ErrPrivacySchema 是隐私表建表、元数据读取及结构不兼容的固定错误，不携带数据库原始错误。
var ErrPrivacySchema = errors.New("privacy schema validation failed")

const privacySQLiteDDL = `CREATE TABLE privacy_rules (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    source BLOB NOT NULL,
    alias BLOB NOT NULL,
    enabled NUMERIC NOT NULL DEFAULT true,
    CONSTRAINT ck_privacy_rules_source_blob CHECK (typeof(source) = 'blob'),
    CONSTRAINT ck_privacy_rules_source_length CHECK (length(source) BETWEEN 1 AND 512),
    CONSTRAINT ck_privacy_rules_alias_blob CHECK (typeof(alias) = 'blob'),
    CONSTRAINT ck_privacy_rules_alias_length CHECK (length(alias) BETWEEN 1 AND 64),
    CONSTRAINT ck_privacy_rules_alias_ascii CHECK (
        instr(alias, X'00') = 0 AND CAST(alias AS TEXT) NOT GLOB '*[^A-Z0-9_]*'
    ),
    CONSTRAINT ck_privacy_rules_enabled CHECK (
        typeof(enabled) = 'integer' AND enabled IN (0, 1)
    )
)`

func init() {
    RegisterBeforeAutoMigration(Migration{
        Version: 6,
        Up:      createPrivacyRules,
    })
}

// PrivacySchemaExists 在任何隐私表 DDL 之前检查存在性；查询失败不能误判成新库。
// 同名视图也算已存在，随后交给结构校验拒绝，避免自动迁移覆盖未知对象。
func PrivacySchemaExists(conn *gorm.DB) (bool, error) {
    if conn == nil || conn.Dialector == nil {
        return false, ErrPrivacySchema
    }
    conn = conn.Session(&gorm.Session{Logger: logger.Discard})
    var query string
    switch conn.Dialector.Name() {
    case "sqlite":
        query = "SELECT COUNT(*) FROM sqlite_master WHERE name = ? COLLATE NOCASE"
    case "mysql":
        query = "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND BINARY table_name = ?"
    case "postgres":
        query = "SELECT COUNT(*) FROM pg_class WHERE oid = to_regclass(?)"
    default:
        return false, ErrPrivacySchema
    }
    var count int64
    if err := conn.Raw(query, "privacy_rules").Scan(&count).Error; err != nil || count > 1 {
        return false, ErrPrivacySchema
    }
    return count == 1, nil
}

// 006 只为 SQLite 创建已知结构，MySQL/PostgreSQL 的新表由静默 AutoMigrate 创建。
func createPrivacyRules(conn *gorm.DB) error {
    if conn == nil || conn.Dialector == nil {
        return ErrPrivacySchema
    }
    conn = conn.Session(&gorm.Session{Logger: logger.Discard})
    switch conn.Dialector.Name() {
    case "mysql", "postgres":
        return nil
    case "sqlite":
        err := conn.Transaction(func(tx *gorm.DB) error {
            exists, err := PrivacySchemaExists(tx)
            if err != nil {
                return ErrPrivacySchema
            }
            if exists {
                return ValidatePrivacySchema(tx)
            }
            for _, ddl := range []string{
                privacySQLiteDDL,
                "CREATE UNIQUE INDEX ux_privacy_rules_source ON privacy_rules(source)",
                "CREATE UNIQUE INDEX ux_privacy_rules_alias ON privacy_rules(alias)",
            } {
                if err := tx.Exec(ddl).Error; err != nil {
                    return ErrPrivacySchema
                }
            }
            return ValidatePrivacySchema(tx)
        })
        // BEGIN/COMMIT 自身的错误同样不能携带底层 SQL 错误。
        if err != nil {
            return ErrPrivacySchema
        }
        return nil
    default:
        return ErrPrivacySchema
    }
}

// ValidatePrivacySchema 每次启动前后均调用，不依赖迁移版本是否已经记为成功。
// 所有查询强制静默；不查询规则正文，也不自动修复不兼容结构。
func ValidatePrivacySchema(conn *gorm.DB) error {
    if conn == nil || conn.Dialector == nil {
        return ErrPrivacySchema
    }
    conn = conn.Session(&gorm.Session{Logger: logger.Discard})
    switch conn.Dialector.Name() {
    case "sqlite":
        return validatePrivacySQLite(conn)
    case "mysql":
        return validatePrivacyMySQL(conn)
    case "postgres":
        return validatePrivacyPostgres(conn)
    default:
        return ErrPrivacySchema
    }
}

func privacyTrueDefault(value sql.NullString) bool {
    if !value.Valid {
        return false
    }
    text := strings.ToLower(strings.TrimSpace(value.String))
    for strings.HasPrefix(text, "(") && strings.HasSuffix(text, ")") {
        text = strings.TrimSpace(text[1 : len(text)-1])
    }
    return text == "true" || text == "1"
}

func validatePrivacySQLite(conn *gorm.DB) error {
    var tables []struct {
        Type string
        SQL  sql.NullString
    }
    if err := conn.Raw("SELECT type, sql FROM sqlite_master WHERE name = ? COLLATE NOCASE", "privacy_rules").Scan(&tables).Error; err != nil ||
        len(tables) != 1 || tables[0].Type != "table" || !tables[0].SQL.Valid {
        return ErrPrivacySchema
    }
    var columns []struct {
        CID     int `gorm:"column:cid"`
        Name    string
        Type    string
        NotNull int            `gorm:"column:notnull"`
        Default sql.NullString `gorm:"column:dflt_value"`
        PK      int
    }
    if err := conn.Raw("PRAGMA table_info('privacy_rules')").Scan(&columns).Error; err != nil || len(columns) != 4 {
        return ErrPrivacySchema
    }
    seen := make(map[string]bool, 4)
    for _, col := range columns {
        if seen[col.Name] {
            return ErrPrivacySchema
        }
        seen[col.Name] = true
        switch col.Name {
        case "id":
            // INTEGER PRIMARY KEY 隐含非空，PRAGMA 的 notnull 可以为 0。
            if !strings.EqualFold(col.Type, "INTEGER") || col.PK != 1 || col.Default.Valid {
                return ErrPrivacySchema
            }
        case "source", "alias":
            if !strings.EqualFold(col.Type, "BLOB") || col.NotNull != 1 || col.PK != 0 || col.Default.Valid {
                return ErrPrivacySchema
            }
        case "enabled":
            if !strings.EqualFold(col.Type, "NUMERIC") || col.NotNull != 1 || col.PK != 0 || !privacyTrueDefault(col.Default) {
                return ErrPrivacySchema
            }
        default:
            return ErrPrivacySchema
        }
    }
    // 按真正的建表语法逐项比较 CHECK 和 AUTOINCREMENT，不能靠注释或字符串里的关键词冒充。
    if !privacySQLiteDefinitionMatches(tables[0].SQL.String) {
        return ErrPrivacySchema
    }
    var indexes []struct {
        Name    string
        Unique  int
        Partial int
    }
    if err := conn.Raw("PRAGMA index_list('privacy_rules')").Scan(&indexes).Error; err != nil {
        return ErrPrivacySchema
    }
    required := map[string]string{
        "ux_privacy_rules_source": "source",
        "ux_privacy_rules_alias":  "alias",
    }
    for _, index := range indexes {
        column, ok := required[index.Name]
        if !ok {
            continue
        }
        if index.Unique != 1 || index.Partial != 0 {
            return ErrPrivacySchema
        }
        var parts []struct {
            SeqNo int `gorm:"column:seqno"`
            CID   int `gorm:"column:cid"`
            Name  sql.NullString
        }
        // 读取索引的全部列，不能先按目标列过滤后把复合索引误认成单列唯一。
        if err := conn.Raw("SELECT seqno, cid, name FROM pragma_index_info(?) ORDER BY seqno", index.Name).Scan(&parts).Error; err != nil ||
            len(parts) != 1 || parts[0].SeqNo != 0 || parts[0].CID < 0 || !parts[0].Name.Valid || parts[0].Name.String != column {
            return ErrPrivacySchema
        }
        delete(required, index.Name)
    }
    if len(required) != 0 {
        return ErrPrivacySchema
    }
    return nil
}

// 将固定 DDL 拆成顶层定义。允许标识符引用、大小写、注释和无语义空白，
// 但未知约束、缺失 CHECK、生成列及 WITHOUT ROWID 等结构一律拒绝。
func privacySQLiteDefinitionMatches(ddl string) bool {
    actual, ok := privacySQLiteDefinitions(ddl)
    if !ok {
        return false
    }
    expected, ok := privacySQLiteDefinitions(privacySQLiteDDL)
    if !ok || len(actual) != len(expected) {
        return false
    }
    definitions := make(map[string]bool, len(expected))
    for _, definition := range expected {
        definitions[definition] = true
    }
    for _, definition := range actual {
        // 显式 NOT NULL 的 INTEGER PRIMARY KEY 也兼容，不要求 PRAGMA 标记为 1。
        if definition == "id integer primary key autoincrement not null" || definition == "id integer not null primary key autoincrement" {
            definition = "id integer primary key autoincrement"
        }
        if strings.HasPrefix(definition, "enabled numeric not null default ") {
            value := strings.TrimPrefix(definition, "enabled numeric not null default ")
            if privacyTrueDefault(sql.NullString{String: value, Valid: true}) {
                definition = "enabled numeric not null default true"
            }
        }
        if !definitions[definition] {
            return false
        }
        delete(definitions, definition)
    }
    return len(definitions) == 0
}

func privacySQLiteDefinitions(ddl string) ([]string, bool) {
    tokens, ok := privacySQLTokens(ddl)
    if !ok || len(tokens) < 5 || tokens[0] != "create" || tokens[1] != "table" {
        return nil, false
    }
    pos := 2
    if len(tokens) > pos+2 && tokens[pos] == "if" && tokens[pos+1] == "not" && tokens[pos+2] == "exists" {
        pos += 3
    }
    if len(tokens) <= pos+1 || tokens[pos] != "privacy_rules" || tokens[pos+1] != "(" {
        return nil, false
    }
    pos += 2
    start, depth := pos, 0
    var definitions []string
    for ; pos < len(tokens); pos++ {
        switch tokens[pos] {
        case "(":
            depth++
        case ")":
            if depth > 0 {
                depth--
                continue
            }
            if start == pos {
                return nil, false
            }
            definitions = append(definitions, strings.Join(tokens[start:pos], " "))
            tail := tokens[pos+1:]
            return definitions, len(tail) == 0 || (len(tail) == 1 && tail[0] == ";")
        case ",":
            if depth == 0 {
                if start == pos {
                    return nil, false
                }
                definitions = append(definitions, strings.Join(tokens[start:pos], " "))
                start = pos + 1
            }
        }
    }
    return nil, false
}

// 只识别固定 SQLite DDL 所需的 SQL 词法；单引号字面量保持大小写和内容。
func privacySQLTokens(text string) ([]string, bool) {
    var tokens []string
    for i := 0; i < len(text); {
        ch := text[i]
        if ch == ' ' || ch == '\t' || ch == '\r' || ch == '\n' {
            i++
            continue
        }
        if strings.HasPrefix(text[i:], "--") {
            for i < len(text) && text[i] != '\n' {
                i++
            }
            continue
        }
        if strings.HasPrefix(text[i:], "/*") {
            end := strings.Index(text[i+2:], "*/")
            if end < 0 {
                return nil, false
            }
            i += end + 4
            continue
        }
        if ch == '\'' || ch == '"' || ch == '`' || ch == '[' {
            end := ch
            if ch == '[' {
                end = ']'
            }
            start := i
            i++
            var value strings.Builder
            closed := false
            for i < len(text) {
                if text[i] == end {
                    if end != ']' && i+1 < len(text) && text[i+1] == end {
                        value.WriteByte(end)
                        i += 2
                        continue
                    }
                    i++
                    closed = true
                    break
                }
                value.WriteByte(text[i])
                i++
            }
            if !closed {
                return nil, false
            }
            if ch == '\'' {
                tokens = append(tokens, text[start:i])
            } else {
                // 引用标识符只能是一个词，避免将整段伪语法当作关键词。
                word := strings.ToLower(value.String())
                if word == "" || strings.ContainsAny(word, " \t\r\n(),;='") {
                    return nil, false
                }
                tokens = append(tokens, word)
            }
            continue
        }
        if strings.ContainsRune("(),;=", rune(ch)) {
            tokens = append(tokens, string(ch))
            i++
            continue
        }
        start := i
        for i < len(text) && ((text[i] >= 'a' && text[i] <= 'z') || (text[i] >= 'A' && text[i] <= 'Z') ||
            (text[i] >= '0' && text[i] <= '9') || text[i] == '_') {
            i++
        }
        if i == start {
            return nil, false
        }
        tokens = append(tokens, strings.ToLower(text[start:i]))
    }
    return tokens, true
}

func validatePrivacyMySQL(conn *gorm.DB) error {
    var count int64
    if err := conn.Raw(`SELECT COUNT(*) FROM information_schema.tables
        WHERE table_schema = DATABASE() AND BINARY table_name = 'privacy_rules' AND table_type = 'BASE TABLE'`).Scan(&count).Error; err != nil || count != 1 {
        return ErrPrivacySchema
    }
    var columns []struct {
        Name       string
        Type       string
        ColumnType string
        Length     sql.NullInt64
        Nullable   string
        Default    sql.NullString `gorm:"column:default_value"`
        Extra      string
        Generation string
    }
    if err := conn.Raw(`SELECT column_name AS name, data_type AS type, column_type,
        character_octet_length AS length, is_nullable AS nullable, column_default AS default_value,
        extra, generation_expression AS generation FROM information_schema.columns
        WHERE table_schema = DATABASE() AND BINARY table_name = 'privacy_rules'`).Scan(&columns).Error; err != nil || len(columns) != 4 {
        return ErrPrivacySchema
    }
    seen := make(map[string]bool, 4)
    for _, col := range columns {
        if seen[col.Name] || col.Nullable != "NO" || col.Generation != "" {
            return ErrPrivacySchema
        }
        seen[col.Name] = true
        switch col.Name {
        case "id":
            if (col.Type != "bigint" && col.Type != "int") || strings.Contains(strings.ToLower(col.ColumnType), "unsigned") ||
                strings.ToLower(col.Extra) != "auto_increment" || col.Default.Valid {
                return ErrPrivacySchema
            }
        case "source", "alias":
            length := int64(512)
            if col.Name == "alias" {
                length = 64
            }
            if col.Type != "varbinary" || !col.Length.Valid || col.Length.Int64 != length || col.Default.Valid || col.Extra != "" {
                return ErrPrivacySchema
            }
        case "enabled":
            if col.Type != "tinyint" || strings.ToLower(col.ColumnType) != "tinyint(1)" || !privacyTrueDefault(col.Default) ||
                (col.Extra != "" && strings.ToLower(col.Extra) != "default_generated") {
                return ErrPrivacySchema
            }
        default:
            return ErrPrivacySchema
        }
    }
    var parts []struct {
        Name      string
        Column    sql.NullString `gorm:"column:column_name"`
        NonUnique int
        Position  int
        Prefix    sql.NullInt64
        Type      string
    }
    if err := conn.Raw(`SELECT index_name AS name, column_name, non_unique, seq_in_index AS position,
        sub_part AS prefix, index_type AS type FROM information_schema.statistics
        WHERE table_schema = DATABASE() AND BINARY table_name = 'privacy_rules' ORDER BY index_name, seq_in_index`).Scan(&parts).Error; err != nil {
        return ErrPrivacySchema
    }
    grouped := make(map[string][]int)
    for i, part := range parts {
        grouped[part.Name] = append(grouped[part.Name], i)
    }
    for name, column := range map[string]string{
        "PRIMARY":                 "id",
        "ux_privacy_rules_source": "source",
        "ux_privacy_rules_alias":  "alias",
    } {
        positions := grouped[name]
        if len(positions) != 1 {
            return ErrPrivacySchema
        }
        part := parts[positions[0]]
        if part.NonUnique != 0 || part.Position != 1 || part.Prefix.Valid || !part.Column.Valid || part.Column.String != column || part.Type != "BTREE" {
            return ErrPrivacySchema
        }
    }
    return nil
}

var privacyPostgresNextval = regexp.MustCompile(`^nextval\('((?:[^']|'')+)'::regclass\)$`)

func validatePrivacyPostgres(conn *gorm.DB) error {
    var count int64
    if err := conn.Raw("SELECT COUNT(*) FROM pg_class WHERE oid = to_regclass('privacy_rules') AND relkind = 'r'").Scan(&count).Error; err != nil || count != 1 {
        return ErrPrivacySchema
    }
    var columns []struct {
        Name      string
        Type      string
        NotNull   bool
        Default   sql.NullString
        Identity  string
        Generated string
    }
    if err := conn.Raw(`SELECT a.attname AS name, t.typname AS type, a.attnotnull AS not_null,
        pg_get_expr(d.adbin, d.adrelid) AS "default", a.attidentity AS identity, a.attgenerated AS generated
        FROM pg_attribute a JOIN pg_type t ON t.oid = a.atttypid AND t.typnamespace = 'pg_catalog'::regnamespace
        LEFT JOIN pg_attrdef d ON d.adrelid = a.attrelid AND d.adnum = a.attnum
        WHERE a.attrelid = to_regclass('privacy_rules') AND a.attnum > 0 AND NOT a.attisdropped`).Scan(&columns).Error; err != nil || len(columns) != 4 {
        return ErrPrivacySchema
    }
    seen := make(map[string]bool, 4)
    for _, col := range columns {
        if seen[col.Name] || !col.NotNull || col.Generated != "" {
            return ErrPrivacySchema
        }
        seen[col.Name] = true
        switch col.Name {
        case "id":
            if col.Type != "int8" && col.Type != "int4" {
                return ErrPrivacySchema
            }
            // 必须是真实归属于 id 的递增序列，而非含 nextval 文本的任意默认表达式。
            if err := conn.Raw(`SELECT COUNT(*) FROM pg_sequence
                WHERE seqrelid = pg_get_serial_sequence('privacy_rules', 'id')::regclass
                AND seqincrement > 0 AND NOT seqcycle`).Scan(&count).Error; err != nil || count != 1 {
                return ErrPrivacySchema
            }
            if col.Identity == "" {
                if !col.Default.Valid {
                    return ErrPrivacySchema
                }
                match := privacyPostgresNextval.FindStringSubmatch(col.Default.String)
                if len(match) != 2 {
                    return ErrPrivacySchema
                }
                var same bool
                if err := conn.Raw("SELECT ?::regclass = pg_get_serial_sequence('privacy_rules', 'id')::regclass",
                    strings.ReplaceAll(match[1], "''", "'")).Scan(&same).Error; err != nil || !same {
                    return ErrPrivacySchema
                }
            } else if (col.Identity != "a" && col.Identity != "d") || col.Default.Valid {
                return ErrPrivacySchema
            }
        case "source", "alias":
            if col.Type != "bytea" || col.Default.Valid || col.Identity != "" {
                return ErrPrivacySchema
            }
        case "enabled":
            if col.Type != "bool" || !col.Default.Valid || strings.TrimSpace(col.Default.String) != "true" || col.Identity != "" {
                return ErrPrivacySchema
            }
        default:
            return ErrPrivacySchema
        }
    }
    var indexes []struct {
        Name       string
        Unique     bool
        Primary    bool
        Valid      bool
        Ready      bool
        Live       bool
        Immediate  bool
        Total      int
        Keys       int
        Full       bool
        Plain      bool
        Type       string
        Column     sql.NullString
        DefaultOps bool
    }
    // indnatts 同时覆盖 INCLUDE 列；真实列号和默认操作符类排除表达式及另类相等语义。
    if err := conn.Raw(`SELECT c.relname AS name, i.indisunique AS "unique", i.indisprimary AS "primary",
        i.indisvalid AS valid, i.indisready AS ready, i.indislive AS live, i.indimmediate AS immediate,
        i.indnatts AS total, i.indnkeyatts AS "keys", i.indpred IS NULL AS "full", i.indexprs IS NULL AS plain,
        am.amname AS type, a.attname AS "column", (op.opcdefault AND op.opcintype = a.atttypid) AS default_ops
        FROM pg_index i JOIN pg_class c ON c.oid = i.indexrelid
        JOIN pg_am am ON am.oid = c.relam
        LEFT JOIN pg_attribute a ON a.attrelid = i.indrelid AND a.attnum = i.indkey[0]
        LEFT JOIN pg_opclass op ON op.oid = i.indclass[0]
        WHERE i.indrelid = to_regclass('privacy_rules')`).Scan(&indexes).Error; err != nil {
        return ErrPrivacySchema
    }
    required := map[string]string{
        "ux_privacy_rules_source": "source",
        "ux_privacy_rules_alias":  "alias",
    }
    primaryCount := 0
    for _, index := range indexes {
        column, requiredIndex := required[index.Name]
        if index.Primary {
            primaryCount++
            column = "id"
            if requiredIndex {
                return ErrPrivacySchema
            }
        } else if !requiredIndex {
            continue
        }
        if !index.Unique || !index.Valid || !index.Ready || !index.Live || !index.Immediate || index.Total != 1 || index.Keys != 1 ||
            !index.Full || !index.Plain || index.Type != "btree" || !index.DefaultOps || !index.Column.Valid || index.Column.String != column {
            return ErrPrivacySchema
        }
        delete(required, index.Name)
    }
    if primaryCount != 1 || len(required) != 0 {
        return ErrPrivacySchema
    }
    return nil
}
