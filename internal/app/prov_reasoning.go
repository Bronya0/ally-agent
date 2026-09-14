// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.

package app

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	oa "github.com/openai/openai-go/v3"
	oaresp "github.com/openai/openai-go/v3/responses"
	legacyopenai "github.com/sashabaranov/go-openai"
)

// ── Reasoning replay state (provider-protocol layer) ────────────────────────
//
// Thinking-mode providers require the assistant reasoning produced in the
// current tool loop to be replayed on the next request within that loop:
//
//   - DeepSeek V4 / Kimi K3 / kimi-k2.7-code / GLM thinking (OpenAI Chat):
//     a missing reasoning_content on a tool-call assistant message is a hard
//     400 ("The reasoning_content in the thinking mode must be passed back").
//     Empty-string reasoning must keep the FIELD present (the wire struct's
//     omitempty would drop it).
//   - Anthropic Messages: thinking blocks with their signature must be passed
//     back verbatim inside a tool turn; outside tool turns they may be omitted.
//   - OpenAI Responses: reasoning items (with encrypted_content under
//     store=false) must be replayed around function calls for quality.
//
// The reasoning TEXT travels on the internal history messages
// (ChatCompletionMessage.ReasoningContent) so it persists with the session.
// Signature / encrypted-content payloads are provider-protocol artifacts that
// only make sense inside the current tool loop (they cannot survive a
// provider switch or a restart), so they live here in a per-session stash
// keyed by the prompt-cache session key, never in the persisted history.

const (
	// maxReasoningStashEntries bounds the stash: one live entry per session
	// plus recently finished ones. Sessions are far fewer than this in
	// practice; the cap only protects a pathological process.
	maxReasoningStashEntries = 64
)

// sessionReasoningPayload holds the provider-specific replay payloads captured
// from the latest model response of a session's current tool loop.
type sessionReasoningPayload struct {
	// anthropic carries the thinking blocks (text + signature) and redacted
	// thinking blocks captured from the last Anthropic response, in order.
	anthropic []anthropicThinkingBlock
	// responses carries the encrypted reasoning items captured from the last
	// OpenAI Responses output, in order, for replay around function calls.
	responses []responsesReasoningItem
}

// anthropicThinkingBlock is one thinking/redacted_thinking block of an
// assistant message. redacted keeps only the encrypted Data (Thinking is
// empty); plain keeps Thinking + Signature.
type anthropicThinkingBlock struct {
	Thinking  string
	Signature string
	Data      string // redacted_thinking payload, non-empty for redacted blocks
}

func (b anthropicThinkingBlock) redacted() bool { return b.Data != "" }

// responsesReasoningItem is one reasoning item of a Responses output: the
// item ID plus (when present) its encrypted content for stateless replay.
type responsesReasoningItem struct {
	ID              string
	EncryptedContent string
	SummaryText      string
}

type reasoningStash struct {
	mu      sync.Mutex
	entries map[string]*sessionReasoningPayload
}

func newReasoningStash() *reasoningStash {
	return &reasoningStash{entries: map[string]*sessionReasoningPayload{}}
}

// key normalizes the session key. Empty keys share one bucket so callers that
// cannot provide a session id still replay within their loop.
func reasoningStashKey(sessionKey string) string {
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" {
		return "__default__"
	}
	return sessionKey
}

func (s *reasoningStash) get(sessionKey string) *sessionReasoningPayload {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.entries[reasoningStashKey(sessionKey)]
}

// set stores the payload and evicts the oldest entries beyond the cap. Never
// stores a nil-equivalent payload so get() can distinguish "no thinking yet".
func (s *reasoningStash) set(sessionKey string, payload *sessionReasoningPayload) {
	if s == nil || payload == nil {
		return
	}
	if len(payload.anthropic) == 0 && len(payload.responses) == 0 {
		return
	}
	key := reasoningStashKey(sessionKey)
	s.mu.Lock()
	defer s.mu.Unlock()
	// Cheap bound: drop every other entry when over cap. Sessions that need
	// their payload again will re-capture it on their next response.
	if len(s.entries) >= maxReasoningStashEntries {
		for k := range s.entries {
			if k != key {
				delete(s.entries, k)
			}
		}
	}
	s.entries[key] = payload
}

func (s *reasoningStash) clear(sessionKey string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.entries, reasoningStashKey(sessionKey))
}

// responsesReasoningInputItem converts a captured reasoning item into a
// request input item. The summary is required by the API even when the
// provider did not send one (stateless replay after store=false), so an
// empty summary degrades to a placeholder rather than being dropped.
func responsesReasoningInputItem(item responsesReasoningItem) oaresp.ResponseInputItemUnionParam {
	summary := []oaresp.ResponseReasoningItemSummaryParam{}
	if item.SummaryText != "" {
		summary = append(summary, oaresp.ResponseReasoningItemSummaryParam{Text: item.SummaryText})
	}
	input := oaresp.ResponseInputItemParamOfReasoning(item.ID, summary)
	if input.OfReasoning != nil && item.EncryptedContent != "" {
		input.OfReasoning.EncryptedContent = oa.String(item.EncryptedContent)
	}
	return input
}

// withResponsesReasoningItems injects the captured reasoning items into the
// request input right before the trailing function-call sequence. OpenAI
// expects reasoning items to precede the function_call items they belong
// to; appending them immediately before the trailing run of function-call /
// function-call-output items preserves that order while leaving the stable
// prefix untouched (prompt cache survives). Idempotent: items already
// present (a retried request) are not duplicated.
func withResponsesReasoningItems(messages []legacyopenai.ChatCompletionMessage, items []responsesReasoningItem) []legacyopenai.ChatCompletionMessage {
	if len(items) == 0 {
		return messages
	}
	// Locate the start of the trailing tool-turn block: walk back over tool
	// messages and the assistant message carrying tool calls.
	cut := len(messages)
	for cut > 0 {
		m := messages[cut-1]
		if m.Role == legacyopenai.ChatMessageRoleTool {
			cut--
			continue
		}
		if m.Role == legacyopenai.ChatMessageRoleAssistant && len(m.ToolCalls) > 0 {
			cut--
			continue
		}
		break
	}
	// Rebuild with the reasoning items converted into a synthetic carrier.
	// The internal message type cannot hold Responses reasoning items, so
	// they ride as a dedicated marker message the request builder expands.
	out := make([]legacyopenai.ChatCompletionMessage, 0, len(messages)+len(items))
	out = append(out, messages[:cut]...)
	for _, item := range items {
		out = append(out, legacyopenai.ChatCompletionMessage{
			Role:    roleResponsesReasoningCarrier,
			Content: item.ID + "\x00" + item.EncryptedContent + "\x00" + item.SummaryText,
		})
	}
	out = append(out, messages[cut:]...)
	return out
}

// roleResponsesReasoningCarrier marks an internal-history message that only
// exists to carry captured Responses reasoning items from the stash to the
// request builder. It is never persisted: buildOpenAIResponsesInput expands
// it into real reasoning input items, and every other consumer must skip it
// (sanitizers drop unknown roles).
const roleResponsesReasoningCarrier = "ally-responses-reasoning"

// responsesReasoningCarriers returns the reasoning items carried by the
// synthetic carrier messages of a request message list.
func responsesReasoningCarriers(messages []legacyopenai.ChatCompletionMessage) []responsesReasoningItem {
	items := []responsesReasoningItem{}
	for _, m := range messages {
		if m.Role != roleResponsesReasoningCarrier {
			continue
		}
		parts := strings.SplitN(m.Content, "\x00", 3)
		if len(parts) != 3 {
			continue
		}
		items = append(items, responsesReasoningItem{ID: parts[0], EncryptedContent: parts[1], SummaryText: parts[2]})
	}
	return items
}

// ── OpenAI Chat reasoning_content replay (empty-string preservation) ────────

// knownReasoningWireKeys lists the wire field names used for reasoning across
// OpenAI-compatible providers (aligning with Kimi-code / vLLM conventions):
//   - "reasoning_content": DeepSeek, Moonshot Kimi, legacy vLLM, OneAPI
//   - "reasoning_details": OpenRouter
//   - "reasoning": OpenAI GPT-OSS, current vLLM (vllm-project/vllm#27752)
var knownReasoningWireKeys = []string{
	"reasoning_content",
	"reasoning_details",
	"reasoning",
}

func isKnownWireReasoningKey(tag string) bool {
	tag = strings.TrimSpace(tag)
	for _, k := range knownReasoningWireKeys {
		if strings.EqualFold(tag, k) {
			return true
		}
	}
	return false
}

// reasoningContentReplayActive reports whether replay mode is on for this
// request: the user explicitly picked a thinking-effort level (thinking mode
// is definitely enabled), or the active turn's assistant history carried
// reasoning from the model (the provider is in thinking mode even under
// "auto").
//
// Scoped to avoid contamination: if the user switched from a reasoning model
// to a non-thinking model mid-session, old reasoning from earlier turns does
// not falsely activate replay on the new model's requests.
func reasoningContentReplayActive(messages []legacyopenai.ChatCompletionMessage, cfg ConfigState) bool {
	if reasoningEffortForAdapter(apiFormatOpenAIChat, cfg.ReasoningEffort) != "" {
		return true
	}
	// Look back through the current turn (since the latest user message).
	// In a tool loop, there will be assistant message(s) between the latest
	// user message and the end of the history.
	hasAssistantInCurrentTurn := false
	for i := len(messages) - 1; i >= 0; i-- {
		m := messages[i]
		if m.Role == legacyopenai.ChatMessageRoleUser {
			break
		}
		if m.Role == legacyopenai.ChatMessageRoleAssistant {
			hasAssistantInCurrentTurn = true
			if m.ReasoningContent != "" {
				return true
			}
		}
	}
	// If an assistant has already spoken in the current turn (e.g. issued a tool call)
	// and produced no reasoning, then the current turn's model is NOT in thinking mode.
	if hasAssistantInCurrentTurn {
		return false
	}
	// If no assistant has spoken in the current turn yet (start of turn), check
	// the immediately preceding assistant response from the prior turn.
	for i := len(messages) - 1; i >= 0; i-- {
		m := messages[i]
		if m.Role == legacyopenai.ChatMessageRoleAssistant {
			return m.ReasoningContent != ""
		}
	}
	return false
}

// reasoningContentReplayTransport rewrites the serialized Chat Completions
// request body so tool-call assistant messages carry an explicit reasoning field
// — including the empty string. go-openai's wire struct tags the field
// omitempty, so an assistant tool-call message whose reasoning was empty (a
// real DeepSeek V4 behavior on obvious tool calls) would drop the field and
// fail the thinking-mode validation with 400. The transport only rewrites
// bodies when replay mode is active; otherwise it passes through untouched.
type reasoningContentReplayTransport struct {
	base         http.RoundTripper
	reasoningKey string
}

func (t *reasoningContentReplayTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req == nil || req.Body == nil || req.Method != http.MethodPost {
		return t.base.RoundTrip(req)
	}
	body, err := io.ReadAll(req.Body)
	_ = req.Body.Close()
	if err != nil {
		return nil, err
	}
	key := t.reasoningKey
	if patched, ok := patchReasoningContentFields(body, key); ok {
		// Restore a replayable body: the SDK may retry the request object,
		// so GetBody must yield the patched bytes every time — otherwise
		// net/http fails retries with "ContentLength=X with Body length 0".
		req = req.Clone(req.Context())
		req.Body = io.NopCloser(bytes.NewReader(patched))
		req.GetBody = func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(patched)), nil
		}
		req.ContentLength = int64(len(patched))
	} else {
		// Even untouched, restore the body: the caller's original GetBody
		// (if any) still describes these bytes, but a plain re-readable
		// reader is the safest universal restoration.
		req = req.Clone(req.Context())
		req.Body = io.NopCloser(bytes.NewReader(body))
		req.GetBody = func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(body)), nil
		}
	}
	return t.base.RoundTrip(req)
}

// patchReasoningContentFields adds an empty reasoning field (defaultKey or
// the detected wire dialect) to the trailing tool-turn assistant message
// that contains tool_calls but lacks a reasoning field. Returns the new body
// and whether it changed.
//
// Key contracts (aligning with Kimi-code and KV-cache / Prompt Cache stability):
// 1. KV-cache / Prompt Cache preservation: ONLY the active trailing tool-turn
//    assistant message is ever patched. All earlier historical messages in
//    the request prefix remain strictly frozen and byte-stable so the
//    upstream KV-cache / prompt-cache prefix hash never drifts.
// 2. Only assistant messages that carry tool_calls require the empty field
//    (DeepSeek V4 / Moonshot Kimi 400 requirement). Plain assistant text
//    responses without tool calls must never be backfilled.
// 3. If the assistant message already carries ANY known reasoning key
//    ("reasoning_content", "reasoning_details", "reasoning"), it is left
//    untouched.
// 4. Dialect detection: echoes the dialect the messages actually carry
//    ("reply in the dialect the peer spoke"), falling back to defaultKey.
func patchReasoningContentFields(body []byte, defaultKey string) ([]byte, bool) {
	if defaultKey == "" {
		defaultKey = defaultReasoningTag
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(body, &payload); err != nil {
		return body, false
	}
	rawMessages, ok := payload["messages"]
	if !ok {
		return body, false
	}
	var messages []map[string]json.RawMessage
	if err := json.Unmarshal(rawMessages, &messages); err != nil {
		return body, false
	}

	targetKey := defaultKey
	// Detect if history already spoke a specific dialect.
findDialect:
	for _, m := range messages {
		for _, k := range knownReasoningWireKeys {
			if _, exists := m[k]; exists {
				targetKey = k
				break findDialect
			}
		}
	}

	// Locate the active trailing tool-turn assistant message by walking back
	// past any trailing tool result messages. Only this message belongs to the
	// in-flight tool turn; earlier messages belong to frozen history and must
	// never be mutated so the KV cache prefix stays bitwise-stable.
	targetIdx := -1
	for i := len(messages) - 1; i >= 0; i-- {
		role, _ := strconv.Unquote(string(messages[i]["role"]))
		if role == legacyopenai.ChatMessageRoleTool {
			continue
		}
		if role == legacyopenai.ChatMessageRoleAssistant {
			toolCallsRaw, hasToolCalls := messages[i]["tool_calls"]
			if hasToolCalls && len(toolCallsRaw) > 0 && string(toolCallsRaw) != "null" && string(toolCallsRaw) != "[]" {
				targetIdx = i
			}
		}
		break
	}
	if targetIdx < 0 {
		return body, false
	}

	targetMsg := messages[targetIdx]
	for _, k := range knownReasoningWireKeys {
		if _, exists := targetMsg[k]; exists {
			return body, false
		}
	}

	targetMsg[targetKey] = json.RawMessage(`""`)
	newMessages, err := json.Marshal(messages)
	if err != nil {
		return body, false
	}
	payload["messages"] = newMessages
	out, err := json.Marshal(payload)
	if err != nil {
		return body, false
	}
	return out, true
}

func isAnthropicClaudeModel(model string) bool {
	m := strings.ToLower(strings.TrimSpace(model))
	return strings.Contains(m, "claude")
}

// withAnthropicThinkingBlocks prepends the captured thinking blocks to the
// LAST assistant message of the request. Anthropic requires thinking blocks
// to be replayed unmodified, in order, alongside the tool_use blocks of the
// same assistant turn; buildAnthropicMessages emits tool_use blocks on the
// last assistant message, so the thinking blocks belong exactly there. If no
// assistant message exists the payload cannot be replayed meaningfully and
// is dropped (the API allows omitting thinking only when the request has no
// in-flight tool loop, which is exactly that case).
func withAnthropicThinkingBlocks(messages []anthropic.MessageParam, blocks []anthropicThinkingBlock, model string) []anthropic.MessageParam {
	if len(blocks) == 0 || len(messages) == 0 {
		return messages
	}
	last := -1
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == anthropic.MessageParamRoleAssistant {
			last = i
			break
		}
	}
	if last < 0 {
		return messages
	}
	// Idempotence: if the last assistant message already starts with the
	// replayed thinking blocks (a retried request path), do not stack them.
	if len(messages[last].Content) >= len(blocks) {
		already := true
		for i, b := range blocks {
			union := messages[last].Content[i]
			if b.redacted() {
				if union.OfRedactedThinking == nil || union.OfRedactedThinking.Data != b.Data {
					already = false
					break
				}
			} else if union.OfThinking == nil || union.OfThinking.Thinking != b.Thinking || union.OfThinking.Signature != b.Signature {
				already = false
				break
			}
		}
		if already {
			return messages
		}
	}
	isClaude := isAnthropicClaudeModel(model)
	prefix := make([]anthropic.ContentBlockParamUnion, 0, len(blocks)+len(messages[last].Content))
	for _, b := range blocks {
		if b.redacted() {
			prefix = append(prefix, anthropic.NewRedactedThinkingBlock(b.Data))
		} else {
			// If model is Claude and signature is empty, Anthropic official API strictly
			// rejects the request with HTTP 400 ("messages.N.content.0.thinking.signature: Field required").
			// Unsigned thinking is only permitted for non-Claude Anthropic-compatible proxies.
			if isClaude && strings.TrimSpace(b.Signature) == "" {
				continue
			}
			prefix = append(prefix, anthropic.NewThinkingBlock(b.Signature, b.Thinking))
		}
	}
	if len(prefix) == 0 {
		return messages
	}
	messages[last].Content = append(prefix, messages[last].Content...)
	return messages
}
