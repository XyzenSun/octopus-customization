# web/src

前端源码根目录. `app/` 提供入口和全局壳, `components/` 放通用组件与业务模块; API, Provider, 导航和状态等基础设施各有独立目录, `hooks/` 与 `lib/` 承载共享辅助能力.

新增业务面板接入 `route/` 和对应模块, 保持 `app/` 入口薄. 修改接入方式时读 `web/src/route/AGENTS.md`, 确认模块加载和预加载约定, 而不是增加一套独立导航机制.

客户端状态沿用现有工具: 局部状态留在组件, 共享状态按职责放在 `stores/` 或所属域的现有 store, 不另引入 Redux/Recoil. 请求优先复用 `api/endpoints/` 的现有能力; 修改请求封装或新增特殊传输时, 读 `web/src/api/AGENTS.md` 核对既有例外.

样式沿用 Tailwind utility, 组件样式留在组件文件, `app/globals.css` 承载全局样式和变量. 共享 hook 与工具按复用范围归位, 单个模块的私有实现留在该模块.
