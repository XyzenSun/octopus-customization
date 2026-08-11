package migrate

import (
	"fmt"

	"github.com/bestruirui/octopus/internal/model"
	"gorm.io/gorm"
)

func init() {
	RegisterBeforeAutoMigration(Migration{
		Version: 5,
		Up:      addUserTwoFactorSecret,
	})
}

// 005: 为用户增加两步验证(TOTP)密钥列。
// two_factor_enabled 开关不在此处理——op.SettingInit 每次启动会按 DefaultSettings 自动补齐缺失的 key。
func addUserTwoFactorSecret(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("db is nil")
	}
	if !db.Migrator().HasTable(&model.User{}) {
		return nil
	}
	if !db.Migrator().HasColumn(&model.User{}, "TwoFactorSecret") {
		if err := db.Migrator().AddColumn(&model.User{}, "TwoFactorSecret"); err != nil {
			return fmt.Errorf("failed to add users.two_factor_secret: %w", err)
		}
	}
	return nil
}
