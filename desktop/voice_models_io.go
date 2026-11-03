// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"encoding/json"
	"fmt"
	"strings"

	backend "ally-dev/internal/app"
)

const voiceModelFileKind = "ally-voice-models"

type voiceModelFile struct {
	Kind    string                     `json:"kind"`
	Version int                        `json:"formatVersion"`
	Models  []backend.VoiceModelConfig `json:"models"`
}

func parseVoiceModelFile(data []byte) ([]backend.VoiceModelConfig, error) {
	if len(data) > 2*1024*1024 {
		return nil, fmt.Errorf("文件超过 2 MB")
	}
	var doc voiceModelFile
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("无法解析语音模型配置：%w", err)
	}
	if doc.Kind != voiceModelFileKind || doc.Version != 1 || doc.Models == nil || len(doc.Models) > 200 {
		return nil, fmt.Errorf("请选择 Ally 导出的语音模型配置文件")
	}
	seen := map[string]bool{}
	for i := range doc.Models {
		m := &doc.Models[i]
		m.ID, m.Name = strings.TrimSpace(m.ID), strings.TrimSpace(m.Name)
		if m.ID == "" || seen[m.ID] {
			return nil, fmt.Errorf("语音模型 ID 必须填写且不能重复")
		}
		seen[m.ID] = true
		if m.Protocol == "" {
			m.Protocol = "openai_audio"
		}
		if m.Deployment == "" {
			m.Deployment = "local"
		}
		if m.AuthMode == "" {
			m.AuthMode = "none"
		}
		if m.FileField == "" {
			m.FileField = "file"
		}
		if m.TimeoutSeconds == 0 {
			m.TimeoutSeconds = 30
		}
		if m.AuthMode == "bearer" && m.AuthPrefix == "" {
			m.AuthPrefix = "Bearer "
		}
	}
	return doc.Models, nil
}

func (a *desktop) importVoiceModels() {
	if a.voiceModels != nil && a.voiceModels.busy {
		return
	}
	a.ensureVoiceModels()
	go func() {
		path, err := a.core.SelectJSONFile("导入语音模型配置")
		if err != nil || path == "" {
			if err != nil {
				a.fail(err)
			}
			return
		}
		data, err := a.core.ReadLocalTextFile(path)
		var imported []backend.VoiceModelConfig
		if err == nil {
			imported, err = parseVoiceModelFile([]byte(data))
		}
		if err != nil {
			a.fail(err)
			return
		}
		a.win.Update(func() {
			s := a.voiceModels
			if s == nil || s.busy {
				return
			}
			next := cloneVoiceModelConfigs(s.models)
			added, updated := 0, 0
			for _, model := range imported {
				found := -1
				for i := range next {
					if next[i].ID == model.ID {
						found = i
						break
					}
				}
				if found < 0 {
					next = append(next, model)
					added++
				} else {
					if model.APIKey == "" {
						model.APIKey = next[found].APIKey
					}
					next[found] = model
					updated++
				}
			}
			a.commitVoiceModels(next, fmt.Sprintf("导入完成：新增 %d 个，更新 %d 个", added, updated), false, cloneVoiceModelConfigs(s.models))
		})
	}()
}

func (a *desktop) exportVoiceModels() {
	a.ensureVoiceModels()
	models := cloneVoiceModelConfigs(a.voiceModels.models)
	// Credentials are deliberately omitted from portable exports, including keys
	// entered as custom Authorization / API-key headers.
	for i := range models {
		models[i].APIKey = ""
		for name := range models[i].Headers {
			n := strings.ToLower(strings.TrimSpace(name))
			if n == "authorization" || n == "proxy-authorization" || n == "x-api-key" || n == strings.ToLower(strings.TrimSpace(models[i].AuthHeader)) {
				delete(models[i].Headers, name)
			}
		}
	}
	if models == nil {
		models = []backend.VoiceModelConfig{}
	}
	data, err := json.MarshalIndent(voiceModelFile{Kind: voiceModelFileKind, Version: 1, Models: models}, "", "  ")
	if err != nil {
		a.notifyError(err.Error())
		return
	}
	a.exportText("ally-voice-models.json", string(data))
}

// commitVoiceModels serializes one voice-model change through the shared config
// write queue: clone the list, mark the page busy, wait for earlier writers, save,
// reload the config and report on the main thread. Save, delete and import all go
// through this one path so they cannot drift apart. restore, when not nil, is the
// list put back if the save fails.
func (a *desktop) commitVoiceModels(models []backend.VoiceModelConfig, success string, closeForm bool, restore []backend.VoiceModelConfig) {
	s := a.voiceModels
	if s == nil || s.busy {
		return
	}
	s.busy = true
	previous, done := a.configWriteSlot()
	next := cloneVoiceModelConfigs(models)
	go func() {
		defer close(done)
		waitConfigWrite(previous)
		err := a.core.SaveNativeVoiceModels(next)
		var cfg backend.ConfigState
		if err == nil {
			cfg, err = a.core.GetConfig()
		}
		a.win.Update(func() {
			s.busy = false
			if err != nil {
				if restore != nil {
					s.models = restore
				}
				a.notifyError(err.Error())
				return
			}
			a.config = cfg
			s.models = cloneVoiceModelConfigs(cfg.VoiceModels)
			if closeForm {
				s.formOpen = false
			}
			a.notifySuccess(success)
		})
	}()
}
