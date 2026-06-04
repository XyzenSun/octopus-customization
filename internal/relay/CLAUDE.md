# internal/relay — 请求转发核心

## 目录定位

接收前端 LLM 请求 → 选通道 → 适配协议 → 转发上游 → 流式回写客户端 → 记录指标。整个项目的核心热路径。

## 文件索引

- `relay.go` — 转发主流程：解析入站请求、循环尝试通道、SSE 流式 pipe、错误回退
- `transformers.go` — 入站/出站协议适配器工厂（OpenAI/Anthropic/Gemini/豆包等），通过 `axonhub/llm` pipeline 实现
- `metrics.go` — `RelayMetrics` 结构：累计 token/cost、调 `op.RelayLogAdd` 落日志、`op.StatsXxxUpdate` 累计指标
- `type.go` — relay 内部类型定义
- `balancer/` — 负载均衡策略，详见子目录 CLAUDE.md

## 关键约束

- **协议适配通过工厂**：新增协议格式（如 Mistral）在 `transformers.go` 工厂注册，不要在 `relay.go` 写 if/switch 协议判断。
- **流式响应在 ctx 取消后仍要落审计日志**：`metrics.Save` 用 `context.WithoutCancel(ctx)` 保护持久化；新增收尾逻辑也要遵循此模式（参见 `metrics.go:103`）。
- **请求体过滤**：日志保存前要过滤 `RawRequest`、图片二进制等大字段（见 `filterRequestForLog`）。新增请求字段如果是大数据/二进制，必须加入过滤。
- **每次 attempt 计 stats，最终落 cost**：通道成功/失败和等待时间在每次 attempt 结束时记录；token/cost 只在最终响应归到实际通道。
- **日志写入异步**：`RelayLogAdd` 走 op 层缓冲队列，不是同步 db 写入；不要在 relay 里加 `db.Save` 旁路。
