using TypeSpec.Http;
using TypeSpec.OpenAPI;

namespace Octopus;

@route("/api/v1/log/list")
@get
@operationId("listRelayLogs")
@tag("Log")
@summary("分页查询转发日志")
@doc("先按时间条件筛选内存缓存, 再反转缓存追加顺序并分页. 启用 relay_log_keep_enabled 时, 不足部分从数据库按 id DESC 补足; 关闭时不读取数据库历史日志. 不是缓存和数据库整体按 time 排序, 也不是原子快照. 不返回总数或 has_more. 普通越界页返回 data: null. page 没有业务上限, offset 计算未检查整数溢出, 极大值存在触发 recovery 500 的风险. Source: internal/server/handlers/log.go:39-74; internal/op/log.go:273-335.")
op listRelayLogs(
  @doc("缺失时默认为 1. 使用 strconv.Atoi 解析, 错误被忽略; 普通非数字输入解析为 0 后回退为 1, 小于 1 也回退为 1. 没有业务最大值, 整数解析范围取决于编译架构. 不应理解为非法输入返回 400.")
  @query(#{ name: "page", explode: true })
  page?: string = "1",

  @doc("缺失时默认为 20. 使用 strconv.Atoi 解析, 错误被忽略. 小于 1 或大于 100 时回退为 20, 不是钳制到 1 或 100, 也不返回 400.")
  @query(#{ name: "page_size", explode: true })
  page_size?: string = "20",

  @doc("Unix 秒的整数文本. 仅当 start_time 和 end_time 都非空时才解析和启用过滤, 此时任一解析失败返回 400. 只有一端非空时整个过滤被忽略, 不校验已提供值. 闭区间, 不校验非负或起止顺序, Go int 范围取决于编译架构.")
  @query(#{ name: "start_time", explode: true })
  start_time?: string,

  @doc("Unix 秒的整数文本. 必须与非空 start_time 同时提供才生效. 过滤为 start_time <= time <= end_time, 倒置区间不会被拒绝, 通常返回空结果.")
  @query(#{ name: "end_time", explode: true })
  end_time?: string,
):
  | HttpResponse<
      200,
      Success<ListRelayLogsStatus200application_jsonDataValue>,
      "日志列表. 无结果时 data 为 null, 不是省略 data 或固定空数组."
    >
  | HttpResponse<
      400,
      ErrorResponse,
      "缺少 Authorization, 或两端时间均非空时任一时间无法解析为 Go int. 分页参数非法或越界本身不返回此状态. Source: internal/server/middleware/auth.go:17-21; internal/server/handlers/log.go:52-63."
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
      "清理成功. 响应不含 data 字段, 不是 data: null."
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
      "日志流令牌."
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
      "token 缺失, 未知或已吊销. message 为 invalid stream token, 返回 JSON 而非 SSE 帧."
    >
  | Internal500;

union ListRelayLogsStatus200application_jsonDataValue {
  RelayLog[],
  null,
}

@doc("重复的 data: <RelayLog JSON>\\n\\n 文本帧.")
scalar StreamRelayLogsStatus200text_event_streamValue extends string;
