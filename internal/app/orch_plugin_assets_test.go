// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package app

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ally-dev/internal/tools/plugin"
)

const pluginTestManifest = `{
  "id": "demo-plugin",
  "name": "Demo",
  "version": "1.0.0",
  "entry": "index.js",
  "menu": [{"key": "demo", "title": "Demo", "icon": "ApiOutlined"}],
  "permissions": {"http": ["example.com"]}
}`

// nextHandler 代表「不是插件资源」时后续的资源处理器：用 204 标记请求被放行过去。
func nextHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
}

func installTestPlugin(t *testing.T) {
	t.Helper()
	dir := filepath.Join(plugin.PluginRoot(appDataDir()), "demo-plugin")
	if err := os.MkdirAll(filepath.Join(dir, "assets"), 0o700); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		plugin.ManifestName: pluginTestManifest,
		"index.js":          "export function mount() {}",
		"assets/logo.svg":   "<svg/>",
		"payload.bin":       "\x00\x01",
		// 插件里真叫 `@xxx/` 的目录：宿主加的缓存戳不能被剥到它头上（见下一个用例）。
		"@literal/probe.js": "literal wins",
	} {
		full := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPluginAssetMiddlewareServesOnlyPluginFiles(t *testing.T) {
	redirectAppStateDir(t)
	installTestPlugin(t)

	app := &App{}
	if disabled := app.disabledPluginSet(); len(disabled) != 0 {
		t.Fatalf("新装的插件不应处于禁用态：%v", disabled)
	}
	handler := PluginAssetMiddleware(app)(nextHandler())

	cases := []struct {
		name        string
		method      string
		urlPath     string
		wantStatus  int
		wantType    string
		wantBodyHas string
	}{
		{"入口 js", http.MethodGet, "/plugins/demo-plugin/index.js", http.StatusOK, "text/javascript", "export function mount"},
		{"子目录资源", http.MethodGet, "/plugins/demo-plugin/assets/logo.svg", http.StatusOK, "image/svg+xml", "<svg"},
		// 宿主在 URL 目录段上加的缓存戳（见 pluginHost.mjs / plugin.StripCacheStamp）：
		// 入口与子资源都要能穿透它取到同一个文件。
		{"带缓存戳的入口 js", http.MethodGet, "/plugins/demo-plugin/@20261008T163100Z/index.js", http.StatusOK, "text/javascript", "export function mount"},
		{"带缓存戳的子目录资源", http.MethodGet, "/plugins/demo-plugin/@20261008T163100Z/assets/logo.svg", http.StatusOK, "image/svg+xml", "<svg"},
		{"插件自己的 @ 目录优先于剥戳", http.MethodGet, "/plugins/demo-plugin/@literal/probe.js", http.StatusOK, "text/javascript", "literal wins"},
		{"HEAD 也支持", http.MethodHead, "/plugins/demo-plugin/index.js", http.StatusOK, "text/javascript", ""},
		{"文件不存在", http.MethodGet, "/plugins/demo-plugin/missing.js", http.StatusNotFound, "", ""},
		{"扩展名不在白名单", http.MethodGet, "/plugins/demo-plugin/payload.bin", http.StatusForbidden, "", ""},
		{"目录不当作资源", http.MethodGet, "/plugins/demo-plugin/assets", http.StatusNotFound, "", ""},
		{"目录不当作资源（带斜杠）", http.MethodGet, "/plugins/demo-plugin/assets/", http.StatusNotFound, "", ""},
		{"非插件路径交给下一个处理器", http.MethodGet, "/assets/app.js", http.StatusNoContent, "", ""},
		{"页面本身交给下一个处理器", http.MethodGet, "/", http.StatusNoContent, "", ""},
		{"POST 不处理", http.MethodPost, "/plugins/demo-plugin/index.js", http.StatusNoContent, "", ""},
		{"非法 id 交给下一个处理器", http.MethodGet, "/plugins/../index.js", http.StatusNoContent, "", ""},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, httptest.NewRequest(testCase.method, testCase.urlPath, nil))
			if recorder.Code != testCase.wantStatus {
				t.Fatalf("%s %s → %d, want %d（body=%q）", testCase.method, testCase.urlPath, recorder.Code, testCase.wantStatus, recorder.Body.String())
			}
			if testCase.wantType != "" {
				if got := recorder.Header().Get("Content-Type"); !strings.Contains(got, testCase.wantType) {
					t.Fatalf("Content-Type = %q, want 包含 %q", got, testCase.wantType)
				}
			}
			if testCase.wantBodyHas != "" && !strings.Contains(recorder.Body.String(), testCase.wantBodyHas) {
				t.Fatalf("body = %q, 期望包含 %q", recorder.Body.String(), testCase.wantBodyHas)
			}
		})
	}
}

// 禁用的插件连资源都不可达：页面入口都没了，资源当然也不该能拿。
func TestPluginAssetMiddlewareHidesDisabledPlugins(t *testing.T) {
	redirectAppStateDir(t)
	installTestPlugin(t)

	app := &App{config: ConfigState{DisabledPlugins: []string{"demo-plugin"}}}
	handler := PluginAssetMiddleware(app)(nextHandler())

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/plugins/demo-plugin/index.js", nil))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("禁用插件的资源应 404，实际 %d", recorder.Code)
	}
}
