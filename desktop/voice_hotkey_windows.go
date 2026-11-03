//go:build windows

// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"errors"
	"runtime"
	"sync"
	"unsafe"

	"github.com/egoist/mygo/ui"
	"golang.org/x/sys/windows"
)

const (
	whKeyboardLL = 13
	hcAction     = 0
	wmQuit       = 0x0012
	wmKeyDown    = 0x0100
	wmKeyUp      = 0x0101
	wmSysKeyDown = 0x0104
	wmSysKeyUp   = 0x0105
	vkTab        = 0x09
	vkEscape     = 0x1B
)

type lowLevelKey struct {
	vk, scan, flags, when uint32
	extra                 uintptr
}
type winMessage struct {
	hwnd     uintptr
	msg      uint32
	pad      uint32
	wparam   uintptr
	lparam   uintptr
	when     uint32
	ptX, ptY int32
	pad2     uint32
}

// voiceKeyboardHook is the part of a hook that turns one keyboard event into a
// decision: handle reports whether this hook consumes the event, which is how a
// recording chord stops typing its own keys into the focused control.
type voiceKeyboardHook interface {
	handle(event, vk uint32) bool
}

var voiceHookMu sync.Mutex
var voiceHookCurrent voiceKeyboardHook

var voiceUser32 = windows.NewLazySystemDLL("user32.dll")
var procSetWindowsHookEx = voiceUser32.NewProc("SetWindowsHookExW")
var procCallNextHookEx = voiceUser32.NewProc("CallNextHookEx")
var procUnhookWindowsHookEx = voiceUser32.NewProc("UnhookWindowsHookEx")
var procGetMessage = voiceUser32.NewProc("GetMessageW")
var procPeekMessage = voiceUser32.NewProc("PeekMessageW")
var procTranslateMessage = voiceUser32.NewProc("TranslateMessage")
var procDispatchMessage = voiceUser32.NewProc("DispatchMessageW")
var procPostThreadMessage = voiceUser32.NewProc("PostThreadMessageW")
var procGetForegroundWindow = voiceUser32.NewProc("GetForegroundWindow")
var procGetWindowThreadProcessID = voiceUser32.NewProc("GetWindowThreadProcessId")

// voiceModifierVKs lists the virtual keys accepted for a modifier, indexed by
// side + 1: any side, left, right. Pinning a side makes only that physical key
// match, which is what "左 Alt" and "右 Ctrl" configure.
var voiceModifierVKs = map[ui.Modifiers][3]uint32{
	ui.Ctrl:  {0x11, 0xA2, 0xA3},
	ui.Alt:   {0x12, 0xA4, 0xA5},
	ui.Shift: {0x10, 0xA0, 0xA1},
	ui.Super: {0x5B, 0x5B, 0x5C},
}

// voiceModifierTokensByVK names the token a captured modifier key stands for.
// Left and right collapse to one token: which side counts is the separate
// 修饰键生效方式 preference.
var voiceModifierTokensByVK = map[uint32]string{
	0xA2: "Ctrl", 0xA3: "Ctrl", 0x11: "Ctrl",
	0xA4: "Alt", 0xA5: "Alt", 0x12: "Alt",
	0xA0: "Shift", 0xA1: "Shift", 0x10: "Shift",
	0x5B: "Super", 0x5C: "Super",
}

// voiceKeyByVK is the reverse of the shared key table, so a captured chord can
// be rendered with the same vocabulary the parser accepts.
var voiceKeyByVK = func() map[uint32]ui.Key {
	keys := make(map[uint32]ui.Key, len(voiceHotkeyKeyTokens))
	for key := range voiceHotkeyKeyTokens {
		if vk := voiceVirtualKey(key); vk != 0 {
			keys[vk] = key
		}
	}
	return keys
}()

// voiceVirtualKey is the virtual-key code of a shared key token.
func voiceVirtualKey(key ui.Key) uint32 {
	switch {
	case key >= ui.KeyA && key <= ui.KeyZ:
		return uint32('A') + uint32(key-ui.KeyA)
	case key >= ui.Key0 && key <= ui.Key9:
		return uint32('0') + uint32(key-ui.Key0)
	case key >= ui.KeyF1 && key <= ui.KeyF12:
		return 0x70 + uint32(key-ui.KeyF1)
	}
	switch key {
	case ui.KeySpace:
		return 0x20
	case ui.KeyEnter:
		return 0x0D
	case ui.KeyEscape:
		return vkEscape
	case ui.KeyTab:
		return vkTab
	case ui.KeyBackspace:
		return 0x08
	case ui.KeyDelete:
		return 0x2E
	case ui.KeyInsert:
		return 0x2D
	case ui.KeyHome:
		return 0x24
	case ui.KeyEnd:
		return 0x23
	case ui.KeyPageUp:
		return 0x21
	case ui.KeyPageDown:
		return 0x22
	case ui.KeyLeft:
		return 0x25
	case ui.KeyUp:
		return 0x26
	case ui.KeyRight:
		return 0x27
	case ui.KeyDown:
		return 0x28
	case ui.KeyMinus:
		return 0xBD
	case ui.KeyEqual:
		return 0xBB
	case ui.KeyComma:
		return 0xBC
	case ui.KeyPeriod:
		return 0xBE
	case ui.KeySlash:
		return 0xBF
	case ui.KeySemicolon:
		return 0xBA
	case ui.KeyQuote:
		return 0xDE
	case ui.KeyBracketLeft:
		return 0xDB
	case ui.KeyBracketRight:
		return 0xDD
	case ui.KeyBackslash:
		return 0xDC
	case ui.KeyBackquote:
		return 0xC0
	}
	return 0
}

// voiceHotkeyRequirement is the parsed chord in virtual keys: every listed key
// must be down, and for each modifier one of its accepted keys must be down.
type voiceHotkeyRequirement struct {
	keyVKs []uint32
	modVKs [][]uint32
}

// newVoiceHotkeyRequirement builds the accepted virtual keys for a chord. The
// left and right flags are the global 修饰键生效方式 preference: a side that is
// switched off never satisfies a modifier, while the generic code is accepted
// either way because some layouts report it instead of a side-specific one.
func newVoiceHotkeyRequirement(hotkey voiceHotkey, left, right bool) (voiceHotkeyRequirement, error) {
	var require voiceHotkeyRequirement
	for _, key := range hotkey.keys {
		vk := voiceVirtualKey(key)
		if vk == 0 {
			return require, errors.New("快捷键包含不支持的按键")
		}
		require.keyVKs = append(require.keyVKs, vk)
	}
	for _, spec := range voiceModifierSpecs {
		if hotkey.mods&spec.mod == 0 {
			continue
		}
		sides, ok := voiceModifierVKs[spec.mod]
		if !ok {
			return require, errors.New("快捷键包含不支持的修饰键")
		}
		accepted := []uint32{sides[0]}
		if left {
			accepted = append(accepted, sides[1])
		}
		if right {
			accepted = append(accepted, sides[2])
		}
		require.modVKs = append(require.modVKs, accepted)
	}
	if len(require.keyVKs) == 0 && len(require.modVKs) == 0 {
		return require, errors.New("快捷键格式无效")
	}
	return require, nil
}

func (r voiceHotkeyRequirement) satisfiedBy(down map[uint32]bool) bool {
	for _, vk := range r.keyVKs {
		if !down[vk] {
			return false
		}
	}
	for _, accepted := range r.modVKs {
		found := false
		for _, vk := range accepted {
			found = found || down[vk]
		}
		if !found {
			return false
		}
	}
	return true
}

// voiceHotkeyHook matches the hold-to-talk chord while Ally is in front. The
// chord's primary keys are swallowed while it records so the same keystrokes do
// not also reach the focused control.
type voiceHotkeyHook struct {
	require      voiceHotkeyRequirement
	owned        map[uint32]bool
	down         map[uint32]bool
	active       bool
	modifierOnly bool
	onDown       func()
	onUp         func()
}

func (h *voiceHotkeyHook) handle(event, vk uint32) bool {
	return h.handleEvent(event, vk, voiceForegroundIsAlly())
}

// handleEvent is the decision without the foreground query, so the chord and
// swallow rules are testable.
func (h *voiceHotkeyHook) handleEvent(event, vk uint32, isAlly bool) bool {
	down := event == wmKeyDown || event == wmSysKeyDown
	up := event == wmKeyUp || event == wmSysKeyUp
	if !down && !up {
		return false
	}
	if down {
		h.down[vk] = true
	} else {
		delete(h.down, vk)
	}
	wasActive := h.active
	matching := h.require.satisfiedBy(h.down)
	activated := false
	switch {
	case down && wasActive && !isAlly:
		// A modifier-only shortcut must not turn a system shortcut into a
		// recording just because another window took the foreground.
		h.active = false
		go h.onUp()
	case down && !wasActive && matching && isAlly:
		if !(h.modifierOnly && h.down[vkTab]) {
			h.active = true
			activated = true
			go h.onDown()
		}
	case wasActive && (!matching || !isAlly):
		h.active = false
		go h.onUp()
	}
	return (activated || wasActive) && h.owned[vk]
}

// voiceHotkeyCaptureHook reads a chord for the settings field: it reports every
// change and finishes once all keys are released. Escape alone cancels.
type voiceHotkeyCaptureHook struct {
	down     map[uint32]bool
	order    []uint32
	started  bool
	onChange func(parts []string, value string)
	onFinish func(value string)
}

func (h *voiceHotkeyCaptureHook) handle(event, vk uint32) bool {
	down := event == wmKeyDown || event == wmSysKeyDown
	up := event == wmKeyUp || event == wmSysKeyUp
	if !down && !up {
		return false
	}
	if down && !h.down[vk] {
		h.down[vk] = true
		h.order = append(h.order, vk)
		h.started = true
	} else if up {
		delete(h.down, vk)
	}
	if down && vk == vkEscape && len(h.order) == 1 {
		h.onFinish("")
		return false
	}
	if up && h.started && len(h.down) == 0 {
		value, _, _ := voiceHotkeyFromVirtualKeys(h.order)
		h.onFinish(value)
		return false
	}
	if h.started && h.onChange != nil {
		value, parts, _ := voiceHotkeyFromVirtualKeys(h.order)
		h.onChange(parts, value)
	}
	return false
}

// voiceHotkeyFromVirtualKeys renders a captured chord as its config value and
// its keycap sequence. value is empty when the chord is not usable, e.g. two
// plain keys without a modifier.
func voiceHotkeyFromVirtualKeys(pressed []uint32) (value string, parts []string, ok bool) {
	modTokens := make([]string, 0, 4)
	keyTokens := make([]string, 0, voiceHotkeyMaxKeys)
	seenMods := map[ui.Modifiers]bool{}
	for _, vk := range pressed {
		if token, isModifier := voiceModifierTokensByVK[vk]; isModifier {
			mod, valid := voiceModifierByToken(token)
			if !valid || seenMods[mod] {
				continue
			}
			seenMods[mod] = true
			modTokens = append(modTokens, token)
			continue
		}
		if key, valid := voiceKeyByVK[vk]; valid {
			keyTokens = append(keyTokens, voiceHotkeyKeyToken(key))
		}
	}
	if value, ok = buildVoiceHotkey(modTokens, keyTokens); ok {
		return value, voiceHotkeyParts(value), true
	}
	return "", voiceHotkeyTokenParts(modTokens, keyTokens), false
}

func voiceForegroundIsAlly() bool {
	foreground, _, _ := procGetForegroundWindow.Call()
	if foreground == 0 {
		return false
	}
	var pid uint32
	procGetWindowThreadProcessID.Call(foreground, uintptr(unsafe.Pointer(&pid)))
	return pid == windows.GetCurrentProcessId()
}

// startNativeVoiceHotkey installs the recording hook for one chord.
func startNativeVoiceHotkey(value string, left, right bool, onDown, onUp func()) (func(), error) {
	hotkey, ok := parseVoiceHotkey(value)
	if !ok {
		return nil, errors.New("快捷键格式无效")
	}
	require, err := newVoiceHotkeyRequirement(hotkey, left, right)
	if err != nil {
		return nil, err
	}
	owned := make(map[uint32]bool, len(require.keyVKs))
	for _, vk := range require.keyVKs {
		owned[vk] = true
	}
	handle, err := startVoiceHook(&voiceHotkeyHook{
		require:      require,
		owned:        owned,
		down:         map[uint32]bool{},
		modifierOnly: len(require.keyVKs) == 0,
		onDown:       onDown,
		onUp:         onUp,
	})
	if err != nil {
		return nil, err
	}
	return handle.stop, nil
}

// startNativeHotkeyCapture installs the hook that reads a chord for the
// settings field. It replaces the recording hook while it is open.
func startNativeHotkeyCapture(onChange func(parts []string, value string), onFinish func(value string)) (func(), error) {
	handle, err := startVoiceHook(&voiceHotkeyCaptureHook{
		down:     map[uint32]bool{},
		onChange: onChange,
		onFinish: onFinish,
	})
	if err != nil {
		return nil, err
	}
	return handle.stop, nil
}

type voiceHookHandle struct {
	done     chan struct{}
	threadID uint32
}

func (h *voiceHookHandle) stop() {
	if h == nil {
		return
	}
	procPostThreadMessage.Call(uintptr(h.threadID), wmQuit, 0, 0)
	<-h.done
}

// startVoiceHook installs a low-level keyboard hook on its own thread and pumps
// its message loop until stop posts WM_QUIT.
func startVoiceHook(hook voiceKeyboardHook) (*voiceHookHandle, error) {
	handle := &voiceHookHandle{done: make(chan struct{})}
	ready := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		var msg winMessage
		// Create the thread message queue before another thread posts WM_QUIT.
		procPeekMessage.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0, 0, 0)
		handle.threadID = windows.GetCurrentThreadId()
		voiceHookMu.Lock()
		voiceHookCurrent = hook
		voiceHookMu.Unlock()
		callback := windows.NewCallback(voiceLowLevelCallback)
		installed, _, callErr := procSetWindowsHookEx.Call(whKeyboardLL, callback, 0, 0)
		if installed == 0 {
			voiceHookMu.Lock()
			voiceHookCurrent = nil
			voiceHookMu.Unlock()
			ready <- callErr
			close(handle.done)
			return
		}
		ready <- nil
		defer func() {
			procUnhookWindowsHookEx.Call(installed)
			voiceHookMu.Lock()
			voiceHookCurrent = nil
			voiceHookMu.Unlock()
			close(handle.done)
		}()
		for {
			ret, _, _ := procGetMessage.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
			if int32(ret) <= 0 {
				return
			}
			procTranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
			procDispatchMessage.Call(uintptr(unsafe.Pointer(&msg)))
		}
	}()
	if err := <-ready; err != nil {
		return nil, err
	}
	return handle, nil
}

func voiceLowLevelCallback(code int, wparam uintptr, lparam uintptr) uintptr {
	if code == hcAction && lparam != 0 {
		key := (*lowLevelKey)(unsafe.Pointer(lparam))
		voiceHookMu.Lock()
		hook := voiceHookCurrent
		swallow := false
		if hook != nil {
			swallow = hook.handle(uint32(wparam), key.vk)
		}
		voiceHookMu.Unlock()
		if swallow {
			// Non-zero stops Windows from passing the key on.
			return 1
		}
	}
	result, _, _ := procCallNextHookEx.Call(0, uintptr(code), wparam, lparam)
	return result
}
