// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package app

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCorruptConfigIsQuarantined: an unparseable config used to be left in place,
// so the app kept running on defaults and the next save wrote those defaults back
// over the file — the user's settings were gone for good. It must be moved aside
// instead, with the original bytes intact.
func TestCorruptConfigIsQuarantined(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	dir := filepath.Join(home, ".ally_agent")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.json")
	corrupt := []byte(`{"workspace": "/tmp/x",`) // truncated on purpose
	if err := os.WriteFile(path, corrupt, 0o600); err != nil {
		t.Fatal(err)
	}

	app := NewApp()
	if err := app.ensureInitialized(); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("the corrupt config must be moved out of the way, stat err = %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	quarantined := ""
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "config.json.corrupt-") {
			quarantined = entry.Name()
		}
	}
	if quarantined == "" {
		t.Fatal("the corrupt config must be kept as config.json.corrupt-<timestamp>")
	}
	got, err := os.ReadFile(filepath.Join(dir, quarantined))
	if err != nil || string(got) != string(corrupt) {
		t.Fatalf("the quarantined copy must keep the original bytes: %q (err=%v)", got, err)
	}
	if want := defaultConfigState(); app.config.Workspace != want.Workspace {
		t.Fatalf("a corrupt config must fall back to defaults, got workspace %q", app.config.Workspace)
	}
}

// TestConfigLoadKeepsHealthyFile: the quarantine path must not fire on a valid
// config — that would silently reset a working installation.
func TestConfigLoadKeepsHealthyFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	dir := filepath.Join(home, ".ally_agent")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.json")
	healthy := []byte("{\n  \"customPrompt\": \"keep me\"\n}\n")
	if err := os.WriteFile(path, healthy, 0o600); err != nil {
		t.Fatal(err)
	}

	app := NewApp()
	if err := app.ensureInitialized(); err != nil {
		t.Fatal(err)
	}
	if app.config.CustomPrompt != "keep me" {
		t.Fatalf("a healthy config must be loaded, got customPrompt %q", app.config.CustomPrompt)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != string(healthy) {
		t.Fatalf("a healthy config must stay untouched: %q (err=%v)", got, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), ".corrupt-") {
			t.Fatalf("no file may be quarantined on a healthy load: %s", entry.Name())
		}
	}
}

// TestUnreadableConfigDecision: only unparseable content may be moved aside. A
// transient read error (EACCES, EMFILE) says nothing about the content, and
// quarantining then would silently degrade a healthy config to defaults.
func TestUnreadableConfigDecision(t *testing.T) {
	dir := t.TempDir()
	healthy := filepath.Join(dir, "config.json")
	body := []byte("{\n  \"customPrompt\": \"keep me\"\n}\n")
	if err := os.WriteFile(healthy, body, 0o600); err != nil {
		t.Fatal(err)
	}
	handleUnreadableConfig(healthy, os.ErrPermission)
	if got, err := os.ReadFile(healthy); err != nil || string(got) != string(body) {
		t.Fatalf("a parsable config must stay in place: %q (err=%v)", got, err)
	}

	broken := filepath.Join(dir, "broken.json")
	if err := os.WriteFile(broken, []byte(`{"workspace": "/tmp/x",`), 0o600); err != nil {
		t.Fatal(err)
	}
	handleUnreadableConfig(broken, errors.New("unexpected end of JSON input"))
	if _, err := os.Stat(broken); !os.IsNotExist(err) {
		t.Fatalf("unparseable content must be moved aside, stat err = %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	kept := false
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "broken.json.corrupt-") {
			kept = true
		}
	}
	if !kept {
		t.Fatal("the invalid file must be kept as broken.json.corrupt-<timestamp>")
	}
}

// TestAllowPrivateNetworkSurvivesReload: the SSRF guard switch is persisted, so a
// user who turned it off must still be off after a restart. mergeConfig does not
// carry the field (a request-level overlay must never widen it), so the load path
// has to adopt the disk value explicitly — otherwise the permissive default
// silently wins on every start.
func TestAllowPrivateNetworkSurvivesReload(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	dir := filepath.Join(home, ".ally_agent")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"allowPrivateNetwork": false}`), 0o600); err != nil {
		t.Fatal(err)
	}

	app := NewApp()
	if err := app.ensureInitialized(); err != nil {
		t.Fatal(err)
	}
	if app.config.allowPrivateNetworkEnabled() {
		t.Fatal("a persisted allowPrivateNetwork=false must survive the load")
	}

	// A legacy config without the field keeps the permissive default instead of
	// being read as "off".
	if err := os.WriteFile(path, []byte(`{"workspace": "/tmp/x"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	legacy := NewApp()
	if err := legacy.ensureInitialized(); err != nil {
		t.Fatal(err)
	}
	if !legacy.config.allowPrivateNetworkEnabled() {
		t.Fatal("a legacy config without the field must keep the permissive default")
	}
}

// TestLegacyTopLevelModelFieldsMigrateToIdentity: 旧版 config.json 把"当前模型"存在
// 顶层字段里（只能由 /api/v1/models/activate 写入）。加载时它必须被收敛成
// lastUsedModel 身份，字段本身不留在内存也不再落盘。
func TestLegacyTopLevelModelFieldsMigrateToIdentity(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	dir := filepath.Join(home, ".ally_agent")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.json")
	legacy := `{
  "providerName": "Relay",
  "apiFormat": "openai_chat",
  "baseUrl": "http://127.0.0.1:8080/v1",
  "model": "relay-model",
  "apiKeys": ["sk-legacy"],
  "models": [{"providerName": "Relay", "model": "relay-model", "apiFormat": "openai_chat", "baseUrl": "http://127.0.0.1:8080/v1", "apiKeys": ["sk-legacy"]}]
}`
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}

	app := NewApp()
	if err := app.ensureInitialized(); err != nil {
		t.Fatal(err)
	}
	if app.config.LastUsedModel == nil || app.config.LastUsedModel.Model != "relay-model" {
		t.Fatalf("legacy top-level model must migrate into an identity, got %#v", app.config.LastUsedModel)
	}
	if app.config.Model != "" || app.config.BaseURL != "" || len(app.config.APIKeys) != 0 {
		t.Fatalf("derived model fields must not stay on the loaded config, got model=%q baseUrl=%q keys=%v", app.config.Model, app.config.BaseURL, app.config.APIKeys)
	}

	// 未初始化前先落一次盘：新形状必须只留 models[] + lastUsedModel。
	if err := app.saveConfig(app.config); err != nil {
		t.Fatal(err)
	}
	rewritten, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var persisted map[string]any
	if err := json.Unmarshal(rewritten, &persisted); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"providerName", "apiFormat", "baseUrl", "model", "apiKeys"} {
		if _, ok := persisted[key]; ok {
			t.Fatalf("rewritten config must not carry the top-level %q field: %s", key, rewritten)
		}
	}
	if _, ok := persisted["lastUsedModel"]; !ok {
		t.Fatalf("rewritten config must carry the last-used identity: %s", rewritten)
	}

	// 文件里带的是旧地址，但内存里已经清空：生效配置按身份从 models[] 展开。
	cfg := app.effectiveConfig(ConfigState{})
	if cfg.Model != "relay-model" || cfg.BaseURL != "http://127.0.0.1:8080/v1" {
		t.Fatalf("effective config must expand the identity, got model=%q baseUrl=%q", cfg.Model, cfg.BaseURL)
	}
}

// TestLegacyTopLevelModelFieldsMaterializePreset: 旧版 config.json 的 models[] 可能是
// 空的（初始版本根本没有这个数组），顶层字段就是用户唯一的模型与密钥。加载时必须把
// 它们物化成一条 preset 并指向它——stripModelFields 清空、随后任意一次保存以新形状
// 落盘，不物化就等于把模型、地址与密钥（含备用 key）一起静默抹掉。
func TestLegacyTopLevelModelFieldsMaterializePreset(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	dir := filepath.Join(home, ".ally_agent")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.json")
	legacy := `{
  "providerName": "Relay",
  "apiFormat": "openai_chat",
  "baseUrl": "http://127.0.0.1:8080/v1",
  "model": "relay-model",
  "apiKey": "sk-legacy",
  "apiKeys": ["sk-legacy", "sk-backup"],
  "maxTokens": 8192,
  "contextWindow": 128000
}`
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}

	app := NewApp()
	if err := app.ensureInitialized(); err != nil {
		t.Fatal(err)
	}
	if len(app.config.Models) != 1 {
		t.Fatalf("legacy top-level fields must materialize one preset, got %#v", app.config.Models)
	}
	entry := app.config.Models[0]
	if entry.Model != "relay-model" || entry.BaseURL != "http://127.0.0.1:8080/v1" || entry.MaxTokens != 8192 || entry.ContextWindow != 128000 {
		t.Fatalf("materialized preset must keep the legacy fields, got %#v", entry)
	}
	if len(entry.APIKeys) != 2 || entry.APIKey != "sk-legacy" {
		t.Fatalf("materialized preset must keep the key pool, got %#v", entry)
	}
	if app.config.LastUsedModel == nil || app.config.LastUsedModel.Model != "relay-model" {
		t.Fatalf("the materialized preset must become the last-used model, got %#v", app.config.LastUsedModel)
	}
	// 旧版就是靠这些字段跑通的，迁移后必须一样能用。
	cfg := app.effectiveConfig(ConfigState{})
	if cfg.Model != "relay-model" || cfg.BaseURL != "http://127.0.0.1:8080/v1" || len(resolveKeyPool(cfg)) != 2 {
		t.Fatalf("effective config must expand the migrated preset, got model=%q baseUrl=%q keys=%v", cfg.Model, cfg.BaseURL, resolveKeyPool(cfg))
	}
	// 落盘再重载：迁移结果必须活过重启。
	if err := app.saveConfig(app.config); err != nil {
		t.Fatal(err)
	}
	reloaded := NewApp()
	if err := reloaded.ensureInitialized(); err != nil {
		t.Fatal(err)
	}
	if reloaded.config.LastUsedModel == nil || reloaded.config.LastUsedModel.Model != "relay-model" {
		t.Fatalf("the migrated identity must survive a reload, got %#v", reloaded.config.LastUsedModel)
	}
	if got := reloaded.effectiveConfig(ConfigState{}); got.Model != "relay-model" || len(resolveKeyPool(got)) != 2 {
		t.Fatalf("the migrated preset must survive a reload, got model=%q keys=%v", got.Model, resolveKeyPool(got))
	}
}

// TestLegacyTopLevelModelFieldsWithoutKeyAreDropped: 顶层字段没有可用密钥时不做物化。
// 没有密钥的旧配置本身就发不出请求，凭空多一条空密钥的 preset 只会污染模型列表。
func TestLegacyTopLevelModelFieldsWithoutKeyAreDropped(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	dir := filepath.Join(home, ".ally_agent")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.json")
	legacy := `{"providerName":"OpenAI Compatible","apiFormat":"openai_chat","baseUrl":"https://api.deepseek.com","model":"deepseek-v4-flash","apiKey":""}`
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}

	app := NewApp()
	if err := app.ensureInitialized(); err != nil {
		t.Fatal(err)
	}
	if len(app.config.Models) != 0 || app.config.LastUsedModel != nil {
		t.Fatalf("a keyless legacy config must not materialize a preset, got models=%#v identity=%#v", app.config.Models, app.config.LastUsedModel)
	}
}

// TestLastUsedModelSurvivesReload: "最近使用模型"身份是持久化配置的一部分，但
// mergeConfig 不携带它（请求级 overlay 不得改写它），所以加载路径必须显式采纳磁盘值。
// 否则每次启动身份都被清成 nil：界面只能回落到"使用频率最高"的模型，用户选的模型重启即丢。
func TestLastUsedModelSurvivesReload(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	dir := filepath.Join(home, ".ally_agent")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.json")
	saved := `{
  "models": [
    {"providerName": "Relay", "model": "relay-a", "apiFormat": "openai_chat", "baseUrl": "http://127.0.0.1:8080/v1", "apiKeys": ["sk-a"]},
    {"providerName": "Relay", "model": "relay-b", "apiFormat": "openai_chat", "baseUrl": "http://127.0.0.1:8080/v1", "apiKeys": ["sk-b"]}
  ],
  "lastUsedModel": {"providerName": "Relay", "model": "relay-b"}
}`
	if err := os.WriteFile(path, []byte(saved), 0o600); err != nil {
		t.Fatal(err)
	}

	app := NewApp()
	if err := app.ensureInitialized(); err != nil {
		t.Fatal(err)
	}
	if app.config.LastUsedModel == nil || app.config.LastUsedModel.Model != "relay-b" {
		t.Fatalf("the persisted last-used identity must survive the load, got %#v", app.config.LastUsedModel)
	}
	if cfg := app.effectiveConfig(ConfigState{}); cfg.Model != "relay-b" || cfg.BaseURL != "http://127.0.0.1:8080/v1" {
		t.Fatalf("effective config must expand the reloaded identity, got model=%q baseUrl=%q", cfg.Model, cfg.BaseURL)
	}
}

// TestGitHubTokenSurvivesReloadAndReachesUpdate: token 是唯一不经 UI、只能手改 config.json
// （或旧版写盘）设置的凭据。加载路径不采纳它，内存里就是空的，GetConfig 把空值回给前端，
// 下一次保存再把空值写回磁盘——用户的 token 被永久抹掉；即使采纳了，mergeConfig 不带
// 空 overlay 也会让它传不进更新链路（fetchReleaseByTag/DownloadUpdate 走 effectiveConfigSafe）。
func TestGitHubTokenSurvivesReloadAndReachesUpdate(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	dir := filepath.Join(home, ".ally_agent")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"githubToken": "ghp_secret", "workspace": "/tmp/x"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	app := NewApp()
	if err := app.ensureInitialized(); err != nil {
		t.Fatal(err)
	}
	if app.config.GitHubToken != "ghp_secret" {
		t.Fatalf("the persisted token must survive the load, got %q", app.config.GitHubToken)
	}
	if got := app.effectiveConfigSafe().GitHubToken; got != "ghp_secret" {
		t.Fatalf("the update path must see the configured token, got %q", got)
	}
}

// TestSaveConfigConvergesDanglingLastUsedModel: 设置页整份保存（SaveConfig）也必须把在
// models[] 里解析不到的"最近使用模型"身份清掉。界面在模型页删掉或改名那条模型后整份
// 保存，身份就成了悬空值：它展开永远落空（HTTP API 会话、计划任务回落会直接报
// "model is required"），而界面因为有 Tab 快照、下次切模型/发送又会重写身份，看不出问题。
// 两个写入方（局部保存的 saveConfig 与整份保存的 SaveConfig）共用 convergeLastUsedModel，
// 只在一边清理就等于没清理。
func TestSaveConfigConvergesDanglingLastUsedModel(t *testing.T) {
	cases := map[string][]ModelConfig{
		// 删掉那条模型
		"deleted": {{ProviderName: "Relay", Model: "other"}},
		// 改了那条模型的 id
		"renamed": {{ProviderName: "Relay", Model: "relay-model-2"}},
	}
	for name, remaining := range cases {
		t.Run(name, func(t *testing.T) {
			app := NewApp()
			app.initialized = true
			path := filepath.Join(t.TempDir(), "config.json")
			app.configPath = path
			app.config = ConfigState{
				Models: []ModelConfig{
					{ProviderName: "Relay", Model: "relay-model", APIFormat: apiFormatOpenAIChat, BaseURL: "http://127.0.0.1:8080/v1", APIKeys: []string{"k1"}},
					{ProviderName: "Relay", Model: "other", APIFormat: apiFormatOpenAIChat, BaseURL: "http://127.0.0.1:8080/v1", APIKeys: []string{"k2"}},
				},
				LastUsedModel: &ModelIdentity{ProviderName: "Relay", Model: "relay-model"},
			}
			// 界面模型页提交的整份草稿：models 里已经没有那条模型，身份还是旧值。
			req := app.config
			req.Models = remaining
			if err := app.SaveConfig(req); err != nil {
				t.Fatal(err)
			}
			if app.config.LastUsedModel != nil {
				t.Fatalf("dangling identity must be converged away, got %#v", app.config.LastUsedModel)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var persisted map[string]any
			if err := json.Unmarshal(data, &persisted); err != nil {
				t.Fatal(err)
			}
			if _, ok := persisted["lastUsedModel"]; ok {
				t.Fatalf("dangling identity must not reach disk: %s", data)
			}
			// 落空 = 没有模型，调用方据此报 "model is required"，而不是拿到别的模型。
			if cfg := app.effectiveConfig(ConfigState{}); cfg.Model != "" {
				t.Fatalf("a dangling identity must not expand anything, got %q", cfg.Model)
			}
		})
	}
}
