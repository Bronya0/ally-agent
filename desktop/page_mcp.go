// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"ally-dev/desktop/uikit"
	"encoding/json"
	"fmt"
	"maps"
	"sort"
	"strings"
	"time"

	backend "ally-dev/internal/app"
	"github.com/egoist/mygo/ui"
)

type mcpSaveOptions struct {
	replaceAll bool
	baseRaw    string
	onSuccess  func()
}

// mcpNoteLine is a one-line note: tab-specific notes must not change height,
// or the content below the tab strip shifts when the tab changes.
func mcpNoteLine(c *ui.Context, s string, ink ...ui.Color) {
	ui.Text(c, s).FontSize(fsTiny).TextColor(uikit.SecondaryInk(c, ink)).SingleLine()
}

// mcpServerEntry mirrors the fields the editor owns; unknown keys survive in
// Extra so a hand-edited mcp.json is round-tripped without loss.
type mcpServerEntry struct {
	Command       string            `json:"command,omitempty"`
	Args          []string          `json:"args,omitempty"`
	Env           map[string]string `json:"env,omitempty"`
	Cwd           string            `json:"cwd,omitempty"`
	Transport     string            `json:"transport,omitempty"`
	URL           string            `json:"url,omitempty"`
	Headers       map[string]string `json:"headers,omitempty"`
	Enabled       *bool             `json:"enabled,omitempty"`
	DisabledTools []string          `json:"disabledTools,omitempty"`
	Extra         map[string]json.RawMessage
}

func (e mcpServerEntry) transportName() string {
	return (backend.McpServerConfig{Transport: e.Transport, Command: e.Command, URL: e.URL}).TransportName()
}

func (e *mcpServerEntry) UnmarshalJSON(data []byte) error {
	type plain mcpServerEntry
	var p plain
	if err := json.Unmarshal(data, &p); err != nil {
		return err
	}
	*e = mcpServerEntry(p)
	var all map[string]json.RawMessage
	if err := json.Unmarshal(data, &all); err == nil {
		for _, k := range []string{"command", "args", "env", "cwd", "transport", "url", "headers", "enabled", "disabledTools"} {
			delete(all, k)
		}
		if len(all) > 0 {
			e.Extra = all
		}
	}
	return nil
}

func (e mcpServerEntry) MarshalJSON() ([]byte, error) {
	type plain mcpServerEntry
	base, err := json.Marshal(plain(e))
	if err != nil {
		return nil, err
	}
	if len(e.Extra) == 0 {
		return base, nil
	}
	var all map[string]json.RawMessage
	if err := json.Unmarshal(base, &all); err != nil {
		return nil, err
	}
	for k, v := range e.Extra {
		all[k] = v
	}
	return json.Marshal(all)
}

type mcpConfigDoc struct {
	Extra   map[string]json.RawMessage
	Servers map[string]mcpServerEntry
}

func (d *mcpConfigDoc) UnmarshalJSON(data []byte) error {
	var all map[string]json.RawMessage
	if err := json.Unmarshal(data, &all); err != nil {
		return err
	}
	serversRaw, ok := all["mcpServers"]
	delete(all, "mcpServers")
	d.Extra = all
	d.Servers = map[string]mcpServerEntry{}
	if !ok || len(serversRaw) == 0 || string(serversRaw) == "null" {
		return nil
	}
	return json.Unmarshal(serversRaw, &d.Servers)
}

func (d mcpConfigDoc) MarshalJSON() ([]byte, error) {
	all := map[string]json.RawMessage{}
	for k, v := range d.Extra {
		all[k] = v
	}
	servers, err := json.Marshal(d.Servers)
	if err != nil {
		return nil, err
	}
	all["mcpServers"] = servers
	return json.Marshal(all)
}

func parseMCPConfig(raw string) (*mcpConfigDoc, error) {
	doc := &mcpConfigDoc{Servers: map[string]mcpServerEntry{}}
	if strings.TrimSpace(raw) == "" {
		return doc, nil
	}
	if err := json.Unmarshal([]byte(raw), doc); err != nil {
		return nil, err
	}
	if doc.Servers == nil {
		doc.Servers = map[string]mcpServerEntry{}
	}
	return doc, nil
}

type mcpListData struct {
	doc   *mcpConfigDoc
	names []string
	err   error
}

func prepareMCPList(raw string) mcpListData {
	doc, err := parseMCPConfig(raw)
	data := mcpListData{doc: doc, err: err}
	if err == nil {
		for name := range doc.Servers {
			data.names = append(data.names, name)
		}
		sort.Strings(data.names)
	}
	return data
}

func (a *desktop) mcpPageBody(c *ui.Context) {
	if a.mcpF.ToolOpen == nil {
		a.mcpF.ToolOpen = map[string]bool{}
	}
	ui.Row(c).Gap(gapField).AlignItems(ui.Center).Wrap().Children(func() {
		uikit.Input(c.Key("mcp-search"), &a.mcpF.Search).Placeholder("搜索").Label("搜索 MCP").Width(200)
		ui.Spacer(c)
		flat(c, "导入").Disabled(a.mcpF.Busy || a.mcpF.ImportBusy).OnClick(func() { a.importMCP() })
		flat(c, "导出").Disabled(len(a.mcpServerNames) == 0).OnClick(func() { a.exportMCP() })
		flat(c, "刷新").Disabled(a.mcpF.Busy || a.mcpF.ImportBusy).OnClick(func() { a.refreshMCPAll(true) })
		a.linkMenu(c, "mcp-market", [][2]string{{"魔搭 ModelScope", "https://modelscope.cn/mcp"}, {"火山引擎 Volcengine", "https://www.volcengine.com/mcp"}, {"Glama", "https://glama.ai/mcp/servers"}, {"PulseMCP", "https://www.pulsemcp.com"}})
		primary(c, "添加").Disabled(a.mcpF.Busy || a.mcpF.ImportBusy).OnClick(func() { a.openMCPEditor("") })
	})
	// tabs 必须排在随 tab 变化的说明文字之前：说明行数不同会把 tab 栏顶上顶下，
	// 用户刚点的 tab 会跑走（"变形"）。tab 栏位置固定，只让下方内容变化。
	if a.scrollTabs(c, "mcp-tabs", &a.mcpF.Tab, "MCP 服务器", "Ally 内置工具") {
		if a.mcpF.Tab == 1 && !a.mcpF.Busy && !a.mcpF.ImportBusy {
			a.refreshMCPInventory()
		}
	}
	// 两个 tab 的说明都固定为一行，切换时说明区高度不变，下方内容不会跳。
	if a.mcpF.Tab == 0 {
		mcpNoteLine(c, "支持 stdio / SSE / Streamable HTTP；只有连接成功的工具会注入 AI，改动自动保存并重连。", a.secondaryInk())
	} else {
		mcpNoteLine(c, "停用后新会话不再注入该工具；已有会话保持冻结，MCP 服务不受影响。", a.secondaryInk())
	}
	ui.Scroll(c).Grow(1).MinHeight(0).Children(func() {
		ui.Column(c).Gap(gapInline).Children(func() {
			if a.mcpF.Tab == 1 {
				a.builtinToolsGrid(c)
			} else {
				a.mcpServerList(c)
			}
		})
	})
	a.mcpEditorDialog(c)
}

func (a *desktop) mcpServerList(c *ui.Context) {
	holder := ui.Column(c.Key("mcp-server-list")).FillWidth().Gap(gapInline)
	data := memoAt[memoOf[string, mcpListData]](holder, "config").get(a.mcpText, prepareMCPList)
	holder.Children(func() { a.renderMCPServerList(c, data) })
}

func (a *desktop) renderMCPServerList(c *ui.Context, data mcpListData) {
	if data.err != nil {
		notice(c, "error", "读取 MCP 配置失败："+data.err.Error(), a.secondaryInk())
		return
	}
	// Event handlers edit a shallow copy; the memoized source stays immutable.
	// The copy is made by the click itself: a frame that only repaints needs none,
	// and the whole server table would otherwise be cloned on every frame.
	var edit *mcpConfigDoc
	editDoc := func() *mcpConfigDoc {
		if edit == nil {
			edit = &mcpConfigDoc{Extra: data.doc.Extra, Servers: maps.Clone(data.doc.Servers)}
		}
		return edit
	}
	names := data.names
	query := strings.ToLower(strings.TrimSpace(a.mcpF.Search))
	// The index is built once per replacement of the status list, so each row
	// reads its status by name instead of asking for the whole index again.
	statuses := a.mcpStatusIndex()
	shown := 0
	for _, name := range names {
		entry := data.doc.Servers[name]
		status := statuses[name]
		if query != "" {
			haystack := strings.ToLower(name + " " + entry.Command + " " + entry.URL + " " + entry.Transport + " " + mcpToolNames(status))
			if !strings.Contains(haystack, query) {
				continue
			}
		}
		shown++
		rowCard(c, "mcp-"+name, func() {
			ui.Row(c).Gap(gapField).AlignItems(ui.Center).Wrap().Children(func() {
				statusDot(c, str(status, "status"))
				ui.Text(c, name).FontSize(fsBody).Bold()
				neutralChip(c, mcpTransportLabel(entry, status))
				ui.Spacer(c)
				tools := list(status, "tools")
				enabledCount := 0
				for _, item := range tools {
					if m, ok := item.(map[string]any); ok && !boolOf(m, "disabled") {
						enabledCount++
					}
				}
				if len(tools) > 0 {
					label := fmt.Sprintf("工具 %d/%d", enabledCount, len(tools))
					btn := uikit.Tooltip(c, cardFlat(c, label), "勾选的工具注入模型上下文，取消勾选即停用；对端新增工具默认启用；下一会话生效。")
					btn.OnClick(func() { a.mcpF.ToolOpen[name] = !a.mcpF.ToolOpen[name] })
				} else if count := int(num(status, "toolCount")); count > 0 {
					fieldNote(c, fmt.Sprintf("%d 个工具", count), a.secondaryInk())
				}
				stateText := mcpStatusText(str(status, "status"))
				uikit.Tooltip(c, ui.Text(c, stateText).FontSize(fsSmall).TextColor(toneColor(c, str(status, "status"), a.secondaryInk())), str(status, "error"))
				enabled := entry.Enabled == nil || *entry.Enabled
				uikit.SwitchNative(c.Key("mcp-enabled-"+name), &enabled).Label("启用 " + name).Disabled(a.mcpF.Busy || a.mcpF.ImportBusy).OnClick(func() {
					entry.Enabled = boolPtr(enabled)
					doc := editDoc()
					doc.Servers[name] = entry
					a.saveMCPDoc(doc)
				})
				cardFlat(c, "编辑").Disabled(a.mcpF.Busy || a.mcpF.ImportBusy).OnClick(func() { a.openMCPEditor(name) })
				cardDanger(c, "删除").Disabled(a.mcpF.Busy || a.mcpF.ImportBusy).OnClick(func() {
					doc := editDoc()
					delete(doc.Servers, name)
					a.saveMCPDoc(doc)
				})
			})
			if errText := str(status, "error"); errText != "" {
				inlineError(c, "mcp-error-"+name, errText)
			}
			if a.mcpF.ToolOpen[name] {
				// The checklist writes into the document it is given, so it takes a
				// provider instead of a copy: an open checklist repaints every frame,
				// and the clone belongs to the click that changes something.
				a.mcpToolChecklist(c, name, entry, editDoc, status)
			}
		})
	}
	if shown == 0 {
		if len(names) == 0 {
			emptyState(c, "暂无 MCP 服务", "点击“添加”创建第一个服务。", a.secondaryInk())
		} else {
			emptyState(c, "无匹配结果", "", a.secondaryInk())
		}
	}
}

func (a *desktop) setMCPServers(servers []map[string]any) {
	a.mcpServers = servers
	a.mcpServersRev++
}

// mcpStatusIndex maps a server name to its status map. It is rebuilt once per
// replacement of the status list instead of being walked once per row: the list
// page reads every visible row on every frame. A server without status is simply
// absent, and a missing map reads like an empty one through str and list.
func (a *desktop) mcpStatusIndex() map[string]map[string]any {
	return a.mcpStatusMemo.get(a.mcpServersRev, func(uint64) map[string]map[string]any {
		index := make(map[string]map[string]any, len(a.mcpServers))
		for _, server := range a.mcpServers {
			name := str(server, "name")
			if _, exists := index[name]; !exists {
				index[name] = server
			}
		}
		return index
	})
}

func mcpToolNames(status map[string]any) string {
	var names []string
	for _, item := range list(status, "tools") {
		if m, ok := item.(map[string]any); ok {
			names = append(names, str(m, "name"))
		}
	}
	return strings.Join(names, " ")
}

func mcpTransportLabel(entry mcpServerEntry, status map[string]any) string {
	if t := str(status, "transport"); t != "" {
		return t
	}
	switch entry.transportName() {
	case "sse":
		return "sse"
	case "streamable-http":
		return "Streamable HTTP"
	}
	return "stdio"
}

func (a *desktop) mcpToolChecklist(c *ui.Context, name string, entry mcpServerEntry, docOf func() *mcpConfigDoc, status map[string]any) {
	ui.Column(c).Key("mcp-tools-"+name).FillWidth().Padding(8, 10).Gap(2).Background(c.Theme().Background).Radius(radiusSmall).Children(func() {
		fieldNote(c, "勾选的工具注入模型上下文，取消勾选即停用；对端新增工具默认启用；下一会话生效，不触发重连。", a.secondaryInk())
		for _, item := range list(status, "tools") {
			m, ok := item.(map[string]any)
			if !ok {
				continue
			}
			toolName := str(m, "name")
			disabled := false
			for _, hidden := range entry.DisabledTools {
				if hidden == toolName {
					disabled = true
					break
				}
			}
			enabled := !disabled
			enabledPtr := &enabled
			ui.Row(c).Gap(gapField).AlignItems(ui.Center).Children(func() {
				ui.Checkbox(c.Key("mcp-tool-"+name+"-"+toolName), enabledPtr, toolName).Disabled(a.mcpF.Busy || a.mcpF.ImportBusy).OnClick(func() {
					doc := docOf()
					entry = doc.Servers[name]
					set := map[string]bool{}
					for _, t := range entry.DisabledTools {
						set[t] = true
					}
					if *enabledPtr {
						delete(set, toolName)
					} else {
						set[toolName] = true
					}
					entry.DisabledTools = nil
					for t := range set {
						entry.DisabledTools = append(entry.DisabledTools, t)
					}
					sort.Strings(entry.DisabledTools)
					doc.Servers[name] = entry
					a.saveMCPDoc(doc)
				})
				uikit.Tooltip(c, ui.Text(c, str(m, "description")).FontSize(fsTiny).TextColor(a.secondaryInk()).SingleLine(), str(m, "description"))
			})
		}
		if len(list(status, "tools")) == 0 {
			fieldNote(c, "连接成功后这里会列出该服务发现的工具。", a.secondaryInk())
		}
	})
}

func (a *desktop) openMCPEditor(name string) {
	doc, err := parseMCPConfig(a.mcpText)
	if err != nil {
		a.notifyError("读取 MCP 配置失败：" + err.Error())
		return
	}
	a.mcpF.EditorOpen = true
	a.mcpF.Editing = name
	if name != "" {
		entry := doc.Servers[name]
		a.mcpF.Name = name
		a.mcpF.Transport = entry.transportName()
		if a.mcpF.Transport == "" {
			a.mcpF.Transport = "stdio"
		}
		a.mcpF.Command = entry.Command
		a.mcpF.Args = formatArgs(entry.Args)
		a.mcpF.Env = formatPairs(entry.Env)
		a.mcpF.URL = entry.URL
		a.mcpF.Headers = formatPairs(entry.Headers)
		return
	}
	a.mcpF.Name = ""
	a.mcpF.Transport = "stdio"
	a.mcpF.Command = ""
	a.mcpF.Args = ""
	a.mcpF.Env = ""
	a.mcpF.URL = ""
	a.mcpF.Headers = ""
}

func formatPairs(values map[string]string) string {
	if len(values) == 0 {
		return ""
	}
	raw, _ := json.MarshalIndent(values, "", "  ")
	return string(raw)
}

func (a *desktop) mcpEditorDialog(c *ui.Context) {
	if !a.mcpF.EditorOpen {
		return
	}
	title := "添加 MCP 服务"
	if a.mcpF.Editing != "" {
		title = "编辑 MCP 服务"
	}
	height := float32(550)
	if a.mcpF.Transport != "stdio" {
		height = 430
	}
	formDialog(c, &a.mcpF.EditorOpen, widthModal, height, title, "", func() {
		ui.Grid(c.Key("mcp-header-fields")).ColumnTracks(ui.Fr(1), ui.Fr(1)).FillWidth().Gap(gapSection).Children(func() {
			fieldRow(c, "mcp-name", "服务名称", &a.mcpF.Name, false)
			ui.Column(c).Gap(gapTight).Children(func() {
				fieldLabel(c, "传输方式")
				transports := []string{"stdio", "sse", "Streamable HTTP"}
				current := a.mcpF.Transport
				if current == "http" || current == "streamable-http" {
					current = "Streamable HTTP"
				}
				if current == "" {
					current = "stdio"
				}
				if uikit.Select(c.Key("mcp-transport"), &current, transports, a.secondaryInk()).Label("传输方式").FillWidth().Changed() {
					a.mcpF.Transport = current
				}
			})
		})
		if a.mcpF.Transport == "stdio" {
			fieldRow(c, "mcp-command", "命令", &a.mcpF.Command, false)
			fieldLabel(c, "参数")
			textArea(c, "mcp-args", "参数", `-y, @playwright/mcp`, &a.mcpF.Args, 90).FillWidth()
			fieldNote(c, "推荐 JSON 字符串数组，可完整保留空格和逗号；也兼容简单的逗号或空格分隔输入。", a.secondaryInk())
			fieldLabel(c, "环境变量")
			textArea(c, "mcp-env", "环境变量", `{"API_KEY": "your-api-key"}`, &a.mcpF.Env, 70)
			fieldNote(c, "JSON 对象，也支持每行 KEY=value。", a.secondaryInk())
		} else {
			fieldRow(c, "mcp-url", "URL", &a.mcpF.URL, false)
			fieldNote(c, "例如 http://127.0.0.1:9121/mcp（SSE 通常为 /sse）。", a.secondaryInk())
			fieldLabel(c, "请求头")
			textArea(c, "mcp-headers", "请求头", `{"Authorization": "Bearer your-token"}`, &a.mcpF.Headers, 70)
			fieldNote(c, "JSON 对象，也支持每行 Key:Value。", a.secondaryInk())
		}
	}, func() {
		flat(c, "取消").OnClick(func() { a.mcpF.EditorOpen = false })
		primary(c, "保存").Disabled(a.mcpF.Busy || a.mcpF.ImportBusy).OnClick(func() { a.saveMCPEditor() })
	})
}

func (a *desktop) saveMCPEditor() {
	name := strings.TrimSpace(a.mcpF.Name)
	if name == "" {
		a.notify("请填写服务名称")
		return
	}
	doc, err := parseMCPConfig(a.mcpText)
	if err != nil {
		a.notifyError("读取 MCP 配置失败：" + err.Error())
		return
	}
	if _, exists := doc.Servers[name]; exists && a.mcpF.Editing != name {
		a.notifyError("已存在同名 MCP 服务，请使用其他名称")
		return
	}
	entry := doc.Servers[name]
	if a.mcpF.Editing != "" && a.mcpF.Editing != name {
		entry = doc.Servers[a.mcpF.Editing]
		delete(doc.Servers, a.mcpF.Editing)
	}
	transport := a.mcpF.Transport
	switch transport {
	case "Streamable HTTP":
		transport = "http"
	case "stdio":
		transport = "stdio"
	case "sse":
		transport = "sse"
	}
	entry.Transport = transport
	if transport == "stdio" {
		args, err := splitArgs(a.mcpF.Args)
		if err != nil {
			a.notifyError("参数必须是 JSON 字符串数组：" + err.Error())
			return
		}
		entry.Command = strings.TrimSpace(a.mcpF.Command)
		entry.Args = args
		entry.Env, err = parsePairs(a.mcpF.Env, "=")
		if err != nil {
			a.notifyError("环境变量必须是 JSON 字符串对象：" + err.Error())
			return
		}
		entry.URL = ""
		entry.Headers = nil
	} else {
		entry.URL = strings.TrimSpace(a.mcpF.URL)
		entry.Headers, err = parsePairs(a.mcpF.Headers, ":")
		if err != nil {
			a.notifyError("请求头必须是 JSON 字符串对象：" + err.Error())
			return
		}
		entry.Command = ""
		entry.Args = nil
		entry.Env = nil
	}
	doc.Servers[name] = entry
	a.saveMCPDoc(doc, mcpSaveOptions{onSuccess: func() { a.mcpF.EditorOpen = false }})
}

func formatArgs(args []string) string {
	if len(args) == 0 {
		return ""
	}
	data, _ := json.MarshalIndent(args, "", "  ")
	return string(data)
}

func splitArgs(raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	if strings.HasPrefix(raw, "[") {
		var args []string
		if err := json.Unmarshal([]byte(raw), &args); err != nil {
			return nil, err
		}
		return args, nil
	}
	parts := strings.Split(raw, ",")
	if len(parts) == 1 {
		parts = strings.Fields(raw)
	}
	var out []string
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out, nil
}

func parsePairs(raw, sep string) (map[string]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	out := map[string]string{}
	if strings.HasPrefix(raw, "{") {
		if err := json.Unmarshal([]byte(raw), &out); err != nil {
			return nil, err
		}
		return out, nil
	}
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !strings.Contains(line, sep) {
			continue
		}
		kv := strings.SplitN(line, sep, 2)
		out[strings.TrimSpace(kv[0])] = strings.TrimSpace(kv[1])
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

func (a *desktop) saveMCPDoc(doc *mcpConfigDoc, options ...mcpSaveOptions) {
	if a.mcpF.Busy || a.mcpF.ImportBusy || a.core == nil || a.win == nil {
		return
	}
	opt := mcpSaveOptions{baseRaw: a.mcpText}
	if len(options) > 0 {
		opt = options[0]
		if opt.baseRaw == "" && !opt.replaceAll {
			opt.baseRaw = a.mcpText
		}
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		a.notifyError(err.Error())
		return
	}
	raw := string(data)
	a.mcpF.Busy = true
	a.mcpF.Revision++
	revision := a.mcpF.Revision
	go func() {
		var saved string
		var err error
		if opt.replaceAll {
			err = a.core.SaveMcpConfigIfUnchanged(opt.baseRaw, raw)
		} else {
			saved, err = a.core.MergeMcpConfig(opt.baseRaw, raw)
		}
		if err == nil {
			err = a.core.ReconcileMcpServers()
		}
		var servers []map[string]any
		var names []string
		if err == nil {
			if saved == "" || opt.replaceAll {
				saved, _ = a.core.GetMcpConfig()
			}
			servers = a.core.GetMcpServers()
			if d, e := parseMCPConfig(saved); e == nil {
				for name := range d.Servers {
					names = append(names, name)
				}
				sort.Strings(names)
			}
		}
		a.win.Update(func() {
			if revision != a.mcpF.Revision {
				return
			}
			a.mcpF.Busy = false
			if err != nil {
				a.notifyError("保存 MCP 配置失败：" + err.Error())
				a.refreshMCPAll()
				return
			}
			a.mcpText = saved
			a.mcpServerNames = names
			a.setMCPServers(servers)
			a.mcpSummary = mcpSummary(servers)
			a.refreshMCPInventory()
			if opt.onSuccess != nil {
				opt.onSuccess()
			}
			a.notifySuccess("MCP 配置已保存")
		})
	}()
}

func (a *desktop) refreshMCPAll(manual ...bool) {
	if a.mcpF.Busy || a.mcpF.ImportBusy {
		return
	}
	a.mcpF.Revision++
	revision := a.mcpF.Revision
	go func() {
		raw, err := a.core.GetMcpConfig()
		var servers []map[string]any
		var names []string
		if err == nil {
			servers = a.core.GetMcpServers()
			if doc, e := parseMCPConfig(raw); e == nil {
				for name := range doc.Servers {
					names = append(names, name)
				}
				sort.Strings(names)
			}
		}
		a.win.Update(func() {
			// ImportBusy 不在这里拦截：导入文件对话框打开期间不会有写盘在飞，
			// 而在飞的这次读取若被丢掉，页面会一直停在“没有服务”的空状态。
			if revision != a.mcpF.Revision || a.mcpF.Busy {
				return
			}
			if err != nil {
				a.notifyError("读取 MCP 配置失败：" + err.Error())
				return
			}
			a.mcpText = raw
			a.setMCPServers(servers)
			a.mcpServerNames = names
			a.mcpSummary = mcpSummary(servers)
			a.refreshMCPInventory()
			if len(manual) > 0 && manual[0] {
				a.notifySuccess("MCP 列表已刷新")
			}
		})
	}()
}

func (a *desktop) refreshMCPInventory() {
	if a.core == nil || a.win == nil {
		return
	}
	a.mcpF.ToolRevision++
	revision := a.mcpF.ToolRevision
	go func() {
		tools := a.core.ListTools()
		a.win.Update(func() {
			if a.closing || revision != a.mcpF.ToolRevision {
				return
			}
			a.toolInventory = tools
			a.toolCount = enabledToolCount(tools)
		})
	}()
}

func (a *desktop) importMCP() {
	if a.mcpF.Busy || a.mcpF.ImportBusy || a.core == nil || a.win == nil {
		return
	}
	a.mcpF.ImportBusy = true
	go func() {
		// 比对基准必须来自后端：页面缓存的 a.mcpText 可能还是空的（首次刷新还在飞），
		// 拿空基准去比对磁盘会把一次正常导入误报成“配置在编辑期间已变化”。
		baseRaw, err := a.core.GetMcpConfig()
		if err != nil {
			a.win.Update(func() {
				a.mcpF.ImportBusy = false
				a.notifyError("读取 MCP 配置失败：" + err.Error())
			})
			return
		}
		path, err := a.core.SelectJSONFile("导入 MCP 配置")
		if err != nil || path == "" {
			a.win.Update(func() {
				a.mcpF.ImportBusy = false
				if err != nil {
					a.notifyError(err.Error())
				}
			})
			return
		}
		data, err := a.core.ReadLocalTextFile(path)
		if err != nil {
			a.win.Update(func() {
				a.mcpF.ImportBusy = false
				a.notifyError(err.Error())
			})
			return
		}
		doc, err := parseMCPConfig(data)
		if err != nil {
			a.win.Update(func() {
				a.mcpF.ImportBusy = false
				a.notify("无法导入：MCP 配置不是有效的 JSON")
			})
			return
		}
		a.win.Update(func() {
			a.mcpF.ImportBusy = false
			a.saveMCPDoc(doc, mcpSaveOptions{replaceAll: true, baseRaw: baseRaw})
		})
	}()
}

func (a *desktop) exportMCP() {
	if strings.TrimSpace(a.mcpText) == "" {
		return
	}
	name := "ally-mcp-" + nowDate() + ".json"
	a.exportText(name, a.mcpText)
}

func nowDate() string {
	return time.Now().Format("2006-01-02")
}

// builtinToolKey identifies one filtered, sorted grid: the search text plus a
// fingerprint of the inventory it was derived from.
type builtinToolKey struct {
	query string
	count int
	sum   uint64
}

type builtinToolLists struct {
	core []backend.ToolDefinitionSummary
	rest []backend.ToolDefinitionSummary
}

// builtinToolKeyFor fingerprints the inventory without allocating. The pointer
// moves and every keystroke repaint the grid, so neither may re-lower and re-sort
// the whole inventory.
func builtinToolKeyFor(inventory []backend.ToolDefinitionSummary, query string) builtinToolKey {
	key := builtinToolKey{query: query, count: len(inventory), sum: 14695981039346656037}
	mix := func(s string) {
		for i := 0; i < len(s); i++ {
			key.sum = (key.sum ^ uint64(s[i])) * 1099511628211
		}
		key.sum = (key.sum ^ 0xff) * 1099511628211
	}
	for _, tool := range inventory {
		mix(tool.Name)
		if query != "" {
			// Only the search reads the description, so without a query it cannot
			// make the memo stale and stays out of the fingerprint.
			mix(tool.Description)
		}
		mix(tool.Source)
		mix(tool.Server)
		if tool.AvailabilityReason != "" {
			// The switch reads the reason when it is pressed, so a copy kept from before
			// the model was configured must not outlive it and refuse the toggle.
			mix(tool.AvailabilityReason)
		}
		flags := uint64(0)
		if tool.Enabled {
			flags |= 1
		}
		if tool.Protected {
			flags |= 2
		}
		key.sum = (key.sum ^ flags) * 1099511628211
	}
	return key
}

// builtinToolListsFor filters and sorts the inventory once per key.
func (a *desktop) builtinToolListsFor(key builtinToolKey) builtinToolLists {
	var lists builtinToolLists
	for _, tool := range a.toolInventory {
		if tool.Source == "mcp" {
			continue
		}
		if key.query != "" && !strings.Contains(strings.ToLower(tool.Name+" "+tool.Description), key.query) {
			continue
		}
		if tool.Protected {
			lists.core = append(lists.core, tool)
		} else {
			lists.rest = append(lists.rest, tool)
		}
	}
	sort.SliceStable(lists.rest, func(i, j int) bool { return lists.rest[i].Name < lists.rest[j].Name })
	sort.SliceStable(lists.core, func(i, j int) bool { return lists.core[i].Name < lists.core[j].Name })
	return lists
}

func (a *desktop) builtinToolsGrid(c *ui.Context) {
	// The column repeats the page's own gap and carries the grid's memo: the
	// built-in list is filtered and sorted once per change, not once per repaint.
	holder := ui.Column(c.Key("builtin-tools-list")).Gap(gapInline)
	query := strings.ToLower(strings.TrimSpace(a.mcpF.Search))
	lists := memoAt[memoOf[builtinToolKey, builtinToolLists]](holder, "builtin-tools").get(builtinToolKeyFor(a.toolInventory, query), a.builtinToolListsFor)
	holder.Children(func() {
		core, rest := lists.core, lists.rest
		render := func(tool backend.ToolDefinitionSummary) {
			rowCard(c, "builtin-"+tool.Name, func() {
				ui.Row(c).Gap(gapField).AlignItems(ui.Center).Children(func() {
					uikit.Tooltip(c, ui.Text(c, builtinToolLabel(tool.Name)).FontSize(fsSmall).Bold(), tool.Name)
					ui.Spacer(c)
					enabled := tool.Enabled
					if tool.Protected {
						uikit.SwitchNative(c.Key("builtin-switch-"+tool.Name), &enabled).Disabled(true).Label(tool.Name)
					} else {
						uikit.SwitchNative(c.Key("builtin-switch-"+tool.Name), &enabled).Disabled(a.mcpF.Busy || a.mcpF.ImportBusy).Label(tool.Name).OnClick(func() { a.toggleBuiltinTool(tool) })
					}
				})
				ui.Row(c).Gap(gapInline).AlignItems(ui.Center).Children(func() {
					neutralChip(c, tool.Name)
					if tool.Protected {
						fieldNote(c, "核心工具，不可停用", a.secondaryInk())
					}
				})
			})
		}
		if len(core) > 0 {
			fieldNote(c, "核心工具始终可用。", a.secondaryInk())
			ui.Grid(c.Key("builtin-core")).Columns(3).Gap(gapField).Children(func() {
				for _, tool := range core {
					render(tool)
				}
			})
		}
		ui.Grid(c.Key("builtin-rest")).Columns(3).Gap(gapField).Children(func() {
			for _, tool := range rest {
				render(tool)
			}
		})
		if len(core)+len(rest) == 0 {
			emptyState(c, "没有匹配的工具", "", a.secondaryInk())
		}
	})
}

var builtinToolLabels = map[string]string{
	"read": "读取文件", "edit": "编辑文件", "create": "创建文件", "delete": "删除路径", "command": "执行命令",
	"list_files": "列目录", "grep": "搜索内容", "screenshot": "截图", "image_generate": "生成图片", "image_edit": "编辑图片",
	"service": "后台服务", "wait": "等待", "ask": "提问", "suggest": "建议", "scheduled_task": "计划任务",
	"http_request": "HTTP 请求", "web_fetch": "抓取网页", "remote_read": "远程读取", "remote_edit": "远程编辑",
	"remote_create_file": "远程创建", "remote_delete_path": "远程删除", "remote_run_command": "远程命令",
	"remote_transfer": "远程传输", "ssh_cluster": "SSH", "calculate": "计算器", "render_html": "渲染 HTML",
	"plan": "计划", "subagent": "子代理", "skill": "技能",
}

func builtinToolLabel(name string) string {
	if label, ok := builtinToolLabels[name]; ok {
		return label
	}
	return name
}

func (a *desktop) toggleBuiltinTool(tool backend.ToolDefinitionSummary) {
	if a.mcpF.Busy || a.mcpF.ImportBusy {
		return
	}
	// Turning an image tool on without a usable picture model would fail on the
	// first call, so say so instead of saving the switch.
	if !tool.Enabled && tool.AvailabilityReason != "" {
		a.notify(tool.AvailabilityReason)
		return
	}
	a.mcpF.Busy = true
	a.mcpF.Revision++
	previous, done := a.configWriteSlot()
	go func() {
		defer close(done)
		waitConfigWrite(previous)
		cfg, err := a.core.GetConfig()
		if err == nil {
			disabled := make([]string, 0, len(cfg.DisabledTools)+1)
			for _, name := range cfg.DisabledTools {
				if name != tool.Name {
					disabled = append(disabled, name)
				}
			}
			if tool.Enabled {
				disabled = append(disabled, tool.Name)
			}
			cfg.DisabledTools = disabled
			err = a.core.SaveConfig(cfg)
		}
		a.win.Update(func() {
			a.mcpF.Busy = false
			if err != nil {
				a.notifyError(err.Error())
				return
			}
			a.config = cfg
			a.refreshMCPInventory()
			a.notifySuccess("工具设置已保存，新会话生效")
		})
	}()
}
