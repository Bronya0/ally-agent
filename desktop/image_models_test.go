// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	backend "ally-dev/internal/app"
	"encoding/json"
	"github.com/egoist/mygo/ui"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func imageTestModel() backend.ImageModelConfig {
	return backend.ImageModelConfig{ProviderName: "OpenAI", Model: "gpt-image-1", Protocol: "openai-images", BaseURL: "https://api.openai.com/v1"}
}
func TestImageModelEditPreservesAndRepairsDefaults(t *testing.T) {
	m := imageTestModel()
	s := imageModelsState{config: backend.ImageGenerationConfig{Models: []backend.ImageModelConfig{m}, GenerateModel: imageIdentity(m), EditModel: imageIdentity(m)}}
	renamed := m
	renamed.Model = "gpt-image-2"
	if err := s.replace(0, &renamed); err != nil {
		t.Fatal(err)
	}
	if s.config.GenerateModel.Model != "gpt-image-2" || s.config.EditModel.Model != "gpt-image-2" {
		t.Fatal("renaming did not preserve defaults")
	}
	incompatible := renamed
	incompatible.Protocol = "minimax-images"
	if err := s.replace(0, &incompatible); err != nil {
		t.Fatal(err)
	}
	if s.config.EditModel != nil || s.config.GenerateModel == nil {
		t.Fatal("edit default not repaired")
	}
	if err := s.replace(-1, &incompatible); err == nil {
		t.Fatal("duplicate model accepted")
	}
	if err := s.replace(0, nil); err != nil || s.config.GenerateModel != nil {
		t.Fatal("deleting model did not clear default")
	}
}
func TestImageModelValidationAndImport(t *testing.T) {
	m := imageTestModel()
	valid, err := normalizeImageDraft(m, []string{" one ", "one", "two"}, `{"X-Example":"value"}`)
	if err != nil || len(valid.APIKeys) != 2 || valid.APIKey != "one" || valid.CustomHeaders["X-Example"] != "value" {
		t.Fatalf("validation: %+v, %v", valid, err)
	}
	if _, err = normalizeImageDraft(m, []string{"key"}, `{"X":3}`); err == nil {
		t.Fatal("invalid headers accepted")
	}
	m.BaseURL = "https://user:secret@example.com"
	if _, err = normalizeImageDraft(m, []string{"key"}, ""); err == nil {
		t.Fatal("credential-bearing URL accepted")
	}
	m = imageTestModel()
	raw, _ := json.Marshal(imageModelFile{Version: 1, Kind: "ally-image-models", Models: []backend.ImageModelConfig{m}})
	models, err := parseImageModelFile(raw)
	if err != nil || len(models) != 1 || models[0].Model != m.Model {
		t.Fatalf("import: %v", err)
	}
	if _, err = parseImageModelFile([]byte(`{"version":1,"kind":"ally-models","models":[]}`)); err == nil {
		t.Fatal("accepted wrong model file kind")
	}
}
func TestImageModelsNativePage(t *testing.T) {
	a := demoDesktop()
	a.page = "models"
	tt := ui.NewTester(a.view, 1280, 850)
	if err := tt.Click("图片模型"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("尚未配置生图模型") {
		t.Fatal("empty image models page missing")
	}
	if err := tt.Click("添加"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if _, ok := tt.Find("生图协议"); !ok {
		t.Fatal("image model form missing")
	}
	if _, ok := tt.Find("图片 API Key 1"); !ok {
		t.Fatal("key pool field missing")
	}
	tt.SetSize(900, 620)
	tt.Frame()
}
func TestImageTestInputRejectsUnsupportedAndOversized(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "bad.gif")
	if err := os.WriteFile(file, []byte("not image"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readImageTestInputs([]string{file}, 0, 0); err == nil {
		t.Fatal("bad input accepted")
	}
	if _, err := readImageTestInputs([]string{file}, 5, 0); err == nil || !strings.Contains(err.Error(), "5") {
		t.Fatal("input limit not enforced")
	}
}

func TestImageModelTestViewAndDefaultFiltering(t *testing.T) {
	a := demoDesktop()
	a.page = "models"
	a.modelTab = "图片模型"
	one := imageTestModel()
	other := one
	other.ProviderName = "MiniMax"
	other.Model = "image-01"
	other.Protocol = "minimax-images"
	a.imageModels = &imageModelsState{config: backend.ImageGenerationConfig{Models: []backend.ImageModelConfig{one, other}, GenerateModel: imageIdentity(one)}}
	a.imageModels.resetSelections()
	for _, option := range a.imageModels.options(true) {
		if strings.Contains(option, "MiniMax") {
			t.Fatal("generation-only model offered for editing")
		}
	}
	tt := ui.NewTester(a.view, 1280, 850)
	if err := tt.Click("测试"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("文生图") {
		t.Fatal("model test dialog missing")
	}
	if _, ok := tt.Find("提示词"); !ok {
		t.Fatal("prompt input missing")
	}
	a.imageModels.testOpen = false
	tt.Frame()
	if _, ok := tt.Find("提示词"); ok {
		t.Fatal("test dialog did not close")
	}
}
