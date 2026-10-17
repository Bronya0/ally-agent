// SPDX-License-Identifier: GPL-3.0-only
package chatgpt

import (
	"crypto/aes"
	"crypto/cipher"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSecureAEADCacheSharesConcurrentUnlock(t *testing.T) {
	block, _ := aes.NewCipher(make([]byte, 32))
	aead, _ := cipher.NewGCM(block)
	var cache secureAEADCache
	var calls atomic.Int32
	load := func(bool) (cipher.AEAD, error) { calls.Add(1); return aead, nil }
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(create bool) {
			defer wg.Done()
			got, err := cache.get(create, load)
			if err != nil || got != aead {
				t.Error("cached key unavailable", err)
			}
		}(i%2 == 0)
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("keychain lookups=%d", calls.Load())
	}
}

func TestSecureAEADCacheRetriesFailureAndAllowsCreation(t *testing.T) {
	var cache secureAEADCache
	failure := errors.New("keychain locked")
	calls := 0
	load := func(bool) (cipher.AEAD, error) { calls++; return nil, failure }
	for i := 0; i < 3; i++ {
		if _, err := cache.get(false, load); !errors.Is(err, failure) {
			t.Fatal("lookup error lost")
		}
	}
	if calls != 1 {
		t.Fatal("failed lookup was not throttled")
	}
	cache.get(true, load)
	if calls != 2 {
		t.Fatal("Open failure prevented key creation")
	}
	cache.retryAfter = time.Now().Add(-time.Second)
	cache.get(true, load)
	if calls != 3 {
		t.Fatal("temporary failure was cached permanently")
	}
}
