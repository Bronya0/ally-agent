// SPDX-License-Identifier: GPL-3.0-only
package app

// SaveNativeImageConfig updates only the image registry while holding the same
// transaction lock used by every other config writer. The native image panel
// must not overwrite unrelated settings modified concurrently by another page.
func (a *App) SaveNativeImageConfig(images ImageGenerationConfig) error {
	if err := a.ensureInitialized(); err != nil {
		return err
	}
	snapshot := cloneImageConfig(&images)
	return a.commitConfig(func(cfg *ConfigState) error { cfg.ImageGeneration = snapshot; return nil })
}
