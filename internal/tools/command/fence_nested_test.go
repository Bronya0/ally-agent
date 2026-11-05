package command

import (
	"strings"
	"testing"
	"time"
)

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
