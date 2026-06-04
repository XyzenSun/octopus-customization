# internal/op — 操作层（CRUD + 内存缓存）

## 目录定位

封装 `db` 操作并叠加内存缓存的中间层。**handler 必须经过本层访问数据**，禁止 handler 直接调用 `db.GetDB()`。

## 文件索引

- `cache.go` — 全局 `InitCache`/`SaveCache` 入口：启动时从 db 装载到内存，关停时落库
- `setting.go` — 设置读写 + 默认值表（`SettingGetBool/Int/String`）
- `channel.go` — 渠道 CRUD + 渠道 key 池缓存 + `ChannelKeySaveDB`
- `group.go` — 分组 CRUD + 模型映射缓存
- `apikey.go` — APIKey CRUD + 鉴权用 hash 缓存
- `user.go` — 用户 CRUD（少量，本地账号）
- `llm.go` — 模型信息缓存（来自 price 同步）
- `log.go` — RelayLog 写入聚合（批量内存缓冲 + 周期落库）+ SSE 订阅器
- `stats.go` — 统计累加（带读写锁的内存累加器，定时 `StatsSaveDB` 落库）
- `backup.go` — 全量导入/导出

## 关键约束

- **handler 入口唯一**：所有 handler 调用形如 `op.XxxList/Get/Create/Update/Delete`；不要绕过本层直接 `db.GetDB().Model(...)`。
- **缓存一致性**：写操作必须先写 db、再更新缓存（先缓存后 db 会因 db 失败导致缓存脏数据）。
- **批量聚合落库**：RelayLog/Stats 等高频写入走"内存缓冲 + 定时 task 落库"模式；新增高频写实体应沿用此模式（参考 `log.go` 的 `relayLogCache` + `RelayLogSaveDBTask`）。
- **shutdown 一定要落库**：新增带内存累加的能力，必须把 `XxxSaveDB(ctx)` 加到 `cache.go` 的 `SaveCache()` 里，否则 SIGTERM 后会丢数据。
- **关于日志模式与缓存**：`relayLogCache` 根据 `relay_log_mode` 设置决定行为：`disabled` 不记录任何日志、`memory` 只保留内存缓存（上限 100 条）、`persistent` 同时缓存（上限 20 条）并持久化到数据库；遇到内存或日志相关问题，先看 `log.go` 顶部常量 `relayLogMaxSizePersistent / relayLogMaxSizeMemory`。
