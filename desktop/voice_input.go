// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"errors"
	"fmt"
	"math"
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
	// aborted marks a take that must not be transcribed, including one whose recorder is
	// still starting.
	aborted     bool
	busy        bool
	quietWarmup bool
	takes       voiceTakeQueue
	// level holds math.Float32bits of the live capture level. The capture pump
	// stores it and the meter paints it, so a recording never rebuilds the view
	// just to move a bar.
	level      atomic.Uint32
	levelStop  chan struct{}
	stopHotkey func()
	// recordable mirrors "the shortcut can start a recording right now". The hook
	// thread reads it to decide whether to swallow the chord's keys.
	recordable atomic.Bool
}

// recordablePage reports whether the current page can accept dictation. Only the
// chat page has a composer to fill, so anywhere else the shortcut must stay out
// of the way instead of eating the keystrokes.
func (a *desktop) recordablePage() bool {
	return a.ready && a.page == "chat" && a.prefs.VoiceInputEnabled && a.voiceInput.prepared != nil
}

// syncVoiceRecordable publishes the current answer to the hook thread. It is called
// whenever the recorder is prepared or released as well as every frame, so the hook
// never swallows keys for a recording that cannot start.
func (a *desktop) syncVoiceRecordable() {
	a.voiceInput.recordable.Store(a.recordablePage())
}

// voiceTakeQueue holds recordings waiting for the single transcription worker.
// Queued takes are kept in recording order and never transcribed concurrently.
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
		a.disposeVoiceInput()
		return
	}
	// Dictation needs an ASR model: a TTS model has no transcription request.
	asrModels := voiceModelsOfKind(a.config.VoiceModels, backend.VoiceKindASR)
	if len(asrModels) == 0 {
		if a.prefs.VoiceInputEnabled {
			// Deleting the last ASR model must not leave the preference stuck
			// on: the settings page hides the switch while no model exists.
			a.prefs.VoiceInputEnabled = false
			a.savePrefs()
			// Tear down anything the preference had already warmed up (hotkey,
			// recorder); the disabled branch above no longer runs for this call.
			a.disposeVoiceInput()
		}
		a.notifyError("请先添加语音识别（ASR）模型，再启用语音输入")
		return
	}
	if !voiceModelIDExists(asrModels, a.prefs.VoiceModelID) {
		a.prefs.VoiceModelID = asrModels[0].ID
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
					// One fallback path for every stage: clear the pin, say so and warm up
					// again on the default device.
					a.fallBackToDefaultVoiceDevice()
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

// disposeVoiceInput releases the hotkey, timers and recorders owned by the
// dictation preference. Shared by the disabled branch and the no-ASR branch so
// clearing the preference cannot leave a warmed-up recorder behind.
func (a *desktop) disposeVoiceInput() {
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
}

func (a *desktop) refreshVoiceInputDevices() {
	if a.voiceInputDevicesBusy || a.win == nil {
		return
	}
	a.voiceInputDevicesBusy = true
	a.voiceInputDevicesScanned = true
	go func() {
		devices, err := listNativeAudioInputDevices()
		a.win.Update(func() {
			a.voiceInputDevicesBusy = false
			a.voiceInputDevicesError = ""
			if err != nil {
				a.voiceInputDevicesError = err.Error()
				// Let the next visit retry: a transient COM failure must not disable the
				// scan for the rest of the session.
				a.voiceInputDevicesScanned = false
				return
			}
			a.voiceInputDevices = devices
			if a.prefs.VoiceInputDeviceID == "" || voiceCaptureDeviceAvailable(devices, a.prefs.VoiceInputDeviceID) {
				return
			}
			a.prefs.VoiceInputDeviceID = ""
			a.savePrefs()
			a.resetVoiceInputDeviceSession()
			if a.prefs.VoiceInputEnabled {
				a.notify("所选麦克风不可用，已切换到系统默认输入设备")
				a.configureVoiceInput()
			}
		})
	}()
}

// fallBackToDefaultVoiceDevice clears a pinned microphone that turned out to be
// unusable, tells the user once and warms the session up again. Every stage that can
// discover a dead device (warm-up, start, rescan) goes through this one path.
func (a *desktop) fallBackToDefaultVoiceDevice() {
	a.prefs.VoiceInputDeviceID = ""
	a.voiceInput.preparedDeviceID = ""
	a.savePrefs()
	a.notify("所选麦克风不可用，已切换到系统默认输入设备")
	a.configureVoiceInput()
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
	if a.voiceInput.active != nil {
		// Switching the microphone mid-take used to discard the words silently; say
		// so, because the user was talking.
		a.notify("已切换麦克风，本次录音已丢弃")
	}
	a.voiceInput.active, a.voiceInput.prepared = nil, nil
	a.voiceInput.preparedDeviceID = ""
	a.voiceInput.target = nil
	a.voiceInput.pressed = false
	a.syncVoiceRecordable()
}

func (a *desktop) beginVoiceInput(t *conversation) {
	r := a.voiceInput.prepared
	if r == nil || a.voiceInput.active != nil || a.voiceInput.busy {
		// The hook only arms when it thinks a take can start; keep it honest for
		// the next press instead of waiting for the next frame.
		a.syncVoiceRecordable()
		return
	}
	a.voiceInput.prepared = nil
	a.voiceInput.aborted = false
	a.syncVoiceRecordable()
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
			if a.voiceInput.aborted {
				// The take was cancelled while the device was starting; do not start it
				// again or finish it.
				if err == nil {
					go r.Cancel()
				}
				return
			}
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
					a.fallBackToDefaultVoiceDevice()
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
			if a.voiceInput.aborted {
				// Aborted while starting: retire it without enqueueing a take.
				a.abortVoiceInput()
				return
			}
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

// abortVoiceInput stops the current take without transcribing it. It is used when
// the chord turned out to be a system shortcut, and when the selected model has
// become unusable: neither case should spend a request or write anything.
func (a *desktop) abortVoiceInput() {
	r := a.voiceInput.active
	// Mark the abort even before the recorder is assigned: beginVoiceInput starts the
	// device asynchronously, and without this flag that callback would finish the
	// already-aborted take, spend a request on silence and report a misleading error.
	a.voiceInput.aborted = true
	a.voiceInput.active = nil
	a.voiceInput.pressed = false
	if a.voiceInput.levelStop != nil {
		close(a.voiceInput.levelStop)
		a.voiceInput.levelStop = nil
	}
	a.voiceInput.setLevel(0)
	if a.voiceInput.timer != nil {
		a.voiceInput.timer.Stop()
		a.voiceInput.timer = nil
	}
	a.voiceInput.target = nil
	a.syncVoiceRecordable()
	if r == nil {
		return
	}
	// The session is reused when it can be, so the next press still starts fast.
	go func() {
		_, _ = r.Stop()
		if recorderCanReuse(r) {
			a.win.Update(func() {
				if a.closing || !a.prefs.VoiceInputEnabled {
					go r.Cancel()
					return
				}
				a.voiceInput.prepared = r
				a.syncVoiceRecordable()
				a.installVoiceHotkey()
			})
			return
		}
		go r.Cancel()
		a.win.Update(func() {
			if a.closing || !a.prefs.VoiceInputEnabled {
				return
			}
			a.voiceInput.quietWarmup = true
			a.configureVoiceInput()
		})
	}()
}

func (a *desktop) finishVoiceInput(timeout bool) {
	if a.voiceInput.aborted {
		// The chord was already abandoned; do not enqueue the silence as a take.
		a.abortVoiceInput()
		return
	}
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
	if a.prefs.VoiceInputEnabled && !backendVoiceModelUsable(a.config.VoiceModels, a.prefs.VoiceModelID) {
		// The selected model was deleted, or turned into a TTS model, while recording.
		// Stop the session and say so: sending this take nowhere would fail with an
		// unrelated validation error ("Endpoint 必须是有效的 URL"). No reconfigure here:
		// cancelling the recorder already disarms the shortcut, and configureVoiceInput
		// would add a second toast ("请先添加 ASR 模型") for the same event.
		go r.Cancel()
		a.syncVoiceRecordable()
		a.notifyError("所选语音模型已不可用，请在设置中重新选择")
		return
	}
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
			if voiceClipTooShort(wav) || !voiceClipHasSignal(wav) {
				// A mistaken tap yields too little audio; holding a modifier-only
				// shortcut for a menu may yield a full-length take of silence. Drop
				// either case instead of spending an ASR request on it.
				a.voiceInput.finishTake()
				return
			}
			take.wav = wav
			a.enqueueVoiceTake(take)
		})
	}()
}

// voiceTake is one finished recording waiting to be transcribed. seq is assigned
// when recording ends, so the queue can always publish in recording order.
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
	stop, err := startNativeVoiceHotkey(a.prefs.VoiceHotkey, a.prefs.VoiceModifierLeft, a.prefs.VoiceModifierRight, func() bool {
		return a.voiceInput.recordable.Load()
	}, func() {
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
	}, func() {
		// A bare-modifier chord also drives system shortcuts; drop the take instead
		// of transcribing silence.
		a.win.Update(func() {
			if !a.closing {
				a.abortVoiceInput()
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
