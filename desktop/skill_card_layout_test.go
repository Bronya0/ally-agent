package desktop

import (
	"github.com/egoist/mygo/ui"
	"testing"
)

func TestSkillCardActionsStayFixedWhileBusy(t *testing.T) {
	for _, width := range []int{260, 500} {
		enabled, busy := true, false
		tt := ui.NewTester(func(c *ui.Context) {
			c.DisableAnimations()
			ui.Row(c).FillWidth().Gap(12).Children(func() {
				ui.Column(c).Grow(1).MinWidth(0).Children(func() { ui.Text(c, "A long skill description that must yield space to the actions").SingleLine() })
				skillCardActions(c.Key("actions"), "技能开关", "SKILL.md", &enabled, busy, func() {}, func(value bool) { enabled = value; busy = true })
			})
		}, width, 100)
		before, ok := tt.Find("查看")
		if !ok {
			t.Fatal("missing view")
		}
		sw, _ := tt.Find("技能开关")
		if err := tt.Click("技能开关"); err != nil {
			t.Fatal(err)
		}
		if enabled || !busy {
			t.Fatal("toggle did not publish new bound value")
		}
		for _, next := range []bool{true, true, false, true, false} {
			busy = next
			tt.Frame()
			after, _ := tt.Find("查看")
			switchAfter, _ := tt.Find("技能开关")
			if after != before || switchAfter != sw {
				t.Fatalf("busy=%v changed geometry: button %v -> %v; switch %v -> %v", busy, before, after, sw, switchAfter)
			}
		}
	}
}
