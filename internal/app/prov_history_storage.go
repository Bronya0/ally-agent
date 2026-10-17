// SPDX-License-Identifier: GPL-3.0-only
package app

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"

	"ally-dev/internal/chatgpt"
	openai "github.com/sashabaranov/go-openai"
)

// The optional protected payload lives INSIDE the atomically replaced history
// file. Older versions ignore its extra JSON field and retain the existing
// text-only history profile. No sidecar lifecycle or second file commit exists.
// The system protector uses DPAPI / the OS keychain, with no plaintext fallback.
type modelHistoryCodec struct {
	mu        sync.Mutex
	protector chatgpt.Protector
}

func newModelHistoryCodec() *modelHistoryCodec {
	return &modelHistoryCodec{protector: chatgpt.NewSystemProtector()}
}

const maxProtectedModelStateBytes = 16 * 1024 * 1024
const protectedModelStateField = "ally_model_state_v1"

type protectedModelState struct {
	Version   int                                     `json:"version"`
	Session   string                                  `json:"session"`
	DiskHash  string                                  `json:"disk_hash"`
	History   []openai.ChatCompletionMessage          `json:"history"`
	Responses map[string]map[string][]json.RawMessage `json:"responses"`
}

func modelHistoryHash(messages []openai.ChatCompletionMessage) string {
	raw, _ := json.Marshal(messages)
	return fmt.Sprintf("%x", sha256.Sum256(raw))
}

func (a *App) modelHistoryForStorage(sessionID string, disk, history []openai.ChatCompletionMessage) any {
	if len(disk) == 0 || a.modelHistoryCodec == nil || a.reasoningStash == nil {
		return disk
	}
	original := history
	history, origins := sanitizeHistoryMessagesWithOrigins(original, historyProfileProtected)
	prefix := reasoningStashScope(openAIResponsesPromptCacheKey(sessionID)) + "\x1f"
	// Rebase untouched turns from live-history ordinals to stored ordinals.
	// Replaying a rewritten turn would resurrect calls removed by hygiene.
	sourceOrdinals := make([]int, len(original))
	ordinal := 0
	for i, m := range original {
		sourceOrdinals[i] = ordinal
		if m.Role == openai.ChatMessageRoleAssistant {
			ordinal++
		}
	}
	valid := map[string]string{}
	ordinal = 0
	for i, m := range history {
		if m.Role != openai.ChatMessageRoleAssistant {
			continue
		}
		sourceIndex := origins[i]
		if responsesMessageKey(0, original[sourceIndex]) == responsesMessageKey(0, m) {
			valid[responsesMessageKey(sourceOrdinals[sourceIndex], original[sourceIndex])] = responsesMessageKey(ordinal, m)
		}
		ordinal++
	}
	scopes := make(map[string]map[string][]json.RawMessage)
	a.reasoningStash.mu.Lock()
	for key, entry := range a.reasoningStash.entries {
		if !strings.HasPrefix(key, prefix) || entry.payload == nil {
			continue
		}
		for messageKey, output := range entry.payload.responsesNative {
			storedKey, ok := valid[messageKey]
			if !ok {
				continue
			}
			if scopes[key] == nil {
				scopes[key] = make(map[string][]json.RawMessage)
			}
			scopes[key][storedKey] = output
		}
	}
	a.reasoningStash.mu.Unlock()
	if len(scopes) == 0 {
		return disk
	}
	state := protectedModelState{Version: 1, Session: sessionID, DiskHash: modelHistoryHash(disk), History: history, Responses: scopes}
	raw, err := json.Marshal(state)
	if err != nil {
		log.Print("model replay persistence unavailable: state serialization failed")
		return disk
	}
	if len(raw) > maxProtectedModelStateBytes {
		log.Printf("model replay persistence unavailable: state exceeds size limit (%d > %d bytes)", len(raw), maxProtectedModelStateBytes)
		return disk
	}
	a.modelHistoryCodec.mu.Lock()
	sealed, err := a.modelHistoryCodec.protector.Seal(raw)
	a.modelHistoryCodec.mu.Unlock()
	if err != nil {
		log.Print("model replay persistence unavailable: secure storage failed")
		return disk
	}
	raw, _ = json.Marshal(disk)
	var rows []map[string]json.RawMessage
	if json.Unmarshal(raw, &rows) != nil {
		return disk
	}
	rows[0][protectedModelStateField], _ = json.Marshal(sealed)
	return rows
}

// Decrypt before taking App.mu; publishing the returned closure only after the
// session load ticket/revision check prevents a cancelled load from restoring
// stale replay state into a released or rewound session.
func (a *App) modelHistoryFromStorage(sessionID string, raw []byte, disk []openai.ChatCompletionMessage) ([]openai.ChatCompletionMessage, func()) {
	if a.modelHistoryCodec == nil || a.reasoningStash == nil || !bytes.Contains(raw, []byte(`"`+protectedModelStateField+`"`)) {
		return disk, nil
	}
	var rows []map[string]json.RawMessage
	if json.Unmarshal(raw, &rows) != nil || len(rows) == 0 {
		return disk, nil
	}
	var sealed []byte
	if json.Unmarshal(rows[0][protectedModelStateField], &sealed) != nil || len(sealed) > maxProtectedModelStateBytes+1024 {
		return disk, nil
	}
	a.modelHistoryCodec.mu.Lock()
	plain, err := a.modelHistoryCodec.protector.Open(sealed)
	a.modelHistoryCodec.mu.Unlock()
	if err != nil || len(plain) > maxProtectedModelStateBytes {
		return disk, nil
	}
	var state protectedModelState
	if json.Unmarshal(plain, &state) != nil || state.Version != 1 || state.Session != sessionID || state.DiskHash != modelHistoryHash(disk) {
		return disk, nil
	}
	history := sanitizeHistoryMessages(state.History)
	// The protected full model history must be the very history represented by
	// this disk profile; no restored payload can substitute another session.
	if modelHistoryHash(sanitizeHistoryMessagesForDisk(history)) != state.DiskHash {
		return disk, nil
	}
	prefix := reasoningStashScope(openAIResponsesPromptCacheKey(sessionID)) + "\x1f"
	return history, func() {
		for scope, turns := range state.Responses {
			if !strings.HasPrefix(scope, prefix) {
				continue
			}
			for key, output := range turns {
				if responsesOutputReplayable(output) {
					a.reasoningStash.appendResponses(scope, key, output)
				}
			}
		}
	}
}
