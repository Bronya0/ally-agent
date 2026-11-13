# LESSONS — Ally Native (mygo)

一行一条：`[tag] YYYY-MM-DD <高危|中危> 触发条件 → 后果/正解。@文件`，整行 120 字符内。

[mygo-anim] 2026-10-28 高危 循环动画用 Element.Loop：每帧重建整个界面（60帧57次）。改 Draw+AnimationFrameRate。@desktop

[mygo-petals] 2026-10-28 高危 Draw 里再调 AnimationFrame 等于没改，装饰层仍每帧重建整窗。@desktop/rail.go

[mygo-avatar] 2026-10-28 中危 逐帧动画用 c.After 推进也是每秒十几次整窗重建；位图改 Draw+p.After。@desktop/native_assets.go

[mygo-popover] 2026-10-28 中危 弹层有高度上限时内容把按钮挤出框外甚至压成0高；明细 Scroll，按钮 Shrink(0)。@desktop/composer.go

[mygo-view-alloc] 2026-10-28 中危 视图里每帧调 Buffer.String 取全文：有动画时等于每帧复制整个文件。按 Version() 缓存。@desktop/page_explorer.go

[mygo-toolcard-json] 2026-10-28 低危 展开的工具卡每帧 json.MarshalIndent 一遍结果（大结果约0.5ms/帧）；暂留，真卡再加缓存。@desktop/tool_cards.go

[mygo-select] 2026-10-28 中危 取回的列表只写进 state 不给控件当选项，用户看到“拉取成功”却无处可选，以为下拉坏了。@desktop/page_models.go

[mygo-rightclick] 2026-10-28 高危 窗口根 Selectable() 会抢走全部右键，控件 RightClicked 永不触发；局部右键菜单的容器要 Unselectable()。@desktop/page_explorer.go

[mygo-compact-ui] 2026-10-28 高危 后端改写历史（压缩）时前端不跟着换消息列表：transcript 与底部占用跟真实历史分叉，还照旧落盘。@desktop/compact.go

[mygo-tool-verb] 2026-10-28 中危 toolActionVerb 未登记名字的兜底返回 "Called MCP"，read/grep 不折叠就全被叫成 MCP 调用。@desktop/tool_details.go

[mygo-selected] 2026-10-28 中危 flat()/iconButton 之后改 TextColor 无效（标签、图标已画）：选中态与主题色全失效。@desktop/widgets.go

[mygo-imgsize] 2026-10-28 中危 只按字节上限决定图片直传：PNG 截图几 kB 却有 3000px，模型白付 token；按像素边重编码，透明图先铺白。@desktop/attachments.go

[mygo-ink] 2026-10-29 中危 Theme.TextMuted 只给框架画占位；次要文字必须传 uikit.SecondaryInk，否则两者一起变淡。@desktop/theme.go

[mygo-crlf] 2026-10-29 中危 Python 脚本写 Go 文件默认转 CRLF，gofmt 会把整文件报未格式化；写时用 newline=""。@desktop

[mygo-restore] 2026-10-29 中危 从外部灌入历史后没设 FollowPending：Y=0 就是开头，恢复会话停在最旧消息。@desktop/page_chat.go

[mygo-wheel] 2026-10-29 中危 ScrollHorizontal 只吃 Shift+滚轮，纯横向容器普通滚轮无效；TrackScroll 后把 DY 转成 X。@desktop/widgets.go

[mygo-interactive] 2026-10-29 中危 Dragged() 会让元素变 interactive，放在 DragWindow 区会抢走窗口拖拽，只在按钮上采集。@desktop/uikit

[mygo-caret] 2026-10-29 中危 ButtonBase.Label 不画字、flat/cardFlat 画；同一文字再 ui.Text 一遍即“详情 详情 ⌄”，带箭头只用 disclosure()。@desktop/widgets.go

[mygo-paste] 2026-10-29 中危 运行中拦下附件粘贴/拖拽且不提示：用户以为输入框坏了；pending 附件属下一轮，应允许添加。@desktop/attachments.go

[mygo-hover-menu] 2026-10-30 中危 悬停才渲染的行里挂 popover：指针移到浮层行失焦、控件被删，菜单跟着消失点不到；改常驻+Opacity/Disabled。@desktop/page_chat.go

[mygo-menu-clip] 2026-10-30 中危 原生菜单末字裁切：应用菜单统一走 uikit.ContextMenu，测长中文和快捷键。@desktop/uikit/menu.go

[session-roots] 2026-10-30 高危 附加目录只改界面会被新轮配置清空；发布后显示并随 chatConfig 携带，每轮刷新授权。@desktop/composer.go

[ssh-auth-id] 2026-10-30 高危 SSH 授权草稿按节点 ID 为键，加载完成且工作区匹配才提交，失败回滚。@desktop/page_ssh.go

[mygo-render-bind] 2026-10-31 中危 渲染期重写输入框绑定变量会抹掉当帧换行，像按键失灵；只在存盘归一。@desktop/page_settings.go

[memory-render-json] 2026-10-30 高危 每帧解析工具结果并复制封装，64KB 单次约 132KB；入口解析一次，渲染只读结构体。@desktop/tool_cards.go

[preview-document-life] 2026-10-30 高危 预览销毁后 LoadHTML 仍留大正文；改可撤销协议 URL，换页/销毁删正文。@desktop/preview_documents.go

[toast-wall-time] 2026-10-30 中危 Toast 失焦/悬停暂停；全局提示按实际时间截止，过期队列不重播。@desktop/notification_deadlines.go

[mermaid-dark-export] 2026-10-30 中危 内置 dark 主题偏灰，改 base 加显式彩色变量；SVG 从已渲染 DOM 导出。@desktop/mermaid_preview.go

[display-bound] 2026-10-30 中危 suggest/ask 展示文案用 maxLength 硬校验，超两字整次失败丢 chips；改为裁剪。@internal/app/app.go

[tool-render-memo] 2026-10-30 中危 参数与 diff 派生行按控件局部状态复用，不做全局无界缓存；卡展开/收起须重置展开量。@desktop/tool_render_memo.go

[compact-token-basis] 2026-10-30 高危 本地裁剪使实测锚点失效，前后须同一估算口径；liveBreakdown 要同步重建。@internal/app/biz_compact_fast.go

[action-intrinsic-width] 2026-10-30 中危 Column 默认 stretch 会拉满未设宽按钮；actionParts 默认 AlignSelf(Start)。@desktop/uikit/semantic.go

[render-small-allocations] 2026-10-30 中危 字段名表收口静态表，字段值每帧只格式化一次；累计分配不等于存活堆。@desktop/tool_resources.go

[chat-built-range] 2026-11-13 中危 帧内先重置 st.built 再让搜索揭示/提示跳转读它，它们永远当行没建，跳转卡死。@desktop/page_chat.go

[tooltip-anchor-width] 2026-10-30 中危 Column 里 Text 可被拉成整行，Tooltip 按锚点定位会飘到页面中央；说明标签用 AlignSelf(Start)。@desktop/page_settings.go

[py-stdin-src] 2026-11-01 中危 把含中文的 py 脚本经 stdin 喂 python3.9：按块解码，中文跨块即报 Non-UTF-8；首行补 coding cookie。@internal/app

[transcript-row-owner] 2026-11-01 中危 谁画这条消息只能由 transcriptRows 一处定；渲染与分页各自判一次，空行就会照旧吃列间距。@desktop/transcript_rows.go

[render-parse] 2026-11-01 高危 渲染期每帧重新解析工具参数/结果，被帧数放大成 GC 压力；派生值挂 ui.Local memo，读同一份 JSON。@desktop/tool_cards.go

[deref-copy] 2026-11-01 中危 cloneMap 后 resolveNode 无条件重建全树，无 $ref 时白拷一遍；按 hasAnyRef 跳过。@internal/tools/schemautil

[schema-map-shared] 2026-11-01 高危 schemautil.Map 的返回值会被 responses 适配器写入 strict/description，禁止返回共享的冻结清单。@internal/app/prov_adapter_responses.go

[collapse-alloc] 2026-11-01 中危 折叠态仍分配行结构与派生缓存，长对话里每组常驻；折叠即释放。@desktop/tool_cards.go

[composer-reasoning] 2026-11-02 中危 输入框改的思考强度只写 Tab：会话快照无模型字段、重启从模型配置读→静默回退；改模型条目并保存。@desktop/composer.go

[menu-tree] 2026-11-02 中危 悬停浮层别改成点击折叠（用户要悬停）；一帧只建一个浮层+粘滞，行 hover 与池层 hover 分开判。@desktop/composer.go

[review-verify] 2026-11-02 中危 转述审查结论前先查后端归一化（空串已改 auto），否则误报已修的事。@internal/app/biz_config_state.go

[log-scroll-track] 2026-11-03 中危 输出窗口未 TrackScroll，“回到最新”只重取数据不改位置，看着像按钮坏了。@desktop/page_tasks.go

[stream-fixed-height] 2026-11-03 中危 流式态用固定高度 Box 包会自收缩的内容：短内容卡片偏高，完成时下方内容被上顶。@desktop/tool_cards.go

2026-11-02 中危 image-restore 在途结果丢弃须记补载；所有唤回统一 OnShow，不能只判缓存 nil。@desktop/attachments.go

2026-11-02 中危 thinking-phase 思考计数只在整轮清零会跨段累加；工具/正文结束即清零，正文与思考同包须合并发出。@desktop/events.go

[preview-loadurl] 2026-11-03 中危 文件预览走异步 LoadURL+协议处理器比 NavigateToString 慢，切换像卡死。@desktop/preview_lifecycle.go

[mono-generic-font] 2026-11-03 高危 字体用泛型 "" 或 "monospace"：中文每段重做字体匹配，79KB md 首屏 720ms。统一用 appFontFamily。@desktop/theme.go

[asr-route] 2026-11-03 中危 百炼 ASR 路由随模型而非模式：qwen3-asr→chat、qwen-audio/fun-asr→multimodal，音频只收 data URI。@internal/app/biz_voice.go

[access-tree-frames] 2026-11-03 中危 UIA 碰过一次窗口后 mygo 每帧新 make 约 160KB 节点数组，占当天分配 45%；只重绘的帧不建。@mygo ui/access.go

[access-tree-upstream] 2026-11-03 中危 那行 make 在 mygo main 仍未修；复用缓冲区可归零，但 darwin 的 s.access=*tree 留着同一 slice，须双缓冲。@go.mod

[history-memo] 2026-11-03 中危 历史块起点缓存须在“行由空变非空”时递增修订号，否则流式首 token 不进窗口（条数没变）。@desktop/transcript_rows.go

[hover-animate] 2026-11-03 中危 悬停淡入淡出别用 Animate：整帧重建并建无障碍树；改 HoverFade，减动效退回 Opacity。@desktop/uikit/motion.go

[fmt-map-render] 2026-11-03 中危 渲染期 fmt.Sprint(map) 先排序全部键再拼串，每帧一遍；先判类型取字段。@desktop/tool_details.go

[widget-key] 2026-11-03 中危 每行 key 用 Sprintf 每帧解析格式串；统一 widgetKey，输出须逐字节不变否则元素状态重置。@desktop/widgets.go

[request-json-thrice] 2026-11-03 中危 一次请求把同一份历史编三次：SDK 编码、传输层重写、工具 schema 哈希（可缓存）。@internal/app/prov_reasoning.go

[stats-full-rewrite] 2026-11-03 中危 统计每 5 秒把当天整份日文件重写一遍（marshal + 临时文件改名），记录越多越贵。@internal/app/biz_stats.go

[voice-ui-copy-leak] 2026-11-03 高危 弹窗占位文案从业务返回值拼，听写路径会写进输入框；返回值只给纯正文。@internal/app/biz_voice.go

[voice-defaults-dupe] 2026-11-03 中危 前后端各写一份字段默认值，与协议默认冲突会让合法配置导入被拒。@desktop/voice_models_io.go

[settings-prefs-dirty] 2026-11-03 中危 设置页 dirty check 只比 ConfigState，存 prefs 的字段离开页面即丢。@desktop/page_settings.go

[memory-tidy] 2026-11-04 中危 别在每帧或定时器里调 FreeOSMemory：STW 加缺页抖动；只低频用户动作触发，1 分钟节流。@desktop/memory_tidy.go

[md-options-maps] 2026-11-06 高危 给会话加并列 map 只随 MD 初始化就写：新建会话已建 MD，nil map 写入首帧 panic 崩进程。@desktop/page_chat.go

2026-11-05 高危 blackboard-history 收信只进请求副本就 Ack，下一步丢协作约束；历史与确认须一起提交。@internal/app/orch_blackboard.go
2026-11-05 高危 blackboard-report 消息标签剥离后报告为空仍标完成，父代理就拿到空结论；完成须有报告。@internal/app/orch_subagent.go
2026-11-05 高危 blackboard-lexer 行作用域状态(反引号/注释/伪标签)跨行续用会静默吞掉后续消息；无闭合标记即复位。@internal/blackboard/protocol.go
2026-11-05 中危 stream-builder 消息存 Builder.String() 后勿 Reset 复用，旧文字会被就地改写。@desktop/events.go
2026-11-05 中危 IconButton 的 size 同时决定方块和图标(0.62)，补命中区必放大图标；用 IconButtonSized。@desktop/uikit
2026-11-07 中危 menu-reentry MyGo 0.8.1 子菜单比上次高亮坐标，Esc 后同点重入漏展开；暂用局部兼容层。@desktop/uikit
2026-11-07 中危 menu-lock MenuBase 在样式前判 Disabled；必须按触发器最终禁用态关菜单，否则运行中仍能选模型。@desktop/uikit
2026-11-07 中危 preview-reuse 换页须换 token，事件和异步命令都校验；Reset 清旧正文，否则误操作新文件。@desktop/workspace_media.go
2026-11-07 高危 plugin-retire 异步卸载须返回 Promise；旧存储写回与安装/删除共锁并验版本，否则覆盖新数据。@desktop/pluginhost
2026-11-07 中危 timer-dispatch Stop 撤不回已排队的 UI 回调；须验 generation，否则旧回调清掉新计时器并提前释放。@desktop/memory_tidy.go
2026-11-07 中危 chat-wheel 滚轮缓动首帧位置未变，按贴底恢复 Follow 会取消滚动；只在实际下移到底恢复。@desktop/page_chat.go
2026-11-07 中危 compact-outcome 裁剪重建工具卡时硬写 success 会把失败或取消伪装成成功；复用状态判定并保留结果元数据。@desktop/compact.go
2026-11-09 中危 ui-enum-alias 下拉选项写别名或原始值而非后端规范值：编辑已有配置时字段消失、界面露出内部串。选项与判断统一用后端导出的常量。@desktop
2026-11-11 中危 stdlib log.Printf 不进 error-*.log（GUI 无 stderr），排障易误判有记录；诊断走 logAppError。@internal/app/infra_log.go
2026-11-12 中危 preview-copy-size 待发图只有 256px 芯片，预览拿缓存尺寸判断清晰度就永不补解码；按源解码，别放大。@desktop/image_preview.go
2026-11-13 中危 hold-place 折叠保持位置要量元素真实 Y，滚动偏移变化不代表内容移动；st.Follow 须同步置 false。@desktop/page_chat.go
2026-11-13 中危 follow-resume 只认滚轮意图：拖滚动条不恢复，流式增长又让滚轮永远到不了新底部；按实际下移或滚轮当时看到的底部恢复并补滚。@desktop/page_chat.go
2026-11-13 中危 outline-children Outline 每帧问所有可见行的 children，在里读盘会预读未展开的目录；按 Open 集合驱动。@desktop/page_explorer.go
2026-11-13 中危 outline-style Outline 行自带上下边距、8px 左距和 accent 选中填充；自绘缩进与箭头并用面板色盖住，否则整棵树变样。@desktop/page_explorer.go
