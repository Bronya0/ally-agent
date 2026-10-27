// SPDX-License-Identifier: GPL-3.0-only
// Package resources embeds the native interface artwork and its license notices.
package resources

import "embed"

// Assets contains compressed avatar artwork and icon sources.
//
//go:embed icons avatar
var Assets embed.FS
