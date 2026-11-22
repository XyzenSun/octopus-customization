# web/src/route

业务面板的声明式配置与加载层, 包含模块映射, lazy/preload 辅助函数和内容加载器. 配置按面板 id 映射组件, 认证判断留在应用容器和认证 hook/store.

新增面板时读 `web/src/route/config.tsx` 和 `web/src/components/modules/navbar/nav-store.ts`, 同步模块配置, 导航类型和顺序, 使用现有 `lazyWithPreload` 接入模块, 保持 `app/` 入口薄.

调整预加载时读 `web/src/route/use-preload.ts` 与 `web/src/components/modules/navbar/navbar.tsx`, 延续按需和 hover 触发, 避免无差别加载全部面板. 调整 lazy 边界时读 `web/src/route/lazy-with-preload.ts` 和实际调用方, 核对异步加载处理.
