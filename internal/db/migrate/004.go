package migrate

import (
	"fmt"

	"github.com/bestruirui/octopus/internal/model"
	"gorm.io/gorm"
)

func init() {
	RegisterBeforeAutoMigration(Migration{
		Version: 4,
		Up:      addGroupRequestOptions,
	})
}

// 004: 为分组增加请求参数覆盖和自定义请求头配置。
func addGroupRequestOptions(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("db is nil")
	}
	if !db.Migrator().HasTable(&model.Group{}) {
		return nil
	}
	if !db.Migrator().HasColumn(&model.Group{}, "CustomHeader") {
		if err := db.Migrator().AddColumn(&model.Group{}, "CustomHeader"); err != nil {
			return fmt.Errorf("failed to add groups.custom_header: %w", err)
		}
	}
	if !db.Migrator().HasColumn(&model.Group{}, "ParamOverride") {
		if err := db.Migrator().AddColumn(&model.Group{}, "ParamOverride"); err != nil {
			return fmt.Errorf("failed to add groups.param_override: %w", err)
		}
	}
	return nil
}
