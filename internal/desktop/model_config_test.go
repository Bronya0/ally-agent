// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	backend "ally-dev/internal/app"
	"ally-dev/internal/chatgpt"
	"github.com/egoist/mygo/ui"
	"testing"
)

func TestOAuthModelIdentityKeepsAccountsSeparate(t *testing.T) {
	a := demoDesktop()
	one := backend.ModelConfig{AuthType: "chatgpt_oauth", ProviderName: "ChatGPT Official", Model: "shared", OAuthAccountID: "account-1"}
	two := one
	two.OAuthAccountID = "account-2"
	a.config.Models = []backend.ModelConfig{one, two}
	a.config.LastUsedModel = modelIdentityPtr(two)
	if modelOption(one) == modelOption(two) {
		t.Fatal("different accounts share an option")
	}
	fresh := a.freshConversation("workspace")
	if fresh.Model != modelOption(two) {
		t.Fatalf("wrong default account: %q", fresh.Model)
	}
	tab := a.current()
	tab.Model = modelOption(one)
	a.updateModelLabels(modelIdentityPtr(two), backend.ModelConfig{ProviderName: two.ProviderName, Model: "changed", OAuthAccountID: two.OAuthAccountID})
	if tab.Model != modelOption(one) {
		t.Fatal("editing another account changed this conversation")
	}
}
func TestOAuthPanelNativeView(t *testing.T) {
	a := demoDesktop()
	a.page = "models"
	a.modelDraft = backend.ModelConfig{AuthType: "chatgpt_oauth", ProviderName: "ChatGPT Official"}
	a.modelForm.Open = true
	a.oauth = &oauthState{}
	a.modelKeys = []string{""}
	tt := ui.NewTester(a.view, 1280, 850)
	if _, ok := tt.Find("ChatGPT 账号"); !ok {
		t.Fatal("account control missing")
	}
	if _, ok := tt.Find("使用 ChatGPT 登录"); !ok {
		t.Fatal("sign-in button missing")
	}
	if _, ok := tt.Find("ChatGPT 模型"); !ok {
		t.Fatal("model selection missing")
	}
	tt.SetSize(900, 620)
	tt.Frame()
}

func TestOAuthDraftRequiresPlanAndModelFromAccount(t *testing.T) {
	a := demoDesktop()
	a.modelDraft = backend.ModelConfig{AuthType: "chatgpt_oauth", Model: "example", OAuthAccountID: "account"}
	a.oauth = &oauthState{accounts: []chatgpt.Account{{ID: "account", SignedIn: true}}}
	if _, err := a.preparedModelDraft(); err == nil {
		t.Fatal("accepted account without plan permission")
	}
	a.oauth.accounts[0].PlanEnabled = true
	if _, err := a.preparedModelDraft(); err == nil {
		t.Fatal("accepted model absent from account catalogue")
	}
	a.oauth.models = []chatgpt.Model{{Slug: "example"}}
	m, err := a.preparedModelDraft()
	if err != nil || m.OAuthAccountID != "account" || m.APIKey != "" {
		t.Fatalf("valid oauth draft: %+v, %v", m, err)
	}
}

func TestOAuthNavigationInvalidatesLogin(t *testing.T) {
	a := demoDesktop()
	a.page = "models"
	a.oauth = &oauthState{busy: true, loading: true, epoch: 2}
	a.newSession()
	if a.oauth.busy || a.oauth.loading || a.oauth.epoch != 3 || a.oauth.notice != "登录已取消" {
		t.Fatal("leaving model page did not invalidate pending login")
	}
	a.oauth = &oauthState{loggingOut: true, epoch: 4}
	a.cancelOAuthWhenLeaving()
	if !a.oauth.loggingOut || a.oauth.epoch != 4 {
		t.Fatal("leaving page discarded pending logout completion")
	}
}
