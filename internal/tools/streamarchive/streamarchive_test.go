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
	"runtime"
	"strings"
	"testing"
)

func TestStreamArchiveFailedSymlinkPreservesDestination(t *testing.T) {
	dest := t.TempDir()
	target := filepath.Join(dest, "target")
	if err := os.WriteFile(target, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	var archive bytes.Buffer
	tw := tar.NewWriter(&archive)
	if err := tw.WriteHeader(&tar.Header{Name: "target", Typeflag: tar.TypeSymlink, Linkname: strings.Repeat("x", 8192)}); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := UnpackTarget(&archive, target, true); err == nil {
		t.Fatal("expected oversized symlink creation to fail")
	}
	if got, err := os.ReadFile(target); err != nil || string(got) != "original" {
		t.Fatalf("failed symlink replacement damaged original: %q, %v", got, err)
	}
	entries, err := os.ReadDir(dest)
	if err != nil || len(entries) != 1 {
		t.Fatalf("unexpected temporary files: %v, %v", entries, err)
	}
	if err := os.Symlink("other", filepath.Join(dest, "probe")); err != nil {
		return // The failure-preservation check above also applies without link privileges.
	}
	if err := os.Remove(filepath.Join(dest, "probe")); err != nil {
		t.Fatal(err)
	}
	archive.Reset()
	tw = tar.NewWriter(&archive)
	if err := tw.WriteHeader(&tar.Header{Name: "target", Typeflag: tar.TypeSymlink, Linkname: "other"}); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := UnpackTarget(&archive, target, true); err != nil {
		t.Fatalf("valid link replacement failed: %v", err)
	}
	if got, err := os.Readlink(target); err != nil || got != "other" {
		t.Fatalf("replacement link = %q, %v", got, err)
	}
}

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
	// Repository metadata is transferred with ordinary files.
	gitDir := filepath.Join(srcDir, ".git")
	_ = os.MkdirAll(gitDir, 0o755)
	_ = os.WriteFile(filepath.Join(gitDir, "HEAD"), []byte("ref: refs/heads/main"), 0o644)

	var buf bytes.Buffer
	statsPack, err := PackWithFilter(srcDir, "mybundle", &buf, nil)
	if err != nil {
		t.Fatalf("Pack failed: %v", err)
	}
	if statsPack.FileCount != 3 {
		t.Fatalf("expected 3 files packed, got %d", statsPack.FileCount)
	}

	statsUnpack, err := UnpackTarget(&buf, filepath.Join(destDir, "mybundle"), false)
	if err != nil {
		t.Fatalf("Unpack failed: %v", err)
	}
	if statsUnpack.FileCount != 3 {
		t.Fatalf("expected 3 files unpacked, got %d", statsUnpack.FileCount)
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
	if content, err := os.ReadFile(filepath.Join(destDir, "mybundle", ".git", "HEAD")); err != nil || string(content) != "ref: refs/heads/main" {
		t.Fatalf("metadata round trip: %q, %v", content, err)
	}

}

func TestStreamArchiveSingleFile(t *testing.T) {
	tempDir := t.TempDir()
	srcFile := filepath.Join(tempDir, "input.txt")
	if err := os.WriteFile(srcFile, []byte("single file content"), 0o644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	statsPack, err := PackWithFilter(srcFile, "custom_name.txt", &buf, nil)
	if err != nil {
		t.Fatalf("Pack single file failed: %v", err)
	}
	if statsPack.FileCount != 1 {
		t.Fatalf("expected 1 file packed, got %d", statsPack.FileCount)
	}

	destDir := filepath.Join(tempDir, "dest_single")
	statsUnpack, err := UnpackTarget(&buf, filepath.Join(destDir, "custom_name.txt"), false)
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

	_, err := UnpackTarget(&buf, filepath.Join(destDir, "requested"), false)
	if err == nil {
		t.Fatalf("expected Tar-Slip attack to be rejected, but Unpack succeeded")
	}
}

func TestStreamArchiveMetadataPaths(t *testing.T) {
	names := []string{"sub/.git/hooks/pre-commit"}
	if runtime.GOOS == "windows" {
		names = append(names, "sub/.Git/hooks/pre-commit", "sub/.SVN/entries", "sub/.HG/config")
	}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			var buf bytes.Buffer
			tw := tar.NewWriter(&buf)
			_ = tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: 4, Typeflag: tar.TypeReg})
			_, _ = tw.Write([]byte("evil"))
			_ = tw.Close()
			dest := t.TempDir()
			if _, err := UnpackTarget(&buf, filepath.Join(dest, "sub"), false); err != nil {
				t.Fatal(err)
			}
			if data, err := os.ReadFile(filepath.Join(dest, filepath.FromSlash(name))); err != nil || string(data) != "evil" {
				t.Fatalf("metadata content=%q, %v", data, err)
			}
		})
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
	_, err := UnpackTarget(bytes.NewReader(tarBytes), filepath.Join(destDir, "target.txt"), false)
	if err != nil {
		t.Fatalf("initial unpack failed: %v", err)
	}

	// 2. Unpack without overwrite should fail
	_, err = UnpackTarget(bytes.NewReader(tarBytes), filepath.Join(destDir, "target.txt"), false)
	if err == nil {
		t.Fatalf("expected unpack without overwrite to fail, but succeeded")
	}

	// 3. Unpack with overwrite should succeed
	_, err = UnpackTarget(bytes.NewReader(tarBytes), filepath.Join(destDir, "target.txt"), true)
	if err != nil {
		t.Fatalf("unpack with overwrite failed: %v", err)
	}

	content, err := os.ReadFile(filepath.Join(destDir, "target.txt"))
	if err != nil || string(content) != "new content" {
		t.Fatalf("unexpected content after overwrite: %s, %v", string(content), err)
	}

	// 4. A directory transfer must not delete an existing file to make room.
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

	_, err = UnpackTarget(bytes.NewReader(dirBuf.Bytes()), filepath.Join(destDir, "target.txt"), true)
	if err == nil {
		t.Fatal("accepted replacing a file with a directory")
	}
	if got, err := os.ReadFile(filepath.Join(destDir, "target.txt")); err != nil || string(got) != "new content" {
		t.Fatalf("existing file damaged: %q, %v", got, err)
	}
	// Prepare a fresh directory for the inverse conflict.
	if err := os.Remove(filepath.Join(destDir, "target.txt")); err != nil {
		t.Fatal(err)
	}
	if _, err := UnpackTarget(bytes.NewReader(dirBuf.Bytes()), filepath.Join(destDir, "target.txt"), true); err != nil {
		t.Fatal(err)
	}

	// 5. A file must never replace an existing directory, even with overwrite.
	_, err = UnpackTarget(bytes.NewReader(tarBytes), filepath.Join(destDir, "target.txt"), true)
	if err == nil {
		t.Fatal("accepted replacing a directory with a file")
	}
	fi, err := os.Stat(filepath.Join(destDir, "target.txt"))
	if err != nil || !fi.IsDir() {
		t.Fatalf("existing directory was replaced: %v", err)
	}
	if content, err := os.ReadFile(filepath.Join(destDir, "target.txt", "child.txt")); err != nil || string(content) != "child" {
		t.Fatalf("existing directory contents changed: %q, %v", content, err)
	}
}

func TestStreamArchiveOverwritePreservesNestedDirectory(t *testing.T) {
	dest := t.TempDir()
	target := filepath.Join(dest, "target")
	metadata := filepath.Join(target, "nested", ".git", "config")
	if err := os.MkdirAll(filepath.Dir(metadata), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(metadata, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	if err := tw.WriteHeader(&tar.Header{Name: "target/nested", Typeflag: tar.TypeReg, Mode: 0o644, Size: 3}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte("new")); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := UnpackTarget(&buf, target, true); err == nil {
		t.Fatal("accepted replacing a nested directory with a file")
	}
	if data, err := os.ReadFile(metadata); err != nil || string(data) != "original" {
		t.Fatalf("existing directory contents changed: %q, %v", data, err)
	}
}

func TestStreamArchiveModeNormalization(t *testing.T) {
	tempDir := t.TempDir()
	srcFile := filepath.Join(tempDir, "file.txt")
	if err := os.WriteFile(srcFile, []byte("content"), 0o666); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if _, err := PackWithFilter(srcFile, "file.txt", &buf, nil); err != nil {
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

		_, err := UnpackTarget(&buf, filepath.Join(destDir, "requested"), false)
		if err == nil {
			t.Fatalf("expected name %q to be rejected as Tar-Slip violation, but succeeded", badName)
		}
	}
}

func TestStreamArchiveSymlinkEscapeRejection(t *testing.T) {
	tempDir := t.TempDir()
	destDir := filepath.Join(tempDir, "dest")

	badTargets := []string{"..", "../outside", "/etc/passwd"}
	if runtime.GOOS == "windows" {
		// filepath.VolumeName only sees a drive there; on POSIX "C:escaped" is an
		// ordinary relative filename and must not be rejected.
		badTargets = append(badTargets, "C:escaped")
	}
	for _, badTarget := range badTargets {
		var buf bytes.Buffer
		tw := tar.NewWriter(&buf)
		_ = tw.WriteHeader(&tar.Header{
			Name:     "link_entry",
			Linkname: badTarget,
			Typeflag: tar.TypeSymlink,
		})
		_ = tw.Close()

		_, err := UnpackTarget(&buf, filepath.Join(destDir, "link_entry"), false)
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

func TestStreamArchiveEmptyDirectory(t *testing.T) {
	src := t.TempDir()
	var buf bytes.Buffer
	packed, err := PackWithFilter(src, "empty", &buf, nil)
	if err != nil {
		t.Fatal(err)
	}
	dest := t.TempDir()
	unpacked, err := UnpackTarget(&buf, filepath.Join(dest, "empty"), false)
	if err != nil {
		t.Fatal(err)
	}
	if packed.DirCount != 1 || unpacked.DirCount != 1 {
		t.Fatalf("missing root directory: %+v %+v", packed, unpacked)
	}
	if info, err := os.Stat(filepath.Join(dest, "empty")); err != nil || !info.IsDir() {
		t.Fatalf("empty directory lost: %v", err)
	}
}

func TestStreamArchiveTargetRejectsSibling(t *testing.T) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	if err := tw.WriteHeader(&tar.Header{Name: "sibling.txt", Typeflag: tar.TypeReg, Size: 4, Mode: 0o644}); err != nil {
		t.Fatal(err)
	}
	_, _ = tw.Write([]byte("evil"))
	_ = tw.Close()
	dest := t.TempDir()
	if _, err := UnpackTarget(&buf, filepath.Join(dest, "requested"), true); err == nil {
		t.Fatal("accepted sibling outside requested destination")
	}
	if _, err := os.Stat(filepath.Join(dest, "sibling.txt")); !os.IsNotExist(err) {
		t.Fatal("sibling was written")
	}
}

func TestStreamArchiveExistingSymlinkParent(t *testing.T) {
	dest := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(dest, "link")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	_ = tw.WriteHeader(&tar.Header{Name: "link/evil.txt", Typeflag: tar.TypeReg, Size: 4, Mode: 0o644})
	_, _ = tw.Write([]byte("evil"))
	_ = tw.Close()
	if _, err := UnpackTarget(&buf, filepath.Join(dest, "link"), true); err == nil {
		t.Fatal("accepted symlink parent escape")
	}
	if _, err := os.Stat(filepath.Join(outside, "evil.txt")); !os.IsNotExist(err) {
		t.Fatal("wrote outside destination")
	}
}

func TestStreamArchiveTruncatedReplacementPreservesDestination(t *testing.T) {
	for _, directory := range []bool{false, true} {
		dest := t.TempDir()
		target := filepath.Join(dest, "target")
		original := target
		if directory {
			if err := os.Mkdir(target, 0o755); err != nil {
				t.Fatal(err)
			}
			original = filepath.Join(target, "original.txt")
		}
		if err := os.WriteFile(original, []byte("original"), 0o600); err != nil {
			t.Fatal(err)
		}
		var buf bytes.Buffer
		tw := tar.NewWriter(&buf)
		_ = tw.WriteHeader(&tar.Header{Name: "target", Typeflag: tar.TypeReg, Size: 4096, Mode: 0o644})
		_, _ = tw.Write([]byte("partial"))
		if _, err := UnpackTarget(&buf, target, true); err == nil {
			t.Fatal("accepted truncated archive")
		}
		if data, err := os.ReadFile(original); err != nil || string(data) != "original" {
			t.Fatalf("destroyed original: %q %v", data, err)
		}
		matches, _ := filepath.Glob(filepath.Join(dest, "*.ally-tmp-*"))
		if len(matches) != 0 {
			t.Fatalf("temporary files leaked: %v", matches)
		}
	}
}

func TestStreamArchivePrivateModePreserved(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permissions")
	}
	src := filepath.Join(t.TempDir(), "private")
	if err := os.WriteFile(src, []byte("private"), 0o600); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if _, err := PackWithFilter(src, "private", &buf, nil); err != nil {
		t.Fatal(err)
	}
	hdr, err := tar.NewReader(&buf).Next()
	if err != nil {
		t.Fatal(err)
	}
	if hdr.Mode != 0o600 {
		t.Fatalf("private permissions broadened to %o", hdr.Mode)
	}
}

func TestStreamArchiveWorkspaceRootRejectsOutsideParent(t *testing.T) {
	ws, outside := t.TempDir(), t.TempDir()
	if err := os.Symlink(outside, filepath.Join(ws, "link")); err != nil {
		t.Skip(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "source"), []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(ws)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	var packed bytes.Buffer
	if _, err := PackRoot(root, "link/source", "file", &packed, nil); err == nil {
		t.Fatal("packed file outside workspace")
	}
	var archive bytes.Buffer
	tw := tar.NewWriter(&archive)
	if err := tw.WriteHeader(&tar.Header{Name: "file", Typeflag: tar.TypeReg, Size: 4, Mode: 0600}); err != nil {
		t.Fatal(err)
	}
	_, _ = tw.Write([]byte("evil"))
	_ = tw.Close()
	if _, err := UnpackRoot(&archive, root, "link/new/file", true); err == nil {
		t.Fatal("unpacked outside workspace")
	}
	if _, err := os.Stat(filepath.Join(outside, "new")); !os.IsNotExist(err) {
		t.Fatalf("created outside parent: %v", err)
	}
}

func TestStreamArchiveReadOnlyDirectory(t *testing.T) {
	dest := t.TempDir()
	t.Cleanup(func() {
		_ = os.Chmod(filepath.Join(dest, "pkg"), 0700)
		_ = os.Chmod(filepath.Join(dest, "pkg", "sub"), 0700)
	})
	var archive bytes.Buffer
	tw := tar.NewWriter(&archive)
	for _, h := range []*tar.Header{
		{Name: "pkg", Typeflag: tar.TypeDir, Mode: 0555},
		{Name: "pkg/sub", Typeflag: tar.TypeDir, Mode: 0500},
		{Name: "pkg/sub/file", Typeflag: tar.TypeReg, Mode: 0600, Size: 4},
	} {
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if h.Typeflag == tar.TypeReg {
			_, _ = tw.Write([]byte("data"))
		}
	}
	_ = tw.Close()
	if _, err := UnpackTarget(&archive, filepath.Join(dest, "pkg"), false); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(dest, "pkg", "sub", "file")); err != nil || string(data) != "data" {
		t.Fatalf("file=%q, %v", data, err)
	}
	if runtime.GOOS != "windows" {
		for path, mode := range map[string]os.FileMode{"pkg": 0555, "pkg/sub": 0500} {
			info, err := os.Stat(filepath.Join(dest, path))
			if err != nil || info.Mode().Perm() != mode {
				t.Fatalf("permissions %s = %v, %v", path, info, err)
			}
		}
	}
}

func TestStreamArchiveInternalParentSymlink(t *testing.T) {
	dest := t.TempDir()
	for _, tc := range []struct {
		link  string
		valid bool
	}{{"../data", true}, {"../../outside", false}} {
		t.Run(tc.link, func(t *testing.T) {
			var archive bytes.Buffer
			tw := tar.NewWriter(&archive)
			_ = tw.WriteHeader(&tar.Header{Name: "pkg/data", Typeflag: tar.TypeReg, Mode: 0600, Size: 4})
			_, _ = tw.Write([]byte("data"))
			_ = tw.WriteHeader(&tar.Header{Name: "pkg/sub/link", Typeflag: tar.TypeSymlink, Linkname: tc.link})
			_ = tw.Close()
			_, err := UnpackTarget(&archive, filepath.Join(dest, "pkg"), true)
			if !tc.valid {
				if err == nil {
					t.Fatal("accepted escaping symlink")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if data, err := os.ReadFile(filepath.Join(dest, "pkg", "sub", "link")); err != nil || string(data) != "data" {
				t.Fatalf("link content=%q, %v", data, err)
			}
		})
	}
}

func TestStreamArchiveFailedReadOnlyDirectoryRestoresMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix directory permissions")
	}
	dest := t.TempDir()
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(dest, "pkg"), 0700) })
	var archive bytes.Buffer
	tw := tar.NewWriter(&archive)
	if err := tw.WriteHeader(&tar.Header{Name: "pkg", Typeflag: tar.TypeDir, Mode: 0555}); err != nil {
		t.Fatal(err)
	}
	if err := tw.WriteHeader(&tar.Header{Name: "pkg/file", Typeflag: tar.TypeReg, Mode: 0600, Size: 4096}); err != nil {
		t.Fatal(err)
	}
	_, _ = tw.Write([]byte("incomplete"))
	if _, err := UnpackTarget(&archive, filepath.Join(dest, "pkg"), false); err == nil {
		t.Fatal("accepted truncated archive")
	}
	if info, err := os.Stat(filepath.Join(dest, "pkg")); err != nil || info.Mode().Perm() != 0555 {
		t.Fatalf("failed extraction directory mode: %v, %v", info, err)
	}
	if _, err := os.Stat(filepath.Join(dest, "pkg", "file")); !os.IsNotExist(err) {
		t.Fatalf("published incomplete file: %v", err)
	}
}

func TestStreamArchiveWorkspaceNestedRoundTrip(t *testing.T) {
	ws := t.TempDir()
	source := filepath.Join(ws, "src", "pkg", "file")
	if err := os.MkdirAll(filepath.Dir(source), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("content"), 0600); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(ws)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	var archive bytes.Buffer
	var filteredFile string
	packed, err := PackRoot(root, "src/pkg", "renamed", &archive, func(path string, isDir bool) error {
		if !isDir {
			filteredFile = path
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if filteredFile != source {
		t.Fatalf("filter source=%s, want %s", filteredFile, source)
	}
	unpacked, err := UnpackRoot(&archive, root, filepath.Join("out", "renamed"), false)
	if err != nil {
		t.Fatal(err)
	}
	if packed != unpacked || unpacked.FileCount != 1 {
		t.Fatalf("statistics: packed=%+v unpacked=%+v", packed, unpacked)
	}
	if data, err := os.ReadFile(filepath.Join(ws, "out", "renamed", "file")); err != nil || string(data) != "content" {
		t.Fatalf("download=%q, %v", data, err)
	}
}
