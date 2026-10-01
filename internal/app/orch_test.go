// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package app

import (
	"bytes"
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
	"ally-dev/internal/tools/toolcall"
	openai "github.com/sashabaranov/go-openai"
)

func TestNormalizeJSONBodyArg(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    string
		wantFix bool
	}{
		{"object kept", `{"a":1}`, `{"a":1}`, false},
		{"quoted object unwrapped", `"{\"a\":1}"`, `{"a":1}`, true},
		{"quoted array unwrapped", `"[1,2]"`, `[1,2]`, true},
		{"quoted non-JSON kept", `"not json"`, `"not json"`, false},
		{"null cleared", `null`, ``, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, fixed := normalizeJSONBodyArg(json.RawMessage(tc.in))
			if fixed != tc.wantFix {
				t.Fatalf("repaired = %v, want %v", fixed, tc.wantFix)
			}
			if string(got) != tc.want {
				t.Fatalf("got %q, want %q", string(got), tc.want)
			}
		})
	}
}

func TestExecuteToolHTTPRequestJSONBodyDoubleEncodedString(t *testing.T) {
	var gotBody string
	var gotContentType string
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		gotBody = string(raw)
		gotContentType = r.Header.Get("Content-Type")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer target.Close()

	app := NewApp()
	// allowPrivateNetwork 是配置级开关，请求侧只能收紧不能放宽：把私网许可放在
	// config 上，模型参数不再需要（也拿不到）这个字段。
	args := fmt.Sprintf(`{"method":"POST","url":%q,"json":"{\"title\":\"x\",\"n\":1}"}`, target.URL+"/echo")
	result := app.executeTool(context.Background(), ConfigState{AllowPrivateNetwork: boolPtr(true)}, "session-1", "http_request", []byte(args))
	if !result.OK {
		t.Fatalf("expected success, got error: %s", result.Error)
	}
	if gotBody != `{"title":"x","n":1}` {
		t.Fatalf("expected unwrapped JSON object body, got %q", gotBody)
	}
	if !strings.Contains(gotContentType, "application/json") {
		t.Fatalf("expected application/json Content-Type, got %q", gotContentType)
	}
}

// TestHTTPRequestCannotWidenAllowPrivateNetwork: the config owns the SSRF
// switch and the model has no path to it. `allowPrivateNetwork` is not a
// declared parameter, so the argument gate rejects a call that tries to carry
// the override rather than silently ignoring it (or, worse, honouring it).
func TestHTTPRequestCannotWidenAllowPrivateNetwork(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer target.Close()

	app := NewApp()
	args := fmt.Sprintf(`{"url":%q,"allowPrivateNetwork":true}`, target.URL)
	result := app.executeTool(context.Background(), ConfigState{}, "session-1", "http_request", []byte(args))
	if result.OK {
		t.Fatalf("a request must not be able to open the private-network guard, got %+v", result.Data)
	}
	if result.ErrorCode != "E_BAD_ARGS" || !strings.Contains(result.Error, "allowPrivateNetwork") {
		t.Fatalf("expected the undeclared parameter to be rejected by name, got %#v", result)
	}
}

func TestHTTPRequestJSONBodyRawBytesForwardedExactly(t *testing.T) {
	var gotBody string
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		gotBody = string(raw)
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	app := NewApp()
	req := HTTPRequestToolRequest{
		Method: "POST",
		URL:    target.URL + "/echo",
		JSON:   json.RawMessage(`{"n": 1, "list": [true, null]}`),
	}
	if _, err := app.httpRequestToolWithConfig(context.Background(), app.effectiveConfigSafe(), req); err != nil {
		t.Fatal(err)
	}
	if gotBody != `{"n": 1, "list": [true, null]}` {
		t.Fatalf("expected raw JSON bytes to be forwarded byte-exactly, got %q", gotBody)
	}
}

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
	_, err := app.httpRequestToolWithConfig(context.Background(), app.effectiveConfigSafe(), HTTPRequestToolRequest{
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
	_, err := app.httpRequestToolWithConfig(context.Background(), app.effectiveConfigSafe(), HTTPRequestToolRequest{
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
	got, err := app.httpRequestToolWithConfig(context.Background(), app.effectiveConfigSafe(), HTTPRequestToolRequest{
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
	if len(properties) != 3 || properties["path"] == nil || properties["version"] == nil || properties["changes"] == nil {
		t.Fatalf("edit should expose exactly the flat path/version/changes fields: %#v", properties)
	}
	for _, legacy := range []string{"files", "oldString", "newString", "replaceAll", "edits", "startLine", "endLine", "newText"} {
		if _, exists := properties[legacy]; exists {
			t.Fatalf("edit schema still exposes legacy field %s", legacy)
		}
	}
	required, ok := params["required"].([]string)
	if !ok {
		t.Fatalf("unexpected edit required type %T", params["required"])
	}
	wantRequired := map[string]bool{"path": true, "version": true, "changes": true}
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
	for _, name := range []string{"target", "path", "version", "changes"} {
		if _, exists := remoteProperties[name]; !exists {
			t.Fatalf("remote_edit schema missing %s", name)
		}
	}
	for _, legacy := range []string{"files", "oldString", "newString", "replaceAll", "edits", "startLine", "endLine", "newText"} {
		if _, exists := remoteProperties[legacy]; exists {
			t.Fatalf("remote_edit schema still exposes legacy field %s", legacy)
		}
	}
	remoteRequired, ok := remoteParams["required"].([]string)
	if !ok {
		t.Fatalf("unexpected remote_edit required type %T", remoteParams["required"])
	}
	wantRemoteRequired := map[string]bool{"target": true, "path": true, "version": true, "changes": true}
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
	if properties["version"] == nil || properties["expectedMd5"] != nil {
		t.Fatalf("edit schema must expose version and reject expectedMd5: %#v", properties)
	}
	changesSchema, ok := properties["changes"].(map[string]any)
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
	for _, field := range []string{"oldText", "lineRange", "replaceAll", "newText"} {
		if _, exists := changeProperties[field]; !exists {
			t.Fatalf("edit change schema missing %s: %#v", field, items)
		}
	}
	changeRequired, ok := items["required"].([]string)
	if !ok || len(changeRequired) != 1 || changeRequired[0] != "newText" {
		t.Fatalf("edit change must require only newText: %#v", items["required"])
	}
	variants, ok := items["oneOf"].([]any)
	if !ok || len(variants) != 2 {
		t.Fatalf("edit change must require exactly one source form: %#v", items)
	}
	if _, exists := items["anyOf"]; exists {
		t.Fatalf("edit change must not accept two source forms: %#v", items)
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

func TestDetectWriteBatchConflictsBetweenSeparateMutationCalls(t *testing.T) {
	cfg := ConfigState{Workspace: t.TempDir()}
	// One flat edit call alone never conflicts with itself; repeated paths
	// inside one internal plan are merged by planLocalEditBatch instead.
	calls := []openai.ToolCall{{Function: openai.FunctionCall{
		Name:      "edit",
		Arguments: `{"path":"sample.txt","version":"abc123","changes":[{"oldText":"a","newText":"b"}]}`,
	}}}
	if conflicts := detectWriteBatchConflicts(cfg, calls); len(conflicts) != 0 {
		t.Fatalf("one local edit call should not conflict with itself, got %#v", conflicts)
	}

	// A later same-path mutation is skipped; the first call in tool-call index
	// order still executes.
	calls = append(calls, openai.ToolCall{Function: openai.FunctionCall{
		Name:      "delete",
		Arguments: `{"path":"sample.txt"}`,
	}})
	conflicts := detectWriteBatchConflicts(cfg, calls)
	if len(conflicts) != 1 {
		t.Fatalf("only the later same-path call must be skipped, got %#v", conflicts)
	}
	if _, exists := conflicts[0]; exists {
		t.Fatalf("the first mutation for a path must execute: %#v", conflicts)
	}
	if toolErrorCode(conflicts[1]) != "E_WRITE_BATCH_CONFLICT" {
		t.Fatalf("expected E_WRITE_BATCH_CONFLICT for call 1, got %v", conflicts[1])
	}
}

func TestDetectWriteBatchConflictsSkipsLaterSamePathCalls(t *testing.T) {
	cfg := ConfigState{Workspace: t.TempDir()}
	// Two edit calls to one file in one batch: the first executes, the second
	// is skipped with guidance to re-send after seeing the first result.
	calls := []openai.ToolCall{
		{Function: openai.FunctionCall{Name: "edit", Arguments: `{"path":"sample.txt","version":"abc123","changes":[{"oldText":"a","newText":"b"}]}`}},
		{Function: openai.FunctionCall{Name: "edit", Arguments: `{"path":"./sample.txt","version":"abc123","changes":[{"oldText":"c","newText":"d"}]}`}},
	}
	conflicts := detectWriteBatchConflicts(cfg, calls)
	if len(conflicts) != 1 {
		t.Fatalf("expected only the later edit call to be skipped, got %#v", conflicts)
	}
	if _, exists := conflicts[0]; exists {
		t.Fatalf("the first edit must execute: %#v", conflicts)
	}
	err := conflicts[1].Error()
	if !strings.Contains(err, "skipped") || !strings.Contains(err, "re-send") {
		t.Fatalf("skipped call should teach re-sending after the first result, got %v", err)
	}
	if toolErrorCode(conflicts[1]) != "E_WRITE_BATCH_CONFLICT" {
		t.Fatalf("expected E_WRITE_BATCH_CONFLICT for the second call, got %v", conflicts[1])
	}
}

func TestDetectWriteBatchConflictsNormalizesSamePath(t *testing.T) {
	cfg := ConfigState{Workspace: t.TempDir()}
	calls := []openai.ToolCall{
		{Function: openai.FunctionCall{Name: "edit", Arguments: `{"path":"sample.txt","version":"abc123","changes":[{"oldText":"a","newText":"b"}]}`}},
		{Function: openai.FunctionCall{Name: "delete", Arguments: `{"path":"./sample.txt"}`}},
		{Function: openai.FunctionCall{Name: "create", Arguments: `{"path":"other.txt"}`}},
	}
	conflicts := detectWriteBatchConflicts(cfg, calls)
	if len(conflicts) != 1 {
		t.Fatalf("expected only the later same-path call to be skipped, got %#v", conflicts)
	}
	if _, exists := conflicts[0]; exists {
		t.Fatalf("the first mutation must execute: %#v", conflicts)
	}
	if toolErrorCode(conflicts[1]) != "E_WRITE_BATCH_CONFLICT" {
		t.Fatalf("expected E_WRITE_BATCH_CONFLICT for call 1, got %v", conflicts[1])
	}
	if _, exists := conflicts[2]; exists {
		t.Fatalf("different path should not conflict: %#v", conflicts)
	}
}

// 表里的名字必须是真实注册的内置工具：拼错一个字母就会静默退回并发池
// （等于没生效），而这类错不会在别处报出来。
func TestToolBatchPhasesOnlyNamesRegisteredTools(t *testing.T) {
	registry := builtinToolNames(t)
	for name := range toolBatchPhases {
		if !registry[name] {
			t.Fatalf("toolBatchPhases declares %q, which is not a registered builtin tool", name)
		}
	}
}

// wait 不再是 barrier：混批时它只是被推迟到批次末尾（runChat / executeSubagent
// 的 deferred tail），因此同批任何调用都不该因它被拒。
func TestDetectToolBatchConflictsDefersWaitInsteadOfRejectingTheBatch(t *testing.T) {
	calls := []openai.ToolCall{
		{Function: openai.FunctionCall{Name: "wait", Arguments: `{"seconds":1,"reason":"service restart"}`}},
		{Function: openai.FunctionCall{Name: "http_request", Arguments: `{"url":"http://localhost:8080/health"}`}},
	}
	if conflicts := detectToolBatchConflicts(ConfigState{}, calls); len(conflicts) != 0 {
		t.Fatalf("a batch carrying wait must not be rejected, got %#v", conflicts)
	}
	// 判定本身按归一化后的工具名：中转把 wait 写成 Wait 时同样要排到末尾，
	// 否则它会落回并发池，与它等待的那批调用抢跑。
	if !isDeferredSerialTool("wait") || !isDeferredSerialTool("Wait") {
		t.Fatal("wait must be classified as the deferred tail tool (name normalization included)")
	}
	if !isOrderedFileMutationTool("Edit") || isOrderedFileMutationTool("wait") {
		t.Fatal("the phase table must classify ordered mutations and deferred tools independently")
	}
	for _, name := range []string{"ask", "suggest", "http_request", "read", "command"} {
		if toolBatchPhaseFor(name) != batchPhaseParallel {
			t.Fatalf("%s must stay in the default parallel phase", name)
		}
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

// The plan tool takes one writer per batch: two writes execute in the same
// concurrent pool, so whichever lands last silently wins and the other call's
// result contradicts it. Reads stay allowed next to a write, and identical ones
// are left to the dedup rule.
func TestDetectToolBatchConflictsKeepsOnePlanWriter(t *testing.T) {
	calls := []openai.ToolCall{
		{Function: openai.FunctionCall{Name: "plan", Arguments: `{"steps":["Read code","Run tests"]}`}},
		{Function: openai.FunctionCall{Name: "plan", Arguments: `{"finish":"Run tests"}`}},
		{Function: openai.FunctionCall{Name: "plan", Arguments: `{}`}},
	}
	conflicts := detectToolBatchConflicts(ConfigState{}, calls)
	if conflicts[0] != nil {
		t.Fatalf("the first plan write must execute, got %v", conflicts[0])
	}
	if toolErrorCode(conflicts[1]) != "E_PLAN_BATCH_CONFLICT" {
		t.Fatalf("expected E_PLAN_BATCH_CONFLICT for the second write, got %v", conflicts[1])
	}
	if conflicts[2] != nil {
		t.Fatalf("a plan read must not count as a second write, got %v", conflicts[2])
	}

	// Read-only plan calls never own the batch, and an absent source must not be
	// read as one just because the key is there.
	reads := []openai.ToolCall{
		{Function: openai.FunctionCall{Name: "plan", Arguments: `{}`}},
		{Function: openai.FunctionCall{Name: "plan", Arguments: `{"finish":""}`}},
		{Function: openai.FunctionCall{Name: "plan", Arguments: `{"finish":false}`}},
	}
	if got := detectToolBatchConflicts(ConfigState{}, reads); len(got) != 0 {
		t.Fatalf("read-only plan calls must not conflict, got %#v", got)
	}

	// Two write sources in one call is a contract error the handler reports, not
	// a write that owns the batch: counting it as a writer would let a malformed
	// call reject the valid write that follows, hiding the real complaint (the
	// batch rule uses the same classifier the handler runs).
	malformed := []openai.ToolCall{
		{Function: openai.FunctionCall{Name: "plan", Arguments: `{"steps":["Read code"],"finish":"Read code"}`}},
		{Function: openai.FunctionCall{Name: "plan", Arguments: `{"finish":"Run tests"}`}},
	}
	if got := detectToolBatchConflicts(ConfigState{}, malformed); len(got) != 0 {
		t.Fatalf("a malformed plan call must not shadow the valid write behind it, got %#v", got)
	}
}

// TestDetectToolBatchConflictsNormalizesToolNameCasing: a model or relay that
// answers with `Edit`/`Ask` must be classified exactly like `edit`/`ask`. The
// execution path normalizes the tool name at its own boundary, so the batch
// policy has to do the same — otherwise a mixed-case file mutation slipped into
// the parallel pool (no write ordering, E_VERSION_MISMATCH instead of
// E_WRITE_BATCH_CONFLICT) and a mixed-case ask no longer owned its batch.
func TestDetectToolBatchConflictsNormalizesToolNameCasing(t *testing.T) {
	cfg := ConfigState{Workspace: t.TempDir()}
	calls := []openai.ToolCall{
		{Function: openai.FunctionCall{Name: "Edit", Arguments: `{"path":"sample.txt","version":"abc123","changes":[{"oldText":"a","newText":"b"}]}`}},
		{Function: openai.FunctionCall{Name: "edit", Arguments: `{"path":"sample.txt","version":"abc123","changes":[{"oldText":"c","newText":"d"}]}`}},
	}
	conflicts := detectWriteBatchConflicts(cfg, calls)
	if len(conflicts) != 1 {
		t.Fatalf("mixed-case mutations of one path must conflict, got %#v", conflicts)
	}
	if toolErrorCode(conflicts[1]) != "E_WRITE_BATCH_CONFLICT" {
		t.Fatalf("expected E_WRITE_BATCH_CONFLICT for the second call, got %v", conflicts[1])
	}

	barrier := []openai.ToolCall{
		{Function: openai.FunctionCall{Name: "Ask", Arguments: `{"questions":[]}`}},
		{Function: openai.FunctionCall{Name: "list_files", Arguments: `{}`}},
	}
	barrierConflicts := detectToolBatchConflicts(ConfigState{}, barrier)
	if len(barrierConflicts) != len(barrier) {
		t.Fatalf("a mixed-case ask must reject every call in its batch, got %#v", barrierConflicts)
	}
	for i := range barrier {
		if code := toolErrorCode(barrierConflicts[i]); code != "E_ASK_BATCH_CONFLICT" {
			t.Fatalf("expected E_ASK_BATCH_CONFLICT for call %d, got %q", i, code)
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
	// The action enum lives in the schema, so the argument gate refuses the
	// call one round before the handler would: same refusal, and it names the
	// value that was wrong.
	if result.OK || !strings.Contains(result.Error, `"status"`) {
		t.Fatalf("expected the unknown action to be rejected by name, got %#v", result)
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
	// 0 is outside the declared 1..3600 range, so the gate refuses it before
	// waitWithContext runs; that handler keeps its own check for callers that
	// never go through tool arguments.
	if bad.OK || bad.ErrorCode != "E_BAD_ARGS" {
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

func TestAskAllowsOptionalRecommendation(t *testing.T) {
	// recommended=false on all options is now allowed (recommended is optional)
	err := validateAskRequest(AskRequest{
		Questions: []AskQuestion{
			{
				ID:       "q1",
				Question: "Choose",
				Options: []AskOption{
					{ID: "a", Label: "A", Description: "First"},
					{ID: "b", Label: "B", Description: "Second"},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("expected optional recommendation to succeed, got error: %v", err)
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
	if !strings.Contains(compact, `reduced-from="`) || !strings.Contains(compact, `id="svc_1"`) || !strings.Contains(compact, "<ally-svc ") {
		t.Fatalf("expected compact process metadata, got %s", compact)
	}
}

func TestContextBreakdownIncludesToolSchemas(t *testing.T) {
	app := NewApp()
	app.initialized = true
	app.config = ConfigState{}
	normal := app.getContextBreakdown("session-1", "")
	if normal.ToolSchemas <= 0 {
		t.Fatalf("expected tool schema tokens to be counted, got %#v", normal)
	}

}

// TestContextBreakdownCountsPlanSnapshotAndFrozenMap guards that the request prefix
// contains only the stable system prompt and workspace map, without transient plan snapshots
// polluting the prefix and breaking KV cache.
func TestContextBreakdownCountsPlanSnapshotAndFrozenMap(t *testing.T) {
	root := t.TempDir()
	app := NewApp()
	app.initialized = true
	app.config = ConfigState{Workspace: root}

	sessionID := "breakdown-session"

	// No plan: no plan part.
	bd := app.getContextBreakdown(sessionID, "")
	for _, part := range bd.SystemPromptParts {
		if part.Label == "计划快照" {
			t.Fatalf("plan snapshot part must be absent, got %#v", part)
		}
	}

	// With a plan, the snapshot is still not in prefix parts (state lives in tool messages).
	app.mu.Lock()
	if app.plans == nil {
		app.plans = map[string][]PlanStep{}
	}
	app.plans[sessionID] = []PlanStep{{Title: "fix the bug", Status: "in_progress"}}
	app.mu.Unlock()

	bd = app.getContextBreakdown(sessionID, "")
	for _, part := range bd.SystemPromptParts {
		if part.Label == "计划快照" {
			t.Fatalf("plan snapshot part must not be in system prompt parts, got %#v", part)
		}
	}
}

// TestContextBreakdownUsesSessionWorkspace guards the per-session workspace
// resolution: a session whose index entry records workspace B must be counted
// against B's AGENTS.md, not the active config workspace A.
func TestContextBreakdownUsesSessionWorkspace(t *testing.T) {
	workspaceA := t.TempDir()
	workspaceB := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspaceB, "AGENTS.md"), []byte("Workspace B rule: count me.\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	app := NewApp()
	app.initialized = true
	app.sessionsDir = t.TempDir()
	app.config = ConfigState{Workspace: workspaceA}

	sessionID := "multi-tab-session"
	if err := app.SaveSessionIndex(SessionIndexEntry{ID: sessionID, Workspace: workspaceB, Title: "B session"}); err != nil {
		t.Fatalf("SaveSessionIndex: %v", err)
	}
	// The override cache must not hide the freshly written index entry.
	sessionWorkspaceOverridesCache.Lock()
	sessionWorkspaceOverridesCache.generatedAt = time.Time{}
	sessionWorkspaceOverridesCache.overrides = nil
	sessionWorkspaceOverridesCache.Unlock()

	bd := app.getContextBreakdown(sessionID, "")
	foundB := false
	for _, part := range bd.SystemPromptParts {
		if part.Label == "AGENTS.md / 项目指令" && part.Tokens > 0 {
			foundB = true
		}
	}
	if !foundB {
		t.Fatalf("expected AGENTS.md part counted from workspace B, got %#v", bd.SystemPromptParts)
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

// grep 家族共用一套形状：铺一棵临时目录 → 发一次（或两次）搜索 → 看结果字段。
// 下面两张表把原来十三个函数收进来，断言逐条照旧，只是不再每个用例重复一遍
// requireRipgrep / t.TempDir / NewApp 这套开场。

// TestGrepFilesReportsErrors：搜索前的三类入参错误，各自有独立的错误码。
func TestGrepFilesReportsErrors(t *testing.T) {
	requireRipgrep(t)
	root := t.TempDir()
	writeToolTestFile(t, root, "sample.txt", "needle\n")
	app := NewApp()

	cases := []struct {
		name     string
		req      GrepRequest
		wantCode string
		wantText string
	}{
		{name: "missing search path", req: GrepRequest{Pattern: "needle", Path: "missing"}, wantCode: "E_GREP_PATH"},
		{name: "invalid regex", req: GrepRequest{Pattern: "["}, wantCode: "E_GREP_REGEX", wantText: "regex"},
		{name: "invalid glob", req: GrepRequest{Pattern: "needle", Glob: "["}, wantCode: "E_GREP_GLOB", wantText: "glob"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := app.grepFilesWithConfig(context.Background(), ConfigState{Workspace: root}, tc.req)
			if err == nil {
				t.Fatalf("expected %s, got no error", tc.wantCode)
			}
			if code := toolErrorCode(err); code != tc.wantCode {
				t.Fatalf("expected %s, got %q (%v)", tc.wantCode, code, err)
			}
			if tc.wantText != "" && !strings.Contains(err.Error(), tc.wantText) {
				t.Fatalf("error should mention %q, got %v", tc.wantText, err)
			}
		})
	}
}

// TestGrepFilesSearchResults：命中统计、采样截断、包含/排除策略、显式路径覆盖。
func TestGrepFilesSearchResults(t *testing.T) {
	large := strings.Repeat("x", 11*1024*1024) + "\nneedle\n"
	cases := []struct {
		name  string
		files map[string]string
		check func(t *testing.T, search func(GrepRequest) (*GrepResult, error))
	}{
		{name: "hidden directory as the search root", files: map[string]string{
			".github/workflows/ci.yml": "needle\n",
		}, check: func(t *testing.T, search func(GrepRequest) (*GrepResult, error)) {
			got, err := search(GrepRequest{Pattern: "needle", OutputMode: grep.OutputModeLines, Path: ".github"})
			if err != nil {
				t.Fatal(err)
			}
			if got.MatchedLines != 1 || got.Files != 1 {
				t.Fatalf("expected one match in hidden search root, got %#v", got)
			}
			if len(got.LineHits) != 1 || got.LineHits[0].Path != ".github/workflows/ci.yml" || len(got.LineHits[0].Lines) != 1 || got.LineHits[0].Lines[0] != 1 {
				t.Fatalf("unexpected lines-mode match %#v", got.LineHits)
			}
		}},
		{name: "default mode returns line groups", files: map[string]string{
			"a.txt": "needle\n", "b.txt": "needle\n",
		}, check: func(t *testing.T, search func(GrepRequest) (*GrepResult, error)) {
			got, err := search(GrepRequest{Pattern: "needle"})
			if err != nil {
				t.Fatal(err)
			}
			if got.Mode != grep.OutputModeLines || len(got.LineHits) != 2 || len(got.FileCounts) != 0 {
				t.Fatalf("default grep must return line groups without per-file counts, got %#v", got)
			}
		}},
		{name: "occurrence count counts every hit", files: map[string]string{
			"a.txt": "ally ally\nfinally\n", "b.txt": "ally\n",
		}, check: func(t *testing.T, search func(GrepRequest) (*GrepResult, error)) {
			got, err := search(GrepRequest{Pattern: "ally"})
			if err != nil {
				t.Fatal(err)
			}
			if got.MatchedLines != 3 {
				t.Fatalf("expected three matching lines, got %#v", got)
			}
			if got.Hits != 4 {
				t.Fatalf("expected four occurrences, got %#v", got)
			}
		}},
		{name: "exact counts survive line-budget truncation", files: map[string]string{
			"a.txt": "needle one\nneedle two\n", "b.txt": "needle three\n",
		}, check: func(t *testing.T, search func(GrepRequest) (*GrepResult, error)) {
			got, err := search(GrepRequest{Pattern: "needle", OutputMode: grep.OutputModeLines, MaxMatches: 1})
			if err != nil {
				t.Fatal(err)
			}
			if got.Files != 2 || got.MatchedLines != 3 || got.Hits != 3 {
				t.Fatalf("expected exact counts despite sample truncation, got %#v", got)
			}
			// 行预算是全局的：采样进一行之后，下一个命中文件就把它用完了。契约是
			// 「样本限制为一行」，不承诺落在哪个文件上。
			if !got.Truncated || len(got.LineHits) != 1 || len(got.LineHits[0].Lines) != 1 {
				t.Fatalf("expected single-line samples and truncation, got %#v", got)
			}
			hits := got.LineHits[0]
			if hits.Path != "a.txt" && hits.Path != "b.txt" {
				t.Fatalf("unexpected sample file %q", hits.Path)
			}
			if len(hits.Texts) != 1 || hits.Texts[0] == "" {
				t.Fatalf("expected the sampled line to carry its text preview, got %#v", hits)
			}
		}},
		{name: "exact counts survive match-limit truncation", files: map[string]string{
			"a.txt": "needle\nneedle\nneedle\nneedle\nneedle\n",
		}, check: func(t *testing.T, search func(GrepRequest) (*GrepResult, error)) {
			got, err := search(GrepRequest{Pattern: "needle", OutputMode: grep.OutputModeLines, MaxMatches: 3})
			if err != nil {
				t.Fatal(err)
			}
			if got.MatchedLines != 5 || got.Hits != 5 || got.Files != 1 {
				t.Fatalf("expected exact counts despite match sample truncation, got %#v", got)
			}
			if !got.Truncated || len(got.LineHits) != 1 || len(got.LineHits[0].Lines) != 3 {
				t.Fatalf("expected three sample lines and truncation, got %#v", got)
			}
		}},
		{name: "ignored files are opt-in", files: map[string]string{
			".ignore": "ignored.txt\n", "kept.txt": "needle\n", "ignored.txt": "needle\n",
		}, check: func(t *testing.T, search func(GrepRequest) (*GrepResult, error)) {
			defaultResult, err := search(GrepRequest{Pattern: "needle"})
			if err != nil {
				t.Fatal(err)
			}
			if defaultResult.MatchedLines != 1 {
				t.Fatalf("expected ignored file to be skipped by default, got %#v", defaultResult)
			}
			includeIgnored, err := search(GrepRequest{Pattern: "needle", IncludeIgnored: true})
			if err != nil {
				t.Fatal(err)
			}
			if includeIgnored.MatchedLines != 2 {
				t.Fatalf("expected ignored file to be included, got %#v", includeIgnored)
			}
		}},
		{name: "heavy directories are always excluded", files: map[string]string{
			"src/main.go": "ally\n", "frontend/dist/app.js": "ally\n", "node_modules/pkg/index.js": "ally\n",
		}, check: func(t *testing.T, search func(GrepRequest) (*GrepResult, error)) {
			got, err := search(GrepRequest{Pattern: "ally", OutputMode: grep.OutputModeLines, IncludeIgnored: true})
			if err != nil {
				t.Fatal(err)
			}
			if got.MatchedLines != 1 || got.Hits != 1 || got.Files != 1 {
				t.Fatalf("expected only source match outside heavy dirs, got %#v", got)
			}
			if got.LineHits[0].Path != "src/main.go" {
				t.Fatalf("unexpected match path %q", got.LineHits[0].Path)
			}
			if len(got.Skipped) == 0 {
				t.Fatalf("workspace-wide grep must report its skip policy, got %#v", got)
			}
		}},
		{name: "explicit path overrides broad exclusions", files: map[string]string{
			"vendor/pkg/source.go": "needle\n",
		}, check: func(t *testing.T, search func(GrepRequest) (*GrepResult, error)) {
			got, err := search(GrepRequest{Pattern: "needle", OutputMode: grep.OutputModeLines, Path: "vendor"})
			if err != nil {
				t.Fatal(err)
			}
			if got.MatchedLines != 1 || got.Hits != 1 || got.Files != 1 {
				t.Fatalf("explicit path must override broad-search exclusions, got %#v", got)
			}
			if len(got.LineHits) != 1 || got.LineHits[0].Path != "vendor/pkg/source.go" {
				t.Fatalf("unexpected explicit-path lines result %#v", got.LineHits)
			}
			if len(got.Skipped) != 0 {
				t.Fatalf("explicit path search must not report broad skip policies, got %#v", got.Skipped)
			}
		}},
		{name: "explicit path searches large files", files: map[string]string{
			"large.txt": large,
		}, check: func(t *testing.T, search func(GrepRequest) (*GrepResult, error)) {
			workspaceSearch, err := search(GrepRequest{Pattern: "needle"})
			if err != nil {
				t.Fatal(err)
			}
			if workspaceSearch.MatchedLines != 0 {
				t.Fatalf("workspace-wide search should keep the large-file guard, got %#v", workspaceSearch)
			}
			explicitSearch, err := search(GrepRequest{Pattern: "needle", Path: "large.txt"})
			if err != nil {
				t.Fatal(err)
			}
			if explicitSearch.MatchedLines != 1 || explicitSearch.Hits != 1 || explicitSearch.Files != 1 {
				t.Fatalf("explicit path must search large files, got %#v", explicitSearch)
			}
		}},
		{name: "file counts and offset end to end", files: map[string]string{
			"cold.txt": "needle\n",
		}, check: func(t *testing.T, search func(GrepRequest) (*GrepResult, error)) {
			// hot.txt 由用例自己写：12 行命中 + cold.txt 1 行，共 13。
			counts, err := search(GrepRequest{Pattern: "needle", OutputMode: grep.OutputModeCountMatches})
			if err != nil {
				t.Fatal(err)
			}
			if counts.MatchedLines != 13 || counts.Hits != 13 || counts.Files != 2 {
				t.Fatalf("expected exact stats, got %#v", counts)
			}
			if len(counts.FileCounts) != 2 || counts.FileCounts[0].Path != "hot.txt" || counts.FileCounts[0].Count != 12 || counts.FileCounts[1].Path != "cold.txt" || counts.FileCounts[1].Count != 1 {
				t.Fatalf("expected descending fileCounts, got %#v", counts.FileCounts)
			}
			got, err := search(GrepRequest{Pattern: "needle", OutputMode: grep.OutputModeLines, MaxMatches: 5})
			if err != nil {
				t.Fatal(err)
			}
			if got.MatchedLines != 13 || got.Hits != 13 || got.Files != 2 {
				t.Fatalf("expected exact stats, got %#v", got)
			}
			if !got.Truncated || got.NextOffset != 5 {
				t.Fatalf("expected truncated page with NextOffset 5, got %#v", got)
			}
			// 行预算跨文件组共享，且 rg 的遍历顺序不是字典序：这里先到 cold.txt，
			// 所以第一页跨两个组、合计五个行号，而不是一个组的五行。
			pageLines := 0
			for _, g := range got.LineHits {
				pageLines += len(g.Lines)
			}
			if pageLines != 5 {
				t.Fatalf("expected five sampled line numbers on page one, got %#v", got.LineHits)
			}
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			requireRipgrep(t)
			root := t.TempDir()
			for name, body := range tc.files {
				writeToolTestFile(t, root, name, body)
			}
			if tc.name == "file counts and offset end to end" {
				var b strings.Builder
				for i := 0; i < 12; i++ {
					fmt.Fprintf(&b, "line %d needle\n", i)
				}
				writeToolTestFile(t, root, "hot.txt", b.String())
			}
			app := NewApp()
			tc.check(t, func(req GrepRequest) (*GrepResult, error) {
				return app.grepFilesWithConfig(context.Background(), ConfigState{Workspace: root}, req)
			})
		})
	}
}

func requireRipgrep(t *testing.T) {
	t.Helper()
	if _, err := grep.Find(); err != nil {
		t.Skip("ripgrep is not installed")
	}
}

func TestCompactToolResultForModelCompactsGrepLines(t *testing.T) {
	total := maxModelGrepMatches + 5
	lines := make([]int, total)
	for i := range lines {
		lines[i] = i + 1
	}
	result := toolResult{OK: true, Data: GrepResult{
		Mode:         "lines",
		LineHits:     []GrepFileMatch{{Path: "a.txt", Lines: lines}},
		MatchedLines: total,
		Hits:         total,
		Files:        1,
		Truncated:    true,
		NextOffset:   total,
	}}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}

	got := compactToolResultForModel("grep", result, string(raw))

	if !strings.HasPrefix(got, fmt.Sprintf("<ally-grep mode=\"lines\" matched=\"%d\" files=\"1\" truncated next-offset=\"%d\">\n", total, maxModelGrepMatches)) {
		t.Fatalf("expected capped header with adjusted next-offset, got %s", got)
	}
	// The approximation marker is gone for good: it could never be false (Search
	// errors out when ripgrep emits no statistics), so the model view must not
	// grow one back.
	if strings.Contains(got, "stats-approx") {
		t.Fatalf("model view must not carry an approximation marker: %s", got)
	}
	if !strings.Contains(got, fmt.Sprintf("[%d of %d matching lines shown]", maxModelGrepMatches, total)) {
		t.Fatalf("expected reduction note, got %.200s", got)
	}
	if !strings.Contains(got, "a.txt\n  1\n") || !strings.Contains(got, fmt.Sprintf("  %d\n", maxModelGrepMatches)) {
		t.Fatalf("expected first and last kept line, got %.200s", got)
	}
	if strings.Contains(got, fmt.Sprintf("  %d\n", maxModelGrepMatches+1)) {
		t.Fatalf("line beyond the cap must be dropped, got %.200s", got)
	}
}

func TestCompactToolResultForModelGroupsGrepRowsByFile(t *testing.T) {
	// lines mode renders one bare path row per file group, then indented
	// "line: text" rows — the path is spelled once per file to save tokens.
	result := toolResult{OK: true, Data: GrepResult{
		Mode: "lines", MatchedLines: 3, Hits: 3, Files: 2,
		LineHits: []GrepFileMatch{
			{Path: "a/x.txt", Lines: []int{10, 20}, Texts: []string{"ten", "twenty"}},
			{Path: "b/y.txt", Lines: []int{5}},
		},
	}}
	got := compactToolResultForModel("grep", result, "fallback")
	want := "a/x.txt\n  10: ten\n  20: twenty\nb/y.txt\n  5\n"
	if !strings.Contains(got, want) {
		t.Fatalf("expected grouped rows %q, got %q", want, got)
	}
	if strings.Contains(got, "a/x.txt:") {
		t.Fatalf("path must not be repeated per match row, got %q", got)
	}
}

func TestCompactToolResultForModelGreFallsFlatForIndentedPath(t *testing.T) {
	// A path with leading whitespace would forge an indented match row, so the
	// whole block falls back to flat "path:line: text" rows.
	result := toolResult{OK: true, Data: GrepResult{
		Mode: "lines", MatchedLines: 2, Hits: 2, Files: 2,
		LineHits: []GrepFileMatch{
			{Path: " indented.txt", Lines: []int{1}, Texts: []string{"one"}},
			{Path: "ok.txt", Lines: []int{2}, Texts: []string{"two"}},
		},
	}}
	got := compactToolResultForModel("grep", result, "fallback")
	if !strings.Contains(got, " indented.txt:1: one\n") || !strings.Contains(got, "ok.txt:2: two\n") {
		t.Fatalf("expected flat fallback rows, got %q", got)
	}
	if strings.Contains(got, "\n  1:") {
		t.Fatalf("no indented rows in flat mode, got %q", got)
	}
}

func TestCompactToolResultForModelRendersStructuredToolsAsTags(t *testing.T) {
	cases := []struct {
		name string
		tool string
		data any
		want string
	}{
		{
			name: "wait",
			tool: "wait",
			data: WaitResult{RequestedSeconds: 30, ElapsedMS: 30120, Reason: "wait for dev server", Completed: true},
			want: `<ally-wait seconds="30" elapsed-ms="30120">wait for dev server</ally-wait>`,
		},
		{
			name: "ask",
			tool: "ask",
			data: AskResult{AskID: "ask_1", Answers: []AskResolvedAnswer{{
				QuestionID: "db", Question: "Which database?",
				Selections: []AskResolvedSelection{{Label: "SQLite", Recommended: true, Description: "Simple"}, {Label: "keep local", Custom: true}},
			}}},
			want: "<ally-ask id=\"ask_1\">\n" +
				"  db: Which database?\n" +
				"  selected: SQLite (recommended) — Simple\n" +
				"  selected: keep local [custom]\n" +
				"</ally-ask>",
		},
		{
			name: "plan-set",
			tool: "plan",
			data: map[string]any{"action": "set", "plan": []PlanStep{{Title: "Inspect code", Status: "in_progress"}, {Title: "Run tests", Status: "pending"}}, "revision": int64(1)},
			want: "<ally-plan action=\"set\" revision=\"1\" state=\"running\" done=\"0\" total=\"2\">\n" +
				"  1 [in_progress] Inspect code\n" +
				"  2 [pending] Run tests\n" +
				"When \"Inspect code\" is done: send {\"finish\":\"Inspect code\"} to mark it — and every step before it — done.\n" +
				"</ally-plan>",
		},
		{
			name: "plan-report-on-the-last-step",
			tool: "plan",
			data: map[string]any{"action": "finish", "plan": []PlanStep{{Title: "Inspect code", Status: "done"}, {Title: "Run tests", Status: "in_progress"}}, "revision": int64(2)},
			want: "<ally-plan action=\"finish\" revision=\"2\" state=\"running\" done=\"1\" total=\"2\">\n" +
				"  1 [done] Inspect code\n" +
				"  2 [in_progress] Run tests\n" +
				"When \"Run tests\" is done: send {\"finish\":\"Run tests\"} to mark it — and every step before it — done.\n" +
				"</ally-plan>",
		},
		{
			// A plan ends when the work reaches its last step, so the report is the
			// call that leaves it closed with every row done.
			name: "plan-finish",
			tool: "plan",
			data: map[string]any{"action": "finish", "plan": []PlanStep{{Title: "Run tests", Status: "done"}}, "revision": int64(3)},
			want: "<ally-plan action=\"finish\" revision=\"3\" state=\"closed\" done=\"1\" total=\"1\">\n" +
				"  1 [done] Run tests\n" +
				"This plan is closed: every step is done.\n" +
				"</ally-plan>",
		},
		{
			name: "plan-cleared",
			tool: "plan",
			data: map[string]any{"action": "set", "plan": []PlanStep{}, "revision": int64(4)},
			want: `<ally-plan action="set" revision="4" state="empty" done="0" total="0"/>`,
		},
		{
			name: "subagent",
			tool: "subagent",
			data: AgentDelegateResult{AgentID: "agent_1", Role: "reviewer", Status: "completed", Steps: 5, Model: "m1", Summary: "All good.", FilesRead: []string{"a.go"}, FilesEdited: []string{"b.go"}},
			want: "<ally-subagent id=\"agent_1\" role=\"reviewer\" status=\"completed\" steps=\"5\" model=\"m1\">\n" +
				"All good.\n" +
				"read: a.go\n" +
				"edited: b.go\n" +
				"</ally-subagent>",
		},
		{
			name: "scheduled_task-delete",
			tool: "scheduled_task",
			data: ScheduledTaskToolResult{Deleted: "t_1", Count: 2},
			want: `<ally-task deleted="t_1" count="2"/>`,
		},
		{
			name: "scheduled_task-create",
			tool: "scheduled_task",
			data: ScheduledTaskToolResult{Task: &ScheduledTaskToolView{ID: "t_2", Name: "nightly", Command: "go test ./...", Workspace: ".", Schedule: ScheduledTaskSchedule{Type: "cron", Cron: "0 3 * * *"}, MaxSteps: 30, TimeoutSeconds: 600, LastStatus: "pending"}},
			want: "<ally-task id=\"t_2\" name=\"nightly\" schedule=\"cron:0 3 * * *\" status=\"pending\" runs=\"0\" max-steps=\"30\" timeout=\"600s\" kind=\"command\">\n" +
				"go test ./...\n" +
				"</ally-task>",
		},
		{
			name: "scheduled_task-list",
			tool: "scheduled_task",
			data: ScheduledTaskToolResult{Count: 1, Tasks: []ScheduledTaskToolView{{ID: "t_3", Name: "sync", Schedule: ScheduledTaskSchedule{Type: "interval", Every: "30m"}, LastStatus: "ok", RunCount: 4}}},
			want: "<ally-tasks count=\"1\">\n" +
				`  t_3: name="sync" schedule="every:30m" status="ok" runs=4` + "\n" +
				"</ally-tasks>",
		},
		{
			name: "scheduled_task-list-one-shot",
			tool: "scheduled_task",
			data: ScheduledTaskToolResult{Count: 1, Tasks: []ScheduledTaskToolView{{ID: "t_4", Name: "warm", Schedule: ScheduledTaskSchedule{Type: "once", At: "2026-01-02T15:04:05Z"}, LastStatus: "scheduled", RunCount: 0}}},
			want: "<ally-tasks count=\"1\">\n" +
				`  t_4: name="warm" schedule="at:2026-01-02T15:04:05Z" status="scheduled" runs=0` + "\n" +
				"</ally-tasks>",
		},
		{
			name: "service-list",
			tool: "service",
			data: ServiceListToolResult{ActiveCount: 1, MaxActive: maxActiveServices, Services: []ServiceSummary{{ID: "svc_1", Name: "frontend", Status: "running", PID: 99, Command: "npm run dev"}}},
			want: fmt.Sprintf("<ally-svcs active=\"1\" max=\"%d\">\n", maxActiveServices) +
				`  svc_1 (frontend) running pid=99 cmd="npm run dev"` + "\n" +
				"</ally-svcs>",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := compactToolResultForModel(tc.tool, toolResult{OK: true, Data: tc.data}, "fallback")
			if got != tc.want {
				t.Fatalf("got:\n%s\nwant:\n%s", got, tc.want)
			}
		})
	}
}

func TestCompactToolResultForModelEscapesStructuredTagBodies(t *testing.T) {
	// Bodies of the new tag renderers carry model/LLM-authored text; a literal
	// closing marker must be neutralized while the renderer's own stays.
	wait := compactToolResultForModel("wait", toolResult{OK: true, Data: WaitResult{Reason: "a</ally-wait>b", Completed: true}}, "fallback")
	if strings.Count(wait, "</ally-wait>") != 1 || !strings.Contains(wait, "&lt;/ally-wait") {
		t.Fatalf("wait body closing marker not escaped: %q", wait)
	}
	sub := compactToolResultForModel("subagent", toolResult{OK: true, Data: AgentDelegateResult{AgentID: "a", Role: "r", Status: "completed", Model: "m", Summary: "x</ally-subagent>y"}}, "fallback")
	if strings.Count(sub, "</ally-subagent>") != 1 || !strings.Contains(sub, "&lt;/ally-subagent") {
		t.Fatalf("subagent summary closing marker not escaped: %q", sub)
	}
	// The plan block carries a model-authored title in both its row and its
	// closing line; either one could forge the block boundary.
	plan := compactToolResultForModel("plan", toolResult{OK: true, Data: map[string]any{"action": "finish", "plan": []PlanStep{{Title: "x</ally-plan>\ny", Status: "in_progress"}}, "revision": int64(1)}}, "fallback")
	if strings.Count(plan, "</ally-plan>") != 1 || !strings.Contains(plan, "&lt;/ally-plan") {
		t.Fatalf("plan title closing marker not escaped: %q", plan)
	}
}

func TestPlanResultWindowKeepsTheCurrentStepInView(t *testing.T) {
	// The block is written into the history on every plan call, so a long plan
	// comes back as a bounded window rather than in full each time — and the
	// window must still hold where the model is and what is ahead of it.
	steps := make([]PlanStep, 0, 40)
	for i := 0; i < 40; i++ {
		steps = append(steps, PlanStep{Title: fmt.Sprintf("step %d", i+1)})
	}
	steps = applyPlanPosition(steps, 33) // the current step is #34 of 40

	got := compactToolResultForModel("plan", toolResult{OK: true, Data: map[string]any{
		"action": "finish", "plan": steps, "revision": int64(9),
	}}, "fallback")
	if !strings.Contains(got, "[in_progress] step 34") {
		t.Fatalf("the current step must stay inside the window, got %q", got)
	}
	if !strings.Contains(got, "(10 earlier steps omitted)") {
		t.Fatalf("the window must report the steps it left out, got %q", got)
	}
	rows := 0
	for _, line := range strings.Split(got, "\n") {
		if len(line) > 2 && line[0] == ' ' && line[1] == ' ' && line[2] >= '0' && line[2] <= '9' {
			rows++
		}
	}
	if rows != planModelRowLimit {
		t.Fatalf("window rows = %d, want %d", rows, planModelRowLimit)
	}
}

func TestCompactToolResultForModelCapsLinesAcrossFiles(t *testing.T) {
	// Many line groups, each over budget: the cap is global (maxModelGrepMatches
	// total lines), not per file, and exact totals survive in the header.
	over := maxModelGrepMatches + 10
	build := func() []GrepFileMatch {
		lines := make([]int, over)
		for i := range lines {
			lines[i] = i + 1
		}
		return []GrepFileMatch{
			{Path: "a.txt", Lines: lines},
			{Path: "b.txt", Lines: lines},
		}
	}
	result := toolResult{OK: true, Data: GrepResult{
		LineHits:     build(),
		MatchedLines: 2 * over,
		Hits:         2 * over,
		Files:        2,
		Truncated:    true,
	}}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}

	compact := compactToolResultForModel("grep", result, string(raw))
	if !strings.Contains(compact, fmt.Sprintf("[%d of %d matching lines shown]", maxModelGrepMatches, 2*over)) {
		t.Fatalf("expected global cap note across files, got %.120s", compact)
	}
	if !strings.Contains(compact, fmt.Sprintf("  %d\n", maxModelGrepMatches)) {
		t.Fatalf("expected the cap boundary line kept, got %.120s", compact)
	}
	if strings.Contains(compact, "b.txt") {
		t.Fatalf("second file must be dropped once the global cap is spent, got %.120s", compact)
	}
}

func TestCompactToolResultForModelPreservesGrepCounts(t *testing.T) {
	// count_matches carries per-file counts instead of line groups. Every row of
	// the page must reach the model: the renderer applies no cap of its own here,
	// because next-offset — which the tool computed for the whole page — would
	// otherwise resume past the trimmed rows and the model would skip those files
	// without notice. The page width itself is the grep tool's own contract and is
	// asserted in that package.
	const pageWidth = 25 // deliberately wider than the tool's real page
	counts := make([]GrepFileCount, pageWidth)
	for i := range counts {
		counts[i] = GrepFileCount{Path: fmt.Sprintf("f%02d.txt", i), Count: pageWidth - i}
	}
	result := toolResult{OK: true, Data: GrepResult{
		Mode:         "count_matches",
		FileCounts:   counts,
		MatchedLines: 40,
		Hits:         60,
		Files:        pageWidth,
	}}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}

	got := compactToolResultForModel("grep", result, string(raw))
	if !strings.HasPrefix(got, "<ally-grep mode=\"count_matches\" matched=\"40\" hits=\"60\" files=\"25\">\n") {
		t.Fatalf("expected counts header with the explicit mode and exact totals, got %s", got)
	}
	if !strings.Contains(got, "f00.txt: count=25\n") || !strings.Contains(got, fmt.Sprintf("f%02d.txt: count=1\n", pageWidth-1)) {
		t.Fatalf("expected every returned per-file count in the payload, got %s", got)
	}
	if strings.Contains(got, "[file counts capped]") {
		t.Fatalf("count rows must not be capped a second time, got %s", got)
	}
}

func TestCompactEditResultForModelPreservesWarnings(t *testing.T) {
	result := toolResult{OK: true, Data: MultiEditResult{
		FileCount:    1,
		Replacements: 1,
		Warnings:     []string{"ignored 2 no-op change(s) whose oldText and newText were identical"},
		Files: []EditResult{{
			Path:          "sample.txt",
			BeforeVersion: "abcdef",
			Version:       "ghjkmn",
		}},
	}}
	compact := compactToolResultForModel("edit", result, "fallback")
	if !strings.Contains(compact, "warning: ignored 2 no-op change(s)") {
		t.Fatalf("expected compact edit result to retain warnings, got %s", compact)
	}
}

func TestCompactListFilesResultForModelUsesPathList(t *testing.T) {
	result := toolResult{OK: true, Data: ListFilesResult{
		Entries: []FileEntry{
			{Path: "frontend", Name: "frontend", Dir: true, Size: 0, ModTime: "2026-08-30T10:00:00+08:00"},
			{Path: "frontend/src/App.vue", Name: "App.vue", Size: 185432, ModTime: "2026-08-30T10:00:00+08:00"},
			{Path: "link", Name: "link", Symlink: true},
		},
		Count: 3,
	}}
	compact := compactToolResultForModel("list_files", result, "fallback")
	if !strings.Contains(compact, "frontend/\nfrontend/src/App.vue\nlink") {
		t.Fatalf("expected a newline-joined path list with dir slashes, got %s", compact)
	}
	// Per-entry metadata must not leak into the model copy.
	for _, noisy := range []string{"modTime", "185432", `"symlink"`, `"name"`} {
		if strings.Contains(compact, noisy) {
			t.Fatalf("compact list_files result must drop %s, got %s", noisy, compact)
		}
	}
	if !strings.Contains(compact, `count="3"`) || strings.Contains(compact, ` truncated`) {
		t.Fatalf("count/truncated summary mismatch: %s", compact)
	}

	// The per-directory overflow placeholder renders as the workspace map's
	// "+N more files" line instead of a fake path.
	overflow := toolResult{OK: true, Data: ListFilesResult{
		Entries: []FileEntry{
			{Path: "data", Name: "data", Dir: true},
			{Path: "data/a.csv", Name: "a.csv"},
			{Path: "data/+more", Name: "+more", MoreFiles: 9950},
		},
		Count: 3,
	}}
	compact = compactToolResultForModel("list_files", overflow, "fallback")
	if !strings.Contains(compact, "data/a.csv\n+9950 more files") {
		t.Fatalf("expected the +N more files line, got %s", compact)
	}
	if strings.Contains(compact, "+more") {
		t.Fatalf("the +more path marker must not leak into the model copy, got %s", compact)
	}

	empty := compactToolResultForModel("list_files", toolResult{OK: true, Data: ListFilesResult{}}, "fallback")
	if !strings.Contains(empty, "hidden") {
		t.Fatalf("empty listing must explain why it can be empty, got %s", empty)
	}
	truncated := compactToolResultForModel("list_files", toolResult{OK: true, Data: ListFilesResult{Count: 200, Truncated: true}}, "fallback")
	if !strings.Contains(truncated, "narrow the path") {
		t.Fatalf("truncated listing must carry a narrowing note, got %s", truncated)
	}
}

func TestListFilesDirBudgetCollapsesModelListingsOnly(t *testing.T) {
	root := t.TempDir()
	// One directory with more direct files than the budget, plus a sibling
	// directory that must stay fully visible.
	for i := 0; i < listFilesDirBudget+20; i++ {
		writeToolTestFile(t, root, filepath.Join("data", fmt.Sprintf("f%03d.csv", i)), "x\n")
	}
	writeToolTestFile(t, root, filepath.Join("src", "main.go"), "package main\n")
	writeToolTestFile(t, root, filepath.Join("src", "util.go"), "package main\n")

	// Model-facing: data contributes exactly the budget's worth of real
	// files plus one placeholder covering the remaining 20; src is complete.
	result, err := NewApp().listFilesWithConfig(ConfigState{Workspace: root}, ListFilesRequest{ModelFacing: true, MaxDepth: 3, Limit: 1000})
	if err != nil {
		t.Fatal(err)
	}
	var dataFiles, srcFiles int
	var placeholder *FileEntry
	for i := range result.Entries {
		e := result.Entries[i]
		switch {
		case e.Dir:
		case e.Name == "main.go" || e.Name == "util.go":
			srcFiles++
		case strings.HasPrefix(e.Path, "data/"):
			if e.MoreFiles > 0 {
				placeholder = &result.Entries[i]
			} else {
				dataFiles++
			}
		}
	}
	if dataFiles != listFilesDirBudget || srcFiles != 2 {
		t.Fatalf("budget breakdown wrong: dataFiles=%d srcFiles=%d", dataFiles, srcFiles)
	}
	if placeholder == nil || placeholder.MoreFiles != 20 || placeholder.Path != "data/+more" {
		t.Fatalf("expected a data/+more placeholder with +20, got %#v", placeholder)
	}
	// The global limit must not have been consumed by data's overflow: src
	// files are present and the result is not flagged truncated.
	if result.Truncated {
		t.Fatal("per-directory collapse must not set the global truncated flag")
	}

	// The UI explorer (ModelFacing=false) keeps every real file.
	uiResult, err := NewApp().ListFiles(ListFilesRequest{Workspace: root, MaxDepth: 3, Limit: 1000})
	if err != nil {
		t.Fatal(err)
	}
	uiDataFiles := 0
	for _, e := range uiResult.Entries {
		if !e.Dir && strings.HasPrefix(e.Path, "data/") {
			uiDataFiles++
		}
	}
	if uiDataFiles != listFilesDirBudget+20 {
		t.Fatalf("UI listing must keep all real files, got %d", uiDataFiles)
	}
}

func TestPlanBatchValidationMergesSharedUnits(t *testing.T) {
	root := t.TempDir()
	cfg := ConfigState{Workspace: root}
	writeToolTestFile(t, root, "pkg/a.go", "package pkg\n")
	writeToolTestFile(t, root, "pkg/b.go", "package pkg\n")
	writeToolTestFile(t, root, "web/x.vue", "<template><div/></template>\n")
	writeToolTestFile(t, root, "data/cfg.json", "{}\n")
	calls := []openai.ToolCall{
		{Function: openai.FunctionCall{Name: "edit", Arguments: `{"path":"pkg/a.go","version":"abc123","changes":[{"oldText":"a","newText":"b"}]}`}},
		{Function: openai.FunctionCall{Name: "read", Arguments: `{"files":[{"path":"pkg/a.go"}]}`}},
		{Function: openai.FunctionCall{Name: "edit", Arguments: `{"path":"pkg/b.go","version":"abc123","changes":[{"oldText":"a","newText":"b"}]}`}},
		{Function: openai.FunctionCall{Name: "create", Arguments: `{"path":"data/cfg.json","content":"{}"}`}},
		{Function: openai.FunctionCall{Name: "edit", Arguments: `{"path":"web/x.vue","version":"abc123","changes":[{"oldText":"div","newText":"span"}]}`}},
	}
	roots, err := workspaceRoots(cfg)
	if err != nil {
		t.Fatal(err)
	}
	plan := planBatchValidation(roots, calls, nil)
	if plan == nil {
		t.Fatal("expected a validation plan")
	}
	// The go package is one unit: call 0 defers to call 2, the last call
	// touching the package, which validates both files with complete filters.
	if got := plan[0]; len(got) != 0 {
		t.Fatalf("call 0 should defer its package to the later edit, got %v", got)
	}
	if got := plan[2]; len(got) != 2 || got[0] != "pkg/a.go" || got[1] != "pkg/b.go" {
		t.Fatalf("call 2 should validate both package files, got %v", got)
	}
	// Per-file units stay with their own call.
	if got := plan[3]; len(got) != 1 || got[0] != "data/cfg.json" {
		t.Fatalf("create keeps its own file, got %v", got)
	}
	if got := plan[4]; len(got) != 1 || got[0] != "web/x.vue" {
		t.Fatalf("vue file stays with its own call, got %v", got)
	}
	if _, exists := plan[1]; exists {
		t.Fatalf("non-mutation calls get no plan entry: %v", plan[1])
	}

	// A batch without edit/create calls yields no plan at all.
	if plan := planBatchValidation(roots, calls[1:2], nil); plan != nil {
		t.Fatalf("expected no plan without edit/create calls, got %v", plan)
	}

	// Conflict-skipped calls never execute, so they must neither own a unit
	// nor contribute paths to it: the executed earlier edit keeps its
	// validation instead of deferring to a call that will not run.
	plan = planBatchValidation(roots, []openai.ToolCall{
		{Function: openai.FunctionCall{Name: "edit", Arguments: `{"path":"pkg/a.go","version":"abc123","changes":[{"oldText":"a","newText":"b"}]}`}},
		{Function: openai.FunctionCall{Name: "edit", Arguments: `{"path":"pkg/b.go","version":"abc123","changes":[{"oldText":"a","newText":"b"}]}`}},
		{Function: openai.FunctionCall{Name: "edit", Arguments: `{"path":"pkg/c.go","version":"abc123","changes":[{"oldText":"a","newText":"b"}]}`}},
	}, map[int]bool{2: true})
	if got := plan[0]; len(got) != 0 {
		t.Fatalf("call 0 should defer its package to the later executing edit, got %v", got)
	}
	if got := plan[1]; len(got) != 2 || got[0] != "pkg/a.go" || got[1] != "pkg/b.go" {
		t.Fatalf("call 1 validates the package's executed files only, got %v", got)
	}
	for _, entry := range plan {
		for _, path := range entry {
			if path == "pkg/c.go" {
				t.Fatalf("conflict-skipped call's path must not be validated: %v", plan)
			}
		}
	}
	if _, exists := plan[2]; exists {
		t.Fatalf("conflict-skipped call must get no plan entry: %v", plan[2])
	}
}

func TestExecuteToolEditValidationFollowsBatchPlan(t *testing.T) {
	root := t.TempDir()
	original := []byte("{\"ok\":true}\n")
	writeToolTestFile(t, root, "config.json", string(original))
	app := NewApp()
	enabled := true
	cfg := ConfigState{Workspace: root, AutoValidationJSON: &enabled}
	args, err := json.Marshal(FileTextEdits{
		Path:    "config.json",
		Version: hashVersion(original),
		Changes: []TextChange{{OldText: "true", NewText: textPtr("")}},
	})
	if err != nil {
		t.Fatal(err)
	}

	// An empty planned set means this call's paths were absorbed by a later
	// call in the same batch: no validation runs here.
	emptyCtx := context.WithValue(context.Background(), batchValidationPathsContextKey{}, []string{})
	result := app.executeTool(emptyCtx, cfg, "s-1", "edit", args)
	if !result.OK {
		t.Fatalf("edit failed: %v", result.Error)
	}
	if edited, ok := result.Data.(MultiEditResult); !ok || edited.Validation != "" {
		t.Fatalf("expected deferred validation to be empty, got %#v", result.Data)
	}

	// With a planned set, the call validates exactly those paths (own or
	// absorbed from earlier same-unit calls).
	writeToolTestFile(t, root, "config.json", string(original))
	plannedCtx := context.WithValue(context.Background(), batchValidationPathsContextKey{}, []string{"config.json"})
	result = app.executeTool(plannedCtx, cfg, "s-1", "edit", args)
	if !result.OK {
		t.Fatalf("edit failed: %v", result.Error)
	}
	edited, ok := result.Data.(MultiEditResult)
	if !ok || edited.Validation == "" {
		t.Fatalf("expected validation report on the planned paths, got %#v", result.Data)
	}

	// Without a plan in ctx the call keeps the plain per-call behavior.
	writeToolTestFile(t, root, "config.json", string(original))
	result = app.executeTool(context.Background(), cfg, "s-1", "edit", args)
	if !result.OK {
		t.Fatalf("edit failed: %v", result.Error)
	}
	if edited, ok := result.Data.(MultiEditResult); !ok || edited.Validation == "" {
		t.Fatalf("expected unchanged per-call validation without a plan, got %#v", result.Data)
	}
}

func TestEditAutoValidationReturnsFailureWithoutUndoingWrite(t *testing.T) {
	root := t.TempDir()
	original := []byte("{\"ok\":true}\n")
	writeToolTestFile(t, root, "config.json", string(original))
	req := FileTextEdits{
		Path:    "config.json",
		Version: hashVersion(original),
		Changes: []TextChange{{OldText: "true", NewText: textPtr("")}},
	}
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
	if !strings.Contains(compact, "edit validation: 自动校验失败（文件已写入）") {
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
	if !strings.Contains(compact, `created="true"`) {
		t.Fatalf("expected compact create result to carry created=true, got %s", compact)
	}
	if !strings.Contains(compact, `dirs="a"`) {
		t.Fatalf("expected compact create result to carry createdDirs, got %s", compact)
	}
}

func TestCompactToolResultForModelRendersCalculateTag(t *testing.T) {
	result := toolResult{OK: true, Data: CalculateResult{Expression: `sqrt(144) + 2^5`, Value: 44, Text: "44"}}
	compact := compactToolResultForModel("calculate", result, "fallback")
	want := `<ally-calc expression="sqrt(144) + 2^5" value="44"/>`
	if compact != want {
		t.Fatalf("got %s, want %s", compact, want)
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
	if !strings.Contains(compact, `validation="自动校验失败（文件已写入）`) {
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

// 自动校验这一族只差三样：要装什么工具、往工作区放什么文件、期望"校验失败"出现
// 还是不出现。收成一张表之后，临时目录与 App 的样板只写一遍。
type autoValidationCase struct {
	name  string
	files map[string]string
	// needCmd 为空表示不需要外部工具；填了就先 LookPath，找不到跳过。
	needCmd string
	// needPython 走 findPythonCommand（它认的 python 不止 PATH 上那一个）。
	needPython bool
	// toggle 是要打开的那一档：空表示用默认配置（不显式开启）。
	toggle string
	// wantFail 为真时期望输出里出现"自动校验失败"，为假时期望它不出现。
	wantFail bool
	wantText []string
}

func (c autoValidationCase) configState(root string, enabled *bool) ConfigState {
	cfg := ConfigState{Workspace: root}
	switch c.toggle {
	case "python":
		cfg.AutoValidationPython = enabled
	case "javascript":
		cfg.AutoValidationJavaScript = enabled
	case "go":
		cfg.AutoValidationGo = enabled
	case "java":
		cfg.AutoValidationJava = enabled
	case "json":
		cfg.AutoValidationJSON = enabled
	}
	return cfg
}

func TestAutoValidationChecksChangedFiles(t *testing.T) {
	cases := []autoValidationCase{
		{name: "python syntax", needPython: true, toggle: "python",
			files:    map[string]string{"broken.py": "def broken(:\n    pass\n"},
			wantFail: true, wantText: []string{"broken.py"}},
		{name: "javascript syntax", needCmd: "node", toggle: "javascript",
			files:    map[string]string{"broken.js": "const = 1;\n"},
			wantFail: true, wantText: []string{"broken.js"}},
		{name: "go vet", needCmd: "go", toggle: "go",
			files: map[string]string{
				"go.mod":  "module example.com/validation\n\ngo 1.23\n",
				"main.go": "package main\n\nfunc main() { missing() }\n",
			},
			wantFail: true, wantText: []string{"Go vet"}},
		{name: "java dependency errors are not our problem", needCmd: "javac", toggle: "java",
			files: map[string]string{
				"Dep.java": "import non-existent.pkg.Thing;\n\npublic class Dep {\n    Thing t;\n}\n",
			}},
		{name: "jsx is not node --check material", needCmd: "node",
			files: map[string]string{"component.jsx": "export const App = () => <div />;\n"}},
		{name: "jsonc is skipped", toggle: "json",
			files: map[string]string{"tsconfig.json": "{\n  // comments are legal JSONC\n  \"compilerOptions\": {}\n}\n"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.needCmd != "" {
				if _, err := exec.LookPath(tc.needCmd); err != nil {
					t.Skipf("%s is unavailable", tc.needCmd)
				}
			}
			root := t.TempDir()
			if tc.needPython {
				if _, _, ok := findPythonCommand(root); !ok {
					t.Skip("python is unavailable")
				}
			}
			for name, body := range tc.files {
				writeToolTestFile(t, root, name, body)
			}
			enabled := true
			changed := make([]string, 0, len(tc.files))
			for name := range tc.files {
				changed = append(changed, name)
			}
			got := NewApp().validateChangedFiles(context.Background(), tc.configState(root, &enabled), changed)
			if tc.wantFail {
				if !strings.Contains(got, "自动校验失败") {
					t.Fatalf("expected a validation failure, got %q", got)
				}
				for _, want := range tc.wantText {
					if !strings.Contains(got, want) {
						t.Fatalf("expected the failure to name %q, got %q", want, got)
					}
				}
				return
			}
			if strings.Contains(got, "自动校验失败") {
				t.Fatalf("expected no validation failure, got %q", got)
			}
		})
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

	result, err := app.deletePathsWithConfig(ConfigState{Workspace: root}, []string{"link.txt"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Paths) != 1 || result.DeletedCount != 1 {
		t.Fatalf("expected exactly one deleted path, got %#v", result)
	}
	item := result.Paths[0]
	if item.Kind != "symlink" || !item.WasSymlink || item.RemovedFiles != 1 {
		t.Fatalf("unexpected symlink delete result: %#v", item)
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

	// 本用例断言围栏在位：判据归内核时越界由内核拒，见 orch_sandbox_writes_test.go。
	pinBoundaryOwnership(t, false)
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

func TestRenamePathRenamesFileAndDirectory(t *testing.T) {
	root := t.TempDir()
	app := NewApp()
	cfg := ConfigState{Workspace: root}
	if _, err := app.createFileWithConfig(cfg, CreateFileRequest{Path: "docs/guide.md", Content: "hello\n"}); err != nil {
		t.Fatal(err)
	}

	// 文件改名：留在原目录，内容跟着走
	renamed, err := app.renamePathWithConfig(cfg, RenamePathRequest{Path: "docs/guide.md", NewName: "readme.md"})
	if err != nil {
		t.Fatal(err)
	}
	if renamed != "docs/readme.md" {
		t.Fatalf("expected docs/readme.md, got %q", renamed)
	}
	if got, err := os.ReadFile(filepath.Join(root, "docs", "readme.md")); err != nil || string(got) != "hello\n" {
		t.Fatalf("expected content to survive the rename, got %q err=%v", string(got), err)
	}
	if _, err := os.Lstat(filepath.Join(root, "docs", "guide.md")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected the old name to be gone, lstat err=%v", err)
	}

	// 目录改名：整棵子树跟着走，返回值仍是工作区相对路径
	renamed, err = app.renamePathWithConfig(cfg, RenamePathRequest{Path: "docs", NewName: "manual"})
	if err != nil {
		t.Fatal(err)
	}
	if renamed != "manual" {
		t.Fatalf("expected manual, got %q", renamed)
	}
	if got, err := os.ReadFile(filepath.Join(root, "manual", "readme.md")); err != nil || string(got) != "hello\n" {
		t.Fatalf("expected renamed directory to keep its children, got %q err=%v", string(got), err)
	}
}

func TestRenamePathSameNameIsNoOp(t *testing.T) {
	root := t.TempDir()
	app := NewApp()
	cfg := ConfigState{Workspace: root}
	if _, err := app.createFileWithConfig(cfg, CreateFileRequest{Path: "a.txt", Content: "a\n"}); err != nil {
		t.Fatal(err)
	}

	renamed, err := app.renamePathWithConfig(cfg, RenamePathRequest{Path: "a.txt", NewName: "a.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if renamed != "a.txt" {
		t.Fatalf("expected a.txt, got %q", renamed)
	}
	if got, err := os.ReadFile(filepath.Join(root, "a.txt")); err != nil || string(got) != "a\n" {
		t.Fatalf("expected the file to stay untouched, got %q err=%v", string(got), err)
	}
}

// 只改大小写：Windows/macOS 上 Lstat 到的目标就是源文件本身，不能被“目标已存在”
// 挡掉，否则文件名大小写永远改不了。
func TestRenamePathChangesCaseOnly(t *testing.T) {
	root := t.TempDir()
	app := NewApp()
	cfg := ConfigState{Workspace: root}
	if _, err := app.createFileWithConfig(cfg, CreateFileRequest{Path: "notes.txt", Content: "x\n"}); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}

	renamed, err := app.renamePathWithConfig(cfg, RenamePathRequest{Path: "notes.txt", NewName: "NOTES.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if renamed != "NOTES.txt" {
		t.Fatalf("expected NOTES.txt, got %q", renamed)
	}
	renamed, err = app.renamePathWithConfig(cfg, RenamePathRequest{Path: "docs", NewName: "DOCS"})
	if err != nil {
		t.Fatal(err)
	}
	if renamed != "DOCS" {
		t.Fatalf("expected DOCS, got %q", renamed)
	}
	// 名字按磁盘上的实际拼写断言：大小写不敏感的文件系统用旧名字查路径也会命中，
	// 只有 ReadDir 返回的名字才是真相（它按文件名排序）
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	if got := strings.Join(names, ","); got != "DOCS,NOTES.txt" {
		t.Fatalf("expected exactly DOCS,NOTES.txt, got %s", got)
	}
}

func TestRenamePathRefusesExistingTarget(t *testing.T) {
	root := t.TempDir()
	app := NewApp()
	cfg := ConfigState{Workspace: root}
	for _, name := range []string{"a.txt", "b.txt"} {
		if _, err := app.createFileWithConfig(cfg, CreateFileRequest{Path: name, Content: name}); err != nil {
			t.Fatal(err)
		}
	}

	_, err := app.renamePathWithConfig(cfg, RenamePathRequest{Path: "a.txt", NewName: "b.txt"})
	if err == nil {
		t.Fatal("expected renaming onto an existing file to fail")
	}
	if code := toolErrorCode(err); code != "E_EXISTS" {
		t.Fatalf("expected E_EXISTS, got %q (%v)", code, err)
	}
	// 失败不能是“先删后改”：两个文件都必须完好
	for _, name := range []string{"a.txt", "b.txt"} {
		if got, readErr := os.ReadFile(filepath.Join(root, name)); readErr != nil || string(got) != name {
			t.Fatalf("expected %s to keep its content, got %q err=%v", name, string(got), readErr)
		}
	}
}

func TestRenamePathRejectsBadNamesAndTargets(t *testing.T) {
	root := t.TempDir()
	app := NewApp()
	cfg := ConfigState{Workspace: root}
	if _, err := app.createFileWithConfig(cfg, CreateFileRequest{Path: "a.txt", Content: "a\n"}); err != nil {
		t.Fatal(err)
	}
	if _, err := app.createFileWithConfig(cfg, CreateFileRequest{Path: "hooked.txt", Content: "h\n"}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name    string
		req     RenamePathRequest
		wantErr string
	}{
		{"空名称", RenamePathRequest{Path: "a.txt", NewName: "  "}, "E_BAD_PATH"},
		{"相对段", RenamePathRequest{Path: "a.txt", NewName: ".."}, "E_BAD_PATH"},
		{"带路径分隔符", RenamePathRequest{Path: "a.txt", NewName: "sub/a.txt"}, "E_BAD_PATH"},
		{"源路径穿越", RenamePathRequest{Path: "../a.txt", NewName: "x.txt"}, "E_BAD_PATH"},
		{"源为绝对路径", RenamePathRequest{Path: "/etc/passwd", NewName: "x.txt"}, "E_BAD_PATH"},
		{"源不存在", RenamePathRequest{Path: "missing.txt", NewName: "x.txt"}, "E_PATH_NOT_FOUND"},
		{"新名字是 VCS 元数据", RenamePathRequest{Path: "hooked.txt", NewName: ".git"}, "E_PROTECTED_PATH"},
		{"源是 VCS 元数据", RenamePathRequest{Path: ".git", NewName: "gitdata"}, "E_PROTECTED_PATH"},
	}
	for _, tc := range cases {
		_, err := app.renamePathWithConfig(cfg, tc.req)
		if err == nil {
			t.Fatalf("%s：预期失败", tc.name)
		}
		if code := toolErrorCode(err); code != tc.wantErr {
			t.Fatalf("%s：预期 %s，实际 %q（%v）", tc.name, tc.wantErr, code, err)
		}
	}
	// 被拒绝的调用不能留下任何痕迹
	for _, name := range []string{"a.txt", "hooked.txt", ".git"} {
		if _, err := os.Lstat(filepath.Join(root, name)); err != nil {
			t.Fatalf("expected %s to remain after rejected renames, lstat err=%v", name, err)
		}
	}
}

func TestRenamePathRenamesSymlinkItself(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires privileges on many Windows environments")
	}
	root := t.TempDir()
	outside := t.TempDir()
	target := filepath.Join(outside, "target.txt")
	if err := os.WriteFile(target, []byte("keep\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(root, "link.txt")); err != nil {
		t.Fatal(err)
	}
	app := NewApp()

	renamed, err := app.renamePathWithConfig(ConfigState{Workspace: root}, RenamePathRequest{Path: "link.txt", NewName: "alias.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if renamed != "alias.txt" {
		t.Fatalf("expected alias.txt, got %q", renamed)
	}
	info, err := os.Lstat(filepath.Join(root, "alias.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("expected the symlink itself to be renamed, mode=%v", info.Mode())
	}
	if got, err := os.ReadFile(target); err != nil || string(got) != "keep\n" {
		t.Fatalf("expected the symlink target to remain, got %q err=%v", string(got), err)
	}
}

func TestRenamePathRejectsSymlinkParentOutsideWorkspace(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires privileges on many Windows environments")
	}
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("s\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	app := NewApp()

	// 本用例断言围栏在位：判据归内核时越界由内核拒。
	pinBoundaryOwnership(t, false)
	_, err := app.renamePathWithConfig(ConfigState{Workspace: root}, RenamePathRequest{Path: "escape/secret.txt", NewName: "renamed.txt"})
	if err == nil {
		t.Fatal("expected rename through a symlinked parent outside the workspace to fail")
	}
	if code := toolErrorCode(err); code != "E_PATH_OUTSIDE" {
		t.Fatalf("expected E_PATH_OUTSIDE, got %q (%v)", code, err)
	}
	if _, statErr := os.Stat(filepath.Join(outside, "renamed.txt")); !errors.Is(statErr, os.ErrNotExist) {
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

// TestRunCommandNoKeywordBlocking 锁定“无关键词拦截”：旧白名单里的命令
// 不再被 E_LONG_RUNNING_COMMAND 拒绝，而是正常执行并快速返回。
func TestRunCommandNoKeywordBlocking(t *testing.T) {
	root := t.TempDir()
	app := NewApp()
	for _, cmd := range []string{
		"npm run dev-setup --if-present", // 子串“npm run dev”曾误拦截
		"echo flask run",                 // 曾命中“flask run”
		"echo nodemon",                   // 裸名曾命中
	} {
		args, err := json.Marshal(CommandRequest{Command: cmd, Timeout: 10})
		if err != nil {
			t.Fatal(err)
		}
		result := app.executeTool(context.Background(), ConfigState{Workspace: root}, "session-1", "command", args)
		if !result.OK {
			t.Errorf("command %q must not be pre-blocked, got %#v", cmd, result)
			continue
		}
		data, ok := result.Data.(CommandResult)
		if !ok || data.PromotedToService {
			t.Errorf("command %q should complete normally, got %#v", cmd, result.Data)
		}
	}
}

// commandSafetyCase 是命令围栏的一条用例。这张表把原来散在十九个函数里的判定收在
// 一处：同一个入口、同一份工作区，只是命令与期望不同，读的人能一眼看全"什么放行、
// 什么拦、拦成哪个错"。
type commandSafetyCase struct {
	name string
	// onlyOS / skipOS 圈定平台：MSYS2 盘符与 cmd.exe 只在 Windows 有意义，
	// 符号链接在多数 Windows 环境上建不出来。
	onlyOS string
	skipOS string
	// command 是字面命令；需要按临时目录拼路径时改填 build。
	command string
	build   func(t *testing.T, workspace, outside string) string
	cwd     string
	// wantCode 为空表示期望放行。
	wantCode string
	// wantText 是错误文本必须同时出现的片段（围栏每条拒绝都要说清原因与指路）。
	wantText []string
}

// TestCommandSafetyFence 是命令围栏的全表：放行的一类（读区外、命令里的关键词只是
// 数据、heredoc 正文、动态目标），拦截的一类（显式删除、高危语义、改动区外已存在的
// 目标、工作区软链穿透、相对路径按 cwd 解析后越界）。
func TestCommandSafetyFence(t *testing.T) {
	cases := []commandSafetyCase{
		{name: "cmd /c option is not a drive path", onlyOS: "windows", command: `cmd.exe /c ver`},
		{name: "read-only outside inspection", onlyOS: "windows", command: `(ls -la /d/coding/python/ | grep xx)`},
		{name: "git bash outside read with /dev/null", onlyOS: "windows", command: `(ls -la /c/Users/DELL/.agents/ 2>/dev/null && echo "---FOUND---" || echo "---NOT FOUND---")`},
		{name: "git bash find outside with /dev/null", onlyOS: "windows", command: `(find /c/Users/DELL/ -maxdepth 5 -name "SKILL.md" -path "ai-writing" 2>/dev/null | head -5)`},
		{name: "quoted git pretty format keeps email brackets", command: `git status --short && git log -1 --pretty=format:'%h %an <%ae> %s'`},
		{name: "risk and delete words as data", command: `grep -R "rm" docs`},
		{name: "remove-item printed as data", command: `echo remove-item`},
		{name: "shutdown inside a git log grep", command: `git log --grep=shutdown`},
		{name: "reboot printed as data", command: `echo "reboot"`},
		{name: "mkfs searched as text", command: `rg mkfs docs`},
		{name: "dd reading a file is fine", command: `dd if=input.bin bs=1 count=16`},
		{name: "chmod 064 is not a wipe", command: `chmod 064 file.txt`},
		{name: "dynamic redirection target stays permissive", command: `printf changed > "$HOME/existing.txt"`},
		{name: "quoted heredoc body with a gt sign", command: "python - <<'EOF'\nif v > 1:\n    pass\nEOF\n"},
		{name: "quoted heredoc body with rm", command: "python - <<'EOF'\nrm -rf /tmp/x\nEOF\n"},
		{name: "quoted heredoc body with substitution text", command: "python - <<'EOF'\nprint('$(rm -rf /tmp/x)')\nEOF\n"},
		{name: "unquoted heredoc body with a gt sign", command: "python - <<EOF\nif v > 1:\n    pass\nEOF\n"},
		{name: "raw deletion is routed to the delete tool", command: `rm generated.txt`, wantCode: "E_COMMAND_BLOCKED"},
		{name: "nested raw deletion", command: `bash -c "rm generated.txt"`, wantCode: "E_COMMAND_BLOCKED"},
		{name: "deletion through xargs", command: `echo generated.txt | xargs rm`, wantCode: "E_COMMAND_BLOCKED"},
		{name: "deletion inside command substitution", command: `echo $(rm generated.txt)`, wantCode: "E_COMMAND_BLOCKED"},
		{name: "shutdown", command: `shutdown -h now`, wantCode: "E_COMMAND_BLOCKED"},
		{name: "dd writing a block device", command: `dd if=image.iso of=/dev/sda`, wantCode: "E_COMMAND_BLOCKED"},
		{name: "chmod wiping every bit", command: `chmod 000 secrets.txt`, wantCode: "E_COMMAND_BLOCKED"},
		{name: "mixed managed and raw deletion", command: `git rm --cached tracked.txt; rm generated.txt`, wantCode: "E_COMMAND_BLOCKED"},
		{name: "substitution inside an unquoted heredoc still executes", command: "python - <<EOF\n$(rm generated.txt)\nEOF\n", wantCode: "E_COMMAND_BLOCKED"},

		{name: "outside copy source is readable", build: func(t *testing.T, workspace, outside string) string {
			source := writeOutsideFile(t, outside, "source.txt")
			return fmt.Sprintf(`cp %q copied.txt`, filepath.ToSlash(source))
		}},
		{name: "python reading an outside file", build: func(t *testing.T, workspace, outside string) string {
			source := writeOutsideFile(t, outside, "source.txt")
			return fmt.Sprintf(`python -c "print(open(%q).read())"`, filepath.ToSlash(source))
		}},
		{name: "unzip listing an outside archive", build: func(t *testing.T, workspace, outside string) string {
			archive := writeOutsideFile(t, outside, "archive.zip")
			return fmt.Sprintf(`unzip -l %q`, filepath.ToSlash(archive))
		}},
		{name: "unzip extracting into the workspace", build: func(t *testing.T, workspace, outside string) string {
			archive := writeOutsideFile(t, outside, "archive.zip")
			return fmt.Sprintf(`unzip %q -d .`, filepath.ToSlash(archive))
		}},
		{name: "existing outside copy destination is blocked", build: func(t *testing.T, workspace, outside string) string {
			writeOutsideFile(t, outside, "destination.txt")
			return fmt.Sprintf(`cp copied.txt %q`, filepath.ToSlash(filepath.Join(outside, "destination.txt")))
		}, wantCode: "E_PATH_OUTSIDE"},
		{name: "touch creating a new outside path", build: func(t *testing.T, workspace, outside string) string {
			return fmt.Sprintf(`touch %q`, filepath.ToSlash(filepath.Join(outside, "new-touch.txt")))
		}},
		{name: "redirect creating a new outside file", build: func(t *testing.T, workspace, outside string) string {
			return fmt.Sprintf(`printf created > %q`, filepath.ToSlash(filepath.Join(outside, "new-redirect.txt")))
		}},
		{name: "touch an existing outside file", build: func(t *testing.T, workspace, outside string) string {
			return fmt.Sprintf(`touch %q`, filepath.ToSlash(writeOutsideFile(t, outside, "existing.txt")))
		}, wantCode: "E_PATH_OUTSIDE", wantText: []string{"安全围栏已拦截", "工作区外", "检测到的目标", "允许的操作", "禁止的操作"}},
		{name: "overwrite an existing outside file", build: func(t *testing.T, workspace, outside string) string {
			return fmt.Sprintf(`printf changed > %q`, filepath.ToSlash(writeOutsideFile(t, outside, "existing.txt")))
		}, wantCode: "E_PATH_OUTSIDE", wantText: []string{"安全围栏已拦截", "工作区外", "检测到的目标", "允许的操作", "禁止的操作"}},
		{name: "append to an existing outside file", build: func(t *testing.T, workspace, outside string) string {
			return fmt.Sprintf(`printf changed >> %q`, filepath.ToSlash(writeOutsideFile(t, outside, "existing.txt")))
		}, wantCode: "E_PATH_OUTSIDE", wantText: []string{"安全围栏已拦截", "工作区外", "检测到的目标", "允许的操作", "禁止的操作"}},
		{name: "literal outside target stays blocked", build: func(t *testing.T, workspace, outside string) string {
			return fmt.Sprintf(`printf changed > %q`, filepath.ToSlash(writeOutsideFile(t, outside, "existing.txt")))
		}, wantCode: "E_PATH_OUTSIDE"},
		{name: "workspace redirect while reading outside", build: func(t *testing.T, workspace, outside string) string {
			source := writeOutsideFile(t, outside, "source.txt")
			if runtime.GOOS == "windows" {
				return fmt.Sprintf(`type %q > result.txt`, filepath.ToSlash(source))
			}
			return fmt.Sprintf(`cat %q > result.txt`, filepath.ToSlash(source))
		}},
		{name: "msys2 drive path mutation", onlyOS: "windows", build: func(t *testing.T, workspace, outside string) string {
			target := writeOutsideFile(t, outside, "existing.txt")
			volume := filepath.VolumeName(target)
			msys := "/" + strings.ToLower(strings.TrimSuffix(volume, ":")) + filepath.ToSlash(strings.TrimPrefix(target, volume))
			return `touch ` + msys
		}, wantCode: "E_PATH_OUTSIDE"},
	}

	for _, tc := range cases {
		if tc.onlyOS != "" && runtime.GOOS != tc.onlyOS {
			continue
		}
		if tc.skipOS != "" && runtime.GOOS == tc.skipOS {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			workspace := t.TempDir()
			outside := t.TempDir()
			command := tc.command
			if tc.build != nil {
				command = tc.build(t, workspace, outside)
			}
			err := checkCommandSafety(CommandRequest{Command: command, Cwd: tc.cwd}, []string{workspace})
			if tc.wantCode == "" {
				if err != nil {
					t.Fatalf("command %q should be allowed: %v", command, err)
				}
				return
			}
			if code := toolErrorCode(err); code != tc.wantCode {
				t.Fatalf("command %q should be blocked with %s, got %v", command, tc.wantCode, code)
			}
			for _, want := range tc.wantText {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("refusal for %q must explain %q, got %v", command, want, err)
				}
			}
		})
	}
}

// 相对路径按 cwd 解析后越界、以及工作区软链穿透，这两条要各自准备目录/链接，
// 单独留在表外：它们考的是"解析基准"，不是命令串本身。
func TestCommandSafetyResolvesTargetsAgainstCwdAndSymlinks(t *testing.T) {
	workspace := t.TempDir()
	outside := t.TempDir()

	cwd := filepath.Join(workspace, "build")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	target := writeOutsideFile(t, outside, "existing.txt")
	relative, err := filepath.Rel(cwd, target)
	if err != nil {
		t.Fatal(err)
	}
	if code := toolErrorCode(checkCommandSafety(CommandRequest{
		Command: fmt.Sprintf(`touch %q`, filepath.ToSlash(relative)),
		Cwd:     "build",
	}, []string{workspace})); code != "E_PATH_OUTSIDE" {
		t.Fatalf("relative mutation must resolve from cwd and be blocked, got %q", code)
	}

	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires privileges on many Windows environments")
	}
	if err := os.Symlink(outside, filepath.Join(workspace, "link")); err != nil {
		t.Fatal(err)
	}
	writeOutsideFile(t, outside, "existing.txt")
	for _, command := range []string{`touch link/existing.txt`, `printf created > link/new.txt`} {
		if code := toolErrorCode(checkCommandSafety(CommandRequest{Command: command}, []string{workspace})); code != "E_PATH_OUTSIDE" {
			t.Fatalf("workspace symlink mutation must be blocked for %q, got %q", command, code)
		}
	}
}

// writeOutsideFile 在工作区之外建一个文件并返回它的路径：越界判定只认"已经存在"
// 的区外目标，所以这些用例必须真的把文件放下去。
func writeOutsideFile(t *testing.T, outside string, name string) string {
	t.Helper()
	path := filepath.Join(outside, name)
	if err := os.WriteFile(path, []byte("keep\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
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
	got, err := app.webFetchToolWithConfig(context.Background(), ConfigState{AllowPrivateNetwork: boolPtr(true)}, WebFetchRequest{URL: server.URL, MaxChars: 1000})
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

	// 本用例断言围栏在位：判据归内核时越界 cwd 交给内核（可读即能跑）。
	pinBoundaryOwnership(t, false)
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

func TestSessionReadCacheReturnsMetadataWithoutDuplicateContent(t *testing.T) {
	dir := t.TempDir()
	writeToolTestFile(t, dir, "sample.txt", "one\ntwo\n")
	app := NewApp()
	cache := newSessionReadCache()
	ctx := context.WithValue(context.Background(), sessionReadCacheContextKey{}, cache)
	args := []byte(`{"files":[{"path":"sample.txt"}]}`)

	first := app.executeTool(ctx, ConfigState{Workspace: dir}, "session-1", "read", args)
	if !first.OK {
		t.Fatalf("first read failed: %#v", first)
	}
	firstData := first.Data.(*BatchReadResult)
	if len(firstData.Files) != 1 || firstData.Files[0].Content == "" || firstData.Files[0].Reused {
		t.Fatalf("expected complete first read, got %#v", firstData)
	}
	// The stored cache entry itself is content-free; it holds only the content hash.
	if len(cache.entries) != 1 {
		t.Fatalf("cache entries after first read = %d, want 1", len(cache.entries))
	}
	for _, h := range cache.entries {
		if h == "" {
			t.Fatalf("cached entry must hold a content hash")
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
	if !strings.Contains(secondModel, "Content omitted") || !strings.Contains(secondModel, ` reused`) || !strings.Contains(secondModel, `version="`) {
		t.Fatalf("expected reused compact read to explain the omission, got: %s", secondModel)
	}

	// In the persistent freshness-checked cache, unchanged files continue to return receipts.
	third := app.executeTool(ctx, ConfigState{Workspace: dir}, "session-1", "read", args)
	thirdData := third.Data.(*BatchReadResult)
	if !third.OK || len(thirdData.Files) != 1 || !thirdData.Files[0].Reused || thirdData.Files[0].Content != "" {
		t.Fatalf("expected reused read when file is unchanged on disk, got %#v", thirdData)
	}

	// Range-awareness: reading a specific line range produces a cache miss and returns full content for that range.
	rangeArgs := []byte(`{"files":[{"path":"sample.txt","startLine":1,"endLine":1}]}`)
	rangeRes := app.executeTool(ctx, ConfigState{Workspace: dir}, "session-1", "read", rangeArgs)
	rangeData := rangeRes.Data.(*BatchReadResult)
	if !rangeRes.OK || len(rangeData.Files) != 1 || rangeData.Files[0].Reused || rangeData.Files[0].Content == "" {
		t.Fatalf("expected full content on range read miss, got %#v", rangeData)
	}

	// Repeated range read hits the range cache!
	rangeRes2 := app.executeTool(ctx, ConfigState{Workspace: dir}, "session-1", "read", rangeArgs)
	rangeData2 := rangeRes2.Data.(*BatchReadResult)
	if !rangeRes2.OK || len(rangeData2.Files) != 1 || !rangeData2.Files[0].Reused {
		t.Fatalf("expected reused content on repeated range read, got %#v", rangeData2)
	}

	// Modifying the file on disk busts the cache for the full read because full
	// content changed. No write path invalidates the cache (see sessionReadCache):
	// this hash comparison is the only thing keeping a rewritten file from being
	// answered with its old payload, so it must keep working.
	writeToolTestFile(t, dir, "sample.txt", "one\ntwo\nthree\n")
	fourth := app.executeTool(ctx, ConfigState{Workspace: dir}, "session-1", "read", args)
	fourthData := fourth.Data.(*BatchReadResult)
	if !fourth.OK || len(fourthData.Files) != 1 || fourthData.Files[0].Reused || fourthData.Files[0].Content == "" {
		t.Fatalf("expected full read after disk file modification, got %#v", fourthData)
	}

	// Crucial feature: reading lines 1..1 is STILL cached as reused because line 1 did not change,
	// even though the rest of the file was modified!
	rangeRes3 := app.executeTool(ctx, ConfigState{Workspace: dir}, "session-1", "read", rangeArgs)
	rangeData3 := rangeRes3.Data.(*BatchReadResult)
	if !rangeRes3.OK || len(rangeData3.Files) != 1 || !rangeData3.Files[0].Reused {
		t.Fatalf("expected reused content for unchanged range 1..1 even after file was edited elsewhere, got %#v", rangeData3)
	}

	// Invalidation clears the entire cache.
	cache.invalidate()
	if len(cache.entries) != 0 {
		t.Fatalf("cache entries after invalidation = %d, want 0", len(cache.entries))
	}
	fifth := app.executeTool(ctx, ConfigState{Workspace: dir}, "session-1", "read", args)
	fifthData := fifth.Data.(*BatchReadResult)
	if !fifth.OK || len(fifthData.Files) != 1 || fifthData.Files[0].Reused || fifthData.Files[0].Content == "" {
		t.Fatalf("expected full read after invalidation, got %#v", fifthData)
	}
}

func TestSessionReadCacheEvictsEntriesUnderPressure(t *testing.T) {
	cache := newSessionReadCache()
	// Fill past the entry budget; every insertion is a distinct key.
	for i := 0; i < sessionReadCacheMaxEntries+8; i++ {
		cache.put(fmt.Sprintf("key-%d", i), "hash-val")
	}
	if got := len(cache.entries); got > sessionReadCacheMaxEntries {
		t.Fatalf("cache entries = %d, want <= %d", got, sessionReadCacheMaxEntries)
	}
	// Re-storing an existing key must not grow the cache.
	cache.put("key-0", "hash-val-updated")
	if got := len(cache.entries); got > sessionReadCacheMaxEntries {
		t.Fatalf("cache entries after re-store = %d, want <= %d", got, sessionReadCacheMaxEntries)
	}
}

func TestBatchReadMixedWithMissingFilesDoesNotCorruptCacheKeys(t *testing.T) {
	app := NewApp()
	dir := t.TempDir()
	app.config.Workspace = dir

	fileA := filepath.Join(dir, "a.txt")
	fileB := filepath.Join(dir, "b.txt")
	if err := os.WriteFile(fileA, []byte("line1\nline2\nline3\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fileB, []byte("b1\nb2\nb3\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cache := newSessionReadCache()
	ctx := context.WithValue(context.Background(), sessionReadCacheContextKey{}, cache)

	// Batch read: a.txt (1..1), missing.txt (2..2), b.txt (3..3)
	args := []byte(`{"files":[` +
		`{"path":"a.txt","startLine":1,"endLine":1},` +
		`{"path":"missing.txt","startLine":2,"endLine":2},` +
		`{"path":"b.txt","startLine":3,"endLine":3}` +
		`]}`)
	res1 := app.executeTool(ctx, ConfigState{Workspace: dir}, "session-1", "read", args)
	if !res1.OK {
		t.Fatalf("executeTool read failed: %#v", res1)
	}

	// Now re-read b.txt (3..3) alone: it MUST hit the cache and be reused with correct key!
	bArgs := []byte(`{"files":[{"path":"b.txt","startLine":3,"endLine":3}]}`)
	res2 := app.executeTool(ctx, ConfigState{Workspace: dir}, "session-1", "read", bArgs)
	if !res2.OK {
		t.Fatalf("executeTool read b.txt failed: %#v", res2)
	}
	bData := res2.Data.(*BatchReadResult)
	if len(bData.Files) != 1 || !bData.Files[0].Reused {
		t.Fatalf("expected b.txt (3..3) to be reused despite missing.txt in earlier batch, got %#v", bData)
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

	result := app.executeTool(context.Background(), cfg, "session-1", "edit", []byte(`{"path": "sample.txt", "version": "000000000000",
		"edits": [
			{"oldString": "alpha", "newString": "ALPHA"},
			{"oldString": "gamma", "newString": "GAMMA"}
		]}`))
	if result.OK || !strings.Contains(result.Error, "edits") {
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

func TestExecuteToolRejectsUnknownArguments(t *testing.T) {
	dir := t.TempDir()
	original := []byte("alpha\nbeta\ngamma\n")
	if err := os.WriteFile(filepath.Join(dir, "sample.txt"), original, 0o600); err != nil {
		t.Fatal(err)
	}
	app := NewApp()
	cfg := ConfigState{Workspace: dir}

	// A stray key is a model bug: it used to run on a default and come back
	// with a warning, which hid the mistake. It now fails, names the key and
	// lists what the tool does accept.
	result := app.executeTool(context.Background(), cfg, "session-1", "list_files", []byte(`{"path":".","recursiveTypo":true}`))
	if result.OK || result.ErrorCode != "E_BAD_ARGS" {
		t.Fatalf("unknown arguments must fail with E_BAD_ARGS, got %#v", result)
	}
	if !strings.Contains(result.Error, "recursiveTypo") || !strings.Contains(result.Error, "path") {
		t.Fatalf("the rejection must name the stray key and the accepted ones, got %q", result.Error)
	}

	// The same holds one level down: without the schema walk, a typo inside a
	// nested object was invisible to the struct-tag check.
	nested := app.executeTool(context.Background(), cfg, "session-1", "read", []byte(`{"files":[{"path":"sample.txt","tailLine":5}]}`))
	if nested.OK || !strings.Contains(nested.Error, "tailLine") {
		t.Fatalf("a nested unknown key must be rejected by name, got %#v", nested)
	}

	// Auto-repair still runs before validation, so a stringified array whose
	// contents are valid keeps working with its warning.
	repaired := app.executeTool(context.Background(), cfg, "session-1", "read", []byte(`{"files":"[{\"path\":\"sample.txt\"}]"}`))
	if !repaired.OK {
		t.Fatalf("a repaired payload must pass the gate, got %q", repaired.Error)
	}
	if len(repaired.Warnings) == 0 || !strings.Contains(repaired.Warnings[0], "files") {
		t.Fatalf("expected the repair warning to survive, got %#v", repaired.Warnings)
	}

	// Missing required parameters still fail loudly, and the report covers
	// both problems in one round instead of one per call.
	missing := app.executeTool(context.Background(), cfg, "session-1", "wait", []byte(`{"seconds":1,"whyTypo":"x"}`))
	if missing.OK || missing.ErrorCode != "E_BAD_ARGS" {
		t.Fatalf("missing required reason must still fail, got %#v", missing)
	}
	if !strings.Contains(missing.Error, "reason") || !strings.Contains(missing.Error, "whyTypo") {
		t.Fatalf("the rejection must report the missing and the stray key together, got %q", missing.Error)
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

	result := app.executeTool(context.Background(), cfg, "session-1", "edit", []byte(fmt.Sprintf(`{"path": "sample.txt",
		"version": %q,
		"changes": [
			{"oldText": "alpha", "newText": "ALPHA"},
			{"oldText": "gamma", "newText": "GAMMA"}
		]}`, strings.ToUpper(hashVersion(original)))))
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
	args := fmt.Sprintf(`{"path":"sample.txt","version":%q,"changes":[{"lineRange":"2-2","newText":"TWO\ninserted"},{"lineRange":"5-5","newText":"FIVE"}]}`, hashVersion(original))
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

// A change naming two sources, or pairing replaceAll with lineRange, is refused
// before anything is written: the runtime must never pick a source silently.
func TestExecuteToolRejectsChangeWithTwoSourcesOrReplaceAllBesideLineRange(t *testing.T) {
	cases := map[string]string{
		"two sources":                 `{"oldText":"alpha","lineRange":"2-2","newText":"ALPHA"}`,
		"replaceAll beside lineRange": `{"lineRange":"2-2","newText":"ALPHA","replaceAll":true}`,
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			original := []byte("alpha\nbeta\n")
			if err := os.WriteFile(filepath.Join(dir, "sample.txt"), original, 0o600); err != nil {
				t.Fatal(err)
			}
			args := fmt.Sprintf(`{"path":"sample.txt","version":%q,"changes":[%s]}`, hashVersion(original), change)
			result := NewApp().executeTool(context.Background(), ConfigState{Workspace: dir}, "session-1", "edit", []byte(args))
			if result.OK {
				t.Fatalf("the call must be refused, got %#v", result)
			}
			want := "replaceAll"
			if name == "two sources" {
				want = "exactly one allowed shape"
			}
			if !strings.Contains(result.Error, want) {
				t.Fatalf("rejection %q must mention %q", result.Error, want)
			}
			got, err := os.ReadFile(filepath.Join(dir, "sample.txt"))
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != string(original) {
				t.Fatalf("a refused edit changed the file: %q", got)
			}
		})
	}
}

func TestExecuteToolEditsParallelCallsPerFile(t *testing.T) {
	dir := t.TempDir()
	a, b := []byte("alpha\n"), []byte("beta\n")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), a, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.txt"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	// One flat edit call per file, sent as parallel tool calls in one batch.
	calls := []openai.ToolCall{
		{Function: openai.FunctionCall{Name: "edit", Arguments: fmt.Sprintf(`{"path":"a.txt","version":%q,"changes":[{"oldText":"alpha","newText":"ALPHA"}]}`, hashVersion(a))}},
		{Function: openai.FunctionCall{Name: "edit", Arguments: fmt.Sprintf(`{"path":"b.txt","version":%q,"changes":[{"oldText":"beta","newText":"BETA"}]}`, hashVersion(b))}},
	}
	app := NewApp()
	for i, call := range calls {
		result := app.executeTool(context.Background(), ConfigState{Workspace: dir}, "session-1", "edit", []byte(call.Function.Arguments))
		if !result.OK {
			t.Fatalf("parallel edit call %d failed: %#v", i, result)
		}
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

func TestExecuteToolRejectsMultiFileEditCall(t *testing.T) {
	dir := t.TempDir()
	a, b := []byte("alpha\n"), []byte("beta\n")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), a, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.txt"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	// The legacy nested multi-file form must fail without touching either
	// file; multi-file changes go through parallel edit calls instead.
	args := fmt.Sprintf(`{"files":[{"path":"a.txt","version":%q,"changes":[{"oldText":"alpha","newText":"ALPHA"}]},{"path":"b.txt","version":%q,"changes":[{"oldText":"beta","newText":"BETA"}]}]}`, hashVersion(a), hashVersion(b))
	result := NewApp().executeTool(context.Background(), ConfigState{Workspace: dir}, "session-1", "edit", []byte(args))
	if result.OK || !strings.Contains(result.Error, "files") {
		t.Fatalf("expected multi-file edit call to be rejected, got %#v", result)
	}
	for path, want := range map[string]string{"a.txt": "alpha\n", "b.txt": "beta\n"} {
		got, err := os.ReadFile(filepath.Join(dir, path))
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != want {
			t.Fatalf("rejected multi-file edit changed %s: %q, want %q", path, got, want)
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

	// The executor still plans the file list as a whole, so a commit failure
	// on the second file must roll the first file's committed write back.
	// Model-facing calls always carry one file, so this enters through the
	// executor directly with a multi-file plan.
	app := NewApp()
	cfg := ConfigState{Workspace: dir}
	files := []FileTextEdits{
		{Path: "a.txt", Version: hashVersion(a), Changes: []TextChange{{OldText: "alpha", NewText: textPtr("ALPHA")}}},
		{Path: "sub/b.txt", Version: hashVersion(b), Changes: []TextChange{{OldText: "beta", NewText: textPtr("BETA")}}},
	}
	_, err := app.editFilesWithConfig(cfg, files)
	if err == nil {
		t.Fatal("expected commit of b.txt to fail")
	}
	if toolErrorCode(err) != "E_EDIT_COMMIT" {
		t.Fatalf("expected E_EDIT_COMMIT, got %v", err)
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

func TestExecuteToolEditRejectsLegacyNestedFilesForm(t *testing.T) {
	dir := t.TempDir()
	original := []byte("alpha\nbeta\ngamma\n")
	if err := os.WriteFile(filepath.Join(dir, "sample.txt"), original, 0o600); err != nil {
		t.Fatal(err)
	}
	version := hashVersion(original)
	app := NewApp()
	cfg := ConfigState{Workspace: dir}

	// The flat single-file shape is the only accepted form; nested
	// {"files":[...]} arguments from before the schema change fail without
	// touching the file — there is no compatibility backdoor.
	args := fmt.Sprintf(`{"files":[{"path":"sample.txt","version":%q,"changes":[{"oldText":"alpha","newText":"ALPHA"}]}]}`, version)
	result := app.executeTool(context.Background(), cfg, "session-1", "edit", []byte(args))
	if result.OK || !strings.Contains(result.Error, "files") {
		t.Fatalf("expected legacy nested files form to be rejected, got %#v", result)
	}

	// A double-encoded files array is likewise rejected, not repaired into
	// an edit.
	stringEncoded := fmt.Sprintf(`{"files":"[{\"path\":\"sample.txt\",\"version\":\"%s\",\"changes\":[{\"oldText\":\"alpha\",\"newText\":\"ALPHA\"}]}]"}`, version)
	result = app.executeTool(context.Background(), cfg, "session-1", "edit", []byte(stringEncoded))
	if result.OK || !strings.Contains(result.Error, "files") {
		t.Fatalf("expected string-encoded legacy files form to be rejected, got %#v", result)
	}

	got, err := os.ReadFile(filepath.Join(dir, "sample.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(original) {
		t.Fatalf("rejected legacy edit must leave file unchanged, got %q", string(got))
	}
}

func TestExecuteToolEditRejectsRepeatedPathEntriesWithDifferentVersions(t *testing.T) {
	dir := t.TempDir()
	original := []byte("alpha\nbeta\n")
	if err := os.WriteFile(filepath.Join(dir, "sample.txt"), original, 0o600); err != nil {
		t.Fatal(err)
	}
	// Multiple entries in one call never execute, regardless of whether the
	// versions agree: one call edits exactly one file.
	args := fmt.Sprintf(`{"files":[{"path":"sample.txt","version":%q,"changes":[{"oldText":"alpha","newText":"ALPHA"}]},{"path":"./sample.txt","version":%q,"changes":[{"oldText":"beta","newText":"BETA"}]}]}`, hashVersion(original), "zzzzzz")
	result := NewApp().executeTool(context.Background(), ConfigState{Workspace: dir}, "session-1", "edit", []byte(args))
	if result.OK || !strings.Contains(result.Error, "files") {
		t.Fatalf("expected multi-entry legacy call to be rejected, got %#v", result)
	}
	got, err := os.ReadFile(filepath.Join(dir, "sample.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(original) {
		t.Fatalf("rejected multi-entry call must leave file unchanged, got %q", got)
	}
}

func TestExecuteToolRequiresVersionForEdit(t *testing.T) {
	dir := t.TempDir()
	original := []byte("alpha\nbeta\n")
	if err := os.WriteFile(filepath.Join(dir, "sample.txt"), original, 0o600); err != nil {
		t.Fatal(err)
	}
	app := NewApp()
	result := app.executeTool(context.Background(), ConfigState{Workspace: dir}, "session-1", "edit", []byte(`{"path": "sample.txt", "changes": [{"oldText": "beta", "newText": "BETA"}]}`))
	if result.OK || !strings.Contains(result.Error, "version") {
		t.Fatalf("expected the missing version to be named, got %#v", result)
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
	result := app.executeTool(context.Background(), ConfigState{Workspace: dir}, "session-1", "edit", []byte(fmt.Sprintf(`{"path": "config.json",
		"version": %q,
		"changes": [{"oldText": "false", "newText": "true"}]}`, hashVersion(original))))
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
	result := app.executeTool(context.Background(), ConfigState{Workspace: dir}, "session-1", "edit", []byte(fmt.Sprintf(`{"path": "sample.txt",
		"version": %q,
		"changes": [{"oldText": "foo", "newText": "bar"}]}`, hashVersion(original))))
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
	result := NewApp().executeTool(context.Background(), ConfigState{Workspace: dir}, "session-1", "edit", []byte(fmt.Sprintf(`{"path": "sample.txt", "version": %q,
		"changes": [{"oldText": "foo", "newText": "bar", "replaceAll": true}]}`, hashVersion(original))))
	if !result.OK {
		t.Fatalf("replaceAll edit failed: %#v", result)
	}
	got, err := os.ReadFile(filepath.Join(dir, "sample.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "bar bar\nbar\n" {
		t.Fatalf("unexpected replaceAll content: %q", got)
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
	result := app.executeTool(context.Background(), ConfigState{Workspace: dir}, "session-1", "edit", []byte(fmt.Sprintf(`{"path": "sample.txt",
		"version": %q,
		"changes": [
			{"oldText": "abc", "newText": "ABC"},
			{"oldText": "bc", "newText": "BC"}
		]}`, hashVersion(original))))
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

// TestWindowsDeleteSafetyTreatsDrivePrefixSpellingsAlike 锁定卷标裁剪：
// C:\...、\\?\C:\...、\\.\C:\... 必须得到同一判断。filepath.VolumeName 返回的是
// 原生反斜杠形式，若先转正斜杠再裁剪，带 \\?\ 前缀的写法整段裁不掉、深度被多算，
// 1/2 级守卫就形同虚设。
func TestWindowsDeleteSafetyTreatsDrivePrefixSpellingsAlike(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("windows path safety")
	}

	for _, path := range []string{
		`C:\Users\alice`,
		`\\?\C:\Users\alice`,
		`\\.\C:\Users\alice`,
		`\\?\C:\Windows\System32\kernel32.dll`,
	} {
		blocked, reason := isDangerousDeletePath(path)
		if !blocked {
			t.Fatalf("expected %s to be blocked, got allowed", path)
		}
		if strings.TrimSpace(reason) == "" {
			t.Fatalf("expected block reason for %s", path)
		}
	}

	// 盘符路径不再套用 POSIX 临时根字面量：C:\tmp\proj 是二级目录，照样受保护。
	if blocked, _ := isDangerousDeletePath(`C:\tmp\proj`); !blocked {
		t.Fatal("expected a second-level directory below a drive-level tmp folder to be protected")
	}

	// 工作区里的普通文件与真正的临时目录不受影响。
	for _, path := range []string{
		`C:\Users\alice\project\temp.txt`,
		`\\?\C:\Users\alice\project\temp.txt`,
		filepath.Join(os.TempDir(), "workspace", "file.txt"),
	} {
		if blocked, reason := isDangerousDeletePath(path); blocked {
			t.Fatalf("expected %s to be allowed, got blocked: %q", path, reason)
		}
	}
}

// TestUnifiedDeleteProtectionRules 验证两套删除保护机制：
// 1. 系统全盘视角下，禁止删除 1 级、2 级骨干目录（如 /root, /etc, /home/alice, /etc/nginx）；
// 2. 深度 >= 3 的常规项目/配置文件允许删除（如 /root/project/app.py, /etc/nginx/conf.d/test.conf）；
// 3. 命中敏感黑名单的路径即使在深层也严格阻断（如 /etc/shadow, /var/local/libs）。
func TestUnifiedDeleteProtectionRules(t *testing.T) {
	// 验证 1 级与 2 级目录被阻断
	for _, dir := range []string{"/", "/root", "/home", "/etc", "/var", "/home/alice", "/etc/nginx"} {
		blocked, reason := isDangerousDeletePath(dir)
		if !blocked {
			t.Fatalf("expected directory %s to be blocked, got allowed", dir)
		}
		if strings.TrimSpace(reason) == "" {
			t.Fatalf("expected block reason for %s", dir)
		}
	}

	// 验证敏感黑名单在深层依然被阻断（这些路径深度都 >= 3，只能靠名单拦住）
	for _, sensitive := range []string{
		"/etc/shadow",
		"/etc/passwd",
		"/var/local/libs",
		"/dev/sda",
		"/proc/cpuinfo",
		"/usr/share/doc/foo",
		"/usr/local/lib/node_modules/pkg/index.js",
	} {
		blocked, reason := isDangerousDeletePath(sensitive)
		if !blocked {
			t.Fatalf("expected sensitive target %s to be blocked, got allowed", sensitive)
		}
		if strings.TrimSpace(reason) == "" {
			t.Fatalf("expected block reason for %s", sensitive)
		}
	}

	// 验证深层（depth >= 3）常规文件/配置允许删除（不误杀）
	for _, allowedPath := range []string{
		"/root/ally-remote-test/app.py",
		"/home/alice/project/file.txt",
		"/etc/nginx/conf.d/test.conf",
		"/usr/local/project/file.txt",
	} {
		blocked, reason := isDangerousDeletePath(allowedPath)
		if blocked {
			t.Fatalf("expected ordinary deep path %s to be allowed, got blocked: %q", allowedPath, reason)
		}
	}

	// 机制 1 只拦第 2 层的“目录”，/etc 直属文件落在它的覆盖之外，只能靠名单兜底：
	// 除了验证行为，还要确认每项确实在名单里 —— 否则删掉名单项后，在文件不存在
	// 的平台上会被层级规则偶然拦住，测试看不出回归。
	for _, directFile := range []string{
		"/etc/hosts",
		"/etc/resolv.conf",
		"/etc/nsswitch.conf",
		"/etc/hostname",
		"/etc/environment",
		"/etc/profile",
		"/etc/bash.bashrc",
		"/etc/ld.so.conf",
		"/etc/shells",
		"/etc/machine-id",
	} {
		listed := false
		for _, target := range sensitiveDeleteTargets {
			if target == directFile {
				listed = true
				break
			}
		}
		if !listed {
			t.Errorf("sensitiveDeleteTargets lost %s: level-2 files are outside the depth guard, only this list can block them", directFile)
		}
		if blocked, _ := isDangerousDeletePath(directFile); !blocked {
			t.Fatalf("expected /etc direct file %s to be blocked, got allowed", directFile)
		}
	}
}

// TestLinuxDeleteSafetyAllowsWorkspaceFilesUnderRootHome 验证 Linux 平台
// 上 /root 子树内的普通工作区文件允许删除，系统树仍整体拒绝。
func TestLinuxDeleteSafetyAllowsWorkspaceFilesUnderRootHome(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux path safety")
	}
	allowed, reason := isDangerousDeletePath("/root/ally-remote-test/app.py")
	if allowed {
		t.Fatalf("expected ordinary workspace file under /root to be allowed, got %q", reason)
	}
	for _, path := range []string{"/", "/root", "/etc", "/etc/passwd", "/usr", "/usr/bin/ls", "/var/log"} {
		blocked, reason := isDangerousDeletePath(path)
		if !blocked {
			t.Fatalf("expected %s to be blocked", path)
		}
		if strings.TrimSpace(reason) == "" {
			t.Fatalf("expected block reason for %s", path)
		}
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

	got := app.GetGitDiff("", "")
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

// TestGitQueriesFollowExplicitWorkspace pins the contract behind the composer
// footer: the git badge and diff modal must follow the directory the caller
// displays. KB and temp tabs never write config.workspace, so an explicit
// workspace is the only way for them to report their own repository.
func TestGitQueriesFollowExplicitWorkspace(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}

	repo := t.TempDir()
	plain := t.TempDir()
	runGitTestCommand(t, repo, "init")
	runGitTestCommand(t, repo, "config", "user.email", "ally-test@example.com")
	runGitTestCommand(t, repo, "config", "user.name", "Ally Test")
	if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("alpha\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGitTestCommand(t, repo, "add", ".")
	runGitTestCommand(t, repo, "commit", "-m", "init")
	if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("beta\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	app := NewApp()
	app.initialized = true
	// The persisted chat workspace is not a repository: only the explicit
	// workspace argument may surface the repo's status and diff.
	app.config = ConfigState{Workspace: plain}

	if got := app.GetGitStatus(""); got.IsRepo {
		t.Fatalf("empty workspace must keep using the active config workspace, got %#v", got)
	}
	status := app.GetGitStatus(repo)
	if !status.IsRepo || status.Modified != 1 {
		t.Fatalf("expected explicit repo workspace to report 1 modified file, got %#v", status)
	}

	diff := app.GetGitDiff(repo, "")
	if !diff.IsRepo {
		t.Fatalf("expected explicit repo workspace diff, error=%q", diff.Error)
	}
	found := false
	for _, file := range diff.Files {
		if file.Path == "file.txt" && strings.Contains(file.Diff, "+beta") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected file.txt change in explicit workspace diff, got %#v", diff.Files)
	}
	if fallback := app.GetGitDiff("", ""); fallback.IsRepo {
		t.Fatalf("empty workspace must keep using the active config workspace, got %#v", fallback)
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
		{Path: "sample.txt", Version: version, Changes: []TextChange{{OldText: "alpha", NewText: textPtr("ALPHA")}}},
		{Path: "./sample.txt", Version: version, Changes: []TextChange{{OldText: "beta", NewText: textPtr("BETA")}}},
	}

	plan, err := planLocalEditBatch(cfg, files, localEditPlanForExecution)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Targets) != 1 || len(plan.Files) != 1 || len(plan.Files[0].Edit.Changes) != 2 {
		t.Fatalf("expected one merged physical target and two changes, got %#v", plan)
	}

	// Model-facing calls are flat and single-file. The executor and the
	// conflict detector must still agree on the same plan boundary.
	args, err := json.Marshal(FileTextEdits{Path: "sample.txt", Version: version, Changes: []TextChange{{OldText: "alpha", NewText: textPtr("ALPHA")}, {OldText: "beta", NewText: textPtr("BETA")}}})
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

func TestExecuteToolEditRejectsTruncatedArguments(t *testing.T) {
	dir := t.TempDir()
	original := []byte("alpha\nbeta\ngamma\n")
	if err := os.WriteFile(filepath.Join(dir, "sample.txt"), original, 0o600); err != nil {
		t.Fatal(err)
	}
	app := NewApp()
	cfg := ConfigState{Workspace: dir}
	version := strings.ToUpper(hashVersion(original))

	// Stream cut inside a change: the call fails with the explicit truncation
	// error and nothing is written. The model resends the complete call.
	truncated := fmt.Sprintf(`{"path":"sample.txt","version":%q,"changes":[{"oldText":"alpha","newText":"ALPHA"},{"oldText":"beta","newText":"BET`, version)
	result := app.executeTool(context.Background(), cfg, "session-1", "edit", []byte(truncated))
	if result.OK || !strings.Contains(strings.ToLower(result.Error), "truncated") {
		t.Fatalf("expected truncation error without salvage, got %#v", result)
	}
	got, err := os.ReadFile(filepath.Join(dir, "sample.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(original) {
		t.Fatalf("truncated edit must not write the file, got %q", string(got))
	}

	// The marker that prepareToolCallsForExecution substitutes for truncated
	// JSON fails with E_TRUNCATED_ARGS before any decoding is attempted.
	result = app.executeTool(context.Background(), cfg, "session-1", "edit", []byte(toolcall.TruncatedArgumentsMarker))
	if result.OK || result.ErrorCode != "E_TRUNCATED_ARGS" {
		t.Fatalf("expected E_TRUNCATED_ARGS for the truncation marker, got %#v", result)
	}
}

func TestPrepareToolCallsForExecutionRewritesInvalidJSONToMarker(t *testing.T) {
	valid := `{"path":"sample.txt","version":"abc123","changes":[{"oldText":"alpha","newText":"ALPHA"}]}`
	prepared, executionArgs := prepareToolCallsForExecution([]openai.ToolCall{
		{ID: "call_1", Type: openai.ToolTypeFunction, Function: openai.FunctionCall{Name: "edit", Arguments: valid}},
		{ID: "call_2", Type: openai.ToolTypeFunction, Function: openai.FunctionCall{Name: "list_files"}},
		{ID: "call_3", Type: openai.ToolTypeFunction, Function: openai.FunctionCall{Name: "edit", Arguments: `{"path":"sample.txt","ver`}},
		{ID: "call_4", Type: openai.ToolTypeFunction, Function: openai.FunctionCall{Name: "read", Arguments: "   "}},
	})
	if len(prepared) != 4 || len(executionArgs) != 4 {
		t.Fatalf("unexpected prepared result: %#v %#v", prepared, executionArgs)
	}
	if prepared[0].Function.Arguments != valid || executionArgs[0] != valid {
		t.Fatalf("valid arguments must pass through unchanged: %q %q", prepared[0].Function.Arguments, executionArgs[0])
	}
	if prepared[1].Function.Arguments != "{}" || executionArgs[1] != "{}" {
		t.Fatalf("empty arguments must normalize to {}: %q %q", prepared[1].Function.Arguments, executionArgs[1])
	}
	if !toolcall.IsTruncatedArguments(prepared[2].Function.Arguments) || !toolcall.IsTruncatedArguments(executionArgs[2]) {
		t.Fatalf("truncated arguments must use the marker on both paths: %q %q", prepared[2].Function.Arguments, executionArgs[2])
	}
	if prepared[3].Function.Arguments != "{}" || executionArgs[3] != "{}" {
		t.Fatalf("whitespace-only arguments must normalize to {}: %q %q", prepared[3].Function.Arguments, executionArgs[3])
	}
}

func TestExecuteToolEditDoesNotMislabelSchemaErrorAsTruncation(t *testing.T) {
	app := NewApp()
	result := app.executeTool(context.Background(), ConfigState{Workspace: t.TempDir()}, "session-1", "edit", []byte(
		`{"path":"sample.txt","version":"abc123","changes":[{"oldString":"alpha","newString":"ALPHA"}]}`,
	))
	if result.OK {
		t.Fatal("legacy unknown change fields must fail the model-facing edit schema")
	}
	if strings.Contains(strings.ToLower(result.Error), "truncated") {
		t.Fatalf("complete schema error must not be reported as truncation: %q", result.Error)
	}
	// Unknown keys inside a change used to be tolerated with a warning while
	// the change itself failed validation; both problems are now reported
	// together, naming every offending key.
	if !strings.Contains(result.Error, "oldString") || !strings.Contains(result.Error, "newText") {
		t.Fatalf("expected the unknown keys and the missing newText, got %q", result.Error)
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

// TestScheduledTaskToolResultKeepsEmptyTasksKey 锁定列表结果在「一条任务都没有」时的
// 线上形状：必须带 tasks 键（[]），而不是整条退化成 {}。前端的卡片网格靠它把卡片正体
// 换成带空态文案的清单，模型也靠它区分「一条都没有」与「结果里根本没有这个字段」——
// 给 Tasks 加回 omitempty 就会退化（服务列表的 Services 是同一条约定）。
func TestScheduledTaskToolResultKeepsEmptyTasksKey(t *testing.T) {
	root := t.TempDir()
	app := NewApp()
	app.configPath = filepath.Join(root, "config.json")
	app.config = ConfigState{Workspace: root}
	if err := app.startScheduledTaskManager(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(app.stopScheduledTaskManager)

	listed, err := app.executeScheduledTaskTool(app.config, ScheduledTaskToolRequest{Action: "list"})
	if err != nil {
		t.Fatal(err)
	}
	if tasks := listed.(ScheduledTaskToolResult).Tasks; len(tasks) != 0 {
		t.Fatalf("expected an empty manager to list no tasks, got %#v", tasks)
	}
	raw, err := json.Marshal(listed)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"tasks":[]`) {
		t.Fatalf("an empty list result must keep the tasks key, got %s", raw)
	}
}

func TestScheduledTasksPersistAcrossRestart(t *testing.T) {
	root := t.TempDir()
	app := NewApp()
	app.configPath = filepath.Join(root, "config.json")
	app.config = ConfigState{Workspace: root}
	if err := app.startScheduledTaskManager(); err != nil {
		t.Fatal(err)
	}
	created, err := app.executeScheduledTaskTool(app.config, ScheduledTaskToolRequest{
		Action:      "create",
		Name:        "recurring",
		Instruction: "echo hi",
		Schedule:    "30m",
	})
	if err != nil {
		t.Fatal(err)
	}
	id := created.(ScheduledTaskToolResult).Task.ID
	app.stopScheduledTaskManager()

	// Restart: the recurring task must come back from disk.
	app2 := NewApp()
	app2.configPath = filepath.Join(root, "config.json")
	app2.config = ConfigState{Workspace: root}
	if err := app2.startScheduledTaskManager(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(app2.stopScheduledTaskManager)
	tasks := app2.ListScheduledTasks()
	if len(tasks) != 1 || tasks[0].ID != id || tasks[0].Running {
		t.Fatalf("expected persisted task %s restored and not running, got %#v", id, tasks)
	}

	// Fired one-shots, past-due one-shots, tasks whose workspace is gone, tasks
	// interrupted mid-run, and malformed entries must not block startup. Only the
	// ones that can still run come back as schedulable; the rest stay visible with
	// an explanatory status.
	path := filepath.Join(root, "scheduled_tasks.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var stored []ScheduledTask
	if err := json.Unmarshal(data, &stored); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-time.Hour).Format(time.RFC3339)
	stored = append(stored,
		ScheduledTask{ID: "task_fired", Name: "done", Instruction: "x", Workspace: root,
			Schedule: ScheduledTaskSchedule{Type: "once", At: past}, LastRunAt: 1},
		ScheduledTask{ID: "task_late", Name: "late", Instruction: "x", Workspace: root,
			Schedule: ScheduledTaskSchedule{Type: "once", At: past}},
		ScheduledTask{ID: "task_gone", Name: "gone", Command: "echo x", Workspace: filepath.Join(root, "deleted-workspace"),
			Schedule: ScheduledTaskSchedule{Type: "interval", Every: "30m"}, LastStatus: "scheduled"},
		ScheduledTask{ID: "task_crashed", Name: "crash", Command: "echo x", Workspace: root,
			Schedule: ScheduledTaskSchedule{Type: "interval", Every: "30m"}, LastStatus: "running", Running: true},
		ScheduledTask{ID: "task_bad", Name: "bad"},
	)
	app2.stopScheduledTaskManager()
	if err := os.WriteFile(path, mustJSON(stored), 0o600); err != nil {
		t.Fatal(err)
	}
	app3 := NewApp()
	app3.configPath = filepath.Join(root, "config.json")
	app3.config = ConfigState{Workspace: root}
	if err := app3.startScheduledTaskManager(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(app3.stopScheduledTaskManager)
	byID := map[string]ScheduledTask{}
	for _, task := range app3.ListScheduledTasks() {
		byID[task.ID] = task
	}
	if _, ok := byID[id]; !ok {
		t.Fatalf("expected the recurring task to survive, got %#v", byID)
	}
	for _, dropped := range []string{"task_fired", "task_bad"} {
		if _, ok := byID[dropped]; ok {
			t.Fatalf("%s must not come back, got %#v", dropped, byID[dropped])
		}
	}
	if late := byID["task_late"]; late.LastStatus != "missed" || late.NextRunAt != 0 || late.Running {
		t.Fatalf("a past-due one-shot must be reported as missed and never re-armed, got %#v", late)
	}
	if gone := byID["task_gone"]; gone.LastStatus != "invalid" || gone.NextRunAt != 0 || gone.Running {
		t.Fatalf("a task whose workspace vanished must be marked invalid and not scheduled, got %#v", gone)
	}
	if crashed := byID["task_crashed"]; crashed.LastStatus != "interrupted" || crashed.Running || crashed.NextRunAt == 0 {
		t.Fatalf("a task interrupted mid-run must resume as interrupted with a next run, got %#v", crashed)
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

// 任务内容契约：instruction 与 command 恰好提供一个；command 型任务的
// 视图必须带回 command（任务中心与工具卡据此渲染内容）。
func TestScheduledTaskCreateRequiresExactlyOneContentKind(t *testing.T) {
	root := t.TempDir()
	app := NewApp()
	app.configPath = filepath.Join(root, "config.json")
	app.config = ConfigState{Workspace: root}
	if err := app.startScheduledTaskManager(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(app.stopScheduledTaskManager)

	if _, err := app.executeScheduledTaskTool(app.config, ScheduledTaskToolRequest{
		Action: "create", Name: "both", Instruction: "x", Command: "echo hi", Schedule: "1h",
	}); err == nil {
		t.Fatal("expected create with both instruction and command to fail")
	}
	if _, err := app.executeScheduledTaskTool(app.config, ScheduledTaskToolRequest{
		Action: "create", Name: "neither", Schedule: "1h",
	}); err == nil {
		t.Fatal("expected create with neither instruction nor command to fail")
	}

	created, err := app.executeScheduledTaskTool(app.config, ScheduledTaskToolRequest{
		Action: "create", Name: "cmd task", Command: "echo hi", Schedule: "1h",
	})
	if err != nil {
		t.Fatal(err)
	}
	result := created.(ScheduledTaskToolResult)
	if result.Task == nil || result.Task.Command != "echo hi" || result.Task.Instruction != "" {
		t.Fatalf("expected command-mode task view to carry the command only, got %#v", result.Task)
	}
	if task := app.ListScheduledTasks(); len(task) != 1 || task[0].Command != "echo hi" {
		t.Fatalf("expected stored command task, got %#v", task)
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

// 服务输出里的内核拒写不能只剩那句裸权限错误：模型侧读回结果要看到解释与可写范围。
// 标记是 sticky 的：拒写那几行会被日志刷出尾部，而它说的是「这次写没成」这件已经
// 发生过的事。卡片只在 start 那张上显示告警（读日志、停服务并不写盘，见前端
// utils/sandboxAlert）。
func TestServiceReadReportsSandboxDeniedWrite(t *testing.T) {
	requireConfinement(t)
	app := NewApp()
	buffer := newRollingBuffer(serviceOutputLimit)
	if _, err := buffer.Write([]byte("/bin/sh: /tmp/x: Operation not permitted\n")); err != nil {
		t.Fatal(err)
	}

	app.servicesMu.Lock()
	app.services["svc_denied"] = &managedService{
		info:       ServiceInfo{ID: "svc_denied", Command: "demo", Status: "running", Sandboxed: true},
		output:     buffer,
		writeRoots: []string{"/tmp/workspace"},
	}
	app.servicesMu.Unlock()

	res, err := app.readServiceOutput(ServiceReadRequest{ID: "svc_denied"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.SandboxDenied {
		t.Fatalf("a service whose output shows a kernel refusal must be flagged, got %#v", res)
	}
	if !strings.Contains(res.DeniedWriteHint, "沙箱已拦截") {
		t.Fatalf("the flag must ride with the same hint the command path gives, got %q", res.DeniedWriteHint)
	}
	if !strings.Contains(res.DeniedWriteHint, "/tmp/workspace") {
		t.Fatalf("the hint must name the writable roots, got %q", res.DeniedWriteHint)
	}

	// 尾部被后续日志刷掉之后，标记仍在（sticky）。
	if _, err := buffer.Write([]byte(strings.Repeat("later line\n", 400))); err != nil {
		t.Fatal(err)
	}
	res, err = app.readServiceOutput(ServiceReadRequest{ID: "svc_denied", TailBytes: 64})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(res.Output, "Operation not permitted") {
		t.Fatalf("the denial should have been pushed out of the tail, got %q", res.Output)
	}
	if !res.SandboxDenied {
		t.Fatalf("the denial must stay recorded once seen, got %#v", res)
	}
}

// TestPromotedCommandResultCarriesDeniedWriteHint 锁定「超时收编」这条支路的模型侧说明：
// 收编是提前 return，到不了普通命令末尾补说明那一步，拒写标记与说明只能从快照带过来。
// 拒写与超时能共存：沙箱拒的是那一次写，命令本身照跑（实测 `sh -c 'echo hi > /tmp/x;
// sleep 9'` 在写被拒后照常跑完），所以收编前的输出里完全可能有内核拒写。
func TestPromotedCommandResultCarriesDeniedWriteHint(t *testing.T) {
	buf := &limitedBuffer{limit: 8 * 1024}
	if _, err := buf.Write([]byte("sh: /tmp/x: Operation not permitted\n")); err != nil {
		t.Fatal(err)
	}
	info := ServiceInfo{
		ID:              "svc_promoted",
		Sandboxed:       true,
		SandboxDenied:   true,
		DeniedWriteHint: "沙箱已拦截：目标 /tmp/x 在可写范围之外",
	}
	result := (&App{}).promotedCommandResult(
		CommandRequest{Command: "sh -c 'echo hi > /tmp/x; sleep 99'"},
		shellInvocation{name: "bash", path: "/bin/bash"},
		"/tmp/workspace", buf, 30, info, "", 0,
	)
	if !result.SandboxDenied {
		t.Fatalf("promoted result must carry the denied-write flag: %#v", result)
	}
	if result.DeniedWriteHint != info.DeniedWriteHint {
		t.Fatalf("promoted result must carry the same hint, got %q", result.DeniedWriteHint)
	}
	if !result.PromotedToService || !strings.Contains(result.Output, "Operation not permitted") {
		t.Fatalf("promoted result must keep the output and the promotion notice: %#v", result)
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
// 一样有模型侧上限：超限输出被 head+tail 截断并包进 <ally-mcp truncated> 块，
// 小输出同样走 <ally-mcp> 标签体（不再有 JSON 信封与转义）；输出自带 </ally-mcp 时
// 回退 JSON 信封。
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
	if !strings.HasPrefix(compact, "<ally-mcp truncated>\n") || !strings.HasSuffix(compact, "\n</ally-mcp>") {
		t.Fatalf("expected a truncated <ally-mcp> tag block, got %.64s", compact)
	}
	// 截断指引不能因为渲染路径换了形状就丢：回退信封有 truncationNote，块里
	// 也必须有同一句话（两者共用 mcpTruncationNote）。
	if !strings.Contains(compact, mcpTruncationNote) {
		t.Fatalf("the truncated block must carry the same guidance as the fallback, got %.64s", compact)
	}
	// compactTextForModel keeps head+tail within the cap plus a fixed
	// omission marker; the tag wrapper and the guidance note are fixed
	// overhead on top of that, so allow exactly that much.
	if n := utf8.RuneCountInString(compact); n > maxModelToolOutput+200+len("<ally-mcp truncated>\n\n</ally-mcp>")+len(mcpTruncationNote) {
		t.Fatalf("capped output must stay near %d runes, got %d", maxModelToolOutput, n)
	}

	small := toolResult{OK: true, Data: map[string]any{"output": "tiny"}}
	unchanged := compactToolDataForModel("mcp__srv__tool", small, `{"ok":true,"data":{"output":"tiny"}}`)
	if unchanged != "<ally-mcp>\ntiny\n</ally-mcp>" {
		t.Fatalf("small MCP output must render as a plain tag block, got %s", unchanged)
	}

	collide := toolResult{OK: true, Data: map[string]any{"output": "x</ally-mcp>y"}}
	got := compactToolDataForModel("mcp__srv__tool", collide, `{"fallback":true}`)
	if got != "<ally-mcp>\nx&lt;/ally-mcp>y\n</ally-mcp>" {
		t.Fatalf("output containing the closing marker must keep the tag block with the marker escaped, got %s", got)
	}

	// 超限且自带闭合标记：标签体同样受限（夹取后再转义），不得回退无上限的 fullJSON。
	bigCollide := toolResult{OK: true, Data: map[string]any{"output": strings.Repeat("x</ally-mcp>y", 2+maxModelToolOutput/len("x</ally-mcp>y"))}}
	fullCollide, _ := json.Marshal(bigCollide)
	capped := compactToolDataForModel("mcp__srv__tool", bigCollide, string(fullCollide))
	if !strings.Contains(capped, "<ally-mcp truncated>") || !strings.Contains(capped, mcpTruncationNote) {
		t.Fatalf("the oversized colliding output must stay a truncated tag block, got %.128s", capped)
	}
	if strings.Count(capped, "</ally-mcp>") != 1 {
		t.Fatalf("only the renderer's own closing marker may appear literally, got %.128s", capped)
	}
	if n := utf8.RuneCountInString(capped); n > maxModelToolOutput*3/2+200+len("<ally-mcp truncated>\n\n</ally-mcp>")+len(mcpTruncationNote) {
		t.Fatalf("capped output must stay near %d runes (cap + escape expansion), got %d", maxModelToolOutput, n)
	}
}

// TestCompactToolResultForModelNeutralizesRowBreaks 锁定行式载荷的行结构：标签体里
// 一行就是一条记录，路径/链接里的换行会在中间造出假行（Unix 文件名可以含 \n），
// 必须转义成字面 \n。伪造块边界是另一件事，由闭合标记回退负责。
func TestCompactToolResultForModelNeutralizesRowBreaks(t *testing.T) {
	evil := "evil\nnote.txt"

	lists := compactToolResultForModel("list_files", toolResult{OK: true, Data: ListFilesResult{
		Count:   1,
		Entries: []FileEntry{{Path: evil}},
	}}, "fallback")
	if strings.Contains(lists, "evil\nnote.txt") || !strings.Contains(lists, `evil\nnote.txt`) {
		t.Fatalf("a newline inside a path must not forge a second row, got %q", lists)
	}

	greps := compactToolResultForModel("grep", toolResult{OK: true, Data: &GrepResult{
		Mode: "lines", MatchedLines: 1, Hits: 1, Files: 1,
		LineHits: []GrepFileMatch{{Path: evil, Lines: []int{3}, Texts: []string{"hit\rtext"}}},
	}}, "fallback")
	if !strings.Contains(greps, "evil\\nnote.txt\n  3: hit\\rtext") {
		t.Fatalf("grep rows must stay one line per match, got %q", greps)
	}

	fetches := compactToolResultForModel("web_fetch", toolResult{OK: true, Data: WebFetchResult{
		Status: 200, Text: "body",
		Links: []WebFetchLink{{"docs\nhere", "https://ex.com/a\nb"}},
	}}, "fallback")
	if !strings.Contains(fetches, `link: docs\nhere <https://ex.com/a\nb>`) {
		t.Fatalf("link rows must stay one line each, got %q", fetches)
	}
}

func TestCompactToolResultForModelCommandRendersTagBlock(t *testing.T) {
	// 模型侧改为 <ally-cmd> 标签体：输出原样落体（无 JSON 转义），command/cwd 不再
	// 回显（模型刚在 tool call 参数里写过），exit=0 是隐含成功态不占属性；
	// 非零退出码 / 超时 / 截断 / 落盘指针以属性表达。
	result := toolResult{OK: true, Data: map[string]any{
		"command": "go build ./...", "cwd": "", "shell": "bash", "shellPath": "/bin/bash",
		"output": "c1\nc2\nc3\nc4\nc5\n", "exitCode": 0, "timedOut": false, "durationMs": 10, "truncated": false,
	}}
	full := `{"command":"go build ./...","cwd":"","shell":"bash","shellPath":"/bin/bash","output":"c1\nc2\nc3\nc4\nc5\n","exitCode":0,"timedOut":false,"durationMs":10,"truncated":false}`
	modelJSON := compactToolDataForModel("command", result, full)
	if !strings.HasPrefix(modelJSON, "<ally-cmd>\n") || !strings.Contains(modelJSON, "c1\nc2\nc3\nc4\nc5\n</ally-cmd>") {
		t.Fatalf("command output must pass through verbatim in the tag body, got %s", modelJSON)
	}
	if strings.Contains(modelJSON, "go build") {
		t.Fatalf("command/cwd echo must be gone from the model payload, got %s", modelJSON)
	}
	if strings.Contains(modelJSON, `exit="0"`) {
		t.Fatalf("zero exit code is implicit and must be omitted, got %s", modelJSON)
	}

	failed := toolResult{OK: true, Data: map[string]any{
		"output": "boom", "exitCode": 2, "truncated": true, "outputFilePath": `C:\tmp\spill.log`,
	}}
	failedModel := compactToolDataForModel("command", failed, `{"fallback":true}`)
	if !strings.Contains(failedModel, `exit="2"`) || !strings.Contains(failedModel, ` truncated`) || !strings.Contains(failedModel, `full="C:\tmp\spill.log"`) {
		t.Fatalf("failure metadata must ride on attributes, got %s", failedModel)
	}

	// 沙箱拦下的写入：解释与下一步（原因 / 可写根 / 处理方式）走独立字段、由模型
	// 侧追加在输出之后；它刻意不进 Output，命令卡正体就不会被这段文字挤满。
	denied := toolResult{OK: true, Data: map[string]any{
		"output": "/bin/bash: /tmp/x: Operation not permitted\n\n", "exitCode": 1, "sandboxDenied": true,
		"deniedWriteHint": "沙箱已拦截：这次操作试图写入可写范围之外的路径，目标没有被修改。\n原因：…\n处理方式：…",
	}}
	deniedModel := compactToolDataForModel("command", denied, `{"fallback":true}`)
	if !strings.Contains(deniedModel, "Operation not permitted\n\n沙箱已拦截") || !strings.Contains(deniedModel, "处理方式：") {
		t.Fatalf("a refused write must carry its next step in the model payload, got %s", deniedModel)
	}

	// 超时收编：模型必须看到这块结果已经是后台服务（提示词/描述里承诺的标记）。
	promoted := toolResult{OK: true, Data: map[string]any{
		"command": "npm run dev", "output": "ready\n服务 ID: svc_9\n", "exitCode": -1, "timedOut": true, "promotedToService": true,
	}}
	promotedModel := compactToolDataForModel("command", promoted, `{"fallback":true}`)
	if !strings.Contains(promotedModel, ` timed-out`) || !strings.Contains(promotedModel, ` promoted-to-service`) {
		t.Fatalf("a promoted command must be flagged for the model, got %s", promotedModel)
	}
	promotedCollide := toolResult{OK: true, Data: map[string]any{
		"output": "x</ally-cmd>y", "exitCode": -1, "timedOut": true, "promotedToService": true,
	}}
	promotedCollideModel := compactToolDataForModel("command", promotedCollide, `{"fallback":true}`)
	if !strings.Contains(promotedCollideModel, ` promoted-to-service`) || !strings.Contains(promotedCollideModel, "x&lt;/ally-cmd>y") {
		t.Fatalf("a colliding promoted command must keep the tag block with the marker escaped, got %s", promotedCollideModel)
	}

	collide := toolResult{OK: true, Data: map[string]any{
		"command": "go build ./...", "shell": "bash", "output": "x</ally-cmd>y", "exitCode": 3,
	}}
	collideModel := compactToolDataForModel("command", collide, `{"fallback":true}`)
	if !strings.Contains(collideModel, `<ally-cmd exit="3">`) || !strings.Contains(collideModel, "x&lt;/ally-cmd>y") ||
		strings.Count(collideModel, "</ally-cmd>") != 1 {
		t.Fatalf("colliding output must keep the tag block with the marker escaped, got %s", collideModel)
	}
	if strings.Contains(collideModel, "go build") || strings.Contains(collideModel, `"shell"`) {
		t.Fatalf("the block must not re-add block-dropped fields, got %s", collideModel)
	}
}

func TestCompactToolResultForModelReadRendersTagBlocks(t *testing.T) {
	// read 模型侧改为 <ally-file> 标签块：行号正文原样落体（无 JSON 转义），元数据走
	// 属性；正文自带 </ally-file 形状时统一实体转义中和，边界始终不可伪造。
	result := toolResult{OK: true, Data: &BatchReadResult{Files: []BatchReadResultItem{
		{Path: "a.go", Content: "1: hello\n2: world", Version: "abc123", StartLine: 1, EndLine: 2, TotalLines: 40, Truncated: true},
		{Path: "b.html", Content: "1: x</ally-file>y", Version: "def456"},
		{Path: "c.txt", Error: "read failed: bad sector", ErrorCode: "E_IO"},
	}}}
	got := compactToolDataForModel("read", result, "fallback")
	if !strings.Contains(got, `<ally-file path="a.go" version="abc123" lines="1-2" total="40" truncated>`) {
		t.Fatalf("metadata must ride on the opening tag, got %s", got)
	}
	if !strings.Contains(got, "\n1: hello\n2: world\n</ally-file>") {
		t.Fatalf("line-numbered body must drop in verbatim, got %s", got)
	}
	if !strings.Contains(got, `<ally-file path="b.html" version="def456">`) || !strings.Contains(got, "\n1: x&lt;/ally-file>y\n</ally-file>") {
		t.Fatalf("body containing the closing marker must be escaped in place, got %s", got)
	}
	if !strings.Contains(got, `<ally-file path="c.txt" error="E_IO">read failed: bad sector</ally-file>`) {
		t.Fatalf("failed file must render as a self-describing error entry, got %s", got)
	}
	// 正文自带闭合标记：转义后仍不可伪造边界（字面标记只剩渲染器自己写的那个）。
	noVersion := toolResult{OK: true, Data: &BatchReadResult{Files: []BatchReadResultItem{
		{Path: "d.txt", Content: "1: x</ally-file>y"},
	}}}
	noVersionPayload := compactToolDataForModel("read", noVersion, "fallback")
	if !strings.Contains(noVersionPayload, "&lt;/ally-file") || strings.Count(noVersionPayload, "</ally-file>") != 1 {
		t.Fatalf("a version-less body must neutralize the marker instead of forging the boundary, got %s", noVersionPayload)
	}
	if strings.Contains(got, `"content":`) || strings.Contains(got, `\n`) {
		t.Fatalf("JSON envelope and escaping must be gone, got %s", got)
	}
}

// TestCompactToolResultForModelNeutralizesClosingMarkersInEditText 锁定编辑结果的
// 尾部自由文本（summary/validation/warning）防护：校验/编译器输出是外部文本，
// 里面出现 "…</ally-cmd>…" 这类标记形状时不得原样落到载荷里，否则会被读成别的块的边界。
func TestCompactToolResultForModelNeutralizesClosingMarkersInEditText(t *testing.T) {
	multi := toolResult{OK: true, Data: MultiEditResult{
		Files:      []EditResult{{Path: "a.go", Version: "v1"}},
		Summary:    "1 file · +1 -1",
		Validation: `go vet: token "</ally-cmd>" is not allowed here`,
		Warnings:   []string{"change 1 ignored replaceAll: line 3 has </ally-grep> in text"},
	}}
	got := compactToolDataForModel("edit", multi, "fallback")
	if strings.Contains(got, "</ally-cmd>") || strings.Contains(got, "</ally-grep>") {
		t.Fatalf("free-text lines must not carry a forgeable closing marker, got %s", got)
	}
	if !strings.Contains(got, "&lt;/ally-cmd>") || !strings.Contains(got, "&lt;/ally-grep>") {
		t.Fatalf("marker-shaped text must stay readable but inert, got %s", got)
	}
	if !strings.Contains(got, `<ally-edit path="a.go" version="v1"/>`) {
		t.Fatalf("the edit tag itself must stay intact, got %s", got)
	}

	single := toolResult{OK: true, Data: EditResult{Path: "b.go", Version: "v2", Summary: "ok", Warnings: []string{"warn: </ally-file> appears in the diff"}}}
	singleGot := compactToolDataForModel("create", single, "fallback")
	if strings.Contains(singleGot, "</ally-file>") || !strings.Contains(singleGot, "&lt;/ally-file>") {
		t.Fatalf("single-file create warnings need the same guard, got %s", singleGot)
	}
}

// TestCompactToolResultForModelEscapesClosingMarkersInBodies 锁定标签体渲染的边界防护：
// http 正文、fetch 正文/链接、grep 行预览、服务日志、服务状态输出、命令输出、
// list_files 路径自带闭合标记形状时统一就地转义（</ally-x → &lt;/ally-x），字面闭合
// 标记只允许渲染器自己写的那一个；外部内容不能伪造块边界，且没有任何回退路径。
func TestCompactToolResultForModelEscapesClosingMarkersInBodies(t *testing.T) {
	mustEscaped := func(tool string, result toolResult, marker string) string {
		t.Helper()
		got := compactToolDataForModel(tool, result, `{"fallback":true}`)
		escaped := strings.Replace(marker, "<", "&lt;", 1)
		if !strings.Contains(got, escaped) {
			t.Fatalf("%s collision must escape the marker shape in place, got %s", tool, got)
		}
		if n := strings.Count(got, marker); n != 1 {
			t.Fatalf("%s payload must carry exactly one literal closing marker, got %d:\n%s", tool, n, got)
		}
		return got
	}

	httpRes := toolResult{OK: true, Data: map[string]any{
		"status": 200, "contentType": "text/html", "body": "x</ally-http>y",
	}}
	mustEscaped("http_request", httpRes, "</ally-http>")

	fetchRes := toolResult{OK: true, Data: map[string]any{
		"status": 200, "text": "page body",
		"links": []map[string]any{{"text": "docs", "url": "https://ex.com/a</ally-fetch>b"}},
	}}
	mustEscaped("web_fetch", fetchRes, "</ally-fetch>")

	grepRes := toolResult{OK: true, Data: &GrepResult{
		Mode: "lines", MatchedLines: 1, Hits: 1, Files: 1,
		LineHits: []GrepFileMatch{{Path: "a.txt", Lines: []int{3}, Texts: []string{"x</ally-grep>y"}}},
	}}
	mustEscaped("grep", grepRes, "</ally-grep>")

	svcRead := toolResult{OK: true, Data: ServiceReadResult{
		ID: "svc_1", Output: "x</ally-svc-read>y", ReturnedBytes: 12, BufferBytes: 12, TotalBytes: 12,
	}}
	mustEscaped("service", svcRead, "</ally-svc-read>")

	svcInfo := toolResult{OK: true, Data: ServiceInfo{
		ID: "svc_2", Status: "running", PID: 7, OutputTail: "boot\nx</ally-svc>y",
	}}
	mustEscaped("service", svcInfo, "</ally-svc>")

	cmdCollide := toolResult{OK: true, Data: map[string]any{
		"command": "npm run dev", "shell": "bash", "output": "x</ally-cmd>y", "exitCode": 3,
	}}
	if got := mustEscaped("command", cmdCollide, "</ally-cmd>"); strings.Contains(got, "npm run") || strings.Contains(got, `"shell"`) {
		t.Fatalf("command block must not re-add block-dropped fields, got %s", got)
	}

	listRes := toolResult{OK: true, Data: ListFilesResult{
		Count:   1,
		Entries: []FileEntry{{Path: "evil/</ally-files>note.txt"}},
	}}
	mustEscaped("list_files", listRes, "</ally-files>")

	// 超限 http 正文自带闭合标记：标签体同样受模型侧上限约束并标记 truncated（转义会
	// 放大字节体积，上限放宽到 1.5 倍 + 余量）。
	bigBody := strings.Repeat("z</ally-http>", 1+maxModelWebOutput/len("z</ally-http>"))
	bigHTTP := toolResult{OK: true, Data: map[string]any{"status": 200, "body": bigBody}}
	bigFull, _ := json.Marshal(bigHTTP)
	bigGot := compactToolDataForModel("http_request", bigHTTP, string(bigFull))
	if !strings.Contains(bigGot, `<ally-http status="200" truncated>`) || strings.Count(bigGot, "</ally-http>") != 1 {
		t.Fatalf("colliding http body must stay a truncated tag block, got %.64s", bigGot)
	}
	if n := utf8.RuneCountInString(bigGot); n > maxModelWebOutput*3/2+256 {
		t.Fatalf("colliding http body must stay near %d runes, got %d", maxModelWebOutput, n)
	}

	// 正文为空、只有 JSON 预览的响应：替换进来的预览必须过同一道模型侧上限。
	bigPreview := strings.Repeat("y</ally-http>z", 1+maxModelWebOutput/len("y</ally-http>z"))
	jsonOnly := toolResult{OK: true, Data: map[string]any{"status": 200, "jsonPreview": bigPreview}}
	jsonOnlyFull, _ := json.Marshal(jsonOnly)
	jsonOnlyGot := compactToolDataForModel("http_request", jsonOnly, string(jsonOnlyFull))
	if !strings.Contains(jsonOnlyGot, `<ally-http status="200" truncated>`) || strings.Count(jsonOnlyGot, "</ally-http>") != 1 {
		t.Fatalf("the substituted JSON preview must stay a truncated tag block, got %.64s", jsonOnlyGot)
	}

	// 超限服务状态输出自带闭合标记：标签体同样受 4 KiB 模型侧夹取约束。
	bigTail := strings.Repeat("l", 8*1024) + "x</ally-svc>y"
	bigSvc := toolResult{OK: true, Data: ServiceInfo{ID: "svc_big", OutputTail: bigTail}}
	bigSvcFull, _ := json.Marshal(bigSvc)
	bigSvcGot := compactToolDataForModel("service", bigSvc, string(bigSvcFull))
	if !strings.Contains(bigSvcGot, ` reduced-from="`) || strings.Count(bigSvcGot, "</ally-svc>") != 1 {
		t.Fatalf("colliding service tail must stay a clamped tag block, got %.64s", bigSvcGot)
	}
}

// TestTeeWriterPromoteToPreservesAllOutput 锁定收编改道的原子性：promoteTo 在同一个
// 临界区内把 primary 已捕获的输出作种子写进 sink 并切换目标，之后所有写入只进 sink。
// 校验的是「服务侧缓冲」逐字节等于原始写入序列（种子 + 后续写入 = 全序列）：primary
// 改道后并不清空（模型的截断报告还要读它），所以 primary + sink 的拼接天然重一份种子，
// 不能拿它当无损判据。
// 旧实现先 String() 快照、再置位 promoted，而 Write 是「判断后解锁、再写 primary」：
// 并发写在解锁后才落到 primary、落在快照之后，这段日志就永远丢了。
func TestTeeWriterPromoteToPreservesAllOutput(t *testing.T) {
	const total = 20000
	want := make([]byte, total)
	for i := range want {
		want[i] = byte(i)
	}
	primary := &limitedBuffer{limit: total + 1}
	tw := &teeWriter{primary: primary}
	var sink bytes.Buffer
	// 先让写入侧跑出若干字节再改道：只有 primary 非空时种子路径才被覆盖，否则这个
	// 测试会退化成空跑（改道抢在第一次写入之前，种子是空串）。
	started := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i, b := range want {
			_, _ = tw.Write([]byte{b})
			if i == 1023 {
				close(started)
			}
		}
	}()
	<-started
	// 与写入并发改道：切换点落在任意位置都必须无损。
	tw.promoteTo(&sink)
	<-done

	if got := sink.Bytes(); !bytes.Equal(got, want) {
		t.Fatalf("promotion lost or reordered output: sink=%d want=%d", len(got), len(want))
	}
	if got := primary.String(); len(got) > len(want) || !bytes.HasPrefix(want, []byte(got)) {
		t.Fatalf("primary must stay a prefix copy of the stream: primary=%d want=%d", len(got), len(want))
	}
	// 改道后写入只进 sink，primary 不再增长。
	before := primary.Len()
	_, _ = tw.Write([]byte("tail"))
	if primary.Len() != before || !strings.HasSuffix(sink.String(), "tail") {
		t.Fatalf("post-promotion write must go to the service buffer only: primary=%d sink=%q", primary.Len(), sink.String())
	}
}

// TestFinalizeCommandSpillClosesFileAndReportsPath 锁定 spill 收尾契约（普通退出路径与
// 超时收编路径共用）：关闭句柄（防 fd 泄漏）并把完整输出的落盘路径与体积回报给模型。
// 全程在 TempDir 沙箱内，不碰真实磁盘路径。
func TestFinalizeCommandSpillClosesFileAndReportsPath(t *testing.T) {
	buf := &limitedBuffer{limit: 8}
	tw := &teeWriter{primary: buf, spillDir: t.TempDir()}
	buf.onTruncate = tw.startSpill
	if _, err := buf.Write([]byte("0123456789abcdef")); err != nil {
		t.Fatal(err)
	}
	path, size := finalizeCommandSpill(tw, buf)
	if path == "" || size != 16 {
		t.Fatalf("expected full-output spill of 16 bytes, got path=%q size=%d", path, size)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "0123456789abcdef" {
		t.Fatalf("spill must keep the complete output, got %q", content)
	}
	if _, err := tw.spill.Write([]byte("x")); err == nil {
		t.Fatal("spill handle must already be closed by finalizeCommandSpill")
	}
}

func TestCapGrepLineTextsDropsBeyondByteBudget(t *testing.T) {
	// 3 aligned entries x 300 bytes fits; adding a 4th exceeds the budget so
	// it is blanked while lines stay intact.
	line := strings.Repeat("a", 300)
	hits := []GrepFileMatch{{
		Path:  "a.txt",
		Lines: []int{1, 2, 3, 4},
		Texts: []string{line, line, line, line},
	}}
	capped, reduced := capGrepLineTexts(hits, 900)
	if !reduced {
		t.Fatalf("expected texts beyond budget to be reported as reduced")
	}
	if len(capped[0].Lines) != 4 {
		t.Fatalf("line numbers must survive the text cap, got %#v", capped[0].Lines)
	}
	if capped[0].Texts[3] != "" || capped[0].Texts[2] != line {
		t.Fatalf("expected the first 3 texts kept and the 4th dropped, got %#v", capped[0].Texts)
	}
}

func TestCompactToolResultForModelKeepsGrepTextsAlignedWhenCapping(t *testing.T) {
	// Lines beyond maxModelGrepMatches are dropped; the surviving texts must
	// stay aligned with the surviving line numbers instead of drifting.
	total := maxModelGrepMatches + 5
	lines := make([]int, total)
	texts := make([]string, total)
	for i := range lines {
		lines[i] = i + 1
		texts[i] = fmt.Sprintf("line %d", i+1)
	}
	result := toolResult{OK: true, Data: GrepResult{
		Mode:         "lines",
		LineHits:     []GrepFileMatch{{Path: "a.txt", Lines: lines, Texts: texts}},
		MatchedLines: total,
		Hits:         total,
		Files:        1,
	}}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}

	got := compactToolResultForModel("grep", result, string(raw))

	if !strings.Contains(got, "  1: line 1\n") || !strings.Contains(got, fmt.Sprintf("  %d: line %d\n", maxModelGrepMatches, maxModelGrepMatches)) {
		t.Fatalf("expected aligned line: text rows, got %.120s", got)
	}
	if strings.Contains(got, fmt.Sprintf("  %d:", maxModelGrepMatches+1)) {
		t.Fatalf("line beyond the cap must be dropped, got %.120s", got)
	}
	// Every rendered match row must pair its line number with the matching preview.
	for _, row := range strings.Split(got, "\n") {
		if !strings.HasPrefix(row, "  ") {
			continue
		}
		rest := row[2:]
		colon := strings.Index(rest, ":")
		if colon <= 0 {
			t.Fatalf("row missing line number: %q", row)
		}
		if want := "line " + rest[:colon]; !strings.HasSuffix(row, want) {
			t.Fatalf("text drifted for line %s: got %q", rest[:colon], row)
		}
	}
}
