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
      "登录成功, 返回 JWT 和 RFC3339 过期时间.",
      "application/json"
    >
  | HttpResponse<
      400,
      ErrorResponse,
      "ShouldBindJSON 解析或类型绑定失败, message 为 Invalid JSON format. 缺少普通属性不因 required 标签报错.",
      "application/json"
    >
  | HttpResponse<
      401,
      ErrorResponse,
      "用户名或密码不匹配, TOTP 无效, 或 TOTP 登录处于锁定期. message 均为 Authentication failed.",
      "application/json"
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
  | HttpResponse<
      200,
      Success<UserTime>,
      "服务器时间和当前两步验证开关.",
      "application/json"
    >
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
  | HttpResponse<
      200,
      Success<"password changed successfully">,
      "密码修改成功.",
      "application/json"
    >
  | HttpResponse<
      400,
      ErrorResponse,
      "缺少 Authorization 时 message 为 Invalid request parameters. ShouldBindJSON 失败时 message 为 Invalid JSON format.",
      "application/json"
    >
  | Unauthorized401
  | InvalidJSON415
  | HttpResponse<
      500,
      ErrorResponse,
      "UserChangePassword 的任意错误, 包括旧密码不匹配, 哈希失败或数据库失败, 均返回 Database operation failed. 未捕获 panic 由全局 recovery 返回 500.",
      "application/json"
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
      "用户名修改成功.",
      "application/json"
    >
  | HttpResponse<
      400,
      ErrorResponse,
      "缺少 Authorization 时 message 为 Invalid request parameters. ShouldBindJSON 失败时 message 为 Invalid JSON format.",
      "application/json"
    >
  | Unauthorized401
  | InvalidJSON415
  | HttpResponse<
      500,
      ErrorResponse,
      "新用户名与旧值相同, 或更新数据库失败, 返回 op 错误文本. 全局 recovery 也可能返回 500.",
      "application/json"
    >;

@route("/api/v1/user/status")
@get
@operationId("getUserStatus")
@tag("User")
@summary("检查管理员登录状态")
@doc("继承根级 JWTAuth. 组中注册的 RequireJSON 对 GET 直接放行, 不要求 Content-Type, 不解析请求 body 或 query. handler 仅返回 ok, 不执行数据库或上游健康检查. 源码: internal/server/handlers/user.go:30-43,113-115.")
op getUserStatus():
  | HttpResponse<
      200,
      Success<"ok">,
      "JWT 鉴权通过, data 为 ok.",
      "application/json"
    >
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
      "新的绑定信息已生成并保存, 仍需 enable 校验验证码后启用.",
      "application/json"
    >
  | HttpResponse<
      400,
      ErrorResponse,
      "缺少 Authorization 时返回 Invalid request parameters. 已启用两步验证时返回 two factor authentication is already enabled. TOTP 生成, secret 保存或二维码处理失败时返回对应 op 错误文本.",
      "application/json"
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
      "两步验证已启用.",
      "application/json"
    >
  | HttpResponse<
      400,
      ErrorResponse,
      "缺少 Authorization, JSON 绑定失败, 已启用, 尚未 setup, 验证码无效或设置写入失败. 绑定失败返回 Invalid JSON format, op 失败返回其错误文本.",
      "application/json"
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
      "两步验证已关闭, secret 已清空.",
      "application/json"
    >
  | HttpResponse<
      400,
      ErrorResponse,
      "缺少 Authorization, JSON 绑定失败, 尚未启用, 验证码无效, 关闭设置或清空 secret 失败. 绑定失败返回 Invalid JSON format, op 失败返回其错误文本.",
      "application/json"
    >
  | Unauthorized401
  | InvalidJSON415
  | Internal500;

@route("/api/v1/channel/list")
@get
@operationId("listChannels")
@tag("Channel")
@summary("获取渠道列表及统计")
@doc("继承根级 JWTAuth. RequireJSON 对 GET 放行, 不要求 Content-Type. 从 channelCache 获取渠道, handler 主动设置每项非 null 的 stats, 并原样返回 keys, 没有密钥脱敏. 当前 op 将结果初始化为非 nil 切片, 无记录时返回空数组, 没有排序保证. StatsChannelGet 缓存未命中时会创建零值统计并标记待持久化. 不解析分页或筛选参数. handler 保留 op 错误的 500 分支, 但当前 ChannelList 总是返回 nil error. 源码: internal/server/handlers/channel.go:60-71, internal/op/channel.go:21-27, internal/op/stats.go:359-375.")
op listChannels():
  | HttpResponse<
      200,
      Success<Channel[]>,
      "渠道数组, 每项含 stats. nil 的渠道切片字段仍可能序列化为 null.",
      "application/json"
    >
  | MissingAuth400
  | Unauthorized401
  | Internal500;

@route("/api/v1/channel/create")
@post
@operationId("createChannel")
@tag("Channel")
@summary("创建渠道并触发辅助处理")
@doc("继承根级 JWTAuth. Auth 先于 RequireJSON 执行. ShouldBindJSON 直接绑定 Channel 实体, 无 binding required, 没有 HTTP 层名称非空, URL 格式, key 非空或枚举范围手动校验. 唯一显式配置格式校验是 param_override: nil 或空白字符串通过, 其余字符串必须能反序列化到 map, JSON object 和字符串内容 null 均通过, 数组或普通标量失败. 不校验对象内参数语义. op 直接 GORM Create, GORM 标签不是请求约束. 成功后填充 stats, 另启 goroutine 执行价格补充, URL 延迟探测和自动分组, 最长使用 5 分钟上下文. 响应不代表这些辅助操作已经成功. keys 和 stats 等实体属性未被请求 DTO 剔除, 实际关联写入效果由 GORM 和数据库决定. RequireJSON 失败返回 415. 源码: internal/server/handlers/channel.go:73-98, internal/server/handlers/param_override.go:15-24, internal/op/channel.go:29-40.")
op createChannel(
  @header contentType: "application/json",

  @doc("渠道实体 JSON. 所有实体属性在解析层都可省略. 非指针标量的 JSON null 可能保留 Go 零值, 不作为规范标量类型扩展. nil 切片和指针允许 null. enabled 的 GORM default:true 不被建模为 HTTP 默认值, 不能仅凭请求省略或 false 承诺最终持久化值.")
  @body
  body: ChannelInput,
):
  | HttpResponse<
      200,
      Success<Channel>,
      "已创建渠道, 含主动填充的 stats 和未脱敏 keys. 辅助任务仍可能执行中.",
      "application/json"
    >
  | HttpResponse<
      400,
      ErrorResponse,
      "缺少 Authorization, JSON 绑定失败, 或 param_override 字符串无法反序列化为 map. 对应 message 为 Invalid request parameters, Invalid JSON format 或 param_override must be a valid JSON object.",
      "application/json"
    >
  | Unauthorized401
  | InvalidJSON415
  | HttpResponse<
      500,
      ErrorResponse,
      "ChannelCreate 返回的数据库或关联写入错误直接作为 message. 全局 recovery 也可能返回 500. 不存在独立的 HTTP 409 分支.",
      "application/json"
    >;

@route("/api/v1/channel/update")
@post
@operationId("updateChannel")
@tag("Channel")
@summary("增量更新渠道及密钥")
@doc("继承根级 JWTAuth. Auth 先于 RequireJSON 执行. 顶层 id 有 binding required, 表达非零而不是正数. 可选指针属性仅在非 nil 时更新, 省略和 JSON null 都不更新, false, 0, 空字符串或空数组可显式提交. 因而 param_override:null, channel_proxy:null 和 match_regex:null 不能清成数据库 NULL. 与 create 相同地手动校验非空 param_override 字符串, 但不校验 match_regex, 协议或 auto_group 枚举范围. key 删除和更新通过 channel_id 限定所属渠道, 不检查 RowsAffected, 无匹配项可能无操作而成功. key 更新只写非 nil 指针, 可显式将 channel_key 设为空字符串. 密钥操作切片没有 binding dive, 元素 required 标签是否由项目准确版本 validator 执行未确认, op 没有补充逐项 required 校验. 事务提交后刷新缓存, 返回含 stats 的渠道, 再启动与 create 相同的价格, 延迟和自动分组 goroutine. 提交后的缓存刷新错误或后台失败不等于数据库未修改. RequireJSON 失败返回 415. 源码: internal/server/handlers/channel.go:100-126, internal/model/channel.go:61-92, internal/op/channel.go:110-248.")
op updateChannel(
  @header contentType: "application/json",

  @doc("增量 DTO. 顶层 id 必须提供且非零. 切片操作省略, null 或空数组均不执行对应操作. 不将嵌套 required 标签建模为已证实执行的约束.")
  @body
  body: ChannelUpdateRequest,
):
  | HttpResponse<
      200,
      Success<Channel>,
      "刷新后的渠道, 含 stats 和未脱敏 keys. 后台辅助处理不由本响应确认成功.",
      "application/json"
    >
  | HttpResponse<
      400,
      ErrorResponse,
      "缺少 Authorization, JSON 绑定或顶层 id required 失败, 或 param_override 格式校验失败. 绑定失败统一返回 Invalid JSON format, 参数覆盖失败返回 param_override must be a valid JSON object.",
      "application/json"
    >
  | Unauthorized401
  | InvalidJSON415
  | HttpResponse<
      500,
      ErrorResponse,
      "渠道不在缓存时返回 channel not found. 数据库事务, 密钥操作, 提交或提交后缓存刷新失败返回 op 错误文本. 没有渠道不存在的 404 分支. 全局 recovery 也可能返回 500.",
      "application/json"
    >;

@route("/api/v1/channel/enable")
@post
@operationId("setChannelEnabled")
@tag("Channel")
@summary("设置渠道启用状态")
@doc("继承根级 JWTAuth. Auth 先于 RequireJSON 执行. ShouldBindJSON 解析 id 和 enabled, 两者均无 binding required 或手动正数检查. 省略 enabled 会得到 Go 零值 false, 对有效渠道执行禁用. 省略 id 得到 0, 通常因缓存不存在而返回 500. op 更新数据库 enabled 后更新缓存. RequireJSON 失败返回 415. 成功调用 resp.Success(c, nil), 因 data 的 omitempty 不返回 data:null, 而是省略 data. 源码: internal/server/handlers/channel.go:128-142, internal/op/channel.go:250-261.")
op setChannelEnabled(
  @header contentType: "application/json",

  @doc("JSON 请求体. 属性都可省略, 标量 JSON null 可能保留 Go 零值, 不被建模为规范标量类型.")
  @body
  body: ChannelEnabledRequest,
):
  | HttpResponse<
      200,
      EmptySuccess,
      "启用状态已设置, 响应省略 data.",
      "application/json"
    >
  | HttpResponse<
      400,
      ErrorResponse,
      "缺少 Authorization 时返回 Invalid request parameters. JSON 绑定失败返回 Invalid JSON format. id 或 enabled 省略没有 required 校验分支.",
      "application/json"
    >
  | Unauthorized401
  | InvalidJSON415
  | HttpResponse<
      500,
      ErrorResponse,
      "渠道缓存不存在时返回 channel not found, 数据库更新失败返回其错误文本. 全局 recovery 也可能返回 500.",
      "application/json"
    >;

@route("/api/v1/channel/delete/{id}")
@delete
@operationId("deleteChannel")
@tag("Channel")
@summary("删除渠道及关联数据")
@doc("继承根级 JWTAuth. RequireJSON 对 DELETE 直接放行, 不要求 Content-Type, 不解析 body. id 使用 strconv.Atoi, 没有正数检查. op 要求渠道在缓存中存在, 在事务中删除引用该渠道的 GroupItem, 渠道 keys, StatsChannel 和渠道, 提交后清理缓存并刷新受影响分组. 受影响分组缓存刷新失败只记日志, 不改变已返回的 op 成功. 成功省略 data, 不是 data:null. 源码: internal/server/handlers/channel.go:144-155, internal/op/channel.go:263-334.")
@extension(
  "parameters",
  #[
    #{
      name: "id",
      in: "path",
      required: true,
      description: "渠道 ID. 路径文本必须能转换为 Go int, 没有正数范围校验. 零或负值通常在缓存查找后返回 500.",
      schema: #{ type: "integer" },
    }
  ]
)
op deleteChannel(
  @doc("渠道 ID. 路径文本必须能转换为 Go int, 没有正数范围校验. 零或负值通常在缓存查找后返回 500.")
  @path("id")
  id: integer,
):
  | HttpResponse<
      200,
      EmptySuccess,
      "通常删除渠道及关联数据后省略 data. op 内部 recover 可能回滚事务后仍返回 nil error, 此异常分支也返回成功包装.",
      "application/json"
    >
  | HttpResponse<
      400,
      ErrorResponse,
      "缺少 Authorization 时返回 Invalid request parameters. id 无法被 strconv.Atoi 转换时返回 Invalid parameter.",
      "application/json"
    >
  | Unauthorized401
  | HttpResponse<
      500,
      ErrorResponse,
      "渠道不存在时返回 channel not found. 查询关联分组, 删除关联项, keys, 统计或渠道, 以及事务提交失败返回 op 错误文本. 没有不存在时的 404 分支. 全局 recovery 也可能返回 500.",
      "application/json"
    >;

@route("/api/v1/channel/fetch-model")
@post
@operationId("fetchChannelModels")
@tag("Channel")
@summary("从渠道上游探测模型列表")
@doc("继承根级 JWTAuth. Auth 先于 RequireJSON 执行. 直接绑定 Channel 实体, 属性无 binding required, 不查找已保存渠道, 也不执行 create/update 的 param_override 校验. helper 按代理配置创建 HTTP client, 从 base_urls 选择非空且 delay 最低的地址, 从已启用且非空的 key 中按成本和可选 Key 熔断规则选择 key. Anthropic 和 Gemini 使用各自模型接口和分页, 空结果可回退 OpenAI, 其他协议包括未知值走默认 OpenAI 分支, Doubao 使用 v3 路径. 非空 match_regex 在获取后编译并过滤模型. 远端响应直接 JSON decode, 不按上游 HTTP 状态码判断失败, 所以成功包装不保证上游 HTTP 成功. 不对 channel.enabled 做检查. 此操作会访问远端, 不持久化传入渠道. RequireJSON 失败返回 415. 源码: internal/server/handlers/channel.go:157-169, internal/helper/fetch.go:16-199, internal/model/channel.go:95-178.")
op fetchChannelModels(
  @header contentType: "application/json",

  @doc("用于即时模型探测的渠道 JSON. 解析属性均可省略, 不代表缺少 URL, key 或代理仍能正常访问远端. 不使用已保存渠道 ID 自动补全配置.")
  @body
  body: ChannelInput,
):
  | HttpResponse<
      200,
      Success<string[]>,
      "模型名称数组. 当前 helper 的正常成功路径使用非 nil 切片, 无结果返回空数组. 不保证已经验证上游 HTTP 状态码.",
      "application/json"
    >
  | HttpResponse<
      400,
      ErrorResponse,
      "缺少 Authorization 时返回 Invalid request parameters. ShouldBindJSON 失败返回 Invalid JSON format.",
      "application/json"
    >
  | Unauthorized401
  | InvalidJSON415
  | HttpResponse<
      500,
      ErrorResponse,
      "HTTP client 或代理构造, 远端请求, 远端 JSON 解析, match_regex 编译或匹配失败返回 helper 错误文本. 上游 HTTP 状态本身不会被直接映射为此接口的同码错误. 全局 recovery 也可能返回 500.",
      "application/json"
    >;

@route("/api/v1/channel/test-model")
@post
@operationId("testChannelModel")
@tag("Channel")
@summary("对渠道单个模型执行即时调用探测")
@doc("继承根级 JWTAuth. Auth 先于 RequireJSON 执行. model 有 binding required. 来源规则按实际 hasID 计算: channel_id 非 null 且大于 0 才是有效 ID 来源, channel 非 null 才是临时对象来源. 必须恰有一个有效来源. 正 channel_id 与非 null channel 同时提供会失败, 但 channel_id 为 0 或负数并带 channel 会走临时渠道, 不能虚构为按属性是否出现的严格互斥. ID 来源从缓存读取渠道, 任何读取错误都映射为 404. key_index 是 keys 数组下标, 默认 0, 必须非负且小于长度. 请求 model 不做 trim, 必须精确属于渠道 model 与 custom_model 经拆分, trim, 去空后的集合. helper 还要求有效 base URL 和 trim 后非空的所选 key, 拒绝 embedding 和未支持的协议. 不检查渠道 enabled 或所选 key enabled. 固定 prompt 为 translate `octopus` to Español, 固定最大输出 token 为 1024, 不使用传入 param_override, 但会应用 custom_header. 不走 relay 的重试, 熔断或统计, 结果不持久化. timeout_ms 非正值使用 30000ms, 正数转为 duration 后大于 5 分钟则钳制为 300000ms, 无转换前整数上限校验, 极大值存在 duration 乘法溢出风险. HTTP client 可执行默认重定向, 本接口的单次探测不等于保证只有一个网络 HTTP 请求. 网络, DNS, 超时或上游错误通常仍以 HTTP 200 返回 TestModelResult. RequireJSON 失败返回 415. 源码: internal/server/handlers/channel_model_probe.go:32-110, internal/helper/test_model.go:20-117,120-210.")
op testChannelModel(
  @header contentType: "application/json",

  @doc("即时探测请求. 来源条件完整记录在 schema description, 不使用会错误拒绝 0/负 channel_id 与临时对象组合的属性级 oneOf.")
  @body
  body: ChannelTestModelRequest,
):
  | HttpResponse<
      200,
      Success<TestModelResult>,
      "探测已生成结果, 不等于模型调用成功. 网络错误或超时通常 status_code 为 0, 上游响应码存于 data.status_code, 不直接用作管理接口 HTTP 状态.",
      "application/json"
    >
  | HttpResponse<
      400,
      ErrorResponse,
      "缺少 Authorization, JSON/model required 绑定失败, 有效来源不满足恰有一个, key_index 越界, model 不在所选集合, 空 base URL, 空 key, embedding 或不支持的协议, 请求构造或 HTTP client 构造错误. 绑定失败返回 Invalid JSON format, 其他错误返回具体手动校验或 helper 文本.",
      "application/json"
    >
  | Unauthorized401
  | HttpResponse<
      404,
      ErrorResponse,
      "有效正 channel_id 的 ChannelGet 返回任何错误, message 统一为 channel not found.",
      "application/json"
    >
  | InvalidJSON415
  | Internal500;

@route("/api/v1/channel/sync")
@post
@operationId("syncChannelModels")
@tag("Channel")
@summary("同步渠道模型并更新关联记录")
@doc("继承根级 JWTAuth. 此路由没有 RequireJSON, 不要求 Content-Type 或 body, handler 不解析请求数据. handler 同步调用 SyncModelsTask, 不是后台排队, task 使用独立于请求的最长 30 分钟后台上下文. 仅处理 auto_sync=true 的渠道, 没有 enabled 过滤, 会访问远端模型接口, 更新 Channel.Model, 删除消失模型关联的 GroupItem, 执行自动分组并更新价格记录. task 不返回 error, 内部错误主要记录日志或跳过, 因此 HTTP 200 不证明所有渠道同步成功. 调用完成后 resp.Success(c, nil) 省略 data. 源码: internal/server/handlers/channel.go:48-57,171-174, internal/task/sync.go:19-107.")
op syncChannelModels():
  | HttpResponse<
      200,
      EmptySuccess,
      "同步函数已返回, 包括内部错误仅记日志或提前返回的情况. 响应省略 data, 不提供逐渠道结果.",
      "application/json"
    >
  | MissingAuth400
  | Unauthorized401
  | Internal500;

@route("/api/v1/channel/last-sync-time")
@get
@operationId("getChannelLastSyncTime")
@tag("Channel")
@summary("获取模型同步时间标记")
@doc("继承根级 JWTAuth. 不使用 RequireJSON, 不解析 body 或 query. 返回进程内 lastSyncModelsTime. 初值是包初始化时的 time.Now(), 不是从数据库读取, 也不是从未同步时的 null. SyncModelsTask 到达末尾时更新该值, 不表示所有渠道成功; 提前返回时可能不更新. 源码: internal/server/handlers/channel.go:176-179, internal/task/sync.go:16,106-110.")
op getChannelLastSyncTime():
  | HttpResponse<
      200,
      Success<GetChannelLastSyncTimeStatus200application_jsonDataValue>,
      "进程内同步时间标记, 由 time.Time 序列化为带时区的时间字符串.",
      "application/json"
    >
  | MissingAuth400
  | Unauthorized401
  | Internal500;

@route("/api/v1/group/list")
@get
@operationId("listGroups")
@tag("Group")
@summary("获取分组列表")
@doc("继承根级 JWTAuth. RequireJSON 对 GET 放行, 不要求 Content-Type, 不解析 body, 分页或筛选参数. 返回 groupCache 的快照, 当前 op 初始化非 nil 切片, 无记录返回空数组, 无排序保证. items 带 omitempty, 空或 nil 时省略, 不保证每项含 items. handler 保留 op 错误的 500 分支, 但当前 GroupList 不返回 error. 源码: internal/server/handlers/group.go:42-49, internal/op/group.go:17-23, internal/model/group.go:12-21.")
op listGroups():
  | HttpResponse<
      200,
      Success<Group[]>,
      "分组数组. 空分组项的 items 属性省略.",
      "application/json"
    >
  | MissingAuth400
  | Unauthorized401
  | Internal500;

@route("/api/v1/group/create")
@post
@operationId("createGroup")
@tag("Group")
@summary("创建分组及分组项")
@doc("继承根级 JWTAuth. Auth 先于 RequireJSON 执行. ShouldBindJSON 直接绑定 Group 实体, 没有 binding required. match_regex 非空时用 regexp2 ECMAScript 模式编译, 失败返回 400. param_override 使用 map 反序列化格式校验, nil, 空白字符串和字符串内容 null 均通过, 不校验参数语义. 没有显式 name 非空, mode 枚举, timeout/session 非负, weight 正数, 渠道存在或模型属于渠道的校验. op 直接 GORM Create 并更新缓存, 不把 GORM not null 和 unique 标签当作 HTTP 请求约束. 包括 custom_header 和 param_override 在内的实际实体字段均可绑定. RequireJSON 失败返回 415. 源码: internal/server/handlers/group.go:51-72, internal/model/group.go:12-31, internal/op/group.go:63-69.")
op createGroup(
  @header contentType: "application/json",

  @doc("分组实体 JSON. 属性在解析层均可省略. 标量 JSON null 可能按 Go 零值宽松解析, 切片和指针显式允许 null. 输入的 items 不复用带响应 required 的 GroupItem.")
  @body
  body: GroupInput,
):
  | HttpResponse<
      200,
      Success<Group>,
      "已创建分组. items 为空时省略, custom_header 的 nil 可为 null, param_override 的 nil 为 null.",
      "application/json"
    >
  | HttpResponse<
      400,
      ErrorResponse,
      "缺少 Authorization, JSON 绑定失败, 非空 match_regex 编译失败, 或 param_override 格式失败. 分组绑定和正则错误返回原始 err.Error(), 参数覆盖失败返回 param_override must be a valid JSON object.",
      "application/json"
    >
  | Unauthorized401
  | InvalidJSON415
  | HttpResponse<
      500,
      ErrorResponse,
      "GroupCreate 的数据库或关联写入错误返回其错误文本. 没有独立的 HTTP 409 约束冲突分支. 全局 recovery 也可能返回 500.",
      "application/json"
    >;

@route("/api/v1/group/update")
@post
@operationId("updateGroup")
@tag("Group")
@summary("增量更新分组及分组项")
@doc("继承根级 JWTAuth. Auth 先于 RequireJSON 执行. 顶层 id 有 binding required, 表达非零而非正数. 可选指针属性省略或 null 不更新, false 之外的适用零值, 空字符串和空数组可显式提交. match_regex 指针非 nil 时编译, 包括空字符串. param_override 使用与 create 相同的格式校验. 没有 mode 枚举, timeout/session 范围, weight 正数, 渠道存在或模型集合校验. items_to_update 中 priority 和 weight 是非指针 int, 漏传会得到 0, op 会同时写入这两个字段, 不是真正的逐字段子项 patch. 子项更新和删除通过 group_id 限定, 未检查 RowsAffected, 无匹配项可能无操作而成功. 子项操作切片没有 binding dive, 元素 required 的准确依赖执行行为未确认, 不加 schema required. 事务提交后刷新缓存并处理名称映射, 刷新失败不代表事务未提交. RequireJSON 失败返回 415. 源码: internal/server/handlers/group.go:74-96, internal/model/group.go:34-60, internal/op/group.go:72-188.")
op updateGroup(
  @header contentType: "application/json",

  @doc("增量 DTO. 顶层 id 必须提供且非零. 指针 null 不更新, 操作切片 null 或空数组不执行操作. 子项 priority/weight 的省略会被零值覆盖.")
  @body
  body: GroupUpdateRequest,
):
  | HttpResponse<
      200,
      Success<UpdateGroupStatus200application_jsonDataValue>,
      "刷新后的分组, 空 items 省略. op 内部 recover 可能回滚后返回有类型的 nil 指针且无错误, 此异常分支的 data 为 null.",
      "application/json"
    >
  | HttpResponse<
      400,
      ErrorResponse,
      "缺少 Authorization, JSON 或顶层 id required 绑定失败, 提供的 match_regex 编译失败, 或 param_override 格式失败. 分组绑定和正则错误返回 err.Error(), 参数覆盖失败返回固定错误文本.",
      "application/json"
    >
  | Unauthorized401
  | InvalidJSON415
  | HttpResponse<
      500,
      ErrorResponse,
      "分组不在缓存时返回 group not found. 更新字段, 子项操作, 事务提交或提交后缓存刷新失败返回 op 错误文本. 没有分组不存在的 404 分支. 全局 recovery 也可能返回 500.",
      "application/json"
    >;

@route("/api/v1/group/delete/{id}")
@delete
@operationId("deleteGroup")
@tag("Group")
@summary("删除分组及分组项")
@doc("继承根级 JWTAuth. RequireJSON 对 DELETE 放行, 不要求 Content-Type 或 body. id 使用 strconv.Atoi, 没有正数检查. op 要求分组在缓存存在, 在事务中删除 GroupItem 和 Group, 提交后删除缓存与名称映射. 成功 data 是固定字符串, 不是空响应. 源码: internal/server/handlers/group.go:98-110, internal/op/group.go:191-220.")
@extension(
  "parameters",
  #[
    #{
      name: "id",
      in: "path",
      required: true,
      description: "分组 ID. 必须能转换为 Go int, 无正数范围约束.",
      schema: #{ type: "integer" },
    }
  ]
)
op deleteGroup(
  @doc("分组 ID. 必须能转换为 Go int, 无正数范围约束.")
  @path("id")
  id: integer,
):
  | HttpResponse<
      200,
      Success<"group deleted successfully">,
      "通常删除分组及关联项后返回成功字符串. op 内部 recover 可能回滚后仍返回 nil error, 200 不保证该异常分支完成删除.",
      "application/json"
    >
  | HttpResponse<
      400,
      ErrorResponse,
      "缺少 Authorization 时返回 Invalid request parameters. id 转换失败时返回 strconv.Atoi 的错误文本, 不是统一 Invalid parameter.",
      "application/json"
    >
  | Unauthorized401
  | HttpResponse<
      500,
      ErrorResponse,
      "分组不存在时返回 group not found. 子项或分组删除, 事务提交失败返回 op 错误文本. 没有不存在时的 404 分支. 全局 recovery 也可能返回 500.",
      "application/json"
    >;

@route("/api/v1/apikey/create")
@post
@operationId("createApiKey")
@tag("APIKey")
@summary("创建 API Key")
@doc("继承根级 JWTAuth. Auth 先于 RequireJSON 执行. ShouldBindJSON 绑定 APIKey 实体, 没有 binding required 或 name 非空, 额度非负, 过期时间范围, supported_models 格式手动校验. handler 用 GenerateAPIKey 覆盖提交的 api_key, 生成格式为 sk- 加 APP_NAME 和 48 个随机字母数字. 随机源错误时生成函数返回空字符串, handler 没有额外检查, 因此不将非空或格式 pattern 作为响应的绝对保证. op 直接 Create 并更新缓存. GORM enabled default:true 不等于已经证实的请求默认值. 返回实际生成的 api_key, 不脱敏. RequireJSON 失败返回 415. 源码: internal/server/handlers/apikey.go:50-62, internal/server/auth/auth.go:48-61, internal/op/apikey.go:15-21.")
op createApiKey(
  @header contentType: "application/json",

  @doc("APIKey 实体 JSON. 所有属性可在解析层省略, api_key 无论是否提供都会被覆盖. 标量 JSON null 可能保留 Go 零值, 不作为规范标量类型扩展.")
  @body
  body: APIKeyInput,
):
  | HttpResponse<
      200,
      Success<APIKey>,
      "创建后的 APIKey, 包含未脱敏的生成密钥. omitempty 属性的零值可能省略.",
      "application/json"
    >
  | HttpResponse<
      400,
      ErrorResponse,
      "缺少 Authorization 时返回 Invalid request parameters. JSON 绑定失败返回 Invalid JSON format, 缺少普通属性不触发 required 错误.",
      "application/json"
    >
  | Unauthorized401
  | InvalidJSON415
  | HttpResponse<
      500,
      ErrorResponse,
      "APIKeyCreate 的数据库写入错误返回 op 包装后的错误文本. 全局 recovery 也可能返回 500.",
      "application/json"
    >;

@route("/api/v1/apikey/list")
@get
@operationId("listApiKeys")
@tag("APIKey")
@summary("获取 API Key 列表")
@doc("继承根级 JWTAuth. RequireJSON 对 GET 放行, 不要求 Content-Type, 不解析 body 或分页参数. 从内存缓存返回 APIKey, 不脱敏 api_key. 当前 op 初始化非 nil 切片, 无记录返回空数组, 没有排序保证. handler 保留 500 错误分支, 当前 APIKeyList 不返回 error. 源码: internal/server/handlers/apikey.go:64-71, internal/op/apikey.go:37-43.")
op listApiKeys():
  | HttpResponse<
      200,
      Success<APIKey[]>,
      "APIKey 数组, 密钥原样输出, 零值 omitempty 属性省略.",
      "application/json"
    >
  | MissingAuth400
  | Unauthorized401
  | Internal500;

@route("/api/v1/apikey/update")
@post
@operationId("updateApiKey")
@tag("APIKey")
@summary("完整替换 API Key 配置并保留密钥")
@doc("继承根级 JWTAuth. Auth 先于 RequireJSON 执行. 直接绑定 APIKey 实体, id 没有 binding required, 不添加 schema required. op 按 id 查缓存, 0, 缺失或不存在通常返回 500 API key not found, 不是 required 400. 使用 Omit(api_key).Save(key), 因此这不是增量更新: name, enabled, expire_at, max_cost 和 supported_models 省略后得到的零值会参与完整保存, 不保留旧值. 提交 api_key 不会改动持久化密钥, op 将原密钥恢复到响应和缓存. 没有字段范围或 supported_models 格式手动校验. RequireJSON 失败返回 415. 源码: internal/server/handlers/apikey.go:73-84, internal/op/apikey.go:24-34.")
op updateApiKey(
  @header contentType: "application/json",

  @doc("完整替换输入, schema 不强制任何属性. 业务上应提供目标 id 和所有希望保留的配置, 但 id 缺失不是 binding 校验错误. 省略或宽松解析为零值的标量可能覆盖原配置, api_key 始终保留原值.")
  @body
  body: APIKeyInput,
):
  | HttpResponse<
      200,
      Success<APIKey>,
      "保存后的 APIKey, api_key 为原密钥, 零值 omitempty 属性省略.",
      "application/json"
    >
  | HttpResponse<
      400,
      ErrorResponse,
      "缺少 Authorization 时返回 Invalid request parameters. JSON 绑定失败返回 Invalid JSON format. id 缺失或 0 不会因 binding required 返回 400.",
      "application/json"
    >
  | Unauthorized401
  | InvalidJSON415
  | HttpResponse<
      500,
      ErrorResponse,
      "缓存未找到目标 id 时返回 API key not found. Save 失败返回 failed to update API key 包装的错误文本. 全局 recovery 也可能返回 500.",
      "application/json"
    >;

@route("/api/v1/apikey/delete/{id}")
@delete
@operationId("deleteApiKey")
@tag("APIKey")
@summary("删除 API Key 及其统计")
@doc("继承根级 JWTAuth. RequireJSON 对 DELETE 放行, 不要求 Content-Type 或 body. id 使用 strconv.Atoi, 无正数校验. op 先调用 StatsAPIKeyDel, 再删除 APIKey, 检查删除 RowsAffected, 最后清理缓存和密钥索引. 这些步骤不在同一事务中, 错误响应不保证此前统计删除没有发生. 成功省略 data, 不是 data:null. 源码: internal/server/handlers/apikey.go:86-98, internal/op/apikey.go:61-79.")
@extension(
  "parameters",
  #[
    #{
      name: "id",
      in: "path",
      required: true,
      description: "API Key ID. 必须能转换为 Go int, 没有正数范围约束.",
      schema: #{ type: "integer" },
    }
  ]
)
op deleteApiKey(
  @doc("API Key ID. 必须能转换为 Go int, 没有正数范围约束.")
  @path("id")
  id: integer,
):
  | HttpResponse<
      200,
      EmptySuccess,
      "APIKey 删除成功, 响应省略 data.",
      "application/json"
    >
  | HttpResponse<
      400,
      ErrorResponse,
      "缺少 Authorization 时返回 Invalid request parameters. id 转换失败返回 Invalid parameter.",
      "application/json"
    >
  | Unauthorized401
  | HttpResponse<
      500,
      ErrorResponse,
      "删除统计失败, APIKey 删除 RowsAffected 为 0 时返回 API key not found, 或数据库删除错误. 没有不存在时的 404 分支. 全局 recovery 也可能返回 500.",
      "application/json"
    >;

@route("/api/v1/apikey/stats")
@get
@operationId("getCurrentApiKeyStats")
@tag("APIKey")
@summary("获取当前 API Key 统计及展示信息")
@doc("覆盖根级 JWTAuth, 接受 x-api-key 或 Authorization API Key. 非空 x-api-key 优先, 否则仅从 Authorization 删除精确 Bearer 前缀, 原始密钥也会被交给验证. 鉴权要求正确 sk-APP_NAME- 前缀, 缓存存在且 enabled. expire_at>0 且小于当前 Unix 秒时拒绝, max_cost>0 且小于 input_cost+output_cost 时拒绝, 等于额度上限仍放行. 这些拒绝均为 401. 不使用 RequireJSON, 不解析 query/body, 当前 ID 取自鉴权上下文. StatsAPIKeyGet 缓存未命中时创建零值统计并标记待持久化. info 含未脱敏 api_key, 但 supported_models 不是原样持久化字段: 原配置为空时显示所有 Group 名称, 非空时按逗号拆分并 trim 后与 Group 名称取精确交集, 再用逗号加空格拼接. 结果为空时该属性因 omitempty 省略. 这里无正则支持, 展示过滤的 trim 不等于 relay 校验也 trim. 源码: internal/server/handlers/apikey.go:100-130, internal/server/middleware/auth.go:32-81, internal/op/stats.go:377-393, internal/relay/relay.go:97-105.")
@useAuth(APIKeyHeader | APIKeyBearer)
op getCurrentApiKeyStats():
  | HttpResponse<
      200,
      Success<APIKeyStatsResponse>,
      "当前密钥统计和展示信息. info.supported_models 经过分组名称过滤改写, 不是原始限制字符串.",
      "application/json"
    >
  | HttpResponse<
      401,
      ErrorResponse,
      "缺少密钥, 前缀错误, 缓存未找到时返回 Authentication failed. 禁用返回 API key is disabled, 过期返回 API key has expired, 超出正额度上限返回 API key has reached the max cost. 没有管理 JWT 缺头的 400 分支.",
      "application/json"
    >
  | HttpResponse<
      500,
      ErrorResponse,
      "鉴权后读取 APIKey 信息失败或 GroupListModel 失败返回对应错误文本, 当前 GroupListModel 实现不返回 error. 全局 recovery 也可能返回 500.",
      "application/json"
    >;

@route("/api/v1/apikey/login")
@get
@operationId("probeApiKeyLogin")
@tag("APIKey")
@summary("探测 API Key 登录可用性")
@doc("覆盖根级 JWTAuth, 接受 x-api-key 或 Authorization API Key, 非空 x-api-key 优先. 不使用 RequireJSON, 不解析 body 或 query. handler 只在 APIKeyAuth 通过后返回空成功包装. 鉴权仍检查前缀, 缓存存在, enabled, 正 expire_at 是否已过期及正 max_cost 是否小于累计 input_cost+output_cost. 等于额度上限仍放行. 不是只检查密钥字符串是否存在. APIKeyAuth 调用 StatsAPIKeyGet, 缓存未命中可建立零值统计并标记待持久化. 成功省略 data, 不是 data:null. 源码: internal/server/handlers/apikey.go:38-47,132-134, internal/server/middleware/auth.go:32-81, internal/op/stats.go:377-393.")
@useAuth(APIKeyHeader | APIKeyBearer)
op probeApiKeyLogin():
  | HttpResponse<
      200,
      EmptySuccess,
      "API Key 鉴权通过, 响应省略 data.",
      "application/json"
    >
  | HttpResponse<
      401,
      ErrorResponse,
      "缺少密钥, 前缀错误, 缓存未找到时返回 Authentication failed. 禁用, 已过期或超过正额度上限分别返回对应 API Key 错误文本. 没有管理 JWT 缺头的 400 分支.",
      "application/json"
    >
  | Internal500;

@route("/api/v1/log/list")
@get
@operationId("listRelayLogs")
@tag("Log")
@summary("分页查询转发日志")
@doc("先按时间条件筛选内存缓存, 再反转缓存追加顺序并分页. 启用 relay_log_keep_enabled 时, 不足部分从数据库按 id DESC 补足; 关闭时不读取数据库历史日志. 不是缓存和数据库整体按 time 排序, 也不是原子快照. 不返回总数或 has_more. 普通越界页返回 data: null. page 没有业务上限, offset 计算未检查整数溢出, 极大值存在触发 recovery 500 的风险. Source: internal/server/handlers/log.go:39-74; internal/op/log.go:273-335.")
@extension(
  "parameters",
  #[
    #{
      name: "page",
      in: "query",
      description: "缺失时默认为 1. 使用 strconv.Atoi 解析, 错误被忽略; 普通非数字输入解析为 0 后回退为 1, 小于 1 也回退为 1. 没有业务最大值, 整数解析范围取决于编译架构. 不应理解为非法输入返回 400.",
      schema: #{ type: "string", default: "1" },
    },
    #{
      name: "page_size",
      in: "query",
      description: "缺失时默认为 20. 使用 strconv.Atoi 解析, 错误被忽略. 小于 1 或大于 100 时回退为 20, 不是钳制到 1 或 100, 也不返回 400.",
      schema: #{ type: "string", default: "20" },
    },
    #{
      name: "start_time",
      in: "query",
      description: "Unix 秒的整数文本. 仅当 start_time 和 end_time 都非空时才解析和启用过滤, 此时任一解析失败返回 400. 只有一端非空时整个过滤被忽略, 不校验已提供值. 闭区间, 不校验非负或起止顺序, Go int 范围取决于编译架构.",
      schema: #{ type: "string" },
    },
    #{
      name: "end_time",
      in: "query",
      description: "Unix 秒的整数文本. 必须与非空 start_time 同时提供才生效. 过滤为 start_time <= time <= end_time, 倒置区间不会被拒绝, 通常返回空结果.",
      schema: #{ type: "string" },
    }
  ]
)
op listRelayLogs(
  @doc("缺失时默认为 1. 使用 strconv.Atoi 解析, 错误被忽略; 普通非数字输入解析为 0 后回退为 1, 小于 1 也回退为 1. 没有业务最大值, 整数解析范围取决于编译架构. 不应理解为非法输入返回 400.")
  @extension("default", "1")
  @query("page")
  page?: string = "1",

  @doc("缺失时默认为 20. 使用 strconv.Atoi 解析, 错误被忽略. 小于 1 或大于 100 时回退为 20, 不是钳制到 1 或 100, 也不返回 400.")
  @extension("default", "20")
  @query("page_size")
  page_size?: string = "20",

  @doc("Unix 秒的整数文本. 仅当 start_time 和 end_time 都非空时才解析和启用过滤, 此时任一解析失败返回 400. 只有一端非空时整个过滤被忽略, 不校验已提供值. 闭区间, 不校验非负或起止顺序, Go int 范围取决于编译架构.")
  @query("start_time")
  start_time?: string,

  @doc("Unix 秒的整数文本. 必须与非空 start_time 同时提供才生效. 过滤为 start_time <= time <= end_time, 倒置区间不会被拒绝, 通常返回空结果.")
  @query("end_time")
  end_time?: string,
):
  | HttpResponse<
      200,
      Success<ListRelayLogsStatus200application_jsonDataValue>,
      "日志列表. 无结果时 data 为 null, 不是省略 data 或固定空数组.",
      "application/json"
    >
  | HttpResponse<
      400,
      ErrorResponse,
      "缺少 Authorization, 或两端时间均非空时任一时间无法解析为 Go int. 分页参数非法或越界本身不返回此状态. Source: internal/server/middleware/auth.go:17-21; internal/server/handlers/log.go:52-63.",
      "application/json"
    >
  | Unauthorized401
  | Internal500;

@route("/api/v1/log/clear")
@delete
@operationId("clearRelayLogs")
@tag("Log")
@summary("清空全部转发日志")
@doc("先删除数据库全部 RelayLog, 成功后清空内存缓存. 即使关闭日志持久化也会删除数据库历史日志. 数据库删除失败返回 500, 不清空缓存. 不重置统计, 不吊销 stream token, 不通知 SSE 订阅者清空事件. 并发新请求仍可产生新日志. Source: internal/server/handlers/log.go:77-82; internal/op/log.go:338-349.")
op clearRelayLogs():
  | HttpResponse<
      200,
      EmptySuccess,
      "清理成功. 响应不含 data 字段, 不是 data: null.",
      "application/json"
    >
  | MissingAuth400
  | Unauthorized401
  | Internal500;

@route("/api/v1/log/stream-token")
@get
@operationId("createLogStreamToken")
@tag("Log")
@summary("申请日志流令牌")
@doc("生成 32 随机字节的 64 位小写十六进制 token, 存入当前进程内存. 没有 TTL, expire_at 或定时过期清理, 不绑定 JWT, 用户或 IP. 未使用令牌一直保留到吊销或进程结束, 原 JWT 过期不使它失效. stream 使用时先校验再吊销, 两步非原子, 并发下不保证严格一次性. Source: internal/server/handlers/log.go:85-91; internal/op/log.go:33-60.")
op createLogStreamToken():
  | HttpResponse<
      200,
      Success<{
        @doc("没有过期时间的进程内令牌.")
        @pattern("^[0-9a-f]{64}$")
        token: string;
      }>,
      "日志流令牌.",
      "application/json"
    >
  | MissingAuth400
  | Unauthorized401
  | Internal500;

@route("/api/v1/log/stream")
@get
@operationId("streamRelayLogs")
@tag("Log")
@summary("订阅实时转发日志")
@doc("由 StreamToken 安全方案表达 query token, 不要求 JWT. 缺失, 未知或已吊销 token 返回 401 JSON. token 没有 TTL, 校验和吊销不是原子消费, 并发连接可能同时通过. 校验后立即吊销, 后续连接失败也不能重用. 每帧为 data: <RelayLog JSON> 后接两个换行, 不使用成功包装. 无初始事件, 心跳, event, id, retry, 回放或断点续传, 只推送订阅后的日志. 每订阅缓冲 10 条, 满时静默丢帧; marshal 失败跳过该帧, Write 错误被忽略, 不发送结构化 SSE 错误. 日志通知为异步且可早于数据库写入, 不保证顺序或可靠持久化. 首条日志前没有主动 Write 或 Flush, 响应头何时被客户端或代理观察到未做运行确认. 客户端 context 结束后退订. Source: internal/server/handlers/log.go:94-129; internal/op/log.go:33-87,137-181.")
@useAuth(StreamToken)
@returnsDoc("持续 SSE 文本流. 每条 data 载荷符合 RelayLog, 不含统一响应包装.")
op streamRelayLogs():
  | {
      ...HttpResponse<
        200,
        StreamRelayLogsStatus200text_event_streamValue,
        "持续 SSE 文本流. 每条 data 载荷符合 RelayLog, 不含统一响应包装.",
        "text/event-stream"
      >;

      @doc("禁用缓存.")
      @header("Cache-Control")
      Cache_Control?: "no-cache";

      @doc("handler 设置的连接头, 实际代理或协议层可能改变它.")
      @header("Connection")
      Connection?: "keep-alive";

      @doc("请求反向代理禁用响应缓冲.")
      @header("X-Accel-Buffering")
      X_Accel_Buffering?: "no";
    }
  | HttpResponse<
      401,
      ErrorResponse,
      "token 缺失, 未知或已吊销. message 为 invalid stream token, 返回 JSON 而非 SSE 帧.",
      "application/json"
    >
  | Internal500;

@route("/api/v1/setting/list")
@get
@operationId("listSettings")
@tag("Setting")
@summary("获取运行期设置列表")
@doc("返回设置内存缓存中的全部项, 包括已加载的自定义 key, 顺序不保证. 空集合为 data: []. 与启动配置分开. 当前 op 实现无业务错误返回, 500 仍可能来自 recovery. Source: internal/server/handlers/setting.go:42-48; internal/op/setting.go:15-23.")
op listSettings():
  | HttpResponse<200, Success<Setting[]>, "设置列表.", "application/json">
  | MissingAuth400
  | Unauthorized401
  | Internal500;

@route("/api/v1/setting/set")
@post
@operationId("setSetting")
@tag("Setting")
@summary("更新单个运行期设置")
@doc("认证后要求 Content-Type 包含大小写敏感子串 application/json, 不是严格 MIME 解析或通用 +json 支持. 绑定 Setting 后进行按 key 的校验, 只更新缓存中已存在的 key, 不创建新 key. 未知 key 通常返回 500 setting not found. two_factor_enabled 无论 value 都返回 403, 必须使用 2FA 专用接口. 先写数据库再更新缓存, 相同值不写数据库. 仅 model_info_update_interval 和 sync_llm_interval 调用 task.Update; stats_save_interval 不热更新任务. task.Update 为尽力更新, 不存在的任务不会创建, 非正间隔删除任务, 非阻塞发送也可能跳过, 接口仍可返回 200. 各默认值和校验见 Setting. Source: internal/server/handlers/setting.go:28-30,51-87; internal/op/setting.go:34-50; internal/task/task.go:54-77.")
op setSetting(
  @header contentType: "application/json",

  @doc("key 和 value 没有 required binding. 遗漏进入零值后再走校验和缓存存在性检查. value 应为字符串形式, 不能提交 JSON 布尔或数值来代替字符串.")
  @body
  body: SettingInput,
):
  | HttpResponse<
      200,
      Success<Setting>,
      "提交的设置对象, value 保持字符串形式.",
      "application/json"
    >
  | HttpResponse<
      400,
      ErrorResponse,
      "缺少 Authorization, JSON 绑定失败或 Setting.Validate 校验失败. 不把未知 key 归为 400. Source: internal/server/handlers/setting.go:53-65.",
      "application/json"
    >
  | Unauthorized401
  | HttpResponse<
      403,
      ErrorResponse,
      "尝试写入 two_factor_enabled. message 为 two factor authentication must be changed via /api/v1/user/2fa.",
      "application/json"
    >
  | InvalidJSON415
  | Internal500;

@route("/api/v1/setting/export")
@get
@operationId("exportDatabase")
@tag("Setting")
@summary("导出数据库数据")
@doc("成功直接返回 DBDump JSON 文件, 不使用统一响应包装. 逐表查询数据库, 不先 flush 日志, 统计或 ChannelKey 运行缓存, 没有导出事务, 不是原子或完整运行态快照. 不包含用户, 密码, 2FA 密钥, 迁移表或启动配置, settings 过滤 two_factor_enabled. Channel 和 Group 查询不 Preload 关联, 密钥和分组项分别位于顶层 channel_keys 和 group_items. 导出内容可能包含明文渠道密钥和 API Key, 应作为敏感备份保管. include_stats 导出的小时表是滚动数据库表, 不是 /stats/hourly 的今天补零结果. Source: internal/server/handlers/setting.go:90-102; internal/op/backup.go:16-76,213-221.")
@extension(
  "parameters",
  #[
    #{
      name: "include_logs",
      in: "query",
      description: "缺失默认 false. strconv.ParseBool 接受 1, t, T, TRUE, true, True 和 0, f, F, FALSE, false, False. 其他文本或空值解析失败后被忽略, 结果为 false, 不返回 400.",
      schema: #{ type: "string", default: "false" },
    },
    #{
      name: "include_stats",
      in: "query",
      description: "缺失默认 false. 接受 strconv.ParseBool 的 1/0, t/f, T/F, TRUE/FALSE, true/false, True/False, 不是任意大小写组合. 非法或空文本回退 false, 不返回 400.",
      schema: #{ type: "string", default: "false" },
    }
  ]
)
@returnsDoc("裸 DBDump JSON 下载. 文件名时间使用本地时间, exported_at 使用 UTC.")
@extension(
  "responses",
  #{
    `200`: #{
      description: "裸 DBDump JSON 下载. 文件名时间使用本地时间, exported_at 使用 UTC.",
      headers: #{
        `Content-Disposition`: #{
          description: "attachment; filename=\"octopus-export-YYYYMMDDhhmmss.json\".",
          schema: #{ type: "string" },
          example: "attachment; filename=\"octopus-export-20260102030405.json\"",
        },
      },
      content: #{
        `application/json`: #{
          schema: #{ $ref: "#/components/schemas/DBDump" },
        },
      },
    },
    `400`: #{
      description: "Authorization 缺失或为空. HTTP 400, message 为 Invalid request parameters.",
      content: #{
        `application/json`: #{
          schema: #{ $ref: "#/components/schemas/ErrorResponse" },
        },
      },
    },
    `401`: #{
      description: "JWT 校验失败, 或 API Key 无效, 禁用, 过期, 累计成本超过正额度上限. 返回 Octopus 错误包装.",
      content: #{
        `application/json`: #{
          schema: #{ $ref: "#/components/schemas/ErrorResponse" },
        },
      },
    },
    `500`: #{
      description: "接口内部错误或全局 panic recovery. 错误原因以 message 为准, 当前实现可能将资源不存在等错误映射为 500.",
      content: #{
        `application/json`: #{
          schema: #{ $ref: "#/components/schemas/ErrorResponse" },
        },
      },
    },
  }
)
op exportDatabase(
  @doc("缺失默认 false. strconv.ParseBool 接受 1, t, T, TRUE, true, True 和 0, f, F, FALSE, false, False. 其他文本或空值解析失败后被忽略, 结果为 false, 不返回 400.")
  @extension("default", "false")
  @query("include_logs")
  include_logs?: string = "false",

  @doc("缺失默认 false. 接受 strconv.ParseBool 的 1/0, t/f, T/F, TRUE/FALSE, true/false, True/False, 不是任意大小写组合. 非法或空文本回退 false, 不返回 400.")
  @extension("default", "false")
  @query("include_stats")
  include_stats?: string = "false",
):
  | {
      ...HttpResponse<
        200,
        DBDump,
        "裸 DBDump JSON 下载. 文件名时间使用本地时间, exported_at 使用 UTC.",
        "application/json"
      >;

      @doc("attachment; filename=\"octopus-export-YYYYMMDDhhmmss.json\".")
      @header("Content-Disposition")
      Content_Disposition?: string;
    }
  | MissingAuth400
  | Unauthorized401
  | Internal500;

@route("/api/v1/setting/import")
@post
@operationId("importDatabase")
@tag("Setting")
@summary("增量导入数据库数据")
@doc("不使用 RequireJSON. Content-Type 大小写敏感地包含 multipart/form-data 时读取文件字段 file, 其他 Content-Type 或缺失头均把原始 body 作为 JSON 解码. application/octet-stream 仅代表其他媒体类型的直接 JSON 路径, 不是限定只接受这里列出的媒体类型. 文件后缀和 part MIME 不做业务校验, handler 使用 io.ReadAll 且没有显式大小限制. 先按裸 DBDump 解码; 仅 version 为 0 且 channels, groups, group_items, settings, api_keys, llm_infos, relay_logs 和全部六种 stats 数组长度都为 0 时尝试 wrapper. 该条件漏查 channel_keys, 也不检查 exported_at 或 include 标志. wrapper 的 code 和 message 不必提供, 不检查 code 为 200; wrapper 自身解码错误被忽略, 只有成功读到 data 才再解到同一个 dump, 未覆盖的顶层字段可能保留. 非零顶层 version 或非空受检查数组会阻止解包, 只兼容一层 wrapper. 例如 code 为字符串可使 data 被忽略而按空 dump 成功. 裸 {}, null 或 data: null 可进行空导入, 未知字段被忽略. anyOf 仅记录兼容形态, 不是严格的解包判定或互斥校验. version 仅允许 0 或 1, 缺失和 null 进入零值; 其他 version 返回 400. 所有表操作在一个事务内, channels, channel_keys, groups, group_items, api_keys 和启用的 relay_logs 按冲突 DoNothing 插入; llm_infos 按 name UpdateAll; settings 按 key 更新 value 并过滤 two_factor_enabled; stats 按各实体主键 UpdateAll, stats_hourly 仅按 hour 而非 date+hour. 缺失或空数组不删除旧数据, 冲突不重映射 ID 或关联. include_logs 和 include_stats 控制顶层可选表操作, 默认 false. 导入不走常规创建校验或 Setting.Validate, 不先保存运行缓存, 也不重调度任务. 提交后 InitCache 的错误被忽略, 仍可能返回 200, 刷新可覆盖未落库增量. Source: internal/server/handlers/setting.go:105-186; internal/op/backup.go:79-221; internal/op/cache.go:9-29.")
@extension(
  "requestBody",
  #{
    required: true,
    description: "裸 JSON, 条件式响应 wrapper 或 multipart 文件. 允许 JSON null, 空 body 则解码失败. 实际媒体类型判断是子串分支而非严格枚举.",
    content: #{
      `application/json`: #{
        schema: #{ $ref: "#/components/schemas/DBDumpImportBody" },
      },
      `application/octet-stream`: #{
        schema: #{ $ref: "#/components/schemas/DBDumpImportBody" },
      },
      `multipart/form-data`: #{
        schema: #{
          type: "object",
          required: #["file"],
          properties: #{
            file: #{
              type: "string",
              format: "binary",
              description: "JSON 文件内容, 支持与直接 body 相同的条件式 wrapper, 不校验后缀或 part MIME.",
            },
          },
        },
      },
    },
  }
)
op importDatabase(
  @header contentType: "application/json" | "application/octet-stream",

  @doc("裸 JSON, 条件式响应 wrapper 或 multipart 文件. 允许 JSON null, 空 body 则解码失败. 实际媒体类型判断是子串分支而非严格枚举.")
  @body
  body: DBDumpImportBody,
):
  | HttpResponse<
      200,
      Success<DBImportResult>,
      "逐表受影响行数, 不是稳定的新增行数. 缓存刷新成功不由该响应保证.",
      "application/json"
    >
  | HttpResponse<
      400,
      ErrorResponse,
      "缺少 Authorization, multipart 缺少或无法读取 file, body 读取或 JSON 解码失败, 不支持的 version, 或数据库导入事务失败. 数据库业务错误在此也映射为 400, 不是 500. multipart 缺少文件分支 message 为 missing upload file field 'file'. Source: internal/server/handlers/setting.go:108-145.",
      "application/json"
    >
  | Unauthorized401
  | Internal500;

@route("/api/v1/circuit/status")
@get
@operationId("getCircuitStatus")
@tag("Circuit")
@summary("查询当前渠道级熔断条目")
@doc("遍历进程内渠道级熔断器, 返回 Open 且冷却未过期的条目. 底层 key 为 channelID:keyID:modelName, 同一渠道可有多条相同名称的结果, 无排序或去重, 响应不含 key 或模型标识. 渠道名称不存在时回退 Channel#<id>. 秒数向下取整, 因而未过期条目也可为 0. 查询没有检查 circuit_breaker_enabled, 关闭开关仍可能展示旧条目. 查询会把冷却到期的 Open 改为 HalfOpen 并隐藏, HalfOpen 也隐藏. 静态风险: IsTripped 只在自己完成 Open 到 HalfOpen 时放行一次, 已经 HalfOpen 时拒绝; 查询提前转换可能在没有其他在途请求改变状态时阻断后续恢复试探. 不能宣称 HalfOpen 必然已允许试探通过. Source: internal/relay/balancer/circuit.go:35-39,111-142,213-280.")
op getCircuitStatus():
  | HttpResponse<
      200,
      Success<CircuitBreakerStatus[]>,
      "当前展示的熔断条目, 无条目为 data: []. 查询本身可改变熔断状态.",
      "application/json"
    >
  | MissingAuth400
  | Unauthorized401
  | Internal500;

@route("/api/v1/circuit/reset")
@post
@operationId("resetCircuitBreakers")
@tag("Circuit")
@summary("重置全部渠道级熔断状态")
@doc("删除 globalBreaker 中的内存状态, 不修改设置, 不清除 ChannelKey.status_code 或 last_use_time_stamp, 因而不重置 Key 级冷却. 不读取请求体, 不要求 application/json, 坏 JSON 本身不被解析. 与并发请求不是原子停机屏障, 可立即产生新状态. Source: internal/server/handlers/circuit.go:33-35; internal/relay/balancer/circuit.go:284-288.")
op resetCircuitBreakers():
  | HttpResponse<
      200,
      EmptySuccess,
      "重置完成. 响应省略 data 字段, 不是 data: null.",
      "application/json"
    >
  | MissingAuth400
  | Unauthorized401
  | Internal500;

@route("/api/v1/stats/today")
@get
@operationId("getStatsToday")
@tag("Stats")
@summary("获取日统计缓存")
@doc("返回 StatsDaily 对象, 包含 date 和平铺指标, 不是只有 StatsMetrics. 日期正常由服务器本地时间生成 YYYYMMDD. getter 不检查跨日, 午夜后若没有新的统计更新或缓存刷新, 可能仍返回前一天日期和数据. 正常无数据初始化为当天零指标对象. Source: internal/server/handlers/stats.go:38-39; internal/op/stats.go:231-246,353-356,436-447.")
op getStatsToday():
  | HttpResponse<
      200,
      Success<StatsDaily>,
      "日统计缓存对象.",
      "application/json"
    >
  | MissingAuth400
  | Unauthorized401
  | Internal500;

@route("/api/v1/stats/total")
@get
@operationId("getStatsTotal")
@tag("Stats")
@summary("获取全局累计统计")
@doc("返回 StatsTotal 内存缓存, 指标平铺并包含大写 JSON 字段 ID. 正常无记录初始化 ID 为 1, 不应把它写成 id 或只返回 StatsMetrics. Source: internal/server/handlers/stats.go:55-56; internal/model/stats.go:13-16; internal/op/stats.go:347-350,449-458.")
op getStatsTotal():
  | HttpResponse<
      200,
      Success<StatsTotal>,
      "全局累计统计对象.",
      "application/json"
    >
  | MissingAuth400
  | Unauthorized401
  | Internal500;

@route("/api/v1/stats/daily")
@get
@operationId("listStatsDaily")
@tag("Stats")
@summary("获取数据库全部日统计")
@doc("直接全表 Find, 无有效分页或时间过滤参数, 无显式排序, 不合并尚未落库的 today 缓存. 数据库查询错误返回 500. 空表时本项目把 nil slice 交给 GORM, 本次未读取对应依赖实现或运行验证, 最终空结果是 [] 还是 null 静态未确认, schema 保守允许两者. Source: internal/server/handlers/stats.go:42-48; internal/op/stats.go:427-433.")
op listStatsDaily():
  | HttpResponse<
      200,
      Success<ListStatsDailyStatus200application_jsonDataValue>,
      "数据库日统计列表. 空结果的 [] 或 null 表现静态未确认.",
      "application/json"
    >
  | MissingAuth400
  | Unauthorized401
  | Internal500;

@route("/api/v1/stats/hourly")
@get
@operationId("listStatsHourly")
@tag("Stats")
@summary("获取今天截至当前小时的统计")
@doc("按服务器本地日期返回从 hour 0 到当前小时的有序数组, 无当日缓存的小时补零. 长度为当前小时加 1, 不是固定 24 项或过去 24 小时. 无请求参数过滤. 与 DBDump.stats_hourly 的滚动数据库表结果不同. Source: internal/server/handlers/stats.go:51-52; internal/op/stats.go:403-424.")
op listStatsHourly():
  | HttpResponse<
      200,
      Success<ListStatsHourlyStatus200application_jsonDataValue>,
      "今天截至当前小时的统计数组.",
      "application/json"
    >
  | MissingAuth400
  | Unauthorized401
  | Internal500;

@route("/api/v1/stats/apikey")
@get
@operationId("listApiKeyStats")
@tag("Stats")
@summary("获取 API Key 维度统计缓存")
@doc("返回统计缓存已有条目, 不是 API Key 列表逐项补零. 无分页, 无排序保证, 空集合确定为 data: []. Source: internal/server/handlers/stats.go:59-60; internal/op/stats.go:395-400.")
op listApiKeyStats():
  | HttpResponse<
      200,
      Success<StatsAPIKey[]>,
      "API Key 统计数组.",
      "application/json"
    >
  | MissingAuth400
  | Unauthorized401
  | Internal500;

@route("/api/v1/update")
@get
@operationId("getLatestUpdateInfo")
@tag("Update")
@summary("获取 GitHub 最新发布信息")
@doc("访问固定仓库 XyzenSun/octopus-customization 的 latest release API, 先直连, 请求或读取错误时再尝试 Setting 代理, 每次 background context 超时 30 秒. 不检查上游 HTTP 状态码. JSON 解析失败或上游 message 非空返回 500, 这些失败不会再次触发代理 fallback, 也不回退当前版本. 不要求 tag_name 或 published_at 非空, 上游 {} 或 null 可形成全空字符串的成功对象. 不缓存或比较版本. Source: internal/server/handlers/update.go:31-37; internal/update/update.go:21-93.")
op getLatestUpdateInfo():
  | HttpResponse<
      200,
      Success<LatestInfo>,
      "LatestInfo, message 字段成功时也存在且为空字符串.",
      "application/json"
    >
  | MissingAuth400
  | Unauthorized401
  | Internal500;

@route("/api/v1/update")
@post
@operationId("updateApplication")
@tag("Update")
@summary("下载并替换程序后尝试重启")
@doc("不读取请求体, 不要求 JSON Content-Type. 根据当前平台同步下载固定 latest ZIP, 解压到当前可执行文件目录, 然后异步 shutdown 和 restart. 不先比较版本, 不验证包中目标二进制, 不做摘要或签名校验, 多文件替换不是原子操作. 有效空 ZIP 也可能进入重启流程. 平台不支持, 下载或解压等错误返回 500, 中途解压失败可能已覆盖部分文件. 成功只表示该路径的下载和解压步骤完成, 不保证新版本启动成功. restart 错误仅写日志. 重启 goroutine 可在 handler 写响应前关闭 HTTP 服务, 因而 200 响应本身也不保证送达. Source: internal/server/handlers/update.go:44-50; internal/update/core.go:15-101; internal/update/update.go:96-158; cmd/start.go:48; internal/server/server.go:53-54.")
op updateApplication():
  | HttpResponse<
      200,
      Success<"update success">,
      "更新步骤成功, 不保证响应送达或重启成功.",
      "application/json"
    >
  | MissingAuth400
  | Unauthorized401
  | Internal500;

@route("/api/v1/update/now-version")
@get
@operationId("getCurrentVersion")
@tag("Update")
@summary("获取当前运行版本字符串")
@doc("返回 conf.Version, 默认值为 dev, 构建可注入其他字符串, 不保证 semver 格式. Source: internal/server/handlers/update.go:40-41; internal/conf/version.go:3-5.")
op getCurrentVersion():
  | HttpResponse<200, Success<string>, "当前版本字符串.", "application/json">
  | MissingAuth400
  | Unauthorized401
  | Internal500;

@route("/api/v1/model/list")
@get
@operationId("listModelPrices")
@tag("Model")
@summary("获取自定义模型价格列表")
@doc("继承根级 JWTAuth. 返回自定义价格缓存中的全部 LLMInfo, 不等同于远端或内置价格库, 也不等同于 /v1/models 的 Group 名称列表. 无分页或排序, 空列表为 []. GET 不要求 Content-Type. 来源: internal/server/handlers/model.go:109-115, internal/op/llm.go:15-23.")
op listModelPrices():
  | HttpResponse<
      200,
      Success<ListModelPricesStatus200application_jsonDataValue>,
      "自定义模型价格列表",
      "application/json"
    >
  | MissingAuth400
  | Unauthorized401
  | Internal500;

@route("/api/v1/model/create")
@post
@operationId("createModelPrice")
@tag("Model")
@summary("创建自定义模型价格")
@doc("继承根级 JWTAuth. RequireJSON 只检查 Content-Type 是否包含小写 application/json, JSON 语法由 ShouldBindJSON 检查. name 和价格没有 required 或非负校验, 省略字段成为 Go 零值. op 创建时把 name 转小写但不 trim, 成功响应仍是原始提交对象, 因此响应 name 可能与保存的名称大小写不同. 重复创建或数据库失败返回 500, 不是 409. 价格按每百万 token 美元计. 来源: internal/server/handlers/model.go:127-137, internal/op/llm.go:59-69.")
op createModelPrice(
  @header contentType: "application/json",

  @doc("自定义价格对象. 可省略字段, 不声明未经执行的必填或价格范围校验.")
  @body
  body: LLMInfoInput,
):
  | HttpResponse<
      200,
      Success<LLMInfo>,
      "创建成功, data 是原始提交对象补上零值后的结果",
      "application/json"
    >
  | HttpResponse<
      400,
      ErrorResponse,
      "JWT 头缺失, JSON 解析或绑定失败",
      "application/json"
    >
  | Unauthorized401
  | InvalidJSON415
  | HttpResponse<500, ErrorResponse, "模型已存在或数据库操作失败等内部错误">;

@route("/api/v1/model/update")
@post
@operationId("updateModelPrice")
@tag("Model")
@summary("更新自定义模型价格")
@doc("继承根级 JWTAuth. RequireJSON 要求 Content-Type 包含小写 application/json. 使用完整 LLMInfo, 不是增量更新. 未提交的价格成为零值并参与 Save. name 没有 binding required, 但业务按精确缓存键查找, 不转小写且不 trim. 不存在的模型或数据库失败返回 500, 不是 404. 不执行价格非负或范围校验. 来源: internal/server/handlers/model.go:140-150, internal/op/llm.go:26-35.")
op updateModelPrice(
  @header contentType: "application/json",
  @body body: LLMInfoInput,
):
  | HttpResponse<200, Success<LLMInfo>, "更新成功", "application/json">
  | HttpResponse<
      400,
      ErrorResponse,
      "JWT 头缺失, JSON 解析或绑定失败",
      "application/json"
    >
  | Unauthorized401
  | InvalidJSON415
  | HttpResponse<
      500,
      ErrorResponse,
      "model not found 或数据库操作失败等内部错误"
    >;

@route("/api/v1/model/delete")
@post
@operationId("deleteModelPrice")
@tag("Model")
@summary("删除自定义模型价格")
@doc("继承根级 JWTAuth. 此路由是 POST, 不是 DELETE. RequireJSON 要求 Content-Type 包含小写 application/json. name 必填且非空, 不 trim 或转小写. 仅删除自定义价格记录及对应缓存, 不删除 Group 或渠道模型配置, 也不保证远端或内置 fallback 价格消失. 不存在的模型返回 500, 成功包装省略 data. 来源: internal/server/handlers/model.go:153-165, internal/op/llm.go:38-47.")
op deleteModelPrice(
  @header contentType: "application/json",
  @body body: ModelPriceDeleteInput,
):
  | HttpResponse<200, EmptySuccess, "删除成功, 不包含 data", "application/json">
  | HttpResponse<
      400,
      ErrorResponse,
      "JWT 头缺失, JSON 解析失败, name 缺失或为空",
      "application/json"
    >
  | Unauthorized401
  | InvalidJSON415
  | HttpResponse<
      500,
      ErrorResponse,
      "model not found 或数据库操作失败等内部错误"
    >;

@route("/api/v1/model/channel")
@get
@operationId("listModelChannels")
@tag("Model")
@summary("获取模型与渠道关联列表")
@doc("继承根级 JWTAuth. 遍历全部渠道缓存, 从 Model 和 CustomModel 按逗号拆分, trim 并去除空项. 包含禁用渠道, 不去重或排序. enabled 表示渠道启用状态, 不表示该模型的实时可用性. 空列表为 []. GET 不要求 Content-Type. 来源: internal/server/handlers/model.go:118-124, internal/op/channel.go:336-352.")
op listModelChannels():
  | HttpResponse<
      200,
      Success<ListModelChannelsStatus200application_jsonDataValue>,
      "模型与渠道关联列表",
      "application/json"
    >
  | MissingAuth400
  | Unauthorized401
  | Internal500;

#suppress "@typespec/http/content-type-ignored" "此接口只检查 Content-Type, handler 不读取 body, 原参数要求通过生成元数据保留."
@route("/api/v1/model/update-price")
@post
@operationId("refreshRemoteModelPrices")
@tag("Model")
@summary("同步更新远端模型价格库")
@doc("继承根级 JWTAuth. 组级 RequireJSON 仍检查 Content-Type, 但 handler 不读取或绑定请求体, body 可为空. 此操作同步拉取 https://models.dev/api.json, 解析并更新所选 provider 的远端价格内存表, 完成后才返回, 不是异步任务. 不更新 /api/v1/model/list 的自定义价格缓存, 自定义价格仍优先于远端或内置价格. 成功更新进程内 lastUpdateTime, 响应省略 data. 网络, 远端状态, 读取或解析失败返回 500. 来源: internal/server/handlers/model.go:168-174, internal/price/price.go:35-100.")
@extension(
  "parameters",
  #[
    #{
      name: "Content-Type",
      in: "header",
      required: true,
      description: "RequireJSON 要求原始字符串包含小写 application/json. 接受 application/json; charset=utf-8. 仅检查请求头, 不要求业务 body.",
      schema: #{ type: "string" },
      example: "application/json",
    }
  ]
)
op refreshRemoteModelPrices(
  @doc("RequireJSON 要求原始字符串包含小写 application/json. 接受 application/json; charset=utf-8. 仅检查请求头, 不要求业务 body.")
  @header("Content-Type")
  Content_Type: string,
):
  | HttpResponse<
      200,
      EmptySuccess,
      "远端价格内存表同步更新成功, 不包含 data",
      "application/json"
    >
  | MissingAuth400
  | Unauthorized401
  | InvalidJSON415
  | Internal500;

@route("/api/v1/model/last-update-time")
@get
@operationId("getRemoteModelPriceUpdateTime")
@tag("Model")
@summary("获取最近远端价格更新时间")
@doc("继承根级 JWTAuth. 返回进程内最近一次远端价格更新成功时间, 不是数据库中自定义价格的修改时间. 尚未成功更新时为 time.Time 零值. GET 不要求 Content-Type. 来源: internal/server/handlers/model.go:177-179, internal/price/price.go:33,79-85.")
op getRemoteModelPriceUpdateTime():
  | HttpResponse<
      200,
      Success<GetRemoteModelPriceUpdateTimeStatus200application_jsonDataValue>,
      "序列化的 time.Time",
      "application/json"
    >
  | MissingAuth400
  | Unauthorized401
  | Internal500;

@route("/v1/models")
@get
@operationId("listCompatibleModels")
@tag("Model")
@summary("获取兼容协议模型列表")
@doc("使用 Octopus API Key. 非空 x-api-key 优先于 Authorization, 两头同时存在时不会在 x-api-key 失败后回退. x-api-key 决定 Anthropic 格式, Authorization 决定 OpenAI 格式, 不提供独立的协议选择请求头. 两种方式均检查 sk-octopus- 前缀, Key 存在, enabled, expire_at 和累计成本, 失败为 401. GET 不要求 Content-Type. 列表来自全部 Group 名称, 不检查可用渠道, 不暴露 channelName/modelName 直连标识, 不提供分页或稳定排序. supported_models 非空时按逗号拆分并 trim 每项后过滤, 但 Relay 访问白名单不 trim, 所以列出的模型未必能通过 Relay 校验. OpenAI 顶层带 success:true. 空 data 为 null, Anthropic 空列表省略 first_id 和 last_id. 成功不使用 SuccessBase 包装, 失败仍用 ErrorResponse. Authorization 通常使用 Bearer <key>, 实现也接受直接 key, 仅剥离精确的 Bearer 加一个空格, 不额外 trim 或读取 Cookie/query 凭证. 来源: internal/server/middleware/auth.go:32-80, internal/server/handlers/model.go:57-106, internal/op/group.go:25-30.")
@useAuth(APIKeyHeader | APIKeyBearer)
op listCompatibleModels():
  | HttpResponse<
      200,
      ListCompatibleModelsStatus200application_jsonValue,
      "由鉴权头决定的兼容模型列表",
      "application/json"
    >
  | Unauthorized401
  | Internal500;

@route("/v1/chat/completions")
@post
@operationId("relayChatCompletions")
@tag("Relay")
@summary("转发 OpenAI Chat Completions 请求")
@doc("入站及客户端响应协议固定为 OpenAI Chat Completions, 与鉴权头无关. 非空 x-api-key 优先于 Authorization, Key 必须属于 Octopus 且启用, 未过期, 累计成本未超过 max_cost. RequireJSON 只检查 Content-Type 包含小写 application/json, 解析后要求非空 model 和非空 messages. 可选择 openai/chat_completions, openai/responses, anthropic/messages, gemini/contents 或 doubao 出站, 普通转换不承诺全部官方或扩展字段无损. stream:true 使用 SSE, 首事件超时只应用于配置了正 first_token_time_out 的 Group, 已写出响应后不重试或追加 JSON 错误. 完整 model 优先精确匹配 Group, Group 存在但无启用候选时返回 503 且不回退. Group 不存在且 model 包含 / 时按第一个 / 解析 channelName/modelName, 严格区分大小写且选路不 trim, 只使用渠道 Model/CustomModel 当前快照. supported_models 非空时禁止实际直连, 正常 Group 白名单按逗号拆分但不 trim, 与 /v1/models 的 trim 行为不同. 直连仅尝试指定渠道一次, 不使用负载均衡, 故障转移, sticky, Group 首事件超时或 Group 请求选项, 仍检查渠道启用, 可用 Key, 有效 BaseURL 和熔断. 分组请求选项先应用, 渠道后覆盖, JSON 顶层 null 删除参数, 自定义 Header 可覆盖或删除认证头. 协议直通另需 passthrough_enabled, 入站与渠道 Type 完全相同, 无适用请求选项且 endpoint 有效, 仍先解析和选路, 可改写 JSON model, 不等同于指定渠道直连. 非流式直通保留原始成功 body, status 和 Header. 流式直通保留解析后的 data 和 event/id, 由 Octopus 重新编码 SSE 并使用本地响应头与 200 状态, 不保证原始字节或全部上游 Header. endpoint 直接拼 BaseURL, 不做普通版本规范化. 本地错误及分组全部失败返回 ErrorResponse, 分组最终失败为 502. 仅指定渠道直连的可识别上游 HTTP 错误保留状态并转换为 OpenAIProtocolError, 无上游 HTTP 状态的真实尝试失败为 502, 不可用或跳过为 503. 流已提交后的错误及客户端取消不保证另有错误 JSON. Group 路由按 mode 排序候选并在配置允许时让可用 sticky 渠道优先, 尚未写出响应时可顺次尝试或故障转移. 尝试结果记录于 RelayLog.Attempts 并统计, 日志持久化受设置影响. Authorization 通常使用 Bearer <key>, 实现也接受直接 key, 仅剥离精确的 Bearer 加一个空格, 不额外 trim 或读取 Cookie/query 凭证. 来源: internal/server/server.go:59, internal/relay/relay.go:42-177,287-603, internal/relay/direct_channel.go:26-117, internal/relay/passthrough.go:37-176.")
@useAuth(APIKeyHeader | APIKeyBearer)
@returnsDoc("普通转换的非流式 JSON 或 stream:true 的 SSE, 无管理包装. 非流式直通可能保留其他 JSON, Content-Type 或空 body.")
@extension(
  "responses",
  #{
    `200`: #{
      description: "普通转换的非流式 JSON 或 stream:true 的 SSE, 无管理包装. 非流式直通可能保留其他 JSON, Content-Type 或空 body.",
      content: #{
        `application/json`: #{
          schema: #{
            anyOf: #[
              #{ $ref: "#/components/schemas/OpenAIChatResponse" },
              #{ $ref: "#/components/schemas/PassthroughJSON" }
            ],
          },
        },
        `*/*`: #{ schema: #{ $ref: "#/components/schemas/PassthroughBody" } },
        `text/event-stream`: #{
          schema: #{
            type: "string",
            description: "SSE 文本. data 通常是 Chat Completions chunk JSON, 可含 [DONE]. 不把整段流声明为 JSON 数组.",
          },
        },
      },
      headers: #{
        `Cache-Control`: #{
          description: "SSE 时为 no-cache",
          schema: #{ type: "string" },
        },
        Connection: #{
          description: "SSE 时为 keep-alive",
          schema: #{ type: "string" },
        },
        `X-Accel-Buffering`: #{
          description: "SSE 时为 no",
          schema: #{ type: "string" },
        },
      },
    },
    `400`: #{
      description: "本地入站校验或模型白名单失败, 或指定渠道上游 HTTP 400",
      content: #{
        `application/json`: #{
          schema: #{
            anyOf: #[
              #{ $ref: "#/components/schemas/ErrorResponse" },
              #{ $ref: "#/components/schemas/OpenAIProtocolError" }
            ],
          },
        },
      },
    },
    `401`: #{
      description: "Octopus API Key 缺失, 无效, 禁用, 过期或超成本, 或指定渠道上游 HTTP 401",
      content: #{
        `application/json`: #{
          schema: #{
            anyOf: #[
              #{ $ref: "#/components/schemas/ErrorResponse" },
              #{ $ref: "#/components/schemas/OpenAIProtocolError" }
            ],
          },
        },
      },
    },
    `404`: #{
      description: "本地模型或直连渠道不存在, 或指定渠道上游 HTTP 404",
      content: #{
        `application/json`: #{
          schema: #{
            anyOf: #[
              #{ $ref: "#/components/schemas/ErrorResponse" },
              #{ $ref: "#/components/schemas/OpenAIProtocolError" }
            ],
          },
        },
      },
    },
    `415`: #{
      description: "RequireJSON 拒绝 Content-Type, 或指定渠道上游 HTTP 415",
      content: #{
        `application/json`: #{
          schema: #{
            anyOf: #[
              #{ $ref: "#/components/schemas/ErrorResponse" },
              #{ $ref: "#/components/schemas/OpenAIProtocolError" }
            ],
          },
        },
      },
    },
    `500`: #{
      description: "本地请求体读取, 解压, 非校验转换或 recovery 失败, 或指定渠道上游 HTTP 500",
      content: #{
        `application/json`: #{
          schema: #{
            anyOf: #[
              #{ $ref: "#/components/schemas/ErrorResponse" },
              #{ $ref: "#/components/schemas/OpenAIProtocolError" }
            ],
          },
        },
      },
    },
    `502`: #{
      description: "分组最终失败, 直连网络或转换等真实尝试失败, 或指定渠道上游 HTTP 502",
      content: #{
        `application/json`: #{
          schema: #{
            anyOf: #[
              #{ $ref: "#/components/schemas/ErrorResponse" },
              #{ $ref: "#/components/schemas/OpenAIProtocolError" }
            ],
          },
        },
      },
    },
    `503`: #{
      description: "无启用候选, 直连渠道禁用, 无可用 Key, 无有效 BaseURL, 不兼容或熔断, 或指定渠道上游 HTTP 503",
      content: #{
        `application/json`: #{
          schema: #{
            anyOf: #[
              #{ $ref: "#/components/schemas/ErrorResponse" },
              #{ $ref: "#/components/schemas/OpenAIProtocolError" }
            ],
          },
        },
      },
    },
    default: #{
      description: "其他上游状态. 不一概表示错误: 协议直通可保留非 200 成功状态, body 和 Content-Type. 指定渠道可返回其他上游 HTTP 错误状态及转换后的协议错误体. 普通转换通常生成 200. SSE 提交后的流错误不按此另发响应.",
      content: #{
        `application/json`: #{
          schema: #{
            anyOf: #[
              #{ $ref: "#/components/schemas/OpenAIChatResponse" },
              #{ $ref: "#/components/schemas/PassthroughJSON" },
              #{ $ref: "#/components/schemas/ErrorResponse" },
              #{ $ref: "#/components/schemas/OpenAIProtocolError" }
            ],
          },
        },
        `*/*`: #{
          schema: #{
            description: "协议直通可能保留未建模的成功 body, 包括空 body 或非 JSON 内容.",
          },
        },
      },
    },
  }
)
op relayChatCompletions(
  @header contentType: "application/json",
  @body body: OpenAIChatRequest,
):
  | {
      ...HttpResponse<
        200,
        RelayChatCompletionsStatus200application_jsonValue,
        "普通转换的非流式 JSON 或 stream:true 的 SSE, 无管理包装. 非流式直通可能保留其他 JSON, Content-Type 或空 body.",
        "application/json"
      >;

      @doc("SSE 时为 no-cache")
      @header("Cache-Control")
      Cache_Control?: string;

      @doc("SSE 时为 keep-alive")
      @header("Connection")
      Connection?: string;

      @doc("SSE 时为 no")
      @header("X-Accel-Buffering")
      X_Accel_Buffering?: string;
    }
  | {
      ...HttpResponse<
        200,
        PassthroughBody,
        "普通转换的非流式 JSON 或 stream:true 的 SSE, 无管理包装. 非流式直通可能保留其他 JSON, Content-Type 或空 body.",
        "*/*"
      >;

      @doc("SSE 时为 no-cache")
      @header("Cache-Control")
      Cache_Control?: string;

      @doc("SSE 时为 keep-alive")
      @header("Connection")
      Connection?: string;

      @doc("SSE 时为 no")
      @header("X-Accel-Buffering")
      X_Accel_Buffering?: string;
    }
  | {
      ...HttpResponse<
        200,
        RelayChatCompletionsStatus200text_event_streamValue,
        "普通转换的非流式 JSON 或 stream:true 的 SSE, 无管理包装. 非流式直通可能保留其他 JSON, Content-Type 或空 body.",
        "text/event-stream"
      >;

      @doc("SSE 时为 no-cache")
      @header("Cache-Control")
      Cache_Control?: string;

      @doc("SSE 时为 keep-alive")
      @header("Connection")
      Connection?: string;

      @doc("SSE 时为 no")
      @header("X-Accel-Buffering")
      X_Accel_Buffering?: string;
    }
  | HttpResponse<
      400,
      RelayChatCompletionsStatus400application_jsonValue,
      "本地入站校验或模型白名单失败, 或指定渠道上游 HTTP 400",
      "application/json"
    >
  | HttpResponse<
      401,
      RelayChatCompletionsStatus400application_jsonValue,
      "Octopus API Key 缺失, 无效, 禁用, 过期或超成本, 或指定渠道上游 HTTP 401",
      "application/json"
    >
  | HttpResponse<
      404,
      RelayChatCompletionsStatus400application_jsonValue,
      "本地模型或直连渠道不存在, 或指定渠道上游 HTTP 404",
      "application/json"
    >
  | HttpResponse<
      415,
      RelayChatCompletionsStatus400application_jsonValue,
      "RequireJSON 拒绝 Content-Type, 或指定渠道上游 HTTP 415",
      "application/json"
    >
  | HttpResponse<
      500,
      RelayChatCompletionsStatus400application_jsonValue,
      "本地请求体读取, 解压, 非校验转换或 recovery 失败, 或指定渠道上游 HTTP 500",
      "application/json"
    >
  | HttpResponse<
      502,
      RelayChatCompletionsStatus400application_jsonValue,
      "分组最终失败, 直连网络或转换等真实尝试失败, 或指定渠道上游 HTTP 502",
      "application/json"
    >
  | HttpResponse<
      503,
      RelayChatCompletionsStatus400application_jsonValue,
      "无启用候选, 直连渠道禁用, 无可用 Key, 无有效 BaseURL, 不兼容或熔断, 或指定渠道上游 HTTP 503",
      "application/json"
    >
  | DefaultHttpResponse<
      RelayChatCompletionsStatusdefaultapplication_jsonValue,
      "其他上游状态. 不一概表示错误: 协议直通可保留非 200 成功状态, body 和 Content-Type. 指定渠道可返回其他上游 HTTP 错误状态及转换后的协议错误体. 普通转换通常生成 200. SSE 提交后的流错误不按此另发响应.",
      "application/json"
    >
  | DefaultHttpResponse<
      RelayChatCompletionsStatusdefault___Value,
      "其他上游状态. 不一概表示错误: 协议直通可保留非 200 成功状态, body 和 Content-Type. 指定渠道可返回其他上游 HTTP 错误状态及转换后的协议错误体. 普通转换通常生成 200. SSE 提交后的流错误不按此另发响应.",
      "*/*"
    >;

@route("/v1/responses")
@post
@operationId("relayResponses")
@tag("Relay")
@summary("转发 OpenAI Responses 请求")
@doc("入站及客户端响应协议固定为 OpenAI Responses, 与鉴权头无关. 非空 x-api-key 优先于 Authorization, Key 必须属于 Octopus 且启用, 未过期, 累计成本未超过 max_cost. RequireJSON 只检查 Content-Type 包含小写 application/json. 解析要求非空 model, input 可省略, 或使用字符串及开放 Item 数组, 不保证最终出站能处理空对话. 可选择 openai/chat_completions, openai/responses, anthropic/messages, gemini/contents 或 doubao 出站. 未知顶层参数在转换路径不保证保留, 部分 Responses 专有 tools, input 和 tool_choice 可经 provider extensions 保留至同协议出站. background 在普通转换路径未映射, previous_response_id 不代表 Octopus 存储会话, 未注册查询, 取消或 compact 路由. stream:true 返回 Responses SSE, Group 正 first_token_time_out 才设置首事件超时, 响应写出后不重试. 完整 model 优先精确匹配 Group, 已存在但无启用候选的 Group 返回 503 且不回退. Group 不存在且 model 含 / 时按第一个 / 拆为 channelName/modelName, 大小写严格且选路不 trim, 只读取渠道当前 Model/CustomModel. supported_models 非空禁止实际直连, Group 白名单按逗号拆分不 trim, 与 /v1/models 的 trim 行为不同. 直连只有一次指定渠道尝试, 无负载均衡, 故障转移, sticky, Group 超时或 Group 请求选项, 仍检查启用, Key, BaseURL 和熔断. Group 请求选项先应用, 渠道后覆盖, JSON 顶层 null 删除参数, Header 可覆盖或删除认证头. 协议直通需开关, 入站与渠道 Type 相同, 无适用请求选项且 endpoint 有效, 仍先解析和选路, 可改 JSON model, 不等同直连. 非流式直通保留 body, status 和 Header. 流式直通保留解析后的 data 和 event/id, 由 Octopus 重新编码 SSE 并使用本地响应头与 200 状态, 不保留原始字节或全部上游 Header. endpoint 直接拼接 BaseURL, 不做普通版本规范化. 本地错误及分组最终失败用 ErrorResponse, 分组最终失败为 502. 只有直连可识别的上游 HTTP 错误保留状态并转为 ResponsesProtocolError, 无 HTTP 状态的真实尝试失败为 502, 跳过或不可用为 503. 已提交 SSE 后的错误和客户端取消不保证另有 JSON. Group 路由按 mode 排序候选并在配置允许时让可用 sticky 渠道优先, 尚未写出响应时可顺次尝试或故障转移. 尝试结果记录于 RelayLog.Attempts 并统计, 日志持久化受设置影响. Authorization 通常使用 Bearer <key>, 实现也接受直接 key, 仅剥离精确的 Bearer 加一个空格, 不额外 trim 或读取 Cookie/query 凭证. 来源: internal/server/server.go:60, internal/relay/relay.go:42-177,287-603, internal/relay/direct_channel.go:26-117, axonhub pin d061ac7df6ae transformer/openai/responses/inbound.go, request_extensions.go.")
@useAuth(APIKeyHeader | APIKeyBearer)
@returnsDoc("普通转换的非流式 Responses JSON 或 SSE, 无管理包装. 非流式直通可能保留其他 JSON, Content-Type 或空 body.")
@extension(
  "responses",
  #{
    `200`: #{
      description: "普通转换的非流式 Responses JSON 或 SSE, 无管理包装. 非流式直通可能保留其他 JSON, Content-Type 或空 body.",
      content: #{
        `application/json`: #{
          schema: #{
            anyOf: #[
              #{ $ref: "#/components/schemas/ResponsesResponse" },
              #{ $ref: "#/components/schemas/PassthroughJSON" }
            ],
          },
        },
        `*/*`: #{ schema: #{ $ref: "#/components/schemas/PassthroughBody" } },
        `text/event-stream`: #{
          schema: #{
            type: "string",
            description: "SSE 文本. 包含 response.created, response.output_text.delta, response.completed 等类型事件, 具体事件取决于转换和上游能力.",
          },
        },
      },
      headers: #{
        `Cache-Control`: #{
          description: "SSE 时为 no-cache",
          schema: #{ type: "string" },
        },
        Connection: #{
          description: "SSE 时为 keep-alive",
          schema: #{ type: "string" },
        },
        `X-Accel-Buffering`: #{
          description: "SSE 时为 no",
          schema: #{ type: "string" },
        },
      },
    },
    `400`: #{
      description: "本地解析, 入站校验或白名单失败, 或指定渠道上游 HTTP 400",
      content: #{
        `application/json`: #{
          schema: #{
            anyOf: #[
              #{ $ref: "#/components/schemas/ErrorResponse" },
              #{ $ref: "#/components/schemas/ResponsesProtocolError" }
            ],
          },
        },
      },
    },
    `401`: #{
      description: "Octopus API Key 缺失, 无效, 禁用, 过期或超成本, 或指定渠道上游 HTTP 401",
      content: #{
        `application/json`: #{
          schema: #{
            anyOf: #[
              #{ $ref: "#/components/schemas/ErrorResponse" },
              #{ $ref: "#/components/schemas/ResponsesProtocolError" }
            ],
          },
        },
      },
    },
    `404`: #{
      description: "本地模型或直连渠道不存在, 或指定渠道上游 HTTP 404",
      content: #{
        `application/json`: #{
          schema: #{
            anyOf: #[
              #{ $ref: "#/components/schemas/ErrorResponse" },
              #{ $ref: "#/components/schemas/ResponsesProtocolError" }
            ],
          },
        },
      },
    },
    `415`: #{
      description: "RequireJSON 拒绝 Content-Type, 或指定渠道上游 HTTP 415",
      content: #{
        `application/json`: #{
          schema: #{
            anyOf: #[
              #{ $ref: "#/components/schemas/ErrorResponse" },
              #{ $ref: "#/components/schemas/ResponsesProtocolError" }
            ],
          },
        },
      },
    },
    `500`: #{
      description: "本地读取, 解压, 非校验转换或 recovery 失败, 或指定渠道上游 HTTP 500",
      content: #{
        `application/json`: #{
          schema: #{
            anyOf: #[
              #{ $ref: "#/components/schemas/ErrorResponse" },
              #{ $ref: "#/components/schemas/ResponsesProtocolError" }
            ],
          },
        },
      },
    },
    `502`: #{
      description: "分组最终失败, 直连网络或转换等真实尝试失败, 或指定渠道上游 HTTP 502",
      content: #{
        `application/json`: #{
          schema: #{
            anyOf: #[
              #{ $ref: "#/components/schemas/ErrorResponse" },
              #{ $ref: "#/components/schemas/ResponsesProtocolError" }
            ],
          },
        },
      },
    },
    `503`: #{
      description: "无启用候选, 直连渠道禁用, 无可用 Key, 无有效 BaseURL, 不兼容或熔断, 或指定渠道上游 HTTP 503",
      content: #{
        `application/json`: #{
          schema: #{
            anyOf: #[
              #{ $ref: "#/components/schemas/ErrorResponse" },
              #{ $ref: "#/components/schemas/ResponsesProtocolError" }
            ],
          },
        },
      },
    },
    default: #{
      description: "其他上游状态, 包括协议直通保留的非 200 成功状态及指定渠道转换后的其他 HTTP 错误. 不一概表示错误. body 和 Content-Type 可能来自直通上游.",
      content: #{
        `application/json`: #{
          schema: #{
            anyOf: #[
              #{ $ref: "#/components/schemas/ResponsesResponse" },
              #{ $ref: "#/components/schemas/PassthroughJSON" },
              #{ $ref: "#/components/schemas/ErrorResponse" },
              #{ $ref: "#/components/schemas/ResponsesProtocolError" }
            ],
          },
        },
        `*/*`: #{
          schema: #{ description: "直通成功可保留空 body 或其他未建模内容." },
        },
      },
    },
  }
)
op relayResponses(
  @header contentType: "application/json",
  @body body: ResponsesRequest,
):
  | {
      ...HttpResponse<
        200,
        RelayResponsesStatus200application_jsonValue,
        "普通转换的非流式 Responses JSON 或 SSE, 无管理包装. 非流式直通可能保留其他 JSON, Content-Type 或空 body.",
        "application/json"
      >;

      @doc("SSE 时为 no-cache")
      @header("Cache-Control")
      Cache_Control?: string;

      @doc("SSE 时为 keep-alive")
      @header("Connection")
      Connection?: string;

      @doc("SSE 时为 no")
      @header("X-Accel-Buffering")
      X_Accel_Buffering?: string;
    }
  | {
      ...HttpResponse<
        200,
        PassthroughBody,
        "普通转换的非流式 Responses JSON 或 SSE, 无管理包装. 非流式直通可能保留其他 JSON, Content-Type 或空 body.",
        "*/*"
      >;

      @doc("SSE 时为 no-cache")
      @header("Cache-Control")
      Cache_Control?: string;

      @doc("SSE 时为 keep-alive")
      @header("Connection")
      Connection?: string;

      @doc("SSE 时为 no")
      @header("X-Accel-Buffering")
      X_Accel_Buffering?: string;
    }
  | {
      ...HttpResponse<
        200,
        RelayResponsesStatus200text_event_streamValue,
        "普通转换的非流式 Responses JSON 或 SSE, 无管理包装. 非流式直通可能保留其他 JSON, Content-Type 或空 body.",
        "text/event-stream"
      >;

      @doc("SSE 时为 no-cache")
      @header("Cache-Control")
      Cache_Control?: string;

      @doc("SSE 时为 keep-alive")
      @header("Connection")
      Connection?: string;

      @doc("SSE 时为 no")
      @header("X-Accel-Buffering")
      X_Accel_Buffering?: string;
    }
  | HttpResponse<
      400,
      RelayResponsesStatus400application_jsonValue,
      "本地解析, 入站校验或白名单失败, 或指定渠道上游 HTTP 400",
      "application/json"
    >
  | HttpResponse<
      401,
      RelayResponsesStatus400application_jsonValue,
      "Octopus API Key 缺失, 无效, 禁用, 过期或超成本, 或指定渠道上游 HTTP 401",
      "application/json"
    >
  | HttpResponse<
      404,
      RelayResponsesStatus400application_jsonValue,
      "本地模型或直连渠道不存在, 或指定渠道上游 HTTP 404",
      "application/json"
    >
  | HttpResponse<
      415,
      RelayResponsesStatus400application_jsonValue,
      "RequireJSON 拒绝 Content-Type, 或指定渠道上游 HTTP 415",
      "application/json"
    >
  | HttpResponse<
      500,
      RelayResponsesStatus400application_jsonValue,
      "本地读取, 解压, 非校验转换或 recovery 失败, 或指定渠道上游 HTTP 500",
      "application/json"
    >
  | HttpResponse<
      502,
      RelayResponsesStatus400application_jsonValue,
      "分组最终失败, 直连网络或转换等真实尝试失败, 或指定渠道上游 HTTP 502",
      "application/json"
    >
  | HttpResponse<
      503,
      RelayResponsesStatus400application_jsonValue,
      "无启用候选, 直连渠道禁用, 无可用 Key, 无有效 BaseURL, 不兼容或熔断, 或指定渠道上游 HTTP 503",
      "application/json"
    >
  | DefaultHttpResponse<
      RelayResponsesStatusdefaultapplication_jsonValue,
      "其他上游状态, 包括协议直通保留的非 200 成功状态及指定渠道转换后的其他 HTTP 错误. 不一概表示错误. body 和 Content-Type 可能来自直通上游.",
      "application/json"
    >
  | DefaultHttpResponse<
      RelayResponsesStatusdefault___Value,
      "其他上游状态, 包括协议直通保留的非 200 成功状态及指定渠道转换后的其他 HTTP 错误. 不一概表示错误. body 和 Content-Type 可能来自直通上游.",
      "*/*"
    >;

@route("/v1/messages")
@post
@operationId("relayAnthropicMessages")
@tag("Relay")
@summary("转发 Anthropic Messages 请求")
@doc("入站及客户端响应协议固定为 Anthropic Messages, 与鉴权头无关. 非空 x-api-key 优先于 Authorization, Key 必须属于 Octopus 且启用, 未过期, 累计成本未超过 max_cost. RequireJSON 只检查 Content-Type 包含小写 application/json. model, messages 和正 max_tokens 在入站执行校验, thinking, system 及 tool_choice 另有明确规则. 可选择 openai/chat_completions, openai/responses, anthropic/messages, gemini/contents 或 doubao 出站, 不保证跨协议所有多模态和原生工具字段无损. Anthropic-Version 入站未强制必填, Anthropic 出站缺省为 2023-06-01, 可受允许合并的入站 Header 或请求选项影响. stream:true 返回 Messages SSE, Group 正 first_token_time_out 才设置首事件超时, 写出后不重试. 完整 model 优先精确命中 Group, 存在但无启用候选时返回 503 且不回退. Group 不存在且 model 含 / 时按第一个 / 解析 channelName/modelName, 选路大小写严格且不 trim, 模型只取渠道 Model/CustomModel 当前快照. supported_models 非空禁止实际直连, Group 白名单按逗号拆分但不 trim, 不同于 /v1/models 的 trim. 直连只尝试指定渠道一次, 不用负载均衡, 故障转移, sticky, Group 超时或 Group 请求选项, 仍检查渠道启用, Key, BaseURL 和熔断. Group 请求选项先应用, 渠道后覆盖, JSON 顶层 null 删除参数, Header 可覆盖或删除认证头. 协议直通需开关, 相同入站和渠道 Type, 无适用请求选项且 endpoint 有效, 仍先解析和选路, 可改 JSON model, 不等同指定渠道直连. 非流式直通保留成功 body, status 和 Header. 流式直通保留解析后的 data 和 event/id, 由 Octopus 重新编码 SSE 并使用本地响应头与 200 状态, 不保留原始字节或全部上游 Header. endpoint 直接拼 BaseURL, 不做普通版本规范化. 本地错误及分组最终失败为 ErrorResponse, 分组最终失败为 502. 仅直连可识别上游 HTTP 错误保留状态并转换为 AnthropicProtocolError, 顶层 type 来自统一错误类型而不固定为 error, 不是完整上游错误头或 body 透传. 无 HTTP 状态的真实尝试失败为 502, 不可用或跳过为 503. 流提交后的错误和客户端取消不保证另有 JSON. Group 路由按 mode 排序候选并在配置允许时让可用 sticky 渠道优先, 尚未写出响应时可顺次尝试或故障转移. 尝试结果记录于 RelayLog.Attempts 并统计, 日志持久化受设置影响. Authorization 通常使用 Bearer <key>, 实现也接受直接 key, 仅剥离精确的 Bearer 加一个空格, 不额外 trim 或读取 Cookie/query 凭证. 来源: internal/server/server.go:61, internal/relay/relay.go:42-177,287-603, internal/relay/direct_channel.go:90-117, axonhub pin d061ac7df6ae transformer/anthropic/inbound.go.")
@useAuth(APIKeyHeader | APIKeyBearer)
@extension(
  "parameters",
  #[
    #{
      name: "Anthropic-Version",
      in: "header",
      required: false,
      description: "可选入站 Header, 本项目没有必填校验. 不选择客户端响应协议. Anthropic 出站通常设置 2023-06-01, 自定义 Header 仍可覆盖或删除.",
      schema: #{ type: "string" },
      example: "2023-06-01",
    },
    #{
      name: "Anthropic-Beta",
      in: "header",
      required: false,
      description: "可选 beta Header. Anthropic 出站允许合并此 Header, 实际功能由上游及转换能力决定.",
      schema: #{ type: "string" },
    }
  ]
)
@returnsDoc("普通转换的非流式 Messages JSON 或 SSE, 无管理包装. 非流式直通可能保留其他 JSON, Content-Type 或空 body.")
@extension(
  "responses",
  #{
    `200`: #{
      description: "普通转换的非流式 Messages JSON 或 SSE, 无管理包装. 非流式直通可能保留其他 JSON, Content-Type 或空 body.",
      content: #{
        `application/json`: #{
          schema: #{
            anyOf: #[
              #{ $ref: "#/components/schemas/AnthropicMessageResponse" },
              #{ $ref: "#/components/schemas/PassthroughJSON" }
            ],
          },
        },
        `*/*`: #{ schema: #{ $ref: "#/components/schemas/PassthroughBody" } },
        `text/event-stream`: #{
          schema: #{
            type: "string",
            description: "SSE 文本. 包含 message_start, content_block_start, content_block_delta, content_block_stop, message_delta 和 message_stop 等类型事件.",
          },
        },
      },
      headers: #{
        `Cache-Control`: #{
          description: "SSE 时为 no-cache",
          schema: #{ type: "string" },
        },
        Connection: #{
          description: "SSE 时为 keep-alive",
          schema: #{ type: "string" },
        },
        `X-Accel-Buffering`: #{
          description: "SSE 时为 no",
          schema: #{ type: "string" },
        },
      },
    },
    `400`: #{
      description: "本地解析, 必填, thinking, system, tool_choice 或白名单校验失败, 或指定渠道上游 HTTP 400",
      content: #{
        `application/json`: #{
          schema: #{
            anyOf: #[
              #{ $ref: "#/components/schemas/ErrorResponse" },
              #{ $ref: "#/components/schemas/AnthropicProtocolError" }
            ],
          },
        },
      },
    },
    `401`: #{
      description: "Octopus API Key 缺失, 无效, 禁用, 过期或超成本, 或指定渠道上游 HTTP 401",
      content: #{
        `application/json`: #{
          schema: #{
            anyOf: #[
              #{ $ref: "#/components/schemas/ErrorResponse" },
              #{ $ref: "#/components/schemas/AnthropicProtocolError" }
            ],
          },
        },
      },
    },
    `404`: #{
      description: "本地模型或直连渠道不存在, 或指定渠道上游 HTTP 404",
      content: #{
        `application/json`: #{
          schema: #{
            anyOf: #[
              #{ $ref: "#/components/schemas/ErrorResponse" },
              #{ $ref: "#/components/schemas/AnthropicProtocolError" }
            ],
          },
        },
      },
    },
    `415`: #{
      description: "RequireJSON 拒绝 Content-Type, 或指定渠道上游 HTTP 415",
      content: #{
        `application/json`: #{
          schema: #{
            anyOf: #[
              #{ $ref: "#/components/schemas/ErrorResponse" },
              #{ $ref: "#/components/schemas/AnthropicProtocolError" }
            ],
          },
        },
      },
    },
    `500`: #{
      description: "本地读取, 解压, 非校验转换或 recovery 失败, 或指定渠道上游 HTTP 500",
      content: #{
        `application/json`: #{
          schema: #{
            anyOf: #[
              #{ $ref: "#/components/schemas/ErrorResponse" },
              #{ $ref: "#/components/schemas/AnthropicProtocolError" }
            ],
          },
        },
      },
    },
    `502`: #{
      description: "分组最终失败, 直连网络或转换等真实尝试失败, 或指定渠道上游 HTTP 502",
      content: #{
        `application/json`: #{
          schema: #{
            anyOf: #[
              #{ $ref: "#/components/schemas/ErrorResponse" },
              #{ $ref: "#/components/schemas/AnthropicProtocolError" }
            ],
          },
        },
      },
    },
    `503`: #{
      description: "无启用候选, 直连渠道禁用, 无可用 Key, 无有效 BaseURL, 不兼容或熔断, 或指定渠道上游 HTTP 503",
      content: #{
        `application/json`: #{
          schema: #{
            anyOf: #[
              #{ $ref: "#/components/schemas/ErrorResponse" },
              #{ $ref: "#/components/schemas/AnthropicProtocolError" }
            ],
          },
        },
      },
    },
    default: #{
      description: "其他上游状态. 包括协议直通保留的非 200 成功状态及指定渠道转换后的其他 HTTP 错误, 不一概表示错误. 直通可保留原始成功 Content-Type 和 body.",
      content: #{
        `application/json`: #{
          schema: #{
            anyOf: #[
              #{ $ref: "#/components/schemas/AnthropicMessageResponse" },
              #{ $ref: "#/components/schemas/PassthroughJSON" },
              #{ $ref: "#/components/schemas/ErrorResponse" },
              #{ $ref: "#/components/schemas/AnthropicProtocolError" }
            ],
          },
        },
        `*/*`: #{
          schema: #{ description: "直通成功可保留空 body 或其他未建模内容." },
        },
      },
    },
  }
)
op relayAnthropicMessages(
  @doc("可选入站 Header, 本项目没有必填校验. 不选择客户端响应协议. Anthropic 出站通常设置 2023-06-01, 自定义 Header 仍可覆盖或删除.")
  @header("Anthropic-Version")
  Anthropic_Version?: string,

  @doc("可选 beta Header. Anthropic 出站允许合并此 Header, 实际功能由上游及转换能力决定.")
  @header("Anthropic-Beta")
  Anthropic_Beta?: string,

  @header contentType: "application/json",
  @body body: AnthropicMessagesRequest,
):
  | {
      ...HttpResponse<
        200,
        RelayAnthropicMessagesStatus200application_jsonValue,
        "普通转换的非流式 Messages JSON 或 SSE, 无管理包装. 非流式直通可能保留其他 JSON, Content-Type 或空 body.",
        "application/json"
      >;

      @doc("SSE 时为 no-cache")
      @header("Cache-Control")
      Cache_Control?: string;

      @doc("SSE 时为 keep-alive")
      @header("Connection")
      Connection?: string;

      @doc("SSE 时为 no")
      @header("X-Accel-Buffering")
      X_Accel_Buffering?: string;
    }
  | {
      ...HttpResponse<
        200,
        PassthroughBody,
        "普通转换的非流式 Messages JSON 或 SSE, 无管理包装. 非流式直通可能保留其他 JSON, Content-Type 或空 body.",
        "*/*"
      >;

      @doc("SSE 时为 no-cache")
      @header("Cache-Control")
      Cache_Control?: string;

      @doc("SSE 时为 keep-alive")
      @header("Connection")
      Connection?: string;

      @doc("SSE 时为 no")
      @header("X-Accel-Buffering")
      X_Accel_Buffering?: string;
    }
  | {
      ...HttpResponse<
        200,
        RelayAnthropicMessagesStatus200text_event_streamValue,
        "普通转换的非流式 Messages JSON 或 SSE, 无管理包装. 非流式直通可能保留其他 JSON, Content-Type 或空 body.",
        "text/event-stream"
      >;

      @doc("SSE 时为 no-cache")
      @header("Cache-Control")
      Cache_Control?: string;

      @doc("SSE 时为 keep-alive")
      @header("Connection")
      Connection?: string;

      @doc("SSE 时为 no")
      @header("X-Accel-Buffering")
      X_Accel_Buffering?: string;
    }
  | HttpResponse<
      400,
      RelayAnthropicMessagesStatus400application_jsonValue,
      "本地解析, 必填, thinking, system, tool_choice 或白名单校验失败, 或指定渠道上游 HTTP 400",
      "application/json"
    >
  | HttpResponse<
      401,
      RelayAnthropicMessagesStatus400application_jsonValue,
      "Octopus API Key 缺失, 无效, 禁用, 过期或超成本, 或指定渠道上游 HTTP 401",
      "application/json"
    >
  | HttpResponse<
      404,
      RelayAnthropicMessagesStatus400application_jsonValue,
      "本地模型或直连渠道不存在, 或指定渠道上游 HTTP 404",
      "application/json"
    >
  | HttpResponse<
      415,
      RelayAnthropicMessagesStatus400application_jsonValue,
      "RequireJSON 拒绝 Content-Type, 或指定渠道上游 HTTP 415",
      "application/json"
    >
  | HttpResponse<
      500,
      RelayAnthropicMessagesStatus400application_jsonValue,
      "本地读取, 解压, 非校验转换或 recovery 失败, 或指定渠道上游 HTTP 500",
      "application/json"
    >
  | HttpResponse<
      502,
      RelayAnthropicMessagesStatus400application_jsonValue,
      "分组最终失败, 直连网络或转换等真实尝试失败, 或指定渠道上游 HTTP 502",
      "application/json"
    >
  | HttpResponse<
      503,
      RelayAnthropicMessagesStatus400application_jsonValue,
      "无启用候选, 直连渠道禁用, 无可用 Key, 无有效 BaseURL, 不兼容或熔断, 或指定渠道上游 HTTP 503",
      "application/json"
    >
  | DefaultHttpResponse<
      RelayAnthropicMessagesStatusdefaultapplication_jsonValue,
      "其他上游状态. 包括协议直通保留的非 200 成功状态及指定渠道转换后的其他 HTTP 错误, 不一概表示错误. 直通可保留原始成功 Content-Type 和 body.",
      "application/json"
    >
  | DefaultHttpResponse<
      RelayResponsesStatusdefault___Value,
      "其他上游状态. 包括协议直通保留的非 200 成功状态及指定渠道转换后的其他 HTTP 错误, 不一概表示错误. 直通可保留原始成功 Content-Type 和 body.",
      "*/*"
    >;

@route("/v1/embeddings")
@post
@operationId("relayEmbeddings")
@tag("Relay")
@summary("转发 OpenAI Embeddings 请求")
@doc("入站及客户端响应固定为 OpenAI Embeddings, 与鉴权头无关. 非空 x-api-key 优先于 Authorization, Key 必须属于 Octopus 且启用, 未过期, 累计成本未超过 max_cost. RequireJSON 只检查 Content-Type 包含小写 application/json. model 必须非空, input 支持 string, string[], token 整数数组或二维 token 数组, 并执行非空校验. 不提供 stream 或正式 SSE 响应路径, 转换路径不识别 stream 字段. 可选择 openai/chat_completions, openai/responses, openai/embeddings, gemini/contents 或 doubao. OpenAI 类渠道实际走 /embeddings, openai/responses 渠道此时也不是 Responses API. Gemini 走 embedContent 或 batchEmbedContents 且仅转换文本, token 数组失败. Doubao 走 /embeddings/multimodal, 当前入站只提供上述文本或 token 类型, 出站只构造文本项, 不承诺 token 输入或多模态兼容. 完整 model 优先精确命中 Group, 存在但无启用候选时 503 且不回退. 不存在且含 / 时按第一个 / 拆 channelName/modelName, 大小写严格且选路不 trim, 只检查渠道 Model/CustomModel 当前快照. supported_models 非空禁止实际直连, Group 白名单按逗号拆分不 trim, 与 /v1/models 的 trim 不同. 直连一次指定渠道尝试, 不用负载均衡, 故障转移, sticky, Group 超时或 Group 请求选项, 仍检查启用, Key, BaseURL 和熔断. Group 请求选项先应用, 渠道后覆盖, 仅 JSON 顶层合并参数, null 删除, Header 可覆盖或删除认证. 协议直通需开关, 相同入站和渠道 Type, 无适用请求选项且 endpoint 有效, 仍先解析和选路, 可改 JSON model, 不等同直连. 直通保留原始成功 body, status 和 Header, endpoint 直接拼 BaseURL, 未做普通版本规范化. 即使原始 body 带 stream 扩展, 也不因此形成正式 SSE 支持. 本地错误及分组最终失败为 ErrorResponse, 分组最终失败为 502. 仅直连可识别的上游 HTTP 错误保留状态并转为 OpenAIProtocolError, 无 HTTP 状态的真实尝试失败为 502, 不可用或跳过为 503. Group 路由按 mode 排序候选并在配置允许时让可用 sticky 渠道优先, 尚未写出响应时可顺次尝试或故障转移. 尝试结果记录于 RelayLog.Attempts 并统计, 日志持久化受设置影响. Authorization 通常使用 Bearer <key>, 实现也接受直接 key, 仅剥离精确的 Bearer 加一个空格, 不额外 trim 或读取 Cookie/query 凭证. 来源: internal/server/server.go:62, internal/relay/relay.go:42-177, internal/relay/transformers.go:49-60, axonhub pin d061ac7df6ae transformer/openai/embedding_inbound.go, transformer/gemini/embedding.go, transformer/doubao/embedding.go.")
@useAuth(APIKeyHeader | APIKeyBearer)
@extension(
  "responses",
  #{
    `200`: #{
      description: "普通转换的非流式 Embeddings JSON, 无管理包装. 非流式直通可能保留其他 JSON, Content-Type 或空 body.",
      content: #{
        `application/json`: #{
          schema: #{
            anyOf: #[
              #{ $ref: "#/components/schemas/OpenAIEmbeddingResponse" },
              #{ $ref: "#/components/schemas/PassthroughJSON" }
            ],
          },
        },
        `*/*`: #{ schema: #{ $ref: "#/components/schemas/PassthroughBody" } },
      },
    },
    `400`: #{
      description: "本地解析, model, input 或白名单校验失败, 或指定渠道上游 HTTP 400",
      content: #{
        `application/json`: #{
          schema: #{
            anyOf: #[
              #{ $ref: "#/components/schemas/ErrorResponse" },
              #{ $ref: "#/components/schemas/OpenAIProtocolError" }
            ],
          },
        },
      },
    },
    `401`: #{
      description: "Octopus API Key 缺失, 无效, 禁用, 过期或超成本, 或指定渠道上游 HTTP 401",
      content: #{
        `application/json`: #{
          schema: #{
            anyOf: #[
              #{ $ref: "#/components/schemas/ErrorResponse" },
              #{ $ref: "#/components/schemas/OpenAIProtocolError" }
            ],
          },
        },
      },
    },
    `404`: #{
      description: "本地模型或直连渠道不存在, 或指定渠道上游 HTTP 404",
      content: #{
        `application/json`: #{
          schema: #{
            anyOf: #[
              #{ $ref: "#/components/schemas/ErrorResponse" },
              #{ $ref: "#/components/schemas/OpenAIProtocolError" }
            ],
          },
        },
      },
    },
    `415`: #{
      description: "RequireJSON 拒绝 Content-Type, 或指定渠道上游 HTTP 415",
      content: #{
        `application/json`: #{
          schema: #{
            anyOf: #[
              #{ $ref: "#/components/schemas/ErrorResponse" },
              #{ $ref: "#/components/schemas/OpenAIProtocolError" }
            ],
          },
        },
      },
    },
    `500`: #{
      description: "本地读取, 解压, 非校验转换或 recovery 失败, 或指定渠道上游 HTTP 500",
      content: #{
        `application/json`: #{
          schema: #{
            anyOf: #[
              #{ $ref: "#/components/schemas/ErrorResponse" },
              #{ $ref: "#/components/schemas/OpenAIProtocolError" }
            ],
          },
        },
      },
    },
    `502`: #{
      description: "分组最终失败, 直连网络或转换等真实尝试失败, 或指定渠道上游 HTTP 502",
      content: #{
        `application/json`: #{
          schema: #{
            anyOf: #[
              #{ $ref: "#/components/schemas/ErrorResponse" },
              #{ $ref: "#/components/schemas/OpenAIProtocolError" }
            ],
          },
        },
      },
    },
    `503`: #{
      description: "无启用候选或直连目标不可用, 不兼容, 熔断, 或指定渠道上游 HTTP 503",
      content: #{
        `application/json`: #{
          schema: #{
            anyOf: #[
              #{ $ref: "#/components/schemas/ErrorResponse" },
              #{ $ref: "#/components/schemas/OpenAIProtocolError" }
            ],
          },
        },
      },
    },
    default: #{
      description: "其他上游状态, 包括协议直通保留的非 200 成功状态和指定渠道其他 HTTP 错误, 不一概表示错误. 不据此声明 embedding 支持 SSE.",
      content: #{
        `application/json`: #{
          schema: #{
            anyOf: #[
              #{ $ref: "#/components/schemas/OpenAIEmbeddingResponse" },
              #{ $ref: "#/components/schemas/PassthroughJSON" },
              #{ $ref: "#/components/schemas/ErrorResponse" },
              #{ $ref: "#/components/schemas/OpenAIProtocolError" }
            ],
          },
        },
        `*/*`: #{
          schema: #{
            description: "直通成功可以保留原始或空 body 及其他 Content-Type.",
          },
        },
      },
    },
  }
)
op relayEmbeddings(
  @header contentType: "application/json",
  @body body: OpenAIEmbeddingRequest,
):
  | HttpResponse<
      200,
      RelayEmbeddingsStatus200application_jsonValue,
      "普通转换的非流式 Embeddings JSON, 无管理包装. 非流式直通可能保留其他 JSON, Content-Type 或空 body.",
      "application/json"
    >
  | HttpResponse<
      200,
      PassthroughBody,
      "普通转换的非流式 Embeddings JSON, 无管理包装. 非流式直通可能保留其他 JSON, Content-Type 或空 body.",
      "*/*"
    >
  | HttpResponse<
      400,
      RelayChatCompletionsStatus400application_jsonValue,
      "本地解析, model, input 或白名单校验失败, 或指定渠道上游 HTTP 400",
      "application/json"
    >
  | HttpResponse<
      401,
      RelayChatCompletionsStatus400application_jsonValue,
      "Octopus API Key 缺失, 无效, 禁用, 过期或超成本, 或指定渠道上游 HTTP 401",
      "application/json"
    >
  | HttpResponse<
      404,
      RelayChatCompletionsStatus400application_jsonValue,
      "本地模型或直连渠道不存在, 或指定渠道上游 HTTP 404",
      "application/json"
    >
  | HttpResponse<
      415,
      RelayChatCompletionsStatus400application_jsonValue,
      "RequireJSON 拒绝 Content-Type, 或指定渠道上游 HTTP 415",
      "application/json"
    >
  | HttpResponse<
      500,
      RelayChatCompletionsStatus400application_jsonValue,
      "本地读取, 解压, 非校验转换或 recovery 失败, 或指定渠道上游 HTTP 500",
      "application/json"
    >
  | HttpResponse<
      502,
      RelayChatCompletionsStatus400application_jsonValue,
      "分组最终失败, 直连网络或转换等真实尝试失败, 或指定渠道上游 HTTP 502",
      "application/json"
    >
  | HttpResponse<
      503,
      RelayChatCompletionsStatus400application_jsonValue,
      "无启用候选或直连目标不可用, 不兼容, 熔断, 或指定渠道上游 HTTP 503",
      "application/json"
    >
  | DefaultHttpResponse<
      RelayEmbeddingsStatusdefaultapplication_jsonValue,
      "其他上游状态, 包括协议直通保留的非 200 成功状态和指定渠道其他 HTTP 错误, 不一概表示错误. 不据此声明 embedding 支持 SSE.",
      "application/json"
    >
  | DefaultHttpResponse<
      RelayEmbeddingsStatusdefault___Value,
      "其他上游状态, 包括协议直通保留的非 200 成功状态和指定渠道其他 HTTP 错误, 不一概表示错误. 不据此声明 embedding 支持 SSE.",
      "*/*"
    >;

@route("/v1/images/generations")
@post
@operationId("relayImageGenerations")
@tag("Relay")
@summary("转发图片生成请求")
@doc("入站及客户端响应固定为 OpenAI Images 格式, 与鉴权头无关. 非空 x-api-key 优先于 Authorization, Key 必须属于 Octopus 且启用, 未过期, 累计成本未超过 max_cost. RequireJSON 检查 Content-Type 包含小写 application/json. prompt 非空但不 trim, model 缺失, null 或空字符串时默认 dall-e-2, stream:true 在选路前返回 400, 不支持 SSE. 可选择 OpenAI chat/responses/image 类渠道, gemini/contents 或 doubao, Anthropic 和 embedding 类渠道不兼容. openai/responses 渠道在本入口实际使用 OpenAI Images outbound, Gemini 用 generateContent, Doubao 用 images/generations. 参数映射及 response_format 的保留取决于出站和实际模型, 不保证所有字段无损. 完整 model 优先精确匹配 Group, 已存在但无启用候选时 503 且不回退. 不存在且含 / 时按第一个 / 拆 channelName/modelName, 大小写严格且选路不 trim, 仅匹配渠道 Model/CustomModel 当前快照. supported_models 非空禁止实际直连, Group 白名单逗号拆分不 trim, 与 /v1/models 的 trim 不同. 直连仅一次指定渠道尝试, 不使用负载均衡, 故障转移, sticky, Group 超时或 Group 请求选项, 仍检查启用, Key, BaseURL 和熔断. Group 请求选项先应用, 渠道后覆盖, JSON 顶层 null 删除参数, Header 可覆盖或删除认证头. 协议直通需开关, 相同入站和渠道 Type, 无适用请求选项且 endpoint 有效, 仍先解析和选路, 可改 JSON model, 不等同直连. 直通保留成功 body, status 和 Header, 直接拼 BaseURL 与 endpoint, 不做普通版本规范化. 本地错误及分组最终失败为 ErrorResponse, 分组最终失败为 502. 仅直连可识别上游 HTTP 错误保留状态并转为 OpenAIProtocolError, 无 HTTP 状态的真实尝试失败为 502, 不可用或跳过为 503. Group 路由按 mode 排序候选并在配置允许时让可用 sticky 渠道优先, 尚未写出响应时可顺次尝试或故障转移. 尝试结果记录于 RelayLog.Attempts 并统计, 日志持久化受设置影响. Authorization 通常使用 Bearer <key>, 实现也接受直接 key, 仅剥离精确的 Bearer 加一个空格, 不额外 trim 或读取 Cookie/query 凭证. 来源: internal/server/server.go:63, internal/relay/relay.go:42-177, internal/relay/transformers.go:62-76, axonhub pin d061ac7df6ae transformer/openai/image_inbound.go:199-250.")
@useAuth(APIKeyHeader | APIKeyBearer)
@extension(
  "responses",
  #{
    `200`: #{
      description: "普通转换的非流式 Images JSON, 无管理包装. 非流式直通可能保留其他 JSON, Content-Type 或空 body.",
      content: #{
        `application/json`: #{
          schema: #{
            anyOf: #[
              #{ $ref: "#/components/schemas/ImageResponse" },
              #{ $ref: "#/components/schemas/PassthroughJSON" }
            ],
          },
        },
        `*/*`: #{ schema: #{ $ref: "#/components/schemas/PassthroughBody" } },
      },
    },
    `400`: #{
      description: "本地解析, prompt, stream 或白名单校验失败, 或指定渠道上游 HTTP 400",
      content: #{
        `application/json`: #{
          schema: #{
            anyOf: #[
              #{ $ref: "#/components/schemas/ErrorResponse" },
              #{ $ref: "#/components/schemas/OpenAIProtocolError" }
            ],
          },
        },
      },
    },
    `401`: #{
      description: "Octopus API Key 缺失, 无效, 禁用, 过期或超成本, 或指定渠道上游 HTTP 401",
      content: #{
        `application/json`: #{
          schema: #{
            anyOf: #[
              #{ $ref: "#/components/schemas/ErrorResponse" },
              #{ $ref: "#/components/schemas/OpenAIProtocolError" }
            ],
          },
        },
      },
    },
    `404`: #{
      description: "本地模型或直连渠道不存在, 或指定渠道上游 HTTP 404",
      content: #{
        `application/json`: #{
          schema: #{
            anyOf: #[
              #{ $ref: "#/components/schemas/ErrorResponse" },
              #{ $ref: "#/components/schemas/OpenAIProtocolError" }
            ],
          },
        },
      },
    },
    `415`: #{
      description: "RequireJSON 拒绝 Content-Type, 或指定渠道上游 HTTP 415",
      content: #{
        `application/json`: #{
          schema: #{
            anyOf: #[
              #{ $ref: "#/components/schemas/ErrorResponse" },
              #{ $ref: "#/components/schemas/OpenAIProtocolError" }
            ],
          },
        },
      },
    },
    `500`: #{
      description: "本地读取, 解压, 非校验转换或 recovery 失败, 或指定渠道上游 HTTP 500",
      content: #{
        `application/json`: #{
          schema: #{
            anyOf: #[
              #{ $ref: "#/components/schemas/ErrorResponse" },
              #{ $ref: "#/components/schemas/OpenAIProtocolError" }
            ],
          },
        },
      },
    },
    `502`: #{
      description: "分组最终失败, 直连网络或转换等真实尝试失败, 或指定渠道上游 HTTP 502",
      content: #{
        `application/json`: #{
          schema: #{
            anyOf: #[
              #{ $ref: "#/components/schemas/ErrorResponse" },
              #{ $ref: "#/components/schemas/OpenAIProtocolError" }
            ],
          },
        },
      },
    },
    `503`: #{
      description: "无启用候选或直连目标不可用, 不兼容, 熔断, 或指定渠道上游 HTTP 503",
      content: #{
        `application/json`: #{
          schema: #{
            anyOf: #[
              #{ $ref: "#/components/schemas/ErrorResponse" },
              #{ $ref: "#/components/schemas/OpenAIProtocolError" }
            ],
          },
        },
      },
    },
    default: #{
      description: "其他上游状态, 包括协议直通保留的非 200 成功及指定渠道其他 HTTP 错误, 不一概表示错误. Images 不提供 SSE 支持.",
      content: #{
        `application/json`: #{
          schema: #{
            anyOf: #[
              #{ $ref: "#/components/schemas/ImageResponse" },
              #{ $ref: "#/components/schemas/PassthroughJSON" },
              #{ $ref: "#/components/schemas/ErrorResponse" },
              #{ $ref: "#/components/schemas/OpenAIProtocolError" }
            ],
          },
        },
        `*/*`: #{
          schema: #{
            description: "直通成功可以保留原始或空 body 及其他 Content-Type.",
          },
        },
      },
    },
  }
)
op relayImageGenerations(
  @header contentType: "application/json",
  @body body: ImageGenerationRequest,
):
  | HttpResponse<
      200,
      RelayImageGenerationsStatus200application_jsonValue,
      "普通转换的非流式 Images JSON, 无管理包装. 非流式直通可能保留其他 JSON, Content-Type 或空 body.",
      "application/json"
    >
  | HttpResponse<
      200,
      PassthroughBody,
      "普通转换的非流式 Images JSON, 无管理包装. 非流式直通可能保留其他 JSON, Content-Type 或空 body.",
      "*/*"
    >
  | HttpResponse<
      400,
      RelayChatCompletionsStatus400application_jsonValue,
      "本地解析, prompt, stream 或白名单校验失败, 或指定渠道上游 HTTP 400",
      "application/json"
    >
  | HttpResponse<
      401,
      RelayChatCompletionsStatus400application_jsonValue,
      "Octopus API Key 缺失, 无效, 禁用, 过期或超成本, 或指定渠道上游 HTTP 401",
      "application/json"
    >
  | HttpResponse<
      404,
      RelayChatCompletionsStatus400application_jsonValue,
      "本地模型或直连渠道不存在, 或指定渠道上游 HTTP 404",
      "application/json"
    >
  | HttpResponse<
      415,
      RelayChatCompletionsStatus400application_jsonValue,
      "RequireJSON 拒绝 Content-Type, 或指定渠道上游 HTTP 415",
      "application/json"
    >
  | HttpResponse<
      500,
      RelayChatCompletionsStatus400application_jsonValue,
      "本地读取, 解压, 非校验转换或 recovery 失败, 或指定渠道上游 HTTP 500",
      "application/json"
    >
  | HttpResponse<
      502,
      RelayChatCompletionsStatus400application_jsonValue,
      "分组最终失败, 直连网络或转换等真实尝试失败, 或指定渠道上游 HTTP 502",
      "application/json"
    >
  | HttpResponse<
      503,
      RelayChatCompletionsStatus400application_jsonValue,
      "无启用候选或直连目标不可用, 不兼容, 熔断, 或指定渠道上游 HTTP 503",
      "application/json"
    >
  | DefaultHttpResponse<
      RelayImageGenerationsStatusdefaultapplication_jsonValue,
      "其他上游状态, 包括协议直通保留的非 200 成功及指定渠道其他 HTTP 错误, 不一概表示错误. Images 不提供 SSE 支持.",
      "application/json"
    >
  | DefaultHttpResponse<
      RelayEmbeddingsStatusdefault___Value,
      "其他上游状态, 包括协议直通保留的非 200 成功及指定渠道其他 HTTP 错误, 不一概表示错误. Images 不提供 SSE 支持.",
      "*/*"
    >;

@route("/v1/images/edits")
@post
@operationId("relayImageEdits")
@tag("Relay")
@summary("转发 multipart 图片编辑请求")
@doc("入站及响应固定为 OpenAI Images 格式. 非空 x-api-key 优先于 Authorization, Key 必须属于 Octopus 且启用, 未过期, 累计成本未超过 max_cost. 不挂 RequireJSON. 使用带 boundary 的 multipart, 解析器接受 multipart/ 前缀, JSON body 不受支持且通常返回 400, 不是入口 415. 至少提供 image 或 image[] 文件 part, filename 必须非空, prompt trim 后非空. model 先 trim, 缺失或空时默认 dall-e-2. 表单 stream trim 后大小写不敏感等于 true 则 400, 不支持 SSE. 文件 MIME 支持 PNG, JPEG, GIF, WebP, 单文件及普通字段默认 50 MiB, AXONHUB_MAX_IMAGE_FILE_SIZE 可调整. 总 body 固定解析限额 816 MiB, 最多 16 个文件 part, 包括 mask 和未知文件字段. 限额在完整 body 读入后校验, 不是入口读流限额, 超限通常 400 而非 413. 可选择 OpenAI chat/responses/image 类渠道, Gemini 或 Doubao, openai/responses 此时仍用 Images outbound. Gemini 和 Doubao 不完整保留 mask 及其他 OpenAI 参数, 不保证无损. 完整 model 优先精确匹配 Group, 存在但无启用候选时 503 且不回退. 不存在且含 / 时按第一个 / 拆 channelName/modelName, 选路大小写严格, multipart 入站已 trim model, 只匹配渠道 Model/CustomModel 当前快照. supported_models 非空禁止实际直连, Group 白名单按逗号拆分不 trim, 与 /v1/models 的 trim 不同. 直连仅一次指定渠道尝试, 不用负载均衡, 故障转移, sticky, Group 超时或 Group 请求选项, 仍检查启用, Key, BaseURL 和熔断. Group 请求选项先应用, 渠道后覆盖, 参数覆盖仅作用于最终 JSON body, 不能改写 OpenAI multipart body, Header 可覆盖或删除认证头. 协议直通需开关, 相同入站和渠道 Type, 无适用请求选项, endpoint 有效且 multipart 不需要模型改写, 仍先解析和选路, 不等同直连. 直连去前缀或 Group 模型改名通常使 multipart 退回转换. 直通保留成功 body, status 和 Header, endpoint 直接拼 BaseURL 而不做普通版本规范化. 本地错误及分组最终失败为 ErrorResponse, 分组最终失败 502. 仅直连可识别上游 HTTP 错误保留状态并转 OpenAIProtocolError, 无 HTTP 状态的真实尝试失败 502, 不可用或跳过 503. Group 路由按 mode 排序候选并在配置允许时让可用 sticky 渠道优先, 尚未写出响应时可顺次尝试或故障转移. 尝试结果记录于 RelayLog.Attempts 并统计, 日志持久化受设置影响. Authorization 通常使用 Bearer <key>, 实现也接受直接 key, 仅剥离精确的 Bearer 加一个空格, 不额外 trim 或读取 Cookie/query 凭证. 来源: internal/server/server.go:64, internal/relay/passthrough.go:37-54, axonhub pin d061ac7df6ae transformer/openai/image_inbound.go:252-321,389-483.")
@useAuth(APIKeyHeader | APIKeyBearer)
@extension(
  "requestBody",
  #{
    required: true,
    description: "multipart 文件和文本字段. 标准形式为 multipart/form-data; boundary=..., 解析器也接受其他 multipart/ MIME, 必须有有效 boundary.",
    content: #{
      `multipart/form-data`: #{
        schema: #{ $ref: "#/components/schemas/ImageEditRequest" },
        encoding: #{
          image: #{
            contentType: "image/png, image/jpeg, image/gif, image/webp",
          },
          `image[]`: #{
            contentType: "image/png, image/jpeg, image/gif, image/webp",
          },
          mask: #{
            contentType: "image/png, image/jpeg, image/gif, image/webp",
          },
        },
      },
    },
  }
)
@extension(
  "responses",
  #{
    `200`: #{
      description: "普通转换的非流式 Images JSON, 无管理包装. 非流式直通可能保留其他 JSON, Content-Type 或空 body.",
      content: #{
        `application/json`: #{
          schema: #{
            anyOf: #[
              #{ $ref: "#/components/schemas/ImageResponse" },
              #{ $ref: "#/components/schemas/PassthroughJSON" }
            ],
          },
        },
        `*/*`: #{ schema: #{ $ref: "#/components/schemas/PassthroughBody" } },
      },
    },
    `400`: #{
      description: "multipart, 文件, prompt, stream 或白名单校验失败, 或指定渠道上游 HTTP 400",
      content: #{
        `application/json`: #{
          schema: #{
            anyOf: #[
              #{ $ref: "#/components/schemas/ErrorResponse" },
              #{ $ref: "#/components/schemas/OpenAIProtocolError" }
            ],
          },
        },
      },
    },
    `401`: #{
      description: "Octopus API Key 缺失, 无效, 禁用, 过期或超成本, 或指定渠道上游 HTTP 401",
      content: #{
        `application/json`: #{
          schema: #{
            anyOf: #[
              #{ $ref: "#/components/schemas/ErrorResponse" },
              #{ $ref: "#/components/schemas/OpenAIProtocolError" }
            ],
          },
        },
      },
    },
    `404`: #{
      description: "本地模型或直连渠道不存在, 或指定渠道上游 HTTP 404",
      content: #{
        `application/json`: #{
          schema: #{
            anyOf: #[
              #{ $ref: "#/components/schemas/ErrorResponse" },
              #{ $ref: "#/components/schemas/OpenAIProtocolError" }
            ],
          },
        },
      },
    },
    `500`: #{
      description: "本地读取, 解压, 非校验转换或 recovery 失败, 或指定渠道上游 HTTP 500",
      content: #{
        `application/json`: #{
          schema: #{
            anyOf: #[
              #{ $ref: "#/components/schemas/ErrorResponse" },
              #{ $ref: "#/components/schemas/OpenAIProtocolError" }
            ],
          },
        },
      },
    },
    `502`: #{
      description: "分组最终失败, 直连网络或转换等真实尝试失败, 或指定渠道上游 HTTP 502",
      content: #{
        `application/json`: #{
          schema: #{
            anyOf: #[
              #{ $ref: "#/components/schemas/ErrorResponse" },
              #{ $ref: "#/components/schemas/OpenAIProtocolError" }
            ],
          },
        },
      },
    },
    `503`: #{
      description: "无启用候选或直连目标不可用, 不兼容, 熔断, 或指定渠道上游 HTTP 503",
      content: #{
        `application/json`: #{
          schema: #{
            anyOf: #[
              #{ $ref: "#/components/schemas/ErrorResponse" },
              #{ $ref: "#/components/schemas/OpenAIProtocolError" }
            ],
          },
        },
      },
    },
    default: #{
      description: "其他上游状态, 包括协议直通非 200 成功及直连其他 HTTP 错误(例如上游 415), 不一概表示错误. 本入口没有 RequireJSON 的本地 415, 不支持 SSE.",
      content: #{
        `application/json`: #{
          schema: #{
            anyOf: #[
              #{ $ref: "#/components/schemas/ImageResponse" },
              #{ $ref: "#/components/schemas/PassthroughJSON" },
              #{ $ref: "#/components/schemas/ErrorResponse" },
              #{ $ref: "#/components/schemas/OpenAIProtocolError" }
            ],
          },
        },
        `*/*`: #{
          schema: #{
            description: "直通成功可以保留原始或空 body 及其他 Content-Type.",
          },
        },
      },
    },
  }
)
op relayImageEdits(
  @header contentType: "multipart/form-data",

  @doc("multipart 文件和文本字段. 标准形式为 multipart/form-data; boundary=..., 解析器也接受其他 multipart/ MIME, 必须有有效 boundary.")
  @multipartBody
  body: ImageEditRequestParts,
):
  | HttpResponse<
      200,
      RelayImageGenerationsStatus200application_jsonValue,
      "普通转换的非流式 Images JSON, 无管理包装. 非流式直通可能保留其他 JSON, Content-Type 或空 body.",
      "application/json"
    >
  | HttpResponse<
      200,
      PassthroughBody,
      "普通转换的非流式 Images JSON, 无管理包装. 非流式直通可能保留其他 JSON, Content-Type 或空 body.",
      "*/*"
    >
  | HttpResponse<
      400,
      RelayChatCompletionsStatus400application_jsonValue,
      "multipart, 文件, prompt, stream 或白名单校验失败, 或指定渠道上游 HTTP 400",
      "application/json"
    >
  | HttpResponse<
      401,
      RelayChatCompletionsStatus400application_jsonValue,
      "Octopus API Key 缺失, 无效, 禁用, 过期或超成本, 或指定渠道上游 HTTP 401",
      "application/json"
    >
  | HttpResponse<
      404,
      RelayChatCompletionsStatus400application_jsonValue,
      "本地模型或直连渠道不存在, 或指定渠道上游 HTTP 404",
      "application/json"
    >
  | HttpResponse<
      500,
      RelayChatCompletionsStatus400application_jsonValue,
      "本地读取, 解压, 非校验转换或 recovery 失败, 或指定渠道上游 HTTP 500",
      "application/json"
    >
  | HttpResponse<
      502,
      RelayChatCompletionsStatus400application_jsonValue,
      "分组最终失败, 直连网络或转换等真实尝试失败, 或指定渠道上游 HTTP 502",
      "application/json"
    >
  | HttpResponse<
      503,
      RelayChatCompletionsStatus400application_jsonValue,
      "无启用候选或直连目标不可用, 不兼容, 熔断, 或指定渠道上游 HTTP 503",
      "application/json"
    >
  | DefaultHttpResponse<
      RelayImageGenerationsStatusdefaultapplication_jsonValue,
      "其他上游状态, 包括协议直通非 200 成功及直连其他 HTTP 错误(例如上游 415), 不一概表示错误. 本入口没有 RequireJSON 的本地 415, 不支持 SSE.",
      "application/json"
    >
  | DefaultHttpResponse<
      RelayEmbeddingsStatusdefault___Value,
      "其他上游状态, 包括协议直通非 200 成功及直连其他 HTTP 错误(例如上游 415), 不一概表示错误. 本入口没有 RequireJSON 的本地 415, 不支持 SSE.",
      "*/*"
    >;

@route("/v1/images/variations")
@post
@operationId("relayImageVariations")
@tag("Relay")
@summary("转发 multipart 图片变体请求")
@doc("入站及响应固定为 OpenAI Images 格式. 非空 x-api-key 优先于 Authorization, Key 必须属于 Octopus 且启用, 未过期, 累计成本未超过 max_cost. 不挂 RequireJSON. 使用有效 boundary 的 multipart, 解析器接受 multipart/ 前缀. JSON body 不受支持且通常返回 400, 不是入口 415. image 或 image[] 合计必须恰好一个带非空 filename 的图片文件. prompt trim 后非空则 400, mask 即使被解析也不用于变体请求. model 先 trim, 缺失或空默认 dall-e-2. 表单 stream trim 后大小写不敏感等于 true 则 400, 不支持 SSE. 文件 MIME 支持 PNG, JPEG, GIF, WebP. 单文件及普通字段默认 50 MiB, AXONHUB_MAX_IMAGE_FILE_SIZE 可调整, 总 body 固定解析限额 816 MiB, 文件 part 总计最多 16(含 mask 或未知字段), 限额在完整读入后校验, 通常 400 而非 413. OpenAI chat/responses/image 类渠道走 OpenAI variations, openai/responses 此时不是 Responses API. 工厂虽允许 Gemini 和 Doubao, 但两者 image outbound 必须有 prompt, 与此入站禁止 prompt 冲突, 标准转换路径不能完成 variation, 会以真实尝试失败处理. 完整 model 优先精确匹配 Group, 存在但无启用候选时 503 且不回退. 不存在且含 / 时按第一个 / 拆 channelName/modelName, 大小写严格, multipart 入站已 trim model, 只匹配渠道 Model/CustomModel 当前快照. supported_models 非空禁止实际直连, Group 白名单逗号拆分不 trim, 与 /v1/models 的 trim 不同. 直连仅一次指定渠道尝试, 无负载均衡, 故障转移, sticky, Group 超时或 Group 请求选项, 仍检查启用, Key, BaseURL 和熔断. Group 请求选项先应用, 渠道后覆盖, 参数覆盖只作用于最终 JSON body, 不能改写 OpenAI multipart body, Header 可覆盖或删除认证. 协议直通需开关, 相同入站和渠道 Type, 无适用请求选项, endpoint 有效且 multipart 不需要模型改写, 仍先解析和选路, 不等同直连. 直连去前缀或 Group 模型改名通常退回转换. 直通保留成功 body, status 和 Header, 直接拼 BaseURL 与 endpoint, 不做普通版本规范化. 本地错误及分组最终失败为 ErrorResponse, 分组最终失败 502. 仅直连可识别的上游 HTTP 错误保留状态并转 OpenAIProtocolError, 无 HTTP 状态的真实尝试失败 502, 不可用或跳过 503. Group 路由按 mode 排序候选并在配置允许时让可用 sticky 渠道优先, 尚未写出响应时可顺次尝试或故障转移. 尝试结果记录于 RelayLog.Attempts 并统计, 日志持久化受设置影响. Authorization 通常使用 Bearer <key>, 实现也接受直接 key, 仅剥离精确的 Bearer 加一个空格, 不额外 trim 或读取 Cookie/query 凭证. 来源: internal/server/server.go:65, axonhub pin d061ac7df6ae transformer/openai/image_inbound.go:323-371, transformer/gemini/image.go:19-29, transformer/doubao/outbound.go:190-198.")
@useAuth(APIKeyHeader | APIKeyBearer)
@extension(
  "requestBody",
  #{
    required: true,
    description: "multipart 图片文件及文本参数. 只允许一个 image, 标准 MIME 为 multipart/form-data, 其他 multipart/ MIME 也需有效 boundary.",
    content: #{
      `multipart/form-data`: #{
        schema: #{ $ref: "#/components/schemas/ImageVariationRequest" },
        encoding: #{
          image: #{
            contentType: "image/png, image/jpeg, image/gif, image/webp",
          },
          `image[]`: #{
            contentType: "image/png, image/jpeg, image/gif, image/webp",
          },
        },
      },
    },
  }
)
@extension(
  "responses",
  #{
    `200`: #{
      description: "普通转换的非流式 Images JSON, 无管理包装. 非流式直通可能保留其他 JSON, Content-Type 或空 body.",
      content: #{
        `application/json`: #{
          schema: #{
            anyOf: #[
              #{ $ref: "#/components/schemas/ImageResponse" },
              #{ $ref: "#/components/schemas/PassthroughJSON" }
            ],
          },
        },
        `*/*`: #{ schema: #{ $ref: "#/components/schemas/PassthroughBody" } },
      },
    },
    `400`: #{
      description: "multipart, 图片数量, prompt, stream 或白名单校验失败, 或指定渠道上游 HTTP 400",
      content: #{
        `application/json`: #{
          schema: #{
            anyOf: #[
              #{ $ref: "#/components/schemas/ErrorResponse" },
              #{ $ref: "#/components/schemas/OpenAIProtocolError" }
            ],
          },
        },
      },
    },
    `401`: #{
      description: "Octopus API Key 缺失, 无效, 禁用, 过期或超成本, 或指定渠道上游 HTTP 401",
      content: #{
        `application/json`: #{
          schema: #{
            anyOf: #[
              #{ $ref: "#/components/schemas/ErrorResponse" },
              #{ $ref: "#/components/schemas/OpenAIProtocolError" }
            ],
          },
        },
      },
    },
    `404`: #{
      description: "本地模型或直连渠道不存在, 或指定渠道上游 HTTP 404",
      content: #{
        `application/json`: #{
          schema: #{
            anyOf: #[
              #{ $ref: "#/components/schemas/ErrorResponse" },
              #{ $ref: "#/components/schemas/OpenAIProtocolError" }
            ],
          },
        },
      },
    },
    `500`: #{
      description: "本地读取, 解压, 非校验转换或 recovery 失败, 或指定渠道上游 HTTP 500",
      content: #{
        `application/json`: #{
          schema: #{
            anyOf: #[
              #{ $ref: "#/components/schemas/ErrorResponse" },
              #{ $ref: "#/components/schemas/OpenAIProtocolError" }
            ],
          },
        },
      },
    },
    `502`: #{
      description: "分组最终失败, 直连网络或转换等真实尝试失败, 包括 Gemini/Doubao variation 的 prompt 冲突, 或指定渠道上游 HTTP 502",
      content: #{
        `application/json`: #{
          schema: #{
            anyOf: #[
              #{ $ref: "#/components/schemas/ErrorResponse" },
              #{ $ref: "#/components/schemas/OpenAIProtocolError" }
            ],
          },
        },
      },
    },
    `503`: #{
      description: "无启用候选或直连目标不可用, 不兼容, 熔断, 或指定渠道上游 HTTP 503",
      content: #{
        `application/json`: #{
          schema: #{
            anyOf: #[
              #{ $ref: "#/components/schemas/ErrorResponse" },
              #{ $ref: "#/components/schemas/OpenAIProtocolError" }
            ],
          },
        },
      },
    },
    default: #{
      description: "其他上游状态, 包括协议直通非 200 成功及直连其他 HTTP 错误(例如上游 415), 不一概表示错误. 本入口没有 RequireJSON 的本地 415, 不支持 SSE.",
      content: #{
        `application/json`: #{
          schema: #{
            anyOf: #[
              #{ $ref: "#/components/schemas/ImageResponse" },
              #{ $ref: "#/components/schemas/PassthroughJSON" },
              #{ $ref: "#/components/schemas/ErrorResponse" },
              #{ $ref: "#/components/schemas/OpenAIProtocolError" }
            ],
          },
        },
        `*/*`: #{
          schema: #{
            description: "直通成功可以保留原始或空 body 及其他 Content-Type.",
          },
        },
      },
    },
  }
)
op relayImageVariations(
  @header contentType: "multipart/form-data",

  @doc("multipart 图片文件及文本参数. 只允许一个 image, 标准 MIME 为 multipart/form-data, 其他 multipart/ MIME 也需有效 boundary.")
  @multipartBody
  body: ImageVariationRequestParts,
):
  | HttpResponse<
      200,
      RelayImageGenerationsStatus200application_jsonValue,
      "普通转换的非流式 Images JSON, 无管理包装. 非流式直通可能保留其他 JSON, Content-Type 或空 body.",
      "application/json"
    >
  | HttpResponse<
      200,
      PassthroughBody,
      "普通转换的非流式 Images JSON, 无管理包装. 非流式直通可能保留其他 JSON, Content-Type 或空 body.",
      "*/*"
    >
  | HttpResponse<
      400,
      RelayChatCompletionsStatus400application_jsonValue,
      "multipart, 图片数量, prompt, stream 或白名单校验失败, 或指定渠道上游 HTTP 400",
      "application/json"
    >
  | HttpResponse<
      401,
      RelayChatCompletionsStatus400application_jsonValue,
      "Octopus API Key 缺失, 无效, 禁用, 过期或超成本, 或指定渠道上游 HTTP 401",
      "application/json"
    >
  | HttpResponse<
      404,
      RelayChatCompletionsStatus400application_jsonValue,
      "本地模型或直连渠道不存在, 或指定渠道上游 HTTP 404",
      "application/json"
    >
  | HttpResponse<
      500,
      RelayChatCompletionsStatus400application_jsonValue,
      "本地读取, 解压, 非校验转换或 recovery 失败, 或指定渠道上游 HTTP 500",
      "application/json"
    >
  | HttpResponse<
      502,
      RelayChatCompletionsStatus400application_jsonValue,
      "分组最终失败, 直连网络或转换等真实尝试失败, 包括 Gemini/Doubao variation 的 prompt 冲突, 或指定渠道上游 HTTP 502",
      "application/json"
    >
  | HttpResponse<
      503,
      RelayChatCompletionsStatus400application_jsonValue,
      "无启用候选或直连目标不可用, 不兼容, 熔断, 或指定渠道上游 HTTP 503",
      "application/json"
    >
  | DefaultHttpResponse<
      RelayImageGenerationsStatusdefaultapplication_jsonValue,
      "其他上游状态, 包括协议直通非 200 成功及直连其他 HTTP 错误(例如上游 415), 不一概表示错误. 本入口没有 RequireJSON 的本地 415, 不支持 SSE.",
      "application/json"
    >
  | DefaultHttpResponse<
      RelayEmbeddingsStatusdefault___Value,
      "其他上游状态, 包括协议直通非 200 成功及直连其他 HTTP 错误(例如上游 415), 不一概表示错误. 本入口没有 RequireJSON 的本地 415, 不支持 SSE.",
      "*/*"
    >;

@format("date-time")
scalar GetChannelLastSyncTimeStatus200application_jsonDataValue extends string;

union UpdateGroupStatus200application_jsonDataValue {
  Group,
  null,
}

union ListRelayLogsStatus200application_jsonDataValue {
  RelayLog[],
  null,
}

@doc("重复的 data: <RelayLog JSON>\\n\\n 文本帧.")
scalar StreamRelayLogsStatus200text_event_streamValue extends string;

union ListStatsDailyStatus200application_jsonDataValue {
  StatsDaily[],
  null,
}

@minItems(1)
@maxItems(24)
model ListStatsHourlyStatus200application_jsonDataValue is StatsHourly[];

@doc("当前实现空列表为 [], 保留 null 兼容边界.")
union ListModelPricesStatus200application_jsonDataValue {
  LLMInfo[],
  null,
}

@doc("当前实现空列表为 [], 保留 null 兼容边界.")
union ListModelChannelsStatus200application_jsonDataValue {
  LLMChannel[],
  null,
}

@doc("RFC3339 风格时间字符串, 可带小数秒及运行环境时区. 尚未更新时为 0001-01-01T00:00:00Z.")
@format("date-time")
@extension("examples", #["0001-01-01T00:00:00Z", "2026-07-15T10:20:30Z"])
scalar GetRemoteModelPriceUpdateTimeStatus200application_jsonDataValue
  extends string;

union ListCompatibleModelsStatus200application_jsonValue {
  OpenAIModelList,
  AnthropicModelList,
}

union RelayChatCompletionsStatus200application_jsonValue {
  OpenAIChatResponse,
  PassthroughJSON,
}

@doc("SSE 文本. data 通常是 Chat Completions chunk JSON, 可含 [DONE]. 不把整段流声明为 JSON 数组.")
scalar RelayChatCompletionsStatus200text_event_streamValue extends string;

union RelayChatCompletionsStatus400application_jsonValue {
  ErrorResponse,
  OpenAIProtocolError,
}

union RelayChatCompletionsStatusdefaultapplication_jsonValue {
  OpenAIChatResponse,
  PassthroughJSON,
  ErrorResponse,
  OpenAIProtocolError,
}

@doc("协议直通可能保留未建模的成功 body, 包括空 body 或非 JSON 内容.")
union RelayChatCompletionsStatusdefault___Value {
  unknown,
}

union RelayResponsesStatus200application_jsonValue {
  ResponsesResponse,
  PassthroughJSON,
}

@doc("SSE 文本. 包含 response.created, response.output_text.delta, response.completed 等类型事件, 具体事件取决于转换和上游能力.")
scalar RelayResponsesStatus200text_event_streamValue extends string;

union RelayResponsesStatus400application_jsonValue {
  ErrorResponse,
  ResponsesProtocolError,
}

union RelayResponsesStatusdefaultapplication_jsonValue {
  ResponsesResponse,
  PassthroughJSON,
  ErrorResponse,
  ResponsesProtocolError,
}

@doc("直通成功可保留空 body 或其他未建模内容.")
union RelayResponsesStatusdefault___Value {
  unknown,
}

union RelayAnthropicMessagesStatus200application_jsonValue {
  AnthropicMessageResponse,
  PassthroughJSON,
}

@doc("SSE 文本. 包含 message_start, content_block_start, content_block_delta, content_block_stop, message_delta 和 message_stop 等类型事件.")
scalar RelayAnthropicMessagesStatus200text_event_streamValue extends string;

union RelayAnthropicMessagesStatus400application_jsonValue {
  ErrorResponse,
  AnthropicProtocolError,
}

union RelayAnthropicMessagesStatusdefaultapplication_jsonValue {
  AnthropicMessageResponse,
  PassthroughJSON,
  ErrorResponse,
  AnthropicProtocolError,
}

union RelayEmbeddingsStatus200application_jsonValue {
  OpenAIEmbeddingResponse,
  PassthroughJSON,
}

union RelayEmbeddingsStatusdefaultapplication_jsonValue {
  OpenAIEmbeddingResponse,
  PassthroughJSON,
  ErrorResponse,
  OpenAIProtocolError,
}

@doc("直通成功可以保留原始或空 body 及其他 Content-Type.")
union RelayEmbeddingsStatusdefault___Value {
  unknown,
}

union RelayImageGenerationsStatus200application_jsonValue {
  ImageResponse,
  PassthroughJSON,
}

union RelayImageGenerationsStatusdefaultapplication_jsonValue {
  ImageResponse,
  PassthroughJSON,
  ErrorResponse,
  OpenAIProtocolError,
}

// HttpPart 复用原字段类型, 文件数组的原始数量约束和 encoding 由操作元数据保留.
model ImageEditRequestParts {
  image?: HttpPart<ImageFile>[];
  `image[]`?: HttpPart<ImageFile>[];
  mask?: HttpPart<ImageEditRequest.mask>;
  prompt: HttpPart<ImageEditRequest.prompt>;
  `model`?: HttpPart<ImageEditRequest.`model`>;
  n?: HttpPart<ImageEditRequest.n>;
  size?: HttpPart<ImageEditRequest.size>;
  quality?: HttpPart<ImageEditRequest.quality>;
  response_format?: HttpPart<ImageEditRequest.response_format>;
  user?: HttpPart<ImageEditRequest.user>;
  background?: HttpPart<ImageEditRequest.background>;
  output_format?: HttpPart<ImageEditRequest.output_format>;
  output_compression?: HttpPart<ImageEditRequest.output_compression>;
  input_fidelity?: HttpPart<ImageEditRequest.input_fidelity>;
  partial_images?: HttpPart<ImageEditRequest.partial_images>;
  stream?: HttpPart<ImageEditRequest.stream>;
}

model ImageVariationRequestParts {
  image?: HttpPart<ImageFile>[];
  `image[]`?: HttpPart<ImageFile>[];
  `model`?: HttpPart<ImageVariationRequest.`model`>;
  n?: HttpPart<ImageVariationRequest.n>;
  size?: HttpPart<ImageVariationRequest.size>;
  response_format?: HttpPart<ImageVariationRequest.response_format>;
  user?: HttpPart<ImageVariationRequest.user>;
  prompt?: HttpPart<ImageVariationRequest.prompt>;
  stream?: HttpPart<ImageVariationRequest.stream>;
}
