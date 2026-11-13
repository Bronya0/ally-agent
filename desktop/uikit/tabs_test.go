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
