# 六家开放平台 API 协议字段参考

> 覆盖厂商：DeepSeek、智谱 GLM、Kimi / Moonshot、MiniMax、MiMo、千问AI平台。
> 其中**千问AI平台只收录协议核心**（四套协议 OpenAPI：openai-chat / openai-responses / anthropic / dashscope，加上文本生成、工具调用、思考、错误码与限流、模型清单等）；该站其余约 800 个页面（视频/图像/语音生成、world-model、agent-infra 等）不在收录范围，详见该家章节开头的说明。
> 抓取方式：逐页抓取各家文档站的 Markdown 原文（Mintlify 站点优先用 `llms.txt` + 页面 `.md` 后缀取原文），字段名保留英文原文，数值 / 默认值 / 枚举照抄，**未做任何推断**。
> 抓不到的页面、以及文档本身没有写的内容，都在对应位置明确标注（「未抓到」「文档未给出」），不做补齐。
> 多模态部分同时覆盖**输入**（文本 / 图片 / 音频 / 视频 / 文件）与**输出**（图片生成 / 语音合成 TTS / 视频生成 / 音乐生成 等），生成类端点也在列。
> 本文只记录对方文档的内容，不含与本仓库实现的对比。

**本文结构与读法**（2026-10-03 合稿：把六家各写一遍的公共部分收成一处）

| 部分 | 写什么 |
|---|---|
| 《通用基线》 | 六家**完全一致**的标准协议部分只写一遍：字段语义、`messages` 与内容块结构、`tools` 结构、响应对象与 SSE 分帧、Responses / Anthropic 公共骨架、标准 HTTP 码 |
| 六家章节（每家 13 节） | 只写**与本家有关的差异**：默认值、取值范围、必填性、限制、枚举、模型相关行为、本家特有字段与端点。公共语义处写作「见《通用基线》§0.x」，不再重复 |
| 附录《各家页面清单》 | 569 条抓取页面**一页一行**（规范 URL + 这页讲了什么）。**全站 URL 只在附录出现一次**，各家章节内不再列「来源」行；抓取结论（未抓到 / 未纳入）也在这里注明 |

> 收录范围：只收**协议与 API 字段**相关的页面。**周边页不收**——客户端 / IDE 接入教程、计费与套餐（订阅 / 席位 / 积分 / 发票 / 优惠券 / 充值 / 免费额度 / 成本优化）、FAQ、更新日志与新闻、法务与营销页，共 221 条。
> 判据收口在一处：`scripts/audit-provider-api-docs.py` 的 `is_peripheral()`（按路径片段判），文档与审计脚本共用同一函数，不各写一份。

核验覆盖率：`python3 scripts/audit-provider-api-docs.py` 会用各家文档站自己的 `sitemap.xml` / `llms.txt` 重取全量页面清单，与本文附录逐条比对（当前 569/569 全覆盖；周边页按上述判据排除，单独列到 `peripheral-*.txt`，不计入缺口）。

## 目录

六家使用同一套 13 节模板，便于按节横向对照：

1. 端点与鉴权
2. Chat Completions 请求字段（只列差异）
3. Responses API 字段（只列差异）
4. Anthropic(Messages/Claude) 兼容端点字段（只列差异）
5. 响应字段与流式结构
6. 思考/推理字段
7. 工具调用字段
8. 多模态与文件（输入 + 输出）
9. 缓存与成本字段
10. 特殊模式
11. 错误码与限流
12. 模型清单与限制
13. 来源页清单（指向附录）

各厂商章节：

- DeepSeek
- 智谱 GLM
- Kimi / Moonshot
- MiniMax
- MiMo
- 千问AI平台（仅协议核心）

附录：

- 各家页面清单（569 条，抓取覆盖核验用）

---

## 通用基线（六家共用）

> 本节只收录 DeepSeek、智谱 GLM、Kimi / Moonshot、MiniMax、MiMo、千问AI平台 六家文档中**完全一致**的结构事实与命名。任何一家的默认值、取值范围、必填性、支持与否或模型相关行为都不在此列，请查各家章节对应小节。字段名、枚举值一律保留英文原文，说明口径与原文一致。

### 0.1 三套协议公共约定

- 六家均提供两类对话端点：OpenAI 兼容的 Chat Completions（路径以 `/chat/completions` 结尾）与 Anthropic 兼容的 Messages（路径以 `/v1/messages` 结尾，`POST`）。
- Responses API（路径以 `/responses` 结尾）为其中五家提供（DeepSeek、GLM、Kimi、MiniMax、千问），MiMo 未提供。
- 三套协议均为 `POST` 请求，请求体为 JSON；均以各平台控制台申请的 API Key 鉴权。
- 模型列表、文件、批处理、Token 计数等辅助端点为各家特有，见各家章节。
- `base_url` 为「平台域名 + 路径前缀」，客户端自行拼接 `/chat/completions`、`/responses`、`/v1/messages`；`/v1` 前缀并非统一写法。
- OpenAI 兼容端点鉴权统一为请求头 `Authorization: Bearer <API key>`；JSON 请求体带 `Content-Type: application/json`。
- Anthropic Messages 端点的鉴权头写法各家不同（`x-api-key` / `Authorization: Bearer` / `api-key`）。
- 流式：请求 `stream: true` 时以 SSE 返回；Chat 流式 chunk 的增量在 `choices[].delta`。
- 保活机制、签名 / nonce 头、超时、`anthropic-beta` / `anthropic-version` 等请求头属各家特有。
- 六家均同时支持流式（`stream: true`）与非流式两种调用方式。

> 各家差异（默认值 / 范围 / 不支持项 / 模型相关行为）见各家章节对应小节。

### 0.2 Chat Completions 公共请求字段

下表列 OpenAI 兼容 Chat Completions 的**标准字段及其公共语义**（六家文档都用这套命名）。各家的默认值、取值范围、必填性、支持与否以及模型相关行为一律**不在此表**，见各家章节 §2 的「差异表」；本表未涵义到的各家特有字段也见各家章节。

| 字段 | 类型 | 公共语义 |
|---|---|---|
| `model` | string | 指定要调用的模型 ID |
| `messages` | array | 对话历史消息列表，按时间顺序排列 |
| `stream` | boolean | 是否以 SSE 流式返回；配合 `stream_options` |
| `stream_options.include_usage` | boolean | 为 `true` 时流式响应中附带 `usage` |
| `temperature` | number | 采样温度，控制随机性；一般只与 `top_p` 调其中一个 |
| `top_p` | number | 核采样阈值 |
| `stop` | string \| array | 命中即停止生成的停止词 / 停止序列 |
| `response_format` | object | 指定输出格式（如 `{"type": "json_object"}` 的 JSON 模式、或 `json_schema` 结构化输出） |
| `logprobs` | boolean | 是否返回输出 token 的对数概率 |
| `top_logprobs` | integer | 每个位置返回概率最高的前 N 个 token 及其对数概率；须同时开 `logprobs` |
| `max_tokens`（部分家写作 `max_completion_tokens`） | integer | 单次生成的最大输出 token 数 |
| `n` | integer | 为一条输入生成几个候选回复 |
| `seed` | integer | 随机种子，用于尽量复现采样结果 |
| `user`（部分家写作 `user_id`） | string | 标识业务侧最终用户，用于内容安全、缓存与调度隔离 |
| `frequency_penalty` / `presence_penalty` | number | 按词频 / 是否已出现过的惩罚项，降低重复输出 |
| `tools` | array | 模型可调用的工具（函数）列表，元素为 function 工具 |
| `tool_choice` | string \| object | 控制模型是否 / 如何调用工具；可选值与默认值各家不同 |

`tools` 与 `tool_choice` 配合使用：先声明工具，再由 `tool_choice` 决定是否 / 如何调用。

> 各家差异（默认值 / 范围 / 不支持项 / 模型相关行为）见各家章节对应小节。

### 0.3 messages 与内容块公共结构

- `messages[].role` 枚举：`system`、`user`、`assistant`、`tool`。
- 角色语义：`system` 设定模型行为与角色；`user` 为用户输入；`assistant` 为模型回复；`tool` 为工具执行结果。
- 多模态输入时，内容块数组放在该消息的 `content` 内。
- `messages[].content` 可为**纯字符串**，也可为**内容块数组**（多模态输入）。
- 内容块用 `type` 字段区分类型；六家公共的块类型为 `text`（文本块），块内文本置于 `text` 字段。
- assistant 消息可带 `tool_calls`：`id`、`type`（`function`）、`function.name`、`function.arguments`（JSON 格式字符串）。
- tool 消息：`role: "tool"`，必带 `tool_call_id`（对应上一轮某次 `tool_calls[].id`）与 `content`（工具执行结果）。

> 各家差异（默认值 / 范围 / 不支持项 / 模型相关行为）见各家章节对应小节。

### 0.4 tools / tool_choice 公共结构

- `tools[]` 元素形态：`{"type": "function", "function": {"name", "description", "parameters"}}`，`type` 固定 `"function"`。
- `function.name`：函数名，同一次请求内必须唯一。
- `function.description`：函数功能描述，供模型判断何时调用。
- `function.parameters`：JSON Schema 对象，描述函数入参，常用关键字为 `type`、`properties`、`required`。
- `tool_choice` 默认形态为 `auto`，也可用 `{"type": "function", "function": {"name": "..."}}` 指定具体函数。
- 响应侧以 `finish_reason: "tool_calls"` 标识本轮返回的是工具调用，`message.tool_calls[]` 逐个给出 `id` / `type` / `function.name` / `function.arguments`。
- 模型本身不执行函数：客户端执行后以 `role: "tool"` 消息回传结果，再发起下一轮请求。
- `tools` 元素类型在各家文档中以 `function` 为主，其余内置工具类型见各家章节。

> 各家差异（默认值 / 范围 / 不支持项 / 模型相关行为）见各家章节对应小节。

### 0.5 公共响应对象与 SSE chunk

**非流式响应公共顶层字段**：`id`、`created`、`model`、`choices`、`usage`。

- `choices[]`：`index`、`message`、`finish_reason`。
- `choices[]` 通常取 `choices[0]`；`message.role` 恒为 `assistant`。
- `choices[].message`：`content`、`tool_calls`；思考模式下另有 `reasoning_content`。
- 思考模式下，Chat 响应与流式 delta 的思考内容字段名均为 `reasoning_content`（六家一致）。
- `finish_reason` 六家公共取值：`stop`、`length`、`tool_calls`。
- `usage`：`prompt_tokens`、`completion_tokens`、`total_tokens`。

**SSE chunk（Chat 流式）**：

- chunk 含 `id`、`choices[]`、`created`、`model`。
- 首个 chunk 的 `delta` 携带 `role`（`assistant`）。
- 增量字段：`delta.content`、`delta.reasoning_content`、`delta.tool_calls[]`；`delta.tool_calls[]` 的首个分片带 `index`、`id`、`type`、`function.name`，后续分片只带 `function.arguments`。
- 同一响应内所有 chunk 共享请求的 `id`；`choices[].index` 标识第几个候选。
- `finish_reason` 在生成中为 `null`，仅在结束块给出枚举值。
- Chat 流以 `data: [DONE]` 收尾（MiniMax 文档未给出终止标记，见 MiniMax 章节 §5）。

缓存命中 / 思考 token 的 usage 字段命名，以及流式下的 usage 下发位置，各家不同，见各家章节的 §5 / §9。

> 各家差异（默认值 / 范围 / 不支持项 / 模型相关行为）见各家章节对应小节。

### 0.6 Responses API 公共结构

以下为提供该协议的五家（DeepSeek、GLM、Kimi、MiniMax、千问）共有的结构；MiMo 未提供 Responses API。

- `input`：纯字符串（视作一条 `user` 消息）或输入 item 列表。
- 公共输入 item 类型：`message`、`function_call`、`function_call_output`、`reasoning`。
- `message` item：`role`（含 `user`、`assistant`）+ `content`（内容块数组，公共块类型如 `input_text`、`output_text`、`input_image`）。
- `function_call` item：`call_id`、`name`、`arguments`；`function_call_output` item：`call_id`、`output`；`reasoning` item 承载思维链。
- `instructions`：顶层系统指令，插入上下文开头。
- `output[]` 公共 item 类型：`message`（`role: assistant`，内容块 `output_text`）、`reasoning`、`function_call`。
- `tools[]` 为**扁平结构**：`name` / `description` / `parameters` 直接放在工具对象顶层（没有 `function` 包装层）。
- `max_output_tokens`：本次输出 token 上限（含思维链 token）。
- 响应 `usage` 含 `input_tokens`、`output_tokens`、`total_tokens`。
- 五家均支持 `stream: true` 流式与一次性返回两种方式。
- `reasoning.effort`：Responses 侧的思考强度开关。
- Responses 流式以 `response.*` 命名的事件推送（各事件结构完整度不同，见各家章节 §3）。

> 各家差异（默认值 / 范围 / 不支持项 / 模型相关行为）见各家章节对应小节。

### 0.7 Anthropic Messages 公共结构

- 端点 `POST <base_url>/v1/messages`（六家均提供）。
- `model`：使用各家模型编码。
- `messages`：`messages[].role` 取 `user` / `assistant`；`messages[].content` 为字符串或内容块数组。
- `max_tokens`：生成内容的最大 token 数。
- `stream`：布尔，`true` 时以 SSE 返回。

- Anthropic Messages 协议的标准骨架字段（公共语义，各家文档给出的完整度不一，各家是否列出见多家章节 §4）：
  - `system`：顶层系统提示词（与 OpenAI 的 `system` 消息位置不同）。
  - `stop_sequences`：停止序列数组（对应 OpenAI 的 `stop`）。
  - `tools[].input_schema`：工具入参 JSON Schema（键名不是 `parameters`）；`tool_choice.type` 取 `auto` / `any` / `tool` / `none`（不是 `required`）。
  - 响应：`content` 为内容块数组，文本块 `{"type": "text", "text": ...}`，工具调用为 `tool_use` 块（`id` / `name` / `input`）；结束原因在 `stop_reason`（`end_turn` / `max_tokens` / `tool_use` / `stop_sequence`），非 `finish_reason`。
  - 流式：以 `message_start` → `content_block_start` / `content_block_delta`（`text_delta` / `input_json_delta` / `thinking_delta`）→ `content_block_stop` → `message_delta`（带 `stop_reason` 与 `usage`）→ `message_stop` 的事件序列返回。

> 各家差异（默认值 / 范围 / 不支持项 / 模型相关行为）见各家章节对应小节。

### 0.8 标准错误体、HTTP 码与限流头

HTTP 状态码通用含义（协议级约定，各家共用）：

| 状态码 | 含义 |
|---|---|
| 400 | 请求参数错误 / 请求格式不合法 |
| 401 | 鉴权失败（API Key 缺失或无效） |
| 402 | 余额不足 / 欠费 |
| 403 | 无访问权限 |
| 404 | 资源不存在（路径或模型 ID 错误） |
| 422 | 参数校验失败 |
| 429 | 触发限流 / 配额耗尽 |
| 500 | 服务端内部错误 |
| 503 | 服务暂不可用 / 过载 |

- 错误响应体通常为 JSON 对象，与 HTTP 状态码一同返回。
- 错误响应体形状**各家不同**：部分平台用自有的 `base_resp.status_code` / `status_msg` 结构；Anthropic 兼容端点为 `{"type": "error", "error": {"type", "message"}}`。字段级错误码清单请见各家章节（本切片未含各家 §8）。
- 限流相关响应头（`Retry-After`、`x-ratelimit-*` 等）为各家文档特有或本切片未收录，见各家章节。

> 各家差异（默认值 / 范围 / 不支持项 / 模型相关行为）见各家章节对应小节。

---

## DeepSeek

抓取自 DeepSeek 开放平台中文文档站（https://api-docs.deepseek.com/zh-cn/）全部页面，逐页抓取时间为本次任务执行时；文中只记录该站文档真实写明的内容，字段名保留英文原文，数值/默认值/枚举照抄。抓不到的页面在相应位置注明。

### 1. 端点与鉴权

| 端点 | Method | Path | base_url | 必需请求头 |
|---|---|---|---|---|
| 对话补全 | POST | `/chat/completions` | `https://api.deepseek.com` | `Content-Type: application/json`、`Authorization: Bearer <API key>` |
| 对话补全（Beta 功能：strict / 前缀续写 / FIM / 8K 输出） | POST | `/chat/completions`、`/completions` | `https://api.deepseek.com/beta` | 同上 |
| Responses API | POST | `/responses` | `https://api.deepseek.com` | 同上 |
| FIM 补全（Beta） | POST | `/completions` | `https://api.deepseek.com/beta` | 同上 |
| 获取模型列表 | GET | `/models` | `https://api.deepseek.com` | 同上 |
| 查询余额 | GET | `/user/balance` | `https://api.deepseek.com` | 同上 |
| 上传文件 | POST | `/files` | `https://api.deepseek.com` | `Authorization: Bearer <API key>`、`Content-Type: multipart/form-data`（表单字段 `file`、`purpose`） |
| 列出文件 | GET | `/files` | `https://api.deepseek.com` | `Authorization: Bearer <API key>` |
| 查询文件 | GET | `/files/:file_id` | `https://api.deepseek.com` | `Authorization: Bearer <API key>` |
| 删除文件 | DELETE | `/files/:file_id` | `https://api.deepseek.com` | `Authorization: Bearer <API key>` |
| Anthropic 兼容 Messages | POST | `/messages`（SDK 自动补 `/v1`，裸 HTTP 写 `/anthropic/v1/messages`） | `https://api.deepseek.com/anthropic` | `x-api-key` |
| Anthropic 兼容 Files | POST/GET/DELETE | `/anthropic/v1/files`、`/anthropic/v1/files/{file_id}` | `https://api.deepseek.com/anthropic` | `x-api-key`、`anthropic-beta: files-api-2025-04-14`（必须） |

补充事实：
- 文档首页示例给出 `base_url`：OpenAI 格式 `https://api.deepseek.com`，Anthropic 格式 `https://api.deepseek.com/anthropic`；`api_key` 需在开放平台申请。
- 带 `/v1` 的写法亦可用：`https://api.deepseek.com/v1/chat/completions`。
- Anthropic 兼容端点请求头支持情况：`x-api-key` 完全支持；`anthropic-version` 忽略；`anthropic-beta` 在 `/messages` 上忽略、Files API 端点必须携带 `files-api-2025-04-14`。
- 请求保活机制：等待模型响应期间 HTTP 连接保持，非流式请求持续返回空行，流式请求持续返回 SSE keep-alive 注释（`: keep-alive`）；若 10 分钟后请求仍未开始推理，服务器将关闭连接。

### 2. Chat Completions 请求字段总表

`POST /chat/completions`（`base_url` 为 `https://api.deepseek.com`）。

下表只列本家与《通用基线》§0.2 不同的字段与取值；未列出的标准字段语义同基线（本家文档未写明其默认值/范围即视为未列出）。

| 字段 | 类型 | 必填/默认 | 取值 | 说明 |
|---|---|---|---|---|
| `model` | string | 必填 | `deepseek-flash`、`deepseek-v4-pro` | 使用的模型 ID |
| `messages` | object[] | 必填 | 数组长度 `>= 1` | 对话消息列表 |
| `messages[].content` | string \| object[] \| null | system/user/tool 必填；assistant 必填（nullable） | 字符串或内容块数组 | user 消息可为内容块数组（可携带图片）；tool 消息可为内容块数组（可携带图片）；assistant 消息内容可为 null |
| `messages[].content[].type` | string | 必填（内容块数组内） | `text`、`image_url`、`file` | 内容块类型（基线只列 `text`） |
| `messages[].content[].text` | string | `type=text` 时必填 | — | 文本内容 |
| `messages[].content[].image_url` | object | `type=image_url` 时必填 | — | 图片内容块 |
| `messages[].content[].image_url.url` | string | 必填 | `http(s)` URL（最多 8192 个字符）或 base64 data URL（`data:image/jpeg;base64,...`） | 支持 JPEG、PNG、GIF、WebP |
| `messages[].content[].image_url.detail` | string | 选填 | `low`、`high`、`original`、`auto` | `low` 将图片缩小到 512x512（更快、更省 token）；`high`/`original`/`auto` 保留原图 |
| `messages[].content[].file_id` | string | 选填（与 `file_data` 互斥） | 形如 `file-api-...` | 通过 Files API 上传的文件 ID |
| `messages[].content[].file_data` | string | 选填（与 `file_id` 互斥） | base64 data URL | 图片以 base64 内联携带 |
| `messages[].content[].filename` | string | 选填 | — | 仅在配合 `file_data` 时有效 |
| `messages[].name` | string | 选填（system/user/assistant） | — | 参与者名称，帮助模型区分相同角色的参与者 |
| `messages[].prefix` | bool | 选填（assistant） | `true` | (Beta) 强制模型以该 assistant 消息中的前缀内容开始回答；必须设置 `base_url="https://api.deepseek.com/beta"` |
| `messages[].reasoning_content` | string \| null | 选填（assistant） | — | (Beta) 思考模式下对话前缀续写时，作为最后一条 assistant 思维链内容的输入；使用时 `prefix` 必须为 `true` |
| `thinking` | object \| null | 选填 | `{"type": "enabled"}` / `{"type": "disabled"}` | 控制思考模式与非思考模式转换。`type` 默认 `enabled`：`enabled` 使用思考模式，`disabled` 使用非思考模式 |
| `reasoning_effort` | string | 选填（默认强度 `high`） | `none`、`low`、`high`、`max` | `none` 关闭思考模式；`low`/`high`/`max` 开启思考模式。出于兼容：`minimal` 映射 `low`，`medium`/`xhigh` 映射 `high` |
| `max_tokens` | integer \| null | 选填 | 1 到 384K（393216） | 限制一次请求生成的最大 token 数。未设置时：非思考模式默认 8K，思考模式默认 64K（`reasoning_effort` 为 `max` 时为 128K）；输入+输出总长受上下文长度限制 |
| `response_format` | object \| null | 选填 | `{"type": "text"}`（默认）、`{"type": "json_object"}` | 指定模型必须输出的格式，`json_object` 启用 JSON 模式 |
| `stop` | string \| string[] \| null | 选填 | 一个 string 或最多 16 个 string 的 list | 遇到这些词时停止生成更多 token |
| `stream` | boolean \| null | 选填（本家未给默认） | `true`/`false` | `true` 时消息流以 `data: [DONE]` 结尾 |
| `stream_options` | object \| null | 选填 | 必须与 `stream: true` 一起使用 | 若 `stream` 未设为 `true`，API 返回 `400` 错误 |
| `stream_options.include_usage` | boolean | 选填 | `true`/`false` | `true` 时流式所有块都含 `usage` 字段（除最后一个块外值为 `null`）；不设置或 `false` 时除最后一个块外其余块不含 `usage` 字段。无论是否设置，`data: [DONE]` 之前的最后一个块都会在 `usage` 中给出整个请求的统计信息，且不会单独下发只含 usage 的块（统计信息附加在最后一个内容块上，该块 `choices` 始终只含一个元素、无新增内容且 `finish_reason` 非 null） |
| `temperature` | number \| null | 选填，默认 `1` | `<= 2` | 采样温度，介于 0 和 2 之间；思考模式下不生效 |
| `top_p` | number \| null | 选填，默认 `1` | `<= 1` | 仅在思考模式下生效，有效取值范围 0.95–1.0，低于 0.95 会按 0.95 处理；非思考模式下恒为 1.0，传入值被忽略 |
| `tools` | object[] \| null | 选填 | 目前仅支持 `function` | tool 名称必须唯一 |
| `tools[].function.name` | string | 必填 | 由 a-z、A-Z、0-9 组成，或包含下划线和连字符；最大长度 128 个字符 | 要调用的 function 名称 |
| `tools[].function.parameters` | object | 选填 | JSON Schema 对象 | 省略 `parameters` 会定义参数列表为空的 function |
| `tools[].function.strict` | boolean | 选填，默认 `false` | `true`/`false` | 设为 `true` 时 API 在函数调用中使用 strict 模式；Beta 功能，需 `base_url="https://api.deepseek.com/beta"` |
| `tool_choice` | string \| object \| null | 选填；无 tool 时默认 `none`，有 tool 时默认 `auto` | `none`、`auto`、`required`，或 `{"type": "function", "function": {"name": "my_function"}}` | `none` 不调用 tool；`auto` 可自行选择；`required` 必须调用一个或多个 tool；指定具体 tool 会强制调用该 tool。思考模式下不支持 `required` 和指定具体 tool，API 返回 `400` 错误 |
| `logprobs` | boolean \| null | 选填 | `true`/`false` | `true` 时在 `message` 的 `content` 中返回每个输出 token 的对数概率 |
| `top_logprobs` | integer \| null | 选填 | 0 到 20 | 指定每个输出位置返回输出概率 top N 的 token 及其对数概率；指定此参数时 `logprobs` 必须为 `true` |
| `user_id` | string \| null | 选填 | 字符集 `[a-zA-Z0-9\-_]`，最大长度 512 | 区分业务侧用户身份（内容安全处理）、KVCache 缓存隔离、调度隔离；请勿包含用户隐私信息 |
| `frequency_penalty` | — | deprecated | — | 该参数已不再支持，传入不会产生任何效果 |
| `presence_penalty` | — | deprecated | — | 该参数已不再支持，传入不会产生任何效果 |

补充：
- 使用 OpenAI SDK 设置 `thinking`、`user_id` 等非标准字段时需放入 `extra_body`。
- 文档首页示例的完整请求体为：`{"model":"deepseek-flash","messages":[{"role":"system","content":"You are a helpful assistant."},{"role":"user","content":"Hello!"}],"thinking":{"type":"enabled"},"reasoning_effort":"high","stream":false}`。
- Chat Completion 接口不支持在对话中间插入工具调用（但支持在对话中间插入 `system` 消息）；如需中间插入工具调用，应改用 Anthropic API（`/messages`）或 Responses API。
- 论坛/新闻页记录的工具数量约束：Function Calling 支持传入多个 Function（最多 128 个），支持并行 Function 调用。

### 3. Responses API 字段差异

`POST /responses`（`base_url` 为 `https://api.deepseek.com`）。该 API 是**无状态**的：服务端不存储响应与会话，多轮对话需客户端在每次请求的 `input` 中回传完整对话历史。标准结构（`input`、公共 item 类型、`instructions`、扁平 `tools[]`、`usage` 的 `input_tokens`/`output_tokens`/`total_tokens` 等）见《通用基线》§0.6。

| 字段 | 类型 | 必填/默认 | 取值 | 说明 |
|---|---|---|---|---|
| `model` | string | 必填 | `deepseek-flash`、`deepseek-v4-pro` | 使用的模型 ID |
| `input` | string \| object[] | 选填（`input` 与 `instructions` 至少传一个） | 纯字符串（视作一条 `user` 消息）或输入 item 列表 | 支持 item 类型 `message`/`function_call`/`function_call_output`/`custom_tool_call`/`custom_tool_call_output`/`reasoning`，其他类型被忽略；消息角色支持 `user`/`assistant`/`system`/`developer`（`developer` 视同 `user`）；`deepseek-flash` 时 `user`/`developer` 消息及 `function_call_output`/`custom_tool_call_output` 的 `output` 中支持 `input_image`；`system`/`assistant` 消息中的图片返回 `400`；文件输入不支持 |
| `input[].type` | string | 选填（`message` item 传了 `role` 时可省略） | `message`、`function_call`、`function_call_output`、`custom_tool_call`、`custom_tool_call_output`、`reasoning` | 输入 item 类型；`custom_tool_call`/`custom_tool_call_output` 配合 `apply_patch` custom 工具使用 |
| `input[].role` | string | 用于 `message` item | `user`、`assistant`、`system`、`developer` | `developer` 视同 `user` |
| `input[].content` | string \| object[] | 用于 `message` item | `input_text`/`output_text`/`input_image` 内容块列表；`reasoning` item 为 `reasoning_text` 内容块列表 | 消息内容 |
| `input[].content[].type` | string | 必填 | `input_text`、`output_text`、`input_image`、`reasoning_text` | 内容块类型 |
| `input[].content[].text` | string | 必填（文本/推理块） | — | 文本内容 / 思维链文本内容 |
| `input[].content[].image_url` | string | 与 `file_id` 互斥 | `http(s)` URL（最多 8192 字符）或 base64 data URL | 两者都不传返回 400（`input_image must have image_url or file_id`），都传返回 400（`input_image cannot have both image_url and file_id`） |
| `input[].content[].detail` | string | 选填 | `low`、`high`、`original`、`auto` | `low` 缩小到 512x512；设置 `file_id` 时该字段被忽略 |
| `input[].content[].file_id` | string | 与 `image_url` 互斥 | `file-api-...` | 通过 Files API 上传的图片文件 ID |
| `input[].call_id` | string | 用于 `function_call`/`function_call_output` | 必须非空且唯一 | 将函数调用与其结果配对；每个 `function_call` 必须有对应 `function_call_output` |
| `input[].name` | string | 用于 `function_call` | — | 要调用的函数名称 |
| `input[].arguments` | string | 用于 `function_call` | JSON 格式 | 调用函数入参 |
| `input[].output` | string \| object[] | 用于 `function_call_output`/`custom_tool_call_output` | 纯字符串或 `input_text`/`input_image` 内容块列表 | 工具调用结果 |
| `instructions` | string \| null | 选填 | — | 系统级指令，作为模型上下文中的第一条 system 消息 |
| `reasoning` | object \| null | 选填 | `{"effort": "none"/"low"/"high"/"max"}` | 思考模式配置。`none` 关闭思考；`low`/`high`/`max` 开启；不传时使用模型默认（默认开启）；`minimal`→`low`，`medium`/`xhigh`→`high`；`reasoning.summary` 可传入但不生成摘要 |
| `max_output_tokens` | integer \| null | 选填 | — | 响应可生成的 token 上限，含可见输出 token 与思维链 token（对应 Chat 的 `max_tokens`） |
| `stream` | boolean \| null | 选填 | `true`/`false` | `true` 时以语义化 SSE 事件返回，最后一个事件是 `response.completed`/`response.incomplete`/`response.failed`（**没有** `data: [DONE]` 消息） |
| `temperature` | number \| null | 选填，默认 `1` | `<= 2` | 思考模式下不生效 |
| `top_p` | number \| null | 选填，默认 `1` | `<= 1` | 仅思考模式生效，下限 0.95；非思考模式恒 1.0 |
| `text` | object \| null | 选填 | — | 文本输出配置 |
| `text.format` | object | 选填，默认 `{"type": "text"}` | `{"type":"text"}`、`{"type":"json_object"}`、`{"type":"json_schema","name":...,"schema":...}` | 纯文本 / JSON 模式 / 结构化输出；`type` 为 `json_schema` 时 `name` 与 `schema` 必填；`text.verbosity` 可传入但不生效 |
| `tools` | object[] \| null | 选填 | `type` 为 `function` | 函数名必须非空、不超过 128 个字符、匹配 `^[a-zA-Z0-9_-]+$`，所有工具名称必须唯一；内置工具类型会被忽略 |
| `tools[].name` / `tools[].description` / `tools[].parameters` | string / string / object | `name` 用于 function | 同 Chat：`parameters` 为 JSON Schema，省略则参数列表为空 | 注意 Responses 的工具字段平铺在工具对象上（无 `function` 包裹层） |
| `tool_choice` | string \| object \| null | 选填，默认 `auto` | `none`、`auto`、`required`，或 `{"type": "function", "name": "my_function"}` | 指定特定工具会强制调用该工具 |
| `top_logprobs` | integer \| null | 选填 | `<= 20`（范围 [0, 20]） | 返回输出概率 top N 的 token 及其对数概率 |
| `user` | string \| null | 选填 | 字符集 `[a-zA-Z0-9\-_]`，最大长度 512 | 对应 Chat 的 `user_id`，用于内容安全审核、KVCache 隔离与调度隔离 |

Responses 顶层参数兼容性表（文档原文）：

| 参数 | 支持情况 |
|---|---|
| `model` | 支持（`deepseek-flash`） |
| `input` | 支持。字符串或输入 item 列表；`input` 与 `instructions` 至少传一个 |
| `instructions` | 支持。作为第一条 system 消息 |
| `stream` | 支持 |
| `temperature` | 支持（范围 [0.0, 2.0]；思考模式下不生效） |
| `top_p` | 支持（思考模式下生效，下限 0.95；非思考模式下恒 1.0） |
| `max_output_tokens` | 支持 |
| `top_logprobs` | 支持（范围 [0, 20]） |
| `tools` | 部分支持。function 支持；其他类型忽略 |
| `tool_choice` | 支持。`none` / `auto` / `required` / 指定某个工具 |
| `reasoning` | 部分支持。`effort` 支持；`summary` 可传入但不生成摘要 |
| `text` | 部分支持。`format` 完整支持；`verbosity` 可传入但不生效 |
| `user` | 支持 |
| `parallel_tool_calls` | 忽略（并行工具调用始终开启） |
| `max_tool_calls` | 忽略 |
| `previous_response_id` | 不支持（无状态 API） |
| `conversation` | 不支持（无状态 API） |
| `store` | 不支持。响应中恒为 `store: false` |
| `background` | 不支持 |
| `metadata` | 不支持 |
| `include` | 不支持 |
| `prompt` | 不支持 |
| `truncation` | 不支持。输入超出上下文窗口时返回 400 错误 |
| `service_tier` | 不支持 |
| `safety_identifier` | 不支持 |
| `prompt_cache_key` / `prompt_cache_retention` | 不支持。上下文缓存自动管理 |
| `context_management` | 不支持 |
| `stream_options` | 不支持 |

不支持的参数会被**静默忽略**、不会报错。输入 item 支持情况：`message`（支持，角色 user/assistant/system/developer；`content` 支持字符串与 `input_text`/`output_text`/`input_image`；文件输入不支持）、`function_call`（支持，归并到相邻 assistant 消息）、`function_call_output`（支持）、`reasoning`（支持，明文 content 归并到相邻 assistant 消息；`summary`、`encrypted_content` 不支持）、`custom_tool_call` / `custom_tool_call_output`（支持，配合 `apply_patch`，含 `call_id` 配对校验）、其他类型（忽略）。`input` 中回传的 `web_search_call` item 仍会被还原并拼接进上下文。Tools 支持情况：`function` 支持；`custom` 仅支持 `{"type": "custom", "name": "apply_patch"}`（用于 Codex 兼容），其他名称返回 400；`web_search` / `file_search` / `code_interpreter` / `computer_use` / `mcp` 等内置工具忽略。

### 4. Anthropic 兼容端点字段差异

`base_url` 为 `https://api.deepseek.com/anthropic`（Anthropic SDK 会自动补 `/v1`）。标准骨架字段（`system`、`stop_sequences`、`tools[].input_schema`、`tool_choice.type`、`content` 内容块、`stop_reason`、流式事件序列等）见《通用基线》§0.7。

模型映射（文档原文）：

| 传入模型名 | 映射结果 |
|---|---|
| `claude-opus` 开头的模型 | `deepseek-v4-pro`（按 V4 Pro 价格计费） |
| `claude-haiku`、`claude-sonnet` 开头的模型 | `deepseek-flash` |
| 其他不支持的模型名 | 自动映射到 `deepseek-flash` |

HTTP Header：

| 字段 | 支持情况 |
|---|---|
| `anthropic-beta` | `/messages` 忽略；Files API 端点必须携带（`files-api-2025-04-14`） |
| `anthropic-version` | 忽略 |
| `x-api-key` | 完全支持 |

简单字段：

| 字段 | 支持情况 |
|---|---|
| `model` | 改为使用 DeepSeek 模型 |
| `max_tokens` | 完全支持 |
| `system` | 完全支持 |
| `messages` | 完全支持（内容块变体见下表） |
| `stream` | 完全支持 |
| `stop_sequences` | 完全支持 |
| `temperature` | 完全支持（范围 [0.0 ~ 2.0]） |
| `top_p` | 仅思考模式下生效（下限 0.95）；非思考模式下恒为 1.0 |
| `top_k` | 忽略 |
| `thinking` | 支持（`budget_tokens` 被忽略） |
| `output_config` | 仅支持 `effort` |
| `metadata` | 支持 `user_id`，其它字段忽略 |
| `container`、`mcp_servers`、`service_tier` | 忽略 |

思考控制（Anthropic 格式拼写，与 OpenAI 格式不同）：

| 目标 | Anthropic 格式写法 |
|---|---|
| 思考模式开关 | `{"reasoning": {"effort": "none/low/high/max"}}`（`none` 表示关闭思考模式） |
| 思考强度控制 | `{"output_config": {"effort": "low/high/max"}}` |

Tool 字段：

| 字段/取值 | 支持情况 |
|---|---|
| `tools[].name` | 完全支持 |
| `tools[].input_schema` | 完全支持 |
| `tools[].description` | 完全支持 |
| `tools[].cache_control` | 忽略 |
| `tool_choice: none` | 完全支持 |
| `tool_choice: auto` | 支持（`disable_parallel_tool_use` 被忽略） |
| `tool_choice: any` | 支持（`disable_parallel_tool_use` 被忽略） |
| `tool_choice: tool` | 支持（`disable_parallel_tool_use` 被忽略） |

Message `content` 变体：

| 变体 | 子字段 | 支持情况 |
|---|---|---|
| string | — | 完全支持 |
| array，`type="text"` | `text` 完全支持；`cache_control`、`citations` 忽略 | 完全支持 |
| array，`type="image"` | `source` 支持；`source.type` 可为 `base64`（媒体类型 jpeg、png、gif、webp）、`url` 或 `file`（`file` 形式需带请求头 `anthropic-beta: files-api-2025-04-14`）；`url` 最多 8192 个字符 | 支持 |
| array，`type="document"` | — | 不支持 |
| array，`type="search_result"` | — | 不支持 |
| array，`type="thinking"` | — | 支持 |
| array，`type="redacted_thinking"` | — | 不支持 |
| array，`type="tool_use"` | `id`、`input`、`name` 完全支持；`cache_control` 忽略 | 完全支持 |
| array，`type="tool_result"` | `tool_use_id`、`content` 完全支持；`cache_control`、`is_error` 忽略 | 完全支持 |
| array，`type="server_tool_use"` | — | 支持 |
| array，`type="web_search_tool_result"` | — | 支持 |
| array，`type="code_execution_tool_result"` | — | 不支持 |
| array，`type="mcp_tool_use"` | — | 不支持 |
| array，`type="mcp_tool_result"` | — | 不支持 |
| array，`type="container_upload"` | — | 不支持 |

其他差异：
- Anthropic 兼容端点返回的文件对象与 OpenAI 兼容版本字段不同：`type`（而非 `object`）、`size_bytes`（而非 `bytes`）、`created_at` 为 RFC 3339 字符串（而非 Unix 秒），并含 `mime_type`。
- Anthropic 兼容端点支持在对话中间插入工具调用消息与 `system` 消息。
- 通过 Anthropic `/messages` 引用 Files API 文件时必须携带 `anthropic-beta: files-api-2025-04-14`。
- 通过 `GET /models` 可按模型声明 Anthropic Messages 的 system 提示词更新方式：`deepseek-flash` 为 `in-history`（可在对话历史中追加 `system` 消息，历史中最新一条 `system` 消息提供完整有效提示词并替代此前所有 system 提示词），`deepseek-v4-pro` 为 `leading-only`（只有对话开头的 system 提示词生效）。

### 5. 响应字段与流式结构

非流式顶层字段 `id`/`created`/`model`/`choices`/`usage`、`choices[].index`、`choices[].message.role`、`tool_calls[]` 的 `id`/`type`/`function.name` 等标准形状见《通用基线》§0.5；以下只列本家特有项。

**Chat Completions 非流式响应**（`object` 为 `chat.completion`）

| 字段 | 类型 | 必填/默认 | 取值 | 说明 |
|---|---|---|---|---|
| `object` | string | 必填 | `chat.completion` | 对象类型 |
| `system_fingerprint` | string | 必填 | — | 后端配置指纹（示例值 `fp_7a09fdf9c2`） |
| `choices[].finish_reason` | string | 必填 | `stop`、`length`、`content_filter`、`tool_calls`、`insufficient_system_resource`、`aborted` | 停止生成的原因：`stop` 自然停止或命中 `stop` 序列；`length` 达到上下文或 `max_tokens` 限制；`content_filter` 内容被过滤；`tool_calls` 进行了工具调用；`insufficient_system_resource` 系统推理资源不足被打断；`aborted` 生成被中断 |
| `choices[].message.content` | string \| null | 必填 | — | completion 内容（可为 null） |
| `choices[].message.reasoning_content` | string \| null | 选填 | — | 仅适用于思考模式；assistant 消息中最终答案之前的推理内容 |
| `choices[].message.tool_calls[].function.arguments` | string | 必填 | JSON 字符串 | 模型生成的参数（不保证是合法 JSON，可能臆造未定义参数，调用前应校验） |
| `choices[].logprobs` | object \| null | 选填 | — | 该 choice 的对数概率信息 |
| `choices[].logprobs.content[]` | object[] \| null | `logprobs` 开启时必填 | 元素含 `token`（必填）、`logprob`（必填，`-9999.0` 表示概率极小、不在 top 20 内）、`bytes`（integer[]\|null，UTF-8 字节表示）、`top_logprobs[]`（含同样三个子字段） | 输出 token 的对数概率列表；`top_logprobs` 返回数量在罕见情况下可能少于请求值 |
| `choices[].logprobs.reasoning_content[]` | object[] \| null | — | 同上结构 | 推理内容对应的对数概率列表 |

**usage 明细字段**（Chat 与 FIM 相同）

| 字段 | 类型 | 必填/默认 | 取值 | 说明 |
|---|---|---|---|---|
| `usage.completion_tokens` | integer | 必填 | — | 模型 completion 产生的 token 数 |
| `usage.prompt_tokens` | integer | 必填 | — | 用户 prompt 的 token 数，等于 `prompt_cache_hit_tokens + prompt_cache_miss_tokens` |
| `usage.total_tokens` | integer | 必填 | — | 全部 token 数（prompt + completion） |
| `usage.prompt_tokens_details.cached_tokens` | integer | 选填 | — | 命中上下文缓存的 token 数，与 `prompt_cache_hit_tokens` 相同 |
| `usage.prompt_cache_hit_tokens` | integer | 必填 | — | prompt 中命中上下文缓存的 token 数 |
| `usage.prompt_cache_miss_tokens` | integer | 必填 | — | prompt 中未命中上下文缓存的 token 数 |
| `usage.completion_tokens_details.reasoning_tokens` | integer | 选填 | — | 推理模型产生的思维链 token 数量 |

**SSE chunk 结构（Chat 流式）**：`object` 为 `chat.completion.chunk`，每个 chunk 字段为 `id`、`choices[]`、`created`（流式响应每个 chunk 时间戳相同）、`model`、`system_fingerprint`。`choices[]` 内为：

| 字段 | 说明 |
|---|---|
| `delta.content` | completion 增量的内容 |
| `delta.reasoning_content` | 仅思考模式；最终答案之前的推理内容增量 |
| `delta.role` | 产生这条消息的角色（`assistant`） |
| `delta.tool_calls[]` | 每个 tool 调用的第一个 chunk 携带 `index`（必填）、`id`、`type`（`function`）和 `function` 字段，后续 chunk 只携带 function 参数 |
| `logprobs` | 该 choice 的对数概率信息（结构同非流式） |
| `finish_reason` | string \| null；正常 chunk 为 `null`，结束 chunk 给出枚举值 |
| `index` | 该 completion 的索引 |

流式结束标记：最后一条为 `data: [DONE]`。实际示例（文档原文）：

```
data: {"id": "1f633d8bfc032625086f14113c411638", "choices": [{"index": 0, "delta": {"content": "", "role": "assistant"}, "finish_reason": null, "logprobs": null}], "created": 1718345013, "model": "deepseek-flash", "system_fingerprint": "fp_a49d71b8a1", "object": "chat.completion.chunk"}

data: {"choices": [{"delta": {"content": "Hello", "role": "assistant"}, "finish_reason": null, "index": 0, "logprobs": null}], "created": 1718345013, "id": "1f633d8bfc032625086f14113c411638", "model": "deepseek-flash", "object": "chat.completion.chunk", "system_fingerprint": "fp_a49d71b8a1"}

data: {"choices": [{"delta": {"content": "", "role": null}, "finish_reason": "stop", "index": 0, "logprobs": null}], "created": 1718345013, "id": "1f633d8bfc032625086f14113c411638", "model": "deepseek-flash", "object": "chat.completion.chunk", "system_fingerprint": "fp_a49d71b8a1", "usage": {"completion_tokens": 9, "prompt_tokens": 17, "total_tokens": 26, "prompt_tokens_details": {"cached_tokens": 0}, "prompt_cache_hit_tokens": 0, "prompt_cache_miss_tokens": 17}}

data: [DONE]
```

**Responses API 响应字段**（`object` 为 `response`）

| 字段 | 类型 | 说明 |
|---|---|---|
| `id` | string | 该响应的唯一标识符 |
| `object` | string | 恒为 `response` |
| `created_at` | integer | 创建时间（Unix 秒） |
| `status` | string | `in_progress`、`completed`、`incomplete`、`failed` |
| `error` | object \| null | 失败时的错误对象，包含 `code` 与 `message` |
| `incomplete_details.reason` | string | `max_output_tokens` 或 `content_filter` |
| `model` | string | 生成该响应的模型 |
| `output[]` | object[] | 输出 item 列表；思考模式下思维链以 `reasoning` item 在 `message` item 之前返回，函数调用以 `function_call` item 返回 |
| `output[].type` | string | `message`、`reasoning`、`function_call` |
| `output[].id` | string | 输出 item 的唯一 ID |
| `output[].status` | string | `in_progress`、`completed`、`incomplete` |
| `output[].role` | string | 用于 `message` item，恒为 `assistant` |
| `output[].content[]` | object[] | `message` item 为 `output_text` 块列表；`reasoning` item 为 `reasoning_text` 块列表（明文承载思维链）；元素字段 `type`（`output_text`/`reasoning_text`）与 `text` |
| `output[].call_id` / `output[].name` / `output[].arguments` | string | 用于 `function_call` item：结果回传标识符 / 函数名 / 模型生成的 JSON 入参 |
| `usage` | object | token 用量统计 |
| `usage.input_tokens` | integer | 输入 token 数 |
| `usage.input_tokens_details.cached_tokens` | integer | 命中上下文缓存的输入 token 数 |
| `usage.output_tokens` | integer | 输出 token 数 |
| `usage.output_tokens_details.reasoning_tokens` | integer | 思维链 token 数 |
| `usage.total_tokens` | integer | 总数（输入 + 输出） |
| `store` | boolean | 恒为 `false` |
| `parallel_tool_calls` | boolean | 恒为 `true` |
| `previous_response_id` | null | 恒为 `null` |

Responses 流式：每个事件带 `event` 字段（事件类型）与递增的 `sequence_number`，结束事件为 `response.completed` / `response.incomplete` / `response.failed`，没有 `data: [DONE]`。事件列表：`response.created`（首事件，状态 `in_progress`）、`response.in_progress`、`response.output_item.added` / `response.output_item.done`（reasoning/message/function_call/custom_tool_call 开始/完成）、`response.content_part.added` / `response.content_part.done`、`response.reasoning_text.delta` / `response.reasoning_text.done`、`response.output_text.delta` / `response.output_text.done`、`response.function_call_arguments.delta` / `response.function_call_arguments.done`、`response.custom_tool_call_input.delta` / `response.custom_tool_call_input.done`、`response.completed` / `response.incomplete` / `response.failed`。事件示例（文档原文）：

```
event: response.created
data: {"type": "response.created", "sequence_number": 0, "response": {"id": "...", "object": "response", "status": "in_progress", ...}}

event: response.output_item.added
data: {"type": "response.output_item.added", "sequence_number": 2, "output_index": 0, "item": {"type": "reasoning", ...}}

event: response.reasoning_text.delta
data: {"type": "response.reasoning_text.delta", "sequence_number": 4, "item_id": "rs_1", "output_index": 0, "content_index": 0, "delta": "The user"}

event: response.output_item.added
data: {"type": "response.output_item.added", "sequence_number": 9, "output_index": 1, "item": {"type": "message", "role": "assistant", ...}}

event: response.output_text.delta
data: {"type": "response.output_text.delta", "sequence_number": 11, "item_id": "msg_1", "output_index": 1, "content_index": 0, "delta": "Hello"}

event: response.completed
data: {"type": "response.completed", "sequence_number": 20, "response": {"id": "...", "object": "response", "status": "completed", "usage": {...}, ...}}
```

**FIM 补全响应**（`POST /completions`，`object` 为 `text_completion`）

| 字段 | 类型 | 说明 |
|---|---|---|
| `id` | string | 补全响应 ID |
| `object` | string | 恒为 `text_completion` |
| `created` | integer | 补全请求开始时间的 Unix 时间戳（秒） |
| `model` | string | 所用模型 |
| `system_fingerprint` | string | 后端配置指纹 |
| `choices[].text` | string | 补全文本 |
| `choices[].index` | integer | — |
| `choices[].finish_reason` | string | `stop`、`length`、`content_filter`、`insufficient_system_resource`、`aborted`（无 `tool_calls`） |
| `choices[].logprobs` | object \| null | 含 `text_offset`（integer[]）、`token_logprobs`（number[]）、`tokens`（string[]）、`top_logprobs`（object[]） |
| `usage` | object | 结构同 Chat：`completion_tokens`、`prompt_tokens`、`prompt_tokens_details.cached_tokens`、`prompt_cache_hit_tokens`、`prompt_cache_miss_tokens`、`total_tokens`、`completion_tokens_details.reasoning_tokens` |

### 6. 思考/推理字段

思考模式默认打开（effort 默认为 `high`）。同一语义在三种协议上的拼写（文档原文对照表）：

| 目标 | OpenAI 格式（Chat Completions） | Anthropic 格式 | Responses API 格式 |
|---|---|---|---|
| 思考模式开关 | `{"thinking": {"type": "enabled/disabled"}}` | `{"reasoning": {"effort": "none/low/high/max"}}`（`none` 表示关闭思考模式） | —（由 `reasoning.effort` 承担，`none` 关闭） |
| 思考强度控制 | `{"reasoning_effort": "low/high/max"}` | `{"output_config": {"effort": "low/high/max"}}` | `{"reasoning": {"effort": "low/high/max"}}` |

字段级取值：

| 字段（协议） | 类型 | 必填/默认 | 取值 | 说明 |
|---|---|---|---|---|
| `thinking.type`（Chat） | string | 选填，默认 `enabled` | `enabled`、`disabled` | `enabled` 使用思考模式；`disabled` 使用非思考模式 |
| `reasoning_effort`（Chat） | string | 选填，默认 `high` | `none`、`low`、`high`、`max` | `none` 关闭思考模式；`low`/`high`/`max` 开启思考模式。出于兼容考虑 `minimal` 映射为 `low`，`medium`/`xhigh` 映射为 `high` |
| `reasoning.effort`（Responses） | string | 选填（默认开启） | `none`、`low`、`high`、`max` | `none` 关闭思考；不传时使用模型默认的思考行为 |
| `output_config.effort`（Anthropic） | string | 选填 | `low`、`high`、`max` | 仅支持 `effort`；关闭思考用 `reasoning.effort: none` |
| `thinking.budget_tokens`（Anthropic） | — | — | — | 被忽略 |
| `reasoning.summary`（Responses） | — | — | — | 可传入但不生成摘要 |
| `messages[].reasoning_content`（Chat，assistant 消息输入） | string \| null | 选填 | — | (Beta) 对话前缀续写下作为最后一条 assistant 思维链内容的输入；使用时 `prefix` 必须为 `true` |
| `choices[].message.reasoning_content` / `delta.reasoning_content`（响应） | string \| null | 选填 | — | 思考模式下思维链内容，与 `content` 同级 |
| `output[].content[].type = reasoning_text`（Responses 响应） | string | — | `reasoning_text` | 明文承载思维链内容 |
| `usage.completion_tokens_details.reasoning_tokens`（Chat usage） | integer | 选填 | — | 思维链 token 数量 |
| `usage.output_tokens_details.reasoning_tokens`（Responses usage） | integer | 选填 | — | 思维链 token 数 |

请求传入 effort 与实际映射 effort 的换算表（文档原文）：

| 请求传入 effort | 实际映射 effort |
|---|---|
| `minimal` | `low` |
| `low` | `low` |
| `medium` | `high` |
| `high` | `high` |
| `xhigh` | `high` |
| `max` | `max` |
| `ultra` | `max` |

思考模式对采样参数的影响（文档原文）：
- 思考模式不支持 `temperature`、`presence_penalty`、`frequency_penalty`。为了兼容已有软件，设置这些参数不会报错，但也不会生效。
- `top_p` 仅在思考模式下生效，有效取值范围为 `0.95`–`1.0`，低于 `0.95` 的取值会按 `0.95` 处理；在非思考模式下该参数恒为 `1.0`，传入的值会被忽略。

回传 `reasoning_content` 的硬要求（文档原文）：
- 请求**携带 `tools` 参数**时：历史轮次的 `reasoning_content` 均应回传给 API，并会被拼接进上下文。携带了 `tools` 参数的请求，在后续所有请求中必须完整回传 `reasoning_content`——即使该轮模型未实际进行工具调用；若代码中未正确回传 `reasoning_content`，API 会返回 400 报错。
- 请求**未携带 `tools` 参数**时：`reasoning_content` 无需回传；即使传入 API 也会被忽略，不会拼接进上下文，因此在下一轮对话中之前的思维链不会被拼进上下文。
- 使用 OpenAI SDK 时 `response.choices[0].message` 已携带 `assistant` 消息的全部必要字段（`content`、`reasoning_content`、`tool_calls`），可直接 `messages.append(response.choices[0].message)`。
- 在 OpenAI SDK 中设置 `thinking` 参数需放入 `extra_body`，例如：`client.chat.completions.create(model="deepseek-flash", reasoning_effort="high", extra_body={"thinking": {"type": "enabled"}})`。
- 思考模式下 `max_tokens` 默认 64K，`reasoning_effort` 为 `max` 时默认 128K；非思考模式默认 8K。

### 7. 工具调用字段

`tools[]` 元素形态、`tool_choice` 默认形态、`finish_reason: "tool_calls"`、`role: "tool"` + `tool_call_id` 回传等公共结构见《通用基线》§0.4；以下只列本家差异与专属项。

| 字段 | 类型 | 必填/默认 | 取值 | 说明 |
|---|---|---|---|---|
| `tools` | object[] | 选填 | 元素 `type` 目前仅支持 `function` | tool 名称必须唯一。Function Calling 支持传入多个 Function（最多 128 个），支持并行 Function 调用 |
| `tools[].function.name` | string | 必填 | 由 a-z、A-Z、0-9 组成，或包含下划线和连字符，最大长度 128 个字符 | 要调用的 function 名称 |
| `tools[].function.parameters` | object | 选填 | JSON Schema 对象 | 省略 `parameters` 会定义参数列表为空的 function |
| `tools[].function.strict` | boolean | 选填，默认 `false` | `true`/`false` | `true` 时在函数调用中使用 strict 模式，确保输出符合函数的 JSON schema 定义（Beta 功能） |
| `tool_choice` | string \| object | 选填；无 tool 时默认 `none`，有 tool 时默认 `auto` | `none`、`auto`、`required`；`{"type": "function", "function": {"name": "my_function"}}` | 思考模式下不支持 `required` 和指定具体 tool，API 返回 `400` 错误，需先关闭思考模式 |
| `messages[].role = tool` + `messages[].tool_call_id` | string | tool 消息必填 | — | 工具结果消息：`tool_call_id` 为此消息所响应的 tool call 的 ID，`content` 可为字符串或内容块数组（可携带图片） |
| `choices[].message.tool_calls[]` | object[] | 选填 | `id`（必填）、`type`（`function`）、`function.name`、`function.arguments`（JSON 字符串） | 响应侧工具调用 |
| 流式 `delta.tool_calls[]` | object[] | — | `index`（必填）、`id`、`type`、`function` | 每个 tool 调用的第一个 chunk 携带 `id`、`type` 和 `function` 字段，后续 chunk 只携带 function 参数 |
| Responses：`tools[].type` / `name` / `description` / `parameters` | string / string / string / object | `name` 用于 function | 函数名必须非空、不超过 128 个字符、匹配 `^[a-zA-Z0-9_-]+$`，所有工具名称必须唯一 | 内置工具类型会被忽略 |
| Responses：`tool_choice` | string \| object | 选填，默认 `auto` | `none`、`auto`、`required`，或 `{"type": "function", "name": "my_function"}` | — |
| Responses：`input[].type = function_call` / `function_call_output` | string | — | `call_id`（必须非空且唯一，每个 call 必须有对应 output）、`name`、`arguments` / `output` | `custom_tool_call` / `custom_tool_call_output` 配合 `apply_patch` custom 工具使用 |
| Responses：`parallel_tool_calls` | boolean | — | 忽略（并行工具调用始终开启） | — |
| Responses：`max_tool_calls` | — | — | 忽略 | — |
| Anthropic：`tools[].name` / `input_schema` / `description` | — | — | 完全支持 | `cache_control` 忽略 |
| Anthropic：`tool_choice` | — | — | `none`（完全支持）、`auto`、`any`、`tool`（后三者中 `disable_parallel_tool_use` 被忽略） | — |

思考模式下的工具调用（文档原文要点）：
- 从 DeepSeek-V3.2 开始，API 支持思考模式下的工具调用；模型在输出最终答案前可进行多轮思考与工具调用。
- 携带了 `tools` 参数的请求，在后续所有请求中必须完整回传 `reasoning_content`（即使该轮未实际调用工具），否则 API 返回 400 报错。
- 在 Turn 1 的每个子请求中都要携带该 Turn 下产生的 `reasoning_content`；Turn 2 的请求仍然携带 Turn 1 产生的 `reasoning_content`。

在对话中间插入工具调用：Anthropic API（`/messages`）与 Responses API 支持在对话中间插入工具调用消息，也支持在对话中间插入 `system` 消息；Chat Completion 接口不支持在对话中间插入工具调用，但支持在对话中间插入 `system` 消息。

`strict` 模式（Beta）要求：
- 用户需设置 `base_url="https://api.deepseek.com/beta"` 开启 Beta 功能。
- 传入的 `tools` 列表中所有 `function` 均需设置 `strict` 属性为 `true`。
- 服务端会校验用户传入 Function 的 JSON Schema，不符合规范或遇到不支持的 JSON Schema 类型会返回错误信息。
- 支持的类型：object、string、number、integer、boolean、array、enum、anyOf；另可使用 `$def` 定义模块、`$ref` 引用（含递归结构）。
- object 类型：每个 object 的所有属性均需设置为 `required`，且 `additionalProperties` 必须为 `false`。
- string 类型：支持 `pattern`、`format`（支持 `email`、`hostname`、`ipv4`、`ipv6`、`uuid`）；不支持 `minLength`、`maxLength`。
- number/integer 类型：支持 `const`、`default`、`minimum`、`maximum`、`exclusiveMinimum`、`exclusiveMaximum`、`multipleOf`。
- array 类型：不支持 `minItems`、`maxItems`。
- enum 可确保输出为预期选项之一；anyOf 匹配所提供的多个 schema 中的任意一个。

### 8. 多模态与文件

本节按四张表整理：① 输入模态总表（文本 / 图片 / 音频 / 视频 / 文件）；② 输出模态总表（是否存在图片生成 / 语音合成 / 视频生成等非文本产出）；③ 文件上传与管理 API（Files API）；④ 相关端点与限制汇总。

#### ① 输入模态总表

该站文档中可用的非文本输入只有**图片**一种；音频、视频与非图片文件输入文档中均未提供（见下表末三行）。图片格式由文件实际内容判断，而非文件名或声明的 MIME 类型。

| 输入模态 | 支持的协议 | 字段名与嵌套结构 | 传法 | 格式白名单 | 单文件体积上限 | 张数上限 | 支持的模型 |
|---|---|---|---|---|---|---|---|
| 文本 | Chat Completions（`POST /chat/completions`）；Responses（`POST /responses`）；Anthropic 兼容 Messages（`POST /anthropic/v1/messages`）；FIM 补全（Beta，`POST /completions`） | Chat：`messages[].content` 为字符串，或内容块数组中的 `{"type": "text", "text": ...}`；Responses：`input` 传纯字符串（视作一条 `user` 消息），或 `input[].content[]` 中的 `{"type": "input_text", "text": ...}`（`output_text` 结构相同），`instructions` 作为第一条 `system` 消息；Anthropic：`messages[].content` 字符串或 `{"type": "text", "text": ...}` 块、顶层 `system`；FIM：`prompt`（必填）、`suffix` | 请求体内联字符串 | 不适用（文档未对文本内容设格式白名单） | 文档未给出（整体受上下文长度与 `max_tokens` / `max_output_tokens` 限制） | 不适用 | `deepseek-flash`、`deepseek-v4-pro` |
| 图片 | Chat Completions；也可用于 Chat 的 `tool` 消息内容 | `messages[].content[]` 中的 `{"type": "image_url", "image_url": {"url": ..., "detail": ...}}`，或 `{"type": "file", "file_id": "file-api-..."}`；`file` 块也可写作 `{"type": "file", "file_data": "data:image/jpeg;base64,...", "filename": "image.jpg"}` | ① http(s) URL；② base64 data URL（`data:image/jpeg;base64,...`）；③ Files API `file_id`；④ `file_data` 内联 base64（`file_data` 与 `file_id` 互斥，`filename` 不能与 `file_id` 同时出现） | JPEG、PNG、GIF、WebP | base64 与外部 URL 单张最大 32 MiB；`file_id` 单张最大 64 MiB（不受 32 MiB 单图检查限制）；外部 URL 长度最多 8192 个字符，且需在 60 秒内完成下载 | 单个请求最大 600 张 | `deepseek-flash`（旧模型名 `deepseek-v4-flash-vision-exp` 仍可调用，但该模型已下线，其请求同样由最新的 Flash 模型承接）；`deepseek-v4-pro` 不支持 |
| 图片 | Responses（`POST /responses`） | `input[].content[]` 中的 `{"type": "input_image", "image_url": ..., "detail": ..., "file_id": ...}`；也可出现在 `function_call_output` / `custom_tool_call_output` item 的 `output` 中 | ① http(s) URL（最多 8192 个字符）；② base64 data URL；③ Files API `file_id`（与 `image_url` 互斥） | JPEG、PNG、GIF、WebP | 文档原文称“三种传入方式与限制均与上文一致”（即 base64 / 外部 URL 单张 32 MiB、`file_id` 单张 64 MiB） | 文档原文称与上文一致（单个请求最大 600 张） | `deepseek-flash`（文档：使用 `deepseek-flash` 时，`user` / `developer` 消息及 `function_call_output` / `custom_tool_call_output` 的 `output` 中支持 `input_image`） |
| 图片 | Anthropic 兼容 Messages（`POST /anthropic/v1/messages`，`base_url` 为 `https://api.deepseek.com/anthropic`） | `messages[].content[]` 中的 `{"type": "image", "source": {...}}`，`source.type` 为 `base64` / `url` / `file` 之一 | ① `source.type=base64`（需 `media_type` 字段）；② `source.type=url`；③ `source.type=file`（Files API `file_id`，需请求头 `anthropic-beta: files-api-2025-04-14`） | `media_type` 可为 `image/jpeg`、`image/png`、`image/gif`、`image/webp` | 该节文档未单独给出（只写明 `url` 最多 8192 个字符；文档称三种 `source` 变体与 OpenAI 的三种方式一一对应） | 该节文档未单独给出 | 文档未按模型单独说明；图像理解页的 Anthropic 示例使用 `deepseek-flash` |
| 音频 | 文档中未提供 | 文档未给出 | 文档未给出 | 文档未给出 | 文档未给出 | 文档未给出 | 文档未提供音频输入；`GET /models` 的 `input_modalities` 为 `["text", "image"]`（`deepseek-flash`）与 `["text"]`（`deepseek-v4-pro`），不含 `audio` |
| 视频 | 文档中未提供 | 文档未给出 | 文档未给出 | 文档未给出 | 文档未给出 | 文档未给出 | 文档未提供视频输入；`GET /models` 的 `input_modalities` 不含 `video` |
| 文件（非图片文件，如 PDF / 文档） | 文档中未提供 | Chat 侧的 `file` 块仅用于图片（`file_id` / `file_data`）；Responses 明确写“文件输入不支持”；Anthropic 侧的 `type="document"` 不支持 | 文档未给出 | Files API 上传字段 `file` 为“要上传的图片文件”，格式仅 JPEG、PNG、GIF、WebP | 文档未给出（非图片文件不可上传） | 文档未给出 | 文档未给出 |

图片相关补充（均为文档原文要点）：
- `detail` 取值（`image_url` / `input_image` 输入专用；Responses 设置 `file_id` 时该字段被忽略）：`low` 推理前将图片缩放到 512×512，当不需要精细视觉细节时更快、更省 token；`high` 保留原图（为兼容性提供，等价于 `original`）；`original` 保留原图；`auto` 自动选择，当前等价于 `original`。
- 使用位置限制：Chat 侧图片仅支持出现在 `user` 消息中，`system` 或 `assistant` 消息携带图片会返回 400 错误；Responses 侧图片仅允许出现在 `user` / `developer` 消息 item 以及 `function_call_output` / `custom_tool_call_output` 的 `output` 中，`system` / `assistant` 消息中的图片返回 400 错误。
- Responses `input_image` 的 400 场景：两者都不传返回 400（`input_image must have image_url or file_id`），两者都传返回 400（`input_image cannot have both image_url and file_id`）。
- 图片 token 用量：图片按尺寸换算成 token 并与文本 token 一起计费；进入模型前每张图片都会被自动缩放——总像素小于约 544×544 的图片会被保持长宽比放大，更大的图片会被保持长宽比缩小到总像素约相当于 1300×1300 的图片；每张图片消耗 token 数存在上限（1024 个），例如 2000×2000 与 5000×5000 缩放后消耗 token 数相同；单请求多图时每张独立按同一规则计算。
- 文档建议改用 Files API 的三种场景：单个请求会超过 48 MiB 请求体大小限制；图片超过 32 MiB（只有通过 Files API 才能使用）；在多个请求中引用同一张图片，希望避免每次重复上传。
- 音频 / 视频 / 非图片文件输入：查过的页面（`/guides/vision`、`/guides/files_api`、`/api/create-chat-completion`、`/api/create-response`、`/api/create-file`、`/api/list-files`、`/api/retrieve-file`、`/api/delete-file`、`/guides/responses_api`、`/guides/anthropic_api`、`/api/list-models`、`/quick_start/pricing`、`/quick_start/token_usage`、`/news/news260821`，以及全站侧边栏“API 文档”与“API 指南”目录）中均未出现音频、视频或非图片文件输入的端点、字段或格式白名单。

#### ② 输出模态总表

该站文档中模型只产出文本（含思维链文本与工具调用参数）；图片生成、语音合成、视频生成等“模型直接产出非文本”的能力，文档中未提供。

| 输出模态 | 该站是否提供 | 端点 | 字段与返回形式 | 限制 |
|---|---|---|---|---|
| 文本（含思维链文本、工具调用参数） | 提供 | Chat：`POST /chat/completions`；Responses：`POST /responses`；Anthropic 兼容：`POST /anthropic/v1/messages`；FIM 补全（Beta）：`POST /completions` | Chat：`choices[].message.content`（流式 `delta.content`）、思维链 `choices[].message.reasoning_content`（流式 `delta.reasoning_content`）、工具调用 `choices[].message.tool_calls[]`（`function.arguments` 为 JSON 字符串）；Responses：`output[]` 中 `message` item 的 `output_text` 块、`reasoning` item 的 `reasoning_text` 块、`function_call` item 的 `arguments`（流式事件 `response.output_text.delta` 等）；FIM：`choices[].text`；Anthropic 兼容：`content` 中的 `text` / `thinking` / `tool_use` 块 | `max_tokens` 取值范围 1 到 384K（393216），未设置时非思考模式默认 8K、思考模式默认 64K（`reasoning_effort=max` 时 128K）；Responses 用 `max_output_tokens`（含思维链 token）；输入 + 输出总长受上下文长度（1M）限制；`finish_reason=length` 或 `status=incomplete`（`incomplete_details.reason=max_output_tokens`）表示被截断；JSON 模式下 `finish_reason=length` 时消息内容可能被部分截断 |
| 图片生成 | 文档中未提供 | 文档中未提供（无图片生成端点） | 文档中未提供 | 文档中未提供 |
| 语音合成（TTS）/ 语音识别（ASR） | 文档中未提供 | 文档中未提供（无音频端点） | 文档中未提供 | 文档中未提供 |
| 视频生成 | 文档中未提供 | 文档中未提供（无视频端点） | 文档中未提供 | 文档中未提供 |
| 其它非文本输出（如向量嵌入 embeddings） | 文档中未提供 | 文档中未提供 | 文档中未提供 | 文档中未提供 |

- 模型清单中两个模型的 `output_modalities` 均为 `["text"]`（`GET /models` 响应字段），与上表一致。
- 查过的页面（均未出现图片生成 / 语音合成 / 语音识别 / 视频生成 / 向量嵌入的端点或字段）：`/api/create-chat-completion`、`/api/create-response`、`/api/create-completion`、`/api/list-models`、`/api/get-user-balance`、`/api/create-file`、`/api/list-files`、`/api/retrieve-file`、`/api/delete-file`、`/guides/vision`、`/guides/files_api`、`/guides/responses_api`、`/guides/anthropic_api`、`/guides/thinking_mode`、`/guides/tool_calls`、`/quick_start/pricing`、`/quick_start/token_usage`、`/news/news260821`，以及全站侧边栏“API 文档”“API 指南”“API 参考”目录（只列出 chat / responses / completions / models / balance / files 六类端点）。

#### ③ 文件上传与管理 API

Files API 用于上传图片并通过 `file_id` 在对话请求中引用；上传的文件与 `deepseek-flash` 模型配合使用。

| 操作 | Method 与 Path | 请求字段 | 体积与格式限制 | 返回的 `id` 怎么用 | 有效期与删除 |
|---|---|---|---|---|---|
| 上传文件 | POST `/files`（`base_url` 为 `https://api.deepseek.com`，`Content-Type: multipart/form-data`） | 表单字段 `file`（binary，必填，要上传的图片文件）、`purpose`（string，必填，必须为 `user_data`）、`expires_after[anchor]`（选填，若提供必须为 `created_at`，需与 `expires_after[seconds]` 一起使用）、`expires_after[seconds]`（选填，integer，`>= 3600` 且 `<= 2592000`） | 单个文件最大 64 MiB，上传需在 10 分钟内完成；格式 JPEG、PNG、GIF、WebP | 响应返回 `id`（形如 `file-api-...`），在对话请求中引用：Chat 用 `{"type": "file", "file_id": "file-api-..."}`；Responses 用 `input_image` 的 `file_id`；Anthropic 用 `image` 块 `source.type=file`（需 `anthropic-beta: files-api-2025-04-14`） | 不传两个 `expires_after` 字段则文件永久有效；`expires_at` 仅在上传时设置了有效期才出现 |
| 列出文件 | GET `/files`（游标分页，返回属于该用户的文件列表） | 查询参数 `after`（分页 `file_id` 游标，返回该文件之后的文件）、`limit`（1 到 1000，默认 `1000`）、`order`（`asc` 默认 / `desc`，按创建时间排序）、`purpose`（仅支持 `user_data`） | 文档未给出额外体积/格式限制 | 响应 `data[]` 为 file object 列表；`first_id` / `last_id` 可用作分页游标 | 响应的 file object 含 `expires_at`（仅设置过有效期才出现） |
| 查询文件 | GET `/files/:file_id` | 路径参数 `file_id`（必填） | 文档未给出额外限制 | 返回同一 file object 结构（`id` 即文件标识符，可在对话补全请求中引用） | 同上传响应（`expires_at` 条件出现） |
| 删除文件 | DELETE `/files/:file_id` | 路径参数 `file_id`（必填，要删除的文件 ID） | 文档未给出额外限制 | 文档未给出（删除后该 `file_id` 能否再被引用，文档未写） | 响应为 `id`（被删除文件 ID）、`object`（`file`）、`deleted`（boolean，是否成功删除） |
| Anthropic 兼容 Files API | POST / GET / DELETE `/anthropic/v1/files`、`/anthropic/v1/files/{file_id}`（`base_url` 为 `https://api.deepseek.com/anthropic`；裸 HTTP 客户端需写完整路径） | 形态同 Anthropic Files API；所有请求必须携带请求头 `anthropic-beta: files-api-2025-04-14` | 同 Files API（文档差异表未改体积/格式限制） | 返回的文件对象 `id` 同为 `file-api-...`，可用于 Anthropic `/messages` 的文件引用 | 删除返回 `{ "id": "...", "type": "file_deleted" }` |

- file object 字段（OpenAI 兼容）：`id`（必填，形如 `file-api-...`，可在对话补全请求中引用）、`object`（`file`，必填）、`bytes`（文件大小字节，必填）、`created_at`（Unix 秒，必填）、`filename`（必填）、`purpose`（`user_data`，必填）、`expires_at`（Unix 秒，仅在上传时设置了有效期才出现）。列表响应另有 `object`（`list`）、`data[]`、`first_id`、`last_id`、`has_more`（必填）。
- Files API 限制（文档原文表格）：支持的格式 JPEG、PNG、GIF、WebP；单个上传文件最大大小 64 MiB；文件名最大长度 512 个字符；单用户最大存储空间 25 GiB；单用户最大存储文件数 10000；文件有效期范围 1 小时到 30 天，或不传 `expires_after` 永久有效。
- 文件归属于你的 API key，可被任一 API 家族引用；通过 Anthropic 兼容 `/messages` 端点引用文件时需携带 `anthropic-beta: files-api-2025-04-14` 请求头。
- 与内联（base64）图片不同，通过 `file_id` 引用的文件不受 32 MiB 单图限制，请求中单张最大 64 MiB。
- `file` 块也可以通过 `file_data` 以 base64 形式内联携带图片，替代 `file_id`（二者互斥）；使用 `file_data` 时还可设置 `filename`，`filename` 不能与 `file_id` 同时出现。
- Anthropic 兼容与 OpenAI 兼容版本的差异（文档原文表）：列表分页 `after` 对 `after_id` / `before_id`（互斥）；列表 limit 1–1000、默认 1000 对 1–1000、默认 20；列表 `order` / `purpose` 支持对不支持；列表顶层 `object` 为 `"list"` 对无；文件对象大小字段 `bytes` 对 `size_bytes`；文件对象类型字段 `object` 对 `type`；`created_at` 为 Unix 时间戳（秒）对 RFC 3339 字符串；必需请求头无对 `anthropic-beta: files-api-2025-04-14`。
- Files API 本身不收费（新闻页记录）。

#### ④ 相关端点与限制汇总

相关端点：`POST /chat/completions`、`POST /responses`、`POST /anthropic/v1/messages`、`POST /completions`（Beta，FIM）、`POST /files`、`GET /files`、`GET /files/:file_id`、`DELETE /files/:file_id`、Anthropic 兼容 `POST` / `GET` / `DELETE /anthropic/v1/files`（及 `/anthropic/v1/files/{file_id}`）、`GET /models`（含 `input_modalities` / `output_modalities`）。`base_url` 为 `https://api.deepseek.com` 或 `https://api.deepseek.com/anthropic`（须按第 1 节的鉴权方式带 `Authorization: Bearer <API key>` 或 `x-api-key`）。

| 限制项 | 数值/说明 | 超限时的错误表现 |
|---|---|---|
| 请求体大小 | 48 MiB（内联图片的 base64 / `file_data` 编码后数据计入该限制） | 文档未给出（文档只写“编码后的数据会计入 48 MiB 请求体大小限制”，未写超限后的错误码或错误信息） |
| 外部图片 URL 长度 | 8192 个字符 | 文档未给出（文档只说链接超长时请改用 base64 data URL 或 Files API） |
| 外部图片下载时限 | 需在 60 秒内完成下载 | 文档未给出 |
| 单张图片最大大小（base64 / 外部 URL） | 32 MiB | 文档未给出（文档建议改用 Files API） |
| 单张图片最大大小（Files API `file_id`） | 64 MiB | 文档未给出 |
| 单个请求最大图片数 | 600 | 文档未给出 |
| 单个请求图片总大小 | 不含 `file_id` 图片最多 64 MiB；包含 `file_id` 图片最高 200 MiB | 文档未给出 |
| 图片最大尺寸 | 单边最长 8192 像素；单个请求包含 15 张及以上图片时，降为单边最长 4096 像素 | 文档未给出 |
| 支持的图片格式 | JPEG、PNG、GIF、WebP（格式由文件实际内容判断，而非文件名或声明的 MIME 类型） | 文档未给出 |
| Files API 单个上传文件 | 最大 64 MiB，上传需在 10 分钟内完成 | 文档未给出 |
| Files API 文件名最大长度 | 512 个字符 | 文档未给出 |
| Files API 单用户配额 | 最大存储空间 25 GiB；最大存储文件数 10000 | 文档未给出 |
| 图片出现位置 | Chat：仅 `user` 消息；Responses：仅 `user` / `developer` 消息 item 与 `function_call_output` / `custom_tool_call_output` 的 `output` | 400（文档：`system` 或 `assistant` 消息携带图片会返回 400 错误） |
| Responses `input_image` 的 `image_url` 与 `file_id` | 二者互斥、必须传其一 | 两者都不传返回 400（`input_image must have image_url or file_id`）；两者都传返回 400（`input_image cannot have both image_url and file_id`） |
| Responses 文件输入 | 不支持 | 文档写“文件输入不支持”，未给出错误码 |
| 错误码总表（图片 / 文件无专用错误码） | 400 请求体格式错误；401 认证失败；402 余额不足；422 请求体参数错误；429 请求速率（TPM 或 RPM）达到上限；500 服务器故障；503 服务器繁忙 | 图片与文件相关限制值本身，文档均未写出对应的错误码或错误信息 |
| 图片 token 上限 | 每张图片 1024 个 token（进入模型前自动缩放：总像素小于约 544×544 放大、更大缩小到约 1300×1300 的总像素） | 不适用（按尺寸换算后与文本 token 一起计费，可用“Token 与用量计算”页的图片 Token 计算器估算） |

### 9. 缓存与成本字段

上下文硬盘缓存：DeepSeek API 上下文硬盘缓存技术对所有用户默认开启，用户无需修改代码；每个请求都会触发硬盘缓存构建，若后续请求与之前的请求在前缀上存在重复，则重复部分只需从缓存中拉取，计入“缓存命中”。

缓存落盘与命中规则（文档原文）：
- 缓存命中的前提是相应前缀已被“落盘”。受 Sliding Window Attention 机制影响，每条缓存前缀是一个独立的完整单元，后续请求只有在**完整匹配**缓存前缀单元时才能命中缓存。
- 落盘时机一：**请求结束位置落盘**——每次请求的用户输入结束位置与模型输出结束位置会产生两个缓存前缀单元，后续请求完整匹配它们才可命中。
- 落盘时机二：**公共前缀检测落盘**——系统检测到多次请求之间存在公共前缀时，会将该公共前缀作为一个独立的缓存前缀单元落盘。
- 落盘时机三：**按固定 token 间隔落盘**——长输入或长输出中，系统会以一定 token 数量为间隔截取缓存前缀单元，避免长前缀迟迟未达到结束位置而完全无法被缓存。
- 只有两个请求的前缀内容相同（从第 0 个 token 开始相同）才算重复，中间开始的重复不能被缓存命中。
- 缓存系统以 64 tokens 为一个存储单元，不足 64 tokens 的内容不会被缓存。
- 缓存系统是“尽力而为”，不保证 100% 缓存命中；缓存构建耗时为秒级，缓存不再使用后会自动被清空（时间一般为几个小时到几天）；每个用户的缓存相互独立、逻辑上不可见。
- 硬盘缓存只匹配用户输入的前缀部分，输出仍通过计算推理得到，仍受 temperature 等参数影响。

usage 中的缓存计数与成本相关字段：

| 字段 | 所在响应 | 说明 |
|---|---|---|
| `usage.prompt_cache_hit_tokens` | Chat / FIM | 本次请求输入中缓存命中的 tokens 数 |
| `usage.prompt_cache_miss_tokens` | Chat / FIM | 本次请求输入中缓存未命中的 tokens 数 |
| `usage.prompt_tokens_details.cached_tokens` | Chat / FIM | 命中上下文缓存的 token 数，与 `prompt_cache_hit_tokens` 相同 |
| `usage.input_tokens_details.cached_tokens` | Responses | 命中上下文缓存的输入 token 数 |

请求侧与缓存相关的字段/说明：

| 字段 | 说明 |
|---|---|
| `user_id`（Chat）/ `user`（Responses）/ `metadata.user_id`（Anthropic） | 可用于 KVCache 缓存隔离，以进行隐私管理；也可用于内容安全处理与调度隔离 |
| `prompt_cache_key` / `prompt_cache_retention`（Responses） | 不支持。上下文缓存自动管理 |
| `tools[].cache_control`、message `cache_control`（Anthropic） | 忽略 |

价格与计费（模型 & 价格页原文，单位为“百万 tokens”）:

| 计费项 | deepseek-flash | deepseek-v4-pro |
|---|---|---|
| 百万 tokens 输入（缓存命中）空闲时段 | 0.02 元 | 0.15 元 |
| 百万 tokens 输入（缓存命中）高峰时段 | 0.04 元 | 0.30 元 |
| 百万 tokens 输入（缓存未命中）空闲时段 | 1 元 | 4.5 元 |
| 百万 tokens 输入（缓存未命中）高峰时段 | 2 元 | 9.0 元 |
| 百万 tokens 输出 空闲时段 | 4 元 | 13.5 元 |
| 百万 tokens 输出 高峰时段 | 8 元 | 27.0 元 |

- 空闲时段价格为高峰时段价格的一半。北京时间周一至周五（不含中国法定节假日）9:00 - 12:00、14:00 - 18:00 为高峰时段；其余时段，包括周末及中国法定节假日全天均为空闲时段。
- 扣费规则：扣减费用 = token 消耗量 × 模型单价，直接从充值余额或赠送余额扣减；两者同时存在时优先扣减赠送余额。
- Token 与字数换算参考：1 个英文字符 ≈ 0.3 个 token，1 个中文字符 ≈ 0.6 个 token；实际以返回结果的 `usage` 为准。

### 10. 特殊模式

**JSON Output（JSON 模式）**

| 字段 | 类型 | 必填/默认 | 取值 | 说明 |
|---|---|---|---|---|
| `response_format`（Chat） | object | 选填，默认 `{"type": "text"}` | `{"type": "json_object"}` | 启用 JSON 模式，保证模型生成的消息是有效的 JSON |
| `text.format`（Responses） | object | 选填，默认 `{"type": "text"}` | `{"type": "json_object"}` / `{"type": "json_schema", "name": ..., "schema": ...}` | `json_schema` 为结构化输出，输出符合给定的 JSON Schema；`type` 为 `json_schema` 时 `name`、`schema` 必填 |

注意事项（文档原文）：用户传入的 system 或 user prompt 中必须含有 `json` 字样，并给出希望模型输出的 JSON 格式的样例；需要合理设置 `max_tokens` 以防 JSON 字符串被中途截断；使用 JSON Output 功能时 API 有概率返回空的 content；如果 `finish_reason="length"`，表示生成超过了 `max_tokens` 或对话超过了最大上下文长度，消息内容可能被部分截断。

**对话前缀续写（Beta）**

| 字段 | 类型 | 必填/默认 | 取值 | 说明 |
|---|---|---|---|---|
| `messages[].prefix` | bool | 最后一条 assistant 消息设置 | `true` | 强制模型以该 assistant 消息中的前缀内容开始回答 |
| `messages[].reasoning_content` | string \| null | 选填 | — | 思考模式下作为最后一条 assistant 思维链内容的输入，使用时 `prefix` 必须为 `true` |

要求：`messages` 列表最后一条消息的 `role` 必须为 `assistant` 且 `prefix` 为 `True`；需设置 `base_url="https://api.deepseek.com/beta"`。该功能也可用于输出被 `max_tokens` 截断后拼接续写。文档示例：`messages=[{"role":"user","content":"Please write quick sort code"},{"role":"assistant","content":"```python\n","prefix":True}]`，并设置 `stop=["```"]`。

**FIM 补全（Beta）**

| 字段 | 类型 | 必填/默认 | 取值 | 说明 |
|---|---|---|---|---|
| `model` | string | 必填 | `deepseek-flash`、`deepseek-v4-pro` | 模型的 ID |
| `prompt` | string | 必填 | — | 用于生成完成内容的提示 |
| `suffix` | string \| null | 选填 | — | 制定被补全内容的后缀 |
| `echo` | boolean \| null | 选填 | `true`/`false` | 在输出中把 prompt 的内容也输出出来；不能与 `suffix` 或 `logprobs` 一起使用 |
| `logprobs` | integer \| null | 选填 | `<= 20` | 输出中包含 logprobs 最可能输出 token 的对数概率，包含采样的 token；响应中最多可能有 `logprobs+1` 个元素 |
| `max_tokens` | integer \| null | 选填 | — | 最大生成 token 数量 |
| `stop` | string \| string[] \| null | 选填 | 一个 string 或最多 16 个 string 的 list | 遇到这些词时停止生成 |
| `stream` | boolean \| null | 选填 | `true`/`false` | SSE 流式，以 `data: [DONE]` 结尾 |
| `stream_options` | object \| null | 选填 | 必须与 `stream: true` 同用，否则 400 | `include_usage` 语义与 Chat 相同 |
| `temperature` | number \| null | 选填，默认 `1` | `<= 2` | — |
| `top_p` | number \| null | 选填，默认 `1` | `<= 1` | — |
| `frequency_penalty` / `presence_penalty` | — | deprecated | — | 已不再支持，传入无效果 |

FIM 说明：模型的最大补全长度为 4K；需设置 `base_url="https://api.deepseek.com/beta"`；FIM 补全接口收费与对话补全相同；模型清单页显示 FIM 补全仅非思考模式支持。

**logprobs**

| 字段（Chat） | 取值 | 说明 |
|---|---|---|
| `logprobs` | `true`/`false` | `true` 时在 `message` 的 `content` 中返回每个输出 token 的对数概率 |
| `top_logprobs` | 0–20 | 指定每个输出位置返回输出概率 top N 的 token 及其对数概率；指定时 `logprobs` 必须为 `true` |

logprob 子字段：`token`、`logprob`（`-9999.0` 代表该 token 输出概率极小、不在 top 20 最可能输出的 token 中）、`bytes`（该 token UTF-8 字节表示的整数列表，无对应字节表示为 `null`）、`top_logprobs`（该输出位置上输出概率 top N 的 token 列表）。Responses 侧仅有 `top_logprobs`（范围 [0, 20]）。

**strict 模式（Beta）**：见第 7 节（需 `/beta` base_url、所有 function 设 `strict: true`）。

**临时/特殊服务端点（历史）**

| base_url | 说明 |
|---|---|
| `https://api.deepseek.com/v3.1_terminus_expires_on_20251015` | 访问 DeepSeek-V3.1-Terminus 的临时接口，调用价格与 V3.2-Exp 相同，保留到北京时间 2025-10-15 23:59 |
| `https://api.deepseek.com/v3.2_speciale_expires_on_20251215` | 访问 DeepSeek-V3.2-Speciale，API 价格不变，只支持思考模式下的对话功能，不支持工具调用等功能，最大输出长度默认为 128K，支持至北京时间 2025-12-15 23:59 |

### 11. 错误码与限流

标准 HTTP 状态码含义同《通用基线》§0.8（本家表列 400/401/402/422/429/500/503）。错误码表给出的名称与处理方法如下（文档原文）：

| 码 | 建议动作（解决方法） |
|---|---|
| 400 - 格式错误 | 请根据错误信息提示修改请求体 |
| 401 - 认证失败 | 检查 API key 是否正确；如无 API key 请先创建 API key |
| 402 - 余额不足 | 确认账户余额，并前往充值页面充值 |
| 422 - 参数错误 | 请根据错误信息提示修改相关参数 |
| 429 - 请求速率达到上限 | 请合理规划请求速率 |
| 500 - 服务器故障 | 等待后重试；若问题一直存在，请联系我们解决 |
| 503 - 服务器繁忙 | 请稍后重试 |

文档其他页面明确会返回 400 的场景（可作为错误定位清单）：

| 场景 | 来源页 |
|---|---|
| `stream_options` 未与 `stream: true` 一起使用 | create-chat-completion |
| 思考模式下使用 `tool_choice: required` 或指定具体 tool | create-chat-completion / tool_calls |
| 携带 `tools` 的后续请求未完整回传 `reasoning_content` | thinking_mode |
| `system` 或 `assistant` 消息携带图片 | vision / responses_api |
| Responses 的 `input_image` 既无 `image_url` 也无 `file_id`（`input_image must have image_url or file_id`） | create-response / responses_api |
| Responses 的 `input_image` 同时带 `image_url` 与 `file_id`（`input_image cannot have both image_url and file_id`） | create-response / responses_api |
| Responses 的 `tools` 使用 `custom` 类型但名称不是 `apply_patch` | responses_api |
| Responses 输入超出上下文窗口（`truncation` 不支持） | responses_api |

并发限速（文档原文）：

|  | deepseek-flash | deepseek-v4-pro |
|---|---|---|
| 并发限制 | 2500 | 500 |

- 一个请求从发出后、到模型响应完成之前记为一个并发。
- 并发限制以账号粒度计，与 API Key 无关。
- 对于一个账号，在并发限度内的 API 请求都会得到响应；超过并发限度时会收到 HTTP 429 错误码。
- 若有更高并发需求，可提交账号扩容申请工单；扩容并不增加额外费用。

`user_id` 隔离（文档原文）：

| 项 | 说明 |
|---|---|
| 内容安全隔离 | `user_id` 用于区分业务侧用户身份，以进行内容安全状况处理 |
| KVCache 隔离 | `user_id` 用于对业务侧用户进行 KVCache 隔离，以进行隐私管理 |
| 调度隔离 | `user_id` 用于对业务侧用户进行调度隔离 |
| 参数格式 | 需满足正则表达式 `[a-zA-Z0-9\-_]+` 的字符串，最大长度 512；请勿包含用户隐私信息 |
| 普通 API 用户 | 所有 `user_id` 合并计算并发限速 |
| 提升并发配额的用户 | 限制账号总并发，同时对每个 `user_id` 做并发限制（空 id 为一个特殊的 `user_id`）；每个 `user_id` 下 `deepseek-flash` 并发限制 2500、`deepseek-v4-pro` 并发限制 500，超限时设置了该 `user_id` 的请求收到 HTTP 429 |

`user_id` 设置方式：OpenAI Chat Completions 接口写在 HTTP 请求体的 `user_id` 字段（OpenAI SDK 需放入 `extra_body`）；Anthropic 接口写在 `metadata: {"user_id": "..."}`（并需带上 `max_tokens`）。

### 12. 模型清单与限制

当前可用模型（模型 & 价格页与 `GET /models` 示例）：

| 项 | deepseek-flash | deepseek-v4-pro |
|---|---|---|
| 模型版本 | DeepSeek-V4.1-Flash | DeepSeek-V4-Pro-0813 |
| 展示名称（`name`） | DeepSeek-V4.1-Flash | DeepSeek-V4-Pro |
| `owned_by` | deepseek | deepseek |
| 上下文长度 | 1M（`context_window: 1048576`） | 1M（`context_window: 1048576`） |
| 输出长度 | 最大 384K（`max_output_tokens: 393216`） | 最大 384K（`max_output_tokens: 393216`） |
| 思考模式 | 支持非思考与思考模式（默认思考） | 支持非思考与思考模式（默认思考） |
| `effort.supported_levels` | `["low", "high", "max"]` | `["low", "high", "max"]` |
| `effort.default_level` | `high` | `high` |
| `input_modalities` | `["text", "image"]` | `["text"]` |
| `output_modalities` | `["text"]` | `["text"]` |
| `api_capabilities.anthropic_messages.system_prompt_update` | `in-history` | `leading-only` |
| 并发限制 | 2500 | 500 |

功能支持对比（模型 & 价格页）：

| 功能 | deepseek-flash | deepseek-v4-pro |
|---|---|---|
| Json Output | 支持 | 支持 |
| Tool Calls | 支持 | 支持 |
| Responses API | 支持 | 支持 |
| Anthropic API | 支持 | 支持 |
| 对话前缀续写（Beta） | 支持 | 支持 |
| FIM 补全（Beta） | 仅非思考模式支持 | 仅非思考模式支持 |
| 图像理解 | 支持 | 不支持 |

参数支持差异（文档中明写的差异点）：

| 参数/能力 | deepseek-flash | deepseek-v4-pro |
|---|---|---|
| `max_tokens` 上限 | 1 到 384K（393216） | 1 到 384K（393216） |
| 图片输入（`image_url` / `input_image` / `file_id`） | 支持（`input_modalities` 含 `image`） | 不支持（`input_modalities` 仅 `text`） |
| Anthropic system 提示词更新 | 可在历史中追加 `system` 消息（`in-history`） | 只有对话开头的 system 提示词生效（`leading-only`） |

`GET /models` 响应字段：`object`（`list`）、`data[]`（元素含 `id`、`object`（`model`）、`owned_by`、`name`、`context_window`、`max_output_tokens`、`input_modalities`、`output_modalities`、`effort.supported_levels`（必填，按建议展示顺序排列，即 `reasoning_effort` 可接受取值，不含关闭思考用的 `none`）、`effort.default_level`（开启思考且未指定强度时的默认档位，一定属于 `supported_levels`，仅当服务端为该模型定义了默认档位时返回）、`api_capabilities.anthropic_messages.system_prompt_update`（`leading-only` 或 `in-history`））。

旧模型名与路由（模型 & 价格页、更新日志、新闻页）：

| 模型名 | 状态 | 说明 |
|---|---|---|
| `deepseek-v4-flash` | 已下线，暂时路由 | 旧模型名仍可调用，但对应模型已下线，请求由 DeepSeek-V4.1-Flash 提供服务，并按 Flash 价格计费 |
| `deepseek-v4-flash-vision-exp` | 已下线，暂时路由 | 同上 |
| `deepseek-v4-pro` | 计划有序下线 | 2026-09-14 12:00 之后的请求将全部路由到 V4.1 Flash 并按 V4.1 Flash 单价计费（新闻页 2026-09-10）；同时更新日志 2026-09-10 写“决定在 2026 年 9 月 14 日之后继续提供 DeepSeek V4 Pro 的 API 调用服务，计费方式保持不变；如有变动将另行通知” |
| `deepseek-chat` | 旧接口模型名 | 指向 `deepseek-v4-flash` 的非思考模式；将于 2026-07-24 停止使用（2026-04-24 更新日志） |
| `deepseek-reasoner` | 旧接口模型名 | 指向 `deepseek-v4-flash` 的思考模式；同样自 2026-07-24 起停止使用 |
| `deepseek-coder` | 历史模型名 | 2024 年与 `deepseek-chat` 并存，均可访问当时的合并模型 |

Anthropic 端点模型名映射：`claude-opus*` → `deepseek-v4-pro`（按 V4 Pro 价格计费）；`claude-haiku*`、`claude-sonnet*` → `deepseek-flash`；其他不支持的模型名自动映射到 `deepseek-flash`。

限制汇总（文档各处明写，按字段归类）：

| 限制项 | 数值 |
|---|---|
| `max_tokens` 取值范围 | 1 到 384K（393216） |
| `max_tokens` 未设置时的默认值 | 非思考模式 8K；思考模式 64K；`reasoning_effort=max` 时 128K |
| `stop` 序列数量 | 一个 string 或最多 16 个 string |
| `top_logprobs` | 0 到 20（Responses 同，范围 [0, 20]） |
| `user_id` / `user` 字符集与长度 | `[a-zA-Z0-9\-_]`，最大长度 512 |
| 函数名长度与字符 | 最大 128 个字符；a-z、A-Z、0-9、下划线、连字符（Responses：`^[a-zA-Z0-9_-]+$`） |
| 单次请求最大图片数 | 600 |
| 请求体大小 | 48 MiB |
| 单个上传文件最大大小 | 64 MiB（Files API）；上传需在 10 分钟内完成 |
| FIM 最大补全长度 | 4K |
| 图片 token 上限 | 1024 个 tokens/张（图像理解页） |

Token 用量：以返回结果的 `usage` 为准；离线可用文档提供的 `deepseek_tokenizer.zip` 运行 tokenizer 计算；换算参考为 1 个英文字符 ≈ 0.3 token、1 个中文字符 ≈ 0.6 token。

### 13. 来源页清单

抓取方式：`web_fetch` 逐页抓取（首页与 pricing 两页额外用 raw 模式交叉校对）。

本家页面清单（含每条 URL 的「这页讲了什么」一句话）统一见《附录：各家页面清单》的本家小节（共 23 条）。

未抓取/未纳入的页面：
- 新闻页中引用的 `https://api-docs.deepseek.com/zh-cn/guides/comparison_testing` 未出现在中文侧边栏，未抓取（该页也无法从侧边栏确认是否存在）。

---

## 智谱 GLM

> 本文所有内容均逐字取自智谱 AI 开放平台官方文档（https://docs.bigmodel.cn）。未在文档中出现的内容一律标注为「文档未给出」，不做任何推测。

---

### 1. 端点与鉴权

智谱开放平台提供三套并行的模型协议，另有若干独立工具端点。

| 协议 | base_url | method + path | 必需请求头 |
| :- | :- | :- | :- |
| OpenAI Chat Completion 协议 | `https://open.bigmodel.cn/api/paas/v4` | `POST /chat/completions` | `Content-Type: application/json`、`Authorization: Bearer YOUR_API_KEY` |
| OpenAI Response 协议 | `https://open.bigmodel.cn/api/v1` | `POST /responses`（另有 `GET /responses/{response_id}`、`GET /responses/{response_id}/input_items`、`DELETE /responses/{response_id}`） | `Authorization: Bearer YOUR_API_KEY`、`Content-Type: application/json` |
| Anthropic Message 协议 | `https://open.bigmodel.cn/api/anthropic` | `POST /v1/messages` | `x-api-key: YOUR_API_KEY`、`content-type: application/json` |

其他相关端点（同属开放平台）：

| 用途 | method + path |
| :- | :- |
| 对话补全（异步） | `POST /api/paas/v4/...`（文档见「对话补全(异步)」） |
| 图像生成 | `POST https://open.bigmodel.cn/api/paas/v4/images/generations` |
| 图像生成（异步，仅 GLM-Image） | `POST https://open.bigmodel.cn/api/paas/v4/async/images/generations` |
| 视频生成（异步） | `POST https://open.bigmodel.cn/api/paas/v4/videos/generations` |
| 查询异步结果（对话补全 / 图像生成 / 视频生成） | `GET https://open.bigmodel.cn/api/paas/v4/async-result/{id}` |
| 文档解析（GLM-OCR） | `POST https://open.bigmodel.cn/api/paas/v4/layout_parsing` |
| 语音转文本（GLM-ASR） | `POST https://open.bigmodel.cn/api/paas/v4/audio/transcriptions` |
| 文本转语音（GLM-TTS） | `POST https://open.bigmodel.cn/api/paas/v4/audio/speech` |
| 音色复刻 / 音色列表 / 删除音色 | `POST .../voice/clone`、`GET .../voice/list`、`POST .../voice/delete` |
| 文件上传 / 文件列表 / 删除文件 / 文件内容 | `POST .../files`、`GET .../files`、`DELETE .../files/{file_id}`、`GET .../files/{file_id}/content` |
| 文件解析（异步）/ 解析结果 / 文件解析（同步） | `POST .../files/parser/create`、`GET .../files/parser/result/{taskId}/{format_type}`、`POST .../files/parser/sync` |
| OCR 服务 | `POST https://open.bigmodel.cn/api/paas/v4/files/ocr` |
| 实时音视频（GLM-Realtime） | `WSS wss://open.bigmodel.cn/api/paas/v4/realtime` |
| 网络搜索 | `POST https://open.bigmodel.cn/api/paas/v4/web_search` |
| 创建 Batch 任务 | `POST https://open.bigmodel.cn/api/paas/v4/batches` |
| 检索 / 取消 / 列出 Batch | `GET https://open.bigmodel.cn/api/paas/v4/batches/{batch_id}`、`POST .../batches/{batch_id}/cancel`、`GET .../batches` |
| 下载 Batch 结果文件 | `GET https://open.bigmodel.cn/api/paas/v4/files/{file_id}/content` |

鉴权方式（HTTP API 文档给出两种）：

- **API Key 鉴权**：`Authorization: Bearer YOUR_API_KEY`。
- **JWT Token 鉴权**：把 API Key 按 `.` 拆成 `id` 与 `secret`，用 HS256 签名生成 JWT，payload 为 `api_key`(=id)、`exp`(毫秒时间戳 + 有效期)、`timestamp`(毫秒时间戳)，headers 为 `{"alg": "HS256", "sign_type": "SIGN"}`。

注意事项（文档原文）：

- Response API 的基址是 `https://open.bigmodel.cn/api/v1`，**不是**对话补全使用的 `https://open.bigmodel.cn/api/paas/v4`；请求超时为 7200 秒。
- Response API 默认 `store=false`、流式结束不发送 `data: [DONE]`、当前未提供 cancel。

---

### 2. Chat Completions 请求字段总表

`POST /paas/v4/chat/completions` 的请求体是 `oneOf` 三种模型形态：`ChatCompletionTextRequest`（文本模型）、`ChatCompletionVisionRequest`（视觉模型）、`ChatCompletionAudioRequest`（音频模型）。三者都 `required: [model, messages]`。

下表只列本家与《通用基线》§0.2 不同的字段与取值；未列出的标准字段语义同基线（本家文档未写明其默认值/范围即视为未列出）。

#### 2.1 文本模型（ChatCompletionTextRequest）

| 字段 | 类型 | 必填/默认 | 取值 | 说明 |
| :- | :- | :- | :- | :- |
| `model` | string | 必填；默认 `glm-5.3` | `glm-5.3`、`glm-5.2`、`glm-5.1`、`glm-5-turbo`、`glm-5`、`glm-4.7`、`glm-4.7-flash`、`glm-4.7-flashx`、`glm-4.6`、`glm-4.5-air`、`glm-4.5-airx`、`glm-4.5-flash`、`glm-4-flash-250414`、`glm-4-flashx-250414` | 调用的模型代码。GLM-5.3 是最新旗舰模型系列 |
| `messages` | array | 必填；`minItems: 1` | 角色 `system` / `user` / `assistant` / `tool` | 按时间顺序排列的完整上下文；不能只包含系统消息或助手消息 |
| `stream` | boolean | 默认 `false` | `true` / `false` | 流式输出结束时会返回 `data: [DONE]` 消息 |
| `thinking` | object | 默认 `{"type": "enabled"}` | `{"type": "enabled"\|"disabled", "clear_thinking": true\|false}` | 控制是否开启思维链，仅 GLM-4.5 及以上支持 |
| `reasoning_effort` | string | 默认 `max` | `max`、`xhigh`、`high`、`medium`、`low`、`minimal`、`none` | `thinking` 开启时生效，默认 `max`，仅 GLM-5.2 及其以上支持。GLM-5.3 / GLM-5.3-FLASH 仅支持 low / high / max；GLM-5.2 传 `none`/`minimal` 放弃思考，`low`/`medium` 映射为 `high`，`xhigh` 映射为 `max` |
| `do_sample` | boolean | 默认 `true` | `true` / `false` | `false` 时总是选择概率最高的词汇（贪心），此时 `temperature` 和 `top_p` 被忽略 |
| `temperature` | number(float) | 默认 `1`；范围 `[0.0, 1.0]`，限两位小数 | — | GLM-5.3 / 5.2 / 5.1 / 5 / 4.7 / 4.6 系列默认 `1.0`；GLM-4.5 系列默认 `0.6`；GLM-4 系列默认 `0.75` |
| `top_p` | number(float) | 默认 `0.95`；范围 `[0.01, 1.0]`，限两位小数 | — | GLM-5.3 / 5.2 / 5.1 / 5 / 4.7 / 4.6 / 4.5 系列默认 `0.95`；GLM-4 系列默认 `0.9`。建议不要与 `temperature` 同时调整 |
| `max_tokens` | integer | 最小值 `1`，最大值 `131072` | — | GLM-5.3 / 5.2 / 5.1 / 5 / 4.7 / 4.6 系列最大支持 128K 输出，GLM-4.5 系列最大 96K，建议不小于 1024 |
| `tool_stream` | boolean | 默认 `false` | `true` / `false` | 是否开启流式响应 Function Calls；仅 GLM-5.3 / 5.2 / 5.1 / 5 / 5-Turbo / 4.7 / 4.6 系列支持 |
| `tools` | array | 可选 | `FunctionToolSchema` / `RetrievalToolSchema` / `WebSearchToolSchema` / `MCPToolSchema` | 模型可调用的工具列表；最多支持 128 个函数 |
| `tool_choice` | string | 默认 `auto`，仅支持 `auto` | `auto` | 仅当工具类型为 `function` 时补充 |
| `stop` | array[string] | 可选；`maxItems: 4` | — | 命中即停止生成，格式如 `["stop_word1"]` |
| `response_format` | object（必含 `type`） | 默认 `text` | `type`: `text` / `json_object` | 仅文本模型支持此字段 |
| `request_id` | string | 可选；长度 6–64 | — | 建议使用 UUID；未提供平台自动生成 |
| `user_id` | string | 可选；长度 6–128 | — | 终端用户唯一标识符，建议不含敏感信息 |

`messages` 各角色字段（文本模型）：

| 角色 | 字段 | 类型 | 必填 | 说明 |
| :- | :- | :- | :- | :- |
| `user` | `role` / `content` | string / string | 必填 | 用户输入文本 |
| `system` | `role` / `content` | string / string | 必填 | 设定 AI 行为与角色 |
| `assistant` | `role` / `content` / `tool_calls` | string / string / array | 必填 `role` | 模型回复；提供 `tool_calls` 时 `content` 通常为空 |
| `assistant.tool_calls[]` | `id` / `type` / `function` | string / string(`function`、`web_search`、`retrieval`) / object(`name`, `arguments`) | `id`、`type` 必填 | 模型生成的工具调用 |
| `tool` | `role` / `content` / `tool_call_id` | string / string / string | 必填 `role`、`content` | 工具调用结果，须回传对应工具调用 ID |

#### 2.2 视觉模型（ChatCompletionVisionRequest）字段差异

| 字段 | 类型 | 必填/默认 | 取值 | 说明 |
| :- | :- | :- | :- | :- |
| `model` | string | 必填；默认 `glm-5.3-flashx` | `glm-5.3-flashx`、`glm-5.3-flash`、`glm-5v-turbo`、`glm-4.6v`、`autoglm-phone`、`glm-4.6v-flash`、`glm-4.6v-flashx`、`glm-4v-flash`、`glm-4.1v-thinking-flashx`、`glm-4.1v-thinking-flash` | 视觉模型代码 |
| `messages` | array | 必填；`minItems: 1` | 角色 `system` / `user` / `assistant` | 支持纯文本或多模态 content（文本、图片、视频、文件） |
| `stream` | boolean | 默认 `false` | — | 同上；结束时返回 `data: [DONE]` |
| `thinking` | object | 同文本模型 | — | 同上 |
| `reasoning_effort` | string | 默认 `max` | 仅 `max`、`high`、`low` | GLM-5.3-FLASH 系列模型支持 low / high / max 档位 |
| `do_sample` | boolean | 默认 `true` | — | 同上 |
| `temperature` | number | 默认 `1` | `[0.0, 1.0]` | GLM-5.3-Flash 系列默认 `1.0`；GLM-5V-Turbo、GLM-4.6V、GLM-4.5V 系列默认 `0.8`；AutoGLM-Phone 默认 `0.0`；GLM-4.1v 系列默认 `0.8` |
| `top_p` | number | 默认 `0.95` | `[0.01, 1.0]` | GLM-5.3-Flash 系列默认 `0.95`；GLM-5V-Turbo、GLM-4.6V、GLM-4.5V 系列默认 `0.6`；AutoGLM-Phone 默认 `0.85`；GLM-4.1v 系列默认 `0.6` |
| `max_tokens` | integer | 最小 `1`，最大 `131072` | — | GLM-5.3-Flash 系列、GLM-5V-Turbo 最大 128K；GLM-4.6V 最大 32K；GLM-4.5V 最大 16K；AutoGLM-Phone 最大 4K；GLM-4.1v 系列最大 16K |
| `tools` | array | 可选 | 仅 `FunctionToolSchema` | 仅 GLM-5.3-Flash 系列、GLM-4.6V 和 AutoGLM-Phone 支持 |
| `tool_choice` | string | 默认 `auto`，仅支持 `auto` | `auto` | 仅 GLM-4.6V 支持此参数 |
| `stop` | array[string] | 可选；`maxItems: 4` | — | 同上 |
| `request_id` / `user_id` | string | 长度 6–64 / 6–128 | — | 视觉模型请求**没有** `response_format`、`tool_stream` |

#### 2.3 音频模型（ChatCompletionAudioRequest）字段差异

| 字段 | 类型 | 必填/默认 | 取值 | 说明 |
| :- | :- | :- | :- | :- |
| `model` | string | 必填；默认 `glm-4-voice` | `glm-4-voice` | GLM-4-Voice 支持语音理解和生成 |
| `messages` | array | 必填；`minItems: 1` | 角色 `system` / `user` / `assistant` | 支持文本和音频内容；`assistant` 可带 `audio.id` |
| `stream` | boolean | 默认 `false` | — | 结束时返回 `data: [DONE]` |
| `do_sample` | boolean | 默认 `true` | — | 语音识别、转录建议设为 `false` |
| `temperature` | number | 默认 `0.8` | `[0.0, 1.0]` | GLM-4-Voice 默认值 `0.8` |
| `top_p` | number | 默认 `0.6` | `[0.01, 1.0]` | GLM-4-Voice 默认值 `0.6` |
| `max_tokens` | integer | 默认 `1024`；最小 `1`，最大 `4096` | — | GLM-4-Voice 最大支持 4K |
| `watermark_enabled` | boolean | 可选 | `true`（默认启用显式水印及隐式数字水印）/ `false`（关闭所有水印，仅允许已签署免责声明的客户） | 控制 AI 生成图片时是否添加水印 |
| `stop` | array[string] | 可选；`maxItems: 4` | — | 同上 |
| `request_id` / `user_id` | string | 长度 6–64 / 6–128 | — | 音频模型**没有** `thinking`、`reasoning_effort`、`tools`、`tool_choice`、`response_format` |

#### 2.4 各模型默认 / 最大 max_tokens（核心参数页数据）

| 模型编码 | 默认 max_tokens | 最大 max_tokens |
| :- | :-: | :-: |
| glm-5.3 | 65536 | 131072 |
| glm-5.3-flash | 65536 | 131072 |
| glm-5.3-flashx | 65536 | 131072 |
| glm-5.2 | 65536 | 131072 |
| glm-5.1 | 65536 | 131072 |
| glm-5v-turbo | 65536 | 131072 |
| glm-5 | 65536 | 131072 |
| glm-5-turbo | 65536 | 131072 |
| glm-4.7 | 65536 | 131072 |
| glm-4.6 | 65536 | 131072 |
| glm-4.6v | 16384 | 32768 |
| glm-4.6v-flash | 16384 | 32768 |
| glm-4.6v-flashx | 16384 | 32768 |
| glm-4.5 | 65536 | 98304 |
| glm-4.5-air | 65536 | 98304 |
| glm-4.5-x | 65536 | 98304 |
| glm-4.5-flash | 65536 | 98304 |
| glm-4.5v | 16384 | 16384 |
| glm-4.1v-thinking-flashx | 16384 | 16384 |
| glm-4.1v-thinking-flash | 32768 | 32768 |
| glm-4-air-250414 | 16384 | 16384 |
| glm-4-flash-250414 | 32768 | 32768 |
| glm-4-plus | 动态计算 | 4095 |
| glm-4-air | 动态计算 | 4095 |
| glm-4-airx | 动态计算 | 4095 |
| glm-4-flash | 动态计算 | 4095 |
| glm-4-flashx | 动态计算 | 4095 |
| glm-4v-plus-0111 | 1024 | 8192 |
| glm-4v-flash | 1024 | 1024 |

---

### 3. Responses API 字段差异

端点 `POST https://open.bigmodel.cn/api/v1/responses`，请求体为 `CreateResponseRequest`（`required: [model, input]`）。

下表只列本家与《通用基线》§0.6 不同的字段与取值；未列出的标准字段语义同基线。

| 字段 | 类型 | 必填/默认 | 取值 | 说明 |
| :- | :- | :- | :- | :- |
| `model` | string | 必填 | 例如 `glm-5.3` | 模型编码 |
| `input` | string / array | 必填 | 字符串，或 `InputItem` 数组 | 用户文本，或输入项列表（`message` / `function_call` / `function_call_output` / `reasoning`） |
| `instructions` | string | 可选 | — | 系统指令 |
| `stream` | boolean | 默认 `false` | — | 是否 SSE 流式返回；结束时不发送 `data: [DONE]` |
| `temperature` | number(float) | 范围 `[0.0, 1.0]`，限两位小数；GLM-5.3 默认 `1.0` | — | 不要与 `top_p` 同时调节 |
| `top_p` | number(float) | 范围 `[0.01, 1.0]`，限两位小数；GLM-5.3 默认 `0.95` | — | 不要与 `temperature` 同时调节 |
| `max_output_tokens` | integer | 默认 `65536`；最小 `1`，最大 `131072` | — | 模型输出最大 tokens（含回答与思维链） |
| `stop` | array[string] | 可选 | — | 遇到其中任一字符串时停止生成 |
| `tools` | array | 可选 | `function` / `namespace` / `custom` / `web_search` | 可调用工具 |
| `tool_choice` | string | 可选 | `none`（不调用任何工具）、`auto`（由模型判断） | — |
| `reasoning` | object | 可选 | `{"effort": ...}` | 限制深度思考工作量 |
| `text` | object | 可选 | `{"format": {"type": "text"\|"json_object"}}` | 模型文本输出格式 |
| `prompt_cache_key` | string | 可选 | — | 用于集群路由，以提高缓存命中率 |
| `previous_response_id` | string | 可选 | — | 上一轮 `id`，用于多轮；须 `store=true`，有效期 7 天 |
| `store` | boolean | 默认 `false` | — | 是否保存本次响应，为 `true` 时可用于查询、删除和多轮 |

`input` 输入项（`InputItem`）：

| 输入项 | 必填字段 | 说明 |
| :- | :- | :- |
| `message` | `type: "message"`、`role`（`user`/`system`/`assistant`/`developer`）、`content` | content 支持字符串、单个内容块或内容块数组 |
| `function_call` | `type: "function_call"`、`name`、`call_id`、`arguments` | 回传工具命中；另有可选 `namespace` |
| `function_call_output` | `type: "function_call_output"`、`call_id`、`output` | 回传工具执行结果 |
| `reasoning` | `type: "reasoning"`、`content`（`reasoning_text` 部分） | 回传思维链 |

内容块 `InputContentPart` 取值：`input_text`（+`text`）、`input_image`（+`image_url`，URL 或 base64）、`input_file`（+`file_data` 或 `file_url`）、`output_text`（回放助手文本，+`text`）。

工具 `Tool` 取值：`function`（`type`、`name` 必填，另有 `description`、`parameters`）、`namespace`（`type`、`name` 必填，另有 `description`、`tools`：该命名空间内的函数工具数组）、`custom`（`type`、`name` 必填，另有 `description`）、`web_search`（仅 `type` 必填，服务端网络搜索）。

`reasoning.effort`：默认 `max`，取值 `none`、`minimal`、`low`、`medium`、`high`、`xhigh`、`max`；`none`/`minimal` 放弃思考，`low`/`medium` 映射为 `high`，`xhigh` 映射为 `max`。

`reasoning` 页面级参数说明（文档表格）：`reasoning.effort` 默认 `max`；`text.format.type` 默认 `text`，可选 `text` 或 `json_object`。

---

### 4. Anthropic(Claude) 兼容端点字段差异

下表只列本家与《通用基线》§0.7 不同的内容；文档明确给出的差异只有以下内容：

| 项目 | 取值 |
| :- | :- |
| base_url | `https://open.bigmodel.cn/api/anthropic` |
| method + path | `POST https://open.bigmodel.cn/api/anthropic/v1/messages` |
| 请求头 | `x-api-key: YOUR_API_KEY`、`content-type: application/json` |
| `model` | 使用智谱模型编码，例如 `glm-5.3`、`glm-5.2` |
| `max_tokens` | 调用示例中为 `1024` |
| `stream` | 支持 `true` |
| `messages` | 形如 `[{"role": "user", "content": "Hello, ZHIPU"}]` |

文档原文提示：「某些场景下智谱与 Claude 接口仍存在差异，但不影响整体兼容性」，并称「完整字段以……为准」式的字段级差异清单**未在文档中给出**（未抓到字段级对照表页）。SDK 使用方式：Anthropic Python SDK 用 `anthropic.Anthropic(api_key=..., base_url="https://open.bigmodel.cn/api/anthropic")`，TypeScript 用 `@anthropic-ai/sdk` + `baseURL`，Java 用 `com.anthropic:anthropic-java:2.6.0` + `.baseUrl(...)`。环境变量建议 `ANTHROPIC_API_KEY`。

---

### 5. 响应字段与流式结构

标准顶层字段与分块形态（`id`、`created`、`model`、`choices`、`usage` 及 `choices[].index`/`choices[].message`/`choices[].delta` 的公共结构）见《通用基线》§0.5；本节只列本家特有字段与取值。

#### 5.1 非流式响应（ChatCompletionResponse）本家特有字段

| 字段 | 类型 | 说明 |
| :- | :- | :- |
| `request_id` | string | 请求 ID |
| `choices[].finish_reason` | string | 推理终止原因：`stop`（自然结束或触发 stop 词）、`tool_calls`（命中函数）、`length`（达到 token 长度限制）、`sensitive`（内容被安全审核接口拦截）、`network_error`（模型推理异常）、`model_context_window_exceeded`（超出模型上下文窗口） |
| `usage.prompt_tokens_details.cached_tokens` | number | 命中的缓存 Token 数量 |
| `usage.total_tokens` | integer | Token 总数；glm-4-voice 模型 1 秒音频 = 12.5 Tokens，向上取整 |
| `video_result` | array | 视频生成结果；`url` 视频链接、`cover_image_url` 视频封面链接 |
| `web_search` | array | 使用 `WebSearchToolSchema` 时返回的网页搜索信息 |
| `web_search[].icon` / `title` / `link` / `media` / `publish_date` / `content` / `refer` | string | 来源网站图标 / 搜索结果标题 / 网页链接 / 媒体来源名称 / 网站发布时间 / 引用的文本内容 / 角标序号 |
| `content_filter` | array | 内容安全相关信息 |
| `content_filter[].role` | string | 安全生效环节：`assistant` 模型推理、`user` 用户输入、`history` 历史上下文 |
| `content_filter[].level` | integer | 严重程度 `level 0-3`，`level 0` 最严重，`3` 最轻微 |

`choices[].message`（ChatCompletionResponseMessage）本家特有字段（`role` 默认 `assistant`；标准字段见《通用基线》§0.5）：

| 字段 | 类型 | 说明 |
| :- | :- | :- |
| `content` | string / array / null | 文本内容；调用函数时为 `null`；GLM-4.5V 系列可能包含 `<think> </think>`、`<|begin_of_box|> <|end_of_box|>` 标签；GLM-4V 系列为多模态数组 |
| `reasoning_content` | string | 思维链内容，仅在使用 glm-4.5 系列、glm-4.1v-thinking 系列模型时返回 |
| `audio` | object | glm-4-voice 模型返回的音频内容：`id`（可用于多轮对话输入）、`data`（base64 编码）、`expires_at`（过期时间） |
| `tool_calls` | array | 生成的函数名称和参数，元素结构见 5.3 |

#### 5.2 SSE 流式 chunk（ChatCompletionChunk）本家特有字段

chunk 公共结构（`id`、`choices[]`、`created`、`model`、首块 `delta.role`、`finish_reason` 生成中为 `null`）见《通用基线》§0.5；本家特有字段：

| 字段 | 类型 | 说明 |
| :- | :- | :- |
| `choices[].delta.content` | string / array / null | 增量文本；GLM-4.5V 系列可能包含 `<think> </think>`、`<|begin_of_box|> <|end_of_box|>` 边界标签；使用 `tool_calls` 时可能为 `null` |
| `choices[].delta.reasoning_content` | string | 思维链内容，仅 glm-4.5 系列支持 |
| `choices[].delta.audio` | object | `id` / `data` / `expires_at`（glm-4-voice） |
| `choices[].delta.tool_calls[]` | array | 逐步生成的工具调用：`index`（工具调用索引）、`id`（唯一标识符）、`type`（目前支持 `function`）、`function.name`、`function.arguments` |
| `choices[].finish_reason` | string | 枚举 `stop`、`length`、`tool_calls`、`sensitive`、`network_error` |
| `usage` | object | 本次调用的 tokens 数量统计：`prompt_tokens`、`completion_tokens`、`total_tokens`（仅最后一个 chunk 给出） |
| `content_filter` | array | `role`（assistant / user / history）、`level`（0-3，`0` 最严重，`3` 最轻微） |

流式响应格式说明（官方）：每个事件包含 `choices[0].delta.content`（增量文本）、`choices[0].delta.reasoning_content`（增量思考）、`choices[0].finish_reason`（仅最后一个 chunk）、`usage`（仅最后一个 chunk）。结束标记：**返回 `data: [DONE]`**（对话补全协议）。Response API 例外——结束以 `response.completed` / `failed` / `incomplete` / `error` 为准，**不发送** `data: [DONE]`。

官方 SSE 示例（节选原文，含本家 `prompt_tokens_details.cached_tokens` 与 `data: [DONE]`）：

```
data: {"id":"1","created":1677652288,"model":"glm-5.2","choices":[{"index":0,"delta":{"content":"春"},"finish_reason":null}]}

data: {"id":"1","created":1677652288,"model":"glm-5.2","choices":[{"index":0,"delta":{"content":"天"},"finish_reason":null}]}

data: {"id":"1","created":1677652288,"model":"glm-5.2","choices":[{"index":0,"finish_reason":"stop","delta":{"role":"assistant","content":""}}],"usage":{"prompt_tokens":8,"completion_tokens":262,"total_tokens":270,"prompt_tokens_details":{"cached_tokens":0}}}

data: [DONE]
```

#### 5.3 工具调用响应字段（ChatCompletionResponseMessageToolCall）

| 字段 | 类型 | 说明 |
| :- | :- | :- |
| `function.name` | string | 生成的函数名称 |
| `function.arguments` | string | 生成的函数调用参数的 JSON 格式字符串；调用函数前请验证参数 |
| `mcp` | object | MCP 工具调用参数 |
| `mcp.id` | string | mcp 工具调用唯一标识 |
| `mcp.type` | string | `mcp_list_tools`、`mcp_call` |
| `mcp.server_label` | string | MCP 服务器标签 |
| `mcp.error` | string | 错误信息 |
| `mcp.tools` | array | `type = mcp_list_tools` 时的工具列表：`name`、`description`、`annotations`、`input_schema`（`type` 固定 `object`、`properties`、`required`、`additionalProperties`） |
| `mcp.arguments` | string | 工具调用参数（json 字符串） |
| `mcp.name` | string | 工具名称 |
| `mcp.output` | object | 工具返回的结果输出 |
| `id` | string | 命中函数的唯一标识符 |
| `type` | string | 调用的工具类型，目前仅支持 `function`、`mcp` |

#### 5.4 Response API 响应对象（Response）

| 字段 | 类型 | 说明 |
| :- | :- | :- |
| `id` | string | 本次请求的唯一标识 |
| `object` | string | 固定为 `response` |
| `created_at` | integer(int64) | 请求创建时间，Unix 秒时间戳 |
| `model` | string | 模型名称 |
| `instructions` | string | 系统指令 |
| `max_output_tokens` | integer(int64) | 模型输出最大 token 数，包含回答和思维链 |
| `status` | string | `completed`、`failed`、`in_progress`、`incomplete` |
| `temperature` | number(float) | 采样温度 |
| `top_p` | number(float) | 核采样概率阈值 |
| `text` | object | `TextConfig`：`format.type` = `text` / `json_object` |
| `tools` | array | 同入参 `tools` |
| `error` | object / null | 模型未能生成响应时的错误对象：`code`、`message` |
| `incomplete_details` | object / null | 响应未能完成的细节：`reason` |
| `output` | array | 本轮输出：回答（`message`）、思维链（`reasoning`）、工具调用（`function_call`）、联网搜索（`web_search_call`） |
| `usage.input_tokens` | integer | 输入 token 数量 |
| `usage.input_tokens_details.cached_tokens` | integer | 缓存 tokens |
| `usage.output_tokens` | integer | 模型输出 tokens 数量 |
| `usage.output_tokens_details.reasoning_tokens` | integer | 模型思考的 tokens 数量 |

`output` 项结构：`OutputMessage`（`type: message`、`role: assistant`、`status`、`id`、`content`＝`output_text` 部分）、`OutputReasoning`（`type: reasoning`、`status`、`id`、`content`＝`reasoning_text` 部分）、`OutputFunctionCall`（`type: function_call`、`namespace`、`status`、`id`、`name`、`call_id`、`arguments`）、`OutputWebSearchCall`（`type: web_search_call`、`status`、`id`、`action.type: search`、`action.queries[]`、`action.source[]`、`result[]`）。

Response API 流式事件（全部含 `sequence_number`，int64）：

`response.created`、`response.in_progress`、`response.completed`、`response.failed`、`response.incomplete`、`response.output_item.added`、`response.output_item.done`、`response.content_part.added`、`response.content_part.done`、`response.output_text.delta`、`response.output_text.done`、`response.function_call_arguments.delta`、`response.function_call_arguments.done`、`response.reasoning_text.delta`、`response.reasoning_text.done`、`response.web_search_call.in_progress`、`response.web_search_call.searching`、`response.web_search_call.completed`、`error`。

（`response.output_text.delta` 带 `output_index`、`content_index`、`item_id`、`delta`；`response.function_call_arguments.done` 带 `arguments`；`response.reasoning_text.done` 带 `text`；`error` 事件带 `code`、`message`。）

#### 5.5 网络搜索 API 响应（WebSearchResponse）

| 字段 | 类型 | 说明 |
| :- | :- | :- |
| `id` | string | 任务 ID |
| `created` | integer | 请求创建时间，秒级 Unix 时间戳 |
| `request_id` | string | 请求标识符 |
| `search_intent` | array | `query`（原始搜索 query）、`intent`（`SEARCH_ALL` 搜索全网 / `SEARCH_NONE` 无搜索意图 / `SEARCH_ALWAYS` 强制搜索模式，当 `search_intent=false` 时返回此值）、`keywords`（改写后的搜索关键词） |
| `search_result` | array | `title`、`content`（内容摘要）、`link`、`media`（网站名称）、`icon`（网站图标）、`refer`（角标序号）、`publish_date`（网站发布时间） |

---

### 6. 思考/推理字段

#### 6.1 字段定义

`thinking` 对象（ChatThinking，仅 GLM-4.5 及以上模型支持）：

| 字段 | 类型 | 默认 | 取值 | 说明 |
| :- | :- | :- | :- | :- |
| `type` | string | `enabled` | `enabled` / `disabled` | GLM-5.3、GLM-5.3-FLASH 系列限制只能开启，由 `reasoning_effort` 控制思考强度。其它模型开启后：GLM-5.2 / 5.1 / 5 / 5-Turbo / 5v-Turbo / 4.6 / 4.6V / 4.5 为模型自动判断是否思考；GLM-4.7 / GLM-4.5V 为强制思考 |
| `clear_thinking` | boolean | `true` | `true` / `false` | `true`：本次请求忽略/移除历史 turns 的 `reasoning_content`；`false`：保留历史 turns 的 `reasoning_content` 并随上下文提供（启用 Preserved Thinking 时必须完整、未修改、按原顺序透传，否则效果下降或无法生效）。该参数只影响跨 turn 的历史 thinking blocks，不改变当前 turn 是否产生思考 |

`reasoning_effort`（字符串，`thinking` 开启时生效，默认 `max`，仅 GLM-5.2 及以上支持）：

| 模型 | 支持的档位 |
| :- | :- |
| GLM-5.3 / GLM-5.3-FLASH / GLM-5.3-FLASHX | 仅 `max`、`high`、`low`（其余输入将报错）；`low`-轻量推理、`high`-增强推理、`max`-深度推理（默认） |
| GLM-5.2 | `max`（默认且推荐）、`xhigh`、`high`、`medium`、`low`、`minimal`、`none`；`none` 或 `minimal` 代表模型放弃思考；`low`/`medium` 映射为 `high`；`xhigh` 映射为 `max` |

Coding Plan 请求中的档位映射（文档单独列出）：

- GLM-5.3 / GLM-5.3-FLASH / GLM-5.3-FLASHX：`none`、`minimal`、`low` 映射为 `low`；`medium`、`high` 映射为 `high`；`xhigh`、`max` 映射为 `max`。
- GLM-5.2：`none` 或 `minimal` 代表模型放弃思考；`low` / `medium` 映射为 `high`；`xhigh` 映射为 `max`。

#### 6.2 各模型思考行为差异（官方原文归纳）

| 模型 | thinking.type 行为 |
| :- | :- |
| GLM-5.3、GLM-5.3-FLASH、GLM-5.3-FLASHX | 强制需开启；API 请求中 `thinking.type` 传 `disabled` 将会报错 |
| GLM-4.7、GLM-4.5V | 强制思考 |
| GLM-5.2、GLM-5.1、GLM-5、GLM-5-Turbo、GLM-5v-Turbo、GLM-4.6、GLM-4.6V、GLM-4.5 | 模型自动判断是否思考（动态思考） |
| GLM-4.6 | 与 GLM-5.3/5.2/5.1/5/4.7 系列「默认开启 Thinking」不同，GLM-4.6 默认为「混合 thinking（自动开启）」 |

「默认思考行为」段落原文：GLM-5.3、GLM-5.3-FLASH、GLM-5.2、GLM-5.1、GLM-5、GLM-4.7 系列默认开启 Thinking；`GLM-5.3`、`GLM-5.3-FLASH` 强制思考不能关闭。

#### 6.3 三种思考机制（思考模式页）

- **交错式思考（Interleaved thinking）**：默认支持（从 GLM 4.5 开始），使 GLM 可以在工具调用之间、以及收到工具结果之后继续思考。注意：使用「交错思考 + 工具」时必须显式保留 Reasoning content，并在返回工具结果时一并返回。
- **保留式思考（Preserved thinking）**：编码场景中模型可在上下文中保留来自先前 assistant 回合的 reasoning content。该能力在 Coding Plan 端点默认开启、标准 API 端点默认关闭；标准 API 端点可通过 `"clear_thinking": False` 开启，且需要将完整、未修改的 reasoning content 传回 API；所有连续 reasoning content 必须与模型原始请求期间生成的序列完全一致，不要重新排序或修改。
- **轮级思考（Turn-level Thinking）**：按轮控制推理计算，同一调用会话中每一轮请求都可以独立选择开启/关闭思考；这是 GLM-4.7 新引入的能力。

#### 6.4 响应中的思考字段

- 非流式：`choices[0].message.reasoning_content`（仅 GLM-4.5 系列、GLM-4.1v-thinking 系列模型返回）。
- 流式：`choices[0].delta.reasoning_content`（「思维链内容, 仅 `glm-4.5` 系列支持」）。
- Response API 侧：入参与出参用 `reasoning`（`effort`）+ `output` 中 `type: reasoning` 的项，流式事件 `response.reasoning_text.delta` / `response.reasoning_text.done`；`usage.output_tokens_details.reasoning_tokens` 统计思考 tokens。
- 官方响应示例里 `usage.completion_tokens_details.reasoning_tokens` 出现在上下文缓存页的响应示例中。

---

### 7. 工具调用字段

#### 7.1 `tools` 入口的四种 schema

`tools` 是一个 `anyOf` 数组，元素可为以下四种：

| schema | 必填 | 字段 |
| :- | :- | :- |
| `FunctionToolSchema`（title: Function Call） | `type`、`function` | `type`（默认/枚举 `function`）、`function` → `FunctionObject`；`additionalProperties: false` |
| `RetrievalToolSchema`（title: Retrieval） | `type`、`retrieval` | `type`（默认/枚举 `retrieval`）、`retrieval` → `RetrievalObject`；`additionalProperties: false` |
| `WebSearchToolSchema`（title: Web Search） | `type`、`web_search` | `type`（默认/枚举 `web_search`）、`web_search` → `WebSearchObject`；`additionalProperties: false` |
| `MCPToolSchema`（title: MCP） | `type`、`mcp` | `type`（默认/枚举 `mcp`）、`mcp` → `MCPObject`；`additionalProperties: false` |

`FunctionObject`（`required: [name, description, parameters]`）：

| 字段 | 类型 | 约束 | 说明 |
| :- | :- | :- | :- |
| `name` | string | `minLength: 1`、`maxLength: 64`、`pattern: ^[a-zA-Z0-9_-]+$` | 必须是 `a-z、A-Z、0-9`，或包含下划线和破折号 |
| `description` | string | — | 函数功能描述，供模型选择何时以及如何调用函数 |
| `parameters` | object | `additionalProperties: true` | 使用 JSON Schema 定义的参数；不需要参数时省略 |

`RetrievalObject`（`required: [knowledge_id]`）：`knowledge_id`（知识库 ID）、`prompt_template`（含占位符 `{{ knowledge }}` 和 `{{ question }}` 的自定义模板；文档给出默认模板原文）。

`WebSearchObject`（`required: [search_engine]`）：

| 字段 | 类型 | 默认 | 取值 |
| :- | :- | :- | :- |
| `enable` | boolean | `false` | 启用搜索时设为 `true` |
| `search_engine` | string | `search_std` | `search_std`、`search_pro`、`search_pro_sogou`、`search_pro_quark` |
| `search_query` | string | — | 强制触发搜索 |
| `search_intent` | string | 默认执行搜索意图识别 | `true` 执行搜索意图识别；`false` 跳过意图识别直接执行搜索 |
| `count` | integer | 默认 `10`；范围 `1-50` | `search_pro_sogou` 可选枚举 `10、20、30、40、50` |
| `search_domain_filter` | string | — | 仅返回指定白名单域名内容（如 `www.example.com`）；支持 `search_std`、`search_pro`、`search_pro_sogou` |
| `search_recency_filter` | string | `noLimit` | `oneDay`、`oneWeek`、`oneMonth`、`oneYear`、`noLimit` |
| `content_size` | string | `medium` | `medium`（摘要）、`high`（最大化上下文） |
| `result_sequence` | string | `after` | `before` / `after`，指定搜索结果返回顺序在模型回复结果之前还是之后 |
| `search_result` | boolean | `false` | 是否返回搜索来源详细信息 |
| `require_search` | boolean | `false` | 是否强制搜索结果才返回回答 |
| `search_prompt` | string | 文档给出默认 Prompt 原文 | 定制搜索结果处理的 Prompt |

`MCPObject`（`required: [server_label]`）：`server_label`（连智谱的 mcp server 时以 mcp code 填充，无需填写 `server_url`）、`server_url`、`transport_type`（默认 `streamable-http`，可选 `sse`、`streamable-http`）、`allowed_tools`（数组）、`headers`（鉴权信息）。

#### 7.2 工具选择与并行

- `tool_choice`：**默认 `auto` 且仅支持 `auto`**（仅当工具类型为 `function` 时补充）。视觉模型中仅 GLM-4.6V 支持此参数。Response API 侧 `tool_choice` 为字符串枚举 `none` / `auto`。
- `tools` 最多支持 **128 个函数**。
- 常见问题页原文：「tools 支持传多个函数，但每次调用只能命中一个。」「函数调用，知识库检索，网络搜索，3 个功能互斥。如果同时使用，按照优先级只会生效一个。优先级顺序为：函数调用>知识库检索>网络搜索。」
- 文档中**未给出**并行工具调用（parallel tool calls）开关或相关字段说明。

#### 7.3 工具流式输出 `tool_stream`

| 字段 | 类型 | 默认 | 说明 |
| :- | :- | :- | :- |
| `tool_stream` | boolean | `false` | 是否开启流式响应 Function Calls；仅限 GLM-5.3、GLM-5.2、GLM-5.1、GLM-5、GLM-5-Turbo、GLM-4.7、GLM-4.6 系列支持此参数 |

- 作用：调用 `chat.completions` 时在不进行缓冲或 JSON 验证的情况下流式传输工具使用参数，从而减少调用延迟。
- 官方明确：「`tool_stream` 仅改变工具调用参数的**返回方式**（由一次性返回变为随流逐步返回），平台不会替您执行工具。收到完整参数后，仍需由应用解析并执行工具、将结果以 `role: "tool"` 回传 `messages` 并再次调用模型，才能获得最终回答。」
- 流式响应 `delta` 中相关字段：`reasoning_content`（模型推理过程文本）、`content`（模型回答文本）、`tool_calls`（工具调用信息，含函数名和参数）。
- 需同时打开 `stream=True` 与 `tool_stream=True`。

#### 7.4 工具结果回传格式

- Chat Completions：新增一条 `{"role": "tool", "content": ..., "tool_call_id": tool_call.id}` 消息（标准形态见《通用基线》§0.4）。
- Response API：新增一个 `{"type": "function_call_output", "call_id": ..., "output": ...}` 输入项，配合 `previous_response_id` 续写（标准形态见《通用基线》§0.6）。

---

### 8. 多模态与文件

#### 8.1 输入模态总表

| 输入模态 | 协议与端点 | 字段名与嵌套结构 | 传法（URL / base64） | 格式白名单 | 体积 / 分辨率 / 时长 / 张数上限 | 支持的模型（文档明示） |
| :- | :- | :- | :- | :- | :- | :- |
| 文本 | Chat Completions `POST /api/paas/v4/chat/completions` | `messages[].content` 可直接是字符串；多模态时为内容块 `{"type": "text", "text": ...}` | — | — | `messages` 必填且 `minItems: 1`，不能只包含系统消息或助手消息 | 全部对话模型 |
| 文本 | Responses `POST /api/v1/responses` | `input` 为字符串；或 `{"type": "message", "role": ..., "content": [...]}` 内用内容块 `{"type": "input_text", "text": ...}`（回放助手文本用 `{"type": "output_text", "text": ...}`） | — | — | `input` 必填 | 文档示例为 `glm-5.3` |
| 文本 | Anthropic 兼容 `POST /api/anthropic/v1/messages` | `messages[].content` 为字符串（文档示例 `[{"role": "user", "content": "Hello, ZHIPU"}]`）；内容块数组形式的字段名**文档未给出** | — | — | 文档未给出 | 文档示例为 `glm-5.3`、`glm-5.2` |
| 图片 | Chat Completions（视觉模型） | `content[].type = "image_url"`、`image_url.url`（必填） | `image_url.url` ：图片 URL **或** Base64 编码；`GLM-4V-Flash` 不支持 Base64 | `jpg`、`png`、`jpeg` | 每张图像 ≤ `5M`，像素 ≤ `6000*6000`；张数：`GLM-5.3-Flash` 系列 / `GLM-5V-Turbo` / `GLM-4.6V` / `GLM-4.5V` 系列 `50` 张；`GLM-4V-Plus-0111` `5` 张；`GLM-4V-Flash` `1` 张 | 视觉模型（第 2.2 节模型名单） |
| 图片 | Responses | `{"type": "input_image", "image_url": ...}` | `image_url`：图片 URL 或 base64 | 文档未给出 | 文档未给出 | 文档未给出 |
| 图片 | Anthropic 兼容（`image` 块） | **文档未给出**（该端点的字段级差异清单未在文档中给出） | 文档未给出 | 文档未给出 | 文档未给出 | — |
| 视频 | Chat Completions（视觉模型） | `content[].type = "video_url"`、`video_url.url`（必填） | 文档只给出 URL 传法（`video_url.url` 为视频的 URL 地址）；Base64 传法文档未给出 | `mp4`、`mkv`、`mov` | `GLM-5.3-Flash` 系列 / `GLM-5V-Turbo` / `GLM-4.6V` / `GLM-4.5V` 系列 ≤ `200M`；`GLM-4V-Plus` ≤ `20M` 且时长 ≤ `30s`；其他多模态模型 ≤ `200M`。注意 `GLM-4V-Plus-0111` 的 `video_url` 必须在 `content` 数组第一位 | `GLM-5.3-Flash` 系列 / `GLM-5V-Turbo` / `GLM-4.6V` / `GLM-4.5V` 系列等 |
| 视频 | Responses | **文档未给出**（`InputContentPart` 只有 `input_text` / `input_image` / `input_file` / `output_text`） | 文档未给出 | 文档未给出 | 文档未给出 | — |
| 视频 | Anthropic 兼容 | **文档未给出** | 文档未给出 | 文档未给出 | 文档未给出 | — |
| 音频（输入） | Chat Completions（音频模型） | `content[].type = "input_audio"`、`input_audio.data`（base64）、`input_audio.format`（均必填） | `input_audio.data` 为语音文件的 base64 编码 | `wav`、`mp3` | 音频最长不超过 `10` 分钟；`1s` 音频 = `12.5 Tokens`，向上取整 | 仅 `glm-4-voice` |
| 音频（输入） | Responses / Anthropic 兼容 | **文档未给出** | 文档未给出 | 文档未给出 | 文档未给出 | — |
| 文件 | Chat Completions（视觉模型） | `content[].type = "file"`、`file.file_id` / `file.file_url` / `file.file_data` 选其一，另有 `file.filename` | `file_id`（文件上传接口返回的 ID）、`file_url`（URL）、`file_data`（`data:<MIME>;base64,<BASE64_DATA>`） | `pdf`、`txt`、`word`、`jsonl`、`xlsx`、`pptx` 等 | 单文件 ≤ `50M`；`file_url` 最多支持 `50` 个 | `GLM-5.3-Flash` 系列、`GLM-4.6V` 系列、`GLM-4.5V` 系列（模型页将「文件」列为输入模态） |
| 文件 | Responses | `{"type": "input_file", "file_data": ...}` 或 `{"type": "input_file", "file_url": ...}` | `file_data` 文件 base64；`file_url` 文件 URL | 文档未给出 | 文档未给出 | 文档未给出 |
| 文件 | Anthropic 兼容（`document` 块） | **文档未给出** | 文档未给出 | 文档未给出 | 文档未给出 | — |

补充说明（均为文档原文口径）：

- 视觉模型 `messages` 支持纯文本或多模态 content（文本、图片、视频、文件）；音频模型 `messages` 支持文本和音频内容；`assistant` 消息可带 `audio.id` 复用上一轮音频。
- 多模态 `content` 内容块按模型形态分两套 schema：视觉模型用 `VisionMultimodalContentItem`（`text` / `image_url` / `video_url` / `file`），音频模型用 `AudioMultimodalContentItem`（`text` / `input_audio`），两者均 `additionalProperties: false`。
- 文件类型的写法在文档中存在两种：`GLM-4.6V` / `GLM-4.5V` 模型页示例写作 `{"type": "file_url", "file_url": {"url": ...}}`，而对话补全 API 参考的 schema 只定义 `type: "file"`（其描述称“新文件类型，兼容历史 `file_url` 类型但不建议增量场景再使用”）；两种写法关系**文档未给出**说明。
- 模型页原文（`GLM-4.6V` / `GLM-4.5V`）：「不支持同时理解文件、视频和图像。」
- 对话补全(异步) `POST /api/paas/v4/async/chat/completions` 同样支持多模态（文本、图片、音频、视频、文件）输入，结果经 `GET /api/paas/v4/async-result/{id}` 查询。

#### 8.2 输出模态总表

| 输出模态 | 端点 | 请求关键字段 | 返回形式 | 尺寸 / 时长 / 其他限制 | 是否流式 | 模型 |
| :- | :- | :- | :- | :- | :- | :- |
| 图像生成（同步） | `POST /api/paas/v4/images/generations` | `model`（必填）、`prompt`（必填）、`quality`、`size`、`watermark_enabled`、`user_id` | JSON：`data[].url`（“目前数组中只包含一张图片”）、`created`、`content_filter`；**输出是图片 URL，需通过 URL 下载**，临时链接有效期 `30` 天 | `size`：`glm-image` 推荐 `1280x1280`（默认）、`1568x1056`、`1056x1568`、`1472x1088`、`1088x1472`、`1728x960`、`960x1728`；自定义参数“长宽推荐设置在 `1024px-2048px` 范围内，最大像素数不超过 `2^22px`，长宽均需为 `32` 的整数倍”（模型页写作“长宽需在 `512px-2048px` 范围内，且长宽均需为 `32` 的整数倍”，两页口径不一致，原文如此）。其他模型：`1024x1024`（默认）、`768x1344`、`864x1152`、`1344x768`、`1152x864`、`1440x720`、`720x1440`；自定义需在 `512px-2048px`、被 `16` 整除、最大像素数不超过 `2^21px`。`quality`：`glm-image` 默认 `hd` 且仅支持 `hd`，其它默认 `standard` | 文档未给出（响应只有 JSON） | `model` 枚举：`glm-image`、`cogview-4-250304`、`cogview-4`、`cogview-3-flash` |
| 图像生成（异步） | `POST /api/paas/v4/async/images/generations` | 同上的 `model`、`prompt`、`quality`（仅 `hd`）、`size`、`watermark_enabled`、`user_id` | 创建返回 `model`、`id`、`request_id`、`task_status`（`PROCESSING` / `SUCCESS` / `FAIL`）；结果经 `GET /api/paas/v4/async-result/{id}` 取 `image_result[].url`（有效期 `30` 天），另有 `task_status`、`model`、`request_id` | `size` 限制同同步接口；**仅支持 `GLM-Image` 模型** | 文档未给出 | `glm-image` |
| 视频生成（异步） | `POST /api/paas/v4/videos/generations` | 请求体 `oneOf`：`CogVideoX3Request`（`cogvideox-3`）、`CogVideoXRequest`（`cogvideox-2`、`cogvideox-flash`）、`ViduText2VideoRequest`（`viduq1-text`）、`ViduImage2VideoRequest`（`viduq1-image`、`vidu2-image`）、`ViduFrames2VideoRequest`（`viduq1-start-end`、`vidu2-start-end`）、`ViduReference2VideoRequest`（`vidu2-reference`）；通用字段 `prompt`、`image_url`、`quality`（`speed` 默认 / `quality`）、`with_audio`（默认 `false`）、`watermark_enabled`、`size`、`fps`、`duration`、`aspect_ratio`、`style`、`movement_amplitude`、`request_id`、`user_id` | 创建返回 `model`、`id`、`request_id`、`task_status`；结果经 `GET /api/paas/v4/async-result/{id}` 取 `video_result[].url`（视频链接）与 `cover_image_url`（视频封面链接） | `prompt` ≤ `512` 字符且 `image_url` 与 `prompt` 不能同时为空；图片：`cogvideox-*` 支持 `png/jpeg/jpg`、≤ `5M`，`cogvideox-3` 还可传 2 张图作首尾帧；Vidu 图生视频仅 1 张、首尾帧 2 张（分辨率比 `0.8–1.25`）、参考图 1–3 张（分辨率 ≥ `128x128`），支持 `png/jpeg/jpg/webp`、≤ `50MB`、宽高比须小于 `1:4` 或 `4:1`。尺寸：`cogvideox-3` 枚举 `1280x720`、`720x1280`、`1024x1024`、`1920x1080`、`1080x1920`、`2048x1080`、`3840x2160`（最高 4K），`cogvideox-*` 另有 `720x480`、`1280x960`、`960x1280`，Vidu 各模型枚举默认 `1920x1080` 或 `1280x720`。`fps`：`30` / `60`（默认 `30`）。`duration`：`cogvideox-3` 默认 `5`、支持 `5` / `10`；Vidu `viduq1` 系列固定 `5`，`vidu2` 系列固定 `4`。`with_audio` 仅 `vidu2` 图生视频注明“最终生成的视频时长为 `4` 秒时支持” | 文档未给出 | 视频生成模型（CogVideoX 系列、Vidu 系列） |
| OCR / 文档解析（GLM-OCR） | `POST /api/paas/v4/layout_parsing` | `model`（必填，枚举 `glm-ocr`）、`file`（必填）、`return_crop_images`（默认 `false`）、`need_layout_visualization`（默认 `false`）、`start_page_id` / `end_page_id`（最小值 `1`）、`request_id`、`user_id` | JSON：`md_results`（Markdown 识别结果）、`layout_details`（二维数组，元素 `index`、`label`（`image`/`text`/`formula`/`table`）、`bbox_2d`（归一化坐标 `[x1,y1,x2,y2]`）、`content`、`height`、`width`）、`layout_visualization`（识别结果图片 url 数组）、`data_info`（`num_pages`、`pages[].width/height`）、`usage`、`id`、`created`、`model`、`request_id` | 输入：图片或 PDF，支持 `url` 和 `base64`；格式 `PDF`、`JPG`、`PNG`；单图 ≤ `10MB`、PDF ≤ `50MB`、最大支持 `100` 页。模型页输出模态：文本、图片链接、md 文档；支持中文、英文、法语、西班牙语、俄罗斯语、德语、日语、韩语等 | 文档未给出 | `glm-ocr` |
| 语音合成（文本转语音） | `POST /api/paas/v4/audio/speech` | `model`（必填，`glm-tts`）、`input`（必填，`maxLength: 1024`）、`voice`（必填）、`watermark_enabled`、`stream`（默认 `false`）、`speed`（默认 `1.0`，`[0.5, 2]`）、`volume`（默认 `1.0`，`(0, 10]`）、`encode_format`（`base64` / `hex`，仅流式返回时决定编码格式）、`response_format`（`wav` / `pcm`，默认 `pcm`） | 非流式返回 `audio/wav` 二进制（“采样率建议设置为 24000”）；流式（`stream: true`）经标准 Event Stream 逐块返回 | `voice` 枚举：`tongtong`（默认）、`chuichui`、`xiaochen`、`jam`、`kazi`、`douji`、`luodo`，另支持复刻音色；流式生成音频时**仅支持返回 `pcm`** 格式；模型页称流式首帧响应速度可达 400ms 以内 | 支持（`stream: true`） | `glm-tts` |
| 音色复刻（音频 + 试听文件） | `POST /api/paas/v4/voice/clone`（另有 `GET /api/paas/v4/voice/list`、`POST /api/paas/v4/voice/delete`） | `model`（`glm-tts-clone`）、`voice_name`、`input`（试听目标文本）、`file_id`（示例音频）、`text`（示例音频文本，选填）、`request_id` | 返回 `voice`（音色）、`file_id`（音频试听文件 ID）、`file_purpose`（固定 `voice-clone-output`）、`request_id` | 示例音频 `file_id` 大小限制不超过 `10M`，建议音频时长在 `3` 秒到 `30` 秒之间；示例音频需先用文件上传接口以 `purpose=voice-clone-input`（`mp3`、`wav`）上传 | 文档未给出 | `glm-tts-clone` |
| 语音转文本（输出为文本） | `POST /api/paas/v4/audio/transcriptions`（`multipart/form-data`） | `file`（必填）、`model`（`glm-asr-2512`）、`file_base64`（与 `file` 只需传一个，同时传入以 `file` 为准）、`prompt`、`hotwords`（`maxItems: 100`）、`stream`（默认 `false`）、`request_id`、`user_id` | 非流式 JSON：`id`、`created`、`model`、`request_id`、`text`（音频转录的完整内容）；流式（`text/event-stream`）：`type` 为 `transcript.text.delta`（正在转录）/ `transcript.text.done`（转录完成），含 `delta`，结束时返回 `data: [DONE]` | 音频文件格式 `.wav / .mp3`，文件大小 ≤ `25 MB`、音频时长 ≤ `30` 秒；`prompt` 建议小于 8000 字；`hotwords` 建议不超过 100 个 | 支持（`stream: true`） | `glm-asr-2512` |
| 语音对话输出（音频） | `POST /api/paas/v4/chat/completions`（音频模型） | `messages` 中 `assistant` 可带 `audio.id` 复用上一轮音频 | 非流式 `choices[].message.audio`：`id`（可用于多轮对话输入）、`data`（base64 编码）、`expires_at`（过期时间）；流式 `choices[].delta.audio` 同结构 | Token 口径：`1` 秒音频 = `12.5 Tokens`，向上取整 | 支持（SSE） | `glm-4-voice` |
| 实时音视频（输出音频） | `WSS wss://open.bigmodel.cn/api/paas/v4/realtime` | `chat_mode` 取 `audio`（语音通话）或 `video`（视频通话）；`modalities` 默认 `["text", "audio"]`；`input_audio_format`（`wav` / `pcm`）、`output_audio_format`（当前仅 `pcm`）、`input_audio_noise_reduction`、`turn_detection`、`max_response_output_tokens`（`(0, 1024]`，默认 `1024`） | 服务器事件：`response.text.delta` / `response.text.done`、`response.audio_transcript.delta` / `.done`、`response.audio.delta`（`pcm` 格式 `base64` 音频块）/ `response.audio.done`；另有 `input_audio_buffer.*`、`conversation.item.input_audio_transcription.completed` 等 | 模型页：输入模态视频、音频、文本，输出模态音频；通话记忆时长长达 2 分钟；`input_audio_format` 支持 `wav` 与 `pcm`（PCM 最好带采样率，如 `pcm16`=16000、`pcm24`=24000，默认 16000，仅支持单声道 16 位深）；`output_audio_format` 采样率 24 kHz、单声道、16 位深；Client VAD 单次上传音频最长 `30` 秒，音频发送最高速率 `50QPS`（推荐按 100ms 一帧、每秒 10 帧）；`chat_mode=video` 时提交事件前必须至少上传一张 base64 编码的 jpg 视频帧 | 支持（WebSocket 事件流） | `GLM-Realtime` |

Response API 侧的输出：文档给出的输出项只有 `OutputMessage`（`output_text`）、`OutputReasoning`（`reasoning_text`）、`OutputFunctionCall`、`OutputWebSearchCall`，**没有图片 / 音频 / 视频输出项**；Anthropic 兼容端点的多模态输出**文档未给出**。

#### 8.3 文件上传与管理

| 端点 | method + path | 关键字段 | 说明与限制 |
| :- | :- | :- | :- |
| 上传文件 | `POST https://open.bigmodel.cn/api/paas/v4/files`（`multipart/form-data`） | `file`（必填）、`purpose`（必填，枚举 `batch` / `code-interpreter` / `agent` / `voice-clone-input`，描述中另列出 `user_data`） | 返回 `FileObject`：`id`、`object`（固定 `file`）、`bytes`、`created_at`、`filename`、`purpose`。用途限制：（`batch`）`.jsonl`，单文件 ≤ 100 MB，文件数不超过 1000 个；（`code-interpreter`）`pdf、docx、doc、xls、xlsx、txt、png、jpg、jpeg、csv`，单文件 ≤ 20M，图片 ≤ 5M，文件数不超过 100 个；（`agent`）格式同上，单文件 ≤ 20M，图片 ≤ 5M，文件数不超过 1000 个；（`voice-clone-input`）`mp3、wav`；（`user_data`）`pptx、ppt、docx、doc、xlsx、xls、pdf`，限制每个用户最多存储 1T 文件 |
| 文件列表 | `GET https://open.bigmodel.cn/api/paas/v4/files` | query：`after`（分页游标）、`purpose`（必填，枚举 `batch` / `code-interpreter` / `agent`）、`order`（`created_at`）、`limit`（`1-100`，默认 `20`） | 获取已上传文件的分页列表，支持按用途和排序过滤 |
| 删除文件 | `DELETE https://open.bigmodel.cn/api/paas/v4/files/{file_id}` | 路径参数 `file_id` | 永久删除指定文件及其所有关联数据（文档页标题级描述） |
| 文件内容 | `GET https://open.bigmodel.cn/api/paas/v4/files/{file_id}/content` | 路径参数 `file_id` | 获取文件内容，**只支持 `batch` 文件类型** |
| 在对话中引用文件 | Chat：`content[].type="file"` 内的 `file.file_id` / `file.file_url` / `file.file_data`；Responses：`input_file.file_url` / `input_file.file_data` | 同 8.1「文件」行 | `file_id` 取自上传文件接口 |

#### 8.4 相关端点与限制汇总

多模态/文件相关端点：

| 用途 | method + path |
| :- | :- |
| 对话补全（多模态输入） | `POST https://open.bigmodel.cn/api/paas/v4/chat/completions` |
| 对话补全（异步） | `POST https://open.bigmodel.cn/api/paas/v4/async/chat/completions` |
| 图像生成 / 图像生成（异步） | `POST https://open.bigmodel.cn/api/paas/v4/images/generations`、`POST .../async/images/generations` |
| 视频生成（异步） | `POST https://open.bigmodel.cn/api/paas/v4/videos/generations` |
| 查询异步结果 | `GET https://open.bigmodel.cn/api/paas/v4/async-result/{id}` |
| 文档解析（GLM-OCR） | `POST https://open.bigmodel.cn/api/paas/v4/layout_parsing` |
| 文件解析（异步）/ 解析结果 / 文件解析（同步） | `POST .../files/parser/create`、`GET .../files/parser/result/{taskId}/{format_type}`、`POST .../files/parser/sync` |
| OCR 服务 | `POST https://open.bigmodel.cn/api/paas/v4/files/ocr`（`multipart/form-data`，`file` 与 `tool_type` 必填；`tool_type` 枚举 `hand_write`；另有 `language_type`、`probability`，默认 `false`） |
| 语音转文本 / 文本转语音 | `POST .../audio/transcriptions`、`POST .../audio/speech` |
| 音色复刻 / 音色列表 / 删除音色 | `POST .../voice/clone`、`GET .../voice/list`、`POST .../voice/delete` |
| 文件上传 / 列表 / 删除 / 内容 | 见 8.3 |
| 实时音视频 | `WSS wss://open.bigmodel.cn/api/paas/v4/realtime` |

各模型模态支持差异（模型页/模型概览原文）：

| 模型 | 输入模态 | 输出模态 |
| :- | :- | :- |
| GLM-5.3 | 仅文本 | 文本 |
| GLM-5.3-Flash / FlashX | 视频、图像、文本、文件 | 文本 |
| GLM-4.6V / 4.6V-FlashX / 4.6V-Flash | 视频、图像、文本、文件（“不支持同时理解文件、视频和图像”） | 文本 |
| GLM-4.5V | 视频、图像、文本、文件（“不支持同时理解文件、视频和图像”） | 文本 |
| GLM-5V-Turbo、GLM-4.1V-Thinking 系列、GLM-4V 系列、AutoGLM-Phone | 模型页模态卡片未逐项给出（图片张数上限见 8.1）；模型概览列为视觉理解模型 | 文本 |
| GLM-OCR | PDF、图片（JPG、PNG）；单图 ≤ 10 MB，PDF ≤ 50 MB；最大支持 100 页 | 文本、图片链接、md 文档 |
| GLM-Image | 文本（最大输入 1000 字符） | 图像（多分辨率支持 1:1、3:4、4:3、16:9 等） |
| CogView-4 / CogView-3-Flash | 文本（`/images/generations` 的 `prompt` 必填） | 图像 |
| GLM-ASR-2512 | 音频 | 文本 |
| GLM-TTS | 文本 | 音频 |
| GLM-TTS-Clone | 文本、音频（模型概览「多模态支持」列） | 音频 |
| GLM-4-Voice | 文本、音频 | 文本、音频（对话补全返回 `message.audio`） |
| GLM-Realtime | 视频、音频、文本 | 音频 |
| CogVideoX-3 | 图像、文本、首尾帧 | 视频 |
| Vidu Q1 / Vidu 2 / CogVideoX-Flash | 模型概览列为「图像、文本、首尾帧」「图像、参考、首尾帧」「图像、文本」 | 视频 |

文件解析（`/files/parser/*`）限制（文档表格原文）：Prime 支持 `pdf、docx、doc、xls、xlsx、ppt、pptx、png、jpg、jpeg、csv、txt、md、html、bmp、gif、webp、heic、eps、icns、im、pcx、ppm、tiff、xbm、heif、jp2`，其中 PDF/DOC/DOCX/PPT ≤ 100MB、XLS/XLSX/CSV ≤ 10MB、PNG/JPG/JPEG ≤ 20MB，结果含图片 + Markdown + 布局 json；Expert 仅 `pdf`、≤ 100M，结果图片 + Markdown；Lite 支持 `pdf、docx、doc、xls、xlsx、ppt、pptx、png、jpg、jpeg、csv、txt、md`、≤ 50M，结果为纯文本（无图片）。异步接口的 `tool_type` 枚举为 `lite` / `expert` / `prime`，同步接口为 `prime-sync`；异步先创建任务拿 `task_id` 再轮询；解析结果下载有效期 24 小时。

文档未给出的项（本节范围内）：

- Anthropic(Claude) 兼容端点的多模态字段（图片 / 文档内容块，如 `image`、`document`）：**文档未给出**（只有 base_url / path / header / 纯文本示例，见第 4 节）。
- Response API 的视频、音频输入项，以及任何图片 / 音频 / 视频输出项：**文档未给出**。
- 图像生成、图像生成（异步）、视频生成、文档解析是否支持流式：**文档未给出**（响应 schema 只有 JSON）。
- `video_url` 的 Base64 传法（对话补全视频输入）：**文档未给出**（只给了 URL）。
- 图片 / 音频 / 文件的张数或数量在单条消息内的合计上限（除 8.1 已列的单件与张数限制外）：**文档未给出**。
- 文本转语音的输出时长上限：**文档未给出**（只给了 `input` 的 `maxLength: 1024`）。
- GLM-OCR、文件解析的流式输出：**文档未给出**。
- 各模型页之外的独立「多模态限制」汇总页：**文档中不存在**（限制分散在对话补全 API 参考、模型概览与各模型页）。

---

### 9. 缓存与成本字段

#### 9.1 上下文缓存机制

- 自动缓存识别：**隐式缓存**，智能识别重复的上下文内容，无需手动配置。
- 触发条件（文档原文）：「要触发上下文缓存，重复的前缀内容必须足够长（**建议 500 Token 以上**），两三句话的短系统提示词通常无法命中。」
- 缓存为异步生效，首次请求后稍等片刻再发起后续请求效果更好。
- 计费说明中的差异：**仅适用于标准 API 计费，不包括资源包和 GLM Coding Plan 套餐**。
  - 新内容 Token：按标准价格计费
  - 缓存命中 Token：按优惠价格计费（**通常为标准价格的 50%**）
  - 输出 Token：按标准价格计费
- 文档计费示例：标准价格 0.01 元/1K Token；总输入 2000、缓存命中 1200、新内容 800、输出 500 → 新内容费用 0.008 元 + 缓存费用 0.006 元 + 输出费用 0.005 元 = 0.019 元；相比无缓存（0.025 元）节省 24%。

#### 9.2 相关字段

| 字段 | 位置 | 说明 |
| :- | :- | :- |
| `usage.prompt_tokens_details.cached_tokens` | 非流式 / 流式响应、文档解析响应 | 命中的缓存 Token 数量（缓存为 0 表示未命中） |
| `usage.prompt_tokens` / `usage.completion_tokens` / `usage.total_tokens` | 非流式 / 流式响应 | 输入 / 输出 / 总 Token 数 |
| `usage.completion_tokens_details.reasoning_tokens` | 缓存页响应示例 | 思考 token 数（示例中为 0） |
| `prompt_cache_key` | Response API 请求 | 用于集群路由，以提高缓存命中率 |
| `clear_thinking: false` | 请求 `thinking` 对象 | 保留式思考可「提高缓存命中率」 |
| `max_tokens` | 请求 | 设置合适的 max_tokens 可以控制响应长度和成本 |
| `reasoning_effort` | 请求 | 深度思考的思考过程计入输出 Token 计费，选择合适思考强度可控制成本 |

Token 计量口径（官方）：

- 通常 `1` 个令牌约等于 `0.75` 个英文单词或 `1.5` 个中文字符。GLM 系列模型 token 和字数的换算比例约为 `1:1.6`。
- 请勿把 `max_tokens`（请求上限）与模型架构的最大输出 Tokens 混为一谈。
- 对于 `glm-4-voice` 模型，`1` 秒音频 = `12.5 Tokens`，向上取整。
- 可调用 `tokenizer` 分词器 API 预估文本 token 数量。

---

### 10. 特殊模式

#### 10.1 结构化输出（response_format）

| 字段 | 类型 | 默认 | 取值 | 说明 |
| :- | :- | :- | :- | :- |
| `response_format.type` | string | `text` | `text`（普通文本输出）、`json_object`（JSON 格式输出） | 指定模型的响应输出格式；`type` 取值收敛为三种（文档原文如此表述，但枚举只列出 `text` 与 `json_object` 两种）；仅文本模型支持此字段 |

用法要点（文档原文）：设置为 `{"type": "json_object"}` 启用 JSON 模式；需在系统消息中定义期望的 JSON 结构和字段要求；文档示例中结构化输出用的模型为 `glm-5.2`。文档另给出配合 `jsonschema.validate` 做 Schema 验证的完整示例。

Response API 侧等价字段：`text.format.type` = `text` 或 `json_object`。

#### 10.2 do_sample（贪心 vs 采样）

| 字段 | 类型 | 默认 | 说明 |
| :- | :- | :- | :- |
| `do_sample` | boolean | `true` | `true`：按每个 token 的概率分布随机采样，增加多样性和创造性；`false`：采用贪心策略，总是选择概率最高的下一个 token，输出确定性高，此时 `temperature` 和 `top_p` 参数将被忽略 |

文档补充：OpenAI 兼容页注明「`temperature` 参数的区间为 (0,1)，`do_sample = False`（`temperature = 0`）在 OpenAI 调用中并不适用」。

#### 10.3 其他模式与开关

| 模式/字段 | 说明 |
| :- | :- |
| `stop` | 停止词列表，最多 4 个；当模型生成文本中遇到指定字符串时立即停止生成 |
| `request_id` | 用户端传递的请求唯一标识，长度 6–64，建议 UUID；未提供平台自动生成 |
| `user_id` | 终端用户唯一标识符，长度 6–128；用于平台对终端用户的非法活动、生成非法不当信息或其他滥用行为进行干预 |
| `watermark_enabled` | 音频模型字段，控制 AI 生成图片时是否添加水印：`true`（默认启用显式水印及隐式数字水印，符合政策要求）、`false`（关闭所有水印，仅允许已签署免责声明的客户使用，签署路径：个人中心-安全管理-去水印管理） |
| 三种调用方式 | SSE 调用（推荐，流式实时获取，适用于首响及响应时长要求高的场景）、同步调用（一次性返回全量结果）、异步调用（需调用异步接口查询处理状态与推理结果，适用于对响应时间不敏感的场景，如批量处理） |
| 对话模型多轮 | 需将之前的对话记录作为 `messages` 参数传过来（文档给出完整示例） |
| 异步任务过期时间 | 「异步任务没有过期时间」 |
| 上下文/输入/输出限制关系 | 模型最大输入限制 = 模型上下文 − 最大输出 |

---

### 11. 错误码与限流

#### 11.1 业务错误码（对话补全等通用 API）

智谱的响应由外层 HTTP 状态码 + 响应体内业务错误码 `error.code` 组成。下表「建议动作」列，官方仅在速率限制页对 1302/1305 给出建议处理方式，其余未给出，故标注为「文档未给出」。

| 码 | HTTP | 含义 | 建议动作 |
| :- | :- | :- | :- |
| - | 500 | 内部错误 | 文档未给出 |
| 1000 | 401 | 身份验证失败 | 文档未给出（401 通用建议：检查 API Key 是否正确） |
| 1001 | 401 | Header 中未收到 Authentication 参数，无法进行身份验证 | 文档未给出（检查 API Key） |
| 1003 | 401 | Authentication Token 已过期，请重新生成/获取 | 重新生成/获取 Token |
| 1005 | 401 | 已开启二次认证保护，需要二次认证登录。 | 文档未给出 |
| 1113 | 429 | 您的账户已欠费，请充值后重试 | 充值后重试 |
| 1200 | 500 | API 调用失败 | 稍后重试（通用 500 建议：稍后重试，如持续出现请联系支持） |
| 1210 | 400 | API 调用参数有误，请检查文档 | 文档未给出 |
| 1211 | 400 | 模型不存在，请检查模型代码 | 检查模型代码 |
| 1212 | 400 | 当前模型不支持 `${method}` 调用方式 | 文档未给出 |
| 1213 | 400 | 未正常接收到 `${field}` 参数 | 文档未给出 |
| 1214 | 400 | `${field}` 参数非法。请检查文档 | 文档未给出 |
| 1215 | 400 | `${field1}` 与 `${field2}` 不能同时设置，请检查文档 | 文档未给出 |
| 1220 | 403 | 您无权访问 `${API_name}` | 文档未给出 |
| 1221 | 400 | API `${API_name}` 已下线 | 文档未给出 |
| 1222 | 400 | API `${API_name}` 不存在 | 文档未给出 |
| 1230 | 500 | API 调用流程出错 | 文档未给出 |
| 1234 | 500 | 网络错误，错误 id：`${error_id}`，请联系客服 | 联系客服 |
| 1261 | 400 | Prompt 超长 | 文档未给出 |
| 1301 | 400 | 系统检测到输入或生成内容可能包含不安全或敏感内容，请您避免输入易产生敏感内容的提示语，感谢您的配合 | 文档未给出 |
| 1302 | 429 | 您的账户已达到速率限制，请您控制请求频率 | 降低并发请求数量；增加请求队列或排队机制；必要时提升账户权益等级（此条不适用 GLM Coding Plan） |
| 1305 | 429 | 该模型当前访问量过大，请您稍后再试 | 稍后重试请求；增加重试间隔，避免立即高频重试；在业务允许的情况下进行降级或延迟处理 |
| 1308 | 429 | 已达到 `${number} ${unit}` 的使用上限。您的限额将在 `${next_flush_time}` 重置 | 等待重置（文档未给出具体动作） |
| 1309 | 429 | 您的 GLM Coding Plan 套餐已到期，暂无法使用，前往官方续订后即可恢复 https://bigmodel.cn/claude-code | 续订套餐 |
| 1310 | 429 | 您已达到每周/每月使用上限，您的限额将在 `${next_flush_time}` 重置 | 等待重置 |
| 1311 | 429 | 当前订阅套餐暂未开放 `${model_name}` 权限 | 文档未给出 |
| 1313 | 429 | 您的账户当前使用模式不符合公平使用策略，请求频率已受到限制。详情请参阅《条款与协议-订阅及自动续费协议》，如需恢复请前往个人中心-编程套餐总览-顶部申请解除限制 | 前往个人中心申请解除限制 |
| 1314 | 429 | 您的企业套餐已失效，请联系企业管理员。 | 联系企业管理员 |
| 1315 | 429 | 该 API Key 仅限企业编程套餐场景使用，请到官网更换对应产品类型的 API Key | 更换对应产品类型的 API Key |
| 1316 | 429 | 已达到 5 小时使用上限。主账号余额不足，无法使用超额按量付费。您的限额将在 `{next_flush_time}` 重置。 | 文档未给出 |
| 1317 | 429 | 已达到 7 天使用上限。主账号余额不足，无法使用超额按量付费。您的限额将在 `{next_flush_time}` 重置。 | 文档未给出 |
| 1318 | 429 | 已达到 5 小时使用上限，且已达子账号月消费上限，无法使用超额按量付费，请联系管理员调整。您的限额将在 `{next_flush_time}` 重置。 | 联系管理员调整 |
| 1319 | 429 | 已达到 7 天使用上限，且已达子账号月消费上限，无法使用超额按量付费，请联系管理员调整。您的限额将在 `{next_flush_time}` 重置。 | 联系管理员调整 |
| 1320 | 429 | 已达到 5 小时使用上限，且已达企业级月消费上限，无法使用超额按量付费，请联系管理员调整。您的限额将在 `{next_flush_time}` 重置。 | 联系管理员调整 |
| 1321 | 429 | 已达到 7 天使用上限，且已达企业级月消费上限，无法使用超额按量付费，请联系管理员调整。您的限额将在 `{next_flush_time}` 重置。 | 联系管理员调整 |

错误响应体形状：`{"error": {"code": "1001", "message": "Header 中未收到 Authentication 参数，无法进行身份验证"}}`（`code`、`message` 均为 string）。

官方特别说明：「使用流式（SSE）调用时，如果 API 在推理过程中异常终止，不会返回上述错误码，而是在响应体的 `finish_reason` 参数中返回异常原因。」

#### 11.2 网络搜索 API 错误码

| 码 | HTTP | 含义 |
| :- | :- | :- |
| 1701 | 文档未给出 | 网络搜索并发已达上限，请稍后重试或减少并发请求 |
| 1702 | 文档未给出 | 系统未找到可用的搜索引擎服务，请检查配置或联系管理员 |
| 1703 | 文档未给出 | 搜索引擎未返回有效数据，请调整查询条件 |

#### 11.3 Response API 错误码（字符串码，与对话补全的数字业务码不是同一套）

| code | 含义 |
| :- | :- |
| `invalid_request` | 请求参数或格式错误 |
| `model_not_found` | 模型不存在 |
| `not_implemented` | 接口或能力未实现 |
| `request_too_large` | 请求体超过大小限制 |
| `authentication_error` / `invalid_api_key` / `expired` | 认证失败 |
| `permission_denied` | 权限不足 |
| `context_length_exceeded` | 输入超过模型上下文窗口 |
| `rate_limit_exceeded` | 请求频率超限 |
| `insufficient_quota` / `quota_exceeded` / `usage_limit_reached` / `usage_not_included` | 额度或用量限制 |
| `server_error` | 服务端内部错误 |
| `server_is_overloaded` / `overloaded` / `slow_down` | 服务过载或要求降速 |
| `content_filter` / `cyber_policy` | 内容或安全策略拦截 |

#### 11.4 速率限制机制

- 速率限制体现在：**并发请求数限制**；不同模型设有独立的并发限制；不同用户权益等级、不同套餐对应不同的并发限制；高峰期的动态限流与平台级保护策略。并发数指的是「同一时刻正在处理中的请求数量」。
- GLM Coding Plan 套餐：默认并发上限与套餐等级相关，推荐同时进行项目数——**Lite：建议同时进行单个项目的开发；Pro：建议同时进行 1-2 个项目的开发；Max：建议同时进行 2+ 个项目的开发**。低峰期享有更高的并发权益（动态提升）。套餐用户按订阅套餐等级统一并发，暂不支持申请调整。
- 平台级服务过载：某一模型短时间内整体访问量激增、底层算力资源高负载、平台维护/扩容/异常恢复时，触发平台级保护机制。
- 通用 API 用户可提交「速率限制调整申请」，需填写需要调整的模型、期望增加的并发数量、实际使用场景与业务说明；平台将在 **10 个工作日内完成审核**。
- 建议的应对策略：使用请求队列或并发池；避免瞬时「洪峰式」请求；避免固定间隔的高频重试；非实时场景使用批处理 API 或异步请求降低并发压力。

#### 11.5 用户权益等级（并发额度依据）

| 等级 | 积分范围 | 主要权益 |
| :- | :- | :- |
| V0 等级 | [0, 2,000) | 基础服务 |
| V1 等级 | [2,000, 10,000) | 并发权益 |
| V2 等级 | [10,000, 50,000) | 更高并发 |
| V3 等级 | >= 50,000 | 最高并发 |

积分与花费按 1:1 比例兑换；赠金账户余额消耗不换算积分；平台于 T+1 日 06:00:00 更新积分，并根据用户最近三个月的最高积分确定本月的用户权益等级。

#### 11.6 Batch API 相关限制与错误处理

- Batch 接口状态：`validating`（文件正在验证中，Batch 任务未开始）、`failed`（文件未通过验证）、`in_progress`（文件已成功验证，Batch 任务正在进行中）、`finalizing`（Batch 任务已完成，结果正在准备中）、`completed`（Batch 任务已完成，结果已准备好）、`expired`（Batch 任务未能完成）、`cancelling`（Batch 任务正在取消中）、`cancelled`（Batch 任务已取消）。
- 文件限制：单个文件最多支持 50,000 个请求；文件大小不超过 100MB；每个 batch 文件只能包含对单个模型的请求；每个请求必须包含唯一的 `custom_id`；向量模型（Embedding-2、Embedding-3）Batch 文件请求数量限制为不超过 10000 次；上传 Batch 文件时每次最多上传 1000 个。
- 结果文件：`output_file_id`（成功执行请求的输出文件 ID）、`error_file_id`（出现错误请求的输出文件 ID）。系统只保留数据 30 天。
- 任务调度：预计任务在 24 小时内完成，如果任务超过 7 天未处理完，将自动取消。

#### 11.7 常见错误处理问答（HTTP API 页）

标准 HTTP 状态码的通用含义见《通用基线》§0.8；本家 HTTP API 页对 401/429/500 另附处理建议：401 未授权→检查 API Key 是否正确；429 请求过于频繁→降低请求频率，实施重试机制；500 服务器内部错误→稍后重试，如持续出现请联系支持。

---

### 12. 模型清单与限制

#### 12.1 推荐模型（模型概览页）

| 模型 | 特点 | 上下文 | 最大输出 |
| :- | :- | :- | :- |
| GLM-5.3 | 编程与智能体能力比肩 Claude Fable 5；长程任务与复杂环境中表现更佳 | 1M | 128K |
| GLM-5.3-Flash | 普惠的全球前沿多模态模型；能够原生理解图片视频，生成可交互代码等专业任务 | 1M | 128K |
| GLM-5.3-FlashX | 更快更流畅的 GLM-5.3-Flash；推理速度达 200 tokens/s | 1M | 128K |
| GLM-5.2 | 支撑复杂长程任务稳定执行；Coding 能力大幅提升，从代码生成走向工程交付 | 1M | 128K |
| GLM-Image | 旗舰图像生成模型；复杂指令遵循与知识密集生成更强；文字渲染表现突出，支持多分辨率 | （文档留空） | （文档留空） |
| GLM-OCR | 轻量图文解析模型；兼顾高精度、高效率文档理解；支持常见复杂版式解析 | 输入：单图 ≤ 10 MB，PDF ≤ 50 MB；最大支持 100 页 | （文档留空） |
| GLM-ASR-2512 | 高精度语音识别模型；字符错误率低，支持自定义词汇；覆盖多种主流语言与方言场景，多模态支持音频 | （文档留空） | （文档留空） |
| GLM-TTS | 语音合成模型；支持超拟人语音生成与情感表达；提供非流式与流式接口，多模态支持文本 | （文档留空） | （文档留空） |
| CogVideoX-3 | 旗舰视频模型；指令遵循与物理模拟更强；现实与 3D 场景表现提升，支持首尾帧生成 | （文档留空） | （文档留空） |
| Embedding-3 | 第三代文本向量化模型（V3）；适用于语义检索、聚类、主题建模与分类等场景 | 8K | - |

#### 12.2 全部模型（模型概览页折叠区）

文本模型：

| 模型 | 特点 | 上下文 | 最大输出 |
| :- | :- | :- | :- |
| GLM-5.1 | Coding 能力对齐 Claude Opus 4.6；长程任务显著提升，可自主工作长达 8 小时 | 200K | 128K |
| GLM-5 | 编程能力对齐 Claude Opus 4.5；擅长 Agentic 长程规划与执行 | 200K | 128K |
| GLM-5-Turbo | 龙虾任务核心需求专项优化；复杂长任务执行连续性好 | 200K | 128K |
| GLM-4.7 | 通用对话、推理与智能体能力上升级 | 200K | 128K |
| GLM-4.7-FlashX | 轻量高速，小尺寸强能力 | 200K | 128K |
| GLM-4.6 | 擅长高级编码、复杂推理与工具调用 | 200K | 128K |
| GLM-4.5-Air | 轻量模型；推理、编码与智能体任务表现稳定 | 128K | 96K |
| GLM-4.5-AirX | 极速版本；适合低延迟、高响应要求的业务场景 | 128K | 96K |
| GLM-4-Long | 能够理解和回应复杂的查询；为处理超长文本和记忆型任务设计 | 1M | 4K |
| GLM-4-FlashX-250414 | Flash 增强高速版本；推理速度快，适合高并发调用场景 | 128K | 16K |
| GLM-4.7-Flash | 免费文本模型；延续 GLM-4.7 基座的通用能力 | 200K | 128K |
| GLM-4.5-Flash | 免费文本模型；支持最长 128K 的上下文处理 | 128K | 96K |
| GLM-4-Flash-250414 | 免费文本模型 | 128K | 16K |

视觉理解模型：

| 模型 | 特点 | 上下文 | 最大输出 |
| :- | :- | :- | :- |
| GLM-5V-Turbo | 多模态 Coding 基座；兼顾视觉理解、推理与代码生成；适配 Agent 工作流与长上下文任务 | 200K | 128K |
| GLM-4.6V | 原生支持工具调用；前端代码复刻效果更稳定 | 128K | 32K |
| AutoGLM-Phone | 手机智能助理框架；支持自然语言完成 App 操作任务；覆盖完整移动端操作指令集 | 20K | 2048 |
| GLM-4.1V-Thinking-FlashX | 擅长复杂场景理解与多步骤分析；适合高并发视觉推理场景 | 64K | 16K |
| GLM-4.6V-Flash | 免费模型，支持视觉推理 | 128K | 32K |
| GLM-4.1V-Thinking-Flash | 免费模型，支持视觉推理 | 64K | 16K |
| GLM-4V-Flash | 免费模型，支持图像理解 | 16K | 1K |

图像生成模型：

| 模型 | 特点 | 多分辨率 |
| :- | :- | :- |
| CogView-4 | 通用图像生成模型，生成质量高；画面细节更完整，适合多类创意场景 | 支持 |
| CogView-3-Flash | 免费模型；适合轻量图像创作 | 支持 |

视频生成模型：

| 模型 | 特点 | 多分辨率 |
| :- | :- | :- |
| Vidu Q1 | 高质量视频生成模型 | 图像、文本、首尾帧 |
| Vidu 2 | 高速低价视频模型 | 图像、参考、首尾帧 |
| CogVideoX-Flash | 免费视频生成模型 | 图像、文本 |

音视频模型：

| 模型 | 特点 | 多模态支持 |
| :- | :- | :- |
| GLM-TTS-Clone | 音色克隆模型；3 秒音频即可快速生成相似音色 | 文本、音频 |
| GLM-Realtime | 实时音视频模型 | 视频、音频、文本 |
| GLM-4-Voice | 实时语音对话模型 | 文本、音频 |

向量模型与其他：

| 模型 | 特点 | 上下文 | 最大输出 |
| :- | :- | :- | :- |
| Embedding-2 | 第二代文本向量化模型（V2） | 8K | - |
| CodeGeeX-4 | 代码补全模型 | 128K | 32K |
| Rerank | 文本重排序模型 | 4K | - |

#### 12.3 各模型页给出的能力/限制差异

| 模型 | 输入模态 | 上下文 | 最大输出 | 思考能力要点 |
| :- | :- | :- | :- | :- |
| GLM-5.3 | 仅文本 | 1M | 128K | `thinking.type` 仅 `enabled`（不支持禁用思考，传 `disabled` 请求会失败）；`reasoning_effort` 支持 `low`/`high`/`max`，默认 `max` |
| GLM-5.3-Flash / FlashX | 视频、图像、文本、文件 | 1M | 128K | `thinking.type` 仅支持 `enabled`，不支持关闭思考；建议设置 `thinking.clear_thinking: false`；推荐 `temperature: 1`、`top_p: 0.95`、`reasoning_effort: max`；流式调用建议同时开启 `stream: true` 和 `tool_stream: true` |
| GLM-5.2 | 文本 | 1M | 128K | 支持 reasoning_effort 全档位映射（见第 6 节） |
| GLM-4.7 / GLM-4.7-FlashX | 文本 | 200K | 128K | 强制思考；新增轮级思考；强化交错式思考与保留式思考 |
| GLM-4.6 | 文本 | 200K | 128K | 默认「混合 thinking（自动开启）」 |
| GLM-4.5 / Air / X / AirX / Flash | 文本 | 128K | 96K | 「混合推理模式」，`thinking.type` 支持 `enabled` 和 `disabled`，默认开启动态思考 |
| GLM-OCR | PDF、图片（JPG、PNG）；单图 ≤ 10 MB，PDF ≤ 50 MB；最大支持 100 页 | — | — | 输出：文本、图片链接、md 文档；支持语言：中文、英文、法语、西班牙语、俄罗斯语、德语、日语、韩语等 |
| GLM-Image | 文本（最大输入 1000 字符） | — | — | 输出：图像；多分辨率支持 1:1、3:4、4:3、16:9 等 |

GLM-4.5 系列补充（模型页原文）：GLM-4.5、GLM-4.5-X 模型即将下线，建议选择 GLM-4.7。GLM-4.5 总参数 3550 亿 / 激活 320 亿；GLM-4.5-Air 总参数 1060 亿 / 激活 120 亿。

图片数量限制（视觉模型）：GLM-5.3-Flash 系列、GLM-5V-Turbo、GLM-4.6V、GLM-4.5V 系列限制 50 张；GLM-4V-Plus-0111 限制 5 张；GLM-4V-Flash 限制 1 张且不支持 Base64。

#### 12.4 模型调用示例（GLM-5.3 页给出的三种协议 Base URL）

| 协议类型 | Base URL |
| :- | :- |
| OpenAI Chat Completion 协议 | `https://open.bigmodel.cn/api/paas/v4` |
| OpenAI Response 协议 | `https://open.bigmodel.cn/api/v1` |
| Anthropic Message 协议 | `https://open.bigmodel.cn/api/anthropic` |

---

### 13. 来源页清单

本家页面清单（含每条 URL 的「这页讲了什么」一句话）统一见《附录：各家页面清单》的本家小节。

未能抓到的内容（一句话说明）：

| 缺口 | 说明 |
| :- | :- |
| Anthropic(Claude) 兼容端点的字段级差异清单 | 文档中不存在该页面，只有 base_url / path / header / 示例（见第 4 节备注） |
| 各模型页之外的独立「限制」汇总页 | 文档中没有单独的模型限制页；限制分散在模型概览、核心参数与各模型页中 |
| 网络搜索 API 错误码的 HTTP 状态码 | 网络搜索 API 参考页只给出 1701/1702/1703 的业务含义，未给 HTTP 码 |
| OpenAI 兼容页声明的 base_url 尾斜杠写法差异 | 文档在不同页面分别写作 `https://open.bigmodel.cn/api/paas/v4/` 与不带尾斜杠，官方未做统一说明 |
| 页面 `https://docs.bigmodel.cn/cn/guide/models/text/glm-4.md`、`glm-4-long.md`、`glm-4.7-flash`、`glm-4v-*` 等子模型详情页 | 本次未逐页抓取（任务指定范围外），其上下文/输出数据已从模型概览与核心参数页取得 |

---

## Kimi / Moonshot

### 1. 端点与鉴权

服务地址：`https://api.moonshot.cn`。国际站使用 `https://api.moonshot.ai/v1`（两站账户与 API Key 完全独立，混用返回 401）。

| 兼容协议 | base_url | 接口 | 可用 SDK / 工具 |
| - | - | - | - |
| OpenAI Chat Completions | `https://api.moonshot.cn/v1` | `/chat/completions` | OpenAI 官方 SDK（Python / Node.js） |
| OpenAI Responses | `https://api.moonshot.cn/v1` | `/responses` | OpenAI 官方 SDK（Python / Node.js） |
| Anthropic Messages | `https://api.moonshot.cn/anthropic` | `/messages`（完整路径 `/anthropic/v1/messages`） | Anthropic 官方 SDK |

| 协议 | method + path | 必需请求头 | 可选请求头 |
| - | - | - | - |
| Chat Completions | `POST /v1/chat/completions` | `Authorization: Bearer $MOONSHOT_API_KEY`、`Content-Type: application/json` | `X-Msh-Request-Nonce`（string，minLength 1，建议 UUID v4） |
| Responses | `POST /v1/responses` | 同上 | `X-Msh-Request-Nonce` |
| Messages | `POST /anthropic/v1/messages` | 同上 | `X-Msh-Request-Nonce` |

- 携带合法的 `X-Msh-Request-Nonce` 时，响应头返回 `Msh-Request-Timestamp`（int64，Kimi API 接受请求的 Unix 毫秒时间戳）与 `Msh-Request-Signature`（以 `reqsigv1_` 为前缀的签名 token，基于 nonce、timestamp 和请求中的 `model` 签发）。只允许一个非空的 Header 值；值不合法时请求照常执行，但不返回上述响应头。流式与非流式均支持。
- 校验签名：`POST /v1/signatures/verify`，请求体 `nonce` / `timestamp`（int64，最小 1）/ `model` / `signature` 四者全部必填，全一致返回 `{"valid": true}`，否则 `{"valid": false}`；响应头 `Cache-Control: no-store`（固定）。签名只证明 Kimi API 在该时间点接受了这个 nonce 和请求模型；服务端不记录 nonce，重放同一组参数仍返回 `valid: true`。

端点一览（含非对话接口）：

| 端点 | 方法 | 协议 | 说明 |
| - | - | - | - |
| `/v1/chat/completions` | POST | OpenAI | 创建对话补全 |
| `/v1/responses` | POST | OpenAI | Responses API |
| `/anthropic/v1/messages` | POST | Anthropic | Messages API |
| `/v1/models` | GET | OpenAI | 列出模型 |
| `/v1/tokenizers/estimate-token-count` | POST | OpenAI | 计算 Token |
| `/v1/users/me/balance` | GET | OpenAI | 查询余额 |
| `/v1/signatures/verify` | POST | Kimi | 校验请求签名 |
| `/v1/tools/search` | POST | Kimi | 联网搜索 Basic |
| `/v1/tools/search_pro` | POST | Kimi | 联网搜索 Pro |
| `/v1/tools/fetch` | POST | Kimi | 网页抓取 |
| `/v1/files` | POST | OpenAI | 上传文件 |
| `/v1/files` | GET | OpenAI | 列出文件 |
| `/v1/files/{file_id}` | GET | OpenAI | 获取文件信息 |
| `/v1/files/{file_id}` | DELETE | OpenAI | 删除文件 |
| `/v1/files/{file_id}/content` | GET | OpenAI | 获取文件内容 |
| `/v1/batches` | POST | OpenAI | 创建批处理任务 |
| `/v1/batches` | GET | OpenAI | 列出批处理任务 |
| `/v1/batches/{batch_id}` | GET | OpenAI | 获取批处理任务详情 |
| `/v1/batches/{batch_id}/cancel` | POST | OpenAI | 取消批处理任务 |
| `/v1/formulas/{uri}/tools` | GET | Kimi | 获取官方工具（Formula）声明 |
| `/v1/formulas/{uri}/fibers` | POST | Kimi | 执行官方工具（Formula） |

### 2. Chat Completions 请求字段总表

下表只列本家与《通用基线》§0.2 不同的字段与取值；未列出的标准字段语义同基线（本家文档未写明其默认值/范围即视为未列出）。

| 字段 | 类型 | 必填/默认 | 取值 | 说明 |
| - | - | - | - | - |
| `model` | string | 必填，默认 `kimi-k3` | `kimi-k3` / `kimi-k2.7-code` / `kimi-k2.7-code-highspeed` / `kimi-k2.6` | 模型 ID |
| `messages` | array | 必填 | 标准消息 · object \| 动态工具消息 · object | Kimi K3 除标准消息外，还可在任意对话位置插入 `{"role":"system","tools":[...]}` 动态加载工具；该消息不含 `content`，只影响后续对话 |
| `messages[].content` | string \| array | 必填（动态工具消息除外） | string，或 `text` / `image_url` / `video_url` 对象数组 | 多模态输入用数组；不得为空 |
| `messages[].name` | string | 可选，默认 `null` | — | 消息发送者名称；Partial Mode 中用作角色前缀 |
| `messages[].partial` | boolean | 可选，默认 `false` | `true` \| `false` | 在最后一条 assistant 消息中设为 `true` 启用 Partial Mode |
| `messages[].reasoning_content` | string | 可选 | — | 回传历史轮次的思考内容（Preserved Thinking 要求） |
| `logprobs` | boolean | 可选，默认 `false` | `true` \| `false` | 为 `true` 时在响应 message 的 `logprobs` 字段返回每个输出 Token 的对数概率 |
| `top_logprobs` | integer | 可选，范围 `0 <= x <= 20` | 0–20 | 每个 Token 位置返回概率最高的候选 Token 数；使用时必须把 `logprobs` 设为 `true` |
| `prediction` | object | 可选 | `type: content` | Predicted Output 配置，可显著降低响应延迟 |
| `prediction.type` | string | — | `content` | 预测内容类型，当前仅支持 `content` |
| `prediction.content` | string \| array[object] | — | — | 需模型匹配的静态预测内容；数组元素仅支持 `type` 为 `text` 的文本内容 |
| `max_tokens` | integer | 已弃用 | — | 已弃用，请使用 `max_completion_tokens` |
| `max_completion_tokens` | integer | 可选 | K3 默认 `131072`，最大 `1048576` | 生成的最大 Token 数（非输入+输出总长）；达到上限未结束时 `finish_reason="length"`；输入 + 该值超出上下文窗口返回 `invalid_request_error` |
| `response_format` | object | 可选，默认 `{"type":"text"}` | `text` / `json_object` / `json_schema` | 输出格式控制 |
| `response_format.json_schema` | object | `type=json_schema` 时必填 | 需含 `name`、`schema` | 结构化输出定义 |
| `response_format.json_schema.name` | string | 必填 | — | Schema 名称 |
| `response_format.json_schema.strict` | boolean | 可选，默认 `true` | `true` \| `false` | 为 `true` 时 schema 需符合 MFJS 规范；为 `false` 仅保证输出为合法 JSON 对象 |
| `response_format.json_schema.schema` | object | 必填 | JSON Schema | 需符合 MFJS（Moonshot Flavored JSON Schema）规范，可用 `walle` CLI 自检 |
| `stop` | string \| array[string] | 可选，默认 `null` | 数组最多 5 个，每个不超过 32 字节 | 停用词，完全匹配时停止输出，匹配到的词本身不输出 |
| `stream` | boolean | 可选，默认 `false` | `true` \| `false` | — |
| `stream_options` | object | 可选 | — | 流式响应选项 |
| `stream_options.include_usage` | boolean | 可选，默认 `false` | `true` \| `false` | 为 `true` 时在 `data: [DONE]` 之前额外发送一个 chunk（`choices` 为空数组），其 `usage` 为整次请求统计；其他 chunk 的 `usage` 为 `null`；流中断时可能收不到该 chunk |
| `tools` | array[object] | 可选 | `ToolDefinition` | 模型可调用的工具列表（顶层全局声明） |
| `tools[].type` | string | 必填 | `function` / `builtin_function` | `builtin_function` 用于内置工具（如 `$web_search`） |
| `tools[].function.name` | string | 必填 | 正则 `^[a-zA-Z_][a-zA-Z0-9-_]{0,127}$` | 一次请求内必须唯一，重名返回 400 |
| `tools[].function.description` | string | 可选 | — | 功能描述 |
| `tools[].function.parameters` | object | 必填（`type=function`） | JSON Schema | 需符合 MFJS 规范，顶层 `type` 必须为 `object` |
| `tools[].function.strict` | boolean | 可选，默认 `true` | `true` \| `false` | 是否严格按 parameters schema 约束工具入参 |
| `prompt_cache_key` | string | 可选，默认 `null` | — | 缓存相似请求以优化命中率；Coding Agent 通常是 session id / task id；Kimi Code Plan 必填 |
| `prompt_cache_options` | object | 可选，不传时默认开启缓存写入（`5m` 档） | — | 上下文缓存写入选项 |
| `prompt_cache_options.mode` | string | 可选，默认 `implicit` | 仅 `implicit` | 自动将请求前缀写入缓存 |
| `prompt_cache_options.ttl` | string | 可选，默认 `5m` | `5m` \| `1h` | 缓存写入有效期，两档互相独立 |
| `safety_identifier` | string | 可选 | — | 检测违反使用政策用户的稳定标识符，建议传哈希值 |
| `tool_choice` | string \| object | 可选，默认 `auto` | `auto` / `none` / `required`；或 `{"type":"function","function":{"name":"..."}}` | 控制是否调用工具；`required` 与指定函数对象仅 K3 支持 |
| `reasoning_effort` | string | 可选，默认 `max` | `low` / `high` / `max` | 顶层推理强度，仅 `kimi-k3` |
| `thinking` | object | 可选（仅 K2.x） | — | K2.x 专属思考开关 |
| `thinking.type` | string | 必填（传 `thinking` 时） | `enabled` / `disabled` | `kimi-k2.7-code` 仅接受 `enabled`，传 `disabled` 报错 |
| `thinking.keep` | string \| null | 可选，默认见模型 | `null` / `all` | 是否保留历史轮次 `reasoning_content`（Preserved Thinking） |
| `temperature` | float | 不可修改 | K3 / k2.7-code 固定 `1.0`；k2.6 思考 `1.0`、非思考 `0.6` | 传入其他值报错，建议不要显式传入 |
| `top_p` | float | 不可修改 | 固定 `0.95` | 传入其他值报错 |
| `n` | integer | 不可修改 | 固定 `1` | 传入大于 1 返回 400（`invalid n: only 1 is allowed for this model`） |
| `presence_penalty` | float | 不可修改 | 固定 `0` | 传入其他值报错 |
| `frequency_penalty` | float | 不可修改 | 固定 `0` | 传入其他值报错 |

- `messages` 中的 `content` 数组元素：`{"type":"text","text":...}`、`{"type":"image_url","image_url":{"url":...}}`、`{"type":"video_url","video_url":{"url":...}}`；`image_url` / `video_url` 也支持直接传字符串（等价于对象里的 `url`）。
- `temperature` / `top_p` / `n` / `presence_penalty` / `frequency_penalty` 在 chat.md 的 OpenAPI schema 中未声明，是 `models-overview` 明确写出的「固定值」；`content` 中出现 `prompt_cache_breakpoint` 时请求会被拒绝（HTTP 400）。

### 3. Responses API 字段差异

`POST /v1/responses`（base_url `https://api.moonshot.cn/v1`）。本接口当前仅支持 `kimi-k3`。

下表只列本家与《通用基线》§0.6 不同的字段与取值；未列出的标准字段语义同基线。

| 字段 | 类型 | 必填/默认 | 取值 | 说明 |
| - | - | - | - | - |
| `model` | string | 必填 | 仅 `kimi-k3` | 模型 ID |
| `input` | string \| array | 必填 | 字符串等价于一条 user 消息；数组元素按 `type` 区分 | 带类型的 item，可含历史对话、工具调用与工具结果 |
| `stream` | boolean | 可选，默认 `false` | — | 为 `true` 时以 SSE 事件流返回 |
| `max_output_tokens` | integer | 可选 | `kimi-k3` 默认 `131072`，最大 `1048576` | 达到上限时 `status=incomplete`，`incomplete_details.reason=max_output_tokens` |
| `reasoning.effort` | string | 可选，默认 `max` | `low` / `high` / `max` | 推理强度（注意与 Chat 的顶层 `reasoning_effort` 不同，这里是嵌套字段） |
| `text.format` | object | 可选 | `type` 仅 `json_schema` | 用 JSON Schema 约束输出结构 |
| `text.format.name` | string | 可选 | 缺省为 `output` | Schema 名称 |
| `text.format.schema` | object | 必填（`text.format` 时） | JSON Schema | 描述输出结构 |
| `text.format.strict` | boolean | 可选 | — | 是否严格按 Schema 约束输出 |
| `tools` | array | 可选 | `function` / `custom`（仅 `apply_patch`）/ `namespace` / `web_search` | 其他工具类型不支持；前三种由调用方本地执行，`web_search` 由服务端执行；每个请求最多包含一个 `web_search` |
| `tool_choice` | string | 可选 | 仅 `auto` | 取 `auto` 时由模型自行决定是否调用工具（Responses 侧不支持 `none` / `required`） |
| `include` | array[string] | 可选 | `web_search_call.results` \| `web_search_call.action.sources` | 仅在使用 `web_search` 时有效；前者返回图片搜索结果，后者返回搜索命中的网页来源 |
| `prompt_cache_key` | string | 可选 | — | 同会话使用相同取值提升缓存命中率 |
| `prompt_cache_options` | object | 可选，不传时默认开启 `5m` 档写入 | `mode: implicit`、`ttl: 5m \| 1h` | 取值语义与 Chat 一致 |
| `safety_identifier` | string | 可选 | — | 同 Chat |

与 Chat 的主要差异：

- 无 `messages`，改用 `input` + `instructions`；input item 类型含 `message`（省略 `type` 时按 message 处理）、`reasoning`、`function_call`、`function_call_output`、`custom_tool_call`、`custom_tool_call_output`、`web_search_call`、`additional_tools`。
- `message` 的 `role` 为 `user` / `assistant` / `developer`（`developer` 按系统指令处理）。
- 输入内容分片 `ResponsesInputContentPart` 只有 `input_text`、`input_image`、`output_text` 三种 `type`；`input_image.image_url` 只接受 data URL（如 `data:image/png;base64,<base64>`），**不支持公网 http(s) URL**；另有 `detail` 枚举 `auto` / `low` / `high` / `original`。
- 联网搜索：`{"type":"web_search"}` 由服务端执行，`output` 最前面会多出一个 `web_search_call` item。`search_context_size`、`blocked_domains`、`filters.blocked_domains` 不支持（传入返回 `invalid_request_error`）；`user_location`、`external_web_access`、`indexed_web_access` 会被忽略。`filters.allowed_domains` 最多 100 个；`search_content_types` 枚举 `text` / `image`（默认仅 `text`）；`image_settings.max_results` 范围 1–10，默认 3，`image_settings.caption` 默认 `false`。
- 自定义工具仅支持名为 `apply_patch` 的 `custom` 工具，必须提供 `format`（`type: grammar`、`syntax: lark`、`definition`）。
- 命名空间工具 `namespace`：把一组 function / custom 工具收纳到同一命名空间，含 `name`、`description`、`tools`。
- 响应中固定值：`store` 固定 `false`，`background` 固定 `false`，`previous_response_id` 固定 `null`，`conversation` 固定 `null`，`encrypted_content` 固定 `null`。
- 响应顶层会回显服务端实际应用的 `prompt_cache_options`（mode / ttl），以返回值为准；Chat Completions 不回显。

### 4. Anthropic(Messages) 协议字段差异

`POST /anthropic/v1/messages`（base_url `https://api.moonshot.cn/anthropic`）。

下表只列本家与《通用基线》§0.7 不同的字段与取值；未列出的标准字段语义同基线。

| 字段 | 类型 | 必填/默认 | 取值 | 说明 |
| - | - | - | - | - |
| `model` | string | 必填，默认 `kimi-k3` | 接口定义中 enum 仅 `kimi-k3` | 模型 ID |
| `messages` | array | 必填 | `MessagesMessageParam` | 最后一条为 assistant 消息时从该消息内容之后继续生成（Partial Mode） |
| `messages[].role` | string | 必填 | `user` / `assistant` | 系统提示请用顶层 `system` 字段 |
| `messages[].content` | string \| array | 必填 | 块类型 `text` / `image` / `thinking` / `tool_use` / `tool_result` | — |
| `max_tokens` | integer | 必填，最小 1 | — | 达到上限未结束时 `stop_reason="max_tokens"` |
| `system` | string \| array | 可选 | 字符串或 text 块数组 | 系统提示 |
| `stream` | boolean | 可选，默认 `false` | — | 以 SSE 流式返回 |
| `stop_sequences` | array[string] | 可选，maxItems 5 | 每个不超过 32 字节 | 停用词，匹配到的词本身不输出 |
| `tools` | array | 可选 | `MessagesTool` | 工具列表 |
| `tools[].type` | string | 可选 | `custom` | 工具类型，可省略 |
| `tools[].name` | string | 必填 | 正则 `^[a-zA-Z_][a-zA-Z0-9-_]{0,127}$` | 工具名称 |
| `tools[].description` | string | 可选 | — | 工具功能描述 |
| `tools[].input_schema` | object | 必填 | JSON Schema | 顶层 `type` 必须为 `object`；需符合 MFJS 规范 |
| `tool_choice` | object | 可选，默认 `{"type":"auto"}` | `type`: `auto` / `any` / `none` | `auto` 模型自行决定；`any` 强制调用任意工具；`none` 不调用工具（注意与 OpenAI 侧 `required` 不同） |
| `metadata.user_id` | string | 可选 | — | 标识终端用户或会话的稳定 ID，提高缓存命中率与滥用检测；Coding Agent 建议传 session id 并保持不变 |
| `cache_control` | object | 可选 | `type: ephemeral`（必填）、`ttl: 5m \| 1h`（默认 `5m`） | 上下文缓存写入选项，**仅顶层传入时生效**，`messages` 消息体内的 `cache_control` 标记会被忽略；不传时本次请求只尝试读取缓存（`5m` 档）不写入 |
| `output_config.effort` | string | 可选，默认 `max` | `low` / `high` / `max` | 推理强度；切换档位会破坏前缀缓存命中，建议会话开始前确定 |
| `output_config.format.type` | string | 必填（用 `output_config.format` 时） | `json_schema` | 结构化输出 |
| `output_config.format.schema` | object | 必填 | JSON Schema | 输出需遵循的 JSON Schema |

- `image` 块：`source.type` 为 `base64` 时需提供 `media_type`（枚举 `image/jpeg` / `image/png` / `image/gif` / `image/webp`）与 `data`；`source.type` 为 `url` 时通过 `url` 传 `ms://<file_id>`。
- `thinking` 块：字段为 `type: thinking`、`thinking`（推理内容）、`signature`（推理签名，回传时需原样保留）。
- `tool_result` 块：字段为 `type: tool_result`、`tool_use_id`（对应 `tool_use` 块的 `id`）、`content`（字符串或 text / image 块数组）。
- `usage` 口径与 Chat / Responses 不同：`usage.input_tokens` 是「既未命中缓存、也未用于创建缓存条目」的输入 Token；总输入 = `input_tokens + cache_read_input_tokens + cache_creation_input_tokens`。

### 5. 响应字段与流式结构

**Chat Completions 非流式响应**

其余字段（`id`、`created`、`model`、`choices[].index`、`choices[].message.role` / `choices[].message.content` / `choices[].message.tool_calls[]`、`choices[].finish_reason` 取 `stop` / `length` / `tool_calls`、`usage.completion_tokens` / `usage.total_tokens`）同《通用基线》§0.5；本家差异如下：

| 字段 | 类型 | 说明 |
| - | - | - |
| `object` | string | 固定 `chat.completion` |
| `choices[].message.reasoning_content` | string \| null | 推理过程，仅在思考模式启用时返回 |
| `usage.prompt_tokens` | integer | 总输入 Token 数（始终为总输入） |
| `usage.cached_tokens` | integer | 命中缓存的 Token 数 |
| `usage.prompt_tokens_details.cached_tokens` | integer | 命中缓存的 Token 数（缓存读取） |
| `usage.prompt_tokens_details.cache_write_tokens` | integer | 本次写入缓存的 Token 数（缓存写入） |

**Chat Completions 流式结构（SSE）**

```
data: {"id":"cmpl-xxx","object":"chat.completion.chunk","created":1698999575,"model":"kimi-k2.6","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}

data: {"id":"cmpl-xxx","object":"chat.completion.chunk","created":1698999575,"model":"kimi-k2.6","choices":[{"index":0,"delta":{"content":"你好"},"finish_reason":null}]}

...

data: {"id":"cmpl-xxx","object":"chat.completion.chunk","created":1698999575,"model":"kimi-k2.6","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":19,"completion_tokens":13,"total_tokens":32,"cached_tokens":12,"prompt_tokens_details":{"cached_tokens":12,"cache_write_tokens":0}}}

data: [DONE]
```

- chunk 的 `object` 为 `chat.completion.chunk`；chunk 组成、`delta` 的三类增量、`tool_calls` 分片拼接规则、首个/末个块的 `role` 与 `finish_reason` 位置同《通用基线》§0.5。本家增量中 `reasoning_content` 先于 `content` 和 `tool_calls` 下发。
- **必须用 `data: [DONE]` 判断传输完成**：即使已收到 `finish_reason=stop`，未收到 `data: [DONE]` 前都应视作消息不完整。
- 每个数据块以 `data: ` 前缀开头，紧跟一个合法 JSON 对象，以两个换行符 `\n\n` 结束；响应 `Content-Type` 为 `text/event-stream`。
- `stream_options.include_usage=true` 时，`[DONE]` 前返回一个 `choices` 为空数组、顶层 `usage` 为整次请求用量的统计数据块。解析时不要假设每个 chunk 都存在 `choices[0]`。

**Responses 流式结构**

SSE 帧形如 `event: <type>` 加 `data: <json>`，事件体字段随 `type` 变化，`sequence_number`（integer，从 0 开始单调递增）必填。`type` 枚举：`response.created`、`response.in_progress`、`response.output_item.added`、`response.output_item.done`、`response.content_part.added`、`response.content_part.done`、`response.output_text.delta`、`response.output_text.done`、`response.reasoning_summary_part.added`、`response.reasoning_summary_part.done`、`response.reasoning_summary_text.delta`、`response.reasoning_summary_text.done`、`response.function_call_arguments.delta`、`response.function_call_arguments.done`、`response.custom_tool_call_input.delta`、`response.custom_tool_call_input.done`、`response.web_search_call.in_progress`、`response.web_search_call.searching`、`response.web_search_call.completed`、`response.completed`、`response.incomplete`、`response.failed`、`error`。

Responses 响应字段要点：`id`、`object`（`response`）、`created_at`、`completed_at`（`null` 或 integer）、`status`（`in_progress` / `completed` / `incomplete` / `failed`）、`model`、`output`（顺序为 web_search_call（如有）、reasoning、message、工具调用）、`usage`、`incomplete_details.reason`（`max_output_tokens` / `content_filter`）、`error.code` / `error.message`、`instructions`、`reasoning`、`text`、`tools`、`tool_choice`、`max_output_tokens`、`temperature`、`top_p`、`metadata`、`parallel_tool_calls`、`service_tier`、`store`、`background`、`previous_response_id`、`conversation`、`prompt_cache_options`。
Responses 的 `usage`：`input_tokens`（含命中缓存部分）、`input_tokens_details.cached_tokens`、`input_tokens_details.cache_write_tokens`、`output_tokens`（含推理 Token）、`output_tokens_details.reasoning_tokens`、`total_tokens`。

**Messages 流式结构**

帧的 `event` 与 `data.type` 一致，顺序为 `message_start` → (`content_block_start` → `content_block_delta…` → `content_block_stop`)… → `message_delta` → `message_stop`。`content_block_delta.delta.type` 枚举 `text_delta` / `thinking_delta` / `signature_delta` / `input_json_delta`（后者 `partial_json` 需拼接后解析）。`message_delta.delta.stop_reason` 与 `usage` 在该事件中返回。

**Messages 非流式响应**：`id`、`type`（`message`）、`role`（`assistant`）、`model`、`content`（块顺序 thinking → text → tool_use）、`stop_reason`（`end_turn` / `max_tokens` / `tool_use` / `refusal` / `null`）、`stop_sequence`、`usage.input_tokens`、`usage.output_tokens`、`usage.cache_read_input_tokens`、`usage.cache_creation_input_tokens`、`usage.cache_creation.ephemeral_5m_input_tokens`、`usage.cache_creation.ephemeral_1h_input_tokens`、`usage.output_tokens_details.thinking_tokens`。

### 6. 思考/推理字段

| 请求字段 | `kimi-k3` | `kimi-k2.7-code` | `kimi-k2.6` |
| - | - | - | - |
| `reasoning_effort` | `low` / `high` / `max`（默认 `max`） | 不支持 | 不支持 |
| `thinking.type` | — | 仅 `enabled`，始终思考，传 `disabled` 报错 | `enabled`（默认）/ `disabled` |
| `thinking.keep` | — | 不传或传合法值 `all` 均按 `all` 处理（始终开启、无法关闭），其他值报错 | `null`（默认，不保留）/ `all`（启用） |

| 响应字段 | 出现位置 | 说明 |
| - | - | - |
| `reasoning_content` | Chat `choices[0].message` / 流式 `delta` | 推理过程，仅在思考模式启用时返回；SDK 类型未声明，Python 需用 `hasattr` / `getattr` 读取 |
| `usage.output_tokens_details.reasoning_tokens` | Responses | 推理消耗的 Token 数 |
| `usage.output_tokens_details.thinking_tokens` | Messages | 输出 Token 中用于推理的部分 |
| `usage.completion_tokens` | Chat | 含推理 Token |

- `kimi-k3` 始终进行推理，保留式思考（Preserved Thinking）始终开启，不支持 `thinking` 参数；如果不希望思考太长，把 `reasoning_effort` 设为 `low`（K3 的思考无法关闭）。
- `reasoning_effort` 是 Chat 的**顶层**字段；Responses 用 `reasoning.effort`，Messages 用 `output_config.effort`。
- **回传要求**：多轮对话与工具调用必须把 API 返回的完整 assistant message 原样回传到 `messages`（含 `reasoning_content`，工具调用时含 `tool_calls`）。K2.x 跨轮保留由 `thinking.keep` 决定：`kimi-k2.6` 默认不保留（`null`），`kimi-k2.7-code` 始终保留。单轮任务内（一次工具调用循环中的多步推理）应保留上下文中所有思考内容。
- Messages 协议下思考块为 `thinking`（含 `thinking` 与 `signature`），多轮时需把响应中的 thinking 块（含 `signature`）原样放回 assistant 消息。
- `reasoning_content` 计入 token 消耗，`reasoning_content` 的 Tokens 数加上 `content` 的 Tokens 数应小于等于 `max_tokens`。
- 建议使用思考模型时设置 `max_tokens >= 16000`；不要显式传 `temperature`；建议使用 `stream=True`。
- 部分续写场景（Partial Mode 补全被截断输出）需要把上一轮的 `reasoning_content` 一并传回；注意 `max_tokens` 会优先被思考消耗，若截断点在思考阶段则 `content` 为空、`finish_reason` 已是 `length`。
- `kimi-k2.6` 在思考开启（`{"type":"enabled"}`）时，内置联网搜索 `$web_search` 暂时不兼容，可先关闭思考再使用。

### 7. 工具调用字段

**tools 定义（Chat Completions / Messages / Responses 见第 3 节）**

| 字段 | 类型 | 必填/默认 | 说明 |
| - | - | - | - |
| `type` | string | 必填 | `function`（普通函数）；`builtin_function`（Kimi 内置工具，如 `$web_search`） |
| `function.name` | string | 必填 | 正则 `^[a-zA-Z_][a-zA-Z0-9-_]{0,127}$`；一次请求内唯一 |
| `function.description` | string | 可选 | 描述工具作用与使用场景 |
| `function.parameters` | object | 必填（`type=function`） | JSON Schema，顶层 `type` 必须为 `object`，需符合 MFJS 规范 |
| `function.strict` | boolean | 可选，默认 `true` | `true` 时严格按 schema 约束入参；`false` 仅保证输出为合法 JSON 对象 |

**tool_choice**

| 取值 | 含义 | 适用范围 |
| - | - | - |
| `auto`（默认） | 模型自行决定是否调用工具 | 全部模型 |
| `none` | 不调用工具 | 全部模型 |
| `required` | 强制至少调用一个工具 | 仅 `kimi-k3`；`kimi-k2.6` / `kimi-k2.7-code` 传入报错 |
| `{"type":"function","function":{"name":"..."}}` | 强制调用指定工具 | 与思考开启不兼容：思考开启时传入返回 400（`tool_choice 'specified' is incompatible with thinking enabled`） |

- `tool_choice` 是请求级参数，每次请求独立生效；是否设置 `tool_choice` 不会破坏前缀缓存。
- Messages 协议的 `tool_choice.type` 为 `auto` / `any` / `none`。

**工具调用响应与回传**

| 字段/消息 | 说明 |
| - | - |
| `finish_reason: "tool_calls"` | 表示本次返回是工具调用而非模型回复；此时 `message.content` 可能非空（模型在解释要调用哪些工具） |
| `message.tool_calls[].id` | 每次调用唯一 |
| `message.tool_calls[].type` | 固定 `function`（与声明时的工具类型一致，可能是 `builtin_function`） |
| `message.tool_calls[].function.name` | 工具名称 |
| `message.tool_calls[].function.arguments` | 合法但被序列化成字符串的 JSON Object |
| `{"role":"tool","tool_call_id":...,"content":...}` | 提交工具执行结果；`tool_call_id` 必须与 `tool_calls[].id` 一一对齐 |

- 每个 `tool_call` 都必须有一条对应的 `role=tool` 消息，数量不一致或 `tool_call_id` 对不上都会报错（如 `tool_call_id not found`）。
- 必须把 API 返回的 assistant 消息（完整含 `tool_calls`）原样加入 `messages`，否则报 `tool_call_id not found`。
- 多个 `tool_calls` 时 `role=tool` 消息顺序不敏感；唯一性要求仅针对当轮 tool_calls-response 的局部。
- `tools` 内容也计入总 Token，需保证 `tools` + `messages` 总和不超上下文窗口。
- 官方建议：不使用已废弃的 `function_call` / `functions`，统一用 `tool_calls`（支持并行调用）。

**动态加载工具**

| 字段 | 类型 | 必填 | 说明 |
| - | - | - | - |
| `messages[].role` | string | 必填 | 固定 `system` |
| `messages[].tools` | array | 必填 | 从该消息位置开始可供模型调用的工具列表，格式与顶层 `tools` 完全一致，必须是完整的工具定义 |

- 携带 `tools` 的 `system` 消息与普通 input message 地位相同，只影响其后的对话；与顶层 `tools` 声明的全局工具并存。
- 该消息**不能**再携带 `content` 字段，否则 400（`cannot be used with content`）；OpenAI SDK 可直接在 `messages` 中透传 `tools`，无需 `extra_body`。
- 目前仅 `kimi-k3` 支持；在其他模型（如 `kimi-k2.6`）上请求会返回 `tokenization failed` 错误。
- 动态工具声明按请求生效，服务端不保存；建议追加到 `messages` 末尾并原样保留（追加不影响前缀缓存，中间插入/修改会使变更位置之后的缓存失效）。
- **Tool Search 组合用法**：顶层只声明一个后端实现的 `search_tools` 工具，system prompt 中声明可搜索领域，首轮用 `tool_choice: "required"` 强制调用，再按返回结果用携带 `tools` 的 `system` 消息动态注入完整工具声明，后续把 `tool_choice` 恢复为 `"auto"`。

**官方工具（Formula）**

| 工具名称 | 工具描述 |
| - | - |
| `convert` | 单位转换（长度、质量、体积、温度、面积、时间、能量、压力、速度、货币） |
| `web-search` | 实时信息及互联网检索 |
| `rethink` | 智能整理想法 |
| `random-choice` | 随机选择 |
| `mew` | 随机产生猫的叫声和祝福 |
| `memory` | 记忆存储和检索，支持对话历史、用户偏好等持久化 |
| `excel` | Excel 和 CSV 文件分析 |
| `date` | 日期时间处理 |
| `base64` | Base64 编码与解码 |
| `fetch` | URL 内容提取并 Markdown 化 |

- formula URI 形如 `moonshot/web-search:latest`（namespace 目前只支持 `moonshot`，`latest` 是默认 tag）；对应 URI 列表：`moonshot/convert:latest`、`moonshot/web-search:latest`、`moonshot/rethink:latest`、`moonshot/random-choice:latest`、`moonshot/mew:latest`、`moonshot/memory:latest`、`moonshot/excel:latest`、`moonshot/date:latest`、`moonshot/base64:latest`、`moonshot/fetch:latest`。
- 流程：`GET /v1/formulas/{uri}/tools` 取声明 → `POST /v1/chat/completions` 带 `tools` → 模型返回标准 `function` 类型 `tool_calls` → `POST /v1/formulas/{uri}/fibers` 以 body `{"name":..., "arguments":...}`（原样透传）执行 → 以 `role=tool` 回传结果继续对话。`web-search` 是 protected，结果在 `context.encrypted_output`（格式 `----MOONSHOT ENCRYPTED BEGIN----...----MOONSHOT ENCRYPTED END----`），其他工具返回 `context.output`。
- 除联网搜索 `web-search` 按次收费外，其余官方工具目前限时免费。
- `$web_search` 内置工具（`builtin_function`）：只需声明 `type` 与 `function.name`，不需参数说明；`$` 前缀是 Kimi 内置函数的约定（普通 `function` 定义中不允许出现 `$`）。调用方把 `tool_call.function.arguments` 原封不动用 `role=tool` 提交，即由 Kimi 执行搜索。该方式**即将下线（预计 2026 年 10 月 20 日）**，新接入请用 `/v1/tools/search`、`/v1/tools/search_pro`、`/v1/tools/fetch`。

### 8. 多模态与文件

**① 输入模态总表**

| 模态 | 支持的协议 | 字段名与嵌套结构 | 传法 | 格式白名单 | 体积与时限 | 哪些模型支持 |
| - | - | - | - | - | - | - |
| 文本 | Chat / Responses / Messages | Chat：`messages[].content` 传 string，或多模态数组里的 `{"type":"text","text":"..."}`；Responses：`input` 传 string，或 item `{"type":"input_text","text":"..."}`（`output_text` 只用于回放 assistant 历史）；Messages：`messages[].content` 传 string 或 `{"type":"text","text":"..."}` 块 | 直接写在请求体里 | 无格式白名单（JSON 字符串） | 计入 Token；输入 + `max_completion_tokens` 不得超过上下文窗口 | 全部模型 |
| 图片 | Chat / Responses / Messages | Chat：`content[].type="image_url"`，`image_url` 可传对象 `{"url":"..."}` 或直接传字符串（等价于对象里的 `url`）；Responses：`input` item `{"type":"input_image","image_url":"...","detail":"..."}`，`detail` 枚举 `auto` / `low` / `high` / `original`；Messages：content 块 `{"type":"image","source":{...}}`，`source.type="base64"` 时需同时给 `media_type` 与 `data`，`source.type="url"` 时用 `url` 传 `ms://<file_id>` | base64 data URL（`data:image/png;base64,...`）或 `ms://<file_id>` 文件引用；Responses 的 `image_url` 只接受 data URL，不支持公网 http(s) URL | `image/jpeg`、`image/png`、`image/gif`、`image/webp`、`image/bmp`、`image/heic`、`image/heif`（Messages 的 `source.media_type` 枚举只声明 `image/jpeg` / `image/png` / `image/gif` / `image/webp`）；SVG 不支持 | 请求 Body ≤ 100M；图片数量无上限；推荐分辨率 ≤ 4k（4096×2160）；单张图片的体积与时限文档未给出 | `kimi-k3`、`kimi-k2.6`、`kimi-k2.7-code`、`kimi-k2.7-code-highspeed` |
| 音频 | 文档未给出 | 文档未给出 | 文档未给出 | 文档未给出 | 文档未给出 | 文档未给出 |
| 视频 | 仅 Chat | `content[].type="video_url"`，`video_url` 可传对象 `{"url":"..."}` 或字符串；对象形式下只有一个 `url` 字段 | `data:video/mp4;base64,...` 或 `ms://<file_id>` | `video/mp4`、`video/mpeg`、`video/mov`、`video/avi`、`video/x-flv`、`video/mpg`、`video/webm`、`video/wmv`、`video/3gpp` | 推荐分辨率 ≤ 1080p（1920×1080）；非常大的视频必须改用上传文件（`purpose="video"` + `ms://`）方式；请求 Body ≤ 100M | `kimi-k3`、`kimi-k2.6`、`kimi-k2.7-code`、`kimi-k2.7-code-highspeed`（Responses / Messages 未提供视频输入类型） |
| 文件 | Files API + 对话协议 | 上传后用 `GET /v1/files/{file_id}/content` 取回抽取结果，把**文件内容**（不是 `file_id`）作为 `role=system` 的 `content` 放进 `messages`；多文件 = 每个文件一条独立 `system` 消息 | 文件抽取出的 Markdown 文本（`file_id` 不参与对话引用） | 见 ③ 表的格式清单 | 单文件 ≤ 100 MiB；组织总存储默认 10 GiB | 文档未按模型区分支持情况（示例统一用 `kimi-k3`） |

- `ms://` 是 Moonshot storage 内部引用文件的协议，`ms://<file_id>` 引用的是「已按 `purpose="image"` / `"video"` 上传」的图片或视频。
- 视觉消息的 `content` 必须是对象数组（JSON 数组），**不要**把数组序列化成字符串塞进 `content`（文档明确说这是非标准格式，不保证被当作视觉输入处理）。
- GIF / WebP 动图同样通过 `image_url` 传入，但底层可能按视频解码，token 也按视频方式计算。
- SVG 不支持：用 `purpose="image"` 上传 SVG、或在 `image_url` 里传 SVG（含 base64）都会被拒绝；要理解 SVG 只能把源码当文本输入。
- 图片公网 URL：`guide/use-kimi-vision-model` 正文「功能支持与限制」明确写「URL 格式的图片：不支持，目前仅支持使用 base64 编码的图片内容和通过文件 ID 上传的图片/视频」；但该页在 `llms.txt` 里的摘要却写着「支持 base64、URL、文件上传与多图片对话」。以正文为准：**不支持 URL 图片**。
- 文档未给出「音频输入」的任何字段或枚举：Chat 的 `content[].type` 只有 `text` / `image_url` / `video_url`，Responses 的内容分片只有 `input_text` / `input_image` / `output_text`，Messages 的块类型只有 `text` / `image` / `thinking` / `tool_use` / `tool_result`，Files API 的 `purpose` 枚举也没有音频项。
- 图片 / 视频按动态 token 计费（分辨率越高、关键帧越多越贵），可用 `POST /v1/tokenizers/estimate-token-count` 提前估算；Vision 模型按推理总 Token 计费。
- Vision 模型支持的特性：多轮对话、流式输出、工具调用、JSON Mode、Partial Mode。
- base64 与上传文件怎么选（文档原文口径）：请求体总大小有限制，**非常大的视频必须用上传文件方式**；需要多次引用的图片或视频也推荐先上传。
- 多模态工具结果：只有 Messages 协议的文档写明 `tool_result` 的 `content` 「可为字符串或 text / image 块数组」；Chat / Responses 的 `role=tool` 内容形态文档未给出。
- 文件引用方式的边界：图片 / 视频可以用 `ms://<file_id>` 引用，但文档明确「目前不支持使用文件 `file_id` 的方式引用文件内容作为上下文」（`file-extract` 必须把内容贴进 prompt）。

**② 输出模态总表**

| 输出模态 | 该站是否有对应端点 | 端点与字段 | 返回形式 | 限制 |
| - | - | - | - | - |
| 文本 | 有 | `POST /v1/chat/completions` 的 `choices[].message.content`、`POST /v1/responses` 的 `output` 里的 message、`POST /anthropic/v1/messages` 的 `text` 块 | 同步返回或 SSE 流式增量 | 受 `max_completion_tokens` / `max_output_tokens` / `max_tokens` 限制，超限时 `finish_reason=length` 或 `status=incomplete` |
| 结构化 JSON | 有 | Chat `response_format`（`json_object` / `json_schema`）、Responses `text.format`、Messages `output_config.format` | 仍是文本输出，只是被约束为 JSON | `json_schema` 需符合 MFJS 规范 |
| 思考内容 | 有 | Chat `choices[].message.reasoning_content`、Responses 的 `reasoning` item、Messages 的 `thinking` 块（含 `signature`） | 与正文一并返回 / 流式增量 | 仅在思考模式启用时返回；计入 Token |
| 工具调用 | 有 | Chat `choices[].message.tool_calls`、Responses `function_call` / `custom_tool_call`、Messages `tool_use` 块 | JSON 字符串形式的 `arguments` / `partial_json` | 由调用方本地执行；`web_search` 由服务端执行 |
| 图片生成 | **文档中未提供** | 文档中未提供 | — | — |
| 语音合成（TTS） | **文档中未提供** | 文档中未提供 | — | — |
| 视频生成 | **文档中未提供** | 文档中未提供 | — | — |
| 图片（搜索结果，非生成） | 有 | `POST /v1/responses` 使用 `web_search` 时 `include: ["web_search_call.results"]` 且 `search_content_types` 含 `image` 才返回 | `image_result`（含 `image_url`、可选的 `caption`） | 需 `image_settings`（`max_results` 范围 1–10，默认 3；`caption` 默认 `false`）；这是搜到的图片链接，不是模型生成 |

- 结论：该站**没有**语音合成 / 图片生成 / 视频生成端点，模型侧输出只有文本（含结构化 JSON、思考内容、工具调用）。
- 为确认「没有」，查过的页与产物：`llms.txt` 全量索引（含侧边栏全部条目，无 audio / speech / image-generation / video-generation 类页面）、`https://platform.kimi.com/docs/openapi.json`（全部 16 个路径：`/v1/chat/completions`、`/v1/responses`、`/anthropic/v1/messages`、`/v1/models`、`/v1/tokenizers/estimate-token-count`、`/v1/users/me/balance`、`/v1/signatures/verify`、`/v1/files`（POST/GET）、`/v1/files/{file_id}`（GET/DELETE）、`/v1/files/{file_id}/content`、`/v1/batches`（POST/GET）、`/v1/batches/{batch_id}`、`/v1/batches/{batch_id}/cancel`、`/v1/tools/search`、`/v1/tools/search_pro`、`/v1/tools/fetch`）、`https://platform.kimi.com/docs/hosted-agents/openapi.yaml`（托管智能体全线路径：agents / artifacts / environments / files / memory-stores / plugins / sessions / skills / triggers / vaults，同样没有音频或内容生成端点）。

**③ 文件上传与管理 API**

| 操作 | 端点 | 关键请求字段 | 关键响应字段 |
| - | - | - | - |
| 上传 | `POST /v1/files`（multipart/form-data） | `file`（必填，须带文件名或在表单里显式给 `filename`；该部分自身的 Content-Type 可省略）、`filename`（可选，覆盖文件自带的名字）、`purpose`（可选，默认 `file-extract`） | 成功 201（不带 `kimi-api-version` 时 200）：文件记录 `id`、`filename`、`mime_type` / `object`、`size_bytes` / `bytes`、`created_at`、`purpose`、`extract_status` / `status` |
| 列表 | `GET /v1/files` | `page_size`（integer，1–1000，默认 50）、`page_token`（string） | `items[]`（最新在前）、`next_page_token`（缺省表示已到最后一页）；**不带 `page_size` 时返回完整列表** |
| 获取信息 | `GET /v1/files/{file_id}` | `file_id`（path，必填） | `id`、`object`（`file`）、`bytes`、`created_at`（Unix 时间戳）、`filename`、`purpose`、`status`、`status_details` |
| 删除 | `DELETE /v1/files/{file_id}` | `file_id`（path，必填） | `id`、`object`（`file`）、`deleted`（boolean）；文件不存在或已删除返回 404 |
| 取内容 | `GET /v1/files/{file_id}/content` | `file_id`（path，必填） | `text/plain`，`file-extract` 用途返回 Markdown 格式提取结果 |

| `purpose` 取值 | 含义 | 内容端点 | 备注 |
| - | - | - | - |
| `file-extract`（默认） | 文档（含表格、公式）解析成供模型使用的 Markdown | 可用，返回解析出的 Markdown | 图片文件不再支持内容抽取 |
| `image` | 上传图片用于视觉理解，跳过文档解析 | 不可用 | 图片不做 OCR |
| `video` | 上传视频用于视频理解，跳过文档解析 | 不可用 | — |
| `batch` | 作为批处理输入（JSONL） | 返回上传的文件（上传时经解析规范化，字节可能与原文件不同） | 只有上传者本人可读 / 可删，且不能挂载到会话 |

- 体积与存储：单文件上限 **100 MiB** 且不能为空；文件个数不再受限，改为按**组织总存储量**限制（默认 **10 GiB**），达到上限后新的上传被拒绝；删除文件可释放配额。
- 支持格式（内容抽取）：`.pdf`、`.txt`、`.csv`、`.doc`、`.docx`、`.xls`、`.xlsx`、`.ppt`、`.pptx`、`.md`、`.dot`、`.epub`、`.html`、`.json`、`.mobi`、`.log`、`.go`、`.h`、`.c`、`.cpp`、`.cxx`、`.cc`、`.cs`、`.java`、`.js`、`.css`、`.jsp`、`.php`、`.py`、`.py3`、`.asp`、`.yaml`、`.yml`、`.ini`、`.conf`、`.ts`、`.tsx` 等。
- 命名与重复：与现有文件重名上传时，服务端在存储的文件名上加后缀，例如 `report (1).txt`；上传后的文件不可修改。
- `id` 怎么在对话里引用（三种，不能混）：`file-extract` 的文件 → 取内容后把**内容**贴进 prompt（不支持 `file_id` 引用）；`image` / `video` 的文件 → 用 `ms://<file_id>` 填进 `image_url` / `video_url`；`batch` 的文件 → 用 `input_file_id` 建批处理任务。
- 有效期：**文档未给出**文件的保留期、过期时间或自动清理策略（只写了删除可释放配额、删除不可撤销）。
- 字段形状有两套写法（都出现在文档里）：`files-upload` / `files-list` 页内嵌的 OpenAPI 用 `mime_type` / `size_bytes` / `extract_status`，并说明「不带 `kimi-api-version` 请求头时改用 `status`、`status_details`」；`files-retrieve` / `files-delete` 页内嵌的 OpenAPI 用 `object` / `bytes` / `created_at`（Unix 时间戳）/ `status` / `status_details`。
- 其他限制：文件解析服务**限时免费**，请求高峰期平台可能临时限流；`purpose` 取值错误返回 400（`Invalid purpose: xxx, only file-extract, batch, batch_output, lambda, image and video accepted`）；提取结果里列表等结构边界处可能出现 `{=html}` 包裹的 `<!-- -->` 空注释块，属正常输出可安全删除；抽取结果可本地留存复用，不必每次重新上传。

**④ 相关端点与限制汇总**

| 用途 | 端点 | 方法与协议 | 备注 |
| - | - | - | - |
| 多模态对话（Chat） | `/v1/chat/completions` | POST / OpenAI | 唯一支持视频输入的协议；图片、视频走 `content[]` |
| 多模态对话（Responses） | `/v1/responses` | POST / OpenAI | 只有 `input_image`，且只收 data URL；无视频输入 |
| 多模态对话（Messages） | `/anthropic/v1/messages` | POST / Anthropic | 只有 `image` 块（base64 或 `ms://`）；无视频输入 |
| 上传 / 列表 / 获取 / 删除 / 取内容 | `/v1/files`、`/v1/files/{file_id}`、`/v1/files/{file_id}/content` | POST / GET / DELETE / GET | 见 ③ 表 |
| 批处理（可带多模态请求体） | `/v1/batches`、`/v1/batches/{batch_id}`、`/v1/batches/{batch_id}/cancel` | POST / GET | 见本节末尾 Batch API |
| Token 估算（含图片 / 视频） | `/v1/tokenizers/estimate-token-count` | POST / OpenAI | 请求体 `model` + `messages`，取 `data.total_tokens` |
| 查询模型能力 | `/v1/models` | GET / OpenAI | 响应含 `supports_image_in`、`supports_video_in`（boolean），可直接判断某模型是否收图片 / 视频 |

| 限制项 | 数值 / 口径 |
| - | - |
| 单文件上限 | 100 MiB，且不能为空 |
| 组织总存储 | 默认 10 GiB（文件个数不限） |
| 多模态请求 Body | ≤ 100M（图片数量本身无限制，但整体 Body 不能超） |
| 图片建议分辨率 | 不超过 4k（4096×2160），更高只会增加处理时间 |
| 视频建议分辨率 | 不超过 1080p（1920×1080） |
| 超大视频 | 必须改用上传文件（`purpose="video"`）+ `ms://<file_id>` |
| 文件有效期 / 图片单张体积上限 / 音频输入 | 文档未给出 |
| 计费 | 图片与视频按动态 token 计费；文件内容抽取与文件存储限时免费；批处理多模态可用 base64 内嵌（体积膨胀约 33%）或 `ms://` 引用 |

**Batch API**

| 字段 | 类型 | 必填/取值 | 说明 |
| - | - | - | - |
| `custom_id` | string | 必须 | 自定义请求标识，用于追踪结果，文件内唯一 |
| `method` | string | 必须，固定 `POST` | 请求方法 |
| `url` | string | 必须，固定 `/v1/chat/completions` | 请求地址 |
| `body` | object | 必须 | 与 Chat Completions API 参数一致 |
| `input_file_id` | string | 必填（创建任务） | 输入的 JSONL 文件 ID |
| `endpoint` | string | 必填 | 固定 `/v1/chat/completions` |
| `completion_window` | string | 必填 | 如 `24h`、`3d`、`7d` |
| `metadata` | object | 可选 | 任务元数据 |
| `request_counts.completed` / `.failed` / `.total` | integer | 响应字段 | 请求计数 |
| `output_file_id` / `error_file_id` | string | 响应字段 | 结果文件 / 错误文件 ID |
| `in_progress_at` / `expires_at` / `finalizing_at` / `completed_at` / `failed_at` / `cancelling_at` / `cancelled_at` | integer | 响应字段 | 各阶段时间戳 |
| `status` | string | `validating` / `failed` / `in_progress` / `finalizing` / `completed` / `expired` / `cancelling` / `cancelled` | 任务状态 |

- 输入文件须为 `.jsonl`，不为空且不超过 100MB；每行必须是含 `custom_id`、`method`、`url`、`body` 的合法 JSON 对象；所有行的 `model` 必须相同（一个批次只允许一个模型）。
- Batch API 支持 `kimi-k2.7-code` 和 `kimi-k2.6`，**暂不支持 `kimi-k3`**；这些模型的 `temperature`、`top_p`、`n`、`presence_penalty`、`frequency_penalty` 不可修改，请勿在 body 中设置。相比实时调用可节省 40% 的推理费用。
- 上传 JSONL 时 `purpose` 必须为 `"batch"`；结果文件中每行含 `id`、`custom_id`、`response.status_code`、`response.body`、`error`。
- 支持多模态：图片/视频可用 base64 内嵌（体积膨胀约 33%）或先上传（`purpose="image"` / `"video"`）后用 `ms://<file_id>` 引用。
- 仅 `validating`、`in_progress`、`finalizing` 状态的任务可以取消；取消后先变 `cancelling`，最终变 `cancelled`。

### 9. 缓存与成本字段

| 字段 | 位置 | 类型 | 取值/默认 | 说明 |
| - | - | - | - | - |
| `prompt_cache_key` | Chat / Responses 请求 | string | 默认 `null` | 缓存相似请求以优化命中率；Coding Agent 通常是 session id / task id，退出并恢复会话时应保持不变 |
| `prompt_cache_options` | Chat / Responses 请求 | object | 不传时默认开启写入（`5m` 档） | 上下文缓存写入选项 |
| `prompt_cache_options.mode` | 同上 | string | `implicit`（默认，唯一取值） | 自动将请求前缀写入缓存 |
| `prompt_cache_options.ttl` | 同上 | string | `5m`（默认）/ `1h` | 两档相互独立，TTL 在首次写入时锁定，不能改写 |
| `cache_control` | Messages 顶层 | object | `type: ephemeral`（必填）、`ttl: 5m \| 1h` | 仅顶层传入时生效；不传时只读缓存（`5m` 档）不写入 |
| `usage.prompt_tokens` | Chat 响应 | integer | — | 始终为总输入 Token 数 |
| `usage.prompt_tokens_details.cached_tokens` | Chat 响应 | integer | — | 命中缓存的 Token 数（缓存读取） |
| `usage.prompt_tokens_details.cache_write_tokens` | Chat 响应 | integer | — | 本次写入缓存的 Token 数（缓存写入） |
| `usage.input_tokens` / `usage.input_tokens_details.cached_tokens` / `.cache_write_tokens` | Responses 响应 | integer | — | 语义同上，字段名不同 |
| `usage.cache_read_input_tokens` / `usage.cache_creation_input_tokens` / `usage.cache_creation.ephemeral_5m_input_tokens` / `.ephemeral_1h_input_tokens` | Messages 响应 | integer | — | Messages 口径不同，见第 4 节 |
| `Msh-Usage-Cache-Write-Tokens-5m` | Chat 响应头 | — | — | 本次按 `5m` 档写入的 token 数；全部命中无新增写入时为 0 |
| `Msh-Usage-Cache-Write-Tokens-1h` | Chat 响应头 | — | — | 本次按 `1h` 档写入的 token 数 |

- Chat / Responses：`cached_tokens`、`cache_write_tokens` 与未缓存部分互斥，三者之和等于总输入（Chat 为 `prompt_tokens`，Responses 为 `input_tokens`），即未缓存部分 = 总输入 − cached_tokens − cache_write_tokens。
- 缓存以组织（org）为粒度隔离，组织之间不共享；相同前缀在有效期内命中后按原 TTL 刷新，命中部分只收缓存读取费用、不再收写入费用；不支持手动清除缓存，无活动超过所选 TTL 后自动过期；缓存按块存储，不足一整块的部分无法写入缓存，会计为缓存未命中。
- 暂不支持显式缓存断点：`content` 中出现 `prompt_cache_breakpoint` 时请求会被拒绝（HTTP 400）。
- 哪些模型支持缓存写入：`kimi-k3` 支持；`kimi-k2.7`、`kimi-k2.7-highspeed`、`kimi-k2.6` 不支持。
- Responses 响应顶层回显实际应用的 `mode` / `ttl`；Chat Completions 不回显，需请求侧自己记录。
- 计费（以 `kimi-k3` 为例，每 1M tokens）：Input（缓存未命中）¥20；Cache Write（`5m`）¥20；Cache Write（`1h`）¥40；Cached Input（缓存命中）¥2。缓存命中价格是未命中价格的 1/10。

### 10. 特殊模式

**JSON Mode / Structured Output（`response_format`）**

| 取值 | 说明 |
| - | - |
| `{"type": "text"}`（默认） | 普通文本输出 |
| `{"type": "json_object"}` | 强制输出合法 JSON Object；必须在 system / user prompt 中明确描述期望的 JSON 字段和类型 |
| `{"type": "json_schema", "json_schema": {...}}` | Structured Output，按给定 JSON Schema 输出结构化数据（推荐） |

| `json_schema` 子字段 | 类型 | 说明 |
| - | - | - |
| `name` | string | Schema 标识名称，用于日志与调试 |
| `strict` | boolean | 是否严格按 schema 约束输出，建议显式设为 `true`；为 `true` 时 schema 需符合 MFJS 规范 |
| `schema` | object | JSON Schema 对象，定义输出结构 |

- 校验 schema 是否符合 MFJS：`go install github.com/moonshotai/walle/cmd/walle@latest` 后 `walle -schema '你的schema' -level strict`。
- 模型差异：`kimi-k3` 稳定支持 Structured Output（嵌套对象、数组、`anyOf` 等）；`kimi-k2.7-code` 支持最稳（含 `$ref` / `oneOf` / `additionalProperties: true`）；`kimi-k2.6` 在复杂 schema 下偶有不稳定（`$ref` 可能返回 Markdown 代码块、`oneOf` 可能被忽略、`partial=true` 可能输出 schema 外字段），建议用简单 schema 并做业务层二次校验。
- 只解析 `choices[0].message.content` 作为最终 JSON，不要 `json.loads` 整个响应对象（思考模型还会返回 `reasoning_content`）。
- 模型只会生成 JSON Object，不要引导它生成 JSON Array。
- 输出被截断时检查 `finish_reason` 是否为 `length`，适当增大 `max_tokens`。
- 建议用可为 `null` 的联合类型（如 `"type": ["integer","null"]`）表达缺失信息。
- 是否设置 `response_format` 不会破坏前缀缓存。

**Partial Mode（Prefill）**

| 字段 | 类型 | 取值 | 说明 |
| - | - | - | - |
| `messages[].partial` | boolean | `true` | 在 messages 数组末尾的最后一条 assistant 消息上设置，模型强制以该消息 `content` 开头继续生成 |
| `messages[].name` | string | — | 强化角色认知，强制模型以 `name` 指定角色的口吻输出；`name` 是输出内容前缀的一部分 |

- 用法：末尾追加 `role="assistant"`、`partial=true` 的消息，把希望模型"接着说"的内容放在 `content`；最终展示时需自行把该前缀拼回生成内容之前。
- 常见用途：强制特定格式开头（如 JSON 的 `{`、代码块的 ```` ```python ````）、角色扮演保持角色名前缀（配合 `name`）、`finish_reason="length"` 时用相同前缀续写被截断的内容。
- **不要**把 Partial Mode 与 `response_format={"type": "json_object"}` 混用。
- 续写思考模型的截断输出时，需要把上一轮的 `reasoning_content` 一并传回 assistant 消息。

**自动断线重连**

- 文档只给出「捕获异常后重试」的示例模式：最多重复 100 次、每次等待 1s，可按需修改重试次数、间隔与重试条件。Kimi API 本身没有专门的重连字段；结合 Partial Mode 可从中断处继续生成（`finish_reason="length"` 场景）。
- 报 `Connection Error` / `Connection Time Out` 时，官方推荐启用 `stream=True`，并检查 SDK 超时与代理设置。

### 11. 错误码与限流

HTTP 状态码的通用含义见《通用基线》§0.8。

**通用错误响应体**：`{"error": {"type": "...", "message": "..."}}`（部分接口还含 `code`）。

| 码 | error type | 含义 | 建议动作 |
| - | - | - | - |
| 400 | `content_filter` | 输入或模型输出触发内容安全审查（`The request was rejected because it was considered high risk`） | 修改提示词，避免敏感/高风险内容 |
| 400 | `invalid_request_error` | 请求格式错误、缺少必填参数或参数类型非法 | 对照接口文档检查请求体 |
| 400 | `invalid_request_error` | `Input token length too long` 输入超过模型最大上下文 | 缩短输入或换用更大上下文模型 |
| 400 | `invalid_request_error` | `prompt tokens + max_tokens` 超过模型规格 | 减小 `max_tokens` 或换模型 |
| 400 | `invalid_request_error` | `Invalid purpose: xxx, only file-extract, batch, batch_output, lambda, image and video accepted` | 改正上传文件的 `purpose` |
| 400 | `invalid_request_error` | `File size is too large, max file size is 100MB` | 压缩或拆分后重新上传 |
| 400 | `invalid_request_error` | `File size is zero` | 检查文件是否损坏或为空 |
| 400 | `invalid_request_error` | 上传文件总数/存储超上限 | 删除不再使用的早期文件后重试 |
| 401 | `invalid_authentication_error` | `Invalid Authentication`，API Key 无效或格式错误 | 检查 `Authorization: Bearer <key>` |
| 401 | `incorrect_api_key_error` | `Incorrect API key provided`，未提供 API Key 或 Key 错误 | 确认 Key 与调用平台一致（中国站 / 国际站 Key 不可混用） |
| 403 | `permission_denied_error` | `The API you are accessing is not open` | 该 API 暂未对当前账号开放 |
| 403 | `permission_denied_error` | `You are not allowed to get other user info` | 检查接口权限范围 |
| 403 | `permission_denied_error` | `Your IP is not allowed to access this organization` | IP 不在组织白名单内（国际站常见），联系管理员添加 IP |
| 404 | `resource_not_found_error` | 模型不存在或当前账号无权限访问 | 检查 `model` 拼写及账号 tier |
| 429 | `engine_overloaded_error` | `The engine is currently overloaded, please try again later` | 按 `Retry-After` 等待、降低并发、指数退避重试；充值或提升 Tier 不能消除 |
| 429 | `exceeded_current_quota_error` | 账户欠费或已停用 / token 额度不足 | 检查余额与账单，充值后再试 |
| 429 | `rate_limit_reached_error` | 触发组织级并发 / RPM / TPM / TPD 限制 | 降低并发或频率，按响应提示等待后重试；或升级 tier |
| 499 | `client_closed_request` | 客户端在服务端返回前断开连接 | 检查 KeepAlive 与超时设置 |
| 500 | `server_error` / `unexpected_output` | 服务端内部错误 | 稍后重试；持续出现时附 `request_id` 联系支持 |
| 503 | `server_unavailable` | 服务暂时不可用 | 稍后重试（通常与节点扩容/维护有关） |
| 504 | `504 Gateway Time-out` | 服务端 900 秒无响应，网关返回 HTML 超时页面 | 非流式长请求建议改用 `stream: true` |

- 排障要点：401 先确认是否使用了正确平台的 API Key；429 先按 `error.type` 区分（节点过载退避重试 / 组织限速降并发或升等级 / 余额不足充值）；500 稍后重试，持续出现附带 `request_id` 联系 `api-service@moonshot.ai`；504 建议改用流式输出。
- 因 429 错误中断的请求不会扣费。OpenAI SDK 等客户端默认自动重试（默认重试 2 次），会把一次操作放大为多次请求并占用限速额度；tier0 用户一次失败请求就可能耗尽 RPM。

**联网搜索 / 网页抓取接口错误码**

| 码 | error.type | 含义 | 建议动作 |
| - | - | - | - |
| 400 | `invalid_request` | `invalid request body` 请求体不是合法 JSON | 修正请求体 |
| 400 | `invalid_request` | `text_query is required` | 补上搜索查询文本 |
| 400 | `invalid_request` | `timeout_seconds must be less than or equal to 60` | `timeout_seconds` 取值范围 1–60 |
| 400 | `invalid_request` | `limit must be between 1 and 20` | `limit` 取值范围 1–20 |
| 400 | `invalid_request` | `sites must contain at most 5 entries` / `site must not contain whitespace or parentheses` | 修正 `sites`（search_pro） |
| 400 | `invalid_request` | `time_window.start is invalid, expect YYYY / YYYY-MM / YYYY-MM-DD` / `time_window.start must not be after time_window.end` | 修正 `time_window`（search_pro） |
| 400 | `invalid_url` | `only http and https are supported` / `missing host` | 修正 `url`（fetch） |
| 401 | —（无错误响应体） | API Key 缺失或无效 | 检查鉴权 |
| 403 | —（无错误响应体） | 账号未激活或已停用 | 检查账号状态 |
| 403 | `security_risk` | URL 触发安全风控（fetch） | 更换 URL |
| 404 | `markdown_not_found` | 页面无可提取的正文内容（fetch） | 更换 URL |
| 408 | `client_canceled` | 客户端在服务端返回前断开连接 | 检查超时 |
| 429 | `rate_limited` | 触发频率或并发限制（响应头携带 `X-RateLimit-Limit`、`X-RateLimit-Remaining`，触发每秒请求数限制时还带 `X-RateLimit-Reset`） | 按响应头退避重试 |
| 429 | `rate_limit_unavailable` | 限速服务暂时不可用 | 稍后重试 |
| 500 | `internal_error` | 服务内部错误 | 稍后重试；持续出现携带 `X-Msh-Track-Id` 联系支持 |
| 502 | `upstream_failed` | 上游服务失败 | 稍后重试 |
| 504 | `timeout` | 搜索/抓取超时 | 调大 `timeout_seconds` 或精简查询后重试 |

- 搜索类接口的所有响应（含错误响应）都携带 `X-Msh-Track-Id`（请求 ID，请求携带同名头时沿用）与 `X-Msh-Chat-Id`（会话 ID，固定为 `toolgw-{X-Msh-Track-Id}`）。

**限流（按累计充值金额分级）**

| 用户等级 | 累计充值金额 | 并发 | RPM | TPM | TPD | 联网搜索 QPS |
| - | - | - | - | - | - | - |
| Tier0 | ¥ 0 | 1 | 3 | 500,000 | 1,500,000 | 1 |
| Tier1 | ¥ 50 | 15 | 100 | 2,000,000 | Unlimited | 3 |
| Tier2 | ¥ 100 | 40 | 100 | 3,000,000 | Unlimited | 5 |
| Tier3 | ¥ 500 | 50 | 200 | 3,000,000 | Unlimited | 10 |
| Tier4 | ¥ 5,000 | 60 | 200 | 4,000,000 | Unlimited | 20 |
| Tier5 | ¥ 20,000 | 100 | 300 | 5,000,000 | Unlimited | 50 |

- 并发 = 同一时间最多处理的请求数；RPM = 每分钟请求数；TPM = 每分钟 Token 数；TPD = 每天 Token 数；联网搜索 QPS = `/v1/tools/search`、`/v1/tools/search_pro` 每秒最多请求数（独立于其他列，不占用也不被占用；两个搜索接口分别计数、互不占额）。
- 代金券不计入累计充值总额；系统检测到账户异常行为会触发风控限速，一旦触发无法解除。

### 12. 模型清单与限制

| 模型名称 | 上下文窗口 | 描述 |
| - | - | - |
| `kimi-k3` | 1,048,576 tokens（1M） | 旗舰模型，2.8 万亿参数，原生支持视觉理解，面向软件工程、知识工作与深度推理 |
| `kimi-k2.7-code` | 262,144 tokens（256K） | Coding 模型，长上下文中更可靠地遵循指令 |
| `kimi-k2.7-code-highspeed` | 262,144 tokens（256K） | 与 `kimi-k2.7-code` 同一模型、参数约束完全一致，仅输出速度不同（约 180 Token/s，短上下文可达 260 Token/s） |
| `kimi-k2.6` | 262,144 tokens（256K） | 支持视觉与文本输入、思考与非思考模式、对话与 Agent 任务 |

已下线模型（不再维护和支持，建议迁移到 `kimi-k3`）：`kimi-k2.5`（2026-08-31 下线）、`moonshot-v1` 系列（含 `moonshot-v1-auto` 及 `-vision-preview` 版本，2026-08-31 下线）、`kimi-k2` 系列（2026-05-25 下线）、`kimi-latest`（2026-01-28 下线）、`kimi-thinking-preview`（2025-11-11 下线）。具体列表：`kimi-k2.5`、`moonshot-v1-8k`、`moonshot-v1-32k`、`moonshot-v1-128k`、`moonshot-v1-auto`、`moonshot-v1-8k-vision-preview`、`moonshot-v1-32k-vision-preview`、`moonshot-v1-128k-vision-preview`、`kimi-k2-0905-preview`、`kimi-k2-0711-preview`、`kimi-k2-turbo-preview`、`kimi-k2-thinking`、`kimi-k2-thinking-turbo`。

各模型参数支持差异：

| 参数 | `kimi-k3` | `kimi-k2.7-code` | `kimi-k2.6` |
| - | - | - | - |
| 上下文窗口 | 1M tokens | 256K tokens | 256K tokens |
| `thinking` | — | 可省略；显式设置时仅接受 `{"type":"enabled","keep":"all"}` | `{"type":"enabled"}`（默认）、`{"type":"disabled"}`、`{"type":"enabled","keep":"all"}` |
| `reasoning_effort` | `low` / `high` / `max`（默认 `max`） | 不支持 | 不支持 |
| `tool_choice` | `auto` / `none` / `required` | 不支持 `required` | 不支持 `required` |
| `temperature` | 固定 1.0 | 固定 1.0 | 思考 1.0 / 非思考 0.6 |
| `top_p` | 固定 0.95 | 固定 0.95 | 固定 0.95 |
| `n` | 固定 1 | 固定 1 | 固定 1 |
| `presence_penalty` / `frequency_penalty` | 固定 0 | 固定 0 | 固定 0 |

- 「固定」表示参数不可修改，传入其他值会报错，建议不要显式传入。
- `temperature` 接近 0 时，`n` 只能为 1，否则返回 `invalid_request_error`。
- 输出长度与上限：`kimi-k3` 的 `max_completion_tokens` 默认 `131072`，最大输出长度为 `1024*1024 - prompt_tokens`；`kimi-k2.7-code`、`kimi-k2.6` 最大输出长度为 `256*1024 - prompt_tokens`，且这两个模型的 `max_tokens` 默认值为 32k（32768）。
- 汉字容量估算：`kimi-k3` 约支持一百五十万个汉字；`kimi-k2.7-code`、`kimi-k2.6` 约支持四十万个汉字（均为估算值）。
- `n` 只会是 1：传入大于 1 返回 400 `invalid n: only 1 is allowed for this model`。
- 视觉模型（`kimi-k3` / `kimi-k2.6` / `kimi-k2.7-code` / `kimi-k2.7-code-highspeed`）支持多轮对话、流式输出、工具调用、JSON Mode、Partial Mode；不支持 URL 格式的图片。
- 官方工具调用说明：`kimi-k3` 上使用联网搜索等官方工具请走 Formula API（OpenAI 协议标准 `function` tool）；联网搜索正在更新，近期不建议用于生产流程。
- 定价（1M = 1,000,000 tokens）：

| 模型 | 计费单位 | 缓存写入（TTL 5min） | 缓存写入（TTL 1h） | 输入（缓存命中） | 输入（缓存未命中） | 输出 | 上下文窗口 |
| - | - | - | - | - | - | - | - |
| `kimi-k3` | 1M tokens | ¥20.00 | ¥40.00 | ¥2.00 | ¥20.00 | ¥100.00 | 1,048,576 tokens |
| `kimi-k2.7-code` | 1M tokens | — | — | ¥1.30 | ¥6.50 | ¥27.00 | 262,144 tokens |
| `kimi-k2.7-code-highspeed` | 1M tokens | — | — | ¥2.60 | ¥13.00 | ¥54.00 | 262,144 tokens |
| `kimi-k2.6` | 1M tokens | — | — | ¥1.10 | ¥6.50 | ¥27.00 | 262,144 tokens |

- 工具定价：联网搜索 Basic（`POST /v1/tools/search`）￥0.01/次；联网搜索 Pro（`POST /v1/tools/search_pro`）￥0.015/次；网页抓取（`POST /v1/tools/fetch`）￥0.01/次。计费条件均为「请求成功（HTTP 200）且返回内容非空」；失败或未返回结果不计费。
- 内建 `$web_search`（存量方式，预计 2026-10-20 下线）：在 `/v1/chat/completions` 的 `tools` 中加入并返回 `finish_reason=tool_calls` 且 `tool_call.function.name=$web_search` 时收 0.03 元；搜索结果会额外计入 Tokens（占用量可在返回的 `tool_call.function.arguments` 中读取，读取路径 `arguments.usage.total_tokens`）。
- 文件相关接口（文件内容抽取 / 文件存储）限时免费。

**`/v1/models` 响应字段**

| 字段 | 类型 | 说明 |
| - | - | - |
| `object` | string | 固定 `"list"` |
| `data[].id` | string | 模型 ID，例如 `kimi-k3` |
| `data[].object` | string | 固定 `"model"` |
| `data[].created` | integer | 模型创建时的 Unix 时间戳 |
| `data[].owned_by` | string | 模型所有者标识，例如 `"moonshot"` |
| `data[].context_length` | integer | 模型支持的最大上下文长度（tokens） |
| `data[].supports_image_in` | boolean | 是否支持图片输入 |
| `data[].supports_video_in` | boolean | 是否支持视频输入 |
| `data[].supports_reasoning` | boolean | 是否支持深度思考 |

**`/v1/tokenizers/estimate-token-count`**：请求体 `model`（默认 `kimi-k3`，enum `kimi-k3` / `kimi-k2.7-code` / `kimi-k2.7-code-highspeed` / `kimi-k2.6`，必填）与 `messages`（必填，role 支持 system / user / assistant / tool，content 不得为空）；无 `error` 字段时取 `data.total_tokens` 作为结果。

**`/v1/users/me/balance`** 响应字段：`code`（integer，0 表示成功）、`data.available_balance`（number，人民币元，含现金余额与代金券余额，≤0 时无法调用推理 API）、`data.voucher_balance`（number，不可为负）、`data.cash_balance`（number，可为负表示欠费；为负时 `available_balance` 等于 `voucher_balance`）、`scode`（string）、`status`（boolean）。

### 13. 来源页清单

本家页面清单（含每条 URL 的「这页讲了什么」一句话）统一见《附录：各家页面清单》的本家小节。

抓取方式：以 `https://platform.kimi.com/docs/llms.txt` 的页面清单为索引，逐页抓取 `https://platform.kimi.com/docs/` 下的 Markdown 版本页面（`<page>.md`）整理。本文件只写文档中真实存在的内容；未抓到的页/字段在对应位置注明「未抓到（页面 URL）」。

未抓到正文的页：`introduction.md`（**未抓到**，仅从 llms.txt 获取描述）。

`llms.txt` 中存在但本次**未逐页抓取**（均与四家协议字段级对比无关，本文件不涵盖其内容）：`get-api-key`、`ai-readable-docs`、`prompt-best-practice`、`benchmark-best-practice`、`use-batch-inference`、`use-kimi-k3-to-setup-agent`、`configure-the-modelscope-mcp-server`、`use-moonpalace`、`use-playground-to-debug-the-model`、`org-best-practice`、`zero-data-retention`、`ask-questions-about-pdf-content`、`join-the-community`、`hosted-agents/*`（快速开始/迁移/agents/permissions/skills/mcp/plugins/tools/environments/sandbox-reference/sessions/session-operations/event-stream/vaults/files/memory/dreams/multiagent-orchestration/triggers/official-ppt-agent/official-investment-research-agent）、`api-reference/*`（智能体、产物、环境、文件、记忆库、插件、会话、技能、触发器、凭据库）。

---

## MiniMax

抓取对象：`https://platform.minimax.cn/docs/`（文档站由 Mintlify 构建，每个页面可加 `.md` 后缀拿到纯 Markdown；`https://platform.minimax.cn/docs/llms.txt` 提供全站页面索引，`https://platform.minimax.cn/docs/sitemap.xml` 提供全站 URL 清单）。
本章内容均照抄自上述页面，未出现的内容一律标注「未抓到」，不做推断。

### 1. 端点与鉴权

| 协议 | base_url | Method + Path | 必需请求头 |
| :- | :- | :- | :- |
| OpenAI 兼容（Chat Completions） | `https://api.minimax.cn/v1` | `POST /v1/chat/completions` | `Authorization: Bearer <API_KEY>`；`Content-Type: application/json` |
| MiniMax 原生（文本合成 V2） | `https://api.minimax.cn` | `POST /v1/text/chatcompletion_v2` | `Authorization: Bearer <API_KEY>`；`Content-Type: application/json` |
| Anthropic 兼容（Messages） | `https://api.minimax.cn/anthropic` | `POST /anthropic/v1/messages` | `Authorization: Bearer <API_KEY>` 或 `x-api-key: <API_KEY>`（两者同时存在时优先 `Authorization`，官方推荐 `Authorization`）；`Content-Type: application/json`；示例中另带 `anthropic-version: 2023-06-01` |
| Anthropic 兼容 Token 计数 | `https://api.minimax.cn/anthropic` | `POST /anthropic/v1/messages/count_tokens` | 同上 |
| OpenAI Responses 兼容 | `https://api.minimax.cn/v1` | `POST /v1/responses` | `Authorization: Bearer <API_KEY>`；`Content-Type: application/json` |
| Responses Token 估算 | `https://api.minimax.cn/v1` | `POST /v1/responses/input_tokens` | 同上 |
| 文件上传（配合多模态） | `https://api.minimax.cn` | `POST /v1/files/upload` | `Authorization: Bearer <API_KEY>`；`Content-Type: multipart/form-data` |
| 文件列出 | `https://api.minimax.cn` | `GET /v1/files/list` | `Authorization: Bearer <API_KEY>`；query 参数 `purpose`（必填） |
| 文件检索 | `https://api.minimax.cn` | `GET /v1/files/retrieve` | `Authorization: Bearer <API_KEY>`；query 参数 `file_id`（必填） |
| 文件下载 | `https://api.minimax.cn` | `GET /v1/files/retrieve_content` | `Authorization: Bearer <API_KEY>`；query 参数 `file_id`（必填） |
| 文件删除 | `https://api.minimax.cn` | `POST /v1/files/delete` | `Authorization: Bearer <API_KEY>`；`Content-Type: application/json` |
| 同步语音合成（HTTP） | `https://api.minimax.cn` | `POST /v1/t2a_v2` | `Authorization: Bearer <API_KEY>`；`Content-Type: application/json` |
| 同步语音合成（WebSocket，客户端按句发文本） | `wss://api.minimax.cn` | `/ws/v1/t2a_v2` | 鉴权方式文档未给出 |
| 双向流式语音合成（WebSocket，可按任意粒度发文本） | `wss://api.minimax.cn` | `/ws/v1/t2a_v2_bidi` | 鉴权方式文档未给出 |
| 异步语音合成（创建任务） | `https://api.minimax.cn` | `POST /v1/t2a_async_v2` | `Authorization: Bearer <API_KEY>`；`Content-Type: application/json` |
| 异步语音合成（查询任务） | `https://api.minimax.cn` | `GET /v1/query/t2a_async_query_v2` | `Authorization: Bearer <API_KEY>`；query 参数 `task_id`（必填） |
| 语音识别 | `https://api.minimax.cn` | `POST /v1/speech_to_text` | `Authorization: Bearer <API_KEY>`；`Content-Type: multipart/form-data`；另有可选请求头 `language` |
| 图片生成（文生图 / 图生图） | `https://api.minimax.cn` | `POST /v1/image_generation` | `Authorization: Bearer <API_KEY>`；`Content-Type: application/json` |
| 视频生成 V2（创建任务） | `https://api.minimax.cn` | `POST /v2/video_generation` | `Authorization: Bearer <API_KEY>`；`Content-Type: application/json` |
| 视频生成 V2（查询任务） | `https://api.minimax.cn` | `GET /v2/query/video_generation/{task_id}` | `Authorization: Bearer <API_KEY>` |
| 视频生成 V2（查询任务列表） | `https://api.minimax.cn` | `GET /v2/query/video_generation` | `Authorization: Bearer <API_KEY>`；可选 query 参数 `page_num`、`page_size`、`filter.status`、`filter.task_ids`、`filter.model`、`filter.task_type` |
| 音乐生成 | `https://api.minimax.cn` | `POST /v1/music_generation` | `Authorization: Bearer <API_KEY>`；`Content-Type: application/json` |
| 歌词生成 | `https://api.minimax.cn` | `POST /v1/lyrics_generation` | `Authorization: Bearer <API_KEY>`；`Content-Type: application/json` |
| 翻唱前处理 | `https://api.minimax.cn` | `POST /v1/music_cover_preprocess` | `Authorization: Bearer <API_KEY>`；`Content-Type: application/json` |

其它鉴权/接入要点（原文摘录）：

- API Key 获取：按量付费走「账户管理 > 接口密钥 > 创建新的 API Key」；订阅 Key 与按量计费 API Key 相互独立。
- 官方推荐 Anthropic 兼容协议（支持 thinking 块、interleaved thinking）。
- 环境变量写法：`ANTHROPIC_BASE_URL=https://api.minimax.cn/anthropic`；`OPENAI_BASE_URL=https://api.minimax.cn/v1`；AI SDK 用 `MINIMAX_API_KEY`。
- 国际站：Prompt 缓存文档写明「国内用户使用 `https://api.minimax.cn/v1`，国际用户使用 `https://api.minimax.io/v1`」；`llms.txt` 页面里的链接全部指向 `platform.minimaxi.com`。
- 安全方案声明：`bearerAuth: http / scheme: bearer / bearerFormat: JWT`；Anthropic 兼容额外声明 `apiKeyAuth: apiKey / in: header / name: x-api-key`。

### 2. Chat Completions 请求字段总表

下表只列本家与《通用基线》§0.2 不同的字段与取值；未列出的标准字段语义同基线（本家文档未写明其默认值/范围即视为未列出）。

#### 2.1 OpenAI 兼容协议（`POST /v1/chat/completions`）

| 字段 | 类型 | 必填/默认 | 取值 | 说明 |
| :- | :- | :- | :- | :- |
| `model` | string | 必填 | `MiniMax-M3.1-Flash-Preview`、`MiniMax-M3`、`MiniMax-M2.7`、`MiniMax-M2.7-highspeed`、`MiniMax-M2.5`、`MiniMax-M2.5-highspeed`、`MiniMax-M2.1`、`MiniMax-M2.1-highspeed`、`MiniMax-M2` | — |
| `messages` | array | 必填 | Message 数组 | 支持文本、图片、视频和工具调用 |
| `service_tier` | string | 默认 `standard` | `standard`、`priority` | 请求准入服务层级。`priority` 价格为 `standard` 的 1.5 倍，会确保优先准入、更快响应并减少失败 |
| `thinking` | object | 默认 `{"type":"adaptive"}` | `type`：`disabled`、`adaptive` | 控制 thinking 行为。`adaptive` 对所有模型生效；`disabled` 的效果因模型不同（见 §6） |
| `reasoning_effort` | string | 仅 `MiniMax-M3.1-Flash-Preview` 生效；省略时该模型默认 `max` | `low`、`medium`、`high`、`xhigh`、`max` | 调节思考深度；其他模型会忽略该字段；不接受 `none`，传入返回 HTTP 400 |
| `reasoning_split` | boolean | 未给默认值 | `true`、`false` | `true` 把 thinking 拆分到 `reasoning_content` 字段；`false` 时 thinking 以 `<think>` 标签留在 `content` 内。该参数不开启/关闭 thinking。`MiniMax-M3.1-Flash-Preview` 暂不支持设为 `false` |
| `stream` | boolean | 默认 `false` | `true`、`false` | — |
| `stream_options` | object | — | 子字段 `include_usage` | 流式响应选项 |
| `stream_options.include_usage` | boolean | 默认 `false` | `true`、`false` | 是否在流式响应中包含 token 用量（为 `true` 时最后一个数据块带完整 usage） |
| `max_completion_tokens` | integer (int64) | 选填，最小 1 | `M3.1-Flash-Preview`/`M3` 推荐 131072（128K）、上限 524288（512K）；其他模型推荐 65536（64K）、上限 204800（200K） | 生成内容长度上限（Token）；因 `length` 中断时可调高 |
| `temperature` | number (double) | 默认 `1` | [0, 2] | 温度系数，越高越随机 |
| `top_p` | number (double) | 默认 `0.95`（M2.x 系列默认 `0.9`） | [0, 1] | 核采样 |
| `tools` | array | 选填 | 元素：`{type:"function", function:{name, description, parameters}}`（`type` 固定 `function`；`function` 必填 `name`、`parameters`） | 当前支持 function 工具 |
| `max_tokens` | integer (int64) | 已弃用（`deprecated: true`），最小 1 | — | 旧版生成长度限制参数，请改用 `max_completion_tokens` |

`messages` 元素（Message）与多模态内容块（标准角色枚举与 `tool_calls` 结构见《通用基线》§0.3）：

| 字段 | 类型 | 必填/默认 | 取值 | 说明 |
| :- | :- | :- | :- | :- |
| `name` | string | 选填 | — | 发送者名称；同一类型角色有多个时须提供以区分 |
| `content` | string 或 array | — | string＝文本；array＝`MessageContentPart` 数组 | `M3.1-Flash-Preview`/`M3` 支持文本、图片、视频内容块 |
| `tool_call_id` | string | `role=tool` 时必填 | 上一轮 assistant `tool_calls` 中对应项的 `id` | 其它角色下该字段会被忽略 |
| `content[].type` | string | 必填 | `text`、`image_url`、`video_url` | 内容块类型 |
| `content[].text` | string | `type=text` 时 | — | 文本内容 |
| `content[].image_url.url` | string | `type=image_url` 时必填 | 图片 URL 或 Base64 data URL | 单张图片最大 10 MB |
| `content[].image_url.detail` | string | 默认 `default` | `low`、`default`、`high` | 图片解析分辨率 |
| `content[].image_url.max_long_side_pixel` | integer | 选填，最小 1 | — | 图片最长边像素限制 |
| `content[].video_url.url` | string | `type=video_url` 时必填 | URL、Base64 data URL 或 `mm_file://{file_id}` | URL/Base64 视频最大 50 MB；Files API 视频最大 512 MB |
| `content[].video_url.detail` | string | 默认 `default` | `low`、`default`、`high` | 视频抽帧分辨率 |
| `content[].video_url.fps` | number | 默认 `1` | [0.2, 5] | 视频抽帧频率 |
| `content[].video_url.max_long_side_pixel` | integer | 选填，最小 1 | — | 视频单帧最长边像素限制 |

#### 2.2 MiniMax 原生协议（`POST /v1/text/chatcompletion_v2`）

| 字段 | 类型 | 必填/默认 | 取值 | 说明 |
| :- | :- | :- | :- | :- |
| `model` | string | 必填 | `MiniMax-M3.1-Flash-Preview`、`MiniMax-M3`、`MiniMax-M2.7`、`MiniMax-M2.7-highspeed`、`MiniMax-M2.5`、`MiniMax-M2.5-highspeed`、`MiniMax-M2.1`、`MiniMax-M2` | 以上均为推理模型，为获得最佳体验建议使用流式输出 |
| `messages` | array | 必填 | Message 数组 | 包含对话历史的消息列表 |
| `stream` | boolean | 默认 `false` | `true`、`false` | 是否流式传输 |
| `max_tokens` | integer (int64) | 已弃用，最小 1 | 默认值：`MiniMax-M2` 10240、`MiniMax-M1` 8192、`MiniMax-Text-01` 2048 | 旧参数，请改用 `max_completion_tokens` |
| `max_completion_tokens` | integer (int64) | 选填，最小 1 | 默认值：`MiniMax-M2` 10240、`MiniMax-M1` 8192、`MiniMax-Text-01` 2048 | 生成长度上限 |
| `temperature` | number (double) | 无统一默认（按模型） | 范围 **(0, 1]**（`exclusiveMinimum: 0`，`maximum: 1`） | `MiniMax-M2` 推荐 1.0；`MiniMax-M1` 默认 1.0、推荐 [0.8, 1.0]；`MiniMax-Text-01` 默认 0.1 |
| `top_p` | number (double) | 默认 `0.95` | 范围 (0, 1] | 采样策略；`MiniMax-M2` 推荐 0.95 |
| `tools` | array | 选填 | 元素 `{type:"function", function:{name, description, parameters}}`（`type`、`function` 必填，`function` 内 `name`/`description`/`parameters` 均必填） | 可供模型调用的工具列表 |
| `tool_choice` | string | 默认 `auto` | `none`、`auto` | 控制模型如何使用工具：`none` 不调用、`auto` 自主决定 |
| `response_format` | object | 选填；**当前仅 `MiniMax-Text-01` 支持** | `{type:"json_schema", json_schema:{name, description, schema}}` | 指定模型输出格式（结构化输出，见 §10） |
| `stream_options.include_usage` | boolean | 默认 `false` | `true`、`false` | 为 `true` 时流式响应最后一个数据块包含完整 usage |

原生协议的 `messages` 元素：

| 字段 | 类型 | 必填/默认 | 取值 | 说明 |
| :- | :- | :- | :- | :- |
| `role` | string | 必填 | `system`、`user`、`assistant`、`tool` | `system` 设定模型角色与行为；`user` 用户输入；`assistant` 模型历史回复（也可含工具调用请求）；`tool` 工具返回结果 |
| `content` | string 或 array | 必填 | string＝纯文本；array＝元素 `{type:"text"|"image_url", text, image_url:{url}}` | 图文混合时为 array；`image_url.url` 为图片公网 URL 或 Base64 Data URL |
| `name` | string | 选填 | — | 发送者名称 |
| `tool_calls` | array | `role=assistant` 时出现 | 元素 `{id, type:"function", function:{name, arguments}}` | `arguments` 为包含函数参数的 JSON 字符串 |

> 注意：原生协议 schema 中的 Message 只声明了 `role`/`content`/`name`/`tool_calls`，**未声明** `tool_call_id` 字段（OpenAI 兼容协议里才有）。
> 原生页给出的示例模型还包含更早的型号：`MiniMax-Text-01`、`MiniMax-M1`、`MiniMax-M2`。

#### 2.3 M2-her 对话模型（走 `POST /v1/text/chatcompletion_v2`）

| 字段 | 类型 | 必填/默认 | 取值 | 说明 |
| :- | :- | :- | :- | :- |
| `model` | string | 必填 | `M2-her` | 固定为该值 |
| `messages` | array | 必填 | 见下表角色 | 对话消息列表 |
| `temperature` | number | 默认 `1.0` | — | 温度系数 |
| `top_p` | number | 默认 `0.95` | — | 采样策略 |
| `max_completion_tokens` | integer | — | 上限 2048 | 生成内容最大长度 |
| `stream` | boolean | 默认 `false` | — | 是否流式输出 |

M2-her 支持的消息角色：基础角色 `system`（设定模型角色和行为）、`user`（用户输入）、`assistant`（模型历史回复）；高级角色 `user_system`（设定用户的角色和人设）、`group`（对话的名称，标识对话分组或场景）、`sample_message_user`（示例用户输入）、`sample_message_ai`（示例模型输出）。示例对话建议提供 1-3 个；所有消息（含示例消息）均计入输入 token；M2-her 当前仅支持文本输入，不支持图文混合输入。

### 3. Responses API 字段差异

该站**提供** OpenAI Responses 兼容协议（`POST /v1/responses`，另有 `POST /v1/responses/input_tokens`）。下表只列本家与《通用基线》§0.6 不同的字段与取值；未列出的标准字段语义同基线。

请求字段（`CreateResponseReq`；`required: model`, `input`）：

| 字段 | 类型 | 必填/默认 | 取值 | 说明 |
| :- | :- | :- | :- | :- |
| `model` | string | 必填 | 如 `MiniMax-M3.1-Flash-Preview` | 调用的模型名称 |
| `input` | string 或 array | 必填 | string＝简单文本；array＝`InputItem` 数组 | 对话内容 |
| `instructions` | string | 选填 | — | 系统指令（对应 Chat 的 system 消息） |
| `service_tier` | string | 默认 `standard` | `standard`、`priority` | 同 Chat |
| `max_output_tokens` | integer | 选填 | — | 最大输出 token 数；**推理 token 也计入此上限**，设置过小会导致 `status` 为 `incomplete` 且 `output` 中没有 `message` 项 |
| `temperature` | number (float) | 默认 `1` | (0, 1] | 采样温度（注意与 Chat 的 [0,2] 不同） |
| `top_p` | number (float) | 默认 `0.95` | (0, 1] | 核采样 |
| `stream` | boolean | 默认 `false` | `true` 启用 SSE 流式 | — |
| `tools` | array | 选填 | `{type:"function", name, description, parameters}`（`type`、`name` 必填；**参数键与 Chat 不同：`name` 在顶层，不再是 `function.name`**） | 工具列表；仅 function |
| `tool_choice` | string | 选填 | `none`、`auto` | 工具选择策略（字符串，不是对象） |
| `metadata` | object | 选填 | key-value 均为字符串（`additionalProperties: string`） | 请求元数据 |
| `prompt_cache_key` | string | 选填 | — | Prompt 缓存路由标识（Chat 协议无此字段） |
| `text` | object | 选填 | `text.format.type`：`text`（默认 `text`） | 输出格式控制 |
| `reasoning` | object | 省略时模型默认行为不同 | `reasoning.effort`：`minimal`、`low`、`medium`、`high`、`xhigh`、`max`、`none` | 推理控制，见 §6 |

`input` 数组元素（`InputItem`，由 `type` 决定形态，默认 `message`）：

| 字段 | 类型 | 必填/默认 | 取值 | 说明 |
| :- | :- | :- | :- | :- |
| `type` | string | 默认 `message` | `message`、`function_call`、`function_call_output`、`reasoning` | 条目类型 |
| `role` | string | 仅 `type=message` | `user`、`assistant`、`system`、`developer`、`tool` | 消息角色（比 Chat 多 `developer`、`tool`） |
| `content` | string 或 `ContentPart` 数组 | 仅 `type=message` | — | 消息内容 |
| `call_id` | string | 仅 `function_call` / `function_call_output` | — | 工具调用 ID |
| `name` | string | 仅 `function_call` | — | 函数名 |
| `arguments` | string | 仅 `function_call` | — | 函数参数 JSON 字符串 |
| `output` | string 或 `ContentPart` 数组 | 仅 `function_call_output` | — | 工具返回结果 |
| `summary` | array | 仅 `type=reasoning` | 元素 `{type:"summary_text", text}` | 思维链段落数组 |

`ContentPart`（多模态片段）：

| 字段 | 类型 | 必填/默认 | 取值 | 说明 |
| :- | :- | :- | :- | :- |
| `type` | string | 必填 | `input_text`、`output_text`、`input_image`、`input_video` | 片段类型（与 Chat 的 `text`/`image_url`/`video_url` 命名不同） |
| `text` | string | `input_text`/`output_text` | — | 文本内容 |
| `image_url` | string 或 object | `input_image` | object 必填 `url`；可选 `detail`：`low`/`default`/`high`（默认 `default`） | 图片输入；支持 JPEG、PNG、GIF、WEBP |
| `video_url` | string 或 object | `input_video` | object 必填 `url`；可选 `fps`（默认 `1`，[0.2,5]）、`detail`（`low`/`default`/`high`）、`max_long_side_pixel` | 视频输入；支持 MP4、AVI、MOV、MKV，大文件建议使用 File API |

### 4. Anthropic 兼容端点字段差异

该站**提供** Anthropic Messages 兼容协议：`POST https://api.minimax.cn/anthropic/v1/messages`（base_url 配 `https://api.minimax.cn/anthropic`），另有 `POST /anthropic/v1/messages/count_tokens`。下表只列本家与《通用基线》§0.7 不同的字段与取值；未列出的标准字段语义同基线。

请求字段（`CreateMessageReq`；`required: model`, `messages`）：

| 字段 | 类型 | 必填/默认 | 取值 | 说明 |
| :- | :- | :- | :- | :- |
| `model` | string | 必填 | `MiniMax-M3.1-Flash-Preview`、`MiniMax-M3`、`MiniMax-M2.7`、`MiniMax-M2.7-highspeed`、`MiniMax-M2.5`、`MiniMax-M2.5-highspeed`、`MiniMax-M2.1`、`MiniMax-M2.1-highspeed`、`MiniMax-M2` | `M3.1-Flash-Preview`/`M3` 为多模态模型，原生支持文本、图片和视频输入，并兼容工具调用与 thinking 内容块；M2.7/M2.5/M2.1/M2 系列仅支持文本与工具调用 |
| `messages` | array | 必填 | Message 数组 | 对话历史（内容块形式见下） |
| `system` | string 或 array | 选填 | string＝纯文本系统提示词；array＝`{type:"text", text, cache_control}` 数组 | 设置模型角色与行为；数组形式可携带 `cache_control` |
| `max_tokens` | integer (int64) | 选填，最小 1 | `M3.1-Flash-Preview`/`M3` 推荐 131072（128K）、上限 524288（512K）；其他模型推荐 65536（64K）、上限 204800（200K） | 生成内容长度上限（注意字段名是 `max_tokens`，不是 `max_completion_tokens`） |
| `temperature` | number (double) | 默认 `1` | [0, 2] | 温度系数 |
| `top_p` | number (double) | 默认 `0.95`（M2.x 默认 `0.9`） | [0, 1] | 核采样 |
| `stream` | boolean | 默认 `false` | `true`、`false` | 流式传输 |
| `tools` | array | 选填 | `{name, description, input_schema, cache_control}`（必填 `name`、`input_schema`；schema 键为 `input_schema`，不是 `parameters`） | Anthropic 兼容工具定义 |
| `tool_choice` | object | 选填 | `{type:"auto"}`、`{type:"none"}`（仅这两种） | 工具选择策略 |
| `thinking` | object | 默认随模型不同，未声明统一默认值 | `type`：`disabled`、`adaptive` | 见 §6 |
| `output_config` | object | 选填 | `output_config.effort`：`low`、`medium`、`high`、`xhigh`、`max`（省略时默认 `max`） | 调节 `MiniMax-M3.1-Flash-Preview` 思考深度；其他模型忽略；不支持 `none`（传入 400） |
| `metadata` | object | 选填 | `metadata.user_id`（string） | 建议对 to-C 业务传入 user_id，便于按终端用户聚合限流和计费分析 |
| `service_tier` | string | 默认 `standard` | `standard`、`priority` | 同 Chat |

消息内容块（`RequestContentBlock`，`type` 必填）：`text`（`text`）、`image`（`source`，仅 `M3.1-Flash-Preview`/`M3`）、`video`（`source`，仅 `M3.1-Flash-Preview`/`M3`）、`tool_use`（`id`/`name`/`input`，回带上一轮 assistant 工具调用）、`tool_result`（`tool_use_id`/`content`，工具执行结果）、`thinking`（`thinking`/`signature`，回带上一轮思考内容）、`mid_conv_system`（对话中途插入的系统指令）；任一内容块可带 `cache_control`。

媒体来源 `MediaSource`：`type`（必填，`base64`/`url`）、`media_type`（base64 输入时必填，如 `image/png`、`video/mp4`）、`data`（base64 字节）、`url`（公网 URL，视频还可用 `mm_file://{file_id}`）、`detail`（`low`/`default`/`high`，默认 `default`）、`fps`（默认 `1`，[0.2,5]）、`max_long_side_pixel`。

支持状态与忽略项（Anthropic SDK 兼容性说明原文）：

| 参数 | 支持状态 |
| :- | :- |
| `model`、`max_tokens`、`stream`、`system`、`temperature`、`tool_choice`、`tools`、`top_p`、`thinking`、`output_config.effort`、`metadata`、`service_tier` | 完全支持 |
| `messages` | 部分支持（M2.7/M2.5/M2.1/M2 不支持图片和视频输入） |
| `top_k`、`stop_sequences`、`mcp_servers`、`context_management`、`container` | 忽略（会被忽略） |

### 5. 响应字段与流式结构

#### 5.1 OpenAI 兼容非流式响应（`ChatCompletionResp`）

标准顶层字段（`id`、`choices[].index`、`choices[].message.content`、`choices[].message.role`、`created`、`model`、`usage` 的公共语义）见《通用基线》§0.5；本家特有与额外字段如下：

| 字段 | 类型 | 说明 |
| :- | :- | :- |
| `object` | string | 非流式 `chat.completion`，流式 `chat.completion.chunk` |
| `choices[].finish_reason` | string | `stop`、`length`、`content_filter`、`tool_calls` |
| `choices[].message.reasoning_content` | string | 思考内容；深度思考自适应，简单轮次可能不思考，此时响应中不会出现该字段，读取前请先判空 |
| `choices[].message.tool_calls` | array | 工具调用列表，仅当 `finish_reason` 为 `tool_calls` 时返回 |
| `usage` | object | Token 使用统计（明细见 §5.5） |
| `input_sensitive` | boolean | 输入内容是否命中敏感词；严重违规时接口返回内容违规错误、回复内容为空 |
| `input_sensitive_type` | integer (int64) | 输入命中敏感词类型（1 严重违规；2 色情；3 广告；4 违禁；5 谩骂；6 暴恐；7 其他），`input_sensitive=true` 时返回 |
| `output_sensitive` | boolean | 输出内容是否命中敏感词 |
| `output_sensitive_type` | integer (int64) | 输出命中敏感词类型 |
| `base_resp` | object | `base_resp.status_code`（错误状态码）、`base_resp.status_msg`（错误详情） |

`base_resp.status_code` 取值（文档内列举）：`1000` 未知错误、`1001` 请求超时、`1002` 触发限流、`1004` 鉴权失败、`1008` 余额不足、`1013` 服务内部错误、`1027` 输出内容错误、`1039` Token 超出限制、`2013` 参数错误。

#### 5.2 流式（SSE）chunk

OpenAI 兼容流式 chunk（`ChatCompletionChunk`）。`id`（同一轮所有 chunk 相同）、`choices[].index`、`choices[].delta.role`、`choices[].delta.content`、`created`、`model` 同《通用基线》§0.5；本家特有字段：

| 字段 | 类型 | 说明 |
| :- | :- | :- |
| `choices[].delta.reasoning_content` | string | 增量思考内容；未生成思考内容时不返回 |
| `choices[].finish_reason` | string / null | 未结束时为 `null`；`stop`（自然结束）、`length`（达到 `max_completion_tokens` 上限） |
| `object` | string | 固定 `chat.completion.chunk` |
| `usage` | object | 仅在最后一个 chunk 中返回 |
| `input_sensitive_type` / `output_sensitive` / `output_sensitive_type` | integer / boolean / integer | 敏感词信息 |

原文示例（片段，思考先于正文下发；同一 `id` 贯穿；`delta` 中另有非标字段 `name`、`audio_content`）：

```json
{"id":"066a2db7cb70134c45f5d6443d434c2c","choices":[{"index":0,"delta":{"reasoning_content":"The user","role":"assistant","name":"MiniMax AI","audio_content":""}}],"created":1780153015,"model":"MiniMax-M3.1-Flash-Preview","object":"chat.completion.chunk","usage":null}
{"id":"066a2db7cb70134c45f5d6443d434c2c","choices":[{"index":0,"delta":{"content":"这张图片是一个小女孩的肖像特写照片。","role":"assistant","name":"MiniMax AI","audio_content":""}}],"...":"..."}
{"id":"066a2db7cb70134c45f5d6443d434c2c","choices":[{"finish_reason":"stop","index":0,"delta":{"content":"，色彩温暖，是一张非常经典的儿童肖像摄影作品","role":"assistant","name":"MiniMax AI","audio_content":""}}]}
```

同时刻的文生文工具调用响应（非流式）示例：

```json
{"id":"066b13db03518f86c2e3c9b073c04272","choices":[{"finish_reason":"tool_calls","index":0,"message":{"content":"我来帮你查询旧金山的当前天气。","reasoning_content":"The user is asking ...","role":"assistant","name":"MiniMax AI","tool_calls":[{"id":"call_function_p4iiqtpnh5bj_1","type":"function","function":{"name":"get_weather","arguments":"{\"location\": \"San Francisco, US\"}"},"index":0}],"audio_content":""}}],"created":1780211931,"model":"MiniMax-M3.1-Flash-Preview","object":"chat.completion","usage":{"total_tokens":477,"total_characters":0,"prompt_tokens":411,"completion_tokens":66,"prompt_tokens_details":{"cached_tokens":114}},"input_sensitive":false,"output_sensitive":false,"input_sensitive_type":0,"output_sensitive_type":0,"output_sensitive_int":0,"base_resp":{"status_code":0,"status_msg":""}}
```

原生协议（`/v1/text/chatcompletion_v2`）流式示例的收尾包：最后一包 `object` 为 `chat.completion`（非 chunk）并带 `usage`（`{"total_tokens":73}`），`choices[0].message.content` 为完整文本、`finish_reason: "stop"`。

**结束标记：文档未说明 `data: [DONE]` 之类的终止标记（未抓到）。** 文档给出的流式收尾证据是：最后一个 chunk 的 `finish_reason` 为 `stop`/`length`，且当 `stream_options.include_usage=true` 时最后一个数据块带完整 usage。

#### 5.3 Anthropic 兼容响应与 SSE 事件

非流式（`CreateMessageResp`）：`id`、`type`（固定 `message`）、`role`（固定 `assistant`）、`model`、`content`（内容块数组）、`stop_reason`（`end_turn`、`max_tokens`、`tool_use`）、`usage`。

`ResponseContentBlock`：`type` ∈ `text`（`text`）、`tool_use`（`id`、`name`、`input`）、`thinking`（`thinking`、`signature`）；响应中不会出现 `image`、`video`、`tool_result` 或 `mid_conv_system` 块。`signature` 为 thinking 内容签名，多轮续写时需要原样回带。

流式事件类型（`StreamEvent.type`）：`message_start`（含完整消息元数据，`content` 初始为空数组，`stop_reason`/`stop_sequence` 为 null）、`ping`（心跳）、`content_block_start`（含 `index` 与 `content_block`）、`content_block_delta`（含 `index` 与 `delta`）、`content_block_stop`、`message_delta`（含 `delta.stop_reason` 与 `usage`）、`message_stop`。增量 `delta.type` ∈ `text_delta`（`text`）、`thinking_delta`（`thinking`）、`signature_delta`（`signature`）。

错误响应（也会以 `event: error` SSE 事件下发，body 结构一致）：`{type:"error", request_id, error:{type, message}}`，`error.type` ∈ `invalid_request_error`、`authentication_error`、`permission_error`、`not_found_error`、`request_too_large`、`rate_limit_error`、`api_error`、`overloaded_error`。

#### 5.4 Responses 响应字段

| 字段 | 类型 | 取值/说明 |
| :- | :- | :- |
| `id` | string | 响应 ID |
| `object` | string | 固定 `response` |
| `created_at` | integer | 响应创建时间（Unix 秒） |
| `model` | string | 实际处理请求的模型名称 |
| `status` | string | `completed`、`incomplete`、`failed` |
| `output` | array | 输出项列表（`message` / `reasoning` / `function_call`，见下） |
| `output_text` | string / null | 便利字段，所有文本输出拼接后的结果 |
| `usage` | object | `input_tokens`、`input_tokens_details.cached_tokens`、`output_tokens`、`output_tokens_details.reasoning_tokens`、`total_tokens` |
| `error` | object / null | 仅 `status=failed` 时返回，含 `code`、`message` |
| `incomplete_details` | object / null | 仅 `status=incomplete` 时返回，`reason` ∈ `max_output_tokens`、`content_filter` |
| `parallel_tool_calls` | boolean | 是否支持并行工具调用 |
| `store` | boolean | 响应是否持久化 |
| `truncation` | string | `disabled` |

`output` 项形态：

- Message：`id`（`<id>_msg` 格式）、`type=message`、`status=completed`、`role=assistant`、`content[]{type:"output_text", text, annotations[]}`。
- Reasoning：`id`（`<id>_rs` 格式）、`type=reasoning`、`status=completed`、`summary[]`、`content[]{type:"reasoning_text", text}`。
- Function Call：`id`（`<id>_fc_<index>` 格式）、`type=function_call`、`status=completed`、`call_id`（用于关联 `function_call_output`）、`name`、`arguments`（JSON 字符串）。

Token 估算接口响应：`{object:"response.input_tokens", input_tokens:<integer>}`。

**Responses 协议的 SSE 事件名/chunk 结构：文档只给了 `stream` 开关与流式示例提及，未列出 SSE 事件结构（未抓到）。**

#### 5.5 usage 明细汇总

- OpenAI 兼容：schema 只声明 `total_tokens`；示例中出现 `total_tokens`、`total_characters`、`prompt_tokens`、`completion_tokens`、`prompt_tokens_details.cached_tokens`（缓存命中）、`completion_tokens_details.reasoning_tokens`（原生协议示例中出现）。
- Anthropic 兼容：`input_tokens`、`output_tokens`、`cache_creation_input_tokens`（创建 prompt cache 的输入 token）、`cache_read_input_tokens`（命中 prompt cache 的输入 token）。窗口期内的 `message_start` 事件也带 usage。
- Responses：`input_tokens` + `input_tokens_details.cached_tokens`、`output_tokens` + `output_tokens_details.reasoning_tokens`、`total_tokens`。

### 6. 思考/推理字段

各协议字段名不同（官方给的对照表 + 各页字段说明）：

| 协议 | 思考深度字段 | 思考内容返回位置 | 输出上限字段 |
| :- | :- | :- | :- |
| Anthropic 兼容 | `output_config.effort` | `thinking` 内容块 | `max_tokens` |
| OpenAI 兼容 | `reasoning_effort` | `reasoning_content` 字段 | `max_tokens` / `max_completion_tokens` |
| OpenAI Responses | `reasoning.effort` | `type: "reasoning"` 输出项 | `max_output_tokens` |

思考开关与档位细节：

- 思考深度档位取值 `low`、`medium`、`high`、`xhigh`、`max`（Responses 协议的 `reasoning.effort` 枚举额外含 `minimal` 与 `none`）；`MiniMax-M3.1-Flash-Preview` 省略档位时默认 `max`。
- `MiniMax-M3.1-Flash-Preview`：强制开启 thinking（OpenAI 兼容页与 text-generation 指南都写明"深度思考默认开启，无需额外配置"；Anthropic 兼容页写"传入 `disabled` 会返回 HTTP 400"）。传入 `thinking: {"type":"disabled"}` 或 `effort: "none"` / `reasoning_effort: "none"` 报错原文：

  ```text
  model "MiniMax-M3.1-Flash-Preview" requires adaptive thinking; thinking.type="disabled" (including reasoning.effort=none) is not allowed (2013)
  ```

  另一处单独错误文案：`model "MiniMax-M3.1-Flash-Preview" requires adaptive thinking`。
- `MiniMax-M3`（同一模型在三个协议上的"省略时默认"不同，原文如此）：OpenAI 兼容页写"默认开启 adaptive thinking；传入 `disabled` 会跳过 thinking 直接回答"；Anthropic 兼容页写"省略 `thinking` 时默认关闭；传入 `adaptive` 可开启并返回 thinking 块"；Responses 页写"默认关闭推理；将 `effort` 设为非 `none` 值可开启推理，但不会调节推理深度"。
- M2.x 模型：thinking 无法关闭，传入 `disabled` 会被接收但不生效（仍保持开启）；`reasoning_effort` 非 M3.1 模型会被忽略。
- OpenAI 兼容协议 `reasoning_split`：`true` → 思考内容拆分到 `reasoning_content`；`false` → 思考以 `<think>` 标签保留在 `content` 内（`MiniMax-M3.1-Flash-Preview` 暂不支持设为 `false`）。用 OpenAI SDK 时通过 `extra_body={"reasoning_split": True}` 传递，或使用 `reasoning_details` 字段（`type: "reasoning.text"`、`format: "MiniMax-response-v1"`、`index`、`text`）。
- Interleaved Thinking 要求：多轮 Function Call 必须把完整的模型返回（含 thinking / reasoning_details / tool_calls）原样加入消息历史，否则思维链被打断（OpenAI 原生格式下 `<think>reasoning_content</think>` 不能被修改）。

### 7. 工具调用字段

工具定义（三种协议形态不同）：

| 协议 | 工具定义字段 |
| :- | :- |
| OpenAI 兼容 | `tools[].type`（固定 `function`）、`tools[].function.name`、`tools[].function.description`、`tools[].function.parameters`（JSON Schema） |
| 原生 `/v1/text/chatcompletion_v2` | `tools[].type`（固定 `function`）、`tools[].function.name`、`tools[].function.description`、`tools[].function.parameters`；另有 `tool_choice`：`none` / `auto`（默认 `auto`） |
| Anthropic 兼容 | `tools[].name`、`tools[].description`、`tools[].input_schema`、`tools[].cache_control`；`tool_choice`：`{type:"auto"}` 或 `{type:"none"}` |
| Responses | `tools[].type`（固定 `function`）、`tools[].name`、`tools[].description`、`tools[].parameters`；`tool_choice`：`none` / `auto` |

响应侧工具调用字段：

| 协议 | 字段 |
| :- | :- |
| OpenAI 兼容 | `choices[].message.tool_calls[].id`、`.type`（`function`）、`.function.name`、`.function.arguments`（JSON 格式字符串）、示例中还出现 `.index`；`finish_reason: "tool_calls"`；回带与 `role: tool` 消息的要求见《通用基线》§0.4 |
| 原生协议 | 同上（`message.tool_calls[]` 结构相同） |
| Anthropic 兼容 | `content[].type = "tool_use"`，字段 `id`、`name`、`input`（对象）；工具结果用 `type="tool_result"` 块回带（`tool_use_id`、`content`）；`stop_reason: "tool_use"` |
| Responses | `output[]` 中 `type="function_call"`，字段 `id`、`call_id`、`name`、`arguments`；回带时为 `input[]` 里的 `type="function_call"`（`call_id`/`name`/`arguments`）与 `type="function_call_output"`（`call_id`/`output`） |

服务端工具（Server Tools，Beta）：

- 支持接口：Anthropic Messages API（`/anthropic/v1/messages`）与 OpenAI Responses API（`/v1/responses`）；当前可用工具只有 `web_search`。
- 声明方式：Anthropic 侧 `{"type":"web_search_20250305","name":"web_search"}`；Responses 侧 `{"type":"web_search"}`。
- Anthropic 响应中会按执行顺序返回内容块：`text`、`server_tool_use`（`name` 为 `web_search`，`input.query` 为检索关键词）、`web_search_tool_result`（`content` 为 `web_search_result` 列表，含 `title`、`url`、`page_age`、`content` 等字段）。
- Responses 响应中：`output[]` 含 `web_search_call`（含 `action.type="search"`、`action.query`）与 `message`；`message.content[].annotations` 含 `url_citation`（`title`、`url`、`start_index`、`end_index`、`content`）；顶层 `output_text` 为聚合文本。
- 计费：`web_search` 0.03 元/次。

### 8. 多模态与文件（输入 / 输出完整清单）

本节把「输入模态」「输出模态」「文件上传与管理」「端点与限制」分成四张表。除对话模型的图片/视频输入外，TTS、ASR、图片、视频、音乐都走**独立端点**，不由对话接口产出。

#### 8.1 ① 输入模态总表（文本 / 图片 / 音频 / 视频 / 文件）

| 输入模态 | 支持的协议 / 端点 | 字段名与嵌套结构 | 传法（URL / base64 / file_id） | 格式白名单 | 体积与分辨率 / 时长 / 张数上限 | 哪些模型支持 |
| :- | :- | :- | :- | :- | :- | :- |
| 文本 | 四种对话协议：`POST /v1/chat/completions`、`POST /v1/text/chatcompletion_v2`、`POST /anthropic/v1/messages`、`POST /v1/responses` | Chat / 原生：`messages[].content` 为 string；Anthropic：`messages[].content[]` 中 `{"type":"text","text"}`；Responses：`input` 为 string，或 `input[].content[]` 中 `{"type":"input_text","text"}` | 直接写在字段中 | 纯文本 | 受上下文窗口（§12）与 `max_completion_tokens` / `max_tokens` / `max_output_tokens` 约束 | 全部语言模型，含 `M2-her`（M2-her 仅文本输入，不支持图文混合） |
| 图片（对话模型理解） | 同上四种协议 | OpenAI 兼容 `messages[].content[]`：`{"type":"image_url","image_url":{"url","detail","max_long_side_pixel"}}`；原生 `messages[].content[]`：`{"type":"image_url","image_url":{"url"}}`（原生 schema 只声明 `text` 与 `image_url`，无 `video_url`）；Anthropic `messages[].content[]`：`{"type":"image","source":MediaSource}`；Responses `input[].content[]`：`{"type":"input_image","image_url"}` | `url` = 公网 URL 或 Base64 data URL；Anthropic 侧 `source.type` = `base64` / `url`（base64 时 `media_type` 必填、`data` 为 base64 字节） | JPEG（.jpg/.jpeg，image/jpeg）、PNG（.png，image/png）、GIF（.gif，image/gif）、WEBP（.webp，image/webp） | 单张图片 ≤ 10 MB；请求体 ≤ 64 MB；`detail` = `low` / `default` / `high`（默认 `default`）；`max_long_side_pixel` 选填（最小 1）；单张图片粗略 token 用量：`low` 通常几百 token、最高约 600，`default` 通常约 1k-3k、最高约 5k，`high` 通常数千、最高约 15k+ | 仅 `MiniMax-M3.1-Flash-Preview`、`MiniMax-M3`；M2.7 / M2.5 / M2.1 / M2 系列不支持，`M2-her` 不支持 |
| 视频（对话模型理解） | OpenAI 兼容 / Anthropic 兼容 / Responses（原生协议 schema 未声明视频内容块） | OpenAI 兼容：`{"type":"video_url","video_url":{"url","detail","fps","max_long_side_pixel"}}`；Anthropic：`{"type":"video","source":MediaSource}`；Responses：`{"type":"input_video","video_url"}` | 公网 URL、Base64 data URL，或 `mm_file://{file_id}`（需先经 `POST /v1/files/upload` 上传） | MP4（video/mp4）、AVI（video/avi 或 video/x-msvideo）、MOV（URL 传入时对象存储需设 `Content-Type: video/quicktime`，base64 用 `data:video/mov;base64,<BASE64_ENCODING>`）、MKV（video/x-matroska） | URL / Base64 视频 ≤ 50 MB；Files API 视频 ≤ 512 MB；请求体 ≤ 64 MB；`fps` 默认 1、范围 [0.2, 5]（越高越敏感、token 花费高、速度慢）；`detail` 控制抽帧分辨率，默认 `default` | 仅 `MiniMax-M3.1-Flash-Preview`、`MiniMax-M3` |
| 音频（作为输入） | 对话协议**不支持音频输入**（原文："当前不支持音频输入"）；音频只作为独立端点的输入：视频生成 V2 `content[].audio_url`、音乐生成与翻唱前处理 `audio_url`/`audio_base64`、语音识别 multipart `file`、文件上传 `purpose=voice_clone` / `prompt_audio` | 视频生成：`{"type":"audio_url","audio_url":{"url"},"role":"reference_audio"}`；音乐与翻唱前处理：顶层 `audio_url` 或 `audio_base64`（二选一）；ASR：multipart 字段 `file` | 视频生成：公网 URL、`mm_file://{file_id}`、`data:audio/<格式>;base64,<Base64>` data URI；音乐：URL 或 Base64；ASR / 文件上传：本地文件（multipart 上传） | 视频生成参考音频 WAV、MP3；音乐参考音频 mp3、wav、flac 等常见格式；ASR：wav / aiff / flac / alac(m4a) / mp3 / aac / opus / ogg（不支持无容器裸 PCM）；文件上传：mp3、m4a、wav | 视频生成参考音频单文件 ≤ 15 MB、≤ 3 个、单段 [2, 15] s 且总时长 ≤ 15 s；音乐参考音频 6 秒–6 分钟、≤ 50 MB；ASR ≤ 50 MB 且 ≤ 500 秒（超 500 s 返回 400 不截断，超 50 MB 返回 413） | 视频生成 `MiniMax-H3`、`MiniMax-H3-Max`；TTS / ASR / 音乐见 8.2、8.3 |
| 文件（file_id 引用） | 上传 `POST /v1/files/upload`（multipart/form-data）；对话与生成侧只写引用 | 引用写法 `mm_file://{file_id}`：对话视频块 `content[].video_url.url`，视频生成 `content[].image_url.url` / `content[].video_url.url` / `content[].audio_url.url`；异步语音合成用整数 `text_file_id` | file_id（int64），上传后由响应返回 | 由 `purpose` 决定，见 8.3 | 见 8.3 | `video_understanding` 供 `MiniMax-M3.1-Flash-Preview` / `MiniMax-M3` 对话理解；`video_generation_input` 供 `MiniMax-H3` / `MiniMax-H3-Max`；`t2a_async_input` 供 `POST /v1/t2a_async_v2`；`voice_clone` / `prompt_audio` 供音色复刻 |

补充：AI SDK 兼容（`vercel-minimax-ai-provider`）不支持图像与文件输入（AI SDK 页原文）。

#### 8.2 ② 输出模态总表（TTS / 语音识别 / 图片 / 视频 / 音乐）

| 输出模态 | 端点 | 请求字段（关键） | 返回形式（URL / base64 / 异步任务 id / 轮询方式） | 时长与尺寸限制 | 是否流式 | 归属 |
| :- | :- | :- | :- | :- | :- | :- |
| 对话文本 + 思考 + 工具调用 | 四种对话协议（Chat / 原生 / Anthropic / Responses） | `stream`、`stream_options.include_usage`、`max_completion_tokens`（Anthropic 侧为 `max_tokens`，Responses 侧为 `max_output_tokens`） | Chat：`choices[].message.content` / `reasoning_content` / `tool_calls`；Anthropic：`content[]` 的 `text` / `thinking` / `tool_use` 块；Responses：`output[]` 的 `message` / `reasoning` / `function_call` | 输出长度上限见 §12 | 是（`stream=true`；Chat 与 Anthropic 的 SSE 结构见 §5.2、§5.3，Responses 的 SSE 事件结构文档未给出） | **对话模型直接产出**。文档**未给出**对话模型直接产出图片或音频：OpenAI 兼容响应示例里出现 `audio_content` 字段，但 schema 未声明、正文无任何说明 |
| 语音合成 TTS（同步 HTTP） | `POST /v1/t2a_v2` | `model`（`speech-2.8-hd` / `speech-2.8-turbo` / `speech-2.6-hd` / `speech-2.6-turbo` / `speech-02-hd` / `speech-02-turbo` / `speech-01-hd` / `speech-01-turbo`）、`text`、`stream`、`stream_options.exclude_aggregated_audio`、`voice_setting{voice_id, speed[0.5,2] 默认 1, vol(0,10] 默认 1, pitch[-12,12] 默认 0, emotion(happy/sad/angry/fearful/disgusted/surprised/calm/fluent/whisper), text_normalization, latex_read}`、`audio_setting{sample_rate, bitrate, format, channel, force_cbr}`、`pronunciation_dict.tone[]`、`timbre_weights[]{voice_id, weight[1,100]，最多 4 种}`、`language_boost`、`voice_modify{pitch, intensity, timbre, sound_effects}`、`subtitle_enable`、`subtitle_type`、`output_format`、`aigc_watermark` | `data.audio`（hex 编码；`output_format=url` 时返回 URL，有效期 24 小时）、`data.status`（1 合成中 / 2 结束）、`data.subtitle_file`（仅非流式）/ `data.subtitle` / `data.subtitles`（流式）、`extra_info{audio_length, audio_sample_rate, audio_size, bitrate, audio_format, audio_channel, invisible_character_ratio, usage_characters, usage_voice_count, word_count}`、`trace_id`、`base_resp` | `text` < 10000 字符（> 3000 字符推荐流式输出）；`audio_setting.sample_rate` 8000/16000/22050/24000/32000/44100；`bitrate` 32000/64000/128000/256000（仅 mp3 生效）；`format` mp3/pcm/flac/wav/pcmu_raw/pcmu_wav/opus；`channel` 1/2；`subtitle_type` sentence/word/word_streaming | 是（`stream=true` 时以 SSE 逐块下发 hex 音频；流式下 `output_format` 仅支持 `hex`，`aigc_watermark` 仅非流式生效） | **独立端点** |
| 语音合成 TTS（WebSocket 同步 / 双向流式） | `wss://api.minimax.cn/ws/v1/t2a_v2`；双向流式 `/ws/v1/t2a_v2_bidi` | 上行事件 `task_start`（`model`、`voice_setting` 等与 HTTP 版一致，双向模式还可带 `session_id`）、`task_continue`（`text` < 10000 字符）、`task_finish`；双向额外支持 `task_cancel`（打断）与 `task_flush`（催出残留但不结束会话） | 下行事件 `connected_success`、`task_started`、`task_continued`（`data.audio` 为 hex 片段、`data.subtitle`、`extra_info`、`is_final`）、`task_finished`、`task_failed`；双向额外有 `sentence_start` / `sentence_end` / `task_canceled` / `task_flushed` | 单条 `task_continue` 文本 < 10000 字符；字幕 `sentence` / `word` / `word_streaming`，流式只经 `data.subtitle` 下发、不提供字幕文件；空闲约 120 秒断连（`2201`） | 是（连接内持续流式） | **独立端点** |
| 语音合成 TTS（异步长文本） | 创建 `POST /v1/t2a_async_v2`；查询 `GET /v1/query/t2a_async_query_v2`（query 参数 `task_id`，文档注明该 API 每秒最多查询 10 次） | 创建：`model`（同上八种）、`text`（最长 5 万字符）或 `text_file_id`（二选一必填）、`voice_setting`、`audio_setting`、`pronunciation_dict`、`language_boost`、`voice_modify`、`aigc_watermark` | 创建返回 `task_id`、`file_id`、`task_token`、`usage_characters`；查询返回 `task_id`、`status`（`processing` / `success` / `failed` / `expired`）、`file_id`；产物再用 `GET /v1/files/retrieve`（传 `file_id`）取 `download_url`，或用 `GET /v1/files/retrieve_content` 直接下载 | `text_file_id` 单个文件 < 100 万字符，支持 txt / zip（zip 内为同格式 txt 或 json，json 支持 `title` / `content` / `extra` 三字段）；下载 URL 自生成起 9 小时（32,400 秒）内有效 | 否（异步任务 + 轮询） | **独立端点** |
| 语音识别 ASR | `POST /v1/speech_to_text`（multipart/form-data；请求头可选 `language`，BCP-47 标签，支持 zh/yue/en/ja/ko/th/vi/id/ms/fil/ar/tr/fr/de/es/it/pt/pl/ru/uk，不传则混合语言识别） | `model`（仅 `asr-1.0`）、`file`、`response_format`、`timestamp_level`、`stream` | `json`：`text` + `duration`；`verbose_json`：再含 `segments[]{id, start, end, speaker, text}` 与 `n_speakers`；`srt` / `vtt`：字幕文本（响应类型分别为 `text/plain`、`text/vtt`）；另有 `trace_id` | 音频时长 ≤ 500 秒（超限返回 400，不截断）、大小 ≤ 50 MB（超限 413）；计费以 `duration` 为准 | 是（`stream=true` 时以 SSE 推 `data: {"index","delta","finish"}`，终止事件带 `duration`；此时 `response_format` 仅支持 `json`） | **独立端点** |
| 图片生成 | `POST /v1/image_generation`（文生图与图生图共用同一端点） | 通用：`model`（`image-01` / `image-01-live`）、`prompt`（≤ 1500 字符）、`aspect_ratio`、`width` / `height`（仅 image-01）、`response_format`、`seed`、`n`、`prompt_optimizer`、`aigc_watermark`；`style{style_type, style_weight}` 仅 `image-01-live`；图生图另加 `subject_reference[]{type:"character", image_file}` | `data.image_urls`（`response_format=url`，URL 有效期 24 小时）或 `data.image_base64`（`response_format=base64`）；`metadata.success_count` / `failed_count`、`id`、`base_resp` | `n` ∈ [1, 9]，默认 1；`aspect_ratio` 八档：1:1 (1024x1024)、16:9 (1280x720)、4:3 (1152x864)、3:2 (1248x832)、2:3 (832x1248)、3:4 (864x1152)、9:16 (720x1280)、21:9 (1344x576，仅 image-01)；`width` / `height` 需同时设置、∈ [512, 2048] 且为 8 的倍数，与 `aspect_ratio` 同设时以 `aspect_ratio` 优先；参考图每次仅支持 1 张、JPG/JPEG/PNG、< 10 MB、建议单人正面照 | 否（无 `stream` 字段，同步返回） | **独立端点** |
| 视频生成 | 创建 `POST /v2/video_generation`；查询 `GET /v2/query/video_generation/{task_id}`；列表 `GET /v2/query/video_generation` | `model`（`MiniMax-H3` / `MiniMax-H3-Max`）、`content[]`（元素 `{type:"text" / "image_url" / "video_url" / "audio_url", text, image_url{url}, video_url{url}, audio_url{url}, role:"first_frame" / "last_frame" / "reference_image" / "reference_video" / "reference_audio"}`）、`resolution`、`duration`（必填整数）、`ratio`、`extra.prompt_expansion_mode`（仅 H3-Max）、`callback_url`、`aigc_watermark` | 创建成功返回 `task_id`（异步）；查询返回 `task{id, model, status, created_at, updated_at, content.url, content.prompt, resolution, duration, ratio, task_type, modality, usage, error}`，`status` ∈ `queued` / `running` / `succeeded` / `failed` / `cancelled`，成功后取 `content.url` 下载；也可配 `callback_url` 接收状态推送 | 分辨率：H3 为 768P / 2K，H3-Max 为 480P / 768P（默认 768P，不支持 2K）；时长：H3 为 4–15 秒，H3-Max 为 5–15 秒；`ratio` 默认 `adaptive`、可选 21:9 / 16:9 / 4:3 / 1:1 / 3:4 / 9:16（文生视频必填且不能为 `adaptive`，图生视频恒为 `adaptive`）；`content` 必含一个非空 `text`，单个 `text` ≤ 7000 字符；请求体总大小 ≤ 64 MB；仅支持查询最近 7 天（`[T-7天, T)`）内的任务，推荐轮询间隔 10 秒；产物下载链接有时效 | 否（异步任务 + 轮询 / 回调） | **独立端点** |
| 音乐生成（含歌词生成 / 翻唱前处理） | 音乐 `POST /v1/music_generation`；歌词 `POST /v1/lyrics_generation`；翻唱前处理 `POST /v1/music_cover_preprocess` | 音乐：`model`（`music-3.0` / `music-2.6` / `music-cover` / `music-3.0-free` / `music-2.6-free` / `music-cover-free`）、`prompt`、`lyrics`、`stream`、`output_format`、`audio_setting{sample_rate, bitrate, format}`、`aigc_watermark`、`lyrics_optimizer`、`is_instrumental`、`audio_url` / `audio_base64` / `cover_feature_id`（三者互斥，仅 music-cover 系列；翻唱前处理只收 `model=music-cover` 与 `audio_url` 或 `audio_base64`）；歌词：`mode`（`write_full_song` / `edit`）、`prompt`、`lyrics`、`title` | 音乐：`data.audio`（`output_format=hex` 时的 16 进制字符串）、`data.status`（1 合成中 / 2 已完成）、`base_resp`；歌词：`song_title`、`style_tags`、`lyrics`；翻唱前处理：`cover_feature_id`、`formatted_lyrics`、`structure_result`、`audio_duration` | 音乐 `prompt`：纯音乐必填且 ∈ [1, 2000] 字符、非纯音乐 ∈ [0, 2000]、翻唱系列必填且 ∈ [10, 300]；`lyrics`：非纯音乐必填 ∈ [1, 3500]、纯音乐非必填、翻唱系列 ∈ [10, 1000]；`audio_setting` 采样率 16000/24000/32000/44100、比特率 32000/64000/128000/256000、格式 mp3/wav/pcm；参考音频 6 秒至 6 分钟、≤ 50 MB；`cover_feature_id` 有效期 24 小时；歌词 `prompt` ≤ 2000 字符、`lyrics` ≤ 3500 字符 | 是（音乐 `stream` 默认 `false`，`stream=true` 时仅支持 `output_format=hex`；`output_format=url` 的有效期 24 小时） | **独立端点** |

补充（均为文档原文）：

- 音乐 `output_format=url` 时返回什么字段：**文档未给出**——`MusicData` 只声明了 `status` 与 hex 形式的 `audio`；此外响应示例里出现过 `trace_id` / `extra_info` / `music_duration` / `analysis_info`，但 schema 未声明这些字段。
- Music API 服务调整：自 2026 年 8 月 20 日起付费接口（音乐生成、歌词生成）不再面向新用户提供服务，历史付费用户可继续使用现有 API 服务；免费音乐生成接口（`music-3.0-free`、`music-2.6-free`、`music-cover-free`）停止服务。
- 视频生成 V2 的输入组合：文生视频（仅 `text`）、图生视频-首帧 / 尾帧 / 首尾帧（`role=first_frame` / `last_frame`）、多模态参考生视频（`reference_image` / `reference_video` / `reference_audio`）；**图生视频与多模态参考互斥**（出现一组 role 就不能出现另一组）。图片 ≤ 30 MB、宽高 [256, 5760]、长宽比 [0.4, 2.5]、首帧 ≤ 1 / 尾帧 ≤ 1 / 参考图 ≤ 9；参考视频 MP4/MOV、≤ 50 MB、≤ 3 个、单段 [2, 15] s 且总时长 ≤ 15 s、帧率 [23.976, 60]；参考音频 WAV/MP3、≤ 15 MB、≤ 3 个；混合输入总上限 12 个文件（指南页原文）。
- 两页对 H3-Max 生成方式的描述不一致：`/video-generation-v2-create` 与视频生成指南都写 H3-Max 支持多模态参考生视频（参考图 / 参考视频 / 参考音频），而概览页 `/guides/models-intro` 写 H3 Max "仅支持文生 / 图生（首帧、尾帧）"。

#### 8.3 ③ 文件上传与管理 API

上传端点 `POST /v1/files/upload`（`Content-Type: multipart/form-data`）：

| 字段 | 类型 | 必填 | 取值 / 说明 |
| :- | :- | :- | :- |
| `purpose` | string | 必填 | `voice_clone`：快速复刻原始文件（支持 mp3、m4a、wav）；`prompt_audio`：音色复刻的示例音频（支持 mp3、m4a、wav）；`t2a_async_input`：异步长文本语音合成的文本文件（支持 text、zip），在 `POST /v1/t2a_async_v2` 中以 `text_file_id` 传入；`video_understanding`：多模态理解使用的视频文件（支持 MP4、AVI、MOV、MKV），在对话请求中以 `mm_file://{file_id}` 引用，最长保存 7 天；`video_generation_input`：视频生成的输入素材（首帧图 / 参考图 / 参考视频 / 参考音频），在生成请求 `content` 的 `url` 字段以 `mm_file://{file_id}` 引用，有效期 7 天（过期后发起生成返回 file expired，需重新上传），上传即校验规格，不合格返回 400 且不留存，heic/heif 宽高由服务端解析、无需客户端转码 |
| `file` | string (binary) | 必填 | 需要上传的文件，填写文件的路径地址 |

各 `purpose` 的格式与体积限制：

| `purpose` | 格式 | 单文件上限 |
| :- | :- | :- |
| `voice_clone` / `prompt_audio` | mp3、m4a、wav | 文档未给出 |
| `t2a_async_input` | text、zip | 文档未给出（下游 `text_file_id` 侧单个文件 < 100 万字符） |
| `video_understanding` | MP4、AVI、MOV、MKV | 文档未给出（对话侧引用时视频 ≤ 512 MB） |
| `video_generation_input` | 图片 jpg / jpeg / png / webp / heic / heif；参考视频 mp4 / mov；参考音频 wav / mp3 | 图片 ≤ 30 MB；参考视频 ≤ 50 MB；参考音频 ≤ 15 MB |

上传响应：`file.file_id`（int64）、`file.bytes`、`file.created_at`、`file.filename`、`file.purpose`、`base_resp.status_code`、`base_resp.status_msg`（状态码枚举：1000 / 1001 / 1002 / 1004 / 1008 / 1013 / 1026 / 1027 / 1039 / 2013）。

**id 怎么在对话里引用**：

- 对话视频理解：先以 `purpose=video_understanding` 上传，再把返回的 `file_id` 写成 `mm_file://{file_id}`——OpenAI 兼容协议放进 `content[].video_url.url`，Anthropic 兼容协议放进 `video` 内容块的 `source.url`。
- 视频生成：以 `purpose=video_generation_input` 上传后，把 `mm_file://{file_id}` 写进 `content[]` 的 `image_url.url` / `video_url.url` / `audio_url.url`。
- 异步语音合成：`purpose=t2a_async_input` 上传后，把整数 `file_id` 作为 `text_file_id` 传入。
- **图片上传的 `purpose`：文档未给出**（`purpose` 枚举里只有上面 5 个），因此对话图片只能以公网 URL 或 Base64 data URL 传入。

管理端点：

| 端点 | 方法 | 参数 | 返回 |
| :- | :- | :- | :- |
| `/v1/files/list` | GET | query `purpose`（必填，取值 `voice_clone` / `prompt_audio` / `t2a_async_input` / `video_generation_input`） | `files[]{file_id, bytes, created_at, filename, purpose}`、`base_resp` |
| `/v1/files/retrieve` | GET | query `file_id`（必填，int64；文档说明本接口支持「视频生成中，查询视频任务状态接口获得的 file_id」与「异步语音合成中，查询语音生成任务状态接口获得的 file_id」） | `file{file_id, bytes, created_at, filename, purpose, download_url}`、`base_resp`（`download_url` 仅出现在示例中，`FileObject` schema 未声明） |
| `/v1/files/retrieve_content` | GET | query `file_id`（必填，int64） | 二进制文件内容 |
| `/v1/files/delete` | POST | body `file_id`、`purpose`（取值 `voice_clone` / `prompt_audio` / `t2a_async` / `t2a_async_input` / `video_generation`；注意与上传枚举不同，多出 `t2a_async` 与 `video_generation`、少了 `video_understanding` 与 `video_generation_input`） | `file_id`、`base_resp` |

#### 8.4 ④ 相关端点与限制汇总

| 端点 | 方法与协议 | 用途 | 关键限制 |
| :- | :- | :- | :- |
| `POST /v1/files/upload` | HTTP multipart/form-data | 上传多模态 / 语音素材，取 `file_id` | 必填 `purpose`、`file`；`video_generation_input` 单项上限见 8.3 |
| `GET /v1/files/list` | HTTP | 按 `purpose` 列出文件 | `purpose` 必填 |
| `GET /v1/files/retrieve` | HTTP | 按 `file_id` 取文件元信息（含 `download_url`） | `file_id` 必填 |
| `GET /v1/files/retrieve_content` | HTTP | 按 `file_id` 直接下载文件内容 | `file_id` 必填 |
| `POST /v1/files/delete` | HTTP JSON | 删除文件 | 必填 `file_id`、`purpose` |
| `POST /v1/t2a_v2` | HTTP JSON / SSE | 同步语音合成 | `text` < 10000 字符；流式下 `output_format` 仅 hex |
| `/ws/v1/t2a_v2` | WebSocket | 客户端按句发送文本、流式返回音频 | 单条 `task_continue` < 10000 字符；空闲约 120 秒断连 |
| `/ws/v1/t2a_v2_bidi` | WebSocket | 任意粒度（含逐字）发送文本、服务端攒句 | 单条 `task_continue` > 10000 字符返回 `2204`；`2205` 表示积压、重发即可 |
| `POST /v1/t2a_async_v2` | HTTP JSON | 创建异步长文本语音合成任务 | `text` ≤ 5 万字符，或 `text_file_id` < 100 万字符（txt / zip） |
| `GET /v1/query/t2a_async_query_v2` | HTTP | 查询异步语音合成任务 | `task_id` 必填；每秒最多查询 10 次；下载 URL 9 小时有效 |
| `POST /v1/speech_to_text` | HTTP multipart/form-data | 语音识别转文本 | 音频 ≤ 500 秒、≤ 50 MB；`stream=true` 时 `response_format` 仅 `json` |
| `POST /v1/image_generation` | HTTP JSON | 文生图 / 图生图 | `prompt` ≤ 1500 字符；`n` ∈ [1, 9]；URL 有效期 24 小时 |
| `POST /v2/video_generation` | HTTP JSON | 创建视频生成任务（异步） | `content` 必含非空 `text` 且 ≤ 7000 字符；请求体 ≤ 64 MB；素材上限见 8.2 |
| `GET /v2/query/video_generation/{task_id}` | HTTP | 查询单个视频生成 / H3-Context-IR / 视频再生成任务 | 仅最近 7 天内的任务；成功取 `content.url`（限时链接） |
| `GET /v2/query/video_generation` | HTTP | 分页查询任务列表 | 仅最近 7 天；可选 `page_num`、`page_size`、`filter.*`；建议轮询间隔 10 秒 |
| `POST /v1/music_generation` | HTTP JSON | 歌曲生成（含翻唱） | `prompt` ≤ 2000 字符、`lyrics` ≤ 3500 字符（翻唱系列各为 ≤ 300 / ≤ 1000）；`stream=true` 时仅 hex |
| `POST /v1/lyrics_generation` | HTTP JSON | 歌词生成 / 编辑续写 | `prompt` ≤ 2000 字符、`lyrics` ≤ 3500 字符；必填 `mode` |
| `POST /v1/music_cover_preprocess` | HTTP JSON | 翻唱前处理，产出 `cover_feature_id` | `model` 固定 `music-cover`；参考音频 6 秒–6 分钟、≤ 50 MB；`cover_feature_id` 有效期 24 小时 |

其它限制：各端点的 RPM / TPM / CONN 限流数值见 §11.3（语言模型、TTS、Voice Cloning、Speech to Text、Image Generation、Music Generation、Video Generation）；错误码见 §11.1（含 ASR 的 1043、语音的 1042 等）。

### 9. 缓存与成本字段

#### 9.1 两种缓存模式

| | Prompt 缓存（被动缓存，自动） | Anthropic 主动缓存 |
| :- | :- | :- |
| 使用方式 | 自动识别重复内容并缓存，无需改接口调用方式 | 在 API 中显式设置 `cache_control` |
| 计费方式 | 命中缓存的 token 以优惠价计费；写入缓存的部分**无额外计费** | 命中缓存的 token 以优惠价计费；首次写入缓存的 token **额外计费** |
| 缓存过期 | 根据系统负载自动调整过期时间 | 5 分钟过期，持续使用会自动续期 |
| 支持模型 | `MiniMax-M3`、`MiniMax-M2.7` 系列、`MiniMax-M2.5` 系列、`MiniMax-M2.1` 系列 | `MiniMax-M2.7` 系列、`MiniMax-M2.5` 系列、`MiniMax-M2.1` 系列、`MiniMax-M2` 系列 |

被动缓存注意项：适用于包含 512 个及以上输入 token 的调用；前缀匹配，按「工具定义 → 系统提示词 → 历史对话内容」顺序构建；建议静态内容放前、动态用户信息放后；通过 usage 中的缓存 token 监测效果。对 `MiniMax-M3`，请求输入 tokens 大于 512k 时适用长上下文价格（输入 tokens 包含缓存命中 tokens）。

主动缓存字段与规则：`cache_control: {"type": "ephemeral"}`（可标在 `tools`、`system` 内容块、`messages.content` 内容块、`tool_use`/`tool_result` 块上）；缓存前缀顺序 `tools` → `system` → `messages`；向前顺序检查命中，每个显式断点前最多回溯 20 个块；一次调用最多支持 4 个 `cache_control`（超过时只取从后向前最近的 4 个）；生命周期 5 分钟，每次命中自动刷新且不额外收费。

缓存相关 usage 字段与含义：

- `cache_creation_input_tokens`：创建新缓存条目时写入缓存的 token 数量。
- `cache_read_input_tokens`：本次请求从缓存中检索的 token 数量。
- `input_tokens`：未从缓存读取或用于创建缓存的输入 token 数量（即最后一个缓存断点之后的 token）。
- `total_input_tokens = cache_read_input_tokens + cache_creation_input_tokens + input_tokens`。

OpenAI 兼容协议的缓存字段是 `usage.prompt_tokens_details.cached_tokens`；Responses 协议是 `usage.input_tokens_details.cached_tokens`。

#### 9.2 成本字段（价格照抄）

MiniMax-M3（标准档；原文标注"永久五折"，划掉价为原价）：

| 档位 | 输入（元/百万 tokens） | 输出（元/百万 tokens） | 缓存读取（元/百万 tokens） |
| :- | :- | :- | :- |
| ≤ 512k 输入 tokens | ~~4.20~~ 2.10 | ~~16.80~~ 8.40 | ~~0.84~~ 0.42 |
| > 512k 输入 tokens | ~~8.40~~ 4.20 | ~~33.60~~ 16.80 | ~~1.68~~ 0.84 |

MiniMax-M3（优先档，`service_tier=priority`，按标准价 1.5 倍计费）：≤512k：~~6.30~~ 3.15 / ~~25.20~~ 12.60 / ~~1.26~~ 0.63；>512k：~~12.60~~ 6.30 / ~~50.40~~ 25.20 / ~~2.52~~ 1.26。

按量计费价目（含缓存写入列）：

| 模型 | 输入（元/百万 tokens） | 输出（元/百万 tokens） | 缓存读取（元/百万 tokens） | 缓存写入（元/百万 tokens） |
| :- | :- | :- | :- | :- |
| MiniMax-M2.7 | 2.1 | 8.4 | 0.42 | 2.625 |
| MiniMax-M2.7-highspeed | 4.2 | 16.8 | 0.42 | 2.625 |
| MiniMax-M2.5 / MiniMax-M2.5-highspeed / MiniMax-M2.1 / MiniMax-M2.1-highspeed / MiniMax-M2（历史模型） | 2.1 | 8.4 | 0.21 | 2.625 |

主动缓存价目表（页面另给一份，含 highspeed 的输出价 16.8）：MiniMax-M2.7 / M2.5 / M2.1 / M2 输入 2.1、输出 8.4（highspeed 输出 16.8）、缓存读取 0.42（M2.7 系列）/ 0.21（M2.5、M2.1、M2）、缓存写入 2.625。

计费口径补充：计费项是 token 数；Token 与字符比估算 1600 中文字符约消耗 1000 tokens；`total_characters` 字段出现在 usage 示例中（字符数）。

计费示例（原文）：MiniMax-M3 输入 ≤512k 标准价 输入 4.20 元/1M、输出 16.80 元/1M、缓存命中 0.84 元/1M；总输入 50000、缓存命中 45000、新增输入 5000、输出 1000 → 新增输入 0.021 元 + 缓存 0.0378 元 + 输出 0.0168 元 = 0.0756 元，相比无缓存 0.2268 元节省约 66.7%。

### 10. 特殊模式

**结构化输出**：仅原生接口 `POST /v1/text/chatcompletion_v2` 的 `response_format` 支持，且"当前仅 `MiniMax-Text-01` 支持此参数"。

```json
{
  "response_format": {
    "type": "json_schema",
    "json_schema": {
      "name": "格式名称（a-z/A-Z/0-9，最长 64 字符，pattern ^\\w+$）",
      "description": "格式的描述",
      "schema": { "type": "object", "properties": { ... }, "required": [ ... ] }
    }
  }
}
```

约束原文：`json_schema` 必填 `name`、`schema`；`schema.type` 应为 `object`，`schema` 必填 `type`、`properties`；支持的类型包括 String、Array、Enum、Number、Integer、Object、Boolean；**使用结构化输出时所有字段或函数参数都必须指定为 required**。

**思考内容拆分（OpenAI 兼容）**：`reasoning_split`（见 §6），`false` 时思考以 `<think>` 标签留在 `content` 内，需自行解析。

**Responses 协议的输出格式控制**：`text.format.type` 只有 `text`（默认 `text`）一个枚举值。

**logprobs**：文档中未找到 logprobs 相关字段（查过 `/docs/api-reference/text-chat-openai`、`/docs/api-reference/text-post`、`/docs/api-reference/responses-create`、`/docs/api-reference/text-openai-api`、`/docs/api-reference/text-anthropic-api`）。另外 OpenAI SDK 页明确写"部分 OpenAI 参数（如 `presence_penalty`、`frequency_penalty`、`logit_bias` 等）会被忽略"、"`n` 参数仅支持值为 1"、"旧版的 `function_call` 已废弃，请使用 `tools` 参数"。

**前缀续写**：文档中未找到前缀续写（prefix continuation）相关页面与字段。已核对全站索引 `/docs/llms.txt` 与 `/docs/sitemap.xml` 中全部 text 相关页面，均无该能力页。

**对话历史/多轮**：不注入瞬态字段，靠客户端在 `messages`/`input` 中完整回带历史；M2-her 额外支持 `user_system` / `group` / `sample_message_user` / `sample_message_ai` 角色（见 §2.3）。

**上下文缓存**：见 §9。

### 11. 错误码与限流

#### 11.1 内部业务错误码（`base_resp.status_code` / 文档错误码表）

| 码 | HTTP | 含义 | 建议动作 |
| :- | :- | :- | :- |
| 0 | 200 | 请求成功 | — |
| 1000 | 500 | 未知错误/系统默认错误 | 请稍后再试 |
| 1001 | — | 请求超时 | 请稍后再试 |
| 1002 | 429 | 请求频率超限（触发限流） | 请稍后再试 |
| 1004 | 401 | 未授权/Token 不匹配/Cookie 缺失（鉴权失败） | 请检查 API Key |
| 1008 | 402 | 余额不足 | 请检查账户余额 |
| 1013 | — | 服务内部错误 | 请稍后再试 |
| 1024 | — | 内部错误 | 请稍后再试 |
| 1026 | 422 | 输入内容涉敏 | 请调整输入内容 |
| 1027 | — | 输出内容涉敏 | 请调整输入内容 |
| 1033 | — | 系统错误/下游服务错误 | 请稍后再试 |
| 1039 | — | Token 限制 | 请调整 `max_tokens` |
| 1041 | — | 连接数限制 | 请联系官方 |
| 1042 | — | 不可见字符比例超限/非法字符超过 10% | 请检查输入内容是否含不可见字符或非法字符 |
| 1043 | — | ASR 相似度检查失败 | 请检查 `file_id` 与 `text_validation` 匹配度 |
| 1044 | — | 克隆提示词相似度检查失败 | 请检查克隆提示音频和提示词 |
| 2013 | 400 | 参数错误 | 请检查请求参数 |
| 20132 | — | 语音克隆样本或 `voice_id` 参数错误 | 检查 Voice Cloning 的 `file_id` 与 T2A 的 `voice_id` |
| 2037 | — | 语音时长不符合要求 | 复刻音频时长不低于 10 秒、不超过 5 分钟 |
| 2038 | — | 用户语音克隆功能被禁用 | 需完成个人或企业认证 |
| 2039 | — | 语音克隆 `voice_id` 重复 | 修改 `voice_id` |
| 2042 | — | 无权访问该 `voice_id` | 确认是否为该 `voice_id` 创建者 |
| 2045 | — | 请求频率增长超限 | 避免请求骤增骤减 |
| 2048 | — | 语音克隆提示音频太长 | `prompt_audio` 时长 < 8s |
| 2049 | — | 无效的 API Key | 请检查 API Key |
| 2056 | — | 超出 M Plan 资源限制 | 等待下一个时间段资源释放后重试 |

（HTTP 列仅填文档明确写出的映射：错误码表本身不给 HTTP；Anthropic 兼容页给出了 `400/401/403/404/413/429/500/529` 的语义，语音识别页给出了 `401/400/429/402/422/500` 的 OpenAI 风格错误示例。）

#### 11.2 Anthropic 兼容协议的错误（HTTP 状态码 + JSON body）

HTTP 状态码的通用含义见《通用基线》§0.8；本家为每个状态码绑定了专属的 `error.type`：

| HTTP | 含义 | `error.type` |
| :- | :- | :- |
| 400 | 请求参数非法（必填缺失、type 不在白名单、`tool_use.input` 非 JSON 对象等） | `invalid_request_error` |
| 401 | API Key 缺失/无效 | `authentication_error` |
| 403 | 无权访问该模型或该路径 | `permission_error` |
| 404 | 模型不存在 | `not_found_error` |
| 413 | 请求体超过 64MB，或多模态文件超出大小限制 | `request_too_large` |
| 429 | 触发 RPM/TPM/连接数等限流 | `rate_limit_error` |
| 500 | 服务端内部错误 | `api_error` |
| 529 | 上游模型过载，可重试 | `overloaded_error` |

body 结构：`{"type":"error","request_id":"req_xxxxxxxx","error":{"type":"...","message":"..."}}`；流式过程中出错会以 `event: error` SSE 事件下发同样结构，客户端收到 error 后应停止读取并清理会话状态。

其它接口的 OpenAI 风格错误体：`{"type":"error","error":{"type":...,"message":"...（内部码）","http_code":"401"},"request_id":"..."}`；`error.type` 取值举例 `authorized_error`(401)、`bad_request_error`(400)、`rate_limit_error`(429)、`insufficient_balance_error`(402)、`unprocessable_entity_error`(422)、`invalid_request_error`(413)、`overloaded_error`(529)、`server_error`(500)。

#### 11.3 速率限制（RPM / TPM）

语言模型：

| 模型 | 限制类型 | 免费用户 | 充值用户 |
| :- | :- | :- | :- |
| MiniMax-M3 | RPM / TPM | 20 / 1,000,000 | 200 / 10,000,000 |
| MiniMax-M2.7、M2.7-highspeed、M2.5、M2.5-highspeed、M2.1、M2.1-highspeed、M2 | RPM / TPM | 20 / 1,000,000 | 500 / 20,000,000 |

其它模态（原文表）：视频 Video Generation（Hailuo 系列）RPM 20；Video Generation V2（MiniMax-H3）RPM 300、最大并行运行任务数 30；语音 T2A v2 免费 10 / 充值 20 RPM，Voice Cloning 60 RPM，Voice Design 20 RPM，Speech to Text 充值用户 2 CONN / 30,000 TPM（免费用户不开放，TPM 以音频秒数计量）；图片 Image Generation 10 RPM / 60 TPM；音乐 Music Generation 免费 3 RPM / 3 CONN，充值 120 RPM / 20 CONN。

限流规则要点：主账号与子账号共享全部限流额度；触发后会被拒绝直到经过指定时间；建议集中处理请求、把多个任务批量放进单个请求以提高 token 吞吐；可发邮件 `api@minimaxi.com` 申请提高限速（有时需 3-5 个工作日）。

### 12. 模型清单与限制

语言模型（上下文窗口 / 输入输出总 token）：

| 模型 ID | 上下文窗口（输入输出总 token） | 说明 |
| :- | :- | :- |
| `MiniMax-M3.1-Flash-Preview` | 1,000,000 | 原生多模态、1M 上下文的 Frontier Coding 模型，思考深度可调 |
| `MiniMax-M3` | 1,000,000 | 原生多模态、1M 上下文的 Frontier Coding 模型（输出速度约 100+ TPS） |
| `MiniMax-M2.7` | 204,800 | 开启模型的自我迭代（输出速度约 60 TPS） |
| `MiniMax-M2.7-highspeed` | 204,800 | M2.7 极速版：效果不变，更快，更敏捷（输出速度约 100 TPS） |
| `MiniMax-M2.5` / `MiniMax-M2.5-highspeed` | 204,800 | 历史模型 |
| `MiniMax-M2.1` / `MiniMax-M2.1-highspeed` | 204,800 | 历史模型 |
| `MiniMax-M2` | 204,800 | 历史模型，专为高效编码与 Agent 工作流而生 |
| `M2-her` | 64 K | 专为对话场景设计，支持角色扮演和多轮对话；仅文本输入，不支持图文混合 |

最大输出（生成长度上限）：

| 模型 | 推荐值 | 上限 |
| :- | :- | :- |
| `MiniMax-M3.1-Flash-Preview`、`MiniMax-M3` | 131072（128K） | 524288（512K） |
| 其他模型（M3.x/M2.x 之外的分档表述为 "其他模型"） | 65536（64K） | 204800（200K） |
| `M2-her` | — | `max_completion_tokens` 上限 2048 |

参数支持差异：

| 能力/参数 | 差异 |
| :- | :- |
| 图片/视频输入 | 仅 `MiniMax-M3.1-Flash-Preview`、`MiniMax-M3`；M2.7/M2.5/M2.1/M2 系列不支持；M2-her 不支持 |
| 思考深度 `reasoning_effort` / `output_config.effort` / `reasoning.effort` | 真正调节思考深度仅 `MiniMax-M3.1-Flash-Preview`；其他模型忽略该字段 |
| `thinking.type=disabled` | `M3.1-Flash-Preview` 返回 400；`M3` 跳过 thinking（Anthropic 侧默认关闭、传 `adaptive` 开启）；M2.x 接收但不生效 |
| `top_p` 默认值 | `M3.1-Flash-Preview`/`M3` = 0.95；M2.x 系列 = 0.9 |
| `temperature` 范围 | OpenAI 兼容与 Anthropic 兼容：[0, 2]，默认 1；原生 `/v1/text/chatcompletion_v2`：(0, 1]；Responses：(0, 1]，默认 1 |
| 多模态 | `M3.1-Flash-Preview`/`M3` 支持文本+图片+视频；不支持音频输入 |
| 结构化输出 `response_format` | 仅原生接口且仅 `MiniMax-Text-01` |
| 服务端工具 `web_search` | 仅 Anthropic Messages API 与 OpenAI Responses API；Beta |
| 主动缓存（`cache_control`） | 支持 M2.7/M2.5/M2.1/M2 系列；被动 Prompt 缓存支持 M3、M2.7、M2.5、M2.1 系列 |

其它模态模型（仅列 id 与一句定性，源自概览页）：视频 `MiniMax-H3`、`MiniMax-H3-Max`，历史模型 Hailuo 2.3 / Hailuo 2.3 Fast / Hailuo 02；语音 `speech-2.8-hd`、`speech-2.8-turbo`，历史 `speech-2.6-hd`、`speech-2.6-turbo`、`speech-02-hd`、`speech-02-turbo`，识别 `asr-1.0`；图片 `image-01`、`image-01-live`；音乐 `music-3.0`、`music-2.6`、`music-cover`（及 `-free` 变体）；MCP 视觉 `API-vlm`。

### 13. 来源页清单

本家页面清单（含每条 URL 的「这页讲了什么」一句话）统一见《附录：各家页面清单》的本家小节。

未抓到 / 文档未提供（逐条说明）：

1. **前缀续写**：全站索引与 sitemap 中均无该页面，未抓到。
2. **logprobs**：文档中未找到该字段；另明确写 `presence_penalty`、`frequency_penalty`、`logit_bias` 等参数会被忽略。
3. **OpenAI 兼容流式的结束标记（如 `data: [DONE]`）**：未抓到。
4. **Responses 协议的 SSE 事件名与 chunk 结构**：未抓到（仅说明 `stream: true` 启用 SSE）。
5. **`guides/quickstart-sdk`**（llms.txt 列出的"通过 SDK 接入"页）：未抓取，其内容与 `text-anthropic-api` 重叠。


---

## MiMo

> 抓取说明：该站文档为 fumadocs 站点，每个文档页都提供 `.mdx` 后缀的纯 Markdown 源（形如 `https://www.mimo-v2.com/zh/docs/<path>.mdx`），本文全部内容取自该源文件，不是从渲染后的 HTML 里反推的。
> 全站文档页清单已用两条独立线索交叉核对：① `https://www.mimo-v2.com/sitemap.xml`；② 文档页内嵌的侧边栏目录树（RSC flight payload 里的 `tree` 字段）；③ 对 28 个猜测路径逐个探测 `.mdx` 是否 404。三处结果一致：本站在 `zh/docs` 下**只有 17 个页面**（1 个首页 + 16 个内容页），已全部抓取。

---

### 1. 端点与鉴权

| 协议 | Base URL | Method + Path | 完整 URL | 必需请求头 |
|---|---|---|---|---|
| OpenAI 兼容 | `https://api.mimo-v2.com/v1` | `POST /chat/completions` | `https://api.mimo-v2.com/v1/chat/completions` | `Content-Type: application/json` + 二者之一：`api-key: <your-api-key>` 或 `Authorization: Bearer <your-api-key>` |
| Anthropic 兼容 | `https://api.mimo-v2.com/anthropic` | `POST /v1/messages` | `https://api.mimo-v2.com/anthropic/v1/messages` | `Content-Type: application/json` + `api-key: <your-api-key>`（该页"认证"表只列了 `api-key` 这一种） |
| TTS（走 Chat Completions） | `https://api.mimo-v2.com/v1` | `POST /chat/completions`（把待合成文本放进 `assistant` 消息，再加 `audio` 对象） | `https://api.mimo-v2.com/v1/chat/completions` | 同 OpenAI 兼容端点 |

补充事实（照抄原文）：

* 环境变量约定：`export MIMO_API_KEY="your-api-key-here"`。
* 同一账户下所有 API Key **共享相同的速率限制**。
* 语音合成页注明：`/v1/audio/speech` **仍然兼容** OpenAI 语音客户端，但**推荐**使用 `/v1/chat/completions` 格式。
* 文档中**没有**给出除上述三个之外的其他端点（`/v1/models`、`/v1/embeddings`、`/v1/files`、`/v1/images`、`/v1/audio/speech` 文档正文等均未出现，其中 `/v1/audio/speech` 仅在 TTS 页作为"仍兼容"被一句话提到，无字段说明）。

---

### 2. Chat Completions 请求字段总表

（对应 `POST https://api.mimo-v2.com/v1/chat/completions`）

下表只列本家与《通用基线》§0.2 不同的字段与取值；未列出的标准字段语义同基线（本家文档未写明其默认值/范围即视为未列出）。

| 字段 | 类型 | 必填/默认 | 取值 | 说明 |
|---|---|---|---|---|
| `model` | string | 必填 | `mimo-v2.5-pro`、`mimo-v2.5`、`mimo-v2-pro`、`mimo-v2-omni`（OpenAI 兼容页给出的可选值）；TTS 端点另可用 `mimo-v2.5-tts`、`mimo-v2.5-tts-voicedesign`、`mimo-v2.5-tts-voiceclone` | 模型 ID |
| `messages` | array | 必填 | 消息对象数组，字段见下 | 消息列表 |
| `messages[].reasoning_content` | string | 否（assistant 消息） | — | 模型的思考/推理内容；构建多轮对话时建议保留历史值（见第 6 节） |
| `max_completion_tokens` | integer | 否，默认 `1024`（模型超参页给出的默认值） | 取值范围因模型而异：MiMo-V2-Pro `1024-128000`、MiMo-V2-Omni `1024-128000`、MiMo-V2-Flash `1024-64000` | 最大生成 token 数；**启用思考模式时，此数值包含可见输出和内部推理 Token** |
| `temperature` | number | 否，默认 `1.0` | `0.0` 到 `2.0` | 采样温度 |
| `top_p` | number | 否，默认 `0.95` | `0.0` 到 `1.0` | 核采样阈值 |
| `stream` | boolean | 否，默认 `false` | `true` / `false` | 流式输出（SSE），以 `data: [DONE]` 结束 |
| `stop` | string / array | 否，默认 `null` | 最多 4 个序列 | 停止序列 |
| `frequency_penalty` | number | 否，默认 `0` | `-2.0` 到 `2.0` | 频率惩罚，正值可减少重复 |
| `presence_penalty` | number | 否，默认 `0` | `-2.0` 到 `2.0` | 存在惩罚，正值鼓励引入新话题 |
| `tools` | array | 否 | 工具/函数定义列表（结构见第 7 节） | 工具（Function Calling）定义 |
| `tool_choice` | string / object | 否 | `auto`、`none` 或指定工具 | 工具选择策略 |
| `audio` | object | 否（仅 TTS 模型使用） | `{ "format": ..., "voice": ... }` | 语音合成输出配置，字段见第 8 节② |

`messages[]` 其余字段（`role`、`content`、`tool_calls`）同《通用基线》§0.3；`role` 可选值 `system`、`user`、`assistant`、`tool`。

`content` 数组内可用的内容块（`text` 块同《通用基线》§0.3，本家各多模态页示例中出现的形式如下）：

| 内容块 `type` | 结构 | 出现于 |
|---|---|---|
| `image_url` | `{"type":"image_url","image_url":{"url":"..."}}` | 图片理解页 |
| `input_audio` | `{"type":"input_audio","input_audio":{"data":"<base64>","format":"wav"}}` | 语音理解页 |
| `video_url` | `{"type":"video_url","video_url":{"url":"..."}}` | 视频理解页 |

* 文档中**未给出** `n`、`stream_options`、`seed`、`response_format`、`logprobs`、`top_logprobs`、`user`、`parallel_tool_calls`、`logit_bias`、`max_tokens`（OpenAI 口径）等字段——OpenAI 兼容页的"请求参数"表（原文计 11 项）之外没有别的字段。

---

### 3. Responses API 字段差异

**该站未提供此协议**（《通用基线》§0.6 亦记 MiMo 未提供）。

已核查的页面（均无 Responses / `/v1/responses` 字样）：`quick-start/first-api-call`、`quick-start/model-parameters`、`quick-start/error-codes`、`pricing`、`faq`、`api/chat/openai-api`、`api/chat/anthropic-api`、`integration/claude-code`、`integration/cline`、`usage-guide/multimodal/image`、`usage-guide/multimodal/audio`、`usage-guide/multimodal/video`、`usage-guide/tts`、`usage-guide/tool-calling/web-search`、`terms/privacy-policy`、`terms/user-agreement`。

另外直接探测了 `https://www.mimo-v2.com/zh/docs/api/responses.mdx` 与 `.../api/chat/responses.mdx`，均返回 **404**（该站 `.mdx` 路由对不存在的页面明确返回 404，因此 404 可作为"页面不存在"的判据）。

---

### 4. Anthropic 兼容端点字段差异

端点：`POST https://api.mimo-v2.com/anthropic/v1/messages`；鉴权头只有 `api-key: <your-api-key>`。Anthropic Messages 的标准骨架字段（`model`、`messages`、`max_tokens`、`stream`、`system`、`stop_sequences`、响应 `content` 块与 `stop_reason`、类型化流式事件序列）见《通用基线》§0.7；下表只留本家差异与文档明确写出的取值。

请求参数：

| 参数 | 类型 | 必填 | 取值 / 默认 | 说明 |
|---|---|---|---|---|
| `model` | string | 是 | `mimo-v2.5-pro`、`mimo-v2.5`、`mimo-v2-pro`、`mimo-v2-omni` | 模型 ID |
| `messages` | array | 是 | 消息对象数组 | 见下表 |
| `max_tokens` | integer | **是**（与 OpenAI 侧不同，这里必填） | 示例中用 `1024` | 最大生成 token 数 |
| `system` | string | 否 | 字符串（示例里是一整段系统提示词，**不是** OpenAI 那种 content 数组） | 系统提示词（顶层字段，不在 `messages` 里） |
| `temperature` | number | 否 | 默认 `1.0` | 采样温度 |
| `top_p` | number | 否 | 默认 `0.95` | 核采样 |
| `stream` | boolean | 否 | 默认 `false` | 启用流式输出 |
| `stop_sequences` | array | 否 | 示例中传 `null` | 停止序列（OpenAI 侧叫 `stop`） |

`messages[]` 消息对象：

| 字段 | 类型 | 说明 |
|---|---|---|
| `role` | string | 可选值：`user`、`assistant`（**没有** system，system 走顶层字段） |
| `content` | string / array | 消息内容（文本或内容块数组）；示例中内容块形如 `{"type":"text","text":"..."}` |

与 OpenAI 兼容端点的字段差异汇总（全部为该站文档明确体现的）：

| 差异点 | OpenAI 兼容侧 | Anthropic 兼容侧 |
|---|---|---|
| 路径 | `POST /v1/chat/completions` | `POST /v1/messages` |
| 鉴权头 | `api-key` 或 `Authorization: Bearer` | 仅 `api-key` |
| 最大输出字段 | `max_completion_tokens`（可选） | `max_tokens`（**必填**） |
| 系统提示词 | `messages` 里 `role: "system"` | 顶层 `system` 字符串 |
| 停止序列字段名 | `stop` | `stop_sequences` |
| 采样惩罚 | 有 `frequency_penalty`、`presence_penalty` | 文档中**未列出** |
| 工具调用 | 有 `tools`、`tool_choice` | 文档中**未列出**（该页参数表无 tools 相关字段） |
| 思考内容 | `reasoning_content` | 文档中**未列出** |
| 流式协议 | `data:` 前缀的 chunk，结尾 `data: [DONE]` | 类型化事件（`event:` + `data:`） |

---

### 5. 响应字段与流式结构

#### 5.1 非流式响应（OpenAI 兼容端点）

顶层字段与 `choices[]`、`usage` 的标准形状见《通用基线》§0.5；本家写出的与本家特有部分：

| 字段 | 说明 |
|---|---|
| `id` | 补全的唯一标识符（示例形如 `chatcmpl-xxx`） |
| `object` | 固定为 `chat.completion` |
| `choices[].message.reasoning_content` | 模型的内部推理（可用时） |
| `choices[].finish_reason` | 模型停止原因：`stop`、`length` 或 `tool_calls` |
| `usage` | 只有 `prompt_tokens` / `completion_tokens` / `total_tokens`；缓存命中与思考 token 明细字段均未给出（见第 9、6 节） |

原文的非流式响应示例只演示上述标准形状（`id`/`object`/`created`/`model`/`choices`/`usage`），见《通用基线》§0.5。

#### 5.2 非流式响应（Anthropic 兼容端点）

| 字段 | 说明 |
|---|---|
| `id` | 消息的唯一标识符（示例形如 `msg_xxx`） |
| `type` | 固定为 `message` |
| `role` | 固定为 `assistant` |
| `content` | 内容块数组；`content[].type`（如 `text`）与 `content[].text` 同《通用基线》§0.7 |
| `stop_reason` | 模型停止原因：`end_turn`、`max_tokens` 或 `stop_sequence` |
| `usage` | 只有 `input_tokens`（示例 50）与 `output_tokens`（示例 100） |

原文的非流式响应示例只演示上述标准形状，见《通用基线》§0.7。

#### 5.3 流式结构（OpenAI 兼容端点）

* 当 `stream: true` 时返回 Server-Sent Events（SSE），每个事件以 `data: ` 前缀。
* chunk 的 `object` 为 `chat.completion.chunk`，增量在 `choices[].delta` 里（同《通用基线》§0.5）；示例 chunk 即标准形状，见基线。
* **结束标记：`data: [DONE]`**。
* 文档说明：在流式模式下 `reasoning_content` **可能出现在主 `content` 之前的早期 delta 块中**。
* 文档中**未给出**流式 chunk 里的 `usage` 字段（即没有流式用量统计的说明，也没有 `stream_options`）。

#### 5.4 流式结构（Anthropic 兼容端点）

* 流由一系列**类型化事件**组成，每个 SSE 事件同时带 `event:` 与 `data:` 两个字段。
* 事件序列（同《通用基线》§0.7）：`message_start` → `content_block_start` → `content_block_delta` → `content_block_stop` → `message_delta` → `message_stop`；`content_block_delta` 的增量文本为 `text_delta`。
* 该页明确提示："与 OpenAI 流式格式不同，Anthropic 兼容的流式输出使用类型化事件，每个 SSE 事件包含 `event:` 字段和 `data:` 字段。"
* Anthropic 侧的 `usage` 在 `message_start`（`input_tokens`，示例 50）与 `message_delta`（`output_tokens`，示例 15）两处分片给出。
* 文档中**未给出** Anthropic 侧的思考/推理流式事件（如 `thinking_delta`）。

---

### 6. 思考/推理字段

该站该能力**有文档**，字段名是 **`reasoning_content`**（不是 `reasoning`、不是 `thinking`、不是 `reasoning_details`）。

| 位置 | 字段 | 说明 |
|---|---|---|
| 请求 `messages[]` 的 assistant 消息 | `reasoning_content` | 文档原文："在思考模式下，模型会同时返回 `tool_calls` 和 `reasoning_content`。"并给出**重要提示**：构建多轮对话时，建议在后续请求中**保留所有历史的 `reasoning_content` 字段**，让模型保持推理过程上下文，从而获得更连贯、更准确的工具调用。请求示例中该字段与 `content` 并列放在 assistant 消息里。 |
| 非流式响应 | `choices[].message.reasoning_content` | "模型的内部推理（可用时）" |
| 流式响应 | `choices[].delta.reasoning_content` | 文档未画出 delta 里的具体 JSON，但明确写道"在流式模式下，`reasoning_content` 可能出现在主 `content` 之前的早期 delta 块中" |
| 能力标签 | "深度思考" | 定价页的能力列里，`mimo-v2.5-pro`、`mimo-v2.5`、`mimo-v2-pro`、`mimo-v2-omni` 都标有"深度思考" |

**明确没有文档的**（该站确实找不到，列一下查过的位置）：

* 没有任何开关注思档位的请求参数：文档中**未找到** `reasoning_effort`、`thinking`、`enable_thinking`、`thinking_budget`、`reasoning.enabled` 之类的字段。（查过：`quick-start/model-parameters` 的参数详解只有 temperature / top_p / max_completion_tokens / frequency_penalty / presence_penalty / stream / stop 七项；`api/chat/openai-api` 请求参数表无任何思考相关字段；`api/chat/anthropic-api` 参数表同样没有。）
* 没有任何思考 Token 的用量字段：`usage` 里**未找到** `reasoning_tokens`、`completion_tokens_details`。
* 文档中**未找到**"如何开启/关闭思考模式"的说明（只在"多轮工具调用与思考模式"标题下描述为"在思考模式下……"，没有给出开启方式）。
* Anthropic 兼容端点的思考字段/事件：**未找到**。

---

### 7. 工具调用字段

#### 7.1 请求侧

请求侧字段结构同《通用基线》§0.4；本家文档明确写出的部分：

* `tools[].type`：文档示例中固定为 `"function"`。
* `tools[].function.name`：函数名，例如 `get_current_weather`；联网搜索内置工具用 `web_search`。
* `tools[].function.parameters`：JSON Schema；文档示例为 `{"type":"object","properties":{...},"required":[...]}`，`properties` 里每个参数用 `type` / `description` / `enum`。
* `tool_choice`：`auto`、`none` 或指定工具。
* 原文请求示例只演示上述标准 `tools` 形状（`"tool_choice": "auto"` 配一个 `function` 工具），见《通用基线》§0.4。

#### 7.2 响应 / 消息侧

| 字段 | 位置 | 说明 |
|---|---|---|
| `choices[].message.tool_calls` | 响应 | "（可选）助手发起的工具调用" |
| `choices[].finish_reason` | 响应 | 取值含 `tool_calls` |
| `messages[].tool_calls` | 请求消息对象 | "（可选）助手发起的工具调用" |
| `messages[].role` | 请求消息对象 | 含 `tool`（工具结果消息） |

**文档没有给出的部分**（不猜）：`tool_calls[]` 的内部结构（`id`、`type`、`function.name`、`function.arguments` 这些子字段）、工具结果消息里的 `tool_call_id`、流式下 `tool_calls` 的 delta 拼接规则——以上在文档中**均未给出**，只有"`tool_calls` array / 助手发起的工具调用"这一句。

#### 7.3 内置联网搜索（web_search）

* 支持模型（原文）：**MiMo-V2-Pro、MiMo-V2-Omni 和 MiMo-V2-Flash** 支持内置联网搜索。
* 启用方式：在 `tools` 里定义 `name: "web_search"`、`description: "Search the web for current information"`、`parameters.properties.query`（`type: string`，必填）的一个 function，并设 `tool_choice: "auto"`。
* 工作原理："搜索结果会自动解析并注入到模型的上下文中"，**你无需手动处理工具调用的返回结果**。
* 定价：**$5 / 1000 次调用**，独立于模型 Token 计费。

---

### 8. 多模态与文件（输入 + 输出）

#### ① 输入模态

| 模态 | 内容块 `type` | 字段名 | 传法（URL / base64） | 格式白名单 | 体积 / 分辨率 / 时长 / 张数上限 | 哪些模型支持 |
|---|---|---|---|---|---|---|
| 图片 | `image_url` | `image_url.url` | **两种都支持**：直接 URL（`https://example.com/image.jpg`）或 base64 内联（`data:image/jpeg;base64,{image_data}`） | `image/jpeg`、`image/png`、`image/gif`、`image/webp` | 文档未给出。仅有说明："图片 Token 消耗与图片分辨率相关。分辨率越高，消耗的 Token 越多" | 文档示例用 `mimo-v2-omni`（页面标题写"MiMo-V2-Omni 支持图片理解"） |
| 音频 | `input_audio` | `input_audio.data`、`input_audio.format` | **只有 base64**（原文："音频需要以 base64 编码数据的形式包含在消息内容中"） | `.wav`、`.mp3`、`.flac`、`.ogg`（`format` 示例值为 `"wav"`） | 文档未给出。仅有说明："音频 Token 消耗与音频时长相关。音频越长，消耗的 Token 越多" | 文档示例用 `mimo-v2-omni` |
| 视频 | `video_url` | `video_url.url` | **两种都支持**：直接 URL（`https://example.com/video.mp4`）或 base64 内联（`data:video/mp4;base64,{video_data}`） | 文档**未给出**白名单（示例只用 MP4） | 文档未给出数值上限。原文说明 Token 用量取决于**视频时长**、**分辨率**、**帧率**（"模型会按固定间隔从视频中采样帧"），并建议"使用较短的片段或较低的分辨率来控制成本。对于长视频，建议提取关键帧作为图片输入" | 文档示例用 `mimo-v2-omni` |
| 通用文件（PDF / docx 等） | — | — | 文档**未给出**（无 `type: "file"` / file_id / file_url 之类的内容块） | — | — | — |

补充：定价页把 `mimo-v2.5` 与 `mimo-v2-omni` 的能力标注为"多模态理解"，`mimo-v2.5-pro` / `mimo-v2-pro` 标注为"文本生成"（不含多模态）。

#### ② 输出模态

| 输出模态 | 端点 | 请求字段 | 返回形式 | 时长 / 尺寸限制 | 备注 |
|---|---|---|---|---|---|
| 语音合成（TTS） | `POST https://api.mimo-v2.com/v1/chat/completions`（另：`/v1/audio/speech` 仍兼容 OpenAI 语音客户端，但推荐用 chat 格式） | `model`（`mimo-v2.5-tts`、`mimo-v2.5-tts-voicedesign`、`mimo-v2.5-tts-voiceclone`）；`messages`（**待合成文本放在 `assistant` 消息里**；`user` 消息**可选**，用来写语气、风格等要求）；`audio.format`（可选，`wav` / `mp3` / `pcm16`）；`audio.voice`（可选，内置音色 ID，**默认 `mimo_default`**） | base64 音频，从 `completion.choices[0].message.audio.data` 取，自行 base64 解码后落盘（文档示例 `base64.b64decode(...)` 写 `output.wav`） | 文档未给出 | — |
| 图片生成 | 文档**未给出** | — | — | — | 全站文档无图片生成端点 |
| 视频生成 | 文档**未给出** | — | — | — | 全站文档无视频生成端点 |
| 音色清单 | 文档**未给出** | — | — | — | 只写了"内置音色 ID，默认 `mimo_default`"，没有音色列表；`voicedesign` / `voiceclone` 两个模型 ID 出现但**没有用法说明** |

#### ③ 文件上传与管理 API

**文档未给出。** 全站 17 个文档页中没有任何文件上传/文件列表/文件删除端点、没有 `file` 内容块、没有 `file_id`。

已核查位置：`sitemap.xml` 全量文档清单 + 侧边栏目录树 + 对 `api/files`、`usage-guide/multimodal/file`、`usage-guide/multimodal/pdf` 三个路径探测 `.mdx`（均为 **404**）。

#### ④ 相关端点汇总

| 用途 | 端点 | 来源页 |
|---|---|---|
| 文本 / 多模态输入 → 文本输出 | `POST https://api.mimo-v2.com/v1/chat/completions` | OpenAI 兼容 API |
| 文本 → 语音输出（TTS） | `POST https://api.mimo-v2.com/v1/chat/completions`（带 `audio` 对象） | 语音合成 |
| 文本 → 语音输出（旧兼容路径） | `/v1/audio/speech`（仍兼容 OpenAI 语音客户端，无字段文档） | 语音合成 |
| Anthropic 协议文本 | `POST https://api.mimo-v2.com/anthropic/v1/messages` | Anthropic 兼容 API |
| 图片 / 音频 / 视频理解 | 无独立端点，走 `/v1/chat/completions` 的 `content` 数组 | 图片 / 语音 / 视频理解 |
| 联网搜索 | 无独立端点，走 `tools` 里的 `web_search` 内置函数 | 联网搜索 |
| 文件上传 / 管理 | **文档未给出** | — |
| 图片生成 / 视频生成 | **文档未给出** | — |

---

### 9. 缓存与成本字段

该站有**缓存计价**，但**没有任何请求/响应侧的缓存控制字段**。

计价（定价页原文）：

| 项目 | 值 |
|---|---|
| 缓存命中输入 Token | 单独一档价格，各模型不同（见第 12 节） |
| 缓存写入 | "**缓存写入目前限时免费**" |
| 计费口径 | "用量根据处理的输入和输出 Token 数量计算。缓存命中的输入 Token 以优惠价格计费。" |

* 请求侧缓存字段：文档中**未找到** `prompt_cache_key`、`cache_control`、`cache_creation_input_tokens` 等。
* 响应 `usage` 里的缓存明细字段：文档中**未找到**（OpenAI 侧 `usage` 只有 `prompt_tokens` / `completion_tokens` / `total_tokens`；Anthropic 侧只有 `input_tokens` / `output_tokens`）。
* 成本相关字段：文档中**未找到** `cost`、`billing`、`price` 之类的响应字段；价格只在定价页以表格给出。
* 联网搜索是唯一有独立计价的内置能力：$5 / 1000 次。

---

### 10. 特殊模式

| 模式 | 文档情况 |
|---|---|
| 流式输出 | **有**。`stream: true`（默认 false），OpenAI 侧 SSE 以 `data: [DONE]` 结束，Anthropic 侧用类型化事件。详见第 5 节。 |
| 思考模式 | **有字段、无开关文档**。见第 6 节：`reasoning_content` 可请求侧回传、响应侧返回；但没有 `reasoning_effort` 之类的档位参数。 |
| 联网搜索（内置工具） | **有**。`tools` 里声明 `web_search` 函数 + `tool_choice: "auto"`，搜索结果自动注入上下文。详见第 7.3 节。 |
| 结构化输出 | **只有能力标签，没有字段和用法**。定价页在 `mimo-v2.5-pro` / `mimo-v2.5` / `mimo-v2-pro` / `mimo-v2-omni` 的能力列里都写了"结构化输出"，但**全站文档未给出** `response_format`、`json_schema`、`json_object`、`strict` 等任何字段，也没有单独的结构化输出页面（探测 `/zh/docs/usage-guide/structured-output.mdx` → **404**）。 |
| 前缀续写（prefix completion / assistant prefix） | **未给出**。全站文档无此说明。 |
| `logprobs` / `top_logprobs` | **未给出**。请求参数表与响应字段表都没有。 |
| 批处理 / Batch API | **未给出**。 |
| 微调 / 模型管理 | **未给出**。 |
| 提示缓存开关 | **未给出**（见第 9 节）。 |

---

### 11. 错误码与限流

#### 11.1 错误码表

标准 HTTP 状态码的通用含义见《通用基线》§0.8；本家业务码、错误响应体与重试建议如下。（照抄 `quick-start/error-codes` 页的表）

| 码 | HTTP | 含义 | 建议动作 |
|---|---|---|---|
| `invalid_request` | 400 | 请求体格式错误或缺少必填字段 | 检查请求参数和格式（发送前验证请求：必填字段齐全、模型名在可用模型内、参数值在范围内、API Key 非空） |
| `authentication_failed` | 401 | API Key 无效或缺失 | 确认 API Key 正确无误 |
| `permission_denied` | 403 | 您的账号无权访问该资源 | 检查账号权限和订阅状态 |
| `not_found` | 404 | 请求的资源不存在 | 确认端点 URL 和模型名称 |
| `rate_limit_exceeded` | 429 | 请求过于频繁，超出速率限制 | 降低请求频率或等待后重试（遵守 `Retry-After` 头、降低并发量、实现请求队列） |
| `internal_error` | 500 | 服务器内部错误 | 短暂等待后重试；如持续出现请联系支持团队 |
| `service_unavailable` | 503 | 服务因高负载暂时不可用 | 使用指数退避策略重试 |

可重试 vs 不可重试（原文）：**可重试** = 429、500、503；**不可重试** = 400、401、403、404。

错误响应结构：

```json
{
  "error": {
    "code": "authentication_failed",
    "message": "Invalid API Key provided.",
    "type": "error"
  }
}
```

指数退避示例（原文）：`delay = min(2 ** attempt + random.random(), 60)`，`max_retries=5`。

#### 11.2 限流

| 项目 | 值 |
|---|---|
| 固定并发限制 | **不设固定并发限制**（原文："Mimo API 不设固定并发限制"），但高负载时会遇到 429 |
| RPM | 文档列出的 5 个模型**均为 RPM: 100** |
| TPM | 文档列出的 5 个模型**均为 TPM: 10M** |
| 429 响应头 | 文档提到"如果响应包含 `Retry-After` 头，请至少等待指定时间后再重试" |
| Key 与限流关系 | 同一账户下所有 API Key **共享相同的速率限制** |

* 注意：定价页没有单独给"按账户/按组织"的限流数值，只有上面这张"模型详情"里的 RPM/TPM。
* 文档中**未给出** `x-ratelimit-*` 系列响应头。

---

### 12. 模型清单与限制

#### 12.1 定价页给出的模型详情（照抄）

| 模型 ID | 类别 | 上下文长度 | 最大输出长度 | 能力 | 定价（USD / 百万 tokens） | 速率限制 |
|---|---|---|---|---|---|---|
| `mimo-v2.5-pro` | 文本生成 - 通用大语言模型 | **1M** | **128K** | 文本生成、深度思考、流式输出、函数调用、结构化输出、联网搜索 | 输入 **$0.435**；输入（缓存命中）**$0.0036**；输出 **$0.87** | RPM: 100, TPM: 10M |
| `mimo-v2.5` | 文本生成 - 多模态理解模型 | **1M** | **128K** | 多模态理解、深度思考、流式输出、函数调用、结构化输出、联网搜索 | 输入 **$0.14**；输入（缓存命中）**$0.0028**；输出 **$0.28** | RPM: 100, TPM: 10M |
| `mimo-v2-pro` | 文本生成 - 通用大语言模型 | **1M** | **128K** | 文本生成、深度思考、流式输出、函数调用、结构化输出、联网搜索 | **2026 年 6 月 1 日后自动按 MiMo-V2.5-Pro 价格计费**：输入 $0.435；输入（缓存命中）$0.0036；输出 $0.87 | RPM: 100, TPM: 10M |
| `mimo-v2-omni` | 文本生成 - 多模态理解模型 | **256K** | **128K** | 多模态理解、深度思考、流式输出、函数调用、结构化输出、联网搜索 | **2026 年 6 月 1 日后自动按 MiMo-V2.5 价格计费**：输入 $0.14；输入（缓存命中）$0.0028；输出 $0.28 | RPM: 100, TPM: 10M |
| `mimo-v2-tts` | 语音合成模型 | **8K** | **8K** | 文本转语音合成 | 限时免费 | RPM: 100, TPM: 10M |

（定价页另有一张"联网搜索插件"表：`$5 / 1000次`，含联网搜索及搜索相关内容的页面解析。页尾注明"缓存写入目前限时免费"。）

#### 12.2 文档其它位置出现的模型 ID / 名称（与上表对照）

| 名称 / ID | 出现位置 | 该处给出的信息 |
|---|---|---|
| `mimo-v2.5-tts-voicedesign` | 语音合成页参数表 | 可用于 `model`；**没有**上下文/定价/用法说明 |
| `mimo-v2.5-tts-voiceclone` | 语音合成页参数表 | 可用于 `model`；**没有**上下文/定价/用法说明 |
| `mimo-v2.5-tts` | 语音合成页参数表 | 可用于 `model`；示例用 `audio: {"format":"wav","voice":"mimo_default"}` |
| MiMo-V2-Flash | 模型超参页（`max_completion_tokens` 1024-64000）、联网搜索页（支持内置联网搜索） | **定价页没有它的条目**，因此其模型 ID、上下文长度、最大输出、定价、RPM/TPM 在文档中**均未给出**（其模型 ID 猜测为 `mimo-v2-flash` 属猜测，**未写**） |

#### 12.3 参数支持差异

| 模型（文档口径名称） | `temperature` 推荐 | `top_p` 推荐 | `max_completion_tokens` 范围 | `frequency_penalty` | `presence_penalty` | `stream` | `stop` |
|---|---|---|---|---|---|---|---|
| MiMo-V2-Pro | 1.0 | 0.95 | 1024-128000 | 0 | 0 | true/false | null |
| MiMo-V2-Omni | 1.0 | 0.95 | 1024-128000 | 0 | 0 | true/false | null |
| MiMo-V2-Flash | 1.0 | 0.95 | 1024-64000 | 0 | 0 | true/false | null |

* 模型超参页只覆盖上述三个模型，**没有** `mimo-v2.5` / `mimo-v2.5-pro` 的推荐参数表。
* 全局默认值（参数详解节）：`temperature` 默认 1.0（范围 0.0-2.0）、`top_p` 默认 0.95（范围 0.0-1.0）、`max_completion_tokens` 默认 1024、`frequency_penalty` 默认 0（范围 -2.0-2.0）、`presence_penalty` 默认 0（范围 -2.0-2.0）、`stream` 默认 false、`stop` 默认 null（最多 4 个序列）。
* 模型超参页明确："当启用思考模式时，`max_completion_tokens` 此数值**包含可见输出和内部推理 Token**"。

#### 12.4 多模态能力差异

| 模型 | 文本输入/输出 | 图片 | 音频输入 | 视频 | 音频输出（TTS） | 函数调用 | 联网搜索 |
|---|---|---|---|---|---|---|---|
| `mimo-v2.5-pro` | 是 | 文档未标注（定价页能力列无"多模态理解"） | 同左 | 同左 | 否 | 是 | 是 |
| `mimo-v2.5` | 是 | 是（"多模态理解"） | 是（同上） | 是（同上） | 否 | 是 | 是 |
| `mimo-v2-pro` | 是 | 否（定价页能力列无"多模态理解"） | 否 | 否 | 否 | 是（文档明确） | 是 |
| `mimo-v2-omni` | 是 | 是（"多模态理解"，三个多模态页的示例模型） | 是 | 是 | 否 | 是（文档明确） | 是 |
| `mimo-v2-tts` | 否（语音合成专用） | 否 | 否 | 否 | 是 | 文档未给出 | 文档未给出 |
| MiMo-V2-Flash | 是 | 文档未给出 | 文档未给出 | 文档未给出 | 否 | 是（文档明确） | 是 |

* 质量/分辨率/时长类硬上限：**文档未给出**（只有 Token 消耗趋势的说明，见第 8 节）。

---

### 13. 来源页清单

本家页面清单（含每条 URL 的「这页讲了什么」一句话）统一见《附录：各家页面清单》的本家小节。

---

### 附：没抓到的项（一句话汇总）

1. **Responses API** —— 该站未提供（已查 16 个内容页 + 探测 2 个路径均 404）。
2. **结构化输出字段** —— 定价页只把它列为能力，全站无 `response_format` / `json_schema` 等字段文档（结构化输出专页探测 404）。
3. **思考档位参数** —— 全站无 `reasoning_effort` / `thinking` 之类字段；`reasoning_content` 有，但"怎么开启思考模式"没写。
4. **`tool_calls` 内部结构 / `tool_call_id` / 流式工具增量拼接** —— 文档只有一句"`tool_calls` array，助手发起的工具调用"。
5. **多模态硬上限** —— 图片体积/分辨率上限、音频时长上限、视频时长/帧率上限、单请求内容块张数上限，文档全部未给出；只有 Token 消耗趋势说明。
6. **文件上传与管理 API** —— 文档未给出（无端点、无 `file` 内容块、无 `file_id`）。
7. **图片生成 / 视频生成** —— 文档未给出。
8. **TTS 音色清单与时长限制**、`voicedesign`/`voiceclone` 的用法 —— 文档未给出（只有默认音色 `mimo_default`）。
9. **缓存控制字段与缓存用量字段** —— 文档未给出（只有定价页的价格档位）。
10. **前缀续写、`logprobs`、Batch API、微调** —— 文档未给出。
11. **MiMo-V2-Flash 的模型 ID / 上下文 / 输出上限 / 定价 / RPM** —— 该模型在三个页面被提到（含 `max_completion_tokens` 1024-64000），但定价页没有它的条目。
12. **`x-ratelimit-*` 响应头** —— 文档未给出（只有 `Retry-After` 被提到）。

---

## 千问AI平台
> 本节收录千问AI平台（https://platform.qianwenai.com）**协议核心**页面与 OpenAPI 定义：四个机器可读协议规格（openapi-openai-chat.json、openapi-openai-responses.json、openapi-anthropic.json、openapi-dashscope.json，正文表格以其为准）、`/api-reference/chat/*`、`/api-reference/more/*`、`/developer-guides/text-generation/*`、`/developer-guides/tool-calling/*`、`/developer-guides/getting-started/*`、`/developer-guides/accuracy-tuning/*`、`/developer-guides/third-party-models/*`、`/developer-guides/run-and-scale/*`，另补错误码、限流、动态限流、视觉理解、文字提取共 5 页。
>
> **不在本次收录范围内**：图像生成、视频生成、3D 生成、语音识别/合成/翻译、音乐生成、向量与重排、世界模型等**生成类 API**（站点有独立页面）；Agent Infra（Managed Agents / RAG / Memory / Sandbox / MCP 服务）等页面。以上本次均未收录，不代表站点没有。

---

### 1. 端点与鉴权

四个协议规格声明同一台服务器（`servers[].url`）：`https://maas.qianwenaiapi.com`（openai-chat / openai-responses / anthropic 描述为「中国」，dashscope 描述为「北京」）。

| 协议 | base_url | method + path | 必需请求头 |
| --- | --- | --- | --- |
| OpenAI 兼容 Chat | `https://maas.qianwenaiapi.com/compatible-mode/v1` | `POST /compatible-mode/v1/chat/completions` | `Authorization: Bearer <API Key>`、`Content-Type: application/json` |
| OpenAI 兼容 Responses | `https://maas.qianwenaiapi.com/compatible-mode/v1` | `POST /compatible-mode/v1/responses` | `Authorization: Bearer <API Key>`、`Content-Type: application/json` |
| OpenAI 兼容 Responses（查询/删除/输入项） | 同上 | `GET /compatible-mode/v1/responses/{response_id}`、`DELETE /compatible-mode/v1/responses/{response_id}`、`GET /compatible-mode/v1/responses/{response_id}/input_items` | `Authorization: Bearer <API Key>` |
| Anthropic 兼容 Messages | `https://maas.qianwenaiapi.com/apps/anthropic` | `POST /apps/anthropic/v1/messages` | `x-api-key: <API Key>` **或** `Authorization: Bearer <API Key>`（二者选其一）、`Content-Type: application/json` |
| DashScope 原生（纯文本） | SDK `base_http_api_url = https://maas.qianwenaiapi.com/api/v1` | `POST /api/v1/services/aigc/text-generation/generation` | `Authorization: Bearer <API Key>`；流式另需 `X-DashScope-SSE: enable` |
| DashScope 原生（多模态） | 同上 | `POST /api/v1/services/aigc/multimodal-generation/generation` | 同上 |
| 异步任务（Task API） | `https://maas.qianwenaiapi.com/api/v1` | `GET /api/v1/tasks/{task_id}`、`POST /api/v1/tasks/{task_id}/cancel` | `Authorization: Bearer sk-ws-xxx` |
| Batch（文件输入） | `https://maas.qianwenaiapi.com/compatible-mode/v1`（OpenAI SDK） | `POST /v1/files`、`POST /v1/batches` 等 Batch 接口（`client.files.create` / `client.batches.create`） | `Authorization: Bearer <API Key>` |
| Batch Chat（同步批量对话） | `https://batch.dashscope.aliyuncs.com/compatible-mode/v1` | `POST /compatible-mode/v1/chat/completions` | `Authorization: Bearer <API Key>` |

鉴权方案（规格 `components.securitySchemes`）：

- openai-chat / openai-responses / dashscope：`BearerAuth`，`type: http`，`scheme: bearer`，说明为「千问AI平台 API Key，详见 /api-reference/preparation/api-key」。全局 `security: [{"BearerAuth": []}]`。
- anthropic：`ApiKeyAuth`，`type: apiKey`，`in: header`，`name: x-api-key`；说明同时接受 `Authorization: Bearer`。
- API Key 环境变量名（示例代码统一使用）：`DASHSCOPE_API_KEY`。

其他端点与鉴权事实（文档原文）：

- Responses API 旧路径 `https://maas.qianwenaiapi.com/api/v2/apps/protocols/compatible-mode/v1/responses` **已停止维护**，须迁移至 `/compatible-mode/v1/responses`。
- Anthropic 兼容端点**只提供** Messages API（`/v1/messages`），**没有** `/v1/models` 模型列表端点；base URL 必须以 `/apps/anthropic` 结尾，若写成 `/apps/anthropic/v1/`，客户端追加 `/v1/models` 会得到 `/v1/v1/models` 并返回 HTTP 404。
- DashScope 原生协议流式输出除 `parameters.stream: true` 外，**HTTP 调用还需请求头 `X-DashScope-SSE: enable`**；Java SDK 用 `streamCall`。
- `http_request` 类端点（临时 API Key、上传文件获取临时 URL、连接复用等）见 `/api-reference/more/*`，本节只列已在同一批页面中给出的地址。

---

### 2. Chat Completions 请求字段总表

以 `openapi-openai-chat.json`（`POST /compatible-mode/v1/chat/completions`）为准。请求体 `required = ["model", "messages"]`。

下表只列本家与《通用基线》§0.2 不同的字段与取值；未列出的标准字段语义同基线（本家文档未写明其默认值/范围即视为未列出）。

| 字段 | 类型 | 必填/默认 | 取值 | 说明 |
| --- | --- | --- | --- | --- |
| `model` | string | 必填 | 模型 ID | 支持通义千问大语言模型（商业版与开源版）、Qwen-VL、Qwen-Coder、Qwen-Omni、Qwen-Math、DeepSeek（阿里云直供 / 硅基流动直供 / 快手万擎直供）、Kimi（阿里云直供 / 月之暗面直供）、GLM（阿里云直供）、MiniMax（阿里云直供 / 稀宇科技直供）。三方直供模型需先在模型市场开通对应服务 |
| `messages` | array\<object> | 必填 | system / user / assistant / tool 四类消息 | 结构见下方「messages 消息结构」 |
| `stream` | boolean | 默认 `false` | `true` / `false` | 流以 `data: [DONE]` 收尾（见第 5 节） |
| `stream_options` | object | 可选 | `include_usage` | 仅在 `stream=true` 时生效；`include_usage`（boolean，默认 `false`）表示最后一个数据块是否返回 Token 用量 |
| `modalities` | array\<string> | 默认 `["text"]` | `["text"]` 或 `["text","audio"]` | 输出模态，仅 Qwen-Omni 模型；`qwen3.8-omni-flash` 仅输出文本 |
| `audio` | object | 可选（`required=["voice","format"]`） | `voice`、`format` | 输出音频的音色与格式，仅 Qwen-Omni 且 `modalities=["text","audio"]`；`format` 目前仅支持 `wav` |
| `temperature` | number | 可选 | `>=0` 且 `<2` | 与 `top_p` 只需设其一；QVQ 模型请勿修改默认温度值 |
| `top_p` | number | 可选 | `(0, 1.0]` | — |
| `top_k` | integer | 可选 | `>=0` 整数 | 采样候选 Token 数；为 `null` 或大于 100 时 `top_k` 禁用，仅 `top_p` 生效 |
| `presence_penalty` | number | 可选 | `-2.0` ~ `2.0` | 正值降低重复内容 |
| `response_format` | object | 默认 `{"type":"text"}`，`required=["type"]` | `type` ∈ `text` / `json_object` / `json_schema`；`json_schema` 含 `name`（必填，≤64 字符）、`description`、`schema`、`strict`（默认 `false`） | — |
| `max_tokens` | integer | 可选 | — | **即将废弃**，新接入请用 `max_completion_tokens`。含义随模型不同：对 deepseek-v4.1-flash、deepseek-v4-pro、deepseek-v4-pro-0813、deepseek-v4-flash、deepseek-v4-flash-0731、qwen3.8-max 为「回答 + 思维链」之和上限，其他模型仅限制回复长度 |
| `max_completion_tokens` | integer | 可选（默认值 = 最大值 = 模型最大输出长度） | — | 限制本次响应输出总 Token 数（**含思维链**），达上限停止且 `finish_reason=length`；思考类模型推荐使用 |
| `vl_high_resolution_images` | boolean | 默认 `false` | `true` / `false` | 将输入图片像素上限提高到对应 16384 Token 的像素值；启用后采用固定分辨率策略，`max_pixels` 被忽略 |
| `n` | integer | 默认 `1` | `1`–`4` | 生成的候选响应数；仅 Qwen3（非思考模式）支持；传 `tools` 时须设为 1 |
| `enable_thinking` | boolean | 可选 | `true` / `false` | 为混合思考模型开启思考模式，支持 Qwen3.7 / 3.6 / 3.5 / Qwen3 / Qwen3-Omni-Flash / Qwen3-VL 及部分 DeepSeek、Kimi（详见第 6 节） |
| `preserve_thinking` | boolean | 默认 `false` | `true` / `false` | 是否把历史 assistant 消息的 `reasoning_content` 拼接进模型输入；支持型号见第 6 节 |
| `thinking_budget` | integer | 可选（默认 = 模型最大思维链长度） | 正整数 | 思考过程最大 Token 数；**非 OpenAI 标准参数**，Python SDK 需通过 `extra_body` 传入；`kimi-k3` 不支持 |
| `reasoning_effort` | string | 默认 `"high"` | Qwen3.8 系列：`low` / `medium` / `xhigh`（默认 `xhigh`） | 控制推理力度；与 `thinking_budget` 不能同时设置，同时设置会报错；两者可互转（未设 `thinking_budget` 时 `low`→4096、`medium`→16384、`xhigh`→按模型） |
| `clear_thinking` | boolean | 默认 `false` | `true` / `false` | 多轮对话中是否忽略历史轮次 `reasoning_content`；**仅 GLM 系列**（glm-5.3 / 5.2 / 5.1 / 5 / 4.7），其中 `glm-5.3` 默认 `true` |
| `tool_stream` | boolean | 默认 `false` | `true` / `false` | 仅在 `stream=true` 时生效，当前仅 Qwen 与 GLM 系列支持（影响复杂工具参数的流式输出） |
| `enable_code_interpreter` | boolean | 默认 `false` | `true` / `false` | 是否启用代码解释器；**非 OpenAI 标准参数**，需 `extra_body` |
| `seed` | integer | 可选 | `[0, 2^31-1]` | 随机种子，用于结果复现 |
| `logprobs` | boolean | 默认 `false` | `true` / `false` | 是否返回输出 Token 的对数概率；思考阶段（`reasoning_content`）不含对数概率；支持 Qwen-plus/Qwen-turbo 系列快照版（不含稳定版）、qwen3-vl-plus（含稳定版）、qwen3-vl-flash（含稳定版）、Qwen3 开源模型等 |
| `top_logprobs` | integer | 默认 `0` | `0`–`5` | 每步返回的候选 Token 数，仅在 `logprobs=true` 时生效 |
| `stop` | string 或 array\<string> | 可选 | — | 停止词；为数组时不能同时包含 `token_id` 与字符串元素 |
| `tools` | array\<object> | 可选 | `type`（固定 `function`）、`function.name`（必填，≤64 Token）、`function.description`（必填）、`function.parameters`（JSON Schema，默认 `{}`） | 模型可调用的工具数组 |
| `tool_choice` | string 或 object | 默认 `"auto"` | `auto` / `none` / `required` 或 `{"type":"function","function":{"name":...}}` | 工具选择策略；Qwen 系列暂不支持 `required`（非思考模式无法保证调用，思考模式当前不支持） |
| `parallel_tool_calls` | boolean | 默认 `false` | `true` / `false` | 是否启用并行工具调用 |
| `enable_search` | boolean | 默认 `false` | `true` / `false` | 启用联网搜索；**非 OpenAI 标准参数**，需 `extra_body`；开启后可能增加 Token 消耗 |
| `search_options` | object | 可选（仅 `enable_search=true` 时生效） | `forced_search`（boolean，默认 `false`）、`search_strategy`（enum `turbo` / `max` / `agent` / `agent_max`，默认 `turbo`）、`enable_search_extension`（boolean，默认 `false`） | 联网搜索策略；`agent` / `agent_max` 仅适用于部分模型（qwen3.7-max 系列、qwen3.5-plus 系列等） |

本家四套 OpenAPI 规格中未出现的基线字段：`user` / `user_id`、`frequency_penalty`。

**messages 消息结构**（`messages` 为 `oneOf` 四类对象）：

| 消息 | 必填字段 | 说明 |
| --- | --- | --- |
| system（`title: System message`） | `role`（固定 `system`）、`content`（string） | 应置于数组开头；QwQ 模型请勿设置系统消息，系统消息对 QVQ 模型无效 |
| user（`title: User message`） | `role`（固定 `user`）、`content`（string 或内容块数组） | 纯文本时为字符串；多模态输入或启用显式缓存时为数组 |
| assistant（`title: Assistant message`） | `role`（固定 `assistant`） | 可选 `content`（string）、`partial`（boolean，默认 `false`，部分模式）、`tool_calls`（数组，元素含 `id`、`type`（固定 `function`）、`function.name`、`function.arguments`、`index`）；含 `tool_calls` 时 `content` 可为空 |
| tool（`title: Tool message`） | `role`（固定 `tool`）、`content`（string）、`tool_call_id` | 工具输出，必须为字符串；`tool_call_id` 取自 `completion.choices[0].message.tool_calls[$index].id` |

**user 消息内容块数组元素**（多模态 / 显式缓存）：

| 字段 | 类型 | 必填 | 取值 | 说明 |
| --- | --- | --- | --- | --- |
| `type` | string | 必填 | `text` / `image_url` / `input_audio` / `video` / `video_url` | 内容类型；`video` 为图片列表形式的视频，`video_url` 为视频文件 |
| `text` | string | `type=text` 时必填 | — | 输入文本 |
| `image_url` | object | `type=image_url` 时必填（`required=["url"]`） | `url` | 输入图片 |
| `input_audio` | object | `type=input_audio` 时必填（`required=["data","format"]`） | `data`、`format` | 输入音频 |
| `video` | array\<string> | `type=video` 时必填 | 图片 URL 列表 | 以图片列表表示的视频 |
| `video_url` | object | `type=video_url` 时必填（`required=["url"]`） | `url` | 视频文件 |
| `fps` | number | 可选 | `[0.1, 10]`，默认 `2.0` | 抽帧频率，并告知模型相邻帧时间间隔 |
| `min_pixels` / `max_pixels` / `total_pixels` | integer | 可选 | 按模型不同 | 图片/视频帧的最小、最大像素阈值与视频总像素上限；具体默认值随模型不同 |
| `cache_control` | object | 可选（`required=["type"]`） | `type: ephemeral` | 启用显式缓存 |

**DashScope 原生协议请求字段**（`POST /api/v1/services/aigc/text-generation/generation`，请求体 `required=["model","input"]`）：参数全部位于 `input`（含 `messages`）与 `parameters` 两个对象内，**不叫 `messages` / `temperature` 顶层字段**。

| 字段 | 类型 | 必填/默认 | 取值 | 说明 |
| --- | --- | --- | --- | --- |
| `model` | string | 必填 | 模型 ID | 支持 Qwen 大语言模型（商业版与开源版）、Qwen-Coder、数学模型、DeepSeek（阿里云直供 / 硅基流动直供）、Kimi（阿里云直供）、GLM（阿里云直供）、MiniMax（阿里云直供 / 稀宇科技直供） |
| `input.messages` | array\<object> | 必填 | system / user / assistant / tool | 对话上下文；assistant 消息支持 `tool_calls` 与 `partial`（前缀续写）；tool 消息含 `content`、`tool_call_id` |
| `parameters.result_format` | string | 默认 `text` | `message` / `text` | 返回格式；多轮对话请设 `message`；多数模型默认 `text`，Qwen3-Max、Qwen3-VL、QwQ、Qwen3 开源（除 qwen3-next-80b-a3b-instruct）默认 `message`；Qwen-VL/QVQ 设 `text` 不生效 |
| `parameters.temperature` | number | 可选 | `[0, 2)`（规格 `minimum=0`、`exclusiveMaximum=2`） | 采样温度；QVQ 模型请勿修改默认值 |
| `parameters.top_p` | number | 可选 | `(0, 1.0]`（`maximum=1`、`exclusiveMinimum=0`） | 核采样阈值 |
| `parameters.top_k` | integer | 可选 | `minimum=0` | 候选 Token 数；为 `None` 或大于 100 时失效 |
| `parameters.max_tokens` | integer | 可选 | — | **即将废弃**；GLM-5.2 及之后行为等同 `max_completion_tokens` |
| `parameters.max_completion_tokens` | integer | 可选 | — | 本次响应输出上限（含思维链） |
| `parameters.stream` | boolean | 默认 `false` | `true` / `false` | 流式；HTTP 还需 `X-DashScope-SSE: enable` |
| `parameters.incremental_output` | boolean | 默认 `false` | `true` / `false` | 流式时只返回新增 Token（`true`）还是累计全文（`false`） |
| `parameters.enable_thinking` | boolean | 可选 | `true` / `false` | 思考模式开关；适用型号见第 6 节 |
| `parameters.preserve_thinking` | boolean | 默认 `false` | `true` / `false` | 是否拼接历史 `reasoning_content` |
| `parameters.thinking_budget` | integer | 可选 | — | 思考链最大长度；HTTP 调用须放在 `parameters` 内 |
| `parameters.reasoning_effort` | string | 可选 | Qwen3.8 系列：`low` / `medium` / `xhigh`（默认 `xhigh`） | 推理力度；不能与 `thinking_budget` 同时设置 |
| `parameters.clear_thinking` | boolean | 默认 `false` | `true` / `false` | 仅 GLM 系列；`glm-5.3` 默认 `true` |
| `parameters.tool_stream` | boolean | 默认 `false` | `true` / `false` | 仅影响复杂工具参数（含 array/object）的流式输出 |
| `parameters.enable_code_interpreter` | boolean | 默认 `false` | `true` / `false` | 代码解释器 |
| `parameters.repetition_penalty` | number | 可选 | 正数，`1.0` 表示不惩罚 | Token 重复惩罚 |
| `parameters.presence_penalty` | number | 可选 | `[-2.0, 2.0]` | 重复内容控制 |
| `parameters.seed` | integer | 可选 | `[0, 2^31-1]`（`minimum=0`） | 随机种子 |
| `parameters.stop` | string 或 array\<string 或 integer> | 可选 | — | 停止序列；同数组不要混用字符串与 token ID |
| `parameters.tools` | array\<object> | 可选 | `type`（固定 `function`）、`function.name`（≤64 字符）、`function.description`、`function.parameters` | 使用工具时须把 `result_format` 设为 `message`；不支持 qwen-vl 系列模型 |
| `parameters.tool_choice` | string 或 object | 默认 `"auto"` | `auto` / `none` / `required`，或 `{"type":"function","function":{"name":...}}` | 思考模式的模型不支持强制指定工具 |
| `parameters.parallel_tool_calls` | boolean | 默认 `false` | `true` / `false` | 思考模式模型在强制指定工具时不支持 |
| `parameters.response_format` | object | 默认 `{"type":"text"}` | `type` ∈ `text` / `json_object` / `json_schema`；`json_schema` 含 `name`、`description`、`schema`、`strict`（默认 `false`） | 设为 `json_object` 时必须在提示词中要求输出 JSON |
| `parameters.logprobs` | boolean | 默认 `false` | `true` / `false` | 支持 qwen-plus / qwen-turbo 快照模型、qwen3-vl-plus / qwen3-vl-flash 系列、Qwen3 开源模型 |
| `parameters.top_logprobs` | integer | 默认 `0` | `0`–`5` | 仅在 `logprobs=true` 时生效 |
| `parameters.n` | integer | 默认 `1` | `1`–`4` | 仅非思考模式 Qwen3；指定 `tools` 时固定为 1 |
| `parameters.vl_high_resolution_images` | boolean | 默认 `false` | `true` / `false` | 高分辨率图像处理，启用后 `max_pixels` 被忽略 |
| `parameters.vl_enable_image_hw_output` | boolean | 默认 `false` | `true` / `false` | 是否在响应中返回缩放后图像尺寸 `image_hw`（流式时在最后一个数据块返回） |
| `parameters.enable_search` | boolean | 默认 `false` | `true` / `false` | 联网搜索参考 |
| `parameters.search_options` | object | 可选 | `enable_source`、`enable_citation`、`citation_format`（enum `[<number>]` / `[ref_<number>]`，默认 `[<number>]`）、`forced_search`、`search_strategy`（enum `turbo` / `max` / `agent` / `agent_max`，默认 `turbo`）、`enable_search_extension`、`prepend_search_result` | 均默认 `false`（除 `citation_format` 与 `search_strategy`）；`prepend_search_result` 暂不支持 DashScope Java SDK |

DashScope 多模态端点（`POST /api/v1/services/aigc/multimodal-generation/generation`）：`input.messages` 为 user / assistant 两类，user 的 `content` 为**内容部分数组**（`text` / `image` / `video` / `file` / `cache_control`），`parameters` 另有 `result_format`（多模态仅支持 `message`）、`max_frames` 等；详见第 8 节。

---

### 3. Responses API 字段差异

以 `openapi-openai-responses.json`（`POST /compatible-mode/v1/responses`）为准。请求体 `required = ["model","input"]`。

下表只列本家与《通用基线》§0.6 不同的字段与取值；未列出的标准字段语义同基线（本家文档未写明其默认值/范围即视为未列出）。

规格与页面声明的兼容性差异（原文）：

- 请求**仅处理文档中列出的参数**，未提及的 OpenAI 参数被忽略。
- 不支持的参数：如 `background`（仅支持同步调用）。
- 思考强度通过 `reasoning.effort` 控制；`enable_thinking` 仍可用但**已不推荐**。
- **最大输入上下文约为模型窗口大小的 80%**（预留约 20% 给内置工具调用与推理），超出自动截断、不报错中断。

| 字段 | 类型 | 必填/默认 | 取值 | 说明 |
| --- | --- | --- | --- | --- |
| `model` | string | 必填 | 模型 ID | 支持 qwen3.8-max 系列、qwen3.8-flash 等 |
| `input` | string 或 array\<object> | 必填 | 纯文本字符串，或 9 类消息对象数组 | — |
| `instructions` | string | 可选 | — | 插入上下文开头的系统指令；使用 `previous_response_id` 时上一轮的 instructions **不会延续** |
| `previous_response_id` | string | 可选 | Response ID（有效期 **7 天**） | 多轮对话；与 `conversation` 不能同时使用 |
| `conversation` | string | 可选 | 会话 ID | 会话历史自动作为上下文，本请求输入输出自动加入会话；与 `previous_response_id` 不能同时使用 |
| `stream` | boolean | 默认 `false` | `true` / `false` | — |
| `store` | boolean | 默认 `true` | `true` / `false` | 是否存储本次响应，供 `previous_response_id` 及后续 API 使用 |
| `tools` | array\<object> | 可选 | `type` ∈ `web_search` / `code_interpreter` / `web_extractor` / `web_search_image` / `image_search` / `file_search` / `mcp` / `function` | 工具列表；见第 7 节字段说明 |
| `tool_choice` | string 或 object | 可选（字符串默认 `"auto"`） | 字符串 `auto` / `none` / `required`（`required` 仅当 tools 只有 1 个时可用）；对象含 `mode`（`auto` / `required`）、`tools`、`type`（固定 `allowed_tools`） | 工具选择 |
| `temperature` | number | 可选 | `[0, 2)` | 采样温度 |
| `top_p` | number | 可选 | `(0, 1.0]` | 核采样阈值 |
| `enable_thinking` | boolean | 可选 | `true` / `false` | 是否启用思考；**非标准参数**，思考内容通过 `reasoning` 类型输出项返回，推理 Token 计入 `output_tokens_details.reasoning_tokens` 并计费 |
| `reasoning` | object | 可选 | `effort`（enum `none` / `minimal` / `low` / `medium` / `high` / `xhigh` / `max`，默认 `xhigh`） | 7 个递增档位；Qwen3.8 系列默认 `xhigh`，支持 `none` / `low` / `medium` / `xhigh`（`minimal` 映射为 `low`，`high` 与 `max` 有各自映射） |
| `max_output_tokens` | integer | 可选，`minimum = 16` | — | 输出 Token 上限；Qwen3.8 系列为「回复 + 思维链」之和上限，其余模型仅限制回复；超限状态为 `incomplete` |
| `ocr_options` | object | 可选 | 含 `task` 等（`task=document_parsing` 时 PDF 最大 50 页，未设或其他任务最大 10 页） | OCR 内置任务参数，**仅适用于 `qwen3.5-ocr`**；结果通过响应 `ocr_result` 字段返回；非 OpenAI 标准参数 |

`input` 数组的元素类型（9 类，与 Chat 的 `messages` 结构差异明显）：

| 元素 | 关键字段 | 说明 |
| --- | --- | --- |
| 系统消息 | `role`（固定 `system`）、`content`、`type`（可选，固定 `message`） | 系统指令 |
| 开发者消息 | `role`（固定 `developer`）、`content`、`type` | 系统级指令 |
| 用户消息 | `role`（固定 `user`）、`content` | 仅文本时为字符串；含图片/文件或显式缓存时为数组 |
| 助手消息 | `role`（固定 `assistant`）、`content` | 传递模型此前回复 |
| 响应输出消息 | `type`（固定 `message`）、`id`、`role`、`status`（`in_progress` / `completed` / `incomplete`）、`content`（元素 `type=output_text`、`text`、`annotations`） | 直接把上一轮 `output` 中的 message 项回传 |
| 函数调用 | `type`（固定 `function_call`）、`name`、`arguments`、`call_id`、可选 `id`、`status` | 回传上一轮 `output` 中的 function_call 项 |
| 函数调用输出 | `type`（固定 `function_call_output`）、`call_id`、`output`、可选 `id`、`status` | 必须紧跟对应的 `function_call` 消息 |
| 推理 | `type`（固定 `reasoning`）、`id`、`summary`（元素 `type=summary_text`、`text`）、可选 `status` | 回传 `output` 中的 reasoning 项以传递思考内容 |
| 网页搜索调用 | `type`（固定 `web_search_call`）、`id`、`status`（`in_progress` / `searching` / `completed` / `failed`）、`action`（`type=search`、可选 `queries`、`sources`） | 回传搜索结果上下文 |

其他差异要点：

- `tools` 采用扁平结构、工具调用以 `function_call` 条目出现在响应 `output` 数组中（均见《通用基线》§0.6）。
- 另有会话缓存：Responses API 配合 `previous_response_id` 时使用请求头 `x-dashscope-session-cache: enable`（见第 9 节）。

---

### 4. Anthropic 兼容端点字段差异

以 `openapi-anthropic.json`（`POST /apps/anthropic/v1/messages`）为准。请求体 `required = ["model","max_tokens","messages"]`。

下表只列本家与《通用基线》§0.7 不同的字段与取值；未列出的标准字段语义同基线（本家文档未写明其默认值/范围即视为未列出）。

| 字段 | 类型 | 必填/默认 | 取值 | 说明 |
| --- | --- | --- | --- | --- |
| `model` | string | 必填 | 模型 ID | 覆盖千问 Max / Plus / Flash / VL 与部分三方模型（具体清单见规格 `model` 描述） |
| `max_tokens` | integer | 必填 | — | 回复最大 Token 数，超限时 `stop_reason = max_tokens`；对部分模型为「回复 + 思维链」之和上限 |
| `system` | string 或 array\<object> | 可选 | 数组元素 `type`（固定 `text`）、`text`、`cache_control`（`type: ephemeral`） | 系统提示词在**顶层**传入，`messages` 数组不接受 `system` 角色；字符串等价单个 text 块；为标记显式缓存断点必须传数组 |
| `messages` | array\<object> | 必填 | `role` ∈ `user` / `assistant` / `system`，`content` 为 string 或内容块数组 | `messages[].content` 块 `type` ∈ `text` / `image` / `video` / `tool_use` / `tool_result`；块字段 `text`、`source`（`type` ∈ `url` / `base64`，配 `url` 或 `media_type` + `data`）、`id` / `name` / `input`（tool_use）、`tool_use_id`（tool_result）、`cache_control` |
| `stream` | boolean | 默认 `false` | `true` / `false` | — |
| `temperature` | number | 可选 | `[0, 2)` | **取值范围与 Anthropic 官方 `[0.0, 1.0]` 不同**，从 Anthropic 迁移需确认；`qwen3.8-max` 思考模式默认 0.6，传入更小值自动提高到 0.6 |
| `top_p` | number | 可选 | — | 建议与 `temperature` 只设其一 |
| `top_k` | integer | 可选 | — | 采样候选集大小 |
| `stop_sequences` | array\<string> | 可选 | — | 命中后 `stop_reason = stop_sequence` 并在响应 `stop_sequence` 回填命中序列 |
| `thinking` | object | 可选 | `type` ∈ `enabled` / `disabled`；`budget_tokens` | 深度思考配置，开启后响应包含 `thinking` 类型内容块；`budget_tokens` **即将废弃**，新接入建议改用 `output_config.effort`；与 `max_tokens` 互不重叠 |
| `tools` | array\<object> | 可选 | `name`（必填）、`description`、`input_schema`（必填，JSON Schema） | 工具定义 |
| `tool_choice` | object | 可选 | `{"type":"auto"}`（默认）/ `{"type":"any"}` / `{"type":"none"}` / `{"type":"tool","name":"..."}` | 工具选择策略 |
| `output_config` | object | 可选 | `effort`（enum `high` / `max` / `low` / `medium` / `xhigh`）、`format`（`required=["type","schema"]`，`type` 固定 `json_schema`） | 输出参数；`effort` 各模型默认值/有效值不同（如 glm-5.3 默认 `max`）；`format` 启用后返回 JSON 字符串 |

响应与流式差异：

- 非流式响应：`id`、`type`（固定 `message`）、`role`（固定 `assistant`）、`model`、`content`（元素 `type` ∈ `text` / `thinking` / `tool_use`，其中 `thinking` 块另有 `signature`，当前固定为空字符串）、`stop_reason`（enum `end_turn` / `max_tokens` / `tool_use`）、`stop_sequence`（固定 `null`）、`usage`。
- 流式响应：事件对象 `type` ∈ `message_start` / `content_block_start` / `content_block_delta` / `content_block_stop` / `message_delta` / `message_stop` / `ping`，另有 `index`、`message`、`content_block`、`delta`、`usage`；`content_block_delta` 的 `delta.type` 取 `text_delta`（含 `text`）、`thinking_delta`（含 `thinking`）等。
- `usage` 四个字段：`input_tokens`、`output_tokens`、`cache_creation_input_tokens`、`cache_read_input_tokens`；流式调用中 `message_start` 事件只含前两个，**完整 4 个字段在 `message_delta` 事件返回**。
- 错误响应体形状与其他协议不同：`{"type":"error","error":{"type":"...","message":"..."}}`，`error.type` 例如 `invalid_request_error`、`authentication_error`、`rate_limit_error`。

---

### 5. 响应字段与流式结构

**Chat Completions 非流式响应**（`object = "chat.completion"`）：公共顶层字段与 `choices[]` 结构见《通用基线》§0.5；本家另有 `service_tier`、`system_fingerprint`（均固定 `null`），`choices[].logprobs` 可为 `null`，`finish_reason` enum `stop` / `length` / `tool_calls`。

`choices[].message` 字段：

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `content` | string | 模型响应文本 |
| `reasoning_content` | string | 思维链推理过程 |
| `refusal` | string / null | 固定为 `null` |
| `role` | string | 固定为 `assistant` |
| `audio` | object / null | 固定为 `null` |
| `function_call` | object / null | 已废弃，固定为 `null`，改用 `tool_calls` |
| `tool_calls` | array\<object> | `id`、`type`（`function`）、`function.name`、`function.arguments`（JSON 字符串）、`index` |

`choices[].logprobs.content[]`：`token`、`bytes`（原始 UTF-8 字节数组）、`logprob`（可为 `null`）、`top_logprobs[]`（同样含 `token` / `bytes` / `logprob`）。

**Chat Completions 流式（SSE）**：每个事件是一行 `data:`，内容为 JSON chunk（公共形态见《通用基线》§0.5）；`object = "chat.completion.chunk"`；**最后一条为 `data: [DONE]`**；同一响应所有数据块共享同一 `id`。chunk 顶层另有 `service_tier`、`system_fingerprint`。`choices[]` 含 `delta`、`finish_reason`（生成中为 `null`）、`index`、`logprobs`（可为 `null`）；结束数据块的 `delta.content` 为空串并给出 `finish_reason`。`delta` 字段：

| 字段 | 说明 |
| --- | --- |
| `content` | 增量消息内容 |
| `reasoning_content` | 增量思维链内容 |
| `function_call` | 默认 `null`，改用 `tool_calls` |
| `audio` | Qwen-Omni 音频输出：`data`（增量 Base64 音频）、`expires_at` |
| `refusal` | 固定 `null` |
| `role` | 仅在第一个数据块出现 |
| `tool_calls` | 含 `index`、`id`、`function.name`（仅第一个数据块）、`function.arguments`（增量片段，需拼接） |

若 `stream_options.include_usage = true`，**最后一个数据块中 `choices` 为空数组**，只带 `usage`。

**思考模型的流式是两阶段**：先 `delta.reasoning_content`（思考），再 `delta.content`（回答）；开启思考后分为三阶段——思考 token、工具调用 delta、（发送工具结果后）最终回答。

**Responses API 非流式响应**：`id`（有效期 7 天，可作 `previous_response_id`）、`created_at`（秒）、`object`（`response`）、`status`（enum `completed` / `failed` / `in_progress` / `cancelled` / `queued` / `incomplete`）、`model`、`output`（数组）、`parallel_tool_calls`、`tool_choice`（回显）、`tools`（回显，结构与请求相同）、`error`（成功时为 `null`，含 `code` / `message`）、`usage`、`x_tools`（内置工具调用次数统计，如 `{"web_search": {"count": 1}}`）。

`output[]` 元素的 `type` ∈ `message` / `reasoning` / `function_call` / `web_search_call` / `code_interpreter_call` / `web_extractor_call` / `web_search_image_call` / `image_search_call` / `mcp_call` / `file_search_call`；公共字段 `id`、`status`（`completed` / `in_progress`）；按类型出现的字段包括 `role`（仅 message）、`name` / `arguments` / `call_id`（function_call 等）、`content`（message，元素 `type=output_text`、`text`、`annotations`）、`summary`（reasoning，元素 `type=summary_text`、`text`）、`action`（web_search_call，含 `query`、`type=search`、`sources[]`（`type`、`url`））、`code` / `outputs`（`type=logs`、`logs`）/ `container_id`（code_interpreter_call）、`goal` / `output` / `urls`（web_extractor_call）、`output`（web_search_image_call / image_search_call，图片结果 JSON 字符串，元素含 `title` / `url` / `index`）、`server_label`（mcp_call）、`queries` / `results`（file_search_call，结果元素含 `file_id` / `filename` / `score` / `text`）。

`GET /compatible-mode/v1/responses/{response_id}` 返回同一响应对象；`DELETE` 返回 `{"id": ..., "deleted": true}`；`GET .../input_items` 返回 `data[]`（`id`、`role` ∈ `user` / `assistant`、`content[]`（`type` ∈ `input_text` / `output_text`、`text`）、`type`（固定 `message`）、`status`（固定 `completed`））、`first_id`、`last_id`、`has_more`、`id`、`model`、`created_at`（**毫秒**，与创建/获取响应的秒不同）、`previous_response_id`。

**Responses API 流式（SSE）**：事件顶层字段 `type`、`sequence_number`（从 0 递增）、`response`（出现在 `response.created` / `response.in_progress` / `response.completed` / `response.incomplete`，后两者含完整响应数据）、`item`（`response.output_item.added` / `response.output_item.done`，added 中为骨架、`content` 为空数组）、`part`（`response.content_part.added` / `response.content_part.done`，`type` 固定 `output_text`，含 `text`、`annotations`、`logprobs`（当前为 `null`））、`delta`（`response.output_text.delta` 的增量文本）、`text`（`response.output_text.done` 的完整文本）、`item_id`、`output_index`、`content_index`。

规格中出现的全部事件名（共 27 个）：

`response.created`、`response.in_progress`、`response.completed`、`response.incomplete`、`response.output_item.added`、`response.output_item.done`、`response.content_part.added`、`response.content_part.done`、`response.output_text.delta`、`response.output_text.done`、`response.reasoning_text.delta`、`response.reasoning_text.done`、`response.custom_tool_call_input.delta`、`response.custom_tool_call_input.done`、`response.web_search_call.in_progress`、`response.web_search_call.searching`、`response.web_search_call.completed`、`response.code_interpreter_call.in_progress`、`response.code_interpreter_call.interpreting`、`response.code_interpreter_call.completed`、`response.mcp_call.in_progress`、`response.mcp_call.completed`、`response.mcp_call_arguments.delta`、`response.mcp_call_arguments.done`、`response.file_search_call.in_progress`、`response.file_search_call.searching`、`response.file_search_call.completed`。

工具调用参数在 Responses 流式中通过 `response.function_call_arguments.delta` 事件分片推送，持续拼接至 `response.function_call_arguments.done`。

**DashScope 原生响应**：顶层 `status_code`（`200` 表示成功；Java SDK 不返回，失败抛异常）、`request_id`、`code`（成功时为空字符串，仅 Python SDK 返回）、`message`（成功时为空字符串）、`output`、`usage`。`output.text`（`result_format=text` 时）与 `output.finish_reason`（`null` 仍生成中 / `stop` / `length` / `tool_calls`）；`result_format=message` 时用 `output.choices[]`，每项含 `finish_reason`、`message`（`role` 固定 `assistant`、`content`（文本模型为字符串，Qwen-VL/Qwen-Audio 为数组，元素可含 `text` 与 `image_hw`）、`reasoning_content`、`tool_calls`（`id`、`type`、`function.name`、`function.arguments`、`index`））、`logprobs`。

**usage 明细与缓存计数（各协议差异）**：

| 协议 | usage 字段 |
| --- | --- |
| Chat Completions | `prompt_tokens`、`completion_tokens`、`total_tokens`、`completion_tokens_details`（`audio_tokens`、`reasoning_tokens`（`text_tokens` 的子集）、`text_tokens`（已含 `reasoning_tokens`））、`prompt_tokens_details`（`audio_tokens`、`cached_tokens`、`text_tokens`、`image_tokens`、`video_tokens`、`cache_creation.ephemeral_5m_input_tokens`、`cache_creation_input_tokens`、`cache_type`（显式缓存时为 `ephemeral`，否则字段不存在）） |
| Responses API | `input_tokens`、`output_tokens`、`total_tokens`、`input_tokens_details.cached_tokens`、`output_tokens_details.reasoning_tokens`、`x_details[]`（`input_tokens`、`output_tokens`、`total_tokens`、`x_billing_type`（固定 `response_api`）、`image_tokens`、`input_tokens_details`（`text_tokens`、`image_tokens`、`cached_tokens`、`cache_creation_input_tokens`、`cache_creation.ephemeral_5m_input_tokens`、`cache_type`（固定 `ephemeral`））、`output_tokens_details`（`reasoning_tokens`、`text_tokens`）、`plugins.web_search.count`）、`x_tools` |
| Anthropic 兼容 | `input_tokens`、`output_tokens`、`cache_creation_input_tokens`、`cache_read_input_tokens` |
| DashScope 原生 | `input_tokens`、`output_tokens`、`total_tokens`、`image_tokens`、`video_tokens`、`audio_tokens`、`input_tokens_details`（`text_tokens` / `image_tokens` / `video_tokens`）、`output_tokens_details`（`text_tokens`、`reasoning_tokens`）、`prompt_tokens_details`（`cached_tokens`、`cache_creation_input_tokens`、`cache_type`、`cache_creation.ephemeral_5m_input_tokens`） |

结束标记：OpenAI 兼容流以 `data: [DONE]` 结束；Anthropic 兼容流以 `message_stop` 事件结束；Responses 流以 `response.completed` / `response.incomplete` / `response.failed` 事件承载最终状态。

流式注意事项（原文）：Nginx 需 `proxy_buffering off`；非流式调用最大超时不少于 300 秒；**QwQ 与 QVQ 仅支持流式输出**，非流式会失败或返回空内容。

---

### 6. 思考/推理字段

各协议拼写与取值：

| 协议 | 开关 | 档位/预算 | 单独说明 |
| --- | --- | --- | --- |
| Chat Completions | `enable_thinking`（boolean） | `thinking_budget`（integer）、`reasoning_effort`（string，默认 `"high"`；Qwen3.8 系列 `low` / `medium` / `xhigh`，默认 `xhigh`） | `preserve_thinking`（默认 `false`）、`clear_thinking`（默认 `false`，仅 GLM）、思考内容在 `message.reasoning_content` / `delta.reasoning_content` |
| Responses API | `enable_thinking`（**已不推荐**） | `reasoning.effort`（枚举 `none` / `minimal` / `low` / `medium` / `high` / `xhigh` / `max`，默认 `xhigh`，共 7 档递增） | 思考内容为 `reasoning` 类型输出项；流式事件 `response.reasoning_text.delta` / `response.reasoning_text.done`；推理 Token 计入 `output_tokens_details.reasoning_tokens` |
| Anthropic 兼容 | `thinking.type`（enum `enabled` / `disabled`） | `thinking.budget_tokens`（**即将废弃**）；新接入用 `output_config.effort`（enum `high` / `max` / `low` / `medium` / `xhigh`，各模型默认值不同，如 glm-5.3 默认 `max`） | 响应返回 `thinking` 类型内容块（含 `signature`，当前固定空字符串） |
| DashScope 原生 | `parameters.enable_thinking` | `parameters.thinking_budget`、`parameters.reasoning_effort`（Qwen3.8 系列 `low` / `medium` / `xhigh`，默认 `xhigh`） | `parameters.preserve_thinking`、`parameters.clear_thinking`；思考内容在 `output.choices[].message.reasoning_content` |

限制与默认值（文档明确给出）：

- `reasoning_effort` 与 `thinking_budget` **不能同时设置**，同时设置会报错；两者支持互转：未设置 `thinking_budget` 时，`reasoning_effort` 自动映射 `thinking_budget`（`low`→4096，`medium`→16384，`xhigh`→按模型）。
- `thinking_budget` 适用 Qwen3.8 / 3.7 / 3.6 / 3.5 / Qwen3-VL / Qwen3、GLM（千问AI平台直供）与 Kimi（千问AI平台直供）系列，其中 `kimi-k3` 不支持；**仅 Chat Completions 与 DashScope 支持，Responses API 暂不支持**。
- `enable_thinking` 适用 Qwen3.7 / 3.6 / 3.5 / Qwen3 / Qwen3-Omni-Flash / Qwen3-VL，以及 DeepSeek-V4.1-Flash、DeepSeek-V4-Pro / V4-Flash（阿里云直供）、DeepSeek-V3.2 / V3.2-exp / V3.1（阿里云直供、硅基流动直供、快手万擎直供）等。
- `preserve_thinking` 支持 `qwen3.8-max`（默认开启）、`qwen3.8-max-0902`（默认开启）、`qwen3.8-omni-flash`（默认开启）、qwen3.7-max / -2026-06-08 / -2026-05-20 / -preview / -2026-05-17、qwen3.7-plus / -2026-05-26、qwen3.6-max-preview、qwen3.6-plus / -2026-04-02、qwen3.7-flash / -2026-07-15、`kimi-k2.7-code`、`kimi-k2.6`（千问AI平台部署）、`kimi/kimi-k3`、`kimi/kimi-k2.7-code-highspeed`、`kimi/kimi-k2.7-code`、`kimi/kimi-k2.6`（月之暗面部署）。Java SDK 不支持该参数；启用后历史 `reasoning_content` 计入输入 Token 并计费。
- 思考模式下 `max_tokens` 有效范围为 `[1, 32768]`，超出返回 400（`InvalidParameter: Range of max_tokens should be [1, 32768]`）；`max_completion_tokens` 无此上限。思维链预算示例：qwen3.8-flash 与 qwen3.7-flash 的 `thinking_budget` 上限 256K，qwen3.6-flash 为 128K（各模型「思考预算」列见第 12 节）。
- 模式分类（思考页）：**混合模式**（可按请求开关，默认值分「默认开启」与「默认关闭」两种）与**纯推理模式**（始终推理、无法关闭）。例如 qwen3.8-max 系列为混合模式默认开启；`qwen3.8-2.4t-a95b` 仅支持思考模式；`qwq-plus` 为纯推理模式；GLM 中 `glm-5.3` 仅思考模式，`glm-5.2` 及以下混合模式默认开启；Kimi 中 `kimi-k3`、`kimi-k2.7-code` 仅思考模式，`kimi-k2.6`、`kimi-k2.5` 混合模式默认关闭。
- 第三方特殊拼写：`MiniMax/MiniMax-M3` 通过 `thinking` 参数控制，取值 `adaptive`（自适应，默认）或 `disabled`（关闭）。
- Prompt 级控制：开启 `enable_thinking: true` 后，消息中 `/no_think` 跳过当次推理、`/think` 恢复，多条指令以最后一条为准；支持开源 Qwen3 混合模型与 `qwen-plus-2025-04-28`。
- 计费：思考内容按输出 Token 计费；模型输出了思考过程时所有输出 Token（含思考与回复）按思考模式输出价格计费，未输出思考过程则按非思考模式价格计费。
- 部分模型（如 qwen3-235b-a22b、qwen3-32b 等开源版）仅支持流式；非流式调用会报 `parameter.enable_thinking only support stream call`。Qwen3 开源模型必须使用流式输出。推理模式下不支持语音输出（Qwen3-Omni）。
- 思考模式下 `tool_choice` 仅支持 `"auto"` 或 `"none"`；JSON 模式与思考模式冲突时返回错误（`Json mode response is not supported when enable_thinking is true`）。

---

### 7. 工具调用字段

**Chat Completions**：

| 字段 | 结构 | 说明 |
| --- | --- | --- |
| `tools[]` | `{"type":"function","function":{"name","description","parameters"}}` | `type` 固定 `function`；`name` 与 `description` 必填（名称 ≤64 Token，只能含字母、数字、下划线和连字符）；`parameters` 为合法 JSON Schema，默认 `{}`，为空表示无入参 |
| `tool_choice` | `"auto"`（默认）/ `"none"` / `"required"` / `{"type":"function","function":{"name":"..."}}` | 默认由模型决定；`required` 强制至少调用一个工具；Qwen 系列暂不支持 `required`（非思考模式无法保证调用，思考模式当前不支持）；思考模式下仅支持 `auto` 与 `none` |
| `parallel_tool_calls` | boolean，默认 `false` | 默认每次响应只返回一个工具调用；设为 `true` 后 `tool_calls` 数组可含多个条目（各带 `index`、`id`） |
| 响应侧 | `choices[].message.tool_calls[]` | `id`、`type`（`function`）、`function.name`、`function.arguments`（JSON 字符串；模型输出有不确定性，调用前请先验证）、`index` |
| 回传 | tool 消息 `{"role":"tool","content":"...","tool_call_id":"..."}` | `content` 必须是字符串，结构化数据请序列化；`tool_call_id` 取自 `completion.choices[0].message.tool_calls[$index].id` |
| 流式 | `delta.tool_calls[]` | 含 `index`、`id`、`function.name`（仅第一个数据块）、`function.arguments`（增量片段）；按索引累积所有片段，流结束后再 `JSON.parse()` |
| `tool_stream` | boolean，默认 `false` | 仅在 `stream=true` 时生效；影响复杂工具参数（含 array/object 类型）的流式输出 |

工具 schema 参考（原文表格）：`type` 固定 `"function"`；`function.name` 必须与应用程序中实际执行的函数名一致；`function.description` 决定模型是否调用；`function.parameters` 为 JSON Schema，可省略；`function.parameters.properties` 每个键为参数名；`function.parameters.required` 为必填参数名数组。

其他要点：工具描述会计入输入 Token，作为 prompt 的一部分计费；工具信息也可改为嵌入 system message（`<tools>` / `<tool_call>` XML 标签方案），但推荐使用 `tools` 参数；建议候选工具不超过 20 个。Qwen-Omni 在工具信息获取阶段**必须流式**（`stream=True`）并建议 `modalities=["text"]`。开启思考后进行 function calling，响应中每次工具调用前都包含 `reasoning_content`，多轮回传时须带上该字段。

**Responses API**：`tools[]` 扁平结构，`type` ∈ `web_search` / `code_interpreter` / `web_extractor` / `web_search_image` / `image_search` / `file_search` / `mcp` / `function`（`qwen3.8-omni-flash` 内置工具仅支持 `web_search`，同时支持自定义 `function`）。各类型附加字段：`file_search` → `vector_store_ids`（必填，**当前仅支持传入一个知识库 ID**）；`mcp` → `server_protocol`（仅支持 `"sse"`，必填）、`server_label`（必填）、`server_url`（必填）、`server_description`（可选）、`headers`（可选，`additionalProperties = string`）；`function` → `name`（必填，≤64 Token）、`description`（必填）、`parameters`（可选 JSON Schema）。`tool_choice` 见第 3 节。工具调用以 `function_call` 输出项返回（`id`、`call_id`、`name`、`arguments`），执行后以 `function_call_output`（`call_id`、`output`）回传。

**Anthropic 兼容**：`tools[]` 为 `name`（必填）、`description`、`input_schema`（必填，JSON Schema）；`tool_choice` 为对象 `{"type":"auto"|"any"|"none"|"tool","name":...}`；响应 `content[]` 内含 `tool_use` 块（`id`、`name`、`input`），回传用 `tool_result` 块（`tool_use_id`）。

**DashScope 原生**：`parameters.tools[]` 同 Chat 结构（`name` ≤64 字符）；使用工具时**必须**把 `result_format` 设为 `message`；**不支持 qwen-vl 系列模型**；`parameters.tool_choice` 默认 `"auto"`，思考模式模型不支持强制指定工具；`parameters.parallel_tool_calls` 默认 `false`，思考模式模型在强制指定工具时不支持。响应侧 `output.choices[].message.tool_calls[]`；回传消息 `tool_call_id` 取自 `response.output.choices[0].message.tool_calls[$index].id`。

---

### 8. 多模态与文件

**输入侧（本次抓到的全部内容）**

Chat Completions 的 user 消息内容块（见第 2 节）：`text`、`image_url`（`url`）、`input_audio`（`data`、`format`）、`video`（图片 URL 列表）、`video_url`（`url`），以及 `fps`（`[0.1, 10]`，默认 `2.0`）、`min_pixels`、`max_pixels`、`total_pixels`、`cache_control`。

Responses API 的 user 消息 `content` 数组支持图片、文件与显式缓存；`qwen3.8-omni-flash` 还支持 `input_audio` 与 `input_video`；内容类型 `input_file` 支持 PDF（最大 100 MB）与图片（最大 20 MB），**目前仅 `qwen3.5-ocr` 支持**。

Anthropic 兼容的内容块 `image` / `video`，`source.type` 为 `url`（`url` 必填）或 `base64`（`media_type`、`data` 必填）。

DashScope 多模态端点的 user `content` 部分数组：`text`、`image`（公开 URL、base64 `data:image/<format>;base64,<data>` 或本地文件路径）、`video`（文件 URL 字符串或图像 URL 列表）、`fps`（`[0.1, 10]`，默认 `2.0`）、`max_frames`、`min_pixels`、`max_pixels`、`total_pixels`、`file`（文档公开 URL，PDF、DOCX 等，适用 Qwen-VL）、`cache_control`。

**图像限制**（视觉理解页）：最小尺寸宽高均须大于 10 像素；长边与短边之比不得超过 `200:1`；建议分辨率控制在 8K（7680x4320）以内。支持的格式：低于 4K（3840x2160）时 BMP、JPEG（.jpe/.jpeg/.jpg）、PNG、TIFF、WEBP、HEIC；分辨率在 4K~8K 之间**仅支持** JPEG、JPG、PNG。大小：公网 URL 传入时 Qwen3.8 / 3.7 / 3.6 / 3.5 / Qwen3-VL 系列单图不超过 20 MB，其他模型不超过 10 MB；本地路径单图不超过 10 MB；Base64 传入时原始文件不超过 20 MB（其他模型 10 MB），且编码后的 Data URI 字符串在 OpenAI 兼容与 DashScope 不超过 20 MB、在 **Anthropic 兼容不超过 32 MB**；所有接口请求体整体不超过 64 MB（多图共享）。数量：公网 URL 或本地路径传入时 Qwen3.8-Max / Qwen3.8-Flash / Qwen3.7-Plus 最多 2048 张，Qwen3.7-Flash / 3.6-Plus / 3.6-Flash / 3.5-Plus / 3.5-Flash / Qwen3-VL / Qwen-VL / QVQ 系列最多 256 张；Base64 传入最多 250 张。

**视频限制**：以图像列表传入时 qwen3.5 系列 4~8000 张，qwen3-vl-plus / qwen3-vl-flash / qwen3-vl-235b-a22b-thinking / -instruct 4~2000 张，其他 Qwen3-VL 开源、Qwen2.5-VL、QVQ 系列 4~512 张，其他模型 4~80 张。以视频文件传入时：公开 URL 大小上限 qwen3.5 系列 / Qwen3-VL 系列 / qwen-vl-max 系列 2 GB，qwen-vl-plus 系列与其他 1 GB，其他模型 150 MB；Base64 编码字符串须小于 10 MB；本地文件路径不超过 100 MB。时长：qwen3.5 系列 2 秒~2 小时；qwen3-vl-plus / flash 系列 2 秒~1 小时；其他 Qwen3-VL 开源与 qwen-vl-max 系列 2 秒~20 分钟；qwen-vl-plus 系列等 2 秒~10 分钟；其他模型 2 秒~40 秒。格式 MP4、AVI、MKV、MOV、FLV、WMV 等；分辨率无特定限制；每次请求最多 64 个视频；**模型不支持理解视频文件中的音频内容**。

**文件传入方式**：公共 URL（请求头**必须**包含 `Content-Length` 与 `Content-Type`，缺失或不正确会下载失败；不可访问 OSS 内网地址 `-internal`）；Base64 编码字符串；本地文件路径（**仅 DashScope SDK**）。选择建议（原文表格）：DashScope SDK 的图/视频优先传本地路径，公网 URL 用于视频 >100 MB；OpenAI 兼容 / DashScope HTTP 下，图片大于 7 MB 且小于 10 MB 仅支持公共 URL，小于 7 MB 可用 Base64；Base64 编码会增加体积，因此原始文件必须小于 7 MB。

**输出侧**：本次未收录（站点有独立的图像/视频/语音生成页）。

---

### 9. 缓存与成本字段

三种上下文缓存模式（上下文缓存页）：

| 模式 | 启用方式 | 缓存创建价格 | 缓存命中价格 | 最小 Token 数 |
| --- | --- | --- | --- | --- |
| 隐式 | 自动生效，无需配置 | 标准输入价格 | 标准输入价格的 20% | 256（智谱部署 GLM、稀宇科技部署 MiniMax 为 512） |
| 显式 | 添加 `cache_control: {"type":"ephemeral"}` 标记 | 标准输入价格的 125% | 标准输入价格的 10% | 1024 |
| 会话 | Responses API + 请求头 `x-dashscope-session-cache: enable` | 标准输入价格的 125% | 标准输入价格的 10% | 1024 |

**显式缓存**：在 `messages` 数组中加 `"cache_control": {"type":"ephemeral"}`；系统从每个标记位置**向前检索最多 20 个 content 块**做前缀匹配；单次请求**最多 4 个**缓存标记（超过则仅最后四个生效）。未命中时用「messages 开头到标记」的内容创建缓存块，**有效期 5 分钟**（缓存创建发生在模型响应之后，建议等创建请求完成后再发下一次）；缓存块**至少 1024 Token**。命中时选最长匹配前缀并**重置 5 分钟有效期**。可加标记的消息类型：System、User、Assistant、Tool 消息；若请求含 `tools`，加标记会同时缓存工具描述。计费：创建按标准输入价 125%（若新缓存含已有缓存作前缀，仅对增量部分收创建费），命中按 10%，未命中且未用于创建的 Token 按标准价；**例外**：qwen3.8-max、qwen3.8-flash、qwen3.8-2.4t-a95b 的显式缓存命中价不是标准单价的 10%（见模型市场）。支持模型包括千问 Max / Plus / Flash / Coder / VL / Character / Doc、对话分析、deepseek-v3.2、kimi-k2.7-code / k2.6 / k2.5、glm-5.3 / glm-5.1 等（完整清单见缓存页表格）。

**隐式缓存**：对所有支持的模型自动开启且**无法关闭**；按前缀匹配查找公共前缀，命中直接用缓存结果推理，未命中则正常处理并把前缀存入缓存；千问AI平台部署的模型要求相邻请求存在不少于 1024 Token 的相同前缀（智谱部署 GLM、稀宇部署 MiniMax 为 512）；**无额外费用**。提高命中率：静态内容放开头、可变内容放末尾；同一图片/视频多问时把媒体放文本之前，不同图片问同一问题时把文本放媒体之前。支持模型含全模态 `qwen3.8-omni-flash`、大量文本模型、视觉模型 `qwen3-vl-plus` / `qwen3-vl-flash` / `qwen-vl-max` / `qwen-vl-plus`、文档理解 `qwen-doc-turbo`、对话分析 `tongyi-xiaomi-analysis-pro` / `-flash`。

**会话缓存**：仅适用于 Responses API，需配合 `previous_response_id` 多轮对话；最小 1024 Token，有效期 5 分钟（命中后重置）；计费随实际使用的缓存类型走；命中数通过 `usage.input_tokens_details.cached_tokens` 查看。

**成本相关字段与折扣**：批量调用 5 折（见第 10 节）；批量折扣**不与上下文缓存或其他折扣叠加**。缓存数据按**账号**与**模型**隔离，不跨账号、不跨模型共享；显式缓存命中会重置有效期；有效期 5 分钟（隐式缓存由系统自动管理、无固定有效期）。`input_tokens` 不等于 `cache_creation_input_tokens + cached_tokens`，因为后端会在用户 prompt 之后附加少量 Token（通常不超过 10 个），位于 `cache_control` 之后，不计入缓存创建/命中但计入总 `input_tokens`。查看一段时间缓存命中量：工作台用量分析页 `cache_tokens` 指标。

---

### 10. 特殊模式

**结构化输出（JSON Object / JSON Schema）**

| 特性 | JSON Object 模式 | JSON Schema 模式 |
| --- | --- | --- |
| 输出合法 JSON | 是 | 是 |
| 严格遵循 Schema | 否 | 是 |
| 支持模型 | 千问大部分模型、Kimi、GLM、DeepSeek、Stepfun | 仅支持部分模型 |
| `response_format` 设置 | `{"type":"json_object"}` | `{"type":"json_schema","json_schema":{...,"strict":true}}` |
| 提示词要求 | 必须包含 "JSON" | 建议明确说明 |
| 适用场景 | 灵活的 JSON 输出 | 精确的结构验证 |

- JSON Object 模式：System 或 User Message 中必须包含 "JSON" 关键词（不区分大小写），否则报错 `'messages' must contain the word 'json' in some form, to use 'response_format' of type 'json_object'.`。
- JSON Schema 模式：提示词无需包含 "JSON" 关键词；官方支持模型为 Qwen3.7-Plus 系列、Qwen3.7-Flash 系列、Qwen3.7-Max 系列、Qwen3.8-Max 系列、Qwen3.8-Flash 系列；Anthropic 兼容侧通过 `output_config.format`（`type` 固定 `json_schema`，须含 `additionalProperties: false`）提供，并区分「严格结构化输出」（qwen3.8 系列、qwen3.7 系列、deepseek 系列、glm 系列）与「常规结构化输出」（其余模型，API 自动降级为普通 JSON 模式）。
- 标注为「非思考模式」的模型在思考模式下设置 `{"type":"json_object"}` 不会报错，但结构化输出可能失效；官方给的兜底方案是两步法（先由思考模型产出，再把不合格 JSON 交给支持 JSON Object 的模型修复，如非思考模式 `qwen-flash`）。
- 思考模式与 JSON 模式同时使用时返回错误：`Json mode response is not supported when enable_thinking is true`。
- 流式输出支持 JSON Mode：同时设置 `stream=true` 与 `response_format={"type":"json_object"}`，拼接后为合法 JSON。

**前缀续写（Partial Mode）**

- 用法：`messages` 数组最后一条消息 `role` 设为 `assistant`，在 `content` 中填前缀，并在该消息上设置 `"partial": true`；模型从指定前缀继续生成。
- 支持模型：Qwen-Max 系列；Qwen-Plus 系列（非思考模式）；Qwen-Flash 系列（非思考模式）；Qwen-Coder 系列；Qwen-VL 系列（qwen-vl-max 与 qwen-vl-plus 支持思考模式，qwen3-vl-plus 与 qwen3-vl-flash 仅支持非思考模式）；Qwen-Turbo 系列（非思考模式）；Qwen 开源系列（Qwen3.5 MoE/dense 支持思考模式，Qwen3.5-35B-A3B、Qwen3 与 Qwen3-VL 开源仅支持非思考模式）。
- 限制：**思考模式不支持前缀续写**；不支持 DashScope Java SDK。
- 计费：按输入 token 与输出 token 计费，前缀内容计入输入 token。
- 相关用法：非流式调用超时时，若响应头包含 `x-dashscope-partialresponse:true`，可把已生成内容加入 `messages` 续写。

**logprobs**

- Chat / DashScope：`logprobs`（默认 `false`）、`top_logprobs`（默认 `0`，有效值 `0`–`5`），思考阶段生成的 `reasoning_content` 不包含对数概率；支持 Qwen-plus 系列快照版（不含稳定版）、Qwen-turbo 系列快照版（不含稳定版）、qwen3-vl-plus（含稳定版）、qwen3-vl-flash（含稳定版）、Qwen3 开源模型等。
- 响应结构：`logprobs.content[]` 含 `token`、`bytes`（原始 UTF-8 字节列表）、`logprob`（可为 `null`，表示概率极低）、`top_logprobs[]`（同样含 `token` / `bytes` / `logprob`）。
- Responses API 的 `part.logprobs` 目前为 `null`。

**Batch（文件输入）**

- 入口：仅支持 OpenAI 兼容格式，`base_url = https://maas.qianwenaiapi.com/compatible-mode/v1`；DashScope Python SDK 不提供批量推理接口。
- 流程：`client.files.create(file=..., purpose="batch")` 上传 JSONL（返回 `file-batch-xxx`，可复用，`client.files.list(purpose="batch")` 可查）→ `client.batches.create(input_file_id=..., endpoint="/v1/chat/completions", completion_window="24h", metadata={"ds_name":...,"ds_description":...})` → 查询状态 → 下载结果。`completion_window` 取值 24h~336h（14 天）；`endpoint` 必须与输入文件中的 url 一致。
- 计费：输入与输出 Token 按实时价格 **50%** 计费，仅对成功请求收费；批量折扣不与上下文缓存或其他折扣叠加。
- 限制：Batch 场景下单次请求上下文最大 256K（对 qwen3.8-max、qwen3.8-flash、qwen3.7-max、qwen3.7-plus、qwen3.7-flash、qwen3.6-plus、qwen3.6-flash、qwen3.5-plus、qwen3.5-flash、qwen3.5-omni-flash、qwen3.5-omni-plus）；结果文件**不包含** `reasoning_content`；每账号最多 10,000 个文件 / 100 GB；创建 1,000 次/分钟（最多 1,000 并发）、查询 1,000 次/分钟、列表 100 次/分钟、取消 1,000 次/分钟；仅最近 30 天任务可通过列表接口查询。JSONL 中 `enable_thinking` 是 `body` 的顶层参数（与 `model` 同级，不能放 `extra_body`）。
- API 参考页：创建批量任务、查询批量任务、列出批量任务、取消批量任务（`/api-reference/platform-api/batch/*`，本次未收录该批页面正文）。

**Batch Chat（同步批量对话）**

- 入口：`base_url = https://batch.dashscope.aliyuncs.com/compatible-mode/v1`，请求端点 `POST https://batch.dashscope.aliyuncs.com/compatible-mode/v1/chat/completions`；只改端点即可把实时请求切到批量。
- 工作原理：请求排队、客户端保持连接，处理完成后服务端一次性返回完整结果；超过最长等待时间连接断开并返回超时错误。
- 限制：服务端最长保持连接 3,600 秒（自定义超时取值 60–3,600 秒）；单账号每模型最多 10,000 个等待中请求；提交频率上限 1,000 QPS（10,000 次 / 10 秒）。
- 计费：按成功请求的输入与输出 Token 计费，官网限时为实时价的 **50%**；失败请求（含系统错误、超时）不计费；批量推理为独立计费项，不支持免费额度与上下文缓存。
- 支持模型（文本生成）：qwen3.8-max、qwen3.8-flash、qwen3.7-max、qwen3.7-plus、qwen3.7-flash、qwen3.6-plus、qwen3.6-flash、qwen3.5-plus、qwen3.5-flash、qwen3-max、qwen-plus、qwen-flash、deepseek-v3.2；图像与视频理解另有一组（qwen3.8-max / flash、qwen3.7-plus / flash、qwen3.6-plus / flash、qwen3.5-plus / flash、qwen3.5-omni-plus / -flash、qwen3-vl-plus / -flash）。

**Prime 模式**

- 定位：为对输出速度敏感的场景提供更高 TPS（AI 编程助手、Agent 多步推理、实时对话等）。
- 用法：把 `model` 指定为支持模型的 model ID 即可，无需额外参数；TPS 提升至标准 API 的 1.5~2 倍；按输入与输出 token 计费，逻辑与标准 API 一致；限流为「软限流」（达到限流值时若平台仍有剩余资源则不触发，实际可用 TPS 不低于限流值）。
- 支持模型：文本 `glm-5.3-prime`、`glm-5.2-fast-preview`（GLM 5.2 的 Prime 模式模型名仍为 `glm-5.2-fast-preview`），视频 `wan3.0-video-prime`；模型能力与使用限制与原版模型相同。

**异步任务（Task API）**

- 查询结果：`GET https://maas.qianwenaiapi.com/api/v1/tasks/{task_id}`，`Authorization: Bearer sk-ws-xxx`；接口 QPS 限制 20 次/账号，任务完成后结果保留 **24 小时**。
- 批量查询状态与取消：`POST /api/v1/tasks/{task_id}/cancel`（仅 `PENDING` 状态任务可取消，已开始执行无法取消；同一主账号下任意 API Key 提交的任务均可取消），QPS 限制 20 次/账号。
- 返回结构示例：`request_id` + `output`（`task_id`、`task_status`、`submit_time`、`scheduled_time`、`end_time`、`results[]`、`task_metrics`）+ `usage`。

**其他专用模型与模式页**（本批收录的 `text-generation/*` 与 `tool-calling/*` 页）

- 深入研究（Qwen-Deep-Research）：模型 `qwen-deep-research`、`qwen-deep-research-2025-12-15`；上下文 1,000,000 / 最大输入 997,952 / 最大输出 32,768；仅支持流式输出；快照版额外支持 MCP 工具调用（`research_tools`），两者独立计费。
- 长上下文（Qwen-Long）：模型 `qwen-long`、`qwen-long-latest`、`qwen-long-2025-01-25`，上下文 **10M**；通过文档上传（文件 ID）或纯文本传入；支持结构化输出。
- 机器翻译（Qwen-MT）：`qwen-mt-plus` / `qwen-mt-turbo` / `qwen-mt-flash` / `qwen-mt-lite`，上下文 16k（页内另有 92 种语言等说明）；支持术语干预、翻译记忆、领域提示、自定义提示词；`qwen-mt-plus`、`qwen-mt-turbo` 的流式每个 chunk 包含截至目前生成的全部内容。
- 对话分析（Tongyi-Xiaomi-Analysis）：`tongyi-xiaomi-analysis-flash`（低时延、结构化分析）与 `tongyi-xiaomi-analysis-pro`（复杂逻辑推理与深度语义理解）。
- 意图理解（Tongyi-Intent-Detect）：`tongyi-intent-detect-v3`，百毫秒级意图解析与工具选择，支持「意图 + 函数调用」「仅意图识别」「仅函数调用」三种用法。
- 数据挖掘（Qwen-Doc）：`qwen-doc-turbo`，支持通过文件 URL / 文件 ID / 纯文本传入。
- 角色扮演（Qwen-Character）：`qwen-plus-character`、`qwen-plus-character-ja`、`qwen-flash-character`；支持角色设定、开场白、追加对话历史、多样化响应、重新生成、模拟群聊等；长期记忆与会话缓存（`qwen-plus-character` 上下文 32,768）。
- GUI-Plus 界面交互：`gui-plus`（非思考模式）与 `gui-plus-2026-02-26`（思考模式、非思考模式，推荐优先使用）；需配合官方 System Prompt；页面另给出图片格式限制、计费与限流小节。
- 文字提取（OCR）：`qwen3.5-ocr`（支持多轮对话与 PDF 文档解析，PDF 仅支持 Responses API，≤100 MB）、`qwen-vl-ocr` 系列（内置任务：高精度识别、信息提取、表格解析、文档解析、公式识别、通用文字识别、多语言识别）；`qwen-vl-ocr` 的 `max_tokens` 默认 4096。
- 内置工具（`tool-calling/*` 页）：代码解释器（获取执行的代码、使用限制、计费）、联网搜索（搜索量级策略、强制联网搜索、来源与引用标注、垂域搜索、时效性、限定站点、提前返回搜索来源、图文混合输出）、图片搜索（以文搜图 / 以图搜图、响应格式、计费）、网页抓取（响应结构、流式 Web extractor 事件、使用限制）、MCP（`server_protocol` 仅支持 sse 等）、PDF 理解（含 Base64 输入、限制说明）。各页参数细节以对应页面为准，本节只列结构与入口。

---

### 11. 错误码与限流

标准 HTTP 码（400 / 401 / 402 / 403 / 404 / 422 / 429 / 500 / 503）的通用含义见《通用基线》§0.8；下列为本家各协议的错误响应体与错误码族。

**错误响应体形状（四协议不同）**

| 协议 | 形状 |
| --- | --- |
| Chat Completions | `{"error":{"message":"可读错误信息","type":"错误类型","param":"导致错误的参数名或 null","code":"错误码或 null"}}`；规格声明 400「请求参数无效」、401「身份验证失败」、429「超出速率限制」 |
| Responses API | `{"request_id":"用于追踪和调试的唯一请求 ID","code":"错误码","message":"可读的错误信息"}`；规格声明 400、401、404（GET / DELETE 时）；429 响应体按完整 response 对象定义 |
| Anthropic 兼容 | `{"type":"error","error":{"type":"invalid_request_error / authentication_error / rate_limit_error 等","message":"错误详情"}}`；规格声明 400、401、429 |
| DashScope 原生 | `{"status_code":HTTP 状态码,"request_id":...,"code":"机器可读的错误码","message":"可读的错误信息"}`；成功时 `code` 与 `message` 为空字符串 |

Request ID 格式为 UUID（例如 `649b2bbc-c541-9e16-9845-db7fe4fe5b2d`），包含在响应 Header 或 Body 中，建议调用失败时记录。

**错误信息页（`/api-reference/preparation/error-messages`）收录的状态码与错误码族**

| HTTP | 错误码族（页面小节） | 代表错误文案/说明 |
| --- | --- | --- |
| 400 | `InvalidParameter`（含 `InvalidParameter.NotSupportEnableThinking`、`InvalidParameter.DataInspection`）、`invalid_request_error` / `invalid_request_error-invalid_value` / `invalid_value`、`Arrearage`、`Contain.Forbidden.Label.Error`、`DataInspectionFailed` / `data_inspection_failed`、`APIConnectionError`、`InvalidFile.*`（DownloadFailed / AudioLengthError / NoHuman / BodyProportion / FacePose / Resolution / FPS / Value / FrontBody / FullFace / FaceNotMatch / Content / FullBody / BodyPose / Size / Duration / ImageSize / AspectRatio / Openerror / Template.Content / Format / MultiHuman）、`InvalidPerson`、`FlowNotPublished`、`InvalidImage.*`、`InvalidImageResolution` / `InvalidImageFormat`、`InvalidURL`（含 ConnectionRefused / Timeout）、`BadRequestException`、`BadRequest.EmptyInput` / `EmptyParameters` / `EmptyModel` / `IllegalInput` / `InputDownloadFailed` / `UnsupportedFileFormat` / `TooLarge` / `ResourceNotExist` / `VoiceNotFound`、`Throttling.AllocationQuota`、`InvalidGarment`、`InvalidSchema` / `InvalidSchemaFormat`、`Audio.*`（AudioShortError / AudioSilentError / PreprocessError / DecoderError / AudioRateError / DurationLimitError）、`InvalidInputLength`、`FaqRuleBlocked`、`ClientDisconnect`、`ServiceUnavailableError`、`IPInfringementSuspect`、`UnsupportedOperation`、`CustomRoleBlocked` | 例：`parameter.enable_thinking must be set to false for non-streaming calls` / `parameter.enable_thinking only support stream call`；`The thinking_budget parameter must be a positive integer and not greater than xxx`；`This model only support stream mode, please enable the stream parameter to access the model.`；`The incremental_output parameter must be "true" when enable_thinking is true`；`Range of max_tokens should be [1, xxx]`；`Temperature should be in [0.0, 2.0)`；`Range of top_p should be (0.0, 1.0]`；`Parameter top_k be greater than or equal to 0`；`Presence_penalty should be in [-2.0, 2.0]`；`Range of n should be [1, 4]`；`Range of seed should be [0, 9223372036854775807]`；`messages with role "tool" must be a response to a preceding message with "tool_calls"`；`An assistant message with "tool_calls" must be followed by tool messages responding to each "tool_call_id"`；`'messages' must contain the word 'json' in some form, to use 'response_format' of type 'json_object'.`；`Json mode response is not supported when enable_thinking is true`；`tool_choice is one of the strings that should be ["none", "auto"]`；`'audio' output only support with stream=true`；`The result_format parameter must be "message" when enable_thinking is true`；`The value of the enable_thinking parameter is restricted to True.`；`Total message token length exceed model limit (10000000 tokens).`；`Input or output data may contain inappropriate content.`；`Access denied, please make sure your account is in good standing.` |
| 401 | `InvalidApiKey` / `invalid_api_key`、`NOT AUTHORIZED`（工作空间不存在或未授权）、`invalid access token or token expired` | `Invalid API-key provided.` / `Incorrect API key provided.` |
| 403 | `AccessDenied` / `access_denied`（含 `Unpurchased`）、`Model.AccessDenied`、`App.AccessDenied`、`Workspace.AccessDenied`、`Endpoint.AccessDenied`、`AllocationQuota.FreeTierOnly` | `Access to model denied. Please make sure you are eligible for using the model.`；`The free tier of the model has been exhausted...` |
| 404 | `ModelNotFound` / `model_not_found`、`model_not_supported`、`WorkSpaceNotFound`、`NotFound` | `The provided model xxx is not supported by the Batch API.`；`Unsupported model xxx for OpenAI compatibility mode.`；`Request path not found.` |
| 409 | `Conflict` | `Model instance xxx already exists, please specify a suffix.` |
| 429 | `Throttling`、`Throttling.RateQuota` / `LimitRequests` / `limit_requests` / `ResourceExhausted` / `Too many requests`、`Throttling.BurstRate` / `limit_burst_rate`、`Throttling.AllocationQuota` / `insufficient_quota`、`Throttling.Concurrency`、`Throttling.ServiceOverloaded`、`Throttling.ResourceExhausted`、`CommodityNotPurchased`、`PrepaidBillOverdue`、`PostpaidBillOverdue` | `Requests throttling triggered.`；`You have exceeded your request limit.`；`Request rate increased too quickly...`；`Allocated quota exceeded, please increase your quota limit.`；`Too many concurrent requests.`；`InternalError.Algo: An error occurred in model serving, error message is: [Too many requests.]`；`Too many requests. Batch requests are being throttled due to system capacity limits.` |
| 430 | `Audio.DecoderError`、`Audio.FileSizeExceed`、`Audio.AudioRateError`、`Audio.AudioSilentError` | 语音相关 |
| 500 | `InternalError` / `internal_error`（含 `.FileUpload` / `.Upload` / `.Algo` / `.Timeout` / `.Configuration` / `.DataInspection` / `.TranslationFailed`）、`SystemError`、`ModelServiceFailed`、`RequestTimeOut`、`ResponseTimeout`、`InvokePluginFailed`、`AppProcessFailed`、`RewriteFailed`、`RetrivalFailed`、`500/503-ServiceUnavailable` | `http://maas.qianwenaiapi.com/compatible-mode/v1/...` 相关超时文案见该小节；`Response stream timeout` |
| 503 | `ModelUnavailable` | `Model is unavailable, please try again later.` |
| 504 | `GatewayTimeout.InputDownload` | `Input file download timed out.` |
| 200 | `BailianGateway.Workspace.NotAuthorised` | 网关侧工作空间未授权 |
| 其他 | SDK 报错（`error.AuthenticationError` / `openai.OpenAIError` / `Bad Request for url` 等）、`NetworkError`（`NoApiKeyException` / `ConnectException` / `InputRequiredException` 等）、`mismatched_model`、`duplicate_custom_id`、WebSocket 报错（`Invalid payload data`、`unsupported audio format`、`NO_INPUT_AUDIO_ERROR` 等） | Batch 专用：`Each batch must contain requests for a single model.`；`The custom_id parameter must be unique for each request in a batch.`；`Upload file capacity exceed limit.` / `Upload file number exceed limit.` |

**限流（`/developer-guides/administration/rate-limits`）**

- 维度：**RPM**（每分钟请求数）与 **TPM**（每分钟 Token 数）；速率限制在**账户级别**生效，同一账户下所有业务空间与 API Key **共享配额**；同时按秒生效（RPS = RPM / 60，TPS = TPM / 60），单秒突发也可能触发限流。
- 查看入口：默认业务空间用账户级限制（模型市场 → 模型详情页「速率限制与上下文」）；子业务空间在「设置 > 业务空间 > 编辑 > 编辑模型」查看与设置（调用/分钟、Token/分钟），但不能超过该模型的账户级限制，默认业务空间不可修改。
- 临时提额：工作台「限流提额」页申请，输入目标 Token 频率上限（Token / 60 秒）；长期未使用的配额可能被缩减至默认限制。页内列出的支持提额模型：qwen3.6-plus、qwen3.6-flash、qwen3.5-flash、qwen3.5-plus、qwen3-vl-flash、qwen-plus、qwen-plus-latest、qwen3-max、text-embedding-v4、qwen3-vl-plus、qwen-flash。
- 速率限制错误（HTTP 429）：`Requests rate limit exceeded` 或 `You exceeded your current requests list` → 达到 RPM；`Allocated quota exceeded` 或 `You exceeded your current quota` → 达到 TPM；`Request rate increased too quickly` → 请求量突增触发稳定性保护（即使 RPM/TPM 未超限）。**限制在一分钟内重置。**
- 最佳实践：平滑请求速率（恒定速率调度、指数退避、请求队列）、使用备用模型、拆分大任务、选择高配额模型、使用批量推理。

**动态限流（`/developer-guides/administration/dynamic-rate-limits`）**

部分模型的 TPM 限流值随平台**月消费额度**按月调整：限流值作为吞吐保证基线（软限流，若平台仍有剩余资源则不触发，实际可用 TPM 不低于限流值）；粒度按**账号 + 模型**（同账号下所有业务空间、API Key 合并计入）；业务空间可单独设置；此类模型 RPM 限额较高、正常使用不会触发。

| 支持的模型 | 等级1（当前）≤¥10万 | 等级2 ¥10万~¥100万 | 等级3 >¥100万 |
| --- | --- | --- | --- |
| kimi-k3 | 120万 | 500万 | 1000万 |
| qwen3.8-max | 500万 | 1000万 | 2000万 |
| deepseek-v4-pro-0813 | 120万 | 500万 | 1000万 |
| qwen3.8-27b | 500万 | 500万 | 500万 |
| qwen3.8-flash | 500万 | 1000万 | 2000万 |
| qwen3.8-2.4t-a95b | 500万 | 500万 | 500万 |
| glm-5.3 | 500万 | 500万 | 500万 |
| qwen3.8-max-0902 | 150万 | 150万 | 150万 |
| qwen3.8-max-prime（邀测中） | 200万 | 500万 | 1000万 |
| qwen3.8-omni-flash | 200万 | 500万 | 1000万 |
| deepseek-v4.1-flash | 120万 | 120万 | 120万 |

调整规则：按自然月账单周期测算消费金额分档；每月 10 日发送通知；每月 15 日新档位生效，有效期至次月 15 日；调整方向为低档→高档或持平。

---

### 12. 模型清单与限制

**推荐模型**（`/developer-guides/getting-started/text-generation-models`）

| 模型 | 上下文 | 思考模式 | 函数调用 | 内置工具 | 结构化输出 | 批量 |
| --- | --- | --- | --- | --- | --- | --- |
| `qwen3.8-max` | 1M | ✓ | ✓ | ✓ | ✓ | ✓ |
| `qwen3.8-flash` | 1M | ✓ | ✓ | ✓ | ✓ | ✓ |
| `qwen3.7-plus` | 1M | ✓ | ✓ | ✓ | ✓ | ✓ |
| `qwen3.6-flash` | 1M | ✓ | ✓ | ✓ | ✓ | ✓ |
| `deepseek-v4.1-flash` | 1M | ✓ | ✓ | ✓ | ✓ | — |
| `deepseek-v4-pro-0813` | 1M | ✓ | ✓ | ✓ | ✓ | — |
| `deepseek-v4-flash` | 1M | ✓ | ✓ | — | ✓ | — |
| `deepseek-v4-flash-0731` | 1M | ✓ | ✓ | — | — | — |
| `kimi-k2.7-code` | 256k | ✓ | ✓ | — | — | — |
| `glm-5.2` | 1M | ✓ | ✓ | — | ✓ | — |
| `MiniMax-M3` | 192k | ✓ | ✓ | — | — | — |
| `mimo-v2.5-pro` | 1M | ✓ | ✓ | — | ✓ | — |

**全部模型**（列：模型 / 上下文 / 最大输出 / 思考预算 / 函数调用 / 内置工具 / 结构化输出 / 批量）

Qwen3.8：

| 模型 | 上下文 | 最大输出 | 思考预算 | 函数调用 | 内置工具 | 结构化输出 | 批量 |
| --- | --- | --- | --- | --- | --- | --- | --- |
| `qwen3.8-max` | 1M | 128k | 256k | ✓ | ✓ | ✓ | ✓ |
| `qwen3.8-max-0902` | 1M | 128k | 256k | ✓ | ✓ | ✓ | — |
| `qwen3.8-flash` | 1M | 128k | 256k | ✓ | ✓ | ✓ | ✓ |
| `qwen3.8-2.4t-a95b` | 1M | 128k | 128k | ✓ | ✓ | ✓ | — |

Qwen3.7：

| 模型 | 上下文 | 最大输出 | 思考预算 | 函数调用 | 内置工具 | 结构化输出 | 批量 |
| --- | --- | --- | --- | --- | --- | --- | --- |
| `qwen3.7-max` | 1M | 64k | 256k | ✓ | ✓ | — | ✓ |
| `qwen3.7-max-2026-06-08` | 1M | 64k | 256k | ✓ | ✓ | — | — |
| `qwen3.7-max-2026-05-20` | 1M | 64k | 256k | ✓ | ✓ | — | — |
| `qwen3.7-max-preview` | 1M | 64k | 256k | ✓ | ✓ | — | — |
| `qwen3.7-max-2026-05-17` | 1M | 64k | 256k | ✓ | ✓ | — | — |
| `qwen3.7-plus` | 1M | 64k | 256k | ✓ | ✓ | ✓ | ✓ |
| `qwen3.7-plus-2026-05-26` | 1M | 64k | 256k | ✓ | ✓ | ✓ | — |
| `qwen3.7-flash` | 1M | 64k | 256k | ✓ | ✓ | ✓ | ✓ |
| `qwen3.7-flash-2026-07-15` | 1M | 64k | 256k | ✓ | ✓ | ✓ | — |

Qwen3.6：

| 模型 | 上下文 | 最大输出 | 思考预算 | 函数调用 | 内置工具 | 结构化输出 | 批量 |
| --- | --- | --- | --- | --- | --- | --- | --- |
| `qwen3.6-plus` | 1M | 64k | 80k | ✓ | ✓ | ✓ | ✓ |
| `qwen3.6-plus-2026-04-02` | 1M | 64k | 80k | ✓ | ✓ | ✓ | — |
| `qwen3.6-flash` | 1M | 64k | 128k | ✓ | ✓ | ✓ | ✓ |
| `qwen3.6-flash-2026-04-16` | 1M | 64k | 128k | ✓ | ✓ | ✓ | — |
| `qwen3.6-35b-a3b` | 256k | 64k | 128k | ✓ | ✓ | ✓ | — |
| `qwen3.6-27b` | 256k | 64k | 128k | ✓ | ✓ | ✓ | — |

Qwen3.5：

| 模型 | 上下文 | 最大输出 | 思考预算 | 函数调用 | 内置工具 | 结构化输出 | 批量 |
| --- | --- | --- | --- | --- | --- | --- | --- |
| `qwen3.5-plus` | 1M | 64k | 80k | ✓ | ✓ | ✓ | ✓ |
| `qwen3.5-plus-2026-02-15` | 1M | 64k | 80k | ✓ | ✓ | ✓ | — |
| `qwen3.5-plus-2026-04-20` | 1M | 64k | 80k | ✓ | ✓ | ✓ | — |
| `qwen3.5-flash` | 1M | 64k | 80k | ✓ | ✓ | ✓ | ✓ |
| `qwen3.5-flash-2026-02-23` | 1M | 64k | 80k | ✓ | ✓ | ✓ | — |
| `qwen3.5-397b-a17b` | 256k | 64k | 80k | ✓ | ✓ | ✓ | — |
| `qwen3.5-122b-a10b` | 256k | 64k | 80k | ✓ | ✓ | ✓ | — |
| `qwen3.5-27b` | 256k | 64k | 80k | ✓ | ✓ | ✓ | — |
| `qwen3.5-35b-a3b` | 256k | 64k | 80k | ✓ | ✓ | ✓ | — |

第三方模型（通过同一 API 可用）：

| 模型 | 上下文 | 最大输出 | 思考预算 | 函数调用 | 内置工具 | 结构化输出 | 批量 |
| --- | --- | --- | --- | --- | --- | --- | --- |
| `deepseek-v4.1-flash` | 1M | 384k（注1） | 384k（注1） | ✓ | ✓ | ✓ | — |
| `deepseek-v4-pro-0813` | 1M | 384k（注1） | 384k（注1） | ✓ | ✓ | ✓ | — |
| `deepseek-v4-pro` | 1M | 384k（注1） | 384k（注1） | ✓ | — | ✓ | — |
| `deepseek-v4-flash` | 1M | 384k（注1） | 384k（注1） | ✓ | — | ✓ | — |
| `deepseek-v4-flash-0731` | 1M | 384k（注1） | 384k（注1） | ✓ | — | — | — |
| `glm-5.2` | 1M | 131k | 131k | ✓ | — | ✓ | — |
| `glm-5.1` | 198k | 128k | 128k | ✓ | — | ✓ | — |
| `kimi-k2.7-code` | 256k | 16k | — | ✓ | — | — | — |
| `kimi-k2.6` | 256k | 96k | 80k | ✓ | — | — | — |
| `MiniMax-M3` | 192k | 32k | 32k（注2） | ✓ | — | — | — |
| `MiniMax-M2.5` | 192k | 32k | 32k（注2） | ✓ | — | — | — |
| `mimo-v2.5-pro` | 1M | 128k | 128k | ✓ | — | ✓ | — |

注1：DeepSeek-V4 的 384k（393,216 Token）限制由思考和最终输出共享。注2：MiniMax 的 32k 限制由 CoT 和最终输出共享。

旧版及其他模型（同页 Accordion）：

Qwen3：

| 模型 | 上下文 | 最大输出 | 思考预算 | 函数调用 | 内置工具 | 结构化输出 | 批量 |
| --- | --- | --- | --- | --- | --- | --- | --- |
| `qwen3-max` | 256k | 64k | 80k | ✓ | ✓ | ✓ | ✓ |
| `qwen3-max-2026-01-23` | 256k | 64k | 80k | ✓ | ✓ | ✓ | — |
| `qwen3-max-preview` | 256k | 64k | 80k | ✓ | ✓ | ✓ | — |
| `qwen3-max-2025-09-23` | 256k | 64k | — | ✓ | ✓ | ✓ | — |
| `qwen3-235b-a22b` | 128k | 16k | 38k | ✓ | — | ✓ | — |
| `qwen3-235b-a22b-thinking-2507` | 128k | 32k | 80k | ✓ | — | — | — |
| `qwen3-235b-a22b-instruct-2507` | 128k | 32k | — | ✓ | — | ✓ | — |
| `qwen3-next-80b-a3b-thinking` | 128k | 32k | 80k | ✓ | — | — | — |
| `qwen3-next-80b-a3b-instruct` | 128k | 32k | — | ✓ | — | ✓ | — |
| `qwen3-32b` | 128k | 16k | 38k | ✓ | — | ✓ | — |
| `qwen3-30b-a3b` | 128k | 16k | 38k | ✓ | — | ✓ | — |
| `qwen3-30b-a3b-thinking-2507` | 128k | 32k | 80k | ✓ | — | — | — |
| `qwen3-30b-a3b-instruct-2507` | 128k | 32k | — | ✓ | — | ✓ | — |
| `qwen3-14b` | 128k | 8k | 38k | ✓ | — | ✓ | — |
| `qwen3-8b` | 128k | 8k | 38k | ✓ | — | ✓ | — |
| `qwen3-4b` | 128k | 8k | 38k | ✓ | — | ✓ | — |
| `qwen3-1.7b` | 32k | 8k | 30k | ✓ | — | ✓ | — |
| `qwen3-0.6b` | 32k | 8k | 30k | ✓ | — | ✓ | — |

Qwen3-Coder（列：模型 / 上下文 / 最大输出 / 函数调用 / 内置工具 / 结构化输出 / 批量）：

| 模型 | 上下文 | 最大输出 | 函数调用 | 内置工具 | 结构化输出 | 批量 |
| --- | --- | --- | --- | --- | --- | --- |
| `qwen3-coder-plus` | 1M | 64k | ✓ | — | ✓ | — |
| `qwen3-coder-plus-2025-09-23` | 1M | 64k | ✓ | — | ✓ | — |
| `qwen3-coder-plus-2025-07-22` | 1M | 64k | ✓ | — | ✓ | — |
| `qwen3-coder-flash` | 1M | 64k | ✓ | — | ✓ | — |
| `qwen3-coder-flash-2025-07-28` | 1M | 64k | ✓ | — | ✓ | — |
| `qwen3-coder-next` | 256k | 64k | ✓ | — | ✓ | — |
| `qwen3-coder-480b-a35b-instruct` | 256k | 64k | ✓ | — | ✓ | — |
| `qwen3-coder-30b-a3b-instruct` | 256k | 64k | ✓ | — | ✓ | — |

Qwen2.5（开源）：`qwen2.5-omni-7b` 32k/8k；`qwen2.5-72b-instruct` 32k/8k；`qwen2.5-72b-instruct-1m` 1M/8k；`qwen2.5-32b-instruct` 32k/8k；`qwen2.5-14b-instruct` 32k/8k；`qwen2.5-7b-instruct` 32k/8k；`qwen2.5-3b-instruct` 32k/8k；`qwen2.5-1.5b-instruct` 32k/8k；`qwen2.5-0.5b-instruct` 32k/8k（均支持函数调用与结构化输出，不支持内置工具与批量）。

QwQ / QVQ（开源）：`qwq-32b` 128k/8k（思考预算 32k）；`qwq-32b-preview` 32k/16k；两者不支持函数调用、内置工具、结构化输出与批量。

Qwen-Coder（旧版，qwen2.5 之前）：`qwen-coder-plus`、`qwen-coder-plus-latest`、`qwen-coder-plus-2024-11-06`、`qwen-coder-turbo`、`qwen-coder-turbo-latest`、`qwen-coder-turbo-2024-09-19`：均 128k 上下文 / 8k 最大输出，支持函数调用与结构化输出。

Qwen2.5-Coder（开源）：`qwen2.5-coder-32b-instruct`、`-14b-instruct`、`-7b-instruct` 均 128k/8k；`-3b-instruct`、`-1.5b-instruct`、`-0.5b-instruct` 均 32k/8k。

翻译（列：模型 / 上下文；思考模式、函数调用、内置工具、结构化输出、批量均不支持）：`qwen-mt-plus` 16k、`qwen-mt-turbo` 16k、`qwen-mt-flash` 16k、`qwen-mt-lite` 16k。

千问Long（长上下文）：`qwen-long` 10M、`qwen-long-latest` 10M、`qwen-long-2025-01-25` 10M；支持结构化输出，`qwen-long` 与 `qwen-long-latest` 支持批量；不支持思考模式、函数调用、内置工具。

角色扮演：`qwen-plus-character` 32k、`qwen-plus-character-ja` 32k、`qwen-flash-character` 8k；均不支持思考模式、函数调用、内置工具、结构化输出与批量。

旧版 Qwen（列：模型 / 上下文 / 最大输出 / 思考预算 / 函数调用 / 内置工具 / 结构化输出 / 批量）：

| 模型 | 上下文 | 最大输出 | 思考预算 | 函数调用 | 内置工具 | 结构化输出 | 批量 |
| --- | --- | --- | --- | --- | --- | --- | --- |
| `qwen-plus` | 1M | 32k | 80k | ✓ | — | ✓ | ✓ |
| `qwen-plus-latest` | 1M | 32k | 80k | ✓ | — | ✓ | ✓ |
| `qwen-plus-2025-12-01` | 1M | 32k | 80k | ✓ | — | ✓ | — |
| `qwen-plus-2025-09-11` | 1M | 32k | 80k | ✓ | — | ✓ | — |
| `qwen-plus-2025-07-28` | 1M | 32k | 80k | ✓ | — | ✓ | — |
| `qwen-plus-2025-07-14` | 128k | 16k | 80k | ✓ | — | ✓ | — |
| `qwen-plus-2025-04-28` | 128k | 16k | 80k | ✓ | — | ✓ | — |
| `qwen-plus-2025-01-25` | 128k | 8k | — | ✓ | — | ✓ | — |
| `qwen-plus-2025-01-12` | 1M | 32k | 80k | ✓ | — | ✓ | — |
| `qwen-plus-2024-12-20` | 1M | 32k | 80k | ✓ | — | ✓ | — |
| `qwen-max` | 32k | 8k | — | ✓ | — | ✓ | — |
| `qwen-max-latest` | 32k | 8k | — | ✓ | — | ✓ | — |
| `qwen-max-2025-01-25` | 32k | 8k | — | ✓ | — | ✓ | — |
| `qwen-max-2024-09-19` | 32k | 8k | — | ✓ | — | ✓ | — |
| `qwen-max-2024-04-28` | 8k | 2k | — | ✓ | — | ✓ | — |
| `qwen-flash` | 1M | 32k | 80k | ✓ | — | ✓ | ✓ |
| `qwen-flash-2025-07-28` | 1M | 32k | 80k | ✓ | — | ✓ | — |
| `qwen-turbo` | 128k | 16k | 38k | ✓ | — | ✓ | — |
| `qwen-turbo-latest` | 128k | 16k | 38k | ✓ | — | ✓ | — |
| `qwen-turbo-2025-04-28` | 128k | 16k | 38k | ✓ | — | ✓ | — |
| `qwen-turbo-2024-11-01` | 1M | 8k | — | ✓ | — | ✓ | — |
| `qwen-turbo-2025-02-11` | 1M | 8k | — | ✓ | — | ✓ | — |
| `qwen-turbo-2025-07-15` | 128k | 16k | 38k | ✓ | — | ✓ | — |
| `qwq-plus` | 128k | 8k | 32k | — | — | — | — |
| `qwq-plus-latest` | 128k | 8k | 32k | — | — | — | — |
| `qwq-plus-2025-03-05` | 128k | 8k | 32k | — | — | — | — |
| `qvq-max` | 128k | 8k | 80k | — | — | — | — |
| `qvq-max-latest` | 128k | 8k | 80k | — | — | — | — |
| `qvq-max-2025-03-25` | 128k | 8k | 80k | — | — | — | — |
| `qwen-omni-turbo` | 32k | 2k | 80k | — | — | — | — |
| `qwen-omni-turbo-latest` | 32k | 2k | 80k | — | — | — | — |
| `qwen-omni-turbo-2025-03-26` | 32k | 2k | 80k | — | — | — | — |

三方模型（旧版列表，列：模型 / 上下文 / 思考模式 / 函数调用 / 内置工具 / 结构化输出 / 批量）：`glm-5` 198k、`glm-4.7` 198k、`glm-4.6` 198k、`glm-4.5` 198k、`glm-4.5-air` 198k（均 ✓ 思考 / ✓ 函数调用 / — 内置工具 / ✓ 结构化输出 / — 批量）；`MiniMax-M2.7` 192k、`MiniMax-M2.1` 200k、`kimi-k2.5` 256k、`kimi-k2-thinking` 256k（结构化输出 ✓）、`Moonshot-Kimi-K2-Instruct` 256k（无思考模式）；`deepseek-v3.2` 128k（批量 ✓）、`deepseek-v3.2-exp` 128k、`deepseek-v3.1` 128k、`deepseek-v3` 128k（无思考模式，批量 ✓）、`deepseek-r1` 128k（批量 ✓）、`deepseek-r1-0528` 128k、`deepseek-r1-distill-llama-70b` / `-qwen-32b` / `-qwen-14b` / `-qwen-7b` / `-qwen-1.5b` / `-llama-8b` 均 128k（支持思考，不支持函数调用）。

**视觉理解模型（输入侧）**：推荐模型表（列：模型 ID / 上下文 / 单张图片最大像素 / 最长视频时长 / 最大视频大小 / 最多图片数（URL）/ 最多图片数（Base64）/ 最多视频数 / Function calling / 内置工具 / 结构化输出 / 批量）中，`qwen3.8-max`、`qwen3.8-flash`、`qwen3.7-plus` 为 1M / 16M / 2h / 2GB / 2,048 / 250 / 64；`qwen3.7-flash`、`qwen3.6-plus`、`qwen3.6-flash` 为 1M / 16M / 2h / 2GB / 256 / 250 / 64；`qwen3.8-omni-flash` 为 1M / 16M / 2h / 2GB / 2,048 / 250 / 64，内置工具仅 `web_search`、结构化输出为 JSON Object、不支持批量；`qwen3.5-omni-plus` 为 256k / 2h / 2GB / 2,048 / 250 / 512、结构化输出 JSON Object。多数模型支持每张图片最高 1600 万像素，每张图片 Token 数计算公式为 `h × w / (32 × 32) + 2`。内置工具支持列表：`qwen3.8-max`、`qwen3.8-max-0902`、`qwen3.8-flash`、`qwen3.7-max-2026-06-08`、`qwen3.7-plus`、`qwen3.7-flash`、`qwen3.6-plus`、`qwen3.6-flash`、`qwen3.5-plus`、`qwen3.5-flash`（`qwen3.8-omni-flash` 支持 `web_search`）。OCR 系列：`qwen3.5-ocr`、`qwen-vl-ocr`（稳定版）、`qwen-vl-ocr-latest`、`qwen-vl-ocr-2025-11-20`、`qwen-vl-ocr-2025-08-28`、`qwen-vl-ocr-2025-04-13`、`qwen-vl-ocr-2024-10-28`（后两者为早期版本，官方建议迁移到 `qwen3.5-ocr`）。

**参数支持差异（本批抓到的明确结论）**：

- 思考模式：见第 6 节（混合 / 纯推理、默认开关、`thinking_budget` 适用面、`preserve_thinking` 白名单、`clear_thinking` 仅 GLM、MiniMax-M3 用 `thinking`）。
- 结构化输出：JSON Object 与 JSON Schema 的模型范围不同（JSON Schema 官方列举 Qwen3.7-Plus / Flash / Max 与 Qwen3.8-Max / Flash 系列）。
- 批量：表格中以「批量」列标注（如 `qwen3.8-max`、`qwen3.8-flash`、`qwen3.7-plus`、`qwen3.5-flash`、`qwen-long`、`deepseek-v3.2`、`deepseek-r1` 等支持），批量调用仅适用于文本生成模型。
- 联网搜索：`search_strategy` 的 `agent` / `agent_max` 仅对部分模型可用（qwen3.7-max 系列、qwen3.5-plus 系列、qwen3.8-max 等）。
- 部分模型必须流式：QwQ 与 QVQ 仅支持流式；Qwen3 开源模型必须使用流式输出；Qwen-Omni 在函数调用时必须流式。
- 前缀续写、`n`、`logprobs`、`vl_high_resolution_images` 等参数各自有模型白名单，见第 2、6、10 节。

---

### 13. 来源页清单

本家页面清单（含每条 URL 的「这页讲了什么」一句话）统一见《附录：各家页面清单》的本家小节。

---

## 附录：各家页面清单（抓取覆盖核验用）

> 一条规范 URL 一行，第二列是「这页讲了什么」（≤120 字符，优先保留该页出现的端点路径与字段名）。
> 这份清单同时承担三件事：本文的**来源清单**、六家章节 §13 的**页面摘要**、以及覆盖率核验的**比对基准**。
> 所以 **URL 只在本文附录出现一次**，各家章节里原来的「来源」行已删除。
> URL 由 `python3 scripts/audit-provider-api-docs.py --dump` 从各家文档站的 `sitemap.xml` / `llms.txt` 生成。
> 多语言镜像、站点根页、以及按 `is_peripheral()` 判定的周边页（客户端接入 / 计费套餐 / FAQ / 更新日志 / 法务 / 营销，共 221 条）不计入；千问AI平台另只收录协议核心的 73 条，该家其余约 876 条按收录范围不计入。抓取日期 2026-10-03。


### DeepSeek — 全部页面 23 条

| 页面（规范 URL） | 这页讲了什么 |
|---|---|
| https://api-docs.deepseek.com/zh-cn/api/create-chat-completion | Chat Completions 全量请求字段与消息内容块、非流式响应 Schema、流式 chunk Schema 与 SSE 样例、`usage` 明细 |
| https://api-docs.deepseek.com/zh-cn/api/create-completion | FIM 补全（Beta）请求字段（`prompt`/`suffix`/`echo`）与 `text_completion` 响应字段 |
| https://api-docs.deepseek.com/zh-cn/api/create-file | `POST /files` 表单字段（`file`/`purpose`/`expires_after`）与 file object 响应字段 |
| https://api-docs.deepseek.com/zh-cn/api/create-response | Responses API 全量请求字段、响应字段与示例、流式事件示例 |
| https://api-docs.deepseek.com/zh-cn/api/delete-file | `DELETE /files/:file_id` 路径参数与删除响应字段（`id`/`object`/`deleted`） |
| https://api-docs.deepseek.com/zh-cn/api/get-user-balance | `GET /user/balance` 响应字段（`is_available`、`balance_infos[].currency/total_balance/granted_balance/topped_up_balance`）与示例 |
| https://api-docs.deepseek.com/zh-cn/api/list-files | `GET /files` 查询参数（`after`/`limit`/`order`/`purpose`）与分页列表响应字段 |
| https://api-docs.deepseek.com/zh-cn/api/list-models | `GET /models` 响应字段：`context_window`、`max_output_tokens`、`input_modalities`、`effort` 档位、`api_capabilities` |
| https://api-docs.deepseek.com/zh-cn/api/retrieve-file | `GET /files/:file_id` 路径参数与响应字段 |
| https://api-docs.deepseek.com/zh-cn/guides/anthropic_api | 使用 Anthropic API：环境变量、claude 模型名映射、Header/简单字段/Tool/Message 兼容性表 |
| https://api-docs.deepseek.com/zh-cn/guides/chat_prefix_completion | 对话前缀续写（Beta）：`prefix` 要求、`/beta` base_url、样例 |
| https://api-docs.deepseek.com/zh-cn/guides/files_api | Files API：四个端点的表单/查询字段、响应示例、Anthropic 兼容 Files 差异表、限制表 |
| https://api-docs.deepseek.com/zh-cn/guides/fim_completion | FIM 补全（Beta）：最大补全 4K、`/beta` base_url、`prompt`/`suffix` 样例、Continue 插件 |
| https://api-docs.deepseek.com/zh-cn/guides/json_mode | JSON Output：`response_format` 设置、prompt 须含 `json`、可能返回空 content |
| https://api-docs.deepseek.com/zh-cn/guides/kv_cache | 上下文硬盘缓存：缓存前缀单元与三种落盘时机、两个示例、`usage` 缓存字段、64 tokens 存储单元 |
| https://api-docs.deepseek.com/zh-cn/guides/multi_round_chat | 多轮对话：无状态 API 的历史拼接方式与样例 |
| https://api-docs.deepseek.com/zh-cn/guides/responses_api | 使用 Responses API：流式事件全表、`input_image` 字段、顶层参数兼容性表、输入 Items 表、Tools 表、响应字段 |
| https://api-docs.deepseek.com/zh-cn/guides/thinking_mode | 思考模式：三协议的思考开关/强度字段拼写、effort 映射表、采样参数限制、`reasoning_content` 回传硬要求 |
| https://api-docs.deepseek.com/zh-cn/guides/tool_calls | Tool Calls：非思考/思考模式样例、对话中间插入工具调用的支持差异、`strict` 模式与 JSON Schema 类型 |
| https://api-docs.deepseek.com/zh-cn/guides/vision | 图像理解：三种传图方式与字段、`detail` 取值表、图片限制表、图片 token 换算、使用限制 |
| https://api-docs.deepseek.com/zh-cn/quick_start/error_codes | 错误码表：400/401/402/422/429/500/503 及解决方法 |
| https://api-docs.deepseek.com/zh-cn/quick_start/rate_limit | 限速与隔离：并发限制表、`user_id` 隔离与三种设置方式、请求保活机制（空行 / `: keep-alive`、10 分钟） |
| https://api-docs.deepseek.com/zh-cn/quick_start/token_usage | Token 用量计算：token 与字数换算比例、离线 tokenizer 压缩包、图片 Token 计算器 |

未能抓取 / 未纳入：
- 新闻页中引用的 `guides/comparison_testing` 未出现在中文侧边栏，未抓取。


### 智谱 GLM — 全部页面 192 条

| 页面（规范 URL） | 这页讲了什么 |
|---|---|
| https://docs.bigmodel.cn/cn/api-reference/agent-api/对话历史 | `POST /v1/agents/conversation`：导出对话历史（`conversation_id`、`agent_id`、`custom_variables`）；现仅支持 `slides_glm_agent` |
| https://docs.bigmodel.cn/cn/api-reference/agent-api/异步结果 | `POST /v1/agents/async-result`：查询智能体异步任务结果，返回 `async_id`、`status`（success/failed/pending）、`choices[].messages[]` |
| https://docs.bigmodel.cn/cn/api-reference/agent-api/智能体对话 | `POST /v1/agents`：智能体对话（`agent_id`、`messages`、`stream`、`custom_variables`）；支持同步与流式 |
| https://docs.bigmodel.cn/cn/api-reference/agent-api/问答-agent-对话（流式） | `POST /zrag/agent/chat`：问答 Agent，header `X-Session-Id`；SSE type：session_created/reasoning/thought/tool_call/answer/done |
| https://docs.bigmodel.cn/cn/api-reference/managed-agents--agent/列出-agent | `GET /agent/managed/v1/agents`：分页列出可读 Agent；query `limit`・`order`・`page`；limit>100 截断为 100 |
| https://docs.bigmodel.cn/cn/api-reference/managed-agents--agent/列出-agent-版本 | `GET /agent/managed/v1/agents/{agentId}/versions`：列出版本，每条为该版本的完整配置快照 |
| https://docs.bigmodel.cn/cn/api-reference/managed-agents--agent/创建-agent | `POST /agent/managed/v1/agents`：创建 Agent 与首个不可变版本（`name`、`model`、`system`、`tools`、`skills`、`mcp_servers`） |
| https://docs.bigmodel.cn/cn/api-reference/managed-agents--agent/归档-agent | `POST /agent/managed/v1/agents/{agentId}/archive`：归档 Agent（仅所有者、幂等），同时归档其运行中的 Deployment |
| https://docs.bigmodel.cn/cn/api-reference/managed-agents--agent/更新-agent | `POST /agent/managed/v1/agents/{agentId}`：更新并生成新不可变版本；带 `version` 冲突返回 409 |
| https://docs.bigmodel.cn/cn/api-reference/managed-agents--agent/获取-agent | `GET /agent/managed/v1/agents/{agentId}`：返回当前版本的完整配置；无读取权限与不存在均返回 404 |
| https://docs.bigmodel.cn/cn/api-reference/managed-agents--deployment/列出-deployment | `GET /agent/managed/v1/deployments`：列出 Deployment；query `agent_id`・`status`・`include_archived` 等 |
| https://docs.bigmodel.cn/cn/api-reference/managed-agents--deployment/列出-deployment-run | `GET /agent/managed/v1/deployment_runs`：列出 Run；query `deployment_id`・`trigger_type` 等；无 `status`，看 `error` 是否为 null |
| https://docs.bigmodel.cn/cn/api-reference/managed-agents--deployment/创建-deployment | `POST /agent/managed/v1/deployments`：`name`、`agent`、`environment_id`、`initial_events` 必填；`schedule` 省略则仅手动触发 |
| https://docs.bigmodel.cn/cn/api-reference/managed-agents--deployment/归档-deployment | `POST /agent/managed/v1/deployments/{deploymentId}/archive`：归档（终态、幂等）；Deployment 无删除接口 |
| https://docs.bigmodel.cn/cn/api-reference/managed-agents--deployment/恢复-deployment | `POST /agent/managed/v1/deployments/{deploymentId}/unpause`：恢复；cron 从当前时间重算下次执行，不补跑错过的任务 |
| https://docs.bigmodel.cn/cn/api-reference/managed-agents--deployment/手动运行-deployment | `POST /agent/managed/v1/deployments/{deploymentId}/run`：立即手动触发，返回 202；不影响 cron 下次时间，暂停时可执行 |
| https://docs.bigmodel.cn/cn/api-reference/managed-agents--deployment/暂停-deployment | `POST /agent/managed/v1/deployments/{deploymentId}/pause`：暂停定时触发（幂等）；暂停期间仍可手动 run |
| https://docs.bigmodel.cn/cn/api-reference/managed-agents--deployment/更新-deployment | `POST /agent/managed/v1/deployments/{deploymentId}`：更新配置；`agent` 只能重钉同一 Agent，`environment_id: null` 保留原值 |
| https://docs.bigmodel.cn/cn/api-reference/managed-agents--deployment/获取-deployment | `GET /agent/managed/v1/deployments/{deploymentId}`：返回当前配置与运行状态（`status` active/paused、`archived_at`） |
| https://docs.bigmodel.cn/cn/api-reference/managed-agents--deployment/获取-deployment-run | `GET /agent/managed/v1/deployment_runs/{runId}`：获取单条 Run（`trigger_context`、`session_id`、`error`）；不存在或无权均 404 |
| https://docs.bigmodel.cn/cn/api-reference/managed-agents--environment/列出-environment | `GET /agent/managed/v1/environments`：分页列出可访问 Environment；query `limit`・`page`；limit>100 截断为 100 |
| https://docs.bigmodel.cn/cn/api-reference/managed-agents--environment/创建-environment | `POST /agent/managed/v1/environments`：创建（`name` 必填，`scope` 仅 organization，`config.type` 仅 cloud） |
| https://docs.bigmodel.cn/cn/api-reference/managed-agents--environment/删除-environment | `DELETE /agent/managed/v1/environments/{environmentId}`：永久删除；删除后不能用于 Session 或 Deployment |
| https://docs.bigmodel.cn/cn/api-reference/managed-agents--environment/归档-environment | `POST /agent/managed/v1/environments/{environmentId}/archive`：归档（`state: archived`），不能再绑定新 Session/Deployment |
| https://docs.bigmodel.cn/cn/api-reference/managed-agents--environment/更新-environment | `POST /agent/managed/v1/environments/{environmentId}`：更新；`config` 整体替换，`config: null` 恢复默认 cloud |
| https://docs.bigmodel.cn/cn/api-reference/managed-agents--environment/获取-environment | `GET /agent/managed/v1/environments/{environmentId}`：获取单个 Environment；不可见或不存在返回 404 |
| https://docs.bigmodel.cn/cn/api-reference/managed-agents--file/上传-file | `POST /agent/managed/v1/files`：multipart 上传（`file` 必填），返回 `ManagedFile`；上传件 `downloadable` 为 false |
| https://docs.bigmodel.cn/cn/api-reference/managed-agents--file/下载-file-内容 | `GET /agent/managed/v1/files/{fileId}/content`：下载二进制内容（application/octet-stream） |
| https://docs.bigmodel.cn/cn/api-reference/managed-agents--file/列出-file | `GET /agent/managed/v1/files`：分页列出文件；query `limit`・`before_id`・`after_id`・`scope_id`（须 `sess_` 前缀） |
| https://docs.bigmodel.cn/cn/api-reference/managed-agents--file/删除-file | `DELETE /agent/managed/v1/files/{fileId}`：删除文件，返回 `{id, type: "file_deleted"}` |
| https://docs.bigmodel.cn/cn/api-reference/managed-agents--file/获取-file | `GET /agent/managed/v1/files/{fileId}`：只返回文件元数据，不返回二进制；无权限与不存在均 404 |
| https://docs.bigmodel.cn/cn/api-reference/managed-agents--memory/列出-memory | `GET /agent/managed/v1/memory_stores/{storeId}/memories`：列出 Memory；query `path_prefix`・`depth`・`view` |
| https://docs.bigmodel.cn/cn/api-reference/managed-agents--memory/列出-memory-store | `GET /agent/managed/v1/memory_stores`：分页列出 Memory Store；query `beta`・`include_archived`・`created_at[gte]` |
| https://docs.bigmodel.cn/cn/api-reference/managed-agents--memory/列出-memory-version | `GET /agent/managed/v1/memory_stores/{storeId}/memory_versions`：列出记忆版本（created/modified/deleted/脱敏） |
| https://docs.bigmodel.cn/cn/api-reference/managed-agents--memory/创建-memory | `POST /agent/managed/v1/memory_stores/{storeId}/memories`：创建记忆（`path` 必填且以 `/` 开头，`content` 默认上限 102400 字节） |
| https://docs.bigmodel.cn/cn/api-reference/managed-agents--memory/创建-memory-store | `POST /agent/managed/v1/memory_stores`：创建 Memory Store（`name` 必填 1–255，`description` ≤1024），用于跨 Session 长期记忆 |
| https://docs.bigmodel.cn/cn/api-reference/managed-agents--memory/删除-memory | `DELETE /agent/managed/v1/memory_stores/{storeId}/memories/{memoryId}`：删除记忆；可带 `expected_content_sha256` 前置条件 |
| https://docs.bigmodel.cn/cn/api-reference/managed-agents--memory/删除-memory-store | `DELETE /agent/managed/v1/memory_stores/{storeId}`：删除 Store 及其 Memory；Store 正在被操作时不能删除 |
| https://docs.bigmodel.cn/cn/api-reference/managed-agents--memory/归档-memory-store | `POST /agent/managed/v1/memory_stores/{storeId}/archive`：归档 Store，不能再作为活动资源挂载或写入 |
| https://docs.bigmodel.cn/cn/api-reference/managed-agents--memory/更新-memory | `POST /agent/managed/v1/memory_stores/{storeId}/memories/{memoryId}`：更新记忆；`precondition.type=content_sha256` 防并发覆盖 |
| https://docs.bigmodel.cn/cn/api-reference/managed-agents--memory/更新-memory-store | `POST /agent/managed/v1/memory_stores/{storeId}`：更新 Store（`name`/`description`/`metadata` 补丁语义） |
| https://docs.bigmodel.cn/cn/api-reference/managed-agents--memory/脱敏-memory-version | `POST /agent/managed/v1/memory_stores/{storeId}/memory_versions/{versionId}/redact`：脱敏历史版本；不能对 head 版本脱敏（409） |
| https://docs.bigmodel.cn/cn/api-reference/managed-agents--memory/获取-memory | `GET /agent/managed/v1/memory_stores/{storeId}/memories/{memoryId}`：获取记忆（`path`、`content`、版本、时间） |
| https://docs.bigmodel.cn/cn/api-reference/managed-agents--memory/获取-memory-store | `GET /agent/managed/v1/memory_stores/{storeId}`：获取 Store 配置、状态、容量与归档信息 |
| https://docs.bigmodel.cn/cn/api-reference/managed-agents--memory/获取-memory-version | `GET /agent/managed/v1/memory_stores/{storeId}/memory_versions/{versionId}`：获取不可变的历史版本快照 |
| https://docs.bigmodel.cn/cn/api-reference/managed-agents--session/列出-session | `GET /agent/managed/v1/sessions`：列出 Session；query `agent_id`・`statuses[]`・`created_at[gte]`・`include_archived` |
| https://docs.bigmodel.cn/cn/api-reference/managed-agents--session/列出-session-resource | `GET /agent/managed/v1/sessions/{sessionId}/resources`：列出会话挂载的 File 与 Memory Store Resource |
| https://docs.bigmodel.cn/cn/api-reference/managed-agents--session/列出事件 | `GET /agent/managed/v1/sessions/{sessionId}/events`：分页读取已持久化历史事件（query `types`・`created_at[gte]`） |
| https://docs.bigmodel.cn/cn/api-reference/managed-agents--session/创建-session | `POST /agent/managed/v1/sessions`：`agent`、`environment_id` 必填；header `x-checkpoint`、`x-events-encrypted` |
| https://docs.bigmodel.cn/cn/api-reference/managed-agents--session/删除-session | `DELETE /agent/managed/v1/sessions/{sessionId}`：删除会话；运行中的会话不能删除 |
| https://docs.bigmodel.cn/cn/api-reference/managed-agents--session/删除-session-file-resource | `DELETE /agent/managed/v1/sessions/{sessionId}/resources/{resourceId}`：解除会话挂载，不删除源 File |
| https://docs.bigmodel.cn/cn/api-reference/managed-agents--session/发送事件 | `POST /agent/managed/v1/sessions/{sessionId}/events`：发送 1–10 个事件，整批原子校验（任一非法零写入） |
| https://docs.bigmodel.cn/cn/api-reference/managed-agents--session/归档-session | `POST /agent/managed/v1/sessions/{sessionId}/archive`：归档即终止并保留历史；运行中不能归档，重复归档冲突 |
| https://docs.bigmodel.cn/cn/api-reference/managed-agents--session/新增-session-file-resource | `POST /agent/managed/v1/sessions/{sessionId}/resources`：新增 File Resource，默认挂载 `/mnt/session/uploads/{file_id}` |
| https://docs.bigmodel.cn/cn/api-reference/managed-agents--session/更新-session | `POST /agent/managed/v1/sessions/{sessionId}`：更新 `title`/`metadata`，或覆盖 `agent.tools`/`agent.mcp_servers`（仅 idle） |
| https://docs.bigmodel.cn/cn/api-reference/managed-agents--session/获取-session | `GET /agent/managed/v1/sessions/{sessionId}`：关注 `status`・`usage`（input/output/cache_read token）・`resources` |
| https://docs.bigmodel.cn/cn/api-reference/managed-agents--session/获取-session-file-resource | `GET /agent/managed/v1/sessions/{sessionId}/resources/{resourceId}`：获取 File Resource 挂载信息 |
| https://docs.bigmodel.cn/cn/api-reference/managed-agents--session/订阅实时事件 | `GET /agent/managed/v1/sessions/{sessionId}/events/stream`：SSE 实时流（live-only；`event_deltas[]` 做增量预览） |
| https://docs.bigmodel.cn/cn/api-reference/managed-agents--skill/下载-skill-zip | `GET /agent/managed/v1/skills/{id}/versions/{v}/content`：下载该不可变版本的 ZIP 内容 |
| https://docs.bigmodel.cn/cn/api-reference/managed-agents--skill/列出-skill | `GET /agent/managed/v1/skills`：分页列出 Skill；query `source`（custom/zai）・`limit`・`order`・`page` |
| https://docs.bigmodel.cn/cn/api-reference/managed-agents--skill/列出-skill-version | `GET /agent/managed/v1/skills/{id}/versions`：分页列出 Skill 历史版本 |
| https://docs.bigmodel.cn/cn/api-reference/managed-agents--skill/创建-skill | `POST /agent/managed/v1/skills`：multipart 上传完整 Skill 目录（必须含顶层 `SKILL.md`）并创建首个版本 |
| https://docs.bigmodel.cn/cn/api-reference/managed-agents--skill/创建-skill-version | `POST /agent/managed/v1/skills/{id}/versions`：重新上传目录创建新版本（同样要求顶层 `SKILL.md`） |
| https://docs.bigmodel.cn/cn/api-reference/managed-agents--skill/删除-skill | `DELETE /agent/managed/v1/skills/{id}`：删除 Skill 及其版本；被活动配置引用时返回错误 |
| https://docs.bigmodel.cn/cn/api-reference/managed-agents--skill/删除-skill-version | `DELETE /agent/managed/v1/skills/{id}/versions/{v}`：删除版本；被 Agent 配置引用的版本不能删 |
| https://docs.bigmodel.cn/cn/api-reference/managed-agents--skill/获取-skill | `GET /agent/managed/v1/skills/{id}`：返回 Skill 元数据与当前版本信息 |
| https://docs.bigmodel.cn/cn/api-reference/managed-agents--skill/获取-skill-version | `GET /agent/managed/v1/skills/{id}/versions/{v}`：返回版本元数据与不可变信息 |
| https://docs.bigmodel.cn/cn/api-reference/managed-agents--vault/列出-credential | `GET /agent/managed/v1/vaults/{vaultId}/credentials`：列出凭据；密钥/Token 等敏感值不会在响应中返回 |
| https://docs.bigmodel.cn/cn/api-reference/managed-agents--vault/列出-vault | `GET /agent/managed/v1/vaults`：分页列出当前身份拥有的 Vault；query `limit`・`page`・`include_archived` |
| https://docs.bigmodel.cn/cn/api-reference/managed-agents--vault/创建-credential | `POST /agent/managed/v1/vaults/{vaultId}/credentials`：创建凭据（`auth` 必填，含 5 种认证类型） |
| https://docs.bigmodel.cn/cn/api-reference/managed-agents--vault/创建-vault | `POST /agent/managed/v1/vaults`：创建 Vault（`display_name` 必填 1–255，`metadata` 可选） |
| https://docs.bigmodel.cn/cn/api-reference/managed-agents--vault/删除-credential | `DELETE /agent/managed/v1/vaults/{vaultId}/credentials/{credentialId}`：永久删除凭据，删除后运行时不能再使用 |
| https://docs.bigmodel.cn/cn/api-reference/managed-agents--vault/删除-vault | `DELETE /agent/managed/v1/vaults/{vaultId}`：永久删除 Vault 并级联删除其中 Credential |
| https://docs.bigmodel.cn/cn/api-reference/managed-agents--vault/归档-credential | `POST /agent/managed/v1/vaults/{vaultId}/credentials/{credentialId}/archive`：归档凭据，不能再注入新 Session/Deployment |
| https://docs.bigmodel.cn/cn/api-reference/managed-agents--vault/归档-vault | `POST /agent/managed/v1/vaults/{vaultId}/archive`：归档 Vault，其中凭据不能再用于新的运行时注入 |
| https://docs.bigmodel.cn/cn/api-reference/managed-agents--vault/更新-credential | `POST /agent/managed/v1/vaults/{vaultId}/credentials/{credentialId}`：轮换凭据；`auth.type`/`host` 等身份键不可修改 |
| https://docs.bigmodel.cn/cn/api-reference/managed-agents--vault/更新-vault | `POST /agent/managed/v1/vaults/{vaultId}`：更新 `display_name`/`metadata`（补丁语义） |
| https://docs.bigmodel.cn/cn/api-reference/managed-agents--vault/获取-credential | `GET /agent/managed/v1/vaults/{vaultId}/credentials/{credentialId}`：返回元数据与认证类型；不返回密钥明文 |
| https://docs.bigmodel.cn/cn/api-reference/managed-agents--vault/获取-vault | `GET /agent/managed/v1/vaults/{vaultId}`：获取 Vault；响应不返回任何凭据明文 |
| https://docs.bigmodel.cn/cn/api-reference/managed-agents--vault/验证-mcp-oauth | `POST /agent/managed/v1/vaults/{vaultId}/credentials/{credentialId}/mcp_oauth_validate`：验证 MCP OAuth 凭据连通性 |
| https://docs.bigmodel.cn/cn/api-reference/response/创建-response | `POST /v1/responses`：创建 Response（`model`、`input` 必填；`tools`/`reasoning.effort`/`store` 等；SSE `response.*` 事件） |
| https://docs.bigmodel.cn/cn/api-reference/response/删除-response | `DELETE /v1/responses/{response_id}`：删除已保存的 Response（创建时须 `store=true`） |
| https://docs.bigmodel.cn/cn/api-reference/response/查询-response | `GET /v1/responses/{response_id}`：按 id 检索已保存的 Response（创建时须 `store=true`） |
| https://docs.bigmodel.cn/cn/api-reference/response/查询输入项列表 | `GET /v1/responses/{response_id}/input_items`：列出输入项（query `order`・`limit`・`after`；返回 `data[]`・`first_id`・`last_id`） |
| https://docs.bigmodel.cn/cn/api-reference/工具-api/ocr-服务 | `POST /paas/v4/files/ocr`：`file`、`tool_type`（仅 `hand_write`）必填；`language_type` 默认 CHN_ENG、`probability` 默认 false |
| https://docs.bigmodel.cn/cn/api-reference/工具-api/内容安全 | `POST /paas/v4/moderations`：`model`（仅 moderation）、`input`；返回 `result_list[]`（risk_level PASS/REVIEW/BLOCK） |
| https://docs.bigmodel.cn/cn/api-reference/工具-api/文件解析 | `POST /paas/v4/files/parser/create`：multipart `file`、`tool_type`（lite/expert/prime）、`file_type`；异步，先拿 task_id |
| https://docs.bigmodel.cn/cn/api-reference/工具-api/文件解析同步 | `POST /paas/v4/files/parser/sync`：multipart `file`、`tool_type`（仅 `prime-sync`）、`file_type`；返回 status、task_id、content |
| https://docs.bigmodel.cn/cn/api-reference/工具-api/网络搜索 | `POST /paas/v4/web_search`：`search_query`/`search_engine`/`search_intent` 必填；`count` 默认 10（1-50）；返回 `search_result[]` |
| https://docs.bigmodel.cn/cn/api-reference/工具-api/网页阅读 | `POST /paas/v4/reader`：`url` 必填；`timeout` 默认 20、`return_format` 默认 markdown、`retain_images` 默认 true；返回 `reader_result` |
| https://docs.bigmodel.cn/cn/api-reference/工具-api/解析结果 | `GET /paas/v4/files/parser/result/{taskId}/{format_type}`：按 taskId 与 format_type（text/download_link）取文件解析结果 |
| https://docs.bigmodel.cn/cn/api-reference/批处理-api/列出批处理任务 | `GET /paas/v4/batches`：列出 Batch；query `after`・`limit`（默认 20）；返回 `data[]`・`first_id`・`last_id`・`has_more` |
| https://docs.bigmodel.cn/cn/api-reference/批处理-api/创建批处理任务 | `POST /paas/v4/batches`：`input_file_id`、`endpoint`（目前仅 `/v4/chat/completions`）必填；`auto_delete_input_file` 默认 true |
| https://docs.bigmodel.cn/cn/api-reference/批处理-api/取消批处理任务 | `POST /paas/v4/batches/{batch_id}/cancel`：取消正在运行的 Batch 任务 |
| https://docs.bigmodel.cn/cn/api-reference/批处理-api/检索批处理任务 | `GET /paas/v4/batches/{batch_id}`：按 ID 获取 Batch 详情（`status`、`request_counts`、`output_file_id` 等） |
| https://docs.bigmodel.cn/cn/api-reference/文件-api/上传文件 | `POST /paas/v4/files`：`file`、`purpose` 必填（batch/code-interpreter/agent/voice-clone-input/user_data）；返回 `FileObject` |
| https://docs.bigmodel.cn/cn/api-reference/文件-api/删除文件 | `DELETE /paas/v4/files/{file_id}`：永久删除指定文件及其所有关联数据 |
| https://docs.bigmodel.cn/cn/api-reference/文件-api/文件内容 | `GET /paas/v4/files/{file_id}/content`：取文件内容（octet-stream），只支持 `batch` 文件类型 |
| https://docs.bigmodel.cn/cn/api-reference/文件-api/文件列表 | `GET /paas/v4/files`：分页列出文件；query `after`・`purpose`（必填）・`order`・`limit`（默认 20） |
| https://docs.bigmodel.cn/cn/api-reference/模型-api/删除音色 | `POST /paas/v4/voice/delete`：删除音色；请求 `voice`、`request_id`；返回 `voice`、`update_time` |
| https://docs.bigmodel.cn/cn/api-reference/模型-api/图像生成 | `POST /paas/v4/images/generations`：`model`（glm-image/cogview-4 等）、`prompt` 必填；`quality` 默认 hd；返回 `data[].url` |
| https://docs.bigmodel.cn/cn/api-reference/模型-api/图像生成异步 | `POST /paas/v4/async/images/generations`：仅 `glm-image`；返回 `id`、`task_status`（PROCESSING/SUCCESS/FAIL） |
| https://docs.bigmodel.cn/cn/api-reference/模型-api/对话补全 | `POST /paas/v4/chat/completions`：oneOf 文本/视觉/音频三型（`model`、`messages` 必填）；`thinking`、`reasoning_effort`、`tools`（≤128 函数） |
| https://docs.bigmodel.cn/cn/api-reference/模型-api/对话补全异步 | `POST /paas/v4/async/chat/completions`：字段同同步版但无 `stream`；返回 `AsyncResponse`（`id`、`task_status`） |
| https://docs.bigmodel.cn/cn/api-reference/模型-api/文本分词器 | `POST /paas/v4/tokenizer`：`model`、`messages` 必填；返回 `usage`（prompt_tokens、video_tokens、image_tokens、total_tokens） |
| https://docs.bigmodel.cn/cn/api-reference/模型-api/文本嵌入 | `POST /paas/v4/embeddings`：`model`（embedding-2/3）、`input` 必填；embedding-3 `dimensions` 可选 256/512/1024/2048 |
| https://docs.bigmodel.cn/cn/api-reference/模型-api/文本转语音 | `POST /paas/v4/audio/speech`：`model`（glm-tts）、`input`（≤1024）、`voice` 必填；`speed` 默认 1.0；流式仅返回 pcm |
| https://docs.bigmodel.cn/cn/api-reference/模型-api/文本重排序 | `POST /paas/v4/rerank`：`model`（仅 rerank）、`query`、`documents`（≤128 条）必填；返回 `results[]` |
| https://docs.bigmodel.cn/cn/api-reference/模型-api/文档解析 | `POST /paas/v4/layout_parsing`：`model`（仅 glm-ocr）、`file` 必填；返回 `md_results`、`layout_details`、`data_info` |
| https://docs.bigmodel.cn/cn/api-reference/模型-api/查询异步结果 | `GET /paas/v4/async-result/{id}`：查询对话补全/图像/视频生成异步结果；图片临时链接有效期 30 天 |
| https://docs.bigmodel.cn/cn/api-reference/模型-api/视频生成异步 | `POST /paas/v4/videos/generations`：请求体 6 个 oneOf（CogVideoX/Vidu）；返回 `id`、`task_status` |
| https://docs.bigmodel.cn/cn/api-reference/模型-api/语音转文本 | `POST /paas/v4/audio/transcriptions`：`model`（glm-asr-2512）等；文件 ≤25MB 且时长 ≤30 秒；流式 `transcript.text.delta/done` |
| https://docs.bigmodel.cn/cn/api-reference/模型-api/音色列表 | `GET /paas/v4/voice/list`：query `voiceName`、`voiceType`（PRIVATE/OFFICIAL）；返回 `voice_list[]`（含 `download_url`） |
| https://docs.bigmodel.cn/cn/api-reference/模型-api/音色复刻 | `POST /paas/v4/voice/clone`：`voice_name`、`input`、`file_id`、`model`（glm-tts-clone）必填；返回 `voice`、`file_id` |
| https://docs.bigmodel.cn/cn/api-reference/知识库-api/上传url文档 | `POST /llm-application/open/document/upload_url`：`upload_detail`（url、knowledge_type 必填）、`knowledge_id` 必填 |
| https://docs.bigmodel.cn/cn/api-reference/知识库-api/上传文件文档 | `POST /llm-application/open/document/upload_document/{id}`（multipart）：`files`、`knowledge_type`、`sentence_size` 20–2000 |
| https://docs.bigmodel.cn/cn/api-reference/知识库-api/全模态知识库检索 | `POST /zrag/retrieval/retrieve`：`knows` 必填；`query` 或 `multimodal_parts` 必传其一；返回 `data.contents[]`、`rewritten_query` |
| https://docs.bigmodel.cn/cn/api-reference/知识库-api/创建知识库 | `POST /llm-application/open/knowledge`：`embedding_id`、`name` 必填；另有 `contextual`、`background`、`icon`；返回 `data.id` |
| https://docs.bigmodel.cn/cn/api-reference/知识库-api/删除文档 | `DELETE /llm-application/open/document/{id}`：按文档 ID 删除文档 |
| https://docs.bigmodel.cn/cn/api-reference/知识库-api/删除知识库 | `DELETE /llm-application/open/knowledge/{id}`：按知识库 ID 删除个人知识库 |
| https://docs.bigmodel.cn/cn/api-reference/知识库-api/文档列表 | `GET /llm-application/open/document`：query `knowledge_id`、`page`、`size`、`word` 必填；返回 `data.list[]` |
| https://docs.bigmodel.cn/cn/api-reference/知识库-api/文档详情 | `GET /llm-application/open/document/{id}`：返回文档详情（KnowledgeDocumentItem） |
| https://docs.bigmodel.cn/cn/api-reference/知识库-api/知识库使用量 | `GET /llm-application/open/knowledge/capacity`：获取个人知识库使用量（`word_num` 字数、`length` 字节数） |
| https://docs.bigmodel.cn/cn/api-reference/知识库-api/知识库列表 | `GET /llm-application/open/knowledge`：query `page`（默认 1）、`size`（默认 10）；返回 `data.list[]`、`data.total` |
| https://docs.bigmodel.cn/cn/api-reference/知识库-api/知识库检索 | `POST /llm-application/open/knowledge/retrieve`：`query`、`knowledge_ids` 必填；`recall_method` embedding/keyword/mixed |
| https://docs.bigmodel.cn/cn/api-reference/知识库-api/知识库详情 | `GET /llm-application/open/knowledge/{id}`：返回知识库详情（KnowledgeListItem） |
| https://docs.bigmodel.cn/cn/api-reference/知识库-api/编辑知识库 | `PUT /llm-application/open/knowledge/{id}`：仅传要改的字段；改向量模型需 `callback_url` 重新构建 |
| https://docs.bigmodel.cn/cn/api-reference/知识库-api/解析文档图片 | `POST /llm-application/open/document/slice/image_list/{id}`：返回文档解析到的图片序号与链接（`text`、`cos_url`） |
| https://docs.bigmodel.cn/cn/api-reference/知识库-api/重新向量化 | `POST /llm-application/open/document/embedding/{id}`：重新向量化文档；完成后回调 `callback_url` |
| https://docs.bigmodel.cn/cn/api/api-code | 业务错误码全表（如 1211 模型不存在、1301 敏感内容、1302/1305 限流）；错误体 `{"error": {"code", "message"}}` |
| https://docs.bigmodel.cn/cn/api/introduction | API 文档快速开始：通用端点、`Authorization: Bearer` 鉴权 |
| https://docs.bigmodel.cn/cn/api/rate-limit | 速率限制：并发请求数限制、1302/1305、GLM Coding Plan 套餐并发、申请提升流程（审核 10 个工作日） |
| https://docs.bigmodel.cn/cn/asyncapi/realtime | GLM-Realtime 音视频通话 AsyncAPI 规格页：`id: realtime`、`servers` 为 `wss://open.bigmodel.cn`；浏览器不允许 WebSocket 带鉴权头 |
| https://docs.bigmodel.cn/cn/best-practice/latency-optimization | 延迟优化指南：选模型与调用方式、减少输出、控制输入与缓存复用、流式输出（示例 `stream: true`）、避免默认用大模型 |
| https://docs.bigmodel.cn/cn/guide/capabilities/cache | 上下文缓存：隐式缓存、重复前缀建议 ≥500 Token 才命中；命中按标准价 50% 计费；`prompt_tokens_details.cached_tokens` |
| https://docs.bigmodel.cn/cn/guide/capabilities/function-calling | 工具调用：`tools`（≤128 函数）、`tool_choice` 仅 `auto`、回传 `role: "tool"` + `tool_call_id`；函数/知识库/搜索互斥 |
| https://docs.bigmodel.cn/cn/guide/capabilities/stream-tool | 工具流式输出：`tool_stream`（默认 false，仅 GLM-5.3/5.2/5.1/5/5-Turbo/4.7/4.6）；需同时 `stream: true` |
| https://docs.bigmodel.cn/cn/guide/capabilities/streaming | 流式消息：SSE 增量 `delta.content`/`delta.reasoning_content`，最后 chunk 带 `finish_reason` 与 `usage`，以 `data: [DONE]` 结束 |
| https://docs.bigmodel.cn/cn/guide/capabilities/struct-output | 结构化输出：`response_format.type` 取 `text`/`json_object`（仅文本模型）；Response 侧为 `text.format.type` |
| https://docs.bigmodel.cn/cn/guide/capabilities/thinking | 深度思考：`thinking{type,clear_thinking}`（默认 enabled/true）、`reasoning_effort`（默认 max）、各模型档位与强制思考清单 |
| https://docs.bigmodel.cn/cn/guide/capabilities/thinking-mode | 思考模式：交错式（Interleaved）、保留式（Preserved，`clear_thinking: false`）、轮级思考（GLM-4.7 新增） |
| https://docs.bigmodel.cn/cn/guide/develop/claude/introduction | Claude API 兼容：base_url `https://open.bigmodel.cn/api/anthropic`、`/v1/messages`、`x-api-key`、各语言 SDK |
| https://docs.bigmodel.cn/cn/guide/develop/http/introduction | HTTP API 调用入口：端点、请求头、两种鉴权（Bearer API Key 与 JWT Token） |
| https://docs.bigmodel.cn/cn/guide/develop/java/introduction | 官方 Java SDK（groupId `ai.z.openapi`）：Java 1.8+、Maven 3.6+/Gradle 6.0+、`ZhipuAiClient.builder()` |
| https://docs.bigmodel.cn/cn/guide/develop/langchain/introduction | LangChain 集成（走 OpenAI 兼容 `https://open.bigmodel.cn/api/paas/v4/`）：`ChatOpenAI`、`ConversationBufferMemory`、Agent |
| https://docs.bigmodel.cn/cn/guide/develop/openai/introduction | OpenAI API 兼容：base_url `https://open.bigmodel.cn/api/paas/v4`、常用参数表与注意事项 |
| https://docs.bigmodel.cn/cn/guide/develop/python/introduction | 官方 Python SDK（`zai-sdk`）：Python 3.8+、`ZhipuAiClient`、chat/images/videos/embeddings 用法与 thinking |
| https://docs.bigmodel.cn/cn/guide/develop/responses/introduction | Response API 兼容：base_url `https://open.bigmodel.cn/api/v1`、参数表、接口一览、与 OpenAI 的差异 |
| https://docs.bigmodel.cn/cn/guide/models/image-generation/glm-image | GLM-Image 模型页：尺寸规范、多分辨率；`size` 推荐 1280x1280、自定义 1024px–2048px 且 32 的整数倍 |
| https://docs.bigmodel.cn/cn/guide/models/text/glm-5.2 | GLM-5.2 模型页：1M 上下文、128K 输出；`reasoning_effort` 全档位（none/minimal/low/medium/high/xhigh/max） |
| https://docs.bigmodel.cn/cn/guide/models/text/glm-5.3 | GLM-5.3 模型页：能力变更、三协议 Base URL、`thinking`/`reasoning_effort` 表、调用示例 |
| https://docs.bigmodel.cn/cn/guide/models/vlm/glm-5.3-flash | GLM-5.3-Flash/FlashX 模型页：多模态输入、参数推荐（`temperature: 1`、`top_p: 0.95`、`reasoning_effort: max`） |
| https://docs.bigmodel.cn/cn/guide/models/vlm/glm-ocr | GLM-OCR 模型页：输入输出模态、单图 ≤10MB/PDF ≤50MB/最多 100 页、`/layout_parsing` 示例 |
| https://docs.bigmodel.cn/cn/guide/platform/equity-explain | 用户权益等级 V0–V3（积分区间与并发权益）、积分按 1:1 兑换、T+1 06:00 更新 |
| https://docs.bigmodel.cn/cn/guide/platform/filing | 模型备案信息：按《生成式人工智能服务管理暂行办法》公告的备案号与时间（如 Beijing-ChatGLM-20230821） |
| https://docs.bigmodel.cn/cn/guide/platform/securityaudit | 内容安全：同步错误码 1301、流式返回 `finish_reason: sensitive`、终端用户 ID（6–128 字符）、审核计费单价 |
| https://docs.bigmodel.cn/cn/guide/start/concept-param | 核心参数：`do_sample`/`temperature`/`top_p`/`max_tokens`/`stream`/`thinking`/`reasoning_effort` 与各模型 max_tokens 表 |
| https://docs.bigmodel.cn/cn/guide/start/introduction | 平台介绍：核心概念 GLM / Token / 上下文窗口 |
| https://docs.bigmodel.cn/cn/guide/start/migrate-to-glm-new | 迁移至 GLM-5.3 的迁移清单与要点 |
| https://docs.bigmodel.cn/cn/guide/start/model-overview | 模型概览：推荐模型与全部模型的上下文、最大输出 |
| https://docs.bigmodel.cn/cn/guide/start/quick-start | 快速开始：注册、API Key、cURL / Python SDK / Java SDK 示例 |
| https://docs.bigmodel.cn/cn/guide/tools/batch | 批量处理：Batch API 全流程、状态表（validating/in_progress/completed/expired 等）、Batch 对象字段、限制与 FAQ |
| https://docs.bigmodel.cn/cn/guide/tools/evaluation | 模型评测：AI 裁判员/基线评测、裁判模板变量 `{{evaluation_scene}}` 等、评测参数、前 3 次免费 |
| https://docs.bigmodel.cn/cn/guide/tools/file-parser | 异步解析：三种服务对比（Prime/Expert/Lite）、`tool_type`/`file_type`/`format_type` 参数表、轮询流程 |
| https://docs.bigmodel.cn/cn/guide/tools/file-parser-sync | 同步解析（Prime-sync）：支持格式与大小（WPS/PDF ≤100MB）、`tool_type` 固定 `prime-sync`、`task_id` 轮询 |
| https://docs.bigmodel.cn/cn/guide/tools/fine-tuning | 模型微调：SFT、LoRA/全参数、可微调模型清单、数据集格式、训练计费公式、单图固定 token |
| https://docs.bigmodel.cn/cn/guide/tools/knowledge/contextual | 上下文增强技术报告：双索引 + 加权 RRF 融合与重排、缓存命中率 85%+、召回率提升数据 |
| https://docs.bigmodel.cn/cn/guide/tools/knowledge/multimodal-retrieval | 全模态知识库：创建参数、导入限制（文档 ≤100MB/图片 ≤5MB/视频 ≤15 分钟）、检索参数默认值（召回 8、分数 0.3） |
| https://docs.bigmodel.cn/cn/guide/tools/knowledge/q&a | 知识问答（体验中心）：视觉/文本模型调用知识库、检索参数默认值、上传限额（图片 ≤5MB ≤150 张） |
| https://docs.bigmodel.cn/cn/guide/tools/knowledge/retrieval | 对话调用知识库：`tools[].type=retrieval` 传知识库 ID、`prompt_template` 含 `{{knowledge}}`/`{{question}}`；知识库最大 1G |
| https://docs.bigmodel.cn/cn/guide/tools/model-deploy | 模型部署：私有实例部署流程、可部署模型、计费与常见问题 |
| https://docs.bigmodel.cn/cn/guide/tools/web-search | 联网搜索：Web Search API、对话中搜索、搜索智能体、四种搜索引擎与价格 |
| https://docs.bigmodel.cn/cn/guide/tools/zhipu-ocr | OCR 服务：请求参数（`tool_type` 固定 `hand_write`、`language_type` 默认 CHN_ENG）、响应字段、格式 ≤8M、计费 |
| https://docs.bigmodel.cn/cn/managed-agents/agent-setup | 定义 Agent：配置字段（`name`/`model`/`system`/`tools`/`skills`）、effort 档位（glm-5.3 默认 max）、更新语义与生命周期 |
| https://docs.bigmodel.cn/cn/managed-agents/api-reference | Managed Agents API 速查：Base URL `https://agent-api.bigmodel.cn/api/agent/managed`、请求头、分页、限流、全部端点总表与错误码 |
| https://docs.bigmodel.cn/cn/managed-agents/cloud-environment | 配置运行环境：`config.type: cloud`、`packages` 六个包管理器、`networking`（unrestricted/limited、`allowed_hosts` ≤256） |
| https://docs.bigmodel.cn/cn/managed-agents/create-session | 创建会话：字段表（`agent`/`environment_id` 必填）、`x-checkpoint` 头跨轮保留、`initial_events` 预置、Vault 提供 MCP 鉴权 |
| https://docs.bigmodel.cn/cn/managed-agents/deployments | 定时任务：Deployment 字段表（`schedule` cron、`initial_events` 1–50 条）、pause/unpause/archive/run 语义、列表过滤 |
| https://docs.bigmodel.cn/cn/managed-agents/events | 事件与流式：客户端/平台事件类型、三个事件接口、SSE 断线恢复、增量预览（`event_deltas`）、自定义工具回路 |
| https://docs.bigmodel.cn/cn/managed-agents/examples | 最佳实践示例：客服 Agent（glm-5.3-flash、只开 read/ls、custom 工具）与数据分析 Agent（全开工具集、产物写 `/mnt/session/outputs`） |
| https://docs.bigmodel.cn/cn/managed-agents/files | 文件：输入（`POST /v1/files` 上传并挂载）与产物（`/mnt/session/outputs` 编目）差异、file 对象字段、`mount_path` 规则 |
| https://docs.bigmodel.cn/cn/managed-agents/mcp | 连接 MCP：`mcp_servers[]`（`type`/`name`/`url`，一个 Agent 最多 20 个）、`mcp_toolset` 配置、用 Vault 提供鉴权 |
| https://docs.bigmodel.cn/cn/managed-agents/memory-stores | 记忆：Memory Store 工作方式、挂载到会话（`access`/`instructions`、单会话 ≤8 个）、沙箱路径 `/mnt/memory/`、审计变更 |
| https://docs.bigmodel.cn/cn/managed-agents/overview | 概述：托管 Agent 概念（Agent/Environment/Session）、内置工具、计费（模型按量、沙箱限时免费、暂不支持 Coding Plan）、Beta |
| https://docs.bigmodel.cn/cn/managed-agents/permission-policies | 工具权限：`always_allow`/`always_ask` 策略、配置位置、审批事件流程（`user.tool_confirmation`）、自定义工具不受策略约束 |
| https://docs.bigmodel.cn/cn/managed-agents/quickstart | 快速入门：核心概念、环境变量（`BASE_URL`）、创建第一个会话流程（Agent→Environment→Session→流式→下载产出） |
| https://docs.bigmodel.cn/cn/managed-agents/sandbox-reference | 预装沙箱环境：语言与版本（Python 3.11、Node 22、Go 1.24 等）、数据库（SQLite/PostgreSQL 16/Redis 7）、工具清单与目录约定 |
| https://docs.bigmodel.cn/cn/managed-agents/session-operations | 管理会话：状态枚举（idle/running/rescheduling/terminated）、更新字段规则、列表过滤参数、归档与删除 |
| https://docs.bigmodel.cn/cn/managed-agents/skills | Skills：平台内置（`planning`/`code-review`）与自定义 Skill、上限（≤20MB/≤200 文件）、版本接口表、挂载字段 |
| https://docs.bigmodel.cn/cn/managed-agents/tools | 工具：内置工具集 `agent_toolset_20260601`（bash/read/write/edit/grep/find/ls）、`configs[]` 配置、自定义工具约束 |
| https://docs.bigmodel.cn/cn/managed-agents/vaults | 凭据管理：创建 Vault、Credential 5 种 `auth.type`、会话 `vault_ids` 引用、验证 MCP OAuth、轮换与安全边界 |
| https://docs.bigmodel.cn/cn/update/new-releases | 模型与产品发布记录：按日期罗列模型/产品上新要点（GLM-5.3-Flash、GLM-5.3、团队版、语音/视频/OCR 等）；纯发布说明 |

**未能抓取 / 未纳入**

- `managed-agents/api-reference`（文档站该页）：`.md` 原文请求返回 404（内容已由同名的指南页整理，见上表对应行）。
- 文档中确有、但不在本 192 条规范清单内的页：`guide/models/text/glm-4.md`、`glm-4-long.md`、`glm-4.7-flash`、`glm-4v-*` 等子模型详情页（本次未逐页抓取）。
- 文档中不存在、故无法收录的页：Anthropic 兼容端点的字段级差异清单；各模型页之外的独立「模型限制」汇总页。


### Kimi / Moonshot — 全部页面 180 条

| 页面（规范 URL） | 这页讲了什么 |
|---|---|
| https://platform.kimi.com/docs/api-reference/产物/下载产物内容 | `GET /v1/artifacts/{id}/content`；`version` 查询参数；返回任意媒体类型的内容字节 |
| https://platform.kimi.com/docs/api-reference/产物/列出产物 | `GET /v1/artifacts`；`session_id` 筛选；`items[]`（`id`/`path`/`name`/`mime_type`/`size_bytes`/`delivered_at`） |
| https://platform.kimi.com/docs/api-reference/产物/列出产物版本 | `GET /v1/artifacts/{id}/versions`；`items[]` 为该版本完整产物，最新在前 |
| https://platform.kimi.com/docs/api-reference/产物/删除产物 | `DELETE /v1/artifacts/{id}`；204；永久删除产物及全部版本 |
| https://platform.kimi.com/docs/api-reference/产物/获取产物 | `GET /v1/artifacts/{id}`；`version` 查询参数；返回产物元数据 |
| https://platform.kimi.com/docs/api-reference/会话/下载会话文件系统中的文件 | `GET /v1/sessions/{id}/filesystem/raw`；`path` 查询参数；返回内容字节 |
| https://platform.kimi.com/docs/api-reference/会话/列出会话 | `GET /v1/sessions`；`order`/`agent_id`/`agent_version`/`statuses`/`include_archived`；`items[]` |
| https://platform.kimi.com/docs/api-reference/会话/列出会话事件 | `GET /v1/sessions/{id}/events`；`order`；`stream_cursor`；历史不含 `agent.delta` |
| https://platform.kimi.com/docs/api-reference/会话/列出会话文件系统中的文件 | `GET /v1/sessions/{id}/filesystem`；`prefix`；`name`/`size_bytes`/`is_dir`/`last_modified` |
| https://platform.kimi.com/docs/api-reference/会话/列出会话的线程 | `GET /v1/sessions/{id}/threads`；`parent_thread_id`/`agent`/`status`；协调者在前 |
| https://platform.kimi.com/docs/api-reference/会话/列出会话资源 | `GET /v1/sessions/{id}/resources`；文件/凭据库/记忆库 union；带绑定 `id` |
| https://platform.kimi.com/docs/api-reference/会话/列出待处理事件 | `GET /v1/sessions/{id}/events/pending`；`event_id`/`type`/`events[]`/`queued_at`；不分页 |
| https://platform.kimi.com/docs/api-reference/会话/列出线程事件 | `GET /v1/sessions/{id}/threads/{thread_id}/events`；`order`；`stream_cursor` |
| https://platform.kimi.com/docs/api-reference/会话/创建会话 | `POST /v1/sessions`；`agent_id`/`environment_id`；初始 `status: idle`；`resources`/`agent_overrides` |
| https://platform.kimi.com/docs/api-reference/会话/删除会话 | `DELETE /v1/sessions/{id}`；须先归档，否则 400 `failed_precondition_error` |
| https://platform.kimi.com/docs/api-reference/会话/向会话发送事件 | `POST /v1/sessions/{id}/events`；`user.message`/`user.interrupt`；`type` 取 `next_turn`/`steer`；202 `event_id` |
| https://platform.kimi.com/docs/api-reference/会话/归档会话 | `POST /v1/sessions/{id}/archive`；204；终止会话并取消线程，状态转 `terminated` |
| https://platform.kimi.com/docs/api-reference/会话/归档会话的线程 | `POST /v1/sessions/{id}/threads/{thread_id}/archive`；仅空闲子线程；204 |
| https://platform.kimi.com/docs/api-reference/会话/撤回待处理事件 | `DELETE /v1/sessions/{id}/events/pending/{event_id}`；204；投递前移除排队输入 |
| https://platform.kimi.com/docs/api-reference/会话/更新会话 | `PATCH /v1/sessions/{id}`；仅 `title` 与 `agent_overrides` 可改；已终止拒绝 |
| https://platform.kimi.com/docs/api-reference/会话/流式获取会话事件（sse） | `GET /v1/sessions/{id}/events/stream`；SSE 帧 `event`/`id`/`data`；`cursor`、`Last-Event-ID` |
| https://platform.kimi.com/docs/api-reference/会话/流式获取线程事件（sse） | `GET /v1/sessions/{id}/threads/{thread_id}/events/stream`；帧格式与游标语义同会话流 |
| https://platform.kimi.com/docs/api-reference/会话/添加会话资源 | `POST /v1/sessions/{id}/resources`；`type: file`、`file_id`；201；重复绑定 409 |
| https://platform.kimi.com/docs/api-reference/会话/移除会话资源 | `DELETE /v1/sessions/{id}/resources/{resource_id}`；204；文件的工作区链接一并移除 |
| https://platform.kimi.com/docs/api-reference/会话/获取会话 | `GET /v1/sessions/{id}`；含资源绑定与协调者智能体快照（含系统提示词） |
| https://platform.kimi.com/docs/api-reference/会话/获取会话的线程 | `GET /v1/sessions/{id}/threads/{thread_id}`；`agent` 快照/`status`/`parent_thread_id` |
| https://platform.kimi.com/docs/api-reference/会话/获取会话资源 | `GET /v1/sessions/{id}/resources/{resource_id}`；资源绑定详情 |
| https://platform.kimi.com/docs/api-reference/凭据库/列出凭据 | `GET /v1/vaults/{id}/credentials`；`include_archived`；`auth`/`display_name`；秘密值不返回 |
| https://platform.kimi.com/docs/api-reference/凭据库/列出凭据库（vault） | `GET /v1/vaults`；`include_archived`；`items[]`（`id`/`display_name`/`metadata`） |
| https://platform.kimi.com/docs/api-reference/凭据库/创建-oauth-会话 | `POST /v1/vaults/{id}/oauth-sessions`；`mcp_server_url`；返回 `authorize_url`/`oauth_session_id` |
| https://platform.kimi.com/docs/api-reference/凭据库/创建凭据 | `POST /v1/vaults/{id}/credentials`；`auth`（`mcp_oauth`/`static_bearer`/`environment_variable`）；秘密值只写 |
| https://platform.kimi.com/docs/api-reference/凭据库/创建凭据库（vault） | `POST /v1/vaults`；`display_name`/`metadata`；`Idempotency-Key`；201 |
| https://platform.kimi.com/docs/api-reference/凭据库/删除凭据 | `DELETE /v1/vaults/{id}/credentials/{credential_id}`；须先归档；204 |
| https://platform.kimi.com/docs/api-reference/凭据库/删除凭据库（vault） | `DELETE /v1/vaults/{id}`；须先归档；204；移除记录 |
| https://platform.kimi.com/docs/api-reference/凭据库/归档凭据 | `POST /v1/vaults/{id}/credentials/{credential_id}/archive`；204；清除秘密值并释放唯一键 |
| https://platform.kimi.com/docs/api-reference/凭据库/归档凭据库（vault） | `POST /v1/vaults/{id}/archive`；204；归档全部活跃凭据并清除秘密值 |
| https://platform.kimi.com/docs/api-reference/凭据库/批量创建凭据 | `POST /v1/vaults/{id}/credentials/batch`；`items` 1–20 条原子创建；重复键 400 |
| https://platform.kimi.com/docs/api-reference/凭据库/更新凭据 | `PATCH /v1/vaults/{id}/credentials/{credential_id}`；字段级合并；不能改 `type` 与标识字段 |
| https://platform.kimi.com/docs/api-reference/凭据库/更新凭据库（vault） | `PATCH /v1/vaults/{id}`；`display_name`/`metadata`（整体替换）；空补丁 400 |
| https://platform.kimi.com/docs/api-reference/凭据库/检查凭据覆盖情况 | `POST /v1/vaults/credentials/check`；`mcp_server_urls`/`vault_ids`；`coverage[]`/`matching_vault_ids` |
| https://platform.kimi.com/docs/api-reference/凭据库/获取-oauth-会话 | `GET /v1/vaults/{id}/oauth-sessions/{session_id}`；`status`（`pending`/`authorized`/`failed`）/`credential_id` |
| https://platform.kimi.com/docs/api-reference/凭据库/获取凭据 | `GET /v1/vaults/{id}/credentials/{credential_id}`；非秘密设置；秘密值不返回 |
| https://platform.kimi.com/docs/api-reference/凭据库/获取凭据库（vault） | `GET /v1/vaults/{id}`；凭据库元数据；已归档仍可读 |
| https://platform.kimi.com/docs/api-reference/技能/列出技能 | `GET /v1/skills`；`scope`（`official`/`org`/`project`/`user`）/`status`/`latest_version` |
| https://platform.kimi.com/docs/api-reference/技能/列出技能版本 | `GET /v1/skills/{id}/versions`；`skill_id`/`version`/`name`/`created_at` |
| https://platform.kimi.com/docs/api-reference/技能/创建技能 | `POST /v1/skills`；`file`（`.zip`/`.skill`，含一个 `SKILL.md`）；201 返回 `skill`+`version` |
| https://platform.kimi.com/docs/api-reference/技能/删除技能 | `DELETE /v1/skills/{id}`；等同归档操作；官方技能 403 `permission_denied_error` |
| https://platform.kimi.com/docs/api-reference/技能/归档技能 | `POST /v1/skills/{id}/archive`；204；归档后读取 API 返回 404 |
| https://platform.kimi.com/docs/api-reference/技能/获取技能 | `GET /v1/skills/{id}`；返回技能目录条目；已归档 404 |
| https://platform.kimi.com/docs/api-reference/技能/获取技能版本内容 | `GET /v1/skills/{id}/versions/{version}/content`；返回 `text/markdown`；`version` 不能为 `latest` |
| https://platform.kimi.com/docs/api-reference/插件/列出插件 | `GET /v1/plugins`；`query`；`items[]`（`mcp_servers[]`/`skills[]`/`keywords[]`） |
| https://platform.kimi.com/docs/api-reference/插件/获取插件 | `GET /v1/plugins/{id}`；插件详情含捆绑的 MCP 服务器与技能 |
| https://platform.kimi.com/docs/api-reference/文件/上传文件 | `POST /v1/files`；`file`/`filename`/`purpose`（`file-extract`/`image`/`video`/`batch`）；201 |
| https://platform.kimi.com/docs/api-reference/文件/列出文件 | `GET /v1/files`；`page_size`（1–1000）；`items[]`（`id`/`filename`/`extract_status`） |
| https://platform.kimi.com/docs/api-reference/文件/删除文件 | `DELETE /v1/files/{id}`；204；可能返回 403 |
| https://platform.kimi.com/docs/api-reference/文件/获取文件 | `GET /v1/files/{id}`；返回文件元数据 |
| https://platform.kimi.com/docs/api-reference/文件/获取文件内容 | `GET /v1/files/{id}/content`；`file-extract` 返回 Markdown；`batch` 返回规范化文件 |
| https://platform.kimi.com/docs/api-reference/智能体/列出智能体 | `GET /v1/agents`；`order`/`exclude_multiagent`/`official_only`；`tools` union |
| https://platform.kimi.com/docs/api-reference/智能体/列出智能体版本 | `GET /v1/agents/{id}/versions`；一页智能体版本，最新在前 |
| https://platform.kimi.com/docs/api-reference/智能体/创建智能体 | `POST /v1/agents`；`name`/`model`/`system`/`tools`/`mcp_servers`/`skills`/`plugins`/`multiagent` |
| https://platform.kimi.com/docs/api-reference/智能体/归档智能体 | `POST /v1/agents/{id}/archive`；204；归档后只读 |
| https://platform.kimi.com/docs/api-reference/智能体/更新智能体 | `PATCH /v1/agents/{id}`；`version` 乐观并发；版本过期 409 `conflict_error` |
| https://platform.kimi.com/docs/api-reference/智能体/获取智能体 | `GET /v1/agents/{id}`；返回最新版本的智能体 |
| https://platform.kimi.com/docs/api-reference/环境/列出环境 | `GET /v1/environments`；`include_archived`/`official_only`；`config`/`build_status`/`scope` |
| https://platform.kimi.com/docs/api-reference/环境/创建环境 | `POST /v1/environments`；`config{type:cloud}`/`packages`/`networking`/`setup_script`；`build_status` |
| https://platform.kimi.com/docs/api-reference/环境/删除环境 | `DELETE /v1/environments/{id}`；须先归档且无未终止会话引用 |
| https://platform.kimi.com/docs/api-reference/环境/归档环境 | `POST /v1/environments/{id}/archive`；204；不可撤销 |
| https://platform.kimi.com/docs/api-reference/环境/更新环境 | `PATCH /v1/environments/{id}`；每次更新生成新不可变版本并构建 |
| https://platform.kimi.com/docs/api-reference/环境/获取环境 | `GET /v1/environments/{id}`；含最新版本构建状态 |
| https://platform.kimi.com/docs/api-reference/触发器/列出触发器 | `GET /v1/triggers`；`status`/`policy_type`/`agent_id`；`policy` union「cron 调度」 |
| https://platform.kimi.com/docs/api-reference/触发器/列出触发器运行 | `GET /v1/triggers/{id}/runs`；`has_error`；`status`/`context`/`error.type`/`session_id` |
| https://platform.kimi.com/docs/api-reference/触发器/创建触发器 | `POST /v1/triggers`；`policy{type:cron,expression,timezone}`/`session_mode`/`initial_events` |
| https://platform.kimi.com/docs/api-reference/触发器/归档触发器 | `POST /v1/triggers/{id}/archive`；204；平台管理的触发器不能归档 |
| https://platform.kimi.com/docs/api-reference/触发器/恢复触发器 | `POST /v1/triggers/{id}/unpause`；204；恢复后计划从现在重算 |
| https://platform.kimi.com/docs/api-reference/触发器/暂停触发器 | `POST /v1/triggers/{id}/pause`；204；已暂停时为空操作 |
| https://platform.kimi.com/docs/api-reference/触发器/更新触发器 | `PATCH /v1/triggers/{id}`；部分更新；平台管理的触发器拒绝 |
| https://platform.kimi.com/docs/api-reference/触发器/获取触发器 | `GET /v1/triggers/{id}`；cron 策略活跃时带 `upcoming_runs_at` |
| https://platform.kimi.com/docs/api-reference/触发器/获取触发器运行 | `GET /v1/triggers/{id}/runs/{run_id}`；`status`/`context`（`schedule`/`manual`）/`error.type` |
| https://platform.kimi.com/docs/api-reference/触发器/运行触发器 | `POST /v1/triggers/{id}/run`；`payload`；201 返回终态运行记录 |
| https://platform.kimi.com/docs/api-reference/记忆库/列出记忆 | `GET /v1/memory-stores/{id}/memories`；`path`/`content_sha256`/`content_size_bytes`；不含内容 |
| https://platform.kimi.com/docs/api-reference/记忆库/列出记忆库 | `GET /v1/memory-stores`；`include_archived`；`items[]`（`id`/`name`/`description`） |
| https://platform.kimi.com/docs/api-reference/记忆库/列出记忆版本 | `GET /v1/memory-stores/{id}/memories/{memory_id}/versions`；`order`；`operation`/`source` |
| https://platform.kimi.com/docs/api-reference/记忆库/创建记忆 | `POST /v1/memory-stores/{id}/memories`；`path`/`content`（base64，≤20 MiB）；同路径 409 |
| https://platform.kimi.com/docs/api-reference/记忆库/创建记忆库 | `POST /v1/memory-stores`；`name`/`description`；名称项目内唯一，冲突 409 |
| https://platform.kimi.com/docs/api-reference/记忆库/删除记忆 | `DELETE /v1/memory-stores/{id}/memories/{memory_id}`；204；记录 `deleted` 版本 |
| https://platform.kimi.com/docs/api-reference/记忆库/删除记忆库 | `DELETE /v1/memory-stores/{id}`；须先归档且无会话绑定 |
| https://platform.kimi.com/docs/api-reference/记忆库/删除记忆版本正文 | `POST .../memories/{memory_id}/versions/{version_id}/redact`；设置 `redacted_at` |
| https://platform.kimi.com/docs/api-reference/记忆库/归档记忆库 | `POST /v1/memory-stores/{id}/archive`；204；拒绝写入，读取仍可用 |
| https://platform.kimi.com/docs/api-reference/记忆库/更新梦境策略 | `PATCH /v1/memory-stores/{id}/dream-policy`；`activity`/`schedule`/`environment_id` 整体替换 |
| https://platform.kimi.com/docs/api-reference/记忆库/更新记忆 | `PATCH /v1/memory-stores/{id}/memories/{memory_id}`；`content_sha256` 并发校验 |
| https://platform.kimi.com/docs/api-reference/记忆库/更新记忆库 | `PATCH /v1/memory-stores/{id}`；`name`/`description`；重名 409；空补丁 400 |
| https://platform.kimi.com/docs/api-reference/记忆库/获取梦境策略 | `GET /v1/memory-stores/{id}/dream-policy`；`activity`/`schedule`/`trigger_id`/`environment_id` |
| https://platform.kimi.com/docs/api-reference/记忆库/获取记忆 | `GET /v1/memory-stores/{id}/memories/{memory_id}`；元数据与 base64 内容 |
| https://platform.kimi.com/docs/api-reference/记忆库/获取记忆库 | `GET /v1/memory-stores/{id}`；记忆库元数据 |
| https://platform.kimi.com/docs/api-reference/记忆库/获取记忆版本 | `GET .../memories/{memory_id}/versions/{version_id}`；版本元数据与历史内容 |
| https://platform.kimi.com/docs/api/batch-cancel | `POST /v1/batches/{batch_id}/cancel`；仅 `validating`/`in_progress`/`finalizing` 可取消 |
| https://platform.kimi.com/docs/api/batch-create | `POST /v1/batches`；`input_file_id`/`endpoint`/`completion_window`/`metadata` |
| https://platform.kimi.com/docs/api/batch-list | `GET /v1/batches`；`request_counts`/`status`/`output_file_id`/`error_file_id` |
| https://platform.kimi.com/docs/api/batch-retrieve | `GET /v1/batches/{batch_id}`；任务状态与各阶段时间戳 |
| https://platform.kimi.com/docs/api/chat | Chat Completions 请求/响应字段、SSE 结构；`reasoning_content`/`tool_calls`/`prompt_cache_options` |
| https://platform.kimi.com/docs/api/errors | 错误码表；`error.type` 取值、含义与建议动作 |
| https://platform.kimi.com/docs/api/estimate | `POST /v1/tokenizers/estimate-token-count`；请求 `model`/`messages`；取 `data.total_tokens` |
| https://platform.kimi.com/docs/api/files-content | `GET /v1/files/{file_id}/content`；`file-extract` 返回 Markdown 提取结果 |
| https://platform.kimi.com/docs/api/files-delete | `DELETE /v1/files/{file_id}`；返回 `id`/`object`/`deleted`；不存在 404 |
| https://platform.kimi.com/docs/api/files-list | `GET /v1/files`；`page_size`（1–1000，默认 50）/`page_token`；`items[]`/`next_page_token` |
| https://platform.kimi.com/docs/api/files-retrieve | `GET /v1/files/{file_id}`；`object`/`bytes`/`created_at`/`status`/`status_details` |
| https://platform.kimi.com/docs/api/files-upload | `POST /v1/files`；`file`/`filename`/`purpose`；`mime_type`/`size_bytes`/`extract_status` |
| https://platform.kimi.com/docs/api/join-the-community | 扫码加入 Kimi 开发者反馈群；无协议字段（纯说明） |
| https://platform.kimi.com/docs/api/list-models | `GET /v1/models`；`data[].supports_image_in`/`supports_video_in`/`supports_reasoning` |
| https://platform.kimi.com/docs/api/messages | Anthropic Messages 字段、`cache_control`、`output_config`、SSE 事件序列 |
| https://platform.kimi.com/docs/api/models-overview | 各模型参数默认值与约束；`temperature`/`top_p`/`n`/penalty 固定值 |
| https://platform.kimi.com/docs/api/overview | 服务地址、三协议兼容性、鉴权头、端点一览 |
| https://platform.kimi.com/docs/api/responses | Responses `input`/`output` item 类型、`tools` 类型、`web_search`、SSE 事件 |
| https://platform.kimi.com/docs/api/signatures-verify | `POST /v1/signatures/verify`；`nonce`/`timestamp`/`model`/`signature`；`{"valid": ...}` |
| https://platform.kimi.com/docs/api/tools-fetch | `POST /v1/tools/fetch`；`url`；错误 `invalid_url`/`markdown_not_found`/`security_risk` |
| https://platform.kimi.com/docs/api/tools-search | `POST /v1/tools/search`；`text_query`/`limit`/`timeout_seconds`；响应头 `X-Msh-Track-Id` |
| https://platform.kimi.com/docs/api/tools-search-pro | `POST /v1/tools/search_pro`；`sites`/`time_window`/`limit`/`chunks` |
| https://platform.kimi.com/docs/get-api-key | 创建 API Key → 选模型 → 首次调用；`MOONSHOT_API_KEY`；`reasoning_effort` 三档 |
| https://platform.kimi.com/docs/guide/ai-readable-docs | `llms.txt`、`llms-full.txt`、`openapi.json`；任意页 URL 加 `.md` 取 Markdown |
| https://platform.kimi.com/docs/guide/ask-questions-about-pdf-content | 文件抽取 PDF 问答；`purpose="file-extract"`；`prompt_cache_options.ttl` |
| https://platform.kimi.com/docs/guide/auto-reconnect | 异常重试示例（最多 100 次、间隔 1s）；无专门重连字段；建议 `stream=True` |
| https://platform.kimi.com/docs/guide/benchmark-best-practice | 基准测试参数（`temperature=1.0`/`stream=true`/`top_p=0.95`）与逐 benchmark 表 |
| https://platform.kimi.com/docs/guide/best-practices-for-web-search | 联网搜索最佳实践：接口选择、查询词写法、参数配置 |
| https://platform.kimi.com/docs/guide/configure-the-modelscope-mcp-server | Playground 同步 ModelScope 托管 MCP 服务；控制台操作步骤，无字段参数 |
| https://platform.kimi.com/docs/guide/context-caching | 上下文缓存：`prompt_cache_key`、`5m`/`1h` TTL、用量字段与定价 |
| https://platform.kimi.com/docs/guide/engage-in-multi-turn-conversations-using-kimi-api | 多轮对话：`messages` 维护与历史截断 |
| https://platform.kimi.com/docs/guide/kimi-k2-6-quickstart | K2.6 快速开始：视觉示例、参数变动表、禁用思考示例 |
| https://platform.kimi.com/docs/guide/kimi-k2-7-code-quickstart | K2.7 Code 快速开始：多模态 Agent 示例、参数变动表、Tool Use 约束 |
| https://platform.kimi.com/docs/guide/kimi-k3-quickstart | K3 快速开始：基础调用、`reasoning_effort`、视觉、结构化输出、限制 |
| https://platform.kimi.com/docs/guide/kimi-k3-tool-calling-best-practice | K3 工具调用最佳实践：`search_tools` + 动态注入 + effort |
| https://platform.kimi.com/docs/guide/open-code | OpenCode 接入；Provider `Moonshot AI (China)`；`kimi-k3` 与推理强度档位 |
| https://platform.kimi.com/docs/guide/org-best-practice | 组织与项目：IP 白名单格式与上限、项目/API Key 个数、消费与限速、角色权限 |
| https://platform.kimi.com/docs/guide/prompt-best-practice | Prompt 最佳实践：角色、分隔符、few-shot、长对话总结；`messages`/`role` 示例 |
| https://platform.kimi.com/docs/guide/response_format | Structured Output：`json_schema`、`strict`、MFJS 校验、模型差异 |
| https://platform.kimi.com/docs/guide/tool-call-repeat | 重复工具调用问题排查：消息布局、system-reminder 提示 |
| https://platform.kimi.com/docs/guide/troubleshooting | 问题排查：账号、计费、认证、输出截断、限速、连接错误 |
| https://platform.kimi.com/docs/guide/use-batch-api | Batch API 指南：JSONL 字段（`custom_id`/`method`/`url`/`body`）、状态机、多模态 |
| https://platform.kimi.com/docs/guide/use-batch-inference | 控制台批量推理：任务名称/最长等待时间/数据文件；仅 Tier1 及以上可用 |
| https://platform.kimi.com/docs/guide/use-dynamic-tool-loading | 动态加载工具：带 `tools` 的 system 消息、Tool Search、缓存影响 |
| https://platform.kimi.com/docs/guide/use-json-mode-feature-of-kimi-api | JSON Mode：`response_format` 基础用法与注意事项 |
| https://platform.kimi.com/docs/guide/use-kimi-api-for-file-based-qa | 文件问答：上传→抽取→放入 system prompt、多文件、清理 |
| https://platform.kimi.com/docs/guide/use-kimi-api-to-complete-tool-calls | 工具调用完整流程：定义、执行、回传、流式片段拼接 |
| https://platform.kimi.com/docs/guide/use-kimi-in-hermes-agent | Hermes Agent 接入：`KIMI_CN_API_KEY`、`custom_providers`、视频 `video_url` 内容块 |
| https://platform.kimi.com/docs/guide/use-kimi-in-openclaw | OpenClaw 配置：`models.providers.moonshot.baseUrl`、`reasoning`/`thinkingLevelMap`/`compat` |
| https://platform.kimi.com/docs/guide/use-kimi-k3-to-setup-agent | 用 K3 + `web-search` Formula 搭研究 Agent；`/tools`、`/fibers`、`additionalProperties:false` |
| https://platform.kimi.com/docs/guide/use-kimi-vision-model | 视觉模型：`image_url`/`video_url`/`ms://`、格式白名单、分辨率与 Body 限制 |
| https://platform.kimi.com/docs/guide/use-moonpalace | MoonPalace（月宫）调试代理：`--port`/`--key`/`--force-stream`；请求导出 |
| https://platform.kimi.com/docs/guide/use-official-tools | 官方工具 Formula：URI、`GET /v1/formulas/{uri}/tools`、`POST .../fibers`、命名要求 |
| https://platform.kimi.com/docs/guide/use-partial-mode-feature-of-kimi-api | Partial Mode：`messages[].partial` 前缀、截断续写、`name` 固定角色 |
| https://platform.kimi.com/docs/guide/use-playground-to-debug-the-model | Playground 调试：提示信息、模型配置、工具（官方/MCP）、模型对比、导入导出 |
| https://platform.kimi.com/docs/guide/use-reasoning-effort | 推理强度：顶层 `reasoning_effort` 三档（`low`/`high`/`max`） |
| https://platform.kimi.com/docs/guide/use-thinking-models | 思考模型：`reasoning_content`、Preserved Thinking、多步工具调用回传 |
| https://platform.kimi.com/docs/guide/use-tool-choice | 工具调用约束：`tool_choice` 取值与限制 |
| https://platform.kimi.com/docs/guide/use-web-search | `$web_search` 内置工具（`builtin_function`）与迁移到独立 REST 接口 |
| https://platform.kimi.com/docs/guide/utilize-the-streaming-output-feature-of-kimi-api | 流式输出：SSE 结构、`data: [DONE]`、`n` 限制、Tokens 统计 |
| https://platform.kimi.com/docs/guide/zero-data-retention | 零数据保留（ZDR）说明；直接上传的图像/视频与托管智能体不在覆盖范围 |
| https://platform.kimi.com/docs/hosted-agents/agents | `POST /v1/agents`；`name`/`model`/`system`/`tools`/`mcp_servers`/`skills`/`plugins`/`multiagent`；`agent_toolset` |
| https://platform.kimi.com/docs/hosted-agents/dreams | 梦境策略：`PATCH /v1/memory-stores/{id}/dream-policy`；`activity`/`schedule`/`trigger_id` |
| https://platform.kimi.com/docs/hosted-agents/environments | `POST /v1/environments`；`config.type=cloud`；`networking`/`packages`/`setup_script`/`build_status` |
| https://platform.kimi.com/docs/hosted-agents/event-stream | `GET /v1/sessions/{session_id}/events/stream`；SSE `event`/`id`/`data`；`agent.delta`/`session.status` |
| https://platform.kimi.com/docs/hosted-agents/files | `POST /v1/files`；`FileMetadata`/`FileObject` 两套格式；`kimi-api-version` 语义差异 |
| https://platform.kimi.com/docs/hosted-agents/mcp | `mcp_servers[].type=url`/`name`/`url`；`mcp_toolset`/`mcp_server_name`；凭据匹配与降级 |
| https://platform.kimi.com/docs/hosted-agents/memory | `POST /v1/memory-stores`；`path`/`content`(base64)/`content_sha256`/`access`/`instructions` |
| https://platform.kimi.com/docs/hosted-agents/migration | 迁移映射（system/工具/沙箱/失败恢复）与三步链路、不需重建的能力 |
| https://platform.kimi.com/docs/hosted-agents/multiagent-orchestration | `multiagent.agents[]`；`spawn_subagent`/`send_message`/`wait_for_message`；线程事件 |
| https://platform.kimi.com/docs/hosted-agents/official-investment-research-agent | 官方金融投研智能体：1 路由 + 13 子智能体、`plugins`、ContextEnvelope 护栏 |
| https://platform.kimi.com/docs/hosted-agents/official-ppt-agent | 官方 PPT 智能体：`skills`/`plugins`/`system` 约定；`save_artifact`；`output/` |
| https://platform.kimi.com/docs/hosted-agents/permissions | `enabled`/`permission_policy`（`always_allow`）/`evaluated_permission`；工具集与单工具两级 |
| https://platform.kimi.com/docs/hosted-agents/plugins | `GET /v1/plugins`；插件=manifest+技能+MCP；版本固定与上/下架行为 |
| https://platform.kimi.com/docs/hosted-agents/quickstart | 快速开始：`kimi-api-version`/`Authorization`；`POST /v1/sessions`、SSE、产物下载 |
| https://platform.kimi.com/docs/hosted-agents/sandbox-reference | 沙箱规格：Debian 12、语言运行时与预装工具、`/mnt/agents/` 挂载路径 |
| https://platform.kimi.com/docs/hosted-agents/session-operations | 会话管理：`status`、`PATCH` 更新 `agent_overrides`、`user.interrupt`、归档与删除 |
| https://platform.kimi.com/docs/hosted-agents/sessions | `POST /v1/sessions`；`agent_id`/`environment_id`/`agent_overrides`/`resources`；`idle`→`running` |
| https://platform.kimi.com/docs/hosted-agents/skills | `POST /v1/skills`；`SKILL.md`；包 64 MiB/解压 128 MiB/256 文件；挂载只读 |
| https://platform.kimi.com/docs/hosted-agents/tools | 内置工具集 `default_toolset_20260901`；`read_file`/`shell`/`save_artifact`；截断与超时 |
| https://platform.kimi.com/docs/hosted-agents/triggers | `POST /v1/triggers`；`policy.type=cron`/`session_mode`/`notification`；Webhook 头与签名 |
| https://platform.kimi.com/docs/hosted-agents/vaults | `POST /v1/vaults`；`auth.type`（`mcp_oauth`/`static_bearer`/`environment_variable`）；只写秘密值 |
| https://platform.kimi.com/docs/introduction | 主要概念（**未抓到正文**，仅从 llms.txt 取得描述） |
| https://platform.kimi.com/docs/models | 模型列表（可用模型 + 已下线模型与迁移提示） |
| https://platform.kimi.com/playground | Playground 工作台入口页；该 URL 加 `.md` 返回应用 HTML，非文档正文，无协议字段 |

**未能抓取 / 未纳入**：`introduction` 未抓到正文（仅从 llms.txt 取得描述）；`playground` 返回 Next.js 应用 HTML、无文档正文。`llms.txt` 中的 `pricing/*`、`agreement/*`、`changelog/*`、客户端接入教程与 FAQ 等**周边页**按本次收录范围不收（判据见 `scripts/audit-provider-api-docs.py` 的 `is_peripheral()`），不在本清单的 180 条内。


### MiniMax — 全部页面 81 条

| 页面（规范 URL） | 这页讲了什么 |
|---|---|
| https://platform.minimax.cn/docs/api-reference/anthropic-api-compatible-cache | Anthropic 主动缓存：`cache_control` 用法、5 分钟生命周期、20 块回溯、最多 4 个断点、缓存价目表。 |
| https://platform.minimax.cn/docs/api-reference/api-overview | 接口概览：语言/视频/语音/图片/音乐/文件管理能力与模型清单。 |
| https://platform.minimax.cn/docs/api-reference/errorcode | 错误码查询表：内部码 → 含义 → 解决办法（`base_resp.status_code`）。 |
| https://platform.minimax.cn/docs/api-reference/file-management-delete | `POST /v1/files/delete`：必填 `file_id`、`purpose`（枚举与上传不同）。 |
| https://platform.minimax.cn/docs/api-reference/file-management-list | `GET /v1/files/list`：query `purpose`（必填），返回 `files[]` 与 `base_resp`。 |
| https://platform.minimax.cn/docs/api-reference/file-management-retrieve | `GET /v1/files/retrieve`：query `file_id`，返回文件元信息与 `download_url`（1 小时有效）。 |
| https://platform.minimax.cn/docs/api-reference/file-management-retrieve-content | `GET /v1/files/retrieve_content`：query `file_id`，直接返回文件二进制内容。 |
| https://platform.minimax.cn/docs/api-reference/file-management-upload | 文件上传 `POST /v1/files/upload`：`purpose` 五枚举、格式与大小限制、`mm_file://{file_id}` 引用。 |
| https://platform.minimax.cn/docs/api-reference/image-generation-i2i | 图生图：`POST /v1/image_generation` 的 `subject_reference`（`type=character`）与响应字段。 |
| https://platform.minimax.cn/docs/api-reference/image-generation-t2i | 文生图 `POST /v1/image_generation` 请求/响应字段（`aspect_ratio`、`width`/`height`、`n`）。 |
| https://platform.minimax.cn/docs/api-reference/lyrics-generation | 歌词生成 `POST /v1/lyrics_generation`：`mode`、`prompt`、`lyrics`、`title`。 |
| https://platform.minimax.cn/docs/api-reference/models/anthropic/list-models | `GET /anthropic/v1/models`：分页 `limit`/`after_id`/`before_id`，鉴权头 `X-Api-Key`。 |
| https://platform.minimax.cn/docs/api-reference/models/anthropic/retrieve-model | `GET /anthropic/v1/models/{model_id}`：返回 `id`、`created_at`、`display_name`、`type`。 |
| https://platform.minimax.cn/docs/api-reference/models/openai/list-models | `GET /v1/models`：`object` 固定 `list`、`data[]`（`id`/`object`/`created`/`owned_by`）。 |
| https://platform.minimax.cn/docs/api-reference/models/openai/retrieve-model | `GET /v1/models/{model_id}`：path `model_id` 必填，返回单个模型信息。 |
| https://platform.minimax.cn/docs/api-reference/music-cover-preprocess | 翻唱前处理 `POST /v1/music_cover_preprocess`：返回 `cover_feature_id`、`formatted_lyrics`、`structure_result`。 |
| https://platform.minimax.cn/docs/api-reference/music-generation | 音乐生成 `POST /v1/music_generation` 请求/响应字段（`lyrics`、`audio_setting`、`output_format`）。 |
| https://platform.minimax.cn/docs/api-reference/responses-create | OpenAI Responses 兼容 `POST /v1/responses`：`input` 条目、`reasoning.effort`、输出项类型。 |
| https://platform.minimax.cn/docs/api-reference/responses-input-tokens | `POST /v1/responses/input_tokens`：输入 token 估算，返回 `input_tokens`。 |
| https://platform.minimax.cn/docs/api-reference/speech-t2a-async-create | 异步语音合成 `POST /v1/t2a_async_v2`：`text`/`text_file_id`，返回 `task_id`、`file_id`。 |
| https://platform.minimax.cn/docs/api-reference/speech-t2a-async-query | `GET /v1/query/t2a_async_query_v2`：query `task_id`，返回 `status`（processing/success/failed/expired）。 |
| https://platform.minimax.cn/docs/api-reference/speech-t2a-http | 同步语音合成 `POST /v1/t2a_v2`：`voice_setting`/`audio_setting`/字幕字段与 hex 音频返回。 |
| https://platform.minimax.cn/docs/api-reference/speech-t2a-websocket | WebSocket 同步语音合成 `/ws/v1/t2a_v2`：上行 `task_start`/`task_continue`/`task_finish`，下行 hex 音频。 |
| https://platform.minimax.cn/docs/api-reference/speech-t2a-websocket-bidi | 双向流式语音合成 `/ws/v1/t2a_v2_bidi`：额外 `task_cancel`/`task_flush` 与 `sentence_start` 等下行事件。 |
| https://platform.minimax.cn/docs/api-reference/speech-to-text | 语音识别 `POST /v1/speech_to_text` 请求/响应字段、音频格式与时长大小限制。 |
| https://platform.minimax.cn/docs/api-reference/text-ai-sdk | 通过 AI SDK（`vercel-minimax-ai-provider`）接入：支持参数表；不支持图像与文件输入。 |
| https://platform.minimax.cn/docs/api-reference/text-anthropic-api | 通过 Anthropic SDK 接入：支持/忽略参数表、thinking 行为表、`count_tokens` 接口。 |
| https://platform.minimax.cn/docs/api-reference/text-chat-anthropic | Anthropic 兼容 Messages 完整 OpenAPI（thinking 块、SSE 事件、错误响应、MediaSource）。 |
| https://platform.minimax.cn/docs/api-reference/text-chat-openai | OpenAI 兼容 Chat Completions 完整 OpenAPI（请求/响应/流式 chunk/多模态内容块/敏感词字段）。 |
| https://platform.minimax.cn/docs/api-reference/text-openai-api | 通过 OpenAI SDK 接入：支持参数表、thinking 行为表、被忽略的 OpenAI 参数。 |
| https://platform.minimax.cn/docs/api-reference/text-post | MiniMax 原生 `POST /v1/text/chatcompletion_v2` 完整 OpenAPI（`tool_choice`、`response_format`）。 |
| https://platform.minimax.cn/docs/api-reference/text-prompt-caching | Prompt 被动缓存：适用条件（512+ 输入 token）、前缀顺序、usage 字段、计费示例。 |
| https://platform.minimax.cn/docs/api-reference/video-agent-create | 视频 Agent `POST /v1/video_template_generation`：`template_id`、`text_inputs`、`media_inputs`。 |
| https://platform.minimax.cn/docs/api-reference/video-agent-query | `GET /v1/query/video_template_generation`：query `task_id`，成功返回 `video_url`（9 小时有效）。 |
| https://platform.minimax.cn/docs/api-reference/video-generation-download | 视频产物下载：`GET /v1/files/retrieve`（`file_id`）取 `download_url`（有效期 1 小时）。 |
| https://platform.minimax.cn/docs/api-reference/video-generation-fl2v | 首尾帧生成 `POST /v1/video_generation`：仅 `MiniMax-Hailuo-02`，必填 `last_frame_image`。 |
| https://platform.minimax.cn/docs/api-reference/video-generation-i2v | 图生视频 `POST /v1/video_generation`：`first_frame_image` 格式/体积限制与 `resolution` 枚举。 |
| https://platform.minimax.cn/docs/api-reference/video-generation-query | `GET /v1/query/video_generation`：query `task_id`，`status` ∈ Preparing/Queueing/Processing/Success/Fail。 |
| https://platform.minimax.cn/docs/api-reference/video-generation-s2v | 主体参考生成：仅 `S2V-01`，`subject_reference`（`type=character`）单主体。 |
| https://platform.minimax.cn/docs/api-reference/video-generation-t2v | 文生视频 `POST /v1/video_generation`：`prompt` ≤2000 字符、`[指令]` 运镜、`duration`/`resolution`。 |
| https://platform.minimax.cn/docs/api-reference/video-generation-v2-create | 视频生成 V2 `POST /v2/video_generation`：`model`/`content[]`/`duration` 与各模态素材限制。 |
| https://platform.minimax.cn/docs/api-reference/video-generation-v2-delete | `DELETE /v2/video_generation/{task_id}`：按状态取消或删除，错误体为 OpenAI 风格 `OaiError`。 |
| https://platform.minimax.cn/docs/api-reference/video-generation-v2-h3-context-ir | `POST /v2/h3_context_ir`：仅 `MiniMax-H3`，只返回增强后的视频提示词、不建任务。 |
| https://platform.minimax.cn/docs/api-reference/video-generation-v2-list | `GET /v2/query/video_generation` 分页列表：`page_num`/`page_size`/`filter.status` 等。 |
| https://platform.minimax.cn/docs/api-reference/video-generation-v2-query | `GET /v2/query/video_generation/{task_id}`：`status`（queued/running/succeeded/failed/cancelled），取 `content.url`。 |
| https://platform.minimax.cn/docs/api-reference/video-generation-v2-regeneration | `POST /v2/video_regeneration`：768P 视频再生成 2K（`source_task_id` 或 `base_video` 二选一）。 |
| https://platform.minimax.cn/docs/api-reference/voice-cloning-clone | 音色快速复刻 `POST /v1/voice_clone`：`file_id`/`voice_id`、`clone_prompt`、`accuracy` 等字段。 |
| https://platform.minimax.cn/docs/api-reference/voice-cloning-uploadcloneaudio | 上传待克隆音频 `POST /v1/files/upload`（`purpose=voice_clone`）：mp3/m4a/wav、10 秒–5 分钟。 |
| https://platform.minimax.cn/docs/api-reference/voice-cloning-uploadprompt | 上传示例音频 `POST /v1/files/upload`（`purpose=prompt_audio`）：mp3/m4a/wav、<8 秒、≤20MB。 |
| https://platform.minimax.cn/docs/api-reference/voice-design-design | 音色设计 `POST /v1/voice_design`：`prompt`/`preview_text`，返回 `voice_id`、`trial_audio`。 |
| https://platform.minimax.cn/docs/api-reference/voice-management-delete | `POST /v1/delete_voice`：`voice_type`（voice_cloning/voice_generation）、`voice_id`。 |
| https://platform.minimax.cn/docs/api-reference/voice-management-get | `POST /v1/get_voice`：`voice_type`（system/voice_cloning/voice_generation/all），返回各音色列表。 |
| https://platform.minimax.cn/docs/essentials/code | Mintlify 模板页：行内代码与围栏代码块、语言标注与文件名标注写法。 |
| https://platform.minimax.cn/docs/essentials/images | Mintlify 模板页：Markdown 图片、HTML `img`、iframe 嵌入；配图须小于 5MB。 |
| https://platform.minimax.cn/docs/essentials/markdown | Mintlify 模板页：标准 Markdown 语法（标题/粗斜体/删除线/上下标/链接/引用/LaTeX）。 |
| https://platform.minimax.cn/docs/essentials/navigation | Mintlify 模板页：`docs.json` 的 `navigation.tabs[].groups[].pages[]` 导航写法与规则。 |
| https://platform.minimax.cn/docs/essentials/reusable-snippets | Mintlify 模板页：`snippets/` 可复用片段（默认导出导入、变量、组件 + props）。 |
| https://platform.minimax.cn/docs/essentials/settings | Mintlify 模板页：`docs.json` 全局配置字段全表（`api.baseUrl`、`api.auth.*` 等）。 |
| https://platform.minimax.cn/docs/guides/image-generation | 图片生成指南：`POST /v1/image_generation` 文生图/图生图参数与限制（详见主节 §8.2）。 |
| https://platform.minimax.cn/docs/guides/local-deploy | 本地/自托管部署总览：MiniMax-M3、M2.7、Music 3、H3 的路径、状态与责任划分。 |
| https://platform.minimax.cn/docs/guides/local-deploy-h3 | ComfyUI 与 SGLang 部署 MiniMax H3：权重 revision、分辨率/帧网格要求与许可范围。 |
| https://platform.minimax.cn/docs/guides/local-deploy-m2-7 | SGLang 部署 MiniMax-M2.7：参考镜像、启动参数、TP/EP 拓扑与验证方式。 |
| https://platform.minimax.cn/docs/guides/local-deploy-m3 | SGLang 部署 MiniMax-M3（Experimental）：权重 revision、镜像 digest、启动参数与硬件矩阵。 |
| https://platform.minimax.cn/docs/guides/local-deploy-music-3 | SGLang-Omni 部署 MiniMax Music 3：`/v1/audio/speech` 参数、`max_new_tokens` 与限制。 |
| https://platform.minimax.cn/docs/guides/models-intro | 全模态模型概览（语言/视频/语音/图片/音乐模型名单）。 |
| https://platform.minimax.cn/docs/guides/music-generation | 音乐生成指南：`music-3.0` 调用、翻唱一步/两步流程、付费接口服务调整通知。 |
| https://platform.minimax.cn/docs/guides/playground | 调试台 Playground：控制台模块菜单与「选模块 → 配参数 → 看结果」流程。 |
| https://platform.minimax.cn/docs/guides/quickstart-preparation | 前置准备：注册、API Key/订阅 Key 获取、base_url 与环境变量、充值。 |
| https://platform.minimax.cn/docs/guides/quickstart-sdk | 通过 SDK 接入：推荐 Anthropic SDK，示例 `client.messages.create` 与 thinking/text 块。 |
| https://platform.minimax.cn/docs/guides/rate-limits | 速率限制：语言/视频/语音/图片/音乐的 RPM、TPM、CONN 表与说明。 |
| https://platform.minimax.cn/docs/guides/server-tools | 服务端工具 `web_search`：Anthropic/Responses 两套声明格式、响应内容块、0.03 元/次。 |
| https://platform.minimax.cn/docs/guides/speech-t2a-async | 异步语音合成指南：上传文本 → 创建任务 → 查询 → 下载四步流程与限制。 |
| https://platform.minimax.cn/docs/guides/speech-t2a-websocket | 同步语音合成指南：HTTP 与 WebSocket 接入地址表、40 种语言、流式播放示例。 |
| https://platform.minimax.cn/docs/guides/speech-to-text | 语音识别指南：`response_format` 四种形态、说话人分离、语种与流式限制。 |
| https://platform.minimax.cn/docs/guides/speech-voice-clone | 音色快速复刻指南：四步流程与上传规范（10 秒–5 分钟、≤20MB）。 |
| https://platform.minimax.cn/docs/guides/text-chat | M2-her 对话模型：高级角色表（`user_system`/`group`/示例消息）与核心参数表。 |
| https://platform.minimax.cn/docs/guides/text-generation | 模型调用指南：URL 配置、两套调用样例、effort 三协议对照表、思考不可关闭的报错文案。 |
| https://platform.minimax.cn/docs/guides/text-m3-function-call | 工具使用 & 交错思维链：tools 定义、`reasoning_split` 与 `<think>` 两种格式、必须回带历史。 |
| https://platform.minimax.cn/docs/guides/video-agent | 使用模板生成视频指南：`video_template_generation` 异步流程与成功时的 `video_url`。 |
| https://platform.minimax.cn/docs/guides/video-generation | 视频生成指南：输入组合、素材规格与混合输入总上限 12 个文件。 |
| https://platform.minimax.cn/docs/guides/video-prompt | H3 亮点功能示例：按能力与场景给出提示词、参考素材与输出示例（无接口参数）。 |

未能抓取 / 未纳入：

- 原文 §13 另记 `guides/quickstart-sdk` 未抓取（内容与 `text-anthropic-api` 重叠），但本家附录分片已按「通过 SDK 接入」页收录该页内容，两处说法不一致。
- 全站索引 `llms.txt` 与 `sitemap.xml` 为本次抓取的目录来源，未列入 urlsB。


### MiMo — 全部页面 20 条

| 页面（规范 URL） | 这页讲了什么 |
|---|---|
| https://mimo-v2.com/zh/docs | 文档首页 /「欢迎使用」：只有侧边栏导航，正文区为空（本家 §13 原文如此） |
| https://mimo-v2.com/zh/docs/api/chat/anthropic-api | Anthropic 兼容 API：`POST /v1/messages`、鉴权 `api-key`、8 个请求参数、消息对象、非流式响应字段、6 种类型化流式事件 |
| https://mimo-v2.com/zh/docs/api/chat/openai-api | OpenAI 兼容 API：`POST /v1/chat/completions`、鉴权（`api-key`/`Authorization: Bearer`）、11 个请求参数、消息字段、SSE chunk 与 `data: [DONE]` |
| https://mimo-v2.com/zh/docs/quick-start/error-codes | 错误码：7 条业务码（`invalid_request`/`authentication_failed`/`rate_limit_exceeded` 等）+ 错误响应体 + 指数退避示例 + `Retry-After` |
| https://mimo-v2.com/zh/docs/quick-start/first-api-call | 首次调用 API：两种协议 Base URL、`MIMO_API_KEY`、四条调用示例、「多轮工具调用与思考模式」的 `reasoning_content` 回传建议 |
| https://mimo-v2.com/zh/docs/quick-start/model-parameters | 模型超参：MiMo-V2-Pro/Omni/Flash 推荐参数表与 `temperature`/`top_p`/`max_completion_tokens` 等七个参数的默认值、范围 |
| https://mimo-v2.com/zh/docs/usage-guide/multimodal/audio | 语音理解：`input_audio` 只支持 base64、`wav`/`mp3`/`flac`/`ogg` 四种格式、时长影响 Token |
| https://mimo-v2.com/zh/docs/usage-guide/multimodal/image | 图片理解：`image_url` 支持 URL 与 base64 两种传法、`jpeg`/`png`/`gif`/`webp` 四种格式、分辨率影响 Token |
| https://mimo-v2.com/zh/docs/usage-guide/multimodal/video | 视频理解：`video_url` 两种传法、Token 用量取决于时长/分辨率/帧率、长视频降本建议 |
| https://mimo-v2.com/zh/docs/usage-guide/tool-calling/web-search | 联网搜索：内置 `web_search` 函数的声明方式（`parameters.properties.query` 必填）、$5/1000 次定价、搜索结果自动注入上下文 |
| https://mimo-v2.com/zh/docs/usage-guide/tts | 语音合成：`POST /v1/chat/completions`、文本放 `assistant` 消息、`audio.format`/`voice`（默认 `mimo_default`）、`/v1/audio/speech` 兼容、限时免费 |
| https://mimo-v2.com/zh/models | 模型索引页：8 张模型卡片的上下文 1M/256K/8K、输出窗口 128K/8K、能力标签（如 Text generation/Deep reasoning/Streaming）与文档入口（无参数名、无价格） |
| https://mimo-v2.com/zh/models/mimo-v2-omni | MiMo-V2-Omni 落地页：上下文 256K、输出窗口 128K、能力标签 Multimodal understanding/Deep reasoning/Function calling/Web search（无参数名） |
| https://mimo-v2.com/zh/models/mimo-v2-pro | MiMo-V2-Pro 长文落地页（唯一 SEO 长文页）：上下文 1,048,576、输出窗口 128K、输入 $0.435/M、输出 $0.87/M、缓存命中 $0.0036/M（标注第三方来源）、发布 2026-03-18 |
| https://mimo-v2.com/zh/models/mimo-v2-tts | MiMo-V2-TTS 落地页：上下文 8K、输出窗口 8K、能力标签 Text-to-speech/Audio output/Voice experiences（音色清单与参数名未给出） |
| https://mimo-v2.com/zh/models/mimo-v2.5 | MiMo-V2.5 落地页：上下文 1M、输出窗口 128K、全模态理解（文本/图像/视频/音频）、能力标签含 Structured output/Web search（无参数名；首页写输入 $0.14/M、输出 $0.28/M） |
| https://mimo-v2.com/zh/models/mimo-v2.5-pro | MiMo-V2.5-Pro 落地页：上下文 1M、输出 128K、能力标签 Deep reasoning/Function calling/Structured output（首页写输入 $0.435/M、输出 $0.87/M） |
| https://mimo-v2.com/zh/models/mimo-v2.5-tts | MiMo-V2.5-TTS 落地页：上下文 8K、输出 8K、能力标签 Text-to-speech/Audio output/Singing/Style control（音色 id 与 `voice`/`audio_tags` 未给出） |
| https://mimo-v2.com/zh/models/mimo-v2.5-tts-voiceclone | MiMo-V2.5-TTS-VoiceClone 落地页：上下文 8K、输出窗口 8K、能力标签 Text-to-speech/Voice cloning/Audio output（参考音频参数名与格式要求未给出） |
| https://mimo-v2.com/zh/models/mimo-v2.5-tts-voicedesign | MiMo-V2.5-TTS-VoiceDesign 落地页：上下文 8K、输出窗口 8K、能力标签 Text-to-speech/Voice design/Audio output（描述文本参数名未给出） |

未抓到 / 未纳入：
- 上表 20 条全部抓到，无失败。模型类 9 条（`/models` 索引页 + 8 个模型落地页）取自 HTML 正文：给这批页加 `.mdx` 后缀只返回空壳 HTML（正文 4 行空白占位），拿不到原始 Markdown。
- 模型清单（`missing-mimo.txt`）11 行全部抓到，其中 2 行是同一个首页的根域名；没有按「营销页，未抓」跳过的行。
- 模型落地页本身不给 RPM/TPM 与缓存价格：RPM 100 / TPM 10M 与各档价格只在主节 §12（定价页）给出。


### 千问AI平台 — 全部页面 73 条

| 页面（规范 URL） | 这页讲了什么 |
|---|---|
| https://platform.qianwenai.com/docs/api-reference/chat/anthropic | Anthropic 兼容 Messages：POST /apps/anthropic/v1/messages；system 顶层、thinking/output_config、SSE 事件序列、usage 四字段 |
| https://platform.qianwenai.com/docs/api-reference/chat/dashscope | DashScope 原生：/api/v1/services/aigc/text-generation/generation 与 multimodal-generation/generation；input/parameters |
| https://platform.qianwenai.com/docs/api-reference/chat/delete-response | DELETE /compatible-mode/v1/responses/{response_id}；返回 {"id": ..., "deleted": true} |
| https://platform.qianwenai.com/docs/api-reference/chat/list-input-items | GET /compatible-mode/v1/responses/{response_id}/input_items；data[]（含 role/content/type/status）、first_id/last_id/has_more |
| https://platform.qianwenai.com/docs/api-reference/chat/openai-chat | Chat 字段总表：model/messages 必填、stream_options、modalities、thinking_budget、enable_search、messages 内容块与 DashScope 原生字段 |
| https://platform.qianwenai.com/docs/api-reference/chat/openai-responses | Responses 字段：input 9 类 item、store 默认 true、reasoning.effort 7 档、max_output_tokens、ocr_options、tools 8 类内置工具 |
| https://platform.qianwenai.com/docs/api-reference/chat/retrieve-response | GET /compatible-mode/v1/responses/{response_id}；返回同一响应对象（id 有效期 7 天，可作 previous_response_id） |
| https://platform.qianwenai.com/docs/api-reference/more/async-task-management | 任务查询 GET /api/v1/tasks/{task_id}、取消 POST /api/v1/tasks/{task_id}/cancel；QPS 20/账号，结果保留 24 小时 |
| https://platform.qianwenai.com/docs/api-reference/more/connection-pooling | 连接复用端点；文档未给出字段细节 |
| https://platform.qianwenai.com/docs/api-reference/more/generate-a-temporary-api-key | 生成临时 API Key 端点；文档未给出字段细节 |
| https://platform.qianwenai.com/docs/api-reference/more/manage-asynchronous-tasks | 异步任务管理端点（Task API）；文档未给出字段细节 |
| https://platform.qianwenai.com/docs/api-reference/more/upload-file-get-temporary-url | 上传文件获取临时 URL 端点；文档未给出字段细节 |
| https://platform.qianwenai.com/docs/developer-guides/accuracy-tuning/explicit-cache-best-practice | 显式缓存最佳实践：cache_control{"type":"ephemeral"}，向前检索最多 20 个 content 块、单请求最多 4 个标记、最小 1024 Token |
| https://platform.qianwenai.com/docs/developer-guides/accuracy-tuning/image-generation | 图像生成调优；生成类页面，本次未收录正文 |
| https://platform.qianwenai.com/docs/developer-guides/accuracy-tuning/overview | 准确性调优总览；文档未给出字段细节 |
| https://platform.qianwenai.com/docs/developer-guides/accuracy-tuning/speech-recognition | 语音识别调优；语音类页面，本次未收录正文 |
| https://platform.qianwenai.com/docs/developer-guides/accuracy-tuning/text-generation | 文本生成准确性调优；文档未给出字段细节 |
| https://platform.qianwenai.com/docs/developer-guides/accuracy-tuning/video-generation | 视频生成调优；生成类页面，本次未收录正文 |
| https://platform.qianwenai.com/docs/developer-guides/getting-started/embedding-models | 向量模型清单页；文档未给出字段细节 |
| https://platform.qianwenai.com/docs/developer-guides/getting-started/first-api-call | 首次 API 调用：/compatible-mode/v1/chat/completions、Bearer 鉴权；文档未给出更多字段细节 |
| https://platform.qianwenai.com/docs/developer-guides/getting-started/image-models | 图像模型清单；生成类页面，本次未收录正文 |
| https://platform.qianwenai.com/docs/developer-guides/getting-started/introduction | 平台简介；文档未给出字段细节 |
| https://platform.qianwenai.com/docs/developer-guides/getting-started/latest-model | 最新模型清单；文档未给出字段细节 |
| https://platform.qianwenai.com/docs/developer-guides/getting-started/model-selection | 模型选择指南；文档未给出字段细节 |
| https://platform.qianwenai.com/docs/developer-guides/getting-started/text-generation-models | 文本模型清单：推荐模型表与全部模型（上下文/最大输出/思考预算/函数调用/内置工具/结构化输出/批量，含 qwen3.8-max 1M） |
| https://platform.qianwenai.com/docs/developer-guides/getting-started/video-models | 视频模型清单；生成类页面，本次未收录正文 |
| https://platform.qianwenai.com/docs/developer-guides/getting-started/vision-models | 视觉理解模型表：上下文/单图最大像素/视频时长与大小/图片与视频数量上限，以及 OCR 系列模型清单 |
| https://platform.qianwenai.com/docs/developer-guides/getting-started/world-model/happyoyster-guide | 世界模型 happyoyster 指南；生成类页面，本次未收录正文 |
| https://platform.qianwenai.com/docs/developer-guides/run-and-scale/async-task-management | 异步任务：GET /api/v1/tasks/{task_id}、cancel；QPS 20/账号，结果保留 24 小时 |
| https://platform.qianwenai.com/docs/developer-guides/run-and-scale/context-cache | 上下文缓存：隐式（命中价 20%、最小 256）、显式（创建 125%/命中 10%、最小 1024）、会话（x-dashscope-session-cache） |
| https://platform.qianwenai.com/docs/developer-guides/run-and-scale/latency-optimization | 时延优化；文档未给出字段细节 |
| https://platform.qianwenai.com/docs/developer-guides/run-and-scale/multi-turn | 多轮对话：messages 历史拼接、previous_response_id（有效期 7 天） |
| https://platform.qianwenai.com/docs/developer-guides/run-and-scale/prime-mode | Prime 模式：model 指定即生效、TPS 1.5~2 倍、软限流；glm-5.3-prime、glm-5.2-fast-preview、wan3.0-video-prime |
| https://platform.qianwenai.com/docs/developer-guides/run-and-scale/safety | 安全与合规说明；文档未给出字段细节 |
| https://platform.qianwenai.com/docs/developer-guides/run-and-scale/streaming | 流式输出：stream=true、choices[0].delta.content、stream_options.include_usage、思考两阶段（reasoning_content 先于 content） |
| https://platform.qianwenai.com/docs/developer-guides/text-generation/batch | Batch 文件输入：files.create/batches.create、completion_window 24h~336h、5 折、10,000 文件/100 GB、上下文 256K |
| https://platform.qianwenai.com/docs/developer-guides/text-generation/batch-chat | Batch Chat：POST batch.dashscope.aliyuncs.com/compatible-mode/v1/chat/completions；最长 3,600 秒、10,000 等待请求、50% 计费 |
| https://platform.qianwenai.com/docs/developer-guides/text-generation/deep-research | 深入研究发现：qwen-deep-research（-2025-12-15），上下文 1,000,000/最大输入 997,952/最大输出 32,768，仅流式 |
| https://platform.qianwenai.com/docs/developer-guides/text-generation/dialogue-analysis | 对话分析 Tongyi-Xiaomi-Analysis：tongyi-xiaomi-analysis-flash 与 tongyi-xiaomi-analysis-pro |
| https://platform.qianwenai.com/docs/developer-guides/text-generation/document-understanding | 数据挖掘 Qwen-Doc：qwen-doc-turbo，支持文件 URL/文件 ID/纯文本传入 |
| https://platform.qianwenai.com/docs/developer-guides/text-generation/gui-interaction | GUI-Plus 界面交互：gui-plus 与 gui-plus-2026-02-26（推荐），需配合官方 System Prompt |
| https://platform.qianwenai.com/docs/developer-guides/text-generation/intent-detect | 意图理解 Tongyi-Intent-Detect：tongyi-intent-detect-v3，三种用法（意图+函数调用/仅意图/仅函数调用） |
| https://platform.qianwenai.com/docs/developer-guides/text-generation/partial-mode | 前缀续写 Partial Mode：assistant 消息 + partial:true、支持模型白名单、不支持思考模式与 DashScope Java SDK |
| https://platform.qianwenai.com/docs/developer-guides/text-generation/quickstart | 文本生成快速开始：/compatible-mode/v1/chat/completions 与 DashScope 端点、DASHSCOPE_API_KEY；文档未给出更多字段细节 |
| https://platform.qianwenai.com/docs/developer-guides/text-generation/qwen-long | Qwen-Long 长上下文：qwen-long（-latest/-2025-01-25）上下文 10M，文件 ID 或纯文本传入，支持结构化输出 |
| https://platform.qianwenai.com/docs/developer-guides/text-generation/qwen-mt | 机器翻译 Qwen-MT：qwen-mt-plus/turbo/flash/lite 上下文 16k；术语干预、翻译记忆、领域提示、自定义提示词 |
| https://platform.qianwenai.com/docs/developer-guides/text-generation/role-playing | 角色扮演 Qwen-Character：qwen-plus-character（-ja）、qwen-flash-character；长期记忆与会话缓存（32,768） |
| https://platform.qianwenai.com/docs/developer-guides/text-generation/structured-output | 结构化输出：JSON Object（提示词须含 JSON）与 JSON Schema（Qwen3.7-Plus/Flash/Max、Qwen3.8-Max/Flash） |
| https://platform.qianwenai.com/docs/developer-guides/text-generation/thinking | 思考模式：enable_thinking/thinking_budget/reasoning_effort、preserve_thinking 白名单、混合与纯推理、/think 与 /no_think |
| https://platform.qianwenai.com/docs/developer-guides/third-party-models/deepseek | 三方模型 DeepSeek 接入（阿里云直供）；文档未给出字段细节 |
| https://platform.qianwenai.com/docs/developer-guides/third-party-models/deepseek-kuaishou | 三方模型 DeepSeek 接入（快手万擎直供）；文档未给出字段细节 |
| https://platform.qianwenai.com/docs/developer-guides/third-party-models/deepseek-siliconflow | 三方模型 DeepSeek 接入（硅基流动直供）；文档未给出字段细节 |
| https://platform.qianwenai.com/docs/developer-guides/third-party-models/glm | 三方模型 GLM 接入（阿里云直供）；文档未给出字段细节 |
| https://platform.qianwenai.com/docs/developer-guides/third-party-models/glm-zhipu | 三方模型 GLM 接入（智谱直供，如 ZHIPU/GLM-5.3）；文档未给出字段细节 |
| https://platform.qianwenai.com/docs/developer-guides/third-party-models/kimi | 三方模型 Kimi 接入（阿里云直供）；文档未给出字段细节 |
| https://platform.qianwenai.com/docs/developer-guides/third-party-models/kimi-moonshot | 三方模型 Kimi 接入（月之暗面直供，如 kimi/kimi-k3）；文档未给出字段细节 |
| https://platform.qianwenai.com/docs/developer-guides/third-party-models/mimo | 三方模型 MiMo 接入（小米直供，如 MiMo-V2.5-Pro）；文档未给出字段细节 |
| https://platform.qianwenai.com/docs/developer-guides/third-party-models/minimax | 三方模型 MiniMax 接入（阿里云直供）；文档未给出字段细节 |
| https://platform.qianwenai.com/docs/developer-guides/third-party-models/minimax-minimaxi | 三方模型 MiniMax 接入（稀宇科技直供）；文档未给出字段细节 |
| https://platform.qianwenai.com/docs/developer-guides/third-party-models/stepfun | 三方模型 StepFun 接入（如 stepfun/step-5-preview）；文档未给出字段细节 |
| https://platform.qianwenai.com/docs/developer-guides/third-party-models/unisound | 三方模型 Unisound 接入（语音）；文档未给出字段细节 |
| https://platform.qianwenai.com/docs/developer-guides/third-party-models/vidu-prompt-guide | 三方模型 Vidu 提示词指南；生成类页面，本次未收录正文 |
| https://platform.qianwenai.com/docs/developer-guides/tool-calling/code-interpreter | 代码解释器内置工具：enable_code_interpreter（非 OpenAI 标准参数，需 extra_body）；获取执行代码、使用限制与计费 |
| https://platform.qianwenai.com/docs/developer-guides/tool-calling/function-calling | 函数调用：tools/tool_choice/parallel_tool_calls、name ≤64 Token、tool_stream、工具结果回传，建议工具不超过 20 个 |
| https://platform.qianwenai.com/docs/developer-guides/tool-calling/image-search | 图片搜索内置工具：web_search_image（文搜图）与 image_search（图搜图）、响应格式与计费 |
| https://platform.qianwenai.com/docs/developer-guides/tool-calling/mcp | MCP 工具：server_protocol 仅支持 sse（必填）、server_label/server_url 必填、server_description 与 headers 可选 |
| https://platform.qianwenai.com/docs/developer-guides/tool-calling/pdf-understanding | PDF 理解：input_file 支持 PDF（≤100 MB）与图片（≤20 MB），目前仅 qwen3.5-ocr |
| https://platform.qianwenai.com/docs/developer-guides/tool-calling/web-scraping | 网页抓取 web_extractor：响应结构、流式 Web extractor 事件、使用限制；必须与 web_search 一起启用 |
| https://platform.qianwenai.com/docs/developer-guides/tool-calling/web-search | 联网搜索：enable_search + search_options（forced_search、search_strategy turbo/max/agent/agent_max、enable_search_extension） |
| https://platform.qianwenai.com/docs/openapi-anthropic.json | Anthropic 兼容 OpenAPI 规格：/apps/anthropic/v1/messages，required model/max_tokens/messages，thinking/output_config |
| https://platform.qianwenai.com/docs/openapi-dashscope.json | DashScope 原生 OpenAPI 规格：/api/v1/services/aigc/text-generation 与 multimodal-generation，input+parameters 两对象 |
| https://platform.qianwenai.com/docs/openapi-openai-chat.json | Chat Completions OpenAPI 规格：POST /compatible-mode/v1/chat/completions，required model/messages |
| https://platform.qianwenai.com/docs/openapi-openai-responses.json | Responses OpenAPI 规格：POST /compatible-mode/v1/responses，required model/input；27 个流式事件 |

**未能抓取 / 未纳入**

- 本家主节另引用、但不在本清单的页面路径：/api-reference/preparation/error-messages、/developer-guides/administration/rate-limits、/developer-guides/administration/dynamic-rate-limits、/developer-guides/multimodal/vision、/developer-guides/multimodal/ocr。


