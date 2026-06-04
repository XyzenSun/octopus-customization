# web/src/stores — Zustand 全局状态

## 目录定位

应用级共享状态。当前只有 setting，存放运行时偏好（主题、语言、用户简档等）。

## 文件索引

- `setting.ts` — Setting store（含持久化到 localStorage）

## 关键约束

- **粒度**：每个 store 围绕一类状态；不要做"超大单例 store"。新增请按域命名（如 `auth.ts`、`channel.ts`）。
- **API**：导出 `useXxxStore` hook + 必要的 selector；组件用 selector 订阅切片，避免不相关字段变更触发重渲染。
- **持久化**：用 zustand `persist` middleware；不要手动写 localStorage 同步逻辑。
- **服务端数据不进 store**：列表/详情等 server state 走 react-query（`provider/query.tsx`），store 只放 client-only 偏好与会话态。
