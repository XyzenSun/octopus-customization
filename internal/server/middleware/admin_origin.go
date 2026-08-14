package middleware

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/bestruirui/octopus/internal/conf"
	"github.com/bestruirui/octopus/internal/server/resp"
	"github.com/gin-gonic/gin"
)

// AdminSameOrigin 在签发或使用管理员 Cookie 前拒绝跨站浏览器请求。
func AdminSameOrigin() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !isAdminSameOrigin(c) {
			resp.Error(c, http.StatusForbidden, resp.ErrForbidden)
			c.Abort()
			return
		}
		c.Header("Cache-Control", "private, no-store")
		c.Next()
	}
}

func isAdminSameOrigin(c *gin.Context) bool {
	site := strings.ToLower(strings.TrimSpace(c.GetHeader("Sec-Fetch-Site")))
	switch site {
	case "same-origin":
		return true
	case "same-site", "cross-site", "none":
		return false
	case "":
		// 旧浏览器和测试客户端没有 Fetch Metadata，必须使用精确 Origin 兜底。
		return adminOriginAllowed(c.Request)
	default:
		return false
	}
}

func adminOriginAllowed(request *http.Request) bool {
	origin := strings.TrimSpace(request.Header.Get("Origin"))
	if origin == "" {
		referer, err := url.Parse(strings.TrimSpace(request.Referer()))
		if err != nil || referer.Scheme == "" || referer.Host == "" {
			return false
		}
		origin = referer.Scheme + "://" + referer.Host
	}

	for _, allowed := range strings.Split(conf.AppConfig.AdminOrigins, ",") {
		if sameOriginValue(origin, strings.TrimRight(strings.TrimSpace(allowed), "/")) {
			return true
		}
	}

	return sameOriginValue(origin, requestOrigin(request))
}

func requestOrigin(request *http.Request) string {
	scheme := "http"
	if request.TLS != nil || strings.EqualFold(strings.TrimSpace(request.Header.Get("X-Forwarded-Proto")), "https") {
		scheme = "https"
	}
	return scheme + "://" + request.Host
}

func sameOriginValue(left, right string) bool {
	if left == "" || right == "" {
		return false
	}
	leftURL, leftErr := url.Parse(left)
	rightURL, rightErr := url.Parse(right)
	if leftErr != nil || rightErr != nil || leftURL.Scheme == "" || rightURL.Scheme == "" {
		return false
	}
	return strings.EqualFold(leftURL.Scheme, rightURL.Scheme) &&
		strings.EqualFold(leftURL.Host, rightURL.Host) &&
		leftURL.Path == "" && leftURL.RawQuery == "" && leftURL.Fragment == "" &&
		rightURL.Path == "" && rightURL.RawQuery == "" && rightURL.Fragment == ""
}
