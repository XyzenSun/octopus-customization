# internal/utils/log — 全局 zap logger

## 目录定位

封装 `go.uber.org/zap`，提供项目内统一的全局日志入口。**所有业务代码的日志输出都走这里**。

## 文件索引

- `log.go` — 包级 API：`Infof / Warnf / Errorf / Debugf` + `SetLevel(level)` 动态调级

## 关键约束

- **统一入口**：业务代码用 `import "github.com/bestruirui/octopus/internal/utils/log"` + `log.Infof("...")`；不要 `fmt.Printf`，不要新建 zap logger 实例。
- **动态级别**：通过 `atomicLevel` 实现，`SetLevel("debug"/"info"/"warn"/"error")` 即时生效；非法 level 字符串会被静默忽略，调用方应校验输入。
- **当前格式**：Console encoder 输出到 stdout，含 caller + level ≥ Error 时附 stacktrace。`AddCallerSkip(1)` 已设好，所以打印的 caller 是业务代码位置而非本包。
- **本包定位 ≠ RelayLog**：本包是 *运行时进程日志*（zap → stdout）；上游 LLM 转发的审计日志是 `internal/op/log.go` 的 `RelayLog`（落 db），二者不要混淆。
- **不要在本包加文件 sink**：项目当前只输出 stdout，由容器 / systemd 接管落盘。需要轮转/分文件时，先与项目维护者确认架构方向，再考虑是否引入 lumberjack 等。
