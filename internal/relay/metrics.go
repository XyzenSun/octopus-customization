package relay

import (
	"context"
	"encoding/json"
	"time"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/bestruirui/octopus/internal/price"
	"github.com/bestruirui/octopus/internal/utils/log"
	"github.com/looplj/axonhub/llm"
)

// RelayMetrics 负责最终的日志收集与持久化
type RelayMetrics struct {
	APIKeyID     int
	RequestModel string
	StartTime    time.Time

	// 首 Token 时间
	FirstTokenTime time.Time

	// 请求和最终响应体；InternalResponse 保存实际写回客户端或流式聚合后的 body，不再强制转换成 llm.Response。
	InternalRequest  *llm.Request
	InternalResponse []byte

	// 统计指标
	ActualModel string
	Stats       model.StatsMetrics

	// 无论指定渠道直连还是分组路由，这里都记录最终实际应用到上游的合并参数覆盖。
    ParamOverride       string
    PrivacyTriggered    bool
    rawChannelUsage     map[int]model.StatsMetrics
    executedChannelID   int
    executedChannelName string
    executedActualModel string
}

func (m *RelayMetrics) RecordUsage(usage *llm.Usage) {
	if usage == nil {
		return
	}

    m.Stats = usageMetrics(m.ActualModel, usage)
}

func (m *RelayMetrics) RecordRawUsage(channelID int, usage *llm.Usage) float64 {
    if usage == nil {
        return 0
    }
    delta := usageMetrics(m.ActualModel, usage)
    if m.rawChannelUsage == nil {
        m.rawChannelUsage = make(map[int]model.StatsMetrics)
    }
    channelUsage := m.rawChannelUsage[channelID]
    channelUsage.Add(delta)
    m.rawChannelUsage[channelID] = channelUsage
    m.Stats.Add(delta)
    return delta.InputCost + delta.OutputCost
}

func usageMetrics(actualModel string, usage *llm.Usage) model.StatsMetrics {
    stats := model.StatsMetrics{InputToken: usage.PromptTokens, OutputToken: usage.CompletionTokens}
    modelPrice := price.GetLLMPrice(actualModel)
	if modelPrice == nil {
        return stats
	}
	tokenDetails := usage.PromptTokensDetails
	if tokenDetails == nil {
		tokenDetails = &llm.PromptTokensDetails{}
	}
	// 缓存读、缓存写和普通输入的单价不同；如果上游返回的缓存明细超过总输入 token，就退回按全部输入 token 计费，避免出现负成本。
	nonCachedTokens := usage.PromptTokens - tokenDetails.CachedTokens - tokenDetails.WriteCachedTokens
	if nonCachedTokens < 0 {
		nonCachedTokens = usage.PromptTokens
	}
    stats.InputCost = (float64(tokenDetails.CachedTokens)*modelPrice.CacheRead +
		float64(tokenDetails.WriteCachedTokens)*modelPrice.CacheWrite +
		float64(nonCachedTokens)*modelPrice.Input) * 1e-6
    stats.OutputCost = float64(usage.CompletionTokens) * modelPrice.Output * 1e-6
    return stats
}

func (m *RelayMetrics) Save(ctx context.Context, success bool, err error, attempts []model.ChannelAttempt) {
	duration := time.Since(m.StartTime)

	globalStats := model.StatsMetrics{
		WaitTime:    duration.Milliseconds(),
		InputToken:  m.Stats.InputToken,
		OutputToken: m.Stats.OutputToken,
		InputCost:   m.Stats.InputCost,
		OutputCost:  m.Stats.OutputCost,
	}
	if success {
		globalStats.RequestSuccess = 1
	} else {
		globalStats.RequestFailed = 1
	}

	channelID, channelName := finalChannel(attempts)
    if m.executedChannelID != 0 {
        channelID, channelName = m.executedChannelID, m.executedChannelName
        m.ActualModel = m.executedActualModel
    }
	op.StatsTotalUpdate(globalStats)
	op.StatsHourlyUpdate(globalStats)
	op.StatsDailyUpdate(context.Background(), globalStats)
	op.StatsAPIKeyUpdate(m.APIKeyID, globalStats)
    if len(m.rawChannelUsage) > 0 {
        for id, usage := range m.rawChannelUsage {
            op.StatsChannelUpdate(id, usage)
        }
    } else if channelID > 0 {
		// 通道成功/失败和等待时间在每次 attempt 结束时已记录；这里仅把最终响应的用量成本归到实际通道，避免重复计数。
		op.StatsChannelUpdate(channelID, model.StatsMetrics{
			InputToken:  m.Stats.InputToken,
			OutputToken: m.Stats.OutputToken,
			InputCost:   m.Stats.InputCost,
			OutputCost:  m.Stats.OutputCost,
		})
	}

	log.Infof("relay complete: model=%s, channel=%d(%s), success=%t, duration=%dms, input_token=%d, output_token=%d, input_cost=%f, output_cost=%f, total_cost=%f, attempts=%d",
		m.RequestModel, channelID, channelName, success, duration.Milliseconds(),
		m.Stats.InputToken, m.Stats.OutputToken,
		m.Stats.InputCost, m.Stats.OutputCost, m.Stats.InputCost+m.Stats.OutputCost,
		len(attempts))

	// 客户端断开或请求上下文取消后仍要保存最终审计日志，因此持久化阶段主动脱离请求取消信号。
	m.saveLog(context.WithoutCancel(ctx), err, duration, attempts, channelID, channelName)
}

func finalChannel(attempts []model.ChannelAttempt) (int, string) {
	var lastID int
	var lastName string
	for i := len(attempts) - 1; i >= 0; i-- {
		a := attempts[i]
		if a.Status == model.AttemptSuccess {
			return a.ChannelID, a.ChannelName
		}
		if a.Status == model.AttemptFailed && lastID == 0 {
			lastID = a.ChannelID
			lastName = a.ChannelName
		}
	}
	return lastID, lastName
}

func (m *RelayMetrics) saveLog(ctx context.Context, err error, duration time.Duration, attempts []model.ChannelAttempt, channelID int, channelName string) {
	relayLog := model.RelayLog{
		Time:             m.StartTime.Unix(),
		RequestModelName: m.RequestModel,
		ChannelName:      channelName,
		ChannelId:        channelID,
		ActualModelName:  m.ActualModel,
		UseTime:          int(duration.Milliseconds()),
		Attempts:         attempts,
		TotalAttempts:    len(attempts),
        PrivacyTriggered: m.PrivacyTriggered,
	}

	if apiKey, getErr := op.APIKeyGet(m.APIKeyID, ctx); getErr == nil {
		relayLog.RequestAPIKeyName = apiKey.Name
	}

	// 首字时间
	if !m.FirstTokenTime.IsZero() {
		relayLog.Ftut = int(m.FirstTokenTime.Sub(m.StartTime).Milliseconds())
	}

	// 用量
	if m.Stats.InputToken > 0 || m.Stats.OutputToken > 0 {
		relayLog.InputTokens = int(m.Stats.InputToken)
		relayLog.OutputTokens = int(m.Stats.OutputToken)
		relayLog.Cost = m.Stats.InputCost + m.Stats.OutputCost
	}

	maxContentSizeMB, settingErr := op.SettingGetInt(model.SettingKeyRelayLogMaxContentSizeMB)
	if settingErr != nil || maxContentSizeMB < -1 {
		maxContentSizeMB = model.DefaultRelayLogMaxContentSizeMB
	}
	responseContentSize := int64(len(m.InternalResponse))
	if relayLogContentExceedsLimit(responseContentSize, maxContentSizeMB) {
		log.Warnf("relay log skipped: response content size=%d bytes exceeds limit=%d MiB", responseContentSize, maxContentSizeMB)
		return
	}

	requestContent := m.requestContent()
	contentSize := int64(len(requestContent)) + responseContentSize
	if relayLogContentExceedsLimit(contentSize, maxContentSizeMB) {
		log.Warnf("relay log skipped: content size=%d bytes exceeds limit=%d MiB", contentSize, maxContentSizeMB)
		return
	}

	relayLog.RequestContent = string(requestContent)
	if len(m.InternalResponse) > 0 {
		relayLog.ResponseContent = string(m.InternalResponse)
	}
	if err != nil {
		relayLog.Error = err.Error()
	}

	if logErr := op.RelayLogAdd(ctx, relayLog); logErr != nil {
		log.Warnf("failed to save relay log: %v", logErr)
	}
}

func relayLogContentExceedsLimit(contentSize int64, maxContentSizeMB int) bool {
	if maxContentSizeMB == -1 {
		return false
	}
	const bytesPerMiB int64 = 1024 * 1024
	maxSize := int64(maxContentSizeMB)
	if maxSize > (1<<63-1)/bytesPerMiB {
		return false
	}
	return contentSize > maxSize*bytesPerMiB
}

func (m *RelayMetrics) requestContent() []byte {
	if m.InternalRequest == nil {
		return nil
	}

	reqJSON, err := json.Marshal(filterRequestForLog(m.InternalRequest))
	if err != nil {
		return nil
	}
	if m.ParamOverride == "" {
		return reqJSON
	}

	var reqMap map[string]any
	if err := json.Unmarshal(reqJSON, &reqMap); err != nil {
		return reqJSON
	}
	var override map[string]any
	if err := json.Unmarshal([]byte(m.ParamOverride), &override); err != nil {
		return reqJSON
	}

	// 日志里的请求体要反映本次实际发给上游的参数覆盖，但失败解析时保留原始可审计内容。
	// 复用上游请求的合并逻辑，确保 null 同样表示删除，而不是在日志里错误地显示为显式空值。
	applyParamOverride(reqMap, override)
	finalJSON, err := json.Marshal(reqMap)
	if err != nil {
		return reqJSON
	}
	return finalJSON
}

// filterRequestForLog 去掉 RawRequest 和图片二进制字段，避免 multipart 原始 body 或图片内容落库。
func filterRequestForLog(req *llm.Request) *llm.Request {
	if req == nil {
		return nil
	}
	filtered := *req
	filtered.RawRequest = nil
	if req.Image != nil {
		img := *req.Image
		if len(img.Images) > 0 {
			img.Images = nil
		}
		if len(img.Mask) > 0 {
			img.Mask = nil
		}
		filtered.Image = &img
	}
	return &filtered
}
