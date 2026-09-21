// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeLessonsFile(t *testing.T, root, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, projectLessonsFileName), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestBuildProjectLessonsContextEmptyWithoutFile(t *testing.T) {
	if got := buildProjectLessonsContext(""); got != "" {
		t.Fatalf("expected empty context for empty workspace, got %q", got)
	}
	if got := buildProjectLessonsContext(t.TempDir()); got != "" {
		t.Fatalf("expected empty context without lessons file, got %q", got)
	}
}

func TestBuildProjectLessonsContextReadsFile(t *testing.T) {
	root := t.TempDir()
	writeLessonsFile(t, root, "- [a] x → y → z @f.go\n- [b] p → q → r @g.go\n")
	got := buildProjectLessonsContext(root)
	if !strings.Contains(got, "- [a] x → y → z @f.go") || !strings.Contains(got, "- [b] p → q → r @g.go") {
		t.Fatalf("expected lesson content, got %q", got)
	}
}

func TestBuildProjectLessonsContextKeepsNewestLines(t *testing.T) {
	root := t.TempDir()
	// One line more than the line cap allows, each short enough that the byte cap
	// cannot be what trims: the oldest line is the one that must fall out.
	total := projectLessonsMaxLines + 1
	var lines []string
	for i := 0; i < total; i++ {
		lines = append(lines, fmt.Sprintf("- [l] lesson %03d @f.go", i))
	}
	writeLessonsFile(t, root, strings.Join(lines, "\n"))
	got := buildProjectLessonsContext(root)
	newest := fmt.Sprintf("lesson %03d", total-1)
	if !strings.Contains(got, newest) {
		t.Fatalf("expected the newest line %q to be kept, got %q", newest, got)
	}
	if strings.Contains(got, "lesson 000") {
		t.Fatalf("expected the oldest line trimmed once the line cap is reached, got %q", got)
	}
}

func TestBuildProjectLessonsContextHonorsByteBudget(t *testing.T) {
	root := t.TempDir()
	// Few lines, each far over the per-lesson rule the prompt states: the byte
	// budget is what limits the injection, and the trim must land on a line
	// boundary so the model never sees half a lesson.
	filler := strings.Repeat("坑", 400)
	var lines []string
	for i := 0; i < 20; i++ {
		lines = append(lines, fmt.Sprintf("- [%02d] %s @f.go", i, filler))
	}
	writeLessonsFile(t, root, strings.Join(lines, "\n"))
	got := buildProjectLessonsContext(root)
	if len(got) > projectLessonsMaxBytes {
		t.Fatalf("injected lessons = %d bytes, want <= %d", len(got), projectLessonsMaxBytes)
	}
	if !strings.Contains(got, "- [19]") {
		t.Fatal("the newest lesson must survive the byte trim")
	}
	if strings.Contains(got, "- [00]") {
		t.Fatal("the oldest lesson must be the one trimmed")
	}
	if !strings.HasPrefix(got, "- [") {
		t.Fatalf("the trim must land on a line boundary, got %.20q", got)
	}
}

func TestProjectLessonsPromptPart(t *testing.T) {
	root := t.TempDir()
	rules := projectLessonsPromptPart(root)
	if !strings.Contains(rules, projectLessonsFileName) || strings.Contains(rules, "project-lessons") {
		t.Fatalf("expected rules without lessons content, got %q", rules)
	}
	writeLessonsFile(t, root, "- [a] x → y → z @f.go\n")
	wrapped := projectLessonsPromptPart(root)
	if !strings.Contains(wrapped, "<project-lessons priority=\"reference-only lower-than-core lower-than-project-instructions\">") {
		t.Fatalf("expected wrapper tag, got %q", wrapped)
	}
	if !strings.Contains(wrapped, "- [a] x → y → z @f.go") {
		t.Fatalf("expected lesson content inside wrapper, got %q", wrapped)
	}
}
