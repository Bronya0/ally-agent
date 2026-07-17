package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

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

func TestEditToolSchemaIsAtomicChangesOnly(t *testing.T) {
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
	if !ok || len(changeProperties) != 2 {
		t.Fatalf("edit change should expose oldText/newText only: %#v", items)
	}
	if !strings.Contains(editTool.Description, "single file with one change") {
		t.Fatalf("edit tool description should include the minimal single-change example: %s", editTool.Description)
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

func TestDetectWriteBatchConflictsNormalizesSamePath(t *testing.T) {
	cfg := ConfigState{Workspace: t.TempDir()}
	calls := []openai.ToolCall{
		{Function: openai.FunctionCall{Name: "edit", Arguments: `{"files":[{"path":"sample.txt"}]}`}},
		{Function: openai.FunctionCall{Name: "delete_path", Arguments: `{"path":"./sample.txt"}`}},
		{Function: openai.FunctionCall{Name: "create_file", Arguments: `{"path":"other.txt"}`}},
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

func TestGrillModeFiltersSideEffectToolSchemas(t *testing.T) {
	app := NewApp()
	tools := app.buildToolsForConfig(ConfigState{grillMode: true})
	names := map[string]bool{}
	for _, tool := range tools {
		if tool.Function != nil {
			names[tool.Function.Name] = true
		}
	}
	for _, blocked := range []string{"edit", "create_file", "delete_path", "run_command", "background_process", "wait", "http_request", "web_fetch", "subagent", "memory_write", "todo_write", "create_goal", "update_goal"} {
		if names[blocked] {
			t.Fatalf("expected %s schema to be filtered in grill mode", blocked)
		}
	}
	for _, allowed := range []string{"list_files", "batch_read", "grep_files", "memory_read", "calculate", "ask", "get_goal", "Skill"} {
		if !names[allowed] {
			t.Fatalf("expected %s schema to remain available in grill mode", allowed)
		}
	}
}

func TestGrillModeRejectsSideEffectToolExecution(t *testing.T) {
	app := NewApp()
	result := app.executeTool(context.Background(), ConfigState{grillMode: true}, "session-1", "run_command", []byte(`{"command":"go test ./..."}`))
	if result.OK || !strings.Contains(result.Error, "disabled in grill mode") {
		t.Fatalf("expected grill mode execution guard, got %#v", result)
	}
}

func TestGrillModeRequiresOneAskQuestion(t *testing.T) {
	app := NewApp()
	result := app.executeTool(context.Background(), ConfigState{grillMode: true}, "session-1", "ask", []byte(`{"questions":[]}`))
	if result.OK || result.ErrorCode != "E_GRILL_ASK_COUNT" {
		t.Fatalf("expected grill ask count guard, got %#v", result)
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
		if tool.Function.Name == "background_process" {
			foundBackgroundProcess = true
		}
	}
	if !foundBackgroundProcess {
		t.Fatal("background_process must be exposed for non-blocking frontend/backend startup")
	}
}

func TestBackgroundProcessRejectsStatusPollingAction(t *testing.T) {
	app := NewApp()
	result := app.executeTool(context.Background(), ConfigState{Workspace: t.TempDir()}, "session-1", "background_process", []byte(`{"action":"status"}`))
	if result.OK || result.ErrorCode != "E_BAD_BACKGROUND_ACTION" {
		t.Fatalf("expected status polling action to be rejected, got %#v", result)
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
	compact := compactToolResultForModel("background_process", result, "fallback")
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
	if got.Count != 1 || got.Files != 1 {
		t.Fatalf("expected one match in hidden search root, got %#v", got)
	}
	if got.Matches[0].Path != ".github/workflows/ci.yml" {
		t.Fatalf("unexpected match path %q", got.Matches[0].Path)
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
	if got.Files != 2 || got.Count != 3 || got.Occurrences != 3 || !got.StatsExact {
		t.Fatalf("expected exact counts despite sample truncation, got %#v", got)
	}
	if !got.SamplesTruncated || !got.Truncated || len(got.Matches) != 2 {
		t.Fatalf("expected only the first file's sample matches and truncation, got %#v", got)
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
	if got.Count != 3 {
		t.Fatalf("expected three matching lines, got %#v", got)
	}
	if got.Occurrences != 4 {
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
	if got.Count != 5 || got.Occurrences != 5 || got.Files != 1 || !got.StatsExact {
		t.Fatalf("expected exact counts despite match sample truncation, got %#v", got)
	}
	if !got.SamplesTruncated || !got.Truncated || len(got.Matches) != 3 {
		t.Fatalf("expected three sample matches and truncation, got %#v", got)
	}
}

func TestGrepTimeoutSecondsDefaultsAndClamps(t *testing.T) {
	if got := grepTimeoutSeconds(GrepRequest{}); got != defaultGrepTimeout {
		t.Fatalf("expected default grep timeout %d, got %d", defaultGrepTimeout, got)
	}
	if got := grepTimeoutSeconds(GrepRequest{TimeoutSeconds: 7}); got != 7 {
		t.Fatalf("expected explicit grep timeout 7, got %d", got)
	}
	if got := grepTimeoutSeconds(GrepRequest{TimeoutSeconds: maxGrepTimeout + 1}); got != maxGrepTimeout {
		t.Fatalf("expected max grep timeout %d, got %d", maxGrepTimeout, got)
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
	if defaultResult.Count != 1 {
		t.Fatalf("expected ignored file to be skipped by default, got %#v", defaultResult)
	}

	includeIgnored, err := app.grepFilesWithConfig(context.Background(), ConfigState{Workspace: root}, GrepRequest{
		Pattern:        "needle",
		IncludeIgnored: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if includeIgnored.Count != 2 {
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
	if got.Count != 1 || got.Occurrences != 1 || got.Files != 1 {
		t.Fatalf("expected only source match outside heavy dirs, got %#v", got)
	}
	if got.Matches[0].Path != "src/main.go" {
		t.Fatalf("unexpected match path %q", got.Matches[0].Path)
	}
}

func requireRipgrep(t *testing.T) {
	t.Helper()
	if _, err := findRipgrep(); err != nil {
		t.Skip("ripgrep is not installed")
	}
}

func TestRipgrepCandidatesIncludeMacHomebrewPaths(t *testing.T) {
	t.Setenv("ALLY_RG_PATH", "")
	candidates := ripgrepCandidatesForOS("darwin")
	for _, want := range []string{"/opt/homebrew/bin/rg", "/usr/local/bin/rg"} {
		found := false
		for _, candidate := range candidates {
			if candidate == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expected darwin ripgrep candidates to include %s, got %#v", want, candidates)
		}
	}
}

func TestCompactToolResultForModelCompactsGrepMatches(t *testing.T) {
	matches := make([]GrepMatch, maxModelGrepMatches+5)
	for i := range matches {
		matches[i] = GrepMatch{Path: "a.txt", LineNum: i + 1, Content: "needle"}
	}
	result := toolResult{OK: true, Data: GrepResult{
		Matches:          matches,
		Count:            len(matches),
		Occurrences:      len(matches),
		Files:            1,
		SamplesTruncated: true,
		StatsExact:       true,
	}}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}

	got := compactToolResultForModel("grep_files", result, string(raw))

	var decoded struct {
		OK   bool `json:"ok"`
		Data struct {
			Matches            []GrepMatch `json:"matches"`
			Count              int         `json:"count"`
			Occurrences        int         `json:"occurrences"`
			Files              int         `json:"files"`
			SamplesTruncated   bool        `json:"samplesTruncated"`
			StatsExact         bool        `json:"statsExact"`
			MatchesReduced     bool        `json:"matchesReduced"`
			OriginalMatchCount int         `json:"originalMatchCount"`
			MatchesOmitted     int         `json:"matchesOmitted"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(got), &decoded); err != nil {
		t.Fatal(err)
	}
	if !decoded.OK {
		t.Fatalf("expected ok compacted result, got %s", got)
	}
	if len(decoded.Data.Matches) != maxModelGrepMatches {
		t.Fatalf("expected %d model matches, got %d", maxModelGrepMatches, len(decoded.Data.Matches))
	}
	if decoded.Data.Count != len(matches) || decoded.Data.Occurrences != len(matches) || decoded.Data.Files != 1 {
		t.Fatalf("expected grep stats to be preserved, got %#v", decoded.Data)
	}
	if !decoded.Data.StatsExact {
		t.Fatalf("expected exact stats marker to be preserved, got %#v", decoded.Data)
	}
	if !decoded.Data.SamplesTruncated {
		t.Fatalf("expected samplesTruncated marker to be preserved, got %#v", decoded.Data)
	}
	if !decoded.Data.MatchesReduced || decoded.Data.OriginalMatchCount != len(matches) || decoded.Data.MatchesOmitted != 5 {
		t.Fatalf("expected reduction metadata, got %#v", decoded.Data)
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

func TestDeletePathReturnsStructuredCounts(t *testing.T) {
	root := t.TempDir()
	writeToolTestFile(t, root, "dir/a.txt", "aa")
	writeToolTestFile(t, root, "dir/sub/b.txt", "bbb")
	app := NewApp()

	result, err := app.deletePathWithConfig(ConfigState{Workspace: root}, DeletePathRequest{
		Path:      "dir",
		Recursive: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Kind != "directory" || result.Path != "dir" || result.Deleted != "dir" || !result.Recursive {
		t.Fatalf("unexpected delete result metadata: %#v", result)
	}
	if result.RemovedFiles != 2 || result.RemovedDirs != 2 || result.RemovedBytes != 5 {
		t.Fatalf("unexpected delete counts: %#v", result)
	}
	if _, statErr := os.Stat(filepath.Join(root, "dir")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("expected directory to be removed, stat err=%v", statErr)
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
	result := app.executeTool(context.Background(), cfg, "session-1", "run_command", args)
	if !result.OK {
		t.Fatalf("expected run_command to succeed, got %#v", result)
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
	result := app.executeTool(context.Background(), ConfigState{Workspace: root}, "session-1", "run_command", args)
	if result.OK {
		t.Fatalf("expected run_command to reject long-running service, got %#v", result)
	}
	if result.ErrorCode != "E_LONG_RUNNING_COMMAND" {
		t.Fatalf("expected E_LONG_RUNNING_COMMAND, got %#v", result)
	}
}

func TestLooksLikeLongRunningServiceAllowsOrdinaryListCommand(t *testing.T) {
	if looksLikeLongRunningService("ls -la") {
		t.Fatal("ls -la must not be classified as a long-running service")
	}
}

func TestCommandSafetyAllowsCmdSlashCOption(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("cmd.exe /c is Windows-specific")
	}
	if err := checkCommandSafety(CommandRequest{Command: "cmd.exe /c ver"}, t.TempDir()); err != nil {
		t.Fatalf("cmd.exe /c option must not be treated as a C drive path: %v", err)
	}
}

func TestCommandSafetyAllowsReadOnlyOutsidePath(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("MSYS2 drive paths are Windows-specific")
	}
	command := `(ls -la /d/coding/python/ | grep xx)`
	if err := checkCommandSafety(CommandRequest{Command: command}, t.TempDir()); err != nil {
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
		if err := checkCommandSafety(CommandRequest{Command: command}, t.TempDir()); err != nil {
			t.Fatalf("Git Bash outside read with /dev/null should be allowed: %v", err)
		}
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
		if err := checkCommandSafety(CommandRequest{Command: command}, workspace); err != nil {
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
		err := checkCommandSafety(CommandRequest{Command: command}, workspace)
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

func TestCommandSafetyBlocksDynamicRedirectionTarget(t *testing.T) {
	err := checkCommandSafety(CommandRequest{Command: `printf changed > "$HOME/existing.txt"`}, t.TempDir())
	if toolErrorCode(err) != "E_PATH_OUTSIDE" {
		t.Fatalf("dynamic redirection target should be blocked conservatively, got %v", err)
	}
	if !strings.Contains(err.Error(), "无法确认真实写入位置") || !strings.Contains(err.Error(), `$HOME/existing.txt`) {
		t.Fatalf("dynamic redirection error should explain the uncertainty and target, got %v", err)
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
	if err := checkCommandSafety(CommandRequest{Command: command}, workspace); err != nil {
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
	err := checkCommandSafety(CommandRequest{Command: `touch ` + msysTarget}, t.TempDir())
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
	result := app.executeTool(context.Background(), ConfigState{Workspace: root}, "session-1", "run_command", args)
	if result.OK {
		t.Fatalf("expected run_command to reject outside symlink cwd, got %#v", result)
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
