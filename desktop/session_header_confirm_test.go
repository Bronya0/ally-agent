package desktop

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestSessionHeaderDisableConfirmation(t *testing.T) {
	for _, header := range []string{"session-id", "x-session-id"} {
		t.Run(header, func(t *testing.T) {
			a := &desktop{}
			a.syncSettingsForm()
			a.settings.Modules = map[string]int{"settingsGeneral": 3}
			tt := ui.NewTester(func(c *ui.Context) {
				c.SetTheme(a.theme())
				ui.Column(c).Fill().Children(func() { a.settingsGeneral(c) })
			}, 1000, 1400)
			target := &a.settings.SendSessionID
			other := &a.settings.SendXSessionID
			if header == "x-session-id" {
				target, other = other, target
			}
			label := "自动发送 " + header
			clickSwitch := func() {
				t.Helper()
				// The text and the switch share a label; Find returns the text first.
				r, ok := tt.Find(label)
				if !ok {
					t.Fatalf("missing switch label %q", label)
				}
				tt.ClickAt(r.X+r.W+gapSection+20, r.Y+r.H+1)
			}
			open := func() {
				t.Helper()
				clickSwitch()
				if !*target || !*other || !a.settings.SessionHeaderConfirmOpen {
					t.Fatalf("disable must open confirmation without changing either header: target=%t other=%t open=%t", *target, *other, a.settings.SessionHeaderConfirmOpen)
				}
				if !tt.HasText("关闭 "+header+" 请求头？") || !tt.HasText("OpenRouter") {
					t.Fatal("confirmation did not identify the header and explain the risk")
				}
			}
			open()
			if err := tt.Click("取消"); err != nil {
				t.Fatal(err)
			}
			if !*target || a.settings.SessionHeaderConfirmOpen {
				t.Fatal("cancel changed the setting or left the dialog open")
			}
			open()
			tt.Key(0, ui.KeyEscape)
			tt.Frame()
			if !*target || a.settings.SessionHeaderConfirmOpen {
				t.Fatal("escape changed the setting or left the dialog open")
			}
			open()
			if err := tt.Click("继续关闭"); err != nil {
				t.Fatal(err)
			}
			if *target || !*other || a.settings.SessionHeaderConfirmOpen {
				t.Fatal("confirmation did not disable only the selected header")
			}
			a.saveSettings()
			if header == "session-id" && (*a.configDraft.SendSessionIDHeader || !*a.configDraft.SendXSessionIDHeader) ||
				header == "x-session-id" && (!*a.configDraft.SendSessionIDHeader || *a.configDraft.SendXSessionIDHeader) {
				t.Fatal("saving did not preserve the independent header settings")
			}
			clickSwitch()
			if !*target || a.settings.SessionHeaderConfirmOpen {
				t.Fatal("re-enabling should not require confirmation")
			}
		})
	}
}
