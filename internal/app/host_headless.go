// SPDX-License-Identifier: GPL-3.0-only
package app

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

// RunHeadless shares the desktop binary and Agent runtime, without starting
// MyGo's application loop, display connection, windows or tray.
func RunHeadless(args []string) int {
	attachHeadlessConsole()
	cfg, err := loadServerOptions(args, os.Stderr)
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	if cfg.DataDir != "" {
		if err := os.Setenv("ALLY_DATA_DIR", cfg.DataDir); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	a := NewApp()
	if logger, _, logErr := InitErrorLogger(); logErr == nil {
		a.SetErrorLogger(logger)
	}
	if err := a.serveHeadless(ctx, cfg, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

// The dedicated listener is always enabled, irrespective of api.json's desktop
// preference. CLI credentials and address do not rewrite desktop settings.
func (a *App) serveHeadless(parent context.Context, cfg ServerConfig, output io.Writer) (err error) {
	if err := parent.Err(); err != nil {
		return err
	}
	if err := a.acquireRuntimeLock(); err != nil {
		return err
	}
	started := false
	defer func() {
		if !started && a.runtime.lock != nil {
			err = errors.Join(err, a.runtime.lock.Unlock())
			a.runtime.lock = nil
		}
	}()
	usingSavedToken := cfg.Token == ""
	if usingSavedToken {
		saved := loadApiSettings()
		cfg.Token = saved.Token
		if cfg.Token == "" {
			cfg.Token, err = generateApiToken()
			if err != nil {
				return err
			}
			saved.Token = cfg.Token
			if err := saveApiSettings(saved); err != nil {
				return err
			}
		}
	}
	listener, err := net.Listen("tcp", cfg.address())
	if err != nil {
		return fmt.Errorf("API listen %s: %w", cfg.address(), err)
	}
	defer listener.Close()
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	sink, err := newNetworkEventSink(cfg.address(), cfg.Token, networkHistoryDefault)
	if err != nil {
		return err
	}
	sink.done = ctx.Done()
	a.events = sink
	a.runtime.workspace, a.runtime.requestShutdown = cfg.Workspace, cancel
	if err := a.startRuntime(ctx); err != nil {
		return err
	}
	started = true
	server := &http.Server{
		Handler:           a.headlessHandler(cfg.Token, sink),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	served := make(chan error, 1)
	go func() { served <- server.Serve(listener) }()
	address := listener.Addr().String()
	fmt.Fprintln(output, "Ally headless API listening:", "http://"+address+"/api/v1")
	fmt.Fprintln(output, "Events:", "http://"+address+"/api/v1/events", "(Bearer authentication required)")
	if cfg.TokenFile != "" {
		fmt.Fprintln(output, "API token file:", cfg.TokenFile)
	} else if usingSavedToken {
		fmt.Fprintln(output, "API token file:", apiSettingsPath(), "(token field)")
	}
	select {
	case <-ctx.Done():
	case serveErr := <-served:
		err = fmt.Errorf("API server stopped: %w", serveErr)
	}
	// Stop admission and release SSE before waiting for active HTTP handlers.
	// Keep the data lock until requests and runtime checkpoints have drained.
	cancel()
	// Allow the 5-second header timeout to expire for connections accepted
	// just before shutdown; using that same deadline races healthy cleanup.
	deadline, stop := context.WithTimeout(context.Background(), 10*time.Second)
	defer stop()
	if shutdownErr := server.Shutdown(deadline); shutdownErr != nil {
		err = errors.Join(err, shutdownErr, server.Close())
	}
	return errors.Join(err, a.stopRuntime(20*time.Second))
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
		a.runtime.requestShutdown()
	})
	mux.HandleFunc("GET /api/v1/events", sink.handleSSE)
	mux.HandleFunc("GET /api/v1/events/poll", sink.handlePoll)
	protected := apiAuthWithToken(mux, func() string { return token })
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/v1/") && !strings.HasPrefix(r.URL.Path, "/generated-images/") {
			http.NotFound(w, r)
			return
		}
		if a.ctx != nil && a.ctx.Err() != nil {
			apiWriteError(w, http.StatusServiceUnavailable, "application is shutting down")
			return
		}
		if a.ctx != nil {
			ctx, cancel := context.WithCancel(r.Context())
			stop := context.AfterFunc(a.ctx, cancel)
			defer cancel()
			defer stop()
			r = r.WithContext(ctx)
		}
		protected.ServeHTTP(w, r)
	})
}
