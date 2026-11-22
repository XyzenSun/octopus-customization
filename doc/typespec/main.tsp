import "@typespec/http";
import "@typespec/openapi";
import "@typespec/openapi3";
import "./management.tsp";
import "./protocols.tsp";
import "./responses.tsp";
import "./operations.tsp";

using TypeSpec.Http;
using TypeSpec.OpenAPI;

// 文档版本独立于运行程序版本, 保留已有契约的鉴权边界与实现限制.
@service(#{ title: "Octopus HTTP API" })
@info(#{ version: "1.0.0" })
@doc("doc/typespec/*.tsp 是 Octopus HTTP 接口的唯一维护契约, 本 OpenAPI 文件由 TypeSpec 生成, 替代已废弃的 doc/API.md. version 表示文档版本, 运行程序版本通过 /api/v1/update/now-version 查询.\n\n契约依据当前 Go 路由, handler, model, op 和 go.mod 固定的 axonhub/llm 实现编写, 覆盖 60 个显式业务接口. 管理接口通常使用 /api/v1 前缀, LLM 兼容接口使用 /v1 前缀. 静态资源, CORS, 框架重定向和未匹配路由不在本契约范围内.\n\n管理成功响应包含 code: 200 和 message: success. 有业务载荷时包含 data, resp.Success(c, nil) 时省略 data. 有类型的空切片可能序列化为 null 或 [], 应按各接口 schema 判断. 本地错误使用 code 和 message, code 等于 HTTP 状态码. 指定渠道直连的特定上游 HTTP 错误按入站协议转换, 分组路由最终失败使用 Octopus 错误包装.\n\n分组路由先精确匹配完整请求模型. 分组不存在时, 可按第一个 / 将请求模型解析为 channelName/modelName, 执行一次指定渠道直连. 非空 supported_models 限制的 API Key 不允许指定渠道直连. 协议直通是协议一致时的转发优化, 保留原始协议扩展, 仍遵循选路和鉴权. 分组请求选项先应用, 渠道请求选项随后覆盖; 指定渠道直连只应用渠道请求选项.\n\n本契约记录现有行为, 不承诺数据库标签对应 HTTP 校验, 也不承诺所有上游扩展字段或跨协议能力无损兼容. 请求 schema 保持未知属性开放; schema 的类型表达不覆盖 Go 对 null 的全部宽松解析行为. 注明的实现风险尚未修复. 已进行源码静态审查, 未调用真实 API, 未验证数据库驱动差异, 运行配置或上游能力. Scalar 预览和 OpenAPI 校验不构成运行时验证.")
@server(
  "http://127.0.0.1:8080",
  "默认本机 API 地址示例. 调试前确认实际部署地址, 凭证和授权; 文档预览端口不提供 API 代理."
)
@tagMetadata(#[
  #{ name: "User", description: "用户登录, 账号管理, 服务器时间与双重认证." },
  #{ name: "Channel", description: "渠道管理, 模型探测与同步." },
  #{ name: "Group", description: "分组路由与请求选项管理." },
  #{ name: "APIKey", description: "终端用户 API Key 与当前 Key 的统计." },
  #{ name: "Log", description: "转发日志查询, 清理和 SSE 推送." },
  #{ name: "Setting", description: "运行期设置与数据库表导入, 导出." },
  #{ name: "Circuit", description: "渠道级内存熔断状态与重置." },
  #{ name: "Model", description: "模型价格, 渠道模型视图和兼容模型列表." },
  #{ name: "Stats", description: "累计, 按日, 按小时与 API Key 统计." },
  #{ name: "Update", description: "程序版本检查和二进制自更新." },
  #{ name: "Relay", description: "LLM 分组路由, 指定渠道直连与入站协议转换." }
])
@useAuth(JWTAuth)
namespace Octopus;

@doc("Authorization: Bearer <jwt>. 当前实现也将裸 JWT 交给校验, 仅删除精确的 Bearer 前缀, 不做大小写或空白归一化. 缺少 Authorization 返回 400, 非空但校验失败返回 401. 修改用户名或密码会改变签名材料, 已签发 Token 随之失效.")
@extension("bearerFormat", "JWT")
@extension("scheme", "bearer")
model JWTAuth is BearerAuth;

@doc("Octopus API Key, 前缀为 sk-octopus-. 非空 x-api-key 优先于 Authorization, 失败不会回退. 在 /v1/models 中使用该头选择 Anthropic 列表, 其他 /v1 路由的入站协议由路径固定. 未知, 禁用, 过期或累计成本超过正 max_cost 的 Key 返回 401; 成本等于上限时仍放行.")
model APIKeyHeader is ApiKeyAuth<ApiKeyLocation.header, "x-api-key">;

@doc("Authorization: Bearer <api_key>. 当前实现也接受裸 Key, 仅删除精确的 Bearer 前缀. 非空 x-api-key 会覆盖此头. 在 /v1/models 中使用 Authorization 选择 OpenAI 列表. Key 无效, 禁用, 过期或累计成本超过正 max_cost 时返回 401.")
@extension("bearerFormat", "Octopus API Key")
@extension("scheme", "bearer")
model APIKeyBearer is BearerAuth;

@doc("通过 /api/v1/log/stream-token 申请的 64 位小写十六进制 Token. 保存在进程内存, 无 TTL, 不绑定用户, IP 或原 JWT 的过期时间. 连接校验后吊销, 校验与吊销分离, 并发下不保证原子一次性消费. 缺失或无效返回 401.")
model StreamToken is ApiKeyAuth<ApiKeyLocation.query, "token">;
