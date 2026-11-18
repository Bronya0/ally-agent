// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"strings"

	"ally-dev/desktop/uikit"
	"ally-dev/locales"
	"github.com/egoist/mygo/ui"
)

type sessionRenameState struct {
	Open  bool
	ID    string
	Title string
	Error string
	Busy  bool
}

func (a *desktop) openSessionRename(id, title string) {
	a.sessionRename = sessionRenameState{Open: true, ID: id, Title: title}
}

func (a *desktop) sessionRenameDialog(c *ui.Context) {
	s := &a.sessionRename
	if !s.Open {
		return
	}
	modalCard(c.Key("session-rename"), &s.Open, widthModalSmall, func() {
		modalTitle(c, locales.Text("重命名会话"), locales.Text("为这个会话设置一个容易识别的名称。"))
		field := uikit.Input(c.Key("session-rename-title"), &s.Title).Label(locales.Text("会话名称")).FillWidth().AutoFocus().Disabled(s.Busy)
		field.OnSubmit(a.submitSessionRename)
		inlineError(c, "session-rename-error", s.Error)
		modalActions(c, func() {
			flat(c, locales.Text("取消")).Disabled(s.Busy).OnClick(func() { s.Open = false })
			primary(c, locales.Text("保存")).Disabled(s.Busy || strings.TrimSpace(s.Title) == "").OnClick(a.submitSessionRename)
		})
	})
}

func (a *desktop) submitSessionRename() {
	s := &a.sessionRename
	if s.Busy || strings.TrimSpace(s.Title) == "" {
		return
	}
	id, title := s.ID, strings.TrimSpace(s.Title)
	s.Busy, s.Error = true, ""
	go func() {
		err := a.core.RenameSession(id, title)
		a.win.Update(func() {
			if a.sessionRename.ID != id {
				return
			}
			a.sessionRename.Busy = false
			if err != nil {
				a.sessionRename.Error = err.Error()
				return
			}
			for _, tab := range a.tabs {
				if tab.ID == id {
					tab.Title = title
				}
			}
			if tab := a.openSessions[id]; tab != nil {
				tab.Title = title
			}
			a.sessionRename.Open = false
			a.reloadSessions()
		})
	}()
}
