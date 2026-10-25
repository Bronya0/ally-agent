// SPDX-License-Identifier: GPL-3.0-only
package app

import (
	"strings"
	"testing"
)

func TestUpdateBlocksAPIChat(t *testing.T) {
	for _, operation := range []string{"applying", "applied"} {
		t.Run(operation, func(t *testing.T) {
			a := &App{updateOperation: operation}
			// The HTTP API uses this shared entry directly, without StartChat.
			if _, err := a.startChatWithConfig(ChatRequest{}, ConfigState{}); err == nil || !strings.Contains(err.Error(), "update is being installed") {
				t.Fatalf("expected update guard before task registration, got %v", err)
			}
		})
	}
}
