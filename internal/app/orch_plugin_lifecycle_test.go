// SPDX-License-Identifier: GPL-3.0-only
package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"ally-dev/internal/tools/plugin"
)

func isolatedPluginApp(t *testing.T) (*App, PluginInfo) {
	t.Helper()
	home := redirectAppStateDir(t)
	t.Setenv("ALLY_DATA_DIR", home)
	a := NewApp()
	t.Cleanup(func() { _ = a.ShutdownNative() })
	info, err := a.ImportPluginFromDir(demoPluginFixture)
	if err != nil {
		t.Fatal(err)
	}
	return a, info
}

func TestPluginStoreVersionCannotOverwriteReinstallationOrReviveDeletion(t *testing.T) {
	a, old := isolatedPluginApp(t)
	if err := a.PluginStoreSetForVersion(old.ID, old.UpdatedAt, `{"old":true}`); err != nil {
		t.Fatal(err)
	}
	// Reinstallation has its own stamp even if all package files are unchanged.
	if _, err := a.ImportPluginFromDir(demoPluginFixture); err != nil {
		t.Fatal(err)
	}
	if err := a.PluginStoreSet(old.ID, `{"new":true}`); err != nil {
		t.Fatal(err)
	}
	if err := a.PluginStoreSetForVersion(old.ID, old.UpdatedAt, `{"late":true}`); err == nil {
		t.Fatal("old realm overwrote new installation")
	}
	if _, err := a.PluginStoreGetForVersion(old.ID, old.UpdatedAt); err == nil {
		t.Fatal("old realm read new installation")
	}
	if raw, err := a.PluginStoreGet(old.ID); err != nil || raw != `{"new":true}` {
		t.Fatal(raw, err)
	}
	if err := a.DeletePlugin(old.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := a.PluginStoreSetForVersion(old.ID, old.UpdatedAt, `{"late":true}`); err == nil {
		t.Fatal("deleted plugin saved")
	}
	if _, err := os.Stat(plugin.DataPath(a.pluginRoot(), old.ID)); !os.IsNotExist(err) {
		t.Fatal("purged data revived", err)
	}
}

func TestPluginStoreWriteWaitsForInstallDeleteLock(t *testing.T) {
	a, info := isolatedPluginApp(t)
	a.pluginMu.Lock()
	result := make(chan error, 1)
	go func() { result <- a.PluginStoreSetForVersion(info.ID, info.UpdatedAt, `{"late":true}`) }()
	select {
	case err := <-result:
		a.pluginMu.Unlock()
		t.Fatal("write bypassed plugin lock", err)
	case <-time.After(30 * time.Millisecond):
	}
	// Model the removal half of DeletePlugin while still owning its actual lock.
	err := os.RemoveAll(filepath.Join(a.pluginRoot(), info.ID))
	a.pluginMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("write did not revalidate installation under lock")
		}
	case <-time.After(time.Second):
		t.Fatal("writer did not leave lock")
	}
	if _, err := os.Stat(plugin.DataPath(a.pluginRoot(), info.ID)); !os.IsNotExist(err) {
		t.Fatal("late writer recreated data", err)
	}
}

func TestPluginHTTPRequestCancellationAndShutdown(t *testing.T) {
	for _, kind := range []string{"caller", "shutdown", "compatibility"} {
		t.Run(kind, func(t *testing.T) {
			a, info := isolatedPluginApp(t)
			a.ctx, a.runtime.cancel = context.WithCancel(context.Background())
			started, canceled := make(chan struct{}), make(chan struct{})
			var count atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				if count.Add(1) == 1 {
					close(started)
				}
				<-r.Context().Done()
				close(canceled)
			}))
			defer server.Close()
			manifestPath := filepath.Join(info.Dir, plugin.ManifestName)
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
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			result := make(chan error, 1)
			go func() {
				req := PluginHTTPRequest{PluginID: info.ID, URL: server.URL, Method: "POST", Body: "effect"}
				var err error
				if kind == "compatibility" {
					_, err = a.PluginHTTPRequest(req)
				} else {
					_, err = a.PluginHTTPRequestContext(ctx, req)
				}
				result <- err
			}()
			select {
			case <-started:
			case <-time.After(2 * time.Second):
				t.Fatal("request did not start")
			}
			if kind == "caller" {
				cancel()
			} else if err := a.ShutdownNative(); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-result:
				if !errors.Is(err, context.Canceled) {
					t.Fatal("cancellation not propagated", err)
				}
			case <-time.After(time.Second):
				t.Fatal("HTTP request survived cancellation")
			}
			select {
			case <-canceled:
			case <-time.After(time.Second):
				t.Fatal("remote request survived cancellation")
			}
			if count.Load() != 1 {
				t.Fatal("side effect request replayed", count.Load())
			}
		})
	}
}
