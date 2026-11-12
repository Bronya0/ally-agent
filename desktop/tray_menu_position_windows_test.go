// SPDX-License-Identifier: GPL-3.0-only
//go:build windows

package desktop

import (
	"ally-dev/desktop/uikit"
	"image"
	"testing"
)

func TestTrayMenuPositionAnchorsVisiblePanelAtEachDPI(t *testing.T) {
	width, height := uikit.TrayMenuInitialSize(3, 2)
	at := image.Pt(600, 500)
	bounds := image.Rect(0, 0, 1920, 1080)
	for _, dpi := range []int{96, 120, 144, 168, 192} {
		size := image.Pt((width*dpi+48)/96, (height*dpi+48)/96)
		inset := (int(uikit.TrayMenuShadowInset)*dpi + 48) / 96
		origin := trayMenuPosition(at, size, bounds, inset)
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
			got := trayMenuPosition(tc.at, tc.size, tc.bounds, 8)
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
		origin := trayMenuPosition(at, size, bounds, 8)
		if corner := origin.Add(image.Pt(8, size.Y-8)); corner != at {
			t.Fatalf("FitContent size %v changed the click anchor to %v", size, corner)
		}
	}
	// Widening next to the right edge keeps the opposite corner at the click.
	at.X = 1900
	for _, size := range []image.Point{image.Pt(156, 110), image.Pt(280, 190)} {
		origin := trayMenuPosition(at, size, bounds, 8)
		if corner := origin.Add(size.Sub(image.Pt(8, 8))); corner != at {
			t.Fatalf("FitContent size %v changed the right-edge anchor to %v", size, corner)
		}
	}
}
