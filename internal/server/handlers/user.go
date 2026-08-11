package handlers

import (
	"net/http"
	"time"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/bestruirui/octopus/internal/server/auth"
	"github.com/bestruirui/octopus/internal/server/middleware"
	"github.com/bestruirui/octopus/internal/server/resp"
	"github.com/bestruirui/octopus/internal/server/router"
	"github.com/gin-gonic/gin"
)

func init() {
	router.NewGroupRouter("/api/v1/user").
		Use(middleware.RequireJSON()).
		AddRoute(
			router.NewRoute("/login", http.MethodPost).
				Handle(login),
		)
	// 登录页需要在未登录状态下展示服务器时间来诊断 TOTP 时间偏差，因此不加鉴权。
	router.NewGroupRouter("/api/v1/user").
		AddRoute(
			router.NewRoute("/time", http.MethodGet).
				Handle(serverTime),
		)
	router.NewGroupRouter("/api/v1/user").
		Use(middleware.Auth()).
		Use(middleware.RequireJSON()).
		AddRoute(
			router.NewRoute("/change-password", http.MethodPost).
				Handle(changePassword),
		).
		AddRoute(
			router.NewRoute("/change-username", http.MethodPost).
				Handle(changeUsername),
		).
		AddRoute(
			router.NewRoute("/status", http.MethodGet).
				Handle(status),
		).
		AddRoute(
			router.NewRoute("/2fa/setup", http.MethodPost).
				Handle(twoFactorSetup),
		).
		AddRoute(
			router.NewRoute("/2fa/enable", http.MethodPost).
				Handle(twoFactorEnable),
		).
		AddRoute(
			router.NewRoute("/2fa/disable", http.MethodPost).
				Handle(twoFactorDisable),
		)
}

func login(c *gin.Context) {
	var user model.UserLogin
	if err := c.ShouldBindJSON(&user); err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidJSON)
		return
	}
	if err := op.UserVerify(user.Username, user.Password); err != nil {
		resp.Error(c, http.StatusUnauthorized, resp.ErrUnauthorized)
		return
	}
	// 密码校验通过后才走 TOTP，失败计数因此只统计验证码环节。
	// 错误信息带上原因（验证码错误 / 已锁定），密码环节仍保持笼统的 ErrUnauthorized，
	// 不会因此泄露"用户名密码是否正确"。
	if err := op.TwoFactorVerifyLogin(user.Code); err != nil {
		resp.Error(c, http.StatusUnauthorized, err.Error())
		return
	}
	token, expire, err := auth.GenerateJWTToken(user.Expire)
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, resp.ErrInternalServer)
		return
	}
	resp.Success(c, model.UserLoginResponse{Token: token, ExpireAt: expire})
}

func changePassword(c *gin.Context) {
	var user model.UserChangePassword
	if err := c.ShouldBindJSON(&user); err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidJSON)
		return
	}
	if err := op.UserChangePassword(user.OldPassword, user.NewPassword); err != nil {
		resp.Error(c, http.StatusInternalServerError, resp.ErrDatabase)
		return
	}
	resp.Success(c, "password changed successfully")
}

func changeUsername(c *gin.Context) {
	var user model.UserChangeUsername
	if err := c.ShouldBindJSON(&user); err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidJSON)
		return
	}
	if err := op.UserChangeUsername(user.NewUsername); err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, "username changed successfully")
}

func status(c *gin.Context) {
	resp.Success(c, "ok")
}

// serverTime 固定按东八区展示。TOTP 本身基于 Unix 时间戳、与时区无关，
// 固定时区只是给用户一个稳定的比对基准——服务器常跑在 UTC 容器里，
// 直接展示本地时区会让人误以为差了 8 小时是故障。
func serverTime(c *gin.Context) {
	const timezoneName = "UTC+8"
	location := time.FixedZone(timezoneName, 8*60*60)
	resp.Success(c, model.ServerTimeResponse{
		ServerTime:       time.Now().In(location).Format("2006-01-02 15:04:05"),
		Timezone:         timezoneName,
		TwoFactorEnabled: op.TwoFactorEnabled(),
	})
}

func twoFactorSetup(c *gin.Context) {
	setup, err := op.TwoFactorSetup()
	if err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	resp.Success(c, setup)
}

func twoFactorEnable(c *gin.Context) {
	var req model.TwoFactorCodeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidJSON)
		return
	}
	if err := op.TwoFactorEnable(req.Code); err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	resp.Success(c, "two factor authentication enabled")
}

func twoFactorDisable(c *gin.Context) {
	var req model.TwoFactorCodeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidJSON)
		return
	}
	if err := op.TwoFactorDisable(req.Code); err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	resp.Success(c, "two factor authentication disabled")
}
