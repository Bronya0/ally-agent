package desktop

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInitialAppearanceAndAvatar(t *testing.T) {
	path := filepath.Join(t.TempDir(), "native-ui.json")
	for _, prefs := range []uiPrefs{defaultPrefs(), readPrefs(path)} {
		if prefs.Appearance != "dark" || prefs.Palette != 2 || prefs.Avatar != "whale" {
			t.Fatalf("wrong initial preferences: %+v", prefs)
		}
	}
	if err := os.WriteFile(path, []byte(`{"appearance":"light","palette":5,"avatar":"bot"}`), 0600); err != nil {
		t.Fatal(err)
	}
	saved := readPrefs(path)
	if saved.Appearance != "light" || saved.Palette != 5 || saved.Avatar != "bot" {
		t.Fatalf("saved choice overwritten: %+v", saved)
	}
	if err := os.WriteFile(path, []byte(`{"avatar":""}`), 0600); err != nil {
		t.Fatal(err)
	}
	if readPrefs(path).Avatar != "whale" {
		t.Fatal("empty avatar must use whale")
	}
	if err := os.WriteFile(path, []byte(`{"palette":1,"avatar":"eye"}`), 0600); err != nil {
		t.Fatal(err)
	}
	legacy := &desktop{prefs: readPrefs(path)}
	if legacy.currentPalette().dark {
		t.Fatal("legacy light selection was overwritten")
	}
}
