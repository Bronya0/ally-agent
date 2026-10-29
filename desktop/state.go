// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	backend "ally-dev/internal/app"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"github.com/egoist/mygo/ui"
)

// uiPrefs mirrors the localStorage preferences the Vue app kept: appearance,
// rail geometry, section folding and the welcome-page avatar. It lives next to
// the agent data so both builds share the same machine.
type uiPrefs struct {
	Appearance         string   `json:"appearance,omitempty"`
	Palette            int      `json:"palette"`
	RailExpanded       bool     `json:"railExpanded"`
	RailWidth          float32  `json:"railWidth"`
	RailWorkspaceOpen  bool     `json:"railWorkspaceOpen"`
	RailSessionsOpen   bool     `json:"railSessionsOpen"`
	Avatar             string   `json:"avatar"`
	RecentModels       []string `json:"recentModels"`
	TreeWidth          float32  `json:"treeWidth"`
	SeenReleaseNotes   string   `json:"seenReleaseNotes"`
	DismissedUpdateTag string   `json:"dismissedUpdateTag"`
}

func defaultPrefs() uiPrefs {
	return uiPrefs{Palette: 2, RailWidth: 220, RailWorkspaceOpen: true, RailSessionsOpen: true, Avatar: "image", TreeWidth: 270}
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
		return defaultPrefs()
	}
	p := defaultPrefs()
	if json.Unmarshal(data, &p) != nil {
		return defaultPrefs()
	}
	if p.RailWidth < 160 || p.RailWidth > 360 {
		p.RailWidth = 220
	}
	if p.TreeWidth < 150 || p.TreeWidth > 600 {
		p.TreeWidth = 270
	}
	if p.Avatar == "" {
		p.Avatar = "eye"
	}
	return p
}

// savePrefs is fire-and-forget: preferences are cosmetic, and a failed write
// must never interrupt the UI.
func (a *desktop) savePrefs() {
	if a.core == nil {
		return
	}
	p := a.prefs
	p.RecentModels = append([]string(nil), p.RecentModels...)
	previous := a.prefsSaveTail
	done := make(chan struct{})
	a.prefsSaveTail = done
	go func() {
		defer close(done)
		if previous != nil {
			<-previous
		}
		prefsMu.Lock()
		defer prefsMu.Unlock()
		path := a.prefsPath()
		if path == "" {
			return
		}
		data, err := json.MarshalIndent(p, "", "  ")
		if err != nil {
			return
		}
		_ = os.MkdirAll(filepath.Dir(path), 0o755)
		_ = os.WriteFile(path, data, 0o600)
	}()
}

// roundStat mirrors one finished turn's numbers shown in the round footer.
type roundStat struct {
	DurationMS   int64
	InputTokens  int
	OutputTokens int
	CacheHit     int
	CacheMiss    int
	Done         bool
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
	RoundAt          map[int]int
	Round            map[int]*roundStat
	PromptOpen       map[int]bool
	PromptHandles    map[int]*ui.Handle
	PendingPrompt    bool
	PendingPromptIdx int
	ToolOpen         map[string]bool
	GroupOpen        map[string]bool
	AskIndex         int
	AskSelected      map[string][]string
	AskCustom        map[string]bool
	AskCustomTxt     map[string]string
	AskSubmitted     bool
	PlanCollapsed    bool
	CmdOpen          bool
	CmdIndex         int
	Mention          mentionState
	SessionsOpen     bool
	SessionsIndex    int
	ModelOpen        bool
	ModelSearch      string
	ModelGroup       string
	FlyoutHot        bool
	ContextOpen      bool
	Retry            *retryInfo
	Git              backend.GitStatus
	GitLoaded        bool
	Usage            backend.WorkspaceTokenUsage
	Breakdown        backend.ContextBreakdown
	FooterLoaded     bool
	FooterBusy       bool
	FooterPending    bool
	CompactNote      string
	CompactBusy      bool
	CompactThinking  int
	DraftCaret       int
	RestoreCaret     bool
	Follow           bool
	FollowPending    bool
	LastScrollY      float32
	HistoryIndex     int
	HistoryDraft     string
	WorkspaceHistory []string
	VisibleStart     int
	ThinkingBytes    int
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
	AppearanceMode int
	ProxyModeIndex int
	Modules        map[string]int
	Tab            string
	Retries        float64
	Concurrency    float64
	CompactTimeout float64
	Threshold      float64
	FontMessage    float64
	FontCode       float64
	FontTool       float64
	FontSub        float64
	FontAux        float64
	BgOpacity      float64
	AllowPrivate   bool
	AutoUpdate     bool
	Autostart      bool
	Validation     map[string]bool
	UpdateNote     string
	UpdateBusy     bool
	Proxy          backend.ProxyStatus
	ProxyNote      string
	ProxyBusy      bool
	API            backend.ApiServiceState
	APIPort        float64
	APIBusy        bool
	APINote        string
	FullJSON       string
	NewToken       bool
}

func (a *desktop) syncSettingsForm() {
	f := &a.settings
	f.AppearanceMode = 0
	if !a.currentPalette().dark {
		f.AppearanceMode = 1
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
	f.CompactTimeout = float64(a.configDraft.CompactTimeoutSeconds)
	f.Threshold = a.configDraft.CompactThreshold
	if f.Threshold <= 0 {
		f.Threshold = 0.6
	}
	messageSize, codeSize := readingSizes(a.configDraft.MessageFontSize, a.configDraft.CodeFontSize)
	f.FontMessage, f.FontCode = float64(messageSize), float64(codeSize)
	f.FontTool = clampView(a.configDraft.ToolFontSize, 15)
	f.FontSub = clampView(a.configDraft.SubFontSize, 13)
	f.FontAux = clampView(a.configDraft.AuxFontSize, 12)
	f.BgOpacity = a.configDraft.BackgroundOpacity
	if f.BgOpacity <= 0 {
		f.BgOpacity = 0.15
	}
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
}

func clampView(v, def float64) float64 {
	if v <= 0 {
		return def
	}
	return v
}

// modelForm is the chat-model editor dialog state.
type modelForm struct {
	KeyEpoch     int
	AdvancedOpen bool
	Open         bool
	Index        int
	Draft        backend.ModelConfig
	Keys         []string
	Headers      string
	Auth         string
	MaxTokens    float64
	Context      float64
	Busy         bool
	FetchBusy    bool
	FetchNote    string
	Providers    []string
	ProviderIdx  int
	TestBusy     bool
	Fetched      []string
}

// mcpForm is the MCP page + server editor state.
type mcpForm struct {
	Tab        int
	Search     string
	Busy       bool
	EditorOpen bool
	Editing    string
	Name       string
	Transport  string
	Command    string
	Args       string
	Env        string
	URL        string
	Headers    string
	ToolOpen   map[string]bool
	ToolsDirty bool
	ImportBusy bool
}

// skillsForm is the skills page + add-skill dialog state.
type skillsForm struct {
	Search     string
	Source     int
	sourceIDs  []string
	Loading    bool
	ToggleBusy map[string]bool
	AddOpen    bool
	AddTarget  string
	AddName    string
	AddDesc    string
	AddWhen    string
	AddContent string
	AddBusy    bool
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
	PortText        string
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
	AuthBusy        bool
}

// tasksForm is the task center state (tabs, scope, log dialog).
type tasksForm struct {
	Tab        int
	Scope      int
	Loading    bool
	LogOpen    bool
	LogTitle   string
	LogMeta    string
	LogBody    string
	LogService string
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
	CanAuto     bool
	Open        bool
	State       string // idle, checking, downloading, ready, applying, restarting, error, latest
	Tag         string
	Progress    float64
	BytesNow    int64
	BytesTotal  int64
	Error       string
	Note        string
	Busy        bool
	ReleaseOpen bool
	ReleaseVer  string
	ReleaseBody string
	ReleaseHTML string
}

// fileUI holds explorer-only state that is not part of the workspace files.
type fileUI struct {
	Preview   bool
	InfoOpen  bool
	Info      backend.WorkspaceFileInfo
	InfoPath  string
	InfoBusy  bool
	InfoError string
	GitOpen   bool
	GitBusy   bool
	GitData   backend.GitDiffResult
	GitRepo   string
	GitFile   int
	NewOpen   bool
	NewDir    bool
	NewName   string
	NewParent string
	Rename    bool
	RenameOf  string
	RenameTo  string
	PromptErr string
	HelpOpen  bool
}

// gameUI keeps one local match per game kind plus the model choices.
type gameUI struct {
	Kind          int
	Started       bool
	VsAI          bool
	ModelA        string
	ModelB        string
	EffortA       string
	EffortB       string
	Thinking      bool
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
