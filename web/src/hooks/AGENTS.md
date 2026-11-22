# web/src/hooks

跨模块复用的 React hooks, 目前包含移动视口判断和点击外部区域处理. 模块单一用途的 hook 留在所属模块, 本目录只承载共享能力.

hook 命名以 `use` 开头, 新增时沿用邻近文件风格并核对实际导出, 不依据文件名猜函数名. 修改移动布局调用时读 `web/src/hooks/use-mobile.ts`, 使用现有 `useIsMobile` 导出并核对断点.

事件监听等副作用在 effect 清理函数中成对移除. 修改外部点击行为时读 `web/src/hooks/useClickOutside.tsx`, 保留捕获阶段, 忽略条件和弹层判断, 清理时使用与注册一致的事件参数.
