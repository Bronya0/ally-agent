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

[history-window] 2026-10-30 中危 页大小存 native-ui.json，默认 80、范围 20–200；分页、工具分组与跳转共用同一窗口值。@desktop/page_chat.go

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
