# internal/relay

本目录实现 LLM 转发热路径. 主流程协调选路, 上游请求与流式回写, balancer 管理候选与熔断, metrics 汇总用量和审计; 另有指定渠道直连与协议直通实现.

修改渠道选择或重试时读取 internal/relay/relay.go, 核对跳过, 真实尝试, 已写回响应和客户端断开的边界. 通道成功/失败与等待时间按真实 attempt 记录, 最终用量与成本归到实际通道, 保持统计不重复计数.

修改流式收尾或审计时读取 internal/relay/metrics.go, 保留脱离请求取消信号的保存路径. 日志正文过滤 RawRequest 与图片二进制, 新增大字段时补齐过滤并核对正文大小限制. 日志继续通过 op 保存, 达到阈值时可能同步写库.

调整指定渠道或直通行为时分别读取 internal/relay/direct_channel.go 或 internal/relay/passthrough.go, 核对访问限制与启用条件, 继续复用既有 pipeline 的请求执行和流生命周期.
