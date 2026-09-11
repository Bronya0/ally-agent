// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
// Package service holds pure helpers for the service tool: a
// thread-safe rolling output buffer and a long-running-command classifier.
//
// Nothing here may depend on App state, ConfigState, or *App receivers.
// App-owned orchestration (process lifecycle, output streaming, service
// registry) stays in internal/app.
package service

import (
	"bytes"
	"strings"
	"sync"
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
// Used by the promoted-command service naming (command tools no longer
// pre-classify: a command that outlives its timeout is promoted to a service
// instead of being blocked or killed).
func NormalizeCommand(command string) string {
	return strings.ToLower(strings.Join(strings.Fields(command), " "))
}

// PromotedServiceName derives the display name for a service created by
// promoting a timed-out command. It keeps the first two tokens of the command
// (e.g. "npm run dev ..." → "npm run"), which is enough to recognize the
// service in the task center without leaking a long command line into the
// name field.
func PromotedServiceName(command string) string {
	fields := strings.Fields(NormalizeCommand(command))
	if len(fields) == 0 {
		return ""
	}
	if len(fields) == 1 {
		return fields[0]
	}
	return fields[0] + " " + fields[1]
}
