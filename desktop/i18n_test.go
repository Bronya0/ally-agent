// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	backend "ally-dev/internal/app"
	"ally-dev/locales"
	"os"
	"testing"

	"github.com/egoist/mygo/i18n"
	"github.com/egoist/mygo/ui"
)

// Existing behavioral fixtures describe the Chinese UI. Pin their language so
// tests are independent of the developer's system and saved app language.
func TestMain(m *testing.M) {
	i18n.SetLocaleFunc(func() string { return "zh-Hans" })
	os.Exit(m.Run())
}

func useTestLocale(t *testing.T, tag string) {
	t.Helper()
	i18n.SetLocaleFunc(func() string { return tag })
	t.Cleanup(func() { i18n.SetLocaleFunc(func() string { return "zh-Hans" }) })
}

func TestSSHAuthValuesAcrossLanguages(t *testing.T) {
	for _, tag := range []string{"en", "zh-Hans", "ja", "ko"} {
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

func TestSettingsRenderAcrossLanguages(t *testing.T) {
	for _, tag := range []string{"en", "zh-Hans", "ja", "ko"} {
		t.Run(tag, func(t *testing.T) {
			useTestLocale(t, tag)
			a := settingsLayoutFixture()
			for module := range 6 {
				a.settings.Modules["settingsGeneral"] = module
				tt := settingsLayoutTester(a, 740, 1800)
				tt.SetLocale(tag)
				tt.Frame()
			}
			for _, section := range settingsSections {
				if section == "版本亮点" {
					continue
				}
				a.settings.Tab = section
				tt := ui.NewTester(func(c *ui.Context) { c.SetTheme(a.theme()); a.settingsPageBody(c) }, 1000, 1400)
				tt.SetLocale(tag)
				tt.Frame()
			}
		})
	}
}

func TestNativePagesRenderAcrossLanguages(t *testing.T) {
	for _, tag := range []string{"en", "zh-Hans", "ja", "ko"} {
		t.Run(tag, func(t *testing.T) {
			useTestLocale(t, tag)
			a := settingsLayoutFixture()
			a.config.Models = []backend.ModelConfig{{ProviderName: "用户供应商", Model: "custom-id", BaseURL: "https://example.invalid/v1"}}
			a.sshServers = []backend.SSHServerNode{{ID: "one", Alias: "用户名称", Group: "", Host: "localhost", Port: 22, Username: "tester", AuthType: "agent"}, {ID: "two", Alias: "用户名称2", Group: "用户分组", Host: "localhost", Port: 22, Username: "tester", AuthType: "key"}}
			for _, view := range []func(*ui.Context){a.modelsPageBody, a.mcpPageBody, a.sshPageBody, a.skillsPageBody, a.tasksPageBody, a.pluginsPageBody, a.statsPageBody, a.gamesPageBody} {
				tt := ui.NewTester(func(c *ui.Context) { c.SetTheme(a.theme()); ui.Column(c).Fill().Children(func() { view(c) }) }, 740, 1100)
				tt.SetLocale(tag)
				tt.Frame()
			}
			for _, modelTab := range []string{"图片模型", "语音模型"} {
				a.modelTab = modelTab
				tt := ui.NewTester(func(c *ui.Context) {
					c.SetTheme(a.theme())
					ui.Column(c).Fill().Children(func() { a.modelsPageBody(c) })
				}, 740, 1100)
				tt.SetLocale(tag)
				tt.Frame()
			}
			if a.sshServers[0].Alias != "用户名称" || a.config.Models[0].Model != "custom-id" {
				t.Fatal("rendering changed user data")
			}
		})
	}
}
