# internal/server/handlers

本目录包含管理资源, 登录与两步验证, 渠道探测, 日志流和备份导入导出的 HTTP handler. 资源端点通过各文件的 init 声明, 辅助解析与校验代码也放在本包.

Handler 负责解析, 校验与编排, 可复用的业务处理交给 op 或相应 helper. 管理接口通过路由中间件选择鉴权, 正常 JSON 响应沿用 resp; 导出文件和 SSE 保留各自的响应形式.

新增日志类 SSE 端点时读取 internal/server/handlers/log.go, 沿用先鉴权获取 token, 再以 query token 建立流并撤销 token 的模式. 修改日志查询时同时核对该文件的分页兜底和可选时间范围处理.

调整两步验证设置时读取 internal/server/handlers/user.go 和 internal/server/handlers/setting.go, 保持专用启用/关闭入口的验证码校验, 以及通用 setting 写入入口的限制.
