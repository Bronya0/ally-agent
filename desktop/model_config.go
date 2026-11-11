// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	backend "ally-dev/internal/app"
	"ally-dev/locales"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

const customProviderName = "custom"

// reasoningLevels is the one list of thinking levels: the composer selector, the
// model form and the chess page offer the same words, so a level reads the same
// wherever it is set. "auto" leaves the choice to the provider, "off" turns
// thinking off, and the backend clamps what a model cannot do.
var reasoningLevels = []string{"auto", "off", "low", "medium", "high", "xhigh", "max"}

func modelNumberInputText(value int, presets []modelNumberPreset) string {
	for _, preset := range presets {
		if value == preset.value {
			return preset.label
		}
	}
	return strconv.Itoa(value)
}

func parseModelNumberInput(text string, max int, label string) (int, error) {
	text = strings.ToUpper(strings.TrimSpace(text))
	if text == "" {
		return 0, nil
	}
	multiplier := 1
	switch {
	case strings.HasSuffix(text, "K"):
		multiplier = 1_000
		text = strings.TrimSpace(strings.TrimSuffix(text, "K"))
	case strings.HasSuffix(text, "M"):
		multiplier = 1_000_000
		text = strings.TrimSpace(strings.TrimSuffix(text, "M"))
	}
	value, err := strconv.Atoi(text)
	if err != nil || value < 0 {
		return 0, fmt.Errorf(locales.Text("%s 必须是非负整数，可使用 K 或 M 后缀"), label)
	}
	if value > max/multiplier {
		return 0, fmt.Errorf(locales.Text("%s 不能超过 %d"), label, max)
	}
	return value * multiplier, nil
}

func modelIdentity(m backend.ModelConfig) backend.ModelIdentity {
	return backend.ModelIdentity{ProviderName: m.ProviderName, Model: m.Model, OAuthAccountID: m.OAuthAccountID}
}
func modelIdentityPtr(m backend.ModelConfig) *backend.ModelIdentity {
	id := modelIdentity(m)
	return &id
}
func sameModelIdentity(a, b backend.ModelIdentity) bool {
	return strings.EqualFold(strings.TrimSpace(a.ProviderName), strings.TrimSpace(b.ProviderName)) && strings.EqualFold(strings.TrimSpace(a.Model), strings.TrimSpace(b.Model)) && a.OAuthAccountID == b.OAuthAccountID
}

// savedReasoning is the level the saved config holds for one model identity,
// "auto" when the entry has none. The first matching entry wins, exactly as in
// modelReasoning and withModelReasoning, so duplicate entries cannot make them
// disagree about whether a level changed.
func savedReasoning(cfg backend.ConfigState, id backend.ModelIdentity) string {
	for _, saved := range cfg.Models {
		if !sameModelIdentity(modelIdentity(saved), id) {
			continue
		}
		if saved.ReasoningEffort == "" {
			return "auto"
		}
		return saved.ReasoningEffort
	}
	return "auto"
}
func modelOption(m backend.ModelConfig) string {
	label := modelLabel(m.ProviderName, m.Model)
	if m.OAuthAccountID != "" {
		label += " · " + m.OAuthAccountID
	}
	return label
}
func (a *desktop) editModel(index int) {
	if a.modelSaving {
		return
	}
	a.modelEdit = index
	a.modelForm.KeyEpoch++
	a.invalidateModelFetch()
	a.modelForm.AdvancedOpen = false
	a.modelPrevious = nil
	a.modelProviderPreset = customProviderPreset
	a.modelProviderModel = ""
	if index >= 0 && index < len(a.config.Models) {
		a.modelDraft = a.config.Models[index]
		a.modelPrevious = modelIdentityPtr(a.modelDraft)
		if a.modelDraft.AuthType == "" {
			a.modelDraft.AuthType = "api_key"
		}
	} else {
		a.modelDraft = backend.ModelConfig{AuthType: "api_key", ProviderName: customProviderName, APIFormat: backend.APIFormatOpenAIChat, BaseURL: "https://api.deepseek.com", Model: "deepseek-v4-flash", MaxTokens: 131072, ContextWindow: 1000000}
	}
	if a.modelDraft.TokenParam == "" {
		a.modelDraft.TokenParam = "auto"
	}
	if a.modelDraft.ReasoningTag == "" {
		a.modelDraft.ReasoningTag = "reasoning_content"
	}
	// Only a fresh draft is empty here: saved entries went through the backend's
	// normalizeModelEntry, which rewrites every spelling of "none" to "auto".
	if a.modelDraft.ReasoningEffort == "" {
		a.modelDraft.ReasoningEffort = "max"
	}
	a.modelAuthChoice = "API Key"
	if a.modelDraft.AuthType == "chatgpt_oauth" {
		a.modelAuthChoice = locales.Text("ChatGPT 登录")
	}
	a.modelKeys = append([]string(nil), a.modelDraft.APIKeys...)
	if len(a.modelKeys) == 0 {
		a.modelKeys = []string{a.modelDraft.APIKey}
	}
	a.modelHeaders = ""
	if len(a.modelDraft.CustomHeaders) > 0 {
		raw, _ := json.MarshalIndent(a.modelDraft.CustomHeaders, "", "  ")
		a.modelHeaders = string(raw)
	}
	a.modelMaxTokens = float64(a.modelDraft.MaxTokens)
	a.modelContextWindow = float64(a.modelDraft.ContextWindow)
	a.modelForm.MaxTokensInput = modelNumberInputText(a.modelDraft.MaxTokens, modelMaxTokenPresets)
	a.modelForm.ContextInput = modelNumberInputText(a.modelDraft.ContextWindow, modelContextWindowPresets)
	a.resetOAuth()
	if a.modelDraft.AuthType == "chatgpt_oauth" {
		a.loadOAuthAccounts()
	}
}
func (a *desktop) updateModelLabels(before *backend.ModelIdentity, after backend.ModelConfig) {
	if before == nil {
		return
	}
	old := backend.ModelConfig{ProviderName: before.ProviderName, Model: before.Model, OAuthAccountID: before.OAuthAccountID}
	oldLabel := modelOption(old)
	newLabel := modelOption(after)
	for _, t := range a.tabs {
		if t.Model == oldLabel {
			t.Model = newLabel
		}
	}
	for _, t := range a.openSessions {
		if t.Model == oldLabel {
			t.Model = newLabel
		}
	}
}
func (a *desktop) preparedModelDraft() (backend.ModelConfig, error) {
	m := a.modelDraft
	maxTokens, err := parseModelNumberInput(a.modelForm.MaxTokensInput, 2_000_000, "Max Tokens")
	if err != nil {
		return m, err
	}
	contextWindow, err := parseModelNumberInput(a.modelForm.ContextInput, 10_000_000, locales.Text("上下文窗口"))
	if err != nil {
		return m, err
	}
	m.MaxTokens = maxTokens
	m.ContextWindow = contextWindow
	if m.AuthType != "chatgpt_oauth" {
		m.APIKeys = nil
		seen := map[string]bool{}
		for _, key := range a.modelKeys {
			key = strings.TrimSpace(key)
			if key != "" && !seen[key] {
				seen[key] = true
				m.APIKeys = append(m.APIKeys, key)
			}
		}
		m.APIKey = ""
		if len(m.APIKeys) > 0 {
			m.APIKey = m.APIKeys[0]
		}
		m.CustomHeaders = nil
		if strings.TrimSpace(a.modelHeaders) != "" {
			if err := json.Unmarshal([]byte(a.modelHeaders), &m.CustomHeaders); err != nil || m.CustomHeaders == nil {
				return m, fmt.Errorf("%s", locales.Text("自定义请求头必须是字符串到字符串的 JSON 对象"))
			}
		}
	}
	if m.AuthType == "chatgpt_oauth" {
		if strings.TrimSpace(m.Model) == "" || strings.TrimSpace(m.OAuthAccountID) == "" {
			return m, fmt.Errorf("%s", locales.Text("请先选择已授权的 ChatGPT 账号和模型"))
		}
		authorized := false
		available := false
		if a.oauth != nil {
			for _, account := range a.oauth.accounts {
				if account.ID == m.OAuthAccountID && account.SignedIn && account.PlanEnabled {
					authorized = true
					break
				}
			}
			for _, candidate := range a.oauth.models {
				if candidate.Slug == m.Model {
					available = true
					break
				}
			}
		}
		if !authorized {
			return m, fmt.Errorf("%s", locales.Text("该账号尚未授权套餐调用"))
		}
		current := backend.ModelIdentity{ProviderName: "ChatGPT Official", Model: m.Model, OAuthAccountID: m.OAuthAccountID}
		if !available && (a.modelPrevious == nil || !sameModelIdentity(*a.modelPrevious, current)) {
			return m, fmt.Errorf("%s", locales.Text("请刷新并选择该账号可用的模型"))
		}
		m.ProviderName = "ChatGPT Official"
		m.APIFormat = backend.APIFormatOpenAIResponses
		m.BaseURL = "https://api.openai.com/v1"
		m.APIKey = ""
		m.APIKeys = nil
		m.CustomHeaders = nil
		if m.ContextWindow <= 0 {
			m.ContextWindow = 512000
		}
	} else if strings.TrimSpace(m.Model) == "" || strings.TrimSpace(m.BaseURL) == "" {
		return m, fmt.Errorf("%s", locales.Text("请填写模型 ID 和 API 地址"))
	}
	return m, nil
}
func (a *desktop) saveModelDraft() {
	if a.modelSaving || a.modelForm.TestBusy {
		return
	}
	m, err := a.preparedModelDraft()
	if err != nil {
		a.notifyError(err.Error())
		return
	}

	previous := a.modelPrevious
	var old *backend.ModelIdentity
	if previous != nil {
		v := *previous
		old = &v
	}
	first := a.config.LastUsedModel == nil
	target := a.current()
	a.modelSaving = true
	prevWrite, done := a.configWriteSlot()
	go func() {
		defer close(done)
		waitConfigWrite(prevWrite)
		err := a.core.SaveNativeModel(m, old)
		var cfg backend.ConfigState
		if err == nil {
			cfg, err = a.core.GetConfig()
		}
		a.win.Update(func() {
			a.modelSaving = false
			if err != nil {
				a.notifyError(err.Error())
				return
			}
			prevLevel := ""
			if old != nil {
				prevLevel = savedReasoning(a.config, *old)
			}
			a.config = cfg
			level := savedReasoning(cfg, modelIdentity(m))
			a.updateModelLabels(old, m)
			// A Tab's own value wins at request time, so a stored level that changed has
			// to reach the Tabs already on this model, or the info bar would show one
			// level while the next restart reads another. Only a change does it: an
			// unrelated edit such as the API key leaves every Tab's pick alone. It runs
			// after the rename, so Tabs under the old label are matched, and it covers
			// the sessions of other workspaces the rename reached as well.
			if old != nil && level != prevLevel {
				label := modelOption(m)
				for _, tab := range a.tabs {
					if tab != nil && tab.Model == label {
						tab.Reasoning = level
					}
				}
				for _, tab := range a.openSessions {
					if tab != nil && tab.Model == label {
						tab.Reasoning = level
					}
				}
			}
			a.modelPrevious = modelIdentityPtr(m)
			a.modelDraft = m
			a.modelEdit = -1
			for i, v := range cfg.Models {
				if sameModelIdentity(modelIdentity(v), modelIdentity(m)) {
					a.modelEdit = i
					break
				}
			}
			if first {
				if t := target; t != nil && t.Model == "" {
					t.Model = modelOption(m)
				}
			}
			a.modelForm.Open = false
			a.cancelOAuthWhenLeaving()
			a.notifySuccess(locales.Text("模型已保存"))
		})
	}()
}
func (a *desktop) useModel(m backend.ModelConfig) {
	if a.modelSaving {
		return
	}
	id := modelIdentity(m)
	target := a.current()
	a.modelSaving = true
	go func() {
		err := a.core.SelectNativeModel(id)
		var cfg backend.ConfigState
		if err == nil {
			cfg, err = a.core.GetConfig()
		}
		a.win.Update(func() {
			a.modelSaving = false
			if err != nil {
				a.notifyError(err.Error())
				return
			}
			a.config = cfg
			if t := target; t != nil {
				t.Model = modelOption(m)
				t.Reasoning = m.ReasoningEffort
				if t.Reasoning == "" {
					t.Reasoning = "auto"
				}
			}
			a.notify(locales.Text("已选择 ") + m.Model)
		})
	}()
}
