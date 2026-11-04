// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

func TestPetAffectionDailyLimitsAndPersistence(t *testing.T) {
	p := petPreferences{Size: 180}
	day := time.Date(2026, 1, 1, 12, 0, 0, 0, time.Local)
	p.reward("deepseek", "", day)
	for range 6 {
		p.reward("deepseek", "pat", day)
		p.reward("deepseek", "task", day)
	}
	got := p.Affection["deepseek"]
	if got.Points != 65 || got.Pats != 3 || got.Tasks != 3 {
		t.Fatalf("daily rewards were not bounded: %+v", got)
	}
	p.reward("glm", "pat", day)
	if p.Affection["glm"].Points != 25 || p.Affection["deepseek"].Points != 65 {
		t.Fatal("character progress was shared")
	}
	p.reward("deepseek", "pat", day.AddDate(0, 0, 1))
	if got = p.Affection["deepseek"]; got.Points != 90 || got.Pats != 1 || got.Tasks != 0 {
		t.Fatalf("daily counters did not reset: %+v", got)
	}
	p.MuteChatter = true
	data, err := json.Marshal(uiPrefs{Pet: p})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "prefs.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if reopened := readPrefs(path).Pet; reopened.Affection["deepseek"] != got || !reopened.MuteChatter {
		t.Fatalf("progress did not round-trip: %+v", reopened)
	}
	copy := p.clone()
	copy.reward("deepseek", "task", day.AddDate(0, 0, 1))
	if p.Affection["deepseek"] != got {
		t.Fatal("save snapshot shared its affection map")
	}
}

func TestPetAffectionReachesTenStarsInAboutHalfAYear(t *testing.T) {
	p := petPreferences{}
	day := time.Date(2026, 1, 1, 12, 0, 0, 0, time.Local)
	for i := 0; i < 182; i++ {
		now := day.AddDate(0, 0, i)
		p.reward("whale", "", now)
		for range 3 {
			p.reward("whale", "pat", now)
		}
		for range 2 {
			p.reward("whale", "task", now)
		}
	}
	if got := p.Affection["whale"].Points; got != petMaxAffection {
		t.Fatalf("after 182 regular days got %d", got)
	}
}

func TestPetClickAndSettings(t *testing.T) {
	a := &desktop{prefs: defaultPrefs()}
	tt := ui.NewTester(a.petView, 180, 180)
	if err := tt.Click("摸摸宠物的头"); err != nil {
		t.Fatal(err)
	}
	if got := a.prefs.Pet.Affection["whale"].Points; got != 25 {
		t.Fatalf("click did not award daily companionship and pat: %d", got)
	}
	a.prefs.Pet.Enabled = false
	settings := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(a.theme())
		ui.Column(c).Fill().Children(func() { a.petSettings(c) })
	}, 600, 280)
	if err := settings.Click("显示桌面宠物"); err != nil {
		t.Fatal(err)
	}
	if !a.prefs.Pet.Enabled {
		t.Fatal("pet setting did not toggle")
	}
	if err := settings.Click("闲时说话"); err != nil {
		t.Fatal(err)
	}
	if !a.prefs.Pet.MuteChatter {
		t.Fatal("chatter setting did not turn off")
	}
	if err := settings.Click("闲时说话"); err != nil {
		t.Fatal(err)
	}
	if a.prefs.Pet.MuteChatter {
		t.Fatal("chatter setting did not turn back on")
	}
	if err := settings.Click("大"); err != nil {
		t.Fatal(err)
	}
	if a.prefs.Pet.Size != 230 {
		t.Fatal("pet size selection did not persist")
	}
}

func TestPetContextMenuOnHeadAndBody(t *testing.T) {
	a := &desktop{prefs: defaultPrefs()}
	a.prefs.Pet.Enabled = true
	tt := ui.NewTester(a.petView, 180, 180)
	want := []string{
		"摸摸头", "和我说句话", "查看好感度", "-", "宠物大小",
		petMenuCheckLabel("闲时说话", !a.prefs.Pet.MuteChatter), "-", "打开主界面", "宠物设置", "关闭桌宠",
	}
	for _, target := range []string{"摸摸宠物的头", "拖动宠物位置"} {
		if err := tt.RightClick(target); err != nil {
			t.Fatal(err)
		}
		if got := tt.Menu(); !slices.Equal(got, want) {
			t.Fatalf("right-click %s: got menu %q", target, got)
		}
		if err := tt.ChooseMenuItem("宠物大小", petMenuCheckLabel("大", a.prefs.Pet.Size == 230)); err != nil {
			t.Fatal(err)
		}
	}
	if a.prefs.Pet.Size != 230 {
		t.Fatalf("menu did not change size: %d", a.prefs.Pet.Size)
	}
	for _, muted := range []bool{true, false} {
		if err := tt.RightClick("拖动宠物位置"); err != nil {
			t.Fatal(err)
		}
		if err := tt.ChooseMenuItem(petMenuCheckLabel("闲时说话", !a.prefs.Pet.MuteChatter)); err != nil {
			t.Fatal(err)
		}
		if a.prefs.Pet.MuteChatter != muted {
			t.Fatalf("menu chatter toggle: muted = %v, want %v", a.prefs.Pet.MuteChatter, muted)
		}
	}
	if err := tt.RightClick("摸摸宠物的头"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("摸摸头"); err != nil {
		t.Fatal(err)
	}
	if a.prefs.Pet.Affection["whale"].Points != 25 {
		t.Fatal("menu pat did not award affection")
	}
	if err := tt.RightClick("摸摸宠物的头"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("关闭桌宠"); err != nil {
		t.Fatal(err)
	}
	if a.prefs.Pet.Enabled {
		t.Fatal("menu did not disable the pet")
	}
}

func TestPetChatterRespectsActivityAndCanWakeSleepingPet(t *testing.T) {
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.Local)
	a := &desktop{prefs: defaultPrefs()}
	a.pet.lastActivity = now.Add(-10 * time.Minute)
	if !a.petCanChatter(now) || a.avatarPose(now, false) != "sleeping" {
		t.Fatal("long-idle sleeping pet should still be able to chat")
	}
	a.sayPetChatter(now, false)
	if a.avatarPose(now, false) != "idle" || a.avatarPose(now.Add(petBubbleDuration+time.Second), false) != "sleeping" {
		t.Fatal("automatic chatter should wake the pet briefly, then let it sleep")
	}
	a.pet.poseUntil = time.Time{}
	for name, set := range map[string]func(){
		"muted":       func() { a.prefs.Pet.MuteChatter = true },
		"working":     func() { a.pet.working = map[string]bool{"run:1": true} },
		"reaction":    func() { a.pet.poseUntil = now.Add(time.Second) },
		"bubble":      func() { a.pet.bubbleUntil = now.Add(time.Second) },
		"interaction": func() { a.pet.lastActivity = now.Add(-petChatterQuietTime + time.Second) },
	} {
		set()
		if a.petCanChatter(now) {
			t.Errorf("chatter was not deferred for %s", name)
		}
		a.prefs.Pet.MuteChatter = false
		a.pet.working = nil
		a.pet.poseUntil, a.pet.bubbleUntil = time.Time{}, time.Time{}
		a.pet.lastActivity = now.Add(-10 * time.Minute)
	}
	if delay := petNextChatterDelay(now); delay < 4*time.Minute || delay >= 7*time.Minute {
		t.Fatalf("next chatter delay out of range: %s", delay)
	}
}

func TestPetCloseClearsBubblePriority(t *testing.T) {
	a := &desktop{pet: petRuntime{bubbleImportant: true, bubbleUntil: time.Now().Add(time.Minute), bubbleText: "完成", pendingBubbles: []string{"下一条"}, bubbleOverflow: 1}}
	a.closePet()
	if a.pet.bubbleImportant || !a.pet.bubbleUntil.IsZero() || a.pet.bubbleText != "" || len(a.pet.pendingBubbles) != 0 || a.pet.bubbleOverflow != 0 {
		t.Fatal("closed pet retained bubble state")
	}
}

func TestPetTaskEventsAndDeduplication(t *testing.T) {
	a := &desktop{prefs: defaultPrefs(), pet: petRuntime{win: &mygo.Window{}, working: map[string]bool{}}}
	a.prefs.Pet.Enabled = true
	p := map[string]any{"runId": "r1", "sessionId": "closed-tab", "durationMs": 12000}
	a.applyEvent("run:start", p)
	if !a.pet.working["run:r1"] {
		t.Fatal("working pose not set")
	}
	a.applyEvent("run:done", p)
	a.applyEvent("run:done", p)
	if len(a.pet.working) != 0 || a.prefs.Pet.Affection["whale"].Points != 30 {
		t.Fatalf("completion not processed once without a tab: %+v", a.prefs.Pet.Affection)
	}
	if got := a.petCompletionText("run:done", map[string]any{"durationMs": 12000, "workspace": "/work/ally-agent"}); got != "工作区「ally-agent」完成，用时 12s。" {
		t.Fatalf("completion must name the workspace: %q", got)
	}
	a.applyEvent("run:error", map[string]any{"runId": "failed"})
	if a.prefs.Pet.Affection["whale"].Points != 30 {
		t.Fatal("failed task earned affection")
	}
	task := map[string]any{"id": "scheduled", "name": "备份", "lastRunAt": 1000, "updatedAt": 4500, "runCount": 1}
	event := map[string]any{"task": task}
	a.applyEvent("scheduled:run_start", event)
	a.applyEvent("scheduled:run_done", event)
	a.applyEvent("scheduled:run_done", event)
	if got := a.petCompletionText("scheduled:run_done", event); got != "定时任务「备份」已完成，用时 4s。" {
		t.Fatalf("wrong scheduled completion: %q", got)
	}
	if a.prefs.Pet.Affection["whale"].Points != 40 || len(a.pet.working) != 0 {
		t.Fatalf("scheduled completion duplicated or left running: %+v", a.pet)
	}
}

func TestPetPositionRestoresAcrossDisplays(t *testing.T) {
	displays := []mygo.Display{{WorkArea: mygo.Rectangle{X: 0, Y: 0, Width: 1920, Height: 1040}}, {WorkArea: mygo.Rectangle{X: -1280, Y: 0, Width: 1280, Height: 1000}}}
	p := petPreferences{Size: 180, HasPosition: true, X: -900, Y: 600}
	if x, y := petRestoredPosition(p, displays, displays[0].WorkArea); x != -900 || y != 600 {
		t.Fatalf("lost second-display position: %d,%d", x, y)
	}
	p.X = -3000
	if x, y := petRestoredPosition(p, displays, displays[0].WorkArea); x != 1716 || y != 836 {
		t.Fatalf("disconnected display did not fall back: %d,%d", x, y)
	}
}

func TestPetQueuesConcurrentCompletionBubbles(t *testing.T) {
	a := &desktop{win: &mygo.Window{}, pet: petRuntime{
		win:             &mygo.Window{},
		bubbleImportant: true,
		bubbleUntil:     time.Now().Add(time.Minute),
	}}
	a.showPetBubble("摸头回复", false)
	for range 10 {
		a.showPetBubble("任务完成", true)
	}
	if len(a.pet.pendingBubbles) != 8 || a.pet.bubbleOverflow != 2 {
		t.Fatalf("concurrent completions were lost or not bounded: %+v", a.pet)
	}
}
