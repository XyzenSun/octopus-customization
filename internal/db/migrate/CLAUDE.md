# internal/db/migrate — Schema 迁移

## 目录定位

按编号递增的增量数据库迁移脚本，启动时由 `migrate.Run()` 顺序执行。

## 文件索引

- `migrate.go` — 迁移调度器：维护 `migrations` 列表，按编号执行未应用的迁移
- `001.go` `002.go` `003.go` — 编号迁移文件，每个返回 `Migration{ID, Up}` 由调度器调用

## 关键约束

- **新增迁移**：在本目录新建 `NNN.go`（编号比当前最大值 +1），在 `migrate.go` 的 `migrations` 切片末尾追加。**不要插入到中间或修改已发布的迁移文件**。
- **幂等性**：`Up` 函数应能在已迁移的库上安全重跑（用 `AutoMigrate` 或 `if not exists` 守卫）。
- **跨驱动**：避免 SQLite/MySQL/PG 独有语法。优先用 GORM `AutoMigrate(&model.Xxx{})`，只在它无法表达（索引、列重命名）时写原生 SQL，并在 SQL 内分别处理三种驱动。
- **不要写数据回填到迁移**：schema 变更归 migrate，数据初始化/默认值归 `internal/op/*RefreshCache` 或单独的 seed 脚本。
