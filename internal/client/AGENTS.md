# internal/client

本目录提供直接连接, 系统代理和自定义代理的 HTTP client 工厂. http.go 按代理配置复用 client 与 Transport, 渠道如何选择 client 由 helper 处理.

新增 client 类型时优先复用现有连接池, 避免每次请求建立 Transport. 代理变更或并发创建时保持既有缓存与空闲连接回收行为.

本包当前不配置整体请求 Timeout, 请求生命周期由调用方的 context 或 relay pipeline 控制. 调整超时或连接池行为时读取 internal/client/http.go 及对应调用点, 区分连接超时, 整体请求超时与流式首事件超时. LLM 请求构造与协议适配留在现有 relay/helper 路径.
