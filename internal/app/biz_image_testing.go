// SPDX-License-Identifier: GPL-3.0-only
package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"

	"ally-dev/internal/tools/imagegen"
)

// ImageModelTestRequest accepts uploaded originals only, never arbitrary paths.
type ImageModelTestRequest struct {
	Prompt      string   `json:"prompt"`
	Images      []string `json:"images"`
	AspectRatio string   `json:"aspectRatio"`
}

func imageModelTestSession(id ModelIdentity) string {
	identity, _ := json.Marshal([]string{strings.ToLower(strings.TrimSpace(id.ProviderName)), strings.ToLower(strings.TrimSpace(id.Model))})
	return "image-model-test:" + string(identity)
}

func (a *App) TestImageModel(id ModelIdentity, input ImageModelTestRequest) (GeneratedImage, error) {
	a.mu.Lock()
	cfg := cloneConfig(a.config)
	a.mu.Unlock()
	edit := len(input.Images) > 0
	model, err := imageModelByIdentity(cfg.ImageGeneration, &id, edit)
	if err != nil {
		return GeneratedImage{}, err
	}
	req := imagegen.Request{Prompt: input.Prompt, Images: input.Images, AspectRatio: input.AspectRatio}
	if err := req.Validate(edit); err != nil {
		return GeneratedImage{}, err
	}
	// Validate the entire upload before writing any sources or making a paid call.
	originals := make([][]byte, len(input.Images))
	total := 0
	for i, source := range input.Images {
		header, encoded, ok := strings.Cut(source, ",")
		if !ok || !strings.HasPrefix(header, "data:image/") || !strings.HasSuffix(header, ";base64") || len(encoded) > base64.StdEncoding.EncodedLen(imagegen.MaxImageBytes-total) {
			return GeneratedImage{}, errors.New("expected uploaded images within the combined 20 MiB limit")
		}
		data, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return GeneratedImage{}, errors.New("invalid image data")
		}
		total += len(data)
		if total > imagegen.MaxImageBytes {
			return GeneratedImage{}, errors.New("reference images exceed the combined 20 MiB limit")
		}
		if _, _, _, err := imagegen.Inspect(data); err != nil {
			return GeneratedImage{}, err
		}
		originals[i] = data
	}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return GeneratedImage{}, err
	}
	sessionID := imageModelTestSession(id)
	req.Images = make([]string, len(originals))
	for i, data := range originals {
		stored, err := a.saveGeneratedImage(data, GeneratedImage{SessionID: sessionID, SourceOnly: true})
		if err != nil {
			return GeneratedImage{}, err
		}
		req.Images[i] = stored.Ref
	}
	return a.imageWithModel(ctx, cfg, sessionID, model, req)
}
