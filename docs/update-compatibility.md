# main → mygo 升级兼容

稳定版 Release 的 tag 必须指向 mygo 提交，且版本号高于已发布稳定版。迁移基线是 main 的 `v1.27.0`，首个 MyGo 稳定版应使用例如 `v1.28.0`。
CI 只把 plist/程序版本写成 `1.27.0`，资产名保留 tag 的 `v`，与旧客户端请求完全一致。

| 平台 | 发布资产（以 v1.28.0 为例） | 包内程序 |
|---|---|---|
| Windows x64 | Ally-v1.28.0-windows-x64.zip | Ally.exe |
| Windows ARM64 | Ally-v1.28.0-windows-arm64.zip | Ally.exe |
| macOS Universal | Ally-v1.28.0-macos-universal.dmg | Ally.app/Contents/MacOS/Ally |
| macOS Intel | Ally-v1.28.0-macos-x64.dmg | 同上 |
| macOS ARM64 | Ally-v1.28.0-macos-arm64.dmg | 同上 |
| Linux x64 | Ally-v1.28.0-linux-x64.tar.gz | 单一顶层目录内的 Ally |
| Linux ARM64 | Ally-v1.28.0-linux-arm64.tar.gz | 同上 |

macOS 的 Bundle ID 保持 `io.github.bronya0.ally`，CFBundleExecutable 保持 `Ally`，
CFBundleShortVersionString / CFBundleVersion 与请求的稳定版 tag 去掉 v 后一致。
旧 main 客户端会校验这些字段。改成 Ally Native.app 或新 Bundle ID 会导致拒绝安装。
Windows ZIP 不含 setup，也不把调试用的 ally-native.exe 作为发布入口。

旧 main：Windows x64 可按原流程检测、下载、校验和替换；macOS 下载 Universal DMG。
main 的 Linux 客户端能检查版本，但没有自动下载/安装流程，用户需手动下载 tar.gz。
新增资产不会改变旧客户端的精确匹配。

新 mygo：Windows x64 / ARM64 下载各自的 ZIP，PE 校验拒绝不同 CPU 的程序。
macOS 检测当前程序是否为 Universal：Universal 安装继续取 Universal；单架构安装
按运行架构取 x64 / arm64 DMG。安装时继续校验 Mach-O CPU、程序名、版本和 Bundle ID。
Linux 继续提供版本检测和手动下载，未宣称具备自动替换能力。

所有包均包含对应架构的 rg；macOS 在签名前准备 rg，保持 ad-hoc 签名。

## 发布顺序

先构建和上传完整资产，再公开 Release，避免旧客户端从 Atom 发现新版本却下载到 404。

1. 将版本 tag 指向已提交的 MyGo 代码。推送 `v*` tag 会触发构建；也可在 Actions 手动运行并填写已存在的稳定 tag。
2. 如需自定义发行说明，提前为同一个 tag 保存 GitHub Release 草稿。CI 保留草稿正文；没有草稿时自动创建并生成说明，正文随各平台包嵌入。
3. CI 构建七个包，上传到草稿，并校验每个资产的名称、上传状态和大小。任一构建/上传/校验失败，Release 保持草稿，修正后可重跑。
4. 全部成功才公开为稳定版和 latest。不要提前手动发布空 Release；新流程不再监听 `release:published`。已公开版本拒绝覆盖，修正已发版本应使用更高 tag。

tag 使用 `v1.28.0` 这样的纯数字正式版本，不使用 rc/beta/build 后缀；同一仓库的版本发布串行执行，发布脚本再次检查版本递增。

本机在 macOS 构建发行包也必须显式提供版本，例如 `BUILD_TAG=v1.28.0 bash scripts/build-all.sh`；缺失或不稳定的 tag 会在编译前被拒绝，避免误打包开发版 `0.1.0`。

## 客户端完整流程

- 正式安装包在启动完成 10 秒后检查更新，之后每两小时检查一次；复用后端的代理、缓存、自动下载开关和跳过清单。开发构建没有正式发行标记，不自动下载线上旧 main 版，也不把本地 `0.1.0` 当成迁移版本。
- 后台下载不弹进度窗口；下载成功提示安装，失败保留检查结果供重试。下载进度、解压、取消和完成通过同一事件路径同步；取消请求要等下载 worker 完成才释放操作所有权。
- 安装前先检查未保存文件和正在保存的配置，保存会话文本草稿并等待会话、设置、界面偏好、SSH 授权队列。保存失败/超时不安装、不退出。停止运行可能产生末尾消息，应用成功后再次等待保存完成才退出。
- 已替换成功但退出失败时，重试只继续保存/退出，不重复替换，也不重新下载；安装期间不允许新的聊天运行或覆盖升级状态的检查。
- Windows 按现有机制安装后退出，提示用户重新打开；macOS 在旧进程退出后自动重新打开。新界面初始化成功后调用 `ConfirmUpdateStartup`，才清理旧 EXE/资源或延迟清理旧 macOS bundle。
- 旧 Windows x64 客户端在 ARM 机器上仍沿用 x64 包；切换到原生 ARM64 需手动安装一次。之后 ARM64 → ARM64 自动更新。Linux 两个架构继续手动下载/替换，不把“有发行包”描述成“已有自动安装器”。

回归：`desktop/update_test.go`、`internal/app/biz_update_*_test.go`、`internal/app/biz_session_draft_test.go`、`scripts/test_build.py`、`scripts/test_release.py`。macOS 的挂载、签名、bundle 交换与重启仍需 macOS 环境验证，交叉编译不等同于真实安装测试。
