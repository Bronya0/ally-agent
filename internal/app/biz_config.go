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
	"net/textproto"
	"os"
	"path/filepath"
	goruntime "runtime"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"time"

	"ally-dev/internal/tools/pathutil"

	"golang.org/x/net/http/httpguts"

	openai "github.com/sashabaranov/go-openai"
)

// ── Config state ─────────────────────────────────────────────

// Workspace 的默认值刻意留空：空 = 用户从未选择过工作区。首次启动时前端
// 据此落在一个临时工作区 Tab（kind:'temp'，随 Tab/进程销毁），而不是把
// 用户文档目录静默当作工作区。用户经 "+" / 发送时选择器选定真实工作区后，
// config.json 持久化该路径，后续启动恢复它。
func defaultConfigState() ConfigState {
	// 这里不再给顶层模型字段任何默认值：它们由 models[] + lastUsedModel 展开
	// （见 ConfigState 的注释），一条模型都没有时就是空的。前端"未配置模型"
	// 的占位显示由 utils/config.mjs 的 placeholderModel 提供，不进配置。
	cfg := ConfigState{
		AllowPrivateNetwork:   boolPtr(true),
		ProxyMode:             proxyModeOff,
		BackgroundOpacity:     defaultBackgroundOpacity,
		CompactThreshold:      defaultCompactThreshold,
		CompactTimeoutSeconds: defaultCompactTimeoutSeconds,
		MessageFontSize:       defaultMessageFontSize,
		CodeFontSize:          defaultCodeFontSize,
		ToolFontSize:          defaultToolFontSize,
		SubFontSize:           defaultSubFontSize,
		AuxFontSize:           defaultAuxFontSize,
	}
	if goruntime.GOOS == "windows" {
		cfg.GitBashPath = resolveGitBashPath("")
	}
	return cfg
}

// detectedGitBashPath 缓存 Windows Git Bash 自动探测结果。mergeConfig 名义
// 上是纯函数却会在每次请求时被 effectiveConfig() 调用，直接探测等于把磁盘
// IO 埋进配置合并热路径；探测只依赖安装环境（不依赖用户配置），进程内
// 缓存一次即可。用户手动设置的 gitBashPath 仍每次校验，不受此缓存影响。
var detectedGitBashPath = sync.OnceValue(func() string {
	path, _ := findWindowsBash("")
	return path
})

// resolveGitBashPath 把用户配置的 gitBashPath 解析为可执行的 shell 路径：
// 手动配置优先且每次校验（用户可能修复路径后立即生效）；未配置时回落到
// 进程内缓存一次的自动探测结果。
func resolveGitBashPath(configured string) string {
	if detected, _ := findWindowsBash(configured); detected != "" {
		return detected
	}
	if strings.TrimSpace(configured) != "" {
		// 用户配置存在但无效：保留原值，让启动警告与设置页能提示修复。
		return configured
	}
	if goruntime.GOOS == "windows" {
		return detectedGitBashPath()
	}
	return ""
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

// handleUnreadableConfig decides what to do with a config that failed to load.
// Only content that cannot parse is moved aside — leaving a corrupt file in place
// meant the next save overwrote it with defaults, so the user's settings were gone
// for good. A transient read error (EACCES, EMFILE) says nothing about the
// content, so the file stays where a later start can still pick it up. The check
// re-reads the file instead of inspecting the error, because readConfigFile
// deliberately returns a single opaque error. Deliberately package-level and
// lock-free: ensureInitialized holds a.mu here, so logAppError (which takes a.mu)
// would self-deadlock.
func handleUnreadableConfig(path string, cause error) {
	if data, err := os.ReadFile(path); err != nil || json.Unmarshal(data, new(ConfigState)) == nil {
		log.Printf("config file could not be read; leaving it in place: path=%s err=%v cause=%v", path, err, cause)
		return
	}
	quarantined := fmt.Sprintf("%s.corrupt-%d", path, time.Now().UnixNano())
	if err := os.Rename(path, quarantined); err != nil {
		log.Printf("config file is invalid and could not be moved aside: path=%s err=%v cause=%v", path, err, cause)
		return
	}
	log.Printf("config file is invalid; moved aside: path=%s quarantined=%s cause=%v", path, quarantined, cause)
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

// userProfileFileName is the machine-global profile file (how to address the
// user, language, timezone, habits) stripped of anything project-specific. It is
// injected into every request, so it lives next to config.json instead of inside
// the memory index; see buildUserProfilePromptPart.
const userProfileFileName = "USER.md"

// userProfileDisplayPath is how the prompt names that file. Deriving both from
// one constant keeps the reader and the prompt text from drifting apart.
const userProfileDisplayPath = "~/.ally_agent/" + userProfileFileName

// userProfilePath resolves the profile file under the app data directory.
func userProfilePath() string {
	return filepath.Join(appDataDir(), userProfileFileName)
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

// saveConfig persists the whole config. The write goes through the atomic
// helper (temp sibling + rename): a plain os.WriteFile truncates in place, so a
// crash mid-write leaves half a JSON document, and the next start silently
// falls back to defaults and then overwrites the user's real settings.
func (a *App) saveConfig(cfg ConfigState) error {
	convergeLastUsedModel(&cfg)
	// 派生模型字段不落盘（见 ConfigState 的注释）：磁盘上只留 models[] 与
	// lastUsedModel，读回来时再按身份展开。
	stripModelFields(&cfg)
	a.mu.Lock()
	cfg.DisabledSkills = normalizeSkillNameList(cfg.DisabledSkills)
	a.config = cfg
	a.disabledSkills = cloneStringSlice(cfg.DisabledSkills)
	path := a.configPath
	a.mu.Unlock()
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomicBytes(path, data, 0o600)
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
	if overlay.KBRoot != "" {
		base.KBRoot = overlay.KBRoot
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
	// VisionCapable 是三态指针：nil 表示 overlay 未提供该字段并保留 base 的
	// 当前值，true/false 才是显式设置（否则每个不携带该字段的旧版前端都会把
	// 已知的视觉能力抹成“未知”）。
	if overlay.VisionCapable != nil {
		base.VisionCapable = overlay.VisionCapable
	}
	if overlay.ReasoningEffort != "" {
		base.ReasoningEffort = overlay.ReasoningEffort
	}
	if strings.TrimSpace(overlay.UserAgent) != "" {
		base.UserAgent = overlay.UserAgent
	}
	// CustomHeaders: non-nil overlay replaces the whole map (an explicitly
	// empty map clears the headers); nil means "field absent" and keeps the
	// current value, so legacy round-trips never drop a configured header.
	if overlay.CustomHeaders != nil {
		base.CustomHeaders = normalizeCustomHeaders(overlay.CustomHeaders)
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
	// GitHubToken 也走“非空 overlay 胜”的常规口径（与 UserAgent 同）：空 overlay 表示
	// 字段没携带，保留 base（fetchReleaseByTag / DownloadUpdate 走
	// effectiveConfigSafe() 的空 overlay，正是靠这一点才能读到用户配置的 token）；
	// 清空只能经 SaveConfig 的显式写入（那里无条件采信请求值，含空串）。
	if strings.TrimSpace(overlay.GitHubToken) != "" {
		base.GitHubToken = strings.TrimSpace(overlay.GitHubToken)
	}
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
	// CompactTimeoutSeconds: same zero-means-absent pattern. Non-zero values
	// are clamped to [30, 3600] so an edit cannot hang the app on compaction
	// forever or choke a legitimately slow long-context provider.
	if overlay.CompactTimeoutSeconds != 0 {
		base.CompactTimeoutSeconds = clampCompactTimeoutSeconds(overlay.CompactTimeoutSeconds)
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
	if overlay.WindowWidth > 0 && overlay.WindowHeight > 0 {
		base.WindowWidth = overlay.WindowWidth
		base.WindowHeight = overlay.WindowHeight
	}
	if base.APIFormat == "" {
		base.APIFormat = apiFormatOpenAIChat
	}
	// 空值保持为空：base 的模型字段是请求级派生值，没有可展开的模型时就该
	// 缺席（调用方据此报 "model is required"），不能在这里补默认值。
	if base.ReasoningTag != "" {
		base.ReasoningTag = normalizeReasoningTag(base.ReasoningTag)
	}
	if base.ReasoningEffort != "" {
		base.ReasoningEffort = normalizeReasoningEffort(base.ReasoningEffort)
	}
	for i := range base.Models {
		base.Models[i].ReasoningTag = normalizeReasoningTag(base.Models[i].ReasoningTag)
		base.Models[i].ReasoningEffort = normalizeReasoningEffort(base.Models[i].ReasoningEffort)
		base.Models[i].CustomHeaders = normalizeCustomHeaders(base.Models[i].CustomHeaders)
		syncModelAPIKeyFields(&base.Models[i])
	}
	if goruntime.GOOS == "windows" {
		base.GitBashPath = resolveGitBashPath(base.GitBashPath)
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

// maxCustomHeaders caps the per-model custom header count so a pathological
// config cannot bloat every outbound request.
const maxCustomHeaders = 32

// managedHeaderNames 是 HTTP 传输层自管的头（连接语义、分帧、路由）。配置它们
// 会破坏请求本身，normalizeCustomHeaders 一律丢弃。
var managedHeaderNames = map[string]struct{}{
	"Host":                {},
	"Content-Length":      {},
	"Connection":          {},
	"Transfer-Encoding":   {},
	"Keep-Alive":          {},
	"Proxy-Authenticate":  {},
	"Proxy-Authorization": {},
	"Te":                  {},
	"Trailer":             {},
	"Upgrade":             {},
}

// normalizeCustomHeaders 清洗配置的自定义请求头：键值去除空白、丢弃空项与
// 传输层自管头（Host/Content-Length/Connection 等）、键按 net/http 规则归一化
// （x-api-version → X-Api-Version）并去重（大小写冲突时字典序首个胜出，行为
// 确定）、校验键值合法性（httpguts），最多保留 maxCustomHeaders 条。空结果
// 返回 nil，避免空 map 残留在配置里。它是自定义头的唯一归一化边界：
// mergeConfig 存储、适配器 transport 与请求构造点共用同一份语义。
func normalizeCustomHeaders(headers map[string]string) map[string]string {
	if len(headers) == 0 {
		return nil
	}
	keys := make([]string, 0, len(headers))
	for key := range headers {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make(map[string]string, len(headers))
	for _, raw := range keys {
		key := textproto.CanonicalMIMEHeaderKey(strings.TrimSpace(raw))
		value := strings.TrimSpace(headers[raw])
		if key == "" || value == "" {
			continue
		}
		if _, managed := managedHeaderNames[key]; managed {
			continue
		}
		if !httpguts.ValidHeaderFieldName(key) || !httpguts.ValidHeaderFieldValue(value) {
			continue
		}
		if _, exists := out[key]; exists {
			continue
		}
		if len(out) >= maxCustomHeaders {
			break
		}
		out[key] = value
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// customHeaderNames 返回自定义头键的有序列表，供脱敏视图（本地 API 服务）
// 展示“配置了哪些头”而不泄漏值。
func customHeaderNames(headers map[string]string) []string {
	normalized := normalizeCustomHeaders(headers)
	if normalized == nil {
		return []string{}
	}
	names := make([]string, 0, len(normalized))
	for key := range normalized {
		names = append(names, key)
	}
	sort.Strings(names)
	return names
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
	// GitHubToken 由保存路径显式写入（含清空）：mergeConfig 不再携带该字段，
	// 空 overlay 的 effectiveConfig 读取路径不会把它抹掉。
	a.config.GitHubToken = strings.TrimSpace(req.GitHubToken)
	// KBRoot 直接采用请求值（含清空），前端 draft 由 GetConfig 加载、整份回传。
	a.config.KBRoot = strings.TrimSpace(req.KBRoot)
	// nil 表示请求没有携带该字段（旧前端）：保留已加载的值，避免一次保存就把用户
	// 关掉的 SSRF 开关静默打开。该字段不放进 mergeConfig，请求级 overlay 不得改写。
	if req.AllowPrivateNetwork != nil {
		a.config.AllowPrivateNetwork = req.AllowPrivateNetwork
	}
	a.config.GitBashPath = req.GitBashPath
	a.config.ProxyMode = normalizeProxyMode(req.ProxyMode)
	a.config.ProxyURL = strings.TrimSpace(req.ProxyURL)
	a.config.ProxyNoProxy = strings.TrimSpace(req.ProxyNoProxy)
	a.config.UserAgent = strings.TrimSpace(req.UserAgent)
	if goruntime.GOOS == "windows" {
		a.config.GitBashPath = resolveGitBashPath(req.GitBashPath)
	}
	// 最近使用模型身份：nil 表示请求没携带该字段（旧前端 / API 调用），保留现值；
	// 非 nil 时按 models[] 收敛（provider 写法归一），解析不到（预设已删）就不采纳
	// ——残留的悬空身份由随后的 convergeLastUsedModel 一并清掉，两个写入方共用同一段
	// 收敛（界面在模型页删掉/改名那条模型后整份保存，磁盘上不能留一个展开落空的身份）。
	// 注意 JSON 的 `null` 也落在"没携带"这一支（前端整份保存永远显式带 null）：身份只由
	// "指向 models[] 里真实存在的那一条"定义，悬空身份统一由 convergeLastUsedModel 清掉，
	// 不需要（也没法）用 null 主动清除。
	if req.LastUsedModel != nil {
		if identity := normalizeModelIdentity(a.config.Models, *req.LastUsedModel); identity != nil {
			a.config.LastUsedModel = identity
		}
	}
	convergeLastUsedModel(&a.config)
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
	// 派生模型字段不落盘（同上）。
	stripModelFields(&a.config)
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
	// 同样走原子替换：设置页保存是最常被用户触碰的写入点，截断写中途崩溃会留下
	// 半截 JSON，下次启动只能整份回落默认值（见 ensureInitialized）。
	if err := writeAtomicBytes(path, data, 0o600); err != nil {
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
		MaxTokens:       model.MaxTokens,
		ContextWindow:   model.ContextWindow,
		TokenParam:      normalizeTokenParam(model.TokenParam),
		ReasoningTag:    normalizeReasoningTag(model.ReasoningTag),
		VisionCapable:   model.VisionCapable,
		ReasoningEffort: normalizeReasoningEffort(model.ReasoningEffort),
		ProxyMode:       networkCfg.ProxyMode,
		ProxyURL:        networkCfg.ProxyURL,
		ProxyNoProxy:    networkCfg.ProxyNoProxy,
		UserAgent:       networkCfg.UserAgent,
		CustomHeaders:   normalizeCustomHeaders(model.CustomHeaders),
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
	testMaxTokens := cfg.MaxTokens
	if testMaxTokens <= 0 {
		testMaxTokens = defaultMaxTokensForAPIFormat(cfg.APIFormat)
	}
	_, err := a.completeModelText(ctx, cfg, cfg.Model, []openai.ChatCompletionMessage{{
		Role:    openai.ChatMessageRoleUser,
		Content: "Reply only with OK.",
	}}, testMaxTokens)
	return err
}

// ── Effective config ─────────────────────────────────────────

func (a *App) effectiveConfig(overlay ConfigState) ConfigState {
	a.mu.Lock()
	base := a.config
	a.mu.Unlock()
	return mergeConfig(expandLastUsedModel(base), overlay)
}

// ── 最近使用模型（models[] 里的一条）─────────────────────────

// ModelIdentity 是 Models 里一条模型的身份：只记 providerName + model id，
// 不复制连接字段与密钥（否则又是第二份会漂移的模型配置）。
type ModelIdentity struct {
	ProviderName string `json:"providerName,omitempty"`
	Model        string `json:"model"`
}

func identityOfModel(m ModelConfig) ModelIdentity {
	return ModelIdentity{ProviderName: strings.TrimSpace(m.ProviderName), Model: strings.TrimSpace(m.Model)}
}

// migrateLegacyModelFields 把旧版 config.json 的顶层模型字段收敛成新形状：先按身份
// 到 models[] 里反查；反查不到（旧配置的 models[] 为空，或那条模型已被删）而顶层又
// 带着可用密钥时，先把顶层字段物化成一条 preset 再指向它。
//
// 物化这一步不能省：顶层字段只有两个写入方（/api/v1/models/activate，以及 2026-09
// 之前设置页的"使用"按钮），而 stripModelFields 紧跟着就会清空它们、随后任意一次
// 保存都以新形状落盘——不物化等于把用户的模型、地址与密钥一起静默抹掉（文件里没有
// 第二份，也没有备份）。没有密钥时不物化：那种顶层字段本身跑不通（发送必然报
// "API key is required"），凭空多一条空密钥的 preset 只会污染模型列表。
func migrateLegacyModelFields(cfg *ConfigState, legacy ConfigState) {
	identity := ModelIdentity{ProviderName: legacy.ProviderName, Model: legacy.Model}
	if strings.TrimSpace(identity.Model) == "" {
		return
	}
	if matched := normalizeModelIdentity(cfg.Models, identity); matched != nil {
		cfg.LastUsedModel = matched
		return
	}
	if len(resolveKeyPool(legacy)) == 0 {
		return
	}
	entry := modelEntryFromLegacyFields(legacy)
	cfg.Models = append(cfg.Models, entry)
	migrated := identityOfModel(entry)
	cfg.LastUsedModel = &migrated
}

// modelEntryFromLegacyFields 把旧版顶层模型字段整条搬成一条 preset（字段一对一，
// 密钥池与镜像字段按同一条规则同步）。
func modelEntryFromLegacyFields(legacy ConfigState) ModelConfig {
	entry := ModelConfig{
		ProviderName:    strings.TrimSpace(legacy.ProviderName),
		APIFormat:       normalizeAPIFormat(legacy.APIFormat),
		BaseURL:         strings.TrimSpace(legacy.BaseURL),
		APIKey:          strings.TrimSpace(legacy.APIKey),
		APIKeys:         cloneStringSlice(legacy.APIKeys),
		Model:           strings.TrimSpace(legacy.Model),
		MaxTokens:       legacy.MaxTokens,
		ContextWindow:   legacy.ContextWindow,
		TokenParam:      normalizeTokenParam(legacy.TokenParam),
		CustomHeaders:   normalizeCustomHeaders(legacy.CustomHeaders),
		ReasoningTag:    normalizeReasoningTag(legacy.ReasoningTag),
		ReasoningEffort: normalizeReasoningEffort(legacy.ReasoningEffort),
	}
	if legacy.VisionCapable != nil {
		vision := *legacy.VisionCapable
		entry.VisionCapable = &vision
	}
	syncModelAPIKeyFields(&entry)
	return entry
}

// modelIndexByIdentity 按身份定位 models[] 里的一条：先 provider + model 全匹配
// （不区分大小写、忽略首尾空格），失败再退一步只按 model id 匹配——用户改了
// provider 标签不该让"最近使用"失效。找不到返回 -1。
func modelIndexByIdentity(models []ModelConfig, id ModelIdentity) int {
	model := strings.TrimSpace(id.Model)
	if model == "" {
		return -1
	}
	provider := strings.TrimSpace(id.ProviderName)
	fallback := -1
	for i := range models {
		if !strings.EqualFold(strings.TrimSpace(models[i].Model), model) {
			continue
		}
		if provider == "" || strings.EqualFold(strings.TrimSpace(models[i].ProviderName), provider) {
			return i
		}
		if fallback < 0 {
			fallback = i
		}
	}
	return fallback
}

// normalizeModelIdentity 把身份收敛到 models[] 里真实存在的那一条（顺带归一
// provider 写法）；解析不到返回 nil，调用方保留原值。
func normalizeModelIdentity(models []ModelConfig, id ModelIdentity) *ModelIdentity {
	index := modelIndexByIdentity(models, id)
	if index < 0 {
		return nil
	}
	identity := identityOfModel(models[index])
	return &identity
}

// applyModelEntry 把一条模型配置展开到请求级模型字段上。SwitchModel 与
// expandLastUsedModel 共用这一处，避免两处各列一份字段表而漂移。
func applyModelEntry(cfg *ConfigState, m ModelConfig) {
	cfg.ProviderName = strings.TrimSpace(m.ProviderName)
	cfg.APIFormat = normalizeAPIFormat(m.APIFormat)
	cfg.BaseURL = strings.TrimSpace(m.BaseURL)
	cfg.APIKey = strings.TrimSpace(m.APIKey)
	cfg.APIKeys = cloneStringSlice(m.APIKeys)
	cfg.Model = strings.TrimSpace(m.Model)
	cfg.MaxTokens = m.MaxTokens
	// ContextWindow 为 0（旧配置没填）时保持 base 现值；消费方对 0 有自己的
	// 兜底（compactThresholdLimit / 前端显示都按模型条目里的值走）。
	if m.ContextWindow > 0 {
		cfg.ContextWindow = m.ContextWindow
	}
	cfg.TokenParam = m.TokenParam
	cfg.CustomHeaders = normalizeCustomHeaders(m.CustomHeaders)
	cfg.ReasoningTag = normalizeReasoningTag(m.ReasoningTag)
	cfg.VisionCapable = m.VisionCapable
	cfg.ReasoningEffort = normalizeReasoningEffort(m.ReasoningEffort)
	syncAPIKeyFields(cfg)
}

// expandLastUsedModel 返回把"最近使用模型"展开进模型字段后的配置。没有
// lastUsedModel（或身份已失效）时模型字段保持为空，调用方据此报
// "model is required"。不改动传入的 cfg。
func expandLastUsedModel(cfg ConfigState) ConfigState {
	if cfg.LastUsedModel == nil {
		return cfg
	}
	index := modelIndexByIdentity(cfg.Models, *cfg.LastUsedModel)
	if index < 0 {
		return cfg
	}
	applyModelEntry(&cfg, cfg.Models[index])
	return cfg
}

// stripModelFields 清空请求级模型字段：保证它们既不进 config.json，也不留在
// a.config 里（单一来源是 models[] + lastUsedModel）。
func stripModelFields(cfg *ConfigState) {
	cfg.ProviderName = ""
	cfg.APIFormat = ""
	cfg.BaseURL = ""
	cfg.APIKey = ""
	cfg.APIKeys = nil
	cfg.Model = ""
	cfg.MaxTokens = 0
	cfg.ContextWindow = 0
	cfg.TokenParam = ""
	cfg.CustomHeaders = nil
	cfg.ReasoningTag = ""
	cfg.VisionCapable = nil
	cfg.ReasoningEffort = ""
}

// convergeLastUsedModel 清掉在 models[] 里解析不到的悬空身份：这种身份展开永远落空
// （等价于"没有最近使用模型"），留着还会让界面把"最近使用"指着一条不存在的模型。
// config 有**两个**写入方——局部保存的 saveConfig 与设置页整份保存的 SaveConfig——
// 同一不变式的收敛必须收口在这里给两边共用：只在一边清理，另一边就会把悬空身份落盘，
// 于是 HTTP API 会话与计划任务回落直接报 "model is required"（界面自己因为还有 Tab
// 快照、下次切模型/发送又会重写身份，看不出问题）。
func convergeLastUsedModel(cfg *ConfigState) {
	if cfg.LastUsedModel != nil && modelIndexByIdentity(cfg.Models, *cfg.LastUsedModel) < 0 {
		cfg.LastUsedModel = nil
	}
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
