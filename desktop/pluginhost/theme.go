// SPDX-License-Identifier: GPL-3.0-only
package pluginhost

import (
	"fmt"
	"github.com/egoist/mygo/ui"
)

// Palette is the only native theme data needed by the v1 CSS token contract.
type Palette struct {
	Name                                                                  string
	Dark                                                                  bool
	Bg, Panel, Chrome, Line, Accent, AccentText, Text, Muted, Hover, Hint ui.Color
}

func NewTheme(p Palette) Theme {
	mode := "light"
	if p.Dark {
		mode = "dark"
	}
	color := func(c ui.Color) string { return fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B) }
	tokens := map[string]string{}
	groups := map[string]ui.Color{
		"surface-content": p.Bg, "surface-panel": p.Panel, "surface-chrome": p.Chrome, "surface-raised": p.Panel, "surface-deep": p.Bg,
		"text-primary": p.Text, "text-high": p.Text, "text-body": p.Text, "text-secondary": p.Muted, "text-soft": p.Muted, "text-tertiary": p.Muted, "text-muted": p.Muted, "text-faint": p.Muted, "text-ghost": p.Hint,
		"accent": p.Accent, "accent-ink": p.AccentText, "accent-bright": p.Accent, "accent-strong": p.Accent, "hover-faint": p.Hover, "hover-strong": p.Hover, "border-input": p.Line,
		"success": ui.Hex("#6fbf8f"), "success-text": ui.Hex("#6fbf8f"), "danger": ui.Hex("#d98585"), "danger-text": ui.Hex("#d98585"), "warning": ui.Hex("#d8b16b"), "warning-text": ui.Hex("#d8b16b"), "info": p.Accent,
	}
	for k, v := range groups {
		tokens["--ally-"+k] = color(v)
	}
	tokens["--ally-ui-font"] = "Inter, system-ui, sans-serif"
	tokens["--ally-mono-font"] = "Consolas, monospace"
	tokens["--ally-message-font-size"] = "14px"
	tokens["--ally-sub-font-size"] = "13px"
	tokens["--ally-aux-font-size"] = "12px"
	tokens["--ally-composer-blur"] = "none"
	tokens["--ally-panel-glass"] = color(p.Panel)
	return Theme{Name: p.Name, Mode: mode, Tokens: tokens}
}
