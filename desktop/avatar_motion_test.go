// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"bytes"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestAvatarAnimationHasOneOwner(t *testing.T) {
	a := &desktop{prefs: defaultPrefs()}
	for _, enabled := range []bool{false, true, false} {
		a.prefs.Pet.Enabled = enabled
		welcome := a.avatarAnimationEnabled(avatarOnWelcome)
		pet := a.avatarAnimationEnabled(avatarOnPet)
		if welcome == pet || welcome == enabled {
			t.Fatalf("pet enabled %v: welcome=%v pet=%v", enabled, welcome, pet)
		}
	}
}

func TestAvatarPosesSharedAcrossPetWindowLifecycle(t *testing.T) {
	for _, style := range []string{"whale", "deepseek", "glm"} {
		a := &desktop{prefs: defaultPrefs(), avatarStyle: style}
		a.prefs.Avatar = style
		a.avatarPoses = decodeAvatarStyle(style)
		if a.avatarPoses["idle"] == nil || a.avatarPoses["sleeping"] == nil {
			t.Fatalf("%s is missing a shared idle or sleep pose", style)
		}
		if a.avatarPoses["pat"] == nil || a.avatarPoses["thinking"] == nil || a.avatarPoses["idle-blink"] == nil {
			t.Fatalf("%s is missing shared interaction poses", style)
		}
		if style == "whale" {
			if w, h := a.avatarPoses["sleeping"].Size(); w != 320 || h != 320 {
				t.Fatalf("whale sleeping pose does not match bust canvas: %dx%d", w, h)
			}
		}
		a.pet.working = map[string]bool{"run:background": true}
		idle := a.avatarPoses["idle"]
		a.closePet()
		if a.avatarPoses["idle"] != idle || a.avatarPose(time.Now(), false) != "thinking" {
			t.Fatalf("%s lost shared artwork or work state when the pet closed", style)
		}
		releaseAvatarPoses(a.avatarPoses)
	}
}

func TestWhaleBlinkBlendsAndRespectsReducedMotion(t *testing.T) {
	a := &desktop{prefs: defaultPrefs(), avatarStyle: "whale", avatarPoses: decodeAvatarStyle("whale")}
	defer releaseAvatarPoses(a.avatarPoses)
	now := time.UnixMilli(6500)
	for _, sample := range []struct {
		at       time.Duration
		min, max float32
	}{{0, 0, .01}, {160 * time.Millisecond, .99, 1}, {319 * time.Millisecond, 0, .02}} {
		_, opacity, blending := a.whalePoseBlend(now.Add(sample.at), "idle-blink", false)
		if !blending || opacity < sample.min || opacity > sample.max {
			t.Fatalf("blink at %s: blending=%v opacity=%f", sample.at, blending, opacity)
		}
	}
	if _, _, blending := a.whalePoseBlend(now.Add(160*time.Millisecond), "idle-blink", true); blending {
		t.Fatal("blink animation ignored reduced motion")
	}
	a.pet.pose, a.pet.poseUntil = "idle", now
	a.pet.lastActivity = now.Add(-10 * time.Minute)
	base, opacity, blending := a.whalePoseBlend(now.Add(110*time.Millisecond), "sleeping", false)
	if !blending || base != a.avatarPoses["idle"] || opacity < .49 || opacity > .51 {
		t.Fatalf("return to sleep was not blended: base=%v opacity=%f", base, opacity)
	}
}

func TestWhaleExpressionKeepsOriginalSilhouette(t *testing.T) {
	a := &desktop{prefs: defaultPrefs(), avatarStyle: "whale", avatarPoses: decodeAvatarStyle("whale")}
	defer releaseAvatarPoses(a.avatarPoses)
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Box(c).Size(230, 230).Draw(func(p *ui.Painter, r ui.Rect) { a.paintAvatar(p, r, true, true) })
	}, 230, 230)
	a.pet.lastActivity = tt.Now()
	tt.Frame()
	before := append([]byte(nil), tt.Image().Pix...)
	a.pet.pose, a.pet.poseUntil = "pat", tt.Now().Add(time.Second)
	tt.Frame()
	after := tt.Image().Pix
	changed := 0
	for i := 0; i < len(before); i += 4 {
		if !bytes.Equal(before[i:i+4], after[i:i+4]) {
			changed++
		}
	}
	if changed < 100 || changed > 230*230/7 {
		t.Fatalf("pat expression changed %d pixels; expected a localized face change", changed)
	}
}

func TestWelcomeAnimationStopsWhenPetEnabledAndResumes(t *testing.T) {
	a := &desktop{prefs: defaultPrefs(), avatarStyle: "deepseek"}
	a.prefs.Avatar = "deepseek"
	a.avatarPoses = decodeAvatarStyle("deepseek")
	defer releaseAvatarPoses(a.avatarPoses)
	if a.avatarPoses["pat"] == nil || a.avatarPoses["idle-blink"] == nil || a.avatarPoses["sleeping"] == nil || a.avatarPoses["thinking"] == nil {
		t.Fatal("shared avatar poses were not loaded")
	}

	paints := 0
	a.prefs.Pet.Enabled = true
	tt := ui.NewTester(func(c *ui.Context) {
		reduced := c.Preferences().ReduceMotion
		ui.Box(c).Size(140, 140).Draw(func(p *ui.Painter, r ui.Rect) {
			paints++
			a.paintAvatar(p, r, reduced, a.avatarAnimationEnabled(avatarOnWelcome))
		})
	}, 140, 140)
	a.pet.pose, a.pet.poseUntil = "pat", tt.Now().Add(2500*time.Millisecond)
	tt.Frame()
	before := append([]byte(nil), tt.Image().Pix...)
	paintCount := paints
	tt.Advance(3 * time.Second)
	if paints != paintCount {
		t.Fatal("inactive welcome avatar requested repaint frames")
	}
	tt.Frame()
	if !bytes.Equal(before, tt.Image().Pix) {
		t.Fatal("inactive welcome avatar changed on an unrelated redraw")
	}

	a.prefs.Pet.Enabled = false
	a.patAvatar(tt.Now())
	tt.Frame()
	before = append([]byte(nil), tt.Image().Pix...)
	paintCount = paints
	tt.Advance(300 * time.Millisecond)
	if paints <= paintCount || bytes.Equal(before, tt.Image().Pix) {
		t.Fatal("welcome avatar did not resume the shared pat animation")
	}

	a.prefs.Pet.Enabled = true
	tt.Frame()
	before = append([]byte(nil), tt.Image().Pix...)
	// A previously requested frame may already be in flight; it must not
	// produce continued animation or alter the fixed welcome image.
	tt.Advance(time.Second)
	paintCount = paints
	tt.Advance(3 * time.Second)
	if paints != paintCount || !bytes.Equal(before, tt.Image().Pix) {
		t.Fatal("welcome animation continued after the pet was enabled")
	}
}

func TestWelcomeAvatarRespondsWithoutChangingPetAffection(t *testing.T) {
	a := &desktop{prefs: defaultPrefs(), avatarStyle: "glm"}
	a.prefs.Pet.Enabled = false
	a.prefs.Avatar = "glm"
	a.avatarPoses = decodeAvatarStyle("glm")
	defer releaseAvatarPoses(a.avatarPoses)
	a.pet.lastActivity = time.Now()
	tt := ui.NewTester(func(c *ui.Context) {
		a.welcomeView(c, &conversation{})
	}, 900, 650)
	if err := tt.Click("欢迎页头像"); err != nil {
		t.Fatal(err)
	}
	if !a.petals || a.pet.pose != "pat" || len(a.prefs.Pet.Affection) != 0 {
		t.Fatal("welcome click must keep petals and show a pat without pet rewards")
	}
	a.prefs.Pet.Enabled = true
	a.pet.pose = ""
	tt.Frame()
	if err := tt.Click("欢迎页头像"); err != nil {
		t.Fatal(err)
	}
	if a.petals || a.pet.pose != "" {
		t.Fatal("a static welcome avatar should only toggle petals")
	}
}

func TestWelcomeSharesTaskAndRestPoses(t *testing.T) {
	a := &desktop{prefs: defaultPrefs()}
	now := time.Now()
	a.pet.lastActivity = now
	payload := map[string]any{"runId": "background", "sessionId": "no-open-tab"}
	a.applyEvent("run:start", payload)
	if got := a.avatarPose(now, false); got != "thinking" {
		t.Fatalf("welcome did not show work pose: %q", got)
	}
	a.applyEvent("run:done", payload)
	if got := a.avatarPose(time.Now(), false); got != "happy" {
		t.Fatalf("welcome did not show completion pose: %q", got)
	}
	if len(a.prefs.Pet.Affection) != 0 {
		t.Fatal("a disabled pet received a task reward")
	}
	a.pet.poseUntil = time.Time{}
	a.pet.lastActivity = now.Add(-6 * time.Minute)
	if got := a.avatarPose(now, false); got != "sleeping" {
		t.Fatalf("welcome did not show the rest pose: %q", got)
	}
}
