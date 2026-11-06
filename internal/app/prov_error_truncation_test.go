package app

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// Provider error previews are shown in the UI and stored in the session; the
// 200-byte cut must not split a Chinese error message.
func TestProviderErrorMessageTruncationKeepsUTF8(t *testing.T) {
	body := []byte(strings.Repeat("a", 199) + "中转错误" + strings.Repeat("b", 10))
	got := providerErrorMessageFromBody(body)
	if !utf8.ValidString(got) {
		t.Fatalf("provider error preview is not valid UTF-8")
	}
	if strings.ContainsRune(got, utf8.RuneError) {
		t.Fatalf("provider error truncation produced a replacement rune")
	}
}
