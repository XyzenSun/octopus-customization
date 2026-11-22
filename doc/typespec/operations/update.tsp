using TypeSpec.Http;
using TypeSpec.OpenAPI;

namespace Octopus;

@route("/api/v1/update")
@get
@operationId("getLatestUpdateInfo")
@tag("Update")
@summary("获取 GitHub 最新发布信息")
@doc("访问固定仓库 XyzenSun/octopus-customization 的 latest release API, 先直连, 请求或读取错误时再尝试 Setting 代理, 每次 background context 超时 30 秒. 不检查上游 HTTP 状态码. JSON 解析失败或上游 message 非空返回 500, 这些失败不会再次触发代理 fallback, 也不回退当前版本. 不要求 tag_name 或 published_at 非空, 上游 {} 或 null 可形成全空字符串的成功对象. 不缓存或比较版本. Source: internal/server/handlers/update.go:31-37; internal/update/update.go:21-93.")
op getLatestUpdateInfo():
  | HttpResponse<
      200,
      Success<LatestInfo>,
      "LatestInfo, message 字段成功时也存在且为空字符串."
    >
  | MissingAuth400
  | Unauthorized401
  | Internal500;

@route("/api/v1/update")
@post
@operationId("updateApplication")
@tag("Update")
@summary("下载并替换程序后尝试重启")
@doc("不读取请求体, 不要求 JSON Content-Type. 根据当前平台同步下载固定 latest ZIP, 解压到当前可执行文件目录, 然后异步 shutdown 和 restart. 不先比较版本, 不验证包中目标二进制, 不做摘要或签名校验, 多文件替换不是原子操作. 有效空 ZIP 也可能进入重启流程. 平台不支持, 下载或解压等错误返回 500, 中途解压失败可能已覆盖部分文件. 成功只表示该路径的下载和解压步骤完成, 不保证新版本启动成功. restart 错误仅写日志. 重启 goroutine 可在 handler 写响应前关闭 HTTP 服务, 因而 200 响应本身也不保证送达. Source: internal/server/handlers/update.go:44-50; internal/update/core.go:15-101; internal/update/update.go:96-158; cmd/start.go:48; internal/server/server.go:53-54.")
op updateApplication():
  | HttpResponse<
      200,
      Success<"update success">,
      "更新步骤成功, 不保证响应送达或重启成功."
    >
  | MissingAuth400
  | Unauthorized401
  | Internal500;

@route("/api/v1/update/now-version")
@get
@operationId("getCurrentVersion")
@tag("Update")
@summary("获取当前运行版本字符串")
@doc("返回 conf.Version, 默认值为 dev, 构建可注入其他字符串, 不保证 semver 格式. Source: internal/server/handlers/update.go:40-41; internal/conf/version.go:3-5.")
op getCurrentVersion():
  | HttpResponse<200, Success<string>, "当前版本字符串.">
  | MissingAuth400
  | Unauthorized401
  | Internal500;
