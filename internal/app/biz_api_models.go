// SPDX-License-Identifier: GPL-3.0-only
package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// modelIdentityToken is an opaque precondition for index-based mutations. It
// covers the whole entry except credentials, so any edit made elsewhere — not
// just a rename — invalidates a token taken before it.
func modelIdentityToken(model ModelConfig) string {
	model = normalizeModelEntry(model)
	model.APIKey, model.APIKeys = "", nil
	raw, err := json.Marshal(model)
	if err != nil {
		raw = []byte(fmt.Sprintf("%v", model))
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func checkModelIdentity(r *http.Request, model ModelConfig) error {
	supplied := strings.TrimSpace(r.Header.Get("X-Model-Identity"))
	if supplied == "" {
		return fmt.Errorf("X-Model-Identity is required; use the identity from GET /models")
	}
	if supplied != modelIdentityToken(model) {
		return fmt.Errorf("model identity changed; refresh the model list")
	}
	return nil
}

// apiModelSummary 是模型条目的脱敏视图：绝不回传 apiKey / apiKeys 明文。
func apiModelSummary(index int, model ModelConfig) map[string]any {
	model = normalizeModelEntry(model)
	return map[string]any{
		"index":           index,
		"identity":        modelIdentityToken(model),
		"authType":        model.AuthType,
		"oauthAccountId":  model.OAuthAccountID,
		"displayName":     model.DisplayName,
		"visionCapable":   model.VisionCapable,
		"providerName":    model.ProviderName,
		"apiFormat":       model.APIFormat,
		"baseUrl":         model.BaseURL,
		"model":           model.Model,
		"maxTokens":       model.MaxTokens,
		"contextWindow":   model.ContextWindow,
		"reasoningTag":    model.ReasoningTag,
		"reasoningEffort": model.ReasoningEffort,
		"tokenParam":      model.TokenParam,
		// 自定义头可能携带网关凭据：只回传键名，永不回传值。
		"customHeaderNames": customHeaderNames(model.CustomHeaders),
		"hasApiKey":         model.APIKey != "" || len(model.APIKeys) > 0,
		"apiKeyCount":       len(resolveKeyPool(ConfigState{APIKey: model.APIKey, APIKeys: model.APIKeys})),
	}
}

func (a *App) apiHandleListModels(w http.ResponseWriter, r *http.Request) {
	cfg, err := a.getConfig()
	if err != nil {
		apiWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	models := make([]map[string]any, 0, len(cfg.Models))
	for index, model := range cfg.Models {
		models = append(models, apiModelSummary(index, model))
	}
	// active 是"下一回合将使用的模型"：API 会话没有 Tab 模型 overlay，就用配置里
	// 的最近使用模型展开出来的结果（界面上的每个 Tab 各自有自己的模型，与它无关）。
	activeIndex := -1
	if cfg.LastUsedModel != nil {
		activeIndex = modelIndexByIdentity(cfg.Models, *cfg.LastUsedModel)
	}
	var activeModel ModelConfig
	if activeIndex >= 0 {
		activeModel = cfg.Models[activeIndex]
	}
	apiWriteOK(w, map[string]any{
		"active": apiModelSummary(activeIndex, activeModel),
		"models": models,
		"count":  len(models),
	})
}

// Updates are field patches: omitted fields, including redacted credentials, survive.
func (a *App) apiHandleSaveModel(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Index *int                       `json:"index"`
		Model map[string]json.RawMessage `json:"model"`
	}
	if err := apiDecodeBody(r, &body); err != nil {
		apiWriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	if body.Model == nil {
		apiWriteError(w, http.StatusBadRequest, "model object is required")
		return
	}
	index := -1
	status := http.StatusInternalServerError
	err := a.commitConfig(func(cfg *ConfigState) error {
		var base ModelConfig
		inUse := false
		if body.Index != nil {
			index = *body.Index
			if index < 0 || index >= len(cfg.Models) {
				status = http.StatusBadRequest
				return fmt.Errorf("model index out of range: %d", index)
			}
			if err := checkModelIdentity(r, cfg.Models[index]); err != nil {
				status = http.StatusConflict
				return err
			}
			base = cfg.Models[index]
			inUse = cfg.LastUsedModel != nil && modelIndexByIdentity(cfg.Models, *cfg.LastUsedModel) == index
		}
		model, err := apiMergeModel(base, body.Model)
		if err != nil {
			status = http.StatusBadRequest
			return err
		}
		id := identityOfModel(model)
		for i, existing := range cfg.Models {
			existingID := identityOfModel(existing)
			if i != index && strings.EqualFold(existingID.ProviderName, id.ProviderName) &&
				strings.EqualFold(existingID.Model, id.Model) && existingID.OAuthAccountID == id.OAuthAccountID {
				status = http.StatusConflict
				return fmt.Errorf("model identity already exists")
			}
		}
		if body.Index != nil {
			cfg.Models[index] = model
		} else {
			index = len(cfg.Models)
			cfg.Models = append(cfg.Models, model)
		}
		if inUse {
			cfg.LastUsedModel = &id
		}
		return nil
	})
	if err != nil {
		apiWriteError(w, status, err.Error())
		return
	}
	apiWriteOK(w, map[string]any{"index": index})
}

// GET /models adds these read-only keys, so a client editing a listed entry
// sends them back. They stay readable and are ignored by the decoder.
var apiModelReadOnlyFields = []string{"index", "identity", "customHeaderNames", "hasApiKey", "apiKeyCount"}

// Merge JSON fields instead of maintaining a second ModelConfig field table.
// Decode into a fresh value so explicit null/empty collections can clear fields.
func apiMergeModel(base ModelConfig, patch map[string]json.RawMessage) (ModelConfig, error) {
	if err := apiValidateFieldNames(patch, ModelConfig{}, apiModelReadOnlyFields...); err != nil {
		return ModelConfig{}, err
	}
	data, err := json.Marshal(base)
	if err != nil {
		return ModelConfig{}, fmt.Errorf("invalid model")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return ModelConfig{}, err
	}
	for name, value := range patch {
		fields[name] = value
	}
	// Either credential representation replaces the old one. A supplied pool wins
	// when both are present, matching the shared key normalization rule.
	if _, ok := patch["apiKeys"]; ok {
		delete(fields, "apiKey")
	} else if _, ok := patch["apiKey"]; ok {
		delete(fields, "apiKeys")
	}
	data, err = json.Marshal(fields)
	if err != nil {
		return ModelConfig{}, fmt.Errorf("invalid model")
	}
	var model ModelConfig
	if err := json.Unmarshal(data, &model); err != nil {
		// Do not echo submitted values: they can contain credentials.
		return ModelConfig{}, fmt.Errorf("invalid model fields")
	}
	model.Model = strings.TrimSpace(model.Model)
	model.ProviderName = strings.TrimSpace(model.ProviderName)
	model.BaseURL = strings.TrimSpace(model.BaseURL)
	if model.Model == "" {
		return ModelConfig{}, fmt.Errorf("model is required")
	}
	// Avoid passing an all-blank, nonempty pool to the legacy synchronizer.
	model.APIKeys = normalizeAPIKeys(model.APIKeys)
	return normalizeModelEntry(model), nil
}

// Delete by the same list index accepted by model selection.
func (a *App) apiHandleDeleteModel(w http.ResponseWriter, r *http.Request) {
	index, err := strconv.Atoi(r.PathValue("index"))
	if err != nil || index < 0 {
		apiWriteError(w, http.StatusBadRequest, "invalid model index")
		return
	}
	status := http.StatusInternalServerError
	err = a.commitConfig(func(cfg *ConfigState) error {
		if index >= len(cfg.Models) {
			status = http.StatusBadRequest
			return fmt.Errorf("model index out of range: %d", index)
		}
		if err := checkModelIdentity(r, cfg.Models[index]); err != nil {
			status = http.StatusConflict
			return err
		}
		inUse := cfg.LastUsedModel != nil && modelIndexByIdentity(cfg.Models, *cfg.LastUsedModel) == index
		cfg.Models = append(cfg.Models[:index], cfg.Models[index+1:]...)
		if inUse {
			cfg.LastUsedModel = nil
		}
		return nil
	})
	if err != nil {
		apiWriteError(w, status, err.Error())
		return
	}
	apiWriteOK(w, map[string]any{"deleted": index})
}

func (a *App) apiHandleActivateModel(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Index *int `json:"index"`
	}
	if err := apiDecodeBody(r, &body); err != nil {
		apiWriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	if body.Index == nil {
		apiWriteError(w, http.StatusBadRequest, "index is required")
		return
	}
	status := http.StatusBadRequest
	if err := a.commitConfig(func(cfg *ConfigState) error {
		if *body.Index < 0 || *body.Index >= len(cfg.Models) {
			return fmt.Errorf("model index out of range: %d", *body.Index)
		}
		if err := checkModelIdentity(r, cfg.Models[*body.Index]); err != nil {
			status = http.StatusConflict
			return err
		}
		id := identityOfModel(cfg.Models[*body.Index])
		cfg.LastUsedModel = &id
		return nil
	}); err != nil {
		apiWriteError(w, status, err.Error())
		return
	}
	apiWriteOK(w, map[string]any{"activated": *body.Index})
}
