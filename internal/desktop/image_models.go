// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	backend "ally-dev/internal/app"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

type imageModelsState struct {
	config         backend.ImageGenerationConfig
	generate, edit string
	draft          backend.ImageModelConfig
	draftKeys      []string
	headers        string
	editing        int
	formOpen       bool
	advancedOpen   bool
	testOpen       bool
	busy           bool
	test           *imageModelTest
}

func cloneImages(v *backend.ImageGenerationConfig) backend.ImageGenerationConfig {
	if v == nil {
		return backend.ImageGenerationConfig{}
	}
	b, _ := json.Marshal(v)
	var out backend.ImageGenerationConfig
	_ = json.Unmarshal(b, &out)
	return out
}
func imageIdentity(m backend.ImageModelConfig) *backend.ModelIdentity {
	return &backend.ModelIdentity{ProviderName: m.ProviderName, Model: m.Model}
}
func imageIdentityKey(p, m string) string {
	return strings.ToLower(strings.TrimSpace(p)) + "\x00" + strings.ToLower(strings.TrimSpace(m))
}
func imageLabel(id *backend.ModelIdentity) string {
	if id == nil {
		return "未设置"
	}
	return modelLabel(id.ProviderName, id.Model)
}
func (s *imageModelsState) resetSelections() {
	s.generate = imageLabel(s.config.GenerateModel)
	s.edit = imageLabel(s.config.EditModel)
}
func (a *desktop) ensureImageModels() {
	if a.imageModels == nil {
		s := &imageModelsState{config: cloneImages(a.config.ImageGeneration), editing: -1}
		s.resetSelections()
		a.imageModels = s
	}
}
func (s *imageModelsState) openForm(index int) {
	s.editing = index
	s.formOpen = true
	s.advancedOpen = false
	s.headers = ""
	s.draftKeys = nil
	if index >= 0 && index < len(s.config.Models) {
		s.draft = s.config.Models[index]
		s.draft.APIKeys = append([]string(nil), s.draft.APIKeys...)
		s.draftKeys = append(s.draftKeys, s.draft.APIKeys...)
		if len(s.draftKeys) == 0 && s.draft.APIKey != "" {
			s.draftKeys = []string{s.draft.APIKey}
		}
		if len(s.draft.CustomHeaders) > 0 {
			b, _ := json.MarshalIndent(s.draft.CustomHeaders, "", "  ")
			s.headers = string(b)
		}
	} else {
		s.draft = backend.ImageModelConfig{ProviderName: "OpenAI", Protocol: "openai-images", BaseURL: "https://api.openai.com/v1"}
	}
	if len(s.draftKeys) == 0 {
		s.draftKeys = []string{""}
	}
}
func normalizeImageDraft(d backend.ImageModelConfig, keys []string, headers string) (backend.ImageModelConfig, error) {
	return normalizeImageModel(d, keys, headers, true)
}
func normalizeImageModel(d backend.ImageModelConfig, keys []string, headers string, requireKey bool) (backend.ImageModelConfig, error) {
	d.ProviderName = strings.TrimSpace(d.ProviderName)
	d.Model = strings.TrimSpace(d.Model)
	if d.Protocol == "" {
		name := strings.ToLower(d.Model)
		switch {
		case strings.HasPrefix(name, "gpt-image-") || name == "chatgpt-image-latest":
			d.Protocol = "openai-images"
		case strings.HasPrefix(name, "grok-imagine-image"):
			d.Protocol = "xai-images"
		case name == "image-01" || name == "image-01-live":
			d.Protocol = "minimax-images"
		}
	}
	d.BaseURL = strings.TrimRight(strings.TrimSpace(d.BaseURL), "/")
	u, err := url.Parse(d.BaseURL)
	if d.ProviderName == "" || d.Model == "" || err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return d, fmt.Errorf("请填写供应商、模型 ID 和有效的 API 地址")
	}
	if d.Protocol != "openai-images" && d.Protocol != "xai-images" && d.Protocol != "minimax-images" {
		return d, fmt.Errorf("请选择支持的图片协议")
	}
	d.APIKeys = nil
	seen := map[string]bool{}
	for _, k := range keys {
		k = strings.TrimSpace(k)
		if k != "" && !seen[k] {
			seen[k] = true
			d.APIKeys = append(d.APIKeys, k)
		}
	}
	if len(d.APIKeys) == 0 && requireKey {
		return d, fmt.Errorf("请至少填写一个 API Key")
	}
	d.APIKey = ""
	if len(d.APIKeys) > 0 {
		d.APIKey = d.APIKeys[0]
	}
	d.CustomHeaders = nil
	if strings.TrimSpace(headers) != "" {
		if err := json.Unmarshal([]byte(headers), &d.CustomHeaders); err != nil || d.CustomHeaders == nil {
			return d, fmt.Errorf("自定义请求头必须是字符串到字符串的 JSON 对象")
		}
	}
	return d, nil
}
func (s *imageModelsState) replace(index int, next *backend.ImageModelConfig) error {
	oldKey := ""
	if index >= 0 && index < len(s.config.Models) {
		old := s.config.Models[index]
		oldKey = imageIdentityKey(old.ProviderName, old.Model)
	}
	if next != nil {
		key := imageIdentityKey(next.ProviderName, next.Model)
		for i, m := range s.config.Models {
			if i != index && imageIdentityKey(m.ProviderName, m.Model) == key {
				return fmt.Errorf("该供应商下已有同名图片模型")
			}
		}
	}
	if index >= 0 && index < len(s.config.Models) {
		if next == nil {
			s.config.Models = append(s.config.Models[:index], s.config.Models[index+1:]...)
		} else {
			s.config.Models[index] = *next
		}
	} else if next != nil {
		s.config.Models = append(s.config.Models, *next)
	}
	for _, field := range []**backend.ModelIdentity{&s.config.GenerateModel, &s.config.EditModel} {
		id := *field
		if id != nil && oldKey != "" && imageIdentityKey(id.ProviderName, id.Model) == oldKey {
			if next == nil || (field == &s.config.EditModel && next.Protocol == "minimax-images") {
				*field = nil
			} else {
				*field = imageIdentity(*next)
			}
		}
	}
	s.resetSelections()
	return nil
}
func (s *imageModelsState) options(edit bool) []string {
	items := []string{"未设置"}
	for _, m := range s.config.Models {
		if !edit || m.Protocol != "minimax-images" {
			items = append(items, modelLabel(m.ProviderName, m.Model))
		}
	}
	return items
}
func (s *imageModelsState) chooseDefault(label string, edit bool) {
	var selected *backend.ModelIdentity
	for _, m := range s.config.Models {
		if label == modelLabel(m.ProviderName, m.Model) && (!edit || m.Protocol != "minimax-images") {
			selected = imageIdentity(m)
			break
		}
	}
	if edit {
		s.config.EditModel = selected
	} else {
		s.config.GenerateModel = selected
	}
}
func (a *desktop) applyImageConfig() {
	s := a.imageModels
	if s == nil || s.busy {
		return
	}
	s.busy = true
	value := cloneImages(&s.config)
	a.status = "正在保存图片模型配置…"
	go func() {
		err := a.core.SaveNativeImageConfig(value)
		var cfg backend.ConfigState
		if err == nil {
			cfg, err = a.core.GetConfig()
		}
		a.win.Update(func() {
			s.busy = false
			if err != nil {
				a.notify(err.Error())
				return
			}
			a.config = cfg
			s.config = cloneImages(cfg.ImageGeneration)
			s.resetSelections()
			a.notify("图片模型配置已保存")
		})
	}()
}
