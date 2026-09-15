// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.

package toolcall

import "testing"

func TestCallIDFoundryPassesFirstSeenIDsThrough(t *testing.T) {
	foundry := NewCallIDFoundry()
	id, synthetic := foundry.Claim("call_1")
	if id != "call_1" || synthetic {
		t.Fatalf("first sight of an id must pass through, got id=%q synthetic=%v", id, synthetic)
	}
	if remaps := foundry.Remaps(); len(remaps) != 0 {
		t.Fatalf("no rewrite expected, got %#v", remaps)
	}
}

func TestCallIDFoundryRewritesDuplicatesWithinOneResponse(t *testing.T) {
	foundry := NewCallIDFoundry()
	if id, _ := foundry.Claim("call_0"); id != "call_0" {
		t.Fatalf("first claim = %q, want call_0", id)
	}
	if id, _ := foundry.Claim("call_0"); id != "call_0__2" {
		t.Fatalf("second claim = %q, want call_0__2", id)
	}
	if id, _ := foundry.Claim("call_0"); id != "call_0__3" {
		t.Fatalf("third claim = %q, want call_0__3", id)
	}
	want := []CallIDRemap{
		{Raw: "call_0", Assigned: "call_0__2"},
		{Raw: "call_0", Assigned: "call_0__3"},
	}
	got := foundry.Remaps()
	if len(got) != len(want) {
		t.Fatalf("remaps = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("remap %d = %#v, want %#v", i, got[i], want[i])
		}
	}
}

func TestCallIDFoundryRewritesIDsTheConversationUses(t *testing.T) {
	// A relay that numbers ids per response re-sends "call_0" in every response;
	// the ids already in the history are what makes that a duplicate.
	foundry := NewCallIDFoundry()
	foundry.Seed("call_0", "   ", "")
	// Re-seeding is idempotent and harmless.
	foundry.Seed("call_0")
	if id, synthetic := foundry.Claim("call_0"); id != "call_0__2" || synthetic {
		t.Fatalf("a seeded id must be rewritten on first claim, got id=%q synthetic=%v", id, synthetic)
	}
}

func TestCallIDFoundrySkipsTakenSuffixes(t *testing.T) {
	foundry := NewCallIDFoundry()
	foundry.Seed("call_0__2")
	if id, _ := foundry.Claim("call_0"); id != "call_0" {
		t.Fatalf("first claim = %q, want call_0", id)
	}
	if id, _ := foundry.Claim("call_0"); id != "call_0__3" {
		t.Fatalf("the taken suffix must be skipped, got %q, want call_0__3", id)
	}
}

func TestCallIDFoundryMintsMissingIDs(t *testing.T) {
	foundry := NewCallIDFoundry()
	foundry.Seed("call_1")
	id, synthetic := foundry.Claim("")
	if id != "call_2" || !synthetic {
		t.Fatalf("minting must skip a seeded id, got id=%q synthetic=%v", id, synthetic)
	}
	if next, synthetic := foundry.Claim(""); next != "call_3" || !synthetic {
		t.Fatalf("minted ids must not repeat, got id=%q synthetic=%v", next, synthetic)
	}
	// A provider id that collides with a minted one is a real duplicate: it is
	// rewritten, and it is not reported as synthetic.
	if id, synthetic := foundry.Claim("call_2"); id != "call_2__2" || synthetic {
		t.Fatalf("a provider id equal to a minted one must be rewritten, got id=%q synthetic=%v", id, synthetic)
	}
}
