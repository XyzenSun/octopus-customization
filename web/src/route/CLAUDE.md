# web/src/route — 客户端路由声明

## 目录定位

绕过 Next.js 文件路由，用声明式配置管理 SPA 路由 + 按需加载 + 鼠标悬停预加载。

## 文件索引

- `config.tsx` — 路由配置：path/component/title/permission，注册所有页面
- `index.ts` — 路由器导出入口
- `lazy-with-preload.ts` — `lazyWithPreload(loader)`：在 `React.lazy` 基础上暴露 `preload()`
- `content-loader.tsx` — Suspense fallback 占位
- `use-preload.ts` — 鼠标 hover 时 `preload()` 路由组件，加快下次切换

## 关键约束

- **新增页面**：在 `config.tsx` 加一项配置 + 用 `lazyWithPreload(() => import("@/components/modules/xxx"))`，不要在 `app/` 物理建文件。
- **预加载使用**：导航/列表项用 `usePreload` 注册 hover 触发，不要无脑 preload all。
- **权限/认证**：路由配置项预留权限字段；登录态由 `provider/query.tsx` + 401 拦截器协同处理，不在路由层写大量逻辑。
