// SPDX-License-Identifier: GPL-3.0-only
package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestRemoteCommandOutsideWriteExplainsExistingTarget(t *testing.T) {
	py := pickRemoteHelperPython(t)
	sandbox := t.TempDir()
	root := filepath.Join(sandbox, "workspace")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	existing := filepath.Join(sandbox, "existing.txt")
	if err := os.WriteFile(existing, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{existing, sandbox} {
		script, err := buildRemoteScript(map[string]any{
			"op": "run", "workspaceRoot": root, "cwd": ".",
			"command": "echo must-not-run", "targets": []string{target},
		})
		if err != nil {
			t.Fatal(err)
		}
		resp, err := runRemoteHelperScript(t, py, script)
		if err != nil {
			t.Fatal(err)
		}
		if resp.OK {
			t.Fatal("existing outside target was allowed")
		}
		result := toolErrorResult(remoteHelperError(resp))
		raw, err := json.Marshal(result)
		if err != nil {
			t.Fatal(err)
		}
		model := compactToolResultForModel("remote_run_command", result, string(raw))
		for _, want := range []string{"E_PATH_OUTSIDE", "ALREADY EXISTS", "was not executed", "not a blanket ban", "genuinely new outside path", "/tmp has no overwrite exemption", ".tmp/output.txt"} {
			if !strings.Contains(model, want) {
				t.Fatalf("model missed %q: %s", want, model)
			}
		}
	}
	data, err := os.ReadFile(existing)
	if err != nil || string(data) != "original" {
		t.Fatalf("target changed: %q %v", data, err)
	}
	allowedTargets := []string{filepath.Join(sandbox, "new.txt"), filepath.Join(root, "new.txt")}
	// Windows NUL is filtered by the Go command parser before reaching the
	// helper. Exercise the POSIX helper's literal /dev/null exemption on POSIX.
	if runtime.GOOS != "windows" {
		allowedTargets = append(allowedTargets, os.DevNull)
	}
	for _, target := range allowedTargets {
		script, err := buildRemoteScript(map[string]any{"op": "_check_write_targets", "workspaceRoot": root, "cwd": ".", "targets": []string{target}})
		if err != nil {
			t.Fatal(err)
		}
		resp, err := runRemoteHelperScript(t, py, script)
		if err != nil || !resp.OK {
			t.Fatalf("allowed target %s rejected: %+v %v", target, resp, err)
		}
	}
}

func TestRemoteCommandSymlinkMessageDistinguishesNewTargets(t *testing.T) {
	py := pickRemoteHelperPython(t)
	sandbox := t.TempDir()
	root := filepath.Join(sandbox, "workspace")
	outside := filepath.Join(sandbox, "outside")
	for _, dir := range []string{root, outside} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	script, err := buildRemoteScript(map[string]any{"op": "_check_write_targets", "workspaceRoot": root, "cwd": ".", "targets": []string{"link/new.txt"}})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := runRemoteHelperScript(t, py, script)
	if err != nil {
		t.Fatal(err)
	}
	if resp.OK || !strings.Contains(resp.Error, "even when the destination does not exist") || !strings.Contains(resp.Error, "through a symlink") {
		t.Fatalf("ambiguous symlink rejection: %+v", resp)
	}
}
