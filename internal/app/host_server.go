//go:build server

package app

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// headlessLifecycle registers lifecycle hooks only. App's desktop bindings are
// deliberately not registered on the public server.
type headlessLifecycle struct {
	app     *App
	ctx     context.Context
	stopErr error
}

func (s *headlessLifecycle) ServiceStartup(_ context.Context, _ application.ServiceOptions) error {
	return s.app.startRuntime(s.ctx)
}
func (s *headlessLifecycle) ServiceShutdown() error {
	s.stopErr = s.app.stopRuntime(20 * time.Second)
	return s.stopErr
}

// RunHeadless uses Wails' server platform implementation, sharing all business
// handlers and orchestration with the desktop host.
func RunHeadless(args []string) int {
	cfg, err := loadServerOptions(args, os.Stderr)
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	if cfg.DataDir != "" {
		if err = os.Setenv("ALLY_DATA_DIR", cfg.DataDir); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
	}
	a := NewApp()
	a.runtime.workspace = cfg.Workspace
	if err = a.acquireRuntimeLock(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer func() {
		if a.runtime.lock != nil {
			_ = a.runtime.lock.Unlock()
		}
	}()
	saved := loadApiSettings()
	if cfg.Token == "" {
		cfg.Token = saved.Token
	}
	if cfg.Token == "" {
		cfg.Token = generateApiToken()
		saved.Token = cfg.Token
		if err = saveApiSettings(saved); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		fmt.Fprintln(os.Stderr, "API token created in", apiSettingsPath())
	}
	// Wails reads these variables after ServerOptions. Normalize them to the
	// resolved CLI/config values so they cannot silently change the listen address.
	_ = os.Setenv("WAILS_SERVER_HOST", cfg.Host)
	_ = os.Setenv("WAILS_SERVER_PORT", fmt.Sprint(cfg.Port))
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if logger, _, logErr := InitErrorLogger(); logErr == nil {
		a.SetErrorLogger(logger)
	}
	a.runtime.requestShutdown = cancel
	sink, sinkErr := newNetworkEventSink("127.0.0.1:0", cfg.Token, networkHistoryDefault)
	if sinkErr != nil {
		fmt.Fprintln(os.Stderr, sinkErr)
		return 1
	}
	sink.done = ctx.Done()
	a.events = sink
	handler := a.headlessHandler(cfg.Token, sink)
	lifecycle := &headlessLifecycle{app: a, ctx: ctx}
	wailsApp := application.New(application.Options{
		Name:     "Ally Server",
		Server:   application.ServerOptions{Host: cfg.Host, Port: cfg.Port, ShutdownTimeout: 25 * time.Second},
		Services: []application.Service{application.NewService(lifecycle)},
		Assets:   application.AssetOptions{Handler: handler, Middleware: func(_ http.Handler) http.Handler { return handler }},
	})
	// Cancellation also closes SSE before Wails waits for active HTTP requests.
	go func() { <-ctx.Done(); wailsApp.Quit() }()
	fmt.Fprintln(os.Stderr, "Starting Ally API at http://"+cfg.address()+"/api/v1 (Bearer authentication required)")
	if err = wailsApp.Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if lifecycle.stopErr != nil {
		fmt.Fprintln(os.Stderr, lifecycle.stopErr)
		return 1
	}
	return 0
}

func (a *App) headlessHandler(token string, sink *networkEventSink) http.Handler {
	mux := a.apiMux()
	mux.HandleFunc("POST /api/v1/shutdown", func(w http.ResponseWriter, r *http.Request) {
		if a.runtime.requestShutdown == nil {
			apiWriteError(w, http.StatusServiceUnavailable, "shutdown is not configured")
			return
		}
		apiWriteOK(w, map[string]any{"shuttingDown": true})
		_ = http.NewResponseController(w).Flush()
		go a.runtime.requestShutdown()
	})
	mux.HandleFunc("GET /api/v1/events", sink.handleSSE)
	mux.HandleFunc("GET /api/v1/events/poll", sink.handlePoll)
	protected := apiAuthWithToken(mux, func() string { return token })
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/v1/") {
			http.NotFound(w, r)
			return
		}
		if a.ctx != nil && a.ctx.Err() != nil {
			apiWriteError(w, http.StatusServiceUnavailable, "application is shutting down")
			return
		}
		if a.ctx != nil {
			requestCtx, cancel := context.WithCancel(r.Context())
			stop := context.AfterFunc(a.ctx, cancel)
			defer cancel()
			defer stop()
			r = r.WithContext(requestCtx)
		}
		protected.ServeHTTP(w, r)
	})
}
