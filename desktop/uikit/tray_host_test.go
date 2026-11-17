package uikit

import (
	"bytes"
	"image/png"
	"testing"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

func TestNativeMenuActionsAndSeparators(t *testing.T) {
	var calls [2]int
	icon := ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16"><circle cx="8" cy="8" r="5" fill="currentColor"/></svg>`))
	noSeparator := false
	items := []TrayItem{
		{Key: "first", Label: "Open", Icon: icon, OnClick: func() { calls[0]++ }},
		{Key: "second", Label: "Update", SeparatorBefore: true, NativeSeparatorBefore: &noSeparator, OnClick: func() { calls[1]++ }},
		{Key: "disabled", Label: "Disabled", Disabled: true, SeparatorBefore: true},
	}
	menu, err := nativeMenu(items)
	if err != nil {
		t.Fatal(err)
	}
	got := menu.Items()
	if len(got) != 4 || got[2].Type != mygo.MenuItemSeparator || !got[3].Disabled {
		t.Fatalf("unexpected menu grouping: %+v", got)
	}
	if got[0].Icon == nil || got[0].Icon.PlatformMenuIcon() == nil || !got[0].Icon.PlatformMenuIcon().Template {
		t.Fatal("native menu must use a template icon")
	}
	got[1].Click(got[1], nil)
	if calls != [2]int{0, 1} {
		t.Fatalf("wrong callback: %v", calls)
	}
}

func TestItemsValidateAndCopyOptions(t *testing.T) {
	if _, err := prepareItems([]TrayItem{{Key: "x"}, {Key: "x"}}); err == nil {
		t.Fatal("duplicate key accepted")
	}
	if _, err := prepareItems([]TrayItem{{}}); err == nil {
		t.Fatal("empty key accepted")
	}
	if derived, err := prepareItems([]TrayItem{{Label: "Open"}}); err != nil || derived[0].Key != "Open" {
		t.Fatalf("label was not used as default key: %+v, %v", derived, err)
	}
	flag := true
	items, err := prepareItems([]TrayItem{{Key: "a", Label: "before", NativeSeparatorBefore: &flag}})
	if err != nil {
		t.Fatal(err)
	}
	flag = false
	if !*items[0].NativeSeparatorBefore {
		t.Fatal("caller changed the stored separator")
	}
}

func TestDefaultIconHasTransparentBackground(t *testing.T) {
	data, err := defaultIcon()
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	_, _, _, corner := img.At(0, 0).RGBA()
	_, _, _, center := img.At(8, 8).RGBA()
	if corner != 0 || center == 0 {
		t.Fatal("default tray icon must contain visible artwork on transparency")
	}
}

func TestStalePopupCannotCloseReplacement(t *testing.T) {
	old := &trayPopupState{open: true}
	current := &trayPopupState{open: true}
	tray := &TrayHost{popup: current}
	tray.finish(old, func() { t.Fatal("stale action ran") })
	if old.pending || tray.popup != current || !current.open {
		t.Fatal("stale popup affected replacement")
	}
	tray.CloseMenu()
	tray.CloseMenu()
	if tray.popup != nil || current.open {
		t.Fatal("closing did not clear popup")
	}
}
