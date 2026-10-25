//go:build !darwin

// SPDX-License-Identifier: GPL-3.0-only
package app

import "errors"

func exchangeUpdateBundles(_, _ string) error { return errors.New("bundle updates require macOS") }
