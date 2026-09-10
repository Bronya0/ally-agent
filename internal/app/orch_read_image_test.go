// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	openai "github.com/sashabaranov/go-openai"
)

func TestImageMimeFromHeader(t *testing.T) {
	cases := []struct {
		name string
		data []byte
		want string
	}{
		{"png", []byte("\x89PNG\r\n\x1a\nrest"), "image/png"},
		{"jpeg", []byte("\xFF\xD8\xFF\xE0rest"), "image/jpeg"},
		{"gif87", []byte("GIF87arest"), "image/gif"},
		{"gif89", []byte("GIF89arest"), "image/gif"},
		{"webp", []byte("RIFF\x00\x00\x00\x00WEBPVP8 "), "image/webp"},
		{"bmp", []byte("BMrest"), "image/bmp"},
		{"text", []byte("hello world"), ""},
		{"empty", []byte{}, ""},
		{"truncated png", []byte("\x89PNG"), ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := imageMimeFromHeader(c.data)
			if got != c.want {
				t.Fatalf("imageMimeFromHeader = %q, want %q", got, c.want)
			}
		})
	}
}

func TestImageDataURL(t *testing.T) {
	url := imageDataURL("image/png", []byte("abc"))
	if !strings.HasPrefix(url, "data:image/png;base64,") {
		t.Fatalf("unexpected data URL prefix: %q", url)
	}
	// base64("abc") == "YWJj"
	if !strings.HasSuffix(url, "YWJj") {
		t.Fatalf("unexpected data URL payload: %q", url)
	}
	// Oversized images yield an empty URL (degraded to a text notice).
	big := make([]byte, maxReadImageBytes+1)
	if got := imageDataURL("image/png", big); got != "" {
		t.Fatalf("oversized image should not produce a data URL, got %d bytes", len(got))
	}
}

func TestReadImageInjectionMessage(t *testing.T) {
	msg := readImageInjectionMessage([]readImageCandidate{
		{Path: "a.png", DataURL: "data:image/png;base64,YWJj"},
		{Path: "b.jpg", DataURL: "data:image/jpeg;base64,WVhZ"},
		{Path: "c.txt", DataURL: ""}, // ignored
	})
	if msg == nil {
		t.Fatal("expected injection message")
	}
	if msg.Role != openai.ChatMessageRoleUser || len(msg.MultiContent) != 3 {
		t.Fatalf("unexpected message: role=%q parts=%d", msg.Role, len(msg.MultiContent))
	}
	if msg.MultiContent[0].Type != openai.ChatMessagePartTypeText || !strings.Contains(msg.MultiContent[0].Text, "a.png") {
		t.Fatalf("expected text lead with image names, got %#v", msg.MultiContent[0])
	}
	for i, part := range msg.MultiContent[1:] {
		if part.Type != openai.ChatMessagePartTypeImageURL || part.ImageURL == nil || part.ImageURL.URL == "" {
			t.Fatalf("part %d is not an image URL part: %#v", i+1, part)
		}
	}

	if got := readImageInjectionMessage(nil); got != nil {
		t.Fatal("nil candidates must produce nil message")
	}
	if got := readImageInjectionMessage([]readImageCandidate{{Path: "x.txt", DataURL: ""}}); got != nil {
		t.Fatal("empty DataURL candidates must produce nil message")
	}
}

func TestIsImageInjectionMessage(t *testing.T) {
	injected := readImageInjectionMessage([]readImageCandidate{
		{Path: "a.png", DataURL: "data:image/png;base64,YWJj"},
	})
	if injected == nil || !isImageInjectionMessage(injected) {
		t.Fatal("injection message must be recognized by marker")
	}
	// Ordinary user messages (including user images from attachments) are not
	// mistaken for injection messages.
	normal := &openai.ChatCompletionMessage{Role: openai.ChatMessageRoleUser, Content: "hello"}
	if isImageInjectionMessage(normal) {
		t.Fatal("plain user message must not be treated as injection")
	}
	withImage := &openai.ChatCompletionMessage{Role: openai.ChatMessageRoleUser, MultiContent: []openai.ChatMessagePart{
		{Type: openai.ChatMessagePartTypeText, Text: "look at this"},
		{Type: openai.ChatMessagePartTypeImageURL, ImageURL: &openai.ChatMessageImageURL{URL: "data:image/png;base64,YWJj"}},
	}}
	if isImageInjectionMessage(withImage) {
		t.Fatal("attachment-style image message must not be treated as injection")
	}
}

func TestSanitizeHistoryKeepsImagesInMemoryAndDropsForDisk(t *testing.T) {
	injected := readImageInjectionMessage([]readImageCandidate{
		{Path: "a.png", DataURL: "data:image/png;base64,YWJj"},
	})
	attachment := openai.ChatCompletionMessage{Role: openai.ChatMessageRoleUser, MultiContent: []openai.ChatMessagePart{
		{Type: openai.ChatMessagePartTypeText, Text: "look at this"},
		{Type: openai.ChatMessagePartTypeImageURL, ImageURL: &openai.ChatMessageImageURL{URL: "data:image/png;base64,YWJj"}},
	}}
	messages := []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleSystem, Content: "sys"},
		{Role: openai.ChatMessageRoleUser, Content: "hi"},
		*injected,
		attachment,
		{Role: openai.ChatMessageRoleAssistant, Content: "let me look"},
	}

	// In-memory variant keeps both image-carrying messages intact.
	memory := sanitizeHistoryMessages(messages)
	if len(memory) != 4 {
		t.Fatalf("in-memory history must keep image messages, got %d: %#v", len(memory), memory)
	}
	keptInjection := false
	keptAttachment := false
	for i := range memory {
		if isImageInjectionMessage(&memory[i]) {
			keptInjection = true
		}
		if memory[i].Role == openai.ChatMessageRoleUser && len(memory[i].MultiContent) > 0 {
			keptAttachment = true
		}
	}
	if !keptInjection || !keptAttachment {
		t.Fatalf("in-memory history lost image messages: injection=%v attachment=%v", keptInjection, keptAttachment)
	}

	// Disk variant drops the injection and flattens the attachment.
	disk := sanitizeHistoryMessagesForDisk(messages)
	if len(disk) != 3 {
		t.Fatalf("disk history must drop the injection message, got %d: %#v", len(disk), disk)
	}
	for i := range disk {
		if isImageInjectionMessage(&disk[i]) {
			t.Fatalf("injection message leaked into disk history: %#v", disk[i])
		}
		if len(disk[i].MultiContent) > 0 {
			t.Fatalf("image MultiContent leaked into disk history: %#v", disk[i])
		}
	}
	if !strings.Contains(disk[1].Content, "image attachment(s) omitted") {
		t.Fatalf("flattened attachment should carry the omission note, got %q", disk[1].Content)
	}
}

func TestCollectReadImages(t *testing.T) {
	okResult := toolResult{OK: true, Data: &BatchReadResult{Files: []BatchReadResultItem{
		{Path: "a.png", DataURL: "data:image/png;base64,YWJj"},
		{Path: "b.txt", DataURL: ""},
	}}}
	got := collectReadImages("read", &okResult)
	if len(got) != 1 || got[0].Path != "a.png" || got[0].DataURL == "" {
		t.Fatalf("unexpected collected images: %#v", got)
	}
	// Non-read tools never contribute images.
	if got := collectReadImages("grep", &okResult); got != nil {
		t.Fatalf("grep must not collect images: %#v", got)
	}
	// Failed reads contribute nothing.
	failed := toolResult{OK: false, Error: "boom"}
	if got := collectReadImages("read", &failed); got != nil {
		t.Fatalf("failed read must not collect images: %#v", got)
	}
}

func TestCompactReadResultOmittedDataURLFromModel(t *testing.T) {
	result := toolResult{OK: true, Data: &BatchReadResult{Files: []BatchReadResultItem{
		{Path: "a.png", Content: "[Image: a.png (PNG, 3 bytes)]", Kind: "image", DataURL: "data:image/png;base64,YWJj"},
	}}}
	compact := compactToolResultForModel("read", result, "fallback")
	if strings.Contains(compact, "data:image") {
		t.Fatalf("model-facing read result must not carry the base64 data URL: %s", compact)
	}
	if !strings.Contains(compact, "sent as image input") {
		t.Fatalf("model-facing read result should note the image injection: %s", compact)
	}
}

func TestReusedReadResultClearsDataURL(t *testing.T) {
	reused := reusedBatchReadResult(&BatchReadResult{Files: []BatchReadResultItem{
		{Path: "a.png", Content: "x", DataURL: "data:image/png;base64,YWJj"},
	}})
	if reused.Files[0].DataURL != "" {
		t.Fatalf("reused read result must clear DataURL to avoid duplicate injection: %#v", reused.Files[0])
	}
}

func TestReadImageThroughExecuteTool(t *testing.T) {
	app := NewApp()
	dir := t.TempDir()
	// Minimal PNG magic header is enough for detection; the tool only reads
	// bytes, it does not decode the image.
	pngPath := filepath.Join(dir, "screenshot.png")
	if err := os.WriteFile(pngPath, []byte("\x89PNG\r\n\x1a\n"+"payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	args := []byte(`{"files":[{"path":"screenshot.png"}]}`)
	res := app.executeTool(t.Context(), ConfigState{Workspace: dir}, "session-1", "read", args)
	if !res.OK {
		t.Fatalf("read image failed: %v", res.Error)
	}
	data, ok := res.Data.(*BatchReadResult)
	if !ok || len(data.Files) != 1 {
		t.Fatalf("unexpected result shape: %#v", res.Data)
	}
	f := data.Files[0]
	if f.Kind != "image" || f.Editable || f.DataURL == "" || !strings.HasPrefix(f.DataURL, "data:image/png;base64,") {
		t.Fatalf("unexpected image read result: %#v", f)
	}
	if f.Version == "" {
		t.Fatalf("image read must still report a version: %#v", f)
	}
}
