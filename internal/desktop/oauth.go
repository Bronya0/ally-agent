// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"ally-dev/internal/chatgpt"
	"fmt"
	"github.com/egoist/mygo/ui"
)

type oauthState struct {
	accounts                  []chatgpt.Account
	models                    []chatgpt.Model
	accountLabel, notice      string
	epoch                     uint64
	busy, loading, loggingOut bool
}

func accountLabel(a chatgpt.Account) string {
	name := a.Email
	if name == "" {
		name = a.Subject
	}
	if name == "" {
		name = "ChatGPT"
	}
	return name + " · " + a.ID
}
func (a *desktop) resetOAuth() {
	if a.oauth != nil && a.oauth.busy && a.core != nil {
		a.core.CancelChatGPTLogin()
	}
	a.oauth = &oauthState{}
}
func (a *desktop) cancelOAuthWhenLeaving() {
	if a.oauth == nil {
		return
	}
	o := a.oauth
	if o.loggingOut {
		return
	}
	o.epoch++
	o.loading = false
	if o.busy {
		if a.core != nil {
			a.core.CancelChatGPTLogin()
		}
		o.busy = false
		o.notice = "登录已取消"
	}
}

func (a *desktop) changeModelAuth(value string) {
	if value == "chatgpt_oauth" {
		a.modelDraft.AuthType = value
		a.modelDraft.ProviderName = "ChatGPT Official"
		a.modelDraft.APIFormat = "openai_responses"
		a.modelDraft.BaseURL = "https://api.openai.com/v1"
		a.modelDraft.ContextWindow = 512000
		a.modelContextWindow = 512000
		a.modelDraft.Model = ""
		a.modelDraft.OAuthAccountID = ""
		a.modelDraft.APIKey = ""
		a.modelDraft.APIKeys = nil
		a.modelDraft.CustomHeaders = nil
		a.resetOAuth()
		a.loadOAuthAccounts()
	} else {
		a.resetOAuth()
		a.modelDraft.AuthType = "api_key"
		a.modelDraft.OAuthAccountID = ""
		a.modelDraft.Model = ""
		if a.modelDraft.ProviderName == "ChatGPT Official" {
			a.modelDraft.ProviderName = ""
		}
		a.modelDraft.APIFormat = "openai"
		a.modelDraft.BaseURL = ""
	}
}
func (a *desktop) loadOAuthAccounts() {
	o := a.oauth
	if o == nil || a.core == nil || o.loggingOut || o.busy {
		return
	}
	o.epoch++
	epoch := o.epoch
	o.loading = true
	go func() {
		accounts, err := a.core.GetChatGPTAccounts()
		a.win.Update(func() {
			if a.oauth != o || o.epoch != epoch || a.closing {
				return
			}
			o.loading = false
			if err != nil {
				o.notice = err.Error()
				return
			}
			o.accounts = accounts
			selected := a.modelDraft.OAuthAccountID
			found := false
			for _, account := range accounts {
				if account.ID == selected {
					o.accountLabel = accountLabel(account)
					found = true
					break
				}
			}
			if !found {
				if selected != "" {
					a.modelDraft.Model = ""
					a.modelDraft.DisplayName = ""
				}
				selected = ""
				o.accountLabel = ""
				a.modelDraft.OAuthAccountID = ""
				if len(accounts) > 0 {
					selected = accounts[0].ID
					o.accountLabel = accountLabel(accounts[0])
					a.modelDraft.OAuthAccountID = selected
				}
			}
			if selected != "" {
				a.fetchOAuthModels(selected)
			}
		})
	}()
}
func (a *desktop) selectOAuthAccount(label string) {
	o := a.oauth
	if o == nil {
		return
	}
	for _, account := range o.accounts {
		if accountLabel(account) == label {
			if a.modelDraft.OAuthAccountID != account.ID {
				a.modelDraft.OAuthAccountID = account.ID
				a.modelDraft.Model = ""
				a.modelDraft.DisplayName = ""
			}
			a.fetchOAuthModels(account.ID)
			return
		}
	}
}
func (a *desktop) fetchOAuthModels(id string) {
	o := a.oauth
	if o == nil || a.core == nil {
		return
	}
	o.epoch++
	epoch := o.epoch
	o.models = nil
	o.loading = false
	allowed := false
	for _, account := range o.accounts {
		if account.ID == id && account.PlanEnabled {
			allowed = true
			break
		}
	}
	if !allowed {
		return
	}
	o.loading = true
	go func() {
		models, err := a.core.FetchChatGPTModels(id)
		a.win.Update(func() {
			if a.oauth != o || o.epoch != epoch || a.modelDraft.OAuthAccountID != id || a.closing {
				return
			}
			o.loading = false
			if err != nil {
				o.notice = err.Error()
				return
			}
			o.models = models
			if len(models) == 0 {
				o.notice = "该账号没有返回可用模型"
			} else {
				o.notice = ""
			}
		})
	}()
}
func (a *desktop) loginOAuth(id string, enablePlan bool) {
	o := a.oauth
	if o == nil || o.busy || o.loggingOut || a.core == nil {
		return
	}
	o.epoch++
	epoch := o.epoch
	o.busy = true
	o.loading = false
	o.models = nil
	o.notice = "请在浏览器完成授权…"
	go func() {
		account, err := a.core.LoginChatGPT(id, enablePlan)
		var accounts []chatgpt.Account
		if err == nil {
			accounts, err = a.core.GetChatGPTAccounts()
		}
		a.win.Update(func() {
			if a.oauth != o || o.epoch != epoch || a.closing {
				return
			}
			o.busy = false
			if err != nil {
				o.notice = err.Error()
				return
			}
			o.accounts = accounts
			o.accountLabel = accountLabel(account)
			if a.modelDraft.OAuthAccountID != account.ID {
				a.modelDraft.Model = ""
				a.modelDraft.DisplayName = ""
			}
			a.modelDraft.OAuthAccountID = account.ID
			if account.PlanEnabled {
				o.notice = "登录成功，正在获取模型"
				a.fetchOAuthModels(account.ID)
			} else {
				o.notice = "已登录；如需使用套餐调用，请点击“启用套餐调用”并完成授权"
			}
		})
	}()
}
func (a *desktop) logoutOAuth() {
	o := a.oauth
	if o == nil || o.busy || o.loggingOut || a.core == nil {
		return
	}
	id := a.modelDraft.OAuthAccountID
	if id == "" {
		return
	}
	o.epoch++
	epoch := o.epoch
	o.loggingOut = true
	o.loading = false
	o.models = nil
	go func() {
		result, err := a.core.LogoutChatGPT(id)
		var accounts []chatgpt.Account
		if err == nil {
			accounts, err = a.core.GetChatGPTAccounts()
		}
		a.win.Update(func() {
			if a.oauth != o || o.epoch != epoch || a.closing {
				return
			}
			o.loggingOut = false
			if err != nil {
				o.notice = err.Error()
				return
			}
			o.accounts = accounts
			o.notice = "账号已退出"
			if !result.RemoteRevoked {
				o.notice = "本地账号已退出，远程授权未撤销"
			}
			for _, account := range accounts {
				if account.ID == id {
					o.accountLabel = accountLabel(account)
					return
				}
			}
			o.accountLabel = ""
			a.modelDraft.OAuthAccountID = ""
			a.modelDraft.Model = ""
			a.modelDraft.DisplayName = ""
		})
	}()
}
func (a *desktop) oauthPanel(c *ui.Context) {
	o := a.oauth
	if o == nil {
		a.oauth = &oauthState{}
		o = a.oauth
	}
	ui.Column(c).Padding(12).Gap(8).Border(1, line).Radius(6).Children(func() {
		ui.Text(c, "ChatGPT").Bold()
		ui.Text(c, "使用 ChatGPT 套餐与额度。登录后在模型管理中添加模型。").FontSize(12).TextColor(c.Theme().TextMuted)
		options := make([]string, 0, len(o.accounts))
		for _, account := range o.accounts {
			options = append(options, accountLabel(account))
		}
		if ui.Select(c, &o.accountLabel, options).Label("ChatGPT 账号").FillWidth().Disabled(o.busy || o.loggingOut).Changed() {
			a.selectOAuthAccount(o.accountLabel)
		}
		var current *chatgpt.Account
		for i := range o.accounts {
			if o.accounts[i].ID == a.modelDraft.OAuthAccountID {
				current = &o.accounts[i]
				break
			}
		}
		if current != nil {
			state := "未登录"
			if current.SignedIn {
				state = "已登录，未授权套餐调用"
				if current.PlanEnabled {
					state = "已授权套餐调用"
				}
			}
			ui.Text(c, fmt.Sprintf("%s · %s", accountLabel(*current), state)).FontSize(11).TextColor(c.Theme().TextMuted)
		}
		ui.Row(c).Gap(6).Wrap().Children(func() {
			ui.Button(c, "使用 ChatGPT 登录").Disabled(o.busy || o.loggingOut).OnClick(func() { a.loginOAuth("", false) })
			if o.busy {
				ui.Button(c, "取消登录").OnClick(func() { a.core.CancelChatGPTLogin() })
			}
			if current != nil {
				label := "重新登录"
				plan := false
				if !current.PlanEnabled {
					label = "启用套餐调用"
					plan = true
				}
				id := current.ID
				ui.Button(c, label).Disabled(o.busy || o.loggingOut).OnClick(func() { a.loginOAuth(id, plan) })
				if current.SignedIn {
					ui.Button(c, "退出账号").Disabled(o.busy || o.loggingOut).OnClick(a.logoutOAuth)
				}
				ui.Button(c, "刷新模型").Disabled(o.busy || o.loading || o.loggingOut || !current.PlanEnabled).OnClick(func() { a.fetchOAuthModels(id) })
			}
			services := c.Services()
			ui.Button(c, "查看套餐用量").OnClick(func() { services.OpenURL("https://chatgpt.com/settings/usage") })
		})
		if o.loading || o.busy {
			ui.Text(c, "正在处理…").FontSize(11).TextColor(c.Theme().TextMuted)
		}
		if o.notice != "" {
			ui.Text(c, o.notice).FontSize(11).TextColor(c.Theme().TextMuted).MaxLines(4)
		}
	})
}
