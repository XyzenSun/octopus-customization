# internal/server

本目录组装 Gin 服务, 提供认证, 中间件, 管理 handler, 响应格式和路由注册子包. server.go 同时挂载管理路由注册表和独立的 LLM 转发路由.

修改服务组装时读取 internal/server/server.go, 核对 recovery, CORS, 静态资源中间件及路由挂载的顺序. 管理资源路由在 handlers 中声明, 转发入口在 server.go 中维护, 保持两者的鉴权与响应职责清晰.

Debug 当前控制 Gin 模式和请求 Logger, 没有挂载 pprof. 排查 debug 行为时以 internal/conf/debug.go 和 internal/server/server.go 为准, 避免依赖未实现的调试端点.
