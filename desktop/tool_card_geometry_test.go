// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"math"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

// toolCardSentinelY renders one tool card in status and returns where the line
// after it landed: the card's own height is whatever pushes that line.
func toolCardSentinelY(t *testing.T, name, status, args string, result map[string]any, expanded bool) float32 {
	t.Helper()
	a := &desktop{}
	a.prefs.Appearance = "dark"
	conv := &conversation{ID: "geometry"}
	msg := map[string]any{
		"name":       name,
		"toolCallId": "geometry",
		"args":       args,
		"result":     result,
		"status":     status,
	}
	conv.uiState().ToolOpen["geometry"] = expanded
	tt := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(a.theme())
		c.DisableAnimations()
		ui.Column(c).Padding(24, 20).Children(func() {
			a.toolCard(c, conv, 0, msg)
			ui.Text(c, "card end")
		})
	}, 560, 460)
	r, ok := tt.Find("card end")
	if !ok {
		t.Fatalf("%s %q shows no line after the card: %v", name, status, tt.Texts())
	}
	return r.Y
}

// A card may grow when its result arrives, but it must never get shorter: the
// create card used a fixed-height box while the file streamed and collapsed to
// its content once written, which pulled everything below it up at the moment
// the call finished. Every tool card is checked against that, because the next
// state-only sizing branch would reintroduce it.
func TestToolCardsNeverShrinkWhenTheCallFinishes(t *testing.T) {
	args := `{"path":"a.go","content":"package main\n\nfunc main() {}\n","command":"echo hi",` +
		`"pattern":"x","query":"q","html":"<b>x</b>","prompt":"p","instruction":"i","seconds":1}`
	names := make([]string, 0, len(toolTable))
	for name := range toolTable {
		names = append(names, name)
	}
	sort.Strings(names)
	checked := 0
	for _, name := range names {
		if strings.HasSuffix(name, "_") {
			continue // prefix aliases, not tool names
		}
		if name == "render_html" {
			// The running card shows the HTML being written, and finishing swaps
			// that for the live preview: a deliberate shape change, not a size slip.
			continue
		}
		checked++
		for _, expanded := range []bool{false, true} {
			running := toolCardSentinelY(t, name, "running", args, map[string]any{}, expanded)
			done := toolCardSentinelY(t, name, "success", args, map[string]any{}, expanded)
			if done < running-1 {
				t.Errorf("%s expanded=%v: card ends at %v while running and %v once done: it shrank",
					name, expanded, running, done)
			}
		}
	}
	if checked < 20 {
		t.Fatalf("only %d tools checked, the table lookup is broken", checked)
	}
}

// The create card keeps one height from the first streamed line to the result,
// for both a file that fits the preview and one that is clipped.
func TestCreateCardKeepsItsHeightWhileStreaming(t *testing.T) {
	for _, tc := range []struct {
		name    string
		content string
	}{
		{"short file", "package main\n\nfunc main() {}\n"},
		{"long file", strings.Repeat("line of the file\n", 40)},
	} {
		for _, expanded := range []bool{false, true} {
			args := `{"path":"a.go","content":` + strconv.Quote(tc.content) + `}`
			result := map[string]any{"path": "a.go"}
			running := toolCardSentinelY(t, "create", "running", args, result, expanded)
			done := toolCardSentinelY(t, "create", "success", args, result, expanded)
			if math.Abs(float64(running-done)) > 1 {
				t.Fatalf("%s expanded=%v: card ends at %v while running and %v once done, want the same",
					tc.name, expanded, running, done)
			}
		}
	}
}
