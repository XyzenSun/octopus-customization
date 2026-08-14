package handlers

import (
	"net/http"

	"github.com/bestruirui/octopus/internal/relay/balancer"
	"github.com/bestruirui/octopus/internal/server/middleware"
	"github.com/bestruirui/octopus/internal/server/resp"
	"github.com/bestruirui/octopus/internal/server/router"
	"github.com/gin-gonic/gin"
)

func init() {
	router.NewGroupRouter("/api/v1/circuit").
		Use(middleware.AdminAuth()).
		AddRoute(
			router.NewRoute("/status", http.MethodGet).
				Handle(getCircuitStatus),
		).
		AddRoute(
			router.NewRoute("/reset", http.MethodPost).
				Handle(resetCircuit),
		)
}

// getCircuitStatus 查询当前处于熔断状态的通道列表
func getCircuitStatus(c *gin.Context) {
	list := balancer.ListTripped()
	resp.Success(c, list)
}

// resetCircuit 清空所有熔断状态
func resetCircuit(c *gin.Context) {
	balancer.ResetAll()
	resp.Success(c, nil)
}
