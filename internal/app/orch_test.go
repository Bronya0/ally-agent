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
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"ally-dev/internal/tools/grep"
	openai "github.com/sashabaranov/go-openai"
)

func TestHTTPRequestRedirectStripsSensitiveHeadersAcrossOrigins(t *testing.T) {
	received := make(chan http.Header, 1)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received <- r.Header.Clone()
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer target.Close()

	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/final", http.StatusFound)
	}))
	defer source.Close()

	app := NewApp()
	_, err := app.httpRequestTool(context.Background(), HTTPRequestToolRequest{
		URL: source.URL + "/redirect",
		Headers: map[string]string{
			"Authorization": "Bearer secret",
			"Cookie":        "sid=secret",
			"X-Api-Key":     "secret-key",
			"X-Test":        "keep-me",
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	headers := <-received
	if got := headers.Get("Authorization"); got != "" {
		t.Fatalf("expected Authorization to be stripped across origins, got %q", got)
	}
	if got := headers.Get("Cookie"); got != "" {
		t.Fatalf("expected Cookie to be stripped across origins, got %q", got)
	}
	if got := headers.Get("X-Api-Key"); got != "" {
		t.Fatalf("expected X-Api-Key to be stripped across origins, got %q", got)
	}
	if got := headers.Get("X-Test"); got != "keep-me" {
		t.Fatalf("expected non-sensitive header to be preserved, got %q", got)
	}
}

func TestHTTPRequestRedirectPreservesSensitiveHeadersOnSameOrigin(t *testing.T) {
	received := make(chan http.Header, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/redirect":
			http.Redirect(w, r, "/final", http.StatusFound)
		case "/final":
			received <- r.Header.Clone()
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ok"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	app := NewApp()
	_, err := app.httpRequestTool(context.Background(), HTTPRequestToolRequest{
		URL: server.URL + "/redirect",
		Headers: map[string]string{
			"Authorization": "Bearer secret",
			"Cookie":        "sid=secret",
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	headers := <-received
	if got := headers.Get("Authorization"); got != "Bearer secret" {
		t.Fatalf("expected Authorization to be preserved on same origin, got %q", got)
	}
	if got := headers.Get("Cookie"); got != "sid=secret" {
		t.Fatalf("expected Cookie to be preserved on same origin, got %q", got)
	}
}

func TestHTTPRequestParsesJSONResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = w.Write([]byte(`{"ok":true,"items":[1,2]}`))
	}))
	defer server.Close()

	app := NewApp()
	got, err := app.httpRequestTool(context.Background(), HTTPRequestToolRequest{
		URL: server.URL,
	})
	if err != nil {
		t.Fatal(err)
	}

	if got.BodyEncoding != "text" {
		t.Fatalf("expected JSON response to use text body encoding, got %q", got.BodyEncoding)
	}
	parsed, ok := got.JSON.(map[string]any)
	if !ok {
		t.Fatalf("expected parsed JSON object, got %#v", got.JSON)
	}
	if parsed["ok"] != true {
		t.Fatalf("expected parsed ok=true, got %#v", parsed["ok"])
	}
	if !strings.Contains(got.JSONPreview, `"ok": true`) {
		t.Fatalf("expected pretty JSON preview to include ok=true, got %q", got.JSONPreview)
	}
}

func TestEditToolSchemaIsBatchChangesOnly(t *testing.T) {
	var editTool *openai.FunctionDefinition
	for _, tool := range chatTools() {
		if tool.Function != nil && tool.Function.Name == "edit" {
			editTool = tool.Function
			break
		}
	}
	if editTool == nil {
		t.Fatal("edit tool schema not found")
	}
	params, ok := editTool.Parameters.(map[string]any)
	if !ok {
		t.Fatalf("unexpected edit parameters type %T", editTool.Parameters)
	}
	properties, ok := params["properties"].(map[string]any)
	if !ok {
		t.Fatalf("edit schema properties missing: %#v", params)
	}
	if len(properties) != 1 || properties["files"] == nil {
		t.Fatalf("edit should expose only files: %#v", properties)
	}
	for _, legacy := range []string{"oldString", "newString", "replaceAll", "edits", "startLine", "endLine", "newText"} {
		if _, exists := properties[legacy]; exists {
			t.Fatalf("edit schema still exposes legacy field %s", legacy)
		}
	}
	required, ok := params["required"].([]string)
	if !ok {
		t.Fatalf("unexpected edit required type %T", params["required"])
	}
	wantRequired := map[string]bool{"files": true}
	if len(required) != len(wantRequired) {
		t.Fatalf("unexpected required fields: %#v", required)
	}
	for _, name := range required {
		if !wantRequired[name] {
			t.Fatalf("unexpected required edit field %s", name)
		}
	}

	var remoteEditTool *openai.FunctionDefinition
	for _, tool := range chatTools() {
		if tool.Function != nil && tool.Function.Name == "remote_edit" {
			remoteEditTool = tool.Function
			break
		}
	}
	if remoteEditTool == nil {
		t.Fatal("remote_edit tool schema not found")
	}
	remoteParams, ok := remoteEditTool.Parameters.(map[string]any)
	if !ok {
		t.Fatalf("unexpected remote_edit parameters type %T", remoteEditTool.Parameters)
	}
	remoteProperties, ok := remoteParams["properties"].(map[string]any)
	if !ok {
		t.Fatalf("remote_edit schema properties missing: %#v", remoteParams)
	}
	for _, name := range []string{"target", "files"} {
		if _, exists := remoteProperties[name]; !exists {
			t.Fatalf("remote_edit schema missing %s", name)
		}
	}
	for _, legacy := range []string{"oldString", "newString", "replaceAll", "edits", "startLine", "endLine", "newText"} {
		if _, exists := remoteProperties[legacy]; exists {
			t.Fatalf("remote_edit schema still exposes legacy field %s", legacy)
		}
	}
	remoteRequired, ok := remoteParams["required"].([]string)
	if !ok {
		t.Fatalf("unexpected remote_edit required type %T", remoteParams["required"])
	}
	wantRemoteRequired := map[string]bool{"target": true, "files": true}
	if len(remoteRequired) != len(wantRemoteRequired) {
		t.Fatalf("unexpected remote_edit required fields: %#v", remoteRequired)
	}
	for _, name := range remoteRequired {
		if !wantRemoteRequired[name] {
			t.Fatalf("unexpected required remote_edit field %s", name)
		}
	}

	for _, tool := range chatTools() {
		if tool.Function != nil && tool.Function.Name == "replace_text" {
			t.Fatal("replace_text should not be exposed; edit is the only local edit tool")
		}
	}
	filesSchema := properties["files"].(map[string]any)
	fileItems := filesSchema["items"].(map[string]any)
	fileProperties := fileItems["properties"].(map[string]any)
	if fileProperties["version"] == nil || fileProperties["expectedMd5"] != nil {
		t.Fatalf("edit file schema must expose version and reject expectedMd5: %#v", fileProperties)
	}
	fileRequired := fileItems["required"].([]string)
	if !containsString(fileRequired, "version") {
		t.Fatalf("edit file schema must require version: %#v", fileRequired)
	}
	changesSchema, ok := fileProperties["changes"].(map[string]any)
	if !ok {
		t.Fatalf("edit changes schema missing: %#v", properties["changes"])
	}
	items, ok := changesSchema["items"].(map[string]any)
	if !ok {
		t.Fatalf("edit changes item schema missing: %#v", changesSchema)
	}
	changeProperties, ok := items["properties"].(map[string]any)
	if !ok {
		t.Fatalf("edit change properties missing: %#v", items)
	}
	for _, field := range []string{"oldText", "lineRange", "replace_all", "newText"} {
		if _, exists := changeProperties[field]; !exists {
			t.Fatalf("edit change schema missing %s: %#v", field, items)
		}
	}
	changeRequired, ok := items["required"].([]string)
	if !ok || len(changeRequired) != 1 || changeRequired[0] != "newText" {
		t.Fatalf("edit change must require only newText: %#v", items["required"])
	}
	if variants, ok := items["oneOf"].([]any); !ok || len(variants) != 2 {
		t.Fatalf("edit change must require exactly one source form: %#v", items)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestDetectWriteBatchConflictsAllowsRepeatedPathInsideOneLocalEdit(t *testing.T) {
	cfg := ConfigState{Workspace: t.TempDir()}
	calls := []openai.ToolCall{{Function: openai.FunctionCall{
		Name:      "edit",
		Arguments: `{"files":[{"path":"sample.txt"},{"path":"./sample.txt"}]}`,
	}}}
	if conflicts := detectWriteBatchConflicts(cfg, calls); len(conflicts) != 0 {
		t.Fatalf("one local edit call should merge repeated paths, got %#v", conflicts)
	}

	calls = append(calls, openai.ToolCall{Function: openai.FunctionCall{
		Name:      "delete",
		Arguments: `{"path":"sample.txt"}`,
	}})
	conflicts := detectWriteBatchConflicts(cfg, calls)
	if len(conflicts) != 2 {
		t.Fatalf("a separate mutation call must still conflict with the merged edit target, got %#v", conflicts)
	}
	for _, index := range []int{0, 1} {
		if toolErrorCode(conflicts[index]) != "E_WRITE_BATCH_CONFLICT" {
			t.Fatalf("expected E_WRITE_BATCH_CONFLICT for call %d, got %v", index, conflicts[index])
		}
	}
}

func TestDetectWriteBatchConflictsNormalizesSamePath(t *testing.T) {
	cfg := ConfigState{Workspace: t.TempDir()}
	calls := []openai.ToolCall{
		{Function: openai.FunctionCall{Name: "edit", Arguments: `{"files":[{"path":"sample.txt"}]}`}},
		{Function: openai.FunctionCall{Name: "delete", Arguments: `{"path":"./sample.txt"}`}},
		{Function: openai.FunctionCall{Name: "create", Arguments: `{"path":"other.txt"}`}},
	}
	conflicts := detectWriteBatchConflicts(cfg, calls)
	if len(conflicts) != 2 {
		t.Fatalf("expected two conflicting calls, got %#v", conflicts)
	}
	for _, index := range []int{0, 1} {
		if toolErrorCode(conflicts[index]) != "E_WRITE_BATCH_CONFLICT" {
			t.Fatalf("expected E_WRITE_BATCH_CONFLICT for call %d, got %v", index, conflicts[index])
		}
	}
	if _, exists := conflicts[2]; exists {
		t.Fatalf("different path should not conflict: %#v", conflicts)
	}
}

func TestDetectToolBatchConflictsRequiresWaitToRunAlone(t *testing.T) {
	calls := []openai.ToolCall{
		{Function: openai.FunctionCall{Name: "wait", Arguments: `{"seconds":1,"reason":"service restart"}`}},
		{Function: openai.FunctionCall{Name: "http_request", Arguments: `{"url":"http://localhost:8080/health"}`}},
	}
	conflicts := detectToolBatchConflicts(ConfigState{}, calls)
	if len(conflicts) != len(calls) {
		t.Fatalf("expected every call to be rejected, got %#v", conflicts)
	}
	for i := range calls {
		if toolErrorCode(conflicts[i]) != "E_WAIT_BATCH_CONFLICT" {
			t.Fatalf("expected E_WAIT_BATCH_CONFLICT for call %d, got %v", i, conflicts[i])
		}
	}
	if conflicts := detectToolBatchConflicts(ConfigState{}, calls[:1]); len(conflicts) != 0 {
		t.Fatalf("single wait call should be allowed, got %#v", conflicts)
	}
}

func TestDetectToolBatchConflictsRequiresAskToRunAlone(t *testing.T) {
	calls := []openai.ToolCall{
		{Function: openai.FunctionCall{Name: "ask", Arguments: `{"questions":[]}`}},
		{Function: openai.FunctionCall{Name: "list_files", Arguments: `{}`}},
	}
	conflicts := detectToolBatchConflicts(ConfigState{}, calls)
	for i := range calls {
		if toolErrorCode(conflicts[i]) != "E_ASK_BATCH_CONFLICT" {
			t.Fatalf("expected E_ASK_BATCH_CONFLICT for call %d, got %v", i, conflicts[i])
		}
	}
}

func TestChatToolsExposeBackgroundProcessWithoutPollingTools(t *testing.T) {
	blocked := map[string]bool{"start_service": true, "stop_service": true, "list_services": true}
	foundBackgroundProcess := false
	for _, tool := range chatTools() {
		if tool.Function == nil {
			continue
		}
		if blocked[tool.Function.Name] {
			t.Fatalf("managed service tool %s must not be exposed to the model", tool.Function.Name)
		}
		if tool.Function.Name == "service" {
			foundBackgroundProcess = true
		}
	}
	if !foundBackgroundProcess {
		t.Fatal("service must be exposed for non-blocking frontend/backend startup")
	}
}

func TestBackgroundProcessRejectsUnknownAction(t *testing.T) {
	app := NewApp()
	result := app.executeTool(context.Background(), ConfigState{Workspace: t.TempDir()}, "session-1", "service", []byte(`{"action":"status"}`))
	if result.OK || result.ErrorCode != "E_BAD_BACKGROUND_ACTION" {
		t.Fatalf("expected unknown action to be rejected, got %#v", result)
	}
}

func TestBackgroundProcessListReturnsMetadataOnly(t *testing.T) {
	app := NewApp()
	app.servicesMu.Lock()
	app.services["svc_list_1"] = &managedService{
		info:   ServiceInfo{ID: "svc_list_1", Command: "npm run dev", Status: "running", PID: 4321, StartedAt: 1700000000},
		output: newRollingBuffer(serviceOutputLimit),
	}
	app.servicesMu.Unlock()

	result := app.executeTool(context.Background(), ConfigState{Workspace: t.TempDir()}, "session-1", "service", []byte(`{"action":"list"}`))
	if !result.OK {
		t.Fatalf("expected list to succeed, got %#v", result)
	}
	var listed ServiceListToolResult
	if !decodeToolData(result.Data, &listed) {
		t.Fatalf("expected ServiceListToolResult, got %#v", result.Data)
	}
	if listed.ActiveCount != 1 || listed.MaxActive != maxActiveServices || len(listed.Services) != 1 {
		t.Fatalf("unexpected list payload: %#v", listed)
	}
	if listed.Services[0].ID != "svc_list_1" {
		t.Fatalf("unexpected service id: %#v", listed.Services[0])
	}
	// list must not include any output content
	raw, _ := json.Marshal(listed)
	if strings.Contains(string(raw), "outputTail") {
		t.Fatalf("list result must not include outputTail: %s", string(raw))
	}
}

func TestBackgroundProcessReadReturnsBoundedOutput(t *testing.T) {
	app := NewApp()
	buf := newRollingBuffer(serviceOutputLimit)
	_, _ = buf.Write([]byte("ready line\n"))
	app.servicesMu.Lock()
	app.services["svc_read_1"] = &managedService{
		info:   ServiceInfo{ID: "svc_read_1", Command: "vite", Status: "running"},
		output: buf,
	}
	app.servicesMu.Unlock()

	args := []byte(`{"action":"read","id":"svc_read_1","tailBytes":4}`)
	result := app.executeTool(context.Background(), ConfigState{Workspace: t.TempDir()}, "session-1", "service", args)
	if !result.OK {
		t.Fatalf("expected read to succeed, got %#v", result)
	}
	var read ServiceReadResult
	if !decodeToolData(result.Data, &read) {
		t.Fatalf("expected ServiceReadResult, got %#v", result.Data)
	}
	if read.ID != "svc_read_1" || read.ReturnedBytes != 4 || read.Status != "running" {
		t.Fatalf("unexpected read payload: %#v", read)
	}
	if !strings.HasSuffix(read.Output, "ine\n") {
		t.Fatalf("expected 4-byte tail of 'ready line\\n', got %q", read.Output)
	}

	// read on unknown id must return E_SERVICE_NOT_FOUND
	bad := app.executeTool(context.Background(), ConfigState{Workspace: t.TempDir()}, "session-1", "service", []byte(`{"action":"read","id":"svc_missing"}`))
	if bad.OK || bad.ErrorCode != "E_SERVICE_NOT_FOUND" {
		t.Fatalf("expected E_SERVICE_NOT_FOUND, got %#v", bad)
	}
}

func TestWaitToolSchemaAndCancellation(t *testing.T) {
	var waitTool *openai.FunctionDefinition
	for _, tool := range chatTools() {
		if tool.Function != nil && tool.Function.Name == "wait" {
			waitTool = tool.Function
			break
		}
	}
	if waitTool == nil {
		t.Fatal("wait tool schema not found")
	}
	params, ok := waitTool.Parameters.(map[string]any)
	if !ok {
		t.Fatalf("unexpected wait parameters type %T", waitTool.Parameters)
	}
	properties, ok := params["properties"].(map[string]any)
	if !ok || properties["seconds"] == nil || properties["reason"] == nil {
		t.Fatalf("wait schema must expose seconds and reason: %#v", params)
	}

	bad := NewApp().executeTool(context.Background(), ConfigState{}, "session-1", "wait", []byte(`{"seconds":0,"reason":"invalid"}`))
	if bad.OK || bad.ErrorCode != "E_BAD_WAIT" {
		t.Fatalf("expected invalid wait duration to fail, got %#v", bad)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result := NewApp().executeTool(ctx, ConfigState{}, "session-1", "wait", []byte(`{"seconds":1,"reason":"service restart"}`))
	if result.OK || result.ErrorCode != "E_WAIT_CANCELLED" {
		t.Fatalf("expected cancelled wait, got %#v", result)
	}
}

func TestAskToolWaitsForValidatedSubmission(t *testing.T) {
	app := NewApp()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	args := []byte(`{"questions":[{"id":"editor","question":"Choose editors","options":[{"id":"vscode","label":"VS Code","description":"Use the existing editor integration.","recommended":true},{"id":"zed","label":"Zed","description":"Use a lightweight alternative.","recommended":false}]}]}`)
	done := make(chan toolResult, 1)
	go func() {
		done <- app.executeTool(ctx, ConfigState{}, "session-1", "ask", args)
	}()

	var askID string
	deadline := time.Now().Add(time.Second)
	for askID == "" && time.Now().Before(deadline) {
		app.askMu.Lock()
		for id := range app.pendingAsks {
			askID = id
			break
		}
		app.askMu.Unlock()
		if askID == "" {
			time.Sleep(5 * time.Millisecond)
		}
	}
	if askID == "" {
		t.Fatal("ask request was not registered")
	}
	if err := app.SubmitAskResponse(AskSubmitRequest{
		AskID: askID, SessionID: "session-1",
		Answers: []AskSubmittedAnswer{{QuestionID: "editor", SelectedOptionIDs: []string{"vscode"}, CustomText: "Keep Vim keybindings"}},
	}); err != nil {
		t.Fatal(err)
	}
	result := <-done
	if !result.OK {
		t.Fatalf("expected ask to complete, got %#v", result)
	}
	var resolved AskResult
	if !decodeToolData(result.Data, &resolved) || len(resolved.Answers) != 1 || len(resolved.Answers[0].Selections) != 2 {
		t.Fatalf("unexpected resolved ask result: %#v", result.Data)
	}
}

func TestAskRejectsMissingRecommendation(t *testing.T) {
	result := NewApp().executeTool(context.Background(), ConfigState{}, "session-1", "ask", []byte(`{"questions":[{"id":"q1","question":"Choose","options":[{"id":"a","label":"A","description":"First","recommended":false},{"id":"b","label":"B","description":"Second","recommended":false}]}]}`))
	if result.OK || result.ErrorCode != "E_BAD_ASK" {
		t.Fatalf("expected invalid recommendation count to fail, got %#v", result)
	}
}

func TestCompactBackgroundProcessResultForModelReducesOutput(t *testing.T) {
	fullOutput := strings.Repeat("startup log line\n", 600)
	result := toolResult{OK: true, Data: ServiceInfo{
		ID:         "svc_1",
		Command:    "npm run dev",
		PID:        1234,
		Status:     "running",
		OutputTail: fullOutput,
	}}
	compact := compactToolResultForModel("service", result, "fallback")
	if len(compact) >= len(fullOutput) {
		t.Fatalf("expected background process output to be reduced: compact=%d full=%d", len(compact), len(fullOutput))
	}
	if !strings.Contains(compact, `"outputReduced":true`) || !strings.Contains(compact, `"id":"svc_1"`) {
		t.Fatalf("expected compact process metadata, got %s", compact)
	}
}

func TestContextBreakdownIncludesToolSchemas(t *testing.T) {
	app := NewApp()
	app.initialized = true
	app.config = ConfigState{}
	normal := app.getContextBreakdown("session-1")
	if normal.ToolSchemas <= 0 {
		t.Fatalf("expected tool schema tokens to be counted, got %#v", normal)
	}

}

func TestComputeLiveBreakdownSetsTotal(t *testing.T) {
	bd := computeLiveBreakdown([]openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleUser, Content: "hello"},
		{Role: openai.ChatMessageRoleAssistant, Content: "hi"},
	})

	if bd.Total <= 0 {
		t.Fatalf("expected live breakdown total to be set, got %#v", bd)
	}
}

func TestLiveBreakdownAccumulatorMatchesFullRecalculation(t *testing.T) {
	messages := []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleUser, Content: "hello"},
		{Role: openai.ChatMessageRoleAssistant, Content: "hi"},
	}
	acc := newLiveBreakdownAccumulator(messages)
	messages = append(messages,
		openai.ChatCompletionMessage{Role: openai.ChatMessageRoleAssistant, ToolCalls: []openai.ToolCall{{Function: openai.FunctionCall{Name: "read", Arguments: `{"files":[{"path":"a.go"}]}`}}}},
		openai.ChatCompletionMessage{Role: openai.ChatMessageRoleTool, Content: `{"ok":true}`},
	)
	got := acc.update(messages)
	want := computeLiveBreakdown(messages)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("incremental breakdown = %#v, full breakdown = %#v", got, want)
	}

	rebuilt := []openai.ChatCompletionMessage{{Role: openai.ChatMessageRoleUser, Content: "rebuilt"}}
	acc.reset(rebuilt)
	got = acc.update(rebuilt)
	want = computeLiveBreakdown(rebuilt)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("reset breakdown = %#v, full breakdown = %#v", got, want)
	}
}

func TestGrepFilesReturnsPathErrors(t *testing.T) {
	app := NewApp()
	_, err := app.grepFilesWithConfig(context.Background(), ConfigState{Workspace: t.TempDir()}, GrepRequest{
		Pattern: "needle",
		Path:    "missing",
	})
	if err == nil {
		t.Fatal("expected missing search path to return an error")
	}
	if code := toolErrorCode(err); code != "E_GREP_PATH" {
		t.Fatalf("expected E_GREP_PATH, got %q (%v)", code, err)
	}
}

func TestGrepFilesReturnsInvalidRegexErrors(t *testing.T) {
	requireRipgrep(t)
	root := t.TempDir()
	writeToolTestFile(t, root, "sample.txt", "needle\n")

	app := NewApp()
	_, err := app.grepFilesWithConfig(context.Background(), ConfigState{Workspace: root}, GrepRequest{
		Pattern: "[",
	})
	if err == nil || !strings.Contains(err.Error(), "regex") {
		t.Fatalf("expected ripgrep regex error, got %v", err)
	}
	if code := toolErrorCode(err); code != "E_GREP_REGEX" {
		t.Fatalf("expected E_GREP_REGEX, got %q (%v)", code, err)
	}
}

func TestGrepFilesReturnsInvalidGlobErrors(t *testing.T) {
	requireRipgrep(t)
	root := t.TempDir()
	writeToolTestFile(t, root, "sample.txt", "needle\n")

	app := NewApp()
	_, err := app.grepFilesWithConfig(context.Background(), ConfigState{Workspace: root}, GrepRequest{
		Pattern: "needle",
		Glob:    "[",
	})
	if err == nil || !strings.Contains(err.Error(), "glob") {
		t.Fatalf("expected ripgrep glob error, got %v", err)
	}
	if code := toolErrorCode(err); code != "E_GREP_GLOB" {
		t.Fatalf("expected E_GREP_GLOB, got %q (%v)", code, err)
	}
}

func TestGrepFilesSearchesHiddenDirectoryWhenItIsTheRoot(t *testing.T) {
	requireRipgrep(t)
	root := t.TempDir()
	writeToolTestFile(t, root, ".github/workflows/ci.yml", "needle\n")

	app := NewApp()
	got, err := app.grepFilesWithConfig(context.Background(), ConfigState{Workspace: root}, GrepRequest{
		Pattern: "needle",
		Path:    ".github",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.MatchedLines != 1 || got.Files != 1 {
		t.Fatalf("expected one match in hidden search root, got %#v", got)
	}
	if got.FileHits[0].Path != ".github/workflows/ci.yml" {
		t.Fatalf("unexpected match path %q", got.FileHits[0].Path)
	}
}

func TestGrepFilesKeepsExactCountsWhenSamplesAreTruncatedByFileLimit(t *testing.T) {
	requireRipgrep(t)
	root := t.TempDir()
	writeToolTestFile(t, root, "a.txt", "needle one\nneedle two\n")
	writeToolTestFile(t, root, "b.txt", "needle three\n")

	app := NewApp()
	got, err := app.grepFilesWithConfig(context.Background(), ConfigState{Workspace: root}, GrepRequest{
		Pattern:  "needle",
		MaxFiles: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Files != 2 || got.MatchedLines != 3 || got.Hits != 3 || !got.StatsExact {
		t.Fatalf("expected exact counts despite sample truncation, got %#v", got)
	}
	// Without --sort the sampled file is whichever rg reaches first, so the
	// contract is "samples are limited to one file", not a specific path.
	if !got.SamplesTruncated || !got.Truncated || len(got.FileHits) != 1 {
		t.Fatalf("expected single-file samples and truncation, got %#v", got)
	}
	hits := got.FileHits[0]
	if hits.Path != "a.txt" && hits.Path != "b.txt" {
		t.Fatalf("unexpected sample file %q", hits.Path)
	}
	if len(hits.Matches) == 0 || len(hits.Matches) > 2 {
		t.Fatalf("expected at least one sample match from the single sampled file, got %#v", hits)
	}
	for _, m := range hits.Matches {
		if !strings.HasPrefix(m.Content, "needle ") {
			t.Fatalf("unexpected sample match content %q", m.Content)
		}
	}
}

func TestGrepFilesReportsOccurrenceCount(t *testing.T) {
	requireRipgrep(t)
	root := t.TempDir()
	writeToolTestFile(t, root, "a.txt", "ally ally\nfinally\n")
	writeToolTestFile(t, root, "b.txt", "ally\n")

	app := NewApp()
	got, err := app.grepFilesWithConfig(context.Background(), ConfigState{Workspace: root}, GrepRequest{
		Pattern: "ally",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.MatchedLines != 3 {
		t.Fatalf("expected three matching lines, got %#v", got)
	}
	if got.Hits != 4 {
		t.Fatalf("expected four occurrences, got %#v", got)
	}
	if !got.StatsExact {
		t.Fatalf("expected exact grep stats, got %#v", got)
	}
}

func TestGrepFilesKeepsExactCountsWhenSamplesAreTruncatedByMatchLimit(t *testing.T) {
	requireRipgrep(t)
	root := t.TempDir()
	writeToolTestFile(t, root, "a.txt", "needle\nneedle\nneedle\nneedle\nneedle\n")

	app := NewApp()
	got, err := app.grepFilesWithConfig(context.Background(), ConfigState{Workspace: root}, GrepRequest{
		Pattern:    "needle",
		MaxMatches: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.MatchedLines != 5 || got.Hits != 5 || got.Files != 1 || !got.StatsExact {
		t.Fatalf("expected exact counts despite match sample truncation, got %#v", got)
	}
	if !got.SamplesTruncated || !got.Truncated || len(got.FileHits) != 1 || len(got.FileHits[0].Matches) != 3 {
		t.Fatalf("expected three sample matches and truncation, got %#v", got)
	}
}

func TestGrepFilesCanIncludeIgnoredFiles(t *testing.T) {
	requireRipgrep(t)
	root := t.TempDir()
	writeToolTestFile(t, root, ".ignore", "ignored.txt\n")
	writeToolTestFile(t, root, "kept.txt", "needle\n")
	writeToolTestFile(t, root, "ignored.txt", "needle\n")

	app := NewApp()
	defaultResult, err := app.grepFilesWithConfig(context.Background(), ConfigState{Workspace: root}, GrepRequest{
		Pattern: "needle",
	})
	if err != nil {
		t.Fatal(err)
	}
	if defaultResult.MatchedLines != 1 {
		t.Fatalf("expected ignored file to be skipped by default, got %#v", defaultResult)
	}

	includeIgnored, err := app.grepFilesWithConfig(context.Background(), ConfigState{Workspace: root}, GrepRequest{
		Pattern:        "needle",
		IncludeIgnored: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if includeIgnored.MatchedLines != 2 {
		t.Fatalf("expected ignored file to be included, got %#v", includeIgnored)
	}
}

func TestGrepFilesAlwaysExcludesHeavyDirectories(t *testing.T) {
	requireRipgrep(t)
	root := t.TempDir()
	writeToolTestFile(t, root, "src/main.go", "ally\n")
	writeToolTestFile(t, root, "frontend/dist/app.js", "ally\n")
	writeToolTestFile(t, root, "node_modules/pkg/index.js", "ally\n")

	app := NewApp()
	got, err := app.grepFilesWithConfig(context.Background(), ConfigState{Workspace: root}, GrepRequest{
		Pattern:        "ally",
		IncludeIgnored: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.MatchedLines != 1 || got.Hits != 1 || got.Files != 1 {
		t.Fatalf("expected only source match outside heavy dirs, got %#v", got)
	}
	if got.FileHits[0].Path != "src/main.go" {
		t.Fatalf("unexpected match path %q", got.FileHits[0].Path)
	}
	if len(got.Skipped) == 0 {
		t.Fatalf("workspace-wide grep must report its skip policy, got %#v", got)
	}
}

func TestGrepFilesExplicitPathSearchesExcludedDirectories(t *testing.T) {
	requireRipgrep(t)
	root := t.TempDir()
	writeToolTestFile(t, root, "vendor/pkg/source.go", "needle\n")

	app := NewApp()
	got, err := app.grepFilesWithConfig(context.Background(), ConfigState{Workspace: root}, GrepRequest{
		Pattern: "needle",
		Path:    "vendor",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.MatchedLines != 1 || got.Hits != 1 || got.Files != 1 {
		t.Fatalf("explicit path must override broad-search exclusions, got %#v", got)
	}
	if len(got.FileHits) != 1 || got.FileHits[0].Path != "vendor/pkg/source.go" {
		t.Fatalf("unexpected explicit-path result %#v", got.FileHits)
	}
	if len(got.Skipped) != 0 {
		t.Fatalf("explicit path search must not report broad skip policies, got %#v", got.Skipped)
	}
}

func TestGrepFilesExplicitPathSearchesLargeFiles(t *testing.T) {
	requireRipgrep(t)
	root := t.TempDir()
	large := strings.Repeat("x", 11*1024*1024) + "\nneedle\n"
	writeToolTestFile(t, root, "large.txt", large)

	app := NewApp()
	workspaceSearch, err := app.grepFilesWithConfig(context.Background(), ConfigState{Workspace: root}, GrepRequest{Pattern: "needle"})
	if err != nil {
		t.Fatal(err)
	}
	if workspaceSearch.MatchedLines != 0 {
		t.Fatalf("workspace-wide search should keep the large-file guard, got %#v", workspaceSearch)
	}

	explicitSearch, err := app.grepFilesWithConfig(context.Background(), ConfigState{Workspace: root}, GrepRequest{
		Pattern: "needle",
		Path:    "large.txt",
	})
	if err != nil {
		t.Fatal(err)
	}
	if explicitSearch.MatchedLines != 1 || explicitSearch.Hits != 1 || explicitSearch.Files != 1 {
		t.Fatalf("explicit path must search large files, got %#v", explicitSearch)
	}
}

func requireRipgrep(t *testing.T) {
	t.Helper()
	if _, err := grep.Find(); err != nil {
		t.Skip("ripgrep is not installed")
	}
}

func TestCompactToolResultForModelCompactsGrepMatches(t *testing.T) {
	total := maxModelGrepMatches + 5
	group := GrepFileMatch{Path: "a.txt", Matches: make([]GrepMatch, total), MatchCount: total + 10}
	for i := range group.Matches {
		group.Matches[i] = GrepMatch{LineNum: i + 1, Content: "needle"}
	}
	result := toolResult{OK: true, Data: GrepResult{
		FileHits:         []GrepFileMatch{group},
		FileCounts:       []GrepFileCount{{Path: "a.txt", Count: total + 10}},
		MatchedLines:     total,
		Hits:             total,
		Files:            1,
		Truncated:        true,
		SamplesTruncated: true,
		StatsExact:       true,
		NextOffset:       total,
	}}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}

	got := compactToolResultForModel("grep", result, string(raw))

	var decoded struct {
		OK   bool `json:"ok"`
		Data struct {
			FileHits           []GrepFileMatch `json:"fileHits"`
			FileCounts         []GrepFileCount `json:"fileCounts"`
			MatchedLines       int             `json:"matchedLines"`
			Hits               int             `json:"hits"`
			Files              int             `json:"files"`
			SamplesTruncated   bool            `json:"samplesTruncated"`
			NextOffset         int             `json:"nextOffset"`
			StatsExact         bool            `json:"statsExact"`
			MatchesReduced     bool            `json:"matchesReduced"`
			OriginalMatchCount int             `json:"originalMatchCount"`
			MatchesOmitted     int             `json:"matchesOmitted"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(got), &decoded); err != nil {
		t.Fatal(err)
	}
	if !decoded.OK {
		t.Fatalf("expected ok compacted result, got %s", got)
	}
	// statsExact is always true today; the model view must omit it instead of
	// repeating an always-true field.
	if strings.Contains(got, "statsExact") {
		t.Fatalf("model view must omit always-true statsExact: %s", got)
	}
	totalModel := 0
	for _, fh := range decoded.Data.FileHits {
		totalModel += len(fh.Matches)
	}
	if totalModel != maxModelGrepMatches {
		t.Fatalf("expected %d model matches, got %d", maxModelGrepMatches, totalModel)
	}
	if decoded.Data.MatchedLines != total || decoded.Data.Hits != total || decoded.Data.Files != 1 {
		t.Fatalf("expected grep stats to be preserved, got %#v", decoded.Data)
	}
	// statsExact is always true and intentionally omitted from the model view
	// (asserted above via strings.Contains); the decoded zero value is fine.
	if !decoded.Data.SamplesTruncated {
		t.Fatalf("expected samplesTruncated marker to be preserved, got %#v", decoded.Data)
	}
	if !decoded.Data.MatchesReduced || decoded.Data.OriginalMatchCount != total || decoded.Data.MatchesOmitted != 5 {
		t.Fatalf("expected reduction metadata, got %#v", decoded.Data)
	}
	// matchCount, fileCounts and nextOffset must survive compaction so the
	// model can rank hotspots and page through the rest.
	if len(decoded.Data.FileHits) != 1 || decoded.Data.FileHits[0].MatchCount != total+10 {
		t.Fatalf("expected exact matchCount to survive compaction, got %#v", decoded.Data.FileHits)
	}
	if len(decoded.Data.FileCounts) != 1 || decoded.Data.FileCounts[0].Path != "a.txt" || decoded.Data.FileCounts[0].Count != total+10 {
		t.Fatalf("expected fileCounts to survive compaction, got %#v", decoded.Data.FileCounts)
	}
	if decoded.Data.NextOffset != total {
		t.Fatalf("expected nextOffset to survive compaction, got %d", decoded.Data.NextOffset)
	}
}

func TestCompactToolResultForModelPreservesRealMatchesWithLargeContext(t *testing.T) {
	matches := make([]GrepMatch, 0, 503)
	for i := 0; i < 500; i++ {
		matches = append(matches, GrepMatch{LineNum: i + 1, Content: "context", Context: true})
	}
	for i := 0; i < 3; i++ {
		matches = append(matches, GrepMatch{LineNum: 501 + i, Content: "needle"})
	}
	result := toolResult{OK: true, Data: GrepResult{
		FileHits:     []GrepFileMatch{{Path: "a.txt", Matches: matches, MatchCount: 3}},
		FileCounts:   []GrepFileCount{{Path: "a.txt", Count: 3}},
		MatchedLines: 3,
		Hits:         3,
		Files:        1,
		StatsExact:   true,
	}}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}

	compact := compactToolResultForModel("grep", result, string(raw))
	var decoded struct {
		Data struct {
			FileHits       []GrepFileMatch `json:"fileHits"`
			MatchesOmitted int             `json:"matchesOmitted"`
			ContextOmitted int             `json:"contextOmitted"`
			MatchesReduced bool            `json:"matchesReduced"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(compact), &decoded); err != nil {
		t.Fatal(err)
	}
	actualMatches, contextLines := 0, 0
	for _, match := range decoded.Data.FileHits[0].Matches {
		if match.Context {
			contextLines++
		} else {
			actualMatches++
		}
	}
	if actualMatches != 3 {
		t.Fatalf("model compaction must preserve all real matches, got %d from %#v", actualMatches, decoded.Data.FileHits)
	}
	if contextLines > maxModelGrepContextLines || decoded.Data.MatchesOmitted != 0 || decoded.Data.ContextOmitted != 100 || !decoded.Data.MatchesReduced {
		t.Fatalf("expected bounded context reduction without dropping matches, got context=%d data=%#v", contextLines, decoded.Data)
	}
}

func TestGrepFilesReportsFileCountsAndOffsetEndToEnd(t *testing.T) {
	requireRipgrep(t)
	root := t.TempDir()
	var b strings.Builder
	for i := 0; i < 12; i++ {
		fmt.Fprintf(&b, "line %d needle\n", i)
	}
	writeToolTestFile(t, root, "hot.txt", b.String())
	writeToolTestFile(t, root, "cold.txt", "needle\n")

	app := NewApp()
	got, err := app.grepFilesWithConfig(context.Background(), ConfigState{Workspace: root}, GrepRequest{
		Pattern:    "needle",
		MaxMatches: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.MatchedLines != 13 || got.Hits != 13 || got.Files != 2 {
		t.Fatalf("expected exact stats, got %#v", got)
	}
	// Hotspot list: hot.txt(12) first, cold.txt(1) second.
	if len(got.FileCounts) != 2 || got.FileCounts[0].Path != "hot.txt" || got.FileCounts[0].Count != 12 || got.FileCounts[1].Path != "cold.txt" || got.FileCounts[1].Count != 1 {
		t.Fatalf("expected descending fileCounts, got %#v", got.FileCounts)
	}
	if !got.Truncated || got.NextOffset != 5 {
		t.Fatalf("expected truncated page with NextOffset 5, got %#v", got)
	}
	// Sampled file must carry its exact full-file matchCount.
	for _, fh := range got.FileHits {
		if fh.Path == "hot.txt" && fh.MatchCount != 12 {
			t.Fatalf("expected hot.txt matchCount 12, got %#v", fh)
		}
	}

	// Page 2 via offset resumes past the first page.
	page2, err := app.grepFilesWithConfig(context.Background(), ConfigState{Workspace: root}, GrepRequest{
		Pattern:    "needle",
		MaxMatches: 5,
		Offset:     got.NextOffset,
	})
	if err != nil {
		t.Fatal(err)
	}
	if page2.NextOffset != 10 {
		t.Fatalf("expected page 2 NextOffset 10, got %d", page2.NextOffset)
	}

	// Case-sensitive search narrows results.
	sensitive, err := app.grepFilesWithConfig(context.Background(), ConfigState{Workspace: root}, GrepRequest{
		Pattern:       "Needle",
		CaseSensitive: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if sensitive.MatchedLines != 0 {
		t.Fatalf("expected zero case-sensitive matches for Needle, got %#v", sensitive)
	}
	insensitive, err := app.grepFilesWithConfig(context.Background(), ConfigState{Workspace: root}, GrepRequest{
		Pattern: "Needle",
	})
	if err != nil {
		t.Fatal(err)
	}
	if insensitive.MatchedLines != 13 {
		t.Fatalf("expected case-insensitive matches for Needle, got %#v", insensitive)
	}
}

func TestCompactEditResultForModelPreservesWarnings(t *testing.T) {
	result := toolResult{OK: true, Data: MultiEditResult{
		FileCount:    1,
		Replacements: 1,
		Warnings:     []string{"change 1 ignored replace_all because it only applies to oldText; lineRange was executed normally"},
		Files: []EditResult{{
			Path:          "sample.txt",
			BeforeVersion: "abcdef",
			Version:       "ghjkmn",
		}},
	}}
	compact := compactToolResultForModel("edit", result, "fallback")
	if !strings.Contains(compact, `"warnings":["change 1 ignored replace_all`) {
		t.Fatalf("expected compact edit result to retain warnings, got %s", compact)
	}
}

func TestEditAutoValidationReturnsFailureWithoutUndoingWrite(t *testing.T) {
	root := t.TempDir()
	original := []byte("{\"ok\":true}\n")
	writeToolTestFile(t, root, "config.json", string(original))
	req := ModelEditToolRequest{Files: []FileTextEdits{{
		Path:    "config.json",
		Version: hashVersion(original),
		Changes: []TextChange{{OldText: "true", NewText: ""}},
	}}}
	payload, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	app := NewApp()
	enabled := true
	result := app.executeTool(context.Background(), ConfigState{Workspace: root, AutoValidationJSON: &enabled}, "", "edit", payload)
	if !result.OK {
		t.Fatalf("edit should remain successful when validation fails: %#v", result)
	}
	edited, ok := result.Data.(MultiEditResult)
	if !ok || !strings.Contains(edited.Validation, "自动校验失败（文件已写入）") {
		t.Fatalf("expected edit validation failure string, got %#v", result.Data)
	}
	content, err := os.ReadFile(filepath.Join(root, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "{\"ok\":}\n" {
		t.Fatalf("validation failure must not roll back the edit, got %q", content)
	}
	full, _ := json.Marshal(result)
	compact := compactToolResultForModel("edit", result, string(full))
	if !strings.Contains(compact, `"validation":"自动校验失败（文件已写入）`) {
		t.Fatalf("expected compact edit result to expose validation string, got %s", compact)
	}
}

func TestCreateFileCreatesParentAutomatically(t *testing.T) {
	root := t.TempDir()
	app := NewApp()

	result, err := app.createFileWithConfig(ConfigState{Workspace: root}, CreateFileRequest{
		Path:    "nested/file.txt",
		Content: "hello\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Path != "nested/file.txt" || result.AfterBytes == 0 {
		t.Fatalf("unexpected create result: %#v", result)
	}
	got, err := os.ReadFile(filepath.Join(root, "nested", "file.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "hello\n" {
		t.Fatalf("unexpected file content: %q", string(got))
	}
}

func TestCreateFileReportsCreatedAndCreatedDirs(t *testing.T) {
	root := t.TempDir()
	app := NewApp()
	cfg := ConfigState{Workspace: root}

	// New file with missing parents: created=true, dirs reported outermost first.
	result, err := app.createFileWithConfig(cfg, CreateFileRequest{
		Path:    "nested/a/b.txt",
		Content: "hello\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Created == nil || !*result.Created {
		t.Fatalf("expected created=true, got %v", result.Created)
	}
	want := []string{"nested", "nested/a"}
	if !reflect.DeepEqual(result.CreatedDirs, want) {
		t.Fatalf("expected createdDirs=%v, got %v", want, result.CreatedDirs)
	}
	if !strings.Contains(result.Summary, "created") {
		t.Fatalf("expected created summary, got %q", result.Summary)
	}

	// Overwrite of an existing file: created=false, no createdDirs.
	result, err = app.createFileWithConfig(cfg, CreateFileRequest{
		Path:      "nested/a/b.txt",
		Content:   "bye\n",
		Overwrite: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Created == nil || *result.Created {
		t.Fatalf("expected created=false, got %v", result.Created)
	}
	if len(result.CreatedDirs) != 0 {
		t.Fatalf("expected no createdDirs on overwrite, got %v", result.CreatedDirs)
	}

	// Create into an existing directory: created=true but no createdDirs.
	result, err = app.createFileWithConfig(cfg, CreateFileRequest{
		Path:    "nested/a/c.txt",
		Content: "hi\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Created == nil || !*result.Created {
		t.Fatalf("expected created=true, got %v", result.Created)
	}
	if len(result.CreatedDirs) != 0 {
		t.Fatalf("expected no createdDirs when parents exist, got %v", result.CreatedDirs)
	}
}

// TestCreateCompactResultCarriesCreatedFields verifies the model-facing
// compact result keeps the new create fields.
func TestCreateCompactResultCarriesCreatedFields(t *testing.T) {
	root := t.TempDir()
	app := NewApp()

	result, err := app.createFileWithConfig(ConfigState{Workspace: root}, CreateFileRequest{
		Path:    "a/b.txt",
		Content: "x\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	tr := toolResult{OK: true, Data: result}
	full, _ := json.Marshal(tr)
	compact := compactToolResultForModel("create", tr, string(full))
	if !strings.Contains(compact, `"created":true`) {
		t.Fatalf("expected compact create result to carry created=true, got %s", compact)
	}
	if !strings.Contains(compact, `"createdDirs":["a"]`) {
		t.Fatalf("expected compact create result to carry createdDirs, got %s", compact)
	}
}

func TestCreateAutoValidationIsAConciseModelString(t *testing.T) {
	root := t.TempDir()
	app := NewApp()
	enabled := true
	result := app.executeTool(context.Background(), ConfigState{Workspace: root, AutoValidationJSON: &enabled}, "", "create", []byte(`{"path":"bad.json","content":"{\"broken\":","overwrite":false}`))
	if !result.OK {
		t.Fatalf("create should succeed even when post-write validation fails: %#v", result)
	}
	created, ok := result.Data.(EditResult)
	if !ok {
		t.Fatalf("unexpected create result type %T", result.Data)
	}
	if !strings.Contains(created.Validation, "自动校验失败（文件已写入）") || !strings.Contains(created.Validation, "bad.json") {
		t.Fatalf("expected concise validation failure string, got %q", created.Validation)
	}
	full, _ := json.Marshal(result)
	compact := compactToolResultForModel("create", result, string(full))
	if !strings.Contains(compact, `"validation":"自动校验失败（文件已写入）`) {
		t.Fatalf("expected compact result to expose validation string, got %s", compact)
	}
}

func TestCreateAutoValidationPassesJSON(t *testing.T) {
	root := t.TempDir()
	app := NewApp()
	enabled := true
	result := app.executeTool(context.Background(), ConfigState{Workspace: root, AutoValidationJSON: &enabled}, "", "create", []byte(`{"path":"good.json","content":"{\"ok\":true}","overwrite":false}`))
	if !result.OK {
		t.Fatalf("unexpected create error: %#v", result)
	}
	created, ok := result.Data.(EditResult)
	if !ok || created.Validation != "自动校验通过：JSON 1 个文件" {
		t.Fatalf("expected concise JSON validation success, got %#v", result.Data)
	}
}

func TestAutoValidationCanBeDisabledPerLanguage(t *testing.T) {
	root := t.TempDir()
	disabled := false
	app := NewApp()
	result := app.executeTool(context.Background(), ConfigState{
		Workspace:          root,
		AutoValidationJSON: &disabled,
	}, "", "create", []byte(`{"path":"disabled.json","content":"{\"broken\":","overwrite":false}`))
	if !result.OK {
		t.Fatalf("create should succeed with validation disabled: %#v", result)
	}
	created, ok := result.Data.(EditResult)
	if !ok || created.Validation != "" {
		t.Fatalf("disabled JSON validation must not return a check result, got %#v", result.Data)
	}
}

func TestAutoValidationCatchesPythonSyntax(t *testing.T) {
	root := t.TempDir()
	if _, _, ok := findPythonCommand(root); !ok {
		t.Skip("python is unavailable")
	}
	enabled := true
	writeToolTestFile(t, root, "broken.py", "def broken(:\n    pass\n")
	got := NewApp().validateChangedFiles(context.Background(), ConfigState{Workspace: root, AutoValidationPython: &enabled}, []string{"broken.py"})
	if !strings.Contains(got, "自动校验失败") || !strings.Contains(got, "broken.py") {
		t.Fatalf("expected Python syntax failure, got %q", got)
	}
}

func TestAutoValidationCatchesJavaScriptSyntax(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is unavailable")
	}
	root := t.TempDir()
	enabled := true
	writeToolTestFile(t, root, "broken.js", "const = 1;\n")
	got := NewApp().validateChangedFiles(context.Background(), ConfigState{Workspace: root, AutoValidationJavaScript: &enabled}, []string{"broken.js"})
	if !strings.Contains(got, "自动校验失败") || !strings.Contains(got, "broken.js") {
		t.Fatalf("expected JavaScript syntax failure, got %q", got)
	}
}

func TestAutoValidationCatchesGoVetFailure(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go is unavailable")
	}
	root := t.TempDir()
	enabled := true
	writeToolTestFile(t, root, "go.mod", "module example.com/validation\n\ngo 1.23\n")
	writeToolTestFile(t, root, "main.go", "package main\n\nfunc main() { missing() }\n")
	got := NewApp().validateChangedFiles(context.Background(), ConfigState{Workspace: root, AutoValidationGo: &enabled}, []string{"main.go"})
	if !strings.Contains(got, "自动校验失败") || !strings.Contains(got, "Go vet") {
		t.Fatalf("expected Go vet failure, got %q", got)
	}
}

func TestFilterJavaSyntaxErrorsKeepsOnlyParseErrors(t *testing.T) {
	output := "Dep.java:3: error: cannot find symbol\n" +
		"  symbol:   class Thing\n" +
		"Dep.java:1: error: package non-existent.pkg does not exist\n" +
		"Broken.java:5: error: ';' expected\n" +
		"Broken.java:9: error: reached end of file while parsing\n" +
		"2 errors\n" +
		"Note: something\n"
	got := filterJavaSyntaxErrors(output)
	if !strings.Contains(got, "';' expected") || !strings.Contains(got, "reached end of file") {
		t.Fatalf("expected syntax errors to be kept, got %q", got)
	}
	for _, dropped := range []string{"cannot find symbol", "does not exist", "errors", "Note:"} {
		if strings.Contains(got, dropped) {
			t.Fatalf("expected %q to be dropped, got %q", dropped, got)
		}
	}
}

func TestAutoValidationJavaDependencyErrorsAreIgnored(t *testing.T) {
	if _, err := exec.LookPath("javac"); err != nil {
		t.Skip("javac is unavailable")
	}
	root := t.TempDir()
	enabled := true
	writeToolTestFile(t, root, "Dep.java", "import non-existent.pkg.Thing;\n\npublic class Dep {\n    Thing t;\n}\n")
	got := NewApp().validateChangedFiles(context.Background(), ConfigState{Workspace: root, AutoValidationJava: &enabled}, []string{"Dep.java"})
	if strings.Contains(got, "自动校验失败") {
		t.Fatalf("dependency-only javac errors must not fail validation, got %q", got)
	}
}

func TestAutoValidationSkipsJSXFiles(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is unavailable")
	}
	root := t.TempDir()
	writeToolTestFile(t, root, "component.jsx", "export const App = () => <div />;\n")
	got := NewApp().validateChangedFiles(context.Background(), ConfigState{Workspace: root}, []string{"component.jsx"})
	if strings.Contains(got, "自动校验失败") {
		t.Fatalf("jsx files must not be checked by node --check, got %q", got)
	}
}

func TestAutoValidationSkipsJSONCFiles(t *testing.T) {
	root := t.TempDir()
	enabled := true
	writeToolTestFile(t, root, "tsconfig.json", "{\n  // comments are legal JSONC\n  \"compilerOptions\": {}\n}\n")
	got := NewApp().validateChangedFiles(context.Background(), ConfigState{Workspace: root, AutoValidationJSON: &enabled}, []string{"tsconfig.json"})
	if strings.Contains(got, "自动校验失败") {
		t.Fatalf("tsconfig.json must be skipped as JSONC, got %q", got)
	}
}

func TestFilterGoVetOutputNormalizesPaths(t *testing.T) {
	rel := map[string]struct{}{"internal/app/main.go": {}}
	output := "# example.com/validation\n" +
		"vet.exe: .\\internal\\app\\main.go:3:15: undefined: missing\n" +
		"vet: ./internal/app/other.go:1:1: printf: bogus\n" +
		"internal/app/main.go:9:2: unreachable code\n"
	got := filterGoVetOutput(output, rel)
	if !strings.Contains(got, "undefined: missing") || !strings.Contains(got, "unreachable code") {
		t.Fatalf("expected changed-file diagnostics to be kept, got %q", got)
	}
	if strings.Contains(got, "other.go") || strings.Contains(got, "example.com") {
		t.Fatalf("expected untouched-file diagnostics to be dropped, got %q", got)
	}
}

func TestGoDependencyIssueDetectsModuleFailures(t *testing.T) {
	for _, output := range []string{
		"main.go:1:2: no required module provides package foo; to add it: go get foo",
		"go: cannot find main module; ...",
		"main.go:5:2: missing go.sum entry for module",
	} {
		if !goDependencyIssue(output) {
			t.Fatalf("expected dependency issue for %q", output)
		}
	}
	if goDependencyIssue("main.go:3:15: undefined: missing") {
		t.Fatal("code error must not be classified as dependency issue")
	}
}

func TestFilterTypeScriptSyntaxErrorsKeepsChangedFileSyntaxOnly(t *testing.T) {
	dir := t.TempDir()
	files := []validationFile{{abs: filepath.Join(dir, "src", "a.ts"), display: "src/a.ts", ext: ".ts"}}
	output := "src/a.ts(12,5): error TS1005: ';' expected\n" +
		"src/a.ts(30,1): error TS2322: Type 'string' is not assignable to type 'number'\n" +
		"src/b.ts(8,9): error TS1005: Declaration or statement expected\n" +
		"src/a.ts(3,1): error TS2307: Cannot find module './missing'\n"
	got := filterTypeScriptSyntaxErrors(output, dir, files)
	if !strings.Contains(got, "TS1005: ';' expected") {
		t.Fatalf("expected changed-file syntax error to be kept, got %q", got)
	}
	for _, dropped := range []string{"TS2322", "src/b.ts", "TS2307"} {
		if strings.Contains(got, dropped) {
			t.Fatalf("expected %q to be dropped, got %q", dropped, got)
		}
	}
}

func TestValidationSkipReportMapsTimeoutAndCancel(t *testing.T) {
	report, ok := validationSkipReport("Go vet", context.DeadlineExceeded)
	if !ok || !report.skipped || report.passed {
		t.Fatalf("expected timeout to be skipped, got %#v ok=%v", report, ok)
	}
	report, ok = validationSkipReport("Go vet", context.Canceled)
	if !ok || !report.skipped {
		t.Fatalf("expected cancel to be skipped, got %#v ok=%v", report, ok)
	}
	if _, ok := validationSkipReport("Go vet", errors.New("boom")); ok {
		t.Fatal("plain errors must not be skipped")
	}
}

func TestCreateFileRefusesNonTextOverwrite(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "binary.dat")
	original := []byte{'a', 0, 'b'}
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	app := NewApp()

	_, err := app.createFileWithConfig(ConfigState{Workspace: root}, CreateFileRequest{
		Path:      "binary.dat",
		Content:   "replacement\n",
		Overwrite: true,
	})
	if err == nil {
		t.Fatal("expected binary overwrite to fail")
	}
	if code := toolErrorCode(err); code != "E_TEXT_OVERWRITE" {
		t.Fatalf("expected E_TEXT_OVERWRITE, got %q (%v)", code, err)
	}
	got, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(got) != string(original) {
		t.Fatalf("expected binary file to remain unchanged, got %v", got)
	}
}

func TestDeletePathRemovesFinalSymlinkOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires privileges on many Windows environments")
	}
	root := t.TempDir()
	outside := t.TempDir()
	outsideFile := filepath.Join(outside, "target.txt")
	if err := os.WriteFile(outsideFile, []byte("keep\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link.txt")
	if err := os.Symlink(outsideFile, link); err != nil {
		t.Fatal(err)
	}
	app := NewApp()

	result, err := app.deletePathWithConfig(ConfigState{Workspace: root}, DeletePathRequest{Path: "link.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Kind != "symlink" || !result.WasSymlink || result.RemovedFiles != 1 {
		t.Fatalf("unexpected symlink delete result: %#v", result)
	}
	if _, err := os.Lstat(link); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected symlink to be removed, lstat err=%v", err)
	}
	if got, err := os.ReadFile(outsideFile); err != nil || string(got) != "keep\n" {
		t.Fatalf("expected symlink target to remain, got %q err=%v", string(got), err)
	}
}

func TestCreateFileRejectsSymlinkParentOutsideWorkspace(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires privileges on many Windows environments")
	}
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	app := NewApp()

	_, err := app.createFileWithConfig(ConfigState{Workspace: root}, CreateFileRequest{
		Path:    "escape/file.txt",
		Content: "nope\n",
	})
	if err == nil {
		t.Fatal("expected symlink parent outside workspace to fail")
	}
	if code := toolErrorCode(err); code != "E_PATH_OUTSIDE" {
		t.Fatalf("expected E_PATH_OUTSIDE, got %q (%v)", code, err)
	}
	if _, statErr := os.Stat(filepath.Join(outside, "file.txt")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("expected outside target not to be created, stat err=%v", statErr)
	}
}

func TestRunCommandInvalidatesWorkspaceMapCache(t *testing.T) {
	root := t.TempDir()
	writeToolTestFile(t, root, "go.mod", "module example\n")
	app := NewApp()
	cfg := ConfigState{Workspace: root}

	first := app.workspaceMapContext(cfg)
	mustNotContain(t, first, "generated.txt")

	command := "printf 'hello\\n' > generated.txt"
	if runtime.GOOS == "windows" {
		// Use PowerShell syntax only when bash is not available.
		if _, bashName := findWindowsBash(""); bashName == "" {
			command = "Set-Content -Path generated.txt -Value hello"
		}
	}
	args, err := json.Marshal(CommandRequest{Command: command})
	if err != nil {
		t.Fatal(err)
	}
	result := app.executeTool(context.Background(), cfg, "session-1", "command", args)
	if !result.OK {
		t.Fatalf("expected command to succeed, got %#v", result)
	}
	refreshed := app.workspaceMapContext(cfg)
	mustContain(t, refreshed, "generated.txt")
}

func TestRunCommandRejectsLongRunningService(t *testing.T) {
	root := t.TempDir()
	app := NewApp()
	args, err := json.Marshal(CommandRequest{Command: "python manage.py runserver --noreload 0.0.0.0:8000"})
	if err != nil {
		t.Fatal(err)
	}
	result := app.executeTool(context.Background(), ConfigState{Workspace: root}, "session-1", "command", args)
	if result.OK {
		t.Fatalf("expected command to reject long-running service, got %#v", result)
	}
	if result.ErrorCode != "E_LONG_RUNNING_COMMAND" {
		t.Fatalf("expected E_LONG_RUNNING_COMMAND, got %#v", result)
	}
}

func TestCommandSafetyAllowsCmdSlashCOption(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("cmd.exe /c is Windows-specific")
	}
	if err := checkCommandSafety(CommandRequest{Command: "cmd.exe /c ver"}, []string{t.TempDir()}); err != nil {
		t.Fatalf("cmd.exe /c option must not be treated as a C drive path: %v", err)
	}
}

func TestCommandSafetyAllowsReadOnlyOutsidePath(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("MSYS2 drive paths are Windows-specific")
	}
	command := `(ls -la /d/coding/python/ | grep xx)`
	if err := checkCommandSafety(CommandRequest{Command: command}, []string{t.TempDir()}); err != nil {
		t.Fatalf("read-only outside inspection should be allowed: %v", err)
	}
}

func TestCommandSafetyAllowsGitBashOutsideReadsWithDevNull(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("MSYS2 drive paths are Windows-specific")
	}
	commands := []string{
		`(ls -la /c/Users/DELL/.agents/ 2>/dev/null && echo "---FOUND---" || echo "---NOT FOUND---")`,
		`(find /c/Users/DELL/ -maxdepth 5 -name "SKILL.md" -path "ai-writing" 2>/dev/null | head -5)`,
	}
	for _, command := range commands {
		if err := checkCommandSafety(CommandRequest{Command: command}, []string{t.TempDir()}); err != nil {
			t.Fatalf("Git Bash outside read with /dev/null should be allowed: %v", err)
		}
	}
}

func TestCommandSafetyAllowsGitLogPrettyFormatWithEmailBrackets(t *testing.T) {
	command := `git status --short && git log -1 --pretty=format:'%h %an <%ae> %s'`
	if err := checkCommandSafety(CommandRequest{Command: command}, []string{t.TempDir()}); err != nil {
		t.Fatalf("quoted git pretty format should not be parsed as shell redirection: %v", err)
	}
}

func TestCommandSafetyAllowsRiskAndDeleteWordsAsData(t *testing.T) {
	commands := []string{
		`grep -R "rm" docs`,
		`echo remove-item`,
		`git log --grep=shutdown`,
		`echo "reboot"`,
		`rg mkfs docs`,
		`dd if=input.bin bs=1 count=16`,
		`chmod 064 file.txt`,
	}
	for _, command := range commands {
		if err := checkCommandSafety(CommandRequest{Command: command}, []string{t.TempDir()}); err != nil {
			t.Fatalf("normal command %q should be allowed: %v", command, err)
		}
	}
}

func TestCommandSafetyAllowsOutsideInputsWithWorkspaceOutputs(t *testing.T) {
	workspace := t.TempDir()
	outsideDir := t.TempDir()
	source := filepath.Join(outsideDir, "source.txt")
	archive := filepath.Join(outsideDir, "archive.zip")
	if err := os.WriteFile(source, []byte("source\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(archive, []byte("archive\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	commands := []string{
		fmt.Sprintf(`cp %q copied.txt`, filepath.ToSlash(source)),
		fmt.Sprintf(`python -c "print(open(%q).read())"`, filepath.ToSlash(source)),
		fmt.Sprintf(`unzip -l %q`, filepath.ToSlash(archive)),
		fmt.Sprintf(`unzip %q -d .`, filepath.ToSlash(archive)),
	}
	for _, command := range commands {
		if err := checkCommandSafety(CommandRequest{Command: command}, []string{workspace}); err != nil {
			t.Fatalf("outside input with workspace output should be allowed for %q: %v", command, err)
		}
	}
}

func TestCommandSafetyBlocksActualAndNestedDangerousCommands(t *testing.T) {
	commands := []string{
		`rm generated.txt`,
		`bash -c "rm generated.txt"`,
		`echo generated.txt | xargs rm`,
		`echo $(rm generated.txt)`,
		`shutdown -h now`,
		`dd if=image.iso of=/dev/sda`,
		`chmod 000 secrets.txt`,
	}
	for _, command := range commands {
		if code := toolErrorCode(checkCommandSafety(CommandRequest{Command: command}, []string{t.TempDir()})); code != "E_COMMAND_BLOCKED" {
			t.Fatalf("dangerous command %q should be blocked, got code %q", command, code)
		}
	}
}

func TestCommandSafetyBlocksMixedManagedAndRawDeletion(t *testing.T) {
	command := `git rm --cached tracked.txt; rm generated.txt`
	if code := toolErrorCode(checkCommandSafety(CommandRequest{Command: command}, []string{t.TempDir()})); code != "E_COMMAND_BLOCKED" {
		t.Fatalf("mixed managed and raw deletion should be blocked, got code %q", code)
	}
}

func TestCommandSafetyChecksCopyDestinationNotOutsideSource(t *testing.T) {
	workspace := t.TempDir()
	outsideDir := t.TempDir()
	source := filepath.Join(outsideDir, "source.txt")
	destination := filepath.Join(outsideDir, "destination.txt")
	for _, path := range []string{source, destination} {
		if err := os.WriteFile(path, []byte("keep\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	allowed := fmt.Sprintf(`cp %q copied.txt`, filepath.ToSlash(source))
	if err := checkCommandSafety(CommandRequest{Command: allowed}, []string{workspace}); err != nil {
		t.Fatalf("outside copy source should be readable: %v", err)
	}

	blocked := fmt.Sprintf(`cp copied.txt %q`, filepath.ToSlash(destination))
	if code := toolErrorCode(checkCommandSafety(CommandRequest{Command: blocked}, []string{workspace})); code != "E_PATH_OUTSIDE" {
		t.Fatalf("existing outside copy destination should be blocked, got code %q", code)
	}
}

func TestCommandSafetyAllowsCreatingNewOutsidePath(t *testing.T) {
	workspace := t.TempDir()
	outside := t.TempDir()
	commands := []string{
		fmt.Sprintf(`touch %q`, filepath.ToSlash(filepath.Join(outside, "new-touch.txt"))),
		fmt.Sprintf(`printf created > %q`, filepath.ToSlash(filepath.Join(outside, "new-redirect.txt"))),
	}
	for _, command := range commands {
		if err := checkCommandSafety(CommandRequest{Command: command}, []string{workspace}); err != nil {
			t.Fatalf("creating a new outside path should be allowed for %q: %v", command, err)
		}
	}
}

func TestCommandSafetyBlocksModifyingExistingOutsidePath(t *testing.T) {
	workspace := t.TempDir()
	target := filepath.Join(t.TempDir(), "existing.txt")
	if err := os.WriteFile(target, []byte("keep\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{
		fmt.Sprintf(`touch %q`, filepath.ToSlash(target)),
		fmt.Sprintf(`printf changed > %q`, filepath.ToSlash(target)),
		fmt.Sprintf(`printf changed >> %q`, filepath.ToSlash(target)),
	} {
		err := checkCommandSafety(CommandRequest{Command: command}, []string{workspace})
		if toolErrorCode(err) != "E_PATH_OUTSIDE" {
			t.Fatalf("existing outside target should be blocked for %q, got %v", command, err)
		}
		for _, want := range []string{"安全围栏已拦截", "工作区外", "检测到的目标", "允许的操作", "禁止的操作", command} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("error should explain %q for %q, got %v", want, command, err)
			}
		}
	}
}

func TestCommandSafetyResolvesRelativeMutationFromCwd(t *testing.T) {
	workspace := t.TempDir()
	cwd := filepath.Join(workspace, "build")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "existing.txt")
	if err := os.WriteFile(target, []byte("keep\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	relative, err := filepath.Rel(cwd, target)
	if err != nil {
		t.Fatal(err)
	}
	command := fmt.Sprintf(`touch %q`, filepath.ToSlash(relative))
	if code := toolErrorCode(checkCommandSafety(CommandRequest{Command: command, Cwd: "build"}, []string{workspace})); code != "E_PATH_OUTSIDE" {
		t.Fatalf("relative mutation must resolve from cwd and be blocked, got %q", code)
	}
}

func TestCommandSafetyBlocksWorkspaceSymlinkMutation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires privileges on many Windows environments")
	}
	workspace := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(workspace, "link")); err != nil {
		t.Fatal(err)
	}
	existing := filepath.Join(outside, "existing.txt")
	if err := os.WriteFile(existing, []byte("keep\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	commands := []string{
		`touch link/existing.txt`,
		`printf created > link/new.txt`,
	}
	for _, command := range commands {
		if code := toolErrorCode(checkCommandSafety(CommandRequest{Command: command}, []string{workspace})); code != "E_PATH_OUTSIDE" {
			t.Fatalf("workspace symlink mutation must be blocked for %q, got %q", command, code)
		}
	}
}

func TestCommandSafetyAllowsDynamicRedirectionTarget(t *testing.T) {
	// 动态目标（变量展开、通配符、heredoc 内容）无法静态解析：宽松策略下放行，
	// 避免对合法复杂命令误判。字面外部已存在目标仍由其他用例覆盖拦截。
	err := checkCommandSafety(CommandRequest{Command: `printf changed > "$HOME/existing.txt"`}, []string{t.TempDir()})
	if err != nil {
		t.Fatalf("dynamic redirection target should be allowed under permissive policy, got %v", err)
	}
}

func TestCommandSafetyAllowsHeredocBodyContents(t *testing.T) {
	// heredoc body 是 stdin 数据，其中的 > 重定向外观、rm 等词、$() 替换
	// 在带引号 delimiter（<<'EOF'）下全部字面，不得触发拦截。
	commands := []string{
		"python - <<'EOF'\nif v > 1:\n    pass\nEOF\n",
		"python - <<'EOF'\nrm -rf /tmp/x\nEOF\n",
		"python - <<'EOF'\nprint('$(rm -rf /tmp/x)')\nEOF\n",
		"python - <<EOF\nif v > 1:\n    pass\nEOF\n",
	}
	for _, command := range commands {
		if err := checkCommandSafety(CommandRequest{Command: command}, []string{t.TempDir()}); err != nil {
			t.Fatalf("heredoc body contents should be allowed for %q: %v", command, err)
		}
	}
}

func TestCommandSafetyBlocksCommandSubstitutionInsideUnquotedHeredoc(t *testing.T) {
	// 无引号 heredoc 中 $(...) 会真实执行，命令替换内的删除命令仍须拦截。
	command := "python - <<EOF\n$(rm generated.txt)\nEOF\n"
	if code := toolErrorCode(checkCommandSafety(CommandRequest{Command: command}, []string{t.TempDir()})); code != "E_COMMAND_BLOCKED" {
		t.Fatalf("command substitution inside unquoted heredoc should be blocked, got code %q", code)
	}
}

func TestCommandSafetyStillBlocksLiteralOutsideTarget(t *testing.T) {
	// 字面外部已存在目标仍是可靠检查，必须继续拦截。
	workspace := t.TempDir()
	target := filepath.Join(t.TempDir(), "existing.txt")
	if err := os.WriteFile(target, []byte("keep\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := checkCommandSafety(CommandRequest{Command: fmt.Sprintf(`printf changed > %q`, filepath.ToSlash(target))}, []string{workspace})
	if toolErrorCode(err) != "E_PATH_OUTSIDE" {
		t.Fatalf("literal existing outside target should still be blocked, got %v", err)
	}
}

func TestCommandSafetyAllowsWorkspaceRedirectWhileReadingOutside(t *testing.T) {
	workspace := t.TempDir()
	outside := filepath.Join(t.TempDir(), "source.txt")
	if err := os.WriteFile(outside, []byte("source\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	command := fmt.Sprintf(`type %q > result.txt`, filepath.ToSlash(outside))
	if runtime.GOOS != "windows" {
		command = fmt.Sprintf(`cat %q > result.txt`, filepath.ToSlash(outside))
	}
	if err := checkCommandSafety(CommandRequest{Command: command}, []string{workspace}); err != nil {
		t.Fatalf("workspace redirect while reading outside should be allowed: %v", err)
	}
}

func TestCommandSafetyBlocksExistingOutsidePathMutation(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("MSYS2 drive paths are Windows-specific")
	}
	outDir := t.TempDir()
	target := filepath.Join(outDir, "existing.txt")
	if err := os.WriteFile(target, []byte("keep\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	volume := filepath.VolumeName(target)
	msysTarget := "/" + strings.ToLower(strings.TrimSuffix(volume, ":")) + filepath.ToSlash(strings.TrimPrefix(target, volume))
	err := checkCommandSafety(CommandRequest{Command: `touch ` + msysTarget}, []string{t.TempDir()})
	if toolErrorCode(err) != "E_PATH_OUTSIDE" {
		t.Fatalf("outside mutation should be blocked with E_PATH_OUTSIDE, got %v", err)
	}
}

func TestWebFetchModelContextKeepsDefaultSizedPage(t *testing.T) {
	page := strings.Repeat("complete page content\n", 2500)
	result := toolResult{OK: true, Data: WebFetchResult{URL: "https://example.com", Text: page}}
	compact := compactToolResultForModel("web_fetch", result, "fallback")
	if strings.Contains(compact, "characters omitted") || strings.Contains(compact, `"textReduced":true`) {
		t.Fatalf("default-sized web page should remain complete for the model")
	}
	if !strings.Contains(compact, "complete page content") || len(compact) < len(page) {
		t.Fatalf("expected full web page text in model context: compact=%d page=%d", len(compact), len(page))
	}
}

func TestWebFetchDefaultSourceLimitReadsPastLegacyHTTPCap(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, "<html><body><script>"+strings.Repeat("x", 300*1024)+"</script><p>visible tail marker</p></body></html>")
	}))
	defer server.Close()

	app := NewApp()
	got, err := app.webFetchToolWithConfig(context.Background(), ConfigState{AllowPrivateNetwork: true}, WebFetchRequest{URL: server.URL, MaxChars: 1000})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.Text, "visible tail marker") {
		t.Fatalf("expected readable text after 256KB source boundary, got %q", got.Text)
	}
}

func TestFindWindowsBashDerivesPortableInstallFromGitPath(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows shell discovery is Windows-specific")
	}
	root := t.TempDir()
	gitPath := filepath.Join(root, "cmd", "git.exe")
	bashPath := filepath.Join(root, "bin", "bash.exe")
	for _, path := range []string{gitPath, bashPath} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("test fixture"), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	t.Setenv("ProgramFiles", filepath.Join(root, "missing-program-files"))
	t.Setenv("ProgramFiles(x86)", filepath.Join(root, "missing-program-files-x86"))
	t.Setenv("LOCALAPPDATA", filepath.Join(root, "missing-local-app-data"))
	t.Setenv("PATH", filepath.Dir(gitPath))

	got, name := findWindowsBash("")
	if name != "bash" || !samePath(got, bashPath) {
		t.Fatalf("expected derived Git Bash %q, got name=%q path=%q", bashPath, name, got)
	}
}

func TestFindWindowsBashRejectsUnrelatedBashExecutable(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows shell discovery is Windows-specific")
	}
	root := t.TempDir()
	bashPath := filepath.Join(root, "System32", "bash.exe")
	if err := os.MkdirAll(filepath.Dir(bashPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bashPath, []byte("not Git Bash"), 0o755); err != nil {
		t.Fatal(err)
	}

	t.Setenv("ProgramFiles", filepath.Join(root, "missing-program-files"))
	t.Setenv("ProgramFiles(x86)", filepath.Join(root, "missing-program-files-x86"))
	t.Setenv("LOCALAPPDATA", filepath.Join(root, "missing-local-app-data"))
	t.Setenv("PATH", filepath.Dir(bashPath))

	if got, name := findWindowsBash(""); got != "" || name != "" {
		t.Fatalf("expected unrelated bash.exe to be rejected, got name=%q path=%q", name, got)
	}
}

func TestRunCommandWithGitBashResolvesWindowsToolchainAndShellExpansion(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows Git Bash behavior is Windows-specific")
	}
	bashPath, _ := findWindowsBash("")
	if bashPath == "" {
		t.Skip("Git Bash is not installed")
	}

	app := NewApp()
	result, err := app.runCommandWithConfig(context.Background(), ConfigState{Workspace: t.TempDir()}, CommandRequest{
		Command: `go version; value=$(printf 'ok'); printf 'value=%s\nbash=%s\n' "$value" "$BASH_VERSION"`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("expected Git Bash command to succeed, got exit=%d output=%q", result.ExitCode, result.Output)
	}
	if !samePath(result.ShellPath, bashPath) {
		t.Fatalf("expected shell path %q, got %q", bashPath, result.ShellPath)
	}
	for _, want := range []string{"go version", "value=ok", "bash="} {
		if !strings.Contains(result.Output, want) {
			t.Fatalf("expected output to contain %q, got %q", want, result.Output)
		}
	}
}

func TestListServicesStartsEmpty(t *testing.T) {
	app := NewApp()
	data := app.ListServices()
	if len(data.Services) != 0 {
		t.Fatalf("expected no services, got %#v", data.Services)
	}
}

func TestRunCommandRejectsCwdSymlinkOutsideWorkspace(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires privileges on many Windows environments")
	}
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	app := NewApp()

	args, err := json.Marshal(CommandRequest{Command: "pwd", Cwd: "escape"})
	if err != nil {
		t.Fatal(err)
	}
	result := app.executeTool(context.Background(), ConfigState{Workspace: root}, "session-1", "command", args)
	if result.OK {
		t.Fatalf("expected command to reject outside symlink cwd, got %#v", result)
	}
	if result.ErrorCode != "E_PATH_OUTSIDE" {
		t.Fatalf("expected E_PATH_OUTSIDE, got %#v", result)
	}
}

func writeToolTestFile(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRunReadCacheReturnsMetadataWithoutDuplicateContent(t *testing.T) {
	dir := t.TempDir()
	writeToolTestFile(t, dir, "sample.txt", "one\ntwo\n")
	app := NewApp()
	cache := newRunReadCache()
	ctx := context.WithValue(context.Background(), runReadCacheContextKey{}, cache)
	args := []byte(`{"files":[{"path":"sample.txt"}]}`)

	first := app.executeTool(ctx, ConfigState{Workspace: dir}, "session-1", "read", args)
	if !first.OK {
		t.Fatalf("first read failed: %#v", first)
	}
	firstData := first.Data.(*BatchReadResult)
	if len(firstData.Files) != 1 || firstData.Files[0].Content == "" || firstData.Files[0].Reused {
		t.Fatalf("expected complete first read, got %#v", firstData)
	}
	// The stored cache entry itself must be content-free; store() strips the
	// payload before caching, so the cache cannot hold file contents.
	if len(cache.entries) != 1 {
		t.Fatalf("cache entries after first read = %d, want 1", len(cache.entries))
	}
	for _, e := range cache.entries {
		if e.result == nil || len(e.result.Files) != 1 {
			t.Fatalf("unexpected cached entry: %#v", e.result)
		}
		f := e.result.Files[0]
		if f.Content != "" || f.Text != "" || f.DataURL != "" {
			t.Fatalf("cached entry holds file content: %#v", f)
		}
		if !f.Reused || f.Version == "" {
			t.Fatalf("cached entry should be a receipt with version, got: %#v", f)
		}
	}

	second := app.executeTool(ctx, ConfigState{Workspace: dir}, "session-1", "read", args)
	if !second.OK {
		t.Fatalf("second read failed: %#v", second)
	}
	secondData := second.Data.(*BatchReadResult)
	if len(secondData.Files) != 1 || !secondData.Files[0].Reused || secondData.Files[0].Content != "" || secondData.Files[0].Version == "" {
		t.Fatalf("expected metadata-only reused read, got %#v", secondData)
	}

	// The model-facing compacted results must carry content on the first read
	// and a human-readable explanation on the reuse, not an empty content field
	// with a bare reused flag.
	firstModel := compactToolResultForModel("read", first, "")
	if !strings.Contains(firstModel, "one") || strings.Contains(firstModel, "Content omitted") {
		t.Fatalf("expected first compact read to carry content, got: %s", firstModel)
	}
	secondModel := compactToolResultForModel("read", second, "")
	if !strings.Contains(secondModel, "Content omitted") || !strings.Contains(secondModel, `"reused":true`) {
		t.Fatalf("expected reused compact read to explain the omission, got: %s", secondModel)
	}

	// The receipt is one-shot: serving the hit removes the entry, so a third
	// identical read (without any invalidation) re-reads the file and returns
	// the full content again.
	if len(cache.entries) != 0 {
		t.Fatalf("cache entries after second read = %d, want 0 (receipt consumed)", len(cache.entries))
	}
	third := app.executeTool(ctx, ConfigState{Workspace: dir}, "session-1", "read", args)
	thirdData := third.Data.(*BatchReadResult)
	if !third.OK || len(thirdData.Files) != 1 || thirdData.Files[0].Reused || thirdData.Files[0].Content == "" {
		t.Fatalf("expected full read after receipt consumption, got %#v", third)
	}

	invalidateRunReadCache(ctx)
	fourth := app.executeTool(ctx, ConfigState{Workspace: dir}, "session-1", "read", args)
	fourthData := fourth.Data.(*BatchReadResult)
	if !fourth.OK || len(fourthData.Files) != 1 || fourthData.Files[0].Reused || fourthData.Files[0].Content == "" {
		t.Fatalf("expected full read after invalidation, got %#v", fourth)
	}
}

func TestRunReadCacheEvictsEntriesUnderPressure(t *testing.T) {
	cache := newRunReadCache()
	// Fill past the entry budget; every insertion is a distinct key.
	for i := 0; i < runReadCacheMaxEntries+8; i++ {
		cache.store(fmt.Sprintf("key-%d", i), &BatchReadResult{
			Files: []BatchReadResultItem{{Path: fmt.Sprintf("f%d.txt", i), Content: "x", Version: "aaaaaa"}},
		})
	}
	if got := len(cache.entries); got > runReadCacheMaxEntries {
		t.Fatalf("cache entries = %d, want <= %d", got, runReadCacheMaxEntries)
	}
	// Re-storing an existing key must not grow the cache.
	cache.store("key-0", &BatchReadResult{
		Files: []BatchReadResultItem{{Path: "f0.txt", Content: "y", Version: "bbbbbb"}},
	})
	if got := len(cache.entries); got > runReadCacheMaxEntries {
		t.Fatalf("cache entries after re-store = %d, want <= %d", got, runReadCacheMaxEntries)
	}
}

// TestReadRejectsOfficeDocumentsWithAnydocGuidance covers the deliberate
// removal of built-in document extraction: office/PDF files must fail with the
// coded E_DOCUMENT_UNSUPPORTED error that points at the anydoc skill instead
// of being parsed (or failing as a generic binary file).
func TestReadRejectsOfficeDocumentsWithAnydocGuidance(t *testing.T) {
	dir := t.TempDir()
	// Content is irrelevant; the extension decides the rejection.
	for _, name := range []string{"spec.docx", "deck.pptx", "book.xlsx", "scan.pdf"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("PK\x03\x04-not-really-parsed"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	app := NewApp()
	cfg := ConfigState{Workspace: dir}

	result, err := app.batchReadFilesWithConfig(cfg, BatchReadRequest{
		Files: []BatchReadFileRequest{
			{Path: "spec.docx"},
			{Path: "deck.pptx"},
			{Path: "book.xlsx"},
			{Path: "scan.pdf"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Files) != 4 {
		t.Fatalf("expected 4 per-file errors, got %#v", result.Files)
	}
	for _, f := range result.Files {
		if f.ErrorCode != "E_DOCUMENT_UNSUPPORTED" {
			t.Fatalf("%s: expected E_DOCUMENT_UNSUPPORTED, got %#v", f.Path, f)
		}
		if !strings.Contains(f.Error, "anydoc") {
			t.Fatalf("%s: error must mention anydoc, got %q", f.Path, f.Error)
		}
	}
}

func TestBatchReadKeepsSamePathWithDifferentEffectiveRanges(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "sample.txt"), []byte("one\ntwo\nthree\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	app := NewApp()
	cfg := ConfigState{Workspace: dir}

	result, err := app.batchReadFilesWithConfig(cfg, BatchReadRequest{
		Files: []BatchReadFileRequest{
			{Path: "sample.txt", EndLine: 1},
			{Path: "sample.txt", StartLine: 2},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Files) != 2 {
		t.Fatalf("expected both distinct read ranges, got %d result(s): %#v", len(result.Files), result.Files)
	}
	if result.Files[0].Content != "1: one" {
		t.Fatalf("expected first read to contain numbered line 1, got:\n%s", result.Files[0].Content)
	}
	if result.Files[1].Content != "2: two\n3: three" {
		t.Fatalf("expected second read to contain numbered lines through EOF, got:\n%s", result.Files[1].Content)
	}
	if result.Files[0].ContentFormat != "line_numbers" || result.Files[0].Version != hashVersion([]byte("one\ntwo\nthree\n")) {
		t.Fatalf("expected numbered content with version metadata, got %#v", result.Files[0])
	}
}

func TestReadPreviewHandlesMillionLineRangeWithoutExpandingAllLines(t *testing.T) {
	const totalLines = 1_000_000
	content := strings.Repeat("x\n", totalLines)

	preview, err := formatLineNumberReadPreviewRangeWithBudget(content, readRangeRequest{
		StartLine: totalLines - 1,
		EndLine:   totalLines,
	}, 1024)
	if err != nil {
		t.Fatal(err)
	}
	if preview.TotalLines != totalLines || preview.StartLine != totalLines-1 || preview.EndLine != totalLines {
		t.Fatalf("unexpected million-line metadata: %#v", preview)
	}
	if preview.RawContent != "x\nx\n" {
		t.Fatalf("unexpected million-line raw range: %q", preview.RawContent)
	}
	if preview.Truncated || preview.NextStartLine != 0 || preview.RangeStatus != "ok" {
		t.Fatalf("explicit final range should be complete: %#v", preview)
	}
	if len(preview.Content) > 1024 {
		t.Fatalf("preview exceeded byte budget: %d", len(preview.Content))
	}
}

func TestReadPreviewNormalizesReversedRangeBeforeEOFCheck(t *testing.T) {
	content := strings.Repeat("x\n", 50)

	preview, err := formatLineNumberReadPreviewRangeWithBudget(content, readRangeRequest{StartLine: 100, EndLine: 20}, 4096)
	if err != nil {
		t.Fatal(err)
	}
	if preview.StartLine != 20 || preview.EndLine != 50 || preview.EmptyRange {
		t.Fatalf("expected reversed range to normalize and clamp to 20-50, got %#v", preview)
	}

	beyond, err := formatLineNumberReadPreviewRangeWithBudget(content, readRangeRequest{StartLine: 100, EndLine: 80}, 4096)
	if err != nil {
		t.Fatal(err)
	}
	if !beyond.EmptyRange || beyond.RangeStatus != "beyond_eof" || beyond.StartLine != 80 {
		t.Fatalf("expected normalized start line 80 to remain beyond EOF, got %#v", beyond)
	}
}

func TestReadPreviewBoundsVeryLongUnicodeLine(t *testing.T) {
	content := strings.Repeat("界", 1_000_000)
	const budget = 1024

	preview, err := formatLineNumberReadPreviewRangeWithBudget(content, readRangeRequest{}, budget)
	if err != nil {
		t.Fatal(err)
	}
	if preview.TotalLines != 1 || preview.StartLine != 1 || preview.EndLine != 1 {
		t.Fatalf("unexpected long-line metadata: %#v", preview)
	}
	if !preview.Truncated || preview.RangeStatus != "truncated" {
		t.Fatalf("long line should report truncation: %#v", preview)
	}
	if len(preview.Content) > budget+128 || len(preview.RawContent) > budget {
		t.Fatalf("long-line preview exceeded bounded output: numbered=%d raw=%d", len(preview.Content), len(preview.RawContent))
	}
	if !utf8.ValidString(preview.Content) || !utf8.ValidString(preview.RawContent) {
		t.Fatal("long-line truncation split a UTF-8 sequence")
	}
}

func TestBatchReadSupportsNegativeTailRange(t *testing.T) {
	dir := t.TempDir()
	content := "one\ntwo\nthree\nfour\nfive"
	if err := os.WriteFile(filepath.Join(dir, "sample.txt"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	app := NewApp()
	result, err := app.batchReadFilesWithConfig(ConfigState{Workspace: dir}, BatchReadRequest{
		Files: []BatchReadFileRequest{{Path: "sample.txt", StartLine: -2}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Files) != 1 {
		t.Fatalf("expected one tail-read result, got %#v", result)
	}
	file := result.Files[0]
	if file.Content != "4: four\n5: five" {
		t.Fatalf("unexpected tail content:\n%s", file.Content)
	}
	if file.StartLine != 4 || file.EndLine != 5 || file.TotalLines != 5 || file.NextStartLine != 0 || file.Truncated {
		t.Fatalf("unexpected tail metadata: %#v", file)
	}
}

func TestLineStartOffsetFromTailMatchesForwardScan(t *testing.T) {
	content := "one\ntwo\nthree\n\nfive\n"
	total := countPlainTextLines(content)
	for line := 1; line <= total; line++ {
		forward := lineStartOffset(content, line)
		backward := lineStartOffsetFromTail(content, total, line)
		if forward != backward {
			t.Fatalf("line %d: forward=%d tail=%d", line, forward, backward)
		}
	}
}

func TestReadPreviewTailClampsAndHandlesTrailingNewline(t *testing.T) {
	content := "one\ntwo\nthree\n"
	preview, err := formatLineNumberReadPreviewRangeWithBudget(content, readRangeRequest{StartLine: -10}, 4096)
	if err != nil {
		t.Fatal(err)
	}
	if preview.StartLine != 1 || preview.EndLine != 3 || preview.Content != "1: one\n2: two\n3: three" {
		t.Fatalf("tail beyond total should clamp to the whole file: %#v", preview)
	}
	if preview.Truncated || preview.NextStartLine != 0 {
		t.Fatalf("clamped full-file tail should not be truncated: %#v", preview)
	}
}
func TestReadPreviewRejectsInvalidNegativeTailRanges(t *testing.T) {
	for _, req := range []readRangeRequest{
		{StartLine: -1, EndLine: 1},
		{StartLine: -1, LineCount: 1},
		{StartLine: -maxReadRangeLines - 1},
	} {
		if _, err := formatLineNumberReadPreviewRangeWithBudget("one\ntwo\n", req, 4096); err == nil {
			t.Fatalf("expected invalid negative tail request to fail: %#v", req)
		}
	}
}

func TestReadPreviewTruncatesLongLinesAndBoundsTruncatedLineMetadata(t *testing.T) {
	longLine := strings.Repeat("界", maxReadLineChars+10)
	preview, err := formatLineNumberReadPreviewRangeWithBudget(longLine+"\nend\n", readRangeRequest{StartLine: 1, EndLine: 1}, 16*1024)
	if err != nil {
		t.Fatal(err)
	}
	if !preview.Truncated || preview.RangeStatus != "truncated" || len(preview.TruncatedLines) != 1 || preview.TruncatedLines[0] != 1 || preview.TruncatedLinesOmitted {
		t.Fatalf("unexpected long-line truncation metadata: %#v", preview)
	}
	line := strings.TrimPrefix(preview.Content, "1: ")
	if !strings.HasSuffix(line, "...") || utf8.RuneCountInString(line) != maxReadLineChars {
		t.Fatalf("expected a %d-rune ellipsis preview, got %d runes", maxReadLineChars, utf8.RuneCountInString(line))
	}
	if !utf8.ValidString(preview.Content) || !utf8.ValidString(preview.RawContent) {
		t.Fatal("long-line truncation split a UTF-8 sequence")
	}
	if !strings.HasSuffix(preview.RawContent, "\n") || utf8.RuneCountInString(strings.TrimSuffix(preview.RawContent, "\n")) != maxReadLineChars-3 {
		t.Fatalf("raw long-line preview was not bounded to the source prefix: %d runes", utf8.RuneCountInString(strings.TrimSuffix(preview.RawContent, "\n")))
	}

	const metadataLines = maxReportedTruncatedLines + 10
	content := strings.Repeat(strings.Repeat("x", maxReadLineChars+1)+"\n", metadataLines)
	metadata, err := formatLineNumberReadPreviewRangeWithBudget(content, readRangeRequest{StartLine: 1, EndLine: metadataLines}, 2*1024*1024)
	if err != nil {
		t.Fatal(err)
	}
	if len(metadata.TruncatedLines) != maxReportedTruncatedLines || !metadata.TruncatedLinesOmitted || !metadata.Truncated {
		t.Fatalf("expected bounded truncated-line metadata, got %d entries omitted=%v truncated=%v", len(metadata.TruncatedLines), metadata.TruncatedLinesOmitted, metadata.Truncated)
	}
}

func TestBatchReadSilentlyFiltersMissingPathsAndDirectories(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "valid.txt"), []byte("ok\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"folder"} {
		if err := os.Mkdir(filepath.Join(dir, name), 0o700); err != nil {
			t.Fatal(err)
		}
	}

	app := NewApp()
	result, err := app.batchReadFilesWithConfig(ConfigState{Workspace: dir}, BatchReadRequest{
		Files: []BatchReadFileRequest{
			{Path: "missing.txt"},
			{Path: "folder"},
			{Path: "valid.txt"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Files) != 1 || result.Files[0].Path != "valid.txt" {
		t.Fatalf("expected only the readable file, got %#v", result)
	}

	empty, err := app.batchReadFilesWithConfig(ConfigState{Workspace: dir}, BatchReadRequest{
		Files: []BatchReadFileRequest{{Path: "missing.txt"}, {Path: "folder"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(empty.Files) != 0 {
		t.Fatalf("expected ignored-only batch to return no entries, got %#v", empty)
	}
}

func TestBatchReadKeepsMeaningfulReadErrors(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "binary.dat"), []byte{'a', 0, 'b'}, 0o600); err != nil {
		t.Fatal(err)
	}

	app := NewApp()
	result, err := app.batchReadFilesWithConfig(ConfigState{Workspace: dir}, BatchReadRequest{
		Files: []BatchReadFileRequest{{Path: "missing.txt"}, {Path: "binary.dat"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Files) != 1 || result.Files[0].Path != "binary.dat" || result.Files[0].ErrorCode != "E_BINARY_FILE" {
		t.Fatalf("expected meaningful read error to remain visible, got %#v", result)
	}
}

func TestEditUsesExactStringReplacementAndExpectedSHA(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "sample.txt"), []byte("alpha\nbeta\ngamma\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	app := NewApp()
	cfg := ConfigState{Workspace: dir}

	read, err := app.readFileWithConfig(cfg, ReadFileRequest{Path: "sample.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(read.Content, "2: beta") {
		t.Fatalf("expected line-numbered read content, got %q", read.Content)
	}
	if read.ContentFormat != "line_numbers" {
		t.Fatalf("expected line_numbers content format, got %q", read.ContentFormat)
	}

	result, err := app.editWithConfig(cfg, EditRequest{
		Path:           "sample.txt",
		ExpectedSHA256: read.SHA256,
		OldString:      "beta\n",
		NewString:      "beta\ninserted one\ninserted two\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.AddedLines != 2 || result.RemovedLines != 0 {
		t.Fatalf("expected +2 -0 stats, got +%d -%d", result.AddedLines, result.RemovedLines)
	}
	got, err := os.ReadFile(filepath.Join(dir, "sample.txt"))
	if err != nil {
		t.Fatal(err)
	}
	want := "alpha\nbeta\ninserted one\ninserted two\ngamma\n"
	if string(got) != want {
		t.Fatalf("unexpected file content:\nwant %q\ngot  %q", want, string(got))
	}
}

func TestEditAppliesMultipleReplacementsInOneCall(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "sample.txt"), []byte("alpha\nbeta\ngamma\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	app := NewApp()
	cfg := ConfigState{Workspace: dir}

	result, err := app.editWithConfig(cfg, EditRequest{
		Path: "sample.txt",
		Edits: []EditOperation{
			{OldString: "alpha", NewString: "ALPHA"},
			{OldString: "gamma", NewString: "GAMMA"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Replacements != 2 {
		t.Fatalf("expected two replacements, got %d", result.Replacements)
	}
	got, err := os.ReadFile(filepath.Join(dir, "sample.txt"))
	if err != nil {
		t.Fatal(err)
	}
	want := "ALPHA\nbeta\nGAMMA\n"
	if string(got) != want {
		t.Fatalf("unexpected file content:\nwant %q\ngot  %q", want, string(got))
	}
}

func TestExecuteToolRejectsLegacyStringEditFields(t *testing.T) {
	dir := t.TempDir()
	original := []byte("alpha\nbeta\ngamma\n")
	if err := os.WriteFile(filepath.Join(dir, "sample.txt"), original, 0o600); err != nil {
		t.Fatal(err)
	}
	app := NewApp()
	cfg := ConfigState{Workspace: dir}

	result := app.executeTool(context.Background(), cfg, "session-1", "edit", []byte(`{
		"files": [{"path": "sample.txt", "version": "000000000000",
		"edits": [
			{"oldString": "alpha", "newString": "ALPHA"},
			{"oldString": "gamma", "newString": "GAMMA"}
		]}]
	}`))
	if result.OK || !strings.Contains(result.Error, "E_BAD_VERSION") {
		t.Fatalf("expected model-facing edit tool to reject legacy edits array, got %#v", result)
	}
	got, err := os.ReadFile(filepath.Join(dir, "sample.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(original) {
		t.Fatalf("expected rejected edit to leave file unchanged, got %q", string(got))
	}
}

func TestExecuteToolUnknownArgumentsWarnInsteadOfFailing(t *testing.T) {
	dir := t.TempDir()
	original := []byte("alpha\nbeta\ngamma\n")
	if err := os.WriteFile(filepath.Join(dir, "sample.txt"), original, 0o600); err != nil {
		t.Fatal(err)
	}
	app := NewApp()
	cfg := ConfigState{Workspace: dir}

	// Unknown top-level keys are tolerated with an envelope warning while the
	// known arguments still execute normally.
	result := app.executeTool(context.Background(), cfg, "session-1", "list_files", []byte(`{"path":".","recursiveTypo":true}`))
	if !result.OK {
		t.Fatalf("unknown arguments must not fail the call, got %#v", result)
	}
	if len(result.Warnings) != 1 || !strings.Contains(result.Warnings[0], "recursiveTypo") {
		t.Fatalf("expected a warning naming the ignored key, got %#v", result.Warnings)
	}

	// The warning must survive model-context compaction for tools with a
	// rebuilt compact payload (edit), not only on the full envelope.
	readResult := app.executeTool(context.Background(), cfg, "session-1", "read", []byte(`{"files":[{"path":"sample.txt"}]}`))
	if !readResult.OK {
		t.Fatalf("read failed: %#v", readResult)
	}
	type fileEntry struct {
		Version string `json:"version"`
	}
	var batch struct {
		Files []fileEntry `json:"files"`
	}
	raw, _ := json.Marshal(readResult.Data)
	if err := json.Unmarshal(raw, &batch); err != nil || len(batch.Files) != 1 {
		t.Fatalf("unexpected read result: %s err=%v", raw, err)
	}
	ver := batch.Files[0].Version
	// Unknown keys are only detected at the top level of the arguments
	// object; nested unknown keys fall through to parameter validation.
	editArgs := fmt.Sprintf(`{"noteTypo":"x","files":[{"path":"sample.txt","version":"%s","changes":[{"oldText":"alpha","newText":"ALPHA"}]}]}`, ver)
	fullJSON, _ := json.Marshal(app.executeTool(context.Background(), cfg, "session-1", "edit", []byte(editArgs)))
	var envelope toolResult
	if err := json.Unmarshal(fullJSON, &envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope.Warnings) == 0 {
		t.Fatalf("expected unknown-argument warning on edit envelope, got %s", fullJSON)
	}
	compact := compactToolResultForModel("edit", envelope, string(fullJSON))
	if !strings.Contains(compact, "noteTypo") {
		t.Fatalf("compact model payload dropped the unknown-argument warning: %s", compact)
	}

	// Missing required parameters still fail loudly — tolerance never applies
	// to schema-required fields.
	missing := app.executeTool(context.Background(), cfg, "session-1", "wait", []byte(`{"seconds":1,"whyTypo":"x"}`))
	if missing.OK || missing.ErrorCode != "E_BAD_WAIT" {
		t.Fatalf("missing required reason must still fail, got %#v", missing)
	}
}

func TestExecuteToolAppliesMultipleBatchTextChanges(t *testing.T) {
	dir := t.TempDir()
	original := []byte("alpha\nbeta\ngamma\n")
	if err := os.WriteFile(filepath.Join(dir, "sample.txt"), original, 0o600); err != nil {
		t.Fatal(err)
	}
	app := NewApp()
	cfg := ConfigState{Workspace: dir}

	result := app.executeTool(context.Background(), cfg, "session-1", "edit", []byte(fmt.Sprintf(`{
		"files": [{"path": "sample.txt",
		"version": %q,
		"changes": [
			{"oldText": "alpha", "newText": "ALPHA"},
			{"oldText": "gamma", "newText": "GAMMA"}
		]}]
	}`, strings.ToUpper(hashVersion(original)))))
	if !result.OK {
		t.Fatalf("expected batch edit to succeed, got error %q", result.Error)
	}
	got, err := os.ReadFile(filepath.Join(dir, "sample.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "ALPHA\nbeta\nGAMMA\n" {
		t.Fatalf("unexpected file content: %q", string(got))
	}
}

func TestExecuteToolAppliesOriginalSnapshotLineRangesWithoutOffsetDrift(t *testing.T) {
	dir := t.TempDir()
	original := []byte("one\ntwo\nthree\nfour\nfive\nsix\n")
	if err := os.WriteFile(filepath.Join(dir, "sample.txt"), original, 0o600); err != nil {
		t.Fatal(err)
	}
	args := fmt.Sprintf(`{"files":[{"path":"sample.txt","version":%q,"changes":[{"lineRange":"2-2","newText":"TWO\ninserted"},{"lineRange":"5-5","newText":"FIVE"}]}]}`, hashVersion(original))
	result := NewApp().executeTool(context.Background(), ConfigState{Workspace: dir}, "session-1", "edit", []byte(args))
	if !result.OK {
		t.Fatalf("line-range edit failed: %#v", result)
	}
	got, err := os.ReadFile(filepath.Join(dir, "sample.txt"))
	if err != nil {
		t.Fatal(err)
	}
	want := "one\nTWO\ninserted\nthree\nfour\nFIVE\nsix\n"
	if string(got) != want {
		t.Fatalf("later line range must retain original snapshot position:\nwant %q\n got %q", want, got)
	}
}

func TestExecuteToolRejectsMixedEditSources(t *testing.T) {
	dir := t.TempDir()
	original := []byte("alpha\nbeta\n")
	if err := os.WriteFile(filepath.Join(dir, "sample.txt"), original, 0o600); err != nil {
		t.Fatal(err)
	}
	args := fmt.Sprintf(`{"files":[{"path":"sample.txt","version":%q,"changes":[{"oldText":"alpha","lineRange":"1-1","newText":"ALPHA"}]}]}`, hashVersion(original))
	result := NewApp().executeTool(context.Background(), ConfigState{Workspace: dir}, "session-1", "edit", []byte(args))
	if result.OK || result.ErrorCode != "E_BAD_EDIT" {
		t.Fatalf("mixed sources must fail with E_BAD_EDIT, got %#v", result)
	}
	got, err := os.ReadFile(filepath.Join(dir, "sample.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(original) {
		t.Fatalf("rejected mixed source edit changed file: %q", got)
	}
}

func TestExecuteToolEditsMultipleFilesInOneCall(t *testing.T) {
	dir := t.TempDir()
	a, b := []byte("alpha\n"), []byte("beta\n")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), a, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.txt"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	args := fmt.Sprintf(`{"files":[{"path":"a.txt","version":%q,"changes":[{"oldText":"alpha","newText":"ALPHA"}]},{"path":"b.txt","version":%q,"changes":[{"oldText":"beta","newText":"BETA"}]}]}`, hashVersion(a), hashVersion(b))
	result := NewApp().executeTool(context.Background(), ConfigState{Workspace: dir}, "session-1", "edit", []byte(args))
	if !result.OK {
		t.Fatalf("cross-file edit failed: %#v", result)
	}
	for path, want := range map[string]string{"a.txt": "ALPHA\n", "b.txt": "BETA\n"} {
		got, err := os.ReadFile(filepath.Join(dir, path))
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != want {
			t.Fatalf("%s = %q, want %q", path, got, want)
		}
	}
}

func TestExecuteToolEditRollsBackEarlierFilesWhenCommitFails(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	a, b := []byte("alpha\n"), []byte("beta\n")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), a, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "b.txt"), b, 0o600); err != nil {
		t.Fatal(err)
	}

	// Make b's commit write fail deterministically while reads keep
	// succeeding, so the batch prepares both files, commits a.txt, then fails
	// on b.txt and must roll a.txt back:
	//   - Windows: the read-only attribute makes MoveFileEx refuse to replace
	//     the destination (temp sibling + rename write path).
	//   - POSIX: a read-only parent directory makes the temp sibling creation
	//     or rename fail with EACCES.
	if runtime.GOOS == "windows" {
		if err := os.Chmod(filepath.Join(sub, "b.txt"), 0o444); err != nil {
			t.Fatal(err)
		}
	} else {
		if err := os.Chmod(sub, 0o555); err != nil {
			t.Fatal(err)
		}
	}
	defer func() {
		if runtime.GOOS == "windows" {
			_ = os.Chmod(filepath.Join(sub, "b.txt"), 0o600)
		} else {
			_ = os.Chmod(sub, 0o755)
		}
	}()

	args := fmt.Sprintf(`{"files":[{"path":"a.txt","version":%q,"changes":[{"oldText":"alpha","newText":"ALPHA"}]},{"path":"sub/b.txt","version":%q,"changes":[{"oldText":"beta","newText":"BETA"}]}]}`, hashVersion(a), hashVersion(b))
	result := NewApp().executeTool(context.Background(), ConfigState{Workspace: dir}, "session-1", "edit", []byte(args))
	if result.OK {
		t.Fatalf("expected commit of b.txt to fail, got success: %#v", result)
	}
	if result.ErrorCode != "E_EDIT_COMMIT" {
		t.Fatalf("expected E_EDIT_COMMIT, got %q (%v)", result.ErrorCode, result.Error)
	}

	gotA, err := os.ReadFile(filepath.Join(dir, "a.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(gotA) != "alpha\n" {
		t.Fatalf("a.txt must be rolled back to its original content after b.txt commit failed, got %q", string(gotA))
	}
	gotB, err := os.ReadFile(filepath.Join(sub, "b.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(gotB) != "beta\n" {
		t.Fatalf("b.txt must remain unchanged, got %q", string(gotB))
	}
}

func TestExecuteToolEditMergesRepeatedPathEntries(t *testing.T) {
	dir := t.TempDir()
	original := []byte("alpha\nbeta\ngamma\n")
	if err := os.WriteFile(filepath.Join(dir, "sample.txt"), original, 0o600); err != nil {
		t.Fatal(err)
	}
	version := hashVersion(original)
	args := fmt.Sprintf(`{"files":[{"path":"sample.txt","version":%q,"changes":[{"oldText":"alpha","newText":"ALPHA"}]},{"path":"./sample.txt","version":%q,"changes":[{"oldText":"gamma","newText":"GAMMA"}]}]}`, version, version)
	result := NewApp().executeTool(context.Background(), ConfigState{Workspace: dir}, "session-1", "edit", []byte(args))
	if !result.OK {
		t.Fatalf("expected repeated path entries to merge, got %#v", result)
	}
	got, err := os.ReadFile(filepath.Join(dir, "sample.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "ALPHA\nbeta\nGAMMA\n" {
		t.Fatalf("unexpected merged edit content %q", string(got))
	}
	edited, ok := result.Data.(MultiEditResult)
	if !ok || edited.FileCount != 1 || len(edited.Files) != 1 || edited.Replacements != 2 {
		t.Fatalf("expected one merged file with two replacements, got %#v", result.Data)
	}
}

func TestExecuteToolEditRejectsRepeatedPathEntriesWithDifferentVersions(t *testing.T) {
	dir := t.TempDir()
	original := []byte("alpha\nbeta\n")
	if err := os.WriteFile(filepath.Join(dir, "sample.txt"), original, 0o600); err != nil {
		t.Fatal(err)
	}
	args := fmt.Sprintf(`{"files":[{"path":"sample.txt","version":%q,"changes":[{"oldText":"alpha","newText":"ALPHA"}]},{"path":"./sample.txt","version":%q,"changes":[{"oldText":"beta","newText":"BETA"}]}]}`, hashVersion(original), "zzzzzz")
	result := NewApp().executeTool(context.Background(), ConfigState{Workspace: dir}, "session-1", "edit", []byte(args))
	if result.OK || result.ErrorCode != "E_VERSION_MISMATCH" {
		t.Fatalf("expected inconsistent duplicate versions to fail with E_VERSION_MISMATCH, got %#v", result)
	}
	got, err := os.ReadFile(filepath.Join(dir, "sample.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(original) {
		t.Fatalf("version conflict must leave file unchanged, got %q", got)
	}
}

func TestExecuteToolEditMergesRepeatedPathEntriesBeforeMatching(t *testing.T) {
	dir := t.TempDir()
	original := []byte("foo foo\n")
	if err := os.WriteFile(filepath.Join(dir, "sample.txt"), original, 0o600); err != nil {
		t.Fatal(err)
	}
	version := hashVersion(original)
	args := fmt.Sprintf(`{"files":[{"path":"sample.txt","version":%q,"changes":[{"oldText":"foo","newText":"one"}]},{"path":"./sample.txt","version":%q,"changes":[{"oldText":"foo","newText":"two"}]}]}`, version, version)
	result := NewApp().executeTool(context.Background(), ConfigState{Workspace: dir}, "session-1", "edit", []byte(args))
	if result.OK || result.ErrorCode != "E_MULTI_MATCH" {
		t.Fatalf("expected merged changes to use one original snapshot and reject ambiguous oldText, got %#v", result)
	}
	got, err := os.ReadFile(filepath.Join(dir, "sample.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(original) {
		t.Fatalf("ambiguous merged edit must leave file unchanged, got %q", got)
	}
}

func TestExecuteToolRequiresVersionForEdit(t *testing.T) {
	dir := t.TempDir()
	original := []byte("alpha\nbeta\n")
	if err := os.WriteFile(filepath.Join(dir, "sample.txt"), original, 0o600); err != nil {
		t.Fatal(err)
	}
	app := NewApp()
	result := app.executeTool(context.Background(), ConfigState{Workspace: dir}, "session-1", "edit", []byte(`{
		"files": [{"path": "sample.txt", "changes": [{"oldText": "beta", "newText": "BETA"}]}]
	}`))
	if result.OK || result.ErrorCode != "E_VERSION_REQUIRED" {
		t.Fatalf("expected E_VERSION_REQUIRED, got %#v", result)
	}
	got, err := os.ReadFile(filepath.Join(dir, "sample.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(original) {
		t.Fatalf("expected missing-version edit to leave file unchanged, got %q", string(got))
	}
}

func TestExecuteToolEditHandlesUniqueSingleLineSubstring(t *testing.T) {
	dir := t.TempDir()
	original := []byte(`{"enabled":false,"name":"ally"}`)
	if err := os.WriteFile(filepath.Join(dir, "config.json"), original, 0o600); err != nil {
		t.Fatal(err)
	}
	app := NewApp()
	result := app.executeTool(context.Background(), ConfigState{Workspace: dir}, "session-1", "edit", []byte(fmt.Sprintf(`{
		"files": [{"path": "config.json",
		"version": %q,
		"changes": [{"oldText": "false", "newText": "true"}]}]
	}`, hashVersion(original))))
	if !result.OK {
		t.Fatalf("expected edit to succeed, got %#v", result)
	}
	got, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"enabled":true,"name":"ally"}` {
		t.Fatalf("unexpected edit content %q", string(got))
	}
}

func TestExecuteToolEditRejectsMultipleMatches(t *testing.T) {
	dir := t.TempDir()
	original := []byte("foo foo")
	if err := os.WriteFile(filepath.Join(dir, "sample.txt"), original, 0o600); err != nil {
		t.Fatal(err)
	}
	app := NewApp()
	result := app.executeTool(context.Background(), ConfigState{Workspace: dir}, "session-1", "edit", []byte(fmt.Sprintf(`{
		"files": [{"path": "sample.txt",
		"version": %q,
		"changes": [{"oldText": "foo", "newText": "bar"}]}]
	}`, hashVersion(original))))
	if result.OK || !strings.Contains(result.Error, "[E_MULTI_MATCH]") {
		t.Fatalf("expected edit multi-match failure, got %#v", result)
	}

	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"details"`) || !strings.Contains(string(raw), `"matchCount":2`) {
		t.Fatalf("expected structured match diagnostics in tool envelope, got %s", raw)
	}
	if len(raw) > 8*1024 {
		t.Fatalf("edit error envelope exceeded bounded budget: %d bytes", len(raw))
	}
	got, err := os.ReadFile(filepath.Join(dir, "sample.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(original) {
		t.Fatalf("ambiguous edit must not modify the file, got %q", got)
	}
}

func TestExecuteToolEditReplacesAllMatchesWhenRequested(t *testing.T) {
	dir := t.TempDir()
	original := []byte("foo foo\nfoo\n")
	if err := os.WriteFile(filepath.Join(dir, "sample.txt"), original, 0o600); err != nil {
		t.Fatal(err)
	}
	result := NewApp().executeTool(context.Background(), ConfigState{Workspace: dir}, "session-1", "edit", []byte(fmt.Sprintf(`{
		"files": [{"path": "sample.txt", "version": %q,
		"changes": [{"oldText": "foo", "newText": "bar", "replace_all": true}]}]
	}`, hashVersion(original))))
	if !result.OK {
		t.Fatalf("replace_all edit failed: %#v", result)
	}
	got, err := os.ReadFile(filepath.Join(dir, "sample.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "bar bar\nbar\n" {
		t.Fatalf("unexpected replace_all content: %q", got)
	}
	edited, ok := result.Data.(MultiEditResult)
	if !ok || edited.Replacements != 3 {
		t.Fatalf("expected three replacements, got %#v", result.Data)
	}
}

func TestExecuteToolEditRejectsOverlappingChangesInBatch(t *testing.T) {
	dir := t.TempDir()
	original := []byte("abcdef")
	if err := os.WriteFile(filepath.Join(dir, "sample.txt"), original, 0o600); err != nil {
		t.Fatal(err)
	}
	app := NewApp()
	result := app.executeTool(context.Background(), ConfigState{Workspace: dir}, "session-1", "edit", []byte(fmt.Sprintf(`{
		"files": [{"path": "sample.txt",
		"version": %q,
		"changes": [
			{"oldText": "abc", "newText": "ABC"},
			{"oldText": "bc", "newText": "BC"}
		]}]
	}`, hashVersion(original))))
	if result.OK || result.ErrorCode != "E_OVERLAPPING_CHANGES" {
		t.Fatalf("expected overlap failure, got %#v", result)
	}
	got, err := os.ReadFile(filepath.Join(dir, "sample.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(original) {
		t.Fatalf("overlapping edit must be all-or-nothing, got %q", string(got))
	}
}

func TestEditLineRangeDeletesWholeLines(t *testing.T) {
	dir := t.TempDir()
	original := "alpha\nbeta\ngamma\ndelta\n"
	if err := os.WriteFile(filepath.Join(dir, "sample.txt"), []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	app := NewApp()
	cfg := ConfigState{Workspace: dir}
	newText := ""

	result, err := app.editWithConfig(cfg, EditRequest{
		Path:      "sample.txt",
		StartLine: 2,
		EndLine:   3,
		NewText:   &newText,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.RemovedLines == 0 {
		t.Fatalf("expected deletion stats, got %#v", result)
	}
	got, err := os.ReadFile(filepath.Join(dir, "sample.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "alpha\ndelta\n" {
		t.Fatalf("expected whole lines to be deleted without blank lines, got %q", string(got))
	}
}

func TestEditRejectsMixedLineAndExactForms(t *testing.T) {
	app := NewApp()
	newText := "ALPHA"
	_, err := app.editWithConfig(ConfigState{Workspace: t.TempDir()}, EditRequest{
		Path:      "sample.txt",
		OldString: "alpha",
		NewString: "ALPHA",
		StartLine: 1,
		NewText:   &newText,
	})
	if err == nil || !strings.Contains(err.Error(), "[E_BAD_EDIT]") {
		t.Fatalf("expected mixed edit form validation error, got %v", err)
	}
}

func TestEditLineRangeRequiresExplicitNewText(t *testing.T) {
	app := NewApp()
	_, err := app.editWithConfig(ConfigState{Workspace: t.TempDir()}, EditRequest{
		Path:      "sample.txt",
		StartLine: 1,
	})
	if err == nil || !strings.Contains(err.Error(), "requires \"newText\"") {
		t.Fatalf("expected missing newText validation error, got %v", err)
	}
}

func TestEditMultipleReplacementsAreAllOrNothingOnFailure(t *testing.T) {
	dir := t.TempDir()
	original := "alpha\nbeta\ngamma\n"
	if err := os.WriteFile(filepath.Join(dir, "sample.txt"), []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	app := NewApp()
	cfg := ConfigState{Workspace: dir}

	_, err := app.editWithConfig(cfg, EditRequest{
		Path: "sample.txt",
		Edits: []EditOperation{
			{OldString: "alpha", NewString: "ALPHA"},
			{OldString: "missing", NewString: "MISSING"},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "edit 2/2 failed") || !strings.Contains(err.Error(), "[E_NO_MATCH]") {
		t.Fatalf("expected second edit no-match error, got %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "sample.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != original {
		t.Fatalf("expected failed multi-edit to leave file unchanged:\nwant %q\ngot  %q", original, string(got))
	}
}

func TestEditLargeFileReturnsLocalizedDiff(t *testing.T) {
	dir := t.TempDir()
	var original strings.Builder
	for i := 1; i <= 600; i++ {
		original.WriteString(fmt.Sprintf("line-%04d\n", i))
	}
	if err := os.WriteFile(filepath.Join(dir, "large.txt"), []byte(original.String()), 0o600); err != nil {
		t.Fatal(err)
	}

	app := NewApp()
	cfg := ConfigState{Workspace: dir}
	read, err := app.readFileWithConfig(cfg, ReadFileRequest{Path: "large.txt", StartLine: 300, LineCount: 1})
	if err != nil {
		t.Fatal(err)
	}

	result, err := app.editWithConfig(cfg, EditRequest{
		Path:           "large.txt",
		ExpectedSHA256: read.SHA256,
		OldString:      "line-0300",
		NewString:      "line-0300 updated",
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(result.Diff, "[diff omitted:") {
		t.Fatalf("expected localized diff, got omitted marker:\n%s", result.Diff)
	}
	if !strings.Contains(result.Diff, "@@ -297,7 +297,7 @@") ||
		!strings.Contains(result.Diff, "-line-0300") ||
		!strings.Contains(result.Diff, "+line-0300 updated") {
		t.Fatalf("expected localized hunk around changed line, got:\n%s", result.Diff)
	}
	if strings.Contains(result.Diff, "line-0001") || strings.Contains(result.Diff, "line-0600") {
		t.Fatalf("expected diff to omit distant context, got:\n%s", result.Diff)
	}
}

func TestEditRejectsExpectedSHAMismatch(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "sample.txt"), []byte("alpha\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	app := NewApp()
	_, err := app.editWithConfig(ConfigState{Workspace: dir}, EditRequest{
		Path:           "sample.txt",
		ExpectedSHA256: "not-current",
		OldString:      "alpha",
		NewString:      "beta",
	})
	if err == nil || !strings.Contains(err.Error(), "[E_VERSION_MISMATCH]") {
		t.Fatalf("expected version mismatch error, got %v", err)
	}
}

func TestEditRejectsAmbiguousStringUnlessReplaceAll(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "sample.txt"), []byte("foo\nfoo\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	app := NewApp()
	cfg := ConfigState{Workspace: dir}

	_, err := app.editWithConfig(cfg, EditRequest{
		Path:      "sample.txt",
		OldString: "foo",
		NewString: "bar",
	})
	if err == nil || !strings.Contains(err.Error(), "[E_MULTI_MATCH]") {
		t.Fatalf("expected multi-match error, got %v", err)
	}

	result, err := app.editWithConfig(cfg, EditRequest{
		Path:       "sample.txt",
		OldString:  "foo",
		NewString:  "bar",
		ReplaceAll: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Replacements != 2 {
		t.Fatalf("expected two replacements, got %d", result.Replacements)
	}
	got, err := os.ReadFile(filepath.Join(dir, "sample.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "bar\nbar\n" {
		t.Fatalf("unexpected file content: %q", string(got))
	}
}

func TestWindowsDeleteSafetyAllowsOrdinaryCDriveWorkspacePaths(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("windows path safety")
	}

	allowed, reason := isDangerousDeletePath(`C:\Users\alice\project\temp.txt`)
	if allowed {
		t.Fatalf("expected ordinary C: workspace path to be allowed, got %q", reason)
	}

	for _, path := range []string{
		`C:\`,
		`C:\Windows\System32\kernel32.dll`,
		`C:\Program Files\App`,
		`C:\Users\alice`,
		`C:\Users\alice\project\.git\config`,
	} {
		blocked, reason := isDangerousDeletePath(path)
		if !blocked {
			t.Fatalf("expected %s to be blocked", path)
		}
		if strings.TrimSpace(reason) == "" {
			t.Fatalf("expected block reason for %s", path)
		}
	}
}

func TestReplaceLinesUsesCurrentFileContent(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "sample.txt"), []byte("alpha\nbeta\ngamma\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	app := NewApp()
	app.initialized = true
	app.config = ConfigState{Workspace: dir}

	result, err := app.ReplaceLines(ReplaceLinesRequest{
		Path:      "sample.txt",
		StartLine: 2,
		EndLine:   2,
		NewText:   "BETA",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.AddedLines != 1 || result.RemovedLines != 1 {
		t.Fatalf("expected replacement stats +1 -1, got +%d -%d", result.AddedLines, result.RemovedLines)
	}
	got, err := os.ReadFile(filepath.Join(dir, "sample.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "alpha\nBETA\ngamma\n" {
		t.Fatalf("unexpected file content: %q", string(got))
	}
}

func TestGetGitDiffUsesRepositoryRootForSubdirectoryWorkspace(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}

	dir := t.TempDir()
	subdir := filepath.Join(dir, "sub")
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(subdir, "file.txt"), []byte("alpha\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	runGitTestCommand(t, dir, "init")
	runGitTestCommand(t, dir, "config", "user.email", "ally-test@example.com")
	runGitTestCommand(t, dir, "config", "user.name", "Ally Test")
	runGitTestCommand(t, dir, "add", ".")
	runGitTestCommand(t, dir, "commit", "-m", "init")

	if err := os.WriteFile(filepath.Join(subdir, "file.txt"), []byte("beta\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	app := NewApp()
	app.initialized = true
	app.config = ConfigState{Workspace: subdir}

	got := app.GetGitDiff()
	if !got.IsRepo {
		t.Fatalf("expected subdirectory workspace to be recognized as repo, error=%q", got.Error)
	}

	var file *GitDiffFile
	for i := range got.Files {
		if got.Files[i].Path == "sub/file.txt" {
			file = &got.Files[i]
			break
		}
	}
	if file == nil {
		t.Fatalf("expected sub/file.txt in diff result, got %#v", got.Files)
	}
	if !strings.Contains(file.Diff, "-alpha") || !strings.Contains(file.Diff, "+beta") {
		t.Fatalf("expected file diff content for subdirectory workspace, got:\n%s", file.Diff)
	}
}

func runGitTestCommand(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, out)
	}
}

// TestNormalizeToolArgsForDedup verifies that the dedup canonicalization is
// invariant under field reordering and whitespace differences, while still
// distinguishing genuinely different argument values and preserving array
// order (e.g. edit.files, ask.questions).
func TestNormalizeToolArgsForDedup(t *testing.T) {
	cases := []struct {
		name string
		a, b string
		want bool // true if a and b should dedup to the same key
	}{
		{
			name: "field order",
			a:    `{"url":"https://x","method":"GET"}`,
			b:    `{"method":"GET","url":"https://x"}`,
			want: true,
		},
		{
			name: "whitespace only",
			a:    `{"url": "https://x", "method": "GET"}`,
			b:    `{"url":"https://x","method":"GET"}`,
			want: true,
		},
		{
			name: "different url",
			a:    `{"url":"https://x"}`,
			b:    `{"url":"https://y"}`,
			want: false,
		},
		{
			name: "different method",
			a:    `{"url":"https://x","method":"GET"}`,
			b:    `{"url":"https://x","method":"POST"}`,
			want: false,
		},
		{
			name: "default value present vs absent stays distinct",
			a:    `{"url":"https://x"}`,
			b:    `{"url":"https://x","method":"GET"}`,
			want: false,
		},
		{
			name: "array order preserved",
			a:    `{"items":["a","b"]}`,
			b:    `{"items":["b","a"]}`,
			want: false,
		},
		{
			name: "nested object order",
			a:    `{"query":{"a":"1","b":"2"}}`,
			b:    `{"query":{"b":"2","a":"1"}}`,
			want: true,
		},
		{
			name: "empty strings",
			a:    ``,
			b:    ``,
			want: true,
		},
		{
			name: "invalid json falls back to raw",
			a:    `not-json`,
			b:    `not-json`,
			want: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ka := normalizeToolArgsForDedup(tc.a)
			kb := normalizeToolArgsForDedup(tc.b)
			got := ka == kb
			if got != tc.want {
				t.Fatalf("normalize(%q)=%q normalize(%q)=%q; want equal=%v got equal=%v",
					tc.a, ka, tc.b, kb, tc.want, got)
			}
		})
	}
}

// TestDetectToolBatchConflictsDedupNormalized verifies that batch-level dedup
// now treats field-reordered and whitespace-different JSON arguments as
// duplicates, while still distinguishing genuinely different calls.
func TestDetectToolBatchConflictsDedupNormalized(t *testing.T) {
	cfg := ConfigState{Workspace: "/ws"}
	mk := func(id, args string) openai.ToolCall {
		return openai.ToolCall{
			ID:   id,
			Type: openai.ToolTypeFunction,
			Function: openai.FunctionCall{
				Name:      "http_request",
				Arguments: args,
			},
		}
	}

	t.Run("field reorder dedups", func(t *testing.T) {
		calls := []openai.ToolCall{
			mk("call_a", `{"url":"https://api.example.com","method":"GET"}`),
			mk("call_b", `{"method":"GET","url":"https://api.example.com"}`),
		}
		conflicts := detectToolBatchConflicts(cfg, calls)
		if len(conflicts) != 1 {
			t.Fatalf("expected 1 conflict, got %d: %v", len(conflicts), conflicts)
		}
		err, ok := conflicts[1]
		if !ok {
			t.Fatalf("expected conflict at index 1")
		}
		if !strings.Contains(err.Error(), "E_DUPLICATE_TOOL_CALL") {
			t.Fatalf("expected E_DUPLICATE_TOOL_CALL, got %v", err)
		}
	})

	t.Run("whitespace difference dedups", func(t *testing.T) {
		calls := []openai.ToolCall{
			mk("call_a", `{"url": "https://api.example.com"}`),
			mk("call_b", `{"url":"https://api.example.com"}`),
		}
		conflicts := detectToolBatchConflicts(cfg, calls)
		if len(conflicts) != 1 {
			t.Fatalf("expected 1 conflict, got %d: %v", len(conflicts), conflicts)
		}
	})

	t.Run("different method stays distinct", func(t *testing.T) {
		calls := []openai.ToolCall{
			mk("call_a", `{"url":"https://api.example.com","method":"GET"}`),
			mk("call_b", `{"url":"https://api.example.com","method":"POST"}`),
		}
		conflicts := detectToolBatchConflicts(cfg, calls)
		if len(conflicts) != 0 {
			t.Fatalf("expected 0 conflicts, got %d: %v", len(conflicts), conflicts)
		}
	})
}

// TestLocalEditPlanIsSharedByConflictDetectionAndExecution verifies that
// planLocalEditBatch is the single normalization boundary consumed by both
// detectWriteBatchConflicts and the executor.
func TestLocalEditPlanIsSharedByConflictDetectionAndExecution(t *testing.T) {
	dir := t.TempDir()
	original := []byte("alpha\nbeta\n")
	if err := os.WriteFile(filepath.Join(dir, "sample.txt"), original, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := ConfigState{Workspace: dir}
	version := hashVersion(original)
	files := []FileTextEdits{
		{Path: "sample.txt", Version: version, Changes: []TextChange{{OldText: "alpha", NewText: "ALPHA"}}},
		{Path: "./sample.txt", Version: version, Changes: []TextChange{{OldText: "beta", NewText: "BETA"}}},
	}

	plan, err := planLocalEditBatch(cfg, files, localEditPlanForExecution)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Targets) != 1 || len(plan.Files) != 1 || len(plan.Files[0].Edit.Changes) != 2 {
		t.Fatalf("expected one merged physical target and two changes, got %#v", plan)
	}

	args, err := json.Marshal(ModelEditToolRequest{Files: files})
	if err != nil {
		t.Fatal(err)
	}
	calls := []openai.ToolCall{{Function: openai.FunctionCall{Name: "edit", Arguments: string(args)}}}
	if conflicts := detectWriteBatchConflicts(cfg, calls); len(conflicts) != 0 {
		t.Fatalf("merged local edit should not conflict with itself: %#v", conflicts)
	}
	targets := fileMutationTargets(cfg, "edit", string(args))
	if len(targets) != 1 || targets[0].key != plan.Targets[0].key {
		t.Fatalf("conflict detector and executor plan disagree: targets=%#v plan=%#v", targets, plan.Targets)
	}

	result := NewApp().executeTool(context.Background(), cfg, "session-1", "edit", args)
	if !result.OK {
		t.Fatalf("shared edit plan execution failed: %#v", result)
	}
	got, err := os.ReadFile(filepath.Join(dir, "sample.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "ALPHA\nBETA\n" {
		t.Fatalf("unexpected edited content %q", got)
	}
}

func TestSalvageEditRequestRecoversCompletePrefix(t *testing.T) {
	// Cut inside the third change's newText: two complete changes survive,
	// the truncated one is dropped and counted.
	req, dropped, ok := salvageEditRequest([]byte(`{"files":[{"path":"a.txt","version":"000000000000","changes":[{"oldText":"alpha","newText":"ALPHA"},{"oldText":"beta","newText":"BETA"},{"oldText":"gamma","newText":"GAM`))
	if !ok {
		t.Fatal("expected salvage to recover the complete prefix")
	}
	if dropped != 1 {
		t.Fatalf("expected 1 dropped change, got %d", dropped)
	}
	if len(req.Files) != 1 || req.Files[0].Path != "a.txt" || req.Files[0].Version != "000000000000" {
		t.Fatalf("unexpected salvaged file: %#v", req.Files)
	}
	if len(req.Files[0].Changes) != 2 || req.Files[0].Changes[0].NewText != "ALPHA" || req.Files[0].Changes[1].NewText != "BETA" {
		t.Fatalf("unexpected salvaged changes: %#v", req.Files[0].Changes)
	}

	// Cut inside a second file entry: the first file is complete, the second
	// contributes its complete changes with the truncated tail dropped.
	req, dropped, ok = salvageEditRequest([]byte(`{"files":[{"path":"a.txt","version":"v1","changes":[{"oldText":"x","newText":"y"}]},{"path":"b.txt","version":"v2","changes":[{"oldText":"1","newText":"2"},{"oldText":"3","newText":"4"},{"oldText":"5","newTe`))
	if !ok {
		t.Fatal("expected salvage to recover both files")
	}
	if len(req.Files) != 2 {
		t.Fatalf("expected 2 salvaged files, got %d: %#v", len(req.Files), req.Files)
	}
	if req.Files[1].Path != "b.txt" || len(req.Files[1].Changes) != 2 || dropped != 1 {
		t.Fatalf("unexpected partial file salvage: %#v dropped=%d", req.Files[1], dropped)
	}

	// Stream cut right after a complete change: nothing is lost.
	req, dropped, ok = salvageEditRequest([]byte(`{"files":[{"path":"a.txt","version":"v1","changes":[{"oldText":"x","newText":"y"}]`))
	if !ok || dropped != 0 || len(req.Files) != 1 || len(req.Files[0].Changes) != 1 {
		t.Fatalf("expected trailing truncation to keep the complete change: %#v dropped=%d ok=%v", req, dropped, ok)
	}

	// Nothing usable was transmitted before the cut.
	_, _, ok = salvageEditRequest([]byte(`{"files":[{"path":"a.txt","ver`))
	if ok {
		t.Fatal("salvage must not report usable output for a prefix without complete changes")
	}

	// Not truncated in the first place: a top-level non-object yields nothing.
	if _, _, ok := salvageEditRequest([]byte(`[]`)); ok {
		t.Fatal("non-object arguments must not salvage")
	}
}

func TestExecuteToolEditSalvagesTruncatedArguments(t *testing.T) {
	dir := t.TempDir()
	original := []byte("alpha\nbeta\ngamma\n")
	if err := os.WriteFile(filepath.Join(dir, "sample.txt"), original, 0o600); err != nil {
		t.Fatal(err)
	}
	app := NewApp()
	cfg := ConfigState{Workspace: dir}
	version := strings.ToUpper(hashVersion(original))

	// Stream cut inside the third change: the first two changes must be
	// applied, the third dropped, and the result must carry the salvage
	// warning instead of failing the call.
	truncated := fmt.Sprintf(`{"files":[{"path":"sample.txt","version":%q,"changes":[{"oldText":"alpha","newText":"ALPHA"},{"oldText":"beta","newText":"BETA"},{"oldText":"gamma","newText":"GAM`, version)
	result := app.executeTool(context.Background(), cfg, "session-1", "edit", []byte(truncated))
	if !result.OK {
		t.Fatalf("expected salvaged edit to succeed, got error %q", result.Error)
	}
	got, err := os.ReadFile(filepath.Join(dir, "sample.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "ALPHA\nBETA\ngamma\n" {
		t.Fatalf("unexpected salvaged content: %q", string(got))
	}
	var multi MultiEditResult
	data, _ := json.Marshal(result.Data)
	if err := json.Unmarshal(data, &multi); err != nil {
		t.Fatalf("decode MultiEditResult: %v", err)
	}
	if len(multi.Warnings) == 0 || !strings.Contains(multi.Warnings[len(multi.Warnings)-1], "截断") {
		t.Fatalf("expected salvage warning in result warnings, got %#v", multi.Warnings)
	}
	if !strings.Contains(multi.Warnings[len(multi.Warnings)-1], "1 个残缺改动") {
		t.Fatalf("salvage warning must report the dropped change count: %#v", multi.Warnings)
	}

	// Salvage that recovers nothing keeps the explicit truncation error.
	result = app.executeTool(context.Background(), cfg, "session-1", "edit", []byte(`{"files":[{"path":"sample.txt","ver`))
	if result.OK || !strings.Contains(result.Error, "truncated") {
		t.Fatalf("expected truncation error when nothing is salvageable, got %#v", result)
	}
}

func TestPrepareToolCallsForExecutionKeepsSalvageBytesAndSafeReplayJSON(t *testing.T) {
	raw := `{"files":[{"path":"sample.txt","version":"abc123","changes":[{"oldText":"alpha","newText":"ALPHA"},{"oldText":"beta","newText":"BET`
	prepared, executionArgs, salvaged := prepareToolCallsForExecution([]openai.ToolCall{{
		ID: "call_1", Type: openai.ToolTypeFunction,
		Function: openai.FunctionCall{Name: "edit", Arguments: raw},
	}})
	if len(prepared) != 1 || len(executionArgs) != 1 {
		t.Fatalf("unexpected prepared result: %#v %#v", prepared, executionArgs)
	}
	if executionArgs[0] != raw {
		t.Fatalf("execution must retain raw truncated bytes for salvage, got %q", executionArgs[0])
	}
	if !salvaged {
		t.Fatal("expected the complete edit prefix to be marked salvageable")
	}
	if !json.Valid([]byte(prepared[0].Function.Arguments)) || isTruncatedArgsMarker([]byte(prepared[0].Function.Arguments)) {
		t.Fatalf("provider replay must use recovered valid JSON, got %q", prepared[0].Function.Arguments)
	}
	var req ModelEditToolRequest
	if err := json.Unmarshal([]byte(prepared[0].Function.Arguments), &req); err != nil || len(req.Files) != 1 || len(req.Files[0].Changes) != 1 {
		t.Fatalf("unexpected recovered replay request: %#v err=%v", req, err)
	}

	prepared, executionArgs, salvaged = prepareToolCallsForExecution([]openai.ToolCall{{
		Function: openai.FunctionCall{Name: "edit", Arguments: `{"files":[{"path":"sample.txt","ver`},
	}})
	if !isTruncatedArgsMarker([]byte(prepared[0].Function.Arguments)) || !isTruncatedArgsMarker([]byte(executionArgs[0])) {
		t.Fatalf("unsalvageable calls must use the truncation marker on both paths: %#v %#v", prepared, executionArgs)
	}
	if salvaged {
		t.Fatal("an edit prefix without a complete change must not bypass max_tokens")
	}
}

func TestExecuteToolEditDoesNotMislabelSchemaErrorAsTruncation(t *testing.T) {
	app := NewApp()
	result := app.executeTool(context.Background(), ConfigState{Workspace: t.TempDir()}, "session-1", "edit", []byte(
		`{"files":[{"path":"sample.txt","version":"abc123","changes":[{"oldString":"alpha","newString":"ALPHA"}]}]}`,
	))
	if result.OK {
		t.Fatal("legacy unknown change fields must fail the model-facing edit schema")
	}
	if strings.Contains(strings.ToLower(result.Error), "truncated") {
		t.Fatalf("complete schema error must not be reported as truncation: %q", result.Error)
	}
	// Unknown keys inside a change are now tolerated with a warning, but the
	// change itself still fails validation: neither oldText nor lineRange was
	// provided, which must surface as the real E_BAD_EDIT error.
	if !strings.Contains(result.Error, "E_BAD_EDIT") {
		t.Fatalf("expected the real validation error, got %q", result.Error)
	}
}

// ─────────────────────── Scheduled tasks ───────────────────────

func TestScheduledTaskCreateListDelete(t *testing.T) {
	root := t.TempDir()
	app := NewApp()
	app.configPath = filepath.Join(root, "config.json")
	app.config = ConfigState{Workspace: root}
	if err := app.startScheduledTaskManager(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(app.stopScheduledTaskManager)

	created, err := app.executeScheduledTaskTool(app.config, ScheduledTaskToolRequest{
		Action:      "create",
		Name:        "hourly check",
		Instruction: "Inspect the workspace and report issues.",
		Schedule:    "1h",
	})
	if err != nil {
		t.Fatal(err)
	}
	createResult := created.(ScheduledTaskToolResult)
	if createResult.Task == nil || createResult.Task.ID == "" {
		t.Fatalf("expected created task id, got %#v", createResult)
	}
	if createResult.Task.MaxSteps != defaultScheduledTaskSteps || createResult.Task.TimeoutSeconds != defaultScheduledTaskTimeout {
		t.Fatalf("expected backend execution defaults, got %#v", createResult.Task)
	}

	listed, err := app.executeScheduledTaskTool(app.config, ScheduledTaskToolRequest{Action: "list"})
	if err != nil {
		t.Fatal(err)
	}
	listResult := listed.(ScheduledTaskToolResult)
	if listResult.Count != 1 || len(listResult.Tasks) != 1 {
		t.Fatalf("expected one scheduled task, got %#v", listResult)
	}

	if err := app.DeleteScheduledTask(createResult.Task.ID); err != nil {
		t.Fatal(err)
	}
	if tasks := app.ListScheduledTasks(); len(tasks) != 0 {
		t.Fatalf("expected task deletion, got %#v", tasks)
	}
}

func TestScheduledTasksClearLegacyPersistenceOnStartup(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "scheduled_tasks.json")
	if err := os.WriteFile(path, []byte(`[{"id":"legacy"}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	app := NewApp()
	app.configPath = filepath.Join(root, "config.json")
	app.config = ConfigState{Workspace: root}
	if err := app.startScheduledTaskManager(); err != nil {
		t.Fatal(err)
	}
	app.stopScheduledTaskManager()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("scheduled task persistence should be removed, stat err=%v", err)
	}
}

func TestScheduledTaskRejectsTooFrequentInterval(t *testing.T) {
	task := ScheduledTask{
		ID: "task_test", Name: "too frequent", Instruction: "check", Workspace: t.TempDir(),
		Schedule: ScheduledTaskSchedule{Type: "interval", Every: "30s"},
	}
	if err := normalizeScheduledTask(&task, time.Now()); err == nil {
		t.Fatal("expected intervals shorter than one minute to be rejected")
	}
}

func TestScheduledTaskToolsIncludeNormalCommandsAndExcludeScheduler(t *testing.T) {
	app := NewApp()
	tools := app.scheduledTaskTools(ConfigState{})
	foundCommand := false
	for _, tool := range tools {
		if tool.Function == nil {
			continue
		}
		if tool.Function.Name == "scheduled_task" {
			t.Fatal("scheduled executions must not recursively manage scheduled tasks")
		}
		if tool.Function.Name == "command" {
			foundCommand = true
		}
	}
	if !foundCommand {
		t.Fatal("scheduled executions must receive command")
	}
}

// ─────────────────────── Background services ───────────────────────

func TestServiceHistoryCleanup(t *testing.T) {
	root := t.TempDir()
	app := NewApp()
	app.configPath = filepath.Join(root, "config.json")
	dir := app.serviceHistoryDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"svc_old.json", "svc_old.log"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("legacy"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := app.loadServiceHistory(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "svc_old.json")); !os.IsNotExist(err) {
		t.Fatalf("expected legacy metadata to be removed, got err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "svc_old.log")); !os.IsNotExist(err) {
		t.Fatalf("expected legacy output to be removed, got err=%v", err)
	}
	if listed := app.ListServices(); len(listed.Services) != 0 {
		t.Fatalf("expected no completed services after cleanup, got %#v", listed.Services)
	}
}

// TestServiceListForToolOmitsOutputTail verifies that the model-facing list
// action returns only metadata (no outputTail) so listing several services
// cannot dominate the model context. The model must call read to inspect
// output.
func TestServiceListForToolOmitsOutputTail(t *testing.T) {
	app := NewApp()
	buffer := newRollingBuffer(serviceOutputLimit)
	_, _ = buffer.Write([]byte("sensitive startup log\n"))
	app.servicesMu.Lock()
	app.services["svc_a"] = &managedService{
		info: ServiceInfo{
			ID: "svc_a", Name: "frontend", Command: "npm run dev",
			Cwd: "/tmp", PID: 111, Status: "running", StartedAt: time.Now().Unix(),
		},
		output: buffer,
	}
	app.servicesMu.Unlock()

	result := app.listServicesForTool()
	if result.MaxActive != maxActiveServices {
		t.Fatalf("expected maxActive=%d, got %d", maxActiveServices, result.MaxActive)
	}
	if result.ActiveCount != 1 || len(result.Services) != 1 {
		t.Fatalf("expected 1 active service, got %#v", result)
	}
	summary := result.Services[0]
	if summary.ID != "svc_a" || summary.Status != "running" || summary.PID != 111 {
		t.Fatalf("unexpected summary: %#v", summary)
	}
	// The summary type intentionally has no OutputTail field; verify by
	// round-tripping through JSON that no output content leaks.
	raw, err := json.Marshal(summary)
	if err != nil {
		t.Fatal(err)
	}
	encoded := string(raw)
	if strings.Contains(encoded, "outputTail") {
		t.Fatalf("list summary must not include outputTail: %s", encoded)
	}
	if strings.Contains(encoded, "sensitive startup log") {
		t.Fatalf("list summary must not include output content: %s", encoded)
	}
}

// TestServiceReadReturnsBoundedTail verifies that read returns at most
// tailBytes of recent output and reports the buffer/total byte accounting so
// the model can decide whether older output was discarded.
func TestServiceReadReturnsBoundedTail(t *testing.T) {
	app := NewApp()
	buffer := newRollingBuffer(serviceOutputLimit)
	payload := strings.Repeat("x", 4096) + "TAIL_MARKER"
	_, _ = buffer.Write([]byte(payload))

	app.servicesMu.Lock()
	app.services["svc_b"] = &managedService{
		info:   ServiceInfo{ID: "svc_b", Command: "demo", Status: "running"},
		output: buffer,
	}
	app.servicesMu.Unlock()

	// Default tail (8 KiB) should return the full payload since it is < 8 KiB.
	res, err := app.readServiceOutput(ServiceReadRequest{ID: "svc_b"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Output, "TAIL_MARKER") {
		t.Fatalf("expected tail marker in default read: %q", res.Output)
	}
	if res.TotalBytes != int64(len(payload)) || res.BufferBytes != int64(len(payload)) {
		t.Fatalf("unexpected byte accounting: %#v", res)
	}
	if res.FromByte != 0 {
		t.Fatalf("expected fromByte=0 for small buffer, got %d", res.FromByte)
	}

	// Explicit small tailBytes should clamp.
	res, err = app.readServiceOutput(ServiceReadRequest{ID: "svc_b", TailBytes: 16})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Output) != 16 || !strings.HasSuffix(res.Output, "TAIL_MARKER") {
		t.Fatalf("expected 16-byte tail ending in marker, got %d bytes: %q", len(res.Output), res.Output)
	}
	if res.FromByte != len(payload)-16 {
		t.Fatalf("expected fromByte=%d, got %d", len(payload)-16, res.FromByte)
	}

	// tailBytes above the hard cap must be clamped to maxServiceReadTailBytes.
	res, err = app.readServiceOutput(ServiceReadRequest{ID: "svc_b", TailBytes: 10 * 1024 * 1024})
	if err != nil {
		t.Fatal(err)
	}
	if res.ReturnedBytes > maxServiceReadTailBytes {
		t.Fatalf("read exceeded max tail bytes: %d", res.ReturnedBytes)
	}
}

// TestServiceReadErrors verifies the error codes for missing id and unknown id.
func TestServiceReadErrors(t *testing.T) {
	app := NewApp()
	if _, err := app.readServiceOutput(ServiceReadRequest{}); err == nil || toolErrorCode(err) != "E_BAD_SERVICE_ID" {
		t.Fatalf("expected E_BAD_SERVICE_ID for empty id, got %v", err)
	}
	if _, err := app.readServiceOutput(ServiceReadRequest{ID: "svc_missing"}); err == nil || toolErrorCode(err) != "E_SERVICE_NOT_FOUND" {
		t.Fatalf("expected E_SERVICE_NOT_FOUND for unknown id, got %v", err)
	}
}

// TestCompactToolResultForModelCapsMcpOutput 验证 mcp__ 工具输出与内置工具
// 一样有模型侧上限：超限输出被 head+tail 截断并带 outputTruncated 标记，
// 小输出原样通过。
func TestCompactToolResultForModelCapsMcpOutput(t *testing.T) {
	big := strings.Repeat("x", maxModelToolOutput*2)
	result := toolResult{OK: true, Data: map[string]any{"output": big}}
	fullJSON, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	compact := compactToolDataForModel("mcp__srv__tool", result, string(fullJSON))
	if len(compact) >= len(big) {
		t.Fatalf("MCP output must be capped: compact=%d raw=%d", len(compact), len(big))
	}
	var decoded struct {
		OK   bool `json:"ok"`
		Data struct {
			Output          string `json:"output"`
			OutputTruncated bool   `json:"outputTruncated"`
			TruncationNote  string `json:"truncationNote"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(compact), &decoded); err != nil {
		t.Fatal(err)
	}
	if !decoded.OK || !decoded.Data.OutputTruncated || decoded.Data.TruncationNote == "" {
		t.Fatalf("expected truncation flag and note, got %+v", decoded)
	}
	// compactTextForModel keeps head+tail within the cap plus a fixed
	// omission marker, so allow a small overhead over the limit.
	if n := utf8.RuneCountInString(decoded.Data.Output); n > maxModelToolOutput+200 {
		t.Fatalf("capped output must stay near %d runes, got %d", maxModelToolOutput, n)
	}

	small := toolResult{OK: true, Data: map[string]any{"output": "tiny"}}
	unchanged := compactToolDataForModel("mcp__srv__tool", small, `{"ok":true,"data":{"output":"tiny"}}`)
	if unchanged != `{"ok":true,"data":{"output":"tiny"}}` {
		t.Fatalf("small MCP output must pass through unchanged, got %s", unchanged)
	}
}
