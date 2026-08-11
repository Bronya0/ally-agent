package app

import (
	"encoding/base64"
	"strings"
	"testing"
	"unicode/utf16"
)

func encodeUTF16LE(s string) []byte {
	units := utf16.Encode([]rune(s))
	out := make([]byte, 0, len(units)*2)
	for _, u := range units {
		out = append(out, byte(u), byte(u>>8))
	}
	return out
}

func encodeUTF16BE(s string) []byte {
	units := utf16.Encode([]rune(s))
	out := make([]byte, 0, len(units)*2)
	for _, u := range units {
		out = append(out, byte(u>>8), byte(u))
	}
	return out
}

func remoteRawPayload(data []byte) struct {
	Path       string `json:"path"`
	DataBase64 string `json:"dataBase64"`
	Size       int64  `json:"size"`
	Mode       int    `json:"mode"`
	ModTime    string `json:"modTime"`
} {
	return struct {
		Path       string `json:"path"`
		DataBase64 string `json:"dataBase64"`
		Size       int64  `json:"size"`
		Mode       int    `json:"mode"`
		ModTime    string `json:"modTime"`
	}{
		Path:       "notes.txt",
		DataBase64: base64.StdEncoding.EncodeToString(data),
		Size:       int64(len(data)),
		Mode:       0o644,
		ModTime:    "2026-01-01T00:00:00Z",
	}
}

func TestDecodeRemoteRawFileTranscodesUTF16(t *testing.T) {
	cases := []struct {
		name string
		data []byte
		want string
	}{
		{"utf8 passthrough", []byte("plain utf8\n"), "plain utf8\n"},
		{"utf16le bom", append([]byte{0xFF, 0xFE}, encodeUTF16LE("hello\nworld\n")...), "hello\nworld\n"},
		{"utf16le no bom", encodeUTF16LE("line1\nline2\n"), "line1\nline2\n"},
		{"utf16le cjk", append([]byte{0xFF, 0xFE}, encodeUTF16LE("你好 world\n")...), "你好 world\n"},
		{"utf16be bom", append([]byte{0xFE, 0xFF}, encodeUTF16BE("hello\nworld\n")...), "hello\nworld\n"},
		{"utf16be no bom", encodeUTF16BE("line1\nline2\n"), "line1\nline2\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			file, err := decodeRemoteRawFile(remoteRawPayload(c.data))
			if err != nil {
				t.Fatalf("decodeRemoteRawFile(%s) error: %v", c.name, err)
			}
			if string(file.Data) != c.want {
				t.Fatalf("decodeRemoteRawFile(%s) = %q, want %q", c.name, file.Data, c.want)
			}
		})
	}
}

func TestDecodeRemoteRawFileRejectsBinary(t *testing.T) {
	cases := []struct {
		name    string
		data    []byte
		wantErr string
	}{
		{"nul bytes", []byte("ab\x00cd"), "E_BINARY_FILE"},
		{"invalid utf8", []byte{0xC3, 0x28}, "E_NOT_UTF8"},
		{"utf32le bom", []byte{0xFF, 0xFE, 0x00, 0x00, 'a', 0, 0, 0}, "E_BINARY_FILE"},
		{"utf32be bom", []byte{0x00, 0x00, 0xFE, 0xFF, 0, 0, 0, 'a'}, "E_BINARY_FILE"},
		{"odd utf16 length", []byte{0xFF, 0xFE, 0x61, 0x00, 0x62}, "E_BINARY_FILE"},
		{"probe odd utf16 length", encodeUTF16LE("abc")[:5], "E_BINARY_FILE"},
		{"decoded nul char", []byte{0xFF, 0xFE, 0x41, 0x00, 0x00, 0x00}, "E_BINARY_FILE"},
		{"binary control pairs", []byte{0x01, 0x00, 0x02, 0x00, 0x03, 0x00}, "E_BINARY_FILE"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := decodeRemoteRawFile(remoteRawPayload(c.data))
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Fatalf("decodeRemoteRawFile(%s) error = %v, want containing %q", c.name, err, c.wantErr)
			}
		})
	}
}
