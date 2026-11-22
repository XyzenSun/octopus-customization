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
