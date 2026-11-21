// Tray hosting methods and callbacks run on the MyGo UI thread.
package uikit

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"runtime"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// TrayItem is a menu action. Key defaults to Label; set a stable key explicitly
// when labels can change or the menu can contain duplicate labels.
type TrayItem struct {
	Key, Label                string
	AccessibilityLabel        string
	Icon                      *ui.SVG
	StatusColor               ui.Color
	Checkable, Checked        bool
	Disabled, SeparatorBefore bool
	// NativeSeparatorBefore overrides SeparatorBefore for the Linux system menu.
	// Nil uses SeparatorBefore, including when no override is needed.
	NativeSeparatorBefore *bool
	OnClick               func()
	Children              []TrayItem
}

// TrayOptions supplies tray content and optional presentation overrides.
// NewTray must be called after MyGo is ready. Zero values use the built-in menu
// theme, width and platform-specific placement.
type TrayOptions struct {
	ID                       string
	Icon                     []byte
	IconIsTemplate           bool
	Tooltip, MenuLabel, Font string
	Items                    []TrayItem
	OnClick                  func()
	// CanOpen can veto opening while the host is shutting down.
	CanOpen func() bool
	// ItemsForOpen snapshots dynamic content immediately before opening a flyout.
	ItemsForOpen func() []TrayItem
	Theme        func(*ui.Context) *ui.Theme
	MaxWidth     float32
	// Placement and Gap control MyGo's tray anchor. On Windows, use a
	// CapturePosition returning nil to opt out of the default click anchor.
	Placement *mygo.Placement
	Gap       int
	// CapturePosition is called in the right-click callback before layout.
	// Its result is applied to the hidden flyout before it is shown. Nil uses
	// the built-in platform behavior; returning nil keeps MyGo's tray anchor.
	CapturePosition func() func(*mygo.Window)
}

type trayPopupState struct {
	win           *mygo.Window
	open, pending bool
}

// TrayHost owns the native tray, its event subscriptions and any open flyout.
type TrayHost struct {
	native             *mygo.Tray
	options            TrayOptions
	baseTheme          *ui.Theme
	nativeIcons        map[*ui.SVG]*mygo.MenuIcon
	items              []TrayItem
	entries            []TrayMenuEntry
	popup              *trayPopupState
	offClick, offRight func()
	closed             bool
}

// NewTray creates the tray and installs the platform's default menu behavior.
func NewTray(o TrayOptions) (*TrayHost, error) {
	items, err := prepareItems(o.Items)
	if err != nil {
		return nil, err
	}
	if o.MenuLabel == "" {
		o.MenuLabel = o.Tooltip
		if o.MenuLabel == "" {
			o.MenuLabel = "Menu"
		}
	}
	if len(o.Icon) == 0 {
		o.Icon, err = defaultIcon()
		if err != nil {
			return nil, err
		}
		o.IconIsTemplate = true
	}
	o.Items = nil
	t := &TrayHost{options: o, baseTheme: TrayMenuTheme(o.Font), items: items, entries: menuEntries(items)}
	var menu *mygo.Menu
	if runtime.GOOS == "linux" {
		t.nativeIcons = make(map[*ui.SVG]*mygo.MenuIcon)
		menu, err = nativeMenu(items, t.nativeIcons)
		if err != nil {
			return nil, err
		}
	}
	t.native, err = mygo.NewTray(mygo.TrayOptions{ID: o.ID, Icon: o.Icon, IconIsTemplate: o.IconIsTemplate, ToolTip: o.Tooltip, Menu: menu})
	if err != nil {
		return nil, err
	}
	t.options.Icon = nil
	t.options.Items = nil
	if runtime.GOOS != "linux" {
		onClick := o.OnClick
		if onClick == nil {
			onClick = t.ToggleMenu
		}
		t.offClick = t.native.OnClick(onClick)
		t.offRight = t.native.OnRightClick(t.ToggleMenu)
	}
	return t, nil
}

// defaultIcon is a small, monochrome template for callers without artwork.
func defaultIcon() ([]byte, error) {
	img := image.NewNRGBA(image.Rect(0, 0, 16, 16))
	for y := 2; y < 14; y++ {
		for x := 2; x < 14; x++ {
			dx, dy := 2*x-15, 2*y-15
			if dx*dx+dy*dy <= 121 {
				img.SetNRGBA(x, y, color.NRGBA{A: 255})
			}
		}
	}
	var data bytes.Buffer
	if err := png.Encode(&data, img); err != nil {
		return nil, fmt.Errorf("encode default tray icon: %w", err)
	}
	return data.Bytes(), nil
}

// SetItems replaces menu items atomically; Linux keeps its prior menu on error.
// Open flyouts update on the next frame, and action callbacks use the item that
// was selected rather than a later list at the same index.
func (t *TrayHost) SetItems(items []TrayItem) error {
	if t == nil || t.closed {
		return fmt.Errorf("tray is closed")
	}
	items, err := prepareItems(items)
	if err != nil {
		return err
	}
	if runtime.GOOS == "linux" {
		menu, err := nativeMenu(items, t.nativeIcons)
		if err != nil {
			return err
		}
		t.native.SetMenu(menu)
	}
	t.items = items
	t.entries = menuEntries(items)
	if t.popup != nil && t.popup.win != nil {
		t.popup.win.Invalidate()
	}
	return nil
}

func prepareItems(items []TrayItem) ([]TrayItem, error) {
	return prepareTrayItems(items, make(map[string]bool))
}

func prepareTrayItems(items []TrayItem, keys map[string]bool) ([]TrayItem, error) {
	copyItems := append([]TrayItem(nil), items...)
	for i, item := range copyItems {
		if item.Key == "" {
			item.Key = item.Label
			copyItems[i].Key = item.Key
		}
		if item.Key == "" || keys[item.Key] {
			return nil, fmt.Errorf("tray menu keys or labels must be nonempty and unique: %q", item.Key)
		}
		keys[item.Key] = true
		if len(item.Children) > 0 {
			children, err := prepareTrayItems(item.Children, keys)
			if err != nil {
				return nil, err
			}
			copyItems[i].Children = children
		}
		if item.NativeSeparatorBefore != nil {
			v := *item.NativeSeparatorBefore
			copyItems[i].NativeSeparatorBefore = &v
		}
	}
	return copyItems, nil
}

func menuEntries(items []TrayItem) []TrayMenuEntry {
	if len(items) == 0 {
		return nil
	}
	entries := make([]TrayMenuEntry, len(items))
	for i, item := range items {
		entries[i] = TrayMenuEntry{Key: item.Key, Label: item.Label, AccessibilityLabel: item.AccessibilityLabel, Icon: item.Icon, StatusColor: item.StatusColor, Checkable: item.Checkable, Checked: item.Checked, Disabled: item.Disabled, SeparatorBefore: item.SeparatorBefore}
		entries[i].Children = menuEntries(item.Children)
	}
	return entries
}

func nativeMenu(items []TrayItem, icons map[*ui.SVG]*mygo.MenuIcon) (*mygo.Menu, error) {
	entries, err := nativeMenuItems(items, icons)
	if err != nil {
		return nil, err
	}
	return mygo.NewMenu(entries), nil
}

func nativeMenuItems(items []TrayItem, icons map[*ui.SVG]*mygo.MenuIcon) ([]*mygo.MenuItem, error) {
	entries := make([]*mygo.MenuItem, 0, len(items)*2)
	for i, item := range items {
		separator := item.SeparatorBefore
		if item.NativeSeparatorBefore != nil {
			separator = *item.NativeSeparatorBefore
		}
		if i > 0 && separator {
			entries = append(entries, mygo.Separator())
		}
		var icon *mygo.MenuIcon
		if item.Icon != nil && !item.Checkable {
			icon = icons[item.Icon]
			if icon == nil {
				var err error
				icon, err = mygo.NewMenuIconSVG(item.Icon, true)
				if err != nil {
					return nil, fmt.Errorf("tray menu icon %q: %w", item.Key, err)
				}
				if icons != nil && len(icons) < 32 {
					icons[item.Icon] = icon
				}
			}
		}
		var children []*mygo.MenuItem
		if len(item.Children) > 0 {
			var err error
			children, err = nativeMenuItems(item.Children, icons)
			if err != nil {
				return nil, err
			}
		}
		kind := mygo.MenuItemNormal
		if len(children) > 0 {
			kind = mygo.MenuItemSubmenu
		}
		if item.Checkable {
			kind = mygo.MenuItemRadio
		}
		var click func(*mygo.MenuItem, *mygo.Window)
		if action := item.OnClick; action != nil && !item.Disabled {
			click = func(*mygo.MenuItem, *mygo.Window) {
				action()
			}
		}
		entries = append(entries, &mygo.MenuItem{ID: item.Key, Label: item.Label, Type: kind, Submenu: children, Checked: item.Checked, Icon: icon, Disabled: item.Disabled, Click: click})
	}
	return entries, nil
}

// SetIcon and SetTooltip update the native tray without recreating it.
func (t *TrayHost) SetIcon(icon []byte, template bool) error {
	if t == nil || t.closed {
		return fmt.Errorf("tray is closed")
	}
	return t.native.SetIcon(icon, template)
}
func (t *TrayHost) SetTooltip(text string) {
	if t != nil && !t.closed {
		t.native.SetToolTip(text)
	}
}

// ToggleMenu opens or dismisses the custom flyout on Windows and macOS.
func (t *TrayHost) ToggleMenu() {
	if t == nil || t.closed || runtime.GOOS == "linux" || (t.options.CanOpen != nil && !t.options.CanOpen()) {
		return
	}
	if t.popup != nil {
		t.CloseMenu()
		return
	}
	if t.options.ItemsForOpen != nil {
		if err := t.SetItems(t.options.ItemsForOpen()); err != nil {
			return
		}
	}
	submenuWidth := int(trayMenuSubmenuWidth(t.entries))
	var position func(*mygo.Window)
	if capture := t.options.CapturePosition; capture != nil {
		position = capture()
	} else {
		position = capturePosition(submenuWidth)
	}
	p := &trayPopupState{open: true}
	t.popup = p
	separators := 0
	for i, item := range t.items {
		if i > 0 && item.SeparatorBefore {
			separators++
		}
	}
	width, height := TrayMenuInitialSize(len(t.items), separators)
	width += submenuWidth
	placement := mygo.PlacementBottom
	if t.options.Placement != nil {
		placement = *t.options.Placement
	}
	p.win = mygo.NewFlyout(mygo.FlyoutOptions{Tray: t.native, Placement: placement, Gap: t.options.Gap, Width: width, Height: height, FitContent: true, Focusable: true, Hidden: true,
		Content: ui.View(func(c *ui.Context) {
			theme := t.baseTheme
			if t.options.Theme != nil {
				if custom := t.options.Theme(c); custom != nil {
					theme = custom
				}
			}
			c.SetTheme(theme.WithPreferences(c.Preferences()))
			c.Root().Background(ui.Transparent)
			choice := TrayMenu(c.Key("tray-menu"), &p.open, t.options.MenuLabel, t.entries, TrayMenuOptions{MaxWidth: t.options.MaxWidth})
			if choice >= 0 {
				t.finish(p, trayAction(t.items, choice))
			} else if !p.open {
				t.finish(p, nil)
			}
		})})
	p.win.OnClosed(func() {
		p.open = false
		if t.popup == p {
			t.popup = nil
		}
	})
	if position != nil {
		position(p.win)
	}
	p.win.Show()
}

func trayAction(items []TrayItem, choice int) func() {
	if choice < 0 {
		return nil
	}
	if choice < len(items) {
		return items[choice].OnClick
	}
	choice -= len(items)
	for _, item := range items {
		if choice < len(item.Children) {
			return item.Children[choice].OnClick
		}
		choice -= len(item.Children)
	}
	return nil
}

func (t *TrayHost) finish(p *trayPopupState, action func()) {
	if t == nil || t.closed || t.popup != p || p.pending || p.win == nil {
		return
	}
	p.pending = true
	p.win.Update(func() {
		if t.closed || t.popup != p || p.win.IsDestroyed() {
			return
		}
		t.CloseMenu()
		if action != nil {
			action()
		}
	})
}

// CloseMenu and Close are idempotent. Close removes listeners and the tray.
func (t *TrayHost) CloseMenu() {
	if t == nil {
		return
	}
	p := t.popup
	t.popup = nil
	if p != nil {
		p.open = false
		if p.win != nil {
			p.win.Destroy()
		}
	}
}
func (t *TrayHost) Close() {
	if t == nil || t.closed {
		return
	}
	t.closed = true
	t.CloseMenu()
	t.nativeIcons = nil
	if t.offClick != nil {
		t.offClick()
	}
	if t.offRight != nil {
		t.offRight()
	}
	if t.native != nil {
		t.native.SetMenu(nil)
		t.native.Destroy()
		t.native = nil
	}
}
