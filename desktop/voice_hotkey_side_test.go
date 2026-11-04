//go:build windows

// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"strings"
	"testing"
	"time"
)

// The note must name the side that actually counts, so a right-only preference
// cannot look like a broken chord.
func TestVoiceModifierSidesNoteNamesTheSideThatCounts(t *testing.T) {
	for _, test := range []struct {
		left, right bool
		want        string
	}{
		{true, true, "左 / 右"},
		{true, false, "只认左侧"},
		{false, true, "只认右侧"},
	} {
		if got := voiceModifierSidesNote(test.left, test.right); !strings.Contains(got, test.want) {
			t.Errorf("voiceModifierSidesNote(%v,%v) = %q, want it to mention %q", test.left, test.right, got, test.want)
		}
	}
}

// A chord that cannot record on this page must not swallow its keys: on a page
// without a composer the shortcut used to eat the letters and do nothing, which
// reads like a broken keyboard.
func TestVoiceHotkeyDoesNotSwallowWhereItCannotRecord(t *testing.T) {
	hook, spy := newTestVoiceHook(t, "Ctrl+Alt+K", true, true)
	hook.armed = func() bool { return false }
	hook.handleEvent(wmKeyDown, 0xA2, true)
	hook.handleEvent(wmKeyDown, 0xA4, true)
	if swallow := hook.handleEvent(wmKeyDown, 0x4B, true); swallow {
		t.Fatal("a chord that cannot record must pass its key through")
	}
	if spy.downs.Load() != 0 {
		t.Fatal("no recording may start where it cannot record")
	}
	// Once the page can record again the same chord must work and swallow.
	hook.armed = func() bool { return true }
	hook.handleEvent(wmKeyUp, 0x4B, true)
	hook.handleEvent(wmKeyUp, 0xA4, true)
	hook.handleEvent(wmKeyUp, 0xA2, true)
	hook.handleEvent(wmKeyDown, 0xA2, true)
	hook.handleEvent(wmKeyDown, 0xA4, true)
	if swallow := hook.handleEvent(wmKeyDown, 0x4B, true); !swallow {
		t.Fatal("the chord must swallow its key where recording is possible")
	}
	if spy.downs.Load() != 1 {
		t.Fatalf("recording started %d times, want 1", spy.downs.Load())
	}
}

// The modifier-side preference decides which physical Alt counts. A chord bound
// as Alt+L while only the right side is enabled must fire with the right Alt and
// stay silent for the left one, exactly as 修饰键生效方式 promises.
func TestVoiceModifierSidesDecideWhichPhysicalAltCounts(t *testing.T) {
	const (
		leftAlt  = 0xA4
		rightAlt = 0xA5
		keyL     = 0x4C
	)
	for _, test := range []struct {
		name   string
		left   bool
		right  bool
		alt    uint32
		wantOn bool
	}{
		{"right side only accepts right Alt", false, true, rightAlt, true},
		{"right side only rejects left Alt", false, true, leftAlt, false},
		{"left side only accepts left Alt", true, false, leftAlt, true},
		{"left side only rejects right Alt", true, false, rightAlt, false},
		{"both sides accept either Alt", true, true, leftAlt, true},
		{"both sides accept either Alt (right)", true, true, rightAlt, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			hook, spy := newTestVoiceHook(t, "Alt+L", test.left, test.right)
			hook.handleEvent(wmSysKeyDown, test.alt, true)
			hook.handleEvent(wmKeyDown, keyL, true)
			hook.handleEvent(wmKeyUp, keyL, true)
			hook.handleEvent(wmSysKeyUp, test.alt, true)
			if test.wantOn {
				waitForCount(t, &spy.downs, 1)
				return
			}
			time.Sleep(120 * time.Millisecond)
			if spy.downs.Load() != 0 {
				t.Fatalf("the chord fired with the %s Alt although only %v/%v is enabled",
					map[bool]string{true: "right", false: "left"}[test.alt == rightAlt], test.left, test.right)
			}
		})
	}
}
