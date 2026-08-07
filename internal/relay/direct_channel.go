package relay

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	dbmodel "github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/bestruirui/octopus/internal/relay/balancer"
	"github.com/bestruirui/octopus/internal/server/resp"
	"github.com/bestruirui/octopus/internal/utils/xstrings"
	"github.com/gin-gonic/gin"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/pipeline"
	"github.com/looplj/axonhub/llm/transformer"
)

func splitDirectChannelModel(requestModel string) (channelName, modelName string, ok bool) {
	return strings.Cut(requestModel, "/")
}

func newDirectChannelRelayRun(
	c *gin.Context,
	inboundType llm.APIFormat,
	inAdapter transformer.Inbound,
	internalRequest *llm.Request,
	channelName string,
	modelName string,
	passthroughEnabled bool,
) (*relayRun, error) {
	channel, err := op.ChannelGetByName(channelName, c.Request.Context())
	if err != nil {
		resp.Error(c, http.StatusNotFound, "channel not found")
		return nil, err
	}
	if !channel.Enabled {
		err := errors.New("no available channel")
		resp.Error(c, http.StatusServiceUnavailable, err.Error())
		return nil, err
	}
	if !channelSupportsModel(channel.Model, channel.CustomModel, modelName) {
		err := errors.New("model not found")
		resp.Error(c, http.StatusNotFound, err.Error())
		return nil, err
	}

	apiKeyID := c.GetInt("api_key_id")
	return &relayRun{
		c:               c,
		inboundType:     inboundType,
		inAdapter:       inAdapter,
		internalRequest: internalRequest,
		metrics: &RelayMetrics{
			APIKeyID:        apiKeyID,
			RequestModel:    internalRequest.Model,
			ActualModel:     modelName,
			StartTime:       time.Now(),
			InternalRequest: internalRequest,
		},
		iter:               balancer.NewSingleIterator(channel.ID, modelName),
		routeMode:          routeModeDirectChannel,
		passthroughEnabled: passthroughEnabled,
	}, nil
}

func validDirectChannelBaseURL(baseURL string) bool {
	if strings.TrimSpace(baseURL) == "" {
		return false
	}
	parsedURL, err := url.Parse(baseURL)
	if err != nil {
		return false
	}
	return (parsedURL.Scheme == "http" || parsedURL.Scheme == "https") && parsedURL.Host != ""
}

func channelSupportsModel(modelNames, customModelNames, target string) bool {
	for _, modelName := range xstrings.SplitTrimCompact(",", modelNames, customModelNames) {
		if modelName == target {
			return true
		}
	}
	return false
}

func (ra *relayAttempt) writeDirectChannelUpstreamError(ctx context.Context, inbound transformer.Inbound, err error) (int, bool) {
	if ra.routeMode != routeModeDirectChannel || !pipeline.IsUpstreamError(err) {
		return 0, false
	}
	var responseErr *llm.ResponseError
	if !errors.As(err, &responseErr) || responseErr.StatusCode < http.StatusBadRequest {
		return 0, false
	}

	clientErr := inbound.TransformError(ctx, err)
	if clientErr == nil || clientErr.StatusCode < http.StatusBadRequest {
		return 0, false
	}
	contentType := "application/json"
	if clientErr.Headers != nil && clientErr.Headers.Get("Content-Type") != "" {
		contentType = clientErr.Headers.Get("Content-Type")
	}
	ra.metrics.InternalResponse = append([]byte(nil), clientErr.Body...)
	ra.c.Data(clientErr.StatusCode, contentType, clientErr.Body)
	return clientErr.StatusCode, true
}

func directChannelFailureStatus(attempts []dbmodel.ChannelAttempt) int {
	if len(attempts) == 1 && attempts[0].Status == dbmodel.AttemptFailed {
		return http.StatusBadGateway
	}
	return http.StatusServiceUnavailable
}
