package uikit

import "github.com/egoist/mygo/ui"

type hoverMenuState struct {
	active, dismissed string
	keyboard          bool
}

// HoverMenus is a build-local adapter for MyGo v0.8.1's submenu re-entry bug:
// its MenuBase compares the pointer with the last highlighted position rather
// than the previous frame, missing a return to exactly that position after Esc.
// Only hover ownership is adapted. Each child remains a ContextMenuBase menu,
// with MyGo owning focus, item activation, keyboard navigation and dismissal.
// Do not retain HoverMenus or any of the rows across builds.
type HoverMenus struct {
	state   *hoverMenuState
	hovered string
	clear   bool
}

func NewHoverMenus(owner ui.Element) *HoverMenus {
	return &HoverMenus{state: ui.Local(owner, "hover-submenu", func() hoverMenuState { return hoverMenuState{} })}
}

// Item registers a provider row. Enter/Space on the parent menu opens it too;
// KeepOpen leaves the parent up while the child menu takes the focus.
func (h *HoverMenus) Item(c *ui.Context, id string, row ui.Element) {
	row.KeepOpen()
	if row.Hovered() {
		h.hovered = id
		h.state.keyboard = false
	}
	if row.Clicked() {
		h.state.dismissed = ""
		h.state.keyboard = c.KeyDown(ui.KeyEnter) || c.KeyDown(ui.KeySpace)
		h.hovered = id
	}
}

// ClearOnHover marks content outside any group, such as recent choices or the
// search field. It must not leave a child's panel over that content.
func (h *HoverMenus) ClearOnHover(row ui.Element) {
	h.clear = h.clear || row.Hovered()
}

// Popup builds at most one child after all parent rows have been registered.
func (h *HoverMenus) Popup(c *ui.Context, parent ui.Element, find func(string) ui.Element, o MenuOptions, build func(string, ui.MenuParts)) {
	st := h.state
	hovered := h.hovered
	if st.dismissed != "" {
		if hovered == st.dismissed {
			hovered = ""
		} else {
			st.dismissed = ""
		}
	}
	active, sticky := hovered, false
	if active == "" && !h.clear && st.active != "" {
		active, sticky = st.active, true
	}
	if active == "" {
		st.active = ""
		return
	}
	row := find(active)
	if !row.Valid() {
		st.active = ""
		return
	}
	open, inside := true, false
	child := ui.ContextMenuBase(c, row, &open)
	child.Popup(func(panel ui.Element) {
		MenuPanel(c, panel, o)
		panel.AttachTo(row, ui.AnchorTopEnd, ui.AnchorTopStart).MarginStart(-6)
		inside = panel.Hovered()
		build(active, child)
	})
	switch {
	case !open:
		st.dismissed, active = active, ""
	case sticky && !st.keyboard && !inside && !parent.Hovered():
		active = ""
	}
	st.active = active
}
