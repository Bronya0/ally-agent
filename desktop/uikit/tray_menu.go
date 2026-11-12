package uikit

import (
	"math"

	"github.com/egoist/mygo/ui"
)

const (
	trayMenuMinWidth  float32 = 140
	trayMenuMaxWidth  float32 = 480
	trayMenuRowHeight float32 = 28
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
	Key, Label      string
	Icon            *ui.SVG
	SeparatorBefore bool
	Disabled        bool
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
	root.Size(width+2*TrayMenuShadowInset, height+2*TrayMenuShadowInset).Shrink(0)
	var anchor ui.Element
	root.Children(func() {
		// AttachTo counts presses on its target as inside the popup. A point
		// anchor leaves the shadow margin outside rather than trapping clicks.
		anchor = ui.Box(c.Key("anchor")).Absolute().Left(TrayMenuShadowInset).Top(TrayMenuShadowInset).Size(0, 0)
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
		for i, entry := range entries {
			if i > 0 && entry.SeparatorBefore {
				ui.Divider(c).Height(1).Shrink(0)
			}
			ui.Column(c.Key(entry.Key)).FillWidth().Shrink(0).PaddingX(trayMenuRowInset).Children(func() {
				row := m.Item().FillWidth().Height(memo.row).Shrink(0).Padding(0, trayMenuRowPad).
					Gap(trayMenuIconGap).Radius(RadiusSmall).Label(entry.Label).Disabled(entry.Disabled)
				fill := ui.Transparent
				if !entry.Disabled {
					if row.Highlighted() || row.Hovered() {
						fill = c.Theme().SurfaceHover
					}
					if row.Pressed() {
						fill = c.Theme().SurfacePressed
					}
				}
				row.Background(fade(row, "tray-hover", fill)).TextColor(c.Theme().Text)
				row.Children(func() {
					if entry.Icon != nil {
						ui.Icon(c, entry.Icon).Size(trayMenuIconSize, trayMenuIconSize).Shrink(0)
					}
					ui.Text(c, entry.Label).FontSize(c.Theme().FontSize).SingleLine().MinWidth(0)
				})
				if row.Clicked() {
					choice = i
				}
			})
		}
	})
	return choice
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
		m.width = max(m.width, float32(math.Ceil(float64(w)))+2*(trayMenuBorder+trayMenuRowInset+trayMenuRowPad)+trayMenuIconSize+trayMenuIconGap)
		m.row = max(m.row, float32(math.Ceil(float64(h)))+2*trayMenuTextPadY)
		m.labels = append(m.labels, entry.Label)
	}
}
