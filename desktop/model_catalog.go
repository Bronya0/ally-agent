// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	_ "embed"
	"encoding/json"
	"strings"
	"sync"
)

//go:embed model_catalog.json
var modelCatalogData []byte

type modelCatalog struct {
	FormatVersion int                    `json:"formatVersion"`
	Source        string                 `json:"source,omitempty"`
	RetrievedAt   string                 `json:"retrievedAt,omitempty"`
	Providers     []modelCatalogProvider `json:"providers"`
}

type modelCatalogProvider struct {
	ID        string              `json:"id"`
	Name      string              `json:"name"`
	APIFormat string              `json:"apiFormat"`
	BaseURL   string              `json:"baseUrl"`
	Doc       string              `json:"doc"`
	Models    []modelCatalogModel `json:"models"`
}

type modelCatalogModel struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	ContextWindow int    `json:"contextWindow"`
	MaxTokens     int    `json:"maxTokens"`
	ReasoningTag  string `json:"reasoningTag"`
	VisionCapable *bool  `json:"visionCapable"`
}

var (
	modelCatalogOnce      sync.Once
	modelCatalogDataValue modelCatalog
)

func availableModelCatalog() modelCatalog {
	modelCatalogOnce.Do(func() { _ = json.Unmarshal(modelCatalogData, &modelCatalogDataValue) })
	return modelCatalogDataValue
}

func catalogProviderOptions() []string {
	catalog := availableModelCatalog()
	counts := make(map[string]int, len(catalog.Providers))
	for _, p := range catalog.Providers {
		counts[p.Name]++
	}
	options := make([]string, 0, len(catalog.Providers)+1)
	for _, p := range catalog.Providers {
		label := p.Name
		if counts[p.Name] > 1 {
			label += " · " + p.ID
		}
		options = append(options, label)
	}
	return append(options, "自定义配置")
}

func catalogProviderLabel(p modelCatalogProvider) string {
	label := p.Name
	count := 0
	for _, candidate := range availableModelCatalog().Providers {
		if candidate.Name == p.Name {
			count++
		}
	}
	if count > 1 {
		label += " · " + p.ID
	}
	return label
}

func catalogModelLabel(m modelCatalogModel) string {
	if m.Name != "" && !strings.EqualFold(m.Name, m.ID) {
		return m.Name + " · " + m.ID
	}
	return m.ID
}

func findCatalogProviderByName(name string) *modelCatalogProvider {
	catalog := availableModelCatalog()
	for i := range catalog.Providers {
		provider := &catalog.Providers[i]
		if catalogProviderLabel(*provider) == name {
			return provider
		}
	}
	return nil
}

func findCatalogModel(provider *modelCatalogProvider, id string) *modelCatalogModel {
	if provider == nil {
		return nil
	}
	for i := range provider.Models {
		if provider.Models[i].ID == id || catalogModelLabel(provider.Models[i]) == id {
			return &provider.Models[i]
		}
	}
	return nil
}
