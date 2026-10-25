// SPDX-License-Identifier: GPL-3.0-only
package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/gofrs/flock"
)

func (a *App) beginUpdateOperation(operation string) error {
	a.updateMu.Lock()
	defer a.updateMu.Unlock()
	if a.updateOperation != "" {
		return fmt.Errorf("update already %s; close and reopen Ally after applying an update", a.updateOperation)
	}
	a.updateOperation = operation
	a.updateCancelRequested = false
	return nil
}
func (a *App) endUpdateOperation() {
	a.updateMu.Lock()
	defer a.updateMu.Unlock()
	if a.updateOperation != "applied" {
		a.updateOperation = ""
	}
	a.updateCancel = nil
}
func (a *App) updateBlocksRuns() bool {
	a.updateMu.Lock()
	defer a.updateMu.Unlock()
	return a.updateOperation == "applying" || a.updateOperation == "applied"
}
func lockUpdateDownloads() (*flock.Flock, error) {
	if err := os.MkdirAll(updateBaseDir(), 0o700); err != nil {
		return nil, err
	}
	lock := flock.New(filepath.Join(updateBaseDir(), ".lock"))
	ok, err := lock.TryLock()
	if err != nil {
		lock.Close()
		return nil, err
	}
	if !ok {
		lock.Close()
		return nil, errors.New("another Ally process is updating")
	}
	return lock, nil
}

// Called by the initialized frontend, rather than early service startup.
func (a *App) ConfirmUpdateStartup() {
	if a.ctx != nil {
		scheduleUpdateBackupCleanup(a.ctx)
	}
}
