// SPDX-License-Identifier: GPL-3.0-only
package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Shared isolation helper formerly located in the Wails window tests.
func redirectAppStateDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	return dir
}

// Exercises the native event adapter against an actual HTTP model stream,
// including checkpoint shutdown, without credentials or external services.
func TestNativeHostStreamRoundTrip(t *testing.T) {
	home := redirectAppStateDir(t)
	t.Setenv("ALLY_DATA_DIR", home)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("unexpected model path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"id\":\"test\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"native reply\"}}]}\n\n")
		fmt.Fprint(w, "data: {\"id\":\"test\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	a := NewApp()
	events := make(chan struct {
		name string
		data map[string]any
	}, 100)
	if err := a.StartNative(context.Background(), func(n string, p any) {
		if m, ok := p.(map[string]any); ok {
			events <- struct {
				name string
				data map[string]any
			}{n, m}
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := a.ShutdownNative(); err != nil {
			t.Error(err)
		}
	})
	_, err := a.StartChat(ChatRequest{SessionID: "native-test", Message: "hello", Config: ConfigState{Workspace: t.TempDir(), ProviderName: "test", Model: "mock", APIFormat: "openai", BaseURL: server.URL, APIKey: "test", MaxTokens: 128}})
	if err != nil {
		t.Fatal(err)
	}
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	var text string
	for {
		select {
		case e := <-events:
			if e.name == "run:stream" {
				text += stringValue(e.data["content"])
			}
			if e.name == "run:error" {
				t.Fatalf("run failed: %v", e.data)
			}
			if e.name == "run:done" {
				if text != "native reply" {
					t.Fatalf("stream = %q", text)
				}
				return
			}
		case <-timer.C:
			t.Fatal("native stream timed out")
		}
	}
}

func TestNativeAttachmentModelRoundTrip(t *testing.T) {
	home := redirectAppStateDir(t)
	t.Setenv("ALLY_DATA_DIR", home)
	requests := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		requests <- string(body)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"id\":\"test\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"}}]}\n\ndata: {\"id\":\"test\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	events := make(chan struct {
		name string
		data map[string]any
	}, 100)
	a := NewApp()
	if err := a.StartNative(context.Background(), func(n string, p any) {
		if m, ok := p.(map[string]any); ok {
			events <- struct {
				name string
				data map[string]any
			}{n, m}
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := a.ShutdownNative(); err != nil {
			t.Error(err)
		}
	})
	a.mu.Lock()
	a.config.ImageGeneration = &ImageGenerationConfig{Enabled: true}
	a.mu.Unlock()
	var pngBytes bytes.Buffer
	if err := png.Encode(&pngBytes, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	dataURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString(pngBytes.Bytes())
	_, err := a.StartChat(ChatRequest{SessionID: "native-image", Message: "inspect", Attachments: []AttachmentInput{{ID: "img1", Name: "image.png", Kind: "image", Type: "image/png", DataURL: dataURL}, {ID: "text1", Name: "note.txt", Kind: "text", Text: "attachment test marker"}}, Config: ConfigState{Workspace: t.TempDir(), ProviderName: "test", Model: "mock", APIFormat: "openai", BaseURL: server.URL, APIKey: "test", MaxTokens: 128, ImageGeneration: &ImageGenerationConfig{Enabled: true}}})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	var ref string
	var done bool
	for !done {
		select {
		case e := <-events:
			switch e.name {
			case "run:attachment":
				ref = stringValue(e.data["imageRef"])
			case "run:error":
				t.Fatalf("run failed: %v", e.data)
			case "run:done":
				done = true
			}
		case <-deadline.C:
			t.Fatal("attachment run timed out")
		}
	}
	if ref == "" {
		t.Fatal("no durable attachment reference")
	}
	stored, err := a.ReadNativeGeneratedImage(ref)
	if err != nil || !bytes.Equal(stored, pngBytes.Bytes()) {
		t.Fatalf("stored image: %v", err)
	}
	select {
	case body := <-requests:
		if !strings.Contains(body, "data:image/png;base64,") || !strings.Contains(body, "attachment test marker") {
			t.Fatal("model request omitted the image or text")
		}
	default:
		t.Fatal("no model request")
	}
}

func TestNativeImageConfigPreservesUnrelatedSettings(t *testing.T) {
	home := redirectAppStateDir(t)
	t.Setenv("ALLY_DATA_DIR", home)
	a := NewApp()
	if err := a.StartNative(context.Background(), func(string, any) {}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := a.ShutdownNative(); err != nil {
			t.Error(err)
		}
	})
	cfg, err := a.GetConfig()
	if err != nil {
		t.Fatal(err)
	}
	cfg.CustomPrompt = "keep this setting"
	if err = a.SaveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	images := ImageGenerationConfig{Enabled: true, Models: []ImageModelConfig{{ProviderName: "p", Model: "m", Protocol: "openai-images", BaseURL: "https://example.com", APIKeys: []string{"secret"}}}}
	if err = a.SaveNativeImageConfig(images); err != nil {
		t.Fatal(err)
	}
	images.Models[0].APIKeys[0] = "mutated"
	got, err := a.GetConfig()
	if err != nil {
		t.Fatal(err)
	}
	if got.CustomPrompt != "keep this setting" || got.ImageGeneration == nil || !got.ImageGeneration.Enabled || got.ImageGeneration.Models[0].APIKeys[0] != "secret" {
		t.Fatal("image config update lost unrelated settings or retained caller-owned memory")
	}
}

func TestNativeModelsKeepOAuthAccountsSeparate(t *testing.T) {
	home := redirectAppStateDir(t)
	t.Setenv("ALLY_DATA_DIR", home)
	a := NewApp()
	if err := a.StartNative(context.Background(), func(string, any) {}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := a.ShutdownNative(); err != nil {
			t.Error(err)
		}
	})
	cfg, err := a.GetConfig()
	if err != nil {
		t.Fatal(err)
	}
	cfg.CustomPrompt = "preserve me"
	if err = a.SaveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	first := ModelConfig{AuthType: "chatgpt_oauth", ProviderName: "ChatGPT Official", Model: "same-model", OAuthAccountID: "account-1", APIFormat: "openai_responses"}
	second := first
	second.OAuthAccountID = "account-2"
	if err = a.SaveNativeModel(first, nil); err != nil {
		t.Fatal(err)
	}
	if err = a.SaveNativeModel(second, nil); err != nil {
		t.Fatal(err)
	}
	if err = a.SelectNativeModel(identityOfModel(second)); err != nil {
		t.Fatal(err)
	}
	cfg, err = a.GetConfig()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Models) != 2 || cfg.LastUsedModel.OAuthAccountID != "account-2" || cfg.CustomPrompt != "preserve me" {
		t.Fatal("separate accounts or settings lost")
	}
	renamed := second
	renamed.Model = "renamed"
	old := identityOfModel(second)
	if err = a.SaveNativeModel(renamed, &old); err != nil {
		t.Fatal(err)
	}
	cfg, _ = a.GetConfig()
	if cfg.LastUsedModel.OAuthAccountID != "account-2" || cfg.LastUsedModel.Model != "renamed" {
		t.Fatal("editing lost selected identity")
	}
	if err = a.DeleteNativeModel(identityOfModel(first)); err != nil {
		t.Fatal(err)
	}
	cfg, _ = a.GetConfig()
	if len(cfg.Models) != 1 || cfg.Models[0].OAuthAccountID != "account-2" || cfg.LastUsedModel.OAuthAccountID != "account-2" {
		t.Fatal("deletion affected another account")
	}
	if err = a.SaveNativeModel(renamed, nil); err == nil {
		t.Fatal("duplicate account/model accepted")
	}
}
