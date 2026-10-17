# ChatGPT 官方登录

Ally 支持 OpenAI Sign in with ChatGPT 的开源应用动态注册流程。无需 API Key、Codex CLI、Codex SDK 或 app-server；推理直接使用官方 `POST https://api.openai.com/v1/responses`，继续使用 Ally 的会话、工具、取消、流式输出和上下文压缩。

## 使用

1. 打开设置 → 通用，点击「使用 ChatGPT 登录」（英文为官方文案 Continue with ChatGPT）。
2. 在系统浏览器选择 ChatGPT 账号和工作区，完成授权。授权完成后 Ally 会尝试显示并聚焦主窗口。
3. 打开模型管理 → 添加模型 → 认证方式「ChatGPT 登录」，选择已授权的账号注册和模型，保存。模型下拉显示服务器的 `display_name`，实际请求使用 `slug`。
4. 在工作区的模型菜单选择该条目，照常发送消息与使用工具。
5. 重启后保留登录；临近 Access Token 到期时，后端自动刷新。刷新失败时按提示检查网络或重新登录。

模型编辑器也可以直接登录。账号控件提供账号切换、重新登录、启用套餐调用、退出、刷新模型目录和 ChatGPT 额度管理链接。相同邮箱可能对应不同工作区注册，列表用正式 client ID 区分。新建不同账号的模型时默认使用独立 Provider 名称，避免既有「Provider + 模型」身份规则把两条配置合并。自定义名称时也应保持该身份唯一。

模型目录按当前账号实时请求 `GET /v1/models`，仅展示 `visibility == "list"`，保持服务端顺序。切换账号会清空旧目录并重新加载。模型和套餐权限以 OpenAI 当前返回为准；Ally 不内置订阅模型名单，也不推断额度重置时间。

有效身份登录不等于获准调用套餐。如果缺少 `chatgpt.tokens.use.direct` 或 `resource.invoke`，账号显示为未授权，不能推理；可明确点击「启用套餐调用」重新请求完整 scope 和 consent。

## 认证和安全存储

- 首次使用 `dynamic_agent_client` 和 `agent_name_hint=Ally`。回调取得正式 client ID；代码交换、后续授权、刷新与撤销均使用该正式 ID。
- 每次生成独立的 state、OIDC nonce 和 PKCE S256。回调只监听 `127.0.0.1` 的随机可用端口，路径固定 `/auth/callback`，校验 Host、state 和返回的 client ID；5 分钟超时，成功、取消或失败后关闭。
- OpenID discovery 从官方 issuer 获取；使用发布的 JWKS 验证 ID Token 的 RS256 签名、issuer、audience、expiration 和 nonce。重新授权和刷新时验证账号身份，验证失败不覆盖原账号。
- 一次安装保存稳定的 `urn:uuid:<UUIDv4>` host ID。注册、身份、Token、scope 和过期时间保存在 `~/.ally_agent/chatgpt/credentials.enc` 中，配置文件仅保存 `authType=chatgpt_oauth`、`oauthAccountId` 和模型信息。
- Windows 使用当前用户 DPAPI；macOS 使用 Keychain 保存 AES-GCM 主密钥；Linux 使用 Secret Service 保存 AES-GCM 主密钥。安全存储不可用时直接报错，不退化到明文。Linux 需要可用且已解锁的桌面 Secret Service；无此服务的环境仍可使用原 API Key 模式。
- 写入使用同目录临时文件、文件同步和原子替换；Unix 权限为 `0600`，目录为 `0700`。OS 文件锁覆盖「读取最新凭据 → 刷新轮换 → 整组写入」，避免多个 Ally 进程竞争同一 Refresh Token。
- Token 仅在后端内存和加密凭据库内使用。Wails 只返回账号状态；不写浏览器存储、普通配置或日志。认证、刷新、模型请求共用当前代理配置；认证和带凭据请求拒绝自动 HTTP 重定向。
- 本地 HTTP API 没有新增 OAuth 登录或 Token 读取端点，原鉴权不变。已经获准调用本地 API 的调用方可使用配置中的模型条目，因此也可能消耗其所选 ChatGPT 账号额度。

## 请求和恢复

OAuth 独立于 API Key 池；订阅失败不会轮换到另一个账号或付费 API Key。请求强制锁定官方 Responses 端点，忽略 OAuth 条目的自定义地址、Key 池和认证头覆盖。

每轮发送完整 `input`，固定 `store:false`、`stream:true`，系统提示使用 `instructions` 或 developer 消息。兼容层过滤 Preview 不支持的参数，包括 `max_output_tokens`、采样参数、持久会话引用和 retention。函数工具统一放在 `ally` namespace 内，由现有 Ally Runtime 执行并回填结果；加密 reasoning 回放按账号隔离。不发送图像生成、托管 MCP、tool_search 等不受支持的工具。

只有收到 `response.completed` 才算正常完成；失败、incomplete 和中断沿用现有错误/停止原因处理。HTTP 错误保留状态码、响应体形状、错误码和 request ID，并移除可能回显的 bearer。流内套餐错误也保留错误码。`subscription_sharing_usage_limit_exceeded` 会停止重试并在当前进程暂停该账号的新推理；检查 ChatGPT 额度设置后，明确刷新模型目录或重新登录可再尝试。

网络/基础设施失败保留凭据并有界重试；终态 Refresh Token 错误清空不可用 Token，但保留注册身份以便重新登录。退出会先停止本进程中该账号的请求，按 discovery 的 revocation endpoint 撤销 Refresh Token（网络/5xx 有界退避），随后清理 Access/Refresh/ID Token。远端撤销未确认会明确提示，可在 ChatGPT 设置断开应用；账号与 host/client 映射保留。其他账号不受影响。

## 验证与待人工验收

自动测试均使用 Mock HTTP Server、临时目录和测试加密器，不访问真实账号或系统凭据库。

- Go：注册/重新授权、账号身份隔离、错误 state、取消/超时、JWT 签名及声明校验、scope 缺失、模型筛选与加载错误、跨 Manager 并发刷新及轮换、刷新失效、撤销成功/失败、清理 Token 和中断 SSE、网络错误、重定向防泄漏、两轮函数调用回填、参数过滤和 API Key 隔离。
- 前端：账号认证字段在 Tab 快照和模型导入/导出中的保留、OAuth 参数清洗、友好模型名称和旧 API Key 兼容；中英键集合继续由现有测试检查。
- 修改后的 Go/Wails 绑定使用 `wails3 build` 验证。

2026-10-17 本机验证结果：

| 验证 | 实际结果 |
|---|---|
| `go test ./...` | 全部通过 |
| OAuth 专项 `go test -race`（`internal/chatgpt` 与 `internal/app`） | 通过，无竞态报告 |
| `cd frontend && npm test` | 279 项全部通过 |
| `go test ./internal/app -run '^TestLayerBoundaries$' -count=1` | 通过 |
| `gofmt -l .`、`git diff --check` | 无输出、无格式问题 |
| `wails3 build` | Windows amd64 构建成功，生成 `bin/Ally.exe`；Vite 有 chunk 体积提示 |
| `CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go test -c ... ./internal/chatgpt` | 交叉编译通过，未在 Linux 执行 |
| `CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go test -c ... ./internal/chatgpt` | 交叉编译通过，未在 macOS 执行 |

全套 Go 首轮曾在既有 `TestSaveSkillProjectTargetNeedsWorkspace` 失败：公共辅助函数已有临时工作区，和该用例的缺工作区前提冲突。仅在该用例显式清空工作区后，全套通过；未修改技能生产逻辑或公共辅助函数。

仍需人工使用真实获准账号完成三平台浏览器授权、系统安全存储解锁、真实模型流式工具任务、重启恢复、到期刷新及 ChatGPT 侧撤销。Linux/macOS OAuth 包交叉编译不能代替其桌面原生构建与运行验收。Preview 是否开放、可用模型和套餐额度由 OpenAI 决定。

## 修改文件索引

| 范围 | 文件 |
|---|---|
| OAuth 与加密凭据 | `internal/chatgpt/manager.go`、`store.go`、`protect_windows.go`、`protect_other.go`、`manager_test.go` |
| 桥接与订阅兼容层 | `internal/app/biz_chatgpt.go`、`host_chatgpt.go`、`prov_chatgpt.go`、`prov_chatgpt_test.go` |
| 配置、运行与 Provider | `internal/app/app.go`、`biz_config.go`、`prov_model.go`、`prov_adapter_responses.go`、`prov_reasoning.go`、`biz_compact.go`、`biz_game_ai.go`、`biz_api.go`、`host_desktop.go` |
| 设置与模型 UI | `frontend/src/components/ChatGPTAccounts.vue`、`SettingsModal.vue`、`ModelsPanel.vue`、`ModelMenu.vue`、`frontend/src/App.vue`、`frontend/src/i18n.mjs` |
| 模型快照与显示 | `frontend/src/utils/modelConfigIO.mjs`、`modelLabel.mjs`、`chatgptModel.test.mjs` |
| 测试前置条件修正 | `internal/app/biz_skills_test.go`（缺工作区用例显式清空辅助函数提供的工作区） |
| 依赖与文档 | `go.mod`、`go.sum`、`CODEGRAPH.md`、`docs/api.md`、本文档 |

## 官方规范

实现依据为 2026-10-17 核对的 [总览](https://developers.openai.com/siwc/token-sharing-open-source)、[注册与登录](https://developers.openai.com/siwc/token-sharing-open-source/sign-in)、[账号与会话](https://developers.openai.com/siwc/token-sharing-open-source/profiles-and-sessions)、[模型与推理](https://developers.openai.com/siwc/token-sharing-open-source/models-and-inference)、[Preview 限制](https://developers.openai.com/siwc/token-sharing-open-source/preview-limitations)、[错误恢复](https://developers.openai.com/siwc/token-sharing-open-source/errors-and-recovery)，以及 Responses [工具 namespace 格式](https://developers.openai.com/api/docs/guides/tools)。
