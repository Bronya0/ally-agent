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
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	openai "github.com/sashabaranov/go-openai"
)

// subagentReportRequest is the decoded body of one sub-agent model request, kept
// to the two fields the report contract is about: the declared tools (they must
// survive the exhausted budget, or the prompt prefix forks at the first token)
// and the message sequence (append-only, so retries read the cache).
type subagentReportRequest struct {
	Tools    []openai.Tool                  `json:"tools"`
	Messages []openai.ChatCompletionMessage `json:"messages"`
}

// subagentReportHarness replays one scripted SSE reply per request and records
// what was sent. A request past the end of the script answers "unscripted", so an
// unexpected extra turn fails the request-count assertions instead of hanging.
type subagentReportHarness struct {
	server *httptest.Server

	mu     sync.Mutex
	script []string
	sent   []subagentReportRequest
}

func newSubagentReportHarness(t *testing.T, replies ...string) *subagentReportHarness {
	t.Helper()
	h := &subagentReportHarness{script: replies}
	h.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req subagentReportRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		h.mu.Lock()
		index := len(h.sent)
		h.sent = append(h.sent, req)
		h.mu.Unlock()

		reply := subagentTextReply("unscripted reply")
		if index < len(h.script) {
			reply = h.script[index]
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, reply)
	}))
	t.Cleanup(h.server.Close)
	return h
}

func (h *subagentReportHarness) recorded() []subagentReportRequest {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]subagentReportRequest(nil), h.sent...)
}

// subagentToolCallReply 是一个只调用工具、不写正文的响应。
func subagentToolCallReply(name, args string) string {
	return sseChatToolCallChunk("call_"+name, name, args) + sseChatFinishChunk("tool_calls") + sseDone
}

// subagentTextReply 是一个纯文本（无工具调用）的响应。
func subagentTextReply(text string) string {
	return sseChatChunk(text) + sseChatFinishChunk("stop") + sseDone
}

// subagentReportBaseConfig 指向伪服务，工作区由调用方给（以便断言落盘副作用）。
func subagentReportBaseConfig(baseURL, workspace string) ConfigState {
	return ConfigState{
		APIFormat: apiFormatOpenAIChat,
		BaseURL:   baseURL,
		APIKeys:   []string{"test-key"},
		Model:     "test-model",
		Workspace: workspace,
		MaxTokens: 64,
	}
}

func subagentReportTool(name string) openai.Tool {
	return openai.Tool{
		Type:     openai.ToolTypeFunction,
		Function: &openai.FunctionDefinition{Name: name},
	}
}

// TestForceSubagentFinalReportKeepsToolsAndRetriesDiscardedCalls 钉住预算耗尽后
// 收尾报告轮的契约：工具声明留着（丢掉它会让提示词前缀从第一个 token 起分叉，
// 本轮最大的那次请求全价击穿缓存）；工具调用一律不执行，只用一句提示再问；
// 直到模型给出正文，或问满 maxForcedReportTurns 次。
func TestForceSubagentFinalReportKeepsToolsAndRetriesDiscardedCalls(t *testing.T) {
	workspace := t.TempDir()
	harness := newSubagentReportHarness(t,
		subagentToolCallReply("create", `{"path":"marker.txt","content":"x"}`),
		subagentToolCallReply("create", `{"path":"marker.txt","content":"x"}`),
		subagentTextReply("report: nothing was written"),
	)

	app := NewApp()
	app.stats = nil
	cfg := subagentReportBaseConfig(harness.server.URL, workspace)
	tools := []openai.Tool{subagentReportTool("create")}
	run := &SubagentRun{ID: "sub-report"}
	history := []openai.ChatCompletionMessage{{Role: openai.ChatMessageRoleUser, Content: "## Task\nwrite marker.txt"}}

	summary, err := app.forceSubagentFinalReport(context.Background(), cfg, "test-model", history, tools, run)
	if err != nil {
		t.Fatalf("forceSubagentFinalReport() error = %v", err)
	}
	if !strings.Contains(summary, "report: nothing was written") {
		t.Fatalf("summary = %q, want the text answer", summary)
	}

	sent := harness.recorded()
	if len(sent) != maxForcedReportTurns {
		t.Fatalf("model requests = %d, want %d (one per report turn)", len(sent), maxForcedReportTurns)
	}
	for i, req := range sent {
		if len(req.Tools) != 1 || req.Tools[0].Function == nil || req.Tools[0].Function.Name != "create" {
			t.Fatalf("request %d tools = %+v, want the sub-agent tool list kept", i, req.Tools)
		}
	}
	// 追加式重试：每次请求逐字重复上一次并只多一条提示，前缀不变才读得到缓存。
	for i := 1; i < len(sent); i++ {
		previous, current := sent[i-1].Messages, sent[i].Messages
		if len(current) != len(previous)+1 {
			t.Fatalf("request %d messages = %d, want %d (append-only)", i, len(current), len(previous)+1)
		}
		for j := range previous {
			if current[j].Role != previous[j].Role || current[j].Content != previous[j].Content {
				t.Fatalf("request %d message %d changed: %+v → %+v", i, j, previous[j], current[j])
			}
		}
	}
	// 比对提示语构造函数本身，不钉文案：改措辞不该让测试变红。
	if got := sent[0].Messages[len(sent[0].Messages)-1].Content; got != subagentBudgetExhaustedNotice() {
		t.Fatalf("first report turn last message = %q, want the exhaustion notice", got)
	}
	if got := sent[1].Messages[len(sent[1].Messages)-1].Content; got != subagentReportNudge(true) {
		t.Fatalf("second turn last message = %q, want the discarded-call nudge", got)
	}
	if _, statErr := os.Stat(filepath.Join(workspace, "marker.txt")); !os.IsNotExist(statErr) {
		t.Fatalf("marker.txt exists (stat err = %v); a discarded tool call must never run", statErr)
	}
	// 报告轮的用量也要进工作区总量：这几轮以前完全不入账，页脚与看板都少算。
	// 伪服务不带 usage，所以入账走的是输出估算（run 局部总量只认服务商上报值）。
	if booked := app.workspaceTokenUsage[workspaceUsageKey(workspace)]; booked.OutputTokens <= 0 {
		t.Fatalf("workspace token usage = %+v, want every report turn booked", booked)
	}
}

// TestForceSubagentFinalReportRetriesAnEmptyAnswer 钉住「空回答也不算报告」：
// 没有工具调用但一个字都没写，同样再问一次，而不是把空摘要交给父代理。
func TestForceSubagentFinalReportRetriesAnEmptyAnswer(t *testing.T) {
	harness := newSubagentReportHarness(t, subagentTextReply(""), subagentTextReply("report: done"))

	app := NewApp()
	app.stats = nil
	cfg := subagentReportBaseConfig(harness.server.URL, t.TempDir())
	tools := []openai.Tool{subagentReportTool("read")}

	summary, err := app.forceSubagentFinalReport(context.Background(), cfg, "test-model",
		[]openai.ChatCompletionMessage{{Role: openai.ChatMessageRoleUser, Content: "## Task\ndo it"}}, tools, &SubagentRun{ID: "sub-empty"})
	if err != nil {
		t.Fatalf("forceSubagentFinalReport() error = %v", err)
	}
	if !strings.Contains(summary, "report: done") {
		t.Fatalf("summary = %q, want the second attempt's text", summary)
	}
	sent := harness.recorded()
	if len(sent) != 2 {
		t.Fatalf("model requests = %d, want 2 (empty answer then retry)", len(sent))
	}
	if last := sent[1].Messages[len(sent[1].Messages)-1].Content; last != subagentReportNudge(false) {
		t.Fatalf("retry nudge = %q, want the empty-answer wording", last)
	}
}

// TestForceSubagentFinalReportGivesUpAfterBoundedTurns 钉住次数上限：一直只调
// 工具时最多问 maxForcedReportTurns 次，随后以专门错误收场，绝不无限重问。
func TestForceSubagentFinalReportGivesUpAfterBoundedTurns(t *testing.T) {
	harness := newSubagentReportHarness(t,
		subagentToolCallReply("read", `{"files":[{"path":"a.txt"}]}`),
		subagentToolCallReply("read", `{"files":[{"path":"a.txt"}]}`),
		subagentToolCallReply("read", `{"files":[{"path":"a.txt"}]}`),
		subagentToolCallReply("read", `{"files":[{"path":"a.txt"}]}`),
	)

	app := NewApp()
	app.stats = nil
	cfg := subagentReportBaseConfig(harness.server.URL, t.TempDir())
	tools := []openai.Tool{subagentReportTool("read")}

	summary, err := app.forceSubagentFinalReport(context.Background(), cfg, "test-model",
		[]openai.ChatCompletionMessage{{Role: openai.ChatMessageRoleUser, Content: "## Task\ndo it"}}, tools, &SubagentRun{ID: "sub-stuck"})
	if !errors.Is(err, errForcedReportMissing) {
		t.Fatalf("forceSubagentFinalReport() error = %v, want errForcedReportMissing", err)
	}
	if summary != "" {
		t.Fatalf("summary = %q, want empty (no text was ever written)", summary)
	}
	if got := len(harness.recorded()); got != maxForcedReportTurns {
		t.Fatalf("model requests = %d, want exactly %d", got, maxForcedReportTurns)
	}
}

// TestSubagentBudgetExhaustionNeverRunsDiscardedToolCalls 走完整子代理路径：预算
// 只够一步，那一步的工具照常执行；之后报告轮里的工具调用一个都不许落盘，最终
// 结果仍是模型写出的报告正文。
func TestSubagentBudgetExhaustionNeverRunsDiscardedToolCalls(t *testing.T) {
	redirectAppStateDir(t)
	workspace := t.TempDir()
	marker := subagentToolCallReply("create", `{"path":"marker.txt","content":"x"}`)
	harness := newSubagentReportHarness(t,
		subagentToolCallReply("calculate", `{"expression":"1+1"}`),
		marker,
		marker,
		subagentTextReply("report: budget was too small"),
	)

	app := NewApp()
	app.stats = nil
	cfg := subagentReportBaseConfig(harness.server.URL, workspace)
	tools := []openai.Tool{subagentReportTool("calculate"), subagentReportTool("create")}

	result, err := app.executeDelegate(context.Background(), cfg, "session-1", AgentDelegateRequest{
		Task:         "write marker.txt",
		CleanContext: true,
		MaxSteps:     1,
		Model:        "test-model",
		tools:        tools,
	})
	if err != nil {
		t.Fatalf("executeDelegate() error = %v", err)
	}
	if result.Status != "completed" || result.StopReason != "step_limit" || !strings.Contains(result.Summary, "report: budget was too small") {
		t.Fatalf("result = %+v, want a report with the step limit explicitly marked", result)
	}
	if rendered := renderSubagentResultForModel(*result); !strings.Contains(rendered, `stopReason="step_limit"`) {
		t.Fatalf("parent cannot see the stop reason: %s", rendered)
	}
	if _, statErr := os.Stat(filepath.Join(workspace, "marker.txt")); !os.IsNotExist(statErr) {
		t.Fatalf("marker.txt exists (stat err = %v); report-turn tool calls must never run", statErr)
	}
	sent := harness.recorded()
	if len(sent) != 1+maxForcedReportTurns {
		t.Fatalf("model requests = %d, want 1 budgeted step + %d report turns", len(sent), maxForcedReportTurns)
	}
	for i, req := range sent {
		if len(req.Tools) == 0 {
			t.Fatalf("request %d declared no tools; the report turn must keep them for the cache", i)
		}
	}
}

// TestRecordSubagentUsageKeepsTheLatestContext：卡片上的 ↑/↓ 是每步累加值，看不出这个
// 子代理当下占多大上下文。上下文取最近一轮的 prompt tokens（服务商报的那个数），某一轮
// 漏报时保留上一次的值，而不是掉回 0 让徽标消失。
func TestRecordSubagentUsageKeepsTheLatestContext(t *testing.T) {
	app := NewApp()
	app.stats = nil
	workspace := t.TempDir()
	run := &SubagentRun{ID: "sub-ctx"}

	app.recordSubagentUsage(run, "test-model", workspace, &modelStreamResult{
		Usage: &modelUsage{PromptTokens: 1200, CompletionTokens: 30},
	})
	if run.ContextTokens != 1200 || run.InputTokens != 1200 || run.OutputTokens != 30 || run.TotalTokens != 1230 {
		t.Fatalf("first turn = ctx %d, in %d, out %d, total %d; want 1200/1200/30/1230",
			run.ContextTokens, run.InputTokens, run.OutputTokens, run.TotalTokens)
	}

	// 第二轮上下文变大：累计输入继续涨，上下文只反映这一轮。
	app.recordSubagentUsage(run, "test-model", workspace, &modelStreamResult{
		Usage: &modelUsage{PromptTokens: 4000, CompletionTokens: 10},
	})
	if run.ContextTokens != 4000 {
		t.Fatalf("second turn context = %d, want 4000", run.ContextTokens)
	}
	if run.InputTokens != 5200 {
		t.Fatalf("input tokens = %d, want the running total 5200", run.InputTokens)
	}

	// 服务商漏报 prompt tokens 的一轮不清空上下文。
	app.recordSubagentUsage(run, "test-model", workspace, &modelStreamResult{
		Usage: &modelUsage{CompletionTokens: 7},
	})
	if run.ContextTokens != 4000 {
		t.Fatalf("a turn without prompt usage must keep the last context, got %d", run.ContextTokens)
	}
}

// TestRecordSubagentUsageAccumulatesCacheSplit：卡片上的命中率来自服务商上报的缓存
// 拆分，必须整轮累加——只留最后一轮会让命中率只反映最后一步，看着比实际好或差。
func TestRecordSubagentUsageAccumulatesCacheSplit(t *testing.T) {
	app := NewApp()
	app.stats = nil
	workspace := t.TempDir()
	run := &SubagentRun{ID: "sub-cache"}

	app.recordSubagentUsage(run, "test-model", workspace, &modelStreamResult{
		Usage: &modelUsage{PromptTokens: 1000, CompletionTokens: 20, CacheHitTokens: 800, CacheMissTokens: 200},
	})
	app.recordSubagentUsage(run, "test-model", workspace, &modelStreamResult{
		Usage: &modelUsage{PromptTokens: 2000, CompletionTokens: 30, CacheHitTokens: 1800, CacheMissTokens: 200},
	})
	if run.CacheHitTokens != 2600 || run.CacheMissTokens != 400 {
		t.Fatalf("cache split = hit %d miss %d, want the running total 2600/400", run.CacheHitTokens, run.CacheMissTokens)
	}

	// 一轮没报缓存（两个桶都是 0）不能把前两步攒下的数改写成全命中或全未命中。
	app.recordSubagentUsage(run, "test-model", workspace, &modelStreamResult{
		Usage: &modelUsage{PromptTokens: 3000, CompletionTokens: 10},
	})
	if run.CacheHitTokens != 2600 || run.CacheMissTokens != 400 {
		t.Fatalf("a turn without cache telemetry changed the split: hit %d miss %d", run.CacheHitTokens, run.CacheMissTokens)
	}
}
