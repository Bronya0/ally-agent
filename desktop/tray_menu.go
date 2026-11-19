// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"errors"

	"ally-dev/desktop/uikit"
	backend "ally-dev/internal/app"
	"ally-dev/locales"

	"github.com/egoist/mygo/ui"
)

func (a *desktop) trayMenuItems() []uikit.TrayItem {
	items := make([]uikit.TrayItem, 0, 5)
	tabs := make([]uikit.TrayItem, 0, len(a.tabs))
	for _, tab := range a.tabs {
		if tab == nil {
			continue
		}
		id := tab.ID
		name := sessionTabLabel(tab)
		icon, state := trayTabStatus(tab)
		color := ui.Hex("#9ca3af")
		switch icon {
		case "refresh":
			color = ui.Hex("#eab308")
		case "check":
			color = ui.Hex("#22a55a")
		case "alert":
			color = ui.Hex("#dc4545")
		}
		tabs = append(tabs, uikit.TrayItem{Key: "tab:" + id, Label: short(name, 30),
			AccessibilityLabel: name + " · " + state, Icon: iconImage(icon), StatusColor: color,
			OnClick: func() { a.showTrayTab(id) }})
	}
	if len(tabs) > 0 {
		items = append(items, uikit.TrayItem{Key: "tabs", Label: locales.Text("工作区"), Icon: iconImage("folder"), Children: tabs})
	}
	currentMode := a.config.ProxyMode
	if currentMode != "system" && currentMode != "manual" {
		currentMode = "off"
	}
	var proxyItems []uikit.TrayItem
	for _, choice := range []struct{ mode, label string }{
		{"off", "关闭代理"}, {"system", "跟随系统代理"}, {"manual", "手动代理"},
	} {
		mode := choice.mode
		label := locales.Text(choice.label)
		accessibility := label
		if currentMode == mode {
			accessibility += " · " + locales.Text("当前")
		}
		proxyItems = append(proxyItems, uikit.TrayItem{
			Key: "proxy:" + mode, Label: label, AccessibilityLabel: accessibility,
			Icon: iconImage("check"), Checkable: true, Checked: currentMode == mode,
			Disabled: !a.ready || a.closing,
			OnClick:  func() { a.setTrayProxyMode(mode) },
		})
	}
	items = append(items, uikit.TrayItem{Key: "proxy", Label: locales.Text("网络代理"), Icon: iconImage("network"), Children: proxyItems, Disabled: !a.ready || a.closing})
	items = append(items,
		uikit.TrayItem{Key: "open", Label: locales.Text("打开主窗口"), Icon: iconImage("home"), SeparatorBefore: true, OnClick: a.showMainWindow},
		uikit.TrayItem{Key: "update", Label: locales.Text("检查更新"), Icon: iconImage("refresh"), OnClick: a.checkTrayUpdates},
		uikit.TrayItem{Key: "quit", Label: locales.Text("退出 Ally"), Icon: iconImage("power"), SeparatorBefore: true, OnClick: a.shutdown},
	)
	return items
}

func (a *desktop) setTrayProxyMode(mode string) {
	if a.core == nil || a.win == nil || !a.ready || a.closing || a.quitReady {
		return
	}
	previous, done := a.configWriteSlot()
	go func() {
		defer close(done)
		waitConfigWrite(previous)
		err := a.core.SetNativeProxyMode(mode)
		var cfg backend.ConfigState
		if err == nil {
			cfg, err = a.core.GetConfig()
		}
		if !errors.Is(err, backend.ErrManualProxyNotConfigured) {
			a.writeFailures.record("config", err)
		}
		a.win.Update(func() {
			defer a.refreshNativeTray()
			if err != nil {
				a.showMainWindow()
				if errors.Is(err, backend.ErrManualProxyNotConfigured) {
					a.notifyError(locales.Text("请输入有效的 HTTP、HTTPS 或 SOCKS5 代理地址；填写完整后自动生效。"))
				} else {
					a.notifyError(locales.Text("保存设置失败：") + err.Error())
				}
				return
			}
			previousMode := a.config.ProxyMode
			a.config = cfg
			if a.settingsBase != nil {
				previousMode = a.settingsBase.ProxyMode
				a.settingsBase.ProxyMode = cfg.ProxyMode
			}
			if plan := a.settingsInFlight; plan != nil && plan.request.Base.ProxyMode != plan.request.Draft.ProxyMode {
				return // The queued save includes an explicit proxy-mode edit.
			}
			if a.configDraft.ProxyMode == previousMode {
				a.setProxyModeDraft(cfg.ProxyMode)
			}
		})
	}()
}

func trayTabStatus(tab *conversation) (string, string) {
	if tab.Busy || (tab.UI != nil && tab.UI.CompactBusy) {
		return "refresh", locales.Text("运行中")
	}
	switch tab.trayOutcome {
	case "done":
		return "check", locales.Text("已完成")
	case "error":
		return "alert", locales.Text("失败")
	case "cancelled":
		return "close", locales.Text("已取消")
	}
	for i := len(tab.Messages) - 1; i >= 0; i-- {
		m := tab.Messages[i]
		if str(m, "role") != "assistant" {
			continue
		}
		if str(m, "status") == "cancelled" {
			return "close", locales.Text("已取消")
		}
		if m["error"] == true {
			return "alert", locales.Text("失败")
		}
		if num(m, "roundDoneAt") > 0 {
			return "check", locales.Text("已完成")
		}
		break
	}
	return "info", locales.Text("空闲")
}

func (a *desktop) showTrayTab(id string) {
	if a.win == nil || a.closing || a.quitReady {
		return
	}
	for i, tab := range a.tabs {
		if tab == nil || tab.ID != id {
			continue
		}
		if a.page == "settings" {
			a.commitSettingsOnLeave()
		}
		if a.page == "models" {
			a.cancelOAuthWhenLeaving()
		}
		if a.page != "chat" || i != a.active {
			a.hideFilePreview()
		}
		a.page = "chat"
		a.activateTab(i)
		a.refreshFiles()
		a.refreshSessionFooter(tab)
		a.refreshGit(tab)
		a.showMainWindow()
		return
	}
}

func (a *desktop) checkTrayUpdates() {
	a.showMainWindow()
	if a.ready && !a.closing && !a.quitReady {
		a.checkUpdates(false)
	}
}
