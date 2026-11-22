# Octopus HTTP 契约维护

`main.tsp` 是入口, `management.tsp` 维护管理数据模型, `protocols.tsp` 维护 LLM 协议模型, `responses.tsp` 复用 HTTP 响应包装, `operations.tsp` 定义操作. 这些 `.tsp` 文件是唯一维护来源, `../openapi.yaml` 仅由 OpenAPI emitter 生成. Markdown 只记录维护方法与迁移决策, 不另列接口字段.

本项目从现有 OpenAPI 3.1.0 等价迁移, 保留已有 `operationId`、模型标识、鉴权边界和实现限制. TypeSpec 可能把 `const` 表达为单值 `enum`, 把可空类型表达为 `anyOf`, 展开输入与输出模型的组合, 或内联公共响应. 这些生成结构的调整不代表 HTTP 行为变更. 新增的辅助 schema 用于承载联合分支的约束和说明.

常规字段、可选性、联合类型和 HTTP 操作由 TypeSpec 定义. `@extension` 保留 JSON Schema 跨字段条件、原始响应媒体类型、multipart encoding 与媒体级示例等生成元数据. 原接口只检查 Content-Type 且不解析 body 的情况保留无请求体定义. 修改模型或操作时同时检查相关生成元数据, 避免残留引用或约束偏差; 不直接修改 YAML 修补生成结果.

使用 Node.js >=22.12.0 与 npm >=10. TypeSpec compiler、http、openapi 和 openapi3 固定为 1.18.0, 依赖独立安装在本目录, 不影响 Go 或前端依赖. 从项目根目录执行:

```bash
npm --prefix doc/typespec ci --no-audit --no-fund
npm --prefix doc/typespec run check
npm --prefix doc/typespec run generate
```

`check` 只检查 TypeSpec, 不写 OpenAPI. `generate` 通过 `tspconfig.yaml` 生成并覆盖 `doc/openapi.yaml`. 仅修改 `.tsp` 后可运行 `npm --prefix doc/typespec run format` 统一格式.

在已安装 design-api skill 的环境中, 可执行完整的编译、OpenAPI 校验、合并与 Scalar 持久导出:

```bash
node /root/.pi/agent/skills/design-api/scripts/api.mjs all \
  doc/typespec --output doc/typespec/site --no-preview
```

`site/` 保存 Scalar 页面、本地资源、编译规范和合并规范, 已被 Git 忽略. 该命令不启动服务. 审阅时通过另行批准的静态 HTTP 服务访问完整目录, 不单独部署 `index.html`. OpenAPI 中的服务器地址仍是业务 API 示例地址, 文档页面不提供 API 代理.

迁移审阅需要同时核对路径与方法、鉴权、状态码、媒体类型、必填性、null、联合与条件约束, 以及说明和示例. 编译与 OpenAPI 校验不证明实现一致或真实请求成功. 真实 API 调试、使用凭证、写入数据和对外开放预览服务均需另行确认.
