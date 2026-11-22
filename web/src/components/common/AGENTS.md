# web/src/components/common

跨业务域复用的复合组件和辅助能力, 包括复制, 通知, 内容动画, 表单编辑/校验及虚拟化网格. PageWrapper 负责子元素动画, 页面标题等业务内容由调用方提供.

通用能力通过 props 暴露, 不依赖具体业务模块; 仅服务单个模块的实现留在该模块. 组件可持有局部交互状态, 需要共享状态时复用已有 store, 而不是在通用组件内另建全局状态.

大卡片列表优先复用 VirtualizedGrid. 修改虚拟化布局或滚动加载时读 `web/src/components/common/VirtualizedGrid.tsx`, 保留稳定 item key, 尺寸测量和分页触发方式, 按实际布局与数据量判断是否需要虚拟化.
