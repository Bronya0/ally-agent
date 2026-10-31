# Native UI kit

Independent native MyGo v0.7.1 controls. Copy this directory into another Go
project and update its import path; it depends only on MyGo's `ui` package.
It contains no Ally state, filesystem access, goroutines, fonts or business logic.
Use the host's `ui.Theme` to supply typography and semantic colors.

```go
import "ally-dev/desktop/uikit"

func view(c *ui.Context) {
    uikit.Select(c.Key("auth"), &state.Auth, []string{"API Key", "OAuth"}).Label("Authentication")
    uikit.Tabs(c.Key("tabs"), &state.Tab, "General", "Advanced")
    uikit.Card(c.Key("account")).Children(func() {
        uikit.FieldLabel(c, "Name")
        uikit.TextField(c.Key("name"), &state.Name, "Example account")
    })
}
```

Exports. Form controls: Button, IconButton, Select, CompactSelect, editable Combobox, Tabs, Segmented,
PasswordInput, NumberInput, TextField, TextArea, FieldLabel, FieldNote. Chrome: Card,
RowCard, Divider, PageHeader, SectionHeader, EmptyState, BusyRow, Notice, InlineError,
StatCard, KV, Tag, Label, Muted, SectionTitle, HoverRow, SelectableRow. Dialogs: DialogPanel,
Popup, PopupTransition.

Button takes a ButtonVariant (secondary, primary, danger, ghost) and returns its label
element too, for the few call sites whose label colour is not the button's own.
IconButton takes an IconTone and a *ui.SVG: the kit does not know an app's icon table,
so the caller resolves names to images. WarningInk and FaintInk fill the two tones
ui.Theme does not carry.

ControlHeight, Radius, the Font and Gap scales and the RadiusCard / RadiusSmall pair
are shared sizing tokens; components take padding and radius as arguments rather than
baking one page's spacing into them. Choice rows use a subtle accent fill plus a check
for selection; hover and keyboard highlight use the surface hover color. CompactSelect
uses the same popup with a quiet 28-DIP toolbar trigger; its locked argument prevents
opening or changing the value without dimming or moving the trigger. Tabs set
pointer cursors on each interactive item and label.

TabUnderline、TabInk、TabRadius 保留为旧接口；当前工作区标签使用 WorkspaceTab，页面内 Tabs 使用 tabs.go 的两套独立样式。

Values and focus belong to the host. Set `c.Key` before constructing dynamic
controls. Never retain returned Elements/Parts between build passes. Attach
`Changed`/`OnClick` as with MyGo controls. Use `DialogPanel` inside DialogBase;
its transition animates entry and removal, respects reduced motion, and does
not animate layout dimensions. Popup uses the same transition and chrome.

The Ally desktop's widgets.go is the compatibility layer for old call sites.
New projects should import this package directly.

`Input`, `Area` and `InputChrome` share visible borders, hover and focus colors.
For side-by-side fields, use fractional grid tracks and stretch the cell's
Column with `JustifySelf(ui.Stretch)`; put `FillWidth` on the control inside
that cell. A percentage width directly on a grid child can resolve against
the whole grid instead of its track in MyGo v0.7.1. The regression tests cover
field gaps and the desktop's context action grid.

Prefer MyGo components before composing a replacement. Select and editable
Combobox use `SelectBase` / `ComboboxBase`; Tabs use `TabsBase`; input fields use
MyGo text inputs. This package styles their parts and preserves their native
keyboard navigation, focus, popup dismissal and value binding. Use MyGo's
Scroll / ScrollHorizontal for overflow rather than implementing wheel handling.

PasswordInput combines a native text input and a visibility button. NumberInput
is a thin wrapper around MyGo v0.7.3's standard NumberInput: numeric editing,
stepping, range validation, keyboard input and accessibility belong to MyGo.
Use the standard inner layout; do not reimplement it to stretch the input.


## Naive 风格公共组件

# Native UI kit

所有控件构造前使用 `c.Key(stableID)`；选择值必须绑定持久状态。

- `Action(c, label, ActionOptions{Tone: Primary, Style: Secondary})`：次要（彩色浅底）、`Tertiary` 次次要（中性浅底）、`Ghost` 透明描边。支持 Small、Round、Circle、Disabled、Loading、Icon。返回原生按钮，可追加 OnClick、Tooltip、Label。
- `ColorTag(c, label, TagOptions{Tone: Success})`：彩色标签，支持 Small、Round、Borderless、Disabled、Icon、OnClose。
- `Radios(c, &state.Value, []RadioOption[T]{...}, buttons)`：原生单选组；buttons 为 true 时采用按钮选项外观。支持选项禁用及方向键。
- `Switch(c, &state.Enabled, "名称")`：原生开关，支持链式 Disabled、Tooltip、OnChange。
- `Message(c, Success, "保存成功", 0)`：原生悬浮信息；返回 ID，可用 c.CloseToast 关闭。更复杂的加载、更新或操作提示使用 c.AddToast(ui.Toast{ID: ..., Type: "loading", Timeout: -1})。Type 支持 info/success/warning/error/loading。
- `Messages(c)`：每个窗口调用一次，负责悬浮提示的统一外观；超时、暂停和无障碍由 MyGo 管理。

语义颜色统一由 ToneColor 提供，Primary 跟随用户强调色，其他语义色分别适配深浅主题。未配置颜色时使用 Default。尺寸单位为 DIP；仅样式保存在此包，不保存跨帧 Element 或 Context。

`SwitchWithOptions` 可配置 Small、Square、Tone、Disabled、Loading。按钮式 Radios 使用相连描边，普通 Radios 使用细环和主题色圆点。

视觉预览：`ALLY_SEMANTIC_PREVIEW=1 CGO_ENABLED=0 go test ./desktop/uikit -run TestSemanticPreview -v`，输出深浅主题控件与悬浮信息预览 PNG（Base64）。

`Quaternary`（次次次要）默认仅显示文字，悬停或按下时显示浅底；`ActionOptions.Selected` 可持续显示选中底色。Card 内的文字操作统一使用此样式，语义颜色保持不变。

## 交互动效约束

所有新增或改动的公共组件遵守本节；动效实现集中在 `motion.go` 和组件内部，页面不另设动画时长。

| 场景 | 行为 | 时长 |
| --- | --- | --- |
| 按钮、图标按钮、选项、单选、Tab、Card 行 | 悬停、按下、选中时底色／文字／边框柔和变色 | 140ms |
| 输入框 | 聚焦边框、底色过渡；文字输入、选区和光标即时响应 | 140ms |
| 开关 | 复用 MyGo 原生滑块位移和底色渐变，不叠加第二层位移 | 原生 140ms |
| 下拉浮层、弹窗 | 淡入淡出，最多 4–6 DIP 位移，不弹跳 | 180ms |
| 全局悬浮通知 | 淡入淡出、4 DIP 位移，连续通知的位置平滑过渡 | 180ms |
| 折叠内容 | 优先原生 Collapsible；已有自定义触发器使用 Reveal 平滑高度并裁剪内容 | 200ms |

- 只给状态变化添加过渡，不给静态文字、正常输入、流式输出每个 token 加动画。
- 悬停和按下不得改变按钮尺寸、字重、内边距或文字位置；不使用放大、弹跳或循环闪烁。
- 布局需要稳定：按钮／开关不参与压缩，加载占用固定空间；不在相邻按钮之间临时插入加载图标。
- 一个元素、一个动画 key 每帧只设置一个目标。先计算目标颜色，再调用 AnimateColor，避免目标互相覆盖。
- 控件构造前绑定稳定 c.Key；动画状态交给 MyGo，不保存跨帧 Element／Context，不创建后台动画线程或手工 sleep。
- 显隐退出期间保留稳定父容器；退出快照不能接收点击或焦点。通知使用原生 ToastViewportBase 管理生命周期。
- 尊重系统 Preferences.ReduceMotion，原生动画 API 自动立即完成，不强制播放。
- 复杂代码编辑器、diff 和长列表保持滚动、输入即时响应，不强制对内容重排添加高度动画。
- 验证至少覆盖过渡中间帧、最终状态、减少动态效果，以及加载期间按钮位置／尺寸不变。

回归测试：`motion_test.go`、`message_animation_test.go`、宿主的 `skill_card_layout_test.go`。

### 小尺寸动画的像素取整案例

2026-10-30：运行状态三线动画在请求 60 FPS 时仍有轻微停顿感。柱高在 9～16 DIP 之间按连续余弦曲线变化，但 MyGo v0.7.3 的 `Painter.Fill` 会将边缘对齐整数物理像素。相邻帧的小数变化被舍去，视觉上变成“保持几帧，再跳一个像素”；曲线在最高／最低附近减速时更明显。循环首尾连续，并非重置跳变。

- 修复：保留原有曲线、1300ms 周期、208ms 错峰和固定布局，改用 `ui.Path + Painter.FillPath` 绘制圆角轮廓，请求 30 FPS。该版本路径绘制按四分之一物理像素量化，配合抗锯齿，比整数边缘能表现更细微的变化；并非无限精度。
- 排查顺序：先检查坐标量化与抗锯齿，再检查曲线端点速度、循环衔接和实际帧间隔；提高 FPS 无法消除整数坐标造成的阶梯。
- 验证：`desktop/thinking_motion_test.go` 覆盖整数取整后边缘相同的两个高度仍产生不同像素覆盖，以及 ReduceMotion 下静止且不持续请求帧。用户运行新版后确认卡顿感已修复；静态截图不能单独证明动画流畅。
- 实现入口：`desktop/composer.go` 的 `runSpinner`、`drawThinkingBar`；周期与错峰集中在 `motion.go`。只请求重绘，不为动画重建布局或创建后台定时线程。30 FPS 是该动画的请求值，窗口实际绘制频率仍由 MyGo 和其他动效共同决定。

### 全局结果提示策略

应用自定义右键菜单统一使用 `uikit.ContextMenu(anchor, build)`，内部复用 MyGo `PopoverMenu` 的文字测量、布局、键盘导航和关闭行为，避免 Windows 原生 owner-draw 菜单测量不足裁切末字。菜单挂在覆盖目标区域的视口，行菜单优先；检查长中文、快捷键、大字号和窗口边缘。组件自带系统菜单不由此入口接管。不得为应用菜单问题直接修改 MyGo 依赖源码。

- 设置页内部切换分区及偏好即时写入静默成功，只在离开整个设置页后提示一次实际保存结果；失败即时提示。
- 查看／打开文件等导航操作不提示成功，失败才提示。
- 手动点击刷新，在异步结果返回且仍有效后提示成功或失败；页面进入、后台同步及变更后的自动重新加载不提示成功。
- 通过 UI 线程的通知队列发送结果，不在发起异步操作时提前报成功。

浅色主题强调色须由宿主提供独立配色，不能把深色主题的浅强调色直接与灰色正文混合，否则会失去色相。文字／图标强调色与浅色背景、白色面板对比至少 4.5:1；主操作与开关、选中项共享强调色。普通操作保留中性色，避免整页所有按钮都变成主操作。

Tooltip 必须使用 `uikit.Tooltip(c, anchor, text)` 或侧栏的 `RightTooltip`，不要直接调用 Element.Tooltip 绕过公共动效。统一 180ms 淡入淡出，保留原生 600ms 悬停延迟、键盘焦点和点击／Esc 关闭行为；右侧菜单提示保持右侧定位。公共入口提前挂载原生 overlay 父容器，保证第一个提示也有入场动画、最后一个提示有退场动画。

## 页面 Tabs：A / C 两套样式

默认 `Tabs(c, &state.Tab, labels...)` 使用 **C 紧凑分段**，由 `DefaultTabStyle = TabSegmented` 集中决定。

```go
// A：笔直细线一级导航；二级只以文字色强调。
uikit.TabsWithOptions(c.Key("top"), &state.Tab,
    uikit.TabOptions{Style: uikit.TabLine}, "对话模型", "图片模型")

// C：紧凑底框，一二级选中项均使用主题色浅底与主题色文字。
uikit.TabsWithOptions(c.Key("provider"), &state.Provider,
    uikit.TabOptions{Style: uikit.TabSegmented, Secondary: true}, providers...)
```

- 两套均复用 MyGo TabsBase，保留角色、方向键、Home/End、焦点与 Changed 行为。
- 一级和二级使用不同强调方式，不叠加两条下划线或两层完整底框。
- 标签字重固定，切换时不改变文字测量宽度；仅颜色／底色／底边平滑变化。
- 页面水平溢出使用宿主现有 tabScroller，不换行挤压内容区。
- 当前页面统一采用 C；窗口顶端的工作区标签保持独立样式。
- 视觉预览：`ALLY_TABS_PREVIEW=1 CGO_ENABLED=0 go test ./desktop/uikit -run TestNativeTabStylesPreview -v`。

状态色分为文字色与指示灯填充色。文字／图标使用 Theme.Success、Warning、Danger（浅色模式深一些以保证可读性）；小圆点由宿主 pillDotColor 提供清晰填充色，停用态使用浅灰。不要再在页面里调用固定深色模式常量充当状态文字色；不能把深色文字色直接用于浅色模式的小状态灯。Git 增删、工具结果、计划完成、子代理、更新标记均遵循此规则。

资源树选中项使用 `TreeSelection`：保留原生 TreeItem 的选择语义、方向键和焦点环，用 18% 主题色浅底和正文文字代替整行实色。文件内容预览优先原生 List / Table 的虚拟化，不为 Office 文档创建 WebView；仅保留一个有界文档预览，关闭时清空文档数据与列表状态。

## 标题栏工作区标签：Chrome 曲线标签

`WorkspaceTab` 使用 Chrome 曲线样式：默认高 44 DIP，顶部留 6 DIP；活跃项使用 Theme.Background，与正文、左侧菜单、右侧资源管理器严格共用底色，顶部圆角、底部向外展开并与下方衔接，取消强调色下划线。非活跃项融入淡主题色标题栏，悬停显示圆角背景，右侧显示短分隔线。状态点和关闭按钮始终占据固定槽位，活跃项的关闭按钮常显，非活跃项在悬停或键盘焦点进入时显示；运行中的标签不可关闭。标题字重固定、底边始终预留空间，切换时文字不移动。标题只保留工作区目录名，悬停时 Tooltip 给出完整路径（运行中的标签在路径上一行标「运行中」），同名目录不会认错。页面内 Tabs 继续使用 C 紧凑分段，互不影响。

Tooltip 身份绑定锚点自身的 Local 轻量标记，不用提示文字作为 identity；文字更新不得重播入场动画。提示正文 Unselectable + PassThrough，不抢锚点悬停。Painter.AnimationFrameRate 用于思考等持续装饰动画，不用 Context.Invalidate/Loop 反复重建整页。纯 reasoning token 事件只更新底部计数，不能设置 transcript FollowPending；只有可见正文增长才请求跟随滚动。

工作区标签宽度按名称自然测量（最小 64、最大 220 DIP），不等分剩余空间；只给溢出名称截断，短名称不占固定 100 DIP。顶栏历史菜单每次新建会话／标签，不复用已打开的同路径标签；历史项身份按完整路径区分，同名目录用 Tooltip 展示路径。菜单右边缘对齐触发按钮并向左展开，历史列表滚动，取消旧系统菜单 16 项截断。

标题栏工作区标签无外部横向间隔，命中区填满 44 DIP 标题栏；曲线与悬停背景只改变绘制，不改变命中区和文字布局。关闭按钮继续固定槽位、悬停／聚焦显隐。

深色 Tertiary 普通按钮文字采用 TextMuted 向 Text 混合 55%，中性底色使用正文色 8.5% 透明度，避免工具栏按钮像禁用态；浅色普通按钮保持原配色。Card 内 Quaternary 仍然静止透明。底部附加工作区菜单使用原生 PopoverBase，对齐触发器上边、菜单下边，并保留 6 DIP 间距，不使用跟随鼠标向下展开的系统菜单。

页面 C Tabs 的一级、二级选中态均跟随 Theme.Accent，统一使用 18% 主题色浅底和主题色文字；不能以中性 Surface/Text 覆盖一级选中态。深浅主题及切换强调色时都应即时更新，未选中项保持中性。

窗口最小化／最大化／关闭按钮必须铺满宿主标题栏高度（宽 46 DIP，最小高 44 DIP），常态绘制宿主传入的不透明 header 背景色，与标题栏共用 palette.headerColor()，不使用透明底色淡出到原生背板。边框／内边距为零，图标与底色同步过渡；关闭悬停为系统红色。普通内容区 Quaternary 按钮仍保持透明，不套用窗口按钮规则。

终端工具的标题命令与回显正文采用独立文字层级：浅色命令行使用 Text 向 TextMuted 混合 30%，回显使用 TextMuted 向 Background 混合 12%；两者对背景对比均至少 4.5:1。深色沿用原配色。折叠／展开、本地／远程命令和服务回显共用此规则；文件代码与 diff 不随终端配色改变。高亮缓存 key 包含基础文字色，避免命令与回显共用缓存后串色。

命令输出（本地与远程）的正文左缘与命令标题动词对齐，折叠／展开均不添加额外左缩进。输出字号为 fsTool - 1（当前 14 DIP，对应标题 15 DIP），与文件代码字号独立；create 的既有轻微缩进不受影响。

标题栏分隔线必须在不透明背景之后绘制：窗口控制按钮用 DrawOver 在底部补画 1 DIP Theme.Border；工作区标签使用独立曲线背景，活跃标签底部不画分隔线。不能只在父容器背景层画线，否则子控件悬停底色会遮住底线。完整标题栏回归需覆盖窗口控制按钮的上下边界及逐像素底线连续性。

启动期间沿用正式窗口标题栏／侧栏和主题背景，不另建品牌 Splash、转圈或闪烁动画；初始化完成后直接填入正文。宿主在创建窗口前读取主题偏好，首帧与原生窗口底色一致。后台初始化保持异步，失败以静态错误信息显示，不留永久加载动画。

思考状态的三条波形线以 Painter.AnimationFrameRate(60) 绘制连续、错峰的余弦波，不保留每轮静止等待段；固定 15×16 DIP 布局，不用 Loop/Invalidate 逐帧重建视图。系统 ReduceMotion 开启时显示静态标记，不继续请求动画帧。
