# internal/update — 版本更新检查

## 目录定位

检查 GitHub release 是否有新版本，供前端 setting 页和管理员手动触发使用。

## 文件索引

- `update.go` — 公开 API：`CheckLatest()` 返回最新版本元信息
- `core.go` — 内部实现：HTTP 请求 + 版本号解析

## 关键约束

- **网络失败要降级**：检查失败时返回当前版本作为兜底，不要让前端因 update 检查失败导致整个 setting 页加载失败。
- **不在启动期阻塞**：`CheckLatest` 由 handler 按需调用，不放进 `cmd/start.go` 的启动序列。
- **版本号语义**：使用 `internal/conf/version.go` 注入的 build 版本作为"当前版本"基准。
