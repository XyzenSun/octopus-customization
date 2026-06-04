# web/src/app — Next.js App Router

## 目录定位

Next.js 16 App Router 的物理路由根。**保持极薄**：只挂全局壳和入口，业务路由由 `src/route/config.tsx` 声明式管理。

## 文件索引

- `layout.tsx` — 根布局：HTML 壳、Provider 树（locale/theme/query）、字体加载
- `page.tsx` — 入口页：渲染 `<App />`（实际路由在 `src/components/app.tsx` + `src/route/config.tsx`）
- `globals.css` — 全局样式（Tailwind base/components/utilities + CSS 变量）

## 关键约束

- **不在本目录建多个 page.tsx**：业务页面用 `route/config.tsx` 注册 + `lazyWithPreload` 按需加载，**不**走 Next.js 文件路由。理由：项目是静态导出 SPA，统一路由更易做预加载与权限控制。
- **Provider 在 layout 一次性挂载**：新增 Provider 也加到 `layout.tsx` 树里，不要在 `page.tsx` 重复包裹。
- **globals.css 只写全局**：组件级样式写在组件文件内（Tailwind utility）；本文件只放 reset、CSS 变量、字体。
