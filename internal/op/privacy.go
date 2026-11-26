package op

import (
    "context"
    "errors"
    "slices"
    "sync"
    "sync/atomic"

    "github.com/bestruirui/octopus/internal/db"
    "github.com/bestruirui/octopus/internal/model"
    "github.com/bestruirui/octopus/internal/privacy"
    sqlite "github.com/glebarez/go-sqlite"
    "github.com/go-sql-driver/mysql"
    "github.com/jackc/pgx/v5/pgconn"
    "gorm.io/gorm"
    "gorm.io/gorm/logger"
    sqlitecode "modernc.org/sqlite/lib"
)

var (
    privacyWriteMu    sync.Mutex
    privacyCurrent    atomic.Pointer[privacy.Snapshot]
    ErrPrivacyStorage = errors.New("privacy storage operation failed")
)

func PrivacySnapshot() *privacy.Snapshot {
    return privacyCurrent.Load()
}

func privacyDB(ctx context.Context) *gorm.DB {
    return db.GetDB().Session(&gorm.Session{Logger: logger.Discard, Context: ctx})
}

func privacyStorageError(err error) error {
    if err == nil {
        return nil
    }
    for _, fixed := range []error{privacy.ErrRuleNotFound, privacy.ErrInvalidRule, privacy.ErrInvalidConfig, privacy.ErrCompile} {
        if errors.Is(err, fixed) {
            return fixed
        }
    }
    var sqliteError *sqlite.Error
    var mysqlError *mysql.MySQLError
    var postgresError *pgconn.PgError
    switch {
    case errors.Is(err, gorm.ErrDuplicatedKey):
        return privacy.ErrRuleConflict
    case errors.As(err, &sqliteError):
        if code := sqliteError.Code(); code == sqlitecode.SQLITE_CONSTRAINT_UNIQUE || code == sqlitecode.SQLITE_CONSTRAINT_PRIMARYKEY {
            return privacy.ErrRuleConflict
        }
    case errors.As(err, &mysqlError):
        if mysqlError.Number == 1062 {
            return privacy.ErrRuleConflict
        }
    case errors.As(err, &postgresError):
        if postgresError.Code == "23505" {
            return privacy.ErrRuleConflict
        }
    }
    return ErrPrivacyStorage
}

func privacyLoadRules(conn *gorm.DB) ([]model.PrivacyRule, error) {
    rules := make([]model.PrivacyRule, 0)
    if err := conn.Order("id ASC").Find(&rules).Error; err != nil {
        return nil, privacyStorageError(err)
    }
    for _, rule := range rules {
        if rule.ID <= 0 {
            return nil, privacy.ErrInvalidRule
        }
        if err := rule.Validate(); err != nil {
            return nil, err
        }
    }
    return rules, nil
}

func PrivacyRuleList(ctx context.Context) ([]model.PrivacyRule, error) {
    return privacyLoadRules(privacyDB(ctx))
}

func privacySettingsFromCache() map[model.SettingKey]string {
    settings := make(map[model.SettingKey]string, 3)
    for _, key := range privacySettingKeys() {
        if value, present := settingCache.Get(key); present {
            settings[key] = value
        }
    }
    return settings
}

func privacySettingKeys() [3]model.SettingKey {
    return [3]model.SettingKey{model.SettingKeyPrivacyEnabled, model.SettingKeyPrivacyScopes, model.SettingKeyPrivacyFailurePolicy}
}

func privacyConfig(settings map[model.SettingKey]string) (privacy.Config, error) {
    return model.ParsePrivacyConfig(settings[model.SettingKeyPrivacyEnabled], settings[model.SettingKeyPrivacyScopes], settings[model.SettingKeyPrivacyFailurePolicy])
}

func PrivacyRefresh(ctx context.Context) error {
    privacyWriteMu.Lock()
    defer privacyWriteMu.Unlock()
    return privacyRefreshLocked(ctx)
}

func privacyRefreshLocked(ctx context.Context) error {
    rules, err := privacyLoadRules(privacyDB(ctx))
    if err != nil {
        return err
    }
    config, err := privacyConfig(privacySettingsFromCache())
    if err != nil {
        return err
    }
    candidate := make([]privacy.Rule, len(rules))
    for index, rule := range rules {
        candidate[index] = rule.Rule()
    }
    version := uint64(1)
    if current := PrivacySnapshot(); current != nil {
        version = current.Version() + 1
    }
    snapshot, err := privacy.Compile(config, candidate, version)
    if err != nil {
        return err
    }
    privacyCurrent.Store(snapshot)
    return nil
}

// 管理写入在同一把锁内固定候选、提交数据库并发布. 请求侧不复制规则.
func privacyCandidate(current *privacy.Snapshot) []privacy.Rule {
    rules := make([]privacy.Rule, 0, current.RuleCount()+1)
    for index := 0; index < current.RuleCount(); index++ {
        rule, _ := current.Rule(index)
        rules = append(rules, rule)
    }
    return rules
}

// *bool 让显式 false 不会被 GORM 实体的 default:true 重写.
type privacyRuleInsert struct {
    ID      int `gorm:"primaryKey;autoIncrement"`
    Source  model.PrivacyBytes
    Alias   model.PrivacyBytes
    Enabled *bool
}

func (privacyRuleInsert) TableName() string { return (model.PrivacyRule{}).TableName() }

func PrivacyRuleCreate(ctx context.Context, input model.PrivacyRuleCreate) (model.PrivacyRule, error) {
    if err := input.Validate(); err != nil {
        return model.PrivacyRule{}, err
    }
    privacyWriteMu.Lock()
    defer privacyWriteMu.Unlock()

    current := PrivacySnapshot()
    if current == nil {
        return model.PrivacyRule{}, ErrPrivacyStorage
    }
    rule := input.Rule()
    finalize, err := privacy.CompileForCreate(current.Config(), append(privacyCandidate(current), rule), current.Version()+1)
    if err != nil {
        return model.PrivacyRule{}, err
    }
    insert := privacyRuleInsert{Source: input.Source, Alias: input.Alias, Enabled: &rule.Enabled}
    var next *privacy.Snapshot
    err = privacyDB(ctx).Transaction(func(tx *gorm.DB) error {
        if err := tx.Create(&insert).Error; err != nil {
            return err
        }
        next = finalize(insert.ID)
        if next == nil {
            return ErrPrivacyStorage
        }
        return nil
    })
    if err != nil {
        return model.PrivacyRule{}, privacyStorageError(err)
    }
    privacyCurrent.Store(next)
    return model.PrivacyRule{ID: insert.ID, Source: input.Source, Alias: input.Alias, Enabled: rule.Enabled}, nil
}

func PrivacyRuleUpdate(ctx context.Context, input model.PrivacyRuleUpdate) (model.PrivacyRule, error) {
    if err := input.Validate(); err != nil {
        return model.PrivacyRule{}, err
    }
    rule := input.Rule()
    err := privacyMutateRule(ctx, rule.ID, func(candidate []privacy.Rule, index int) []privacy.Rule {
        candidate[index] = rule
        return candidate
    }, func(tx *gorm.DB) error {
        result := tx.Model(&model.PrivacyRule{}).Where("id = ?", rule.ID).Updates(map[string]any{
            "source": input.Source, "alias": input.Alias, "enabled": rule.Enabled,
        })
        if result.Error != nil || result.RowsAffected != 0 {
            return result.Error
        }
        // MySQL 对同值更新可能返回 0, 需要区分未改变和该行已不存在.
        var count int64
        if err := tx.Model(&model.PrivacyRule{}).Where("id = ?", rule.ID).Count(&count).Error; err != nil {
            return err
        }
        if count == 0 {
            return privacy.ErrRuleNotFound
        }
        return nil
    })
    if err != nil {
        return model.PrivacyRule{}, err
    }
    return model.PrivacyRule{ID: rule.ID, Source: input.Source, Alias: input.Alias, Enabled: rule.Enabled}, nil
}

func PrivacyRuleDelete(ctx context.Context, id int) error {
    if id <= 0 {
        return privacy.ErrInvalidRule
    }
    return privacyMutateRule(ctx, id, func(candidate []privacy.Rule, index int) []privacy.Rule {
        return slices.Delete(candidate, index, index+1)
    }, func(tx *gorm.DB) error {
        result := tx.Delete(&model.PrivacyRule{}, id)
        if result.Error == nil && result.RowsAffected == 0 {
            return privacy.ErrRuleNotFound
        }
        return result.Error
    })
}

func privacyMutateRule(ctx context.Context, id int, change func([]privacy.Rule, int) []privacy.Rule, write func(*gorm.DB) error) error {
    privacyWriteMu.Lock()
    defer privacyWriteMu.Unlock()

    current := PrivacySnapshot()
    if current == nil {
        return ErrPrivacyStorage
    }
    candidate := privacyCandidate(current)
    index := slices.IndexFunc(candidate, func(rule privacy.Rule) bool { return rule.ID == id })
    if index < 0 {
        return privacy.ErrRuleNotFound
    }
    next, err := privacy.Compile(current.Config(), change(candidate, index), current.Version()+1)
    if err != nil {
        return err
    }
    if err := privacyDB(ctx).Transaction(write); err != nil {
        return privacyStorageError(err)
    }
    privacyCurrent.Store(next)
    return nil
}

func PrivacySetSetting(ctx context.Context, key model.SettingKey, value string) error {
    if !model.IsPrivacySettingKey(key) {
        return privacy.ErrInvalidConfig
    }
    setting := model.Setting{Key: key, Value: value}
    if err := setting.Validate(); err != nil {
        return err
    }
    privacyWriteMu.Lock()
    defer privacyWriteMu.Unlock()

    current := PrivacySnapshot()
    if current == nil {
        return ErrPrivacyStorage
    }
    settings := privacySettingsFromCache()
    if settings[key] == value {
        return nil
    }
    settings[key] = value
    config, err := privacyConfig(settings)
    if err != nil {
        return err
    }
    next, err := privacy.Compile(config, privacyCandidate(current), current.Version()+1)
    if err != nil {
        return err
    }
    err = privacyDB(ctx).Transaction(func(tx *gorm.DB) error {
        result := tx.Model(&model.Setting{}).Where(&model.Setting{Key: key}).Update("value", value)
        if result.Error != nil {
            return result.Error
        }
        if result.RowsAffected == 0 {
            return ErrPrivacyStorage
        }
        return nil
    })
    if err != nil {
        return privacyStorageError(err)
    }
    privacyCurrent.Store(next)
    settingCache.Set(key, value)
    return nil
}
