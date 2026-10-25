// SPDX-License-Identifier: GPL-3.0-only
package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"ally-dev/internal/tools/imagegen"
	openai "github.com/sashabaranov/go-openai"
)

func imageTestPNG(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 3, 2))); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func imageTestConfig(url, protocol string) ConfigState {
	m := ImageModelConfig{ProviderName: "test", Model: "test-image", Protocol: protocol, BaseURL: url, APIKey: "test-secret"}
	id := &ModelIdentity{ProviderName: m.ProviderName, Model: m.Model}
	return ConfigState{ImageGeneration: &ImageGenerationConfig{Enabled: true, Models: []ImageModelConfig{m}, GenerateModel: id, EditModel: id}}
}

func TestImageToolGatesAndSessionFreeze(t *testing.T) {
	a := &App{}
	cfg := imageTestConfig("https://example.com/v1", "openai-images")
	has := func(tools []openai.Tool, name string) bool {
		for _, tool := range tools {
			if tool.Function.Name == name {
				return true
			}
		}
		return false
	}
	for _, tc := range []struct {
		name           string
		change         func(*ConfigState)
		generate, edit bool
	}{
		{"enabled", func(*ConfigState) {}, true, true},
		{"no configuration", func(c *ConfigState) { c.ImageGeneration = nil }, false, false},
		{"not in use", func(c *ConfigState) { c.ImageGeneration.Enabled = false }, false, false},
		{"no models", func(c *ConfigState) { c.ImageGeneration.Models = nil }, false, false},
		{"no defaults", func(c *ConfigState) { c.ImageGeneration.GenerateModel = nil; c.ImageGeneration.EditModel = nil }, false, false},
		{"no key", func(c *ConfigState) { c.ImageGeneration.Models[0].APIKey = "" }, false, false},
		{"generate disabled", func(c *ConfigState) { c.DisabledTools = []string{"image_generate"} }, false, true},
		{"edit disabled", func(c *ConfigState) { c.DisabledTools = []string{"image_edit"} }, true, false},
		{"MiniMax generation only", func(c *ConfigState) { c.ImageGeneration.Models[0].Protocol = "minimax-images" }, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := cloneConfig(cfg)
			tc.change(&c)
			tools := a.buildToolsForConfig(c)
			if has(tools, "image_generate") != tc.generate || has(tools, "image_edit") != tc.edit {
				t.Fatal("wrong image tool injection")
			}
			a.config = cloneConfig(c)
			inventory := make(map[string]bool)
			for _, tool := range a.ListTools() {
				inventory[tool.Name] = tool.Enabled
			}
			for name, expected := range map[string]bool{"image_generate": tc.generate, "image_edit": tc.edit} {
				value, listed := inventory[name]
				if !listed || value != expected {
					t.Fatalf("tool inventory disagrees with injection: %s listed=%v enabled=%v", name, listed, value)
				}
			}
		})
	}
	before := a.buildToolsForSession("frozen", cfg)
	cfg.ImageGeneration.Enabled = false
	after := a.buildToolsForSession("frozen", cfg)
	if !has(before, "image_generate") || !has(after, "image_generate") || has(a.buildToolsForSession("new", cfg), "image_generate") {
		t.Fatal("image schemas must follow existing session freeze")
	}
}

func TestImageConfigRoundTripAndOwnership(t *testing.T) {
	a := configTransactionApp(t)
	cfg := imageTestConfig("https://example.com/v1", "xai-images")
	cfg.ImageGeneration.Models[0].APIKeys = []string{"first", "second"}
	cfg.ImageGeneration.Models[0].CustomHeaders = map[string]string{"X-Image": "value"}
	if err := a.SaveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	cfg.ImageGeneration.Models[0].APIKeys[0] = "changed"
	cfg.ImageGeneration.GenerateModel.Model = "changed"
	got, err := a.GetConfig()
	if err != nil {
		t.Fatal(err)
	}
	if got.ImageGeneration.Models[0].APIKeys[0] != "first" || got.ImageGeneration.GenerateModel.Model != "test-image" {
		t.Fatal("saved config aliases caller")
	}
	got.ImageGeneration.Models[0].CustomHeaders["X-Image"] = "changed"
	again, _ := a.GetConfig()
	if again.ImageGeneration.Models[0].CustomHeaders["X-Image"] != "value" {
		t.Fatal("returned config aliases state")
	}
	loaded, err := readConfigFile(a.configPath)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ImageGeneration.Models[0].APIKey != "first" || loaded.ImageGeneration.EditModel.Model != "test-image" {
		t.Fatal("image configuration not persisted")
	}
	if err := a.SaveConfig(ConfigState{}); err != nil {
		t.Fatal(err)
	}
	again, _ = a.GetConfig()
	if again.ImageGeneration == nil {
		t.Fatal("unrelated settings save erased image configuration")
	}
}

func TestImageOriginalAndEditSurviveRestart(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	a := &App{dataDir: dir}
	data := imageTestPNG(t)
	first, err := a.saveGeneratedImage(data, GeneratedImage{SessionID: "session", Model: "first", Prompt: "cat"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := a.saveGeneratedImage(data, GeneratedImage{SessionID: "session", Model: "second", Sources: []string{first.Ref}})
	if err != nil {
		t.Fatal(err)
	}
	if first.Ref == second.Ref {
		t.Fatal("edit replaced original")
	}
	restarted := &App{dataDir: dir}
	for _, saved := range []GeneratedImage{first, second} {
		got, meta, err := restarted.readGeneratedImage(saved.Ref)
		if err != nil || !bytes.Equal(got, data) || meta.Ref != saved.Ref {
			t.Fatalf("lost original: %v", err)
		}
	}
	_, meta, _ := restarted.readGeneratedImage(second.Ref)
	if len(meta.Sources) != 1 || meta.Sources[0] != first.Ref {
		t.Fatal("lost edit provenance")
	}
	if latest, err := restarted.latestGeneratedImage("session"); err != nil || latest != second.Ref {
		t.Fatalf("wrong latest output: %q, %v", latest, err)
	}
	for _, ref := range []string{"image:../../config.json", "image:" + imageSessionKey("session") + "/../config", "https://example.com/image.png"} {
		if _, _, err := a.readGeneratedImage(ref); err == nil {
			t.Fatal("accepted invalid artifact reference")
		}
	}
	for _, data := range [][]byte{[]byte("not an image"), bytes.Repeat([]byte{'x'}, imagegen.MaxImageBytes+1)} {
		if _, err := a.saveGeneratedImage(data, GeneratedImage{SessionID: "session"}); err == nil {
			t.Fatal("accepted invalid image")
		}
	}
	r := httptest.NewRequest(http.MethodGet, first.URL, nil)
	w := httptest.NewRecorder()
	GeneratedImageMiddleware(a)(http.NotFoundHandler()).ServeHTTP(w, r)
	if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), data) || w.Header().Get("Content-Type") != "image/png" {
		t.Fatal("original asset unavailable")
	}
}

func TestImageHistoryRecoversMissingSnapshotAttachments(t *testing.T) {
	a := newIsolatedChatTestApp(t)
	stored, err := a.saveGeneratedImage(imageTestPNG(t), GeneratedImage{SessionID: "image-session"})
	if err != nil {
		t.Fatal(err)
	}
	history := []openai.ChatCompletionMessage{
		{Role: "user", Content: "draw"},
		{Role: "assistant", ToolCalls: []openai.ToolCall{{ID: "image-call", Type: "function", Function: openai.FunctionCall{Name: "image_generate", Arguments: `{"prompt":"draw"}`}}}},
		{Role: "tool", ToolCallID: "image-call", Content: generatedImageHistoryMarker + stored.Ref + " (3x2); original saved."},
	}
	a.saveHistory("image-session", history)
	if err := a.SaveSession(SessionSnapshot{ID: "image-session", Messages: []map[string]any{{"role": "user", "content": "draw"}}}); err != nil {
		t.Fatal(err)
	}
	// A fresh instance must recover from disk, not the in-memory history cache.
	restarted := NewApp()
	restarted.initialized = true
	restarted.dataDir, restarted.sessionsDir, restarted.historiesDir = a.dataDir, a.sessionsDir, a.historiesDir
	snapshot, err := restarted.LoadSession("image-session")
	if err != nil || len(snapshot.Messages) != 2 {
		t.Fatalf("missing recovered image: %v, %+v", err, snapshot.Messages)
	}
	attachment := snapshot.Messages[1]["attachments"].([]any)[0].(map[string]any)
	if card := snapshot.Messages[1]; card["role"] != "tool_call" || card["toolCallId"] != "image-call" || card["name"] != "image_generate" {
		t.Fatal("recovered image did not belong to its tool card")
	}
	if attachment["imageRef"] != stored.Ref || attachment["url"] != stored.URL || attachment["dataUrl"] != nil {
		t.Fatal("recovered attachment lost original or embedded full image")
	}
	if err := restarted.SaveSession(snapshot); err != nil {
		t.Fatal(err)
	}
	snapshot, err = restarted.LoadSession("image-session")
	if err != nil || len(snapshot.Messages) != 2 {
		t.Fatal("recovery duplicated an existing image")
	}
	other := SessionSnapshot{ID: "another-session"}
	restarted.restoreGeneratedImageAttachments(&other, history)
	if len(other.Messages) != 0 {
		t.Fatal("recovery leaked another session's image")
	}
}

func TestImageHistoryMovesLegacyAssistantImagesToTheirTools(t *testing.T) {
	a := newIsolatedChatTestApp(t)
	const sessionID = "legacy-image-tools"
	history := []openai.ChatCompletionMessage{}
	cards := []map[string]any{}
	attachments := []any{}
	refs := []string{}
	for _, id := range []string{"front", "back"} {
		stored, err := a.saveGeneratedImage(imageTestPNG(t), GeneratedImage{SessionID: sessionID, Prompt: id})
		if err != nil {
			t.Fatal(err)
		}
		refs = append(refs, stored.Ref)
		cards = append(cards, map[string]any{"role": "tool_call", "name": "image_edit", "toolCallId": id, "status": "success"})
		attachments = append(attachments, map[string]any{"imageRef": stored.Ref, "url": stored.URL, "previewUrl": "thumbnail-" + id, "generated": true})
		history = append(history, openai.ChatCompletionMessage{Role: "tool", ToolCallID: id, Content: generatedImageHistoryMarker + stored.Ref + " (3x2); original saved."})
	}
	assistant := map[string]any{"role": "assistant", "content": "done", "attachments": attachments}
	snapshot := SessionSnapshot{ID: sessionID, Messages: append(cards, assistant)}
	for range 2 {
		a.restoreGeneratedImageAttachments(&snapshot, history)
	}
	if len(snapshot.Messages) != 3 || len(assistant["attachments"].([]any)) != 0 || assistant["content"] != "done" {
		t.Fatal("legacy image migration duplicated rows or lost assistant text")
	}
	for i, card := range cards {
		images := card["attachments"].([]any)
		if len(images) != 1 {
			t.Fatal("image did not move to exactly one tool card")
		}
		image := images[0].(map[string]any)
		if image["imageRef"] != refs[i] || image["previewUrl"] != "thumbnail-"+card["toolCallId"].(string) || card["title"] != card["toolCallId"] {
			t.Fatal("image migrated to another call or lost its preview/prompt")
		}
	}
}

func TestImageUploadReferencesPreserveSources(t *testing.T) {
	a := newIsolatedChatTestApp(t)
	data := imageTestPNG(t)
	original := []AttachmentInput{{ID: "upload", Type: "image/png", DataURL: generatedImageDataURL(data, "image/png")}}
	messages := []ChatMessageInput{{Role: "user", Attachments: original}}
	req := ChatRequest{SessionID: "upload-session", Attachments: original, Messages: messages}
	if err := a.persistRunImageAttachments(&req, "run"); err != nil {
		t.Fatal(err)
	}
	if original[0].ImageRef != "" || messages[0].Attachments[0].ImageRef != "" || req.Attachments[0].ImageRef == "" {
		t.Fatal("upload reference mutated caller data or was not assigned")
	}
	if req.Messages[0].Attachments[0].ImageRef != req.Attachments[0].ImageRef {
		t.Fatal("desktop and API attachments did not share the saved source")
	}
	got, meta, err := a.readGeneratedImage(req.Attachments[0].ImageRef)
	if err != nil || !meta.SourceOnly || !bytes.Equal(got, data) {
		t.Fatal("upload lost its original source")
	}
	if _, err := a.latestGeneratedImage(req.SessionID); err == nil {
		t.Fatal("uploaded source became the latest generated output")
	}
}

func TestDesktopPastedImageCanBeEdited(t *testing.T) {
	a := newIsolatedChatTestApp(t)
	a.stats = nil
	recorder := &compactRecorder{done: make(chan struct{})}
	a.events = recorder
	data := imageTestPNG(t)
	dataURL := generatedImageDataURL(data, "image/png")
	var edits atomic.Int32
	imageServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		edits.Add(1)
		var request struct {
			Image struct {
				URL string `json:"url"`
			} `json:"image"`
		}
		if r.URL.Path != "/v1/images/edits" || json.NewDecoder(r.Body).Decode(&request) != nil || request.Image.URL != dataURL {
			t.Error("editing endpoint did not receive the pasted original")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{{"b64_json": base64.StdEncoding.EncodeToString(data)}}})
	}))
	defer imageServer.Close()
	// Old clients supplied a fabricated workspace path. A durable reference
	// must hide that hint from the model as well as preserve the source bytes.
	fakePath := filepath.Join(a.config.Workspace, "clipboard-only.png")
	refPattern := regexp.MustCompile(`image:[a-f0-9]{64}/[a-f0-9]{32}`)
	chat := &planCheckServer{script: func(round int, request string) string {
		if round == 1 {
			ref := refPattern.FindString(request)
			if ref == "" || !strings.Contains(request, "image reference for image_edit") {
				t.Error("desktop attachment never received an editing reference")
				return sseChatFinishChunk("stop") + sseDone
			}
			var decoded map[string]any
			_ = json.Unmarshal([]byte(request), &decoded)
			readable := fmt.Sprint(decoded["messages"])
			if strings.Contains(readable, fakePath) || !strings.Contains(readable, dataURL) {
				t.Error("attachment lost its image input or exposed a fabricated file path")
			}
			args, _ := json.Marshal(map[string]any{"prompt": "draw a front view", "images": []string{ref}})
			return sseChatToolCallChunk("edit_pasted", "image_edit", string(args)) + sseChatFinishChunk("tool_calls") + sseDone
		}
		if !strings.Contains(request, "Generated image: image:") {
			t.Error("editing did not return a saved image reference")
		}
		return sseChatChunk("done") + sseChatFinishChunk("stop") + sseDone
	}}
	chatServer := httptest.NewServer(chat.handler())
	defer chatServer.Close()
	cfg := a.config
	cfg.APIFormat, cfg.BaseURL, cfg.Model = apiFormatOpenAIChat, chatServer.URL+"/v1", "test-model"
	cfg.APIKeys, cfg.MaxTokens = []string{"test-key"}, 64
	cfg.ImageGeneration = imageTestConfig(imageServer.URL+"/v1", "xai-images").ImageGeneration
	a.config = cfg
	const sessionID = "desktop-pasted-image"
	_, err := a.StartChat(ChatRequest{
		SessionID: sessionID, Message: "edit this pasted image", Config: cfg,
		Messages: []ChatMessageInput{{Role: "user", Content: "edit this pasted image", Attachments: []AttachmentInput{{
			ID: "paste", Name: "clipboard-only.png", Type: "image/png", Kind: "image",
			DataURL: dataURL, FilePath: fakePath,
		}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-recorder.done:
	case <-time.After(15 * time.Second):
		t.Fatal("pasted image edit did not finish")
	}
	if recorder.endStatus() != "run:done" || edits.Load() != 1 || chat.count() != 2 {
		t.Fatalf("image editing failed: status=%s edits=%d rounds=%d errors=%v", recorder.endStatus(), edits.Load(), chat.count(), recorder.errorEvents())
	}
	ref, err := a.latestGeneratedImage(sessionID)
	if err != nil {
		t.Fatal(err)
	}
	_, edited, err := a.readGeneratedImage(ref)
	if err != nil || len(edited.Sources) != 1 {
		t.Fatalf("edited image lost its source: %+v, %v", edited, err)
	}
	original, source, err := a.readGeneratedImage(edited.Sources[0])
	if err != nil || !source.SourceOnly || !bytes.Equal(original, data) {
		t.Fatal("pasted original was not preserved")
	}
}

func TestImageOriginalAPIRequiresAuthentication(t *testing.T) {
	handler, a, token := newApiTestHandler(t)
	data := imageTestPNG(t)
	stored, err := a.saveGeneratedImage(data, GeneratedImage{SessionID: "api-session"})
	if err != nil {
		t.Fatal(err)
	}
	for _, authorized := range []bool{false, true} {
		r := httptest.NewRequest(http.MethodGet, stored.URL, nil)
		if authorized {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if authorized {
			if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), data) {
				t.Fatal("authenticated original download failed")
			}
		} else if w.Code != http.StatusUnauthorized {
			t.Fatal("original was accessible without authentication")
		}
	}
}

func TestImageProviderWireAndNoBillableRetries(t *testing.T) {
	data := imageTestPNG(t)
	for _, protocol := range []string{"openai-images", "xai-images"} {
		for _, count := range []int{0, 1, 2} {
			t.Run(fmt.Sprintf("%s references=%d", protocol, count), func(t *testing.T) {
				edit := count > 0
				var requests atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					requests.Add(1)
					wantPath := "/v1/images/generations"
					if edit {
						wantPath = "/v1/images/edits"
					}
					if r.URL.Path != wantPath || r.Header.Get("Authorization") != "Bearer test-secret" || r.Header.Get("X-Image") != "image-value" {
						t.Error("wrong endpoint or model credentials")
					}
					if protocol == "openai-images" && edit {
						if err := r.ParseMultipartForm(imagegen.MaxImageBytes); err != nil {
							t.Error(err)
							return
						}
						defer r.MultipartForm.RemoveAll()
						if r.FormValue("prompt") != "draw" || r.FormValue("size") != "1536x1024" {
							t.Error("wrong multipart fields")
						}
						files := r.MultipartForm.File["image[]"]
						if len(files) != count {
							t.Error("missing image upload")
							return
						}
						f, err := files[0].Open()
						if err != nil {
							t.Error(err)
							return
						}
						defer f.Close()
						got, _ := io.ReadAll(f)
						if !bytes.Equal(got, data) {
							t.Error("reference bytes changed")
						}
					} else {
						var fields map[string]any
						if json.NewDecoder(r.Body).Decode(&fields) != nil {
							t.Error("invalid JSON")
							return
						}
						if fields["model"] != "test-image" || fields["prompt"] != "draw" {
							t.Error("missing model or prompt")
						}
						if protocol == "openai-images" {
							if _, ok := fields["response_format"]; ok {
								t.Error("GPT Image does not accept response_format")
							}
						} else {
							if fields["response_format"] != "b64_json" || fields["aspect_ratio"] != "3:2" {
								t.Error("wrong xAI fields")
							}
							if edit {
								refs := []any{fields["image"]}
								if count > 1 {
									refs, _ = fields["images"].([]any)
								}
								if len(refs) != count {
									t.Error("wrong xAI reference count")
								}
								for _, raw := range refs {
									ref, _ := raw.(map[string]any)
									if ref["type"] != "image_url" || ref["url"] != generatedImageDataURL(data, "image/png") {
										t.Error("wrong xAI reference")
									}
								}
							}
						}
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{{"b64_json": base64.StdEncoding.EncodeToString(data)}}})
				}))
				defer server.Close()
				cfg := imageTestConfig(server.URL+"/v1", protocol)
				m := cfg.ImageGeneration.Models[0]
				m.CustomHeaders = map[string]string{"X-Image": "image-value"}
				var inputs []imageInput
				for range count {
					inputs = append(inputs, imageInput{Data: data, Mime: "image/png"})
				}
				got, err := (&App{}).requestImage(t.Context(), cfg, m, imagegen.Request{Prompt: "draw", AspectRatio: "3:2"}, inputs)
				if err != nil || !bytes.Equal(got, data) || requests.Load() != 1 {
					t.Fatalf("generation failed: %v", err)
				}
			})
		}
	}
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(429)
		_, _ = w.Write([]byte("test-secret"))
	}))
	defer server.Close()
	cfg := imageTestConfig(server.URL, "openai-images")
	_, err := (&App{}).requestImage(t.Context(), cfg, cfg.ImageGeneration.Models[0], imagegen.Request{Prompt: "draw"}, nil)
	if err == nil || strings.Contains(err.Error(), "test-secret") || calls.Load() != 1 {
		t.Fatal("request was retried or key leaked")
	}
}

func TestImageKeyFallbackRequiresDefinitiveRejection(t *testing.T) {
	for _, status := range []int{401, 403, 429, 500} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Header.Get("Authorization") == "Bearer rejected" {
					w.WriteHeader(status)
					return
				}
				var fields map[string]any
				if json.NewDecoder(r.Body).Decode(&fields) != nil || fields["prompt"] != "draw" {
					t.Error("fallback lost request body")
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{{"b64_json": base64.StdEncoding.EncodeToString(imageTestPNG(t))}}})
			}))
			defer server.Close()
			cfg := imageTestConfig(server.URL, "openai-images")
			model := cfg.ImageGeneration.Models[0]
			model.APIKeys = []string{"rejected", "accepted"}
			_, err := (&App{}).requestImage(t.Context(), cfg, model, imagegen.Request{Prompt: "draw"}, nil)
			if status == 500 {
				if err == nil || calls.Load() != 1 {
					t.Fatal("retried an ambiguous billable failure")
				}
			} else if err != nil || calls.Load() != 2 {
				t.Fatalf("failed safe key fallback: %v", err)
			}
		})
	}
}

func TestMiniMaxImageProtocolAndPrivateDownloadGuard(t *testing.T) {
	var downloads atomic.Int32
	imageServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { downloads.Add(1) }))
	defer imageServer.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var fields map[string]any
		_ = json.NewDecoder(r.Body).Decode(&fields)
		if r.URL.Path != "/v1/image_generation" || fields["response_format"] != "url" || fields["prompt_optimizer"] != false || fields["aspect_ratio"] != "2:3" {
			t.Error("wrong MiniMax wire format")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"image_urls": []string{imageServer.URL}}, "base_resp": map[string]any{"status_code": 0}})
	}))
	defer server.Close()
	cfg := imageTestConfig(server.URL+"/v1", "minimax-images")
	_, err := (&App{}).requestImage(t.Context(), cfg, cfg.ImageGeneration.Models[0], imagegen.Request{Prompt: "draw", AspectRatio: "2:3"}, nil)
	if err == nil || downloads.Load() != 0 {
		t.Fatal("provider output accessed private network")
	}
}

func TestImageEditingExecutionAndReferenceHistory(t *testing.T) {
	a := configTransactionApp(t)
	data := imageTestPNG(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{{"b64_json": base64.StdEncoding.EncodeToString(data)}}})
	}))
	defer server.Close()
	a.config = imageTestConfig(server.URL, "xai-images")
	original, err := a.saveGeneratedImage(data, GeneratedImage{SessionID: "session"})
	if err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]any{"prompt": "edit", "images": []string{original.Ref}})
	result := a.executeTool(context.Background(), a.config, "session", "image_edit", args)
	if !result.OK {
		t.Fatal(result.Error)
	}
	output := result.Data.(GeneratedImage)
	if len(output.Sources) != 1 || output.Sources[0] != original.Ref {
		t.Fatal("edit lost original reference")
	}
	modelText := compactToolResultForModel("image_edit", result, "")
	history := []openai.ChatCompletionMessage{{Role: "assistant", ToolCalls: []openai.ToolCall{{ID: "call", Type: "function", Function: openai.FunctionCall{Name: "image_edit", Arguments: string(args)}}}}, {Role: "tool", ToolCallID: "call", Content: modelText}}
	disk := sanitizeHistoryMessagesForDisk(history)
	if len(disk) != 2 || disk[1].Content != modelText {
		t.Fatal("restart history lost image reference")
	}
	if strings.Contains(modelText, "base64") || !strings.Contains(modelText, output.Ref) {
		t.Fatal("bad image result compacting")
	}
	if result := a.executeTool(context.Background(), a.config, "other-session", "image_edit", args); result.OK {
		t.Fatal("accepted another session's reference")
	}
	a.config.ImageGeneration.Enabled = false
	if result := a.executeTool(context.Background(), a.config, "session", "image_edit", args); result.OK {
		t.Fatal("executed disabled frozen tool")
	}
	if _, err := os.Stat(filepath.Join(a.imagesDir(), strings.TrimPrefix(original.Ref, "image:")+".png")); err != nil {
		t.Fatal("original removed after edit")
	}
}
