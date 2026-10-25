// SPDX-License-Identifier: GPL-3.0-only
package app

import (
	"errors"
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
)

// Exchange complete sibling bundles atomically; the installed path is never
// missing, and the old bundle becomes the backup in the same operation.
func exchangeUpdateBundles(install, backup string) error {
	if filepath.Dir(install) != filepath.Dir(backup) {
		return errors.New("bundle backup must share the installation directory")
	}
	root, err := os.OpenRoot(filepath.Dir(install))
	if err != nil {
		return err
	}
	defer root.Close()
	for _, name := range []string{filepath.Base(install), filepath.Base(backup)} {
		if err := updateNoLinks(root, name, false); err != nil {
			return err
		}
	}
	parent, err := root.Open(".")
	if err != nil {
		return err
	}
	defer parent.Close()
	return unix.RenameatxNp(int(parent.Fd()), filepath.Base(install), int(parent.Fd()), filepath.Base(backup), unix.RENAME_SWAP)
}
