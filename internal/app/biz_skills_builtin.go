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
	"path/filepath"

	"ally-dev/internal/builtin_skills"
)

// materializeBuiltinSkill writes an embedded skill's files into the data
// directory and returns that directory, so a skill that ships scripts can be
// read and run the way a skill on disk is. It returns "" for a skill with
// nothing but SKILL.md: those stay embedded, with no directory to point at.
func (a *App) materializeBuiltinSkill(name string) (string, error) {
	dir := filepath.Join(a.AppDataDir(), "builtin-skills", name)
	wrote, err := builtin_skills.Unpack(name, dir)
	if err != nil {
		return "", err
	}
	if !wrote {
		return "", nil
	}
	return dir, nil
}

// skillLoadDir is the directory a loaded skill reports. A built-in skill that
// ships supporting files is unpacked into the data directory first, so the model
// can read those files and run the scripts the skill documents; every other
// skill keeps the directory it was scanned from. A failed unpack is reported
// instead of falling back to the embedded alias: that alias is not a path any
// read could open, so pretending the skill loaded would only fail later.
func (a *App) skillLoadDir(sk SkillDefinition) (string, error) {
	if sk.Source != "builtin" {
		return sk.Dir, nil
	}
	dir, err := a.materializeBuiltinSkill(sk.Name)
	if err != nil {
		return "", fmt.Errorf("failed to unpack builtin skill %q: %w", sk.Name, err)
	}
	if dir == "" {
		// Nothing beside SKILL.md: the body travels with the load.
		return sk.Dir, nil
	}
	return dir, nil
}
