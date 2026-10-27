// SPDX-License-Identifier: GPL-3.0-only
package app

import (
	"github.com/egoist/mygo"
	"os"
	"path/filepath"
)

func (a *App) ExportGeneratedImage(ref string) (string, error) {
	data, meta, err := a.readGeneratedImage(ref)
	if err != nil {
		return "", err
	}
	p, err := mygo.Dialog.Save(mygo.SaveDialogOptions{Title: "保存原图", DefaultPath: generatedImageFilename(ref, meta.Mime)})
	if err != nil || p == "" {
		return p, err
	}
	if filepath.Ext(p) == "" {
		p += generatedImageExtension(meta.Mime)
	}
	return p, os.WriteFile(p, data, 0600)
}

// ReadNativeGeneratedImage exposes the existing validated image store to the
// native desktop host; it does not change storage or image-generation logic.
func (a *App) ReadNativeGeneratedImage(ref string) ([]byte, error) {
	data, _, err := a.readGeneratedImage(ref)
	return data, err
}
