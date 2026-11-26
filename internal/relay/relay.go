package relay

import (
    "bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
    "net/http/httptrace"
	"slices"
	"strings"
    "sync/atomic"
	"time"

	"github.com/bestruirui/octopus/internal/helper"
	dbmodel "github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
    "github.com/bestruirui/octopus/internal/privacy"
	"github.com/bestruirui/octopus/internal/relay/balancer"
	"github.com/bestruirui/octopus/internal/server/resp"
	"github.com/bestruirui/octopus/internal/utils/log"
	"github.com/gin-contrib/sse"
	"github.com/gin-gonic/gin"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/pipeline"
	"github.com/looplj/axonhub/llm/pipeline/stream"
	"github.com/looplj/axonhub/llm/streams"
	"github.com/looplj/axonhub/llm/transformer"
)

// Handler 返回处理入站请求并转发到上游服务的 Gin handler。
func Handler(inboundType llm.APIFormat) gin.HandlerFunc {
	inAdapter := newInbound(inboundType)
	return func(c *gin.Context) {
		run, err := newRelayRun(c, inboundType, inAdapter)
		if err != nil {
			return
		}
		run.run()
	}
}

func newRelayRun(c *gin.Context, inboundType llm.APIFormat, inAdapter transformer.Inbound) (*relayRun, error) {
    var snapshot *privacy.Snapshot
    if !privacyExcludedFormat(inboundType) {
        snapshot = op.PrivacySnapshot()
    }
    internalRequest, err := parseRequest(c, inboundType, inAdapter, snapshot)
	if err != nil {
		return nil, err
    }
    if internalRequest.RequestType == llm.RequestTypeEmbedding || internalRequest.RequestType == llm.RequestTypeImage {
        snapshot = nil
	}
	passthroughEnabled := passthroughEnabledSnapshot()

	group, groupErr := op.GroupGetEnabledMap(internalRequest.Model, c.Request.Context())
	if groupErr != nil {
		if !strings.Contains(internalRequest.Model, "/") {
            writeRelayError(c, snapshot, nil, http.StatusNotFound, "model not found")
			return nil, groupErr
		}
        if err := validateDirectChannelModelAccess(c, snapshot); err != nil {
			return nil, err
		}
		channelName, modelName, _ := splitDirectChannelModel(internalRequest.Model)
        return newDirectChannelRelayRun(c, inboundType, inAdapter, internalRequest, channelName, modelName, passthroughEnabled, snapshot)
	}

    if err := validateSupportedModel(c, internalRequest.Model, snapshot); err != nil {
		return nil, err
	}

	apiKeyID := c.GetInt("api_key_id")
	iter := balancer.NewIterator(group, apiKeyID, internalRequest.Model)
	if iter.Len() == 0 {
		err := errors.New("no available channel")
        writeRelayError(c, snapshot, nil, http.StatusServiceUnavailable, err.Error())
		return nil, err
	}

	return &relayRun{
		c:               c,
		inboundType:     inboundType,
		inAdapter:       inAdapter,
		internalRequest: internalRequest,
		metrics: &RelayMetrics{
			APIKeyID:        apiKeyID,
			RequestModel:    internalRequest.Model,
			ActualModel:     internalRequest.Model,
			StartTime:       time.Now(),
			InternalRequest: internalRequest,
		},
		iter:               iter,
		group:              group,
		passthroughEnabled: passthroughEnabled,
        privacySnapshot:    snapshot,
	}, nil
}

func passthroughEnabledSnapshot() bool {
	enabled, err := op.SettingGetBool(dbmodel.SettingKeyPassthroughEnabled)
	return err == nil && enabled
}

func validateSupportedModel(c *gin.Context, requestModel string, snapshot *privacy.Snapshot) error {
	if supportedModels := c.GetString("supported_models"); supportedModels != "" {
		if !slices.Contains(strings.Split(supportedModels, ","), requestModel) {
			err := errors.New("model not supported")
            writeRelayError(c, snapshot, nil, http.StatusBadRequest, err.Error())
			return err
		}
	}
	return nil
}

func validateDirectChannelModelAccess(c *gin.Context, snapshots ...*privacy.Snapshot) error {
	if c.GetString("supported_models") == "" {
		return nil
	}
	err := errors.New("model not supported")
    writeRelayError(c, relaySnapshot(snapshots), nil, http.StatusBadRequest, err.Error())
	return err
}

var errClientDisconnected = errors.New("client disconnected")

func isClientDisconnected(ctx context.Context, err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, http.ErrAbortHandler) {
		return true
	}
	return err == nil && ctx != nil && errors.Is(ctx.Err(), context.Canceled)
}

func (r *relayRun) run() {
	ctx := r.c.Request.Context()
	var lastErr error

	for r.iter.Next() {
		select {
		case <-ctx.Done():
			log.Infof("request context canceled, stopping retry")
			r.metrics.Save(ctx, false, context.Canceled, r.iter.Attempts())
			return
		default:
		}

		attempt, err := r.prepareAttempt()
		if err != nil {
			lastErr = err
			continue
		}
		if attempt == nil {
			continue
		}

		written, err := attempt.run()
		if err == nil {
			r.metrics.Save(ctx, true, nil, r.iter.Attempts())
			return
		}
		if errors.Is(err, errClientDisconnected) {
			r.metrics.Save(ctx, false, context.Canceled, r.iter.Attempts())
			return
		}
        if isPrivacyFailure(err) {
            if !written {
                resp.Error(r.c, http.StatusBadGateway, privacyFailureMessage)
            }
            r.metrics.Save(ctx, false, err, r.iter.Attempts())
            if written {
                abortPrivacyStream(r.c)
            }
            return
        }
		if written {
			r.metrics.Save(ctx, false, err, r.iter.Attempts())
			return
		}
		lastErr = err
	}

	if lastErr == nil {
		if r.routeMode == routeModeDirectChannel {
			lastErr = errors.New("channel unavailable")
		} else {
			lastErr = errors.New("all channels failed")
		}
	}
    status := http.StatusBadGateway
	if r.routeMode == routeModeDirectChannel {
        status = directChannelFailureStatus(r.iter.Attempts())
    }
    if err := writeRelayError(r.c, r.privacySnapshot, r.metrics, status, lastErr.Error()); err != nil {
        lastErr = err
	}
	r.metrics.Save(ctx, false, lastErr, r.iter.Attempts())
}

func (r *relayRun) prepareAttempt() (*relayAttempt, error) {
	item := r.iter.Item()
	channel, err := op.ChannelGet(item.ChannelID, r.c.Request.Context())
	if err != nil {
		log.Warnf("failed to get channel %d: %v", item.ChannelID, err)
		r.iter.Skip(item.ChannelID, 0, fmt.Sprintf("channel_%d", item.ChannelID), fmt.Sprintf("channel not found: %v", err))
		return nil, err
	}
	if !channel.Enabled {
		r.iter.Skip(channel.ID, 0, channel.Name, "channel disabled")
		return nil, nil
	}

	usedKey := channel.GetChannelKey(balancer.IsKeyCircuitBreakerEnabled())
	if usedKey.ChannelKey == "" {
		r.iter.Skip(channel.ID, 0, channel.Name, "no available key")
		return nil, nil
	}

	baseURL := channel.GetBaseUrl()
	var httpClient *http.Client
	if r.routeMode == routeModeDirectChannel {
		if !validDirectChannelBaseURL(baseURL) {
			r.iter.Skip(channel.ID, usedKey.ID, channel.Name, "no valid base URL")
			return nil, nil
		}
		httpClient, err = helper.ChannelHttpClient(channel)
		if err != nil {
			r.iter.Skip(channel.ID, usedKey.ID, channel.Name, fmt.Sprintf("http client unavailable: %v", err))
			return nil, nil
		}
	} else if r.iter.SkipCircuitBreak(channel.ID, usedKey.ID, channel.Name) {
		return nil, nil
	}

	outAdapter, err := newOutbound(channel.Type, r.internalRequest, baseURL, usedKey.ChannelKey)
	if err != nil {
		r.iter.Skip(channel.ID, usedKey.ID, channel.Name, err.Error())
		return nil, nil
	}
	if r.routeMode == routeModeDirectChannel && r.iter.SkipCircuitBreak(channel.ID, usedKey.ID, channel.Name) {
		return nil, nil
	}

	// 每次尝试都把客户端模型改成本次候选的实际上游模型；重试时会被下一候选覆盖。
	r.internalRequest.Model = item.ModelName
	r.metrics.ActualModel = item.ModelName
	r.metrics.ParamOverride = ""
	if r.routeMode == routeModeDirectChannel {
		log.Infof("direct channel request model %s, forwarding to channel: %s model: %s",
			r.metrics.RequestModel, channel.Name, item.ModelName)
	} else {
		log.Infof("request model %s, mode: %d, forwarding to channel: %s model: %s (attempt %d/%d, sticky=%t)",
			r.metrics.RequestModel, r.group.Mode, channel.Name, item.ModelName,
			r.iter.Index()+1, r.iter.Len(), r.iter.IsSticky())
	}

	return &relayAttempt{
		relayRun:   r,
		outAdapter: outAdapter,
		channel:    channel,
		usedKey:    usedKey,
		httpClient: httpClient,
	}, nil
}

// run 统一管理一次通道尝试的完整生命周期。
func (ra *relayAttempt) run() (bool, error) {
	span := ra.iter.StartAttempt(ra.channel.ID, ra.usedKey.ID, ra.channel.Name)

	upstreamStatusCode, fwdErr := ra.forward()
    var cost float64
    if ra.rawUsage != nil && ra.upstreamExecuted {
        ra.metrics.executedChannelID = ra.channel.ID
        ra.metrics.executedChannelName = ra.channel.Name
        ra.metrics.executedActualModel = ra.metrics.ActualModel
        cost = ra.metrics.RecordRawUsage(ra.channel.ID, ra.rawUsage.Usage())
    }
    // 本地错误和取消保留已观测费用, 不覆盖 Key 的冷却状态.
    if (isPrivacyFailure(fwdErr) && upstreamStatusCode < http.StatusBadRequest) || errors.Is(fwdErr, errClientDisconnected) {
        if cost != 0 {
            _ = op.ChannelKeyAddCost(ra.usedKey.ID, cost)
        }
    }
    if isPrivacyFailure(fwdErr) && upstreamStatusCode < http.StatusBadRequest {
        span.End(dbmodel.AttemptSkipped, privacyFailureMessage)
        return ra.c.Writer.Written(), fwdErr
    }
	if fwdErr == nil && upstreamStatusCode == 0 {
		upstreamStatusCode = http.StatusOK
	}
	ra.usedKey.StatusCode = upstreamStatusCode
	ra.usedKey.LastUseTimeStamp = time.Now().Unix()

	if fwdErr == nil {
        if ra.rawUsage == nil {
		op.ChannelKeyUpdate(ra.usedKey, ra.metrics.Stats.InputCost+ra.metrics.Stats.OutputCost)
        } else {
            op.ChannelKeyUpdate(ra.usedKey, cost)
        }

		span.End(dbmodel.AttemptSuccess, "")
		op.StatsChannelUpdate(ra.channel.ID, dbmodel.StatsMetrics{
			WaitTime:       span.Duration().Milliseconds(),
			RequestSuccess: 1,
		})
		balancer.RecordSuccess(ra.channel.ID, ra.usedKey.ID, ra.internalRequest.Model)
		if ra.routeMode == routeModeGroup {
			balancer.SetSticky(ra.metrics.APIKeyID, ra.metrics.RequestModel, ra.channel.ID, ra.usedKey.ID)
		}
		return false, nil
	}
	if errors.Is(fwdErr, errClientDisconnected) {
        span.End(dbmodel.AttemptSkipped, errClientDisconnected.Error())
		return false, fwdErr
	}

    op.ChannelKeyUpdate(ra.usedKey, cost)
	span.End(dbmodel.AttemptFailed, fwdErr.Error())
	op.StatsChannelUpdate(ra.channel.ID, dbmodel.StatsMetrics{
		WaitTime:      span.Duration().Milliseconds(),
		RequestFailed: 1,
	})
	balancer.RecordFailure(ra.channel.ID, ra.usedKey.ID, ra.internalRequest.Model)

    if isPrivacyFailure(fwdErr) {
        return ra.c.Writer.Written(), fwdErr
    }
	return ra.c.Writer.Written(), fmt.Errorf("channel %s failed: %v", ra.channel.Name, fwdErr)
}

// parseRequest 解析并验证入站请求
func parseRequest(c *gin.Context, inboundType llm.APIFormat, inAdapter transformer.Inbound, snapshot *privacy.Snapshot) (*llm.Request, error) {
	if inAdapter == nil {
		err := fmt.Errorf("unsupported inbound type: %s", inboundType)
        writeRelayError(c, snapshot, nil, http.StatusBadRequest, err.Error())
		return nil, err
	}

	httpRequest, err := httpclient.ReadHTTPRequest(c.Request)
	if err != nil {
        writeRelayError(c, snapshot, nil, http.StatusInternalServerError, err.Error())
		return nil, err
	}

	internalRequest, err := inAdapter.TransformRequest(c.Request.Context(), httpRequest)
	if err != nil {
		statusCode := http.StatusInternalServerError
		if errors.Is(err, transformer.ErrInvalidRequest) {
			statusCode = http.StatusBadRequest
		}
        writeRelayError(c, snapshot, nil, statusCode, err.Error())
		return nil, err
	}
	if internalRequest.RawRequest == nil {
		internalRequest.RawRequest = httpRequest
	}

	return internalRequest, nil
}

// forward 转发请求到上游服务
func (ra *relayAttempt) forward() (int, error) {
	ctx := ra.c.Request.Context()
	if ra.internalRequest.RawRequest == nil {
		return 0, fmt.Errorf("missing raw request")
	}

	httpClient := ra.httpClient
	if httpClient == nil {
		var err error
		httpClient, err = helper.ChannelHttpClient(ra.channel)
		if err != nil {
			log.Warnf("failed to get http client: %v", err)
			return 0, err
		}
	}

	relayMiddleware := &relayPipelineMiddleware{attempt: ra}
    if ra.privacySnapshot.Active() {
        var sent atomic.Bool
        ctx = httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{
            WroteRequest: func(info httptrace.WroteRequestInfo) {
                if info.Err == nil {
                    sent.Store(true)
                }
            },
        })
        defer func() {
            if ra.privacyRequestChanged && (sent.Load() || ra.upstreamExecuted || relayMiddleware.upstreamStatusCode >= http.StatusBadRequest) {
                ra.metrics.PrivacyTriggered = true
            }
        }()
    }
	passthroughEnabled := ra.canPassthrough()
	middlewares := []pipeline.Middleware{relayMiddleware}
	if !passthroughEnabled {
		middlewares = append([]pipeline.Middleware{stream.EnsureUsage()}, middlewares...)
	}
	pipelineOptions := []pipeline.Option{
		pipeline.WithMiddlewares(middlewares...),
		pipeline.WithEmptyResponseDetection(),
	}
	if ra.routeMode == routeModeGroup && ra.internalRequest.Stream != nil && *ra.internalRequest.Stream && ra.group.FirstTokenTimeOut > 0 {
		pipelineOptions = append(pipelineOptions, pipeline.WithResponseTimeouts(time.Duration(ra.group.FirstTokenTimeOut)*time.Second, 0))
	}

	inbound := transformer.Inbound(&parsedRequestInbound{Inbound: ra.inAdapter, request: ra.internalRequest})
	outbound := ra.outAdapter
	if passthroughEnabled {
		passthrough, err := newPassthroughTransformer(ra)
		if err != nil {
			return 0, err
		}
		inbound = &passthroughInbound{transformer: passthrough, request: ra.internalRequest}
		outbound = passthrough
	}

	result, err := pipeline.NewFactory(httpclient.NewHttpClientWithClient(httpClient)).
		Pipeline(
			inbound,
			outbound,
			pipelineOptions...,
		).
		Process(ctx, ra.internalRequest.RawRequest)
	if err != nil {
		if errors.Is(err, pipeline.ErrStreamFirstEventTimeout) {
			log.Warnf("first token timeout (%ds), switching channel", ra.group.FirstTokenTimeOut)
			return relayMiddleware.upstreamStatusCode, err
		}
		if isClientDisconnected(ctx, err) {
			return relayMiddleware.upstreamStatusCode, errClientDisconnected
		}
        if isPrivacyFailure(err) {
            return relayMiddleware.upstreamStatusCode, err
        }
        if statusCode, written, privacyErr := ra.writeDirectChannelUpstreamError(ctx, inbound, err); privacyErr != nil {
            return statusCode, privacyErr
        } else if written {
			return statusCode, err
		}
		return relayMiddleware.upstreamStatusCode, err
	}
	if result == nil {
		return 0, fmt.Errorf("empty pipeline result")
	}
	if result.Stream {
		if err := ra.writeStream(ctx, result.EventStream); err != nil {
			return http.StatusOK, err
		}
		return http.StatusOK, nil
	}
	if result.Response == nil {
		return 0, fmt.Errorf("empty pipeline response")
	}
    body, changed, err := ra.restorePrivacyResponse(result.Response.Body)
    if err != nil {
        return result.Response.StatusCode, err
    }
	statusCode := result.Response.StatusCode
	if statusCode == 0 {
		statusCode = http.StatusOK
	}
	contentType := "application/json"
	if result.Response.Headers != nil {
		for key, values := range result.Response.Headers {
			ra.c.Writer.Header()[key] = append([]string(nil), values...)
		}
		if result.Response.Headers.Get("Content-Type") != "" {
			contentType = result.Response.Headers.Get("Content-Type")
		}
	}
    if changed {
        deleteContentLength(ra.c.Writer.Header())
    }
    written, writeErr := writeRelayBody(ra.c, statusCode, contentType, body)
    ra.metrics.InternalResponse = body[:written]
    ra.metrics.PrivacyTriggered = ra.metrics.PrivacyTriggered || changed && writeErr == nil
    if writeErr != nil {
        return statusCode, writeErr
    }
	return statusCode, nil
}

func (ra *relayAttempt) applyRequestOptions(outboundRequest *httpclient.Request) {
	// 参数覆盖只作用于 JSON 请求体；分组配置先合并，渠道配置再覆盖同名顶层参数。
	if strings.Contains(strings.ToLower(outboundRequest.Headers.Get("Content-Type")+" "+outboundRequest.ContentType), "application/json") {
		override, serializedOverride, ok := ra.mergedParamOverride()
		if ok {
			var bodyMap map[string]any
			if err := json.Unmarshal(outboundRequest.Body, &bodyMap); err != nil {
				log.Warnf("failed to unmarshal request body: %v, skipping param_override", err)
			} else {
				applyParamOverride(bodyMap, override)
				modifiedBody, err := json.Marshal(bodyMap)
				if err != nil {
					log.Warnf("failed to marshal modified body: %v, skipping param_override", err)
				} else {
					outboundRequest.Body = modifiedBody
					ra.metrics.ParamOverride = serializedOverride
				}
			}
		}
	}

	// 自定义 Header 在 pipeline 完成认证配置后应用，所以既能覆盖/删除普通 Header，
	// 也能按用户配置覆盖/删除 Authorization 等认证 Header。
	helper.ApplyCustomHeaders(outboundRequest.Headers, ra.mergedCustomHeaders())
}

// applyParamOverride 把 override 的顶层键合并进请求体。
// RFC 7396（JSON Merge Patch）：值为 JSON null 表示删除该参数，
// 其余值（含 0/false/""/[]/{}）都是正常的参数值，照常覆盖。
func applyParamOverride(bodyMap, override map[string]any) {
	for key, value := range override {
		if value == nil {
			delete(bodyMap, key)
			continue
		}
		bodyMap[key] = value
	}
}

func (ra *relayAttempt) mergedParamOverride() (map[string]any, string, bool) {
	merged := make(map[string]any)
	configured := false
	merge := func(scope string, raw *string) {
		if raw == nil || strings.TrimSpace(*raw) == "" {
			return
		}
		var override map[string]any
		if err := json.Unmarshal([]byte(*raw), &override); err != nil {
			log.Warnf("failed to unmarshal %s param_override: %v, skipping", scope, err)
			return
		}
		// 两级配置的叠加，null 当普通值参与覆盖而非在此删除：
		// group ： {"a":null}、channel ：{"a":"x"} 时结果是 "x"
		maps.Copy(merged, override)
		configured = true
	}

	if ra.routeMode == routeModeGroup {
		merge("group", ra.group.ParamOverride)
	}
	merge("channel", ra.channel.ParamOverride)
	if !configured {
		return nil, "", false
	}

	serialized, err := json.Marshal(merged)
	if err != nil {
		log.Warnf("failed to marshal merged param_override: %v, skipping", err)
		return nil, "", false
	}
	return merged, string(serialized), true
}

func (ra *relayAttempt) mergedCustomHeaders() []dbmodel.CustomHeader {
	headers := make([]dbmodel.CustomHeader, 0, len(ra.group.CustomHeader)+len(ra.channel.CustomHeader))
	indexByKey := make(map[string]int, cap(headers))
	merge := func(source []dbmodel.CustomHeader) {
		for _, header := range source {
			key := strings.TrimSpace(header.HeaderKey)
			if key == "" {
				continue
			}
			header.HeaderKey = key
			normalizedKey := strings.ToLower(key)
			if index, ok := indexByKey[normalizedKey]; ok {
				headers[index] = header
				continue
			}
			indexByKey[normalizedKey] = len(headers)
			headers = append(headers, header)
		}
	}

	if ra.routeMode == routeModeGroup {
		merge(ra.group.CustomHeader)
	}
	merge(ra.channel.CustomHeader)
	return headers
}

// writeStream 把 pipeline 输出的客户端格式流写回请求方。
// 首个有效上游响应片段的超时由 pipeline.WithResponseTimeouts 在 Process 阶段处理。
func (ra *relayAttempt) writeStream(ctx context.Context, clientStream streams.Stream[*httpclient.StreamEvent]) error {
	if clientStream == nil {
		return fmt.Errorf("empty pipeline stream")
	}

	// 设置 SSE 响应头
	ra.c.Header("Content-Type", "text/event-stream")
	ra.c.Header("Cache-Control", "no-cache")
	ra.c.Header("Connection", "keep-alive")
	ra.c.Header("X-Accel-Buffering", "no")

    var restorer *privacy.StreamRestorer
    if ra.privacySnapshot.Active() {
        restorer = privacy.NewStreamRestorer(ra.privacySnapshot, ra.inboundType)
        defer restorer.Cancel()
    }
	responseEvents := make([]*httpclient.StreamEvent, 0, 8)
    defer func() {
        if len(responseEvents) == 0 {
            return
        }
        body, meta, err := ra.inAdapter.AggregateStreamChunks(context.WithoutCancel(ctx), responseEvents)
        if err != nil {
            log.Warnf("failed to aggregate committed stream for log")
            return
        }
        ra.metrics.InternalResponse = body
        if ra.rawUsage == nil {
            ra.metrics.RecordUsage(meta.Usage)
        }
    }()
    commitEvents := func(commits []privacy.StreamCommit) error {
        for _, commit := range commits {
            if ctx.Err() != nil {
                return errClientDisconnected
            }
            event := commit.Event
            if event == nil || len(event.Data) == 0 && event.Type == "" && event.LastEventID == "" {
                continue
            }
            // SSE encoder 会忽略底层 writer 的部分错误. 先在内存编码, 再确认写入结果.
            var frame bytes.Buffer
            if err := sse.Encode(&frame, sse.Event{Event: event.Type, Id: event.LastEventID, Data: event.Data}); err != nil {
                return err
            }
            written, err := ra.c.Writer.Write(frame.Bytes())
            if err != nil || written != frame.Len() {
                return errClientDisconnected
            }
            if err := flushRelayResponse(ra.c); err != nil {
                return errClientDisconnected
            }
            if ra.metrics.FirstTokenTime.IsZero() {
                ra.metrics.FirstTokenTime = time.Now()
            }
            ra.metrics.PrivacyTriggered = ra.metrics.PrivacyTriggered || commit.Changed
            responseEvents = append(responseEvents, event)
        }
        return nil
    }
    finishPrivacy := func() error {
        if restorer == nil {
            return nil
        }
        commits, err := restorer.Finish()
        if writeErr := commitEvents(commits); writeErr != nil {
            return writeErr
        }
        if err != nil {
            reportPrivacyFailure(err)
            if len(commits) == 0 {
                return err
            }
        }
        return nil
    }
	type sseReadResult struct {
		event *httpclient.StreamEvent
		err   error
	}
	results := make(chan sseReadResult, 1)
	done := make(chan struct{})
    readerDone := make(chan struct{})
    defer func() {
        close(done)
        _ = clientStream.Close()
        <-readerDone
    }()
	go func() {
        defer close(readerDone)
		defer close(results)
		defer func() {
			if r := recover(); r != nil {
				log.Warnf("stream reader panic: %v", r)
				select {
				case results <- sseReadResult{err: fmt.Errorf("stream reader panic: %v", r)}:
				case <-done:
				case <-ctx.Done():
				}
			}
		}()
		// Next 可能阻塞等待上游 token；放到协程里让首 token 超时和客户端断开都能及时打断本次通道尝试。
		for clientStream.Next() {
			select {
			case results <- sseReadResult{event: clientStream.Current()}:
			case <-done:
				return
			case <-ctx.Done():
				return
			}
		}
		if err := clientStream.Err(); err != nil {
			select {
			case results <- sseReadResult{err: err}:
			case <-done:
			case <-ctx.Done():
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
            return errClientDisconnected
		case r, ok := <-results:
            if ctx.Err() != nil || isClientDisconnected(ctx, r.err) {
                return errClientDisconnected
            }
			if !ok {
                return finishPrivacy()
			}
			if r.err != nil {
                if err := finishPrivacy(); err != nil {
                    reportPrivacyFailure(err)
                }
				return fmt.Errorf("failed to read stream event: %w", r.err)
			}
            if r.event == nil {
				continue
			}
            upstreamFailure := isUpstreamStreamFailure(r.event)
            if restorer == nil {
                if len(r.event.Data) != 0 {
                    if err := commitEvents([]privacy.StreamCommit{{Event: r.event}}); err != nil {
                        return err
			}
                }
                if upstreamFailure {
                    return errUpstreamStreamFailure
                }
                continue
            }
            commits, err := restorer.Push(r.event)
            if writeErr := commitEvents(commits); writeErr != nil {
                return writeErr
            }
            if err != nil {
                reportPrivacyFailure(err)
            }
            if upstreamFailure {
                return errUpstreamStreamFailure
            }
            if err != nil && restorer.Closed() {
                return err
            }
		}
	}
}

// relayPipelineMiddleware 承接 octopus 自己的请求级副作用：
// 1. 在 pipeline 发出上游请求前应用分组与渠道的参数覆盖和自定义 header；
// 2. 在上游失败时保存 HTTP 状态码，供 key 冷却、熔断和后续选路使用；
// 3. 在协议转换前观测原始 usage, 其他请求沿用转换后的 usage.
// axonhub/llm 只提供了部分函数式 middleware 构造器，错误状态码和 llm 响应 usage 这两个回调没有公开构造器，
// 所以这里保留一个很薄的结构体实现完整接口，而不是在 relay 主流程里重复 pipeline 的执行逻辑。
type relayPipelineMiddleware struct {
	pipeline.DummyMiddleware
	attempt            *relayAttempt
	upstreamStatusCode int
}

func (m *relayPipelineMiddleware) Name() string {
	return "octopus_relay"
}

func (m *relayPipelineMiddleware) OnOutboundRawRequest(ctx context.Context, request *httpclient.Request) (*httpclient.Request, error) {
	if request.Headers == nil {
		request.Headers = make(http.Header)
	}
	m.attempt.applyRequestOptions(request)
    if !m.attempt.privacySnapshot.Active() {
        return request, nil
    }
    format := llm.APIFormat(request.APIFormat)
    m.attempt.rawUsage = newRawUsageCapture(format)
    body, changed, err := m.attempt.privacySnapshot.RewriteRequest(format, request.Body)
    if err != nil {
        reportPrivacyFailure(err)
        if m.attempt.privacySnapshot.Config().FailurePolicy == privacy.Abort {
            return nil, err
        }
        return request, nil
    }
    if changed {
        deleteContentLength(request.Headers)
        request.Body = body
        m.attempt.privacyRequestChanged = true
    }
	return request, nil
}

func (m *relayPipelineMiddleware) OnOutboundRawError(ctx context.Context, err error) {
	var upstreamErr *httpclient.Error
	if errors.As(err, &upstreamErr) {
		// pipeline 会把上游错误转换成统一错误返回；这里在转换前记录原始 HTTP 状态码，用于渠道 key 的后续调度决策。
		m.upstreamStatusCode = upstreamErr.StatusCode
        if m.attempt.rawUsage != nil {
            m.attempt.upstreamExecuted = true
            m.attempt.rawUsage.ObserveJSON(upstreamErr.Body)
        }
	}
}

func (m *relayPipelineMiddleware) OnOutboundRawResponse(_ context.Context, response *httpclient.Response) (*httpclient.Response, error) {
    if response != nil && m.attempt.rawUsage != nil {
        m.attempt.upstreamExecuted = true
        m.attempt.rawUsage.ObserveJSON(response.Body)
    }
    return response, nil
}

func (m *relayPipelineMiddleware) OnOutboundRawStream(_ context.Context, upstream streams.Stream[*httpclient.StreamEvent]) (streams.Stream[*httpclient.StreamEvent], error) {
    if m.attempt.rawUsage != nil {
        m.attempt.upstreamExecuted = true
        upstream = m.attempt.rawUsage.Wrap(upstream)
    }
    return upstream, nil
}

func (m *relayPipelineMiddleware) OnOutboundLlmResponse(ctx context.Context, response *llm.Response) (*llm.Response, error) {
    if response != nil && m.attempt.rawUsage == nil {
		// 非流式 usage 已由 outbound transformer 标准化到 llm.Response；流式 usage 在最终聚合时记录，避免重复计数。
		m.attempt.metrics.RecordUsage(response.Usage)
	}
	return response, nil
}

// parsedRequestInbound 让 pipeline 复用 relay 在选路前已经解析好的 llm.Request。
// 这样每次候选通道尝试只重新执行 outbound transform 和 HTTP 请求，不会重复读取或解析客户端 body。
type parsedRequestInbound struct {
	transformer.Inbound
	request *llm.Request
}

func (in *parsedRequestInbound) TransformRequest(ctx context.Context, request *httpclient.Request) (*llm.Request, error) {
	if in.request == nil {
		return nil, fmt.Errorf("missing parsed request")
	}
	// relay 已经为选路解析过请求；pipeline 入口复用该结果，避免每次通道尝试再次解析同一份 body。
	in.request.RawRequest = request
	return in.request, nil
}
