package relay

import (
	"net/http"

	dbmodel "github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/relay/balancer"
	"github.com/gin-gonic/gin"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/transformer"
)

type routeMode int

const (
	routeModeGroup routeMode = iota
	routeModeDirectChannel
)

// relayRun 保存一次客户端请求在负载均衡循环中共享的状态。
type relayRun struct {
	c               *gin.Context
	inboundType     llm.APIFormat
	inAdapter       transformer.Inbound
	internalRequest *llm.Request
	metrics         *RelayMetrics
	iter            *balancer.Iterator
	group           dbmodel.Group
	routeMode       routeMode
}

// relayAttempt 保存一次上游通道尝试的状态。
type relayAttempt struct {
	*relayRun

	outAdapter transformer.Outbound
	channel    *dbmodel.Channel
	usedKey    dbmodel.ChannelKey
	httpClient *http.Client
}
