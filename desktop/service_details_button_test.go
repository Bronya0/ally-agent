// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"testing"

	backend "ally-dev/internal/app"
	"github.com/egoist/mygo/ui"
)

// The service card's 详情 action names itself with its label and draws it once.
// A row inside the button drew the same text again, so the button read
// 详情 详情 ⌄.
func TestServiceDetailsButtonDrawsItsLabelOnce(t *testing.T) {
	for _, open := range []bool{false, true} {
		open := open
		a := &desktop{}
		a.prefs.Appearance = "dark"
		a.services = []backend.ServiceInfo{{ID: "svc_1", Name: "生成服务", Status: "running", PID: 42}}
		if open {
			a.mcpF.ToolOpen = map[string]bool{"service-svc_1": true}
		}
		tt := ui.NewTester(func(c *ui.Context) {
			c.SetTheme(a.theme())
			c.DisableAnimations()
			ui.Column(c).Padding(10).Children(func() {
				a.servicesSection(c, "")
			})
		}, 600, 500)
		count := 0
		for _, text := range tt.Texts() {
			if text == "详情" {
				count++
			}
		}
		// The button names itself with its label and draws it as text: a second
		// drawn copy would count three.
		if count != 2 {
			t.Fatalf("open=%v: 详情 drawn %d times, want a name and one text: %v", open, count, tt.Texts())
		}
	}
}
