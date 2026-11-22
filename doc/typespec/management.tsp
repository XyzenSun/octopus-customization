using TypeSpec.Http;
using TypeSpec.OpenAPI;

namespace Octopus;

@doc("Octopus 本地错误响应, 不包含 data. 框架或上游协议错误需按对应接口的其他 schema 判断.")
@extension("examples", #[#{ code: 400, message: "Invalid request parameters" }])
@extension("additionalProperties", false)
model ErrorResponse {
  @doc("与 HTTP 状态码一致.")
  code: integer;

  @doc("错误说明.")
  message: string;
}

@doc("管理成功响应的公共字段. 业务载荷通过各接口的 data schema 定义.")
model SuccessBase {
  @extension("type", "integer")
  code: 200;

  message: "success";
}

@doc("无业务载荷的管理成功响应, data 字段省略.")
@extension("examples", #[#{ code: 200, message: "success" }])
@extension("additionalProperties", false)
model EmptySuccess is SuccessBase;

@doc("管理员登录输入. 所有字段无 binding required, 不添加 schema required. Go 标量对 JSON null 可能宽松解析为或保留零值, 这里只声明规范标量类型. 源码: internal/model/user.go:16-22.")
model UserLogin {
  @doc("用户名, 与当前用户缓存精确比较, 无长度或非空 binding 校验.")
  username?: string;

  @doc("明文密码, 使用 bcrypt 比较, 无输入长度或非空 binding 校验.")
  password?: string;

  @doc("JWT 有效分钟数, 不是小时. 省略或 0 使用 15 分钟, 正数使用对应分钟数, -1 使用 30 天. 小于 -1 未设置 ExpiresAt, 存在后续 nil 解引用并返回 500 的路径. 极大正数与 time.Minute 相乘可能溢出, 签发成功时过期时间也可能落在过去. 无 schema 范围约束.")
  expire?: integer;

  @doc("TOTP 验证码. 未启用两步验证时忽略, 启用时由 op 手动校验 6 位 SHA1 和 30 秒周期, 前后各一个周期容差. 不用 schema pattern 伪造 binding 校验.")
  code?: string;
}

@doc("登录成功载荷. 两个字段都没有 omitempty. 源码: internal/model/user.go:33-36.")
model UserLoginResponse {
  @doc("签发的管理员 JWT.")
  token: string;

  @doc("以 RFC3339 格式化的过期时间.")
  @format("date-time")
  expire_at: string;
}

@doc("修改密码输入, 无 binding required, 没有新密码非空, 长度或复杂度手动规则. 属性省略得到 Go 零值, 标量 JSON null 可能宽松解析. 源码: internal/model/user.go:24-27.")
model UserChangePassword {
  @doc("旧明文密码, op 校验不匹配会由 handler 返回 500.")
  old_password?: string;

  @doc("新明文密码, op 对其执行 bcrypt 哈希. 不添加未经实现的密码规则.")
  new_password?: string;
}

@doc("修改用户名输入, 无 binding required. 源码: internal/model/user.go:29-31.")
model UserChangeUsername {
  @doc("新用户名. op 仅显式拒绝与旧值相同, 无非空, 长度或字符集检查. 省略或标量 null 可能留下空字符串.")
  new_username?: string;
}

@doc("登录页使用的服务器时间信息. 所有属性都没有 omitempty. server_time 不是 RFC3339, 不标注 date-time format. 源码: internal/model/user.go:53-57.")
model UserTime {
  @doc("固定 UTC+8 展示的服务器时间, 格式 YYYY-MM-DD HH:mm:ss.")
  @extension("example", "2026-07-17 12:34:56")
  server_time: string;

  @doc("固定展示时区名.")
  timezone: "UTC+8";

  @doc("当前两步验证开关. 设置读取失败时返回 false.")
  two_factor_enabled: boolean;
}

@doc("已生成并保存的两步验证绑定信息, 不代表开关已经启用. 三个字段均无 omitempty. 源码: internal/model/user.go:40-44.")
model TwoFactorSetup {
  @doc("新生成的 TOTP secret, 属于敏感绑定信息.")
  secret: string;

  @doc("用于认证器绑定的 otpauth URI.")
  uri: string;

  @doc("二维码 PNG data URL, 前缀为 data:image/png;base64,.")
  qr_code: string;
}

@doc("启用或关闭两步验证的输入. code 无 binding required, 是否有效由 op 判断. 不添加未执行的长度或字符集 schema 约束. 源码: internal/model/user.go:47-49.")
model TwoFactorCodeRequest {
  @doc("当前 TOTP 验证码. 省略或标量 null 可能保留空字符串, 后续校验会失败.")
  code?: string;
}

@doc("渠道基础 URL 输入, 属性无 binding required, 不把 URL 语义或 delay 范围转为未执行的 schema 限制. 源码: internal/model/channel.go:39-42.")
model BaseUrlInput {
  @doc("基础 URL 文本. CRUD 不做 URL 格式校验, GetBaseUrl 选择非空地址.")
  url?: string;

  @doc("用于地址选择的延迟值, 单位毫秒. 选择非空 URL 中 delay 最低者, CRUD 未检查非负范围.")
  delay?: integer;
}

@doc("响应中的基础 URL, url 和 delay 均没有 omitempty.")
model BaseUrl {
  ...OmitProperties<BaseUrlInput, "url" | "delay">;

  @doc("基础 URL 文本. CRUD 不做 URL 格式校验, GetBaseUrl 选择非空地址.")
  url: string;

  @doc("用于地址选择的延迟值, 单位毫秒. 选择非空 URL 中 delay 最低者, CRUD 未检查非负范围.")
  delay: integer;
}

@doc("管理接口自定义 Header 输入. 独立于共享响应 CustomHeader 的 required, 属性都可省略. header_value 为 *string, 省略或 null 都是 nil, ApplyCustomHeaders 删除该 Header 的全部值; 空字符串保留显式空值. header_key 经 trim 为空时忽略. 源码: internal/model/channel.go:44-47, internal/helper/channel.go:30-47.")
model ManagementCustomHeaderInput {
  @doc("Header 名称, 应用时 trim, 空名称忽略.")
  header_key?: string;

  @doc("非 null 使用 Set 新增或覆盖. null 或省略删除 Header, 空字符串不是删除.")
  header_value?: string | null;
}

@doc("直接绑定 Channel 实体时可解析的 stats 对象. 输入属性无 binding required, 不复用带响应 required 的共享 StatsChannel. 请求提供此关联对象没有被 handler 剔除, 实际关联写入效果未通过数据库验证. 源码: internal/model/channel.go:35, internal/model/stats.go:3-11,36-39.")
model ManagementStatsChannelInput {
  @doc("统计关联渠道 ID.")
  channel_id?: integer;

  @doc("累计输入 token 数.")
  input_token?: int64;

  @doc("累计输出 token 数.")
  output_token?: int64;

  @doc("累计输入费用.")
  input_cost?: float64;

  @doc("累计输出费用.")
  output_cost?: float64;

  @doc("累计等待时间, 单位毫秒.")
  wait_time?: int64;

  @doc("累计成功请求数.")
  request_success?: int64;

  @doc("累计失败请求数.")
  request_failed?: int64;
}

@doc("Channel 实体 keys 中的密钥输入. 全部属性无 binding required, 没有 channel_key 非空校验. 运行状态字段也没有被请求 DTO 删除. GORM enabled default:true 不是 schema 默认值, 创建的最终值未通过数据库验证. 源码: internal/model/channel.go:49-58.")
model ChannelKeyInput {
  @doc("密钥主键, 实体绑定未排除客户端提供的 ID.")
  id?: integer;

  @doc("所属渠道 ID, 实际关联赋值和写入效果由 GORM 决定.")
  channel_id?: integer;

  @doc("密钥启用状态. 输入省略得到 Go 零值, 不保证 Create 后仍为 false.")
  enabled?: boolean;

  @doc("上游明文密钥, 此实体没有 required 或非空手动校验.")
  channel_key?: string;

  @doc("最近请求状态码, 实体请求可以绑定.")
  status_code?: integer;

  @doc("最近使用的 Unix 秒时间戳.")
  last_use_time_stamp?: int64;

  @doc("累计费用.")
  total_cost?: float64;

  @doc("密钥备注.")
  remark?: string;
}

@doc("渠道响应中的完整密钥对象. 所有字段均无 omitempty, channel_key 不脱敏. 其运行字段的快照新鲜度取决于缓存读取路径, 不承诺 list 已合并所有实时 Key 状态.")
model ChannelKey {
  ...OmitProperties<
    ChannelKeyInput,
    | "id"
    | "channel_id"
    | "enabled"
    | "channel_key"
    | "status_code"
    | "last_use_time_stamp"
    | "total_cost"
    | "remark"
  >;

  @doc("密钥主键, 实体绑定未排除客户端提供的 ID.")
  id: integer;

  @doc("所属渠道 ID, 实际关联赋值和写入效果由 GORM 决定.")
  channel_id: integer;

  @doc("密钥启用状态. 输入省略得到 Go 零值, 不保证 Create 后仍为 false.")
  enabled: boolean;

  @doc("上游明文密钥, 此实体没有 required 或非空手动校验.")
  channel_key: string;

  @doc("最近请求状态码, 实体请求可以绑定.")
  status_code: integer;

  @doc("最近使用的 Unix 秒时间戳.")
  last_use_time_stamp: int64;

  @doc("累计费用.")
  total_cost: float64;

  @doc("密钥备注.")
  remark: string;
}

@doc("直接绑定 model.Channel 的请求实体, 用于 create, fetch-model 和临时 test-model. 所有属性在解析层可省略, 不包含响应 required. Go 标量 JSON null 的宽松解析仅通过文字说明, 指针和切片显式允许 null. 不将 GORM unique, not null 或 default 转成输入限制. 不同操作对配置的实际使用和校验不同. 源码: internal/model/channel.go:20-37.")
model ChannelInput {
  @doc("渠道主键, 实体绑定允许客户端提供, 没有正数手动校验.")
  id?: integer;

  @doc("渠道名称. GORM 有 unique 和 not null 标签, HTTP CRUD 未做非空校验.")
  name?: string;

  @doc("llm.APIFormat 的字符串值. 当前项目适配器涉及 openai/chat_completions, openai/responses, openai/embeddings, anthropic/messages, gemini/contents, doubao, openai/image_generation, openai/image_edit 和 openai/image_variation. 这些是实现支持值说明, CRUD 无枚举校验, 不使用 enum. 不同请求类型与探测接口支持范围不同, 不能把依赖全部常量当成有效渠道.")
  type?: string;

  @doc("渠道启用状态. GORM default:true 不是已证实的 HTTP 默认值.")
  enabled?: boolean;

  @doc("基础地址切片, nil 可为 null. CRUD 不要求至少一个地址.")
  base_urls?: BaseUrlInput[] | null;

  @doc("渠道密钥切片, nil 可为 null. create 直接绑定实体, 没有逐项 required, 临时 test-model 按 key_index 使用.")
  keys?: ChannelKeyInput[] | null;

  @doc("渠道原始模型名称的逗号分隔文本, CRUD 不校验模型集合.")
  `model`?: string;

  @doc("渠道自定义模型名称的逗号分隔文本.")
  custom_model?: string;

  @doc("是否使用代理. false 时忽略 channel_proxy 并使用无代理 client, true 时选择渠道代理或全局代理.")
  proxy?: boolean;

  @doc("是否参与模型同步任务. SyncModelsTask 仅按此开关筛选, 不同时检查 enabled.")
  auto_sync?: boolean;

  @doc("AutoGroupType: 0 不自动分组, 1 模糊匹配, 2 精确匹配, 3 正则匹配. CRUD 无范围校验, 不使用 enum. 未知值在辅助 switch 中没有匹配处理.")
  auto_group?: integer;

  @doc("自定义请求头规则切片, nil 为 null. 输入 Header 值允许 null 删除语义.")
  custom_header?: ManagementCustomHeaderInput[] | null;

  @doc("参数覆盖 JSON 字符串, 不是直接的 JSON object. create/update 校验 nil 或空白直接通过, 其余需能反序列化为 map, 字符串内容 null 也通过. fetch-model 和 test-model 不执行此校验, test-model 不应用此配置.")
  param_override?: string | null;

  @doc("渠道代理地址. proxy=false 时忽略, proxy=true 且此值 nil 或空白时使用全局代理. HTTP client 支持的 scheme 和实际可用性不是 CRUD 校验约束.")
  channel_proxy?: string | null;

  @doc("请求可绑定的统计关联指针, nil 为 null. 管理返回会主动替换为 StatsChannelGet 的统计.")
  stats?: ManagementStatsChannelInput | null;

  @doc("模型探测结果过滤正则. Channel create/update 不编译校验; fetch helper 对非空值使用 regexp2 ECMAScript 模式编译并匹配, 失败返回 500.")
  match_regex?: string | null;
}

@doc("通用渠道输出实体. 除 stats 外所有字段无 omitempty, nil 切片与可空指针保留 null. stats 可选, 非 nil 时输出统计对象, nil 时省略. 数据库导出不 Preload 关联, 管理 list/create/update 通过 Channel schema 额外保证 stats 存在. keys 明文输出.")
model ChannelEntity {
  ...OmitProperties<
    ChannelInput,
    | "id"
    | "name"
    | "type"
    | "enabled"
    | "base_urls"
    | "keys"
    | "model"
    | "custom_model"
    | "proxy"
    | "auto_sync"
    | "auto_group"
    | "custom_header"
    | "param_override"
    | "channel_proxy"
    | "stats"
    | "match_regex"
  >;

  @doc("渠道主键, 实体绑定允许客户端提供, 没有正数手动校验.")
  id: integer;

  @doc("渠道名称. GORM 有 unique 和 not null 标签, HTTP CRUD 未做非空校验.")
  name: string;

  @doc("llm.APIFormat 的字符串值. 当前项目适配器涉及 openai/chat_completions, openai/responses, openai/embeddings, anthropic/messages, gemini/contents, doubao, openai/image_generation, openai/image_edit 和 openai/image_variation. 这些是实现支持值说明, CRUD 无枚举校验, 不使用 enum. 不同请求类型与探测接口支持范围不同, 不能把依赖全部常量当成有效渠道.")
  type: string;

  @doc("渠道启用状态. GORM default:true 不是已证实的 HTTP 默认值.")
  enabled: boolean;

  @doc("完整基础 URL 响应切片, nil 时为 null.")
  base_urls: BaseUrl[] | null;

  @doc("完整未脱敏密钥响应切片, nil 时为 null.")
  keys: ChannelKey[] | null;

  @doc("渠道原始模型名称的逗号分隔文本, CRUD 不校验模型集合.")
  `model`: string;

  @doc("渠道自定义模型名称的逗号分隔文本.")
  custom_model: string;

  @doc("是否使用代理. false 时忽略 channel_proxy 并使用无代理 client, true 时选择渠道代理或全局代理.")
  proxy: boolean;

  @doc("是否参与模型同步任务. SyncModelsTask 仅按此开关筛选, 不同时检查 enabled.")
  auto_sync: boolean;

  @doc("AutoGroupType: 0 不自动分组, 1 模糊匹配, 2 精确匹配, 3 正则匹配. CRUD 无范围校验, 不使用 enum. 未知值在辅助 switch 中没有匹配处理.")
  auto_group: integer;

  @doc("完整自定义 Header 响应切片, nil 时为 null.")
  custom_header: CustomHeader[] | null;

  @doc("参数覆盖 JSON 字符串, 不是直接的 JSON object. create/update 校验 nil 或空白直接通过, 其余需能反序列化为 map, 字符串内容 null 也通过. fetch-model 和 test-model 不执行此校验, test-model 不应用此配置.")
  param_override: string | null;

  @doc("渠道代理地址. proxy=false 时忽略, proxy=true 且此值 nil 或空白时使用全局代理. HTTP client 支持的 scheme 和实际可用性不是 CRUD 校验约束.")
  channel_proxy: string | null;

  @doc("可选的非 null 统计关系, nil 时省略. 管理 Channel 响应通过额外 required 保证此属性存在.")
  stats?: StatsChannel;

  @doc("模型探测结果过滤正则. Channel create/update 不编译校验; fetch helper 对非空值使用 regexp2 ECMAScript 模式编译并匹配, 失败返回 500.")
  match_regex: string | null;
}

@doc("管理 list/create/update 返回的渠道, handler 在通用实体基础上主动填充非 nil stats.")
model Channel {
  ...OmitProperties<ChannelEntity, "stats">;

  @doc("可选的非 null 统计关系, nil 时省略. 管理 Channel 响应通过额外 required 保证此属性存在.")
  stats: StatsChannel;
}

@doc("渠道增量 DTO. id binding required 为非零要求, 非正数也能通过此标签. 指针属性省略或 null 均不更新, 非 null 零值可显式写入. keys 操作切片无 binding dive, 不将其元素标签当成已经确认执行的 required. 源码: internal/model/channel.go:61-92.")
model ChannelUpdateRequest {
  @doc("目标渠道 ID, 顶层 binding required 排除 0, 不要求正数. 不存在由 op 返回 500.")
  @extension("not", #{ `const`: 0 })
  id: integer;

  @doc("名称 patch, 省略/null 不更新, 空字符串可提交, 无非空手动校验.")
  name?: string | null;

  @doc("协议 patch, 支持值说明见 ChannelInput.type, 无 enum 校验. 省略/null 不更新.")
  type?: string | null;

  @doc("启用状态 patch, false 可显式写入, 省略/null 不更新.")
  enabled?: boolean | null;

  @doc("完整替换基础地址切片, 空数组可清空, 省略/null 不更新.")
  base_urls?: BaseUrlInput[] | null;

  @doc("替换逗号分隔原始模型文本, 空字符串可写入, 省略/null 不更新.")
  `model`?: string | null;

  @doc("替换自定义模型文本, 空字符串可写入, 省略/null 不更新.")
  custom_model?: string | null;

  @doc("代理开关 patch, false 可写入, 省略/null 不更新.")
  proxy?: boolean | null;

  @doc("自动同步开关 patch, false 可写入, 省略/null 不更新.")
  auto_sync?: boolean | null;

  @doc("自动分组类型 patch, 0 不分组, 1 模糊, 2 精确, 3 正则. CRUD 无范围校验, 无 enum, 省略/null 不更新.")
  auto_group?: integer | null;

  @doc("完整替换 Header 规则, 空数组可清空, 外层省略/null 不更新. 元素 header_value:null 仍表示删除 Header.")
  custom_header?: ManagementCustomHeaderInput[] | null;

  @doc("代理地址 patch, 外层 null 不更新且不能清成数据库 NULL. 空字符串可显式写入.")
  channel_proxy?: string | null;

  @doc("参数覆盖字符串 patch, 外层 null 不更新且不能清成数据库 NULL. 空白字符串直接通过, 字符串内容 null 也通过格式校验.")
  param_override?: string | null;

  @doc("模型过滤正则 patch, create/update 不编译. 外层 null 不更新, 空字符串可写入.")
  match_regex?: string | null;

  @doc("新增密钥, null 或空数组不操作. 缺少 dive, 元素 required 的准确版本执行情况未确认.")
  keys_to_add?: ChannelKeyAddRequest[] | null;

  @doc("逐项更新密钥, null 或空数组不操作. 仅更新非 nil 字段并限定所属渠道, 无 RowsAffected 检查.")
  keys_to_update?: ChannelKeyUpdateRequest[] | null;

  @doc("按 ID 删除该渠道密钥, null 或空数组不操作, 不检查删除数或逐项正数范围.")
  keys_to_delete?: integer[] | null;
}

@doc("渠道增量请求中的新增密钥项. channel_key 在 Go 结构体带 binding required, 但外层 keys_to_add 无 binding dive, 准确 Gin 1.11.0/validator 10.29.0 源码未静态核实, op 没有补充非空检查. 因此不加 required 或 minLength, 不将邻近版本证据当成已验证. 源码: internal/model/channel.go:82-86.")
model ChannelKeyAddRequest {
  @doc("新增密钥启用状态, 省略时 Go 零值 false. 新建实体有 GORM default:true, 最终数据库值未运行验证, 不声明 HTTP 默认值.")
  enabled?: boolean;

  @doc("上游密钥. 存在元素级 required 标签但其执行未确认, op 无非空手动校验.")
  channel_key?: string;

  @doc("密钥备注, 省略时空字符串.")
  remark?: string;
}

@doc("渠道增量请求中的密钥更新项. 元素 id 带 binding required, 外层 keys_to_update 无 dive, 准确依赖执行未确认, 不加 schema required 或非零约束. op 更新限定 channel_id, 无 RowsAffected 检查. 源码: internal/model/channel.go:88-93.")
model ChannelKeyUpdateRequest {
  @doc("密钥 ID. 元素级 required 的执行未确认, 目标不存在或不属该渠道可能无操作而成功.")
  id?: integer;

  @doc("省略/null 不更新, false 可显式写入.")
  enabled?: boolean | null;

  @doc("省略/null 不更新, 空字符串可显式写入, 没有非空手动校验.")
  channel_key?: string | null;

  @doc("省略/null 不更新, 空字符串可清空备注.")
  remark?: string | null;
}

@doc("设置渠道启用状态的输入. 两属性均无 binding required. 标量 JSON null 可能留下 Go 零值. 源码: internal/server/handlers/channel.go:129-132.")
model ChannelEnabledRequest {
  @doc("渠道 ID, 省略得 0, 无正数检查, 未找到由 op 返回 500.")
  id?: integer;

  @doc("目标启用状态, 省略得到 false 并对有效渠道执行禁用.")
  enabled?: boolean;
}

@doc("渠道模型探测输入. model 是唯一 binding required 字段. 正 channel_id 是有效 ID 来源, 非 null channel 是有效对象来源, 必须恰有一个有效来源. channel_id 为 0/负数并提供 channel 允许走对象来源, 所以不按属性出现与否使用互斥 oneOf. channel_id 为 null 或非正且 channel 省略/null 时失败. 不通过 schema 虚构已保存渠道存在或动态模型集合条件. 源码: internal/server/handlers/channel_model_probe.go:32-100.")
model ChannelTestModelRequest {
  @doc("仅大于 0 且非 null 才算有效 ID 来源. 正 ID 与非 null channel 同时提供失败. 0 或负值加 channel 可用, 并不要求此属性在 channel 路径下省略.")
  channel_id?: integer | null;

  @doc("非 null 的临时渠道对象算有效来源, 不自动保存. 与有效正 ID 互斥, 但允许同时带 0 或负 ID.")
  channel?: ChannelInput | null;

  @doc("binding required 要求非空字符串, 不 trim 请求值, 后续必须精确匹配渠道 model+custom_model 经拆分和 trim 后的集合.")
  @minLength(1)
  `model`: string;

  @doc("keys 数组下标, 省略为 0. 手动要求非负且小于渠道 keys 长度, 不按密钥 ID 或 enabled 筛选.")
  @minValue(0)
  key_index?: integer;

  @doc("非正值或省略时使用 30000ms. 正数转 duration 后大于 5 分钟则钳制为 300000ms, 不因超过 300000 而拒绝, 所以没有 schema maximum. 极大值存在转换乘法溢出风险.")
  timeout_ms?: int64;
}

@doc("即时探测结果, 不持久化. model, status_code 和 delay_ms 无 omitempty, error 为空时省略. HTTP 200 管理响应内可能包含失败结果. 源码: internal/helper/test_model.go:29-34,94-117,263-268.")
model TestModelResult {
  @doc("实际被测模型名.")
  `model`: string;

  @doc("获得的上游 HTTP 状态码. 网络, DNS 或超时等 Do 错误路径保持 0, 不直接变为管理接口 HTTP 状态.")
  status_code: integer;

  @doc("httpClient.Do 的耗时毫秒数, 不包含后续完整响应 body 读取时间.")
  delay_ms: int64;

  @doc("网络错误, 读 body 错误或 status_code>=400 时的上游摘要. 空字符串因 omitempty 省略. 不是所有非 2xx 都生成摘要, 截断按字节并追加三个点, 不声明严格 200 字符 maxLength.")
  error?: string;
}

@doc("Group 实体 items 中的输入项, 没有 binding required. GORM 联合唯一索引和 not null 标签不作为 HTTP schema 约束. 源码: internal/model/group.go:24-31.")
model GroupItemInput {
  @doc("分组项主键, 实体绑定未剔除.")
  id?: integer;

  @doc("所属分组 ID, 实际关联赋值由 GORM 决定.")
  group_id?: integer;

  @doc("关联渠道 ID, 管理 create 不检查渠道存在性.")
  channel_id?: integer;

  @doc("实际模型名, 管理 create 不检查是否属于渠道模型集合.")
  model_name?: string;

  @doc("分组项优先级, 无范围手动校验.")
  priority?: integer;

  @doc("分组项权重, 无正数或总和手动校验.")
  weight?: integer;
}

@doc("分组响应中的完整分组项. 所有字段无 omitempty.")
model GroupItem {
  ...OmitProperties<
    GroupItemInput,
    "id" | "group_id" | "channel_id" | "model_name" | "priority" | "weight"
  >;

  @doc("分组项主键, 实体绑定未剔除.")
  id: integer;

  @doc("所属分组 ID, 实际关联赋值由 GORM 决定.")
  group_id: integer;

  @doc("关联渠道 ID, 管理 create 不检查渠道存在性.")
  channel_id: integer;

  @doc("实际模型名, 管理 create 不检查是否属于渠道模型集合.")
  model_name: string;

  @doc("分组项优先级, 无范围手动校验.")
  priority: integer;

  @doc("分组项权重, 无正数或总和手动校验.")
  weight: integer;
}

@doc("直接绑定 Group 的实体输入. 属性无 binding required, 输入独立于响应 required. 标量 JSON null 的宽松解析仅通过描述说明, 切片和指针显式允许 null. 源码: internal/model/group.go:12-21.")
model GroupInput {
  @doc("分组主键, 实体绑定未剔除, 无正数手动校验.")
  id?: integer;

  @doc("分组名称, GORM unique/not null 不是 HTTP 非空校验.")
  name?: string;

  @doc("GroupMode: 1 轮询, 2 随机, 3 故障转移, 4 加权分配. CRUD 无枚举范围检查, 不添加 enum.")
  mode?: integer;

  @doc("分组匹配正则, create 仅在非空时编译, regexp2 ECMAScript 模式, 不同于 Channel 的 match_regex 校验路径.")
  match_regex?: string;

  @doc("单个渠道首 token 超时秒数, 管理 CRUD 未检查非负范围.")
  first_token_time_out?: integer;

  @doc("会话保持秒数, 模型注释约定 0 禁用, 管理 CRUD 未检查范围.")
  session_keep_time?: integer;

  @doc("分组自定义请求头规则, nil 为 null, 元素值允许 null 删除 Header.")
  custom_header?: ManagementCustomHeaderInput[] | null;

  @doc("分组参数覆盖的 JSON 字符串. nil 和空白直接通过, 非空字符串可反序列化到 map 时通过, 包括字符串内容 null. 不校验参数语义.")
  param_override?: string | null;

  @doc("关联分组项, 输入允许省略/null/空数组. 管理 create 不检查渠道存在或模型集合.")
  items?: GroupItemInput[] | null;
}

@doc("分组管理响应. 除 items 外所有实体字段都没有 omitempty, custom_header 的 nil 为 null, param_override 的 nil 为 null. items 空或 nil 时省略, 属性出现时是数组而不是 null. 从 Input 增加响应 required, 输入不会继承该要求.")
model Group {
  ...OmitProperties<
    GroupInput,
    | "id"
    | "name"
    | "mode"
    | "match_regex"
    | "first_token_time_out"
    | "session_keep_time"
    | "custom_header"
    | "param_override"
    | "items"
  >;

  @doc("分组主键, 实体绑定未剔除, 无正数手动校验.")
  id: integer;

  @doc("分组名称, GORM unique/not null 不是 HTTP 非空校验.")
  name: string;

  @doc("GroupMode: 1 轮询, 2 随机, 3 故障转移, 4 加权分配. CRUD 无枚举范围检查, 不添加 enum.")
  mode: integer;

  @doc("分组匹配正则, create 仅在非空时编译, regexp2 ECMAScript 模式, 不同于 Channel 的 match_regex 校验路径.")
  match_regex: string;

  @doc("单个渠道首 token 超时秒数, 管理 CRUD 未检查非负范围.")
  first_token_time_out: integer;

  @doc("会话保持秒数, 模型注释约定 0 禁用, 管理 CRUD 未检查范围.")
  session_keep_time: integer;

  @doc("完整自定义 Header 响应规则, nil 为 null.")
  custom_header: CustomHeader[] | null;

  @doc("分组参数覆盖的 JSON 字符串. nil 和空白直接通过, 非空字符串可反序列化到 map 时通过, 包括字符串内容 null. 不校验参数语义.")
  param_override: string | null;

  @doc("完整分组项, 空或 nil 时因 omitempty 省略, 不是必须存在的响应属性.")
  items?: GroupItem[];
}

@doc("分组增量 DTO. 顶层 id 有 binding required 非零要求. 指针省略/null 不更新, 零值和空数组可显式更新. items 操作切片缺少 dive, 不添加未经准确版本确认的元素 required. 源码: internal/model/group.go:34-60.")
model GroupUpdateRequest {
  @doc("目标分组 ID, binding required 排除 0, 不是正数检查. 不存在时返回 500.")
  @extension("not", #{ `const`: 0 })
  id: integer;

  @doc("名称 patch, 省略/null 不更新, 空字符串可写入, 无非空手动校验.")
  name?: string | null;

  @doc("模式 patch, 1 轮询, 2 随机, 3 故障转移, 4 加权, 无范围或 enum 校验, 省略/null 不更新.")
  mode?: integer | null;

  @doc("正则 patch, 非 null 时编译, 包括空字符串; 省略/null 不更新.")
  match_regex?: string | null;

  @doc("首 token 超时秒数 patch, 无非负检查, 省略/null 不更新.")
  first_token_time_out?: integer | null;

  @doc("会话保持秒数 patch, 0 可显式写入, 无范围检查, 省略/null 不更新.")
  session_keep_time?: integer | null;

  @doc("完整替换分组 Header 规则, 空数组可清空, 外层省略/null 不更新, 元素值 null 删除 Header.")
  custom_header?: ManagementCustomHeaderInput[] | null;

  @doc("参数覆盖字符串 patch, 外层省略/null 不更新且不能清成数据库 NULL. 空白或字符串内容 null 可通过格式校验.")
  param_override?: string | null;

  @doc("新增项, null 或空数组不操作. op 无渠道存在或模型集合手动校验, 元素 required 执行未确认.")
  items_to_add?: GroupItemAddRequest[] | null;

  @doc("更新项, null 或空数组不操作. priority 和 weight 漏传会同时按 0 写入, 不是真正的子项 patch.")
  items_to_update?: GroupItemUpdateRequest[] | null;

  @doc("删除所属本分组的项 ID, null 或空数组不操作, 无逐项正数或 RowsAffected 校验.")
  items_to_delete?: integer[] | null;
}

@doc("分组增量新增项. channel_id 和 model_name 在 Go 元素结构体带 binding required, 但外层 items_to_add 无 dive, 准确 Gin 1.11.0/validator 10.29.0 的执行未确认, op 无补充检查. 不加 required, 非零或 minLength 限制. 源码: internal/model/group.go:49-54.")
model GroupItemAddRequest {
  @doc("渠道 ID, 元素 required 执行未确认, op 不检查渠道存在.")
  channel_id?: integer;

  @doc("模型名, 元素 required 执行未确认, op 不检查所属渠道模型集合.")
  model_name?: string;

  @doc("优先级, 省略为 0, 无范围手动校验.")
  priority?: integer;

  @doc("权重, 省略为 0, 无正数或总和手动校验.")
  weight?: integer;
}

@doc("分组增量更新项. 元素 id 有 binding required, 外层 items_to_update 无 dive, 准确依赖执行未确认, 不添加 schema required 或非零限制. priority 和 weight 为非指针 int, 省略得到 0, op 同时写入两个字段. JSON omitempty 只影响序列化, 不表示输入省略会保留旧值. 源码: internal/model/group.go:57-61, internal/op/group.go:133-155.")
model GroupItemUpdateRequest {
  @doc("分组项 ID, 元素 required 执行未确认, 更新限定 group_id 且无 RowsAffected 检查.")
  id?: integer;

  @doc("写入的优先级, 省略或宽松解析为零值时写入 0, 不保留旧值.")
  priority?: integer;

  @doc("写入的权重, 省略或宽松解析为零值时写入 0, 不保留旧值.")
  weight?: integer;
}

@doc("直接绑定 APIKey 的输入实体, create/update 均无 binding required, 不添加任何属性 required. 标量 JSON null 的 Go 宽松解析仅在描述中记录, 不扩展规范标量类型. GORM not null/default 不作为 HTTP 约束. update 是完整替换, 漏传属性会按零值保存, 除 api_key 保留旧值. 源码: internal/model/apikey.go:3-11.")
model APIKeyInput {
  @doc("主键. create 未剔除客户端 ID. update 缺失/0/不存在通常返回 500, 不是 binding required 400.")
  id?: integer;

  @doc("密钥名称, 没有 HTTP 非空或长度校验. update 漏传会写空字符串.")
  name?: string;

  @doc("密钥文本. create 会被生成值覆盖, update 不更新此列并恢复原值.")
  api_key?: string;

  @doc("启用状态. create 的 GORM default:true 不声明为请求默认值. update 漏传得到 false 并完整保存.")
  enabled?: boolean;

  @doc("Unix 秒过期时间, 无范围校验. 鉴权仅在值大于 0 且小于当前 Unix 秒时拒绝, 非正值不限制. update 漏传保存 0.")
  expire_at?: int64;

  @doc("最大消费额度, 无非负校验. 鉴权仅在大于 0 且小于累计输入加输出费用时拒绝, 等于上限仍放行, 非正值不限制. update 漏传保存 0.")
  max_cost?: float64;

  @doc("逗号分隔的允许模型文本, 无格式或正则校验. 空字符串不限制. relay 按逗号拆分精确匹配且不 trim, stats 展示逻辑会 trim 并与 Group 名称取交集. update 漏传保存空字符串.")
  supported_models?: string;
}

@doc("APIKey 管理响应. id, name, api_key 和 enabled 无 omitempty, 始终输出. expire_at=0, max_cost=0 和 supported_models 为空时省略, 不是输出 null; 非零负值仍可输出. api_key 不脱敏. stats 路径的 supported_models 是改写后的展示文本.")
model APIKey {
  ...OmitProperties<APIKeyInput, "id" | "name" | "api_key" | "enabled">;

  @doc("主键. create 未剔除客户端 ID. update 缺失/0/不存在通常返回 500, 不是 binding required 400.")
  id: integer;

  @doc("密钥名称, 没有 HTTP 非空或长度校验. update 漏传会写空字符串.")
  name: string;

  @doc("密钥文本. create 会被生成值覆盖, update 不更新此列并恢复原值.")
  api_key: string;

  @doc("启用状态. create 的 GORM default:true 不声明为请求默认值. update 漏传得到 false 并完整保存.")
  enabled: boolean;
}

@doc("当前 APIKey 统计复合载荷, 两个字段始终输出且非 null. stats 使用共享 StatsAPIKey 和 StatsMetrics 定义, info 的 supported_models 已按分组名称改写. 源码: internal/server/handlers/apikey.go:100-130.")
model APIKeyStatsResponse {
  @doc("当前密钥原始数值统计, 非前端格式化值.")
  stats: StatsAPIKey;

  @doc("当前密钥信息, api_key 原样输出, supported_models 已替换为可展示的分组名称集合.")
  info: APIKey;
}

@doc("公共累计指标, 嵌入各 Stats 实体时字段平铺, 不存在 stats_metrics 嵌套对象. 不含前端派生的 request_count, total_token 或 total_cost. 计数为 int64, 费用为 float64, 未设置非负约束以兼容宽松导入的历史数据. Source: internal/model/stats.go:3-10; internal/relay/metrics.go:64-82.")
model StatsMetrics {
  @doc("累计输入 Token 数.")
  input_token: int64;

  @doc("累计输出 Token 数.")
  output_token: int64;

  @doc("累计输入费用.")
  input_cost: float64;

  @doc("累计输出费用.")
  output_cost: float64;

  @doc("累计等待毫秒数, 不是平均值.")
  wait_time: int64;

  @doc("累计成功请求数.")
  request_success: int64;

  @doc("累计失败请求数.")
  request_failed: int64;
}

@doc("日统计实体. date 正常为服务器本地时间 YYYYMMDD, 不使用 date format, 导入路径没有日期格式业务校验. Source: internal/model/stats.go:24-26.")
model StatsDaily {
  ...OmitProperties<StatsMetrics, "date">;

  @doc("日主键, 正常形如 20260102.")
  date: string;
}

@doc("滚动小时统计实体, 数据库主键仅为 hour, 跨天复用该小时并用 date 标记归属. /stats/hourly 只返回今天截至当前小时并补零, DBDump 则直接导出滚动数据库表. 导入没有 hour 或 date 格式校验, 通用实体 schema 不强制 hour 为 0 到 23. Source: internal/model/stats.go:18-21; internal/op/stats.go:403-424.")
model StatsHourly {
  ...OmitProperties<StatsMetrics, "hour" | "date">;

  @doc("小时主键, 正常为 0 到 23, Go int.")
  hour: integer;

  @doc("最后更新的本地日期, 正常为 YYYYMMDD.")
  date: string;
}

@doc("全局累计统计. ID 没有 JSON tag, 线格式必须为大写 ID, 正常初始化为 1, 不固定为常量以兼容数据库记录. Source: internal/model/stats.go:13-16; internal/op/stats.go:449-458.")
model StatsTotal {
  ...OmitProperties<StatsMetrics, "ID">;

  @doc("大写字段名, Go int 主键.")
  ID: integer;
}

@doc("模型和渠道维度统计实体, 指标平铺. Source: internal/model/stats.go:29-33.")
model StatsModel {
  ...OmitProperties<StatsMetrics, "id" | "name" | "channel_id">;

  @doc("Go int 主键.")
  id: integer;

  @doc("模型名.")
  name: string;

  @doc("渠道 ID, Go int.")
  channel_id: integer;
}

@doc("渠道维度统计实体, 指标平铺. Source: internal/model/stats.go:36-38.")
model StatsChannel {
  ...OmitProperties<StatsMetrics, "channel_id">;

  @doc("渠道 ID 主键, Go int.")
  channel_id: integer;
}

@doc("API Key 维度统计实体, 指标平铺. Source: internal/model/stats.go:41-43.")
model StatsAPIKey {
  ...OmitProperties<StatsMetrics, "api_key_id">;

  @doc("API Key ID 主键, Go int.")
  api_key_id: integer;
}

@doc("自定义请求头对象, header_value 是指针, 可为 null. Source: internal/model/channel.go:44-46.")
model CustomHeader {
  header_key: string;
  header_value: string | null;
}

@doc("宽松自定义请求头输入, 没有 required binding, 遗漏或 null 进入 Go 零值, 未知字段允许.")
union CustomHeaderInput {
  {
    header_key?: string | null,
    header_value?: string | null,
  },
  null,
}

@doc("渠道尝试记录. Go int 的具体范围依编译架构, 不统一标成 int32. status 枚举为当前代码产生的状态, 宽松导入不校验枚举. Source: internal/model/log.go:6-23.")
model ChannelAttempt {
  @doc("尝试渠道 ID.")
  channel_id: integer;

  @doc("使用的 Key ID, 0 时省略.")
  channel_key_id?: integer;

  channel_name: string;
  model_name: string;

  @doc("尝试序号, 可包含跳过项.")
  attempt_num: integer;

  @doc("通常为 success, failed, circuit_break 或 skipped. 导入路径允许其他字符串, 因而响应 schema 不强制 enum.")
  status: string;

  @doc("该尝试耗时, 毫秒.")
  duration: integer;

  @doc("会话保持命中, false 时省略.")
  sticky?: boolean;

  @doc("附加消息, 空字符串时省略.")
  msg?: string;
}

@doc("转发日志, list, SSE 和 DBDump 使用同一实体. id 是 JSON 数值形式的 Snowflake int64, JS number 可能丢精度, 不能声明为字符串. 实际渠道键为 channel, 不是 channel_id. 请求和响应正文是字符串, 不保证原始 body. 请求日志会过滤 RawRequest 和图片二进制并反映参数覆盖. 超正文上限时跳过整条日志, 统计可仍已更新. Source: internal/model/log.go:26-43; internal/relay/metrics.go:105-175,191-243.")
model RelayLog {
  @doc("Snowflake ID, JSON number.")
  id: int64;

  @doc("请求开始时间, Unix 秒.")
  time: int64;

  request_model_name: string;
  request_api_key_name: string;

  @doc("实际渠道 ID, 优先成功尝试, 否则最后失败尝试, 无此类尝试时可为 0.")
  channel: integer;

  channel_name: string;
  actual_model_name: string;

  @doc("输入 Token 数, Go int.")
  input_tokens: integer;

  @doc("输出 Token 数, Go int.")
  output_tokens: integer;

  @doc("首 Token 耗时毫秒, 未记录时为 0.")
  ftut: integer;

  @doc("总耗时毫秒.")
  use_time: integer;

  @doc("日志中记录的输入和输出费用之和.")
  cost: float64;

  request_content: string;
  response_content: string;

  @doc("无错误时也输出空字符串.")
  error: string;

  @doc("所有记录的尝试, nil 时为 null, 不是省略字段.")
  attempts: ChannelAttempt[] | null;

  @doc("attempts 长度, 包括跳过或熔断项, 不仅是实际上游调用次数.")
  total_attempts: integer;
}

@doc("运行期 KV 设置, key 和 value 均以字符串输出, 不强制 key enum, 缓存可含已加载的自定义 key. 当前默认值: proxy_url='', stats_save_interval='10', cors_allow_origins='*', model_info_update_interval='24', sync_llm_interval='24', relay_log_keep_period='7', relay_log_keep_enabled='true', relay_log_flush_size='50', relay_log_memory_cache_size='100', relay_log_max_content_size_mb='2', relay_log_memory_log_max_dimidiate_times='15', circuit_breaker_threshold='5', circuit_breaker_cooldown='60', circuit_breaker_max_cooldown='600', circuit_breaker_enabled='true', key_circuit_breaker_enabled='true', passthrough_enabled='false', two_factor_enabled='false'. /setting/set 校验: proxy_url 允许空字符串, 非空仅 http/https/socks5 且 host 非空; model_info_update_interval, sync_llm_interval, relay_log_keep_period, relay_log_flush_size, relay_log_memory_cache_size 和三个熔断数值只校验可解析为 Go int, 不要求正数; relay_log_max_content_size_mb 要求整数 >= -1, -1 不限正文, 0 仅允许零正文大小; relay_log_memory_log_max_dimidiate_times 要求 -1 或正整数; relay_log_keep_enabled, circuit_breaker_enabled, key_circuit_breaker_enabled, passthrough_enabled 仅接受字符串 true/false; two_factor_enabled 在 handler 直接 403; stats_save_interval 和 cors_allow_origins 没有内容校验. 运行时日志刷写或内存上限负值回退默认, flush 0 实时写数据库, memory 0 不记日志, 内存 dimidiate=-1 同时绕过折半清理而不保证硬缓存上限. keep_period<=0 不清理历史, 熔断数值<=0 回退 5/60/600. stats_save_interval 修改不热更新任务. 导入不执行这些 /set 校验, 导入和导出都过滤 two_factor_enabled. Source: internal/model/setting.go:11-138; internal/server/handlers/setting.go:51-87; internal/op/log.go:145-268; internal/relay/balancer/circuit.go:74-106.")
model Setting {
  @doc("设置键, 不强制预定义 enum.")
  key: string;

  @doc("字符串形式的值, 包括布尔和数值设置.")
  value: string;
}

@doc("宽松 Setting 输入, key 和 value 没有 required binding, 遗漏或 null 进入 Go 字符串零值. /set 再执行按 key 校验和缓存存在性检查, 导入则不调用 Validate. 预定义键, 默认和校验见 Setting, 此 schema 不把它们作为导入约束.")
union SettingInput {
  {
    key?: string | null,
    value?: string | null,
  },
  null,
}

@doc("数据库导出主体, 不是完整用户或原子运行态快照. 顶层四项元数据必有, version 当前固定 1. 所有表数组带 omitempty, 空时字段省略而不是固定 [] 或 null. channels 不 Preload keys 或 stats, groups 不 Preload items, 对应关联在单独顶层数组. settings 排除 two_factor_enabled. include_stats 和 include_logs 为 false 时相应顶层表不会导出. Source: internal/model/backup.go:7-28; internal/op/backup.go:16-76.")
model DBDump {
  @extension("type", "integer")
  @doc("当前导出格式版本.")
  version: 1;

  @doc("UTC 时间, Go time.Time JSON 编码可带小数秒.")
  @format("date-time")
  exported_at: string;

  include_logs: boolean;
  include_stats: boolean;
  channels?: ChannelEntity[];
  channel_keys?: ChannelKey[];
  groups?: Group[];
  group_items?: GroupItem[];
  llm_infos?: LLMInfo[];
  api_keys?: APIKey[];
  settings?: Setting[];
  stats_total?: StatsTotal[];
  stats_daily?: StatsDaily[];
  stats_hourly?: StatsHourly[];
  stats_model?: StatsModel[];
  stats_channel?: StatsChannel[];
  stats_api_key?: StatsAPIKey[];
  relay_logs?: RelayLog[];
}

@doc("宽松增量导入对象. 无 required, 未知字段允许并被 Go JSON 解码忽略, JSON null 或 {} 可走空导入. version 缺失或 null 成为 0, 仅 0 或 1 被业务接受, 其他版本返回 400, 此处以描述记录版本校验而不把导出 const 复用于输入. exported_at 若提供必须能被 Go time.Time 解码, 可遗漏或为 null, 未用于恢复逻辑. 数组遗漏, null 或 [] 都表示无对应行. include 标志缺失或 null 默认为 false, 只有相应标志为 true 才处理顶层日志或统计. 不调用实体创建校验或 Setting.Validate, 不强制枚举, 非负数或日期业务规则. 整数仍必须符合对应 Go 类型解析范围. Source: internal/server/handlers/setting.go:153-186; internal/model/backup.go:7-28; internal/op/backup.go:79-175.")
union DBDumpInput {
  {
    @doc("业务接受 0 或 1, 缺失或 null 进入 0, 其他值返回 400.")
    version?: integer | null,

    @doc("若提供非 null 值, 由 Go time.Time 按 RFC3339 时间字符串解码; 不要求存在.")
    @extension("format", "date-time")
    exported_at?: string | null,

    @doc("缺失或 null 默认为 false.")
    include_logs?: boolean | null,

    @doc("缺失或 null 默认为 false.")
    include_stats?: boolean | null,

    channels?: DBDumpInputchannelsItemValue[] | null,
    channel_keys?: ChannelKeyImport[] | null,
    groups?: DBDumpInputgroupsItemValue[] | null,
    group_items?: GroupItemImport[] | null,
    llm_infos?: DBDumpInputllm_infosItemValue[] | null,
    api_keys?: DBDumpInputapi_keysItemValue[] | null,
    settings?: SettingInput[] | null,
    stats_total?: StatsTotalInput[] | null,
    stats_daily?: StatsDailyInput[] | null,
    stats_hourly?: StatsHourlyInput[] | null,
    stats_model?: StatsModelInput[] | null,
    stats_channel?: StatsChannelInput[] | null,
    stats_api_key?: StatsAPIKeyInput[] | null,
    relay_logs?: RelayLogInput[] | null,
  },
  null,
}

@doc("导入兼容形态, anyOf 不表示严格或互斥解包规则. 裸 DBDumpInput 已允许未知字段, 因而任意含 data 的对象仍可能按裸对象处理. 仅当外层 version 为 0 且受检查业务数组全空时才尝试一层 wrapper, 条件漏查 channel_keys, 不检查元数据标志; code/message 不必提供或满足成功值, wrapper 解码失败被忽略. 精确规则见 importDatabase. JSON null 由 DBDumpInput 兼容.")
union DBDumpImportBody {
  DBDumpInput,
  {
    @doc("可省略, 解包时按 Go int 尝试解码, 不校验成功码; 类型错误可使 wrapper 被静默忽略.")
    code?: unknown,

    @doc("可省略, 解包时按 Go string 尝试解码, 不校验内容; 类型错误可使 wrapper 被静默忽略.")
    message?: unknown,

    data: DBDumpInput,
  },
}

@doc("逐表 RowsAffected, 依数据库驱动和冲突策略而异, 不是稳定的新增行数. 基础 channels, channel_keys, groups, group_items, llm_infos, api_keys, settings 总有键, 空操作为 0. stats_total, stats_daily, stats_hourly, stats_model, stats_channel, stats_api_key 仅 include_stats=true 时有键. relay_logs 仅 include_logs=true 时有键. Source: internal/model/backup.go:31-33; internal/op/backup.go:89-175.")
model DBImportResult {
  @doc("数据库操作受影响行数映射.")
  rows_affected: {
    ...Record<DBImportResultrows_affectedAdditionalPropertyValue>;
  };
}

@doc("当前列表展示的 Open 条目, 没有底层 key, 渠道 ID 或模型标识, 同名条目可重复. 查询会隐藏 HalfOpen, 不应据此推断试探已被放行. Source: internal/relay/balancer/circuit.go:213-280.")
model CircuitBreakerStatus {
  @doc("渠道名称, 未命中缓存时为 Channel#<id>.")
  channel_name: string;

  @doc("当前 ListTripped 只输出 open, half_open 不展示.")
  state: "open";

  @doc("剩余冷却秒数, 向下取整, 不足 1 秒时可为 0.")
  @minValue(0)
  remaining_cooldown: int64;
}

@doc("GitHub latest release 的选取字段, 不含下载地址或 assets. published_at 是未解析的 string, 不标 date-time. 所有字段都输出, 上游缺字段可成为空字符串. 上游 message 非空转 HTTP 500, 成功时仍必有 message: ''. Source: internal/update/update.go:26-30,79-93.")
model LatestInfo {
  @doc("发布标签, 未强制非空或 semver.")
  tag_name: string;

  @doc("上游发布时间原样字符串, 未做时间格式校验.")
  published_at: string;

  @doc("Release notes 正文.")
  body: string;

  @doc("成功 data 中必有的空字符串, 非空上游值会转错误响应.")
  message: "";
}

@doc("DBDump.channel_keys 的宽松输入, 不复用严格输出 required. 遗漏或 null 进入 Go 零值, 未知字段允许. default:true 与关联写入的精确 GORM 行为未做依赖或运行验证. Source: internal/model/channel.go:49-57.")
union ChannelKeyImport {
  {
    id?: integer | null,
    channel_id?: integer | null,
    enabled?: boolean | null,
    channel_key?: string | null,
    status_code?: integer | null,

    @doc("Unix 秒.")
    last_use_time_stamp?: int64 | null,

    total_cost?: float64 | null,
    remark?: string | null,
  },
  null,
}

@doc("DBDump.group_items 的宽松输入, 不执行分组创建或增量更新请求的 required 校验, 数据库约束仍可能使事务失败. Source: internal/model/group.go:24-30.")
union GroupItemImport {
  {
    id?: integer | null,
    group_id?: integer | null,
    channel_id?: integer | null,
    model_name?: string | null,
    priority?: integer | null,
    weight?: integer | null,
  },
  null,
}

@doc("DBDump 统计指标宽松输入, 无 required 或非负约束. null 或遗漏的标量进入零值, 未知字段允许.")
union StatsMetricsInput {
  {
    input_token?: int64 | null,
    output_token?: int64 | null,
    input_cost?: float64 | null,
    output_cost?: float64 | null,
    wait_time?: int64 | null,
    request_success?: int64 | null,
    request_failed?: int64 | null,
  },
  null,
}

@doc("宽松日统计输入, 不校验 date 格式, 按 date upsert.")
union StatsDailyInput {
  {
    input_token?: int64 | null,
    output_token?: int64 | null,
    input_cost?: float64 | null,
    output_cost?: float64 | null,
    wait_time?: int64 | null,
    request_success?: int64 | null,
    request_failed?: int64 | null,
    date?: string | null,
  },
  null,
}

@doc("宽松小时统计输入, 不校验 hour 为 0 到 23 或 date 格式, 仅按 hour upsert.")
union StatsHourlyInput {
  {
    input_token?: int64 | null,
    output_token?: int64 | null,
    input_cost?: float64 | null,
    output_cost?: float64 | null,
    wait_time?: int64 | null,
    request_success?: int64 | null,
    request_failed?: int64 | null,
    hour?: integer | null,
    date?: string | null,
  },
  null,
}

@doc("宽松全局统计输入, 规范字段为大写 ID, Go JSON 解码也可能按大小写不敏感匹配输入名. 按数据库 id upsert.")
union StatsTotalInput {
  {
    input_token?: int64 | null,
    output_token?: int64 | null,
    input_cost?: float64 | null,
    output_cost?: float64 | null,
    wait_time?: int64 | null,
    request_success?: int64 | null,
    request_failed?: int64 | null,
    ID?: integer | null,
  },
  null,
}

@doc("宽松模型统计输入, 不要求名称或关联 ID, 按 id upsert, 数据库约束仍可失败.")
union StatsModelInput {
  {
    input_token?: int64 | null,
    output_token?: int64 | null,
    input_cost?: float64 | null,
    output_cost?: float64 | null,
    wait_time?: int64 | null,
    request_success?: int64 | null,
    request_failed?: int64 | null,
    id?: integer | null,
    name?: string | null,
    channel_id?: integer | null,
  },
  null,
}

@doc("宽松渠道统计输入, 按 channel_id upsert.")
union StatsChannelInput {
  {
    input_token?: int64 | null,
    output_token?: int64 | null,
    input_cost?: float64 | null,
    output_cost?: float64 | null,
    wait_time?: int64 | null,
    request_success?: int64 | null,
    request_failed?: int64 | null,
    channel_id?: integer | null,
  },
  null,
}

@doc("宽松 API Key 统计输入, 按 api_key_id upsert.")
union StatsAPIKeyInput {
  {
    input_token?: int64 | null,
    output_token?: int64 | null,
    input_cost?: float64 | null,
    output_cost?: float64 | null,
    wait_time?: int64 | null,
    request_success?: int64 | null,
    request_failed?: int64 | null,
    api_key_id?: integer | null,
  },
  null,
}

@doc("宽松导入尝试记录, 无 required, 不校验 status 枚举或次数范围.")
union ChannelAttemptInput {
  {
    channel_id?: integer | null,
    channel_key_id?: integer | null,
    channel_name?: string | null,
    model_name?: string | null,
    attempt_num?: integer | null,
    status?: string | null,
    duration?: integer | null,
    sticky?: boolean | null,
    msg?: string | null,
  },
  null,
}

@doc("宽松 DBDump 日志输入, 无 required, 不校验时间范围, 状态枚举或 total_attempts 一致性. id 不自动生成, 只有 include_logs=true 时处理该顶层数组, 按冲突 DoNothing 插入.")
union RelayLogInput {
  {
    id?: int64 | null,
    time?: int64 | null,
    request_model_name?: string | null,
    request_api_key_name?: string | null,
    channel?: integer | null,
    channel_name?: string | null,
    actual_model_name?: string | null,
    input_tokens?: integer | null,
    output_tokens?: integer | null,
    ftut?: integer | null,
    use_time?: integer | null,
    cost?: float64 | null,
    request_content?: string | null,
    response_content?: string | null,
    error?: string | null,
    attempts?: ChannelAttemptInput[] | null,
    total_attempts?: integer | null,
  },
  null,
}

@doc("自定义模型价格输出. 全部字段由 Go 序列化输出. 价格为每百万 token 美元, 来源: internal/model/llm.go:3-13, internal/relay/metrics.go:58-61.")
model LLMInfo {
  name: string;
  input: float64;
  output: float64;
  cache_read: float64;
  cache_write: float64;
}

@doc("创建或更新的输入, 无 binding required 或数值范围校验. 省略字段及标量 JSON null 形成 Go 零值, 顶层 null 也不会触发字段必填校验, 后续数据库行为不由本 schema 保证. create 在 op 内将 name 转小写且响应仍保留原始 name, update 按原值精确查找. 来源: internal/model/llm.go:3-13, internal/server/handlers/model.go:127-150.")
union LLMInfoInput {
  {
    name?: string | null,
    input?: float64 | null,
    output?: float64 | null,
    cache_read?: float64 | null,
    cache_write?: float64 | null,
  },
  null,
}

@doc("模型与渠道关联输出, enabled 只表示渠道启用. 来源: internal/model/llm.go:15-20.")
model LLMChannel {
  name: string;
  enabled: boolean;
  channel_id: integer;
  channel_name: string;
}

model ModelPriceDeleteInput {
  @doc("binding required, 不 trim, 精确匹配自定义价格缓存键.")
  @minLength(1)
  name: string;
}

union DBDumpInputchannelsItemValue {
  ChannelInput,
  null,
}

union DBDumpInputgroupsItemValue {
  GroupInput,
  null,
}

union DBDumpInputllm_infosItemValue {
  LLMInfoInput,
  null,
}

union DBDumpInputapi_keysItemValue {
  APIKeyInput,
  null,
}

scalar DBImportResultrows_affectedAdditionalPropertyValue extends int64;
