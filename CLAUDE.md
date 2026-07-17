# Octopus - LLM API Aggregation & Load Balancing Service

## Project Overview

Octopus 是一个面向个人用户的 LLM API 聚合与负载均衡服务，核心能力是将多个 LLM 供应商（OpenAI、Anthropic、Gemini、豆包等）的 API 统一为一个入口，并提供智能选路、负载均衡、协议转换和用量统计。

项目采用前后端分离架构，后端 Go（Gin + GORM + Cobra）提供 API 服务与 LLM 转发，前端 Next.js 16（React 19 + Zustand + TailwindCSS 4）提供管理面板 UI。

## Directory Structure

```
octopus-customization/
├── main.go                  # 入口：调用 cmd.Execute()
├── cmd/                     # Cobra CLI 命令定义（start、version）
├── internal/                # 后端核心业务（全部 Go）
│   ├── client/              # HTTP 客户端（通道专用 client）
│   ├── conf/                # 配置加载（viper + 环境变量 OCTOPUS_*）
│   ├── db/                  # 数据库初始化与迁移（SQLite/MySQL/PG）
│   ├── helper/              # 辅助工具（延迟检测、价格同步、通道 HTTP client）
│   ├── model/               # GORM 数据模型（channel、group、apikey、log、setting、stats）
│   ├── op/                  # 操作层：CRUD + 内存缓存（不直接暴露 db 给 handler）
│   ├── price/               # 模型定价管理
│   ├── relay/               # 核心转发层
│   │   ├── balancer/        # 负载均衡策略（RoundRobin/Random/Failover/Weighted）
│   │   ├── transformers.go  # 入站/出站协议适配器工厂（OpenAI/Anthropic/Gemini/豆包）
│   │   ├── relay.go         # 请求转发主流程 + SSE 流式处理
│   │   └── metrics.go       # 请求统计采集
│   ├── server/              # Gin HTTP 服务
│   │   ├── auth/            # JWT 认证
│   │   ├── handlers/        # 各资源 handler（channel、group、apikey、log、setting）
│   │   ├── middleware/       # Auth/APIKeyAuth/CORS/Logger/Static/RequireJSON
│   │   ├── resp/            # 统一响应格式
│   │   └── router/          # 路由注册框架（GroupRouter + Route 声明式注册）
│   ├── task/                # 定时任务调度器（价格同步、模型同步、统计落库）
│   ├── update/              # 版本更新检查
│   └── utils/               # 工具包（cache/diff/log/shutdown/snowflake/xstrings）
├── web/                     # 前端（Next.js 16 静态导出）
│   ├── src/
│   │   ├── api/             # API 客户端（client.ts + endpoints/ 各资源）
│   │   ├── app/             # Next.js App Router 页面
│   │   ├── components/      # UI 组件（modules/ 按功能域分、ui/ 基础组件、animate-ui/）
│   │   ├── hooks/           # 自定义 React hooks
│   │   ├── lib/             # 工具库
│   │   ├── provider/        # React Provider
│   │   ├── route/           # 路由配置（lazy-with-preload 按需加载）
│   │   ├── stores/          # Zustand stores（setting.ts）
│   │   └── hooks/           # 自定义 hooks
│   └── public/locale/       # i18n 翻译文件（next-intl）
├── scripts/dockerfiles/     # Docker 构建文件（alpine/debian + entrypoint）
├── static/out/              # 前端构建产物（Go embed 嵌入）
├── .trellis/                # Trellis 工作流管理（spec/tasks/workspace）
└── .claude/                 # Claude Code 配置（skills/commands/hooks）
```

## Tech Stack

| Layer | Stack | Key Dependencies |
|-------|-------|------------------|
| Backend | Go 1.26+ | Gin、GORM（SQLite/MySQL/PG）、Cobra、Viper、zap、jwt、axonhub/llm |
| Frontend | Next.js 16 + React 19 | Zustand、TailwindCSS 4、next-intl、Radix UI、Recharts、React Compiler |
| Build | Go build + pnpm | 前端构建后 mv web/out static/，Go embed 打包进二进制 |
| Deploy | Docker（Alpine/Debian） | docker-compose.yml，端口 8080 |

## Key Commands

```bash
# 后端开发
go run main.go start                          # 启动后端（默认 SQLite，监听 0.0.0.0:8080）
go run main.go start --config path/to/config  # 指定配置文件

# 前端开发
cd web && pnpm install                        # 安装前端依赖
cd web && NEXT_PUBLIC_API_BASE_URL="http://127.0.0.1:8080" pnpm run dev  # 前端 dev server
cd web && pnpm run build && cd .. && mv web/out static/                  # 构建前端并移到 static/

# 生产构建（使用 scripts/build.sh 包装器）
./scripts/build.sh build linux x86_64         # 构建当前平台二进制（含前端+后端）
./scripts/build.sh release                    # 全量多平台构建 + 打包 + checksum
```

## Architecture Conventions

### 后端分层

```
handler → op（缓存+CRUD）→ model（GORM 模型）→ db（数据库）
                  ↘ relay（转发层）→ balancer + transformers → axonhub/llm pipeline
```

- `op/` 层封装 db 操作 + 内存缓存，handler 不直接调用 db
- 路由注册使用声明式 `router.NewGroupRouter().AddRoute()` + `init()` 自动注册
- relay 层通过 axonhub/llm pipeline 处理协议转换，入站（客户端格式→内部格式）和出站（内部格式→上游格式）分离
- 定时任务使用 `task.Register(name, interval, runOnStart, fn)` 注册，`task.RUN()` 启动

### 前端结构

- 状态管理：Zustand（`stores/setting.ts`）
- API 客户端：`api/client.ts`（统一 fetch wrapper，自动注入 JWT token）
- 路由：Next.js App Router + `route/config.tsx` 声明式 lazy load
- i18n：next-intl，翻译文件在 `public/locale/`
- 组件按功能域分模块：`components/modules/{channel,group,log,setting,...}/`

## Constraints

- 配置优先级：环境变量 `OCTOPUS_*` > config.json > 默认值（viper AutomaticEnv）
- 前端为静态导出（`output: "export"`），不支持 Next.js SSR/API routes
- axonhub/llm 使用 monorepo commit 伪版本 pin（无独立 `llm/vX.Y.Z` tag）；不要再依赖本地 `../axonhub/llm` replace
- 数据库迁移在 `internal/db/migrate/`，按编号递增（001、002、003...）
- shutdown 需使用 SIGTERM 或 Ctrl+C，不要 `kill -9`（会导致内存统计丢失）
- 默认登录 admin/admin，首次启动后应立即修改密码

## Development Notes

- 修改后端 API 后，前端 `api/endpoints/` 对应文件需同步更新类型
- 修改 GORM model 后需在 `db/migrate/` 新增迁移文件
- relay 的入站/出站适配器在 `transformers.go` 工厂函数中注册，新增协议格式需在此添加
- 添加新定时任务只需在 `task.Register()` 注册，`task.RUN()` 会自动调度
- 前端新增页面模块：在 `route/config.tsx` 添加路由配置 + `components/modules/` 新建目录
- 运行遇到复杂配置或数据库连接问题时，参见 `README_zh.md` 获取完整配置示例

### 远程开发环境配置

当在远程服务器上开发时，前端需要访问远程后端 API：

```bash
# 前端开发服务器指向远程后端
cd web
NEXT_PUBLIC_API_BASE_URL="http://<服务器IP>:8080" pnpm run dev
```

后端 CORS 配置（开发环境）：
```bash
# 通过 API 设置 CORS 允许所有来源（仅开发环境）
curl -X POST "http://127.0.0.1:8080/api/v1/setting/set" \
     -H "Authorization: Bearer $TOKEN" \
     -H "Content-Type: application/json" \
     -d '{"key":"cors_allow_origins","value":"*"}'
```

**生产环境** 必须设置具体域名：`"example.com,app.example.com"`

### 配置项删除检查清单

删除 Setting 配置项时必须检查：
1. `internal/model/setting.go`：常量定义、`DefaultSettings()`、`Validate()`
2. `web/src/api/endpoints/setting.ts`：`SettingKey` 对象
3. 前端组件：移除状态变量、handler 函数、UI 元素
4. 相关业务逻辑：确认没有其他地方引用该配置

### API 测试最佳实践

- 先确认路由的正确 HTTP 方法（查看 handler 文件或使用 `grep`）
- JSON 请求必须设置 `Content-Type: application/json`
- 测试前清理旧数据，避免结果污染：`curl -X DELETE .../log/clear`
- 验证边界值：`0`、负数、极大值等特殊情况

更多问题记录和解决方案参见：`.trellis/troubleshooting.md`