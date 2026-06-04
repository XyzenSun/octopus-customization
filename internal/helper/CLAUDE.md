# internal/helper — 跨层辅助工具

## 目录定位

不属于 `model`/`op`/`relay` 任何单一层的横切辅助函数：渠道延迟探测、价格同步、上游 fetch 工具。

## 文件索引

- `channel.go` — 渠道相关辅助（构造请求 URL、按渠道选 client 等）
- `delay.go` — base URL 延迟探测（被 `task.ChannelBaseUrlDelayTask` 调用）
- `fetch.go` — 通用上游 HTTP fetch 工具（带超时和重试）
- `price.go` — 模型价格同步辅助（被 `price.UpdateLLMPrice` 调用）

## 关键约束

- **不放业务逻辑**：本目录是工具集，所有函数应是无状态、可独立测试的纯函数式辅助；带状态/缓存的写到 `internal/op`。
- **依赖方向**：可依赖 `model`/`utils/log`/`client`，**不要**依赖 `op`/`server`/`task`（避免循环依赖）。
- **新工具优先合并**：新增辅助函数前先看现有四个文件能否归类，避免无序膨胀（如新加一个 channel 相关辅助应进 `channel.go`）。
