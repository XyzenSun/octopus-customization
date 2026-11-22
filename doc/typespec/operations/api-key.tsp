using TypeSpec.Http;
using TypeSpec.OpenAPI;

namespace Octopus;

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
      "创建后的 APIKey, 包含未脱敏的生成密钥. omitempty 属性的零值可能省略."
    >
  | HttpResponse<
      400,
      ErrorResponse,
      "缺少 Authorization 时返回 Invalid request parameters. JSON 绑定失败返回 Invalid JSON format, 缺少普通属性不触发 required 错误."
    >
  | Unauthorized401
  | InvalidJSON415
  | HttpResponse<
      500,
      ErrorResponse,
      "APIKeyCreate 的数据库写入错误返回 op 包装后的错误文本. 全局 recovery 也可能返回 500."
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
      "APIKey 数组, 密钥原样输出, 零值 omitempty 属性省略."
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
      "保存后的 APIKey, api_key 为原密钥, 零值 omitempty 属性省略."
    >
  | HttpResponse<
      400,
      ErrorResponse,
      "缺少 Authorization 时返回 Invalid request parameters. JSON 绑定失败返回 Invalid JSON format. id 缺失或 0 不会因 binding required 返回 400."
    >
  | Unauthorized401
  | InvalidJSON415
  | HttpResponse<
      500,
      ErrorResponse,
      "缓存未找到目标 id 时返回 API key not found. Save 失败返回 failed to update API key 包装的错误文本. 全局 recovery 也可能返回 500."
    >;

@route("/api/v1/apikey/delete/{id}")
@delete
@operationId("deleteApiKey")
@tag("APIKey")
@summary("删除 API Key 及其统计")
@doc("继承根级 JWTAuth. RequireJSON 对 DELETE 放行, 不要求 Content-Type 或 body. id 使用 strconv.Atoi, 无正数校验. op 先调用 StatsAPIKeyDel, 再删除 APIKey, 检查删除 RowsAffected, 最后清理缓存和密钥索引. 这些步骤不在同一事务中, 错误响应不保证此前统计删除没有发生. 成功省略 data, 不是 data:null. 源码: internal/server/handlers/apikey.go:86-98, internal/op/apikey.go:61-79.")
op deleteApiKey(
  @doc("API Key ID. 必须能转换为 Go int, 没有正数范围约束.")
  @path("id")
  id: integer,
):
  | HttpResponse<200, EmptySuccess, "APIKey 删除成功, 响应省略 data.">
  | HttpResponse<
      400,
      ErrorResponse,
      "缺少 Authorization 时返回 Invalid request parameters. id 转换失败返回 Invalid parameter."
    >
  | Unauthorized401
  | HttpResponse<
      500,
      ErrorResponse,
      "删除统计失败, APIKey 删除 RowsAffected 为 0 时返回 API key not found, 或数据库删除错误. 没有不存在时的 404 分支. 全局 recovery 也可能返回 500."
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
      "当前密钥统计和展示信息. info.supported_models 经过分组名称过滤改写, 不是原始限制字符串."
    >
  | HttpResponse<
      401,
      ErrorResponse,
      "缺少密钥, 前缀错误, 缓存未找到时返回 Authentication failed. 禁用返回 API key is disabled, 过期返回 API key has expired, 超出正额度上限返回 API key has reached the max cost. 没有管理 JWT 缺头的 400 分支."
    >
  | HttpResponse<
      500,
      ErrorResponse,
      "鉴权后读取 APIKey 信息失败或 GroupListModel 失败返回对应错误文本, 当前 GroupListModel 实现不返回 error. 全局 recovery 也可能返回 500."
    >;

@route("/api/v1/apikey/login")
@get
@operationId("probeApiKeyLogin")
@tag("APIKey")
@summary("探测 API Key 登录可用性")
@doc("覆盖根级 JWTAuth, 接受 x-api-key 或 Authorization API Key, 非空 x-api-key 优先. 不使用 RequireJSON, 不解析 body 或 query. handler 只在 APIKeyAuth 通过后返回空成功包装. 鉴权仍检查前缀, 缓存存在, enabled, 正 expire_at 是否已过期及正 max_cost 是否小于累计 input_cost+output_cost. 等于额度上限仍放行. 不是只检查密钥字符串是否存在. APIKeyAuth 调用 StatsAPIKeyGet, 缓存未命中可建立零值统计并标记待持久化. 成功省略 data, 不是 data:null. 源码: internal/server/handlers/apikey.go:38-47,132-134, internal/server/middleware/auth.go:32-81, internal/op/stats.go:377-393.")
@useAuth(APIKeyHeader | APIKeyBearer)
op probeApiKeyLogin():
  | HttpResponse<200, EmptySuccess, "API Key 鉴权通过, 响应省略 data.">
  | HttpResponse<
      401,
      ErrorResponse,
      "缺少密钥, 前缀错误, 缓存未找到时返回 Authentication failed. 禁用, 已过期或超过正额度上限分别返回对应 API Key 错误文本. 没有管理 JWT 缺头的 400 分支."
    >
  | Internal500;
