# internal/utils/snowflake — Snowflake ID 生成器

## 目录定位

生成趋势递增的 64-bit ID，用于 RelayLog 等高频写入表的主键。

## 文件索引

- `snowflake.go` — 包级 `GenerateID() int64`（含 worker id 配置初始化）

## 关键约束

- **唯一全局调用点**：写入需要 ID 的实体在 `op` 层调 `snowflake.GenerateID()`，**不**在 model struct 内部调用、不在 handler 调用。
- **不依赖时钟回拨保护**：当前实现不强制处理时钟回拨；部署环境应启用 NTP 平滑同步。
- **ID 排序作为分页隐含契约**：`RelayLogList` 用 `Order("id DESC")` 等价于按时间倒序；改 ID 生成方式时要同步评估这条假设。
