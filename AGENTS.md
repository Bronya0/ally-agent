# Ally Native

- Windows 开发命令优先使用 Git Bash，搜索文本和文件优先 rg。
- 根目录 `main.go` 仅保留入口；原生页面、状态和组件在 `internal/desktop`，页面文件使用 `page_*.go`。共享样式和组件分别收口在 `theme.go` 与 `widgets.go`。
- 原生界面仅使用 github.com/egoist/mygo/ui；禁止创建 WebView、HTML/JS 前端或引入 Wails。
- Agent 模块路径保持 ally-dev。`internal/desktop` 是上层宿主，可依赖 `internal/app`；工具及纯逻辑包不可反向依赖 app/desktop。app 内非 host_* 文件保持宿主中立，桌面宿主适配放 internal/app/host_*.go。
- UI 状态只在主线程修改。后台结果经 Window.Update 发布；不得保存 Element 或把 Context 带入后台。动态控件构造前使用 c.Key。
- 不在视图渲染期间读写磁盘、执行网络请求或启动无条件后台任务。素材通过 resources 包嵌入。
- 所有测试读写在 t.TempDir，读取用户目录的测试同时隔离 HOME 和 USERPROFILE。运行全套测试时不要设置全局 ALLY_DATA_DIR，测试自己管理它。
- 构建：bash build.sh；测试：CGO_ENABLED=0 go test -p 1 ./...；UI 检查：go run github.com/egoist/mygo/cmd/mygo@v0.7.1 vet .。
- migration/legacy-host 保留旧 Wails 专用实现/测试作为迁移参考，不参与构建。dist 和 .tmp 为生成及本地调试目录，不提交。
- 提交作者使用 ally agent <ally@agent.dev>；本实现推送 mygo 分支，不修改 main。
