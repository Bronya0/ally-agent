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
