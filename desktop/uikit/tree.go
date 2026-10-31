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

// GitTreeItem keeps MyGo's disclosure, focus and keyboard behavior while giving
// each piece of the label its own status color. Counts keep their space when
// a long filename is ellipsized.
func GitTreeItem(c *ui.Context, name, status string, added, deleted int, open *bool, children func()) ui.Element {
	t := c.Theme()
	ink := t.Warning
	switch status {
	case "A", "?":
		ink = t.Success
	case "D":
		ink = t.Danger
	case "R":
		ink = t.Accent
	}
	row := ui.TreeItem(c, "", open, children).Gap(0).Label(name)
	row.Children(func() {
		ui.Row(c).Grow(1).MinWidth(0).Gap(6).PaddingStart(2).Children(func() {
			ui.Text(c, name).Grow(1).MinWidth(0).SingleLine().TextColor(ink)
			if children == nil {
				ui.Text(c, status).Shrink(0).TextColor(ink)
				if added > 0 {
					ui.Textf(c, "+%d", added).Shrink(0).TextColor(t.Success)
				}
				if deleted > 0 {
					ui.Textf(c, "−%d", deleted).Shrink(0).TextColor(t.Danger)
				}
			}
		})
	})
	return row
}
