// SPDX-License-Identifier: GPL-3.0-only
package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"ally-dev/internal/tools/imagegen"
)

func (a *App) imageWithConfig(ctx context.Context, cfg ConfigState, sessionID, name string, req imagegen.Request) (GeneratedImage, error) {
	if err := req.Validate(name == "image_edit"); err != nil {
		return GeneratedImage{}, codedToolError("E_BAD_ARGS", err)
	}
	// Re-check current switches without changing the session's frozen schemas.
	a.mu.Lock()
	live := cloneConfig(a.config)
	a.mu.Unlock()
	model, err := imageModelForTool(live, name)
	if err != nil {
		return GeneratedImage{}, codedToolError("E_IMAGE_DISABLED", err)
	}
	return a.imageWithModel(ctx, cfg, sessionID, model, req)
}

// Both tools and the settings tester share reference handling, provider calls,
// and durable originals. Only their model selection and input entry differ.
func (a *App) imageWithModel(ctx context.Context, cfg ConfigState, sessionID string, model ImageModelConfig, req imagegen.Request) (GeneratedImage, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	inputs := make([]imageInput, 0, len(req.Images))
	sources := make([]string, 0, len(req.Images))
	totalBytes := 0
	var err error
	for _, source := range req.Images {
		if err := ctx.Err(); err != nil {
			return GeneratedImage{}, err
		}
		if source == "latest" {
			source, err = a.latestGeneratedImage(sessionID)
			if err != nil {
				return GeneratedImage{}, err
			}
		}
		var data []byte
		if strings.HasPrefix(source, "image:") {
			if !strings.HasPrefix(source, "image:"+imageSessionKey(sessionID)+"/") {
				return GeneratedImage{}, errors.New("image reference belongs to another session")
			}
			var meta GeneratedImage
			data, meta, err = a.readGeneratedImage(source)
			if err == nil && meta.SessionID != sessionID {
				err = errors.New("image reference belongs to another session")
			}
		} else {
			var path string
			path, err = resolveReadFilePath(cfg, source)
			if err == nil {
				err = kbDenyCheckPaths(ctx, cfg, source)
			}
			if err == nil {
				var f *os.File
				f, err = os.Open(path)
				if err == nil {
					data, err = io.ReadAll(io.LimitReader(f, imagegen.MaxImageBytes+1))
					_ = f.Close()
				}
			}
		}
		if err != nil {
			return GeneratedImage{}, fmt.Errorf("read reference image: %w", err)
		}
		totalBytes += len(data)
		if totalBytes > imagegen.MaxImageBytes {
			return GeneratedImage{}, errors.New("reference images exceed the combined 20 MiB limit")
		}
		mime, _, _, err := imagegen.Inspect(data)
		if err != nil {
			return GeneratedImage{}, err
		}
		// Preserve local source originals too, so later edits never depend on a
		// workspace file that may move or be overwritten.
		if !strings.HasPrefix(source, "image:") {
			stored, err := a.saveGeneratedImage(data, GeneratedImage{SessionID: sessionID, Sources: []string{source}, SourceOnly: true})
			if err != nil {
				return GeneratedImage{}, err
			}
			source = stored.Ref
		}
		inputs = append(inputs, imageInput{Data: data, Mime: mime})
		sources = append(sources, source)
	}
	data, err := a.requestImage(ctx, cfg, model, req, inputs)
	if err != nil {
		return GeneratedImage{}, err
	}
	stored, err := a.saveGeneratedImage(data, GeneratedImage{SessionID: sessionID, Model: model.Model, Provider: model.ProviderName, Prompt: req.Prompt, Sources: sources})
	if err != nil {
		return GeneratedImage{}, fmt.Errorf("save generated original: %w", err)
	}
	// The tool result owns this artifact. run:image is reserved for native
	// model image output, which has no tool card to attach to.
	return stored, nil
}
