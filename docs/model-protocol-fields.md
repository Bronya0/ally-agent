# Ally 三协议请求字段

实现核对：2026-10-25。本文集中维护 **Ally 当前实际发送的字段、条件与回放契约**；以最终序列化请求为口径。供应商支持某字段，不代表 Ally 正在发送它。

| 要查什么 | 文档 |
|---|---|
| 当前请求体、请求头、思考映射、缓存键 | 本文 |
| 六家兼容平台公开的字段及抓取来源 | [供应商字段资料](provider-api-fields.md)（资料快照，不是实现清单） |
| 提示词冻结、追加历史、用量统计 | [缓存实现与观测](agent-prompt-cache.md) |
| ChatGPT 登录和凭据生命周期 | [ChatGPT Auth](chatgpt-auth.md) |
| 订阅零缓存实测、限制与采集方法 | [订阅缓存排查](chatgpt-cache-diagnostics.md) |

“省略”表示 Ally 不主动设置该请求字段，不表示服务端禁用该能力；服务端可能采用默认值。历史内容中的同名字段按回放契约处理。

<a id="chat"></a>
## 1. Chat Completions

入口：`streamOpenAIChat`（[适配器](../internal/app/prov_adapter_chat.go)）；最终补写：`patchChatRequestFields`（[请求改写](../internal/app/prov_reasoning.go)）。

| 请求字段 | 当前发送规则 |
|---|---|
| `model` | 当前模型 ID |
| `messages` | 系统提示、历史与当前输入；内部消息经适配后发送 |
| `stream:true` | SDK 流式接口补入 |
| `stream_options.include_usage:true` | 默认请求流尾用量；端点明确拒绝时重试一次不带该字段，并记住兼容结果 |
| `max_tokens` / `max_completion_tokens` | 按 Token 参数设置与模型选择二者之一，不同时发送 |
| `reasoning_effort` | `reasoningWireForAdapter` 映射后非空时发送；auto 省略 |
| `tools` | 有工具时发送；函数包含 `type:function`、`function.name`、非空 `description`、`parameters`；当前普通函数未主动开启 strict |
| `prompt_cache_key` | 按[缓存键规则](#cache)发送 |
| `store:false` | 仅官方 OpenAI 端点；与是否发送缓存键无关 |
| `thinking:{type:"disabled"}` / `enable_thinking:false` | 非官方端点的 off 档位按供应商选择；千问使用后者，其他支持关闭的兼容族使用前者；不能关闭或没有该字段的族由统一映射处理 |

消息内部的 `role`、`content`、`tool_calls`、`tool_call_id` 与多模态内容按消息类型发送。助手工具调用及工具结果消息缺少 `content` 时补显式空串。非官方端点且非 off 时，助手消息缺少思考字段则按检测到的方言补空串；已有思考文本及按工具调用 ID 保存的 `reasoning_details` 继续回放。不要把“空字符串”改成“不存在的字段”。

当前不主动发送 `tool_choice`、`parallel_tool_calls`、`temperature`、`top_p`、`n`、`stop`、`seed`、`response_format`、`verbosity`、`service_tier`、`metadata`、`user`、`logprobs`、`top_logprobs`、`prompt_cache_retention` 等可选项。

<a id="responses"></a>
## 2. Responses

公共构造：`buildOpenAIResponsesRequest`；最终请求：`newOpenAIResponsesSSEStream`（[适配器](../internal/app/prov_adapter_responses.go)）。登录订阅额外经过 `chatGPTResponsesRequest`（[SIWC 兼容层](../internal/app/prov_chatgpt.go)）。

| 请求字段 | 普通 API-key / 兼容端点 | ChatGPT 登录订阅 |
|---|---|---|
| `model` | 当前模型 ID | 同左 |
| `input` | 输入数组，包含所需历史 | 必须为数组，携带完整所需上下文 |
| `instructions` | 系统提示非空时发送 | 同左；输入条目的 system 角色另转换成 developer |
| `stream:true` | 发送 | 强制发送 |
| `max_output_tokens` | 发送经下限校正的配置输出上限 | 兼容层删除，不发送 |
| `reasoning.effort` | 统一映射非空时发送；auto 省略 | 同左 |
| `tools` | 有函数工具时发送 | 函数/custom 工具封装在 `ally` namespace 中；回放的 function_call 补相同 namespace |
| 函数工具 `type/name/parameters/description` | type 为 function；description 非空时发送 | namespace 内同样处理 |
| 函数工具 `strict:false` | 显式保留，避免省略后 schema 被默认归一到严格模式 | 同左 |
| `store:false` | 仅官方 OpenAI 端点发送 | 强制发送 |
| `include:["reasoning.encrypted_content"]` | 仅官方 OpenAI 端点发送，保留旧模型/路由兼容提示 | 保留 |
| `prompt_cache_key` | 按[缓存键规则](#cache)发送 | 同一规则，不按认证方式另改会话身份 |

当前公共构造器 **不发送**：

- `reasoning.summary`；订阅兼容层还会防御性删除此请求字段。
- `tool_choice:auto`、`parallel_tool_calls:true`，沿用服务端默认行为。
- 自动附加的 `image_generation` 工具及其 `output_format:png`；现有响应解析能力不因此删除。
- `reasoning.context`、`temperature`、`top_p`、`text.verbosity`、`service_tier`、`previous_response_id`、`conversation`、`background`。
- `prompt_cache_options`、`prompt_cache_breakpoint`、`prompt_cache_retention`；没有显式缓存模式或 TTL 开关。

订阅兼容层的允许名单可能比公共构造器实际发送集合更大；允许某键通过不等于当前会构造它。SIWC 要求及不支持字段以[官方预览限制](https://developers.openai.com/siwc/token-sharing-open-source/preview-limitations)为外部参考，不把普通 API 能力直接推广到订阅路由。

<a id="anthropic"></a>
## 3. Anthropic Messages

入口：`streamAnthropicMessages`；思考映射：`configureAnthropicThinking`；缓存：`markAnthropicPromptCacheBreakpoints`（[适配器](../internal/app/prov_adapter_anthropic.go)）。

| 请求字段 | 当前发送规则 |
|---|---|
| `model`、`messages` | 当前模型及转换后的历史内容块；首条不是 user 时补一个内容为 `...` 的 user 消息 |
| `max_tokens` | 配置的输出上限 |
| `stream:true` | SDK 流式接口补入 |
| `system` | 系统提示非空时发送文本块数组 |
| `tools` | 有工具时发送 `name`、非空 `description`、`input_schema`；schema 顶层归一为 object |
| `thinking.type` | 依据模型族及档位发送 adaptive / enabled / disabled；auto 或未声明思考字段的族不发 |
| `thinking.budget_tokens` | 仅预算型分支；low=1024、medium=4096、high/xhigh/max=32000，再受 max_tokens 与保留回答空间约束；千问 switch-only 分支省略 |
| `output_config.effort` | Claude adaptive 及支持此字段的兼容族按统一映射发送；Kimi 族仅用此字段表达思考档位 |
| 顶层 `reasoning.effort:"none"` | **仅 DeepSeek 官方兼容端点且用户选择 off 时发送**；auto 及开启思考的档位不发送该关闭指令 |
| 块内 `cache_control:{type:"ephemeral"}` | 最后一个工具、最后一个 system 块、最后一个可承载标记的历史内容块；没有对应内容就不产生该标记 |
| 顶层 `cache_control:{type:"ephemeral"}` | 仅 Moonshot 端点额外发送，当前判定收口在 `anthropicTopLevelCacheControlEndpoint` |

当前不发送 `thinking.display`、任何缓存标记的 `ttl`，也不主动配置 `tool_choice`、`temperature`、`top_p`、`top_k`、`stop_sequences`、`metadata`。

省略 ttl 时保留 `type:ephemeral`；SDK 全零值会把整个缓存标记省略。标记由 `anthropic.NewCacheControlEphemeralParam()` 创建。历史尾部遇到 thinking / redacted_thinking 时继续向前找 text / image / tool_use / tool_result 等可标记块。

省略 display 取模型默认值，部分模型仍会返回摘要；不等于关闭思考。Anthropic 文档明确指出隐藏思考文本仍按完整思考 token 计费，主要收益是流式延迟，不能据此声称省额度。

<a id="cache"></a>
## 4. 缓存键与内部会话身份

发送判断唯一入口为 `promptCacheKeyForRequest`（[共享规则](../internal/app/prov_model.go)）；没有内部会话键时，所有路径都不发送请求级缓存键。

| 端点 / 模型 | Chat | Responses |
|---|---|---|
| 官方 OpenAI 全部模型（含 GPT-6.1 Sol、登录订阅） | 有会话键则发 | 有会话键则发 |
| 兼容 Kimi / Moonshot 模型族 | 有会话键则发 | 有会话键则发 |
| 兼容 GLM、MiniMax 模型族 | 不发 | 有会话键则发 |
| 其余兼容端点 | 不发 | 不发 |

2026-10-25 按用户要求恢复官方端点的会话缓存键，不再按 GPT 版本省略。兼容供应商继续沿用 `promptCacheKeyVendors`。

内部 `responsesPromptCacheKey` 仍按会话生成，用于回放作用域与诊断分组；省略请求字段不会清空它。子代理继续使用父会话的稳定通道身份，并以单独的 `reasoningScope` 隔离每个 run 的思考台账。OpenRouter 的 `x-session-id` 请求头也保留，见下一节。

OpenAI Docs 将 GPT-5.6+ 的 key 定义为可选的缓存核算分组，而非必需路由开关。Ally 当前仍发送稳定 key，以恢复此前请求形状供用户验证；这不代表恢复 key 已被证实能改善订阅额度。2026-10-25 小样本对照中，带 key 与不带 key 都出现过命中和零命中；不能承诺任何一项请求精简必然提高命中率，详见[实测记录](chatgpt-cache-diagnostics.md)。

<a id="history"></a>
## 5. 请求开关与历史回放

| 项目 | 必须区分的行为 |
|---|---|
| 请求级 `reasoning.summary` | 当前省略，表示不主动请求可读摘要 |
| Responses 历史 reasoning item 的 `summary` | 保留；空数组被 SDK 省略时由 `ensureResponsesReasoningSummaries` 补回，不能连同请求开关一起删 |
| `encrypted_content`、原生 output、phase、工具身份 | 按已有台账回放，继续工具轮与多轮上下文 |
| Anthropic thinking / redacted_thinking / signature | 保留原生块；改变显示设置不授权改写签名或历史 |
| Chat `reasoning_content` / `reasoning_details` | 保留已有内容，缺失字段按适配规则补齐 |
| `ally_model_state_v1` 等 Ally 本地状态 | 用于恢复适配台账，不作为供应商自定义消息字段发送 |

off 与 auto 是独立档位：auto 省略等级、采用供应商默认；off 使用该供应商支持的关闭方式，不能关闭的模型按已定义的最低档替代规则处理。压缩等内部 LLM 调用沿用用户 cfg，不能为了省字段擅自改档位。

<a id="headers"></a>
## 6. 请求头与非请求体配置

- Chat / Responses 使用 Bearer 鉴权；登录订阅由 `Manager.Do` 注入 OAuth Token。
- Anthropic 由 SDK 设置 API Key 鉴权及 `anthropic-version:2023-06-01`；SDK 还会附带自身版本、运行时等传输标识。
- JSON / SSE 所需 Content-Type、Accept 按各传输实现设置。
- `modelRequestHeaders` 应用配置或默认 User-Agent；普通模型自定义头可覆盖内置头。OAuth 模型归一时清空自定义头。
- 有内部会话键时，所有端点都添加 `session-id` 与 `x-session-id`（值为同一个哈希会话键）。这两个头决定**路由亲和**，不参与前缀匹配：供应商的提示词缓存按机器存放，同一会话落到同一台机器才可能复用前缀，所以该值必须在一段会话内保持稳定。ChatGPT 按 `session-id` 做缓存亲和（与 Codex 同名头），OpenRouter 文档定义 `x-session-id`，其余端点忽略未知头。
- Anthropic 显式开启思考、有工具、且不是 adaptive / 无思考块族时，添加 `anthropic-beta:interleaved-thinking-2025-05-14`。

代理、超时、重试次数、流式节流属于客户端配置，不是请求体参数。删除某个请求字段不会减少工具循环或重试实际发出的请求数。

## 7. 维护与验证

修改适配器字段时先更新本文；认证页只维护登录与凭据，缓存页只维护缓存机制，实测页只维护实验条件及证据边界。供应商资料快照按自己的抓取时间维护，不把“Ally 不发”改写成“供应商不支持”。

| 契约 | 代码 / 验证位置 |
|---|---|
| Chat 实际构造与幂等补写 | `prov_adapter_chat.go`、`prov_reasoning.go`；`prov_model_test.go` 中 Chat / PatchChat 测试 |
| Responses 省略项、strict 与缓存键 | `prov_adapter_responses.go`、`prov_model.go`；`TestResponsesRequestOmitsOptionalToolControls`、`TestPromptCacheKeyRestoredOnOfficialModels` |
| 订阅兼容、原生思考回放 | `prov_chatgpt.go`；`prov_chatgpt_test.go` |
| DeepSeek off 与开启档位不冲突 | `TestDeepSeekAnthropicOffOnlyWhenSelected`（本地模拟服务，不访问供应商） |
| Anthropic 标记保留而 ttl 省略 | `prompt_cache_retention_test.go`、`prov_model_test.go` |
| 最终请求与用量诊断 | `prov_perf.go`、`prov_perf_test.go` |

字段存在性测试和构建通过只证明客户端契约，不证明服务端缓存命中或订阅额度改善。

## 8. 外部参考

以下用于解释协议含义，不替代上述源码发送规则。核对日期：2026-10-25。

- [OpenAI Docs：Function calling](https://developers.openai.com/api/docs/guides/function-calling)
- [OpenAI Docs：Reasoning](https://developers.openai.com/api/docs/guides/reasoning)
- [OpenAI Docs：Prompt caching](https://developers.openai.com/api/docs/guides/prompt-caching)
- [OpenAI Docs：Prompt cache diagnostics](https://developers.openai.com/api/docs/guides/prompt-caching/diagnostics)
- [SIWC：Preview limitations](https://developers.openai.com/siwc/token-sharing-open-source/preview-limitations)
- [Anthropic：Thinking](https://platform.claude.com/docs/en/build-with-claude/thinking)
- [Anthropic：Prompt caching](https://platform.claude.com/docs/en/build-with-claude/prompt-caching)
