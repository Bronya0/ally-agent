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
	"errors"
	"strings"
	"testing"
	"time"
)

func TestNormalizeAPIKeys(t *testing.T) {
	got := normalizeAPIKeys([]string{"  k1  ", "", "k2", "k1", "k2", "k3"})
	want := []string{"k1", "k2", "k3"}
	if len(got) != len(want) {
		t.Fatalf("normalizeAPIKeys() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("normalizeAPIKeys() = %v, want %v", got, want)
		}
	}
	if got := normalizeAPIKeys([]string{"", "  "}); len(got) != 0 {
		t.Fatalf("normalizeAPIKeys(all empty) = %v, want empty", got)
	}
}

func TestSyncAPIKeyFields(t *testing.T) {
	// Pool is the source of truth; apiKey mirrors the first entry.
	cfg := ConfigState{APIKey: "old", APIKeys: []string{"k1", "k2"}}
	syncAPIKeyFields(&cfg)
	if cfg.APIKey != "k1" {
		t.Fatalf("syncAPIKeyFields() apiKey = %q, want k1", cfg.APIKey)
	}
	if len(cfg.APIKeys) != 2 {
		t.Fatalf("syncAPIKeyFields() apiKeys = %v, want [k1 k2]", cfg.APIKeys)
	}

	// Legacy: only apiKey present → pool is constructed from it.
	cfg = ConfigState{APIKey: "legacy"}
	syncAPIKeyFields(&cfg)
	if len(cfg.APIKeys) != 1 || cfg.APIKeys[0] != "legacy" {
		t.Fatalf("syncAPIKeyFields() legacy apiKeys = %v, want [legacy]", cfg.APIKeys)
	}

	// Both empty → pool nil.
	cfg = ConfigState{}
	syncAPIKeyFields(&cfg)
	if cfg.APIKeys != nil || cfg.APIKey != "" {
		t.Fatalf("syncAPIKeyFields() empty = apiKey %q apiKeys %v, want both empty", cfg.APIKey, cfg.APIKeys)
	}

	// ModelConfig variant.
	m := ModelConfig{APIKey: "m-old", APIKeys: []string{"m1", "m2"}}
	syncModelAPIKeyFields(&m)
	if m.APIKey != "m1" || len(m.APIKeys) != 2 {
		t.Fatalf("syncModelAPIKeyFields() = apiKey %q apiKeys %v", m.APIKey, m.APIKeys)
	}
}

func TestResolveKeyPool(t *testing.T) {
	if got := resolveKeyPool(ConfigState{APIKeys: []string{"k1", "k2"}}); len(got) != 2 {
		t.Fatalf("resolveKeyPool(pool) = %v, want [k1 k2]", got)
	}
	if got := resolveKeyPool(ConfigState{APIKey: "legacy"}); len(got) != 1 || got[0] != "legacy" {
		t.Fatalf("resolveKeyPool(legacy) = %v, want [legacy]", got)
	}
	if got := resolveKeyPool(ConfigState{}); got != nil {
		t.Fatalf("resolveKeyPool(empty) = %v, want nil", got)
	}
}

func TestMergeConfigAPIKeyPool(t *testing.T) {
	// Overlay with a fresh pool replaces the base pool entirely.
	base := ConfigState{APIKey: "old", APIKeys: []string{"old", "old2"}}
	overlay := ConfigState{APIKeys: []string{"k1", "k2"}}
	got := mergeConfig(base, overlay)
	if len(got.APIKeys) != 2 || got.APIKeys[0] != "k1" || got.APIKey != "k1" {
		t.Fatalf("mergeConfig(pool) = apiKey %q apiKeys %v, want k1 / [k1 k2]", got.APIKey, got.APIKeys)
	}

	// Overlay that clears the pool (empty non-nil slice) removes all keys.
	overlay = ConfigState{APIKeys: []string{}}
	got = mergeConfig(base, overlay)
	if got.APIKeys != nil || got.APIKey != "" {
		t.Fatalf("mergeConfig(clear) = apiKey %q apiKeys %v, want both empty", got.APIKey, got.APIKeys)
	}

	// Legacy overlay with only apiKey replaces the pool with a single key.
	base = ConfigState{APIKey: "old", APIKeys: []string{"old", "old2"}}
	overlay = ConfigState{APIKey: "legacy-new"}
	got = mergeConfig(base, overlay)
	if len(got.APIKeys) != 1 || got.APIKeys[0] != "legacy-new" {
		t.Fatalf("mergeConfig(legacy) apiKeys = %v, want [legacy-new]", got.APIKeys)
	}

	// Empty overlay keeps the existing pool (zero-value fields preserved).
	base = ConfigState{APIKey: "keep", APIKeys: []string{"keep", "keep2"}}
	got = mergeConfig(base, ConfigState{})
	if len(got.APIKeys) != 2 || got.APIKey != "keep" {
		t.Fatalf("mergeConfig(empty overlay) = apiKey %q apiKeys %v, want keep / [keep keep2]", got.APIKey, got.APIKeys)
	}
}

func TestMergeConfigModelAPIKeyPool(t *testing.T) {
	base := ConfigState{Models: []ModelConfig{{Model: "m", APIKey: "old", APIKeys: []string{"old", "old2"}}}}
	got := mergeConfig(base, ConfigState{})
	if len(got.Models[0].APIKeys) != 2 || got.Models[0].APIKey != "old" {
		t.Fatalf("mergeConfig(model pool) = apiKey %q apiKeys %v, want old / [old old2]", got.Models[0].APIKey, got.Models[0].APIKeys)
	}
	// New pool replaces the old one after normalization.
	overlay := ConfigState{Models: []ModelConfig{{Model: "m", APIKey: "n1", APIKeys: []string{"n1", "n2"}}}}
	got = mergeConfig(base, overlay)
	if len(got.Models[0].APIKeys) != 2 || got.Models[0].APIKeys[0] != "n1" || got.Models[0].APIKey != "n1" {
		t.Fatalf("mergeConfig(model replace) = apiKey %q apiKeys %v", got.Models[0].APIKey, got.Models[0].APIKeys)
	}
}

func TestShouldFailoverKey(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"401 unauthorized", errors.New("authentication failed: invalid api key (401)"), true},
		{"403 forbidden", errors.New("403 Forbidden"), true},
		{"quota exhausted", errors.New("insufficient_quota"), true},
		{"402 payment required", errors.New("payment required (402)"), true},
		{"deepseek insufficient_balance", errors.New("insufficient_balance"), true},
		{"rate limit", errors.New("429 too many requests"), true},
		{"5xx", errors.New("502 Bad Gateway"), true},
		{"network reset", errors.New("connection reset by peer"), true},
		{"plain 400", errors.New("400 Bad Request"), true},
		{"anthropic permission_error", errors.New("permission_error: invalid permissions"), true},
		{"cloudflare access denied", errors.New("error code: 1020 access denied"), true},
		{"openai invalid_request_error", errors.New("invalid_request_error: bad request"), true},
		{"context canceled", context.Canceled, false},
		{"deadline", context.DeadlineExceeded, false},
		{"nil", nil, false},
	}
	for _, tc := range cases {
		if got := shouldFailoverKey(tc.err); got != tc.want {
			t.Errorf("shouldFailoverKey(%s) = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestKeyCooldownSelection exercises the in-memory key selection state:
// default starts at the highest priority key, failure starts a cooldown that
// makes the next request fall through to the next key, and an expired cooldown
// lets the primary key take over again.
func TestKeyCooldownSelection(t *testing.T) {
	a := NewApp()
	keys := []string{"k1", "k2", "k3"}
	cfg := ConfigState{APIFormat: apiFormatOpenAIChat, BaseURL: defaultBaseURL}

	if got := a.firstUsableKeyIndex(cfg, keys); got != 0 {
		t.Fatalf("initial usable index = %d, want 0", got)
	}

	// k1 fails → cooldown starts, next usable is k2.
	a.recordKeyFailure(cfg, "k1", keyTransientCooldownDuration)
	if got := a.firstUsableKeyIndex(cfg, keys); got != 1 {
		t.Fatalf("after failure usable index = %d, want 1", got)
	}
	if !a.isKeyCoolingDown(cfg, "k1") {
		t.Fatal("k1 should be cooling down after failure")
	}
	if a.isKeyCoolingDown(cfg, "k2") {
		t.Fatal("k2 should not be cooling down")
	}

	// k2 also fails → next usable is k3.
	a.recordKeyFailure(cfg, "k2", keyTransientCooldownDuration)
	if got := a.firstUsableKeyIndex(cfg, keys); got != 2 {
		t.Fatalf("after second failure usable index = %d, want 2", got)
	}

	// Expired k1 cooldown lets the primary key take over again.
	a.keyStateMu.Lock()
	a.keyCooldowns[keyCooldownID(cfg, "k1")] = time.Now().Add(-time.Second)
	a.keyStateMu.Unlock()
	if got := a.firstUsableKeyIndex(cfg, keys); got != 0 {
		t.Fatalf("after cooldown expiry usable index = %d, want 0", got)
	}
	if a.isKeyCoolingDown(cfg, "k1") {
		t.Fatal("expired k1 cooldown should be lazily cleaned")
	}

	// All keys cooling down → firstUsableKeyIndex falls back to 0 and the
	// callers loop skips every key (isKeyCoolingDown reports true).
	a.recordKeyFailure(cfg, "k3", keyTransientCooldownDuration)
	a.recordKeyFailure(cfg, "k1", keyTransientCooldownDuration)
	if got := a.firstUsableKeyIndex(cfg, keys); got != 0 {
		t.Fatalf("all cooling usable index = %d, want 0", got)
	}
	for _, k := range keys {
		if !a.isKeyCoolingDown(cfg, k) {
			t.Fatalf("%s should be cooling down", k)
		}
	}
}

// TestKeyCooldownIsolation ensures cooldown state is scoped per endpoint, so
// switching base URLs never leaks cooldown state.
func TestKeyCooldownIsolation(t *testing.T) {
	a := NewApp()
	cfgA := ConfigState{APIFormat: apiFormatOpenAIChat, BaseURL: "https://a.example.com"}
	cfgB := ConfigState{APIFormat: apiFormatOpenAIChat, BaseURL: "https://b.example.com"}
	keys := []string{"k1", "k2"}

	a.recordKeyFailure(cfgA, "k1", keyTransientCooldownDuration)
	if got := a.firstUsableKeyIndex(cfgA, keys); got != 1 {
		t.Fatalf("cfgA usable index = %d, want 1", got)
	}
	if got := a.firstUsableKeyIndex(cfgB, keys); got != 0 {
		t.Fatalf("cfgB usable index = %d, want 0 (isolated)", got)
	}
	if a.isKeyCoolingDown(cfgB, "k1") {
		t.Fatal("cfgB k1 should not be cooling down (isolated)")
	}
}

// TestKeyCooldownDurations verifies auth errors cool down longer than transient
// errors, so an endpoint-wide 5xx storm does not freeze the whole pool for a
// minute.
func TestKeyCooldownDurations(t *testing.T) {
	a := NewApp()
	cfg := ConfigState{APIFormat: apiFormatOpenAIChat, BaseURL: defaultBaseURL}

	a.recordKeyFailure(cfg, "auth-bad", keyAuthCooldownDuration)
	a.recordKeyFailure(cfg, "transient-bad", keyTransientCooldownDuration)

	authUntil := a.keyCooldowns[keyCooldownID(cfg, "auth-bad")]
	transientUntil := a.keyCooldowns[keyCooldownID(cfg, "transient-bad")]
	if transientUntil.Sub(authUntil) > 0 {
		t.Fatalf("transient cooldown %v should be shorter than auth cooldown %v", transientUntil.Sub(time.Now()), authUntil.Sub(time.Now()))
	}
}

// TestEmitLLMRetryEventKeyFields ensures the retry event carries key info.
func TestEmitLLMRetryEventKeyFields(t *testing.T) {
	var got *modelRetryInfo
	onEvent := func(e modelStreamEvent) {
		if e.Retry != nil {
			got = e.Retry
		}
	}
	emitLLMRetryEventForKey(onEvent, 1, 2, errors.New("boom"), 0, 1, 3)
	if got == nil {
		t.Fatal("expected retry event")
	}
	if got.KeyIndex != 1 || got.TotalKeys != 3 || got.Attempt != 1 || got.MaxAttempts != 2 {
		t.Fatalf("retry info = %+v, want keyIndex=1 totalKeys=3 attempt=1 maxAttempts=2", got)
	}
	if !strings.Contains(got.Error, "boom") {
		t.Fatalf("retry error = %q, want to contain boom", got.Error)
	}
}

// TestIsAuthKeyErrorRateLimitNotAuth 确保 429/限流类错误(即使文案含
// quota,如阿里云 429 token-limit)不被误判为认证/计费类错误——否则多 key
// 池会被 30 分钟冷却整体冻结,后续请求全部立刻失败,只能重启恢复。
func TestIsAuthKeyErrorRateLimitNotAuth(t *testing.T) {
	// 阿里云 429 token-limit:与 OpenAI 计费文案同形,实为分钟级限流。
	aliyun429 := `error, status code: 429, status: 429 Too Many Requests, message: You exceeded your current quota, please check your plan and billing details. For details, see https://help.aliyun.com/zh/model-studio/error-code#token-limit`
	if isAuthKeyError(errors.New(aliyun429)) {
		t.Fatal("aliyun 429 token-limit should be transient, not auth")
	}
	if isAuthKeyError(errors.New("status: 429 Too Many Requests")) {
		t.Fatal("plain 429 should be transient, not auth")
	}
	if isAuthKeyError(errors.New("rate limit exceeded")) {
		t.Fatal("rate limit should be transient, not auth")
	}
	// 明确的计费类标记仍视为 key 级故障。
	for _, billing := range []string{
		"status code: 402, message: payment required",
		"insufficient_quota",
		"insufficient_balance",
	} {
		if !isAuthKeyError(errors.New(billing)) {
			t.Fatalf("%q should be auth/billing", billing)
		}
	}
	// 认证类错误不受影响。
	for _, auth := range []string{
		"status code: 401, message: invalid api key",
		"status code: 403, message: forbidden",
	} {
		if !isAuthKeyError(errors.New(auth)) {
			t.Fatalf("%q should be auth", auth)
		}
	}
}
