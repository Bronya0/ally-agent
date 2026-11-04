// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"fmt"
	"math"
	"strings"
	"sync/atomic"
	"time"

	"ally-dev/desktop/uikit"
	backend "ally-dev/internal/app"
)

const voiceCaptureMaxDuration = 20 * time.Second

type voiceModelsState struct {
	models          []backend.VoiceModelConfig
	draft           backend.VoiceModelConfig
	preset          string
	presetMode      string
	endpointEditing bool
	fetched         []string
	// fetchEpoch advances whenever the fetched list changes, so modelOptions can
	// cache its result without comparing slices.
	fetchEpoch                 int
	optionsMemo                []string
	optionsEpoch               int
	optionsDraft               string
	includeAllModels           bool
	fetchBusy                  bool
	fetchNote                  string
	editing                    int
	formOpen, busy, testBusy   bool
	endpointBusy, recorderOpen bool
	endpointState              string
	// endpointPassed is the request shape that last passed the check. Comparing
	// the shape directly keeps the per-frame green/red decision allocation-free
	// and makes it obvious that model and protocol belong to it.
	endpointPassed  endpointShape
	endpointChecked bool
	transcript      string
	// wave is the meter history. The capture side writes it as it reads levels
	// and the painter reads it during Draw, so an animating meter never rebuilds
	// the window just to move a bar.
	wave voiceLevelRing
	// lines caches each row's derived text (see rowLines).
	// draftMemo caches the derived request draft (see requestDraft).
	draftMemo       *backend.VoiceModelConfig
	draftMemoKey    voiceDraftKey
	lines           map[string]voiceRowLines
	recordStartedAt time.Time
	recording       nativeAudioRecording
	levelWatchStop  chan struct{}
	recordState     string
	recordTimer     *time.Timer
	// recordSeconds is the elapsed whole second of the running take. The watcher
	// stores it and the dialog formats it, so a clock tick only repaints.
	recordSeconds atomic.Int32
	// presetOptions is built once: the dialog asks for the service list every
	// frame, and the presets never change at runtime.
	presetOptions []uikit.Option
}

// voiceLevelRing keeps the last voiceWaveBars levels in a fixed array written by
// the capture pump and read by the painter. Atomics instead of a slice: the meter
// would otherwise need the view to be rebuilt 10 times a second to stay in sync.
type voiceLevelRing struct {
	levels [voiceWaveBars]atomic.Uint32
	count  atomic.Int32
}

func (r *voiceLevelRing) reset() {
	r.count.Store(0)
}

func (r *voiceLevelRing) push(level float32) {
	r.levels[int(r.count.Load())%voiceWaveBars].Store(math.Float32bits(level))
	r.count.Add(1)
}

// level returns the level index bars back from the newest sample, or 0 when the
// meter has not filled that slot yet.
func (r *voiceLevelRing) level(index int) float32 {
	total := int(r.count.Load())
	if index < 0 || index >= voiceWaveBars || index >= total {
		return 0
	}
	return math.Float32frombits(r.levels[(total-1-index)%voiceWaveBars].Load())
}

func (r *voiceLevelRing) filled() int { return min(int(r.count.Load()), voiceWaveBars) }

// voiceWaveBars is how many meter bars the dialog draws.
const voiceWaveBars = 56

func (a *desktop) ensureVoiceModels() {
	if a.voiceModels == nil {
		a.voiceModels = &voiceModelsState{models: cloneVoiceModelConfigs(a.config.VoiceModels), editing: -1}
		a.voiceModels.presetOptions = voicePresetOptions()
	}
}

// voicePresetOptions is the service picker's contents: fixed at build time, so it
// is built once instead of on every frame of the dialog.
func voicePresetOptions() []uikit.Option {
	presets := backend.VoiceProviderPresets()
	options := make([]uikit.Option, 0, len(presets))
	for _, preset := range presets {
		options = append(options, uikit.Option{Value: preset.ID, Label: preset.Label})
	}
	return options
}

func cloneVoiceModelConfigs(in []backend.VoiceModelConfig) []backend.VoiceModelConfig {
	out := make([]backend.VoiceModelConfig, len(in))
	for i, model := range in {
		out[i] = model
		out[i].Headers = map[string]string{}
		for k, v := range model.Headers {
			out[i].Headers[k] = v
		}
		out[i].ExtraFields = map[string]string{}
		for k, v := range model.ExtraFields {
			out[i].ExtraFields[k] = v
		}
	}
	return out
}

func (s *voiceModelsState) open(index int) {
	s.editing, s.formOpen = index, true
	s.endpointBusy, s.endpointState, s.endpointChecked = false, "", false
	s.preset, s.presetMode = "", ""
	s.endpointEditing = false
	s.fetched, s.fetchBusy, s.fetchNote = nil, false, ""
	s.fetchEpoch++
	s.includeAllModels = false
	s.recorderOpen, s.transcript = false, ""
	s.wave.reset()
	if index >= 0 && index < len(s.models) {
		s.draft = s.models[index]
		s.preset = voicePresetFor(s.draft.Endpoint)
		if preset, ok := backend.VoiceProviderPresetSpec(s.preset); ok {
			s.presetMode = preset.DefaultMode()
		}
		return
	}
	// The editor asks for nothing but the service and the model: identity, auth
	// and timeout are derived, so a new draft is just an id plus the backend's own
	// normalization, then the local preset fills endpoint, protocol and auth.
	s.draft = backend.NormalizeVoiceModels([]backend.VoiceModelConfig{{ID: nextVoiceModelID(s.models)}})[0]
	s.preset = defaultVoicePreset
	if preset, ok := backend.VoiceProviderPresetSpec(s.preset); ok {
		s.presetMode = preset.DefaultMode()
		s.draft = backend.NormalizeVoiceModels([]backend.VoiceModelConfig{preset.Apply(s.draft, s.presetMode)})[0]
	}
}

// voiceRowLines is one model row's derived text, cached with the fields it was
// built from. The list rebuilds every frame, so joining provider and endpoint
// there would allocate on every frame; comparing the sources costs nothing.
type voiceRowLines struct {
	provider, endpoint, model string
	providerLine, modelLine   string
}

// rowLines returns the cached lines for one model, rebuilding them only when the
// fields they were derived from changed.
func (s *voiceModelsState) rowLines(m backend.VoiceModelConfig) voiceRowLines {
	if cached, ok := s.lines[m.ID]; ok && cached.provider == m.Provider && cached.endpoint == m.Endpoint && cached.model == m.Model {
		return cached
	}
	line := voiceRowLines{provider: m.Provider, endpoint: m.Endpoint, model: m.Model}
	line.providerLine = m.Provider + " · " + m.Endpoint
	if m.Model != "" {
		line.modelLine = "模型：" + m.Model
	}
	if s.lines == nil {
		s.lines = make(map[string]voiceRowLines, len(s.models))
	}
	s.lines[m.ID] = line
	return line
}

// Every voice model starts from one of the offered presets: there is no manual
// "custom" path any more, so a new draft defaults to the local one.
const defaultVoicePreset = "local_openai_asr"

// applyVoicePreset switches the draft to another service and drops what belonged
// to the previous one: the key, the model id, the headers and the fetched list are
// all service-specific, so keeping them would silently test the wrong account.
func (s *voiceModelsState) applyVoicePreset(preset backend.VoiceProviderPreset, modeID string) {
	s.presetMode = modeID
	s.draft = preset.Apply(s.draft, modeID)
	s.draft.APIKey, s.draft.Model, s.draft.Name = "", "", ""
	s.draft.Headers, s.draft.ExtraFields = nil, nil
	s.fetched, s.fetchBusy, s.fetchNote = nil, false, ""
	s.fetchEpoch++
	s.includeAllModels = false
	s.endpointEditing = false
	s.endpointState, s.endpointChecked = "", false
}

// voicePresetFor names the preset an existing model was built from, so reopening
// it shows the service instead of an empty picker.
func voicePresetFor(endpoint string) string {
	host := voiceEndpointHost(endpoint)
	switch {
	case strings.Contains(host, "dashscope.aliyuncs.com"):
		return "dashscope"
	case strings.Contains(host, "siliconflow"):
		return "siliconflow"
	case backend.VoiceDeploymentForEndpoint(host) == "local":
		return "local_openai_asr"
	}
	return ""
}

// nextVoiceModelID picks the first free voice-N id: the editor has no config-id
// field any more, so the id has to be generated without colliding.
func nextVoiceModelID(models []backend.VoiceModelConfig) string {
	for i := 1; ; i++ {
		if id := fmt.Sprintf("voice-%d", i); !voiceModelIDExists(models, id) {
			return id
		}
	}
}

// requestDraft is the model the next request will send. The editor no longer
// exposes identity or auth fields, so both are derived here from the endpoint and
// the API key; a saved custom auth survives because a filled key keeps it.
func (s *voiceModelsState) requestDraft() backend.VoiceModelConfig {
	// The dialog asks for this (directly and through endpointVerified) on every frame,
	// and deriving the deployment + auth chrome walks strings each time. Cache it keyed
	// by the fields it is derived from; those are compared, not hashed.
	if key := voiceDraftKeyOf(s.draft); s.draftMemo != nil && s.draftMemoKey == key {
		return *s.draftMemo
	}
	draft := s.draft
	if strings.TrimSpace(draft.ID) == "" {
		draft.ID = nextVoiceModelID(s.models)
	}
	// A saved model without a display name would list as its model id and give the
	// delete confirmation an empty subject, so the name falls back once here.
	if strings.TrimSpace(draft.Name) == "" {
		draft.Name = strings.TrimSpace(draft.Model)
	}
	draft.Deployment = backend.VoiceDeploymentForEndpoint(draft.Endpoint)
	draft.AuthMode, draft.AuthHeader, draft.AuthPrefix = voiceAuthFor(draft)
	s.draftMemo, s.draftMemoKey = &draft, voiceDraftKeyOf(s.draft)
	return draft
}

// voiceDraftKey is the comparable part of a draft: every field requestDraft derives
// from or may overwrite. Missing one (Kind, FileField, TimeoutSeconds were missing) let a
// cached copy hide a field a preset had just written, because saveVoiceModels writes the
// request draft back into the editor. VoiceModelConfig carries maps, which are not
// comparable and are copied by reference anyway, so their contents stay visible.
type voiceDraftKey struct {
	id, name, protocol, endpoint, model  string
	apiKey, authMode, authHeader, prefix string
	kind, fileField                      string
	timeoutSeconds                       int
}

func voiceDraftKeyOf(m backend.VoiceModelConfig) voiceDraftKey {
	return voiceDraftKey{
		id: m.ID, name: m.Name, protocol: m.Protocol, endpoint: m.Endpoint, model: m.Model,
		apiKey: m.APIKey, authMode: m.AuthMode, authHeader: m.AuthHeader, prefix: m.AuthPrefix,
		kind: m.Kind, fileField: m.FileField, timeoutSeconds: m.TimeoutSeconds,
	}
}

// voiceAuthFor derives the auth chrome from the only credential the editor still
// asks for. An empty key means an open local service; a filled key keeps a
// configured bearer/header chrome when the protocol allows it and otherwise falls
// back to the protocol's default. A protocol with its own signed request keeps its
// mode and credentials.
func voiceAuthFor(model backend.VoiceModelConfig) (string, string, string) {
	spec, _ := backend.VoiceProtocolSpec(model.Protocol)
	header, prefix := strings.TrimSpace(model.AuthHeader), model.AuthPrefix
	if header == "" {
		header = "Authorization"
	}
	mode := strings.ToLower(strings.TrimSpace(model.AuthMode))
	if mode != "" && mode != "none" && mode != "bearer" && mode != "header" {
		return mode, header, prefix
	}
	switch {
	case strings.TrimSpace(model.APIKey) == "":
		mode = "none"
	case (mode == "bearer" || mode == "header") && spec.AllowsAuth(mode):
		// Keep the stored chrome.
	case spec.AllowsAuth("bearer"):
		mode = "bearer"
	case len(spec.AuthModes) > 0:
		mode = spec.AuthModes[0]
	}
	if mode == "bearer" && strings.TrimSpace(prefix) == "" {
		prefix = "Bearer "
	}
	return mode, header, prefix
}

// modelOptions is what the 模型名 combobox offers: the ids fetched from the
// endpoint first, then whatever is already typed so a hand-written id never
// disappears from its own dropdown.
func (s *voiceModelsState) modelOptions() []string {
	// The combobox asks every frame. Rebuilding the list there allocated a slice and
	// a map per frame; the cache key is the fetched batch plus the typed name.
	if s.optionsMemo != nil && s.optionsEpoch == s.fetchEpoch && s.optionsDraft == s.draft.Model {
		return s.optionsMemo
	}
	out := make([]string, 0, len(s.fetched)+1)
	seen := make(map[string]bool, len(s.fetched)+1)
	for _, id := range s.fetched {
		if id = strings.TrimSpace(id); id != "" && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	if id := strings.TrimSpace(s.draft.Model); id != "" && !seen[id] {
		out = append(out, id)
	}
	s.optionsMemo, s.optionsEpoch, s.optionsDraft = out, s.fetchEpoch, s.draft.Model
	return out
}

// fetchVoiceModelList asks the endpoint for its model list, so the model name
// does not have to be typed from memory.
func (a *desktop) fetchVoiceModelList() {
	s := a.voiceModels
	if s == nil || s.fetchBusy || s.busy {
		return
	}
	draft := s.requestDraft()
	s.fetchBusy, s.fetchNote = true, "拉取中…"
	go func() {
		models, err := a.core.FetchVoiceModelList(draft, s.includeAllModels)
		a.win.Update(func() {
			s.fetchBusy = false
			if err != nil {
				s.fetchNote = "拉取模型列表失败：" + err.Error()
				return
			}
			if len(models) == 0 {
				s.fetchNote = "该服务没有列出语音模型（可能只暴露对话模型）；请手动填写模型名，或勾选「全部」看完整列表。"
				return
			}
			s.fetched = models
			s.fetchEpoch++
			if s.includeAllModels {
				s.fetchNote = fmt.Sprintf("已获取全部 %d 个模型，也可以直接输入模型名。", len(models))
			} else {
				s.fetchNote = fmt.Sprintf("已获取 %d 个语音模型；需要看全部模型请勾选「全部」。", len(models))
			}
		})
	}()
}

func (a *desktop) saveVoiceModels() {
	s := a.voiceModels
	if s == nil || s.busy || s.testBusy || s.recording != nil {
		return
	}
	s.draft = s.requestDraft()
	for i, existing := range s.models {
		if i != s.editing && strings.EqualFold(strings.TrimSpace(existing.ID), strings.TrimSpace(s.draft.ID)) {
			a.notifyError("语音模型配置 ID 已存在")
			return
		}
	}
	next := cloneVoiceModelConfigs(s.models)
	if s.editing >= 0 && s.editing < len(next) {
		next[s.editing] = s.draft
	} else {
		next = append(next, s.draft)
	}
	a.commitVoiceModels(next, "语音模型已保存", true, nil)
}

func (a *desktop) testVoiceEndpoint() {
	s := a.voiceModels
	if s == nil || s.busy || s.testBusy || s.endpointBusy || s.recording != nil {
		return
	}
	draft := s.requestDraft()
	if draft.ID == "" {
		draft.ID = "voice-endpoint-check"
	}
	s.endpointBusy = true
	s.endpointState, s.endpointChecked = "正在检查 Endpoint（不会上传录音）…", false
	shape := voiceEndpointShape(s.requestDraft())
	go func() {
		result, err := a.core.TestNativeVoiceEndpoint(draft)
		a.win.Update(func() {
			s.endpointBusy, s.endpointState = false, ""
			if err != nil {
				a.notifyError("Endpoint 检查失败：" + err.Error())
				return
			}
			// The probe result is a one-line status, not page copy: report it as a
			// transient toast and keep only the verified shape in the page.
			s.endpointPassed, s.endpointChecked = shape, true
			a.notify(result)
		})
	}()
}

// voiceEndpointHost is the lowercased host part of an endpoint. The backend owns the
// rule (it also needs it when a config file omits the deployment), so this is a thin
// alias instead of a second copy.
func voiceEndpointHost(endpoint string) string { return backend.VoiceEndpointHost(endpoint) }

// voiceModelLabel is the one display-name fallback: a model may have a friendly name,
// only an id, or neither. The model page and the settings picker both use it.
func voiceModelLabel(model backend.VoiceModelConfig) string {
	for _, candidate := range []string{model.Name, model.Model, model.ID} {
		if candidate = strings.TrimSpace(candidate); candidate != "" {
			return candidate
		}
	}
	return "未命名模型"
}

func voiceModelIDExists(models []backend.VoiceModelConfig, id string) bool {
	for _, model := range models {
		if model.ID == id {
			return true
		}
	}
	return false
}

// voiceModelIDExistsInOptions is the same check against a built picker, so the
// settings page does not have to assemble an ASR slice it throws away.
func voiceModelIDExistsInOptions(options []uikit.Option, id string) bool {
	for _, option := range options {
		if option.Value == id {
			return true
		}
	}
	return false
}

// endpointShape is the part of a model that decides where a request goes: the
// route, the model that picks it, and the credential. The endpoint check is only
// valid for one shape; comparing the fields directly avoids both a hash per frame
// and a stale pass after a model change (which returned "format is empty").
type endpointShape struct {
	endpoint, protocol, model, authMode, authHeader, authPrefix, apiKey string
}

func voiceEndpointShape(draft backend.VoiceModelConfig) endpointShape {
	return endpointShape{
		endpoint: draft.Endpoint, protocol: draft.Protocol, model: draft.Model,
		authMode: draft.AuthMode, authHeader: draft.AuthHeader, authPrefix: draft.AuthPrefix,
		apiKey: draft.APIKey,
	}
}

func (a *desktop) endpointVerified(s *voiceModelsState) bool {
	return s != nil && s.endpointChecked && s.endpointPassed == voiceEndpointShape(s.requestDraft())
}

func (a *desktop) openVoiceRecorder() {
	s := a.voiceModels
	if s == nil || s.endpointBusy || s.testBusy || !a.endpointVerified(s) {
		return
	}
	s.recorderOpen, s.transcript = true, ""
	s.wave.reset()
	s.recordState = "准备就绪。按下开始后允许麦克风访问，再说一句话。"
}

func (a *desktop) testVoiceModel() {
	s := a.voiceModels
	if s == nil || s.busy || s.testBusy || s.recording != nil || !a.endpointVerified(s) {
		return
	}
	s.recordState = "正在请求麦克风权限…"
	s.testBusy = true
	go func() {
		recorder, err := startNativeAudioRecording()
		if err == nil {
			err = recorder.Start()
		}
		a.win.Update(func() {
			s.testBusy = false
			if err != nil {
				s.recordState = "录音启动失败：" + err.Error()
				return
			}
			if !s.formOpen || a.page != "models" || a.modelTab != "语音模型" {
				go recorder.Cancel()
				return
			}
			s.recording = recorder
			s.recordStartedAt = time.Now()
			s.recordSeconds.Store(0)
			s.recordState = "正在录音…"
			levelWatchStop := make(chan struct{})
			s.levelWatchStop = levelWatchStop
			go a.watchVoiceLevels(s, recorder, levelWatchStop)
			s.recordTimer = time.AfterFunc(voiceCaptureMaxDuration, func() {
				a.win.Update(func() {
					if s.recording == recorder {
						a.stopVoiceRecording()
					}
				})
			})
		})
	}()
}

func (a *desktop) stopVoiceRecording() {
	s := a.voiceModels
	if s == nil || s.recording == nil || s.testBusy {
		return
	}
	recorder := s.recording
	s.recording = nil
	stopVoiceLevelWatch(s)
	if s.recordTimer != nil {
		s.recordTimer.Stop()
		s.recordTimer = nil
	}
	s.testBusy = true
	s.transcript = ""
	s.recordState = "正在处理录音并请求语音模型…"
	s.recordSeconds.Store(0)
	draft := s.requestDraft()
	go func() {
		audio, err := recorder.Stop()
		// Cancel waits at most voiceCancelTimeout for the thread to exit, but a hung
		// device should not delay the result either: retiring it in the background keeps
		// this path bounded.
		go recorder.Cancel()
		var result string
		finished := &atomic.Bool{}
		if err == nil {
			// SSE can deliver many partials per second; publishing each one rebuilt the
			// whole window. Coalesce to the latest text at 5 Hz, which still reads as
			// live typing, and stop as soon as the final text is in: a late partial must
			// never overwrite a finished transcript.
			var pending atomic.Value
			var publish atomic.Bool
			done := make(chan struct{})
			defer close(done)
			go func() {
				ticker := time.NewTicker(200 * time.Millisecond)
				defer ticker.Stop()
				for {
					select {
					case <-done:
						return
					case <-ticker.C:
						if finished.Load() || !publish.Swap(false) {
							continue
						}
						if text, ok := pending.Load().(string); ok {
							a.win.Update(func() {
								if !finished.Load() {
									s.transcript = text
								}
							})
						}
					}
				}
			}()
			result, err = a.core.TestNativeVoiceModelAudioStream(draft, audio, func(text string) {
				pending.Store(text)
				publish.Store(true)
			})
		}
		finished.Store(true)
		a.win.Update(func() {
			s.testBusy = false
			if err != nil {
				s.recordState = "转写失败：" + err.Error()
				return
			}
			s.transcript = strings.TrimSpace(result)
			if s.transcript == "" {
				s.recordState = "请求成功，但模型没有识别出文字。"
			} else {
				s.recordState = "转写完成"
			}
		})
	}()
}

func stopVoiceLevelWatch(s *voiceModelsState) {
	if s.levelWatchStop != nil {
		close(s.levelWatchStop)
		s.levelWatchStop = nil
	}
}

// watchVoiceLevels moves the meter and the elapsed label. Levels go straight into the
// ring the painter reads and the elapsed second into an atomic, so the pump avoids
// reading mutable UI state. The explicit stop signal retires it when the take ends, even
// if a hung device never closes the level channel.
func (a *desktop) watchVoiceLevels(s *voiceModelsState, recorder nativeAudioRecording, stop <-chan struct{}) {
	levels := recorder.Levels()
	// One ticker drives both jobs: the meter wants every tick, the clock counter wants
	// every sixteenth.
	ticker := time.NewTicker(62 * time.Millisecond)
	defer ticker.Stop()
	var latest atomic.Uint32
	var dirty atomic.Bool
	ticks := 0
	for {
		select {
		case <-stop:
			return
		case level, ok := <-levels:
			if !ok {
				return
			}
			latest.Store(math.Float32bits(level))
			dirty.Store(true)
		case <-ticker.C:
			if dirty.Swap(false) {
				s.wave.push(math.Float32frombits(latest.Load()))
			}
			if ticks = (ticks + 1) % 16; ticks != 0 {
				continue
			}
			seconds := int32(time.Since(s.recordStartedAt) / time.Second)
			if s.recordSeconds.Swap(seconds) == seconds {
				continue
			}
			if a.win != nil {
				a.win.Invalidate()
			}
		}
	}
}

func (a *desktop) cancelVoiceRecording() {
	s := a.voiceModels
	if s == nil || s.recording == nil {
		return
	}
	s.recorderOpen = false
	recorder := s.recording
	s.recording = nil
	stopVoiceLevelWatch(s)
	if s.recordTimer != nil {
		s.recordTimer.Stop()
		s.recordTimer = nil
	}
	go recorder.Cancel()
}

func (a *desktop) deleteVoiceModel(index int) {
	s := a.voiceModels
	if s == nil || s.busy || index < 0 || index >= len(s.models) {
		return
	}
	label := voiceModelLabel(s.models[index])
	// Deleting has no background work to do before the UI may change, so run is empty
	// and the list is edited in after, which confirmAction posts to the main thread.
	// Editing s.models from run would race with the render loop reading it.
	a.confirmAction("删除语音模型", label, func() error { return nil }, func() {
		if s.busy || index >= len(s.models) {
			return
		}
		before := cloneVoiceModelConfigs(s.models)
		s.models = append(s.models[:index], s.models[index+1:]...)
		a.commitVoiceModels(s.models, "语音模型已删除", false, before)
	})
}
