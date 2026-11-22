# web/src/provider

应用级 context 注入层, 包含主题, Query client 和语言 Provider. 主题与语言消息沿用各自现有同步方式.

新增或调整 Provider 时读 `web/src/app/layout.tsx`, 按实际消费关系确定嵌套, 在 layout 集中挂载而非在业务组件重复包裹. Provider 聚焦 context 及必要同步, 业务判断放相应 hook/store.

修改查询默认行为时读 `web/src/provider/query.tsx`, 保留 QueryClient 生命周期. 认证错误处理在 `web/src/api/client.ts` 与 `web/src/api/endpoints/user.ts`, 修改登录行为时读这两处, 不在 QueryProvider 另写一套处理.

修改语言支持时读 `web/src/provider/locale.tsx`, 同步静态翻译导入, Locale 类型和 BCP 47 映射, 区分内部语言 key 与语言标签.
