# 预置 ripgrep

这里保存 Ally 支持平台的官方原始压缩包：

- Windows x64：`ripgrep-*-x86_64-pc-windows-msvc.zip`
- Linux x64：`ripgrep-*-x86_64-unknown-linux-musl.tar.gz`
- Linux arm64：`ripgrep-*-aarch64-unknown-linux-musl.tar.gz`
- macOS x64：`ripgrep-*-x86_64-apple-darwin.tar.gz`
- macOS arm64：`ripgrep-*-aarch64-apple-darwin.tar.gz`

来源：https://github.com/BurntSushi/ripgrep/releases

`wails3 build` / `wails3 dev` 自动解压当前目标平台的可执行文件至 `bin/tools/`，运行时沿用可执行文件旁的 `tools/rg`（Windows 为 `rg.exe`）查找逻辑。macOS 开发 `.dev.app` 和发布 `.app` 会在签名前准备各自的内置 rg；universal 包合并两个架构的 rg。压缩包不会进入发行包，准备脚本没有网络请求。

也可在仓库根手动运行：

```sh
node scripts/prepare-ripgrep.mjs
node scripts/prepare-ripgrep.mjs --os linux --arch arm64 --output-dir bin/tools
```

依赖构建环境已有的 Node.js 和 `tar`。Windows ZIP 使用系统自带的 bsdtar；其他主机交叉构建 Windows 时需要 `unzip`。macOS 主机合并 universal rg 使用 `lipo`，其他主机使用已有的 `wails3 tool lipo`。临时文件保存在系统临时目录，不进入发行包。

## 升级 rg

用新版本的上述五个官方压缩包替换旧文件即可，无需清单或版本配置，也无需修改准备脚本、Go 代码或工作流。每个平台必须且只能匹配一个压缩包，否则构建明确报错；macOS universal 的两个包必须同版本。许可证变更时同步更新 `third_party/ripgrep/LICENSE-MIT` 和 `UNLICENSE`。

发行包名称继续保留 Windows x64 和 macOS universal 的历史命名，避免旧客户端自动更新失效；Linux 分别提供 x64、arm64 包。

各平台构建成功后分别上传自己的 release 资产；手动构建只保留 artifacts。
