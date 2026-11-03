//go:build windows

// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// A chord whose primary keys are pressed but whose modifier does not match must
// report it instead of failing silently: that is exactly how a wrong-side Alt
// press looks, and the user cannot tell it apart from a dead shortcut.
// mismatches counts mismatch reports across the throttled hook.
var mismatches atomic.Int32

func TestVoiceHotkeyReportsModifierMismatch(t *testing.T) {
	hook := &voiceHotkeyHook{
		require: voiceHotkeyRequirement{
			keyVKs: []uint32{0x4C},           // L
			modVKs: [][]uint32{{0x12, 0xA5}}, // generic Alt or right Alt
		},
		owned:      map[uint32]bool{0x4C: true},
		down:       map[uint32]bool{},
		onMismatch: func() { mismatches.Add(1) },
	}
	// Left Alt is not accepted by a right-only preference.
	hook.handleEvent(wmSysKeyDown, 0xA4, true)
	hook.handleEvent(wmKeyDown, 0x4C, true)
	waitForCount(t, &mismatches, 1)
	// Repeats within the throttle window must not spam.
	hook.handleEvent(wmKeyUp, 0x4C, true)
	hook.handleEvent(wmKeyDown, 0x4C, true)
	time.Sleep(120 * time.Millisecond)
	if got := mismatches.Load(); got != 1 {
		t.Fatalf("mismatch reported %d times, want it throttled to 1", got)
	}
}

// The notice must name the side that counts, otherwise it explains nothing.
func TestVoiceModifierMismatchNoticeNamesTheSide(t *testing.T) {
	for _, test := range []struct {
		left, right bool
		want        string
	}{
		{true, true, "修饰键"},
		{true, false, "只认左侧"},
		{false, true, "只认右侧"},
	} {
		if got := voiceModifierMismatchNotice(test.left, test.right); !strings.Contains(got, test.want) {
			t.Errorf("notice(%v,%v) = %q, want it to mention %q", test.left, test.right, got, test.want)
		}
	}
}

// The note must name the side that actually counts, so a right-only preference
// cannot look like a broken chord.
func TestVoiceModifierSidesNoteNamesTheSideThatCounts(t *testing.T) {
	for _, test := range []struct {
		left, right bool
		want        string
	}{
		{true, true, "左、右侧"},
		{true, false, "只认左侧"},
		{false, true, "只认右侧"},
	} {
		if got := voiceModifierSidesNote(test.left, test.right); !strings.Contains(got, test.want) {
			t.Errorf("voiceModifierSidesNote(%v,%v) = %q, want it to mention %q", test.left, test.right, got, test.want)
		}
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
