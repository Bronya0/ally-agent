// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// deepSeekBalanceHost is the only host serving GET /user/balance. Relays that
// re-serve DeepSeek models have no such endpoint, so the UI offers the balance
// button for that host alone and the request refuses everything else.
const deepSeekBalanceHost = "api.deepseek.com"

// ModelBalance is one currency line of DeepSeek's balance response.
type ModelBalance struct {
	Currency string
	Total    string
}

// IsDeepSeekOfficialEndpoint reports whether baseURL points at DeepSeek's own
// API. It is what the models page uses to decide whether to show the balance
// button, so a relay URL that merely mentions "deepseek" stays excluded.
func IsDeepSeekOfficialEndpoint(baseURL string) bool {
	u, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return false
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return false
	}
	return strings.EqualFold(u.Hostname(), deepSeekBalanceHost)
}

// deepSeekBalanceEndpoint builds the balance URL from the origin: the endpoint
// always sits at the API root, so a base such as https://api.deepseek.com/v1
// must not turn into /v1/user/balance.
func deepSeekBalanceEndpoint(baseURL string) (string, error) {
	if !IsDeepSeekOfficialEndpoint(baseURL) {
		return "", fmt.Errorf("the balance API only exists on %s; this endpoint is not DeepSeek's official one", deepSeekBalanceHost)
	}
	u, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return "", err
	}
	return u.Scheme + "://" + u.Host + "/user/balance", nil
}

// QueryModelBalance reads the account balance behind one saved model. It runs
// only when the user clicks the button: nothing polls, caches or refreshes.
func (a *App) QueryModelBalance(model ModelConfig) ([]ModelBalance, error) {
	cfg := a.configForModelEntry(model)
	endpoint, err := deepSeekBalanceEndpoint(cfg.BaseURL)
	if err != nil {
		return nil, err
	}
	pool := resolveKeyPool(cfg)
	if len(pool) == 0 {
		return nil, fmt.Errorf("an API key is required to read the DeepSeek balance")
	}

	ctx := context.Background()
	if a.ctx != nil {
		ctx = a.ctx
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	client := proxyHTTPClient(cfg, false, 15*time.Second)
	return fetchDeepSeekBalance(ctx, client, cfg, endpoint, strings.TrimSpace(pool[0]))
}

// fetchDeepSeekBalance performs the GET itself. It is split from the official
// host check so the request shape stays testable without loosening that gate.
func fetchDeepSeekBalance(ctx context.Context, client *http.Client, cfg ConfigState, endpoint, key string) ([]ModelBalance, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)
	applyModelHeaders(req, cfg)

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("GET %s returned HTTP %d: %s", endpoint, resp.StatusCode, truncateRunes(strings.TrimSpace(string(body)), 200))
	}
	return parseDeepSeekBalance(body)
}

// parseDeepSeekBalance reads {"is_available":…,"balance_infos":[…]}.
func parseDeepSeekBalance(body []byte) ([]ModelBalance, error) {
	var payload struct {
		BalanceInfos []struct {
			Currency     string `json:"currency"`
			TotalBalance string `json:"total_balance"`
		} `json:"balance_infos"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("unexpected response format from the DeepSeek balance API")
	}
	out := make([]ModelBalance, 0, len(payload.BalanceInfos))
	for _, info := range payload.BalanceInfos {
		out = append(out, ModelBalance{Currency: strings.TrimSpace(info.Currency), Total: strings.TrimSpace(info.TotalBalance)})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("the DeepSeek balance API returned no balance information")
	}
	return out, nil
}
