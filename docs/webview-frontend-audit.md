# Windows / macOS 桌面前端评估

评估日期：2026-10-20。基于当前代码及本次修整后的生产构建。

## 结论与范围

存在不删减功能的改进空间，优先级是异步结果归属、重型依赖加载、后台渲染及多 Tab 的驻留成本。没有证据支持为了省内存关闭 GPU、强制频繁 GC 或替换 Naive UI。

Windows 使用 WebView2，macOS 使用 WKWebView，不是同一个引擎。两者需分别验收；本次完成源码评估、前端回归、Vite 生产构建，以及 Windows Edge 中的生产组件验收和 DevTools/CDP 渲染对照；没有进行 Windows/macOS 桌面进程内存采样、WKWebView 实机或锁屏恢复测试，不能据此承诺整机内存下降多少或断言所有功能已通过跨平台端到端验收。

检查入口覆盖 `main.go`、桌面启动/关闭与事件桥、会话释放和流式节流；前端检查 App、49 个 components 目录下的 Vue 文件的绑定/样式引用候选、监听器/定时器/Observer、缓存及 Blob 生命周期，并重点阅读聊天、任务日志、编辑器、HTML、主题、插件的实现。使用 Naive UI、Vue 3、Wails v3 技能，具体 API 以安装的依赖源码为准。不是对所有后端包逐行审计。

## 本次完成的修整

| 问题 | 修改及作用 |
|---|---|
| 服务 A 的日志请求在打开服务 B、计划任务日志或关闭重开后返回，会覆盖新窗口内容；过期 finally 还会清掉新请求的 loading | `TaskCenterPanel.vue` 以请求版本隔离结果、错误和 finally；打开、关闭、卸载均使旧请求失效 |
| 慢桥接请求期间每 1.5 秒叠加一次日志请求；窗口未关闭但切换日志对象时轮询归属不完整 | 同一日志只允许一个请求在飞；轮询同时跟踪可见性、服务 ID 和运行状态；隐藏窗口跳过自动获取，恢复后下个轮询周期继续；手动刷新保留 |
| Tooltip 排除规则被同组的 `.n-popover` 无条件选择器绕过 | 删除这个泛用选择器，保留 `.n-popover-shared:not(.n-tooltip)`；Tooltip 不再被强加 backdrop blur，其他浮层保留现有效果 |
| 未使用的 Notification Provider | 删除包裹层；子组件仍依赖的 Message/Dialog Provider 保留，App 自己使用的 discrete API 保留 |
| discrete API 创建独立 Vue 应用、全局错误监听器没有对应卸载 | 增加 discrete `unmount()` 和错误监听器解绑，防止根组件重挂载/HMR 遗留。单次正常启动本来只创建一份，不能把它夸大为持续增长的生产泄漏 |
| 初始隐藏状态仅在下一次 visibilitychange 才同步 | 挂载时立即同步 `app-hidden`，沿用既有动画暂停机制 |
| 计划面板用 max-height 动画，下载进度条用 width 动画 | 计划面板保留 opacity/transform 进退场；进度条改为 scaleX，保留进度值及过渡。计划面板周边布局现在直接到位，不再逐帧撑开 |
| HTML 卡静态导入把完整 ECharts 字符串带入首屏 | `ChatMessages.vue` 按需加载 HtmlRenderCard；首次遇到 HTML 卡才加载，离线导出和沙箱图表支持保留 |
| 失去模板引用的 scoped 样式 | 删除 MCP 页的 `panel-header-copy`、`config-inline-subtitle`，模型页的 `config-section-header/title/subtitle`，设置页的 `sandbox-status-alert`、`settings-page-actions`、`settings-opt-badge`，共 8 组 |

未因“本文件搜不到”而删除跨组件的 `:deep(.stats-inner)`、状态色 class、平台 class、Naive 内部 class、HTML 动态生成的 class 或 Vue Transition 自动 class。未发现仅凭单次名字出现次数就能安全批量删除的 JS 顶层绑定。

## 首轮按需加载的构建证据

同一工作区、同一依赖，HTML 卡分块前后 Vite 输出：

| 文件 | 分块前 | 分块后 |
|---|---:|---:|
| 入口 index JS | 3,135.84 kB | 2,006.22 kB |
| 入口 index JS gzip | 1,003.10 kB | 630.58 kB |
| 按需 HtmlRenderCard JS | 包含在入口 | 1,129.87 kB（gzip 373.11 kB） |

入口 JS 体积减少约 36%，不是整个应用包体积减少 36%，也不是实测内存减少 36%。首次展示 HTML 时仍需加载这份代码；恢复的会话首屏本来就有 HTML 卡时会立即加载。入口仍超过 1,200 kB，Vite 的大 chunk 警告依然存在。

追加优化时入口为 2,010.21 kB（gzip 631.83 kB）。后续复核修复后的入口为 2,010.46 kB（gzip 631.91 kB），没有继续拆分页面。

## 现有实现中应保留的部分

- Naive 按需组件解析、统一 ConfigProvider、深浅主题及中文/英文 locale 的接线成立；`naiveTheme.mjs` 从 CSS 取实色供 Naive 做颜色计算，避免向其颜色算法传入 `var()`。这些不属于死代码。
- 后端流式节流与前端工具更新合并已有统一入口；工具大 payload 会降低刷新频率。不要再在另一层散落一组竞争的计时常量。
- 聊天消息有显示上限、离屏 content-visibility；非活动且非打开 Tab 的会话会卸载消息。附件有预览释放路径，Mermaid 有条数/字符预算及脱离 DOM 节点清理。
- 编辑器已按需分块，Ace 有 destroy、ResizeObserver 有 disconnect，工作区加载已有请求失效控制；语言 mode 按需导入。不能把这些简单改成“每次切换都重建”。
- 后台服务日志已有 rolling buffer，不能把前端轮询优化误报成修复了后端无限日志。
- Windows 当前采用 HWND 宿主并已有最小化恢复后的 zoom 校正，注释明确记录 DirectComposition 设备丢失风险。不要仅为 RDP 性能打开 UseVisualHosting。

## 用户确认后追加实施的优化

本轮范围排除进一步页面拆分、Windows 后台保活参数调整和隐藏 Tab DOM 回收，其他方案已落实：

| 优化 | 实现 |
|---|---|
| 隐藏 Tab 暂停展示 | App 向 ChatMessages 传活动状态；Markdown 停止 rAF/解析，通用工具卡保持浅层展示快照，等待/子代理时长停止更新，贴底与正文 overflow 测量跳过。恢复时读取最新数据，包含隐藏期间收到的终态。后台事件接收、任务、落盘、草稿和 DOM 保留不变；没有尝试冻结任意 HTML 脚本 |
| 共享展示时钟 | `displayClock.mjs` 只维护一个 timeout，按订阅者的 250ms/1000ms 周期发布；没有订阅者或窗口不可见时停止。`presentation.mjs` 负责可见性监听的引用计数及作用域释放。等待、子代理与压缩耗时共用，恢复后从绝对时间重算；压缩不再每秒改写所有会话的 elapsedText |
| 单 iframe 全屏 | 同一个 iframe 原地切换 fixed 布局，不移动 DOM、不 Teleport、不重设 srcdoc。仅当前全屏卡片解除 paint containment，维持原位置占位；保留图表状态、焦点恢复，支持沙箱内 Esc，切换工作区关闭全屏、最小化保留全屏状态。只在全屏期间注册键盘监听，并保证同时只有一个 HTML 全屏页 |
| 共享图表脚本 | iframe 的 srcdoc 只引用构建后的同一本地 ECharts 资产；严格比较协议/host，兼容 Windows HTTP 与 macOS 注册的 `wails://` 协议。不开放远程 CDN 或 allow-same-origin，离线导出继续完整内联。为兼容任意现有 HTML，仍提供全局 echarts，不通过搜索文本猜测是否需要图表库；不同 iframe 的图表实例与 JS 上下文仍独立 |
| HTML 渲染生命周期 | 图表 resize 跟踪改为 Set，避免重复 init 重复登记，dispose 时从集合移除；高度 Observer 回调合并到一帧，避免同一批 DOM 修改反复同步测量 |
| 动画 | 思考文字改为静态渐变高光层的 opacity 呼吸；自有 CSS 不再 transition 颜色、背景、阴影、尺寸或 all，只保留 transform/opacity。状态色与悬停反馈仍在，部分颜色变化变为立即切换；统计条淡入保留、隐藏立即生效，樱花保留 opacity 淡出后卸载 |
| Naive 主题 | Button/Checkbox/Radio/Switch 迁移到公开 themeOverrides；标题 Tabs 与输入框使用局部公开主题配置。删除 `--n-* !important` 覆盖，CSS 仍是唯一色板，传入颜色算法的调色板 mix 先解析为实色。输入框没有公开 colorHover 字段，其 hover 表面只用普通 CSS，不再写无效的 `--n-color-hover` |
| Vue 事件校验 | 修复 CodeView / TerminalOutputView 的 `overflow: Boolean`：它会把合法 false 当成校验失败；改用明确的布尔类型校验 |

## 生产组件验收记录（后续复核前）

通过临时组件验收页生产构建，使用 Playwright 驱动 Windows Edge，数据均为构造数据，没有连接真实会话：

- 在真实 ChatMessages / Naive Scrollbar 层级内连续三次全屏往返：iframe 数量始终 1、DOM 节点身份不变；1280×720 视口中 iframe 位于 x=44/y=82，尺寸 1236×638，未被消息卡或滚动容器裁剪；图表与脚本计数状态保留。
- 隐藏期间替换助手正文、结束流式、替换工具结果：Markdown 渲染次数与展示时钟不增加，工具卡保留旧展示；恢复后正文和工具终态追平。DOM 与原会话消息仍在。
- 5 种主题 × 深浅两种模式：按钮、复选框、单选框、开关主色一致，标题 Tab 间距及输入框表面主题配置生效；单测另检验文字对比度及无未解析的颜色混合。
- 沙箱内按 Esc 正确退出全屏，iframe 未被销毁。约 3.8 KB 的示例 srcdoc 可通过共享资产正常创建图表；导出的约 1.12 MB HTML 不包含外部 script src，能够独立执行图表。
- 用 DevTools/CDP 的 devtools.timeline、LayerTree，在同一生产验收页、暂停其他展示更新后，对相同文字做 1.8 秒对照：复现旧 background-position 声明时 Paint=181、UpdateLayoutTree=181；新的 opacity 高光 Paint=0、UpdateLayoutTree=0，两者 Layout=0。该结果只证明这条动画消除了逐帧重绘，不代表整机 CPU/内存或所有页面的总收益。

## 仍保留的优化与验证

| 项目 | 保留原因与下一步 |
|---|---|
| Windows 的三条禁止后台节流参数 | 用户明确排除本轮。配置有历史白屏兼容原因，后续需逐条对照最小化、锁屏、睡眠/显示器唤醒和显卡恢复，并证明后台 run/落盘/终态不丢失 |
| 隐藏 Tab 的 DOM 冷却回收 | 用户明确排除本轮。消息节点仍保留原生查找、选取、滚动、Mermaid 与编辑器状态；只暂停已列出的展示工作 |
| 进一步拆分设置/模型/统计等低频页面 | 用户明确排除本轮。首轮已有的 HTML 卡按需加载保留；入口仍有大 chunk 警告，不通过调高告警阈值掩盖 |
| macOS 原生及长期运行验收 | `wails://` 地址解析已由单测覆盖，WKWebView 的实际资源加载/脚本 CSP、IME、缩放、锁屏恢复及长期内存曲线仍需 macOS 实机，不能用 Windows Edge 检查替代 |

## 用户列项复核与快速修复

- ECharts 双份载入成立。前一轮漏算了 HTML 卡中的 raw 副本：当时 HTML 卡 JS 为 1,131.62 kB，另有 1,121.88 kB 独立脚本，首次展示合计约 2.25 MB。现已删除 raw 导入，HTML 卡 JS 为 **10.65 kB**，只保留 **1,121.88 kB** 独立资产；首张 HTML 卡的这两项合计约 **1.13 MB**。导出时才读取同一资产的源码、缓存一次并内联到离线文件，失败可重试。此处是构建体积，不是压缩传输量或内存测量。
- 文档构建抛错路径成立。HTML 卡现在捕获错误，在卡内显示原因与原始内容文本，避免异常逃逸；导出读取失败也在卡内反馈。
- 日志重复开关的请求重叠成立。增加独立的按服务 pending 表；同一服务的旧请求结束后，仅最新版本的打开操作发出一次新请求，UI loading 不再是唯一并发约束。
- 共享时钟订阅者异常导致停摆成立。逐个隔离回调异常并报告，其他订阅者、后续 tick、恢复可见时的发布均继续。
- CSP 测试缺口成立。新增断言检查 script-src 明确放行 iframe 实际引用的脚本地址，防止字符串替换失配时测试仍通过。
- “primary dashed 变实心”不成立：项目安装的 Naive Button 在 ghost/dashed 分支将背景明确设为 `#0000`，与此前浏览器的实测一致，两个按钮保持原样。
- alpha 的输入格式限制属未来风险，当前调色板均为六位 hex，没有触发 NaN，按用户要求不扩展实现。

新增回归覆盖重复开关十次的请求合并、时钟异常隔离、共享资产导出读取/失败重试、卡内错误与恢复及精确 CSP 放行。上述快速修复已通过 330 项前端测试和生产构建；新的导出读取路径尚未在 macOS 原生 WKWebView 实测。

## 验证及实机验收口径

- `cd frontend && npm test`：330 项通过，包含首轮新增 5 项执行实际组件 setup 的日志乱序、关闭重开、轮询重叠/隐藏及卸载测试。日志测试通过可控桌面桥 Promise 和计时器验证状态归属；另覆盖共享时钟、Windows/macOS 资源地址、图表 dispose 释放、跨主题控件配色。这些 Node 测试不替代原生 WebView 验收。
- `cd frontend && npm run build`：通过，存在上述大 chunk 警告。
- `git diff --check`：通过；本次未改 Go 或绑定，没有运行 `wails3 build` 或后端全套测试。
- 已完成上述生产组件浏览器检查与渲染对照；未做整个 Ally 桌面系统验收、长时内存采样或 WKWebView 实机测试。未将构建成功等同于跨平台稳定性保证。

建议固定相同会话、模型输出和生产包，对 Windows/macOS 各跑：冷启动；1/5/10 个工作区；连续流式与取消；多张 HTML 卡及全屏往返；大文件编辑后关闭；最小化/锁屏/唤醒；反复关闭 Tab。记录 Go 进程、WebView 内容进程和 GPU 进程的内存、JS heap、DOM/监听器数量和 CPU。关注关闭后驻留量是否逐轮上升，而不是要求 OS 工作集立即回落；浏览器缓存和分配器保留不等于对象泄漏。后台 Agent 必须继续运行、输出和历史不能丢失。

官方参考：[Wails 平台引擎示例](https://pkg.go.dev/github.com/wailsapp/wails/v3/examples/webview-api-check)、[Microsoft WebView2 性能指南](https://learn.microsoft.com/en-us/microsoft-edge/webview2/concepts/performance)。后者建议关注最新 Runtime 和应用自身资源使用；具体框架选项需以项目锁定的 Wails beta.15 源码为准。
