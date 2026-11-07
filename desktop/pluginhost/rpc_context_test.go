// SPDX-License-Identifier: GPL-3.0-only
package pluginhost

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestPluginRPCCombinesRequestAndPageCancellation(t *testing.T) {
	for _, kind := range []string{"request", "retirement"} {
		t.Run(kind, func(t *testing.T) {
			h, info, workspace, queue := queuedPluginHost(t)
			started, aborted := make(chan struct{}), make(chan struct{})
			var requests atomic.Int32
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				if requests.Add(1) == 1 {
					close(started)
				}
				<-r.Context().Done()
				close(aborted)
			}))
			defer target.Close()
			// The imported installation is entirely under this test's app-data dir.
			manifestPath := filepath.Join(info.Dir, "plugin.json")
			data, err := os.ReadFile(manifestPath)
			if err != nil {
				t.Fatal(err)
			}
			var manifest map[string]any
			if err = json.Unmarshal(data, &manifest); err != nil {
				t.Fatal(err)
			}
			manifest["permissions"].(map[string]any)["http"] = []string{"127.0.0.1"}
			data, _ = json.Marshal(manifest)
			if err = os.WriteFile(manifestPath, data, 0600); err != nil {
				t.Fatal(err)
			}
			surface := &fakePluginSurface{eval: func(ctx context.Context, code string) (any, error) {
				<-ctx.Done()
				return nil, ctx.Err()
			}}
			p := fakeServerPage(t, h, info, workspace, surface)
			h.pages = map[string]*page{info.ID: p}
			data, _ = json.Marshal(map[string]any{"method": "PluginHTTPRequest", "args": []any{map[string]string{"url": target.URL, "method": "POST", "body": "side effect"}}})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			req, _ := http.NewRequestWithContext(ctx, "POST", p.url+"/rpc", bytes.NewReader(data))
			req.Header.Set("Origin", p.url)
			req.Header.Set("X-Ally-Plugin", p.token)
			result := make(chan error, 1)
			go func() {
				resp, err := http.DefaultClient.Do(req)
				if err == nil {
					_, _ = io.Copy(io.Discard, resp.Body)
					resp.Body.Close()
				}
				result <- err
			}()
			select {
			case <-started:
			case <-time.After(2 * time.Second):
				t.Fatal("RPC did not reach local target")
			}
			if kind == "request" {
				cancel()
			} else {
				h.Reload(info.ID)
			}
			// Retirement's JS deadline is 500ms; HTTP must be aborted before it.
			select {
			case <-aborted:
			case <-time.After(300 * time.Millisecond):
				t.Fatal("HTTP was not canceled immediately")
			}
			select {
			case <-result:
			case <-time.After(time.Second):
				t.Fatal("RPC did not finish")
			}
			if requests.Load() != 1 {
				t.Fatal("side-effect request replayed", requests.Load())
			}
			if kind == "retirement" {
				pumpPluginUI(t, queue, func() bool { return h.retiring[info.ID] == nil })
			}
		})
	}
}

func TestPluginNotifyRPCRechecksAtUIDelivery(t *testing.T) {
	h, info, workspace, queue := queuedPluginHost(t)
	p := fakeServerPage(t, h, info, workspace, &fakePluginSurface{})
	h.pages = map[string]*page{info.ID: p}
	count := 0
	h.notify = func(string, string) { count++ }
	if code, body := pluginTestRPC(t, p, "notify", "old", "info"); code != 200 {
		t.Fatal(code, body)
	}
	h.Reload(info.ID)
	(<-queue)()
	if count != 0 {
		t.Fatal("queued old notification delivered after retirement")
	}
	pumpPluginUI(t, queue, func() bool { return h.retiring[info.ID] == nil })
}
