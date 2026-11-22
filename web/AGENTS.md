# web

管理面板的前端目录. `src/` 放应用源码, `public/` 放静态资源和翻译, 顶层配置文件管理依赖, 路径别名和 Next.js 行为. 目录概览与跨前后端约定沿用项目根文档.

生产交付保持静态导出兼容, 需要请求时服务端执行的数据处理放在 Go 后端. 修改运行配置时先读 `web/next.config.ts`, 区分生产导出与开发 API 代理; 调整开发后端地址时读 `web/src/api/client.ts`, 核对 `NEXT_PUBLIC_API_BASE_URL` 与相对地址回退行为.

组件和工具优先使用 `@/` 路径别名. 新增或修改用户文案时, 同步更新 `web/public/locale/` 下各语言 JSON 的 key; 每种语言对应一个文件, 加载与语言标签映射以 `web/src/provider/locale.tsx` 为准, 修改语言支持时再读该文件.

前端接口类型调整时读 `web/src/api/AGENTS.md`, 定位对应 endpoint 并核对认证, 响应处理与特殊传输例外.
