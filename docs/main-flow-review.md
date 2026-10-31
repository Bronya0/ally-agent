# 主功能快审清单

用于逐项检查发布前主流程。一次只跑一个编号，记录复现条件、结果、日志/截图和对应代码或测试。自动化用例通过不代表真实供应商、MCP 服务、SSH 主机或更新源已联调。

状态：`[ ]` 未检查，`[x]` 通过，`[!]` 发现问题，`[-]` 当前环境无法验证。

## 先补入原清单的主流程

- [ ] CORE-01 新建、切换、关闭和恢复会话；工作区与模型归属正确，草稿和历史不串会话。
- [ ] CORE-02 流式回复、停止生成、超时/错误重试；停止后没有残留请求或重复副作用。
- [ ] CORE-03 上下文统计、自动压缩、压缩失败恢复；压缩成功后提示词/技能/工具快照按约定重建。
- [ ] CORE-04 内置 read/grep/edit/create/delete/command 全链路；越界、保护路径、版本冲突和退出码在 UI 与模型结果中准确。
- [ ] CORE-05 资源树刷新保留展开状态，只读取仍展开的目录；切工作区、删除/改名后不留旧分支。
- [ ] CORE-06 文件打开、保存、并发修改冲突、刷新/丢弃；文本编辑器和各类预览的路由正确。
- [ ] CORE-07 调度任务真实创建、运行、失败、重跑、删除；补齐任务中心事件同步检查。
- [ ] CORE-08 代理、OAuth/账号凭据和连接错误路径；聊天、压缩、测试连接、定时任务使用同一模型身份与凭据。

## 对话交互、计划与协作

- [ ] CHAT-01 大历史分页、上一条提问/回到最新、切换会话后的滚动位置恢复；流式追加时不跳页、不重复或漏消息。
- [ ] CHAT-02 删除某条提问及其后续对话；取消确认无变更，确认后界面、持久化快照和模型历史截断到同一位置，重开会话结果一致。
- [ ] CHAT-03 建议追问、快捷消息、复制总结、导出回答/会话；操作作用于正确会话和消息，导出内容与产品约定一致。
- [ ] AGENT-01 `plan` 创建、读取、更新和完成；面板进度与工具结果一致，计划落后于已完成工作时能在结束前补齐，切会话不会串计划。
- [ ] AGENT-02 `ask_user` 多题、选项/自定义答案、前后题切换、全部答完后提交；重复提交、发送普通消息或取消不会误解除等待。
- [ ] AGENT-03 子代理启动、进度、完成/失败/取消、报告与文件清单；上下文、实际获得的工具权限、模型、用量和主会话隔离正确。
- [ ] MEM-01 全局 `USER.md`、项目记忆索引和 `LESSONS.md` 的读取与更新；全局偏好不混入项目事实，更新后按新会话/提示词快照重建边界生效，且不把凭据写入记忆。
- [ ] CTX-01 项目 `AGENTS.md`、代码图谱、项目教训和工作区图谱的发现、预算、来源标注和工作区切换；只读当前项目允许的文件，旧会话按提示词快照规则处理。
- [ ] CTX-02 上下文详情面板的提示词分段、工具/技能/MCP、历史消息、图片与 token 用量；显示值与请求输入同源，刷新和压缩后不显示过期快照。

自动化入口：`desktop/chat_restore_scroll_test.go`、`desktop/page_chat_window_test.go`、`desktop/composer_hint_test.go`、`desktop/composer_context_test.go`、`internal/app/biz_sessions_truncate_test.go`、`internal/app/app_plan_check_test.go`、`internal/app/orch_subagent_context_test.go`、`internal/app/orch_subagent_report_test.go`、`internal/app/biz_prompt_user_profile_test.go`、`internal/app/biz_prompt_lessons_test.go`、`internal/app/biz_project_context_test.go`、`internal/app/app_freeze_regression_test.go`。

## 工作区与开发工具

- [ ] WORK-01 最近工作区、创建/切换临时工作区、空工作区和关闭最后一个标签；会话、草稿、模型与资源树归属正确，清理临时目录前没有活动任务仍在使用。
- [ ] WORK-02 会话级附加工作区的添加、重复添加、移除和恢复；授权只影响当前会话，越界路径给出可操作提示，移除对后续请求生效，当前运行遵守已冻结的本轮配置。
- [ ] WORK-03 资源树创建文件/目录、重命名、删除、复制/粘贴、属性查看；冲突、取消和失败不留下幽灵节点，路径有空格/中文时结果正确。
- [ ] WORK-04 `@` 文件/目录引用、搜索建议和工作区图谱；仅把本轮需要的内容带入上下文，目录大时仍可取消/切换，引用路径不串工作区。
- [ ] WORK-05 Git 状态与多仓库 diff：未跟踪/删除/重命名文件、选择文件、刷新、关闭/取消读取、非 Git 目录及仓库切换后无旧结果。
- [ ] WORK-06 自动代码校验的语言开关与编辑器/Agent 写入联动；校验失败能定位问题，关闭某语言时不误跑其他校验。

自动化入口：`desktop/workspace_menu_test.go`、`desktop/extra_roots_menu_test.go`、`desktop/extra_roots_config_test.go`、`desktop/explorer_clipboard_test.go`、`desktop/explorer_refresh_test.go`、`internal/app/biz_temp_workspace_test.go`、`internal/app/biz_session_roots_test.go`、`internal/app/biz_workspace_map_test.go`、`internal/app/app_orch_git_test.go`、`internal/app/orch_validation_formats_test.go`、`internal/app/orch_validation_regression_test.go`。

## 多模态输入与图片模型

- [ ] MEDIA-01 图片/文本/文件附件通过选择器、拖放和剪贴板加入；移除、重复项、取消、格式/大小限制、发送失败和切会话时不丢失或串用。
- [ ] MEDIA-02 图片作为模型输入时验证视觉模型路由、图片预览/历史恢复、非视觉模型切换；模型实际收到的图与当前消息一致。
- [ ] IMAGE-01 图片模型独立于对话模型添加、编辑、导入/导出和删除；供应商、协议、Key 池、自定义头、生成/编辑模型身份与测试连接一致。
- [ ] IMAGE-02 文生图/图生图、比例、最多参考图、生成失败、原图保存/下载和历史恢复；编辑引用只能访问当前会话拥有的源图。
- [ ] IMAGE-03 对话中的图片生成工具、生成结果卡片和附件引用；工具失败、重试及压缩/重开历史后不出现伪成功或丢失原图。

自动化入口：`desktop/composer_paste_test.go`、`desktop/attachment_chip_theme_test.go`、`internal/app/app_run_input_test.go`、`internal/app/app_run_input_images_test.go`、`internal/app/orch_read_image_test.go`、`desktop/model_config_test.go`、`internal/app/biz_image_testing_test.go`、`internal/app/biz_images_test.go`、`internal/app/orch_image_test.go`。真实图像供应商仍需手动联调。

## 网络与屏幕工具

- [ ] TOOL-01 `web_fetch` / `http_request` 的 URL 校验、重定向、超时/取消、代理和内网访问开关；失败时不会把网络错误伪装成内容或无限重试。
- [ ] TOOL-02 屏幕/窗口枚举与截图：权限拒绝、窗口关闭、无可用窗口、图像大小限制及多模态模型接收；结果和错误能在工具卡片中辨认。
- [ ] TOOL-03 命令超时转后台服务、进程组停止、代码校验和工具重试；已启动的长任务不会被误认为已终止或被重复启动。

自动化入口：`internal/app/orch_webfetch_test.go`、`internal/app/orch_screenshot_test.go`、`internal/tools/screenshot/screenshot_test.go`、`internal/app/host_command_context_test.go`、`internal/app/orch_services_stop_test.go`。

## 技能

- [ ] SK-01 扫描项目、用户、兼容目录和内置技能；优先级、大小写去重、损坏副本回退正确，冷扫描和缓存刷新耗时可接受。
- [ ] SK-02 手动刷新绕过 TTL；页面来源分组、搜索、数量和启停状态来自同一份扫描结果。
- [ ] SK-03 新会话只注入启用技能元数据；启停/刷新不改当前会话，压缩重建和释放会话遵守快照边界。
- [ ] SK-04 注入目录树并明确允许 `read` 引用文件；技能加载按会话冻结的定义定位，不能只读 `SKILL.md`，也不能混入后来启用的技能。

自动化入口：`internal/app/biz_skills_test.go`、`internal/app/app_freeze_regression_test.go`。手动重点：冷缓存/热缓存耗时、旧会话与新会话对照。

## MCP 与内置工具

- [ ] MCP-01 添加、编辑、连接 stdio / SSE / Streamable HTTP；transport 推断、参数数组往返、非法输入和未知字段保留正确。
- [ ] MCP-02 多轮调用不附带聊天历史或轮次业务状态；连接/握手复用，断线恢复不重放可能已产生副作用的调用。
- [ ] MCP-03 `disabledTools` 按对端原始名称持久化；禁用工具不注入新会话，新工具默认启用。
- [ ] MCP-04 添加、编辑、删除、启停服务器和工具配置只影响新会话；旧会话工具清单保持快照，失效连接如实报错。
- [ ] MCP-05 MCP 调用不传 `stage`；内置工具开关同步控制新会话注入与实际执行，禁用时返回明确错误。
- [ ] MCP-06 导入/导出 JSON 验证 UTF-8、BOM、UTF-16、中文、路径、空参数、引号/反斜杠和大小限制。

自动化入口：`desktop/mcp_tool_ui_test.go`、`internal/app/biz_mcp_test.go`、`biz_mcp_readiness_test.go`、`biz_mcp_config_io_test.go`、`orch_tool_contract_test.go`、`orch_disabled_tools_test.go`、`host_config_import_test.go`。

## 模型与配置

- [ ] MODEL-01 添加、编辑、选择、搜索/拉取模型；供应商、模型 ID、OAuth 账号身份不串，连接测试使用实际保存配置。
- [ ] MODEL-02 验证 `max_tokens`、`max_completion_tokens`、`auto`、思考强度、自定义头、视觉能力的保存与实际请求字段。
- [ ] MODEL-03 上下文窗口只参与本地统计和压缩阈值；对照各协议请求，确认没有误发为供应商容量参数。
- [ ] MODEL-04 聊天、压缩、连接测试、定时任务都取到正确模型和凭据；切换模型后新旧会话边界正确。
- [ ] MODEL-05 模型导入/导出覆盖 UTF-8、BOM、UTF-16、中文、显式空值/false、密钥字段和未知字段保留。
- [ ] MODEL-06 ChatGPT OAuth 账号加载/切换、浏览器登录与取消、套餐调用授权、模型刷新、退出账号及令牌失效；账号身份不与普通 API Key 模型混用。

自动化入口：`desktop/model_config_test.go`、`internal/app/host_config_import_test.go`、`prov_protocol_regression_test.go`、`prov_responses_wire_test.go`。协议字段以 `docs/model-protocol-fields.md` 为准；OAuth 浏览器授权需要真实账号手测。

## SSH

- [ ] SSH-01 添加、编辑、删除免密/私钥/密码节点；认证模式决定凭据含义，切换模式不残留隐藏凭据，编辑/删除清缓存。
- [ ] SSH-02 模型可先 `ssh_cluster` 列表再选节点调用 `remote_*`；列表包含用途描述和状态，不含密码、私钥内容或私钥路径。
- [ ] SSH-03 未授权节点不可被模型使用；列表缺少目标时提示用户在底部添加并勾选授权或走申请授权流程。
- [ ] SSH-04 授权按工作区和稳定节点 ID 生效；改别名不丢授权、不串节点；确认当前模型看到授权节点的描述信息。

自动化入口：`desktop/ssh_auth_test.go`、`internal/app/biz_ssh_identity_test.go`、`biz_ssh_cluster_test.go`、`orch_ssh_cluster_test.go`、`orch_remote_test.go`、`orch_remote_hostkey_test.go`。

## 本地 API 与外部集成

- [ ] API-01 设置页启停桌面 API、查看/复制/重新生成 Token；旧 Token 立即失效，端口冲突显示未运行，保存后重启状态与监听地址一致。
- [ ] API-02 Bearer 鉴权、错误状态码和请求大小限制；未授权请求无数据泄露，业务 API 只绑定预期地址，关闭应用后端口释放。
- [ ] API-03 API 会话闭环：创建、发消息、运行中排队、轮询状态/结果、取消、压缩、删除；附件和指定模型参数按接口限制处理。
- [ ] API-04 待回答问题、计划和事件流：提交 ask 答案、SSE 断线续传/轮询 resync；断线不取消任务，重同步后会话消息与状态一致。
- [ ] API-05 模型/MCP/技能配置接口与工具、子代理、服务、任务查询；更新遵循各接口的整体替换/增量应用规则，响应不含 API Key 或自定义头值。

自动化入口：`internal/app/biz_api_test.go`、`internal/app/host_native_test.go`；接口契约见 `docs/api.md`。桌面 API 与真实插件/脚本仍需做端到端联调。

## 任务中心、升级与退出

- [ ] TASK-01 后台服务和定时任务创建→开始→成功/失败→重跑→删除；无对应聊天 tab 时列表仍同步，通知不代替状态更新。
- [ ] TASK-02 长命令转后台服务后按当前/全部工作区筛选；查看输出尾部与完整日志、复制日志、停止服务、退出码/失败原因和已结束服务清理正确。
- [ ] TASK-03 计划任务由工具创建后验证一次性/周期表达式、时区与下次运行时间、运行模型身份、工作区授权、失败重跑和重启恢复；不把被取消的任务当成功。
- [ ] TASK-04 任务详情与运行日志在快速切页、刷新、删除、无聊天标签、事件乱序/重复时保持状态一致；停止/删除后的迟到事件不能复活任务。
- [ ] UPDATE-01 版本检查、跳过版本、下载进度/取消、资产校验、安装失败恢复、已应用更新重试不重复替换。
- [ ] UPDATE-02 退出/更新保存屏障覆盖未保存文件、会话/草稿、设置、偏好和 SSH 授权；等待失败或超时保留窗口。
- [ ] UPDATE-03 Windows 手动重开提示、启动成功确认；macOS 安装包标识与旧版兼容，发布资产齐全且先校验后公开。

自动化入口：`desktop/task_sync_test.go`、`desktop/update_test.go`、`update_version_test.go`、`internal/app/biz_update_*_test.go`、`host_process_windows_test.go`。升级兼容顺序以 `docs/update-compatibility.md` 为准；真实下载源和跨平台安装仍需手动验证。

## 用量统计

- [ ] STATS-01 今日、近 7 日、本月和近 30 天图表，与模型/工作区分布、输入/输出、缓存命中/写入数据一致；悬停精确值和空数据状态正确。
- [ ] STATS-02 正常、失败、取消、压缩、子代理和图片请求结束后的用量汇总不重复、不串工作区；跨日、跨月、重启后刷新结果稳定。

自动化入口：`internal/app/biz_stats_test.go`、`internal/app/biz_footer_stats_test.go`。

## 暂缓接入

- 游戏页面尚未完成，不接入本轮导航与发布验收。
- 知识库页面尚未接入本轮导航与发布验收；相关页面流程暂不列为已交付功能。

## 设置与配置生效时机

- [ ] SET-01 逐个枚举设置页、模型/MCP/SSH 页和偏好项：记录 UI 字段、持久化字段、读取调用方、生效时机、是否需重启。
- [ ] SET-02 每项执行修改→保存→关闭/重启→重读；验证空值、未提供、显式 `false`/空集合，以及并发保存不覆盖其他配置。
- [ ] SET-03 区分持久化配置、当前会话注入快照、运行中的连接/任务；验证新会话生效，旧会话按约定冻结，压缩和释放的例外正确。
- [ ] SET-04 外观与偏好：跟随系统/手动明暗、强调色、字体字号、头像/背景、侧栏/资源树尺寸和动效偏好；重启后恢复，清除自定义素材不残留引用。
- [ ] SET-05 通用设置：自定义提示词、Git Bash、User-Agent、重试、压缩阈值/方式/超时、工具并发；保存后实际调用使用新值，旧会话按快照规则处理。
- [ ] SET-06 网络与围栏：系统代理检测、手动代理、绕过地址和连通测试；代理失败不意外直连，内网访问开关和命令/路径围栏按配置生效。
- [ ] SET-07 自动校验语言开关、页面显隐、开机启动和自动更新；隐藏页面可重新开启，平台不支持的开机启动给出明确结果。
- [ ] SET-08 完整配置查看/编辑/重新载入/保存；非法 JSON、保存失败和并发变更不能覆盖有效配置或把半份内容落盘。

自动化入口：`biz_config_transactions_test.go`、`biz_config_autosave_test.go`、`biz_config_load_test.go`、`desktop/prefs_defaults_test.go`、`desktop/model_config_test.go`。配置文件导入复用 `ReadLocalTextFile` 和 `read.DecodeTextBytes`。

## 插件管理、启动与诊断

- [ ] PLUGIN-02 ZIP/目录安装、刷新、启停、重新加载、导出（含/不含数据）、删除/清理数据；无效包、权限声明和失败回滚显示准确，宿主临时资源与订阅释放。
- [ ] PLUGIN-03 插件导航/页面显示隐藏、主题变化、保活与重新挂载；插件调用本地 API 的 token、错误响应和卸载后的请求/事件停止正确。
- [ ] APP-01 首次启动、读取既有数据目录、配置损坏提示、已有进程锁、无工作区/默认工作区和恢复会话；不能静默换数据目录或覆盖用户历史。
- [ ] APP-02 正式版首次启动亮点、关闭后设置页重看、版本检查和更新后版本状态；开发版或无发行说明时不显示误导内容。
- [ ] APP-03 内存诊断导出成功、取消/写入失败、重复点击保护；报告只包含约定的诊断数据，路径提示可再次定位文件。

自动化入口：`internal/app/orch_plugin_flow_test.go`、`desktop/pluginhost/server_test.go`、`desktop/startup_view_test.go`、`desktop/startup_workspace_test.go`、`internal/app/biz_startup_test.go`。内存诊断与亮点弹窗的端到端生命周期仍需手动检查。

## 插件与 WebView 生命周期

- [ ] PLUGIN-01 插件运行时创建、启动、停止、异常退出与更新；宿主进程、端口、临时文件和事件订阅在停止/失败后清理，权限摘要不被误当作完整沙箱边界。
- [ ] PREVIEW-01 图片、SVG、音频、视频、PDF、HTML/Markdown、Office 文档按类型预览；不支持或解码失败时提示可理解且不误报成功。
- [ ] PREVIEW-02 连续打开、快速切换、关闭文件/资源树/工作区、窗口关闭、读取取消和渲染失败后销毁 WebView；过期异步结果不能重新挂载。
- [ ] PREVIEW-03 重复打开/关闭媒体和文档，观察 WebView 数量、内存和句柄是否回落；验证限量复用、不可见时不创建以及释放兜底。

自动化入口：`internal/app/orch_plugin_flow_test.go`、`desktop/pluginhost/server_test.go`、`desktop/file_documents_test.go`、`desktop/preview_lifecycle_test.go`、`desktop/preview_documents_test.go`。内存回落需在目标平台做循环手测。

## 每项记录

```text
编号：
版本/平台/工作区：
前置条件与操作：
预期 / 实际：
结果：[ ] 通过  [ ] 发现问题  [ ] 环境阻塞
证据（日志、截图、测试名）：
```
