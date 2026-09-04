// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"

	"time"
	"unicode/utf8"

	"ally-dev/internal/tools/grep"
	toolshared "ally-dev/internal/tools/shared"

	openai "github.com/sashabaranov/go-openai"
)

const (
	appName                 = "Ally"
	defaultModel            = "deepseek-v4-flash"
	defaultBaseURL          = "https://api.deepseek.com"
	defaultReasoningTag     = "reasoning_content"
	maxReadFileBytes        = 32 * 1024 * 1024
	maxToolOutput           = 128 * 1024
	maxFinishedSubagents    = 50
	maxSubagentToolCalls    = 100
	maxModelToolOutput      = 12 * 1024
	maxModelWebOutput       = 96 * 1024
	maxCodeGraphPromptBytes = 96 * 1024
	modelToolHeadBytes      = 4 * 1024
	modelToolTailBytes      = 8 * 1024
	maxModelGrepMatches     = 200
	maxModelGrepSampleLines = 250
	maxModelGrepFileCounts  = 20
	maxAgentSteps           = 9999
	// runInputBufferSize is the per-run capacity of the injected-message queue
	// (InjectRunMessage). The buffered channel plus non-blocking drain keeps
	// injection off the chat hot path; a full queue fails the call instead of
	// blocking the frontend.
	runInputBufferSize               = 32
	defaultLLMRetries                = 6
	defaultShellLimit                = 120
	defaultHTTPTimeout               = 60
	defaultGrepTimeout               = grep.DefaultTimeout
	maxGrepTimeout                   = grep.MaxTimeout
	maxWaitSeconds                   = 3600
	maxHTTPBodyBytes                 = 50 * 1024 * 1024
	defaultHTTPMaxBody               = 256 * 1024
	defaultWebFetchBody              = 2 * 1024 * 1024
	maxHTTPJSONPreview               = 24 * 1024
	httpRateDelay                    = 1 * time.Second
	defaultHTTPUA                    = "AllyAgent/1.0 (+user-controlled desktop app)"
	workspaceMapDepth                = 3
	workspaceMapLimit                = 320
	workspaceMapTTL                  = 30 * time.Second
	workspacePathIndexTTL            = 10 * time.Minute
	workspacePathTruncatedRefreshTTL = 60 * time.Minute
	workspacePathIndexBuildTimeout   = 8 * time.Second
	workspacePathIndexMaxEntries     = 50000
	workspacePathSearchDefaultLimit  = 30
	workspacePathSearchMaxLimit      = 80
	memoryIndexLimit                 = 200
	maxAttachmentText                = 200 * 1024
	maxAttachmentDataURL             = 8 * 1024 * 1024
	// maxReadImageBytes caps image files that the read tool will inline as a
	// base64 data URL for multimodal model input. Base64 inflates by ~33%, and
	// Anthropic's per-image limit is 5MB of base64 data, so the raw cap is
	// 3.5MB (~4.7MB base64) to stay safe across all providers.
	maxReadImageBytes        = 3 * 1024 * 1024
	maxSavedHistoryTokens    = 256 * 1024
	maxSavedHistoryJSONBytes = 8 * 1024 * 1024
	// Background image storage. Bytes are written to
	// ~/.ally_agent/background.<ext> so config.json stays small; the
	// filename is stored in ConfigState.BackgroundImage.
	backgroundImageMaxBytes  = 12 * 1024 * 1024
	defaultBackgroundOpacity = 0.15
	// defaultCompactThreshold is the auto-compaction trigger as a fraction of
	// the context window (0.4 = 40%). Treated as "use default" when the
	// stored value is zero, so legacy config.json without the field migrates
	// to the new default transparently.
	defaultCompactThreshold = 0.4
	// defaultMessageFontSize is the default font size (px) for AI message
	// bodies and the welcome greeting. Zero means "use default" so legacy
	// config.json without the field migrates transparently.
	defaultMessageFontSize = 15.5
	// defaultCodeFontSize / defaultToolFontSize / defaultSubFontSize /
	// defaultAuxFontSize are the default UI font sizes (px) for code content,
	// tool cards, secondary text, and auxiliary text. Zero means "use default"
	// for each (legacy config migrates transparently).
	defaultCodeFontSize = 14
	defaultToolFontSize = 15
	defaultSubFontSize  = 13
	defaultAuxFontSize  = 12
)

// effectiveUserAgent returns the User-Agent string to send on outbound HTTP
// requests. If the user has configured a custom UA in settings, it is used
// as-is; otherwise the built-in default is returned. The result is never
// empty, so callers can safely assign it to a request header.
func effectiveUserAgent(cfg ConfigState) string {
	if ua := strings.TrimSpace(cfg.UserAgent); ua != "" {
		return ua
	}
	return defaultHTTPUA
}

// clampBackgroundOpacity normalizes the chat background opacity to [0, 1].
// A zero value (the JSON default when the field is absent) is replaced with
// the frontend default so legacy config without the field still shows a
// faint background once an image is uploaded.
func clampBackgroundOpacity(v float64) float64 {
	if v <= 0 {
		// Distinguish "field absent" (use default) from "user dragged to 0"
		// would require a pointer; the slider's min is 0.05 in the UI, so a
		// raw 0 here is treated as "not set" → default.
		return defaultBackgroundOpacity
	}
	if v > 1 {
		return 1
	}
	return v
}

// clampCompactThreshold normalizes the auto-compaction threshold. Zero (or
// out-of-range values) fall back to the default so legacy config.json and
// hand-edited junk both land on a sane value; otherwise the value is clamped
// to [0.2, 0.95] so the loop neither thrashes nor waits until the model
// hard-errors.
func clampCompactThreshold(v float64) float64 {
	if v <= 0 {
		return defaultCompactThreshold
	}
	if v < 0.2 {
		return 0.2
	}
	if v > 0.95 {
		return 0.95
	}
	return v
}

const (
	// defaultCompactTimeoutSeconds covers the common compaction summary call:
	// large history in, structured summary out, through a normally fast
	// provider.
	defaultCompactTimeoutSeconds = 180
	// maxCompactTimeoutSeconds lets slow long-context providers take up to an
	// hour instead of hard-failing with "context deadline exceeded".
	maxCompactTimeoutSeconds = 3600
)

// clampCompactTimeoutSeconds normalizes the compaction timeout. Zero (or
// out-of-range values) fall back to the default so legacy config.json and
// hand-edited junk both land on a sane value; otherwise the value is clamped
// to [30, 3600] seconds.
func clampCompactTimeoutSeconds(v int) int {
	if v <= 0 {
		return defaultCompactTimeoutSeconds
	}
	if v < 30 {
		return 30
	}
	if v > maxCompactTimeoutSeconds {
		return maxCompactTimeoutSeconds
	}
	return v
}

var (
	pythonRuntimeOnce sync.Once
	pythonRuntimeLine string
)

type workspaceMapCacheEntry struct {
	content     string
	generatedAt time.Time
}

// gitStatusCacheEntry caches a GitStatus result with a short TTL so that
// rapid consecutive GetGitStatus calls (workspace switch + run:done) don't
// each spawn 2 git subprocesses.
type gitStatusCacheEntry struct {
	status      GitStatus
	generatedAt time.Time
}

// contextStaticCacheEntry caches a ContextBreakdown keyed by config+skills+version.
type contextStaticCacheEntry struct {
	breakdown   ContextBreakdown
	generatedAt time.Time
}

type skillListCacheEntry struct {
	skills      []SkillDefinition
	generatedAt time.Time
}

// App is the Wails-bound application module.
type App struct {
	ctx    context.Context
	events eventSink

	// wails is the desktop host adapter injected by SetApp/SetWindow; nil in
	// tests and headless embeddings. Concrete Wails v3 types live only in
	// host_desktop.go so core Agent code never imports the Wails runtime.
	wails *wailsAppHandle

	// notifier is the desktop notifications service injected by SetNotifier
	// (host_notifications.go); nil in tests and headless embeddings.
	notifier               completionNotifier
	lastCompletionNotifyAt time.Time

	mu          sync.Mutex
	config      ConfigState
	configPath  string
	runs        map[string]context.CancelFunc
	runSessions map[string]string
	// runInputs queues user messages injected into a live run (runID → buffered
	// channel). runChat drains it at the top of every agent step so injected
	// messages enter the model context only after the current tool batch
	// completes, and they are persisted with the rest of the run history.
	runInputs map[string]chan string
	// compactingSessions holds the session IDs with a compaction LLM call in
	// flight. Compaction is a session-scoped operation, so the composer run
	// indicator and the /compact guard must be keyed per session instead of
	// being process-global state.
	compactingSessions map[string]struct{}
	// compactingCancels holds the cancel function of each in-flight manual
	// compaction so ESC (CancelCompaction) can abort the summary LLM call
	// instead of waiting out the full timeout.
	compactingCancels map[string]context.CancelFunc
	historiesDir      string
	sessionsDir       string
	histories         map[string][]openai.ChatCompletionMessage
	sessionMu         sync.Mutex
	initialized       bool
	disabledSkills    []string
	mcpManager        *McpManager
	todos             map[string][]TodoEntry // sessionID → todos
	todoRevisions     map[string]int64
	// sessionWorkspaceMaps freezes the workspace map bytes per session
	// (sessionID → map text) so the request prefix stays byte-stable across
	// runs and provider prompt caches survive; guarded by mu (declared in
	// biz_workspace.go with the workspace cache fields).
	sessionWorkspaceMaps map[string]string

	askMu       sync.Mutex
	pendingAsks map[string]*pendingAsk

	// sshCredentials holds chat-provided SSH passwords in memory only (never
	// persisted). Keys are lowercase user@host; see orch_ssh_credential.go.
	sshCredentials *sshCredentialCache

	subRuns   map[string]*SubagentRun // subId → run
	subRunsMu sync.Mutex
	subSem    chan struct{} // concurrency limiter (cap 4)
	fileOpsMu sync.Mutex    // serializes write ops (edit, create, delete) to prevent lost updates

	gitDiffMu     sync.Mutex
	gitDiffCancel context.CancelFunc
	gitDiffRunID  int64

	// updateMu guards the in-progress self-update download cancel handle so
	// CancelUpdate can abort a running DownloadUpdate.
	updateMu     sync.Mutex
	updateCancel context.CancelFunc
	// updateCancelPending records a cancel request that arrived before the
	// download registered its handle, so DownloadUpdate aborts right after
	// registering instead of proceeding with the download.
	updateCancelPending bool

	// gitStatusCache memoizes the last GetGitStatus result for a short TTL
	// so that rapid consecutive calls (e.g. workspace switch + run:done)
	// don't each spawn 2 git subprocesses. Keyed by workspace path.
	gitStatusCacheMu  sync.Mutex
	gitStatusCache    map[string]gitStatusCacheEntry
	gitStatusInFlight map[string]chan struct{}

	skillCacheMu sync.Mutex
	skillCache   map[string]skillListCacheEntry

	// workspaceCaches owns all workspace-map / path-index memoization state
	// (TTL, version, in-flight rebuilds). The concrete type lives in
	// biz_workspace.go next to the cache logic, so this struct stays slim.
	workspaceCaches *workspaceCacheHolder

	// contextStaticCaches owns the ContextBreakdown memoization and its
	// invalidation version. Concrete type lives in biz_context.go with the
	// breakdown logic.
	contextStaticCaches *contextStaticCacheHolder

	httpRateMu   sync.Mutex
	httpLastHost map[string]time.Time

	// liveBreakdown caches the full token-count breakdown for active sessions.
	// Updated by runChat each agent step. Maps sessionID → ContextBreakdown.
	liveBreakdown map[string]ContextBreakdown

	// workspaceTokenUsage accumulates model-reported usage for this app run.
	// Maps normalized workspace path → WorkspaceTokenUsage.
	workspaceTokenUsage map[string]WorkspaceTokenUsage
	taskbarMu           sync.Mutex
	taskbarActiveRuns   int

	servicesMu    sync.Mutex
	services      map[string]*managedService
	finishedQueue []string // finished services, oldest first, capped at maxFinishedServices

	keyStateMu   sync.Mutex
	keyCooldowns map[string]time.Time // endpoint\x00key → cooldown until

	scheduledMu sync.Mutex
	scheduled   *scheduledTaskManager

	// lastEstimatedTokens is retained for ResetWorkspaceTokenUsage cleanup;
	// recordWorkspaceTokenUsage no longer uses fallback delta logic.
	lastEstimatedTokens map[string]WorkspaceTokenUsage

	// stats asynchronously records LLM token usage for the stats dashboard.
	// Its bounded non-blocking queue keeps telemetry off the chat hot path;
	// persistence runs on its own goroutine.
	stats *statsRecorder

	trayIconMu sync.Mutex
	trayIcon   []byte
}

func NewApp() *App {
	a := &App{
		runs:                map[string]context.CancelFunc{},
		runSessions:         map[string]string{},
		runInputs:           map[string]chan string{},
		compactingSessions:  map[string]struct{}{},
		compactingCancels:   map[string]context.CancelFunc{},
		histories:           map[string][]openai.ChatCompletionMessage{},
		todos:               map[string][]TodoEntry{},
		todoRevisions:       map[string]int64{},
		pendingAsks:         map[string]*pendingAsk{},
		sshCredentials:      newSSHCredentialCache(),
		subRuns:             map[string]*SubagentRun{},
		subSem:              make(chan struct{}, 4),
		gitStatusCache:      map[string]gitStatusCacheEntry{},
		gitStatusInFlight:   map[string]chan struct{}{},
		skillCache:          map[string]skillListCacheEntry{},
		workspaceCaches:     newWorkspaceCacheHolder(),
		contextStaticCaches: newContextStaticCacheHolder(),
		httpLastHost:        map[string]time.Time{},
		liveBreakdown:       map[string]ContextBreakdown{},
		workspaceTokenUsage: map[string]WorkspaceTokenUsage{},
		services:            map[string]*managedService{},
		keyCooldowns:        map[string]time.Time{},
		lastEstimatedTokens: map[string]WorkspaceTokenUsage{},
		stats:               newStatsRecorder(),
	}
	// Expose the active App to package-level helpers that predate Runtime
	// injection (listMemories, memoryIndexCache usage in prompt_builder).
	// Only one App instance exists per process; there is no teardown.
	aGlobalApp = a
	return a
}

func clampInt(value, minValue, maxValue int) int {
	if maxValue < minValue {
		return maxValue
	}
	if value < minValue {
		return minValue
	}
	if value > maxValue {
		return maxValue
	}
	return value
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// ConfigState stores provider, workspace and runtime settings.
type ModelConfig struct {
	ProviderName string `json:"providerName"`
	APIFormat    string `json:"apiFormat"`
	BaseURL      string `json:"baseUrl"`
	APIKey       string `json:"apiKey"`
	// APIKeys is the ordered API key pool for this model. The first key has
	// the highest priority: it is used while it works; when it fails, the
	// next key takes over. APIKey stays in sync with the first entry for
	// backward compatibility with older configs and frontends.
	APIKeys       []string `json:"apiKeys,omitempty"`
	Model         string   `json:"model"`
	MaxTokens     int      `json:"maxTokens"`
	ContextWindow int      `json:"contextWindow"`
	ReasoningTag  string   `json:"reasoningTag,omitempty"`
	// TokenParam selects which token-limit field the OpenAI Chat adapter
	// sends: "auto"/"max_tokens" -> max_tokens (broadest compatibility),
	// "max_completion_tokens" -> max_completion_tokens (official OpenAI
	// o-series / newer GPT models that reject the legacy field). Ignored by
	// the Responses and Anthropic adapters.
	TokenParam string `json:"tokenParam,omitempty"`
	// ReasoningEffort selects the thinking-strength level sent to the
	// provider: "" / "auto" sends nothing and keeps the provider default;
	// otherwise "low"/"medium"/"high"/"xhigh"/"max" is sent unchanged
	// through the adapter (OpenAI reasoning_effort / reasoning.effort or
	// Anthropic output_config.effort without enabling thinking blocks).
	// "auto" is the safe default because not every model accepts the
	// parameter and value sets differ across providers.
	ReasoningEffort string `json:"reasoningEffort,omitempty"`
}

type ConfigState struct {
	ProviderName string `json:"providerName"`
	APIFormat    string `json:"apiFormat"`
	BaseURL      string `json:"baseUrl"`
	APIKey       string `json:"apiKey"`
	// APIKeys is the ordered API key pool; see ModelConfig.APIKeys.
	APIKeys    []string `json:"apiKeys,omitempty"`
	Model      string   `json:"model"`
	Workspace  string   `json:"workspace"`
	ExtraRoots []string `json:"extraRoots,omitempty"`
	// KBRoot is the user-configured knowledge-base root directory. When a run
	// resolves its workspace to this path (SamePath), the session runs in
	// knowledge-base mode: the KB system-prompt part is injected and the
	// sources/ subdirectory becomes read-only for model tools.
	KBRoot              string        `json:"kbRoot,omitempty"`
	MaxTokens           int           `json:"maxTokens"`
	ContextWindow       int           `json:"contextWindow"`
	TokenParam          string        `json:"tokenParam,omitempty"`
	CustomPrompt        string        `json:"customPrompt"`
	AllowPrivateNetwork bool          `json:"allowPrivateNetwork"`
	GitBashPath         string        `json:"gitBashPath"`
	ProxyMode           string        `json:"proxyMode,omitempty"`
	ProxyURL            string        `json:"proxyUrl,omitempty"`
	ProxyNoProxy        string        `json:"proxyNoProxy,omitempty"`
	UserAgent           string        `json:"userAgent,omitempty"`
	ReasoningTag        string        `json:"reasoningTag,omitempty"`
	ReasoningEffort     string        `json:"reasoningEffort,omitempty"`
	Models              []ModelConfig `json:"models,omitempty"`
	DisabledSkills      []string      `json:"disabledSkills,omitempty"`
	LLMRetries          int           `json:"llmRetries,omitempty"`
	// AutoValidation* are nil for legacy configs (treated as disabled).
	// Post-write checks only run for languages the user explicitly enabled
	// in Settings.
	AutoValidationPython     *bool `json:"autoValidationPython,omitempty"`
	AutoValidationGo         *bool `json:"autoValidationGo,omitempty"`
	AutoValidationJavaScript *bool `json:"autoValidationJavaScript,omitempty"`
	AutoValidationTypeScript *bool `json:"autoValidationTypeScript,omitempty"`
	AutoValidationVue        *bool `json:"autoValidationVue,omitempty"`
	AutoValidationJava       *bool `json:"autoValidationJava,omitempty"`
	AutoValidationJSON       *bool `json:"autoValidationJson,omitempty"`
	// AutoUpdate is a pointer so that an absent field in legacy config.json
	// is treated as "default on" rather than "off". Only an explicit false
	// disables automatic background downloads.
	AutoUpdate *bool `json:"autoUpdate,omitempty"`
	// SkippedUpdates records release tags the user chose to skip. They are
	// excluded from automatic download until the user clears them.
	SkippedUpdates []string `json:"skippedUpdates,omitempty"`
	// GitHubToken is an optional personal access token used to raise the
	// GitHub API rate limit from 60/hour (anonymous) to 5000/hour when
	// checking for updates and downloading release assets. Reading public
	// repo releases needs no scope, so a no-scope classic token works.
	GitHubToken string `json:"githubToken,omitempty"`
	// BackgroundImage is the filename of the user-uploaded chat background
	// image stored under ~/.ally_agent/. Empty means no custom background.
	// The actual bytes live on disk so config.json stays small; the frontend
	// resolves it to a file:// URL via GetBackgroundImageURL.
	BackgroundImage string `json:"backgroundImage,omitempty"`
	// BackgroundOpacity controls how strongly the custom background image
	// shows through the chat area. 0 = invisible, 1 = fully opaque. Clamped
	// to [0, 1] on save; the frontend default is 0.15 (faint silhouette).
	BackgroundOpacity float64 `json:"backgroundOpacity,omitempty"`
	// CloseToTray is a pointer so an absent field in legacy config.json is
	// treated as "default on" (closing the window hides to the system tray).
	// Only an explicit false makes closing the window quit the app.
	CloseToTray *bool `json:"closeToTray,omitempty"`
	// WindowWidth / WindowHeight remember the user's manual window size.
	// Zero means "no saved size yet": the window starts at a golden-ratio
	// share of the primary screen (61.8% x 61.8%) and the size is saved
	// after the first user-driven resize.
	WindowWidth  int `json:"windowWidth,omitempty"`
	WindowHeight int `json:"windowHeight,omitempty"`
	// CompactThreshold is the context-usage fraction (0..1) at which the
	// chat loop auto-compacts history. Zero means "use default" so legacy
	// configs migrate transparently; the effective value is exposed via
	// effectiveCompactThreshold().
	CompactThreshold float64 `json:"compactThreshold,omitempty"`
	// CompactTimeoutSeconds bounds the compaction LLM call (summary request).
	// Zero means "use default" so legacy configs migrate transparently; the
	// effective value is exposed via clampCompactTimeoutSeconds().
	CompactTimeoutSeconds int `json:"compactTimeoutSeconds,omitempty"`
	// MessageFontSize is the AI message body / welcome greeting font size in
	// px. Zero means "use default" (15.5); the effective value is exposed via
	// clampMessageFontSize and applied by the frontend as a CSS variable.
	MessageFontSize float64 `json:"messageFontSize,omitempty"`
	// CodeFontSize / ToolFontSize / SubFontSize / AuxFontSize are the UI
	// font sizes (px) for code content, tool cards, secondary text, and
	// auxiliary text. Zero means "use default"; the frontend applies them
	CodeFontSize float64 `json:"codeFontSize,omitempty"`
	ToolFontSize float64 `json:"toolFontSize,omitempty"`
	SubFontSize  float64 `json:"subFontSize,omitempty"`
	AuxFontSize  float64 `json:"auxFontSize,omitempty"`
	// noAdapterRetry 是进程内非序列化标记:多 key 模式下置 true,让适配器
	// 内部关闭退避重试,由 streamModelResponse 的外层循环统一承担重试与
	// 故障切换,避免 N 个 key × 适配器重试组合爆炸。
	noAdapterRetry          bool
	responsesPromptCacheKey string // nonserialized, session-local OpenAI Responses cache route
}

// clampMessageFontSize normalizes the message font size (px). Zero (field
// absent in legacy config) falls back to the default; values outside the
// readable range are clamped to [12, 24].
func clampMessageFontSize(v float64) float64 {
	if v <= 0 {
		return defaultMessageFontSize
	}
	if v < 12 {
		return 12
	}
	if v > 24 {
		return 24
	}
	return v
}

// clampFontSize normalizes a UI font size (px): zero (field absent in legacy
// config) falls back to def, then clamps to [min, max].
func clampFontSize(v, def, min, max float64) float64 {
	if v <= 0 {
		return def
	}
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

// autoUpdateEnabled returns true unless AutoUpdate was explicitly set to false.
// Legacy config.json without the field defaults to enabled.
func (c ConfigState) autoUpdateEnabled() bool {
	if c.AutoUpdate != nil {
		return *c.AutoUpdate
	}
	return true
}

// closeToTrayEnabled returns true unless CloseToTray was explicitly set to
// false. Legacy config without the field defaults to hide-to-tray.
func (c ConfigState) closeToTrayEnabled() bool {
	if c.CloseToTray != nil {
		return *c.CloseToTray
	}
	return true
}

type ToolDefinitionSummary struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Source      string `json:"source"`
	Server      string `json:"server,omitempty"`
}

type ChatMessageInput struct {
	Role        string            `json:"role"`
	Content     string            `json:"content"`
	Attachments []AttachmentInput `json:"attachments,omitempty"`
}

type AttachmentInput struct {
	ID        string `json:"id,omitempty"`
	Name      string `json:"name"`
	Type      string `json:"type,omitempty"`
	Size      int64  `json:"size,omitempty"`
	Kind      string `json:"kind,omitempty"`
	DataURL   string `json:"dataUrl,omitempty"`
	Text      string `json:"text,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
	Error     string `json:"error,omitempty"`
	FilePath  string `json:"filePath,omitempty"`
}

type ChatRequest struct {
	SessionID   string             `json:"sessionId"`
	Message     string             `json:"message"`
	Messages    []ChatMessageInput `json:"messages"`
	Attachments []AttachmentInput  `json:"attachments,omitempty"`
	Config      ConfigState        `json:"config"`
}

type ListFilesRequest struct {
	// Workspace is used by the UI explorer to pin the request to a Tab's
	// workspace. Model-facing list_files calls leave it empty and use the
	// active runtime configuration.
	Workspace      string `json:"workspace,omitempty"`
	Path           string `json:"path"`
	MaxDepth       int    `json:"maxDepth"`
	Limit          int    `json:"limit"`
	IncludeHidden  bool   `json:"includeHidden"`
	IncludeIgnored bool   `json:"includeIgnored"`
	// ModelFacing marks requests originating from the model's list_files
	// tool call; the UI explorer leaves it false. Model-facing listings
	// always skip VCS internals (.git/.svn/.hg) even when includeHidden or
	// includeIgnored are set — they are pure noise for the model. json:"-"
	// keeps the model from forging it via extra JSON fields.
	ModelFacing bool `json:"-"`
}

type WorkspacePathRequest struct {
	Workspace string `json:"workspace"`
	Path      string `json:"path"`
}

type FileEntry struct {
	Path    string `json:"path"`
	Name    string `json:"name"`
	Dir     bool   `json:"dir"`
	Size    int64  `json:"size"`
	ModTime string `json:"modTime"`
	// Symlink marks symlink entries. WalkDir never follows links, so these
	// render as leaves; the UI uses the flag to label them honestly.
	Symlink bool `json:"symlink,omitempty"`
	// MoreFiles marks the per-directory overflow placeholder of model-facing
	// listings (mirroring the workspace map): the entry renders as "+N more
	// files" and its Path is a non-resolvable "parent/+more" marker. Only
	// set when ModelFacing is true; the UI explorer never sees it.
	MoreFiles int `json:"moreFiles,omitempty"`
}

type ListFilesResult struct {
	Entries   []FileEntry `json:"entries"`
	Count     int         `json:"count"`
	Truncated bool        `json:"truncated"`
}

type WorkspacePathSearchRequest struct {
	// Workspace pins the search root. UI workspace explorers pass the active
	// Tab's path so a knowledge-base Tab searches the KB root, not the chat
	// workspace. An empty value preserves the legacy active-config behavior.
	Workspace string `json:"workspace,omitempty"`
	Query     string `json:"query"`
	Limit     int    `json:"limit"`
	Force     bool   `json:"force"`
}

type WorkspacePathEntry struct {
	Path string `json:"path"`
	Name string `json:"name"`
	Dir  bool   `json:"dir"`
}

type WorkspacePathSearchResult struct {
	Entries       []WorkspacePathEntry `json:"entries"`
	Count         int                  `json:"count"`
	Total         int                  `json:"total"`
	Truncated     bool                 `json:"truncated"`
	IndexVersion  int64                `json:"indexVersion"`
	IndexedAt     string               `json:"indexedAt"`
	BuildDuration int64                `json:"buildDurationMs"`
	Source        string               `json:"source"`
}

type ReadFileRequest struct {
	Path          string `json:"path"`
	StartLine     int    `json:"startLine"`
	EndLine       int    `json:"endLine,omitempty"`
	LineCount     int    `json:"lineCount"`
	ContextBefore int    `json:"contextBefore,omitempty"`
	ContextAfter  int    `json:"contextAfter,omitempty"`
}

type ReadFileResult struct {
	Path                  string   `json:"path"`
	Content               string   `json:"content"`
	RawContent            string   `json:"-"`
	Text                  string   `json:"text,omitempty"`
	Kind                  string   `json:"kind,omitempty"`
	ContentFormat         string   `json:"contentFormat,omitempty"`
	Type                  string   `json:"type,omitempty"`
	Editable              bool     `json:"editable"`
	StartLine             int      `json:"startLine"`
	EndLine               int      `json:"endLine"`
	NextStartLine         int      `json:"nextStartLine,omitempty"`
	TotalLines            int      `json:"totalLines"`
	SHA256                string   `json:"sha256"`
	Version               string   `json:"version"`
	Size                  int64    `json:"size"`
	LineEnding            string   `json:"lineEnding"`
	Truncated             bool     `json:"truncated"`
	TruncatedLines        []int    `json:"truncatedLines,omitempty"`
	TruncatedLinesOmitted bool     `json:"truncatedLinesOmitted,omitempty"`
	RangeStatus           string   `json:"rangeStatus,omitempty"`
	EmptyRange            bool     `json:"emptyRange,omitempty"`
	Sheets                []string `json:"sheets,omitempty"`
	// DataURL is set only for image files: a data:image/<mime>;base64,... URL
	// used to inject the picture into multimodal model context.
	DataURL string `json:"dataUrl,omitempty"`
}

type ReplaceExactRequest struct {
	Path           string `json:"path"`
	ExpectedSHA256 string `json:"expectedSha256"`
	OldString      string `json:"oldString"`
	NewString      string `json:"newString"`
	ReplaceAll     bool   `json:"replaceAll"`
}

type ReplaceLinesRequest struct {
	Path           string `json:"path"`
	ExpectedSHA256 string `json:"expectedSha256"`
	StartLine      int    `json:"startLine"`
	EndLine        int    `json:"endLine"`
	NewText        string `json:"newText"`
}

type CreateFileRequest struct {
	Workspace string `json:"workspace,omitempty"`
	Path      string `json:"path"`
	Content   string `json:"content"`
	Overwrite bool   `json:"overwrite"`
}

type CreateDirectoryRequest struct {
	Workspace string `json:"workspace,omitempty"`
	Path      string `json:"path"`
}

// CopyFilesIntoWorkspaceRequest is the UI drag-and-drop copy payload: absolute
// source paths from the native file-drop event plus a workspace-relative
// destination directory ("" = workspace root).
type CopyFilesIntoWorkspaceRequest struct {
	Workspace string   `json:"workspace,omitempty"`
	TargetDir string   `json:"targetDir,omitempty"`
	Sources   []string `json:"sources"`
}

type CopyFilesIntoWorkspaceResult struct {
	TargetDir string            `json:"targetDir"`
	Copied    []string          `json:"copied"`
	Failed    []CopyFileFailure `json:"failed,omitempty"`
}

type CopyFileFailure struct {
	Source string `json:"source"`
	Error  string `json:"error"`
}

type DeletePathRequest struct {
	Workspace string `json:"workspace,omitempty"`
	Path      string `json:"path"`
	Recursive bool   `json:"recursive"`
}

// MovePathRequest moves a file or directory from Source to Destination
// within the workspace. Both paths are workspace-relative.
type MovePathRequest struct {
	Source      string `json:"source"`
	Destination string `json:"destination"`
	Overwrite   bool   `json:"overwrite"`
}

// MovePathResult reports the resolved absolute paths after a move.
type MovePathResult struct {
	Source      string `json:"source"`
	Destination string `json:"destination"`
	Moved       bool   `json:"moved"`
}

type DeleteResult struct {
	Deleted      string `json:"deleted"`
	Path         string `json:"path"`
	ResolvedPath string `json:"resolvedPath,omitempty"`
	Kind         string `json:"kind"`
	Recursive    bool   `json:"recursive"`
	RemovedFiles int    `json:"removedFiles"`
	RemovedDirs  int    `json:"removedDirs"`
	RemovedBytes int64  `json:"removedBytes"`
	WasSymlink   bool   `json:"wasSymlink"`
}

type CommandRequest struct {
	Command string `json:"command"`
	Cwd     string `json:"cwd"`
	Timeout int    `json:"timeout"`
	// FullOutput 由模型在调用时显式传入：默认模型侧只收到尾部几行 +
	// exitCode（对齐 UI 折叠卡片），传 true 才内联返回完整输出。
	// UI 始终收到完整输出，不受该参数影响。
	FullOutput bool `json:"fullOutput,omitempty"`
}

type StartServiceRequest struct {
	Name    string `json:"name,omitempty"`
	Command string `json:"command"`
	Cwd     string `json:"cwd,omitempty"`
}

type StopServiceRequest struct {
	ID string `json:"id"`
	// GraceSeconds bounds the graceful-termination wait before the process
	// tree is force killed. <=0 uses the default; values above the cap clamp.
	GraceSeconds int `json:"graceSeconds,omitempty"`
}

type BackgroundProcessRequest struct {
	Action       string `json:"action"`
	Name         string `json:"name,omitempty"`
	Command      string `json:"command,omitempty"`
	Cwd          string `json:"cwd,omitempty"`
	ID           string `json:"id,omitempty"`
	TailBytes    int    `json:"tailBytes,omitempty"`
	GraceSeconds int    `json:"graceSeconds,omitempty"`
}

// ServiceReadRequest is the model-facing read payload for the service tool.
type ServiceReadRequest struct {
	ID        string `json:"id"`
	TailBytes int    `json:"tailBytes,omitempty"`
}

type WaitRequest struct {
	Seconds int    `json:"seconds"`
	Reason  string `json:"reason"`
}

type WaitResult struct {
	RequestedSeconds int    `json:"requestedSeconds"`
	ElapsedMS        int64  `json:"elapsedMs"`
	Reason           string `json:"reason"`
	Completed        bool   `json:"completed"`
}

type AskOption struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Description string `json:"description"`
	Recommended bool   `json:"recommended"`
}

type AskQuestion struct {
	ID       string      `json:"id"`
	Question string      `json:"question"`
	Options  []AskOption `json:"options"`
}

type AskRequest struct {
	Questions []AskQuestion `json:"questions"`
}

type AskSubmittedAnswer struct {
	QuestionID        string   `json:"questionId"`
	SelectedOptionIDs []string `json:"selectedOptionIds"`
	CustomText        string   `json:"customText,omitempty"`
}

type AskSubmitRequest struct {
	AskID     string               `json:"askId"`
	SessionID string               `json:"sessionId"`
	Answers   []AskSubmittedAnswer `json:"answers"`
}

type AskResolvedSelection struct {
	OptionID    string `json:"optionId,omitempty"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
	Recommended bool   `json:"recommended,omitempty"`
	Custom      bool   `json:"custom,omitempty"`
}

type AskResolvedAnswer struct {
	QuestionID string                 `json:"questionId"`
	Question   string                 `json:"question"`
	Selections []AskResolvedSelection `json:"selections"`
}

type AskResult struct {
	AskID   string              `json:"askId"`
	Answers []AskResolvedAnswer `json:"answers"`
}

type pendingAsk struct {
	sessionID string
	request   AskRequest
	answers   chan AskResult
	mu        sync.Mutex
	cancelled bool
}

type toolExecutionMeta struct {
	runID         string
	sessionID     string
	toolBatchID   string
	toolCallIndex int
	toolCallID    string
	toolName      string
	toolArgs      string
}

type toolExecutionMetaContextKey struct{}
type runReadCacheContextKey struct{}

type ServiceInfo struct {
	ID              string `json:"id"`
	Name            string `json:"name,omitempty"`
	Command         string `json:"command"`
	Cwd             string `json:"cwd"`
	PID             int    `json:"pid"`
	Status          string `json:"status"`
	StartedAt       int64  `json:"startedAt"`
	StoppedAt       int64  `json:"stoppedAt,omitempty"`
	ExitCode        int    `json:"exitCode,omitempty"`
	OutputTail      string `json:"outputTail,omitempty"`
	OutputBytes     int64  `json:"outputBytes,omitempty"`
	OutputTruncated bool   `json:"outputTruncated,omitempty"`
	Error           string `json:"error,omitempty"`
}

type ServiceListResult struct {
	Services []ServiceInfo `json:"services"`
}

type ServiceOutputResult struct {
	ID        string `json:"id"`
	Output    string `json:"output"`
	Bytes     int64  `json:"bytes"`
	Truncated bool   `json:"truncated"`
}

type CommandResult struct {
	Command        string `json:"command"`
	Cwd            string `json:"cwd"`
	Shell          string `json:"shell"`
	ShellPath      string `json:"shellPath"`
	Output         string `json:"output"`
	OutputFilePath string `json:"outputFilePath,omitempty"`
	// OutputFileBytes 是截断落盘文件的字节体积，让模型在读全量前能
	// 自行判断是否分段读取。仅 OutputFilePath 非空时有意义。
	OutputFileBytes int64 `json:"outputFileBytes,omitempty"`
	ExitCode        int   `json:"exitCode"`
	TimedOut        bool  `json:"timedOut"`
	Cancelled       bool  `json:"cancelled"`
	DurationMS      int64 `json:"durationMs"`
	Truncated       bool  `json:"truncated"`
	// FullOutput 记录模型是否通过 fullOutput 参数请求了完整输出。
	// 只影响模型侧序列化（见 infra_result.go），不影响 UI。
	FullOutput bool `json:"fullOutput,omitempty"`
}

type HTTPRequestToolRequest struct {
	Method              string            `json:"method"`
	URL                 string            `json:"url"`
	Headers             map[string]string `json:"headers,omitempty"`
	Query               map[string]string `json:"query,omitempty"`
	Body                string            `json:"body,omitempty"`
	JSON                any               `json:"json,omitempty"`
	SaveTo              string            `json:"saveTo,omitempty"`
	Timeout             int               `json:"timeout,omitempty"`
	MaxBytes            int               `json:"maxBytes,omitempty"`
	FollowRedirects     *bool             `json:"followRedirects,omitempty"`
	AllowPrivateNetwork *bool             `json:"allowPrivateNetwork,omitempty"`
	InsecureSkipVerify  *bool             `json:"insecureSkipVerify,omitempty"`
}

type HTTPRequestToolResult struct {
	Method        string            `json:"method"`
	URL           string            `json:"url"`
	FinalURL      string            `json:"finalUrl"`
	Status        int               `json:"status"`
	StatusText    string            `json:"statusText"`
	Headers       map[string]string `json:"headers"`
	ContentType   string            `json:"contentType"`
	Body          string            `json:"body,omitempty"`
	BodyBase64    string            `json:"bodyBase64,omitempty"`
	BodyEncoding  string            `json:"bodyEncoding"`
	JSON          any               `json:"json,omitempty"`
	JSONPreview   string            `json:"jsonPreview,omitempty"`
	JSONTruncated bool              `json:"jsonTruncated,omitempty"`
	BytesRead     int               `json:"bytesRead"`
	Truncated     bool              `json:"truncated"`
	DurationMS    int64             `json:"durationMs"`
	Redirects     []string          `json:"redirects,omitempty"`
	SavedPath     string            `json:"savedPath,omitempty"`
}

type WebFetchRequest struct {
	URL                 string            `json:"url"`
	Format              string            `json:"format,omitempty"`
	Headers             map[string]string `json:"headers,omitempty"`
	Timeout             int               `json:"timeout,omitempty"`
	MaxBytes            int               `json:"maxBytes,omitempty"`
	MaxChars            int               `json:"maxChars,omitempty"`
	AllowPrivateNetwork *bool             `json:"allowPrivateNetwork,omitempty"`
	InsecureSkipVerify  *bool             `json:"insecureSkipVerify,omitempty"`
}

type WebFetchLink struct {
	Text string `json:"text"`
	URL  string `json:"url"`
}

type WebFetchResult struct {
	URL         string         `json:"url"`
	FinalURL    string         `json:"finalUrl"`
	Status      int            `json:"status"`
	StatusText  string         `json:"statusText"`
	Title       string         `json:"title,omitempty"`
	Text        string         `json:"text"`
	ContentType string         `json:"contentType"`
	Links       []WebFetchLink `json:"links,omitempty"`
	BytesRead   int            `json:"bytesRead"`
	Truncated   bool           `json:"truncated"`
	DurationMS  int64          `json:"durationMs"`
}

type RemoteReadFileRequest struct {
	Target string                 `json:"target"`
	Files  []BatchReadFileRequest `json:"files"`
	// Compatibility fields for legacy single-file reads
	Path      string `json:"path,omitempty"`
	StartLine int    `json:"startLine,omitempty"`
	EndLine   int    `json:"endLine,omitempty"`
}

// RemoteEditRequest is the flat model-facing remote_edit request: exactly one
// file per call, path/version/changes at the top level under the SSH target —
// the same shape local edit uses, prefixed with `target`. Multi-file changes
// are parallel remote_edit calls in one model response.
type RemoteEditRequest struct {
	Target  string       `json:"target"`
	Path    string       `json:"path"`
	Version string       `json:"version"`
	Changes []TextChange `json:"changes"`
}

func (req RemoteEditRequest) file() FileTextEdits {
	return FileTextEdits{Path: req.Path, Version: req.Version, Changes: req.Changes}
}

type RemoteCreateFileRequest struct {
	Target    string `json:"target"`
	Path      string `json:"path"`
	Content   string `json:"content"`
	Overwrite bool   `json:"overwrite"`
	Mkdirs    bool   `json:"-"`
}

type RemoteDeletePathRequest struct {
	Target    string `json:"target"`
	Path      string `json:"path"`
	Recursive bool   `json:"recursive"`
}

type RemoteRunCommandRequest struct {
	Target  string `json:"target"`
	Command string `json:"command"`
	Cwd     string `json:"cwd,omitempty"`
	Timeout int    `json:"timeout,omitempty"`
	Shell   string `json:"shell,omitempty"`
	// FullOutput 与本地 command 同语义：默认模型侧只收尾部几行。
	FullOutput bool `json:"fullOutput,omitempty"`
}

type remoteTarget struct {
	Raw           string
	Host          string
	Port          string
	WorkspaceRoot string
}

type remoteRawFile struct {
	Path       string
	Data       []byte
	Size       int64
	Mode       int
	ModTime    string
	LineEnding string
}

type GitStatus struct {
	Added    int    `json:"added"`
	Modified int    `json:"modified"`
	Deleted  int    `json:"deleted"`
	Ahead    int    `json:"ahead"`
	Behind   int    `json:"behind"`
	IsRepo   bool   `json:"isRepo"`
	Branch   string `json:"branch"`
}

type GitDiffFile struct {
	Path      string `json:"path"`
	Status    string `json:"status"`
	Diff      string `json:"diff"`
	Added     int    `json:"added"`
	Deleted   int    `json:"deleted"`
	Truncated bool   `json:"truncated"`
	Binary    bool   `json:"binary"`
	Error     string `json:"error,omitempty"`
}

type GitDiffResult struct {
	IsRepo    bool          `json:"isRepo"`
	Branch    string        `json:"branch"`
	Files     []GitDiffFile `json:"files"`
	Truncated bool          `json:"truncated"`
	Error     string        `json:"error,omitempty"`
}

type EditResult struct {
	Path              string   `json:"path"`
	BeforeSHA256      string   `json:"beforeSha256"`
	AfterSHA256       string   `json:"afterSha256"`
	BeforeVersion     string   `json:"beforeVersion"`
	Version           string   `json:"version"`
	BeforeBytes       int      `json:"beforeBytes"`
	AfterBytes        int      `json:"afterBytes"`
	Replacements      int      `json:"-"`
	AddedLines        int      `json:"addedLines"`
	RemovedLines      int      `json:"removedLines"`
	LineEnding        string   `json:"lineEnding"`
	Summary           string   `json:"summary"`
	Diff              string   `json:"diff,omitempty"`
	FirstChanged      int      `json:"firstChangedLine,omitempty"`
	LastChanged       int      `json:"lastChangedLine,omitempty"`
	Warnings          []string `json:"warnings,omitempty"`
	Classification    string   `json:"classification,omitempty"`
	ChangedLinesBlock string   `json:"changedLinesBlock,omitempty"`
	// Created is set only by create: true when the file did not exist before,
	// false when an existing file was overwritten. nil for non-create tools.
	Created *bool `json:"created,omitempty"`
	// CreatedDirs lists parent directories newly created by this call (outermost
	// first, workspace-relative when inside the primary workspace), only when
	// non-empty.
	CreatedDirs []string `json:"createdDirs,omitempty"`
	// Validation is a concise post-write syntax/compile check for the model.
	// It is intentionally a string so the model can act on it without another
	// nested result schema.
	Validation string `json:"validation,omitempty"`
}

// EditRequest is the backend edit engine request. The model-facing edit tool
// uses batched all-or-nothing changes; legacy fields remain for Wails/backend compatibility.
type EditRequest struct {
	Path           string          `json:"path"`
	ExpectedSHA256 string          `json:"expectedSha256,omitempty"`
	Version        string          `json:"version,omitempty"`
	OldString      string          `json:"oldString,omitempty"`
	NewString      string          `json:"newString,omitempty"`
	ReplaceAll     bool            `json:"replaceAll,omitempty"`
	StartLine      int             `json:"startLine,omitempty"`
	EndLine        int             `json:"endLine,omitempty"`
	NewText        *string         `json:"newText,omitempty"`
	Edits          []EditOperation `json:"edits,omitempty"`
	BatchChanges   []TextChange    `json:"-"`
}

type EditOperation struct {
	OldString  string `json:"oldString"`
	NewString  string `json:"newString"`
	ReplaceAll bool   `json:"replaceAll,omitempty"`
}

type FileTextEdits struct {
	Path    string       `json:"path"`
	Version string       `json:"version"`
	Changes []TextChange `json:"changes"`
}

type TextChange struct {
	OldText    string `json:"oldText,omitempty"`
	LineRange  string `json:"lineRange,omitempty"`
	NewText    string `json:"newText"`
	ReplaceAll bool   `json:"replace_all,omitempty"`
}

type MultiEditResult struct {
	Files        []EditResult `json:"files"`
	FileCount    int          `json:"fileCount"`
	Replacements int          `json:"-"`
	AddedLines   int          `json:"addedLines"`
	RemovedLines int          `json:"removedLines"`
	Warnings     []string     `json:"warnings,omitempty"`
	Summary      string       `json:"summary"`
	Diff         string       `json:"diff,omitempty"`
	Validation   string       `json:"validation,omitempty"`
}

type editPlan struct {
	mode      string
	ops       []EditOperation
	changes   []TextChange
	startLine int
	endLine   int
	newText   string
}

type SkillDefinition struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Source      string `json:"source"`
	Path        string `json:"path"`
	Dir         string `json:"dir"`
	Type        string `json:"type"`
	WhenToUse   string `json:"whenToUse"`
	// embeddedContent holds the full skill body for built-in skills embedded
	// into the binary via go:embed. When non-empty, readers must use it
	// directly instead of os.ReadFile(Path). Not serialized to JSON.
	embeddedContent string
}

type BatchReadRequest struct {
	Path      string                 `json:"path,omitempty"`
	Paths     []string               `json:"paths,omitempty"`
	Files     []BatchReadFileRequest `json:"files,omitempty"`
	StartLine int                    `json:"startLine,omitempty"`
	EndLine   int                    `json:"endLine,omitempty"`
}

type BatchReadFileRequest struct {
	Path      string `json:"path"`
	StartLine int    `json:"startLine,omitempty"`
	EndLine   int    `json:"endLine,omitempty"`
}

type BatchReadResultItem struct {
	Path                  string `json:"path"`
	Content               string `json:"content"`
	Text                  string `json:"text,omitempty"`
	Kind                  string `json:"kind,omitempty"`
	ContentFormat         string `json:"contentFormat,omitempty"`
	Type                  string `json:"type,omitempty"`
	Editable              bool   `json:"editable"`
	StartLine             int    `json:"startLine"`
	EndLine               int    `json:"endLine"`
	NextStartLine         int    `json:"nextStartLine,omitempty"`
	Version               string `json:"version"`
	Size                  int64  `json:"size"`
	TotalLines            int    `json:"totalLines"`
	LineEnding            string `json:"lineEnding"`
	Truncated             bool   `json:"truncated"`
	TruncatedLines        []int  `json:"truncatedLines,omitempty"`
	TruncatedLinesOmitted bool   `json:"truncatedLinesOmitted,omitempty"`
	RangeStatus           string `json:"rangeStatus,omitempty"`
	EmptyRange            bool   `json:"emptyRange,omitempty"`
	Error                 string `json:"error,omitempty"`
	ErrorCode             string `json:"errorCode,omitempty"`
	Reused                bool   `json:"reused,omitempty"`
	// DataURL is set only for image files: a data:image/<mime>;base64,... URL
	// used to inject the picture into multimodal model context.
	DataURL string `json:"dataUrl,omitempty"`
}

type BatchReadResult struct {
	Files []BatchReadResultItem `json:"files"`
}

type DocumentReadRequest struct {
	Path string `json:"path"`
}

type DocumentReadResult struct {
	Path string `json:"path"`
}

type TodoEntry struct {
	Title  string `json:"title"`
	Status string `json:"status"` // pending, in_progress, done
}

type TodoListRequest struct {
	Todos []TodoEntry `json:"todos,omitempty"`
}

type AgentDelegateRequest struct {
	Task         string `json:"task"`
	Role         string `json:"role"`
	Description  string `json:"description,omitempty"`
	CleanContext bool   `json:"cleanContext,omitempty"`
	Model        string `json:"model,omitempty"`
	MaxSteps     int    `json:"maxSteps,omitempty"`
	tools        []openai.Tool
}

type AgentDelegateResult struct {
	AgentID     string   `json:"agentId"`
	Role        string   `json:"role"`
	Description string   `json:"description"`
	Status      string   `json:"status"`
	Steps       int      `json:"steps"`
	Summary     string   `json:"summary"`
	FilesRead   []string `json:"filesRead,omitempty"`
	FilesEdited []string `json:"filesEdited,omitempty"`
	Model       string   `json:"model"`
	Error       string   `json:"error,omitempty"`
}

// SubagentRun tracks a running/complete sub-agent instance.
type SubagentRun struct {
	ID           string             `json:"id"`
	SessionID    string             `json:"sessionId,omitempty"`
	Description  string             `json:"description"`
	Profile      string             `json:"profile"`
	Role         string             `json:"role,omitempty"`
	Status       string             `json:"status"` // running, completed, failed
	Steps        int                `json:"steps"`
	Summary      string             `json:"summary,omitempty"`
	FilesRead    []string           `json:"filesRead,omitempty"`
	FilesEdited  []string           `json:"filesEdited,omitempty"`
	Error        string             `json:"error,omitempty"`
	ToolCalls    []SubToolEvent     `json:"toolCalls,omitempty"`
	StartTime    int64              `json:"startTime"`
	InputTokens  int                `json:"inputTokens,omitempty"`
	OutputTokens int                `json:"outputTokens,omitempty"`
	TotalTokens  int                `json:"totalTokens,omitempty"`
	cancel       context.CancelFunc `json:"-"`
}

// SubToolEvent records a single tool invocation inside a sub-agent.
type SubToolEvent struct {
	ToolCallID string `json:"toolCallId"`
	Name       string `json:"name"`
	Args       string `json:"args"`
	Status     string `json:"status"` // running, success, error
	Summary    string `json:"summary,omitempty"`
	DurationMS int64  `json:"durationMs,omitempty"`
}

func (a *App) ensureInitialized() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.initialized {
		return nil
	}

	cfgDir := appDataDir()
	a.configPath = filepath.Join(cfgDir, "config.json")
	a.historiesDir = filepath.Join(cfgDir, "histories")
	a.sessionsDir = filepath.Join(cfgDir, "sessions")
	os.MkdirAll(a.historiesDir, 0755)
	os.MkdirAll(a.sessionsDir, 0755)
	os.MkdirAll(memoriesDir(), 0755)

	a.config = defaultConfigState()

	if loadPath, err := resolveConfigLoadPath(a.configPath); err == nil {
		var loaded ConfigState
		if loaded, err = readConfigFile(loadPath); err == nil {
			a.config = mergeConfig(a.config, loaded)
		}
	}
	a.disabledSkills = normalizeSkillNameList(a.config.DisabledSkills)
	a.config.DisabledSkills = cloneStringSlice(a.disabledSkills)
	// command 截断落盘文件位于工作区 .tmp，启动时清理过期文件（含旧 .ally/tmp 遗留）
	cleanupCommandSpillFiles(a.config.Workspace)
	a.initialized = true
	return nil
}

func (a *App) beginTaskbarRun() {
	a.taskbarMu.Lock()
	defer a.taskbarMu.Unlock()

	a.taskbarActiveRuns++
	if a.taskbarActiveRuns == 1 {
		setTaskbarRunningProgress()
	}
}

func (a *App) endTaskbarRun() {
	a.taskbarMu.Lock()
	if a.taskbarActiveRuns > 0 {
		a.taskbarActiveRuns--
	}
	if a.taskbarActiveRuns == 0 {
		clearTaskbarProgress()
	}
	a.taskbarMu.Unlock()

	flashTaskbarWindowIfInactive()
}

func (a *App) StartChat(req ChatRequest) (string, error) {
	if err := a.ensureInitialized(); err != nil {
		return "", err
	}
	cfg := a.effectiveConfig(req.Config)
	cfg.APIFormat = normalizeAPIFormat(cfg.APIFormat)
	if strings.TrimSpace(cfg.Model) == "" {
		return "", errors.New("model is required")
	}
	if strings.TrimSpace(cfg.BaseURL) == "" {
		cfg.BaseURL = defaultBaseURLForAPIFormat(cfg.APIFormat)
	}
	if len(resolveKeyPool(cfg)) == 0 {
		return "", errors.New("API key is required")
	}
	if strings.TrimSpace(cfg.Workspace) == "" {
		return "", errors.New("workspace is required")
	}

	runID := newID()
	ctx, cancel := context.WithCancel(context.Background())
	a.mu.Lock()
	// 同一会话同时只允许一个活跃 run：并发 run 会交叉执行 saveHistory，
	// 破坏模型上下文的消息顺序。前端在运行中的追问已走 InjectRunMessage
	// 注入路径，不会触发本守卫；这是对其它事件下游（如网络端 sink）的
	// 纵深防御，与 releaseSession 的活跃 run 检查保持对称。
	if req.SessionID != "" {
		for _, activeSessionID := range a.runSessions {
			if activeSessionID == req.SessionID {
				a.mu.Unlock()
				cancel()
				return "", errors.New("session already has an active run")
			}
		}
	}
	a.runs[runID] = cancel
	a.runSessions[runID] = req.SessionID
	a.runInputs[runID] = make(chan string, runInputBufferSize)
	a.mu.Unlock()

	go a.runChat(ctx, runID, req, cfg)
	return runID, nil
}

func (a *App) CancelRun(runID string) error {
	a.mu.Lock()
	cancel := a.runs[runID]
	a.mu.Unlock()
	if cancel == nil {
		return nil
	}
	cancel()
	return nil
}

func (a *App) finishRun(runID string) {
	a.mu.Lock()
	delete(a.runs, runID)
	delete(a.runSessions, runID)
	// The queue is dropped, not closed: closing a channel while a concurrent
	// InjectRunMessage is sending would panic, and dropping the only reference
	// lets the channel (plus any queued messages) be GC'd with the run.
	delete(a.runInputs, runID)
	a.mu.Unlock()
}

// InjectRunMessage queues a new user message into a live run. runChat injects
// it at the next agent step boundary (after the current tool batch finishes),
// so the model sees it on the following request. It returns an error when the
// run no longer exists or the queue is full.
func (a *App) InjectRunMessage(runID string, text string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return errors.New("message is required")
	}
	a.mu.Lock()
	ch := a.runInputs[runID]
	a.mu.Unlock()
	if ch == nil {
		return errors.New("run not found or already finished")
	}
	select {
	case ch <- text:
		return nil
	default:
		return fmt.Errorf("run message queue is full (limit %d)", runInputBufferSize)
	}
}

// drainPendingInputs non-blockingly takes every queued injected message for a
// run, in arrival order.
func (a *App) drainPendingInputs(runID string) []string {
	a.mu.Lock()
	ch := a.runInputs[runID]
	a.mu.Unlock()
	if ch == nil {
		return nil
	}
	var out []string
	for {
		select {
		case text := <-ch:
			out = append(out, text)
		default:
			return out
		}
	}
}

// appendPendingRunInputs drains the run's injected-message queue and appends
// each text as a user message. It reports whether anything was injected.
func (a *App) appendPendingRunInputs(runID string, messages []openai.ChatCompletionMessage) ([]openai.ChatCompletionMessage, bool) {
	texts := a.drainPendingInputs(runID)
	if len(texts) == 0 {
		return messages, false
	}
	for _, text := range texts {
		messages = append(messages, openai.ChatCompletionMessage{Role: openai.ChatMessageRoleUser, Content: text})
	}
	return messages, true
}

// ReleaseSession releases backend-only state while preserving persisted history.
func (a *App) ReleaseSession(sessionID string) error {
	return a.releaseSession(sessionID, false)
}

// DeleteSession releases backend state and removes persisted history.
func (a *App) DeleteSession(sessionID string) error {
	return a.releaseSession(sessionID, true)
}

func (a *App) releaseSession(sessionID string, deleteHistory bool) error {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return errors.New("session id is required")
	}
	a.mu.Lock()
	for _, activeSessionID := range a.runSessions {
		if activeSessionID == sessionID {
			a.mu.Unlock()
			return errors.New("session is still running")
		}
	}
	// A compaction in flight is about to saveHistory: releasing the session
	// underneath it would drop the summary on the floor and could write into
	// a freshly-created session reusing the same id.
	if _, compacting := a.compactingSessions[sessionID]; compacting {
		a.mu.Unlock()
		return errors.New("session is compacting")
	}
	delete(a.histories, sessionID)
	delete(a.todos, sessionID)
	delete(a.todoRevisions, sessionID)
	delete(a.liveBreakdown, sessionID)
	delete(a.sessionWorkspaceMaps, sessionID)
	a.mu.Unlock()

	a.subRunsMu.Lock()
	for id, run := range a.subRuns {
		if run != nil && run.SessionID == sessionID && run.Status != "running" {
			delete(a.subRuns, id)
		}
	}
	a.subRunsMu.Unlock()

	if deleteHistory && a.historiesDir != "" {
		for _, diskPath := range a.historyDiskPaths(sessionID) {
			if err := os.Remove(diskPath); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
	}
	if deleteHistory {
		if err := a.deleteSessionSnapshot(sessionID); err != nil {
			return err
		}
	}
	return nil
}

// compactSessionRunning reports whether a compaction LLM call is in flight
// for the given session.
func (a *App) compactSessionRunning(sessionID string) bool {
	a.mu.Lock()
	_, running := a.compactingSessions[sessionID]
	a.mu.Unlock()
	return running
}

// CompactSession compacts the conversation history for a session. Stage 1
// stubs stale tool-result bodies (free); the LLM summary (stage 2) only runs
// when usage is still over the threshold afterwards.
func (a *App) CompactSession(sessionID, instruction string) (map[string]any, error) {
	parent := a.ctx
	if parent == nil {
		parent = context.Background()
	}
	return a.compactSession(parent, sessionID, instruction)
}

// CancelCompaction aborts an in-flight manual compaction for the session so
// ESC does not have to wait out the compaction timeout.
func (a *App) CancelCompaction(sessionID string) error {
	a.mu.Lock()
	cancel := a.compactingCancels[sessionID]
	a.mu.Unlock()
	if cancel == nil {
		return nil
	}
	cancel()
	return nil
}

func (a *App) compactSession(parent context.Context, sessionID, instruction string) (map[string]any, error) {
	if err := a.ensureInitialized(); err != nil {
		return nil, err
	}
	cfg, err := a.getConfig()
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(cfg.Model) == "" {
		return nil, errors.New("model is required")
	}
	if len(resolveKeyPool(cfg)) == 0 {
		return nil, errors.New("API key is required")
	}
	// A live chat run may rewrite history concurrently (auto-compaction,
	// sanitize repair, new turns); compacting on top of it would interleave
	// saveHistory calls and race the run's own message list.
	a.mu.Lock()
	for _, activeSessionID := range a.runSessions {
		if activeSessionID == sessionID {
			a.mu.Unlock()
			return nil, errors.New("session is still running")
		}
	}
	// Compaction rewrites the whole session history, so two concurrent
	// compactions on the same session would interleave saveHistory calls and
	// pay for the LLM request twice. Guard at the session level so other
	// sessions compact independently.
	if _, running := a.compactingSessions[sessionID]; running {
		a.mu.Unlock()
		return nil, errors.New("session is already compacting")
	}
	// The summary LLM call runs on a cancellable child of the app context so
	// CancelCompaction (ESC) can abort it mid-flight.
	ctx, cancel := context.WithCancel(parent)
	// Lazy-init guard: a nil map write here panics while a.mu is held, and
	// the panic escapes before the cleanup defer is registered — the mutex
	// stays locked forever and every later call (ESC cancel, shutdown, any
	// binding) deadlocks the whole app. App instances built outside NewApp
	// (tests) must not take the process down with them.
	if a.compactingSessions == nil {
		a.compactingSessions = map[string]struct{}{}
	}
	if a.compactingCancels == nil {
		a.compactingCancels = map[string]context.CancelFunc{}
	}
	a.compactingSessions[sessionID] = struct{}{}
	a.compactingCancels[sessionID] = cancel
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		delete(a.compactingSessions, sessionID)
		delete(a.compactingCancels, sessionID)
		a.mu.Unlock()
		cancel()
		a.emit("compact:done", map[string]any{"sessionId": sessionID})
	}()

	history := sanitizeHistoryMessages(a.histories[sessionID])
	if len(history) == 0 {
		return nil, errors.New("no messages to compact")
	}

	tokensBefore := a.getContextBreakdown(sessionID).Total
	if tokensBefore <= 0 {
		tokensBefore = estimateTokensFromMessages(history)
	}

	// 手动点击压缩是用户的明确意图：无条件执行总结压缩，将切分点之前的较早历史总结为 Summary。
	// （自动压缩才会受 threshold 阈值限制）
	return a.compactHistory(ctx, cfg, sessionID, instruction, history, true, tokensBefore)
}

// compactThresholdLimit returns the absolute token count at which
// auto-compaction triggers for the given config (context window × threshold).
func compactThresholdLimit(cfg ConfigState) int {
	maxCtx := cfg.ContextWindow
	if maxCtx <= 0 {
		maxCtx = 1000000
	}
	return int(float64(maxCtx) * clampCompactThreshold(cfg.CompactThreshold))
}

// compactHistory summarizes the older portion of history using the model while retaining
// recent messages intact (pi-agent cut point design).
// keepLastUser preserves the final user message when not already part of retainedTail.
// tokensBefore is the total context tokens before compaction.
func (a *App) compactHistory(ctx context.Context, cfg ConfigState, sessionID, instruction string, history []openai.ChatCompletionMessage, keepLastUser bool, tokensBefore int) (map[string]any, error) {
	// The compaction LLM call runs inside this timeout. It is configurable
	// (Settings → General) because long-context summary requests through slow
	// providers can legitimately take minutes; the default covers the common
	// case without hanging forever. Cancellation through the parent context
	// (e.g. app shutdown) still works on top of the deadline.
	timeout := time.Duration(clampCompactTimeoutSeconds(cfg.CompactTimeoutSeconds)) * time.Second
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	if len(history) == 0 {
		return nil, errors.New("no messages to compact")
	}

	// Summarize all existing history cleanly into a single summary.
	messagesToSummarize := history
	// Build compaction prompt. Structured sections maximize information density
	// and give the model concrete anchors to recover from after compaction.
	compactPrompt := `The conversation context is getting long and is being compacted. Provide a high-density, structured handoff summary so work can continue seamlessly after clearing history.

CRITICAL LANGUAGE RULE:
Always write the summary in the primary language used by the user in the conversation (e.g. Chinese if the user spoke Chinese, English if the user spoke English, etc.). The section headings below may be translated into the user's language or kept as equivalent clear headings.

Use the following structure with Markdown headings:

## User Intent & Requirements / 用户需求与目标
Concise statement of the user's core intent, ongoing tasks, and explicit requirements.

## Findings & Analysis / 探索与分析结果
Key findings, root causes, architectural patterns, or logic flow discovered during investigation.

## What Has Been Done / 已完成工作
Bullet list of concrete actions taken: files edited, created, or deleted (with notes on changes), commands executed and their outcomes, verified items.

## Key Files & Locations / 关键文件与位置
Bullet list of key file paths referenced or touched, explicitly describing each file's specific role, responsibility, and what was modified:
- [path]: 具体用途与职责（该文件在项目中负责什么），以及本次对话中涉及的改动内容与关键位置


## Next Steps / 下一步工作
Exact, prioritized next steps to take immediately.

Rules:
- Strictly write in the user's language.
- DO NOT CALL ANY TOOLS (no tool calls). Output plain text Markdown directly.
- Keep file paths, command strings, function names, and identifiers exact.
- Factual and concise. Do not invent details; state "unknown" if not certain.
- This summary replaces prior conversation and must stand alone completely.`

	if instruction != "" {
		compactPrompt += "\n\nAdditional instruction: " + instruction
	}

	// Build messages for the compaction call
	compactionMessages := []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleSystem, Content: "You are an expert engineering assistant generating a structured context compaction summary. Output plain Markdown text directly. Do not call any tools. Always respond in the user's primary language."},
	}
	compactionMessages = append(compactionMessages, messagesToSummarize...)
	compactionMessages = append(compactionMessages, openai.ChatCompletionMessage{
		Role:    openai.ChatMessageRoleUser,
		Content: compactPrompt,
	})

	compactionMaxTokens := cfg.MaxTokens
	if compactionMaxTokens <= 0 {
		compactionMaxTokens = defaultMaxTokensForAPIFormat(cfg.APIFormat)
	}
	a.emit("compact:start", map[string]any{
		"sessionId":    sessionID,
		"tokensBefore": tokensBefore,
		"messages":     len(history),
		"timeoutMs":    int(timeout.Milliseconds()),
	})
	// Turn off reasoning_effort for compaction call so thinking models
	// don't waste the entire context/budget on hidden reasoning chains.
	compactionCfg := cfg
	compactionCfg.ReasoningEffort = "low"

	summary, usage, err := a.completeModelTextWithUsage(ctx, compactionCfg, cfg.Model, compactionMessages, compactionMaxTokens)
	if err != nil {
		return nil, fmt.Errorf("compaction failed: %w", err)
	}

	if strings.TrimSpace(summary) == "" {
		return nil, errors.New("compaction returned empty summary")
	}

	// Account the compaction LLM call in the workspace/token statistics so the
	// tokens spent summarizing are visible in the footer and the stats modal.
	fallbackInput := 0
	fallbackOutput := 0
	if usage == nil || usage.PromptTokens <= 0 {
		fallbackInput = estimateRequestTokens(compactionMessages, nil)
	}
	if usage == nil || usage.CompletionTokens <= 0 {
		fallbackOutput = estimateCompletionTokens(summary, "", nil)
	}
	a.recordWorkspaceTokenUsage(cfg.Workspace, usage, fallbackInput, fallbackOutput)
	fullSummary := summary

	// Replace history cleanly with just the compacted summary as a clean start.
	newHistory := []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleUser, Content: fullSummary},
	}

	a.saveHistory(sessionID, newHistory)

	tokensAfter := a.getContextBreakdown(sessionID).Total
	if tokensAfter <= 0 {
		tokensAfter = estimateTokensFromMessages(newHistory)
	}

	return map[string]any{
		"summary":      summary,
		"tokensBefore": tokensBefore,
		"tokensAfter":  tokensAfter,
	}, nil
}

func estimateTokensFromMessages(msgs []openai.ChatCompletionMessage) int {
	total := 0
	for _, m := range msgs {
		total += utf8.RuneCountInString(m.Content)
	}
	return total / 3 // rough estimate: 3 chars ≈ 1 token
}

// intFromAny converts a JSON-decoded (float64) or native (int) numeric value
// to int. Compaction results carry native ints for direct calls and float64
// once they cross the Wails JSON boundary.
func intFromAny(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	}
	return 0
}

func (a *App) runChat(ctx context.Context, runID string, req ChatRequest, cfg ConfigState) {
	sessionID := req.SessionID
	cfg.responsesPromptCacheKey = openAIResponsesPromptCacheKey(sessionID)
	// Knowledge-base runs mark their sources/ subtree read-only for every
	// tool call in this run; sub-agents inherit the policy via ctx.
	ctx = withKBDenyRoots(ctx, kbDenyRootsForConfig(cfg))
	a.beginTaskbarRun()
	// success marks a run that already persisted its history on the normal
	// run:done path. Interrupted runs (ESC/cancel, provider errors, stop
	// reasons, step limits) fall through to the deferred checkpoint save so
	// work completed before the interruption survives into the next request.
	success := false
	var messages []openai.ChatCompletionMessage
	startTime := time.Now()
	// runCacheHit/Miss accumulate prompt-cache hit/miss tokens across every
	// LLM request in this Run (a tool loop may issue many). The aggregate
	// rate Σhit/Σ(hit+miss) is what the frontend shows on the final assistant
	// message — a per-Run cache efficiency number, not a per-turn one.
	var runCacheHit, runCacheMiss int
	var runInputTokens, runOutputTokens int
	emitRunEnd := func(event string, kind string, payload map[string]any) {
		if payload == nil {
			payload = map[string]any{}
		}
		payload["runId"] = runID
		payload["sessionId"] = sessionID
		payload["durationMs"] = time.Since(startTime).Milliseconds()
		payload["cacheHit"] = runCacheHit
		payload["cacheMiss"] = runCacheMiss
		payload["inputTokens"] = runInputTokens
		payload["outputTokens"] = runOutputTokens
		a.emit(event, payload)
		// 任务结束的系统提示音（done/error/cancelled），经桌面通知服务
		// 播放。服务不可用（headless/测试/平台后端失败）时是静默 no-op。
		// kind 由调用点显式给出，避免从用户可见文案反推状态。
		a.notifyCompletion(kind, cfg.Workspace)
	}
	defer func() {
		if !success && len(sanitizeHistoryMessages(messages)) > 0 {
			a.saveHistory(req.SessionID, messages)
		}
		a.restoreSavedHistoryBreakdown(sessionID)
		a.endTaskbarRun()
		a.finishRun(runID)
	}()
	// Panic 兜底：runChat 在独立 goroutine 里运行，逃逸的 panic 会直接击穿
	// 整个桌面进程。这里把它转换成常规 run:error 结束路径。注册在上面的清理
	// defer 之后（LIFO 先执行），panic 在进入清理前就被拦下，部分历史检查点
	// 照常落盘、run 注册照常释放。
	defer func() {
		if r := recover(); r != nil {
			log.Printf("runChat: run %s panicked: %v\n%s", runID, r, debug.Stack())
			emitRunEnd("run:error", "error", map[string]any{"error": fmt.Sprintf("agent 内部错误: %v", r)})
		}
	}()

	a.emit("run:start", map[string]any{"runId": runID, "sessionId": sessionID})

	messages = a.buildMessages(req, cfg, a.listCachedSkills())
	tools := a.buildToolsForConfig(cfg)
	breakdownAcc := newLiveBreakdownAccumulator(messages)
	readCache := newRunReadCache()

	planAttached := false
	for step := 0; step < maxAgentSteps; step++ {
		sanitizedThisStep := false
		select {
		case <-ctx.Done():
			// 记录用户主动取消标记，让下一轮模型能区分"用户中断"与
			// provider 报错等其他原因导致的未完成回合。
			messages = append(messages, cancelledTurnMarker())
			emitRunEnd("run:error", "cancelled", map[string]any{"error": "已取消"})
			return
		default:
		} // Inject user messages queued while this run was working: they wait
		// for the current tool batch to complete and enter the context here,
		// right before the next model request, so the model sees them in the
		// following turn. They are persisted with the rest of the history.
		var injected bool
		messages, injected = a.appendPendingRunInputs(runID, messages)
		if injected {
			// Frontend boundary: the queued message has now entered the model
			// context. The UI may close out the previous assistant message so
			// the next response starts on a fresh one.
			a.emit("run:inject", map[string]any{"runId": runID, "sessionId": sessionID})
		}
		// Update live breakdown for context display (includes all tool calls/results)
		bd := breakdownAcc.update(messages)
		bd.ToolSchemas = estimateToolSchemaTokens(tools)
		finalizeContextBreakdownTotal(&bd)
		a.mu.Lock()
		a.liveBreakdown[sessionID] = bd
		a.mu.Unlock()

		// Auto-compact: when context usage exceeds the configured threshold
		// of the window, compact history. Threshold uses only usedTokens
		// (not usedTokens + maxTokens) so it reflects actual context state
		// instead of pre-reserving a fixed reply budget. The threshold is
		// configurable via Settings → General (default 60%); legacy config
		// without the field migrates to the default through mergeConfig.
		usedTokens := bd.Total
		compactThreshold := compactThresholdLimit(cfg)
		if usedTokens > compactThreshold {
			// Auto-compaction: context reached threshold.
			// Summarize older history while retaining recent work intact (cut-point design),
			// preventing prefix cache invalidation on sub-threshold turns.
			h := sanitizeHistoryMessages(messages)
			if len(h) > 2 {
				a.emit("run:compact", map[string]any{"sessionId": sessionID, "tokensBefore": usedTokens})
				// keepLastUser=false: the current request and any continuation
				// prompt are re-appended below, so carrying the trailing user
				// message into the compacted history would duplicate it.
				if result, err := a.compactHistory(ctx, cfg, sessionID, "", h, false, usedTokens); err == nil {
					a.mu.Lock()
					compacted := sanitizeHistoryMessages(a.histories[sessionID])
					a.mu.Unlock()
					messages = a.buildSystemContextMessages(sessionID, cfg, a.listCachedSkills())
					messages = append(messages, compacted...)
					if strings.TrimSpace(req.Message) != "" || len(req.Attachments) > 0 {
						messages = appendUserMessageWithAttachments(messages, req.Message, req.Attachments)
					}
					breakdownAcc.reset(messages)
					payload := map[string]any{
						"sessionId":    sessionID,
						"tokensBefore": intFromAny(result["tokensBefore"]),
						"tokensAfter":  intFromAny(result["tokensAfter"]),
					}
					if s, _ := result["summary"].(string); s != "" {
						payload["summary"] = s
					}
					a.emit("run:compacted", payload)
				} else {
					a.emit("run:compacted", map[string]any{"sessionId": sessionID, "error": err.Error()})
				}
			}
		}

		// 当前用户会话只在第一次模型请求前附带一次计划。
		requestIncludesPlan := !planAttached
		requestMessages := messages
		if requestIncludesPlan {
			requestMessages = a.appendPlanForUserTurn(sessionID, messages)
			planAttached = true
		}

		a.emit("run:llm_wait", map[string]any{"runId": runID, "sessionId": sessionID})
		toolCalls := []openai.ToolCall{}
		var modelResp *modelStreamResult
		var toolProgress *toolCallProgressTracker
		var toolBatchID string
		var err error
		// 轮次重试预算统一取自"LLM 请求重试次数"设置(单一来源):流式输出
		// 中断(已产出内容,适配器/多 key 循环不敢重试防重复输出)或空响应
		// 时整轮重来。pre-stream 失败已在 streamModelResponse 内部用同一
		// 预算重试过,这里不再叠加,避免 (N+1)×(N+1) 组合爆炸。
		maxTurnRetries := effectiveLLMRetries(cfg)
		for turnAttempt := 0; ; turnAttempt++ {
			toolCalls = []openai.ToolCall{}
			toolBatchID = fmt.Sprintf("%d", step)
			emittedEvents := false
			streamDeltas := newRunStreamDeltaEmitter(runID, sessionID, func(name string, payload map[string]any) {
				a.emit(name, payload)
			})
			toolProgress = newToolCallProgressTracker().withArgsRedact(a.redactSSHCredentials)
			modelResp, err = a.streamModelResponse(ctx, cfg, cfg.Model, requestMessages, tools, func(event modelStreamEvent) {
				if event.ContentDelta != "" {
					emittedEvents = true
					streamDeltas.addContent(event.ContentDelta)
				}
				if event.ReasoningDelta != "" {
					emittedEvents = true
					streamDeltas.addReasoning(event.ReasoningDelta)
				}
				if event.Retry != nil {
					streamDeltas.flush()
					a.emit("run:retry", map[string]any{
						"runId": runID, "sessionId": sessionID,
						"attempt":     event.Retry.Attempt,
						"maxAttempts": event.Retry.MaxAttempts,
						"error":       event.Retry.Error,
						"waitMs":      event.Retry.WaitMS,
						"keyIndex":    event.Retry.KeyIndex,
						"totalKeys":   event.Retry.TotalKeys,
					})
				}
				if event.Image != nil && event.Image.DataURL != "" {
					emittedEvents = true
					streamDeltas.flush()
					a.emit("run:image", map[string]any{
						"runId": runID, "sessionId": sessionID, "id": event.Image.ID,
						"dataUrl": event.Image.DataURL, "mimeType": event.Image.MimeType, "partial": event.Image.Partial,
					})
				}
				if event.ToolCalls != nil {
					emittedEvents = true
					streamDeltas.flush()
					toolCalls = cloneToolCalls(event.ToolCalls)
					for _, toolEvent := range toolProgress.events(runID, sessionID, toolBatchID, toolCalls, a.mcpToolEventMeta) {
						a.emit(toolEvent.Name, toolEvent.Payload)
					}
				}
			})
			streamDeltas.flush()
			// Provider-declared terminal failures (for example finish_reason=length)
			// are reported below with their usage intact. Only a nominally valid
			// response with no visible output enters the transient retry path.
			if err == nil && modelResponseStopError(cfg, modelResp) == nil {
				err = emptyModelResponseError(modelResp)
			}
			if err == nil {
				break
			}
			if errors.Is(err, context.Canceled) {
				messages = append(messages, cancelledTurnMarker())
				emitRunEnd("run:error", "cancelled", map[string]any{"error": "已取消"})
				return
			}
			if isProvider400Error(err) && !sanitizedThisStep {
				sanitizedThisStep = true
				repaired := sanitizeHistoryMessages(messages)
				if len(repaired) < len(messages) {
					messages = repaired
					// requestMessages is a request-only snapshot (it may contain
					// the transient plan message). Rebuild it after sanitizing so
					// the retry cannot send the poisoned pre-repair context again.
					requestMessages = messages
					if requestIncludesPlan {
						requestMessages = a.appendPlanForUserTurn(sessionID, messages)
					}
					a.emit("run:retry", map[string]any{"runId": runID, "sessionId": sessionID, "attempt": 1, "maxAttempts": 1, "reason": "context sanitized after provider 400"})
					continue
				}
			}
			// 轮次重试只接管内层无法重试的场景:流中断(已发射事件,内层重试会
			// 造成重复输出)或空响应(errEmptyModelResponse,内层重试逻辑不覆盖)。
			// pre-stream 失败已在 streamModelResponse 内部按同一预算退避重试过,
			// 这里直接失败,避免同一请求被两层循环重复重试。
			if turnAttempt >= maxTurnRetries || !shouldRetryLLMError(err) ||
				!(emittedEvents || errors.Is(err, errEmptyModelResponse)) {
				emitRunEnd("run:error", "error", map[string]any{"error": err.Error()})
				return
			}
			wait := llmRetryDelay(turnAttempt + 1)
			a.emit("run:retry", map[string]any{
				"runId": runID, "sessionId": sessionID, "attempt": turnAttempt + 1,
				"maxAttempts": maxTurnRetries, "error": err.Error(), "waitMs": wait,
				"reason": "stream_interrupted", "discardCurrentResponse": true,
			})
			select {
			case <-time.After(wait):
			case <-ctx.Done():
				messages = append(messages, cancelledTurnMarker())
				emitRunEnd("run:error", "cancelled", map[string]any{"error": "已取消"})
				return
			}
		}

		content := modelResp.Content
		reasoning := modelResp.Reasoning
		// Keep the provider-facing history valid even when a streamed tool call
		// ended mid-JSON: truncated arguments are rewritten to the explicit
		// truncation marker for both replay and execution.
		var toolExecutionArgs []string
		toolCalls, toolExecutionArgs = prepareToolCallsForExecution(modelResp.ToolCalls)
		fallbackInput := 0
		fallbackOutput := 0
		if modelResp.Usage == nil || modelResp.Usage.PromptTokens <= 0 {
			fallbackInput = estimateRequestTokens(messages, tools)
		}
		if modelResp.Usage == nil || modelResp.Usage.CompletionTokens <= 0 {
			fallbackOutput = estimateCompletionTokens(content, reasoning, toolCalls)
		}
		a.recordWorkspaceTokenUsage(cfg.Workspace, modelResp.Usage, fallbackInput, fallbackOutput)
		a.recordTokenStats(cfg.Model, cfg.Workspace, modelResp.Usage, fallbackInput, fallbackOutput)
		if modelResp.Usage != nil {
			runCacheHit += modelResp.Usage.CacheHitTokens
			runCacheMiss += modelResp.Usage.CacheMissTokens
			runInputTokens += modelResp.Usage.PromptTokens
			runOutputTokens += modelResp.Usage.CompletionTokens
		}
		if stopErr := modelResponseStopError(cfg, modelResp); stopErr != nil {
			if content != "" || reasoning != "" {
				messages = append(messages, openai.ChatCompletionMessage{
					Role:             openai.ChatMessageRoleAssistant,
					Content:          content,
					ReasoningContent: reasoning,
				})
			}
			emitRunEnd("run:error", "error", map[string]any{"error": stopErr.Error(), "stopReason": modelResp.StopReason})
			return
		}
		if len(toolCalls) == 0 {
			if content != "" {
				messages = append(messages, openai.ChatCompletionMessage{Role: openai.ChatMessageRoleAssistant, Content: content})
			}
			// The model stopped calling tools, but the user may have just
			// injected a message: take the queue once more and continue for
			// another step so the model actually sees it, instead of ending
			// the run with the injection still queued.
			var injected bool
			messages, injected = a.appendPendingRunInputs(runID, messages)
			if injected {
				a.emit("run:inject", map[string]any{"runId": runID, "sessionId": sessionID})
				continue
			}
			a.saveHistory(req.SessionID, messages)
			success = true
			emitRunEnd("run:done", "done", nil)
			return
		}

		for i := range toolCalls {
			if toolCalls[i].ID == "" {
				toolCalls[i].ID = fmt.Sprintf("call_%s_%d", runID, i)
			}
			if toolCalls[i].Type == "" {
				toolCalls[i].Type = openai.ToolTypeFunction
			}
		}
		for _, event := range toolProgress.forceEvents(runID, sessionID, toolBatchID, toolCalls, a.mcpToolEventMeta) {
			a.emit(event.Name, event.Payload)
		}
		messages = append(messages, openai.ChatCompletionMessage{
			Role:      openai.ChatMessageRoleAssistant,
			Content:   content,
			ToolCalls: toolCalls,
		})

		// Execute non-file tools in parallel. Built-in file mutations run
		// afterward in tool-call order so writes are deterministic.
		type toolOutcome struct {
			index     int
			callID    string
			name      string
			result    toolResult
			json      string
			modelJSON string
			duration  int64
		}

		totalCalls := len(toolCalls)
		toolSem := make(chan struct{}, 4)
		outcomes := make([]toolOutcome, totalCalls)
		toolConflicts := detectToolBatchConflicts(cfg, toolCalls)

		// emitOutcome pushes a single tool's result/error to the frontend as
		// soon as that tool finishes, instead of waiting for the whole batch.
		// The Wails emit chain is concurrency-safe (notifyLock + windowthread
		// serialization) and the frontend locates cards by
		// runId:toolBatchId:toolCallIndex, so out-of-order emits land
		// correctly. messages append stays ordered below.
		emitOutcome := func(o toolOutcome) {
			if o.result.OK {
				a.emit("tool:result", mergeToolEventMeta(map[string]any{"runId": runID, "sessionId": sessionID, "toolBatchId": toolBatchID, "toolCallIndex": o.index, "toolCallId": o.callID, "name": o.name, "result": a.redactSSHCredentials(o.json), "durationMs": o.duration}, a.mcpToolEventMeta(o.name)))
			} else {
				a.emit("tool:error", mergeToolEventMeta(map[string]any{"runId": runID, "sessionId": sessionID, "toolBatchId": toolBatchID, "toolCallIndex": o.index, "toolCallId": o.callID, "name": o.name, "error": a.redactSSHCredentials(o.result.Error), "errorCode": o.result.ErrorCode, "durationMs": o.duration}, a.mcpToolEventMeta(o.name)))
			}
		}

		executeCall := func(idx int, c openai.ToolCall, plannedValidationPaths []string, hasValidationPlan bool) {
			started := time.Now()
			executionArgs := c.Function.Arguments
			if idx < len(toolExecutionArgs) && toolExecutionArgs[idx] != "" {
				executionArgs = toolExecutionArgs[idx]
			}
			toolCtx := context.WithValue(ctx, toolExecutionMetaContextKey{}, toolExecutionMeta{
				runID: runID, sessionID: sessionID, toolBatchID: toolBatchID,
				toolCallIndex: idx, toolCallID: c.ID, toolName: c.Function.Name, toolArgs: c.Function.Arguments,
			})
			toolCtx = context.WithValue(toolCtx, runReadCacheContextKey{}, readCache)
			if hasValidationPlan {
				toolCtx = context.WithValue(toolCtx, batchValidationPathsContextKey{}, plannedValidationPaths)
			}
			r := a.executeTool(toolCtx, cfg, sessionID, c.Function.Name, []byte(executionArgs))
			duration := time.Since(started).Milliseconds()
			rj, _ := json.Marshal(r)
			fullJSON := string(rj)
			o := toolOutcome{index: idx, callID: c.ID, name: c.Function.Name, result: r, json: fullJSON, modelJSON: compactToolResultForModel(c.Function.Name, r, fullJSON), duration: duration}
			outcomes[idx] = o
			emitOutcome(o)
		}
		setConflictOutcome := func(idx int, c openai.ToolCall, conflictErr error) {
			r := toolErrorResult(conflictErr)
			rj, _ := json.Marshal(r)
			fullJSON := string(rj)
			o := toolOutcome{index: idx, callID: c.ID, name: c.Function.Name, result: r, json: fullJSON, modelJSON: fullJSON}
			outcomes[idx] = o
			emitOutcome(o)
		}

		var wg sync.WaitGroup
		for i, call := range toolCalls {
			if conflictErr, conflict := toolConflicts[i]; conflict {
				setConflictOutcome(i, call, conflictErr)
				continue
			}
			if isOrderedFileMutationTool(call.Function.Name) {
				continue
			}
			wg.Add(1)
			go func(idx int, c openai.ToolCall) {
				defer wg.Done()
				toolSem <- struct{}{}        // acquire
				defer func() { <-toolSem }() // release
				executeCall(idx, c, nil, false)
			}(i, call)
		}
		wg.Wait()
		// Spread validation work over the batch: directory-level checks (go
		// vet per package, tsc/vue-tsc per project) run once per unit on the
		// last mutation call touching it instead of once per edit call.
		validationRoots, _ := workspaceRoots(cfg)
		validationPlan := planBatchValidation(validationRoots, toolCalls, conflictSkippedCallIndexes(toolConflicts))
		for i, call := range toolCalls {
			if _, conflict := toolConflicts[i]; conflict || !isOrderedFileMutationTool(call.Function.Name) {
				continue
			}
			planned, hasPlan := validationPlan[i]
			executeCall(i, call, planned, hasPlan)
		}

		// Append tool results to the model message history in tool-call
		// order. Emitting already happened per-tool as each finished.
		// Strip the previous turn's image-injection message first so each
		// tool batch carries only its own images (single-turn context).
		messages = stripImageInjectionMessages(messages)
		for _, o := range outcomes {
			messages = append(messages, openai.ChatCompletionMessage{
				Role:       openai.ChatMessageRoleTool,
				ToolCallID: o.callID,
				Content:    o.modelJSON,
			})
		}
		// Inject image files read by this tool batch into multimodal model
		// context (read of a .png/.jpg/... returns a base64 DataURL). The
		// user message sits right after the tool results, which keeps the
		// Anthropic tool-result/user pairing valid and gives all adapters a
		// user turn to attach the images to.
		var readImages []readImageCandidate
		for _, o := range outcomes {
			readImages = append(readImages, collectReadImages(o.name, &o.result)...)
		}
		if imgMsg := readImageInjectionMessage(readImages); imgMsg != nil {
			messages = append(messages, *imgMsg)
		}

		// A successful sole `suggest` call ends the run: its chips render
		// under the last assistant message, so issuing another model step
		// would only invite trailing content after them. Failed or
		// batch-conflicted suggest calls keep the loop running so the
		// model can recover from the error.
		if len(outcomes) == 1 && outcomes[0].name == "suggest" && outcomes[0].result.OK {
			var injected bool
			messages, injected = a.appendPendingRunInputs(runID, messages)
			if injected {
				a.emit("run:inject", map[string]any{"runId": runID, "sessionId": sessionID})
				continue
			}
			a.saveHistory(req.SessionID, messages)
			success = true
			emitRunEnd("run:done", "done", nil)
			return
		}
	}
	emitRunEnd("run:error", "error", map[string]any{"error": "达到最大 agent 步数，已停止"})
	return
}

// Static tool schemas live in internal/tools/shared. These wrappers keep the
// orchestration and existing package-level tests independent from that layout.
func chatTools() []openai.Tool {
	return toolshared.Builtins()
}
func rawFunctionTool(name, description string, parameters map[string]any) openai.Tool {
	return toolshared.RawFunction(name, description, parameters)
}
func normalizeToolName(name string) string { return toolshared.NormalizeName(name) }

func (a *App) executeTool(ctx context.Context, cfg ConfigState, sessionID, name string, args []byte) (result toolResult) {
	// Tool execution digests model-controlled JSON and is the single choke
	// point for the main loop, sub-agents, and scheduled tasks. A panic in
	// any handler would otherwise crash the whole desktop app — and in the
	// ordered file-mutation phase it would unwind runChat and poison the
	// deferred checkpoint save with dangling tool_calls. Convert panics into
	// a normal tool error so the agent loop keeps running.
	defer func() {
		if r := recover(); r != nil {
			log.Printf("executeTool: tool %s panicked: %v\n%s", name, r, debug.Stack())
			result = toolErrorResult(codedToolError("E_TOOL_PANIC", fmt.Errorf("tool %s crashed internally: %v", name, r)))
		}
	}()
	// decodeJSON unmarshals args into v, allowing unknown fields but collecting
	// them as warnings. Returns (error, warnings) where warnings are finished
	// model-facing notices: ignored unknown argument keys plus auto-repair
	// notes when the model emitted JSON-encoded strings where arrays/objects
	// belong (e.g. {"files":"[{...}]"}).
	decodeJSON := func(v any) (error, []string) {
		if len(bytes.TrimSpace(args)) == 0 {
			return nil, nil
		}
		// First, collect all valid JSON field names from the target struct.
		validFields := collectValidJSONFields(v)
		// Parse the raw JSON to a map to detect extra keys.
		var rawMap map[string]json.RawMessage
		if err := json.Unmarshal(args, &rawMap); err != nil {
			// Only an unexpected end is evidence of a cut-off stream. Unknown
			// fields, wrong types, and other complete-JSON schema errors used to
			// be mislabeled as truncation, making small oldText/legacy oldString
			// calls look as if they had exhausted max_tokens.
			if isIncompleteStreamJSON(err) {
				if name == "edit" || name == "replace_exact" || name == "replace_lines" || name == "remote_edit" {
					return fmt.Errorf("tool arguments JSON was truncated (output cut off or stream interrupted). Merge small changes into one edit call; split large changes (a whole function or section) into separate edit calls; use lineRange (inclusive A-B whole-line range) for large deletions or replacements of a contiguous line range: %w", err), nil
				}
				return fmt.Errorf("tool arguments JSON was truncated (output cut off or stream interrupted): %w", err), nil
			}
			return fmt.Errorf("invalid tool arguments JSON for %s: %w", name, err), nil
		}
		// Unmarshal into the actual target (allows unknown fields). Type errors
		// trigger one repair round per offending field path: double-encoded
		// string fields are decoded in place, so a recoverable formatting slip
		// does not waste a model round trip.
		var repairedFields []string
		cur := args
		for round := 0; ; round++ {
			err := json.Unmarshal(cur, v)
			if err == nil {
				break
			}
			var typeErr *json.UnmarshalTypeError
			if !errors.As(err, &typeErr) || round >= maxToolArgRepairRounds {
				return fmt.Errorf("invalid tool arguments JSON for %s: %w", name, err), nil
			}
			fixed, ok := repairToolArgJSON(cur, typeErr)
			if !ok {
				return fmt.Errorf("invalid tool arguments JSON for %s: %w", name, err), nil
			}
			cur = fixed
			repairedFields = append(repairedFields, typeErr.Field)
		}
		var warnings []string
		if len(repairedFields) > 0 {
			warnings = append(warnings, fmt.Sprintf(
				"参数 %s 的格式有误（应为 JSON 数组/对象，却收到了带引号的字符串），已自动修复并照常执行；后续调用请直接传 JSON 数组或对象，不要序列化成字符串。",
				strings.Join(repairedFields, ", ")))
		}
		// Collect extra keys. Repair only rewrites values, never keys, so the
		// original rawMap stays authoritative here.
		extraFields := make([]string, 0, len(rawMap))
		for k := range rawMap {
			if _, ok := validFields[k]; !ok {
				extraFields = append(extraFields, k)
			}
		}
		if len(extraFields) > 0 {
			warnings = append(warnings, fmt.Sprintf("以下参数不被该工具支持，已忽略：%s", strings.Join(extraFields, ", ")))
		}
		return nil, warnings
	}

	// Normalize once at the boundary: lower-case for case-insensitivity and
	// resolve deprecated aliases so historical sessions keep working after a
	// rename. MCP tools (mcp__*) pass through unchanged because their
	// sanitized names are already lowercase.
	name = normalizeToolName(name)

	// 截断参数的 tool call 一律不执行：normalizeToolCalls 把流式截断的
	// arguments 替换成了 {"allyTruncatedArguments":true}。直接执行会让 MCP
	// 工具收到垃圾参数并报错，错误结果进历史后可能毒化会话。直接返回
	// 截断错误，让模型重新调用。
	if isTruncatedArgsMarker(args) {
		return toolErrorResult(codedToolError("E_TRUNCATED_ARGS",
			fmt.Errorf("tool arguments were truncated during streaming; please re-send the call with complete parameters (merge small changes, split large edits into separate calls)")))
	}

	var data any
	var err error
	// Lenient decode: unknown argument keys and auto-repaired argument values
	// are collected across the switch and reported once on the successful
	// result envelope instead of failing the whole call. Missing/invalid
	// required parameters still fail loudly.
	var argWarnings []string

	switch name {
	case "list_files":
		var req ListFilesRequest
		err, argWarnings = decodeJSON(&req)
		if err == nil {
			req.ModelFacing = true
			data, err = a.listFilesWithConfig(cfg, req)
		}
	case "edit":
		// The model-facing request is the flat FileTextEdits itself: exactly
		// one file per call, path/version/changes at the top level. Multi-file
		// changes are parallel edit calls in one model response. Truncated
		// arguments never reach this case: prepareToolCallsForExecution has
		// already rewritten them to the truncation marker, which fails above
		// with E_TRUNCATED_ARGS and the model resends the complete call.
		var req FileTextEdits
		err, argWarnings = decodeJSON(&req)
		if err == nil {
			err = validateModelEditToolRequest([]FileTextEdits{req})
		}
		if err == nil {
			err = kbDenyCheckPaths(ctx, cfg, req.Path)
		}
		if err == nil {
			a.fileOpsMu.Lock()
			data, err = a.editFilesWithConfig(cfg, []FileTextEdits{req})
			a.fileOpsMu.Unlock()
			if err == nil {
				data = attachValidation(data, a.validateChangedFilesForCall(ctx, cfg, []string{req.Path}))
				a.invalidateWorkspaceMapCache(cfg)
				invalidateRunReadCache(ctx)
			}
		}
	case "create":
		var req CreateFileRequest
		err, argWarnings = decodeJSON(&req)
		if err == nil {
			err = kbDenyCheckPaths(ctx, cfg, req.Path)
		}
		if err == nil {
			a.fileOpsMu.Lock()
			data, err = a.createFileWithConfig(cfg, req)
			a.fileOpsMu.Unlock()
			if err == nil {
				data = attachValidation(data, a.validateChangedFilesForCall(ctx, cfg, []string{req.Path}))
				a.invalidateWorkspaceMapCache(cfg)
				invalidateRunReadCache(ctx)
			}
		}
	case "delete":
		var req DeletePathRequest
		err, argWarnings = decodeJSON(&req)
		if err == nil {
			err = kbDenyCheckPaths(ctx, cfg, req.Path)
		}
		if err == nil {
			a.fileOpsMu.Lock()
			data, err = a.deletePathWithConfig(cfg, req)
			a.fileOpsMu.Unlock()
			if err == nil {
				a.invalidateWorkspaceMapCache(cfg)
				invalidateRunReadCache(ctx)
			}
		}
	case "command":
		var req CommandRequest
		err, argWarnings = decodeJSON(&req)
		if err == nil {
			// A command can modify files before returning an error, and can run
			// concurrently with reads in one tool batch. Clear on both sides.
			invalidateRunReadCache(ctx)
			data, err = a.runCommandWithConfig(ctx, cfg, req)
			invalidateRunReadCache(ctx)
			if err == nil {
				a.invalidateWorkspaceMapCache(cfg)
			}
		}
	case "service":
		var req BackgroundProcessRequest
		err, argWarnings = decodeJSON(&req)
		if err == nil {
			switch strings.ToLower(strings.TrimSpace(req.Action)) {
			case "start":
				if err = kbDenyCheckCommand(ctx, cfg, req.Command); err == nil {
					data, err = a.startServiceWithConfig(cfg, StartServiceRequest{
						Name:    req.Name,
						Command: req.Command,
						Cwd:     req.Cwd,
					})
				}
			case "stop":
				data, err = a.stopService(StopServiceRequest{ID: req.ID, GraceSeconds: req.GraceSeconds})
			case "list":
				data = a.listServicesForTool()
			case "read":
				data, err = a.readServiceOutput(ServiceReadRequest{
					ID:        req.ID,
					TailBytes: req.TailBytes,
				})
			default:
				err = codedToolError("E_BAD_BACKGROUND_ACTION", errors.New("action must be start, stop, list, or read"))
			}
		}
	case "wait":
		var req WaitRequest
		err, argWarnings = decodeJSON(&req)
		if err == nil {
			data, err = waitWithContext(ctx, req)
		}
	case "ask":
		var req AskRequest
		err, argWarnings = decodeJSON(&req)
		if err == nil {
			data, err = a.executeAsk(ctx, sessionID, req)
		}
	case "suggest":
		var req struct {
			Items []string `json:"items"`
		}
		err, argWarnings = decodeJSON(&req)
		if err == nil {
			if len(req.Items) == 0 {
				err = codedToolError("E_BAD_SUGGEST", errors.New("items must contain at least 1 suggestion"))
			} else {
				data = map[string]any{"items": req.Items}
			}
		}
	case "scheduled_task":
		var req ScheduledTaskToolRequest
		err, argWarnings = decodeJSON(&req)
		if err == nil {
			data, err = a.executeScheduledTaskTool(cfg, req)
		}
	case "http_request":
		var req HTTPRequestToolRequest
		err, argWarnings = decodeJSON(&req)
		if err == nil {
			err = kbDenyCheckPaths(ctx, cfg, req.SaveTo)
		}
		if err == nil {
			data, err = a.httpRequestToolWithConfig(ctx, cfg, req)
		}
	case "web_fetch":
		var req WebFetchRequest
		err, argWarnings = decodeJSON(&req)
		if err == nil {
			data, err = a.webFetchToolWithConfig(ctx, cfg, req)
		}
	case "ssh_credential":
		var req SSHCredentialRequest
		err, argWarnings = decodeJSON(&req)
		if err == nil {
			data, err = a.executeSSHCredentialTool(req)
		}
	case "remote_read":
		var req RemoteReadFileRequest
		err, argWarnings = decodeJSON(&req)
		if err == nil {
			data, err = a.remoteReadFile(ctx, req)
		}
	case "remote_edit":
		// Flat single-file request: target + the same path/version/changes as
		// local edit. Truncated arguments are rejected upstream the same way
		// as edit (E_TRUNCATED_ARGS marker), so only complete JSON lands here.
		var req RemoteEditRequest
		err, argWarnings = decodeJSON(&req)
		if err == nil {
			err = validateModelEditToolRequest([]FileTextEdits{req.file()})
		}
		if err == nil {
			data, err = a.remoteEdit(ctx, req)
		}
	case "remote_create_file":
		var req RemoteCreateFileRequest
		err, argWarnings = decodeJSON(&req)
		if err == nil {
			data, err = a.remoteCreateFile(ctx, req)
		}
	case "remote_delete_path":
		var req RemoteDeletePathRequest
		err, argWarnings = decodeJSON(&req)
		if err == nil {
			data, err = a.remoteDeletePath(ctx, req)
		}
	case "remote_run_command":
		var req RemoteRunCommandRequest
		err, argWarnings = decodeJSON(&req)
		if err == nil {
			data, err = a.remoteRunCommand(ctx, req)
			// 与本地 command 对齐：默认模型侧只收尾部几行，被截内容
			// 落盘到本地工作区 .tmp 供 read 取回，避免重跑远端命令。
			if err == nil {
				if res, ok := data.(CommandResult); ok {
					res.FullOutput = req.FullOutput
					if !req.FullOutput {
						if roots, rootErr := workspaceRoots(cfg); rootErr == nil && len(roots) > 0 {
							if path, size, spilled := spillCommandOutputForTail(roots[0], res.Output, res.OutputFilePath); spilled {
								res.OutputFilePath = path
								res.OutputFileBytes = size
							}
						}
					}
					data = res
				}
			}
		}
	case "grep":
		var reqGF GrepRequest
		err, argWarnings = decodeJSON(&reqGF)
		if err == nil {
			data, err = a.grepFilesWithConfig(ctx, cfg, reqGF)
		}
	case "read":
		var reqBR BatchReadRequest
		err, argWarnings = decodeJSON(&reqBR)
		if err == nil {
			if cache, ok := ctx.Value(runReadCacheContextKey{}).(*runReadCache); ok {
				data, err = cache.read(a, cfg, reqBR)
			} else {
				data, err = a.batchReadFilesWithConfig(cfg, reqBR)
			}
		}

	case "document_read": // legacy name; documents now require the anydoc skill
		err = codedToolError("E_DOCUMENT_UNSUPPORTED",
			errors.New("document_read was removed; office/PDF documents are no longer read directly. Convert to Markdown with the anydoc skill first, then use read on the converted .md file"))
	case "calculate":
		var reqCalc CalculateRequest
		err, argWarnings = decodeJSON(&reqCalc)
		if err == nil {
			data, err = calculateExpression(reqCalc)
		}
	case "render_html":
		var req struct {
			HTML  string `json:"html"`
			Title string `json:"title"`
		}
		err, argWarnings = decodeJSON(&req)
		if err == nil {
			if len(req.HTML) > 50000 {
				err = errors.New("HTML content exceeds 50,000 character limit")
			} else {
				data = map[string]any{
					"rendered": true,
					"length":   len(req.HTML),
					"title":    req.Title,
				}
			}
		}
	case "subagent", "agent_delegate":
		var adReq AgentDelegateRequest
		err, argWarnings = decodeJSON(&adReq)
		if err == nil {
			err = a.acquireSubagentSlot(ctx)
			if err == nil {
				defer a.releaseSubagentSlot()
				subCtx, cancel := context.WithCancel(ctx)
				// Sub-agents build a fresh model context and never saw the parent
				// run's read content, so sharing the parent's read cache would
				// hand them a "content already returned" receipt for content they
				// never received. Give each sub-agent a fresh cache so dedup only
				// applies within its own reads.
				subCtx = context.WithValue(subCtx, runReadCacheContextKey{}, newRunReadCache())
				defer cancel()
				res, delegateErr := a.executeDelegate(subCtx, cfg, sessionID, adReq, cancel)
				if delegateErr != nil {
					err = delegateErr
				} else {
					data = res
				}
			}
		}
	case "plan":
		var tReq TodoListRequest
		err, argWarnings = decodeJSON(&tReq)
		if err == nil {
			data, err = a.handleTodoList(sessionID, tReq)
		}
	case "skill":
		var skReq struct {
			Skill string `json:"skill"`
			Args  string `json:"args,omitempty"`
		}
		err, argWarnings = decodeJSON(&skReq)
		if err == nil {
			data, err = a.handleSkillToolCall(skReq.Skill, skReq.Args)
		}
	default:
		// Route MCP tool calls by their sanitized OpenAI function name.
		if strings.HasPrefix(name, "mcp__") {
			var mcpArgs map[string]any
			if decodeErr := json.Unmarshal(args, &mcpArgs); decodeErr == nil {
				result, callErr := a.executeMcpFunctionTool(ctx, name, mcpArgs)
				if callErr != nil {
					err = callErr
				} else {
					data = result
				}
			} else {
				err = fmt.Errorf("invalid MCP tool args: %w", decodeErr)
			}
			break
		}
		err = fmt.Errorf("unknown tool: %s", name)
	}
	if err != nil {
		return toolErrorResult(err)
	}
	return toolResult{OK: true, Data: data, Warnings: argWarnings}
}

func waitWithContext(ctx context.Context, req WaitRequest) (WaitResult, error) {
	reason := strings.TrimSpace(req.Reason)
	if req.Seconds < 1 || req.Seconds > maxWaitSeconds {
		return WaitResult{}, codedToolError("E_BAD_WAIT", fmt.Errorf("seconds must be between 1 and %d", maxWaitSeconds))
	}
	if reason == "" {
		return WaitResult{}, codedToolError("E_BAD_WAIT", errors.New("reason is required"))
	}
	if utf8.RuneCountInString(reason) > 200 {
		return WaitResult{}, codedToolError("E_BAD_WAIT", errors.New("reason must be at most 200 characters"))
	}
	if ctx == nil {
		ctx = context.Background()
	}
	started := time.Now()
	timer := time.NewTimer(time.Duration(req.Seconds) * time.Second)
	defer timer.Stop()
	select {
	case <-timer.C:
		return WaitResult{RequestedSeconds: req.Seconds, ElapsedMS: time.Since(started).Milliseconds(), Reason: reason, Completed: true}, nil
	case <-ctx.Done():
		return WaitResult{}, codedToolError("E_WAIT_CANCELLED", errors.New("wait was cancelled"))
	}
}

func validateAskRequest(req AskRequest) error {
	if len(req.Questions) < 1 || len(req.Questions) > 5 {
		return codedToolError("E_BAD_ASK", errors.New("questions must contain between 1 and 5 items"))
	}
	questionIDs := map[string]bool{}
	for qi, question := range req.Questions {
		question.ID = strings.TrimSpace(question.ID)
		if question.ID == "" || len(question.ID) > 64 {
			return codedToolError("E_BAD_ASK", fmt.Errorf("question %d requires an id of at most 64 characters", qi+1))
		}
		if questionIDs[question.ID] {
			return codedToolError("E_BAD_ASK", fmt.Errorf("duplicate question id: %s", question.ID))
		}
		questionIDs[question.ID] = true
		if strings.TrimSpace(question.Question) == "" {
			return codedToolError("E_BAD_ASK", fmt.Errorf("question %s requires non-empty text", question.ID))
		}
		if len(question.Options) < 2 || len(question.Options) > 6 {
			return codedToolError("E_BAD_ASK", fmt.Errorf("question %s must provide between 2 and 6 options", question.ID))
		}
		optionIDs := map[string]bool{}
		recommended := 0
		for oi, option := range question.Options {
			option.ID = strings.TrimSpace(option.ID)
			if option.ID == "" || len(option.ID) > 64 {
				return codedToolError("E_BAD_ASK", fmt.Errorf("question %s option %d requires an id of at most 64 characters", question.ID, oi+1))
			}
			if optionIDs[option.ID] {
				return codedToolError("E_BAD_ASK", fmt.Errorf("question %s has duplicate option id %s", question.ID, option.ID))
			}
			optionIDs[option.ID] = true
			if strings.TrimSpace(option.Label) == "" || strings.TrimSpace(option.Description) == "" {
				return codedToolError("E_BAD_ASK", fmt.Errorf("question %s option %s requires label and description", question.ID, option.ID))
			}
			if option.Recommended {
				recommended++
			}
		}
		if recommended != 1 {
			return codedToolError("E_BAD_ASK", fmt.Errorf("question %s must mark exactly one option as recommended", question.ID))
		}
	}
	return nil
}

func (a *App) executeAsk(ctx context.Context, sessionID string, req AskRequest) (AskResult, error) {
	if err := validateAskRequest(req); err != nil {
		return AskResult{}, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	askID := newID()
	pending := &pendingAsk{sessionID: sessionID, request: req, answers: make(chan AskResult, 1)}
	a.askMu.Lock()
	a.pendingAsks[askID] = pending
	a.askMu.Unlock()
	defer func() {
		a.askMu.Lock()
		if a.pendingAsks[askID] == pending {
			delete(a.pendingAsks, askID)
		}
		a.askMu.Unlock()
	}()

	payload := map[string]any{"askId": askID, "sessionId": sessionID, "questions": req.Questions}
	if meta, ok := ctx.Value(toolExecutionMetaContextKey{}).(toolExecutionMeta); ok {
		payload["runId"] = meta.runID
		payload["toolBatchId"] = meta.toolBatchID
		payload["toolCallIndex"] = meta.toolCallIndex
		payload["toolCallId"] = meta.toolCallID
	}
	a.emit("ask:ready", payload)

	select {
	case result := <-pending.answers:
		return result, nil
	case <-ctx.Done():
		pending.mu.Lock()
		pending.cancelled = true
		pending.mu.Unlock()
		a.emit("ask:closed", map[string]any{"askId": askID, "sessionId": sessionID})
		return AskResult{}, codedToolError("E_ASK_CANCELLED", errors.New("ask was cancelled"))
	}
}

func resolveAskSubmission(askID string, req AskRequest, submitted []AskSubmittedAnswer) (AskResult, error) {
	if len(submitted) != len(req.Questions) {
		return AskResult{}, codedToolError("E_BAD_ASK_ANSWER", errors.New("every question requires an answer"))
	}
	answersByQuestion := make(map[string]AskSubmittedAnswer, len(submitted))
	for _, answer := range submitted {
		if _, exists := answersByQuestion[answer.QuestionID]; exists {
			return AskResult{}, codedToolError("E_BAD_ASK_ANSWER", fmt.Errorf("duplicate answer for question %s", answer.QuestionID))
		}
		answersByQuestion[answer.QuestionID] = answer
	}
	result := AskResult{AskID: askID, Answers: make([]AskResolvedAnswer, 0, len(req.Questions))}
	for _, question := range req.Questions {
		submittedAnswer, ok := answersByQuestion[question.ID]
		if !ok {
			return AskResult{}, codedToolError("E_BAD_ASK_ANSWER", fmt.Errorf("missing answer for question %s", question.ID))
		}
		options := make(map[string]AskOption, len(question.Options))
		for _, option := range question.Options {
			options[option.ID] = option
		}
		resolved := AskResolvedAnswer{QuestionID: question.ID, Question: question.Question}
		selected := map[string]bool{}
		for _, optionID := range submittedAnswer.SelectedOptionIDs {
			if selected[optionID] {
				continue
			}
			option, exists := options[optionID]
			if !exists {
				return AskResult{}, codedToolError("E_BAD_ASK_ANSWER", fmt.Errorf("question %s has unknown option %s", question.ID, optionID))
			}
			selected[optionID] = true
			resolved.Selections = append(resolved.Selections, AskResolvedSelection{
				OptionID: option.ID, Label: option.Label, Description: option.Description, Recommended: option.Recommended,
			})
		}
		if custom := strings.TrimSpace(submittedAnswer.CustomText); custom != "" {
			resolved.Selections = append(resolved.Selections, AskResolvedSelection{Label: custom, Custom: true})
		}
		if len(resolved.Selections) == 0 {
			return AskResult{}, codedToolError("E_BAD_ASK_ANSWER", fmt.Errorf("question %s requires at least one selected or custom answer", question.ID))
		}
		result.Answers = append(result.Answers, resolved)
	}
	return result, nil
}

func (a *App) SubmitAskResponse(req AskSubmitRequest) error {
	askID := strings.TrimSpace(req.AskID)
	if askID == "" {
		return codedToolError("E_BAD_ASK_ANSWER", errors.New("askId is required"))
	}
	a.askMu.Lock()
	pending := a.pendingAsks[askID]
	a.askMu.Unlock()
	if pending == nil {
		return codedToolError("E_ASK_NOT_FOUND", errors.New("ask request is no longer pending"))
	}
	if req.SessionID != "" && req.SessionID != pending.sessionID {
		return codedToolError("E_BAD_ASK_ANSWER", errors.New("sessionId does not match the pending ask request"))
	}
	result, err := resolveAskSubmission(askID, pending.request, req.Answers)
	if err != nil {
		return err
	}
	pending.mu.Lock()
	defer pending.mu.Unlock()
	if pending.cancelled {
		return codedToolError("E_ASK_CANCELLED", errors.New("ask was cancelled"))
	}
	select {
	case pending.answers <- result:
		return nil
	default:
		return codedToolError("E_ASK_ALREADY_SUBMITTED", errors.New("ask response was already submitted"))
	}
}

func (a *App) ListFiles(req ListFilesRequest) (ListFilesResult, error) {
	cfg, err := a.configForWorkspace(req.Workspace)
	if err != nil {
		return ListFilesResult{}, err
	}
	return a.listFilesWithConfig(cfg, req)
}

func (a *App) SearchWorkspacePaths(req WorkspacePathSearchRequest) (WorkspacePathSearchResult, error) {
	cfg, err := a.configForWorkspace(req.Workspace)
	if err != nil {
		return WorkspacePathSearchResult{}, err
	}
	return a.searchWorkspacePaths(cfg, req)
}

func (a *App) ReadFile(req ReadFileRequest) (ReadFileResult, error) {
	return a.readFileWithConfig(a.effectiveConfig(ConfigState{}), req)
}

func (a *App) ReplaceExact(req ReplaceExactRequest) (EditResult, error) {
	editReq := EditRequest{
		Path:           req.Path,
		ExpectedSHA256: req.ExpectedSHA256,
		OldString:      req.OldString,
		NewString:      req.NewString,
		ReplaceAll:     req.ReplaceAll,
	}
	return a.editWithConfig(a.effectiveConfig(ConfigState{}), editReq)
}

func (a *App) ReplaceLines(req ReplaceLinesRequest) (EditResult, error) {
	editReq := EditRequest{
		Path:           req.Path,
		ExpectedSHA256: req.ExpectedSHA256,
		StartLine:      req.StartLine,
		EndLine:        req.EndLine,
		NewText:        &req.NewText,
	}
	return a.editWithConfig(a.effectiveConfig(ConfigState{}), editReq)
}

func (a *App) CreateFile(req CreateFileRequest) (EditResult, error) {
	cfg, err := a.configForWorkspace(req.Workspace)
	if err != nil {
		return EditResult{}, err
	}
	// UI 直调的写操作与模型侧文件变更共用 fileOpsMu 串行化（对齐 executeTool
	// 与 MovePath），避免用户与 Agent 同时写同一路径时丢失更新。
	a.fileOpsMu.Lock()
	defer a.fileOpsMu.Unlock()
	result, err := a.createFileWithConfig(cfg, req)
	if err == nil {
		a.invalidateWorkspaceMapCache(cfg)
	}
	return result, err
}

func (a *App) CreateDirectory(req CreateDirectoryRequest) error {
	cfg, err := a.configForWorkspace(req.Workspace)
	if err != nil {
		return err
	}
	a.fileOpsMu.Lock()
	defer a.fileOpsMu.Unlock()
	err = a.createDirectoryWithConfig(cfg, req)
	if err == nil {
		a.invalidateWorkspaceMapCache(cfg)
	}
	return err
}

func (a *App) DeletePath(req DeletePathRequest) error {
	cfg, err := a.configForWorkspace(req.Workspace)
	if err != nil {
		return err
	}
	a.fileOpsMu.Lock()
	defer a.fileOpsMu.Unlock()
	_, err = a.deletePathWithConfig(cfg, req)
	if err == nil {
		a.invalidateWorkspaceMapCache(cfg)
	}
	return err
}

// CopyFilesIntoWorkspace copies files/directories dropped from the system
// file manager into a workspace directory (UI drag-and-drop; not a model
// tool). Sources are absolute paths from the native drop event; TargetDir is
// workspace-relative ("" = workspace root). Name conflicts get "name (N)"
// copies instead of overwriting.
func (a *App) CopyFilesIntoWorkspace(req CopyFilesIntoWorkspaceRequest) (CopyFilesIntoWorkspaceResult, error) {
	cfg, err := a.configForWorkspace(req.Workspace)
	if err != nil {
		return CopyFilesIntoWorkspaceResult{}, err
	}
	a.fileOpsMu.Lock()
	defer a.fileOpsMu.Unlock()
	result, err := a.copyFilesIntoWorkspaceWithConfig(cfg, req)
	if err == nil {
		a.invalidateWorkspaceMapCache(cfg)
	}
	return result, err
}

// ReadClipboardFiles returns the absolute file/folder paths currently on the
// system clipboard (Explorer/Finder Ctrl+C), or an empty list when the
// clipboard holds no file list. Used by the explorer's paste-into-workspace
// flow; platform-specific readers live in host_clipboard_*.go.
func (a *App) ReadClipboardFiles() ([]string, error) {
	return clipboardFiles()
}

// MovePath moves a file or directory from Source to Destination within the
// workspace. Both paths are workspace-relative. It rejects symlink paths and
// paths resolving outside the workspace. When Overwrite is false the
// destination must not already exist; when true an existing file at the
// destination is replaced (directories are not merged or replaced).
func (a *App) MovePath(req MovePathRequest) (MovePathResult, error) {
	if strings.TrimSpace(req.Source) == "" || strings.TrimSpace(req.Destination) == "" {
		return MovePathResult{}, codedToolError("E_BAD_PATH", errors.New("move requires non-empty source and destination"))
	}
	cfg := a.effectiveConfig(ConfigState{})
	a.fileOpsMu.Lock()
	defer a.fileOpsMu.Unlock()
	roots, err := workspaceRoots(cfg)
	if err != nil {
		return MovePathResult{}, err
	}
	srcAbs, err := resolveDeletablePath(roots, req.Source)
	if err != nil {
		return MovePathResult{}, err
	}
	dstAbs, err := resolveWritableFilePath(roots, req.Destination)
	if err != nil {
		return MovePathResult{}, err
	}
	if samePath(srcAbs, dstAbs) {
		return MovePathResult{Source: srcAbs, Destination: dstAbs, Moved: false}, nil
	}
	if _, err := os.Lstat(dstAbs); err == nil {
		if !req.Overwrite {
			return MovePathResult{}, codedToolError("E_EXISTS", fmt.Errorf("destination already exists: %s", req.Destination))
		}
		if err := os.RemoveAll(dstAbs); err != nil {
			return MovePathResult{}, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return MovePathResult{}, err
	}
	if err := os.MkdirAll(filepath.Dir(dstAbs), 0o755); err != nil {
		return MovePathResult{}, err
	}
	if err := os.Rename(srcAbs, dstAbs); err != nil {
		return MovePathResult{}, err
	}
	a.invalidateWorkspaceMapCache(cfg)
	return MovePathResult{Source: srcAbs, Destination: dstAbs, Moved: true}, nil
}

func (a *App) RunCommand(req CommandRequest) (CommandResult, error) {
	return a.runCommandWithConfig(context.Background(), a.effectiveConfig(ConfigState{}), req)
}

// SwitchModel applies a model config by index to the current settings.
func (a *App) SwitchModel(index int) error {
	if err := a.ensureInitialized(); err != nil {
		return err
	}
	a.mu.Lock()
	if index < 0 || index >= len(a.config.Models) {
		a.mu.Unlock()
		return fmt.Errorf("model index out of range: %d", index)
	}
	m := a.config.Models[index]
	a.config.ProviderName = m.ProviderName
	a.config.APIFormat = normalizeAPIFormat(m.APIFormat)
	a.config.BaseURL = m.BaseURL
	a.config.APIKey = m.APIKey
	a.config.APIKeys = cloneStringSlice(m.APIKeys)
	a.config.Model = m.Model
	a.config.MaxTokens = m.MaxTokens
	if m.ContextWindow > 0 {
		a.config.ContextWindow = m.ContextWindow
	}
	a.config.TokenParam = m.TokenParam
	a.config.ReasoningTag = normalizeReasoningTag(m.ReasoningTag)
	a.config.ReasoningEffort = normalizeReasoningEffort(m.ReasoningEffort)
	cfg := a.config
	syncAPIKeyFields(&cfg)
	a.mu.Unlock()
	return a.saveConfig(cfg)
}

// collectValidJSONFields returns a set of valid JSON field names from the
// target struct's JSON tags. It traverses the struct recursively to handle
// embedded structs and pointers. The input v must be a pointer to a struct.
func collectValidJSONFields(v any) map[string]struct{} {
	result := make(map[string]struct{})
	val := reflect.ValueOf(v)
	if val.Kind() == reflect.Ptr {
		val = val.Elem()
	}
	if val.Kind() != reflect.Struct {
		return result
	}
	typ := val.Type()
	for i := 0; i < val.NumField(); i++ {
		field := typ.Field(i)
		if field.PkgPath != "" && !field.Anonymous { // unexported, skip
			continue
		}
		tag := field.Tag.Get("json")
		if tag == "" || tag == "-" {
			continue
		}
		// json tag format: "name,omitempty" or just "name"
		name := strings.Split(tag, ",")[0]
		if name != "" {
			result[name] = struct{}{}
		}
		// Handle embedded structs recursively
		if field.Anonymous {
			var embeddedVal reflect.Value
			if val.Field(i).Kind() == reflect.Ptr {
				if val.Field(i).IsNil() {
					continue
				}
				embeddedVal = val.Field(i).Elem()
			} else {
				embeddedVal = val.Field(i)
			}
			if embeddedVal.Kind() == reflect.Struct {
				for k := range collectValidJSONFields(embeddedVal.Addr().Interface()) {
					result[k] = struct{}{}
				}
			}
		}
	}
	return result
}

// maxToolArgRepairRounds bounds the auto-repair loop for model-emitted tool
// arguments. Each round repairs one reported field path; chained mistakes
// (e.g. "files" emitted as a string whose items also encode "changes" as a
// string) need one round per layer.
const maxToolArgRepairRounds = 4

// repairToolArgJSON fixes common model JSON mistakes in tool arguments,
// guided by the *json.UnmarshalTypeError field path: a field expected to be
// a JSON array/object was emitted as a quoted JSON string, or a single
// object was emitted where an array is expected. Only values named by the
// type error are rewritten, so legitimate string arguments are never touched.
func repairToolArgJSON(args []byte, typeErr *json.UnmarshalTypeError) ([]byte, bool) {
	if typeErr == nil || typeErr.Field == "" {
		return nil, false
	}
	// encoding/json reports array indices as numeric path segments
	// ("files.0.changes"). Drop them: the same encoding mistake usually
	// repeats across array items, so the repair applies to every element.
	var path []string
	for _, seg := range strings.Split(typeErr.Field, ".") {
		if seg == "" {
			return nil, false
		}
		if _, err := strconv.Atoi(seg); err == nil {
			continue
		}
		path = append(path, seg)
	}
	if len(path) == 0 {
		return nil, false
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(args, &root); err != nil {
		return nil, false
	}
	wantSlice := typeErr.Type != nil && typeErr.Type.Kind() == reflect.Slice
	if !repairJSONFieldAtPath(root, path, wantSlice) {
		return nil, false
	}
	out, err := json.Marshal(root)
	if err != nil {
		return nil, false
	}
	return out, true
}

// repairJSONFieldAtPath walks the dotted field path through nested objects
// and arrays of objects (the same field name applies to every element) and
// repairs the leaf value. Reports whether anything changed.
func repairJSONFieldAtPath(obj map[string]json.RawMessage, path []string, wantSlice bool) bool {
	raw, ok := obj[path[0]]
	if !ok {
		return false
	}
	if len(path) == 1 {
		fixed, changed := repairJSONLeaf(raw, wantSlice)
		if changed {
			obj[path[0]] = fixed
		}
		return changed
	}
	// Nested object: descend one segment.
	var nested map[string]json.RawMessage
	if err := json.Unmarshal(raw, &nested); err == nil {
		if repairJSONFieldAtPath(nested, path[1:], wantSlice) {
			if out, err := json.Marshal(nested); err == nil {
				obj[path[0]] = out
				return true
			}
		}
		return false
	}
	// Array of objects: the remaining path applies to every element.
	var arr []json.RawMessage
	if err := json.Unmarshal(raw, &arr); err != nil {
		return false
	}
	changed := false
	for i, el := range arr {
		var item map[string]json.RawMessage
		if json.Unmarshal(el, &item) != nil {
			continue
		}
		if repairJSONFieldAtPath(item, path[1:], wantSlice) {
			if out, err := json.Marshal(item); err == nil {
				arr[i] = out
				changed = true
			}
		}
	}
	if changed {
		if out, err := json.Marshal(arr); err == nil {
			obj[path[0]] = out
			return true
		}
	}
	return false
}

// repairJSONLeaf rewrites one value: a quoted string whose content is itself
// valid JSON array/object text becomes the decoded value (wrapped in an
// array when a slice is expected), and a bare object becomes a one-element
// array when a slice is expected.
func repairJSONLeaf(raw json.RawMessage, wantSlice bool) (json.RawMessage, bool) {
	trim := bytes.TrimSpace(raw)
	var s string
	if err := json.Unmarshal(trim, &s); err == nil {
		inner := strings.TrimSpace(s)
		if !strings.HasPrefix(inner, "[") && !strings.HasPrefix(inner, "{") {
			return raw, false
		}
		if !json.Valid([]byte(inner)) {
			return raw, false
		}
		if wantSlice && strings.HasPrefix(inner, "{") {
			inner = "[" + inner + "]"
		}
		return json.RawMessage(inner), true
	}
	if wantSlice && bytes.HasPrefix(trim, []byte("{")) {
		return append(append([]byte("["), trim...), ']'), true
	}
	return raw, false
}

// Sub-agent frontend bindings (GetSubagents, cloneSubagentRun, StopSubagent)
// moved to orch_subagent.go.
