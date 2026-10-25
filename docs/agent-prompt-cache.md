# Agent 提示词缓存：实现与观测

本文记录 Ally 为复用稳定上下文所做的工程措施。三协议的**当前字段清单只维护在[模型协议字段](model-protocol-fields.md)**；供应商资料见[字段参考](provider-api-fields.md)，订阅实测见[缓存排查](chatgpt-cache-diagnostics.md)。

## 1. 一致性不等于命中

缓存复用依赖供应商实际渲染的模型输入前缀，也受模型、思考配置、工具定义、缓存寿命、服务端路由和核算规则影响。客户端保存同一段历史、甚至发出完全相同的 JSON，都不能证明服务端一定命中。

不能把 HTTP JSON 的任意空白或键顺序变化直接等同于 token 前缀变化；供应商解析和渲染请求后才形成模型输入。Ally 仍采用确定、幂等的序列化与补写，目的是减少自身制造的变化并支持排查。

```mermaid
flowchart LR
    A["冻结系统提示和工具集"] --> B["保留已有历史"]
    B --> C["追加用户输入或工具结果"]
    C --> D["协议转换与最终序列化"]
    D --> E["服务端渲染、路由与缓存"]
    E --> F["响应 usage：实际观测值"]
```

2026-10-25 的小样本实测中，完全相同的请求体连续两次也出现过不同缓存计数。当前不能把“删除 summary”或“恢复 key”视为所有订阅零缓存问题的确定修复。

## 2. 协议边界

协议策略、发送条件、字段省略项集中在[缓存键规则](model-protocol-fields.md#cache)及各协议表，本页不再复制一份字段矩阵。

- OpenAI 两条协议沿用服务端默认缓存行为；是否发送请求级缓存键与内部会话身份分开处理。
- Anthropic 的可用内容块使用缓存标记；省略 TTL 后仍要保留标记的 type，不能让 SDK 把整个零值对象省略。尾部是不可标记的思考块时，继续向前找可用内容块。
- 思考回填、原生 output 与工具调用 ID 属于[历史协议契约](model-protocol-fields.md#history)，不能作为“精简可选字段”删除。
- 某模型在普通 API 支持的缓存参数不一定被登录订阅路由接受。请求被接受也不证明该字段改善了命中。

## 3. 会话前缀与历史

<a id="prefix"></a>
### 3.1 冻结前缀

[上下文实现](../internal/app/biz_context.go)中的 `sessionSystemPromptParts` 按会话保存系统提示词快照；[工作区实现](../internal/app/biz_workspace.go)中的 `sessionWorkspaceMap` 保存目录地图快照。工具集也按会话冻结。

这样，当前会话内修改 AGENTS.md、CODEGRAPH.md、LESSONS.md 等文件，不会在下一工具步无意中重建整段前缀。需要新文件信息时，通过工具结果追加到历史。

冻结不是永久不更新。成功压缩后，`refreshSessionPromptPrefix` 清除系统提示、地图、工具集等快照，下一次请求重新构造；新会话和会话释放也有各自生命周期。压缩已经重写历史，不能要求压缩前后的整个请求前缀保持相同。

<a id="scopes"></a>
### 3.2 内部身份与回放隔离

[子代理编排](../internal/app/orch_subagent.go)中的 `subagentCacheConfig` 保留两种身份：

| 身份 | 用途 |
|---|---|
| `responsesPromptCacheKey` | 同一父会话子代理通道的稳定标识；仅在协议发送规则允许时转成请求级缓存键 |
| `reasoningScope` | 每个 run 独立的思考台账作用域，防止相同工具调用 ID 导致不同 run 互相覆盖 |

省略网络请求里的 key 不会清空这两个内部身份。稳定通道有利于保留可复用条件，但不保证不同请求都到同一机器，也不保证缓存命中。子代理结束时清理自己的思考作用域，不能清掉主会话或其他 run 的台账。

收尾报告阶段保持既有工具声明，避免仅因为进入报告阶段就改写工具前缀；是否执行新工具调用由编排逻辑决定。

<a id="history"></a>
### 3.3 追加已有历史

正常工具循环保留既有消息顺序，将助手调用、工具结果和新用户输入追加到末尾。计划进度由 plan 工具结果进入历史，不在每个用户问题之前临时插入又撤回一份计划快照。

请求级通知、收尾提示、用户编辑、失败后的历史修复和压缩需要分别核对。不能把“正常循环采用追加策略”写成“任意路径永远只 append”或“后续调用接近 100% 命中”。

协议转换同样需要稳定：Chat 的显式空思考字段、Responses 的原生思考条目、Anthropic 的签名块都按各自契约回传。只比较编排层消息，不能排除 SDK 或最终请求改写造成的差异。

## 4. 用量统计

以供应商响应 usage 为观测来源。[三协议适配器](../internal/app/prov_model.go)归一成 `modelUsage`：

| 归一字段 | 含义 |
|---|---|
| `PromptTokens` | 归一后的输入总量 |
| `CacheHitTokens` | 供应商报告的缓存读取量 |
| `CacheMissTokens` | 未命中的输入量，包含归一后计入此项的缓存写入量 |
| `CacheWriteTokens` | 缓存写入量，作为独立观测指标；不是第三份输入 |
| `CompletionTokens` | 输出总量 |
| `ReasoningTokens` | 输出总量中的思考子集，不能重复相加 |

Anthropic 原始 `input_tokens` 不含缓存读取/写入部分，适配器将 `input_tokens + cache_creation_input_tokens` 归入 miss，再加 `cache_read_input_tokens` 得到输入总量。不要直接把不同协议同名字段当成相同口径。

命中率按汇总量计算：

```text
CacheHitRate = Σ CacheHitTokens / Σ (CacheHitTokens + CacheMissTokens)
```

消息页脚展示本次 run 的命中率，当前前端向下取整为整数百分比；0% 单独看可能包含极低命中率。排查时同时查看 hit / miss 原始数值，区分“明确报告零命中”和“没有获得缓存遥测”。不能把缓存写入量再次加入分母，也不能把 API 现金价格直接换算为订阅额度百分比。

## 5. 排查顺序

1. 确认实际运行程序、构建时间、端点、认证方式、模型和思考档位。
2. 对照[字段清单](model-protocol-fields.md)，查看**最终请求**，包括 SDK 序列化和订阅兼容层处理后的内容。
3. 检查输入历史是否完整、同一前缀是否变化，是否发生压缩、工具集切换或身份切换。
4. 确认原始 usage 是否包含缓存字段；用量缺失与明确的零值分开记录。
5. 对相同账号/端点做控制变量实验。每组分别预热、重复、追加完整原生输出；记录时间、返回模型、effort、service tier 及原始计数。
6. 对请求被接受、字段被省略、前缀未变、缓存确实命中分别下结论，不能相互替代。

最终请求诊断开关、字段含义和已知局限统一见[订阅缓存排查](chatgpt-cache-diagnostics.md#diagnostics)。其中的客户端前缀比较目前只覆盖同一 run，不能用多个 run 都出现 first_request 来证明跨提问前缀一致。

## 6. 参考

- [OpenAI Docs：Prompt caching](https://developers.openai.com/api/docs/guides/prompt-caching)
- [OpenAI Docs：Prompt cache diagnostics](https://developers.openai.com/api/docs/guides/prompt-caching/diagnostics)
- [Anthropic：Prompt caching](https://platform.claude.com/docs/en/build-with-claude/prompt-caching)
