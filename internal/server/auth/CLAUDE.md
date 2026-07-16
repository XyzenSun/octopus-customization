# internal/server/auth — JWT 鉴权

## 目录定位

JWT token 签发与解析，仅服务于管理面板登录。

## 文件索引

- `auth.go` — `Generate(userID)`/`Parse(token)`：HS256 签名、过期校验

## 关键约束

- **secret 来源**：从 `internal/conf` 取，**不**写死或读环境变量副本。
- **过期时间**：通过 setting 表 `SettingKeyJWTExpire` 读取（运行时可调）；签发时统一调用 `op.SettingGetInt`。
- **不签发 APIKey 用的 token**：本目录仅服务管理面板登录；上游用户调用走 APIKey 鉴权（见 `middleware/auth.go` 的 `APIKeyAuth`）。
