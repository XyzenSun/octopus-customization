# web/src — 前端源码

## 目录定位

Next.js App Router + React 19 的源码根。按"基础设施 / 业务模块 / 通用工具"三类组织。

## 文件索引（按子目录）

- `app/` — Next.js App Router 路由（`layout.tsx` 全局壳、`page.tsx` 入口、`globals.css`）
- `api/` — 后端 API 客户端：`client.ts` 统一 fetch wrapper + `endpoints/` 各资源类型与函数
- `components/` — UI 组件：`ui/`（基础原子）、`common/`（业务通用）、`modules/`（按域分模块）、`animate-ui/`（动画基元）、`app.tsx`（应用根）、`sw-register.tsx`（Service Worker 注册）
- `route/` — 路由声明：`config.tsx` 定义页面 + `lazy-with-preload.ts` 实现按需加载 + 预加载
- `stores/` — Zustand 全局 store（当前只有 `setting.ts`）
- `provider/` — React Provider（`locale.tsx` / `query.tsx` / `theme.tsx`）
- `hooks/` — 自定义 hooks（`use-mobile.ts` / `useClickOutside.tsx`）
- `lib/` — 通用工具：`utils.ts` cn classnames、`logger.ts` 前端日志、`info.ts`、`model-icons.tsx`、`sw.ts`、`animations/`、`get-strict-context.tsx`

## 关键约束

- **状态管理**：全局共享状态用 Zustand store（在 `stores/`）；组件局部状态用 `useState`。**不要**引入 Redux/Recoil。
- **服务端数据**：通过 `@/api/endpoints/<resource>` 调用，自动经过 `client.ts` 的 JWT 注入；**不**直接 `fetch`，不绕过 client wrapper。
- **路由组织**：新增页面在 `route/config.tsx` 注册条目（用 `lazyWithPreload`），不直接在 `app/` 写大量物理文件。
- **样式**：TailwindCSS 4 utility-first；只有 `globals.css` 写少量全局变量与 reset，组件不写独立 .css。
- **i18n**：所有用户可见文案用 `next-intl` 的 `useTranslations`，避免硬编码中/英字符串。
