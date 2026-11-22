# internal/db

本目录管理 GORM 连接, 连接池和数据库初始化, migrate 子目录承载显式迁移. db.go 在模型 AutoMigrate 前后分别执行对应阶段的迁移.

调整数据库初始化或驱动参数时读取 internal/db/db.go, 核对连接池设置及迁移顺序. 需要显式迁移时读取 internal/db/migrate/AGENTS.md, 按现有注册机制选择执行阶段.

查询与迁移保持 SQLite, MySQL 和 PostgreSQL 兼容. 无法使用通用 GORM 表达的操作, 在迁移中按驱动分支处理, 参考既有列转换与删除逻辑核对 SQL.
