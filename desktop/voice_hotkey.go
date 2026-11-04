// SPDX-License-Identifier: GPL-3.0-only
package desktop

// The hold-to-talk vocabulary: tokens, parsing, keycap rendering, the modifier-side
// preference and the chord capture used by the settings field. Nothing here touches
// audio or the transcription queue, so it can be read (and tested) on its own.

import (
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
)

// A hold-to-talk chord of two or more plain keys would leak its first key into
// whatever has focus before the chord completes, so plain-key chords are
// rejected and a modifier is required to reach that count.
const voiceHotkeyMaxKeys = 3

// voiceHotkey is the parsed form of prefs.VoiceHotkey: the modifiers that must
// be held and up to three primary keys held together. Which physical side of a
// modifier counts is a separate global preference (修饰键生效方式), so a chord
// never pins left or right.
type voiceHotkey struct {
	mods ui.Modifiers
	keys []ui.Key
}

// voiceModifierSpec is one bindable modifier: its canonical token, the bit it
// sets and the label the settings page shows.
var voiceModifierSpecs = []struct {
	token   string
	display string
	mod     ui.Modifiers
}{
	{"Ctrl", "Ctrl", ui.Ctrl},
	{"Alt", "Alt", ui.Alt},
	{"Shift", "Shift", ui.Shift},
	{"Super", "Win", ui.Super},
}

// voiceModifierAliases accepts the spellings users and older configs use. The
// left/right-pinned spellings still load and normalize to the side-less token.
var voiceModifierAliases = map[string]string{
	"control": "Ctrl", "win": "Super", "cmd": "Super", "meta": "Super",
	"leftctrl": "Ctrl", "rightctrl": "Ctrl",
	"leftalt": "Alt", "rightalt": "Alt",
	"leftshift": "Shift", "rightshift": "Shift",
	"leftsuper": "Super", "rightsuper": "Super", "leftwin": "Super", "rightwin": "Super",
}

func voiceModifierByToken(token string) (ui.Modifiers, bool) {
	token = strings.ToLower(token)
	if alias, ok := voiceModifierAliases[token]; ok {
		token = strings.ToLower(alias)
	}
	for _, spec := range voiceModifierSpecs {
		if strings.ToLower(spec.token) == token {
			return spec.mod, true
		}
	}
	return 0, false
}

// voiceModifierDisplay is a modifier token's label.
func voiceModifierDisplay(token string) string {
	for _, spec := range voiceModifierSpecs {
		if strings.EqualFold(spec.token, token) {
			return spec.display
		}
	}
	return token
}

// voiceHotkeyKeySpec is one bindable primary key. Canonical tokens keep letters
// and named keys readable in the config file; parsing lowercases them.
type voiceHotkeyKeySpec struct {
	token string
	key   ui.Key
}

// voiceHotkeyKeySpecs is the single key vocabulary. Parsing, labels and the
// platform hooks all read it, so a key can never be listed in one place and
// rejected in another.
var voiceHotkeyKeySpecs = func() []voiceHotkeyKeySpec {
	specs := []voiceHotkeyKeySpec{
		{"Space", ui.KeySpace}, {"Enter", ui.KeyEnter}, {"Escape", ui.KeyEscape},
		{"Tab", ui.KeyTab}, {"Backspace", ui.KeyBackspace}, {"Delete", ui.KeyDelete}, {"Insert", ui.KeyInsert},
		{"Home", ui.KeyHome}, {"End", ui.KeyEnd}, {"PageUp", ui.KeyPageUp}, {"PageDown", ui.KeyPageDown},
		{"Up", ui.KeyUp}, {"Down", ui.KeyDown}, {"Left", ui.KeyLeft}, {"Right", ui.KeyRight},
		{"Minus", ui.KeyMinus}, {"Equal", ui.KeyEqual}, {"Comma", ui.KeyComma}, {"Period", ui.KeyPeriod},
		{"Slash", ui.KeySlash}, {"Semicolon", ui.KeySemicolon}, {"Quote", ui.KeyQuote},
		{"BracketLeft", ui.KeyBracketLeft}, {"BracketRight", ui.KeyBracketRight},
		{"Backslash", ui.KeyBackslash}, {"Backquote", ui.KeyBackquote},
	}
	for i := 0; i < 26; i++ {
		specs = append(specs, voiceHotkeyKeySpec{string(rune('A' + i)), ui.KeyA + ui.Key(i)})
	}
	for i := 0; i < 10; i++ {
		specs = append(specs, voiceHotkeyKeySpec{fmt.Sprintf("%d", i), ui.Key0 + ui.Key(i)})
	}
	for i := 0; i < 12; i++ {
		specs = append(specs, voiceHotkeyKeySpec{fmt.Sprintf("F%d", i+1), ui.KeyF1 + ui.Key(i)})
	}
	return specs
}()

var voiceHotkeyPrimaryTokens = func() map[string]ui.Key {
	tokens := make(map[string]ui.Key, len(voiceHotkeyKeySpecs))
	for _, spec := range voiceHotkeyKeySpecs {
		tokens[strings.ToLower(spec.token)] = spec.key
	}
	return tokens
}()

var voiceHotkeyKeyTokens = func() map[ui.Key]string {
	tokens := make(map[ui.Key]string, len(voiceHotkeyKeySpecs))
	for _, spec := range voiceHotkeyKeySpecs {
		tokens[spec.key] = spec.token
	}
	return tokens
}()

// voiceHotkeyKeyToken is the config token of a primary key.
func voiceHotkeyKeyToken(key ui.Key) string {
	if token, ok := voiceHotkeyKeyTokens[key]; ok {
		return token
	}
	return ""
}

// voiceHotkeyKeyLabel is the token as the settings page shows it.
func voiceHotkeyKeyLabel(key ui.Key) string {
	switch key {
	case ui.KeyEscape:
		return "Esc"
	case ui.KeyUp:
		return "↑"
	case ui.KeyDown:
		return "↓"
	case ui.KeyLeft:
		return "←"
	case ui.KeyRight:
		return "→"
	case ui.KeyTab:
		return "Tab"
	}
	if token := voiceHotkeyKeyToken(key); token != "" {
		return token
	}
	return ""
}

// parseVoiceHotkey reads the config form. It rejects a chord of two or more
// primary keys without a modifier, duplicate keys and more than three keys.
func parseVoiceHotkey(value string) (voiceHotkey, bool) {
	var hotkey voiceHotkey
	seen := map[ui.Key]bool{}
	for _, raw := range strings.Split(strings.ToLower(strings.TrimSpace(value)), "+") {
		token := strings.TrimSpace(raw)
		if token == "" {
			return voiceHotkey{}, false
		}
		if mod, ok := voiceModifierByToken(token); ok {
			hotkey.mods |= mod
			continue
		}
		key, ok := voiceHotkeyPrimaryTokens[token]
		if !ok || seen[key] || len(hotkey.keys) >= voiceHotkeyMaxKeys {
			return voiceHotkey{}, false
		}
		seen[key] = true
		hotkey.keys = append(hotkey.keys, key)
	}
	if hotkey.mods == 0 && len(hotkey.keys) == 0 {
		return voiceHotkey{}, false
	}
	if hotkey.mods == 0 && len(hotkey.keys) > 1 {
		return voiceHotkey{}, false
	}
	return hotkey, true
}

// String renders the canonical config form: modifiers in Ctrl, Alt, Shift, Win
// order, then the primary keys in the order the user pressed them.
func (h voiceHotkey) String() string {
	parts := make([]string, 0, len(h.keys)+len(voiceModifierSpecs))
	for _, spec := range voiceModifierSpecs {
		if h.mods&spec.mod != 0 {
			parts = append(parts, spec.token)
		}
	}
	for _, key := range h.keys {
		token := voiceHotkeyKeyToken(key)
		if token == "" {
			return ""
		}
		parts = append(parts, token)
	}
	return strings.Join(parts, "+")
}

// parts is the keycap sequence of the chord: one label per modifier and per
// key, in the order the settings page draws them.
func (h voiceHotkey) parts() []string {
	parts := make([]string, 0, len(h.keys)+len(voiceModifierSpecs))
	for _, spec := range voiceModifierSpecs {
		if h.mods&spec.mod != 0 {
			parts = append(parts, spec.display)
		}
	}
	for _, key := range h.keys {
		parts = append(parts, voiceHotkeyKeyLabel(key))
	}
	return parts
}

// voiceHotkeyParts is the keycap sequence of a stored value. Unparseable values
// fall back to their raw tokens so the settings page can still show something.
func voiceHotkeyParts(value string) []string {
	if hotkey, ok := parseVoiceHotkey(value); ok {
		return hotkey.parts()
	}
	parts := make([]string, 0, 4)
	for _, token := range strings.Split(value, "+") {
		if token = strings.TrimSpace(token); token != "" {
			parts = append(parts, token)
		}
	}
	return parts
}

// hotkeyPartsFor caches the parse for the value currently bound: the settings page
// asks for the keycaps on every frame, and re-parsing a string there is exactly the
// per-frame derivation the project avoids.
func (a *desktop) hotkeyPartsFor(value string) []string {
	if a.hotkeyMemo.value == value && a.hotkeyMemo.parts != nil {
		return a.hotkeyMemo.parts
	}
	a.hotkeyMemo = hotkeyMemo{value: value, parts: voiceHotkeyParts(value)}
	return a.hotkeyMemo.parts
}

type hotkeyMemo struct {
	value string
	parts []string
}

// voiceHotkeyTokenParts renders raw modifier and key tokens as keycap labels,
// used while a captured chord is not valid yet.
func voiceHotkeyTokenParts(modTokens, keyTokens []string) []string {
	parts := make([]string, 0, len(modTokens)+len(keyTokens))
	for _, token := range modTokens {
		parts = append(parts, voiceModifierDisplay(token))
	}
	for _, token := range keyTokens {
		label := token
		if key, ok := voiceHotkeyPrimaryTokens[strings.ToLower(token)]; ok {
			label = voiceHotkeyKeyLabel(key)
		}
		parts = append(parts, label)
	}
	return parts
}

// buildVoiceHotkey joins captured modifier and primary tokens in canonical
// order, validating the result through the same parser the hook uses.
func buildVoiceHotkey(modTokens, keyTokens []string) (string, bool) {
	parts := make([]string, 0, len(modTokens)+len(keyTokens))
	parts = append(parts, modTokens...)
	parts = append(parts, keyTokens...)
	hotkey, ok := parseVoiceHotkey(strings.Join(parts, "+"))
	if !ok {
		return "", false
	}
	return hotkey.String(), true
}

// setVoiceModifierSide records which physical sides of the modifiers count. At
// least one side always stays enabled so the hotkey can never stop matching.
func (a *desktop) setVoiceModifierSide(left, right bool) {
	if !left && !right {
		return
	}
	if a.prefs.VoiceModifierLeft == left && a.prefs.VoiceModifierRight == right {
		return
	}
	a.prefs.VoiceModifierLeft, a.prefs.VoiceModifierRight = left, right
	a.savePrefs()
	a.restartVoiceHotkey()
}

// voiceModifierSidesNote names which physical sides count. The side preference
// silently decides whether a chord fires, so leaving it implicit made a bound
// Alt+L look broken whenever only one side was enabled. Keep it to a few words:
// the settings page is a form, not a manual.
func voiceModifierSidesNote(left, right bool) string {
	switch {
	case left && right:
		return "左 / 右都生效"
	case left:
		return "只认左侧"
	case right:
		return "只认右侧"
	}
	return "至少选一侧"
}

func (a *desktop) setVoiceHotkey(value string) {
	hotkey, ok := parseVoiceHotkey(value)
	if !ok {
		return
	}
	a.prefs.VoiceHotkey = hotkey.String()
	a.savePrefs()
	a.restartVoiceHotkey()
}

// beginVoiceHotkeyCapture listens for a chord while the settings field is
// active. The recording hook is stopped for the duration so one chord cannot
// both record and be captured, and the capture hook reports every change until
// all keys are released.
func (a *desktop) beginVoiceHotkeyCapture() {
	if a.voiceHotkeyCapturing || a.closing {
		return
	}
	a.voiceHotkeyCapturing = true
	a.voiceHotkeyPreviewParts = nil
	if a.voiceInput.stopHotkey != nil {
		a.voiceInput.stopHotkey()
		a.voiceInput.stopHotkey = nil
	}
	stop, err := startNativeHotkeyCapture(func(parts []string, value string) {
		a.win.Update(func() {
			if !a.voiceHotkeyCapturing {
				return
			}
			a.voiceHotkeyPreviewParts = parts
		})
	}, func(value string) {
		a.win.Update(func() { a.finishVoiceHotkeyCapture(value) })
	})
	if err != nil {
		a.voiceHotkeyCapturing = false
		a.notifyError("无法开始录制快捷键：" + err.Error())
		if a.prefs.VoiceInputEnabled {
			a.installVoiceHotkey()
		}
		return
	}
	a.voiceHotkeyCaptureStop = stop
	a.voiceHotkeyCaptureTimer = time.AfterFunc(15*time.Second, func() {
		a.win.Update(func() { a.finishVoiceHotkeyCapture("") })
	})
}

// finishVoiceHotkeyCapture commits a captured chord, or cancels when value is
// empty, and restores the recording hook either way.
func (a *desktop) finishVoiceHotkeyCapture(value string) {
	if !a.voiceHotkeyCapturing {
		return
	}
	label := strings.Join(a.voiceHotkeyPreviewParts, " + ")
	a.voiceHotkeyCapturing = false
	a.voiceHotkeyPreviewParts = nil
	if a.voiceHotkeyCaptureTimer != nil {
		a.voiceHotkeyCaptureTimer.Stop()
		a.voiceHotkeyCaptureTimer = nil
	}
	if stop := a.voiceHotkeyCaptureStop; stop != nil {
		a.voiceHotkeyCaptureStop = nil
		// Hand the hook back before installing the recorder's own. Releasing it
		// asynchronously let its cleanup run after the new hook had claimed the
		// slot, which cleared it and left the new chord unable to fire.
		stop()
	}
	if strings.TrimSpace(value) == "" {
		if label != "" {
			a.notifyError(label + " 需要搭配至少一个修饰键（Ctrl/Alt/Shift/Win）才能不漏键")
		}
		if a.prefs.VoiceInputEnabled {
			a.installVoiceHotkey()
		}
		return
	}
	a.setVoiceHotkey(value)
}
