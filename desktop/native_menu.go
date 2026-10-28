// SPDX-License-Identifier: GPL-3.0-only
package desktop

import "github.com/egoist/mygo"

// popupMenu shows items as the system's own menu. mygo.NewMenu with
// Menu.Popup is a different menu from ui.Menu, which Element.Menu opens: that
// one carries labels, separators, submenus and disabled states only. This one
// also carries check marks, accelerators, tooltips and icons, and it is what a
// menu on the desktop looks like on the platform.
//
// Popup returns once the menu closes, so it never runs inside the click that
// opened it: the main thread has to keep handling events for a menu to work,
// and a blocking call there would freeze the very menu it waits for. items are
// built before the call, on the frame's thread, because the clicks they carry
// touch app state that only that thread writes.
func (a *desktop) popupMenu(items []*mygo.MenuItem) {
	win := a.win
	if win == nil || len(items) == 0 {
		return
	}
	go func() {
		mygo.NewMenu(items).Popup(win)
	}()
}

// menuItem is one entry of popupMenu, with the check mark that shows the
// current value and the click that takes a new one.
func menuItem(label string, checked bool, choose func()) *mygo.MenuItem {
	return &mygo.MenuItem{
		Label:   label,
		Type:    mygo.MenuItemRadio,
		Checked: checked,
		Click:   func(*mygo.MenuItem, *mygo.Window) { choose() },
	}
}

// menuAction is one entry of popupMenu that does something rather than
// choosing among values.
func menuAction(label string, choose func()) *mygo.MenuItem {
	return &mygo.MenuItem{
		Label: label,
		Click: func(*mygo.MenuItem, *mygo.Window) { choose() },
	}
}

// menuSeparator divides groups of entries.
func menuSeparator() *mygo.MenuItem { return mygo.Separator() }
