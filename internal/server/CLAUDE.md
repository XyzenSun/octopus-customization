# internal/server — Gin HTTP 服务

## 目录定位

HTTP 服务编排：构造 Gin engine、注册中间件、加载所有 handlers（通过 `init()` 自注册）、提供静态前端。

## 文件索引

- `server.go` — `Start(addr)` 主入口：组装 engine + 全局中间件 + 启动监听
- `auth/` — JWT 签发与解析（详见子目录）
- `handlers/` — 各资源 HTTP 处理器（详见子目录，`init()` 自动注册到 router）
- `middleware/` — Auth/CORS/Logger/Static/RequireJSON 中间件
- `resp/` — 统一响应格式（`resp.Success/Error`）
- `router/` — 声明式路由注册框架（`GroupRouter` + `Route`）

## 关键约束

- **路由声明式**：handler 在 `init()` 里调 `router.NewGroupRouter().AddRoute(...)`；`server.go` 不维护路由表，只调 `router.Apply(engine)`。
- **响应统一走 resp 包**：handler 返回时用 `resp.Success(c, data)` 或 `resp.Error(c, code, msg)`，**不要**直接 `c.JSON()`，避免响应格式漂移。
- **中间件可组合**：路由组用 `.Use(middleware.Auth())` 链式声明，不要在每个 handler 内手动校验 token。
- **静态前端**：`middleware.Static()` 通过 `embed.FS` 提供 `static/out/` 下的 SPA；新增前端构建产物路径变化时同步更新。
- **debug pprof**：仅在 `conf.Debug` 开启时挂载 `/debug/pprof`；定位日志/relay 内存问题时启用此开关。
