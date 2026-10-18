# ChatGPT 订阅缓存排查

缓存命中由服务端决定。客户端的系统提示词冻结、稳定路由键和原生 Responses 回放只是必要的请求一致性措施，不能证明某次请求已经命中缓存。

## 已核对的客户端行为

- `runChat` 按会话设置 `responsesPromptCacheKey`；订阅兼容层保留 `prompt_cache_key`。
- `response.completed` 的原始 JSON 由 `modelUsageFromResponsesEvent` 解析，避免 SDK union 解码丢失缓存用量。
- 完整 Responses output（含 reasoning、phase、工具调用）在后续请求中回放；这条路径也覆盖 OAuth 订阅。
- 编排层的 `noteRequestPrefix` 比对发生在协议转换之前，无法观察 SDK 序列化和订阅兼容层改写后的差异。

## 显式断点实验结果：已撤回

2026-10-18 使用 ChatGPT 订阅路由的 `gpt-6-luna` 分别测试了两个字段，两者都返回 HTTP 400：

- `prompt_cache_options is not supported on this model`
- `prompt_cache_breakpoint is not supported on this model`

普通 API 的模型文档不能证明订阅路由接受相同的缓存控制字段。不能再仅凭 GPT 版本号开启这些参数，也不能据这两次失败断言所有订阅模型都不支持。

Ally 已撤回这次实验：冻结提示词恢复为顶层 `instructions`，不添加 developer 锚点，不发送任何显式缓存字段，使用原来的隐式缓存请求格式。稳定缓存键、原生 Responses 回放与诊断功能保留。低命中原因仍未查实，此次回退只恢复兼容性。

当前测试程序构建在 `bin/tool-efficiency/Ally.exe`，已包含兼容性回退。退出旧版后，可在仓库目录的 PowerShell 启动：

```powershell
$env:ALLY_PERF_DIAGNOSTICS = Join-Path $PWD "ally-cache-compatible.jsonl"
& .\bin\tool-efficiency\Ally.exe
```

新日志应显示 `wire_explicit_breakpoints: 0`，不显示 `wire_cache_mode`（使用默认隐式模式）。旧实验程序和采集日志已清理，不随源码提交。

## 采集最终请求诊断

退出已有 Ally 后，在仓库目录的 Git Bash 启动刚构建的程序：

```bash
ALLY_PERF_DIAGNOSTICS="$PWD/ally-cache.jsonl" ./bin/Ally.exe
```

在同一轮任务中让模型连续执行几批工具，然后正常结束。诊断默认关闭；上述环境变量仅对这次启动生效。文件为追加写入，仅含请求时序、计数和比对结果，不保存正文、工具参数、凭据、账号、缓存键值或前缀哈希。

`responses_attempt` 的新增字段：

| 字段 | 含义 |
| --- | --- |
| `wire_prefix` | `first_request` 首次；`unchanged_prefix` 已发前缀保持一致；`head_changed` 请求头部或参数变化；`tail_changed` 已发历史改变；`removed_changed` 历史缩短；`lane_limit` 本轮诊断泳道达到上限 |
| `wire_input_items` | 最终 input 条目数 |
| `wire_reasoning_items` / `wire_encrypted_reasoning_items` | 最终请求中 reasoning 及带加密内容的条目数 |
| `wire_cache_key_present` | 最终请求是否携带缓存键，不记录其值 |
| `wire_explicit_breakpoints` | 最终 input 的消息内容块中显式断点数 |
| `wire_cache_mode` | 最终请求显式携带的缓存模式；沿用默认模式时不记录 |
| `cache_usage_reported` | 是否收到可用的缓存命中/未命中计数；false 不应当解释为实测零命中 |
| `cache_write_tokens` | 服务端报告的缓存写入 token 数；未提供时为 0 |
| `reasoning_context_effective` | 服务端返回的推理上下文模式；未返回则不记录 |

比对基线只存在于本轮 Run 内，每条模型/端点/身份/回放泳道独立，最多 64 条，结束即释放。跨用户提问的两个 Run 都从 `first_request` 开始，不能用它们判定跨 Run 前缀是否变化。普通接口未带缓存键且缺少独立回放 scope 时，也不能据此区分逻辑会话。

连续工具步若出现 `head_changed` / `tail_changed`，先定位客户端改变的请求；若保持 `unchanged_prefix`、确实收到零命中，则继续核对服务端实际 reasoning 模式、缓存写入及请求 ID。客户端 JSON 一致仍不能证明服务端渲染的完整 token 前缀一致或其缓存可用。压缩和显式历史改写造成的变化是预期行为。

## 与 Codex 比较时的边界

Ally 使用官方 Sign in with ChatGPT 的公共 Responses 路由。不能只凭模型显示名相同，就推定它与另一个客户端具有相同请求形状、服务端路由和订阅额度核算。API 现金价格也不能直接换算为 ChatGPT 订阅额度。

- [官方缓存说明](https://developers.openai.com/api/docs/guides/prompt-caching)
- [订阅推理路由](https://developers.openai.com/siwc/token-sharing-open-source/models-and-inference)
- [订阅预览限制](https://developers.openai.com/siwc/token-sharing-open-source/preview-limitations)
