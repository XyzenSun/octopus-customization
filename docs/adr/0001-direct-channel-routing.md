---
status: accepted
---

# 分组优先的指定渠道直连

客户端模型首先按完整字符串精确匹配分组；只有分组不存在且模型包含 `/` 时，才按第一个 `/` 解析为 `channelName/modelName`。直连只读取当前渠道与模型内存快照，不进入 `/v1/models`，不使用分组负载均衡、sticky session 或首 token 超时，并且只尝试所指定渠道一次；渠道禁用、无可用 Key、无有效 BaseURL 或目标 Key/模型熔断均返回 503。

受限 API Key 只能访问 `SupportedModels` 中的正常分组，不能通过写入 `channelName/modelName` 获得直连权限。名称比较严格区分大小写且不 trim；上游模型配置仍按既有逗号分隔、trim、去空规则读取。

上游 HTTP 错误保留状态码，并通过 axonhub/llm 的 outbound `TransformError` 产生 `llm.ResponseError`，再由入站 `TransformError` 转成客户端协议错误体；没有上游 HTTP 状态的网络、空响应或转换错误返回 502，客户端取消后不再写响应。直连保持现有 Key 选择后再检查熔断的语义，不因本功能改选同渠道其他 Key。
