# internal/utils/shutdown — 优雅关闭钩子

## 目录定位

注册 SIGTERM/SIGINT 关闭钩子，由 `cmd/start.go` 在收到信号后串行执行所有钩子（一般是 `op.SaveCache()` 落库）。

## 文件索引

- `shutdown.go` — `Register(name, fn)` 注册钩子；`Wait()` 阻塞等待信号并串行执行

## 关键约束

- **钩子幂等**：钩子可能被重复触发（如双 SIGTERM），实现需幂等。
- **钩子要带超时**：钩子内部应使用 `context.WithTimeout` 限时；不要写无限阻塞操作。
- **不要 kill -9**：`kill -9` 不触发本机制，会丢失内存累计的 stats/relay log。文档/部署说明应明确使用 SIGTERM 或 Ctrl+C。
- **注册时机**：在各包 `init()` 或 `cmd/start.go` 启动序列中注册；`Wait()` 在 `start.go` 主流程末尾调用一次。
