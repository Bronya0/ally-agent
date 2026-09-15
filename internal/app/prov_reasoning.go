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
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"ally-dev/internal/tools/toolcall"

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
	// maxReasoningStashEntries bounds the stash with LRU eviction. Keys are
	// scoped per session x wire protocol x model, plus one key per subagent run,
	// so a bounded map is what keeps long-lived processes from growing without
	// limit; the eviction never drops more than it has to (see evictLocked).
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
	// responsesToolItems maps a function-call id to the Responses item id
	// (fc_…) the model emitted for it. OpenAI pairs a replayed reasoning item
	// with the item that FOLLOWS it by id: sending the reasoning item's id
	// back without the follower's id is rejected ("Item 'rs_…' of type
	// 'reasoning' was provided without its required following item"), which is
	// why pi keeps the item id for the same model
	// (openai-responses-shared.ts:247-292 using the stored `call_id|item_id`).
	responsesToolItems map[string]string
	// chatDetails carries OpenRouter-style reasoning_details (array form)
	// captured from the last Chat Completions response. Sending the details
	// back is what preserves reasoning for providers that require the signed
	// trace (Anthropic through OpenRouter); `reasoning_content` alone is not
	// enough there.
	chatDetails json.RawMessage
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

// responsesReasoningItem mirrors one reasoning item of a Responses output. Every
// documented field is preserved so the item can be replayed as a copy of the one
// the API produced: OpenAI resolves a replayed reasoning item by id and expects
// the item's own content back, which is why pi stores the whole item as JSON and
// sends it verbatim (openai-responses-shared.ts:222/689-690). Ally keeps the
// typed SDK fields rather than raw JSON so a provider-side schema change still
// surfaces at compile time.
type responsesReasoningItem struct {
	ID               string
	EncryptedContent string
	SummaryTexts     []string
	ContentTexts     []string
	Status           string
}

// reasoningStashEntry pairs a payload with the logical clock reading of its last
// use, so the stash can evict the coldest entry instead of dropping arbitrary
// (possibly in-flight) ones.
type reasoningStashEntry struct {
	payload  *sessionReasoningPayload
	lastUsed uint64
}

type reasoningStash struct {
	mu      sync.Mutex
	clock   uint64
	entries map[string]*reasoningStashEntry
}

func newReasoningStash() *reasoningStash {
	return &reasoningStash{entries: map[string]*reasoningStashEntry{}}
}

// reasoningStashKey normalizes the session key. Empty keys share one bucket so
// callers that cannot provide a session id still replay within their loop.
func reasoningStashKey(sessionKey string) string {
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" {
		return "__default__"
	}
	return sessionKey
}

// reasoningReplayKey scopes a session's replay payload to the wire protocol and
// the model that actually issues the request. Signatures and encrypted reasoning
// are only valid for the exact model that emitted them: replaying them after the
// user switched models fails at the provider (Anthropic validates the thinking
// signature, OpenAI resolves the reasoning item id), and a rejected replay
// poisons every later request of the session. Model switches therefore start a
// fresh payload instead of reusing the previous one (pi: transform-messages.ts
// isSameModel). The separator is stripped from the session component so a
// caller-supplied session id cannot forge another session's key.
func reasoningReplayKey(cfg ConfigState, model string) string {
	if strings.TrimSpace(model) == "" {
		model = cfg.Model
	}
	return strings.Join([]string{
		strings.ReplaceAll(reasoningStashKey(cfg.responsesPromptCacheKey), "\x1f", "_"),
		normalizeAPIFormat(cfg.APIFormat),
		strings.ToLower(strings.TrimSpace(model)),
	}, "\x1f")
}

func (s *reasoningStash) get(key string) *sessionReasoningPayload {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.entries[key]
	if !ok {
		return nil
	}
	s.clock++
	entry.lastUsed = s.clock
	return entry.payload
}

// set stores the payload for one key, evicting the coldest entries when the map
// is full. Never stores a nil-equivalent payload so get() can distinguish
// "no thinking yet".
func (s *reasoningStash) set(key string, payload *sessionReasoningPayload) {
	if s == nil || payload == nil {
		return
	}
	if len(payload.anthropic) == 0 && len(payload.responses) == 0 &&
		len(payload.responsesToolItems) == 0 && len(payload.chatDetails) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.evictLocked(key)
	s.clock++
	s.entries[key] = &reasoningStashEntry{payload: payload, lastUsed: s.clock}
}

// evictLocked drops the coldest entry until the map has room for one more. It
// never drops the key being written and never clears the whole map: keys are
// scoped per session x protocol x model (plus one per subagent run), so the map
// normally holds many live entries and a wholesale wipe would silently strip an
// in-flight tool loop of the signatures its next request has to replay.
func (s *reasoningStash) evictLocked(keep string) {
	for len(s.entries) >= maxReasoningStashEntries {
		coldest := ""
		var coldestAt uint64
		for key, entry := range s.entries {
			if key == keep {
				continue
			}
			if coldest == "" || entry.lastUsed < coldestAt {
				coldest, coldestAt = key, entry.lastUsed
			}
		}
		if coldest == "" {
			return
		}
		delete(s.entries, coldest)
	}
}

func (s *reasoningStash) clear(key string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.entries, key)
}

// clearSession drops every payload of one session across protocols and models.
// Called when the session's history is discarded or rewritten (session release,
// rewind/truncate): the retained signatures and encrypted reasoning belong to
// turns that no longer exist, and replaying them later would attach them to an
// unrelated assistant message.
func (s *reasoningStash) clearSession(sessionKey string) {
	if s == nil {
		return
	}
	prefix := reasoningStashKey(sessionKey) + "\x1f"
	s.mu.Lock()
	defer s.mu.Unlock()
	for key := range s.entries {
		if strings.HasPrefix(key, prefix) {
			delete(s.entries, key)
		}
	}
}

// captureResponsesReasoningItem copies every documented field of a reasoning
// output item. The second return value is false for an item that cannot be
// replayed at all: the API resolves a replayed reasoning item by id, so an item
// without a usable id would be rejected outright. Skipping it is safe — the
// following function_call carries its own id, and only a reasoning item requires
// its follower, never the other way round.
func captureResponsesReasoningItem(item oaresp.ResponseReasoningItem) (responsesReasoningItem, bool) {
	captured := responsesReasoningItem{
		ID:               strings.TrimSpace(item.ID),
		EncryptedContent: item.EncryptedContent,
		Status:           strings.TrimSpace(string(item.Status)),
	}
	if !toolcall.IsSafeResponsesItemID(captured.ID) {
		return responsesReasoningItem{}, false
	}
	for _, part := range item.Summary {
		if part.Text != "" {
			captured.SummaryTexts = append(captured.SummaryTexts, part.Text)
		}
	}
	for _, part := range item.Content {
		if part.Text != "" {
			captured.ContentTexts = append(captured.ContentTexts, part.Text)
		}
	}
	return captured, true
}

// responsesReasoningInputItem converts a captured reasoning item into a request
// input item. The summary key itself is guaranteed by
// ensureResponsesReasoningSummaries: the SDK drops it when it is empty, and the
// API only accepts an exact copy of the item it produced.
func responsesReasoningInputItem(item responsesReasoningItem) oaresp.ResponseInputItemUnionParam {
	summary := make([]oaresp.ResponseReasoningItemSummaryParam, 0, len(item.SummaryTexts))
	for _, text := range item.SummaryTexts {
		if text != "" {
			summary = append(summary, oaresp.ResponseReasoningItemSummaryParam{Text: text})
		}
	}
	input := oaresp.ResponseInputItemParamOfReasoning(item.ID, summary)
	if input.OfReasoning == nil {
		return input
	}
	if item.EncryptedContent != "" {
		input.OfReasoning.EncryptedContent = oa.String(item.EncryptedContent)
	}
	for _, text := range item.ContentTexts {
		if text != "" {
			input.OfReasoning.Content = append(input.OfReasoning.Content, oaresp.ResponseReasoningItemContentParam{Text: text})
		}
	}
	if item.Status != "" {
		input.OfReasoning.Status = oaresp.ResponseReasoningItemStatus(item.Status)
	}
	return input
}

// trailingToolTurnStart returns the index of the assistant message that starts
// the trailing tool turn (tool calls, optionally followed by the tool results
// answering them), or -1 when the history does not end in one. Single source of
// truth for "may provider reasoning artifacts be replayed here?".
func trailingToolTurnStart(messages []legacyopenai.ChatCompletionMessage) int {
	cut := len(messages)
	for cut > 0 && messages[cut-1].Role == legacyopenai.ChatMessageRoleTool {
		cut--
	}
	if cut == 0 {
		return -1
	}
	turn := messages[cut-1]
	if turn.Role != legacyopenai.ChatMessageRoleAssistant || len(turn.ToolCalls) == 0 {
		return -1
	}
	return cut - 1
}

// withResponsesReasoningItems injects the captured reasoning items into the
// request input right before the trailing tool turn. OpenAI expects reasoning
// items to precede the function_call items they belong to; inserting them at the
// start of that block preserves the order while leaving the stable prefix
// untouched (prompt cache survives). Callers pass a fresh message list per
// request, so the carriers are never duplicated.
//
// The history must END in a tool turn. Without one there is no item to attach the
// reasoning to: appending it anyway leaves a dangling reasoning item at the end
// of the input (the first request of every new user turn), which the API rejects
// — "Item 'rs_...' of type 'reasoning' was provided without its required
// following item" — and which would otherwise glue a stale reasoning trace to the
// new question.
func withResponsesReasoningItems(messages []legacyopenai.ChatCompletionMessage, items []responsesReasoningItem) []legacyopenai.ChatCompletionMessage {
	if len(items) == 0 {
		return messages
	}
	cut := trailingToolTurnStart(messages)
	if cut < 0 {
		return messages
	}
	// Rebuild with the reasoning items converted into a synthetic carrier.
	// The internal message type cannot hold Responses reasoning items, so
	// they ride as a dedicated marker message the request builder expands.
	out := make([]legacyopenai.ChatCompletionMessage, 0, len(messages)+len(items))
	out = append(out, messages[:cut]...)
	for _, item := range items {
		carrier := encodeResponsesReasoningCarrier(item)
		if carrier == "" {
			continue
		}
		out = append(out, legacyopenai.ChatCompletionMessage{
			Role:    roleResponsesReasoningCarrier,
			Content: carrier,
		})
	}
	out = append(out, messages[cut:]...)
	return out
}

// trailingToolTurnItemIDsComplete reports whether every tool call of the trailing
// tool turn has a captured Responses item id. OpenAI pairs a replayed reasoning
// item with the id of the item that follows it: when a follower id is unknown (a
// foreign turn, a relay with unusual ids, a payload evicted meanwhile), the pair
// cannot be completed, and the caller skips the replay entirely instead of
// risking the documented "reasoning item was provided without its required
// following item" rejection.
func trailingToolTurnItemIDsComplete(messages []legacyopenai.ChatCompletionMessage, toolItemIDs map[string]string) bool {
	start := trailingToolTurnStart(messages)
	if start < 0 {
		return false
	}
	for _, call := range messages[start].ToolCalls {
		key := toolcall.ForResponsesCall(toolcall.Effective(call.ID, call.Function.Name))
		if strings.TrimSpace(toolItemIDs[key]) == "" {
			return false
		}
	}
	return true
}

// roleResponsesReasoningCarrier marks an internal-history message that only
// exists to carry captured Responses reasoning items from the stash to the
// request builder. It is never persisted: buildOpenAIResponsesInput expands
// it into real reasoning input items, and every other consumer must skip it
// (sanitizers drop unknown roles).
const roleResponsesReasoningCarrier = "ally-responses-reasoning"

// encodeResponsesReasoningCarrier serializes a captured reasoning item into the
// carrier message that transports it from the stash to the request builder. The
// internal history type cannot hold a Responses reasoning item, so it rides as
// JSON text; the carrier is request-only and never persisted (see
// roleResponsesReasoningCarrier), and JSON keeps every field of the item in one
// place instead of a positional encoding that has to be extended by hand.
func encodeResponsesReasoningCarrier(item responsesReasoningItem) string {
	raw, err := json.Marshal(item)
	if err != nil || !toolcall.IsSafeResponsesItemID(strings.TrimSpace(item.ID)) {
		return ""
	}
	return string(raw)
}

// responsesReasoningCarriers returns the reasoning items carried by the
// synthetic carrier messages of a request message list.
func responsesReasoningCarriers(messages []legacyopenai.ChatCompletionMessage) []responsesReasoningItem {
	items := []responsesReasoningItem{}
	for _, m := range messages {
		if m.Role != roleResponsesReasoningCarrier || strings.TrimSpace(m.Content) == "" {
			continue
		}
		var item responsesReasoningItem
		if err := json.Unmarshal([]byte(m.Content), &item); err != nil || !toolcall.IsSafeResponsesItemID(strings.TrimSpace(item.ID)) {
			continue
		}
		items = append(items, item)
	}
	return items
}

// ── OpenAI Chat reasoning_content replay (empty-string preservation) ────────

// knownReasoningWireKeys lists the wire field names used for reasoning across
// OpenAI-compatible providers. It is the same set pi supports
// (openai-completions.ts:280 OPENAI_COMPLETIONS_REASONING_FIELDS):
//   - "reasoning_content": DeepSeek, Moonshot Kimi, legacy vLLM, OneAPI
//   - "reasoning_details": OpenRouter
//   - "reasoning": OpenAI GPT-OSS, current vLLM (vllm-project/vllm#27752)
//   - "reasoning_text": newer vLLM / llama.cpp-class servers
//
// Order matters twice: detectChatReasoningDialect returns the first key a
// message already carries, so the longest-standing conventions stay first; and
// a replay dialect configured to any of these names is written back verbatim.
var knownReasoningWireKeys = []string{
	"reasoning_content",
	"reasoning_details",
	"reasoning",
	"reasoning_text",
}

// rawParsedReasoningKeys are the keys this file has to parse out of the raw
// chunk bytes itself. It is derived from knownReasoningWireKeys so a new
// dialect only has to be declared once; go-openai already unmarshals
// "reasoning_content" into the typed delta field, so re-scanning for it here
// would only add a second JSON pass per streamed chunk.
var rawParsedReasoningKeys = func() []string {
	keys := make([]string, 0, len(knownReasoningWireKeys))
	for _, key := range knownReasoningWireKeys {
		if key != "reasoning_content" {
			keys = append(keys, key)
		}
	}
	return keys
}()

func isKnownWireReasoningKey(tag string) bool {
	tag = strings.TrimSpace(tag)
	for _, k := range knownReasoningWireKeys {
		if strings.EqualFold(tag, k) {
			return true
		}
	}
	return false
}

// jsonStringValue returns the JSON encoding of s for a request-body rewrite, so a
// rewritten field carries an explicit value instead of disappearing through an
// omitempty wire tag. Built with strconv.Quote so the encoding lives in one place
// with no raw literal involved.
func jsonStringValue(s string) json.RawMessage {
	return json.RawMessage(strconv.Quote(s))
}

var (
	// jsonEmptyString is the explicit empty string such a rewrite writes into a
	// text field that go-openai would otherwise drop.
	jsonEmptyString = jsonStringValue("")
	// jsonBoolFalse is the explicit false written for `store`.
	jsonBoolFalse = json.RawMessage("false")
	// jsonThinkingDisabled is the stop-thinking field written for the "off" level.
	// It is DeepSeek's documented spelling for the OpenAI wire — their sample passes
	// `{"thinking": {"type": "disabled"}}` through extra_body, because the OpenAI
	// Chat schema has no such field — and the compatible gateways follow the same
	// shape.
	jsonThinkingDisabled = json.RawMessage(`{"type":"disabled"}`)
)

// chatReasoningBackfillKey returns the wire field every assistant message of the
// request must carry, or "" when no placeholder may be written.
//
// The placeholder exists so a history restored from disk still satisfies the
// providers that validate the field per assistant message: DeepSeek documents
// that with the tools parameter present the reasoning_content "must be fully
// passed back to the API in all subsequent requests — even for turns where the
// model did not perform a tool call", while the disk profile deliberately
// stores no reasoning text (so a restored history has nothing to send).
//
// It is written for every non-OpenAI endpoint without asking which model is in
// use: the requirement belongs to the provider, and a model list would be wrong
// within weeks. The official OpenAI API is the one endpoint left out — its
// protocol never exposes reasoning over the Chat wire and its message-field
// validation is strict — which also keeps the first-party path byte-identical to
// what plain SDK clients send.
//
// The "off" level (关闭思考) is the other case that writes nothing: the request
// asks the provider to stop thinking, so there is no reasoning to hand back and
// the field must not be added (reasoningWireForAdapter owns how the level itself
// reaches the wire).
func chatReasoningBackfillKey(cfg ConfigState) string {
	if isOfficialOpenAIEndpoint(cfg) {
		return ""
	}
	if normalizeReasoningEffort(cfg.ReasoningEffort) == reasoningEffortOff {
		return ""
	}
	if isKnownWireReasoningKey(cfg.ReasoningTag) {
		return strings.TrimSpace(cfg.ReasoningTag)
	}
	return defaultReasoningTag
}

// The stream terminator is an SSE frame: the `data:` field whose payload is
// `[DONE]`. go-openai collapses it and "the connection closed at a frame
// boundary" into the same bare io.EOF (stream_reader.go:100-102 sets isFinished
// and returns io.EOF for `[DONE]`; :64-76 returns the raw read error otherwise),
// so an adapter cannot tell a finished stream from a truncated one by the
// returned error alone. The reader below records the real terminator, which lets
// the chat adapter require one of {finish_reason, [DONE]} instead of guessing
// (pi does the same by requiring a finish_reason, openai-completions.ts:685-693).
//
// The frame is matched the way go-openai's own reader matches it (`^data:\s*`
// before comparing the payload against "[DONE]", stream_reader.go:13-16 and
// :96-99), not by a single literal: `data: [DONE]`, `data:[DONE]` and
// `data:  [DONE]` all terminate the stream for the SDK, so a literal-only match
// would report a finished stream as truncated whenever a gateway sends one of
// the other spellings.
var (
	sseDoneField = []byte("data:")
	sseDoneToken = []byte("[DONE]")
)

// sseDoneCarryLen is how many trailing bytes of a read are kept for the next
// one so a terminator split across two reads is still recognized: the field name
// plus the whitespace a gateway may put between it and the payload.
const sseDoneCarryLen = 16

// containsSSEDone reports whether window holds a terminator frame.
func containsSSEDone(window []byte) bool {
	for {
		idx := bytes.Index(window, sseDoneField)
		if idx < 0 {
			return false
		}
		window = bytes.TrimLeft(window[idx+len(sseDoneField):], " \t")
		if bytes.HasPrefix(window, sseDoneToken) {
			return true
		}
	}
}

type sseDoneWatcher struct{ seen atomic.Bool }

func (w *sseDoneWatcher) mark() { w.seen.Store(true) }
func (w *sseDoneWatcher) Done() bool {
	if w == nil {
		return false
	}
	return w.seen.Load()
}

// reset clears the recorded terminator. The watcher is created once per logical
// stream and shared by every adapter-internal retry (the transport owns the
// pointer), so the terminator seen by a failed attempt must not make a later,
// truncated attempt look complete.
func (w *sseDoneWatcher) reset() {
	if w == nil {
		return
	}
	w.seen.Store(false)
}

// sseDoneReadCloser scans the response body for the terminator, carrying the
// last len(sseDoneMarker)-1 bytes across reads so a marker split between two
// Read calls is still matched.
type sseDoneReadCloser struct {
	rc      io.ReadCloser
	watcher *sseDoneWatcher
	carry   []byte
	window  []byte
}

func (r *sseDoneReadCloser) Read(p []byte) (int, error) {
	n, err := r.rc.Read(p)
	if n > 0 && !r.watcher.Done() {
		r.window = append(r.window[:0], r.carry...)
		r.window = append(r.window, p[:n]...)
		if containsSSEDone(r.window) {
			r.watcher.mark()
		}
		keep := sseDoneCarryLen
		if len(r.window) > keep {
			r.carry = append(r.carry[:0], r.window[len(r.window)-keep:]...)
		} else {
			r.carry = append(r.carry[:0], r.window...)
		}
	}
	return n, err
}

func (r *sseDoneReadCloser) Close() error { return r.rc.Close() }

// chatRequestRewriteTransport normalizes the serialized Chat Completions
// request body and attaches the session's sticky-routing headers:
//
//  1. `content` is always present. go-openai tags the field omitempty, so an
//     assistant message whose text is empty (a pure tool call) and a tool
//     message with an empty result would drop the key entirely — strict
//     OpenAI-compatible validators (LiteLLM-style relays, some vLLM setups)
//     reject that instead of treating the field as absent. The normalization is
//     deterministic, so the same bytes are produced on every request and the
//     prompt-cache prefix stays stable afterwards.
//  2. Every assistant message keeps an explicit (possibly empty) reasoning
//     field — the omitempty wire tag would drop it, and DeepSeek V4 / Kimi K3
//     reject a request whose assistant messages omit it (see
//     chatReasoningBackfillKey; asking to stop thinking is the one level that
//     adds nothing, since there is no reasoning to hand back).
//  3. reasoning_details captured from the previous response of this tool loop
//     are attached to that same message, so providers that require reasoning to
//     be passed back with its signature (Anthropic through OpenRouter) keep
//     working where `reasoning_content` alone is not enough.
//
// Every POST body is buffered once so the rewrite can be decided on the parsed
// message list; when nothing needs rewriting the original bytes are replayed
// untouched, and only the retry-safe body restoration happens unconditionally.
// Streaming responses are additionally wrapped so the adapter can tell a
// finished stream (`[DONE]`) from a connection that was cut mid-stream.
type chatRequestRewriteTransport struct {
	base           http.RoundTripper
	reasoningKey   string
	details        json.RawMessage
	headers        map[string]string
	promptCacheKey string
	streamDone     *sseDoneWatcher
	// disableThinking writes the stop-thinking field the "off" level asks for
	// (see patchChatRequestFields).
	disableThinking bool
}

func (t *chatRequestRewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	if req == nil {
		return nil, errors.New("chat request rewrite transport: nil request")
	}
	for key, value := range t.headers {
		req.Header.Set(key, value)
	}
	if req.Body == nil || req.Method != http.MethodPost {
		return base.RoundTrip(req)
	}
	body, err := io.ReadAll(req.Body)
	_ = req.Body.Close()
	if err != nil {
		return nil, err
	}
	rewritten := body
	if patched, ok := patchChatRequestFields(body, t.reasoningKey, t.details, t.promptCacheKey, t.disableThinking); ok {
		rewritten = patched
	}
	// Restore a replayable body: the SDK may retry the request object, so
	// GetBody must yield the rewritten bytes every time — otherwise net/http
	// fails retries with "ContentLength=X with Body length 0".
	req = req.Clone(req.Context())
	req.Body = io.NopCloser(bytes.NewReader(rewritten))
	req.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(rewritten)), nil
	}
	req.ContentLength = int64(len(rewritten))
	resp, err := base.RoundTrip(req)
	if err != nil || resp == nil || resp.Body == nil || t.streamDone == nil {
		return resp, err
	}
	if strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
		resp.Body = &sseDoneReadCloser{rc: resp.Body, watcher: t.streamDone}
	}
	return resp, nil
}

// patchChatRequestFields applies the Chat Completions request normalizations
// described on chatRequestRewriteTransport. Returns the new body and whether it
// changed.
//
// Key contracts (aligning with Kimi-code and KV-cache / Prompt Cache stability):
//  1. KV-cache / Prompt Cache preservation: every rewrite below is deterministic
//     and idempotent — the same wire bytes always produce the same output, and a
//     key that already exists is never rewritten — so from the first patched
//     request on the provider keeps hashing the same message prefix. The
//     `content` normalization and the reasoning placeholder are both one-time
//     rewrites of keys that are missing on every request.
//  2. EVERY assistant message carries the reasoning field on every non-OpenAI
//     endpoint whose level leaves thinking on: the requirement is per message,
//     not per turn, and a history restored from disk has no reasoning text left
//     to send (the disk profile never stores it). The field is written as an
//     explicit empty string — presence is what the provider validates, so the
//     session file stays free of stale reasoning. See chatReasoningBackfillKey
//     for why the endpoint, and not a model list, decides this, and for the "off"
//     level, which adds nothing at all.
//  3. If the assistant message already carries ANY known reasoning key
//     ("reasoning_content", "reasoning_details", "reasoning"), it is left
//     untouched — the real text the SDK serialized belongs there.
//  4. Dialect detection: echoes the dialect the messages actually carry
//     ("reply in the dialect the peer spoke"), falling back to defaultKey.
//     defaultKey is empty when thinking-mode replay is off, and then no
//     reasoning field is added at all. "reasoning_details" is an array, so it is
//     never used as an empty-string placeholder — the captured details array
//     (when present) fills that slot.
//  5. The session-sticky prompt cache key (and `store: false`) is attached for
//     the official OpenAI endpoint only; promptCacheKey is empty everywhere
//     else, so a compatible gateway never sees an unknown top-level parameter.
//  6. The stop-thinking field (`thinking: {"type": "disabled"}`) is written when
//     the selected level is "off" and the endpoint is not the official OpenAI API
//     (reasoningWireForAdapter). It is a top-level parameter, so the message
//     prefix the provider hashes stays untouched, and it is only written when
//     absent — the same bytes every request.
func patchChatRequestFields(body []byte, defaultKey string, details json.RawMessage, promptCacheKey string, disableThinking bool) ([]byte, bool) {
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
	changed := false

	// 1. Session-sticky prompt-cache routing, official endpoint only (the key is
	// empty elsewhere — see openAIChatPromptCacheKey). `store` is documented for
	// Chat Completions too and pi pins it to false so a response is never
	// retained server-side (openai-completions.ts:810-824). Both are top-level
	// parameters: the message prefix the provider hashes for the cache is not
	// touched.
	if promptCacheKey != "" {
		if _, exists := payload["prompt_cache_key"]; !exists {
			payload["prompt_cache_key"] = jsonStringValue(promptCacheKey)
			changed = true
		}
		if _, exists := payload["store"]; !exists {
			payload["store"] = jsonBoolFalse
			changed = true
		}
	}

	// 6. Stop-thinking field for the "off" level (see the doc comment).
	if disableThinking {
		if _, exists := payload["thinking"]; !exists {
			payload["thinking"] = jsonThinkingDisabled
			changed = true
		}
	}

	// 2. content must be a present key on assistant tool-call and tool
	// messages (see the transport doc comment).
	for i := range messages {
		switch chatMessageRole(messages[i]) {
		case legacyopenai.ChatMessageRoleAssistant:
			if !chatMessageHasToolCalls(messages[i]) {
				continue
			}
		case legacyopenai.ChatMessageRoleTool:
		default:
			continue
		}
		if _, exists := messages[i]["content"]; exists {
			continue
		}
		messages[i]["content"] = jsonEmptyString
		changed = true
	}

	// 3./4. reasoning replay. The dialect is resolved from the messages as they
	// arrived, before reasoning_details is attached: the attached array is itself
	// a known reasoning key and would otherwise become the detected dialect.
	fillKey := ""
	if defaultKey != "" {
		fillKey = detectChatReasoningDialect(messages, defaultKey)
	}

	// 3. reasoning_details belong to the active trailing tool turn: they are the
	// captured trace of the response that turn continues.
	if targetIdx := trailingToolTurnAssistantIndex(messages); targetIdx >= 0 && len(details) > 0 {
		if _, exists := messages[targetIdx]["reasoning_details"]; !exists {
			messages[targetIdx]["reasoning_details"] = details
			changed = true
		}
	}

	// 4. The plain reasoning field goes on every assistant message (see the doc
	// comment). "reasoning_details" is an array and can never be a placeholder.
	if fillKey != "" && fillKey != "reasoning_details" {
		for i := range messages {
			if chatMessageRole(messages[i]) != legacyopenai.ChatMessageRoleAssistant {
				continue
			}
			if chatMessageHasReasoningKey(messages[i]) {
				continue
			}
			messages[i][fillKey] = jsonEmptyString
			changed = true
		}
	}
	if !changed {
		return body, false
	}
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

func chatMessageRole(m map[string]json.RawMessage) string {
	role, _ := strconv.Unquote(string(m["role"]))
	return role
}

func chatMessageHasToolCalls(m map[string]json.RawMessage) bool {
	raw, ok := m["tool_calls"]
	if !ok {
		return false
	}
	trimmed := strings.TrimSpace(string(raw))
	return trimmed != "" && trimmed != "null" && trimmed != "[]"
}

func chatMessageHasReasoningKey(m map[string]json.RawMessage) bool {
	for _, key := range knownReasoningWireKeys {
		if _, exists := m[key]; exists {
			return true
		}
	}
	return false
}

// trailingToolTurnAssistantIndex returns the index of the assistant message
// that starts the trailing tool turn (tool calls optionally followed by the tool
// results answering them), or -1 when the history does not end in such a turn.
func trailingToolTurnAssistantIndex(messages []map[string]json.RawMessage) int {
	cut := len(messages)
	for cut > 0 && chatMessageRole(messages[cut-1]) == legacyopenai.ChatMessageRoleTool {
		cut--
	}
	if cut == 0 || chatMessageRole(messages[cut-1]) != legacyopenai.ChatMessageRoleAssistant || !chatMessageHasToolCalls(messages[cut-1]) {
		return -1
	}
	return cut - 1
}

// detectChatReasoningDialect echoes the reasoning dialect the request already
// speaks, falling back to the configured/default wire key.
func detectChatReasoningDialect(messages []map[string]json.RawMessage, defaultKey string) string {
	if defaultKey == "" {
		defaultKey = defaultReasoningTag
	}
	for _, m := range messages {
		for _, key := range knownReasoningWireKeys {
			if _, exists := m[key]; exists {
				return key
			}
		}
	}
	return defaultKey
}

// parseStreamReasoning extracts reasoning text and OpenRouter-style reasoning
// details from one streamed Chat Completions chunk. Providers disagree on the
// wire shape: DeepSeek/Moonshot stream `reasoning_content` (the SDK struct
// field), vLLM and OpenAI OSS stream `reasoning`, and OpenRouter streams
// `reasoning_details` as an ARRAY of detail objects — reasoning.text /
// reasoning.summary / reasoning.encrypted, fragmented across chunks. A string
// under any of those keys (some relays) is still treated as text. The key set
// is rawParsedReasoningKeys, i.e. every dialect except the SDK-typed
// "reasoning_content".
// chunkMentionsReasoningKey reports whether a streamed chunk mentions a
// raw-parsed reasoning key at all. The quoted form keeps unrelated fields from
// triggering a parse attempt: a chunk carrying only "reasoning_effort" or
// "reasoning.enabled" must not match.
func chunkMentionsReasoningKey(raw []byte) bool {
	for _, key := range rawParsedReasoningKeys {
		if bytes.Contains(raw, []byte(strconv.Quote(key))) {
			return true
		}
	}
	return false
}

func parseStreamReasoning(raw []byte) (string, []map[string]any) {
	if len(raw) == 0 || !chunkMentionsReasoningKey(raw) {
		return "", nil
	}
	var chunk struct {
		Choices []struct {
			Delta struct {
				Reasoning        any `json:"reasoning"`
				ReasoningText    any `json:"reasoning_text"`
				ReasoningDetails any `json:"reasoning_details"`
			} `json:"delta"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &chunk); err != nil || len(chunk.Choices) == 0 {
		return "", nil
	}
	delta := chunk.Choices[0].Delta
	// Precedence follows pi (openai-completions.ts:601-610): the first non-empty
	// reasoning field wins, in the order reasoning_content (SDK-typed, handled by
	// the caller), reasoning, reasoning_text.
	text := ""
	if s, ok := delta.Reasoning.(string); ok {
		text = s
	}
	if text == "" {
		if s, ok := delta.ReasoningText.(string); ok {
			text = s
		}
	}
	details := []map[string]any{}
	switch v := delta.ReasoningDetails.(type) {
	case string:
		// Some relays forward the details as a plain string.
		if text == "" {
			text = v
		}
	case []any:
		for _, detail := range v {
			m, ok := detail.(map[string]any)
			if !ok || len(m) == 0 {
				continue
			}
			details = appendChatReasoningDetail(details, m)
		}
		if text == "" {
			text = chatReasoningDetailsText(details)
		}
	}
	return text, details
}

// appendChatReasoningDetail merges one reasoning detail into the accumulated
// list the way pi does (openai-completions.ts appendOpenAIReasoningDetail):
// consecutive text/summary fragments of the same kind are concatenated — the
// provider streams them one token at a time — while encrypted details stay
// discrete. The signature of a textual block is kept from whichever fragment
// carried it.
func appendChatReasoningDetail(details []map[string]any, detail map[string]any) []map[string]any {
	kind, _ := detail["type"].(string)
	if len(details) > 0 {
		last := details[len(details)-1]
		if lastKind, _ := last["type"].(string); lastKind == kind {
			switch kind {
			case "reasoning.text":
				last["text"] = chatDetailString(last, "text") + chatDetailString(detail, "text")
				if chatDetailString(last, "signature") == "" {
					if sig := chatDetailString(detail, "signature"); sig != "" {
						last["signature"] = sig
					}
				}
				return details
			case "reasoning.summary":
				last["summary"] = chatDetailString(last, "summary") + chatDetailString(detail, "summary")
				return details
			}
		}
	}
	return append(details, detail)
}

func chatDetailString(detail map[string]any, key string) string {
	s, _ := detail[key].(string)
	return s
}

// chatReasoningDetailsText renders the human-readable part of merged reasoning
// details for the thinking panel; encrypted details have no displayable text.
func chatReasoningDetailsText(details []map[string]any) string {
	var b strings.Builder
	for _, detail := range details {
		kind, _ := detail["type"].(string)
		switch kind {
		case "reasoning.text":
			b.WriteString(chatDetailString(detail, "text"))
		case "reasoning.summary":
			b.WriteString(chatDetailString(detail, "summary"))
		}
	}
	return b.String()
}

// chatReasoningDetailsJSON serializes merged reasoning details for the request
// stash; nil when there is nothing to replay.
func chatReasoningDetailsJSON(details []map[string]any) json.RawMessage {
	if len(details) == 0 {
		return nil
	}
	raw, err := json.Marshal(details)
	if err != nil {
		return nil
	}
	return raw
}

func isAnthropicClaudeModel(model string) bool {
	m := strings.ToLower(strings.TrimSpace(model))
	return strings.Contains(m, "claude")
}

// anthropicRequestEndsInToolTurn reports whether the converted request ends in a
// tool turn: an assistant message carrying tool_use followed by the user message
// that carries its tool_result blocks. Thinking blocks may only be replayed
// there — Anthropic requires them alongside the tool_use blocks of the same turn
// and accepts omitting them everywhere else. The gate also keeps a stale payload
// from being attached to an unrelated assistant message after a rewind, a
// compaction, or when the payload belongs to another session.
func anthropicRequestEndsInToolTurn(messages []anthropic.MessageParam) bool {
	if len(messages) < 2 {
		return false
	}
	last := messages[len(messages)-1]
	prev := messages[len(messages)-2]
	if last.Role != anthropic.MessageParamRoleUser || prev.Role != anthropic.MessageParamRoleAssistant {
		return false
	}
	hasToolResult := false
	for _, block := range last.Content {
		if block.OfToolResult != nil {
			hasToolResult = true
			break
		}
	}
	if !hasToolResult {
		return false
	}
	for _, block := range prev.Content {
		if block.OfToolUse != nil {
			return true
		}
	}
	return false
}

// withAnthropicThinkingBlocks prepends the captured thinking blocks to the LAST
// assistant message of the request. Anthropic requires thinking blocks to be
// replayed unmodified, in order, alongside the tool_use blocks of the same
// assistant turn; buildAnthropicMessages emits tool_use blocks on the last
// assistant message, so the thinking blocks belong exactly there — and only
// there: without a trailing tool turn the payload is dropped, because the API
// allows omitting thinking for every earlier turn.
func withAnthropicThinkingBlocks(messages []anthropic.MessageParam, blocks []anthropicThinkingBlock, model string) []anthropic.MessageParam {
	if len(blocks) == 0 || len(messages) == 0 {
		return messages
	}
	if !anthropicRequestEndsInToolTurn(messages) {
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
