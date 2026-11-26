package op

import (
	"context"
    "database/sql"
    "errors"
	"fmt"
	"time"

	"github.com/bestruirui/octopus/internal/model"
    "github.com/bestruirui/octopus/internal/privacy"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const dbDumpVersion = 1

var (
    ErrBackupStorage               = errors.New("backup storage operation failed")
    ErrImportCommittedCacheRefresh = errors.New("backup import committed but cache refresh failed")
)

func DBExportAll(ctx context.Context, includeLogs, includeStats bool) (*model.DBDump, error) {
    privacyWriteMu.Lock()
    defer privacyWriteMu.Unlock()

	d := &model.DBDump{
		Version:      dbDumpVersion,
		ExportedAt:   time.Now().UTC(),
		IncludeLogs:  includeLogs,
		IncludeStats: includeStats,
	}

    // 在同一持锁, 静默, 只读事务中读取规则和设置.
    // 不刷新实时密钥或统计聚合.
    err := privacyDB(ctx).Transaction(func(conn *gorm.DB) error {
	if err := conn.Find(&d.Channels).Error; err != nil {
            return ErrBackupStorage
	}
	if err := conn.Find(&d.ChannelKeys).Error; err != nil {
            return ErrBackupStorage
	}
	if err := conn.Find(&d.Groups).Error; err != nil {
            return ErrBackupStorage
	}
	if err := conn.Find(&d.GroupItems).Error; err != nil {
            return ErrBackupStorage
	}
	if err := conn.Find(&d.LLMInfos).Error; err != nil {
            return ErrBackupStorage
	}
	if err := conn.Find(&d.APIKeys).Error; err != nil {
            return ErrBackupStorage
	}
	if err := conn.Find(&d.Settings).Error; err != nil {
            return ErrBackupStorage
	}
	d.Settings = excludeTwoFactorSetting(d.Settings)
        settings := make(map[model.SettingKey]string, 3)
        for _, setting := range d.Settings {
            if model.IsPrivacySettingKey(setting.Key) {
                if err := setting.Validate(); err != nil {
                    return err
                }
                settings[setting.Key] = setting.Value
            }
        }
        if _, err := privacyConfig(settings); err != nil {
            return err
        }
        var err error
        d.PrivacyRules, err = privacyLoadRules(conn)
        if err != nil {
            return err
        }

	if includeStats {
		if err := conn.Find(&d.StatsTotal).Error; err != nil {
                return ErrBackupStorage
		}
		if err := conn.Find(&d.StatsDaily).Error; err != nil {
                return ErrBackupStorage
		}
		if err := conn.Find(&d.StatsHourly).Error; err != nil {
                return ErrBackupStorage
		}
		if err := conn.Find(&d.StatsModel).Error; err != nil {
                return ErrBackupStorage
		}
		if err := conn.Find(&d.StatsChannel).Error; err != nil {
                return ErrBackupStorage
		}
		if err := conn.Find(&d.StatsAPIKey).Error; err != nil {
                return ErrBackupStorage
		}
	}

	if includeLogs {
		if err := conn.Find(&d.RelayLogs).Error; err != nil {
                return ErrBackupStorage
		}
	}

        return nil
    }, &sql.TxOptions{ReadOnly: true})
    if err != nil {
        return nil, backupStorageError(err)
    }
	return d, nil
}

func DBImportIncremental(ctx context.Context, dump *model.DBDump) (*model.DBImportResult, error) {
	if dump == nil {
		return nil, fmt.Errorf("empty dump")
	}

	if dump.Version != 0 && dump.Version != dbDumpVersion {
		return nil, fmt.Errorf("unsupported dump version: %d", dump.Version)
	}

    privacyWriteMu.Lock()
    defer privacyWriteMu.Unlock()
    current := PrivacySnapshot()
    if current == nil {
        return nil, ErrPrivacyStorage
    }
    rules, inserts, err := privacyImportRules(current, dump.PrivacyRules)
    if err != nil {
        return nil, err
    }
    settings := privacySettingsFromCache()
    importedSettings := excludeTwoFactorSetting(dump.Settings)
    for _, setting := range importedSettings {
        if err := setting.Validate(); err != nil {
            return nil, err
        }
        if model.IsPrivacySettingKey(setting.Key) {
            settings[setting.Key] = setting.Value
        }
    }
    config, err := privacyConfig(settings)
    if err != nil {
        return nil, err
    }
    // 事务提交后不得执行可能失败的匹配器操作.
    next, err := privacy.Compile(config, rules, current.Version()+1)
    if err != nil {
        return nil, err
    }
	res := &model.DBImportResult{RowsAffected: map[string]int64{}}

    err = privacyDB(ctx).Transaction(func(tx *gorm.DB) error {
        var affected int64
        for _, rule := range inserts {
            enabled := rule.Enabled
            insert := privacyRuleInsert{ID: rule.ID, Source: rule.Source, Alias: rule.Alias, Enabled: &enabled}
            result := tx.Create(&insert)
            if result.Error != nil {
                return privacyStorageError(result.Error)
            }
            affected += result.RowsAffected
        }
        res.RowsAffected["privacy_rules"] = affected
		// base tables
		if n, err := createDoNothing(tx, dump.Channels); err != nil {
			return fmt.Errorf("import channels: %w", err)
		} else {
			res.RowsAffected["channels"] = n
		}
		if n, err := createDoNothing(tx, dump.ChannelKeys); err != nil {
			return fmt.Errorf("import channel_keys: %w", err)
		} else {
			res.RowsAffected["channel_keys"] = n
		}
		if n, err := createDoNothing(tx, dump.Groups); err != nil {
			return fmt.Errorf("import groups: %w", err)
		} else {
			res.RowsAffected["groups"] = n
		}
		if n, err := createDoNothing(tx, dump.GroupItems); err != nil {
			return fmt.Errorf("import group_items: %w", err)
		} else {
			res.RowsAffected["group_items"] = n
		}
		if n, err := createUpsertAll(tx, dump.LLMInfos, []clause.Column{{Name: "name"}}); err != nil {
			return fmt.Errorf("import llm_infos: %w", err)
		} else {
			res.RowsAffected["llm_infos"] = n
		}
		if n, err := createDoNothing(tx, dump.APIKeys); err != nil {
			return fmt.Errorf("import api_keys: %w", err)
		} else {
			res.RowsAffected["api_keys"] = n
		}
        if n, err := createUpsertSettings(tx, importedSettings); err != nil {
			return fmt.Errorf("import settings: %w", err)
		} else {
			res.RowsAffected["settings"] = n
		}

		if dump.IncludeStats {
			if n, err := createUpsertAll(tx, dump.StatsTotal, []clause.Column{{Name: "id"}}); err != nil {
				return fmt.Errorf("import stats_total: %w", err)
			} else {
				res.RowsAffected["stats_total"] = n
			}
			if n, err := createUpsertAll(tx, dump.StatsDaily, []clause.Column{{Name: "date"}}); err != nil {
				return fmt.Errorf("import stats_daily: %w", err)
			} else {
				res.RowsAffected["stats_daily"] = n
			}
			if n, err := createUpsertAll(tx, dump.StatsHourly, []clause.Column{{Name: "hour"}}); err != nil {
				return fmt.Errorf("import stats_hourly: %w", err)
			} else {
				res.RowsAffected["stats_hourly"] = n
			}
			if n, err := createUpsertAll(tx, dump.StatsModel, []clause.Column{{Name: "id"}}); err != nil {
				return fmt.Errorf("import stats_model: %w", err)
			} else {
				res.RowsAffected["stats_model"] = n
			}
			if n, err := createUpsertAll(tx, dump.StatsChannel, []clause.Column{{Name: "channel_id"}}); err != nil {
				return fmt.Errorf("import stats_channel: %w", err)
			} else {
				res.RowsAffected["stats_channel"] = n
			}
			if n, err := createUpsertAll(tx, dump.StatsAPIKey, []clause.Column{{Name: "api_key_id"}}); err != nil {
				return fmt.Errorf("import stats_api_key: %w", err)
			} else {
				res.RowsAffected["stats_api_key"] = n
			}
		}

		if dump.IncludeLogs {
			if n, err := createDoNothing(tx, dump.RelayLogs); err != nil {
				return fmt.Errorf("import relay_logs: %w", err)
			} else {
				res.RowsAffected["relay_logs"] = n
			}
		}

        if len(inserts) > 0 {
            if err := privacyImportSequence(tx); err != nil {
                return err
            }
        }
		return nil
	})
	if err != nil {
        return nil, backupStorageError(err)
    }
    privacyCurrent.Store(next)
    for key, value := range settings {
        settingCache.Set(key, value)
    }
    // 即使其他缓存刷新失败, 也保留已发布的隐私状态.
    // 错误明确表示导入已经提交.
    refreshCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
    defer cancel()
    if err := refreshImportedCaches(refreshCtx); err != nil {
        return res, ErrImportCommittedCacheRefresh
	}
	return res, nil
}

func backupStorageError(err error) error {
    for _, fixed := range []error{privacy.ErrInvalidRule, privacy.ErrInvalidConfig, privacy.ErrRuleConflict, privacy.ErrCompile, ErrPrivacyStorage} {
        if errors.Is(err, fixed) {
            return fixed
        }
    }
    // 其他导入表也可能含敏感数据. 不通过备份处理器返回驱动 SQL,
    // 参数值或 panic 文本.
    return ErrBackupStorage
}

// 相同映射保持幂等, 保留目标 ID 和启用状态.
// 旧备份缺失数组时不作修改, 不删除或替换已有规则.
func privacyImportRules(current *privacy.Snapshot, rows []model.PrivacyRule) ([]privacy.Rule, []model.PrivacyRule, error) {
    candidate := privacyCandidate(current)
    sources := make(map[string]privacy.Rule, len(candidate)+len(rows))
    aliases := make(map[string]bool, len(candidate)+len(rows))
    ids := make(map[int]bool, len(candidate)+len(rows))
    for _, rule := range candidate {
        sources[rule.Source], aliases[rule.Alias], ids[rule.ID] = rule, true, true
    }
    inserts := make([]model.PrivacyRule, 0, len(rows))
    for _, row := range rows {
        if row.ID <= 0 {
            return nil, nil, privacy.ErrInvalidRule
        }
        if err := row.Validate(); err != nil {
            return nil, nil, err
        }
        rule := row.Rule()
        if existing, ok := sources[rule.Source]; ok {
            if existing.Alias != rule.Alias {
                return nil, nil, privacy.ErrRuleConflict
            }
            continue
        }
        if aliases[rule.Alias] || ids[rule.ID] {
            return nil, nil, privacy.ErrRuleConflict
        }
        candidate = append(candidate, rule)
        inserts = append(inserts, row)
        sources[rule.Source], aliases[rule.Alias], ids[rule.ID] = rule, true, true
    }
    return candidate, inserts, nil
}

func privacyImportSequence(tx *gorm.DB) error {
    if tx.Dialector.Name() != "postgres" {
        return nil
    }
    if err := tx.Exec("LOCK TABLE privacy_rules IN SHARE ROW EXCLUSIVE MODE").Error; err != nil {
        return ErrPrivacyStorage
    }
    var sequence sql.NullString
    if err := tx.Raw("SELECT pg_get_serial_sequence('privacy_rules', 'id')").Scan(&sequence).Error; err != nil || !sequence.Valid || sequence.String == "" {
        return ErrPrivacyStorage
    }
    // 仅绑定数据库返回的 regclass, 不使用用户标识符.
    // PostgreSQL 序列不回滚, 导入失败可能留下空号.
    if err := tx.Exec(`SELECT setval(?::regclass,
        GREATEST(COALESCE((SELECT MAX(id) FROM privacy_rules), 0), nextval(?::regclass)), true)`, sequence.String, sequence.String).Error; err != nil {
        return ErrPrivacyStorage
    }
    return nil
}

func createDoNothing[T any](tx *gorm.DB, rows []T) (int64, error) {
	if len(rows) == 0 {
		return 0, nil
	}
	result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&rows)
	return result.RowsAffected, result.Error
}

func createUpsertAll[T any](tx *gorm.DB, rows []T, columns []clause.Column) (int64, error) {
	if len(rows) == 0 {
		return 0, nil
	}
	result := tx.Clauses(clause.OnConflict{
		Columns:   columns,
		UpdateAll: true,
	}).Create(&rows)
	return result.RowsAffected, result.Error
}

func createUpsertSettings(tx *gorm.DB, rows []model.Setting) (int64, error) {
	rows = excludeTwoFactorSetting(rows)
    // 此处为事务内路径, 不调用会重入 privacyWriteMu 的公开设置接口.
    // 通用插入或更新前逐行校验.
    for _, row := range rows {
        if err := row.Validate(); err != nil {
            return 0, err
        }
    }
	if len(rows) == 0 {
		return 0, nil
	}
	result := tx.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "key"}},
		DoUpdates: clause.AssignmentColumns([]string{"value"}),
	}).Create(&rows)
	return result.RowsAffected, result.Error
}

// excludeTwoFactorSetting 把两步验证开关排除在备份之外。
// 密钥存在 users 表且不参与导出，开关跟着备份走的话，恢复到新环境会变成
// "已开启但没有密钥"——任何验证码都算不出来，直接把人锁死。
// 导出和导入两侧都过滤：导出侧防止旧备份带出开关，导入侧防止手工构造的备份覆盖它。
func excludeTwoFactorSetting(rows []model.Setting) []model.Setting {
	filtered := make([]model.Setting, 0, len(rows))
	for _, row := range rows {
		if row.Key == model.SettingKeyTwoFactorEnabled {
			continue
		}
		filtered = append(filtered, row)
	}
	return filtered
}
