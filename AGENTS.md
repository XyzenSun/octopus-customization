# Octopus 项目指南

Octopus 是面向个人用户的 LLM API 聚合与负载均衡服务, 提供分组路由、指定渠道直连、协议转换和用量统计. 后端使用 Go、Gin、GORM、Cobra 和 Viper; 前端使用 Next.js 16、React 19、TanStack Query、Zustand 和 TailwindCSS 4, 已启用 React Compiler. 依赖版本以 `go.mod` 和 `web/package.json` 为准.

开始本项目工作时先读取根目录 `CONTEXT.md`, 按其中的专有词汇理解用户需求、代码命名和文档表述. 该文件记录术语及含义; 开发规则放在各层 `AGENTS.md`. 修改子目录前读取对应的 `AGENTS.md`, 以了解模块约束.

## 目录与分层

```text
octopus-customization/
├── main.go, cmd/       # CLI 入口, 启动、版本与关闭 2FA 命令
├── internal/
│   ├── conf/, db/      # 启动配置, SQLite/MySQL/PostgreSQL 初始化与迁移
│   ├── model/, op/     # 数据实体与校验, CRUD、缓存和聚合写入
│   ├── relay/          # 请求转发、协议适配、SSE、统计与 balancer
│   ├── server/         # Gin 服务, 管理 handler、路由、鉴权与响应
│   ├── client/, helper/ # HTTP 连接池、渠道辅助与模型同步相关操作
│   ├── price/, update/ # 模型价格, 版本检查与程序更新
│   └── task/, utils/   # 定时调度, 缓存、日志与优雅关闭等通用工具
├── web/                # 前端源码与 public/locale/*.json 翻译文件
├── static/static.go    # Go embed 入口, 嵌入 static/out 前端构建产物
├── scripts/            # 构建包装器、价格更新与 Docker 文件
├── doc/                # 接口/实体/前端资料
└── .github/            # CI 与发布流程
```

管理请求沿 `handler → op → db` 操作 `model` 实体; handler 通过 op 访问业务数据和缓存. 转发请求由 server 注册 `relay.Handler`, 经 relay、balancer 与 axonhub/llm pipeline 访问上游. 入站与出站协议适配器在 `internal/relay/transformers.go` 中分离管理. 管理路由采用声明式注册, 转发路由由 `internal/server/server.go` 维护.

前端服务端数据由 TanStack Query 管理, 客户端偏好及交互状态由 Zustand 等管理. App Router 承载页面入口, 业务导航通过状态切换面板; 新增业务面板在 `web/src/route/config.tsx` 注册, 实现在 `web/src/components/modules/`. API 调用集中在 `web/src/api/`, 常规 JSON 请求由 client 处理认证、响应解包和错误, 上传、下载与 SSE 使用对应 endpoint 的专用流程.

## 开发与构建

命令在项目根目录执行. Go 工具链要求以 `go.mod` 为准, 当前为 Go 1.27.4. 编译或运行后端前必须准备 `static/out`, 干净检出可能没有该目录. 包装器会安装前端依赖、构建并替换静态产物, 同时执行 `go mod tidy` 和价格更新; 使用前确认这些操作符合任务范围.

```bash
# 构建前后端及 Linux x86_64 二进制, 输出到 build/
bash scripts/build.sh build linux x86_64

# 已准备 static/out 后启动后端
go run main.go start
go run main.go start --config path/to/config.json

# 前端开发, 默认通过 Next.js rewrites 代理本机后端
(cd web && pnpm install && pnpm run dev)

# 前端直接访问指定后端, 地址必须可从用户浏览器访问
(cd web && NEXT_PUBLIC_API_BASE_URL="http://127.0.0.1:8080" pnpm run dev)

# 多平台发布构建与打包
bash scripts/build.sh release
```

生产前端采用静态导出, 由 Go 服务提供资源, 功能需兼容无 SSR 和 Next.js API routes 的部署方式. 开发阶段启用 rewrites, 无需静态导出. 手动构建时将 `web/out` 完整替换为 `static/out`, 保留 `static/static.go`, 避免重复移动造成目录嵌套.

启动配置优先级为 `OCTOPUS_*` 环境变量 > 配置文件 > 默认值, 默认读取 `data/config.json`, 使用 SQLite 并监听 `0.0.0.0:8080`. 运行期 Setting 由数据库和 op 缓存管理, 与启动配置分开. 浏览器直接跨域访问后端时配置 `cors_allow_origins`; 开发可临时使用 `*`, 生产填写具体 origin 或域名, 非默认端口需保留端口. 遇到部署、配置或数据库连接问题时读取 `README_zh.md` 获取配置示例, 并与当前源码核对.

## 变更约束

- 修改后端 API 或持久化字段时, 同步核对 `web/src/api/endpoints/` 的类型与调用方. 删除 Setting 时一并检查 model 中的常量、默认值和校验, 前端 SettingKey、组件状态、handler 及业务引用.
- 数据库初始化包含 AutoMigrate 与 before/after 显式迁移. 先判断字段变更能否由 AutoMigrate 处理; 旧数据转换或兼容性变更在 `internal/db/migrate/` 增加递增版本的迁移, 核对所有受支持驱动, 保持重复启动安全.
- axonhub/llm 当前使用 commit 伪版本 pin, 保持可独立拉取的依赖, 不引入本地目录 replace. 升级协议依赖时核对 `go.mod` 中的 SSE fork replace 与流式处理兼容性.
- 定时任务通过 task 注册, 服务启动后运行调度器; 聚合统计及缓存脏数据依赖关闭钩子落库. 停止进程使用 SIGTERM 或 Ctrl+C, 给回调留出执行时间.
- API 验证前从 handler 确认 HTTP 方法, JSON 请求设置 `Content-Type: application/json`, 使用隔离测试数据并覆盖必要边界值. 清空日志、删除数据或修改真实运行设置前单独确认操作范围.
- 初始账号为 `admin/admin`, 首次登录后修改密码. 鉴权、路由和请求选项变更需保留现有权限边界; 涉及指定渠道直连时读取 `internal/relay/relay.go` 和 `internal/relay/direct_channel.go`, 核对分组优先、单次尝试及受限 API Key 规则.
