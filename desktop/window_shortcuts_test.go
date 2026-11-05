// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"os"
	"strings"
	"testing"
)

// The window shortcut table is registered from view(), and nothing else calls it.
// A refactor once replaced that call while adding voice wiring, which silently
// removed Ctrl/Cmd+N, Ctrl+Tab, Alt+1..9 and the memory diagnostic from the
// running app — every unit test still passed, because they call globalShortcuts
// directly. mygo exposes no headless Context, so this guard reads the view
// function's source: it fails the moment the call disappears again.
func TestViewRegistersWindowShortcuts(t *testing.T) {
	source, err := os.ReadFile("window.go")
	if err != nil {
		t.Fatalf("read window.go: %v", err)
	}
	view := string(source)
	if !strings.Contains(view, "a.globalShortcuts(c)") {
		t.Fatal("view() no longer calls globalShortcuts: every window shortcut is dead")
	}
	if !strings.Contains(view, "a.syncVoiceRecordable()") {
		t.Fatal("view() no longer publishes the voice arm state to the hook")
	}
}

// globalShortcuts must actually bind the table the app documents: the guard above
// only proves the call exists, this one proves the bindings were not gutted.
func TestGlobalShortcutsBindingsArePresent(t *testing.T) {
	source, err := os.ReadFile("chat_polish.go")
	if err != nil {
		t.Fatalf("read chat_polish.go: %v", err)
	}
	body := string(source)
	for _, binding := range []string{
		"ui.KeyN", "ui.KeyT", "ui.KeyW", "ui.KeyTab", "ui.KeyF12", "ui.Alt", "ui.KeyLeft", "ui.KeyRight",
	} {
		if !strings.Contains(body, binding) {
			t.Errorf("globalShortcuts no longer binds %s", binding)
		}
	}
}
