package op

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
)

func TestMain(m *testing.M) {
	testDir, err := os.MkdirTemp("", "octopus-op-test-")
	if err != nil {
		panic(err)
	}
	if err := db.InitDB("sqlite", filepath.Join(testDir, "op.db"), false); err != nil {
		panic(err)
	}
	if err := InitCache(); err != nil {
		panic(err)
	}

	exitCode := m.Run()
	if err := db.Close(); err != nil && exitCode == 0 {
		fmt.Fprintf(os.Stderr, "close test database: %v\n", err)
		exitCode = 1
	}
	if err := os.RemoveAll(testDir); err != nil && exitCode == 0 {
		fmt.Fprintf(os.Stderr, "remove test database: %v\n", err)
		exitCode = 1
	}
	os.Exit(exitCode)
}

func TestSettingRefreshCacheCreatesMissingPassthroughSetting(t *testing.T) {
	originalSetting := model.Setting{Key: model.SettingKeyPassthroughEnabled, Value: "false"}
	if err := db.GetDB().FirstOrCreate(&originalSetting, model.Setting{Key: model.SettingKeyPassthroughEnabled}).Error; err != nil {
		t.Fatalf("read original passthrough setting: %v", err)
	}
	originalValue := originalSetting.Value

	t.Cleanup(func() {
		if err := db.GetDB().Model(&model.Setting{Key: model.SettingKeyPassthroughEnabled}).Update("Value", originalValue).Error; err != nil {
			t.Errorf("restore passthrough setting: %v", err)
		}
		if err := settingRefreshCache(context.Background()); err != nil {
			t.Errorf("restore setting cache: %v", err)
		}
	})

	if err := db.GetDB().Where("key = ?", model.SettingKeyPassthroughEnabled).Delete(&model.Setting{}).Error; err != nil {
		t.Fatalf("delete passthrough setting: %v", err)
	}
	if err := settingRefreshCache(context.Background()); err != nil {
		t.Fatalf("settingRefreshCache() error = %v", err)
	}

	enabled, err := SettingGetBool(model.SettingKeyPassthroughEnabled)
	if err != nil {
		t.Fatalf("SettingGetBool() error = %v", err)
	}
	if enabled {
		t.Fatal("missing passthrough setting was not recreated with the disabled default")
	}

	var setting model.Setting
	if err := db.GetDB().First(&setting, "key = ?", model.SettingKeyPassthroughEnabled).Error; err != nil {
		t.Fatalf("read recreated passthrough setting: %v", err)
	}
	if setting.Value != "false" {
		t.Fatalf("recreated passthrough setting = %q, want false", setting.Value)
	}
}
