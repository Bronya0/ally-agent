package uikit

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestTabStylesNavigationAndStableLayout(t *testing.T) {
	for _, style := range []TabStyle{TabLine, TabSegmented} {
		for _, secondary := range []bool{false, true} {
			selected, changes := 0, 0
			tt := chineseTester(func(c *ui.Context) {
				ui.Column(c).Children(func() {
					if TabsWithOptions(c.Key("tabs"), &selected, TabOptions{Style: style, Secondary: secondary}, "First", "Second", "Third").Changed() {
						changes++
					}
				})
			}, 420, 100)
			before, _ := tt.Find("Second")
			if err := tt.Click("Second"); err != nil {
				t.Fatal(err)
			}
			tt.Frame()
			after, _ := tt.Find("Second")
			if selected != 1 || changes != 1 || before != after {
				t.Fatalf("style %d secondary=%v unstable selection: %d %d %v %v", style, secondary, selected, changes, before, after)
			}
			tt.Key(0, ui.KeyRight)
			tt.Frame()
			if selected != 2 || changes != 2 {
				t.Fatal("keyboard navigation failed")
			}
			tt.Key(0, ui.KeyHome)
			tt.Frame()
			if selected != 0 {
				t.Fatal("Home failed")
			}
		}
	}
}

func TestSegmentedTabsFollowAccentAtBothLevels(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, secondary := range []bool{false, true} {
			accent := ui.Hex("#365dcc")
			selected := 0
			tt := chineseTester(func(c *ui.Context) {
				theme := ui.LightTheme()
				if dark {
					theme = ui.DarkTheme()
				}
				theme.Accent = accent
				c.SetTheme(theme)
				c.DisableAnimations()
				ui.Column(c).Fill().Background(theme.Background).Children(func() {
					TabsWithOptions(c.Key("tabs"), &selected, TabOptions{Style: TabSegmented, Secondary: secondary}, "Selected", "Idle")
				})
			}, 360, 100)
			chosen, _ := tt.Find("Selected")
			idle, _ := tt.Find("Idle")
			pick := func(r ui.Rect) [4]uint8 {
				v := tt.Image().RGBAAt(int(r.X+6), int(r.Y+4))
				return [4]uint8{v.R, v.G, v.B, v.A}
			}
			chosenBefore, idleBefore := pick(chosen), pick(idle)
			accent = ui.Hex("#9b6218")
			tt.Frame()
			if pick(chosen) == chosenBefore {
				t.Fatalf("selected fill ignored accent: dark=%v secondary=%v", dark, secondary)
			}
			if pick(idle) != idleBefore {
				t.Fatal("unselected tab changed color with accent")
			}
		}
	}
}
