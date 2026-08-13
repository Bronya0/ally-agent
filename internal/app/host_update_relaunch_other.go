// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.

//go:build !darwin

package app

import "errors"

func startUpdateRelaunchHelper(_ string) error {
	return errors.New("update relaunch helper is only available on macOS")
}

// RunUpdateRelaunchHelper recognizes the internal helper invocation. Non-macOS
// builds never consume it, so normal CLI arguments keep their existing meaning.
func RunUpdateRelaunchHelper(_ []string) (bool, error) {
	return false, nil
}
