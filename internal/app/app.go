package app

import (
	"bufio"
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
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

	"ally-dev/internal/tools/command"
	"ally-dev/internal/tools/edit"
	"ally-dev/internal/tools/grep"
	"ally-dev/internal/tools/read"
	toolshared "ally-dev/internal/tools/shared"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
	openai "github.com/sashabaranov/go-openai"
	"golang.org/x/net/html"
	"golang.org/x/net/html/charset"
	"golang.org/x/text/transform"
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
	taskbarActiveRuns   int

	servicesMu sync.Mutex
	services   map[string]*managedService

	scheduledMu sync.Mutex
	scheduled   *scheduledTaskManager

	// lastEstimatedTokens is retained for ResetWorkspaceTokenUsage cleanup;
	// recordWorkspaceTokenUsage no longer uses fallback delta logic.
	lastEstimatedTokens map[string]WorkspaceTokenUsage
}

func NewApp() *App {
	return &App{
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
		lastEstimatedTokens: map[string]WorkspaceTokenUsage{},
	}
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
	ProviderName  string  `json:"providerName"`
	APIFormat     string  `json:"apiFormat"`
	BaseURL       string  `json:"baseUrl"`
	APIKey        string  `json:"apiKey"`
	Model         string  `json:"model"`
	Temperature   float32 `json:"temperature"`
	MaxTokens     int     `json:"maxTokens"`
	ContextWindow int     `json:"contextWindow"`
	ReasoningTag  string  `json:"reasoningTag,omitempty"`
	// TokenParam selects which token-limit field the OpenAI Chat adapter
	// sends: "auto"/"max_tokens" -> max_tokens (broadest compatibility),
	// "max_completion_tokens" -> max_completion_tokens (official OpenAI
	// o-series / newer GPT models that reject the legacy field). Ignored by
	// the Responses and Anthropic adapters.
	TokenParam string `json:"tokenParam,omitempty"`
}

type ConfigState struct {
	ProviderName        string        `json:"providerName"`
	APIFormat           string        `json:"apiFormat"`
	BaseURL             string        `json:"baseUrl"`
	APIKey              string        `json:"apiKey"`
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
	grillMode      bool
	temperatureSet bool
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

type MemoryIndexEntry struct {
	Path        string `json:"path"`
	Description string `json:"description"`
	SHA256      string `json:"sha256,omitempty"`
	Size        int64  `json:"size,omitempty"`
}

type MemoryListResult struct {
	Dir      string             `json:"dir"`
	Memories []MemoryIndexEntry `json:"memories"`
	Count    int                `json:"count"`
}

type MemoryReadRequest struct {
	Path string `json:"path"`
}

type MemoryReadResult struct {
	Path        string `json:"path"`
	Description string `json:"description"`
	Content     string `json:"content"`
	SHA256      string `json:"sha256"`
	Version     string `json:"version"`
	Size        int64  `json:"size"`
}

type MemoryWriteRequest struct {
	Path        string `json:"path"`
	Description string `json:"description"`
	Content     string `json:"content"`
	Version     string `json:"version,omitempty"`
}

type MemoryWriteResult struct {
	Path         string `json:"path"`
	Description  string `json:"description"`
	SHA256       string `json:"sha256"`
	Version      string `json:"version"`
	Size         int64  `json:"size"`
	Created      bool   `json:"created"`
	UpdatedIndex bool   `json:"updatedIndex"`
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

func memoriesDir() string {
	return filepath.Join(appDataDir(), "memories")
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
	if overlay.APIKey != "" {
		base.APIKey = overlay.APIKey
	}
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
	if base.APIFormat == "" {
		base.APIFormat = apiFormatOpenAIChat
	}
	base.ReasoningTag = normalizeReasoningTag(base.ReasoningTag)
	for i := range base.Models {
		base.Models[i].ReasoningTag = normalizeReasoningTag(base.Models[i].ReasoningTag)
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
		ProviderName:  model.ProviderName,
		APIFormat:     normalizeAPIFormat(model.APIFormat),
		BaseURL:       strings.TrimSpace(model.BaseURL),
		APIKey:        strings.TrimSpace(model.APIKey),
		Model:         strings.TrimSpace(model.Model),
		Temperature:   model.Temperature,
		MaxTokens:     32,
		ContextWindow: model.ContextWindow,
		TokenParam:    normalizeTokenParam(model.TokenParam),
		ReasoningTag:  normalizeReasoningTag(model.ReasoningTag),
		ProxyMode:     networkCfg.ProxyMode,
		ProxyURL:      networkCfg.ProxyURL,
		ProxyNoProxy:  networkCfg.ProxyNoProxy,
		UserAgent:     networkCfg.UserAgent,
	}
	if cfg.Model == "" {
		return errors.New("model is required")
	}
	if cfg.APIKey == "" {
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
}

func (a *App) endTaskbarRun() {
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
	if strings.TrimSpace(cfg.APIKey) == "" {
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
		diskPath := filepath.Join(a.historiesDir, url.PathEscape(sessionID)+".json")
		if err := os.Remove(diskPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
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
	if strings.TrimSpace(cfg.APIKey) == "" {
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

	a.mu.Lock()
	a.histories[sessionID] = newHistory
	a.mu.Unlock()

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
		TagName string `json:"tag_name"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return CheckForUpdatesResult{Error: err.Error()}
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
	defer func() {
		a.endTaskbarRun()
		a.finishRun(runID)
	}()

	a.emit("run:start", map[string]any{"runId": runID, "sessionId": sessionID})

	messages := a.buildMessages(req, cfg, a.listCachedSkills())
	tools := a.buildToolsForConfig(cfg)
	startTime := time.Now()
	grillProtocolRetries := 0
	emitRunEnd := func(event string, payload map[string]any) {
		if payload == nil {
			payload = map[string]any{}
		}
		payload["runId"] = runID
		payload["sessionId"] = sessionID
		payload["durationMs"] = time.Since(startTime).Milliseconds()
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
				a.saveHistory(req.SessionID, messages)
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
				a.saveHistory(req.SessionID, messages)
				emitRunEnd("run:error", map[string]any{"error": err.Error()})
				return
			}

			content := modelResp.Content
			reasoning := modelResp.Reasoning
			toolCalls = modelResp.ToolCalls
			a.recordWorkspaceTokenUsage(cfg.Workspace, modelResp.Usage, estimateRequestTokens(messages, tools), estimateCompletionTokens(content, reasoning, toolCalls))
			if stopErr := modelResponseStopError(cfg, modelResp); stopErr != nil {
				if content != "" || reasoning != "" {
					messages = append(messages, openai.ChatCompletionMessage{
						Role:             openai.ChatMessageRoleAssistant,
						Content:          content,
						ReasoningContent: reasoning,
					})
				}
				a.saveHistory(req.SessionID, messages)
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
							a.saveHistory(req.SessionID, messages)
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

		a.saveHistory(req.SessionID, messages)
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
	b.WriteString("Never end your turn with a dangling `in_progress` item that is actually finished.")
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

	lastMatchedFrontend := -1
	backendAt := 0
	for i, item := range front {
		if item.key == "" {
			continue
		}
		for backendAt < len(backendKeys) {
			if backendKeys[backendAt] == item.key {
				lastMatchedFrontend = i
				backendAt++
				break
			}
			backendAt++
		}
	}

	for _, item := range front[lastMatchedFrontend+1:] {
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

func (a *App) saveHistory(sessionID string, messages []openai.ChatCompletionMessage) {
	if sessionID == "" {
		return
	}
	filtered := trimSavedHistory(sanitizeHistoryMessages(messages))
	a.mu.Lock()
	a.histories[sessionID] = cloneChatMessages(filtered)
	a.mu.Unlock()

	// Persist to disk
	if a.historiesDir != "" {
		safeName := url.PathEscape(sessionID)
		diskPath := filepath.Join(a.historiesDir, safeName+".json")
		if data, err := json.Marshal(filtered); err == nil {
			_ = os.WriteFile(diskPath, data, 0644)
		}
	}
}

func (a *App) loadHistoryLocked(sessionID string) []openai.ChatCompletionMessage {
	if a.historiesDir == "" {
		return nil
	}
	safeName := url.PathEscape(sessionID)
	diskPath := filepath.Join(a.historiesDir, safeName+".json")
	data, err := os.ReadFile(diskPath)
	if err != nil {
		return nil
	}
	var messages []openai.ChatCompletionMessage
	if err := json.Unmarshal(data, &messages); err != nil {
		return nil
	}
	messages = trimSavedHistory(sanitizeHistoryMessages(messages))
	a.histories[sessionID] = messages
	return messages
}

const maxSavedHistoryMessages = 40

func trimSavedHistory(messages []openai.ChatCompletionMessage) []openai.ChatCompletionMessage {
	if len(messages) <= maxSavedHistoryMessages {
		return messages
	}
	start := len(messages) - maxSavedHistoryMessages
	for start < len(messages) && messages[start].Role == openai.ChatMessageRoleTool {
		start++
	}
	if start < len(messages) {
		return messages[start:]
	}

	// The tail is one oversized tool-result batch. Drop that incomplete batch
	// and retain the preceding bounded conversation instead of saving orphans.
	end := len(messages) - maxSavedHistoryMessages
	for end > 0 && messages[end-1].Role == openai.ChatMessageRoleTool {
		end--
	}
	if end > 0 && len(messages[end-1].ToolCalls) > 0 {
		end--
	}
	if end <= 0 {
		return nil
	}
	start = end - maxSavedHistoryMessages
	if start < 0 {
		start = 0
	}
	for start < end && messages[start].Role == openai.ChatMessageRoleTool {
		start++
	}
	return messages[start:end]
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

		modelResp, err := a.streamModelResponse(ctx, cfg, model, messages, tools, nil)
		if err == nil {
			err = modelResponseStopError(cfg, modelResp)
		}
		if err != nil {
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

	return `You are an Ally sub-agent. Complete the delegated task using available tools, then return a concise summary.

# Tool Use

Prefer dedicated tools over shell commands: grep_files for search, read for file content, edit/create_file/delete_path for file changes, list_files for directory listings.

**Batch and parallelize aggressively** — this is the #1 way to reduce round-trips:
- If you need file contents, prefer one read call with all relevant paths instead of separate reads.
- If you need to edit files, put all cross-file changes in one edit call.
- If you need to search across files, send one grep_files instead of reading each file.
- The backend executes independent non-file tool calls in parallel; built-in file mutations are ordered by tool-call index.

# Edit Rules

- Read files before their first edit. read returns raw content and version; edit accepts a files array with path, version, and changes per file.
- A successful edit returns the new version for every file. Reuse it for a follow-up edit when exact current oldText is already known. Re-read only when content is unknown, external modification is possible, or E_VERSION_MISMATCH/E_NO_MATCH/E_MULTI_MATCH occurs.
- Put every independent replacement for the same file in one edit call. Each oldText must be non-empty, exact, unique in the original snapshot, and non-overlapping with other changes.
- Empty newText deletes oldText. Insert by replacing a unique anchor with the anchor plus inserted content.
- Do not use patch, unified diff, git apply, or patch-style edits.
- Never send multiple file-mutation tool calls for the same path in one tool batch; the backend rejects the entire conflicting path group.
- Repeated normalized paths inside one local edit call are merged when their versions match; prefer one file entry with all changes when possible.

# Coding Guidelines

- Understand relevant code before changing it; fix root causes with focused changes and update all affected call sites.
- Do not weaken valid assertions merely to make tests pass; update tests when the intended behavior changes.
- Avoid unrelated cleanup and premature abstractions.
- After edits, run the narrowest relevant build/test/lint command when feasible and include the result in your summary.

# Safety

- Workspace boundary: write/edit/create/delete and shell commands are allowed only inside the workspace, except ~/.ally_agent is also allowed for Ally global config.
- Do NOT ask the user questions — the user cannot see you.
- Do NOT call subagent — nested delegation is not supported.
- MCP tools are available when connected. Use them when they materially help the delegated task, and treat their results like any other tool output.
- Do NOT write global memories. The parent agent owns durable memory decisions.
- Use network tools only when the delegated task explicitly requires external information.
- Do NOT use shell deletion commands; use delete_path for deletion.
- Never delete or overwrite workspace root, home roots, system directories, or any path containing .git.
- When creating intermediate artifacts (scripts, drafts, test fixtures) that are not final deliverables, place them under a ` + "`.tmp/`" + ` directory within the workspace.

# Output

- Be concise. The parent agent only sees your final summary.
- When done, write a summary of what you did, which files you changed, and any verification results.
- Use wait only for a concrete short delay after asynchronous work has started. It must be the only tool call in that response.
- For remote work, every remote tool call must include an explicit target such as host:/absolute/workspace.
- ` + platformNote + `. Use command syntax appropriate for this platform.`
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

type httpFetchResult struct {
	Result HTTPRequestToolResult
	Raw    []byte
}

func (a *App) httpRequestTool(ctx context.Context, req HTTPRequestToolRequest) (HTTPRequestToolResult, error) {
	return a.httpRequestToolWithConfig(ctx, a.effectiveConfigSafe(), req)
}

func (a *App) httpRequestToolWithConfig(ctx context.Context, cfg ConfigState, req HTTPRequestToolRequest) (HTTPRequestToolResult, error) {
	if strings.TrimSpace(req.SaveTo) != "" && req.MaxBytes <= 0 {
		req.MaxBytes = maxHTTPBodyBytes
	}
	allowPrivate := cfg.AllowPrivateNetwork
	if req.AllowPrivateNetwork != nil {
		allowPrivate = *req.AllowPrivateNetwork
	}
	fetched, err := a.doHTTPRequest(ctx, cfg, req, false, allowPrivate)
	if err != nil {
		return HTTPRequestToolResult{}, err
	}
	if strings.TrimSpace(req.SaveTo) != "" {
		roots, err := workspaceRoots(cfg)
		if err != nil {
			return HTTPRequestToolResult{}, err
		}
		path, err := resolveWritableFilePath(roots, req.SaveTo)
		if err != nil {
			return HTTPRequestToolResult{}, err
		}
		if err := safeWriteFile(path, fetched.Raw, 0o644); err != nil {
			return HTTPRequestToolResult{}, err
		}
		fetched.Result.SavedPath = filepath.ToSlash(req.SaveTo)
		fetched.Result.Body, fetched.Result.BodyBase64 = "", ""
		fetched.Result.JSON, fetched.Result.JSONPreview = nil, ""
	}
	return fetched.Result, nil
}

func (a *App) webFetchToolWithConfig(ctx context.Context, cfg ConfigState, req WebFetchRequest) (WebFetchResult, error) {
	if strings.TrimSpace(req.URL) == "" {
		return WebFetchResult{}, errors.New("url is required")
	}
	maxChars := req.MaxChars
	if maxChars <= 0 {
		maxChars = 60000
	}
	if maxChars > 200000 {
		maxChars = 200000
	}
	if req.MaxBytes <= 0 {
		req.MaxBytes = defaultWebFetchBody
	}
	respectRobots := req.RespectRobots
	if respectRobots == nil {
		v := false
		respectRobots = &v
	}
	allowPrivate := cfg.AllowPrivateNetwork
	if req.AllowPrivateNetwork != nil {
		allowPrivate = *req.AllowPrivateNetwork
	}
	fetched, err := a.doHTTPRequest(ctx, cfg, HTTPRequestToolRequest{
		Method:          "GET",
		URL:             req.URL,
		Headers:         mergeStringMaps(map[string]string{"Accept": "text/html,application/xhtml+xml,text/plain;q=0.9,*/*;q=0.5"}, req.Headers),
		TimeoutSeconds:  req.TimeoutSeconds,
		MaxBytes:        req.MaxBytes,
		FollowRedirects: boolPtr(true),
		RespectRobots:   respectRobots,
	}, true, allowPrivate)
	if err != nil {
		return WebFetchResult{}, err
	}

	contentType := fetched.Result.ContentType
	title := ""
	text := ""
	links := []WebFetchLink{}
	if isHTMLContentType(contentType) {
		htmlData := fetched.Raw
		if fetched.Result.BodyEncoding == "text" {
			htmlData = []byte(fetched.Result.Body)
		}
		title, text, links = htmlReadableText(htmlData, fetched.Result.FinalURL)
	} else if fetched.Result.BodyEncoding == "text" {
		text = fetched.Result.Body
	} else {
		return WebFetchResult{}, codedToolError("E_WEB_FETCH_NOT_TEXT", fmt.Errorf("web_fetch expected readable text/html, got %q", contentType))
	}

	text = normalizeWhitespace(text)
	truncated := fetched.Result.Truncated
	if len([]rune(text)) > maxChars {
		text = truncateRunes(text, maxChars)
		truncated = true
	}

	return WebFetchResult{
		URL:           fetched.Result.URL,
		FinalURL:      fetched.Result.FinalURL,
		Status:        fetched.Result.Status,
		StatusText:    fetched.Result.StatusText,
		Title:         title,
		Text:          text,
		ContentType:   contentType,
		Links:         links,
		BytesRead:     fetched.Result.BytesRead,
		Truncated:     truncated,
		DurationMS:    fetched.Result.DurationMS,
		RobotsAllowed: fetched.Result.RobotsAllowed,
	}, nil
}

func (a *App) doHTTPRequest(parent context.Context, cfg ConfigState, req HTTPRequestToolRequest, preferText bool, allowPrivateNetwork bool) (httpFetchResult, error) {
	method := strings.ToUpper(strings.TrimSpace(req.Method))
	if method == "" {
		method = http.MethodGet
	}
	if strings.ContainsAny(method, " \t\r\n") {
		return httpFetchResult{}, codedToolError("E_HTTP_BAD_METHOD", fmt.Errorf("invalid HTTP method %q", method))
	}
	target, err := normalizeHTTPRequestURL(req.URL, req.Query)
	if err != nil {
		return httpFetchResult{}, codedToolError("E_HTTP_BAD_URL", err)
	}
	if err := validateHTTPURLAccessForConfig(target, allowPrivateNetwork, cfg); err != nil {
		return httpFetchResult{}, err
	}

	timeout := req.TimeoutSeconds
	if timeout <= 0 {
		timeout = defaultHTTPTimeout
	}
	if timeout > 120 {
		timeout = 120
	}
	maxBytes := req.MaxBytes
	if maxBytes <= 0 {
		maxBytes = defaultHTTPMaxBody
	}
	if maxBytes > maxHTTPBodyBytes {
		maxBytes = maxHTTPBodyBytes
	}

	headers := normalizeHeaders(req.Headers)
	ua := headerValue(headers, "User-Agent")
	if ua == "" {
		ua = defaultHTTPUA
		headers["User-Agent"] = ua
	}
	if headerValue(headers, "Accept") == "" {
		if preferText {
			headers["Accept"] = "text/html,application/xhtml+xml,text/plain;q=0.9,*/*;q=0.5"
		} else {
			headers["Accept"] = "*/*"
		}
	}
	if headerValue(headers, "Accept-Language") == "" {
		headers["Accept-Language"] = "en-US,en;q=0.8"
	}
	if headerValue(headers, "Accept-Encoding") == "" {
		headers["Accept-Encoding"] = "gzip, deflate, br, zstd"
	}

	if boolDefault(req.RespectRobots, false) && (method == http.MethodGet || method == http.MethodHead) {
		allowed, err := a.robotsAllows(parent, cfg, target, ua, allowPrivateNetwork)
		if err != nil {
			return httpFetchResult{}, err
		}
		if !allowed {
			return httpFetchResult{}, fmt.Errorf("blocked by robots.txt for %s", target.String())
		}
	}

	var body io.Reader
	if req.Body != "" && req.JSON != nil {
		return httpFetchResult{}, errors.New("body and json are mutually exclusive")
	}
	if req.JSON != nil {
		payload, err := json.Marshal(req.JSON)
		if err != nil {
			return httpFetchResult{}, fmt.Errorf("encode json body: %w", err)
		}
		body = bytes.NewReader(payload)
		if headerValue(headers, "Content-Type") == "" {
			headers["Content-Type"] = "application/json"
		}
	} else if req.Body != "" {
		body = strings.NewReader(req.Body)
	}

	ctx, cancel := context.WithTimeout(parent, time.Duration(timeout)*time.Second)
	defer cancel()

	redirects := []string{}
	followRedirects := boolDefault(req.FollowRedirects, true)
	client := &http.Client{
		Timeout:   time.Duration(timeout) * time.Second,
		Transport: httpTransport(cfg, allowPrivateNetwork),
		CheckRedirect: func(next *http.Request, via []*http.Request) error {
			if !followRedirects {
				return http.ErrUseLastResponse
			}
			if len(via) >= 5 {
				return errors.New("stopped after 5 redirects")
			}
			if err := validateHTTPURLAccessForConfig(next.URL, allowPrivateNetwork, cfg); err != nil {
				return err
			}
			redirects = append(redirects, next.URL.String())
			previousURL := target
			if len(via) > 0 && via[len(via)-1] != nil && via[len(via)-1].URL != nil {
				previousURL = via[len(via)-1].URL
			}
			sameOrigin := sameHTTPOrigin(previousURL, next.URL)
			if !sameOrigin {
				stripSensitiveRedirectHeaders(next.Header)
			}
			for k, v := range headers {
				if strings.EqualFold(k, "Host") || (!sameOrigin && isSensitiveRedirectHeader(k)) {
					continue
				}
				if next.Header.Get(k) == "" {
					next.Header.Set(k, v)
				}
			}
			return nil
		},
	}

	if err := a.waitHTTPRateLimit(ctx, target); err != nil {
		return httpFetchResult{}, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, method, target.String(), body)
	if err != nil {
		return httpFetchResult{}, err
	}
	for k, v := range headers {
		if strings.EqualFold(k, "Host") {
			httpReq.Host = v
			continue
		}
		httpReq.Header.Set(k, v)
	}

	start := time.Now()
	resp, err := client.Do(httpReq)
	duration := time.Since(start).Milliseconds()
	if err != nil {
		return httpFetchResult{}, err
	}

	decodedBody, contentEncoding, err := decodedHTTPBody(resp)
	if err != nil {
		return httpFetchResult{}, err
	}
	defer decodedBody.Close()

	raw, truncated, err := readLimited(decodedBody, maxBytes)
	if err != nil {
		return httpFetchResult{}, err
	}
	contentType := resp.Header.Get("Content-Type")
	mediaType, _, _ := mime.ParseMediaType(contentType)
	if mediaType == "" {
		mediaType = contentType
	}

	result := HTTPRequestToolResult{
		Method:        method,
		URL:           target.String(),
		FinalURL:      resp.Request.URL.String(),
		Status:        resp.StatusCode,
		StatusText:    resp.Status,
		Headers:       flattenHTTPHeaders(resp.Header),
		ContentType:   mediaType,
		BytesRead:     len(raw),
		Truncated:     truncated,
		DurationMS:    duration,
		Redirects:     redirects,
		RobotsAllowed: boolDefault(req.RespectRobots, false),
	}
	if contentEncoding != "" {
		result.Headers["Ally-Decoded-Content-Encoding"] = contentEncoding
	}
	if isTextResponse(mediaType, raw) {
		result.Body = decodeHTTPText(raw, contentType)
		result.BodyEncoding = "text"
	} else {
		result.BodyBase64 = base64.StdEncoding.EncodeToString(raw)
		result.BodyEncoding = "base64"
	}
	if isJSONContentType(mediaType) {
		var parsed any
		if err := json.Unmarshal(raw, &parsed); err == nil {
			result.JSON = parsed
			result.JSONPreview, result.JSONTruncated = previewJSON(parsed, maxHTTPJSONPreview)
			if truncated {
				result.JSONTruncated = true
			}
		}
	}
	return httpFetchResult{Result: result, Raw: raw}, nil
}

func sameHTTPOrigin(a, b *url.URL) bool {
	if a == nil || b == nil {
		return false
	}
	return strings.EqualFold(a.Scheme, b.Scheme) &&
		strings.EqualFold(a.Hostname(), b.Hostname()) &&
		effectiveHTTPPort(a) == effectiveHTTPPort(b)
}

func effectiveHTTPPort(u *url.URL) string {
	if u == nil {
		return ""
	}
	if port := u.Port(); port != "" {
		return port
	}
	switch strings.ToLower(u.Scheme) {
	case "http":
		return "80"
	case "https":
		return "443"
	default:
		return ""
	}
}

func isSensitiveRedirectHeader(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "authorization", "proxy-authorization", "cookie", "cookie2",
		"x-api-key", "api-key", "x-auth-token", "x-access-token",
		"x-csrf-token", "x-xsrf-token":
		return true
	default:
		return false
	}
}

func stripSensitiveRedirectHeaders(headers http.Header) {
	for name := range headers {
		if isSensitiveRedirectHeader(name) {
			headers.Del(name)
		}
	}
}

func isJSONContentType(mediaType string) bool {
	mediaType = strings.ToLower(strings.TrimSpace(mediaType))
	return mediaType == "application/json" ||
		mediaType == "text/json" ||
		strings.HasSuffix(mediaType, "+json")
}

func previewJSON(value any, limit int) (string, bool) {
	if value == nil || limit <= 0 {
		return "", false
	}
	pretty, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return "", false
	}
	if len(pretty) <= limit {
		return string(pretty), false
	}
	return truncateRunes(string(pretty), limit), true
}

func normalizeHTTPRequestURL(rawURL string, query map[string]string) (*url.URL, error) {
	if strings.TrimSpace(rawURL) == "" {
		return nil, errors.New("url is required")
	}
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return nil, fmt.Errorf("invalid url: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("unsupported URL scheme %q; only http and https are allowed", parsed.Scheme)
	}
	if parsed.Hostname() == "" {
		return nil, errors.New("url must include a host")
	}
	if len(query) > 0 {
		q := parsed.Query()
		for k, v := range query {
			q.Set(k, v)
		}
		parsed.RawQuery = q.Encode()
	}
	return parsed, nil
}

func validateHTTPURLAccess(target *url.URL, allowPrivate bool) error {
	if target == nil {
		return errors.New("url is required")
	}
	if target.Scheme != "http" && target.Scheme != "https" {
		return fmt.Errorf("unsupported URL scheme %q; only http and https are allowed", target.Scheme)
	}
	host := target.Hostname()
	if host == "" {
		return errors.New("url must include a host")
	}
	if allowPrivate {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return fmt.Errorf("resolve %s: %w", host, err)
	}
	if len(ips) == 0 {
		return fmt.Errorf("resolve %s: no addresses", host)
	}
	for _, ipAddr := range ips {
		if isPrivateHTTPAddress(ipAddr.IP) {
			return fmt.Errorf("refusing private or local network address %s for host %s because allowPrivateNetwork=false", ipAddr.IP.String(), host)
		}
	}
	return nil
}

func validateHTTPURLAccessForConfig(target *url.URL, allowPrivate bool, cfg ConfigState) error {
	if normalizeProxyMode(cfg.ProxyMode) == proxyModeOff {
		return validateHTTPURLAccess(target, allowPrivate)
	}
	if target == nil || (target.Scheme != "http" && target.Scheme != "https") || target.Hostname() == "" {
		return validateHTTPURLAccess(target, allowPrivate)
	}
	if allowPrivate {
		return nil
	}
	if ip := net.ParseIP(target.Hostname()); ip != nil && isPrivateHTTPAddress(ip) {
		return fmt.Errorf("refusing private or local network address %s because allowPrivateNetwork=false", ip)
	}
	// Proxy DNS may resolve names that are intentionally unavailable locally.
	// Keep literal private IP blocking, but let the configured proxy resolve hostnames.
	return nil
}

func httpTransport(cfg ConfigState, allowPrivate bool) *http.Transport {
	return proxyHTTPTransport(cfg, allowPrivate)
}

type httpDecodedReadCloser struct {
	reader  io.Reader
	closers []io.Closer
}

func (r *httpDecodedReadCloser) Read(p []byte) (int, error) {
	return r.reader.Read(p)
}

func (r *httpDecodedReadCloser) Close() error {
	var firstErr error
	for i := len(r.closers) - 1; i >= 0; i-- {
		if err := r.closers[i].Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

type zstdReadCloser struct {
	*zstd.Decoder
}

func (r zstdReadCloser) Close() error {
	r.Decoder.Close()
	return nil
}

func decodedHTTPBody(resp *http.Response) (io.ReadCloser, string, error) {
	if resp == nil || resp.Body == nil {
		return io.NopCloser(bytes.NewReader(nil)), "", nil
	}
	contentEncoding := strings.TrimSpace(resp.Header.Get("Content-Encoding"))
	if contentEncoding == "" {
		return resp.Body, "", nil
	}

	reader := io.Reader(resp.Body)
	closers := []io.Closer{resp.Body}
	encodings := splitHTTPContentEncodings(contentEncoding)
	for i := len(encodings) - 1; i >= 0; i-- {
		encoding := encodings[i]
		switch encoding {
		case "", "identity":
			continue
		case "gzip", "x-gzip":
			gr, err := gzip.NewReader(reader)
			if err != nil {
				_ = (&httpDecodedReadCloser{reader: reader, closers: closers}).Close()
				return nil, contentEncoding, fmt.Errorf("decode gzip response: %w", err)
			}
			reader = gr
			closers = append(closers, gr)
		case "deflate":
			dr, err := newDeflateReader(reader)
			if err != nil {
				_ = (&httpDecodedReadCloser{reader: reader, closers: closers}).Close()
				return nil, contentEncoding, fmt.Errorf("decode deflate response: %w", err)
			}
			reader = dr
			closers = append(closers, dr)
		case "br":
			reader = brotli.NewReader(reader)
		case "zstd", "x-zstd":
			zr, err := zstd.NewReader(reader)
			if err != nil {
				_ = (&httpDecodedReadCloser{reader: reader, closers: closers}).Close()
				return nil, contentEncoding, fmt.Errorf("decode zstd response: %w", err)
			}
			reader = zr
			closers = append(closers, zstdReadCloser{zr})
		default:
			_ = (&httpDecodedReadCloser{reader: reader, closers: closers}).Close()
			return nil, contentEncoding, fmt.Errorf("unsupported content encoding %q", encoding)
		}
	}
	return &httpDecodedReadCloser{reader: reader, closers: closers}, contentEncoding, nil
}

func splitHTTPContentEncodings(value string) []string {
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.ToLower(strings.TrimSpace(part))
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func newDeflateReader(r io.Reader) (io.ReadCloser, error) {
	br := bufio.NewReader(r)
	if header, err := br.Peek(2); err == nil && looksLikeZlibHeader(header) {
		return zlib.NewReader(br)
	}
	return flate.NewReader(br), nil
}

func looksLikeZlibHeader(header []byte) bool {
	if len(header) < 2 {
		return false
	}
	cmf := header[0]
	flg := header[1]
	word := int(cmf)<<8 | int(flg)
	return cmf&0x0f == 8 && word > 0 && word%31 == 0
}

func decodeHTTPText(data []byte, contentType string) string {
	if len(data) == 0 {
		return ""
	}
	enc, _, _ := charset.DetermineEncoding(data, contentType)
	decoded, _, err := transform.Bytes(enc.NewDecoder(), data)
	if err == nil {
		return string(decoded)
	}
	if utf8.Valid(data) {
		return string(data)
	}
	return string(bytes.ToValidUTF8(data, []byte("\uFFFD")))
}

func isPrivateHTTPAddress(ip net.IP) bool {
	if ip == nil {
		return true
	}
	return ip.IsLoopback() ||
		ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() ||
		ip.IsUnspecified()
}

func normalizeHeaders(headers map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range headers {
		key := http.CanonicalHeaderKey(strings.TrimSpace(k))
		if key == "" {
			continue
		}
		out[key] = v
	}
	return out
}

func headerValue(headers map[string]string, key string) string {
	for k, v := range headers {
		if strings.EqualFold(k, key) {
			return v
		}
	}
	return ""
}

func mergeStringMaps(base, override map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range base {
		out[k] = v
	}
	for k, v := range override {
		out[k] = v
	}
	return out
}

func boolPtr(v bool) *bool {
	return &v
}

func boolDefault(v *bool, fallback bool) bool {
	if v == nil {
		return fallback
	}
	return *v
}

func (a *App) waitHTTPRateLimit(ctx context.Context, target *url.URL) error {
	host := strings.ToLower(target.Hostname())
	if host == "" {
		return nil
	}
	a.httpRateMu.Lock()
	last := a.httpLastHost[host]
	wait := httpRateDelay - time.Since(last)
	if wait <= 0 {
		a.httpLastHost[host] = time.Now()
		a.httpRateMu.Unlock()
		return nil
	}
	a.httpRateMu.Unlock()

	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
	}

	a.httpRateMu.Lock()
	a.httpLastHost[host] = time.Now()
	a.httpRateMu.Unlock()
	return nil
}

func readLimited(r io.Reader, limit int) ([]byte, bool, error) {
	var buf bytes.Buffer
	_, err := io.Copy(&buf, io.LimitReader(r, int64(limit)+1))
	if err != nil {
		return nil, false, err
	}
	data := buf.Bytes()
	if len(data) > limit {
		return data[:limit], true, nil
	}
	return data, false, nil
}

func flattenHTTPHeaders(headers http.Header) map[string]string {
	out := map[string]string{}
	for k, values := range headers {
		out[k] = strings.Join(values, ", ")
	}
	return out
}

func isTextResponse(mediaType string, data []byte) bool {
	mediaType = strings.ToLower(mediaType)
	if strings.HasPrefix(mediaType, "text/") {
		return true
	}
	switch mediaType {
	case "application/json", "application/xml", "application/xhtml+xml", "application/javascript", "application/x-javascript", "image/svg+xml":
		return true
	}
	return utf8.Valid(data)
}

func isHTMLContentType(contentType string) bool {
	contentType = strings.ToLower(contentType)
	return contentType == "text/html" || contentType == "application/xhtml+xml" || strings.Contains(contentType, "html")
}

func (a *App) robotsAllows(ctx context.Context, cfg ConfigState, target *url.URL, userAgent string, allowPrivate bool) (bool, error) {
	robotsURL := *target
	robotsURL.Path = "/robots.txt"
	robotsURL.RawPath = ""
	robotsURL.RawQuery = ""
	robotsURL.Fragment = ""
	if err := validateHTTPURLAccessForConfig(&robotsURL, allowPrivate, cfg); err != nil {
		return false, err
	}
	if err := a.waitHTTPRateLimit(ctx, &robotsURL); err != nil {
		return false, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, robotsURL.String(), nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("User-Agent", userAgent)
	client := &http.Client{Timeout: 10 * time.Second, Transport: httpTransport(cfg, allowPrivate)}
	resp, err := client.Do(req)
	if err != nil {
		return true, nil
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return true, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return true, nil
	}
	data, _, err := readLimited(resp.Body, 64*1024)
	if err != nil {
		return true, nil
	}
	return robotsPathAllowed(string(data), target.EscapedPath(), userAgent), nil
}

func robotsPathAllowed(robotsText, escapedPath, userAgent string) bool {
	if escapedPath == "" {
		escapedPath = "/"
	}
	type rule struct {
		allow   bool
		pattern string
	}
	applies := false
	currentAgents := []string{}
	rules := []rule{}
	flush := func() {
		if len(currentAgents) == 0 {
			return
		}
		matches := false
		ua := strings.ToLower(userAgent)
		for _, agent := range currentAgents {
			agent = strings.ToLower(strings.TrimSpace(agent))
			if agent == "*" || (agent != "" && strings.Contains(ua, agent)) {
				matches = true
				break
			}
		}
		if matches {
			applies = true
		}
	}

	lines := strings.Split(robotsText, "\n")
	for _, raw := range lines {
		line := strings.TrimSpace(strings.SplitN(raw, "#", 2)[0])
		if line == "" {
			if applies {
				break
			}
			currentAgents = nil
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(parts[0]))
		value := strings.TrimSpace(parts[1])
		switch key {
		case "user-agent":
			if len(rules) > 0 && !applies {
				currentAgents = nil
			}
			currentAgents = append(currentAgents, value)
		case "allow", "disallow":
			flush()
			if applies {
				rules = append(rules, rule{allow: key == "allow", pattern: value})
			}
		}
	}
	flush()

	bestLen := -1
	bestAllow := true
	for _, r := range rules {
		if r.pattern == "" {
			continue
		}
		if robotsPatternMatches(r.pattern, escapedPath) && len(r.pattern) > bestLen {
			bestLen = len(r.pattern)
			bestAllow = r.allow
		}
	}
	return bestAllow
}

func robotsPatternMatches(pattern, escapedPath string) bool {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return false
	}
	if strings.HasSuffix(pattern, "$") {
		prefix := strings.TrimSuffix(pattern, "$")
		return escapedPath == prefix
	}
	if strings.Contains(pattern, "*") {
		re, err := regexp.Compile("^" + grep.GlobPatternToRegex(pattern))
		return err == nil && re.MatchString(escapedPath)
	}
	return strings.HasPrefix(escapedPath, pattern)
}

func htmlReadableText(data []byte, finalURL string) (string, string, []WebFetchLink) {
	doc, err := html.Parse(bytes.NewReader(data))
	if err != nil {
		return "", string(data), nil
	}
	baseURL, _ := url.Parse(finalURL)
	var titleParts []string
	var textParts []string
	links := []WebFetchLink{}
	var walk func(*html.Node, bool, bool)
	walk = func(n *html.Node, hidden bool, inTitle bool) {
		if n == nil {
			return
		}
		if n.Type == html.ElementNode {
			name := strings.ToLower(n.Data)
			switch name {
			case "script", "style", "noscript", "svg", "canvas", "template":
				hidden = true
			case "title":
				inTitle = true
			case "br", "p", "div", "section", "article", "li", "tr", "h1", "h2", "h3", "h4", "h5", "h6":
				textParts = append(textParts, "\n")
			case "a":
				if len(links) < 80 {
					if href := htmlAttr(n, "href"); href != "" {
						if u, err := url.Parse(strings.TrimSpace(href)); err == nil {
							if baseURL != nil {
								u = baseURL.ResolveReference(u)
							}
							links = append(links, WebFetchLink{URL: u.String()})
						}
					}
				}
			}
		}
		if n.Type == html.TextNode && !hidden {
			text := strings.TrimSpace(n.Data)
			if text != "" {
				if inTitle {
					titleParts = append(titleParts, text)
				} else {
					textParts = append(textParts, text)
				}
			}
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child, hidden, inTitle)
		}
	}
	walk(doc, false, false)

	text := normalizeWhitespace(strings.Join(textParts, " "))
	title := normalizeWhitespace(strings.Join(titleParts, " "))
	fillLinkText(doc, links)
	return title, text, links
}

func htmlAttr(n *html.Node, key string) string {
	for _, attr := range n.Attr {
		if strings.EqualFold(attr.Key, key) {
			return attr.Val
		}
	}
	return ""
}

func fillLinkText(doc *html.Node, links []WebFetchLink) {
	idx := 0
	var walk func(*html.Node, bool)
	walk = func(n *html.Node, inLink bool) {
		if n == nil || idx >= len(links) {
			return
		}
		if n.Type == html.ElementNode && strings.EqualFold(n.Data, "a") {
			inLink = true
		}
		if inLink && n.Type == html.TextNode {
			text := normalizeWhitespace(n.Data)
			if text != "" {
				links[idx].Text = truncateRunes(text, 80)
				idx++
				inLink = false
			}
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child, inLink)
		}
	}
	walk(doc, false)
}

func normalizeWhitespace(text string) string {
	fields := strings.Fields(text)
	return strings.Join(fields, " ")
}

func truncateRunes(text string, max int) string {
	if max <= 0 {
		return ""
	}
	runes := []rune(text)
	if len(runes) <= max {
		return text
	}
	return string(runes[:max]) + "..."
}

const remotePythonMarker = "ALLY_REMOTE_RESULT_JSON:"

const remotePythonScript = `
import base64, json, os, pathlib, re, selectors, shutil, signal, subprocess, sys, tempfile, time, traceback
from datetime import datetime, timezone

MARKER = "ALLY_REMOTE_RESULT_JSON:"

def fail(msg):
    print(MARKER + json.dumps({"ok": False, "error": str(msg)}, separators=(",", ":")))
    sys.exit(0)

def ok(data):
    print(MARKER + json.dumps({"ok": True, "data": data}, separators=(",", ":")))
    sys.exit(0)

def decode_payload(arg):
    padding = "=" * (-len(arg) % 4)
    return json.loads(base64.urlsafe_b64decode((arg + padding).encode("ascii")).decode("utf-8"))

def as_posix_rel(root, path):
    return pathlib.Path(path).relative_to(root).as_posix()

def safe_join(root, rel):
    rel = "" if rel is None else str(rel)
    if "\x00" in rel:
        raise ValueError("path contains NUL byte")
    if rel == "" or rel == ".":
        return root
    rel_path = pathlib.PurePosixPath(rel.replace("\\", "/"))
    if rel_path.is_absolute():
        raise ValueError("remote path must be relative to workspaceRoot")
    if any(part == ".." for part in rel_path.parts):
        raise ValueError("remote path must not contain '..'")
    target = (root / pathlib.Path(*rel_path.parts)).resolve(strict=False)
    if os.path.commonpath([str(root), str(target)]) != str(root):
        raise ValueError("remote path is outside workspaceRoot")
    return target

def is_heavy_dir(name):
    return name.lower() in {".git", "node_modules", "dist", "build", "target", ".next", ".nuxt", ".svelte-kit", "vendor", "__pycache__"}

def contains_vcs(path):
    return any(part in {".git", ".svn", ".hg"} for part in path.parts)

def is_protected_delete_path(path):
    p = str(path)
    exact_only = ["/", "/home", "/Users", "/Volumes"]
    for item in exact_only:
        if p == item:
            return True
    protected_trees = ["/etc", "/usr", "/bin", "/sbin", "/lib", "/lib64", "/boot", "/dev", "/proc", "/sys", "/root", "/System", "/Library", "/Applications"]
    for item in protected_trees:
        if p == item or p.startswith(item.rstrip("/") + "/"):
            return True
    return False

def iso_mtime(st):
    return datetime.fromtimestamp(st.st_mtime, timezone.utc).isoformat()

def op_list(root, payload):
    start = safe_join(root, payload.get("path", ""))
    if not start.exists():
        raise FileNotFoundError(str(start))
    if not start.is_dir():
        raise ValueError("path is not a directory")
    max_depth = int(payload.get("maxDepth") or 3)
    if max_depth < 0:
        max_depth = 0
    if max_depth > 20:
        max_depth = 20
    limit = int(payload.get("limit") or 200)
    if limit < 1:
        limit = 1
    if limit > 1000:
        limit = 1000
    include_hidden = bool(payload.get("includeHidden"))
    entries = []
    truncated = False
    for current, dirs, files in os.walk(start):
        current_path = pathlib.Path(current)
        rel_current = current_path.relative_to(start)
        depth = 0 if str(rel_current) == "." else len(rel_current.parts)
        if depth >= max_depth:
            dirs[:] = []
        dirs[:] = sorted([d for d in dirs if include_hidden or not d.startswith(".")])
        dirs[:] = [d for d in dirs if include_hidden or not is_heavy_dir(d)]
        names = [(d, True) for d in dirs] + [(f, False) for f in sorted(files) if include_hidden or not f.startswith(".")]
        for name, is_dir in names:
            abs_path = current_path / name
            try:
                st = abs_path.stat()
            except OSError:
                continue
            entries.append({
                "path": as_posix_rel(root, abs_path),
                "name": name,
                "dir": is_dir,
                "size": 0 if is_dir else st.st_size,
                "modTime": iso_mtime(st),
            })
            if len(entries) >= limit:
                truncated = True
                return {"entries": entries, "count": len(entries), "truncated": truncated}
    return {"entries": entries, "count": len(entries), "truncated": truncated}

def op_read(root, payload):
    path = safe_join(root, payload.get("path", ""))
    max_bytes = int(payload.get("maxBytes") or 2097152)
    if path.is_dir():
        raise ValueError("path is a directory")
    st = path.stat()
    if st.st_size > max_bytes:
        raise ValueError("file is too large: %d bytes" % st.st_size)
    data = path.read_bytes()
    return {"path": as_posix_rel(root, path), "dataBase64": base64.b64encode(data).decode("ascii"), "size": len(data), "mode": st.st_mode & 0o777, "modTime": iso_mtime(st)}

def op_write(root, payload):
    path = safe_join(root, payload.get("path", ""))
    mkdirs = bool(payload.get("mkdirs"))
    overwrite = bool(payload.get("overwrite"))
    original_mode = None
    if path.exists() and path.is_dir():
        raise ValueError("path is a directory")
    if path.exists() and not overwrite:
        raise FileExistsError("file already exists: " + payload.get("path", ""))
    if path.exists():
        original_mode = path.stat().st_mode & 0o7777
    parent = path.parent
    if mkdirs:
        parent.mkdir(parents=True, exist_ok=True)
    elif not parent.exists():
        raise FileNotFoundError(str(parent))
    data = base64.b64decode(payload.get("dataBase64", ""))
    fd, tmp = tempfile.mkstemp(prefix=".ally-write-", dir=str(parent))
    try:
        if original_mode is not None:
            os.fchmod(fd, original_mode)
        with os.fdopen(fd, "wb") as f:
            fd = -1
            f.write(data)
            f.flush()
            os.fsync(f.fileno())
        os.replace(tmp, path)
    finally:
        if fd >= 0:
            try:
                os.close(fd)
            except OSError:
                pass
        try:
            if os.path.exists(tmp):
                os.unlink(tmp)
        except OSError:
            pass
    st = path.stat()
    return {"path": as_posix_rel(root, path), "size": st.st_size, "mode": st.st_mode & 0o7777, "modTime": iso_mtime(st)}

def op_delete(root, payload):
    path = safe_join(root, payload.get("path", ""))
    if path == root:
        raise ValueError("refusing to delete remote workspace root")
    if contains_vcs(path):
        raise ValueError("refusing to delete path containing VCS metadata")
    if is_protected_delete_path(path):
        raise ValueError("refusing to delete OS-sensitive path")
    if path.is_dir():
        if not payload.get("recursive"):
            raise ValueError("path is a directory; set recursive=true")
        shutil.rmtree(path)
    else:
        path.unlink()
    return {"deleted": payload.get("path", "")}

DELETE_RE = re.compile(r"(?i)(^|[\s;&|()])(?:rm|unlink|rmdir|del|erase|rd|remove-item|ri)\b")

def op_run(root, payload):
    command = str(payload.get("command") or "")
    if not command.strip():
        raise ValueError("command is required")
    if DELETE_RE.search(command):
        raise ValueError("remote_run_command refuses explicit deletion commands; use remote_delete_path")
    cwd = safe_join(root, payload.get("cwd", ""))
    if not cwd.is_dir():
        raise ValueError("cwd is not a directory")
    timeout = int(payload.get("timeoutSeconds") or 120)
    if timeout < 1:
        timeout = 1
    if timeout > 600:
        timeout = 600
    shell = str(payload.get("shell") or "")
    if not shell:
        shell = "/bin/bash" if os.path.exists("/bin/bash") else "/bin/sh"
    max_output = int(payload.get("maxOutput") or 131072)
    start = time.time()
    preexec = os.setsid if hasattr(os, "setsid") else None
    proc = subprocess.Popen(command, shell=True, cwd=str(cwd), executable=shell, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, stdin=subprocess.DEVNULL, preexec_fn=preexec)
    out = bytearray()
    truncated = False
    timed_out = False
    sel = selectors.DefaultSelector()
    sel.register(proc.stdout, selectors.EVENT_READ)
    deadline = start + timeout
    while True:
        if time.time() > deadline:
            timed_out = True
            try:
                if preexec:
                    os.killpg(proc.pid, signal.SIGKILL)
                else:
                    proc.kill()
            except Exception:
                pass
            break
        events = sel.select(timeout=0.1)
        for key, _ in events:
            chunk = key.fileobj.read1(8192) if hasattr(key.fileobj, "read1") else key.fileobj.read(8192)
            if not chunk:
                continue
            remain = max_output - len(out)
            if remain > 0:
                out.extend(chunk[:remain])
            if len(chunk) > remain:
                truncated = True
        if proc.poll() is not None:
            rest = proc.stdout.read() or b""
            remain = max_output - len(out)
            if remain > 0:
                out.extend(rest[:remain])
            if len(rest) > remain:
                truncated = True
            break
    exit_code = proc.poll()
    if timed_out:
        exit_code = -1
    duration = int((time.time() - start) * 1000)
    output = out.decode("utf-8", errors="replace")
    return {"command": command, "cwd": str(cwd), "shell": shell, "shellPath": shell, "output": output, "exitCode": exit_code, "timedOut": timed_out, "durationMs": duration, "truncated": truncated}

try:
    payload = decode_payload(sys.argv[1])
    root = pathlib.Path(payload["workspaceRoot"]).expanduser().resolve(strict=True)
    if str(root) == "/":
        raise ValueError("workspaceRoot must not be filesystem root")
    op = payload.get("op")
    if op == "list":
        ok(op_list(root, payload))
    elif op == "read":
        ok(op_read(root, payload))
    elif op == "write":
        ok(op_write(root, payload))
    elif op == "delete":
        ok(op_delete(root, payload))
    elif op == "run":
        ok(op_run(root, payload))
    else:
        raise ValueError("unknown op: %s" % op)
except Exception as exc:
    fail(str(exc))
`

type remotePythonResponse struct {
	OK    bool            `json:"ok"`
	Data  json.RawMessage `json:"data,omitempty"`
	Error string          `json:"error,omitempty"`
}

func parseRemoteTarget(raw string) (remoteTarget, error) {
	return parseRemoteTargetWithOptions(raw, false)
}

func parseRemoteTargetWithOptions(raw string, allowRoot bool) (remoteTarget, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return remoteTarget{}, errors.New("target is required")
	}
	if strings.HasPrefix(raw, "ssh://") {
		u, err := url.Parse(raw)
		if err != nil {
			return remoteTarget{}, fmt.Errorf("invalid ssh target: %w", err)
		}
		if u.Hostname() == "" || u.Path == "" {
			return remoteTarget{}, errors.New("ssh target must include host and absolute workspace path")
		}
		host := u.Hostname()
		if u.User != nil {
			host = u.User.String() + "@" + host
		}
		root := path.Clean(u.Path)
		if root == "." || !strings.HasPrefix(root, "/") || (!allowRoot && root == "/") {
			return remoteTarget{}, errors.New("remote workspaceRoot must be an absolute non-root path")
		}
		return remoteTarget{Raw: raw, Host: host, Port: u.Port(), WorkspaceRoot: root}, nil
	}
	idx := strings.LastIndex(raw, ":")
	if idx <= 0 || idx == len(raw)-1 {
		return remoteTarget{}, errors.New("target must be formatted as host:/absolute/workspace")
	}
	host := strings.TrimSpace(raw[:idx])
	root := strings.TrimSpace(raw[idx+1:])
	if host == "" {
		return remoteTarget{}, errors.New("target host is required")
	}
	root = path.Clean(filepath.ToSlash(root))
	if root == "." || !strings.HasPrefix(root, "/") || (!allowRoot && root == "/") {
		return remoteTarget{}, errors.New("remote workspaceRoot must be an absolute non-root path")
	}
	return remoteTarget{Raw: raw, Host: host, WorkspaceRoot: root}, nil
}

func normalizeRemoteListTarget(rawTarget, rawPath string) (remoteTarget, string, error) {
	rt, err := parseRemoteTargetWithOptions(rawTarget, true)
	if err != nil {
		return remoteTarget{}, "", err
	}
	if rt.WorkspaceRoot != "/" {
		cleanPath, err := validateRemoteRelativePath(rawPath, true)
		return rt, cleanPath, err
	}
	p := strings.TrimSpace(filepath.ToSlash(rawPath))
	if p == "" || p == "." || p == "/" {
		return remoteTarget{}, "", errors.New("remote workspaceRoot '/' is not allowed for broad listing; use target like host:/home or host:/srv/app")
	}
	if strings.ContainsRune(p, 0) {
		return remoteTarget{}, "", errors.New("path contains NUL byte")
	}
	p = strings.TrimPrefix(p, "/")
	for _, part := range strings.Split(p, "/") {
		if part == ".." {
			return remoteTarget{}, "", errors.New("path must not contain '..'")
		}
	}
	rt.WorkspaceRoot = "/" + path.Clean(p)
	return rt, ".", nil
}

func validateRemoteRelativePath(p string, allowRoot bool) (string, error) {
	p = strings.TrimSpace(filepath.ToSlash(p))
	if p == "" || p == "." {
		if allowRoot {
			return ".", nil
		}
		return "", errors.New("path is required")
	}
	if strings.ContainsRune(p, 0) {
		return "", errors.New("path contains NUL byte")
	}
	if strings.HasPrefix(p, "/") {
		return "", errors.New("path must be relative to remote workspaceRoot")
	}
	for _, part := range strings.Split(p, "/") {
		if part == ".." {
			return "", errors.New("path must not contain '..'")
		}
	}
	clean := path.Clean(p)
	if clean == "." {
		if allowRoot {
			return ".", nil
		}
		return "", errors.New("path is required")
	}
	return clean, nil
}

func remotePayload(rt remoteTarget, op string, extra map[string]any) map[string]any {
	payload := map[string]any{
		"op":            op,
		"workspaceRoot": rt.WorkspaceRoot,
	}
	for k, v := range extra {
		payload[k] = v
	}
	return payload
}

func (a *App) invokeRemotePython(ctx context.Context, rt remoteTarget, payload map[string]any, timeout time.Duration, out any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	encoded := base64.RawURLEncoding.EncodeToString(raw)
	args := []string{"-o", "BatchMode=yes", "-o", "ConnectTimeout=10"}
	if rt.Port != "" {
		args = append(args, "-p", rt.Port)
	}
	args = append(args, rt.Host, "python3", "-", encoded)
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(runCtx, "ssh", args...)
	cmd.Stdin = strings.NewReader(remotePythonScript)
	var stdout bytes.Buffer
	var stderr limitedBuffer
	stderr.limit = 64 * 1024
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	hideCommandWindow(cmd)
	err = cmd.Run()
	if runCtx.Err() == context.DeadlineExceeded {
		return fmt.Errorf("remote ssh timed out after %s", timeout)
	}
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(stdout.String())
		}
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("ssh %s failed: %s", rt.Host, msg)
	}
	line := ""
	for _, candidate := range strings.Split(stdout.String(), "\n") {
		if strings.HasPrefix(candidate, remotePythonMarker) {
			line = strings.TrimPrefix(candidate, remotePythonMarker)
		}
	}
	if line == "" {
		return fmt.Errorf("remote helper returned no JSON result; stderr: %s", strings.TrimSpace(stderr.String()))
	}
	var resp remotePythonResponse
	if err := json.Unmarshal([]byte(line), &resp); err != nil {
		return fmt.Errorf("decode remote helper result: %w", err)
	}
	if !resp.OK {
		if resp.Error == "" {
			resp.Error = "remote helper failed"
		}
		return errors.New(resp.Error)
	}
	if out != nil {
		if err := json.Unmarshal(resp.Data, out); err != nil {
			return fmt.Errorf("decode remote helper data: %w", err)
		}
	}
	return nil
}

func decodeRemoteRawFile(data struct {
	Path       string `json:"path"`
	DataBase64 string `json:"dataBase64"`
	Size       int64  `json:"size"`
	Mode       int    `json:"mode"`
	ModTime    string `json:"modTime"`
}) (remoteRawFile, error) {
	raw, err := base64.StdEncoding.DecodeString(data.DataBase64)
	if err != nil {
		return remoteRawFile{}, err
	}
	if bytes.Contains(raw, []byte{0}) {
		return remoteRawFile{}, errors.New("binary file is not supported")
	}
	if !utf8.Valid(raw) {
		return remoteRawFile{}, errors.New("file is not valid UTF-8")
	}
	_, ending := normalizeText(raw)
	return remoteRawFile{Path: data.Path, Data: raw, Size: data.Size, Mode: data.Mode, ModTime: data.ModTime, LineEnding: ending}, nil
}

func (a *App) remoteReadRaw(ctx context.Context, target, relPath string) (remoteTarget, remoteRawFile, error) {
	rt, err := parseRemoteTarget(target)
	if err != nil {
		return remoteTarget{}, remoteRawFile{}, err
	}
	cleanPath, err := validateRemoteRelativePath(relPath, false)
	if err != nil {
		return remoteTarget{}, remoteRawFile{}, err
	}
	var rawResp struct {
		Path       string `json:"path"`
		DataBase64 string `json:"dataBase64"`
		Size       int64  `json:"size"`
		Mode       int    `json:"mode"`
		ModTime    string `json:"modTime"`
	}
	err = a.invokeRemotePython(ctx, rt, remotePayload(rt, "read", map[string]any{"path": cleanPath, "maxBytes": maxReadFileBytes}), 60*time.Second, &rawResp)
	if err != nil {
		return remoteTarget{}, remoteRawFile{}, err
	}
	file, err := decodeRemoteRawFile(rawResp)
	if err != nil {
		return remoteTarget{}, remoteRawFile{}, err
	}
	return rt, file, nil
}

func (a *App) remoteWriteRaw(ctx context.Context, rt remoteTarget, relPath string, data []byte, overwrite, mkdirs bool) error {
	cleanPath, err := validateRemoteRelativePath(relPath, false)
	if err != nil {
		return err
	}
	return a.invokeRemotePython(ctx, rt, remotePayload(rt, "write", map[string]any{
		"path":       cleanPath,
		"dataBase64": base64.StdEncoding.EncodeToString(data),
		"overwrite":  overwrite,
		"mkdirs":     mkdirs,
	}), 60*time.Second, nil)
}

func (a *App) remoteListFiles(ctx context.Context, req RemoteListFilesRequest) (ListFilesResult, error) {
	rt, cleanPath, err := normalizeRemoteListTarget(req.Target, req.Path)
	if err != nil {
		return ListFilesResult{}, err
	}
	maxDepth := req.MaxDepth
	if maxDepth <= 0 {
		maxDepth = 3
	}
	limit := req.Limit
	if limit <= 0 {
		limit = 200
	}
	var result ListFilesResult
	err = a.invokeRemotePython(ctx, rt, remotePayload(rt, "list", map[string]any{
		"path":          cleanPath,
		"maxDepth":      maxDepth,
		"limit":         limit,
		"includeHidden": req.IncludeHidden,
	}), 60*time.Second, &result)
	return result, err
}

func (a *App) remoteReadFile(ctx context.Context, req RemoteReadFileRequest) (ReadFileResult, error) {
	_, file, err := a.remoteReadRaw(ctx, req.Target, req.Path)
	if err != nil {
		return ReadFileResult{}, err
	}
	text, ending := normalizeText(file.Data)
	preview, err := formatLineNumberReadPreviewRangeWithBudget(text, readRangeRequest{
		StartLine: req.StartLine,
		EndLine:   req.EndLine,
	}, maxToolOutput)
	if err != nil {
		return ReadFileResult{}, err
	}
	return ReadFileResult{
		Path:          file.Path,
		Content:       preview.RawContent,
		RawContent:    preview.RawContent,
		Kind:          "text",
		ContentFormat: "raw",
		Editable:      true,
		StartLine:     preview.StartLine,
		EndLine:       preview.EndLine,
		NextStartLine: preview.NextStartLine,
		TotalLines:    preview.TotalLines,
		SHA256:        hashBytes(file.Data),
		Version:       hashVersion(file.Data),
		Size:          file.Size,
		LineEnding:    ending,
		Truncated:     preview.Truncated,
		RangeStatus:   preview.RangeStatus,
		EmptyRange:    preview.EmptyRange,
	}, nil
}

func (a *App) remoteEdit(ctx context.Context, req RemoteEditRequest) (MultiEditResult, error) {
	if strings.TrimSpace(req.Target) == "" {
		return MultiEditResult{}, errors.New("target is required")
	}
	if err := validateModelEditToolRequest(req.Files); err != nil {
		return MultiEditResult{}, err
	}
	result := MultiEditResult{Files: make([]EditResult, 0, len(req.Files))}
	type remoteBackup struct {
		rt   remoteTarget
		path string
		data []byte
	}
	backups := make([]remoteBackup, 0, len(req.Files))
	var rollbackErrors []string
	for _, file := range req.Files {
		rt, original, err := a.remoteReadRaw(ctx, req.Target, file.Path)
		if err != nil {
			for i := len(backups) - 1; i >= 0; i-- {
				if rbErr := a.remoteWriteRaw(ctx, backups[i].rt, backups[i].path, backups[i].data, true, true); rbErr != nil {
					rollbackErrors = append(rollbackErrors, fmt.Sprintf("%s: %v", backups[i].path, rbErr))
				}
			}
			if len(rollbackErrors) > 0 {
				err = fmt.Errorf("%w (rollback failures: %s)", err, strings.Join(rollbackErrors, "; "))
			}
			return MultiEditResult{}, err
		}
		edited, err := a.remoteEditOne(ctx, rt, file, original)
		if err != nil {
			for i := len(backups) - 1; i >= 0; i-- {
				if rbErr := a.remoteWriteRaw(ctx, backups[i].rt, backups[i].path, backups[i].data, true, true); rbErr != nil {
					rollbackErrors = append(rollbackErrors, fmt.Sprintf("%s: %v", backups[i].path, rbErr))
				}
			}
			if len(rollbackErrors) > 0 {
				err = fmt.Errorf("%w (rollback failures: %s)", err, strings.Join(rollbackErrors, "; "))
			}
			return MultiEditResult{}, err
		}
		backups = append(backups, remoteBackup{rt: rt, path: file.Path, data: original.Data})
		result.Files = append(result.Files, edited)
		result.Replacements += edited.Replacements
		result.AddedLines += edited.AddedLines
		result.RemovedLines += edited.RemovedLines
	}
	result.FileCount = len(result.Files)
	result.Summary = fmt.Sprintf("Edited %d remote files", result.FileCount)
	return result, nil
}

func (a *App) remoteEditOne(ctx context.Context, rt remoteTarget, req FileTextEdits, file remoteRawFile) (EditResult, error) {
	beforeHash := hashBytes(file.Data)
	beforeVersion := hashVersion(file.Data)
	if !strings.EqualFold(req.Version, beforeVersion) {
		return EditResult{}, codedToolError("E_VERSION_MISMATCH", fmt.Errorf("remote file changed: expected version %s, current %s. Re-read before editing", req.Version, beforeVersion))
	}
	text, ending := normalizeText(file.Data)
	result, replacements, err := edit.ApplyBatchTextChanges(text, toEditChanges(req.Changes))
	if err != nil {
		return EditResult{}, err
	}
	after := encodeLineEnding(result.Content, ending)
	if bytes.Equal(file.Data, after) {
		return EditResult{}, codedToolError("E_NOOP", errors.New("edit produced no content changes"))
	}
	if err := a.remoteWriteRaw(ctx, rt, req.Path, after, true, true); err != nil {
		return EditResult{}, err
	}
	beforeLines, _ := splitLines(text)
	afterLines, _ := splitLines(result.Content)
	diff := edit.GenerateEditDiffPreview(text, result.Content, maxToolOutput)
	added, removed := 0, 0
	if diff != "" {
		added, removed = edit.CountEditDiffStats(diff, beforeLines, afterLines)
	} else {
		added, removed = edit.ApproximateLineDelta(beforeLines, afterLines)
	}
	classification := "edit"
	if len(result.Content) > len(text) {
		classification = "addition"
	} else if len(result.Content) < len(text) {
		classification = "deletion"
	}
	return EditResult{
		Path:              file.Path,
		BeforeSHA256:      beforeHash,
		AfterSHA256:       hashBytes(after),
		BeforeVersion:     beforeVersion,
		Version:           hashVersion(after),
		BeforeBytes:       len(file.Data),
		AfterBytes:        len(after),
		Replacements:      replacements,
		AddedLines:        added,
		RemovedLines:      removed,
		LineEnding:        ending,
		Summary:           fmt.Sprintf("%s updated on %s: %d -> %d bytes", file.Path, rt.Host, len(file.Data), len(after)),
		Diff:              diff,
		FirstChanged:      result.FirstChangedLine,
		LastChanged:       result.LastChangedLine,
		Warnings:          result.Warnings,
		Classification:    classification,
		ChangedLinesBlock: edit.BuildLineNumberContextBlock(result.Content, result.FirstChangedLine, result.LastChangedLine, splitLines),
	}, nil
}

func (a *App) remoteCreateFile(ctx context.Context, req RemoteCreateFileRequest) (EditResult, error) {
	rt, err := parseRemoteTarget(req.Target)
	if err != nil {
		return EditResult{}, err
	}
	cleanPath, err := validateRemoteRelativePath(req.Path, false)
	if err != nil {
		return EditResult{}, err
	}
	before := []byte{}
	beforeHash := ""
	if _, existing, readErr := a.remoteReadRaw(ctx, req.Target, cleanPath); readErr == nil {
		before = existing.Data
		beforeHash = hashBytes(existing.Data)
	}
	content, ending := normalizeText([]byte(req.Content))
	encoded := encodeLineEnding(content, ending)
	if err := a.remoteWriteRaw(ctx, rt, cleanPath, encoded, req.Overwrite, true); err != nil {
		return EditResult{}, err
	}
	return makeEditResult(cleanPath, beforeHash, before, encoded, ending, 1, string(before), content), nil
}

func (a *App) remoteDeletePath(ctx context.Context, req RemoteDeletePathRequest) (map[string]any, error) {
	rt, err := parseRemoteTarget(req.Target)
	if err != nil {
		return nil, err
	}
	cleanPath, err := validateRemoteRelativePath(req.Path, false)
	if err != nil {
		return nil, err
	}
	if cleanPath == "." {
		return nil, codedToolError("E_DELETE_BLOCKED", errors.New("refusing to delete remote workspace root"))
	}
	for _, part := range strings.Split(cleanPath, "/") {
		if part == ".git" || part == ".svn" || part == ".hg" {
			return nil, codedToolError("E_DELETE_BLOCKED", errors.New("refusing to delete VCS metadata"))
		}
	}
	var result map[string]any
	err = a.invokeRemotePython(ctx, rt, remotePayload(rt, "delete", map[string]any{"path": cleanPath, "recursive": req.Recursive}), 60*time.Second, &result)
	return result, err
}

func (a *App) remoteRunCommand(ctx context.Context, req RemoteRunCommandRequest) (CommandResult, error) {
	if strings.TrimSpace(req.Command) == "" {
		return CommandResult{}, errors.New("command is required")
	}
	if command.ContainsExplicitDeleteCommand(req.Command) && !command.IsAllowedDeleteContext(req.Command) {
		return CommandResult{}, codedToolError("E_COMMAND_BLOCKED", fmt.Errorf("remote_run_command refuses explicit deletion commands. Use remote_delete_path for deletion.\n被拦截的命令: %s", req.Command))
	}
	if err := validateRemoteCommandSafety(req.Command); err != nil {
		return CommandResult{}, err
	}
	rt, err := parseRemoteTarget(req.Target)
	if err != nil {
		return CommandResult{}, err
	}
	cwd, err := validateRemoteRelativePath(req.Cwd, true)
	if err != nil {
		return CommandResult{}, err
	}
	timeout := req.TimeoutSeconds
	if timeout <= 0 {
		timeout = defaultShellLimit
	}
	if timeout > 600 {
		timeout = 600
	}
	shell := strings.TrimSpace(req.Shell)
	if shell != "" {
		allowed := false
		for _, s := range []string{"/bin/bash", "/bin/sh", "/usr/bin/bash", "/usr/bin/sh", "/usr/bin/zsh", "/bin/zsh"} {
			if shell == s {
				allowed = true
				break
			}
		}
		if !allowed {
			return CommandResult{}, codedToolError("E_BAD_SHELL", fmt.Errorf("unsupported shell %q: only bash, sh, zsh are allowed", shell))
		}
	}
	var result CommandResult
	err = a.invokeRemotePython(ctx, rt, remotePayload(rt, "run", map[string]any{
		"command":        req.Command,
		"cwd":            cwd,
		"timeoutSeconds": timeout,
		"shell":          req.Shell,
		"maxOutput":      maxToolOutput,
	}), time.Duration(timeout+20)*time.Second, &result)
	return result, err
}

func (a *App) listFilesWithConfig(cfg ConfigState, req ListFilesRequest) (ListFilesResult, error) {
	root, err := workspaceRoot(cfg)
	if err != nil {
		return ListFilesResult{}, err
	}
	start := root
	if strings.TrimSpace(req.Path) != "" {
		start, err = resolveReadablePath(cfg, req.Path)
		if err != nil {
			return ListFilesResult{}, err
		}
	}
	info, err := os.Stat(start)
	if err != nil {
		return ListFilesResult{}, err
	}
	if !info.IsDir() {
		return ListFilesResult{}, codedToolError("E_BAD_PATH", fmt.Errorf("not a directory: %s", req.Path))
	}
	if !insideRoot(root, start) {
		if blocked, reason := isDangerousSearchRoot(start); blocked {
			return ListFilesResult{}, codedToolError("E_SEARCH_ROOT_BLOCKED", fmt.Errorf("%s\n\nThis listing has been blocked for safety. Specify a narrower project subdirectory or explicit file path.", reason))
		}
	}

	maxDepth := req.MaxDepth
	if maxDepth <= 0 {
		maxDepth = 3
	}
	limit := req.Limit
	if limit <= 0 || limit > 1000 {
		limit = 200
	}

	entries := []FileEntry{}
	truncated := false
	err = filepath.WalkDir(start, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if path == start {
			return nil
		}
		name := d.Name()
		if !req.IncludeHidden && strings.HasPrefix(name, ".") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !req.IncludeIgnored && d.IsDir() && isHeavyDir(name) {
			return filepath.SkipDir
		}

		rel, _ := filepath.Rel(start, path)
		depth := pathDepth(rel)
		if depth > maxDepth {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if len(entries) >= limit {
			truncated = true
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		entries = append(entries, FileEntry{
			Path:    grep.DisplayPathForRoot(root, path),
			Name:    name,
			Dir:     d.IsDir(),
			Size:    info.Size(),
			ModTime: info.ModTime().Format(time.RFC3339),
		})
		return nil
	})
	if err != nil {
		return ListFilesResult{}, err
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Dir != entries[j].Dir {
			return entries[i].Dir
		}
		return strings.ToLower(entries[i].Path) < strings.ToLower(entries[j].Path)
	})
	return ListFilesResult{Entries: entries, Count: len(entries), Truncated: truncated}, nil
}

func (a *App) workspaceMapContext(cfg ConfigState) string {
	root, err := workspaceRoot(cfg)
	if err != nil {
		return ""
	}
	key := workspaceMapCacheKey(root)

	a.workspaceMapMu.Lock()
	cached, ok := a.workspaceMapCache[key]
	if ok && time.Since(cached.generatedAt) < workspaceMapTTL {
		content := cached.content
		a.workspaceMapMu.Unlock()
		return content
	}
	a.workspaceMapMu.Unlock()

	content := buildWorkspaceMapContext(root)

	a.workspaceMapMu.Lock()
	a.workspaceMapCache[key] = workspaceMapCacheEntry{content: content, generatedAt: time.Now()}
	a.workspaceMapMu.Unlock()
	return content
}

func (a *App) invalidateWorkspaceMapCache(cfg ConfigState) {
	root, err := workspaceRoot(cfg)
	if err != nil {
		return
	}
	key := workspaceMapCacheKey(root)
	a.workspaceMapMu.Lock()
	delete(a.workspaceMapCache, key)
	a.workspaceMapMu.Unlock()
}

func workspaceMapCacheKey(root string) string {
	key := filepath.Clean(root)
	if goruntime.GOOS == "windows" {
		key = strings.ToLower(key)
	}
	return key
}

type workspaceMapEntry struct {
	Path string
	Dir  bool
}

type workspaceMapBuildResult struct {
	Entries        []workspaceMapEntry
	Truncated      bool
	SkippedDepth   int
	SkippedIgnored int
	SkippedHeavy   int
	SkippedLimit   int
}

type workspacePathIndex struct {
	Entries       []workspacePathIndexedEntry
	GeneratedAt   time.Time
	BuildDuration time.Duration
	Version       int64
	Truncated     bool
	Source        string
}

type workspacePathIndexedEntry struct {
	Path      string
	Name      string
	LowerPath string
	LowerName string
	Dir       bool
}

type workspacePathCandidate struct {
	Entry workspacePathIndexedEntry
	Score int
	Pos   int
}

type workspacePathIndexBuilder struct {
	entries   []workspacePathIndexedEntry
	seen      map[string]struct{}
	truncated bool
}

func (a *App) searchWorkspacePaths(cfg ConfigState, req WorkspacePathSearchRequest) (WorkspacePathSearchResult, error) {
	root, err := workspaceRoot(cfg)
	if err != nil {
		return WorkspacePathSearchResult{}, err
	}
	limit := req.Limit
	if limit <= 0 {
		limit = workspacePathSearchDefaultLimit
	}
	if limit > workspacePathSearchMaxLimit {
		limit = workspacePathSearchMaxLimit
	}
	index, err := a.workspacePathIndex(root, req.Force)
	if err != nil {
		return WorkspacePathSearchResult{}, err
	}
	query := strings.TrimSpace(strings.TrimPrefix(req.Query, "@"))
	query = strings.Trim(query, "\"'")
	lowerQuery := strings.ToLower(filepath.ToSlash(query))
	if lowerQuery == "" {
		count := len(index.Entries)
		if count > limit {
			count = limit
		}
		entries := make([]WorkspacePathEntry, 0, count)
		for _, entry := range index.Entries[:count] {
			entries = append(entries, WorkspacePathEntry{Path: entry.Path, Name: entry.Name, Dir: entry.Dir})
		}
		return WorkspacePathSearchResult{
			Entries:       entries,
			Count:         len(index.Entries),
			Total:         len(index.Entries),
			Truncated:     index.Truncated || len(index.Entries) > len(entries),
			IndexVersion:  index.Version,
			IndexedAt:     index.GeneratedAt.Format(time.RFC3339),
			BuildDuration: index.BuildDuration.Milliseconds(),
			Source:        index.Source,
		}, nil
	}

	candidateCap := min(max(limit*8, 64), 512)
	candidates := make([]workspacePathCandidate, 0, min(candidateCap, len(index.Entries)))
	count := 0
	for pos, entry := range index.Entries {
		score, ok := workspacePathMatchScore(entry, lowerQuery)
		if !ok {
			continue
		}
		count++
		candidate := workspacePathCandidate{Entry: entry, Score: score, Pos: pos}
		if len(candidates) < candidateCap {
			candidates = append(candidates, candidate)
			continue
		}
		worst := 0
		for i := 1; i < len(candidates); i++ {
			if workspacePathCandidateLess(candidates[worst], candidates[i]) {
				worst = i
			}
		}
		if workspacePathCandidateLess(candidate, candidates[worst]) {
			candidates[worst] = candidate
		}
	}
	sort.Slice(candidates, func(i, j int) bool { return workspacePathCandidateLess(candidates[i], candidates[j]) })

	if len(candidates) > limit {
		candidates = candidates[:limit]
	}
	entries := make([]WorkspacePathEntry, 0, len(candidates))
	for _, candidate := range candidates {
		entry := candidate.Entry
		entries = append(entries, WorkspacePathEntry{Path: entry.Path, Name: entry.Name, Dir: entry.Dir})
	}
	return WorkspacePathSearchResult{
		Entries:       entries,
		Count:         count,
		Total:         len(index.Entries),
		Truncated:     index.Truncated || count > len(entries),
		IndexVersion:  index.Version,
		IndexedAt:     index.GeneratedAt.Format(time.RFC3339),
		BuildDuration: index.BuildDuration.Milliseconds(),
		Source:        index.Source,
	}, nil
}

func workspacePathCandidateLess(a, b workspacePathCandidate) bool {
	if a.Score != b.Score {
		return a.Score < b.Score
	}
	if a.Entry.Dir != b.Entry.Dir {
		return a.Entry.Dir
	}
	if len(a.Entry.Path) != len(b.Entry.Path) {
		return len(a.Entry.Path) < len(b.Entry.Path)
	}
	return a.Pos < b.Pos
}

func workspacePathMatchScore(entry workspacePathIndexedEntry, query string) (int, bool) {
	if query == "" {
		if entry.Dir {
			return 10, true
		}
		return 20, true
	}
	if strings.HasPrefix(entry.LowerName, query) {
		return 0, true
	}
	if strings.HasPrefix(entry.LowerPath, query) {
		return 1, true
	}
	if strings.Contains(entry.LowerPath, "/"+query) {
		return 2, true
	}
	for _, part := range strings.Split(entry.LowerPath, "/") {
		if strings.HasPrefix(part, query) {
			return 3, true
		}
	}
	return 0, false
}

func (a *App) workspacePathIndex(root string, force bool) (*workspacePathIndex, error) {
	key := workspaceMapCacheKey(root)
	for {
		a.workspacePathMu.Lock()
		cached := a.workspacePathCache[key]
		if !force && cached != nil {
			if time.Since(cached.GeneratedAt) >= workspacePathIndexRefreshTTL(cached) && !isBroadWorkspacePathRoot(root) {
				if _, ok := a.workspacePathBuilds[key]; !ok {
					waitCh := make(chan struct{})
					a.workspacePathBuilds[key] = waitCh
					go a.rebuildWorkspacePathIndex(root, key, waitCh)
				}
			}
			a.workspacePathMu.Unlock()
			return cached, nil
		}
		if waitCh, ok := a.workspacePathBuilds[key]; ok {
			a.workspacePathMu.Unlock()
			<-waitCh
			force = false
			continue
		}
		waitCh := make(chan struct{})
		a.workspacePathBuilds[key] = waitCh
		a.workspacePathMu.Unlock()

		index, err := a.buildWorkspacePathIndex(root)

		a.workspacePathMu.Lock()
		a.finishWorkspacePathIndexBuildLocked(key, index, err)
		close(waitCh)
		a.workspacePathMu.Unlock()
		return index, err
	}
}

func (a *App) rebuildWorkspacePathIndex(root, key string, waitCh chan struct{}) {
	index, err := a.buildWorkspacePathIndex(root)
	a.workspacePathMu.Lock()
	a.finishWorkspacePathIndexBuildLocked(key, index, err)
	close(waitCh)
	a.workspacePathMu.Unlock()
}

func (a *App) finishWorkspacePathIndexBuildLocked(key string, index *workspacePathIndex, err error) {
	delete(a.workspacePathBuilds, key)
	if err == nil && index != nil {
		a.workspacePathVersion++
		index.Version = a.workspacePathVersion
		a.workspacePathCache[key] = index
	}
}

func workspacePathIndexRefreshTTL(index *workspacePathIndex) time.Duration {
	if index != nil && index.Truncated {
		return workspacePathTruncatedRefreshTTL
	}
	return workspacePathIndexTTL
}

func isBroadWorkspacePathRoot(root string) bool {
	clean := filepath.Clean(strings.TrimSpace(root))
	if clean == "." || clean == string(filepath.Separator) || filepath.Dir(clean) == clean {
		return true
	}
	if home, err := os.UserHomeDir(); err == nil && samePath(clean, home) {
		return true
	}
	vol := filepath.VolumeName(clean)
	rest := strings.Trim(strings.TrimPrefix(clean, vol), `\/`)
	if rest == "" {
		return true
	}
	parts := strings.FieldsFunc(rest, func(r rune) bool { return r == '/' || r == '\\' })
	return goruntime.GOOS == "windows" && len(parts) <= 1
}

func (a *App) buildWorkspacePathIndex(root string) (*workspacePathIndex, error) {
	started := time.Now()
	baseCtx := a.ctx
	if baseCtx == nil {
		baseCtx = context.Background()
	}
	ctx, cancel := context.WithTimeout(baseCtx, workspacePathIndexBuildTimeout)
	defer cancel()
	builder := newWorkspacePathIndexBuilder()
	source, truncated, err := workspacePathFilesWithRipgrep(ctx, root, builder.addFile)
	if err != nil && ctx.Err() == nil {
		builder = newWorkspacePathIndexBuilder()
		source, truncated, err = workspacePathFilesWithWalkDir(ctx, root, builder.addFile)
	}
	if err != nil && len(builder.entries) == 0 {
		return nil, err
	}
	if err != nil {
		truncated = true
	}
	if builder.truncated {
		truncated = true
	}
	if source == "" {
		source = "partial"
	}
	entries := builder.entries
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Dir != entries[j].Dir {
			return entries[i].Dir
		}
		return entries[i].LowerPath < entries[j].LowerPath
	})
	return &workspacePathIndex{Entries: entries, GeneratedAt: time.Now(), BuildDuration: time.Since(started), Truncated: truncated, Source: source}, nil
}

func newWorkspacePathIndexBuilder() *workspacePathIndexBuilder {
	return &workspacePathIndexBuilder{
		entries: make([]workspacePathIndexedEntry, 0, 1024),
		seen:    make(map[string]struct{}, 1024),
	}
}

func (b *workspacePathIndexBuilder) addFile(rel string) bool {
	rel = strings.Trim(strings.TrimSpace(filepath.ToSlash(rel)), "/")
	if rel == "" || rel == "." {
		return true
	}
	if !b.add(rel, false) {
		return false
	}
	parts := strings.Split(rel, "/")
	if len(parts) <= 1 {
		return true
	}
	cur := ""
	for i := 0; i < len(parts)-1; i++ {
		if cur == "" {
			cur = parts[i]
		} else {
			cur += "/" + parts[i]
		}
		if !b.add(cur, true) {
			return false
		}
	}
	return true
}

func (b *workspacePathIndexBuilder) add(rel string, dir bool) bool {
	rel = strings.Trim(strings.TrimSpace(filepath.ToSlash(rel)), "/")
	if rel == "" || rel == "." {
		return true
	}
	key := rel
	if goruntime.GOOS == "windows" {
		key = strings.ToLower(key)
	}
	if _, ok := b.seen[key]; ok {
		return true
	}
	if len(b.entries) >= workspacePathIndexMaxEntries {
		b.truncated = true
		return false
	}
	b.seen[key] = struct{}{}
	name := path.Base(rel)
	b.entries = append(b.entries, workspacePathIndexedEntry{
		Path:      rel,
		Name:      name,
		LowerPath: strings.ToLower(rel),
		LowerName: strings.ToLower(name),
		Dir:       dir,
	})
	return true
}

func workspacePathFilesWithRipgrep(ctx context.Context, root string, add func(string) bool) (string, bool, error) {
	rgPath, err := grep.Find()
	if err != nil {
		return "", false, err
	}
	runCtx, stop := context.WithCancel(ctx)
	defer stop()
	args := []string{"--files", "--hidden", "-g", "!.git", "-g", "!.git/**"}
	for _, dir := range workspacePathIndexIgnoredDirs() {
		args = append(args, "-g", "!"+dir+"/**", "-g", "!**/"+dir+"/**")
	}
	cmd := exec.CommandContext(runCtx, rgPath, args...)
	cmd.Dir = root
	hideCommandWindow(cmd)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", false, err
	}
	stderr := &limitedBuffer{limit: 8 * 1024}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return "", false, err
	}
	truncated := false
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		p := strings.TrimSpace(scanner.Text())
		if p == "" {
			continue
		}
		if !add(p) {
			truncated = true
			stop()
			break
		}
	}
	scanErr := scanner.Err()
	waitErr := cmd.Wait()
	if ctx.Err() != nil {
		return "rg", true, ctx.Err()
	}
	if scanErr != nil && !truncated {
		return "", truncated, scanErr
	}
	if waitErr != nil && !truncated {
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return "", truncated, fmt.Errorf("rg --files failed: %s", msg)
		}
		return "", truncated, waitErr
	}
	return "rg", truncated, nil
}

func workspacePathFilesWithWalkDir(ctx context.Context, root string, add func(string) bool) (string, bool, error) {
	truncated := false
	err := filepath.WalkDir(root, func(absPath string, d fs.DirEntry, walkErr error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if walkErr != nil || absPath == root {
			return nil
		}
		name := d.Name()
		if strings.HasPrefix(name, ".") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			if isWorkspaceMapHeavyDir(name) {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(root, absPath)
		if err == nil && !add(rel) {
			truncated = true
			return fs.SkipAll
		}
		return nil
	})
	return "walkdir", truncated, err
}

func workspacePathIndexIgnoredDirs() []string {
	return []string{".git", "node_modules", "dist", "build", "target", ".next", ".nuxt", ".svelte-kit", "vendor", "__pycache__"}
}

func buildWorkspaceMapContext(root string) string {
	result := buildWorkspaceMap(root, workspaceMapDepth, workspaceMapLimit)
	if len(result.Entries) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("# Workspace Map\n\n")
	b.WriteString("This is a bounded hidden workspace map. It contains paths only, not file contents.\n")
	b.WriteString("Root: " + filepath.ToSlash(root) + "\n")
	b.WriteString(fmt.Sprintf("Limits: depth=%d entries=%d truncated=%t\n", workspaceMapDepth, workspaceMapLimit, result.Truncated))

	if stack := detectWorkspaceStack(root); len(stack) > 0 {
		b.WriteString("Detected stack: " + strings.Join(stack, ", ") + "\n")
	}
	if keyFiles := detectWorkspaceKeyFiles(root); len(keyFiles) > 0 {
		b.WriteString("Key files: " + strings.Join(keyFiles, ", ") + "\n")
	}
	if result.SkippedIgnored > 0 || result.SkippedHeavy > 0 || result.SkippedDepth > 0 || result.SkippedLimit > 0 {
		b.WriteString(fmt.Sprintf("Skipped: ignored=%d heavy=%d depth=%d limit=%d\n", result.SkippedIgnored, result.SkippedHeavy, result.SkippedDepth, result.SkippedLimit))
	}

	b.WriteString("\nTree:\n")
	b.WriteString(".\n")
	for _, entry := range result.Entries {
		depth := strings.Count(entry.Path, "/")
		name := path.Base(entry.Path)
		if entry.Dir {
			name += "/"
		}
		b.WriteString(strings.Repeat("  ", depth))
		b.WriteString("- ")
		b.WriteString(name)
		b.WriteString("\n")
	}
	b.WriteString("\nUse read for file contents only when needed.\n")
	return b.String()
}

func buildWorkspaceMap(root string, maxDepth, limit int) workspaceMapBuildResult {
	if maxDepth <= 0 {
		maxDepth = workspaceMapDepth
	}
	if limit <= 0 {
		limit = workspaceMapLimit
	}
	rules := loadRootGitignoreRules(root)
	result := workspaceMapBuildResult{Entries: make([]workspaceMapEntry, 0, min(limit, 64))}

	_ = filepath.WalkDir(root, func(absPath string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if samePath(absPath, root) {
			return nil
		}

		name := d.Name()
		rel, err := filepath.Rel(root, absPath)
		if err != nil {
			return nil
		}
		relSlash := filepath.ToSlash(rel)

		if d.IsDir() && isWorkspaceMapHeavyDir(name) {
			result.SkippedHeavy++
			return filepath.SkipDir
		}
		if isWorkspaceMapSensitiveFile(name, d.IsDir()) {
			result.SkippedIgnored++
			return nil
		}
		if matchGitignoreRules(rules, relSlash, d.IsDir()) {
			result.SkippedIgnored++
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		depth := pathDepth(rel)
		if depth > maxDepth {
			result.SkippedDepth++
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		if len(result.Entries) >= limit {
			result.Truncated = true
			result.SkippedLimit++
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		result.Entries = append(result.Entries, workspaceMapEntry{Path: relSlash, Dir: d.IsDir()})
		return nil
	})

	sort.Slice(result.Entries, func(i, j int) bool {
		return strings.ToLower(result.Entries[i].Path) < strings.ToLower(result.Entries[j].Path)
	})
	return result
}

func detectWorkspaceStack(root string) []string {
	type marker struct {
		label string
		paths []string
	}
	markers := []marker{
		{"Go", []string{"go.mod"}},
		{"Wails", []string{"wails.json"}},
		{"Node", []string{"package.json", "frontend/package.json"}},
		{"Vue", []string{"frontend/src/App.vue", "src/App.vue"}},
		{"Vite", []string{"vite.config.js", "vite.config.ts", "frontend/vite.config.js", "frontend/vite.config.ts"}},
		{"TypeScript", []string{"tsconfig.json", "frontend/tsconfig.json"}},
		{"Python", []string{"pyproject.toml", "requirements.txt"}},
		{"Rust", []string{"Cargo.toml"}},
		{"Docker", []string{"Dockerfile", "docker-compose.yml", "compose.yml"}},
	}

	var stack []string
	for _, marker := range markers {
		for _, rel := range marker.paths {
			if fileOrDirExists(filepath.Join(root, filepath.FromSlash(rel))) {
				stack = append(stack, marker.label)
				break
			}
		}
	}
	return stack
}

func detectWorkspaceKeyFiles(root string) []string {
	candidates := []string{
		"AGENTS.md", "CLAUDE.md", "README.md",
		"go.mod", "go.sum", "wails.json",
		"package.json", "frontend/package.json",
		"vite.config.js", "vite.config.ts", "frontend/vite.config.js", "frontend/vite.config.ts",
		"tsconfig.json", "frontend/tsconfig.json",
		"pyproject.toml", "Cargo.toml", "Dockerfile",
		".gitignore",
	}

	var files []string
	for _, rel := range candidates {
		if fileOrDirExists(filepath.Join(root, filepath.FromSlash(rel))) {
			files = append(files, rel)
		}
	}
	return files
}

func fileOrDirExists(absPath string) bool {
	_, err := os.Stat(absPath)
	return err == nil
}

func (a *App) readFileWithConfig(cfg ConfigState, req ReadFileRequest) (ReadFileResult, error) {
	if shouldExtractDocumentInRead(req.Path) {
		return a.readDocumentAsReadFileWithConfig(cfg, req)
	}
	path, err := resolveReadPath(cfg, req.Path)
	if err != nil {
		return ReadFileResult{}, err
	}
	data, info, err := readTextFile(path)
	if err != nil {
		return ReadFileResult{}, err
	}
	text, ending := normalizeText(data)
	preview, err := formatLineNumberReadPreviewRangeWithBudget(text, readRangeRequest{
		StartLine:     req.StartLine,
		EndLine:       req.EndLine,
		LineCount:     req.LineCount,
		ContextBefore: req.ContextBefore,
		ContextAfter:  req.ContextAfter,
	}, maxToolOutput)
	if err != nil {
		return ReadFileResult{}, err
	}
	return ReadFileResult{
		Path:          displayPathForConfig(cfg, path),
		Content:       preview.Content,
		RawContent:    preview.RawContent,
		Kind:          "text",
		ContentFormat: "line_numbers",
		Editable:      true,
		StartLine:     preview.StartLine,
		EndLine:       preview.EndLine,
		NextStartLine: preview.NextStartLine,
		TotalLines:    preview.TotalLines,
		SHA256:        hashBytes(data),
		Version:       hashVersion(data),
		Size:          info.Size(),
		LineEnding:    ending,
		Truncated:     preview.Truncated,
		RangeStatus:   preview.RangeStatus,
		EmptyRange:    preview.EmptyRange,
	}, nil
}

func shouldExtractDocumentInRead(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".docx", ".pptx", ".xlsx", ".pdf":
		return true
	default:
		return false
	}
}

func (a *App) readDocumentAsReadFileWithConfig(cfg ConfigState, req ReadFileRequest) (ReadFileResult, error) {
	doc, err := a.readDocumentWithConfig(cfg, DocumentReadRequest{
		Path:     req.Path,
		Sheet:    req.Sheet,
		MaxChars: req.MaxChars,
	})
	if err != nil {
		return ReadFileResult{}, err
	}
	fullPath, err := resolveReadPath(cfg, req.Path)
	if err != nil {
		return ReadFileResult{}, err
	}
	info, err := os.Stat(fullPath)
	if err != nil {
		return ReadFileResult{}, err
	}
	sha, err := hashFileSHA256(fullPath)
	if err != nil {
		return ReadFileResult{}, err
	}
	version, err := versionFromSHA256Hex(sha)
	if err != nil {
		return ReadFileResult{}, err
	}
	totalLines := countPlainTextLines(doc.Text)
	return ReadFileResult{
		Path:          doc.Path,
		Content:       doc.Text,
		RawContent:    doc.Text,
		Text:          doc.Text,
		Kind:          "document",
		ContentFormat: "plain",
		Type:          doc.Type,
		Editable:      false,
		StartLine:     1,
		EndLine:       totalLines,
		TotalLines:    totalLines,
		SHA256:        sha,
		Version:       version,
		Size:          info.Size(),
		Truncated:     doc.Truncated,
		RangeStatus:   "document",
		Sheets:        doc.Sheets,
	}, nil
}

func countPlainTextLines(text string) int {
	if text == "" {
		return 0
	}
	lines := strings.Count(text, "\n") + 1
	if strings.HasSuffix(text, "\n") {
		lines--
	}
	return lines
}

func formatLineNumberReadPreviewRangeWithBudget(content string, req readRangeRequest, budgetBytes int) (readPreviewResult, error) {
	if req.LineCount > 0 && req.EndLine > 0 {
		return readPreviewResult{}, errors.New("lineCount and endLine are mutually exclusive")
	}
	if req.ContextBefore < 0 || req.ContextAfter < 0 {
		return readPreviewResult{}, errors.New("contextBefore/contextAfter must be non-negative")
	}
	if len(content) == 0 {
		return readPreviewResult{
			Content:     "File is empty. Use create_file with overwrite=true to write content.",
			TotalLines:  0,
			StartLine:   1,
			EndLine:     0,
			RangeStatus: "empty_file",
			EmptyRange:  true,
		}, nil
	}

	allLines, trailingNewline := splitLines(content)
	total := len(allLines)

	startLine := req.StartLine
	if startLine <= 0 {
		startLine = 1
	}
	if startLine > total {
		return readPreviewResult{
			Content:     fmt.Sprintf("startLine %d is beyond end of file (%d lines total).", startLine, total),
			TotalLines:  total,
			StartLine:   startLine,
			EndLine:     0,
			RangeStatus: "beyond_eof",
			EmptyRange:  true,
		}, nil
	}

	baseEnd := total
	if req.EndLine > 0 {
		if req.EndLine < startLine {
			return readPreviewResult{}, fmt.Errorf("endLine %d is before startLine %d", req.EndLine, startLine)
		}
		baseEnd = req.EndLine
	} else if req.LineCount > 0 {
		baseEnd = startLine + req.LineCount - 1
	} else if req.ContextBefore > 0 || req.ContextAfter > 0 {
		baseEnd = startLine
	}

	start := startLine - req.ContextBefore
	if start < 1 {
		start = 1
	}
	end := baseEnd + req.ContextAfter
	if end > total {
		end = total
	}
	if end < start {
		return readPreviewResult{
			Content:     fmt.Sprintf("Requested range %d-%d is empty.", start, end),
			TotalLines:  total,
			StartLine:   start,
			EndLine:     0,
			RangeStatus: "empty_range",
			EmptyRange:  true,
		}, nil
	}

	rangeLimited := false
	if end-start+1 > maxReadRangeLines {
		end = start + maxReadRangeLines - 1
		rangeLimited = true
	}

	width := len(strconv.Itoa(end))
	var b strings.Builder
	actualEnd := start - 1
	budgetLimited := false
	for lineNum := start; lineNum <= end; lineNum++ {
		lineText := formatNumberedLine(lineNum, allLines[lineNum-1], width)
		if b.Len() > 0 {
			lineText = "\n" + lineText
		}
		if budgetBytes > 0 && b.Len()+len(lineText) > budgetBytes {
			budgetLimited = true
			break
		}
		b.WriteString(lineText)
		actualEnd = lineNum
	}
	result := b.String()
	rawContent := ""
	partialFirstLine := false
	if result == "" && budgetBytes > 0 {
		lineText := formatNumberedLine(start, allLines[start-1], width)
		if len(lineText) > budgetBytes {
			cut := budgetBytes
			for cut > 0 && !utf8.ValidString(lineText[:cut]) {
				cut--
			}
			lineText = lineText[:cut]
		}
		result = lineText
		actualEnd = start
		budgetLimited = true
		rawLine := allLines[start-1]
		cut := budgetBytes
		if cut > len(rawLine) {
			cut = len(rawLine)
		}
		for cut > 0 && !utf8.ValidString(rawLine[:cut]) {
			cut--
		}
		rawContent = rawLine[:cut]
		partialFirstLine = cut < len(rawLine)
	}
	if !partialFirstLine && actualEnd >= start {
		rawContent = strings.Join(allLines[start-1:actualEnd], "\n")
		if actualEnd < total || (actualEnd == total && trailingNewline) {
			rawContent += "\n"
		}
	}

	nextStartLine := 0
	requestedFullFile := req.EndLine == 0 && req.LineCount == 0 && req.ContextBefore == 0 && req.ContextAfter == 0
	pagedRequest := req.LineCount > 0
	if actualEnd < total && (budgetLimited || rangeLimited || pagedRequest || requestedFullFile) {
		nextStartLine = actualEnd + 1
		result += fmt.Sprintf("\n\n[Showing lines %d-%d of %d. Use startLine=%d to continue.]", start, actualEnd, total, nextStartLine)
	}

	status := "ok"
	if budgetLimited || rangeLimited {
		status = "truncated"
	}
	return readPreviewResult{
		Content:       result,
		RawContent:    rawContent,
		TotalLines:    total,
		StartLine:     start,
		EndLine:       actualEnd,
		NextStartLine: nextStartLine,
		Truncated:     nextStartLine > 0 || budgetLimited || rangeLimited,
		RangeStatus:   status,
	}, nil
}

func formatNumberedLine(lineNum int, line string, width int) string {
	return strconv.Itoa(lineNum) + ": " + line
}

func (a *App) createFileWithConfig(cfg ConfigState, req CreateFileRequest) (EditResult, error) {
	if strings.TrimSpace(req.Path) == "" {
		return EditResult{}, codedToolError("E_BAD_PATH", errors.New("create_file requires a non-empty path"))
	}
	roots, err := workspaceRoots(cfg)
	if err != nil {
		return EditResult{}, err
	}
	path, err := resolveWritableFilePath(roots, req.Path)
	if err != nil {
		return EditResult{}, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return EditResult{}, err
	}
	path, err = resolveWritableFilePath(roots, req.Path)
	if err != nil {
		return EditResult{}, err
	}

	before := []byte{}
	beforeHash := ""
	perm := os.FileMode(0o644)
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return EditResult{}, codedToolError("E_SYMLINK_PATH", fmt.Errorf("refusing to overwrite symlink target: %s", req.Path))
		}
		if info.IsDir() {
			return EditResult{}, codedToolError("E_TARGET_IS_DIRECTORY", fmt.Errorf("path is a directory: %s", req.Path))
		}
		if !req.Overwrite {
			return EditResult{}, codedToolError("E_EXISTS", fmt.Errorf("file already exists: %s", req.Path))
		}
		before, _, err = readTextFile(path)
		if err != nil {
			return EditResult{}, codedToolError("E_TEXT_OVERWRITE", fmt.Errorf("refusing to overwrite non-text or unreadable file %s: %w", req.Path, err))
		}
		beforeHash = hashBytes(before)
		perm = info.Mode().Perm()
	} else if !errors.Is(err, os.ErrNotExist) {
		return EditResult{}, err
	}

	content, ending := normalizeText([]byte(req.Content))
	encoded := encodeLineEnding(content, ending)
	if req.Overwrite {
		if err := safeWriteFileWithDir(path, encoded, perm, false); err != nil {
			return EditResult{}, err
		}
	} else {
		if err := safeWriteNewFile(path, encoded, perm); err != nil {
			return EditResult{}, err
		}
	}
	after, _, err := readTextFile(path)
	if err != nil {
		return EditResult{}, err
	}
	return makeEditResult(req.Path, beforeHash, before, after, ending, 1, string(before), content), nil
}

func (a *App) deletePathWithConfig(cfg ConfigState, req DeletePathRequest) (DeleteResult, error) {
	if strings.TrimSpace(req.Path) == "" {
		return DeleteResult{}, codedToolError("E_BAD_PATH", errors.New("delete_path requires a non-empty path"))
	}
	roots, err := workspaceRoots(cfg)
	if err != nil {
		return DeleteResult{}, err
	}
	path, err := resolveDeletablePath(roots, req.Path)
	if err != nil {
		return DeleteResult{}, err
	}
	for _, root := range roots {
		if samePath(path, root) {
			return DeleteResult{}, codedToolError("E_DELETE_BLOCKED", errors.New("refusing to delete workspace root"))
		}
	}

	// Safety: block dangerous delete targets
	if blocked, reason := isDangerousDeletePath(path); blocked {
		return DeleteResult{}, codedToolError("E_DELETE_BLOCKED", fmt.Errorf("%s\n\nThis operation has been blocked for safety. If you really need to delete this path, do it manually outside the agent.", reason))
	}

	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return DeleteResult{}, codedToolError("E_PATH_NOT_FOUND", err)
		}
		return DeleteResult{}, err
	}
	if info.IsDir() && !req.Recursive {
		return DeleteResult{}, codedToolError("E_DIR_REQUIRES_RECURSIVE", errors.New("path is a directory; set recursive=true"))
	}
	result, err := inspectDeleteTarget(req.Path, path, req.Recursive, info)
	if err != nil {
		return DeleteResult{}, err
	}
	if req.Recursive && info.IsDir() {
		err = os.RemoveAll(path)
	} else {
		err = os.Remove(path)
	}
	if err != nil {
		return DeleteResult{}, err
	}
	return result, nil
}

func (a *App) runCommandWithConfig(parent context.Context, cfg ConfigState, req CommandRequest) (CommandResult, error) {
	if strings.TrimSpace(req.Command) == "" {
		return CommandResult{}, codedToolError("E_BAD_COMMAND", errors.New("command is required"))
	}
	if looksLikeLongRunningService(req.Command) {
		return CommandResult{}, longRunningCommandError(req.Command)
	}
	roots, err := workspaceRoots(cfg)
	if err != nil {
		return CommandResult{}, err
	}
	root := roots[0]
	if err := checkCommandSafety(req, roots); err != nil {
		return CommandResult{}, err
	}
	cwd := root
	if strings.TrimSpace(req.Cwd) != "" {
		cwd, err = resolveCommandCwd(roots, req.Cwd)
		if err != nil {
			return CommandResult{}, err
		}
	}
	timeout := req.TimeoutSeconds
	if timeout <= 0 {
		timeout = defaultShellLimit
	}
	if timeout > 600 {
		timeout = 600
	}
	ctx, cancel := context.WithTimeout(parent, time.Duration(timeout)*time.Second)
	defer cancel()

	shell := commandShell(req.Command, cfg.GitBashPath)
	cmd := exec.CommandContext(ctx, shell.path, shell.args...)
	cmd.Dir = cwd
	cmd.Env = proxyEnvironment(cfg, os.Environ())
	buf := &limitedBuffer{limit: maxToolOutput}
	cmd.Stdout = buf
	cmd.Stderr = buf
	prepareServiceCommand(cmd)
	// ESC 取消运行时，杀掉整棵进程树而不是只杀外壳 bash/powershell，
	// 否则 npm/vite/devserver 等子进程会变成孤儿继续占用端口。
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return stopProcessTree(cmd.Process.Pid)
	}
	started := time.Now()
	outputDone := make(chan struct{})
	var outputWG sync.WaitGroup
	if meta, ok := parent.Value(toolExecutionMetaContextKey{}).(toolExecutionMeta); ok && meta.runID != "" && meta.sessionID != "" {
		outputWG.Add(1)
		go func() {
			defer outputWG.Done()
			ticker := time.NewTicker(120 * time.Millisecond)
			defer ticker.Stop()
			lastOutput := ""
			emit := func() {
				output := buf.String()
				if output == "" || output == lastOutput {
					return
				}
				lastOutput = output
				a.emit("tool:update", map[string]any{
					"runId":         meta.runID,
					"sessionId":     meta.sessionID,
					"toolBatchId":   meta.toolBatchID,
					"toolCallIndex": meta.toolCallIndex,
					"toolCallId":    meta.toolCallID,
					"name":          meta.toolName,
					"args":          meta.toolArgs,
					"output":        output,
					"streaming":     true,
				})
			}
			for {
				select {
				case <-ticker.C:
					emit()
				case <-outputDone:
					emit()
					return
				case <-parent.Done():
					return
				}
			}
		}()
	}
	err = cmd.Run()
	close(outputDone)
	outputWG.Wait()
	duration := time.Since(started).Milliseconds()
	result := CommandResult{
		Command:    req.Command,
		Cwd:        filepath.ToSlash(cwd),
		Shell:      shell.name,
		ShellPath:  shell.path,
		Output:     buf.String(),
		ExitCode:   0,
		TimedOut:   errors.Is(ctx.Err(), context.DeadlineExceeded),
		Cancelled:  errors.Is(ctx.Err(), context.Canceled),
		DurationMS: duration,
		Truncated:  buf.truncated,
	}
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			result.ExitCode = exitErr.ExitCode()
			return result, nil
		}
		if result.TimedOut {
			result.ExitCode = -1
			return result, nil
		}
		return result, err
	}
	return result, nil
}

type shellInvocation struct {
	name string
	path string
	args []string
}

// commandShell determines which shell to use for executing a command.
//
// On Windows it prefers Git Bash (bash.exe) so command syntax is unified with
// Linux/macOS. Detection order:
//  1. The gitBashPath setting (manual user override, passed as configuredPath)
//  2. Git for Windows common installation paths
//  3. A Git for Windows installation found through git.exe on PATH
//  4. A Git Bash executable found on PATH
//  5. Fallback to PowerShell (pwsh.exe → powershell.exe), which is always
//     available on Windows (5.1 is built-in, no installation required).
//
// On Linux/macOS it uses bash -c directly and ignores configuredPath.
func commandShell(command, configuredPath string) shellInvocation {
	if goruntime.GOOS == "windows" {
		if bashPath, bashName := findWindowsBash(configuredPath); bashPath != "" {
			return shellInvocation{name: bashName, path: bashPath, args: []string{"-c", command}}
		}
		shell := windowsPowerShell()
		return shellInvocation{
			name: shell.name,
			path: shell.path,
			args: []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", wrapPowerShellCommand(command)},
		}
	}
	return shellInvocation{name: "bash", path: "bash", args: []string{"-c", command}}
}

// shellBinary holds a resolved shell name and executable path.
type shellBinary struct {
	name string
	path string
}

// findWindowsBash searches for a usable bash.exe on Windows.
// configuredPath is an explicit user override from the gitBashPath setting;
// when set and valid it takes priority. Returns the path and a display name
// ("bash"), or empty strings if no bash was found.
func findWindowsBash(configuredPath string) (string, string) {
	// 1. User-configured path (manual override).
	if p := existingGitBashPath(configuredPath); p != "" {
		return p, "bash"
	}

	// 2. Git for Windows common installation paths.
	gitBashPaths := []string{}
	if progFiles := os.Getenv("ProgramFiles"); progFiles != "" {
		gitBashPaths = append(gitBashPaths,
			filepath.Join(progFiles, "Git", "bin", "bash.exe"),
			filepath.Join(progFiles, "Git", "usr", "bin", "bash.exe"),
		)
	}
	if progFilesX86 := os.Getenv("ProgramFiles(x86)"); progFilesX86 != "" {
		gitBashPaths = append(gitBashPaths,
			filepath.Join(progFilesX86, "Git", "bin", "bash.exe"),
			filepath.Join(progFilesX86, "Git", "usr", "bin", "bash.exe"),
		)
	}
	if localAppData := os.Getenv("LOCALAPPDATA"); localAppData != "" {
		gitBashPaths = append(gitBashPaths,
			filepath.Join(localAppData, "Programs", "Git", "bin", "bash.exe"),
			filepath.Join(localAppData, "Programs", "Git", "usr", "bin", "bash.exe"),
		)
	}
	for _, p := range gitBashPaths {
		if p = existingGitBashPath(p); p != "" {
			return p, "bash"
		}
	}

	// 3. Derive Git Bash from git.exe on PATH. This supports portable and
	// non-default Git for Windows installations without accidentally selecting
	// C:\Windows\System32\bash.exe, which is the legacy WSL launcher.
	for _, gitName := range []string{"git.exe", "git"} {
		gitPath, err := exec.LookPath(gitName)
		if err != nil {
			continue
		}
		for _, candidate := range gitBashCandidatesFromGitExecutable(gitPath) {
			if p := existingGitBashPath(candidate); p != "" {
				return p, "bash"
			}
		}
	}

	// 4. bash.exe on PATH, but only when it belongs to a Git for Windows
	// installation. Accepting an arbitrary bash.exe here can select WSL, whose
	// command-line forwarding and Linux PATH semantics break Windows tools and
	// can cause shell input such as $BASH_VERSION or $(...) to be parsed twice.
	if p, err := exec.LookPath("bash.exe"); err == nil {
		if p = existingGitBashPath(p); p != "" {
			return p, "bash"
		}
	}

	return "", ""
}

func gitBashCandidatesFromGitExecutable(gitPath string) []string {
	dir := filepath.Dir(filepath.Clean(strings.TrimSpace(gitPath)))
	base := strings.ToLower(filepath.Base(dir))
	var root string
	switch base {
	case "cmd", "bin":
		root = filepath.Dir(dir)
	default:
		return nil
	}
	return []string{
		filepath.Join(root, "bin", "bash.exe"),
		filepath.Join(root, "usr", "bin", "bash.exe"),
	}
}

func existingGitBashPath(candidate string) string {
	candidate = strings.TrimSpace(candidate)
	if candidate == "" {
		return ""
	}
	info, err := os.Stat(candidate)
	if err != nil || info.IsDir() {
		return ""
	}

	dir := filepath.Dir(filepath.Clean(candidate))
	if !strings.EqualFold(filepath.Base(dir), "bin") {
		return ""
	}
	root := filepath.Dir(dir)
	if strings.EqualFold(filepath.Base(root), "usr") {
		root = filepath.Dir(root)
	}
	for _, gitPath := range []string{
		filepath.Join(root, "cmd", "git.exe"),
		filepath.Join(root, "bin", "git.exe"),
	} {
		if gitInfo, statErr := os.Stat(gitPath); statErr == nil && !gitInfo.IsDir() {
			return candidate
		}
	}
	return ""
}

// windowsPowerShell resolves the best available PowerShell on Windows.
func windowsPowerShell() shellBinary {
	for _, candidate := range []string{"pwsh.exe", "pwsh", "powershell.exe", "powershell"} {
		if p, err := exec.LookPath(candidate); err == nil {
			name := strings.TrimSuffix(strings.ToLower(filepath.Base(p)), ".exe")
			return shellBinary{name: name, path: p}
		}
	}
	return shellBinary{name: "powershell", path: "powershell.exe"}
}

// windowsShellInfo returns the display name and path of the shell that
// commandShell will use on Windows. On non-Windows it returns ("bash", "bash").
func windowsShellInfo(configuredPath string) shellBinary {
	if goruntime.GOOS != "windows" {
		return shellBinary{name: "bash", path: "bash"}
	}
	if bashPath, _ := findWindowsBash(configuredPath); bashPath != "" {
		return shellBinary{name: "bash", path: bashPath}
	}
	return windowsPowerShell()
}

func wrapPowerShellCommand(command string) string {
	return "$ErrorActionPreference = 'Stop'; try { " + command + "; if ($global:LASTEXITCODE -is [int]) { exit $global:LASTEXITCODE } } catch { Write-Error $_; exit 1 }"
}

func workspaceRoot(cfg ConfigState) (string, error) {
	root := strings.TrimSpace(cfg.Workspace)
	if root == "" {
		return "", errors.New("workspace is required")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("workspace is not a directory: %s", abs)
	}
	return filepath.Clean(abs), nil
}

// workspaceRoots 返回主工作区 + 会话级 ExtraRoots 的去重列表。
// 主工作区始终是 roots[0]，且必须存在；ExtraRoots 中不存在或非目录的条目被静默跳过。
// 重复路径（按 OS 风格归一化后）只保留首次出现。
func workspaceRoots(cfg ConfigState) ([]string, error) {
	primary, err := workspaceRoot(cfg)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	roots := make([]string, 0, 1+len(cfg.ExtraRoots))
	markKey := func(clean string) string {
		if goruntime.GOOS == "windows" {
			return strings.ToLower(clean)
		}
		return clean
	}
	addRoot := func(path string) {
		abs, err := filepath.Abs(strings.TrimSpace(path))
		if err != nil {
			return
		}
		clean := filepath.Clean(abs)
		info, err := os.Stat(clean)
		if err != nil {
			return // 不存在的附加目录被跳过
		}
		if !info.IsDir() {
			return
		}
		key := markKey(clean)
		if seen[key] {
			return
		}
		seen[key] = true
		roots = append(roots, clean)
	}
	addRoot(primary)
	for _, extra := range cfg.ExtraRoots {
		if strings.TrimSpace(extra) == "" {
			continue
		}
		addRoot(extra)
	}
	return roots, nil
}

// insideAnyRoot 判断 target 是否落在任一 root 内（不含 symlink 解析）。
func insideAnyRoot(roots []string, target string) bool {
	for _, root := range roots {
		if insideRoot(root, target) {
			return true
		}
	}
	return false
}

func safeJoin(roots []string, p string) (string, error) {
	if len(roots) == 0 {
		return "", errors.New("workspace is required")
	}
	primaryAbs, err := filepath.Abs(roots[0])
	if err != nil {
		return "", err
	}
	var target string
	if strings.TrimSpace(p) == "" || p == "." {
		target = primaryAbs
	} else if filepath.IsAbs(p) {
		target = p
	} else {
		target = filepath.Join(primaryAbs, filepath.Clean(p))
	}
	abs, err := filepath.Abs(target)
	if err != nil {
		return "", err
	}
	absClean := filepath.Clean(abs)
	if !insideAnyRoot(roots, absClean) && !insideAllyAgentDir(absClean) {
		return "", fmt.Errorf("path is outside workspace or ~/.ally_agent: %s", p)
	}
	return absClean, nil
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
	if len(roots) == 0 {
		return "(无)"
	}
	parts := make([]string, 0, len(roots))
	for i, root := range roots {
		prefix := "  附加工作区"
		if i == 0 {
			prefix = "  主工作区"
		}
		parts = append(parts, prefix+" "+filepath.ToSlash(root))
	}
	return strings.Join(parts, "\n")
}

func evalExistingPrefix(target string) (string, error) {
	return read.EvalExistingPrefix(target)
}

// insideWriteRoot 判断 target（已解析）是否落在任一可写 root 内。
// 对每个 root 都做 insideRoot 检查 + EvalSymlinks 解析；任一通过即放行。
// ~/.ally_agent 始终作为兜底白名单。
func insideWriteRoot(roots []string, target string) bool {
	clean := filepath.Clean(target)
	for _, root := range roots {
		rootAbs, err := filepath.Abs(root)
		if err != nil {
			continue
		}
		rootClean := filepath.Clean(rootAbs)
		if insideRoot(rootClean, clean) {
			return true
		}
		if resolvedRoot, err := filepath.EvalSymlinks(rootClean); err == nil && insideRoot(filepath.Clean(resolvedRoot), clean) {
			return true
		}
	}
	return insideAllyAgentDir(clean)
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
	if strings.TrimSpace(p) == "" {
		return "", errors.New("path is required")
	}
	root, err := workspaceRoot(cfg)
	if err != nil {
		return "", err
	}
	if filepath.IsAbs(p) {
		abs, err := filepath.Abs(p)
		if err != nil {
			return "", err
		}
		return filepath.Clean(abs), nil
	}
	// 读取操作：相对路径仅解析到主工作区。额外根目录请用绝对路径访问。
	return safeJoin([]string{root}, p)
}

func insideRoot(root, target string) bool {
	if samePath(root, target) {
		return true
	}
	root = filepath.Clean(root)
	target = filepath.Clean(target)
	if goruntime.GOOS == "windows" {
		root = strings.ToLower(root)
		target = strings.ToLower(target)
	}
	sep := string(os.PathSeparator)
	return strings.HasPrefix(target, strings.TrimRight(root, sep)+sep)
}

func insideAllyAgentDir(target string) bool {
	dir, err := filepath.Abs(appDataDir())
	if err != nil {
		return false
	}
	return insideRoot(filepath.Clean(dir), filepath.Clean(target))
}

func samePath(a, b string) bool {
	a = filepath.Clean(a)
	b = filepath.Clean(b)
	if goruntime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
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

func inspectDeleteTarget(requestPath, absPath string, recursive bool, info os.FileInfo) (DeleteResult, error) {
	result := DeleteResult{
		Deleted:      filepath.ToSlash(requestPath),
		Path:         filepath.ToSlash(requestPath),
		ResolvedPath: filepath.ToSlash(absPath),
		Kind:         deleteTargetKind(info),
		Recursive:    recursive,
		WasSymlink:   info.Mode()&os.ModeSymlink != 0,
	}
	if info.IsDir() {
		if !recursive {
			result.RemovedDirs = 1
			return result, nil
		}
		err := filepath.WalkDir(absPath, func(_ string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			entryInfo, err := d.Info()
			if err != nil {
				return err
			}
			if d.IsDir() {
				result.RemovedDirs++
			} else {
				result.RemovedFiles++
				result.RemovedBytes += entryInfo.Size()
			}
			return nil
		})
		return result, err
	}
	result.RemovedFiles = 1
	result.RemovedBytes = info.Size()
	return result, nil
}

func deleteTargetKind(info os.FileInfo) string {
	mode := info.Mode()
	if mode&os.ModeSymlink != 0 {
		return "symlink"
	}
	if info.IsDir() {
		return "directory"
	}
	if mode.IsRegular() {
		return "file"
	}
	return "other"
}

func makeEditResult(rel string, beforeHash string, before, after []byte, ending string, replacements int, beforeText, afterText string) EditResult {
	beforeLines, _ := splitLines(beforeText)
	afterLines, _ := splitLines(afterText)
	added := len(afterLines) - len(beforeLines)
	removed := 0
	if added < 0 {
		removed = -added
		added = 0
	}
	return EditResult{
		Path:          filepath.ToSlash(rel),
		BeforeSHA256:  beforeHash,
		AfterSHA256:   hashBytes(after),
		BeforeVersion: hashVersion(before),
		Version:       hashVersion(after),
		BeforeBytes:   len(before),
		AfterBytes:    len(after),
		Replacements:  replacements,
		AddedLines:    added,
		RemovedLines:  removed,
		LineEnding:    ending,
		Summary:       fmt.Sprintf("%s updated: %d -> %d bytes", filepath.ToSlash(rel), len(before), len(after)),
	}
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

// ── Safety guards for destructive / expensive operations ──

// isDangerousDeletePath returns (blocked, reason). Blocks paths that are
// OS-protected locations, home roots, VCS metadata, or workspace root.
func isDangerousDeletePath(absPath string) (bool, string) {
	abs := filepath.Clean(absPath)
	lower := strings.ToLower(abs)

	// 1. VCS metadata — never delete .git or similar
	base := strings.ToLower(filepath.Base(abs))
	if base == ".git" || base == ".svn" || base == ".hg" {
		return true, fmt.Sprintf("refusing to delete VCS directory %q; if you need to remove version control data, do it manually", abs)
	}
	// Also block any path containing /.git/ (not just the .git dir itself)
	if strings.Contains(lower, string(filepath.Separator)+".git"+string(filepath.Separator)) ||
		strings.HasSuffix(lower, string(filepath.Separator)+".git") {
		return true, fmt.Sprintf("path %q contains .git; refusing to protect version control data", abs)
	}

	// Test and build workspaces commonly live below the OS temp directory.
	// Workspace confinement still applies before this guard is reached.
	if tmp := os.TempDir(); tmp != "" && isPathOrDescendant(abs, tmp) {
		return false, ""
	}

	if blocked, reason := isOSProtectedDeletePath(abs); blocked {
		return true, reason
	}

	// Home directory — block outright deletion of any user's home root, but
	// allow ordinary project files under a user's home directory.
	if homeDir, err := os.UserHomeDir(); err == nil {
		if abs == filepath.Clean(homeDir) {
			return true, fmt.Sprintf("refusing to delete home directory %q", abs)
		}
	}
	if allyDir, err := filepath.Abs(appDataDir()); err == nil && samePath(abs, allyDir) {
		return true, fmt.Sprintf("refusing to delete Ally data directory %q", abs)
	}
	// Also check for other users' homes (Unix: /home/*, macOS: /Users/*, Windows: C:\Users\*)
	parent := filepath.Dir(abs)
	parentLower := strings.ToLower(parent)
	if parentLower == "/home" || parentLower == "/users" || parentLower == `c:\users` {
		return true, fmt.Sprintf("refusing to delete user home directory %q", abs)
	}

	return false, ""
}

func isOSProtectedDeletePath(abs string) (bool, string) {
	switch goruntime.GOOS {
	case "windows":
		if isWindowsVolumeRoot(abs) {
			return true, fmt.Sprintf("refusing to delete drive root %q", abs)
		}
		if windowsPathIsTopLevelDir(abs, "users") {
			return true, fmt.Sprintf("refusing to delete Windows protected path %q", abs)
		}
		for _, protected := range []string{
			`windows`,
			`program files`,
			`program files (x86)`,
			`programdata`,
			`recovery`,
			`system volume information`,
			`$recycle.bin`,
		} {
			if windowsPathHasTopLevelDir(abs, protected) {
				return true, fmt.Sprintf("refusing to delete Windows protected path %q", abs)
			}
		}
	case "darwin":
		if abs == "/" {
			return true, `refusing to delete filesystem root "/"`
		}
		for _, protected := range []string{
			"/Applications",
			"/bin",
			"/cores",
			"/dev",
			"/etc",
			"/Library",
			"/Network",
			"/private",
			"/sbin",
			"/System",
			"/usr",
			"/var",
			"/Volumes",
		} {
			if isPathOrDescendant(abs, protected) {
				return true, fmt.Sprintf("refusing to delete macOS protected path %q", abs)
			}
		}
	case "linux":
		if abs == "/" {
			return true, `refusing to delete filesystem root "/"`
		}
		for _, protected := range []string{
			"/bin",
			"/boot",
			"/dev",
			"/etc",
			"/lib",
			"/lib64",
			"/opt",
			"/proc",
			"/root",
			"/run",
			"/sbin",
			"/sys",
			"/usr",
			"/var",
		} {
			if isPathOrDescendant(abs, protected) {
				return true, fmt.Sprintf("refusing to delete Linux protected path %q", abs)
			}
		}
	default:
		if abs == string(os.PathSeparator) {
			return true, fmt.Sprintf("refusing to delete filesystem root %q", abs)
		}
	}
	return false, ""
}

func isWindowsVolumeRoot(abs string) bool {
	volume := filepath.VolumeName(abs)
	if volume == "" {
		return false
	}
	rest := strings.TrimPrefix(abs, volume)
	rest = strings.Trim(rest, `\/`)
	return rest == ""
}

func windowsPathHasTopLevelDir(abs string, dir string) bool {
	parts := windowsPathParts(abs)
	return len(parts) > 0 && strings.EqualFold(parts[0], dir)
}

func windowsPathIsTopLevelDir(abs string, dir string) bool {
	parts := windowsPathParts(abs)
	return len(parts) == 1 && strings.EqualFold(parts[0], dir)
}

func windowsPathParts(abs string) []string {
	volume := filepath.VolumeName(abs)
	rest := abs
	if volume != "" {
		rest = strings.TrimPrefix(abs, volume)
	}
	rest = strings.Trim(rest, `\/`)
	if rest == "" {
		return nil
	}
	return strings.FieldsFunc(rest, func(r rune) bool {
		return r == '\\' || r == '/'
	})
}

func isPathOrDescendant(abs, protected string) bool {
	abs = filepath.Clean(abs)
	protected = filepath.Clean(protected)
	if samePath(abs, protected) {
		return true
	}
	sep := string(os.PathSeparator)
	return strings.HasPrefix(abs, strings.TrimRight(protected, sep)+sep)
}

// isDangerousSearchRoot returns (blocked, reason). Blocks grep/list operations
// that would traverse system directories, home directories, or other high-risk paths.
func isDangerousSearchRoot(absPath string) (bool, string) {
	abs := filepath.Clean(absPath)
	lower := strings.ToLower(abs)

	if insideAllyAgentDir(abs) {
		return false, ""
	}

	// 1. Root paths — too broad
	if abs == "/" || lower == `c:\` || lower == `c:` {
		return true, fmt.Sprintf("refusing to search from root %q; this would scan the entire filesystem. Specify a project subdirectory instead", abs)
	}

	// Test and temporary workspaces commonly live below /var on macOS.
	if tmp := os.TempDir(); tmp != "" && isPathOrDescendant(abs, tmp) {
		return false, ""
	}

	// 2. Unix/macOS system directories
	unixDangerous := []string{
		"/etc", "/usr", "/bin", "/sbin", "/lib", "/lib64",
		"/boot", "/dev", "/proc", "/sys", "/var", "/opt", "/root",
		"/System", "/Library", "/Applications",
	}
	for _, d := range unixDangerous {
		if abs == d || strings.HasPrefix(abs, d+"/") {
			return true, fmt.Sprintf("refusing to search system directory %q; this path is outside the project scope", abs)
		}
	}

	// 3. Windows system directories
	winPrefixes := []string{
		`c:\windows`, `c:\program files`, `c:\program files (x86)`,
	}
	for _, d := range winPrefixes {
		if lower == d || strings.HasPrefix(lower, d+`\`) {
			return true, fmt.Sprintf("refusing to search system directory %q; this path is outside the project scope", abs)
		}
	}

	// 4. Home directories — too broad
	if homeDir, err := os.UserHomeDir(); err == nil {
		cleanHome := filepath.Clean(homeDir)
		if abs == cleanHome {
			return true, fmt.Sprintf("refusing to search from home directory %q; this would scan personal files. Specify a project subdirectory", abs)
		}
	}

	return false, ""
}

// GetTodos returns the current todo list for a session.
func (a *App) GetTodos(sessionID string) []TodoEntry {
	a.mu.Lock()
	defer a.mu.Unlock()
	list := a.todos[sessionID]
	if list == nil {
		return []TodoEntry{}
	}
	return cloneTodos(list)
}

// ClearTodos clears the current todo list for a session.
func (a *App) ClearTodos(sessionID string) {
	sid := strings.TrimSpace(sessionID)
	if sid == "" {
		return
	}
	a.mu.Lock()
	delete(a.todos, sid)
	a.todoRevisions[sid]++
	revision := a.todoRevisions[sid]
	a.mu.Unlock()
	a.emitTodoUpdate(sid, []TodoEntry{}, revision)
}

// emitTodoUpdate sends the current todo list to the frontend.
func (a *App) emitTodoUpdate(sid string, todos []TodoEntry, revision int64) {
	a.emit("todo:update", map[string]any{
		"sessionId": sid,
		"todos":     cloneTodos(todos),
		"revision":  revision,
	})
}

// ContextBreakdown breaks down estimated token usage by category.
type ContextBreakdownPart struct {
	Label  string `json:"label"`
	Tokens int    `json:"tokens"`
}

type ContextBreakdown struct {
	Total             int                    `json:"total"`
	SystemPrompt      int                    `json:"systemPrompt"`
	SystemPromptParts []ContextBreakdownPart `json:"systemPromptParts,omitempty"`
	ToolSchemas       int                    `json:"toolSchemas"`
	UserMessages      int                    `json:"userMessages"`
	AssistantMsgs     int                    `json:"assistantMsgs"`
	ToolResults       int                    `json:"toolResults"`
	Reasoning         int                    `json:"reasoning"`
}

// WorkspaceTokenUsage is the cumulative input/output token usage for a workspace.
type WorkspaceTokenUsage struct {
	InputTokens  int `json:"inputTokens"`
	OutputTokens int `json:"outputTokens"`
}

// GetContextBreakdown returns detailed token usage breakdown for a session.
func (a *App) GetContextBreakdown(sessionID string) ContextBreakdown {
	return a.getContextBreakdown(sessionID)
}

// GetWorkspaceTokenUsage returns cumulative usage for the current app run.
func (a *App) GetWorkspaceTokenUsage(workspace string) WorkspaceTokenUsage {
	key := workspaceUsageKey(workspace)
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.workspaceTokenUsage[key]
}

// GetSessionContextTokens returns the estimated token count for a session's full payload.
func (a *App) GetSessionContextTokens(sessionID string) int {
	return a.getContextBreakdown(sessionID).Total
}

// ResetWorkspaceTokenUsage resets cumulative token usage for a workspace.
func (a *App) ResetWorkspaceTokenUsage(workspace string) {
	key := workspaceUsageKey(workspace)
	a.mu.Lock()
	delete(a.workspaceTokenUsage, key)
	delete(a.lastEstimatedTokens, key)
	a.mu.Unlock()
	a.emit("tokens:reset", map[string]any{"workspace": workspace})
}

func (a *App) recordWorkspaceTokenUsage(workspace string, usage *modelUsage, fallbackInput, fallbackOutput int) {
	input := 0
	output := fallbackOutput
	if usage != nil {
		if usage.PromptTokens > 0 {
			input = usage.PromptTokens
		}
		if usage.CompletionTokens > 0 {
			output = usage.CompletionTokens
		}
	}
	// If the provider did not return real prompt usage, do not add the full
	// estimated request size: it includes the entire retained context and makes
	// the footer cumulative input counter jump by thousands on tiny prompts.
	_ = fallbackInput
	if input <= 0 && output <= 0 {
		return
	}

	key := workspaceUsageKey(workspace)
	a.mu.Lock()

	total := a.workspaceTokenUsage[key]
	total.InputTokens += input
	total.OutputTokens += output
	a.workspaceTokenUsage[key] = total
	a.mu.Unlock()

	a.emit("tokens:update", map[string]any{
		"workspace":    workspace,
		"inputTokens":  total.InputTokens,
		"outputTokens": total.OutputTokens,
	})
}

func workspaceUsageKey(workspace string) string {
	workspace = strings.TrimSpace(workspace)
	if workspace == "" {
		return "__default__"
	}
	if abs, err := filepath.Abs(workspace); err == nil {
		workspace = abs
	}
	workspace = filepath.Clean(workspace)
	if goruntime.GOOS == "windows" {
		workspace = strings.ToLower(workspace)
	}
	return workspace
}

func estimateTokensFromText(text string) int {
	if text == "" {
		return 0
	}
	asciiCount := 0
	nonAsciiCount := 0
	for _, r := range text {
		if r <= 127 {
			asciiCount++
		} else {
			nonAsciiCount++
		}
	}
	return int(math.Ceil(float64(asciiCount)/4)) + nonAsciiCount
}

func estimateRequestTokens(messages []openai.ChatCompletionMessage, tools []openai.Tool) int {
	total := 0
	for _, m := range messages {
		total += estimateTokensFromText(m.Role)
		total += estimateMessageBodyTokens(m)
		total += estimateTokensFromText(m.Name)
		total += estimateTokensFromText(m.ToolCallID)
		if m.ReasoningContent != "" {
			total += estimateTokensFromText(m.ReasoningContent)
		}
		for _, tc := range m.ToolCalls {
			total += estimateTokensFromText(tc.ID)
			total += estimateTokensFromText(string(tc.Type))
			total += estimateTokensFromText(tc.Function.Name)
			total += estimateTokensFromText(tc.Function.Arguments)
		}
	}
	total += estimateToolSchemaTokens(tools)
	return total
}

// builtinToolSchemaTokens caches the token estimate of the built-in tool
// list (chatTools()), which is static across the process lifetime. MCP tools
// change over time and must be re-marshaled per call; their contribution is
// added separately. Combined with chatToolsCache, this avoids re-marshaling
// the large static schema (typically 5-15KB JSON) on every getContextBreakdown
// call — the context popover refresh is user-visible and was hitting this
// path multiple times per second during a run.
var builtinToolSchemaTokens = sync.OnceValue(func() int {
	tools := chatTools()
	if len(tools) == 0 {
		return 0
	}
	data, _ := json.Marshal(tools)
	return estimateTokensFromText(string(data))
})

// mcpToolSchemaTokens re-marshals the MCP tool schemas portion of `tools`.
// `tools` is expected to be a slice returned by buildToolsWithMcp; only the
// entries whose Function.Name starts with `mcp__` are counted, so the cached
// built-in portion can be added by the caller without double counting.
func mcpToolSchemaTokens(tools []openai.Tool) int {
	if len(tools) == 0 {
		return 0
	}
	// Fast path: if no MCP tools, skip Marshal entirely.
	hasMcp := false
	for _, t := range tools {
		if t.Function != nil && strings.HasPrefix(t.Function.Name, "mcp__") {
			hasMcp = true
			break
		}
	}
	if !hasMcp {
		return 0
	}
	// Slow path: marshal only the MCP entries. Avoids re-marshaling the
	// large built-in schema that is already accounted for by the cache.
	mcpOnly := make([]openai.Tool, 0, 8)
	for _, t := range tools {
		if t.Function != nil && strings.HasPrefix(t.Function.Name, "mcp__") {
			mcpOnly = append(mcpOnly, t)
		}
	}
	if len(mcpOnly) == 0 {
		return 0
	}
	data, _ := json.Marshal(mcpOnly)
	return estimateTokensFromText(string(data))
}

func estimateToolSchemaTokens(tools []openai.Tool) int {
	if len(tools) == 0 {
		return 0
	}
	// Split into builtin (non-mcp__) and mcp portions. The builtin portion
	// uses the cache only when it is the full chatTools() set — grill mode
	// filters out side-effectful builtin tools, so the filtered builtin slice
	// must be re-marshaled to avoid over-counting the cached full-set tokens.
	cachedBuiltinCount := len(chatTools())
	builtinCount := 0
	hasMcp := false
	for _, t := range tools {
		if t.Function != nil && strings.HasPrefix(t.Function.Name, "mcp__") {
			hasMcp = true
			continue
		}
		builtinCount++
	}
	var total int
	if builtinCount == cachedBuiltinCount {
		// Full builtin set — use the cached estimate.
		total = builtinToolSchemaTokens()
	} else if builtinCount > 0 {
		// Filtered builtin subset (e.g. grill mode) — marshal directly.
		builtinOnly := make([]openai.Tool, 0, builtinCount)
		for _, t := range tools {
			if t.Function != nil && !strings.HasPrefix(t.Function.Name, "mcp__") {
				builtinOnly = append(builtinOnly, t)
			}
		}
		data, _ := json.Marshal(builtinOnly)
		total = estimateTokensFromText(string(data))
	}
	if hasMcp {
		total += mcpToolSchemaTokens(tools)
	}
	return total
}

func finalizeContextBreakdownTotal(result *ContextBreakdown) {
	result.Total = result.SystemPrompt + result.ToolSchemas + result.UserMessages + result.AssistantMsgs + result.ToolResults + result.Reasoning
}

func estimateMessageBodyTokens(m openai.ChatCompletionMessage) int {
	total := estimateTokensFromText(m.Content)
	for _, part := range m.MultiContent {
		switch part.Type {
		case openai.ChatMessagePartTypeText:
			total += estimateTokensFromText(part.Text)
		case openai.ChatMessagePartTypeImageURL:
			total += 256
		default:
			total += estimateTokensFromText(part.Text)
		}
	}
	return total
}

func estimateCompletionTokens(content, reasoning string, toolCalls []openai.ToolCall) int {
	total := estimateTokensFromText(content) + estimateTokensFromText(reasoning)
	for _, tc := range toolCalls {
		total += estimateTokensFromText(tc.ID)
		total += estimateTokensFromText(string(tc.Type))
		total += estimateTokensFromText(tc.Function.Name)
		total += estimateTokensFromText(tc.Function.Arguments)
	}
	return total
}

// getContextBreakdown computes token estimates from the real session state.
// If liveBreakdown is available, it returns a merged view (live messages + current system/tools).
func (a *App) getContextBreakdown(sessionID string) ContextBreakdown {
	a.mu.Lock()
	cfg := a.config
	a.mu.Unlock()

	result := ContextBreakdown{}
	for _, part := range buildSystemPromptParts(a.listCachedSkills(), cfg.Workspace, cfg.ExtraRoots, cfg.CustomPrompt, cfg.GitBashPath) {
		tokens := estimateTokensFromText(part.content)
		if tokens <= 0 {
			continue
		}
		result.SystemPrompt += tokens
		result.SystemPromptParts = append(result.SystemPromptParts, ContextBreakdownPart{
			Label:  part.label,
			Tokens: tokens,
		})
	}

	// Workspace map (appended as a separate system message in buildMessages)
	if wm := a.workspaceMapContext(cfg); wm != "" {
		tokens := estimateTokensFromText(wm)
		result.SystemPrompt += tokens
		result.SystemPromptParts = append(result.SystemPromptParts, ContextBreakdownPart{
			Label:  "工作区文件结构",
			Tokens: tokens,
		})
	}

	// Active goal context (also injected as a system message)
	if g := a.getActiveGoal(sessionID); g != nil {
		var goalCtx string
		budgetInfo := ""
		if g.TurnBudget > 0 {
			budgetInfo = fmt.Sprintf(" (turns %d/%d)", g.TurnsUsed, g.TurnBudget)
		}
		goalCtx = fmt.Sprintf("Active goal: %s | Status: %s | Turns: %d%s", g.Objective, g.Status, g.TurnsUsed, budgetInfo)
		if g.CompletionCriterion != "" {
			goalCtx += " | Completion: " + g.CompletionCriterion
		}
		tokens := estimateTokensFromText(goalCtx)
		result.SystemPrompt += tokens
		result.SystemPromptParts = append(result.SystemPromptParts, ContextBreakdownPart{
			Label:  "目标上下文",
			Tokens: tokens,
		})
	}

	// Check if live breakdown is available (covers tool calls + tool results not in a.histories)
	a.mu.Lock()
	live, hasLive := a.liveBreakdown[sessionID]
	a.mu.Unlock()
	if hasLive {
		// Use live message counts but keep current system/tool schemas (they may change)
		result.UserMessages = live.UserMessages
		result.AssistantMsgs = live.AssistantMsgs
		result.ToolResults = live.ToolResults
		result.Reasoning = live.Reasoning
	} else {
		// Fall back to history-based counting (missing tool calls + tool results)
		// loadSessionHistoryCopy triggers lazy disk load when the session is not yet
		// cached in this process (e.g. after switching sessions from localStorage),
		// so context stats are accurate on session restore without waiting for StartChat.
		hist := a.loadSessionHistoryCopy(sessionID)
		for _, m := range hist {
			tokens := estimateMessageBodyTokens(m)
			switch m.Role {
			case "user":
				result.UserMessages += tokens
			case "assistant":
				result.AssistantMsgs += tokens
			case "tool":
				result.ToolResults += tokens
			}
			for _, tc := range m.ToolCalls {
				result.AssistantMsgs += estimateTokensFromText(tc.Function.Name) + estimateTokensFromText(tc.Function.Arguments)
			}
			if m.ReasoningContent != "" {
				result.Reasoning += estimateTokensFromText(m.ReasoningContent)
			}
		}
	}

	result.ToolSchemas = estimateToolSchemaTokens(a.buildToolsForConfig(cfg))
	finalizeContextBreakdownTotal(&result)
	return result
}

// computeLiveBreakdown builds a ContextBreakdown from the actual live messages that will be sent to the API.
// This includes tool call arguments (assistant msgs with ToolCalls) and tool result messages,
// which are filtered out by saveHistory and thus missing from a.histories.
func computeLiveBreakdown(msgs []openai.ChatCompletionMessage) ContextBreakdown {
	result := ContextBreakdown{}
	for _, m := range msgs {
		tokens := estimateMessageBodyTokens(m)
		switch m.Role {
		case "user":
			result.UserMessages += tokens
		case "assistant":
			result.AssistantMsgs += tokens
		case "tool":
			result.ToolResults += tokens
		}
		for _, tc := range m.ToolCalls {
			result.AssistantMsgs += estimateTokensFromText(tc.Function.Name) + estimateTokensFromText(tc.Function.Arguments)
		}
		if m.ReasoningContent != "" {
			result.Reasoning += estimateTokensFromText(m.ReasoningContent)
		}
	}
	finalizeContextBreakdownTotal(&result)
	return result
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
	a.config.Model = m.Model
	a.config.Temperature = m.Temperature
	a.config.MaxTokens = m.MaxTokens
	if m.ContextWindow > 0 {
		a.config.ContextWindow = m.ContextWindow
	}
	a.config.TokenParam = m.TokenParam
	a.config.ReasoningTag = normalizeReasoningTag(m.ReasoningTag)
	cfg := a.config
	a.mu.Unlock()
	return a.saveConfig(cfg)
}

// handleTodoList implements the todo_write tool.
func (a *App) handleTodoList(sessionID string, req TodoListRequest) (any, error) {
	sid := strings.TrimSpace(sessionID)
	if sid == "" {
		return nil, errors.New("no active session")
	}

	for _, todo := range req.Todos {
		switch todo.Status {
		case "pending", "in_progress", "done":
		default:
			return nil, fmt.Errorf("invalid todo status %q: must be pending, in_progress, or done", todo.Status)
		}
		if strings.TrimSpace(todo.Title) == "" {
			return nil, errors.New("todo title is required")
		}
	}

	a.mu.Lock()
	// Query mode: return current list
	if req.Todos == nil {
		list := a.todos[sid]
		if list == nil {
			list = []TodoEntry{}
		}
		list = cloneTodos(list)
		revision := a.todoRevisions[sid]
		a.mu.Unlock()
		return map[string]any{
			"todos":    list,
			"revision": revision,
			"message":  "Current todo list.",
		}, nil
	}

	// Replace mode
	updated := cloneTodos(req.Todos)
	a.todos[sid] = updated
	a.todoRevisions[sid]++
	revision := a.todoRevisions[sid]
	a.mu.Unlock()

	a.emitTodoUpdate(sid, updated, revision)
	message := "Todo list updated."
	if len(updated) == 0 {
		message = "Todo list cleared."
	}
	return map[string]any{
		"todos":    updated,
		"revision": revision,
		"message":  message,
	}, nil
}

func cloneTodos(list []TodoEntry) []TodoEntry {
	if len(list) == 0 {
		return []TodoEntry{}
	}
	out := make([]TodoEntry, len(list))
	copy(out, list)
	return out
}

// ── Sub-agent management (frontend bindings) ──

// GetSubagents returns all sub-agent runs, both running and finished.
func (a *App) GetSubagents() []*SubagentRun {
	a.subRunsMu.Lock()
	defer a.subRunsMu.Unlock()
	result := make([]*SubagentRun, 0, len(a.subRuns))
	for _, r := range a.subRuns {
		result = append(result, cloneSubagentRun(r))
	}
	return result
}

func cloneSubagentRun(r *SubagentRun) *SubagentRun {
	if r == nil {
		return nil
	}
	c := *r
	c.cancel = nil
	c.FilesRead = append([]string(nil), r.FilesRead...)
	c.FilesEdited = append([]string(nil), r.FilesEdited...)
	c.ToolCalls = append([]SubToolEvent(nil), r.ToolCalls...)
	return &c
}

// StopSubagent cancels a running sub-agent.
func (a *App) StopSubagent(subID string) error {
	a.subRunsMu.Lock()
	run := a.subRuns[subID]
	if run == nil {
		a.subRunsMu.Unlock()
		return fmt.Errorf("sub-agent not found: %s", subID)
	}
	if run.Status != "running" {
		a.subRunsMu.Unlock()
		return fmt.Errorf("sub-agent is not running: %s", subID)
	}
	cancel := run.cancel
	a.subRunsMu.Unlock()
	if cancel != nil {
		cancel()
	}
	return nil
}
