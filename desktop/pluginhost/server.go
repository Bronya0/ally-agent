// SPDX-License-Identifier: GPL-3.0-only
package pluginhost

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	backend "ally-dev/internal/app"
	"ally-dev/resources"
	"github.com/egoist/mygo"

	"github.com/google/uuid"
)

// Like examples/embed-webview, each visited page owns a native WebView. Keep
// at most three pages (including the visible one); evicted pages mount anew.
const maxPluginViews = 3

type Theme struct {
	Active bool              `json:"active"`
	Name   string            `json:"name"`
	Mode   string            `json:"mode"`
	Tokens map[string]string `json:"tokens"`
}
type page struct {
	info       backend.PluginInfo
	workspace  string
	view       *mygo.WebView
	server     *http.Server
	url, token string
	mu         sync.Mutex
	state      Theme
	changed    chan struct{}
	closed     bool
	used       uint64
}

func pluginRelativePath(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, `\`) || filepath.IsAbs(raw) || strings.Contains(raw, ":") {
		return "", errors.New("host.files 只接受工作区内的相对路径")
	}
	for _, part := range strings.FieldsFunc(raw, func(r rune) bool { return r == '/' || r == '\\' }) {
		if part == ".." {
			return "", errors.New("host.files 不接受上级目录引用")
		}
	}
	return raw, nil
}

func (p *page) rpc(h *Host, w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" || r.Header.Get("X-Ally-Plugin") != p.token || r.Header.Get("Origin") != p.url {
		http.Error(w, "forbidden", 403)
		return
	}
	var call struct {
		Method string            `json:"method"`
		Args   []json.RawMessage `json:"args"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<20)).Decode(&call); err != nil {
		http.Error(w, "invalid request", 400)
		return
	}
	p.mu.Lock()
	closed := p.closed
	p.mu.Unlock()
	if closed {
		http.Error(w, "closed", 410)
		return
	}
	var result any
	var err error
	arg := func(i int, v any) error {
		if i >= len(call.Args) {
			return errors.New("missing argument")
		}
		return json.Unmarshal(call.Args[i], v)
	}
	var value string
	switch call.Method {
	case "PluginHTTPRequest":
		var req backend.PluginHTTPRequest
		if err = arg(0, &req); err == nil {
			req.PluginID = p.info.ID
			result, err = h.core.PluginHTTPRequest(req)
		}
	case "PluginStoreGet":
		result, err = h.core.PluginStoreGet(p.info.ID)
	case "PluginStoreSet":
		if err = arg(1, &value); err == nil {
			err = h.core.PluginStoreSet(p.info.ID, value)
		}
	case "ReadWorkspaceFileAt", "ListFiles", "GetWorkspaceFileInfoAt", "GetWorkspaceMediaURL", "OpenPathInFileManager":
		if p.info.Permissions.Workspace != "read" || p.workspace == "" {
			err = errors.New("插件没有工作区读取权限")
			break
		}
		var req backend.WorkspacePathRequest
		if call.Method == "OpenPathInFileManager" {
			err = arg(0, &value)
			req.Path = strings.TrimPrefix(value, p.workspace+"/")
		} else {
			err = arg(0, &req)
		}
		if err != nil {
			break
		}
		req.Workspace = p.workspace
		req.Path, err = pluginRelativePath(req.Path)
		if err != nil {
			break
		}
		switch call.Method {
		case "ReadWorkspaceFileAt":
			result, err = h.core.ReadWorkspaceFileAt(req)
		case "GetWorkspaceFileInfoAt":
			result, err = h.core.GetWorkspaceFileInfoAt(req)
		case "GetWorkspaceMediaURL":
			result, err = h.core.GetWorkspaceMediaURL(req)
		case "OpenPathInFileManager":
			err = h.core.OpenPathInFileManager(filepath.Join(p.workspace, req.Path))
		case "ListFiles":
			var list backend.ListFilesRequest
			if err = arg(0, &list); err == nil {
				list.Workspace = p.workspace
				list.Path = req.Path
				result, err = h.core.ListFiles(list)
			}
		}
	case "LogFrontendError":
		if err = arg(0, &value); err == nil {
			err = h.core.LogFrontendError("[plugin:"+p.info.ID+"] "+value, "")
		}
	case "notify":
		var kind string
		err = arg(0, &value)
		_ = arg(1, &kind)
		if err == nil {
			h.win.Update(func() {
				if !h.closed {
					h.notify(value, kind)
				}
			})
		}
	default:
		err = errors.New("unsupported plugin method")
	}
	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		w.WriteHeader(400)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	_ = json.NewEncoder(w).Encode(result)
}

func (p *page) setState(s Theme) {
	p.mu.Lock()
	p.state = s
	p.mu.Unlock()
	select {
	case p.changed <- struct{}{}:
	default:
	}
}
func (p *page) serveEvents(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("token") != p.token {
		http.Error(w, "forbidden", 403)
		return
	}
	f, ok := w.(http.Flusher)
	if !ok {
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	for {
		p.mu.Lock()
		s, closed := p.state, p.closed
		p.mu.Unlock()
		if closed {
			return
		}
		data, _ := json.Marshal(s)
		_, err := fmt.Fprintf(w, "data: %s\n\n", data)
		if err != nil {
			return
		}
		f.Flush()
		select {
		case <-r.Context().Done():
			return
		case <-p.changed:
		}
	}
}

func (h *Host) newServer(info backend.PluginInfo, workspace string, state Theme) (*page, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	p := &page{info: info, workspace: workspace, url: "http://" + listener.Addr().String(), token: uuid.NewString(), state: state, changed: make(chan struct{}, 1)}
	assets, _ := fs.Sub(resources.Assets, "plugin")
	static := http.FileServer(http.FS(assets))
	mux := http.NewServeMux()
	mux.HandleFunc("/rpc", func(w http.ResponseWriter, r *http.Request) { p.rpc(h, w, r) })
	mux.HandleFunc("/events", p.serveEvents)
	mux.HandleFunc("/config.js", func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		s := p.state
		p.mu.Unlock()
		cfg := map[string]any{"plugin": info, "token": p.token, "state": s, "env": map[string]string{"workspace": workspace, "locale": "zh-CN", "version": mygo.App.Version()}}
		data, _ := json.Marshal(cfg)
		w.Header().Set("Content-Type", "text/javascript")
		w.Header().Set("Cache-Control", "no-store")
		fmt.Fprintf(w, "window.pluginConfig=%s;", data)
	})
	mux.Handle("/", static)
	handler := backend.PluginAssetMiddleware(h.core)(mux)
	p.server = &http.Server{ReadHeaderTimeout: 5 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != listener.Addr().String() {
			http.Error(w, "forbidden", 403)
			return
		}
		// One origin per page. Resources for other installed plugins are unavailable.
		if strings.HasPrefix(r.URL.Path, "/plugins/") && !strings.HasPrefix(r.URL.Path, "/plugins/"+info.ID+"/") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
		w.Header().Set("Referrer-Policy", "no-referrer")
		if site := r.Header.Get("Sec-Fetch-Site"); site == "cross-site" {
			http.Error(w, "forbidden", 403)
			return
		}
		handler.ServeHTTP(w, r)
	})}
	go func() { _ = p.server.Serve(listener) }()
	return p, nil
}
