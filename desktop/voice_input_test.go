// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"testing"
)

type voiceRecorderReuseStub struct{ reusable bool }

func (voiceRecorderReuseStub) Start() error           { return nil }
func (voiceRecorderReuseStub) Stop() ([]byte, error)  { return nil, nil }
func (voiceRecorderReuseStub) Cancel()                {}
func (voiceRecorderReuseStub) Levels() <-chan float32 { return nil }
func (r voiceRecorderReuseStub) CanReuse() bool       { return r.reusable }

func TestVoiceCaptureDeviceAvailable(t *testing.T) {
	devices := []voiceCaptureDevice{{ID: "mic-a", Name: "Internal microphone"}}
	if !voiceCaptureDeviceAvailable(devices, "mic-a") {
		t.Fatal("listed active microphone should be available")
	}
	if voiceCaptureDeviceAvailable(devices, "mic-b") {
		t.Fatal("missing microphone should not be available")
	}
}

func TestRecorderCanReuse(t *testing.T) {
	if !recorderCanReuse(voiceRecorderReuseStub{reusable: true}) {
		t.Fatal("healthy recorder should be reused")
	}
	if recorderCanReuse(voiceRecorderReuseStub{}) {
		t.Fatal("recorder with invalid session must be recreated")
	}
}

func TestAppendVoiceTranscript(t *testing.T) {
	for _, test := range []struct{ draft, transcript, want string }{
		{"已有内容", " 新转写 ", "已有内容 新转写"},
		{"已有内容 ", "新转写", "已有内容 新转写"},
		{"", "新转写", "新转写"},
		{"已有内容", "  ", "已有内容"},
	} {
		if got := appendVoiceTranscript(test.draft, test.transcript); got != test.want {
			t.Errorf("appendVoiceTranscript(%q,%q) = %q, want %q", test.draft, test.transcript, got, test.want)
		}
	}
}

func TestParseVoiceHotkey(t *testing.T) {
	for _, test := range []struct{ value, want string }{
		{"Alt", "Alt"},
		{"RightAlt", "Alt"},
		{"LeftAlt+J+K", "Alt+J+K"},
		{"Ctrl+Alt+R", "Ctrl+Alt+R"},
		{"Shift+Space", "Shift+Space"},
		{"Ctrl+Shift+F9", "Ctrl+Shift+F9"},
		{"K+Ctrl", "Ctrl+K"},
		{"Control+Win+K", "Ctrl+Super+K"},
		{"Tab", "Tab"},
		{"F9", "F9"},
		{"RightCtrl+Semicolon", "Ctrl+Semicolon"},
		{"Ctrl+Up", "Ctrl+Up"},
		{"LeftCtrl+RightCtrl+K", "Ctrl+K"},
	} {
		hotkey, ok := parseVoiceHotkey(test.value)
		if !ok {
			t.Errorf("parseVoiceHotkey(%q) rejected a valid chord", test.value)
			continue
		}
		if got := hotkey.String(); got != test.want {
			t.Errorf("parseVoiceHotkey(%q).String() = %q, want %q", test.value, got, test.want)
		}
	}
	// Plain-key chords would leak their first key into the focused control, and
	// pinned sides must not contradict each other.
	for _, value := range []string{"", "Alt+", "Ctrl+NoSuchKey", "G+H", "Ctrl+K+K", "Ctrl+K+J+L+M", "++"} {
		if _, ok := parseVoiceHotkey(value); ok {
			t.Errorf("parseVoiceHotkey(%q) unexpectedly succeeded", value)
		}
	}
}

func TestVoiceTakeQueueKeepsRecordingOrder(t *testing.T) {
	var q voiceTakeQueue
	if _, start := q.queueTake(voiceTake{seq: 2}); !start {
		t.Fatal("the first take must start the worker")
	}
	q.queueTake(voiceTake{seq: 3})
	q.queueTake(voiceTake{seq: 1}) // finishes Stop() last but was recorded first
	for want := uint64(1); want <= 3; want++ {
		take, ok := q.next()
		if !ok || take.seq != want {
			t.Fatalf("next take = %d (ok=%v), want %d", take.seq, ok, want)
		}
	}
	if _, ok := q.next(); ok {
		t.Fatal("an empty queue must not hand out a take")
	}
	if q.worker {
		t.Fatal("draining the queue must clear the worker flag")
	}
}

func TestVoiceTakeQueueDropsOldestWhenFull(t *testing.T) {
	var q voiceTakeQueue
	for seq := uint64(1); seq <= voiceTakeQueueLimit; seq++ {
		if dropped, _ := q.queueTake(voiceTake{seq: seq}); dropped {
			t.Fatalf("take %d was dropped from a queue below the limit", seq)
		}
	}
	dropped, start := q.queueTake(voiceTake{seq: 99})
	if !dropped {
		t.Fatal("a full queue must drop the oldest waiting take")
	}
	if start {
		t.Fatal("the worker is already running, it must not be started twice")
	}
	if take, _ := q.next(); take.seq != 2 {
		t.Fatalf("oldest waiting take (1) should have been dropped, got %d", take.seq)
	}
}

func TestVoiceTakeOutstandingCount(t *testing.T) {
	v := &voiceInputRuntime{}
	v.beginTake()
	v.beginTake()
	if got := v.outstandingTakes(); got != 2 {
		t.Fatalf("outstanding = %d, want 2", got)
	}
	v.finishTake()
	if got := v.outstandingTakes(); got != 1 {
		t.Fatalf("outstanding = %d, want 1", got)
	}
	v.finishTake()
	v.finishTake()
	if got := v.outstandingTakes(); got != 0 {
		t.Fatalf("outstanding = %d, want 0", got)
	}
}

func TestInferVoiceDeployment(t *testing.T) {
	for _, test := range []struct{ endpoint, want string }{
		{"", "local"},
		{"http://127.0.0.1:8786/v1/audio/transcriptions", "local"},
		{"http://localhost:9000/v1/audio/transcriptions", "local"},
		{"http://10.111.10.22:8786/v1/audio/transcriptions", "local"},
		{"http://192.168.1.9:8786/v1/audio/transcriptions", "local"},
		{"http://172.20.0.4:8786/v1/audio/transcriptions", "local"},
		{"https://api.siliconflow.cn/v1/audio/transcriptions", "api"},
		{"https://dashscope.aliyuncs.com/compatible-mode/v1/chat/completions", "api"},
		{"https://asr.example.com:8443/v1", "api"},
	} {
		if got := inferVoiceDeployment(test.endpoint); got != test.want {
			t.Errorf("inferVoiceDeployment(%q) = %q, want %q", test.endpoint, got, test.want)
		}
	}
}

func TestVoiceModifierSidePreference(t *testing.T) {
	d := &desktop{prefs: uiPrefs{VoiceModifierLeft: true, VoiceModifierRight: true}}
	d.setVoiceModifierSide(false, false)
	if !d.prefs.VoiceModifierLeft || !d.prefs.VoiceModifierRight {
		t.Fatal("both sides must never be switched off")
	}
	d.setVoiceModifierSide(true, false)
	if !d.prefs.VoiceModifierLeft || d.prefs.VoiceModifierRight {
		t.Fatalf("left-only preference = %v/%v", d.prefs.VoiceModifierLeft, d.prefs.VoiceModifierRight)
	}
}

func TestBuildVoiceHotkey(t *testing.T) {
	if value, ok := buildVoiceHotkey([]string{"RightCtrl", "Shift"}, []string{"K", "J"}); !ok || value != "Ctrl+Shift+K+J" {
		t.Fatalf("buildVoiceHotkey = (%q,%v), want Ctrl+Shift+K+J", value, ok)
	}
	if _, ok := buildVoiceHotkey(nil, []string{"G", "H"}); ok {
		t.Fatal("plain-key chord must be rejected without a modifier")
	}
}
