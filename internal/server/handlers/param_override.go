package handlers

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/bestruirui/octopus/internal/server/resp"
	"github.com/gin-gonic/gin"
)

// validateParamOverride 只校验 param_override 的格式：必须是可反序列化为 map 的 JSON object。
// 不校验内容语义（比如 {"max_tokens":"true"} 这种值类型错误留给上游处理）。
// raw 为 nil 或空白字符串时视为"未配置"，直接通过。
func validateParamOverride(c *gin.Context, raw *string) bool {
	if raw == nil || strings.TrimSpace(*raw) == "" {
		return true
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(*raw), &obj); err != nil {
		resp.Error(c, http.StatusBadRequest, "param_override must be a valid JSON object")
		return false
	}
	return true
}
