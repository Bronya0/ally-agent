// SPDX-License-Identifier: GPL-3.0-only
package app

import "ally-dev/internal/tools/read"

// hashVersion mirrors the version token the read/edit tools compute. Tests use
// it to forge FileTextEdits versions; production code calls read.HashVersion or
// hashBytesAndVersion directly.
func hashVersion(data []byte) string { return read.HashVersion(data) }
