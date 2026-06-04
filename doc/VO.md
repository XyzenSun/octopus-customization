# 值对象文档（VO - Value Object）

> 本文档描述 Octopus 项目中的值对象（Value Object）：用于在表现层、协议适配层、业务模块间传递的不可变数据载体。
> 与 ENTITY（持久化实体）和 DTO（HTTP 请求/响应）区分：
> - **ENTITY**：GORM 持久化模型，对应数据库表
> - **DTO**：HTTP 接口的请求体/响应体，关注「线协议」
> - **VO**：业务语义上的数据值容器，关注「领域语义」，可被嵌入实体、组合进 DTO、或在前端用于展示
>
> 源码位置：
> - 后端嵌入值对象：`internal/model/*.go`（与实体共存）
> - 后端协议适配 VO：`internal/model/llm.go`
> - 前端展示 VO：`web/src/api/endpoints/*.ts`

---

## 一、嵌入值对象（Embedded Value Object）

> 这类 VO 通过 GORM `serializer:json` 嵌入到实体的某一列中，**不独立建表**，但具有清晰的领域语义，可在业务代码中复用。

### BaseUrl（基础 URL）

**用途**：表示一个上游 API 的访问端点及其网络延迟，用于渠道多端点选路。
**载体**：`Channel.BaseUrls []BaseUrl`（JSON 序列化存储）

| 字段 | Go 类型 | TS 类型 | JSON 键 | 说明 |
|------|---------|---------|---------|------|
| URL | `string` | `string` | `url` | 上游 API 基础地址 |
| Delay | `int` | `number` | `delay` | 网络延迟（毫秒），由探测任务定期更新 |

**关联逻辑**：`Channel.GetBaseUrl()` 会从 `BaseUrls` 中选取 `Delay` 最小且非空的 URL。

---

### CustomHeader（自定义请求头）

**用途**：在转发到上游时附加的自定义 HTTP Header。
**载体**：`Channel.CustomHeader []CustomHeader`（JSON 序列化存储）

| 字段 | Go 类型 | TS 类型 | JSON 键 | 说明 |
|------|---------|---------|---------|------|
| HeaderKey | `string` | `string` | `header_key` | 请求头键名 |
| HeaderValue | `string` | `string` | `header_value` | 请求头值 |

---

### ChannelAttempt（渠道尝试记录）

**用途**：记录一次上游转发过程中对某个渠道的单次尝试结果（成功/失败/熔断/跳过），用于审计与失败诊断。
**载体**：`RelayLog.Attempts []ChannelAttempt`（JSON 序列化存储）

| 字段 | Go 类型 | TS 类型 | JSON 键 | 说明 |
|------|---------|---------|---------|------|
| ChannelID | `int` | `number` | `channel_id` | 尝试的渠道 ID |
| ChannelKeyID | `int` | `number?` | `channel_key_id,omitempty` | 使用的密钥 ID（0 表示未到使用 key 的步骤） |
| ChannelName | `string` | `string` | `channel_name` | 渠道名称（冗余字段，便于日志查看） |
| ModelName | `string` | `string` | `model_name` | 使用的上游模型名 |
| AttemptNum | `int` | `number` | `attempt_num` | 第几次尝试（从 1 开始递增） |
| Status | `AttemptStatus` | `AttemptStatus` | `status` | 尝试状态（见枚举） |
| Duration | `int` | `number` | `duration` | 本次尝试耗时（毫秒） |
| Sticky | `bool` | `boolean?` | `sticky,omitempty` | 是否会话保持命中 |
| Msg | `string` | `string?` | `msg,omitempty` | 附加消息（错误/跳过原因） |

**枚举 AttemptStatus：**

| 值 | Go 常量 | TS 字面量 | 说明 |
|----|---------|-----------|------|
| `success` | `AttemptSuccess` | `'success'` | 转发成功 |
| `failed` | `AttemptFailed` | `'failed'` | 转发失败（上游返回错误） |
| `circuit_break` | `AttemptCircuitBreak` | `'circuit_break'` | 熔断跳过 |
| `skipped` | `AttemptSkipped` | `'skipped'` | 其他原因跳过（禁用/无 Key/类型不兼容等） |

---

### LLMPrice（模型定价）

**用途**：描述一个 LLM 模型的四档单价，作为价格表的核心结构。被嵌入到 `LLMInfo`、`StatsModel` 等实体中以供成本计算。

| 字段 | Go 类型 | TS 类型 | JSON 键 | 说明 |
|------|---------|---------|---------|------|
| Input | `float64` | `number` | `input` | 输入 Token 单价 |
| Output | `float64` | `number` | `output` | 输出 Token 单价 |
| CacheRead | `float64` | `number` | `cache_read` | 缓存读取 Token 单价 |
| CacheWrite | `float64` | `number` | `cache_write` | 缓存写入 Token 单价 |

> 单位由调用方自行约定（通常为美元/百万 Token）。

---

### StatsMetrics（统计度量）

**用途**：所有统计维度共享的度量集合（Token 数、费用、请求成功/失败数、等待时间），通过 Go 嵌入到各类 Stats 实体。

| 字段 | Go 类型 | TS 类型 | JSON 键 | 说明 |
|------|---------|---------|---------|------|
| InputToken | `int64` | `number` | `input_token` | 累计输入 Token 数 |
| OutputToken | `int64` | `number` | `output_token` | 累计输出 Token 数 |
| InputCost | `float64` | `number` | `input_cost` | 累计输入 Token 费用 |
| OutputCost | `float64` | `number` | `output_cost` | 累计输出 Token 费用 |
| WaitTime | `int64` | `number` | `wait_time` | 累计等待时间（毫秒） |
| RequestSuccess | `int64` | `number` | `request_success` | 成功请求数 |
| RequestFailed | `int64` | `number` | `request_failed` | 失败请求数 |

**行为方法**：
- `Add(delta StatsMetrics)`：将另一个度量累加到当前度量（用于运行时聚合）。

---
