// SPDX-License-Identifier: GPL-3.0-only
package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ally-dev/internal/tools/edit"
)

func TestReadRangeTokensRequireExactCompleteExplicitRange(t *testing.T) {
	text := "one\ntwo\nthree\n"
	for name, tc := range map[string]struct {
		text  string
		req   ReadFileRequest
		token bool
	}{
		"one line":        {text, ReadFileRequest{StartLine: 2, EndLine: 2}, true},
		"span":            {text, ReadFileRequest{StartLine: 1, EndLine: 3}, true},
		"default preview": {text, ReadFileRequest{}, false},
		"start only":      {text, ReadFileRequest{StartLine: 2}, false},
		"out of bounds":   {text, ReadFileRequest{StartLine: 2, EndLine: 10}, false},
		"context":         {text, ReadFileRequest{StartLine: 2, EndLine: 2, ContextBefore: 1}, false},
		"long line":       {strings.Repeat("x", maxReadLineChars+1) + "\n", ReadFileRequest{StartLine: 1, EndLine: 1}, false},
		"budget":          {strings.Repeat(strings.Repeat("x", 100)+"\n", 200), ReadFileRequest{StartLine: 1, EndLine: 200}, false},
	} {
		t.Run(name, func(t *testing.T) {
			result, err := buildTextReadResult("/source.txt", []byte(tc.text), int64(len(tc.text)), tc.req)
			if err != nil {
				t.Fatal(err)
			}
			if (result.RangeToken != "") != tc.token {
				t.Fatalf("wrong range token eligibility: %+v", result)
			}
			if tc.token {
				_, _, err = edit.ApplyBatchTextChangesForFile(tc.text, []edit.TextChange{{LineRange: &edit.LineRange{Start: tc.req.StartLine, End: tc.req.EndLine, RangeToken: result.RangeToken}, NewText: ""}}, "/source.txt")
				if err != nil {
					t.Fatalf("read token cannot be used: %v", err)
				}
			}
		})
	}
}

func TestLocalReadTokenDeletesExactlyOneLineAndRejectsChangedBounds(t *testing.T) {
	t.Parallel()
	a := newIsolatedToolTestApp(t)
	target := filepath.Join(a.config.Workspace, "source.txt")
	original := "keep\ndelete me\nkeep\n"
	if err := os.WriteFile(target, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	result, err := a.batchReadFilesWithConfig(a.config, BatchReadRequest{Files: []BatchReadFileRequest{{Path: "source.txt", StartLine: 2, EndLine: 2}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Files) != 1 || result.Files[0].RangeToken == "" {
		t.Fatalf("missing read token: %+v", result)
	}
	item := result.Files[0]
	modelBody := compactToolResultForModel("read", toolResult{OK: true, Data: result}, "")
	if !strings.Contains(modelBody, item.RangeToken) || !strings.Contains(modelBody, "2: delete me") {
		t.Fatalf("model did not receive the complete source and token: %q", modelBody)
	}
	change := TextChange{LineRange: &edit.LineRange{Start: 1, End: 2, RangeToken: item.RangeToken}, NewText: ""}
	_, err = a.editFilesWithConfig(a.config, []FileTextEdits{{Path: "source.txt", Version: item.Version, Changes: []TextChange{change}}})
	if toolErrorCode(err) != "E_LINE_RANGE" {
		t.Fatalf("changed bounds accepted: %v", err)
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != original {
		t.Fatalf("rejected edit wrote data: %q %v", data, err)
	}
	change.LineRange.Start, change.LineRange.End = 2, 0
	edited, err := a.editFilesWithConfig(a.config, []FileTextEdits{{Path: target, Version: item.Version, Changes: []TextChange{change}}})
	if err != nil || edited.Replacements != 1 {
		t.Fatalf("one-line deletion failed: %+v %v", edited, err)
	}
	data, err = os.ReadFile(target)
	if err != nil || string(data) != "keep\nkeep\n" {
		t.Fatalf("wrong line deletion: %q %v", data, err)
	}
}

func TestRemoteReadTokenBindsAliasAndUsesSharedLineEngine(t *testing.T) {
	text := "keep\ntarget\nkeep\n"
	rt := remoteTarget{Raw: "dev", Host: "user@example.invalid", Port: "22", poolKey: "node:dev"}
	item, err := buildRemoteReadResultItemForTarget(remoteRawFile{Path: "/srv/source.txt", Data: []byte(text), Size: int64(len(text))}, BatchReadFileRequest{Path: "/srv/source.txt", StartLine: 2, EndLine: 2}, rt)
	if err != nil || item.RangeToken == "" {
		t.Fatalf("remote read did not supply token: %+v %v", item, err)
	}
	change := edit.TextChange{LineRange: &edit.LineRange{Start: 2, RangeToken: item.RangeToken}, NewText: "replacement\ninserted"}
	result, count, err := edit.ApplyBatchTextChangesForFile(text, []edit.TextChange{change}, remoteEditIdentity(rt, "/srv/source.txt"))
	if err != nil || count != 1 || result.Content != "keep\nreplacement\ninserted\nkeep\n" {
		t.Fatalf("remote token/engine mismatch: %+v %d %v", result, count, err)
	}
	rt.Raw = "other"
	_, _, err = edit.ApplyBatchTextChangesForFile(text, []edit.TextChange{change}, remoteEditIdentity(rt, "/srv/source.txt"))
	if toolErrorCode(err) != "E_LINE_RANGE" {
		t.Fatalf("another alias reused token: %v", err)
	}
	// remoteEditOne validates proof before any transport is needed.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = newIsolatedToolTestApp(t).remoteEditOne(ctx, rt, FileTextEdits{Path: "/srv/source.txt", Version: item.Version, Changes: []TextChange{{LineRange: change.LineRange, NewText: ""}}}, remoteRawFile{Path: "/srv/source.txt", Data: []byte(text)})
	if toolErrorCode(err) != "E_LINE_RANGE" {
		t.Fatalf("remote edit bypassed alias binding: %v", err)
	}
}

func TestRemoteReadTokenRejectsRetargetedAlias(t *testing.T) {
	text := "keep\ntarget\nkeep\n"
	original := remoteTarget{Raw: "dev", Host: "user@first.invalid", Port: "22", poolKey: "node:first"}
	file := remoteRawFile{Path: "/srv/source.txt", Data: []byte(text), Size: int64(len(text))}
	item, err := buildRemoteReadResultItemForTarget(file, BatchReadFileRequest{Path: file.Path, StartLine: 2, EndLine: 2}, original)
	if err != nil || item.RangeToken == "" {
		t.Fatalf("missing remote token: %+v %v", item, err)
	}
	// Even if validation regresses, a cancelled context prevents any transport.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	a := newIsolatedToolTestApp(t)
	// The same resolved target accepts the proof, including a normalized path
	// spelling; a no-op keeps this regression entirely independent of SSH.
	unchanged, err := a.remoteEditOne(ctx, original, FileTextEdits{Path: " /srv/./source.txt ", Version: item.Version, Changes: []TextChange{{LineRange: &edit.LineRange{Start: 2, RangeToken: item.RangeToken}, NewText: "target"}}}, file)
	if err != nil || unchanged.Classification != "noop" || unchanged.Version != item.Version {
		t.Fatalf("same target rejected its read token: %+v %v", unchanged, err)
	}
	for name, changed := range map[string]remoteTarget{
		"host": {Raw: original.Raw, Host: "user@second.invalid", Port: original.Port, poolKey: original.poolKey},
		"port": {Raw: original.Raw, Host: original.Host, Port: "2222", poolKey: original.poolKey},
		"node": {Raw: original.Raw, Host: original.Host, Port: original.Port, poolKey: "node:second"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := a.remoteEditOne(ctx, changed, FileTextEdits{Path: file.Path, Version: item.Version, Changes: []TextChange{{LineRange: &edit.LineRange{Start: 2, RangeToken: item.RangeToken}, NewText: ""}}}, file)
			if toolErrorCode(err) != "E_LINE_RANGE" {
				t.Fatalf("retargeted alias reused an old token: %v", err)
			}
		})
	}
}
