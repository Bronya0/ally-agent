# 原生界面内存与渲染优化记录

记录截至 2026-11-07、mygo 分支 `b1d8e343` 的已落地优化与维护边界；重点场景是一个 tab 从早到晚持续对话和执行任务，每条要点独立一行，具体上限以实现中的常量为准。

可重复执行的性能与存活内存套件见 [tests/performance/README.md](../tests/performance/README.md)，入口为 `python tests/performance/run.py`。覆盖超长会话、索引、搜索、工具结果、快照、离屏渲染和释放；固定运行一套完整测试，无模式或筛选选项；源码保留，统一报告和原始日志写入 `.tmp/performance/`。

## 渲染与缓存的基本边界

- MyGo 每帧重建控件树，解析、切分、排序、格式化、JSON 编解码等派生工作应按数据变化执行，避免随帧重复执行；入口见 [tool_render_memo.go](../desktop/tool_render_memo.go)。
- 控件派生数据优先用 `ui.Local`、`memoAt`、`memoOf` 保存单份最新结果，让缓存随控件生命周期释放，不新增按消息或源文本无限增长的全局缓存。
- 缓存键必须覆盖实际输入，包含文本来源、主题、语言、显示模式等影响结果的字段；不能只按长度、指针或“已计算”布尔值判断。
- 列表派生结果使用 `indexedMemo` 逐项比较展示与筛选字段，同数量替换或修改记录也必须失效，不能把长度相同当成内容未变。
- 缓存尽量保存索引、ID、计数和必要的展示数据，避免保存完整模型配置、消息 map、工具输出或历史副本。
- UI 状态及其缓存只在主线程修改，后台任务通过 `Window.Update` 发布结果，不把 `ui.Context` 或 `ui.Element` 带入后台。
- 渲染期不读写磁盘、不发网络请求、不启动无条件后台任务，加载由已有事件或显式用户操作触发。
- 优化时分清每帧、每事件和每请求开销，`sendSel` / `metal.send` 的绘制开销不能与每请求一次的 Marshal、工具清单克隆混算。

## 主对话索引、分页与工具组

- 工具调用与子代理事件通过 `transcriptLookup` 的 ID → 行号索引定位，普通追加只索引新尾部，避免每次事件倒扫全天消息；入口见 [transcript_lookup.go](../desktop/transcript_lookup.go)。
- `transcriptLookup` 只保存 ID 和行号，删除、替换或压缩历史后必须 `resetTranscriptLookup`，不能让旧行号继续定位新消息。
- 消息绘制归属、工具折叠和分页共同使用 `transcriptRows`，避免三个入口各自判断导致空白行、重复子代理或页首落在不可见消息上；入口见 [transcript_rows.go](../desktop/transcript_rows.go)。
- 普通消息追加通过 `attachTail` 增量更新归属信息，已有父卡片 ID 和子代理 ID 映射只保留身份数据，不复制消息或结果。
- 新增 subagent 父卡片时，依据旧消息的父 ID 和 agent ID 引用判断能否增量追加；可能认领旧子消息或覆盖已使用映射时仍完整两遍重建。
- 已有行的工具类型或 subagent 结果中的 agent ID 变化时必须使归属索引失效，不能只依赖消息数量变化。
- 历史块起点通过 `blockStarts` 增量追加，复用旧前缀并重新判断追加接缝，不在每次追加时重新扫描全部历史。
- `historyBlocksKey` 同时包含行数、`historyRev` 和归属对象，只有归属对象存活且版本未变时才可复用分页前缀。
- 历史起点切片复用容量但不按全部消息数预留，历史大幅缩短时淘汰过大容量，避免大量折叠工具记录撑大常驻数组。
- 已索引的空 assistant 收到首个 token 时用 `markHistoryAt` 失效，尚未索引的新行由长度键覆盖，避免流式首 token 无条件重建旧历史。
- 删除或丢弃压缩预览时显式增加 `historyRev`，即使下一帧前又追加到原消息数量也不能命中过期索引。
- 欢迎消息存在性随归属索引增量维护，避免追加一条工具消息就重新扫描整段历史，也不保留跨会话的全局欢迎缓存。
- `chatMessages` 只遍历当前页实际绘制的 `visibleRows`，不逐条访问大工具组中已经折叠、被其他卡片绘制的消息；入口见 [page_chat.go](../desktop/page_chat.go)。
- 当前页的回合统计位置按历史版本、窗口范围和回合数缓存，正文持续流式变化时不反复重建相同的 footer 位置。
- 回合位置使用排序后的真实统计索引，以及 user / suggest 索引的二分查找，不扫描两次提问之间成千上万条工具记录。
- 分页落在回答和 suggest 之间时保留前一回合的统计，新增 suggest 后移动统计位置，删除或压缩后清理旧位置。
- 工具组头部计数和状态保存标量摘要，稳定帧直接复用，工具事件更新使用 `toolRev`，历史拓扑或裁剪使用 `historyRev`；入口见 [tool_cards.go](../desktop/tool_cards.go)。
- 工具组通过最多 64 条的 `toolGroupChanges` 日志增量应用状态与计数变化，日志只保存前后类型、状态、折叠标记、裁剪标记和行号。
- 工具组新增成员只扫描新增尾部，窗口缩小、历史重写、工具归属改变或日志溢出时回退完整重算，不能为了增量更新牺牲正确性。
- 工具组状态保留“有 running 优先，否则有 pending 优先，否则最后成员结果”的原规则，不能只拿最近事件代替整个组的状态。
- `run:done` / `run:error` 批量结算仍运行的工具时也记录工具组变化，不能依赖每条工具都一定收到独立结束事件。
- 工具参数统一走 `toolArgsMemo`，卡片头和卡体在同一帧重复读取时命中同一份结果，不另建解析缓存。
- 折叠工具组或子卡片时释放展开的行结构、派生缓存及命令详情，并同步关闭相关子卡，避免重新展开后出现等待已丢缓存的加载状态。
- 折叠减少的是控件数量和绘制调用，原始消息仍可能驻留，不能把“折叠了”当成工具正文已经释放。

## 工具结果、create 预览与历史落盘

- 模型运行上下文、原生 UI 消息、磁盘历史是三份不同的数据边界，展示裁剪只操作 UI 副本，不能删掉运行中模型仍需使用的证据。
- `toolEventForDisplay` 在 UI 事件序列化和入队前裁剪本地 command 及可折叠 read/search/list 的正文，入口再次投影作为直接调用和结构化载荷的兜底；入口见 [command_detail.go](../desktop/command_detail.go)。
- 展示投影先复制消息，并用 `detachToolResultEnvelope` 分离结果 envelope 和 data，再展开元数据，防止 unwrap 的就地合并改写后台原始事件；入口见 [tool_snapshot.go](../desktop/tool_snapshot.go)。
- 工具结果在事件或历史加载入口规范化为 map，渲染读取已解码结构，不在卡片头和卡体重复 `json.Unmarshal`；缺失结果返回 nil，避免空 map 分配。
- 可折叠 read / remote_read / batch_read / grep / list / list_files / glob 的 UI 消息只保留卡片所需路径、行范围、数量、状态和错误，不常驻整份读取或匹配正文。
- read 系列的 live UI 投影保留完整的逐文件错误、missing 信息及范围信息，磁盘错误另按原有预算截断，两者不能混用。
- UI 投影保留明确的 `fastCompacted` 摘录；MCP 卡片只显示状态和耗时，事件及历史 UI 副本保留这些摘要，运行上下文保留完整结果。无结果的进度事件不凭空创建结果。
- 新鲜 live UI 元数据不标记 `resultOmitted`，该标记表示历史结果已省略，不能把正常工具结果显示成历史缺失。
- 本地 command 的 UI 消息只保留退出码、状态、耗时等摘要，展开时通过现有模型上下文入口按需加载详情，不为每条命令常驻另一份输出。
- 命令详情关闭、所在历史页离开、历史重写或会话释放时同步清理 `CommandDetails` 与展开状态，后续需要时重新按需加载。
- 命令详情异步返回时核对 session ID、当前展开状态和同一 detail 对象，关闭后或替换后的迟到结果不能重新塞回缓存。
- 已完成 success/error 工具收到迟到 `tool:update` 时在复制字段前返回，避免恢复已裁剪 create 正文或覆盖最终结果；入口见 [events.go](../desktop/events.go)。
- create / remote_create_file 在完成、失败或取消后，对超过 8 KiB 的合法 args 内容只保留末尾 `createPreviewLines` 行，其他参数和文件路径保留，流式阶段保留原效果。
- create 预览最多保留最后 6 行、8 KiB，并保存裁剪前的起始行号；截取不能拆开 UTF-8 字符。
- 新 args 到达时清理旧 `createPreviewTrimmed` / `createPreviewStartLine` 标记，避免下一份内容沿用旧行号。
- create / edit 工具行点击复用工作区文件打开入口，edit 下方 diff 的展开折叠独立保留，文件实际不存在时沿用已有错误处理，不伪造编辑器成功打开。
- 快照先投影会省略的大正文、再深克隆，已完全分离的摘要可直接交给快照，避免先复制大数据再丢掉；入口见 [desktop.go](../desktop/desktop.go) 的 `snapshot`。
- 原生历史快照保留观察型工具的展示摘要，正文省略名单统一来自 `OmitToolResultOnDisk`，不让 UI 和后端各维护一份不一致的名单；入口见 [builtins.go](../internal/tools/shared/builtins.go)。
- 后端最近任务的磁盘历史沿用原有工具结果占位策略，受保护历史也省略 read/search/list/command 原始观察；保留调用 ID、参数和合法调用配对，不能只删除 result 消息。更早任务先复用快速压缩投影，转为目的与结果元信息记录；入口见 [prov_history_hygiene.go](../internal/app/prov_history_hygiene.go) 与 [prov_history_storage.go](../internal/app/prov_history_storage.go)。
- 从旧受保护历史恢复时重新应用当前省略规则，避免重启后复活过时的大正文，同时保留必要的原生回放及推理状态；入口见 [prov_history_storage.go](../internal/app/prov_history_storage.go)。
- 明确压缩过的有界工具摘录和持久的生成图片元数据继续保留，不能把所有工具结果一刀切为空。
- 会话落盘仍按既有完整快照入口执行，这些优化减少快照载荷和复制量，没有把持久化改成增量日志；后续修改保存策略须单独核对原子写和退出保存屏障。

### 本地快速压缩

- 主任务开始时记录边界，和历史一起原子保存；运行中追加的用户输入、图片注入和子代理返回不移动边界。无边界的旧存档按完整助手回复后的用户输入推断，保守保留最近一轮任务。
- 最近一轮任务的参数、结果、推理及协议回放保持完整。更早历史转为带原发言角色的普通资料消息：用户和助手正文保留，工具只保留目的与结果元信息头；旧调用、结果和推理回放成套移除，不发送参数已清空的历史调用。
- 工具元信息在结果生产入口从结构化数据提取，再与执行状态一起保存；批量 read/edit 保留各成员的头，不扫描文件、命令或 MCP 正文中的 `//` 行。目的和元信息分别有 UTF-8 大小上限；旧存档缺少生产元信息时仅采用可识别的首行或真实已知状态，不猜测正文。
- 最新结构化计划作为任务状态保留。旧助手正文不再做首尾摘录或 8k 清空；正文占据主要空间时，本地压缩收益可能有限。
- 没有旧协议推理状态可释放时，手动压缩不会因资料块包装而增加短纯文本对话的估算 token；没有收益时保持原历史。加密/签名推理不计入纯文本估算，清理它不保证估算 token 同步下降。
- 自动压缩只在工具批次（含全部子代理）结束后判断；当前任务的子代理报告不会在主模型读取前被裁掉。候选节省不足一半时仍用原始历史走模型总结，候选失败/取消不能提前更改历史、回放或读取缓存。
- UI 保留原用户/助手行和压缩工具卡片；从模型历史恢复时解码原角色，不能把宿主生成的资料块显示成用户的新提问。删除旧提问也按资料块内的原始用户消息定位。
- 快速压缩保持会话冻结的提示词、技能和工具清单；完整模型总结成功后才刷新这些快照。自定义历史改写会损失部分供应商缓存或后续推理复用，不承诺协议服务端无损延续。
- 模型历史和 UI 会话快照落盘复用同一纯投影，只修改磁盘副本；正常/异常/取消结束等既有保存入口不变，不增加工具批次或定时保存。普通历史与加密恢复数据从同一份投影生成，近期原生回放的 assistant 序号单独重映射；运行中的模型上下文、推理账本、读取缓存和 UI 原始消息不受保存影响。

## 流式文本、Markdown、高亮与搜索

- 普通回答和压缩摘要共用 `appendStreamChunk` 的 `strings.Builder`，按累计内容增长，避免每个 token 都用 `before + delta` 复制全部旧文本；入口见 [events.go](../desktop/events.go) 与 [compact.go](../desktop/compact.go)。
- 流式缓冲绑定行号和当前原文，行号或原文不再对应时重建，取消摘要后继续正文不能把摘要缓冲接到原回答上。
- 删除、压缩或替换历史时通过 `releaseStaleStream` 释放已退休的流式缓冲，不能一直等下一次回复才释放旧长文本。
- 完整替换历史时成套清理 Markdown、流式、工具组日志、回合统计、提示导航、工具展开和命令详情，不保留旧行号对应的派生状态。
- 主对话 code block 按消息源、语言和扩展设置缓存代码、行号等派生内容，未闭合的大代码围栏在源不变时也不重复取 Code；入口见 [markdown_theme.go](../desktop/markdown_theme.go)。
- 源文本缓存只用于主对话即时展示的 `markdown.View`，没有源 getter 的通用 viewer 保留 `Code()` 行为，避免破坏 `markdown.Stream` 随帧显露内容的语义。
- 主对话未闭合代码块保持增量的内层 Markdown 源，去除解析器为不完整末行添加的换行，闭合后恢复完整代码块；避免每次更新重新高亮整段代码。
- create 尾部、diff 行、命令预览和输出清理按来源字符串 memo，`tailLines` 从尾部扫描，不恢复成全文 Split 后再 Join；入口见 [tool_render_memo.go](../desktop/tool_render_memo.go)。
- 代码高亮逐行使用 `strings.SplitSeq`，复用 tokenizer 的 token 切片，并合并同一行相邻同样式 span，减少临时行数组和 span 数量；入口见 [tool_resources.go](../desktop/tool_resources.go)。
- 代码高亮共享缓存同时限制 32 项和约 256 KiB 的来源及 span 准入权重，超过预算的内容可绘制但不常驻，权重不是实际堆内存测量值。
- 高亮缓存键包含来源、路径、深浅主题、accent 和基础文字颜色，切主题或改变显示色时不能复用错误颜色。
- 缓存短尾部或片段前用 `strings.Clone` 分离来源和路径，防止 Go 子字符串通过底层大字符串继续保留整份文件或日志。
- 搜索 Query 与大小写、整词、正则选项的组合键按变化缓存，正则表达式按查询编译，不能每个消息块每帧重复格式化或编译；入口见 [chat_search.go](../desktop/chat_search.go)。
- 搜索同步扫描上限为 4 MiB、400 个命中及 8192 个正则候选，保留截断状态，不能把预算耗尽报告成完整无结果。
- 搜索代码块高亮限制 64 KiB、200 个 span 和 1000 行，超限保留普通代码绘制与命中导航，避免一帧生成无界高亮控件。
- 流式正文末尾变化使用 `invalidateChatSearchTail`，保留此前命中并约每 150 ms 重扫尾部，编辑旧行或历史形状变化使用完整失效。
- 新工具记录只追加时复用已有搜索结果和字节预算，仅扫描新增尾部；旧文本改动、预算截断或命中上限继续完整重扫。
- 压缩预览位于末尾时走尾部搜索失效，若用户注入后预览位于中间则走完整失效，不能误把中间改动当成末尾追加。
- 搜索 span/code 缓存只保留当前历史页，关闭搜索、改变查询或重写消息时释放旧缓存，不随全天访问过的页面积累。
- 搜索的 `frameDoc` 只在当前构建共享，帧结束即清空，不让最后解析的文档额外常驻。
- MyGo 会把已注册 Handle 绑定保留到窗口关闭，提示导航和搜索行复用离开当前页的 Handle，不能每次翻页都新注册一批；入口见 [transcript_rows.go](../desktop/transcript_rows.go)。
- 同一提示行的搜索和提问导航共用 Handle，回收时去重并清理旧映射，避免复用后滚动到另一条消息。

## 工作区、文件预览与界面状态

- 文件树按打开的工作区保留目录展开、已加载子目录和滚动状态，切 tab 回来恢复原状态，搜索结果不覆盖普通目录树；入口见 [explorer_state.go](../desktop/explorer_state.go)。
- 关闭工作区最后一个 tab 时释放目录树并清理当前别名引用，防止关闭后又把旧树写回缓存；迟到加载结果继续使用既有 epoch 检查。
- 文件预览每个打开的工作区最多保留一个 WebView，同工作区图片或视频切换复用实例，切工作区、页面、隐藏或最小化保留已有页面和播放状态；入口见 [workspace_preview.go](../desktop/workspace_preview.go)。
- 关闭预览、关闭资源管理器或关闭工作区最后一个 tab 时销毁对应文件预览，退出统一释放，损坏实例可重建，不用频繁创建销毁掩盖内存问题。
- 图片切换先加载并解码新图片，再替换页面内图片节点，避免清空页面、重新导航和 WebView 重建导致的两次闪烁；入口见 [workspace_media.go](../desktop/workspace_media.go)。
- 媒体播放状态、播放模式和一次性播放意图按工作区隔离，异步回调核对 token / generation，防止切换后旧事件控制新文件。
- Markdown 文件预览与编辑沿用工作区的 `editor.State`，显示模式切换不创建新缓冲替代未保存内容，也不套用媒体 WebView 生命周期；入口见 [file_documents.go](../desktop/file_documents.go)。
- HTML / SVG 等标记文件与生成文档预览保留大小预算，不能为省布局步骤无界生成或常驻一份大型 HTML；入口见 [workspace_preview.go](../desktop/workspace_preview.go) 的预览常量。
- render_html / Mermaid 工具预览统一通过 `previewPool` 和 `previewTargets`，当前可见目标最多 4 个，先选定有界目标再协调实例，防止第 5 个目标反复驱逐重建；入口见 [preview_lifecycle.go](../desktop/preview_lifecycle.go)。
- 预览失败保留失败状态直到显式重试，异步加载和替换核对实例及 generation，不能每帧自动重建或发布过期结果。
- 历史会话与底部选择面板使用原生浮层覆盖正文，再次点击入口关闭，不把面板放进正常布局挤压聊天区域；入口见 [composer.go](../desktop/composer.go)。
- 聊天滚动容器占满正文，只有内容在内部限宽居中，避免大窗口滚动条跑到页面中间；入口见 [page_chat.go](../desktop/page_chat.go)。
- 最近工作区由访问记录维护并按最近访问展示，当前最多记录 100 个；不能用字母排序或扫描结果冒充最近访问；入口见 [workspace_history.go](../desktop/workspace_history.go)。

## 常驻引用、列表与其他页面

- 未活跃且可重新加载的会话释放 Markdown 和附件图片派生缓存，`rememberSession` 只额外保留最近一个可丢弃历史对象，运行中任务、草稿和待处理附件不丢弃；入口见 [desktop.go](../desktop/desktop.go)。
- 离开历史页释放页外 Markdown options 与 code hook，避免访问过的每条消息都长期保存控件配置；入口见 [page_chat.go](../desktop/page_chat.go)。
- 关闭 tab、删除附件、模型、任务或队列元素时使用 `slices.Delete` 或显式清空尾槽，避免缩短切片后底层数组仍保留已删除大对象。
- 对话删除确认、任务日志弹窗等的按钮、Escape 和背景关闭路径均清理目标对象或大正文，不能只在“关闭”按钮里释放；入口见 [page_chat.go](../desktop/page_chat.go) 与 [page_tasks.go](../desktop/page_tasks.go)。
- 图片生成结果丢弃时释放旧预览图引用，语音模型重新加载时清理旧展示行；入口见 [page_image_playground.go](../desktop/page_image_playground.go) 与 [voice_models_io.go](../desktop/voice_models_io.go)。
- 语音队列有界且消费/丢弃时清空旧槽位，仅多项时排序，避免已消费录音仍通过底层数组常驻；入口见 [voice_takes.go](../desktop/voice_takes.go)。
- 会话列表只展示前 80 项时复制所需前缀再缓存，不用短切片继续引用全部排序结果；权威会话索引仍保留全部记录；入口见 [rail.go](../desktop/rail.go)。
- 模型列表按展示与搜索字段缓存筛选索引及供应商顺序，只绘制当前供应商，不每帧复制完整配置或给未显示供应商分组；入口见 [page_models.go](../desktop/page_models.go)。
- 技能列表按同一份扫描结果缓存排序索引、来源分组、数量和标签，启停等当前状态仍读权威数据，不每帧排序并复制完整定义；入口见 [page_skills.go](../desktop/page_skills.go)。
- 统计环形图按数据变化缓存标签、占比与扇区数据，Path 缓存键包含圆心、半径和扇区，悬停或布局变化时更新几何；入口见 [page_stats.go](../desktop/page_stats.go)。
- Go 内存软限额为 320 MiB，作用于 Go 管理内存且不是硬上限，不包含 WebView、原生纹理等内存；入口见 [memory_tidy.go](../desktop/memory_tidy.go)。
- 隐藏或最小化约 5 秒后才调度闲置 Go 堆释放，两次释放至少间隔 2 分钟，恢复窗口使排队回调失效，避免频繁 GC 与重新缺页。
- `FreeOSMemory` 只用于窗口不可见时的受控整理，不放进渲染或每条消息更新路径，不靠清空活跃内容或销毁保留预览降低工作集。
- 小尺寸动效保持统一时长、减少动态效果与像素取整约束，不通过盲目提高帧率解决跳动，状态三线动画请求 30 FPS；规范见 [uikit/README.md](../desktop/uikit/README.md)。

## 已测收益与验证方式

- 2026-11-07 的一次性基准运行于 Windows / i5-12600KF，以下数值描述构造样例的索引、帧或保留堆成本，不代表整个进程占用或用户真实全天操作结果。
- 11 万条消息（5000 轮、每轮 20 个 read）追加索引：参考全历史归属扫描约 8.38 ms、432 B/op、3 allocs/op，增量约 813 ns、336 B/op、2 allocs/op。
- 持续增长的 1 万条工具组主对话帧：该轮中间基线约 2.98 ms、975200 B/op、20367 allocs/op，优化后约 0.24–0.29 ms、14 KB/op、353–368 allocs/op；基线已含增量历史优化。
- 稳定的未闭合约 90 KB 代码围栏帧：约 98840 B/op → 536 B/op，耗时约 1.346 ms → 1.322 ms，主要收益是分配减少。
- 约 256 KB 压缩摘要分 500 段流式追加：约 9.57 ms、65.9 MB/op、1007 allocs/op → 0.177 ms、1.19 MB/op、533 allocs/op。
- 200 次各 256 KB 的 read UI 消息，保留 fixture 并在前后 GC 后测堆：约 52949688 B → 458352 B，正文仍可存在于模型运行上下文中。
- 性能比较同时记录 ns/op、B/op、allocs/op，必要时查看 CPU/heap profile，不能只凭任务管理器瞬时工作集判断是否泄漏。
- 性能探针和基准文件用完删除，生成物留在忽略目录，保留的是必要行为回归，不为一次性能实验长期增加测试文件。
- 主对话回归入口为 [transcript_rows_test.go](../desktop/transcript_rows_test.go)、[command_group_test.go](../desktop/command_group_test.go)、[tool_result_memory_test.go](../desktop/tool_result_memory_test.go)、[tool_render_memo_test.go](../desktop/tool_render_memo_test.go)、[markdown_theme_test.go](../desktop/markdown_theme_test.go) 和 [chat_search_test.go](../desktop/chat_search_test.go)。
- 预览与工作区回归入口为 [workspace_preview_retention_test.go](../desktop/workspace_preview_retention_test.go)、[workspace_preview_lifecycle_test.go](../desktop/workspace_preview_lifecycle_test.go)、[preview_failure_test.go](../desktop/preview_failure_test.go)、[explorer_state_test.go](../desktop/explorer_state_test.go) 和 [workspace_history_test.go](../desktop/workspace_history_test.go)。
- 后端落盘和协议回归入口为 [prov_protocol_regression_test.go](../internal/app/prov_protocol_regression_test.go)，须确认调用配对、显式压缩摘录、旧受保护历史恢复及运行中原始证据都保留正确行为。
- 新功能至少核对稳定帧、追加、就地修改、折叠/展开、翻页、删除后同数量补回、压缩成功/失败、切 tab、迟到回调和关闭路径中受影响的情况。
- 代码变更先跑对应行为回归，再执行 `CGO_ENABLED=0 go test -p 1 ./...`、`go tool mygo vet .` 和 `python scripts/build.py dev`，测试文件操作使用 `t.TempDir`，全套不设置全局 `ALLY_DATA_DIR`。
- 当前未发现同范围内其他有明确收益且能低风险实施的瓶颈；模型所需的运行上下文、原生文字布局和绘制仍有必要成本，后续优化以测量证据为依据，不承诺理论上零优化空间。
