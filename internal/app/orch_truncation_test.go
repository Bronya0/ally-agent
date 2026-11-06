package app

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// The validation output cut (8 KiB) must not split a multi-byte rune: the
// result is serialized into the tool result the model reads.
func TestValidationOutputTruncationKeepsUTF8(t *testing.T) {
	input := strings.Repeat("a", autoValidationOutputBytes-1) + "中" + strings.Repeat("b", 10)
	got := compactValidationOutput(input)
	if !utf8.ValidString(got) {
		t.Fatalf("truncated validation output is not valid UTF-8")
	}
	if strings.ContainsRune(got, utf8.RuneError) {
		t.Fatalf("truncation produced a replacement rune")
	}
}

// Attachment text is capped at 200 KiB; the cut used to land inside a rune.
func TestAttachmentTextTruncationKeepsUTF8(t *testing.T) {
	body := strings.Repeat("a", maxAttachmentText-1) + "中" + strings.Repeat("b", 10)
	got := buildAttachmentTextContext("prompt", []AttachmentInput{{
		Name: "note.txt",
		Type: "text/plain",
		Text: body,
	}})
	if !utf8.ValidString(got) {
		t.Fatalf("attachment context is not valid UTF-8")
	}
	if strings.ContainsRune(got, utf8.RuneError) {
		t.Fatalf("attachment truncation produced a replacement rune")
	}
	if !strings.Contains(got, "[attachment text truncated]") {
		t.Fatalf("expected the truncation marker")
	}
}
