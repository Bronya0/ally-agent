// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	backend "ally-dev/internal/app"

	"github.com/egoist/mygo"
)

// confirmAction runs a destructive operation after a native confirmation
// dialog. Everything that deletes user data goes through here so the wording
// and the button order stay consistent.
func (a *desktop) confirmAction(title, detail string, run func() error, after func()) {
	confirmButton := "删除"
	if title == "确认删除" {
		confirmButton = "删除"
	}
	go func() {
		result, err := mygo.Dialog.Message(mygo.MessageOptions{Parent: a.win, Type: mygo.MessageWarning, Title: title, Message: title, Detail: detail, Buttons: []string{"取消", confirmButton}, CancelButton: 0})
		if err != nil {
			a.fail(err)
			return
		}
		if result.Button != 1 {
			return
		}
		if err = run(); err != nil {
			a.fail(err)
			return
		}
		a.win.Update(func() {
			if after != nil {
				after()
			}
		})
	}()
}

func (a *desktop) deleteSession(id, label string) {
	a.confirmAction("删除会话", label, func() error { return a.core.DeleteSession(id) }, func() {
		delete(a.openSessions, id)
		a.releaseSessionHTMLPreviews(id)
		for i, t := range a.tabs {
			if t.ID != id {
				continue
			}
			next := a.freshConversation(t.Workspace)
			next.Model, next.Reasoning = t.Model, t.Reasoning
			a.tabs[i] = next
		}
		go func() {
			list, err := a.core.ListSessionIndex()
			if err != nil {
				a.fail(err)
				return
			}
			a.win.Update(func() { a.sessions = list })
		}()
	})
}

func (a *desktop) deleteModel(model backend.ModelConfig) {
	id := modelIdentity(model)
	label := modelOption(model)
	var saved backend.ConfigState
	a.confirmAction("删除模型", label, func() error {
		if err := a.core.DeleteNativeModel(id); err != nil {
			return err
		}
		var err error
		saved, err = a.core.GetConfig()
		return err
	}, func() {
		a.config = saved
		a.modelEdit = -1
		a.modelPrevious = nil
		a.modelDraft = backend.ModelConfig{}
		for _, t := range a.tabs {
			if t.Model == label {
				t.Model = ""
				t.Reasoning = "auto"
			}
		}
		for _, t := range a.openSessions {
			if t.Model == label {
				t.Model = ""
				t.Reasoning = "auto"
			}
		}
	})
}
