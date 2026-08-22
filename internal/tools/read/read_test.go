// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package read

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"
)

func TestExtractPDFTextBestEffortReadsCompressedText(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "compressed.pdf")
	content := "BT /F1 12 Tf 72 720 Td (Compressed PDF text) Tj ET"
	compressed := new(bytes.Buffer)
	writer := zlib.NewWriter(compressed)
	if _, err := writer.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	var document bytes.Buffer
	document.WriteString("%PDF-1.4\n%\xE2\xE3\xCF\xD3\n")
	offsets := make([]int, 6)
	writeObject := func(number int, body []byte) {
		offsets[number] = document.Len()
		fmt.Fprintf(&document, "%d 0 obj\n", number)
		document.Write(body)
		document.WriteString("\nendobj\n")
	}
	writeObject(1, []byte("<< /Type /Catalog /Pages 2 0 R >>"))
	writeObject(2, []byte("<< /Type /Pages /Kids [3 0 R] /Count 1 >>"))
	writeObject(3, []byte("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 4 0 R >> >> /Contents 5 0 R >>"))
	writeObject(4, []byte("<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>"))
	stream := fmt.Sprintf("<< /Length %d /Filter /FlateDecode >>\nstream\n", compressed.Len())
	streamBytes := append([]byte(stream), compressed.Bytes()...)
	streamBytes = append(streamBytes, []byte("\nendstream")...)
	writeObject(5, streamBytes)

	xrefOffset := document.Len()
	document.WriteString("xref\n0 6\n0000000000 65535 f \n")
	for number := 1; number < len(offsets); number++ {
		fmt.Fprintf(&document, "%010d 00000 n \n", offsets[number])
	}
	fmt.Fprintf(&document, "trailer\n<< /Size 6 /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", xrefOffset)
	if err := os.WriteFile(path, document.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := ExtractPDFTextBestEffort(path)
	if err != nil {
		t.Fatalf("ExtractPDFTextBestEffort() error: %v", err)
	}
	if !strings.Contains(got, "Compressed PDF text") {
		t.Fatalf("ExtractPDFTextBestEffort() = %q, want compressed text", got)
	}
}

func TestExtractPDFTextBestEffortRejectsOversizedFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "oversized.pdf")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(MaxReadBytes + 1); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	_, err = ExtractPDFTextBestEffort(path)
	if err == nil || !strings.Contains(err.Error(), "E_FILE_TOO_LARGE") {
		t.Fatalf("ExtractPDFTextBestEffort() error = %v, want E_FILE_TOO_LARGE", err)
	}
}

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
		name   string
		data   []byte
		want   string
		ending string
		hadBOM bool
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
		name   string
		text   string
		ending string
		hadBOM bool
		want   []byte
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

// utf16LE/utf16BE encode s as UTF-16 bytes (no BOM) using utf16.Encode so
// runes beyond the BMP (surrogate pairs) are encoded correctly.
func utf16LE(s string) []byte {
	units := utf16.Encode([]rune(s))
	out := make([]byte, 0, len(units)*2)
	for _, u := range units {
		out = append(out, byte(u), byte(u>>8))
	}
	return out
}

func utf16BE(s string) []byte {
	units := utf16.Encode([]rune(s))
	out := make([]byte, 0, len(units)*2)
	for _, u := range units {
		out = append(out, byte(u>>8), byte(u))
	}
	return out
}

func TestReadTextFileTranscodesUTF16(t *testing.T) {
	cases := []struct {
		name string
		data []byte
		want string
	}{
		{"utf16le bom", append([]byte{0xFF, 0xFE}, utf16LE("hello\nworld\n")...), "hello\nworld\n"},
		{"utf16be bom", append([]byte{0xFE, 0xFF}, utf16BE("hello\nworld\n")...), "hello\nworld\n"},
		{"utf16le no bom", utf16LE("line1\nline2\n"), "line1\nline2\n"},
		{"utf16be no bom", utf16BE("line1\nline2\n"), "line1\nline2\n"},
		{"utf16le mixed cjk", append([]byte{0xFF, 0xFE}, utf16LE("你好 world\n")...), "你好 world\n"},
		{"utf16be mixed cjk", append([]byte{0xFE, 0xFF}, utf16BE("你好 world\n")...), "你好 world\n"},
		{"utf16le emoji", append([]byte{0xFF, 0xFE}, utf16LE("hi 😀\n")...), "hi 😀\n"},
		{"utf16be emoji", append([]byte{0xFE, 0xFF}, utf16BE("hi 😀\n")...), "hi 😀\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			p := filepath.Join(dir, "f.txt")
			if err := os.WriteFile(p, c.data, 0o644); err != nil {
				t.Fatal(err)
			}
			got, _, err := ReadTextFile(p)
			if err != nil {
				t.Fatalf("ReadTextFile(%s) error: %v", c.name, err)
			}
			if string(got) != c.want {
				t.Fatalf("ReadTextFile(%s) = %q, want %q", c.name, got, c.want)
			}
		})
	}
}

func TestReadTextFileRejectsBinaryAndUTF32(t *testing.T) {
	cases := []struct {
		name    string
		data    []byte
		wantErr string
	}{
		{"utf8 text", []byte("plain utf8\n"), ""},
		{"empty file", []byte{}, ""},
		{"utf16 bom only", []byte{0xFF, 0xFE}, ""},
		// 16-bit PCM-style samples decode to printable runes ("D¬3"); this is
		// the inherently undecidable boundary between binary and UTF-16 text.
		{"pcm-like samples decode as text", []byte{0x44, 0x00, 0xAC, 0x00, 0x33, 0x00}, ""},
		{"utf32le bom", []byte{0xFF, 0xFE, 0x00, 0x00, 'a', 0, 0, 0}, "E_BINARY_FILE"},
		{"utf32be bom", []byte{0x00, 0x00, 0xFE, 0xFF, 0, 0, 0, 'a'}, "E_BINARY_FILE"},
		{"utf8 with one nul", []byte("ab\x00cd"), "E_BINARY_FILE"},
		{"nul skew below threshold", []byte{0x61, 0x00, 0x62, 0x00, 0x63, 0x00, 0x00, 0x01}, "E_BINARY_FILE"},
		{"binary uniform nuls", []byte{0x00, 0x01, 0x02, 0x00, 0x00, 0x03, 0x04, 0x00, 0x05, 0x00, 0x00, 0x06}, "E_BINARY_FILE"},
		{"skewed binary control chars", []byte{0x01, 0x00, 0x02, 0x00, 0x03, 0x00}, "E_BINARY_FILE"},
		{"bom binary control chars", []byte{0xFF, 0xFE, 0x01, 0x00, 0x02, 0x00, 0x03, 0x00}, "E_BINARY_FILE"},
		{"odd length utf16", []byte{0xFF, 0xFE, 0x61, 0x00, 0x62}, "E_BINARY_FILE"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			p := filepath.Join(dir, "f.txt")
			if err := os.WriteFile(p, c.data, 0o644); err != nil {
				t.Fatal(err)
			}
			_, _, err := ReadTextFile(p)
			if c.wantErr == "" {
				if err != nil {
					t.Fatalf("ReadTextFile(%s) unexpected error: %v", c.name, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Fatalf("ReadTextFile(%s) error = %v, want containing %q", c.name, err, c.wantErr)
			}
		})
	}
}
