# internal/utils/cache

本目录实现泛型分片缓存. cache.go 提供操作入口与分片选择, shard.go 用读写锁保护各分片的 map, 主要存储配置等小对象.

缓存没有 TTL, 业务通过显式删除, 清空或刷新管理生命周期. 大正文沿用 op 的专用缓冲, 避免放入通用配置缓存.

调整缓存调用或并发行为时读取 internal/utils/cache/cache.go 和 internal/utils/cache/shard.go, 核对泛型类型, 实际删除/遍历方式与分片锁的边界, 保持业务生命周期管理在调用方.
