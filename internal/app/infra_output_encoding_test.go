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
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
)

func gbEncode(t *testing.T, s string) []byte {
	t.Helper()
	raw, err := simplifiedchinese.GB18030.NewEncoder().Bytes([]byte(s))
	if err != nil {
		t.Fatalf("gb18030 encode: %v", err)
	}
	return raw
}

func TestDecodeConsoleOutput(t *testing.T) {
	chinese := "以太网适配器 本地连接: 已接收 1,024 字节"

	// Valid UTF-8 passes through byte-identically — the common case.
	if got := decodeConsoleOutput(chinese); got != chinese {
		t.Fatalf("valid UTF-8 must pass through unchanged: %q", got)
	}
	if got := decodeConsoleOutput("plain ascii 123\n"); got != "plain ascii 123\n" {
		t.Fatalf("ascii must pass through: %q", got)
	}
	if got := decodeConsoleOutput(""); got != "" {
		t.Fatalf("empty must stay empty: %q", got)
	}

	// GBK bytes (what native tools emit on a codepage-936 system) decode back.
	if got := decodeConsoleOutput(string(gbEncode(t, chinese))); got != chinese {
		t.Fatalf("gbk output must decode to the original text: %q", got)
	}

	// Mixed ASCII + GBK keeps both halves intact.
	raw := append([]byte("Build ok\n"), gbEncode(t, chinese)...)
	got := decodeConsoleOutput(string(raw))
	if !strings.HasPrefix(got, "Build ok\n") || !strings.Contains(got, "本地连接") {
		t.Fatalf("mixed ascii+gbk output must decode both halves: %q", got)
	}

	// Bytes that are neither UTF-8 nor GBK degrade to replacement characters
	// instead of panicking or corrupting the caller.
	got = decodeConsoleOutput(string([]byte{0xC3, 0x28, 0xFF, 0x41}))
	if !utf8.ValidString(got) {
		t.Fatalf("decode result must always be valid UTF-8: %q", got)
	}
}

func TestIncompleteTrailingUTF8Len(t *testing.T) {
	cases := []struct {
		name string
		in   []byte
		want int
	}{
		{"ascii only", []byte("abc"), 0},
		{"complete sequence", append([]byte("ab"), []byte("中")...), 0},
		{"cut two-byte", []byte{'a', 0xC2}, 1},
		{"cut three-byte", []byte{'a', 0xE4, 0xB8}, 2},
		{"invalid lead", []byte{'a', 0xFF}, 0},
		{"lead with bad continuation", []byte{'a', 0xC2, 0x41}, 0},
	}
	for _, tc := range cases {
		if got := incompleteTrailingUTF8Len(tc.in); got != tc.want {
			t.Fatalf("%s: got %d, want %d", tc.name, got, tc.want)
		}
	}
}

func TestTranscodeSpillFileForRead(t *testing.T) {
	dir := t.TempDir()

	// A GBK spill becomes valid UTF-8 with the original text intact.
	gbkPath := filepath.Join(dir, "gbk.log")
	gbkContent := append(gbEncode(t, "中文输出 第一行\n"), gbEncode(t, "第二行 数据\n")...)
	if err := os.WriteFile(gbkPath, gbkContent, 0o600); err != nil {
		t.Fatal(err)
	}
	transcodeSpillFileForRead(gbkPath)
	got, err := os.ReadFile(gbkPath)
	if err != nil {
		t.Fatal(err)
	}
	if !utf8.Valid(got) {
		t.Fatalf("transcoded spill must be valid UTF-8: %x", got)
	}
	if string(got) != "中文输出 第一行\n第二行 数据\n" {
		t.Fatalf("unexpected transcoded content: %q", string(got))
	}

	// A valid UTF-8 spill is left byte-identical.
	utf8Path := filepath.Join(dir, "utf8.log")
	utf8Content := []byte("中文保持原样\nraw bytes stay\n")
	if err := os.WriteFile(utf8Path, utf8Content, 0o600); err != nil {
		t.Fatal(err)
	}
	transcodeSpillFileForRead(utf8Path)
	got, err = os.ReadFile(utf8Path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(utf8Content) {
		t.Fatalf("valid UTF-8 spill must be untouched: %q", string(got))
	}

	// A large GBK spill exercises the streaming path beyond the probe window.
	bigPath := filepath.Join(dir, "big.log")
	var want strings.Builder
	want.WriteString(strings.Repeat("line\n", 20*1024))
	want.WriteString("中文结尾行\n")
	if err := os.WriteFile(bigPath, gbEncode(t, want.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	transcodeSpillFileForRead(bigPath)
	got, err = os.ReadFile(bigPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want.String() {
		t.Fatalf("large spill transcode mismatch: got %d bytes, want %d", len(got), want.Len())
	}

	// A missing file is a no-op.
	transcodeSpillFileForRead(filepath.Join(dir, "missing.log"))

	// A valid UTF-8 file whose multi-byte character straddles the 64KB
	// validation chunk boundary must stay valid (byte-identical).
	straddlePath := filepath.Join(dir, "straddle.log")
	straddle := make([]byte, 64*1024+8)
	for i := range straddle {
		straddle[i] = 'a'
	}
	copy(straddle[64*1024-1:], []byte("中文"))
	if err := os.WriteFile(straddlePath, straddle, 0o600); err != nil {
		t.Fatal(err)
	}
	transcodeSpillFileForRead(straddlePath)
	got, err = os.ReadFile(straddlePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(straddle) {
		t.Fatalf("UTF-8 file with a boundary-straddling character must be untouched: got %d bytes", len(got))
	}

	// A file cut off mid-final-character is not valid UTF-8 and is repaired.
	truncPath := filepath.Join(dir, "trunc.log")
	truncated := append([]byte("ok\n"), []byte("中")[:2]...)
	if err := os.WriteFile(truncPath, truncated, 0o600); err != nil {
		t.Fatal(err)
	}
	transcodeSpillFileForRead(truncPath)
	got, err = os.ReadFile(truncPath)
	if err != nil {
		t.Fatal(err)
	}
	if !utf8.Valid(got) {
		t.Fatalf("spill with a truncated final character must be repaired: %x", got)
	}
}
