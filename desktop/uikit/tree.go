package uikit

import "github.com/egoist/mygo/ui"

// TreeSelection retains TreeItem's native selection/keyboard semantics and
// gives files a quiet tint instead of the full-strength primary action fill.
func TreeSelection(c *ui.Context, row ui.Element, selected bool) ui.Element {
	row.Selected(selected)
	if selected {
		row.Background(SelectedFill(c.Theme().Accent)).TextColor(c.Theme().Text)
	}
	return row
}
