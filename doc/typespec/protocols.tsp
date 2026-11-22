using TypeSpec.Http;
using TypeSpec.OpenAPI;

namespace Octopus;

@extension("additionalProperties", true)
model OpenAIModel {
  id: string;
  object: "model";

  @extension("type", "integer")
  @doc("固定常量, 不是真实模型创建时间.")
  created: 1763395200;

  owned_by: "octopus";
}

@extension("additionalProperties", true)
model AnthropicModel {
  id: string;

  @doc("固定常量, 不是真实创建时间.")
  @extension("format", "date-time")
  created_at: "2024-01-01T00:00:00Z";

  @doc("与 id 相同.")
  display_name: string;

  type: "model";
}

@doc("Authorization API Key 鉴权的列表形态, 不使用 SuccessBase. 空 data 为 null. 来源: internal/server/handlers/model.go:93-106.")
@extension("additionalProperties", true)
model OpenAIModelList {
  success: true;
  object: "list";
  data: OpenAIModel[] | null;
}

@doc("x-api-key 鉴权的列表形态, 不使用 SuccessBase. 空 data 为 null 并省略 first_id/last_id. 首末项随未排序的缓存遍历而变. 来源: internal/server/handlers/model.go:72-91.")
@extension("additionalProperties", true)
model AnthropicModelList {
  data: AnthropicModel[] | null;
  has_more: false;
  first_id?: string;
  last_id?: string;
}

@doc("仅用于非流式协议直通. 上游成功 JSON 不做结构校验, 可为任意 JSON 值, 包括 null. 普通转换仍使用对应协议的已知响应 schema.")
union PassthroughJSON {
  unknown,
}

@doc("仅用于非流式协议直通. 按上游 Content-Type 返回原始成功字节, 不要求 JSON 或特定协议结构. 也可能没有响应 body.")
@format("binary")
scalar PassthroughBody extends string;

@doc("请求模型标识. 完整值先精确匹配 Group, 未命中且有 / 才按第一个 / 解析 channelName/modelName. 选路大小写严格且不 trim, 受限 Key 不允许实际直连. Group 白名单 supported_models 按逗号拆分但不 trim, 与 /v1/models 的 trim 不同. 实际发送的 model 被改为 GroupItem 模型或直连去前缀模型. 图片入口另有默认模型和 multipart trim 行为.")
@minLength(1)
scalar RelayRoutingModel extends string;

@doc("依据 axonhub/llm v0.0.0-20260710123537-d061ac7df6ae 的 transformer/openai/model.go:15-103 和 inbound.go:26-71. 只声明已执行的 model/messages 非空约束, 不把字段注释中的采样范围或 role 值视为入口校验. 未知字段可出现在原始请求并在符合条件的直通中保留, 转换路径不保证保留. optional 指针接受 null, 普通标量 JSON null 通常成为零值.")
@extension("additionalProperties", true)
model OpenAIChatRequest {
  `model`: RelayRoutingModel;

  @minItems(1)
  messages: OpenAIChatMessage[];

  frequency_penalty?: numeric | null;
  logprobs?: boolean | null;
  max_completion_tokens?: int64 | null;

  @doc("旧 token 上限字段, 未在本入站统一验证数值范围.")
  max_tokens?: int64 | null;

  presence_penalty?: numeric | null;
  seed?: int64 | null;
  store?: boolean | null;
  temperature?: numeric | null;
  top_logprobs?: int64 | null;
  top_p?: numeric | null;
  prompt_cache_key?: string | null;
  safety_identifier?: string | null;
  user?: string | null;
  logit_bias?:
    | {
        ...Record<OpenAIChatRequestlogit_biasAdditionalPropertyValue>;
      }
    | null;
  metadata?:
    | {
        ...Record<string | null>;
      }
    | null;

  @doc("常见 text, audio, image, 未执行枚举校验, 是否跨协议支持由转换与上游决定.")
  modalities?: (string | null)[] | null;

  reasoning_effort?: string | null;
  reasoning_budget?: int64 | null;
  reasoning_summary?: string | null;
  service_tier?: string | null;
  stop?: string | (string | null)[] | null;

  @doc("true 进入客户端 SSE 路径, null 或省略为非流式.")
  @extension("default", false)
  stream?: boolean | null;

  stream_options?: OpenAIChatStreamOptions;
  parallel_tool_calls?: boolean | null;
  tools?: OpenAIChatTool[] | null;
  tool_choice?: OpenAIChatToolChoice;
  response_format?: OpenAIChatResponseFormat;
  verbosity?: string | null;

  @extension("additionalProperties", true)
  thinking?:
    | {
        @doc("常见 enabled 或 disabled, 此 OpenAI 入站未执行枚举校验.")
        type?: string | null;
      }
    | null;
}

@doc("对话消息及响应消息. role 和 content 没有逐项 required 校验. 来源: axonhub pin transformer/openai/model.go:162-187.")
@extension("additionalProperties", true)
union OpenAIChatMessage {
  {
    @doc("常见 system, developer, user, assistant, tool, 入站未逐项执行枚举校验.")
    role?: string | null,

    content?: OpenAIChatContent,
    name?: string | null,
    refusal?: string | null,
    tool_call_id?: string | null,
    tool_calls?: OpenAIChatToolCall[] | null,
    reasoning_content?: string | null,
    reasoning?: string | null,
    annotations?: RelayAnnotation[] | null,
    audio?: OpenAIOutputAudio,
  },
  null,
}

@doc("字符串, null 或多模态内容块数组. 不代表所有块都可在跨协议转换中无损保留. 来源: axonhub pin transformer/openai/model.go:210-257.")
union OpenAIChatContent {
  string,
  null,
  OpenAIChatContentPart[],
}

@extension("additionalProperties", true)
union OpenAIChatContentPart {
  {
    @doc("常见 text, image_url, video_url, input_audio, 不执行通用枚举校验.")
    type?: string | null,

    text?: string | null,
    image_url?: OpenAIImageURL,

    @extension("additionalProperties", true)
    video_url?:
      | {
          url?: string | null,
        }
      | null,

    input_audio?: OpenAIInputAudio,
  },
  null,
}

@extension("additionalProperties", true)
union OpenAIImageURL {
  {
    @doc("URL 或 data URL, 不在此声明统一 URI 或图片格式校验.")
    url?: string | null,

    @doc("常见 auto, low, high, 入站未统一枚举校验.")
    detail?: string | null,
  },
  null,
}

@extension("additionalProperties", true)
union OpenAIInputAudio {
  {
    format?: string | null,

    @doc("通常为 base64 音频, 本 schema 不伪造格式校验.")
    data?: string | null,
  },
  null,
}

@extension("additionalProperties", true)
union OpenAIOutputAudio {
  {
    id?: string | null,
    data?: string | null,
    expires_at?: int64 | null,
    transcript?: string | null,
  },
  null,
}

@doc("引用或标注. Responses 还接受扁平 url/title 并转换到 url_citation. 来源: axonhub pin transformer/openai/model.go:190-207, transformer/openai/responses/model.go:375-413.")
@extension("additionalProperties", true)
union RelayAnnotation {
  {
    type?: string | null,
    start_index?: int64 | null,
    end_index?: int64 | null,

    @extension("additionalProperties", true)
    url_citation?:
      | {
          url?: string | null,
          title?: string | null,
        }
      | null,

    url?: string | null,
    title?: string | null,
  },
  null,
}

@extension("additionalProperties", true)
union OpenAIChatStreamOptions {
  {
    @doc("是否请求 usage, 实际流 usage 仍依赖上游和 pipeline.")
    include_usage?: boolean | null,
  },
  null,
}

@extension("additionalProperties", true)
union OpenAIChatTool {
  {
    @doc("常见 function, 未执行统一枚举校验.")
    type?: string | null,

    function?: OpenAIFunctionDefinition,
  },
  null,
}

@extension("additionalProperties", true)
union OpenAIFunctionDefinition {
  {
    name?: string | null,
    description?: string | null,

    @doc("任意 JSON 形式的函数参数 schema, 对应 json.RawMessage, 此入口不执行 schema 合规校验.")
    parameters?: unknown,

    strict?: boolean | null,
  },
  null,
}

@extension("additionalProperties", true)
union OpenAIChatToolCall {
  {
    id?: string | null,
    type?: string | null,
    index?: integer | null,

    @extension("additionalProperties", true)
    function?:
      | {
          name?: string | null,

          @doc("通常为 JSON 字符串, 此 Chat 结构没有 Responses Item 的任意 JSON arguments 兼容解析.")
          arguments?: string | null,
        }
      | null,

    @extension("additionalProperties", true)
    extra_content?:
      | {
          @extension("additionalProperties", true)
          google?:
            | {
                thought_signature?: string | null,
              }
            | null,
        }
      | null,

    @extension("additionalProperties", true)
    extra_fields?:
      | {
          @extension("additionalProperties", true)
          extra_content?:
            | {
                @extension("additionalProperties", true)
                google?:
                  | {
                      thought_signature?: string | null,
                    }
                  | null,
              }
            | null,
        }
      | null,
  },
  null,
}

@doc("字符串或命名工具对象, optional null 被忽略. 常见 auto, none, required, 本入站未执行这些枚举. 来源: axonhub pin transformer/openai/model.go:413-455.")
union OpenAIChatToolChoice {
  string,
  null,
  {
    type?: string | null,

    @extension("additionalProperties", true)
    function?:
      | {
          name?: string | null,
        }
      | null,
  },
}

@extension("additionalProperties", true)
union OpenAIChatResponseFormat {
  {
    @doc("常见 text, json_object, json_schema, 本入口无统一枚举校验.")
    type?: string | null,

    @doc("任意 JSON, 对应 json.RawMessage, 不保证上游执行所有约束.")
    json_schema?: unknown,
  },
  null,
}

@doc("普通转换的 Chat Completions 响应或流 chunk 已知字段. 为覆盖符合条件的协议直通, 不强制所有上游字段齐全. usage 可为 null, choices 可为空或 null. 来源: axonhub pin transformer/openai/model.go:304-328.")
@extension("additionalProperties", true)
model OpenAIChatResponse {
  id?: string;

  @doc("非流式常见 chat.completion, 流 chunk 常见 chat.completion.chunk, 直通不强制常量.")
  object?: string;

  created?: int64;

  @doc("可能是实际上游模型或上游返回标识, 不保证等于请求的 Group 名称.")
  `model`?: string;

  choices?: OpenAIChatChoice[] | null;
  usage?: OpenAIChatUsage;
  system_fingerprint?: string;
  service_tier?: string;
  citations?: string[] | null;

  @doc("此依赖 Response.Error 使用 OpenAIError 包装, 原生或直通响应还可能有其他扩展形态.")
  error?: OpenAIProtocolError | null;
}

@extension("additionalProperties", true)
model OpenAIChatChoice {
  index?: integer;
  message?: OpenAIChatMessage;
  delta?: OpenAIChatMessage;
  finish_reason?: string | null;

  @extension("additionalProperties", true)
  logprobs?:
    | {
        content?: OpenAITokenLogprob[] | null;
      }
    | null;
}

@extension("additionalProperties", true)
model OpenAITokenLogprob {
  token?: string;
  logprob?: numeric;
  bytes?: integer[] | null;
  top_logprobs?:
    | {
        token?: string;
        logprob?: numeric;
        bytes?: integer[] | null;
      }[]
    | null;
}

@doc("普通转换时按此字段输出, 未强制上游 usage 或扩展完整. 来源: axonhub pin transformer/openai/usage.go:6-30.")
@extension("additionalProperties", true)
union OpenAIChatUsage {
  {
    prompt_tokens?: int64,
    completion_tokens?: int64,
    total_tokens?: int64,
    cached_tokens?: int64,

    @extension("additionalProperties", true)
    prompt_tokens_details?:
      | {
          audio_tokens?: int64,
          cached_tokens?: int64,
          write_cached_tokens?: int64,
        }
      | null,

    @extension("additionalProperties", true)
    completion_tokens_details?:
      | {
          audio_tokens?: int64,
          reasoning_tokens?: int64,
          accepted_prediction_tokens?: int64,
          rejected_prediction_tokens?: int64,
        }
      | null,
  },
  null,
}

@doc("依据 axonhub pin transformer/openai/responses/model.go:89-159 和 inbound.go:38-63. input 没有必填或 minItems 校验, 省略可通过入站但部分出站要求非空 messages. background 字段被解析但普通转换没有映射, prompt/conversation 未作为本请求结构字段实现, 未知字段仅在直通或特定 provider extensions 下可能保留. 不声明未执行的采样或枚举范围.")
@extension("additionalProperties", true)
model ResponsesRequest {
  `model`: RelayRoutingModel;
  instructions?: string | null;
  temperature?: numeric | null;
  input?: ResponsesInput;
  tools?: ResponsesTool[] | null;
  parallel_tool_calls?: boolean | null;

  @doc("普通转换路径未映射, 同协议直通可能保留. 不承诺 Octopus 的异步任务或查询能力.")
  background?: boolean | null;

  @extension("default", false)
  stream?: boolean | null;

  store?: boolean | null;
  service_tier?: string | null;
  safety_identifier?: string | null;
  user?: string | null;
  metadata?:
    | {
        ...Record<string | null>;
      }
    | null;
  max_output_tokens?: int64 | null;
  max_tool_calls?: int64 | null;
  text?: ResponsesTextOptions;
  include?: (string | null)[] | null;

  @doc("可以携带, 不表示 Octopus 持久化或恢复对应会话, 跨协议保留不保证.")
  previous_response_id?: string | null;

  prompt_cache_key?: string | null;
  prompt_cache_retention?: string | null;
  reasoning?: ResponsesReasoning;

  @extension("additionalProperties", true)
  stream_options?:
    | {
        include_obfuscation?: boolean | null;
      }
    | null;

  tool_choice?: ResponsesToolChoice;
  truncation?: string | null;
  top_logprobs?: int64 | null;
  top_p?: numeric | null;
}

@doc("字符串或开放 Item 数组, input 本身可省略. JSON null 在依赖字符串解码中成为空文本. 没有输入非空校验. 来源: axonhub pin transformer/openai/responses/model.go:341-373.")
union ResponsesInput {
  string,
  ResponsesItem[],
  null,
}

@doc("Responses 输入或输出的开放联合项. 已知 type 包括 message, input_text, input_image, output_text, function_call, function_call_output, custom_tool_call, custom_tool_call_output, image_generation_call, reasoning, compaction, compaction_summary 等, 不按注释添加 type/role 枚举或逐项必填. function_call_output/custom_tool_call_output 在入站转换时要求 output 非 null, 此规则在描述中限定. 不支持的项在跨协议转换中可能跳过, 特定同协议 provider extensions 可保留原始片段. 来源: axonhub pin transformer/openai/responses/model.go:512-674, inbound.go:487-629, request_extensions.go.")
@extension("additionalProperties", true)
union ResponsesItem {
  {
    id?: string | null,
    type?: string | null,
    annotations?: RelayAnnotation[] | null,

    @doc("常见 system, user, assistant, developer, 无统一枚举校验.")
    role?: string | null,

    content?: ResponsesInput,
    status?: string | null,

    @doc("URL 或 data URL.")
    image_url?: string | null,

    detail?: string | null,
    text?: string | null,
    background?: string | null,
    output_format?: string | null,
    quality?: string | null,
    size?: string | null,
    result?: string | null,
    call_id?: string | null,
    name?: string | null,
    `namespace`?: string | null,

    @doc("输入接受字符串或任意 JSON 值, 非字符串在自定义解码中压缩为 JSON 字符串. 普通输出为字符串.")
    arguments?: unknown,

    @doc("custom_tool_call 的自由文本输入, 与顶层 input 的类型不同.")
    input?: string | null,

    output?: ResponsesInput,
    summary?:
      | (
          | {
              text?: string | null,
              type?: string | null,
            }
          | null)[]
      | null,
    reasoning_content?:
      | (
          | {
              text?: string | null,
              type?: string | null,
            }
          | null)[]
      | null,
    encrypted_content?: string | null,
    action?:
      | string
      | null
      | {
          type?: string | null,
          query?: string | null,
          queries?: (string | null)[] | null,
          sources?:
            | (
                | {
                    type?: string | null,
                    url?: string | null,
                    title?: string | null,
                  }
                | null)[]
            | null,
        },
    created_by?: string | null,
  },
  null,
}

@doc("已知结构表达 function, image_generation, custom, web_search. 其他 type 不声明枚举限制, 普通跨协议转换可能跳过, 同协议出站有部分原始片段恢复能力. 来源: axonhub pin transformer/openai/responses/model.go:25-65.")
@extension("additionalProperties", true)
union ResponsesTool {
  {
    type?: string | null,
    name?: string | null,
    description?: string | null,

    @extension("additionalProperties", true)
    parameters?: {} | null,

    strict?: boolean | null,

    @extension("additionalProperties", true)
    format?:
      | {
          type?: string | null,
          syntax?: string | null,
          definition?: string | null,
        }
      | null,

    @extension("additionalProperties", true)
    filters?:
      | {
          allowed_domains?: (string | null)[] | null,
        }
      | null,

    @extension("additionalProperties", true)
    user_location?:
      | {
          type?: string | null,
          city?: string | null,
          country?: string | null,
          region?: string | null,
          timezone?: string | null,
        }
      | null,

    action?: string | null,
    background?: string | null,
    input_fidelity?: string | null,

    @extension("additionalProperties", true)
    input_image_mask?: {} | null,

    `model`?: string | null,
    moderation?: string | null,
    output_compression?: int64 | null,
    output_format?: string | null,
    partial_images?: int64 | null,
    quality?: string | null,
    size?: string | null,
  },
  null,
}

@doc("字符串或开放工具选择对象. 未按注释添加枚举或必填. tools 数组选择可由特定同协议 provider extensions 保留. 来源: axonhub pin transformer/openai/responses/model.go:182-239.")
union ResponsesToolChoice {
  string,
  null,
  {
    mode?: string | null,
    type?: string | null,
    name?: string | null,
    tools?:
      | (
          | {
              type?: string | null,
              name?: string | null,
            }
          | null)[]
      | null,
  },
}

@extension("additionalProperties", true)
union ResponsesTextOptions {
  {
    verbosity?: string | null,

    @extension("additionalProperties", true)
    format?:
      | {
          type?: string | null,
          name?: string | null,
          description?: string | null,

          @doc("任意 JSON schema 内容, 此入口不统一验证约束.")
          schema?: unknown,

          strict?: boolean | null,
        }
      | null,
  },
  null,
}

@doc("不把依赖注释中的 effort/summary 值或 token 范围当作统一入站校验.")
@extension("additionalProperties", true)
union ResponsesReasoning {
  {
    effort?: string | null,
    generate_summary?: string | null,
    summary?: string | null,
    max_tokens?: int64 | null,
  },
  null,
}

@doc("Responses 非流式已知输出, 普通转换主要生成 object, id, created_at, model, output, status 及可用 usage. 其余已知扩展可能只由原生或直通上游提供, 不保证转换完整往返. 为兼容直通未将所有字段设为 required. 来源: axonhub pin transformer/openai/responses/model.go:769-858.")
@extension("additionalProperties", true)
model ResponsesResponse {
  @doc("普通转换为 response, 不强制直通常量.")
  object?: string;

  id?: string;

  @extension("additionalProperties", true)
  error?:
    | {
        type?: string;
        code?: string;
        message?: string;
      }
    | null;

  created_at?: int64;
  `model`?: string;
  output?: ResponsesItem[] | null;

  @doc("常见 completed, failed, in_progress, canceled, queued, incomplete, 实际状态由转换或直通决定.")
  status?: string | null;

  @extension("additionalProperties", true)
  incomplete_details?:
    | {
        reason?: string;
      }
    | null;

  instructions?: string | null;
  metadata?:
    | {
        ...Record<string>;
      }
    | null;
  parallel_tool_calls?: boolean | null;
  temperature?: numeric | null;
  tool_choice?: ResponsesToolChoice;
  tools?: ResponsesTool[] | null;
  top_p?: numeric | null;
  background?: boolean | null;

  @extension("additionalProperties", true)
  conversation?:
    | {
        id?: string;
      }
    | null;

  max_output_tokens?: int64 | null;
  max_tool_calls?: int64 | null;
  previous_response_id?: string | null;

  @extension("additionalProperties", true)
  prompt?:
    | {
        id?: string;
        version?: string | null;
        variables?:
          | {
              ...Record<string>;
            }
          | null;
      }
    | null;

  prompt_cache_key?: string | null;
  prompt_cache_retention?: string | null;
  reasoning?: ResponsesReasoning;
  safety_identifier?: string | null;
  service_tier?: string | null;
  text?: ResponsesTextOptions;
  top_logprobs?: int64 | null;
  truncation?: string | null;
  usage?: ResponsesUsage;
  user?: string | null;
}

@doc("来源: axonhub pin transformer/openai/responses/usage.go:7-17. 可无 usage, 不强制非负值或额外字段.")
@extension("additionalProperties", true)
union ResponsesUsage {
  {
    input_tokens?: int64,

    @extension("additionalProperties", true)
    input_tokens_details?:
      | {
          cached_tokens?: int64,
        }
      | null,

    output_tokens?: int64,

    @extension("additionalProperties", true)
    output_tokens_details?:
      | {
          reasoning_tokens?: int64,
        }
      | null,

    total_tokens?: int64,
  },
  null,
}

@doc("单个 SSE data 解码后的已知事件结构, 不是整个 text/event-stream 响应的 JSON schema. 来源: axonhub pin transformer/openai/responses/stream_event.go:69-129.")
@extension("additionalProperties", true)
model ResponsesStreamEvent {
  @doc("生命周期, output item, content part, text, tool arguments, reasoning summary, image generation 或 error 等事件, 不限制扩展类型.")
  type?: string;

  sequence_number?: integer;
  response?: ResponsesResponse;
  output_index?: integer;
  item?: ResponsesItem;
  item_id?: string | null;
  content_index?: integer | null;

  @extension("additionalProperties", true)
  part?:
    | {
        type?: string;
        text?: string;
        annotations?: RelayAnnotation[] | null;
        refusal?: string | null;
      }
    | null;

  delta?: string;
  text?: string;
  name?: string;
  `namespace`?: string;
  call_id?: string;
  arguments?: string;
  input?: string;
  summary_index?: integer | null;
  partial_image_b64?: string;
  partial_image_index?: integer | null;
  code?: string;
  message?: string;
  param?: string | null;
}

@doc("依据 axonhub pin transformer/anthropic/model.go:11-102 和 inbound.go:28-119. 执行 model/messages 非空和 max_tokens>0 校验. system 数组只允许 text 类型, thinking enabled 要求正 budget_tokens, adaptive 仅对非空 output_config.effort 检查 low, medium, high, xhigh, max. 其他采样范围, role, cache_control 注释枚举不当作执行校验. 允许未知扩展, 跨协议转换不保证全部原生工具和多模态内容无损.")
@extension("additionalProperties", true)
model AnthropicMessagesRequest {
  `model`: RelayRoutingModel;

  @minItems(1)
  messages: AnthropicMessageParam[];

  @minValue(1)
  max_tokens: int64;

  @doc("已知 body 字段, 不等同于强制必填的 Header.")
  anthropic_version?: string | null;

  anthropic_beta?: (string | null)[] | null;
  temperature?: numeric | null;
  top_k?: int64 | null;
  top_p?: numeric | null;

  @extension("additionalProperties", true)
  metadata?:
    | {
        user_id?: string | null;
      }
    | null;

  service_tier?: string | null;
  stop_sequences?: (string | null)[] | null;
  system?: string | null | AnthropicSystemPromptPart[];
  thinking?: AnthropicThinking;

  @extension("additionalProperties", true)
  output_config?:
    | {
        @doc("仅 thinking.type=adaptive 且 effort 非空时校验 low, medium, high, xhigh, max. 其他情况下不伪造此枚举校验.")
        effort?: string | null;
      }
    | null;

  tools?: AnthropicTool[] | null;
  tool_choice?: AnthropicToolChoice;

  @extension("default", false)
  stream?: boolean | null;

  cache_control?: AnthropicCacheControl;
}

@doc("消息元素没有逐项 role/content 必填或 role 枚举验证. content 若显式出现为 null, 自定义解码会报错; 省略 content 不触发其解码器. 来源: axonhub pin transformer/anthropic/model.go:264-348.")
@extension("additionalProperties", true)
union AnthropicMessageParam {
  {
    @doc("常见 user 或 assistant, 未执行 role 枚举校验.")
    role?: string | null,

    content?: AnthropicContent,
  },
  null,
}

@doc("string 或内容块数组, 显式 null 不合法. tool_result 嵌套内容遵循相同结构, provider-native 数组的部分原始字段有恢复能力, 不保证跨协议完整保留.")
union AnthropicContent {
  string,
  AnthropicContentBlock[],
}

@doc("text, image, thinking, redacted_thinking, tool_use, server_tool_use, tool_result 或 provider-native 工具结果等开放内容块. 不给 type 和逐项字段添加未经执行的统一必填/枚举. 来源: axonhub pin transformer/anthropic/model.go:351-424.")
@extension("additionalProperties", true)
union AnthropicContentBlock {
  {
    type?: string | null,
    text?: string | null,
    citations?:
      | (
          | {
              type?: string | null,
              url?: string | null,
              title?: string | null,
              encrypted_index?: string | null,
              cited_text?: string | null,
            }
          | null)[]
      | null,
    thinking?: string | null,
    signature?: string | null,
    data?: string | null,
    source?: AnthropicImageSource,
    id?: string | null,
    name?: string | null,

    @doc("任意 JSON, 对应原生工具输入 json.RawMessage.")
    input?: unknown,

    cache_control?: AnthropicCacheControl,
    tool_use_id?: string | null,

    @doc("optional 指针 null 被忽略, 非 null 使用 string 或块数组.")
    content?: AnthropicContent | null,

    is_error?: boolean | null,
    url?: string | null,
    title?: string | null,
    encrypted_content?: string | null,
    page_age?: string | null,

    @doc("任意 JSON 原生工具调用来源信息.")
    caller?: unknown,
  },
  null,
}

@doc("仅 type=text 是执行过的逐项规则, text 没有另行非空或 required 校验.")
@extension("additionalProperties", true)
model AnthropicSystemPromptPart {
  type: "text";
  text?: string | null;
  cache_control?: AnthropicCacheControl;
}

@doc("已知 source 字段, 常见 type=base64 或 url, media_type 常见 PNG/JPEG/GIF/WebP. 本消息入口未统一执行枚举或 base64 校验, 不把图片上传路由的规则套用到此处.")
@extension("additionalProperties", true)
union AnthropicImageSource {
  {
    type?: string | null,
    media_type?: string | null,
    data?: string | null,
    url?: string | null,
  },
  null,
}

@extension("additionalProperties", true)
union AnthropicCacheControl {
  {
    @doc("常见 ephemeral, 不把 validate tag 视为已执行枚举校验.")
    type?: string | null,

    @doc("常见 5m 或 1h, 本入站未统一校验.")
    ttl?: string | null,
  },
  null,
}

@doc("自定义工具和原生 web_search 等工具. name/input_schema 没有统一 required 校验, 不声称 allowed_domains 与 blocked_domains 互斥被入口执行. 来源: axonhub pin transformer/anthropic/model.go:207-235.")
@extension("additionalProperties", true)
union AnthropicTool {
  {
    type?: string | null,
    name?: string | null,
    description?: string | null,

    @doc("任意 JSON 工具参数 schema, 此入口不执行完整 schema 验证.")
    input_schema?: unknown,

    cache_control?: AnthropicCacheControl,
    max_uses?: int64 | null,
    strict?: boolean | null,
    allowed_domains?: (string | null)[] | null,
    blocked_domains?: (string | null)[] | null,

    @extension("additionalProperties", true)
    user_location?:
      | {
          city?: string | null,
          country?: string | null,
          region?: string | null,
          timezone?: string | null,

          @doc("常见 approximate, 无入口枚举校验.")
          type?: string | null,
        }
      | null,
  },
  null,
}

// TypeSpec 的联合类型保留基础结构, JSON Schema 条件补充跨字段校验, 两者共同约束同一载荷.
@doc("非 null 对象执行 type 枚举, enabled 执行正 budget_tokens. adaptive 对非空 output_config.effort 的额外规则见请求描述. 来源: axonhub pin transformer/anthropic/inbound.go:79-101.")
@extension("additionalProperties", true)
@extension(
  "allOf",
  #[
    #{
      `if`: #{
        type: "object",
        required: #["type"],
        properties: #{ type: #{ `const`: "enabled" } },
      },
      then: #{
        required: #["budget_tokens"],
        properties: #{ budget_tokens: #{ type: "integer", minimum: 1 } },
      },
    }
  ]
)
union AnthropicThinking {
  {
    type: "enabled" | "disabled" | "adaptive",
    budget_tokens?: int64 | null,

    @doc("常见 summarized 或 omitted, 未执行枚举校验.")
    display?: string | null,
  },
  null,
}

// TypeSpec 的联合类型保留基础结构, JSON Schema 条件补充跨字段校验, 两者共同约束同一载荷.
@doc("非 null 对象执行 type 枚举, tool 要求 name 非空. 不 trim name. 来源: axonhub pin transformer/anthropic/inbound.go:104-117.")
@extension("additionalProperties", true)
@extension(
  "allOf",
  #[
    #{
      `if`: #{
        type: "object",
        required: #["type"],
        properties: #{ type: #{ `const`: "tool" } },
      },
      then: #{
        required: #["name"],
        properties: #{ name: #{ type: "string", minLength: 1 } },
      },
    }
  ]
)
union AnthropicToolChoice {
  {
    type: "auto" | "none" | "any" | "tool",
    disable_parallel_tool_use?: boolean | null,
    name?: string | null,
  },
  null,
}

@doc("来源: axonhub pin transformer/anthropic/usage.go:7-33. 普通转换可能重算 input_tokens 的缓存部分, 不保证直通及转换数值含义完全相同.")
@extension("additionalProperties", true)
union AnthropicUsage {
  {
    input_tokens?: int64,
    output_tokens?: int64,
    cache_creation_input_tokens?: int64,
    cache_read_input_tokens?: int64,

    @extension("additionalProperties", true)
    cache_creation?:
      | {
          ephemeral_5m_input_tokens?: int64,
          ephemeral_1h_input_tokens?: int64,
        }
      | null,

    service_tier?: string,
    cached_tokens?: int64,
  },
  null,
}

@doc("非流式 Messages 及 message_start.message 的已知输出. 普通转换包含 id/type/role/content/model, 为直通兼容不强制所有字段. 来源: axonhub pin transformer/anthropic/model.go:519-544.")
@extension("additionalProperties", true)
model AnthropicMessageResponse {
  id?: string;
  type?: string;
  role?: string;
  content?: AnthropicContentBlock[] | null;
  `model`?: string;

  @doc("常见 end_turn, max_tokens, stop_sequence, tool_use, pause_turn, refusal, 不限制扩展.")
  stop_reason?: string | null;

  stop_sequence?: string | null;
  usage?: AnthropicUsage;
}

@doc("单个 SSE data 的已知事件结构, 整段响应仍为 text/event-stream string. 来源: axonhub pin transformer/anthropic/model.go:446-466.")
@extension("additionalProperties", true)
model AnthropicStreamEvent {
  @doc("message_start, message_delta, message_stop, content_block_start/delta/stop 等, 保留扩展类型.")
  type?: string;

  message?: AnthropicMessageResponse;
  index?: int64 | null;
  content_block?: AnthropicContentBlock;
  delta?: AnthropicStreamDelta;
  usage?: AnthropicUsage;
}

@extension("additionalProperties", true)
union AnthropicStreamDelta {
  {
    @doc("常见 text_delta, input_json_delta, citations_delta, thinking_delta, signature_delta.")
    type?: string | null,

    text?: string | null,
    partial_json?: string | null,

    @extension("additionalProperties", true)
    citation?:
      | {
          type?: string,
          url?: string,
          title?: string,
          encrypted_index?: string | null,
          cited_text?: string | null,
        }
      | null,

    thinking?: string | null,
    signature?: string | null,
    stop_reason?: string | null,
    stop_sequence?: string | null,
  },
  null,
}

@doc("依据 axonhub pin transformer/openai/embedding.go:13-19 和 embedding_inbound.go:26-136. 无正式 stream 字段, 未知字段可保留于直通, 不能因此声明 SSE 支持. 不执行 dimensions 正数或 encoding_format 枚举校验.")
@extension("additionalProperties", true)
model OpenAIEmbeddingRequest {
  `model`: RelayRoutingModel;
  input: EmbeddingInput;

  @doc("常见 float 或 base64, 无入口枚举校验.")
  encoding_format?: string | null;

  dimensions?: integer | null;
  user?: string | null;
}

@doc("四种输入联合. 字符串及 string[] 每项 trim 后不得为空, schema 的 minLength 仅表达基础非空, 全空白仍由实现拒绝. 整数数组不要求 token 非负, 二维数组每项非空. Gemini/Doubao 的文本转换不保证支持 token 数组. 来源: axonhub pin embedding.go:15-76, transformer/openai/embedding_inbound.go:88-135.")
union EmbeddingInput {
  EmbeddingInputVariant1Value,
  EmbeddingInputVariant2Value,
  EmbeddingInputVariant3Value,
  EmbeddingInputVariant4Value,
}

@doc("OpenAI 兼容非流式向量响应. 普通转换输出 object/data/model/usage, 直通扩展允许. Gemini 转换可能输出空 model 和零 usage, 不保证真实模型名称或计数. 来源: axonhub pin transformer/openai/embedding.go:21-36, embedding_inbound.go:139-198.")
@extension("additionalProperties", true)
model OpenAIEmbeddingResponse {
  object?: string;
  data?: EmbeddingData[] | null;
  `model`?: string;

  @extension("additionalProperties", true)
  usage?:
    | {
        prompt_tokens?: int64;
        total_tokens?: int64;
      }
    | null;
}

@extension("additionalProperties", true)
model EmbeddingData {
  object?: string;
  embedding?: numeric[] | EmbeddingDataembeddingVariant2Value | null;
  index?: integer;
}

@doc("依据 axonhub pin transformer/openai/image_inbound.go:54-69,199-250. prompt 非空但不 trim. 可选普通标量的 JSON null 被当作零值, model null/空/省略默认 dall-e-2. 不加未执行的 n, quality, size, compression 或格式范围校验. 参数保留受出站及实际模型限制.")
@extension("additionalProperties", true)
model ImageGenerationRequest {
  @minLength(1)
  prompt: string;

  @doc("空字符串也使用 dall-e-2, 然后按完整模型执行 Group 优先或指定渠道直连规则.")
  @extension("default", "dall-e-2")
  `model`?: string | null;

  n?: int64 | null;
  quality?: string | null;
  response_format?: string | null;
  size?: string | null;
  style?: string | null;
  user?: string | null;
  background?: string | null;
  output_format?: string | null;
  output_compression?: int64 | null;
  moderation?: string | null;
  partial_images?: int64 | null;

  @doc("true 明确返回 400, null 被忽略并归零, 不支持 SSE.")
  @extension("default", false)
  stream?: false | null;
}

@doc("multipart 文件 part, 必须有非空 filename. MIME 支持 image/png, image/jpeg, image/gif, image/webp, 缺失 MIME 时尝试内容探测. 默认单文件限制 50 MiB, AXONHUB_MAX_IMAGE_FILE_SIZE 可调整, 因此不写固定 maxLength. 不统一验证图片尺寸或 mask 透明度.")
@format("binary")
scalar ImageFile extends string;

@doc("至少 image 或 image[] 提供非空文件集合, 支持多张图片和可选 mask. 总文件 part 数限制包括 mask/未知文件, 不将其误写为仅图片数量. 所有文本参数是 multipart 字符串. 可选 int64 参数解析失败成为 nil, 不触发数值范围校验. 来源: axonhub pin transformer/openai/image_inbound.go:252-321,389-483,536-549.")
@extension("additionalProperties", true)
@extension("anyOf", #[#{ required: #["image"] }, #{ required: #["image[]"] }])
model ImageEditRequest {
  @doc("单个或重复 image 文件 part.")
  image?: ImageFile | ImageEditRequestimageVariant2Value;

  @doc("重复 image[] 文件 part.")
  @minItems(1)
  `image[]`?: ImageFile[];

  mask?: ImageFile;

  @doc("trim 后必须非空, minLength 不替代全空白检查.")
  @minLength(1)
  prompt: string;

  @doc("先 trim, 空或省略用 dall-e-2, 再执行 Group 优先和直连规则.")
  @extension("default", "dall-e-2")
  `model`?: string;

  @doc("十进制 int64 文本, 非法或空成为 nil, 无范围校验.")
  n?: string;

  size?: string;
  quality?: string;
  response_format?: string;
  user?: string;
  background?: string;
  output_format?: string;

  @doc("十进制 int64 文本, 非法或空成为 nil.")
  output_compression?: string;

  input_fidelity?: string;

  @doc("十进制 int64 文本, 非法或空成为 nil.")
  partial_images?: string;

  @doc("trim 后大小写不敏感等于 true 时返回 400. 其余字符串未作为严格布尔值校验, 最终仍为非流式.")
  @extension("default", "false")
  stream?: string;
}

@doc("image 或 image[] 合计恰好一个图片文件. 此 schema 拒绝同时提供两种文件字段. 非空 prompt 不允许, mask 即使被解析也不参与内部 variation. 不支持 SSE. 来源: axonhub pin transformer/openai/image_inbound.go:323-371.")
@extension("not", #{ required: #["image", "image[]"] })
@extension("additionalProperties", true)
@extension("anyOf", #[#{ required: #["image"] }, #{ required: #["image[]"] }])
model ImageVariationRequest {
  image?: ImageFile | ImageVariationRequestimageVariant2Value;

  @minItems(1)
  @maxItems(1)
  `image[]`?: ImageFile[];

  @doc("先 trim, 空或省略使用默认模型, 再执行 Group 优先和直连规则.")
  @extension("default", "dall-e-2")
  `model`?: string;

  @doc("十进制 int64 文本, 非法或空成为 nil, 无范围校验.")
  n?: string;

  size?: string;
  response_format?: string;
  user?: string;

  @doc("只能省略, 空字符串或全空白, trim 后非空返回 400. 不声明 prompt 必填.")
  prompt?: string;

  @doc("trim 后大小写不敏感等于 true 时返回 400, 其他字符串未严格校验, 最终非流式.")
  @extension("default", "false")
  stream?: string;
}

@doc("三个 Images 路由的非流式已知输出. 普通转换包含 created 和 data, 直通成功扩展保持开放, 不保证 URL 和 base64 两种字段同时存在. 来源: axonhub pin transformer/openai/image_outbound.go 的 ImagesResponse/ImageData, image_inbound.go:120-183.")
@extension("additionalProperties", true)
model ImageResponse {
  created?: int64;
  data?: ImageData[] | null;
  background?: string;
  output_format?: string;
  quality?: string;
  size?: string;
  usage?: ImageUsage;
}

@extension("additionalProperties", true)
model ImageData {
  @doc("base64 图片字符串, 此 schema 不声称执行编码校验.")
  b64_json?: string;

  url?: string;
  revised_prompt?: string;
}

@extension("additionalProperties", true)
union ImageUsage {
  {
    input_tokens?: int64,
    output_tokens?: int64,
    total_tokens?: int64,

    @extension("additionalProperties", true)
    input_tokens_details?:
      | {
          image_tokens?: int64,
          text_tokens?: int64,
          cached_tokens?: int64,
        }
      | null,

    @extension("additionalProperties", true)
    output_tokens_details?:
      | {
          reasoning_tokens?: int64,
        }
      | null,
  },
  null,
}

@doc("指定渠道直连可识别上游 HTTP 错误转换后的 OpenAI 体. 本地错误和 Group 最终失败不使用此体. 非逐字透传原始错误或全部 Header. 来源: internal/relay/direct_channel.go:90-109, axonhub pin transformer/openai/inbound.go:202-246, model.go:351-354, 根级 model.go:871-877.")
@extension("additionalProperties", true)
model OpenAIProtocolError {
  @extension("additionalProperties", true)
  error: {
    message: string;
    type: string;
    code?: string;
    param?: string;
    request_id?: string;
  };
}

@doc("指定渠道直连可识别上游 HTTP 错误转换后的 Responses 体. 本地错误和 Group 最终失败使用 ErrorResponse. 来源: axonhub pin transformer/openai/responses/inbound.go:90-164.")
@extension("additionalProperties", true)
model ResponsesProtocolError {
  @extension("additionalProperties", true)
  error: {
    message: string;
    type: string;
    code?: string;
  };
}

@doc("指定渠道直连可识别上游 HTTP 错误转换后的 Anthropic 体. 顶层 type 来自统一 Detail.Type, 可能为 api_error 等, 不固定为 error, 空 type 可省略. request_id 可为空字符串. 本地错误和 Group 最终失败仍用 ErrorResponse. 来源: axonhub pin transformer/anthropic/inbound.go:188-202, model.go:546-558.")
@extension("additionalProperties", true)
model AnthropicProtocolError {
  type?: string;
  request_id: string;

  @extension("additionalProperties", true)
  error: {
    type: string;
    message: string;
  };
}

union OpenAIChatRequestlogit_biasAdditionalPropertyValue {
  int64,
  null,
}

@minLength(1)
scalar EmbeddingInputVariant1Value extends string;

@minItems(1)
model EmbeddingInputVariant2Value is EmbeddingInputVariant1Value[];

@minItems(1)
model EmbeddingInputVariant3Value
  is DBImportResultrows_affectedAdditionalPropertyValue[];

@minItems(1)
model EmbeddingInputVariant4Value is EmbeddingInputVariant3Value[];

@doc("通常为 base64, 不添加未经执行的编码约束.")
scalar EmbeddingDataembeddingVariant2Value extends string;

@minItems(1)
model ImageEditRequestimageVariant2Value is ImageFile[];

@minItems(1)
@maxItems(1)
model ImageVariationRequestimageVariant2Value is ImageFile[];
