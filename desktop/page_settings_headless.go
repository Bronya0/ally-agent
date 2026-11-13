// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"ally-dev/locales"

	"github.com/egoist/mygo/ui"
)

func (a *desktop) settingsHeadless(c *ui.Context) {
	a.settingsCard(c, "headless-start", locales.Text("无头模式"), locales.Text("同一个程序，默认打开窗口；在第一个参数位置加 --headless，可在没有桌面的服务器上运行。"), func() {
		fieldNote(c, locales.Text("无头模式自动开启 API，不受桌面 API 开关影响，也不会修改桌面设置。"), a.secondaryInk())
		a.headlessCommand(c, "headless-linux", "Linux", "./ally-native --headless --port 47821")
		a.headlessCommand(c, "headless-macos", "macOS", "/Applications/Ally.app/Contents/MacOS/Ally --headless --port 47821")
		a.headlessCommand(c, "headless-windows", "Windows PowerShell", `& .\ally-native.exe --headless --port 47821 | Out-Host`)
		fieldNote(c, locales.Text("按实际安装位置替换程序路径。Windows 使用 Out-Host 等待运行并查看输出，不会另开窗口。"), a.secondaryInk())
	})
	a.settingsCard(c, "headless-address", locales.Text("端口与远程连接"), locales.Text("默认仅监听 127.0.0.1:47821；--port 指定端口，--host 指定监听地址。端口占用时退出，不自动换端口。"), func() {
		a.headlessCommand(c, "headless-remote", locales.Text("Linux 服务器示例"), "./ally-native --headless --host 0.0.0.0 --port 49000 --data-dir /srv/ally/data --workspace /srv/projects/demo --token-file /srv/ally/token")
		fieldNote(c, locales.Text("监听成功后，终端打印 API 与事件流地址。远程客户端将 0.0.0.0 或 :: 替换为服务器 IP 或域名，例如 http://服务器IP:49000/api/v1。"), a.secondaryInk())
		fieldNote(c, locales.Text("--workspace 可省略；--data-dir 指定数据目录。同一数据目录不能同时运行桌面和无头实例，多实例需使用不同目录和端口。"), a.secondaryInk())
	})
	a.settingsCard(c, "headless-auth", locales.Text("Token 与客户端连接"), locales.Text("所有业务与事件接口都需要 Authorization: Bearer <Token>。远程连接建议使用 HTTPS 反向代理或 SSH 隧道。"), func() {
		fieldNote(c, locales.Text("未指定 Token 时，复用数据目录 api.json 的 token；首次启动会生成并保存。终端只打印 Token 文件路径，不显示密钥。"), a.secondaryInk())
		fieldNote(c, locales.Text("--token-file 指定纯文本 Token 文件；也可用 --token 传入。自定义端口与 Token 不写回桌面 API 设置。"), a.secondaryInk())
		a.headlessCommand(c, "headless-health", locales.Text("验证连接（替换 Token 和地址）"), `curl -H "Authorization: Bearer <Token>" http://127.0.0.1:47821/api/v1/health`)
		fieldNote(c, locales.Text("首次使用需通过 API 配置模型。创建会话、发送消息和查询结果沿用同一套 API；事件流位于 /api/v1/events，轮询位于 /api/v1/events/poll。"), a.secondaryInk())
	})
	a.settingsCard(c, "headless-stop", locales.Text("退出与恢复"), locales.Text("Ctrl+C 或 Linux/macOS 的 SIGTERM 会停止接收任务、取消运行并等待会话保存。不要用强制结束代替正常退出。"), func() {
		a.headlessCommand(c, "headless-shutdown", locales.Text("通过 API 关闭（替换 Token 和地址）"), `curl -X POST -H "Authorization: Bearer <Token>" http://127.0.0.1:47821/api/v1/shutdown`)
		fieldNote(c, locales.Text("重启后保留历史与配置，中断的对话任务不会自动重放。无头模式不提供网页界面。"), a.secondaryInk())
		a.headlessCommand(c, "headless-help", locales.Text("查看全部启动参数"), "./ally-native --headless --help")
	})
}

func (a *desktop) headlessCommand(c *ui.Context, key, label, command string) {
	ui.Column(c.Key(key)).FillWidth().MinWidth(0).Gap(gapTight).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
			ui.Text(c, label).FontSize(fsSmall).TextColor(a.secondaryInk()).Grow(1).MinWidth(0)
			settingsAction(c, locales.Text("复制")).OnClick(func() {
				c.WriteClipboard(command)
				c.Toast(locales.Text("已复制"))
			})
		})
		codeBlock(c, key+"-command", command, 100, a.secondaryInk())
	})
}
