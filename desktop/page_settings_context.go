// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"ally-dev/desktop/uikit"
	backend "ally-dev/internal/app"
	"ally-dev/locales"
	"fmt"
	"github.com/egoist/mygo/ui"
	"slices"
	"sort"
	"strings"
)

var contextLoadingOptions = backend.ContextLoadingOptions()

func toggledDisabledItem(items []string, id string, enabled bool) []string {
	result := make([]string, 0, len(items)+1)
	for _, item := range items {
		if item != id {
			result = append(result, item)
		}
	}
	if !enabled {
		result = append(result, id)
	}
	return result
}

func (a *desktop) settingsContextLoading(c *ui.Context) {
	a.settingsCard(c, "settings-context-sources", locales.Text("上下文加载"), locales.Text("默认开启；离开设置后保存，仅新会话生效。关闭自动加载不限制按需读取文件。"), func() {
		for _, option := range contextLoadingOptions {
			enabled := a.configDraft.ContextEnabled(option.ID)
			if control := a.settingsSwitch(c, "context-"+option.ID, locales.Text(option.Label), locales.Text(option.Description), &enabled); control.Changed() {
				if enabled {
					a.configDraft.DisabledContext = toggledDisabledItem(a.configDraft.DisabledContext, option.ID, true)
					continue
				}
				// Every source carries the same consequence, so one shared warning
				// covers all of them instead of a paragraph per switch.
				a.settings.ContextLoadingToDisable = option.ID
				a.settings.ContextLoadingConfirmOpen = true
			}
		}
		fieldNote(c, locales.Text("内置系统规则和平台环境始终加载。MCP 工具由 MCP 页的服务器与工具开关管理。"), a.secondaryInk())
	})
	a.settingsCard(c, "settings-context-tools", locales.Text("内置工具 Schema"), locales.Text("每个工具单独控制，新会话生效；核心文件与命令工具始终可用。"), func() {
		ui.Collapsible(c.Key("settings-builtin-tools"), locales.Text("内置工具开关"), &a.contextToolsOpen, func() {
			uikit.Input(c.Key("settings-builtin-search"), &a.builtinToolSearch).Placeholder(locales.Text("搜索")).FillWidth()
			a.builtinToolsGrid(c)
		}).FillWidth()
	})
}

// focusBuiltinToolsSettings selects Settings → General → Context loading with the
// built-in tool grid expanded. The page is already active when the welcome
// popover calls this, and the section is located by name rather than index.
func (a *desktop) focusBuiltinToolsSettings() {
	a.selectSettingsSection("通用")
	index := settingsGeneralSection("上下文加载")
	if index < 0 {
		return
	}
	if a.settings.Modules == nil {
		a.settings.Modules = map[string]int{}
	}
	a.settings.Modules["settingsGeneral"] = index
	a.contextToolsOpen = true
}

// contextLoadingConfirmDialog warns once for every optional source: turning one
// off has the same consequence whichever one it is, so the wording lives here
// instead of beside each switch.
func (a *desktop) contextLoadingConfirmDialog(c *ui.Context) {
	if !a.settings.ContextLoadingConfirmOpen {
		return
	}
	id := a.settings.ContextLoadingToDisable
	label := id
	for _, option := range contextLoadingOptions {
		if option.ID == id {
			label = locales.Text(option.Label)
			break
		}
	}
	closeDialog := func() {
		a.settings.ContextLoadingConfirmOpen = false
		a.settings.ContextLoadingToDisable = ""
	}
	dialog := modalCard(c.Key("context-loading-confirm"), &a.settings.ContextLoadingConfirmOpen, widthModalSmall, func() {
		modalTitle(c, fmt.Sprintf(locales.Text("关闭「%s」？"), label), "")
		ui.Text(c, locales.Text("关闭只对之后的新会话生效，已有对话保持原样；这些会话会缺少这类自动上下文，模型可能因此忽略项目约定、个人偏好或项目教训。它仍可主动读取对应文件，所以关闭不等于禁止访问。建议只关掉确定用不上的项。")).
			FontSize(fsSmall).TextColor(a.secondaryInk()).LineHeight(1.5).Selectable()
		modalActions(c, func() {
			flat(c, locales.Text("取消")).OnClick(closeDialog)
			primary(c, locales.Text("仍然关闭")).OnClick(func() {
				a.configDraft.DisabledContext = toggledDisabledItem(a.configDraft.DisabledContext, id, false)
				closeDialog()
			})
		})
	})
	if dialog.Dismissed() || !a.settings.ContextLoadingConfirmOpen {
		a.settings.ContextLoadingToDisable = ""
	}
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
	query := strings.ToLower(strings.TrimSpace(a.builtinToolSearch))
	lists := memoAt[memoOf[builtinToolKey, builtinToolLists]](holder, "builtin-tools").get(builtinToolKeyFor(a.toolInventory, query), a.builtinToolListsFor)
	holder.Children(func() {
		core, rest := lists.core, lists.rest
		render := func(tool backend.ToolDefinitionSummary) {
			rowCard(c, "builtin-"+tool.Name, func() {
				ui.Row(c).Gap(gapField).AlignItems(ui.Center).Children(func() {
					uikit.Tooltip(c, ui.Text(c, builtinToolLabel(tool.Name)).FontSize(fsSmall).Bold(), tool.Name)
					ui.Spacer(c)
					enabled := !slices.Contains(a.configDraft.DisabledTools, tool.Name) && tool.AvailabilityReason == ""
					if tool.Protected {
						enabled = true
					}
					if tool.Protected {
						uikit.SwitchNative(c.Key("builtin-switch-"+tool.Name), &enabled).Disabled(true).Label(tool.Name)
					} else {
						uikit.SwitchNative(c.Key("builtin-switch-"+tool.Name), &enabled).Label(tool.Name).OnClick(func() { a.setBuiltinToolEnabled(tool, enabled) })
					}
				})
				ui.Row(c).Gap(gapInline).AlignItems(ui.Center).Children(func() {
					neutralChip(c, tool.Name)
					if tool.Protected {
						fieldNote(c, locales.Text("核心工具，不可停用"), a.secondaryInk())
					}
				})
			})
		}
		if len(core) > 0 {
			fieldNote(c, locales.Text("核心工具始终可用。"), a.secondaryInk())
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
			emptyState(c, locales.Text("没有匹配的工具"), "", a.secondaryInk())
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
		return locales.Text(label)
	}
	return name
}

func (a *desktop) setBuiltinToolEnabled(tool backend.ToolDefinitionSummary, enabled bool) {
	if tool.Protected {
		return
	}
	if enabled && tool.AvailabilityReason != "" {
		a.notify(locales.Text(tool.AvailabilityReason))
		return
	}
	a.configDraft.DisabledTools = toggledDisabledItem(a.configDraft.DisabledTools, tool.Name, enabled)
}
