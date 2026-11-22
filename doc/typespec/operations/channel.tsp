using TypeSpec.Http;
using TypeSpec.OpenAPI;

namespace Octopus;

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
      "渠道数组, 每项含 stats. nil 的渠道切片字段仍可能序列化为 null."
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
      "已创建渠道, 含主动填充的 stats 和未脱敏 keys. 辅助任务仍可能执行中."
    >
  | HttpResponse<
      400,
      ErrorResponse,
      "缺少 Authorization, JSON 绑定失败, 或 param_override 字符串无法反序列化为 map. 对应 message 为 Invalid request parameters, Invalid JSON format 或 param_override must be a valid JSON object."
    >
  | Unauthorized401
  | InvalidJSON415
  | HttpResponse<
      500,
      ErrorResponse,
      "ChannelCreate 返回的数据库或关联写入错误直接作为 message. 全局 recovery 也可能返回 500. 不存在独立的 HTTP 409 分支."
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
      "刷新后的渠道, 含 stats 和未脱敏 keys. 后台辅助处理不由本响应确认成功."
    >
  | HttpResponse<
      400,
      ErrorResponse,
      "缺少 Authorization, JSON 绑定或顶层 id required 失败, 或 param_override 格式校验失败. 绑定失败统一返回 Invalid JSON format, 参数覆盖失败返回 param_override must be a valid JSON object."
    >
  | Unauthorized401
  | InvalidJSON415
  | HttpResponse<
      500,
      ErrorResponse,
      "渠道不在缓存时返回 channel not found. 数据库事务, 密钥操作, 提交或提交后缓存刷新失败返回 op 错误文本. 没有渠道不存在的 404 分支. 全局 recovery 也可能返回 500."
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
  | HttpResponse<200, EmptySuccess, "启用状态已设置, 响应省略 data.">
  | HttpResponse<
      400,
      ErrorResponse,
      "缺少 Authorization 时返回 Invalid request parameters. JSON 绑定失败返回 Invalid JSON format. id 或 enabled 省略没有 required 校验分支."
    >
  | Unauthorized401
  | InvalidJSON415
  | HttpResponse<
      500,
      ErrorResponse,
      "渠道缓存不存在时返回 channel not found, 数据库更新失败返回其错误文本. 全局 recovery 也可能返回 500."
    >;

@route("/api/v1/channel/delete/{id}")
@delete
@operationId("deleteChannel")
@tag("Channel")
@summary("删除渠道及关联数据")
@doc("继承根级 JWTAuth. RequireJSON 对 DELETE 直接放行, 不要求 Content-Type, 不解析 body. id 使用 strconv.Atoi, 没有正数检查. op 要求渠道在缓存中存在, 在事务中删除引用该渠道的 GroupItem, 渠道 keys, StatsChannel 和渠道, 提交后清理缓存并刷新受影响分组. 受影响分组缓存刷新失败只记日志, 不改变已返回的 op 成功. 成功省略 data, 不是 data:null. 源码: internal/server/handlers/channel.go:144-155, internal/op/channel.go:263-334.")
op deleteChannel(
  @doc("渠道 ID. 路径文本必须能转换为 Go int, 没有正数范围校验. 零或负值通常在缓存查找后返回 500.")
  @path("id")
  id: integer,
):
  | HttpResponse<
      200,
      EmptySuccess,
      "通常删除渠道及关联数据后省略 data. op 内部 recover 可能回滚事务后仍返回 nil error, 此异常分支也返回成功包装."
    >
  | HttpResponse<
      400,
      ErrorResponse,
      "缺少 Authorization 时返回 Invalid request parameters. id 无法被 strconv.Atoi 转换时返回 Invalid parameter."
    >
  | Unauthorized401
  | HttpResponse<
      500,
      ErrorResponse,
      "渠道不存在时返回 channel not found. 查询关联分组, 删除关联项, keys, 统计或渠道, 以及事务提交失败返回 op 错误文本. 没有不存在时的 404 分支. 全局 recovery 也可能返回 500."
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
      "模型名称数组. 当前 helper 的正常成功路径使用非 nil 切片, 无结果返回空数组. 不保证已经验证上游 HTTP 状态码."
    >
  | HttpResponse<
      400,
      ErrorResponse,
      "缺少 Authorization 时返回 Invalid request parameters. ShouldBindJSON 失败返回 Invalid JSON format."
    >
  | Unauthorized401
  | InvalidJSON415
  | HttpResponse<
      500,
      ErrorResponse,
      "HTTP client 或代理构造, 远端请求, 远端 JSON 解析, match_regex 编译或匹配失败返回 helper 错误文本. 上游 HTTP 状态本身不会被直接映射为此接口的同码错误. 全局 recovery 也可能返回 500."
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
      "探测已生成结果, 不等于模型调用成功. 网络错误或超时通常 status_code 为 0, 上游响应码存于 data.status_code, 不直接用作管理接口 HTTP 状态."
    >
  | HttpResponse<
      400,
      ErrorResponse,
      "缺少 Authorization, JSON/model required 绑定失败, 有效来源不满足恰有一个, key_index 越界, model 不在所选集合, 空 base URL, 空 key, embedding 或不支持的协议, 请求构造或 HTTP client 构造错误. 绑定失败返回 Invalid JSON format, 其他错误返回具体手动校验或 helper 文本."
    >
  | Unauthorized401
  | HttpResponse<
      404,
      ErrorResponse,
      "有效正 channel_id 的 ChannelGet 返回任何错误, message 统一为 channel not found."
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
      "同步函数已返回, 包括内部错误仅记日志或提前返回的情况. 响应省略 data, 不提供逐渠道结果."
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
      "进程内同步时间标记, 由 time.Time 序列化为带时区的时间字符串."
    >
  | MissingAuth400
  | Unauthorized401
  | Internal500;

@format("date-time")
scalar GetChannelLastSyncTimeStatus200application_jsonDataValue extends string;
