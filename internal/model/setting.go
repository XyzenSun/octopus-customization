package model

import (
	"fmt"
	"net/url"
	"strconv"

    "github.com/bestruirui/octopus/internal/privacy"
)

type SettingKey string

const (
	SettingKeyProxyURL                           SettingKey = "proxy_url"
	SettingKeyStatsSaveInterval                  SettingKey = "stats_save_interval"                      // 将统计信息写入数据库的周期(分钟)
	SettingKeyModelInfoUpdateInterval            SettingKey = "model_info_update_interval"               // 模型信息更新间隔(小时)
	SettingKeySyncLLMInterval                    SettingKey = "sync_llm_interval"                        // LLM 同步间隔(小时)
	SettingKeyRelayLogKeepPeriod                 SettingKey = "relay_log_keep_period"                    // 日志保存时间范围(天)
	SettingKeyRelayLogKeepEnabled                SettingKey = "relay_log_keep_enabled"                   // 是否保留历史日志
	SettingKeyRelayLogFlushSize                  SettingKey = "relay_log_flush_size"                     // 启用日志保存时的刷写上限
	SettingKeyRelayLogMemoryCacheSize            SettingKey = "relay_log_memory_cache_size"              // 仅内存时的缓存上限
	SettingKeyRelayLogMaxContentSizeMB           SettingKey = "relay_log_max_content_size_mb"            // 单条日志请求与响应正文合计上限（MiB）
	SettingKeyRelayLogMemoryLogMaxDimidiateTimes SettingKey = "relay_log_memory_log_max_dimidiate_times" // 折半 GC 触发次数，-1 关闭
	SettingKeyCORSAllowOrigins                   SettingKey = "cors_allow_origins"                       // 跨域白名单(逗号分隔, 如 "example.com,example2.com"). 为空不允许跨域, "*"允许所有
	SettingKeyCircuitBreakerThreshold            SettingKey = "circuit_breaker_threshold"                // 渠道级熔断触发阈值（连续失败次数）
	SettingKeyCircuitBreakerCooldown             SettingKey = "circuit_breaker_cooldown"                 // 渠道级熔断基础冷却时间（秒）
	SettingKeyCircuitBreakerMaxCooldown          SettingKey = "circuit_breaker_max_cooldown"             // 渠道级熔断最大冷却时间（秒），指数退避上限
	SettingKeyCircuitBreakerEnabled              SettingKey = "circuit_breaker_enabled"                  // 渠道级熔断器开关（按 渠道+Key+模型 统计连续失败）
	SettingKeyKeyCircuitBreakerEnabled           SettingKey = "key_circuit_breaker_enabled"              // Key 级熔断器开关（上游返回 401/403/429/503 时冷却该 Key）
	SettingKeyPassthroughEnabled                 SettingKey = "passthrough_enabled"                      // 协议直通全局开关
	SettingKeyTwoFactorEnabled                   SettingKey = "two_factor_enabled"                       // 两步验证(TOTP)开关，密钥另存于 users 表
    SettingKeyPrivacyEnabled                    SettingKey = "privacy_enabled"                          // 数据脱敏全局开关
    SettingKeyPrivacyScopes                     SettingKey = "privacy_scopes"                           // 数据脱敏范围位掩码
    SettingKeyPrivacyFailurePolicy              SettingKey = "privacy_failure_policy"                   // 数据脱敏失败策略
)

// 默认值常量
const (
	DefaultRelayLogFlushSize                  = 50  // 默认刷写上限
	DefaultRelayLogMemoryCacheSize            = 100 // 默认内存缓存上限
	DefaultRelayLogMaxContentSizeMB           = 2   // 默认单条日志正文上限（MiB）
	DefaultRelayLogMemoryLogMaxDimidiateTimes = 15  // 默认折半 GC 触发次数
)

const (
    DefaultPrivacyEnabled       = "false"
    DefaultPrivacyScopes        = "1"
    DefaultPrivacyFailurePolicy = "continue"
)

type Setting struct {
	Key   SettingKey `json:"key" gorm:"primaryKey"`
	Value string     `json:"value" gorm:"not null"`
}

// IsPrivacySettingKey 供通用设置写入口分流, 隐私设置必须随快照事务发布.
func IsPrivacySettingKey(key SettingKey) bool {
    switch key {
    case SettingKeyPrivacyEnabled, SettingKeyPrivacyScopes, SettingKeyPrivacyFailurePolicy:
        return true
    default:
        return false
    }
}

// ParsePrivacyConfig 从完整的三个字符串设置构造中性配置, 不补缺失默认值.
// 所有失败只返回固定错误, 不包含设置原值.
func ParsePrivacyConfig(enabled, scopes, failurePolicy string) (privacy.Config, error) {
    if enabled != "true" && enabled != "false" {
        return privacy.Config{}, privacy.ErrInvalidConfig
    }
    if scopes == "" {
        return privacy.Config{}, privacy.ErrInvalidConfig
    }
    for index := 0; index < len(scopes); index++ {
        if scopes[index] < '0' || scopes[index] > '9' {
            return privacy.Config{}, privacy.ErrInvalidConfig
        }
    }
    scopeValue, err := strconv.Atoi(scopes)
    if err != nil || scopeValue < 0 || scopeValue > int(privacy.AllScopes) {
        return privacy.Config{}, privacy.ErrInvalidConfig
    }
    config := privacy.Config{
        Enabled:       enabled == "true",
        Scopes:        privacy.Scope(scopeValue),
        FailurePolicy: privacy.FailurePolicy(failurePolicy),
    }
    if err := privacy.ValidateConfig(config); err != nil {
        return privacy.Config{}, privacy.ErrInvalidConfig
    }
    return config, nil
}

func DefaultSettings() []Setting {
	return []Setting{
		{Key: SettingKeyProxyURL, Value: ""},
		{Key: SettingKeyStatsSaveInterval, Value: "10"},                  // 默认10分钟保存一次统计信息
		{Key: SettingKeyCORSAllowOrigins, Value: "*"},                    // CORS 默认允许所有来源跨域
		{Key: SettingKeyModelInfoUpdateInterval, Value: "24"},            // 默认24小时更新一次模型信息
		{Key: SettingKeySyncLLMInterval, Value: "24"},                    // 默认24小时同步一次LLM
		{Key: SettingKeyRelayLogKeepPeriod, Value: "7"},                  // 默认日志保存7天
		{Key: SettingKeyRelayLogKeepEnabled, Value: "true"},              // 默认保留历史日志
		{Key: SettingKeyRelayLogFlushSize, Value: "50"},                  // 默认刷写上限50条
		{Key: SettingKeyRelayLogMemoryCacheSize, Value: "100"},           // 默认内存缓存100条
		{Key: SettingKeyRelayLogMaxContentSizeMB, Value: "2"},            // 默认单条日志正文上限2MiB
		{Key: SettingKeyRelayLogMemoryLogMaxDimidiateTimes, Value: "15"}, // 默认折半 GC 触发次数
		{Key: SettingKeyCircuitBreakerThreshold, Value: "5"},             // 默认连续失败5次触发熔断
		{Key: SettingKeyCircuitBreakerCooldown, Value: "60"},             // 默认基础冷却60秒
		{Key: SettingKeyCircuitBreakerMaxCooldown, Value: "600"},         // 默认最大冷却600秒（10分钟）
		{Key: SettingKeyCircuitBreakerEnabled, Value: "true"},            // 默认启用渠道级熔断器
		{Key: SettingKeyKeyCircuitBreakerEnabled, Value: "true"},         // 默认启用 Key 级熔断器（与历史行为一致）
		{Key: SettingKeyPassthroughEnabled, Value: "false"},              // Beta 功能默认关闭协议直通
		{Key: SettingKeyTwoFactorEnabled, Value: "false"},                // 两步验证默认关闭
        {Key: SettingKeyPrivacyEnabled, Value: DefaultPrivacyEnabled},
        {Key: SettingKeyPrivacyScopes, Value: DefaultPrivacyScopes},
        {Key: SettingKeyPrivacyFailurePolicy, Value: DefaultPrivacyFailurePolicy},
	}
}

func (s *Setting) Validate() error {
	switch s.Key {
    case SettingKeyPrivacyEnabled, SettingKeyPrivacyScopes, SettingKeyPrivacyFailurePolicy:
        enabled, scopes, failurePolicy := DefaultPrivacyEnabled, DefaultPrivacyScopes, DefaultPrivacyFailurePolicy
        switch s.Key {
        case SettingKeyPrivacyEnabled:
            enabled = s.Value
        case SettingKeyPrivacyScopes:
            scopes = s.Value
        case SettingKeyPrivacyFailurePolicy:
            failurePolicy = s.Value
        }
        _, err := ParsePrivacyConfig(enabled, scopes, failurePolicy)
        return err
	case SettingKeyRelayLogMaxContentSizeMB:
		value, err := strconv.Atoi(s.Value)
		if err != nil {
			return fmt.Errorf("relay log max content size must be an integer")
		}
		if value < -1 {
			return fmt.Errorf("relay log max content size must be -1 or greater")
		}
		return nil
	case SettingKeyRelayLogMemoryLogMaxDimidiateTimes:
		value, err := strconv.Atoi(s.Value)
		if err != nil {
			return fmt.Errorf("relay log memory log max dimidiate times must be an integer")
		}
		if value != -1 && value <= 0 {
			return fmt.Errorf("relay log memory log max dimidiate times must be -1 or a positive integer")
		}
		return nil
	case SettingKeyModelInfoUpdateInterval, SettingKeySyncLLMInterval, SettingKeyRelayLogKeepPeriod,
		SettingKeyCircuitBreakerThreshold, SettingKeyCircuitBreakerCooldown, SettingKeyCircuitBreakerMaxCooldown,
		SettingKeyRelayLogFlushSize, SettingKeyRelayLogMemoryCacheSize:
		_, err := strconv.Atoi(s.Value)
		if err != nil {
			return fmt.Errorf("model info update interval must be an integer")
		}
		return nil
	case SettingKeyRelayLogKeepEnabled:
		if s.Value != "true" && s.Value != "false" {
			return fmt.Errorf("relay log keep enabled must be true or false")
		}
		return nil
	case SettingKeyCircuitBreakerEnabled, SettingKeyKeyCircuitBreakerEnabled:
		if s.Value != "true" && s.Value != "false" {
			return fmt.Errorf("circuit breaker enabled must be true or false")
		}
		return nil
	case SettingKeyPassthroughEnabled:
		if s.Value != "true" && s.Value != "false" {
			return fmt.Errorf("passthrough enabled must be true or false")
		}
		return nil
	case SettingKeyTwoFactorEnabled:
		if s.Value != "true" && s.Value != "false" {
			return fmt.Errorf("two factor enabled must be true or false")
		}
		return nil
	case SettingKeyProxyURL:
		if s.Value == "" {
			return nil
		}
		parsedURL, err := url.Parse(s.Value)
		if err != nil {
			return fmt.Errorf("proxy URL is invalid: %w", err)
		}
		validSchemes := map[string]bool{
			"http":   true,
			"https":  true,
			"socks5": true,
		}
		if !validSchemes[parsedURL.Scheme] {
			return fmt.Errorf("proxy URL scheme must be http, https, socks, or socks5")
		}
		if parsedURL.Host == "" {
			return fmt.Errorf("proxy URL must have a host")
		}
		return nil
	}

	return nil
}
