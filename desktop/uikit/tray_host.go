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
	Icon                      *ui.SVG
	Disabled, SeparatorBefore bool
	// NativeSeparatorBefore overrides SeparatorBefore for the Linux system menu.
	// Nil uses SeparatorBefore, including when no override is needed.
	NativeSeparatorBefore *bool
	OnClick               func()
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
	CanOpen  func() bool
	Theme    func(*ui.Context) *ui.Theme
	MaxWidth float32
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
		menu, err = nativeMenu(items)
		if err != nil {
			return nil, err
		}
	}
	t.native, err = mygo.NewTray(mygo.TrayOptions{ID: o.ID, Icon: o.Icon, IconIsTemplate: o.IconIsTemplate, ToolTip: o.Tooltip, Menu: menu})
	if err != nil {
		return nil, err
	}
	t.options.Icon = nil
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
		menu, err := nativeMenu(items)
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
	copyItems := append([]TrayItem(nil), items...)
	keys := make(map[string]bool, len(copyItems))
	for i, item := range copyItems {
		if item.Key == "" {
			item.Key = item.Label
			copyItems[i].Key = item.Key
		}
		if item.Key == "" || keys[item.Key] {
			return nil, fmt.Errorf("tray menu keys or labels must be nonempty and unique: %q", item.Key)
		}
		keys[item.Key] = true
		if item.NativeSeparatorBefore != nil {
			v := *item.NativeSeparatorBefore
			copyItems[i].NativeSeparatorBefore = &v
		}
	}
	return copyItems, nil
}

func menuEntries(items []TrayItem) []TrayMenuEntry {
	entries := make([]TrayMenuEntry, len(items))
	for i, item := range items {
		entries[i] = TrayMenuEntry{Key: item.Key, Label: item.Label, Icon: item.Icon, Disabled: item.Disabled, SeparatorBefore: item.SeparatorBefore}
	}
	return entries
}

func nativeMenu(items []TrayItem) (*mygo.Menu, error) {
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
		if item.Icon != nil {
			var err error
			icon, err = mygo.NewMenuIconSVG(item.Icon, true)
			if err != nil {
				return nil, fmt.Errorf("tray menu icon %q: %w", item.Key, err)
			}
		}
		action := item.OnClick
		entries = append(entries, &mygo.MenuItem{ID: item.Key, Label: item.Label, Icon: icon, Disabled: item.Disabled, Click: func(*mygo.MenuItem, *mygo.Window) {
			if action != nil {
				action()
			}
		}})
	}
	return mygo.NewMenu(entries), nil
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
	capture := t.options.CapturePosition
	if capture == nil {
		capture = capturePosition
	}
	position := capture()
	p := &trayPopupState{open: true}
	t.popup = p
	separators := 0
	for i, item := range t.items {
		if i > 0 && item.SeparatorBefore {
			separators++
		}
	}
	width, height := TrayMenuInitialSize(len(t.items), separators)
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
			if choice >= 0 && choice < len(t.items) {
				t.finish(p, t.items[choice].OnClick)
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
