package app

import (
	"math"
	"strings"
	"testing"
)

func TestUTF8PrefixOnlyStepsBackTrailingRune(t *testing.T) {
	if got := utf8Prefix("a中b", 2); got != "a" {
		t.Fatalf("cut inside a rune: got %q", got)
	}
	if got := utf8Prefix("a中b", 4); got != "a中" {
		t.Fatalf("run-aligned cut changed text: got %q", got)
	}
}

// Non-UTF-8 input (GBK validation output) must keep its bytes instead of
// collapsing to the first invalid byte.
func TestUTF8PrefixKeepsInvalidTextBytes(t *testing.T) {
	gbk := []byte{0xD6, 0xD0, 0xCE, 0xC4, 0xB2, 0xE2, 0xCA, 0xD4} // "中文测试"
	got := utf8Prefix(string(gbk), 6)
	if got != string(gbk[:6]) {
		t.Fatalf("invalid text was shortened: got %d bytes, want 6", len(got))
	}
	input := strings.Repeat("a", 100) + "\x80" + strings.Repeat("b", 48*1024)
	got = utf8Prefix(input, 48*1024)
	if len(got) != 48*1024 {
		t.Fatalf("a byte before the cut must not shorten the prefix: got %d bytes", len(got))
	}
}

// The byte fast path must produce exactly the same count as rune decoding.
func TestEstimateTokensFromTextMatchesRuneScan(t *testing.T) {
	reference := func(text string) int {
		asciiCount, nonAsciiCount := 0, 0
		for _, r := range text {
			if r <= 127 {
				asciiCount++
			} else {
				nonAsciiCount++
			}
		}
		return int(math.Ceil(float64(asciiCount)/3.2)) + int(math.Ceil(float64(nonAsciiCount)*1.4))
	}
	for _, text := range []string{"", "abc", "中文abc", strings.Repeat("x", 1000), string([]byte{0xff, 'a', 0xe4, 0xb8})} {
		if got, want := estimateTokensFromText(text), reference(text); got != want {
			t.Fatalf("estimate mismatch for %q: got %d, want %d", text, got, want)
		}
	}
}
