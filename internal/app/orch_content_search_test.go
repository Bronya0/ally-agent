// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ally-dev/internal/tools/grep"
	toolshared "ally-dev/internal/tools/shared"
	openai "github.com/sashabaranov/go-openai"
)

func TestGrepToolIsRemoved(t *testing.T) {
	a := newIsolatedChatTestApp(t)
	for _, tools := range [][]openai.Tool{
		toolshared.Builtins(),
		a.buildToolsForSession("search", a.config),
		a.subagentTools(a.config),
	} {
		for _, tool := range tools {
			if tool.Function != nil && tool.Function.Name == "grep" {
				t.Fatal("removed grep tool is still registered")
			}
		}
	}
	if _, exists := toolshared.BuiltinSchema("grep"); exists || toolshared.IsBuiltinTool("grep") {
		t.Fatal("removed grep still has a builtin schema")
	}
	result := a.executeTool(context.Background(), a.config, "", "grep", []byte("{}"))
	if result.OK || result.Error != "unknown tool: grep" || result.Data != nil {
		t.Fatalf("removed tool executed: %+v", result)
	}
}

func TestLocalGrepCommandsAreBlockedBeforeExecution(t *testing.T) {
	a := newApiTestApp(t)
	cfg := a.config
	command := "printf ran > marker.txt; grep needle file.txt"
	args, err := json.Marshal(CommandRequest{Command: command})
	if err != nil {
		t.Fatal(err)
	}
	result := a.executeTool(context.Background(), cfg, "", "command", args)
	if result.OK || result.ErrorCode != "E_COMMAND_BLOCKED" || result.Data != nil {
		t.Fatalf("grep command was not rejected: %+v", result)
	}
	if _, err := os.Stat(filepath.Join(cfg.Workspace, "marker.txt")); !os.IsNotExist(err) {
		t.Fatalf("command prefix ran before rejection: %v", err)
	}
	if _, err := a.startServiceWithConfig(context.Background(), cfg, StartServiceRequest{
		Command: command, Description: "search",
	}); toolErrorCode(err) != "E_COMMAND_BLOCKED" {
		t.Fatalf("service bypassed grep restriction: %v", err)
	}
}

func TestRemoteSearchUsesServerTools(t *testing.T) {
	for _, cmd := range []string{
		"grep -nE 'needle|other' /srv/app/main.go",
		"sh -c 'grep needle /srv/app/main.go'",
		"egrep needle /srv/app/main.go",
		"fgrep needle /srv/app/main.go",
		"rg -n needle /srv/app/main.go",
		"git log --grep=needle",
	} {
		if err := validateRemoteCommandSafety(cmd); err != nil {
			t.Fatalf("remote search %q was rejected: %v", cmd, err)
		}
	}
	if err := validateRemoteCommandSafety("grep needle /srv/app/main.go; mkfs.ext4 /dev/sda"); toolErrorCode(err) != "E_COMMAND_BLOCKED" {
		t.Fatalf("allowing remote grep bypassed the risk fence: %v", err)
	}
}

func TestRipgrepContentSearchThroughCommand(t *testing.T) {
	if _, err := grep.Find(); err != nil {
		t.Skip("ripgrep is not installed")
	}
	a := newApiTestApp(t)
	dir := filepath.Join(a.config.Workspace, "src")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"alpha.go":     "Alpha\nbeta\n",
		"beta_test.go": "BETA\n",
		"ignored.txt":  "Alpha\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		pattern string
		exit    int
	}{
		{"alpha|beta", 0},
		{"no_such_symbol", 1},
	} {
		args, err := json.Marshal(CommandRequest{
			Command: "rg -p --color never -i -g '*.go' -e '" + tc.pattern + "' -- src",
		})
		if err != nil {
			t.Fatal(err)
		}
		result := a.executeTool(context.Background(), a.config, "", "command", args)
		output, ok := result.Data.(CommandResult)
		if !result.OK || !ok || output.ExitCode != tc.exit {
			t.Fatalf("pattern=%q result=%+v", tc.pattern, result)
		}
		if tc.exit == 0 {
			for _, want := range []string{"alpha.go\n", "1:Alpha", "2:beta", "beta_test.go\n", "1:BETA"} {
				if !strings.Contains(output.Output, want) {
					t.Fatalf("missing %q from search output: %q", want, output.Output)
				}
			}
			if strings.Contains(output.Output, "ignored.txt") || strings.Contains(output.Output, "\x1b[") {
				t.Fatalf("file filter or plain output failed: %q", output.Output)
			}
		} else if output.Output != "" {
			t.Fatalf("no-match search returned output: %q", output.Output)
		}
	}
}
