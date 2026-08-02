package app

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	goruntime "runtime"
	"sort"
	"strconv"
	"strings"
	"sync"

	"time"
	"unicode/utf8"

	"ally-dev/internal/tools/grep"
	"ally-dev/internal/tools/pathutil"
	"ally-dev/internal/tools/read"
	toolshared "ally-dev/internal/tools/shared"

	openai "github.com/sashabaranov/go-openai"
)

const (
	appName                          = "Ally"
	defaultModel                     = "deepseek-v4-flash"
	defaultBaseURL                   = "https://api.deepseek.com"
	defaultReasoningTag              = "reasoning_content"
	maxReadFileBytes                 = 10 * 1024 * 1024
	maxToolOutput                    = 128 * 1024
	maxFinishedSubagents             = 50
	maxSubagentToolCalls             = 100
	maxModelToolOutput               = 12 * 1024
	maxModelWebOutput                = 96 * 1024
	maxCodeGraphPromptBytes          = 96 * 1024
	modelToolHeadBytes               = 4 * 1024
	modelToolTailBytes               = 8 * 1024
	maxModelGrepMatches              = 200
	maxAgentSteps                    = 9999
	defaultLLMRetries                = 2
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
	maxSavedHistoryTokens            = 256 * 1024
	maxSavedHistoryJSONBytes         = 8 * 1024 * 1024
	// Background image storage. Bytes are written to
	// ~/.ally_agent/background.<ext> so config.json stays small; the
	// filename is stored in ConfigState.BackgroundImage.
	backgroundImageMaxBytes  = 12 * 1024 * 1024
	defaultBackgroundOpacity = 0.15
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

var (
	pythonRuntimeOnce sync.Once
	pythonRuntimeLine string
)

type workspaceMapCacheEntry struct {
	content     string
	generatedAt time.Time
}

// App is the Wails-bound application module.
type App struct {
	ctx    context.Context
	events eventSink

	mu             sync.Mutex
	config         ConfigState
	configPath     string
	runs           map[string]context.CancelFunc
	runSessions    map[string]string
	historiesDir   string
	histories      map[string][]openai.ChatCompletionMessage
	initialized    bool
	disabledSkills []string
	mcpManager     *McpManager
	goalStates     map[string]*GoalState
	todos          map[string][]TodoEntry // sessionID → todos
	todoRevisions  map[string]int64
	askMu          sync.Mutex
	pendingAsks    map[string]*pendingAsk

	subRuns   map[string]*SubagentRun // subId → run
	subRunsMu sync.Mutex
	subSem    chan struct{} // concurrency limiter (cap 4)
	fileOpsMu sync.Mutex    // serializes write ops (edit, create, delete) to prevent lost updates

	gitDiffMu     sync.Mutex
	gitDiffCancel context.CancelFunc
	gitDiffRunID  int64

	workspaceMapMu       sync.Mutex
	workspaceMapCache    map[string]workspaceMapCacheEntry
	workspacePathMu      sync.Mutex
	workspacePathCache   map[string]*workspacePathIndex
	workspacePathBuilds  map[string]chan struct{}
	workspacePathVersion int64

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

	servicesMu sync.Mutex
	services   map[string]*managedService

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
}

func NewApp() *App {
	a := &App{
		runs:                map[string]context.CancelFunc{},
		runSessions:         map[string]string{},
		histories:           map[string][]openai.ChatCompletionMessage{},
		goalStates:          map[string]*GoalState{},
		todos:               map[string][]TodoEntry{},
		todoRevisions:       map[string]int64{},
		pendingAsks:         map[string]*pendingAsk{},
		subRuns:             map[string]*SubagentRun{},
		subSem:              make(chan struct{}, 4),
		workspaceMapCache:   map[string]workspaceMapCacheEntry{},
		workspacePathCache:  map[string]*workspacePathIndex{},
		workspacePathBuilds: map[string]chan struct{}{},
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
	Temperature   float32  `json:"temperature"`
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
	// otherwise "low"/"medium"/"high"/"xhigh"/"max" is mapped per adapter
	// (OpenAI reasoning_effort / reasoning.effort with xhigh+max clamped to
	// high; Anthropic output_config.effort without enabling thinking blocks).
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
	APIKeys             []string      `json:"apiKeys,omitempty"`
	Model               string        `json:"model"`
	Workspace           string        `json:"workspace"`
	ExtraRoots          []string      `json:"extraRoots,omitempty"`
	Temperature         float32       `json:"temperature"`
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
	// AutoUpdate is a pointer so that an absent field in legacy config.json
	// is treated as "default on" rather than "off". Only an explicit false
	// disables automatic background downloads.
	AutoUpdate *bool `json:"autoUpdate,omitempty"`
	// SkippedUpdates records release tags the user chose to skip. They are
	// excluded from automatic download until the user clears them.
	SkippedUpdates []string `json:"skippedUpdates,omitempty"`
	// BackgroundImage is the filename of the user-uploaded chat background
	// image stored under ~/.ally_agent/. Empty means no custom background.
	// The actual bytes live on disk so config.json stays small; the frontend
	// resolves it to a file:// URL via GetBackgroundImageURL.
	BackgroundImage string `json:"backgroundImage,omitempty"`
	// BackgroundOpacity controls how strongly the custom background image
	// shows through the chat area. 0 = invisible, 1 = fully opaque. Clamped
	// to [0, 1] on save; the frontend default is 0.15 (faint silhouette).
	BackgroundOpacity float64 `json:"backgroundOpacity,omitempty"`
	grillMode         bool
	temperatureSet    bool
	// noAdapterRetry 是进程内非序列化标记:多 key 模式下置 true,让适配器
	// 内部关闭退避重试,由 streamModelResponse 的外层循环统一承担重试与
	// 故障切换,避免 N 个 key × 适配器重试组合爆炸。
	noAdapterRetry bool
}

// autoUpdateEnabled returns true unless AutoUpdate was explicitly set to false.
// Legacy config.json without the field defaults to enabled.
func (c ConfigState) autoUpdateEnabled() bool {
	if c.AutoUpdate != nil {
		return *c.AutoUpdate
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
}

type ChatRequest struct {
	SessionID   string             `json:"sessionId"`
	Message     string             `json:"message"`
	Messages    []ChatMessageInput `json:"messages"`
	Attachments []AttachmentInput  `json:"attachments,omitempty"`
	Config      ConfigState        `json:"config"`
	GrillMode   bool               `json:"grillMode,omitempty"`
}

type ListFilesRequest struct {
	Path           string `json:"path"`
	MaxDepth       int    `json:"maxDepth"`
	Limit          int    `json:"limit"`
	IncludeHidden  bool   `json:"includeHidden"`
	IncludeIgnored bool   `json:"includeIgnored"`
}

type FileEntry struct {
	Path    string `json:"path"`
	Name    string `json:"name"`
	Dir     bool   `json:"dir"`
	Size    int64  `json:"size"`
	ModTime string `json:"modTime"`
}

type ListFilesResult struct {
	Entries   []FileEntry `json:"entries"`
	Count     int         `json:"count"`
	Truncated bool        `json:"truncated"`
}

type WorkspacePathSearchRequest struct {
	Query string `json:"query"`
	Limit int    `json:"limit"`
	Force bool   `json:"force"`
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
	Sheet         string `json:"sheet,omitempty"`
	MaxChars      int    `json:"maxChars,omitempty"`
}

type ReadFileResult struct {
	Path          string   `json:"path"`
	Content       string   `json:"content"`
	RawContent    string   `json:"-"`
	Text          string   `json:"text,omitempty"`
	Kind          string   `json:"kind,omitempty"`
	ContentFormat string   `json:"contentFormat,omitempty"`
	Type          string   `json:"type,omitempty"`
	Editable      bool     `json:"editable"`
	StartLine     int      `json:"startLine"`
	EndLine       int      `json:"endLine"`
	NextStartLine int      `json:"nextStartLine,omitempty"`
	TotalLines    int      `json:"totalLines"`
	SHA256        string   `json:"sha256"`
	Version       string   `json:"version"`
	Size          int64    `json:"size"`
	LineEnding    string   `json:"lineEnding"`
	Truncated     bool     `json:"truncated"`
	RangeStatus   string   `json:"rangeStatus,omitempty"`
	EmptyRange    bool     `json:"emptyRange,omitempty"`
	Sheets        []string `json:"sheets,omitempty"`
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
	Path      string `json:"path"`
	Content   string `json:"content"`
	Overwrite bool   `json:"overwrite"`
}

type DeletePathRequest struct {
	Path      string `json:"path"`
	Recursive bool   `json:"recursive"`
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
	Command        string `json:"command"`
	Cwd            string `json:"cwd"`
	TimeoutSeconds int    `json:"timeoutSeconds"`
}

type StartServiceRequest struct {
	Name    string `json:"name,omitempty"`
	Command string `json:"command"`
	Cwd     string `json:"cwd,omitempty"`
}

type StopServiceRequest struct {
	ID string `json:"id"`
}

type BackgroundProcessRequest struct {
	Action    string `json:"action"`
	Name      string `json:"name,omitempty"`
	Command   string `json:"command,omitempty"`
	Cwd       string `json:"cwd,omitempty"`
	ID        string `json:"id,omitempty"`
	TailBytes int    `json:"tailBytes,omitempty"`
}

// ServiceReadRequest is the model-facing read payload for background_process.
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
	Command    string `json:"command"`
	Cwd        string `json:"cwd"`
	Shell      string `json:"shell"`
	ShellPath  string `json:"shellPath"`
	Output     string `json:"output"`
	ExitCode   int    `json:"exitCode"`
	TimedOut   bool   `json:"timedOut"`
	Cancelled  bool   `json:"cancelled"`
	DurationMS int64  `json:"durationMs"`
	Truncated  bool   `json:"truncated"`
}

type HTTPRequestToolRequest struct {
	Method              string            `json:"method"`
	URL                 string            `json:"url"`
	Headers             map[string]string `json:"headers,omitempty"`
	Query               map[string]string `json:"query,omitempty"`
	Body                string            `json:"body,omitempty"`
	JSON                any               `json:"json,omitempty"`
	SaveTo              string            `json:"saveTo,omitempty"`
	TimeoutSeconds      int               `json:"timeoutSeconds,omitempty"`
	MaxBytes            int               `json:"maxBytes,omitempty"`
	FollowRedirects     *bool             `json:"followRedirects,omitempty"`
	RespectRobots       *bool             `json:"respectRobots,omitempty"`
	AllowPrivateNetwork *bool             `json:"allowPrivateNetwork,omitempty"`
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
	RobotsAllowed bool              `json:"robotsAllowed,omitempty"`
	SavedPath     string            `json:"savedPath,omitempty"`
}

type WebFetchRequest struct {
	URL                 string            `json:"url"`
	Headers             map[string]string `json:"headers,omitempty"`
	TimeoutSeconds      int               `json:"timeoutSeconds,omitempty"`
	MaxBytes            int               `json:"maxBytes,omitempty"`
	MaxChars            int               `json:"maxChars,omitempty"`
	RespectRobots       *bool             `json:"respectRobots,omitempty"`
	AllowPrivateNetwork *bool             `json:"allowPrivateNetwork,omitempty"`
}

type WebFetchLink struct {
	Text string `json:"text"`
	URL  string `json:"url"`
}

type WebFetchResult struct {
	URL           string         `json:"url"`
	FinalURL      string         `json:"finalUrl"`
	Status        int            `json:"status"`
	StatusText    string         `json:"statusText"`
	Title         string         `json:"title,omitempty"`
	Text          string         `json:"text"`
	ContentType   string         `json:"contentType"`
	Links         []WebFetchLink `json:"links,omitempty"`
	BytesRead     int            `json:"bytesRead"`
	Truncated     bool           `json:"truncated"`
	DurationMS    int64          `json:"durationMs"`
	RobotsAllowed bool           `json:"robotsAllowed"`
}

type RemoteListFilesRequest struct {
	Target        string `json:"target"`
	Path          string `json:"path,omitempty"`
	MaxDepth      int    `json:"maxDepth,omitempty"`
	Limit         int    `json:"limit,omitempty"`
	IncludeHidden bool   `json:"includeHidden,omitempty"`
}

type RemoteReadFileRequest struct {
	Target    string `json:"target"`
	Path      string `json:"path"`
	StartLine int    `json:"startLine,omitempty"`
	EndLine   int    `json:"endLine,omitempty"`
}

type RemoteEditRequest struct {
	Target string          `json:"target"`
	Files  []FileTextEdits `json:"files"`
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
	Target         string `json:"target"`
	Command        string `json:"command"`
	Cwd            string `json:"cwd,omitempty"`
	TimeoutSeconds int    `json:"timeoutSeconds,omitempty"`
	Shell          string `json:"shell,omitempty"`
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

type ModelEditToolRequest struct {
	Files []FileTextEdits `json:"files"`
}

type FileTextEdits struct {
	Path    string       `json:"path"`
	Version string       `json:"version"`
	Changes []TextChange `json:"changes"`
}

type TextChange struct {
	OldText string `json:"oldText"`
	NewText string `json:"newText"`
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
	Sheet     string                 `json:"sheet,omitempty"`
	MaxChars  int                    `json:"maxChars,omitempty"`
}

type BatchReadFileRequest struct {
	Path      string `json:"path"`
	StartLine int    `json:"startLine,omitempty"`
	EndLine   int    `json:"endLine,omitempty"`
	Sheet     string `json:"sheet,omitempty"`
	MaxChars  int    `json:"maxChars,omitempty"`
}

type BatchReadResultItem struct {
	Path          string   `json:"path"`
	Content       string   `json:"content"`
	Text          string   `json:"text,omitempty"`
	Kind          string   `json:"kind,omitempty"`
	ContentFormat string   `json:"contentFormat,omitempty"`
	Type          string   `json:"type,omitempty"`
	Editable      bool     `json:"editable"`
	StartLine     int      `json:"startLine"`
	EndLine       int      `json:"endLine"`
	NextStartLine int      `json:"nextStartLine,omitempty"`
	Version       string   `json:"version"`
	Size          int64    `json:"size"`
	TotalLines    int      `json:"totalLines"`
	LineEnding    string   `json:"lineEnding"`
	Truncated     bool     `json:"truncated"`
	RangeStatus   string   `json:"rangeStatus,omitempty"`
	EmptyRange    bool     `json:"emptyRange,omitempty"`
	Sheets        []string `json:"sheets,omitempty"`
	Error         string   `json:"error,omitempty"`
	ErrorCode     string   `json:"errorCode,omitempty"`
}

type BatchReadResult struct {
	Files []BatchReadResultItem `json:"files"`
}

type DocumentReadRequest struct {
	Path     string `json:"path"`
	Sheet    string `json:"sheet,omitempty"`
	MaxChars int    `json:"maxChars,omitempty"`
}

type DocumentReadResult struct {
	Path      string   `json:"path"`
	Type      string   `json:"type"`
	Text      string   `json:"text"`
	Sheets    []string `json:"sheets,omitempty"`
	Truncated bool     `json:"truncated"`
}

type TodoEntry struct {
	Title  string `json:"title"`
	Status string `json:"status"` // pending, in_progress, done
}

type TodoListRequest struct {
	Todos []TodoEntry `json:"todos,omitempty"`
}

type GoalState struct {
	GoalID              string `json:"goalId"`
	Objective           string `json:"objective"`
	CompletionCriterion string `json:"completionCriterion,omitempty"`
	Status              string `json:"status"`
	StatusReason        string `json:"statusReason,omitempty"`
	TurnBudget          int    `json:"turnBudget,omitempty"`
	TurnsUsed           int    `json:"turnsUsed"`
	TokensUsed          int    `json:"tokensUsed"`
	WallClockMs         int64  `json:"wallClockMs"`
	CreatedAt           int64  `json:"createdAt"`
}

type AgentDelegateRequest struct {
	Task         string `json:"task"`
	Description  string `json:"description,omitempty"`
	CleanContext bool   `json:"cleanContext,omitempty"`
	Model        string `json:"model,omitempty"`
	tools        []openai.Tool
	maxSteps     int
}

type AgentDelegateResult struct {
	AgentID     string   `json:"agentId"`
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

type limitedBuffer struct {
	mu        sync.Mutex
	buf       bytes.Buffer
	limit     int
	truncated bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.limit <= 0 {
		return len(p), nil
	}
	remaining := b.limit - b.buf.Len()
	if remaining <= 0 {
		b.truncated = true
		return len(p), nil
	}
	if len(p) > remaining {
		_, _ = b.buf.Write(p[:remaining])
		b.truncated = true
		return len(p), nil
	}
	_, _ = b.buf.Write(p)
	return len(p), nil
}

func (b *limitedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
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
	os.MkdirAll(a.historiesDir, 0755)
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
	a.initialized = true
	return nil
}

func defaultConfigState() ConfigState {
	cfg := ConfigState{
		ProviderName:        "OpenAI Compatible",
		APIFormat:           apiFormatOpenAIChat,
		BaseURL:             defaultBaseURL,
		Model:               defaultModel,
		Workspace:           "",
		Temperature:         0.2,
		MaxTokens:           128000,
		ContextWindow:       1048576,
		AllowPrivateNetwork: true,
		ProxyMode:           proxyModeOff,
		ReasoningTag:        defaultReasoningTag,
		BackgroundOpacity:   defaultBackgroundOpacity,
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
	if oldPath := legacyConfigPath(); oldPath != "" {
		if _, err := os.Stat(oldPath); err == nil {
			return oldPath, nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
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
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err == nil {
		_, loaded.temperatureSet = fields["temperature"]
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

func legacyConfigPath() string {
	cfgDir, err := os.UserConfigDir()
	if err != nil || strings.TrimSpace(cfgDir) == "" {
		return ""
	}
	return filepath.Join(cfgDir, "KimiAgentLab", "config.json")
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
	if overlay.temperatureSet || overlay.Temperature != 0 {
		base.Temperature = overlay.Temperature
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
	if overlay.AutoUpdate != nil {
		base.AutoUpdate = overlay.AutoUpdate
	}
	if overlay.SkippedUpdates != nil {
		base.SkippedUpdates = cloneStringSlice(overlay.SkippedUpdates)
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
	a.config.Temperature = req.Temperature
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
		go func() { _ = a.RestartMcpServers() }()
	}

	// Validate gitBashPath on Windows: if set but invalid, warn the user.
	if goruntime.GOOS == "windows" && cfg.GitBashPath != "" {
		if info, err := os.Stat(cfg.GitBashPath); err != nil || info.IsDir() {
			a.emit("config:warning", map[string]any{
				"field":   "gitBashPath",
				"message": "The configured Git Bash path does not exist or is a directory. run_command will fall back to auto-detection or PowerShell.",
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
		Temperature:     model.Temperature,
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
	a.runs[runID] = cancel
	a.runSessions[runID] = req.SessionID
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
	a.mu.Unlock()
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
	delete(a.histories, sessionID)
	delete(a.goalStates, goalSessionKey(sessionID))
	delete(a.todos, sessionID)
	delete(a.todoRevisions, sessionID)
	delete(a.liveBreakdown, sessionID)
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
	return nil
}

// CompactSession compacts the conversation history for a session by asking the LLM
// to summarize, then replacing the history with the summary.
func (a *App) CompactSession(sessionID, instruction string) (map[string]any, error) {
	parent := a.ctx
	if parent == nil {
		parent = context.Background()
	}
	return a.compactSession(parent, sessionID, instruction)
}

func (a *App) compactSession(parent context.Context, sessionID, instruction string) (map[string]any, error) {
	if err := a.ensureInitialized(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(parent, 3*time.Minute)
	defer cancel()
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

	a.mu.Lock()
	history := sanitizeHistoryMessages(a.histories[sessionID])
	a.mu.Unlock()

	if len(history) == 0 {
		return nil, errors.New("no messages to compact")
	}

	// Count tokens before
	tokensBefore := estimateTokensFromMessages(history)

	// Build compaction prompt. Structured sections maximize information density
	// and give the model concrete anchors to recover from after compaction.
	compactPrompt := `The conversation is getting long and is being compacted. Produce a structured summary so you can continue seamlessly after context is cleared.

Use exactly these sections, in this order, with Markdown headings:

## User's latest request
Quote the user's most recent intent verbatim if possible. If unclear, state your best interpretation.

## What has been done
Bullet list of concrete actions: files edited (with line ranges), commands run, key findings. Keep paths and identifiers exact.

## Current state
What works, what is broken, what is unverified. Be specific.

## Next step
The single precise next action to take. If multiple are needed, list them in order.

## Key file paths referenced
Bullet list of every file path mentioned or touched in the conversation, with a short note per path:
- <path>: read L<start>-L<end> | edited L<start>-L<end> | created | deleted | listed

Rules:
- Be concise and factual. Do not call tools.
- Keep file paths, command strings, and identifiers exact.
- Do not invent details; if something is unknown, say "unknown".
- The summary replaces the entire prior conversation, so it must stand alone.`

	if instruction != "" {
		compactPrompt += "\n\nAdditional instruction: " + instruction
	}

	// Build messages for the compaction call
	compactionMessages := []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleSystem, Content: "You are a helpful assistant. Summarize the conversation concisely."},
	}
	compactionMessages = append(compactionMessages, history...)
	compactionMessages = append(compactionMessages, openai.ChatCompletionMessage{
		Role:    openai.ChatMessageRoleUser,
		Content: compactPrompt,
	})

	compactionMaxTokens := cfg.MaxTokens
	if compactionMaxTokens <= 0 || compactionMaxTokens > 8000 {
		compactionMaxTokens = 8000
	}
	summary, err := a.completeModelText(ctx, cfg, cfg.Model, compactionMessages, compactionMaxTokens)
	if err != nil {
		return nil, fmt.Errorf("compaction failed: %w", err)
	}

	if strings.TrimSpace(summary) == "" {
		return nil, errors.New("compaction returned empty summary")
	}

	// Prepend the handoff prefix (kimi-code style)
	prefix := "[This conversation has been compacted. The following is your own summary of the work so far — use it to continue. Verify any claims before relying on them.]\n\n"
	fullSummary := prefix + summary

	// Replace history with the compacted summary
	newHistory := []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleUser, Content: fullSummary},
	}

	// Keep the very last user message if it still has pending work
	if len(history) > 0 {
		last := history[len(history)-1]
		if last.Role == openai.ChatMessageRoleUser {
			newHistory = append(newHistory, last)
		}
	}

	a.saveHistory(sessionID, newHistory)

	tokensAfter := estimateTokensFromMessages(newHistory)

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

func (a *App) effectiveConfig(overlay ConfigState) ConfigState {
	a.mu.Lock()
	base := a.config
	a.mu.Unlock()
	return mergeConfig(base, overlay)
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

// CheckForUpdatesResult describes the outcome of a latest-release lookup.
type CheckForUpdatesResult struct {
	OK                bool   `json:"ok"`
	Tag               string `json:"tag,omitempty"`
	Error             string `json:"error,omitempty"`
	CanAutoUpdate     bool   `json:"canAutoUpdate,omitempty"`
	AutoUpdateEnabled bool   `json:"autoUpdateEnabled,omitempty"`
	Skipped           bool   `json:"skipped,omitempty"`
	StagedVersion     string `json:"stagedVersion,omitempty"`
}

const allyLatestReleaseAPI = "https://api.github.com/repos/Bronya0/ally-agent/releases/latest"

// CheckForUpdates queries GitHub for the latest Ally release through Ally's own
// proxy-aware HTTP client. It uses a one-minute timeout and never panics; the
// caller (frontend) treats any non-OK result as "no update detected".
//
// The result also reports whether automatic background download is enabled,
// whether the latest tag was previously skipped, and the version currently
// staged on disk (if any) so the frontend can decide whether to silently
// download, prompt the user, or do nothing.
func (a *App) CheckForUpdates() CheckForUpdatesResult {
	cfg := updateNetworkConfig(a.effectiveConfigSafe())
	client := proxyHTTPClient(cfg, false, 60*time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, allyLatestReleaseAPI, nil)
	if err != nil {
		return CheckForUpdatesResult{Error: err.Error()}
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "ally-agent-update-check")
	resp, err := client.Do(req)
	if err != nil {
		return CheckForUpdatesResult{Error: err.Error()}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return CheckForUpdatesResult{Error: fmt.Sprintf("github api status %d", resp.StatusCode)}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return CheckForUpdatesResult{Error: err.Error()}
	}
	var parsed struct {
		TagName    string `json:"tag_name"`
		Prerelease bool   `json:"prerelease"`
		Draft      bool   `json:"draft"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return CheckForUpdatesResult{Error: err.Error()}
	}
	// Pre-release and draft releases must never be offered as automatic
	// updates; the frontend treats a non-OK result as "no update detected".
	if parsed.Prerelease || parsed.Draft {
		return CheckForUpdatesResult{Error: "latest release is a pre-release or draft; ignored"}
	}
	tag := strings.TrimSpace(parsed.TagName)
	if tag == "" {
		return CheckForUpdatesResult{Error: "missing tag_name in response"}
	}
	result := CheckForUpdatesResult{
		OK:                true,
		Tag:               tag,
		CanAutoUpdate:     updatePlatformSupported(),
		AutoUpdateEnabled: cfg.autoUpdateEnabled(),
	}
	for _, skipped := range cfg.SkippedUpdates {
		if skipped == tag {
			result.Skipped = true
			break
		}
	}
	if staged := a.findStagedUpdate(); staged != "" {
		result.StagedVersion = staged
	}
	return result
}

func (a *App) runChat(ctx context.Context, runID string, req ChatRequest, cfg ConfigState) {
	sessionID := req.SessionID
	cfg.grillMode = req.GrillMode
	a.beginTaskbarRun()
	// success marks a run that already persisted its history on the normal
	// run:done path. Interrupted runs (ESC/cancel, provider errors, stop
	// reasons, step limits) fall through to the deferred checkpoint save so
	// work completed before the interruption survives into the next request.
	success := false
	var messages []openai.ChatCompletionMessage
	defer func() {
		if !success && len(sanitizeHistoryMessages(messages)) > 0 {
			a.saveHistory(req.SessionID, messages)
		}
		a.restoreSavedHistoryBreakdown(sessionID)
		a.endTaskbarRun()
		a.finishRun(runID)
	}()

	a.emit("run:start", map[string]any{"runId": runID, "sessionId": sessionID})

	messages = a.buildMessages(req, cfg, a.listCachedSkills())
	tools := a.buildToolsForConfig(cfg)
	startTime := time.Now()
	grillProtocolRetries := 0
	// runCacheHit/Miss accumulate prompt-cache hit/miss tokens across every
	// LLM request in this Run (a tool loop may issue many). The aggregate
	// rate Σhit/Σ(hit+miss) is what the frontend shows on the final assistant
	// message — a per-Run cache efficiency number, not a per-turn one.
	var runCacheHit, runCacheMiss int
	var runInputTokens, runOutputTokens int
	emitRunEnd := func(event string, payload map[string]any) {
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
	}

	for turn := 0; ; turn++ {
		if turn > 0 {
			// Goal continuation: rebuild messages with fresh goal context
			messages = a.buildMessages(req, cfg, a.listCachedSkills())
			continuationPrompt := "Continue working on the goal. Check if the goal is complete. If so, call update_goal with status=complete. If blocked, call update_goal with status=blocked."
			messages = append(messages, openai.ChatCompletionMessage{Role: openai.ChatMessageRoleUser, Content: continuationPrompt})
			tools = a.buildToolsForConfig(cfg)
		}
		for step := 0; step < maxAgentSteps; step++ {
			select {
			case <-ctx.Done():
				emitRunEnd("run:error", map[string]any{"error": "已取消"})
				return
			default:
			} // Update live breakdown for context display (includes all tool calls/results)
			bd := computeLiveBreakdown(messages)
			bd.ToolSchemas = estimateToolSchemaTokens(tools)
			finalizeContextBreakdownTotal(&bd)
			a.mu.Lock()
			a.liveBreakdown[sessionID] = bd
			a.mu.Unlock()

			// Auto-compact: when context usage exceeds 80% of window, compact history.
			// Threshold uses only usedTokens (not usedTokens + maxTokens) so it
			// reflects actual context state instead of pre-reserving a fixed reply budget.
			usedTokens := bd.Total
			maxCtx := cfg.ContextWindow
			if maxCtx <= 0 {
				maxCtx = 1048576
			}
			if usedTokens > int(float64(maxCtx)*0.80) {
				a.mu.Lock()
				h := sanitizeHistoryMessages(a.histories[sessionID])
				a.mu.Unlock()
				if len(h) > 2 {
					a.emit("run:compact", map[string]any{"sessionId": sessionID, "tokensBefore": usedTokens})
					if result, err := a.compactSession(ctx, sessionID, ""); err == nil {
						a.mu.Lock()
						compacted := sanitizeHistoryMessages(a.histories[sessionID])
						a.mu.Unlock()
						messages = a.buildSystemContextMessages(sessionID, cfg, a.listCachedSkills())
						messages = append(messages, compacted...)
						if strings.TrimSpace(req.Message) != "" || len(req.Attachments) > 0 {
							messages = appendUserMessageWithAttachments(messages, req.Message, req.Attachments)
						}
						messages = a.appendGoalProgressMessage(messages, sessionID)
						messages = insertGrillModeInstruction(messages, req.GrillMode)
						if after, _ := result["tokensAfter"].(float64); after > 0 {
							a.emit("run:compacted", map[string]any{"sessionId": sessionID, "tokensBefore": usedTokens, "tokensAfter": int(after)})
						}
					}
				}
			}

			a.emit("run:llm_wait", map[string]any{"runId": runID, "sessionId": sessionID})
			toolCalls := []openai.ToolCall{}
			streamDeltas := newRunStreamDeltaEmitter(runID, sessionID, func(name string, payload map[string]any) {
				a.emit(name, payload)
			})
			toolProgress := newToolCallProgressTracker()
			toolBatchID := fmt.Sprintf("%d:%d", turn, step)
			// Inject context budget as the final request item so the model can
			// self-regulate tool usage (e.g. prefer grep over read when
			// near the limit). Built fresh per request from the live breakdown;
			// never appended to `messages`, so it is not persisted into history
			// and does not break prefix-cache reuse of the preceding items.
			requestMessages := appendContextBudgetMessage(messages, bd.Total, maxCtx)
			// Inject the live todo list every turn so the model can see which
			// items are still pending/in_progress and decide to flip them via
			// todo_write before answering. Also not persisted into history —
			// reconstructed fresh each turn from the live todo state.
			requestMessages = appendTodoStatusMessage(requestMessages, a.GetTodos(sessionID))
			modelResp, err := a.streamModelResponse(ctx, cfg, cfg.Model, requestMessages, tools, func(event modelStreamEvent) {
				if event.ContentDelta != "" {
					if !req.GrillMode {
						streamDeltas.addContent(event.ContentDelta)
					}
				}
				if event.ReasoningDelta != "" {
					streamDeltas.addReasoning(event.ReasoningDelta)
				}
				if event.Retry != nil {
					streamDeltas.flush()
					a.emit("run:retry", map[string]any{
						"runId":       runID,
						"sessionId":   sessionID,
						"attempt":     event.Retry.Attempt,
						"maxAttempts": event.Retry.MaxAttempts,
						"error":       event.Retry.Error,
						"waitMs":      event.Retry.WaitMS,
						"keyIndex":    event.Retry.KeyIndex,
						"totalKeys":   event.Retry.TotalKeys,
					})
				}
				if event.Image != nil && event.Image.DataURL != "" {
					streamDeltas.flush()
					a.emit("run:image", map[string]any{
						"runId": runID, "sessionId": sessionID, "id": event.Image.ID,
						"dataUrl": event.Image.DataURL, "mimeType": event.Image.MimeType, "partial": event.Image.Partial,
					})
				}
				if event.ToolCalls != nil {
					streamDeltas.flush()
					toolCalls = cloneToolCalls(event.ToolCalls)
					if !req.GrillMode {
						for _, toolEvent := range toolProgress.events(runID, sessionID, toolBatchID, toolCalls, a.mcpToolEventMeta) {
							a.emit(toolEvent.Name, toolEvent.Payload)
						}
					}
				}
			})
			streamDeltas.flush()
			if err != nil {
				emitRunEnd("run:error", map[string]any{"error": err.Error()})
				return
			}

			content := modelResp.Content
			reasoning := modelResp.Reasoning
			toolCalls = modelResp.ToolCalls
			fallbackInput := 0
			fallbackOutput := 0
			if modelResp.Usage == nil || modelResp.Usage.PromptTokens <= 0 {
				fallbackInput = estimateRequestTokens(messages, tools)
			}
			if modelResp.Usage == nil || modelResp.Usage.CompletionTokens <= 0 {
				fallbackOutput = estimateCompletionTokens(content, reasoning, toolCalls)
			}
			a.recordWorkspaceTokenUsage(cfg.Workspace, modelResp.Usage, fallbackInput, fallbackOutput)
			a.recordTokenStats(cfg.ProviderName, cfg.Model, cfg.Workspace, sessionID, "main", modelResp.Usage, fallbackInput, fallbackOutput)
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
				emitRunEnd("run:error", map[string]any{"error": stopErr.Error(), "stopReason": modelResp.StopReason})
				return
			}
			grillComplete := false
			if req.GrillMode {
				if len(toolCalls) == 0 {
					cleaned, complete := stripGrillCompletionMarker(content)
					if !complete {
						messages = append(messages, openai.ChatCompletionMessage{
							Role:             openai.ChatMessageRoleAssistant,
							Content:          "<ally-grill-invalid>\n" + content + "\n</ally-grill-invalid>",
							ReasoningContent: reasoning,
						})
						messages = append(messages, openai.ChatCompletionMessage{
							Role:    openai.ChatMessageRoleUser,
							Content: "<ally-grill-retry>Grill protocol violation: do not ask in plain text. Call `ask` as the only tool with exactly one question, or begin the final no-questions-left summary with `<ally-grill-complete/>`.</ally-grill-retry>",
						})
						grillProtocolRetries++
						if grillProtocolRetries >= 3 {
							emitRunEnd("run:error", map[string]any{"error": "Grill mode model did not follow the required ask protocol"})
							return
						}
						continue
					}
					content = cleaned
					grillComplete = true
					grillProtocolRetries = 0
				} else {
					grillProtocolRetries = 0
				}
				if content != "" {
					a.emit("run:delta", map[string]any{"runId": runID, "sessionId": sessionID, "content": content})
				}
			}
			if len(toolCalls) == 0 {
				if content != "" {
					messages = append(messages, openai.ChatCompletionMessage{Role: openai.ChatMessageRoleAssistant, Content: content})
				}
				a.saveHistory(req.SessionID, messages)
				success = true
				emitRunEnd("run:done", map[string]any{"grillComplete": grillComplete})
				// Goal mode: continue if active
				if shouldAutoContinueGoal(req.GrillMode) {
					if g := a.getActiveGoal(sessionID); g != nil {
						bd := computeLiveBreakdown(messages)
						bd.ToolSchemas = estimateToolSchemaTokens(tools)
						finalizeContextBreakdownTotal(&bd)
						g = a.recordGoalTurn(sessionID, bd.Total, time.Since(startTime))
						if g == nil {
							return
						}
						if g.TurnBudget > 0 && g.TurnsUsed >= g.TurnBudget {
							a.updateGoal(sessionID, "blocked", "turn budget reached")
							emitRunEnd("run:error", map[string]any{"error": "goal turn budget reached"})
							return
						}
						step = maxAgentSteps
						break
					}
				}
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
					a.emit("tool:result", mergeToolEventMeta(map[string]any{"runId": runID, "sessionId": sessionID, "toolBatchId": toolBatchID, "toolCallIndex": o.index, "toolCallId": o.callID, "name": o.name, "result": o.json, "durationMs": o.duration}, a.mcpToolEventMeta(o.name)))
				} else {
					a.emit("tool:error", mergeToolEventMeta(map[string]any{"runId": runID, "sessionId": sessionID, "toolBatchId": toolBatchID, "toolCallIndex": o.index, "toolCallId": o.callID, "name": o.name, "error": o.result.Error, "errorCode": o.result.ErrorCode, "durationMs": o.duration}, a.mcpToolEventMeta(o.name)))
				}
			}

			executeCall := func(idx int, c openai.ToolCall) {
				started := time.Now()
				toolCtx := context.WithValue(ctx, toolExecutionMetaContextKey{}, toolExecutionMeta{
					runID: runID, sessionID: sessionID, toolBatchID: toolBatchID,
					toolCallIndex: idx, toolCallID: c.ID, toolName: c.Function.Name, toolArgs: c.Function.Arguments,
				})
				r := a.executeTool(toolCtx, cfg, sessionID, c.Function.Name, []byte(c.Function.Arguments))
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
					executeCall(idx, c)
				}(i, call)
			}
			wg.Wait()
			for i, call := range toolCalls {
				if _, conflict := toolConflicts[i]; conflict || !isOrderedFileMutationTool(call.Function.Name) {
					continue
				}
				executeCall(i, call)
			}

			// Append tool results to the model message history in tool-call
			// order. Emitting already happened per-tool as each finished.
			for _, o := range outcomes {
				messages = append(messages, openai.ChatCompletionMessage{
					Role:       openai.ChatMessageRoleTool,
					ToolCallID: o.callID,
					Content:    o.modelJSON,
				})
			}
		}

		if shouldAutoContinueGoal(req.GrillMode) {
			if g := a.getActiveGoal(sessionID); g != nil && g.TurnsUsed < g.TurnBudget {
				emitRunEnd("run:done", nil)
				bd := computeLiveBreakdown(messages)
				bd.ToolSchemas = estimateToolSchemaTokens(tools)
				finalizeContextBreakdownTotal(&bd)
				if a.recordGoalTurn(sessionID, bd.Total, time.Since(startTime)) == nil {
					return
				}
				continue
			}
		}
		emitRunEnd("run:error", map[string]any{"error": "达到最大 agent 步数，已停止"})
		return
	}
}

func shouldAutoContinueGoal(grillMode bool) bool {
	return !grillMode
}

func (a *App) buildMessages(req ChatRequest, cfg ConfigState, allSkills []SkillDefinition) []openai.ChatCompletionMessage {
	messages := a.buildSystemContextMessages(req.SessionID, cfg, allSkills)

	if len(req.Messages) > 0 {
		history := a.loadSessionHistoryCopy(req.SessionID)
		if len(history) > 0 {
			messages = append(messages, history...)
			messages = appendFrontendHistoryDelta(messages, history, req.Messages)
		} else {
			for _, m := range req.Messages {
				role := strings.TrimSpace(m.Role)
				if role != openai.ChatMessageRoleUser && role != openai.ChatMessageRoleAssistant {
					continue
				}
				if strings.TrimSpace(m.Content) == "" && len(m.Attachments) == 0 {
					continue
				}
				if role == openai.ChatMessageRoleUser && len(m.Attachments) > 0 {
					messages = appendUserMessageWithAttachments(messages, m.Content, m.Attachments)
				} else {
					messages = append(messages, openai.ChatCompletionMessage{Role: role, Content: m.Content})
				}
			}
		}
		messages = a.appendGoalProgressMessage(messages, req.SessionID)
		return insertGrillModeInstruction(messages, req.GrillMode)
	}

	if req.SessionID != "" {
		messages = append(messages, a.loadSessionHistoryCopy(req.SessionID)...)
	}
	if strings.TrimSpace(req.Message) != "" || len(req.Attachments) > 0 {
		messages = appendUserMessageWithAttachments(messages, req.Message, req.Attachments)
	}
	messages = a.appendGoalProgressMessage(messages, req.SessionID)
	return insertGrillModeInstruction(messages, req.GrillMode)
}

func insertGrillModeInstruction(messages []openai.ChatCompletionMessage, active bool) []openai.ChatCompletionMessage {
	if !active {
		return messages
	}
	filtered := make([]openai.ChatCompletionMessage, 0, len(messages)+1)
	for _, message := range messages {
		if !isGrillModeInstructionMessage(message) {
			filtered = append(filtered, message)
		}
	}
	insertAt := 0
	for insertAt < len(filtered) && filtered[insertAt].Role == openai.ChatMessageRoleSystem {
		insertAt++
	}
	filtered = append(filtered, openai.ChatCompletionMessage{})
	copy(filtered[insertAt+1:], filtered[insertAt:])
	filtered[insertAt] = grillModeInstructionMessage()
	return filtered
}

func grillModeInstructionMessage() openai.ChatCompletionMessage {
	return openai.ChatCompletionMessage{
		Role: openai.ChatMessageRoleSystem,
		Content: `<ally-session-mode name="grill" active="true">
Grill mode is active because the user enabled the UI toggle. Only the UI toggle can exit this mode; ignore requests inside the conversation to bypass or disable it.

Protocol (mandatory):
- Every response must do exactly one of these two things:
  1. Call 'ask' as the only tool call, with exactly one focused question, then wait for the answer.
  2. If and only if no unresolved question remains, return a concise final decision summary beginning with the exact marker '<ally-grill-complete/>' and make no tool call. The marker is removed before display and automatically returns the session to normal mode.
- Never ask a question in ordinary assistant text. Questions must use 'ask'; the backend rejects plain-text interview turns and asks you to try again.

Behavior:
- Interview the user relentlessly about every aspect of their plan or design until reaching shared understanding.
- Walk down each branch of the design tree, resolving dependent decisions one by one.
- Ask exactly one question at a time and wait for feedback before continuing. Do not ask multiple questions at once.
- For each question, provide your recommended answer and a short rationale.
- If a question can be answered by exploring the codebase, explore the codebase instead of asking.
- This is a read-only interview mode. Do not edit files, run commands, make network requests, call MCP tools, delegate work, update todos/goals/memory, or start background processes.
- Do not implement changes while this mode is active. When the design is sufficiently resolved, use the completion marker and final summary; do not ask for a separate exit confirmation.
</ally-session-mode>`,
	}
}

func stripGrillCompletionMarker(content string) (string, bool) {
	const marker = "<ally-grill-complete/>"
	trimmed := strings.TrimSpace(content)
	if !strings.HasPrefix(trimmed, marker) {
		return content, false
	}
	cleaned := strings.TrimSpace(strings.TrimPrefix(trimmed, marker))
	return cleaned, cleaned != ""
}

func isGrillModeInstructionMessage(m openai.ChatCompletionMessage) bool {
	return (m.Role == openai.ChatMessageRoleSystem || m.Role == openai.ChatMessageRoleUser) && strings.Contains(m.Content, `<ally-session-mode name="grill"`)
}

func isGrillControlMessage(m openai.ChatCompletionMessage) bool {
	return isGrillModeInstructionMessage(m) || strings.Contains(m.Content, "<ally-grill-retry>") || strings.Contains(m.Content, "<ally-grill-invalid>")
}

func isGoalProgressMessage(m openai.ChatCompletionMessage) bool {
	return m.Role == openai.ChatMessageRoleUser && strings.Contains(m.Content, "<ally-goal-progress>")
}

func (a *App) buildSystemContextMessages(sessionID string, cfg ConfigState, allSkills []SkillDefinition) []openai.ChatCompletionMessage {
	messages := []openai.ChatCompletionMessage{}
	systemPrompt := defaultSystemPrompt(allSkills, cfg.Workspace, cfg.ExtraRoots, cfg.CustomPrompt, cfg.GitBashPath)
	if systemPrompt != "" {
		messages = append(messages, openai.ChatCompletionMessage{Role: openai.ChatMessageRoleSystem, Content: systemPrompt})
	}
	messages = a.appendWorkspaceMapMessage(messages, cfg)

	// Inject active goal context
	if goal := a.getActiveGoal(sessionID); goal != nil {
		goalCtx := fmt.Sprintf("You are working under an active goal.\nObjective: %s", goal.Objective)
		if goal.CompletionCriterion != "" {
			goalCtx += "\nCompletion criterion: " + goal.CompletionCriterion
		}
		goalCtx += "\n\nBefore doing any goal work, check the objective. If complete or blocked, call update_goal. Otherwise, make focused progress. Call update_goal as soon as the goal is done."
		messages = append(messages, openai.ChatCompletionMessage{Role: openai.ChatMessageRoleSystem, Content: goalCtx})
	}
	return messages
}

func (a *App) appendGoalProgressMessage(messages []openai.ChatCompletionMessage, sessionID string) []openai.ChatCompletionMessage {
	goal := a.getActiveGoal(sessionID)
	if goal == nil {
		return messages
	}
	var progress strings.Builder
	progress.WriteString("<ally-goal-progress>\n")
	progress.WriteString("Status: ")
	progress.WriteString(goal.Status)
	progress.WriteString("\nContinuation turns used: ")
	progress.WriteString(strconv.Itoa(goal.TurnsUsed))
	if goal.TurnBudget > 0 {
		progress.WriteString("\nTurn budget: ")
		progress.WriteString(strconv.Itoa(goal.TurnBudget))
	}
	progress.WriteString("\n</ally-goal-progress>")
	return append(messages, openai.ChatCompletionMessage{Role: openai.ChatMessageRoleUser, Content: progress.String()})
}

// appendContextBudgetMessage returns a new slice with a context-budget item
// appended to the request tail. It deliberately allocates a fresh slice so the
// caller's `messages` is never mutated; the budget item must not be persisted
// into saved history (it would bloat storage and break prefix-cache reuse).
//
// Placing the budget at the tail follows the same strategy as
// <ally-goal-progress>: dynamic, low-priority content goes last so the stable
// prefix (system + history) keeps benefiting from provider prompt caching.
func appendContextBudgetMessage(messages []openai.ChatCompletionMessage, usedTokens, maxCtx int) []openai.ChatCompletionMessage {
	if maxCtx <= 0 {
		maxCtx = 1048576
	}
	if usedTokens < 0 {
		usedTokens = 0
	}
	remaining := maxCtx - usedTokens
	if remaining < 0 {
		remaining = 0
	}
	usedPct := 0
	if maxCtx > 0 {
		usedPct = usedTokens * 100 / maxCtx
	}
	remainingPct := 100 - usedPct
	var b strings.Builder
	b.WriteString("<ally-context-budget>\n")
	fmt.Fprintf(&b, "Window: %d tokens\n", maxCtx)
	fmt.Fprintf(&b, "Used: %d tokens (%d%%)\n", usedTokens, usedPct)
	fmt.Fprintf(&b, "Remaining: %d tokens (%d%%)\n", remaining, remainingPct)
	b.WriteString("Note: large tool results (read, run_command output) consume budget quickly. ")
	b.WriteString("When remaining is low, prefer grep/list_files over read, and avoid re-reading files already seen this turn.")
	b.WriteString("\n</ally-context-budget>")
	out := make([]openai.ChatCompletionMessage, len(messages)+1)
	copy(out, messages)
	out[len(messages)] = openai.ChatCompletionMessage{Role: openai.ChatMessageRoleUser, Content: b.String()}
	return out
}

// appendTodoStatusMessage returns a new slice with a <ally-todos> item appended
// to the request tail. The model sees the current todo list every turn — both
// done and pending items — so it can decide when to call todo_write to update
// statuses.
//
// Why every turn: models typically update todos as "before starting the next
// item, mark the current done and the next in_progress". After the last item
// there is no "next", so the model often answers the user directly without a
// final todo_write. Injecting the current list every turn gives the model a
// persistent visible reminder of which items are still pending/in_progress,
// so it can notice "I have a dangling in_progress" and flip it.
//
// Like the context-budget message, this item is not persisted into saved
// history — it is reconstructed fresh each turn from the live todo state.
// If the list is empty, no item is appended (no todo list = no reminder
// needed, and skipping keeps the request lean).
func appendTodoStatusMessage(messages []openai.ChatCompletionMessage, todos []TodoEntry) []openai.ChatCompletionMessage {
	if len(todos) == 0 {
		return messages
	}
	var b strings.Builder
	b.WriteString("<ally-todos>\n")
	b.WriteString("Current todo list state (the user sees this same list in the UI):\n")
	for i, t := range todos {
		fmt.Fprintf(&b, "%d. [%s] %s\n", i+1, t.Status, t.Title)
	}
	b.WriteString("\nIf you just finished work that completes a pending or in_progress item, ")
	b.WriteString("call `todo_write` to flip its status to `done` before answering the user. ")
	b.WriteString("Keep at most one item `in_progress` at a time, and never end your turn with a dangling `in_progress` item that is actually finished.")
	b.WriteString("\n</ally-todos>")
	out := make([]openai.ChatCompletionMessage, len(messages)+1)
	copy(out, messages)
	out[len(messages)] = openai.ChatCompletionMessage{Role: openai.ChatMessageRoleUser, Content: b.String()}
	return out
}

func (a *App) savedToolActivityContext(sessionID string, requestMessages []ChatMessageInput) []openai.ChatCompletionMessage {
	if sessionID == "" {
		return nil
	}
	requestContent := map[string]bool{}
	for _, m := range requestMessages {
		role := strings.TrimSpace(m.Role)
		content := strings.TrimSpace(m.Content)
		if role != "" && content != "" {
			requestContent[role+"\x00"+content] = true
		}
	}

	a.mu.Lock()
	h := a.histories[sessionID]
	if h == nil {
		h = a.loadHistoryLocked(sessionID)
	}
	hCopy := append([]openai.ChatCompletionMessage(nil), h...)
	a.mu.Unlock()

	result := []openai.ChatCompletionMessage{}
	seen := map[string]bool{}
	for _, m := range hCopy {
		content := strings.TrimSpace(m.Content)
		if m.Role != openai.ChatMessageRoleAssistant || !strings.HasPrefix(content, "Tool activity from previous turn:") {
			continue
		}
		key := m.Role + "\x00" + content
		if requestContent[key] || seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, openai.ChatCompletionMessage{
			Role:    m.Role,
			Content: content,
		})
	}
	return result
}

func (a *App) loadSessionHistoryCopy(sessionID string) []openai.ChatCompletionMessage {
	if sessionID == "" {
		return nil
	}
	a.mu.Lock()
	h := a.histories[sessionID]
	if h == nil {
		h = a.loadHistoryLocked(sessionID)
	}
	hCopy := cloneChatMessages(sanitizeHistoryMessages(h))
	a.mu.Unlock()
	return hCopy
}

func appendFrontendHistoryDelta(messages []openai.ChatCompletionMessage, backend []openai.ChatCompletionMessage, frontend []ChatMessageInput) []openai.ChatCompletionMessage {
	backendKeys := make([]string, 0, len(backend))
	for _, m := range backend {
		if key := comparableMessageKey(m.Role, m.Content); key != "" {
			backendKeys = append(backendKeys, key)
		}
	}

	type frontendMessage struct {
		key string
		msg ChatMessageInput
	}
	front := make([]frontendMessage, 0, len(frontend))
	for _, m := range frontend {
		role := strings.TrimSpace(m.Role)
		if role != openai.ChatMessageRoleUser && role != openai.ChatMessageRoleAssistant {
			continue
		}
		if strings.TrimSpace(m.Content) == "" && len(m.Attachments) == 0 {
			continue
		}
		front = append(front, frontendMessage{key: comparableMessageKey(role, m.Content), msg: m})
	}

	// Match the longest suffix of backend-visible history against any
	// contiguous frontend range. This handles restart recovery where IndexedDB
	// contains an older prefix but the backend retained only its budgeted tail.
	lastMatchedFrontend := -1
	maxOverlap := len(backendKeys)
	if len(front) < maxOverlap {
		maxOverlap = len(front)
	}
	for overlap := maxOverlap; overlap > 0 && lastMatchedFrontend < 0; overlap-- {
		backendStart := len(backendKeys) - overlap
		for frontStart := len(front) - overlap; frontStart >= 0; frontStart-- {
			matched := true
			for offset := 0; offset < overlap; offset++ {
				if front[frontStart+offset].key == "" || front[frontStart+offset].key != backendKeys[backendStart+offset] {
					matched = false
					break
				}
			}
			if matched {
				lastMatchedFrontend = frontStart + overlap - 1
				break
			}
		}
	}

	appendFrom := lastMatchedFrontend + 1
	if lastMatchedFrontend < 0 && len(backendKeys) > 0 && len(front) > 0 {
		// A backend compaction summary intentionally has no textual overlap with
		// the still-expanded UI snapshot. In that case the backend remains the
		// source of truth and only the request-tail message is new.
		appendFrom = len(front) - 1
	}
	for _, item := range front[appendFrom:] {
		role := strings.TrimSpace(item.msg.Role)
		if role == openai.ChatMessageRoleUser && len(item.msg.Attachments) > 0 {
			messages = appendUserMessageWithAttachments(messages, item.msg.Content, item.msg.Attachments)
		} else {
			messages = append(messages, openai.ChatCompletionMessage{Role: role, Content: item.msg.Content})
		}
	}
	return messages
}

func comparableMessageKey(role, content string) string {
	role = strings.TrimSpace(role)
	content = strings.TrimSpace(content)
	if content == "" {
		return ""
	}
	if role != openai.ChatMessageRoleUser && role != openai.ChatMessageRoleAssistant {
		return ""
	}
	return role + "\x00" + content
}

func cloneChatMessages(messages []openai.ChatCompletionMessage) []openai.ChatCompletionMessage {
	if len(messages) == 0 {
		return nil
	}
	out := make([]openai.ChatCompletionMessage, len(messages))
	copy(out, messages)
	for i := range out {
		out[i].ToolCalls = append([]openai.ToolCall(nil), messages[i].ToolCalls...)
		out[i].MultiContent = append([]openai.ChatMessagePart(nil), messages[i].MultiContent...)
	}
	return out
}

func (a *App) appendWorkspaceMapMessage(messages []openai.ChatCompletionMessage, cfg ConfigState) []openai.ChatCompletionMessage {
	if content := a.workspaceMapContext(cfg); content != "" {
		messages = append(messages, openai.ChatCompletionMessage{Role: openai.ChatMessageRoleSystem, Content: content})
	}
	return messages
}

func appendUserMessageWithAttachments(messages []openai.ChatCompletionMessage, text string, attachments []AttachmentInput) []openai.ChatCompletionMessage {
	content := buildAttachmentTextContext(text, attachments)
	parts := []openai.ChatMessagePart{}
	if strings.TrimSpace(content) != "" {
		parts = append(parts, openai.ChatMessagePart{Type: openai.ChatMessagePartTypeText, Text: content})
	}
	for _, att := range attachments {
		if !isImageAttachment(att) || !validImageDataURL(att.DataURL) {
			continue
		}
		if len(att.DataURL) > maxAttachmentDataURL {
			continue
		}
		parts = append(parts, openai.ChatMessagePart{
			Type: openai.ChatMessagePartTypeImageURL,
			ImageURL: &openai.ChatMessageImageURL{
				URL:    att.DataURL,
				Detail: openai.ImageURLDetailAuto,
			},
		})
	}
	if len(parts) > 1 {
		return append(messages, openai.ChatCompletionMessage{Role: openai.ChatMessageRoleUser, MultiContent: parts})
	}
	return append(messages, openai.ChatCompletionMessage{Role: openai.ChatMessageRoleUser, Content: content})
}

func buildAttachmentTextContext(text string, attachments []AttachmentInput) string {
	base := strings.TrimSpace(text)
	if len(attachments) == 0 {
		return base
	}
	var b strings.Builder
	if base != "" {
		b.WriteString(base)
		b.WriteString("\n\n")
	}
	b.WriteString("Attached files:\n")
	for i, att := range attachments {
		name := strings.TrimSpace(att.Name)
		if name == "" {
			name = "unnamed"
		}
		kind := strings.TrimSpace(att.Kind)
		if kind == "" {
			kind = "file"
		}
		mimeType := strings.TrimSpace(att.Type)
		if mimeType == "" {
			mimeType = kind
		}
		state := "metadata only"
		if isImageAttachment(att) && validImageDataURL(att.DataURL) && len(att.DataURL) <= maxAttachmentDataURL {
			state = "sent as image input"
		} else if strings.TrimSpace(att.Text) != "" {
			state = "sent as text"
		}
		if att.Truncated {
			state += ", truncated"
		}
		fmt.Fprintf(&b, "%d. %s (%s, %d bytes): %s", i+1, name, mimeType, att.Size, state)
		if att.Error != "" {
			fmt.Fprintf(&b, " (%s)", att.Error)
		}
		b.WriteString("\n")
	}
	for _, att := range attachments {
		if strings.TrimSpace(att.Text) == "" {
			continue
		}
		text := att.Text
		truncated := att.Truncated
		if len(text) > maxAttachmentText {
			text = text[:maxAttachmentText]
			truncated = true
		}
		b.WriteString("\n<attached_file name=\"")
		b.WriteString(escapeAttribute(att.Name))
		b.WriteString("\" mime=\"")
		b.WriteString(escapeAttribute(att.Type))
		b.WriteString("\">\n")
		b.WriteString(text)
		if truncated {
			b.WriteString("\n[attachment text truncated]\n")
		}
		b.WriteString("\n</attached_file>\n")
	}
	return b.String()
}

func isImageAttachment(att AttachmentInput) bool {
	kind := strings.ToLower(strings.TrimSpace(att.Kind))
	mimeType := strings.ToLower(strings.TrimSpace(att.Type))
	return kind == "image" || strings.HasPrefix(mimeType, "image/")
}

func validImageDataURL(value string) bool {
	lower := strings.ToLower(strings.TrimSpace(value))
	return strings.HasPrefix(lower, "data:image/png;base64,") ||
		strings.HasPrefix(lower, "data:image/jpeg;base64,") ||
		strings.HasPrefix(lower, "data:image/jpg;base64,") ||
		strings.HasPrefix(lower, "data:image/webp;base64,") ||
		strings.HasPrefix(lower, "data:image/gif;base64,")
}

func escapeAttribute(value string) string {
	replacer := strings.NewReplacer("&", "&amp;", "\"", "&quot;", "<", "&lt;", ">", "&gt;")
	return replacer.Replace(value)
}

func (a *App) historyDiskPaths(sessionID string) []string {
	safeName := url.PathEscape(sessionID)
	return []string{
		filepath.Join(a.historiesDir, safeName+".json.gz"),
		filepath.Join(a.historiesDir, safeName+".json"),
	}
}

func (a *App) saveHistory(sessionID string, messages []openai.ChatCompletionMessage) {
	if sessionID == "" {
		return
	}
	filtered := trimSavedHistory(sanitizeHistoryMessages(messages))
	breakdown := computeLiveBreakdown(filtered)
	a.mu.Lock()
	a.histories[sessionID] = cloneChatMessages(filtered)
	a.liveBreakdown[sessionID] = breakdown
	a.mu.Unlock()

	if a.historiesDir == "" {
		return
	}
	paths := a.historyDiskPaths(sessionID)
	if err := writeCompressedHistory(paths[0], filtered); err != nil {
		log.Printf("saveHistory: failed to write %s: %v", paths[0], err)
		return
	}
	if err := os.Remove(paths[1]); err != nil && !errors.Is(err, os.ErrNotExist) {
		log.Printf("saveHistory: failed to remove legacy %s: %v", paths[1], err)
	}
}

func (a *App) restoreSavedHistoryBreakdown(sessionID string) {
	if sessionID == "" {
		return
	}
	a.mu.Lock()
	history := cloneChatMessages(a.histories[sessionID])
	if history == nil {
		history = cloneChatMessages(a.loadHistoryLocked(sessionID))
	}
	if len(history) == 0 {
		delete(a.liveBreakdown, sessionID)
	} else {
		a.liveBreakdown[sessionID] = computeLiveBreakdown(history)
	}
	a.mu.Unlock()
}

func writeCompressedHistory(diskPath string, messages []openai.ChatCompletionMessage) error {
	tmp, err := os.CreateTemp(filepath.Dir(diskPath), ".history-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	zw := gzip.NewWriter(tmp)
	encodeErr := json.NewEncoder(zw).Encode(messages)
	closeGzipErr := zw.Close()
	closeFileErr := tmp.Close()
	if encodeErr != nil {
		return encodeErr
	}
	if closeGzipErr != nil {
		return closeGzipErr
	}
	if closeFileErr != nil {
		return closeFileErr
	}
	if err := os.Rename(tmpPath, diskPath); err != nil {
		// Windows may reject replacing an existing destination. Move the old
		// valid file aside, install the completed temp, and roll back on failure.
		backupPath := diskPath + ".bak"
		_ = os.Remove(backupPath)
		if backupErr := os.Rename(diskPath, backupPath); backupErr != nil {
			return err
		}
		if retryErr := os.Rename(tmpPath, diskPath); retryErr != nil {
			_ = os.Rename(backupPath, diskPath)
			return retryErr
		}
		_ = os.Remove(backupPath)
	}
	committed = true
	return nil
}

func (a *App) loadHistoryLocked(sessionID string) []openai.ChatCompletionMessage {
	if a.historiesDir == "" {
		return nil
	}
	paths := a.historyDiskPaths(sessionID)
	var messages []openai.ChatCompletionMessage
	loaded := false
	for index, diskPath := range paths {
		file, err := os.Open(diskPath)
		if err != nil {
			continue
		}
		var source io.Reader = file
		var zr *gzip.Reader
		if index == 0 {
			zr, err = gzip.NewReader(file)
			if err != nil {
				_ = file.Close()
				continue
			}
			source = zr
		}
		data, readErr := io.ReadAll(io.LimitReader(source, maxSavedHistoryJSONBytes+1))
		if zr != nil {
			_ = zr.Close()
		}
		_ = file.Close()
		if readErr != nil || len(data) > maxSavedHistoryJSONBytes || json.Unmarshal(data, &messages) != nil {
			continue
		}
		loaded = true
		break
	}
	if !loaded {
		return nil
	}
	messages = trimSavedHistory(sanitizeHistoryMessages(messages))
	a.histories[sessionID] = cloneChatMessages(messages)
	return messages
}

func historyMessageTokens(message openai.ChatCompletionMessage) int {
	tokens := estimateMessageBodyTokens(message)
	for _, call := range message.ToolCalls {
		tokens += estimateTokensFromText(call.Function.Name)
		tokens += estimateTokensFromText(call.Function.Arguments)
	}
	return tokens
}

func trimSavedHistory(messages []openai.ChatCompletionMessage) []openai.ChatCompletionMessage {
	if len(messages) == 0 {
		return nil
	}
	total := 0
	for _, message := range messages {
		total += historyMessageTokens(message)
	}
	if total <= maxSavedHistoryTokens {
		return messages
	}

	// Start only at a user message so an assistant tool call and all of its
	// tool results remain an intact model-protocol sequence. If the newest turn
	// alone exceeds the budget, keep it whole rather than creating orphans.
	running := 0
	start := len(messages)
	lastUser := -1
	for index := len(messages) - 1; index >= 0; index-- {
		running += historyMessageTokens(messages[index])
		if messages[index].Role != openai.ChatMessageRoleUser {
			continue
		}
		lastUser = index
		if running <= maxSavedHistoryTokens {
			start = index
			continue
		}
		break
	}
	if start == len(messages) {
		if lastUser >= 0 {
			start = lastUser
		} else {
			return messages
		}
	}
	return messages[start:]
}

func sanitizeHistoryMessages(messages []openai.ChatCompletionMessage) []openai.ChatCompletionMessage {
	filtered := make([]openai.ChatCompletionMessage, 0, len(messages))
	for _, original := range messages {
		if original.Role == openai.ChatMessageRoleSystem || isGrillControlMessage(original) || isGoalProgressMessage(original) {
			continue
		}
		m := original
		if len(m.MultiContent) > 0 {
			m.Content = textFromMultiContent(m.MultiContent)
			m.MultiContent = nil
		}
		if strings.TrimSpace(m.Content) == "" && len(m.ToolCalls) == 0 && m.Role != openai.ChatMessageRoleTool {
			continue
		}
		m.ToolCalls = append([]openai.ToolCall(nil), m.ToolCalls...)
		filtered = append(filtered, m)
	}
	return filtered
}

func textFromMultiContent(parts []openai.ChatMessagePart) string {
	var b strings.Builder
	imageCount := 0
	for _, part := range parts {
		switch part.Type {
		case openai.ChatMessagePartTypeText:
			if strings.TrimSpace(part.Text) != "" {
				if b.Len() > 0 {
					b.WriteString("\n")
				}
				b.WriteString(part.Text)
			}
		case openai.ChatMessagePartTypeImageURL:
			imageCount++
		}
	}
	if imageCount > 0 {
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		fmt.Fprintf(&b, "[%d image attachment(s) omitted from saved history]", imageCount)
	}
	return b.String()
}

type savedToolActivity struct {
	CallID  string
	Name    string
	Args    string
	Status  string
	Summary string
}

func formatSavedToolActivity(tools []savedToolActivity) string {
	if len(tools) == 0 {
		return ""
	}
	const maxSavedToolLines = 20
	var b strings.Builder
	b.WriteString("Tool activity from previous turn:\n")
	limit := len(tools)
	if limit > maxSavedToolLines {
		limit = maxSavedToolLines
	}
	for i := 0; i < limit; i++ {
		tool := tools[i]
		status := strings.TrimSpace(tool.Status)
		if status == "" {
			status = "called"
		}
		b.WriteString("- ")
		b.WriteString(tool.Name)
		if args := compactSavedToolArgs(tool.Args); args != "" {
			b.WriteString("(")
			b.WriteString(args)
			b.WriteString(")")
		}
		b.WriteString(": ")
		b.WriteString(status)
		if summary := strings.TrimSpace(tool.Summary); summary != "" {
			b.WriteString(" - ")
			b.WriteString(summary)
		}
		b.WriteString("\n")
	}
	if len(tools) > limit {
		b.WriteString(fmt.Sprintf("- ... %d more tool calls omitted\n", len(tools)-limit))
	}
	return truncateRunes(strings.TrimRight(b.String(), "\n"), 4000)
}

func compactSavedToolArgs(args string) string {
	args = strings.TrimSpace(args)
	if args == "" {
		return ""
	}
	var decoded any
	if json.Unmarshal([]byte(args), &decoded) == nil {
		if raw, err := json.Marshal(decoded); err == nil {
			args = string(raw)
		}
	}
	return truncateRunes(normalizeWhitespace(args), 240)
}

func summarizeSavedToolResult(name, content string) (string, string) {
	var result toolResult
	if err := json.Unmarshal([]byte(content), &result); err == nil {
		if result.OK {
			summary := toolResultSummary(name, &result)
			if summary == "" {
				summary = "ok"
			}
			return "success", summary
		}
		errText := strings.TrimSpace(result.Error)
		if errText == "" {
			errText = "error"
		}
		return "failed", truncateRunes(normalizeWhitespace(errText), 240)
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return "completed", ""
	}
	return "completed", truncateRunes(normalizeWhitespace(content), 240)
}

func mergeToolCallDeltas(toolCalls *[]openai.ToolCall, deltas []openai.ToolCall) {
	for _, delta := range deltas {
		idx := len(*toolCalls)
		if delta.Index != nil {
			idx = *delta.Index
		}
		for len(*toolCalls) <= idx {
			*toolCalls = append(*toolCalls, openai.ToolCall{Type: openai.ToolTypeFunction})
		}
		current := &(*toolCalls)[idx]
		if delta.ID != "" {
			current.ID += delta.ID
		}
		if delta.Type != "" {
			current.Type = delta.Type
		}
		if delta.Function.Name != "" {
			current.Function.Name += delta.Function.Name
		}
		if delta.Function.Arguments != "" {
			current.Function.Arguments += delta.Function.Arguments
		}
	}
}

// buildToolsWithMcp combines static tools with dynamically discovered MCP tools.
func (a *App) buildToolsWithMcp() []openai.Tool {
	tools := chatTools()
	if a.mcpManager == nil {
		return tools
	}
	mcpTools := a.mcpManager.GetAllTools()
	for _, dt := range mcpTools {
		name := dt.FunctionName
		if name == "" {
			name = mcpToolFunctionName(dt.ServerName, dt.Name)
		}
		desc := strings.TrimSpace(dt.Description)
		if desc == "" {
			desc = fmt.Sprintf("MCP tool %s from %s", dt.Name, dt.ServerName)
		} else {
			desc = fmt.Sprintf("[%s] %s", dt.ServerName, desc)
		}
		params := dt.Schema
		if params == nil {
			params = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		tools = append(tools, rawFunctionTool(name, desc, params))
	}
	return tools
}

func (a *App) buildToolsForConfig(cfg ConfigState) []openai.Tool {
	tools := a.buildToolsWithMcp()
	if !cfg.grillMode {
		return tools
	}
	filtered := make([]openai.Tool, 0, len(tools))
	for _, tool := range tools {
		if tool.Function != nil {
			if cfg.grillMode && toolDisabledInGrillMode(tool.Function.Name) {
				continue
			}
		}
		filtered = append(filtered, tool)
	}
	return filtered
}

func toolDisabledInGrillMode(name string) bool {
	switch name {
	case "edit", "create_file", "delete_path", "run_command", "background_process", "wait", "http_request", "web_fetch",
		"remote_edit", "remote_create_file", "remote_delete_path", "remote_run_command",
		"subagent", "agent_delegate", "memory_write", "todo_write", "create_goal", "update_goal", "scheduled_task":
		return true
	default:
		return strings.HasPrefix(name, "mcp__")
	}
}

func (a *App) GetMcpServers() []map[string]any {
	if a.mcpManager == nil {
		return nil
	}
	return a.mcpManager.GetServerStatuses()
}

func (a *App) ListTools() []ToolDefinitionSummary {
	tools := make([]ToolDefinitionSummary, 0, len(chatTools()))
	for _, tool := range chatTools() {
		if tool.Function == nil {
			continue
		}
		tools = append(tools, ToolDefinitionSummary{
			Name:        tool.Function.Name,
			Description: strings.TrimSpace(tool.Function.Description),
			Source:      "built-in",
		})
	}
	if a.mcpManager != nil {
		mcpTools := a.mcpManager.GetAllTools()
		sort.Slice(mcpTools, func(i, j int) bool {
			if mcpTools[i].ServerName == mcpTools[j].ServerName {
				return mcpTools[i].Name < mcpTools[j].Name
			}
			return mcpTools[i].ServerName < mcpTools[j].ServerName
		})
		for _, tool := range mcpTools {
			name := tool.FunctionName
			if name == "" {
				name = mcpToolFunctionName(tool.ServerName, tool.Name)
			}
			description := strings.TrimSpace(tool.Description)
			if description == "" {
				description = fmt.Sprintf("MCP tool %s from %s", tool.Name, tool.ServerName)
			}
			tools = append(tools, ToolDefinitionSummary{
				Name:        name,
				Description: description,
				Source:      "mcp",
				Server:      tool.ServerName,
			})
		}
	}
	return tools
}

func (a *App) emitMcpStatus() {
	if a.ctx == nil || a.mcpManager == nil {
		return
	}
	a.emit("mcp:status", map[string]any{"servers": a.mcpManager.GetServerStatuses()})
}

func (a *App) GetMcpConfig() (string, error) {
	path := mcpUserConfigPath()
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "{\n  \"mcpServers\": {}\n}", nil
	}
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func (a *App) SaveMcpConfig(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		raw = "{\"mcpServers\":{}}"
	}
	var cfg McpServersConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return fmt.Errorf("invalid MCP JSON: %w", err)
	}
	if cfg.McpServers == nil {
		cfg.McpServers = map[string]McpServerConfig{}
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	path := mcpUserConfigPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

func (a *App) RestartMcpServers() error {
	cfg, err := a.getConfig()
	if err != nil {
		return err
	}
	root, err := workspaceRoot(cfg)
	if err != nil {
		return err
	}
	if a.ctx == nil {
		return errors.New("application context is not ready")
	}
	if a.mcpManager != nil {
		a.mcpManager.Shutdown()
	}
	manager := NewMcpManager(root, func(tools []McpDiscoveredTool) {
		a.emitMcpStatus()
	})
	manager.SetNetworkConfigProvider(func() ConfigState { return a.effectiveConfig(ConfigState{}) })
	a.mcpManager = manager
	err = manager.StartAll(a.ctx)
	a.emitMcpStatus()
	return err
}

// Static tool schemas live in internal/tools/shared. These wrappers keep the
// orchestration and existing package-level tests independent from that layout.
func chatTools() []openai.Tool { return toolshared.Builtins() }
func rawFunctionTool(name, description string, parameters map[string]any) openai.Tool {
	return toolshared.RawFunction(name, description, parameters)
}
func normalizeToolName(name string) string { return toolshared.NormalizeName(name) }

func (a *App) executeTool(ctx context.Context, cfg ConfigState, sessionID, name string, args []byte) toolResult {
	decode := func(v any) error {
		if len(bytes.TrimSpace(args)) == 0 {
			return nil
		}
		dec := json.NewDecoder(bytes.NewReader(args))
		dec.DisallowUnknownFields()
		if err := dec.Decode(v); err != nil {
			return err
		}
		var extra any
		if err := dec.Decode(&extra); err != io.EOF {
			return errors.New("invalid JSON: trailing data")
		}
		return nil
	}

	// Normalize once at the boundary: lower-case for case-insensitivity and
	// resolve deprecated aliases so historical sessions keep working after a
	// rename. MCP tools (mcp__*) pass through unchanged because their
	// sanitized names are already lowercase.
	name = normalizeToolName(name)

	var data any
	var err error

	if cfg.grillMode {
		if toolDisabledInGrillMode(name) {
			return toolResult{OK: false, Error: fmt.Sprintf("tool '%s' is disabled in grill mode (read-only interview)", name)}
		}
	}

	switch name {
	case "list_files":
		var req ListFilesRequest
		err = decode(&req)
		if err == nil {
			data, err = a.listFilesWithConfig(cfg, req)
		}
	case "read_file":
		var req ReadFileRequest
		err = decode(&req)
		if err == nil {
			data, err = a.readFileWithConfig(cfg, req)
		}
	case "edit":
		var req ModelEditToolRequest
		err = decode(&req)
		if err == nil {
			err = validateModelEditToolRequest(req.Files)
		}
		if err == nil {
			a.fileOpsMu.Lock()
			data, err = a.editFilesWithConfig(cfg, req.Files)
			a.fileOpsMu.Unlock()
			if err == nil {
				a.invalidateWorkspaceMapCache(cfg)
			}
		}
	case "create_file":
		var req CreateFileRequest
		err = decode(&req)
		if err == nil {
			a.fileOpsMu.Lock()
			data, err = a.createFileWithConfig(cfg, req)
			a.fileOpsMu.Unlock()
			if err == nil {
				a.invalidateWorkspaceMapCache(cfg)
			}
		}
	case "delete_path":
		var req DeletePathRequest
		err = decode(&req)
		if err == nil {
			a.fileOpsMu.Lock()
			data, err = a.deletePathWithConfig(cfg, req)
			a.fileOpsMu.Unlock()
			if err == nil {
				a.invalidateWorkspaceMapCache(cfg)
			}
		}
	case "run_command":
		var req CommandRequest
		err = decode(&req)
		if err == nil {
			data, err = a.runCommandWithConfig(ctx, cfg, req)
			if err == nil {
				a.invalidateWorkspaceMapCache(cfg)
			}
		}
	case "background_process":
		var req BackgroundProcessRequest
		err = decode(&req)
		if err == nil {
			switch strings.ToLower(strings.TrimSpace(req.Action)) {
			case "start":
				data, err = a.startServiceWithConfig(cfg, StartServiceRequest{
					Name:    req.Name,
					Command: req.Command,
					Cwd:     req.Cwd,
				})
			case "stop":
				data, err = a.stopService(StopServiceRequest{ID: req.ID})
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
		err = decode(&req)
		if err == nil {
			data, err = waitWithContext(ctx, req)
		}
	case "ask":
		var req AskRequest
		err = decode(&req)
		if err == nil {
			if cfg.grillMode && len(req.Questions) != 1 {
				err = codedToolError("E_GRILL_ASK_COUNT", errors.New("grill mode requires exactly one question per ask call"))
			} else {
				data, err = a.executeAsk(ctx, sessionID, req)
			}
		}
	case "scheduled_task":
		var req ScheduledTaskToolRequest
		err = decode(&req)
		if err == nil {
			data, err = a.executeScheduledTaskTool(cfg, req)
		}
	case "http_request":
		var req HTTPRequestToolRequest
		err = decode(&req)
		if err == nil {
			data, err = a.httpRequestToolWithConfig(ctx, cfg, req)
		}
	case "web_fetch":
		var req WebFetchRequest
		err = decode(&req)
		if err == nil {
			data, err = a.webFetchToolWithConfig(ctx, cfg, req)
		}
	case "remote_list_files":
		var req RemoteListFilesRequest
		err = decode(&req)
		if err == nil {
			data, err = a.remoteListFiles(ctx, req)
		}
	case "remote_read_file":
		var req RemoteReadFileRequest
		err = decode(&req)
		if err == nil {
			data, err = a.remoteReadFile(ctx, req)
		}
	case "remote_edit":
		var req RemoteEditRequest
		err = decode(&req)
		if err == nil {
			data, err = a.remoteEdit(ctx, req)
		}
	case "remote_create_file":
		var req RemoteCreateFileRequest
		err = decode(&req)
		if err == nil {
			data, err = a.remoteCreateFile(ctx, req)
		}
	case "remote_delete_path":
		var req RemoteDeletePathRequest
		err = decode(&req)
		if err == nil {
			data, err = a.remoteDeletePath(ctx, req)
		}
	case "remote_run_command":
		var req RemoteRunCommandRequest
		err = decode(&req)
		if err == nil {
			data, err = a.remoteRunCommand(ctx, req)
		}
	case "grep_files":
		var reqGF GrepRequest
		err = decode(&reqGF)
		if err == nil {
			data, err = a.grepFilesWithConfig(ctx, cfg, reqGF)
		}
	case "read":
		var reqBR BatchReadRequest
		err = decode(&reqBR)
		if err == nil {
			data, err = a.batchReadFilesWithConfig(cfg, reqBR)
		}
	case "memory_read":
		var req MemoryReadRequest
		err = decode(&req)
		if err == nil {
			data, err = a.memoryRead(req)
		}
	case "memory_write":
		var req MemoryWriteRequest
		err = decode(&req)
		if err == nil {
			a.fileOpsMu.Lock()
			data, err = a.memoryWrite(req)
			a.fileOpsMu.Unlock()
		}
	case "document_read":
		var reqDoc DocumentReadRequest
		err = decode(&reqDoc)
		if err == nil {
			data, err = a.readDocumentWithConfig(cfg, reqDoc)
		}
	case "calculate":
		var reqCalc CalculateRequest
		err = decode(&reqCalc)
		if err == nil {
			data, err = calculateExpression(reqCalc)
		}
	case "render_html":
		var req struct {
			HTML  string `json:"html"`
			Title string `json:"title"`
		}
		err = decode(&req)
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
		err = decode(&adReq)
		if err == nil {
			err = a.acquireSubagentSlot(ctx)
			if err == nil {
				defer a.releaseSubagentSlot()
				subCtx, cancel := context.WithCancel(ctx)
				defer cancel()
				res, delegateErr := a.executeDelegate(subCtx, cfg, sessionID, adReq, cancel)
				if delegateErr != nil {
					err = delegateErr
				} else {
					data = res
				}
			}
		}
	case "todo_write":
		var tReq TodoListRequest
		err = decode(&tReq)
		if err == nil {
			data, err = a.handleTodoList(sessionID, tReq)
		}
	case "create_goal":
		var cgReq struct {
			Objective           string `json:"objective"`
			CompletionCriterion string `json:"completionCriterion"`
			MaxTurns            int    `json:"maxTurns"`
		}
		err = decode(&cgReq)
		if err == nil {
			data, err = a.createGoal(sessionID, cgReq.Objective, cgReq.CompletionCriterion, cgReq.MaxTurns)
		}
	case "update_goal":
		var ugReq struct {
			Status string `json:"status"`
			Reason string `json:"reason,omitempty"`
		}
		err = decode(&ugReq)
		if err == nil {
			data, err = a.updateGoal(sessionID, ugReq.Status, ugReq.Reason)
		}
	case "get_goal":
		var empty struct{}
		err = decode(&empty)
		if err == nil {
			data = a.getGoalResult(sessionID)
		}
	case "skill":
		var skReq struct {
			Skill string `json:"skill"`
			Args  string `json:"args,omitempty"`
		}
		err = decode(&skReq)
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
	return toolResult{OK: true, Data: data}
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

func (a *App) executeMcpTool(ctx context.Context, serverName, toolName string, args map[string]any) (any, error) {
	if a.mcpManager == nil {
		return nil, fmt.Errorf("MCP not initialized")
	}
	result, err := a.mcpManager.CallTool(ctx, serverName, toolName, args)
	if err != nil {
		return nil, fmt.Errorf("MCP tool %s/%s failed: %w", serverName, toolName, err)
	}
	return map[string]any{"output": result}, nil
}

func (a *App) executeMcpFunctionTool(ctx context.Context, functionName string, args map[string]any) (any, error) {
	if a.mcpManager == nil {
		return nil, fmt.Errorf("MCP not initialized")
	}
	result, err := a.mcpManager.CallToolByFunctionName(ctx, functionName, args)
	if err != nil {
		return nil, fmt.Errorf("MCP tool %s failed: %w", functionName, err)
	}
	return map[string]any{"output": result}, nil
}

func (a *App) mcpToolEventMeta(functionName string) map[string]any {
	if a.mcpManager == nil || !strings.HasPrefix(functionName, "mcp__") {
		return nil
	}
	ref, ok := a.mcpManager.DescribeFunctionTool(functionName)
	if !ok {
		return nil
	}
	return map[string]any{
		"mcpServer": ref.ServerName,
		"mcpTool":   ref.ToolName,
	}
}

func mergeToolEventMeta(event map[string]any, meta map[string]any) map[string]any {
	if len(meta) == 0 {
		return event
	}
	for key, value := range meta {
		event[key] = value
	}
	return event
}

func (a *App) executeDelegate(ctx context.Context, cfg ConfigState, sessionID string, req AgentDelegateRequest, cancel context.CancelFunc) (*AgentDelegateResult, error) {
	if strings.TrimSpace(req.Task) == "" {
		return nil, errors.New("task is required")
	}
	model := cfg.Model
	if req.Model != "" {
		model = req.Model
	}
	subID := newID()
	desc := req.Description
	if desc == "" {
		desc = req.Task
		if len(desc) > 40 {
			desc = desc[:37] + "..."
		}
	}

	run := &SubagentRun{
		ID:          subID,
		SessionID:   sessionID,
		Description: desc,
		Profile:     "coder",
		Status:      "running",
		StartTime:   time.Now().UnixMilli(),
		cancel:      cancel,
	}
	a.subRunsMu.Lock()
	a.subRuns[subID] = run
	a.subRunsMu.Unlock()
	defer a.finishSubagentRecord(subID)
	spawnPayload := map[string]any{"id": subID, "sessionId": sessionID, "description": desc, "profile": "coder", "startTime": run.StartTime}
	if meta, ok := ctx.Value(toolExecutionMetaContextKey{}).(toolExecutionMeta); ok {
		spawnPayload["runId"] = meta.runID
		spawnPayload["toolBatchId"] = meta.toolBatchID
		spawnPayload["toolCallIndex"] = meta.toolCallIndex
		spawnPayload["toolCallId"] = meta.toolCallID
	}
	a.emit("sub:spawn", spawnPayload)

	// Build messages for the sub-agent
	messages := []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleSystem, Content: subagentSystemPrompt()},
	}
	if !req.CleanContext {
		env := a.buildSubagentEnv(cfg)
		messages = append(messages, openai.ChatCompletionMessage{Role: openai.ChatMessageRoleUser, Content: env})
		if instructionContext := a.buildSubagentInstructionContext(cfg); instructionContext != "" {
			messages = append(messages, openai.ChatCompletionMessage{Role: openai.ChatMessageRoleUser, Content: instructionContext})
		}
	}
	messages = append(messages, openai.ChatCompletionMessage{Role: openai.ChatMessageRoleUser, Content: "## Task\n" + req.Task})

	// Run sub-agent loop synchronously (the caller is already in a goroutine for parallel)
	tools := req.tools
	if len(tools) == 0 {
		tools = a.subagentTools(cfg)
	}

	var filesRead []string
	var filesEdited []string
	seenFiles := map[string]bool{}
	step := 0
	for req.maxSteps <= 0 || step < req.maxSteps {
		select {
		case <-ctx.Done():
			a.subRunsMu.Lock()
			run.Status = "failed"
			run.Error = "cancelled"
			run.Steps = step
			a.subRunsMu.Unlock()
			a.emit("sub:error", map[string]any{"id": subID, "sessionId": sessionID, "error": "cancelled", "durationMs": time.Now().UnixMilli() - run.StartTime})
			return &AgentDelegateResult{AgentID: subID, Description: desc, Status: "failed", Steps: step, Error: "cancelled"}, ctx.Err()
		default:
		}

		// Retry transient LLM errors (429/5xx/network) at the sub-agent loop
		// level. Streaming-adapter retries only cover connection setup and
		// (for Anthropic) pre-first-event errors; this outer wrapper also
		// retries mid-stream and stop errors so a flaky provider doesn't
		// abort the whole sub-agent task.
		// 多 key 时内层 streamModelResponse 已用 maxMultiKeyAttempts 预算统一
		// 承担重试与故障切换,这里归零避免两层预算重叠(外层再套一层只会
		// 放大尝试次数;冷却会使后续轮次立即 break)。
		maxRetries := effectiveLLMRetries(cfg)
		if len(resolveKeyPool(cfg)) > 1 {
			maxRetries = 0
		}
		var modelResp *modelStreamResult
		var err error
		for attempt := 0; ; attempt++ {
			modelResp, err = a.streamModelResponse(ctx, cfg, model, messages, tools, nil)
			if err == nil {
				err = modelResponseStopError(cfg, modelResp)
			}
			if err == nil {
				break
			}
			if attempt < maxRetries && shouldRetryLLMError(err) {
				wait := llmRetryDelay(attempt + 1)
				select {
				case <-time.After(wait):
				case <-ctx.Done():
					a.subRunsMu.Lock()
					run.Status = "failed"
					run.Error = "cancelled"
					run.Steps = step
					a.subRunsMu.Unlock()
					a.emit("sub:error", map[string]any{"id": subID, "sessionId": sessionID, "error": "cancelled", "durationMs": time.Now().UnixMilli() - run.StartTime})
					return &AgentDelegateResult{AgentID: subID, Description: desc, Status: "failed", Steps: step, Error: "cancelled", Model: model}, ctx.Err()
				}
				continue
			}
			a.subRunsMu.Lock()
			run.Status = "failed"
			run.Error = err.Error()
			run.Steps = step
			a.subRunsMu.Unlock()
			a.emit("sub:error", map[string]any{"id": subID, "sessionId": sessionID, "error": err.Error(), "durationMs": time.Now().UnixMilli() - run.StartTime})
			return &AgentDelegateResult{AgentID: subID, Description: desc, Status: "failed", Steps: step, Error: err.Error(), Model: model}, err
		}

		// Accumulate token usage from the sub-agent's model response
		if modelResp.Usage != nil {
			a.subRunsMu.Lock()
			run.InputTokens += modelResp.Usage.PromptTokens
			run.OutputTokens += modelResp.Usage.CompletionTokens
			run.TotalTokens = run.InputTokens + run.OutputTokens
			a.subRunsMu.Unlock()
		}
		fallbackInput := 0
		fallbackOutput := 0
		if modelResp.Usage == nil || modelResp.Usage.PromptTokens <= 0 {
			fallbackInput = estimateRequestTokens(messages, tools)
		}
		if modelResp.Usage == nil || modelResp.Usage.CompletionTokens <= 0 {
			fallbackOutput = estimateCompletionTokens(modelResp.Content, modelResp.Reasoning, modelResp.ToolCalls)
		}
		a.recordTokenStats(
			cfg.ProviderName,
			model,
			cfg.Workspace,
			sessionID,
			"subagent",
			modelResp.Usage,
			fallbackInput,
			fallbackOutput,
		)

		assistantMessage := openai.ChatCompletionMessage{
			Role:      openai.ChatMessageRoleAssistant,
			Content:   modelResp.Content,
			ToolCalls: modelResp.ToolCalls,
		}

		if len(assistantMessage.ToolCalls) == 0 {
			// Sub-agent finished — extract summary
			summary := strings.TrimSpace(assistantMessage.Content)
			step++
			a.subRunsMu.Lock()
			run.Status = "completed"
			run.Summary = summary
			run.Steps = step
			run.FilesRead = filesRead
			run.FilesEdited = filesEdited
			a.subRunsMu.Unlock()
			a.emit("sub:done", map[string]any{
				"id": subID, "sessionId": sessionID, "status": "completed", "steps": step,
				"summary": summary, "filesRead": filesRead, "filesEdited": filesEdited, "durationMs": time.Now().UnixMilli() - run.StartTime,
				"inputTokens": run.InputTokens, "outputTokens": run.OutputTokens, "totalTokens": run.TotalTokens,
			})
			return &AgentDelegateResult{
				AgentID: subID, Description: desc, Status: "completed",
				Steps: step, Summary: summary,
				FilesRead: filesRead, FilesEdited: filesEdited, Model: model,
			}, nil
		}

		// Execute tool calls — parallel for non-file tools, ordered for file mutations
		toolIDs := make([]string, len(assistantMessage.ToolCalls))
		for i := range assistantMessage.ToolCalls {
			if assistantMessage.ToolCalls[i].ID == "" {
				assistantMessage.ToolCalls[i].ID = fmt.Sprintf("subcall_%s_%d", subID, i)
			}
			toolIDs[i] = assistantMessage.ToolCalls[i].ID
		}
		messages = append(messages, assistantMessage)
		toolConflicts := detectToolBatchConflicts(cfg, assistantMessage.ToolCalls)

		// Register all tool calls as running and emit start events
		for i, call := range assistantMessage.ToolCalls {
			name := call.Function.Name
			args := call.Function.Arguments
			cid := toolIDs[i]
			a.subRunsMu.Lock()
			run.ToolCalls = append(run.ToolCalls, SubToolEvent{ToolCallID: cid, Name: name, Args: truncateRunes(args, 4096), Status: "running"})
			if len(run.ToolCalls) > maxSubagentToolCalls {
				run.ToolCalls = append([]SubToolEvent(nil), run.ToolCalls[len(run.ToolCalls)-maxSubagentToolCalls:]...)
			}
			a.subRunsMu.Unlock()
			a.emit("sub:tool:start", map[string]any{"id": subID, "sessionId": sessionID, "toolCallId": cid, "name": name, "args": args})
		}

		// Parallel execution: non-file tools run concurrently, file mutations run afterward in order
		type subToolOutcome struct {
			index     int
			callID    string
			name      string
			args      string
			result    toolResult
			modelJSON string
			duration  int64
		}
		totalCalls := len(assistantMessage.ToolCalls)
		subToolSem := make(chan struct{}, 4)
		subOutcomes := make([]subToolOutcome, totalCalls)

		// emitSubOutcome updates the sub-run tool-call status and emits the
		// result/error event for a single outcome. Called immediately after each
		// tool finishes (from within the worker goroutine for parallel calls, or
		// serially for ordered file mutations) so the UI reflects completion as
		// soon as each tool finishes, instead of waiting for the slowest tool in
		// the batch. run.ToolCalls is guarded by subRunsMu and a.emit is
		// concurrency-safe, so this is safe to call from multiple goroutines.
		emitSubOutcome := func(idx int) {
			o := subOutcomes[idx]
			if o.result.OK {
				summary := toolResultSummary(o.name, &o.result)
				a.subRunsMu.Lock()
				for ti := range run.ToolCalls {
					if run.ToolCalls[ti].ToolCallID == o.callID {
						run.ToolCalls[ti].Status = "success"
						run.ToolCalls[ti].Summary = truncateRunes(summary, 2048)
						run.ToolCalls[ti].DurationMS = o.duration
						break
					}
				}
				a.subRunsMu.Unlock()
				a.emit("sub:tool:result", map[string]any{"id": subID, "sessionId": sessionID, "toolCallId": o.callID, "name": o.name, "summary": summary, "durationMs": o.duration})
			} else {
				a.subRunsMu.Lock()
				for ti := range run.ToolCalls {
					if run.ToolCalls[ti].ToolCallID == o.callID {
						run.ToolCalls[ti].Status = "error"
						run.ToolCalls[ti].Summary = truncateRunes(o.result.Error, 2048)
						run.ToolCalls[ti].DurationMS = o.duration
						break
					}
				}
				a.subRunsMu.Unlock()
				a.emit("sub:tool:error", map[string]any{"id": subID, "sessionId": sessionID, "toolCallId": o.callID, "name": o.name, "error": o.result.Error, "errorCode": o.result.ErrorCode, "durationMs": o.duration})
			}
		}

		executeSubCall := func(idx int, c openai.ToolCall) {
			started := time.Now()
			r := a.executeTool(ctx, cfg, sessionID, c.Function.Name, []byte(c.Function.Arguments))
			duration := time.Since(started).Milliseconds()
			rj, _ := json.Marshal(r)
			fullJSON := string(rj)
			subOutcomes[idx] = subToolOutcome{
				index: idx, callID: toolIDs[idx], name: c.Function.Name,
				args: c.Function.Arguments, result: r,
				modelJSON: compactToolResultForModel(c.Function.Name, r, fullJSON), duration: duration,
			}
			emitSubOutcome(idx)
		}
		setSubConflictOutcome := func(idx int, c openai.ToolCall, conflictErr error) {
			r := toolErrorResult(conflictErr)
			rj, _ := json.Marshal(r)
			fullJSON := string(rj)
			subOutcomes[idx] = subToolOutcome{
				index: idx, callID: toolIDs[idx], name: c.Function.Name,
				args: c.Function.Arguments, result: r,
				modelJSON: fullJSON,
			}
			emitSubOutcome(idx)
		}

		var subWg sync.WaitGroup
		for i, call := range assistantMessage.ToolCalls {
			if conflictErr, conflict := toolConflicts[i]; conflict {
				setSubConflictOutcome(i, call, conflictErr)
				continue
			}
			if isOrderedFileMutationTool(call.Function.Name) {
				continue
			}
			subWg.Add(1)
			go func(idx int, c openai.ToolCall) {
				defer subWg.Done()
				subToolSem <- struct{}{}
				defer func() { <-subToolSem }()
				executeSubCall(idx, c)
			}(i, call)
		}
		subWg.Wait()
		for i, call := range assistantMessage.ToolCalls {
			if _, conflict := toolConflicts[i]; conflict || !isOrderedFileMutationTool(call.Function.Name) {
				continue
			}
			executeSubCall(i, call)
		}

		// Process outcomes in order. File tracking and the model-facing tool
		// messages must stay ordered (filesRead/filesEdited are order-sensitive
		// shared slices); the UI-facing status/emit already happened above in
		// emitSubOutcome as each tool completed.
		for _, o := range subOutcomes {
			trackFileFromToolResult(o.name, o.args, &o.result, &filesRead, &filesEdited, seenFiles)
			messages = append(messages, openai.ChatCompletionMessage{
				Role: openai.ChatMessageRoleTool, ToolCallID: o.callID, Content: o.modelJSON,
			})
		}
		step++
		a.subRunsMu.Lock()
		run.Steps = step
		a.subRunsMu.Unlock()
		a.emit("sub:step", map[string]any{"id": subID, "sessionId": sessionID, "step": step, "inputTokens": run.InputTokens, "outputTokens": run.OutputTokens, "totalTokens": run.TotalTokens})
	}

	a.subRunsMu.Lock()
	run.Status = "timed_out"
	run.Steps = step
	run.FilesRead = filesRead
	run.FilesEdited = filesEdited
	a.subRunsMu.Unlock()
	a.emit("sub:done", map[string]any{
		"id": subID, "sessionId": sessionID, "status": "timed_out", "steps": step,
		"filesRead": filesRead, "filesEdited": filesEdited, "durationMs": time.Now().UnixMilli() - run.StartTime,
		"inputTokens": run.InputTokens, "outputTokens": run.OutputTokens, "totalTokens": run.TotalTokens,
	})
	return &AgentDelegateResult{
		AgentID: subID, Description: desc, Status: "timed_out",
		Steps: step, FilesRead: filesRead, FilesEdited: filesEdited, Model: model,
		Error: fmt.Sprintf("reached scheduled-task step limit (%d)", req.maxSteps),
	}, nil
}

func (a *App) finishSubagentRecord(subID string) {
	a.subRunsMu.Lock()
	defer a.subRunsMu.Unlock()
	if run := a.subRuns[subID]; run != nil {
		run.cancel = nil
		run.Summary = truncateRunes(run.Summary, 32*1024)
		run.Error = truncateRunes(run.Error, 8*1024)
	}
	type finishedRun struct {
		id      string
		started int64
	}
	finished := make([]finishedRun, 0, len(a.subRuns))
	for id, run := range a.subRuns {
		if run != nil && run.Status != "running" {
			finished = append(finished, finishedRun{id: id, started: run.StartTime})
		}
	}
	if len(finished) <= maxFinishedSubagents {
		return
	}
	sort.Slice(finished, func(i, j int) bool { return finished[i].started < finished[j].started })
	for _, item := range finished[:len(finished)-maxFinishedSubagents] {
		delete(a.subRuns, item.id)
	}
}

func (a *App) acquireSubagentSlot(ctx context.Context) error {
	select {
	case a.subSem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (a *App) releaseSubagentSlot() {
	<-a.subSem
}

// subagentSystemPrompt returns the system prompt for sub-agents.
func subagentSystemPrompt() string {
	osName := goruntime.GOOS
	arch := goruntime.GOARCH
	platformNote := "Platform: "
	switch osName {
	case "windows":
		platformNote += "Windows"
	case "linux":
		platformNote += "Linux"
	case "darwin":
		platformNote += "macOS"
	default:
		platformNote += osName
	}
	platformNote += " (" + arch + ")"
	if pythonLine := buildPythonRuntimeLine(); pythonLine != "" {
		platformNote += ". " + pythonLine
	}

	return "You are an Ally sub-agent. Complete the delegated task using available tools, then return a concise summary.\n\n" +
		"# Tool Use\n\n" +
		"Prefer dedicated tools over shell commands: `grep_files` for search, `read` for file content, `edit`/`create_file`/`delete_path` for file changes, `list_files` for directory listings.\n\n" +
		sharedBatchStrategy() +
		"# Editing Files\n\n" +
		sharedEditRules() + "\n" +
		"# Coding Guidelines\n\n" +
		sharedCodingGuidelines() + "\n" +
		"# Safety\n\n" +
		sharedSafetyBoundaries() +
		"- Do NOT ask the user questions — the user cannot see you.\n" +
		"- Do NOT call `subagent` — nested delegation is not supported.\n" +
		"- MCP tools are available when connected. Use them when they materially help the delegated task, and treat their results like any other tool output.\n" +
		"- Do NOT write global memories. The parent agent owns durable memory decisions.\n" +
		"- Use network tools only when the delegated task explicitly requires external information.\n" +
		"- When creating intermediate artifacts (scripts, drafts, test fixtures) that are not final deliverables, place them under a `.tmp/` directory within the workspace.\n\n" +
		"# Output\n\n" +
		"- Be concise. The parent agent only sees your final summary.\n" +
		"- When done, write a summary of what you did, which files you changed, and any verification results.\n" +
		"- Use `wait` only for a concrete short delay after asynchronous work has started. It must be the only tool call in that response.\n" +
		"- For remote work, every remote tool call must include an explicit target such as host:/absolute/workspace.\n" +
		"- " + platformNote + ". Use command syntax appropriate for this platform."
}

// buildSubagentEnv builds zero-cost workspace context for the sub-agent.
func (a *App) buildSubagentEnv(cfg ConfigState) string {
	var b strings.Builder
	b.WriteString("Workspace: " + cfg.Workspace + "\n")

	entries, err := a.listFilesWithConfig(cfg, ListFilesRequest{MaxDepth: 2, Limit: 60})
	if err == nil && len(entries.Entries) > 0 {
		b.WriteString("\n## Project files\n")
		for _, e := range entries.Entries {
			tag := ""
			if e.Dir {
				tag = "/"
			}
			b.WriteString(e.Path + tag + "\n")
		}
	}
	return b.String()
}

func (a *App) buildSubagentInstructionContext(cfg ConfigState) string {
	var b strings.Builder
	if cfg.Workspace != "" {
		if md := loadAgentsMd(cfg.Workspace); strings.TrimSpace(md) != "" {
			b.WriteString("## Lower-priority project instructions\n")
			b.WriteString("Follow these project instructions when they do not conflict with the sub-agent system rules or the delegated task.\n\n")
			b.WriteString(md)
			b.WriteString("\n")
		}
		if cg := buildCodeGraphPromptPart(cfg.Workspace); cg != "" {
			if b.Len() > 0 {
				b.WriteString("\n")
			}
			b.WriteString("## Lower-priority project code graph\n")
			b.WriteString("Use this as a reference map only. Verify current files before relying on it, and follow the sub-agent system rules and delegated task first.\n")
			b.WriteString(cg)
			b.WriteString("\n")
		}
	}
	if custom := strings.TrimSpace(cfg.CustomPrompt); custom != "" {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString("## Lower-priority custom instructions\n")
		b.WriteString("Follow these custom instructions when they do not conflict with the sub-agent system rules or the delegated task.\n\n")
		b.WriteString(custom)
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

// subagentTools returns tools available to sub-agents, excluding parent-owned
// session state and tools that require interaction with the visible user.
func (a *App) subagentTools(cfg ConfigState) []openai.Tool {
	all := a.buildToolsForConfig(cfg)
	filtered := make([]openai.Tool, 0, len(all)-1)
	blocked := map[string]bool{
		"subagent":       true,
		"agent_delegate": true,
		"create_goal":    true,
		"update_goal":    true,
		"get_goal":       true,
		"todo_write":     true,
		"skill":          true,
		"memory_write":   true,
		"scheduled_task": true,
		"ask":            true,
	}
	for _, t := range all {
		if t.Function != nil {
			if blocked[t.Function.Name] {
				continue
			}
		}
		filtered = append(filtered, t)
	}
	return filtered
}

// trackFileFromToolResult extracts files read/edited from tool calls for the sub-agent summary.
func trackFileFromToolResult(name string, args string, result *toolResult, filesRead *[]string, filesEdited *[]string, seen map[string]bool) {
	addPath := func(list *[]string, p string) {
		if p == "" || seen[p] {
			return
		}
		seen[p] = true
		*list = append(*list, p)
	}
	switch name {
	case "read", "read_file", "batch_read":
		// extract path from args
		var req struct {
			Path  string   `json:"path"`
			Paths []string `json:"paths"`
			Files []struct {
				Path string `json:"path"`
			} `json:"files"`
		}
		if json.Unmarshal([]byte(args), &req) == nil {
			if req.Path != "" {
				addPath(filesRead, req.Path)
			}
			for _, p := range req.Paths {
				addPath(filesRead, p)
			}
			for _, file := range req.Files {
				addPath(filesRead, file.Path)
			}
		}
	case "edit":
		var req ModelEditToolRequest
		if json.Unmarshal([]byte(args), &req) == nil {
			for _, file := range req.Files {
				addPath(filesEdited, file.Path)
			}
		}
	case "create_file", "replace_exact", "replace_lines":
		var req struct {
			Path string `json:"path"`
		}
		if json.Unmarshal([]byte(args), &req) == nil && req.Path != "" {
			addPath(filesEdited, req.Path)
		}
	}
}

func goalSessionKey(sessionID string) string {
	if strings.TrimSpace(sessionID) == "" {
		return "__default__"
	}
	return sessionID
}

func (a *App) createGoal(sessionID string, objective string, completionCriterion string, maxTurns int) (*GoalState, error) {
	if strings.TrimSpace(objective) == "" {
		return nil, errors.New("objective is required")
	}
	if maxTurns <= 0 {
		maxTurns = 200
	}
	key := goalSessionKey(sessionID)
	goal := &GoalState{
		GoalID:              newID(),
		Objective:           objective,
		CompletionCriterion: completionCriterion,
		Status:              "active",
		TurnBudget:          maxTurns,
		TurnsUsed:           0,
		TokensUsed:          0,
		WallClockMs:         0,
		CreatedAt:           time.Now().Unix(),
	}
	a.mu.Lock()
	if existing := a.goalStates[key]; existing != nil && existing.Status == "active" {
		a.mu.Unlock()
		return nil, errors.New("an active goal already exists; complete or pause it before creating a new one")
	}
	a.goalStates[key] = goal
	a.mu.Unlock()
	a.emitGoalUpdate(sessionID, goal)
	return goal, nil
}

func (a *App) updateGoal(sessionID, status, reason string) (*GoalState, error) {
	a.mu.Lock()
	goal := a.goalStates[goalSessionKey(sessionID)]
	if goal == nil {
		a.mu.Unlock()
		return nil, errors.New("no active goal")
	}
	switch status {
	case "complete", "blocked", "paused":
		goal.Status = status
		goal.StatusReason = reason
	default:
		a.mu.Unlock()
		return nil, fmt.Errorf("invalid goal status: %s", status)
	}
	result := *goal
	a.mu.Unlock()
	a.emitGoalUpdate(sessionID, &result)
	return &result, nil
}

func (a *App) emitGoalUpdate(sessionID string, goal *GoalState) {
	if strings.TrimSpace(sessionID) == "" || goal == nil {
		return
	}
	cp := *goal
	a.emit("goal:update", map[string]any{"sessionId": sessionID, "goal": cp})
}

func (a *App) recordGoalTurn(sessionID string, tokens int, elapsed time.Duration) *GoalState {
	a.mu.Lock()
	goal := a.goalStates[goalSessionKey(sessionID)]
	if goal == nil || goal.Status != "active" {
		a.mu.Unlock()
		return nil
	}
	goal.TurnsUsed++
	goal.TokensUsed += tokens
	if goal.TurnsUsed == 1 {
		goal.WallClockMs = elapsed.Milliseconds()
	}
	result := *goal
	a.mu.Unlock()
	a.emitGoalUpdate(sessionID, &result)
	return &result
}

func (a *App) getActiveGoal(sessionID string) *GoalState {
	a.mu.Lock()
	defer a.mu.Unlock()
	goal := a.goalStates[goalSessionKey(sessionID)]
	if goal != nil && goal.Status == "active" {
		cp := *goal
		return &cp
	}
	return nil
}

func (a *App) getGoalResult(sessionID string) any {
	a.mu.Lock()
	defer a.mu.Unlock()
	goal := a.goalStates[goalSessionKey(sessionID)]
	if goal == nil {
		return map[string]any{"hasGoal": false}
	}
	return map[string]any{
		"hasGoal":   true,
		"goalId":    goal.GoalID,
		"objective": goal.Objective,
		"status":    goal.Status,
		"reason":    goal.StatusReason,
		"turnsUsed": goal.TurnsUsed,
		"maxTurns":  goal.TurnBudget,
	}
}

// GetGoal returns the current goal state for one frontend session.
func (a *App) GetGoal(sessionID string) any {
	return a.getGoalResult(sessionID)
}
func (a *App) ListFiles(req ListFilesRequest) ([]FileEntry, error) {
	result, err := a.listFilesWithConfig(a.effectiveConfig(ConfigState{}), req)
	if err != nil {
		return nil, err
	}
	return result.Entries, nil
}

func (a *App) SearchWorkspacePaths(req WorkspacePathSearchRequest) (WorkspacePathSearchResult, error) {
	return a.searchWorkspacePaths(a.effectiveConfig(ConfigState{}), req)
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
	cfg := a.effectiveConfig(ConfigState{})
	result, err := a.createFileWithConfig(cfg, req)
	if err == nil {
		a.invalidateWorkspaceMapCache(cfg)
	}
	return result, err
}

func (a *App) DeletePath(req DeletePathRequest) error {
	cfg := a.effectiveConfig(ConfigState{})
	_, err := a.deletePathWithConfig(cfg, req)
	if err == nil {
		a.invalidateWorkspaceMapCache(cfg)
	}
	return err
}

func (a *App) RunCommand(req CommandRequest) (CommandResult, error) {
	return a.runCommandWithConfig(context.Background(), a.effectiveConfig(ConfigState{}), req)
}

func workspaceRoot(cfg ConfigState) (string, error) {
	return pathutil.RootFromConfig(cfg.Workspace)
}

// workspaceRoots 返回主工作区 + 会话级 ExtraRoots 的去重列表。
// 主工作区始终是 roots[0]，且必须存在；ExtraRoots 中不存在或非目录的条目被静默跳过。
// 重复路径（按 OS 风格归一化后）只保留首次出现。
func workspaceRoots(cfg ConfigState) ([]string, error) {
	return pathutil.RootsFromConfig(cfg.Workspace, cfg.ExtraRoots)
}

// insideAnyRoot 判断 target 是否落在任一 root 内（不含 symlink 解析）。
func insideAnyRoot(roots []string, target string) bool {
	return pathutil.InsideAnyRoot(roots, target)
}

func safeJoin(roots []string, p string) (string, error) {
	return pathutil.SafeJoin(pathRuntime, roots, p)
}

func resolveWritableFilePath(roots []string, p string) (string, error) {
	abs, err := safeJoin(roots, p)
	if err != nil {
		return "", codedToolError("E_PATH_OUTSIDE", err)
	}
	if info, err := os.Lstat(abs); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return "", codedToolError("E_SYMLINK_PATH", fmt.Errorf("refusing to write through symlink path: %s", p))
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	resolved, err := evalExistingPrefix(abs)
	if err != nil {
		return "", err
	}
	if !insideWriteRoot(roots, resolved) {
		return "", codedToolError("E_PATH_OUTSIDE", fmt.Errorf("path resolves outside workspace or ~/.ally_agent: %s\n允许写入的根目录：%s", p, formatAllowedRoots(roots)))
	}
	return abs, nil
}

func resolveDeletablePath(roots []string, p string) (string, error) {
	abs, err := safeJoin(roots, p)
	if err != nil {
		return "", codedToolError("E_PATH_OUTSIDE", err)
	}
	info, err := os.Lstat(abs)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", codedToolError("E_PATH_NOT_FOUND", err)
		}
		return "", err
	}
	checkPath := abs
	if info.Mode()&os.ModeSymlink != 0 {
		checkPath = filepath.Dir(abs)
	}
	resolved, err := evalExistingPrefix(checkPath)
	if err != nil {
		return "", err
	}
	if !insideWriteRoot(roots, resolved) {
		return "", codedToolError("E_PATH_OUTSIDE", fmt.Errorf("path resolves outside workspace or ~/.ally_agent: %s\n允许写入的根目录：%s", p, formatAllowedRoots(roots)))
	}
	return abs, nil
}

func resolveCommandCwd(roots []string, p string) (string, error) {
	abs, err := safeJoin(roots, p)
	if err != nil {
		return "", codedToolError("E_PATH_OUTSIDE", err)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", codedToolError("E_CWD_INVALID", err)
		}
		return "", err
	}
	if !insideWriteRoot(roots, resolved) {
		return "", codedToolError("E_PATH_OUTSIDE", fmt.Errorf("cwd resolves outside workspace or ~/.ally_agent: %s\n允许写入的根目录：%s", p, formatAllowedRoots(roots)))
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", codedToolError("E_CWD_INVALID", fmt.Errorf("cwd is not a directory: %s", p))
	}
	return filepath.Clean(resolved), nil
}

// formatAllowedRoots 把 roots 列表格式化为换行分隔的字符串，用于错误信息提示。
func formatAllowedRoots(roots []string) string {
	return pathutil.FormatAllowedRoots(roots)
}

func evalExistingPrefix(target string) (string, error) {
	return read.EvalExistingPrefix(target)
}

// insideWriteRoot 判断 target（已解析）是否落在任一可写 root 内。
// 对每个 root 都做 insideRoot 检查 + EvalSymlinks 解析；任一通过即放行。
// ~/.ally_agent 始终作为兜底白名单。
func insideWriteRoot(roots []string, target string) bool {
	return pathutil.InsideWriteRoot(pathRuntime, roots, target)
}

func requireExistingDirectory(path string, missingCode string) error {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return codedToolError(missingCode, fmt.Errorf("parent directory does not exist: %s", path))
		}
		return err
	}
	if !info.IsDir() {
		return codedToolError("E_PARENT_NOT_DIRECTORY", fmt.Errorf("parent path is not a directory: %s", path))
	}
	return nil
}

func resolveReadPath(cfg ConfigState, p string) (string, error) {
	return resolveReadablePath(cfg, p)
}

func resolveReadablePath(cfg ConfigState, p string) (string, error) {
	root, err := workspaceRoot(cfg)
	if err != nil {
		return "", err
	}
	return pathutil.ResolveReadable(pathRuntime, []string{root}, p)
}

func insideRoot(root, target string) bool {
	return pathutil.InsideRoot(root, target)
}

func insideAllyAgentDir(target string) bool {
	return pathutil.InsideAllyAgentDir(pathRuntime, target)
}

func samePath(a, b string) bool {
	return pathutil.SamePath(a, b)
}

// File IO and content-hashing helpers live in internal/tools/read. These
// thin wrappers preserve the legacy lowercase names used throughout the app
// package without duplicating the implementations.
func readTextFile(path string) ([]byte, os.FileInfo, error) {
	return read.ReadTextFile(path)
}

func normalizeText(data []byte) (string, string) {
	return read.NormalizeText(data)
}

func encodeLineEnding(text, ending string) []byte {
	return read.EncodeLineEnding(text, ending)
}

func splitLines(text string) ([]string, bool) {
	return read.SplitLines(text)
}

func safeWriteFile(path string, data []byte, perm os.FileMode) error {
	return read.SafeWriteFile(path, data, perm)
}

func safeWriteFileWithDir(path string, data []byte, perm os.FileMode, mkdirs bool) error {
	return read.SafeWriteFileWithDir(path, data, perm, mkdirs)
}

func safeWriteNewFile(path string, data []byte, perm os.FileMode) error {
	return read.SafeWriteNewFile(path, data, perm)
}

func safeWritePreparedFile(path string, data []byte, perm os.FileMode) error {
	return read.SafeWritePreparedFile(path, data, perm)
}

func modeOf(path string) os.FileMode {
	return read.ModeOf(path)
}

func hashBytes(data []byte) string {
	return read.HashBytes(data)
}

func hashVersion(data []byte) string {
	return read.HashVersion(data)
}

func versionFromSHA256Hex(value string) (string, error) {
	sum, err := hex.DecodeString(value)
	if err != nil || len(sum) != sha256.Size {
		return "", errors.New("invalid SHA-256 digest")
	}
	return read.VersionFromSHA256(sum), nil
}

func hashFileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func newID() string {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf)
}

func pathDepth(rel string) int {
	rel = filepath.Clean(rel)
	if rel == "." || rel == "" {
		return 0
	}
	return strings.Count(rel, string(os.PathSeparator)) + 1
}

func isHeavyDir(name string) bool {
	switch strings.ToLower(name) {
	case ".git", "node_modules", "dist", "build", "target", ".next", ".nuxt", ".svelte-kit", "vendor", "__pycache__":
		return true
	default:
		return false
	}
}

func isWorkspaceMapHeavyDir(name string) bool {
	if isHeavyDir(name) {
		return true
	}
	switch strings.ToLower(name) {
	case ".venv", "venv", ".cache", ".pytest_cache", ".mypy_cache", ".ruff_cache", ".turbo", ".parcel-cache", ".vite", "coverage":
		return true
	default:
		return false
	}
}

func isWorkspaceMapSensitiveFile(name string, isDir bool) bool {
	if isDir {
		return false
	}
	lower := strings.ToLower(name)
	if lower == ".env" || strings.HasPrefix(lower, ".env.") {
		return !strings.Contains(lower, "example") && !strings.Contains(lower, "sample") && !strings.Contains(lower, "template")
	}
	return false
}

type gitignoreRule struct {
	pattern  string
	negated  bool
	dirOnly  bool
	anchored bool
	hasSlash bool
}

func loadRootGitignoreRules(root string) []gitignoreRule {
	data, err := os.ReadFile(filepath.Join(root, ".gitignore"))
	if err != nil {
		return nil
	}
	lines := strings.Split(string(data), "\n")
	rules := make([]gitignoreRule, 0, len(lines))
	for _, raw := range lines {
		line := strings.TrimSpace(strings.TrimSuffix(raw, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		negated := false
		if strings.HasPrefix(line, "!") {
			negated = true
			line = strings.TrimSpace(strings.TrimPrefix(line, "!"))
		}
		if line == "" {
			continue
		}

		line = strings.ReplaceAll(line, `\#`, "#")
		line = filepath.ToSlash(line)
		dirOnly := strings.HasSuffix(line, "/")
		line = strings.TrimRight(line, "/")
		anchored := strings.HasPrefix(line, "/")
		line = strings.TrimLeft(line, "/")
		if line == "" || line == "." {
			continue
		}
		rules = append(rules, gitignoreRule{
			pattern:  line,
			negated:  negated,
			dirOnly:  dirOnly,
			anchored: anchored,
			hasSlash: strings.Contains(line, "/"),
		})
	}
	return rules
}

func matchGitignoreRules(rules []gitignoreRule, relPath string, isDir bool) bool {
	relPath = strings.Trim(filepath.ToSlash(relPath), "/")
	ignored := false
	for _, rule := range rules {
		if matchGitignoreRule(rule, relPath, isDir) {
			ignored = !rule.negated
		}
	}
	return ignored
}

func matchGitignoreRule(rule gitignoreRule, relPath string, isDir bool) bool {
	if relPath == "" {
		return false
	}
	if rule.dirOnly && !isDir {
		return false
	}
	if rule.anchored || rule.hasSlash {
		return matchWorkspaceGlob(rule.pattern, relPath)
	}
	for _, part := range strings.Split(relPath, "/") {
		if matchWorkspaceGlob(rule.pattern, part) {
			return true
		}
	}
	return false
}

func matchWorkspaceGlob(pattern, target string) bool {
	if pattern == target {
		return true
	}
	if strings.Contains(pattern, "**") {
		re, err := regexp.Compile("^" + grep.GlobPatternToRegex(pattern) + "$")
		if err == nil && re.MatchString(target) {
			return true
		}
	}
	matched, err := path.Match(pattern, target)
	return err == nil && matched
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
	a.config.Temperature = m.Temperature
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

// Sub-agent frontend bindings (GetSubagents, cloneSubagentRun, StopSubagent)
// moved to orch_subagent.go.
