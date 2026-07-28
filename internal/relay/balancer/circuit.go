package balancer

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/bestruirui/octopus/internal/utils/log"
)

// CircuitState 熔断器状态
type CircuitState int

const (
	StateClosed   CircuitState = iota // 正常通行
	StateOpen                         // 熔断中，拒绝所有请求
	StateHalfOpen                     // 半开，仅允许单个试探请求
)

// circuitEntry 单个熔断器条目
type circuitEntry struct {
	State               CircuitState
	ConsecutiveFailures int64
	LastFailureTime     time.Time
	TripCount           int // 累计熔断触发次数（用于指数退避）
	mu                  sync.Mutex
}

// 全局熔断器存储
var globalBreaker sync.Map // key: string -> value: *circuitEntry

// circuitKey 生成熔断器键：channelID:channelKeyID:modelName
func circuitKey(channelID, keyID int, modelName string) string {
	return fmt.Sprintf("%d:%d:%s", channelID, keyID, modelName)
}

// getOrCreateEntry 获取或创建熔断器条目
func getOrCreateEntry(key string) *circuitEntry {
	if v, ok := globalBreaker.Load(key); ok {
		return v.(*circuitEntry)
	}
	entry := &circuitEntry{State: StateClosed}
	actual, _ := globalBreaker.LoadOrStore(key, entry)
	return actual.(*circuitEntry)
}

// IsCircuitBreakerEnabled 读取熔断器全局开关
// err 或未设置时默认返回 true（向后兼容）
func IsCircuitBreakerEnabled() bool {
	v, err := op.SettingGetBool(model.SettingKeyCircuitBreakerEnabled)
	if err != nil {
		return true
	}
	return v
}

// getThreshold 获取熔断阈值配置
func getThreshold() int64 {
	v, err := op.SettingGetInt(model.SettingKeyCircuitBreakerThreshold)
	if err != nil || v <= 0 {
		return 5
	}
	return int64(v)
}

// GetCooldown 获取当前冷却时间（带指数退避）
func GetCooldown(tripCount int) time.Duration {
	base, err := op.SettingGetInt(model.SettingKeyCircuitBreakerCooldown)
	if err != nil || base <= 0 {
		base = 60
	}
	maxCooldown, err := op.SettingGetInt(model.SettingKeyCircuitBreakerMaxCooldown)
	if err != nil || maxCooldown <= 0 {
		maxCooldown = 600
	}

	// 指数退避：baseCooldown * 2^(tripCount-1)
	cooldown := base
	if tripCount > 1 {
		shift := tripCount - 1
		if shift > 20 { // 防止溢出
			shift = 20
		}
		cooldown = base << shift
	}
	if cooldown > maxCooldown {
		cooldown = maxCooldown
	}

	return time.Duration(cooldown) * time.Second
}

// IsTripped 检查通道是否处于熔断状态
// 返回 tripped=true 表示该通道应被跳过，remaining 为剩余冷却时间
func IsTripped(channelID, keyID int, modelName string) (tripped bool, remaining time.Duration) {
	if !IsCircuitBreakerEnabled() {
		return false, 0
	}
	key := circuitKey(channelID, keyID, modelName)
	v, ok := globalBreaker.Load(key)
	if !ok {
		return false, 0 // 无记录，视为 Closed
	}
	entry := v.(*circuitEntry)

	entry.mu.Lock()
	defer entry.mu.Unlock()

	switch entry.State {
	case StateClosed:
		return false, 0

	case StateOpen:
		cooldown := GetCooldown(entry.TripCount)
		elapsed := time.Since(entry.LastFailureTime)
		if elapsed >= cooldown {
			entry.State = StateHalfOpen
			log.Infof("circuit breaker [%s] Open -> HalfOpen (cooldown %v elapsed)", key, cooldown)
			return false, 0
		}
		// 仍在冷却中
		return true, cooldown - elapsed

	case StateHalfOpen:
		// 已有试探请求在进行中，拒绝其他请求
		return true, 0

	default:
		return false, 0
	}
}

// RecordSuccess 记录成功，重置熔断器状态
func RecordSuccess(channelID, keyID int, modelName string) {
	if !IsCircuitBreakerEnabled() {
		return
	}
	key := circuitKey(channelID, keyID, modelName)
	v, ok := globalBreaker.Load(key)
	if !ok {
		return
	}
	entry := v.(*circuitEntry)

	entry.mu.Lock()
	defer entry.mu.Unlock()

	if entry.State == StateHalfOpen {
		log.Infof("circuit breaker [%s] HalfOpen -> Closed (probe succeeded)", key)
	}

	// 重置全部状态
	entry.State = StateClosed
	entry.ConsecutiveFailures = 0
	entry.TripCount = 0
}

// RecordFailure 记录失败，可能触发熔断
func RecordFailure(channelID, keyID int, modelName string) {
	if !IsCircuitBreakerEnabled() {
		return
	}
	key := circuitKey(channelID, keyID, modelName)
	entry := getOrCreateEntry(key)

	entry.mu.Lock()
	defer entry.mu.Unlock()

	entry.LastFailureTime = time.Now()

	switch entry.State {
	case StateClosed:
		entry.ConsecutiveFailures++
		threshold := getThreshold()
		if entry.ConsecutiveFailures >= threshold {
			entry.State = StateOpen
			entry.TripCount++
			log.Warnf("circuit breaker [%s] Closed -> Open (failures=%d >= threshold=%d, tripCount=%d, cooldown=%v)",
				key, entry.ConsecutiveFailures, threshold, entry.TripCount, GetCooldown(entry.TripCount))
		}

	case StateHalfOpen:
		// 试探失败，重新进入 Open 状态，TripCount 递增（冷却时间翻倍）
		entry.State = StateOpen
		entry.TripCount++
		entry.ConsecutiveFailures = 0 // 重新开始计数
		log.Warnf("circuit breaker [%s] HalfOpen -> Open (probe failed, tripCount=%d, cooldown=%v)",
			key, entry.TripCount, GetCooldown(entry.TripCount))

	case StateOpen:
		// 理论上不应该在 Open 状态下接收到失败记录（请求应被拒绝），
		// 但为安全起见仍更新失败时间
	}
}

// CircuitBreakerStatus 熔断器状态导出结构（供 API 查询）
type CircuitBreakerStatus struct {
	ChannelName       string `json:"channel_name"`
	State             string `json:"state"`              // "open" / "half_open"
	RemainingCooldown int64  `json:"remaining_cooldown"` // 秒
}

// ListTripped 遍历 globalBreaker，仅返回 State != StateClosed 的条目
// 对 Open 计算剩余冷却时间（HalfOpen 返回 0）
func ListTripped() []CircuitBreakerStatus {
	result := make([]CircuitBreakerStatus, 0)
	globalBreaker.Range(func(k, v any) bool {
		key, _ := k.(string)
		entry, ok := v.(*circuitEntry)
		if !ok {
			return true
		}

		entry.mu.Lock()

		if entry.State == StateClosed {
			entry.mu.Unlock()
			return true
		}

		// key 格式: "channelID:keyID:modelName"，modelName 可能包含 ":"
		parts := strings.SplitN(key, ":", 3)
		if len(parts) != 3 {
			entry.mu.Unlock()
			return true
		}
		channelID, err := strconv.Atoi(parts[0])
		if err != nil {
			entry.mu.Unlock()
			return true
		}

		status := CircuitBreakerStatus{}
		// 查 channel name（缓存命中失败时回退为 "Channel#<id>"）
		if ch, err := op.ChannelGet(channelID, context.Background()); err == nil {
			status.ChannelName = ch.Name
		} else {
			status.ChannelName = fmt.Sprintf("Channel#%d", channelID)
		}

		switch entry.State {
		case StateOpen:
			cooldown := GetCooldown(entry.TripCount)
			remaining := cooldown - time.Since(entry.LastFailureTime)
			if remaining <= 0 {
				// 冷却已过：与 IsTripped 保持一致，转为 HalfOpen 并跳过展示
				// （下次 IsTripped 调用会真正完成 Open -> HalfOpen 转换）
				entry.State = StateHalfOpen
				entry.mu.Unlock()
				return true
			}
			status.State = "open"
			status.RemainingCooldown = int64(remaining / time.Second)
		case StateHalfOpen:
			// 半开状态：已允许试探请求通过，不再展示为"熔断中"
			entry.mu.Unlock()
			return true
		}

		entry.mu.Unlock()
		result = append(result, status)
		return true
	})
	return result
}

// ResetAll 清空所有熔断器状态
func ResetAll() {
	globalBreaker.Range(func(k, _ any) bool {
		globalBreaker.Delete(k)
		return true
	})
}
