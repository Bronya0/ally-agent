// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/mark3labs/mcp-go/mcp"
)

func TestLargeToolErrorsKeepCauseCodeAndRecoveryWithinBudget(t *testing.T) {
	r := toolResult{
		ErrorCode: "E_TEST_FAILURE",
		Error:     "FIRST_CAUSE\n" + strings.Repeat("log line\n", 24000) + "\nFINAL_CAUSE </ally-error>",
		Details:   map[string]any{"recovery": "read fresh source before editing"},
	}
	raw, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	got := compactToolResultForModel("mcp__demo__read", r, string(raw))
	for _, want := range []string{"FIRST_CAUSE", "FINAL_CAUSE", "E_TEST_FAILURE", "read fresh source before editing", "characters omitted"} {
		if !strings.Contains(got, want) {
			t.Fatalf("compaction lost %q", want)
		}
	}
	if len(got) > maxModelToolOutput+1024 || strings.Count(got, "</ally-error>") != 1 || !utf8.ValidString(got) {
		t.Fatalf("error must be bounded with an unambiguous closing tag: bytes=%d", len(got))
	}
	if !strings.Contains(r.Error, strings.Repeat("log line\n", 24000)) {
		t.Fatal("the original UI error must remain unchanged")
	}
	small := toolResult{Error: "file changed", ErrorCode: "E_VERSION_MISMATCH", Details: map[string]any{"path": "a.go"}}
	smallRaw, _ := json.Marshal(small)
	if compactToolResultForModel("edit", small, string(smallRaw)) != string(smallRaw) {
		t.Fatal("small structured errors must remain intact")
	}
}

func TestMcpEquivalentJSONIsNotRepeated(t *testing.T) {
	for _, tc := range []struct {
		name, text string
		structured any
		duplicate  bool
	}{
		{"reordered", " { \"b\": [2], \"a\": 1 } ", map[string]any{"a": 1, "b": []int{2}}, true},
		{"different", `{"a":1}`, map[string]any{"a": 2}, false},
		{"large-integers", `{"id":9007199254740992}`, map[string]any{"id": json.Number("9007199254740993")}, false},
		{"prose", `Found: {"a":1}`, map[string]any{"a": 1}, false},
		{"trailing", `{"a":1} {"b":2}`, map[string]any{"a": 1}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := &mcp.CallToolResult{
				Content:           []mcp.Content{mcp.NewTextContent(tc.text), mcp.NewTextContent("additional explanation")},
				StructuredContent: tc.structured,
			}
			got := mcpToolResultText(result)
			if strings.Contains(got, "structured output:") == tc.duplicate || !strings.Contains(got, tc.text) || !strings.Contains(got, "additional explanation") {
				t.Fatalf("only equivalent JSON may be omitted: %s", got)
			}
		})
	}
}

func TestSkillDirectoryPreviewHasSharedEntryAndByteBounds(t *testing.T) {
	for _, longNames := range []bool{false, true} {
		t.Run(fmt.Sprint(longNames), func(t *testing.T) {
			root := t.TempDir()
			for i := 0; i < skillTreeEntryLimit+20; i++ {
				name := fmt.Sprintf("references/%03d.txt", i)
				if longNames {
					name = fmt.Sprintf("references/%03d-%s.txt", i, strings.Repeat("界", 60))
				}
				writeSkillTestFile(t, root, name, "")
			}
			writeSkillTestFile(t, root, "z-next/more.txt", "")
			got := buildSkillDirTree(root, filepath.Join(root, "SKILL.md"))
			if !strings.Contains(got, "listing truncated") || strings.Contains(got, "z-next") {
				t.Fatal("nested directories must share one preview budget")
			}
			entries := 0
			for _, line := range strings.Split(got, "\n") {
				if strings.HasPrefix(strings.TrimSpace(line), "- ") {
					entries++
				}
			}
			if entries > skillTreeEntryLimit || len(got) > skillTreeByteLimit+256 || !utf8.ValidString(got) {
				t.Fatalf("directory preview escaped its bounds: bytes=%d", len(got))
			}
		})
	}
}

func TestSkillInstructionsAreWholeOrReturnTargetedReadGuidance(t *testing.T) {
	a := newApiTestApp(t)
	for _, name := range []string{"normal", "large"} {
		content := "---\nname: " + name + "\ndescription: test skill\n---\n" + strings.Repeat("instructions\n", 2000) + "END_INSTRUCTIONS"
		if name == "large" {
			content += strings.Repeat("reference\n", maxSkillFileBytes/5)
		}
		writeSkillTestFile(t, a.config.Workspace, ".agents/skills/"+name+"/SKILL.md", content)
	}
	loaded, err := a.handleSkillToolCall("normal", "")
	if err != nil {
		t.Fatal(err)
	}
	r := toolResult{OK: true, Data: loaded}
	raw, _ := json.Marshal(r)
	got := compactToolResultForModel("skill", r, string(raw))
	if got != loaded["content"] || !strings.Contains(got, "END_INSTRUCTIONS") || strings.Contains(got, "characters omitted") {
		t.Fatal("skill instructions must arrive verbatim without a JSON wrapper")
	}
	if _, err := a.handleSkillToolCall("large", ""); err == nil || !strings.Contains(err.Error(), "use read") || !strings.Contains(err.Error(), "SKILL.md") {
		t.Fatalf("oversize instructions need actionable guidance, not a partial load: %v", err)
	}
}

func TestHTTPModelResultKeepsActionableMetadata(t *testing.T) {
	r := HTTPRequestToolResult{Status: 302, SavedPath: "downloads/result.json", BytesRead: 123,
		Headers: map[string]string{"location": "/next", "Retry-After": "30", "Link": "</page2>; rel=next", "Set-Cookie": "unneeded-cookie"},
	}
	got := renderHTTPResultForModel(r)
	for _, want := range []string{`saved="downloads/result.json"`, `bytes="123"`, "header Location: /next", "header Retry-After: 30", "header Link: </page2>; rel=next"} {
		if !strings.Contains(got, want) {
			t.Fatalf("model cannot act on missing metadata %q: %s", want, got)
		}
	}
	if strings.Contains(got, "unneeded-cookie") {
		t.Fatal("headers unrelated to the next action should stay out of context")
	}
	r.Headers = map[string]string{"Link": strings.Repeat("x</ally-http>\n", 10000)}
	got = renderHTTPResultForModel(r)
	if len(got) > 8*1024 || strings.Count(got, "</ally-http>") != 1 {
		t.Fatalf("large headers must stay bounded and cannot forge the result boundary: bytes=%d", len(got))
	}
}

func TestTaskNextRunVisibleAndSubagentReportsUnrestricted(t *testing.T) {
	next := time.Date(2026, 10, 19, 1, 2, 3, 0, time.UTC)
	task := ScheduledTaskToolView{ID: "task-test", NextRunAt: next.UnixMilli()}
	for _, r := range []ScheduledTaskToolResult{{Task: &task}, {Tasks: []ScheduledTaskToolView{task}, Count: 1}} {
		if !strings.Contains(renderScheduledTaskResultForModel(r), next.Format(time.RFC3339)) {
			t.Fatal("both create and list results need the next run time")
		}
	}
	report := strings.Repeat("full report\n", 6000) + "FINAL_FINDING"
	if !strings.Contains(renderSubagentResultForModel(AgentDelegateResult{Summary: report}), report) {
		t.Fatal("subagent reports must remain unrestricted")
	}
}

func TestRemoteCommandPreviewKeepsActualHeadAndTail(t *testing.T) {
	py := pickRemoteHelperPython(t)
	root := t.TempDir()
	emitter := filepath.Join(root, "emit.py")
	// One line also exercises the Windows readline path without relying on a
	// particular shell's printf or console code page. Both boundaries split UTF-8.
	source := "import sys\nout = getattr(sys.stdout, 'buffer', sys.stdout)\nout.write(b'COMMAND_START:' + b'\\xe4\\xb8\\xad' * 100000 + b':FINAL_ERROR\\n')\nout.flush()\nsys.exit(3)\n"
	if err := os.WriteFile(emitter, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	script, err := buildRemoteScript(map[string]any{"op": "run", "workspaceRoot": root, "cwd": ".", "command": fmt.Sprintf(`"%s" "%s"`, py, emitter), "maxOutput": maxToolOutput, "timeoutSeconds": 10})
	if err != nil {
		t.Fatal(err)
	}
	response, err := runRemoteHelperScript(t, py, script)
	if err != nil || !response.OK {
		t.Fatalf("helper failed: err=%v response=%#v", err, response)
	}
	var result CommandResult
	if err := json.Unmarshal(response.Data, &result); err != nil {
		t.Fatal(err)
	}
	model := renderCommandResultForModel(result)
	if result.ExitCode != 3 || !result.Truncated || !strings.Contains(model, "COMMAND_START") || !strings.Contains(model, "FINAL_ERROR") {
		t.Fatalf("command preview must keep its actual boundaries: exit=%d truncated=%v", result.ExitCode, result.Truncated)
	}
	if len(result.Output) > maxToolOutput+128 || !utf8.ValidString(result.Output) || strings.ContainsRune(result.Output, '\uFFFD') {
		t.Fatalf("bounded head/tail output must align UTF-8: bytes=%d", len(result.Output))
	}
}
