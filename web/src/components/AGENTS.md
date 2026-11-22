# web/src/components

组件按基础, 通用和业务职责分层: `ui/` 提供基础 UI, `common/` 提供跨域复合能力, `modules/` 放业务面板与全局导航工具, `animate-ui/` 放动画组件和原语. 本层另有应用容器和 Service Worker 注册入口.

业务模块可以组合 common, ui 和动画能力, common 可以组合基础组件, 基础 ui 保持与业务模块解耦. 新增组件先查现有基础能力; 业务专用实现归所属模块, 跨域通用能力归 common.

修改基础组件生成配置时读 `web/components.json`, 保持现有风格和路径. 调整跨模块复用时读 `web/src/components/modules/AGENTS.md`, 区分一般依赖边界与现有导航, 工具栏的复用例外.
