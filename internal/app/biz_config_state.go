// SPDX-License-Identifier: GPL-3.0-only
package app

import (
	"errors"
	"maps"
	"runtime"
	"slices"
	"strings"
)

// cloneConfig owns mutable values; runtime service dependencies remain shared.
func cloneConfig(cfg ConfigState) ConfigState {
	cfg.APIKeys = slices.Clone(cfg.APIKeys)
	cfg.ExtraRoots = slices.Clone(cfg.ExtraRoots)
	cfg.CustomHeaders = maps.Clone(cfg.CustomHeaders)
	cfg.Models = slices.Clone(cfg.Models)
	for i := range cfg.Models {
		cfg.Models[i].APIKeys = slices.Clone(cfg.Models[i].APIKeys)
		cfg.Models[i].CustomHeaders = maps.Clone(cfg.Models[i].CustomHeaders)
		cfg.Models[i].VisionCapable = cloneConfigPointer(cfg.Models[i].VisionCapable)
	}
	cfg.DisabledSkills = slices.Clone(cfg.DisabledSkills)
	cfg.DisabledTools = slices.Clone(cfg.DisabledTools)
	cfg.DisabledPlugins = slices.Clone(cfg.DisabledPlugins)
	cfg.HiddenModes = slices.Clone(cfg.HiddenModes)
	cfg.SkippedUpdates = slices.Clone(cfg.SkippedUpdates)
	cfg.VisionCapable = cloneConfigPointer(cfg.VisionCapable)
	cfg.LastUsedModel = cloneConfigPointer(cfg.LastUsedModel)
	cfg.AllowPrivateNetwork = cloneConfigPointer(cfg.AllowPrivateNetwork)
	cfg.AutoValidationPython = cloneConfigPointer(cfg.AutoValidationPython)
	cfg.AutoValidationGo = cloneConfigPointer(cfg.AutoValidationGo)
	cfg.AutoValidationJavaScript = cloneConfigPointer(cfg.AutoValidationJavaScript)
	cfg.AutoValidationTypeScript = cloneConfigPointer(cfg.AutoValidationTypeScript)
	cfg.AutoValidationVue = cloneConfigPointer(cfg.AutoValidationVue)
	cfg.AutoValidationJava = cloneConfigPointer(cfg.AutoValidationJava)
	cfg.AutoValidationJSON = cloneConfigPointer(cfg.AutoValidationJSON)
	cfg.AutoUpdate = cloneConfigPointer(cfg.AutoUpdate)
	return cfg
}

func cloneConfigPointer[T any](value *T) *T {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

// normalizeStoredConfig is shared by disk loading and committed writes.
// Zero values here are values, not omitted updates.
func normalizeStoredConfig(cfg ConfigState) ConfigState {
	if runtime.GOOS == "windows" {
		cfg.GitBashPath = resolveGitBashPath(cfg.GitBashPath)
	}
	cfg.ToolConcurrency = clampToolConcurrency(cfg.ToolConcurrency)
	cfg.ProxyMode = normalizeProxyMode(cfg.ProxyMode)
	cfg.ProxyURL = strings.TrimSpace(cfg.ProxyURL)
	cfg.ProxyNoProxy = strings.TrimSpace(cfg.ProxyNoProxy)
	cfg.UserAgent = strings.TrimSpace(cfg.UserAgent)
	cfg.GitHubToken = strings.TrimSpace(cfg.GitHubToken)
	cfg.KBRoot = strings.TrimSpace(cfg.KBRoot)
	cfg.BackgroundOpacity = clampBackgroundOpacity(cfg.BackgroundOpacity)
	cfg.AutoCompactMethod = normalizeAutoCompactMethod(cfg.AutoCompactMethod)
	cfg.CompactThreshold = clampCompactThreshold(cfg.CompactThreshold)
	cfg.CompactTimeoutSeconds = clampCompactTimeoutSeconds(cfg.CompactTimeoutSeconds)
	cfg.MessageFontSize = clampMessageFontSize(cfg.MessageFontSize)
	cfg.CodeFontSize = clampFontSize(cfg.CodeFontSize, defaultCodeFontSize, 12, 24)
	cfg.ToolFontSize = clampFontSize(cfg.ToolFontSize, defaultToolFontSize, 12, 24)
	cfg.SubFontSize = clampFontSize(cfg.SubFontSize, defaultSubFontSize, 11, 18)
	cfg.AuxFontSize = clampFontSize(cfg.AuxFontSize, defaultAuxFontSize, 10, 20)
	cfg.DisabledSkills = normalizeSkillNameList(cfg.DisabledSkills)
	for i := range cfg.Models {
		cfg.Models[i] = normalizeModelEntry(cfg.Models[i])
	}
	return cfg
}

// Called only on owned model entries by stored and request normalization.
func normalizeModelEntry(model ModelConfig) ModelConfig {
	model.APIFormat = normalizeAPIFormat(model.APIFormat)
	model.ReasoningTag = normalizeReasoningTag(model.ReasoningTag)
	model.ReasoningEffort = normalizeReasoningEffort(model.ReasoningEffort)
	model.CustomHeaders = normalizeCustomHeaders(model.CustomHeaders)
	syncModelAPIKeyFields(&model)
	return model
}

func validateStoredConfig(cfg ConfigState) error {
	if cfg.ProxyMode == proxyModeManual && normalizeProxyURL(cfg.ProxyURL, "http") == "" {
		return errors.New("manual proxy URL must be a valid http://, https://, or socks5:// URL")
	}
	return nil
}

// readConfigFile decodes onto defaults, distinguishing absent legacy fields
// from explicit JSON zero, false, and empty values.
func loadedConfig(loaded ConfigState) ConfigState {
	cfg := cloneConfig(loaded)
	if cfg.LastUsedModel != nil {
		cfg.LastUsedModel = normalizeModelIdentity(cfg.Models, *cfg.LastUsedModel)
	}
	if cfg.LastUsedModel == nil {
		migrateLegacyModelFields(&cfg, loaded)
	}
	return persistableConfig(cfg)
}

// Settings accept editable fields only, excluding request model fields and
// ExtraRoots. Background filenames belong to the picker. Nil collections and
// pointers preserve compatibility with callers that omit newer fields.
func settingsConfig(base, req ConfigState) ConfigState {
	cfg := cloneConfig(base)
	req = cloneConfig(req)
	cfg.CustomPrompt = req.CustomPrompt
	cfg.GitHubToken = req.GitHubToken
	cfg.KBRoot = req.KBRoot
	cfg.GitBashPath = req.GitBashPath
	cfg.ProxyMode = req.ProxyMode
	cfg.ProxyURL = req.ProxyURL
	cfg.ProxyNoProxy = req.ProxyNoProxy
	cfg.UserAgent = req.UserAgent
	cfg.BackgroundOpacity = req.BackgroundOpacity
	if req.Workspace != "" {
		cfg.Workspace = req.Workspace
	}
	if req.Models != nil {
		cfg.Models = req.Models
	}
	if req.DisabledSkills != nil {
		cfg.DisabledSkills = req.DisabledSkills
	}
	if req.DisabledTools != nil {
		cfg.DisabledTools = req.DisabledTools
	}
	if req.DisabledPlugins != nil {
		cfg.DisabledPlugins = req.DisabledPlugins
	}
	if req.HiddenModes != nil {
		cfg.HiddenModes = req.HiddenModes
	}
	if req.SkippedUpdates != nil {
		cfg.SkippedUpdates = req.SkippedUpdates
	}
	if req.AllowPrivateNetwork != nil {
		cfg.AllowPrivateNetwork = req.AllowPrivateNetwork
	}
	if req.AutoValidationPython != nil {
		cfg.AutoValidationPython = req.AutoValidationPython
	}
	if req.AutoValidationGo != nil {
		cfg.AutoValidationGo = req.AutoValidationGo
	}
	if req.AutoValidationJavaScript != nil {
		cfg.AutoValidationJavaScript = req.AutoValidationJavaScript
	}
	if req.AutoValidationTypeScript != nil {
		cfg.AutoValidationTypeScript = req.AutoValidationTypeScript
	}
	if req.AutoValidationVue != nil {
		cfg.AutoValidationVue = req.AutoValidationVue
	}
	if req.AutoValidationJava != nil {
		cfg.AutoValidationJava = req.AutoValidationJava
	}
	if req.AutoValidationJSON != nil {
		cfg.AutoValidationJSON = req.AutoValidationJSON
	}
	if req.AutoUpdate != nil {
		cfg.AutoUpdate = req.AutoUpdate
	}
	if req.ToolConcurrency != 0 {
		cfg.ToolConcurrency = req.ToolConcurrency
	}
	if req.LLMRetries != 0 {
		cfg.LLMRetries = req.LLMRetries
	}
	if req.CompactThreshold != 0 {
		cfg.CompactThreshold = req.CompactThreshold
	}
	if req.CompactTimeoutSeconds != 0 {
		cfg.CompactTimeoutSeconds = req.CompactTimeoutSeconds
	}
	if req.MessageFontSize != 0 {
		cfg.MessageFontSize = req.MessageFontSize
	}
	if req.CodeFontSize != 0 {
		cfg.CodeFontSize = req.CodeFontSize
	}
	if req.ToolFontSize != 0 {
		cfg.ToolFontSize = req.ToolFontSize
	}
	if req.SubFontSize != 0 {
		cfg.SubFontSize = req.SubFontSize
	}
	if req.AuxFontSize != 0 {
		cfg.AuxFontSize = req.AuxFontSize
	}
	if req.AutoCompactMethod != "" {
		cfg.AutoCompactMethod = req.AutoCompactMethod
	}
	if req.WindowWidth > 0 && req.WindowHeight > 0 {
		cfg.WindowWidth, cfg.WindowHeight = req.WindowWidth, req.WindowHeight
	}
	if req.LastUsedModel != nil {
		if identity := normalizeModelIdentity(cfg.Models, *req.LastUsedModel); identity != nil {
			cfg.LastUsedModel = identity
		}
	}
	return normalizeStoredConfig(cfg)
}
