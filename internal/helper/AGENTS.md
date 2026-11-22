# internal/helper

本目录提供跨层辅助流程, 包含渠道 client 选择, 自定义请求头, 延迟探测, 模型拉取/探测, 自动分组与价格记录同步. 这些流程会访问网络及通过 op 修改缓存或数据库, 不按纯函数工具集理解.

新增辅助逻辑先按现有职责归类, 数据读写继续通过 op. 保持依赖方向: helper 可使用独立的 relay/balancer 子包, server, task 与 relay 父包继续作为调用方, 避免反向依赖成环.

新增按渠道选择 Key 的模型拉取调用时读取 internal/helper/fetch.go, 沿用调用方读取并传入 Key 级熔断开关的模式. 请求超时由调用方 context 提供, 当前模型拉取没有统一重试封装.

调整自定义请求头时读取 internal/helper/channel.go, 保持新增/覆盖, 显式空值和 nil 删除的语义, 让模型探测与实际转发共用同一处理逻辑.
