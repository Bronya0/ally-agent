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
)

// TestNormalizeAPIFormat pins the alias buckets the frontend mirrors
// (modelConfigIO.mjs normalizeAPIFormat). Both tables have to be pinned on their
// own side: a spelling only one of them folds is not "tolerated", it is silently
// rewritten on the side that missed it.

func TestMergeConfigReasoningEffort(t *testing.T) {
	// Empty overlay preserves the base value.
	base := ConfigState{ReasoningEffort: reasoningEffortHigh}
	got := mergeConfig(base, ConfigState{})
	if got.ReasoningEffort != reasoningEffortHigh {
		t.Fatalf("empty overlay changed reasoningEffort: got %q, want %q", got.ReasoningEffort, reasoningEffortHigh)
	}

	// A non-empty overlay replaces and normalizes the value.
	got = mergeConfig(base, ConfigState{ReasoningEffort: "x-high"})
	if got.ReasoningEffort != reasoningEffortXHigh {
		t.Fatalf("overlay reasoningEffort not normalized: got %q, want %q", got.ReasoningEffort, reasoningEffortXHigh)
	}

	// Model entries are normalized on merge.
	got = mergeConfig(ConfigState{}, ConfigState{Models: []ModelConfig{{ReasoningEffort: "MAX"}}})
	if len(got.Models) != 1 || got.Models[0].ReasoningEffort != reasoningEffortMax {
		t.Fatalf("model reasoningEffort not normalized on merge: got %#v", got.Models)
	}
}

func TestLimitedBufferSpillMatchesCompleteOutput(t *testing.T) {
	const limit = 64
	chunks := []string{
		"构建输出 第一行\n",
		"构建输出 第二行 包含较多中文内容\n",
		"third line with ascii only\n",
		"最后一行 ⚙ done\n",
	}
	buf := &limitedBuffer{limit: limit}
	writer := &teeWriter{primary: buf, spillDir: t.TempDir()}
	buf.onTruncate = writer.startSpill

	for _, chunk := range chunks {
		if _, err := writer.Write([]byte(chunk)); err != nil {
			t.Fatal(err)
		}
	}
	if writer.spill == nil {
		t.Fatal("overflowing writes must create the spill sink")
	}
	spillPath := writer.spill.Name()
	if err := writer.spill.Close(); err != nil {
		t.Fatal(err)
	}

	// The capped buffer keeps a UTF-8-valid prefix: a byte-sliced truncation
	// would leave a dangling multi-byte sequence, which downstream decoding
	// misreads as GBK mojibake.
	kept := buf.String()
	if !utf8.ValidString(kept) {
		t.Fatalf("buffered prefix must stay valid UTF-8: %q", kept)
	}
	if !buf.truncated {
		t.Fatal("overflow must set the truncated flag")
	}

	// The spill file is the byte-exact complete output: no chunk duplicated
	// (the overflowing chunk is written once, not once by the prefix capture
	// and again in full) and none lost.
	spilled, err := os.ReadFile(spillPath)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join(chunks, "")
	if string(spilled) != want {
		t.Fatalf("spill file must equal the complete output:\n got %q\nwant %q", spilled, want)
	}
	if !strings.HasPrefix(want, kept) {
		t.Fatalf("buffered content must be a prefix of the full output, got %q", kept)
	}
}

// TestAtomicReplaceFileKeepsExistingBackup: the fallback path moves the current
// file aside to <dst>.bak. It used to delete whatever already sat at that name,
// which could be the user's own backup (or a .bak written by another tool).
func TestAtomicReplaceFileKeepsExistingBackup(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "config.json")
	backup := dst + ".bak"
	if err := os.WriteFile(backup, []byte("user backup\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Make the first os.Rename(tmp, dst) fail: renaming a file over a non-empty
	// directory is refused on every platform.
	if err := os.MkdirAll(filepath.Join(dst, "child"), 0o755); err != nil {
		t.Fatal(err)
	}
	tmp := filepath.Join(dir, "tmp-file")
	if err := os.WriteFile(tmp, []byte("new\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := atomicReplaceFile(tmp, dst); err != nil {
		t.Fatalf("atomicReplaceFile() = %v", err)
	}
	if got, err := os.ReadFile(backup); err != nil || string(got) != "user backup\n" {
		t.Fatalf("the existing .bak must survive: %q (err=%v)", got, err)
	}
	if got, err := os.ReadFile(dst); err != nil || string(got) != "new\n" {
		t.Fatalf("the temp file must be installed at dst: %q (err=%v)", got, err)
	}
}
