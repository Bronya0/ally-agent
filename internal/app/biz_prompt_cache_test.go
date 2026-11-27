package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 缓存必须只存有界窗口：一个多兆字节的 LESSONS.md 不能被整个读进内存再截断。
func TestPromptFileCacheBoundsItsStoredContent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	lessons := filepath.Join(dir, "LESSONS.md")
	if err := os.WriteFile(lessons, []byte(strings.Repeat("lesson line\n", 200_000)), 0o600); err != nil {
		t.Fatal(err)
	}
	var c promptFileCache
	got := c.readTail(lessons, lessonsCacheTailBytes)
	if len(c.content) > lessonsCacheTailBytes {
		t.Fatalf("the tail cache stored %d bytes of a %d-byte file", len(c.content), len("lesson line\n")*200_000)
	}
	if len(got) == 0 || !strings.HasSuffix(got, "lesson line\n") {
		t.Fatalf("the tail window lost the newest lines: %d bytes", len(got))
	}

	profile := filepath.Join(dir, "USER.md")
	if err := os.WriteFile(profile, []byte(strings.Repeat("fact line\n", 100_000)), 0o600); err != nil {
		t.Fatal(err)
	}
	var p promptFileCache
	head := p.readHead(profile, userProfileMaxBytes*2+1)
	if len(p.content) > userProfileMaxBytes*2+1 {
		t.Fatalf("the head cache stored %d bytes", len(p.content))
	}
	if !strings.HasPrefix(strings.TrimSpace(head), "fact line") {
		t.Fatal("the head window lost the first lines")
	}
	if len(head) <= userProfileMaxBytes {
		t.Fatal("the caller can no longer detect truncation")
	}

	// 文件未变时走缓存，且窗口仍是上一份有界内容。
	again := p.readHead(profile, userProfileMaxBytes+1)
	if again != head {
		t.Fatal("an unchanged file re-read past the cache")
	}
}

func TestReadTailDropsThePartialFirstLine(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "LESSONS.md")
	body := strings.Repeat("A", 4096) + "\nfirst full line\nlast line\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	c := promptFileCache{}
	got := c.readTail(path, 64)
	if strings.Contains(got, strings.Repeat("A", 16)) {
		preview := got
		if len(preview) > 64 {
			preview = preview[:64]
		}
		t.Fatalf("the partial first line survived: %q", preview)
	}
	if !strings.HasSuffix(got, "last line\n") {
		t.Fatalf("the tail lines were lost: %q", got)
	}
}

// 一个多兆字节的 CODEGRAPH.md 只注入头部并带截断标记。
func TestLoadCodeGraphBoundsItsRead(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "CODEGRAPH.md"), []byte(strings.Repeat("graph row\n", 250_000)), 0o600); err != nil {
		t.Fatal(err)
	}
	got := loadCodeGraph(dir)
	if len(got) > maxCodeGraphPromptBytes+256 {
		t.Fatalf("code graph injection exceeded the cap: %d bytes", len(got))
	}
	if !strings.Contains(got, "was truncated") {
		t.Fatal("the truncation marker is missing")
	}
	if !strings.HasPrefix(strings.TrimSpace(got), "graph row") {
		t.Fatal("the head rows were lost")
	}
}
