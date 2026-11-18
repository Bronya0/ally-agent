// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	backend "ally-dev/internal/app"
	"ally-dev/locales"
	"fmt"
	"os"
	"testing"

	"github.com/egoist/mygo/i18n"
	"github.com/egoist/mygo/ui"
)

// Existing behavioral fixtures describe the Chinese UI. Pin their language so
// tests are independent of the developer's system and saved app language.
func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "ally-desktop-test-home-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	for _, key := range []string{"HOME", "USERPROFILE"} {
		if err := os.Setenv(key, home); err != nil {
			fmt.Fprintln(os.Stderr, err)
			_ = os.RemoveAll(home)
			os.Exit(1)
		}
	}
	_ = os.Unsetenv("ALLY_DATA_DIR")
	i18n.SetLocaleFunc(func() string { return "zh-Hans" })
	code := m.Run()
	_ = os.RemoveAll(home)
	os.Exit(code)
}

func useTestLocale(t *testing.T, tag string) {
	t.Helper()
	i18n.SetLocaleFunc(func() string { return tag })
	t.Cleanup(func() { i18n.SetLocaleFunc(func() string { return "zh-Hans" }) })
}

func TestSSHAuthValuesAcrossLanguages(t *testing.T) {
	// 英文（最短）与日文（最长）足够暴露宽度问题，四种语言只是重复渲染。
	for _, tag := range []string{"en", "ja"} {
		t.Run(tag, func(t *testing.T) {
			useTestLocale(t, tag)
			a := &desktop{prefs: defaultPrefs()}
			a.sshF.EditorOpen = true
			a.sshF.Port = 22
			a.sshF.Draft = backend.SSHServerNode{AuthType: "agent", Alias: "用户自己的名称", Host: "localhost", Username: "tester"}
			tt := ui.NewTester(func(c *ui.Context) { c.SetTheme(a.theme()); a.sshEditorDialog(c) }, 900, 1000)
			tt.SetLocale(tag)
			for _, option := range []struct{ label, value string }{{"私钥文件", "key"}, {"密码", "password"}, {"本机免密", "agent"}} {
				control, ok := tt.AccessByRole(ui.RolePopUpButton, locales.Text("认证方式"))
				if !ok {
					t.Fatal("missing SSH auth selector")
				}
				r := control.Bounds
				tt.ClickAt(r.X+r.W/2, r.Y+r.H/2)
				tt.Frame()
				if err := tt.Click(locales.Text(option.label)); err != nil {
					t.Fatal(err)
				}
				tt.Frame()
				if a.sshF.Draft.AuthType != option.value || a.sshF.Draft.Alias != "用户自己的名称" {
					t.Fatalf("selection changed configuration data: %+v", a.sshF.Draft)
				}
			}
		})
	}
}

func TestSSHGroupEditorTypingAndSelection(t *testing.T) {
	a := &desktop{prefs: defaultPrefs(), sshServers: []backend.SSHServerNode{{ID: "one", Group: "33"}, {ID: "two", Group: "运维"}}}
	a.openSSHEditor("one", "33")
	tt := ui.NewTester(func(c *ui.Context) { c.SetTheme(a.theme()); a.sshEditorDialog(c) }, 900, 1000)
	input, ok := tt.AccessByRole(ui.RoleComboBox, "分组")
	if !ok {
		t.Fatal("missing group input")
	}
	tt.ClickAt(input.Bounds.X+input.Bounds.W/2, input.Bounds.Y+input.Bounds.H/2)
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("自定义")
	if a.sshF.GroupInput != "自定义" {
		t.Fatalf("typed group = %q", a.sshF.GroupInput)
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Key(0, ui.KeyBackspace)
	if a.sshF.GroupInput != "" {
		t.Fatalf("cleared group = %q", a.sshF.GroupInput)
	}
	tt.Key(0, ui.KeyDown)
	tt.Frame()
	if err := tt.Click("运维"); err != nil {
		t.Fatal(err)
	}
	if a.sshF.GroupInput != "运维" {
		t.Fatalf("selected group = %q", a.sshF.GroupInput)
	}
}

func TestLanguageChangePreservesModelDraft(t *testing.T) {
	a := &desktop{config: backend.ConfigState{Models: []backend.ModelConfig{{ProviderName: "自定义名字", Model: "my-model", BaseURL: "https://example.invalid/v1", TokenParam: "max_completion_tokens", MaxTokens: 8192, ContextWindow: 128000}}}}
	a.editModel(0)
	a.modelKeys = []string{"unsaved-key"}
	for _, tag := range []string{"en", "zh-Hans", "ja", "ko"} {
		t.Run(tag, func(t *testing.T) {
			useTestLocale(t, tag)
			tt := ui.NewTester(func(c *ui.Context) { c.SetTheme(a.theme()); a.chatModelDialog(c) }, 1000, 1000)
			tt.SetLocale(tag)
			tt.Frame()
			got, err := a.preparedModelDraft()
			if err != nil {
				t.Fatal(err)
			}
			if got.ProviderName != "自定义名字" || got.APIKey != "unsaved-key" || got.TokenParam != "max_completion_tokens" || got.MaxTokens != 8192 || got.ContextWindow != 128000 {
				t.Fatalf("language change changed model draft: %+v", got)
			}
		})
	}
}

func TestDisplayMemosRefreshOnlyOnChanges(t *testing.T) {
	var memo memoOf[string, string]
	var list indexedMemo[string, string]
	derived, listDerived := 0, 0
	for _, tag := range []string{"en", "zh-Hans", "ja", "ko"} {
		useTestLocale(t, tag)
		for range 3 {
			got := memo.get("same-data", func(string) string { derived++; return locales.Text("运行中") })
			rows := list.get(1, "", func(int) string { return "same-data" }, func() string { listDerived++; return locales.Text("运行中") })
			if got != locales.Text("运行中") || rows != got {
				t.Fatal("stale display language")
			}
		}
	}
	if derived != 4 || listDerived != 4 {
		t.Fatalf("unexpected derivations: %d/%d", derived, listDerived)
	}
}
