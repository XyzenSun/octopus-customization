# API 文档已废弃

本文件已废弃, 不再维护接口定义. 当前 HTTP 接口契约统一维护在 [TypeSpec 入口](./typespec/main.tsp) 及其导入的 `.tsp` 文件, [openapi.yaml](./openapi.yaml) 是生成的 OpenAPI 3.1.0 产物, 不手工修改. 契约覆盖管理接口, LLM 兼容接口和转发路由, 生成与审阅方法见 [维护说明](./typespec/README.md).

契约依据当前源码编写, 包含请求, 响应, 鉴权, 错误码, SSE 和文件上传/下载规则. 标注的实现限制及风险保留现状, 文档校验和预览不代表真实 API, 数据库或上游行为已验证.
