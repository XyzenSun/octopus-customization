# internal/conf — 配置加载

## 目录定位

基于 viper + 环境变量 `OCTOPUS_*` 的配置加载层，提供全局配置访问入口与版本/调试信息。

## 文件索引

- `config.go` — 配置结构体定义 + `LoadConfig` 主入口；viper AutomaticEnv 绑定 `OCTOPUS_*` 前缀
- `const.go` — 配置 key 常量（默认值、env 名、配置路径）
- `banner.go` — 启动横幅打印
- `version.go` — 版本/commit/build time 元数据（由 `-ldflags` 注入）
- `debug.go` — debug 模式开关

## 关键约束

- **优先级（从高到低）**：环境变量 `OCTOPUS_X` > `--config <file>` 指定的 JSON > 默认值。新增配置项必须三处同步：结构体字段、`const.go` 默认值、文档说明。
- **不要在本包做业务**：本包只解析配置；带 setting 表的"运行时可改"配置应放到 `internal/op/setting.go`（DB 持久化 + 内存缓存）。
- **环境变量命名**：统一 `OCTOPUS_<UPPER_SNAKE>` 前缀，对应配置结构体路径用 `.` 分隔（viper 会自动转换）。

## 与 setting 表的边界

- `internal/conf` = 启动期不可变配置（监听端口、DB 连接串、JWT secret 等）
- `internal/op/setting` = 运行期可改、可在 UI 配置的设置（日志保留天数、模型同步周期等）
