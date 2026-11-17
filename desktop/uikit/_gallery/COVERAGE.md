# MyGo v0.9.2 组件画廊覆盖清单

此清单依据项目锁定版本的 `docs/ui/README.md` 中的组件分类整理。“原生”表示左侧有示例；“uikit”表示右侧有对应示例。两个组件共用一组时，组内控件的高度和顺序不一定完全一致。

| 官方组件 | 原生 | uikit |
| --- | --- | --- |
| Button、SplitButton、Toggle、Segmented、Toolbar | 是 | 是 |
| Link、MenuButton、ContextMenu | 是 | Link、MenuButton；ContextMenu 在错误信息上 |
| Checkbox、Switch、Radio、Select | 是 | 是 |
| Slider、RangeSlider、Stepper、Rating | 是 | 是 |
| TextInput、TextArea、NumberInput、DecimalInput | 是 | 是 |
| SearchField、Combobox、Autocomplete、TokenField、EditableText | 是 | 是 |
| FindBar、RichTextEditor、CodeEditor、DiffViewer、Markdown | 是 | 是 |
| Calendar、DateInput、TimeInput、ColorPicker、ColorWell | 是 | 是 |
| List、Table、Tree、Outline、GridView | 是 | 是 |
| Scroll、Grid、Split | 是 | 基础布局直接使用 MyGo，无独立封装 |
| Collapsible、Accordion、Form | 是 | 是 |
| Tabs、Sidebar、Breadcrumbs | 是 | 是 |
| BackButton、ForwardButton | 是 | 有封装，画廊未展示 |
| Modal、Popover、Tooltip | 是 | 是 |
| AlertDialog | 是 | 有封装，画廊未展示 |
| Toast | uikit 的 Messages 接管同一窗口的 Toast 视口 | Message、Notification |
| Progress、Spinner、Meter、Badge | 是 | 是 |
| Chart、Image、Icon、Avatar | 是 | 是 |
| Video | 需要媒体资源和播放器生命周期，未展示 | 有封装，未展示 |
| WebView | 需要创建和释放原生 WebView，未展示 | 有封装，未展示 |

`Base` 组件是供自定义控件组合的底层部件，不是独立外观；布局、绘图、动画、事件、无障碍与窗口等指南也不是单个带默认外观的组件。画廊不会在渲染时创建媒体播放器或 WebView。
