# internal/server/auth — JWT 鉴权

## 目录定位

JWT 签发与解析仅服务于管理面板，并通过固定 7 天的 HttpOnly Cookie 传递。

## 文件索引

- `auth.go` — 固定 7 天管理员 JWT、严格 claims 校验、HttpOnly Cookie 设置与清理

## 关键约束

- **secret 来源**：由当前用户名和密码哈希派生，修改任一项会使旧会话立即失效；不要额外复制或写死 secret。
- **过期时间**：固定 7 天，客户端不可指定；Cookie 的 Max-Age 与 JWT exp 保持一致。
- **不签发 APIKey 用的 token**：本目录仅服务管理面板登录；上游用户调用走 APIKey 鉴权（见 `middleware/auth.go` 的 `APIKeyAuth`）。
