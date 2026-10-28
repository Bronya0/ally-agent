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

