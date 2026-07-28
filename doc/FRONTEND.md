# 前端说明文档

Octopus 管理面板基于 **Next.js 16 + React 19**，采用静态导出（`output: "export"`）模式，构建产物移至项目根 `static/out/`，由 Go `embed` 嵌入后端二进制统一分发。

## 技术栈

| 类别 | 选型 | 说明 |
|------|------|------|
| 框架 | Next.js 16（Turbopack） | 静态导出，无 SSR / API routes |
| UI 库 | React 19 | 启用 React Compiler（`babel-plugin-react-compiler`） |
| 状态管理 | Zustand 5 | 客户端偏好（主题、语言），见 `src/stores/` |
| 服务端数据 | TanStack Query 5 | `src/api/endpoints/` 各资源 hook |
| 样式 | TailwindCSS 4 | utility-first，shadcn 风格基础组件 |
| i18n | next-intl 4 | 三语：简中 `zh_hans` / 繁中 `zh_hant` / 英 `en` |
| 图标 | lucide-react | |
| 图表 | Recharts 2 | 首页活跃度图表 |
| 动画 | motion 12 + tw-animate-css | |
| 拖拽 | @hello-pangea/dnd | 分组排序 |
| 虚拟滚动 | @tanstack/react-virtual | 长列表性能 |

## 目录结构

```
web/
├── package.json / pnpm-lock.yaml
├── next.config.ts          # 静态导出 + React Compiler + dev rewrites 代理
├── tsconfig.json           # 路径别名 @/ → src/
├── public/locale/          # i18n 翻译文件（en / zh_hans / zh_hant）
└── src/
    ├── app/                # Next.js App Router（layout.tsx / page.tsx / globals.css）
    ├── api/               # 后端 API 客户端
    │   ├── client.ts       # 统一 fetch wrapper（JWT 注入、401 登出、{code,data,msg} 解包）
    │   ├── types.ts        # 跨资源共享类型
    │   └── endpoints/      # 按资源拆分的 hook（useXxxList / useXxxCreate ...）
    ├── components/
    │   ├── ui/             # shadcn 风格基础原子（button / dialog / select / table ...）
    │   ├── common/         # 业务通用复合（Toast / CopyButton / PageWrapper ...）
    │   ├── modules/        # 按业务域分模块（channel / group / log / setting ...）
    │   └── animate-ui/     # 动画基元
    ├── hooks/              # 自定义 hooks（use-mobile / useClickOutside）
    ├── lib/                # 工具库（utils.ts cn / logger / model-icons / animations）
    ├── provider/           # 应用级 Provider（locale / theme / query）
    ├── route/              # 路由声明（config.tsx + lazy-with-preload）
    └── stores/             # Zustand store（setting.ts）
```

## 开发

```bash
# 安装依赖
cd web && pnpm install

# 开发模式（API 通过 next.config.ts rewrites 代理到 127.0.0.1:8080，无跨域）
cd web && pnpm run dev

# 远程后端开发（需配合后端 CORS 设置）
cd web && NEXT_PUBLIC_API_BASE_URL="https://<host>:8080" pnpm run dev

# 类型检查
cd web && pnpm exec tsc --noEmit

# 构建（产物在 web/out/）
cd web && pnpm run build

# 交付到 Go embed
cd .. && rm -rf static/out && cp -r web/out static/out
```

## 关键约束

### 静态导出限制

`output: "export"` 模式下**不能用**：
- Next.js Server Components 的 server-side 数据获取
- API routes（`app/api/...`）
- middleware
- `rewrites` / `redirects`（生产构建不生效，仅 dev 模式可用）

所有数据通过 `src/api/client.ts` 调后端，无 server 兜底——后端字段改名时前端必须同步，否则运行时崩溃。

### 分层依赖方向

```
modules/  →  common/  →  ui/  →  lib/
                     ↘  animate-ui/
```

- `ui/` 不依赖任何上层，只接 props + className
- `common/` 可用 `ui/` / `animate-ui`
- `modules/` 可用 `common/` / `ui/` / `animate-ui`
- **禁止反向依赖**（如 `ui/` 引用 `modules/`）

### 状态管理

| 类型 | 位置 | 示例 |
|------|------|------|
| 客户端偏好 | Zustand `stores/` | 主题、语言 |
| 服务端数据 | TanStack Query `api/endpoints/` | 通道列表、日志、统计 |
| 组件局部 | `useState` | 表单输入、弹窗开关 |

**不引入** Redux / Recoil；服务端数据不进 Zustand。

### API 调用

- 组件**只 import** `@/api/endpoints/<resource>` 提供的 hook，**不直接** `fetch`
- `client.ts` 统一注入 `Authorization: Bearer <jwt>`、解包 `{code, data, msg}`、401 自动登出
- 新增资源：在 `endpoints/` 新建 `<resource>.ts`，导出 `useXxxList / useXxxCreate / useXxxUpdate / useXxxDelete` 风格 hook
- 类型与后端 `internal/model/<resource>.go` 的 JSON tag 对齐

### i18n

- 所有用户可见文案用 `useTranslations('namespace')`，避免硬编码
- 新增文案必须在 `public/locale/{en,zh_hans,zh_hant}.json` **三语同步**
- **locale 标签**：内部用下划线（`zh_hans` / `zh_hant` / `en`，用于 localStorage / 文件名），传给 `NextIntlClientProvider` 时转为 BCP 47（`zh-Hans` / `zh-Hant` / `en`），见 `src/provider/locale.tsx` 的 `localeToBCP47` 映射
- **带变量的文案**用 ICU 模板：`"剩余冷却 {seconds}s"` + `t('key', { seconds: 30 })`

### 路由

- 路由在 `src/route/config.tsx` 声明式注册（`lazyWithPreload` 按需加载 + 预加载）
- 不在 `app/` 写大量物理文件；新增页面在 `config.tsx` 加条目 + `components/modules/` 新建目录

### 样式

- TailwindCSS 4 utility-first
- 全局变量与 reset 在 `app/globals.css`，组件不写独立 .css
- className 合并用 `lib/utils.ts` 的 `cn()`
- 基础组件变体用 `class-variance-authority`，不手写 if-else 拼 classname

## Provider 树

`src/app/layout.tsx` 一次性包裹：

```
LocaleProvider        # next-intl i18n
  └─ ThemeProvider    # 亮 / 暗 / 跟随系统
     └─ QueryProvider # TanStack Query + DevTools（401 自动登出）
        └─ AppContainer
```

新增 Provider 在 `provider/` 新建文件，并在 `layout.tsx` 调整嵌套顺序。

## 业务模块

`src/components/modules/` 按域组织，每个目录自治：

| 模块 | 说明 |
|------|------|
| `home/` | 首页：总量、活跃度图表、排行 |
| `channel/` | 渠道（上游 LLM 供应商）：列表、详情、创建、表单 |
| `group/` | 渠道分组：模型映射、负载均衡策略编辑 |
| `apikey-dashboard/` | API Key 仪表板 |
| `log/` | 中继日志列表 + 实时流（SSE） |
| `setting/` | 设置页：账户、外观、备份、日志、熔断器 ... |
| `model/` | 模型展示 |
| `login/` | 登录页 |
| `navbar/` / `toolbar/` / `logo/` | 全局导航 / 工具栏 / Logo |

模块间不互相 import 内部文件；跨模块共享走 `common/`。

## SSE 实时流（日志模块）

浏览器 `EventSource` 不支持自定义 header，所以日志流采用"两步"模式：

1. `GET /api/v1/log/stream-token` 拿一次性 token（JWT 鉴权）
2. 用 token 作 query 参数构造 `EventSource`：`/api/v1/log/stream?token=xxx`
3. 后端验证并 revoke token，避免 token 复用

新增 SSE 端点沿用此模式。

## 构建产物嵌入

```
web/out/  ──(cp -r)──>  static/out/  ──(go:embed)──>  二进制
```

`internal/server/middleware/static.go` 通过 `embed.FS` 提供 SPA 服务；前端构建产物路径变化时同步更新 embed 指令。

## 常见问题

### 修改后端 API 后前端类型不匹配

后端 `internal/model/<resource>.go` 字段改名 → 同步更新 `web/src/api/endpoints/<resource>.ts` 的 interface，否则运行时字段不存在（无 server 兜底）。

### i18n 文案显示为 key 路径

若 UI 上出现 `setting.xxx.yyy` 字面文本，检查：
1. `public/locale/<lang>.json` 是否有该 key
2. `src/provider/locale.tsx` 的 `localeToBCP47` 映射是否正确（next-intl 4.x 严格校验 BCP 47 标签）
3. 带变量的文案是否传了正确的 values

### dev 模式跨域

`next.config.ts` 在 dev 模式下通过 `rewrites` 把 `/api/*` 和 `/v1/*` 代理到 `http://127.0.0.1:8080`，无需 CORS 配置。生产构建不用 rewrites（静态导出不支持），前端与后端同源。

### React Compiler

`next.config.ts` 中 `reactCompiler: true` 启用。React Compiler 会自动 memoize，**不要**手动滥用 `useMemo` / `useCallback`；新增组件按纯函数写法即可。
