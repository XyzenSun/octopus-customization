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

## 二、协议适配 VO（OpenAI / Anthropic / Gemini 模型列表）

> 这类 VO 不入库，仅用于在 `/v1/models`、`/v1/messages` 等兼容路由上「按上游协议格式」返回模型列表，供第三方客户端识别。
> 源码：`internal/model/llm.go`

### LLMChannel（模型-渠道关联视图）

**用途**：管理面板查询「某模型由哪些渠道提供」的视图对象，由 op 层基于 `Group` + `Channel` + `LLMInfo` 联表组装。

| 字段 | Go 类型 | TS 类型 | JSON 键 | 说明 |
|------|---------|---------|---------|------|
| Name | `string` | `string` | `name` | 模型名称 |
| Enabled | `bool` | `boolean` | `enabled` | 渠道是否启用 |
| ChannelID | `int` | `number` | `channel_id` | 渠道 ID |
| ChannelName | `string` | `string` | `channel_name` | 渠道名称 |

---

### OpenAIModel / OpenAIModelList

**用途**：OpenAI 兼容路由 `GET /v1/models` 的响应格式。

**OpenAIModel：**

| 字段 | Go 类型 | JSON 键 | 说明 |
|------|---------|---------|------|
| ID | `string` | `id` | 模型 ID |
| Object | `string` | `object` | 固定值 `"model"` |
| Created | `int` | `created` | 创建时间戳（秒） |
| OwnedBy | `string` | `owned_by` | 模型所属（如 `openai`） |

**OpenAIModelList（列表包装）：**

| 字段 | Go 类型 | JSON 键 | 说明 |
|------|---------|---------|------|
| Object | `string` | `object` | 固定值 `"list"` |
| Data | `[]OpenAIModel` | `data` | 模型数组 |

---

### AnthropicModel / AnthropicModelList

**用途**：Anthropic 兼容路由 `GET /v1/models` 的响应格式。

**AnthropicModel：**

| 字段 | Go 类型 | JSON 键 | 说明 |
|------|---------|---------|------|
| ID | `string` | `id` | 模型 ID |
| CreatedAt | `string` | `created_at` | 创建时间（ISO 8601 字符串） |
| DisplayName | `string` | `display_name` | 显示名称 |
| Type | `string` | `type` | 固定值 `"model"` |

**AnthropicModelList（列表包装，支持游标分页）：**

| 字段 | Go 类型 | JSON 键 | 说明 |
|------|---------|---------|------|
| Data | `[]AnthropicModel` | `data` | 模型数组 |
| FirstID | `string` | `first_id` | 当前页第一个模型 ID |
| HasMore | `bool` | `has_more` | 是否还有更多 |
| LastID | `string` | `last_id` | 当前页最后一个模型 ID |

---

### GeminiModel / GeminiModelList

**用途**：Gemini 兼容路由 `GET /v1beta/models` 的响应格式。

**GeminiModel：**

| 字段 | Go 类型 | JSON 键 | 说明 |
|------|---------|---------|------|
| Name | `string` | `name` | 模型资源名（如 `models/gemini-pro`） |
| DisplayName | `string` | `displayName` | 显示名称（驼峰命名遵循 Gemini 规范） |
| Description | `string` | `description` | 模型描述 |

**GeminiModelList（列表包装，支持 token 分页）：**

| 字段 | Go 类型 | JSON 键 | 说明 |
|------|---------|---------|------|
| Models | `[]GeminiModel` | `models` | 模型数组 |
| NextPageToken | `string` | `nextPageToken` | 下一页令牌 |

---

## 三、备份/导入 VO

> 由 `Setting` 模块的导入/导出接口使用，将多个实体打包为一份可移植的快照。
> 源码：`internal/model/backup.go`

### DBDump（数据库导出快照）

**用途**：`GET /api/v1/setting/export` 返回的完整数据库 JSON 快照；`POST /api/v1/setting/import` 接收同结构进行增量导入。

| 字段 | Go 类型 | JSON 键 | 说明 |
|------|---------|---------|------|
| Version | `int` | `version` | Dump 格式版本号（用于向前兼容） |
| ExportedAt | `time.Time` | `exported_at` | 导出时间 |
| IncludeLogs | `bool` | `include_logs` | 是否包含日志 |
| IncludeStats | `bool` | `include_stats` | 是否包含统计 |
| Channels | `[]Channel` | `channels,omitempty` | 渠道列表 |
| ChannelKeys | `[]ChannelKey` | `channel_keys,omitempty` | 渠道密钥列表 |
| Groups | `[]Group` | `groups,omitempty` | 分组列表 |
| GroupItems | `[]GroupItem` | `group_items,omitempty` | 分组项列表 |
| LLMInfos | `[]LLMInfo` | `llm_infos,omitempty` | 模型价格表 |
| APIKeys | `[]APIKey` | `api_keys,omitempty` | API Key 列表 |
| Settings | `[]Setting` | `settings,omitempty` | 系统设置 |
| StatsTotal | `[]StatsTotal` | `stats_total,omitempty` | 全局累计统计（仅 IncludeStats 时） |
| StatsDaily | `[]StatsDaily` | `stats_daily,omitempty` | 按日统计 |
| StatsHourly | `[]StatsHourly` | `stats_hourly,omitempty` | 按小时统计 |
| StatsModel | `[]StatsModel` | `stats_model,omitempty` | 按模型统计 |
| StatsChannel | `[]StatsChannel` | `stats_channel,omitempty` | 按渠道统计 |
| StatsAPIKey | `[]StatsAPIKey` | `stats_api_key,omitempty` | 按 API Key 统计 |
| RelayLogs | `[]RelayLog` | `relay_logs,omitempty` | 转发日志（仅 IncludeLogs 时） |

> 导入时按表执行：部分表为 insert（新增不存在的行），部分为 upsert（按主键覆盖），由 op 层根据表语义决定。

---

### DBImportResult（导入结果摘要）

**用途**：`POST /api/v1/setting/import` 的返回值，逐表统计写入行数。

| 字段 | Go 类型 | TS 类型 | JSON 键 | 说明 |
|------|---------|---------|---------|------|
| RowsAffected | `map[string]int64` | `Record<string, number>` | `rows_affected` | 表名 → 受影响行数 |

---

## 四、版本检查 VO

### LatestInfo（GitHub 最新发布信息）

**用途**：从 GitHub Release API 拉取最新版本信息，供前端展示与触发自更新。
**源码**：`internal/update/update.go`

| 字段 | Go 类型 | TS 类型 | JSON 键 | 说明 |
|------|---------|---------|---------|------|
| TagName | `string` | `string` | `tag_name` | Release 标签名（版本号） |
| PublishedAt | `string` | `string` | `published_at` | 发布时间（ISO 8601 字符串） |
| Body | `string` | `string` | `body` | Release Notes 正文（Markdown） |
| Message | `string` | `string` | `message` | GitHub API 错误消息（仅失败时存在） |

> 当 `Message` 非空时，视为请求失败（如限流、未授权），后端会返回错误而不是 LatestInfo。

---

## 五、辅助键值 VO

### GroupIDAndLLMName（分组项查找键）

**用途**：作为 `op` 层批量查找/去重 GroupItem 的临时键，**不暴露给 HTTP 接口**，无 JSON tag。
**源码**：`internal/model/group.go`

| 字段 | Go 类型 | 说明 |
|------|---------|------|
| ChannelID | `int` | 渠道 ID |
| ModelName | `string` | 模型名称 |

> 与 `GroupItem` 的联合唯一索引 `(GroupID, ChannelID, ModelName)` 配合使用。

---

## 六、前端展示 VO（Formatted）

> 后端返回的 `StatsMetrics` 字段都是数值，前端通过 `select()` 将其转换为「格式化字符串」用于直接渲染（千分位、单位、时长等），由 `web/src/lib/utils.ts` 中的 `formatCount` / `formatMoney` / `formatTime` 完成。
> 源码：`web/src/api/endpoints/stats.ts`、`apikey.ts`、`channel.ts`

### StatsMetricsFormatted（基础度量格式化）

**用途**：UI 直接渲染的统计度量；附加了三个聚合字段（请求总数、Token 总数、费用总数），后端不计算这些聚合值。

| 字段 | TS 类型（来源） | 说明 |
|------|----------------|------|
| input_token | `ReturnType<typeof formatCount>` | 输入 Token 数（带千分位/k/M 单位） |
| output_token | `ReturnType<typeof formatCount>` | 输出 Token 数 |
| input_cost | `ReturnType<typeof formatMoney>` | 输入费用（带货币符号） |
| output_cost | `ReturnType<typeof formatMoney>` | 输出费用 |
| wait_time | `ReturnType<typeof formatTime>` | 等待时长（带时间单位） |
| request_success | `ReturnType<typeof formatCount>` | 成功请求数 |
| request_failed | `ReturnType<typeof formatCount>` | 失败请求数 |
| **request_count** | `ReturnType<typeof formatCount>` | **聚合**：success + failed |
| **total_token** | `ReturnType<typeof formatCount>` | **聚合**：input + output |
| **total_cost** | `ReturnType<typeof formatMoney>` | **聚合**：input_cost + output_cost |

---

### Stats*Formatted 派生类型

| 类型 | 继承 | 附加字段 | 来源接口 |
|------|------|----------|---------|
| `StatsTotalFormatted` | `StatsMetricsFormatted`（type alias） | — | `GET /api/v1/stats/total` |
| `StatsDailyFormatted` | `StatsMetricsFormatted` | `date: string` | `GET /api/v1/stats/daily` |
| `StatsHourlyFormatted` | `StatsMetricsFormatted` | `hour: number`, `date: string` | `GET /api/v1/stats/hourly` |
| `StatsAPIKeyFormatted` | `StatsMetricsFormatted` | `api_key_id: number` | `GET /api/v1/stats/apikey` 等 |

> 这些类型仅存在于前端，作为 React Query 的 `select()` 转换结果，不会出现在网络请求中。

---

### APIKeyStatsResponseFormatted（仪表盘复合视图）

**用途**：API Key 登录用户的仪表盘视图，将 API Key 元信息与统计度量打包返回。
**来源接口**：`GET /api/v1/apikey/stats`

| 字段 | TS 类型 | 说明 |
|------|---------|------|
| stats | `StatsAPIKeyFormatted` | 当前 Key 的格式化统计 |
| info | `APIKey` | 当前 Key 的实体信息（不格式化） |

> 后端返回的 `APIKeyStatsResponse` 中 `stats` 是原始 `StatsAPIKey`，前端通过 `select()` 转为 `*Formatted` 版本。

---

### Channel 列表的 raw + formatted 包装

**用途**：`useChannelList()` Hook 返回的项不是原始 `Channel`，而是 `{ raw: Channel, formatted: StatsMetricsFormatted }` 形式，便于在表格行同时使用结构化字段和已格式化文本。

| 字段 | TS 类型 | 说明 |
|------|---------|------|
| raw | `Channel` | 原始渠道实体（含 keys/base_urls 等） |
| formatted | `StatsMetricsFormatted` | 由 `Channel.stats` 派生的格式化度量 |

> 该包装仅由前端的 `select()` 产生；后端始终返回 `Channel[]` 数组。

---

## 七、VO 组合关系图

```
Channel (实体)
  ├─ BaseUrls       []BaseUrl       (嵌入 VO，JSON)
  ├─ CustomHeader   []CustomHeader  (嵌入 VO，JSON)
  └─ Stats          *StatsChannel   (一对一关联实体，含 StatsMetrics)

RelayLog (实体)
  └─ Attempts       []ChannelAttempt (嵌入 VO，JSON)

LLMInfo (实体)
  └─ (嵌入) LLMPrice                (嵌入 VO，平铺字段)

Stats* (六张实体表)
  └─ (嵌入) StatsMetrics            (嵌入 VO，平铺字段)
                ↓
       StatsMetricsFormatted       (前端格式化 VO)
                ↓
        Stats{Daily,Hourly,Total,APIKey}Formatted

DBDump (备份 VO)
  └─ 聚合所有实体 + StatsMetrics 的快照
```

---

## 八、命名约定与设计原则

1. **嵌入 VO**：与所属实体定义在同一文件中，通过 `gorm:"serializer:json"` 或 Go 嵌入语法（无字段名的同类型字段）入表。
2. **协议 VO**：以协议名 + Model/ModelList 命名（`OpenAIModel` / `AnthropicModelList`），仅用于响应序列化。
3. **备份 VO**：以 `DB` 前缀命名（`DBDump` / `DBImportResult`）。
4. **前端展示 VO**：在原类型名后加 `Formatted` 后缀（`StatsMetricsFormatted`），通过 React Query `select()` 转换。
5. **不持久化的辅助键值**：无 JSON tag、无 GORM tag（如 `GroupIDAndLLMName`），仅用于内部映射。
6. **复用优先**：`LLMPrice`、`StatsMetrics` 都通过 Go 匿名字段嵌入到多个实体，避免重复定义；新增统计维度时只需创建空壳实体并嵌入 `StatsMetrics`。

