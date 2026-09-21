// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU
// Public License v3. See the LICENSE file for details.
package app

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"time"

	openai "github.com/sashabaranov/go-openai"
)

// gameAICancel holds the cancel func of the in-flight GameAIAction call.
// The games panel is single-flight by design (its UI locks while thinking),
// so one slot is enough; a stale cancel func is simply a no-op.
var gameAICancel atomic.Pointer[context.CancelFunc]

// GameAIActionCancel aborts the in-flight GameAIAction request, if any.
// Intended for the games panel's stop button; safe to call when nothing is
// running.
func (a *App) GameAIActionCancel() {
	if fn := gameAICancel.Load(); fn != nil {
		(*fn)()
	}
}

// GameAIActionResult carries the model's reply plus the provider-reported
// token usage so the games panel can show per-call and per-game context size.
type GameAIActionResult struct {
	Reply            string `json:"reply"`
	PromptTokens     int    `json:"promptTokens"`
	CompletionTokens int    `json:"completionTokens"`
	TotalTokens      int    `json:"totalTokens"`
}

// GameAIAction runs one one-shot model round trip for the games panel's
// play-against-model mode. All game rules and board serialization live in the
// frontend (frontend/src/games); this binding is transport only. Config
// assembly mirrors TestModelConnection so per-model key pools, custom headers
// and the global proxy behave exactly like a chat request with that model.
func (a *App) GameAIAction(model ModelConfig, system, user string) (GameAIActionResult, error) {
	system = strings.TrimSpace(system)
	user = strings.TrimSpace(user)
	if user == "" {
		return GameAIActionResult{}, errors.New("prompt is required")
	}
	// A serialized board plus rules fits comfortably in this budget; the cap
	// keeps a runaway caller from streaming whole files through this binding.
	const maxPromptBytes = 64 * 1024
	if len(system) > maxPromptBytes || len(user) > maxPromptBytes {
		return GameAIActionResult{}, errors.New("prompt is too large")
	}
	networkCfg := a.effectiveConfig(ConfigState{})
	cfg := ConfigState{
		ProviderName:    model.ProviderName,
		APIFormat:       normalizeAPIFormat(model.APIFormat),
		BaseURL:         strings.TrimSpace(model.BaseURL),
		APIKey:          strings.TrimSpace(model.APIKey),
		APIKeys:         cloneStringSlice(model.APIKeys),
		Model:           strings.TrimSpace(model.Model),
		MaxTokens:       model.MaxTokens,
		ContextWindow:   model.ContextWindow,
		TokenParam:      normalizeTokenParam(model.TokenParam),
		ReasoningTag:    normalizeReasoningTag(model.ReasoningTag),
		VisionCapable:   model.VisionCapable,
		ReasoningEffort: normalizeReasoningEffort(model.ReasoningEffort),
		ProxyMode:       networkCfg.ProxyMode,
		ProxyURL:        networkCfg.ProxyURL,
		ProxyNoProxy:    networkCfg.ProxyNoProxy,
		UserAgent:       networkCfg.UserAgent,
		CustomHeaders:   normalizeCustomHeaders(model.CustomHeaders),
	}
	if cfg.Model == "" {
		return GameAIActionResult{}, errors.New("model is required")
	}
	if len(resolveKeyPool(cfg)) == 0 {
		return GameAIActionResult{}, errors.New("API key is required")
	}
	if cfg.MaxTokens <= 0 {
		cfg.MaxTokens = defaultMaxTokensForAPIFormat(cfg.APIFormat)
	}
	ctx := context.Background()
	if a.ctx != nil {
		ctx = a.ctx
	}
	// Reasoning models may think for a while before emitting the one-line
	// move, so allow a longer budget than the connectivity probe.
	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	cf := cancel
	gameAICancel.Store(&cf)
	defer gameAICancel.CompareAndSwap(&cf, nil)
	defer cancel()
	messages := make([]openai.ChatCompletionMessage, 0, 2)
	if system != "" {
		messages = append(messages, openai.ChatCompletionMessage{
			Role:    openai.ChatMessageRoleSystem,
			Content: system,
		})
	}
	messages = append(messages, openai.ChatCompletionMessage{
		Role:    openai.ChatMessageRoleUser,
		Content: user,
	})
	text, usage, err := a.completeModelTextWithUsage(ctx, cfg, cfg.Model, messages, cfg.MaxTokens)
	if err != nil {
		return GameAIActionResult{}, err
	}
	result := GameAIActionResult{Reply: text}
	if usage != nil {
		result.PromptTokens = usage.PromptTokens
		result.CompletionTokens = usage.CompletionTokens
		result.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	}
	return result, nil
}
