# web/src/stores

共享客户端状态目录, 当前 `setting.ts` 只持久化语言偏好. 认证, 导航和工具栏等 store 也有各自现有归属, 主题由主题 Provider 管理.

新增 store 按一类职责命名和拆分, 沿用 `useXxxStore` 风格, 组件通过 selector 订阅所需切片. 需要持久化时使用 Zustand persist, 避免另写 localStorage 同步机制; 服务端列表和详情沿用现有 Query 层, 不复制进客户端 store.

修改语言偏好时一起读 `web/src/stores/setting.ts` 和 `web/src/provider/locale.tsx`, 核对持久化字段, 语言类型与消息映射.
