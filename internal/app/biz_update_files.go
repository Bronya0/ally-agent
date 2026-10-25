// SPDX-License-Identifier: GPL-3.0-only
package app

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// All installation writes are relative to an already-open directory. Root
// enforces containment even when a parent link changes after validation.
func updateLocalPath(name string) error {
	if !filepath.IsLocal(name) || name == "." || strings.ContainsAny(filepath.ToSlash(name), ":\\") {
		return fmt.Errorf("invalid update path %q", name)
	}
	for _, part := range strings.Split(filepath.ToSlash(name), "/") {
		if part == ".." || strings.HasPrefix(strings.ToLower(part), ".ally-update") {
			return fmt.Errorf("reserved update path %q", name)
		}
	}
	return nil
}

func updateNoLinks(root *os.Root, name string, allowMissing bool) error {
	if err := updateLocalPath(name); err != nil {
		return err
	}
	parts := strings.Split(filepath.ToSlash(name), "/")
	for i := range parts {
		info, err := root.Lstat(filepath.FromSlash(strings.Join(parts[:i+1], "/")))
		if allowMissing && errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || (!info.IsDir() && i < len(parts)-1) {
			return fmt.Errorf("update path contains a link or non-directory: %s", name)
		}
	}
	return nil
}

func copyUpdateFile(source, target *os.Root, sourceName, targetName string) error {
	if err := updateNoLinks(source, sourceName, false); err != nil {
		return err
	}
	if err := updateNoLinks(target, targetName, true); err != nil {
		return err
	}
	in, err := source.Open(sourceName)
	if err != nil {
		return err
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("not a regular update file: %s", sourceName)
	}
	if err := target.MkdirAll(filepath.Dir(targetName), 0o755); err != nil {
		return err
	}
	tmp := filepath.Join(filepath.Dir(targetName), fmt.Sprintf(".ally-copy-%x", randBytesForUpdate()))
	out, err := target.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm())
	if err != nil {
		return err
	}
	defer target.Remove(tmp)
	_, copyErr := io.Copy(out, in)
	if copyErr == nil {
		copyErr = out.Sync()
	}
	if closeErr := out.Close(); copyErr == nil {
		copyErr = closeErr
	}
	if copyErr != nil {
		return copyErr
	}
	if err := target.Rename(tmp, targetName); err != nil {
		return err
	}
	return syncUpdateDirectory(target, filepath.Dir(targetName))
}

func copyUpdatePath(sourceDir, targetDir, name string) error {
	source, err := os.OpenRoot(sourceDir)
	if err != nil {
		return err
	}
	defer source.Close()
	target, err := os.OpenRoot(targetDir)
	if err != nil {
		return err
	}
	defer target.Close()
	return copyUpdateFile(source, target, name, name)
}

func randBytesForUpdate() []byte {
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		panic(err)
	}
	return nonce[:]
}

func syncUpdateDirectory(root *os.Root, name string) error {
	f, err := root.Open(name)
	if err != nil {
		return err
	}
	defer f.Close()
	// Windows does not support FlushFileBuffers on a directory handle.
	return syncUpdateDirFile(f)
}
