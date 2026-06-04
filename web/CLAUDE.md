# web — 管理面板前端

## 目录定位

Next.js 16 + React 19 的管理面板。**静态导出**（`output: "export"`）后产物移到项目根 `static/out/`，由 Go embed 嵌入二进制。

## 文件索引（顶层）

- `package.json` / `pnpm-lock.yaml` — pnpm 依赖（Zustand、TailwindCSS 4、next-intl、Radix UI、Recharts）
- `next.config.ts` — 静态导出配置 + React Compiler 开关
- `tsconfig.json` — TS 配置（路径别名 `@/`）
- `eslint.config.mjs` / `postcss.config.mjs` / `components.json`
- `public/` — 静态资源（含 `public/locale/` 翻译文件）
- `src/` — 应用源码（详见 src 子目录）

## 关键命令（包装而非详述）

```bash
# 开发模式（指向后端）
cd web && NEXT_PUBLIC_API_BASE_URL="http://127.0.0.1:8080" pnpm run dev

# 构建并交付到 Go embed
cd web && pnpm run build && cd .. && mv web/out static/
```

## 关键约束

- **静态导出限制**：不能用 Next.js Server Components 的 server-side 数据获取、API routes、middleware；所有数据走 `src/api/client.ts` 调后端。
- **路径别名**：组件/工具优先用 `@/...` 引入，不要写大量 `../../`。
- **i18n**：新增文案必须在 `public/locale/<lang>/*.json` 同步增加 key；运行时由 `next-intl` 加载。
- **后端契约**：修改后端 API 时同步更新 `src/api/endpoints/<resource>.ts` 类型，否则会运行时崩溃（无 server 兜底）。
