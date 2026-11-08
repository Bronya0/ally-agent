package uikit

import (
	"fmt"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestFormRowUsesAvailableWidth(t *testing.T) {
	for _, width := range []float32{900, 580, 360, 260} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			value := "saved value"
			tt := chineseTester(func(c *ui.Context) {
				ui.Column(c).Width(width).Children(func() {
					FormCard(c, "card", "Preferences", "", func() {
						FormRow(c, "field", "Editable", "A description that wraps instead of crowding the input.", FormControlWidth, func() ui.Element {
							return Input(c.Key("input"), &value).FillWidth()
						})
					})
				})
			}, 1200, 400) // Deliberately wider than the actual settings container.
			tt.Frame()
			input, ok := tt.AccessByRole(ui.RoleTextField, "Editable")
			if !ok {
				t.Fatal("missing editable control")
			}
			caption, ok := tt.Find("Editable")
			if !ok {
				t.Fatal("missing field caption")
			}
			note, ok := tt.Find("A description that wraps instead of crowding the input.")
			if !ok {
				t.Fatal("missing field description")
			}
			r := input.Bounds
			if r.W <= 0 || r.W > FormControlWidth+1 || r.X+r.W > width-PadForm+1 {
				t.Fatalf("control overflows parent width %v: %+v", width, r)
			}
			if width == 900 {
				if r.X < note.X+note.W+GapSection*2-1 {
					t.Fatalf("wide row lacks space between explanation and control: note=%+v input=%+v", note, r)
				}
			} else if r.Y < note.Y+note.H+GapField-1 || r.X < caption.X-1 {
				t.Fatalf("narrow row did not stack without overlap: caption=%+v note=%+v input=%+v", caption, note, r)
			}
		})
	}
}

func TestFormRowKeepsFocusAndTextAcrossWrap(t *testing.T) {
	width, value := float32(900), ""
	tt := chineseTester(func(c *ui.Context) {
		ui.Column(c).Width(width).Children(func() {
			FormCard(c, "card", "Preferences", "", func() {
				FormRow(c, "field", "Editable", "", FormControlWidth, func() ui.Element {
					return Input(c.Key("input"), &value).FillWidth()
				})
			})
		})
	}, 1200, 400)
	if err := tt.Click("Editable"); err != nil {
		t.Fatal(err)
	}
	tt.Type("agent ")
	for _, next := range []float32{360, 900, 580} {
		width = next
		tt.Frame()
		input, ok := tt.AccessByRole(ui.RoleTextField, "Editable")
		if !ok || !input.Focused || value != "agent " {
			t.Fatalf("wrap at width %v lost focus or edited text: input=%+v value=%q", width, input, value)
		}
	}
	tt.Type("name")
	if value != "agent name" {
		t.Fatalf("typing after wrap did not continue the same edit: %q", value)
	}
}

func TestFormRowLabelTogglesOnlyItsSwitch(t *testing.T) {
	first, second := false, true
	tt := chineseTester(func(c *ui.Context) {
		FormCard(c, "card", "Preferences", "", func() {
			FormRow(c, "first", "First switch", "", 40, func() ui.Element { return Switch(c, &first, "First switch") })
			FormRow(c, "second", "Second switch", "", 40, func() ui.Element { return Switch(c, &second, "Second switch") })
		})
	}, 700, 400)
	if err := tt.Click("First switch"); err != nil {
		t.Fatal(err)
	}
	if !first || !second {
		t.Fatal("label activation must toggle only the associated switch")
	}
	if err := tt.Click("Second switch"); err != nil {
		t.Fatal(err)
	}
	if !first || second {
		t.Fatal("second label activation changed the wrong switch")
	}
}
