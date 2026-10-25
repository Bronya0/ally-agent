# ChatGPT 订阅缓存排查

更新：2026-10-25。本文保存实测条件、结果与诊断方法；Ally **当前发送规则**统一见[三协议字段](model-protocol-fields.md)，缓存架构见[缓存实现与观测](agent-prompt-cache.md)。

当前结论：真实会话零缓存的根因是缺少 `session-id` 路由亲和头——请求体字段与系统提示的位置已逐项排除，前缀本身没有问题；补上该头后字节相同的重复请求稳定命中（见第 6 节）。请求级 summary 仍保持省略，但它不是零命中的原因；第 3 节“带 key 与不带 key 都出现过命中和零命中”由路由随机解释，不是字段差异。21:18—21:20 的 Luna 官方诊断返回 unavailable；该登录路由要求 store:false，显式缓存控制仍被拒绝，详见第 5 节。

## 1. 2026-10-25 18:46—18:47：摘要参数对照

在本机已授权账号的官方 SIWC `POST /v1/responses` 路由，用 `gpt-6.1-sol`、medium 和约 1,370 input token 的固定合成上下文进行对照。带 namespace 工具、`store:false`、`stream:true`、加密 reasoning include；不发送项目内容、不执行工具、不自动重试。

该次对照保持账号、模型、37 字符缓存键、instructions、tools、include、effort 和 input 相同，只改变请求级 `reasoning.summary`。每组前两次请求体 SHA-256 相同，第三次（如有）追加完整原生 output 与 user 输入。

| 北京时间 | 请求差异 | Input token | Cached token | 返回的 summary |
|---|---|---:|---:|---|
| 18:46:26 / 18:46:31 | summary:auto，原样重复两次 | 各 1,368 | 均 0 | detailed |
| 18:47:21 | 仅省略 summary，首次 | 1,368 | 0 | null |
| 18:47:25 | 省略 summary，原样重复 | 1,368 | 1,280 | null |
| 18:47:28 | 省略 summary，追加完整原生 output 和下一条 user | 1,377 | 1,280 | null |
| 18:47:52 | 恢复与第一组相同的 summary:auto 请求 | 1,368 | 0 | detailed |

服务端均返回 `reasoning.context:all_turns`、`effort:medium`、`service_tier:default`；每次输出 5 token，其中 reasoning token 为 0。响应都显式提供缓存计数。

其他早期对照中，移动 instructions 到 developer、单独去掉缓存键或工具均未恢复命中；它们不能替代下一节“已去 summary，再比较 key”的实验。

**当时采取的处理**：省略请求级摘要，保留思考档位与原生历史；之后按用户要求扩展到所有 Responses 路由。这个处理不删除历史 reasoning item 的 summary 数组。实现规则及其他可选字段精简见[字段文档](model-protocol-fields.md#responses)。

**证据边界**：这组结果曾支持“摘要参数与该次零命中有关”的假设，但后续发现相同请求也会有缓存计数波动。因此不能仅靠这组小样本认定 summary 是确定触发器、唯一根因，或所有 API-key / 订阅模型都存在相同行为。普通 Responses 的统一省略是实现选择，不是全平台实测结论。

## 2. 2026-10-25 20:06—20:07：真实会话仍为零

用户运行 `bin/Ally.exe`，构建时间为 20:00:49，进程启动于 20:05:13。选择官方 ChatGPT OAuth Responses 的 `gpt-6.1-sol`、medium；截图与本地用量记录对应。

| 北京时间 | 输入 | 输出 | 本地记录的缓存命中 |
|---|---:|---:|---:|
| 20:06:00 | 22,270 | 13 | 0 |
| 20:06:09 | 22,291 | 54 | 0 |
| 20:06:14 | 22,380 | 28 | 0 |
| 20:06:28 | 22,416 | 13 | 0 |
| 20:06:37 | 22,437 | 10 | 0 |
| 20:07:22 | 22,456 | 82 | 0 |

该构建的字段测试确认省略请求级 summary 和新版官方模型的 key；本地记录确实为零，不是页脚整数百分比的舍入现象。但该轮没有保存最终请求及原始响应 usage；这些记录本身不能证明每次服务端渲染前缀相同，也不能独立排除遥测提取问题。

## 3. 2026-10-25 20:18—20:20：去摘要后比较缓存键

同一已授权账号、Sol、medium、固定合成内容与 namespace 工具；两组都省略 summary、tool_choice、parallel_tool_calls，函数工具 strict 为 false，保留 include 和 store:false。组间只有请求级 key 的有无不同；每组前两次请求体相同，第三次追加该组完整原生 output 与新 user。

| 组别 | 北京时间 | Input token | Cached token |
|---|---|---:|---:|
| 不带 key，首次 | 20:18:58 | 1,368 | 1,280 |
| 不带 key，原样重复 | 20:19:02 | 1,368 | 0 |
| 不带 key，追加历史 | 20:19:06 | 1,377 | 1,280 |
| 带 key，首次 | 20:20:17 | 1,368 | 0 |
| 带 key，原样重复 | 20:20:22 | 1,368 | 0 |
| 带 key，追加历史 | 20:20:26 | 1,377 | 1,280 |

六次响应均为 HTTP 200 / response.completed，返回 summary:null、all_turns、medium、service_tier:default；每次输出 5 token，reasoning token 为 0。合计输入 8,226、输出 30 token；没有执行工具或发送用户项目内容。

这组输入复用早期合成文本，没有为各组构造全新前缀，因此“组内首次”不等于服务端冷缓存。它也不是随机交错的大样本测试，不能量化 key 对长期命中率的影响。

**可以确认**：

- 不带 key 仍能命中，删 key 不是“必然零命中”的条件。
- 带 key 也会零命中，恢复 key 不能被当成确定修复。
- 相同请求体出现不同计数，客户端字段相同不足以保证命中。
- 约 1,400 token 的合成探测不能解释约 22,000 token 的真实会话为何连续为零；该问题仍需最终请求和原始 usage 证据。

### 后续处理：恢复会话缓存键

2026-10-25 按用户要求，恢复官方 OpenAI Chat / Responses 请求的稳定 `prompt_cache_key`（含 GPT-6.1 Sol 登录订阅），保留请求级 summary 省略和其他已完成修改。新测试构建输出到 `bin/key-restored/Ally.exe`，用于与 20:00:49 构建对照；恢复 key 的真实会话效果尚待验证。上表记录的是恢复前的实验，不改写为当前默认发送行为。

## 4. 显式缓存参数实验：已撤回

2026-10-18 的订阅 `gpt-6-luna` 分别拒绝 `prompt_cache_options` 和 `prompt_cache_breakpoint`（HTTP 400）。2026-10-25 的 Sol 也拒绝 `prompt_cache_options:{mode:"implicit"}`，错误参数为 prompt_cache_options。

这些结果只说明当时测试的账号/路由不接受所测显式控制字段，不能推广到所有模型或该对象的其他子字段。第 5 节记录了同日稍晚 comparison_response_id 被接受但诊断 unavailable 的 Luna 结果。Ally 因而沿用默认缓存行为，冻结系统提示保留在 instructions，不增加显式 developer 缓存锚点。不要仅凭普通 API 文档或模型版本号向订阅请求添加这些控制项。

## 5. 2026-10-25 21:18—21:20：Luna 官方诊断与 store 对照

按用户要求，直接用本机已授权账号请求官方 SIWC `POST /v1/responses`，模型为 `gpt-6-luna`、medium。保留稳定 key、`store:false`、`stream:true` 和加密 reasoning include；省略请求级 summary 与可选工具控制字段。固定合成 instructions 在开头增加本次唯一实验标记，避免复用早期探测前缀；namespace 内函数为 strict:false，未执行工具、未发送项目内容。

| 北京时间 | 请求 | Input token | Cached token | Cache write token | 官方诊断 |
|---|---|---:|---:|---:|---|
| 21:18:22 | 基线，无 comparison | 1,383 | 0 | 0 | 未请求 |
| 21:18:26 | 相同内容，comparison 指向基线 | 1,383 | 0 | 0 | unavailable |
| 21:18:29 | 追加完整原生 output 与新 user，comparison 指向前一响应 | 1,392 | 0 | 0 | unavailable |
| 21:20:10 | 重发 21:18:26 的相同请求，仍比较最早基线 | 1,383 | 0 | 0 | unavailable |

四次均为 HTTP 200 / response.completed，返回 `store:false`、summary:null、all_turns、medium、service_tier:default；每次输出 5 token、reasoning token 为 0，合计输入 5,541、输出 20 token。最后一次距基线完成约 108 秒；延迟重试仍未给出可用诊断，不能简单认定只是立即比较时记录暂未就绪。

**本次端点能力发生了需要区分的变化**：

- 仅发送 `prompt_cache_options.comparison_response_id` 被接受，响应返回 `prompt_cache_options:{mode:"implicit",ttl:"30m",comparison_response_id:...}` 和 `prompt_cache_diagnostics:{type:"unavailable"}`。
- 改为 `prompt_cache_options:{mode:"explicit"}` 并在 developer input_text 添加显式断点，首请求即 HTTP 400：`invalid_parameter`，参数为 `prompt_cache_options`，信息为 `prompt_cache_options is not supported on this model`。不能把“诊断参数被接受”等同于“显式缓存控制受支持”；该组合失败也不能独立确认断点字段的支持性。
- `store:true` 与省略 store 均为 HTTP 400；复核 true 时服务端明确返回 `Store must be set to false`。未产生成功的 true/false 配对，不能声称完成了两种保存设置下的缓存命中率对照。

**store 与缓存的结论**：此登录订阅路由要求 store:false，不能通过打开响应保存修复缓存。OpenAI Docs 将响应保存与提示词 KV 缓存分为不同机制；提示词缓存需要先写入、再复用，诊断也兼容 Zero Data Retention，不依赖取回之前保存的响应。前面 Sol 的成功命中同样使用 store:false，因此该字段并非“必然禁止缓存”的开关。

**仍未定位的部分**：本次原始 usage 明确报告读取与写入均为零；但不能仅凭这些计数确定物理 KV 缓存是否创建、服务端路由是否变化或订阅核算原因。`unavailable` 是无结论结果，不是某个具体 miss reason，也不能单独证明所有 Luna 请求均不支持诊断。Ally 本次未增加生产请求字段，也未改变 store:false。

探测脚本与完整计数保存在本地忽略目录 `.tmp/cache-probe/`。这里记录的是特定账号、路由与时点的结果；保留第 4 节早期拒绝记录，但不再据此概括所有 prompt_cache_options 子字段都不接受。

## 6. 2026-10-25 夜间：根因是缺少 session-id 路由亲和头

承接第 5 节的“仍未定位”。同一已授权账号、同一官方 SiWC `POST /v1/responses`，`gpt-6.1-sol`（`gpt-6-luna` 表现一致）、medium，固定合成前缀约 3.6k 可见 token 放在 `instructions`。每组请求体逐字节相同（比对 sha256），连续发两次；单次探针最多两组，不重试、不执行工具。

| 请求头 | 第 1 次 cached | 第 2 次 cached |
|---|---:|---:|
| 不发亲和头 | 0 | 0 |
| `session-id` + `thread-id` | 0 | 3,584（98%） |
| 仅 `session-id` | 0 | 3,584（98%） |
| 仅 `session-id`，值为 `ally:<32 位 hex>` | 3,584（98%） | 3,584（98%） |

不发亲和头的这一组在本轮累计十余次请求（含逐字段加字段、把长内容分别放进 instructions / developer / user 三种布局），`cached_tokens` 与 `cache_write_tokens` 均为 0。发头的一组稳定命中，首次仍可能是冷启动。`thread-id` 对命中无影响，可不发；`ally:` 前缀的哈希值被服务端接受，没有 400。

**机制**：提示词缓存按机器存放，`session-id` 决定请求路由到哪台机器，不参与前缀匹配。因此同一会话内该值必须稳定；值变化不会必然失配，只是可能落到没有该前缀的机器——这同时解释第 3 节与第 5 节“相同请求体出现不同计数”。

**请求体字段与内容位置已被排除**：从最小合法集（model + instructions + input + 强制的 `store:false`）逐层加 tools、reasoning.effort、include、prompt_cache_key，以及把系统提示分别放进 instructions / developer message / user message / 完全省略 instructions，在不发亲和头时全部为零。服务端 `attribution.request_fields` 显示 instructions 单独 3,525 token 时也是 `cache_write_tokens:0`。

**证据边界**：

- 只在 ChatGPT 订阅路由（`chatgpt_oauth`）实测；API-key 直连官方端点与第三方中转未验证。
- 未验证多轮追加、工具循环、压缩前后的表现；没有取得服务端物理缓存状态的直接证据。
- 第 5 节的端点能力结论不变：`store:true` 与 `prompt_cache_options.mode` 仍被拒，`comparison_response_id` 仍返回 `unavailable`，不能改用官方诊断定位 miss。
- 官方客户端 Codex 只把 `session-id` 发在 Responses 上；Ally 有意在所有端点发送，见[字段文档](model-protocol-fields.md#headers)的请求头一节。

实现：`sessionAffinityHeaders`（[请求字段](../internal/app/prov_model.go)）。探针保留在 `.tmp/cacheprobe/`。

<a id="diagnostics"></a>
## 7. 采集最终请求诊断

先完成当前任务并退出旧进程，再在仓库目录的 PowerShell 中启动已构建程序：

```powershell
$env:ALLY_PERF_DIAGNOSTICS = Join-Path $PWD "ally-cache.jsonl"
& .\bin\Ally.exe
```

测试后在该终端关闭环境开关：

```powershell
Remove-Item Env:ALLY_PERF_DIAGNOSTICS
```

日志为追加写入，默认关闭。它保存时序、计数与比较结论，不保存请求正文、工具参数、凭据、账号、缓存键值或前缀哈希；它不是可重放的原始请求抓包。

`prov_perf.go` 在 SDK 序列化、订阅兼容处理之后比较最终请求；编排层的 `noteRequestPrefix` 无法覆盖这层差异。原始 Responses completed 事件的 usage 由 `modelUsageFromResponsesEvent` 解析。

| responses_attempt 字段 | 含义 |
|---|---|
| `wire_prefix` | first_request / unchanged_prefix / head_changed / tail_changed / removed_changed / lane_limit |
| `wire_input_items` | 最终 input 条目数 |
| `wire_reasoning_items` / `wire_encrypted_reasoning_items` | reasoning 及带加密内容的条目数 |
| `wire_cache_key_present` | 最终请求是否带 key；不记录其值 |
| `wire_explicit_breakpoints` | 内容块中显式缓存断点数 |
| `wire_cache_mode` | 显式指定的 implicit / explicit；沿用默认时不存在 |
| `cache_usage_reported` | 是否取得缓存计数；false 不能当成实测零命中 |
| `cache_write_tokens` | 服务端报告的写入量；未提供时为 0 |
| `reasoning_context_effective` | 服务端返回的思考上下文模式；未返回则不存在 |

当前默认请求应没有显式断点或 cache mode。key 是否出现按[实现规则](model-protocol-fields.md#cache)判断，不能统一要求为 true。

比较基线只在**单次 run** 内保存，按模型/端点/内部回放身份分组，最多 64 组，run 结束即释放。多个用户问题各自开始的 run 都出现 first_request，不能据此证明跨提问前缀一致。当前诊断也没有逐项输出请求级 summary 的存在性；仅凭该日志不能直接检查此键，字段存在性另由序列化测试覆盖。

若同一工具循环出现 head_changed / tail_changed，先查客户端变化；若 unchanged_prefix 且明确报告零命中，则继续调查服务端渲染、路由、缓存寿命和核算。不要将“客户端一致”写成“平台故障已证实”。

## 8. 与其他客户端比较

相同显示模型名不证明两个客户端具有相同请求形状、服务端路由或订阅核算。保持账号、端点、模型、effort、工具集、输入与回放方式可比；缓存比例也不能直接换算成五小时／每周额度百分比。

- [OpenAI Docs：Prompt caching](https://developers.openai.com/api/docs/guides/prompt-caching)
- [OpenAI Docs：Prompt cache diagnostics](https://developers.openai.com/api/docs/guides/prompt-caching/diagnostics)
- [SIWC：Models and inference](https://developers.openai.com/siwc/token-sharing-open-source/models-and-inference)
- [SIWC：Preview limitations](https://developers.openai.com/siwc/token-sharing-open-source/preview-limitations)
