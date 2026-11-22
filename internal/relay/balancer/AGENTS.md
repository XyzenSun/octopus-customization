# internal/relay/balancer

本目录负责候选渠道排序, 一次性迭代与 attempt 追踪, 渠道级熔断和 sticky 记录. balancer.go 实现排序策略, iterator.go 编排候选, circuit.go 与 session.go 保存运行状态.

新增策略实现现有 Balancer 的候选排序接口并在入口选择, 每次请求新建 Iterator. 修改策略或 sticky 时读取 internal/relay/balancer/balancer.go, internal/relay/balancer/iterator.go 和 internal/relay/balancer/session.go, 保持排序与迭代职责分开. Sticky 按 APIKey ID 与请求模型关联渠道, 有效期由分组配置决定, 状态仅保存在内存.

两级熔断的开关分别配置. 渠道级按连续失败阈值和指数退避工作, 状态在内存, 重启清零; Key 级依据上游状态码与最近使用时间冷却, 429/503 为一分钟, 401/403 为五分钟, 状态随 ChannelKey 经 op 保存后可跨重启保留.

渠道级状态键保持 channelID:keyID:modelName 的粒度. 保留 Key 与模型维度, 避免单个坏 Key 影响整条渠道. 修改熔断时读取 internal/relay/balancer/circuit.go 与 internal/model/channel.go, 分别核对渠道级状态机和 Key 冷却选择. Key 开关由调用方读取并传入 model 的选择方法, 新增选择调用点也沿用该边界.

AttemptSkipped 与 AttemptCircuitBreak 不累计失败, 真实 AttemptFailed 才累计渠道级熔断. Key 冷却导致没有可用 Key 时记录 skipped 和 no available key, 与未配置或手动禁用 Key 共用消息. 排查此类跳过时核对 ChannelKey 的状态码与使用时间, 不把它等同于渠道级熔断记录.

状态查询和重置只覆盖渠道级. /api/v1/circuit/reset 保留 ChannelKey 的最后响应状态, 不顺带清除 Key 冷却依据. 调整重置接口时读取 internal/server/handlers/circuit.go, 保持这一区别.
