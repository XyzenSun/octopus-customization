package handlers

import (
    "errors"
    "net/http"
    "strconv"

    "github.com/bestruirui/octopus/internal/model"
    "github.com/bestruirui/octopus/internal/op"
    "github.com/bestruirui/octopus/internal/privacy"
    "github.com/bestruirui/octopus/internal/server/middleware"
    "github.com/bestruirui/octopus/internal/server/resp"
    "github.com/bestruirui/octopus/internal/server/router"
    "github.com/gin-gonic/gin"
)

func init() {
    router.NewGroupRouter("/api/v1/privacy").
        Use(middleware.Auth()).
        Use(middleware.RequireJSON()).
        AddRoute(router.NewRoute("/list", http.MethodGet).Handle(listPrivacyRules)).
        AddRoute(router.NewRoute("/create", http.MethodPost).Handle(createPrivacyRule)).
        AddRoute(router.NewRoute("/update", http.MethodPost).Handle(updatePrivacyRule)).
        AddRoute(router.NewRoute("/delete/:id", http.MethodDelete).Handle(deletePrivacyRule))
}

func listPrivacyRules(c *gin.Context) {
    rules, err := op.PrivacyRuleList(c.Request.Context())
    if err != nil {
        // 存储规则无效属于内部故障, 而非客户端输入错误.
        resp.Error(c, http.StatusInternalServerError, op.ErrPrivacyStorage.Error())
        return
    }
    if rules == nil {
        rules = make([]model.PrivacyRule, 0)
    }
    resp.Success(c, rules)
}

func createPrivacyRule(c *gin.Context) {
    var input model.PrivacyRuleCreate
    if err := c.ShouldBindJSON(&input); err != nil {
        resp.Error(c, http.StatusBadRequest, resp.ErrInvalidJSON)
        return
    }
    if err := input.Validate(); err != nil {
        privacyRuleError(c, err)
        return
    }
    rule, err := op.PrivacyRuleCreate(c.Request.Context(), input)
    if err != nil {
        privacyRuleError(c, err)
        return
    }
    resp.Success(c, rule)
}

func updatePrivacyRule(c *gin.Context) {
    var input model.PrivacyRuleUpdate
    if err := c.ShouldBindJSON(&input); err != nil {
        resp.Error(c, http.StatusBadRequest, resp.ErrInvalidJSON)
        return
    }
    if err := input.Validate(); err != nil {
        privacyRuleError(c, err)
        return
    }
    rule, err := op.PrivacyRuleUpdate(c.Request.Context(), input)
    if err != nil {
        privacyRuleError(c, err)
        return
    }
    resp.Success(c, rule)
}

func deletePrivacyRule(c *gin.Context) {
    id, err := strconv.Atoi(c.Param("id"))
    if err != nil || id <= 0 {
        resp.Error(c, http.StatusBadRequest, resp.ErrInvalidParam)
        return
    }
    if err := op.PrivacyRuleDelete(c.Request.Context(), id); err != nil {
        privacyRuleError(c, err)
        return
    }
    resp.Success(c, nil)
}

// 匹配已知错误, 但不返回传入错误的文本,
// 避免泄露数据库参数, 规则值或恢复的 panic 内容.
func privacyRuleError(c *gin.Context, err error) {
    switch {
    case errors.Is(err, privacy.ErrInvalidRule):
        resp.Error(c, http.StatusBadRequest, privacy.ErrInvalidRule.Error())
    case errors.Is(err, privacy.ErrRuleNotFound):
        resp.Error(c, http.StatusNotFound, privacy.ErrRuleNotFound.Error())
    case errors.Is(err, privacy.ErrRuleConflict):
        resp.Error(c, http.StatusConflict, privacy.ErrRuleConflict.Error())
    case errors.Is(err, privacy.ErrCompile):
        resp.Error(c, http.StatusInternalServerError, privacy.ErrCompile.Error())
    default:
        resp.Error(c, http.StatusInternalServerError, op.ErrPrivacyStorage.Error())
    }
}
