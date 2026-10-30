package desktop

import (
	backend "ally-dev/internal/app"
	"slices"
	"testing"
)

func TestChatConfigCarriesSessionExtraRoots(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	model := backend.ModelConfig{Model: "test-model"}
	a := &desktop{core: backend.NewApp(), config: backend.ConfigState{Models: []backend.ModelConfig{model}}}
	conv := &conversation{ID: "session", Workspace: t.TempDir(), Model: modelOption(model)}
	extra := t.TempDir()
	for _, roots := range [][]string{{extra}, {}, {extra}, {}} {
		if err := a.setExtraRoots(conv, roots); err != nil {
			t.Fatal(err)
		}
		cfg, ok := a.chatConfig(conv)
		if !ok || cfg.ExtraRoots == nil || !slices.Equal(cfg.ExtraRoots, roots) {
			t.Fatalf("request roots = %#v, want %#v (model found %v)", cfg.ExtraRoots, roots, ok)
		}
		if len(cfg.ExtraRoots) > 0 {
			cfg.ExtraRoots[0] = "mutated"
			if a.extraRoots[conv.ID][0] != extra {
				t.Fatal("request roots alias UI state")
			}
		}
	}
	other := &conversation{ID: "other", Workspace: t.TempDir(), Model: conv.Model}
	if err := a.setExtraRoots(conv, []string{extra}); err != nil {
		t.Fatal(err)
	}
	if cfg, _ := a.chatConfig(other); len(cfg.ExtraRoots) != 0 {
		t.Fatal("another session inherited the extra directory")
	}
}

func TestExtraRootsUICommitsOnlyAfterPublish(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	a := &desktop{core: backend.NewApp()}
	if err := a.setExtraRoots(&conversation{}, []string{t.TempDir()}); err == nil {
		t.Fatal("empty session ID was accepted")
	}
	if len(a.extraRoots) != 0 {
		t.Fatal("UI displayed roots that were not published")
	}
}
