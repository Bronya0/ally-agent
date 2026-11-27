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
	"fmt"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// rotatingErrorWriter is an io.Writer that appends to the error log file
// for the current calendar day (error-YYYY-MM-DD.log). When the date changes,
// it transparently closes the old file, opens the new one, and cleans up older logs.
// Wrapping this in an io.Writer ensures that any slog.Logger (including wailsApp.Logger
// and App.errorLogger) permanently writes to the active file without needing its
// Handler or logger instance replaced.
type rotatingErrorWriter struct {
	mu   sync.Mutex
	file *os.File
	path string
	date string // YYYY-MM-DD
}

var globalRotatingErrorWriter = &rotatingErrorWriter{}
var globalErrorLogger *slog.Logger

const errorLogPrefix = "error-"
const errorLogSuffix = ".log"

// maxErrorLogBytes caps one day's file. Daily cleanup drops yesterday, but a
// single long session that logs tool failures can fill a day on its own, and a
// diagnostic log must never be the reason the disk runs out.
const maxErrorLogBytes = 64 << 20

func errorLogFileName(date string) string {
	return errorLogPrefix + date + errorLogSuffix
}

func todayDate() string {
	return time.Now().Format("2006-01-02")
}

// Write implements io.Writer with transparent daily rotation.
func (w *rotatingErrorWriter) Write(p []byte) (n int, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	today := todayDate()
	if w.file == nil || w.date != today {
		if err := w.rotateLocked(today); err != nil {
			if w.file != nil {
				return w.file.Write(p) // fallback to open file if rotation fails
			}
			return os.Stderr.Write(p)
		}
	}
	// Past the cap the record is dropped, but reported as written: a logging
	// failure must not surface as a tool error.
	if info, err := w.file.Stat(); err == nil && info.Size() >= maxErrorLogBytes {
		return len(p), nil
	}
	return w.file.Write(p)
}

func (w *rotatingErrorWriter) rotateLocked(today string) error {
	dir := filepath.Join(appDataDir(), "logs")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	path := filepath.Join(dir, errorLogFileName(today))
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	old := w.file
	w.file = f
	w.path = path
	w.date = today
	if old != nil {
		_ = old.Close()
	}
	cleanupOldErrorLogs(dir, today)
	return nil
}

// InitErrorLogger initializes the shared on-disk error logger writing to
// ~/.ally_agent/logs/error-YYYY-MM-DD.log. Daily file rotation and cleanup
// are managed transparently via rotatingErrorWriter.
func InitErrorLogger() (*slog.Logger, string, error) {
	globalRotatingErrorWriter.mu.Lock()
	defer globalRotatingErrorWriter.mu.Unlock()

	if globalErrorLogger != nil {
		return globalErrorLogger, globalRotatingErrorWriter.path, nil
	}

	today := todayDate()
	if err := globalRotatingErrorWriter.rotateLocked(today); err != nil {
		return nil, "", err
	}

	handler := slog.NewTextHandler(globalRotatingErrorWriter, &slog.HandlerOptions{
		Level:     slog.LevelError,
		AddSource: false,
	})
	globalErrorLogger = slog.New(handler)
	return globalErrorLogger, globalRotatingErrorWriter.path, nil
}

// errorLogNameRe matches the exact shape we manage: error-YYYY-MM-DD.log.
// A strict pattern (not just a prefix/suffix check) prevents deleting files
// that merely look similar but are not our dated error logs.
var errorLogNameRe = regexp.MustCompile(`^error-(\d{4}-\d{2}-\d{2})\.log$`)

// cleanupOldErrorLogs removes error logs from days strictly before today so
// that only the current day's file remains. Deletion is deliberately
// conservative:
//   - only files matching errorLogNameRe are ever considered;
//   - the embedded date must parse as a real calendar date;
//   - only dates strictly before today are removed (today and any future-dated
//     file are left untouched).
//
// today is todayDate(), so a valid YYYY-MM-DD compares as a date too.
// Unrelated files (service.log, error-foo.log, subdirs, …) are never touched.
func cleanupOldErrorLogs(dir, today string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		m := errorLogNameRe.FindStringSubmatch(e.Name())
		if m == nil {
			continue // not one of our managed error logs
		}
		if _, perr := time.Parse("2006-01-02", m[1]); perr != nil {
			continue // unparseable date — leave it alone
		}
		if m[1] >= today {
			continue // today or future — never delete
		}
		_ = os.Remove(filepath.Join(dir, e.Name())) // best-effort
	}
}

// SetErrorLogger stores the structured error logger on the App so backend and
// frontend errors share one on-disk channel.
func (a *App) SetErrorLogger(l *slog.Logger) {
	a.mu.Lock()
	a.errorLogger = l
	a.mu.Unlock()
}

// logAppError records a backend error. It falls back to the stdlib logger when
// the structured logger was never initialised (e.g. log dir unwritable).
func (a *App) logAppError(msg string, args ...any) {
	a.mu.Lock()
	l := a.errorLogger
	a.mu.Unlock()
	if l != nil {
		l.Error(msg, args...)
		return
	}
	log.Printf("[error] %s%s", msg, formatLogArgs(args))
}

// logToolFailure records a failed tool call, including what the model sent and
// what came back, so a report can be checked without replaying the session.
// Both sides are workspace payloads (file bodies, command output, tokens a
// command printed), so each field is clipped: the log is a pointer to the
// failure, not a copy of the workspace.
func (a *App) logToolFailure(sessionID, name string, args []byte, result toolResult) {
	// The user stopping a call is not a defect and would drown the real ones.
	if result.ErrorCode == "E_CANCELLED" || strings.HasSuffix(result.ErrorCode, "_CANCELLED") {
		return
	}
	detail := result.Error
	if detail == "" {
		detail = "tool reported failure without a message"
	}
	var data string
	if result.Data != nil {
		if encoded, err := json.Marshal(result.Data); err == nil {
			data = string(encoded)
		}
	}
	a.logAppError("tool call failed",
		"session", sessionID,
		"tool", name,
		"code", result.ErrorCode,
		"error", logHead(detail, 500),
		"args", logHead(string(args), toolFailureLogBytes),
		"result", logHead(data, toolFailureLogBytes),
	)
}

// toolFailureLogBytes bounds one logged argument or result field.
const toolFailureLogBytes = 2 << 10

// logHead clips a field to a bounded head, ending on a rune boundary so the log
// stays valid UTF-8 whatever the workspace contains.
func logHead(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut] + "…(truncated)"
}

// formatLogArgs renders the key/value pairs the structured logger would have
// taken. The fallback line is the only record left when the log directory is
// unwritable, and passing them positionally loses exactly the fields that make
// a report actionable (fmt prints "%!(EXTRA string=session, ...)").
func formatLogArgs(args []any) string {
	if len(args) == 0 {
		return ""
	}
	var b strings.Builder
	for i := 0; i < len(args); i += 2 {
		fmt.Fprintf(&b, " %v=", args[i])
		if i+1 < len(args) {
			fmt.Fprintf(&b, "%v", args[i+1])
		} else {
			b.WriteString("(missing value)")
		}
	}
	return b.String()
}

// LogFrontendError is a Wails binding: the frontend routes uncaught errors
// (window.onerror, unhandledrejection, Vue errorHandler) here so they land in
// the same error log as backend errors. It never fails — a reporting problem
// must not crash the UI.
func (a *App) LogFrontendError(message string, stack string) error {
	a.mu.Lock()
	l := a.errorLogger
	a.mu.Unlock()
	if l != nil {
		if stack != "" {
			l.Error("frontend error", "message", message, "stack", stack)
		} else {
			l.Error("frontend error", "message", message)
		}
		return nil
	}
	log.Printf("[frontend error] %s\n%s", message, stack)
	return nil
}
