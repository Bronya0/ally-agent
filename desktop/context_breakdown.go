// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"ally-dev/desktop/uikit"
	"ally-dev/locales"
	"strconv"

	"github.com/egoist/mygo/ui"
)

const contextToolListHeight float32 = 144

func (a *desktop) contextToolSchemas(c *ui.Context, st *chatUI) {
	b := st.Breakdown
	if len(b.ToolSchemaParts) == 0 {
		contextPart(c, locales.Text("工具 Schema · 内置"), b.ToolSchemas, a.secondaryInk())
		return
	}
	ui.Column(c.Key("context-schema-group")).FillWidth().Children(func() {
		contextPartFold(c, "tool-schemas", locales.Text("工具 Schema · 内置"), b.ToolSchemas, st.ToolSchemaPartsOpen, func() {
			st.ToolSchemaPartsOpen = !st.ToolSchemaPartsOpen
		}, a.secondaryInk())
		uikit.Reveal(c.Key("schema-parts"), st.ToolSchemaPartsOpen, func() {
			ui.List(c.Key("schemas"), nil, len(b.ToolSchemaParts)).FillWidth().
				Height(min(contextToolListHeight, float32(len(b.ToolSchemaParts))*24)).
				ItemKey(func(i int) any { return b.ToolSchemaParts[i].Label }).Rows(func(row ui.ListRow) {
				part := b.ToolSchemaParts[row.Index]
				ui.Row(row.Context).FillWidth().Height(24).Gap(gapInline).AlignItems(ui.Center).Children(func() {
					contextToolName(row.Context, part.Label, a.secondaryInk())
					uikit.Tooltip(row.Context, ui.Text(row.Context, tokenUnits(part.Tokens)).FontSize(fsTiny).Shrink(0), strconv.Itoa(part.Tokens)+" tokens")
				})
			})
		})
	})
}

func (a *desktop) contextToolUsage(c *ui.Context, st *chatUI) {
	b := st.Breakdown
	if len(b.ToolUsage) == 0 {
		contextPart(c, locales.Text("工具结果"), b.ToolResults, a.secondaryInk())
		return
	}
	ui.Column(c.Key("context-tool-group")).FillWidth().Children(func() {
		contextPartFold(c, "tool-usage", locales.Text("工具结果"), b.ToolResults, st.ToolUsageOpen, func() {
			st.ToolUsageOpen = !st.ToolUsageOpen
		}, a.secondaryInk())
		uikit.Reveal(c.Key("tool-usage-parts"), st.ToolUsageOpen, func() {
			ui.Column(c).FillWidth().Padding(gapInline, 0, 0).Children(func() {
				ui.Text(c, locales.Text("当前上下文估算；调用含参数，计入 AI 回复")).FontSize(fsTiny).TextColor(a.secondaryInk())
				ui.Row(c).FillWidth().Height(24).Gap(gapInline).AlignItems(ui.Center).Children(func() {
					ui.Text(c, locales.Text("工具")).FontSize(fsTiny).Grow(1).Basis(0).MinWidth(0).TextColor(a.secondaryInk())
					contextToolNumber(c, locales.Text("次数"), 32, a.secondaryInk())
					contextToolNumber(c, locales.Text("调用"), 44, a.secondaryInk())
					contextToolNumber(c, locales.Text("结果"), 44, a.secondaryInk())
				})
				ui.List(c.Key("tool-usage"), nil, len(b.ToolUsage)).FillWidth().
					Height(min(contextToolListHeight, float32(len(b.ToolUsage))*24)).
					ItemKey(func(i int) any { return b.ToolUsage[i].Name }).Rows(func(row ui.ListRow) {
					item := b.ToolUsage[row.Index]
					rc := row.Context
					ui.Row(rc).FillWidth().Height(24).Gap(gapInline).AlignItems(ui.Center).Children(func() {
						contextToolName(rc, item.Name, a.secondaryInk())
						contextToolNumber(rc, strconv.Itoa(item.Calls), 32, a.secondaryInk())
						uikit.Tooltip(rc, contextToolNumber(rc, tokenUnits(item.CallTokens), 44, rc.Theme().Text), strconv.Itoa(item.CallTokens)+" tokens")
						uikit.Tooltip(rc, contextToolNumber(rc, tokenUnits(item.ResultTokens), 44, rc.Theme().Text), strconv.Itoa(item.ResultTokens)+" tokens")
					})
				})
			})
		})
	})
}

func contextToolName(c *ui.Context, name string, ink ui.Color) {
	if name == "unknown" {
		name = locales.Text("未知工具")
	}
	uikit.Tooltip(c, ui.Text(c, name).FontSize(fsTiny).TextColor(ink).SingleLine().Grow(1).Basis(0).MinWidth(0), name)
}

func contextToolNumber(c *ui.Context, text string, width float32, ink ui.Color) ui.Element {
	return ui.Text(c, text).FontSize(fsTiny).TextColor(ink).SingleLine().Width(width).Shrink(0).TextAlign(ui.End)
}
