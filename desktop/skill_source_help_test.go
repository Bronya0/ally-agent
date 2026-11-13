// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestSkillSourceHelpHoverKeepsPageLayout(t *testing.T) {
	for _, appearance := range []string{"dark", "light"} {
		for _, width := range []int{360, 900} {
			t.Run(fmt.Sprintf("%s/%d", appearance, width), func(t *testing.T) {
				a := &desktop{page: "skills", prefs: defaultPrefs()}
				a.prefs.Appearance = appearance
				if appearance == "light" {
					a.settings.AppearanceMode = 1
				}
				tt := ui.NewTester(func(c *ui.Context) {
					c.SetTheme(a.theme())
					c.DisableAnimations()
					ui.Column(c).Fill().Selectable().Children(func() { a.pageContent(c, float32(width)) })
				}, width, 650)
				badge, ok := tt.Find("技能来源帮助")
				// 布局会有亚像素尾差，这里只守“图标是固定尺寸”这个行为。
				if !ok || math.Abs(float64(badge.W-22)) > 0.5 || math.Abs(float64(badge.H-22)) > 0.5 {
					t.Fatalf("empty skill page has no fixed-size help badge: %v", badge)
				}
				search, ok := tt.Find("搜索技能")
				if !ok {
					t.Fatal("missing skill search field")
				}
				if _, ok := tt.Find(skillSourcesHelpText); ok {
					t.Fatal("help opened before interacting with the badge")
				}
				tt.Move(badge.X+badge.W/2, badge.Y+badge.H/2)
				tt.Advance(time.Second)
				tip, ok := tt.Find(skillSourcesHelpText)
				if !ok || tip.X < 0 || tip.Y < 0 || tip.X+tip.W > float32(width) || tip.Y+tip.H > 650 {
					t.Fatalf("help tooltip is missing or outside the viewport: %v", tip)
				}
				if after, _ := tt.Find("技能来源帮助"); after != badge {
					t.Fatalf("showing help moved its badge: %v -> %v", badge, after)
				}
				if after, _ := tt.Find("搜索技能"); after != search {
					t.Fatalf("showing help moved the page toolbar: %v -> %v", search, after)
				}
				tt.Move(float32(width)/2, 620)
				tt.Advance(time.Second)
				if _, ok := tt.Find(skillSourcesHelpText); ok {
					t.Fatal("help did not dismiss after leaving the badge")
				}
			})
		}
	}
}

func TestSkillSourceHelpKeyboardFocusAndEscape(t *testing.T) {
	a := &desktop{page: "skills", prefs: defaultPrefs()}
	tt := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(a.theme())
		c.DisableAnimations()
		ui.Column(c).Fill().Children(func() { a.pageContent(c, 600) })
	}, 600, 650)
	tt.Key(0, ui.KeyTab)
	if !tt.Focused("技能来源帮助") {
		t.Fatal("help badge is not reachable with the keyboard")
	}
	if _, ok := tt.Find(skillSourcesHelpText); !ok {
		t.Fatal("keyboard focus did not open help")
	}
	tt.Key(0, ui.KeyEscape)
	if _, ok := tt.Find(skillSourcesHelpText); ok {
		t.Fatal("Escape did not dismiss help")
	}
	if _, ok := tt.Find("技能来源帮助"); !ok {
		t.Fatal("dismissing help removed the badge")
	}
}
