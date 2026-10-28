// SPDX-License-Identifier: GPL-3.0-only
package desktop

// This file is the single source of truth for visual tokens: colours, spacing,
// radii, font sizes and the stroke icon set. Page code must not hard-code
// colours or magic sizes; it composes the widgets in ui_widgets.go instead.

import (
	"ally-dev/desktop/uikit"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// ---- Motion --------------------------------------------------------------

// motionFade is the shared hover and focus colour transition. Borders and fills
// snap by default, which reads as a jolt the instant one lights up; every
// interactive surface fades the same way instead.
const motionFade = 140 * time.Millisecond

// fadeColor animates a hover/focus colour on e. It needs a stable element key:
// an element rebuilt without one smooths nothing, because the animation state
// lives on the node, which is why interactive widgets are created with c.Key.
func fadeColor(e ui.Element, key string, target ui.Color) ui.Color {
	return e.AnimateColor(key, target, ui.Animation{Duration: motionFade, Ease: ui.EaseInOut})
}

// ---- Design tokens -------------------------------------------------------

const (
	gapTight   float32 = 4
	gapInline  float32 = 6
	gapField   float32 = 8
	gapSection float32 = 12
	gapPage    float32 = 18
	padRow     float32 = 8
	padCard    float32 = 10
	padPage    float32 = 22

	radiusCard    float32 = 8
	radiusControl float32 = 7
	radiusSmall   float32 = 5

	heightHeader       float32 = 44
	heightRail         float32 = 44
	heightControl      float32 = 28
	heightRow          float32 = 26
	heightTabRow       float32 = 32
	heightRailItem     float32 = 34
	widthRailCollapsed float32 = 44
	// railPadH is the collapsed rail's horizontal padding.
	railPadH float32 = 2
	// heightRailItemCollapsed is the side of a collapsed entry: both sides take
	// this one token, so a selected entry is a square and cannot come out as a
	// slightly flat rectangle. It has to fit what the rail leaves inside its 1px
	// border and that padding (44 - 1 - 4 = 39 DIP).
	heightRailItemCollapsed float32 = 38
	sizeRailBadge           float32 = 34
	sizeRailIcon            float32 = 17
	sizeRailIconCollapsed   float32 = 20
	sizeRailToggleIcon      float32 = 16
	radiusRailBadge         float32 = 6
	widthModalWide          float32 = 760
	widthModal              float32 = 580
	widthModalSmall         float32 = 460

	fsPageTitle float32 = 20
	fsSection   float32 = 13
	fsBody      float32 = 14
	fsSmall     float32 = 13
	fsTiny      float32 = 12
	// Chat-parity tokens, mirroring main's CSS variables (--ally-tool-font-size
	// 15px for tool verbs, --ally-aux-font-size 12px for support lines).
	fsTool float32 = 15
	fsAux  float32 = 12
)

// Semantic colours derived from the theme rather than fixed per page.
func colBg(a *desktop) ui.Color     { return a.currentPalette().bg }
func colPanel(a *desktop) ui.Color  { return a.currentPalette().panel }
func colLine(a *desktop) ui.Color   { return a.currentPalette().line }
func colAccent(a *desktop) ui.Color { return a.currentPalette().accent }
func colText(a *desktop) ui.Color   { return a.currentPalette().text }
func colMuted(a *desktop) ui.Color  { return a.currentPalette().muted }
func colHover(a *desktop) ui.Color  { return a.currentPalette().hover }

func colSuccess() ui.Color { return okColor }
func colWarning() ui.Color { return warnCol }
func colDanger() ui.Color  { return errCol }
func colFaint() ui.Color   { return faint }

// ---- Selection -----------------------------------------------------------
// One selected look for the whole desktop, so a page never assembles its own
// "this one is chosen" styling. selectionFill and selectionEdge come from the
// shared kit tokens; selectionInk is the accent every selected label uses.

func selectionFill(tone ui.Color) ui.Color { return uikit.SelectedFill(tone) }
func selectionEdge(tone ui.Color) ui.Color { return uikit.SelectedStrokeColor(tone) }

// palette is one selectable appearance preset (mode + accent combined, as the
// original settings page offered five looks).
type palette struct {
	name                                                    string
	dark                                                    bool
	bg, panel, line, accent, accentText, text, muted, hover ui.Color
}

// Resting edges — cards, chips, inputs, buttons, popups — are the palette line
// as-is. They used to fold 14–16% of the text colour in, which drew a stroke
// brighter than the panel behind it, so every surface read as a bright outline.
// Only hover and emphasis may lift an edge, and only by this much.
const edgeHoverMix float32 = 0.18

var palettes = []palette{
	{name: "暗夜", dark: true, bg: ui.Hex("#1b1b1b"), panel: ui.Hex("#212122"), line: ui.Hex("#303031"), accent: ui.Hex("#8da5df"), accentText: ui.Hex("#15171d"), text: ui.Hex("#d4d4d4"), muted: ui.Hex("#909093"), hover: ui.Hex("#2b2b2e")},
	{name: "亮色", dark: false, bg: ui.Hex("#f5f6f8"), panel: ui.Hex("#ffffff"), line: ui.Hex("#d9dce3"), accent: ui.Hex("#4d6bd8"), accentText: ui.Hex("#ffffff"), text: ui.Hex("#24262d"), muted: ui.Hex("#535b68"), hover: ui.Hex("#eef0f5")},
	{name: "琥珀", dark: true, bg: ui.Hex("#1c1a17"), panel: ui.Hex("#232019"), line: ui.Hex("#363128"), accent: ui.Hex("#e0a458"), accentText: ui.Hex("#1a1712"), text: ui.Hex("#d7d2c8"), muted: ui.Hex("#9b9488"), hover: ui.Hex("#2c2820")},
	{name: "海洋", dark: true, bg: ui.Hex("#151a1f"), panel: ui.Hex("#1a2027"), line: ui.Hex("#28323c"), accent: ui.Hex("#63b3d8"), accentText: ui.Hex("#0d1418"), text: ui.Hex("#c6d0d8"), muted: ui.Hex("#8896a2"), hover: ui.Hex("#222b34")},
	{name: "森林", dark: true, bg: ui.Hex("#151a17"), panel: ui.Hex("#1a201c"), line: ui.Hex("#29362d"), accent: ui.Hex("#6fbf8f"), accentText: ui.Hex("#0e1511"), text: ui.Hex("#c4d2c8"), muted: ui.Hex("#889a8e"), hover: ui.Hex("#212b24")},
	{name: "紫罗兰", dark: true, bg: ui.Hex("#191722"), panel: ui.Hex("#1f1c29"), line: ui.Hex("#312c42"), accent: ui.Hex("#a68bea"), accentText: ui.Hex("#141019"), text: ui.Hex("#cfc8de"), muted: ui.Hex("#948aa8"), hover: ui.Hex("#292436")},
}

func (a *desktop) currentPalette() palette {
	idx := a.prefs.Palette
	if idx < 0 || idx >= len(palettes) {
		idx = 2
	}
	base := 0
	if a.prefs.Appearance == "light" || (a.prefs.Appearance == "" && idx == 1) {
		base = 1
	}
	p := palettes[base]
	p.accent = palettes[idx].accent
	if !p.dark {
		// Palette accents are tuned for dark surfaces; deepen them for readable
		// labels, icons and focus states against the light surface.
		if idx == 2 {
			p.accent = ui.Hex("#9b6218")
		} else {
			p.accent = p.accent.Mix(p.text, 0.5)
		}
	}
	if base == 0 {
		p.accentText = palettes[0].accentText
	} else {
		p.accentText = palettes[1].accentText
	}
	return p
}

// wordmarkGradientColor approximates the original silver → accent → cool
// silver → silver gradient at a point across the four-letter wordmark.
func wordmarkGradientColor(p palette, at float32) ui.Color {
	silver := ui.Hex("#f8fafc")
	coolSilver := ui.Hex("#d7dde8")
	if !p.dark {
		// Keep the wordmark legible on the white header while retaining its
		// silver-to-accent gradient.
		silver = ui.Hex("#4d5868")
		coolSilver = ui.Hex("#697487")
	}
	switch {
	case at <= 0.4:
		return silver.Mix(p.accent, at/0.4)
	case at <= 0.75:
		return p.accent.Mix(coolSilver, (at-0.4)/0.35)
	default:
		return coolSilver.Mix(silver, (at-0.75)/0.25)
	}
}

// syncNativeAppearance keeps the OS frame in the same appearance as the
// custom UI. Setting the ui.Context theme alone does not theme the HWND;
// Windows 10 otherwise leaves a white sizing border around a dark window.
// Call on appearance changes, never during view construction.
func (a *desktop) syncNativeAppearance() {
	p := a.currentPalette()
	source := mygo.ThemeLight
	if p.dark {
		source = mygo.ThemeDark
	}
	mygo.Theme.SetSource(source)
	if a.win != nil {
		_ = a.win.SetBackgroundColor(fmt.Sprintf("#%02x%02x%02x", p.bg.R, p.bg.G, p.bg.B))
	}
}

func (a *desktop) theme() *ui.Theme {
	p := a.currentPalette()
	t := ui.DarkTheme()
	if !p.dark {
		t = ui.LightTheme()
	}
	t.Background = p.bg
	t.Surface = p.panel
	t.SurfaceHover = p.hover
	t.SurfacePressed = p.hover
	t.Border = p.line
	t.Accent = p.accent
	t.AccentHover = p.accent.Mix(ui.Hex("#ffffff"), 0.18)
	t.AccentPressed = p.accent.Mix(ui.Hex("#000000"), 0.12)
	t.AccentText = p.accentText
	t.Text = p.text
	t.TextMuted = p.muted
	t.Danger = errCol
	t.Success = okColor
	t.Warning = warnCol
	t.Focus = p.accent
	t.Font = nativeFontFamily()
	t.FontSize = 13
	t.Radius = radiusControl
	return t
}

// Raw base colours used by the token presets above.
var (
	bg       = ui.Hex("#1b1b1b")
	panel    = ui.Hex("#212122")
	panelAlt = ui.Hex("#1f1f20")
	line     = ui.Hex("#333335")
	green    = ui.Hex("#8da5df")
	hoverBg  = ui.Hex("#2b2b2e")
	softText = ui.Hex("#9a9a9f")
	faint    = ui.Hex("#6f6f74")
	okColor  = ui.Hex("#4ec87e")
	warnCol  = ui.Hex("#d9a441")
	errCol   = ui.Hex("#e85454")
)

// ---- Icons ---------------------------------------------------------------

var iconPaths = map[string]string{
	"chat":        `<path d="M4 5h16v11H9l-5 4z"/><path d="M8 9h8M8 12.5h5"/>`,
	"kb":          `<path d="M13 2 5 14h5l-1 8 9-13h-5z"/>`,
	"skills":      `<path d="m9 15-2 2a4 4 0 0 1-5-5l4-4a4 4 0 0 1 5 0m2 1 2-2a4 4 0 0 1 5 5l-4 4a4 4 0 0 1-5 0M8 16l8-8"/>`,
	"mcp":         `<rect x="4" y="3" width="16" height="16" rx="2"/><path d="M4 15h16M8 7v3m8-3v3M8 22h8"/>`,
	"models":      `<rect x="8" y="2" width="8" height="5" rx="1"/><path d="M12 7v5M4 12h16M4 12v4m8-4v4m8-4v4"/><rect x="1" y="16" width="6" height="5" rx="1"/><rect x="9" y="16" width="6" height="5" rx="1"/><rect x="17" y="16" width="6" height="5" rx="1"/>`,
	"ssh":         `<path d="M4 16v-4a8 8 0 0 1 16 0v4M4 12H2v7h5v-7zm16 0h2v7h-5v-7zM17 21h-5"/>`,
	"stats":       `<path d="M3 3v18h19M7 16v-5m5 5V6m5 10V9"/>`,
	"tasks":       `<rect x="3" y="3" width="18" height="18" rx="2"/><path d="M8 3v18M12 7h5m-5 4h5m-5 4h3"/>`,
	"games":       `<path d="M6 4l12 16M18 4L6 20M4 8l4-4m8 0 4 4M4 16l4 4m8 0 4-4"/>`,
	"plugins":     `<path d="M10 3h4v3a2 2 0 1 0 4 0h3v4h-3a2 2 0 1 0 0 4h3v4h-4a2 2 0 1 0-4 0H6v-4a2 2 0 1 0 0-4H3V6h4a2 2 0 1 0 3-3z"/>`,
	"settings":    `<path d="m9 3 1-1h4l1 1 1 3 3 1 1 4-1 2-1 3v3l-3 2-3-1h-3l-3 1-3-2v-3l-1-3-1-2 1-4 3-1z"/><circle cx="12" cy="11" r="4"/>`,
	"folder":      `<path d="M3 6h7l2 2h9v11H3z"/>`,
	"folderopen":  `<path d="M3 6h7l2 2h9l-2 11H3zM3 6V4h7l2 2h7v2"/>`,
	"addfolder":   `<path d="M3 6h7l2 2h9v5M3 6V4h7l2 2h7v2M3 6v13h8m7-5v8m-4-4h8"/>`,
	"history":     `<path d="M3 5v5h5M3 10a9 9 0 1 1 1 8M12 6v6l4 2"/>`,
	"plus":        `<path d="M12 4v16M4 12h16"/>`,
	"menu":        `<path d="M4 6h16M4 12h16M4 18h16"/>`,
	"expand":      `<path d="m5 5 7 7-7 7m7-14 7 7-7 7"/>`,
	"collapse":    `<path d="m19 5-7 7 7 7M12 5 5 12l7 7"/>`,
	"close":       `<path d="m6 6 12 12M6 18 18 6"/>`,
	"min":         `<path d="M5 12h14"/>`,
	"max":         `<rect x="5" y="5" width="14" height="14" rx="1"/>`,
	"restore":     `<rect x="4" y="7" width="12" height="12" rx="1"/><path d="M8 7V4h12v12h-4"/>`,
	"send":        `<path d="m4 13 8-8 8 8M12 5v16"/>`,
	"attachment":  `<path d="M8 12l6-6a4 4 0 0 1 6 6l-8 8a6 6 0 0 1-8-8l8-8M7 15l8-8"/>`,
	"terminal":    `<rect x="3" y="4" width="18" height="16" rx="2"/><path d="m7 9 3 3-3 3m6 0h4"/>`,
	"trash":       `<path d="M4 7h16M9 7V4h6v3m-8 0 1 14h8l1-14M10 11v6m4-6v6"/>`,
	"edit":        `<path d="M12 20h9M16.5 3.5a2.1 2.1 0 0 1 3 3L7 19l-4 1 1-4z"/>`,
	"refresh":     `<path d="M21 12a9 9 0 1 1-3-6.7M21 4v5h-5"/>`,
	"download":    `<path d="M12 3v12m-5-5 5 5 5-5M4 20h16"/>`,
	"upload":      `<path d="M12 21V9m-5 5 5-5 5 5M4 4h16"/>`,
	"copy":        `<rect x="9" y="9" width="12" height="12" rx="2"/><path d="M5 15V4a1 1 0 0 1 1-1h10"/>`,
	"check":       `<path d="m4 12 5 5L20 6"/>`,
	"alert":       `<path d="M12 3 2 21h20zM12 9v5m0 3v.5"/>`,
	"info":        `<circle cx="12" cy="12" r="9"/><path d="M12 11v6M12 7.5v.5"/>`,
	"play":        `<path d="m6 4 14 8-14 8z"/>`,
	"stop":        `<rect x="6" y="6" width="12" height="12" rx="1"/>`,
	"file":        `<path d="M6 2h8l4 4v16H6zM14 2v5h5"/>`,
	"code":        `<path d="m8 7-5 5 5 5M16 7l5 5-5 5"/>`,
	"image":       `<rect x="3" y="3" width="18" height="18" rx="2"/><circle cx="9" cy="9" r="2"/><path d="m21 16-5-5-6 6"/>`,
	"link":        `<path d="M10 13a5 5 0 0 0 7 0l3-3a5 5 0 0 0-7-7l-1 1M14 11a5 5 0 0 0-7 0l-3 3a5 5 0 0 0 7 7l1-1"/>`,
	"external":    `<path d="M14 3h7v7M21 3l-9 9M18 14v6H4V6h6"/>`,
	"search":      `<circle cx="10.5" cy="10.5" r="6.5"/><path d="m16 16 5 5"/>`,
	"filter":      `<path d="M3 5h18l-7 8v6l-4 2v-8z"/>`,
	"chevron":     `<path d="m9 6 6 6-6 6"/>`,
	"chevrondown": `<path d="m6 9 6 6 6-6"/>`,
	"arrowup":     `<path d="M12 19V5m-7 7 7-7 7 7"/>`,
	"arrowdown":   `<path d="M12 5v14m7-7-7 7-7-7"/>`,
	"dot":         `<circle cx="12" cy="12" r="4"/>`,
	"book":        `<path d="M4 4h6a3 3 0 0 1 3 3v13a3 3 0 0 0-3-3H4zM20 4h-6a3 3 0 0 0-3 3v13a3 3 0 0 1 3-3h6z"/>`,
	"shield":      `<path d="M12 3 4 6v6c0 5 4 8 8 9 4-1 8-4 8-9V6z"/><path d="m9 12 2 2 4-4"/>`,
	"globe":       `<circle cx="12" cy="12" r="9"/><path d="M3 12h18M12 3c3 3 3 15 0 18-3-3-3-15 0-18z"/>`,
	"server":      `<rect x="3" y="3" width="18" height="7" rx="2"/><rect x="3" y="14" width="18" height="7" rx="2"/><path d="M7 6.5h.5M7 17.5h.5"/>`,
	"database":    `<ellipse cx="12" cy="6" rx="8" ry="3"/><path d="M4 6v12c0 2 4 3 8 3s8-1 8-3V6M4 12c0 2 4 3 8 3s8-1 8-3"/>`,
	"clock":       `<circle cx="12" cy="12" r="9"/><path d="M12 7v5l3 2"/>`,
	"star":        `<path d="m12 3 2.8 6 6.2.9-4.5 4.4 1 6.2-5.5-3-5.5 3 1-6.2L3 9.9 9.2 9z"/>`,
	"key":         `<circle cx="8" cy="15" r="4"/><path d="m11 12 9-9m-3 3 3 3m-6 0 2 2"/>`,
	"package":     `<path d="m12 2 9 5v10l-9 5-9-5V7zM3 7l9 5 9-5M12 12v10"/>`,
	"brain":       `<path d="M9 3a4 4 0 0 0-4 4 4 4 0 0 0-1 7 4 4 0 0 0 5 6V3zM15 3a4 4 0 0 1 4 4 4 4 0 0 1 1 7 4 4 0 0 1-5 6V3z"/>`,
	"wand":        `<path d="m4 20 10-10M13 5l1.5 3.5L18 10l-3.5 1.5L13 15l-1.5-3.5L8 10l3.5-1.5z"/>`,
	"pause":       `<rect x="7" y="5" width="3" height="14"/><rect x="14" y="5" width="3" height="14"/>`,
	"lock":        `<rect x="4" y="10" width="16" height="11" rx="2"/><path d="M8 10V7a4 4 0 0 1 8 0v3"/>`,
	"grid":        `<rect x="3" y="3" width="7" height="7"/><rect x="14" y="3" width="7" height="7"/><rect x="3" y="14" width="7" height="7"/><rect x="14" y="14" width="7" height="7"/>`,
	"list":        `<path d="M8 6h13M8 12h13M8 18h13M3 6h.5M3 12h.5M3 18h.5"/>`,
	"sword":       `<path d="M14 3h7v7L9 22l-4-4zM6 18l-3 3M15 8l1 1"/>`,
	"robot":       `<rect x="4" y="7" width="16" height="12" rx="3"/><path d="M12 3v4M9 13h.5M14.5 13h.5M9 17h6"/>`,
}

var icons = func() map[string]*ui.SVG {
	m := map[string]*ui.SVG{}
	for k, p := range iconPaths {
		m[k] = ui.MustParseSVG([]byte(`<svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round">` + p + `</svg>`))
	}
	return m
}()

// ---- Formatting ----------------------------------------------------------

func fmtK(n int) string {
	switch {
	case n >= 1_000_000_000:
		return fmt.Sprintf("%.1fB", float64(n)/1_000_000_000)
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.1fk", float64(n)/1_000)
	}
	return strconv.Itoa(n)
}

func trimZero(v float64) string {
	if v >= 100 {
		return fmt.Sprintf("%.0f", v)
	}
	return strings.TrimSuffix(fmt.Sprintf("%.1f", v), ".0")
}

func fmtBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for value := n / unit; value >= unit && exp < 4; value /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTP"[exp])
}

func fmtTime(ms int64) string {
	if ms <= 0 {
		return "-"
	}
	return time.UnixMilli(ms).Local().Format("01-02 15:04")
}

func fmtClock(ms int64) string {
	if ms <= 0 {
		return ""
	}
	return time.UnixMilli(ms).Local().Format("15:04")
}

func fmtDur(ms int64) string {
	switch {
	case ms <= 0:
		return ""
	case ms < 1000:
		return fmt.Sprintf("%dms", ms)
	case ms < 60_000:
		return trimZero(float64(ms)/1000) + "s"
	default:
		return fmt.Sprintf("%dm%02ds", ms/60000, (ms%60000)/1000)
	}
}

// pctOf renders a whole-number percentage: the footer is a glanceable badge,
// so 12.4% only ever reads as 12%. math.Round keeps halves round-half-up instead
// of the format verb's round-half-to-even.
func pctOf(v, total int) string {
	if total <= 0 {
		return "0%"
	}
	return fmt.Sprintf("%d%%", int(math.Round(float64(v)*100/float64(total))))
}

func short(s string, n int) string {
	r := []rune(strings.ReplaceAll(strings.ReplaceAll(s, "\n", " "), "\r", ""))
	if len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

func firstLine(s string) string {
	for _, l := range strings.Split(s, "\n") {
		if t := strings.TrimSpace(l); t != "" {
			return t
		}
	}
	return ""
}

func yesNo(v bool) string {
	if v {
		return "是"
	}
	return "否"
}

func boolValue(p *bool) bool { return p != nil && *p }

func boolPtr(v bool) *bool { return &v }

// ---- Untyped event/message map helpers ----------------------------------

func str(p map[string]any, k string) string {
	s, _ := p[k].(string)
	return s
}

func num(p map[string]any, k string) float64 {
	switch v := p[k].(type) {
	case float64:
		return v
	case float32:
		return float64(v)
	case int:
		return float64(v)
	case int64:
		return float64(v)
	}
	return 0
}

func obj(p map[string]any, k string) map[string]any {
	m, _ := p[k].(map[string]any)
	return m
}

func list(p map[string]any, k string) []any {
	l, _ := p[k].([]any)
	return l
}

func boolOf(p map[string]any, k string) bool {
	v, _ := p[k].(bool)
	return v
}

func nativeFontFamily() string {
	return "Inter"
}

func nativeMonoFontFamily() string { return "Inter" }
