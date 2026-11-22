# internal/task

本目录注册并调度价格更新, 模型同步, 渠道延迟探测和统计/日志保存任务. task.go 管理执行与周期更新, init.go 集中注册, sync.go 与 channel.go 承载同步/探测流程.

新增周期任务沿用集中注册入口, 可配置周期使用现有 setting 机制, 固定行为保留明确命名的任务. 修改周期更新时读取 internal/task/task.go 和 internal/task/init.go, 核对任务注册, 移除与更新通道行为.

任务函数自行处理错误, 修改网络操作时核对 context 的期限与取消来源. 调度器当前直接异步执行函数, 没有统一 panic recover 或 shutdown context; 修改容错或退出行为时以调用链为准, 避免把未实现的保护当作保证. 内存数据的最终落库由启动层登记的保存钩子承担.
