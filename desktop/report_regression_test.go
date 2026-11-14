package desktop

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	backend "ally-dev/internal/app"
	"github.com/egoist/mygo/ui"
)

// A new run takes the reader to the newest message, even from far up the
// history: what it streams lands at the end.
func TestNewRunLeavesOlderHistoryPage(t *testing.T) {
	conv := &conversation{ID: "history", Messages: restoredHistory(200)}
	st := conv.uiState()
	a := &desktop{tabs: []*conversation{conv}}
	tt := ui.NewTester(func(c *ui.Context) { c.SetTheme(a.theme()); a.chatMessages(c, conv, 900) }, 900, 400)
	tt.Frame()
	st.List.ScrollTo(0, ui.Start)
	tt.Frame()
	if st.List.AtEnd() {
		t.Fatal("the transcript did not scroll away from its end")
	}
	a.applyEvent("run:start", map[string]any{"sessionId": conv.ID})
	tt.Frame()
	tt.Frame()
	if !st.List.AtEnd() || conv.Scroll.Y < conv.Scroll.MaxY-2 {
		t.Fatalf("a new run stayed up the history: %v / %v", conv.Scroll.Y, conv.Scroll.MaxY)
	}
}

// A streamed answer is the one row that starts drawing after its index exists:
// history paging has to see it even though the transcript did not grow.
func TestStreamedAnswerOpensHistoryBlock(t *testing.T) {
	conv := &conversation{ID: "stream", Messages: []map[string]any{
		{"role": "user", "content": "问题"},
		{"role": "assistant", "content": ""},
	}}
	a := &desktop{tabs: []*conversation{conv}}
	if starts := conv.blockStarts(); len(starts) != 2 || starts[1] != 2 {
		t.Fatalf("an empty answer opens no block: %v", starts)
	}
	a.applyEvent("run:stream", map[string]any{"sessionId": conv.ID, "content": "答"})
	if starts := conv.blockStarts(); len(starts) != 3 || starts[1] != 1 {
		t.Fatalf("a streamed answer must open a block: %v", starts)
	}
}

// The same error is announced once, whether or not the reader is at the end.
func TestRunErrorAppendsOnce(t *testing.T) {
	conv := &conversation{}
	appendRunError(conv, "多行\n错误", "error")
	appendRunError(conv, "多行\n错误", "error")
	if len(conv.Messages) != 1 {
		t.Fatalf("messages = %v", conv.Messages)
	}
}

func TestCompactReasoningDoesNotChangeTheTranscript(t *testing.T) {
	a := &desktop{}
	conv := &conversation{}
	a.compactDelta(conv, "", 32)
	if len(conv.Messages) != 0 {
		t.Fatal("reasoning-only compaction changed the transcript")
	}
	a.compactDelta(conv, "summary", 0)
	if len(conv.Messages) != 1 {
		t.Fatal("visible compaction content did not reach the transcript")
	}
}

func TestGameCoordinatesMatchPromptAndHumanInput(t *testing.T) {
	for _, tc := range []struct{ kind, size int }{{gameGomoku, 15}, {gameGo, 9}} {
		a := &desktop{}
		a.resetGame(tc.kind)
		a.gameF.Started = true
		action, err := parseFirstJSON(fmt.Sprintf(`{"type":"place","x":%d,"y":%d}`, tc.size, tc.size))
		if err != nil || normalizeGameAIAction(tc.kind, action) != nil {
			t.Fatalf("valid boundary rejected: %v", err)
		}
		if err := a.applyGameAction(0, action); err != nil || a.gameF.Board[tc.size*tc.size-1] != 1 {
			t.Fatalf("last cell: %v", err)
		}
		if err := normalizeGameAIAction(tc.kind, map[string]any{"type": "place", "x": float64(0), "y": float64(1)}); err == nil {
			t.Fatal("zero-based model coordinate accepted")
		}
	}
	a := &desktop{}
	a.resetGame(gameXiangqi)
	action, _ := parseFirstJSON(`{"type":"move","fromX":1,"fromY":7,"toX":1,"toY":6}`)
	if err := normalizeGameAIAction(gameXiangqi, action); err != nil {
		t.Fatal(err)
	}
	if err := a.applyGameAction(0, action); err != nil || a.gameF.XQ[5][0] != "rP" {
		t.Fatalf("red pawn move failed: %v", err)
	}
}

func TestDoudizhuStartsWithAIDealAndFinishedBoardRemains(t *testing.T) {
	a := &desktop{gameF: gameUI{Kind: gameDoudizhu, VsAI: true}}
	a.startGame() // no configured model: the deal still happens before selection fails
	g := &a.gameF
	if !g.VsAI || g.Phase != ddzPhaseBid || len(g.Hands[0]) != 17 || len(g.Bottom) != 3 {
		t.Fatalf("AI game did not deal: %+v", *g)
	}
	g.Winner = 1
	a.gameFinished()
	if !g.Finished || g.Started || len(g.Hands[0]) != 17 {
		t.Fatal("finishing the game discarded the board")
	}
}

func TestMCPPairInputRejectsBrokenJSON(t *testing.T) {
	doc, err := parseMCPConfig("")
	if err != nil || doc.Servers == nil {
		t.Fatal("empty MCP configuration cannot accept a new server")
	}
	for _, tc := range []struct{ raw, sep string }{{`{"API_KEY":"sk-1"`, "="}, {`{"Authorization":"x"`, ":"}} {
		if _, err := parsePairs(tc.raw, tc.sep); err == nil {
			t.Fatalf("accepted invalid JSON %q", tc.raw)
		}
	}
	got, err := parsePairs("A=hello world\nB=中文", "=")
	if err != nil || got["A"] != "hello world" || got["B"] != "中文" {
		t.Fatalf("line format: %v %v", got, err)
	}
	a := &desktop{mcpText: `{"mcpServers":{"server":{"command":"node","env":{"A":"old"}}}}`}
	a.mcpF = mcpForm{Name: "server", Editing: "server", Transport: "stdio", Command: "node", Env: `{"A":"new"`, EditorOpen: true}
	before := a.mcpText
	a.saveMCPEditor()
	if a.mcpText != before || !a.mcpF.EditorOpen || len(a.notifications) == 0 || a.mcpF.Env != `{"A":"new"` {
		t.Fatal("invalid environment JSON was saved or discarded")
	}
}

type fakeSSHImportStore struct{ nodes []backend.SSHServerNode }

func (s *fakeSSHImportStore) ListAllSSHServers() []backend.SSHServerNode { return s.nodes }
func (s *fakeSSHImportStore) SaveSSHServer(node backend.SSHServerNode) error {
	if node.Alias == "bad" {
		return errors.New("invalid host")
	}
	if node.ID == "" {
		node.ID = "local-new"
		s.nodes = append(s.nodes, node)
		return nil
	}
	for i := range s.nodes {
		if s.nodes[i].ID == node.ID {
			s.nodes[i] = node
			return nil
		}
	}
	return errors.New("server no longer exists")
}

func TestSSHImportUsesLocalIDsAndReportsFailures(t *testing.T) {
	store := &fakeSSHImportStore{nodes: []backend.SSHServerNode{{ID: "local-old", Alias: "existing"}}}
	added, updated, failures := importSSHNodes(store, []backend.SSHServerNode{
		{ID: "foreign-1", Alias: "new"}, {ID: "foreign-2", Alias: "EXISTING"}, {ID: "foreign-3", Alias: "bad"},
	})
	if added != 1 || updated != 1 || len(failures) != 1 || !strings.Contains(failures[0], "invalid host") || store.nodes[0].ID != "local-old" || store.nodes[1].ID != "local-new" {
		t.Fatalf("import = %d/%d, %v, %+v", added, updated, failures, store.nodes)
	}
}

func TestServiceEventDoesNotDiscardFetchedLog(t *testing.T) {
	a := &desktop{}
	a.showServiceLog(backend.ServiceInfo{ID: "svc", OutputTail: "preview"})
	a.tasksF.LogBody = strings.Repeat("older history\n", 1000)
	a.tasksF.LogComplete, a.tasksF.LogTruncated = true, true
	before := a.tasksF.LogBody
	a.applyServiceUpdate(map[string]any{"service": map[string]any{"id": "svc", "status": "stopped", "outputTail": "new tail"}})
	if a.tasksF.LogBody != before || !a.tasksF.LogTruncated || len(a.services) != 1 {
		t.Fatal("service update replaced the loaded log or failed to update the list")
	}
}

func TestMentionWithoutCandidatesFallsThroughToSend(t *testing.T) {
	conv := &conversation{ID: "mention", Draft: "看下 @composer.go 这段"}
	conv.uiState().Mention.Open = true
	a := &desktop{ready: true}
	tt := ui.NewTester(func(c *ui.Context) { c.SetTheme(a.theme()); a.composer(c, conv) }, 700, 400)
	box, ok := tt.Find("消息输入框")
	if !ok {
		t.Fatal("composer missing")
	}
	tt.ClickAt(box.X+box.W/2, box.Y+box.H/2)
	tt.Key(0, ui.KeyEnter)
	if len(a.notifications) == 0 || !strings.Contains(a.notifications[len(a.notifications)-1].Title, "模型") {
		t.Fatal("Enter was consumed before reaching send")
	}
}

func TestAttachmentLoadingReportsRejectedImage(t *testing.T) {
	a := &desktop{}
	conv := &conversation{ID: "loading", AttachmentLoading: true}
	a.dropAttachments(conv, []string{"pending.png"})
	if len(a.notifications) == 0 {
		t.Fatal("dropped file was rejected silently")
	}
	a.notifications = nil
	tt := ui.NewTester(func(c *ui.Context) { c.SetTheme(a.theme()); a.composer(c, conv) }, 700, 400)
	box, ok := tt.Find("消息输入框")
	if !ok {
		t.Fatal("composer missing")
	}
	tt.ClickAt(box.X+box.W/2, box.Y+box.H/2)
	tt.SetClipboardData(clipboardPNG(t))
	tt.Key(ui.Cmd, ui.KeyV)
	if len(a.notifications) == 0 {
		t.Fatal("pasted image was rejected silently")
	}
}

func TestImageModelTestDismissalReleasesResults(t *testing.T) {
	s := &imageModelsState{testOpen: true, test: &imageModelTest{inputs: []imageTestInput{{dataURL: "large input"}}, results: []imageTestResult{{}}}}
	a := &desktop{imageModels: s}
	tt := ui.NewTester(func(c *ui.Context) { c.SetTheme(a.theme()); a.imageModelTestDialog(c) }, 900, 700)
	tt.Frame()
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if s.testOpen || s.test != nil {
		t.Fatal("closed test dialog retained images")
	}
}

func TestUserAgentFieldKeepsSpacesBetweenKeystrokes(t *testing.T) {
	a := &desktop{}
	a.settings.Modules = map[string]int{"settingsGeneral": 2}
	tt := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(a.theme())
		ui.Column(c).Fill().Children(func() { a.settingsGeneral(c) })
	}, 1000, 700)
	if err := tt.Click("User-Agent"); err != nil {
		t.Fatal(err)
	}
	for _, part := range []string{"agent", " ", "name"} {
		tt.Type(part)
		tt.Frame()
	}
	if a.configDraft.UserAgent != "agent name" {
		t.Fatalf("user agent lost input: %q", a.configDraft.UserAgent)
	}
}

func TestUserAgentDefaultLabelIsNormalizedOnlyOnSave(t *testing.T) {
	a := &desktop{}
	a.settings.Modules = map[string]int{"settingsGeneral": 2}
	a.configDraft.UserAgent = defaultUserAgentOption
	tt := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(a.theme())
		ui.Column(c).Fill().Children(func() { a.settingsGeneral(c) })
	}, 1000, 700)
	tt.Frame()
	tt.Frame()
	if a.configDraft.UserAgent != defaultUserAgentOption {
		t.Fatal("rendering erased the user-agent input")
	}
	a.saveSettings()
	if a.configDraft.UserAgent != "" {
		t.Fatal("saving did not map the default option to the default agent")
	}
}

func TestCatalogModelCanBeClearedAndReplacedWithCustomID(t *testing.T) {
	provider := availableModelCatalog().Providers[0]
	if len(provider.Models) == 0 {
		t.Skip("first catalog provider has no models")
	}
	a := &desktop{}
	a.editModel(-1)
	a.modelProviderPreset = catalogProviderLabel(provider)
	a.modelProviderModel = catalogModelLabel(provider.Models[0])
	applyCatalogModel(a, provider.Models[0])
	a.modelForm.Open = true
	tt := ui.NewTester(func(c *ui.Context) { c.SetTheme(a.theme()); a.chatModelDialog(c) }, 1000, 850)
	if err := tt.Click("供应商模型"); err != nil {
		t.Fatal(err)
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Key(0, ui.KeyBackspace)
	tt.Frame()
	for _, part := range []string{"custom", "-model"} {
		tt.Type(part)
		tt.Frame()
	}
	if a.modelDraft.Model != "custom-model" || a.modelDraft.DisplayName != "" {
		t.Fatalf("custom model not saved to draft: %q / %q", a.modelDraft.Model, a.modelDraft.DisplayName)
	}
}

func TestServerAndToolSearchShowEmptyState(t *testing.T) {
	a := &desktop{mcpText: `{"mcpServers":{"server":{"command":"node"}}}`, toolInventory: []backend.ToolDefinitionSummary{{Name: "read", Protected: true}}}
	a.mcpF.Search = "not-a-match"
	a.builtinToolSearch = "not-a-match"
	for _, tc := range []struct {
		name string
		draw func(*ui.Context)
		want string
	}{{"servers", a.mcpServerList, "无匹配结果"}, {"builtins", a.builtinToolsGrid, "没有匹配的工具"}} {
		t.Run(tc.name, func(t *testing.T) {
			tt := ui.NewTester(func(c *ui.Context) { c.SetTheme(a.theme()); ui.Column(c).Fill().Children(func() { tc.draw(c) }) }, 700, 400)
			if _, ok := tt.Find(tc.want); !ok {
				t.Fatalf("missing %q: %v", tc.want, tt.Texts())
			}
		})
	}
}

func TestCommandPreviewMatchesJoinedOutput(t *testing.T) {
	for _, result := range []map[string]any{
		{"stdout": "one\n\n", "stderr": "two\t spaces\r\n\n"},
		{"output": "hello", "text": " \n\n", "_text": "最后\n"},
		{"stderr": "\n\n"},
	} {
		for _, n := range []int{1, 2, 4} {
			if got, want := commandResultPreview(result, n), commandPreviewText(commandOutput(result), n); got != want {
				t.Fatalf("preview %q != %q", got, want)
			}
		}
	}
}

// Removing a workspace entry deletes its sessions, so it must collect every
// one recorded there, not just the window the rail displays.
func TestWorkspaceHistoryRemovalCoversEverySession(t *testing.T) {
	root := t.TempDir()
	other := t.TempDir()
	a := &desktop{sessions: []backend.SessionIndexEntry{
		{ID: "one", Workspace: root},
		{ID: "two", Workspace: strings.ToUpper(root)},
		{ID: "three", Workspace: other},
		{ID: "", Workspace: root},
	}}
	ids := a.workspaceSessionIDs(root)
	if len(ids) != 2 || ids[0] != "one" || ids[1] != "two" {
		t.Fatalf("workspace removal targeted %v", ids)
	}
}
