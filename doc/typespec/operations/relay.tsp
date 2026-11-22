using TypeSpec.Http;
using TypeSpec.OpenAPI;

namespace Octopus;

@route("/v1/chat/completions")
@post
@operationId("relayChatCompletions")
@tag("Relay")
@summary("转发 OpenAI Chat Completions 请求")
@doc("入站及客户端响应协议固定为 OpenAI Chat Completions, 与鉴权头无关. 非空 x-api-key 优先于 Authorization, Key 必须属于 Octopus 且启用, 未过期, 累计成本未超过 max_cost. RequireJSON 只检查 Content-Type 包含小写 application/json, 解析后要求非空 model 和非空 messages. 可选择 openai/chat_completions, openai/responses, anthropic/messages, gemini/contents 或 doubao 出站, 普通转换不承诺全部官方或扩展字段无损. stream:true 使用 SSE, 首事件超时只应用于配置了正 first_token_time_out 的 Group, 已写出响应后不重试或追加 JSON 错误. 完整 model 优先精确匹配 Group, Group 存在但无启用候选时返回 503 且不回退. Group 不存在且 model 包含 / 时按第一个 / 解析 channelName/modelName, 严格区分大小写且选路不 trim, 只使用渠道 Model/CustomModel 当前快照. supported_models 非空时禁止实际直连, 正常 Group 白名单按逗号拆分但不 trim, 与 /v1/models 的 trim 行为不同. 直连仅尝试指定渠道一次, 不使用负载均衡, 故障转移, sticky, Group 首事件超时或 Group 请求选项, 仍检查渠道启用, 可用 Key, 有效 BaseURL 和熔断. 分组请求选项先应用, 渠道后覆盖, JSON 顶层 null 删除参数, 自定义 Header 可覆盖或删除认证头. 协议直通另需 passthrough_enabled, 入站与渠道 Type 完全相同, 无适用请求选项且 endpoint 有效, 仍先解析和选路, 可改写 JSON model, 不等同于指定渠道直连. 非流式直通保留原始成功 body, status 和 Header. 流式直通保留解析后的 data 和 event/id, 由 Octopus 重新编码 SSE 并使用本地响应头与 200 状态, 不保证原始字节或全部上游 Header. endpoint 直接拼 BaseURL, 不做普通版本规范化. 本地错误及分组全部失败返回 ErrorResponse, 分组最终失败为 502. 仅指定渠道直连的可识别上游 HTTP 错误保留状态并转换为 OpenAIProtocolError, 无上游 HTTP 状态的真实尝试失败为 502, 不可用或跳过为 503. 流已提交后的错误及客户端取消不保证另有错误 JSON. Group 路由按 mode 排序候选并在配置允许时让可用 sticky 渠道优先, 尚未写出响应时可顺次尝试或故障转移. 尝试结果记录于 RelayLog.Attempts 并统计, 日志持久化受设置影响. Authorization 通常使用 Bearer <key>, 实现也接受直接 key, 仅剥离精确的 Bearer 加一个空格, 不额外 trim 或读取 Cookie/query 凭证. 来源: internal/server/server.go:59, internal/relay/relay.go:42-177,287-603, internal/relay/direct_channel.go:26-117, internal/relay/passthrough.go:37-176.")
@useAuth(APIKeyHeader | APIKeyBearer)
@returnsDoc("普通转换的非流式 JSON 或 stream:true 的 SSE, 无管理包装. 非流式直通可能保留其他 JSON, Content-Type 或空 body.")
op relayChatCompletions(
  @header contentType: "application/json",
  @body body: OpenAIChatRequest,
):
  | RelayStreamingSuccess<
      RelayChatCompletionsStatus200application_jsonValue,
      RelayChatCompletionsStatus200text_event_streamValue,
      "普通转换的非流式 JSON 或 stream:true 的 SSE, 无管理包装. 非流式直通可能保留其他 JSON, Content-Type 或空 body."
    >
  | HttpResponse<
      400,
      RelayChatCompletionsStatus400application_jsonValue,
      "本地入站校验或模型白名单失败, 或指定渠道上游 HTTP 400"
    >
  | RelayUnauthorized<RelayChatCompletionsStatus400application_jsonValue>
  | RelayNotFound<RelayChatCompletionsStatus400application_jsonValue>
  | RelayUnsupportedMediaType<RelayChatCompletionsStatus400application_jsonValue>
  | HttpResponse<
      500,
      RelayChatCompletionsStatus400application_jsonValue,
      "本地请求体读取, 解压, 非校验转换或 recovery 失败, 或指定渠道上游 HTTP 500"
    >
  | RelayBadGateway<RelayChatCompletionsStatus400application_jsonValue>
  | RelayUnavailable<RelayChatCompletionsStatus400application_jsonValue>
  | RelayOtherResponses<
      RelayChatCompletionsStatusdefaultapplication_jsonValue,
      RelayChatCompletionsStatusdefault___Value,
      "其他上游状态. 不一概表示错误: 协议直通可保留非 200 成功状态, body 和 Content-Type. 指定渠道可返回其他上游 HTTP 错误状态及转换后的协议错误体. 普通转换通常生成 200. SSE 提交后的流错误不按此另发响应."
    >;

@route("/v1/responses")
@post
@operationId("relayResponses")
@tag("Relay")
@summary("转发 OpenAI Responses 请求")
@doc("入站及客户端响应协议固定为 OpenAI Responses, 与鉴权头无关. 非空 x-api-key 优先于 Authorization, Key 必须属于 Octopus 且启用, 未过期, 累计成本未超过 max_cost. RequireJSON 只检查 Content-Type 包含小写 application/json. 解析要求非空 model, input 可省略, 或使用字符串及开放 Item 数组, 不保证最终出站能处理空对话. 可选择 openai/chat_completions, openai/responses, anthropic/messages, gemini/contents 或 doubao 出站. 未知顶层参数在转换路径不保证保留, 部分 Responses 专有 tools, input 和 tool_choice 可经 provider extensions 保留至同协议出站. background 在普通转换路径未映射, previous_response_id 不代表 Octopus 存储会话, 未注册查询, 取消或 compact 路由. stream:true 返回 Responses SSE, Group 正 first_token_time_out 才设置首事件超时, 响应写出后不重试. 完整 model 优先精确匹配 Group, 已存在但无启用候选的 Group 返回 503 且不回退. Group 不存在且 model 含 / 时按第一个 / 拆为 channelName/modelName, 大小写严格且选路不 trim, 只读取渠道当前 Model/CustomModel. supported_models 非空禁止实际直连, Group 白名单按逗号拆分不 trim, 与 /v1/models 的 trim 行为不同. 直连只有一次指定渠道尝试, 无负载均衡, 故障转移, sticky, Group 超时或 Group 请求选项, 仍检查启用, Key, BaseURL 和熔断. Group 请求选项先应用, 渠道后覆盖, JSON 顶层 null 删除参数, Header 可覆盖或删除认证头. 协议直通需开关, 入站与渠道 Type 相同, 无适用请求选项且 endpoint 有效, 仍先解析和选路, 可改 JSON model, 不等同直连. 非流式直通保留 body, status 和 Header. 流式直通保留解析后的 data 和 event/id, 由 Octopus 重新编码 SSE 并使用本地响应头与 200 状态, 不保留原始字节或全部上游 Header. endpoint 直接拼接 BaseURL, 不做普通版本规范化. 本地错误及分组最终失败用 ErrorResponse, 分组最终失败为 502. 只有直连可识别的上游 HTTP 错误保留状态并转为 ResponsesProtocolError, 无 HTTP 状态的真实尝试失败为 502, 跳过或不可用为 503. 已提交 SSE 后的错误和客户端取消不保证另有 JSON. Group 路由按 mode 排序候选并在配置允许时让可用 sticky 渠道优先, 尚未写出响应时可顺次尝试或故障转移. 尝试结果记录于 RelayLog.Attempts 并统计, 日志持久化受设置影响. Authorization 通常使用 Bearer <key>, 实现也接受直接 key, 仅剥离精确的 Bearer 加一个空格, 不额外 trim 或读取 Cookie/query 凭证. 来源: internal/server/server.go:60, internal/relay/relay.go:42-177,287-603, internal/relay/direct_channel.go:26-117, axonhub pin d061ac7df6ae transformer/openai/responses/inbound.go, request_extensions.go.")
@useAuth(APIKeyHeader | APIKeyBearer)
@returnsDoc("普通转换的非流式 Responses JSON 或 SSE, 无管理包装. 非流式直通可能保留其他 JSON, Content-Type 或空 body.")
op relayResponses(
  @header contentType: "application/json",
  @body body: ResponsesRequest,
):
  | RelayStreamingSuccess<
      RelayResponsesStatus200application_jsonValue,
      RelayResponsesStatus200text_event_streamValue,
      "普通转换的非流式 Responses JSON 或 SSE, 无管理包装. 非流式直通可能保留其他 JSON, Content-Type 或空 body."
    >
  | HttpResponse<
      400,
      RelayResponsesStatus400application_jsonValue,
      "本地解析, 入站校验或白名单失败, 或指定渠道上游 HTTP 400"
    >
  | RelayUnauthorized<RelayResponsesStatus400application_jsonValue>
  | RelayNotFound<RelayResponsesStatus400application_jsonValue>
  | RelayUnsupportedMediaType<RelayResponsesStatus400application_jsonValue>
  | RelayInternalError<RelayResponsesStatus400application_jsonValue>
  | RelayBadGateway<RelayResponsesStatus400application_jsonValue>
  | RelayUnavailable<RelayResponsesStatus400application_jsonValue>
  | RelayOtherResponses<
      RelayResponsesStatusdefaultapplication_jsonValue,
      RelayResponsesStatusdefault___Value,
      "其他上游状态, 包括协议直通保留的非 200 成功状态及指定渠道转换后的其他 HTTP 错误. 不一概表示错误. body 和 Content-Type 可能来自直通上游."
    >;

@route("/v1/messages")
@post
@operationId("relayAnthropicMessages")
@tag("Relay")
@summary("转发 Anthropic Messages 请求")
@doc("入站及客户端响应协议固定为 Anthropic Messages, 与鉴权头无关. 非空 x-api-key 优先于 Authorization, Key 必须属于 Octopus 且启用, 未过期, 累计成本未超过 max_cost. RequireJSON 只检查 Content-Type 包含小写 application/json. model, messages 和正 max_tokens 在入站执行校验, thinking, system 及 tool_choice 另有明确规则. 可选择 openai/chat_completions, openai/responses, anthropic/messages, gemini/contents 或 doubao 出站, 不保证跨协议所有多模态和原生工具字段无损. Anthropic-Version 入站未强制必填, Anthropic 出站缺省为 2023-06-01, 可受允许合并的入站 Header 或请求选项影响. stream:true 返回 Messages SSE, Group 正 first_token_time_out 才设置首事件超时, 写出后不重试. 完整 model 优先精确命中 Group, 存在但无启用候选时返回 503 且不回退. Group 不存在且 model 含 / 时按第一个 / 解析 channelName/modelName, 选路大小写严格且不 trim, 模型只取渠道 Model/CustomModel 当前快照. supported_models 非空禁止实际直连, Group 白名单按逗号拆分但不 trim, 不同于 /v1/models 的 trim. 直连只尝试指定渠道一次, 不用负载均衡, 故障转移, sticky, Group 超时或 Group 请求选项, 仍检查渠道启用, Key, BaseURL 和熔断. Group 请求选项先应用, 渠道后覆盖, JSON 顶层 null 删除参数, Header 可覆盖或删除认证头. 协议直通需开关, 相同入站和渠道 Type, 无适用请求选项且 endpoint 有效, 仍先解析和选路, 可改 JSON model, 不等同指定渠道直连. 非流式直通保留成功 body, status 和 Header. 流式直通保留解析后的 data 和 event/id, 由 Octopus 重新编码 SSE 并使用本地响应头与 200 状态, 不保留原始字节或全部上游 Header. endpoint 直接拼 BaseURL, 不做普通版本规范化. 本地错误及分组最终失败为 ErrorResponse, 分组最终失败为 502. 仅直连可识别上游 HTTP 错误保留状态并转换为 AnthropicProtocolError, 顶层 type 来自统一错误类型而不固定为 error, 不是完整上游错误头或 body 透传. 无 HTTP 状态的真实尝试失败为 502, 不可用或跳过为 503. 流提交后的错误和客户端取消不保证另有 JSON. Group 路由按 mode 排序候选并在配置允许时让可用 sticky 渠道优先, 尚未写出响应时可顺次尝试或故障转移. 尝试结果记录于 RelayLog.Attempts 并统计, 日志持久化受设置影响. Authorization 通常使用 Bearer <key>, 实现也接受直接 key, 仅剥离精确的 Bearer 加一个空格, 不额外 trim 或读取 Cookie/query 凭证. 来源: internal/server/server.go:61, internal/relay/relay.go:42-177,287-603, internal/relay/direct_channel.go:90-117, axonhub pin d061ac7df6ae transformer/anthropic/inbound.go.")
@useAuth(APIKeyHeader | APIKeyBearer)
@returnsDoc("普通转换的非流式 Messages JSON 或 SSE, 无管理包装. 非流式直通可能保留其他 JSON, Content-Type 或空 body.")
op relayAnthropicMessages(
  @doc("可选入站 Header, 本项目没有必填校验. 不选择客户端响应协议. Anthropic 出站通常设置 2023-06-01, 自定义 Header 仍可覆盖或删除.")
  @header("Anthropic-Version")
  @extension("example", "2023-06-01")
  Anthropic_Version?: string,

  @doc("可选 beta Header. Anthropic 出站允许合并此 Header, 实际功能由上游及转换能力决定.")
  @header("Anthropic-Beta")
  Anthropic_Beta?: string,

  @header contentType: "application/json",
  @body body: AnthropicMessagesRequest,
):
  | RelayStreamingSuccess<
      RelayAnthropicMessagesStatus200application_jsonValue,
      RelayAnthropicMessagesStatus200text_event_streamValue,
      "普通转换的非流式 Messages JSON 或 SSE, 无管理包装. 非流式直通可能保留其他 JSON, Content-Type 或空 body."
    >
  | HttpResponse<
      400,
      RelayAnthropicMessagesStatus400application_jsonValue,
      "本地解析, 必填, thinking, system, tool_choice 或白名单校验失败, 或指定渠道上游 HTTP 400"
    >
  | RelayUnauthorized<RelayAnthropicMessagesStatus400application_jsonValue>
  | RelayNotFound<RelayAnthropicMessagesStatus400application_jsonValue>
  | RelayUnsupportedMediaType<RelayAnthropicMessagesStatus400application_jsonValue>
  | RelayInternalError<RelayAnthropicMessagesStatus400application_jsonValue>
  | RelayBadGateway<RelayAnthropicMessagesStatus400application_jsonValue>
  | RelayUnavailable<RelayAnthropicMessagesStatus400application_jsonValue>
  | RelayOtherResponses<
      RelayAnthropicMessagesStatusdefaultapplication_jsonValue,
      RelayResponsesStatusdefault___Value,
      "其他上游状态. 包括协议直通保留的非 200 成功状态及指定渠道转换后的其他 HTTP 错误, 不一概表示错误. 直通可保留原始成功 Content-Type 和 body."
    >;

@route("/v1/embeddings")
@post
@operationId("relayEmbeddings")
@tag("Relay")
@summary("转发 OpenAI Embeddings 请求")
@doc("入站及客户端响应固定为 OpenAI Embeddings, 与鉴权头无关. 非空 x-api-key 优先于 Authorization, Key 必须属于 Octopus 且启用, 未过期, 累计成本未超过 max_cost. RequireJSON 只检查 Content-Type 包含小写 application/json. model 必须非空, input 支持 string, string[], token 整数数组或二维 token 数组, 并执行非空校验. 不提供 stream 或正式 SSE 响应路径, 转换路径不识别 stream 字段. 可选择 openai/chat_completions, openai/responses, openai/embeddings, gemini/contents 或 doubao. OpenAI 类渠道实际走 /embeddings, openai/responses 渠道此时也不是 Responses API. Gemini 走 embedContent 或 batchEmbedContents 且仅转换文本, token 数组失败. Doubao 走 /embeddings/multimodal, 当前入站只提供上述文本或 token 类型, 出站只构造文本项, 不承诺 token 输入或多模态兼容. 完整 model 优先精确命中 Group, 存在但无启用候选时 503 且不回退. 不存在且含 / 时按第一个 / 拆 channelName/modelName, 大小写严格且选路不 trim, 只检查渠道 Model/CustomModel 当前快照. supported_models 非空禁止实际直连, Group 白名单按逗号拆分不 trim, 与 /v1/models 的 trim 不同. 直连一次指定渠道尝试, 不用负载均衡, 故障转移, sticky, Group 超时或 Group 请求选项, 仍检查启用, Key, BaseURL 和熔断. Group 请求选项先应用, 渠道后覆盖, 仅 JSON 顶层合并参数, null 删除, Header 可覆盖或删除认证. 协议直通需开关, 相同入站和渠道 Type, 无适用请求选项且 endpoint 有效, 仍先解析和选路, 可改 JSON model, 不等同直连. 直通保留原始成功 body, status 和 Header, endpoint 直接拼 BaseURL, 未做普通版本规范化. 即使原始 body 带 stream 扩展, 也不因此形成正式 SSE 支持. 本地错误及分组最终失败为 ErrorResponse, 分组最终失败为 502. 仅直连可识别的上游 HTTP 错误保留状态并转为 OpenAIProtocolError, 无 HTTP 状态的真实尝试失败为 502, 不可用或跳过为 503. Group 路由按 mode 排序候选并在配置允许时让可用 sticky 渠道优先, 尚未写出响应时可顺次尝试或故障转移. 尝试结果记录于 RelayLog.Attempts 并统计, 日志持久化受设置影响. Authorization 通常使用 Bearer <key>, 实现也接受直接 key, 仅剥离精确的 Bearer 加一个空格, 不额外 trim 或读取 Cookie/query 凭证. 来源: internal/server/server.go:62, internal/relay/relay.go:42-177, internal/relay/transformers.go:49-60, axonhub pin d061ac7df6ae transformer/openai/embedding_inbound.go, transformer/gemini/embedding.go, transformer/doubao/embedding.go.")
@useAuth(APIKeyHeader | APIKeyBearer)
op relayEmbeddings(
  @header contentType: "application/json",
  @body body: OpenAIEmbeddingRequest,
):
  | RelaySuccess<
      RelayEmbeddingsStatus200application_jsonValue,
      "普通转换的非流式 Embeddings JSON, 无管理包装. 非流式直通可能保留其他 JSON, Content-Type 或空 body."
    >
  | HttpResponse<
      400,
      RelayChatCompletionsStatus400application_jsonValue,
      "本地解析, model, input 或白名单校验失败, 或指定渠道上游 HTTP 400"
    >
  | RelayUnauthorized<RelayChatCompletionsStatus400application_jsonValue>
  | RelayNotFound<RelayChatCompletionsStatus400application_jsonValue>
  | RelayUnsupportedMediaType<RelayChatCompletionsStatus400application_jsonValue>
  | RelayInternalError<RelayChatCompletionsStatus400application_jsonValue>
  | RelayBadGateway<RelayChatCompletionsStatus400application_jsonValue>
  | RelayEmbeddingsUnavailable<RelayChatCompletionsStatus400application_jsonValue>
  | RelayOtherResponses<
      RelayEmbeddingsStatusdefaultapplication_jsonValue,
      RelayEmbeddingsStatusdefault___Value,
      "其他上游状态, 包括协议直通保留的非 200 成功状态和指定渠道其他 HTTP 错误, 不一概表示错误. 不据此声明 embedding 支持 SSE."
    >;

@route("/v1/images/generations")
@post
@operationId("relayImageGenerations")
@tag("Relay")
@summary("转发图片生成请求")
@doc("入站及客户端响应固定为 OpenAI Images 格式, 与鉴权头无关. 非空 x-api-key 优先于 Authorization, Key 必须属于 Octopus 且启用, 未过期, 累计成本未超过 max_cost. RequireJSON 检查 Content-Type 包含小写 application/json. prompt 非空但不 trim, model 缺失, null 或空字符串时默认 dall-e-2, stream:true 在选路前返回 400, 不支持 SSE. 可选择 OpenAI chat/responses/image 类渠道, gemini/contents 或 doubao, Anthropic 和 embedding 类渠道不兼容. openai/responses 渠道在本入口实际使用 OpenAI Images outbound, Gemini 用 generateContent, Doubao 用 images/generations. 参数映射及 response_format 的保留取决于出站和实际模型, 不保证所有字段无损. 完整 model 优先精确匹配 Group, 已存在但无启用候选时 503 且不回退. 不存在且含 / 时按第一个 / 拆 channelName/modelName, 大小写严格且选路不 trim, 仅匹配渠道 Model/CustomModel 当前快照. supported_models 非空禁止实际直连, Group 白名单逗号拆分不 trim, 与 /v1/models 的 trim 不同. 直连仅一次指定渠道尝试, 不使用负载均衡, 故障转移, sticky, Group 超时或 Group 请求选项, 仍检查启用, Key, BaseURL 和熔断. Group 请求选项先应用, 渠道后覆盖, JSON 顶层 null 删除参数, Header 可覆盖或删除认证头. 协议直通需开关, 相同入站和渠道 Type, 无适用请求选项且 endpoint 有效, 仍先解析和选路, 可改 JSON model, 不等同直连. 直通保留成功 body, status 和 Header, 直接拼 BaseURL 与 endpoint, 不做普通版本规范化. 本地错误及分组最终失败为 ErrorResponse, 分组最终失败为 502. 仅直连可识别上游 HTTP 错误保留状态并转为 OpenAIProtocolError, 无 HTTP 状态的真实尝试失败为 502, 不可用或跳过为 503. Group 路由按 mode 排序候选并在配置允许时让可用 sticky 渠道优先, 尚未写出响应时可顺次尝试或故障转移. 尝试结果记录于 RelayLog.Attempts 并统计, 日志持久化受设置影响. Authorization 通常使用 Bearer <key>, 实现也接受直接 key, 仅剥离精确的 Bearer 加一个空格, 不额外 trim 或读取 Cookie/query 凭证. 来源: internal/server/server.go:63, internal/relay/relay.go:42-177, internal/relay/transformers.go:62-76, axonhub pin d061ac7df6ae transformer/openai/image_inbound.go:199-250.")
@useAuth(APIKeyHeader | APIKeyBearer)
op relayImageGenerations(
  @header contentType: "application/json",
  @body body: ImageGenerationRequest,
):
  | RelaySuccess<
      RelayImageGenerationsStatus200application_jsonValue,
      "普通转换的非流式 Images JSON, 无管理包装. 非流式直通可能保留其他 JSON, Content-Type 或空 body."
    >
  | HttpResponse<
      400,
      RelayChatCompletionsStatus400application_jsonValue,
      "本地解析, prompt, stream 或白名单校验失败, 或指定渠道上游 HTTP 400"
    >
  | RelayUnauthorized<RelayChatCompletionsStatus400application_jsonValue>
  | RelayNotFound<RelayChatCompletionsStatus400application_jsonValue>
  | RelayUnsupportedMediaType<RelayChatCompletionsStatus400application_jsonValue>
  | RelayInternalError<RelayChatCompletionsStatus400application_jsonValue>
  | RelayBadGateway<RelayChatCompletionsStatus400application_jsonValue>
  | RelayEmbeddingsUnavailable<RelayChatCompletionsStatus400application_jsonValue>
  | RelayOtherResponses<
      RelayImageGenerationsStatusdefaultapplication_jsonValue,
      RelayEmbeddingsStatusdefault___Value,
      "其他上游状态, 包括协议直通保留的非 200 成功及指定渠道其他 HTTP 错误, 不一概表示错误. Images 不提供 SSE 支持."
    >;

@route("/v1/images/edits")
@post
@operationId("relayImageEdits")
@tag("Relay")
@summary("转发 multipart 图片编辑请求")
@doc("入站及响应固定为 OpenAI Images 格式. 非空 x-api-key 优先于 Authorization, Key 必须属于 Octopus 且启用, 未过期, 累计成本未超过 max_cost. 不挂 RequireJSON. 使用带 boundary 的 multipart, 解析器接受 multipart/ 前缀, JSON body 不受支持且通常返回 400, 不是入口 415. 至少提供 image 或 image[] 文件 part, filename 必须非空, prompt trim 后非空. model 先 trim, 缺失或空时默认 dall-e-2. 表单 stream trim 后大小写不敏感等于 true 则 400, 不支持 SSE. 文件 MIME 支持 PNG, JPEG, GIF, WebP, 单文件及普通字段默认 50 MiB, AXONHUB_MAX_IMAGE_FILE_SIZE 可调整. 总 body 固定解析限额 816 MiB, 最多 16 个文件 part, 包括 mask 和未知文件字段. 限额在完整 body 读入后校验, 不是入口读流限额, 超限通常 400 而非 413. 可选择 OpenAI chat/responses/image 类渠道, Gemini 或 Doubao, openai/responses 此时仍用 Images outbound. Gemini 和 Doubao 不完整保留 mask 及其他 OpenAI 参数, 不保证无损. 完整 model 优先精确匹配 Group, 存在但无启用候选时 503 且不回退. 不存在且含 / 时按第一个 / 拆 channelName/modelName, 选路大小写严格, multipart 入站已 trim model, 只匹配渠道 Model/CustomModel 当前快照. supported_models 非空禁止实际直连, Group 白名单按逗号拆分不 trim, 与 /v1/models 的 trim 不同. 直连仅一次指定渠道尝试, 不用负载均衡, 故障转移, sticky, Group 超时或 Group 请求选项, 仍检查启用, Key, BaseURL 和熔断. Group 请求选项先应用, 渠道后覆盖, 参数覆盖仅作用于最终 JSON body, 不能改写 OpenAI multipart body, Header 可覆盖或删除认证头. 协议直通需开关, 相同入站和渠道 Type, 无适用请求选项, endpoint 有效且 multipart 不需要模型改写, 仍先解析和选路, 不等同直连. 直连去前缀或 Group 模型改名通常使 multipart 退回转换. 直通保留成功 body, status 和 Header, endpoint 直接拼 BaseURL 而不做普通版本规范化. 本地错误及分组最终失败为 ErrorResponse, 分组最终失败 502. 仅直连可识别上游 HTTP 错误保留状态并转 OpenAIProtocolError, 无 HTTP 状态的真实尝试失败 502, 不可用或跳过 503. Group 路由按 mode 排序候选并在配置允许时让可用 sticky 渠道优先, 尚未写出响应时可顺次尝试或故障转移. 尝试结果记录于 RelayLog.Attempts 并统计, 日志持久化受设置影响. Authorization 通常使用 Bearer <key>, 实现也接受直接 key, 仅剥离精确的 Bearer 加一个空格, 不额外 trim 或读取 Cookie/query 凭证. 来源: internal/server/server.go:64, internal/relay/passthrough.go:37-54, axonhub pin d061ac7df6ae transformer/openai/image_inbound.go:252-321,389-483.")
@useAuth(APIKeyHeader | APIKeyBearer)
@extension(
  "requestBody",
  #{
    required: true,
    description: "multipart 文件和文本字段. 标准形式为 multipart/form-data; boundary=..., 解析器也接受其他 multipart/ MIME, 必须有有效 boundary.",
    content: #{
      `multipart/form-data`: #{
        schema: #{ $ref: "#/components/schemas/ImageEditRequest" },
        encoding: #{
          image: #{
            contentType: "image/png, image/jpeg, image/gif, image/webp",
          },
          `image[]`: #{
            contentType: "image/png, image/jpeg, image/gif, image/webp",
          },
          mask: #{
            contentType: "image/png, image/jpeg, image/gif, image/webp",
          },
        },
      },
    },
  }
)
op relayImageEdits(
  @header contentType: "multipart/form-data",

  @doc("multipart 文件和文本字段. 标准形式为 multipart/form-data; boundary=..., 解析器也接受其他 multipart/ MIME, 必须有有效 boundary.")
  @multipartBody
  body: ImageEditRequestParts,
):
  | RelaySuccess<
      RelayImageGenerationsStatus200application_jsonValue,
      "普通转换的非流式 Images JSON, 无管理包装. 非流式直通可能保留其他 JSON, Content-Type 或空 body."
    >
  | HttpResponse<
      400,
      RelayChatCompletionsStatus400application_jsonValue,
      "multipart, 文件, prompt, stream 或白名单校验失败, 或指定渠道上游 HTTP 400"
    >
  | RelayUnauthorized<RelayChatCompletionsStatus400application_jsonValue>
  | RelayNotFound<RelayChatCompletionsStatus400application_jsonValue>
  | RelayInternalError<RelayChatCompletionsStatus400application_jsonValue>
  | RelayBadGateway<RelayChatCompletionsStatus400application_jsonValue>
  | RelayEmbeddingsUnavailable<RelayChatCompletionsStatus400application_jsonValue>
  | RelayOtherResponses<
      RelayImageGenerationsStatusdefaultapplication_jsonValue,
      RelayEmbeddingsStatusdefault___Value,
      "其他上游状态, 包括协议直通非 200 成功及直连其他 HTTP 错误(例如上游 415), 不一概表示错误. 本入口没有 RequireJSON 的本地 415, 不支持 SSE."
    >;

@route("/v1/images/variations")
@post
@operationId("relayImageVariations")
@tag("Relay")
@summary("转发 multipart 图片变体请求")
@doc("入站及响应固定为 OpenAI Images 格式. 非空 x-api-key 优先于 Authorization, Key 必须属于 Octopus 且启用, 未过期, 累计成本未超过 max_cost. 不挂 RequireJSON. 使用有效 boundary 的 multipart, 解析器接受 multipart/ 前缀. JSON body 不受支持且通常返回 400, 不是入口 415. image 或 image[] 合计必须恰好一个带非空 filename 的图片文件. prompt trim 后非空则 400, mask 即使被解析也不用于变体请求. model 先 trim, 缺失或空默认 dall-e-2. 表单 stream trim 后大小写不敏感等于 true 则 400, 不支持 SSE. 文件 MIME 支持 PNG, JPEG, GIF, WebP. 单文件及普通字段默认 50 MiB, AXONHUB_MAX_IMAGE_FILE_SIZE 可调整, 总 body 固定解析限额 816 MiB, 文件 part 总计最多 16(含 mask 或未知字段), 限额在完整读入后校验, 通常 400 而非 413. OpenAI chat/responses/image 类渠道走 OpenAI variations, openai/responses 此时不是 Responses API. 工厂虽允许 Gemini 和 Doubao, 但两者 image outbound 必须有 prompt, 与此入站禁止 prompt 冲突, 标准转换路径不能完成 variation, 会以真实尝试失败处理. 完整 model 优先精确匹配 Group, 存在但无启用候选时 503 且不回退. 不存在且含 / 时按第一个 / 拆 channelName/modelName, 大小写严格, multipart 入站已 trim model, 只匹配渠道 Model/CustomModel 当前快照. supported_models 非空禁止实际直连, Group 白名单逗号拆分不 trim, 与 /v1/models 的 trim 不同. 直连仅一次指定渠道尝试, 无负载均衡, 故障转移, sticky, Group 超时或 Group 请求选项, 仍检查启用, Key, BaseURL 和熔断. Group 请求选项先应用, 渠道后覆盖, 参数覆盖只作用于最终 JSON body, 不能改写 OpenAI multipart body, Header 可覆盖或删除认证. 协议直通需开关, 相同入站和渠道 Type, 无适用请求选项, endpoint 有效且 multipart 不需要模型改写, 仍先解析和选路, 不等同直连. 直连去前缀或 Group 模型改名通常退回转换. 直通保留成功 body, status 和 Header, 直接拼 BaseURL 与 endpoint, 不做普通版本规范化. 本地错误及分组最终失败为 ErrorResponse, 分组最终失败 502. 仅直连可识别的上游 HTTP 错误保留状态并转 OpenAIProtocolError, 无 HTTP 状态的真实尝试失败 502, 不可用或跳过 503. Group 路由按 mode 排序候选并在配置允许时让可用 sticky 渠道优先, 尚未写出响应时可顺次尝试或故障转移. 尝试结果记录于 RelayLog.Attempts 并统计, 日志持久化受设置影响. Authorization 通常使用 Bearer <key>, 实现也接受直接 key, 仅剥离精确的 Bearer 加一个空格, 不额外 trim 或读取 Cookie/query 凭证. 来源: internal/server/server.go:65, axonhub pin d061ac7df6ae transformer/openai/image_inbound.go:323-371, transformer/gemini/image.go:19-29, transformer/doubao/outbound.go:190-198.")
@useAuth(APIKeyHeader | APIKeyBearer)
@extension(
  "requestBody",
  #{
    required: true,
    description: "multipart 图片文件及文本参数. 只允许一个 image, 标准 MIME 为 multipart/form-data, 其他 multipart/ MIME 也需有效 boundary.",
    content: #{
      `multipart/form-data`: #{
        schema: #{ $ref: "#/components/schemas/ImageVariationRequest" },
        encoding: #{
          image: #{
            contentType: "image/png, image/jpeg, image/gif, image/webp",
          },
          `image[]`: #{
            contentType: "image/png, image/jpeg, image/gif, image/webp",
          },
        },
      },
    },
  }
)
op relayImageVariations(
  @header contentType: "multipart/form-data",

  @doc("multipart 图片文件及文本参数. 只允许一个 image, 标准 MIME 为 multipart/form-data, 其他 multipart/ MIME 也需有效 boundary.")
  @multipartBody
  body: ImageVariationRequestParts,
):
  | RelaySuccess<
      RelayImageGenerationsStatus200application_jsonValue,
      "普通转换的非流式 Images JSON, 无管理包装. 非流式直通可能保留其他 JSON, Content-Type 或空 body."
    >
  | HttpResponse<
      400,
      RelayChatCompletionsStatus400application_jsonValue,
      "multipart, 图片数量, prompt, stream 或白名单校验失败, 或指定渠道上游 HTTP 400"
    >
  | RelayUnauthorized<RelayChatCompletionsStatus400application_jsonValue>
  | RelayNotFound<RelayChatCompletionsStatus400application_jsonValue>
  | RelayInternalError<RelayChatCompletionsStatus400application_jsonValue>
  | HttpResponse<
      502,
      RelayChatCompletionsStatus400application_jsonValue,
      "分组最终失败, 直连网络或转换等真实尝试失败, 包括 Gemini/Doubao variation 的 prompt 冲突, 或指定渠道上游 HTTP 502"
    >
  | RelayEmbeddingsUnavailable<RelayChatCompletionsStatus400application_jsonValue>
  | RelayOtherResponses<
      RelayImageGenerationsStatusdefaultapplication_jsonValue,
      RelayEmbeddingsStatusdefault___Value,
      "其他上游状态, 包括协议直通非 200 成功及直连其他 HTTP 错误(例如上游 415), 不一概表示错误. 本入口没有 RequireJSON 的本地 415, 不支持 SSE."
    >;

union RelayChatCompletionsStatus200application_jsonValue {
  OpenAIChatResponse,
  PassthroughJSON,
}

@doc("SSE 文本. data 通常是 Chat Completions chunk JSON, 可含 [DONE]. 不把整段流声明为 JSON 数组.")
scalar RelayChatCompletionsStatus200text_event_streamValue extends string;

union RelayChatCompletionsStatus400application_jsonValue {
  ErrorResponse,
  OpenAIProtocolError,
}

union RelayChatCompletionsStatusdefaultapplication_jsonValue {
  OpenAIChatResponse,
  PassthroughJSON,
  ErrorResponse,
  OpenAIProtocolError,
}

@doc("协议直通可能保留未建模的成功 body, 包括空 body 或非 JSON 内容.")
union RelayChatCompletionsStatusdefault___Value {
  unknown,
}

union RelayResponsesStatus200application_jsonValue {
  ResponsesResponse,
  PassthroughJSON,
}

@doc("SSE 文本. 包含 response.created, response.output_text.delta, response.completed 等类型事件, 具体事件取决于转换和上游能力.")
scalar RelayResponsesStatus200text_event_streamValue extends string;

union RelayResponsesStatus400application_jsonValue {
  ErrorResponse,
  ResponsesProtocolError,
}

union RelayResponsesStatusdefaultapplication_jsonValue {
  ResponsesResponse,
  PassthroughJSON,
  ErrorResponse,
  ResponsesProtocolError,
}

@doc("直通成功可保留空 body 或其他未建模内容.")
union RelayResponsesStatusdefault___Value {
  unknown,
}

union RelayAnthropicMessagesStatus200application_jsonValue {
  AnthropicMessageResponse,
  PassthroughJSON,
}

@doc("SSE 文本. 包含 message_start, content_block_start, content_block_delta, content_block_stop, message_delta 和 message_stop 等类型事件.")
scalar RelayAnthropicMessagesStatus200text_event_streamValue extends string;

union RelayAnthropicMessagesStatus400application_jsonValue {
  ErrorResponse,
  AnthropicProtocolError,
}

union RelayAnthropicMessagesStatusdefaultapplication_jsonValue {
  AnthropicMessageResponse,
  PassthroughJSON,
  ErrorResponse,
  AnthropicProtocolError,
}

union RelayEmbeddingsStatus200application_jsonValue {
  OpenAIEmbeddingResponse,
  PassthroughJSON,
}

union RelayEmbeddingsStatusdefaultapplication_jsonValue {
  OpenAIEmbeddingResponse,
  PassthroughJSON,
  ErrorResponse,
  OpenAIProtocolError,
}

@doc("直通成功可以保留原始或空 body 及其他 Content-Type.")
union RelayEmbeddingsStatusdefault___Value {
  unknown,
}

union RelayImageGenerationsStatus200application_jsonValue {
  ImageResponse,
  PassthroughJSON,
}

union RelayImageGenerationsStatusdefaultapplication_jsonValue {
  ImageResponse,
  PassthroughJSON,
  ErrorResponse,
  OpenAIProtocolError,
}

// HttpPart 复用原字段类型, 文件数组的原始数量约束和 encoding 由操作元数据保留.
model ImageEditRequestParts {
  image?: HttpPart<ImageFile>[];
  `image[]`?: HttpPart<ImageFile>[];
  mask?: HttpPart<ImageEditRequest.mask>;
  prompt: HttpPart<ImageEditRequest.prompt>;
  `model`?: HttpPart<ImageEditRequest.`model`>;
  n?: HttpPart<ImageEditRequest.n>;
  size?: HttpPart<ImageEditRequest.size>;
  quality?: HttpPart<ImageEditRequest.quality>;
  response_format?: HttpPart<ImageEditRequest.response_format>;
  user?: HttpPart<ImageEditRequest.user>;
  background?: HttpPart<ImageEditRequest.background>;
  output_format?: HttpPart<ImageEditRequest.output_format>;
  output_compression?: HttpPart<ImageEditRequest.output_compression>;
  input_fidelity?: HttpPart<ImageEditRequest.input_fidelity>;
  partial_images?: HttpPart<ImageEditRequest.partial_images>;
  stream?: HttpPart<ImageEditRequest.stream>;
}

model ImageVariationRequestParts {
  image?: HttpPart<ImageFile>[];
  `image[]`?: HttpPart<ImageFile>[];
  `model`?: HttpPart<ImageVariationRequest.`model`>;
  n?: HttpPart<ImageVariationRequest.n>;
  size?: HttpPart<ImageVariationRequest.size>;
  response_format?: HttpPart<ImageVariationRequest.response_format>;
  user?: HttpPart<ImageVariationRequest.user>;
  prompt?: HttpPart<ImageVariationRequest.prompt>;
  stream?: HttpPart<ImageVariationRequest.stream>;
}
