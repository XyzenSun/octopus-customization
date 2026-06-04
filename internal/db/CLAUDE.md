# internal/db — 数据库初始化与迁移

## 目录定位

GORM 数据库连接初始化、驱动选择（SQLite/MySQL/PostgreSQL）和 schema 迁移调度。

## 文件索引

- `db.go` — `Init()`/`GetDB()` 全局入口，按 conf 选择驱动，调用 `migrate.Run()`
- `migrate/` — 增量迁移脚本目录，详见子目录 CLAUDE.md

## 关键约束

- **唯一访问入口**：业务代码必须通过 `db.GetDB()` 获取 `*gorm.DB`，禁止保存全局变量副本（避免连接池被绕过）。
- **handler 不直连 db**：handler → `internal/op` → `db.GetDB()`，不要让 handler 直接调用 `db.GetDB()` 写 SQL。
- **多驱动兼容**：写迁移和查询时避免使用 SQLite/PG 独有语法（如 PG 的 `RETURNING`、SQLite 的 `WITHOUT ROWID`），需要时在 `migrate/` 里按驱动分支处理。
