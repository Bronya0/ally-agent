// SPDX-License-Identifier: GPL-3.0-only
package desktop

import "ally-dev/desktop/pluginhost"

type pluginHostSyncKey struct {
	page, workspace string
	palette         pluginhost.Palette
	revision        uint64
	empty           bool
	hidden          bool
}

func (a *desktop) syncPluginHost() {
	hidden := a.mainWindowHidden || a.win != nil && a.win.IsMinimized()
	a.syncPluginHostVisibility(hidden)
}

// Visibility events must update the host even while native rendering is paused.
func (a *desktop) syncPluginHostVisibility(hidden bool) {
	if a.pluginHost == nil || a.closing {
		return
	}
	if len(a.plugins) == 0 {
		key := pluginHostSyncKey{empty: true}
		if a.pluginSyncReady && a.pluginSyncKey == key {
			return
		}
		a.pluginSyncKey, a.pluginSyncReady = key, true
		a.pluginHost.Update(pluginhost.Input{})
		return
	}
	workspace := ""
	if t := a.current(); t != nil {
		workspace = t.Workspace
	}
	p := a.currentPalette()
	palette := pluginhost.Palette{
		Name: p.name, Dark: p.dark, Bg: p.bg, Panel: p.panel, Chrome: p.chromeColor(),
		Line: p.line, Accent: p.accent, AccentText: p.accentText, Text: p.text,
		Muted: p.muted, Hover: p.hover, Hint: p.hint,
	}
	key := pluginHostSyncKey{page: a.page, workspace: workspace, palette: palette, revision: a.pluginsRevision, hidden: hidden}
	if a.pluginSyncReady && a.pluginSyncKey == key {
		return
	}
	a.pluginSyncKey, a.pluginSyncReady = key, true
	a.pluginHost.Update(pluginhost.Input{
		Page: a.page, Workspace: workspace, Plugins: a.plugins,
		Theme: a.pluginThemeFor(palette), Hidden: hidden,
	})
}

func (a *desktop) pluginThemeFor(palette pluginhost.Palette) pluginhost.Theme {
	if !a.pluginThemeReady || a.pluginThemePalette != palette {
		a.pluginThemePalette = palette
		a.pluginTheme = pluginhost.NewTheme(palette)
		a.pluginThemeReady = true
	}
	return a.pluginTheme
}
