// SPDX-License-Identifier: GPL-3.0-only
package app

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestImageModelManualGenerationAndEditing(t *testing.T) {
	a := newIsolatedChatTestApp(t)
	original := imageTestPNG(t)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer test-secret" {
			t.Error("manual test did not reuse configured credentials")
		}
		switch r.URL.Path {
		case "/images/generations":
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil || payload["model"] != "test-image" || payload["prompt"] != "draw a cat" || payload["size"] != "1536x1024" {
				t.Errorf("wrong generation request: %v %v", payload, err)
			}
		case "/images/edits":
			if err := r.ParseMultipartForm(2 << 20); err != nil {
				t.Error(err)
				w.WriteHeader(400)
				return
			}
			defer r.MultipartForm.RemoveAll()
			f, _, err := r.FormFile("image[]")
			if err != nil {
				t.Error(err)
				w.WriteHeader(400)
				return
			}
			defer f.Close()
			got, _ := io.ReadAll(f)
			if !bytes.Equal(got, original) || r.FormValue("prompt") != "make it blue" {
				t.Error("editing must send the uploaded original and prompt")
			}
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]string{"b64_json": base64.StdEncoding.EncodeToString(original)}}})
	}))
	defer server.Close()
	a.config = imageTestConfig(server.URL, "openai-images")
	// Testing is useful before opting into tools and does not change any gates.
	a.config.ImageGeneration.Enabled = false
	a.config.ImageGeneration.GenerateModel = nil
	a.config.ImageGeneration.EditModel = nil
	a.config.DisabledTools = []string{"image_generate", "image_edit"}
	id := ModelIdentity{ProviderName: "test", Model: "test-image"}
	first, err := a.TestImageModel(id, ImageModelTestRequest{Prompt: "draw a cat", AspectRatio: "3:2"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := a.TestImageModel(id, ImageModelTestRequest{Prompt: "make it blue", Images: []string{generatedImageDataURL(original, "image/png")}})
	if err != nil {
		t.Fatal(err)
	}
	for _, meta := range []GeneratedImage{first, second} {
		data, stored, err := a.readGeneratedImage(meta.Ref)
		if err != nil || !bytes.Equal(data, original) || stored.SessionID != imageModelTestSession(id) {
			t.Fatalf("manual output was not preserved: %v", err)
		}
	}
	if len(second.Sources) != 1 {
		t.Fatal("missing original reference provenance")
	}
	data, source, err := a.readGeneratedImage(second.Sources[0])
	if err != nil || !source.SourceOnly || !bytes.Equal(data, original) {
		t.Fatal("reference original was not retained")
	}
	// Reopening the app recovers history without frontend state or full image reads.
	reopened := newAppState(a.AppDataDir())
	history, err := reopened.ListImageModelTests(ModelIdentity{ProviderName: "TEST", Model: "TEST-IMAGE"})
	if err != nil || len(history) != 2 || history[0].Ref != second.Ref || history[1].Ref != first.Ref {
		t.Fatalf("history lost or out of order: %+v %v", history, err)
	}
	other, err := reopened.ListImageModelTests(ModelIdentity{ProviderName: "other", Model: id.Model})
	if err != nil || len(other) != 0 {
		t.Fatal("test history leaked across model identities")
	}
	if calls.Load() != 2 || a.config.ImageGeneration.Enabled || a.config.ImageGeneration.GenerateModel != nil {
		t.Fatal("manual tests changed tool configuration or retried")
	}
	if _, err := imageModelForTool(a.config, "image_generate"); err == nil {
		t.Fatal("manual testing enabled conversation tools")
	}
}

func TestImageModelManualValidationBeforeStorageOrBilling(t *testing.T) {
	a := newIsolatedChatTestApp(t)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(500) }))
	defer server.Close()
	a.config = imageTestConfig(server.URL, "openai-images")
	id := *a.config.ImageGeneration.GenerateModel
	valid := generatedImageDataURL(imageTestPNG(t), "image/png")
	for _, input := range []ImageModelTestRequest{
		{Prompt: " "},
		{Prompt: "draw", AspectRatio: "16:9"},
		{Prompt: "draw", Images: []string{"C:\\private.png"}},
		{Prompt: "draw", Images: []string{valid, "data:image/png;base64,bm90IGFuIGltYWdl"}},
		{Prompt: "draw", Images: []string{valid, valid, valid, valid, valid, valid}},
		{Prompt: "draw", Images: []string{"data:image/png;base64," + strings.Repeat("A", (20*1024*1024)*4/3+8)}},
	} {
		if _, err := a.TestImageModel(id, input); err == nil {
			t.Fatal("invalid test input accepted")
		}
	}
	a.config.ImageGeneration.Models[0].Protocol = "minimax-images"
	if _, err := a.TestImageModel(id, ImageModelTestRequest{Prompt: "draw", Images: []string{valid}}); err == nil {
		t.Fatal("MiniMax editing accepted")
	}
	if _, err := a.TestImageModel(ModelIdentity{ProviderName: "missing", Model: id.Model}, ImageModelTestRequest{Prompt: "draw"}); err == nil {
		t.Fatal("unknown model accepted")
	}
	if calls.Load() != 0 {
		t.Fatal("invalid test made a paid request")
	}
	if _, err := os.Stat(filepath.Join(a.imagesDir(), imageSessionKey(imageModelTestSession(id)))); !os.IsNotExist(err) {
		t.Fatalf("invalid upload wrote partial references: %v", err)
	}
}

func TestImageModelManualHistoryBounded(t *testing.T) {
	a := newIsolatedChatTestApp(t)
	id := ModelIdentity{ProviderName: "test", Model: "image"}
	data := imageTestPNG(t)
	var last GeneratedImage
	for i := 0; i < imageModelTestHistoryLimit+3; i++ {
		var err error
		last, err = a.saveGeneratedImage(data, GeneratedImage{SessionID: imageModelTestSession(id)})
		if err != nil {
			t.Fatal(err)
		}
	}
	history, err := a.ListImageModelTests(id)
	if err != nil || len(history) != imageModelTestHistoryLimit || history[0].Ref != last.Ref {
		t.Fatalf("history must be bounded and newest first: %d %v", len(history), err)
	}
}
