# Ally 本地 HTTP API 文档（v1）

Ally 提供一组本地 HTTP API，供其它工具（脚本、IDE 插件、自动化平台等）驱动 Ally 的会话、模型、MCP 与 Skill 能力。服务由 **设置 → API** 页手动开启，默认端口 `47821`，仅监听回环地址 `127.0.0.1`，不对局域网暴露。

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
| 409 | 冲突（会话已有活跃 run、运行中注入空消息等） |
| 500 | 服务端内部错误 |

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
| `queuedMessages` | 排队等待注入的用户消息数 |
| `model` / `modelProvider` | 下一回合将使用的模型（即全局激活模型） |
| `id` / `title` / `workspace` / `createdAt` / `updatedAt` / `messageCount` / `contextTokens` | 会话元信息 |

会话不存在返回 404。

### `GET /api/v1/sessions/{id}/result` — 查询任务结果

响应 `data`：

| 字段 | 说明 |
|------|------|
| `status` | `running`（还在执行）或 `done` |
| `result` | 最近一条完成的 assistant 消息全文；会话还没产出过回答时为空字符串 |
| `updatedAt` | 快照更新时间（毫秒） |

### `GET /api/v1/sessions/{id}/messages` — 获取完整消息快照

返回会话全部 UI 消息（与界面渲染一致，含工具卡等展示字段；`role` 为 `user`/`assistant`/`tool` 等，字段为动态 map）。适合需要全量上下文的集成场景；只要最终回答用 `/result` 即可。

### `GET /api/v1/sessions/{id}/todos` — 查询会话计划

响应 `data.todos`：`[{ "title": "步骤一", "status": "in_progress" }]`，`status` 取值 `pending` / `in_progress` / `done`。

### `POST /api/v1/sessions/{id}/messages` — 给会话发送消息

请求体：

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `message` | string | 是* | 消息文本；运行中的会话只接受纯文本 |
| `attachments` | array | 否 | 附件透传给模型（与界面附件同构），仅空闲会话生效 |

*带 `attachments` 时 `message` 可为空；否则必填。

自动判断逻辑（与界面一致）：

- 会话空闲 → 直接开始新回合（首次发送与继续是同一条持久化历史路径），返回 `{"runId":"...","queued":false}`
- 会话运行中 → 排队追加到当前 run，下一步边界注入模型上下文，返回 `{"runId":"...","queued":true}`

错误：400 配置不完整（未配置模型/API Key/工作区）或消息为空；409 运行中排队失败（队列满）。

### `POST /api/v1/sessions/{id}/cancel` — 终止会话当前运行

等同界面按 ESC。无请求参数。响应 `data.wasRunning` 表示取消时是否确有运行中的 run（空闲时调用是幂等 no-op）。

### `POST /api/v1/sessions/{id}/compact` — 压缩会话历史

请求体（可选）：

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `instruction` | string | 否 | 压缩时给 LLM 的总结要求 |

**同步接口**：会等待 LLM 完成历史总结才返回（可能数十秒），响应 `data` 为压缩结果详情。400 表示会话不存在或压缩条件不满足。

### `DELETE /api/v1/sessions/{id}` — 删除会话

删除持久化历史与快照。会话运行中返回 409；对不存在的会话重复调用幂等成功。

---

## 模型

### `GET /api/v1/models` — 查询模型列表与激活模型

响应 `data`：

| 字段 | 说明 |
|------|------|
| `active` | 当前激活模型：`{providerName, apiFormat, baseUrl, model, reasoningTag, reasoningEffort}`，即所有会话下一回合使用的模型 |
| `models[]` | 已配置的模型条目（下标即 `index`，供更新/激活用） |
| `models[].hasApiKey` / `apiKeyCount` | 密钥配置状态（**响应永不回传密钥明文**） |

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
| `reasoningTag` / `reasoningEffort` / `tokenParam` | string | 否 | 推理内容标签 / 思考强度（`low`/`medium`/`high`/`xhigh`/`max`）/ token 参数风格 |

注意：更新是**整体替换**该下标的条目——没传的字段会被清空，改单条时请把原条目字段一并传回。响应 `data`：`{ "index": 2 }`。

### `POST /api/v1/models/activate` — 切换激活模型

请求体：`{ "index": 2 }`（`models[]` 的下标）。切换的是全局激活模型（Ally 的会话在后端不持有独立模型状态，所有会话的下一回合都使用它）。越界返回 400。

---

## MCP

### `GET /api/v1/mcp` — 查询 MCP 列表与配置

响应 `data`：

| 字段 | 说明 |
|------|------|
| `servers[]` | 各服务器状态：`name`、`status`（connected/connecting/failed/disabled）、`transport`、`toolCount`、`error` |
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

无请求参数。内置 skill 永远启用，disable 内置 skill 无实际效果。响应 `data`：`{ "name": "...", "enabled": true/false }`。

---

## 工具 / 子代理 / 工作区

### `GET /api/v1/tools` — 查询工具清单

响应 `data.tools[]`：`{ name, description, source, server? }`，`source` 为 `built-in` 或 `mcp`（MCP 条目带 `server` 名，`name` 即模型侧函数名 `mcp__<server>__<tool>`）。

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

### `GET /api/v1/tasks` — 查询临时计划任务

响应 `data.tasks[]`：`{ id, name, instruction, workspace, schedule, permissionMode, maxSteps, timeoutSeconds, nextRunAt, lastRunAt, lastStatus, lastSummary, lastError, running, ... }`。计划任务仅存活于当前 Ally 进程。

### `DELETE /api/v1/tasks/{id}` — 删除计划任务

无请求参数。响应 `data`：`{ "deleted": true }`。
