// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"fmt"
	"math"
	"testing"

	backend "ally-dev/internal/app"
	"github.com/egoist/mygo/ui"
)

func settingsLayoutFixture() *desktop {
	cfg := backend.ConfigState{
		LLMRetries: 6, ToolConcurrency: 4, CompactTimeoutSeconds: 180,
		AutoCompactMethod: "recent", CompactThreshold: .6,
		CompactThresholdMode: backend.CompactThresholdModeTokens, CompactThresholdTokens: 350000,
		SendSessionIDHeader: boolPtr(true), SendXSessionIDHeader: boolPtr(true),
	}
	a := &desktop{prefs: defaultPrefs(), config: cfg, configDraft: cfg, page: "settings"}
	a.syncSettingsForm()
	a.settings.Modules = map[string]int{"settingsGeneral": 3}
	return a
}

func settingsLayoutTester(a *desktop, width, height int) *ui.Tester {
	return ui.NewTester(func(c *ui.Context) {
		c.SetTheme(a.theme())
		ui.Column(c).Fill().Padding(padPage).Gap(gapPage).Children(func() { a.settingsGeneral(c) })
	}, width, height)
}

func settingsControlBounds(t *testing.T, tt *ui.Tester, role ui.Role, name string) ui.Rect {
	t.Helper()
	info, ok := tt.AccessByRole(role, name)
	if !ok || info.Bounds.W <= 0 || info.Bounds.H <= 0 {
		t.Fatalf("missing control geometry for role %v %q: %+v", role, name, info)
	}
	return info.Bounds
}

func TestSettingsLayoutSeparatesGroupsAndAlignsNumbers(t *testing.T) {
	for _, width := range []int{1100, 740, 480} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			a := settingsLayoutFixture()
			tt := settingsLayoutTester(a, width, 1800)
			tt.Frame()
			var previous ui.Rect
			for _, name := range []string{"请求与执行", "上下文管理", "会话请求头"} {
				r := settingsControlBounds(t, tt, ui.RoleGroup, name)
				if r.W > settingsMaxWidth+1 || r.X+r.W > float32(width)-padPage+1 {
					t.Fatalf("group exceeds readable page width: %+v", r)
				}
				if previous.H > 0 && r.Y < previous.Y+previous.H+gapPage-1 {
					t.Fatalf("groups lack separation: previous=%+v next=%+v", previous, r)
				}
				previous = r
			}
			var right float32
			for _, name := range []string{"LLM 请求重试次数", "工具执行并发度", "压缩超时时间"} {
				r := settingsControlBounds(t, tt, ui.RoleGroup, name)
				if r.W > settingsNumberWidth+1 || r.X+r.W > float32(width)-padPage+1 {
					t.Fatalf("number control stretched or overflowed: %q %+v", name, r)
				}
				if right != 0 && math.Abs(float64(r.X+r.W-right)) > 1 {
					t.Fatalf("number controls are not aligned: %q right=%v want=%v", name, r.X+r.W, right)
				}
				right = r.X + r.W
				caption, ok := tt.Find(name)
				if !ok {
					t.Fatalf("missing caption for %q", name)
				}
				if width == 480 && r.Y < caption.Y+caption.H+gapField-1 {
					t.Fatalf("narrow settings must place control below caption: %q caption=%+v control=%+v", name, caption, r)
				}
			}
			if a.settingsPendingEdits() {
				t.Fatal("layout-only rendering changed the saved configuration")
			}
		})
	}
}

func TestSettingsLayoutResizingPreservesNumericEdit(t *testing.T) {
	a := settingsLayoutFixture()
	tt := settingsLayoutTester(a, 1100, 1800)
	r := settingsControlBounds(t, tt, ui.RoleGroup, "LLM 请求重试次数")
	tt.ClickAt(r.X+10, r.Y+r.H/2)
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("5")
	tt.SetSize(480, 1800)
	tt.Frame()
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("7")
	tt.SetSize(1100, 1800)
	tt.Frame()
	cfg := a.settingsConfigDraft()
	if cfg.LLMRetries != 7 || cfg.ToolConcurrency != 4 || cfg.CompactThresholdTokens != 350000 || cfg.AutoCompactMethod != "recent" {
		t.Fatalf("resize lost an edit or changed another setting: retries=%d concurrency=%d tokens=%d method=%q", cfg.LLMRetries, cfg.ToolConcurrency, cfg.CompactThresholdTokens, cfg.AutoCompactMethod)
	}
	if !a.settingsPendingEdits() {
		t.Fatal("edited numeric value must still be saved on leave")
	}
}

func TestSettingsLayoutAPIBusyStateLocksControls(t *testing.T) {
	a := settingsLayoutFixture()
	a.settings.APIPort, a.settings.API.Port = 8080, 8080
	a.settings.API.Token = "test-token"
	a.settings.APIBusy = true
	tt := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(a.theme())
		ui.Column(c).Fill().Padding(padPage).Gap(gapPage).Children(func() { a.settingsAPI(c) })
	}, 900, 1600)
	control, ok := tt.AccessByRole(ui.RoleSwitch, "启用 API 服务")
	if !ok || !control.Disabled {
		t.Fatal("busy API switch must be visibly disabled")
	}
	caption, ok := tt.Find("启用 API 服务")
	if !ok {
		t.Fatal("missing API switch caption")
	}
	tt.ClickAt(caption.X+caption.W/2, caption.Y+caption.H/2)
	control, ok = tt.AccessByRole(ui.RoleSwitch, "启用 API 服务")
	if !ok || !control.Disabled || control.Checked || a.settings.APIRevision != 0 {
		t.Fatal("caption click bypassed the busy-state lock")
	}
	port := settingsControlBounds(t, tt, ui.RoleGroup, "API 端口")
	tt.ClickAt(port.X+10, port.Y+port.H/2)
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("9000")
	token := settingsControlBounds(t, tt, ui.RoleTextField, "API Token")
	tt.ClickAt(token.X+10, token.Y+token.H/2)
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("replacement")
	if a.settings.APIPort != 8080 || a.settings.API.Token != "test-token" {
		t.Fatal("busy form accepted port or token edits")
	}
}

func TestSettingsLayoutFontFieldsWrapWithoutStretching(t *testing.T) {
	for _, width := range []int{1100, 650, 480} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			a := settingsLayoutFixture()
			a.settings.Modules["settingsGeneral"] = 0
			tt := settingsLayoutTester(a, width, 2200)
			tt.Frame()
			var fields []ui.Rect
			for _, name := range []string{"消息正文", "代码块", "工具卡", "次级正文", "辅助文字"} {
				r := settingsControlBounds(t, tt, ui.RoleGroup, name)
				if r.W > settingsNumberWidth+1 || r.X+r.W > float32(width)-padPage+1 {
					t.Fatalf("font field stretched or overflowed: %q %+v", name, r)
				}
				for _, other := range fields {
					if r.X < other.X+other.W-1 && r.X+r.W > other.X+1 && r.Y < other.Y+other.H-1 && r.Y+r.H > other.Y+1 {
						t.Fatalf("font fields overlap: %+v %+v", other, r)
					}
				}
				fields = append(fields, r)
			}
		})
	}
}
