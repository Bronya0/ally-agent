// SPDX-License-Identifier: GPL-3.0-only
package edit

import (
	"crypto/sha256"
	"fmt"
)

// RangeToken is a stateless checksum of the current file, its host/file
// identity, and exact inclusive bounds. Only complete explicit reads publish it.
func RangeToken(content, identity string, start, end int) string {
	return rangeTokenForHash(sha256.Sum256([]byte(content)), identity, start, end)
}

func rangeTokenForHash(snapshotHash [32]byte, identity string, start, end int) string {
	h := sha256.New()
	fmt.Fprintf(h, "ally-edit-range-v1\x00%s\x00%d:%d\x00", identity, start, end)
	h.Write(snapshotHash[:])
	return fmt.Sprintf("%x", h.Sum(nil)[:16])
}
