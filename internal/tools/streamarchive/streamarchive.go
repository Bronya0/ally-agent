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
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Stats records the outcome of an archive pack or unpack operation.
type Stats struct {
	TotalBytes int64 `json:"totalBytes"`
	FileCount  int   `json:"fileCount"`
	DirCount   int   `json:"dirCount"`
}

// PathFilter checks whether a file or directory path is allowed to be packed.
// An error halts packing immediately with that error.
type PathFilter func(absPath string, isDir bool) error

// PackWithFilter streams a local file or directory with an optional per-entry path filter.
func PackWithFilter(srcPath, arcRootName string, w io.Writer, filter PathFilter) (Stats, error) {
	root, err := os.OpenRoot(filepath.Dir(srcPath))
	if err != nil {
		return Stats{}, err
	}
	defer root.Close()
	return PackRoot(root, filepath.Base(srcPath), arcRootName, w, filter)
}

// PackRoot packs a workspace-relative source using a bounded directory handle.
func PackRoot(root *os.Root, srcPath, arcRootName string, w io.Writer, filter PathFilter) (Stats, error) {
	srcPath = filepath.ToSlash(srcPath)
	if !fs.ValidPath(srcPath) {
		return Stats{}, fmt.Errorf("invalid source path: %s", srcPath)
	}
	absoluteFilter := filter
	if filter != nil {
		filter = func(current string, isDir bool) error {
			return absoluteFilter(filepath.Join(root.Name(), filepath.FromSlash(current)), isDir)
		}
	}
	var stats Stats
	fi, err := root.Lstat(srcPath)
	if err != nil {
		return stats, err
	}

	tw := tar.NewWriter(w)
	defer tw.Close()

	if arcRootName == "" {
		arcRootName = filepath.Base(srcPath)
	}
	arcRootName = filepath.ToSlash(filepath.Clean(arcRootName))
	if arcRootName == "." || arcRootName == "/" || strings.HasPrefix(arcRootName, "/") || strings.HasPrefix(arcRootName, "../") || arcRootName == ".." || filepath.VolumeName(arcRootName) != "" || filepath.IsAbs(arcRootName) {
		return stats, fmt.Errorf("invalid archive root name: %s", arcRootName)
	}

	buf := make([]byte, 64*1024)

	// Single file pack
	if !fi.IsDir() {
		if filter != nil {
			if err := filter(srcPath, false); err != nil {
				return stats, err
			}
		}
		linkTarget := ""
		if fi.Mode()&os.ModeSymlink != 0 {
			link, err := root.Readlink(srcPath)
			if err != nil {
				return stats, fmt.Errorf("read symlink %s: %w", srcPath, err)
			}
			linkTarget = filepath.ToSlash(link)
		}
		header, err := tar.FileInfoHeader(fi, linkTarget)
		if err != nil {
			return stats, fmt.Errorf("create tar header: %w", err)
		}
		header.Name = arcRootName
		header.Mode = header.Mode & 0o777 &^ 0o022
		if err := tw.WriteHeader(header); err != nil {
			return stats, fmt.Errorf("write tar header: %w", err)
		}
		if fi.Mode().IsRegular() {
			f, err := root.Open(srcPath)
			if err != nil {
				return stats, fmt.Errorf("open source file: %w", err)
			}
			defer f.Close()

			n, err := io.CopyBuffer(tw, f, buf)
			if err != nil {
				return stats, fmt.Errorf("stream copy file content: %w", err)
			}
			stats.TotalBytes += n
			stats.FileCount++
		}
		return stats, tw.Close()
	}

	// Directory tree pack
	srcClean := filepath.ToSlash(filepath.Clean(srcPath))
	err = fs.WalkDir(root.FS(), srcClean, func(current string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(srcClean, current)
		if err != nil {
			return err
		}
		relPosix := filepath.ToSlash(rel)
		if filter != nil {
			if err := filter(current, info.IsDir()); err != nil {
				return err
			}
		}

		arcName := arcRootName
		if rel != "." {
			arcName += "/" + relPosix
		}
		linkTarget := ""
		if info.Mode()&os.ModeSymlink != 0 {
			link, err := root.Readlink(current)
			if err != nil {
				return err
			}
			linkTarget = filepath.ToSlash(link)
		}

		header, err := tar.FileInfoHeader(info, linkTarget)
		if err != nil {
			return fmt.Errorf("create tar header for %s: %w", relPosix, err)
		}
		header.Name = arcName
		header.Mode = header.Mode & 0o777 &^ 0o022

		if err := tw.WriteHeader(header); err != nil {
			return fmt.Errorf("write tar header for %s: %w", relPosix, err)
		}

		if info.Mode().IsRegular() {
			f, err := root.Open(current)
			if err != nil {
				return fmt.Errorf("open file %s: %w", current, err)
			}
			n, err := io.CopyBuffer(tw, f, buf)
			_ = f.Close()
			if err != nil {
				return fmt.Errorf("stream copy file %s: %w", current, err)
			}
			stats.TotalBytes += n
			stats.FileCount++
		} else if info.IsDir() {
			stats.DirCount++
		}
		return nil
	})
	if err != nil {
		return stats, err
	}
	return stats, tw.Close()
}

// UnpackTarget confines every entry to the requested file or directory, so a
// remote archive cannot modify siblings of the destination.
func UnpackTarget(r io.Reader, target string, overwrite bool) (Stats, error) {
	destClean := filepath.Dir(target)
	if err := os.MkdirAll(destClean, 0o755); err != nil {
		return Stats{}, fmt.Errorf("create destination directory: %w", err)
	}

	// All filesystem operations are relative to a directory handle. Unlike a
	// lexical prefix check, os.Root also rejects escapes via existing symlinks
	// and concurrent symlink replacements.
	root, err := os.OpenRoot(destClean)
	if err != nil {
		return Stats{}, fmt.Errorf("open destination root: %w", err)
	}
	defer root.Close()

	return UnpackRoot(r, root, filepath.Base(target), overwrite)
}

// UnpackRoot confines archive writes to a workspace-relative target. The archive
// root is its basename; ancestors are always accessed through the workspace root.
func UnpackRoot(r io.Reader, root *os.Root, target string, overwrite bool) (Stats, error) {
	var stats Stats
	target = filepath.Clean(target)
	if !fs.ValidPath(filepath.ToSlash(target)) || target == "." {
		return stats, fmt.Errorf("invalid target path: %s", target)
	}
	destClean := filepath.Dir(target)
	expectedRoot := filepath.Base(target)
	tr := tar.NewReader(r)
	buf := make([]byte, 64*1024)

	// Keep track of extracted files and dirs for setting mod times at the end
	type dirModTime struct {
		path  string
		mtime time.Time
		mode  os.FileMode
	}
	var dirTimes []dirModTime
	defer func() {
		for i := len(dirTimes) - 1; i >= 0; i-- {
			if dirTimes[i].mode != 0 {
				_ = root.Chmod(dirTimes[i].path, dirTimes[i].mode)
			}
		}
	}()

	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return stats, fmt.Errorf("read tar archive: %w", err)
		}

		name := filepath.ToSlash(filepath.Clean(header.Name))
		if name == "." || name == "/" || strings.HasPrefix(name, "/") || strings.HasPrefix(name, "../") || name == ".." || filepath.VolumeName(name) != "" || filepath.IsAbs(name) {
			return stats, fmt.Errorf("Tar-Slip violation: illegal relative path %s", header.Name)
		}
		if expectedRoot != "" && name != expectedRoot && !strings.HasPrefix(name, expectedRoot+"/") {
			return stats, fmt.Errorf("Tar-Slip violation: entry outside transfer destination: %s", header.Name)
		}

		targetPath := filepath.Join(destClean, filepath.FromSlash(name))
		rel, err := filepath.Rel(destClean, targetPath)
		if err != nil || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." {
			return stats, fmt.Errorf("Tar-Slip violation: target escapes destination %s -> %s", header.Name, targetPath)
		}

		// Never traverse a symlink supplied by the archive or already present in
		// the destination, even when it points to another location inside root.
		name = filepath.Join(destClean, name)
		for parent := filepath.Dir(name); parent != "."; parent = filepath.Dir(parent) {
			info, err := root.Lstat(parent)
			if err != nil && !os.IsNotExist(err) {
				return stats, err
			}
			if err == nil && info.Mode()&os.ModeSymlink != 0 {
				return stats, fmt.Errorf("Tar-Slip violation: symlink parent: %s", parent)
			}
		}
		// Root methods take relative paths, including paths kept for timestamps.
		targetPath = name
		switch header.Typeflag {
		case tar.TypeDir:
			created := false
			if existing, err := root.Lstat(targetPath); os.IsNotExist(err) {
				created = true
			} else if err != nil {
				return stats, err
			} else {
				if !existing.IsDir() {
					return stats, fmt.Errorf("cannot replace a file or symlink with a directory: %s", rel)
				}
			}
			mode := header.FileInfo().Mode().Perm() &^ 0o022
			if mode == 0 {
				mode = 0o755
			}
			if err := root.MkdirAll(targetPath, mode|0o700); err != nil {
				return stats, fmt.Errorf("create directory %s: %w", targetPath, err)
			}
			restoreMode := os.FileMode(0)
			if created {
				restoreMode = mode
			}
			dirTimes = append(dirTimes, dirModTime{path: targetPath, mtime: header.ModTime, mode: restoreMode})
			stats.DirCount++

		case tar.TypeReg, tar.TypeRegA:
			parent := filepath.Dir(targetPath)
			if err := root.MkdirAll(parent, 0o755); err != nil {
				return stats, fmt.Errorf("create parent directory for %s: %w", targetPath, err)
			}

			// Existence check
			if existing, err := root.Lstat(targetPath); err == nil {
				if existing.IsDir() {
					return stats, fmt.Errorf("cannot replace a directory with a file: %s; choose a file path inside the directory", rel)
				}
				if !overwrite {
					return stats, fmt.Errorf("file already exists: %s", rel)
				}
			}

			// Atomic write via temp file
			tmpPath := targetPath + fmt.Sprintf(".ally-tmp-%d-%d", os.Getpid(), time.Now().UnixNano())
			mode := header.FileInfo().Mode().Perm() &^ 0o022
			if mode == 0 {
				mode = 0o644
			}
			tmpFile, err := root.OpenFile(tmpPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
			if err != nil {
				return stats, fmt.Errorf("create temp file for %s: %w", targetPath, err)
			}

			n, copyErr := io.CopyBuffer(tmpFile, tr, buf)
			closeErr := tmpFile.Close()
			if copyErr != nil {
				_ = root.Remove(tmpPath)
				return stats, fmt.Errorf("write file %s: %w", targetPath, copyErr)
			}
			if closeErr != nil {
				_ = root.Remove(tmpPath)
				return stats, fmt.Errorf("close temp file %s: %w", targetPath, closeErr)
			}

			// Link publishes without clobbering a concurrently created destination.
			if !overwrite {
				if err := root.Link(tmpPath, targetPath); err != nil {
					_ = root.Remove(tmpPath)
					return stats, fmt.Errorf("publish file %s: %w", targetPath, err)
				}
				_ = root.Remove(tmpPath)
			} else {
				// Rename rejects directories that appeared after the initial check.
				// Never remove a directory to make a file replacement succeed.
				if err := root.Rename(tmpPath, targetPath); err != nil {
					_ = root.Remove(tmpPath)
					return stats, fmt.Errorf("atomic rename to %s: %w", targetPath, err)
				}
			}

			if header.ModTime.Unix() > 0 {
				_ = root.Chtimes(targetPath, header.ModTime, header.ModTime)
			}
			stats.TotalBytes += n
			stats.FileCount++

		case tar.TypeSymlink:
			linkTarget := filepath.Clean(header.Linkname)
			resolvedLink := filepath.Clean(filepath.Join(filepath.Dir(targetPath), linkTarget))
			transferRoot := filepath.Join(destClean, expectedRoot)
			linkBoundary := transferRoot
			if targetPath == transferRoot {
				linkBoundary = destClean
			}
			linkRel, linkErr := filepath.Rel(linkBoundary, resolvedLink)
			if filepath.IsAbs(linkTarget) || filepath.VolumeName(linkTarget) != "" || linkErr != nil || linkRel == ".." || strings.HasPrefix(linkRel, ".."+string(filepath.Separator)) {
				return stats, fmt.Errorf("refusing symlink pointing outside archive: %s -> %s", header.Name, header.Linkname)
			}
			parent := filepath.Dir(targetPath)
			if err := root.MkdirAll(parent, 0o755); err != nil {
				return stats, err
			}
			if !overwrite {
				// Symlink itself is an atomic no-clobber creation.
				if err := root.Symlink(header.Linkname, targetPath); err != nil {
					return stats, fmt.Errorf("create symlink %s: %w", targetPath, err)
				}
			} else {
				// Prepare the link before replacing anything: creation may fail
				// because of permissions, unsupported filesystems or bad targets.
				tmpPath := filepath.Join(parent, fmt.Sprintf(".ally-link-%d-%d", os.Getpid(), time.Now().UnixNano()))
				if err := root.Symlink(header.Linkname, tmpPath); err != nil {
					return stats, fmt.Errorf("create symlink %s: %w", targetPath, err)
				}
				err := root.Rename(tmpPath, targetPath)
				_ = root.Remove(tmpPath)
				if err != nil {
					return stats, fmt.Errorf("replace symlink %s: %w", targetPath, err)
				}
			}

		default:
			return stats, fmt.Errorf("unsupported tar entry type %d: %s", header.Typeflag, header.Name)
		}
	}

	// Restore directory modtimes in reverse order (deepest directories first)
	for i := len(dirTimes) - 1; i >= 0; i-- {
		dt := dirTimes[i]
		if dt.mode != 0 {
			if err := root.Chmod(dt.path, dt.mode); err != nil {
				return stats, fmt.Errorf("restore directory permissions %s: %w", dt.path, err)
			}
		}
		dirTimes[i].mode = 0
		if dt.mtime.Unix() > 0 {
			_ = root.Chtimes(dt.path, dt.mtime, dt.mtime)
		}
	}

	return stats, nil
}
