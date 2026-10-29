# 预置 ripgrep

这里保存 Ally 支持平台的官方原始压缩包：

- Windows x64：`ripgrep-*-x86_64-pc-windows-msvc.zip`
- Windows arm64：`ripgrep-*-aarch64-pc-windows-msvc.zip`
- Linux x64：`ripgrep-*-x86_64-unknown-linux-musl.tar.gz`
- Linux arm64：`ripgrep-*-aarch64-unknown-linux-musl.tar.gz`
- macOS x64：`ripgrep-*-x86_64-apple-darwin.tar.gz`
- macOS arm64：`ripgrep-*-aarch64-apple-darwin.tar.gz`

来源：https://github.com/BurntSushi/ripgrep/releases

Windows arm64 15.2.0 ZIP 的 SHA-256：`e4abca10c3a64ebea742667dd7009449d49403db5460dd6873e389fa2945360f`。

`scripts/build.py` 从这些归档提取对应平台的可执行文件，运行时沿用可执行文件旁的 `tools/rg`（Windows 为 `rg.exe`）查找逻辑。macOS universal 包合并两个架构的 rg，单架构包只保留对应架构；所有处理都在签名前完成。压缩包不会进入发行包，打包时无需网络下载 rg。

也可在仓库根手动运行：

```sh
node scripts/prepare-ripgrep.mjs
node scripts/prepare-ripgrep.mjs --os linux --arch arm64 --output-dir bin/tools
```

依赖构建环境已有的 Node.js 和 `tar`。Windows ZIP 使用系统自带的 bsdtar；其他主机交叉构建 Windows 时需要 `unzip`。macOS 主机合并 universal rg 使用 `lipo`，其他主机使用已有的 `wails3 tool lipo`。临时文件保存在系统临时目录，不进入发行包。

## 升级 rg

用新版本的上述六个官方压缩包替换旧文件即可，无需清单或版本配置，也无需修改准备脚本、Go 代码或工作流。每个平台必须且只能匹配一个压缩包，否则构建明确报错；macOS universal 的两个包必须同版本。许可证变更时同步更新 `third_party/ripgrep/LICENSE-MIT` 和 `UNLICENSE`。

发行包名称继续保留 Windows x64 和 macOS universal 的历史命名，另提供 Windows arm64、macOS 单架构以及 Linux x64 / arm64 包。

各平台构建成功后分别上传自己的 release 资产；手动构建只保留 artifacts。
