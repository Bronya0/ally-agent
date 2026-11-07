// SPDX-License-Identifier: GPL-3.0-only
package app

import (
	"os"
	"path/filepath"

	"github.com/google/uuid"
)

type NativeSettingsRequest struct {
	Base, Draft ConfigState
	// Nil preserves the background; empty clears it; otherwise a picked file.
	BackgroundPath *string
}

// SaveNativeSettings applies only the native form's edits under the same
// config transaction used by models, skills, tools and ordinary config saves.
func (a *App) SaveNativeSettings(req NativeSettingsRequest) error {
	if err := a.ensureInitialized(); err != nil {
		return err
	}
	req.Base, req.Draft = cloneConfig(req.Base), cloneConfig(req.Draft)
	var name, dest string
	var imageData []byte
	if path := req.BackgroundPath; path != nil && *path != "" {
		data, ext, err := readBackgroundImageFromFile(*path)
		if err != nil {
			return err
		}
		name = "background." + uuid.NewString() + "." + ext
		dest = filepath.Join(appDataDir(), name)
		imageData = data
	}
	committed := false
	defer func() {
		if !committed && dest != "" {
			_ = os.Remove(dest)
		}
	}()
	if err := a.saveSettingsConfig(func(current *ConfigState) error {
		cfg := settingsConfigChanges(*current, req.Base, req.Draft)
		if req.BackgroundPath != nil {
			cfg.BackgroundImage = name
		}
		if dest != "" {
			if err := writeAtomicBytes(dest, imageData, 0600); err != nil {
				return err
			}
		}
		*current = cfg
		return nil
	}); err != nil {
		return err
	}
	committed = true
	if req.BackgroundPath != nil {
		a.configWriteMu.Lock()
		a.mu.Lock()
		keep := a.config.BackgroundImage
		a.mu.Unlock()
		purgeStaleBackgroundFiles(appDataDir(), keep)
		a.configWriteMu.Unlock()
	}
	return nil
}
