# web/src/components/modules

按业务域组织面板, 表单和列表, 也包含登录, API Key 仪表板及导航/工具栏等应用级模块. 各域保留自身入口, 在域内按职责拆分组件与私有辅助逻辑.

一般跨域通用组件优先归 `common/`, API 调用复用 endpoints, 模块内复用现有 client 而非另建 wrapper. 现有例外包括 toolbar 导入其他域的创建内容, 以及模块共享导航/工具栏状态. 修改这些联动时先读 `web/src/components/modules/toolbar/index.tsx` 及其实际依赖, 保持既有入口可用.

新增请求或修改特殊传输时读 `web/src/api/AGENTS.md`, 核对普通 client 调用与既有例外. 调整日志实时连接时读 `web/src/api/endpoints/log.ts`, 保持 token 获取, 连接生命周期和缓存合并逻辑, 而不是在日志组件内另写一套流客户端.
