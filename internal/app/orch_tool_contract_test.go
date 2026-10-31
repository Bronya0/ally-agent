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
	"unicode/utf8"

	toolshared "ally-dev/internal/tools/shared"
)

func TestDisabledBuiltinFollowsInjectedTools(t *testing.T) {
	a := &App{}
	cfg := ConfigState{}
	existing := withBuiltinToolAvailability(context.Background(), a.buildToolsForSession("existing", cfg))
	cfg.DisabledTools = []string{"CALCULATE"}
	fresh := withBuiltinToolAvailability(context.Background(), a.buildToolsForSession("fresh", cfg))
	child := withBuiltinToolAvailability(existing, a.subagentTools(cfg))
	for _, tc := range []struct {
		name    string
		ctx     context.Context
		allowed bool
	}{
		{"existing", existing, true}, {"fresh", fresh, false},
		{"child", child, false}, {"stateless", context.Background(), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := a.executeTool(tc.ctx, cfg, "s", "calculate", []byte(`{"expression":"1+1"}`))
			if result.OK != tc.allowed {
				t.Fatalf("allowed=%v result=%+v", tc.allowed, result)
			}
			if !tc.allowed && (result.ErrorCode != "E_TOOL_DISABLED" || !strings.Contains(result.Error, "已禁用")) {
				t.Fatalf("missing disabled-tool message: %+v", result)
			}
		})
	}
}

// TestEditRejectsChangeWithoutNewText pins the rule the declaration states: a
// change that omits newText fails at the argument gate instead of quietly
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
	// before the edit handler runs.
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

// TestSuggestItemsAreRepairedNotRejected pins what a repairable suggest call
// does now: an item a couple of runes over the declared limit, a blank entry or
// one item too many comes back as a usable chip row (clipped, blanks dropped,
// extras ignored). It used to lose every chip and show the user a raw argument
// error over a character count the model cannot get right.
func TestSuggestItemsAreRepairedNotRejected(t *testing.T) {
	app := NewApp()
	cfg := ConfigState{Workspace: t.TempDir()}
	ctx := context.Background()

	args, err := json.Marshal(map[string]any{"items": []string{
		strings.Repeat("x", toolshared.MaxSuggestItemChars+2),
		"   ",
		"second",
		"third",
		"fourth",
		"fifth",
	}})
	if err != nil {
		t.Fatal(err)
	}
	res := app.executeTool(ctx, cfg, "s-1", "suggest", args)
	if !res.OK {
		t.Fatalf("a repairable suggest call must go through, got ok=%v code=%q err=%q", res.OK, res.ErrorCode, res.Error)
	}
	data, ok := res.Data.(map[string]any)
	if !ok {
		t.Fatalf("unexpected result shape: %#v", res.Data)
	}
	items, _ := data["items"].([]string)
	if len(items) != toolshared.MaxSuggestItems {
		t.Fatalf("the blank and the extra item must be dropped, got %d items: %q", len(items), items)
	}
	longest := items[0]
	if n := utf8.RuneCountInString(longest); n != toolshared.MaxSuggestItemChars {
		t.Fatalf("the over-long item must be clipped to %d runes, got %d (%q)", toolshared.MaxSuggestItemChars, n, longest)
	}
	if !strings.HasSuffix(longest, "…") {
		t.Fatalf("a clipped item must end with an ellipsis, got %q", longest)
	}

	// Nothing usable left is still a failure: a call that would render an empty
	// chip row must not pass as a success.
	if empty := app.executeTool(ctx, cfg, "s-1", "suggest", []byte(`{"items":["  "]}`)); empty.OK {
		t.Fatal("a call with no usable item must fail, got ok=true")
	}
}

// TestRepairAskArgsClipsDisplayText keeps the question card alive when the model
// overshoots a display bound: the label and description are clipped, while ids
// and the other option fields arrive untouched because the answer resolver
// matches on the ids.
func TestRepairAskArgsClipsDisplayText(t *testing.T) {
	raw, err := json.Marshal(map[string]any{
		"questions": []any{map[string]any{
			"question": "pick one",
			"id":       "q1",
			"options": []any{
				map[string]any{"label": strings.Repeat("a", toolshared.MaxAskOptionLabelChars+5), "description": "d", "id": "o1"},
				map[string]any{"label": "b", "description": strings.Repeat("d", toolshared.MaxAskOptionDescriptionChars+5), "id": "o2", "recommended": true},
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	fixed, ok := repairAskArgs(raw)
	if !ok {
		t.Fatal("an over-long ask label must be repaired")
	}
	var decoded struct {
		Questions []struct {
			ID      string `json:"id"`
			Options []struct {
				ID          string `json:"id"`
				Label       string `json:"label"`
				Description string `json:"description"`
				Recommended bool   `json:"recommended"`
			} `json:"options"`
		} `json:"questions"`
	}
	if err := json.Unmarshal(fixed, &decoded); err != nil {
		t.Fatal(err)
	}
	question := decoded.Questions[0]
	if question.ID != "q1" || question.Options[0].ID != "o1" {
		t.Fatalf("ids must survive the repair, got question %q option %q", question.ID, question.Options[0].ID)
	}
	if n := utf8.RuneCountInString(question.Options[0].Label); n != toolshared.MaxAskOptionLabelChars {
		t.Fatalf("the label must be clipped to %d runes, got %d", toolshared.MaxAskOptionLabelChars, n)
	}
	if n := utf8.RuneCountInString(question.Options[1].Description); n != toolshared.MaxAskOptionDescriptionChars {
		t.Fatalf("the description must be clipped to %d runes, got %d", toolshared.MaxAskOptionDescriptionChars, n)
	}
	if !question.Options[1].Recommended {
		t.Fatal("other option fields must arrive untouched")
	}
}

// TestDisplayClippingRunsAfterTypeRepair guards the order inside the gate. A
// stringified payload is only unwrapped by the type repair, so clipping that runs
// first sees a string and clips nothing, leaving the gate to reject the call with
// exactly the error the clipping exists to remove.
func TestDisplayClippingRunsAfterTypeRepair(t *testing.T) {
	longItem := strings.Repeat("x", toolshared.MaxSuggestItemChars+2)
	items, err := json.Marshal(map[string]any{"items": "[\"" + longItem + "\"]"})
	if err != nil {
		t.Fatal(err)
	}
	var suggest struct {
		Items []string `json:"items"`
	}
	if err, _ := decodeToolArguments("suggest", items, &suggest); err != nil {
		t.Fatalf("a stringified items array must be repaired, got %v", err)
	}
	if len(suggest.Items) != 1 || utf8.RuneCountInString(suggest.Items[0]) != toolshared.MaxSuggestItemChars {
		t.Fatalf("expected one clipped item, got %q", suggest.Items)
	}

	inner, err := json.Marshal([]any{map[string]any{"question": "q", "options": []any{
		map[string]any{"label": strings.Repeat("a", toolshared.MaxAskOptionLabelChars+5), "description": "d"},
		map[string]any{"label": "b", "description": "d2"},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	questions, err := json.Marshal(map[string]any{"questions": string(inner)})
	if err != nil {
		t.Fatal(err)
	}
	var ask AskRequest
	if err, _ := decodeToolArguments("ask", questions, &ask); err != nil {
		t.Fatalf("a stringified questions array must be repaired, got %v", err)
	}
	if n := utf8.RuneCountInString(ask.Questions[0].Options[0].Label); n != toolshared.MaxAskOptionLabelChars {
		t.Fatalf("expected the label clipped to %d runes, got %d", toolshared.MaxAskOptionLabelChars, n)
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

func TestServiceStartRequiresDescription(t *testing.T) {
	root := redirectAppStateDir(t)
	app := NewApp()
	cfg := ConfigState{Workspace: root}
	for _, description := range []any{nil, "", " \n\t ", strings.Repeat("中", toolshared.MaxServiceDescriptionChars+1)} {
		args := map[string]any{"action": "start", "command": "echo should-not-start"}
		if description != nil {
			args["description"] = description
		}
		raw, _ := json.Marshal(args)
		result := app.executeTool(context.Background(), cfg, "s", "service", raw)
		if result.OK || result.ErrorCode != "E_BAD_ARGS" || !strings.Contains(result.Error, "description") {
			t.Fatalf("invalid description %v reached process startup: %+v", description, result)
		}
	}
	// Direct callers also enforce the contract; valid Unicode at the maximum
	// length reaches the next (empty command) check rather than being rejected.
	for _, description := range []string{"", " \t ", strings.Repeat("中", toolshared.MaxServiceDescriptionChars+1)} {
		_, err := app.startServiceWithConfig(context.Background(), cfg, StartServiceRequest{Description: description, Command: "echo should-not-start"})
		if toolErrorCode(err) != "E_BAD_ARGS" {
			t.Fatalf("direct start bypassed description validation: %v", err)
		}
	}
	_, err := app.startServiceWithConfig(context.Background(), cfg, StartServiceRequest{Description: strings.Repeat("中", toolshared.MaxServiceDescriptionChars)})
	if toolErrorCode(err) != "E_BAD_COMMAND" {
		t.Fatalf("valid Unicode description rejected: %v", err)
	}
	if len(app.ListServices().Services) != 0 {
		t.Fatal("invalid requests created a service")
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

// TestPlanToolVocabularyPassesTheGate pins the shapes the plan description
// teaches: the steps/finish vocabulary has to clear the strict gate — a gate that
// refused it would burn a model round on every plan call. The retired forms
// (todos, and the `next` parameter that the report replaced) are pinned below as
// gone on purpose, so no compat layer creeps back.
func TestPlanToolVocabularyPassesTheGate(t *testing.T) {
	app := NewApp()
	cfg := ConfigState{Workspace: t.TempDir()}
	ctx := context.Background()

	// The plan is laid out first so the report has a title to resolve against:
	// a report naming a step the plan does not carry is a handler refusal, not a
	// gate refusal, and the two are asserted separately below.
	steps := app.executeTool(ctx, cfg, "s-1", "plan", []byte(`{"steps":["Read code","Run tests"]}`))
	if !steps.OK {
		t.Fatalf("plan steps must pass the gate, got code=%q err=%q", steps.ErrorCode, steps.Error)
	}

	for _, args := range []string{
		`{"finish":"Run tests"}`,
		// A blank value is not a source (the handler reads instead), so the
		// declaration has to let it through instead of failing the gate.
		`{"finish":""}`,
		`{"steps":[]}`,
		`{}`,
	} {
		if res := app.executeTool(ctx, cfg, "s-1", "plan", []byte(args)); !res.OK {
			t.Fatalf("plan %s must pass the gate, got code=%q err=%q", args, res.ErrorCode, res.Error)
		}
	}

	// The retired parameter is not a second spelling: it fails by name instead of
	// being silently accepted, so nothing can quietly depend on it again.
	retired := app.executeTool(ctx, cfg, "s-1", "plan", []byte(`{"next":"Run tests"}`))
	if retired.OK || retired.ErrorCode != "E_BAD_ARGS" || !strings.Contains(retired.Error, "next") {
		t.Fatalf("the retired next parameter must be refused by name, got ok=%v code=%q err=%q", retired.OK, retired.ErrorCode, retired.Error)
	}

	// The retired todos form is not a second spelling either.
	todos := app.executeTool(ctx, cfg, "s-1", "plan", []byte(`{"todos":[{"title":"Read code","status":"in_progress"}]}`))
	if todos.OK || todos.ErrorCode != "E_BAD_ARGS" || !strings.Contains(todos.Error, "todos") {
		t.Fatalf("the retired todos form must be refused by name, got ok=%v code=%q err=%q", todos.OK, todos.ErrorCode, todos.Error)
	}

	// finish takes a step title and nothing else: a boolean, a number and an
	// object are all refused by the gate rather than decoded into a zero value
	// that would report nothing. The boolean shape is the one the tool used to
	// accept as "end the plan where it stands"; it stays refused on purpose, so no
	// compat path creeps back.
	for _, args := range []string{
		`{"finish":true}`,
		`{"finish":false}`,
		`{"finish":3}`,
		`{"finish":{"title":"Run tests"}}`,
		`{"finish":"` + strings.Repeat("x", toolshared.MaxPlanStepTitleChars+1) + `"}`,
	} {
		res := app.executeTool(ctx, cfg, "s-1", "plan", []byte(args))
		if res.OK || res.ErrorCode != "E_BAD_ARGS" {
			t.Fatalf("plan %s must be refused by the gate, got ok=%v code=%q err=%q", args, res.OK, res.ErrorCode, res.Error)
		}
	}

	// Two write sources in one call is a contract rejection from the handler, not
	// a schema violation: the declared properties stay independent.
	both := app.executeTool(ctx, cfg, "s-1", "plan", []byte(`{"steps":["a"],"finish":"a"}`))
	if both.OK || both.ErrorCode == "E_BAD_ARGS" {
		t.Fatalf("steps + finish must fail as a contract error, got ok=%v code=%q err=%q", both.OK, both.ErrorCode, both.Error)
	}

	// A field the description never mentions still fails loudly.
	typo := app.executeTool(ctx, cfg, "s-1", "plan", []byte(`{"step":["a"]}`))
	if typo.OK || typo.ErrorCode != "E_BAD_ARGS" || !strings.Contains(typo.Error, "step") {
		t.Fatalf("a misspelled field must be refused by name, got ok=%v code=%q err=%q", typo.OK, typo.ErrorCode, typo.Error)
	}
}

// TestSubagentStepBudgetIsHeldToTheDeclaredMaximum: the cap the description
// states has to be the cap the gate enforces, otherwise a model can ask for an
// unbounded child loop.
func TestSubagentStepBudgetIsHeldToTheDeclaredMaximum(t *testing.T) {
	redirectAppStateDir(t)
	app := NewApp()
	res := app.executeTool(context.Background(), ConfigState{Workspace: t.TempDir()}, "s-1", "subagent",
		[]byte(`{"task":"noop","role":"tester","maxSteps":1001}`))
	if res.OK || res.ErrorCode != "E_BAD_ARGS" || !strings.Contains(res.Error, "1000") {
		t.Fatalf("maxSteps above the declared cap must be refused, got ok=%v code=%q err=%q", res.OK, res.ErrorCode, res.Error)
	}
}
