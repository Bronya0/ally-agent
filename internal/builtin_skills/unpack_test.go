// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package builtin_skills

import (
	"os"
	"path/filepath"
	"testing"
)

func TestUnpackWritesSupportFilesOnly(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "md2other")
	wrote, err := Unpack("md2other", dest)
	if err != nil || !wrote {
		t.Fatalf("unpack: wrote=%v err=%v", wrote, err)
	}
	script := filepath.Join(dest, "md2docx.py")
	data, err := os.ReadFile(script)
	if err != nil {
		t.Fatalf("script was not unpacked: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("unpacked script is empty")
	}
	if _, err := os.Stat(filepath.Join(dest, "SKILL.md")); err != nil {
		t.Fatalf("unpacked dir is not self-contained: %v", err)
	}

	if wrote, err := Unpack("anydoc", filepath.Join(dir, "anydoc")); wrote || err != nil {
		t.Fatalf("a skill with only SKILL.md must stay embedded: wrote=%v err=%v", wrote, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "anydoc")); !os.IsNotExist(err) {
		t.Fatalf("a skill with only SKILL.md got a directory anyway: %v", err)
	}
}

func TestUnpackRewritesChangedFilesAndSkipsIdenticalOnes(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "md2other")
	if _, err := Unpack("md2other", dest); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(dest, "md2docx.py")
	if err := os.WriteFile(script, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Unpack("md2other", dest); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(script)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) == "stale" {
		t.Fatal("a changed builtin file was not replaced")
	}

	// 相同内容不该被重写：修改时间保持不动，否则每次加载都在写盘。
	before, err := os.Stat(script)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Unpack("md2other", dest); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(script)
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Fatal("an unchanged builtin file was rewritten on reload")
	}
}
