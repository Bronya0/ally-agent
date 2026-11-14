// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"ally-dev/desktop/uikit"
	"ally-dev/locales"
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

// Avoid rebuilding a very long checklist on every frame of a height animation.
// Large lists use a paint-only fade while the existing page scroll remains instant.
const mcpAnimatedChecklistLimit = 40

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

// The editor's options are the values TransportName reports, so a stored
// transport always matches an option and only the label reads differently.
const (
	mcpTransportStdio = "stdio"
	mcpTransportSSE   = "sse"
	mcpTransportHTTP  = "streamable-http"
)

// mcpTransport is the transport the open editor is on, inferred the way the
// connection does.
func (a *desktop) mcpTransport() string {
	if name := (backend.McpServerConfig{Transport: a.mcpF.Transport}).TransportName(); name != "" {
		return name
	}
	return mcpTransportStdio
}

func mcpTransportLabelFor(name string) string {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case mcpTransportSSE:
		return "SSE"
	case "http", "rest", mcpTransportHTTP:
		return "Streamable HTTP"
	case "", mcpTransportStdio:
		return "stdio"
	}
	return name
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
		uikit.Input(c.Key("mcp-search"), &a.mcpF.Search).Placeholder(locales.Text("搜索")).Label(locales.Text("搜索 MCP")).Width(200)
		ui.Spacer(c)
		flat(c, locales.Text("导入")).Disabled(a.mcpF.Busy || a.mcpF.ImportBusy).OnClick(func() { a.importMCP() })
		flat(c, locales.Text("导出")).Disabled(len(a.mcpServerNames) == 0).OnClick(func() { a.exportMCP() })
		flat(c, locales.Text("刷新")).Disabled(a.mcpF.Busy || a.mcpF.ImportBusy).OnClick(func() { a.refreshMCPAll(true) })
		a.linkMenu(c, "mcp-market", [][2]string{{locales.Text("魔搭 ModelScope"), "https://modelscope.cn/mcp"}, {locales.Text("火山引擎 Volcengine"), "https://www.volcengine.com/mcp"}, {"Glama", "https://glama.ai/mcp/servers"}, {"PulseMCP", "https://www.pulsemcp.com"}})
		primary(c, locales.Text("添加")).Disabled(a.mcpF.Busy || a.mcpF.ImportBusy).OnClick(func() { a.openMCPEditor("") })
	})
	mcpNoteLine(c, locales.Text("支持 stdio / SSE / Streamable HTTP；服务器配置自动保存，工具开关只影响新会话。"), a.secondaryInk())
	ui.Scroll(c).Grow(1).MinHeight(0).Children(func() {
		ui.Column(c).Gap(gapInline).Children(func() { a.mcpServerList(c) })
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
		notice(c, "error", locales.Text("读取 MCP 配置失败：")+data.err.Error(), a.secondaryInk())
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
		tools := list(status, "tools")
		rowCard(c, "mcp-"+name, func() {
			ui.Column(c.Key("mcp-body-" + name)).FillWidth().Children(func() {
				ui.Column(c).FillWidth().Gap(gapInline).Children(func() {
					ui.Row(c).Gap(gapField).AlignItems(ui.Center).Wrap().Children(func() {
						statusDot(c, str(status, "status"))
						ui.Text(c, name).FontSize(fsBody).Bold()
						neutralChip(c, mcpTransportLabel(entry, status))
						ui.Spacer(c)
						enabledCount := 0
						for _, item := range tools {
							if m, ok := item.(map[string]any); ok && !boolOf(m, "disabled") {
								enabledCount++
							}
						}
						if len(tools) > 0 {
							label := fmt.Sprintf(locales.Text("工具 %d/%d"), enabledCount, len(tools))
							btn := uikit.Tooltip(c, cardFlat(c, label), locales.Text("勾选的工具注入模型上下文，取消勾选即停用；对端新增工具默认启用；下一会话生效。"))
							btn.OnClick(func() { a.mcpF.ToolOpen[name] = !a.mcpF.ToolOpen[name] })
						} else if count := int(num(status, "toolCount")); count > 0 {
							fieldNote(c, fmt.Sprintf(locales.Text("%d 个工具"), count), a.secondaryInk())
						}
						stateText := mcpStatusText(str(status, "status"))
						uikit.Tooltip(c, ui.Text(c, stateText).FontSize(fsSmall).TextColor(toneColor(c, str(status, "status"), a.secondaryInk())), str(status, "error"))
						enabled := entry.Enabled == nil || *entry.Enabled
						uikit.SwitchNative(c.Key("mcp-enabled-"+name), &enabled).Label(locales.Text("启用 ") + name).Disabled(a.mcpF.Busy || a.mcpF.ImportBusy).OnClick(func() {
							entry.Enabled = boolPtr(enabled)
							doc := editDoc()
							doc.Servers[name] = entry
							a.saveMCPDoc(doc)
						})
						cardFlat(c, locales.Text("编辑")).Disabled(a.mcpF.Busy || a.mcpF.ImportBusy).OnClick(func() { a.openMCPEditor(name) })
						cardDanger(c, locales.Text("删除")).Disabled(a.mcpF.Busy || a.mcpF.ImportBusy).OnClick(func() {
							doc := editDoc()
							delete(doc.Servers, name)
							a.saveMCPDoc(doc)
						})
					})
					if errText := str(status, "error"); errText != "" {
						inlineError(c, "mcp-error-"+name, errText)
					}
				})
				// The checklist takes a provider so a document is cloned only on edit.
				checklist := func() {
					ui.Column(c).FillWidth().Padding(gapInline, 0, 0).Children(func() {
						a.mcpToolChecklist(c, name, entry, editDoc, status)
					})
				}
				if len(tools) <= mcpAnimatedChecklistLimit {
					uikit.Reveal(c.Key("mcp-tools-reveal-"+name), a.mcpF.ToolOpen[name], checklist)
				} else {
					uikit.FadeReveal(c.Key("mcp-tools-fade-"+name), a.mcpF.ToolOpen[name], checklist)
				}
			})
		})
	}
	if shown == 0 {
		if len(names) == 0 {
			emptyState(c, locales.Text("暂无 MCP 服务"), locales.Text("点击“添加”创建第一个服务。"), a.secondaryInk())
		} else {
			emptyState(c, locales.Text("无匹配结果"), "", a.secondaryInk())
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
	name := str(status, "transport")
	if name == "" {
		name = entry.transportName()
	}
	return mcpTransportLabelFor(name)
}

func (a *desktop) mcpToolChecklist(c *ui.Context, name string, entry mcpServerEntry, docOf func() *mcpConfigDoc, status map[string]any) {
	ui.Column(c).Key("mcp-tools-"+name).FillWidth().Padding(8, 10).Gap(2).Background(c.Theme().Background).Radius(radiusSmall).Children(func() {
		fieldNote(c, locales.Text("勾选的工具注入模型上下文，取消勾选即停用；对端新增工具默认启用；下一会话生效，不触发重连。"), a.secondaryInk())
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
			fieldNote(c, locales.Text("连接成功后这里会列出该服务发现的工具。"), a.secondaryInk())
		}
	})
}

func (a *desktop) openMCPEditor(name string) {
	doc, err := parseMCPConfig(a.mcpText)
	if err != nil {
		a.notifyError(locales.Text("读取 MCP 配置失败：") + err.Error())
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
	title := locales.Text("添加 MCP 服务")
	if a.mcpF.Editing != "" {
		title = locales.Text("编辑 MCP 服务")
	}
	height := float32(550)
	if a.mcpTransport() != mcpTransportStdio {
		height = 430
	}
	formDialog(c, &a.mcpF.EditorOpen, widthModal, height, title, "", func() {
		ui.Grid(c.Key("mcp-header-fields")).ColumnTracks(ui.Fr(1), ui.Fr(1)).FillWidth().Gap(gapSection).Children(func() {
			fieldRow(c, "mcp-name", locales.Text("服务名称"), &a.mcpF.Name, false)
			ui.Column(c).Gap(gapTight).Children(func() {
				fieldLabel(c, locales.Text("传输方式"))
				current := a.mcpTransport()
				transports := []uikit.Option{
					{Value: mcpTransportStdio, Label: mcpTransportLabelFor(mcpTransportStdio)},
					{Value: mcpTransportSSE, Label: mcpTransportLabelFor(mcpTransportSSE)},
					{Value: mcpTransportHTTP, Label: mcpTransportLabelFor(mcpTransportHTTP)},
				}
				if uikit.SelectField(c.Key("mcp-transport"), &current, transports, a.secondaryInk()).Label(locales.Text("传输方式")).FillWidth().Changed() {
					a.mcpF.Transport = current
				}
			})
		})
		if a.mcpTransport() == mcpTransportStdio {
			fieldRow(c, "mcp-command", locales.Text("命令"), &a.mcpF.Command, false)
			fieldLabel(c, locales.Text("参数"))
			textArea(c, "mcp-args", locales.Text("参数"), `["-y", "@playwright/mcp"]`, &a.mcpF.Args, 90).FillWidth()
			fieldNote(c, locales.Text("填写 JSON 字符串数组，保留参数中的空格和逗号；无参数可留空。"), a.secondaryInk())
			fieldLabel(c, locales.Text("环境变量"))
			textArea(c, "mcp-env", locales.Text("环境变量"), `{"API_KEY": "your-api-key"}`, &a.mcpF.Env, 70)
			fieldNote(c, locales.Text("JSON 对象，也支持每行 KEY=value。"), a.secondaryInk())
		} else {
			fieldRow(c, "mcp-url", "URL", &a.mcpF.URL, false)
			fieldNote(c, locales.Text("例如 http://127.0.0.1:9121/mcp（SSE 通常为 /sse）。"), a.secondaryInk())
			fieldLabel(c, locales.Text("请求头"))
			textArea(c, "mcp-headers", locales.Text("请求头"), `{"Authorization": "Bearer your-token"}`, &a.mcpF.Headers, 70)
			fieldNote(c, locales.Text("JSON 对象，也支持每行 Key:Value。"), a.secondaryInk())
		}
	}, func() {
		flat(c, locales.Text("取消")).OnClick(func() { a.mcpF.EditorOpen = false })
		primary(c, locales.Text("保存")).Disabled(a.mcpF.Busy || a.mcpF.ImportBusy).OnClick(func() { a.saveMCPEditor() })
	})
}

func (a *desktop) saveMCPEditor() {
	name := strings.TrimSpace(a.mcpF.Name)
	if name == "" {
		a.notify(locales.Text("请填写服务名称"))
		return
	}
	doc, err := parseMCPConfig(a.mcpText)
	if err != nil {
		a.notifyError(locales.Text("读取 MCP 配置失败：") + err.Error())
		return
	}
	if _, exists := doc.Servers[name]; exists && a.mcpF.Editing != name {
		a.notifyError(locales.Text("已存在同名 MCP 服务，请使用其他名称"))
		return
	}
	entry := doc.Servers[name]
	if a.mcpF.Editing != "" && a.mcpF.Editing != name {
		entry = doc.Servers[a.mcpF.Editing]
		delete(doc.Servers, a.mcpF.Editing)
	}
	transport := a.mcpTransport()
	if transport == mcpTransportHTTP {
		transport = "http" // the spelling other MCP clients write
	}
	entry.Transport = transport
	if transport == "stdio" {
		args, err := parseMCPArgs(a.mcpF.Args)
		if err != nil {
			a.notifyError(locales.Text("参数必须是 JSON 字符串数组：") + err.Error())
			return
		}
		entry.Command = strings.TrimSpace(a.mcpF.Command)
		entry.Args = args
		entry.Env, err = parsePairs(a.mcpF.Env, "=")
		if err != nil {
			a.notifyError(locales.Text("环境变量必须是 JSON 字符串对象：") + err.Error())
			return
		}
		entry.URL = ""
		entry.Headers = nil
	} else {
		entry.URL = strings.TrimSpace(a.mcpF.URL)
		entry.Headers, err = parsePairs(a.mcpF.Headers, ":")
		if err != nil {
			a.notifyError(locales.Text("请求头必须是 JSON 字符串对象：") + err.Error())
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

func parseMCPArgs(raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var values []any
	if err := json.Unmarshal([]byte(raw), &values); err != nil {
		return nil, err
	}
	if values == nil {
		return nil, fmt.Errorf("expected a JSON string array")
	}
	args := make([]string, len(values))
	for i, value := range values {
		text, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("argument %d must be a string", i+1)
		}
		args[i] = text
	}
	return args, nil
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
				a.notifyError(locales.Text("保存 MCP 配置失败：") + err.Error())
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
			a.notifySuccess(locales.Text("MCP 配置已保存"))
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
				a.notifyError(locales.Text("读取 MCP 配置失败：") + err.Error())
				return
			}
			a.mcpText = raw
			a.setMCPServers(servers)
			a.mcpServerNames = names
			a.mcpSummary = mcpSummary(servers)
			a.refreshMCPInventory()
			if len(manual) > 0 && manual[0] {
				a.notifySuccess(locales.Text("MCP 列表已刷新"))
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
				a.notifyError(locales.Text("读取 MCP 配置失败：") + err.Error())
			})
			return
		}
		path, err := a.core.SelectJSONFile(locales.Text("导入 MCP 配置"))
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
				a.notify(locales.Text("无法导入：MCP 配置不是有效的 JSON"))
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
