package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bestruirui/octopus/internal/conf"
	"github.com/gin-gonic/gin"
)

func TestIsAdminSameOrigin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	conf.AppConfig.AdminOrigins = "http://localhost:3000,https://octopus.example.com"

	tests := []struct {
		name      string
		fetchSite string
		origin    string
		referer   string
		host      string
		want      bool
	}{
		{name: "fetch metadata same origin", fetchSite: "same-origin", want: true},
		{name: "same site sibling rejected", fetchSite: "same-site", want: false},
		{name: "cross site rejected", fetchSite: "cross-site", want: false},
		{name: "configured origin fallback", origin: "http://localhost:3000", host: "127.0.0.1:8080", want: true},
		{name: "request origin fallback", origin: "http://127.0.0.1:8080", host: "127.0.0.1:8080", want: true},
		{name: "referer fallback", referer: "http://127.0.0.1:8080/settings", host: "127.0.0.1:8080", want: true},
		{name: "untrusted origin rejected", origin: "https://evil.example", host: "127.0.0.1:8080", want: false},
		{name: "missing provenance rejected", host: "127.0.0.1:8080", want: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			context, _ := gin.CreateTestContext(recorder)
			request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8080/api/v1/user/status", nil)
			if test.host != "" {
				request.Host = test.host
			}
			request.Header.Set("Sec-Fetch-Site", test.fetchSite)
			request.Header.Set("Origin", test.origin)
			request.Header.Set("Referer", test.referer)
			context.Request = request

			if got := isAdminSameOrigin(context); got != test.want {
				t.Fatalf("isAdminSameOrigin() = %t, want %t", got, test.want)
			}
		})
	}
}
