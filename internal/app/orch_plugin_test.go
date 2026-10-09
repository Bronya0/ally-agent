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
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"ally-dev/internal/tools/plugin"
)

// 插件 HTTP 代理的目标判定是这一块唯一的安全闸门：名单内的放行（含内网地址），
// 名单外的一律拒绝。这里全部用字面量主机，避免任何 DNS 依赖。
func TestValidateHTTPTargetAccessUsesPluginAllowlist(t *testing.T) {
	allowlist := []string{"jira.corp.com", "*.corp.cn"}
	cases := []struct {
		name    string
		rawURL  string
		wantErr bool
	}{
		{"精确主机放行", "https://jira.corp.com/rest/api/2/issue/1", false},
		{"未声明的内网地址拒绝", "http://10.20.30.40/rest/api/2/issue", true},
		{"通配子域放行", "https://api.corp.cn/x", false},
		{"裸域放行", "https://corp.cn/x", false},
		{"名单外主机拒绝", "https://evil.com/x", true},
		{"近似域名不算命中", "https://jira.corp.com.evil.com/x", true},
		{"未声明的内网地址拒绝", "http://192.168.1.10/api", true},
		{"非 http(s) 协议拒绝", "file:///etc/passwd", true},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			target, err := url.Parse(testCase.rawURL)
			if err != nil {
				t.Fatalf("parse %s: %v", testCase.rawURL, err)
			}
			// allowPrivate=true：传输层不拦私网（真实调用里也是这么传的），
			// 于是这里检验的就是白名单本身的判定。
			got := validateHTTPTargetAccess(target, true, ConfigState{}, allowlist)
			if testCase.wantErr && got == nil {
				t.Fatalf("%s 必须被拒", testCase.rawURL)
			}
			if !testCase.wantErr && got != nil {
				t.Fatalf("%s 应放行，却报：%v", testCase.rawURL, got)
			}
		})
	}

	// 声明了内网地址（IP 字面量）时也要放行：公司 Jira 常常就是一个内网 IP。
	ipURL, err := url.Parse("http://10.20.30.40/rest/api/2/issue")
	if err != nil {
		t.Fatal(err)
	}
	if err := validateHTTPTargetAccess(ipURL, true, ConfigState{}, []string{"10.20.30.40"}); err != nil {
		t.Fatalf("声明的内网 IP 被报：%v", err)
	}

	// 拒绝时的错误码要稳定：前端按它把「被拒请求」显示给用户。
	denied, _ := url.Parse("http://192.168.1.10/api")
	err = validateHTTPTargetAccess(denied, true, ConfigState{}, allowlist)
	if err == nil {
		t.Fatal("期望被拒")
	}
	if code := toolErrorCode(err); code != "E_PLUGIN_HOST_DENIED" {
		t.Fatalf("错误码 = %q, want E_PLUGIN_HOST_DENIED", code)
	}
}

// 空白名单 = 工具侧请求，必须完全走原有 SSRF 判定（allowPrivate=true 时不查私网，
// 行为与加这个函数之前一致）。
func TestValidateHTTPTargetAccessFallsBackForToolRequests(t *testing.T) {
	target, err := url.Parse("https://example.com/x")
	if err != nil {
		t.Fatal(err)
	}
	if err := validateHTTPTargetAccess(target, true, ConfigState{}, nil); err != nil {
		t.Fatalf("工具侧请求被误拒：%v", err)
	}
	badScheme, err := url.Parse("ftp://example.com")
	if err != nil {
		t.Fatal(err)
	}
	if err := validateHTTPTargetAccess(badScheme, true, ConfigState{}, nil); err == nil {
		t.Fatal("非 http(s) 协议必须被拒")
	}
}

// 重定向后的目标也要复判白名单：首包放行 → 302 到别的主机是白名单最容易被绕的路。
// 这里用一个只做跳转的本机服务钉住它（服务本身就是 127.0.0.1，命中声明）。
func TestPluginHTTPRedirectTargetIsRechecked(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://not-declared.invalid/next", http.StatusFound)
	}))
	defer server.Close()

	app := NewApp()
	_, err := app.doHTTPRequest(context.Background(), ConfigState{}, HTTPRequestToolRequest{
		URL:          server.URL,
		AllowedHosts: []string{"127.0.0.1"},
	}, false, true)
	if err == nil {
		t.Fatal("跳转到未声明主机必须被拒")
	}
	if code := toolErrorCode(err); code != "E_PLUGIN_HOST_DENIED" {
		t.Fatalf("错误码 = %q, want E_PLUGIN_HOST_DENIED（err=%v）", code, err)
	}
}

func TestSplitPluginAssetPath(t *testing.T) {
	cases := []struct {
		urlPath string
		id      string
		rel     string
		ok      bool
	}{
		{"/plugins/jira-helper/index.js", "jira-helper", "index.js", true},
		{"/plugins/JIRA-helper/sub/app.mjs", "jira-helper", "sub/app.mjs", true},
		{"plugins/jira-helper/index.js", "jira-helper", "index.js", true},
		{"/plugins/jira-helper/assets/a.css", "jira-helper", "assets/a.css", true},
		{"/assets/app.js", "", "", false},
		{"/plugins/jira-helper/", "", "", false},
		{"/plugins/jira-helper", "", "", false},
		{"/plugins//index.js", "", "", false},
		{"/plugins/../secret.txt", "", "", false},
		{"/plugins/bad id/index.js", "", "", false},
	}
	for _, testCase := range cases {
		id, rel, ok := splitPluginAssetPath(testCase.urlPath)
		if ok != testCase.ok || id != testCase.id || rel != testCase.rel {
			t.Errorf("splitPluginAssetPath(%q) = (%q, %q, %v), want (%q, %q, %v)",
				testCase.urlPath, id, rel, ok, testCase.id, testCase.rel, testCase.ok)
		}
	}
}

// 身份比较必须两边用同一把尺子：手工拷进来的目录名可能带大写，禁用/删除要按**盘上原样
// 名字**找目录、按归一后的键比较配置。只做一半就会「开关按下去自己弹回来」、「删除说
// 没安装」。
func TestPluginIdentityIgnoresDirCase(t *testing.T) {
	redirectAppStateDir(t)
	app := NewApp()
	if err := app.ensureInitialized(); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(plugin.PluginRoot(appDataDir()), "Foo-Broken")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, plugin.ManifestName), []byte("{ not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := app.SetPluginEnabled("Foo-Broken", false); err != nil {
		t.Fatalf("SetPluginEnabled: %v", err)
	}
	list, err := app.ListPlugins()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Enabled {
		t.Fatalf("禁用状态没体现在列表上：%+v", list)
	}

	if err := app.DeletePlugin(list[0].ID, true); err != nil {
		t.Fatalf("DeletePlugin: %v", err)
	}
	if _, statErr := os.Stat(dir); !os.IsNotExist(statErr) {
		t.Fatal("目录没被删掉")
	}
	app.mu.Lock()
	disabledPlugins := append([]string(nil), app.config.DisabledPlugins...)
	app.mu.Unlock()
	for _, p := range disabledPlugins {
		if plugin.IdentityKey(p) == plugin.IdentityKey(list[0].ID) {
			t.Fatalf("DeletePlugin did not remove plugin from DisabledPlugins: %v", disabledPlugins)
		}
	}
}
