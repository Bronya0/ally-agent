// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"ally-dev/desktop/uikit"
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// Motion fields are shared by the welcome page and pet and survive closing
// the pet window. All fields belong to the UI thread; timers use Window.Update.
type petRuntime struct {
	win, bubble                       *mygo.Window
	generation                        uint64
	moveTimer, idleTimer, bubbleTimer *time.Timer
	bubbleSeq                         uint64
	idleSeq                           uint64
	chatterIndex                      int
	bubbleText                        string
	bubbleUntil                       time.Time
	bubbleImportant                   bool
	pendingBubbles                    []string
	bubbleOverflow                    int
	lastActivity                      time.Time
	pose                              string
	poseUntil                         time.Time
	previousPose                      string
	transitionAt                      time.Time
	working                           map[string]bool
	seen                              map[string]bool
	seenOrder                         []string
}

func (a *desktop) petSettings(c *ui.Context) {
	sectionTitle(c, "桌面宠物")
	uikit.SwitchNative(c.Key("pet-enabled"), &a.prefs.Pet.Enabled).Label("显示桌面宠物").OnChange(func() {
		a.setPetEnabled(a.prefs.Pet.Enabled)
	})
	fieldNote(c, "跟随欢迎页形象；拖动身体调整位置，单击头部摸摸头，右键打开菜单。", a.secondaryInk())
	if !a.prefs.Pet.Enabled {
		return
	}
	chatterEnabled := !a.prefs.Pet.MuteChatter
	uikit.SwitchNative(c.Key("pet-chatter"), &chatterEnabled).Label("闲时说话").OnChange(func() {
		a.setPetChatter(chatterEnabled)
	})
	fieldNote(c, "空闲时偶尔显示本地短句；右键可马上和宠物打招呼。", a.secondaryInk())
	fieldLabel(c, "宠物大小")
	uikit.OptionGroup(c.Key("pet-size")).Children(func() {
		for _, option := range []struct {
			size  int
			label string
		}{{140, "小"}, {180, "中"}, {230, "大"}} {
			option := option
			choiceButton(c, fmt.Sprintf("pet-size-%d", option.size), option.label, a.prefs.Pet.Size == option.size, c.Theme().Accent).OnClick(func() {
				a.setPetSize(option.size)
			})
		}
	})
	score := a.prefs.Pet.Affection[normalizedAvatarStyle(a.prefs.Avatar)].Points
	stars := max(0, min(10, score/1000))
	ui.Text(c, strings.Repeat("★", stars)+strings.Repeat("☆", 10-stars)).FontSize(fsGlyph).TextColor(c.Theme().Accent)
	fieldNote(c, fmt.Sprintf("好感度 %.3f / 10 星 · 每天陪伴 +0.020，前三次摸头各 +0.005，前三个完成任务各 +0.010。", float64(score)/1000), a.secondaryInk())
}

func (a *desktop) openPet() {
	if a.win == nil || a.closing || a.pet.win != nil {
		return
	}
	a.prefs.Pet.normalize()
	a.pet.generation++
	if a.pet.lastActivity.IsZero() {
		a.pet.lastActivity = time.Now()
	}
	if a.pet.working == nil {
		a.pet.working = make(map[string]bool)
	}
	for _, t := range a.tabs {
		if t.Busy && t.RunID != "" {
			a.pet.working["run:"+t.RunID] = true
		}
	}
	for _, t := range a.tasks {
		if t.Running {
			a.pet.working["scheduled:"+t.ID] = true
		}
	}
	size := a.prefs.Pet.Size
	x, y := petRestoredPosition(a.prefs.Pet, mygo.Screen.Displays(), mygo.Screen.PrimaryDisplay().WorkArea)
	w := mygo.NewWindow(mygo.WindowOptions{
		Title: "Ally 桌面宠物", Width: size, Height: size, X: x, Y: y,
		Frameless: true, Transparent: true, AlwaysOnTop: true, SkipTaskbar: true,
		DisableResize: true, DisableMaximize: true, DisableMinimize: true, DisableFullScreen: true,
		DisableShadow: true, Hidden: true, Content: ui.View(a.petView),
	})
	a.pet.win = w
	// NewWindow centers when both coordinates are zero, but (0, 0) is also a
	// valid saved position at the top left of a display.
	w.SetPosition(x, y)
	w.OnReadyToShow(func() {
		if a.pet.win == w && !a.closing {
			w.ShowInactive()
		}
	})
	w.OnClosed(func() {
		if a.pet.win != w {
			return
		}
		// Closing the pet itself must not leave the setting enabled with a
		// dead window handle. Normal app shutdown uses closePet/Destroy and
		// preserves the user's enabled preference.
		a.pet.win = nil
		a.closePet()
		if a.closing {
			return
		}
		a.prefs.Pet.Enabled = false
		a.savePrefs()
		if a.win != nil {
			a.win.Invalidate()
		}
	})
	w.OnMove(func() { a.petMoved(w) })
	a.rewardPet("", time.Now())
	a.schedulePetIdle(petFirstChatterDelay)
}

func (a *desktop) setPetEnabled(enabled bool) {
	a.prefs.Pet.Enabled = enabled
	if enabled {
		a.openPet()
	} else {
		a.closePet()
	}
	a.savePrefs()
}

func (a *desktop) setPetSize(size int) {
	a.prefs.Pet.Size = max(120, min(280, size))
	a.resizePet()
	a.savePrefs()
}

func (a *desktop) setPetChatter(enabled bool) {
	a.prefs.Pet.MuteChatter = !enabled
	a.savePrefs()
	if a.pet.win != nil {
		delay := petMutedChatterDelay
		if enabled {
			delay = petFirstChatterDelay
		}
		a.schedulePetIdle(delay)
	}
}

func petRestoredPosition(p petPreferences, displays []mygo.Display, primary mygo.Rectangle) (int, int) {
	size := p.Size
	if !p.HasPosition {
		return primary.X + max(0, primary.Width-size-24), primary.Y + max(0, primary.Height-size-24)
	}
	// Restore only where at least a useful part remains on a connected display.
	for _, d := range displays {
		r := d.WorkArea
		if p.X+size > r.X+32 && p.X < r.X+r.Width-32 && p.Y+size > r.Y+32 && p.Y < r.Y+r.Height-32 {
			return p.X, p.Y
		}
	}
	return primary.X + max(0, primary.Width-size-24), primary.Y + max(0, primary.Height-size-24)
}

func (a *desktop) closePet() {
	a.pet.generation++
	for _, timer := range []*time.Timer{a.pet.moveTimer, a.pet.idleTimer, a.pet.bubbleTimer} {
		if timer != nil {
			timer.Stop()
		}
	}
	a.pet.moveTimer, a.pet.idleTimer, a.pet.bubbleTimer = nil, nil, nil
	if a.pet.bubble != nil {
		a.pet.bubble.Destroy()
		a.pet.bubble = nil
	}
	if a.pet.win != nil {
		w := a.pet.win
		a.pet.win = nil
		w.Destroy()
	}
	a.pet.bubbleText = ""
	a.pet.bubbleUntil = time.Time{}
	a.pet.bubbleImportant = false
	a.pet.pendingBubbles = nil
	a.pet.bubbleOverflow = 0
}

func (a *desktop) resizePet() {
	if a.pet.win == nil {
		return
	}
	w := a.pet.win
	w.SetSize(a.prefs.Pet.Size, a.prefs.Pet.Size)
	a.positionPetBubble()
}

func (a *desktop) petMoved(w *mygo.Window) {
	if a.pet.win != w || a.closing {
		return
	}
	r := w.Bounds()
	now := time.Now()
	a.beginAvatarTransition(now)
	a.pet.lastActivity = now
	a.invalidatePet()
	a.prefs.Pet.X, a.prefs.Pet.Y, a.prefs.Pet.HasPosition = r.X, r.Y, true
	a.positionPetBubble()
	if a.pet.moveTimer != nil {
		a.pet.moveTimer.Stop()
	}
	generation := a.pet.generation
	a.pet.moveTimer = time.AfterFunc(500*time.Millisecond, func() {
		a.win.Update(func() {
			if generation == a.pet.generation && !a.closing {
				a.savePrefs()
			}
		})
	})
}

func (a *desktop) resetAvatarMotion() {
	a.pet.pose, a.pet.poseUntil = "", time.Time{}
	a.pet.previousPose, a.pet.transitionAt = "", time.Time{}
	a.pet.lastActivity = time.Now()
	if a.pet.win != nil && a.prefs.Pet.Enabled {
		a.rewardPet("", a.pet.lastActivity)
	}
	a.invalidateAvatar()
}

func (a *desktop) invalidatePet() {
	if a.pet.win != nil {
		a.pet.win.Invalidate()
	}
}

func (a *desktop) invalidateAvatar() {
	if a.prefs.Pet.Enabled {
		a.invalidatePet()
	} else if a.win != nil {
		a.win.Invalidate()
	}
}

func (a *desktop) rewardPet(activity string, now time.Time) {
	if a.prefs.Pet.reward(normalizedAvatarStyle(a.prefs.Avatar), activity, now) {
		a.savePrefs()
	}
}

func (a *desktop) petView(c *ui.Context) {
	c.Root().Background(ui.Transparent)
	reduceMotion := c.Preferences().ReduceMotion
	pet := ui.Column(c.Key("pet-character")).Fill().Label("桌面宠物").Draw(func(p *ui.Painter, r ui.Rect) {
		a.paintAvatar(p, r, reduceMotion, a.avatarAnimationEnabled(avatarOnPet))
	})
	pet.ContextMenu(a.petContextMenu)
	pet.Children(func() {
		ui.ButtonBase(c.Key("pet-head")).FillWidth().Grow(2).Basis(0).
			Cursor(ui.CursorPointer).Label("摸摸宠物的头").OnClick(a.patPet)
		ui.Box(c.Key("pet-body")).FillWidth().Grow(3).Basis(0).
			Cursor(ui.CursorGrab).Label("拖动宠物位置").DragWindow()
	})
}

func (a *desktop) patPet() {
	now := time.Now()
	a.patAvatar(now)
	a.rewardPet("pat", now)
	lines := []string{"摸摸头收到啦，谢谢主人！", "嘿嘿，再陪我一会儿吧。", "被摸摸的感觉真好～", "有主人在，今天也很开心。"}
	a.showPetBubble(lines[now.UnixNano()%int64(len(lines))], false)
}

func (a *desktop) patAvatar(now time.Time) {
	a.beginAvatarTransition(now)
	a.pet.lastActivity, a.pet.pose, a.pet.poseUntil = now, "pat", now.Add(2500*time.Millisecond)
	a.invalidateAvatar()
}
