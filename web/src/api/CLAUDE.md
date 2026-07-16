# web/src/api — 后端 API 客户端

## 目录定位

封装与 Go 后端的 HTTP 通讯：统一 fetch wrapper + 按资源拆分的端点函数与类型。

## 文件索引

- `client.ts` — 统一 fetch wrapper：注入 `Authorization: Bearer <jwt>`、解包 `{code,data,msg}`、401 自动登出
- `types.ts` — 跨资源共享类型（分页、统一响应等）
- `endpoints/` — 按资源拆分的端点函数：`apikey.ts / channel.ts / group.ts / log.ts / model.ts / setting.ts / stats.ts / update.ts / user.ts`

## 关键约束

- **唯一调用入口**：组件层只 import `endpoints/<resource>` 提供的函数，**不**直接 `fetch`、不绕过 `client.ts`。
- **类型与后端契约对齐**：每个 endpoint 文件的请求/响应类型对应 `internal/model/<resource>.go` 的 JSON tag；后端字段改名时本目录必须同步。
- **错误处理**：`client.ts` 已统一抛错，组件层用 try/catch 或 react-query 的 `onError`；不要在 endpoints 内部 try/catch 吞错。
- **新增资源**：在 `endpoints/` 新建 `<resource>.ts`，导出 `xxxList / xxxGet / xxxCreate / xxxUpdate / xxxDelete` 命名风格保持一致。
