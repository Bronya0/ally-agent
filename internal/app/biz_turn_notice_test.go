// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package app

import (
	"strings"
	"testing"
)

func TestPendingTurnNoticesFollowsRootChanges(t *testing.T) {
	cases := []struct {
		name      string
		known     []string
		current   []string
		wantNotes bool
	}{
		{"unchanged", []string{"/a"}, []string{"/a"}, false},
		{"reordered only", []string{"/a", "/b"}, []string{"/b", "/a"}, false},
		{"added", []string{}, []string{"/a"}, true},
		{"removed", []string{"/a"}, nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			known := map[string]string{
				extraRootsTurnNotice.id: extraRootsTurnNotice.state(ConfigState{ExtraRoots: tc.known}),
			}
			pending := pendingTurnNotices(known, ConfigState{ExtraRoots: tc.current})
			if got := len(pending) == 1; got != tc.wantNotes {
				t.Fatalf("pending=%d, want notice=%v", len(pending), tc.wantNotes)
			}
		})
	}
}

func TestTurnNoticeRoundTrip(t *testing.T) {
	cfg := ConfigState{ExtraRoots: []string{"/b", "/a"}}
	msg := turnNoticeMessage(extraRootsTurnNotice, cfg)
	id, payload, ok := parseTurnNotice(msg)
	if !ok || id != extraRootsTurnNotice.id {
		t.Fatalf("parse failed: id=%q ok=%v", id, ok)
	}
	if payload != extraRootsTurnNotice.state(cfg) {
		t.Fatalf("payload drifted: %q", payload)
	}
	if !strings.Contains(msg.Content, "- /a") || !strings.Contains(msg.Content, "- /b") {
		t.Fatalf("notice body misses roots: %q", msg.Content)
	}
}
