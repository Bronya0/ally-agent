// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package builtin_skills

import (
	"bytes"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// SkillFiles lists every file an embedded skill ships, as paths inside FS.
func SkillFiles(name string) []string {
	root := path.Join(Root, name)
	var files []string
	_ = fs.WalkDir(FS, root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		files = append(files, p)
		return nil
	})
	return files
}

// Unpack writes an embedded skill into dest and reports whether anything was
// written. A skill that only has SKILL.md is left embedded: its body travels with
// the load, and giving it a directory would only add a copy nobody reads.
//
// Files whose bytes already match are skipped, so loading the skill again costs
// one read per file and never rewrites what did not change; a file that changed
// in a new version is replaced.
func Unpack(name, dest string) (bool, error) {
	files := SkillFiles(name)
	if len(files) <= 1 {
		return false, nil
	}
	root := path.Join(Root, name)
	for _, embedded := range files {
		data, err := FS.ReadFile(embedded)
		if err != nil {
			return false, err
		}
		target := filepath.Join(dest, filepath.FromSlash(strings.TrimPrefix(embedded, root+"/")))
		if current, err := os.ReadFile(target); err == nil && bytes.Equal(current, data) {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return false, err
		}
		if err := os.WriteFile(target, data, 0o644); err != nil {
			return false, err
		}
	}
	return true, nil
}
