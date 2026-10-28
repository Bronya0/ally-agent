# 原生桌面目录与依赖

`main.go` 是薄入口，调用 `desktop.Run()`。页面、状态和原生组件集中在根目录 `desktop/`；`page_*.go` 是页面及其编辑表单，`widgets.go` 与 `theme.go` 是共享组件和视觉规范。测试与被测页面在同一个包内。

依赖方向：

```text
main → desktop → internal/app → internal/tools、internal/sandbox
       desktop → resources
```

`desktop/` 是上层桌面宿主，可以依赖 `internal/app`；工具、沙箱和其他纯逻辑包不能反向依赖 `internal/app` 或桌面页面。`internal/app` 内原生窗口、对话框和宿主生命周期适配只放在 `host_*.go`。

UI 状态仅在主线程修改。后台工作通过 `Window.Update` 发布结果；不保存 `ui.Element`，不向后台传递 `ui.Context`。动态控件在构造前调用 `c.Key`。渲染期间不进行文件和网络 I/O，不启动无条件后台任务。

`resources` 包统一嵌入压缩头像、SVG 图标及其许可。只解码选中的头像；切换会话时释放可重建的 Markdown 和图片预览缓存。草稿、聊天记录和未保存的编辑内容不随视图卸载而丢弃。

新增页面时使用 `page_<功能>.go`，在 `window.go` 的页面路由中组合；优先复用 `widgets.go` 的表单、弹窗和主题浮层。后台业务进入 `internal/app` 或独立工具包，避免继续堆到页面渲染函数中。

`migration/legacy-host/*.txt` 仅保存旧宿主迁移参考，不参与 Go 构建。其他 `docs/*.md` 保留原项目后端设计资料，涉及 Wails/Vue 的历史描述不代表本分支的界面实现。
