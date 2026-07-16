# web/src/components — UI 组件库

## 目录定位

按"原子—通用—业务"三级组织的组件目录。

## 文件索引

- `ui/` — Radix-based 基础原子组件（accordion/button/dialog/select/table/...），由 shadcn-style 生成
- `common/` — 业务通用复合组件：`AnimatedNumber / CopyButton / PageWrapper / Toast / VirtualizedGrid`
- `modules/` — 按业务域拆分（详见子目录）：`channel / group / log / setting / home / login / apikey-dashboard / model / navbar / toolbar / logo`
- `animate-ui/` — 动画基元（components 与 primitives，复用 `lib/animations/`）
- `app.tsx` — 应用根组件（挂路由）
- `sw-register.tsx` — Service Worker 注册入口

## 关键约束

- **分层依赖方向**：`modules` 可以用 `common`/`ui`/`animate-ui`；`common` 可以用 `ui`/`animate-ui`；`ui` 不依赖任何上层。**禁止反向依赖**。
- **新增基础组件先看 `ui/`**：避免重复造 button/dialog；现有 `ui/*` 不够时再自己加，并用 `components.json` shadcn 风格保持一致。
- **业务组件归 modules**：新增页面相关组件按"业务域"放到 `modules/<domain>/`，不要塞进 `common/`。
- **i18n**：组件内文案统一用 `useTranslations`，与 `public/locale/` 翻译文件对齐。
