# internal/server/router

本目录提供管理 API 的路由声明与注册机制. router.go 保存路由组, 中间件和 handler 声明, 由 HTTP 服务启动时统一挂载.

管理路由通过 handler 包的 init 注册, 包被导入后即可触发该包内所有 init. 新增管理端点沿用路由组声明方式, 转发路由仍由 server 的独立入口维护.

修改注册机制时读取 internal/server/router/router.go, 核对路由校验, 组级与路由级中间件的顺序, 以及注册表在挂载后清空的行为. 本包专注路由机制, 业务处理留在 handler 及其调用的业务包.
