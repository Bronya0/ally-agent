// SPDX-License-Identifier: GPL-3.0-only
package app

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestRunChatInjectedPastedImageCanBeEdited(t *testing.T) {
	for _, boundary := range []string{"after-tool-batch", "after-final-answer", "after-suggest"} {
		t.Run(boundary, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			a := newIsolatedChatTestApp(t)
			a.stats = nil
			recorder := &compactRecorder{done: make(chan struct{})}
			events := &compactEventRecorder{}
			a.events = nativeEventSink{send: func(name string, payload any) {
				events.Emit(name, payload)
				recorder.Emit(name, payload)
			}}
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
					t.Error("editing endpoint did not receive the injected clipboard image")
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{{"b64_json": base64.StdEncoding.EncodeToString(data)}}})
			}))
			defer imageServer.Close()
			runIDReady := make(chan string, 1)
			refPattern := regexp.MustCompile(`image:[a-f0-9]{64}/[a-f0-9]{32}`)
			chat := &planCheckServer{script: func(round int, request string) string {
				if round == 1 {
					select {
					case runID := <-runIDReady:
						if err := a.InjectRunMessage(runID, "edit the image pasted while running", AttachmentInput{
							ID: "live-paste", Name: "剪贴板图片.png", Kind: "image", Type: "image/png", DataURL: dataURL,
						}); err != nil {
							t.Error(err)
						}
					case <-time.After(5 * time.Second):
						t.Error("run ID was not published")
					}
					switch boundary {
					case "after-tool-batch":
						return sseChatToolCallChunk("calculate_first", "calculate", `{"expression":"1+1"}`) + sseChatFinishChunk("tool_calls") + sseDone
					case "after-suggest":
						return sseChatToolCallChunk("suggest_first", "suggest", `{"items":["1. 继续"]}`) + sseChatFinishChunk("tool_calls") + sseDone
					default:
						return sseChatChunk("first answer") + sseChatFinishChunk("stop") + sseDone
					}
				}
				if round == 2 {
					ref := refPattern.FindString(request)
					if ref == "" || !strings.Contains(request, "image reference for image_edit") {
						t.Error("injected image never received an editing reference")
						return sseChatFinishChunk("stop") + sseDone
					}
					if !strings.Contains(request, dataURL) {
						t.Error("injected source lost its multimodal image input")
					}
					args, _ := json.Marshal(map[string]any{"prompt": "draw a front view", "images": []string{ref}})
					return sseChatToolCallChunk("edit_live_paste", "image_edit", string(args)) + sseChatFinishChunk("tool_calls") + sseDone
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
			const sessionID = "injected-pasted-image"
			runID, err := a.StartChat(ChatRequest{SessionID: sessionID, Message: "start", Config: cfg})
			if err != nil {
				t.Fatal(err)
			}
			runIDReady <- runID
			select {
			case <-recorder.done:
			case <-time.After(15 * time.Second):
				t.Fatal("injected image edit did not finish")
			}
			if recorder.endStatus() != "run:done" || edits.Load() != 1 || chat.count() != 3 {
				t.Fatalf("image editing failed: status=%s edits=%d rounds=%d errors=%v", recorder.endStatus(), edits.Load(), chat.count(), recorder.errorEvents())
			}
			payloads := events.payloads("run:attachment")
			if len(payloads) != 1 {
				t.Fatalf("expected one source-reference event, got %v", payloads)
			}
			var event map[string]string
			if err := json.Unmarshal([]byte(payloads[0]), &event); err != nil {
				t.Fatal(err)
			}
			if event["runId"] != runID || event["sessionId"] != sessionID || event["id"] != "live-paste" || event["imageRef"] == "" {
				t.Fatalf("source reference was routed to the wrong attachment: %v", event)
			}
			original, source, err := newAppState(a.AppDataDir()).readGeneratedImage(event["imageRef"])
			if err != nil || !source.SourceOnly || !bytes.Equal(original, data) {
				t.Fatalf("injected source did not survive reopening: %+v, %v", source, err)
			}
		})
	}
}

func TestAppendPendingRunImagesUseRunConfigAndShareSources(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	for _, mode := range []string{"enabled", "disabled", "unconfigured"} {
		t.Run(mode, func(t *testing.T) {
			a := newIsolatedChatTestApp(t)
			events := &compactEventRecorder{}
			a.events = events
			data := imageTestPNG(t)
			attachments := []AttachmentInput{
				{ID: "note", Name: "note.txt", Text: "text attachment"},
				{ID: "paste", Name: "剪贴板图片.png", Kind: "image", Type: "image/png", DataURL: generatedImageDataURL(data, "image/png")},
			}
			cfg := ConfigState{}
			if mode != "unconfigured" {
				cfg.ImageGeneration = &ImageGenerationConfig{Enabled: mode == "enabled"}
			}
			// The live global config is deliberately opposite to the run config.
			a.config.ImageGeneration = &ImageGenerationConfig{Enabled: mode != "enabled"}
			a.runInputs["run"] = make(chan runInput, runInputBufferSize)
			for _, text := range []string{"first", "second"} {
				if err := a.InjectRunMessage("run", text, attachments...); err != nil {
					t.Fatal(err)
				}
			}
			messages, injected, err := a.appendPendingRunInputs("run", "source-session", cfg, nil)
			if err != nil || !injected || len(messages) != 2 {
				t.Fatalf("queued inputs lost: injected=%v messages=%d err=%v", injected, len(messages), err)
			}
			if attachments[1].ImageRef != "" {
				t.Fatal("preprocessing mutated caller attachments")
			}
			refPattern := regexp.MustCompile(`image:[a-f0-9]{64}/[a-f0-9]{32}`)
			var savedRef string
			for i, message := range messages {
				if len(message.MultiContent) != 2 || !strings.Contains(message.MultiContent[0].Text, []string{"first", "second"}[i]) || !strings.Contains(message.MultiContent[0].Text, "text attachment") {
					t.Fatal("mixed attachments or queued message order lost")
				}
				ref := refPattern.FindString(message.MultiContent[0].Text)
				if mode == "enabled" {
					if ref == "" || (savedRef != "" && savedRef != ref) {
						t.Fatal("same-batch duplicate sources did not share an editing reference")
					}
					savedRef = ref
				} else if ref != "" {
					t.Fatal("image references generated despite disabled run config")
				}
			}
			if mode == "enabled" {
				original, source, err := a.readGeneratedImage(savedRef)
				if err != nil || source.SessionID != "source-session" || !source.SourceOnly || !bytes.Equal(original, data) {
					t.Fatalf("source bytes or session lost: %+v, %v", source, err)
				}
				if len(events.payloads("run:attachment")) != 2 {
					t.Fatal("reference events missing for duplicate attachments")
				}
			} else if _, err := os.Stat(a.imagesDir()); !os.IsNotExist(err) || len(events.payloads("run:attachment")) != 0 {
				t.Fatal("disabled image tools still wrote source images or emitted reference events")
			}
		})
	}
}

func TestAppendPendingRunImageSaveFailurePreservesInputs(t *testing.T) {
	a := newIsolatedChatTestApp(t)
	// A regular file in place of the image directory deterministically fails saves.
	if err := os.WriteFile(a.imagesDir(), []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}
	a.runInputs["run"] = make(chan runInput, runInputBufferSize)
	att := AttachmentInput{Name: "paste.png", Kind: "image", Type: "image/png", DataURL: generatedImageDataURL(imageTestPNG(t), "image/png")}
	if err := a.InjectRunMessage("run", "first input", att); err != nil {
		t.Fatal(err)
	}
	if err := a.InjectRunMessage("run", "later input"); err != nil {
		t.Fatal(err)
	}
	cfg := ConfigState{ImageGeneration: &ImageGenerationConfig{Enabled: true}}
	messages, injected, err := a.appendPendingRunInputs("run", "source-session", cfg, nil)
	if err == nil || !injected || len(messages) != 2 {
		t.Fatalf("save failure not reported or discarded input: injected=%v messages=%d err=%v", injected, len(messages), err)
	}
	if len(messages[0].MultiContent) != 2 || !strings.Contains(messages[0].MultiContent[0].Text, "first input") || messages[0].MultiContent[1].ImageURL.URL != att.DataURL || messages[1].Content != "later input" {
		t.Fatal("failed save lost queued text or original image from checkpoint history")
	}
}
