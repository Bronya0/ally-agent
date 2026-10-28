// SPDX-License-Identifier: GPL-3.0-only
// Package resources embeds the native interface artwork and its license notices.
package resources

import "embed"

// Assets contains compressed avatar artwork and icon sources.
//
//go:embed icon.png icons avatar fonts preview
var Assets embed.FS
