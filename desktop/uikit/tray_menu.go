package uikit

import (
	"math"

	"github.com/egoist/mygo/ui"
)

const (
	trayMenuMinWidth   float32 = 140
	trayMenuChildWidth float32 = 160
	trayMenuMaxWidth   float32 = 480
	trayMenuRowHeight  float32 = 28
	// TrayMenuShadowInset is the transparent margin around the panel, in DIPs.
	TrayMenuShadowInset float32 = 8
	trayMenuPadY        float32 = 3
	trayMenuRowInset    float32 = 4
	trayMenuRowPad      float32 = 8
	trayMenuTextPadY    float32 = 4
	trayMenuIconSize    float32 = 16
	trayMenuIconGap     float32 = 6
	trayMenuBorder      float32 = 1
	trayMenuRadius      float32 = 7
)

// TrayMenuEntry contains presentation data only. Key must be stable and unique
// among entries. The host dispatches the selected action after the window build.
type TrayMenuEntry struct {
	Key, Label         string
	AccessibilityLabel string
	Icon               *ui.SVG
	StatusColor        ui.Color
	Checkable, Checked bool
	SeparatorBefore    bool
	Disabled           bool
	Children           []TrayMenuEntry
}

// TrayMenuOptions configures the menu surface. MaxWidth is in DIPs and excludes
// the shadow. Zero or a non-finite value uses the default; positive values below
// 140 are raised to the minimum width.
type TrayMenuOptions struct {
	MaxWidth float32
}

// TrayMenuTheme returns the compact neutral light palette. Apply the host's
// accessibility preferences with WithPreferences before setting the theme.
func TrayMenuTheme(font string) *ui.Theme {
	t := ui.LightTheme()
	t.Background = ui.Transparent
	t.Surface = ui.Hex("#fafafa")
	t.SurfaceHover = ui.Hex("#ededed")
	t.SurfacePressed = ui.Hex("#e4e4e4")
	t.Border = ui.Hex("#e6e6e6")
	t.Text = ui.Hex("#181d27")
	t.Font = font
	t.FontSize = FontTiny
	return t
}

// TrayMenuInitialSize includes space for the self-drawn shadow. FitContent
// later grows this footprint if translated labels or larger text need it.
func TrayMenuInitialSize(actions, separators int) (int, int) {
	return int(trayMenuMinWidth + 2*TrayMenuShadowInset),
		int(float32(actions)*trayMenuRowHeight + float32(separators) + 2*(trayMenuPadY+trayMenuBorder+TrayMenuShadowInset))
}

func trayMenuSubmenuWidth(entries []TrayMenuEntry) float32 {
	for _, entry := range entries {
		if len(entry.Children) > 0 {
			return trayMenuChildWidth
		}
	}
	return 0
}

type trayMenuMeasure struct {
	font, locale string
	fontSize     float32
	labels       []string
	width, row   float32
}

// TrayMenu draws a single menu surface in a transparent flyout. ContextMenuBase
// still owns selection, keyboard navigation, focus, Escape and outside presses.
// The regular root declares its footprint because absolute menus do not count
// toward a native flyout's FitContent measurement. label is the accessible
// menu name. Schedule flyout destruction and action dispatch after the build.
func TrayMenu(c *ui.Context, open *bool, label string, entries []TrayMenuEntry, options ...TrayMenuOptions) int {
	maxWidth := trayMenuMaxWidth
	if len(options) > 0 {
		v := options[0].MaxWidth
		if v > 0 && !math.IsInf(float64(v), 0) && !math.IsNaN(float64(v)) {
			maxWidth = max(trayMenuMinWidth, v)
		}
	}
	root := ui.Box(c).Background(ui.Transparent)
	memo := ui.Local(root, "tray-measure", func() trayMenuMeasure { return trayMenuMeasure{} })
	memo.measure(c, entries)
	width := min(memo.width, maxWidth)
	height := 2 * (trayMenuPadY + trayMenuBorder)
	for i, entry := range entries {
		height += memo.row
		if i > 0 && entry.SeparatorBefore {
			height++
		}
	}
	childWidth := trayMenuSubmenuWidth(entries)
	childHeight := float32(0)
	for _, entry := range entries {
		if len(entry.Children) > 0 {
			childHeight = max(childHeight, 2*(trayMenuPadY+trayMenuBorder)+float32(min(len(entry.Children), 8))*memo.row)
		}
	}
	root.Size(width+childWidth+2*TrayMenuShadowInset, max(height, childHeight)+2*TrayMenuShadowInset).Shrink(0)
	var anchor ui.Element
	root.Children(func() {
		// AttachTo counts presses on its target as inside the popup. A point
		// anchor leaves the shadow margin outside rather than trapping clicks.
		anchor = ui.Box(c.Key("anchor")).Absolute().Left(TrayMenuShadowInset+childWidth).Top(TrayMenuShadowInset+max(0, childHeight-height)).Size(0, 0)
	})
	m := ui.ContextMenuBase(c, anchor, open)
	choice := -1
	m.Popup(func(panel ui.Element) {
		w, h := c.Size()
		panel.AttachTo(anchor, ui.AnchorTopLeft, ui.AnchorTopLeft).AttachEdgeMargin(TrayMenuShadowInset).
			Width(width).Height(height).MaxWidth(max(1, w-2*TrayMenuShadowInset)).MaxHeight(max(1, h-2*TrayMenuShadowInset)).
			Padding(trayMenuPadY, 0).Background(c.Theme().Surface).
			Border(trayMenuBorder, c.Theme().Border).Radius(trayMenuRadius).
			Shadow(0, 2, 6, 0, ui.RGBA(0, 0, 0, 0.10)).Label(label).Scrollbars(false)
		childOffset := len(entries)
		for i, entry := range entries {
			startChild := childOffset
			childOffset += len(entry.Children)
			if i > 0 && entry.SeparatorBefore {
				ui.Divider(c).Height(1).Shrink(0)
			}
			ui.Column(c.Key(entry.Key)).FillWidth().Shrink(0).PaddingX(trayMenuRowInset).Children(func() {
				if len(entry.Children) == 0 {
					if trayMenuRow(c, m, entry, memo.row).Clicked() && !entry.Disabled {
						choice = i
					}
					return
				}
				row := trayMenuRow(c, m, entry, memo.row)
				m.Submenu(row, func(sub ui.Element) {
					sub.Width(childWidth).MaxHeight(max(1, h-2*TrayMenuShadowInset)).Padding(trayMenuPadY, 0).Background(c.Theme().Surface).
						Border(trayMenuBorder, c.Theme().Border).Radius(trayMenuRadius).
						Shadow(0, 2, 6, 0, ui.RGBA(0, 0, 0, 0.10))
					for j, child := range entry.Children {
						ui.Column(c.Key(child.Key)).FillWidth().Shrink(0).PaddingX(trayMenuRowInset).Children(func() {
							if trayMenuRow(c, m, child, memo.row).Clicked() && !child.Disabled {
								choice = startChild + j
							}
						})
					}
				})
			})
		}
	})
	return choice
}

func trayMenuRow(c *ui.Context, menu ui.MenuParts, entry TrayMenuEntry, height float32) ui.Element {
	var row ui.Element
	hasChildren := len(entry.Children) > 0
	switch {
	case hasChildren:
		row = menu.SubmenuItem()
	case entry.Checkable:
		row = menu.RadioItem(entry.Checked)
	default:
		row = menu.Item()
	}
	label := entry.AccessibilityLabel
	if label == "" {
		label = entry.Label
	}
	row.FillWidth().Height(height).Shrink(0).Padding(0, trayMenuRowPad).
		Gap(trayMenuIconGap).Radius(RadiusSmall).Label(label).Disabled(entry.Disabled)
	theme := c.Theme()
	fill := ui.Transparent
	if !entry.Disabled {
		if row.Highlighted() || row.Hovered() {
			fill = theme.SurfaceHover
		}
		if row.Pressed() {
			fill = theme.SurfacePressed
		}
	}
	row.Background(fade(row, "tray-hover", fill)).TextColor(theme.Text).Children(func() {
		switch {
		case entry.StatusColor.A > 0:
			ui.Box(c).Size(trayMenuIconSize, trayMenuIconSize).Shrink(0).Center().Children(func() {
				ui.Box(c).Size(10, 10).Shrink(0).Radius(5).Background(entry.StatusColor)
			})
		case entry.Checkable && !entry.Checked:
			ui.Box(c).Size(trayMenuIconSize, trayMenuIconSize).Shrink(0)
		case entry.Icon != nil:
			ui.Icon(c, entry.Icon).Size(trayMenuIconSize, trayMenuIconSize).Shrink(0)
		}
		text := ui.Text(c, entry.Label).FontSize(theme.FontSize).SingleLine().MinWidth(0)
		if hasChildren {
			text.Grow(1)
			ui.Text(c, "›").FontSize(theme.FontSize + 3).Shrink(0)
		}
	})
	return row
}

// Cache actual text measurements by the labels and font, not rune counts. This
// keeps English/CJK labels intact without shaping text again on every hover.
func (m *trayMenuMeasure) measure(c *ui.Context, entries []TrayMenuEntry) {
	t := c.Theme()
	changed := m.font != t.Font || m.fontSize != t.FontSize || m.locale != c.Locale() || len(m.labels) != len(entries)
	if !changed {
		for i, entry := range entries {
			if m.labels[i] != entry.Label {
				changed = true
				break
			}
		}
	}
	if !changed {
		return
	}
	m.font, m.fontSize, m.locale = t.Font, t.FontSize, c.Locale()
	m.labels = m.labels[:0]
	m.width, m.row = trayMenuMinWidth, trayMenuRowHeight
	for _, entry := range entries {
		w, h := c.MeasureText(0, ui.Span{Text: entry.Label, Size: t.FontSize, Font: t.Font})
		width := float32(math.Ceil(float64(w))) + 2*(trayMenuBorder+trayMenuRowInset+trayMenuRowPad) + trayMenuIconSize + trayMenuIconGap
		if len(entry.Children) > 0 {
			width += trayMenuIconGap + t.FontSize
		}
		m.width = max(m.width, width)
		m.row = max(m.row, float32(math.Ceil(float64(h)))+2*trayMenuTextPadY)
		m.labels = append(m.labels, entry.Label)
	}
}
