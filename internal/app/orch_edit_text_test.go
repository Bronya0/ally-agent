// SPDX-License-Identifier: GPL-3.0-only
package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ally-dev/internal/tools/edit"
)

func TestReadSnapshotSupportsTextDeletion(t *testing.T) {
	t.Parallel()
	a := newIsolatedToolTestApp(t)
	target := filepath.Join(a.config.Workspace, "source.txt")
	original := []byte("keep\ndelete me\nkeep\n")
	if err := os.WriteFile(target, original, 0600); err != nil {
		t.Fatal(err)
	}
	result, err := a.batchReadFilesWithConfig(a.config, BatchReadRequest{Files: []BatchReadFileRequest{{Path: "source.txt", StartLine: 2, EndLine: 2}}})
	if err != nil || len(result.Files) != 1 {
		t.Fatalf("read failed: %+v %v", result, err)
	}
	local := result.Files[0]
	remote, err := buildRemoteReadResultItem(remoteRawFile{Path: "/srv/source.txt", Data: original, Size: int64(len(original))}, BatchReadFileRequest{StartLine: 2, EndLine: 2})
	if err != nil {
		t.Fatal(err)
	}
	for name, item := range map[string]BatchReadResultItem{"local": local, "remote": remote} {
		if !item.Editable || item.Content != "2: delete me" {
			t.Fatalf("%s read lost the text edit snapshot: %+v", name, item)
		}
		raw, err := json.Marshal(item)
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			t.Fatal(err)
		}
		if _, exists := fields["rangeToken"]; exists {
			t.Fatalf("%s read still returns a removed edit credential: %s", name, raw)
		}
		if _, exists := fields["version"]; exists {
			t.Fatalf("%s read still returns an edit version: %s", name, raw)
		}
	}
	edited, err := a.editFilesWithConfig(a.config, []FileTextEdits{{Path: "source.txt", Changes: []TextChange{{OldText: "delete me\n", NewText: ""}}}})
	if err != nil || edited.Replacements != 1 || len(edited.Files) != 1 {
		t.Fatalf("text deletion failed: %+v %v", edited, err)
	}
	actual, err := os.ReadFile(target)
	if err != nil || string(actual) != "keep\nkeep\n" {
		t.Fatalf("text deletion changed the wrong source: %q %v", actual, err)
	}
}

// Removed replacement-scope parameters stay out of the declaration, so they are
// dropped with a warning instead of failing the call: refusing cost a model round
// trip for a value nothing reads. The edit then spans what oldText matches, and an
// ambiguous match is still refused by the unique-match rule.
func TestEditArgumentGateDropsRemovedConstraints(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"edit", "remote_edit"} {
		for _, key := range []string{"lineRange", "rangeToken"} {
			t.Run(name+"/"+key, func(t *testing.T) {
				args := []byte(`{"alias":"dev","path":"/source.txt","changes":[{"oldText":"same","newText":"new","` + key + `":{"start":2}}]}`)
				if name == "edit" {
					args = []byte(strings.Replace(string(args), `"alias":"dev",`, "", 1))
				}
				req := &RemoteEditRequest{}
				err, warnings := decodeToolArguments(name, args, req)
				if err != nil {
					t.Fatalf("a removed constraint must not fail the call: %v", err)
				}
				if len(warnings) == 0 || !strings.Contains(strings.Join(warnings, " "), key) {
					t.Fatalf("the dropped constraint must be named: %v", warnings)
				}
				if len(req.Changes) != 1 || req.Changes[0].OldText != "same" || req.Changes[0].NewText != "new" {
					t.Fatalf("unexpected decoded changes: %+v", req.Changes)
				}
			})
		}
	}
}

func TestEditUncertainCharactersLeaveSourceUntouched(t *testing.T) {
	t.Parallel()
	a := newIsolatedToolTestApp(t)
	target := filepath.Join(a.config.Workspace, "source.txt")
	original := []byte("keep\nvalue ‘old’\nkeep\n")
	if err := os.WriteFile(target, original, 0600); err != nil {
		t.Fatal(err)
	}
	req := FileTextEdits{Path: "source.txt", Changes: []TextChange{
		{OldText: "keep\nvalue", NewText: "kept\nvalue"},
		{OldText: "'old'", NewText: "‘new’"},
	}}
	_, err := a.editFilesWithConfig(a.config, []FileTextEdits{req})
	if toolErrorCode(err) != "E_NO_MATCH" {
		t.Fatalf("uncertain local source was accepted: %v", err)
	}
	actual, err := os.ReadFile(target)
	if err != nil || string(actual) != string(original) {
		t.Fatalf("rejected edit committed part of the batch: %q %v", actual, err)
	}
	// The remote handler must reject during preparation, before needing SSH.
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = a.remoteEditOne(ctx, remoteTarget{Host: "example.invalid"}, req, remoteRawFile{Path: "/source.txt", Data: original})
	if toolErrorCode(err) != "E_NO_MATCH" {
		t.Fatalf("remote edit bypassed shared source validation: %v", err)
	}
	req.Changes[1].OldText = "‘old’"
	result, err := a.editFilesWithConfig(a.config, []FileTextEdits{req})
	if err != nil || result.Replacements != 2 {
		t.Fatalf("exact retry failed: %+v %v", result, err)
	}
	actual, err = os.ReadFile(target)
	if err != nil || string(actual) != "kept\nvalue ‘new’\nkeep\n" {
		t.Fatalf("exact retry changed unrelated characters: %q %v", actual, err)
	}
}

func TestEditRecoversFromUnsafeIndentationRebase(t *testing.T) {
	t.Parallel()
	a := newIsolatedToolTestApp(t)
	const exact = "\tstep()\n\tfinish()"
	const original = "begin\n" + exact + "\nend\n"
	target := filepath.Join(a.config.Workspace, "source.txt")
	if err := os.WriteFile(target, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	req := FileTextEdits{Path: "source.txt", Changes: []TextChange{
		{OldText: "end\n", NewText: "done\n"},
		{OldText: "  step()\n  finish()", NewText: "  step()\n  check()\n  finish()"},
	}}
	result := a.executeTool(t.Context(), a.config, "", "edit", encodedToolArgs(t, req))
	if result.OK || result.ErrorCode != "E_NO_MATCH" {
		t.Fatalf("unsafe rebase was not refused: %+v", result)
	}
	modelResult := compactToolResultForModel("edit", result, "")
	_, detailJSON, found := strings.Cut(modelResult, "\ndetails: ")
	var details edit.MatchErrorDetails
	if err := json.Unmarshal([]byte(detailJSON), &details); !found || err != nil {
		t.Fatalf("model did not receive structured recovery details: %q", modelResult)
	}
	if details.ChangeIndex != 2 || details.MatchType != "indentation" || details.MatchCount != 1 || len(details.Candidates) != 1 {
		t.Fatalf("wrong failed change or candidates: %+v", details)
	}
	candidate := details.Candidates[0]
	if candidate.Line != 2 || candidate.StartLine != 1 || candidate.EndLine != 4 || !strings.Contains(candidate.Preview, exact) {
		t.Fatalf("wrong original source or line range: %+v", candidate)
	}
	if actual, err := os.ReadFile(target); err != nil || string(actual) != original {
		t.Fatalf("rejected edit partially wrote the batch: %q, %v", actual, err)
	}
	req.Changes[1] = TextChange{OldText: exact, NewText: "\tstep()\n\tcheck()\n\tfinish()"}
	if result = a.executeTool(t.Context(), a.config, "", "edit", encodedToolArgs(t, req)); !result.OK {
		t.Fatalf("retry with the original indentation failed: %+v", result)
	}
	if actual, err := os.ReadFile(target); err != nil || string(actual) != "begin\n\tstep()\n\tcheck()\n\tfinish()\ndone\n" {
		t.Fatalf("retry did not apply the entire intended batch: %q, %v", actual, err)
	}
}
