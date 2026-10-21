package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestLocalCommandToolWarningsUseExecutablePositions(t *testing.T) {
	for _, tc := range []struct {
		command string
		warn    bool
	}{
		{`grep needle file.txt`, true},
		{`printf needle | grep needle | grep other`, true},
		{`env LC_ALL=C /usr/bin/grep needle file.txt`, true},
		{`bash -c 'rg needle .'`, true},
		{`fgrep needle file.txt`, true},
		{`egrep needle file.txt`, true},
		{`grep.exe needle file.txt`, true},
		{`git log --grep=needle`, false},
		{`printf 'grep needle file.txt'`, false},
		{`echo rg`, false},
		{"cat <<'EOF'\ngrep needle file.txt\nEOF", false},
	} {
		t.Run(tc.command, func(t *testing.T) {
			warnings := localCommandToolWarnings(tc.command)
			if (len(warnings) > 0) != tc.warn || len(warnings) > 1 {
				t.Fatalf("unexpected warnings: %v", warnings)
			}
		})
	}
}

func TestCommandSearchWarningReachesModelWithoutChangingExitCode(t *testing.T) {
	a := newApiTestApp(t)
	for _, commandLine := range []string{`printf needle | grep needle`, `printf needle | grep absent`} {
		args, err := json.Marshal(CommandRequest{Command: commandLine, Timeout: 10})
		if err != nil {
			t.Fatal(err)
		}
		result := a.executeTool(context.Background(), a.config, "", "command", args)
		if !result.OK || len(result.Warnings) != 1 {
			t.Fatalf("expected successful execution with one reminder: %+v", result)
		}
		data := result.Data.(CommandResult)
		if (data.ExitCode == 1) != strings.Contains(commandLine, "absent") {
			t.Fatalf("search exit code changed: %d", data.ExitCode)
		}
		full, _ := json.Marshal(result)
		model := compactToolResultForModel("command", result, string(full))
		if !strings.Contains(model, "Prefer the dedicated grep tool") {
			t.Fatalf("model reminder missing: %s", model)
		}
	}
}
