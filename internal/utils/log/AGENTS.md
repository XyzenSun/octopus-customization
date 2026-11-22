# internal/utils/log

本目录封装全局 zap 进程日志. log.go 提供格式化输出与动态级别调整, 使用 Console encoder 输出到 stdout, 包含调用位置, Error 及以上附带堆栈.

业务进程日志复用本包入口, 保持调用位置与动态级别行为一致. 调整级别时读取 internal/utils/log/log.go, 核对非法级别字符串被忽略的处理, 调用方需要有效输入时先完成校验.

stdout 的落盘由容器或 systemd 等运行环境承担. 需要文件 sink 或轮转时先与维护者确认方向; LLM 审计日志继续由 op 的 RelayLog 路径管理.
