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
// only make sense for the model that produced them (they cannot survive a
// provider switch or a restart), so they live here in a per-session,
// per-model stash keyed by the prompt-cache session key, never in the
// persisted history. The stash is a per-TURN ledger: every tool-loop turn's
// payload is kept and replayed on its own assistant message in every later
// request, because a payload that decorates only the trailing turn is dropped
// from that (now older) message in the next request — the prefix bytes change
// and the provider prompt cache is lost from that message onward, re-billing
// the recent tool output on every agent step (pi keeps the payloads on the
// conversation messages for the same reason).

const (
	// maxReasoningStashEntries bounds the stash with LRU eviction. Keys are
	// scoped per session x wire protocol x model, plus one key per sub-agent run
	// (released when the run ends, see clearScope), so a bounded map is what keeps
	// long-lived processes from growing without limit; the eviction never drops
	// more than it has to (see evictLocked).
	maxReasoningStashEntries = 64
)

// reasoningTurn is the replay payload of ONE assistant turn of a tool loop,
// identified by the turn's tool-call ids. Keeping every turn — not just the
// latest response — is what keeps the request prefix byte-stable across
// agent steps (see the file-header note).
type reasoningTurn struct {
	// callIDs are the tool-call ids of the assistant turn that produced the
	// payload. Ids are minted/deduped per stream (toolcall.CallIDFoundry) and
	// survive into the history verbatim, so a later request can match the
	// turn again however many turns were appended since. runChat fills ids
	// that arrive empty only AFTER the adapter returned, so a turn whose ids
	// were all empty at capture time can never be matched again and is not
	// stored at all.
	callIDs []string

	// anthropic carries the thinking/redacted_thinking blocks of the turn,
	// in order, replayed at the front of the turn's assistant message.
	anthropic []anthropicThinkingBlock
	// responses carries the encrypted reasoning items of the turn, in order,
	// replayed right before the turn's function_call items.
	responses []responsesReasoningItem
	// responsesItems maps the turn's sanitized call id to the Responses item
	// id (fc_…) the model emitted for it. OpenAI pairs a replayed reasoning
	// item with the item that FOLLOWS it by id: sending the reasoning item's
	// id back without the follower's id is rejected ("Item 'rs_…' of type
	// 'reasoning' was provided without its required following item"), which
	// is why pi keeps the item id for the same model
	// (openai-responses-shared.ts:247-292 using the stored `call_id|item_id`).
	responsesItems map[string]string
	// chatDetails carries the turn's OpenRouter-style reasoning_details
	// (array form). Sending the details back is what preserves reasoning for
	// providers that require the signed trace (Anthropic through OpenRouter);
	// `reasoning_content` alone is not enough there.
	chatDetails json.RawMessage
}

func (t reasoningTurn) empty() bool {
	return len(t.anthropic) == 0 && len(t.responses) == 0 && t.chatDetails == nil
}

// sessionReasoningPayload is one session's per-turn replay ledger (scope:
// session x wire protocol x model, see reasoningReplayKey). Turns are
// append-only; appending replaces an existing turn of the same identity so a
// retried capture cannot stack duplicates. The ledger has no per-session turn
// cap on purpose: trimming it would shift the request prefix and kill the
// provider prompt cache. Its only bounds are the process lifetime (it is
// memory-only, never persisted) and compaction — when the compact threshold
// rewrites the history, the stash is cleared with it (see compactHistory).
type sessionReasoningPayload struct {
	turns []reasoningTurn
}

// turnForAny returns the turn whose call ids intersect callIDs, or nil. The
// nil receiver is a valid "no ledger" so callers can chain it off stash.get.
func (p *sessionReasoningPayload) turnForAny(callIDs []string) *reasoningTurn {
	if p == nil {
		return nil
	}
	for _, want := range callIDs {
		want = strings.TrimSpace(want)
		if want == "" {
			continue
		}
		for i := range p.turns {
			for _, stored := range p.turns[i].callIDs {
				if stored == want {
					return &p.turns[i]
				}
			}
		}
	}
	return nil
}

// chatDetailsByCallID flattens the ledger into the id→details map the Chat
// request rewrite consumes (see patchChatRequestFields).
func (p *sessionReasoningPayload) chatDetailsByCallID() map[string]json.RawMessage {
	if p == nil {
		return nil
	}
	var out map[string]json.RawMessage
	for _, turn := range p.turns {
		if len(turn.chatDetails) == 0 {
			continue
		}
		for _, id := range turn.callIDs {
			if out == nil {
				out = map[string]json.RawMessage{}
			}
			out[id] = turn.chatDetails
		}
	}
	return out
}

// toolCallIDsOf returns the non-empty tool-call ids of a turn — the identity
// the reasoning ledger matches on (see reasoningTurn.callIDs).
func toolCallIDsOf(calls []legacyopenai.ToolCall) []string {
	ids := make([]string, 0, len(calls))
	for _, c := range calls {
		if id := strings.TrimSpace(c.ID); id != "" {
			ids = append(ids, id)
		}
	}
	return ids
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
// reasoningStashScope normalizes the session component of every stash key. It is
// the one place the write path (reasoningReplayKey, whose cfg carries
// openAIResponsesPromptCacheKey(sessionID)) and the cleanup path (clearSession,
// which only has the raw session id) have to agree on: they used to derive it
// separately — the write path hashed the id, the cleanup path compared the raw
// id — which silently turned clearSession into a no-op, so released and rewound
// sessions kept replaying signatures of turns that no longer existed and their
// entries occupied the LRU against live tool loops.
func reasoningStashScope(sessionID string) string {
	return strings.ReplaceAll(reasoningStashKey(sessionID), "\x1f", "_")
}

// reasoningReplayScope is the replay-ledger scope of one conversation: the
// run-local identity when the run set one (a sub-agent run), the prompt-cache
// route otherwise. Two runs must never share a scope — the ledger replaces a turn
// whose first tool-call id matches, and a relay that renumbers every response as
// call_0 (exactly the shape CallIDFoundry repairs on the wire) would let one
// run's thinking blocks land on another run's turn.
func reasoningReplayScope(cfg ConfigState) string {
	if scope := strings.TrimSpace(cfg.reasoningScope); scope != "" {
		return scope
	}
	return cfg.responsesPromptCacheKey
}

func reasoningReplayKey(cfg ConfigState, model string) string {
	if strings.TrimSpace(model) == "" {
		model = cfg.Model
	}
	return strings.Join([]string{
		reasoningStashScope(reasoningReplayScope(cfg)),
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

// appendTurn records one turn's replay payload, evicting the coldest
// entries when the map is full. The payload snapshot is replaced copy-on-write
// so a reader holding the previous snapshot (a concurrent stateless caller
// sharing the default bucket) never observes a mid-append slice.
func (s *reasoningStash) appendTurn(key string, turn reasoningTurn) {
	if s == nil || turn.empty() || len(turn.callIDs) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.entries[key]
	if !ok {
		s.evictLocked(key)
		entry = &reasoningStashEntry{}
		s.entries[key] = entry
	}
	next := &sessionReasoningPayload{}
	if entry.payload != nil {
		next.turns = append(next.turns, entry.payload.turns...)
	}
	replaced := false
	for i := range next.turns {
		if len(next.turns[i].callIDs) > 0 && next.turns[i].callIDs[0] == turn.callIDs[0] {
			next.turns[i] = turn
			replaced = true
			break
		}
	}
	if !replaced {
		next.turns = append(next.turns, turn)
	}
	s.clock++
	entry.lastUsed = s.clock
	entry.payload = next
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

// clearSession drops every payload of one session across protocols and models.
// Called when the session's history is discarded or rewritten (session release,
// rewind/truncate): the retained signatures and encrypted reasoning belong to
// turns that no longer exist, and replaying them later would attach them to an
// unrelated assistant message.
func (s *reasoningStash) clearSession(sessionID string) {
	s.clearScope(openAIResponsesPromptCacheKey(sessionID))
}

// clearScope drops every payload of one stash scope across protocols and models.
// Callers pass the scope reasoningReplayScope would derive — a released session,
// or a finished sub-agent run, whose scope is run-local and would otherwise keep
// a dead entry inside the bounded stash until the LRU cap evicted a live session
// instead. An empty scope is a no-op: those keys belong to stateless callers that
// share the default bucket, not to whoever called clear.
func (s *reasoningStash) clearScope(scope string) {
	if s == nil {
		return
	}
	scope = strings.TrimSpace(scope)
	if scope == "" {
		return
	}
	prefix := reasoningStashScope(scope) + "\x1f"
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
// The terminator frame is matched the way go-openai's own reader matches it
// (`strings.TrimSpace(line)` → prefix `data:` → TrimSpace of the rest equals
// "[DONE]", stream_reader.go:13-16 and :96-99), not by a single literal:
// `data: [DONE]`, `data:[DONE]` and `data:  [DONE]` all terminate the stream for
// the SDK, so a literal-only match would report a finished stream as truncated
// whenever a gateway sends one of the other spellings.
//
// Matching is anchored to a frame line and never searched for in the raw byte
// window: the window also holds the payloads, so assistant text or a tool
// argument that literally contains `data: [DONE]` (a review of this scanner, an
// SSE test file) would mark a stream that is cut later as complete, and the
// truncated turn would be persisted as a finished one.
var (
	sseDoneField = []byte("data:")
	sseDoneToken = []byte("[DONE]")
)

// maxSSEDoneLineBytes bounds the per-line buffer: every accepted spelling of the
// terminator fits in far less, so a longer line is known not to be one and its
// bytes are dropped instead of accumulated.
const maxSSEDoneLineBytes = 64

// lineIsSSEDone reports whether one complete frame line is the terminator.
func lineIsSSEDone(line []byte) bool {
	trimmed := bytes.TrimSpace(line)
	if !bytes.HasPrefix(trimmed, sseDoneField) {
		return false
	}
	return bytes.Equal(bytes.TrimSpace(trimmed[len(sseDoneField):]), sseDoneToken)
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

// sseDoneReadCloser matches the terminator frame while the body streams by. Only
// the current, not yet newline-terminated line is kept (bounded by
// maxSSEDoneLineBytes), which is what lets a marker split across two reads — or
// two events arriving in one read — still be recognized.
type sseDoneReadCloser struct {
	rc      io.ReadCloser
	watcher *sseDoneWatcher
	line    []byte
	// dead marks a line that can no longer be the terminator (it got too long).
	// Its remainder is the middle of a line, never the start of a new one.
	dead bool
}

func (r *sseDoneReadCloser) Read(p []byte) (int, error) {
	n, err := r.rc.Read(p)
	if n > 0 && !r.watcher.Done() {
		r.scanLines(p[:n])
	}
	if err != nil && !r.watcher.Done() && r.lineIsDone() {
		// The last line of a body is not required to end with a newline.
		r.watcher.mark()
	}
	return n, err
}

func (r *sseDoneReadCloser) scanLines(chunk []byte) {
	for len(chunk) > 0 {
		idx := bytes.IndexByte(chunk, '\n')
		if idx < 0 {
			r.appendLine(chunk)
			return
		}
		r.appendLine(chunk[:idx])
		if r.lineIsDone() {
			r.watcher.mark()
			return
		}
		r.line = r.line[:0]
		r.dead = false
		chunk = chunk[idx+1:]
	}
}

func (r *sseDoneReadCloser) appendLine(b []byte) {
	if r.dead {
		return
	}
	if len(r.line)+len(b) > maxSSEDoneLineBytes {
		r.dead = true
		r.line = r.line[:0]
		return
	}
	r.line = append(r.line, b...)
}

func (r *sseDoneReadCloser) lineIsDone() bool {
	return !r.dead && lineIsSSEDone(r.line)
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
//  3. reasoning_details captured from each tool-loop turn are attached to
//     that turn's own assistant message — on EVERY request, not just the
//     trailing one — so providers that require reasoning to be passed back
//     with its signature (Anthropic through OpenRouter) keep working where
//     `reasoning_content` alone is not enough, and the message prefix stays
//     byte-identical across agent steps (prompt-cache stability).
//
// Every POST body is buffered once so the rewrite can be decided on the parsed
// message list; when nothing needs rewriting the original bytes are replayed
// untouched, and only the retry-safe body restoration happens unconditionally.
// Streaming responses are additionally wrapped so the adapter can tell a
// finished stream (`[DONE]`) from a connection that was cut mid-stream.
type chatRequestRewriteTransport struct {
	base           http.RoundTripper
	reasoningKey   string
	turnDetails    map[string]json.RawMessage
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
	if patched, ok := patchChatRequestFields(body, t.reasoningKey, t.turnDetails, t.promptCacheKey, t.disableThinking); ok {
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
func patchChatRequestFields(body []byte, defaultKey string, turnDetails map[string]json.RawMessage, promptCacheKey string, disableThinking bool) ([]byte, bool) {
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

	// 3. reasoning_details belong to their own assistant turn: the captured
	// trace of the response that turn continues, matched by the turn's
	// tool-call ids. Attaching every turn's details on every request — not
	// just the trailing turn's — keeps the message prefix byte-identical
	// across agent steps, which is what the provider prompt cache hashes.
	for i := range messages {
		if chatMessageRole(messages[i]) != legacyopenai.ChatMessageRoleAssistant || !chatMessageHasToolCalls(messages[i]) {
			continue
		}
		if _, exists := messages[i]["reasoning_details"]; exists {
			continue
		}
		details, ok := chatTurnDetails(turnDetails, chatToolCallIDs(messages[i]))
		if !ok {
			continue
		}
		messages[i]["reasoning_details"] = details
		changed = true
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

// chatToolCallIDs returns the tool-call ids of a serialized assistant
// message, in order, for matching the message against the session's per-turn
// reasoning ledger.
func chatToolCallIDs(m map[string]json.RawMessage) []string {
	raw, ok := m["tool_calls"]
	if !ok {
		return nil
	}
	var calls []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &calls); err != nil {
		return nil
	}
	ids := make([]string, 0, len(calls))
	for _, c := range calls {
		if id := strings.TrimSpace(c.ID); id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}

// chatTurnDetails returns the captured reasoning_details of the first ledger
// turn matching any of callIDs.
func chatTurnDetails(turnDetails map[string]json.RawMessage, callIDs []string) (json.RawMessage, bool) {
	if len(turnDetails) == 0 {
		return nil, false
	}
	for _, id := range callIDs {
		if details, ok := turnDetails[id]; ok && len(details) > 0 {
			return details, true
		}
	}
	return nil, false
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

// anthropicThinkingBlockParams converts captured thinking blocks into request
// content blocks. Redacted blocks replay verbatim; plain thinking blocks must
// carry the provider signature — Anthropic's official API rejects an unsigned
// thinking block outright ("thinking.signature: Field required" and signature
// validation), so unsigned blocks are dropped there. Compatible gateways by
// contrast mostly check only field presence (the SDK always serializes
// signature, even as an empty string), so unsigned blocks replay to them with
// signature:"" — dropping them would silently lose reasoning context and can
// even 400 on gateways that require the field (see the effort-off-level lesson:
// the discriminator is the endpoint, not the model name).
func anthropicThinkingBlockParams(blocks []anthropicThinkingBlock, officialEndpoint bool) []anthropic.ContentBlockParamUnion {
	out := make([]anthropic.ContentBlockParamUnion, 0, len(blocks))
	for _, b := range blocks {
		if b.redacted() {
			out = append(out, anthropic.NewRedactedThinkingBlock(b.Data))
			continue
		}
		if officialEndpoint && strings.TrimSpace(b.Signature) == "" {
			continue
		}
		out = append(out, anthropic.NewThinkingBlock(b.Signature, b.Thinking))
	}
	return out
}
