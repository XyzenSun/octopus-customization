# web/src/app

Next.js 的物理入口和全局壳. 根 layout 挂载全局 Provider, 通知和静态资源声明, 根 page 渲染 AppContainer, `globals.css` 放全局样式; 业务内容由面板模块承载.

新增业务面板通过 `route/` 接入, 保持这里薄, 不为面板增加一套文件页面导航. 调整全局 Provider 时读 `web/src/app/layout.tsx` 和 `web/src/provider/AGENTS.md`, 核对实际嵌套与依赖, 在 layout 集中挂载而不是在 page 重复包裹.

`globals.css` 保留全局 reset, 变量, 主题和基础样式; 组件专属样式用该组件的 Tailwind utility 表达.
