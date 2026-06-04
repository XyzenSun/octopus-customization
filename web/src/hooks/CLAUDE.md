# web/src/hooks — 自定义 React hooks

## 目录定位

复用型 React hooks。仅放跨模块用的；模块内单一用途的 hook 留在模块目录内。

## 文件索引

- `use-mobile.ts` — 检测视口是否在移动尺寸（用于布局切换）
- `useClickOutside.tsx` — 监听点击外部区域（弹层关闭等）

## 关键约束

- **命名**：以 `use` 开头；hook 文件命名与导出名一致（`use-mobile.ts` 导出 `useMobile`，`useClickOutside.tsx` 导出 `useClickOutside`）。
- **副作用清理**：所有 `useEffect` 添加事件监听必须在清理函数里 remove，避免组件卸载后内存泄漏。
- **跨模块才进本目录**：仅被一个模块使用的 hook 留在 `components/modules/<domain>/`，不要污染本目录。
