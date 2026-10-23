// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package app

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"ally-dev/internal/tools/streamarchive"

	openai "github.com/sashabaranov/go-openai"
)

// pythonFStringPrefix matches an f-string literal prefix (f"…"/f'…', including
// the r/f/b combinations) at a token boundary. Python 2.7 cannot parse any of
// them, but a bare substring search for `f"` also hits hex escapes such as
// b"\xbf" and identifiers that merely end in f — all valid Python 2 code.
var pythonFStringPrefix = regexp.MustCompile(`(?m)(?:^|[^0-9A-Za-z_])[rbuRBU]{0,2}[fF][rbuRBU]{0,2}["']`)

// TestRemoteTransferScriptSyntaxAndSafetyInvariants validates Python 2.7 / 3.x compatibility
// and ensures required markers and security checks are present.
func TestRemoteTransferScriptSyntaxAndSafetyInvariants(t *testing.T) {
	if strings.Contains(remoteTransferPythonScript, "pathlib") {
		t.Error("remoteTransferPythonScript must not use pathlib (unsupported in Python 2.7)")
	}
	if found := pythonFStringPrefix.FindString(remoteTransferPythonScript); found != "" {
		t.Errorf("remoteTransferPythonScript must not use f-strings (unsupported in Python 2.7): %q", found)
	}
	if !strings.Contains(remoteTransferPythonScript, remoteTransferMarker) {
		t.Errorf("remoteTransferPythonScript missing marker %s", remoteTransferMarker)
	}
	if !strings.Contains(remoteTransferPythonScript, "Tar-Slip violation:") {
		t.Error("remoteTransferPythonScript missing Tar-Slip defense")
	}
	if !strings.Contains(remoteTransferPythonScript, "E_PROTECTED_PATH:") {
		t.Error("remoteTransferPythonScript missing VCS protected path defense")
	}
}

// TestPythonFStringGuardSeparatesPrefixesFromHexEscapes pins the guard's
// judgement: it must flag every f-string form Python 2.7 rejects while leaving
// hex escapes and identifiers that merely end in f alone.
func TestPythonFStringGuardSeparatesPrefixesFromHexEscapes(t *testing.T) {
	for _, tc := range []struct {
		code string
		want bool
	}{
		{`if tail[:1] and b"\x80" <= tail[:1] <= b"\xbf":`, false},
		{`if data == b"\x1f":`, false},
		{`name = prefix`, false},
		{`label = f"{value}"`, true},
		{`label = f'{value}'`, true},
		{`label = F"{value}"`, true},
		{`label = rf"{value}\d"`, true},
		{`label = fr'{value}'`, true},
		{`print(f"plain")`, true},
		{"value = f\"\"\"block\"\"\"", true},
	} {
		if got := pythonFStringPrefix.MatchString(tc.code); got != tc.want {
			t.Fatalf("f-string guard misjudged %q: got %v want %v", tc.code, got, tc.want)
		}
	}
}

func runPythonTransferBridge(t *testing.T, py string, payload map[string]any, stdin io.Reader, stdout io.Writer) ([]byte, error) {
	t.Helper()
	rawPayload, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	b64Payload := base64.RawURLEncoding.EncodeToString(rawPayload)
	b64Script := getRemoteTransferCompressedScript()

	bootstrap := "import base64, sys, zlib; code = zlib.decompress(base64.b64decode(sys.argv.pop(1))); exec(compile(code, '<ally>', 'exec'))"
	cmd := exec.Command(py, "-u", "-c", bootstrap, b64Script, b64Payload)
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	runErr := cmd.Run()
	return stderr.Bytes(), runErr
}

func TestRemoteTransferUploadFileAndDirectory(t *testing.T) {
	py := pickRemoteHelperPython(t)
	localRoot := t.TempDir()
	remoteRoot := t.TempDir()

	// 1. Single file upload
	testFile := filepath.Join(localRoot, "single.txt")
	if err := os.WriteFile(testFile, []byte("single file content"), 0o644); err != nil {
		t.Fatal(err)
	}

	var packBuf bytes.Buffer
	if _, err := streamarchive.PackWithFilter(testFile, "target.txt", &packBuf, nil); err != nil {
		t.Fatalf("Pack failed: %v", err)
	}

	stderr, err := runPythonTransferBridge(t, py, map[string]any{
		"action":        "upload",
		"workspaceRoot": remoteRoot,
		"remotePath":    "sub/target.txt",
		"overwrite":     false,
	}, &packBuf, nil)
	if err != nil {
		t.Fatalf("runPythonTransferBridge upload failed: %v, stderr: %s", err, string(stderr))
	}

	var outcome struct {
		TotalBytes int64 `json:"totalBytes"`
		FileCount  int   `json:"fileCount"`
	}
	if err := decodeRemoteTransferMarker(stderr, &outcome); err != nil {
		t.Fatalf("decodeRemoteTransferMarker: %v", err)
	}
	if outcome.FileCount != 1 {
		t.Fatalf("expected 1 file uploaded, got %d", outcome.FileCount)
	}

	uploadedContent, err := os.ReadFile(filepath.Join(remoteRoot, "sub", "target.txt"))
	if err != nil || string(uploadedContent) != "single file content" {
		t.Fatalf("unexpected uploaded content: %s, err: %v", string(uploadedContent), err)
	}

	// 2. Directory tree upload
	srcDir := filepath.Join(localRoot, "mydir")
	if err := os.MkdirAll(filepath.Join(srcDir, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "a.txt"), []byte("file a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "nested", "b.txt"), []byte("file b"), 0o644); err != nil {
		t.Fatal(err)
	}

	packBuf.Reset()
	if _, err := streamarchive.PackWithFilter(srcDir, "remotedir", &packBuf, nil); err != nil {
		t.Fatalf("Pack dir failed: %v", err)
	}

	stderr, err = runPythonTransferBridge(t, py, map[string]any{
		"action":        "upload",
		"workspaceRoot": remoteRoot,
		"remotePath":    "remotedir",
		"overwrite":     false,
	}, &packBuf, nil)
	if err != nil {
		t.Fatalf("upload dir failed: %v, stderr: %s", err, string(stderr))
	}

	if err := decodeRemoteTransferMarker(stderr, &outcome); err != nil {
		t.Fatalf("decodeRemoteTransferMarker dir: %v", err)
	}
	if outcome.FileCount != 2 {
		t.Fatalf("expected 2 files in directory, got %d", outcome.FileCount)
	}

	bContent, err := os.ReadFile(filepath.Join(remoteRoot, "remotedir", "nested", "b.txt"))
	if err != nil || string(bContent) != "file b" {
		t.Fatalf("unexpected nested content: %s", string(bContent))
	}
}

func TestRemoteTransferDownloadFileAndDirectory(t *testing.T) {
	py := pickRemoteHelperPython(t)
	remoteRoot := t.TempDir()
	localRoot := t.TempDir()

	// 1. Prepare remote files
	remoteDir := filepath.Join(remoteRoot, "pkg")
	if err := os.MkdirAll(filepath.Join(remoteDir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(remoteDir, "lib.go"), []byte("package lib"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(remoteDir, "sub", "util.go"), []byte("package sub"), 0o644); err != nil {
		t.Fatal(err)
	}

	// 2. Download directory stream
	var downloadStream bytes.Buffer
	stderr, err := runPythonTransferBridge(t, py, map[string]any{
		"action":        "download",
		"workspaceRoot": remoteRoot,
		"remotePath":    "pkg",
		"arcRootName":   "localpkg",
	}, nil, &downloadStream)
	if err != nil {
		t.Fatalf("download failed: %v, stderr: %s", err, string(stderr))
	}

	var outcome struct {
		TotalBytes int64 `json:"totalBytes"`
		FileCount  int   `json:"fileCount"`
	}
	if err := decodeRemoteTransferMarker(stderr, &outcome); err != nil {
		t.Fatalf("decodeRemoteTransferMarker download: %v", err)
	}
	if outcome.FileCount != 2 {
		t.Fatalf("expected 2 files downloaded, got %d", outcome.FileCount)
	}

	// 3. Unpack on local side
	stats, err := streamarchive.UnpackTarget(&downloadStream, filepath.Join(localRoot, "localpkg"), false)
	if err != nil {
		t.Fatalf("local unpack failed: %v", err)
	}
	if stats.FileCount != 2 {
		t.Fatalf("expected 2 files unpacked, got %d", stats.FileCount)
	}

	data, err := os.ReadFile(filepath.Join(localRoot, "localpkg", "lib.go"))
	if err != nil || string(data) != "package lib" {
		t.Fatalf("unexpected content for lib.go: %s, err: %v", string(data), err)
	}
}

func TestRemoteTransferUploadOverwriteDefense(t *testing.T) {
	py := pickRemoteHelperPython(t)
	remoteRoot := t.TempDir()
	destFile := filepath.Join(remoteRoot, "exists.txt")
	if err := os.WriteFile(destFile, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}

	var packBuf bytes.Buffer
	// Packing a dummy file
	testLocal := filepath.Join(t.TempDir(), "new.txt")
	_ = os.WriteFile(testLocal, []byte("new content"), 0o644)
	_, _ = streamarchive.PackWithFilter(testLocal, "exists.txt", &packBuf, nil)

	// Attempt upload with overwrite=false should fail
	stderr, err := runPythonTransferBridge(t, py, map[string]any{
		"action":        "upload",
		"workspaceRoot": remoteRoot,
		"remotePath":    "exists.txt",
		"overwrite":     false,
	}, &packBuf, nil)

	if err == nil {
		t.Fatalf("expected error when overwrite=false on existing destination, but succeeded")
	}
	if !strings.Contains(string(stderr), "remote destination already exists") {
		t.Fatalf("expected destination already exists in stderr, got: %s", string(stderr))
	}

	// Verify original file remained intact
	orig, _ := os.ReadFile(destFile)
	if string(orig) != "original" {
		t.Fatalf("original file was modified: %s", string(orig))
	}
}

func TestRemoteTransferSecurityFences(t *testing.T) {
	py := pickRemoteHelperPython(t)
	remoteRoot := t.TempDir()

	// 1. Refuse VCS metadata in remotePath
	var packBuf bytes.Buffer
	testLocal := filepath.Join(t.TempDir(), "new.txt")
	_ = os.WriteFile(testLocal, []byte("data"), 0o644)
	_, _ = streamarchive.PackWithFilter(testLocal, "hook", &packBuf, nil)

	stderr, err := runPythonTransferBridge(t, py, map[string]any{
		"action":        "upload",
		"workspaceRoot": remoteRoot,
		"remotePath":    ".git/hooks/pre-commit",
		"overwrite":     true,
	}, &packBuf, nil)
	if err == nil {
		t.Fatalf("expected error when targeting VCS path, but succeeded")
	}
	if !strings.Contains(string(stderr), "E_PROTECTED_PATH") {
		t.Fatalf("expected E_PROTECTED_PATH in stderr, got: %s", string(stderr))
	}

	// 2. Refuse uploading directly to workspace root
	packBuf.Reset()
	_, _ = streamarchive.PackWithFilter(testLocal, "new.txt", &packBuf, nil)
	stderr, err = runPythonTransferBridge(t, py, map[string]any{
		"action":        "upload",
		"workspaceRoot": remoteRoot,
		"remotePath":    "",
	}, &packBuf, nil)
	if err == nil {
		t.Fatalf("expected error when targeting workspace root without subpath, but succeeded")
	}
	if !strings.Contains(string(stderr), "refusing to upload directly to workspace root") {
		t.Fatalf("expected refusing to upload directly to workspace root, got: %s", string(stderr))
	}
}

func TestRemoteTransferExecuteValidation(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	ws := t.TempDir()
	a := NewApp()
	a.config.Workspace = ws

	ctx := context.Background()

	// 1. Invalid action
	_, err := a.executeRemoteTransfer(ctx, RemoteTransferRequest{
		Action:     "sync",
		Target:     "user@remote.example.com:/srv/app",
		LocalPath:  "local.txt",
		RemotePath: "remote.txt",
	})
	if toolErrorCode(err) != "E_BAD_ARGS" {
		t.Fatalf("expected E_BAD_ARGS for action 'sync', got %v", err)
	}

	// 2. Missing target
	_, err = a.executeRemoteTransfer(ctx, RemoteTransferRequest{
		Action:     "upload",
		Target:     "",
		LocalPath:  "local.txt",
		RemotePath: "remote.txt",
	})
	if toolErrorCode(err) != "E_BAD_ARGS" {
		t.Fatalf("expected E_BAD_ARGS for missing target, got %v", err)
	}

	// 3. Remote path escapes remote workspace
	_, err = a.executeRemoteTransfer(ctx, RemoteTransferRequest{
		Action:     "upload",
		Target:     "user@remote.example.com:/srv/app",
		LocalPath:  "local.txt",
		RemotePath: "/etc/passwd",
	})
	if err == nil {
		t.Fatalf("expected error for remote path /etc/passwd outside /srv/app, got nil")
	}

	// 4. Remote path targeting root directly
	_, err = a.executeRemoteTransfer(ctx, RemoteTransferRequest{
		Action:     "upload",
		Target:     "user@remote.example.com:/srv/app",
		LocalPath:  "local.txt",
		RemotePath: "/srv/app",
	})
	if err == nil || !strings.Contains(err.Error(), "path is required") {
		t.Fatalf("expected 'path is required' for remote root without subpath, got %v", err)
	}

	// 5. Remote path VCS metadata
	_, err = a.executeRemoteTransfer(ctx, RemoteTransferRequest{
		Action:     "upload",
		Target:     "user@remote.example.com:/srv/app",
		LocalPath:  "local.txt",
		RemotePath: ".git/hooks/post-commit",
	})
	if toolErrorCode(err) != "E_PROTECTED_PATH" {
		t.Fatalf("expected E_PROTECTED_PATH for remote .git, got %v", err)
	}

	// 6. Local path escapes local workspace
	_, err = a.executeRemoteTransfer(ctx, RemoteTransferRequest{
		Action:     "upload",
		Target:     "user@remote.example.com:/srv/app",
		LocalPath:  "../../outside.txt",
		RemotePath: "dest.txt",
	})
	if toolErrorCode(err) != "E_PATH_OUTSIDE" {
		t.Fatalf("expected E_PATH_OUTSIDE for escaping localPath, got %v", err)
	}

	// 7. Local path targeting workspace root directly
	_, err = a.executeRemoteTransfer(ctx, RemoteTransferRequest{
		Action:     "upload",
		Target:     "user@remote.example.com:/srv/app",
		LocalPath:  ".",
		RemotePath: "dest.txt",
	})
	if toolErrorCode(err) != "E_PATH_OUTSIDE" {
		t.Fatalf("expected E_PATH_OUTSIDE for localPath '.', got %v", err)
	}

	// 8. Local path targeting VCS metadata
	_, err = a.executeRemoteTransfer(ctx, RemoteTransferRequest{
		Action:     "upload",
		Target:     "user@remote.example.com:/srv/app",
		LocalPath:  ".git/config",
		RemotePath: "dest.txt",
	})
	if toolErrorCode(err) != "E_PROTECTED_PATH" {
		t.Fatalf("expected E_PROTECTED_PATH for local .git, got %v", err)
	}

	// 9. Upload non-existent local file
	_, err = a.executeRemoteTransfer(ctx, RemoteTransferRequest{
		Action:     "upload",
		Target:     "user@remote.example.com:/srv/app",
		LocalPath:  "not_found.txt",
		RemotePath: "dest.txt",
	})
	if err == nil || !strings.Contains(err.Error(), "local source does not exist") {
		t.Fatalf("expected 'local source does not exist', got %v", err)
	}

	// 10. Upload with absolute local path inside workspace
	validLocalFile := filepath.Join(ws, "valid.txt")
	if err := os.WriteFile(validLocalFile, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctxFast, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	_, err = a.executeRemoteTransfer(ctxFast, RemoteTransferRequest{
		Action:     "upload",
		Target:     "user@remote.example.com:/srv/app",
		LocalPath:  validLocalFile,
		RemotePath: "dest.txt",
	})
	if toolErrorCode(err) == "E_PATH_OUTSIDE" || (err != nil && strings.Contains(err.Error(), "local source does not exist")) {
		t.Fatalf("absolute local path inside workspace should be accepted, got %v", err)
	}
}

func TestDecodeRemoteTransferMarkerTrailingStderr(t *testing.T) {
	stderrWithTrailingBanner := []byte("Some initial banner\n" +
		remoteTransferMarker + `{"ok":true,"data":{"totalBytes":4096,"fileCount":3,"dirCount":1}}` + "\n" +
		"Connection to 192.168.1.10 closed by remote host.\r\n")

	var outcome struct {
		TotalBytes int64 `json:"totalBytes"`
		FileCount  int   `json:"fileCount"`
		DirCount   int   `json:"dirCount"`
	}
	if err := decodeRemoteTransferMarker(stderrWithTrailingBanner, &outcome); err != nil {
		t.Fatalf("decodeRemoteTransferMarker should ignore trailing stderr banner, got err: %v", err)
	}
	if outcome.TotalBytes != 4096 || outcome.FileCount != 3 || outcome.DirCount != 1 {
		t.Fatalf("unexpected outcome: %+v", outcome)
	}
}

func TestRemoteTransferBatchPolicyOrderingAndConflict(t *testing.T) {
	if !isFileMutationTool("remote_transfer") {
		t.Fatal("remote_transfer must participate in file mutation conflict checks")
	}
	if !toolDidPlanWork("remote_transfer") {
		t.Fatal("remote_transfer must count as work that moves the plan forward")
	}

	ws := t.TempDir()
	cfg := ConfigState{Workspace: ws}

	// 1. Download conflict with local create
	conflicts := detectToolBatchConflicts(cfg, []openai.ToolCall{
		{
			ID:   "call_1",
			Type: openai.ToolTypeFunction,
			Function: openai.FunctionCall{
				Name:      "remote_transfer",
				Arguments: `{"target":"dev:/srv/app","action":"download","remotePath":"src/a.txt","localPath":"a.txt"}`,
			},
		},
		{
			ID:   "call_2",
			Type: openai.ToolTypeFunction,
			Function: openai.FunctionCall{
				Name:      "create",
				Arguments: `{"path":"a.txt","content":"hello"}`,
			},
		},
	})
	if conflicts[0] != nil {
		t.Fatalf("first call should not be conflicted, got %v", conflicts[0])
	}
	if conflicts[1] == nil || toolErrorCode(conflicts[1]) != "E_WRITE_BATCH_CONFLICT" {
		t.Fatalf("second call must be rejected with E_WRITE_BATCH_CONFLICT, got %v", conflicts[1])
	}

	// 2. Upload conflict with another remote upload to same remote target
	conflicts = detectToolBatchConflicts(cfg, []openai.ToolCall{
		{
			ID:   "call_3",
			Type: openai.ToolTypeFunction,
			Function: openai.FunctionCall{
				Name:      "remote_transfer",
				Arguments: `{"target":"dev:/srv/app","action":"upload","localPath":"a.txt","remotePath":"dist/bundle.js"}`,
			},
		},
		{
			ID:   "call_4",
			Type: openai.ToolTypeFunction,
			Function: openai.FunctionCall{
				Name:      "remote_transfer",
				Arguments: `{"target":"dev:/srv/app","action":"upload","localPath":"b.txt","remotePath":"dist/bundle.js"}`,
			},
		},
	})
	if conflicts[0] != nil {
		t.Fatalf("first call should not be conflicted, got %v", conflicts[0])
	}
	if conflicts[1] == nil || toolErrorCode(conflicts[1]) != "E_WRITE_BATCH_CONFLICT" {
		t.Fatalf("second call must be rejected with E_WRITE_BATCH_CONFLICT, got %v", conflicts[1])
	}
}

func TestRemoteTransferUploadSensitiveDirectoryBlocked(t *testing.T) {
	ws := t.TempDir()
	t.Setenv("HOME", ws)
	t.Setenv("USERPROFILE", ws)

	a := NewApp()
	a.config.Workspace = ws

	// Create an .ssh directory inside workspace (e.g. simulated sensitive path)
	sshDir := filepath.Join(ws, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sshDir, "id_rsa"), []byte("SECRET KEY"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Uploading the .ssh directory itself must be blocked
	_, err := a.executeRemoteTransfer(context.Background(), RemoteTransferRequest{
		Action:     "upload",
		Target:     "user@remote.example.com:/srv/app",
		LocalPath:  ".ssh",
		RemotePath: "backup_ssh",
	})
	if toolErrorCode(err) != "E_PROTECTED_PATH" {
		t.Fatalf("expected E_PROTECTED_PATH when uploading sensitive directory .ssh, got %v", err)
	}
}

func TestRemoteTransferUploadArchiveFences(t *testing.T) {
	py := pickRemoteHelperPython(t)
	for _, name := range []string{"sibling.txt", "/dest/evil.txt", "dest/../evil.txt", "dest/.git/config"} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			var buf bytes.Buffer
			tw := tar.NewWriter(&buf)
			_ = tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Size: 4, Mode: 0o644})
			_, _ = tw.Write([]byte("evil"))
			_ = tw.Close()
			stderr, err := runPythonTransferBridge(t, py, map[string]any{"action": "upload", "workspaceRoot": root, "remotePath": "dest", "overwrite": true}, &buf, nil)
			if err == nil {
				t.Fatalf("accepted malicious archive %q: %s", name, stderr)
			}
			if err := decodeRemoteTransferMarker(stderr, nil); err == nil {
				t.Fatal("reported success for malicious archive")
			}
		})
	}
}

func TestRemoteTransferUploadExistingSymlinkParent(t *testing.T) {
	py := pickRemoteHelperPython(t)
	root, outside := t.TempDir(), t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "dest"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "dest", "link")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	_ = tw.WriteHeader(&tar.Header{Name: "dest/link/evil.txt", Typeflag: tar.TypeReg, Size: 4, Mode: 0o644})
	_, _ = tw.Write([]byte("evil"))
	_ = tw.Close()
	stderr, err := runPythonTransferBridge(t, py, map[string]any{"action": "upload", "workspaceRoot": root, "remotePath": "dest", "overwrite": true}, &buf, nil)
	if err == nil {
		t.Fatalf("accepted symlink escape: %s", stderr)
	}
	if _, err := os.Stat(filepath.Join(outside, "evil.txt")); !os.IsNotExist(err) {
		t.Fatal("wrote outside workspace")
	}
}

func TestRemoteTransferDownloadHardlinks(t *testing.T) {
	py := pickRemoteHelperPython(t)
	root := t.TempDir()
	pkg := filepath.Join(root, "pkg")
	if err := os.Mkdir(pkg, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkg, "a.txt"), []byte("shared content"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(filepath.Join(pkg, "a.txt"), filepath.Join(pkg, "b.txt")); err != nil {
		t.Skipf("hardlinks unavailable: %v", err)
	}
	var buf bytes.Buffer
	stderr, err := runPythonTransferBridge(t, py, map[string]any{"action": "download", "workspaceRoot": root, "remotePath": "pkg", "arcRootName": "pkg"}, nil, &buf)
	if err != nil {
		t.Fatalf("download: %v %s", err, stderr)
	}
	dest := t.TempDir()
	stats, err := streamarchive.UnpackTarget(&buf, filepath.Join(dest, "pkg"), false)
	if err != nil {
		t.Fatal(err)
	}
	var reported streamarchive.Stats
	if err := decodeRemoteTransferMarker(stderr, &reported); err != nil {
		t.Fatal(err)
	}
	if stats != reported || stats.FileCount != 2 {
		t.Fatalf("stats mismatch: %+v %+v", stats, reported)
	}
	for _, name := range []string{"a.txt", "b.txt"} {
		if data, err := os.ReadFile(filepath.Join(dest, "pkg", name)); err != nil || string(data) != "shared content" {
			t.Fatalf("hardlinked file missing: %s %q %v", name, data, err)
		}
	}
}

func TestRemoteTransferDownloadReadErrorIsFailure(t *testing.T) {
	py := pickRemoteHelperPython(t)
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "pkg", "unreadable.txt"), []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]any{"action": "download", "workspaceRoot": root, "remotePath": "pkg"})
	// Deterministically simulate a file becoming unreadable between walk and add,
	// including when the test process is an administrator/root.
	prelude := "import tarfile\noriginal_add = tarfile.TarFile.add\ndef failing_add(self, name, *args, **kwargs):\n    if name.endswith('unreadable.txt'): raise IOError('simulated read failure')\n    return original_add(self, name, *args, **kwargs)\ntarfile.TarFile.add = failing_add\n"
	// The helper contains a future import, so execute it as a separate compilation.
	cmd := exec.Command(py, "-u", "-", base64.RawURLEncoding.EncodeToString(payload))
	cmd.Stdin = strings.NewReader(prelude + "exec(compile(" + stringMustJSON(remoteTransferPythonScript) + ", '<ally-transfer>', 'exec'))")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	cmd.Stdout = io.Discard
	if err := cmd.Run(); err == nil {
		t.Fatalf("read error silently reported success: %s", stderr.String())
	}
	if !strings.Contains(stderr.String(), "simulated read failure") {
		t.Fatalf("missing failure reason: %s", stderr.String())
	}
}

func stringMustJSON(s string) string {
	encoded, _ := json.Marshal(s)
	return string(encoded)
}

func TestDecodeRemoteTransferMarkerRejectsInvalidStatistics(t *testing.T) {
	for _, response := range []string{
		`{"ok":true}`,
		`{"ok":true,"data":null}`,
		`{"ok":true,"data":{}}`,
		`{"ok":true,"data":{"totalBytes":-1,"fileCount":1,"dirCount":0}}`,
		`{"ok":true,"data":{"totalBytes":1,"fileCount":1}}`,
	} {
		if err := decodeRemoteTransferMarker([]byte(remoteTransferMarker+response), nil); err == nil {
			t.Fatalf("accepted invalid success response: %s", response)
		}
	}
}

func TestRemoteTransferUploadTruncatedReplacementPreservesDestination(t *testing.T) {
	py := pickRemoteHelperPython(t)
	for _, directory := range []bool{false, true} {
		root := t.TempDir()
		target := filepath.Join(root, "target")
		original := target
		if directory {
			if err := os.Mkdir(target, 0o755); err != nil {
				t.Fatal(err)
			}
			original = filepath.Join(target, "original.txt")
		}
		if err := os.WriteFile(original, []byte("original"), 0o600); err != nil {
			t.Fatal(err)
		}
		var buf bytes.Buffer
		tw := tar.NewWriter(&buf)
		_ = tw.WriteHeader(&tar.Header{Name: "target", Typeflag: tar.TypeReg, Size: 4096, Mode: 0o644})
		_, _ = tw.Write([]byte("partial"))
		stderr, err := runPythonTransferBridge(t, py, map[string]any{"action": "upload", "workspaceRoot": root, "remotePath": "target", "overwrite": true}, &buf, nil)
		if err == nil {
			t.Fatalf("accepted truncated stream: %s", stderr)
		}
		if data, err := os.ReadFile(original); err != nil || string(data) != "original" {
			t.Fatalf("destroyed original: %q %v", data, err)
		}
		matches, _ := filepath.Glob(filepath.Join(root, ".ally-tmp-*"))
		if len(matches) != 0 {
			t.Fatalf("temporary files leaked: %v", matches)
		}
	}
}

func TestRemoteTransferUploadReadOnlyDirectory(t *testing.T) {
	py := pickRemoteHelperPython(t)
	root := t.TempDir()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, header := range []*tar.Header{
		{Name: "target", Typeflag: tar.TypeDir, Mode: 0o555},
		{Name: "target/child", Typeflag: tar.TypeDir, Mode: 0o500},
		{Name: "target/child/file.txt", Typeflag: tar.TypeReg, Mode: 0o644, Size: 7},
	} {
		if err := tw.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if header.Typeflag == tar.TypeReg {
			_, _ = tw.Write([]byte("content"))
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	// Restore directory permissions before TempDir cleanup on Unix.
	t.Cleanup(func() {
		_ = os.Chmod(filepath.Join(root, "target"), 0o700)
		_ = os.Chmod(filepath.Join(root, "target", "child"), 0o700)
	})
	stderr, err := runPythonTransferBridge(t, py, map[string]any{
		"action": "upload", "workspaceRoot": root, "remotePath": "target",
	}, &buf, nil)
	if err != nil {
		t.Fatalf("read-only directory upload failed: %v: %s", err, stderr)
	}
	if data, err := os.ReadFile(filepath.Join(root, "target", "child", "file.txt")); err != nil || string(data) != "content" {
		t.Fatalf("uploaded file = %q, %v", data, err)
	}
	if runtime.GOOS != "windows" {
		for name, mode := range map[string]os.FileMode{"target": 0o555, "target/child": 0o500} {
			st, err := os.Stat(filepath.Join(root, name))
			if err != nil || st.Mode().Perm() != mode {
				t.Fatalf("directory %s permissions: %v, %v", name, st, err)
			}
		}
	}
}

func TestRemoteTransferUploadDuplicateEntryDoesNotClobber(t *testing.T) {
	py := pickRemoteHelperPython(t)
	root := t.TempDir()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, content := range []string{"original", "modified"} {
		_ = tw.WriteHeader(&tar.Header{Name: "target", Typeflag: tar.TypeReg, Size: int64(len(content)), Mode: 0o600})
		_, _ = tw.Write([]byte(content))
	}
	_ = tw.Close()
	stderr, err := runPythonTransferBridge(t, py, map[string]any{"action": "upload", "workspaceRoot": root, "remotePath": "target", "overwrite": false}, &buf, nil)
	if err == nil {
		t.Fatalf("accepted duplicate entry with overwrite disabled: %s", stderr)
	}
	if data, err := os.ReadFile(filepath.Join(root, "target")); err != nil || string(data) != "original" {
		t.Fatalf("clobbered original: %q %v", data, err)
	}
}
