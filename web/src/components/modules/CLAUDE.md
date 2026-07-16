# web/src/components/modules — 业务模块

## 目录定位

按业务域组织的页面级组件，每个子目录对应一个管理面板的功能区。

## 文件索引（按域）

- `channel/` — 渠道（上游 LLM 供应商）：列表卡片、详情、创建、表单
- `group/` — 渠道分组（含模型映射、负载均衡策略编辑）
- `apikey-dashboard/` — API Key 仪表板
- `log/` — 中继日志列表 + 实时流（SSE）
- `setting/` — 设置页（账户、外观、备份、日志、模型同步、价格等多 tab）
- `home/` — 首页：总量、活跃度图表、排行
- `login/` — 登录页
- `model/` — 模型展示
- `navbar/` / `toolbar/` / `logo/` — 全局导航 / 工具栏 / Logo

## 关键约束

- **每个域目录自治**：模块内用 `index.tsx` 作为入口，按需拆 `Card.tsx / Form.tsx / Create.tsx / Editor.tsx / ItemList.tsx / utils.ts`；模块间不互相 import 内部文件。
- **跨模块共享走 common**：两个模块都需要的东西放到 `web/src/components/common/`，不要在某个模块里写然后让另一个模块跨域 import。
- **新 API 走 endpoints 集中管理**：模块若需要新调用，去 `@/api/endpoints/<resource>` 加函数，不要在模块内联 `fetch`/`client.post`。"不直接 fetch"由 `web/src/CLAUDE.md` 全局约束；这里强调的是"也不要在模块里写一份新的 client wrapper"。
- **日志模块特殊**：`log/` 的实时流要先 GET stream-token 再连 EventSource，详见 `web/src/api/endpoints/log.ts` 中的 stream 实现。
