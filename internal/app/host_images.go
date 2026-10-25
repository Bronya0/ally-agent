// SPDX-License-Identifier: GPL-3.0-only
package app

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// ExportGeneratedImage saves the original bytes, never the displayed preview.
func (a *App) ExportGeneratedImage(ref string) (string, error) {
	if a.wails == nil || a.wails.app == nil {
		return "", errors.New("desktop host not initialized")
	}
	data, meta, err := a.readGeneratedImage(ref)
	if err != nil {
		return "", err
	}
	ext := generatedImageExtension(meta.Mime)
	dialog := a.wails.app.Dialog.SaveFile()
	if a.wails.window != nil {
		dialog.AttachToWindow(a.wails.window)
	}
	dialog.SetOptions(&application.SaveFileDialogOptions{Title: "保存原图", Filename: generatedImageFilename(ref, meta.Mime), Filters: []application.FileFilter{{DisplayName: meta.Mime, Pattern: "*" + ext}}})
	selected, err := dialog.PromptForSingleSelection()
	if err != nil || selected == "" {
		return selected, err
	}
	if filepath.Ext(selected) == "" {
		selected += ext
	}
	if err := os.WriteFile(selected, data, 0o600); err != nil {
		return "", err
	}
	return selected, nil
}
