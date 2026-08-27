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
	"strings"
	"time"
)

// FetchModelList queries the OpenAI-standard `GET {baseUrl}/models` endpoint
// and returns the model id list (`{"object":"list","data":[{"id":...}]}`).
// Any HTTP error status, request failure, or unexpected response shape is
// returned verbatim as an error; the UI surfaces it directly.
func (a *App) FetchModelList(baseUrl, apiKey string) ([]string, error) {
	base := strings.TrimRight(strings.TrimSpace(baseUrl), "/")
	if base == "" {
		return nil, fmt.Errorf("base URL is required")
	}
	url := base + "/models"

	// Model-list fetching rides the user's network settings (proxy) the same
	// way LLM requests do; allowPrivate keeps local endpoints such as Ollama
	// or LM Studio reachable.
	networkCfg := a.effectiveConfig(ConfigState{})
	cfg := ConfigState{
		ProxyMode:    networkCfg.ProxyMode,
		ProxyURL:     networkCfg.ProxyURL,
		ProxyNoProxy: networkCfg.ProxyNoProxy,
		UserAgent:    networkCfg.UserAgent,
	}

	ctx := context.Background()
	if a.ctx != nil {
		ctx = a.ctx
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if key := strings.TrimSpace(apiKey); key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}

	resp, err := proxyHTTPClient(cfg, true, 15*time.Second).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	const maxResponseBytes = 8 << 20 // models lists are small; refuse absurd bodies
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, err
	}
	snippet := strings.TrimSpace(string(body))
	if len(snippet) > 200 {
		snippet = snippet[:200]
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("GET %s returned HTTP %d: %s", url, resp.StatusCode, snippet)
	}

	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil || payload.Data == nil {
		return nil, fmt.Errorf("unexpected response format from GET %s", url)
	}
	models := make([]string, 0, len(payload.Data))
	for _, item := range payload.Data {
		if id := strings.TrimSpace(item.ID); id != "" {
			models = append(models, id)
		}
	}
	return models, nil
}
