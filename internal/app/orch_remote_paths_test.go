package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Helper fixtures run the remote Python script locally, including on Windows.
// Convert fixture-relative names to native absolute paths before transport.
func remoteFixturePath(directory, name string) string {
	if !filepath.IsAbs(name) && !strings.HasPrefix(name, "/") {
		name = filepath.Join(directory, name)
	}
	return filepath.ToSlash(name)
}

func remoteFixturePaths(directory string, names []string) []string {
	paths := make([]string, len(names))
	for i, name := range names {
		paths[i] = remoteFixturePath(directory, name)
	}
	return paths
}

func TestParseRemoteServerTargets(t *testing.T) {
	for _, tc := range []struct{ input, host, port string }{
		{"dev", "dev", ""},
		{"deploy@example.com", "deploy@example.com", ""},
		{"deploy@example.com:2222", "deploy@example.com", "2222"},
		{"ssh://deploy@example.com:2222", "deploy@example.com", "2222"},
		{"ssh://deploy@[2001:db8::1]:2222", "deploy@2001:db8::1", "2222"},
	} {
		rt, err := parseRemoteTarget(tc.input)
		if err != nil || rt.Host != tc.host || rt.Port != tc.port {
			t.Fatalf("target %q: %+v %v", tc.input, rt, err)
		}
	}
	for _, input := range []string{"dev:/srv/app", "dev:/", "ssh://host/srv/app", "", "dev/relative", "ssh://", "ssh://host:0", "host:65536", "host:", "ssh://host:abc", "http://host", "ssh://user:secret@host", "ssh://host?query", "ssh://host#fragment"} {
		if rt, err := parseRemoteTarget(input); err == nil {
			t.Fatalf("invalid target %q accepted: %+v", input, rt)
		}
	}
}

func TestRemoteMutationIdentityIncludesServerAndAbsolutePath(t *testing.T) {
	want := remoteMutationKey("dev", "/srv/app/main.go")
	for _, input := range []struct{ target, path string }{
		{"DEV", "/srv/app/main.go"},
		{"ssh://dev:22", "/srv/app/./main.go"},
	} {
		if got := remoteMutationKey(input.target, input.path); got != want {
			t.Fatalf("same file has different identities: %q vs %q", got, want)
		}
	}
	for _, input := range []struct{ target, path string }{
		{"other", "/srv/app/main.go"}, {"dev:2222", "/srv/app/main.go"}, {"dev", "/etc/main.go"},
	} {
		if got := remoteMutationKey(input.target, input.path); got == want {
			t.Fatalf("distinct file shared identity %q", got)
		}
	}
}

func TestRemoteHelperAbsolutePathsFollowOrdinarySymlinks(t *testing.T) {
	t.Parallel()
	py := pickRemoteHelperPython(t)
	base, outside := t.TempDir(), t.TempDir()
	link := filepath.Join(base, "link")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	file := filepath.Join(link, "config.txt")
	run := func(payload map[string]any) remotePythonResponse {
		t.Helper()
		script, err := buildRemoteScript(payload)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := runRemoteHelperScript(t, py, script)
		if err != nil || !resp.OK {
			t.Fatalf("helper failed: %+v %v", resp, err)
		}
		return resp
	}
	for _, content := range []string{"first", "updated"} {
		run(map[string]any{"op": "write", "path": file, "overwrite": true, "dataBase64": base64.StdEncoding.EncodeToString([]byte(content))})
		resp := run(map[string]any{"op": "read", "path": file})
		var data struct {
			DataBase64 string `json:"dataBase64"`
		}
		if err := json.Unmarshal(resp.Data, &data); err != nil {
			t.Fatal(err)
		}
		if data.DataBase64 != base64.StdEncoding.EncodeToString([]byte(content)) {
			t.Fatalf("wrong read: %s", resp.Data)
		}
	}
	run(map[string]any{"op": "delete_batch", "paths": []string{file}})
	if _, err := os.Stat(filepath.Join(outside, "config.txt")); !os.IsNotExist(err) {
		t.Fatalf("cross-directory file not removed: %v", err)
	}
}

func TestRemoteCommandDirectoryDoesNotPersist(t *testing.T) {
	t.Parallel()
	py := pickRemoteHelperPython(t)
	home := t.TempDir()
	project := filepath.Join(t.TempDir(), "a project")
	if err := os.Mkdir(project, 0700); err != nil {
		t.Fatal(err)
	}
	change, current := "cd '"+project+"' && pwd", "pwd"
	if runtime.GOOS == "windows" {
		// Mirror the helper's process-directory behavior with the local shell.
		change, current = `cd /d "`+project+`" && cd`, "cd"
	}
	for _, tc := range []struct{ command, want string }{{change, project}, {current, home}} {
		script, err := buildRemoteScript(map[string]any{"op": "run", "command": tc.command})
		if err != nil {
			t.Fatal(err)
		}
		resp, err := runRemoteHelperScript(t, py, script, home)
		if err != nil || !resp.OK {
			t.Fatalf("command failed: %+v %v", resp, err)
		}
		var data CommandResult
		if err := json.Unmarshal(resp.Data, &data); err != nil {
			t.Fatal(err)
		}
		got, err := filepath.EvalSymlinks(strings.TrimSpace(data.Output))
		want, wantErr := filepath.EvalSymlinks(tc.want)
		if err != nil || wantErr != nil || data.ExitCode != 0 || got != want {
			t.Fatalf("directory got=%q want=%q: %+v %v", got, want, data, err)
		}
	}
}

func TestRemoteServerOnlyFileValidationPrecedesConnection(t *testing.T) {
	t.Parallel()
	a := newIsolatedToolTestApp(t)
	_, err := a.remoteWriteRaw(context.Background(), remoteTarget{Host: "example.invalid"}, "relative.txt", []byte("data"), true, false)
	if err == nil || !strings.Contains(err.Error(), "absolute") {
		t.Fatalf("relative path reached transport: %v", err)
	}
}
