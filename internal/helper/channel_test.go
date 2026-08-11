package helper

import (
	"net/http"
	"testing"

	"github.com/bestruirui/octopus/internal/model"
)

func TestApplyCustomHeaders(t *testing.T) {
	emptyValue := ""
	normalValue := "custom"

	headers := http.Header{
		"X-Keep":        []string{"keep"},
		"X-Delete":      []string{"original"},
		"Authorization": []string{"Bearer upstream-key"},
	}

	ApplyCustomHeaders(headers, []model.CustomHeader{
		{HeaderKey: "X-New", HeaderValue: &normalValue},
		{HeaderKey: "X-Empty", HeaderValue: &emptyValue},
		{HeaderKey: "x-delete", HeaderValue: nil},
		{HeaderKey: "Authorization", HeaderValue: nil},
		{HeaderKey: "   ", HeaderValue: &normalValue},
	})

	if headers.Get("X-Keep") != "keep" {
		t.Fatalf("X-Keep = %q, want untouched", headers.Get("X-Keep"))
	}
	if headers.Get("X-New") != "custom" {
		t.Fatalf("X-New = %q, want added", headers.Get("X-New"))
	}
	// 空字符串保留一个显式空值 Header，与"字段不存在"是两种不同状态。
	if values, exists := headers["X-Empty"]; !exists || len(values) != 1 || values[0] != "" {
		t.Fatalf("X-Empty = %#v (exists=%t), want one explicit empty value", values, exists)
	}
	if _, exists := headers["X-Delete"]; exists {
		t.Fatalf("X-Delete should be removed by nil value: %#v", headers)
	}
	// 认证 Header 同样按用户配置处理，由用户自行保证渠道配置正确。
	if _, exists := headers["Authorization"]; exists {
		t.Fatalf("Authorization should be removable by user configuration: %#v", headers)
	}
	// 空白 key 视为无效配置，跳过而不是写入一个空名 Header。
	if len(headers) != 3 {
		t.Fatalf("unexpected header count: %#v", headers)
	}
}

func TestApplyCustomHeadersIgnoresNilHeaders(t *testing.T) {
	value := "custom"
	// 不应 panic：调用方可能在请求头尚未初始化时就应用配置。
	ApplyCustomHeaders(nil, []model.CustomHeader{{HeaderKey: "X-Foo", HeaderValue: &value}})
}
