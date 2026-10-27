// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	backend "ally-dev/internal/app"
	"encoding/json"
	"fmt"
	"github.com/egoist/mygo"
	"io"
	"os"
	"strings"
)

type imageModelFile struct {
	Version int                        `json:"version"`
	Kind    string                     `json:"kind"`
	Models  []backend.ImageModelConfig `json:"models"`
}

func parseImageModelFile(data []byte) ([]backend.ImageModelConfig, error) {
	var doc imageModelFile
	if len(data) > 2*1024*1024 {
		return nil, fmt.Errorf("文件超过 2 MB")
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("无法解析图片模型文件：%w", err)
	}
	if doc.Kind != "ally-image-models" || doc.Version != 1 || doc.Models == nil || len(doc.Models) > 200 {
		return nil, fmt.Errorf("请选择原版导出的图片模型文件")
	}
	models := make([]backend.ImageModelConfig, 0, len(doc.Models))
	for _, model := range doc.Models {
		keys := model.APIKeys
		if len(keys) == 0 {
			keys = []string{model.APIKey}
		}
		headers := ""
		if model.CustomHeaders != nil {
			b, _ := json.Marshal(model.CustomHeaders)
			headers = string(b)
		}
		m, err := normalizeImageModel(model, keys, headers, false)
		if err != nil {
			return nil, err
		}
		models = append(models, m)
	}
	return models, nil
}
func (a *desktop) importImageModels(s *imageModelsState) {
	if s.busy {
		return
	}
	s.busy = true
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: a.win, Title: "导入图片模型", Filters: []mygo.FileFilter{{Name: "JSON", Extensions: []string{"json"}}}})
		if err != nil || len(paths) == 0 {
			a.win.Update(func() {
				s.busy = false
				if err != nil {
					a.notify(err.Error())
				}
			})
			return
		}
		f, err := os.Open(paths[0])
		var data []byte
		if err == nil {
			data, err = io.ReadAll(io.LimitReader(f, 2*1024*1024+1))
			_ = f.Close()
		}
		var models []backend.ImageModelConfig
		if err == nil {
			models, err = parseImageModelFile(data)
		}
		a.win.Update(func() {
			s.busy = false
			if err != nil {
				a.notify(err.Error())
				return
			}
			added, updated := 0, 0
			for _, model := range models {
				index := -1
				for i, existing := range s.config.Models {
					if imageIdentityKey(existing.ProviderName, existing.Model) == imageIdentityKey(model.ProviderName, model.Model) {
						index = i
						break
					}
				}
				if index < 0 {
					added++
				} else {
					updated++
				}
				_ = s.replace(index, &model)
			}
			a.applyImageConfig()
			a.status = fmt.Sprintf("导入完成：新增 %d，更新 %d，正在保存…", added, updated)
		})
	}()
}
func (a *desktop) exportImageModels(s *imageModelsState) {
	if s.busy {
		return
	}
	s.busy = true
	models := cloneImages(&s.config).Models
	if models == nil {
		models = []backend.ImageModelConfig{}
	}
	go func() {
		path, err := mygo.Dialog.Save(mygo.SaveDialogOptions{Parent: a.win, Title: "导出图片模型", DefaultPath: "ally-image-models.json", Filters: []mygo.FileFilter{{Name: "JSON", Extensions: []string{"json"}}}})
		if err == nil && path != "" {
			if !strings.HasSuffix(strings.ToLower(path), ".json") {
				path += ".json"
			}
			var data []byte
			data, err = json.MarshalIndent(imageModelFile{Version: 1, Kind: "ally-image-models", Models: models}, "", "  ")
			if err == nil {
				data = append(data, '\n')
				err = os.WriteFile(path, data, 0600)
			}
		}
		a.win.Update(func() {
			s.busy = false
			if err != nil {
				a.notify(err.Error())
			} else if path != "" {
				a.status = fmt.Sprintf("已导出 %d 个图片模型", len(models))
			}
		})
	}()
}
