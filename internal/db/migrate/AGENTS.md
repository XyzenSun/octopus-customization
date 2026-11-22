# internal/db/migrate

本目录保存编号迁移与执行记录. migrate.go 分别维护 AutoMigrate 前后的注册表, 各编号文件在 init 中登记 Version 和 Up; 每个阶段按版本排序, 已成功的迁移跳过.

新增显式迁移时先核对现有编号, 使用递增且不重复的版本, 并在新文件中注册到合适阶段. 已发布的迁移保持稳定, 后续变化用新版本表达. 判断阶段时读取 internal/db/db.go 和 internal/db/migrate/migrate.go, 确认操作需要发生在模型自动迁移之前还是之后.

迁移应能安全重跑, 用表/列存在性检查等守卫处理新库与旧库, 原生 SQL 按驱动兼容处理. 旧数据结构转换可随迁移执行; 常规 setting 默认值仍由缓存加载时补齐.
