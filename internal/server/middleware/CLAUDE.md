# internal/server/middleware — Gin 中间件

## 目录定位

可组合的 Gin 中间件集合，通过 `router.Use(...)` 链式声明使用。

## 文件索引

- `auth.go` — JWT 校验中间件（管理面板用）+ APIKey 校验中间件（上游 LLM 转发用）
- `cors.go` — CORS 跨域头
- `logger.go` — 请求日志记录（接入 `internal/utils/log`）
- `static.go` — 嵌入式静态文件服务（`static/out/` 的 SPA）
- `validate.go` — `RequireJSON` 等通用请求体校验

## 关键约束

- **每个中间件单一职责**：不要在 auth 里夹带日志，不要在 logger 里写鉴权。
- **失败短路用 abort + resp**：中间件失败时调 `resp.Error(c, ...)` + `c.Abort()`，**不**直接 `c.JSON` + `return`（避免后续 handler 仍执行）。
- **APIKey 鉴权走缓存**：`APIKeyAuth` 通过 `op.APIKeyAuthenticate(key)` 命中内存缓存，不要每请求查 db。
- **静态文件 fallback**：`Static()` 对未匹配路径回退到 `index.html`（SPA 路由要求）；新增 API 路径前缀如 `/v2/`，要在 static 中间件白名单里排除。
