package db

import (
    "errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/bestruirui/octopus/internal/db/migrate"
	"github.com/bestruirui/octopus/internal/model"
	"github.com/glebarez/sqlite"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var db *gorm.DB

func InitDB(dbType, dsn string, debug bool) error {
	var err error
	gormConfig := gorm.Config{Logger: logger.Discard}
	if debug {
		gormConfig.Logger = logger.Default.LogMode(logger.Info)
	}

	switch dbType {
	case "sqlite":
		db, err = initSQLite(dsn, &gormConfig)
	case "mysql":
		db, err = initMySQL(dsn, &gormConfig)
	case "postgres", "postgresql":
		db, err = initPostgres(dsn, &gormConfig)
	default:
		return fmt.Errorf("unsupported database type: %s", dbType)
	}

	if err != nil {
		return err
	}

	sqlDB, err := db.DB()
	if err != nil {
		return err
	}

	sqlDB.SetMaxIdleConns(10)
	sqlDB.SetMaxOpenConns(100)
	sqlDB.SetConnMaxLifetime(time.Hour)
	sqlDB.SetConnMaxIdleTime(10 * time.Minute)

    // 先检查已有隐私表，防止 AutoMigrate 在校验前改写未知结构。
    privacyConn := db.Session(&gorm.Session{Logger: logger.Discard})
    privacyExists, err := migrate.PrivacySchemaExists(privacyConn)
    if err != nil {
        return migrate.ErrPrivacySchema
    }
    if privacyExists {
        if err := migrate.ValidatePrivacySchema(privacyConn); err != nil {
            return migrate.ErrPrivacySchema
        }
    }

	if err := migrate.BeforeAutoMigrate(db); err != nil {
        if errors.Is(err, migrate.ErrPrivacySchema) {
            return migrate.ErrPrivacySchema
        }
		return err
	}
    // SQLite 的隐私表始终只走版本化 DDL，不加入下方通用模型清单。
	if err := db.AutoMigrate(
		&model.User{},
		&model.Channel{},
		&model.ChannelKey{},
		&model.Group{},
		&model.GroupItem{},
		&model.LLMInfo{},
		&model.APIKey{},
		&model.Setting{},
		&model.StatsTotal{},
		&model.StatsDaily{},
		&model.StatsHourly{},
		&model.StatsModel{},
		&model.StatsChannel{},
		&model.StatsAPIKey{},
		&model.RelayLog{},
		&migrate.MigrationRecord{},
	); err != nil {
		return err
	}
    // 只将 MySQL/PostgreSQL 的新隐私表加入独立的静默迁移清单，
    // 已存在且校验通过的表也不交给 AutoMigrate；其他模型保留原有 SQL 日志行为。
    if !privacyExists && db.Dialector.Name() != "sqlite" {
        if err := privacyConn.AutoMigrate(&model.PrivacyRule{}); err != nil {
            return migrate.ErrPrivacySchema
        }
        if err := migrate.ValidatePrivacySchema(privacyConn); err != nil {
            return migrate.ErrPrivacySchema
        }
    }
	if err := migrate.AfterAutoMigrate(db); err != nil {
		return err
	}
    // 不依赖 006 是否曾成功执行，每次启动迁移结束都重新检查完整结构。
    if err := migrate.ValidatePrivacySchema(privacyConn); err != nil {
        return migrate.ErrPrivacySchema
    }
    // PostgreSQL 迁移可能使已缓存的预执行计划失效，完成后清理连接计划。
	if db.Dialector != nil && db.Dialector.Name() == "postgres" {
		db.Exec("DEALLOCATE ALL")
		db.Exec("DISCARD ALL")
	}
	return nil
}

// initSQLite 使用指定文件路径初始化 SQLite，并为每个连接应用运行参数。
func initSQLite(path string, config *gorm.Config) (*gorm.DB, error) {
	params := url.Values{}
	params.Add("_pragma", "journal_mode(WAL)")
	params.Add("_pragma", "synchronous(NORMAL)")
	params.Add("_pragma", "cache_size(10000)")
	params.Add("_pragma", "busy_timeout(5000)")
	params.Add("_pragma", "foreign_keys(ON)")
	params.Add("_pragma", "auto_vacuum(INCREMENTAL)")
	params.Add("_pragma", "mmap_size(268435456)")
	params.Add("_pragma", "locking_mode(NORMAL)")
	return gorm.Open(sqlite.Open(path+"?"+params.Encode()), config)
}

func initMySQL(dsn string, config *gorm.Config) (*gorm.DB, error) {
	// DSN 格式: user:password@tcp(host:port)/dbname?charset=utf8mb4&parseTime=True&loc=Local
	if !strings.Contains(dsn, "?") {
		dsn += "?charset=utf8mb4&parseTime=True&loc=Local"
	}
	return gorm.Open(mysql.Open(dsn), config)
}

func initPostgres(dsn string, config *gorm.Config) (*gorm.DB, error) {
	// DSN 格式: host=localhost user=postgres password=xxx dbname=octopus port=5432 sslmode=disable
	return gorm.Open(postgres.Open(dsn), config)
}

func Close() error {
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}

func GetDB() *gorm.DB {
	return db
}
