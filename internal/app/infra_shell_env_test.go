package app

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestProbeLoginShellPathUsesLoginShellAndAbsoluteEnv(t *testing.T) {
	var gotFile string
	var gotArgs []string
	var gotTimeout time.Duration
	got := probeLoginShellPath(loginShellPathProbeDeps{
		platform: "darwin",
		env:      []string{"SHELL=/bin/zsh"},
		execFileText: func(file string, args []string, timeout time.Duration) (string, error) {
			gotFile = file
			gotArgs = append([]string(nil), args...)
			gotTimeout = timeout
			return "HOME=/Users/test\nPATH=/opt/homebrew/bin:/usr/bin:/bin\nTERM=dumb\n", nil
		},
	})
	if got != "/opt/homebrew/bin:/usr/bin:/bin" {
		t.Fatalf("unexpected login-shell PATH %q", got)
	}
	if gotFile != "/bin/zsh" || !reflect.DeepEqual(gotArgs, []string{"-l", "-c", "/usr/bin/env"}) {
		t.Fatalf("unexpected probe invocation: file=%q args=%q", gotFile, gotArgs)
	}
	if gotTimeout != loginShellEnvTimeout {
		t.Fatalf("expected timeout %s, got %s", loginShellEnvTimeout, gotTimeout)
	}
}

func TestProbeLoginShellPathKeepsLastPathLineAndIgnoresFailures(t *testing.T) {
	deps := loginShellPathProbeDeps{
		platform: "darwin",
		env:      []string{"SHELL=/bin/zsh"},
		execFileText: func(string, []string, time.Duration) (string, error) {
			return "PATH=/from-profile-noise\nprofile banner\nPATH=/real/bin:/usr/bin\r\n", nil
		},
	}
	if got := probeLoginShellPath(deps); got != "/real/bin:/usr/bin" {
		t.Fatalf("expected last PATH line, got %q", got)
	}

	deps.execFileText = func(string, []string, time.Duration) (string, error) {
		return "", errors.New("shell failed")
	}
	if got := probeLoginShellPath(deps); got != "" {
		t.Fatalf("expected failed probe to return empty PATH, got %q", got)
	}

	deps.execFileText = func(string, []string, time.Duration) (string, error) {
		return "HOME=/Users/test\nTERM=dumb\n", nil
	}
	if got := probeLoginShellPath(deps); got != "" {
		t.Fatalf("expected missing PATH to return empty PATH, got %q", got)
	}
}

func TestProbeLoginShellPathFallsBackToAccountShellAndSkipsWindows(t *testing.T) {
	called := false
	deps := loginShellPathProbeDeps{
		platform: "darwin",
		env:      []string{"SHELL=   "},
		userShell: func() string {
			return "/bin/zsh"
		},
		execFileText: func(file string, args []string, timeout time.Duration) (string, error) {
			called = true
			if file != "/bin/zsh" || !reflect.DeepEqual(args, []string{"-l", "-c", "/usr/bin/env"}) || timeout != loginShellEnvTimeout {
				t.Fatalf("unexpected fallback invocation: %q %q %s", file, args, timeout)
			}
			return "PATH=/opt/homebrew/bin:/usr/bin\n", nil
		},
	}
	if got := probeLoginShellPath(deps); got != "/opt/homebrew/bin:/usr/bin" || !called {
		t.Fatalf("expected account-shell fallback, got path=%q called=%v", got, called)
	}

	called = false
	deps.platform = "windows"
	if got := probeLoginShellPath(deps); got != "" || called {
		t.Fatalf("Windows must skip login-shell probing: path=%q called=%v", got, called)
	}
}

func TestMergeLoginShellPathPreservesCurrentPriorityAndRelativeEntries(t *testing.T) {
	current := "/usr/bin:/bin"
	if got := mergeLoginShellPath(&current, "/opt/homebrew/bin:/usr/bin:/extra/bin"); got != "/usr/bin:/bin:/opt/homebrew/bin:/extra/bin" {
		t.Fatalf("unexpected merged PATH %q", got)
	}

	current = "/a::/b:/a:"
	if got := mergeLoginShellPath(&current, "/b:/a"); got != current {
		t.Fatalf("no-op merge changed PATH from %q to %q", current, got)
	}

	for _, test := range []struct {
		current string
		login   string
		want    string
	}{
		{current: ":/usr/bin", login: "/new", want: ":/usr/bin:/new"},
		{current: "/usr/bin:", login: "/new", want: "/usr/bin::/new"},
		{current: "", login: "/new", want: ":/new"},
		{current: "/a", login: ":/b::/a:../x:/c", want: "/a:/b:/c"},
	} {
		current := test.current
		if got := mergeLoginShellPath(&current, test.login); got != test.want {
			t.Errorf("merge(%q, %q) = %q, want %q", test.current, test.login, got, test.want)
		}
	}

	if got := mergeLoginShellPath(nil, "/a:/b:/a"); got != "/a:/b" {
		t.Fatalf("unexpected unset PATH merge %q", got)
	}
}

func TestEnrichLoginShellPathEnvironmentOnlyChangesPath(t *testing.T) {
	base := []string{
		"PATH=/usr/bin:/bin",
		"SHELL=/bin/zsh",
		"GOPATH=/login-only",
		"NVM_DIR=/login-only/.nvm",
	}
	got := enrichLoginShellPathEnvironment(
		base,
		"darwin",
		nil,
		func(string, []string, time.Duration) (string, error) {
			return "PATH=/opt/homebrew/bin:/usr/bin\nGOPATH=/other\n", nil
		},
	)
	if path, _ := environmentValue(got, "PATH"); path != "/usr/bin:/bin:/opt/homebrew/bin" {
		t.Fatalf("unexpected enriched PATH %q", path)
	}
	if value, _ := environmentValue(got, "GOPATH"); value != "/login-only" {
		t.Fatalf("login-shell GOPATH leaked into command env: %q", value)
	}
	if value, _ := environmentValue(got, "NVM_DIR"); value != "/login-only/.nvm" {
		t.Fatalf("existing NVM_DIR changed: %q", value)
	}
	if len(got) != len(base) {
		t.Fatalf("expected only PATH replacement, got %#v", got)
	}
}
