# web/src/components/ui

基础 UI 目录, 包括 Radix/shadcn 风格封装, 展示组件及动画交互组件. 组件通过 props 和组合提供能力, 可以有交互所需的局部状态, 保持与业务 API 和 store 解耦.

业务数据获取, 领域判断和具体 i18n key 放在调用方, 通过 props 传入基础组件. 变体优先用 class-variance-authority, className 合并使用 `web/src/lib/utils.ts` 的 `cn`, 避免另写重复的类名拼接机制.

新增基础组件先复用现有实现. 需要生成组件时读 `web/components.json`, 核对风格, 别名和 registry, 从项目根执行 `(cd web && pnpm dlx shadcn@latest add <name>)` 并按项目需要调整, 文件命名保持 lowercase-kebab.
