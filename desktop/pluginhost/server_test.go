// SPDX-License-Identifier: GPL-3.0-only
package pluginhost

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	backend "ally-dev/internal/app"
)

func pluginTestHost(t *testing.T) (*Host, backend.PluginInfo, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("ALLY_DATA_DIR", t.TempDir())
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "hello.txt"), []byte("plugin workspace"), 0600); err != nil {
		t.Fatal(err)
	}
	a := New(backend.NewApp(), nil, nil)
	t.Cleanup(func() { _ = a.core.ShutdownNative() })
	info, err := a.core.ImportPluginFromDir(filepath.Join("..", "..", "internal", "tools", "plugin", "testdata", "demo-plugin"))
	if err != nil {
		t.Fatal(err)
	}
	return a, info, workspace
}
func pluginTestRPC(t *testing.T, p *page, method string, args ...any) (int, string) {
	t.Helper()
	data, _ := json.Marshal(map[string]any{"method": method, "args": args})
	r, _ := http.NewRequest("POST", p.url+"/rpc", bytes.NewReader(data))
	r.Header.Set("Origin", p.url)
	r.Header.Set("X-Ally-Plugin", p.token)
	response, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	return response.StatusCode, string(body)
}
func TestPluginWebServerCompatibilityAndIdentity(t *testing.T) {
	a, info, workspace := pluginTestHost(t)
	p, err := a.newServer(info, workspace, NewTheme(Palette{Name: "test", Dark: true}))
	if err != nil {
		t.Fatal(err)
	}
	defer p.server.Close()
	// A caller cannot change plugin or workspace identity by altering RPC args.
	if code, body := pluginTestRPC(t, p, "PluginStoreSet", "another-plugin", `{"legacy":true}`); code != 200 {
		t.Fatal(code, body)
	}
	if code, body := pluginTestRPC(t, p, "PluginStoreGet", "another-plugin"); code != 200 || !strings.Contains(body, "legacy") {
		t.Fatal(code, body)
	}
	if code, body := pluginTestRPC(t, p, "ReadWorkspaceFileAt", map[string]string{"workspace": t.TempDir(), "path": "hello.txt"}); code != 200 || !strings.Contains(body, "plugin workspace") {
		t.Fatal(code, body)
	}
	for _, path := range []string{"../secret", "a/../secret", `C:\secret`, `\\server\share`, "/etc/passwd", "a\\..\\secret"} {
		if code, body := pluginTestRPC(t, p, "ReadWorkspaceFileAt", map[string]string{"path": path}); code != 400 {
			t.Fatalf("%q: %d %s", path, code, body)
		}
	}
	if code, body := pluginTestRPC(t, p, "PluginHTTPRequest", map[string]string{"pluginId": "another-plugin", "url": "https://not-declared.invalid"}); code != 400 || !strings.Contains(body, "E_PLUGIN_HOST_DENIED") {
		t.Fatal(code, body)
	}
	for _, path := range []string{"/", "/pluginHost.mjs", "/plugins/" + info.ID + "/@test/index.js", "/plugins/" + info.ID + "/@test/style.css"} {
		response, err := http.Get(p.url + path)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != 200 {
			t.Fatal(path, response.StatusCode)
		}
	}
	for _, path := range []string{"/plugins/another-plugin/index.js", "/plugins/" + info.ID + "/../another-plugin/index.js"} {
		response, err := http.Get(p.url + path)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode == 200 {
			t.Fatal("other plugin served", path)
		}
	}
	req, _ := http.NewRequest("POST", p.url+"/rpc", strings.NewReader(`{"method":"PluginStoreGet"}`))
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 403 {
		t.Fatal("untrusted RPC accepted")
	}
	req, _ = http.NewRequest("GET", p.url+"/config.js", nil)
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	response, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 403 {
		t.Fatal("cross-site config accepted")
	}
	p.info.Permissions.Workspace = "none"
	if code, body := pluginTestRPC(t, p, "ReadWorkspaceFileAt", map[string]string{"path": "hello.txt"}); code != 400 {
		t.Fatal(code, body)
	}
}
func TestPluginWebEventsAndServerRelease(t *testing.T) {
	a, info, workspace := pluginTestHost(t)
	p, err := a.newServer(info, workspace, NewTheme(Palette{Name: "test", Dark: true}))
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.Get(p.url + "/events?token=" + p.token)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	buf := make([]byte, 4096)
	if _, err = response.Body.Read(buf); err != nil {
		t.Fatal(err)
	}
	state := NewTheme(Palette{Name: "test", Dark: true})
	state.Active = true
	p.setState(state)
	n, err := response.Body.Read(buf)
	if err != nil || !strings.Contains(string(buf[:n]), `"active":true`) {
		t.Fatal(string(buf[:n]), err)
	}
	a.pages = map[string]*page{info.ID: p}
	a.Close()
	if len(a.pages) != 0 {
		t.Fatal("page retained")
	}
	client := http.Client{Timeout: time.Second}
	if response, err := client.Get(p.url); err == nil {
		response.Body.Close()
		t.Fatal("listener survived release")
	}
	if _, err = response.Body.Read(buf); err == nil {
		t.Fatal("event stream survived release")
	}
}

func TestPluginWebCacheInvalidation(t *testing.T) {
	a, installed, workspace := pluginTestHost(t)
	input := Input{Page: "chat", Workspace: workspace, Theme: NewTheme(Palette{Name: "test", Dark: true})}
	for _, name := range []string{"keep-alive", "destroy-on-hide", "disabled", "deleted", "updated", "workspace-changed"} {
		t.Run(name, func(t *testing.T) {
			a := New(a.core, nil, nil)
			info := installed
			info.KeepAlive = name != "destroy-on-hide"
			state := NewTheme(Palette{Name: "test", Dark: true})
			state.Active = true
			p, err := a.newServer(info, workspace, state)
			if err != nil {
				t.Fatal(err)
			}
			defer p.server.Close()
			input.Plugins = []backend.PluginInfo{info}
			input.Workspace = workspace
			switch name {
			case "disabled":
				input.Plugins[0].Enabled = false
			case "deleted":
				input.Plugins = nil
			case "updated":
				input.Plugins[0].UpdatedAt = "new-version"
			case "workspace-changed":
				input.Workspace = t.TempDir()
			}
			a.pages = map[string]*page{info.ID: p}
			a.sync(input)
			retained := a.pages[info.ID] != nil
			if retained != (name == "keep-alive") {
				t.Fatalf("retained=%v", retained)
			}
			p.mu.Lock()
			active, closed := p.state.Active, p.closed
			p.mu.Unlock()
			if retained && (active || closed) {
				t.Fatal("hidden cache state incorrect")
			}
			if !retained && !closed {
				t.Fatal("discarded page kept resources")
			}
			a.Close()
		})
	}
}

// Runs the real historical demo module, its relative CSS, store, HTTP denial,
// workspace reads and unmount in Chromium. No production source is modified.
func TestPluginLegacyBrowser(t *testing.T) {
	browser := os.Getenv("ALLY_PLUGIN_BROWSER")
	if browser == "" {
		t.Skip("set ALLY_PLUGIN_BROWSER to a Chromium executable")
	}
	a, info, workspace := pluginTestHost(t)
	source := t.TempDir()
	for _, name := range []string{"plugin.json", "index.js", "style.css"} {
		data, err := os.ReadFile(filepath.Join(info.Dir, name))
		if err != nil {
			t.Fatal(err)
		}
		target := name
		if name == "index.js" {
			target = "demo.js"
		}
		if err = os.WriteFile(filepath.Join(source, target), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	harness := `import {mount as legacyMount} from './demo.js';
export async function mount(element,host){
 const cleanup=await legacyMount(element,host);
 if(!element.querySelector('#demo-theme-mode'))throw Error('legacy DOM missing');
 await host.store.set({compatible:true});if(!(await host.store.get()).compatible)throw Error('store');
 if(await host.files.read('hello.txt')!=='plugin workspace')throw Error('workspace');
 try{await host.http.request({url:'https://not-declared.invalid/'});throw Error('allowlist bypass')}catch(e){if(!String(e).includes('E_PLUGIN_HOST_DENIED'))throw e;}
 let shown=0,hidden=0;host.events.on('shown',()=>shown++);host.events.on('hidden',()=>hidden++);
 setTimeout(()=>{
  if(shown!==1){document.body.dataset.result='bad-shown';return}
  window.allyDispose();window.allyDispose();
  document.body.dataset.result=hidden===1 && !document.querySelector('style[data-plugin]') && !element.children.length?'passed':'bad-cleanup';
 },100);
 return cleanup;
}`
	if err := os.WriteFile(filepath.Join(source, "index.js"), []byte(harness), 0600); err != nil {
		t.Fatal(err)
	}
	var err error
	info, err = a.core.ImportPluginFromDir(source)
	if err != nil {
		t.Fatal(err)
	}
	state := NewTheme(Palette{Name: "test", Dark: true})
	state.Active = true
	p, err := a.newServer(info, workspace, state)
	if err != nil {
		t.Fatal(err)
	}
	defer p.server.Close()
	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// Playwright waits for a DOM result without treating the persistent SSE
	// stream as a pending navigation, as dump-dom virtual time can do.
	script := `import sys
from playwright.sync_api import sync_playwright
with sync_playwright() as p:
    context = p.chromium.launch_persistent_context(sys.argv[3], executable_path=sys.argv[1], headless=True, timeout=10000)
    page = context.pages[0] if context.pages else context.new_page()
    page.on("pageerror", lambda e: print("PAGE ERROR", e, flush=True))
    page.on("console", lambda m: print("CONSOLE", m.type, m.text, flush=True))
    page.goto(sys.argv[2], wait_until="domcontentloaded")
    try:
        page.wait_for_function("document.body.dataset.result !== undefined", timeout=10000, polling=100)
    finally:
        print(page.content(), flush=True)
        context.close()
`
	cmd := exec.CommandContext(ctx, "python", "-c", script, browser, p.url, filepath.Join(dir, "profile"))
	cmd.WaitDelay = 3 * time.Second
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("browser: %v: %.4000s", err, out)
	}
	if !strings.Contains(string(out), `data-result="passed"`) {
		t.Fatalf("legacy plugin failed: %.5000s", out)
	}
}
