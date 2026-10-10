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
	"path/filepath"
	"sort"
	"strings"

	openai "github.com/sashabaranov/go-openai"
)

// turnNoticeKind is one kind of session state the model must hear about when it
// changes mid-session. The system prompt and workspace map are frozen per
// session for prompt-cache stability, so a change cannot go there; the first
// user turn after the change carries a notice instead. Adding a new kind is one
// entry in turnNoticeKinds and nothing else.
type turnNoticeKind struct {
	// id names the kind inside the notice marker. Keep it stable once written
	// into history.
	id string
	// state returns the canonical single-line serialization of the state. Equal
	// states must give equal strings, so normalize and sort inside.
	state func(cfg ConfigState) string
	// body is the human-readable text that follows the marker line.
	body func(cfg ConfigState) string
}

// turnNoticeKinds is the single registry of injectable session state.
var turnNoticeKinds = []turnNoticeKind{
	extraRootsTurnNotice,
}

// turnNoticePrefix opens every notice; the kind id and a space follow it, then
// the state payload on the first line.
const turnNoticePrefix = "[ally-notice:"

func turnNoticeMarker(id string) string { return turnNoticePrefix + id + "] " }

// turnNoticeBaseline snapshots every kind's state at the moment the system
// prompt is frozen, i.e. what the model was told there. It is stored alongside
// the frozen prompt and dropped with it (refreshSessionPromptPrefix).
func turnNoticeBaseline(cfg ConfigState) map[string]string {
	out := make(map[string]string, len(turnNoticeKinds))
	for _, k := range turnNoticeKinds {
		out[k.id] = k.state(cfg)
	}
	return out
}

// knownTurnNotices returns the state the model last saw, per kind: the latest
// notice in history wins, the frozen baseline fills the rest. A kind missing
// from the result means the model has no record of it, so nothing is corrected.
func (a *App) knownTurnNotices(sessionID string, history []openai.ChatCompletionMessage) map[string]string {
	known := map[string]string{}
	for i := len(history) - 1; i >= 0; i-- {
		if id, payload, ok := parseTurnNotice(history[i]); ok {
			if _, seen := known[id]; !seen {
				known[id] = payload
			}
		}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for id, payload := range a.frozenTurnNotices[sessionID] {
		if _, seen := known[id]; !seen {
			known[id] = payload
		}
	}
	return known
}

// pendingTurnNotices is the pure decision: the kinds whose current state differs
// from what the model last saw.
func pendingTurnNotices(known map[string]string, cfg ConfigState) []turnNoticeKind {
	var out []turnNoticeKind
	for _, k := range turnNoticeKinds {
		prev, ok := known[k.id]
		if !ok {
			continue
		}
		if k.state(cfg) != prev {
			out = append(out, k)
		}
	}
	return out
}

// withTurnNotices inserts the pending notices right before the newest user
// message of this turn (messages[turnStart:]). They are appended to history, so
// only bytes at the tail change and the existing prompt prefix stays identical.
func (a *App) withTurnNotices(sessionID string, cfg ConfigState, history, messages []openai.ChatCompletionMessage, turnStart int) []openai.ChatCompletionMessage {
	if strings.TrimSpace(sessionID) == "" {
		return messages
	}
	at := -1
	for i := len(messages) - 1; i >= turnStart && i >= 0; i-- {
		if messages[i].Role == openai.ChatMessageRoleUser {
			at = i
			break
		}
	}
	if at < 0 {
		return messages
	}
	pending := pendingTurnNotices(a.knownTurnNotices(sessionID, history), cfg)
	if len(pending) == 0 {
		return messages
	}
	out := make([]openai.ChatCompletionMessage, 0, len(messages)+len(pending))
	out = append(out, messages[:at]...)
	for _, k := range pending {
		out = append(out, turnNoticeMessage(k, cfg))
	}
	return append(out, messages[at:]...)
}

func turnNoticeMessage(k turnNoticeKind, cfg ConfigState) openai.ChatCompletionMessage {
	return openai.ChatCompletionMessage{
		Role:    openai.ChatMessageRoleUser,
		Content: turnNoticeMarker(k.id) + k.state(cfg) + "\n" + k.body(cfg),
	}
}

// parseTurnNotice reads the kind id and state payload back out of a notice.
func parseTurnNotice(m openai.ChatCompletionMessage) (id, payload string, ok bool) {
	if m.Role != openai.ChatMessageRoleUser || !strings.HasPrefix(m.Content, turnNoticePrefix) {
		return "", "", false
	}
	rest := m.Content[len(turnNoticePrefix):]
	end := strings.Index(rest, "] ")
	if end < 0 {
		return "", "", false
	}
	line := rest[end+2:]
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = line[:i]
	}
	return rest[:end], line, true
}

func normalizeExtraRoots(roots []string) []string {
	out := make([]string, 0, len(roots))
	for _, r := range roots {
		out = append(out, filepath.ToSlash(filepath.Clean(r)))
	}
	return out
}

// extraRootsTurnNotice tells the model when the session's extra roots change
// (added or removed in the composer panel).
var extraRootsTurnNotice = turnNoticeKind{
	id: "extra-roots",
	state: func(cfg ConfigState) string {
		roots := normalizeExtraRoots(cfg.ExtraRoots)
		sort.Strings(roots)
		b, _ := json.Marshal(struct {
			// No omitempty: an empty list must serialize as [] so "no extra
			// roots" is a real state, not a missing field.
			Roots []string `json:"roots"`
		}{Roots: roots})
		return string(b)
	},
	body: func(cfg ConfigState) string {
		var b strings.Builder
		b.WriteString("The user changed the session extra roots. These are the write roots beyond the primary workspace that are in effect now. They replace the list in the \"Session Extra Roots\" section of the system prompt; an empty list means no extra roots are active:\n")
		roots := normalizeExtraRoots(cfg.ExtraRoots)
		if len(roots) == 0 {
			b.WriteString("(none)\n")
		}
		for _, r := range roots {
			b.WriteString("- " + r + "\n")
		}
		return b.String()
	},
}
