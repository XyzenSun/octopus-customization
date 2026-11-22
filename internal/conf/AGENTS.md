# internal/conf

本目录加载启动配置, 并提供版本信息, debug 开关和启动横幅. 配置结构与默认值集中在 config.go; const.go 只保存应用名称与简介.

新增启动配置时同步结构字段, config.go 的默认值和配置说明. 环境变量按配置路径映射, 如 server.port 对应 OCTOPUS_SERVER_PORT. 排查配置文件选择或环境变量映射时读取 internal/conf/config.go, 核对路径选择与绑定逻辑.

运行期间可修改并持久化的设置继续放在 setting 机制中. JWT 签名材料由用户凭证决定, 本包当前没有 JWT secret 配置.
