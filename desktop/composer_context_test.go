// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"testing"

	backend "ally-dev/internal/app"
	"github.com/egoist/mygo/ui"
)

// The breakdown is a fixed set of rows, so the panel has to show it whole: a row
// that ends below the buttons means the list was capped and clipped behind a
// scrollbar that carried no news. The numbers below are a project with every
// instruction file and a session that has run for a while.
func TestContextBreakdownPanelShowsEveryRow(t *testing.T) {
	conv := &conversation{ID: "breakdown", Workspace: `C:\work\ally`, Model: "provider / model"}
	st := conv.uiState()
	st.FooterLoaded = true
	st.ContextOpen = true
	// The prompt's sections start folded, so the tallest state — every row the
	// panel can show, sections included — is the one that has to fit.
	st.PromptPartsOpen = true
	st.Breakdown = backend.ContextBreakdown{
		Total:        738_700,
		SystemPrompt: 2_600,
		SystemPromptParts: []backend.ContextBreakdownPart{
			{Label: "全局记忆索引", Tokens: 973},
			{Label: "用户档案 User Profile", Tokens: 816},
			{Label: "AGENTS.md / 项目指令", Tokens: 44_300},
			{Label: "项目经验 Lessons", Tokens: 2_500},
			{Label: "自定义提示词", Tokens: 2_900},
			{Label: "工作区文件结构", Tokens: 3_500},
		},
		ToolSchemas:   18_800,
		UserMessages:  84,
		AssistantMsgs: 2_800,
		ToolResults:   21_700,
		Reasoning:     9_400,
		ImageBlocks:   3,
	}
	st.Usage = backend.WorkspaceTokenUsage{InputTokens: 738_700, OutputTokens: 8_600}

	a := &desktop{}
	a.config.Models = []backend.ModelConfig{{ProviderName: "provider", Model: "model", ContextWindow: 1_000_000}}

	const width, height = 900, 800
	tester := ui.NewTester(func(c *ui.Context) { a.contextUsage(c, conv) }, width, height)

	rows := []struct {
		what string
		text string
	}{
		{"the title", "上下文构成"},
		{"the first row", "系统提示词"},
		{"the image count", "历史图片块"},
		{"the image value", "3 张"},
		{"the buttons", "压缩上下文"},
	}
	rects := map[string]ui.Rect{}
	for _, row := range rows {
		r, ok := tester.Find(row.text)
		if !ok {
			t.Fatalf("%s %q is not on screen, the panel shows %v", row.what, row.text, tester.Texts())
		}
		rects[row.what] = r
		if r.W == 0 || r.H == 0 {
			t.Errorf("%s %q shows %vx%v: the list was capped and clipped it away", row.what, row.text, r.W, r.H)
		}
		if r.Y < 0 || r.Y+r.H > height {
			t.Errorf("%s %q sits at y %v..%v, outside the %d tall window", row.what, row.text, r.Y, r.Y+r.H, height)
		}
	}
}

// The prompt's sections are the one group long enough to fold: the total on the
// parent line is what the reader came for, and the seven lines under it are
// detail. The parent row opens and closes them without closing the panel.
func TestContextBreakdownFoldsSystemPromptParts(t *testing.T) {
	conv := &conversation{ID: "fold", Workspace: `C:\work\ally`, Model: "provider / model"}
	st := conv.uiState()
	st.FooterLoaded = true
	st.ContextOpen = true
	st.Breakdown = backend.ContextBreakdown{
		SystemPrompt: 4_900,
		SystemPromptParts: []backend.ContextBreakdownPart{
			{Label: "核心系统提示词", Tokens: 2_900},
			{Label: "项目经验 Lessons", Tokens: 383},
		},
		ToolSchemas: 18_700,
	}
	a := &desktop{}
	a.config.Models = []backend.ModelConfig{{ProviderName: "provider", Model: "model", ContextWindow: 1_000_000}}

	tester := ui.NewTester(func(c *ui.Context) {
		c.DisableAnimations()
		a.contextUsage(c, conv)
	}, 900, 800)

	if st.PromptPartsOpen {
		t.Fatal("the prompt's sections start folded")
	}
	if tester.HasText("核心系统提示词") {
		t.Fatalf("the folded row still lists its sections: %v", tester.Texts())
	}
	folded, ok := tester.Find("系统提示词")
	if !ok || folded.W == 0 || folded.H == 0 {
		t.Fatalf("the folded row is missing or has no room: %+v", folded)
	}
	// The total rides on the same line rather than on a row of its own: folding a
	// group must not cost the reader the number it stands for.
	total, ok := tester.Find(fmtK(st.Breakdown.SystemPrompt))
	if !ok || total.Y+total.H <= folded.Y || total.Y >= folded.Y+folded.H {
		t.Fatalf("the folded row lost its total: %+v beside a row at y %v..%v", total, folded.Y, folded.Y+folded.H)
	}
	// The chevron takes the trailing edge, after the value, so this one number stops
	// short of the column the rows below share instead of pushing them out of line.
	if plain, ok := tester.Find(fmtK(st.Breakdown.ToolSchemas)); !ok {
		t.Fatalf("the rows below lost their numbers: %v", tester.Texts())
	} else if total.X+total.W >= plain.X+plain.W-10 {
		t.Fatalf("the chevron is not trailing: the total ends at %v, the column at %v", total.X+total.W, plain.X+plain.W)
	}

	if err := tester.Click("系统提示词"); err != nil {
		t.Fatal(err)
	}
	if !st.PromptPartsOpen {
		t.Fatal("the click did not open the prompt's sections")
	}
	if !tester.HasText("核心系统提示词") {
		t.Fatalf("the sections stayed hidden: %v", tester.Texts())
	}
	if !st.ContextOpen {
		t.Fatal("opening the sections closed the panel")
	}

	if err := tester.Click("系统提示词"); err != nil {
		t.Fatal(err)
	}
	if st.PromptPartsOpen || tester.HasText("核心系统提示词") {
		t.Fatal("the second click did not fold the sections again")
	}

	// The row is a button, not a caption: once it holds the focus, Space opens it the
	// same way a click does.
	tester.Key(0, ui.KeySpace)
	if !st.PromptPartsOpen || !tester.HasText("核心系统提示词") {
		t.Fatal("the focused row ignored the keyboard")
	}
}
