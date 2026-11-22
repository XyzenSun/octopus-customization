# internal/model

本目录定义持久化实体, 请求/响应结构, 公共枚举和设置默认值, 也包含实体相关的选择, 校验与密码处理方法. 数据库 CRUD 与缓存管理由 op 承担.

字段的 JSON 标签与 GORM 标签分别影响接口和存储契约, 修改时核对调用方与已有数据. 日志尝试记录等复杂字段沿用现有 JSON 序列化方式. RelayLog 使用调用方生成的 int64 ID, 其他实体的主键策略按各自声明保留.

新增或调整 setting 时读取 internal/model/setting.go 和 internal/op/setting.go, 同步常量, 默认值, 校验与缺失设置的补齐逻辑. 调整渠道 Key 选择时读取 internal/model/channel.go, 保持调用方传入 Key 级熔断开关的边界, 避免 model 反向依赖 op.
