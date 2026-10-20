# Ally 本地 HTTP API 文档（v1）

Ally 提供一组本地 HTTP API，供 Ally 插件、脚本、IDE 与自动化平台调用 AI、会话、模型、MCP 与 Skill 等能力。这是插件调用业务能力的统一契约，不另提供 AI / 会话函数 SDK；插件接线方式见 [插件开发指南 §4.5](plugin-system.md#45-调用-ally-的-ai会话等-http-api)。

无窗口部署、启动参数及同端口 SSE / 轮询事件接口见 [无头运行](headless.md)。

桌面模式服务首次默认关闭，由 **设置 → API** 页开启；端口、Token 与开关保存到 `~/.ally_agent/api.json`，重启 Ally 自动恢复已启用的服务。默认端口 `47821`，仅监听 `127.0.0.1`。端口冲突等启动失败会记录日志，设置页显示实际未运行状态；正常退出不会清除启用偏好。

## 基础信息

| 项 | 说明 |
|----|------|
| Base URL | `http://127.0.0.1:<端口>`（默认 `http://127.0.0.1:47821`） |
| 请求格式 | 需要请求体的接口传 JSON（`Content-Type: application/json`）；无参数接口空请求体即可 |
| 响应格式 | 统一 JSON 信封：成功 `{"ok":true,"data":{...}}`，失败 `{"ok":false,"error":"错误说明"}` |
| 请求体上限 | 4 MB |

HTTP 状态码语义：

| 状态码 | 含义 |
|--------|------|
| 200 | 成功 |
| 400 | 参数缺失/非法，或模型配置不完整（如未配置模型、API Key） |
| 401 | 鉴权失败（token 缺失或错误） |
| 404 | 资源不存在（会话/skill/服务不存在） |
| 409 | 冲突（会话运行 / 压缩中，或注入队列满；运行中发送附件 / 切模型也返回 409） |
| 500 | 服务端内部错误 |
| 503 | 服务正在关闭或尚未初始化 |
| 502 / 504 | 单次 AI 调用失败 / 超过 120 秒 |

## 认证

所有接口都要求携带 Bearer Token 请求头：

```
Authorization: Bearer <token>
```

Token 在 **Ally 设置 → API** 页查看和复制；留空保存会自动生成 32 位随机值，点「重新生成」会立即换新（旧 token 失效）。

curl 示例：

```bash
curl -H "Authorization: Bearer <token>" http://127.0.0.1:47821/api/v1/health
```

PowerShell 示例：

```powershell
Invoke-RestMethod -Uri "http://127.0.0.1:47821/api/v1/health" -Headers @{ Authorization = "Bearer <token>" }
```

## 典型调用流程

外部工具最常用的闭环：创建会话 → 发消息 → 轮询状态 → 取结果。

```bash
TOKEN="Authorization: Bearer <token>"
BASE="http://127.0.0.1:47821/api/v1"

# 1. 创建会话（也可以直接对已有会话发消息，跳过此步）
SID=$(curl -s -H "$TOKEN" -H "Content-Type: application/json" \
  -d '{"title":"重构任务"}' "$BASE/sessions" | jq -r .data.id)

# 2. 发消息（空闲 → 立即开始新回合，返回 runId；运行中 → 自动排队追加）
curl -s -H "$TOKEN" -H "Content-Type: application/json" \
  -d '{"message":"把 utils.js 重构成 TypeScript"}' \
  "$BASE/sessions/$SID/messages"

# 3. 轮询状态（running=true 表示还在执行）
curl -s -H "$TOKEN" "$BASE/sessions/$SID"

# 4. 会话结束后取任务结果（最近一条完成的 assistant 消息）
curl -s -H "$TOKEN" "$BASE/sessions/$SID/result"
```

---

## 会话

### `GET /api/v1/sessions` — 查询会话列表

Query 参数（均可选）：

| 参数 | 类型 | 说明 |
|------|------|------|
| `workspace` | string | 按工作区路径精确过滤 |

响应 `data`：

| 字段 | 说明 |
|------|------|
| `sessions[]` | 会话条目数组 |
| `sessions[].id` | 会话 ID（后续所有会话接口用它） |
| `sessions[].title` / `firstPrompt` | 标题 / 首条提问 |
| `sessions[].workspace` | 工作区路径 |
| `sessions[].createdAt` / `updatedAt` | 毫秒时间戳 |
| `sessions[].messageCount` / `contextTokens` | 消息条数 / 上下文 token 估算 |
| `sessions[].hasSnapshot` | 是否已有完整快照 |
| `sessions[].running` | 是否有活跃 run |
| `count` | 条数 |

```bash
curl -H "Authorization: Bearer <token>" "http://127.0.0.1:47821/api/v1/sessions?workspace=F:/work/demo"
```

### `POST /api/v1/sessions` — 创建新会话

请求体（全部字段可选，可传空对象 `{}`）：

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `title` | string | 否 | 缺省用工作区目录名 |
| `workspace` | string | 否 | 缺省用当前应用配置的工作区 |

响应 `data`：`{ "id": "...", "title": "...", "workspace": "...", "createdAt": 1756500000000 }`

### `GET /api/v1/sessions/{id}` — 查询会话状态

路径参数：`id` = 会话 ID。

响应 `data`：

| 字段 | 说明 |
|------|------|
| `running` | 是否有活跃 run（AI 正在执行） |
| `runId` / `compacting` | 当前 run ID（无运行时空字符串）/ 是否正在压缩 |
| `lastRunStatus` / `lastRunError` | 本进程记录的最近运行状态与错误；重启后尚无记录时为空 |
| `queuedMessages` | 排队等待注入的用户消息数 |
| `model` / `modelProvider` | 下一回合将使用的模型（本地 HTTP API 会话用的是"最近使用模型"，即 `POST /api/v1/models/activate` 设的那一条） |
| `id` / `title` / `workspace` / `createdAt` / `updatedAt` / `messageCount` / `contextTokens` | 会话元信息 |

会话不存在返回 404。

### `GET /api/v1/sessions/{id}/result` — 查询任务结果

响应 `data`：

| 字段 | 说明 |
|------|------|
| `status` | `running` / `compacting` / `done` / `error` / `cancelled`；结束状态在本进程记录，重启后无记录回落 `done`（仅表示当前空闲） |
| `runId` / `error` | 当前或最近一次 run ID / 最近运行错误（无记录时为空） |
| `result` | 后端历史中最近一条有正文、且不带工具调用的 assistant 消息；没有回答时为空。运行中可能是上一轮或当前已保存的中间结果；只有结合 `status` 才能判断是否完成 |
| `updatedAt` | 快照更新时间（毫秒） |

### `GET /api/v1/sessions/{id}/messages` — 获取完整消息快照

返回 `data.{id,title,workspace,createdAt,updatedAt,contextTokens,messages}`。有后端模型历史时优先返回该历史，避免 API 执行后仍读到旧 UI 快照。`messages[]` 包含 `role`、`content`、`done`；assistant 工具调用为 `toolCalls: [{id,name,arguments}]`（arguments 是 JSON 字符串），tool 结果带 `toolCallId`。不含系统提示词或供应商思考签名。重启后工具输出使用落盘占位内容，图片不恢复；没有模型历史时回落 UI 快照（可能带界面展示字段）。这不是实时 token 流，只返回已保存的检查点；只要回答用 `/result`。

### `GET /api/v1/sessions/{id}/todos` — 查询会话计划

响应 `data.todos`：`[{ "title": "步骤一", "status": "in_progress" }]`，`status` 取值 `pending` / `in_progress` / `done`。

计划为当前进程内状态，重启后未恢复运行时可能为空；会话不存在返回 404。

### `GET /api/v1/sessions/{id}/questions` — 查询待回答的问题

响应 `data.questions` 为 `[{askId, request: {questions: [{id, question, options: [{id,label,description,recommended}]}]}}]`。没有等待中的问题时返回空数组。Agent 使用 ask 工具等待用户时，插件应展示这些问题并提交答案；单纯追加消息不会解除等待。

### `POST /api/v1/sessions/{id}/answers` — 回答问题

```json
{"askId":"...","answers":[{"questionId":"q1","selectedOptionIds":["o1"],"customText":"补充说明"}]}
```

每个问题都必须有一项答案；可选选项 ID、自由文本或两者。响应 `data.submitted=true`。不存在或不属于该会话的问题返回 404，答案非法 / 重复提交返回 400。

### `POST /api/v1/sessions/{id}/messages` — 给会话发送消息

请求体：

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `message` | string | 是* | 消息文本；运行中的会话只接受纯文本 |
| `attachments` | array | 否 | 仅空闲时接收；每项 `{name,type?,size?,kind?,dataUrl?,text?,filePath?}`，kind 为 image/text/file，图片使用 data URL |
| `modelIndex` | int | 否 | 本回合使用 `GET /models` 的指定条目；缺省用最近使用模型，不更改全局模型选择；运行中携带此字段返回 409 |

*带 `attachments` 时 `message` 可为空；否则必填。

自动判断逻辑（与界面一致）：

- 会话空闲 → 直接开始新回合（首次发送与继续是同一条持久化历史路径），返回 `{"runId":"...","queued":false}`
- 会话运行中 → 排队追加到当前 run，下一步边界注入模型上下文，返回 `{"runId":"...","queued":true}`

新回合的工作区与附加根目录取自该会话，不随用户当前打开的工作区变化。运行中带附件会返回 409，避免静默丢失；压缩中不能启动新回合。会话必须先创建，不存在返回 404。

错误：400 配置不完整（未配置模型/API Key/工作区）或消息为空；409 运行中排队失败（队列满）。

### `POST /api/v1/sessions/{id}/cancel` — 终止会话当前运行

等同界面按 ESC，支持取消运行或手动压缩。无请求参数。响应 `data.wasRunning` 表示是否发出取消，取消压缩另带 `compacting=true`。取消是异步的，继续轮询至空闲；空闲时幂等 no-op，不存在返回 404。

### `POST /api/v1/sessions/{id}/compact` — 压缩会话历史

请求体（可选）：

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `instruction` | string | 否 | 压缩时给 LLM 的总结要求 |

**同步接口**：等待 LLM 完成历史总结才返回，断开 HTTP 连接会取消。使用会话本进程冻结的模型配置，没有记录时用最近使用模型；原样保留思考档位。不存在返回 404，运行 / 压缩中返回 409，条件不满足返回 400。可能超过插件 HTTP 代理的 120 秒上限，调用方应处理超时。

### `DELETE /api/v1/sessions/{id}` — 删除会话

删除持久化历史与快照。会话运行或压缩中返回 409；对不存在的会话重复调用幂等成功。

---

## 单次 AI 调用

### `POST /api/v1/ai/complete` — 生成文本

请求 `{ "prompt": "待处理文本", "system": "可选系统指令", "modelIndex": 0 }`。`prompt` 必填且不能全为空白，prompt / system 各限 64 KiB；modelIndex 可选，缺省使用最近使用模型，下标非法返回 400。

复用 Ally 已配置模型的三协议适配、密钥池、代理、自定义请求头和思考档位；不创建会话、不注入工作区提示词、不运行工具、不改全局模型选择。同步等待完成，最长 120 秒，客户端断连或 Ally 退出会取消该请求，互不影响其它调用或游戏面板。

响应 `data`：`{ "reply": "生成文本", "promptTokens": 12, "completionTokens": 3, "totalTokens": 15 }`。用量来自供应商回报，未回报时为 0。配置不完整返回 400，模型请求失败返回 502，超时返回 504。该接口不是 OpenAI 兼容转发端点；需要 Agent / 多轮历史时使用会话接口。

## 模型

### `GET /api/v1/models` — 查询模型列表与最近使用模型

响应 `data`：

| 字段 | 说明 |
|------|------|
| `active` | 最近使用模型：`{providerName, apiFormat, baseUrl, model, reasoningTag, reasoningEffort, customHeaderNames}`，即本地 HTTP API 会话（不带 Tab 模型 overlay）下一回合使用的模型；界面上每个 Tab 各自有自己的模型，与它无关。一条模型都没配置时各字段为空串 |
| `models[]` | 已配置的模型条目（下标即 `index`，供更新/激活用） |
| `models[].hasApiKey` / `apiKeyCount` | 密钥配置状态（**响应永不回传密钥明文**） |
| `models[].customHeaderNames` | 该条目自定义请求头的键名列表（**只回传键名，永不回传值**，值可能携带网关凭据） |

### `POST /api/v1/models` — 新建或更新模型配置

请求体：

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `index` | int | 否 | 提供时按下标**整体更新**该条目；缺省为**追加**新条目 |
| `model` | object | 是 | 模型配置，字段见下 |

`model` 对象字段（对应 `ModelConfig`）：

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `model` | string | 是 | 模型 ID，如 `gpt-4o`、`claude-sonnet-4` |
| `providerName` | string | 否 | 供应商标签 |
| `apiFormat` | string | 否 | `openai_chat` / `openai_responses` / `anthropic_messages`（兼容别名 `chat`、`responses`、`anthropic`，自动归一化） |
| `baseUrl` | string | 否 | 缺省按 apiFormat 取默认值 |
| `apiKey` | string | 否 | 密钥；多 key 用 `apiKeys` 数组（首个优先） |
| `apiKeys` | string[] | 否 | 密钥池 |
| `maxTokens` / `contextWindow` | int | 否 | 输出上限 / 上下文窗口 |
| `reasoningTag` / `reasoningEffort` / `tokenParam` | string | 否 | 推理内容标签 / 思考强度（`auto`/`off`/`low`/`medium`/`high`/`xhigh`/`max`；`off` 表示关闭思考，`auto` 交给供应商决定）/ token 参数风格 |
| `customHeaders` | object | 否 | 自定义请求头（`{"X-Api-Version": "2023-06-01"}`），随该模型每个 API 请求发送；可覆盖 Authorization/User-Agent，Host/Content-Length 等传输层头部会被丢弃，最多 32 条 |

注意：更新是**整体替换**该下标的条目——没传的字段会被清空，改单条时请把原条目字段一并传回。响应 `data`：`{ "index": 2 }`。

### `POST /api/v1/models/activate` — 设置最近使用模型

请求体：`{ "index": 2 }`（`models[]` 的下标）。它记下的是模型的**身份**（providerName + model id）：config.json 里只存 `models[]` 与这个身份，模型字段在每次生效时按身份展开；界面切换模型/发送时写回的是同一个身份（两边谁后写谁生效）。本地 HTTP API 会话没有 Tab 上下文，下一回合就用它；界面上每个 Tab 的模型不受影响。越界返回 400。

---

## MCP

### `GET /api/v1/mcp` — 查询 MCP 列表与配置

响应 `data`：

| 字段 | 说明 |
|------|------|
| `servers[]` | 各服务器状态：`name`、`status`（connected/connecting/failed/disabled）、`transport`、`toolCount`、`error`、`tools[]`（每项 `{name, description, disabled}`，`description` 截断到 200 字符，`disabled` 表示该工具在注入黑名单中） |
| `config` | `mcp.json` 文件原文（字符串），修改后可直接通过 `PUT /mcp/config` 传回 |

### `PUT /api/v1/mcp/config` — 更新并应用 MCP 配置

请求体：

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `config` | string | 是 | `mcp.json` 完整原文（JSON 字符串，顶层结构 `{"mcpServers": {...}}`） |

保存后自动执行增量 reconcile：只重连新增/删除/变更的服务器，未动的保持连接。响应 `data`：`{ "applied": true }`；JSON 非法返回 400。

---

## Skill

### `GET /api/v1/skills` — 查询 skill 列表

响应 `data.skills[]`：`{ name, description, source, type, whenToUse, enabled }`，`source` 取值 `project` / `user` / `builtin`。

### `GET /api/v1/skills/{name}` — 读取 skill 内容

路径参数：`name`（大小写不敏感）。响应 `data.content` 为完整 SKILL.md 正文。不存在返回 404。

### `POST /api/v1/skills/{name}/enable`、`POST /api/v1/skills/{name}/disable` — 启停 skill

无请求参数。可启停项目、用户及内置 skill，影响后续技能注入；已冻结的会话前缀不会在中途改写。响应 `data`：`{ "name": "...", "enabled": true/false }`。

---

## 工具 / 子代理 / 工作区

### `GET /api/v1/tools` — 查询工具清单

响应 `data.tools[]`：`{ name, description, source, server?, enabled }`，`source` 为 `built-in` 或 `mcp`（MCP 条目带 `server` 名，`name` 即模型侧函数名 `mcp__<server>__<tool>`）；`enabled=false` 表示该 MCP 工具在所在服务器的 `disabledTools` 注入黑名单中，不进入模型请求但清单内仍可见。

### `GET /api/v1/subagents` — 查询子代理运行状态

响应 `data.subagents[]`：`{ id, sessionId, role, status(running/completed/failed), steps, summary, filesRead, filesEdited, error, startTime, totalTokens, toolCalls[] }`。

### `GET /api/v1/workspace` — 查询工作区配置

响应 `data`：`{ "workspace": "F:/work/demo", "extraRoots": [...] }`（脱敏，不含任何密钥）。

---

## 后台服务 / 计划任务

### `GET /api/v1/services` — 查询后台服务

响应 `data.services[]`：`{ id, name, command, cwd, pid, status, startedAt, stoppedAt, exitCode, outputTail, outputBytes, error }`。

### `GET /api/v1/services/{id}/output` — 获取服务输出

响应 `data`：`{ id, output, bytes, truncated }`（bounded 输出缓冲）。不存在返回 404。

### `POST /api/v1/services/{id}/stop` — 停止后台服务

请求体（可选）：`{ "graceSeconds": 3 }`——优雅终止等待时间，缺省 3 秒、上限 30 秒，超时后强制杀死整个进程树。不存在返回 404。

### `GET /api/v1/tasks` — 查询计划任务

响应 `data.tasks[]`：`{ id, name, instruction, workspace, schedule, permissionMode, maxSteps, timeoutSeconds, nextRunAt, lastRunAt, lastStatus, lastSummary, lastError, running, ... }`。计划任务持久化在配置目录，重新启动 Ally 后继续运行。

### `DELETE /api/v1/tasks/{id}` — 删除计划任务

无请求参数。响应 `data`：`{ "deleted": true }`。

### ChatGPT OAuth 模型

模型管理中的 `chatgpt_oauth` 条目可参与现有模型索引选择和 Agent 调用，使用 Ally 桌面端已经授权的账号。普通配置只包含账号注册引用，不包含 OAuth Token；本地 API 不提供登录、刷新或读取 Token 的端点，原有本地服务鉴权保持不变。OAuth 失败不会使用条目里的 API Key 或其他模型支付请求。账号登录、退出和刷新模型目录均在桌面设置完成，见 [ChatGPT Auth 对接实现](chatgpt-auth.md)。
