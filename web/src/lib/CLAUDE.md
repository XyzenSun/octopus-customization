# web/src/lib — 通用工具与基础设施

## 目录定位

无 React/无业务依赖的纯工具，可被任何 src 子目录导入。

## 文件索引

- `utils.ts` — `cn(...inputs)` className 合并（clsx + tailwind-merge）
- `logger.ts` — 前端日志（console wrapper，可统一禁用/过滤）
- `info.ts` — 应用信息（版本、构建时间等编译期注入）
- `model-icons.tsx` — 模型 icon 渲染（按 model id 映射 SVG）
- `sw.ts` — Service Worker 工具
- `get-strict-context.tsx` — `createContext` 严格版（用前必须有 Provider，未提供时抛错而不是返回 undefined）
- `animations/` — framer-motion 共享动画配置（duration、easing、variants）

## 关键约束

- **零业务依赖**：本目录文件不能 import `@/components / @/api / @/stores`，否则会出现循环依赖。
- **classname 合并必须走 cn**：组件内拼 className 一律 `cn("base", cond && "extra")`，不直接字符串拼接（避免重复 utility）。
- **新增工具评估归宿**：先想是不是组件内私有；再想是不是应该进 `hooks/`（带 React 的）；都不是才放 lib。
