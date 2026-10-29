package uikit

import (
	"github.com/egoist/mygo/ui"
	"testing"
)

func TestSemanticControls(t *testing.T) {
	selected, on, clicks := 0, false, 0
	tt := ui.NewTester(func(c *ui.Context) {
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

func TestRadioKeyboardAndSwitchLock(t *testing.T) {
	selected, on := 0, false
	tt := ui.NewTester(func(c *ui.Context) {
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
