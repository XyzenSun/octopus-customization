# internal/relay/balancer — 负载均衡与熔断

## 目录定位

按分组（group）配置的策略选择下一个可用通道；维护熔断状态、sticky session、迭代游标。

## 文件索引

- `balancer.go` — 策略入口，按分组配置返回迭代器（RoundRobin / Random / Failover / Weighted）
- `iterator.go` — 通道迭代器接口与各策略实现
- `circuit.go` — 两级熔断器：渠道级状态机（连续失败阈值 + 指数退避）与 Key 级开关，详见下方「两级熔断器」
- `session.go` — sticky session 支持：同一会话粘到同一通道（按 hash 或 cookie）

## 两级熔断器

两套机制彼此独立、开关分别可配，区别在**触发信号**而非"渠道 vs Key"的粒度：

| | 渠道级熔断器 | Key 级熔断器 |
|---|---|---|
| 触发信号 | 连续失败次数达阈值 | 上游返回 401/403/429/503 |
| 状态源 | 内存 `sync.Map`，重启清零 | `ChannelKey.StatusCode`/`LastUseTimeStamp`，重启保留 |
| 冷却 | 可配阈值 + 指数退避 | 硬编码：429/503 → 60s，401/403 → 300s |
| 开关 setting | `circuit_breaker_enabled` | `key_circuit_breaker_enabled` |
| 判定入口 | `IsTripped`（本包） | `model.Channel.GetChannelKey`（model 包） |

- **"渠道级"名不副实是有意保留的**：它的状态键是 `channelID:keyID:modelName`，含 keyID 和模型名。改成纯 `channelID` 会让单个坏 Key 熔断整条渠道所有 Key，是行为回归，不要动。
- **Key 级判定为什么不在本包**：`model` 包零内部依赖，读不到 setting（依赖 `op` 会循环导入）。所以本包只提供 `IsKeyCircuitBreakerEnabled()`，由调用方（`relay.go`、`helper/fetch.go`）读出后作为参数传给 `GetChannelKey`。新增取 Key 的调用点时要一并传这个开关，否则该处会绕过 Key 级熔断。
- **`ListTripped`/`ResetAll` 只覆盖渠道级**：Key 级冷却状态在渠道详情页已通过 `status_code` 徽章可见；`/circuit/reset` 不清 `StatusCode`，那是上游最后一次真实响应的事实记录，不该被"重置熔断"顺手抹掉。

## 关键约束

- **渠道级熔断状态在内存**：熔断窗口/阈值由 setting 表读取，状态本身只存内存（重启后清零是可接受行为）。Key 级相反，状态随 `ChannelKey` 落库、重启保留。
- **iterator 一次性**：每次请求新建迭代器，不要复用；并发请求各自独立迭代。
- **新增策略**：在 `iterator.go` 实现 `Iterator` 接口，在 `balancer.go` 入口注册。命名与前端 `group.Editor` 中的策略选项保持一致。
- **跳过与失败的语义**：`AttemptSkipped`（不可用、类型不匹配）和 `AttemptCircuitBreak`（熔断中）不计入失败统计；只有 `AttemptFailed` 才触发熔断累计。
- **`AttemptCircuitBreak` 只标记渠道级**：Key 级冷却导致选不出 Key 时，`relay.go` 记的是 `AttemptSkipped` + `"no available key"`，与"渠道没配 Key""Key 全被手动禁用"共用同一条消息、无法区分。排查时去渠道详情页看 `status_code` 徽章。
