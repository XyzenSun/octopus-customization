# web/src/api

后端 HTTP 通信层. `client.ts` 处理普通 JSON 请求, Bearer 认证, 响应解包和 HTTP 错误, `types.ts` 放共享类型, `endpoints/` 按资源域组织类型与 Query/Mutation hooks.

普通业务请求优先复用 endpoints 和现有 `apiClient`. 响应使用 `code/message/data`, client 解包成功响应的 `data`, HTTP 401 通过注册的认证 store getter 触发登出. 修改认证时一起读 `web/src/api/client.ts` 与 `web/src/api/endpoints/user.ts`, 保留用户 token 和 API Key 两种 Bearer 模式.

既有例外是备份上传/下载直接 fetch, 日志 SSE 使用 EventSource, 以及应用容器直接 `apiClient` 预取. 修改特殊传输前读 `web/src/api/endpoints/AGENTS.md`, 保留认证与错误处理, 复用既有流程而不是另建通用 client wrapper.

新增资源沿用邻近 endpoint 的 hook 和类型命名. 修改字段时核对对应后端 handler 的请求/响应及 model JSON tag, 同步调用方. 普通请求错误交给调用方或 Query/Mutation 回调处理, 必要的局部捕获明确反馈失败.
