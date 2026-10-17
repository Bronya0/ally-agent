# ChatGPT Auth 对接实现

Ally 通过系统浏览器完成 ChatGPT OAuth 授权，由 Go 后端管理凭据，并将已授权账号接入 Responses Provider。认证模块位于 [`internal/chatgpt/`](../internal/chatgpt/)，桌面桥接位于 [`biz_chatgpt.go`](../internal/app/biz_chatgpt.go) 和 [`host_chatgpt.go`](../internal/app/host_chatgpt.go)。

## 授权流程

入口为 `LoginChatGPT(accountID, enablePlan)`，实际流程由 `Manager.Login` 实现：

1. 从加密凭据库读取安装级 `ext_agent_host_id`；首次使用生成 `urn:uuid:<UUID>`，之后复用。
2. 在 `127.0.0.1` 随机端口启动 `/auth/callback`，生成独立的 `state`、`nonce` 和 PKCE verifier，使用 `S256` challenge。
3. 通过系统浏览器打开 `https://auth.openai.com/api/accounts/authorize`。首次授权使用 `client_id=dynamic_agent_client`、`agent_name_hint=Ally`；已有账号重新授权使用保存的实际 `client_id`，并按已有身份提供登录提示。`enablePlan` 为真时携带 `prompt=consent`。
4. 回调校验 HTTP 方法、Host、`state`、授权码及客户端注册身份。首次授权从回调取得实际签发的 `client_id`，换取 Token 时使用该值，而非 `dynamic_agent_client`。
5. 携带授权码、实际 `client_id`、PKCE verifier、同一 `redirect_uri` 和 `resource` 交换 Token。Token 端点通过 OIDC discovery 获取。
6. 使用 discovery/JWKS 校验 ID Token 的 RS256 签名、issuer、audience、有效期及 `nonce`。重新授权还校验 `sub`，避免覆盖其他身份。
7. 保存凭据并返回脱敏的账号信息。成功、失败、取消或超时都会关闭回调监听；整个登录过程限时五分钟。

授权请求的固定参数为：

```text
issuer   = https://auth.openai.com
resource = https://api.openai.com/v1
scope    = openid profile email offline_access resource.invoke chatgpt.tokens.use.direct
```

账号以实际签发的 `client_id` 作为注册标识，不按邮箱合并。订阅调用权限依据实际授予的 `resource.invoke` 和 `chatgpt.tokens.use.direct` 判断。

## 凭据保存、刷新与退出

[`store.go`](../internal/chatgpt/store.go) 将安装标识和多账号凭据保存在应用数据目录下的 `chatgpt/credentials.enc`。普通模型配置只保存 `authType=chatgpt_oauth`、`OAuthAccountID` 等引用信息；前端账号列表不接收 Access Token、Refresh Token 或 ID Token。

- Windows 使用当前用户的 DPAPI；其他平台使用系统钥匙串保存的密钥进行 AES-GCM 加密。保护器不可用时返回错误，不回退到明文。
- `credentials.lock` 跨进程文件锁覆盖读取、刷新和保存；写入通过同目录临时文件、同步及原子替换完成，避免并发刷新覆盖轮换后的 Token。
- 请求前检查 Access Token；剩余有效期不超过 60 秒时，使用该账号保存的实际 `client_id`、Refresh Token 和 `resource` 刷新。刷新返回新 ID Token 时验证身份，返回轮换 Token 时更新凭据。
- 网络等临时刷新错误保留原凭据；终止性刷新错误清除失效 Token，保留注册信息以便重新授权。
- `LogoutChatGPT` 先取消该账号的在途请求，再尝试向 discovery 提供的撤销端点撤销 Refresh Token，并清除本地 Token。保留安装标识与注册信息，不影响其他账号。

## 接入模型请求

[`biz_chatgpt.go`](../internal/app/biz_chatgpt.go) 的 `normalizeModelAuth` 将 OAuth 模型固定为官方 Resource 和 Responses 协议，并清空 API Key、Key 池及自定义请求头。[`prov_model.go`](../internal/app/prov_model.go) 在 API Key 选择之前分流 OAuth 请求，认证失败直接返回错误。

`Manager.Do` 只允许向官方 Resource 发送 `GET /models` 和 `POST /responses`，在后端注入 Bearer Token，禁止自动重定向，并登记在途请求供退出时取消。HTTP 客户端沿用 Ally 的代理配置。

[`prov_chatgpt.go`](../internal/app/prov_chatgpt.go) 负责订阅 Responses 请求的兼容转换：

- 只保留支持的请求字段，固定 `store:false`、`stream:true`，要求 `input` 为完整上下文数组。
- 将输入中的 `system` 消息转换为 `developer`。
- 将本地 function/custom 工具封装到 `ally` namespace，并为回放的 `function_call` 补充同名 namespace；不支持的托管工具直接报错。
- 保留用户选择的推理档位，通过现有 Responses 适配器处理流式输出与上下文续接。
- 保留结构化错误与请求标识，并清理错误中可能回显的 Bearer Token。`subscription_sharing_usage_limit_exceeded` 会暂停该账号的调用，重新授权或显式刷新模型目录后可再次尝试；其他永久性授权错误直接返回，不自动重试。

设置页与模型页共用 `ChatGPTAccounts.vue`，通过桌面桥接执行登录、取消、退出和模型目录刷新。本地 HTTP API 可以调用已经配置好的 OAuth 模型，但不提供登录、刷新凭据或读取 Token 的接口。
