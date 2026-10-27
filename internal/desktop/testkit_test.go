// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	backend "ally-dev/internal/app"
	"github.com/egoist/mygo/ui/markdown"
)

// demoDesktop builds a fully populated desktop without touching the backend.
// Every UI test drives this fixture through ui.NewTester, so nothing here may
// perform I/O: the fields are plain data the view reads.
func demoDesktop() *desktop {
	root := "D:/projects/ally-agent"
	a := &desktop{
		ready:          true,
		page:           "chat",
		prefs:          defaultPrefs(),
		settingsTab:    "通用",
		modelTab:       "对话模型",
		plans:          map[string][]backend.PlanStep{},
		planRev:        map[string]int64{},
		subRuns:        map[string]*backend.SubagentRun{},
		openSessions:   map[string]*conversation{},
		fileOpen:       map[string]bool{},
		fileChildren:   map[string][]backend.FileEntry{},
		fileLoading:    map[string]bool{},
		workspaceFiles: map[string]*workspaceFile{},
		gameF:          gameUI{ModelA: "OpenAI / gpt", ModelB: "OpenAI / gpt", EffortA: "auto", EffortB: "auto"},
		pluginsF:       pluginsForm{MenuOpen: map[string]bool{}},
		config: backend.ConfigState{
			Workspace: root,
			Models: []backend.ModelConfig{{
				ProviderName: "OpenAI",
				Model:        "gpt",
				APIFormat:    "openai",
				BaseURL:      "https://api.openai.com/v1",
				APIKey:       "test-key",
			}},
			LLMRetries:        2,
			ToolConcurrency:   4,
			MessageFontSize:   15.5,
			CodeFontSize:      14,
			ToolFontSize:      15,
			SubFontSize:       13,
			AuxFontSize:       12,
			BackgroundOpacity: 0.15,
		},
	}
	a.syncSettingsForm()
	t := a.freshConversation(root)
	t.Title = "Ally 原生工作台"
	t.Model = "OpenAI / gpt"
	t.Reasoning = "auto"
	t.MD = map[int]*markdown.Options{}
	t.Expanded = map[int]bool{}
	t.Messages = []map[string]any{
		{"role": "user", "content": "介绍一下这个项目，并帮我梳理一下代码结构。"},
		{"role": "assistant", "content": "这是 Ally 的原生界面版本，后端与 Vue 版本共用同一套 Agent 核心。"},
	}
	a.tabs = []*conversation{t}
	a.openSessions[t.ID] = t
	a.files = []backend.FileEntry{
		{Path: "internal", Name: "internal", Dir: true},
		{Path: "README.md", Name: "README.md"},
		{Path: "go.mod", Name: "go.mod"},
	}
	a.fileChildren["internal"] = []backend.FileEntry{
		{Path: "internal/app", Name: "app", Dir: true},
		{Path: "internal/tools", Name: "tools", Dir: true},
	}
	a.fileChildren["internal/app"] = []backend.FileEntry{
		{Path: "internal/app/app.go", Name: "app.go"},
	}
	a.status = "就绪"
	return a
}
