# internal/client — HTTP 客户端工厂

## 目录定位

封装通道（channel）专用的 `*http.Client`，统一超时、代理、Transport 复用策略，供 `internal/relay` 和 `internal/helper` 调用。

## 文件索引

- `http.go` — 通道 HTTP client 工厂，按 channel 配置构造带代理/超时的 client，复用底层 Transport

## 关键约束

- **client 复用**：相同代理 + 超时配置应共享 `*http.Transport`，避免每次请求都新建连接池导致 fd 泄漏。新增 client 类型时，先评估是否能复用现有 Transport。
- **职责单一**：本目录只造 client，**不**承载 LLM 请求构造、协议适配（那是 `internal/relay/transformers.go` 的事）。
- **超时策略**：必须显式设置 `Timeout` 或 `Context` 超时；流式请求（SSE）超时由 `relay` 层用 `context` 控制，client 本身可设较长 idle timeout。
