package desktop

import (
	"testing"

	backend "ally-dev/internal/app"
)

func TestDialogRendering(t *testing.T) {
	// model editor
	a := demoDesktop()
	a.page = "models"
	a.editModel(0)
	a.modelForm.Open = true
	renderWorkbench(t, "modal-model", a, 1266, 843)

	// mcp editor
	b := demoDesktop()
	b.page = "mcp"
	b.mcpText = `{"mcpServers":{"demo":{"command":"npx","args":["-y","demo-server"]}}}`
	b.openMCPEditor("demo")
	renderWorkbench(t, "modal-mcp", b, 1266, 843)

	// ssh editor + auth
	c := demoDesktop()
	c.page = "ssh"
	c.sshServers = []backend.SSHServerNode{{ID: "s1", Alias: "dev", Group: "生产", Host: "10.0.0.7", Port: 22, Username: "root", AuthType: "agent"}}
	c.openSSHEditor("s1", "生产")
	renderWorkbench(t, "modal-ssh", c, 1266, 843)
	c.sshF.EditorOpen = false
	c.sshF.AuthOpen = true
	renderWorkbench(t, "modal-ssh-auth", c, 1266, 843)

	// image model editor
	d := demoDesktop()
	d.page = "models"
	d.modelTab = "图片模型"
	d.ensureImageModels()
	d.imageModels.openForm(-1)
	renderWorkbench(t, "modal-image", d, 1266, 843)

	// git diff + file info
	e := demoDesktop()
	e.explorer = true
	e.filesF.GitOpen = true
	e.filesF.GitFile = 0
	e.filesF.GitData = backend.GitDiffResult{IsRepo: true, Branch: "main", Files: []backend.GitDiffFile{{Path: "internal/app/app.go", Status: "modified", Added: 12, Deleted: 3, Diff: "@@ -1,4 +1,6 @@\n package app\n-func old() {}\n+func new() {}\n+func extra() {}\n"}}}
	renderWorkbench(t, "modal-git", e, 1266, 843)

	// task log
	f2 := demoDesktop()
	f2.page = "tasks"
	f2.tasksF.LogOpen = true
	f2.tasksF.LogTitle = "开发服务 · 实时日志"
	f2.tasksF.LogBody = "ready in 120ms\nGET / 200 1.2ms\nGET /assets/app.js 200 0.4ms"
	renderWorkbench(t, "modal-log", f2, 1266, 843)

	// ask form
	g := demoDesktop()
	g.applyEvent("ask:ready", map[string]any{"sessionId": g.current().ID, "askId": "q", "questions": []any{
		map[string]any{"id": "q1", "question": "选择数据库", "options": []any{map[string]any{"id": "a", "label": "SQLite"}, map[string]any{"id": "b", "label": "PostgreSQL"}}},
	}})
	renderWorkbench(t, "ask", g, 1266, 843)

	// games started
	h := demoDesktop()
	h.page = "games"
	h.startGame()
	renderWorkbench(t, "games-started", h, 1266, 843)

	// settings advanced
	i2 := demoDesktop()
	i2.page = "settings"
	i2.settings.Tab = "高级"
	i2.settingsTab = "高级"
	renderWorkbench(t, "settings-advanced", i2, 1266, 843)

	// kb empty
	k := demoDesktop()
	k.page = "kb"
	k.config.KBRoot = ""
	renderWorkbench(t, "kb-empty", k, 1266, 843)
}
