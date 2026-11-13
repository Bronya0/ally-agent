// SPDX-License-Identifier: GPL-3.0-only
package app

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	toolshared "ally-dev/internal/tools/shared"
)

// A short optimistic read keeps history and run state from different turns
// out of one response without holding the Agent mutex over disk IO.
type apiSessionRead struct {
	runID           string
	outcome         sessionRunOutcome
	compacting      bool
	history         *historyCacheState
	revision        uint64
	queued          int
	model, provider string
}

func (a *App) apiSessionReadState(id string) apiSessionRead {
	a.mu.Lock()
	defer a.mu.Unlock()
	history := a.historyCacheStates[id]
	var revision uint64
	if history != nil {
		revision = history.revision
	}
	_, compacting := a.compactingSessions[id]
	runID := a.activeRunForSession(id)
	queued := len(a.runInputs[runID])
	model := a.sessionModelConfigs[id]
	return apiSessionRead{runID: runID, outcome: a.sessionRunOutcomes[id], compacting: compacting, history: history, revision: revision, queued: queued, model: model.model, provider: model.providerName}
}

func (a *App) apiStableSessionSnapshot(w http.ResponseWriter, id string) (SessionSnapshot, apiSessionRead, bool) {
	for attempt := 0; attempt < 3; attempt++ {
		before := a.apiSessionReadState(id)
		snapshot, ok := a.apiLoadSession(w, id)
		if !ok {
			return SessionSnapshot{}, apiSessionRead{}, false
		}
		if before == a.apiSessionReadState(id) {
			return snapshot, before, true
		}
	}
	apiWriteError(w, http.StatusConflict, "session changed while reading; retry the request")
	return SessionSnapshot{}, apiSessionRead{}, false
}

func (a *App) apiHandleSessionContext(w http.ResponseWriter, r *http.Request) {
	snapshot, ok := a.apiLoadSession(w, r.PathValue("id"))
	if !ok {
		return
	}
	apiWriteOK(w, a.GetContextBreakdown(snapshot.ID, snapshot.Workspace))
}

func (a *App) apiHandleTokenStats(w http.ResponseWriter, r *http.Request) {
	result := a.GetTokenStats()
	if !result.OK {
		apiWriteError(w, http.StatusServiceUnavailable, result.Error)
		return
	}
	apiWriteOK(w, result)
}

func (a *App) apiHandleRefreshSkills(w http.ResponseWriter, r *http.Request) {
	state, err := a.RefreshSkillState()
	if err != nil {
		apiWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.apiWriteSkills(w, state)
}

func (a *App) apiHandleReconnectMcp(w http.ResponseWriter, r *http.Request) {
	if err := a.RestartMcpServers(); err != nil {
		apiWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	apiWriteOK(w, map[string]any{"reconnected": true})
}

func (a *App) apiHandleContextSettings(w http.ResponseWriter, r *http.Request) {
	cfg, err := a.getConfig()
	if err != nil {
		apiWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	options := make([]map[string]any, 0, len(ContextLoadingOptions()))
	for _, option := range ContextLoadingOptions() {
		options = append(options, map[string]any{"id": option.ID, "label": option.Label, "description": option.Description, "enabled": cfg.ContextEnabled(option.ID)})
	}
	apiWriteOK(w, map[string]any{"options": options, "disabledContext": cfg.DisabledContext, "disabledTools": cfg.DisabledTools, "customPrompt": cfg.CustomPrompt})
}

func (a *App) apiHandlePutContextSettings(w http.ResponseWriter, r *http.Request) {
	var body map[string]json.RawMessage
	if err := apiDecodeBody(r, &body); err != nil {
		apiWriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(body) == 0 {
		apiWriteError(w, http.StatusBadRequest, "provide at least one context setting")
		return
	}
	var disabledContext, disabledTools []string
	var prompt string
	for key, raw := range body {
		var err error
		if string(raw) == "null" {
			err = errors.New("null is not a setting value")
		} else {
			switch key {
			case "disabledContext":
				err = json.Unmarshal(raw, &disabledContext)
			case "disabledTools":
				err = json.Unmarshal(raw, &disabledTools)
			case "customPrompt":
				err = json.Unmarshal(raw, &prompt)
			default:
				err = errors.New("unknown context setting: " + key)
			}
		}
		if err != nil {
			apiWriteError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	known := map[string]bool{}
	for _, option := range ContextLoadingOptions() {
		known[option.ID] = true
	}
	for _, id := range disabledContext {
		if !known[id] {
			apiWriteError(w, http.StatusBadRequest, "unknown context source: "+id)
			return
		}
	}
	for _, name := range disabledTools {
		if !toolshared.IsBuiltinTool(toolshared.NormalizeName(name)) {
			apiWriteError(w, http.StatusBadRequest, "unknown built-in tool: "+name)
			return
		}
	}
	err := a.commitConfig(func(cfg *ConfigState) error {
		if _, ok := body["disabledContext"]; ok {
			cfg.DisabledContext = append([]string{}, disabledContext...)
		}
		if _, ok := body["disabledTools"]; ok {
			cfg.DisabledTools = toolshared.SanitizeDisabledTools(disabledTools)
		}
		if _, ok := body["customPrompt"]; ok {
			cfg.CustomPrompt = prompt
		}
		return nil
	})
	if err != nil {
		apiWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.apiHandleContextSettings(w, r)
}

func (a *App) apiHandleFastCompactSession(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if !a.apiRequireSession(w, id) {
		return
	}
	result, err := a.compactSessionWithMode(r.Context(), id, "", ConfigState{}, true)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, errSessionRunning) || errors.Is(err, errSessionCompacting) {
			status = http.StatusConflict
		}
		if errors.Is(err, errAppStopping) {
			status = http.StatusServiceUnavailable
		}
		apiWriteError(w, status, err.Error())
		return
	}
	apiWriteOK(w, result)
}
