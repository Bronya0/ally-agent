// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package app

// Console output encoding repair. Git Bash does not transcode child output:
// MSYS2 tools and git emit UTF-8, but native Windows executables (ipconfig,
// netstat, tasklist, ...) and locale-encoded runtimes (Python < 3.15 writing
// to pipes) emit bytes in the system ANSI codepage — GBK/CP936 on a stock
// zh-CN installation — through the same pipe. Those bytes are not valid UTF-8
// and previously reached the model as U+FFFD mojibake.

import (
	"io"
	"os"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"
)

// decodeConsoleOutput converts captured command output to UTF-8 text. Valid
// UTF-8 passes through unchanged, so the common case costs one validity scan.
// Non-UTF-8 output is decoded as GB18030 as a whole: the superset covers GBK
// and keeps ASCII identical, which repairs the dominant single-encoding case
// exactly. A stream mixing valid UTF-8 Chinese with GBK Chinese sections is
// decoded as one GB18030 payload — the UTF-8 sections then mojibake in return;
// this is accepted because such compound output is rare and the alternative
// (per-byte-run decoding) mis-splits GBK pairs whose trail byte is ASCII-range.
// Non-GBK invalid bytes decode to replacement characters, no worse than the
// previous lossy pass-through.
func decodeConsoleOutput(s string) string {
	if utf8.ValidString(s) {
		return s
	}
	decoded, err := simplifiedchinese.GB18030.NewDecoder().Bytes([]byte(s))
	if err != nil || len(decoded) == 0 {
		return s
	}
	return string(decoded)
}

// transcodeSpillFileForRead rewrites the command full-output spill file as
// UTF-8 in place when the file is not valid UTF-8, so `read` — which rejects
// non-UTF-8 text with E_NOT_UTF8 — can open the complete output on
// codepage-936 systems. Valid files are left byte-identical.
func transcodeSpillFileForRead(path string) {
	valid, err := fileIsUTF8(path)
	if err != nil || valid {
		return
	}
	src, err := os.Open(path)
	if err != nil {
		return
	}
	tmp, err := os.CreateTemp(sameDir(path), "ally-run-*.utf8")
	if err != nil {
		src.Close()
		return
	}
	tmpName := tmp.Name()
	_, copyErr := io.Copy(tmp, transform.NewReader(src, simplifiedchinese.GB18030.NewDecoder()))
	// Close both handles before the rename: on Windows renaming over an open
	// file fails with a sharing violation.
	srcErr := src.Close()
	tmpErr := tmp.Close()
	if copyErr != nil || srcErr != nil || tmpErr != nil {
		_ = os.Remove(tmpName)
		return
	}
	_ = os.Rename(tmpName, path)
}

// fileIsUTF8 reports whether the whole file is valid UTF-8 without loading it
// into memory. Chunks are validated with at most a 3-byte partial-sequence
// carry between reads, so a chunk boundary split mid-character cannot
// misreport valid UTF-8 as invalid; the final carry is validated whole at EOF,
// which also rejects a file whose last character was cut off.
func fileIsUTF8(path string) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()
	chunk := make([]byte, 64*1024)
	scan := make([]byte, 0, len(chunk)+3)
	var carry []byte
	for {
		n, readErr := f.Read(chunk)
		if n > 0 {
			scan = append(scan[:0], carry...)
			scan = append(scan, chunk[:n]...)
			drop := 0
			if readErr == nil {
				drop = incompleteTrailingUTF8Len(scan)
			}
			if !utf8.Valid(scan[:len(scan)-drop]) {
				return false, nil
			}
			carry = append(carry[:0], scan[len(scan)-drop:]...)
		}
		if readErr != nil {
			if readErr != io.EOF {
				return false, readErr
			}
			// EOF: a leftover carry is a truncated final character.
			return len(carry) == 0, nil
		}
	}
}

// incompleteTrailingUTF8Len reports how many trailing bytes of p form an
// unterminated multi-byte UTF-8 sequence. Complete and invalid sequences
// report 0.
func incompleteTrailingUTF8Len(p []byte) int {
	for i := len(p) - 1; i >= 0 && i >= len(p)-3; i-- {
		c := p[i]
		if c < 0x80 {
			return 0
		}
		if c&0xC0 == 0x80 {
			continue // continuation byte, look further back
		}
		var want int
		switch {
		case c&0xE0 == 0xC0:
			want = 2
		case c&0xF0 == 0xE0:
			want = 3
		case c&0xF8 == 0xF0:
			want = 4
		default:
			return 0
		}
		if len(p)-i < want {
			return len(p) - i
		}
		return 0
	}
	return 0
}

func sameDir(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' || path[i] == '\\' {
			return path[:i]
		}
	}
	return "."
}
