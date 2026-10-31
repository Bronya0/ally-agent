// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"ally-dev/desktop/pluginhost"
	"ally-dev/internal/documentpreview"
	"ally-dev/resources"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"time"

	backend "ally-dev/internal/app"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/egoist/mygo/ui/markdown"
	"github.com/google/uuid"
)

type conversation struct {
	ID, Workspace, Title, Draft, RunID, Model, Status string
	Reasoning                                         string
	ContextTokens                                     int
	Messages                                          []map[string]any
	Busy                                              bool
	CancelRequested                                   bool
	Created                                           int64
	SnapshotQueued                                    bool
	Scroll                                            ui.ScrollState
	PendingAttachments                                []backend.AttachmentInput
	AttachmentLoading                                 bool
	AttachmentImages                                  map[string]*ui.Bitmap
	PendingAttachmentImages                           map[string]*ui.Bitmap
	AttachmentImageVersions                           map[string]uint64
	AttachmentImageJobs                               int
	AskID                                             string
	Questions                                         []backend.AskQuestion
	Answers                                           []string
	Selected                                          []string
	MD                                                map[int]*markdown.Options
	Expanded                                          map[int]bool
	UI                                                *chatUI
}

type workspaceFile struct {
	Path, Version string
	Editor        *editor.State
	PreviewPath   string
	PreviewHTML   string
	PreviewError  string
	Assist        *editorAssistIndex
	Saving        bool
	Document      *documentpreview.Document
	DocumentSheet string
	MarkupPreview bool
}
type desktop struct {
	memoryDiagnosticBusy                    bool
	pluginHost                              *pluginhost.Host
	previews                                previewPool
	previewRequests                         []previewRequest
	previewTimer                            *time.Timer
	previewGeneration                       uint64
	customAvatar                            *ui.Bitmap
	avatarStyle                             string
	avatarPoses                             map[string]*ui.Bitmap
	avatarLoadID                            uint64
	configSaveTail                          chan struct{}
	writeFailures                           windowWriteFailures
	prefsSaveTail                           chan struct{}
	lastCompletionNotice                    time.Time
	completionNotices                       map[string]bool
	completionNoticeOrder                   []string
	composerFocus                           string
	welcomeMCP, welcomeSkills, welcomeTools bool
	popupFlags                              map[string]*bool
	notifications                           []ui.Toast
	noticeDeadlines                         map[string]time.Time
	noticeSequence                          uint64
	taskPopup                               bool
	petals                                  bool
	ShowAllMessages                         bool
	modelSearch                             string
	modelTabIdx                             int
	modelProvider                           string
	modelProviderPreset                     string
	modelProviderModel                      string
	extraRoots                              map[string][]string
	sshAllowed                              map[string][]string
	prefs                                   uiPrefs
	autoAppearance                          autoAppearanceState
	settings                                settingsForm
	modelForm                               modelForm
	mcpF                                    mcpForm
	skillsF                                 skillsForm
	sshF                                    sshForm
	tasksF                                  tasksForm
	pluginsF                                pluginsForm
	upd                                     updateUI
	filesF                                  fileUI
	gameF                                   gameUI
	plans                                   map[string][]backend.PlanStep
	planRev                                 map[string]int64
	subRuns                                 map[string]*backend.SubagentRun
	railExpanded                            bool
	plugins                                 []backend.PluginInfo
	pluginBusy                              bool
	pluginSeq                               uint64
	pluginDelete                            *backend.PluginInfo
	pluginDeleteOpen, pluginPurge           bool
	pluginGroupOpen                         map[string]bool
	fileOpen                                map[string]bool
	fileRoot                                string
	fileChildren                            map[string][]backend.FileEntry
	fileLoading                             map[string]bool
	openSessions                            map[string]*conversation
	attachmentImageCacheSessions            []string
	attachmentImageJobSessions              map[*conversation]struct{}
	// tabStrips keeps one scroll position and one press per tab strip, by id
	// (see tabStrip): a strip's offset has to live in app state for the wheel to
	// move it, and its press outlives a frame.
	tabStrips                                 map[string]*tabStrip
	mcpSummary                                string
	toolCount                                 int
	toolInventory                             []backend.ToolDefinitionSummary
	mcpServers                                []map[string]any
	mcpTab, mcpNameFilter                     string
	mcpEditor, mcpBusy                        bool
	mcpServerNames                            []string
	sshServers                                []backend.SSHServerNode
	sshDraft                                  backend.SSHServerNode
	sshPort                                   float64
	sshEditing                                bool
	tasks                                     []backend.ScheduledTask
	services                                  []backend.ServiceInfo
	taskTab, taskScope, taskLog, taskLogTitle string
	stats                                     backend.TokenStatsResult
	configDraft                               backend.ConfigState
	settingsTab                               string
	settingsRetries, settingsConcurrency      float64
	settingsAllowPrivate                      bool
	apiState                                  backend.ApiServiceState
	apiPort                                   float64
	sandboxState                              backend.SandboxStatus
	autostartValue                            bool

	core                                                *backend.App
	win                                                 *mygo.Window
	ready, closing, quitReady                           bool
	page, filter                                        string
	config                                              backend.ConfigState
	tabs                                                []*conversation
	active                                              int
	sessions                                            []backend.SessionIndexEntry
	explorer                                            bool
	workspaceMenuOpen                                   bool
	startupError                                        string
	files                                               []backend.FileEntry
	workspaceFiles                                      map[string]*workspaceFile
	workspacePreview                                    workspacePreviewLifecycle
	fileEpoch                                           int
	modelTab                                            string
	modelSaving                                         bool
	modelKeys                                           []string
	modelHeaders                                        string
	modelAuthChoice                                     string
	modelMaxTokens, modelContextWindow                  float64
	modelPrevious                                       *backend.ModelIdentity
	oauth                                               *oauthState
	imageModels                                         *imageModelsState
	modelDraft                                          backend.ModelConfig
	modelEdit                                           int
	mcpText, configText, infoText, skillText, skillName string
	skills                                              []backend.SkillDefinition
	pageEpoch                                           int
	persistTail                                         chan struct{}
	sshAuthTail                                         chan struct{}
	sshRefreshSeq                                       uint64
	deletePrompt                                        *deletePromptState
}

// Run starts the native desktop host and its Agent runtime.
func Run() {
	if runScreenshotCaptureHelper() {
		return
	}
	if handled, err := backend.RunUpdateRelaunchHelper(os.Args[1:]); handled {
		if err != nil {
			log.Print(err)
		}
		return
	}
	if err := mygo.Protocol.HandleFunc("ally-preview", servePreviewDocument); err != nil {
		log.Fatal(err)
	}
	mygo.App.SetName("Ally Native")
	a := &desktop{core: backend.NewApp(), page: "chat", modelEdit: -1, explorer: true, plans: map[string][]backend.PlanStep{}, planRev: map[string]int64{}, subRuns: map[string]*backend.SubagentRun{}, prefs: defaultPrefs(), extraRoots: map[string][]string{}, sshAllowed: map[string][]string{}}
	a.loadPrefs()
	a.loadReleaseNotes()
	mygo.App.WhenReady(func() {
		a.refreshAutoAppearance(time.Now())
		a.syncNativeAppearance()
		opts := mygo.WindowOptions{
			Title: "Ally", Width: 1280, Height: 850, MinWidth: 900, MinHeight: 620,
			StateKey: "ally-native-main", TitleBarStyle: mygo.TitleBarHidden,
			TitleBarHeight: headerBarHeight,
			Hidden:         true, BackgroundColor: fmt.Sprintf("#%02x%02x%02x", a.currentPalette().bg.R, a.currentPalette().bg.G, a.currentPalette().bg.B), Content: ui.View(a.view),
		}
		// macOS keeps the native traffic lights over the hidden title bar, so
		// the header reserves their room instead of hiding them. Elsewhere the
		// header draws its own window buttons and the system ones stay hidden.
		if runtime.GOOS != "darwin" {
			opts.HideWindowButtons = true
		} else {
			// AppKit's own inset leaves the lights near the top of the 44px bar,
			// which reads as too high: put the top of the 16px buttons 14px down
			// so their center meets the bar's at 22. The same figure makes the
			// bar mygo reports exactly as tall as the header.
			opts.TrafficLightPosition = &mygo.Point{X: 14, Y: (headerBarHeight - 16) / 2}
		}
		a.win = mygo.NewWindow(opts)
		a.pluginHost = pluginhost.New(a.core, a.win, func(message, kind string) {
			if !a.closing {
				a.queueNotice(message, kind)
			}
		})
		mygo.App.OnDidBecomeActive(mygo.ClearNotifications)
		// Configure the native frame before showing it to avoid a white border
		// during startup, while keeping the native resize edges and shadow.
		configureWindowFrame(a.win)
		a.startAutoAppearance()
		if icon, err := resources.Assets.ReadFile("icon.png"); err == nil {
			_ = a.win.SetIcon(icon)
			if runtime.GOOS == "darwin" {
				_ = mygo.App.Dock.SetIcon(icon)
			}
		}
		showWindowAfterFirstFrame(a.win)
		a.core.SetNativeWindow(a.win)
		if len(os.Args) > 1 && os.Args[1] == "--smoke" {
			time.AfterFunc(4*time.Second, func() { a.win.Update(func() { log.Print("native window startup verified"); a.shutdown() }) })
		}
		a.win.OnClose(func(e *mygo.CloseEvent) {
			if !a.quitReady {
				e.PreventDefault()
				a.shutdown()
			}
		})
		mygo.App.OnBeforeQuit(func(e *mygo.QuitEvent) {
			if !a.quitReady {
				e.PreventDefault()
				a.shutdown()
			}
		})
		prefs := a.prefs
		go func() {
			if logger, _, err := backend.InitErrorLogger(); err == nil {
				a.core.SetErrorLogger(logger)
			}
			customAvatar := a.loadCustomAvatar()
			avatarStyle := normalizedAvatarStyle(prefs.Avatar)
			avatarPoses := decodeAvatarStyle(avatarStyle)
			err := a.core.StartNative(context.Background(), a.onEvent)
			if err != nil {
				a.win.Update(func() { a.startupError = "启动失败：" + err.Error() })
				return
			}
			cfg, err := a.core.GetConfig()
			if err != nil {
				a.win.Update(func() { a.startupError = "读取配置失败：" + err.Error() })
				return
			}
			sessions, _ := a.core.ListSessionIndex()
			workspace := startupWorkspace(sessions, cfg.Workspace)
			skills, _ := a.core.ListSkills()
			tools := a.core.ListTools()
			servers := a.core.GetMcpServers()
			plugins, pluginErr := a.core.ListPlugins()
			a.win.Update(func() {
				a.config = cfg
				a.syncNativeAppearance()
				a.customAvatar = customAvatar
				a.avatarStyle, a.avatarPoses = avatarStyle, avatarPoses
				a.plugins = plugins
				if pluginErr != nil {
					a.notifyError("插件列表加载失败：" + pluginErr.Error())
				}
				a.skills = skills
				a.toolInventory = tools
				a.toolCount = enabledToolCount(tools)
				a.mcpServers = servers
				a.mcpSummary = mcpSummary(servers)
				a.sessions = sessions
				for _, tab := range a.tabs {
					a.loadWorkspaceInputHistory(tab)
				}
				a.syncSettingsForm()
				a.ready = true
				a.newConversation(workspace)
				a.maybeShowReleaseNotes()
				if a.installedRelease() {
					go a.core.ConfirmUpdateStartup()
				}
				a.scheduleUpdateCheck(10 * time.Second)
			})
		}()
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
func (a *desktop) shutdown() {
	if a.closing {
		return
	}
	if a.upd.installing() && !a.upd.Applied {
		a.notify("正在准备更新，请等待完成")
		return
	}
	a.closing = true
	if a.upd.Applied && a.upd.State == "restarting" {
		// The updater already flushed after ApplyUpdate stopped all runs.
		a.finishQuit()
		return
	}
	a.prepareWindowClose(a.finishShutdown, func() { a.closing = false })
}

// Both normal quit and updating check editor changes before replacing files.
func (a *desktop) prepareWindowClose(proceed, cancel func()) {
	for _, file := range a.workspaceFiles {
		if file.Saving {
			cancel()
			a.notify("文件正在保存，请稍后退出")
			return
		}
		if file.Editor != nil && file.Editor.Buffer.Modified() {
			go func() {
				result, err := mygo.Dialog.Message(mygo.MessageOptions{Parent: a.win, Type: mygo.MessageWarning, Title: "未保存的文件", Message: "存在未保存的文件修改", Detail: "退出会放弃这些修改。", Buttons: []string{"取消", "放弃并退出"}, CancelButton: 0})
				a.win.Update(func() {
					if err != nil {
						a.notifyError(err.Error())
						cancel()
						return
					}
					if result.Button != 1 {
						cancel()
						return
					}
					proceed()
				})
			}()
			return
		}
	}
	proceed()
}
func (a *desktop) finishShutdown() {
	a.flushWindowWrites(func(err error) {
		if err != nil {
			a.closing = false
			a.notifyError("保存失败，窗口保持打开：" + err.Error())
			return
		}
		a.finishQuit()
	})
}

func (a *desktop) finishQuit() {
	a.stopAutoAppearance()
	a.stopUpdateChecks()
	go func() {
		if err := a.core.ShutdownNative(); err != nil {
			log.Printf("shutdown: %v", err)
		}
		a.win.Update(func() {
			a.discardAttachmentImagesOnShutdown()
			a.releaseWorkspaceFiles()
			a.releaseAllHTMLPreviews()
			if a.pluginHost != nil {
				a.pluginHost.Close()
			}
			a.quitReady = true
			mygo.App.Quit()
		})
	}()
}
func (a *desktop) current() *conversation {
	if a.active < 0 || a.active >= len(a.tabs) {
		return nil
	}
	return a.tabs[a.active]
}
func (a *desktop) newConversation(workspace string) {
	if a.page == "settings" {
		a.commitSettingsOnLeave()
	}
	if a.page == "models" {
		a.cancelOAuthWhenLeaving()
	}
	if workspace == "" {
		go func() {
			p, err := a.core.CreateTempWorkspace()
			if err != nil {
				a.fail(err)
				return
			}
			a.win.Update(func() {
				if !a.closing {
					a.newConversation(p)
				}
			})
		}()
		return
	}

	t := a.freshConversation(workspace)
	a.composerFocus = t.ID
	a.tabs = append(a.tabs, t)
	a.page = "chat"
	a.activateTab(len(a.tabs) - 1)
	a.refreshFiles()
	a.refreshSessionFooter(t)
	a.refreshGit(t)
}

func (a *desktop) freshConversation(workspace string) *conversation {
	t := &conversation{ID: uuid.NewString(), Workspace: workspace, Title: "新对话", Created: time.Now().UnixMilli(), MD: map[int]*markdown.Options{}, Expanded: map[int]bool{}}
	if a.config.LastUsedModel != nil {
		for _, model := range a.config.Models {
			if sameModelIdentity(modelIdentity(model), *a.config.LastUsedModel) {
				t.Model = modelOption(model)
				break
			}
		}
	}
	for _, m := range a.config.Models {
		if modelOption(m) == t.Model {
			t.Reasoning = m.ReasoningEffort
			break
		}
	}
	if t.Reasoning == "" {
		t.Reasoning = "auto"
	}
	a.loadWorkspaceInputHistory(t)
	return t
}

func (a *desktop) rememberSession(t *conversation) {
	if t == nil {
		return
	}
	releaseCommandDetails(t)
	if a.openSessions == nil {
		a.openSessions = map[string]*conversation{}
	}
	if !t.Busy && len(t.PendingAttachments) == 0 {
		t.MD = nil
		a.releaseAttachmentImages(t)
		a.forgetAttachmentImageCache(t.ID)
	}
	// Keep only the most recent disposable transcript. Unsaved input and
	// running work must survive; older clean sessions can reload from disk.
	for id, old := range a.openSessions {
		if old != t && !old.Busy && old.Draft == "" && len(old.PendingAttachments) == 0 && !old.AttachmentLoading {
			delete(a.openSessions, id)
		}
	}
	a.openSessions[t.ID] = t
}

func (a *desktop) newSession() {
	if a.page == "settings" {
		a.commitSettingsOnLeave()
	}
	if a.page == "models" {
		a.cancelOAuthWhenLeaving()
	}
	t := a.current()
	if t == nil {
		return
	}
	next := a.freshConversation(t.Workspace)
	next.Model = t.Model
	next.Reasoning = t.Reasoning
	a.composerFocus = next.ID
	keepInTabs := t.Busy || t.uiState().CompactBusy
	if keepInTabs {
		// Active sessions must stay in tabs: event dispatch routes their
		// streaming output and completion by session ID through this list.
		a.tabs = append(a.tabs, next)
	} else {
		a.rememberSession(t)
		a.tabs[a.active] = next
	}
	a.page = "chat"
	if keepInTabs {
		a.activateTab(len(a.tabs) - 1)
	} else {
		a.activateTab(a.active)
	}
	a.notifySuccess("已新建会话")
	if a.win != nil {
		a.refreshSessionFooter(next)
		a.refreshGit(next)
	}
}

func (a *desktop) openWorkspace(workspace string) {
	if a.page == "settings" {
		a.commitSettingsOnLeave()
	}
	if a.page == "models" {
		a.cancelOAuthWhenLeaving()
	}
	for i, t := range a.tabs {
		if strings.EqualFold(t.Workspace, workspace) {
			a.page = "chat"
			a.activateTab(i)
			a.refreshFiles()
			a.refreshSessionFooter(t)
			a.refreshGit(t)
			return
		}
	}
	a.newConversation(workspace)
}
func modelLabel(p, m string) string {
	if p == "" {
		return m
	}
	return p + " / " + m
}

// notify is called on the UI thread; the view drains the queue into the
// window's toast service so background work never captures a UI context.
func (a *desktop) notify(message string)        { a.queueNotice(message, "info") }
func (a *desktop) notifyError(message string)   { a.queueNotice(message, "error") }
func (a *desktop) notifySuccess(message string) { a.queueNotice(message, "success") }
func (a *desktop) queueNotice(message, kind string) {
	if message == "" {
		return
	}
	a.noticeSequence++
	item := ui.Toast{ID: fmt.Sprintf("ally-notice-%d", a.noticeSequence), Title: message, Type: kind, Data: time.Now().Add(4 * time.Second)}
	if len(a.notifications) > 0 && a.notifications[len(a.notifications)-1].Title == message && a.notifications[len(a.notifications)-1].Type == kind {
		return
	}
	if len(a.notifications) >= 8 {
		a.notifications = a.notifications[1:]
	}
	a.notifications = append(a.notifications, item)
}

func (a *desktop) fail(err error) {
	if err != nil {
		a.win.Update(func() { a.notifyError(err.Error()) })
	}
}

// job runs work off the UI thread and reports the failure it meets. A success
// shows only the notice the caller names: opening a folder or cancelling a run
// speaks for itself, and a default "操作完成" there told the user nothing.
func (a *desktop) job(work func() error, notice ...string) {
	go func() {
		err := work()
		a.win.Update(func() {
			if err != nil {
				a.notifyError(err.Error())
				return
			}
			if len(notice) > 0 {
				a.notifySuccess(notice[0])
			}
		})
	}()
}
func (a *desktop) chooseWorkspace() {
	go func() {
		p, e := a.core.SelectWorkspace()
		if e != nil {
			a.fail(e)
			return
		}
		if p != "" {
			a.win.Update(func() { a.config.Workspace = p; a.newConversation(p) })
		}
	}()
}

// inject delivers a follow-up into the live run instead of starting a new one:
// the backend queues it and the model picks it up at the next agent step, so a
// running conversation accepts more input, including attachments.
func (a *desktop) inject(t *conversation, text string) bool {
	if a.upd.installing() || a.upd.Applied {
		return false
	}
	if text == "" && len(t.PendingAttachments) == 0 {
		return false
	}
	if t.RunID == "" {
		a.notifyError("本轮正在启动，请稍后再追加")
		return false
	}
	attachments := append([]backend.AttachmentInput(nil), t.PendingAttachments...)
	if err := a.core.InjectRunMessage(t.RunID, text, attachments...); err != nil {
		a.notifyError("追加失败：" + err.Error())
		return false
	}
	display := text
	if display == "" && len(attachments) > 0 {
		display = "附件：" + attachments[0].Name
	}
	displayAttachments := attachmentDisplayRows(attachments)
	t.Messages = append(t.Messages, map[string]any{"role": "user", "content": display, "attachments": displayAttachments})
	commitPendingPreviews(t)
	t.PendingAttachments = nil
	t.Draft = ""
	t.Status = "已追加，本轮继续执行…"
	t.uiState().Follow = true
	t.uiState().FollowPending = true
	a.persist(t)
	return true
}

// chatConfig resolves the model a Tab selected into a request config. A
// compaction sends the same overlay a chat request does: the backend cannot read
// Tab state, and a session restored from disk has no frozen record yet, so
// without it a summary request would go to whatever endpoint ran last.
func (a *desktop) chatConfig(t *conversation) (backend.ConfigState, bool) {
	cfg := a.config
	cfg.Workspace = t.Workspace
	// Always send the session's list, including an explicit empty list. A new
	// run publishes this config as its live roots and must not erase UI grants.
	cfg.ExtraRoots = append([]string{}, a.extraRoots[t.ID]...)
	for _, m := range cfg.Models {
		if modelOption(m) != t.Model {
			continue
		}
		cfg.AuthType = m.AuthType
		cfg.OAuthAccountID = m.OAuthAccountID
		cfg.ProviderName = m.ProviderName
		cfg.Model = m.Model
		cfg.APIFormat = m.APIFormat
		cfg.BaseURL = m.BaseURL
		cfg.APIKey = m.APIKey
		cfg.APIKeys = append([]string(nil), m.APIKeys...)
		cfg.MaxTokens = m.MaxTokens
		cfg.ContextWindow = m.ContextWindow
		cfg.TokenParam = m.TokenParam
		cfg.ReasoningTag = m.ReasoningTag
		cfg.ReasoningEffort = m.ReasoningEffort
		if t.Reasoning != "" {
			cfg.ReasoningEffort = t.Reasoning
		}
		cfg.CustomHeaders = m.CustomHeaders
		cfg.VisionCapable = m.VisionCapable
		cfg.LastUsedModel = modelIdentityPtr(m)
		return cfg, cfg.Model != ""
	}
	return cfg, false
}

func (a *desktop) send(t *conversation, text string) bool {
	text = strings.TrimSpace(text)
	if (text == "" && len(t.PendingAttachments) == 0) || t.AttachmentLoading || !a.ready || a.closing || a.upd.installing() || a.upd.Applied {
		return false
	}
	if t.Busy {
		return a.inject(t, text)
	}
	if t.Model == "" {
		a.notifyError("请先在模型页面配置并选择模型")
		return false
	}
	cfg, found := a.chatConfig(t)
	if !found {
		a.notifyError("所选模型已不存在，请重新选择")
		return false
	}
	attachments := append([]backend.AttachmentInput(nil), t.PendingAttachments...)
	display := text
	if display == "" && len(attachments) > 0 {
		display = "附件：" + attachments[0].Name
	}
	if len(t.Messages) == 0 {
		t.Title = short(display, 36)
	}
	displayAttachments := attachmentDisplayRows(attachments)
	t.Messages = append(t.Messages, map[string]any{"role": "user", "content": display, "attachments": displayAttachments})
	commitPendingPreviews(t)
	t.PendingAttachments = nil
	t.Busy = true
	t.Draft = ""
	t.Status = "正在连接模型…"
	t.uiState().Follow = true
	t.uiState().FollowPending = true
	a.persist(t)
	go func() {
		id, err := a.core.StartChat(backend.ChatRequest{SessionID: t.ID, Message: text, Attachments: attachments, Config: cfg})
		a.win.Update(func() {
			if err != nil {
				t.Busy = false
				t.Status = err.Error()
				a.notifyError(err.Error())
				t.Messages = append(t.Messages, map[string]any{"role": "assistant", "content": err.Error(), "error": true})
				a.persist(t)
			} else if t.Busy {
				t.RunID = id
				if t.CancelRequested {
					t.CancelRequested = false
					a.job(func() error { return a.core.CancelRun(id) })
				}
			}
		})
	}()
	return true
}
func snapshot(t *conversation) backend.SessionSnapshot {
	// Detach the JSON-shaped transcript before the asynchronous save. Copying
	// maps/slices directly avoids encoding and decoding the entire conversation
	// on every persisted UI event. Unusual values still use a JSON round-trip so
	// the snapshot keeps the old normalization behavior for them.
	var msgs []map[string]any
	if t.Messages != nil {
		msgs = make([]map[string]any, len(t.Messages))
		active := make(map[snapshotCloneVisit]struct{})
		for i, message := range t.Messages {
			if message == nil {
				continue
			}
			messageCopy := make(map[string]any, len(message))
			for key, value := range message {
				messageCopy[key] = value
			}
			// Drop large command output before recursively copying it. This only
			// mutates the shallow copy; the live transcript remains unchanged.
			retainLocalCommandSummary(messageCopy)
			clear(active)
			cloned, err := cloneSnapshotJSONValue(messageCopy, 0, active)
			if err != nil {
				// Match the former failed Marshal/Unmarshal path: do not persist a
				// partially cloned transcript.
				msgs = nil
				break
			}
			msgs[i], _ = cloned.(map[string]any)
		}
	}
	// Stored image references are durable; avoid copying large base64 payloads
	// into every snapshot once the backend confirms the image was saved.
	for _, m := range msgs {
		for _, att := range messageAttachmentList(m) {
			if str(att, "imageRef") != "" {
				delete(att, "dataUrl")
			}
		}
	}
	return backend.SessionSnapshot{ID: t.ID, Title: t.Title, Draft: t.Draft, Workspace: t.Workspace, CreatedAt: t.Created, UpdatedAt: time.Now().UnixMilli(), ContextTokens: t.ContextTokens, HasSnapshot: true, Messages: msgs}
}

const maxSnapshotCloneDepth = 10_000

type snapshotCloneVisit struct {
	kind reflect.Kind
	ptr  uintptr
}

// cloneSnapshotJSONValue detaches the generic JSON values used by transcript
// messages. The bounded recursion also turns cycles into the same all-or-none
// snapshot failure the previous JSON marshal path produced.
func cloneSnapshotJSONValue(value any, depth int, active map[snapshotCloneVisit]struct{}) (any, error) {
	if depth > maxSnapshotCloneDepth {
		return nil, fmt.Errorf("session snapshot nesting exceeds %d", maxSnapshotCloneDepth)
	}
	switch value := value.(type) {
	case map[string]any:
		if value == nil {
			return map[string]any(nil), nil
		}
		visit := snapshotCloneVisit{kind: reflect.Map, ptr: reflect.ValueOf(value).Pointer()}
		if _, exists := active[visit]; exists {
			return nil, fmt.Errorf("session snapshot contains a cycle")
		}
		active[visit] = struct{}{}
		defer delete(active, visit)
		mapCopy := make(map[string]any, len(value))
		for key, nested := range value {
			cloned, err := cloneSnapshotJSONValue(nested, depth+1, active)
			if err != nil {
				return nil, err
			}
			mapCopy[key] = cloned
		}
		return mapCopy, nil
	case []any:
		if value == nil {
			return []any(nil), nil
		}
		visit := snapshotCloneVisit{kind: reflect.Slice, ptr: reflect.ValueOf(value).Pointer()}
		if len(value) > 0 {
			if _, exists := active[visit]; exists {
				return nil, fmt.Errorf("session snapshot contains a cycle")
			}
			active[visit] = struct{}{}
			defer delete(active, visit)
		}
		sliceCopy := make([]any, len(value))
		for i, nested := range value {
			cloned, err := cloneSnapshotJSONValue(nested, depth+1, active)
			if err != nil {
				return nil, err
			}
			sliceCopy[i] = cloned
		}
		return sliceCopy, nil
	case []map[string]any:
		if value == nil {
			return []any(nil), nil
		}
		visit := snapshotCloneVisit{kind: reflect.Slice, ptr: reflect.ValueOf(value).Pointer()}
		if len(value) > 0 {
			if _, exists := active[visit]; exists {
				return nil, fmt.Errorf("session snapshot contains a cycle")
			}
			active[visit] = struct{}{}
			defer delete(active, visit)
		}
		sliceCopy := make([]any, len(value))
		for i, nested := range value {
			cloned, err := cloneSnapshotJSONValue(nested, depth+1, active)
			if err != nil {
				return nil, err
			}
			sliceCopy[i] = cloned
		}
		return sliceCopy, nil
	case nil, bool, string:
		return value, nil
	case float64:
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return nil, fmt.Errorf("session snapshot contains a non-finite number")
		}
		return value, nil
	case int:
		return float64(value), nil
	case int8:
		return float64(value), nil
	case int16:
		return float64(value), nil
	case int32:
		return float64(value), nil
	case int64:
		return float64(value), nil
	case uint:
		return float64(value), nil
	case uint8:
		return float64(value), nil
	case uint16:
		return float64(value), nil
	case uint32:
		return float64(value), nil
	case uint64:
		return float64(value), nil
	default:
		data, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		var cloned any
		if err := json.Unmarshal(data, &cloned); err != nil {
			return nil, err
		}
		return cloned, nil
	}
}

func (a *desktop) persist(t *conversation) {
	t.SnapshotQueued = true
	s := snapshot(t)
	previous := a.persistTail
	done := make(chan struct{})
	a.persistTail = done
	go func() {
		defer close(done)
		if previous != nil {
			<-previous
		}
		err := a.core.SaveSession(s)
		a.writeFailures.record("session:"+s.ID, err)
		if err != nil {
			a.fail(err)
			return
		}
		list, _ := a.core.ListSessionIndex()
		a.win.Update(func() { a.sessions = list })
	}()
}
func (a *desktop) openSession(id string) {
	if a.page == "settings" {
		a.commitSettingsOnLeave()
	}
	if a.page == "models" {
		a.cancelOAuthWhenLeaving()
	}
	for i, t := range a.tabs {
		if t.ID == id {
			a.page = "chat"
			a.activateTab(i)
			a.refreshFiles()
			a.refreshSessionFooter(t)
			a.refreshGit(t)
			return
		}
	}
	if t := a.openSessions[id]; t != nil {
		a.showSession(t)
		return
	}
	go func() {
		s, err := a.core.LoadSession(id)
		if err != nil {
			a.fail(err)
			return
		}
		// Only replace a cleaned temporary workspace. Never silently route a
		// missing project to a different directory.
		if info, statErr := os.Stat(s.Workspace); (s.Workspace == "" || strings.HasPrefix(filepath.Base(s.Workspace), "ally-temp-")) && (statErr != nil || !info.IsDir()) {
			root, tempErr := a.core.CreateTempWorkspace()
			if tempErr != nil {
				a.fail(tempErr)
				return
			}
			s.Workspace = root
		}
		a.win.Update(func() {
			t := a.freshConversation(s.Workspace)
			t.ID, t.Title, t.Created, t.Messages, t.ContextTokens = s.ID, s.Title, s.CreatedAt, s.Messages, s.ContextTokens
			t.Draft = s.Draft
			t.SnapshotQueued = true
			for _, message := range t.Messages {
				if str(message, "role") == "tool_call" || str(message, "toolCallId") != "" {
					normalizeToolMessage(message)
				}
				retainLocalCommandSummary(message)
			}
			restoreRoundStats(t)
			a.showSession(t)
		})
	}()
}

func (a *desktop) showSession(next *conversation) {
	// A transcript restored from disk carries no offset, and a scroll container
	// shows its start at 0. Park it at the newest message; a conversation the
	// user left scrolled somewhere keeps that position.
	if len(next.Messages) > 0 && next.uiState().Follow && next.Scroll.Y == 0 {
		followLatest(next)
	}
	if a.page == "models" {
		a.cancelOAuthWhenLeaving()
	}
	for i, t := range a.tabs {
		if strings.EqualFold(t.Workspace, next.Workspace) && !t.Busy {
			a.rememberSession(t)
			if next.Model == "" {
				next.Model = t.Model
			}
			a.tabs[i] = next
			a.page = "chat"
			a.activateTab(i)
			a.refreshFiles()
			a.refreshSessionFooter(next)
			a.refreshGit(next)
			return
		}
	}
	a.tabs = append(a.tabs, next)
	a.page = "chat"
	a.activateTab(len(a.tabs) - 1)
	a.refreshFiles()
	a.refreshSessionFooter(next)
	a.refreshGit(next)
}

// onEvent is wired to the backend event sink; the dispatcher lives in
// events.go so the chat, task and update streams stay together.
func (a *desktop) onEvent(name string, payload any) { a.dispatchEvent(name, payload) }
func pretty(v any) string                           { b, _ := json.MarshalIndent(v, "", "  "); return string(b) }
func (a *desktop) refreshFiles(manual ...bool) {
	if a.core == nil || a.win == nil {
		return
	}
	t := a.current()
	if t == nil {
		return
	}
	a.fileEpoch++
	epoch := a.fileEpoch
	root := t.Workspace
	a.prepareFileTree(root)
	a.workspacePreview.cancelLoad("")
	a.closeFileInfo()
	if root == "" {
		return
	}
	go func() {
		r, e := a.core.ListFiles(backend.ListFilesRequest{Workspace: root, Path: ".", MaxDepth: 1, Limit: 1000, IncludeHidden: true, IncludeIgnored: true})
		a.win.Update(func() {
			if epoch != a.fileEpoch {
				return
			}
			if e != nil {
				a.notifyError(e.Error())

				return
			}
			a.applyFileTreeEntries("", r.Entries)
			if len(manual) > 0 && manual[0] {
				a.notifySuccess("资源树已刷新")
			}
			if r.Truncated {
				a.notify("文件过多，目录列表仅显示前 1000 项")
			}
		})
	}()
}
func (a *desktop) openFile(path string) {
	t := a.current()
	if t == nil {
		return
	}
	root := t.Workspace
	if workspaceFileKindForPath(path) == workspaceFileProperties {
		a.releaseFilePreview()
		a.openFileInfo(path)
		return
	}
	if old := a.workspaceFiles[workspaceKey(root)]; old != nil {
		if old.Path == path {
			return
		}
		if old.Saving {
			a.notify("文件正在保存，请稍后打开其他文件")
			return
		}
		if old.Editor.Buffer.Modified() {
			a.confirmDiscardFile(root, old, func() { a.loadFileReplacing(root, path, old, true) })
			return
		}
	}
	a.loadFileReplacing(root, path, a.workspaceFiles[workspaceKey(root)], false)
}

func workspaceKey(root string) string { return strings.ToLower(filepath.Clean(root)) }

func (a *desktop) activeFile() *workspaceFile {
	t := a.current()
	if t == nil {
		return nil
	}
	return a.workspaceFiles[workspaceKey(t.Workspace)]
}

func (a *desktop) loadFileReplacing(root, path string, expected *workspaceFile, discardConfirmed bool) {
	kind := workspaceFileKindForPath(path)
	ctx, load := a.workspacePreview.beginLoad(root, path)
	epoch := a.fileEpoch
	expectedVersion := uint64(0)
	if expected != nil && expected.Editor != nil && expected.Editor.Buffer != nil {
		expectedVersion = expected.Editor.Buffer.Version()
	}
	go func() {
		req := backend.WorkspacePathRequest{Workspace: root, Path: path}
		var newFile *workspaceFile
		var previewErr error
		var assist *editorAssistIndex
		var loadedText string
		if kind == workspaceFileDocument {
			doc, err := a.core.ReadWorkspaceDocument(ctx, req)
			previewErr = err
			if err == nil {
				newFile = &workspaceFile{Path: path, Document: doc}
			}
		} else if kind == workspaceFileSVG {
			source, err := a.core.ReadWorkspaceSVGAt(ctx, req)
			previewErr = err
			if err == nil {
				document, renderErr := svgPreviewHTML(path, source)
				previewErr = renderErr
				if renderErr == nil {
					newFile = &workspaceFile{Path: path, PreviewHTML: document}
				}
			}
		} else if kind.isDirectWebViewFile() {
			localPath, err := a.core.GetWorkspaceMediaPath(req)
			previewErr = err
			if err == nil {
				newFile = &workspaceFile{Path: path, PreviewPath: localPath}
			}
		} else {
			r, err := a.core.ReadWorkspaceFileAt(req)
			previewErr = err
			if err == nil {
				assist = buildEditorAssistIndex(path, r.Content, 0)
				loadedText = r.Content
				newFile = &workspaceFile{Path: path, Version: r.Version}
			}
		}
		if ctx.Err() != nil {
			if newFile != nil {
				releaseWorkspaceFileValue(newFile)
			}
			return
		}
		a.win.Update(func() {
			if ctx.Err() != nil {
				if newFile != nil {
					releaseWorkspaceFileValue(newFile)
				}
				return
			}
			if !a.workspacePreview.finishLoad(load) {
				if newFile != nil {
					releaseWorkspaceFileValue(newFile)
				}
				return
			}
			if epoch != a.fileEpoch || a.closing {
				if newFile != nil {
					releaseWorkspaceFileValue(newFile)
				}
				return
			}
			if !a.workspaceFileLoadStillCurrent(root, expected, expectedVersion, discardConfirmed) {
				if newFile != nil {
					releaseWorkspaceFileValue(newFile)
				}
				return
			}
			if previewErr != nil {
				if kind.hasWebViewPreview() {
					if expected != nil {
						a.releaseWorkspaceFile(root)
					}
					if a.workspaceFiles == nil {
						a.workspaceFiles = map[string]*workspaceFile{}
					}
					newFile = &workspaceFile{Path: path, PreviewError: previewErr.Error(), Editor: emptyFileEditor()}
					a.workspaceFiles[workspaceKey(root)] = newFile
					a.notifyError("文件预览失败：" + previewErr.Error())
					return
				}
				if note := backend.WorkspaceFilePreviewNote(previewErr); note != "" {
					a.openFileInfoWithNote(path, note)
				} else {
					a.notifyError("打开文件失败：" + previewErr.Error())
				}
				return
			}
			if a.workspaceFiles == nil {
				a.workspaceFiles = map[string]*workspaceFile{}
			}
			if newFile == nil {
				a.notifyError("打开文件失败：未返回文件内容")
				return
			}
			if kind.isPreviewOnly() {
				newFile.Editor = emptyFileEditor()
			} else {
				buffer := editor.NewBuffer(loadedText)
				buffer.SetLanguage(editor.LanguageFor(path))
				newFile.Editor = &editor.State{Buffer: buffer, Font: ui.Font{Family: "Inter", Size: fsEditor}, Label: "文件编辑器", NoScrollPastEnd: true}
				newFile.Assist = assist
			}
			if newFile.Document != nil {
				if len(newFile.Document.Sheets) > 0 {
					newFile.DocumentSheet = newFile.Document.Sheets[0].Name
				}
				// Keep parsed document previews to one open file at a time.
				for key, old := range a.workspaceFiles {
					if old != newFile && old != expected && old.Document != nil {
						a.workspacePreview.releaseMountedWorkspace(key)
						delete(a.workspaceFiles, key)
						releaseWorkspaceFileValue(old)
					}
				}
			}
			if expected != nil {
				a.releaseWorkspaceFile(root)
			}
			a.workspaceFiles[workspaceKey(root)] = newFile
			if !kind.isPreviewOnly() {
				a.configureEditorAssist(root, newFile)
			}
			if expected != nil {
				a.notifySuccess("文件已刷新")
			}
		})
	}()
}

func (a *desktop) workspaceFileLoadStillCurrent(root string, expected *workspaceFile, version uint64, discardConfirmed bool) bool {
	if a.workspaceFiles[workspaceKey(root)] != expected {
		return false
	}
	if expected == nil {
		return true
	}
	if expected.Saving || expected.Editor == nil || expected.Editor.Buffer == nil {
		a.notify("文件状态已变化，已取消加载")
		return false
	}
	if expected.Editor.Buffer.Version() != version {
		a.notify("文件加载期间内容已变化，已取消替换")
		return false
	}
	if expected.Editor.Buffer.Modified() && !discardConfirmed {
		a.notify("文件有未保存修改，已取消替换")
		return false
	}
	return true
}

func (a *desktop) confirmDiscardFile(root string, file *workspaceFile, action func()) {
	go func() {
		result, err := mygo.Dialog.Message(mygo.MessageOptions{Parent: a.win, Type: mygo.MessageWarning, Title: "未保存的文件", Message: "放弃对 " + file.Path + " 的修改？", Buttons: []string{"取消", "放弃修改"}, CancelButton: 0})
		a.win.Update(func() {
			if err != nil {
				a.notifyError(err.Error())
				return
			}
			if result.Button == 1 && a.workspaceFiles[workspaceKey(root)] == file {
				action()
			}
		})
	}()
}

func (a *desktop) closeFile() {
	a.workspacePreview.cancelLoad("")
	t := a.current()
	file := a.activeFile()
	if t == nil || file == nil {
		return
	}
	if file.Saving {
		a.notify("文件正在保存，请稍后关闭")
		return
	}
	root := t.Workspace
	close := func() { a.releaseWorkspaceFile(root) }
	if file.Editor.Buffer.Modified() {
		a.confirmDiscardFile(root, file, close)
	} else {
		close()
	}
}

func (a *desktop) saveFile() {
	t := a.current()
	file := a.activeFile()
	if t == nil || file == nil || file.Editor == nil || file.Editor.Buffer == nil || workspaceFileKindForPath(file.Path).isPreviewOnly() || file.Saving || !file.Editor.Buffer.Modified() {
		return
	}
	text := file.Editor.Buffer.String()
	file.Saving = true
	req := backend.SaveWorkspaceFileRequest{Workspace: t.Workspace, Path: file.Path, Version: file.Version, Content: text}
	go func() {
		result, err := a.core.SaveWorkspaceFile(req)
		a.win.Update(func() {
			file.Saving = false
			if err != nil {
				a.notifyError("保存失败：" + err.Error())
				return
			}
			file.Version = result.Version
			if file.Editor.Buffer.String() == text {
				file.Editor.Buffer.MarkSaved()
			}
			a.notify("文件已保存")
		})
	}()
}

// Load a bounded history outside the view; never perform disk IO in key handlers.
func (a *desktop) loadWorkspaceInputHistory(t *conversation) {
	if a.core == nil || a.win == nil {
		return
	}
	entries := a.workspaceSessions(t.Workspace)
	if len(entries) > 30 {
		entries = entries[:30]
	}
	go func() {
		var groups [][]string
		total := 0
		for _, entry := range entries {
			snap, err := a.core.LoadSession(entry.ID)
			if err != nil {
				continue
			}
			var prompts []string
			for _, m := range snap.Messages {
				if str(m, "role") == "user" && strings.TrimSpace(str(m, "content")) != "" {
					prompts = append(prompts, str(m, "content"))
				}
			}
			groups = append(groups, prompts)
			total += len(prompts)
			if total >= 200 {
				break
			}
		}
		var history []string
		for n := len(groups) - 1; n >= 0; n-- {
			history = append(history, groups[n]...)
		}
		if len(history) > 200 {
			history = history[len(history)-200:]
		}
		a.win.Update(func() { t.uiState().WorkspaceHistory = history })
	}()
}
