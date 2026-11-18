// SPDX-License-Identifier: GPL-3.0-only
package app

import (
	"ally-dev/locales"
	"errors"
	"fmt"
	"reflect"
	"strings"
)

var errNativeModelUnchanged = errors.New("model unchanged")

// SetNativeModelReasoning applies the change to the latest committed model.
func (a *App) SetNativeModelReasoning(id ModelIdentity, level string) (bool, bool, error) {
	if err := a.ensureInitialized(); err != nil {
		return false, false, err
	}
	found := false
	err := a.commitConfig(func(cfg *ConfigState) error {
		index := nativeModelIndex(cfg.Models, id)
		if index < 0 {
			return errNativeModelUnchanged
		}
		found = true
		stored := cfg.Models[index].ReasoningEffort
		if stored == "" {
			stored = "auto"
		}
		if stored == level {
			return errNativeModelUnchanged
		}
		cfg.Models[index].ReasoningEffort = level
		return nil
	})
	if errors.Is(err, errNativeModelUnchanged) {
		return found, false, nil
	}
	return found, err == nil, err
}

// ImportNativeModels merges each imported identity into the latest registry.
func (a *App) ImportNativeModels(models []ModelConfig) (int, int, error) {
	if err := a.ensureInitialized(); err != nil {
		return 0, 0, err
	}
	models = cloneConfig(ConfigState{Models: models}).Models
	added, updated := 0, 0
	err := a.commitConfig(func(cfg *ConfigState) error {
		for _, model := range models {
			index := nativeModelIndex(cfg.Models, identityOfModel(model))
			if index < 0 {
				cfg.Models = append(cfg.Models, model)
				added++
			} else {
				cfg.Models[index] = model
				updated++
			}
		}
		return nil
	})
	return added, updated, err
}

func nativeSameModel(a, b ModelIdentity) bool {
	return strings.EqualFold(strings.TrimSpace(a.ProviderName), strings.TrimSpace(b.ProviderName)) && strings.EqualFold(strings.TrimSpace(a.Model), strings.TrimSpace(b.Model)) && a.OAuthAccountID == b.OAuthAccountID
}
func nativeModelIndex(models []ModelConfig, id ModelIdentity) int {
	for i, m := range models {
		if nativeSameModel(identityOfModel(m), id) {
			return i
		}
	}
	return -1
}

// SaveNativeModel changes one model under the backend config transaction lock.
func (a *App) SaveNativeModel(model ModelConfig, previous *ModelIdentity) error {
	return a.saveNativeModel(model, previous, nil)
}

func (a *App) SaveNativeModelIfUnchanged(model ModelConfig, previous *ModelConfig) error {
	if previous == nil {
		return a.saveNativeModel(model, nil, nil)
	}
	id := identityOfModel(*previous)
	return a.saveNativeModel(model, &id, previous)
}

func (a *App) saveNativeModel(model ModelConfig, previous *ModelIdentity, baseline *ModelConfig) error {
	if err := a.ensureInitialized(); err != nil {
		return err
	}
	copyModel := cloneConfig(ConfigState{Models: []ModelConfig{model}}).Models[0]
	id := identityOfModel(copyModel)
	return a.commitConfig(func(cfg *ConfigState) error {
		index := -1
		if previous != nil {
			index = nativeModelIndex(cfg.Models, *previous)
			if index < 0 {
				return fmt.Errorf("%s", locales.Text("模型已被修改或删除，请刷新后重试"))
			}
		}
		if baseline != nil && index >= 0 {
			// Compare normalized values: the UI copies the entry through JSON, and
			// a future normalization that produces empty rather than nil fields
			// must not turn every edit into a false conflict.
			if !reflect.DeepEqual(normalizeModelEntry(cfg.Models[index]), normalizeModelEntry(*baseline)) {
				return fmt.Errorf("%s", locales.Text("模型已被修改或删除，请刷新后重试"))
			}
		}
		duplicate := nativeModelIndex(cfg.Models, id)
		if duplicate >= 0 && duplicate != index {
			return fmt.Errorf("%s", locales.Text("该模型和账号已存在"))
		}
		if index >= 0 {
			cfg.Models[index] = copyModel
		} else {
			cfg.Models = append(cfg.Models, copyModel)
		}
		if cfg.LastUsedModel == nil || previous != nil && nativeSameModel(*cfg.LastUsedModel, *previous) {
			cfg.LastUsedModel = &ModelIdentity{ProviderName: id.ProviderName, Model: id.Model, OAuthAccountID: id.OAuthAccountID}
		}
		return nil
	})
}
func (a *App) DeleteNativeModel(id ModelIdentity) error {
	if err := a.ensureInitialized(); err != nil {
		return err
	}
	return a.commitConfig(func(cfg *ConfigState) error {
		index := nativeModelIndex(cfg.Models, id)
		if index < 0 {
			return fmt.Errorf("%s", locales.Text("模型已不存在，请刷新列表"))
		}
		cfg.Models = append(cfg.Models[:index], cfg.Models[index+1:]...)
		if cfg.LastUsedModel != nil && nativeSameModel(*cfg.LastUsedModel, id) {
			cfg.LastUsedModel = nil
		}
		return nil
	})
}
func (a *App) SelectNativeModel(id ModelIdentity) error {
	if err := a.ensureInitialized(); err != nil {
		return err
	}
	return a.commitConfig(func(cfg *ConfigState) error {
		index := nativeModelIndex(cfg.Models, id)
		if index < 0 {
			return fmt.Errorf("%s", locales.Text("模型已不存在，请刷新列表"))
		}
		actual := identityOfModel(cfg.Models[index])
		cfg.LastUsedModel = &actual
		return nil
	})
}
