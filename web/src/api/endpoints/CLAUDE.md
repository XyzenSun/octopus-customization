# web/src/api/endpoints — API 资源端点

## 目录定位

按资源拆分的 API 调用函数集合。每个文件对应后端一个资源域。

## 文件索引

- `apikey.ts` — `/api/v1/apikey` CRUD
- `channel.ts` — `/api/v1/channel` CRUD + 测试连接
- `group.ts` — `/api/v1/group` CRUD + 模型映射
- `user.ts` — 登录、改密、用户信息
- `setting.ts` — 设置项读写
- `model.ts` — 可用模型列表
- `log.ts` — 日志列表/清空 + SSE token + EventSource 连接
- `stats.ts` — 多维度统计查询
- `update.ts` — 版本更新检查

## 关键约束

- **统一通过 client wrapper**：每个函数内部调 `client.get/post/put/delete`，不直接 `fetch`。
- **命名一致**：导出函数采用 `xxxList / xxxGet / xxxCreate / xxxUpdate / xxxDelete` 模式；类型导出名 `XxxItem / XxxCreateReq / XxxUpdateReq`。
- **SSE 特殊处理**：`log.ts` 的 stream 端点需要"先 GET token，再用 query 参数构造 EventSource"两步；浏览器 `EventSource` 不支持自定义 header，所以走这种模式（对应后端 `internal/server/handlers/log.go`）。
- **类型对齐后端**：新增/修改字段后，去 `internal/model/<resource>.go` 核对 JSON tag，避免运行时字段不存在。
