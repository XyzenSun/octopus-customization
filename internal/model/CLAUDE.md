# internal/model — GORM 数据模型

## 目录定位

定义所有持久化实体（GORM struct + JSON tag）和共用枚举/常量。**纯数据定义层**，不含业务方法。

## 文件索引

- `apikey.go` — API Key 实体（用户调用 octopus 的凭证）
- `channel.go` — 渠道实体（上游 LLM 供应商配置：endpoint、key 池、协议类型）
- `group.go` — 分组实体（渠道分组 + 模型映射 + 负载均衡策略）
- `user.go` — 用户实体（管理员账号）
- `log.go` — `RelayLog` 中继日志 + `ChannelAttempt` 单次尝试记录
- `setting.go` — 运行时 setting 表 + 全部 setting key 常量（`SettingKeyXxx`）
- `stats.go` — 统计指标（`StatsMetrics` 总/小时/日/通道/APIKey 多维度）
- `llm.go` — 模型信息（model id、定价、能力标签）
- `backup.go` — 备份/恢复用的导出结构体

## 关键约束

- **零业务逻辑**：本包不写 CRUD 函数（那是 `op` 层的事），只允许定义 struct、tag、枚举、常量。
- **JSON tag 即 API 契约**：字段的 `json:"xxx"` tag 直接作为前后端协议；改名前确认前端 `web/src/api/endpoints/` 同步更新。
- **GORM 序列化器**：复杂字段（如 `[]ChannelAttempt`）用 `gorm:"serializer:json"`，不要拆表。
- **Snowflake ID**：用 `int64` + `primaryKey;autoIncrement:false`，由调用方（`op` 层）调 `snowflake.GenerateID()` 赋值，**不**用 GORM 自增。
- **新增 setting key**：在 `setting.go` 加 `SettingKeyXxx` 常量，并在 `op/setting.go` 默认值表里登记。
