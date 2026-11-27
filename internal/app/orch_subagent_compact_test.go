// SPDX-License-Identifier: GPL-3.0-only
package app

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	openai "github.com/sashabaranov/go-openai"
)

func TestSubagentCompactsAndContinuesWithoutResettingBudget(t *testing.T) {
	const checkpoint = "checkpoint: first tool a success"
	const report = "report: delegated work stopped at the expected point"
	const task = "Verify the product configuration"
	const parentID = "parent-session"
	const parentText = "unrelated main conversation"

	for _, tc := range []struct {
		name      string
		budget    int
		steps     int
		stop      string
		wantCalls int
	}{
		{name: "threshold_at_report", budget: 1, steps: 1, stop: "step_limit", wantCalls: 4},
		{name: "overflow_during_work", budget: 3, steps: 2, wantCalls: 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			workspace := t.TempDir()
			first := sseChatToolCallChunk("initial-create", "create", `{"path":"kept.txt","content":"created","overwrite":false,"purpose":"Create test file"}`) + sseChatFinishChunk("tool_calls")
			var replies []string
			if tc.stop == "step_limit" {
				first += `data: {"choices":[],"usage":{"prompt_tokens":60000,"completion_tokens":5,"total_tokens":60005}}` + "\n\n"
				replies = []string{first + sseDone, subagentTextReply(checkpoint), subagentToolCallReply("create", `{"path":"forbidden.txt","content":"must not execute","overwrite":false}`), subagentTextReply(report)}
			} else {
				replies = []string{first + sseDone, overflowLengthError, subagentTextReply(checkpoint), subagentTextReply(report)}
			}
			h := newSubagentReportHarness(t, replies...)
			a := newIsolatedTestApp(t)
			a.stats = nil
			a.saveHistory(parentID, []openai.ChatCompletionMessage{{Role: openai.ChatMessageRoleUser, Content: parentText}})
			cfg := subagentReportBaseConfig(h.server.URL, workspace)
			cfg.CompactThresholdMode, cfg.CompactThresholdTokens = CompactThresholdModeTokens, 50_000
			tools := []openai.Tool{subagentReportTool("create")}
			result, err := a.executeDelegate(t.Context(), cfg, parentID, AgentDelegateRequest{Task: task, CleanContext: true, MaxSteps: tc.budget, tools: tools})
			if err != nil || result == nil || result.Status != "completed" || result.Steps != tc.steps || result.StopReason != tc.stop || result.Summary != report {
				t.Fatalf("result=%+v err=%v; want %d steps, stop=%q, the scripted final report", result, err, tc.steps, tc.stop)
			}
			if data, err := os.ReadFile(filepath.Join(workspace, "kept.txt")); err != nil || string(data) != "created" {
				t.Fatalf("budgeted tool effects missing after compaction: data=%q err=%v", data, err)
			}
			if _, err := os.Stat(filepath.Join(workspace, "forbidden.txt")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("a tool ran after exhaustion: stat=%v", err)
			}
			sent := h.recorded()
			if len(sent) != tc.wantCalls {
				t.Fatalf("requests=%d, want %d", len(sent), tc.wantCalls)
			}
			var summarized, resumed bool
			for _, request := range sent[1:] {
				var hasCheckpoint, hasOldTool, hasTask bool
				for _, m := range request.Messages {
					hasCheckpoint = hasCheckpoint || m.Content == checkpoint
					hasOldTool = hasOldTool || m.ToolCallID == "initial-create"
					hasTask = hasTask || m.Content == "## Task\n"+task
				}
				if len(request.Tools) == 0 && hasOldTool {
					summarized = true
				}
				if len(request.Tools) == 1 && hasCheckpoint && hasTask && !hasOldTool {
					resumed = true
				}
			}
			if !summarized || !resumed {
				t.Fatalf("did not summarize the old tool history and resume with the original task and tool list: summary=%v resume=%v", summarized, resumed)
			}
			if saved := a.loadSessionHistoryCopy(parentID); len(saved) != 1 || saved[0].Content != parentText {
				t.Fatalf("child compaction modified the main history: %#v", saved)
			}
		})
	}
}
