// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	backend "ally-dev/internal/app"
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// These global events are processed before looking for a visible conversation
// tab, just as the task center and system notifications are.
func (a *desktop) updatePetForEvent(event string, p map[string]any) {
	if a.closing {
		return
	}
	key := ""
	if id := str(p, "runId"); id != "" {
		key = "run:" + id
	}
	task, _ := p["task"].(map[string]any)
	if strings.HasPrefix(event, "scheduled:") && str(task, "id") != "" {
		key = "scheduled:" + str(task, "id")
	}
	previousPose := ""
	switch event {
	case "run:start", "scheduled:run_start":
		now := time.Now()
		a.beginAvatarTransition(now)
		if key != "" {
			if a.pet.working == nil {
				a.pet.working = make(map[string]bool)
			}
			a.pet.working[key] = true
		}
		a.pet.lastActivity = now
		a.invalidateAvatar()
		return
	case "run:done", "run:error", "scheduled:run_done", "scheduled:run_error":
		previousPose = a.avatarPose(time.Now(), false)
		delete(a.pet.working, key)
	default:
		return
	}
	completionKey := key
	if strings.HasPrefix(event, "scheduled:") && key != "" {
		completionKey = fmt.Sprintf("%s:%v:%v", key, task["lastRunAt"], task["runCount"])
	}
	if completionKey != "" && !a.petAcceptCompletion(completionKey) {
		return
	}
	now := time.Now()
	a.pet.previousPose, a.pet.transitionAt = previousPose, now
	a.pet.lastActivity = now
	ok := event == "run:done" || event == "scheduled:run_done"
	if ok {
		a.pet.pose, a.pet.poseUntil = "happy", now.Add(4*time.Second)
		if a.prefs.Pet.Enabled && a.pet.win != nil {
			a.rewardPet("task", now)
		}
	} else {
		a.pet.pose, a.pet.poseUntil = "thinking", now.Add(4*time.Second)
	}
	if a.prefs.Pet.Enabled && a.pet.win != nil {
		a.showPetBubble(a.petCompletionText(event, p), true)
	}
	a.invalidateAvatar()
	if a.win != nil {
		a.win.Invalidate()
	}
}

func (a *desktop) petAcceptCompletion(key string) bool {
	if a.pet.seen == nil {
		a.pet.seen = make(map[string]bool)
	}
	if a.pet.seen[key] {
		return false
	}
	if len(a.pet.seenOrder) >= 256 {
		delete(a.pet.seen, a.pet.seenOrder[0])
		a.pet.seenOrder = a.pet.seenOrder[1:]
	}
	a.pet.seen[key] = true
	a.pet.seenOrder = append(a.pet.seenOrder, key)
	return true
}

func (a *desktop) petCompletionText(event string, p map[string]any) string {
	label := completionTitle(event, p)
	var ms int64
	if strings.HasPrefix(event, "scheduled:") {
		task, _ := p["task"].(map[string]any)
		name := str(task, "name")
		if strings.TrimSpace(name) == "" {
			name = str(task, "id")
		}
		if name = petShortTitle(name); name != "" {
			result := "已完成"
			if event == "scheduled:run_error" {
				result = "出错"
				if str(task, "lastStatus") == "cancelled" {
					result = "已取消"
				}
			}
			label = "定时任务「" + name + "」" + result
		}
		start, end := int64(num(task, "lastRunAt")), int64(num(task, "updatedAt"))
		if start > 0 && end >= start {
			ms = end - start
		}
	} else {
		ms = int64(num(p, "durationMs"))
		// 气泡报的是工作区：一次运行可能属于已经关掉的标签页，而工作区的
		// 名字比会话标题更能说明这些活发生在哪个项目里。
		if ws := strings.TrimSpace(str(p, "workspace")); ws != "" {
			if name := petShortTitle(workspaceLabel(ws)); name != "" {
				label = "工作区「" + name + "」" + strings.TrimPrefix(label, "任务")
			}
		}
	}
	if elapsed := backend.FormatDurationMS(ms); elapsed != "" {
		label += "，用时 " + elapsed
	}
	return label + "。"
}

func petShortTitle(s string) string {
	runes := []rune(strings.Join(strings.Fields(s), " "))
	if len(runes) > 20 {
		return string(runes[:20]) + "…"
	}
	return string(runes)
}

func (a *desktop) showPetBubble(message string, important bool) {
	if a.pet.win == nil || a.win == nil || a.closing {
		return
	}
	now := time.Now()
	if a.pet.bubbleImportant && now.Before(a.pet.bubbleUntil) {
		if important {
			if len(a.pet.pendingBubbles) < 8 {
				a.pet.pendingBubbles = append(a.pet.pendingBubbles, message)
			} else {
				a.pet.bubbleOverflow++
			}
		}
		return
	}
	a.pet.bubbleSeq++
	seq, generation := a.pet.bubbleSeq, a.pet.generation
	a.pet.bubbleText, a.pet.bubbleImportant = message, important
	a.pet.bubbleUntil = now.Add(petBubbleDuration)
	width, height := petBubbleSize(message)
	if a.pet.bubbleTimer != nil {
		a.pet.bubbleTimer.Stop()
	}
	if a.pet.bubble == nil {
		w := mygo.NewWindow(mygo.WindowOptions{
			Title: "Ally 宠物消息", Width: width, Height: height,
			Frameless: true, Transparent: true, AlwaysOnTop: true, SkipTaskbar: true,
			DisableResize: true, DisableMaximize: true, DisableMinimize: true, DisableFullScreen: true,
			DisableShadow: true, Hidden: true, Content: ui.View(a.petBubbleView),
		})
		a.pet.bubble = w
		w.SetIgnoreMouseEvents(true)
		a.positionPetBubble()
		w.OnReadyToShow(func() {
			if a.pet.bubble == w && time.Now().Before(a.pet.bubbleUntil) && !a.closing {
				w.ShowInactive()
			}
		})
	} else {
		a.pet.bubble.SetSize(width, height)
		a.positionPetBubble()
		a.pet.bubble.Invalidate()
		a.pet.bubble.ShowInactive()
	}
	a.pet.bubbleTimer = time.AfterFunc(petBubbleDuration, func() {
		a.win.Update(func() {
			if a.pet.generation == generation && a.pet.bubbleSeq == seq && a.pet.bubble != nil {
				a.pet.bubbleImportant = false
				if len(a.pet.pendingBubbles) > 0 {
					next := a.pet.pendingBubbles[0]
					a.pet.pendingBubbles = a.pet.pendingBubbles[1:]
					a.showPetBubble(next, true)
					return
				}
				if a.pet.bubbleOverflow > 0 {
					count := a.pet.bubbleOverflow
					a.pet.bubbleOverflow = 0
					a.showPetBubble(fmt.Sprintf("还有 %d 项任务结束了，请查看对话或任务中心。", count), true)
					return
				}
				a.pet.bubble.Hide()
				a.pet.bubbleText = ""
			}
		})
	})
}

func (a *desktop) positionPetBubble() {
	if a.pet.win == nil || a.pet.bubble == nil {
		return
	}
	width, height := petBubbleSize(a.pet.bubbleText)
	r := a.pet.win.Bounds()
	work := mygo.Screen.DisplayMatching(r).WorkArea
	x := r.X + (r.Width-width)/2
	y := r.Y - height
	if y < work.Y {
		y = r.Y + r.Height
	}
	if work.Width >= width {
		x = max(work.X, min(x, work.X+work.Width-width))
	}
	if work.Height >= height {
		y = max(work.Y, min(y, work.Y+work.Height-height))
	}
	a.pet.bubble.SetPosition(x, y)
}

func petBubbleSize(message string) (width, height int) {
	textWidth := 0
	for _, r := range message {
		if r <= 0x7f {
			textWidth += 7
		} else {
			textWidth += 13
		}
	}
	width = max(petBubbleMinWidth, min(petBubbleMaxWidth, textWidth+petBubblePaddingX*2))
	contentWidth := width - petBubblePaddingX*2
	lines := max(1, (textWidth+contentWidth-1)/contentWidth)
	height = max(petBubbleMinHeight, petBubblePaddingY*2+lines*petBubbleLineHeight)
	return width, height
}

func (a *desktop) petBubbleView(c *ui.Context) {
	c.Root().Background(ui.Transparent)
	ui.Column(c).Fill().Padding(petBubblePaddingY, petBubblePaddingX).Justify(ui.Center).Radius(12).
		Background(ui.RGB(248, 246, 244)).Children(func() {
		ui.Text(c, a.pet.bubbleText).FontSize(fsSmall).TextColor(ui.RGB(53, 55, 64)).FillWidth().MinWidth(0)
	})
}

const (
	petBubbleMinWidth    = 150
	petBubbleMaxWidth    = 220
	petBubbleMinHeight   = 48
	petBubblePaddingX    = 12
	petBubblePaddingY    = 8
	petBubbleLineHeight  = 17
	petFirstChatterDelay = time.Minute
	petChatterRetryDelay = 45 * time.Second
	petMutedChatterDelay = 10 * time.Minute
	petChatterQuietTime  = 30 * time.Second
	petBubbleDuration    = 8 * time.Second
)

var petChatterLines = []string{
	"我在这里陪着你，随时可以叫我。",
	"今天也辛苦啦，记得喝口水。",
	"忙完一件事，也值得给自己一点奖励。",
	"要不要休息一下，伸个懒腰？",
	"我刚才悄悄看了看你，还好一切顺利。",
	"慢慢来，我们一起把事情做好。",
	"有时候停下来想一想，灵感就来了。",
	"谢谢你今天也陪着我。",
}

func (a *desktop) petCanChatter(now time.Time) bool {
	if a.prefs.Pet.MuteChatter || len(a.pet.working) > 0 || now.Before(a.pet.poseUntil) || now.Before(a.pet.bubbleUntil) {
		return false
	}
	return a.pet.lastActivity.IsZero() || now.Sub(a.pet.lastActivity) >= petChatterQuietTime
}

func petNextChatterDelay(now time.Time) time.Duration {
	// 4–7 minutes, stable for this scheduling decision and without a busy loop.
	return 4*time.Minute + time.Duration(now.UnixNano()%int64(3*time.Minute))
}

func (a *desktop) sayPetChatter(now time.Time, manual bool) {
	if len(a.pet.working) == 0 && !now.Before(a.pet.poseUntil) && (manual || a.avatarPose(now, false) == "sleeping") {
		// A sleeping companion wakes briefly to speak, then resumes its rest.
		a.beginAvatarTransition(now)
		a.pet.pose, a.pet.poseUntil = "idle", now.Add(petBubbleDuration)
		a.invalidateAvatar()
	}
	if manual {
		a.pet.lastActivity = now
	}
	line := petChatterLines[a.pet.chatterIndex%len(petChatterLines)]
	a.pet.chatterIndex++
	a.showPetBubble(line, manual)
}

func (a *desktop) schedulePetIdle(delay time.Duration) {
	if a.pet.win == nil || a.win == nil {
		return
	}
	if a.pet.idleTimer != nil {
		a.pet.idleTimer.Stop()
	}
	a.pet.idleSeq++
	seq := a.pet.idleSeq
	generation := a.pet.generation
	a.pet.idleTimer = time.AfterFunc(delay, func() {
		a.win.Update(func() {
			if a.pet.generation != generation || a.pet.idleSeq != seq || a.pet.win == nil {
				return
			}
			if a.closing {
				// Closing can be cancelled by the unsaved-file dialog. Keep the
				// timer armed until closePet actually disposes the window.
				a.schedulePetIdle(petChatterRetryDelay)
				return
			}
			now := time.Now()
			a.rewardPet("", now)
			if a.prefs.Pet.MuteChatter {
				a.schedulePetIdle(petMutedChatterDelay)
				return
			}
			if !a.petCanChatter(now) {
				a.schedulePetIdle(petChatterRetryDelay)
				return
			}
			a.sayPetChatter(now, false)
			a.schedulePetIdle(petNextChatterDelay(now))
		})
	})
}
