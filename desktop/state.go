// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	backend "ally-dev/internal/app"
	"ally-dev/internal/tools/read"
	"ally-dev/locales"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
)

// uiPrefs mirrors the localStorage preferences the Vue app kept: appearance,
// rail geometry, section folding and the welcome-page avatar. It lives next to
// the agent data so both builds share the same machine.
type uiPrefs struct {
	Appearance         string   `json:"appearance,omitempty"`
	Palette            int      `json:"palette"`
	TopNavigation      bool     `json:"topNavigation,omitempty"`
	RailExpanded       bool     `json:"railExpanded"`
	RailWidth          float32  `json:"railWidth"`
	RailWorkspaceOpen  bool     `json:"railWorkspaceOpen"`
	RailSessionsOpen   bool     `json:"railSessionsOpen"`
	Avatar             string   `json:"avatar"`
	RecentModels       []string `json:"recentModels"`
	RecentWorkspaces   []string `json:"recentWorkspaces,omitempty"`
	TreeWidth          float32  `json:"treeWidth"`
	SeenReleaseNotes   string   `json:"seenReleaseNotes"`
	DismissedUpdateTag string   `json:"dismissedUpdateTag"`
	// AutostartInitialized gates the one-time default, not the current OS state.
	AutostartInitialized bool   `json:"autostartInitialized"`
	VoiceInputEnabled    bool   `json:"voiceInputEnabled,omitempty"`
	VoiceModelID         string `json:"voiceModelId,omitempty"`
	VoiceHotkey          string `json:"voiceHotkey,omitempty"`
	// CloseToTray parks the window in the tray instead of quitting. It defaults
	// on and is written out either way: `omitempty` would drop an explicit false
	// and read back as the default, so turning it off could never stick.
	CloseToTray bool `json:"closeToTray"`
	// VoiceModifierLeft and VoiceModifierRight decide which physical sides of
	// Ctrl/Alt/Shift/Win satisfy a hotkey modifier. Both are on by default, so a
	// chord never has to pin a side.
	VoiceModifierLeft  bool           `json:"voiceModifierLeft"`
	VoiceModifierRight bool           `json:"voiceModifierRight"`
	VoiceInputDeviceID string         `json:"voiceInputDeviceId,omitempty"`
	Pet                petPreferences `json:"pet"`
}

func defaultPrefs() uiPrefs {
	return uiPrefs{Appearance: "dark", Palette: defaultPalette, RailWidth: 220, RailWorkspaceOpen: true, RailSessionsOpen: true, Avatar: "whale", TreeWidth: 270, VoiceHotkey: "Alt", VoiceModifierLeft: true, VoiceModifierRight: true, CloseToTray: true, Pet: petPreferences{Enabled: true, Size: 180}}
}

var prefsMu sync.Mutex

func (a *desktop) prefsPath() string {
	if a.core == nil {
		return ""
	}
	dir := a.core.AppDataDir()
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "native-ui.json")
}

func (a *desktop) loadPrefs() { a.prefs = readPrefs(a.prefsPath()) }

func readPrefs(path string) uiPrefs {
	if path == "" {
		return defaultPrefs()
	}
	data, err := os.ReadFile(path)
	if err != nil {
		p := defaultPrefs()
		// Only a missing file establishes a new installation; other read errors
		// must not turn an existing opt-out back on.
		p.AutostartInitialized = !os.IsNotExist(err)
		return p
	}
	p := defaultPrefs()
	// Existing records predate the default-on behavior. Keep their OS state,
	// including an explicit opt-out, rather than enabling it during an upgrade.
	p.AutostartInitialized = true
	// Older records infer appearance from the palette when this field is absent.
	p.Appearance = ""
	if json.Unmarshal(data, &p) != nil {
		p = defaultPrefs()
		p.AutostartInitialized = true
		return p
	}
	if p.RailWidth < 160 || p.RailWidth > 360 {
		p.RailWidth = 220
	}
	if p.TreeWidth < 150 || p.TreeWidth > 600 {
		p.TreeWidth = 270
	}
	if p.Avatar == "" {
		p.Avatar = "whale"
	}
	// Older builds stored the side inside the hotkey ("RightAlt") and defaulted
	// to a single side; both normalize to the side-less chord plus the global
	// modifier-side preference.
	if hotkey, ok := parseVoiceHotkey(p.VoiceHotkey); ok {
		p.VoiceHotkey = hotkey.String()
	} else {
		p.VoiceHotkey = "Alt"
	}
	if !p.VoiceModifierLeft && !p.VoiceModifierRight {
		p.VoiceModifierLeft, p.VoiceModifierRight = true, true
	}
	p.Pet.normalize()
	p.RecentWorkspaces = workspaceHistoryRoots(p.RecentWorkspaces, nil)
	if len(p.RecentWorkspaces) > maxRecentWorkspaces {
		p.RecentWorkspaces = p.RecentWorkspaces[:maxRecentWorkspaces]
	}
	return p
}

// savePrefs writes an owned snapshot asynchronously. Failures are reported
// without blocking the UI and retained for the quit/update save barrier.
func (a *desktop) savePrefs(notice ...string) <-chan error {
	if a.prefsGeometryTimer != nil {
		a.prefsGeometryTimer.Stop()
		a.prefsGeometryTimer = nil
	}
	if a.core == nil {
		return nil
	}
	if a.page == "settings" && !a.committingSettings {
		a.prefsPending = true
		return nil
	}
	p := a.prefs
	p.RecentModels = append([]string(nil), p.RecentModels...)
	p.RecentWorkspaces = append([]string(nil), p.RecentWorkspaces...)
	p.Pet = p.Pet.clone()
	previous := a.prefsSaveTail
	done := make(chan struct{})
	a.prefsSaveTail = done
	a.prefsPending = false
	result := make(chan error, 1)
	path := a.prefsPath()
	go func() {
		defer close(done)
		if previous != nil {
			<-previous
		}
		prefsMu.Lock()
		defer prefsMu.Unlock()
		if path == "" {
			result <- nil
			return
		}
		data, err := json.MarshalIndent(p, "", "  ")
		if err == nil {
			err = read.SafeWriteFile(path, data, 0o600)
		}
		a.writeFailures.record("prefs", err)
		result <- err
		if a.win != nil {
			a.win.Update(func() {
				if err != nil && a.prefsSaveTail == done {
					a.prefsPending = true
				}
				a.prefsSaveResult(err, notice)
			})
		}
	}()
	return result
}

// Split controls report every drag frame. Save the final geometry after the
// drag settles; any ordinary save or the quit/update barrier also flushes it.
func (a *desktop) scheduleGeometryPrefsSave() {
	if a.core == nil || a.win == nil {
		return
	}
	if a.prefsGeometryTimer != nil {
		a.prefsGeometryTimer.Stop()
	}
	var timer *time.Timer
	timer = time.AfterFunc(300*time.Millisecond, func() {
		a.win.Update(func() {
			if a.prefsGeometryTimer != timer {
				return
			}
			a.prefsGeometryTimer = nil
			a.savePrefs()
		})
	})
	a.prefsGeometryTimer = timer
}

// roundStat mirrors one finished turn's numbers shown in the round footer.
type roundStat struct {
	DurationMS int64
	// ModelDurationMS sums provider-call time and excludes local tool execution.
	ModelDurationMS int64
	InputTokens     int
	OutputTokens    int
	CacheHit        int
	CacheMiss       int
	Done            bool
}

// retryInfo drives the retry banner above the composer.
type retryInfo struct {
	Attempt     int
	MaxAttempts int
	Error       string
	KeyIndex    int
	TotalKeys   int
}

// mentionState holds the @-file menu: entries, loading state and the request
// sequence used to discard stale searches.
type mentionState struct {
	Open    bool
	Index   int
	Entries []backend.WorkspacePathEntry
	Meta    string
	Loading bool
	Seq     int
}

// chatUI is per-conversation, non-persisted UI state. Message content stays in
// conversation.Messages; this only remembers what the reader expanded.
type chatUI struct {
	RoundAt              map[int]int
	roundIndices         memoOf[roundIndexKey, []int]
	chatRowsMemo         memoOf[chatRowsKey, []chatRow]
	Round                map[int]*roundStat
	PromptOpen           map[int]bool
	PromptHandles        map[int]*ui.Handle
	transcriptHandlePool []*ui.Handle
	PendingPrompt        bool
	PendingPromptIdx     int
	ToolOpen             map[string]bool
	CommandDetails       map[string]*commandDetail
	GroupOpen            map[string]bool
	AskIndex             int
	AskSelected          map[string][]string
	AskCustom            map[string]bool
	AskCustomTxt         map[string]string
	AskSubmitted         bool
	PlanCollapsed        bool
	CmdOpen              bool
	CmdIndex             int
	Mention              mentionState
	SessionsOpen         bool
	SessionsIndex        int
	ModelOpen            bool
	ModelSearch          string
	ContextOpen          bool
	PromptPartsOpen      bool // the prompt's sections are detail: folded until the parent row is clicked
	ExtraRootsOpen       bool
	Retry                *retryInfo
	Git                  backend.GitStatus
	GitLoaded            bool
	Usage                backend.WorkspaceTokenUsage
	Breakdown            backend.ContextBreakdown
	FooterLoaded         bool
	FooterBusy           bool
	FooterPending        bool
	CompactNote          string
	CompactBusy          bool
	CompactThinking      int
	Search               chatSearch
	DraftCaret           int
	RestoreCaret         bool
	Follow               bool
	FollowPending        bool
	// FollowBottom is the end a wheel asked to come back to. An answer that
	// grows on the way moves the end further down; waiting for the new one
	// would never arrive.
	FollowBottom     float32
	LastScrollY      float32
	HistoryIndex     int
	HistoryDraft     string
	WorkspaceHistory []string
	// List is the transcript's list: it builds only the rows in view and owns
	// where they are scrolled.
	List ui.ListState
	// built is the range of messages the last frame drew. Caches keyed by
	// message are pruned to it, so a long conversation does not keep a parsed
	// copy of every row it once showed.
	built builtRange
	// transcriptRows is the frame's ownership view. The transcript sets it for
	// the duration of one render so a card does not rebuild it per child.
	transcriptRows *transcriptRows
	ThinkingBytes  int
	// Stream grows the row that is streaming: see appendStreamChunk.
	Stream *streamAccumulator
}

type deletePromptState struct {
	Open         bool
	Conversation *conversation
	MessageIndex int
	UserIndex    int
	Content      string
}

func newChatUI() chatUI {
	return chatUI{
		Round:         map[int]*roundStat{},
		PromptOpen:    map[int]bool{},
		PromptHandles: map[int]*ui.Handle{},
		ToolOpen:      map[string]bool{},
		GroupOpen:     map[string]bool{},
		AskSelected:   map[string][]string{},
		AskCustom:     map[string]bool{},
		AskCustomTxt:  map[string]string{},
		Follow:        true,
		HistoryIndex:  -1,
		// The transcript follows its own end: the list keeps the newest
		// message in view as the conversation grows, and stops when the
		// reader scrolls up.
		List: ui.ListState{FollowEnd: true},
	}
}

func (t *conversation) uiState() *chatUI {
	if t.UI == nil {
		u := newChatUI()
		t.UI = &u
	}
	return t.UI
}

// settingsForm holds numeric mirrors for the settings tab controls. Config
// keeps the persisted values; these exist because NumberInput binds float64.
type settingsForm struct {
	AppearanceMode  int
	ProxyModeIndex  int
	Modules         map[string]int
	Tab             string
	Retries         float64
	Concurrency     float64
	CompactTimeout  float64
	Threshold       float64
	ThresholdMode   string
	ThresholdTokens float64
	FontMessage     float64
	FontCode        float64
	FontTool        float64
	FontSub         float64
	FontAux         float64
	BgOpacity       float64
	AllowPrivate    bool
	AutoUpdate      bool
	SendSessionID   bool
	SendXSessionID  bool
	Autostart       bool
	Validation      map[string]bool
	UpdateNote      string
	UpdateBusy      bool
	Proxy           backend.ProxyStatus
	ProxyNote       string
	ProxyBusy       bool
	API             backend.ApiServiceState
	APIPort         float64
	APIBusy         bool
	APIRevision     uint64
	APINote         string
	FullJSON        string
	NewToken        bool

	SessionHeaderConfirmOpen bool
	SessionHeaderToDisable   string
	BackgroundPath           *string
}

func (a *desktop) syncSettingsForm() {
	f := &a.settings
	f.AppearanceMode = 0
	if !a.currentPalette().dark {
		f.AppearanceMode = 1
	}
	if a.prefs.Appearance == "auto" {
		f.AppearanceMode = 2
	}
	f.ProxyModeIndex = 0
	switch a.configDraft.ProxyMode {
	case "system":
		f.ProxyModeIndex = 1
	case "manual":
		f.ProxyModeIndex = 2
	}
	f.Tab = a.settingsTab
	f.Retries = float64(a.configDraft.LLMRetries)
	f.Concurrency = float64(a.configDraft.ToolConcurrency)
	f.AllowPrivate = boolValue(a.configDraft.AllowPrivateNetwork)
	f.AutoUpdate = a.configDraft.AutoUpdate == nil || *a.configDraft.AutoUpdate
	f.Autostart = a.autostartValue
	f.SendSessionID = a.configDraft.SendSessionIDHeader == nil || *a.configDraft.SendSessionIDHeader
	f.SendXSessionID = a.configDraft.SendXSessionIDHeader == nil || *a.configDraft.SendXSessionIDHeader
	f.CompactTimeout = float64(a.configDraft.CompactTimeoutSeconds)
	f.Threshold = a.configDraft.CompactThreshold
	if f.Threshold <= 0 {
		f.Threshold = 0.6
	}
	compactCfg := normalizeSettingsConfig(a.configDraft)
	f.ThresholdMode = compactCfg.CompactThresholdMode
	f.ThresholdTokens = float64(compactCfg.CompactThresholdTokens)
	messageSize, codeSize := readingSizes(a.configDraft.MessageFontSize, a.configDraft.CodeFontSize)
	f.FontMessage, f.FontCode = float64(messageSize), float64(codeSize)
	f.FontTool = clampView(a.configDraft.ToolFontSize, 15)
	f.FontSub = clampView(a.configDraft.SubFontSize, 13)
	f.FontAux = clampView(a.configDraft.AuxFontSize, 12)
	f.BgOpacity = a.configDraft.BackgroundOpacity
	f.Validation = map[string]bool{
		"Python":     boolValue(a.configDraft.AutoValidationPython),
		"Go":         boolValue(a.configDraft.AutoValidationGo),
		"JavaScript": boolValue(a.configDraft.AutoValidationJavaScript),
		"TypeScript": boolValue(a.configDraft.AutoValidationTypeScript),
		"Vue":        boolValue(a.configDraft.AutoValidationVue),
		"Java":       boolValue(a.configDraft.AutoValidationJava),
		"JSON":       boolValue(a.configDraft.AutoValidationJSON),
		"YAML":       boolValue(a.configDraft.AutoValidationYAML),
		"XML":        boolValue(a.configDraft.AutoValidationXML),
	}
	if f.Tab == "" {
		f.Tab = "通用"
	}
	baseline := normalizeSettingsConfig(a.settingsConfigDraft()).Clone()
	a.settingsBase = &baseline
}

func clampView(v, def float64) float64 {
	if v <= 0 {
		return def
	}
	return v
}

// normalizeSettingsConfig maps a saved config onto the values the settings form
// shows, so comparing it with the form's projection does not report nil
// pointers and unset defaults as edits. Keep it in step with syncSettingsForm.
func normalizeSettingsConfig(cfg backend.ConfigState) backend.ConfigState {
	if cfg.AllowPrivateNetwork == nil {
		cfg.AllowPrivateNetwork = boolPtr(false)
	}
	if cfg.AutoUpdate == nil {
		cfg.AutoUpdate = boolPtr(true)
	}
	for _, field := range []**bool{
		&cfg.AutoValidationPython, &cfg.AutoValidationGo, &cfg.AutoValidationJavaScript,
		&cfg.AutoValidationTypeScript, &cfg.AutoValidationVue, &cfg.AutoValidationJava,
		&cfg.AutoValidationJSON, &cfg.AutoValidationYAML, &cfg.AutoValidationXML,
	} {
		if *field == nil {
			*field = boolPtr(false)
		}
	}
	if cfg.CompactThreshold <= 0 {
		cfg.CompactThreshold = 0.6
	}
	if cfg.CompactThresholdMode != backend.CompactThresholdModeTokens {
		cfg.CompactThresholdMode = backend.CompactThresholdModeRatio
	}
	if cfg.CompactThresholdTokens <= 0 {
		cfg.CompactThresholdTokens = backend.DefaultCompactThresholdTokens
	}
	body, code := readingSizes(cfg.MessageFontSize, cfg.CodeFontSize)
	cfg.MessageFontSize, cfg.CodeFontSize = float64(body), float64(code)
	cfg.ToolFontSize = clampView(cfg.ToolFontSize, 15)
	cfg.SubFontSize = clampView(cfg.SubFontSize, 13)
	cfg.AuxFontSize = clampView(cfg.AuxFontSize, 12)
	return cfg
}

// modelForm is the chat-model editor dialog state.
type modelForm struct {
	KeyEpoch       int
	AdvancedOpen   bool
	Open           bool
	Index          int
	MaxTokensInput string
	ContextInput   string
	Draft          backend.ModelConfig
	Keys           []string
	Headers        string
	Auth           string
	MaxTokens      float64
	Context        float64
	Busy           bool
	FetchBusy      bool
	FetchSeq       uint64
	FetchNote      string
	Providers      []string
	ProviderIdx    int
	TestBusy       bool
	Fetched        []string
}

// mcpForm is the MCP page + server editor state.
type mcpForm struct {
	Tab          int
	Search       string
	Busy         bool
	Revision     uint64
	ToolRevision uint64
	EditorOpen   bool
	Editing      string
	Name         string
	Transport    string
	Command      string
	Args         string
	Env          string
	URL          string
	Headers      string
	ToolOpen     map[string]bool
	ToolsDirty   bool
	ImportBusy   bool
}

// skillsForm is the skills page + add-skill dialog state.
type skillsForm struct {
	Search         string
	Source         int
	sourceIDs      []string
	Loading        bool
	ToggleBusy     map[string]bool
	AddOpen        bool
	AddTarget      string
	AddName        string
	AddDesc        string
	AddWhen        string
	AddContent     string
	AddBusy        bool
	ViewOpen       bool
	ViewLoading    bool
	ViewName       string
	ViewContent    string
	ViewError      string
	ViewRequestSeq uint64
	ViewMD         *markdown.Options
}

// sshForm is the SSH cluster page + editor dialog state.
type sshForm struct {
	Search          string
	Group           int
	GroupName       string
	EditorOpen      bool
	CloneMode       bool
	CloneSource     string
	ModalTitle      string
	EditingID       string
	OrgGroup        string
	GroupInput      string
	Draft           backend.SSHServerNode
	Port            float64
	Busy            bool
	TestBusy        map[string]bool
	DeleteID        string
	DeleteOpen      bool
	ImportBusy      bool
	AuthOpen        bool
	AuthOnlyAllowed bool
	AuthGroup       string
	AuthSearch      string
	AuthWorkspace   string
	AuthDraft       map[string]bool
	AuthReady       bool
	AuthLoadSeq     uint64
	AuthBusy        bool
}

// tasksForm is the task center state (tabs, scope, log dialog).
type scheduledTaskChange struct {
	task    backend.ScheduledTask
	deleted bool
}

type tasksForm struct {
	Tab     int
	Scope   int
	Loading bool
	// ManualPending records a refresh click that arrived while a snapshot was
	// in flight; the completion handler starts that load instead of dropping it.
	ManualPending    bool
	InstructionOpen  map[string]bool
	ScheduledChanges map[string]scheduledTaskChange
	ServiceChanges   map[string]backend.ServiceInfo
	LogOpen          bool
	LogTitle         string
	LogMeta          string
	LogBody          string
	LogService       string
	LogComplete      bool
	LogTruncated     bool
	LogRequestSeq    uint64
	// LogScroll keeps the log dialog's place; it follows the newest line unless
	// the reader scrolled up, so a running service does not yank them away.
	LogScroll  ui.ScrollState
	StopBusy   map[string]bool
	DeleteBusy map[string]bool
}

// pluginsForm is the plugin management state.
type pluginsForm struct {
	Busy       bool
	DeleteID   string
	DeleteOpen bool
	Purge      bool
	Importing  bool
	MenuOpen   map[string]bool
}

// updateUI drives the in-app update dialog.
type updateUI struct {
	CanAuto           bool
	Open              bool
	State             string // idle, checking, downloading, extracting, ready, preparing, applying, restarting, error, latest
	Tag               string
	Progress          float64
	BytesNow          int64
	BytesTotal        int64
	Error             string
	Note              string
	Busy              bool
	CheckBusy         bool
	Silent            bool
	Cancelling        bool
	Applied           bool
	RetryApply        bool
	Timer             *time.Timer
	ReleaseOpen       bool
	ReleaseVer        string
	ReleaseBody       string
	ReleaseDialogMD   *markdown.Options
	ReleaseSettingsMD *markdown.Options
}

// fileUI holds explorer-only state that is not part of the workspace files.
type fileUI struct {
	Search         explorerSearch
	TreeSelection  int
	InfoGeneration uint64
	InfoCancel     context.CancelFunc
	InfoWorkspace  string
	InfoNote       string
	InfoDetails    bool
	InfoOpen       bool
	Info           backend.WorkspaceFileInfo
	InfoPath       string
	InfoBusy       bool
	InfoError      string
	GitOpen        bool
	GitBusy        bool
	GitData        backend.GitDiffResult
	GitTree        []*gitTreeNode
	GitRepo        string
	GitFile        int
	NewOpen        bool
	NewDir         bool
	NewName        string
	NewParent      string
	Rename         bool
	RenameOf       string
	RenameTo       string
	PromptErr      string
	HelpOpen       bool
}

// gameUI keeps one local match per game kind plus the model choices.
type gameUI struct {
	Kind          int
	Started       bool
	Finished      bool
	VsAI          bool
	ModelA        string
	ModelB        string
	EffortA       string
	EffortB       string
	Thinking      bool
	RequestSeq    uint64
	Calls         int
	InTokens      int
	OutTokens     int
	Status        string
	Winner        int
	Board         []int
	Turn          int
	LastIndex     int
	Passes        int
	Ko            int
	XQ            [10][9]string
	Selected      int
	Hands         [3][]int
	Bottom        []int
	Landlord      int
	Phase         string
	LastCards     []int
	LastPlayer    int
	DDZPasses     int
	SelectedCards []int
}

// Confirm helper ----------------------------------------------------------

var _ = ui.Transparent

func (a *desktop) prefsSaveResult(err error, notice []string) {
	if err != nil {
		a.notifyError(locales.Text("保存外观偏好失败：") + err.Error())
		return
	}
	if len(notice) > 0 {
		a.notifySuccess(notice[0])
	}
}
