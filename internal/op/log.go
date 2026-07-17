package op

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/utils/log"
	"github.com/bestruirui/octopus/internal/utils/snowflake"
)

var relayLogCache = make([]model.RelayLog, 0, model.DefaultRelayLogFlushSize)
var relayLogCacheLock sync.Mutex

var relayLogFlushLock sync.Mutex

var relayLogSubscribers = make(map[chan model.RelayLog]struct{})
var relayLogSubscribersLock sync.RWMutex

var relayLogStreamTokens = make(map[string]struct{})
var relayLogStreamTokensLock sync.RWMutex

func RelayLogStreamTokenCreate() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	token := hex.EncodeToString(bytes)

	relayLogStreamTokensLock.Lock()
	relayLogStreamTokens[token] = struct{}{}
	relayLogStreamTokensLock.Unlock()

	return token, nil
}

func RelayLogStreamTokenVerify(token string) bool {
	relayLogStreamTokensLock.RLock()
	_, ok := relayLogStreamTokens[token]
	relayLogStreamTokensLock.RUnlock()
	return ok
}

func RelayLogStreamTokenRevoke(token string) {
	relayLogStreamTokensLock.Lock()
	delete(relayLogStreamTokens, token)
	relayLogStreamTokensLock.Unlock()
}

func RelayLogSubscribe() chan model.RelayLog {
	ch := make(chan model.RelayLog, 10)
	relayLogSubscribersLock.Lock()
	relayLogSubscribers[ch] = struct{}{}
	relayLogSubscribersLock.Unlock()
	return ch
}

func RelayLogUnsubscribe(ch chan model.RelayLog) {
	relayLogSubscribersLock.Lock()
	delete(relayLogSubscribers, ch)
	relayLogSubscribersLock.Unlock()
	close(ch)
}

func notifySubscribers(relayLog model.RelayLog) {
	relayLogSubscribersLock.RLock()
	defer relayLogSubscribersLock.RUnlock()

	for ch := range relayLogSubscribers {
		select {
		case ch <- relayLog:
		default:
		}
	}
}

func relayLogFlushToDB(ctx context.Context, capacity int) error {
	relayLogFlushLock.Lock()
	defer relayLogFlushLock.Unlock()

	relayLogCacheLock.Lock()
	if len(relayLogCache) == 0 {
		relayLogCacheLock.Unlock()
		return nil
	}
	batch := make([]model.RelayLog, len(relayLogCache))
	copy(batch, relayLogCache)
	flushedUpto := len(batch)
	relayLogCacheLock.Unlock()

	result := db.GetDB().WithContext(ctx).Create(&batch)
	if result.Error != nil {
		return result.Error
	}

	relayLogCacheLock.Lock()
	if len(relayLogCache) >= flushedUpto {
		// 重建底层数组而不是 reslice，避免数组持续引用已 flush 日志的 Request/ResponseContent 导致内存无法回收
		remainingCount := len(relayLogCache) - flushedUpto
		if remainingCount > 0 {
			newCache := make([]model.RelayLog, remainingCount, capacity)
			copy(newCache, relayLogCache[flushedUpto:])
			relayLogCache = newCache
		} else {
			relayLogCache = make([]model.RelayLog, 0, capacity)
		}
	} else {
		relayLogCache = make([]model.RelayLog, 0, capacity)
	}
	relayLogCacheLock.Unlock()

	return nil
}

func RelayLogAdd(ctx context.Context, relayLog model.RelayLog) error {
	enabled, err := SettingGetBool(model.SettingKeyRelayLogKeepEnabled)
	if err != nil {
		return err
	}

	// 获取配置的刷写/缓存上限
	var maxSize int
	if enabled {
		maxSize, err = SettingGetInt(model.SettingKeyRelayLogFlushSize)
		if err != nil || maxSize < 0 {
			maxSize = model.DefaultRelayLogFlushSize
		}
		// maxSize = 0 表示实时写入数据库
	} else {
		maxSize, err = SettingGetInt(model.SettingKeyRelayLogMemoryCacheSize)
		if err != nil || maxSize < 0 {
			maxSize = model.DefaultRelayLogMemoryCacheSize
		}
		// maxSize = 0 表示不记录任何日志
		if maxSize == 0 {
			return nil
		}
	}

	relayLog.ID = snowflake.GenerateID()
	go notifySubscribers(relayLog)

	relayLogCacheLock.Lock()
	relayLogCache = append(relayLogCache, relayLog)

	if enabled {
		// 数据库模式：maxSize = 0 表示实时写入，> 0 表示达到阈值后批量写入
		if maxSize == 0 || len(relayLogCache) >= maxSize {
			relayLogCacheLock.Unlock()
			return relayLogFlushToDB(ctx, maxSize)
		}
	} else {
		// 仅内存模式：达到上限后保留最新的一半
		if len(relayLogCache) >= maxSize {
			// 重建底层数组而不是 reslice，避免数组持续引用旧日志的 Request/ResponseContent 导致内存无法回收
			keepSize := maxSize / 2
			if len(relayLogCache) > keepSize {
				newCache := make([]model.RelayLog, keepSize, maxSize)
				copy(newCache, relayLogCache[len(relayLogCache)-keepSize:])
				relayLogCache = newCache
			}
		}
	}
	relayLogCacheLock.Unlock()
	return nil
}

func RelayLogSaveDBTask(ctx context.Context) error {
	log.Debugf("relay log save db task started")
	startTime := time.Now()
	defer func() {
		log.Debugf("relay log save db task finished, save time: %s", time.Since(startTime))
	}()
	enabled, err := SettingGetBool(model.SettingKeyRelayLogKeepEnabled)
	if err != nil {
		return err
	}

	if enabled {
		maxSize, err := SettingGetInt(model.SettingKeyRelayLogFlushSize)
		if err != nil || maxSize < 0 {
			maxSize = model.DefaultRelayLogFlushSize
		}
		if err := relayLogFlushToDB(ctx, maxSize); err != nil {
			return err
		}
		return relayLogCleanup(ctx)
	}

	// 如果未启用日志保存，检查缓存大小，如果超过限制则清理旧日志
	relayLogCacheLock.Lock()
	maxSizeNoDB, err := SettingGetInt(model.SettingKeyRelayLogMemoryCacheSize)
	if err != nil || maxSizeNoDB < 0 {
		maxSizeNoDB = model.DefaultRelayLogMemoryCacheSize
	}
	// maxSizeNoDB = 0 表示不记录日志，清空缓存
	if maxSizeNoDB == 0 {
		relayLogCache = make([]model.RelayLog, 0, model.DefaultRelayLogFlushSize)
	} else if len(relayLogCache) > maxSizeNoDB {
		keepSize := maxSizeNoDB / 2
		newCache := make([]model.RelayLog, keepSize, maxSizeNoDB)
		copy(newCache, relayLogCache[len(relayLogCache)-keepSize:])
		relayLogCache = newCache
	}
	relayLogCacheLock.Unlock()

	return nil
}

func relayLogCleanup(ctx context.Context) error {
	keepPeriod, err := SettingGetInt(model.SettingKeyRelayLogKeepPeriod)
	if err != nil {
		return err
	}

	if keepPeriod <= 0 {
		return nil
	}

	cutoffTime := time.Now().Add(-time.Duration(keepPeriod) * 24 * time.Hour).Unix()
	return db.GetDB().WithContext(ctx).Where("time < ?", cutoffTime).Delete(&model.RelayLog{}).Error
}

// RelayLogList 查询日志列表，支持可选的时间范围过滤
// startTime 和 endTime 为 nil 时表示不限制时间范围
func RelayLogList(ctx context.Context, startTime, endTime *int, page, pageSize int) ([]model.RelayLog, error) {
	enabled, err := SettingGetBool(model.SettingKeyRelayLogKeepEnabled)
	if err != nil {
		return nil, err
	}
	hasTimeFilter := startTime != nil && endTime != nil

	// 获取缓存中符合条件的日志
	relayLogCacheLock.Lock()
	var cachedLogs []model.RelayLog
	for _, log := range relayLogCache {
		if hasTimeFilter {
			if log.Time >= int64(*startTime) && log.Time <= int64(*endTime) {
				cachedLogs = append(cachedLogs, log)
			}
		} else {
			cachedLogs = append(cachedLogs, log)
		}
	}
	relayLogCacheLock.Unlock()

	// 反转缓存日志顺序（原本新的在末尾，反转后新的在前面，方便分页）
	for i, j := 0, len(cachedLogs)-1; i < j; i, j = i+1, j-1 {
		cachedLogs[i], cachedLogs[j] = cachedLogs[j], cachedLogs[i]
	}

	cacheCount := len(cachedLogs)
	offset := (page - 1) * pageSize

	var result []model.RelayLog

	// 先从缓存中取（缓存是最新的日志）
	if offset < cacheCount {
		cacheEnd := offset + pageSize
		if cacheEnd > cacheCount {
			cacheEnd = cacheCount
		}
		result = append(result, cachedLogs[offset:cacheEnd]...)
	}

	// 如果启用了日志保存，缓存不够时从数据库补充
	if enabled {
		remaining := pageSize - len(result)
		if remaining > 0 {
			dbOffset := 0
			if offset > cacheCount {
				dbOffset = offset - cacheCount
			}

			query := db.GetDB().WithContext(ctx)
			if hasTimeFilter {
				query = query.Where("time >= ? AND time <= ?", *startTime, *endTime)
			}

			var dbLogs []model.RelayLog
			if err := query.Order("id DESC").Offset(dbOffset).Limit(remaining).Find(&dbLogs).Error; err != nil {
				return nil, err
			}
			result = append(result, dbLogs...)
		}
	}

	return result, nil
}

func RelayLogClear(ctx context.Context) error {
	relayLogCacheLock.Lock()
	relayLogCache = make([]model.RelayLog, 0, model.DefaultRelayLogFlushSize)
	relayLogCacheLock.Unlock()
	return db.GetDB().WithContext(ctx).Where("1 = 1").Delete(&model.RelayLog{}).Error
}
