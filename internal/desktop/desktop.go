// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
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
	Scroll                                            ui.ScrollState
	PendingAttachments                                []backend.AttachmentInput
	AttachmentLoading                                 bool
	AttachmentImages                                  map[string]*ui.Bitmap
	AttachmentImageVersions                           map[string]uint64
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
	Image         *ui.Bitmap
	Saving        bool
}
type desktop struct {
	customAvatar                              *ui.Bitmap
	avatarStyle                               string
	avatarPoses                               map[string]*ui.Bitmap
	avatarLoadID                              uint64
	configSaveTail                            chan struct{}
	prefsSaveTail                             chan struct{}
	railResizing, treeResizing                bool
	welcomeMCP, welcomeSkills, welcomeTools   bool
	popupFlags                                map[string]*bool
	notifications                             []string
	workspaceMenu                             bool
	taskPopup                                 bool
	petals                                    bool
	ShowAllMessages                           bool
	modelSearch                               string
	modelTabIdx                               int
	modelProvider                             string
	modelProviderPreset                       string
	extraRoots                                map[string][]string
	sshAllowed                                map[string][]string
	prefs                                     uiPrefs
	settings                                  settingsForm
	modelForm                                 modelForm
	mcpF                                      mcpForm
	skillsF                                   skillsForm
	sshF                                      sshForm
	tasksF                                    tasksForm
	pluginsF                                  pluginsForm
	upd                                       updateUI
	filesF                                    fileUI
	gameF                                     gameUI
	plans                                     map[string][]backend.PlanStep
	planRev                                   map[string]int64
	subRuns                                   map[string]*backend.SubagentRun
	railExpanded                              bool
	plugins                                   []backend.PluginInfo
	pluginBusy                                bool
	pluginSeq                                 uint64
	pluginDelete                              *backend.PluginInfo
	pluginDeleteOpen, pluginPurge             bool
	pluginGroupOpen                           map[string]bool
	fileOpen                                  map[string]bool
	fileChildren                              map[string][]backend.FileEntry
	fileLoading                               map[string]bool
	openSessions                              map[string]*conversation
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
	page, status, filter                                string
	config                                              backend.ConfigState
	tabs                                                []*conversation
	active                                              int
	sessions                                            []backend.SessionIndexEntry
	explorer                                            bool
	files                                               []backend.FileEntry
	workspaceFiles                                      map[string]*workspaceFile
	fileLoadID                                          uint64
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
	mygo.App.SetName("Ally Native")
	a := &desktop{core: backend.NewApp(), page: "chat", modelEdit: -1, explorer: true, status: "正在启动…", plans: map[string][]backend.PlanStep{}, planRev: map[string]int64{}, subRuns: map[string]*backend.SubagentRun{}, prefs: defaultPrefs(), extraRoots: map[string][]string{}, sshAllowed: map[string][]string{}}
	mygo.App.WhenReady(func() {
		a.syncNativeAppearance()
		a.win = mygo.NewWindow(mygo.WindowOptions{
			Title: "Ally", Width: 1280, Height: 850, MinWidth: 900, MinHeight: 620,
			StateKey: "ally-native-main", TitleBarStyle: mygo.TitleBarHidden,
			HideWindowButtons: true, TitleBarHeight: 44,
			Hidden: true, BackgroundColor: "#1b1b1b", Content: ui.View(a.view),
		})
		// Configure the native frame before showing it to avoid a white border
		// during startup, while keeping the native resize edges and shadow.
		configureWindowFrame(a.win)
		a.win.Show()
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
		go func() {
			if logger, _, err := backend.InitErrorLogger(); err == nil {
				a.core.SetErrorLogger(logger)
			}
			prefs := readPrefs(a.prefsPath())
			customAvatar := a.loadCustomAvatar()
			avatarStyle := normalizedAvatarStyle(prefs.Avatar)
			avatarPoses := decodeAvatarStyle(avatarStyle)
			err := a.core.StartNative(context.Background(), a.onEvent)
			if err != nil {
				a.win.Update(func() { a.status = "启动失败：" + err.Error() })
				return
			}
			cfg, err := a.core.GetConfig()
			if err != nil {
				a.fail(err)
				return
			}
			sessions, _ := a.core.ListSessionIndex()
			skills, _ := a.core.ListSkills()
			tools := a.core.ListTools()
			servers := a.core.GetMcpServers()
			plugins, pluginErr := a.core.ListPlugins()
			a.win.Update(func() {
				a.config = cfg
				a.prefs = prefs
				a.syncNativeAppearance()
				a.customAvatar = customAvatar
				a.avatarStyle, a.avatarPoses = avatarStyle, avatarPoses
				a.plugins = plugins
				if pluginErr != nil {
					a.status = "插件列表加载失败：" + pluginErr.Error()
				}
				a.skills = skills
				a.toolInventory = tools
				a.toolCount = enabledToolCount(tools)
				a.mcpServers = servers
				a.mcpSummary = mcpSummary(servers)
				a.sessions = sessions
				a.syncSettingsForm()
				a.ready = true
				a.status = "就绪"
				a.newConversation(cfg.Workspace)
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
	a.closing = true
	for _, file := range a.workspaceFiles {
		if file.Saving {
			a.closing = false
			a.status = "文件正在保存，请稍后退出"
			return
		}
		if file.Editor.Buffer.Modified() {
			go func() {
				result, err := mygo.Dialog.Message(mygo.MessageOptions{Parent: a.win, Type: mygo.MessageWarning, Title: "未保存的文件", Message: "存在未保存的文件修改", Detail: "退出会放弃这些修改。", Buttons: []string{"取消", "放弃并退出"}, CancelButton: 0})
				a.win.Update(func() {
					if err != nil {
						a.notify(err.Error())
						a.closing = false
						return
					}
					if result.Button != 1 {
						a.closing = false
						return
					}
					a.finishShutdown()
				})
			}()
			return
		}
	}
	a.finishShutdown()
}
func (a *desktop) finishShutdown() {
	a.status = "正在保存并退出…"
	go func() {
		err := a.core.ShutdownNative()
		if err != nil {
			log.Printf("shutdown: %v", err)
		}
		a.win.Update(func() {
			for _, t := range a.tabs {
				if len(t.Messages) > 0 {
					a.persist(t)
				}
			}
			tail := a.persistTail
			go func() {
				if tail != nil {
					<-tail
				}
				a.win.Update(func() { a.quitReady = true; mygo.App.Quit() })
			}()
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
	a.tabs = append(a.tabs, t)
	a.active = len(a.tabs) - 1
	a.page = "chat"
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
	return t
}

func (a *desktop) rememberSession(t *conversation) {
	if t == nil {
		return
	}
	if a.openSessions == nil {
		a.openSessions = map[string]*conversation{}
	}
	if !t.Busy && len(t.PendingAttachments) == 0 {
		t.MD = nil
		for id := range t.AttachmentImageVersions {
			t.AttachmentImageVersions[id]++
		}
		t.AttachmentImages = nil
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
	if t.Busy {
		a.status = "当前会话正在运行，请等待完成后再切换会话"
		return
	}
	a.rememberSession(t)
	next := a.freshConversation(t.Workspace)
	next.Model = t.Model
	next.Reasoning = t.Reasoning
	a.tabs[a.active] = next
	a.page = "chat"
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
			a.active = i
			a.page = "chat"
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
func (a *desktop) notify(message string) {
	if message == "" {
		return
	}
	a.status = message
	if len(a.notifications) >= 8 {
		a.notifications = a.notifications[1:]
	}
	if len(a.notifications) == 0 || a.notifications[len(a.notifications)-1] != message {
		a.notifications = append(a.notifications, message)
	}
}

func (a *desktop) fail(err error) {
	if err != nil {
		a.win.Update(func() { a.notify(err.Error()) })
	}
}
func (a *desktop) job(work func() error) {
	go func() {
		err := work()
		a.win.Update(func() {
			if err != nil {
				a.notify(err.Error())
			} else {
				a.status = "操作完成"
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
			a.win.Update(func() { a.config.Workspace = p; a.openWorkspace(p) })
		}
	}()
}
func (a *desktop) send(t *conversation, text string) bool {
	text = strings.TrimSpace(text)
	if (text == "" && len(t.PendingAttachments) == 0) || t.AttachmentLoading || t.Busy || !a.ready || a.closing {
		return false
	}
	if t.Model == "" {
		a.status = "请先在模型页面配置并选择模型"
		return false
	}
	cfg := a.config
	cfg.Workspace = t.Workspace
	found := false
	for _, m := range cfg.Models {
		if modelOption(m) == t.Model {
			found = true
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
			break
		}
	}
	if !found || cfg.Model == "" {
		a.status = "所选模型已不存在，请重新选择"
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
	raw, _ := json.Marshal(attachments)
	var displayAttachments []any
	_ = json.Unmarshal(raw, &displayAttachments)
	t.Messages = append(t.Messages, map[string]any{"role": "user", "content": display, "attachments": displayAttachments})
	t.PendingAttachments = nil
	t.Busy = true
	a.status = "就绪"
	t.Draft = ""
	t.Status = "正在连接模型…"
	t.Scroll.Y = 1e9
	a.persist(t)
	go func() {
		id, err := a.core.StartChat(backend.ChatRequest{SessionID: t.ID, Message: text, Attachments: attachments, Config: cfg})
		a.win.Update(func() {
			if err != nil {
				t.Busy = false
				t.Status = err.Error()
				a.notify(err.Error())
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
	// JSON makes nested tool payloads independent of subsequent UI mutation.
	data, _ := json.Marshal(t.Messages)
	var msgs []map[string]any
	_ = json.Unmarshal(data, &msgs)
	// Stored image references are durable; avoid copying large base64 payloads
	// into every snapshot once the backend confirms the image was saved.
	for _, m := range msgs {
		for _, att := range messageAttachmentList(m) {
			if str(att, "imageRef") != "" {
				delete(att, "dataUrl")
			}
		}
	}
	return backend.SessionSnapshot{ID: t.ID, Title: t.Title, Workspace: t.Workspace, CreatedAt: t.Created, UpdatedAt: time.Now().UnixMilli(), ContextTokens: t.ContextTokens, HasSnapshot: true, Messages: msgs}
}
func (a *desktop) persist(t *conversation) {
	s := snapshot(t)
	previous := a.persistTail
	done := make(chan struct{})
	a.persistTail = done
	go func() {
		defer close(done)
		if previous != nil {
			<-previous
		}
		if err := a.core.SaveSession(s); err != nil {
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
			a.active = i
			a.page = "chat"
			a.refreshFiles()
			a.refreshSessionFooter(t)
			a.refreshGit(t)
			return
		}
	}
	if t := a.openSessions[id]; t != nil {
		a.showSession(t)
		if t.AttachmentImages == nil && a.win != nil {
			a.loadSessionAttachmentImages(t)
		}
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
			a.showSession(t)
			a.loadSessionAttachmentImages(t)
		})
	}()
}

func (a *desktop) showSession(next *conversation) {
	if a.page == "models" {
		a.cancelOAuthWhenLeaving()
	}
	for i, t := range a.tabs {
		if strings.EqualFold(t.Workspace, next.Workspace) && !t.Busy {
			a.rememberSession(t)
			if next.Model == "" {
				next.Model = t.Model
			}
			a.tabs[i], a.active, a.page = next, i, "chat"
			a.refreshFiles()
			a.refreshSessionFooter(next)
			a.refreshGit(next)
			return
		}
	}
	a.tabs = append(a.tabs, next)
	a.active, a.page = len(a.tabs)-1, "chat"
	a.refreshFiles()
	a.refreshSessionFooter(next)
	a.refreshGit(next)
}

// onEvent is wired to the backend event sink; the dispatcher lives in
// events.go so the chat, task and update streams stay together.
func (a *desktop) onEvent(name string, payload any) { a.dispatchEvent(name, payload) }
func pretty(v any) string                           { b, _ := json.MarshalIndent(v, "", "  "); return string(b) }
func (a *desktop) refreshFiles() {
	t := a.current()
	if t == nil {
		return
	}
	a.fileEpoch++
	epoch := a.fileEpoch
	root := t.Workspace
	a.files = nil
	a.fileOpen = map[string]bool{}
	a.fileChildren = map[string][]backend.FileEntry{}
	a.fileLoading = map[string]bool{}
	a.fileLoadID++
	if root == "" {
		return
	}
	go func() {
		r, e := a.core.ListFiles(backend.ListFilesRequest{Workspace: root, Path: ".", MaxDepth: 1, Limit: 1000, IncludeHidden: true})
		a.win.Update(func() {
			if epoch != a.fileEpoch {
				return
			}
			if e != nil {
				a.status = e.Error()
				return
			}
			a.files = r.Entries
			if r.Truncated {
				a.status = "文件过多，目录列表仅显示前 1000 项"
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
	if old := a.workspaceFiles[workspaceKey(root)]; old != nil {
		if old.Path == path {
			return
		}
		if old.Saving {
			a.status = "文件正在保存，请稍后打开其他文件"
			return
		}
		if old.Editor.Buffer.Modified() {
			a.confirmDiscardFile(root, old, func() { a.loadFile(root, path) })
			return
		}
	}
	a.loadFile(root, path)
}

func workspaceKey(root string) string { return strings.ToLower(filepath.Clean(root)) }

func (a *desktop) activeFile() *workspaceFile {
	t := a.current()
	if t == nil {
		return nil
	}
	return a.workspaceFiles[workspaceKey(t.Workspace)]
}

func (a *desktop) loadFile(root, path string) {
	epoch := a.fileEpoch
	a.fileLoadID++
	loadID := a.fileLoadID
	go func() {
		req := backend.WorkspacePathRequest{Workspace: root, Path: path}
		if isPreviewImage(path) {
			data, err := a.core.ReadNativeWorkspaceImage(req)
			var bitmap *ui.Bitmap
			if err == nil {
				bitmap, err = ui.DecodeBitmap(data)
			}
			a.win.Update(func() {
				if epoch != a.fileEpoch || loadID != a.fileLoadID {
					return
				}
				if err != nil {
					a.notify(err.Error())
					return
				}
				if a.workspaceFiles == nil {
					a.workspaceFiles = map[string]*workspaceFile{}
				}
				a.workspaceFiles[workspaceKey(root)] = &workspaceFile{Path: path, Image: bitmap, Editor: &editor.State{Buffer: editor.NewBuffer("")}}
			})
			return
		}
		r, e := a.core.ReadWorkspaceFileAt(req)
		a.win.Update(func() {
			if epoch != a.fileEpoch || loadID != a.fileLoadID {
				return
			}
			if e != nil {
				a.status = e.Error()
				return
			}
			buffer := editor.NewBuffer(r.Content)
			buffer.SetLanguage(editor.LanguageFor(path))
			if a.workspaceFiles == nil {
				a.workspaceFiles = map[string]*workspaceFile{}
			}
			a.workspaceFiles[workspaceKey(root)] = &workspaceFile{Path: path, Version: r.Version, Editor: &editor.State{Buffer: buffer, Label: "文件编辑器", NoScrollPastEnd: true}}
		})
	}()
}

func (a *desktop) confirmDiscardFile(root string, file *workspaceFile, action func()) {
	go func() {
		result, err := mygo.Dialog.Message(mygo.MessageOptions{Parent: a.win, Type: mygo.MessageWarning, Title: "未保存的文件", Message: "放弃对 " + file.Path + " 的修改？", Buttons: []string{"取消", "放弃修改"}, CancelButton: 0})
		a.win.Update(func() {
			if err != nil {
				a.notify(err.Error())
				return
			}
			if result.Button == 1 && a.workspaceFiles[workspaceKey(root)] == file {
				action()
			}
		})
	}()
}

func (a *desktop) closeFile() {
	t := a.current()
	file := a.activeFile()
	if t == nil || file == nil {
		return
	}
	if file.Saving {
		a.status = "文件正在保存，请稍后关闭"
		return
	}
	root := t.Workspace
	close := func() { delete(a.workspaceFiles, workspaceKey(root)) }
	if file.Editor.Buffer.Modified() {
		a.confirmDiscardFile(root, file, close)
	} else {
		close()
	}
}

func (a *desktop) saveFile() {
	t := a.current()
	file := a.activeFile()
	if t == nil || file == nil || file.Saving || !file.Editor.Buffer.Modified() {
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
				a.status = "保存失败：" + err.Error()
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
