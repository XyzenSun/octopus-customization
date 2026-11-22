using TypeSpec.Http;
using TypeSpec.OpenAPI;

namespace Octopus;

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
      "当前展示的熔断条目, 无条目为 data: []. 查询本身可改变熔断状态."
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
      "重置完成. 响应省略 data 字段, 不是 data: null."
    >
  | MissingAuth400
  | Unauthorized401
  | Internal500;
