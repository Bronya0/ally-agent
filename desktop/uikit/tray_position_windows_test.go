// SPDX-License-Identifier: GPL-3.0-only
//go:build windows

package uikit

import (
	"image"
	"testing"
)

func TestTrayMenuPositionAnchorsVisiblePanelAtEachDPI(t *testing.T) {
	width, height := TrayMenuInitialSize(3, 2)
	at := image.Pt(600, 500)
	bounds := image.Rect(0, 0, 1920, 1080)
	for _, dpi := range []int{96, 120, 144, 168, 192} {
		size := image.Pt((width*dpi+48)/96, (height*dpi+48)/96)
		inset := (int(TrayMenuShadowInset)*dpi + 48) / 96
		origin := trayMenuPosition(at, size, bounds, inset, inset)
		if corner := origin.Add(image.Pt(inset, size.Y-inset)); corner != at {
			t.Fatalf("DPI %d anchors the shadow rather than the visible panel: corner=%v click=%v", dpi, corner, at)
		}
	}
}

func TestTrayMenuPositionAvoidsMonitorEdges(t *testing.T) {
	for _, tc := range []struct {
		name   string
		at     image.Point
		size   image.Point
		bounds image.Rectangle
		want   image.Point
	}{
		{"taskbar click", image.Pt(600, 1056), image.Pt(156, 110), image.Rect(0, 0, 1920, 1080), image.Pt(592, 954)},
		{"right edge", image.Pt(1900, 800), image.Pt(156, 110), image.Rect(0, 0, 1920, 1080), image.Pt(1752, 698)},
		{"top edge", image.Pt(400, 12), image.Pt(156, 110), image.Rect(0, 0, 1920, 1080), image.Pt(392, 4)},
		{"left edge", image.Pt(4, 500), image.Pt(156, 110), image.Rect(0, 0, 1920, 1080), image.Pt(0, 398)},
		{"bottom edge", image.Pt(600, 1078), image.Pt(156, 110), image.Rect(0, 0, 1920, 1080), image.Pt(592, 970)},
		{"top right corner", image.Pt(1918, 12), image.Pt(156, 110), image.Rect(0, 0, 1920, 1080), image.Pt(1764, 4)},
		{"negative monitor origin", image.Pt(-400, 600), image.Pt(156, 110), image.Rect(-1920, -200, 0, 880), image.Pt(-408, 498)},
		{"negative left edge", image.Pt(-1918, 600), image.Pt(156, 110), image.Rect(-1920, -200, 0, 880), image.Pt(-1920, 498)},
		{"small monitor", image.Pt(50, 40), image.Pt(80, 60), image.Rect(0, 0, 100, 80), image.Pt(20, 0)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := trayMenuPosition(tc.at, tc.size, tc.bounds, 8, 8)
			if got != tc.want {
				t.Fatalf("position=%v, want %v", got, tc.want)
			}
			if !(image.Rectangle{Min: got, Max: got.Add(tc.size)}).In(tc.bounds) {
				t.Fatalf("menu footprint at %v exceeded monitor %v", got, tc.bounds)
			}
		})
	}
}

func TestTrayMenuPositionKeepsClickAnchorAfterFitContent(t *testing.T) {
	at := image.Pt(600, 500)
	bounds := image.Rect(0, 0, 1920, 1080)
	for _, size := range []image.Point{image.Pt(156, 110), image.Pt(280, 190)} {
		origin := trayMenuPosition(at, size, bounds, 8, 8)
		if corner := origin.Add(image.Pt(8, size.Y-8)); corner != at {
			t.Fatalf("FitContent size %v changed the click anchor to %v", size, corner)
		}
	}
	// Widening next to the right edge keeps the opposite corner at the click.
	at.X = 1900
	for _, size := range []image.Point{image.Pt(156, 110), image.Pt(280, 190)} {
		origin := trayMenuPosition(at, size, bounds, 8, 8)
		if corner := origin.Add(size.Sub(image.Pt(8, 8))); corner != at {
			t.Fatalf("FitContent size %v changed the right-edge anchor to %v", size, corner)
		}
	}
}

func TestTrayMenuPositionAnchorsMainPanelWithSubmenuSpace(t *testing.T) {
	entries := []TrayMenuEntry{{Children: []TrayMenuEntry{{Key: "child"}}}}
	const shadow = int(TrayMenuShadowInset)
	leftInset := shadow + int(trayMenuSubmenuWidth(entries))
	if leftInset == shadow {
		t.Fatal("menu with children must reserve submenu space")
	}
	bounds := image.Rect(0, 0, 1920, 1080)
	size := image.Pt(leftInset+140+shadow, 110)
	for _, at := range []image.Point{image.Pt(1500, 1056), image.Pt(1900, 1056)} {
		origin := trayMenuPosition(at, size, bounds, leftInset, shadow)
		panelBottomLeft := origin.Add(image.Pt(leftInset, size.Y-shadow))
		panelBottomRight := origin.Add(image.Pt(size.X-shadow, size.Y-shadow))
		if at != panelBottomLeft && at != panelBottomRight {
			t.Fatalf("click %v missed main panel corners %v and %v", at, panelBottomLeft, panelBottomRight)
		}
	}
}
