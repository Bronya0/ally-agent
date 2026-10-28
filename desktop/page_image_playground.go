// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	backend "ally-dev/internal/app"
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"io"
	"os"
	"path/filepath"
	"strings"

	"unicode/utf8"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

type imageTestInput struct {
	name    string
	dataURL string
	size    int
	preview *ui.Bitmap
}
type imageTestResult struct {
	meta    backend.GeneratedImage
	preview *ui.Bitmap
}
type imageModelTest struct {
	model              backend.ModelIdentity
	prompt, ratio, err string
	inputs             []imageTestInput
	results            []imageTestResult
	busy               bool
}
type preparedTestInput struct {
	name, dataURL string
	size          int
	preview       image.Image
}

const imageTestMaxBytes = 20 * 1024 * 1024

func readImageTestInputs(paths []string, existingCount, existingBytes int) ([]preparedTestInput, error) {
	if len(paths)+existingCount > 5 {
		return nil, fmt.Errorf("最多添加 5 张图片")
	}
	out := make([]preparedTestInput, 0, len(paths))
	total := existingBytes
	for _, path := range paths {
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		info, err := f.Stat()
		if err != nil {
			_ = f.Close()
			return nil, err
		}
		if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > int64(imageTestMaxBytes-total) {
			_ = f.Close()
			return nil, fmt.Errorf("图片合计不得超过 20 MiB")
		}
		data, err := io.ReadAll(io.LimitReader(f, int64(imageTestMaxBytes-total)+1))
		_ = f.Close()
		if err != nil {
			return nil, err
		}
		total += len(data)
		if total > imageTestMaxBytes {
			return nil, fmt.Errorf("图片合计不得超过 20 MiB")
		}
		cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
		if err != nil || cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > 64*1024*1024 {
			return nil, fmt.Errorf("图片无法读取或尺寸过大")
		}
		typ := ""
		switch format {
		case "png":
			typ = "image/png"
		case "jpeg":
			typ = "image/jpeg"
		case "webp":
			typ = "image/webp"
		default:
			return nil, fmt.Errorf("仅支持 PNG、JPEG 或 WebP")
		}
		img, _, err := image.Decode(bytes.NewReader(data))
		if err != nil {
			return nil, err
		}
		out = append(out, preparedTestInput{name: filepath.Base(path), size: len(data), dataURL: "data:" + typ + ";base64," + base64.StdEncoding.EncodeToString(data), preview: resizeImage(img, 256)})
	}
	return out, nil
}
func (a *desktop) chooseImageTestInputs(s *imageModelsState, t *imageModelTest) {
	if t.busy {
		return
	}
	t.busy = true
	count, total := len(t.inputs), 0
	for _, input := range t.inputs {
		total += input.size
	}
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: a.win, Title: "选择参考图片", Multiple: true, Filters: []mygo.FileFilter{{Name: "图片", Extensions: []string{"png", "jpg", "jpeg", "webp"}}}})
		var prepared []preparedTestInput
		if err == nil && len(paths) > 0 {
			prepared, err = readImageTestInputs(paths, count, total)
		}
		a.win.Update(func() {
			t.busy = false
			if s.test != t {
				return
			}
			if err != nil {
				t.err = err.Error()
				return
			}
			for _, in := range prepared {
				t.inputs = append(t.inputs, imageTestInput{name: in.name, size: in.size, dataURL: in.dataURL, preview: ui.NewBitmap(in.preview)})
			}
			t.err = ""
		})
	}()
}

func (a *desktop) runImageModelTest(s *imageModelsState, t *imageModelTest) {
	if t.busy || s.busy {
		return
	}
	prompt := strings.TrimSpace(t.prompt)
	if prompt == "" || utf8.RuneCountInString(prompt) > 16000 {
		t.err = "提示词不能为空，且不能超过 16000 字"
		return
	}
	images := make([]string, len(t.inputs))
	for i, in := range t.inputs {
		images[i] = in.dataURL
	}
	value := cloneImages(&s.config)
	identity := t.model
	ratio := t.ratio
	t.busy = true
	s.busy = true
	t.err = ""
	go func() {
		err := a.core.SaveNativeImageConfig(value)
		var cfg backend.ConfigState
		fresh := false
		if err == nil {
			cfg, err = a.core.GetConfig()
			fresh = err == nil
		}
		var result backend.GeneratedImage
		var preview image.Image
		if err == nil {
			result, err = a.core.TestImageModel(identity, backend.ImageModelTestRequest{Prompt: prompt, Images: images, AspectRatio: ratio})
		}
		if err == nil {
			data, e := a.core.ReadNativeGeneratedImage(result.Ref)
			if e == nil {
				if cfg, _, e := image.DecodeConfig(bytes.NewReader(data)); e == nil && int64(cfg.Width)*int64(cfg.Height) <= 64*1024*1024 {
					if img, _, e := image.Decode(bytes.NewReader(data)); e == nil {
						preview = resizeImage(img, 512)
					}
				}
			}
		}
		a.win.Update(func() {
			t.busy = false
			s.busy = false
			if fresh {
				a.config = cfg
				s.config = cloneImages(cfg.ImageGeneration)
				s.resetSelections()
			}
			if s.test != t {
				return
			}
			if err != nil {
				t.err = err.Error()
				return
			}
			var bm *ui.Bitmap
			if preview != nil {
				bm = ui.NewBitmap(preview)
			}
			t.results = append([]imageTestResult{{meta: result, preview: bm}}, t.results...)
			if len(t.results) > 20 {
				t.results = t.results[:20]
			}
		})
	}()
}
