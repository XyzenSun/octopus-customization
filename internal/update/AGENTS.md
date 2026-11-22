# internal/update

本目录处理 GitHub release 信息查询, 更新包下载/解压和程序重启. update.go 包含网络与解压逻辑, core.go 编排可执行文件更新与重启.

版本查询由管理 handler 按需调用, 继续避免放入启动阻塞流程. 当前直连失败会尝试系统代理, 最终失败返回 error, 没有返回当前版本的兜底.

调整更新流程时读取 internal/update/update.go 和 internal/update/core.go, 保持请求超时, 解压目标路径校验, 跳过符号链接与重启前执行关闭钩子的现有行为. 变更失败响应时同时核对 internal/server/handlers/update.go, 保持查询与更新接口的错误处理一致.
