# Ally Agent Core：上下文管理与 Token 核算代码导读

> 本文是 agent 系列导读的第三篇（前两篇：`agent-core-loop.md`、`agent-tools.md`），讲上下文预算这条线。
> 行号基于撰写时的工作区版本，重构后可能漂移；定位以「函数名 + 文件」为准。

## 0. 三件事

上下文模块只回答三个问题：

| 问题 | 代码 |
|---|---|
| 下一次请求到底有多大？ | `ContextBreakdown` 口径（`biz_context.go`） |
| 太大了怎么办？ | 压缩三入口（`biz_compact.go`） |
| 花了多少 token？ | 统计累计与面板（`biz_stats.go`） |

核心设计原则只有一条：**一个数、一个口径**。footer 显示的占用比例、自动压缩的触发线、压缩前后的 before/after 都必须是同一个数，否则用户会看到「45% 却不压缩」这类现象。收口点是 `getContextBreakdown`（footer 轮询）与 `finalizeSessionBreakdown`（run 循环每步调用），两者最终都落到 `finalizeContextBreakdownTotal`。

## 一、口径：`ContextBreakdown`（`biz_context.go:75`）

```go
type ContextBreakdown struct {
    Total        int   // 权威值
    Estimated    int   // 各分项估算之和
    SystemPrompt int   // 请求前缀：系统提示词 + 工作区地图
    ToolSchemas  int
    UserMessages, AssistantMsgs, ToolResults, Reasoning int
    MeasuredTokens   int  // provider 实测（prompt + completion）
    MeasuredMessages int  // 该实测覆盖的「会话消息条数」
    TrailingTokens   int  // 实测之后新增消息的估算
}
```

权威值的计算规则（`finalizeContextBreakdownTotal`，`biz_context.go:324`）：

```go
result.Estimated = 各分项之和
result.Total = result.Estimated
if result.MeasuredTokens > 0 { result.Total = result.MeasuredTokens + result.TrailingTokens }
```

即：**没有实测时纯文本估算；有实测时，实测覆盖「请求前缀 + 它数到的那些消息」，只有其后新增的消息才重新估算**。分项字段在任何情况下都是估算值，所以产生实测后分项和可以不等于 `Total`——这点在结构体注释里写明了，避免后来者以为是 bug。

## 二、估算器：口径怎么来的

| 函数 | 规则 |
|---|---|
| `estimateTokensFromText`（`biz_context.go:189`） | 按字符分类：ASCII `⌈n/3.2⌉`，非 ASCII（CJK）`⌈n×1.4⌉`。注释说明这是按 o200k/cl100k/Claude/Qwen 的真实分词率调过的——老的 4 字符/token 会低估代码与 JSON |
| `estimateMessageBodyTokens`（`:332`） | 文本/多模态 part；**图片 part 固定计 2000 token**（按像素定价 1568×1568≈3200、1024×768≈1000-1300 取常数；旧的 256 曾低估 6-8 倍，导致图片多的会话不触发压缩） |
| `estimateRequestTokens`（`:208`） | 逐消息累加 role/body/name/toolCallID/reasoning + 每个 tool_call 的 id/type/name/arguments，最后加工具 schema |
| `estimateToolSchemaTokens`（`:279`） | 内置 schema 走 `sync.OnceValue` 进程级缓存（静态 5-15KB JSON，footer 每秒会轮询多次）；只挑 `mcp__` 前缀的部分单独 marshal，避免重复计数 |
| `estimateTokensFromMessages`（`:1373`） | 粗糙兜底（`字符数/3`），仅用于压缩路径的初始值/回退 |

教学点：**热路径上的估算要能承受每秒多次调用**。内置 schema 的 token 数缓存、`builtinToolSchemaTokens` 的 OnceValue、MCP 部分按前缀切分，都是为了这件事。

## 三、请求前缀与消息分桶

### 3.1 前缀：`sessionPrefixBreakdown`（`biz_context.go:503`）

```go
for _, part := range a.systemPromptPartsForBreakdown(...) { appendPart(part.label, part.content) } // 系统提示词各段
appendPart(workspaceMapPartLabel, a.peekSessionWorkspaceMap(sessionID, cfg))                       // 工作区地图
```

- **peek 语义**：还没有冻结的 session 用实时内容计数，但**不去冻结它**——footer 的轮询不能抢在首次真实请求之前把提示词字节定下来。
- 计划任务与进度完全由内置 `plan` 工具的调用与返回结果（Tool Message）自然保存在历史中，不向前缀动态注入非持久快照，保证消息序列 100% 严格单调追加与 KV 缓存复用。
- 这个函数同时被 footer 和 run 循环使用（`app.go:1824`），这就是「一个数」的实现方式。
- 分节 label 与前端共享（`workspaceMapPartLabel` 常量，`biz_context.go:679`），前端 `ComposerInfoBar` 用同一批 label 做本地化。

### 3.2 消息分桶：`liveBreakdownAccumulator`（`:590`）

利用 run 的消息列表是 **append-only** 的事实做增量扫描：

```go
func (acc *liveBreakdownAccumulator) update(messages) ContextBreakdown {
    if acc.nextMessage > len(messages) { acc.nextMessage = 0; acc.breakdown = ContextBreakdown{} } // 列表变短 → 重算
    for _, m := range messages[acc.nextMessage:] { addBreakdownMessage(&acc.breakdown, m) }
    acc.nextMessage = len(messages)
    finalizeContextBreakdownTotal(&acc.breakdown)
}
```

调用方在**上下文被整段替换后**必须 `reset`（压缩就是这种情况，见 `app.go:1845`、`:1957`）。`addBreakdownMessage`（`:648`）按 role 分桶到 user/assistant/tool，reasoning 单独一行——`computeLiveBreakdown` 和历史回退路径也走它，保证「有 run / 无 run」两种状态下 footer 数字一致。

`getContextBreakdown`（`:540`）的合并策略：**消息分桶用 live，前缀与工具 schema 现算**（它们可能在两次轮询之间变化）；没有 live（未在运行中）则回退到磁盘历史逐条累计，并同样解析实测锚点（`:583`）——这样切到老会话时 footer 立刻有正确数字。

## 四、实测锚点：把估算钉在 provider 上报的数上

纯文本估算在长工具循环里会持续漂移，所以用 provider 的 `usage` 做锚点。

```go
type contextAnchor struct { covered int; tokens int }   // biz_context.go:695
```

- **covered 按「会话消息条数」计，不是列表下标**（`conversationMessageCount`，`:703`，跳过 system）。原因写在注释里：系统提示词与工作区地图每请求重拼、永不落盘，run 的消息列表与持久化历史这两个列表的「下标」不通用，条数才通用。
- **写入**：`recordContextAnchor`（`:755`）在每个 assistant 回合之后调用（`app.go:2024`、`:2032`、`:2060`），`tokens = PromptTokens + CompletionTokens`。
- **解析**：`resolveContextAnchor`（`:723`）只在两种情况下有效——有实测，且当前列表的会话消息条数 **≥** 记录的 `covered`（即锚点仍覆盖一个前缀）；否则回退纯估算。列表变短（压缩、截断）时锚点必须失效，这是安全方向：估算不会少于真实请求。
- **失效**：`clearContextAnchor`（`:786`）在会话被重写时调用（压缩里 `biz_compact.go:365`）。
- **收口**：`finalizeSessionBreakdown`（`:800`）——「已持有 `a.mu` 的调用方用 `applyContextAnchor`，其余的走它」，注释明确要求调用方不得持锁（`a.mu` 不可重入）。
- **run 结束**：`restoreSavedHistoryBreakdown`（`biz_sessions.go:930`）把 live 口径换成持久化历史口径，并在**已持锁**的情况下直接读 `a.contextAnchors[sessionID]` 而不是调用 `contextAnchorFor`——同一条不可重入的坑，代码里有注释。

## 五、触发线：阈值怎么算

```go
usedTokens := bd.Total                                  // app.go:1836
if usedTokens > compactThresholdLimit(cfg) { ... }      // 自动压缩

func compactThresholdLimit(cfg ConfigState) int {       // biz_compact.go:270
    maxCtx := cfg.ContextWindow; if maxCtx <= 0 { maxCtx = 1000000 }
    return int(float64(maxCtx) * clampCompactThreshold(cfg.CompactThreshold))
}
```

`clampCompactThreshold`（`biz_compact.go:32`）：`<=0` → 默认 0.6；否则夹到 `[0.2, 0.95]`（太低会反复抖、太高会等到 provider 直接报错）。配置合并时也会 clamp 一次（`biz_config.go:322`）。

## 六、压缩：三个入口，一套内核

| 入口 | 触发 | 位置 |
|---|---|---|
| 手动 | 用户点压缩按钮，**无条件**执行总结 | `CompactSession` → `compactSessionConfig` → `compactSession` → `compactHistory`（`biz_compact.go:90`、`:126`、`:139`、`:280`） |
| 自动（阈值） | 每步 `bd.Total > 阈值`，失败不致命 | `compactRunHistory(..., compactReasonThreshold, ...)`（`app.go:1843`） |
| 强化（溢出恢复） | provider 判定上下文超长后，忽略阈值强制压缩一次再重发 | `compactRunHistory(..., compactReasonOverflow, ...)`（`app.go:1950`） |

**手动入口由调用方带模型**：`CompactSession(sessionID, instruction, overlay)` 的 overlay 就是 `StartChat` 收到的那份（GUI 传当前 Tab 的模型）。持久化配置里已经没有顶层模型字段，只有 `models[]` 预设 + `lastUsedModel` 身份（见 `expandLastUsedModel`）：界面切模型/发送时把身份写回配置，本地 API 用 `/api/v1/models/activate` 设它。优先级收口在 `compactSessionConfig`（`biz_compact.go:126`）：调用方 overlay → 会话冻结记录（无 Tab 上下文的本地 HTTP API 走这里）→ 配置里按身份展开出来的最近使用模型（会话在本进程从没跑过时的兜底）。

### 6.1 run 内压缩后的消息列表怎么重建（`compactRunHistory`，`biz_compact.go:238`）

```go
h := sanitizeHistoryMessages(history)
if len(h) <= 2 { return nil, nil, errHistoryTooShortToCompact }   // 非「失败」，只是没东西可压
a.emit("run:compact", ...)
result, err := a.compactHistory(ctx, cfg, sessionID, "", h, tokensBefore)
messages := a.buildSystemContextMessages(...)   // 系统前缀重建
messages = append(messages, compacted...)       // 压缩后的历史（含 summary）
if 本轮有用户消息 { messages = append(messages, 当前用户回合) }   // 再补一次，否则它被并进 summary 而丢失
```

要点：**压缩调用本身就带着这条 user 消息**（那才是它要总结的内容），所以重建请求时必须再补一次，否则最新用户消息只存在于 summary 里、不再作为真实 turn 出现。

### 6.2 内核 `compactHistory`（`biz_compact.go:280`）

1. **超时可配**：`clampCompactTimeoutSeconds`，默认 180s、范围 `[30, 3600]`；`Context.WithTimeout` 叠在父 ctx 上，所以 app 关闭仍能取消。
2. **提示词是「结构化交接文档」**（`:307`），包含几条关键规则：
   - 语言规则：用用户的语言写；
   - **需求漂移规则**：如果历史里已有上一次的 summary，其「用户需求与目标」「约束与偏好」两节必须**原样继承**，只有用户明确改过才更新并标注；不得为简洁删减，也不得复活已被取代的需求；
   - 写「当前有效版本」而不是照抄用户原话；冲突时以用户最新指令为准；
   - 固定章节：User Intent & Requirements / Constraints & Preferences / Findings & Analysis / What Has Been Done / Key Files & Locations / Next Steps；
   - 文件路径、命令、函数名必须精确；只输出 Markdown、不得调用任何工具。
3. **思考档位原样传递**：`completeModelTextWithUsage(ctx, cfg, ...)` 用用户自己的 `cfg`。注释明确了原因：写死档位会与设置静默漂移，而强推「关闭思考」会让必思考模型直接 400。
4. **计入统计**：压缩请求的 token 也走 `recordWorkspaceTokenUsage`（`:418`），用户能在面板看到压缩成本。
5. **落地与失效**：新历史 = 单条 user 消息（summary），然后
   `clearContextAnchor`（实测覆盖的消息已不存在）+ `reasoningStash.clearSession`（思考台账跟那些回合一起成了死重，而台账唯一的回收点就是这里——因为 `appendTurn` 为了前缀稳定从不裁剪）+ `saveHistory`。

### 6.3 并发与取消

- 同 session 只允许一个压缩（`compactingSessions`），且**run 期间禁止压缩**（`activeRunForSession` 非空直接 `errSessionRunning`）——否则两次 `saveHistory` 会交错（`biz_compact.go:157-166`）。
- `compactingSessions` / `compactingCancels` 在持锁前做 **nil 懒加载兜底**，注释写明：持锁写 nil map 的 panic 会逃逸在 cleanup defer 注册之前，互斥锁永久锁死、整个应用后续调用全部死锁。
- `CancelCompaction`（`:100`）让 ESC 不必等完压缩超时。

## 七、统计线：`biz_stats.go`

设计目标写在文件头（`biz_stats.go:24`）：

- **记录是 fire-and-forget**：`recordTokenStats` → `statsRecorder.record`（`:105`）走**有界非阻塞队列**（`statsQueueSize = 2048`），队列满就**丢样本**——注释明确「宁可掉一条遥测，也绝不让模型响应等待」。chat 热路径上只碰一个极短的 lifecycle 锁。
- **单个后台 goroutine 负责落盘**：每 `statsFlushInterval = 5s` 落一次脏日文件，退出时 drain（`statsShutdownTimeout = 10s`）。
- **存储**：`~/.ally_agent/stats/<date>.json`，保留 `statsRetentionDays = 90` 天；上限 `statsMaxRecordsPerDay = 10000`、`statsMaxTotalRecords = 20000`、单文件 `64MB`，超限丢最旧（`dropOldestStatsRecord`）；原子替换 + 备份恢复（`replaceStatsFile` / `recoverStatsBackups`）。
- **聚合按需算**：`GetTokenStats` 从内存快照计算，打开面板零磁盘 IO；`statsWindows` 同时覆盖「日柱状窗口」与「月初窗口」（注释举例：31 天月份的 1 号会落在 today-29 之前，只看 dailyStart 会漏掉月度汇总）。
- `statsRecord` 字段：model / workspace / ts / input / output / cacheHit / cacheMiss / requests；缓存命中率 = `Σhit / Σ(hit+miss)`。

**footer 的累计口径要注意一处刻意的取舍**（`biz_context.go:139` `recordWorkspaceTokenUsage`）：provider 没回真实 `PromptTokens` 时**不累加估算输入**（只累加估算输出）。原因是估算的输入包含整个留存上下文，会让 footer 的累计输入计数在很小的问题上突然跳几千。

## 八、一处已修正的注释

`CompactSession` 原来的注释描述了一个两段式流程（「Stage 1 免费 stub 掉陈旧的 tool-result，Stage 2 仅在占用仍超阈值时才调 LLM 总结」），但代码里从来没有 stub 阶段：`compactSession` 直接走 `compactHistory`。该注释已按实际行为改写——手动压缩是用户的明确意图、**无条件**执行总结，只有自动路径受阈值约束（`compactRunHistory` 的 `compactReasonThreshold` / `compactReasonOverflow`）。

把它记在这里，是因为这类残留注释正是「结论要看代码、注释只当线索」的典型例子。

## 九、可复用的设计原则

1. **一个数一个口径**：footer、触发线、before/after 全部落到 `finalizeContextBreakdownTotal`；前缀计数由 `sessionPrefixBreakdown` 单点提供，谁都不许另算一套。
2. **估算 + 实测锚点**：纯文本估算会漂，所以用 provider 上报值覆盖「前缀 + 已覆盖消息」，只估算其后的增量；锚点的覆盖范围用**与列表无关的计数**（会话消息条数）表达，列表变短即失效、回退估算（偏大是安全方向）。
3. **热路径零阻塞**：统计走有界丢样本队列 + 后台单写者；schema token 数进程级缓存；footer 轮询走增量累加器。
4. **压缩是「交接文档」而不是「摘要」**：固定章节 + 需求漂移规则 + 保留精确标识符，让压缩后能无缝续做；旧 summary 的需求/约束必须原样继承。
5. **压缩破坏的东西要一起清**：请求历史被重写 → 实测锚点失效、思考台账作废、`saveHistory` 重写；这些必须与压缩在同一处完成。

## 十、建议阅读顺序

1. `biz_context.go:60-330` —— 结构体口径 + 估算器 + `finalizeContextBreakdownTotal`
2. `biz_context.go:477-585` —— `sessionPrefixBreakdown` / `getContextBreakdown`
3. `biz_context.go:590-810` —— 增量累加器 + 实测锚点全套
4. `biz_compact.go:32-166` —— clamp 规则与手动压缩入口/并发守卫
5. `biz_compact.go:238-418` —— 三入口共用的压缩内核（提示词在这里）
6. `app.go:1830-1960` —— 触发线与两条恢复路径（阈值压缩 / 溢出压缩）
7. `biz_stats.go:24-52`、`638-778` —— 统计队列与聚合（想细看再读落盘部分）
