# internal/utils/shutdown

本目录在 shutdown.go 中管理关闭回调与信号监听. 初始化会清空回调表, 监听 SIGINT, SIGTERM 和 SIGHUP 后按注册逆序执行; 程序更新也会调用这些回调.

在初始化之后注册资源关闭操作, 按逆序执行安排依赖, 避免在包 init 中登记后被清空. 调整关闭顺序时读取 internal/utils/shutdown/shutdown.go 和 cmd/start.go, 核对初始化, 注册与阻塞监听的位置.

回调保持幂等并在内部限时, 避免退出流程无限阻塞. 需要数据保存的退出操作沿用信号或程序内关闭入口, 让缓存保存钩子有机会完成.
