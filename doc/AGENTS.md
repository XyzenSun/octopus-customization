# doc 文档指南

本目录保存 HTTP 契约, 数据库说明, 前端资料及任务记录. 下文路径相对项目根目录, 命令从项目根目录执行.

```text
doc/
├── typespec/       # API 契约源码, 操作按业务模块拆分
├── openapi.yaml    # TypeSpec 生成的 OpenAPI 文档
├── ENTITY.dbml     # 数据库实体结构说明
├── tasks/          # 历史或正在进行的任务
├── deprecated/     # 已废弃的 API, DTO 和 VO 文档
└── FRONTEND.md     # 前端结构资料
```

## API 契约维护

`doc/typespec/` 下的 `.tsp` 文件是 HTTP 接口定义的唯一维护来源. 修改接口先定位 `doc/typespec/operations/<模块>.tsp`, 再按引用读取 `doc/typespec/management.tsp`, `doc/typespec/protocols.tsp` 或 `doc/typespec/responses.tsp`. `doc/typespec/main.tsp` 管理服务及鉴权方案, `doc/typespec/operations.tsp` 只聚合模块导入. 默认按模块阅读, 需要核对生成差异时再读取相关 OpenAPI 内容, 避免每次加载整套契约.

请求与响应结构, 必填性, null, 鉴权, 错误和媒体类型统一在 `.tsp` 维护. 优先复用原生类型和公共响应, `@extension` 只补充原生表达未覆盖的约束或生成元数据, 避免重复维护完整的 responses 或普通 parameters 覆盖. Markdown 记录背景与决策, 不另建字段或错误定义的维护来源. `doc/deprecated/` 内的旧版 API, DTO 和 VO 文档仅按需作为历史资料参考, 接口变更统一更新 `.tsp`.

核对实际行为时读取受影响路由, handler, model 和 op. 契约与实现不一致时先报告证据并确认处理方式, 不从数据库标签推断 HTTP 校验. 在获批范围内检查并生成:

```bash
npm --prefix doc/typespec run check
npm --prefix doc/typespec run generate
```

`check` 只检查 TypeSpec, `generate` 覆盖 `doc/openapi.yaml`. OpenAPI 只通过生成更新, 不手工修补 YAML. 生成后还需在获批范围内执行 OpenAPI 校验及受影响契约对比, 区分格式变化与业务变化. 需要安装依赖, 校验工具或 Scalar 审阅时读取 `doc/typespec/README.md`. 编译与文档校验不证明真实 API 成功; 真实请求, 凭证使用和对外开放预览另行确认. `doc/typespec/site/` 和 `doc/typespec/node_modules/` 是本地产物, 不纳入提交.

## 数据库说明

`doc/ENTITY.dbml` 集中维护数据库表, 字段, 索引和关联说明, 实际建表由 Go 模型及数据库迁移负责. 持久化结构变化时, 按受影响的 `internal/model/`, `internal/db/db.go` 和 `internal/db/migrate/` 核对并同步 DBML. 区分实际数据库关联, 业务引用和 JSON 值对象, 记录已有约束及驱动差异, 不把 Go 非指针字段直接当作数据库 NOT NULL. DBML 用于结构说明, 不作为直接执行的迁移脚本, 也不替代 HTTP 输入与输出契约.

## 任务与其他资料

`doc/tasks/` 保存历史或正在进行的任务, 按用户指定或与当前需求直接相关的文件选择性读取. 根据记录状态和用户确认区分已实施行为与计划, 状态不明时先确认; 历史方案仅作为背景, 不直接覆盖当前契约或实现.

维护任务记录时说明背景, 范围, 决策, 进展及已验证和未验证项. 涉及接口或数据库结构时引用对应 `.tsp` 或 `doc/ENTITY.dbml`, 避免在任务 Markdown 中长期维护另一套字段定义. 涉及前端结构资料时再读取 `doc/FRONTEND.md`.
