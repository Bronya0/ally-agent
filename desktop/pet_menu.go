// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"fmt"
	"time"

	"github.com/egoist/mygo/ui"
)

// The native menu can extend outside the small transparent pet window.
func (a *desktop) petContextMenu(m *ui.Menu) {
	if m.Item("摸摸头").Chosen() {
		a.petMenuAction(a.patPet)
	}
	if m.Item("和我说句话").Chosen() {
		a.petMenuAction(func() { a.sayPetChatter(time.Now(), true) })
	}
	if m.Item("查看好感度").Chosen() {
		a.petMenuAction(func() {
			score := a.prefs.Pet.Affection[normalizedAvatarStyle(a.prefs.Avatar)].Points
			a.showPetBubble(fmt.Sprintf("我们的好感度是 %.3f / 10 星。", float64(score)/1000), true)
		})
	}
	m.Separator()
	m.Submenu("宠物大小", func(m *ui.Menu) {
		for _, option := range []struct {
			label string
			size  int
		}{{"小", 140}, {"中", 180}, {"大", 230}} {
			option := option
			if m.Item(option.label).Radio(a.prefs.Pet.Size == option.size).Chosen() {
				a.petMenuAction(func() { a.setPetSize(option.size) })
			}
		}
	})
	if m.Item("闲时说话").Checked(!a.prefs.Pet.MuteChatter).Chosen() {
		a.petMenuAction(func() { a.setPetChatter(a.prefs.Pet.MuteChatter) })
	}
	m.Separator()
	if m.Item("打开主界面").Chosen() {
		a.petMenuAction(a.showMainWindow)
	}
	if m.Item("宠物设置").Chosen() {
		a.petMenuAction(a.openPetSettings)
	}
	if m.Item("关闭桌宠").Chosen() {
		a.petMenuAction(func() { a.setPetEnabled(false) })
	}
}

// Menu callbacks are evaluated while the pet view is being built. Queue
// window resizing/destruction through the main window instead of destroying
// the surface that is currently building its menu.
func (a *desktop) petMenuAction(action func()) {
	if a.win == nil {
		if !a.closing {
			action()
		}
		return
	}
	generation := a.pet.generation
	a.win.Update(func() {
		if !a.closing && a.pet.win != nil && a.pet.generation == generation {
			action()
		}
	})
}

func (a *desktop) openPetSettings() {
	if a.win == nil || !a.ready {
		return
	}
	a.settingsTab, a.settings.Tab = "通用", "通用"
	if a.settings.Modules == nil {
		a.settings.Modules = make(map[string]int)
	}
	a.settings.Modules["settingsGeneral"] = 0
	a.changePage("settings")
	a.showMainWindow()
}
