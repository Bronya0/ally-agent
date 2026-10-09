// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package streamarchive

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStreamArchivePackAndUnpack(t *testing.T) {
	tempDir := t.TempDir()
	srcDir := filepath.Join(tempDir, "src")
	destDir := filepath.Join(tempDir, "dest")

	if err := os.MkdirAll(filepath.Join(srcDir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "hello.txt"), []byte("hello world"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "sub", "child.txt"), []byte("child data"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Create .git directory to ensure it is skipped
	gitDir := filepath.Join(srcDir, ".git")
	_ = os.MkdirAll(gitDir, 0o755)
	_ = os.WriteFile(filepath.Join(gitDir, "HEAD"), []byte("ref: refs/heads/main"), 0o644)

	var buf bytes.Buffer
	statsPack, err := Pack(srcDir, "mybundle", &buf)
	if err != nil {
		t.Fatalf("Pack failed: %v", err)
	}
	if statsPack.FileCount != 2 {
		t.Fatalf("expected 2 files packed, got %d", statsPack.FileCount)
	}

	statsUnpack, err := Unpack(&buf, destDir, false)
	if err != nil {
		t.Fatalf("Unpack failed: %v", err)
	}
	if statsUnpack.FileCount != 2 {
		t.Fatalf("expected 2 files unpacked, got %d", statsUnpack.FileCount)
	}

	// Verify unpacked files
	content1, err := os.ReadFile(filepath.Join(destDir, "mybundle", "hello.txt"))
	if err != nil || string(content1) != "hello world" {
		t.Fatalf("unexpected content for hello.txt: %s, err: %v", string(content1), err)
	}
	content2, err := os.ReadFile(filepath.Join(destDir, "mybundle", "sub", "child.txt"))
	if err != nil || string(content2) != "child data" {
		t.Fatalf("unexpected content for child.txt: %s, err: %v", string(content2), err)
	}
	// Verify .git was NOT packed/unpacked
	if _, err := os.Stat(filepath.Join(destDir, "mybundle", ".git")); !os.IsNotExist(err) {
		t.Fatalf(".git was expected not to exist")
	}
}

func TestStreamArchiveSingleFile(t *testing.T) {
	tempDir := t.TempDir()
	srcFile := filepath.Join(tempDir, "input.txt")
	if err := os.WriteFile(srcFile, []byte("single file content"), 0o644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	statsPack, err := Pack(srcFile, "custom_name.txt", &buf)
	if err != nil {
		t.Fatalf("Pack single file failed: %v", err)
	}
	if statsPack.FileCount != 1 {
		t.Fatalf("expected 1 file packed, got %d", statsPack.FileCount)
	}

	destDir := filepath.Join(tempDir, "dest_single")
	statsUnpack, err := Unpack(&buf, destDir, false)
	if err != nil {
		t.Fatalf("Unpack single file failed: %v", err)
	}
	if statsUnpack.FileCount != 1 {
		t.Fatalf("expected 1 file unpacked, got %d", statsUnpack.FileCount)
	}

	content, err := os.ReadFile(filepath.Join(destDir, "custom_name.txt"))
	if err != nil || string(content) != "single file content" {
		t.Fatalf("unexpected content: %s, err: %v", string(content), err)
	}
}

func TestStreamArchiveTarSlipProtection(t *testing.T) {
	tempDir := t.TempDir()
	destDir := filepath.Join(tempDir, "dest")

	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	maliciousHeader := &tar.Header{
		Name:     "../../evil.txt",
		Mode:     0o644,
		Size:     4,
		Typeflag: tar.TypeReg,
	}
	_ = tw.WriteHeader(maliciousHeader)
	_, _ = tw.Write([]byte("evil"))
	_ = tw.Close()

	_, err := Unpack(&buf, destDir, false)
	if err == nil {
		t.Fatalf("expected Tar-Slip attack to be rejected, but Unpack succeeded")
	}
}

func TestStreamArchiveVCSRejection(t *testing.T) {
	tempDir := t.TempDir()
	destDir := filepath.Join(tempDir, "dest")

	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	vcsHeader := &tar.Header{
		Name:     "sub/.git/hooks/pre-commit",
		Mode:     0o755,
		Size:     4,
		Typeflag: tar.TypeReg,
	}
	_ = tw.WriteHeader(vcsHeader)
	_, _ = tw.Write([]byte("evil"))
	_ = tw.Close()

	_, err := Unpack(&buf, destDir, false)
	if err == nil {
		t.Fatalf("expected VCS path unpack to be rejected, but Unpack succeeded")
	}
}

func TestStreamArchiveOverwrite(t *testing.T) {
	tempDir := t.TempDir()
	destDir := filepath.Join(tempDir, "dest")

	// 1. Pack a file
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	hdr := &tar.Header{
		Name:     "target.txt",
		Mode:     0o644,
		Size:     int64(len("new content")),
		Typeflag: tar.TypeReg,
	}
	_ = tw.WriteHeader(hdr)
	_, _ = tw.Write([]byte("new content"))
	_ = tw.Close()

	// Initial unpack
	tarBytes := buf.Bytes()
	_, err := Unpack(bytes.NewReader(tarBytes), destDir, false)
	if err != nil {
		t.Fatalf("initial unpack failed: %v", err)
	}

	// 2. Unpack without overwrite should fail
	_, err = Unpack(bytes.NewReader(tarBytes), destDir, false)
	if err == nil {
		t.Fatalf("expected unpack without overwrite to fail, but succeeded")
	}

	// 3. Unpack with overwrite should succeed
	_, err = Unpack(bytes.NewReader(tarBytes), destDir, true)
	if err != nil {
		t.Fatalf("unpack with overwrite failed: %v", err)
	}

	content, err := os.ReadFile(filepath.Join(destDir, "target.txt"))
	if err != nil || string(content) != "new content" {
		t.Fatalf("unexpected content after overwrite: %s, %v", string(content), err)
	}

	// 4. Overwrite file with directory
	var dirBuf bytes.Buffer
	dirTw := tar.NewWriter(&dirBuf)
	dirHdr := &tar.Header{
		Name:     "target.txt/",
		Mode:     0o755,
		Typeflag: tar.TypeDir,
	}
	_ = dirTw.WriteHeader(dirHdr)
	childHdr := &tar.Header{
		Name:     "target.txt/child.txt",
		Mode:     0o644,
		Size:     int64(len("child")),
		Typeflag: tar.TypeReg,
	}
	_ = dirTw.WriteHeader(childHdr)
	_, _ = dirTw.Write([]byte("child"))
	_ = dirTw.Close()

	_, err = Unpack(bytes.NewReader(dirBuf.Bytes()), destDir, true)
	if err != nil {
		t.Fatalf("overwrite file with directory failed: %v", err)
	}
	fi, err := os.Stat(filepath.Join(destDir, "target.txt"))
	if err != nil || !fi.IsDir() {
		t.Fatalf("expected target.txt to be a directory now: %v", err)
	}

	// 5. Overwrite directory with file
	_, err = Unpack(bytes.NewReader(tarBytes), destDir, true)
	if err != nil {
		t.Fatalf("overwrite directory with file failed: %v", err)
	}
	fi, err = os.Stat(filepath.Join(destDir, "target.txt"))
	if err != nil || fi.IsDir() {
		t.Fatalf("expected target.txt to be a file now: %v", err)
	}
}

func TestStreamArchiveModeNormalization(t *testing.T) {
	tempDir := t.TempDir()
	srcFile := filepath.Join(tempDir, "file.txt")
	if err := os.WriteFile(srcFile, []byte("content"), 0o666); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if _, err := Pack(srcFile, "file.txt", &buf); err != nil {
		t.Fatalf("Pack failed: %v", err)
	}

	tr := tar.NewReader(&buf)
	hdr, err := tr.Next()
	if err != nil {
		t.Fatalf("Next failed: %v", err)
	}
	// Perm must have group/other write stripped (0644 or 0755)
	if hdr.Mode&0o022 != 0 {
		t.Fatalf("expected header mode not to have group/other write, got %o", hdr.Mode)
	}
}

func TestStreamArchiveTarSlipAbsoluteAndVolume(t *testing.T) {
	tempDir := t.TempDir()
	destDir := filepath.Join(tempDir, "dest")

	for _, badName := range []string{"/absolute/path.txt", "C:/Windows/cmd.exe", "\\root\\test.txt"} {
		var buf bytes.Buffer
		tw := tar.NewWriter(&buf)
		_ = tw.WriteHeader(&tar.Header{
			Name:     badName,
			Mode:     0o644,
			Size:     4,
			Typeflag: tar.TypeReg,
		})
		_, _ = tw.Write([]byte("evil"))
		_ = tw.Close()

		_, err := Unpack(&buf, destDir, false)
		if err == nil {
			t.Fatalf("expected name %q to be rejected as Tar-Slip violation, but succeeded", badName)
		}
	}
}

func TestStreamArchiveSymlinkEscapeRejection(t *testing.T) {
	tempDir := t.TempDir()
	destDir := filepath.Join(tempDir, "dest")

	for _, badTarget := range []string{"..", "../outside", "C:escaped", "/etc/passwd"} {
		var buf bytes.Buffer
		tw := tar.NewWriter(&buf)
		_ = tw.WriteHeader(&tar.Header{
			Name:     "link_entry",
			Linkname: badTarget,
			Typeflag: tar.TypeSymlink,
		})
		_ = tw.Close()

		_, err := Unpack(&buf, destDir, false)
		if err == nil {
			t.Fatalf("expected symlink pointing to %q to be rejected, but succeeded", badTarget)
		}
	}
}

func TestStreamArchivePackWithFilter(t *testing.T) {
	tempDir := t.TempDir()
	srcDir := filepath.Join(tempDir, "src")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "safe.txt"), []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "sensitive.key"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	_, err := PackWithFilter(srcDir, "bundle", &buf, func(absPath string, isDir bool) error {
		if strings.HasSuffix(absPath, ".key") {
			return os.ErrPermission
		}
		return nil
	})
	if err == nil {
		t.Fatalf("expected PackWithFilter to fail when filter returns error, but succeeded")
	}
}
