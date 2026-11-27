package uikit

import "github.com/egoist/mygo/ui"

// ThemeID identifies a built-in palette independently of display language.
type ThemeID string

const (
	ThemeNative ThemeID = "native"
	ThemeBlue   ThemeID = "blue"
	ThemeAmber  ThemeID = "amber"
	ThemeForest ThemeID = "forest"
	ThemeViolet ThemeID = "violet"
	ThemeTeal   ThemeID = "teal"
	ThemeRose   ThemeID = "rose"
	ThemeIndigo ThemeID = "indigo"
	ThemeKhaki  ThemeID = "khaki"
	ThemeCodex  ThemeID = "codex"
)

// ThemeChoice describes a built-in palette. Name is the Chinese source label;
// applications may localize it in their own catalog.
type ThemeChoice struct {
	ID   ThemeID
	Name string
}

// ThemeColors are the shared colors of a built-in appearance. Secondary is
// for supporting text; Placeholder is reserved for input placeholders.
type ThemeColors struct {
	Background, Surface, Hover, Border, Text, Secondary, Placeholder, Accent, AccentText ui.Color
}

type builtinTheme struct {
	ThemeChoice
	lightAccent, darkAccent ui.Color
}

var builtinThemes = [...]builtinTheme{
	{ThemeChoice{ThemeNative, "原生"}, ui.Hex("#536d87"), ui.Hex("#a8bdd2")},
	{ThemeChoice{ThemeBlue, "蓝色"}, ui.Hex("#365dcc"), ui.Hex("#8da5df")},
	{ThemeChoice{ThemeAmber, "琥珀"}, ui.Hex("#9b6218"), ui.Hex("#e0a458")},
	{ThemeChoice{ThemeForest, "森林"}, ui.Hex("#247649"), ui.Hex("#6fbf8f")},
	{ThemeChoice{ThemeViolet, "紫罗兰"}, ui.Hex("#7850b8"), ui.Hex("#a68bea")},
	{ThemeChoice{ThemeTeal, "青瓷"}, ui.Hex("#176e69"), ui.Hex("#72bbb5")},
	{ThemeChoice{ThemeRose, "玫瑰"}, ui.Hex("#a24753"), ui.Hex("#d69a9f")},
	{ThemeChoice{ThemeIndigo, "靛蓝"}, ui.Hex("#5557a0"), ui.Hex("#a8a9e0")},
	{ThemeChoice{ThemeKhaki, "暖灰卡其"}, ui.Hex("#80643e"), ui.Hex("#c2a276")},
	{ThemeChoice{ThemeCodex, "柔彩"}, ui.Hex("#365dcc"), ui.Hex("#8da5df")},
}

var builtinLightColors = ThemeColors{ui.Hex("#f5f6f8"), ui.Hex("#ffffff"), ui.Hex("#eef0f5"), ui.Hex("#d9dce3"), ui.Hex("#24262d"), ui.Hex("#535b68"), ui.Hex("#8b929e"), ui.Color{}, ui.Hex("#ffffff")}
var builtinDarkColors = ThemeColors{ui.Hex("#1b1b1b"), ui.Hex("#212122"), ui.Hex("#2b2b2e"), ui.Hex("#303031"), ui.Hex("#cccccc"), ui.Hex("#909093"), ui.Hex("#5d5d62"), ui.Color{}, ui.Hex("#15171d")}
var khakiLightColors = ThemeColors{ui.Hex("#f5f2ec"), ui.Hex("#fffdfa"), ui.Hex("#eee9e0"), ui.Hex("#ded7ca"), ui.Hex("#302d28"), ui.Hex("#6e675d"), ui.Hex("#867e73"), ui.Color{}, ui.Hex("#ffffff")}
var khakiDarkColors = ThemeColors{ui.Hex("#1c1a17"), ui.Hex("#26231f"), ui.Hex("#302c26"), ui.Hex("#474137"), ui.Hex("#e8e2d8"), ui.Hex("#ada497"), ui.Hex("#8e867b"), ui.Color{}, ui.Hex("#211d18")}

var codexDarkColors = ThemeColors{ui.Hex("#171717"), ui.Hex("#1d1d1d"), ui.Hex("#292929"), ui.Hex("#414141"), ui.Hex("#cccccc"), ui.Hex("#999999"), ui.Hex("#686868"), ui.Color{}, ui.Hex("#171717")}

// BuiltinThemes returns the available themes in display order as a new slice.
func BuiltinThemes() []ThemeChoice {
	choices := make([]ThemeChoice, len(builtinThemes))
	for i, t := range builtinThemes {
		choices[i] = t.ThemeChoice
	}
	return choices
}

// BuiltinThemeColors returns colors for a theme, falling back to blue for an
// unknown ID. The returned value is independent of subsequent calls.
func BuiltinThemeColors(id ThemeID, dark bool) ThemeColors {
	var colors ThemeColors
	if dark {
		colors = builtinDarkColors
	} else {
		colors = builtinLightColors
	}
	chosen := builtinThemes[1]
	for _, t := range builtinThemes {
		if t.ID == id {
			chosen = t
			break
		}
	}
	if chosen.ID == ThemeKhaki {
		if dark {
			colors = khakiDarkColors
		} else {
			colors = khakiLightColors
		}
	}
	if chosen.ID == ThemeCodex && dark {
		colors = codexDarkColors
	}
	if dark {
		colors.Accent = chosen.darkAccent
	} else {
		colors.Accent = chosen.lightAccent
	}
	return colors
}

// BuiltinTheme creates a fresh MyGo theme for a built-in palette. Callers may
// customize the returned theme without changing other windows or future calls.
func BuiltinTheme(id ThemeID, dark bool) *ui.Theme {
	var t *ui.Theme
	if dark {
		t = ui.DarkTheme()
	} else {
		t = ui.LightTheme()
	}
	c := BuiltinThemeColors(id, dark)
	t = t.WithAccent(c.Accent)
	t.Background, t.Surface, t.SurfaceHover, t.SurfacePressed = c.Background, c.Surface, c.Hover, c.Hover
	t.Border, t.Text, t.TextMuted, t.AccentText = c.Border, c.Text, c.Placeholder, c.AccentText
	return t
}
