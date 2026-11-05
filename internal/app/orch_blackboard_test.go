// SPDX-License-Identifier: GPL-3.0-only
package app

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"ally-dev/internal/blackboard"
	openai "github.com/sashabaranov/go-openai"
)

func TestSubagentBlackboardDeliveryCommitsWithSuccessfulTurn(t *testing.T) {
	t.Parallel()
	h := newSubagentReportHarness(t,
		sseChatChunk("partial")+sseChatFinishChunk("length")+sseDone,
		subagentTextReply("accepted"),
		subagentTextReply("next turn"),
	)
	a := newIsolatedTestApp(t)
	cfg := subagentReportBaseConfig(h.server.URL, t.TempDir())
	ctx, closeBoard := newRunBlackboard(context.Background())
	defer closeBoard()
	board := runBlackboard(ctx)
	if _, err := board.Register("sender"); err != nil {
		t.Fatal(err)
	}
	ctx, _, err := startBlackboardDelegate(ctx, "receiver")
	if err != nil {
		t.Fatal(err)
	}
	participant, _ := currentBlackboardParticipant(ctx)
	participant.subID = "receiver-run"
	ctx = context.WithValue(ctx, blackboardParticipantContextKey{}, participant)
	a.subRuns[participant.subID] = &SubagentRun{ID: participant.subID, Name: "receiver", SessionID: "session"}
	publish := func(body string) {
		t.Helper()
		if !board.Publish("sender", blackboard.Message{To: "receiver", Type: "finding", Body: body}) {
			t.Fatal("publication failed")
		}
	}
	publish("first-fact")
	parent := []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleAssistant, ToolCalls: []openai.ToolCall{stagedCall("calculate", `{}`)}},
		{Role: openai.ChatMessageRoleTool, ToolCallID: "calculation", Content: "original result"},
	}
	history := append([]openai.ChatCompletionMessage(nil), parent...)
	resp, err := a.streamSubagentModelResponse(ctx, cfg, cfg.Model, &history, nil, nil)
	if err != nil || modelResponseStopError(cfg, resp) == nil {
		t.Fatalf("expected a provider-rejected turn: response=%+v, error=%v", resp, err)
	}
	if !reflect.DeepEqual(history, parent) {
		t.Fatal("rejected turn changed child history")
	}
	if unread, _ := board.Read("receiver"); len(unread) != 1 {
		t.Fatalf("rejected turn consumed delivery: %+v", unread)
	}
	if run := a.GetSubagents()[0]; run.BlackboardReceived != 0 || len(run.BlackboardEvents) != 0 {
		t.Fatal("rejected turn was displayed as received")
	}
	publish("second-fact")
	if _, err := a.streamSubagentModelResponse(ctx, cfg, cfg.Model, &history, nil, nil); err != nil {
		t.Fatal(err)
	}
	if unread, _ := board.Read("receiver"); len(unread) != 0 {
		t.Fatalf("accepted turn left messages unread: %+v", unread)
	}
	if parent[1].Content != "original result" {
		t.Fatal("delivery mutated another history sharing the input slice")
	}
	history = append(history, openai.ChatCompletionMessage{Role: openai.ChatMessageRoleAssistant, Content: "accepted"})
	if _, err := a.streamSubagentModelResponse(ctx, cfg, cfg.Model, &history, nil, nil); err != nil {
		t.Fatal(err)
	}
	if run := a.GetSubagents()[0]; run.BlackboardReceived != 2 || len(run.BlackboardEvents) != 2 {
		t.Fatalf("received count lost or repeated deliveries: %+v", run)
	}
	sent := h.recorded()
	for i, want := range []string{"first-fact", "first-fact second-fact", "first-fact second-fact"} {
		var text string
		for _, message := range sent[i].Messages {
			text += message.Content
		}
		for _, fact := range strings.Fields(want) {
			if strings.Count(text, fact) != 1 {
				t.Errorf("request %d must carry %q exactly once", i+1, fact)
			}
		}
	}
}

func TestSubagentBlackboardHistorySurvivesToolsAndForcedReport(t *testing.T) {
	redirectAppStateDir(t)
	h := newSubagentReportHarness(t,
		subagentToolCallReply("calculate", `{"expression":"1+1"}`),
		subagentToolCallReply("calculate", `{"expression":"2+2"}`),
		subagentTextReply("report"),
	)
	a := newIsolatedTestApp(t)
	a.stats = nil
	cfg := subagentReportBaseConfig(h.server.URL, t.TempDir())
	ctx, closeBoard := newRunBlackboard(context.Background())
	defer closeBoard()
	board := runBlackboard(ctx)
	if _, err := board.Register("sender"); err != nil {
		t.Fatal(err)
	}
	const fact = "collaboration-fact-83721"
	if !board.Publish("sender", blackboard.Message{To: "receiver", Type: "finding", Body: fact}) {
		t.Fatal("publication failed")
	}
	result, err := a.executeDelegate(ctx, cfg, "audit", AgentDelegateRequest{
		Name: "receiver", Task: "calculate and report", CleanContext: true, MaxSteps: 2,
		tools: []openai.Tool{subagentReportTool("calculate")},
	})
	if err != nil || result.Summary != "report" || result.StopReason != "step_limit" {
		t.Fatalf("result=%+v, error=%v", result, err)
	}
	sent := h.recorded()
	if len(sent) != 3 {
		t.Fatalf("model requests=%d, want 3", len(sent))
	}
	for i, request := range sent {
		var text string
		for _, message := range request.Messages {
			text += message.Content
		}
		if strings.Count(text, fact) != 1 {
			t.Errorf("request %d lost or repeated delivery", i+1)
		}
		if i > 0 && !reflect.DeepEqual(sent[i-1].Messages, request.Messages[:len(sent[i-1].Messages)]) {
			t.Errorf("request %d changed its already-sent prefix", i+1)
		}
	}
}

func TestSubagentBlackboardProtocolOnlyRequiresReport(t *testing.T) {
	for _, forced := range []bool{false, true} {
		name := "normal"
		if forced {
			name = "forced"
		}
		t.Run(name, func(t *testing.T) {
			redirectAppStateDir(t)
			protocol := subagentTextReply(`<msg to="peer" type="finding">finding</msg>`)
			script := []string{protocol, subagentTextReply("actual report")}
			budget := 2
			if forced {
				script = append([]string{subagentToolCallReply("calculate", `{"expression":"1+1"}`)}, script...)
				budget = 1
			}
			h := newSubagentReportHarness(t, script...)
			a := newIsolatedTestApp(t)
			a.stats = nil
			cfg := subagentReportBaseConfig(h.server.URL, t.TempDir())
			ctx, closeBoard := newRunBlackboard(context.Background())
			defer closeBoard()
			if _, err := runBlackboard(ctx).Register("peer"); err != nil {
				t.Fatal(err)
			}
			result, err := a.executeDelegate(ctx, cfg, "audit", AgentDelegateRequest{
				Name: "sender", Task: "work and report", CleanContext: true, MaxSteps: budget,
				tools: []openai.Tool{subagentReportTool("calculate")},
			})
			if err != nil || result.Status != "completed" || result.Summary != "actual report" {
				t.Fatalf("result=%+v, error=%v", result, err)
			}
			if len(h.recorded()) != len(script) {
				t.Fatalf("requests=%d, want %d", len(h.recorded()), len(script))
			}
			if run := a.GetSubagents()[0]; run.BlackboardSent != 1 || run.BlackboardReceived != 0 || len(run.BlackboardEvents) != 1 {
				t.Fatalf("publication display lost its sending/receiving semantics: %+v", run)
			}
			messages, _ := runBlackboard(ctx).Read("peer")
			if len(messages) != 1 || messages[0].Body != "finding" {
				t.Fatalf("report recovery lost or repeated publication: %+v", messages)
			}
		})
	}
}

func TestSubagentBlackboardMissingReportRecoveryIsBounded(t *testing.T) {
	redirectAppStateDir(t)
	protocol := subagentTextReply(`<msg to="peer" type="finding">finding</msg>`)
	script := make([]string, 2+maxForcedReportTurns)
	for i := range script {
		script[i] = protocol
	}
	h := newSubagentReportHarness(t, script...)
	a := newIsolatedTestApp(t)
	a.stats = nil
	ctx, closeBoard := newRunBlackboard(context.Background())
	defer closeBoard()
	cfg := subagentReportBaseConfig(h.server.URL, t.TempDir())
	result, err := a.executeDelegate(ctx, cfg, "audit", AgentDelegateRequest{
		Name: "sender", Task: "work and report", CleanContext: true, MaxSteps: 2,
		tools: []openai.Tool{subagentReportTool("calculate")},
	})
	if err != nil || result.Status == "completed" || result.Error == "" || result.StopReason != "step_limit" {
		t.Fatalf("missing report incorrectly completed: result=%+v, error=%v", result, err)
	}
	if len(h.recorded()) != len(script) {
		t.Fatalf("requests=%d, want bounded %d", len(h.recorded()), len(script))
	}
}

func TestSubagentBlackboardDisplayIsBoundedIsolatedAndDetached(t *testing.T) {
	t.Parallel()
	a := newIsolatedTestApp(t)
	sink := &captureEventSink{}
	a.events = sink
	ctx, closeBoard := newRunBlackboard(context.Background())
	defer closeBoard()
	ctx, _, err := startBlackboardDelegate(ctx, "sender")
	if err != nil {
		t.Fatal(err)
	}
	participant, _ := currentBlackboardParticipant(ctx)
	participant.subID = "sender-run"
	ctx = context.WithValue(ctx, blackboardParticipantContextKey{}, participant)
	a.subRuns[participant.subID] = &SubagentRun{ID: participant.subID, Name: "sender", SessionID: "session"}
	a.subRuns["other-run"] = &SubagentRun{ID: "other-run", Name: "sender", SessionID: "other-session"}
	body := strings.Repeat("中", maxSubagentBlackboardBodyRunes+1)
	for i := 0; i < MaxSubagentBlackboardEvents+3; i++ {
		text := fmt.Sprintf("<msg to='queued' type='finding'>%s</msg>", body)
		if clean := strings.TrimSpace(a.interceptSubagentBlackboard(ctx, text)); clean != "" {
			t.Fatal("valid publication was not intercepted")
		}
	}
	a.interceptSubagentBlackboard(ctx, "<msg to='queued' type='finding' from='spoof'>invalid</msg>")
	var run *SubagentRun
	for _, snapshot := range a.GetSubagents() {
		if snapshot.ID == "sender-run" {
			run = snapshot
		} else if snapshot.BlackboardSent != 0 || len(snapshot.BlackboardEvents) != 0 {
			t.Fatal("display events leaked into another run sharing the same name")
		}
	}
	if run == nil || run.BlackboardSent != MaxSubagentBlackboardEvents+3 || run.BlackboardReceived != 0 || len(run.BlackboardEvents) != MaxSubagentBlackboardEvents {
		t.Fatalf("unbounded or incorrect display counts: %+v", run)
	}
	first := run.BlackboardEvents[0]
	if first.Seq != 4 || !first.Truncated || len([]rune(first.Body)) != maxSubagentBlackboardBodyRunes {
		t.Fatalf("incorrect display window/body limit: %+v", first)
	}
	payload, ok := sink.payload.(map[string]any)
	if !ok || sink.name != "sub:blackboard" || sink.count != MaxSubagentBlackboardEvents+3 || payload["id"] != "sender-run" || payload["sessionId"] != "session" {
		t.Fatalf("incorrect host event routing: name=%s count=%d payload=%+v", sink.name, sink.count, sink.payload)
	}
	if event, ok := payload["blackboardEvent"].(SubagentBlackboardEvent); !ok || event.Direction != "sent" || event.To != "queued" || event.At <= 0 {
		t.Fatalf("host event lost display metadata: %+v", payload)
	}
	if _, err := runBlackboard(ctx).Register("queued"); err != nil {
		t.Fatal(err)
	}
	messages, _ := runBlackboard(ctx).Read("queued")
	if len(messages) != MaxSubagentBlackboardEvents+3 || messages[0].Body != body {
		t.Fatal("display limits truncated actual collaboration data")
	}
	run.BlackboardEvents[0].Body = "mutated snapshot"
	closeBoard()
	for _, snapshot := range a.GetSubagents() {
		if snapshot.ID == "sender-run" && snapshot.BlackboardEvents[0].Body != first.Body {
			t.Fatal("closing the run or mutating a snapshot changed retained display data")
		}
	}
}

func TestChatBlackboardSkippedDelegateCanRetryItsName(t *testing.T) {
	redirectAppStateDir(t)
	first := sseChatToolCallChunk("failed", "calculate", `{"expression":"1+","stage":0}`) +
		sseChatToolCallChunk("skipped", "subagent", `{"name":"worker","role":"tester","task":"inspect","cleanContext":true,"maxSteps":2,"stage":1}`) +
		sseChatFinishChunk("tool_calls") + sseDone
	h := newSubagentReportHarness(t, first,
		subagentToolCallReply("subagent", `{"name":"worker","role":"tester","task":"inspect","cleanContext":true,"maxSteps":2}`),
		subagentTextReply("child report"), subagentTextReply("parent report"),
	)
	a := newIsolatedChatTestApp(t)
	a.stats = nil
	cfg := subagentReportBaseConfig(h.server.URL, a.config.Workspace)
	a.runChat(context.Background(), "audit-run", ChatRequest{SessionID: "audit", Message: "inspect"}, cfg)
	runs := a.GetSubagents()
	if len(runs) != 1 || runs[0].Name != "worker" || runs[0].Status != "completed" || runs[0].Summary != "child report" {
		t.Fatalf("skipped delegate was started or could not retry: %+v", runs)
	}
	requests := h.recorded()
	if len(requests) != 4 {
		t.Fatalf("requests=%d, want 4", len(requests))
	}
	var skipped bool
	for _, message := range requests[1].Messages {
		if message.ToolCallID == "skipped" {
			skipped = containsStageBlocked(message.Content)
		}
	}
	if !skipped || a.sessionRunOutcomes["audit"].Status != "done" {
		t.Fatalf("unexpected skip/run outcome: skipped=%v, outcome=%+v", skipped, a.sessionRunOutcomes["audit"])
	}
}
