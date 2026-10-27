# Ally Native（mygo 分支）

Ally 的 MyGo v0.7.1 原生桌面实现，使用 Go 直接绘制界面，无需 WebView2。Agent 核心继续使用 `ally-dev` 模块中的后端实现。

## 运行与构建

需要 Go 1.27.3；Windows 开发优先使用 Git Bash。

```bash
go run .
bash build.sh
```

Windows 构建产物固定为 `dist/ally-native.exe`，后续构建覆盖同名文件，不添加版本、日期或 `fixed` 等后缀。构建脚本不接受自定义输出文件名。

默认读取原版的 `~/.ally_agent` 配置和会话。同一数据目录只允许一个进程运行，独立试用可指定目录：

```bash
ALLY_DATA_DIR="D:/ally-native-data" ./dist/ally-native.exe
```

Enter 发送，Ctrl+Enter 换行，Esc 中断当前会话或工具调用；弹窗打开时 Esc 优先关闭弹窗。标签栏支持鼠标滚轮横向滚动。侧栏和资源树宽度可拖拽调整并自动保存。

## 代码目录

```text
main.go                  原生程序入口，只调用 desktop.Run
internal/desktop/        原生页面、窗口状态、事件路由和可复用 UI
  page_*.go              按功能命名的页面及表单
  widgets.go             按钮、标签、表单、弹窗等公共组件
  theme.go               配色、尺寸、字体和图标样式
  desktop.go             桌面生命周期与会话协调
  window.go              窗口与页面组合
  *_test.go              原生界面交互和布局回归测试
internal/app/            Agent 业务与桌面宿主适配（host_*.go）
internal/tools/          可独立测试的工具逻辑
internal/sandbox/        命令沙箱
resources/               嵌入的图标、头像与许可说明
migration/legacy-host/   旧 Wails 宿主实现的迁移参考，不参与编译
docs/                    后端文档与原生界面架构说明
```

界面保持在一个 `desktop` 包内，使页面能共享受主线程约束的状态，无需导出内部控件或引入跨页面全局变量。界面公共样式收口在 `theme.go` 与 `widgets.go`，素材只通过 `resources` 包嵌入。目录边界详见 [原生界面架构](docs/native-architecture.md)。

## 验证

```bash
CGO_ENABLED=0 go test -p 1 ./...
go run github.com/egoist/mygo/cmd/mygo@v0.7.1 vet .
bash build.sh
```

原生 Windows CI 示例在 `docs/ci/native-windows.yml.example`，可在具备 GitHub 工作流管理权限时启用；现有仓库发布工作流沿用主分支的 Wails 配置。

测试使用临时目录和本地模拟模型服务，不依赖真实模型密钥。页面和弹窗测试覆盖多次重建及窗口尺寸变化；交互测试覆盖输入快捷键、主题浮层、表单、标签切换、分隔条拖拽和配置持久化。

可用独立数据目录执行真实窗口启动检查：

```bash
ALLY_DATA_DIR="D:/ally-native-smoke" ./dist/ally-native.exe --smoke
```

`--smoke` 创建原生窗口，约四秒后保存并退出。生成文件在 `dist/`，本地调试文件在 `.tmp/`，均不提交。

## 功能与边界

原生界面包含 Agent 对话、知识库、技能、MCP 和工具管理、对话与图片模型、SSH、任务中心、用量统计、游戏、插件管理和设置。图片文件使用原生预览，文本文件使用 MyGo 编辑器。模型调用、工具执行、沙箱、上下文和会话持久化由后端负责。

页面已迁移并保留持续完善的交互测试；这不表示每个视觉细节都与原 Vue 页面完全一致。插件网页不会在本分支中创建 WebView。动画 WebP 当前按静态首帧显示；支持姿势集的内置头像会切换姿势。原生发行版的更新资产与跨平台打包仍需分别验证，不能直接用旧 Wails 发行包替换本程序。

原项目版权和 GPL-3.0 许可保留，详见 [LICENSE](LICENSE)。第三方素材与组件说明见 [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)。
