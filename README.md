# Ally Native（mygo 分支）

Ally 的 MyGo v0.7.1 原生桌面实现，使用 Go 直接绘制界面，无需 WebView2。Agent 核心继续使用 `ally-dev` 模块中的后端实现。

## 运行与构建

需要 Go 1.27.3；Windows 开发优先使用 Git Bash。

开发入口只保留两个，直接双击即可：

| 系统 | 入口 | 产物 |
| --- | --- | --- |
| Windows | `scripts/build.bat` | `dist/ally-native.exe` 与 `dist/tools/rg.exe` |
| macOS | `scripts/build.command` | 当前机器架构的 `dist/Ally.app`，含 rg 和 Ad-hoc 签名 |

完成或失败后窗口保留，便于查看结果。更新前先关闭正在运行的开发版。

```bash
go run .                          # 直接运行源码
python scripts/build.py dev       # 开发编译的命令行入口，无交互暂停
bash scripts/build-all.sh         # 仅 macOS：全平台发行打包，供 GitHub CI 使用
```

构建依赖 Go 1.27.3 和 Python 3.9+；macOS 使用系统自带的 lipo、codesign、hdiutil（需安装 Command Line Tools）。
本地入口与 GitHub CI 共用 `scripts/build.py`，无需 Node；Windows 双击脚本不再依赖 Git Bash。
所有入口可以从任意目录启动。`build-all.sh` 不暂停，不启动 GUI，构建失败会以非零状态退出。

正式发行构建通过 `BUILD_TAG` 和 `ALLY_RELEASE_NOTES_BODY` 嵌入版本号与 Markdown 更新说明；GitHub Release 发布事件会自动提供这两项。说明随各平台程序交付，无需联网。正式版本有正文且尚未读过时，初始化完成后弹出一次限高滚动窗口，关闭后记录到 `native-ui.json`；设置的“版本亮点”可再次查看。开发构建不注入、不弹窗，未提供正文的手动发行构建也不弹窗。打包结束（包括失败）会恢复仓库中的空说明文件。

Windows 开发 exe 固定为 `dist/ally-native.exe`，被占用时明确报错，关闭程序后重试。
发行包放在 `dist/release/`：Windows x64 / arm64 ZIP 解压后包含对应架构的 `Ally.exe`、`tools/rg.exe`、README 和许可文件；
Linux tar.gz 包含单一顶层目录、可执行文件、`tools/rg` 和许可文件，不发布 deb。
Windows 构建使用纯 Go 编译和固定版本的 go-winres 添加图标、版本信息及 DPI manifest，
不调用 NSIS，不生成 setup；go-winres 首次运行由 Go 下载。
macOS 提供 universal、Intel（x64）和 Apple 芯片（arm64）三个 DMG，应用和内置 rg 的架构一致，均做免费的 ad-hoc 签名，不需要付费证书或公证。
打包器从仓库的 ripgrep 归档提取对应二进制，不要求 Node 或从网络下载 rg。

默认读取原版的 `~/.ally_agent` 配置和会话。同一数据目录只允许一个进程运行，独立试用可指定目录：

```bash
ALLY_DATA_DIR="D:/ally-native-data" ./dist/ally-native.exe
```

Enter 发送，Ctrl+Enter 换行，Esc 中断当前会话或工具调用；弹窗打开时 Esc 优先关闭弹窗。标签栏支持鼠标滚轮横向滚动。侧栏和资源树宽度可拖拽调整并自动保存。

## 代码目录

```text
main.go                  原生程序入口，只调用 desktop.Run
desktop/                 原生页面、窗口状态、事件路由和可复用 UI
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

界面保持在根目录的 `desktop` 包内，使页面能共享受主线程约束的状态，无需导出内部控件或引入跨页面全局变量。界面公共样式收口在 `theme.go` 与 `widgets.go`，素材只通过 `resources` 包嵌入。目录边界详见 [原生界面架构](docs/native-architecture.md)。

## 验证

```bash
CGO_ENABLED=0 go test -p 1 ./...
go tool mygo vet .
python scripts/build.py dev
```

全平台发行构建在一个 macOS runner 上通过共享脚本交叉生成 Windows x64/ARM64、Linux x64/ARM64 和 macOS universal/Intel/ARM64 共七个发行包。Windows 只发布包含 exe 和资源的绿色 ZIP。macOS 生成 DMG，应用使用免费的 ad-hoc 签名，不做 Developer ID 签名或公证；首次打开时可能需要在 Finder 中 Control-click 并选择“打开”。MyGo CLI 版本由 `go.mod` 的 tool directive 固定。

测试使用临时目录和本地模拟模型服务，不依赖真实模型密钥。页面和弹窗测试覆盖多次重建及窗口尺寸变化；交互测试覆盖输入快捷键、主题浮层、表单、标签切换、分隔条拖拽和配置持久化。

可用独立数据目录执行真实窗口启动检查：

```bash
ALLY_DATA_DIR="D:/ally-native-smoke" ./dist/ally-native.exe --smoke
```

`--smoke` 创建原生窗口，约四秒后保存并退出。生成文件在 `dist/`，本地调试文件在 `.tmp/`，均不提交。

## 功能与边界

原生界面包含 Agent 对话、知识库、技能、MCP 和工具管理、对话与图片模型、SSH、任务中心、用量统计、游戏、插件管理和设置。图片文件使用原生预览，文本文件使用 MyGo 编辑器。模型调用、工具执行、沙箱、上下文和会话持久化由后端负责。

页面已迁移并保留持续完善的交互测试；这不表示每个视觉细节都与原 Vue 页面完全一致。插件网页不会在本分支中创建 WebView。动画 WebP 当前按静态首帧显示；支持姿势集的内置头像会切换姿势。发行构建可由单个 macOS runner 交叉生成各平台包；仍需在对应系统做运行验收，不能直接用旧 Wails 发行包替换本程序。

原项目版权和 GPL-3.0 许可保留，详见 [LICENSE](LICENSE)。第三方素材与组件说明见 [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)。

升级兼容规则见 [升级兼容说明](docs/update-compatibility.md)。发行资产名保留 Release tag 的
完整拼写（包括 v 前缀），Windows x64 和 macOS universal 沿用 main 的精确名称。
