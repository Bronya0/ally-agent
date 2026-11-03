package uikit

import (
	"github.com/egoist/mygo/ui"
	"time"
)

// Motion durations are shared by every kit component. MyGo settles the values
// of Animate on its own when ReduceMotion or DisableAnimations is on; motion a
// view paints itself has to ask StillMotion.
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

// StillMotion reports that motion painted frame by frame must settle at once:
// the user asked for reduced motion, or this frame runs with animations off.
func StillMotion(c *ui.Context) bool {
	return c.Preferences().ReduceMotion || c.Instant()
}

// HoverFade shows e at opacity as the pointer asks, over HoverDuration. It
// fades through a pose: a pose fades in frames that only paint, where animating
// the opacity here would rebuild the whole view, and the accessibility tree
// every built frame carries, once per frame of the fade. With reduced motion it
// settles at once, as the animated opacity did.
func HoverFade(c *ui.Context, e ui.Element, opacity float32) ui.Element {
	if StillMotion(c) {
		return e.Opacity(opacity)
	}
	return e.AnimatePose(ui.Pose{Fade: 1 - opacity}, ui.Animation{Duration: HoverDuration})
}
