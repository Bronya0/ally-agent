//go:build windows

// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"sync/atomic"
	"testing"
	"time"
)

// The keyboard slot has exactly one owner. A hook that finishes stopping after
// its replacement was installed must not clear the slot: clearing it left the
// keyboard unwatched, so a rebound chord could never fire again.
func TestVoiceHookSlotKeepsTheNewestOwner(t *testing.T) {
	first := &voiceHotkeyHook{down: map[uint32]bool{}}
	second := &voiceHotkeyHook{down: map[uint32]bool{}}

	claimVoiceHook(first)
	claimVoiceHook(second)
	releaseVoiceHook(first)
	if got := activeVoiceHook(); got != second {
		t.Fatalf("the older hook cleared the slot its replacement owns: %#v", got)
	}
	releaseVoiceHook(second)
	if got := activeVoiceHook(); got != nil {
		t.Fatalf("the owner must release the slot, got %#v", got)
	}
}

// The real rebind sequence: the settings field takes the hook over, then hands it
// back while the recorder's hook is installed. Both must end with a live hook.
func TestVoiceHookSlotSurvivesRebind(t *testing.T) {
	stopRecorder, err := startNativeVoiceHotkey("Alt", true, true, func() {}, func() {})
	if err != nil {
		t.Skipf("low-level keyboard hooks are unavailable here: %v", err)
	}
	stopRecorder()
	if activeVoiceHook() != nil {
		t.Fatal("the slot must be free once the hook stopped")
	}

	stopCapture, err := startNativeHotkeyCapture(func([]string, string) {}, func(string) {})
	if err != nil {
		t.Fatalf("capture hook: %v", err)
	}
	// Releasing the capture hook asynchronously is how the editor handed it back.
	go stopCapture()
	stopRebound, err := startNativeVoiceHotkey("Ctrl+Alt+K", true, true, func() {}, func() {})
	if err != nil {
		t.Fatalf("rebound hook: %v", err)
	}
	// Give the stopped capture hook time to finish if it is going to.
	time.Sleep(300 * time.Millisecond)
	if activeVoiceHook() == nil {
		t.Fatal("the stopped capture hook cleared the slot its replacement owns")
	}
	stopRebound()
	if activeVoiceHook() != nil {
		t.Fatal("stop must release the slot")
	}
}

// A rebound chord must actually match its keys, whether it is a bare modifier, a
// single key or a modifier plus a letter; otherwise "更换" appears to save but
// nothing ever triggers.
func TestVoiceHotkeyMatchesReboundChords(t *testing.T) {
	for _, test := range []struct {
		name  string
		value string
		left  bool
		right bool
		keys  []struct {
			event uint32
			vk    uint32
		}
		wantDown int32
	}{
		{
			name: "bare modifier", value: "Alt", left: true, right: true,
			keys: []struct {
				event uint32
				vk    uint32
			}{{wmSysKeyDown, 0xA4}, {wmSysKeyUp, 0xA4}},
			wantDown: 1,
		},
		{
			name: "single function key", value: "F9", left: true, right: true,
			keys: []struct {
				event uint32
				vk    uint32
			}{{wmKeyDown, 0x78}, {wmKeyUp, 0x78}},
			wantDown: 1,
		},
		{
			name: "modifiers plus letter", value: "Ctrl+Alt+K", left: true, right: true,
			keys: []struct {
				event uint32
				vk    uint32
			}{{wmKeyDown, 0xA2}, {wmSysKeyDown, 0xA4}, {wmKeyDown, 0x4B}, {wmKeyUp, 0x4B}, {wmSysKeyUp, 0xA4}, {wmKeyUp, 0xA2}},
			wantDown: 1,
		},
		{
			name: "side preference excludes the other side", value: "Ctrl+K", left: true, right: false,
			keys: []struct {
				event uint32
				vk    uint32
			}{{wmKeyDown, 0xA3}, {wmKeyDown, 0x4B}},
			wantDown: 0,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			hook, spy := newTestVoiceHook(t, test.value, test.left, test.right)
			for _, key := range test.keys {
				hook.handleEvent(key.event, key.vk, true)
			}
			// onDown/onUp run on their own goroutine so the hook returns to
			// Windows at once: give them the same short grace the app relies on.
			if test.wantDown > 0 {
				waitForCount(t, &spy.downs, test.wantDown)
				return
			}
			time.Sleep(100 * time.Millisecond)
			if got := spy.downs.Load(); got != 0 {
				t.Fatalf("%q activated %d times, want none", test.value, got)
			}
		})
	}
}

// A chord must not record while another window owns the foreground, and the
// primary key must be swallowed while recording so it does not also type.
func TestVoiceHotkeyOnlyFiresInAllyAndSwallowsItsKeys(t *testing.T) {
	hook, spy := newTestVoiceHook(t, "Ctrl+Alt+K", true, true)
	hook.handleEvent(wmKeyDown, 0xA2, false)
	hook.handleEvent(wmKeyDown, 0xA4, false)
	if swallow := hook.handleEvent(wmKeyDown, 0x4B, false); swallow {
		t.Fatal("a chord that does not fire must not swallow its key")
	}
	if spy.downs.Load() != 0 {
		t.Fatal("the chord fired while another window had the foreground")
	}

	hook.handleEvent(wmKeyDown, 0xA2, true)
	hook.handleEvent(wmKeyDown, 0xA4, true)
	if swallow := hook.handleEvent(wmKeyDown, 0x4B, true); !swallow {
		t.Fatal("the primary key must be swallowed while recording")
	}
	hook.handleEvent(wmKeyUp, 0x4B, true)
	waitForCount(t, &spy.downs, 1)
	waitForCount(t, &spy.ups, 1)
}

type voiceHookSpy struct {
	downs, ups atomic.Int32
}

func newTestVoiceHook(t *testing.T, value string, left, right bool) (*voiceHotkeyHook, *voiceHookSpy) {
	t.Helper()
	hotkey, ok := parseVoiceHotkey(value)
	if !ok {
		t.Fatalf("%q does not parse", value)
	}
	require, err := newVoiceHotkeyRequirement(hotkey, left, right)
	if err != nil {
		t.Fatalf("%q: %v", value, err)
	}
	owned := make(map[uint32]bool, len(require.keyVKs))
	for _, vk := range require.keyVKs {
		owned[vk] = true
	}
	spy := &voiceHookSpy{}
	return &voiceHotkeyHook{
		require:      require,
		owned:        owned,
		down:         map[uint32]bool{},
		modifierOnly: len(require.keyVKs) == 0,
		onDown:       func() { spy.downs.Add(1) },
		onUp:         func() { spy.ups.Add(1) },
	}, spy
}

func waitForCount(t *testing.T, counter *atomic.Int32, want int32) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for counter.Load() < want && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	if got := counter.Load(); got < want {
		t.Fatalf("callback ran %d times, want %d", got, want)
	}
}
