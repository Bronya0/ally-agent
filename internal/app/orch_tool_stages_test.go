// SPDX-License-Identifier: GPL-3.0-only
package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	openai "github.com/sashabaranov/go-openai"
)

func stagedCall(name, args string) openai.ToolCall {
	return openai.ToolCall{Function: openai.FunctionCall{Name: name, Arguments: args}}
}

func TestToolCallStage(t *testing.T) {
	for _, tc := range []struct {
		args string
		want int64
		bad  bool
	}{
		{`{}`, 0, false}, {`{"stage":0}`, 0, false}, {`{"stage":9}`, 9, false},
		{`{"stage":2147483647}`, 2147483647, false},
		{`{"stage":-1}`, 0, true}, {`{"stage":1.5}`, 0, true},
		{`{"stage":"2"}`, 0, true}, {`{"stage":null}`, 0, true},
		{`{"stage":2147483648}`, 0, true}, {`{"stage":`, 0, true},
	} {
		t.Run(tc.args, func(t *testing.T) {
			got, err := toolCallStage(stagedCall("command", tc.args))
			if got != tc.want || (err != nil) != tc.bad {
				t.Fatalf("stage = %d, %v", got, err)
			}
		})
	}
	if got, err := toolCallStage(stagedCall("mcp__server__tool", `{"stage":"server-owned"}`)); got != 0 || err != nil {
		t.Fatalf("MCP arguments must not be interpreted: %d, %v", got, err)
	}
}

func TestStagedBatchOrdersByStageAndPreservesArguments(t *testing.T) {
	calls := []openai.ToolCall{
		stagedCall("wait", `{"seconds":1,"stage":9}`),
		stagedCall("command", `{"command":"install","stage":4}`),
		stagedCall("edit", `{"stage":0}`),
	}
	var order []string
	results := make([]toolResult, len(calls))
	a := &App{}
	a.executeStagedToolBatch(context.Background(), ConfigState{ToolConcurrency: 4}, calls, nil,
		func(_ context.Context, i int, call openai.ToolCall) toolResult {
			if call.Function.Arguments != calls[i].Function.Arguments {
				return toolErrorResult(fmt.Errorf("stage was stripped"))
			}
			order = append(order, call.Function.Name)
			return toolResult{OK: true}
		}, func(i int, _ openai.ToolCall, r toolResult, _ int64) { results[i] = r })
	if !reflect.DeepEqual(order, []string{"edit", "command", "wait"}) {
		t.Fatalf("unexpected execution order: %v", order)
	}
	for _, result := range results {
		if !result.OK {
			t.Fatal(result.Error)
		}
	}
}

func TestStagedBatchParallelFailureDrainsPeersBeforeSkipping(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	calls := []openai.ToolCall{
		stagedCall("command", `{}`), stagedCall("command", `{}`),
		stagedCall("service", `{"stage":1}`),
	}
	started := make(chan int, 3)
	release := make(chan struct{})
	done := make(chan struct{})
	results := make([]toolResult, len(calls))
	go func() {
		defer close(done)
		a := &App{}
		a.executeStagedToolBatch(ctx, ConfigState{ToolConcurrency: 2}, calls, nil,
			func(ctx context.Context, i int, _ openai.ToolCall) toolResult {
				started <- i
				select {
				case <-release:
				case <-ctx.Done():
					return toolErrorResult(ctx.Err())
				}
				return toolResult{OK: true, Data: CommandResult{ExitCode: i * 3, Output: "preserved"}}
			}, func(i int, _ openai.ToolCall, r toolResult, _ int64) { results[i] = r })
	}()
	// Both calls must start before either can complete: this proves concurrency
	// without timing guesses, and a deadline makes a serial implementation fail.
	for range 2 {
		select {
		case <-started:
		case <-ctx.Done():
			t.Fatal("same-stage calls did not execute concurrently")
		}
	}
	close(release)
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("batch did not finish")
	}
	if len(started) != 0 || results[2].ErrorCode != "E_STAGE_BLOCKED" {
		t.Fatalf("later stage executed: %+v", results)
	}
	if data, ok := results[1].Data.(CommandResult); !ok || data.ExitCode != 3 || data.Output != "preserved" {
		t.Fatalf("failed command output was lost: %+v", results[1])
	}
}

func TestStagedBatchFailureStillExecutesQueuedPeers(t *testing.T) {
	calls := []openai.ToolCall{stagedCall("command", `{}`), stagedCall("wait", `{}`), stagedCall("service", `{"stage":2}`)}
	var count atomic.Int32
	results := make([]toolResult, len(calls))
	a := &App{}
	a.executeStagedToolBatch(context.Background(), ConfigState{ToolConcurrency: 1}, calls, nil,
		func(_ context.Context, _ int, _ openai.ToolCall) toolResult {
			count.Add(1)
			return toolErrorResult(fmt.Errorf("failed"))
		}, func(i int, _ openai.ToolCall, r toolResult, _ int64) { results[i] = r })
	if count.Load() != 2 || results[2].ErrorCode != "E_STAGE_BLOCKED" {
		t.Fatalf("stage did not drain: count=%d results=%+v", count.Load(), results)
	}
}

func TestStagedBatchDuplicateNoticeDoesNotMaskOriginalOutcome(t *testing.T) {
	for _, originalFails := range []bool{false, true} {
		t.Run(fmt.Sprintf("originalFails=%v", originalFails), func(t *testing.T) {
			calls := []openai.ToolCall{
				stagedCall("calculate", `{"expression":"1+1"}`),
				stagedCall("calculate", `{"expression":"1+1","stage":0}`),
				stagedCall("command", `{"stage":1}`),
			}
			conflicts := detectToolBatchConflicts(ConfigState{}, calls)
			if toolErrorCode(conflicts[1]) != "E_DUPLICATE_TOOL_CALL" {
				t.Fatalf("expected duplicate: %v", conflicts)
			}
			results := make([]toolResult, len(calls))
			a := &App{}
			a.executeStagedToolBatch(context.Background(), ConfigState{}, calls, conflicts,
				func(_ context.Context, i int, _ openai.ToolCall) toolResult {
					if i == 1 {
						t.Error("duplicate executed")
					}
					return toolResult{OK: !(i == 0 && originalFails)}
				}, func(i int, _ openai.ToolCall, r toolResult, _ int64) { results[i] = r })
			if results[2].OK == originalFails || results[1].ErrorCode != "E_DUPLICATE_TOOL_CALL" {
				t.Fatalf("duplicate changed original stage outcome: %+v", results)
			}
		})
	}
}

func TestStagedBatchRetainsExclusiveConflictAcrossStages(t *testing.T) {
	calls := []openai.ToolCall{stagedCall("ask", `{}`), stagedCall("command", `{"stage":2}`)}
	conflicts := detectToolBatchConflicts(ConfigState{}, calls)
	results := make([]toolResult, len(calls))
	a := &App{}
	a.executeStagedToolBatch(context.Background(), ConfigState{}, calls, conflicts,
		func(context.Context, int, openai.ToolCall) toolResult {
			t.Error("exclusive batch executed")
			return toolResult{OK: true}
		},
		func(i int, _ openai.ToolCall, r toolResult, _ int64) { results[i] = r })
	for _, result := range results {
		if result.ErrorCode != "E_ASK_BATCH_CONFLICT" {
			t.Fatalf("specific conflict overwritten: %+v", result)
		}
	}
}

func TestStagedBatchRejectsInvalidStageBeforeSideEffects(t *testing.T) {
	calls := []openai.ToolCall{stagedCall("command", `{}`), stagedCall("edit", `{"stage":-1}`)}
	results := make([]toolResult, len(calls))
	a := &App{}
	a.executeStagedToolBatch(context.Background(), ConfigState{}, calls, nil,
		func(context.Context, int, openai.ToolCall) toolResult {
			t.Error("invalid batch executed")
			return toolResult{OK: true}
		},
		func(i int, _ openai.ToolCall, r toolResult, _ int64) { results[i] = r })
	if results[0].ErrorCode != "E_STAGE_BLOCKED" || results[1].ErrorCode != "E_BAD_ARGS" {
		t.Fatalf("unexpected rejection: %+v", results)
	}
}

func TestStagedBatchCancellationDoesNotStartTools(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var count atomic.Int32
	a := &App{}
	a.executeStagedToolBatch(ctx, ConfigState{}, []openai.ToolCall{stagedCall("command", `{}`)}, nil,
		func(context.Context, int, openai.ToolCall) toolResult { count.Add(1); return toolResult{OK: true} },
		func(_ int, _ openai.ToolCall, r toolResult, _ int64) {
			if r.ErrorCode != "E_CANCELLED" {
				t.Errorf("unexpected cancellation: %+v", r)
			}
		})
	if count.Load() != 0 {
		t.Fatal("cancelled batch started a tool")
	}
}

func TestStagedBatchPublishesFileFailureBeforePeerFinishes(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	calls := []openai.ToolCall{
		stagedCall("create", `{"path":"failed.txt"}`),
		stagedCall("command", `{}`),
	}
	failed := make(chan struct{})
	releasePeer := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		a := &App{}
		a.executeStagedToolBatch(ctx, ConfigState{ToolConcurrency: 2}, calls, nil,
			func(ctx context.Context, i int, _ openai.ToolCall) toolResult {
				if i == 0 {
					return toolErrorResult(fmt.Errorf("write failed"))
				}
				select {
				case <-releasePeer:
				case <-ctx.Done():
				}
				return toolResult{OK: true}
			}, func(i int, _ openai.ToolCall, _ toolResult, _ int64) {
				if i == 0 {
					close(failed)
				}
			})
	}()
	select {
	case <-failed:
	case <-ctx.Done():
		t.Fatal("file failure waited for its peer")
	}
	close(releasePeer)
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("peer did not finish")
	}
}

func TestToolStageSucceeded(t *testing.T) {
	for _, data := range []any{
		CommandResult{ExitCode: 1}, CommandResult{TimedOut: true}, CommandResult{Cancelled: true},
		CommandResult{SandboxDenied: true}, CommandResult{PromotedToService: true},
		DeletePathsResult{FailedCount: 1}, BatchReadResult{Files: []BatchReadResultItem{{Error: "missing"}}},
	} {
		if toolStageSucceeded(toolResult{OK: true, Data: data}) {
			t.Errorf("failure accepted: %+v", data)
		}
	}
	if !toolStageSucceeded(toolResult{OK: true, Data: CommandResult{ExitCode: 0}}) {
		t.Fatal("exit 0 rejected")
	}
}

func TestStageLocalWriteConflicts(t *testing.T) {
	root := t.TempDir()
	call := func(stage int) openai.ToolCall {
		args, _ := json.Marshal(map[string]any{"path": "config.txt", "content": "x", "stage": stage})
		return stagedCall("create", string(args))
	}
	if conflicts := detectToolBatchConflicts(ConfigState{Workspace: root}, []openai.ToolCall{call(0), call(1)}); len(conflicts) != 0 {
		t.Fatalf("different stages must not conflict: %v", conflicts)
	}
	if conflicts := detectToolBatchConflicts(ConfigState{Workspace: root}, []openai.ToolCall{call(0), call(0)}); toolErrorCode(conflicts[1]) != "E_WRITE_BATCH_CONFLICT" {
		t.Fatalf("same-stage writes must conflict: %v", conflicts)
	}
}

func TestSubagentStepLimitBlocksDependentStage(t *testing.T) {
	for _, limited := range []bool{false, true} {
		t.Run(fmt.Sprintf("limited=%v", limited), func(t *testing.T) {
			a := newIsolatedTestApp(t)
			calls := []openai.ToolCall{stagedCall("subagent", `{"stage":0}`), stagedCall("calculate", `{"stage":1}`)}
			var results [2]toolResult
			executed := false
			a.executeStagedToolBatch(context.Background(), ConfigState{Workspace: t.TempDir()}, calls, nil,
				func(_ context.Context, index int, _ openai.ToolCall) toolResult {
					if index == 0 {
						result := AgentDelegateResult{Status: "completed"}
						if limited {
							result.StopReason = "step_limit"
						}
						return toolResult{OK: true, Data: result}
					}
					executed = true
					return toolResult{OK: true}
				}, func(index int, _ openai.ToolCall, result toolResult, _ int64) { results[index] = result })
			if executed == limited || (limited && results[1].ErrorCode != "E_STAGE_BLOCKED") {
				t.Fatalf("executed=%v, results=%+v", executed, results)
			}
		})
	}
}

func TestAgentLoopsStageFailureBlocksLaterWrites(t *testing.T) {
	for _, child := range []bool{false, true} {
		t.Run(fmt.Sprintf("child=%v", child), func(t *testing.T) {
			redirectAppStateDir(t)
			workspace := t.TempDir()
			reply := sseChatToolCallChunk("kept", "create", `{"path":"kept.txt","content":"kept","stage":0}`) +
				sseChatToolCallChunk("failed", "calculate", `{"expression":"1+","stage":0}`) +
				sseChatToolCallChunk("skipped", "create", `{"path":"skipped.txt","content":"must not exist","stage":1}`) +
				sseChatFinishChunk("tool_calls") + sseDone
			harness := newSubagentReportHarness(t, reply, subagentTextReply("Finished."))
			a := NewApp()
			a.initialized, a.stats = true, nil
			cfg := subagentReportBaseConfig(harness.server.URL, workspace)
			if child {
				_, err := a.executeDelegate(context.Background(), cfg, "stages-child", AgentDelegateRequest{
					Task: "exercise stages", CleanContext: true, MaxSteps: 3, Model: "test-model",
					tools: []openai.Tool{subagentReportTool("create"), subagentReportTool("calculate")},
				})
				if err != nil {
					t.Fatal(err)
				}
				for _, run := range a.GetSubagents() {
					for _, call := range run.ToolCalls {
						if call.ToolCallID == "skipped" {
							t.Fatal("skipped tool remained in the sub-agent snapshot")
						}
					}
				}
			} else {
				recorder := &batchOrderRecorder{done: make(chan struct{}, 1)}
				a.events = recorder
				if _, err := a.StartChat(ChatRequest{SessionID: "stages-main", Message: "exercise stages", Config: cfg}); err != nil {
					t.Fatal(err)
				}
				select {
				case <-recorder.done:
				case <-time.After(15 * time.Second):
					t.Fatal("run did not finish")
				}
			}
			if content, err := os.ReadFile(filepath.Join(workspace, "kept.txt")); err != nil || string(content) != "kept" {
				t.Fatalf("successful peer was lost: %q, %v", content, err)
			}
			if _, err := os.Stat(filepath.Join(workspace, "skipped.txt")); !os.IsNotExist(err) {
				t.Fatalf("blocked write executed: %v", err)
			}
			requests := harness.recorded()
			if len(requests) != 2 {
				t.Fatalf("unexpected model rounds: %d", len(requests))
			}
			var tools int
			var blocked bool
			for _, message := range requests[1].Messages {
				if message.Role == "tool" {
					tools++
					blocked = blocked || message.ToolCallID == "skipped" && containsStageBlocked(message.Content)
				}
			}
			if tools != 3 || !blocked {
				t.Fatalf("missing tool results or skip reason: %+v", requests[1].Messages)
			}
		})
	}
}

func containsStageBlocked(content string) bool {
	var result toolResult
	return json.Unmarshal([]byte(content), &result) == nil && result.ErrorCode == "E_STAGE_BLOCKED"
}
