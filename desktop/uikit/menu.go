package uikit

import "github.com/egoist/mygo/ui"

// ContextMenu uses MyGo's measured, themed menu layout for application menus.
// Keep native keyboard navigation and dismissal without Windows owner-draw
// menu metrics, which can leave too little room for the last label glyph.
func ContextMenu(anchor ui.Element, build func(*ui.Menu)) ui.Element {
	return anchor.PopoverMenu(build)
}
