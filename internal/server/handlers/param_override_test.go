package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestValidateParamOverride(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name string
		raw  *string
		want bool // 是否通过校验
	}{
		{name: "nil pointer skips", raw: nil, want: true},
		{name: "empty string skips", raw: strPtr(""), want: true},
		{name: "whitespace skips", raw: strPtr("   "), want: true},
		{name: "plain object passes", raw: strPtr(`{"temperature":0.7}`), want: true},
		{name: "wrong value type still passes", raw: strPtr(`{"max_tokens":"true"}`), want: true},
		{name: "nested object passes", raw: strPtr(`{"metadata":{"source":"x"}}`), want: true},
		{name: "top level bool rejected", raw: strPtr(`"true"`), want: false},
		{name: "top level number rejected", raw: strPtr(`42`), want: false},
		{name: "top level array rejected", raw: strPtr(`["temperature",0.7]`), want: false},
		{name: "top level string rejected", raw: strPtr(`"just a string"`), want: false},
		{name: "malformed json rejected", raw: strPtr(`{"temperature":0.7,}`), want: false},
		{name: "unclosed brace rejected", raw: strPtr(`{"temperature":0.7`), want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodPost, "/", nil)

			got := validateParamOverride(c, tt.raw)
			if got != tt.want {
				t.Fatalf("validateParamOverride(%v) = %v, want %v", tt.raw, got, tt.want)
			}
			if !got && w.Code != http.StatusBadRequest {
				t.Fatalf("expected 400 on failure, got %d", w.Code)
			}
		})
	}
}

func strPtr(s string) *string {
	return &s
}
