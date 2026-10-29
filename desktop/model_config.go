// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	backend "ally-dev/internal/app"
	"encoding/json"
	"fmt"
	"strings"
)

const customProviderName = "custom"

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
	a.modelForm.AdvancedOpen = false
	a.modelForm.Fetched = nil
	a.modelForm.FetchNote = ""
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
		a.modelDraft = backend.ModelConfig{AuthType: "api_key", ProviderName: customProviderName, APIFormat: "openai", BaseURL: "https://api.deepseek.com", Model: "deepseek-v4-flash", MaxTokens: 131072, ContextWindow: 1000000}
	}
	if a.modelDraft.TokenParam == "" || a.modelDraft.TokenParam == "max_tokens" {
		a.modelDraft.TokenParam = "auto"
	}
	if a.modelDraft.ReasoningTag == "" {
		a.modelDraft.ReasoningTag = "reasoning_content"
	}
	if a.modelDraft.ReasoningEffort == "" {
		a.modelDraft.ReasoningEffort = "max"
	}
	a.modelAuthChoice = "API Key"
	if a.modelDraft.AuthType == "chatgpt_oauth" {
		a.modelAuthChoice = "ChatGPT 登录"
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
	m.MaxTokens = int(a.modelMaxTokens)
	m.ContextWindow = int(a.modelContextWindow)
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
				return m, fmt.Errorf("自定义请求头必须是字符串到字符串的 JSON 对象")
			}
		}
	}
	if m.AuthType == "chatgpt_oauth" {
		if strings.TrimSpace(m.Model) == "" || strings.TrimSpace(m.OAuthAccountID) == "" {
			return m, fmt.Errorf("请先选择已授权的 ChatGPT 账号和模型")
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
			return m, fmt.Errorf("该账号尚未授权套餐调用")
		}
		current := backend.ModelIdentity{ProviderName: "ChatGPT Official", Model: m.Model, OAuthAccountID: m.OAuthAccountID}
		if !available && (a.modelPrevious == nil || !sameModelIdentity(*a.modelPrevious, current)) {
			return m, fmt.Errorf("请刷新并选择该账号可用的模型")
		}
		m.ProviderName = "ChatGPT Official"
		m.APIFormat = "openai_responses"
		m.BaseURL = "https://api.openai.com/v1"
		m.APIKey = ""
		m.APIKeys = nil
		m.CustomHeaders = nil
		if m.ContextWindow <= 0 {
			m.ContextWindow = 512000
		}
	} else if strings.TrimSpace(m.Model) == "" || strings.TrimSpace(m.BaseURL) == "" {
		return m, fmt.Errorf("请填写模型 ID 和 API 地址")
	}
	return m, nil
}
func (a *desktop) saveModelDraft() {
	if a.modelSaving {
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
	go func() {
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
			a.config = cfg
			a.updateModelLabels(old, m)
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
			a.notifySuccess("模型已保存")
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
			a.notify("已选择 " + m.Model)
		})
	}()
}
