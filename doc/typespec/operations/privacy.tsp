using TypeSpec.Http;
using TypeSpec.OpenAPI;

namespace Octopus;

// 管理 JWT 与普通 API Key 保持分离. Auth 先于 RequireJSON 执行.
@route("/api/v1/privacy/list")
@get
@useAuth(JWTAuth)
@operationId("listPrivacyRules")
@tag("Privacy")
@summary("获取全部隐私规则")
@doc("管理 JWT 鉴权, 不接受普通 API Key. RequireJSON 对 GET 放行, 不要求 Content-Type 或 body. 按 id 升序返回全部规则, 包含停用规则, 空集合为 data: []. source/alias 明文返回, 不包含匹配次数或会话数据. 读取或内部失败仅返回固定错误, 不暴露数据库错误或映射参数. Source: internal/server/handlers/privacy.go; internal/op/privacy.go.")
op listPrivacyRules():
  | HttpResponse<
      200,
      Success<PrivacyRule[]>,
      "按 id 升序的全部规则, 空列表为 []."
    >
  | MissingAuth400
  | Unauthorized401
  | HttpResponse<
      500,
      ErrorResponse,
      "固定隐私内部错误, 不含原数据库错误或敏感参数."
    >;

@route("/api/v1/privacy/create")
@post
@useAuth(JWTAuth)
@operationId("createPrivacyRule")
@tag("Privacy")
@summary("创建隐私规则")
@doc("Auth 后执行 RequireJSON, Content-Type 必须包含大小写敏感子串 application/json, 不通用支持 +json. source/alias 必填, enabled 省略为 true, 显式 false 保留, null 拒绝. 创建输入的 id 可省略或为 null; 提供非 null 值时必须能被当前架构的 Go int JSON 解码, 字符串等错误类型返回 400, 不检查正数. 数据库生成正自增 id, 不采用客户端 id 值. source 与 alias 分别全表唯一, 停用规则仍占用键. 候选规则及完整隐私配置校验/编译和事务提交成功后一次发布, 失败保留旧快照; 功能开关, 四范围及失败策略见 Setting, 当前请求不因管理更新切换版本. 停用项退出后续请求的替换及反查, 不释放唯一键. 日志/规则/备份仍可能含明文, 不自动清理或加密. 错误只使用固定文本. Source: internal/server/handlers/privacy.go; internal/model/privacy.go; internal/op/privacy.go.")
op createPrivacyRule(
  @header contentType: "application/json",
  @body body: PrivacyRuleCreate,
):
  | HttpResponse<
      200,
      Success<PrivacyRule>,
      "已提交规则, 包括数据库生成的 ID 和实际 enabled."
    >
  | HttpResponse<
      400,
      ErrorResponse,
      "缺少 Authorization, JSON 解析失败或规则校验失败."
    >
  | Unauthorized401
  | HttpResponse<
      409,
      ErrorResponse,
      "source 或 alias 冲突, message 为 privacy rule conflict, 不返回冲突值."
    >
  | InvalidJSON415
  | HttpResponse<
      500,
      ErrorResponse,
      "编译失败或存储等内部失败, 仅固定文本, 不泄露参数."
    >;

@route("/api/v1/privacy/update")
@post
@useAuth(JWTAuth)
@operationId("updatePrivacyRule")
@tag("Privacy")
@summary("完整更新或启停隐私规则")
@doc("Auth 后 RequireJSON. 更新必须提供正整数 id 和完整 source/alias/enabled, 缺失字段不隐式覆盖旧值. source/alias 不 trim 或归一化, false 显式保存. 未找到返回 404, 双唯一冲突返回 409, 失败不发布候选快照. 不提供单独 enabled patch. 当前请求继续使用原规则/配置快照, 管理写入只影响后续请求. 硬删除或改绑/复用别名可能改变历史标记反查含义, 不保留历史映射或会话关联. Source: internal/server/handlers/privacy.go; internal/model/privacy.go; internal/op/privacy.go.")
op updatePrivacyRule(
  @header contentType: "application/json",
  @body body: PrivacyRuleUpdate,
):
  | HttpResponse<200, Success<PrivacyRule>, "完整保存后的规则, 同值更新也成功.">
  | HttpResponse<
      400,
      ErrorResponse,
      "缺少 Authorization, JSON 解析失败或规则/ID 校验失败."
    >
  | Unauthorized401
  | HttpResponse<
      404,
      ErrorResponse,
      "规则不存在, message 为 privacy rule not found."
    >
  | HttpResponse<
      409,
      ErrorResponse,
      "source 或 alias 冲突, message 为 privacy rule conflict, 不返回冲突值."
    >
  | InvalidJSON415
  | HttpResponse<
      500,
      ErrorResponse,
      "编译失败或存储等内部失败, 仅固定文本, 不泄露参数."
    >;

@route("/api/v1/privacy/delete/{id}")
@delete
@useAuth(JWTAuth)
@operationId("deletePrivacyRule")
@tag("Privacy")
@summary("硬删除隐私规则")
@doc("Auth 后 RequireJSON, DELETE 不要求 Content-Type 或 body. id 使用 strconv.Atoi 且要求正数. 硬删除不保留旧映射, 释放 source/alias 唯一键, 接受别名复用改变历史标记反查含义. 不存在明确返回 404. 提交成功后发布, 当前请求仍保持旧快照. 成功省略 data, 不是 data:null. Source: internal/server/handlers/privacy.go; internal/op/privacy.go.")
op deletePrivacyRule(
  @doc("规则 ID, 必须能解析为当前架构的 Go int 且大于 0.")
  @minValue(1)
  @path("id")
  id: integer,
):
  | HttpResponse<200, EmptySuccess, "已硬删除规则, 响应省略 data.">
  | HttpResponse<400, ErrorResponse, "缺少 Authorization, 或 ID 非法/非正数.">
  | Unauthorized401
  | HttpResponse<
      404,
      ErrorResponse,
      "规则不存在, message 为 privacy rule not found."
    >
  | HttpResponse<
      500,
      ErrorResponse,
      "编译失败或存储等内部失败, 仅固定文本, 不泄露参数."
    >;
