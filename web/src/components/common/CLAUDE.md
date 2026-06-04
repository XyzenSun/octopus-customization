# web/src/components/common — 业务通用组件

## 目录定位

跨业务模块复用、但又含业务语义的复合组件（介于纯 `ui/` 原子和 `modules/` 业务模块之间）。

## 文件索引

- `AnimatedNumber.tsx` — 数字滚动动画（统计页常用）
- `CopyButton.tsx` — 复制到剪贴板按钮
- `PageWrapper.tsx` — 页面壳（标题、面包屑、内容区）
- `Toast.tsx` — 全局 toast（基于 `sonner`）
- `VirtualizedGrid.tsx` — 虚拟化网格（卡片大列表用，避免全量渲染）

## 关键约束

- **可被任何 module 使用**：本目录组件不依赖具体业务域；如果发现某个组件只被一个 module 使用，应迁回那个 module。
- **不持有全局状态**：组件内部状态可以有，全局共享状态走 `stores/`。
- **大列表性能**：渲染量大于约 50 条卡片的场景统一走 `VirtualizedGrid`，不要各模块各自实现虚拟化。
