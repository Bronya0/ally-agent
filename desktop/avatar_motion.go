// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"fmt"
	"math"
	"time"

	"github.com/egoist/mygo/ui"
)

type avatarSurface uint8

const (
	avatarOnWelcome avatarSurface = iota
	avatarOnPet
)

func (a *desktop) avatarAnimationEnabled(surface avatarSurface) bool {
	if surface == avatarOnPet {
		return a.prefs.Pet.Enabled
	}
	return !a.prefs.Pet.Enabled
}

func (a *desktop) beginAvatarTransition(now time.Time) {
	a.pet.previousPose = a.avatarPose(now, false)
	a.pet.transitionAt = now
}

func (a *desktop) avatarBlinkEnd() int64 {
	if normalizedAvatarStyle(a.prefs.Avatar) == "whale" {
		return 6820
	}
	return 6700
}

func (a *desktop) avatarPose(now time.Time, reduced bool) string {
	pose := "idle"
	if now.Before(a.pet.poseUntil) {
		pose = a.pet.pose
	} else if len(a.pet.working) > 0 {
		pose = "thinking"
	} else if !a.pet.lastActivity.IsZero() && now.Sub(a.pet.lastActivity) >= 5*time.Minute {
		pose = "sleeping"
	} else if !reduced {
		phase := now.UnixMilli() % 18000
		switch {
		case phase >= 6500 && phase < a.avatarBlinkEnd():
			pose = "idle-blink"
		case phase >= 12500 && phase < 14500:
			pose = "stretch"
		}
	}
	return pose
}

// Both surfaces paint the same poses. When the other surface owns animation,
// paint a fixed idle frame and do not request a timer or animation frame.
func (a *desktop) paintAvatar(p *ui.Painter, r ui.Rect, reduced, animated bool) {
	now := p.Now()
	pose := "idle"
	if animated {
		pose = a.avatarPose(now, reduced)
	}
	src := a.avatarImage(pose, now, reduced || !animated)
	if src == nil {
		src = eyeArtwork
	}
	pad := r.W * 0.025
	box := ui.Rect{X: r.X + pad, Y: r.Y + pad, W: r.W - 2*pad, H: r.H - 2*pad}
	if !animated {
		p.Image(src, box, ui.Contain)
		return
	}
	angle, dy := float32(0), float32(0)
	scaleX, scaleY := float32(1), float32(1)
	base, opacity, blending := a.whalePoseBlend(now, pose, reduced)
	if !reduced {
		phaseMS := now.UnixMilli() % 18000
		phase := float64(phaseMS) / 1000
		reaction := pose == "pat" || pose == "happy"
		active := reaction || (phaseMS < 2000 && pose == "idle")
		if active {
			if reaction {
				duration := 2500 * time.Millisecond
				if pose == "happy" {
					duration = 4 * time.Second
				}
				elapsed := duration.Seconds() - a.pet.poseUntil.Sub(now).Seconds()
				envelope := math.Exp(-1.7 * max(0, elapsed))
				wave := math.Sin(elapsed * math.Pi * 3)
				angle, dy = float32(wave*3*envelope), float32(-math.Abs(wave)*5*envelope)
			} else {
				wave := math.Sin(phase * math.Pi)
				angle, dy = float32(wave*1.5), float32(-math.Abs(wave)*2)
			}
			p.AnimationFrameRate(15)
		} else if normalizedAvatarStyle(a.prefs.Avatar) == "whale" && pose == "stretch" {
			// A subtle anchored stretch keeps the flat artwork lively without
			// moving the layout or distorting the face abruptly.
			progress := float64(phaseMS-12500) / 2000
			wave := math.Sin(math.Pi * progress)
			scaleX, scaleY = float32(1-0.012*wave), float32(1+0.025*wave)
			angle, dy = float32(math.Sin(2*math.Pi*progress)*0.8*wave), float32(-1.2*wave)
			p.AnimationFrameRate(30)
		} else if a.avatarStyle == "bot" {
			p.After(80 * time.Millisecond)
		} else if pose == "thinking" {
			p.After(600 * time.Millisecond)
		} else if pose == "sleeping" {
			if now.Before(a.pet.poseUntil) {
				p.After(a.pet.poseUntil.Sub(now))
			}
		} else {
			// Wake only at the next pose or idle-motion boundary.
			next := int64(18000)
			for _, boundary := range []int64{2000, 6500, a.avatarBlinkEnd(), 12500, 14500, 18000} {
				if boundary > phaseMS {
					next = boundary
					break
				}
			}
			p.After(time.Duration(next-phaseMS) * time.Millisecond)
		}
		if blending {
			p.AnimationFrameRate(30)
		}
		if len(a.pet.working) == 0 && !a.pet.lastActivity.IsZero() && !now.Before(a.pet.poseUntil) {
			if untilSleep := a.pet.lastActivity.Add(5 * time.Minute).Sub(now); untilSleep > 0 {
				p.After(untilSleep)
			}
		}
	} else {
		if now.Before(a.pet.poseUntil) {
			p.After(a.pet.poseUntil.Sub(now))
		} else if len(a.pet.working) == 0 && !a.pet.lastActivity.IsZero() && now.Sub(a.pet.lastActivity) < 5*time.Minute {
			p.After(a.pet.lastActivity.Add(5 * time.Minute).Sub(now))
		}
	}
	if !reduced && now.Before(a.pet.poseUntil) {
		// Repaint at the end of a temporary pose, including a brief wake-up
		// for chatter, even if its usual idle timer would wake later.
		p.After(a.pet.poseUntil.Sub(now))
	}
	p.Transform(ui.Scaling(scaleX, scaleY).Then(ui.Rotation(angle)).Then(ui.Translation(0, dy)), r.X+r.W/2, r.Y+r.H*0.9, func() {
		if target, ok := src.(*ui.Bitmap); normalizedAvatarStyle(a.prefs.Avatar) == "whale" && ok && target != nil && a.avatarPoses["idle"] != nil {
			// The generated expressions have small differences in their hair and
			// outline. Keep the original artwork fixed and blend just the face,
			// so an expression never makes the whole silhouette jump.
			idle := a.avatarPoses["idle"]
			p.Image(idle, box, ui.Contain)
			if blending {
				if previous, ok := base.(*ui.Bitmap); ok && previous != idle {
					paintWhaleFace(p, previous, box, 1)
				}
				paintWhaleFace(p, target, box, opacity)
			} else if target != idle {
				paintWhaleFace(p, target, box, 1)
			}
		} else {
			p.Image(src, box, ui.Contain)
		}
	})
	if pose == "thinking" {
		dot := max(float32(3), r.W*0.018)
		for i := 0; i < 3; i++ {
			opacity := float32(0.55)
			if !reduced && int(now.UnixMilli()/600)%3 == i {
				opacity = 1
			}
			p.Fill(ui.Rect{X: r.X + r.W*0.78 + float32(i)*dot*1.7, Y: r.Y + r.H*0.17, W: dot, H: dot}, ui.RGB(97, 155, 219).Alpha(opacity), dot/2)
		}
	} else if pose == "sleeping" {
		p.Text(r.X+r.W*0.82, r.Y+r.H*0.08, "z", max(float32(12), r.W*0.075), ui.RGB(114, 148, 190))
	}
	if !reduced && pose == "pat" && now.Before(a.pet.poseUntil) {
		progress := 1 - float32(a.pet.poseUntil.Sub(now).Seconds()/2.5)
		if progress >= 0 && progress < 0.8 {
			paintAvatarHeart(p, r.X+r.W*0.78, r.Y+r.H*(0.22-progress*0.13), r.W*0.035, 1-progress/0.8)
		}
	}
}

func paintWhaleFace(p *ui.Painter, target *ui.Bitmap, box ui.Rect, opacity float32) {
	if target == nil || opacity <= 0 {
		return
	}
	// Coordinates are in the shared 320×320 artwork canvas. Rounded clips
	// stay on the face while leaving the hair, hands and decorations intact.
	for _, part := range []ui.Rect{{X: 96, Y: 157, W: 53, H: 52}, {X: 171, Y: 151, W: 58, H: 57}, {X: 144, Y: 198, W: 42, H: 38}} {
		clip := ui.Rect{X: box.X + box.W*part.X/320, Y: box.Y + box.H*part.Y/320, W: box.W * part.W / 320, H: box.H * part.H / 320}
		p.Clip(clip, box.W*8/320, func() {
			p.ImageRegion(target, ui.Rect{}, box, ui.ImageOptions{Tint: ui.RGB(255, 255, 255).Alpha(opacity)})
		})
	}
}

// New whale expressions share a square canvas with the idle art. Fade only
// their changes, so the short blink and other pose changes do not pop.
func (a *desktop) whalePoseBlend(now time.Time, pose string, reduced bool) (ui.ImageSource, float32, bool) {
	if normalizedAvatarStyle(a.prefs.Avatar) != "whale" || reduced {
		return nil, 0, false
	}
	if pose == "idle-blink" {
		progress := float64(now.UnixMilli()%18000-6500) / float64(a.avatarBlinkEnd()-6500)
		if idle := a.avatarPoses["idle"]; idle != nil {
			return idle, float32(math.Sin(math.Pi * max(0, min(1, progress)))), true
		}
	}
	if pose == "pat" && now.Before(a.pet.poseUntil) {
		remaining := a.pet.poseUntil.Sub(now)
		if remaining < 180*time.Millisecond {
			next := "idle"
			if len(a.pet.working) > 0 {
				next = "thinking"
			}
			if base := a.avatarImage(next, now, true); base != nil {
				return base, float32(remaining.Seconds() / 0.18), true
			}
		}
	}
	if (a.pet.pose == "idle" && pose == "sleeping") || (a.pet.pose == "thinking" && pose != "thinking") {
		if elapsed := now.Sub(a.pet.poseUntil); !a.pet.poseUntil.IsZero() && elapsed >= 0 && elapsed < 220*time.Millisecond {
			if base := a.avatarImage(a.pet.pose, now, true); base != nil {
				return base, float32(elapsed.Seconds() / 0.22), true
			}
		}
	}
	if pose == "sleeping" && !a.pet.lastActivity.IsZero() {
		elapsed := now.Sub(a.pet.lastActivity.Add(5 * time.Minute))
		if elapsed >= 0 && elapsed < 250*time.Millisecond {
			if base := a.avatarPoses["idle"]; base != nil {
				return base, float32(elapsed.Seconds() / 0.25), true
			}
		}
	}
	if elapsed := now.Sub(a.pet.transitionAt); !a.pet.transitionAt.IsZero() && elapsed >= 0 && elapsed < 220*time.Millisecond && a.pet.previousPose != pose {
		if base := a.avatarImage(a.pet.previousPose, now, true); base != nil {
			return base, float32(elapsed.Seconds() / 0.22), true
		}
	}
	return nil, 0, false
}

func paintAvatarHeart(p *ui.Painter, x, y, size, opacity float32) {
	var heart ui.Path
	heart.MoveTo(x, y+size*0.35).
		CubeTo(x-size, y-size*0.4, x-size*0.9, y+size*0.55, x, y+size*1.2).
		CubeTo(x+size*0.9, y+size*0.55, x+size, y-size*0.4, x, y+size*0.35).Close()
	p.FillPath(&heart, ui.RGB(239, 121, 145).Alpha(opacity))
}

func (a *desktop) avatarImage(pose string, now time.Time, reduced bool) ui.ImageSource {
	if bm := a.avatarPoses[pose]; bm != nil {
		return bm
	}
	if pose == "pat" {
		pose = "happy"
	}
	if pose == "thinking" {
		pose = "proud"
	}
	if pose == "sleeping" || pose == "idle-blink" {
		pose = "idle"
	}
	if a.prefs.Avatar == "custom" {
		if a.customAvatar != nil {
			return a.customAvatar
		}
		return nil
	}
	if a.prefs.Avatar == "eye" {
		return eyeArtwork
	}
	if a.avatarStyle == "bot" {
		frame := int64(0)
		if !reduced {
			frame = (now.UnixMilli() / 80) % 100
		}
		if bm := a.avatarPoses[fmt.Sprintf("frame%02d", frame)]; bm != nil {
			return bm
		}
		return nil
	}
	if image := a.avatarPoses[pose]; image != nil {
		return image
	}
	if bm := a.avatarPoses["idle"]; bm != nil {
		return bm
	}
	return nil
}
