// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
// Package screenshot captures still images of the local screen and windows.
//
// Layering (see AGENTS.md): this package is the pure algorithm layer — no
// internal/app symbols, no workspace state. Platform capture lives in the
// GOOS-tagged files (capture_windows.go / capture_darwin.go /
// capture_linux.go); everything shared — target resolution, region
// validation, model-input downscaling and encoding — lives here so the
// per-platform files only implement "give me pixels for this rectangle".
package screenshot

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"strings"
	"time"
	"unicode"

	"golang.org/x/image/draw"
)

// Bounds shared with the schema declarations in internal/tools/shared (the
// schema and the runtime read the same numbers so they cannot drift).
const (
	// MaxRegionWidthPx / MaxRegionHeightPx bound one capture request.
	MaxRegionWidthPx  = 7680
	MaxRegionHeightPx = 4320
	// MaxListWindows bounds one screenshot action=list response.
	MaxListWindows = 50
	// MaxModelImageEdgePx caps the longest image edge handed to the model.
	// Anthropic's documented sweet spot for vision input is ~1568px on the
	// long edge; larger captures are downscaled, never upscaled.
	MaxModelImageEdgePx = 1568
	// MaxModelImageBytes caps the encoded image size. Oversized PNGs (screen
	// photos, gradients) fall back to JPEG and then to further downscaling
	// so one capture cannot blow up the context budget.
	MaxModelImageBytes = 1500_000
	// MaxTitleChars bounds one window title inside results. Titles are
	// model-facing user data; the cap keeps one pathological window from
	// flooding a list response.
	MaxTitleChars = 200
	// focusSettleDelay gives the compositor a beat after raising a window
	// before the pixels on screen actually belong to it.
	focusSettleDelay = 150 * time.Millisecond
)

// tailBytes keeps the tail of a failed command's output for error context.
func tailBytes(out []byte) string {
	s := strings.TrimSpace(string(out))
	if len(s) > 400 {
		s = s[len(s)-400:]
	}
	return s
}

// Sentinel errors surfaced as coded tool errors by the orchestration layer.
var (
	ErrWindowNotFound     = errors.New("no matching visible window")
	ErrAmbiguousWindow    = errors.New("window title matches multiple windows; pass windowId from the list instead")
	ErrCaptureUnsupported = errors.New("screen capture is not supported in this session (no graphics session or missing system tools)")
	ErrCaptureFailed      = errors.New("screen capture failed")
	ErrBadRegion          = errors.New("invalid capture region")
	ErrDisplayNotFound    = errors.New("display index out of range")
)

// Window is one visible top-level window. ID is the capture target handle
// (HWND on Windows, CGWindowID on macOS, X11 window id on Linux); it is only
// meaningful within the session that produced it.
type Window struct {
	ID      int    `json:"id"`
	Title   string `json:"title"`
	Process string `json:"process,omitempty"`
	X       int    `json:"x"`
	Y       int    `json:"y"`
	Width   int    `json:"width"`
	Height  int    `json:"height"`
	Focused bool   `json:"focused,omitempty"`
}

// ListOptions filters a platform window enumeration.
type ListOptions struct {
	// Title is a case-insensitive substring filter; empty lists everything.
	Title string
	// Limit caps the returned rows; 0 means MaxListWindows.
	Limit int
}

// Rect is one screen-space rectangle in physical pixels. Coordinates follow
// each platform's virtual-screen convention (primary display's top-left is
// the origin; secondary monitors may carry negative coordinates).
type Rect struct {
	X      int `json:"x"`
	Y      int `json:"y"`
	Width  int `json:"width"`
	Height int `json:"height"`
}

// CaptureOptions selects what one capture call targets. Exactly one target
// wins, resolved by TargetKind: WindowID/WindowTitle > Region > Display.
type CaptureOptions struct {
	// WindowID captures the (screen) rectangle of one window from
	// ListWindows. 0 means unset.
	WindowID int
	// WindowTitle resolves through ResolveWindow when WindowID is unset.
	WindowTitle string
	// Region captures an explicit screen rectangle. Nil means unset.
	Region *Rect
	// Display selects a monitor by index (0 = primary) for full-screen
	// captures. Ignored for window and region targets.
	Display int
	// Focus brings the target window to the front before capturing; without
	// it the capture shows whatever is visibly on top of that rectangle.
	Focus bool
}

// ModelImage is one encoded image ready for model input.
type ModelImage struct {
	Image  image.Image
	Mime   string // "image/png" or "image/jpeg"
	Data   []byte
	Scaled bool // true when the frame was downscaled to fit model limits
}

// TargetKind classifies the effective target of a CaptureOptions after
// validation. It is the single authority both the orchestrator's result
// metadata and the platform files switch on.
type TargetKind int

const (
	TargetDisplay TargetKind = iota
	TargetWindow
	TargetRegion
)

// CaptureSource records what one capture actually targeted, for the tool
// result metadata the orchestrator reports.
type CaptureSource struct {
	Kind    TargetKind
	Window  *Window
	Region  *Rect
	Display int
}

// Capture resolves the target, grabs the frame on the current platform,
// downscales it to model-input limits and encodes it. Window targets are
// resolved against a fresh enumeration so a stale id fails loudly instead
// of capturing a recycled handle.
func Capture(opts CaptureOptions) (ModelImage, CaptureSource, error) {
	kind, id, title, region, err := opts.Target()
	if err != nil {
		return ModelImage{}, CaptureSource{}, err
	}
	var frame image.Image
	source := CaptureSource{Kind: kind, Region: region, Display: opts.Display}
	switch kind {
	case TargetWindow:
		windows, listErr := listWindows()
		if listErr != nil {
			return ModelImage{}, CaptureSource{}, listErr
		}
		win, resolveErr := ResolveWindow(windows, id, title)
		if resolveErr != nil {
			return ModelImage{}, CaptureSource{}, resolveErr
		}
		source.Window = &win
		frame, err = captureWindow(win, opts.Focus)
	case TargetRegion:
		frame, err = captureRegion(*region)
	default:
		frame, err = captureDisplay(opts.Display)
	}
	if err != nil {
		return ModelImage{}, source, err
	}
	scaled, didScale := ScaleForModel(frame)
	cap, err := EncodeForModel(scaled)
	if err != nil {
		return ModelImage{}, source, err
	}
	cap.Scaled = cap.Scaled || didScale
	return cap, source, nil
}

// ListWindows enumerates visible top-level windows on the current platform
// and applies the shared filter and cap. It returns the surviving rows plus
// the unfiltered total so callers can flag truncation.
func ListWindows(opts ListOptions) ([]Window, int, error) {
	windows, err := listWindows()
	if err != nil {
		return nil, 0, err
	}
	kept, total := FilterWindows(windows, opts)
	return kept, total, nil
}

// Target returns the effective target kind and the window-id/title pair that
// produced it. Region beats Display when both are set; an explicit window
// target beats both. Conflicting window sources (id + title) are a usage
// error, as is a Region with impossible dimensions.
func (o CaptureOptions) Target() (TargetKind, int, string, *Rect, error) {
	if o.WindowID != 0 && strings.TrimSpace(o.WindowTitle) != "" {
		return 0, 0, "", nil, errors.New("windowId and windowTitle are mutually exclusive; pass one")
	}
	if o.Region != nil {
		if err := ValidateRegion(*o.Region); err != nil {
			return 0, 0, "", nil, err
		}
	}
	if o.WindowID != 0 || strings.TrimSpace(o.WindowTitle) != "" {
		return TargetWindow, o.WindowID, o.WindowTitle, nil, nil
	}
	if o.Region != nil {
		return TargetRegion, 0, "", o.Region, nil
	}
	return TargetDisplay, 0, "", nil, nil
}

// ValidateRegion rejects impossible rectangles before any syscall runs.
func ValidateRegion(r Rect) error {
	switch {
	case r.Width <= 0 || r.Height <= 0:
		return fmt.Errorf("%w: width and height must be positive (got %dx%d)", ErrBadRegion, r.Width, r.Height)
	case r.Width > MaxRegionWidthPx || r.Height > MaxRegionHeightPx:
		return fmt.Errorf("%w: %dx%d exceeds the %dx%d capture limit", ErrBadRegion, r.Width, r.Height, MaxRegionWidthPx, MaxRegionHeightPx)
	}
	return nil
}

// ClampListLimit folds a requested list limit into the declared bounds.
func ClampListLimit(limit int) int {
	if limit <= 0 || limit > MaxListWindows {
		return MaxListWindows
	}
	return limit
}

// FilterWindows applies the list filter and cap. It returns the surviving
// rows plus the unfiltered total so callers can flag truncation. Rows are
// expected front-to-back from the platform enumeration; the order and the
// Focused flags are preserved as-is.
func FilterWindows(windows []Window, opts ListOptions) (kept []Window, total int) {
	needle := strings.ToLower(strings.TrimSpace(opts.Title))
	limit := ClampListLimit(opts.Limit)
	kept = make([]Window, 0, len(windows))
	for _, w := range windows {
		if needle != "" && !strings.Contains(strings.ToLower(w.Title), needle) &&
			!strings.Contains(strings.ToLower(w.Process), needle) {
			continue
		}
		total++
		if len(kept) < limit {
			kept = append(kept, clipTitle(w))
		}
	}
	return kept, total
}

// clipTitle bounds one window title; titles come from other processes' UI
// and are user data the model must see bounded.
func clipTitle(w Window) Window {
	if runes := []rune(w.Title); len(runes) > MaxTitleChars {
		w.Title = string(runes[:MaxTitleChars]) + "…"
	}
	return w
}

// ResolveWindow picks the capture target out of a platform enumeration.
// An explicit id is matched exactly. A title substring must resolve to
// exactly one row: zero matches fail with ErrWindowNotFound, several with
// ErrAmbiguousWindow carrying the candidate titles so the caller can show
// the list instead of guessing (the surveyed MCP tools silently took the
// first match; that silent pick is the bug this refuses to copy).
func ResolveWindow(windows []Window, id int, title string) (Window, error) {
	if id != 0 {
		for _, w := range windows {
			if w.ID == id {
				return w, nil
			}
		}
		return Window{}, fmt.Errorf("%w: id %d is not in the current window list", ErrWindowNotFound, id)
	}
	needle := strings.ToLower(strings.TrimSpace(title))
	if needle == "" {
		return Window{}, errors.New("window target requires windowId or windowTitle")
	}
	var matches []Window
	for _, w := range windows {
		if strings.Contains(strings.ToLower(w.Title), needle) ||
			strings.Contains(strings.ToLower(w.Process), needle) {
			matches = append(matches, w)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return Window{}, fmt.Errorf("%w: no visible window title or process contains %q", ErrWindowNotFound, title)
	default:
		names := make([]string, 0, len(matches))
		for _, m := range matches {
			names = append(names, fmt.Sprintf("id=%d %q", m.ID, m.Title))
		}
		return Window{}, fmt.Errorf("%w: %d matches — %s", ErrAmbiguousWindow, len(matches), strings.Join(names, "; "))
	}
}

// ScaleForModel downscales frame so its longest edge fits
// MaxModelImageEdgePx, preserving aspect. Small frames are returned as-is;
// the resampler is CatmullRom for legible UI text.
func ScaleForModel(frame image.Image) (image.Image, bool) {
	b := frame.Bounds()
	w, h := b.Dx(), b.Dy()
	longest := max(w, h)
	if longest <= MaxModelImageEdgePx || longest == 0 {
		return frame, false
	}
	scale := float64(MaxModelImageEdgePx) / float64(longest)
	nw := max(1, int(float64(w)*scale))
	nh := max(1, int(float64(h)*scale))
	dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
	draw.CatmullRom.Scale(dst, dst.Bounds(), frame, b, draw.Over, nil)
	return dst, true
}

// EncodeForModel serializes the frame for model input: PNG for crisp UI
// pixels, falling back to JPEG (white-matted, screenshots have no alpha)
// when the PNG blows the byte budget, and only then to progressively
// smaller, lower-quality JPEG attempts. The loop is bounded: at most three
// attempts before the result is returned regardless.
func EncodeForModel(frame image.Image) (ModelImage, error) {
	encoded, err := encodePNG(frame)
	if err != nil {
		return ModelImage{}, err
	}
	if len(encoded) <= MaxModelImageBytes {
		return ModelImage{Image: frame, Mime: "image/png", Data: encoded}, nil
	}
	scaled := frame
	for quality := 88; ; quality -= 20 {
		data, jerr := encodeJPEG(scaled, quality)
		if jerr != nil {
			return ModelImage{}, jerr
		}
		if len(data) <= MaxModelImageBytes || quality <= 48 {
			return ModelImage{Image: scaled, Mime: "image/jpeg", Data: data, Scaled: scaled.Bounds().Dx() != frame.Bounds().Dx()}, nil
		}
		scaled = scaleDownHalf(scaled)
	}
}

func encodePNG(frame image.Image) ([]byte, error) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, frame); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCaptureFailed, err)
	}
	return buf.Bytes(), nil
}

func encodeJPEG(frame image.Image, quality int) ([]byte, error) {
	// JPEG has no alpha: matte onto opaque white so transparent frame corners
	// (rare on screen captures) do not turn black.
	b := frame.Bounds()
	matte := image.NewRGBA(b)
	draw.Draw(matte, b, image.White, image.Point{}, draw.Src)
	draw.Draw(matte, b, frame, b.Min, draw.Over)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, matte, &jpeg.Options{Quality: quality}); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCaptureFailed, err)
	}
	return buf.Bytes(), nil
}

func scaleDownHalf(frame image.Image) image.Image {
	b := frame.Bounds()
	nw := max(1, b.Dx()/2)
	nh := max(1, b.Dy()/2)
	dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
	draw.CatmullRom.Scale(dst, dst.Bounds(), frame, b, draw.Over, nil)
	return dst
}

// SanitizeTitle collapses control characters out of a window title. Titles
// are other processes' strings; control bytes would corrupt line-oriented
// list output on the way to the model.
func SanitizeTitle(title string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, title)
}
