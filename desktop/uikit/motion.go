package uikit

import (
	"github.com/egoist/mygo/ui"
	"time"
)

// Motion durations are shared by every kit component. MyGo handles ReduceMotion.
const (
	HoverDuration      = 140 * time.Millisecond
	OverlayDuration    = 180 * time.Millisecond
	DisclosureDuration = 200 * time.Millisecond
	ThinkingCycle      = 1300 * time.Millisecond
	ThinkingStagger    = 208 * time.Millisecond
)

// Reveal keeps a stable clipping parent during closing, like MyGo's native
// Collapsible panel, without creating another trigger or changing app state.
func Reveal(c *ui.Context, open bool, build func()) ui.Element {
	root := ui.Column(c).FillWidth().MinWidth(0).Shrink(0)
	target := float32(0)
	if open {
		target = 1
	}
	progress := root.Animate("reveal", target, DisclosureDuration)
	if !open && progress == 0 {
		return root.Height(0)
	}
	var inner ui.Element
	root.Children(func() { inner = ui.Column(c).FillWidth().MinWidth(0).Shrink(0); inner.Children(build) })
	if progress < 1 {
		root.ClipY().Height(progress * inner.Bounds().H)
	}
	return root
}
