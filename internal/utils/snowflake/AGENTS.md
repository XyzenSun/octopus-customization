# internal/utils/snowflake

本目录在 snowflake.go 中生成进程内递增的 int64 ID, 用于 RelayLog 主键. 实现用互斥锁保护毫秒时间戳与上次值, 没有 worker 配置; 时间未前进或回拨时递增上次值.

需要生成日志 ID 时沿用 op 的写入路径, 避免分散到 model 或 handler. 部署环境保持可靠时钟同步, 当前递增保护仅作用于本进程.

修改生成方式时读取 internal/utils/snowflake/snowflake.go 和 internal/op/log.go, 核对重启后的行为, ID 唯一性与日志按 id 倒序分页的依赖.
