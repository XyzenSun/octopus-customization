# 数据库实体文档 (ENTITY)

> 本文档描述 Octopus 项目中所有 GORM 数据库实体（持久化到 SQLite/MySQL/PostgreSQL 的模型）。
> 源码位置：`internal/model/*.go`
> AutoMigrate 注册位置：`internal/db/db.go`

---

## Channel（渠道）

| 字段 | 类型 | Go类型 | GORM约束 | JSON键 | 说明 |
|------|------|--------|----------|--------|------|
| ID | integer | `int` | `primaryKey` | `id` | 渠道主键 |
| Name | string | `string` | `unique;not null` | `name` | 渠道名称，全局唯一 |
| Type | string | `llm.APIFormat` | — | `type` | 渠道协议类型（openai_chat_completion / openai_response / anthropic_message / gemini_contents / doubao / openai_embedding） |
| Enabled | boolean | `bool` | `default:true` | `enabled` | 是否启用 |
| BaseUrls | JSON | `[]BaseUrl` | `serializer:json` | `base_urls` | 基础URL列表（含延迟信息） |
| Keys | — | `[]ChannelKey` | `foreignKey:ChannelID` | `keys` | 渠道密钥列表（外键关联） |
| Model | string | `string` | — | `model` | 原始模型名称 |
| CustomModel | string | `string` | — | `custom_model` | 自定义模型名称 |
| Proxy | boolean | `bool` | `default:false` | `proxy` | 是否使用全局代理 |
| AutoSync | boolean | `bool` | `default:false` | `auto_sync` | 是否自动同步模型 |
| AutoGroup | integer | `AutoGroupType` | `default:0` | `auto_group` | 自动分组类型（0=不分组, 1=模糊匹配, 2=精确匹配, 3=正则匹配） |
| CustomHeader | JSON | `[]CustomHeader` | `serializer:json` | `custom_header` | 自定义请求头列表 |
| ParamOverride | string | `*string` | — | `param_override` | 参数覆盖（JSON字符串，nullable） |
| ChannelProxy | string | `*string` | — | `channel_proxy` | 渠道专属代理地址（nullable） |
| Stats | — | `*StatsChannel` | `foreignKey:ChannelID` | `stats,omitempty` | 渠道统计信息（外键关联，nullable） |
| MatchRegex | string | `*string` | — | `match_regex` | 模型匹配正则表达式（nullable） |

**枚举 AutoGroupType：**

| 值 | 常量 | 说明 |
|----|------|------|
| 0 | `AutoGroupTypeNone` | 不自动分组 |
| 1 | `AutoGroupTypeFuzzy` | 模糊匹配 |
| 2 | `AutoGroupTypeExact` | 精确匹配 |
| 3 | `AutoGroupTypeRegex` | 正则匹配 |

**嵌入值对象 BaseUrl（serializer:json，不独立建表）：**

| 字段 | Go类型 | JSON键 | 说明 |
|------|--------|--------|------|
| URL | `string` | `url` | 基础URL地址 |
| Delay | `int` | `delay` | 延迟（ms） |

**嵌入值对象 CustomHeader（serializer:json，不独立建表）：**

| 字段 | Go类型 | JSON键 | 说明 |
|------|--------|--------|------|
| HeaderKey | `string` | `header_key` | 自定义请求头键名 |
| HeaderValue | `string` | `header_value` | 自定义请求头值 |

---

## ChannelKey（渠道密钥）

| 字段 | 类型 | Go类型 | GORM约束 | JSON键 | 说明 |
|------|------|--------|----------|--------|------|
| ID | integer | `int` | `primaryKey` | `id` | 密钥主键 |
| ChannelID | integer | `int` | — | `channel_id` | 所属渠道ID（外键） |
| Enabled | boolean | `bool` | `default:true` | `enabled` | 是否启用 |
| ChannelKey | string | `string` | — | `channel_key` | 密钥字符串 |
| StatusCode | integer | `int` | — | `status_code` | 最近一次请求状态码 |
| LastUseTimeStamp | integer | `int64` | — | `last_use_time_stamp` | 最近使用时间戳（秒） |
| TotalCost | float | `float64` | — | `total_cost` | 累计消耗费用 |
| Remark | string | `string` | — | `remark` | 备注 |

---

## Group（分组）

| 字段 | 类型 | Go类型 | GORM约束 | JSON键 | 说明 |
|------|------|--------|----------|--------|------|
| ID | integer | `int` | `primaryKey` | `id` | 分组主键 |
| Name | string | `string` | `unique;not null` | `name` | 分组名称，全局唯一 |
| Mode | integer | `GroupMode` | `not null` | `mode` | 负载均衡模式（1=轮询, 2=随机, 3=故障转移, 4=加权） |
| MatchRegex | string | `string` | — | `match_regex` | 模型匹配正则表达式 |
| FirstTokenTimeOut | integer | `int` | — | `first_token_time_out` | 单个渠道首个 Token 响应超时时间（秒） |
| SessionKeepTime | integer | `int` | — | `session_keep_time` | 会话保持时间（秒），0 为禁用 |
| Items | — | `[]GroupItem` | `foreignKey:GroupID` | `items,omitempty` | 分组项列表（外键关联） |

**枚举 GroupMode：**

| 值 | 常量 | 说明 |
|----|------|------|
| 1 | `GroupModeRoundRobin` | 轮询：依次循环选择渠道 |
| 2 | `GroupModeRandom` | 随机：每次随机选择一个渠道 |
| 3 | `GroupModeFailover` | 故障转移：按优先级选择，失败时降级 |
| 4 | `GroupModeWeighted` | 加权分配：按权重分配流量 |

---

## GroupItem（分组项）

| 字段 | 类型 | Go类型 | GORM约束 | JSON键 | 说明 |
|------|------|--------|----------|--------|------|
| ID | integer | `int` | `primaryKey` | `id` | 分组项主键 |
| GroupID | integer | `int` | `not null;index:idx_group_channel_model,unique` | `group_id` | 所属分组ID（外键），与 ChannelID + ModelName 构成联合唯一索引 |
| ChannelID | integer | `int` | `not null;index:idx_group_channel_model,unique` | `channel_id` | 关联的渠道ID，与 GroupID + ModelName 构成联合唯一索引 |
| ModelName | string | `string` | `not null;index:idx_group_channel_model,unique` | `model_name` | 模型名称，与 GroupID + ChannelID 构成联合唯一索引 |
| Priority | integer | `int` | — | `priority` | 优先级（用于 Failover 模式） |
| Weight | integer | `int` | — | `weight` | 权重（用于 Weighted 模式） |

> **联合唯一索引 `idx_group_channel_model`**：保证同一个分组下，相同 `(channel_id, model_name)` 组合唯一。

---

## APIKey（API 密钥）

| 字段 | 类型 | Go类型 | GORM约束 | JSON键 | 说明 |
|------|------|--------|----------|--------|------|
| ID | integer | `int` | `primaryKey` | `id` | 主键 |
| Name | string | `string` | `not null` | `name` | API Key 名称（用户标识） |
| APIKey | string | `string` | `not null` | `api_key` | 实际的 API Key 字符串 |
| Enabled | boolean | `bool` | `default:true` | `enabled` | 是否启用 |
| ExpireAt | integer | `int64` | — | `expire_at,omitempty` | 过期时间戳（秒），0 表示永不过期 |
| MaxCost | float | `float64` | — | `max_cost,omitempty` | 最大消费额度，0 表示无限制 |
| SupportedModels | string | `string` | — | `supported_models,omitempty` | 允许使用的模型列表（字符串格式，逗号分隔或正则） |

---

## User（用户）

| 字段 | 类型 | Go类型 | GORM约束 | JSON键 | 说明 |
|------|------|--------|----------|--------|------|
| ID | integer | `uint` | `primaryKey` | — | 用户主键 |
| Username | string | `string` | `unique` | — | 用户名，全局唯一 |
| Password | string | `string` | `not null` | — | 密码（bcrypt 哈希后存储） |

> 默认账号：`admin / admin`，首次启动后应立即修改密码。
> 密码使用 `golang.org/x/crypto/bcrypt` 加密；提供 `HashPassword()` 与 `ComparePassword()` 方法。

---

## Setting（系统设置）

| 字段 | 类型 | Go类型 | GORM约束 | JSON键 | 说明 |
|------|------|--------|----------|--------|------|
| Key | string | `SettingKey` | `primaryKey` | `key` | 设置项键名（主键） |
| Value | string | `string` | `not null` | `value` | 设置项的值（字符串存储，由调用方按类型解析） |

**预定义 SettingKey 列表：**

| 键名 | 默认值 | 说明 |
|------|--------|------|
| `proxy_url` | `""` | 全局代理 URL（支持 http/https/socks5） |
| `stats_save_interval` | `10` | 统计信息写入数据库的周期（分钟） |
| `model_info_update_interval` | `24` | 模型信息更新间隔（小时） |
| `sync_llm_interval` | `24` | LLM 同步间隔（小时） |
| `relay_log_keep_period` | `7` | 日志保存时间范围（天） |
| `relay_log_mode` | `persistent` | 日志模式（disabled/memory/persistent） |
| `cors_allow_origins` | `""` | 跨域白名单（逗号分隔；`""` 不允许跨域，`*` 允许所有） |
| `circuit_breaker_threshold` | `5` | 熔断触发阈值（连续失败次数） |
| `circuit_breaker_cooldown` | `60` | 熔断基础冷却时间（秒） |
| `circuit_breaker_max_cooldown` | `600` | 熔断最大冷却时间（秒，指数退避上限） |

> 数值类设置由 `Validate()` 校验为整数；`relay_log_mode` 仅接受 `disabled/memory/persistent`；`proxy_url` 校验 scheme 为 http/https/socks5 且 host 非空。

---

## LLMInfo（LLM 模型信息 / 价格表）

| 字段 | 类型 | Go类型 | GORM约束 | JSON键 | 说明 |
|------|------|--------|----------|--------|------|
| Name | string | `string` | `primaryKey;not null` | `name` | 模型名称（主键） |
| Input | float | `float64` | — | `input` | 输入 Token 单价 |
| Output | float | `float64` | — | `output` | 输出 Token 单价 |
| CacheRead | float | `float64` | — | `cache_read` | 缓存读取 Token 单价 |
| CacheWrite | float | `float64` | — | `cache_write` | 缓存写入 Token 单价 |

> 嵌入了 `LLMPrice` 结构体（包含 Input/Output/CacheRead/CacheWrite 四个字段）。

---

## RelayLog（转发日志）

| 字段 | 类型 | Go类型 | GORM约束 | JSON键 | 说明 |
|------|------|--------|----------|--------|------|
| ID | integer | `int64` | `primaryKey;autoIncrement:false` | `id` | 主键，使用 Snowflake ID 生成 |
| Time | integer | `int64` | — | `time` | 请求时间戳（秒） |
| RequestModelName | string | `string` | — | `request_model_name` | 请求的模型名称 |
| RequestAPIKeyName | string | `string` | — | `request_api_key_name` | 请求使用的 API Key 名称 |
| ChannelId | integer | `int` | — | `channel` | 实际使用的渠道ID |
| ChannelName | string | `string` | — | `channel_name` | 渠道名称 |
| ActualModelName | string | `string` | — | `actual_model_name` | 实际使用的模型名称（可能与请求模型不同） |
| InputTokens | integer | `int` | — | `input_tokens` | 输入 Token 数 |
| OutputTokens | integer | `int` | — | `output_tokens` | 输出 Token 数 |
| Ftut | integer | `int` | — | `ftut` | 首字时间（毫秒，First Token Use Time） |
| UseTime | integer | `int` | — | `use_time` | 总用时（毫秒） |
| Cost | float | `float64` | — | `cost` | 本次请求消耗费用 |
| RequestContent | string | `string` | — | `request_content` | 请求内容（原始 body） |
| ResponseContent | string | `string` | — | `response_content` | 响应内容（原始 body） |
| Error | string | `string` | — | `error` | 错误信息 |
| Attempts | JSON | `[]ChannelAttempt` | `serializer:json` | `attempts` | 所有渠道尝试记录 |
| TotalAttempts | integer | `int` | — | `total_attempts` | 总尝试次数 |

**嵌入值对象 ChannelAttempt（serializer:json，不独立建表）：**

| 字段 | Go类型 | JSON键 | 说明 |
|------|--------|--------|------|
| ChannelID | `int` | `channel_id` | 渠道ID |
| ChannelKeyID | `int` | `channel_key_id,omitempty` | 渠道密钥ID |
| ChannelName | `string` | `channel_name` | 渠道名称 |
| ModelName | `string` | `model_name` | 使用的模型名 |
| AttemptNum | `int` | `attempt_num` | 第几次尝试 |
| Status | `AttemptStatus` | `status` | 尝试状态 |
| Duration | `int` | `duration` | 本次尝试耗时（毫秒） |
| Sticky | `bool` | `sticky,omitempty` | 是否会话保持命中 |
| Msg | `string` | `msg,omitempty` | 附加消息（错误或跳过原因） |

**枚举 AttemptStatus：**

| 值 | 常量 | 说明 |
|----|------|------|
| `success` | `AttemptSuccess` | 转发成功 |
| `failed` | `AttemptFailed` | 转发失败 |
| `circuit_break` | `AttemptCircuitBreak` | 熔断跳过 |
| `skipped` | `AttemptSkipped` | 其他原因跳过（禁用/无 Key/类型不兼容等） |

> 日志保留时间由 `Setting.relay_log_keep_period` 控制；日志模式由 `Setting.relay_log_mode` 控制（disabled=彻底关闭，memory=内存缓存，persistent=持久化保存）。

---

## StatsMetrics（统计度量 - 嵌入结构体）

> **不独立建表**，作为公共字段嵌入到所有 Stats* 表中。

| 字段 | 类型 | Go类型 | GORM约束 | JSON键 | 说明 |
|------|------|--------|----------|--------|------|
| InputToken | bigint | `int64` | `bigint` | `input_token` | 累计输入 Token 数 |
| OutputToken | bigint | `int64` | `bigint` | `output_token` | 累计输出 Token 数 |
| InputCost | real | `float64` | `type:real` | `input_cost` | 累计输入 Token 费用 |
| OutputCost | real | `float64` | `type:real` | `output_cost` | 累计输出 Token 费用 |
| WaitTime | bigint | `int64` | `bigint` | `wait_time` | 累计等待时间（毫秒，用于均值计算） |
| RequestSuccess | bigint | `int64` | `bigint` | `request_success` | 成功请求数 |
| RequestFailed | bigint | `int64` | `bigint` | `request_failed` | 失败请求数 |

> 提供 `Add(delta StatsMetrics)` 方法用于累加合并。

---

## StatsTotal（全局累计统计）

| 字段 | 类型 | Go类型 | GORM约束 | 说明 |
|------|------|--------|----------|------|
| ID | integer | `int` | `primaryKey` | 主键（通常单行，固定 ID） |
| *（嵌入）* | — | `StatsMetrics` | — | 见 StatsMetrics 表 |

---

## StatsDaily（按日统计）

| 字段 | 类型 | Go类型 | GORM约束 | JSON键 | 说明 |
|------|------|--------|----------|--------|------|
| Date | string | `string` | `primaryKey` | `date` | 日期主键，格式 `20060102` |
| *（嵌入）* | — | `StatsMetrics` | — | — | 见 StatsMetrics 表 |

---

## StatsHourly（按小时统计）

| 字段 | 类型 | Go类型 | GORM约束 | JSON键 | 说明 |
|------|------|--------|----------|--------|------|
| Hour | integer | `int` | `primaryKey` | `hour` | 小时主键（0-23） |
| Date | string | `string` | `not null` | `date` | 最后更新日期，格式 `20060102` |
| *（嵌入）* | — | `StatsMetrics` | — | — | 见 StatsMetrics 表 |

> 小时统计是滚动表：相同小时号会被跨天复用，`Date` 字段用于判断当前数据归属哪一天。

---

## StatsModel（按模型统计）

| 字段 | 类型 | Go类型 | GORM约束 | JSON键 | 说明 |
|------|------|--------|----------|--------|------|
| ID | integer | `int` | `primaryKey` | `id` | 主键 |
| Name | string | `string` | `not null` | `name` | 模型名称 |
| ChannelID | integer | `int` | `not null` | `channel_id` | 渠道ID |
| *（嵌入）* | — | `StatsMetrics` | — | — | 见 StatsMetrics 表 |

> 同一个模型在不同渠道的统计独立记录。

---

## StatsChannel（按渠道统计）

| 字段 | 类型 | Go类型 | GORM约束 | JSON键 | 说明 |
|------|------|--------|----------|--------|------|
| ChannelID | integer | `int` | `primaryKey` | `channel_id` | 渠道ID（主键） |
| *（嵌入）* | — | `StatsMetrics` | — | — | 见 StatsMetrics 表 |

> 与 `Channel` 表通过 `ChannelID` 一对一关联（`Channel.Stats` 字段）。

---

## StatsAPIKey（按 API Key 统计）

| 字段 | 类型 | Go类型 | GORM约束 | JSON键 | 说明 |
|------|------|--------|----------|--------|------|
| APIKeyID | integer | `int` | `primaryKey` | `api_key_id` | API Key ID（主键） |
| *（嵌入）* | — | `StatsMetrics` | — | — | 见 StatsMetrics 表 |

---

## MigrationRecord（迁移记录）

> 系统内部表，由 `internal/db/migrate/migrate.go` 维护，用于记录已执行的数据库迁移版本，避免重复执行。

| 字段 | 类型 | Go类型 | GORM约束 | 说明 |
|------|------|--------|----------|------|
| Version | integer | `int` | `primaryKey` | 迁移版本号 |
| Status | integer | `MigrationRecordStatus` | — | 执行状态（1=成功, 2=失败） |

**枚举 MigrationRecordStatus：**

| 值 | 常量 | 说明 |
|----|------|------|
| 1 | `MigrationRecordStatusSuccess` | 迁移成功 |
| 2 | `MigrationRecordStatusFailed` | 迁移失败 |

> 迁移分为两个阶段：`BeforeAutoMigrate`（在 GORM AutoMigrate 之前执行，如改列类型）、`AfterAutoMigrate`（在 AutoMigrate 之后执行，如数据迁移和清理旧字段）。

---

## 实体关系总览

```
User (独立)
Setting (独立 KV)
LLMInfo (独立，模型价格表)
APIKey (独立)
MigrationRecord (系统表)

Channel ─┬─ 1:N ─→ ChannelKey
         └─ 1:1 ─→ StatsChannel

Group ── 1:N ─→ GroupItem
              └─ 引用 Channel.ID + ModelName

RelayLog ── 嵌入 ─→ []ChannelAttempt (JSON)

StatsMetrics (嵌入)
  ├─ StatsTotal
  ├─ StatsDaily
  ├─ StatsHourly
  ├─ StatsModel (按 Channel + Name 区分)
  ├─ StatsChannel (按 Channel 关联)
  └─ StatsAPIKey (按 APIKey 关联)
```

**关键约束：**
- `Channel.Name`、`Group.Name`、`User.Username`、`LLMInfo.Name` 全局唯一
- `GroupItem` 的 `(GroupID, ChannelID, ModelName)` 联合唯一
- `RelayLog.ID` 使用 Snowflake ID（不自动递增）
- 所有 GORM 实体经 `internal/db/db.go` 中的 `AutoMigrate` 自动建表
- 数据库迁移通过 `internal/db/migrate/` 中的版本化脚本管理（当前版本 1、2、3）