// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	backend "ally-dev/internal/app"
)

const voiceCaptureMaxDuration = 20 * time.Second

type voiceModelsState struct {
	models                         []backend.VoiceModelConfig
	draft                          backend.VoiceModelConfig
	preset                         string
	presetMode                     string
	endpointEditing                bool
	fetched                        []string
	includeAllModels               bool
	fetchBusy                      bool
	fetchNote                      string
	editing                        int
	formOpen, busy, testBusy       bool
	endpointBusy, recorderOpen     bool
	endpointState, endpointPassKey string
	transcript                     string
	waveLevels                     []float32
	micLevel                       float32
	recordStartedAt                time.Time
	recording                      nativeAudioRecording
	recordState                    string
	recordTimer                    *time.Timer
}

func (a *desktop) ensureVoiceModels() {
	if a.voiceModels == nil {
		a.voiceModels = &voiceModelsState{models: cloneVoiceModelConfigs(a.config.VoiceModels), editing: -1}
	}
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
	s.endpointBusy, s.endpointState, s.endpointPassKey = false, "", ""
	s.preset, s.presetMode = "", ""
	s.endpointEditing = false
	s.fetched, s.fetchBusy, s.fetchNote = nil, false, ""
	s.includeAllModels = false
	s.recorderOpen, s.transcript = false, ""
	s.waveLevels, s.micLevel = nil, 0
	if index >= 0 && index < len(s.models) {
		s.draft = s.models[index]
		s.preset = voicePresetFor(s.draft.Endpoint)
		if preset, ok := backend.VoiceProviderPresetSpec(s.preset); ok {
			s.presetMode = preset.DefaultMode()
		}
		return
	}
	// The editor asks for nothing but the service and the model: identity, auth
	// and timeout are derived, so a new draft starts from sane defaults plus the
	// local preset, which fills the endpoint, protocol and auth mode.
	s.draft = backend.VoiceModelConfig{
		ID: nextVoiceModelID(s.models), Deployment: "local", Protocol: "openai_audio",
		FileField: "file", AuthMode: "none", AuthHeader: "Authorization", AuthPrefix: "Bearer ",
		TimeoutSeconds: 30,
	}
	s.preset = defaultVoicePreset
	if preset, ok := backend.VoiceProviderPresetSpec(s.preset); ok {
		s.presetMode = preset.DefaultMode()
		s.draft = preset.Apply(s.draft, s.presetMode)
	}
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
	s.includeAllModels = false
	s.endpointEditing = false
	s.endpointState, s.endpointPassKey = "", ""
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
	case inferVoiceDeployment(host) == "local":
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
	draft := s.draft
	if strings.TrimSpace(draft.ID) == "" {
		draft.ID = nextVoiceModelID(s.models)
	}
	draft.Deployment = inferVoiceDeployment(draft.Endpoint)
	draft.AuthMode, draft.AuthHeader, draft.AuthPrefix = voiceAuthFor(draft)
	return draft
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
	out := make([]string, 0, len(s.fetched)+1)
	seen := map[string]bool{}
	for _, id := range append(append([]string{}, s.fetched...), s.draft.Model) {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
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
	s.endpointState, s.endpointPassKey = "正在检查 Endpoint（不会上传录音）…", ""
	fingerprint := voiceEndpointFingerprint(draft)
	go func() {
		result, err := a.core.TestNativeVoiceEndpoint(draft)
		a.win.Update(func() {
			s.endpointBusy, s.endpointState = false, ""
			if err != nil {
				a.notifyError("Endpoint 检查失败：" + err.Error())
				return
			}
			// The probe result is a one-line status, not page copy: report it as a
			// transient toast and keep only the verified fingerprint in the page.
			s.endpointPassKey = fingerprint
			a.notify(result)
		})
	}()
}

// voiceEndpointHost is the lowercased host part of an endpoint, without scheme,
// port or path. Both the deployment label and the preset guess read it.
func voiceEndpointHost(endpoint string) string {
	host := strings.ToLower(strings.TrimSpace(endpoint))
	if idx := strings.Index(host, "://"); idx >= 0 {
		host = host[idx+3:]
	}
	if slash := strings.IndexAny(host, "/:?"); slash >= 0 {
		host = host[:slash]
	}
	return host
}

// inferVoiceDeployment labels a model local or api from its endpoint host, so
// the list chip stays meaningful without a form field.
func inferVoiceDeployment(endpoint string) string {
	host := voiceEndpointHost(endpoint)
	switch {
	case host == "", host == "localhost", host == "127.0.0.1", host == "::1", strings.HasSuffix(host, ".local"):
		return "local"
	case strings.HasPrefix(host, "10."), strings.HasPrefix(host, "192.168."), strings.HasPrefix(host, "127."):
		return "local"
	}
	for _, block := range []string{"172.16.", "172.17.", "172.18.", "172.19.", "172.2", "172.30.", "172.31."} {
		if strings.HasPrefix(host, block) {
			return "local"
		}
	}
	return "api"
}

func voiceModelIDExists(models []backend.VoiceModelConfig, id string) bool {
	for _, model := range models {
		if model.ID == id {
			return true
		}
	}
	return false
}

// voiceEndpointFingerprint identifies one set of connection parameters. It reads
// the normalized draft, so a change to auth derivation also invalidates the check.
func voiceEndpointFingerprint(draft backend.VoiceModelConfig) string {
	value := strings.Join([]string{draft.Endpoint, draft.AuthMode, draft.AuthHeader, draft.AuthPrefix, draft.APIKey}, "\x00")
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func (a *desktop) endpointVerified(s *voiceModelsState) bool {
	return s != nil && s.endpointPassKey != "" && s.endpointPassKey == voiceEndpointFingerprint(s.requestDraft())
}

func (a *desktop) openVoiceRecorder() {
	s := a.voiceModels
	if s == nil || s.endpointBusy || s.testBusy || !a.endpointVerified(s) {
		return
	}
	s.recorderOpen, s.transcript = true, ""
	s.waveLevels, s.micLevel = nil, 0
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
			s.recordState = "正在录音…"
			go a.watchVoiceLevels(s, recorder)
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
	if s.recordTimer != nil {
		s.recordTimer.Stop()
		s.recordTimer = nil
	}
	s.testBusy = true
	s.transcript = ""
	s.recordState = "正在处理录音并请求语音模型…"
	draft := s.requestDraft()
	go func() {
		audio, err := recorder.Stop()
		recorder.Cancel()
		var result string
		if err == nil {
			result, err = a.core.TestNativeVoiceModelAudioStream(draft, audio, func(text string) {
				a.win.Update(func() { s.transcript = text })
			})
		}
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

func (a *desktop) watchVoiceLevels(s *voiceModelsState, recorder nativeAudioRecording) {
	levels := recorder.Levels()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	var latest float32
	dirty := false
	for {
		select {
		case level, ok := <-levels:
			if !ok {
				return
			}
			latest, dirty = level, true
		case <-ticker.C:
			if !dirty {
				continue
			}
			dirty = false
			a.win.Update(func() {
				if s.recording != recorder {
					return
				}
				s.micLevel = latest
				s.waveLevels = append(s.waveLevels, latest)
				if len(s.waveLevels) > 56 {
					copy(s.waveLevels, s.waveLevels[len(s.waveLevels)-56:])
					s.waveLevels = s.waveLevels[:56]
				}
				elapsed := time.Since(s.recordStartedAt).Round(time.Second)
				s.recordState = fmt.Sprintf("录音中 · %s / 20 秒", elapsed)
			})
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
	a.confirmAction("删除语音模型", s.models[index].Name, func() error { return nil }, func() {
		before := cloneVoiceModelConfigs(s.models)
		s.models = append(s.models[:index], s.models[index+1:]...)
		a.commitVoiceModels(s.models, "语音模型已删除", false, before)
	})
}
