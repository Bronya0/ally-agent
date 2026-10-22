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
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	openai "github.com/sashabaranov/go-openai"
)

const (
	// maxSessionIndexEntries bounds how many entries sessions/index.json
	// keeps. Index writes never fail on volume; the oldest entries (lowest
	// UpdatedAt) are evicted instead. Evicted snapshots and histories stay
	// on disk and are rediscovered by ListSessions(), so eviction costs at
	// most repeated re-discovery work, never conversation data.
	maxSessionIndexEntries = 2000

	maxSessionSnapshotJSONBytes = 16 * 1024 * 1024
	maxSessionIndexTitleChars   = 160
	// SessionIndexEntry.FirstPrompt is a short list preview, not
	// conversation content. Clamp it wherever an entry is normalized so a
	// giant first prompt cannot bloat the index across rewrites.
	maxSessionIndexFirstPromptChars = 160
)

// SessionIndexEntry is the small, frontend-facing metadata record stored in
// sessions/index.json. Conversation messages are intentionally kept in the
// per-session snapshot file and are loaded only when the user selects a
// session.
type SessionIndexEntry struct {
	ID            string   `json:"id"`
	Title         string   `json:"title"`
	FirstPrompt   string   `json:"firstPrompt,omitempty"`
	Workspace     string   `json:"workspace"`
	ExtraRoots    []string `json:"extraRoots,omitempty"`
	CreatedAt     int64    `json:"createdAt"`
	UpdatedAt     int64    `json:"updatedAt"`
	MessageCount  int      `json:"messageCount"`
	ContextTokens int      `json:"contextTokens,omitempty"`
	HasSnapshot   bool     `json:"hasSnapshot"`
}

// SessionSnapshot is the UI conversation snapshot stored in a local file.
// Messages use map values because the frontend attaches display-only fields
// to tool cards and generated media that are not part of the model history
// protocol.
type SessionSnapshot struct {
	ID            string           `json:"id"`
	Title         string           `json:"title"`
	FirstPrompt   string           `json:"firstPrompt,omitempty"`
	Workspace     string           `json:"workspace"`
	ExtraRoots    []string         `json:"extraRoots,omitempty"`
	CreatedAt     int64            `json:"createdAt"`
	UpdatedAt     int64            `json:"updatedAt"`
	ContextTokens int              `json:"contextTokens,omitempty"`
	HasSnapshot   bool             `json:"hasSnapshot"`
	Messages      []map[string]any `json:"messages"`
}

// ListSessionIndex is the startup fast path: no directory scans or snapshot
// decompression. Runtime maintenance repairs orphaned/legacy records later.
func (a *App) ListSessionIndex() ([]SessionIndexEntry, error) {
	if err := a.ensureInitialized(); err != nil {
		return nil, err
	}
	a.sessionMu.Lock()
	defer a.sessionMu.Unlock()
	return a.readSessionIndexLocked()
}

// ListSessions retains the explicit recovery path for API/manual callers.
func (a *App) ListSessions() ([]SessionIndexEntry, error) {
	return a.repairSessionIndex(context.Background())
}

func (a *App) repairSessionIndex(ctx context.Context) ([]SessionIndexEntry, error) {
	if err := a.ensureInitialized(); err != nil {
		return nil, err
	}
	if a.sessionsDir == "" {
		return []SessionIndexEntry{}, nil
	}

	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		a.sessionMu.Lock()
		entries, err := a.readSessionIndexLocked()
		revision := a.sessionIndexRevision
		a.sessionMu.Unlock()
		if err != nil {
			return nil, err
		}
		entries, changed, err := a.reconcileSessionEntries(ctx, entries)
		if err != nil {
			return nil, err
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		committed, err := a.commitSessionIndexRepair(revision, entries, changed)
		if err != nil || committed {
			if err == nil && changed {
				a.emit("sessions:index-repaired", nil)
			}
			return entries, err
		}
		// A user edited/deleted a session during the disk scan. Re-read the latest
		// index rather than overwriting it with stale repair results.
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func (a *App) commitSessionIndexRepair(revision uint64, entries []SessionIndexEntry, changed bool) (bool, error) {
	a.sessionMu.Lock()
	defer a.sessionMu.Unlock()
	if revision != a.sessionIndexRevision {
		return false, nil
	}
	if changed {
		return true, a.writeSessionIndexLocked(entries)
	}
	return true, nil
}

// All expensive discovery/decompression runs without sessionMu. The commit
// checks its input revision before publishing, including concurrent deletions.
func (a *App) reconcileSessionEntries(ctx context.Context, entries []SessionIndexEntry) ([]SessionIndexEntry, bool, error) {
	changed := false
	known := make(map[string]struct{}, len(entries))
	entryIndexes := make(map[string]int, len(entries))
	for index, entry := range entries {
		known[entry.ID] = struct{}{}
		entryIndexes[entry.ID] = index
	}

	for _, sessionID := range a.diskSnapshotSessionIDs() {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		if index, exists := entryIndexes[sessionID]; exists {
			if !entries[index].HasSnapshot {
				entries[index].HasSnapshot = true
				changed = true
			}
			continue
		}
		entry := a.snapshotIndexEntry(sessionID)
		entries = append(entries, entry)
		known[sessionID] = struct{}{}
		entryIndexes[sessionID] = len(entries) - 1
		changed = true
	}

	for _, sessionID := range a.diskHistorySessionIDs() {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		if _, exists := known[sessionID]; exists {
			continue
		}
		entry := a.legacySessionIndexEntry(sessionID)
		if entry.ID == "" {
			continue
		}
		entries = append(entries, entry)
		known[sessionID] = struct{}{}
		changed = true
	}

	// Backfill metadata created before firstPrompt was added. This runs only
	// while the field is missing and persists the result for later fast opens.
	for index := range entries {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		entry := &entries[index]
		if entry.FirstPrompt != "" {
			continue
		}
		if entry.HasSnapshot {
			snapshot, snapshotErr := readCompressedSessionSnapshot(a.sessionSnapshotPath(entry.ID))
			if snapshotErr != nil {
				continue
			}
			enriched := sessionIndexEntryFromSnapshot(snapshot)
			if enriched.FirstPrompt != "" {
				entry.FirstPrompt = enriched.FirstPrompt
				changed = true
			}
			if entry.MessageCount <= 0 && enriched.MessageCount > 0 {
				entry.MessageCount = enriched.MessageCount
				changed = true
			}
			if entry.ContextTokens <= 0 && enriched.ContextTokens > 0 {
				entry.ContextTokens = enriched.ContextTokens
				changed = true
			}
			if entry.Workspace == "" && enriched.Workspace != "" {
				entry.Workspace = enriched.Workspace
				changed = true
			}
			continue
		}
		history, _ := a.readHistoryFromDisk(entry.ID)
		firstPrompt := firstPromptFromHistory(history)
		if firstPrompt == "" {
			continue
		}
		entry.FirstPrompt = firstPrompt
		if entry.ContextTokens <= 0 {
			entry.ContextTokens = estimateTokensFromMessages(history)
		}
		changed = true
	}

	return normalizeSessionIndexEntries(entries), changed, nil
}

// LoadSession loads exactly one session snapshot from disk. Legacy model
// history files are converted to a minimal display snapshot on demand.
func (a *App) LoadSession(sessionID string) (SessionSnapshot, error) {
	if err := a.ensureInitialized(); err != nil {
		return SessionSnapshot{}, err
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return SessionSnapshot{}, errors.New("session id is required")
	}

	a.sessionMu.Lock()
	defer a.sessionMu.Unlock()

	if path := a.sessionSnapshotPath(sessionID); path != "" {
		if snapshot, err := readCompressedSessionSnapshot(path); err == nil {
			snapshot.HasSnapshot = true
			return normalizeSessionSnapshot(snapshot, sessionID), nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return SessionSnapshot{}, err
		}
	}

	history := a.loadSessionHistoryCopy(sessionID)
	if len(history) == 0 {
		if entries, indexErr := a.readSessionIndexLocked(); indexErr == nil {
			for _, entry := range entries {
				if entry.ID != sessionID {
					continue
				}
				return SessionSnapshot{
					ID:          entry.ID,
					Title:       entry.Title,
					Workspace:   entry.Workspace,
					ExtraRoots:  cloneStringSlice(entry.ExtraRoots),
					CreatedAt:   entry.CreatedAt,
					UpdatedAt:   entry.UpdatedAt,
					HasSnapshot: entry.HasSnapshot,
					Messages:    []map[string]any{},
				}, nil
			}
		}
		return SessionSnapshot{}, os.ErrNotExist
	}
	snapshot := legacySessionSnapshot(sessionID, history)
	if entries, indexErr := a.readSessionIndexLocked(); indexErr == nil {
		for _, entry := range entries {
			if entry.ID != sessionID {
				continue
			}
			snapshot.Title = entry.Title
			snapshot.Workspace = entry.Workspace
			snapshot.ExtraRoots = cloneStringSlice(entry.ExtraRoots)
			if entry.CreatedAt > 0 {
				snapshot.CreatedAt = entry.CreatedAt
			}
			if entry.UpdatedAt > 0 {
				snapshot.UpdatedAt = entry.UpdatedAt
			}
			if entry.ContextTokens > 0 {
				snapshot.ContextTokens = entry.ContextTokens
			}
			break
		}
	}
	return snapshot, nil
}

// syncSessionIndexFromHistory refreshes metadata for runs without a UI writer.
func (a *App) syncSessionIndexFromHistory(sessionID string) error {
	snapshot, err := a.LoadSession(sessionID)
	if err != nil {
		return err
	}
	history := a.loadSessionHistoryCopy(sessionID)
	snapshot.Messages = legacySessionSnapshot(sessionID, history).Messages
	snapshot.ContextTokens = estimateTokensFromMessages(history)
	snapshot.UpdatedAt = time.Now().UnixMilli()
	return a.SaveSessionIndex(sessionIndexEntryFromSnapshot(snapshot))
}

// SaveSession writes one complete UI snapshot and updates the local index.
// The frontend calls this only after a completed turn or an explicit session
// metadata change; streaming state is never persisted here.
func (a *App) SaveSession(snapshot SessionSnapshot) error {
	if err := a.ensureInitialized(); err != nil {
		return err
	}
	snapshot = normalizeSessionSnapshot(snapshot, strings.TrimSpace(snapshot.ID))
	snapshot.HasSnapshot = true
	if snapshot.ID == "" {
		return errors.New("session id is required")
	}
	if a.sessionsDir == "" {
		return errors.New("session storage is not initialized")
	}

	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	if len(encoded) > maxSessionSnapshotJSONBytes {
		return fmt.Errorf("session snapshot exceeds %d bytes", maxSessionSnapshotJSONBytes)
	}

	a.sessionMu.Lock()
	defer a.sessionMu.Unlock()
	if err := writeCompressedJSONAtomic(a.sessionSnapshotPath(snapshot.ID), encoded); err != nil {
		return err
	}
	entry := sessionIndexEntryFromSnapshot(snapshot)
	entry.HasSnapshot = true
	entries, err := a.readSessionIndexLocked()
	if err != nil {
		return err
	}
	entries = replaceSessionIndexEntry(entries, entry)
	return a.writeSessionIndexLocked(entries)
}

// SaveSessionIndex updates only one small metadata record. It is used for
// workspace/title/mode changes without rewriting a large conversation file.
func (a *App) SaveSessionIndex(entry SessionIndexEntry) error {
	if err := a.ensureInitialized(); err != nil {
		return err
	}
	entry = normalizeSessionIndexEntry(entry)
	if entry.ID == "" {
		return errors.New("session id is required")
	}
	if a.sessionsDir == "" {
		return errors.New("session storage is not initialized")
	}

	a.sessionMu.Lock()
	defer a.sessionMu.Unlock()
	entries, err := a.readSessionIndexLocked()
	if err != nil {
		return err
	}
	return a.writeSessionIndexLocked(replaceSessionIndexEntry(entries, entry))
}

func (a *App) deleteSessionSnapshot(sessionID string) error {
	if strings.TrimSpace(sessionID) == "" || a.sessionsDir == "" {
		return nil
	}

	a.sessionMu.Lock()
	defer a.sessionMu.Unlock()
	a.sessionIndexRevision++
	if path := a.sessionSnapshotPath(sessionID); path != "" {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	entries, err := a.readSessionIndexLocked()
	if err != nil {
		return err
	}
	filtered := entries[:0]
	for _, entry := range entries {
		if entry.ID != sessionID {
			filtered = append(filtered, entry)
		}
	}
	return a.writeSessionIndexLocked(filtered)
}

func (a *App) sessionSnapshotPath(sessionID string) string {
	if a.sessionsDir == "" {
		return ""
	}
	return filepath.Join(a.sessionsDir, url.PathEscape(strings.TrimSpace(sessionID))+".json.gz")
}

func (a *App) sessionIndexPath() string {
	if a.sessionsDir == "" {
		return ""
	}
	return filepath.Join(a.sessionsDir, "index.json")
}

func (a *App) readSessionIndexLocked() ([]SessionIndexEntry, error) {
	path := a.sessionIndexPath()
	if path == "" {
		return []SessionIndexEntry{}, nil
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return []SessionIndexEntry{}, nil
	}
	if err != nil {
		return nil, err
	}
	var entries []SessionIndexEntry
	if len(bytes.TrimSpace(data)) == 0 {
		return []SessionIndexEntry{}, nil
	}
	if err := json.Unmarshal(data, &entries); err != nil {
		// An unreadable index (truncated write, manual edit) must never fail
		// every session operation. Degrade to empty: ListSessions() rescans
		// snapshots and histories from disk and the next index write replaces
		// this file with a healthy one.
		log.Printf("session index %s unreadable (%v); rebuilding from disk", path, err)
		return []SessionIndexEntry{}, nil
	}
	return normalizeSessionIndexEntries(entries), nil
}

func (a *App) writeSessionIndexLocked(entries []SessionIndexEntry) error {
	path := a.sessionIndexPath()
	if path == "" {
		return nil
	}
	entries = normalizeSessionIndexEntries(entries)
	entries = pruneSessionIndexEntries(entries)
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return err
	}
	if err := writeAtomicBytes(path, data, 0o600); err != nil {
		return err
	}
	a.sessionIndexRevision++
	return nil
}

func normalizeSessionIndexEntries(entries []SessionIndexEntry) []SessionIndexEntry {
	byID := make(map[string]SessionIndexEntry, len(entries))
	for _, entry := range entries {
		entry = normalizeSessionIndexEntry(entry)
		if entry.ID == "" {
			continue
		}
		previous, exists := byID[entry.ID]
		if !exists || entry.UpdatedAt >= previous.UpdatedAt {
			byID[entry.ID] = entry
		}
	}
	result := make([]SessionIndexEntry, 0, len(byID))
	for _, entry := range byID {
		result = append(result, entry)
	}
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].UpdatedAt != result[j].UpdatedAt {
			return result[i].UpdatedAt > result[j].UpdatedAt
		}
		return result[i].CreatedAt > result[j].CreatedAt
	})
	return result
}

func normalizeSessionIndexEntry(entry SessionIndexEntry) SessionIndexEntry {
	entry.ID = strings.TrimSpace(entry.ID)
	entry.Title = normalizeSessionIndexTitle(entry.Title)
	entry.FirstPrompt = normalizeSessionIndexPreview(entry.FirstPrompt)
	entry.Workspace = strings.TrimSpace(entry.Workspace)
	entry.ExtraRoots = cloneStringSlice(entry.ExtraRoots)
	now := time.Now().UnixMilli()
	if entry.CreatedAt <= 0 {
		entry.CreatedAt = now
	}
	if entry.UpdatedAt <= 0 {
		entry.UpdatedAt = entry.CreatedAt
	}
	if entry.MessageCount < 0 {
		entry.MessageCount = 0
	}
	if entry.ContextTokens < 0 {
		entry.ContextTokens = 0
	}
	return entry
}

// normalizeSessionIndexPreview collapses whitespace and clamps an index
// preview (FirstPrompt) to its own budget. Unlike titles, an absent preview
// stays empty instead of falling back to a placeholder.
func normalizeSessionIndexPreview(preview string) string {
	preview = strings.Join(strings.Fields(preview), " ")
	runes := []rune(preview)
	if len(runes) > maxSessionIndexFirstPromptChars {
		return string(runes[:maxSessionIndexFirstPromptChars-1]) + "\u2026"
	}
	return preview
}

func normalizeSessionIndexTitle(title string) string {
	title = strings.Join(strings.Fields(title), " ")
	if title == "" {
		return "Session"
	}
	runes := []rune(title)
	if len(runes) > maxSessionIndexTitleChars {
		return string(runes[:maxSessionIndexTitleChars-1]) + "…"
	}
	return title
}

func normalizeSessionSnapshot(snapshot SessionSnapshot, fallbackID string) SessionSnapshot {
	snapshot.ID = strings.TrimSpace(snapshot.ID)
	if snapshot.ID == "" {
		snapshot.ID = strings.TrimSpace(fallbackID)
	}
	if snapshot.FirstPrompt == "" {
		_, snapshot.FirstPrompt, _ = summarizeSessionMessages(snapshot.Messages)
	}
	entry := normalizeSessionIndexEntry(SessionIndexEntry{
		ID:          snapshot.ID,
		Title:       snapshot.Title,
		FirstPrompt: snapshot.FirstPrompt,
		Workspace:   snapshot.Workspace,
		ExtraRoots:  snapshot.ExtraRoots,
		CreatedAt:   snapshot.CreatedAt,
		UpdatedAt:   snapshot.UpdatedAt,
	})
	snapshot.ID = entry.ID
	snapshot.Title = entry.Title
	snapshot.FirstPrompt = entry.FirstPrompt
	snapshot.Workspace = entry.Workspace
	snapshot.ExtraRoots = entry.ExtraRoots
	snapshot.CreatedAt = entry.CreatedAt
	snapshot.UpdatedAt = entry.UpdatedAt
	if snapshot.Messages == nil {
		snapshot.Messages = []map[string]any{}
	}
	return snapshot
}

func replaceSessionIndexEntry(entries []SessionIndexEntry, replacement SessionIndexEntry) []SessionIndexEntry {
	result := make([]SessionIndexEntry, 0, len(entries)+1)
	for _, entry := range entries {
		if entry.ID != replacement.ID {
			result = append(result, entry)
		}
	}
	result = append(result, replacement)
	return normalizeSessionIndexEntries(result)
}

// pruneSessionIndexEntries keeps the index volume-bounded by evicting the
// oldest entries (lowest UpdatedAt) once the count exceeds
// maxSessionIndexEntries. Only metadata rows are removed: the corresponding
// snapshot/history files stay on disk and ListSessions() re-derives entries
// for them on demand, so eviction is always recoverable.
func pruneSessionIndexEntries(entries []SessionIndexEntry) []SessionIndexEntry {
	if len(entries) <= maxSessionIndexEntries {
		return entries
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].UpdatedAt != entries[j].UpdatedAt {
			return entries[i].UpdatedAt > entries[j].UpdatedAt
		}
		return entries[i].CreatedAt > entries[j].CreatedAt
	})
	return entries[:maxSessionIndexEntries]
}

func sessionIndexEntryFromSnapshot(snapshot SessionSnapshot) SessionIndexEntry {
	messageCount, firstPrompt, _ := summarizeSessionMessages(snapshot.Messages)
	if firstPrompt == "" {
		firstPrompt = snapshot.FirstPrompt
	}
	return normalizeSessionIndexEntry(SessionIndexEntry{
		ID:            snapshot.ID,
		Title:         snapshot.Title,
		FirstPrompt:   firstPrompt,
		Workspace:     snapshot.Workspace,
		ExtraRoots:    snapshot.ExtraRoots,
		CreatedAt:     snapshot.CreatedAt,
		UpdatedAt:     snapshot.UpdatedAt,
		ContextTokens: snapshot.ContextTokens,
		MessageCount:  messageCount,
		HasSnapshot:   true,
	})
}

func summarizeSessionMessages(messages []map[string]any) (int, string, string) {
	count := 0
	firstPrompt := ""
	latestPrompt := ""
	for _, message := range messages {
		role := strings.TrimSpace(stringValue(message["role"]))
		if role != openai.ChatMessageRoleUser && role != openai.ChatMessageRoleAssistant {
			continue
		}
		count++
		if role != openai.ChatMessageRoleUser {
			continue
		}
		content := strings.TrimSpace(stringValue(message["content"]))
		if content == "" {
			if attachments, ok := message["attachments"].([]any); ok && len(attachments) > 0 {
				content = fmt.Sprintf("%d attachment(s)", len(attachments))
			}
		}
		if content == "" {
			continue
		}
		if firstPrompt == "" {
			firstPrompt = content
		}
		latestPrompt = content
	}
	return count, firstPrompt, latestPrompt
}

func firstPromptFromHistory(history []openai.ChatCompletionMessage) string {
	for _, message := range history {
		if message.Role != openai.ChatMessageRoleUser {
			continue
		}
		content := strings.TrimSpace(message.Content)
		if content == "" && len(message.MultiContent) > 0 {
			content = strings.TrimSpace(textFromMultiContent(message.MultiContent))
		}
		if content != "" {
			return content
		}
	}
	return ""
}

func stringValue(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	return fmt.Sprint(value)
}

func (a *App) diskSnapshotSessionIDs() []string {
	if a.sessionsDir == "" {
		return nil
	}
	entries, err := os.ReadDir(a.sessionsDir)
	if err != nil {
		return nil
	}
	ids := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json.gz") {
			continue
		}
		encoded := strings.TrimSuffix(entry.Name(), ".json.gz")
		id, err := url.PathUnescape(encoded)
		if err == nil && strings.TrimSpace(id) != "" {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

func (a *App) snapshotIndexEntry(sessionID string) SessionIndexEntry {
	if snapshot, err := readCompressedSessionSnapshot(a.sessionSnapshotPath(sessionID)); err == nil {
		entry := sessionIndexEntryFromSnapshot(snapshot)
		entry.ID = sessionID
		entry.HasSnapshot = true
		return entry
	}
	entry := SessionIndexEntry{ID: sessionID, Title: "Session", HasSnapshot: true}
	if stat, err := os.Stat(a.sessionSnapshotPath(sessionID)); err == nil {
		entry.CreatedAt = stat.ModTime().UnixMilli()
		entry.UpdatedAt = entry.CreatedAt
	}
	return normalizeSessionIndexEntry(entry)
}

func (a *App) diskHistorySessionIDs() []string {
	if a.historiesDir == "" {
		return nil
	}
	entries, err := os.ReadDir(a.historiesDir)
	if err != nil {
		return nil
	}
	ids := make([]string, 0, len(entries))
	seen := map[string]struct{}{}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		var encoded string
		switch {
		case strings.HasSuffix(name, ".json.gz"):
			encoded = strings.TrimSuffix(name, ".json.gz")
		case strings.HasSuffix(name, ".json"):
			encoded = strings.TrimSuffix(name, ".json")
		default:
			continue
		}
		id, err := url.PathUnescape(encoded)
		if err != nil || strings.TrimSpace(id) == "" {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func (a *App) legacySessionIndexEntry(sessionID string) SessionIndexEntry {
	entry := SessionIndexEntry{ID: sessionID, Title: "Session"}
	history, _ := a.readHistoryFromDisk(sessionID)
	if len(history) > 0 {
		count := 0
		firstPrompt := ""
		for _, message := range history {
			if message.Role != openai.ChatMessageRoleUser && message.Role != openai.ChatMessageRoleAssistant {
				continue
			}
			count++
			if message.Role == openai.ChatMessageRoleUser {
				content := strings.TrimSpace(message.Content)
				if content == "" {
					content = strings.TrimSpace(textFromMultiContent(message.MultiContent))
				}
				if content != "" {
					if firstPrompt == "" {
						firstPrompt = content
					}
				}
			}
		}
		entry.MessageCount = count
		if firstPrompt != "" {
			entry.Title = firstPrompt
			entry.FirstPrompt = firstPrompt
		}
		entry.ContextTokens = estimateTokensFromMessages(history)
	}
	if stat, err := os.Stat(a.historyDiskPaths(sessionID)[0]); err == nil {
		entry.CreatedAt = stat.ModTime().UnixMilli()
		entry.UpdatedAt = stat.ModTime().UnixMilli()
	} else if stat, err := os.Stat(a.historyDiskPaths(sessionID)[1]); err == nil {
		entry.CreatedAt = stat.ModTime().UnixMilli()
		entry.UpdatedAt = stat.ModTime().UnixMilli()
	}
	return normalizeSessionIndexEntry(entry)
}

func legacySessionSnapshot(sessionID string, history []openai.ChatCompletionMessage) SessionSnapshot {
	messages := make([]map[string]any, 0, len(history))
	for _, message := range history {
		if message.Role == openai.ChatMessageRoleSystem {
			continue
		}
		content := message.Content
		if content == "" && len(message.MultiContent) > 0 {
			content = textFromMultiContent(message.MultiContent)
		}
		if message.Role == openai.ChatMessageRoleUser || message.Role == openai.ChatMessageRoleAssistant {
			if strings.TrimSpace(content) == "" {
				continue
			}
			messages = append(messages, map[string]any{
				"role":    message.Role,
				"content": content,
				"done":    true,
			})
		}
	}
	entry := SessionIndexEntry{ID: sessionID, Title: "Session"}
	count, firstPrompt, _ := summarizeSessionMessages(messages)
	entry.MessageCount = count
	if firstPrompt != "" {
		entry.Title = firstPrompt
	}
	entry = normalizeSessionIndexEntry(entry)
	return SessionSnapshot{
		ID:            sessionID,
		Title:         entry.Title,
		FirstPrompt:   entry.FirstPrompt,
		CreatedAt:     entry.CreatedAt,
		UpdatedAt:     entry.UpdatedAt,
		ContextTokens: estimateTokensFromMessages(history),
		HasSnapshot:   false,
		Messages:      messages,
	}
}

func readCompressedSessionSnapshot(path string) (SessionSnapshot, error) {
	file, err := os.Open(path)
	if err != nil {
		return SessionSnapshot{}, err
	}
	defer file.Close()
	reader, err := gzip.NewReader(file)
	if err != nil {
		return SessionSnapshot{}, err
	}
	defer reader.Close()
	data, err := io.ReadAll(io.LimitReader(reader, maxSessionSnapshotJSONBytes+1))
	if err != nil {
		return SessionSnapshot{}, err
	}
	if len(data) > maxSessionSnapshotJSONBytes {
		return SessionSnapshot{}, fmt.Errorf("session snapshot exceeds %d bytes", maxSessionSnapshotJSONBytes)
	}
	var snapshot SessionSnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return SessionSnapshot{}, err
	}
	return snapshot, nil
}

func writeCompressedJSONAtomic(path string, data []byte) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("session snapshot path is empty")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".session-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	writer := gzip.NewWriter(tmp)
	if _, err := writer.Write(data); err != nil {
		_ = writer.Close()
		_ = tmp.Close()
		return err
	}
	if err := writer.Close(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := replaceSessionFile(tmpPath, path); err != nil {
		return err
	}
	committed = true
	return nil
}

func writeAtomicBytes(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".index-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := replaceSessionFile(tmpPath, path); err != nil {
		return err
	}
	committed = true
	return nil
}

func replaceSessionFile(source, destination string) error {
	return atomicReplaceFile(source, destination)
}

// ── Saved history persistence ─────────────────────────────────

func (a *App) historyDiskPaths(sessionID string) []string {
	safeName := url.PathEscape(sessionID)
	return []string{
		filepath.Join(a.historiesDir, safeName+".json.gz"),
		filepath.Join(a.historiesDir, safeName+".json"),
	}
}

func (a *App) saveHistory(sessionID string, messages []openai.ChatCompletionMessage) {
	if sessionID == "" {
		return
	}
	// In-memory history keeps image MultiContent so subsequent runs in this
	// process still see the images (stable prefix, prompt cache survives).
	// The legacy disk profile remains text-only. A bounded, OS-protected
	// provider payload can preserve full Responses context in the same file.
	// SSH passwords stay in the history verbatim (no redaction): the live history
	// is what the next request is built from, so masking a stored password here
	// would make the model re-send the mask for the next host instead of the real
	// credential.
	inMemory := sanitizeHistoryMessages(messages)
	if countToolMessages(inMemory) < countToolMessages(messages) {
		// The repair pass dropped tool results (dangling or duplicated calls), so
		// the payloads they carried left the model's history: the read cache must
		// stop telling the model it already has them.
		a.invalidateSessionReadCache(sessionID)
	}
	// Resolve the session's provider measurement against the stored conversation
	// so a footer poll between runs reports the measured request size instead of
	// a fresh text estimate. No lock is held here: the anchor lookup takes a.mu.
	breakdown := computeLiveBreakdown(inMemory)
	a.finalizeSessionBreakdown(sessionID, &breakdown, inMemory)
	a.mu.Lock()
	a.historyCacheStateLocked(sessionID).revision++
	a.histories[sessionID] = cloneChatMessages(inMemory)
	a.liveBreakdown[sessionID] = breakdown
	a.mu.Unlock()

	if a.historiesDir == "" {
		return
	}
	disk := sanitizeHistoryMessagesForDisk(inMemory)
	paths := a.historyDiskPaths(sessionID)
	if err := writeCompressedHistoryPayload(paths[0], a.modelHistoryForStorage(sessionID, disk, inMemory)); err != nil {
		log.Printf("saveHistory: failed to write %s: %v", paths[0], err)
		return
	}
	if err := os.Remove(paths[1]); err != nil && !errors.Is(err, os.ErrNotExist) {
		log.Printf("saveHistory: failed to remove legacy %s: %v", paths[1], err)
	}
}

// countToolMessages counts role=tool messages: they are the only carriers of read
// payloads, so a decrease across a history rewrite means content the model could
// previously see is gone.
func countToolMessages(messages []openai.ChatCompletionMessage) int {
	n := 0
	for i := range messages {
		if messages[i].Role == openai.ChatMessageRoleTool {
			n++
		}
	}
	return n
}

func (a *App) restoreSavedHistoryBreakdown(sessionID string) {
	if sessionID == "" {
		return
	}
	history, state, revision := a.sessionHistorySnapshot(sessionID)
	breakdown := computeLiveBreakdown(history)
	a.finalizeSessionBreakdown(sessionID, &breakdown, history)
	a.mu.Lock()
	defer a.mu.Unlock()
	if state == nil || a.historyCacheStates[sessionID] != state || state.revision != revision {
		return
	}
	if len(history) == 0 {
		delete(a.liveBreakdown, sessionID)
		return
	}
	if a.liveBreakdown == nil {
		a.liveBreakdown = map[string]ContextBreakdown{}
	}
	a.liveBreakdown[sessionID] = breakdown
}

func writeCompressedHistoryPayload(diskPath string, payload any) error {
	tmp, err := os.CreateTemp(filepath.Dir(diskPath), ".history-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	zw := gzip.NewWriter(tmp)
	encodeErr := json.NewEncoder(zw).Encode(payload)
	closeGzipErr := zw.Close()
	closeFileErr := tmp.Close()
	if encodeErr != nil {
		return encodeErr
	}
	if closeGzipErr != nil {
		return closeGzipErr
	}
	if closeFileErr != nil {
		return closeFileErr
	}
	if err := atomicReplaceFile(tmpPath, diskPath); err != nil {
		return err
	}
	committed = true
	return nil
}

// A per-session load ticket and revision. Release drops the ticket, so an
// older disk load cannot resurrect a released/deleted session.
type historyCacheState struct {
	revision          uint64
	loading           chan struct{}
	breakdown         *ContextBreakdown
	breakdownRevision uint64
}

// Caller holds a.mu.
func (a *App) historyCacheStateLocked(sessionID string) *historyCacheState {
	if a.historyCacheStates == nil {
		a.historyCacheStates = map[string]*historyCacheState{}
	}
	state := a.historyCacheStates[sessionID]
	if state == nil {
		state = &historyCacheState{}
		a.historyCacheStates[sessionID] = state
	}
	return state
}

func (a *App) sessionHistorySnapshot(sessionID string) ([]openai.ChatCompletionMessage, *historyCacheState, uint64) {
	if sessionID == "" {
		return nil, nil, 0
	}
	for {
		a.mu.Lock()
		state := a.historyCacheStateLocked(sessionID)
		if history, ok := a.histories[sessionID]; ok {
			snapshot := cloneChatMessages(history)
			revision := state.revision
			a.mu.Unlock()
			return snapshot, state, revision
		}
		if state.loading != nil {
			ready := state.loading
			a.mu.Unlock()
			<-ready
			a.mu.Lock()
			current := a.historyCacheStates[sessionID] == state
			a.mu.Unlock()
			if !current {
				return nil, nil, 0
			}
			continue
		}
		ready := make(chan struct{})
		state.loading = ready
		revision := state.revision
		a.mu.Unlock()

		history, restoreReplay := a.readHistoryFromDisk(sessionID)
		a.mu.Lock()
		current := a.historyCacheStates[sessionID] == state
		if current && state.revision == revision {
			if a.histories == nil {
				a.histories = map[string][]openai.ChatCompletionMessage{}
			}
			a.invalidateSessionReadCacheLocked(sessionID)
			a.histories[sessionID] = history
			if restoreReplay != nil {
				restoreReplay()
			}
		}
		state.loading = nil
		close(ready)
		a.mu.Unlock()
		if !current {
			return nil, nil, 0
		}
	}
}

// Saved-history estimates are separate from the live run cache: a footer
// preview must not replace a running agent's authoritative message snapshot.
func (a *App) savedHistoryBreakdown(sessionID string) ContextBreakdown {
	a.mu.Lock()
	state := a.historyCacheStates[sessionID]
	if state != nil && state.breakdown != nil && state.breakdownRevision == state.revision {
		result := *state.breakdown
		a.mu.Unlock()
		return result
	}
	a.mu.Unlock()
	history, state, revision := a.sessionHistorySnapshot(sessionID)
	result := computeLiveBreakdown(history)
	a.finalizeSessionBreakdown(sessionID, &result, history)
	a.mu.Lock()
	if state != nil && a.historyCacheStates[sessionID] == state && state.revision == revision {
		state.breakdown = &result
		state.breakdownRevision = revision
	}
	a.mu.Unlock()
	return result
}

func (a *App) readHistoryFromDisk(sessionID string) ([]openai.ChatCompletionMessage, func()) {
	if a.historiesDir == "" {
		return nil, nil
	}
	paths := a.historyDiskPaths(sessionID)
	var messages []openai.ChatCompletionMessage
	loaded := false
	var restoreReplay func()
	for index, diskPath := range paths {
		file, err := os.Open(diskPath)
		if err != nil {
			continue
		}
		var source io.Reader = file
		var zr *gzip.Reader
		if index == 0 {
			zr, err = gzip.NewReader(file)
			if err != nil {
				_ = file.Close()
				continue
			}
			source = zr
		}
		data, readErr := io.ReadAll(source)
		if zr != nil {
			_ = zr.Close()
		}
		_ = file.Close()
		if readErr != nil || json.Unmarshal(data, &messages) != nil {
			continue
		}
		messages, restoreReplay = a.modelHistoryFromStorage(sessionID, data, messages)
		loaded = true
		break
	}
	if !loaded {
		return nil, nil
	}
	return sanitizeHistoryMessages(messages), restoreReplay
}

// TruncateSessionHistoryRequest identifies which user turn to keep as the last
// one in the saved model history. The frontend sends the target user message's
// content along with its 0-based index among user turns.
type TruncateSessionHistoryRequest struct {
	SessionID        string `json:"sessionId"`
	UserMessageIndex int    `json:"userMessageIndex"`
	ExpectedContent  string `json:"expectedContent"`
}

// TruncateSessionHistory cuts the saved model history (memory + disk) so the
// target user turn (inclusive) and everything after it are dropped. UI snapshot
// persistence is handled by the frontend via SaveSession; this method only
// rewrites the model-side history that buildMessages() feeds to the provider.
func (a *App) TruncateSessionHistory(req TruncateSessionHistoryRequest) (int, error) {
	if err := a.ensureInitialized(); err != nil {
		return 0, err
	}
	sessionID := strings.TrimSpace(req.SessionID)
	if sessionID == "" {
		return 0, errors.New("session id is required")
	}
	if req.UserMessageIndex < 0 {
		return 0, errors.New("user message index must be non-negative")
	}

	// A compaction rewrites the same history on its own goroutine, so a truncate
	// running concurrently would be overwritten by the compaction result —
	// silently restoring the turns the user just deleted.
	if a.compactSessionRunning(sessionID) {
		return 0, codedToolError("E_SESSION_COMPACTING", errors.New("session is compacting; retry the deletion after the compaction finishes"))
	}

	// Load and decompress without holding the App mutex.
	messages := a.loadSessionHistoryCopy(sessionID)
	if len(messages) == 0 {
		return 0, errors.New("session has no saved history")
	}

	// Locate the target user turn. Prefer content matching over the index
	// because the frontend's session.messages may include locally injected
	// user turns (e.g. /init, /note) that the backend history reorders or
	// drops, so the user-turn ordinal is not a reliable locator. With content
	// we find the LAST user turn containing it — that is the one the user is
	// hovering (the most recent occurrence).
	targetIndex := -1
	expected := strings.TrimSpace(req.ExpectedContent)
	expectedLen := len(expected)
	if expectedLen > 0 {
		for i, m := range messages {
			if m.Role != openai.ChatMessageRoleUser {
				continue
			}
			content := strings.TrimSpace(textFromMessage(m))
			if strings.Contains(content, expected) {
				targetIndex = i
			}
		}
		if targetIndex < 0 {
			return 0, fmt.Errorf("no user message contains expected content")
		}
	} else {
		// Fallback: locate by ordinal among user turns.
		userSeen := 0
		for i, m := range messages {
			if m.Role != openai.ChatMessageRoleUser {
				continue
			}
			if userSeen != req.UserMessageIndex {
				userSeen++
				continue
			}
			targetIndex = i
			break
		}
		if targetIndex < 0 {
			return 0, fmt.Errorf("user message index %d not found in history", req.UserMessageIndex)
		}
	}

	// Keep everything strictly before the target user turn. No rewinding is
	// needed on top of that: the only way such a prefix can be protocol-invalid
	// is a trailing assistant message whose tool_calls were never answered, and
	// repairDanglingToolCalls is the single authority on that invariant (saved
	// and loaded histories already go through it). The old hand-written rewind
	// ("a tool message precedes the cut, so back up to the user turn that opened
	// that round") dropped that entire earlier round, and emptied the history
	// when the target was the second user turn — which happens whenever the user
	// message was queued while a run was working or the run was interrupted
	// between a tool result and the next user turn.
	truncated := repairDanglingToolCalls(messages[:targetIndex])

	// The discarded turns may have left provider reasoning artifacts (thinking
	// signatures, encrypted reasoning) in the replay stash; they no longer belong
	// to any message of the history.
	a.reasoningStash.clearSession(sessionID)
	// The conversation was rewritten: the recorded provider measurement covered
	// turns that no longer exist.
	a.clearContextAnchor(sessionID)
	a.saveHistory(sessionID, truncated)
	// The dropped turns may have been the model's only copy of a read payload;
	// content that is no longer in the history has to be sent again.
	a.invalidateSessionReadCache(sessionID)
	return len(truncated), nil
}

// textFromMessage returns the model-facing text of a stored message, handling
// multi-content form used by attachment-bearing messages.
func textFromMessage(m openai.ChatCompletionMessage) string {
	if len(m.MultiContent) > 0 {
		return textFromMultiContent(m.MultiContent)
	}
	return m.Content
}
