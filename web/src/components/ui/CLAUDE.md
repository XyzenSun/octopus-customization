# web/src/components/ui — 基础原子组件

## 目录定位

shadcn 风格的 Radix UI 封装：accordion/button/dialog/select/table 等纯展示型/可访问的基础组件。

## 文件索引

`accordion / alert-dialog / badge / button / calendar / card / chart / dialog / field / input / label / morphing-dialog / popover / progress / select / separator / sonner / switch / table` 共约 19 个基础组件。

## 关键约束

- **不写业务**：只接 props 和 className，不读 store、不调 api、不内嵌 i18n key。
- **保持 shadcn 一致**：通过 `components.json` 维护风格；新增组件用 `pnpm dlx shadcn@latest add <name>` 生成（必要时改细节），保持目录命名 lowercase-kebab。
- **Tailwind variants**：用 `class-variance-authority` 表达变体，不要在组件内手写 if-else 拼 classname。
- **样式合并**：用 `lib/utils.ts` 的 `cn` 合并 className；不直接拼字符串。
