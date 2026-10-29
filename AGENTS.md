# Ally Native

- Windows 开发命令优先使用 Git Bash，搜索文本和文件优先 rg。
- 根目录 `main.go` 仅保留入口；原生页面、状态和组件在根目录 `desktop/`，页面文件使用 `page_*.go`。共享样式和组件分别收口在 `theme.go` 与 `widgets.go`。
- 原生界面仅使用 github.com/egoist/mygo/ui；禁止创建 HTML/JS 前端或引入 Wails。经用户授权，render_html 与 Mermaid 图表预览可使用受控 WebView；仅在可见时按需创建，统一限量复用，离开预览后销毁。
- Agent 模块路径保持 ally-dev。`desktop/` 是上层宿主，可依赖 `internal/app`；工具及纯逻辑包不可反向依赖 app/desktop。app 内非 host_* 文件保持宿主中立，桌面宿主适配放 internal/app/host_*.go。
- 优先复用 MyGo 标准组件；需要定制外观时使用对应的 Base/Parts API，并在 pkg/uikit 统一微调样式。不要重复实现已有的选择、键盘导航、焦点、浮层和滚动行为。
- UI 状态只在主线程修改。后台结果经 Window.Update 发布；不得保存 Element 或把 Context 带入后台。动态控件构造前使用 c.Key。
- 不在视图渲染期间读写磁盘、执行网络请求或启动无条件后台任务。素材通过 resources 包嵌入。
- 所有测试读写在 t.TempDir，读取用户目录的测试同时隔离 HOME 和 USERPROFILE。运行全套测试时不要设置全局 ALLY_DATA_DIR，测试自己管理它。
- 构建：python scripts/build.py dev（本机 Windows exe / macOS app；双击 scripts/build.bat / build.command）；全平台交叉构建和 macOS DMG：在 macOS 上运行 bash scripts/build-all.sh；测试：CGO_ENABLED=0 go test -p 1 ./...；UI 检查：go tool mygo vet .。
- Windows 构建产物固定为 dist/ally-native.exe，不添加版本、日期或 fixed 等后缀；覆盖失败时处理文件占用，不得改名绕过。
- migration/legacy-host 保留旧 Wails 专用实现/测试作为迁移参考，不参与构建。dist 和 .tmp 为生成及本地调试目录，不提交。
- 提交作者使用 ally agent <ally@agent.dev>；本实现推送 mygo 分支，不修改 main。

- 公共组件动效遵守 `desktop/uikit/README.md` 的“交互动效约束”，时长集中在 `desktop/uikit/motion.go`。悬停不改变控件尺寸和文字位置；加载不挤压邻近操作；尊重系统减少动态效果设置。
