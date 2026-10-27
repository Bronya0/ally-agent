package desktop

import (
	"testing"

	backend "ally-dev/internal/app"
	"github.com/egoist/mygo/ui"
)

func renderWorkbench(t *testing.T, name string, a *desktop, w, h int) {
	t.Helper()
	t.Run(name, func(t *testing.T) {
		tt := ui.NewTester(a.view, w, h)
		tt.Frame()
		tt.SetSize(900, 620)
		tt.Frame()
		tt.SetSize(w, h)
		tt.Frame()
	})
}

func TestPageRendering(t *testing.T) {
	pages := []string{"settings", "models", "mcp", "skills", "ssh", "plugins", "tasks", "stats", "games", "kb"}
	for _, page := range pages {
		a := demoDesktop()
		a.page = page
		a.skills = []backend.SkillDefinition{{Name: "codegraph", Description: "生成或更新 CODEGRAPH.md", Source: "builtin", WhenToUse: "需要代码导航时"},
			{Name: "mygo-api", Description: "mygo 原生 UI 组件参考", Source: "user"}}
		a.plugins = []backend.PluginInfo{{ID: "weather", Name: "天气", Version: "1.0.0", Mode: "plugin:weather", Dir: "plugins/weather"}}
		a.sshServers = []backend.SSHServerNode{{ID: "s1", Alias: "dev", Group: "生产", Host: "10.0.0.7", Port: 22, Username: "root", AuthType: "agent"}}
		a.services = []backend.ServiceInfo{{ID: "svc-1", Name: "开发服务", Status: "running", Command: "go run .", Workspace: a.current().Workspace, OutputTail: "ready"}}
		a.tasks = []backend.ScheduledTask{{ID: "t1", Name: "每日构建", Workspace: a.current().Workspace, LastSummary: "上次执行完成", Running: true}}
		a.stats = backend.TokenStatsResult{
			OK:           true,
			SummaryToday: backend.TokenStatsSummary{TotalTokens: 12000, InputTokens: 9000, OutputTokens: 3000, Requests: 12, CacheHitRate: 0.42},
			Summary7Days: backend.TokenStatsSummary{TotalTokens: 88000, InputTokens: 61000, OutputTokens: 27000, Requests: 74, CacheHitRate: 0.35},
			SummaryMonth: backend.TokenStatsSummary{TotalTokens: 320000, InputTokens: 240000, OutputTokens: 80000, Requests: 260, CacheHitRate: 0.31},
			Daily:        []backend.TokenDailyStat{{Date: "2026-10-25", InputTokens: 12000, OutputTokens: 4000}, {Date: "2026-10-26", InputTokens: 8000, OutputTokens: 2000}, {Date: "2026-10-27", InputTokens: 9000, OutputTokens: 3000}},
			MonthDaily:   []backend.TokenDailyStat{{Date: "2026-10-01", InputTokens: 2000}, {Date: "2026-10-05", InputTokens: 9000}, {Date: "2026-10-27", InputTokens: 9000}},
			ModelWeek:    []backend.TokenDimensionStat{{Name: "gpt-5", InputTokens: 60000, OutputTokens: 8000}, {Name: "claude", InputTokens: 28000, OutputTokens: 4000}},
			ModelMonth:   []backend.TokenDimensionStat{{Name: "gpt-5", InputTokens: 200000, OutputTokens: 30000}},
		}
		a.mcpText = `{"mcpServers":{"demo":{"command":"server"}}}`
		a.toolInventory = []backend.ToolDefinitionSummary{{Name: "read", Source: "builtin", Enabled: true, Protected: true}, {Name: "search", Source: "builtin", Enabled: true}}
		renderWorkbench(t, page, a, 1266, 843)
	}
	// chat with tool card + plan + ask
	a := demoDesktop()
	a.applyEvent("tool:start", map[string]any{"sessionId": a.current().ID, "toolCallId": "c1", "name": "read", "args": `{"path":"go.mod"}`})
	a.applyEvent("tool:result", map[string]any{"sessionId": a.current().ID, "toolCallId": "c1", "result": `{"path":"go.mod","content":"module ally-dev\n","startLine":1,"endLine":1}`})
	a.applyEvent("plan:update", map[string]any{"sessionId": a.current().ID, "plan": []any{map[string]any{"title": "梳理页面", "status": "done"}, map[string]any{"title": "补齐模态框", "status": "in_progress"}}})
	renderWorkbench(t, "chat-tools", a, 1266, 843)
	a2 := demoDesktop()
	a2.explorer = true
	renderWorkbench(t, "chat-explorer", a2, 1266, 843)
}
