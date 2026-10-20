# Ally 无头运行

无头模式使用 Wails v3 的 `server` 构建，共用桌面端的 Agent、工具、模型适配、
会话、MCP 和调度实现。无头产物不创建窗口，不构建或嵌入前端，也不启动桌面通知。

## 构建

三平台均可在对应 Go 构建环境中执行：

```bash
wails3 task build:server
```

产物在 `bin/Ally-server`（Windows 为 `.exe`）。也可直接构建：

```bash
CGO_ENABLED=0 go build -tags server,production -trimpath -o bin/Ally-server .
```

交叉构建示例：

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -tags server,production -o bin/Ally-server-linux-amd64 .
CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -tags server,production -o bin/Ally-server-darwin-arm64 .
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -tags server,production -o bin/Ally-server.exe .
```

## 启动参数

```bash
./Ally-server --host 127.0.0.1 --port 47821   --data-dir /srv/ally/data --workspace /srv/projects/demo   --token-file /srv/ally/token
```

Windows 示例（参数含空格时加引号）：

```text
Ally-server.exe --port 47821 --data-dir "D:\AllyData" --workspace "D:\projects\demo" --token-file "D:\AllyData\token"
```

也可传 `--config server.json`：

```json
{
  "host": "127.0.0.1",
  "port": 47821,
  "dataDir": "/srv/ally/data",
  "workspace": "/srv/projects/demo",
  "tokenFile": "/srv/ally/token"
}
```

- 监听参数优先级：命令行 > 指定的 JSON 文件 > `WAILS_SERVER_HOST` /
  `WAILS_SERVER_PORT` 环境变量 > 默认值（127.0.0.1:47821）。
- `--workspace` 可省略：保留已有配置的工作区；创建会话时可以指定各自的服务器工作区。
- 数据目录默认 `~/.ally_agent`，也可用 `ALLY_DATA_DIR`；`--data-dir` 优先。
- 认证可用 `--token` / JSON 的 `token`，或 `--token-file` / `tokenFile`，
  两者不同时指定。文件内容是 Token 本身，不是 JSON。
- 不提供认证参数时使用数据目录 `api.json` 的 Token；首次自动生成并保存，
  控制台只打印文件路径，不打印密钥。
- 无头服务始终启用；不依赖、不改写桌面 API 的 `enabled` 开关。
  CLI 的端口和 Token 覆盖不会写回桌面配置。
- 路径相对进程工作目录解析。固定端口绑定失败时退出，不自动换端口。
- 一个数据目录只能由一个 Ally 实例占用（桌面和无头共用锁）。
  多实例使用不同数据目录、端口。
- 远程部署可显式监听 `0.0.0.0`；跨不可信网络时用 HTTPS 反向代理或 SSH 隧道。
  所有业务和事件接口要求 `Authorization: Bearer <token>`。

无头入口不提供完整网页界面或 App 的桌面 bindings。Wails 仍注册框架路由
（如 `/health`、`/wails/custom.js`、`/wails/stream/ws`），这些路由不受业务 Token
中间件覆盖；未注册 Agent 业务 bindings，也不向框架事件通道发送业务事件。
Wails 自带的 `/health` 是不含业务数据的基础存活检查；业务健康接口是鉴权的
`/api/v1/health`。

## 下任务、查看过程和结果

完整业务接口见 [API 文档](api.md)。下面使用 Bash：

```bash
BASE=http://127.0.0.1:47821/api/v1
TOKEN='<token>'

# 1. 创建会话；工作区是服务端路径。
curl -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json'   -d '{"title":"构建检查","workspace":"/srv/projects/demo"}' "$BASE/sessions"

# 2. 将返回的 data.id 填入 SESSION，再发送任务。接口返回 data.runId。
SESSION='<sessionId>'
curl -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json'   -d '{"message":"检查项目构建并修复错误"}' "$BASE/sessions/$SESSION/messages"

# 3. SSE 实时过程；也可以在发送任务前就建立订阅。
curl -N -H "Authorization: Bearer $TOKEN" "$BASE/events"

# 4. 查询运行状态与结果，以及完整消息快照。
curl -H "Authorization: Bearer $TOKEN" "$BASE/sessions/$SESSION/result"
curl -H "Authorization: Bearer $TOKEN" "$BASE/sessions/$SESSION/messages"

# 5. 取消，或查询待回答问题（包括人工审批）。
curl -X POST -H "Authorization: Bearer $TOKEN" "$BASE/sessions/$SESSION/cancel"
curl -H "Authorization: Bearer $TOKEN" "$BASE/sessions/$SESSION/questions"
```

首次使用需先通过 API 配置模型并选择最近使用模型，或在发送消息时指定 `modelIndex`。
MCP 配置通过 `PUT /api/v1/mcp/config` 注册并应用，业务扩展无需重新编译 Ally。
未选择默认工作区时，MCP 仍会加载用户配置；stdio 子进程的默认工作目录是独立的空临时目录，退出时清理。
这不是文件系统沙箱：MCP 进程仍拥有服务账户的文件权限。
MCP 配置、服务凭证和事件流是实例共享的，不提供多租户隔离。

不同会话可同时执行；同一会话只能有一个运行，运行中发纯文本会排队追加。
同工作区的并行会话共享文件，需要文件隔离时使用不同目录或 Git worktree。

## 事件协议与断线恢复

`GET /api/v1/events` 和 `GET /api/v1/events/poll` 与业务 API 同端口、同 Token。
沿用现有事件名，例如 `run:start`、`run:stream`、`tool:result`、
`tool:error`、`run:done`、`run:error`。按 payload 的 `sessionId` /
`runId` 在客户端分流；这是全实例事件流。

SSE 消息示例：

```text
id: <instance>:12
event: run:start
data: {"name":"run:start","payload":{"sessionId":"...","runId":"..."},"seq":12,"instance":"...","ts":"..."}
```

- SSE 重连传 `Last-Event-ID: <instance>:<最后收到的seq>`，从下一条续传。
- 也可使用 `?since=<下次seq>&instance=<instance>`。`since` 是包含式游标。
- 轮询返回 `{events: [], next, instance, resyncRequired}`，下一次传返回的
  `next` 和 `instance`，没有事件时为数组 `[]`。
- 只保留最近 200 条事件，每条 payload 上限 64 KiB，不作为永久审计日志。
  大 payload 可能是 `{truncated:true,size:...}`，完整内容通过会话快照等接口读取。
- 历史已被覆盖、服务实例发生变化或游标超前时，轮询的 `resyncRequired` 为 true，
  SSE 发送 `stream:reset`。客户端应重新拉取会话状态、消息、计划和待回答问题。
- 慢客户端的订阅队列溢出时发送 `stream:reset` 并结束连接，不能静默当成完整过程。
- 事件流有 15 秒心跳；断线不取消任务。浏览器原生 EventSource 不能直接设置
  Bearer header，可用支持流式读取的 fetch 客户端。
- 该协议也兼容已有的独立网络事件服务 `/events`、`/poll`；桌面环境变量
  `ALLY_NETWORK_EVENTS` 行为保留，无头不另外开启该监听器。

## 退出与恢复

Linux/macOS 的 Ctrl+C / SIGTERM、Windows 的 Ctrl+C / Ctrl+Break 会关闭事件流、停止接收工作，取消聊天、压缩和定时任务，
等待聊天检查点及会话索引落盘，然后关闭后台服务、MCP 并刷新统计。
定时任务退出会等待实际执行 worker，不只停止 cron 触发器。
等待超时会记录错误，无头程序以非零状态退出。

正常关闭后可在同一端口、同一数据目录重新启动并读取历史。
中断的任务不会自动重新执行；需要业务确认后继续发送消息。
强制终止进程（kill -9、任务管理器强杀）不能保证执行有序关闭。


Windows 服务管理器应在强制终止前调用带鉴权的关闭接口，并等待进程退出：

```bash
curl -X POST -H "Authorization: Bearer <token>" http://127.0.0.1:47821/api/v1/shutdown
```

接口返回 `{"ok":true,"data":{"shuttingDown":true}}` 后进入有序关闭。
Windows 不提供通用的 SIGTERM 投递通道，不能把发送 SIGTERM 当作保存保证。
