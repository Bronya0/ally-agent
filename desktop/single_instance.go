// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"runtime"
	"strings"
)

func singleInstanceLockName(dataDir string) string {
	path, err := filepath.Abs(dataDir)
	if err == nil {
		dataDir = path
	}
	if resolved, err := filepath.EvalSymlinks(dataDir); err == nil {
		dataDir = resolved
	}
	dataDir = filepath.Clean(dataDir)
	if runtime.GOOS == "windows" {
		dataDir = strings.ToLower(dataDir)
	}
	identity := sha256.Sum256([]byte(dataDir))
	return "Ally Native " + hex.EncodeToString(identity[:])
}
