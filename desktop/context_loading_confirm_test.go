package desktop

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// Turning any optional source off must go through the same warning: the draft
// stays untouched until the reader confirms, and escape or cancel keeps it on.
func TestContextLoadingDisableConfirmation(t *testing.T) {
	a := &desktop{}
	a.syncSettingsForm()
	a.settings.Modules = map[string]int{"settingsGeneral": settingsGeneralSection("上下文加载")}
	tt := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(a.theme())
		ui.Column(c).Fill().Children(func() { a.settingsGeneral(c) })
	}, 1000, 1400)

	const id, label = "user_instructions", "用户级指令"
	enabled := func() bool { return a.configDraft.ContextEnabled(id) }
	clickSwitch := func() {
		t.Helper()
		control, ok := tt.AccessByRole(ui.RoleSwitch, label)
		if !ok {
			t.Fatalf("missing switch %q", label)
		}
		r := control.Bounds
		tt.ClickAt(r.X+r.W/2, r.Y+r.H/2)
	}

	clickSwitch()
	if !enabled() || !a.settings.ContextLoadingConfirmOpen {
		t.Fatalf("disable must open the shared warning without changing the draft: enabled=%t open=%t", enabled(), a.settings.ContextLoadingConfirmOpen)
	}
	if !tt.HasText("关闭「" + label + "」？") {
		t.Fatal("the warning did not name the source being turned off")
	}
	if err := tt.Click("取消"); err != nil {
		t.Fatal(err)
	}
	if !enabled() || a.settings.ContextLoadingConfirmOpen {
		t.Fatal("cancel changed the draft or left the dialog open")
	}
	clickSwitch()
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if !enabled() || a.settings.ContextLoadingConfirmOpen {
		t.Fatal("escape changed the draft or left the dialog open")
	}
	clickSwitch()
	if err := tt.Click("仍然关闭"); err != nil {
		t.Fatal(err)
	}
	if enabled() || a.settings.ContextLoadingConfirmOpen {
		t.Fatal("confirmation did not turn the source off")
	}
	clickSwitch()
	if !enabled() || a.settings.ContextLoadingConfirmOpen {
		t.Fatal("re-enabling should not ask again")
	}
}
