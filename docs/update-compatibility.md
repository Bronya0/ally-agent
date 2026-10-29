# main → mygo 升级兼容

稳定版 Release 的 tag 必须指向 mygo 提交，且版本号高于已安装版本；例如 `v1.27.0`。
CI 只把 plist/程序版本写成 `1.27.0`，资产名保留 tag 的 `v`，与旧客户端请求完全一致。

| 平台 | 发布资产（以 v1.27.0 为例） | 包内程序 |
|---|---|---|
| Windows x64 | Ally-v1.27.0-windows-x64.zip | Ally.exe |
| Windows ARM64 | Ally-v1.27.0-windows-arm64.zip | Ally.exe |
| macOS Universal | Ally-v1.27.0-macos-universal.dmg | Ally.app/Contents/MacOS/Ally |
| macOS Intel | Ally-v1.27.0-macos-x64.dmg | 同上 |
| macOS ARM64 | Ally-v1.27.0-macos-arm64.dmg | 同上 |
| Linux x64 | Ally-v1.27.0-linux-x64.tar.gz | 单一顶层目录内的 Ally |
| Linux ARM64 | Ally-v1.27.0-linux-arm64.tar.gz | 同上 |

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
