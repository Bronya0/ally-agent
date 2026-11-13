//go:build ally_explorer

// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"fmt"
	"testing"
	"time"

	backend "ally-dev/internal/app"
	"github.com/egoist/mygo/ui"
)

// onDemandTree builds the explorer on a workspace holding one expanded
// directory with rows entries, laid out once.
func onDemandTree(t *testing.T, rows int) (*desktop, *ui.Tester) {
	t.Helper()
	a := &desktop{prefs: defaultPrefs(), page: "chat", tabs: []*conversation{{Workspace: "/work/scale"}}}
	a.files = []backend.FileEntry{
		{Path: "src", Name: "src", Dir: true},
		{Path: "README.md", Name: "README.md"},
	}
	a.fileOutline.Open.Add("src")
	children := make([]backend.FileEntry, 0, rows)
	for i := range rows {
		name := fmt.Sprintf("f-%05d.go", i)
		children = append(children, backend.FileEntry{Path: "src/" + name, Name: name})
	}
	a.fileChildren = map[string][]backend.FileEntry{"src": children}
	a.reindexFileTree()
	tt := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(a.theme())
		c.DisableAnimations()
		ui.Row(c).Fill().AlignItems(ui.Stretch).Children(func() { a.fileTree(c) })
	}, 320, 500)
	tt.Frame()
	return a, tt
}

// frameCost measures a frame of the tree: counted allocations, which do not
// depend on how busy the machine is, and the mean time for the report.
func frameCost(tt *ui.Tester) (float64, time.Duration) {
	allocs := testing.AllocsPerRun(20, tt.Frame)
	start := time.Now()
	for range 50 {
		tt.Frame()
	}
	return allocs, time.Since(start) / 50
}

// TestOnDemandExplorerTreeScale reports what a frame of the tree costs at
// 1000 to 10000 open rows: the rows outside the view must not be built, so
// only the flattening of the open rows may grow.
func TestOnDemandExplorerTreeScale(t *testing.T) {
	smallest := 0.0
	for _, rows := range []int{1000, 2000, 8000, 10000} {
		a, tt := onDemandTree(t, rows)
		allocs, perFrame := frameCost(tt)
		t.Logf("rows=%d  %.2fms/frame  %.0f allocs/frame  outline rows=%d", rows, float64(perFrame.Microseconds())/1000, allocs, a.fileOutline.Rows())
		if a.fileOutline.Rows() != rows+2 {
			t.Fatalf("rows=%d: the outline holds %d rows", rows, a.fileOutline.Rows())
		}
		if rows == 1000 {
			smallest = allocs
			continue
		}
		if allocs > smallest*1.2 {
			t.Fatalf("rows=%d: %.0f allocations a frame against %.0f at 1000 rows", rows, allocs, smallest)
		}
	}

	a, tt := onDemandTree(t, 10000)
	if !tt.HasText("f-00000.go") {
		t.Fatal("the first row in view was not built")
	}
	if tt.HasText("f-09000.go") {
		t.Fatal("a row far below the view was built")
	}
	if a.fileOutline.Rows() != 10002 {
		t.Fatalf("the outline lost rows: %d", a.fileOutline.Rows())
	}
}

// TestOnDemandExplorerSearchScale searches a workspace whose tree already holds
// ten thousand rows: the results replace the rows and build only those in view.
func TestOnDemandExplorerSearchScale(t *testing.T) {
	a, tt := onDemandTree(t, 10000)
	entries, children, opened := explorerSearchTree([]backend.FileEntry{
		{Path: "src/one.go"}, {Path: "src/two.go"}, {Path: "deep/three.go"},
	})
	a.files, a.fileChildren = entries, children
	a.resetFileOutline()
	for path := range opened {
		a.fileOutline.Open.Add(path)
	}
	a.reindexFileTree()
	allocs, perFrame := frameCost(tt)
	t.Logf("search results: %.2fms/frame  %.0f allocs/frame", float64(perFrame.Microseconds())/1000, allocs)
	if !tt.HasText("one.go") || !tt.HasText("three.go") {
		t.Fatal("the search results are missing")
	}
}
