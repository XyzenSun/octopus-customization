# internal/server/middleware

本目录提供管理 JWT 鉴权, APIKey 鉴权, CORS, 请求日志, 静态资源服务与 JSON 请求校验. 中间件通过路由组或服务组装组合, 请求 Logger 当前使用 Gin logger.

中间件保持单一职责. 鉴权或校验失败时用 resp.Error 中止请求并返回, 通过后继续执行后续处理. APIKey 校验沿用 op 的缓存查询, 避免每次请求访问数据库.

修改静态资源处理时读取 internal/server/middleware/static.go, 核对路径排除与中止行为. 当前只在文件存在时提供静态内容, 未实现未知路径回退 index.html; 新增 API 前缀时确认它能到达后续路由.
