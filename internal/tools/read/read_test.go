package read

import (
	"strings"
	"testing"
)

func TestVersionTokenUsesSixCrockfordBase32Characters(t *testing.T) {
	version := HashVersion([]byte("versioned content"))
	if len(version) != 6 {
		t.Fatalf("HashVersion length = %d, want 6 (%q)", len(version), version)
	}
	if err := ValidateVersion(version); err != nil {
		t.Fatalf("ValidateVersion(%q) returned error: %v", version, err)
	}
	if err := ValidateVersion(strings.ToUpper(version)); err != nil {
		t.Fatalf("ValidateVersion must accept uppercase token: %v", err)
	}
}

func TestValidateVersionRejectsLegacyTwelveCharacterToken(t *testing.T) {
	if err := ValidateVersion("9k3m7x2p4t6w"); err == nil {
		t.Fatal("ValidateVersion accepted legacy 12-character token")
	}
}

func TestNormalizeTextStripsBOMAndReportsIt(t *testing.T) {
	cases := []struct {
		name    string
		data    []byte
		want    string
		ending  string
		hadBOM  bool
	}{
		{"no bom lf", []byte("a\nb\n"), "a\nb\n", "LF", false},
		{"bom lf", []byte("\uFEFFa\nb\n"), "a\nb\n", "LF", true},
		{"bom crlf", []byte("\uFEFFa\r\nb\r\n"), "a\nb\n", "CRLF", true},
		{"bom only", []byte("\uFEFF"), "", "LF", true},
		{"cr only", []byte("a\rb\r"), "a\nb\n", "LF", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ending, hadBOM := NormalizeText(c.data)
			if got != c.want || ending != c.ending || hadBOM != c.hadBOM {
				t.Fatalf("NormalizeText(%q) = (%q, %q, %v), want (%q, %q, %v)",
					c.data, got, ending, hadBOM, c.want, c.ending, c.hadBOM)
			}
		})
	}
}

func TestEncodeTextRestoresBOMAndLineEnding(t *testing.T) {
	cases := []struct {
		name    string
		text    string
		ending  string
		hadBOM  bool
		want    []byte
	}{
		{"lf no bom", "a\nb\n", "LF", false, []byte("a\nb\n")},
		{"crlf no bom", "a\nb\n", "CRLF", false, []byte("a\r\nb\r\n")},
		{"lf bom", "a\nb\n", "LF", true, []byte("\uFEFFa\nb\n")},
		{"crlf bom", "a\nb\n", "CRLF", true, []byte("\uFEFFa\r\nb\r\n")},
		{"empty with bom", "", "LF", true, []byte("\uFEFF")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := EncodeText(c.text, c.ending, c.hadBOM)
			if string(got) != string(c.want) {
				t.Fatalf("EncodeText(%q, %q, %v) = %q, want %q", c.text, c.ending, c.hadBOM, got, c.want)
			}
		})
	}
}

func TestNormalizeEncodeRoundTripPreservesBOMAndEnding(t *testing.T) {
	original := []byte("\uFEFFline1\r\nline2\r\n")
	text, ending, hadBOM := NormalizeText(original)
	roundTrip := EncodeText(text, ending, hadBOM)
	if string(roundTrip) != string(original) {
		t.Fatalf("round-trip = %q, want %q", roundTrip, original)
	}
}
