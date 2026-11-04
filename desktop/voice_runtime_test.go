//go:build windows

// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"sync/atomic"
	"testing"
	"time"
)

// During a rebind two hook threads can dispatch into the same hook. Idempotent
// decisions are not a substitute for mutual exclusion: the held-key map is written
// from both threads, and an unsynchronised map write is a fatal error, not a race the
// runtime can recover from.
func TestVoiceHookToleratesConcurrentDispatch(t *testing.T) {
	hook, _ := newTestVoiceHook(t, "Ctrl+Alt+K", true, true)
	const workers = 8
	const perWorker = 200
	done := make(chan struct{}, workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			for j := 0; j < perWorker; j++ {
				hook.handleEvent(wmKeyDown, 0xA2, true)
				hook.handleEvent(wmSysKeyDown, 0xA4, true)
				hook.handleEvent(wmKeyDown, 0x4B, true)
				hook.handleEvent(wmKeyUp, 0x4B, true)
				hook.handleEvent(wmSysKeyUp, 0xA4, true)
				hook.handleEvent(wmKeyUp, 0xA2, true)
			}
		}()
	}
	for i := 0; i < workers; i++ {
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Fatal("concurrent dispatch did not finish")
		}
	}
}

// After an abort the chord's modifiers are still held, so the next keystroke must not
// start another take: otherwise Alt+letter restarts the microphone per key.
func TestVoiceAbortSuppressesUntilChordReleased(t *testing.T) {
	var downs, aborts int32
	hook, _ := newTestVoiceHook(t, "Alt", true, true)
	hook.onDown = func() { atomic.AddInt32(&downs, 1) }
	hook.onAbort = func() { atomic.AddInt32(&aborts, 1) }

	hook.handleEvent(wmSysKeyDown, 0xA4, true) // start
	hook.handleEvent(wmKeyDown, 'A', true)     // abort
	hook.handleEvent(wmKeyUp, 'A', true)
	hook.handleEvent(wmKeyDown, 'B', true) // still holding Alt: must stay suppressed
	hook.handleEvent(wmKeyUp, 'B', true)
	if got := atomic.LoadInt32(&downs); got != 1 {
		t.Fatalf("recording started %d times while Alt stayed held, want 1", got)
	}
	if got := atomic.LoadInt32(&aborts); got != 1 {
		t.Fatalf("aborts = %d, want 1", got)
	}
	// Releasing Alt clears the suppression, so the next press works again.
	hook.handleEvent(wmSysKeyUp, 0xA4, true)
	hook.handleEvent(wmSysKeyDown, 0xA4, true)
	if got := atomic.LoadInt32(&downs); got != 2 {
		t.Fatalf("recording started %d times after release, want 2", got)
	}
}

func TestVoiceCaptureSlotRequiresOwnerToRelease(t *testing.T) {
	first := &windowsAudioRecording{}
	second := &windowsAudioRecording{}
	if !acquireVoiceCaptureSlot(first, time.Second) {
		t.Fatal("failed to acquire empty capture slot")
	}
	defer releaseVoiceCaptureSlot(first)
	if acquireVoiceCaptureSlot(second, 10*time.Millisecond) {
		t.Fatal("second recorder acquired an occupied capture slot")
	}
	releaseVoiceCaptureSlot(second)
	if acquireVoiceCaptureSlot(second, 10*time.Millisecond) {
		t.Fatal("non-owner released the first recorder's slot")
	}
	releaseVoiceCaptureSlot(first)
	if !acquireVoiceCaptureSlot(second, time.Second) {
		t.Fatal("slot was not released by its owner")
	}
	releaseVoiceCaptureSlot(second)
}

// A stalled recorder must not stall the queue forever, and the session must be retired
// so a late result cannot be handed to the next take as if it were new.
func TestVoiceRecorderStopTimesOutAndRetiresSession(t *testing.T) {
	// A session with no capture loop behind it: exactly the "device hung" case.
	recorder := &windowsAudioRecording{
		stop:          make(chan struct{}, 1),
		cancel:        make(chan struct{}),
		done:          make(chan struct{}),
		result:        make(chan captureResult, 1),
		levels:        make(chan float32, 1),
		stopTimeout:   50 * time.Millisecond,
		cancelTimeout: 50 * time.Millisecond,
	}
	recorder.setReusable(true)
	defer close(recorder.done)

	start := time.Now()
	if _, err := recorder.Stop(); err == nil {
		t.Fatal("a stalled recorder must fail instead of hanging")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("Stop waited %s, want the shortened budget", elapsed)
	}
	if recorder.CanReuse() {
		t.Fatal("a timed-out session must not be reused: a stale take would be returned")
	}
	if recorder.stopTimeout != 50*time.Millisecond {
		t.Fatalf("test timeout was not scoped to this recorder: %s", recorder.stopTimeout)
	}
	// A second Stop returns at once rather than waiting on a request already spent.
	done := make(chan struct{})
	go func() { _, _ = recorder.Stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("the second Stop blocked")
	}
}
