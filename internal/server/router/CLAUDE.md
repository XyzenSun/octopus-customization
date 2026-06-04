# internal/server/router — 声明式路由注册框架

## 目录定位

提供 `GroupRouter` + `Route` DSL，让各 handler 在 `init()` 中声明路由，由 `server.go` 启动时统一 `Apply` 到 Gin engine。

## 文件索引

- `router.go` — `NewGroupRouter / NewRoute / AddRoute / Use / Apply` 全部 API

## 关键约束

- **使用模式**：

  ```go
  func init() {
      router.NewGroupRouter("/api/v1/foo").
          Use(middleware.Auth()).
          AddRoute(router.NewRoute("/list", http.MethodGet).Handle(listFoo))
  }
  ```

  不要绕过本框架直接调 `engine.GET()`。

- **注册时机**：`init()` 阶段把路由声明添加到全局表；`server.Start` 里调 `router.Apply(engine)` 一次性挂载。新增 handler 必须 `import _ "..."` 或显式 import 触发 `init()`。
- **不在本包写业务**：本目录只是 DSL；新增路由属性（如 swagger 注释）应扩展 `Route` 字段，而不是在 handler 内绕过。
