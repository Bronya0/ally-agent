// SPDX-License-Identifier: GPL-3.0-only
package app

import (
	"errors"
	"maps"
	"net/url"
	"slices"
	"strings"
)

// Image models have their own registry: they never become chat models.
type ImageModelConfig struct {
	ProviderName  string            `json:"providerName"`
	Model         string            `json:"model"`
	Protocol      string            `json:"protocol"`
	BaseURL       string            `json:"baseUrl"`
	APIKey        string            `json:"apiKey"`
	APIKeys       []string          `json:"apiKeys,omitempty"`
	CustomHeaders map[string]string `json:"customHeaders,omitempty"`
}

type ImageGenerationConfig struct {
	Enabled       bool               `json:"enabled"`
	Models        []ImageModelConfig `json:"models"`
	GenerateModel *ModelIdentity     `json:"generateModel"`
	EditModel     *ModelIdentity     `json:"editModel"`
}

func cloneImageConfig(source *ImageGenerationConfig) *ImageGenerationConfig {
	if source == nil {
		return nil
	}
	cfg := *source
	cfg.Models = slices.Clone(source.Models)
	for i := range cfg.Models {
		cfg.Models[i].APIKeys = slices.Clone(cfg.Models[i].APIKeys)
		cfg.Models[i].CustomHeaders = maps.Clone(cfg.Models[i].CustomHeaders)
	}
	cfg.GenerateModel = cloneConfigPointer(source.GenerateModel)
	cfg.EditModel = cloneConfigPointer(source.EditModel)
	return &cfg
}

func normalizeImageConfig(cfg *ImageGenerationConfig) {
	if cfg == nil {
		return
	}
	for i := range cfg.Models {
		m := &cfg.Models[i]
		m.ProviderName = strings.TrimSpace(m.ProviderName)
		m.Model = strings.TrimSpace(m.Model)
		m.Protocol = strings.ToLower(strings.TrimSpace(m.Protocol))
		m.BaseURL = strings.TrimRight(strings.TrimSpace(m.BaseURL), "/")
		keys := normalizeAPIKeys(m.APIKeys)
		if len(keys) == 0 && strings.TrimSpace(m.APIKey) != "" {
			keys = []string{strings.TrimSpace(m.APIKey)}
		}
		m.APIKeys, m.APIKey = keys, ""
		if len(keys) > 0 {
			m.APIKey = keys[0]
		}
		m.CustomHeaders = normalizeCustomHeaders(m.CustomHeaders)
	}
}

func imageModelForTool(cfg ConfigState, name string) (ImageModelConfig, error) {
	images := cfg.ImageGeneration
	if images == nil || !images.Enabled {
		return ImageModelConfig{}, errors.New("image generation is not enabled")
	}
	for _, disabled := range cfg.DisabledTools {
		if normalizeToolName(disabled) == name {
			return ImageModelConfig{}, errors.New("image tool is disabled")
		}
	}
	id := images.GenerateModel
	if name == "image_edit" {
		id = images.EditModel
	}
	return imageModelByIdentity(images, id, name == "image_edit")
}

// Manual tests resolve the same registry without enabling conversation tools.
func imageModelByIdentity(images *ImageGenerationConfig, id *ModelIdentity, edit bool) (ImageModelConfig, error) {
	if images != nil && id != nil {
		for _, m := range images.Models {
			if !strings.EqualFold(m.ProviderName, id.ProviderName) || !strings.EqualFold(m.Model, id.Model) {
				continue
			}
			if m.Model == "" || !validImageBaseURL(m.BaseURL) || len(resolveKeyPool(ConfigState{APIKey: m.APIKey, APIKeys: m.APIKeys})) == 0 {
				break
			}
			if m.Protocol != "openai-images" && m.Protocol != "xai-images" && m.Protocol != "minimax-images" {
				break
			}
			if edit && m.Protocol == "minimax-images" {
				return ImageModelConfig{}, errors.New("MiniMax supports generation only; configure an editing model")
			}
			return m, nil
		}
	}
	return ImageModelConfig{}, errors.New("no usable image model configured")
}

func validImageBaseURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" && u.User == nil && u.RawQuery == "" && u.Fragment == ""
}
