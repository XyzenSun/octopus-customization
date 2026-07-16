# internal/price — 模型定价

## 目录定位

模型价格表内存视图与远端同步逻辑。`relay/metrics.go` 在请求结束时调 `GetLLMPrice` 计算成本。

## 文件索引

- `price.go` — `GetLLMPrice(model string)` 查询接口 + `UpdateLLMPrice(ctx)` 远端拉取并刷新
- `presets.go` — 内置兜底价格表（远端不可达时使用）

## 关键约束

- **价格单位**：所有价格按"每 1M token 美元价"定义；`relay/metrics.go` 计算时统一乘 `1e-6`。新增价格字段时遵循此约定。
- **原子刷新**：`UpdateLLMPrice` 整体替换内存表，不要做"逐条 upsert"以免出现混合状态。
- **缓存读写分离**：`Get` 是高频热路径，必须无锁或读锁；写入只在 `Update` 调用，可以加写锁。
