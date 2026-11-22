# web/src/api/endpoints

按资源域组织后端契约, 数据查询和变更 hooks. 此层复用 API client, 并负责各资源的 Query key, 缓存更新和调用错误反馈; 导出风格以同域现有代码为准.

普通 JSON 请求使用 `apiClient`, 新增或修改接口时核对对应后端 handler 和 model, 保持请求, 响应类型及成功后的缓存刷新一致. 调用失败交给 Query/Mutation 状态或明确的错误反馈, 不静默吞掉普通请求失败.

备份是既有传输例外. 修改上传/下载时先读 `web/src/api/endpoints/setting.ts`, 保留直接 fetch 所需的 Bearer 头, 上传 FormData, 下载 Blob/文件名和错误处理, 不将其强行套入仅处理普通 JSON 的调用方式.

修改日志实时流时先读 `web/src/api/endpoints/log.ts`: 先经 client GET stream-token, 再用 token query 参数建立 EventSource, 因浏览器 EventSource 不提供自定义认证 header. 保留解析失败反馈, 连接错误处理和卸载关闭连接的逻辑.
