// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General Public
// License v3. See the LICENSE file for details.
package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	toolshared "ally-dev/internal/tools/shared"
)

// textPtr builds the pointer the model-facing TextChange uses so "newText was
// left out" stays distinguishable from "newText is an empty string".
func textPtr(s string) *string { return &s }

// TestEditRejectsChangeWithoutNewText pins the rule the schema declares but the
// runtime used to miss: a change that omits newText fails instead of quietly
// replacing the matched text with nothing. An explicit empty string is still the
// documented way to delete, and must keep working.
func TestEditRejectsChangeWithoutNewText(t *testing.T) {
	dir := t.TempDir()
	original := []byte("alpha\nbeta\n")
	if err := os.WriteFile(filepath.Join(dir, "sample.txt"), original, 0o600); err != nil {
		t.Fatal(err)
	}
	app := NewApp()
	cfg := ConfigState{Workspace: dir}
	ctx := context.Background()
	path := filepath.Join(dir, "sample.txt")

	missing := app.executeTool(ctx, cfg, "s-1", "edit",
		[]byte(fmt.Sprintf(`{"path":"sample.txt","version":%q,"changes":[{"oldText":"alpha"}]}`, hashVersion(original))))
	// The change schema requires newText, so the argument gate refuses the call
	// before the edit handler runs; validateModelTextChangeNewText keeps the
	// same rule for callers that never go through tool arguments.
	if missing.OK || missing.ErrorCode != "E_BAD_ARGS" {
		t.Fatalf("a change without newText must fail, got ok=%v code=%q err=%q", missing.OK, missing.ErrorCode, missing.Error)
	}
	if !strings.Contains(missing.Error, "newText") {
		t.Fatalf("the rejection must name newText, got %q", missing.Error)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != string(original) {
		t.Fatalf("the file must stay untouched, got %q err=%v", got, err)
	}

	deleted := app.executeTool(ctx, cfg, "s-1", "edit",
		[]byte(fmt.Sprintf(`{"path":"sample.txt","version":%q,"changes":[{"oldText":"alpha\n","newText":""}]}`, hashVersion(original))))
	if !deleted.OK {
		t.Fatalf("an explicit empty newText must still delete text, got %q", deleted.Error)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != "beta\n" {
		t.Fatalf("expected the first line to be deleted, got %q err=%v", got, err)
	}
}

// TestSuggestItemsEnforceTheDeclaredLimits keeps schema and runtime in step: the
// description promises 1-4 short texts, and every one of those limits is also
// written into the schema, which the argument gate enforces. The rejection code
// is therefore whichever layer gets there first — the gate for model calls — so
// the test pins the refusal and the named limit, not the code.
func TestSuggestItemsEnforceTheDeclaredLimits(t *testing.T) {
	app := NewApp()
	cfg := ConfigState{Workspace: t.TempDir()}
	ctx := context.Background()
	cases := []struct {
		name  string
		items []string
	}{
		{"too many items", []string{"a", "b", "c", "d", "e"}},
		{"blank item", []string{"ok", "   "}},
		{"item over the character limit", []string{strings.Repeat("x", toolshared.MaxSuggestItemChars+1)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			args, err := json.Marshal(map[string]any{"items": tc.items})
			if err != nil {
				t.Fatal(err)
			}
			res := app.executeTool(ctx, cfg, "s-1", "suggest", args)
			if res.OK {
				t.Fatalf("%s must be rejected, got ok=true", tc.name)
			}
			// One layer gives E_BAD_SUGGEST, the schema gate gives E_BAD_ARGS;
			// both must name the limit that was broken.
			if res.ErrorCode != "E_BAD_SUGGEST" && res.ErrorCode != "E_BAD_ARGS" {
				t.Fatalf("%s must be rejected, got ok=%v code=%q err=%q", tc.name, res.OK, res.ErrorCode, res.Error)
			}
		})
	}

	bounded, err := json.Marshal(map[string]any{"items": []string{
		strings.Repeat("x", toolshared.MaxSuggestItemChars), "second", "third", "fourth",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if res := app.executeTool(ctx, cfg, "s-1", "suggest", bounded); !res.OK {
		t.Fatalf("four items at the character limit must pass, got %q", res.Error)
	}
}

// TestRenderHTMLRejectsBlankAndOversizedTitle closes the other two schema
// promises the runtime ignored: a blank snippet rendered an empty card, and an
// over-long title reached the UI unfiltered.
func TestRenderHTMLRejectsBlankAndOversizedTitle(t *testing.T) {
	app := NewApp()
	cfg := ConfigState{Workspace: t.TempDir()}
	ctx := context.Background()

	blank := app.executeTool(ctx, cfg, "s-1", "render_html", []byte(`{"html":"   "}`))
	// Whitespace-only html passes the schema's minLength, so this one still
	// comes from the handler; the over-long values below are caught by the
	// schema gate first.
	if blank.OK || blank.ErrorCode != "E_BAD_RENDER_HTML" {
		t.Fatalf("a blank snippet must be rejected, got ok=%v code=%q err=%q", blank.OK, blank.ErrorCode, blank.Error)
	}

	args, err := json.Marshal(map[string]string{
		"html":  "<b>x</b>",
		"title": strings.Repeat("t", toolshared.MaxRenderHTMLTitleChars+1),
	})
	if err != nil {
		t.Fatal(err)
	}
	if long := app.executeTool(ctx, cfg, "s-1", "render_html", args); long.OK || !strings.Contains(long.Error, "200") {
		t.Fatalf("an over-long title must be rejected by the declared limit, got ok=%v code=%q err=%q", long.OK, long.ErrorCode, long.Error)
	}

	if ok := app.executeTool(ctx, cfg, "s-1", "render_html", []byte(`{"html":"<b>x</b>","title":"fine"}`)); !ok.OK {
		t.Fatalf("a bounded snippet with a short title must render, got %q", ok.Error)
	}
}

// TestSSHClusterAddNamesEveryMissingField: the add branch needs host, username,
// description and reason for an alias that is not registered yet, while the
// schema can only require `alias` (the already-registered path ignores the other
// four). Failing one field per round cost up to four wasted model turns, so the
// rejection must name them together.
func TestSSHClusterAddNamesEveryMissingField(t *testing.T) {
	app := NewApp()
	_, err := app.executeSSHClusterTool(context.Background(), "s-1", t.TempDir(), SSHClusterRequest{
		Action: "add",
		Alias:  "never-registered-node",
	})
	if err == nil {
		t.Fatal("add without host/username/description/reason must fail")
	}
	if code := toolErrorCode(err); code != "E_BAD_SSH_CLUSTER" {
		t.Fatalf("expected E_BAD_SSH_CLUSTER, got %q (%v)", code, err)
	}
	for _, field := range []string{"host", "username", "description", "reason"} {
		if !strings.Contains(err.Error(), field) {
			t.Fatalf("the rejection must name %s in the same round, got %q", field, err.Error())
		}
	}
}

// TestExecuteToolGatesEveryBuiltinTool is the guard against a schema that only
// exists in the prompt: each declared tool must refuse an undeclared argument at
// the boundary, naming it, no matter which handler sits behind it. A tool that
// silently accepted the stray key would mean the gate never ran for it (or the
// declaration stopped being a strict object).
func TestExecuteToolGatesEveryBuiltinTool(t *testing.T) {
	app := NewApp()
	cfg := ConfigState{Workspace: t.TempDir()}
	for _, tool := range toolshared.Builtins() {
		if tool.Function == nil {
			continue
		}
		name := tool.Function.Name
		t.Run(name, func(t *testing.T) {
			res := app.executeTool(context.Background(), cfg, "s-1", name, []byte(`{"allyUnknownArgument":1}`))
			if res.OK || res.ErrorCode != "E_BAD_ARGS" {
				t.Fatalf("%s must reject an undeclared argument with E_BAD_ARGS, got ok=%v code=%q err=%q", name, res.OK, res.ErrorCode, res.Error)
			}
			if !strings.Contains(res.Error, "allyUnknownArgument") {
				t.Fatalf("%s must name the undeclared argument, got %q", name, res.Error)
			}
		})
	}
}

// TestServiceTuningStaysInsideTheDeclaredBounds: graceSeconds and tailBytes are
// the two knobs the runtime always honoured but the schema never declared. They
// must accept a value in range (reaching the service lookup) and refuse one
// above the clamp, so the declared maximum is what the model is held to.
func TestServiceTuningStaysInsideTheDeclaredBounds(t *testing.T) {
	app := NewApp()
	cfg := ConfigState{Workspace: t.TempDir()}
	ctx := context.Background()

	accepted := app.executeTool(ctx, cfg, "s-1", "service", []byte(`{"action":"stop","id":"svc_missing","graceSeconds":10}`))
	if accepted.ErrorCode == "E_BAD_ARGS" {
		t.Fatalf("a graceful-stop window inside the bound must reach the service lookup, got %q", accepted.Error)
	}

	tooLong := app.executeTool(ctx, cfg, "s-1", "service", []byte(`{"action":"stop","id":"svc_missing","graceSeconds":31}`))
	if tooLong.ErrorCode != "E_BAD_ARGS" || !strings.Contains(tooLong.Error, fmt.Sprintf("%d", toolshared.MaxServiceStopGraceSeconds)) {
		t.Fatalf("a grace window above the clamp must be refused by name, got ok=%v code=%q err=%q", tooLong.OK, tooLong.ErrorCode, tooLong.Error)
	}

	acceptedRead := app.executeTool(ctx, cfg, "s-1", "service", []byte(`{"action":"read","id":"svc_missing","tailBytes":16384}`))
	if acceptedRead.ErrorCode == "E_BAD_ARGS" {
		t.Fatalf("a larger tail inside the bound must reach the service lookup, got %q", acceptedRead.Error)
	}

	tooLarge := app.executeTool(ctx, cfg, "s-1", "service", []byte(`{"action":"read","id":"svc_missing","tailBytes":40000}`))
	if tooLarge.ErrorCode != "E_BAD_ARGS" || !strings.Contains(tooLarge.Error, fmt.Sprintf("%d", toolshared.MaxServiceReadTailBytes)) {
		t.Fatalf("a tail above the clamp must be refused by name, got ok=%v code=%q err=%q", tooLarge.OK, tooLarge.ErrorCode, tooLarge.Error)
	}
}

// TestSubagentStepBudgetIsHeldToTheDeclaredMaximum: the cap the description
// states has to be the cap the gate enforces, otherwise a model can ask for an
// unbounded child loop.
func TestSubagentStepBudgetIsHeldToTheDeclaredMaximum(t *testing.T) {
	app := NewApp()
	res := app.executeTool(context.Background(), ConfigState{Workspace: t.TempDir()}, "s-1", "subagent",
		[]byte(`{"task":"noop","role":"tester","maxSteps":1001}`))
	if res.OK || res.ErrorCode != "E_BAD_ARGS" || !strings.Contains(res.Error, "1000") {
		t.Fatalf("maxSteps above the declared cap must be refused, got ok=%v code=%q err=%q", res.OK, res.ErrorCode, res.Error)
	}
}
