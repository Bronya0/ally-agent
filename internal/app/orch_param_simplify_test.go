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

// TestFillAskIDsFillsOnlyWhatTheModelOmitted pins the ask contract after ids
// left the model-facing schema: ids are plumbing (the UI echoes them back, the
// answer resolver matches on them), so a missing one is filled in, a supplied one
// is never touched, and a generated one never collides with a supplied one.
func TestFillAskIDsFillsOnlyWhatTheModelOmitted(t *testing.T) {
	req := AskRequest{Questions: []AskQuestion{
		{
			Question: "first",
			Options:  []AskOption{{Label: "a", Description: "da"}, {Label: "b", Description: "db"}},
		},
		{
			ID:       "q1",
			Question: "second",
			Options: []AskOption{
				{ID: "o1", Label: "a", Description: "da"},
				{Label: "b", Description: "db"},
			},
		},
	}}

	filled := fillAskIDs(req)

	if filled.Questions[0].ID != "q1_2" {
		t.Fatalf("a generated question id must avoid the supplied %q, got %q", "q1", filled.Questions[0].ID)
	}
	if filled.Questions[1].ID != "q1" {
		t.Fatalf("a supplied question id must survive, got %q", filled.Questions[1].ID)
	}
	if filled.Questions[0].Options[0].ID == "" || filled.Questions[0].Options[1].ID == "" {
		t.Fatalf("every option needs an id, got %#v", filled.Questions[0].Options)
	}
	if filled.Questions[1].Options[0].ID != "o1" || filled.Questions[1].Options[1].ID != "o2" {
		t.Fatalf("option ids must be filled per question, got %#v", filled.Questions[1].Options)
	}
	// The filled request is what validation runs on, so it has to pass as-is.
	if err := validateAskRequest(filled); err != nil {
		t.Fatalf("a request with filled ids must validate: %v", err)
	}
	// fillAskIDs returns a filled copy: the caller's request must stay untouched,
	// so nothing can accidentally emit a half-filled payload.
	if req.Questions[0].ID != "" || req.Questions[1].Options[1].ID != "" {
		t.Fatalf("fillAskIDs must not mutate its input, got %#v", req.Questions)
	}
	// Ordering is load-bearing: executeAsk fills before validating, because
	// validation itself still rejects a missing id (the internal approval gates
	// rely on that).
	unfilled := AskRequest{Questions: []AskQuestion{{Question: "q", Options: []AskOption{
		{Label: "a", Description: "da"}, {Label: "b", Description: "db"},
	}}}}
	if err := validateAskRequest(unfilled); err == nil {
		t.Fatal("validation must reject a request with no ids, which is why executeAsk fills them first")
	}
}

// TestResolveReadStartLine pins the one place the model-facing tailLines form is
// folded into the negative startLine the preview pipeline understands, so "last N
// lines" keeps a single implementation and a mixed request is rejected in the
// model's own vocabulary.
func TestResolveReadStartLine(t *testing.T) {
	cases := []struct {
		startLine int
		endLine   int
		tailLines int
		want      int
	}{
		{0, 0, 0, 0},
		{0, 0, 200, -200},
		{5, 0, 0, 5},
		{0, 20, 0, 0},
		{0, 0, -3, 0}, // a negative tailLines is not a tail
		{0, 0, maxReadRangeLines + 50, -maxReadRangeLines}, // clamped to the preview ceiling
	}
	for _, tc := range cases {
		got, err := resolveReadStartLine(tc.startLine, tc.endLine, tc.tailLines)
		if err != nil {
			t.Errorf("resolveReadStartLine(%d, %d, %d) failed: %v", tc.startLine, tc.endLine, tc.tailLines, err)
			continue
		}
		if got != tc.want {
			t.Errorf("resolveReadStartLine(%d, %d, %d) = %d, want %d", tc.startLine, tc.endLine, tc.tailLines, got, tc.want)
		}
	}
	// Mixing the forms must fail on a complaint about tailLines: the preview's own
	// wording ("negative startLine ...") names a value the model never sent, and
	// the schema's oneOf is advisory only, so a stray combination does reach here.
	for _, tc := range []struct{ startLine, endLine int }{{5, 0}, {0, 20}} {
		_, err := resolveReadStartLine(tc.startLine, tc.endLine, 200)
		if err == nil {
			t.Fatalf("tailLines combined with startLine=%d endLine=%d must be rejected", tc.startLine, tc.endLine)
		}
		if !strings.Contains(err.Error(), "tailLines") || strings.Contains(err.Error(), "negative startLine") {
			t.Fatalf("rejection must name tailLines, not the internal startLine wording: %v", err)
		}
	}
}
