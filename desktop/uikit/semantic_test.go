package uikit

import (
	"github.com/egoist/mygo/ui"
	"testing"
)

func TestSemanticControls(t *testing.T) {
	selected, on, clicks := 0, false, 0
	tt := chineseTester(func(c *ui.Context) {
		ui.Column(c).Gap(12).Children(func() {
			Action(c.Key("action"), "运行", ActionOptions{Tone: Primary, Style: Secondary}).OnClick(func() { clicks++ })
			Action(c.Key("disabled"), "禁用", ActionOptions{Disabled: true}).OnClick(func() { clicks++ })
			Radios(c.Key("radios"), &selected, []RadioOption[int]{{0, "第一项", false}, {1, "第二项", false}}, true)
			Switch(c.Key("switch"), &on, "启用")
			ColorTag(c.Key("tag"), "可移除", TagOptions{Tone: Success, OnClose: func() { clicks++ }})
		})
	}, 480, 300)
	for _, label := range []string{"运行", "第二项", "启用", "移除 可移除"} {
		if err := tt.Click(label); err != nil {
			t.Fatal(err)
		}
		tt.Frame()
	}
	if clicks != 2 || selected != 1 || !on {
		t.Fatalf("clicks=%d selected=%d on=%v", clicks, selected, on)
	}
	if err := tt.Click("禁用"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if clicks != 2 {
		t.Fatal("disabled action activated")
	}
}

func TestNotificationActionAndClose(t *testing.T) {
	opened, actions, closed := false, 0, 0
	tt := chineseTester(func(c *ui.Context) {
		c.DisableAnimations()
		if opened {
			opened = false
			Notification(c, NotificationOptions{Title: "连接成功", Description: "服务器已连接", Timeout: -1, Action: "查看详情", OnAction: func() { actions++ }, OnClose: func() { closed++ }})
		}
		Messages(c)
	}, 500, 300)
	opened = true
	tt.Frame()
	if !tt.HasText("连接成功") || !tt.HasText("服务器已连接") {
		t.Fatal("notification content missing")
	}
	if err := tt.Click("查看详情"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if actions != 1 || closed != 1 || tt.HasText("连接成功") {
		t.Fatalf("action=%d closed=%d", actions, closed)
	}
	opened = true
	tt.Frame()
	if err := tt.Click("关闭通知"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if actions != 1 || closed != 2 || tt.HasText("连接成功") {
		t.Fatalf("action=%d closed=%d", actions, closed)
	}
}

func TestActionLoadingKeepsNeighborPosition(t *testing.T) {
	loading := false
	tt := chineseTester(func(c *ui.Context) {
		ui.Row(c).Children(func() {
			Action(c.Key("load"), "保存", ActionOptions{Loading: loading})
			ui.Text(c, "相邻操作")
		})
	}, 320, 100)
	before, ok := tt.Find("相邻操作")
	if !ok {
		t.Fatal("missing neighbor")
	}
	loading = true
	tt.Frame()
	after, _ := tt.Find("相邻操作")
	if before.X != after.X {
		t.Fatal("loading moved the neighboring action")
	}
}

func TestRadioKeyboardAndSwitchLock(t *testing.T) {
	selected, on := 0, false
	tt := chineseTester(func(c *ui.Context) {
		ui.Column(c).Children(func() {
			Radios(c.Key("radio"), &selected, []RadioOption[int]{{0, "One", false}, {1, "Two", true}, {2, "Three", false}}, false)
			SwitchWithOptions(c.Key("lock"), &on, "Locked", SwitchOptions{Loading: true})
		})
	}, 480, 160)
	if err := tt.Click("One"); err != nil {
		t.Fatal(err)
	}
	tt.Key(0, ui.KeyRight)
	tt.Frame()
	if selected != 2 {
		t.Fatalf("keyboard should skip disabled option: %d", selected)
	}
	if err := tt.Click("Locked"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if on {
		t.Fatal("loading switch changed")
	}
}

func TestRadiosPreserveFocusWhenReordered(t *testing.T) {
	selected := "a"
	options := []RadioOption[string]{{Value: "a", Label: "A"}, {Value: "b", Label: "B"}, {Value: "c", Label: "C"}}
	tt := chineseTester(func(c *ui.Context) { Radios(c.Key("items"), &selected, options, false) }, 400, 120)
	if err := tt.Click("B"); err != nil {
		t.Fatal(err)
	}
	options = []RadioOption[string]{options[2], options[1], options[0]}
	tt.Frame()
	tt.Key(0, ui.KeyRight)
	if selected != "a" {
		t.Fatalf("focus moved to another option on reorder: selected=%q", selected)
	}
}

func TestActionDefaultsToContentWidth(t *testing.T) {
	var compact, full ui.Handle
	var compactWidth, fullWidth float32
	tt := chineseTester(func(c *ui.Context) {
		ui.Column(c).FillWidth().Children(func() {
			Action(c.Key("compact"), "导出内存诊断", ActionOptions{}).Bind(&compact)
			Action(c.Key("full"), "显式铺满", ActionOptions{}).FillWidth().Bind(&full)
		})
		compactWidth, fullWidth = compact.Bounds(c).W, full.Bounds(c).W
	}, 600, 160)
	tt.Frame()
	if compactWidth <= 0 || compactWidth >= 300 || fullWidth < 590 {
		t.Fatalf("compact=%v full=%v", compactWidth, fullWidth)
	}
}
