# API 文档

> 本文档描述 Octopus 项目所有 HTTP 接口。
> 管理 API 路径前缀：`/api/v1/*`；LLM 兼容路由前缀：`/v1/*`

---

## 认证方式

| 方式 | 说明 | 适用路由 |
|------|------|---------|
| JWT Bearer | `Authorization: Bearer <jwt_token>` | 管理类接口 `/api/v1/*` |
| API Key Bearer | `Authorization: Bearer <api_key>` | 终端用户接口 `/v1/*`、`/api/v1/apikey/stats`、`/api/v1/apikey/login` |
| Stream Token | Query `?token=<stream_token>` | SSE 日志流 `/api/v1/log/stream` |

---

## 通用响应格式

除文件下载、SSE 流外，所有 `/api/v1/*` 接口均返回统一 JSON 包装：

```json
{ "code": 200, "message": "success", "data": { ... } }
{ "code": 400, "message": "错误描述" }
```

---

## 用户模块（User）

路径前缀：`/api/v1/user`

| 方法 | 路径 | 鉴权 | 说明 |
|------|------|------|------|
| `POST` | `/api/v1/user/login` | 无 | 登录，返回 JWT Token |
| `POST` | `/api/v1/user/change-password` | JWT | 修改密码 |
| `POST` | `/api/v1/user/change-username` | JWT | 修改用户名 |
| `GET` | `/api/v1/user/status` | JWT | 健康检查 |

### POST /api/v1/user/login

**请求体：**

| 字段 | 类型 | 说明 |
|------|------|------|
| `username` | `string` | 用户名 |
| `password` | `string` | 密码（明文） |
| `expire` | `int` | Token 过期时长（小时） |

**响应 `data`：**

| 字段 | 类型 | 说明 |
|------|------|------|
| `token` | `string` | JWT Token |
| `expire_at` | `string` | 过期时间 |

### POST /api/v1/user/change-password

**请求体：**

| 字段 | 类型 | 说明 |
|------|------|------|
| `old_password` | `string` | 旧密码 |
| `new_password` | `string` | 新密码 |

**响应 `data`：** `"password changed successfully"`

### POST /api/v1/user/change-username

**请求体：**

| 字段 | 类型 | 说明 |
|------|------|------|
| `new_username` | `string` | 新用户名 |

**响应 `data`：** `"username changed successfully"`

### GET /api/v1/user/status

**响应 `data`：** `"ok"`

---

## 渠道模块（Channel）

路径前缀：`/api/v1/channel`，全部需要 JWT 鉴权。

| 方法 | 路径 | 说明 |
|------|------|------|
| `GET` | `/api/v1/channel/list` | 渠道列表 |
| `POST` | `/api/v1/channel/create` | 创建渠道 |
| `POST` | `/api/v1/channel/update` | 增量更新渠道 |
| `POST` | `/api/v1/channel/enable` | 启用/禁用渠道 |
| `DELETE` | `/api/v1/channel/delete/:id` | 删除渠道 |
| `POST` | `/api/v1/channel/fetch-model` | 探测远端可用模型 |
| `POST` | `/api/v1/channel/sync` | 手动触发模型同步 |
| `GET` | `/api/v1/channel/last-sync-time` | 最近同步时间 |

### GET /api/v1/channel/list

**响应 `data`：** `[]Channel`（每项含 `stats` 字段）

### POST /api/v1/channel/create

**请求体：** `Channel` 对象，关键字段见 ENTITY.md Channel 表。

**响应 `data`：** `Channel`（含 `stats`）

### POST /api/v1/channel/update

**请求体：**

| 字段 | 类型 | binding | 说明 |
|------|------|---------|------|
| `id` | `int` | `required` | 渠道 ID |
| `name` | `*string` | — | 名称 |
| `type` | `*string` | — | 协议类型 |
| `enabled` | `*bool` | — | 启用状态 |
| `base_urls` | `*[]BaseUrl` | — | 基础 URL 列表 |
| `model` | `*string` | — | 原始模型名 |
| `custom_model` | `*string` | — | 自定义模型名 |
| `proxy` | `*bool` | — | 全局代理 |
| `auto_sync` | `*bool` | — | 自动同步 |
| `auto_group` | `*int` | — | 自动分组类型 |
| `custom_header` | `*[]CustomHeader` | — | 自定义请求头 |
| `channel_proxy` | `*string` | — | 渠道代理 |
| `param_override` | `*string` | — | 参数覆盖 |
| `match_regex` | `*string` | — | 模型匹配正则 |
| `keys_to_add` | `[]ChannelKeyAddRequest` | — | 新增密钥 |
| `keys_to_update` | `[]ChannelKeyUpdateRequest` | — | 更新密钥 |
| `keys_to_delete` | `[]int` | — | 删除密钥 ID 列表 |

**ChannelKeyAddRequest：**

| 字段 | 类型 | binding | 说明 |
|------|------|---------|------|
| `enabled` | `bool` | — | 启用状态 |
| `channel_key` | `string` | `required` | 密钥字符串 |
| `remark` | `string` | — | 备注 |

**ChannelKeyUpdateRequest：**

| 字段 | 类型 | binding | 说明 |
|------|------|---------|------|
| `id` | `int` | `required` | 密钥 ID |
| `enabled` | `*bool` | — | 启用状态 |
| `channel_key` | `*string` | — | 密钥字符串 |
| `remark` | `*string` | — | 备注 |

**响应 `data`：** `Channel`（含 `stats`）

### POST /api/v1/channel/enable

**请求体：**

| 字段 | 类型 | 说明 |
|------|------|------|
| `id` | `int` | 渠道 ID |
| `enabled` | `bool` | 启用状态 |

**响应 `data`：** `null`

### DELETE /api/v1/channel/delete/:id

路径参数 `id`（int）。**响应 `data`：** `null`

### POST /api/v1/channel/fetch-model

**请求体：** 完整 `Channel` 对象（用于探测远端）。

**响应 `data`：** `[]string`（可用模型名列表）

### POST /api/v1/channel/sync

手动触发模型同步。**响应 `data`：** `null`

### GET /api/v1/channel/last-sync-time

**响应 `data`：** `time.Time`

---

## 分组模块（Group）

路径前缀：`/api/v1/group`，全部需要 JWT 鉴权。

| 方法 | 路径 | 说明 |
|------|------|------|
| `GET` | `/api/v1/group/list` | 分组列表 |
| `POST` | `/api/v1/group/create` | 创建分组 |
| `POST` | `/api/v1/group/update` | 增量更新分组 |
| `DELETE` | `/api/v1/group/delete/:id` | 删除分组 |

### GET /api/v1/group/list

**响应 `data`：** `[]Group`（含 `items`）

### POST /api/v1/group/create

**请求体：** `Group` 对象，关键字段见 ENTITY.md Group 表。

**响应 `data`：** `Group`

### POST /api/v1/group/update

**请求体：**

| 字段 | 类型 | binding | 说明 |
|------|------|---------|------|
| `id` | `int` | `required` | 分组 ID |
| `name` | `*string` | — | 名称 |
| `mode` | `*int` | — | 负载均衡模式 |
| `match_regex` | `*string` | — | 匹配正则 |
| `first_token_time_out` | `*int` | — | 首字超时（秒） |
| `session_keep_time` | `*int` | — | 会话保持时间（秒） |
| `items_to_add` | `[]GroupItemAddRequest` | — | 新增分组项 |
| `items_to_update` | `[]GroupItemUpdateRequest` | — | 更新分组项 |
| `items_to_delete` | `[]int` | — | 删除分组项 ID 列表 |

**GroupItemAddRequest：**

| 字段 | 类型 | binding | 说明 |
|------|------|---------|------|
| `channel_id` | `int` | `required` | 渠道 ID |
| `model_name` | `string` | `required` | 模型名称 |
| `priority` | `int` | — | 优先级 |
| `weight` | `int` | — | 权重 |

**GroupItemUpdateRequest：**

| 字段 | 类型 | binding | 说明 |
|------|------|---------|------|
| `id` | `int` | `required` | 分组项 ID |
| `priority` | `int` | — | 优先级 |
| `weight` | `int` | — | 权重 |

**响应 `data`：** `Group`

### DELETE /api/v1/group/delete/:id

路径参数 `id`（int）。**响应 `data`：** `"group deleted successfully"`

---

## API Key 模块（APIKey）

路径前缀：`/api/v1/apikey`

| 方法 | 路径 | 鉴权 | 说明 |
|------|------|------|------|
| `POST` | `/api/v1/apikey/create` | JWT | 创建 API Key |
| `GET` | `/api/v1/apikey/list` | JWT | 列表 |
| `POST` | `/api/v1/apikey/update` | JWT | 更新 |
| `DELETE` | `/api/v1/apikey/delete/:id` | JWT | 删除 |
| `GET` | `/api/v1/apikey/stats` | API Key | 当前 Key 的统计与信息 |
| `GET` | `/api/v1/apikey/login` | API Key | 登录探测 |

### POST /api/v1/apikey/create

**请求体：** `APIKey` 对象（`api_key` 由后端自动生成并覆盖）。

**响应 `data`：** `APIKey`（含后端生成的 `api_key` 字符串）

### GET /api/v1/apikey/list

**响应 `data`：** `[]APIKey`

### POST /api/v1/apikey/update

**请求体：** 完整 `APIKey` 对象（`id` 必填）。

**响应 `data`：** `APIKey`

### DELETE /api/v1/apikey/delete/:id

路径参数 `id`（int）。**响应 `data`：** `null`

### GET /api/v1/apikey/stats

**响应 `data`：**

```json
{
  "stats": { "input_token": 0, "output_token": 0, "input_cost": 0, ... },
  "info":  { "id": 1, "name": "test", "api_key": "sk-...", ... }
}
```

| 键 | 类型 | 说明 |
|----|------|------|
| `stats` | `StatsAPIKey` | 当前 Key 统计度量 |
| `info` | `APIKey` | 当前 Key 信息 |

### GET /api/v1/apikey/login

仅用于登录探测。**响应 `data`：** `null`

---

## 日志模块（Log）

路径前缀：`/api/v1/log`

| 方法 | 路径 | 鉴权 | 说明 |
|------|------|------|------|
| `GET` | `/api/v1/log/list` | JWT | 分页查询日志 |
| `DELETE` | `/api/v1/log/clear` | JWT | 清空日志 |
| `GET` | `/api/v1/log/stream-token` | JWT | 申请 SSE 临时令牌 |
| `GET` | `/api/v1/log/stream` | Stream Token | SSE 实时日志推送 |

### GET /api/v1/log/list

**Query 参数：**

| 参数 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `page` | int | 1 | 页码（最小 1） |
| `page_size` | int | 20 | 每页数量（1–100） |
| `start_time` | int | — | 起始时间戳（秒），与 `end_time` 同时提供才生效 |
| `end_time` | int | — | 结束时间戳（秒） |

**响应 `data`：** `[]RelayLog`

### DELETE /api/v1/log/clear

清空全部日志。**响应 `data`：** `null`

### GET /api/v1/log/stream-token

申请一次性 SSE Token（使用后立即吊销）。

**响应 `data`：** `{ "token": "<stream_token>" }`

### GET /api/v1/log/stream

**Query 参数：**

| 参数 | 类型 | 说明 |
|------|------|------|
| `token` | string | 由 `/stream-token` 生成的一次性令牌 |

**响应：** `text/event-stream`（SSE），每帧格式：

```
data: {RelayLog JSON}\n\n
```

> 响应头：`Cache-Control: no-cache`、`Connection: keep-alive`、`X-Accel-Buffering: no`

---

## 设置模块（Setting）

路径前缀：`/api/v1/setting`，全部需要 JWT 鉴权。

| 方法 | 路径 | 说明 |
|------|------|------|
| `GET` | `/api/v1/setting/list` | 获取全部设置项 |
| `POST` | `/api/v1/setting/set` | 更新单个设置项 |
| `GET` | `/api/v1/setting/export` | 导出数据库快照（JSON 文件下载） |
| `POST` | `/api/v1/setting/import` | 导入数据库快照 |

### GET /api/v1/setting/list

**响应 `data`：** `[]Setting`

### POST /api/v1/setting/set

**请求体：**

| 字段 | 类型 | 说明 |
|------|------|------|
| `key` | `string` | 设置项键名（见 ENTITY.md Setting 表） |
| `value` | `string` | 设置项值（字符串） |

**响应 `data`：** `Setting`

### GET /api/v1/setting/export

**Query 参数：**

| 参数 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `include_logs` | bool | false | 是否包含转发日志 |
| `include_stats` | bool | false | 是否包含统计数据 |

**响应：** JSON 文件下载（`Content-Disposition: attachment`，文件名含时间戳），内容为 `DBDump` 结构。

### POST /api/v1/setting/import

支持两种 Content-Type：

| Content-Type | Body |
|--------------|------|
| `multipart/form-data` | 表单字段 `file`（JSON 文件） |
| 其它 | 请求体直接为 `DBDump` JSON，或包了一层 `{code, message, data: DBDump}` 的格式 |

**响应 `data`：** `DBImportResult`（`{ "rows_affected": { "channels": 3, ... } }`）

---

## 模型模块（Model / LLM）

路径前缀：`/api/v1/model`（JWT），兼容路由：`/v1/models`（API Key）

| 方法 | 路径 | 鉴权 | 说明 |
|------|------|------|------|
| `GET` | `/api/v1/model/list` | JWT | 模型价格列表 |
| `POST` | `/api/v1/model/create` | JWT | 创建模型价格 |
| `POST` | `/api/v1/model/update` | JWT | 更新模型价格 |
| `POST` | `/api/v1/model/delete` | JWT | 删除模型 |
| `GET` | `/api/v1/model/channel` | JWT | 模型-渠道关联视图 |
| `POST` | `/api/v1/model/update-price` | JWT | 触发价格库更新 |
| `GET` | `/api/v1/model/last-update-time` | JWT | 最近价格更新时间 |
| `GET` | `/v1/models` | API Key | 兼容路由（OpenAI/Anthropic 格式） |

### GET /api/v1/model/list

**响应 `data`：** `[]LLMInfo`

### POST /api/v1/model/create / POST /api/v1/model/update

**请求体：** `LLMInfo` 对象

| 字段 | 类型 | 说明 |
|------|------|------|
| `name` | `string` | 模型名（主键） |
| `input` | `float64` | 输入 Token 单价 |
| `output` | `float64` | 输出 Token 单价 |
| `cache_read` | `float64` | 缓存读单价 |
| `cache_write` | `float64` | 缓存写单价 |

**响应 `data`：** `LLMInfo`

### POST /api/v1/model/delete

**请求体：**

| 字段 | 类型 | binding | 说明 |
|------|------|---------|------|
| `name` | `string` | `required` | 模型名 |

**响应 `data`：** `null`

### GET /api/v1/model/channel

**响应 `data`：** `[]LLMChannel`

| 字段 | 类型 | 说明 |
|------|------|------|
| `name` | `string` | 模型名 |
| `enabled` | `bool` | 渠道是否启用 |
| `channel_id` | `int` | 渠道 ID |
| `channel_name` | `string` | 渠道名 |

### POST /api/v1/model/update-price

触发价格库异步更新。**响应 `data`：** `null`

### GET /api/v1/model/last-update-time

**响应 `data`：** `time.Time`

### GET /v1/models（兼容路由）

**鉴权：** API Key。**请求头：** `x-request-type: openai`（默认）或 `x-request-type: anthropic`

响应**不使用** `ResponseStruct` 包装，按客户端协议输出：

**OpenAI 格式（`x-request-type: openai`）：**

```json
{
  "object": "list",
  "data": [{ "id": "gpt-4o", "object": "model", "created": 1763395200, "owned_by": "octopus" }]
}
```

**Anthropic 格式（`x-request-type: anthropic`）：**

```json
{
  "data": [{ "id": "claude-3-5-sonnet", "created_at": "2024-01-01T00:00:00Z", "display_name": "claude-3-5-sonnet", "type": "model" }],
  "has_more": false,
  "first_id": "claude-3-5-sonnet",
  "last_id": "claude-3-5-sonnet"
}
```

---

## 统计模块（Stats）

路径前缀：`/api/v1/stats`，全部需要 JWT 鉴权，无请求体。

| 方法 | 路径 | 响应 `data` | 说明 |
|------|------|------------|------|
| `GET` | `/api/v1/stats/today` | `StatsMetrics` | 今日累计统计 |
| `GET` | `/api/v1/stats/total` | `StatsMetrics` | 全局累计统计 |
| `GET` | `/api/v1/stats/daily` | `[]StatsDaily` | 按日统计列表 |
| `GET` | `/api/v1/stats/hourly` | `[]StatsHourly` | 按小时统计列表 |
| `GET` | `/api/v1/stats/apikey` | `[]StatsAPIKey` | 按 API Key 维度统计 |

> `StatsMetrics` 字段见 ENTITY.md StatsMetrics 表。

---

## 更新模块（Update）

路径前缀：`/api/v1/update`，全部需要 JWT 鉴权，无请求体。

| 方法 | 路径 | 响应 `data` | 说明 |
|------|------|------------|------|
| `GET` | `/api/v1/update` | `LatestInfo` | 拉取 GitHub 最新版本信息 |
| `GET` | `/api/v1/update/now-version` | `string` | 当前运行版本号 |
| `POST` | `/api/v1/update` | `"update success"` | 触发自动更新（拉取并替换二进制） |

> `LatestInfo` 字段见 VO.md 第四节。

---

## LLM 转发路由（Relay）

路径前缀：`/v1`，**鉴权：API Key**，请求体需为 JSON（图片编辑/变体除外）。

> 这些路由将请求透明转发至上游 LLM 提供商，响应格式由上游决定。

| 方法 | 路径 | 协议格式 | 说明 |
|------|------|---------|------|
| `POST` | `/v1/chat/completions` | OpenAI Chat Completions | 聊天对话（流式/非流式） |
| `POST` | `/v1/responses` | OpenAI Responses | OpenAI Responses API |
| `POST` | `/v1/messages` | Anthropic Messages | Anthropic 对话 |
| `POST` | `/v1/embeddings` | OpenAI Embeddings | 文本向量化 |
| `POST` | `/v1/images/generations` | OpenAI Image Generation | 文生图 |
| `POST` | `/v1/images/edits` | OpenAI Image Edit | 图片编辑（multipart） |
| `POST` | `/v1/images/variations` | OpenAI Image Variation | 图片变体（multipart） |

**转发流程：**

```
客户端请求 → APIKeyAuth → RelayHandler → Balancer（选渠道）→ Transformer（协议转换）→ 上游 LLM
                                                                        ↓
                                                          metrics 采集 + RelayLog 写入
```

- 支持 SSE 流式响应（`stream: true`）
- 失败时按 Group 的负载均衡策略自动重试/故障转移
- 每次尝试结果记录在 `RelayLog.Attempts`（`[]ChannelAttempt`）

---

## 接口汇总

| 模块 | 接口数 | 鉴权方式 |
|------|--------|---------|
| 用户（User） | 4 | 无 / JWT |
| 渠道（Channel） | 8 | JWT |
| 分组（Group） | 4 | JWT |
| API Key | 6 | JWT / API Key |
| 日志（Log） | 4 | JWT / Stream Token |
| 设置（Setting） | 4 | JWT |
| 模型（Model） | 8 | JWT / API Key |
| 统计（Stats） | 5 | JWT |
| 更新（Update） | 3 | JWT |
| LLM 转发（Relay） | 7 | API Key |
| **合计** | **53** | — |
