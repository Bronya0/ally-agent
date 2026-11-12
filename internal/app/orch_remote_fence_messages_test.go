// SPDX-License-Identifier: GPL-3.0-only
package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRemoteCommandAllowsCrossDirectoryWrites(t *testing.T) {
	t.Parallel()
	py := pickRemoteHelperPython(t)
	cwd, outside := t.TempDir(), t.TempDir()
	existing := filepath.Join(outside, "existing.txt")
	if err := os.WriteFile(existing, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{existing, outside, filepath.Join(outside, "new.txt")} {
		script, err := buildRemoteScript(map[string]any{
			"op": "_check_write_targets", "cwd": cwd, "targets": []string{target},
		})
		if err != nil {
			t.Fatal(err)
		}
		resp, err := runRemoteHelperScript(t, py, script)
		if err != nil || !resp.OK {
			t.Fatalf("cross-directory target %s rejected: %+v %v", target, resp, err)
		}
	}
}

func TestRemoteCommandSymlinksKeepIndependentVCSProtection(t *testing.T) {
	t.Parallel()
	py := pickRemoteHelperPython(t)
	cwd, outside := t.TempDir(), t.TempDir()
	if err := os.Mkdir(filepath.Join(outside, ".git"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(cwd, "link")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := os.Symlink(filepath.Join(outside, ".git"), filepath.Join(cwd, "metadata")); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		path    string
		allowed bool
	}{
		{"link/new.txt", true}, {"link/existing.txt", true}, {"metadata/config", false},
	} {
		targets := remoteWriteTargets("cd '" + filepath.ToSlash(cwd) + "' && echo data > " + tc.path)
		script, err := buildRemoteScript(map[string]any{
			"op": "_check_write_targets", "cwd": t.TempDir(), "targets": targets,
		})
		if err != nil {
			t.Fatal(err)
		}
		resp, err := runRemoteHelperScript(t, py, script)
		if err != nil || resp.OK != tc.allowed {
			t.Fatalf("path %s: got %+v %v, allowed=%v", tc.path, resp, err, tc.allowed)
		}
		if !tc.allowed && toolErrorCode(remoteHelperError(resp)) != "E_PROTECTED_PATH" {
			t.Fatalf("VCS rejection lost error code: %s", resp.Error)
		}
	}
}
