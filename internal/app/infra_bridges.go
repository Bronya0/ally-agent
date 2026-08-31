// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package app

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	calculatetool "ally-dev/internal/tools/calculate"
	"ally-dev/internal/tools/grep"
	"ally-dev/internal/tools/pathutil"
	"ally-dev/internal/tools/read"
)

type CalculateRequest = calculatetool.Request
type CalculateResult = calculatetool.Result

// Grep type aliases keep the historical app-package names in the Wails
// bindings while the implementations live in internal/tools/grep.
type GrepRequest = grep.Request
type GrepFileMatch = grep.LineFileMatch
type GrepFileCount = grep.FileCount
type GrepResult = grep.Result

const (
	apiFormatOpenAIChat           = "openai_chat"
	apiFormatOpenAIResponses      = "openai_responses"
	apiFormatAnthropicMessages    = "anthropic_messages"
	defaultOpenAIResponsesURL     = "https://api.openai.com/v1"
	defaultAnthropicMessagesURL   = "https://api.anthropic.com"
	tokenParamAuto                = "auto"
	tokenParamMaxTokens           = "max_tokens"
	tokenParamMaxCompletionTokens = "max_completion_tokens"
	reasoningEffortAuto           = "auto"
	reasoningEffortLow            = "low"
	reasoningEffortMedium         = "medium"
	reasoningEffortHigh           = "high"
	reasoningEffortXHigh          = "xhigh"
	reasoningEffortMax            = "max"
)

// normalizeReasoningEffort accepts any supported spelling (with case, dash,
// space or underscore separators) and returns the canonical lowercase level.
// Unknown values fall back to "auto" so a stale or mistyped config never
// injects an unsupported parameter into a request.
func normalizeReasoningEffort(value string) string {
	v := strings.ToLower(strings.TrimSpace(value))
	v = strings.NewReplacer("-", "", "_", "", " ", "").Replace(v)
	switch v {
	case "auto", "default", "unset", "", "off":
		return reasoningEffortAuto
	case "low":
		return reasoningEffortLow
	case "medium", "med":
		return reasoningEffortMedium
	case "high":
		return reasoningEffortHigh
	case "xhigh", "extrahigh", "extremehigh":
		return reasoningEffortXHigh
	case "max", "maximum", "maximal":
		return reasoningEffortMax
	default:
		return reasoningEffortAuto
	}
}

// reasoningEffortForAdapter returns the normalized effort level to send for a
// provider adapter, or "" when nothing should be sent (auto). The selected
// supported level is preserved unchanged, including xhigh and max; the
// provider is responsible for rejecting a level it does not support.
func reasoningEffortForAdapter(_ string, effort string) string {
	effort = normalizeReasoningEffort(effort)
	if effort == reasoningEffortAuto {
		return ""
	}
	return effort
}

func normalizeTokenParam(value string) string {
	v := strings.ToLower(strings.TrimSpace(value))
	v = strings.NewReplacer("-", "_", " ", "_").Replace(v)
	switch v {
	case tokenParamMaxCompletionTokens, "max_completion_token", "completion_tokens", "completion":
		return tokenParamMaxCompletionTokens
	case tokenParamMaxTokens, "max_token", "tokens", "legacy":
		return tokenParamMaxTokens
	default:
		return tokenParamAuto
	}
}

func normalizeAPIFormat(value string) string {
	v := strings.ToLower(strings.TrimSpace(value))
	v = strings.NewReplacer("-", "_", " ", "_").Replace(v)
	switch v {
	case "", "openai", "openai_compatible", "openai_chat", "chat", "chat_completions", "chat_completion":
		return apiFormatOpenAIChat
	case "openai_responses", "responses", "response":
		return apiFormatOpenAIResponses
	case "anthropic", "anthropic_messages", "claude", "claude_messages", "messages":
		return apiFormatAnthropicMessages
	default:
		return apiFormatOpenAIChat
	}
}

func defaultBaseURLForAPIFormat(format string) string {
	switch normalizeAPIFormat(format) {
	case apiFormatOpenAIResponses:
		return defaultOpenAIResponsesURL
	case apiFormatAnthropicMessages:
		return defaultAnthropicMessagesURL
	default:
		return defaultBaseURL
	}
}

func baseURLForAPIFormat(cfg ConfigState) string {
	base := strings.TrimSpace(cfg.BaseURL)
	if base == "" {
		base = defaultBaseURLForAPIFormat(cfg.APIFormat)
	}
	base = strings.TrimRight(base, "/")
	if normalizeAPIFormat(cfg.APIFormat) == apiFormatAnthropicMessages && strings.HasSuffix(strings.ToLower(base), "/v1") {
		base = base[:len(base)-3]
	}
	return base
}

func defaultMaxTokensForAPIFormat(format string) int {
	// Unified fallback for every API format: one number, no per-format
	// branching. 131072 (128K) covers current catalog entries; relay
	// endpoints typically clamp server-side.
	return 131072
}

func calculateExpression(req CalculateRequest) (CalculateResult, error) {
	return calculatetool.Evaluate(req)
}

// ── Shared infra helpers ─────────────────────────────────────

// limitedBuffer is a concurrency-safe byte buffer that keeps at most limit
// bytes and records truncation. Used for command output capture and login
// shell env probing.
type limitedBuffer struct {
	mu        sync.Mutex
	buf       bytes.Buffer
	limit     int
	truncated bool
	// onTruncate, when set, is invoked exactly once (under mu) on the first
	// overflow with the content buffered so far. command uses it to
	// lazily create a full-output spill file only when truncation happens.
	onTruncate    func(prefix []byte)
	truncatedOnce sync.Once
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.limit <= 0 {
		return len(p), nil
	}
	remaining := b.limit - b.buf.Len()
	if remaining <= 0 {
		b.truncated = true
		b.noteTruncate()
		return len(p), nil
	}
	if len(p) > remaining {
		_, _ = b.buf.Write(p[:remaining])
		b.truncated = true
		b.noteTruncate()
		return len(p), nil
	}
	_, _ = b.buf.Write(p)
	return len(p), nil
}

// noteTruncate fires the onTruncate callback once on the first overflow. It
// must be called with b.mu held; the callback receives the buffered prefix so
// a spill sink can capture the complete output even though the buffer drops
// the overflowing tail.
func (b *limitedBuffer) noteTruncate() {
	b.truncatedOnce.Do(func() {
		if b.onTruncate != nil {
			b.onTruncate(b.buf.Bytes())
		}
	})
}

func (b *limitedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// Len returns the current buffered length without copying. Callers that only
// need to know whether the buffer grew (e.g. the command streaming ticker)
// can use this instead of String(), which would allocate a full copy under the
// lock on every 120ms tick — even when nothing changed.
func (b *limitedBuffer) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Len()
}

// TailString returns the last min(n, buf.Len()) bytes as a string. Used by the
// command streaming ticker to avoid emitting the full buffer (up to 128KB)
// on every 120ms tick — only the tail is sent during streaming, the complete
// output is delivered in the final CommandResult. This cuts IPC payload ~8x
// for long builds that fill the buffer.
func (b *limitedBuffer) TailString(n int) string {
	if n <= 0 {
		return ""
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	p := b.buf.Bytes()
	if len(p) <= n {
		return string(p)
	}
	return string(p[len(p)-n:])
}

// ── Path / content-hash thin wrappers ────────────────────────

func evalExistingPrefix(target string) (string, error) {
	return read.EvalExistingPrefix(target)
}

// insideWriteRoot 判断 target（已解析）是否落在任一可写 root 内。
// 对每个 root 都做 insideRoot 检查 + EvalSymlinks 解析；任一通过即放行。
// ~/.ally_agent 始终作为兜底白名单。
func insideWriteRoot(roots []string, target string) bool {
	return pathutil.InsideWriteRoot(pathRuntime, roots, target)
}

func requireExistingDirectory(path string, missingCode string) error {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return codedToolError(missingCode, fmt.Errorf("parent directory does not exist: %s", path))
		}
		return err
	}
	if !info.IsDir() {
		return codedToolError("E_PARENT_NOT_DIRECTORY", fmt.Errorf("parent path is not a directory: %s", path))
	}
	return nil
}

func resolveReadPath(cfg ConfigState, p string) (string, error) {
	return resolveReadablePath(cfg, p)
}

func resolveReadablePath(cfg ConfigState, p string) (string, error) {
	root, err := workspaceRoot(cfg)
	if err != nil {
		return "", err
	}
	return pathutil.ResolveReadable(pathRuntime, []string{root}, p)
}

func insideRoot(root, target string) bool {
	return pathutil.InsideRoot(root, target)
}

func insideAllyAgentDir(target string) bool {
	return pathutil.InsideAllyAgentDir(pathRuntime, target)
}

func samePath(a, b string) bool {
	return pathutil.SamePath(a, b)
}

// File IO and content-hashing helpers live in internal/tools/read. These
// thin wrappers preserve the legacy lowercase names used throughout the app
// package without duplicating the implementations.
func readTextFile(path string) ([]byte, os.FileInfo, error) {
	return read.ReadTextFile(path)
}

func normalizeText(data []byte) (string, string, bool) {
	return read.NormalizeText(data)
}

func encodeText(text, ending string, hadBOM bool) []byte {
	return read.EncodeText(text, ending, hadBOM)
}

func splitLines(text string) ([]string, bool) {
	return read.SplitLines(text)
}

func safeWriteFile(path string, data []byte, perm os.FileMode) error {
	return read.SafeWriteFile(path, data, perm)
}

func safeWriteFileWithDir(path string, data []byte, perm os.FileMode, mkdirs bool) error {
	return read.SafeWriteFileWithDir(path, data, perm, mkdirs)
}

func safeWriteNewFile(path string, data []byte, perm os.FileMode) error {
	return read.SafeWriteNewFile(path, data, perm)
}

func safeWritePreparedFile(path string, data []byte, perm os.FileMode) error {
	return read.SafeWritePreparedFile(path, data, perm)
}

func modeOf(path string) os.FileMode {
	return read.ModeOf(path)
}

func hashBytes(data []byte) string {
	return read.HashBytes(data)
}

func hashVersion(data []byte) string {
	return read.HashVersion(data)
}

// hashBytesAndVersion hashes data once and returns both the hex digest and the
// version token. Replaces the previous hashBytes + hashVersion pair on the read
// hot path, where a 10 MB file was being SHA-256'd twice per call.
func hashBytesAndVersion(data []byte) (string, string) {
	return read.HashBytesAndVersion(data)
}

func versionFromSHA256Hex(value string) (string, error) {
	sum, err := hex.DecodeString(value)
	if err != nil || len(sum) != sha256.Size {
		return "", errors.New("invalid SHA-256 digest")
	}
	return read.VersionFromSHA256(sum), nil
}

func hashFileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func newID() string {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf)
}
