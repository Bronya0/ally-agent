// SPDX-License-Identifier: GPL-3.0-only
package app

import "github.com/zalando/go-keyring"

func init() {
	keyring.MockInit()
	_ = keyring.Set("io.github.bronya0.ally.chatgpt", "vault-key-v1", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
}
