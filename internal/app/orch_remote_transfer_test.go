// SPDX-License-Identifier: GPL-3.0-only
package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	toolshared "ally-dev/internal/tools/shared"
	openai "github.com/sashabaranov/go-openai"
)

func runPythonTransferBridge(t *testing.T, py string, payload map[string]any, stdin io.Reader, stdout io.Writer, preludes ...string) ([]byte, error) {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	bootstrap := "import base64, sys, zlib; code = zlib.decompress(base64.b64decode(sys.argv.pop(1))); exec(compile(code, '<ally>', 'exec'))"
	if len(preludes) > 0 {
		bootstrap = preludes[0] + "\n" + bootstrap
	}
	cmd := exec.Command(py, "-u", "-c", bootstrap, getRemoteTransferCompressedScript(), base64.RawURLEncoding.EncodeToString(raw))
	cmd.Stdin, cmd.Stdout = stdin, stdout
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err = cmd.Run()
	return stderr.Bytes(), err
}

func pythonTransferStream(t *testing.T, remoteRoot string) remoteTransferStream {
	t.Helper()
	py := pickRemoteHelperPython(t)
	return func(payload map[string]any, stdin io.Reader, stdout io.Writer) (remoteTransferStats, error) {
		payload["remotePath"] = remoteFixturePath(remoteRoot, strings.TrimPrefix(payload["remotePath"].(string), "/"))
		stderr, err := runPythonTransferBridge(t, py, payload, stdin, stdout)
		if err != nil {
			return remoteTransferStats{}, parseRemoteTransferError("fixture", stderr, err)
		}
		var stats remoteTransferStats
		err = decodeRemoteTransferMarker(stderr, &stats)
		return stats, err
	}
}

func isolateTransferHome(t *testing.T, home string) {
	t.Helper()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
}

func requireTransferOriginal(t *testing.T, path string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("destination changed: %q, %v", got, err)
	}
	leftovers, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".ally-tmp-*"))
	if err != nil || len(leftovers) != 0 {
		t.Fatalf("temporary files leaked: %v, %v", leftovers, err)
	}
}

func TestRemoteTransferBatchRoundTrip(t *testing.T) {
	workspace, remote := t.TempDir(), t.TempDir()
	isolateTransferHome(t, t.TempDir())
	files := []RemoteTransferFile{
		{LocalPath: "config 中文.json", RemotePath: "/app/config 中文.json"},
		{LocalPath: "dist/app.bin", RemotePath: "/app/app.bin"},
		{LocalPath: ".git/config", RemotePath: "/app/.git/config"},
	}
	contents := [][]byte{[]byte("配置\r\n"), bytes.Repeat([]byte{0, 255, 128, 13, 10}, 14000), {}}
	mtime := time.Unix(1700000000, 0)
	var expected int64
	for i, file := range files {
		path := filepath.Join(workspace, filepath.FromSlash(file.LocalPath))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, contents[i], 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, mtime, mtime); err != nil {
			t.Fatal(err)
		}
		expected += int64(len(contents[i]))
	}
	run := pythonTransferStream(t, remote)
	transfer := func(action string, files []RemoteTransferFile) RemoteTransferResult {
		t.Helper()
		plan, err := planRemoteTransferFiles(workspace, action, files)
		if err != nil {
			t.Fatal(err)
		}
		return runRemoteTransferBatch(context.Background(), RemoteTransferRequest{Action: action, Alias: "开发机", Files: files}, plan, func(file remoteTransferFilePlan) (int64, error) {
			return transferRemoteFile(context.Background(), action, workspace, file.localAbs, file.RemotePath, false, run)
		})
	}
	uploaded := transfer("upload", files)
	if uploaded.FileCount != len(files) || uploaded.FailedCount != 0 || uploaded.TotalBytes != expected {
		t.Fatalf("upload: %+v", uploaded)
	}
	for i := range files {
		files[i].LocalPath = "downloads/" + files[i].LocalPath
	}
	downloaded := transfer("download", files)
	if downloaded.FileCount != len(files) || downloaded.FailedCount != 0 || downloaded.TotalBytes != expected {
		t.Fatalf("download: %+v", downloaded)
	}
	for i, file := range files {
		path := filepath.Join(workspace, filepath.FromSlash(file.LocalPath))
		requireTransferOriginal(t, path, contents[i])
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.ModTime().Unix() != mtime.Unix() {
			t.Fatalf("modification time lost: %v", info.ModTime())
		}
		if runtime.GOOS != "windows" && info.Mode().Perm() != 0o750 {
			t.Fatalf("file mode lost: %v", info.Mode())
		}
		if !downloaded.Files[i].OK || downloaded.Files[i].RemotePath != file.RemotePath {
			t.Fatalf("file result lost mapping: %+v", downloaded.Files[i])
		}
	}
}

func TestRemoteTransferFilesRejectDirectories(t *testing.T) {
	workspace, remote := t.TempDir(), t.TempDir()
	isolateTransferHome(t, t.TempDir())
	if err := os.Mkdir(filepath.Join(workspace, "folder"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(remote, "folder"), 0o755); err != nil {
		t.Fatal(err)
	}
	original := []byte("original")
	localFile, remoteFile := filepath.Join(workspace, "file"), filepath.Join(remote, "file")
	if err := os.WriteFile(localFile, original, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(remoteFile, original, 0o600); err != nil {
		t.Fatal(err)
	}
	run := pythonTransferStream(t, remote)
	for _, tc := range []struct{ action, local, remote string }{
		{"upload", "folder", "/file"},
		{"upload", "file", "/folder"},
		{"download", "file", "/folder"},
		{"download", "folder", "/file"},
	} {
		t.Run(tc.action+"-"+tc.local+"-"+tc.remote, func(t *testing.T) {
			if _, err := transferRemoteFile(context.Background(), tc.action, workspace, filepath.Join(workspace, tc.local), tc.remote, true, run); err == nil {
				t.Fatal("directory transfer succeeded")
			}
			requireTransferOriginal(t, localFile, original)
			requireTransferOriginal(t, remoteFile, original)
			for _, root := range []string{workspace, remote} {
				entries, err := os.ReadDir(filepath.Join(root, "folder"))
				if err != nil || len(entries) != 0 {
					t.Fatalf("directory modified: %v, %v", entries, err)
				}
			}
		})
	}
}

func TestRemoteTransferUploadIncompletePreservesDestination(t *testing.T) {
	py := pickRemoteHelperPython(t)
	for _, tc := range []struct {
		name      string
		overwrite bool
		size      int
	}{
		{"existing", false, 3}, {"short", true, 4}, {"extra", true, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			remote := t.TempDir()
			target := filepath.Join(remote, "file")
			original := []byte("original")
			if err := os.WriteFile(target, original, 0o600); err != nil {
				t.Fatal(err)
			}
			stderr, err := runPythonTransferBridge(t, py, map[string]any{
				"action": "upload", "remotePath": remoteFixturePath(remote, "file"),
				"totalBytes": tc.size, "overwrite": tc.overwrite,
			}, strings.NewReader("new"), nil)
			if err == nil || decodeRemoteTransferMarker(stderr, nil) == nil {
				t.Fatalf("invalid upload succeeded: %s", stderr)
			}
			if tc.name == "existing" {
				transferErr := parseRemoteTransferError("fixture", stderr, err)
				if toolErrorCode(transferErr) != "E_EXISTS" || !strings.Contains(transferErr.Error(), "overwrite=true") {
					t.Fatalf("remote destination recovery lost: %v", transferErr)
				}
			}
			requireTransferOriginal(t, target, original)
			entries, err := os.ReadDir(remote)
			if err != nil || len(entries) != 1 {
				t.Fatalf("upload left temporary files: %v, %v", entries, err)
			}
		})
	}
}

func TestRemoteTransferUploadPublishWithoutClobber(t *testing.T) {
	py := pickRemoteHelperPython(t)
	for _, kind := range []string{"no-hard-links", "concurrent-destination"} {
		t.Run(kind, func(t *testing.T) {
			remote := t.TempDir()
			target := filepath.Join(remote, "file")
			prelude := `import errno, os
def no_links(source, target):
    raise OSError(getattr(errno, 'EOPNOTSUPP', errno.EPERM), 'no hard links')
os.link = no_links
`
			if kind == "concurrent-destination" {
				prelude = `import os
original_link, original_rename = os.link, os.rename
def concurrent_publish(publish, source, target):
    with open(target, 'wb') as f:
        f.write(b'concurrent')
    return publish(source, target)
os.link = lambda source, target: concurrent_publish(original_link, source, target)
os.rename = lambda source, target: concurrent_publish(original_rename, source, target)
`
			}
			stderr, runErr := runPythonTransferBridge(t, py, map[string]any{
				"action": "upload", "remotePath": remoteFixturePath(remote, "file"), "totalBytes": 3,
			}, strings.NewReader("new"), nil, prelude)
			if kind == "no-hard-links" && runtime.GOOS == "windows" {
				if runErr != nil || decodeRemoteTransferMarker(stderr, nil) != nil {
					t.Fatalf("upload still requires hard links: %v; %s", runErr, stderr)
				}
				requireTransferOriginal(t, target, []byte("new"))
				return
			}
			if runErr == nil {
				t.Fatal("unsafe or unsupported publication succeeded")
			}
			transferErr := parseRemoteTransferError("fixture", stderr, runErr)
			wantCode := "E_UNSUPPORTED"
			if kind == "concurrent-destination" {
				wantCode = "E_EXISTS"
				requireTransferOriginal(t, target, []byte("concurrent"))
			} else if _, err := os.Stat(target); !os.IsNotExist(err) {
				t.Fatalf("unsupported publication left a destination: %v", err)
			}
			if toolErrorCode(transferErr) != wantCode || !strings.Contains(transferErr.Error(), "overwrite=true") {
				t.Fatalf("publication recovery lost: %v", transferErr)
			}
			leftovers, err := filepath.Glob(filepath.Join(remote, ".ally-tmp-*"))
			if err != nil || len(leftovers) != 0 {
				t.Fatalf("publication leaked temporary files: %v, %v", leftovers, err)
			}
		})
	}
}

func TestRemoteTransferDownloadFailurePreservesDestination(t *testing.T) {
	for _, kind := range []string{"transport", "size", "cancel", "no-clobber"} {
		t.Run(kind, func(t *testing.T) {
			workspace := t.TempDir()
			target := filepath.Join(workspace, "file")
			original := []byte("original")
			overwrite := kind != "no-clobber"
			if overwrite {
				if err := os.WriteFile(target, original, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			_, err := transferRemoteFile(ctx, "download", workspace, target, "/file", overwrite, func(_ map[string]any, _ io.Reader, out io.Writer) (remoteTransferStats, error) {
				if _, err := io.WriteString(out, "new"); err != nil {
					return remoteTransferStats{}, err
				}
				stats := remoteTransferStats{TotalBytes: 3, Mode: 0o600}
				switch kind {
				case "transport":
					return stats, io.ErrUnexpectedEOF
				case "size":
					stats.TotalBytes++
				case "cancel":
					cancel()
				case "no-clobber":
					if err := os.WriteFile(target, original, 0o600); err != nil {
						return stats, err
					}
				}
				return stats, nil
			})
			if err == nil {
				t.Fatal("failed download was published")
			}
			requireTransferOriginal(t, target, original)
		})
	}
}

func TestRemoteTransferOverwriteFiles(t *testing.T) {
	workspace, remote := t.TempDir(), t.TempDir()
	isolateTransferHome(t, t.TempDir())
	local, dest := filepath.Join(workspace, "file"), filepath.Join(remote, "file")
	if err := os.WriteFile(local, []byte("replacement"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	run := pythonTransferStream(t, remote)
	if n, err := transferRemoteFile(context.Background(), "upload", workspace, local, "/file", true, run); err != nil || n != 11 {
		t.Fatalf("overwrite upload: %d, %v", n, err)
	}
	requireTransferOriginal(t, dest, []byte("replacement"))
	if err := os.WriteFile(local, []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	if n, err := transferRemoteFile(context.Background(), "download", workspace, local, "/file", true, run); err != nil || n != 11 {
		t.Fatalf("overwrite download: %d, %v", n, err)
	}
	requireTransferOriginal(t, local, []byte("replacement"))
}

func TestRemoteTransferBatchStopsAfterFirstError(t *testing.T) {
	workspace, remote := t.TempDir(), t.TempDir()
	isolateTransferHome(t, t.TempDir())
	for _, file := range []string{"first", "last"} {
		if err := os.WriteFile(filepath.Join(workspace, file), []byte(file), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	files := []RemoteTransferFile{{"first", "/first"}, {"missing", "/missing"}, {"last", "/last"}}
	plan, err := planRemoteTransferFiles(workspace, "upload", files)
	if err != nil {
		t.Fatal(err)
	}
	run := pythonTransferStream(t, remote)
	calls := 0
	result := runRemoteTransferBatch(context.Background(), RemoteTransferRequest{Alias: "dev", Action: "upload", Files: files}, plan, func(file remoteTransferFilePlan) (int64, error) {
		calls++
		return transferRemoteFile(context.Background(), "upload", workspace, file.localAbs, file.RemotePath, false, run)
	})
	if calls != 2 || result.FileCount != 1 || result.FailedCount != 1 || result.SkippedCount != 1 || result.TotalBytes != 5 || len(result.Files) != 3 {
		t.Fatalf("partial result: calls=%d result=%+v", calls, result)
	}
	if !result.Files[0].OK || result.Files[0].Skipped || result.Files[1].OK || result.Files[1].Skipped || result.Files[1].Error == "" || result.Files[2].OK || !result.Files[2].Skipped || result.Files[2].Error != "" {
		t.Fatalf("per-file results: %+v", result.Files)
	}
	if result.Files[1].ErrorCode != "E_PATH_NOT_FOUND" || !strings.Contains(result.Files[1].Error, "list_files") {
		t.Fatalf("missing source recovery lost: %+v", result.Files[1])
	}
	requireTransferOriginal(t, filepath.Join(remote, "first"), []byte("first"))
	for _, name := range []string{"missing", "last"} {
		if _, err := os.Stat(filepath.Join(remote, name)); !os.IsNotExist(err) {
			t.Fatalf("uncompleted file %s created destination: %v", name, err)
		}
	}
	modelText := compactToolDataForModel("remote_transfer", toolResult{OK: true, Data: result}, "")
	if !strings.Contains(modelText, strconv.Quote(result.Files[1].Error)) || !strings.Contains(modelText, `local="last"`) {
		t.Fatalf("model lost per-file outcome: %s", modelText)
	}
}

func TestRemoteTransferBatchCancellationStopsRemainingFiles(t *testing.T) {
	files := []RemoteTransferFile{{"a", "/a"}, {"b", "/b"}, {"c", "/c"}}
	plan, err := planRemoteTransferFiles(t.TempDir(), "upload", files)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	result := runRemoteTransferBatch(ctx, RemoteTransferRequest{Alias: "dev", Action: "upload"}, plan, func(remoteTransferFilePlan) (int64, error) {
		calls++
		cancel()
		return 4, nil
	})
	if calls != 1 || result.FileCount != 1 || result.FailedCount != 0 || result.SkippedCount != 2 || result.TotalBytes != 4 || !result.Files[0].OK {
		t.Fatalf("cancelled batch: calls=%d result=%+v", calls, result)
	}
	for _, item := range result.Files[1:] {
		if item.OK || !item.Skipped {
			t.Fatalf("unattempted item: %+v", item)
		}
	}
	if result.Files[1].ErrorCode != "E_CANCELLED" || result.Files[1].Error == "" || result.Files[2].Error != "" {
		t.Fatalf("cancellation should be reported once: %+v", result.Files)
	}
}

func TestRemoteTransferBatchCancelledBeforeStart(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	plan := []remoteTransferFilePlan{{RemoteTransferFile: RemoteTransferFile{"a", "/a"}}, {RemoteTransferFile: RemoteTransferFile{"b", "/b"}}}
	calls := 0
	result := runRemoteTransferBatch(ctx, RemoteTransferRequest{Alias: "dev", Action: "upload"}, plan, func(remoteTransferFilePlan) (int64, error) {
		calls++
		return 1, nil
	})
	if calls != 0 || result.FileCount != 0 || result.FailedCount != 0 || result.SkippedCount != len(plan) || result.TotalBytes != 0 {
		t.Fatalf("cancelled before start: calls=%d result=%+v", calls, result)
	}
	for _, file := range result.Files {
		if file.OK || !file.Skipped {
			t.Fatalf("cancelled file was attempted: %+v", file)
		}
	}
}

func TestRemoteTransferModelOutputBudget(t *testing.T) {
	message := "FIRST_CAUSE\n" + strings.Repeat("中文\n\t", 8192) + "\nFINAL_CAUSE"
	for _, longPaths := range []bool{false, true} {
		name := "error"
		if longPaths {
			name = "paths"
		}
		t.Run(name, func(t *testing.T) {
			result := RemoteTransferResult{Action: "upload", Alias: "dev", FailedCount: 1, SkippedCount: toolshared.RemoteTransferFileLimit - 1}
			if longPaths {
				result.Alias = strings.Repeat("昵称", 4096)
			}
			for i := 0; i < toolshared.RemoteTransferFileLimit; i++ {
				local := fmt.Sprintf("file-%d", i)
				if longPaths {
					local = strings.Repeat("目录", 2048) + "/" + local
				}
				file := RemoteTransferFileResult{RemoteTransferFile: RemoteTransferFile{local, "/tmp/" + local}, Skipped: i > 0}
				if i == 0 {
					file.Error, file.ErrorCode = message, "E_TEST_FAILURE"
				}
				result.Files = append(result.Files, file)
			}
			got := compactToolResultForModel("remote_transfer", toolResult{OK: true, Data: result}, "")
			if len(got) > maxModelToolOutput+1024 || !utf8.ValidString(got) {
				t.Fatalf("transfer output exceeded budget or broke UTF-8: bytes=%d", len(got))
			}
			if result.Files[0].Error != message {
				t.Fatal("model compaction changed the original error")
			}
			if !longPaths && (strings.Count(got, "local=") != len(result.Files) || !strings.Contains(got, "FIRST_CAUSE") || !strings.Contains(got, "FINAL_CAUSE") || !strings.Contains(got, "E_TEST_FAILURE")) {
				t.Fatalf("bounded errors lost diagnostics or file outcomes: %s", got)
			}
		})
	}
}

func TestRemoteTransferPlanValidation(t *testing.T) {
	workspace := t.TempDir()
	cases := []struct {
		name, action, code string
		files              []RemoteTransferFile
	}{
		{"empty", "upload", "E_BAD_ARGS", nil},
		{"blank", "upload", "E_BAD_ARGS", []RemoteTransferFile{{"", "/a"}}},
		{"relative-remote", "upload", "E_BAD_ARGS", []RemoteTransferFile{{"a", "a"}}},
		{"remote-root", "upload", "E_BAD_ARGS", []RemoteTransferFile{{"a", "/"}}},
		{"local-root", "upload", "E_PATH_OUTSIDE", []RemoteTransferFile{{".", "/a"}}},
		{"outside", "upload", "E_PATH_OUTSIDE", []RemoteTransferFile{{"../outside", "/a"}}},
		{"duplicate-upload", "upload", "E_BAD_ARGS", []RemoteTransferFile{{"a", "/app/a"}, {"b", "/app/x/../a"}}},
		{"duplicate-download", "download", "E_BAD_ARGS", []RemoteTransferFile{{"a", "/a"}, {"x/../a", "/b"}}},
		{"limit", "upload", "E_BAD_ARGS", make([]RemoteTransferFile, toolshared.RemoteTransferFileLimit+1)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := planRemoteTransferFiles(workspace, tc.action, tc.files); toolErrorCode(err) != tc.code {
				t.Fatalf("expected %s, got %v", tc.code, err)
			}
		})
	}
	files := []RemoteTransferFile{{"..valid", "/a"}, {"..valid", "/b"}}
	plan, err := planRemoteTransferFiles(workspace, "upload", files)
	if err != nil || len(plan) != 2 || plan[0].localAbs != filepath.Join(workspace, "..valid") {
		t.Fatalf("same source to different destinations: %+v, %v", plan, err)
	}
}

func TestRemoteTransferUsesRequestWorkspace(t *testing.T) {
	isolateTransferHome(t, t.TempDir())
	workspace := t.TempDir()
	if err := os.Mkdir(filepath.Join(workspace, "folder"), 0o755); err != nil {
		t.Fatal(err)
	}
	a := NewApp()
	t.Cleanup(a.sshPool.Close)
	a.config.Workspace = t.TempDir()
	result, err := a.executeRemoteTransfer(withToolWorkspace(context.Background(), workspace), RemoteTransferRequest{
		Alias: "dev", Action: "upload", Files: []RemoteTransferFile{{"folder", "/file"}},
	})
	if err != nil || result.FailedCount != 1 || len(result.Files) != 1 || result.Files[0].ErrorCode != "E_BAD_ARGS" {
		t.Fatalf("request workspace ignored: %+v, %v", result, err)
	}
	for _, req := range []RemoteTransferRequest{{Alias: "dev", Action: "sync"}, {Action: "upload"}} {
		if _, err := a.executeRemoteTransfer(context.Background(), req); toolErrorCode(err) != "E_BAD_ARGS" {
			t.Fatalf("invalid request accepted: %+v, %v", req, err)
		}
	}
}

func TestRemoteTransferCredentialFence(t *testing.T) {
	workspace := t.TempDir()
	isolateTransferHome(t, workspace)
	source := filepath.Join(workspace, ".ssh", "id_rsa")
	if err := os.MkdirAll(filepath.Dir(source), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"upload", "download"} {
		t.Run(action, func(t *testing.T) {
			calls := 0
			_, err := transferRemoteFile(context.Background(), action, workspace, source, "/file", true, func(map[string]any, io.Reader, io.Writer) (remoteTransferStats, error) {
				calls++
				return remoteTransferStats{}, errors.New("unexpected stream")
			})
			if toolErrorCode(err) != "E_PROTECTED_PATH" || calls != 0 {
				t.Fatalf("credential fence: calls=%d err=%v", calls, err)
			}
			requireTransferOriginal(t, source, []byte("secret"))
		})
	}
}

func TestRemoteTransferResolvedLocalFence(t *testing.T) {
	workspace, outside := t.TempDir(), t.TempDir()
	if err := os.Symlink(outside, filepath.Join(workspace, "link")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	for _, action := range []string{"upload", "download"} {
		if _, err := planRemoteTransferFiles(workspace, action, []RemoteTransferFile{{"link/file", "/file"}}); toolErrorCode(err) != "E_PATH_OUTSIDE" {
			t.Fatalf("symlink parent escaped workspace: %s, %v", action, err)
		}
	}
}

func TestRemoteTransferArgumentContract(t *testing.T) {
	valid := []byte(`{"alias":"dev","action":"upload","files":[{"localPath":"a","remotePath":"/a"},{"localPath":"b","remotePath":"/b"}]}`)
	var req RemoteTransferRequest
	if err, warnings := decodeToolArguments("remote_transfer", valid, &req); err != nil || len(warnings) != 0 || len(req.Files) != 2 || req.Files[1].RemotePath != "/b" {
		t.Fatalf("batch contract: %+v, %v, %v", req, err, warnings)
	}
	for _, raw := range []string{
		`{"alias":"dev","action":"upload","localPath":"a","remotePath":"/a"}`,
		`{"alias":"dev","action":"upload","files":[]}`,
		`{"alias":"dev","action":"upload","files":[{"localPath":"a"}]}`,
	} {
		if err, _ := decodeToolArguments("remote_transfer", []byte(raw), &RemoteTransferRequest{}); err == nil {
			t.Fatalf("invalid contract accepted: %s", raw)
		}
	}
}

func TestDecodeRemoteTransferMarkerStatistics(t *testing.T) {
	valid := `{"ok":true,"data":{"totalBytes":0,"mode":384,"modTime":1700000000}}`
	var stats remoteTransferStats
	if err := decodeRemoteTransferMarker([]byte("noise\n"+remoteTransferMarker+valid+"\ntrailing stderr\n"), &stats); err != nil || stats.TotalBytes != 0 || stats.Mode != 0o600 || stats.ModTime != 1700000000 {
		t.Fatalf("valid empty-file response: %+v, %v", stats, err)
	}
	for _, response := range []string{
		`{"ok":true}`, `{"ok":true,"data":null}`, `{"ok":true,"data":{}}`,
		`{"ok":true,"data":{"totalBytes":-1,"mode":384,"modTime":0}}`,
		`{"ok":true,"data":{"totalBytes":0,"mode":4095,"modTime":0}}`,
		`{"ok":true,"data":{"totalBytes":0,"mode":384}}`,
		`{"ok":false,"error":"source missing"}`,
	} {
		if err := decodeRemoteTransferMarker([]byte(remoteTransferMarker+response), nil); err == nil {
			t.Fatalf("invalid response accepted: %s", response)
		}
	}
}

func TestRemoteTransferBatchPolicyOrderingAndConflict(t *testing.T) {
	if !isFileMutationTool("remote_transfer") || !toolDidPlanWork("remote_transfer") {
		t.Fatal("transfer omitted from mutation/work policy")
	}
	cfg := ConfigState{Workspace: t.TempDir()}
	call := func(id, name, args string) openai.ToolCall {
		return openai.ToolCall{ID: id, Type: openai.ToolTypeFunction, Function: openai.FunctionCall{Name: name, Arguments: args}}
	}
	for _, calls := range [][]openai.ToolCall{
		{
			call("a", "remote_transfer", `{"alias":"dev","action":"download","files":[{"localPath":"a","remotePath":"/a"},{"localPath":"b","remotePath":"/b"}]}`),
			call("b", "create", `{"path":"b","content":"new"}`),
		},
		{
			call("a", "remote_transfer", `{"alias":"dev","action":"upload","files":[{"localPath":"a","remotePath":"/a"},{"localPath":"b","remotePath":"/b"}]}`),
			call("b", "remote_create_file", `{"alias":"dev","path":"/b","content":"new"}`),
		},
	} {
		conflicts := detectToolBatchConflicts(cfg, calls)
		if conflicts[0] != nil || toolErrorCode(conflicts[1]) != "E_WRITE_BATCH_CONFLICT" {
			t.Fatalf("second mapping missed conflict: %v", conflicts)
		}
		if got := fileMutationTargets(cfg, "remote_transfer", calls[0].Function.Arguments); len(got) != 2 {
			t.Fatalf("missing targets: %v", got)
		}
	}
}
