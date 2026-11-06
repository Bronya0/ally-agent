package command

import (
	"strings"
	"testing"
	"time"
)

func TestNestedShellStopsAtScriptArguments(t *testing.T) {
	for _, line := range []string{
		`bash ./build.sh -config 'echo text > .git/hooks/pre-commit; rm generated.txt'`,
		`sh script.sh -c 'echo text > .git/hooks/pre-commit; rm generated.txt'`,
		`bash -- -c 'echo text > .git/hooks/pre-commit; rm generated.txt'`,
		`bash -s -- -lc 'echo text > .git/hooks/pre-commit; rm generated.txt'`,
		`bash --rcfile -config ./build.sh 'echo text > .git/hooks/pre-commit; rm generated.txt'`,
	} {
		if got := ShellRedirectionTargets(line); len(got) != 0 {
			t.Errorf("%q: script data produced write targets %v", line, got)
		}
		if ContainsExplicitDeleteCommand(line) {
			t.Errorf("%q: script data was treated as a delete command", line)
		}
	}
}

func TestNestedShellSkipsOptionValues(t *testing.T) {
	for _, line := range []string{
		`bash --rcfile init.sh -lc 'rm generated.txt > result.txt'`,
		`bash --init-file init.sh -c 'rm generated.txt > result.txt'`,
		`bash -o noclobber -c 'rm generated.txt > result.txt'`,
		`bash -eo noclobber -c 'rm generated.txt > result.txt'`,
		`bash -O extglob -lc 'rm generated.txt > result.txt'`,
		`sh +o noclobber -ec 'rm generated.txt > result.txt'`,
	} {
		if !ContainsExplicitDeleteCommand(line) {
			t.Errorf("%q: nested delete was missed", line)
		}
		got := ShellRedirectionTargets(line)
		if len(got) != 1 || got[0] != "result.txt" {
			t.Errorf("%q: nested write targets = %v", line, got)
		}
	}
}

func TestShellRedirectionTargetsInsideNestedShell(t *testing.T) {
	for _, line := range []string{
		`sh -c "echo evil > .git/hooks/pre-commit"`,
		`bash -lc "echo evil > .git/hooks/pre-commit"`,
		`sh -ec "echo evil > .git/hooks/pre-commit"`,
	} {
		found := false
		for _, target := range ShellRedirectionTargets(line) {
			if target == ".git/hooks/pre-commit" {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("%q: redirection target not found in %v", line, ShellRedirectionTargets(line))
		}
	}
}

func TestShellRedirectionTargetsInsideEval(t *testing.T) {
	for _, line := range []string{
		`eval 'echo evil > .git/hooks/pre-commit'`,
		`eval "echo evil > .git/hooks/pre-commit"`,
	} {
		found := false
		for _, target := range ShellRedirectionTargets(line) {
			if target == ".git/hooks/pre-commit" {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("%q: redirection target not found in %v", line, ShellRedirectionTargets(line))
		}
	}
}

// Deeply nested scripting must not turn the fence into an exponential walk.
func TestNestedEvalStopsAtDepthLimit(t *testing.T) {
	line := strings.Repeat("eval '", 26) + "echo x > .git/hooks/pre-commit" + strings.Repeat("'", 26)
	start := time.Now()
	_ = ShellRedirectionTargets(line)
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("nested eval took %s", elapsed)
	}
}

func TestContainsExplicitDeleteCommandForMergedShellOptions(t *testing.T) {
	for _, line := range []string{
		`bash -c "rm generated.txt"`,
		`bash -lc "rm generated.txt"`,
		`sh -ec "rm generated.txt"`,
		`zsh -ic "rm generated.txt"`,
	} {
		if !ContainsExplicitDeleteCommand(line) {
			t.Fatalf("%q: explicit delete not detected", line)
		}
	}
}
