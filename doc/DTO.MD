# DTO 文档（Data Transfer Object）

> 本文档描述 Octopus 后端 HTTP 接口的请求 / 响应数据传输对象。
> 源码位置：`internal/model/*.go`（DTO 定义）、`internal/server/handlers/*.go`（绑定使用）、`internal/server/resp/*.go`（统一响应封装）
> 路径前缀：管理 API `/api/v1/*`，OpenAI 兼容路由 `/v1/*`

---

## 通用响应封装

所有 `/api/v1/*` 接口返回统一的 JSON 包装结构（除文件下载、SSE 流外）。

### ResponseStruct（响应外层）

| 字段 | Go类型 | JSON键 | 说明 |
|------|--------|--------|------|
| Code | `int` | `code` | HTTP 状态码（200 表示成功） |
| Message | `string` | `message` | 成功为 `"success"`，失败为错误描述 |
| Data | `interface{}` | `data,omitempty` | 业务数据载荷（成功时返回） |

**成功响应示例：**

```json
{ "code": 200, "message": "success", "data": { ... } }
```

**错误响应示例：**

```json
{ "code": 400, "message": "Invalid JSON format" }
```

### 错误消息常量（`internal/server/resp/error.go`）

| 常量 | 值 | 适用场景 |
|------|----|---------|
| `ErrBadRequest` | `Invalid request parameters` | 参数缺失或非法 |
| `ErrInvalidJSON` | `Invalid JSON format` | JSON 反序列化失败 |
| `ErrInvalidParam` | `Invalid parameter` | URL 参数解析失败 |
| `ErrValidation` | `Input validation failed` | binding 校验失败 |
| `ErrDuplicateResource` | `Resource already exists` | 唯一性冲突 |
| `ErrResourceNotFound` | `Resource not found` | 资源不存在 |
| `ErrInternalServer` | `An unexpected error occurred` | 内部错误 |
| `ErrDatabase` | `Database operation failed` | 数据库操作失败 |
| `ErrUnauthorized` | `Authentication failed` | 认证失败 |

---

## 用户模块（User）

路径前缀 `/api/v1/user`

### 请求 DTO

#### UserLogin（登录请求）

`POST /api/v1/user/login`

| 字段 | Go类型 | JSON键 | binding | 说明 |
|------|--------|--------|---------|------|
| Username | `string` | `username` | — | 用户名 |
| Password | `string` | `password` | — | 密码（明文，由后端 bcrypt 比对） |
| Expire | `int` | `expire` | — | Token 过期时长（小时） |

#### UserChangePassword（修改密码请求）

`POST /api/v1/user/change-password`（需 JWT）

| 字段 | Go类型 | JSON键 | binding | 说明 |
|------|--------|--------|---------|------|
| OldPassword | `string` | `old_password` | — | 旧密码 |
| NewPassword | `string` | `new_password` | — | 新密码 |

#### UserChangeUsername（修改用户名请求）

`POST /api/v1/user/change-username`（需 JWT）

| 字段 | Go类型 | JSON键 | binding | 说明 |
|------|--------|--------|---------|------|
| NewUsername | `string` | `new_username` | — | 新用户名 |

### 响应 DTO

#### UserLoginResponse（登录响应）

| 字段 | Go类型 | JSON键 | 说明 |
|------|--------|--------|------|
| Token | `string` | `token` | JWT Token |
| ExpireAt | `string` | `expire_at` | 过期时间（字符串格式） |

#### 其它接口

| 接口 | 响应 `data` | 说明 |
|------|------------|------|
| `/change-password` | `"password changed successfully"` | 字符串提示 |
| `/change-username` | `"username changed successfully"` | 字符串提示 |
| `/status` | `"ok"` | 健康检查 |

---

## 渠道模块（Channel）

路径前缀 `/api/v1/channel`，全部需要 JWT 鉴权。

### 请求 DTO

#### 创建渠道

`POST /api/v1/channel/create`

请求体直接绑定到 `model.Channel`（参考 `ENTITY.md` 中的 Channel 实体）。常用字段：

| 字段 | Go类型 | JSON键 | 说明 |
|------|--------|--------|------|
| Name | `string` | `name` | 渠道名（唯一） |
| Type | `llm.APIFormat` | `type` | 协议类型 |
| Enabled | `bool` | `enabled` | 是否启用 |
| BaseUrls | `[]BaseUrl` | `base_urls` | 基础 URL 列表 |
| Keys | `[]ChannelKey` | `keys` | 渠道密钥列表 |
| Model | `string` | `model` | 原始模型名 |
| CustomModel | `string` | `custom_model` | 自定义模型名 |
| Proxy | `bool` | `proxy` | 是否使用全局代理 |
| AutoSync | `bool` | `auto_sync` | 是否自动同步模型 |
| AutoGroup | `AutoGroupType` | `auto_group` | 自动分组类型 |
| CustomHeader | `[]CustomHeader` | `custom_header` | 自定义请求头列表 |
| ParamOverride | `*string` | `param_override` | 参数覆盖（JSON 字符串） |
| ChannelProxy | `*string` | `channel_proxy` | 渠道专属代理 |
| MatchRegex | `*string` | `match_regex` | 模型匹配正则 |

#### ChannelUpdateRequest（增量更新请求）

`POST /api/v1/channel/update`

> 仅传变更字段；指针类型 `nil` 表示不更新。Keys 通过 add/update/delete 三组数组分开操作。

| 字段 | Go类型 | JSON键 | binding | 说明 |
|------|--------|--------|---------|------|
| ID | `int` | `id` | `required` | 渠道ID |
| Name | `*string` | `name,omitempty` | — | 名称 |
| Type | `*llm.APIFormat` | `type,omitempty` | — | 协议类型 |
| Enabled | `*bool` | `enabled,omitempty` | — | 启用状态 |
| BaseUrls | `*[]BaseUrl` | `base_urls,omitempty` | — | 基础URL列表 |
| Model | `*string` | `model,omitempty` | — | 原始模型名 |
| CustomModel | `*string` | `custom_model,omitempty` | — | 自定义模型名 |
| Proxy | `*bool` | `proxy,omitempty` | — | 全局代理 |
| AutoSync | `*bool` | `auto_sync,omitempty` | — | 自动同步 |
| AutoGroup | `*AutoGroupType` | `auto_group,omitempty` | — | 自动分组类型 |
| CustomHeader | `*[]CustomHeader` | `custom_header,omitempty` | — | 自定义请求头 |
| ChannelProxy | `*string` | `channel_proxy,omitempty` | — | 渠道代理 |
| ParamOverride | `*string` | `param_override,omitempty` | — | 参数覆盖 |
| MatchRegex | `*string` | `match_regex,omitempty` | — | 模型匹配正则 |
| KeysToAdd | `[]ChannelKeyAddRequest` | `keys_to_add,omitempty` | — | 新增的密钥 |
| KeysToUpdate | `[]ChannelKeyUpdateRequest` | `keys_to_update,omitempty` | — | 更新的密钥 |
| KeysToDelete | `[]int` | `keys_to_delete,omitempty` | — | 删除的密钥 ID 列表 |

##### ChannelKeyAddRequest

| 字段 | Go类型 | JSON键 | binding | 说明 |
|------|--------|--------|---------|------|
| Enabled | `bool` | `enabled` | — | 启用状态 |
| ChannelKey | `string` | `channel_key` | `required` | 密钥字符串 |
| Remark | `string` | `remark` | — | 备注 |

##### ChannelKeyUpdateRequest

| 字段 | Go类型 | JSON键 | binding | 说明 |
|------|--------|--------|---------|------|
| ID | `int` | `id` | `required` | 密钥ID |
| Enabled | `*bool` | `enabled,omitempty` | — | 启用状态 |
| ChannelKey | `*string` | `channel_key,omitempty` | — | 密钥字符串 |
| Remark | `*string` | `remark,omitempty` | — | 备注 |

#### 启用/禁用渠道

`POST /api/v1/channel/enable`（匿名内联结构）

| 字段 | Go类型 | JSON键 | 说明 |
|------|--------|--------|------|
| ID | `int` | `id` | 渠道ID |
| Enabled | `bool` | `enabled` | 启用状态 |

#### 删除渠道

`DELETE /api/v1/channel/delete/:id`，无请求体；`id` 为路径参数（int）。

#### 获取模型列表（探测）

`POST /api/v1/channel/fetch-model`，请求体为完整 `model.Channel`，用于探测远端可用模型。

#### 同步渠道模型

`POST /api/v1/channel/sync`，无请求体（手动触发同步任务）。

### 响应 DTO

| 接口 | 响应 `data` | 说明 |
|------|------------|------|
| `/list` | `[]Channel` | 渠道列表，每项填充 `Stats` 字段 |
| `/create` | `Channel` | 创建成功的渠道（含 `Stats`） |
| `/update` | `Channel` | 更新后的渠道（含 `Stats`） |
| `/enable` | `null` | — |
| `/delete/:id` | `null` | — |
| `/fetch-model` | `[]string` | 远端可用模型名列表 |
| `/sync` | `null` | — |
| `/last-sync-time` | `time.Time` | 最近一次同步时间 |

---

## 分组模块（Group）

路径前缀 `/api/v1/group`，全部需要 JWT 鉴权。

### 请求 DTO

#### 创建分组

`POST /api/v1/group/create`，请求体绑定到 `model.Group`：

| 字段 | Go类型 | JSON键 | 说明 |
|------|--------|--------|------|
| Name | `string` | `name` | 分组名（唯一） |
| Mode | `GroupMode` | `mode` | 负载均衡模式（1-4） |
| MatchRegex | `string` | `match_regex` | 模型匹配正则（ECMAScript） |
| FirstTokenTimeOut | `int` | `first_token_time_out` | 首字超时（秒） |
| SessionKeepTime | `int` | `session_keep_time` | 会话保持时间（秒） |
| Items | `[]GroupItem` | `items,omitempty` | 分组项 |

> `MatchRegex` 非空时，会用 `regexp2.ECMAScript` 校验合法性。

#### GroupUpdateRequest（增量更新请求）

`POST /api/v1/group/update`

| 字段 | Go类型 | JSON键 | binding | 说明 |
|------|--------|--------|---------|------|
| ID | `int` | `id` | `required` | 分组ID |
| Name | `*string` | `name,omitempty` | — | 名称 |
| Mode | `*GroupMode` | `mode,omitempty` | — | 模式 |
| MatchRegex | `*string` | `match_regex,omitempty` | — | 匹配正则 |
| FirstTokenTimeOut | `*int` | `first_token_time_out,omitempty` | — | 首字超时（秒） |
| SessionKeepTime | `*int` | `session_keep_time,omitempty` | — | 会话保持时间（秒） |
| ItemsToAdd | `[]GroupItemAddRequest` | `items_to_add,omitempty` | — | 新增分组项 |
| ItemsToUpdate | `[]GroupItemUpdateRequest` | `items_to_update,omitempty` | — | 更新分组项 |
| ItemsToDelete | `[]int` | `items_to_delete,omitempty` | — | 删除分组项ID |

##### GroupItemAddRequest

| 字段 | Go类型 | JSON键 | binding | 说明 |
|------|--------|--------|---------|------|
| ChannelID | `int` | `channel_id` | `required` | 渠道ID |
| ModelName | `string` | `model_name` | `required` | 模型名称 |
| Priority | `int` | `priority,omitempty` | — | 优先级 |
| Weight | `int` | `weight,omitempty` | — | 权重 |

##### GroupItemUpdateRequest

| 字段 | Go类型 | JSON键 | binding | 说明 |
|------|--------|--------|---------|------|
| ID | `int` | `id` | `required` | 分组项ID |
| Priority | `int` | `priority,omitempty` | — | 优先级 |
| Weight | `int` | `weight,omitempty` | — | 权重 |

#### 删除分组

`DELETE /api/v1/group/delete/:id`，无请求体。

### 响应 DTO

| 接口 | 响应 `data` | 说明 |
|------|------------|------|
| `/list` | `[]Group` | 分组列表（含 `Items`） |
| `/create` | `Group` | 创建成功的分组 |
| `/update` | `Group` | 更新后的分组 |
| `/delete/:id` | `"group deleted successfully"` | 字符串提示 |

---

## API Key 模块（APIKey）

路径前缀 `/api/v1/apikey`。

| 接口 | 鉴权 | 说明 |
|------|------|------|
| `POST /create` | JWT | 创建 API Key（后端自动生成 key 字符串） |
| `GET /list` | JWT | 列表 |
| `POST /update` | JWT | 更新（按完整对象） |
| `DELETE /delete/:id` | JWT | 删除 |
| `GET /stats` | API Key | 当前 API Key 的统计与信息 |
| `GET /login` | API Key | 仅返回成功（用于登录探测） |

### 请求 DTO

#### 创建 / 更新

请求体绑定到 `model.APIKey`：

| 字段 | Go类型 | JSON键 | 说明 |
|------|--------|--------|------|
| ID | `int` | `id` | 主键（更新时必填） |
| Name | `string` | `name` | 名称 |
| APIKey | `string` | `api_key` | 密钥（创建时由后端生成并覆盖） |
| Enabled | `bool` | `enabled` | 启用状态 |
| ExpireAt | `int64` | `expire_at,omitempty` | 过期时间戳（秒，0 = 永不过期） |
| MaxCost | `float64` | `max_cost,omitempty` | 最大额度（0 = 无限制） |
| SupportedModels | `string` | `supported_models,omitempty` | 允许使用的模型（逗号分隔） |

### 响应 DTO

| 接口 | 响应 `data` | 说明 |
|------|------------|------|
| `/create` | `APIKey` | 含后端生成的 `api_key` |
| `/list` | `[]APIKey` | 列表 |
| `/update` | `APIKey` | 更新后的对象 |
| `/delete/:id` | `null` | — |
| `/stats` | `{stats, info}` | 见下方 |
| `/login` | `null` | — |

#### `/stats` 复合响应

```json
{
  "code": 200,
  "message": "success",
  "data": {
    "stats": { "input_token": 123, "output_token": 456, "input_cost": 0.01, ... },
    "info":  { "id": 1, "name": "test", "supported_models": "gpt-4o, claude-3-5-sonnet", ... }
  }
}
```

| 键 | Go类型 | 说明 |
|----|--------|------|
| `stats` | `StatsAPIKey` | 当前 API Key 的统计度量 |
| `info` | `APIKey` | 当前 API Key 信息（`supported_models` 字段已被解析为允许的模型字符串） |

> 当 `info.SupportedModels` 为空时，返回所有可用模型；否则按交集过滤。

---

## 设置模块（Setting）

路径前缀 `/api/v1/setting`，全部需要 JWT 鉴权。

### 请求 DTO

#### 设置项更新

`POST /api/v1/setting/set`，请求体绑定到 `model.Setting`：

| 字段 | Go类型 | JSON键 | 说明 |
|------|--------|--------|------|
| Key | `SettingKey` | `key` | 设置项键名（参见 ENTITY.md 中的列表） |
| Value | `string` | `value` | 字符串形式的值 |

> 设置项写入前会调用 `Validate()` 校验；当 `Key` 为 `model_info_update_interval` 或 `sync_llm_interval` 时，会同步刷新对应定时任务的间隔。

#### 数据库导出

`GET /api/v1/setting/export?include_logs={bool}&include_stats={bool}`

| Query 参数 | 类型 | 默认值 | 说明 |
|-----------|------|--------|------|
| `include_logs` | bool | false | 是否导出 RelayLog |
| `include_stats` | bool | false | 是否导出全部 Stats* |

#### 数据库导入

`POST /api/v1/setting/import`

支持两种 Content-Type：

| Content-Type | Body 来源 |
|--------------|----------|
| `multipart/form-data` | 表单字段名 `file`（上传 JSON 文件） |
| 其它 | 请求体直接为 `DBDump` JSON 字符串（也兼容包了一层 `{code, message, data: DBDump}` 的格式） |

##### DBDump（导入 / 导出主体结构）

| 字段 | Go类型 | JSON键 | 说明 |
|------|--------|--------|------|
| Version | `int` | `version` | 导出版本号 |
| ExportedAt | `time.Time` | `exported_at` | 导出时间 |
| IncludeLogs | `bool` | `include_logs` | 是否包含日志 |
| IncludeStats | `bool` | `include_stats` | 是否包含统计 |
| Channels | `[]Channel` | `channels,omitempty` | — |
| ChannelKeys | `[]ChannelKey` | `channel_keys,omitempty` | — |
| Groups | `[]Group` | `groups,omitempty` | — |
| GroupItems | `[]GroupItem` | `group_items,omitempty` | — |
| LLMInfos | `[]LLMInfo` | `llm_infos,omitempty` | — |
| APIKeys | `[]APIKey` | `api_keys,omitempty` | — |
| Settings | `[]Setting` | `settings,omitempty` | — |
| StatsTotal | `[]StatsTotal` | `stats_total,omitempty` | — |
| StatsDaily | `[]StatsDaily` | `stats_daily,omitempty` | — |
| StatsHourly | `[]StatsHourly` | `stats_hourly,omitempty` | — |
| StatsModel | `[]StatsModel` | `stats_model,omitempty` | — |
| StatsChannel | `[]StatsChannel` | `stats_channel,omitempty` | — |
| StatsAPIKey | `[]StatsAPIKey` | `stats_api_key,omitempty` | — |
| RelayLogs | `[]RelayLog` | `relay_logs,omitempty` | — |

### 响应 DTO

| 接口 | 响应 `data` | 说明 |
|------|------------|------|
| `/list` | `[]Setting` | 全部设置项 |
| `/set` | `Setting` | 更新后的设置项 |
| `/export` | `DBDump`（直接 JSON 文件下载） | 通过 `Content-Disposition` 触发下载，文件名带时间戳 |
| `/import` | `DBImportResult` | 见下方 |

#### DBImportResult

| 字段 | Go类型 | JSON键 | 说明 |
|------|--------|--------|------|
| RowsAffected | `map[string]int64` | `rows_affected` | 各表受影响的行数 |

---

## 模型模块（Model / LLM）

含两组路由：管理类 `/api/v1/model/*`（JWT），OpenAI 兼容 `/v1/models`（API Key）。

### 请求 DTO

#### 创建 / 更新模型价格

`POST /api/v1/model/create` 或 `POST /api/v1/model/update`，请求体绑定到 `model.LLMInfo`：

| 字段 | Go类型 | JSON键 | 说明 |
|------|--------|--------|------|
| Name | `string` | `name` | 模型名（主键） |
| Input | `float64` | `input` | 输入 Token 单价 |
| Output | `float64` | `output` | 输出 Token 单价 |
| CacheRead | `float64` | `cache_read` | 缓存读单价 |
| CacheWrite | `float64` | `cache_write` | 缓存写单价 |

#### 删除模型（匿名内联结构）

`POST /api/v1/model/delete`

| 字段 | Go类型 | JSON键 | binding | 说明 |
|------|--------|--------|---------|------|
| Name | `string` | `name` | `required` | 模型名 |

#### 更新模型价格库 / 获取上次更新时间

`POST /api/v1/model/update-price`、`GET /api/v1/model/last-update-time`，无请求体。

#### 同步渠道模型

`GET /api/v1/model/channel`，无请求体。

### 响应 DTO

| 接口 | 响应 `data` | 说明 |
|------|------------|------|
| `/list` | `[]LLMInfo` | 模型价格列表 |
| `/create` | `LLMInfo` | — |
| `/update` | `LLMInfo` | — |
| `/delete` | `null` | — |
| `/channel` | `[]LLMChannel` | 渠道维度的模型列表 |
| `/update-price` | `null` | — |
| `/last-update-time` | `time.Time` | — |

#### LLMChannel（响应项）

| 字段 | Go类型 | JSON键 | 说明 |
|------|--------|--------|------|
| Name | `string` | `name` | 模型名 |
| Enabled | `bool` | `enabled` | 是否启用 |
| ChannelID | `int` | `channel_id` | 渠道ID |
| ChannelName | `string` | `channel_name` | 渠道名 |

### OpenAI / Anthropic 兼容响应

`GET /v1/models`（需 API Key 认证），**响应不使用 ResponseStruct 包装**，按客户端协议输出。

#### 当 `request_type=openai`

```json
{
  "success": true,
  "object": "list",
  "data": [
    { "id": "gpt-4o", "object": "model", "created": 1763395200, "owned_by": "octopus" }
  ]
}
```

##### OpenAIModel

| 字段 | Go类型 | JSON键 | 说明 |
|------|--------|--------|------|
| ID | `string` | `id` | 模型ID |
| Object | `string` | `object` | 固定 `model` |
| Created | `int` | `created` | 创建时间戳 |
| OwnedBy | `string` | `owned_by` | 固定 `octopus` |

#### 当 `request_type=anthropic`

```json
{
  "data": [
    { "id": "claude-3-5-sonnet", "created_at": "2024-01-01T00:00:00Z", "display_name": "claude-3-5-sonnet", "type": "model" }
  ],
  "has_more": false,
  "first_id": "claude-3-5-sonnet",
  "last_id": "claude-3-5-sonnet"
}
```

##### AnthropicModel

| 字段 | Go类型 | JSON键 | 说明 |
|------|--------|--------|------|
| ID | `string` | `id` | 模型ID |
| CreatedAt | `string` | `created_at` | 固定 `2024-01-01T00:00:00Z` |
| DisplayName | `string` | `display_name` | 显示名 |
| Type | `string` | `type` | 固定 `model` |

> 上游同步使用的 `GeminiModel`、`OpenAIModelList`、`AnthropicModelList` 等结构定义同源（`internal/model/llm.go`），仅在与上游适配时出现，非对外 DTO。

---

## 日志模块（Log）

路径前缀 `/api/v1/log`。

| 接口 | 鉴权 | 说明 |
|------|------|------|
| `GET /list` | JWT | 分页查询日志 |
| `DELETE /clear` | JWT | 清空日志 |
| `GET /stream-token` | JWT | 申请 SSE 临时令牌 |
| `GET /stream` | Token Query | SSE 实时日志推送 |

### 请求 DTO

#### 日志列表 Query 参数

`GET /api/v1/log/list`

| 参数 | 类型 | 默认 | 说明 |
|------|------|------|------|
| `page` | int | 1 | 页码（最小 1） |
| `page_size` | int | 20 | 每页数量（1–100，超出回退为 20） |
| `start_time` | int | — | 起始时间戳（秒），与 `end_time` 同时提供时启用区间过滤 |
| `end_time` | int | — | 结束时间戳（秒） |

#### SSE 流参数

`GET /api/v1/log/stream?token={token}`

| 参数 | 类型 | 说明 |
|------|------|------|
| `token` | string | 由 `/stream-token` 接口生成的一次性令牌 |

### 响应 DTO

| 接口 | 响应 | 说明 |
|------|------|------|
| `/list` | `[]RelayLog` | 日志列表 |
| `/clear` | `null` | — |
| `/stream-token` | `{ "token": "..." }` | 一次性令牌 |
| `/stream` | `text/event-stream` | SSE 流，`data: {RelayLog JSON}\n\n`，每条日志一帧 |

> SSE 不使用 `ResponseStruct` 包装；headers 含 `Cache-Control: no-cache`、`X-Accel-Buffering: no`。

---

## 统计模块（Stats）

路径前缀 `/api/v1/stats`，全部需要 JWT 鉴权。无请求体。

| 接口 | 响应 `data` | 说明 |
|------|------------|------|
| `GET /today` | `StatsMetrics` | 今日累计统计 |
| `GET /daily` | `[]StatsDaily` | 按日统计列表 |
| `GET /hourly` | `[]StatsHourly` | 按小时统计列表 |
| `GET /total` | `StatsMetrics` | 全局累计 |
| `GET /apikey` | `[]StatsAPIKey` | 按 API Key 维度统计 |

> `StatsMetrics`、`StatsDaily`、`StatsHourly`、`StatsAPIKey` 字段定义详见 `ENTITY.md`。

---

## 更新模块（Update）

路径前缀 `/api/v1/update`，全部需要 JWT 鉴权。无请求体。

| 接口 | 响应 `data` | 说明 |
|------|------------|------|
| `GET /` | `UpdateInfo` | 远端最新版本信息 |
| `GET /now-version` | `string` | 当前运行版本（来自 `conf.Version`） |
| `POST /` | `"update success"` | 触发自动更新（拉取并替换二进制） |

> `UpdateInfo` 字段由 `internal/update` 提供，含版本号、下载地址、changelog 等。具体字段以源码为准。

