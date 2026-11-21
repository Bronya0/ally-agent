// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"ally-dev/locales"

	"github.com/egoist/mygo/ui"
)

// settingsHeadless mirrors the API page: three form cards, every fact on its own
// labelled row, and each command a box with the copy action inside it. The old
// layout stacked prose lines and put 复制 at the far side of the card, so the
// text read as a wall and the action looked detached from the command.
func (a *desktop) settingsHeadless(c *ui.Context) {
	a.settingsCard(c, "headless-start", locales.Text("启动无头模式"),
		locales.Text("无头模式不打开窗口，自动启用 API，不修改桌面 API 设置。请在终端运行："), func() {
			a.headlessCommand(c, "headless-linux", "Linux", "./ally-native --headless")
			a.headlessCommand(c, "headless-macos", "macOS", "/Applications/Ally.app/Contents/MacOS/Ally --headless")
			a.headlessCommand(c, "headless-windows", "Windows PowerShell", `& .\ally-native.exe --headless | Out-Host`)
			a.headlessNote(c, locales.Text("按实际安装位置替换程序路径。Windows 使用 Out-Host 等待运行并查看输出，不会另开窗口。"))
		})
	a.settingsCard(c, "headless-connect", locales.Text("连接与配置"),
		locales.Text("默认只监听本机，以下参数都可叠加在启动命令后面。"), func() {
			a.headlessFact(c, "headless-address", locales.Text("监听地址"), "127.0.0.1:47821",
				locales.Text("用 --host 和 --port 修改；端口被占用时程序会退出，自定义端口不写回桌面 API 设置。"))
			a.headlessFact(c, "headless-data-dir", locales.Text("数据目录"), "~/.ally_agent",
				locales.Text("用 --data-dir 换位置；同一目录不能同时供桌面和无头实例使用。"))
			a.headlessFact(c, "headless-workspace", locales.Text("工作区"), "--workspace <目录>",
				locales.Text("不指定时使用默认工作区，也可在创建会话时指定；首次使用需配置并选择模型。"))
			a.headlessFact(c, "headless-token", "Token", "--token-file <文件>",
				locales.Text("未指定时复用数据目录 api.json 的 token，首次启动生成并保存；也可用 --token 直接传入。终端只打印路径，不显示密钥。"))
			a.headlessFact(c, "headless-auth", locales.Text("鉴权"), "Authorization: Bearer <Token>",
				locales.Text("所有业务与事件接口都需要；远程连接建议使用 HTTPS 反向代理或 SSH 隧道。"))
			a.headlessCommand(c, "headless-health", locales.Text("验证连接（替换 Token 和地址）"), `curl -H "Authorization: Bearer <Token>" http://127.0.0.1:47821/api/v1/health`)
			a.settingsRow(c, "headless-docs", locales.Text("接口文档"), locales.Text("端点清单、请求示例与事件流说明。"), settingsControlWidth, func() ui.Element {
				return settingsAction(c, locales.Text("打开文档")).OnClick(func() {
					c.Services().OpenURL("https://github.com/Bronya0/ally-agent/blob/main/docs/headless.md")
				})
			})
			a.headlessNote(c, locales.Text("API 地址和事件流地址会打印在终端；远程访问时请将监听地址 0.0.0.0 替换为服务器 IP 或域名。无头模式不提供网页界面。"))
		})
	a.settingsCard(c, "headless-stop", locales.Text("退出与恢复"),
		locales.Text("Ctrl+C 或 Linux/macOS 的 SIGTERM 会停止接收任务、取消运行并等待会话保存。不要用强制结束代替正常退出。"), func() {
			a.headlessCommand(c, "headless-shutdown", locales.Text("通过 API 关闭（替换 Token 和地址）"), `curl -X POST -H "Authorization: Bearer <Token>" http://127.0.0.1:47821/api/v1/shutdown`)
			a.headlessCommand(c, "headless-help", locales.Text("查看全部启动参数"), "./ally-native --headless --help")
			a.headlessNote(c, locales.Text("重启后保留历史与配置，中断的对话任务不会自动重放。"))
		})
}

// headlessFact is a read-only form row: the literal address, path or header sits
// where other settings put their control, in the mono face, so it reads as a
// value rather than as another paragraph of explanation.
func (a *desktop) headlessFact(c *ui.Context, key, title, value, note string) {
	a.settingsRow(c, key, title, note, settingsControlWidth, func() ui.Element {
		return ui.Text(c, value).Font(monoFamily).FontSize(fsSmall).Selectable()
	})
}

// headlessCommand keeps the command and its copy action in one box. The label
// stays a quiet caption above it, so a long label never pushes the action to the
// far edge of the card.
func (a *desktop) headlessCommand(c *ui.Context, key, label, command string) {
	ui.Column(c.Key(key)).FillWidth().MinWidth(0).Gap(gapField).PaddingY(gapSection).Children(func() {
		ui.Text(c, label).FontSize(fsSmall).TextColor(a.secondaryInk()).SingleLine()
		ui.Row(c).FillWidth().MinWidth(0).AlignItems(ui.Center).Gap(gapField).Padding(4, 8).
			Radius(radiusSmall).Background(c.Theme().SurfaceHover).Children(func() {
			ui.Text(c, command).Font(monoFamily).FontSize(fsTiny).Grow(1).MinWidth(0).Selectable()
			mutedIconAction(c, key+"-copy", locales.Text("复制"), "copy", 15, func() {
				c.WriteClipboard(command)
				c.Toast(locales.Text("已复制"))
			})
		})
	})
}

// headlessNote carries a trailing explanation inside a form card without
// borrowing the row layout, which expects a control.
func (a *desktop) headlessNote(c *ui.Context, note string) {
	ui.Column(c).PaddingY(gapSection).Children(func() { fieldNote(c, note, a.secondaryInk()) })
}
