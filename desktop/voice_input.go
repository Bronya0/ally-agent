// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"ally-dev/desktop/uikit"
	backend "ally-dev/internal/app"
	"github.com/egoist/mygo/ui"
)

type voiceInputRuntime struct {
	prepared         nativeAudioRecording
	preparedDeviceID string
	active           nativeAudioRecording
	target           *conversation
	timer            *time.Timer
	pressed          bool
	busy             bool
	quietWarmup      bool
	takes            voiceTakeQueue
	// level holds math.Float32bits of the live capture level. The capture pump
	// stores it and the meter paints it, so a recording never rebuilds the view
	// just to move a bar.
	level      atomic.Uint32
	levelStop  chan struct{}
	stopHotkey func()
}

// voiceTakeQueue holds recordings waiting for the single transcription worker.
// Queued takes are kept in recording order and never transcribed concurrently.
type voiceTakeQueue struct {
	mu          sync.Mutex
	pending     []voiceTake
	outstanding int
	worker      bool
	nextSeq     uint64
}

func (v *voiceInputRuntime) setLevel(level float32) { v.level.Store(math.Float32bits(level)) }

func (v *voiceInputRuntime) levelValue() float32 { return math.Float32frombits(v.level.Load()) }

// dropQueuedTakes forgets recordings that have not started transcribing. A
// request already in flight still publishes: it is cheap and its words are not
// worth discarding.
func (a *desktop) dropQueuedTakes() {
	q := &a.voiceInput.takes
	q.mu.Lock()
	queued := len(q.pending)
	q.pending = nil
	q.mu.Unlock()
	for i := 0; i < queued; i++ {
		a.voiceInput.finishTake()
	}
}

func (a *desktop) configureVoiceInput() {
	if a.closing {
		a.closeVoiceInput()
		return
	}
	if !a.prefs.VoiceInputEnabled {
		a.voiceInput.quietWarmup = false
		a.voiceInput.preparedDeviceID = ""
		a.dropQueuedTakes()
		if a.voiceInput.levelStop != nil {
			close(a.voiceInput.levelStop)
			a.voiceInput.levelStop = nil
		}
		if a.voiceInput.stopHotkey != nil {
			a.voiceInput.stopHotkey()
			a.voiceInput.stopHotkey = nil
		}
		a.voiceInput.pressed = false
		if a.voiceInput.timer != nil {
			a.voiceInput.timer.Stop()
			a.voiceInput.timer = nil
		}
		if r := a.voiceInput.active; r != nil {
			a.voiceInput.active = nil
			go r.Cancel()
		}
		if r := a.voiceInput.prepared; r != nil {
			a.voiceInput.prepared = nil
			go r.Cancel()
		}
		return
	}
	if !voiceModelIDExists(a.config.VoiceModels, a.prefs.VoiceModelID) {
		if len(a.config.VoiceModels) == 0 {
			a.notifyError("请先添加语音模型，再启用语音输入")
			return
		}
		a.prefs.VoiceModelID = a.config.VoiceModels[0].ID
		a.savePrefs()
	}
	if a.voiceInput.prepared != nil && a.voiceInput.preparedDeviceID == a.prefs.VoiceInputDeviceID {
		a.voiceInput.quietWarmup = false
		a.installVoiceHotkey()
		return
	}
	if a.voiceInput.prepared != nil {
		old := a.voiceInput.prepared
		a.voiceInput.prepared = nil
		a.voiceInput.preparedDeviceID = ""
		go old.Cancel()
	}
	if a.voiceInput.active != nil || a.voiceInput.busy {
		return
	}
	a.voiceInput.busy = true
	deviceID := a.prefs.VoiceInputDeviceID
	go func() {
		recorder, err := startNativeAudioRecording(deviceID)
		a.win.Update(func() {
			a.voiceInput.busy = false
			a.voiceInput.quietWarmup = false
			if deviceID != a.prefs.VoiceInputDeviceID {
				if recorder != nil {
					go recorder.Cancel()
				}
				a.configureVoiceInput()
				return
			}
			if !a.prefs.VoiceInputEnabled {
				if recorder != nil {
					go recorder.Cancel()
				}
				return
			}
			if err != nil {
				if errors.Is(err, errVoiceCaptureDeviceUnavailable) && deviceID != "" {
					a.prefs.VoiceInputDeviceID = ""
					a.savePrefs()
					a.notifyError("所选麦克风已不可用，已切换到系统默认输入设备")
					a.configureVoiceInput()
					return
				}
				a.prefs.VoiceInputEnabled = false
				a.savePrefs()
				a.notifyError("语音录音预热失败：" + err.Error())
				return
			}
			a.voiceInput.prepared = recorder
			a.voiceInput.preparedDeviceID = deviceID
			a.installVoiceHotkey()
		})
	}()
}

func (a *desktop) refreshVoiceInputDevices() {
	if a.voiceInputDevicesBusy || a.win == nil {
		return
	}
	a.voiceInputDevicesBusy = true
	go func() {
		devices, err := listNativeAudioInputDevices()
		a.win.Update(func() {
			a.voiceInputDevicesBusy = false
			a.voiceInputDevicesError = ""
			if err != nil {
				a.voiceInputDevicesError = err.Error()
				return
			}
			a.voiceInputDevices = devices
			if a.prefs.VoiceInputDeviceID == "" || voiceCaptureDeviceAvailable(devices, a.prefs.VoiceInputDeviceID) {
				return
			}
			a.prefs.VoiceInputDeviceID = ""
			a.savePrefs()
			a.resetVoiceInputDeviceSession()
			a.notify("所选麦克风已断开，已切换到系统默认输入设备")
			if a.prefs.VoiceInputEnabled {
				a.configureVoiceInput()
			}
		})
	}()
}

func voiceCaptureDeviceAvailable(devices []voiceCaptureDevice, id string) bool {
	for _, device := range devices {
		if device.ID == id {
			return true
		}
	}
	return false
}

func (a *desktop) selectVoiceInputDevice(id string) {
	if id == a.prefs.VoiceInputDeviceID {
		return
	}
	a.prefs.VoiceInputDeviceID = id
	a.savePrefs()
	a.resetVoiceInputDeviceSession()
	if a.prefs.VoiceInputEnabled {
		a.configureVoiceInput()
	}
}

func (a *desktop) resetVoiceInputDeviceSession() {
	if a.voiceInput.stopHotkey != nil {
		a.voiceInput.stopHotkey()
		a.voiceInput.stopHotkey = nil
	}
	if a.voiceInput.levelStop != nil {
		close(a.voiceInput.levelStop)
		a.voiceInput.levelStop = nil
	}
	if a.voiceInput.timer != nil {
		a.voiceInput.timer.Stop()
		a.voiceInput.timer = nil
	}
	for _, recorder := range []nativeAudioRecording{a.voiceInput.active, a.voiceInput.prepared} {
		if recorder != nil {
			go recorder.Cancel()
		}
	}
	a.voiceInput.active, a.voiceInput.prepared = nil, nil
	a.voiceInput.preparedDeviceID = ""
	a.voiceInput.target = nil
	a.voiceInput.pressed = false
}

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

// voiceModifierSidesNote spells out which physical sides count. The side
// preference silently decides whether a chord fires, so leaving it implicit made
// a bound Alt+L look broken whenever only one side was enabled.
func voiceModifierSidesNote(left, right bool) string {
	switch {
	case left && right:
		return "「修饰键」左/右都选：按左、右侧的修饰键都能触发。"
	case left:
		return "当前只认左侧修饰键：按右侧（如右 Alt）不会触发录音。"
	case right:
		return "当前只认右侧修饰键：按左侧（如左 Alt）不会触发录音。"
	}
	return "「修饰键」至少保留一侧，否则快捷键不会生效。"
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

func (a *desktop) beginVoiceInput(t *conversation) {
	r := a.voiceInput.prepared
	if r == nil || a.voiceInput.active != nil || a.voiceInput.busy {
		return
	}
	a.voiceInput.prepared = nil
	a.voiceInput.busy = true
	a.voiceInput.target = t
	a.voiceInput.pressed = true
	deviceID := a.voiceInput.preparedDeviceID
	go func() {
		err := r.Start()
		if err != nil {
			r.Cancel()
		}
		a.win.Update(func() {
			a.voiceInput.busy = false
			if deviceID != a.prefs.VoiceInputDeviceID {
				if err == nil {
					go r.Cancel()
				}
				a.voiceInput.pressed = false
				a.configureVoiceInput()
				return
			}
			if !a.prefs.VoiceInputEnabled || a.closing {
				if err == nil {
					go r.Cancel()
				}
				return
			}
			if err != nil {
				if errors.Is(err, errVoiceCaptureDeviceUnavailable) && deviceID != "" {
					a.prefs.VoiceInputDeviceID = ""
					a.savePrefs()
					a.notifyError("所选麦克风已断开，已切换到系统默认输入设备")
					a.voiceInput.preparedDeviceID = ""
					a.configureVoiceInput()
					return
				}
				a.voiceInput.pressed = false
				a.notifyError("启动麦克风失败：" + err.Error())
				a.configureVoiceInput()
				return
			}
			a.voiceInput.active = r
			a.voiceInput.setLevel(0)
			a.voiceInput.levelStop = make(chan struct{})
			levelStop := a.voiceInput.levelStop
			a.voiceInput.timer = time.AfterFunc(voiceCaptureMaxDuration, func() { a.win.Update(func() { a.finishVoiceInput(true) }) })
			if !a.voiceInput.pressed {
				a.finishVoiceInput(false)
			}
			// Levels go straight to the meter's painted state: publishing them
			// through win.Update rebuilt the whole conversation tree up to ten
			// times a second while recording.
			go func() {
				levels := r.Levels()
				for {
					select {
					case <-levelStop:
						return
					case level, ok := <-levels:
						if !ok {
							return
						}
						a.voiceInput.setLevel(level)
					}
				}
			}()
		})
	}()
}

func (a *desktop) finishVoiceInput(timeout bool) {
	r := a.voiceInput.active
	if r == nil {
		a.voiceInput.pressed = false
		return
	}
	if a.voiceInput.levelStop != nil {
		close(a.voiceInput.levelStop)
		a.voiceInput.levelStop = nil
	}
	a.voiceInput.active = nil
	a.voiceInput.pressed = false
	a.voiceInput.setLevel(0)
	if a.voiceInput.timer != nil {
		a.voiceInput.timer.Stop()
		a.voiceInput.timer = nil
	}
	target := a.voiceInput.target
	a.voiceInput.target = nil
	model := voiceModelByID(a.config.VoiceModels, a.prefs.VoiceModelID)
	// The take joins the queue in recording order even though Stop() and the
	// transcription are asynchronous, so a fast second take can never overtake
	// the first one's text.
	take := voiceTake{seq: a.voiceInput.nextTakeSeq(), model: model, target: target, timeout: timeout}
	a.voiceInput.beginTake()
	go func() {
		wav, err := r.Stop()
		canReuse := recorderCanReuse(r)
		a.win.Update(func() {
			if !a.prefs.VoiceInputEnabled || a.closing {
				go r.Cancel()
				a.voiceInput.finishTake()
				return
			}
			if canReuse {
				a.voiceInput.prepared = r
				a.installVoiceHotkey()
			} else {
				go r.Cancel()
				a.voiceInput.quietWarmup = true
				a.configureVoiceInput()
			}
			if err != nil {
				a.voiceInput.finishTake()
				a.notifyError("语音转写失败：" + err.Error())
				return
			}
			take.wav = wav
			a.enqueueVoiceTake(take)
		})
	}()
}

// voiceTake is one finished recording waiting to be transcribed. seq is assigned
// when recording ends, so the queue can always publish in recording order.
type voiceTake struct {
	seq     uint64
	wav     []byte
	model   backend.VoiceModelConfig
	target  *conversation
	timeout bool
}

// voiceTakeQueueLimit bounds how many takes may be queued or in flight. A
// dictation clip is short, so a deeper backlog would only add latency the user
// cannot see; the oldest waiting take is dropped instead.
const voiceTakeQueueLimit = 3

func (v *voiceInputRuntime) nextTakeSeq() uint64 {
	v.takes.nextSeq++
	return v.takes.nextSeq
}

func (v *voiceInputRuntime) beginTake() {
	v.takes.mu.Lock()
	v.takes.outstanding++
	v.takes.mu.Unlock()
}

func (v *voiceInputRuntime) finishTake() {
	v.takes.mu.Lock()
	if v.takes.outstanding > 0 {
		v.takes.outstanding--
	}
	v.takes.mu.Unlock()
}

// outstandingTakes reports how many recordings are queued or being transcribed.
func (v *voiceInputRuntime) outstandingTakes() int {
	v.takes.mu.Lock()
	defer v.takes.mu.Unlock()
	return v.takes.outstanding
}

// queueTake inserts a take in recording order and reports whether the queue was
// full (so the oldest waiting take was dropped) and whether the worker must be
// started.
func (q *voiceTakeQueue) queueTake(take voiceTake) (dropped, start bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.pending) >= voiceTakeQueueLimit {
		q.pending = q.pending[1:]
		dropped = true
	}
	q.pending = append(q.pending, take)
	sort.Slice(q.pending, func(i, j int) bool { return q.pending[i].seq < q.pending[j].seq })
	start = !q.worker
	q.worker = true
	return dropped, start
}

// next hands out the oldest queued take and clears the worker flag once nothing
// is left, so the next enqueue starts a fresh worker.
func (q *voiceTakeQueue) next() (voiceTake, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.pending) == 0 {
		q.worker = false
		return voiceTake{}, false
	}
	take := q.pending[0]
	q.pending = q.pending[1:]
	return take, true
}

// enqueueVoiceTake queues a take behind the ones already waiting and starts the
// single worker that transcribes them one at a time.
func (a *desktop) enqueueVoiceTake(take voiceTake) {
	dropped, start := a.voiceInput.takes.queueTake(take)
	if dropped {
		a.voiceInput.finishTake()
		a.notify("已丢弃最早的一条录音（最多排队 " + strconv.Itoa(voiceTakeQueueLimit) + " 条）")
	}
	if start {
		go a.runVoiceTakes()
	}
}

// runVoiceTakes is the single transcription worker: one request at a time, in
// recording order, so both the endpoint and the draft see a stable sequence.
func (a *desktop) runVoiceTakes() {
	for {
		take, ok := a.voiceInput.takes.next()
		if !ok {
			return
		}
		text, err := a.core.TestNativeVoiceModelAudio(take.model, take.wav)
		a.win.Update(func() { a.publishVoiceTranscript(take, text, err) })
	}
}

// publishVoiceTranscript applies one finished take on the main thread.
func (a *desktop) publishVoiceTranscript(take voiceTake, text string, err error) {
	a.voiceInput.finishTake()
	if take.timeout {
		a.notify("语音录音已达到 20 秒上限")
	}
	if err != nil {
		a.notifyError("语音转写失败：" + err.Error())
		return
	}
	if strings.TrimSpace(text) == "" {
		return
	}
	target := take.target
	if target == nil || !conversationOpen(a.tabs, target) {
		return
	}
	target.Draft = appendVoiceTranscript(target.Draft, text)
	target.uiState().DraftCaret = len([]rune(target.Draft))
	target.uiState().RestoreCaret = true
	a.composerFocus = target.ID
}

func recorderCanReuse(r nativeAudioRecording) bool {
	state, ok := r.(interface{ CanReuse() bool })
	return ok && state.CanReuse()
}

func appendVoiceTranscript(draft, transcript string) string {
	transcript = strings.TrimSpace(transcript)
	if transcript == "" {
		return draft
	}
	if draft != "" && !strings.HasSuffix(draft, " ") && !strings.HasSuffix(draft, "\n") {
		draft += " "
	}
	return draft + transcript
}

func voiceModelByID(models []backend.VoiceModelConfig, id string) backend.VoiceModelConfig {
	for _, model := range models {
		if model.ID == id {
			return model
		}
	}
	return backend.VoiceModelConfig{}
}

func conversationOpen(tabs []*conversation, target *conversation) bool {
	for _, tab := range tabs {
		if tab == target {
			return true
		}
	}
	return false
}

func (a *desktop) installVoiceHotkey() {
	// The capture hook owns the single low-level hook slot while the settings
	// field is listening; it reinstalls the recording hook when it finishes.
	if !a.prefs.VoiceInputEnabled || a.voiceInput.prepared == nil || a.voiceHotkeyCapturing || a.closing {
		return
	}
	if a.voiceInput.stopHotkey != nil {
		if voiceHotkeyHookLive() {
			return
		}
		// The handle outlived its hook, so the chord is silently unbound: drop the
		// handle and install again instead of leaving the shortcut dead.
		a.voiceInput.stopHotkey = nil
	}
	stop, err := startNativeVoiceHotkey(a.prefs.VoiceHotkey, a.prefs.VoiceModifierLeft, a.prefs.VoiceModifierRight, func() {
		// win.Update posts to the main loop's FIFO queue, so down and up keep
		// their order without a goroutine that could overtake its own partner.
		a.win.Update(func() {
			if a.closing {
				return
			}
			if a.page == "chat" {
				a.beginVoiceInput(a.current())
			}
		})
	}, func() {
		a.win.Update(func() {
			if !a.closing {
				a.finishVoiceInput(false)
			}
		})
	})
	if err != nil {
		a.notifyError("语音快捷键启动失败：" + err.Error())
		return
	}
	a.voiceInput.stopHotkey = stop
}

func (a *desktop) restartVoiceHotkey() {
	if a.voiceInput.stopHotkey != nil {
		a.voiceInput.stopHotkey()
		a.voiceInput.stopHotkey = nil
	}
	a.installVoiceHotkey()
}

func (a *desktop) closeVoiceInput() {
	a.dropQueuedTakes()
	if a.voiceHotkeyCapturing {
		a.finishVoiceHotkeyCapture("")
	}
	if a.voiceInput.stopHotkey != nil {
		a.voiceInput.stopHotkey()
		a.voiceInput.stopHotkey = nil
	}
	if a.voiceInput.timer != nil {
		a.voiceInput.timer.Stop()
		a.voiceInput.timer = nil
	}
	for _, recorder := range []nativeAudioRecording{a.voiceInput.active, a.voiceInput.prepared} {
		if recorder != nil {
			go recorder.Cancel()
		}
	}
	if a.voiceInput.levelStop != nil {
		close(a.voiceInput.levelStop)
		a.voiceInput.levelStop = nil
	}
	a.voiceInput.active, a.voiceInput.prepared = nil, nil
	a.voiceInput.preparedDeviceID = ""
	a.voiceInput.pressed = false
}

// hotkeyKeycaps draws a chord the way shortcut editors do: one badge per key
// joined by a plus, so the bound keys are readable without parsing a text field.
func (a *desktop) hotkeyKeycaps(c *ui.Context, parts []string, placeholder string) {
	if len(parts) == 0 {
		ui.Text(c, placeholder).FontSize(fsTiny).TextColor(a.secondaryInk())
		return
	}
	theme := c.Theme()
	ui.Row(c.Key("voice-hotkey-keys")).Gap(gapInline).AlignItems(ui.Center).Children(func() {
		for i, part := range parts {
			if i > 0 {
				ui.Text(c, "+").FontSize(fsTiny).TextColor(a.secondaryInk())
			}
			ui.Box(c.Key(fmt.Sprintf("voice-hotkey-key-%d", i))).Padding(3, 8).Radius(radiusSmall).Background(theme.SurfaceHover).Border(1, theme.Border).Children(func() {
				ui.Text(c, part).FontSize(fsTiny).TextColor(theme.Text)
			})
		}
	})
}

// voiceStatusRow owns the live recorder strip above the composer. It lives in a
// Reveal so entering and leaving the recording state slides instead of shoving
// the input box down, and every label keeps a fixed width so nothing inside the
// row moves while the level changes.
func (a *desktop) voiceStatusRow(c *ui.Context) {
	recording := a.voiceInput.active != nil
	outstanding := a.voiceInput.outstandingTakes()
	visible := recording || (a.voiceInput.busy && !a.voiceInput.quietWarmup) || outstanding > 0
	accent := a.currentPalette().accent
	uikit.Reveal(c, visible, func() {
		ui.Row(c.Key("voice-input-live-status")).Gap(gapField).AlignItems(ui.Center).Children(func() {
			switch {
			case recording:
				ui.Text(c, "录音中").FontSize(fsTiny).TextColor(accent)
				a.voiceLevelMeter(c, accent)
				if outstanding > 0 {
					fieldNote(c, fmt.Sprintf("稍后转录 %d 条", outstanding), a.secondaryInk())
				} else {
					fieldNote(c, "松开快捷键结束", a.secondaryInk())
				}
			case outstanding > 0:
				ui.Spinner(c).Size(13, 13)
				text := "正在转录…"
				if outstanding > 1 {
					text = fmt.Sprintf("正在转录…（还有 %d 条排队）", outstanding-1)
				}
				ui.Text(c, text).FontSize(fsTiny).TextColor(accent)
			default:
				ui.Spinner(c).Size(13, 13)
				ui.Text(c, "语音输入正在初始化…").FontSize(fsTiny).TextColor(accent)
			}
		})
	})
}

// voiceLevelMeter paints the capture level itself. Reading the atomic level in
// Draw and asking for animation frames keeps a moving bar off the rebuild path.
func (a *desktop) voiceLevelMeter(c *ui.Context, accent ui.Color) {
	track := c.Theme().SurfaceHover
	ui.Box(c.Key("voice-level-meter")).Width(96).Height(6).Draw(func(p *ui.Painter, r ui.Rect) {
		p.Fill(r, track, r.H)
		if level := a.voiceInput.levelValue(); level > 0 {
			p.Fill(ui.Rect{X: r.X, Y: r.Y, W: r.W * level, H: r.H}, accent, r.H)
		}
		if !uikit.StillMotion(c) {
			p.AnimationFrameRate(30)
		}
	})
}
