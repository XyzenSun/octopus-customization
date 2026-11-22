using TypeSpec.Http;
using TypeSpec.OpenAPI;

namespace Octopus;

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
      "自定义模型价格列表"
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
      "创建成功, data 是原始提交对象补上零值后的结果"
    >
  | HttpResponse<400, ErrorResponse, "JWT 头缺失, JSON 解析或绑定失败">
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
  | HttpResponse<200, Success<LLMInfo>, "更新成功">
  | HttpResponse<400, ErrorResponse, "JWT 头缺失, JSON 解析或绑定失败">
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
  | HttpResponse<200, EmptySuccess, "删除成功, 不包含 data">
  | HttpResponse<
      400,
      ErrorResponse,
      "JWT 头缺失, JSON 解析失败, name 缺失或为空"
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
      "模型与渠道关联列表"
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
  | HttpResponse<200, EmptySuccess, "远端价格内存表同步更新成功, 不包含 data">
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
      "序列化的 time.Time"
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
      "由鉴权头决定的兼容模型列表"
    >
  | Unauthorized401
  | Internal500;

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
