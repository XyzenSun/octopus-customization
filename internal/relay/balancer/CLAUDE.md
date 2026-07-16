# internal/relay/balancer — 负载均衡与熔断

## 目录定位

按分组（group）配置的策略选择下一个可用通道；维护熔断状态、sticky session、迭代游标。

## 文件索引

- `balancer.go` — 策略入口，按分组配置返回迭代器（RoundRobin / Random / Failover / Weighted）
- `iterator.go` — 通道迭代器接口与各策略实现
- `circuit.go` — 熔断器：连续失败阈值触发熔断，窗口期内跳过该通道
- `session.go` — sticky session 支持：同一会话粘到同一通道（按 hash 或 cookie）

## 关键约束

- **熔断状态在内存**：熔断窗口/阈值由 setting 表读取，状态本身只存内存（重启后清零是可接受行为）。
- **iterator 一次性**：每次请求新建迭代器，不要复用；并发请求各自独立迭代。
- **新增策略**：在 `iterator.go` 实现 `Iterator` 接口，在 `balancer.go` 入口注册。命名与前端 `group.Editor` 中的策略选项保持一致。
- **跳过与失败的语义**：`AttemptSkipped`（不可用、类型不匹配）和 `AttemptCircuitBreak`（熔断中）不计入失败统计；只有 `AttemptFailed` 才触发熔断累计。
