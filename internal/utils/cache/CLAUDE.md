# internal/utils/cache — 分片并发缓存

## 目录定位

基于分片 + RWMutex 的内存 KV 缓存，给 op 层提供"非热路径但需要并发安全"的轻量缓存。

## 文件索引

- `cache.go` — 公共 API：`New / Get / Set / Delete / Range`
- `shard.go` — 分片实现：固定 N 个 shard，按 key hash 分散锁竞争

## 关键约束

- **非过期缓存**：当前实现不带 TTL，业务自己负责显式 `Delete` 或整体替换。需要 TTL 时优先看是否能用替换式刷新（参考 `op/llm.go`），而不是引入到本包。
- **不存大对象**：本包用于热路径上的小对象（id → 配置）；大 blob/请求体走 op 层专用缓冲（如 `relayLogCache`）。
- **泛型约束**：当前如果是非泛型 map[string]any，业务侧自行类型断言；新增封装时优先考虑用 Go 泛型重写而非每类业务一份副本。
