// SPDX-License-Identifier: GPL-3.0-only
package chatgpt

import (
	"crypto/cipher"
	"sync"
	"time"
)

// Keep successful keychain unlocks in memory for this process. Failed lookups
// have a short cooldown, not a permanent sync.Once failure. An Open failure
// must not prevent a subsequent Seal from creating a missing key.
type secureAEADCache struct {
	mu         sync.Mutex
	aead       cipher.AEAD
	err        error
	failedMode bool
	retryAfter time.Time
}

func (c *secureAEADCache) get(create bool, load func(bool) (cipher.AEAD, error)) (cipher.AEAD, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.aead != nil {
		return c.aead, nil
	}
	if c.err != nil && c.failedMode == create && time.Now().Before(c.retryAfter) {
		return nil, c.err
	}
	aead, err := load(create)
	if err != nil {
		c.err, c.failedMode, c.retryAfter = err, create, time.Now().Add(30*time.Second)
		return nil, err
	}
	c.aead, c.err = aead, nil
	return aead, nil
}
