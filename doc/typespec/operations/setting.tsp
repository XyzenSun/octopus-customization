using TypeSpec.Http;
using TypeSpec.OpenAPI;

namespace Octopus;

@route("/api/v1/setting/list")
@get
@operationId("listSettings")
@tag("Setting")
@summary("获取运行期设置列表")
@doc("返回设置内存缓存中的全部项, 包括已加载的自定义 key, 顺序不保证. 空集合为 data: []. 与启动配置分开. 当前 op 实现无业务错误返回, 500 仍可能来自 recovery. Source: internal/server/handlers/setting.go:42-48; internal/op/setting.go:15-23.")
op listSettings():
  | HttpResponse<200, Success<Setting[]>, "设置列表.">
  | MissingAuth400
  | Unauthorized401
  | Internal500;

@route("/api/v1/setting/set")
@post
@operationId("setSetting")
@tag("Setting")
@summary("更新单个运行期设置")
@doc("认证后要求 Content-Type 包含大小写敏感子串 application/json, 不是严格 MIME 解析或通用 +json 支持. 绑定 Setting 后进行按 key 的校验, 只更新缓存中已存在的 key, 不创建新 key. 未知 key 通常返回 500 setting not found. two_factor_enabled 无论 value 都返回 403, 必须使用 2FA 专用接口. 先写数据库再更新缓存, 相同值不写数据库. 仅 model_info_update_interval 和 sync_llm_interval 调用 task.Update; stats_save_interval 不热更新任务. task.Update 为尽力更新, 不存在的任务不会创建, 非正间隔删除任务, 非阻塞发送也可能跳过, 接口仍可返回 200. 各默认值和校验见 Setting. Source: internal/server/handlers/setting.go:28-30,51-87; internal/op/setting.go:34-50; internal/task/task.go:54-77.")
op setSetting(
  @header contentType: "application/json",

  @doc("key 和 value 没有 required binding. 遗漏进入零值后再走校验和缓存存在性检查. value 应为字符串形式, 不能提交 JSON 布尔或数值来代替字符串.")
  @body
  body: SettingInput,
):
  | HttpResponse<200, Success<Setting>, "提交的设置对象, value 保持字符串形式.">
  | HttpResponse<
      400,
      ErrorResponse,
      "缺少 Authorization, JSON 绑定失败或 Setting.Validate 校验失败. 不把未知 key 归为 400. Source: internal/server/handlers/setting.go:53-65."
    >
  | Unauthorized401
  | HttpResponse<
      403,
      ErrorResponse,
      "尝试写入 two_factor_enabled. message 为 two factor authentication must be changed via /api/v1/user/2fa."
    >
  | InvalidJSON415
  | Internal500;

@route("/api/v1/setting/export")
@get
@operationId("exportDatabase")
@tag("Setting")
@summary("导出数据库数据")
@doc("成功直接返回 DBDump JSON 文件, 不使用统一响应包装. 逐表查询数据库, 不先 flush 日志, 统计或 ChannelKey 运行缓存, 没有导出事务, 不是原子或完整运行态快照. 不包含用户, 密码, 2FA 密钥, 迁移表或启动配置, settings 过滤 two_factor_enabled. Channel 和 Group 查询不 Preload 关联, 密钥和分组项分别位于顶层 channel_keys 和 group_items. 导出内容可能包含明文渠道密钥和 API Key, 应作为敏感备份保管. include_stats 导出的小时表是滚动数据库表, 不是 /stats/hourly 的今天补零结果. Source: internal/server/handlers/setting.go:90-102; internal/op/backup.go:16-76,213-221.")
@returnsDoc("裸 DBDump JSON 下载. 文件名时间使用本地时间, exported_at 使用 UTC.")
op exportDatabase(
  @doc("缺失默认 false. strconv.ParseBool 接受 1, t, T, TRUE, true, True 和 0, f, F, FALSE, false, False. 其他文本或空值解析失败后被忽略, 结果为 false, 不返回 400.")
  @query(#{ name: "include_logs", explode: true })
  include_logs?: string = "false",

  @doc("缺失默认 false. 接受 strconv.ParseBool 的 1/0, t/f, T/F, TRUE/FALSE, true/false, True/False, 不是任意大小写组合. 非法或空文本回退 false, 不返回 400.")
  @query(#{ name: "include_stats", explode: true })
  include_stats?: string = "false",
):
  | {
      ...HttpResponse<
        200,
        DBDump,
        "裸 DBDump JSON 下载. 文件名时间使用本地时间, exported_at 使用 UTC."
      >;

      @doc("attachment; filename=\"octopus-export-YYYYMMDDhhmmss.json\".")
      @header("Content-Disposition")
      @extension(
        "example",
        "attachment; filename=\"octopus-export-20260102030405.json\""
      )
      Content_Disposition?: string;
    }
  | MissingAuth400
  | Unauthorized401
  | Internal500;

@route("/api/v1/setting/import")
@post
@operationId("importDatabase")
@tag("Setting")
@summary("增量导入数据库数据")
@doc("不使用 RequireJSON. Content-Type 大小写敏感地包含 multipart/form-data 时读取文件字段 file, 其他 Content-Type 或缺失头均把原始 body 作为 JSON 解码. application/octet-stream 仅代表其他媒体类型的直接 JSON 路径, 不是限定只接受这里列出的媒体类型. 文件后缀和 part MIME 不做业务校验, handler 使用 io.ReadAll 且没有显式大小限制. 先按裸 DBDump 解码; 仅 version 为 0 且 channels, groups, group_items, settings, api_keys, llm_infos, relay_logs 和全部六种 stats 数组长度都为 0 时尝试 wrapper. 该条件漏查 channel_keys, 也不检查 exported_at 或 include 标志. wrapper 的 code 和 message 不必提供, 不检查 code 为 200; wrapper 自身解码错误被忽略, 只有成功读到 data 才再解到同一个 dump, 未覆盖的顶层字段可能保留. 非零顶层 version 或非空受检查数组会阻止解包, 只兼容一层 wrapper. 例如 code 为字符串可使 data 被忽略而按空 dump 成功. 裸 {}, null 或 data: null 可进行空导入, 未知字段被忽略. anyOf 仅记录兼容形态, 不是严格的解包判定或互斥校验. version 仅允许 0 或 1, 缺失和 null 进入零值; 其他 version 返回 400. 所有表操作在一个事务内, channels, channel_keys, groups, group_items, api_keys 和启用的 relay_logs 按冲突 DoNothing 插入; llm_infos 按 name UpdateAll; settings 按 key 更新 value 并过滤 two_factor_enabled; stats 按各实体主键 UpdateAll, stats_hourly 仅按 hour 而非 date+hour. 缺失或空数组不删除旧数据, 冲突不重映射 ID 或关联. include_logs 和 include_stats 控制顶层可选表操作, 默认 false. 导入不走常规创建校验或 Setting.Validate, 不先保存运行缓存, 也不重调度任务. 提交后 InitCache 的错误被忽略, 仍可能返回 200, 刷新可覆盖未落库增量. Source: internal/server/handlers/setting.go:105-186; internal/op/backup.go:79-221; internal/op/cache.go:9-29.")
@extension(
  "requestBody",
  #{
    required: true,
    description: "裸 JSON, 条件式响应 wrapper 或 multipart 文件. 允许 JSON null, 空 body 则解码失败. 实际媒体类型判断是子串分支而非严格枚举.",
    content: #{
      `application/json`: #{
        schema: #{ $ref: "#/components/schemas/DBDumpImportBody" },
      },
      `application/octet-stream`: #{
        schema: #{ $ref: "#/components/schemas/DBDumpImportBody" },
      },
      `multipart/form-data`: #{
        schema: #{
          type: "object",
          required: #["file"],
          properties: #{
            file: #{
              type: "string",
              format: "binary",
              description: "JSON 文件内容, 支持与直接 body 相同的条件式 wrapper, 不校验后缀或 part MIME.",
            },
          },
        },
      },
    },
  }
)
op importDatabase(
  @header contentType: "application/json" | "application/octet-stream",

  @doc("裸 JSON, 条件式响应 wrapper 或 multipart 文件. 允许 JSON null, 空 body 则解码失败. 实际媒体类型判断是子串分支而非严格枚举.")
  @body
  body: DBDumpImportBody,
):
  | HttpResponse<
      200,
      Success<DBImportResult>,
      "逐表受影响行数, 不是稳定的新增行数. 缓存刷新成功不由该响应保证."
    >
  | HttpResponse<
      400,
      ErrorResponse,
      "缺少 Authorization, multipart 缺少或无法读取 file, body 读取或 JSON 解码失败, 不支持的 version, 或数据库导入事务失败. 数据库业务错误在此也映射为 400, 不是 500. multipart 缺少文件分支 message 为 missing upload file field 'file'. Source: internal/server/handlers/setting.go:108-145."
    >
  | Unauthorized401
  | Internal500;
