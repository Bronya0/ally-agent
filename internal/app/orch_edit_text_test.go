// SPDX-License-Identifier: GPL-3.0-only
package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
