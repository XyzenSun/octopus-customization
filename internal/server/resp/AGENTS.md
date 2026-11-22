# internal/server/resp

本目录定义管理 JSON 响应结构与常用错误消息. resp.go 封装成功与错误输出, error.go 保存可复用的错误消息.

普通管理 JSON 响应统一使用本包. 成功时 HTTP status 和 code 均为 200, message 为 success; 错误时 code 与 HTTP status 一致, message 承载错误说明. data 可省略, 扩展内容放在 data 内保持已有契约稳定.

修改响应契约时读取 internal/server/resp/resp.go, 核对字段标签和错误中止行为. 文件导出, SSE 与 LLM 协议响应保留各自格式, 无需套用管理 JSON 包装.
