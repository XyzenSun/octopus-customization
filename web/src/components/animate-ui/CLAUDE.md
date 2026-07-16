# web/src/components/animate-ui — 动画基元

## 目录定位

动画相关的 UI 组件与原语。从 `components/`（成品组件）和 `primitives/`（更底层的 effect）两层组织。

## 文件索引

- `components/animate/` — 成品动画组件（开箱即用）
- `primitives/animate/` — 动画原语（更底层，组合用）
- `primitives/effects/` — 视觉效果原语

## 关键约束

- **底层用 framer-motion**：通过 `lib/animations/` 共享配置（duration/easing），不要在组件内硬编码动画时长。
- **可访问性**：尊重用户的 `prefers-reduced-motion` 偏好，动画组件应支持降级到无动画版本。
- **依赖方向**：`components/` 可以用 `primitives/`；`primitives/` 不依赖 `components/`。新增带状态/业务的动画组件归 `components/`，纯效果归 `primitives/`。
