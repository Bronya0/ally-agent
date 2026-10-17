// SPDX-License-Identifier: GPL-3.0-only
package chatgpt

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/gofrs/flock"
)

// Protector never falls back to plaintext. Tests supply an in-memory key.
type Protector interface {
	Seal([]byte) ([]byte, error)
	Open([]byte) ([]byte, error)
}

type vault struct {
	HostID   string       `json:"host_id"`
	Accounts []credential `json:"accounts"`
}

// withVault reloads under an OS file lock, including across Ally processes.
// Refresh-token rotation and all credential replacements share this transaction.
func (m *Manager) withVault(ctx context.Context, fn func(*vault) (bool, error)) error {
	if err := os.MkdirAll(m.dir, 0700); err != nil {
		return errors.New("ChatGPT credential directory unavailable")
	}
	l := flock.New(filepath.Join(m.dir, "credentials.lock"))
	ok, err := l.TryLockContext(ctx, 25*time.Millisecond)
	if err != nil || !ok {
		return errors.New("ChatGPT credential lock unavailable")
	}
	defer l.Unlock()
	v := vault{Accounts: []credential{}}
	path := filepath.Join(m.dir, "credentials.enc")
	data, err := os.ReadFile(path)
	if err == nil {
		plain, err := m.protector.Open(data)
		if err != nil {
			return errors.New("ChatGPT secure storage could not be unlocked")
		}
		if json.Unmarshal(plain, &v) != nil {
			return errors.New("ChatGPT credential record is invalid")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return errors.New("ChatGPT credentials could not be read")
	}
	write, err := fn(&v)
	if !write {
		return err
	}
	plain, marshalErr := json.Marshal(v)
	if marshalErr != nil {
		return marshalErr
	}
	sealed, sealErr := m.protector.Seal(plain)
	if sealErr != nil {
		return errors.New("ChatGPT secure storage unavailable; credentials were not saved")
	}
	f, fileErr := os.CreateTemp(m.dir, ".credentials-*")
	if fileErr != nil {
		return errors.New("ChatGPT credentials could not be saved")
	}
	defer os.Remove(f.Name())
	if fileErr = f.Chmod(0600); fileErr == nil {
		_, fileErr = f.Write(sealed)
	}
	if fileErr == nil {
		fileErr = f.Sync()
	}
	closeErr := f.Close()
	if fileErr == nil {
		fileErr = closeErr
	}
	if fileErr == nil {
		fileErr = os.Rename(f.Name(), path)
	}
	if fileErr != nil {
		return errors.New("ChatGPT credentials could not be saved atomically")
	}
	return err
}
