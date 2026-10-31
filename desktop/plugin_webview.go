// SPDX-License-Identifier: GPL-3.0-only
package desktop

import "ally-dev/desktop/pluginhost"

func (a *desktop) syncPluginHost() {
	if a.pluginHost == nil || a.closing {
		return
	}
	if len(a.plugins) == 0 {
		a.pluginHost.Update(pluginhost.Input{})
		return
	}
	workspace := ""
	if t := a.current(); t != nil {
		workspace = t.Workspace
	}
	p := a.currentPalette()
	a.pluginHost.Update(pluginhost.Input{
		Page: a.page, Workspace: workspace, Plugins: a.plugins,
		Theme: pluginhost.NewTheme(pluginhost.Palette{
			Name: p.name, Dark: p.dark, Bg: p.bg, Panel: p.panel, Chrome: p.chromeColor(),
			Line: p.line, Accent: p.accent, AccentText: p.accentText, Text: p.text,
			Muted: p.muted, Hover: p.hover, Hint: p.hint,
		}),
	})
}
