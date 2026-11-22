# web/src/lib

共享工具与基础设施, 包括格式化/类名工具, 日志, 应用信息, 模型图标, Service Worker 常量, 动画参数和严格 React context 辅助工具. 纯工具与 React 辅助能力沿用现有归属.

工具保持不依赖业务 components, api 或 stores; 业务私有实现留在模块, 复用型 hook 放 `hooks/`.

修改类名合并或格式化行为时读 `web/src/lib/utils.ts`, 复用现有 `cn` 等能力. 修改 Provider/context 配对时读 `web/src/lib/get-strict-context.tsx`, 保留缺少 Provider 时的明确错误反馈.

调整版本或仓库链接时读 `web/src/lib/info.ts`, 核对实际环境变量和默认值. 共享动画配置使用 Motion, 改动前读 `web/src/lib/animations/fluid-transitions.ts` 核对现有参数.
