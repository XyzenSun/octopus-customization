# internal/server/handlers — HTTP 处理器

## 目录定位

各资源的 Gin handler。每个文件 `init()` 里声明式注册路由（`router.NewGroupRouter().AddRoute(...)`）。

## 文件索引

- `apikey.go` — `/api/v1/apikey` CRUD
- `channel.go` — `/api/v1/channel` CRUD + 测试连接
- `group.go` — `/api/v1/group` CRUD + 模型映射
- `user.go` — 登录、修改密码、用户信息
- `setting.go` — 设置项读写
- `model.go` — 可用模型列表
- `log.go` — `/api/v1/log` 列表/清空 + SSE stream（含 token 一次性鉴权）
- `stats.go` — 多维度统计查询
- `update.go` — 版本更新检查

## 关键约束

- **handler 只编排**：解析参数 → 调 `op.Xxx` → 用 `resp.Success/Error` 返回；**禁止**直接 `db.GetDB()`，禁止内联业务逻辑。
- **路由注册在 `init()`**：新增 handler 文件时一并加 `init()` 块；不要在 `server.go` 手动注册。
- **鉴权选择**：管理面板路由用 `.Use(middleware.Auth())`（JWT）；上游 LLM 转发路由用 `.Use(middleware.APIKeyAuth())`。
- **SSE 鉴权特殊**：浏览器 `EventSource` 不支持自定义 header，所以 `log.go` 用"先 GET token，再 stream 时带 query 参数 + 一次性 revoke"模式（见 `getStreamToken`/`streamLog`）。新增 SSE 端点沿用此模式。
- **参数校验**：分页参数兜底（`page<1→1`，`pageSize>100→20`）；时间范围用 `*int` 表示可选。
