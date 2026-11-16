// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestSessionRenameDialogEditsTitleAndCancels(t *testing.T) {
	a := &desktop{sessionRename: sessionRenameState{Open: true, ID: "session-1", Title: "Old title"}}
	tester := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(a.theme())
		c.DisableAnimations()
		a.sessionRenameDialog(c)
	}, 600, 300)
	if err := tester.Click("会话名称"); err != nil {
		t.Fatal(err)
	}
	tester.Key(ui.Cmd, ui.KeyA)
	tester.Type("New title")
	if a.sessionRename.Title != "New title" {
		t.Fatalf("title input = %q", a.sessionRename.Title)
	}
	if err := tester.Click("取消"); err != nil {
		t.Fatal(err)
	}
	if a.sessionRename.Open {
		t.Fatal("cancel did not close rename dialog")
	}
}
