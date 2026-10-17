// SPDX-License-Identifier: GPL-3.0-only
package app

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"time"

	"ally-dev/internal/chatgpt"
)

const (
	authChatGPTOAuth     = "chatgpt_oauth"
	chatGPTProviderName  = "ChatGPT Official"
	chatGPTContextWindow = 512000
)

func (a *App) chatGPTManager() *chatgpt.Manager {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.chatGPT == nil {
		a.chatGPT = chatgpt.New(filepath.Join(appDataDir(), "chatgpt"), chatgpt.NewSystemProtector())
	}
	return a.chatGPT
}
func (a *App) chatGPTContext(timeout time.Duration) (context.Context, context.CancelFunc) {
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithTimeout(ctx, timeout)
}
func (a *App) chatGPTClient(timeout time.Duration) *http.Client {
	cfg := a.effectiveConfig(ConfigState{})
	client := proxyHTTPClient(cfg, true, timeout)
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return client
}
func (a *App) GetChatGPTAccounts() ([]chatgpt.Account, error) {
	ctx, cancel := a.chatGPTContext(15 * time.Second)
	defer cancel()
	return a.chatGPTManager().Accounts(ctx)
}
func (a *App) LoginChatGPT(accountID string, enablePlan bool) (chatgpt.Account, error) {
	ctx, cancel := a.chatGPTContext(5 * time.Minute)
	defer cancel()
	account, err := a.chatGPTManager().Login(ctx, a.chatGPTClient(30*time.Second), accountID, enablePlan, a.openChatGPTAuthorization)
	if err == nil {
		a.showMainWindow()
	}
	return account, err
}
func (a *App) CancelChatGPTLogin() { a.chatGPTManager().CancelLogin() }
func (a *App) LogoutChatGPT(accountID string) (chatgpt.LogoutResult, error) {
	ctx, cancel := a.chatGPTContext(15 * time.Second)
	defer cancel()
	return a.chatGPTManager().Logout(ctx, a.chatGPTClient(4*time.Second), accountID)
}
func (a *App) FetchChatGPTModels(accountID string) ([]chatgpt.Model, error) {
	if accountID == "" {
		return nil, errors.New("Select a ChatGPT account first")
	}
	ctx, cancel := a.chatGPTContext(45 * time.Second)
	defer cancel()
	return a.chatGPTManager().Models(ctx, a.chatGPTClient(30*time.Second), accountID)
}
func hasModelCredential(cfg ConfigState) bool {
	if cfg.AuthType == authChatGPTOAuth {
		return cfg.OAuthAccountID != ""
	}
	return len(resolveKeyPool(cfg)) > 0
}

func missingModelCredentialError(cfg ConfigState) error {
	if cfg.AuthType == authChatGPTOAuth {
		return errors.New("Select a ChatGPT account in Settings and authorize plan usage")
	}
	return errors.New("API key is required")
}

// Pin OAuth routes and remove all key-pool and custom-header state. This is
// applied at both config boundaries and the final provider dispatcher.
func normalizeModelAuth(cfg *ConfigState) {
	if cfg.AuthType != authChatGPTOAuth {
		return
	}
	cfg.ProviderName = chatGPTProviderName
	if cfg.ContextWindow <= 0 {
		cfg.ContextWindow = chatGPTContextWindow
	}
	cfg.APIFormat = apiFormatOpenAIResponses
	cfg.BaseURL = chatgpt.Resource
	cfg.APIKey = ""
	cfg.APIKeys = nil
	cfg.CustomHeaders = nil
}
