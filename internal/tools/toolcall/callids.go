// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.

package toolcall

import (
	"strconv"
	"strings"
)

// CallIDRemap records one provider-sent id that had to be rewritten because the
// conversation already used it.
type CallIDRemap struct {
	Raw      string
	Assigned string
}

// CallIDFoundry hands out the tool-call ids a streamed response will send back to
// the provider, and guarantees they are unique for the whole conversation: a
// request whose assistant messages reuse one tool_call id is rejected outright,
// and the tool result of the second call can never be paired.
//
// The duplicate is field behavior, not a hypothetical: a relay that numbers its
// calls per response sends "call_0" again in the second response of a session
// while the first "call_0" still sits in the history. kimi draws the same line
// with ToolCallIdNormalizer (toolCallIdNormalizer.ts): the first sight of an id
// passes through, a repeat becomes "id__2", "id__3", ….
//
// Handing out ids is deterministic — seed the conversation, then claim in stream
// order — so the same response always produces the same ids and a retried round
// does not drift.
type CallIDFoundry struct {
	seen   map[string]struct{}
	byRaw  map[string]int
	minted int
	remaps []CallIDRemap
}

// NewCallIDFoundry returns a foundry that has claimed nothing yet.
func NewCallIDFoundry() *CallIDFoundry {
	return &CallIDFoundry{seen: map[string]struct{}{}, byRaw: map[string]int{}}
}

// Seed marks ids the conversation already uses (the history about to be sent), so
// a response that re-sends one of them is rewritten on first sight. Empty and
// whitespace-only ids are ignored.
func (f *CallIDFoundry) Seed(ids ...string) {
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id != "" {
			f.seen[id] = struct{}{}
		}
	}
}

// Claim returns the id for a fragment that starts a tool call. An id the
// conversation already uses is rewritten to "<id>__<n>"; a fragment that carries
// no id at all gets a minted one, because a call without an id can neither be
// sent to the provider nor paired with its result. synthetic reports the minted
// case: the provider's own id may still arrive later, and the caller may adopt it.
func (f *CallIDFoundry) Claim(rawID string) (id string, synthetic bool) {
	raw := strings.TrimSpace(rawID)
	if raw == "" {
		id := f.mint()
		f.seen[id] = struct{}{}
		return id, true
	}
	total := f.byRaw[raw] + 1
	f.byRaw[raw] = total
	if total == 1 {
		if _, taken := f.seen[raw]; !taken {
			f.seen[raw] = struct{}{}
			return raw, false
		}
	}
	// The suffix is the occurrence number (the second sight becomes "__2"), and a
	// seeded id whose first sight is already taken is the second occurrence.
	suffix := total
	if suffix < 2 {
		suffix = 2
	}
	assigned := f.uniqueID(raw, suffix)
	f.seen[assigned] = struct{}{}
	f.remaps = append(f.remaps, CallIDRemap{Raw: raw, Assigned: assigned})
	return assigned, false
}

// Remaps lists the ids this foundry had to rewrite, in claim order. Callers log
// them: a rewrite means the provider or relay reused an id.
func (f *CallIDFoundry) Remaps() []CallIDRemap {
	if len(f.remaps) == 0 {
		return nil
	}
	out := make([]CallIDRemap, len(f.remaps))
	copy(out, f.remaps)
	return out
}

// uniqueID returns "<raw>__<n>", bumping n until the conversation does not use it.
// The suffix keeps the id readable and stays inside the charset all three
// protocols accept (^[a-zA-Z0-9_-]{1,64}$).
func (f *CallIDFoundry) uniqueID(raw string, n int) string {
	for {
		candidate := raw + "__" + strconv.Itoa(n)
		if _, taken := f.seen[candidate]; !taken {
			return candidate
		}
		n++
	}
}

// mint returns the next unused "call_<n>". A seeded id of the same shape is
// skipped rather than handed out twice.
func (f *CallIDFoundry) mint() string {
	for {
		f.minted++
		id := "call_" + strconv.Itoa(f.minted)
		if _, taken := f.seen[id]; !taken {
			return id
		}
	}
}
