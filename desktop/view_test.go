// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"fmt"
	"strings"
	"testing"
	"time"

	backend "ally-dev/internal/app"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

func TestNativeWorkbench(t *testing.T) {
	a := demoDesktop()
	tt := ui.NewTester(a.view, 1280, 850)
	for _, label := range []string{"Ally", "消息输入框", "任务中心", "设置"} {
		if _, ok := tt.Find(label); !ok {
			t.Errorf("missing %q", label)
		}
	}
	if err := tt.Click("打开工作区文件浏览器"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !a.explorer {
		t.Fatal("explorer toggle did not apply")
	}
	if _, ok := tt.Find("go.mod"); !ok {
		t.Fatal("file tree missing from explorer")
	}
	tt.SetSize(900, 620)
	tt.Frame()
	tt.SetSize(1500, 950)
	tt.Frame()
}

func TestStreamAndToolsKeepSessionIdentity(t *testing.T) {
	a := demoDesktop()
	target := a.current()
	other := &conversation{ID: "other"}
	a.tabs = append(a.tabs, other)
	a.applyEvent("run:stream", map[string]any{"sessionId": target.ID, "content": "继续"})
	if got := str(target.Messages[len(target.Messages)-1], "content"); len(got) == 0 {
		t.Fatal("stream lost")
	}
	a.applyEvent("tool:start", map[string]any{"sessionId": target.ID, "toolCallId": "id-1", "name": "read", "args": "a"})
	a.applyEvent("tool:update", map[string]any{"sessionId": target.ID, "toolCallId": "id-1", "name": "read", "args": "abc"})
	a.applyEvent("tool:result", map[string]any{"sessionId": target.ID, "toolCallId": "id-1", "result": "ok"})
	count := 0
	for _, m := range target.Messages {
		if str(m, "toolCallId") == "id-1" {
			count++
			if str(m, "result") != "ok" {
				t.Fatal("lost result")
			}
		}
	}
	if count != 1 {
		t.Fatalf("duplicate cards: %d", count)
	}
	a.applyEvent("run:stream", map[string]any{"sessionId": target.ID, "content": "结果"})
	if str(target.Messages[len(target.Messages)-1], "role") != "assistant" {
		t.Fatal("tool and text merged")
	}
	if len(other.Messages) != 0 {
		t.Fatal("cross-session event")
	}
	copied := snapshot(target)
	target.Messages[0]["content"] = "mutated"
	if str(copied.Messages[0], "content") == "mutated" {
		t.Fatal("snapshot aliases live data")
	}
}

func TestPlanAndTokenStatsRender(t *testing.T) {
	a := demoDesktop()
	tt := ui.NewTester(a.view, 1280, 850)
	a.applyEvent("plan:update", map[string]any{"sessionId": a.current().ID, "plan": []any{
		map[string]any{"title": "梳理页面", "status": "done"},
		map[string]any{"title": "补齐模态框", "status": "in_progress"},
	}})
	tt.Frame()
	if !tt.HasText("梳理页面") || !tt.HasText("补齐模态框") {
		t.Fatal("plan panel missing")
	}
}

func TestModelPageOpensEditorAndImageTab(t *testing.T) {
	a := demoDesktop()
	a.page = "models"
	tt := ui.NewTester(a.view, 1200, 800)
	tt.Frame()
	if err := tt.Click("编辑"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.modelDraft.Model != "gpt" {
		t.Fatal("wrong edit target")
	}
	if _, ok := tt.Find("API Key 1"); !ok {
		t.Fatal("model editor did not open")
	}
	a.modelForm.Open = false
	tt.Frame()
	if err := tt.Click("图片模型"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("尚未配置生图模型") {
		t.Fatal("image models tab missing")
	}
}

func TestComposerKeepsDraftAndMatchesEnterBehavior(t *testing.T) {
	a := demoDesktop()
	tab := a.current()
	tab.Model = ""
	tab.Draft = "草稿"
	tt := ui.NewTester(a.view, 1280, 850)
	if err := tt.Click("消息输入框"); err != nil {
		t.Fatal(err)
	}
	tt.Key(0, ui.KeyEnter)
	if tab.Draft != "草稿" || !strings.Contains(a.status, "模型") {
		t.Fatalf("failed send lost draft: %q, %q", tab.Draft, a.status)
	}
	tt.Key(ui.Ctrl, ui.KeyEnter)
	if !strings.Contains(tab.Draft, "\n") {
		t.Fatalf("ctrl-enter did not insert newline: %q", tab.Draft)
	}
	tt.Compose("拼", 1)
	tt.Key(0, ui.KeyEnter)
	if !strings.Contains(tab.Draft, "草稿") {
		t.Fatal("IME confirmation sent or lost draft")
	}
}

func TestTreeExpandAndNewSessionKeepDraft(t *testing.T) {
	a := demoDesktop()
	a.explorer = true
	tt := ui.NewTester(a.view, 1280, 850)
	if _, ok := tt.Find("app"); ok {
		t.Fatal("collapsed child visible")
	}
	if err := tt.RightClick("internal"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("复制相对路径"); err != nil {
		t.Fatal(err)
	}
	if tt.Clipboard() != "internal" {
		t.Fatalf("copied path = %q", tt.Clipboard())
	}
	if err := tt.Click("internal"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if _, ok := tt.Find("app"); !ok {
		t.Fatal("expanded child missing")
	}
	if err := tt.Click("app"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if _, ok := tt.Find("app.go"); !ok {
		t.Fatal("nested file missing")
	}
	if err := tt.Click("internal"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if _, ok := tt.Find("app.go"); ok {
		t.Fatal("collapsed descendant visible")
	}
	previous := a.current()
	previous.Draft = "保留草稿"
	if err := tt.Click("新建会话"); err != nil {
		t.Fatal(err)
	}
	if len(a.tabs) != 1 || a.current() == previous || a.openSessions[previous.ID].Draft != "保留草稿" {
		t.Fatal("new session changed workspace tabs or lost draft")
	}
	tt.Frame()
}

func TestSettingsSectionsRender(t *testing.T) {
	a := demoDesktop()
	a.page = "settings"
	tt := ui.NewTester(a.view, 1200, 800)
	if !tt.HasText("外观") || !tt.HasText("提示词") {
		t.Fatal("general settings missing")
	}
	if err := tt.Click("网络"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("网络代理") {
		t.Fatal("network controls missing")
	}
	if err := tt.Click("API"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("/api/v1/sessions") {
		t.Fatal("api endpoints missing")
	}
}

func TestSSHPageEditor(t *testing.T) {
	a := demoDesktop()
	a.page = "ssh"
	a.sshServers = []backend.SSHServerNode{{ID: "test", Alias: "dev", Host: "host", Port: 22, Username: "user", AuthType: "agent"}}
	tt := ui.NewTester(a.view, 1200, 800)
	if !tt.HasText("dev") {
		t.Fatal("configured server missing")
	}
	if err := tt.Click("编辑"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.sshF.Draft.Alias != "dev" {
		t.Fatal("ssh draft not loaded")
	}
	if _, ok := tt.Find("昵称"); !ok {
		t.Fatal("ssh editor missing")
	}
}

func TestMCPServerAndToolViews(t *testing.T) {
	a := demoDesktop()
	a.page = "mcp"
	a.mcpText = `{"mcpServers":{"demo":{"command":"server"}}}`
	a.toolInventory = []backend.ToolDefinitionSummary{
		{Name: "search", Source: "builtin", Enabled: true},
		{Name: "read", Source: "builtin", Enabled: true, Protected: true},
	}
	tt := ui.NewTester(a.view, 1200, 800)
	if !tt.HasText("demo") {
		t.Fatal("configured server missing")
	}
	a.mcpF.Tab = 1
	tt.Frame()
	tt.Frame()
	if !tt.HasText("search") || !tt.HasText("核心工具") {
		t.Fatal("builtin tools missing")
	}
	a.mcpF.Tab = 0
	tt.Frame()
	if err := tt.Click("编辑"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if _, ok := tt.Find("编辑 MCP 服务"); !ok {
		t.Fatal("MCP config editor missing")
	}
}

func TestNativeFileEditorAndNarrowLayout(t *testing.T) {
	a := demoDesktop()
	key := workspaceKey(a.current().Workspace)
	buffer := editor.NewBuffer("package main\n")
	a.workspaceFiles[key] = &workspaceFile{Path: "main.go", Version: "test", Editor: &editor.State{Buffer: buffer, Label: "文件编辑器"}}
	a.explorer = true
	tt := ui.NewTester(a.view, 1280, 850)
	if _, ok := tt.Find("文件编辑器"); !ok {
		t.Fatal("editor missing")
	}
	if err := tt.Click("文件编辑器"); err != nil {
		t.Fatal(err)
	}
	tt.Type("x")
	if !buffer.Modified() {
		t.Fatal("editor did not retain edits")
	}
	file := a.activeFile()
	file.Saving = true
	tt.Frame()
	before := buffer.String()
	tt.Type("not-saved-yet")
	if buffer.String() != before {
		t.Fatal("editor changed while a save was in progress")
	}
	file.Saving = false
	tt.SetSize(900, 620)
	tt.Frame()
	if _, ok := tt.Find("文件编辑器"); !ok {
		t.Fatal("editor vanished at minimum size")
	}
	if _, ok := tt.Find("关闭编辑器"); !ok {
		t.Fatal("editor close action missing")
	}
}

func TestTaskCenterAndStatisticsPages(t *testing.T) {
	a := demoDesktop()
	a.page = "tasks"
	a.services = []backend.ServiceInfo{{ID: "s1", Name: "开发服务", Status: "running", Command: "go run .", Workspace: a.current().Workspace, OutputTail: "ready"}}
	a.tasks = []backend.ScheduledTask{{ID: "t1", Name: "定时任务", Workspace: a.current().Workspace, LastSummary: "执行完成"}}
	tt := ui.NewTester(a.view, 1280, 850)
	if !tt.HasText("开发服务") {
		t.Fatal("service missing")
	}
	if err := tt.Click("定时任务 (0/1)"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("定时任务") {
		t.Fatal("scheduled task missing")
	}
	a.tasksF.LogOpen = true
	a.tasksF.LogTitle = "定时任务 · 完整日志"
	a.tasksF.LogBody = "第一步\n第二步"
	tt.Frame()
	if !tt.HasText("第一步") {
		t.Fatal("task log missing")
	}
	a.tasksF.LogOpen = false
	a.page = "stats"
	a.stats = backend.TokenStatsResult{
		OK:           true,
		SummaryMonth: backend.TokenStatsSummary{TotalTokens: 100, InputTokens: 60, OutputTokens: 40},
		Daily:        []backend.TokenDailyStat{{Date: "2026-10-27", InputTokens: 100, OutputTokens: 40}},
		MonthDaily:   []backend.TokenDailyStat{{Date: "2026-10-01", InputTokens: 20}},
		ModelWeek:    []backend.TokenDimensionStat{{Name: "model", InputTokens: 100}},
	}
	tt.Frame()
	if !tt.HasText("近 30 天用量") || !tt.HasText("当月热力图") {
		t.Fatal("statistics charts missing")
	}
	tt.SetSize(900, 620)
	tt.Frame()
}

func TestGamesPageRendersBoard(t *testing.T) {
	a := demoDesktop()
	a.page = "games"
	tt := ui.NewTester(a.view, 1280, 850)
	for _, label := range []string{"五子棋", "围棋", "象棋", "斗地主"} {
		if !tt.HasText(label) {
			t.Fatalf("missing game %q", label)
		}
	}
	if err := tt.Click("开始对局"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !a.gameF.Started {
		t.Fatal("game did not start")
	}
	if _, ok := tt.Find("棋盘"); !ok {
		t.Fatal("board missing")
	}
}

func TestChatPopoversAndSSHAuthorizationRender(t *testing.T) {
	a := demoDesktop()
	a.workspaceMenu = true
	a.current().uiState().ModelOpen = true
	a.current().uiState().FooterLoaded = true
	a.current().uiState().ContextOpen = true
	a.sshF.AuthOpen = true
	a.sshF.AuthReady = true
	tt := ui.NewTester(a.view, 1280, 850)
	tt.Frame()
	for _, label := range []string{"临时工作区", "管理模型", "上下文构成", "SSH 集群授权"} {
		if !tt.HasText(label) {
			t.Errorf("missing %q", label)
		}
	}
	a.workspaceMenu = false
	a.current().uiState().ModelOpen = false
	a.current().uiState().ContextOpen = false
	a.sshF.AuthOpen = false
	tt.Frame()
	tt.Frame()
}

func TestComposerPanelsAndSettingsNavigation(t *testing.T) {
	a := demoDesktop()
	a.prefs.RailExpanded = true
	st := a.current().uiState()
	st.ModelOpen, st.EffortOpen = true, true
	a.taskPopup = true
	tt := ui.NewTester(a.view, 1280, 850)
	tt.Frame()
	for _, label := range []string{"搜索模型或供应商", "最近使用", "思考强度", "后台服务", "工作区会话"} {
		if !tt.HasText(label) {
			t.Errorf("missing %q", label)
		}
	}
	if a.page != "chat" {
		t.Fatalf("task popup changed page: %q", a.page)
	}
	st.ModelOpen, st.EffortOpen, a.taskPopup = false, false, false
	a.page = "settings"
	tt.Frame()
	if tt.HasText("保存设置") {
		t.Error("settings should save on leaving")
	}
}

func TestPaneDividersResize(t *testing.T) {
	a := demoDesktop()
	a.prefs.RailExpanded = true
	a.explorer = true
	tt := ui.NewTester(a.view, 1280, 850)
	tt.Frame()
	beforeRail := a.prefs.RailWidth
	tt.Press(beforeRail+2, 500)
	tt.Move(beforeRail+32, 500)
	tt.Release(beforeRail+32, 500)
	tt.Frame()
	if a.prefs.RailWidth <= beforeRail {
		t.Fatalf("rail did not resize: %v -> %v", beforeRail, a.prefs.RailWidth)
	}
	beforeTree := a.prefs.TreeWidth
	x := float32(1280) - beforeTree - 3
	tt.Press(x, 500)
	tt.Move(x-30, 500)
	tt.Release(x-30, 500)
	tt.Frame()
	if a.prefs.TreeWidth <= beforeTree {
		t.Fatalf("tree did not resize: %v -> %v", beforeTree, a.prefs.TreeWidth)
	}
}

func TestWelcomePopoverUsesTheme(t *testing.T) {
	a := demoDesktop()
	a.current().Messages = nil
	a.prefs.Palette = 1
	a.welcomeMCP, a.welcomeSkills, a.welcomeTools = true, true, true
	tt := ui.NewTester(a.view, 1280, 850)
	tt.Frame()
	for _, text := range []string{"管理 MCP 服务", "管理技能", "管理内置工具"} {
		if !tt.HasText(text) {
			t.Errorf("welcome popover missing %q", text)
		}
	}
}

func TestPaneWidthsPersist(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	t.Setenv("ALLY_DATA_DIR", dir)
	a := &desktop{core: backend.NewApp(), prefs: defaultPrefs()}
	a.prefs.RailWidth, a.prefs.TreeWidth = 275, 325
	a.savePrefs()
	<-a.prefsSaveTail
	restored := &desktop{core: a.core}
	restored.loadPrefs()
	if restored.prefs.RailWidth != 275 || restored.prefs.TreeWidth != 325 {
		t.Fatalf("pane widths were not restored: %+v", restored.prefs)
	}
}

func TestModelProviderTabsSwitch(t *testing.T) {
	a := demoDesktop()
	a.page = "models"
	a.config.Models = append(a.config.Models, backend.ModelConfig{ProviderName: "Another Provider", Model: "another-model"})
	tt := ui.NewTester(a.view, 1280, 850)
	if err := tt.Click("Another Provider"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.modelProvider != "Another Provider" || !tt.HasText("another-model") {
		t.Fatalf("provider tab did not switch: %q", a.modelProvider)
	}
}

func TestRailSectionHeadersToggle(t *testing.T) {
	a := demoDesktop()
	a.prefs.RailExpanded = true
	tt := ui.NewTester(a.view, 1280, 850)
	for _, item := range []struct {
		label string
		state *bool
	}{{"历史工作区", &a.prefs.RailWorkspaceOpen}, {"工作区会话", &a.prefs.RailSessionsOpen}} {
		if !*item.state {
			t.Fatalf("%s starts collapsed", item.label)
		}
		if err := tt.Click(item.label); err != nil {
			t.Fatal(err)
		}
		tt.Frame()
		if *item.state {
			t.Fatalf("%s did not collapse", item.label)
		}
		if err := tt.Click(item.label); err != nil {
			t.Fatal(err)
		}
		tt.Frame()
		if !*item.state {
			t.Fatalf("%s did not expand", item.label)
		}
	}
}

func TestRailSectionHeadersStayFixed(t *testing.T) {
	a := demoDesktop()
	a.prefs.RailExpanded = true
	root := a.config.Workspace
	for i := 0; i < 12; i++ {
		a.sessions = append(a.sessions, backend.SessionIndexEntry{
			ID:        fmt.Sprintf("ws-%02d", i),
			Workspace: fmt.Sprintf("D:/projects/ws-%02d", i),
			Title:     fmt.Sprintf("项目 %02d", i),
		})
	}
	for i := 0; i < 24; i++ {
		a.sessions = append(a.sessions, backend.SessionIndexEntry{
			ID:        fmt.Sprintf("cur-%02d", i),
			Workspace: root,
			Title:     fmt.Sprintf("会话 %02d", i),
		})
	}
	tt := ui.NewTester(a.view, 1280, 850)
	tt.Frame()
	before, ok := tt.Find("工作区会话")
	if !ok {
		t.Fatal("missing sessions header")
	}
	// Only the list items scroll; section headings stay visible.
	tt.Scroll(110, 600, 0, 260)
	tt.Frame()
	after, ok := tt.Find("工作区会话")
	if !ok {
		t.Fatal("sessions header vanished after scrolling")
	}
	if after.Y != before.Y {
		t.Fatalf("sessions header moved while the list scrolled: %.1f -> %.1f", before.Y, after.Y)
	}
}

func TestFormDialogHeaderAndActionsStayVisible(t *testing.T) {
	a := demoDesktop()
	a.page = "skills"
	a.skillsF.AddOpen, a.skillsF.AddTarget = true, "user"
	tt := ui.NewTester(a.view, 900, 620)
	tt.Frame()
	header, ok := tt.Find("添加技能")
	if !ok {
		t.Fatal("missing dialog header")
	}
	footer, ok := tt.Find("保存")
	if !ok {
		t.Fatal("missing dialog save button")
	}
	tt.Scroll(450, 350, 0, 700)
	tt.Frame()
	nextHeader, ok := tt.Find("添加技能")
	if !ok || nextHeader.Y != header.Y {
		t.Fatal("dialog header moved when scrolling")
	}
	nextFooter, ok := tt.Find("保存")
	if !ok || nextFooter.Y != footer.Y {
		t.Fatal("dialog footer moved when scrolling")
	}
}

func TestSkillDialogLocationSelection(t *testing.T) {
	a := demoDesktop()
	a.page = "skills"
	a.skillsF.AddOpen, a.skillsF.AddTarget = true, "user"
	tt := ui.NewTester(a.view, 1000, 850)
	if err := tt.Click("项目级"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.skillsF.AddTarget != "project" {
		t.Fatalf("location not selected: %q", a.skillsF.AddTarget)
	}
}

func TestMCPDialogTransportChanges(t *testing.T) {
	a := demoDesktop()
	a.page = "mcp"
	a.mcpF.EditorOpen, a.mcpF.Transport = true, "stdio"
	tt := ui.NewTester(a.view, 1000, 850)
	if err := tt.Click("stdio"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("sse"); err != nil {
		t.Fatalf("%v; texts=%v", err, tt.Texts())
	}
	tt.Frame()
	if a.mcpF.Transport != "sse" || !tt.HasText("URL") {
		t.Fatalf("transport did not change: %q", a.mcpF.Transport)
	}
}

func TestEditorFieldsFollowOriginalColumns(t *testing.T) {
	a := demoDesktop()
	a.page = "mcp"
	a.mcpF.EditorOpen, a.mcpF.Transport = true, "stdio"
	tt := ui.NewTester(a.view, 1000, 850)
	name, ok1 := tt.Find("服务名称")
	transport, ok2 := tt.Find("传输方式")
	if !ok1 || !ok2 || name.Y != transport.Y || name.X >= transport.X {
		t.Fatalf("MCP fields not aligned: %#v %#v", name, transport)
	}
	a.mcpF.EditorOpen = false
	a.page = "models"
	a.editModel(-1)
	a.modelForm.Open = true
	tt.Frame()
	provider, ok1 := tt.Find("Provider 名称")
	format, ok2 := tt.Find("接口格式")
	if !ok1 || !ok2 || provider.Y != format.Y || provider.X >= format.X {
		t.Fatalf("model fields not aligned: %#v %#v", provider, format)
	}
}

func TestModelEditorAuthSelection(t *testing.T) {
	a := demoDesktop()
	a.page = "models"
	a.editModel(-1)
	a.modelForm.Open = true
	tt := ui.NewTester(a.view, 1000, 850)
	if err := tt.Click("API Key 认证"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("ChatGPT 登录"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.modelDraft.AuthType != "chatgpt_oauth" {
		t.Fatalf("auth type = %q", a.modelDraft.AuthType)
	}
}

func TestTabsWheelScrollsHorizontally(t *testing.T) {
	selected := 0
	labels := []string{"Alpha", "Bravo", "Charlie", "Delta", "Echo", "Foxtrot", "Golf", "Hotel", "India"}
	tt := ui.NewTester(func(c *ui.Context) { ui.Column(c).Fill().Children(func() { scrollTabs(c, &selected, labels...) }) }, 220, 120)
	before, ok := tt.Find("Alpha")
	if !ok {
		t.Fatal("first tab missing")
	}
	tt.Scroll(100, 15, 0, 140)
	tt.Frame()
	after, ok := tt.Find("Bravo")
	if !ok {
		t.Fatal("second tab missing after scrolling")
	}
	if after.X >= before.X+80 {
		t.Fatalf("wheel did not move tabs: before=%#v after=%#v", before, after)
	}
}

func TestComposerCtrlEnterKeepsCaretAndHasNoSendButton(t *testing.T) {
	a := demoDesktop()
	tab := a.current()
	tab.Draft = "abcd"
	tt := ui.NewTester(a.view, 1280, 850)
	if err := tt.Click("消息输入框"); err != nil {
		t.Fatal(err)
	}
	tt.Key(0, ui.KeyEnd)
	tt.Key(0, ui.KeyLeft)
	tt.Key(0, ui.KeyLeft)
	tt.Key(ui.Ctrl, ui.KeyEnter)
	tt.Type("X")
	if tab.Draft != "ab\nXcd" {
		t.Fatalf("Ctrl+Enter lost caret: %q", tab.Draft)
	}
	if tt.HasText("发送 ↑") {
		t.Fatal("composer should not render a send button")
	}
}

func TestEscapeQueuesCancellationBeforeRunIDArrives(t *testing.T) {
	a := demoDesktop()
	tab := a.current()
	tab.Busy = true
	tt := ui.NewTester(a.view, 1280, 850)
	tt.Key(0, ui.KeyEscape)
	if !tab.CancelRequested || tab.Status != "正在取消…" {
		t.Fatalf("cancellation was lost: %+v", tab)
	}
}

func TestGlobalNotificationsAppearOnce(t *testing.T) {
	a := demoDesktop()
	a.notify("设置已自动保存")
	a.notify("设置已自动保存")
	if len(a.notifications) != 1 {
		t.Fatal("duplicate notification queued")
	}
	tt := ui.NewTester(a.view, 1280, 850)
	tt.Frame()
	if !tt.HasText("设置已自动保存") || len(a.notifications) != 0 {
		t.Fatal("global notification was not shown and drained")
	}
}

func TestRememberSessionReleasesDerivedCaches(t *testing.T) {
	a := demoDesktop()
	tab := a.current()
	tab.AttachmentImageVersions = map[string]uint64{"image": 1}
	a.rememberSession(tab)
	if tab.MD != nil || tab.AttachmentImages != nil || tab.AttachmentImageVersions["image"] != 2 {
		t.Fatal("derived caches were not released or background image load remained valid")
	}
	if len(tab.Messages) == 0 {
		t.Fatal("releasing cache lost conversation history")
	}
}

func TestEscapeClosesDialogBeforeCancellingRun(t *testing.T) {
	a := demoDesktop()
	a.current().Busy = true
	a.page = "skills"
	a.skillsF.AddOpen, a.skillsF.AddTarget = true, "user"
	tt := ui.NewTester(a.view, 1000, 850)
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if a.skillsF.AddOpen {
		t.Fatal("Escape did not close dialog")
	}
	if a.current().CancelRequested {
		t.Fatal("Escape cancelled run through an open dialog")
	}
}

func TestDailyChartsShareRowAndBarsShowTooltip(t *testing.T) {
	a := demoDesktop()
	a.page = "stats"
	a.stats.SummaryMonth.TotalTokens = 100
	a.stats.Daily = []backend.TokenDailyStat{{Date: "2026-10-26", InputTokens: 60, OutputTokens: 40}}
	tt := ui.NewTester(a.view, 1280, 850)
	bars, ok1 := tt.Find("近 30 天用量")
	heatmap, ok2 := tt.Find("当月热力图")
	if !ok1 || !ok2 || absFloat(bars.Y-heatmap.Y) > 1 || bars.X >= heatmap.X {
		t.Fatalf("charts do not share a row: %#v %#v", bars, heatmap)
	}
	plot, ok := tt.Find("近30天用量图")
	if !ok {
		t.Fatal("bar plot missing")
	}
	tt.Move(plot.X+50, plot.Y+80)
	tt.Advance(time.Second)
	if !tt.HasText("2026-10-26 · 输入 60 · 输出 40 · 合计 100") {
		t.Fatalf("bar tooltip missing: %v", tt.Texts())
	}
}

func absFloat(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}
