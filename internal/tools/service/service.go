// Package service holds pure helpers for the background_process tool: a
// thread-safe rolling output buffer and a long-running-command classifier.
//
// Nothing here may depend on App state, ConfigState, or *App receivers.
// App-owned orchestration (process lifecycle, output streaming, service
// registry) stays in internal/app.
package service

import (
	"bytes"
	"fmt"
	"strings"
	"sync"

	toolerrors "ally-dev/internal/tools/shared"
)

// Output limits used by both the app layer and the model-facing read action.
const (
	OutputLimit       = 512 * 1024
	OutputPreview     = 8 * 1024
	MaxActive         = 8
	DefaultReadTail   = 8 * 1024
	MaxReadTail       = 32 * 1024
)

// RollingBuffer is a thread-safe byte buffer that keeps at most `limit` bytes
// of the most recent output. Writes beyond the limit drop the oldest bytes and
// mark the buffer as truncated. It is safe for concurrent use.
type RollingBuffer struct {
	mu        sync.Mutex
	buf       []byte
	limit     int
	total     int64
	truncated bool
}

// NewRollingBuffer returns a buffer that retains the last `limit` bytes.
func NewRollingBuffer(limit int) *RollingBuffer {
	return &RollingBuffer{limit: limit}
}

// Write appends p to the buffer, dropping older bytes if needed.
func (b *RollingBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.total += int64(len(p))
	if b.limit <= 0 {
		return len(p), nil
	}
	if len(p) >= b.limit {
		b.buf = append(b.buf[:0], p[len(p)-b.limit:]...)
		b.truncated = true
		return len(p), nil
	}
	if overflow := len(b.buf) + len(p) - b.limit; overflow > 0 {
		copy(b.buf, b.buf[overflow:])
		b.buf = b.buf[:len(b.buf)-overflow]
		b.truncated = true
	}
	b.buf = append(b.buf, p...)
	return len(p), nil
}

// String returns the current buffered output as a string.
func (b *RollingBuffer) String() string {
	output, _, _ := b.Snapshot()
	return output
}

// Snapshot returns the current buffered output, the total bytes ever written,
// and whether the buffer was truncated.
func (b *RollingBuffer) Snapshot() (string, int64, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(bytes.Clone(b.buf)), b.total, b.truncated
}

// Restore replaces the buffer state with the given output. Used when
// restoring service history on startup.
func (b *RollingBuffer) Restore(output []byte, total int64, truncated bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(output) > b.limit {
		output = output[len(output)-b.limit:]
		truncated = true
	}
	b.buf = append(b.buf[:0], output...)
	b.total = total
	if b.total < int64(len(output)) {
		b.total = int64(len(output))
	}
	b.truncated = truncated
}

// TailString returns the last `limit` bytes of s. If limit <= 0 or s is
// shorter than limit, s is returned unchanged.
func TailString(s string, limit int) string {
	if limit <= 0 || len(s) <= limit {
		return s
	}
	return s[len(s)-limit:]
}

// NormalizeCommand lower-cases the command and collapses runs of whitespace.
func NormalizeCommand(command string) string {
	return strings.ToLower(strings.Join(strings.Fields(command), " "))
}

// LooksLikeLongRunningService only blocks an explicit whitelist of known
// dev-server commands. Anything else continues to the normal run_command
// safety checks/timeouts.
func LooksLikeLongRunningService(command string) bool {
	cmd := NormalizeCommand(command)
	if cmd == "" {
		return false
	}
	patterns := []string{
		"manage.py runserver",
		"flask run",
		"uvicorn ",
		"hypercorn ",
		"fastapi dev",
		"npm run dev",
		"pnpm run dev",
		"pnpm dev",
		"yarn run dev",
		"yarn dev",
		"bun run dev",
		"bun dev",
		"next dev",
		"nuxt dev",
		"wails dev",
		"vite preview",
		"vite dev",
		"-m http.server",
		"streamlit run",
		"ng serve",
		"react-scripts start",
		"vue-cli-service serve",
		"mkdocs serve",
		"hugo server",
		"php artisan serve",
		"jupyter notebook",
		"jupyter lab",
		"nodemon",
	}
	for _, pattern := range patterns {
		if strings.Contains(cmd, pattern) {
			return true
		}
	}
	return false
}

// LongRunningCommandError wraps the command in an E_LONG_RUNNING_COMMAND error
// directing the caller to use background_process with action=start.
func LongRunningCommandError(command string) error {
	return toolerrors.New("E_LONG_RUNNING_COMMAND", fmt.Errorf("this command looks like a long-running process; use background_process with action=start so it can run without blocking the agent.\n被拦截的命令: %s", command))
}
