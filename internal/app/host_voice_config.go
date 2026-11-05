// SPDX-License-Identifier: GPL-3.0-only
package app

// SaveNativeVoiceModels updates only the speech-to-text registry, preserving
// unrelated configuration changed concurrently by other settings pages.
func (a *App) SaveNativeVoiceModels(models []VoiceModelConfig) error {
	if err := a.ensureInitialized(); err != nil {
		return err
	}
	snapshot := normalizeVoiceModels(models)
	for _, model := range snapshot {
		if err := validateVoiceModel(model); err != nil {
			return err
		}
	}
	return a.commitConfig(func(cfg *ConfigState) error {
		cfg.VoiceModels = snapshot
		return nil
	})
}
