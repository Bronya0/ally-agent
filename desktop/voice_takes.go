// SPDX-License-Identifier: GPL-3.0-only
package desktop

// The dictation pipeline behind the recorder: one ordered queue of finished takes
// and the single worker that transcribes them, so text always lands in the order
// the user spoke it.

import (
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"

	backend "ally-dev/internal/app"
)

type voiceTakeQueue struct {
	mu          sync.Mutex
	pending     []voiceTake
	outstanding int
	worker      bool
	nextSeq     uint64
}

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

// nextTakeSeq hands out the recording order. It is called on the main thread only,
// while the queue's own fields are guarded by mu because the worker reads them from
// another goroutine; nextSeq is deliberately outside that lock.
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
		q.pending = slices.Delete(q.pending, 0, 1)
		dropped = true
	}
	q.pending = append(q.pending, take)
	if len(q.pending) > 1 {
		sort.Slice(q.pending, func(i, j int) bool { return q.pending[i].seq < q.pending[j].seq })
	}
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
	q.pending = slices.Delete(q.pending, 0, 1)
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
		// The user closed the tab while the request was in flight: say so instead of
		// letting the text vanish after the "正在转录" promise.
		a.notify("目标会话已关闭，本次转写结果未插入")
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

// voiceModelsOfKind keeps the models of one category. The dictation path only
// accepts ASR models, because a TTS model has no transcription request.
func voiceModelsOfKind(models []backend.VoiceModelConfig, kind string) []backend.VoiceModelConfig {
	want := backend.VoiceKindOf(kind)
	out := make([]backend.VoiceModelConfig, 0, len(models))
	for _, model := range models {
		if backend.VoiceKindOf(model.Kind) == want {
			out = append(out, model)
		}
	}
	return out
}

// backendVoiceModelUsable reports whether this id still points at a transcription
// model: it must exist and be ASR.
func backendVoiceModelUsable(models []backend.VoiceModelConfig, id string) bool {
	model := voiceModelByID(models, id)
	return model.ID != "" && backend.VoiceKindOf(model.Kind) == backend.VoiceKindASR
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
