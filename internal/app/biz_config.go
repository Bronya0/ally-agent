// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	goruntime "runtime"
	"runtime/debug"
	"strings"
	"time"

	"ally-dev/internal/tools/pathutil"

	openai "github.com/sashabaranov/go-openai"
)

// ── Config state ─────────────────────────────────────────────

func defaultConfigState() ConfigState {
	cfg := ConfigState{
		ProviderName:        "OpenAI Compatible",
		APIFormat:           apiFormatOpenAIChat,
		BaseURL:             defaultBaseURL,
		Model:               defaultModel,
		Workspace:           defaultWorkspaceDir(),
		MaxTokens:           131072,
		ContextWindow:       1000000,
		AllowPrivateNetwork: true,
		ProxyMode:           proxyModeOff,
		ReasoningTag:        defaultReasoningTag,
		ReasoningEffort:     reasoningEffortMax,
		BackgroundOpacity:   defaultBackgroundOpacity,
		CompactThreshold:    defaultCompactThreshold,
		MessageFontSize:     defaultMessageFontSize,
		CodeFontSize:        defaultCodeFontSize,
		ToolFontSize:        defaultToolFontSize,
		SubFontSize:         defaultSubFontSize,
		AuxFontSize:         defaultAuxFontSize,
	}
	if goruntime.GOOS == "windows" {
		cfg.GitBashPath, _ = findWindowsBash("")
	}
	return cfg
}

func resolveConfigLoadPath(configPath string) (string, error) {
	if _, err := os.Stat(configPath); err == nil {
		return configPath, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	return "", os.ErrNotExist
}

func readConfigFile(path string) (ConfigState, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return ConfigState{}, err
	}
	var loaded ConfigState
	if err := json.Unmarshal(data, &loaded); err != nil {
		return ConfigState{}, err
	}
	return loaded, nil
}

func appDataDir() string {
	if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		return filepath.Join(home, ".ally_agent")
	}
	if cfgDir, err := os.UserConfigDir(); err == nil && strings.TrimSpace(cfgDir) != "" {
		return filepath.Join(cfgDir, "Ally")
	}
	return ".ally_agent"
}

// AppDataDir returns the absolute path to ~/.ally_agent/. Implements
// pathutil.Runtime so the path-safety helpers can reach the global config
// directory through a host-neutral interface without importing app.
func (a *App) AppDataDir() string { return appDataDir() }

// appPathRuntime is a package-level pathutil.Runtime backed by appDataDir().
// It lets the package-level path helper wrappers below delegate to pathutil
// without each call site passing *App explicitly, and without depending on
// aGlobalApp (which is nil before NewApp).
type appPathRuntime struct{}

func (appPathRuntime) AppDataDir() string { return appDataDir() }

// pathRuntime is the pathutil.Runtime used by the package-level path helpers.
var pathRuntime pathutil.Runtime = appPathRuntime{}

// MemoriesDir returns the absolute path to ~/.ally_agent/memories/.
// Implements memory.Runtime so the memory tool can reach the memories
// directory through the host-neutral interface without importing app.
func (a *App) MemoriesDir() string {
	return filepath.Join(appDataDir(), "memories")
}

// memoriesDir is a package-level convenience kept for the few call sites
// that still use it instead of (*App).MemoriesDir().
func memoriesDir() string {
	return aGlobalApp.MemoriesDir()
}

// ── Skills ───────────────────────────────────────────────

func (a *App) getConfig() (ConfigState, error) {
	if err := a.ensureInitialized(); err != nil {
		return ConfigState{}, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.config, nil
}

func (a *App) saveConfig(cfg ConfigState) error {
	a.mu.Lock()
	cfg.DisabledSkills = normalizeSkillNameList(cfg.DisabledSkills)
	a.config = cfg
	a.disabledSkills = cloneStringSlice(cfg.DisabledSkills)
	path := a.configPath
	a.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// ── Skills: system prompt metadata injection ──

func mergeConfig(base, overlay ConfigState) ConfigState {
	if overlay.ProviderName != "" {
		base.ProviderName = overlay.ProviderName
	}
	if overlay.APIFormat != "" {
		base.APIFormat = normalizeAPIFormat(overlay.APIFormat)
	}
	if overlay.BaseURL != "" {
		base.BaseURL = overlay.BaseURL
	}
	if overlay.APIKeys != nil {
		base.APIKeys = normalizeAPIKeys(overlay.APIKeys)
		// 显式清空旧 apiKey,让 syncAPIKeyFields 重新同步:池非空时镜像
		// 第一个条目,池为空时两者都为空(用户清空全部 key 的场景)。
		base.APIKey = ""
	} else if overlay.APIKey != "" {
		// 旧前端只发 apiKey:整体替换 key 池为单 key,避免残留旧池。
		base.APIKey = strings.TrimSpace(overlay.APIKey)
		base.APIKeys = nil
	}
	syncAPIKeyFields(&base)
	if overlay.Model != "" {
		base.Model = overlay.Model
	}
	if overlay.Workspace != "" {
		base.Workspace = overlay.Workspace
	}
	// ExtraRoots 是会话级配置：overlay 非-nil 时整体替换（包括空切片表示"无附加"）。
	// 不写入 ~/.ally_agent/config.json，因为 a.config.ExtraRoots 始终为 nil（仅在
	// 每次 StartChat 时通过 effectiveConfig 透传到 cfg）。
	if overlay.ExtraRoots != nil {
		base.ExtraRoots = cloneStringSlice(overlay.ExtraRoots)
	}
	if overlay.MaxTokens != 0 {
		base.MaxTokens = overlay.MaxTokens
	}
	if overlay.ContextWindow != 0 {
		base.ContextWindow = overlay.ContextWindow
	}
	if overlay.TokenParam != "" {
		base.TokenParam = overlay.TokenParam
	}
	if overlay.CustomPrompt != "" {
		base.CustomPrompt = overlay.CustomPrompt
	}
	if overlay.GitBashPath != "" {
		base.GitBashPath = overlay.GitBashPath
	}
	if overlay.ProxyMode != "" {
		base.ProxyMode = normalizeProxyMode(overlay.ProxyMode)
	}
	if overlay.ProxyURL != "" {
		base.ProxyURL = overlay.ProxyURL
	}
	if overlay.ProxyNoProxy != "" {
		base.ProxyNoProxy = overlay.ProxyNoProxy
	}
	if overlay.ReasoningTag != "" {
		base.ReasoningTag = overlay.ReasoningTag
	}
	if overlay.ReasoningEffort != "" {
		base.ReasoningEffort = overlay.ReasoningEffort
	}
	if strings.TrimSpace(overlay.UserAgent) != "" {
		base.UserAgent = overlay.UserAgent
	}
	if overlay.Models != nil {
		base.Models = overlay.Models
	}
	if overlay.DisabledSkills != nil {
		base.DisabledSkills = normalizeSkillNameList(overlay.DisabledSkills)
	}
	if overlay.LLMRetries > 0 {
		base.LLMRetries = overlay.LLMRetries
	}
	if overlay.AutoValidationPython != nil {
		base.AutoValidationPython = overlay.AutoValidationPython
	}
	if overlay.AutoValidationGo != nil {
		base.AutoValidationGo = overlay.AutoValidationGo
	}
	if overlay.AutoValidationJavaScript != nil {
		base.AutoValidationJavaScript = overlay.AutoValidationJavaScript
	}
	if overlay.AutoValidationTypeScript != nil {
		base.AutoValidationTypeScript = overlay.AutoValidationTypeScript
	}
	if overlay.AutoValidationVue != nil {
		base.AutoValidationVue = overlay.AutoValidationVue
	}
	if overlay.AutoValidationJava != nil {
		base.AutoValidationJava = overlay.AutoValidationJava
	}
	if overlay.AutoValidationJSON != nil {
		base.AutoValidationJSON = overlay.AutoValidationJSON
	}
	if overlay.AutoUpdate != nil {
		base.AutoUpdate = overlay.AutoUpdate
	}
	if overlay.SkippedUpdates != nil {
		base.SkippedUpdates = cloneStringSlice(overlay.SkippedUpdates)
	}
	// GitHubToken: overlay always wins (including empty, which clears the
	// token). This is safe because the frontend draft is loaded from the
	// current config, so a non-token session always sends "" — which is
	// also the correct default. Legacy frontends that don't know this field
	// also send "", which is correct because they never set a token.
	base.GitHubToken = strings.TrimSpace(overlay.GitHubToken)
	// Background image filename is stored verbatim (it is set by
	// SaveBackgroundImage, not by SaveConfig overlay from the frontend).
	// Opacity is normalized and clamped to [0, 1].
	if strings.TrimSpace(overlay.BackgroundImage) != "" {
		base.BackgroundImage = strings.TrimSpace(overlay.BackgroundImage)
	}
	// BackgroundOpacity: overlay wins whenever it is non-zero. A zero overlay
	// means "frontend didn't include the field" (legacy/older build), so we
	// keep whatever was already on base (which itself defaulted to 0.15 in
	// defaultConfigState). This avoids accidentally resetting the user's
	// chosen opacity when an older frontend round-trips a partial config.
	if overlay.BackgroundOpacity != 0 {
		base.BackgroundOpacity = clampBackgroundOpacity(overlay.BackgroundOpacity)
	}
	// CompactThreshold: same pattern — zero overlay means "field absent",
	// so base (which defaulted to defaultCompactThreshold) is preserved.
	// Non-zero values are clamped to [0.2, 0.95] so a misconfigured value
	// cannot starve the model of reply budget or trigger thrashing.
	if overlay.CompactThreshold != 0 {
		base.CompactThreshold = clampCompactThreshold(overlay.CompactThreshold)
	}
	// MessageFontSize: zero overlay means "field absent" (legacy / older
	// build), so base is preserved; non-zero values are clamped to a
	// readable range.
	if overlay.MessageFontSize != 0 {
		base.MessageFontSize = clampMessageFontSize(overlay.MessageFontSize)
	}
	// CodeFontSize / ToolFontSize / SubFontSize / AuxFontSize follow the
	// same zero-means-absent pattern; non-zero values are clamped to readable
	// ranges.
	if overlay.CodeFontSize != 0 {
		base.CodeFontSize = clampFontSize(overlay.CodeFontSize, defaultCodeFontSize, 12, 24)
	}
	if overlay.ToolFontSize != 0 {
		base.ToolFontSize = clampFontSize(overlay.ToolFontSize, defaultToolFontSize, 12, 24)
	}
	if overlay.SubFontSize != 0 {
		base.SubFontSize = clampFontSize(overlay.SubFontSize, defaultSubFontSize, 11, 18)
	}
	if overlay.AuxFontSize != 0 {
		base.AuxFontSize = clampFontSize(overlay.AuxFontSize, defaultAuxFontSize, 10, 20)
	}
	if overlay.CloseToTray != nil {
		base.CloseToTray = overlay.CloseToTray
	}
	if overlay.WindowWidth > 0 && overlay.WindowHeight > 0 {
		base.WindowWidth = overlay.WindowWidth
		base.WindowHeight = overlay.WindowHeight
	}
	if base.APIFormat == "" {
		base.APIFormat = apiFormatOpenAIChat
	}
	base.ReasoningTag = normalizeReasoningTag(base.ReasoningTag)
	base.ReasoningEffort = normalizeReasoningEffort(base.ReasoningEffort)
	for i := range base.Models {
		base.Models[i].ReasoningTag = normalizeReasoningTag(base.Models[i].ReasoningTag)
		base.Models[i].ReasoningEffort = normalizeReasoningEffort(base.Models[i].ReasoningEffort)
		syncModelAPIKeyFields(&base.Models[i])
	}
	if goruntime.GOOS == "windows" {
		if detected, _ := findWindowsBash(base.GitBashPath); detected != "" {
			base.GitBashPath = detected
		}
	}
	return base
}

func normalizeReasoningTag(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return defaultReasoningTag
	}
	return value
}

// normalizeAPIKeys 归一化 key 池:去除空白、空项并按出现顺序去重。
func normalizeAPIKeys(keys []string) []string {
	out := make([]string, 0, len(keys))
	seen := make(map[string]bool, len(keys))
	for _, k := range keys {
		k = strings.TrimSpace(k)
		if k == "" || seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, k)
	}
	return out
}

// syncAPIKeyFields 保持 APIKey 与 APIKeys 的一致性:池非空时 APIKey 镜像
// 第一个条目(最高优先级);池为空但旧 APIKey 存在时用旧值构造池;两者
// 皆空时清空。
func syncAPIKeyFields(cfg *ConfigState) {
	if len(cfg.APIKeys) > 0 {
		cfg.APIKeys = normalizeAPIKeys(cfg.APIKeys)
		cfg.APIKey = cfg.APIKeys[0]
		return
	}
	if k := strings.TrimSpace(cfg.APIKey); k != "" {
		cfg.APIKeys = []string{k}
		cfg.APIKey = k
		return
	}
	cfg.APIKeys = nil
}

// syncModelAPIKeyFields 是 syncAPIKeyFields 的 ModelConfig 版本。
func syncModelAPIKeyFields(m *ModelConfig) {
	if len(m.APIKeys) > 0 {
		m.APIKeys = normalizeAPIKeys(m.APIKeys)
		m.APIKey = m.APIKeys[0]
		return
	}
	if k := strings.TrimSpace(m.APIKey); k != "" {
		m.APIKeys = []string{k}
		m.APIKey = k
		return
	}
	m.APIKeys = nil
}

// resolveKeyPool 返回配置生效的 key 池(按优先级从高到低)。无池时回退到
// 旧 APIKey 字段构造单元素池,保证老配置兼容。
func resolveKeyPool(cfg ConfigState) []string {
	if len(cfg.APIKeys) > 0 {
		return cfg.APIKeys
	}
	if k := strings.TrimSpace(cfg.APIKey); k != "" {
		return []string{k}
	}
	return nil
}

func (a *App) GetConfig() (ConfigState, error) {
	if err := a.ensureInitialized(); err != nil {
		return ConfigState{}, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.config, nil
}

func (a *App) ReloadConfig() (ConfigState, error) {
	if err := a.ensureInitialized(); err != nil {
		return ConfigState{}, err
	}
	a.mu.Lock()
	configPath := a.configPath
	a.mu.Unlock()

	loadPath, err := resolveConfigLoadPath(configPath)
	if err != nil {
		return ConfigState{}, fmt.Errorf("config file not found: %w", err)
	}
	loaded, err := readConfigFile(loadPath)
	if err != nil {
		return ConfigState{}, err
	}
	cfg := mergeConfig(defaultConfigState(), loaded)

	a.mu.Lock()
	a.config = cfg
	a.disabledSkills = normalizeSkillNameList(cfg.DisabledSkills)
	a.config.DisabledSkills = cloneStringSlice(a.disabledSkills)
	a.mu.Unlock()
	return cfg, nil
}

func (a *App) SaveConfig(req ConfigState) error {
	if err := a.ensureInitialized(); err != nil {
		return err
	}
	if normalizeProxyMode(req.ProxyMode) == proxyModeManual && normalizeProxyURL(req.ProxyURL, "http") == "" {
		return errors.New("manual proxy URL must be a valid http://, https://, or socks5:// URL")
	}
	a.mu.Lock()
	proxyChanged := normalizeProxyMode(a.config.ProxyMode) != normalizeProxyMode(req.ProxyMode) ||
		strings.TrimSpace(a.config.ProxyURL) != strings.TrimSpace(req.ProxyURL) ||
		strings.TrimSpace(a.config.ProxyNoProxy) != strings.TrimSpace(req.ProxyNoProxy)
	a.config = mergeConfig(a.config, req)
	a.config.CustomPrompt = req.CustomPrompt
	a.config.AllowPrivateNetwork = req.AllowPrivateNetwork
	a.config.GitBashPath = req.GitBashPath
	a.config.ProxyMode = normalizeProxyMode(req.ProxyMode)
	a.config.ProxyURL = strings.TrimSpace(req.ProxyURL)
	a.config.ProxyNoProxy = strings.TrimSpace(req.ProxyNoProxy)
	a.config.UserAgent = strings.TrimSpace(req.UserAgent)
	if goruntime.GOOS == "windows" {
		if detected, _ := findWindowsBash(req.GitBashPath); detected != "" {
			a.config.GitBashPath = detected
		}
	}
	a.config.ReasoningTag = normalizeReasoningTag(req.ReasoningTag)
	a.config.ReasoningEffort = normalizeReasoningEffort(req.ReasoningEffort)
	// Background opacity is editable from the frontend slider; persist it
	// directly. The image filename is managed by SaveBackgroundImage — only
	// adopt it from the SaveConfig overlay when the frontend echoes the
	// already-stored value (no path mutation through this code path).
	if strings.TrimSpace(req.BackgroundImage) != "" {
		a.config.BackgroundImage = strings.TrimSpace(req.BackgroundImage)
	}
	a.config.BackgroundOpacity = clampBackgroundOpacity(req.BackgroundOpacity)
	a.disabledSkills = normalizeSkillNameList(a.config.DisabledSkills)
	a.config.DisabledSkills = cloneStringSlice(a.disabledSkills)
	cfg := a.config
	path := a.configPath
	a.mu.Unlock()

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return err
	}
	if proxyChanged && a.ctx != nil {
		// Drop cached Transports immediately so idle connections through the
		// old proxy are released instead of lingering up to IdleConnTimeout.
		invalidateProxyTransportCache()
		go func() {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("RestartMcpServers after proxy change panicked: %v\n%s", r, debug.Stack())
					a.emit("config:warning", map[string]any{
						"field":   "mcp",
						"message": fmt.Sprintf("MCP servers failed to restart after proxy change: %v", r),
					})
				}
			}()
			_ = a.RestartMcpServers()
		}()
	}

	// Validate gitBashPath on Windows: if set but invalid, warn the user.
	if goruntime.GOOS == "windows" && cfg.GitBashPath != "" {
		if info, err := os.Stat(cfg.GitBashPath); err != nil || info.IsDir() {
			a.emit("config:warning", map[string]any{
				"field":   "gitBashPath",
				"message": "The configured Git Bash path does not exist or is a directory. command will fall back to auto-detection or PowerShell.",
			})
		}
	}
	return nil
}

func (a *App) TestModelConnection(model ModelConfig) error {
	networkCfg := a.effectiveConfig(ConfigState{})
	cfg := ConfigState{
		ProviderName:    model.ProviderName,
		APIFormat:       normalizeAPIFormat(model.APIFormat),
		BaseURL:         strings.TrimSpace(model.BaseURL),
		APIKey:          strings.TrimSpace(model.APIKey),
		APIKeys:         cloneStringSlice(model.APIKeys),
		Model:           strings.TrimSpace(model.Model),
		MaxTokens:       32,
		ContextWindow:   model.ContextWindow,
		TokenParam:      normalizeTokenParam(model.TokenParam),
		ReasoningTag:    normalizeReasoningTag(model.ReasoningTag),
		ReasoningEffort: normalizeReasoningEffort(model.ReasoningEffort),
		ProxyMode:       networkCfg.ProxyMode,
		ProxyURL:        networkCfg.ProxyURL,
		ProxyNoProxy:    networkCfg.ProxyNoProxy,
		UserAgent:       networkCfg.UserAgent,
	}
	if cfg.Model == "" {
		return errors.New("model is required")
	}
	if len(resolveKeyPool(cfg)) == 0 {
		return errors.New("API key is required")
	}
	ctx := context.Background()
	if a.ctx != nil {
		ctx = a.ctx
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	_, err := a.completeModelText(ctx, cfg, cfg.Model, []openai.ChatCompletionMessage{{
		Role:    openai.ChatMessageRoleUser,
		Content: "Reply only with OK.",
	}}, 32)
	return err
}

// ── Effective config ─────────────────────────────────────────

func (a *App) effectiveConfig(overlay ConfigState) ConfigState {
	a.mu.Lock()
	base := a.config
	a.mu.Unlock()
	return mergeConfig(base, overlay)
}

// configForWorkspace returns a request-scoped config whose primary workspace is
// explicitly pinned by the caller. UI workspace explorers use this boundary so
// a request cannot accidentally resolve a relative path against another Tab's
// active workspace. An empty workspace preserves the legacy active-config
// behavior used by model-facing tools and compatibility bindings.
func (a *App) configForWorkspace(workspace string) (ConfigState, error) {
	if err := a.ensureInitialized(); err != nil {
		return ConfigState{}, err
	}
	cfg := a.effectiveConfig(ConfigState{})
	workspace = strings.TrimSpace(workspace)
	if workspace == "" {
		return cfg, nil
	}
	root, err := pathutil.RootFromConfig(workspace)
	if err != nil {
		return ConfigState{}, err
	}
	cfg.Workspace = root
	// An explicitly pinned explorer request is confined to this workspace;
	// session-level extra roots must not change its path boundary.
	cfg.ExtraRoots = nil
	return cfg, nil
}

func (a *App) effectiveConfigSafe() ConfigState {
	if err := a.ensureInitialized(); err != nil {
		return defaultConfigState()
	}
	return a.effectiveConfig(ConfigState{})
}

// updateNetworkConfig returns a proxy configuration used only by the
// self-update flow. Updates always prefer the detected system proxy and fall
// back to a direct connection when no usable system/environment proxy exists.
// It intentionally does not mutate or reuse the user's configured proxy mode.
func updateNetworkConfig(cfg ConfigState) ConfigState {
	cfg.ProxyMode = proxyModeSystem
	cfg.ProxyURL = ""
	cfg.ProxyNoProxy = ""
	if !resolveProxy(cfg).status.Enabled {
		cfg.ProxyMode = proxyModeOff
	}
	return cfg
}
