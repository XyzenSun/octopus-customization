# cmd — Cobra CLI 入口

## 目录定位

CLI 命令定义层，由 `main.go` 调用 `cmd.Execute()` 启动。仅处理命令行解析、配置加载和信号编排，不承载业务逻辑。

## 文件索引

- `root.go` — 根命令 `octopus`，注册子命令；定义全局 flag `--config`
- `start.go` — `start` 子命令：加载配置 → 初始化 db/op cache → 启动 task scheduler 与 HTTP server → 阻塞等待 shutdown 信号
- `version.go` — `version` 子命令：输出 build 信息（版本、commit、build time）

## 关键约束

- **业务逻辑下沉**：本目录只做编排（按顺序调用 `db.Init / op.InitCache / task.Init / server.Start`），任何业务判断写到对应 `internal/*` 包。
- **配置加载顺序**：环境变量 `OCTOPUS_*` > `--config` 指定文件 > 默认值，由 `internal/conf` 实现，本目录直接调用。
- **优雅关闭**：通过 `internal/utils/shutdown` 注册回调，`start.go` 监听 SIGTERM/SIGINT 后触发 `op.SaveCache()` 落库。新增需要在退出时持久化的资源时，在 shutdown 钩子里登记，而不是在 `start.go` 写硬编码。
- **新增子命令**：在本目录新建 `xxx.go`，在 `init()` 里调用 `rootCmd.AddCommand()` 注册。
