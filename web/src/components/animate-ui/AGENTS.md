# web/src/components/animate-ui

动画能力分为 `components/` 成品组件和 `primitives/` 原语, 原语内部包含动画交互与视觉效果. 实现使用 Motion.

成品可以组合原语, 原语保持不依赖成品层; 可组合的底层效果归 primitives, 成品封装归 components, 业务逻辑仍归业务模块.

可复用动效参数优先复用 `web/src/lib/animations/fluid-transitions.ts`, 调整共享动效时再读该文件. 原语也有局部 transition 默认值, 修改时保留调用方覆盖参数的能力.

新增或调整动画时尊重 `prefers-reduced-motion`, 提供减少或关闭动画的可行呈现.
