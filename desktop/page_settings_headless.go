// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"ally-dev/locales"

	"github.com/egoist/mygo/ui"
)

func (a *desktop) settingsHeadless(c *ui.Context) {
	card(c, "headless-start", func() {
		ui.Column(c).FillWidth().Gap(gapSection).Children(func() {
			sectionTitle(c, locales.Text("启动无头模式"))
			fieldNote(c, locales.Text("无头模式不打开窗口，自动启用 API，不修改桌面 API 设置。请在终端运行："), a.secondaryInk())
			a.headlessCommand(c, "headless-linux", "Linux", "./ally-native --headless")
			a.headlessCommand(c, "headless-macos", "macOS", "/Applications/Ally.app/Contents/MacOS/Ally --headless")
			a.headlessCommand(c, "headless-windows", "Windows PowerShell", `& .\ally-native.exe --headless | Out-Host`)
			fieldNote(c, locales.Text("按实际安装位置替换程序路径。Windows 使用 Out-Host 等待运行并查看输出，不会另开窗口。"), a.secondaryInk())
		})
	})
	card(c, "headless-connect", func() {
		ui.Column(c).FillWidth().Gap(gapSection).Children(func() {
			sectionTitle(c, locales.Text("连接与配置"))
			fieldNote(c, locales.Text("默认监听 127.0.0.1:47821。用 --host 和 --port 修改；端口被占用时程序会退出。"), a.secondaryInk())
			fieldNote(c, locales.Text("默认使用 ~/.ally_agent；--data-dir 可指定其他数据目录。同一目录不能同时供桌面和无头实例使用。"), a.secondaryInk())
			fieldNote(c, locales.Text("默认工作区可用 --workspace 指定，也可创建会话时指定。首次使用需配置并选择模型。"), a.secondaryInk())
			fieldNote(c, locales.Text("未指定 Token 时，复用数据目录 api.json 的 token；首次启动会生成并保存。终端只打印 Token 文件路径，不显示密钥。"), a.secondaryInk())
			fieldNote(c, locales.Text("--token-file 指定纯文本 Token 文件；也可用 --token 传入。自定义端口与 Token 不写回桌面 API 设置。"), a.secondaryInk())
			fieldNote(c, locales.Text("所有业务与事件接口都需要 Authorization: Bearer <Token>。远程连接建议使用 HTTPS 反向代理或 SSH 隧道。"), a.secondaryInk())
			a.headlessCommand(c, "headless-health", locales.Text("验证连接（替换 Token 和地址）"), `curl -H "Authorization: Bearer <Token>" http://127.0.0.1:47821/api/v1/health`)
			fieldNote(c, locales.Text("API 地址和事件流地址会打印在终端；远程访问时请将监听地址 0.0.0.0 替换为服务器 IP 或域名。无头模式不提供网页界面。"), a.secondaryInk())
			services := c.Services()
			settingsAction(c, locales.Text("接口文档")).OnClick(func() { services.OpenURL("https://github.com/Bronya0/ally-agent/blob/main/docs/headless.md") })
		})
	})
	card(c, "headless-stop", func() {
		ui.Column(c).FillWidth().Gap(gapSection).Children(func() {
			sectionTitle(c, locales.Text("退出与恢复"))
			fieldNote(c, locales.Text("Ctrl+C 或 Linux/macOS 的 SIGTERM 会停止接收任务、取消运行并等待会话保存。不要用强制结束代替正常退出。"), a.secondaryInk())
			a.headlessCommand(c, "headless-shutdown", locales.Text("通过 API 关闭（替换 Token 和地址）"), `curl -X POST -H "Authorization: Bearer <Token>" http://127.0.0.1:47821/api/v1/shutdown`)
			fieldNote(c, locales.Text("重启后保留历史与配置，中断的对话任务不会自动重放。无头模式不提供网页界面。"), a.secondaryInk())
			a.headlessCommand(c, "headless-help", locales.Text("查看全部启动参数"), "./ally-native --headless --help")
		})
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
