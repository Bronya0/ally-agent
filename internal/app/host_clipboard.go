// SPDX-License-Identifier: GPL-3.0-only
package app

import (
	"errors"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"
)

// An unavailable file representation is a legitimate "nothing to paste".
// Other errors (such as a busy clipboard) remain visible to the caller.
func clipboardFiles() ([]string, error) {
	paths, err := mygo.Clipboard.ReadFiles()
	if errors.Is(err, transfer.ErrFormat) {
		return nil, nil
	}
	return paths, err
}
