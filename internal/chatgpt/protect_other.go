//go:build !windows

// SPDX-License-Identifier: GPL-3.0-only
package chatgpt

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"

	"github.com/zalando/go-keyring"
)

type systemProtector struct{}

func NewSystemProtector() Protector { return systemProtector{} }

// macOS: Keychain; Linux: Secret Service. No file-based master-key fallback.
func secureAEAD(create bool) (cipher.AEAD, error) {
	encoded, err := keyring.Get("io.github.bronya0.ally.chatgpt", "vault-key-v1")
	if errors.Is(err, keyring.ErrNotFound) && create {
		key := make([]byte, 32)
		if _, err = rand.Read(key); err != nil {
			return nil, err
		}
		encoded = base64.RawStdEncoding.EncodeToString(key)
		err = keyring.Set("io.github.bronya0.ally.chatgpt", "vault-key-v1", encoded)
	}
	if err != nil {
		return nil, err
	}
	key, err := base64.RawStdEncoding.DecodeString(encoded)
	if err != nil || len(key) != 32 {
		return nil, errors.New("invalid secure storage key")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
func (systemProtector) Seal(data []byte) ([]byte, error) {
	aead, err := secureAEAD(true)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return nil, err
	}
	return aead.Seal(nonce, nonce, data, []byte("Ally ChatGPT vault v1")), nil
}
func (systemProtector) Open(data []byte) ([]byte, error) {
	aead, err := secureAEAD(false)
	if err != nil {
		return nil, err
	}
	if len(data) < aead.NonceSize() {
		return nil, errors.New("invalid secure record")
	}
	return aead.Open(nil, data[:aead.NonceSize()], data[aead.NonceSize():], []byte("Ally ChatGPT vault v1"))
}
