// SPDX-License-Identifier: GPL-3.0-only
package app

import (
	"ally-dev/locales"
	"fmt"
	"reflect"
	"strings"
)

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
