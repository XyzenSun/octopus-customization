# internal/server/auth

本目录处理管理登录 JWT 的签发与校验, 并生成 APIKey 随机凭证. JWT 签名材料取自当前用户的用户名和密码哈希, 有效期由登录请求传入.

修改登录有效期或凭证更新行为时读取 internal/server/auth/auth.go 和 internal/server/handlers/user.go, 保持签发与校验的签名材料一致, 并核对有效期特殊值的现有语义. 当前没有 setting 表中的 JWT 有效期配置.

管理登录 JWT 与 LLM 调用 APIKey 保持各自的鉴权路径. 新增凭证调用点时复用本包现有生成逻辑, APIKey 校验继续由对应中间件完成.
