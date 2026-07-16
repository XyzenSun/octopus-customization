# internal/task — 定时任务调度

## 目录定位

注册 + 执行后台周期任务（价格同步、模型同步、统计/日志落库、通道延迟探测）。

## 文件索引

- `task.go` — 调度器核心：`Register(name, interval, runOnStart, fn)` + `RUN()` 启动所有
- `init.go` — `Init()` 注册项目内所有任务（调用 `Register`）
- `sync.go` — `SyncModelsTask`：从上游同步可用模型列表
- `channel.go` — `ChannelBaseUrlDelayTask`：探测各通道 base URL 延迟

## 关键约束

- **统一注册入口**：新增任务在 `init.go` 末尾加 `Register(...)` 调用；不要在其他包散落 `time.Ticker`。
- **任务周期可配置**：周期来自 setting 表（如 `SettingKeyStatsSaveInterval`），不要硬编码间隔。例外：固定行为（如 `TaskRelayLogSave` 10 分钟）可硬编码，但应在常量里命名。
- **任务函数容错**：`fn` 内部要 `recover` panic 或返回 error，避免单个任务崩溃影响调度器；本包内已注册的任务遵循此规范。
- **shutdown 不抢任务**：任务函数自己负责对 `context.Done()` 响应；调度器在退出时调 `op.SaveCache()` 落库一次（在 `cmd/start.go` 里）。
