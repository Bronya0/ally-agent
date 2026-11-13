//go:build ally_explorer

// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"fmt"
	"testing"

	backend "ally-dev/internal/app"
	"github.com/egoist/mygo/ui"
)

// TestOnDemandRailSessionScale checks that the rail's session list keeps
// building only the rows in view when a workspace has thousands of sessions:
// only rows in the viewport are built at either size.
func TestOnDemandRailSessionScale(t *testing.T) {
	smallest := 0.0
	for _, total := range []int{80, 2000} {
		a := &desktop{prefs: defaultPrefs(), page: "chat", tabs: []*conversation{{ID: "s0", Workspace: "/work/alpha"}}}
		a.prefs.RailExpanded, a.prefs.RailSessionsOpen = true, true
		for i := range total {
			a.sessions = append(a.sessions, backend.SessionIndexEntry{
				ID: fmt.Sprint("s", i), Title: fmt.Sprint("session ", i), Workspace: "/work/alpha", UpdatedAt: 1700000000, MessageCount: 3,
			})
		}
		a.sessRev = 1
		tt := ui.NewTester(func(c *ui.Context) {
			c.SetTheme(a.theme())
			c.DisableAnimations()
			ui.Column(c).Fill().Children(func() {
				ui.Box(c).Height(heightHeader).Shrink(0)
				ui.Split(c, &a.prefs.RailWidth, func() { a.rail(c) }, func() { ui.Column(c).Fill() }).Grow(1).MinHeight(0).FillWidth()
			})
		}, 1000, 850)
		tt.Frame()
		allocs, perFrame := frameCost(tt)
		t.Logf("sessions=%d  %.2fms/frame  %.0f allocs/frame", total, float64(perFrame.Microseconds())/1000, allocs)
		if !tt.HasText("session 0") {
			t.Fatal("the newest session was not built")
		}
		if tt.HasText("session 79") {
			t.Fatal("a session below the fold was built")
		}
		if total == 80 {
			smallest = allocs
			continue
		}
		if allocs > smallest*1.2 {
			t.Fatalf("%.0f allocations a frame with %d sessions against %.0f with 80", allocs, total, smallest)
		}
	}
}
