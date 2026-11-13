//go:build ally_explorer

// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"image"
	"image/color"
	"testing"

	backend "ally-dev/internal/app"
	"github.com/egoist/mygo/ui"
)

// TestOnDemandExplorerRowStyle pins the tree's look to what it had before the
// outline: 23 pixel rows, a chosen row in the quiet tint of the panel color
// rather than the list's accent, and the name behind a 4 + 16 a level indent,
// a 16 pixel arrow and a 2 pixel gap. The row draws these itself; a frame that
// hands them back to the outline changes the look, which this catches.
func TestOnDemandExplorerRowStyle(t *testing.T) {
	a := &desktop{prefs: defaultPrefs(), page: "chat", tabs: []*conversation{{Workspace: "/work/style"}}}
	a.files = []backend.FileEntry{
		{Path: "src", Name: "src", Dir: true},
		{Path: "README.md", Name: "README.md"},
	}
	a.fileChildren = map[string][]backend.FileEntry{
		"src": {{Path: "src/main.go", Name: "main.go"}},
	}
	a.workspaceFiles = map[string]*workspaceFile{workspaceKey("/work/style"): {Path: "src/main.go"}}
	a.fileOutline.Open.Add("src")
	a.reindexFileTree()
	tt := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(a.theme())
		c.DisableAnimations()
		ui.Row(c).Fill().AlignItems(ui.Stretch).Children(func() { a.fileTree(c) })
	}, 320, 420)
	tt.Frame()
	tt.Frame()

	dir, ok := tt.Find("src")
	if !ok {
		t.Fatal("missing the directory row")
	}
	file, ok := tt.Find("main.go")
	if !ok {
		t.Fatal("missing the file row")
	}
	if dir.H != 23 || file.H != 23 {
		t.Fatalf("rows are %.0f and %.0f high, want 23", dir.H, file.H)
	}
	if file.Y-dir.Y != 23 {
		t.Fatalf("rows are %.0f apart, want 23", file.Y-dir.Y)
	}

	shot := tt.Image()
	if shot.Bounds().Dy() != 420 {
		t.Skip("the tester scales its image")
	}
	panel := a.currentPalette().bg
	accent := a.theme().Accent
	row := func(y float32) image.Rectangle {
		top, bottom := int(y)+1, int(y+dir.H)-1
		return image.Rect(8, top, int(dir.W)-8, bottom)
	}
	chosen := shot.RGBAAt(row(file.Y).Max.X, row(file.Y).Min.Y)
	if chosen != (color.RGBA{R: panel.R, G: panel.G, B: panel.B, A: 255}) {
		// A chosen row keeps the panel color under a faint tint: neither bare
		// nor the list's accent.
		if chosen == (color.RGBA{R: accent.R, G: accent.G, B: accent.B, A: 255}) {
			t.Fatal("the chosen row is filled with the accent color")
		}
	} else {
		t.Fatal("the chosen row is not tinted at all")
	}
	if plain := shot.RGBAAt(row(dir.Y).Max.X, row(dir.Y).Min.Y); plain != (color.RGBA{R: panel.R, G: panel.G, B: panel.B, A: 255}) {
		t.Fatalf("a row that is not chosen is filled: %v", plain)
	}

	// The name of a file one level in starts after the indent, the arrow and
	// the gap: 4 + 16 + 16 + 2.
	band := row(file.Y)
	ink := -1
	for x := band.Min.X; x < band.Max.X; x++ {
		if px := shot.RGBAAt(x, band.Min.Y+band.Dy()/2); px != chosen {
			ink = x
			break
		}
	}
	if ink < 36 || ink > 42 {
		t.Fatalf("the name starts at %d, want 38", ink)
	}
}

// TestOnDemandExplorerChosenDirArrow keeps the arrow of a chosen directory
// readable. The row is only tinted, so an arrow in the accent text color, which
// is meant to sit on the accent fill, turns black on the tint.
func TestOnDemandExplorerChosenDirArrow(t *testing.T) {
	a := &desktop{prefs: defaultPrefs(), page: "chat", tabs: []*conversation{{Workspace: "/work/arrow"}}}
	a.files = []backend.FileEntry{
		{Path: "src", Name: "src", Dir: true},
		{Path: "README.md", Name: "README.md"},
	}
	a.fileChildren = map[string][]backend.FileEntry{
		"src": {{Path: "src/main.go", Name: "main.go"}},
	}
	a.workspaceFiles = map[string]*workspaceFile{workspaceKey("/work/arrow"): {Path: "src"}}
	a.fileOutline.Open.Add("src")
	a.reindexFileTree()
	tt := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(a.theme())
		c.DisableAnimations()
		ui.Row(c).Fill().AlignItems(ui.Stretch).Children(func() { a.fileTree(c) })
	}, 320, 420)
	tt.Frame()
	tt.Frame()
	row, ok := tt.Find("src")
	if !ok {
		t.Fatal("missing the directory row")
	}
	if a.fileChosenPath() != "src" {
		t.Fatalf("the chosen row is %q, want src", a.fileChosenPath())
	}
	shot := tt.Image()
	if shot.Bounds().Dy() != 420 {
		t.Skip("the tester scales its image")
	}
	bright := func(r, g, b uint8) int { return (int(r) + int(g) + int(b)) / 3 }
	muted := a.theme().TextMuted
	want := bright(muted.R, muted.G, muted.B)
	best := 0
	for x := int(row.X) + 4; x < int(row.X)+20; x++ {
		for y := int(row.Y) + 4; y < int(row.Y+row.H)-4; y++ {
			px := shot.RGBAAt(x, y)
			if l := bright(px.R, px.G, px.B); l > best {
				best = l
			}
		}
	}
	if best*10 < want*6 {
		t.Fatalf("the chosen directory's arrow is %d bright, want about %d", best, want)
	}
}
