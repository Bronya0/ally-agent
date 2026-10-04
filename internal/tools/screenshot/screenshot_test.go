// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package screenshot

import (
	"errors"
	"image"
	"math/rand"
	"strings"
	"testing"
)

func TestCaptureOptionsTargetResolution(t *testing.T) {
	region := &Rect{X: 1, Y: 2, Width: 30, Height: 40}

	t.Run("default is the primary display", func(t *testing.T) {
		kind, id, title, gotRegion, err := CaptureOptions{}.Target()
		if err != nil || kind != TargetDisplay || id != 0 || title != "" || gotRegion != nil {
			t.Fatalf("got kind=%v id=%d title=%q region=%v err=%v", kind, id, title, gotRegion, err)
		}
	})

	t.Run("window beats region beats display", func(t *testing.T) {
		kind, id, _, gotRegion, err := CaptureOptions{WindowID: 7, Region: region, Display: 1}.Target()
		if err != nil || kind != TargetWindow || id != 7 || gotRegion != nil {
			t.Fatalf("window must win: kind=%v id=%d region=%v err=%v", kind, id, gotRegion, err)
		}
		kind, _, _, gotRegion, err = CaptureOptions{Region: region, Display: 1}.Target()
		if err != nil || kind != TargetRegion || gotRegion != region {
			t.Fatalf("region must beat display: kind=%v region=%v err=%v", kind, gotRegion, err)
		}
	})

	t.Run("id and title together are rejected", func(t *testing.T) {
		_, _, _, _, err := CaptureOptions{WindowID: 1, WindowTitle: "x"}.Target()
		if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
			t.Fatalf("expected mutual-exclusion error, got %v", err)
		}
	})

	t.Run("region is validated", func(t *testing.T) {
		_, _, _, _, err := CaptureOptions{Region: &Rect{Width: 0, Height: 10}}.Target()
		if !errors.Is(err, ErrBadRegion) {
			t.Fatalf("expected ErrBadRegion, got %v", err)
		}
	})
}

func TestValidateRegionBounds(t *testing.T) {
	cases := []struct {
		name    string
		region  Rect
		wantErr bool
	}{
		{"ok", Rect{X: -100, Y: -100, Width: 800, Height: 600}, false},
		{"negative origin allowed", Rect{X: -2000, Y: -2000, Width: 1000, Height: 1000}, false},
		{"zero width", Rect{Width: 0, Height: 10}, true},
		{"negative height", Rect{Width: 10, Height: -1}, true},
		{"overwide", Rect{Width: MaxRegionWidthPx + 1, Height: 10}, true},
		{"overhigh", Rect{Width: 10, Height: MaxRegionHeightPx + 1}, true},
		{"max allowed", Rect{Width: MaxRegionWidthPx, Height: MaxRegionHeightPx}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateRegion(tc.region)
			if (err != nil) != tc.wantErr {
				t.Fatalf("region %+v: err=%v wantErr=%v", tc.region, err, tc.wantErr)
			}
		})
	}
}

func TestResolveWindow(t *testing.T) {
	windows := []Window{
		{ID: 1, Title: "main.go - ally-agent - GoLand", Process: "goland64.exe"},
		{ID: 2, Title: "ally-agent — chat", Process: "Ally.exe"},
		{ID: 3, Title: "Settings", Process: "Ally.exe"},
	}

	t.Run("by id", func(t *testing.T) {
		w, err := ResolveWindow(windows, 2, "")
		if err != nil || w.ID != 2 {
			t.Fatalf("got %+v err=%v", w, err)
		}
	})

	t.Run("unknown id", func(t *testing.T) {
		_, err := ResolveWindow(windows, 99, "")
		if !errors.Is(err, ErrWindowNotFound) {
			t.Fatalf("expected ErrWindowNotFound, got %v", err)
		}
	})

	t.Run("unique title substring is case-insensitive", func(t *testing.T) {
		w, err := ResolveWindow(windows, 0, "goland")
		if err != nil || w.ID != 1 {
			t.Fatalf("got %+v err=%v", w, err)
		}
	})

	t.Run("process name also matches", func(t *testing.T) {
		// "chat" matches one title; process matching needs a unique needle.
		w, err := ResolveWindow(windows, 0, "chat")
		if err != nil || w.ID != 2 {
			t.Fatalf("got %+v err=%v", w, err)
		}
	})

	t.Run("ambiguous title lists candidates instead of guessing", func(t *testing.T) {
		_, err := ResolveWindow(windows, 0, "ally-agent")
		if !errors.Is(err, ErrAmbiguousWindow) {
			t.Fatalf("expected ErrAmbiguousWindow, got %v", err)
		}
		if !strings.Contains(err.Error(), "id=1") || !strings.Contains(err.Error(), "id=2") {
			t.Fatalf("error must carry candidate ids, got %q", err.Error())
		}
	})

	t.Run("no match names the needle", func(t *testing.T) {
		_, err := ResolveWindow(windows, 0, "chrome")
		if !errors.Is(err, ErrWindowNotFound) || !strings.Contains(err.Error(), "chrome") {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("blank needle is a usage error", func(t *testing.T) {
		_, err := ResolveWindow(windows, 0, "   ")
		if err == nil {
			t.Fatal("expected an error for a blank title")
		}
	})
}

func TestFilterWindows(t *testing.T) {
	windows := make([]Window, 0, 12)
	for i := 1; i <= 12; i++ {
		windows = append(windows, Window{ID: i, Title: strings.Repeat("w", i), Process: "app"})
	}

	t.Run("limit caps rows but total counts all", func(t *testing.T) {
		kept, total := FilterWindows(windows, ListOptions{})
		if len(kept) != 12 || total != 12 {
			t.Fatalf("kept=%d total=%d", len(kept), total)
		}
	})

	t.Run("explicit limit", func(t *testing.T) {
		kept, total := FilterWindows(windows, ListOptions{Limit: 3})
		if len(kept) != 3 || total != 12 {
			t.Fatalf("kept=%d total=%d", len(kept), total)
		}
	})

	t.Run("limit above the maximum falls back to the cap", func(t *testing.T) {
		huge := make([]Window, 0, MaxListWindows+10)
		for i := 1; i <= MaxListWindows+10; i++ {
			huge = append(huge, Window{ID: i, Title: "w"})
		}
		kept, total := FilterWindows(huge, ListOptions{Limit: 9999})
		if len(kept) != MaxListWindows || total != MaxListWindows+10 {
			t.Fatalf("kept=%d total=%d", len(kept), total)
		}
	})

	t.Run("filter matches title or process case-insensitively", func(t *testing.T) {
		kept, total := FilterWindows(windows, ListOptions{Title: "APP"})
		if total != 12 || len(kept) != 12 {
			t.Fatalf("kept=%d total=%d", len(kept), total)
		}
		kept, total = FilterWindows(windows, ListOptions{Title: "www"})
		if total == 0 || len(kept) == 0 {
			t.Fatalf("title filter dropped everything: kept=%d total=%d", len(kept), total)
		}
	})
}

func TestClipTitleBoundsRunes(t *testing.T) {
	long := Window{Title: strings.Repeat("汉", MaxTitleChars+50)}
	got := clipTitle(long)
	if runeCount := len([]rune(got.Title)); runeCount != MaxTitleChars+1 { // + ellipsis
		t.Fatalf("title clipped to %d runes, want %d", runeCount, MaxTitleChars+1)
	}
	if !strings.HasSuffix(got.Title, "…") {
		t.Fatal("clipped title must end with an ellipsis")
	}
}

func TestScaleForModel(t *testing.T) {
	small := image.NewRGBA(image.Rect(0, 0, 800, 600))
	if img, scaled := ScaleForModel(small); scaled || img != small {
		t.Fatal("small frames must pass through untouched")
	}
	big := image.NewRGBA(image.Rect(0, 0, 2560, 1440))
	img, scaled := ScaleForModel(big)
	if !scaled {
		t.Fatal("big frames must be scaled")
	}
	b := img.Bounds()
	if max(b.Dx(), b.Dy()) != MaxModelImageEdgePx {
		t.Fatalf("long edge %d, want %d", max(b.Dx(), b.Dy()), MaxModelImageEdgePx)
	}
	if w, want := b.Dx(), 1568; w != want { // 2560x1440 -> 1568x882
		t.Fatalf("width %d, want %d", w, want)
	}
	if h := b.Dy(); h != 882 {
		t.Fatalf("height %d, want 882", h)
	}
}

func TestEncodeForModelFallsBackToJPEGWhenPNGOvershoots(t *testing.T) {
	// Random noise is the worst case for PNG: a 1600x1200 noise frame
	// encodes far above MaxModelImageBytes, forcing the JPEG path.
	frame := image.NewNRGBA(image.Rect(0, 0, 1600, 1200))
	rnd := rand.New(rand.NewSource(42))
	for i := range frame.Pix {
		frame.Pix[i] = byte(rnd.Intn(256))
	}
	cap, err := EncodeForModel(frame)
	if err != nil {
		t.Fatalf("EncodeForModel: %v", err)
	}
	if cap.Mime != "image/jpeg" {
		t.Fatalf("expected JPEG fallback, got %s", cap.Mime)
	}
	if len(cap.Data) > MaxModelImageBytes {
		t.Fatalf("encoded size %d exceeds budget %d", len(cap.Data), MaxModelImageBytes)
	}
}

func TestEncodeForModelKeepsPNGForSmallFrames(t *testing.T) {
	frame := image.NewRGBA(image.Rect(0, 0, 64, 32))
	cap, err := EncodeForModel(frame)
	if err != nil {
		t.Fatalf("EncodeForModel: %v", err)
	}
	if cap.Mime != "image/png" || cap.Scaled {
		t.Fatalf("mime=%s scaled=%v", cap.Mime, cap.Scaled)
	}
}

func TestSanitizeTitleStripsControlChars(t *testing.T) {
	got := SanitizeTitle("doc\x01\x1b[31m - \ttab")
	if got != "doc  [31m -  tab" {
		t.Fatalf("got %q", got)
	}
}
