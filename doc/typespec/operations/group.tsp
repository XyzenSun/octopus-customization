using TypeSpec.Http;
using TypeSpec.OpenAPI;

namespace Octopus;

@route("/api/v1/group/list")
@get
@operationId("listGroups")
@tag("Group")
@summary("获取分组列表")
@doc("继承根级 JWTAuth. RequireJSON 对 GET 放行, 不要求 Content-Type, 不解析 body, 分页或筛选参数. 返回 groupCache 的快照, 当前 op 初始化非 nil 切片, 无记录返回空数组, 无排序保证. items 带 omitempty, 空或 nil 时省略, 不保证每项含 items. handler 保留 op 错误的 500 分支, 但当前 GroupList 不返回 error. 源码: internal/server/handlers/group.go:42-49, internal/op/group.go:17-23, internal/model/group.go:12-21.")
op listGroups():
  | HttpResponse<200, Success<Group[]>, "分组数组. 空分组项的 items 属性省略.">
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
      "已创建分组. items 为空时省略, custom_header 的 nil 可为 null, param_override 的 nil 为 null."
    >
  | HttpResponse<
      400,
      ErrorResponse,
      "缺少 Authorization, JSON 绑定失败, 非空 match_regex 编译失败, 或 param_override 格式失败. 分组绑定和正则错误返回原始 err.Error(), 参数覆盖失败返回 param_override must be a valid JSON object."
    >
  | Unauthorized401
  | InvalidJSON415
  | HttpResponse<
      500,
      ErrorResponse,
      "GroupCreate 的数据库或关联写入错误返回其错误文本. 没有独立的 HTTP 409 约束冲突分支. 全局 recovery 也可能返回 500."
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
      "刷新后的分组, 空 items 省略. op 内部 recover 可能回滚后返回有类型的 nil 指针且无错误, 此异常分支的 data 为 null."
    >
  | HttpResponse<
      400,
      ErrorResponse,
      "缺少 Authorization, JSON 或顶层 id required 绑定失败, 提供的 match_regex 编译失败, 或 param_override 格式失败. 分组绑定和正则错误返回 err.Error(), 参数覆盖失败返回固定错误文本."
    >
  | Unauthorized401
  | InvalidJSON415
  | HttpResponse<
      500,
      ErrorResponse,
      "分组不在缓存时返回 group not found. 更新字段, 子项操作, 事务提交或提交后缓存刷新失败返回 op 错误文本. 没有分组不存在的 404 分支. 全局 recovery 也可能返回 500."
    >;

@route("/api/v1/group/delete/{id}")
@delete
@operationId("deleteGroup")
@tag("Group")
@summary("删除分组及分组项")
@doc("继承根级 JWTAuth. RequireJSON 对 DELETE 放行, 不要求 Content-Type 或 body. id 使用 strconv.Atoi, 没有正数检查. op 要求分组在缓存存在, 在事务中删除 GroupItem 和 Group, 提交后删除缓存与名称映射. 成功 data 是固定字符串, 不是空响应. 源码: internal/server/handlers/group.go:98-110, internal/op/group.go:191-220.")
op deleteGroup(
  @doc("分组 ID. 必须能转换为 Go int, 无正数范围约束.")
  @path("id")
  id: integer,
):
  | HttpResponse<
      200,
      Success<"group deleted successfully">,
      "通常删除分组及关联项后返回成功字符串. op 内部 recover 可能回滚后仍返回 nil error, 200 不保证该异常分支完成删除."
    >
  | HttpResponse<
      400,
      ErrorResponse,
      "缺少 Authorization 时返回 Invalid request parameters. id 转换失败时返回 strconv.Atoi 的错误文本, 不是统一 Invalid parameter."
    >
  | Unauthorized401
  | HttpResponse<
      500,
      ErrorResponse,
      "分组不存在时返回 group not found. 子项或分组删除, 事务提交失败返回 op 错误文本. 没有不存在时的 404 分支. 全局 recovery 也可能返回 500."
    >;

union UpdateGroupStatus200application_jsonDataValue {
  Group,
  null,
}
