// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"fmt"
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
	}
	st.Usage = backend.WorkspaceTokenUsage{InputTokens: 738_700, OutputTokens: 8_600}

	a := &desktop{}
	a.config.Models = []backend.ModelConfig{{ProviderName: "provider", Model: "model", ContextWindow: 1_000_000}}

	const width, height = 900, 800
	tester := ui.NewTester(func(c *ui.Context) { a.contextUsage(c, conv) }, width, height)

	last := fmt.Sprintf("累计输入 %s / 输出 %s", fmtK(st.Usage.InputTokens), fmtK(st.Usage.OutputTokens))
	rows := []struct {
		what string
		text string
	}{
		{"the title", "上下文构成"},
		{"the first row", "系统提示词"},
		{"the last row", last},
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
	if tail, buttons := rects["the last row"], rects["the buttons"]; tail.Y+tail.H > buttons.Y {
		t.Errorf("the last row ends at y %v, below the buttons at y %v: the breakdown was capped and scrolls", tail.Y+tail.H, buttons.Y)
	}
}
