using TypeSpec.Http;
using TypeSpec.OpenAPI;

namespace Octopus;

// HTTP 包装独立于 JSON 载荷, 状态码与媒体类型不进入业务响应字段.
@doc(Description)
model HttpResponse<
  Status extends integer,
  Body,
  Description extends valueof string,
  MediaType extends string = "application/json"
> {
  @statusCode statusCode: Status;
  @header contentType: MediaType;
  @body body: Body;
}

// 有载荷的成功响应要求 data 存在, 无载荷接口复用 EmptySuccess.
model Success<Data> extends SuccessBase {
  data: Data;
}

@defaultResponse
@doc(Description)
model DefaultHttpResponse<
  Body,
  Description extends valueof string,
  MediaType extends string = "application/json"
> {
  @header contentType: MediaType;
  @body body: Body;
}

model MissingAuth400 is HttpResponse<
  400,
  ErrorResponse,
  "Authorization 缺失或为空. HTTP 400, message 为 Invalid request parameters."
>;

model Unauthorized401 is HttpResponse<
  401,
  ErrorResponse,
  "JWT 校验失败, 或 API Key 无效, 禁用, 过期, 累计成本超过正额度上限. 返回 Octopus 错误包装."
>;

model InvalidJSON415 is HttpResponse<
  415,
  ErrorResponse,
  "Content-Type 未包含大小写敏感的 application/json 子串. RequireJSON 不检查 JSON 内容; 语法错误由接口解析处理. 有鉴权的路由先执行鉴权, 公开登录不要求先认证."
>;

model Internal500 is HttpResponse<
  500,
  ErrorResponse,
  "接口内部错误或全局 panic recovery. 错误原因以 message 为准, 当前实现可能将资源不存在等错误映射为 500."
>;

// 三种 Relay SSE 协议复用相同的可选响应头, 不对非流式响应承诺 SSE 头值.
@doc(Description)
model SSEHttpResponse<
  Status extends integer,
  Body,
  Description extends valueof string,
  MediaType extends string = "application/json"
> extends HttpResponse<Status, Body, Description, MediaType> {
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

// 同一状态的媒体分支共享说明, JSON body 与原始字节 body 仍分别建模.
alias RelayStreamingSuccess<
  Body,
  StreamBody,
  Description extends valueof string
> =
  | SSEHttpResponse<200, Body, Description>
  | SSEHttpResponse<200, PassthroughBody, Description, "*/*">
  | SSEHttpResponse<200, StreamBody, Description, "text/event-stream">;

alias RelaySuccess<Body, Description extends valueof string> =
  | HttpResponse<200, Body, Description>
  | HttpResponse<200, PassthroughBody, Description, "*/*">;

alias RelayOtherResponses<Body, RawBody, Description extends valueof string> =
  | DefaultHttpResponse<Body, Description>
  | DefaultHttpResponse<RawBody, Description, "*/*">;

// 共用状态码与说明, Body 保留各入站协议的本地错误和协议错误联合.
alias RelayUnauthorized<Body> = HttpResponse<
  401,
  Body,
  "Octopus API Key 缺失, 无效, 禁用, 过期或超成本, 或指定渠道上游 HTTP 401"
>;

alias RelayNotFound<Body> = HttpResponse<
  404,
  Body,
  "本地模型或直连渠道不存在, 或指定渠道上游 HTTP 404"
>;

alias RelayUnsupportedMediaType<Body> = HttpResponse<
  415,
  Body,
  "RequireJSON 拒绝 Content-Type, 或指定渠道上游 HTTP 415"
>;

alias RelayBadGateway<Body> = HttpResponse<
  502,
  Body,
  "分组最终失败, 直连网络或转换等真实尝试失败, 或指定渠道上游 HTTP 502"
>;

alias RelayUnavailable<Body> = HttpResponse<
  503,
  Body,
  "无启用候选, 直连渠道禁用, 无可用 Key, 无有效 BaseURL, 不兼容或熔断, 或指定渠道上游 HTTP 503"
>;

alias RelayInternalError<Body> = HttpResponse<
  500,
  Body,
  "本地读取, 解压, 非校验转换或 recovery 失败, 或指定渠道上游 HTTP 500"
>;

alias RelayEmbeddingsUnavailable<Body> = HttpResponse<
  503,
  Body,
  "无启用候选或直连目标不可用, 不兼容, 熔断, 或指定渠道上游 HTTP 503"
>;
