package handlers

import (
	"context"
	"net/http"
	"time"

	"github.com/bestruirui/octopus/internal/helper"
	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/bestruirui/octopus/internal/server/middleware"
	"github.com/bestruirui/octopus/internal/server/resp"
	"github.com/bestruirui/octopus/internal/server/router"
	"github.com/bestruirui/octopus/internal/utils/xstrings"
	"github.com/gin-gonic/gin"
)

func init() {
	router.NewGroupRouter("/api/v1/channel").
		Use(middleware.Auth()).
		Use(middleware.RequireJSON()).
		AddRoute(
			router.NewRoute("/test-model", http.MethodPost).
				Handle(testChannelModel),
		)
}

// testModelRequest POST /api/v1/channel/test-model 请求体。
// ChannelID 与 Channel 二选一:
//   - ChannelID:测已保存到 DB 的渠道,key_index 是该渠道 channel.keys 数组下标
//   - Channel:测新建弹窗里未保存的临时渠道(用户已输入 Key 但还没提交),key_index 同样是 keys 数组下标
type testModelRequest struct {
	ChannelID *int           `json:"channel_id,omitempty"`
	Channel   *model.Channel `json:"channel,omitempty"`
	Model     string         `json:"model" binding:"required"`
	KeyIndex  int            `json:"key_index,omitempty"` // 0 是合法值,表示第一个 Key
	TimeoutMS int64          `json:"timeout_ms,omitempty"`
}

// testChannelModel 对单个渠道上的单个模型发起真实测试调用。
//
// NOTE: 本文件原名 channel_test.go,但 Go 工具链会把 *_test.go 一律视为
// 单元测试文件并排除在普通 build 之外,会导致路由不在二进制中注册(404)。
// 命名为 channel_model_probe.go 避开该后缀。
//
// NOTE(security): 当前端走 ChannelID 路径时仅传 ID 与模型名,Channel 数据从缓存读取,
// 不要求前端把 ChannelKey 再发回来(虽然 listChannel 接口设计如此——见 /workspace/octopus安全泄露问题.md)。
func testChannelModel(c *gin.Context) {
	var req testModelRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidJSON)
		return
	}

	// 渠道来源二选一
	hasID := req.ChannelID != nil && *req.ChannelID > 0
	hasChannel := req.Channel != nil
	if (hasID && hasChannel) || (!hasID && !hasChannel) {
		resp.Error(c, http.StatusBadRequest, "exactly one of channel_id or channel is required")
		return
	}

	var channel *model.Channel
	if hasID {
		ch, err := op.ChannelGet(*req.ChannelID, c.Request.Context())
		if err != nil {
			resp.Error(c, http.StatusNotFound, "channel not found")
			return
		}
		channel = ch
	} else {
		channel = req.Channel
	}

	// key_index 边界检查
	if req.KeyIndex < 0 || req.KeyIndex >= len(channel.Keys) {
		resp.Error(c, http.StatusBadRequest, "key_index out of range")
		return
	}
	// 模型必须在渠道已选模型集合里(model + custom_model 合并去重)
	modelSet := make(map[string]struct{})
	for _, m := range xstrings.SplitTrimCompact(",", channel.Model, channel.CustomModel) {
		modelSet[m] = struct{}{}
	}
	if _, ok := modelSet[req.Model]; !ok {
		resp.Error(c, http.StatusBadRequest, "model is not in channel's selected models")
		return
	}

	// 超时
	timeout := 30 * time.Second
	if req.TimeoutMS > 0 {
		timeout = time.Duration(req.TimeoutMS) * time.Millisecond
	}
	// 上限保护:单次最长 5 分钟,防止恶意构造的 timeout_ms 挂死资源
	if timeout > 5*time.Minute {
		timeout = 5 * time.Minute
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), timeout)
	defer cancel()

	result, err := helper.TestChannelModel(ctx, channel, req.Model, req.KeyIndex, timeout)
	if err != nil {
		// helper 返回的错误都是参数/上下文类问题,统一回 400
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	resp.Success(c, result)
}
