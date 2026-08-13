package model

import (
	"testing"
	"time"
)

// Key 级熔断开启时，各上游状态码对应的冷却窗口判定；
// 同时验证开关关闭后冷却判定整体失效。
func TestGetChannelKeyCooldownWindow(t *testing.T) {
	tests := []struct {
		name             string
		statusCode       int
		lastUseAgo       int64 // 距今秒数，0 表示该 Key 从未被使用过
		wantPickedWhenOn bool  // Key 级熔断开启时是否仍能被选中
	}{
		{name: "429 冷却窗口内", statusCode: 429, lastUseAgo: 30, wantPickedWhenOn: false},
		{name: "429 冷却窗口外", statusCode: 429, lastUseAgo: 61, wantPickedWhenOn: true},
		{name: "503 冷却窗口内", statusCode: 503, lastUseAgo: 30, wantPickedWhenOn: false},
		{name: "503 冷却窗口外", statusCode: 503, lastUseAgo: 61, wantPickedWhenOn: true},
		{name: "401 冷却窗口内", statusCode: 401, lastUseAgo: 120, wantPickedWhenOn: false},
		{name: "401 冷却窗口外", statusCode: 401, lastUseAgo: 301, wantPickedWhenOn: true},
		{name: "403 冷却窗口内", statusCode: 403, lastUseAgo: 120, wantPickedWhenOn: false},
		{name: "403 冷却窗口外", statusCode: 403, lastUseAgo: 301, wantPickedWhenOn: true},
		{name: "500 不在冷却名单内", statusCode: 500, lastUseAgo: 1, wantPickedWhenOn: true},
		{name: "200 正常", statusCode: 200, lastUseAgo: 1, wantPickedWhenOn: true},
		{name: "429 但从未使用过", statusCode: 429, lastUseAgo: 0, wantPickedWhenOn: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lastUse := int64(0)
			if tt.lastUseAgo > 0 {
				lastUse = time.Now().Unix() - tt.lastUseAgo
			}
			channel := Channel{Keys: []ChannelKey{{
				ID:               1,
				Enabled:          true,
				ChannelKey:       "sk-test",
				StatusCode:       tt.statusCode,
				LastUseTimeStamp: lastUse,
			}}}

			picked := channel.GetChannelKey(true).ChannelKey != ""
			if picked != tt.wantPickedWhenOn {
				t.Fatalf("GetChannelKey(true) picked = %t, want %t", picked, tt.wantPickedWhenOn)
			}
			if channel.GetChannelKey(false).ChannelKey == "" {
				t.Fatal("GetChannelKey(false) 返回空 Key，关闭 Key 级熔断后冷却判定应完全失效")
			}
		})
	}
}

// 开关直接改变选中结果：开启时避开冷却中的低成本 Key，关闭时回落到成本最低者。
func TestGetChannelKeySwitchAffectsSelection(t *testing.T) {
	nowSec := time.Now().Unix()
	channel := Channel{Keys: []ChannelKey{
		{ID: 1, Enabled: true, ChannelKey: "cheap-but-cooling", StatusCode: 429, LastUseTimeStamp: nowSec, TotalCost: 1},
		{ID: 2, Enabled: true, ChannelKey: "healthy-but-pricey", StatusCode: 200, LastUseTimeStamp: nowSec, TotalCost: 5},
	}}

	if got := channel.GetChannelKey(true); got.ID != 2 {
		t.Fatalf("Key 级熔断开启时选中 ID = %d, want 2（冷却中的低成本 Key 应被跳过）", got.ID)
	}
	if got := channel.GetChannelKey(false); got.ID != 1 {
		t.Fatalf("Key 级熔断关闭时选中 ID = %d, want 1（应回落到成本最低的 Key）", got.ID)
	}
}

// 手动禁用与空 Key 不属于熔断范畴，两种开关状态下都必须跳过。
func TestGetChannelKeySkipsDisabledAndEmptyRegardlessOfSwitch(t *testing.T) {
	nowSec := time.Now().Unix()
	channel := Channel{Keys: []ChannelKey{
		{ID: 1, Enabled: false, ChannelKey: "manually-disabled"},
		{ID: 2, Enabled: true, ChannelKey: ""},
		{ID: 3, Enabled: true, ChannelKey: "cooling", StatusCode: 429, LastUseTimeStamp: nowSec, TotalCost: 9},
	}}

	if got := channel.GetChannelKey(false); got.ID != 3 {
		t.Fatalf("Key 级熔断关闭时选中 ID = %d, want 3（禁用与空 Key 仍应被跳过）", got.ID)
	}
	if got := channel.GetChannelKey(true); got.ChannelKey != "" {
		t.Fatalf("Key 级熔断开启时应无可用 Key，实际选中 ID = %d", got.ID)
	}
}
