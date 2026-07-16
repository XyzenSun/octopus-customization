# internal/utils — 通用工具集

## 目录定位

无业务依赖的纯工具子包；可被任何 `internal/*` 包导入，**不应**反向依赖业务包。

## 文件索引（按子目录）

- `cache/` — 分片并发安全缓存（sync.RWMutex 分片版 map）
- `diff/` — 结构体字段差异比较
- `log/` — 全局 zap logger（`log.Infof/Warnf/Errorf/Debugf` + 动态级别 `SetLevel`）
- `shutdown/` — 优雅关闭钩子注册（按 SIGTERM/SIGINT 触发）
- `snowflake/` — `GenerateID()` 雪花 ID 生成器（用于 RelayLog 等表的主键）
- `xstrings/` — 字符串扩展工具（脱敏、截断等）

## 关键约束

- **零业务依赖**：本目录任何子包都不能 import `internal/model / op / relay / server / task`，否则会出现循环依赖。
- **新增工具谨慎**：先看现有六个子包能否容纳；不要在本目录平级开新文件，统一以子包方式组织。
- **日志使用**：业务代码统一通过 `internal/utils/log` 输出，不要直接 `fmt.Printf` 或自建 zap logger 实例。
