# internal/server/resp — 统一响应格式

## 目录定位

所有 HTTP 响应统一走本包，保证前端 `web/src/api/client.ts` 的解析逻辑稳定。

## 文件索引

- `resp.go` — `Success(c, data)`：HTTP 200 + `{code:0, data:..., msg:"ok"}`
- `error.go` — `Error(c, httpStatus, msg)`：标准化错误响应 + 常用错误码常量

## 关键约束

- **唯一出口**：handler 必须用 `resp.Success/Error`，禁止 `c.JSON()` 直出。
- **响应字段稳定**：`code/data/msg` 三字段是前后端契约，新增顶层字段会破坏前端解析；扩展信息放进 `data` 内部。
- **错误码分层**：HTTP status 表达传输层（400/401/500），业务错误细节通过 `msg` 描述；不要发明新业务码字段。
