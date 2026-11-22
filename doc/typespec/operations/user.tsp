using TypeSpec.Http;
using TypeSpec.OpenAPI;

namespace Octopus;

@route("/api/v1/user/login")
@post
@operationId("loginUser")
@tag("User")
@summary("管理员登录")
@doc("公开接口. RequireJSON 要求 Content-Type 包含大小写敏感的 application/json, 不符合时返回 415. 使用 ShouldBindJSON 解析请求, 请求字段均无 binding required. 先验证当前缓存中的用户名和 bcrypt 密码, 再验证 TOTP. 未启用两步验证时忽略 code. 启用时, 密码正确但验证码失败计入全局内存计数, 5 次失败后锁定 5 分钟. 密码错误, 验证码错误和锁定均返回相同的 401, 不在响应中区分原因. expire 实际单位为分钟: 0 使用 15 分钟, 正数使用对应分钟数, -1 使用 30 天. expire 小于 -1 时未设置 ExpiresAt, 后续 Format 存在 nil 解引用路径, 可能由全局 recovery 返回 500. 极大正数直接乘 time.Minute 可能发生 duration 溢出, 200 签发响应不保证 JWT 尚未过期. JWT 使用当前用户名与密码哈希拼接作为签名 secret. 不保证运行服务已经启用两步验证. 源码: internal/server/handlers/user.go:59-85, internal/server/auth/auth.go:13-43, internal/op/twofactor.go:155-183.")
@useAuth(NoAuth)
@extension("security", #[])
op loginUser(
  @header contentType: "application/json",

  @doc("JSON 请求体. 属性可以省略, 但空 body 会导致解析错误. Go 标量对 JSON null 的解析较宽松, 可能保留零值, 这里不将 null 建模为标量的规范输入类型. 没有用户名, 密码长度或 expire 范围的 binding 校验.")
  @body
  body: UserLogin,
):
  | HttpResponse<
      200,
      Success<UserLoginResponse>,
      "登录成功, 返回 JWT 和 RFC3339 过期时间."
    >
  | HttpResponse<
      400,
      ErrorResponse,
      "ShouldBindJSON 解析或类型绑定失败, message 为 Invalid JSON format. 缺少普通属性不因 required 标签报错."
    >
  | HttpResponse<
      401,
      ErrorResponse,
      "用户名或密码不匹配, TOTP 无效, 或 TOTP 登录处于锁定期. message 均为 Authentication failed."
    >
  | InvalidJSON415
  | Internal500;

@route("/api/v1/user/time")
@get
@operationId("getUserServerTime")
@tag("User")
@summary("查询服务器时间和两步验证开关")
@doc("公开接口, 不使用 JWT 鉴权或 RequireJSON, 不解析请求 body 或 query. 返回按固定 UTC+8 展示的当前服务器时间和两步验证开关, 供登录页诊断时间偏差. TOTP 本身基于 Unix 时间, 与展示时区无关. 设置读取失败时 TwoFactorEnabled 按 false 处理. 源码: internal/server/handlers/user.go:120-128, internal/op/twofactor.go:67-70.")
@useAuth(NoAuth)
@extension("security", #[])
op getUserServerTime():
  | HttpResponse<200, Success<UserTime>, "服务器时间和当前两步验证开关.">
  | Internal500;

@route("/api/v1/user/change-password")
@post
@operationId("changeUserPassword")
@tag("User")
@summary("修改管理员密码")
@doc("继承根级 JWTAuth. Auth 先于 RequireJSON 执行. 请求无 binding required, op 先验证旧密码, 再 bcrypt 哈希新密码并更新数据库. 没有显式的新密码非空, 最小长度或复杂度规则. 旧密码错误, 哈希失败和数据库失败均映射为 500 Database operation failed, 不是 400 或 401. 成功后当前密码哈希变化, 后续 JWT 校验使用的新 secret 会使旧 JWT 失效. op 在数据库更新前修改用户缓存, 失败路径也不能保证缓存状态不变. RequireJSON 失败返回 415. 源码: internal/server/handlers/user.go:87-98, internal/op/user.go:29-43, internal/server/auth/auth.go:36-43.")
op changeUserPassword(
  @header contentType: "application/json",

  @doc("JSON 请求体. 属性可省略, 标量 JSON null 可能按 Go 零值宽松解析. 省略旧密码通常在后续密码验证失败, 并映射为 500.")
  @body
  body: UserChangePassword,
):
  | HttpResponse<200, Success<"password changed successfully">, "密码修改成功.">
  | HttpResponse<
      400,
      ErrorResponse,
      "缺少 Authorization 时 message 为 Invalid request parameters. ShouldBindJSON 失败时 message 为 Invalid JSON format."
    >
  | Unauthorized401
  | InvalidJSON415
  | HttpResponse<
      500,
      ErrorResponse,
      "UserChangePassword 的任意错误, 包括旧密码不匹配, 哈希失败或数据库失败, 均返回 Database operation failed. 未捕获 panic 由全局 recovery 返回 500."
    >;

@route("/api/v1/user/change-username")
@post
@operationId("changeUserUsername")
@tag("User")
@summary("修改管理员用户名")
@doc("继承根级 JWTAuth. Auth 先于 RequireJSON 执行. 请求无 binding required. op 仅显式拒绝与旧用户名相同的值, 没有非空, 长度或字符集手动校验. 同名拒绝和数据库错误均返回 500, message 为 op 错误文本. 成功后用户名变化, 后续 JWT 校验使用的新 secret 会使旧 JWT 失效. op 在数据库更新前修改用户缓存, 失败路径也不能保证缓存状态不变. RequireJSON 失败返回 415. 源码: internal/server/handlers/user.go:100-111, internal/op/user.go:46-54.")
op changeUserUsername(
  @header contentType: "application/json",

  @doc("JSON 请求体. new_username 可以省略, 标量 JSON null 可能保留 Go 零值. 不将 GORM unique 标签当作 HTTP 输入校验.")
  @body
  body: UserChangeUsername,
):
  | HttpResponse<
      200,
      Success<"username changed successfully">,
      "用户名修改成功."
    >
  | HttpResponse<
      400,
      ErrorResponse,
      "缺少 Authorization 时 message 为 Invalid request parameters. ShouldBindJSON 失败时 message 为 Invalid JSON format."
    >
  | Unauthorized401
  | InvalidJSON415
  | HttpResponse<
      500,
      ErrorResponse,
      "新用户名与旧值相同, 或更新数据库失败, 返回 op 错误文本. 全局 recovery 也可能返回 500."
    >;

@route("/api/v1/user/status")
@get
@operationId("getUserStatus")
@tag("User")
@summary("检查管理员登录状态")
@doc("继承根级 JWTAuth. 组中注册的 RequireJSON 对 GET 直接放行, 不要求 Content-Type, 不解析请求 body 或 query. handler 仅返回 ok, 不执行数据库或上游健康检查. 源码: internal/server/handlers/user.go:30-43,113-115.")
op getUserStatus():
  | HttpResponse<200, Success<"ok">, "JWT 鉴权通过, data 为 ok.">
  | MissingAuth400
  | Unauthorized401
  | Internal500;

#suppress "@typespec/http/content-type-ignored" "此接口只检查 Content-Type, handler 不读取 body, 原参数要求通过生成元数据保留."
@route("/api/v1/user/2fa/setup")
@post
@operationId("setupUserTwoFactor")
@tag("User")
@summary("生成并保存两步验证绑定信息")
@doc("继承根级 JWTAuth. RequireJSON 要求 Content-Type 包含 application/json, 但 handler 不读取或解析 body, 不要求提供 JSON body. 已启用两步验证时拒绝 setup. 每次成功调用都会生成并覆盖已保存的 TOTP secret, 返回 secret, otpauth URI 和 PNG data URL, 不自动打开两步验证开关. 生成, 保存和二维码处理错误全部映射为 400. 因 secret 在渲染二维码之前保存, 后续失败不代表没有写入. 源码: internal/server/handlers/user.go:130-137, internal/op/twofactor.go:78-108.")
@extension(
  "parameters",
  #[
    #{
      name: "Content-Type",
      in: "header",
      required: true,
      description: "RequireJSON 要求值包含大小写敏感的 application/json. 即使没有 body 也必须提供此请求头.",
      schema: #{ type: "string" },
      example: "application/json",
    }
  ]
)
op setupUserTwoFactor(
  @doc("RequireJSON 要求值包含大小写敏感的 application/json. 即使没有 body 也必须提供此请求头.")
  @header("Content-Type")
  Content_Type: string,
):
  | HttpResponse<
      200,
      Success<TwoFactorSetup>,
      "新的绑定信息已生成并保存, 仍需 enable 校验验证码后启用."
    >
  | HttpResponse<
      400,
      ErrorResponse,
      "缺少 Authorization 时返回 Invalid request parameters. 已启用两步验证时返回 two factor authentication is already enabled. TOTP 生成, secret 保存或二维码处理失败时返回对应 op 错误文本."
    >
  | Unauthorized401
  | InvalidJSON415
  | Internal500;

@route("/api/v1/user/2fa/enable")
@post
@operationId("enableUserTwoFactor")
@tag("User")
@summary("校验验证码并启用两步验证")
@doc("继承根级 JWTAuth. Auth 先于 RequireJSON 执行. ShouldBindJSON 解析 code, 没有 binding required. op 要求尚未启用且已保存 secret, 再使用 totp.Validate 校验验证码并写入启用设置. 不应将登录失败计数或 verifyTOTPCode 的自定义窗口推断为 enable 的实现规则. op 错误包括设置写入错误均映射为 400. RequireJSON 失败返回 415. 源码: internal/server/handlers/user.go:139-150, internal/op/twofactor.go:112-122.")
op enableUserTwoFactor(
  @header contentType: "application/json",

  @doc("JSON 请求体. code 可在解析层省略, 但验证码无效会被 op 拒绝.")
  @body
  body: TwoFactorCodeRequest,
):
  | HttpResponse<
      200,
      Success<"two factor authentication enabled">,
      "两步验证已启用."
    >
  | HttpResponse<
      400,
      ErrorResponse,
      "缺少 Authorization, JSON 绑定失败, 已启用, 尚未 setup, 验证码无效或设置写入失败. 绑定失败返回 Invalid JSON format, op 失败返回其错误文本."
    >
  | Unauthorized401
  | InvalidJSON415
  | Internal500;

@route("/api/v1/user/2fa/disable")
@post
@operationId("disableUserTwoFactor")
@tag("User")
@summary("校验验证码并关闭两步验证")
@doc("继承根级 JWTAuth. Auth 先于 RequireJSON 执行. 请求 code 没有 binding required. op 要求当前已启用, 并通过 verifyTOTPCode 校验当前验证码, 使用 6 位 SHA1, 30 秒周期和前后各一个周期容差. 先关闭设置, 再清空已保存 secret, 最后重置登录失败计数. 步骤失败不代表前面的写入回滚. 不接受仅凭 JWT 无验证码关闭. RequireJSON 失败返回 415. 源码: internal/server/handlers/user.go:152-163, internal/op/twofactor.go:127-141,170-183.")
op disableUserTwoFactor(
  @header contentType: "application/json",

  @doc("JSON 请求体. code 可在解析层省略, 但 op 会拒绝无效验证码.")
  @body
  body: TwoFactorCodeRequest,
):
  | HttpResponse<
      200,
      Success<"two factor authentication disabled">,
      "两步验证已关闭, secret 已清空."
    >
  | HttpResponse<
      400,
      ErrorResponse,
      "缺少 Authorization, JSON 绑定失败, 尚未启用, 验证码无效, 关闭设置或清空 secret 失败. 绑定失败返回 Invalid JSON format, op 失败返回其错误文本."
    >
  | Unauthorized401
  | InvalidJSON415
  | Internal500;
