// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"fmt"
	"math"
	"testing"

	"ally-dev/desktop/uikit"
	backend "ally-dev/internal/app"

	"github.com/egoist/mygo/ui"
)

func TestPlanPanelHeightFollowsStepsAndKeepsScrollCap(t *testing.T) {
	for _, appearance := range []string{"dark", "light"} {
		t.Run(appearance, func(t *testing.T) {
			conversation := &conversation{ID: "plan-layout"}
			a := &desktop{
				prefs:   defaultPrefs(),
				plans:   map[string][]backend.PlanStep{},
				planRev: map[string]int64{},
			}
			a.prefs.Appearance = appearance
			if appearance == "light" {
				a.settings.AppearanceMode = 1
			}
			setSteps := func(count int, current int) {
				steps := make([]backend.PlanStep, count)
				for i := range steps {
					status := "pending"
					if i < current {
						status = "done"
					} else if i == current {
						status = "in_progress"
					}
					steps[i] = backend.PlanStep{Title: fmt.Sprintf("step %02d", i+1), Status: status}
				}
				a.plans[conversation.ID] = steps
				a.planRev[conversation.ID]++
			}
			setSteps(1, 0)
			tt := ui.NewTester(func(c *ui.Context) {
				c.SetTheme(a.theme())
				c.DisableAnimations()
				ui.Column(c).FillWidth().Label("plan-panel-layout").Children(func() {
					a.planPanel(c, conversation)
				})
			}, 640, 500)
			bounds := func() ui.Rect {
				t.Helper()
				r, ok := tt.Find("plan-panel-layout")
				if !ok {
					t.Fatal("missing plan panel")
				}
				return r
			}
			heights := map[int]float32{}
			for _, count := range []int{1, 2, 4} {
				setSteps(count, 0)
				tt.Frame()
				panel := bounds()
				heights[count] = panel.H
				last, ok := tt.Find(a.plans[conversation.ID][count-1].Title)
				if !ok || last.Y < panel.Y || last.Y+last.H > panel.Y+panel.H {
					t.Fatalf("short plan clipped its last step: panel=%v step=%v", panel, last)
				}
				if slack := panel.Y + panel.H - last.Y - last.H; slack > 12 {
					t.Fatalf("%d-step plan leaves %.1f DIP below its content", count, slack)
				}
			}
			if heights[1] >= heights[2] || heights[2] >= heights[4] {
				t.Fatalf("plan height did not follow step count: %v", heights)
			}

			setSteps(24, 23)
			tt.Frame()
			panel := bounds()
			// Preserve the 150 DIP body cap, plus the 30 DIP header and border.
			if panel.H <= heights[4] || panel.H > 182 {
				t.Fatalf("long plan lost its scroll cap: short=%v long=%v", heights[4], panel)
			}
			last, ok := tt.Find(a.plans[conversation.ID][23].Title)
			if !ok || last.Y < panel.Y+30 || last.Y+last.H > panel.Y+panel.H {
				t.Fatalf("long plan did not reveal its current step: panel=%v step=%v", panel, last)
			}

			setSteps(2, 0)
			tt.Frame()
			if panel := bounds(); math.Abs(float64(panel.H-heights[2])) > 0.5 {
				t.Fatalf("shorter replacement kept the previous height: got %v, want %v", panel.H, heights[2])
			}
			if err := tt.Click("plan"); err != nil {
				t.Fatal(err)
			}
			tt.Advance(2 * uikit.DisclosureDuration)
			if panel := bounds(); !conversation.uiState().PlanCollapsed || panel.H > 32 {
				t.Fatalf("collapsed plan retained its body: %v", panel)
			}
			if err := tt.Click("plan"); err != nil {
				t.Fatal(err)
			}
			tt.Advance(2 * uikit.DisclosureDuration)
			if panel := bounds(); conversation.uiState().PlanCollapsed || math.Abs(float64(panel.H-heights[2])) > 0.5 {
				t.Fatalf("reopened plan lost its content height: %v", panel)
			}
		})
	}
}

func TestPlanPanelHeightAccountsForWrappedTitles(t *testing.T) {
	conversation := &conversation{ID: "plan-wrapping"}
	const title = "A longer plan step should wrap naturally instead of leaving a tall empty panel or clipping its text."
	a := &desktop{
		prefs: defaultPrefs(),
		plans: map[string][]backend.PlanStep{
			conversation.ID: {{Title: title, Status: "in_progress"}},
		},
	}
	heights := map[int]float32{}
	for _, width := range []int{280, 640} {
		tt := ui.NewTester(func(c *ui.Context) {
			c.SetTheme(a.theme())
			c.DisableAnimations()
			ui.Column(c).FillWidth().Label("plan-panel-layout").Children(func() {
				a.planPanel(c, conversation)
			})
		}, width, 500)
		panel, ok := tt.Find("plan-panel-layout")
		if !ok {
			t.Fatal("missing plan panel")
		}
		heights[width] = panel.H
		text, ok := tt.Find(title)
		if !ok || text.X < panel.X || text.X+text.W > panel.X+panel.W || text.Y+text.H > panel.Y+panel.H {
			t.Fatalf("width=%d wrapped title escaped its panel: panel=%v text=%v", width, panel, text)
		}
		if slack := panel.Y + panel.H - text.Y - text.H; slack > 12 {
			t.Fatalf("width=%d wrapped title leaves %.1f DIP below its content", width, slack)
		}
	}
	if heights[280] <= heights[640] {
		t.Fatalf("plan did not grow with wrapped text: %v", heights)
	}
}

// A wide window must not widen the panel past the transcript and the composer it
// sits between: drawn at the window's own width, it overhangs both and reads as a
// stray bar across the chat. Capped like them, its rows start inside the composer's
// left edge instead of the window's.
func TestPlanPanelKeepsTheChatWidthInAWideWindow(t *testing.T) {
	conv := &conversation{ID: "plan-wide", Workspace: "ws", Messages: []map[string]any{{"role": "assistant", "content": "answer"}}}
	a := &desktop{
		prefs:   defaultPrefs(),
		tabs:    []*conversation{conv},
		plans:   map[string][]backend.PlanStep{conv.ID: {{Title: "step 01", Status: "in_progress"}}},
		planRev: map[string]int64{},
	}
	tt := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(a.theme())
		c.DisableAnimations()
		ui.Column(c).Fill().Children(func() { a.chatColumn(c, 2600) })
	}, 2600, 900)
	step, ok := tt.Find("step 01")
	if !ok {
		t.Fatalf("missing plan step: %v", tt.Texts())
	}
	input, ok := tt.Find("消息输入框")
	if !ok {
		t.Fatal("missing composer input")
	}
	if input.W > chatMaxWidth {
		t.Fatalf("the composer itself is not capped: %v", input)
	}
	// The step sits inside the panel's padding, and the panel is narrower than the
	// composer: anything at or left of the composer's edge means the panel took the
	// window's width instead of the chat's.
	if step.X <= input.X {
		t.Fatalf("the plan row starts at %.1f, at or past the composer's edge %.1f: the panel is window-wide", step.X, input.X)
	}
	if step.X+step.W > input.X+input.W {
		t.Fatalf("the plan row ends at %.1f, past the composer's right edge %.1f", step.X+step.W, input.X+input.W)
	}
}
