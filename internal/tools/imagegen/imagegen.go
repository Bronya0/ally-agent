// SPDX-License-Identifier: GPL-3.0-only
// Package imagegen owns provider-neutral image input bounds and validation.
package imagegen

import (
	"bytes"
	"errors"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"strings"
	"unicode/utf8"

	_ "golang.org/x/image/webp"
)

const MaxImageBytes = 20 * 1024 * 1024
const MaxPromptChars = 16000
const MaxInputs = 5

type Request struct {
	Prompt      string   `json:"prompt"`
	Images      []string `json:"images,omitempty"`
	AspectRatio string   `json:"aspect_ratio,omitempty"`
}

func (r Request) Validate(edit bool) error {
	if strings.TrimSpace(r.Prompt) == "" || utf8.RuneCountInString(r.Prompt) > MaxPromptChars {
		return errors.New("prompt must contain 1–16000 characters")
	}
	if edit && (len(r.Images) == 0 || len(r.Images) > MaxInputs) {
		return errors.New("editing requires 1–5 image references")
	}
	if !edit && len(r.Images) != 0 {
		return errors.New("use image_edit for reference images")
	}
	switch r.AspectRatio {
	case "", "1:1", "3:2", "2:3":
		return nil
	}
	return errors.New("aspect_ratio must be 1:1, 3:2, or 2:3")
}

func Inspect(data []byte) (mime string, width, height int, err error) {
	if len(data) == 0 || len(data) > MaxImageBytes {
		return "", 0, 0, errors.New("image exceeds the 20 MiB limit or is empty")
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || (format != "png" && format != "jpeg" && format != "webp") {
		return "", 0, 0, errors.New("expected a PNG, JPEG, or WebP image")
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > 100_000_000 {
		return "", 0, 0, errors.New("invalid image dimensions")
	}
	return "image/" + format, cfg.Width, cfg.Height, nil
}
