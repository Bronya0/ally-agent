//go:build windows

// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/go-ole/go-ole"
	"github.com/moutend/go-wca/pkg/wca"
)

func TestAudioBufferEmptyIsNotFatal(t *testing.T) {
	if !isAudioBufferEmpty(ole.NewError(audclntSBufferEmpty)) {
		t.Fatal("WASAPI buffer-empty status should be treated as a normal poll result")
	}
	if isAudioBufferEmpty(ole.NewError(0x88890004)) {
		t.Fatal("device-invalidated error must not be treated as an empty buffer")
	}
}

func TestStopPrefersCompletedResultWhenSessionCloses(t *testing.T) {
	want := []byte{1, 2, 3}
	r := &windowsAudioRecording{done: make(chan struct{}), result: make(chan captureResult, 1)}
	r.result <- captureResult{wav: want, reusable: false}
	close(r.done)
	got, err := r.Stop()
	if err != nil || len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("Stop returned (%v, %v), want completed WAV result", got, err)
	}
}

func TestVoiceCaptureDeviceChanged(t *testing.T) {
	if voiceCaptureDeviceChanged("mic-a", "mic-a") {
		t.Fatal("same endpoint should keep the initialized session")
	}
	if !voiceCaptureDeviceChanged("mic-a", "mic-b") {
		t.Fatal("changed endpoint should force reinitialization")
	}
}

func TestVoiceVirtualKeyMapping(t *testing.T) {
	for _, test := range []struct {
		key ui.Key
		vk  uint32
	}{{ui.KeyA, 0x41}, {ui.KeyF9, 0x78}, {ui.KeyTab, vkTab}, {ui.KeySlash, 0xBF}, {ui.KeyBackquote, 0xC0}, {ui.KeyEscape, vkEscape}} {
		if got := voiceVirtualKey(test.key); got != test.vk {
			t.Errorf("voiceVirtualKey(%v) = %#x, want %#x", test.key, got, test.vk)
		}
		if _, ok := voiceKeyByVK[test.vk]; !ok {
			t.Errorf("virtual key %#x is not in the reverse table", test.vk)
		}
	}
}

func TestVoiceHotkeyRequirementMatchesHeldKeys(t *testing.T) {
	hotkey, ok := parseVoiceHotkey("Ctrl+K+J")
	if !ok {
		t.Fatal("Ctrl+K+J should parse")
	}
	require, err := newVoiceHotkeyRequirement(hotkey, true, true)
	if err != nil {
		t.Fatal(err)
	}
	down := map[uint32]bool{0xA2: true, 0x4B: true}
	if require.satisfiedBy(down) {
		t.Fatal("an incomplete chord must not record")
	}
	down[0x4A] = true
	if !require.satisfiedBy(down) {
		t.Fatal("the full chord should record")
	}
}

func TestVoiceHotkeyRequirementHonoursModifierSideMode(t *testing.T) {
	hotkey, ok := parseVoiceHotkey("Ctrl+S")
	if !ok {
		t.Fatal("Ctrl+S should parse")
	}
	left, err := newVoiceHotkeyRequirement(hotkey, true, false)
	if err != nil {
		t.Fatal(err)
	}
	if left.satisfiedBy(map[uint32]bool{0xA3: true, 0x53: true}) {
		t.Fatal("right Ctrl must not satisfy a left-only preference")
	}
	if !left.satisfiedBy(map[uint32]bool{0xA2: true, 0x53: true}) {
		t.Fatal("left Ctrl should satisfy a left-only preference")
	}
	right, err := newVoiceHotkeyRequirement(hotkey, false, true)
	if err != nil {
		t.Fatal(err)
	}
	if right.satisfiedBy(map[uint32]bool{0xA2: true, 0x53: true}) {
		t.Fatal("left Ctrl must not satisfy a right-only preference")
	}
	if !right.satisfiedBy(map[uint32]bool{0xA3: true, 0x53: true}) {
		t.Fatal("right Ctrl should satisfy a right-only preference")
	}
	both, err := newVoiceHotkeyRequirement(hotkey, true, true)
	if err != nil {
		t.Fatal(err)
	}
	if !both.satisfiedBy(map[uint32]bool{0x11: true, 0x53: true}) {
		t.Fatal("a generic modifier code must satisfy either side")
	}
}

func TestVoiceHotkeyFromVirtualKeysRendersChord(t *testing.T) {
	value, parts, ok := voiceHotkeyFromVirtualKeys([]uint32{0xA2, 0x4B, 0x4A})
	if !ok || value != "Ctrl+K+J" || strings.Join(parts, " + ") != "Ctrl + K + J" {
		t.Fatalf("chord rendered as (%q,%v,%v)", value, parts, ok)
	}
	value, parts, ok = voiceHotkeyFromVirtualKeys([]uint32{0x47, 0x48})
	if ok || value != "" || strings.Join(parts, " + ") != "G + H" {
		t.Fatalf("plain-key chord rendered as (%q,%v,%v)", value, parts, ok)
	}
}

func TestVoiceHotkeyHookSwallowsChordKeys(t *testing.T) {
	hotkey, ok := parseVoiceHotkey("Ctrl+K")
	if !ok {
		t.Fatal("Ctrl+K should parse")
	}
	require, err := newVoiceHotkeyRequirement(hotkey, true, true)
	if err != nil {
		t.Fatal(err)
	}
	hook := &voiceHotkeyHook{require: require, owned: map[uint32]bool{0x4B: true}, down: map[uint32]bool{}, onDown: func() {}, onUp: func() {}}
	if hook.handleEvent(wmKeyDown, 0xA2, true) {
		t.Fatal("the modifier key itself must keep reaching the app")
	}
	if !hook.handleEvent(wmKeyDown, 0x4B, true) {
		t.Fatal("the primary key that completes the chord must be swallowed")
	}
	if !hook.handleEvent(wmKeyUp, 0x4B, true) {
		t.Fatal("the release of a recording chord must be swallowed too")
	}
	if hook.active {
		t.Fatal("releasing a chord key must end the recording")
	}
}

func TestVoiceHotkeyHookIgnoresOtherWindows(t *testing.T) {
	hotkey, _ := parseVoiceHotkey("Alt")
	require, err := newVoiceHotkeyRequirement(hotkey, true, true)
	if err != nil {
		t.Fatal(err)
	}
	hook := &voiceHotkeyHook{require: require, owned: map[uint32]bool{}, down: map[uint32]bool{}, modifierOnly: true, onDown: func() {}, onUp: func() {}}
	if hook.handleEvent(wmSysKeyDown, 0xA5, false) {
		t.Fatal("a shortcut outside Ally must not record")
	}
	if hook.active {
		t.Fatal("recording must not start while another window is in front")
	}
}

func TestCaptureFormatOfRecognizesFloatMixFormat(t *testing.T) {
	got, err := captureFormatOf(&wca.WAVEFORMATEX{WFormatTag: 3, NChannels: 2, NSamplesPerSec: 48000, NBlockAlign: 8, WBitsPerSample: 32})
	if err != nil {
		t.Fatal(err)
	}
	if !got.Float || got.BytesPerSample != 4 || got.BlockAlign != 8 || got.isTarget() {
		t.Fatalf("unexpected float mix format: %+v", got)
	}
	if _, err := captureFormatOf(&wca.WAVEFORMATEX{WFormatTag: 2, NChannels: 1, NSamplesPerSec: 16000, NBlockAlign: 2, WBitsPerSample: 16}); err == nil {
		t.Fatal("compressed mix format should be rejected with a clear error")
	}
}
