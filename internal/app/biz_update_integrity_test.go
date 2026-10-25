// SPDX-License-Identifier: GPL-3.0-only
package app

import (
	"os"
	"path/filepath"
	"testing"
)

func updateTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestUpdateReceiptDetectsChangesAndCrossVersionReuse(t *testing.T) {
	path := filepath.Join(t.TempDir(), "archive.zip")
	updateTestFile(t, path, "downloaded contents")
	if err := saveUpdateReceipt(path, "v2.0.0", "Ally-v2.0.0-windows-x64.zip"); err != nil {
		t.Fatal(err)
	}
	if err := verifyUpdateReceipt(path, "v2.0.0", "Ally-v2.0.0-windows-x64.zip"); err != nil {
		t.Fatal(err)
	}
	if err := verifyUpdateReceipt(path, "v3.0.0", "Ally-v3.0.0-windows-x64.zip"); err == nil {
		t.Fatal("cross-version receipt accepted")
	}
	updateTestFile(t, path, "tampered!! contents") // Same byte count, different hash.
	if err := verifyUpdateReceipt(path, "v2.0.0", "Ally-v2.0.0-windows-x64.zip"); err == nil {
		t.Fatal("modified archive accepted")
	}
}

func TestUpdateRejectsMagicOnlyExecutablesAndDMGs(t *testing.T) {
	root := t.TempDir()
	pe := filepath.Join(root, "Ally.exe")
	data := make([]byte, 128)
	copy(data, "MZ")
	data[0x3c] = 64
	copy(data[64:], "PE\x00\x00")
	if err := os.WriteFile(pe, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateStagedExecutable(pe); err == nil {
		t.Fatal("header-only PE accepted")
	}
	dmg := filepath.Join(root, "download.dmg")
	data = make([]byte, 1<<20)
	copy(data[len(data)-512:], "koly")
	if err := os.WriteFile(dmg, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateStagedDMG(dmg); err == nil {
		t.Fatal("header-only DMG accepted")
	}
	app := filepath.Join(root, "Ally.app")
	updateTestFile(t, filepath.Join(app, "Contents", "Info.plist"), "invalid metadata")
	data = make([]byte, 64)
	copy(data, []byte{0xfe, 0xed, 0xfa, 0xcf})
	exe := filepath.Join(app, "Contents", "MacOS", "Ally")
	if err := os.MkdirAll(filepath.Dir(exe), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exe, data, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := validateMacAppBundle(app); err == nil {
		t.Fatal("header-only app accepted")
	}
}
