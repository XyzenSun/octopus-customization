package op

import (
	"context"
	"fmt"
	"time"

    "github.com/bestruirui/octopus/internal/model"
)

func InitCache() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
    if err := initSettingAndPrivacyCache(ctx); err != nil {
        return err
	}
    return refreshResourceCaches(ctx)
}

// 导入已在写锁内发布隐私规则和配置. 仅刷新普通设置及资源缓存,
// 不重新编译隐私状态, 也不以可能失败的二次数据库读取覆盖隐私设置.
func refreshImportedCaches(ctx context.Context) error {
    var settings []model.Setting
    keys := privacySettingKeys()
    if err := privacyDB(ctx).Where("key NOT IN ?", keys[:]).Find(&settings).Error; err != nil {
        return err
    }
    for _, setting := range settings {
        if err := setting.Validate(); err != nil {
            return err
        }
    }
    for _, setting := range settings {
        settingCache.Set(setting.Key, setting.Value)
    }
    return refreshResourceCaches(ctx)
}

func refreshResourceCaches(ctx context.Context) error {
	if err := channelRefreshCache(ctx); err != nil {
		return fmt.Errorf("channel refresh cache error: %v", err)
	}
	if err := groupRefreshCache(ctx); err != nil {
		return fmt.Errorf("group refresh cache error: %v", err)
	}
	if err := apiKeyRefreshCache(ctx); err != nil {
		return fmt.Errorf("api key refresh cache error: %v", err)
	}
	if err := llmRefreshCache(ctx); err != nil {
		return fmt.Errorf("llm refresh cache error: %v", err)
	}
	if err := statsRefreshCache(ctx); err != nil {
		return fmt.Errorf("stats refresh cache error: %v", err)
	}
	return nil
}

func initSettingAndPrivacyCache(ctx context.Context) error {
    privacyWriteMu.Lock()
    defer privacyWriteMu.Unlock()
    if err := settingRefreshCache(ctx); err != nil {
        return fmt.Errorf("setting refresh cache error: %w", err)
    }
    if err := privacyRefreshLocked(ctx); err != nil {
        return fmt.Errorf("privacy refresh cache error: %w", err)
    }
    return nil
}

func SaveCache() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := StatsSaveDB(ctx); err != nil {
		return err
	}
	if err := ChannelKeySaveDB(ctx); err != nil {
		return err
	}
	if err := RelayLogSaveDBTask(ctx); err != nil {
		return err
	}
	return nil
}
