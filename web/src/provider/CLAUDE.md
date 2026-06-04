# web/src/provider — React Provider

## 目录定位

应用级 Provider 集合，由 `app/layout.tsx` 一次性包裹整树。

## 文件索引

- `locale.tsx` — `next-intl` 国际化 Provider，加载 `public/locale/<lang>.json`
- `theme.tsx` — 主题（亮/暗/跟随系统），写到 `<html>` class
- `query.tsx` — react-query 客户端 + DevTools；统一处理 401 自动登出

## 关键约束

- **嵌套顺序**：locale → theme → query → 业务（具体由 `layout.tsx` 决定，新增 Provider 时复议位置）。
- **不在 Provider 内做副作用判断**：判断逻辑写到对应 store/hook；Provider 只负责注入 context。
- **新增 Provider 同时改 layout.tsx**：不要让 Provider 在某个子组件内单独挂载，避免树外组件用不到。
