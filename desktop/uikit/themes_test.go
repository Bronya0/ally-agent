package uikit

import "testing"

func TestBuiltinThemesAreIndependent(t *testing.T) {
	choices := BuiltinThemes()
	if len(choices) == 0 {
		t.Fatal("no built-in themes")
	}
	first := choices[0]
	choices[0].Name = "changed"
	if BuiltinThemes()[0] != first {
		t.Fatal("modifying the returned list changed the catalog")
	}
	light := BuiltinTheme(ThemeKhaki, false)
	dark := BuiltinTheme(ThemeKhaki, true)
	if light.Dark || !dark.Dark || light.Background == dark.Background || light.Accent == dark.Accent {
		t.Fatal("khaki did not follow the requested appearance")
	}
	before := BuiltinTheme(ThemeBlue, false).Accent
	light.Accent = before
	if BuiltinTheme(ThemeKhaki, false).Accent == before {
		t.Fatal("changing a theme affected another call")
	}
	if BuiltinTheme(ThemeID("unknown"), false).Accent != before {
		t.Fatal("unknown theme did not fall back to blue")
	}
}
