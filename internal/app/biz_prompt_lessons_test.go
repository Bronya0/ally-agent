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
	dir := filepath.Join(root, ".ally")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "lessons.md"), []byte(content), 0o644); err != nil {
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
	var lines []string
	for i := 0; i < 50; i++ {
		lines = append(lines, fmt.Sprintf("- [l] lesson %02d @f.go", i))
	}
	writeLessonsFile(t, root, strings.Join(lines, "\n"))
	got := buildProjectLessonsContext(root)
	if !strings.Contains(got, "lesson 49") || !strings.Contains(got, "lesson 20") {
		t.Fatalf("expected newest lines kept, got %q", got)
	}
	if strings.Contains(got, "lesson 19") {
		t.Fatalf("expected oldest lines trimmed, got %q", got)
	}
}

func TestProjectLessonsPromptPart(t *testing.T) {
	root := t.TempDir()
	rules := projectLessonsPromptPart(root)
	if !strings.Contains(rules, ".ally/lessons.md") || strings.Contains(rules, "project-lessons") {
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
