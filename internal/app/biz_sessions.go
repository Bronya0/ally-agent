// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package app

import (
	"compress/gzip"
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
	maxSessionIndexJSONBytes    = 2 * 1024 * 1024
	maxSessionSnapshotJSONBytes = 16 * 1024 * 1024
	maxSessionIndexTitleChars   = 160
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

// ListSessions returns only the local session index. It never loads all UI
// message snapshots into the frontend.
func (a *App) ListSessions() ([]SessionIndexEntry, error) {
	if err := a.ensureInitialized(); err != nil {
		return nil, err
	}
	if a.sessionsDir == "" {
		return []SessionIndexEntry{}, nil
	}

	a.sessionMu.Lock()
	defer a.sessionMu.Unlock()

	entries, err := a.readSessionIndexLocked()
	if err != nil {
		return nil, err
	}
	changed := false
	known := make(map[string]struct{}, len(entries))
	entryIndexes := make(map[string]int, len(entries))
	for index, entry := range entries {
		known[entry.ID] = struct{}{}
		entryIndexes[entry.ID] = index
	}

	for _, sessionID := range a.diskSnapshotSessionIDs() {
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
		history := a.loadSessionHistoryCopy(entry.ID)
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

	entries = normalizeSessionIndexEntries(entries)
	if changed {
		if err := a.writeSessionIndexLocked(entries); err != nil {
			return nil, err
		}
	}
	return entries, nil
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
	if len(data) > maxSessionIndexJSONBytes {
		return nil, fmt.Errorf("session index exceeds %d bytes", maxSessionIndexJSONBytes)
	}
	var entries []SessionIndexEntry
	if len(strings.TrimSpace(string(data))) == 0 {
		return []SessionIndexEntry{}, nil
	}
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, err
	}
	return normalizeSessionIndexEntries(entries), nil
}

func (a *App) writeSessionIndexLocked(entries []SessionIndexEntry) error {
	path := a.sessionIndexPath()
	if path == "" {
		return nil
	}
	entries = normalizeSessionIndexEntries(entries)
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomicBytes(path, data, 0o600)
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
	if strings.TrimSpace(entry.FirstPrompt) != "" {
		entry.FirstPrompt = normalizeSessionIndexTitle(entry.FirstPrompt)
	} else {
		entry.FirstPrompt = ""
	}
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
	history := a.loadSessionHistoryCopy(sessionID)
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
	if err := os.Rename(source, destination); err == nil {
		return nil
	}
	backup := destination + ".bak"
	_ = os.Remove(backup)
	if err := os.Rename(destination, backup); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Rename(source, destination); err != nil {
		_ = os.Rename(backup, destination)
		return err
	}
	_ = os.Remove(backup)
	return nil
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
	filtered := trimSavedHistory(sanitizeHistoryMessages(messages))
	breakdown := computeLiveBreakdown(filtered)
	a.mu.Lock()
	a.histories[sessionID] = cloneChatMessages(filtered)
	a.liveBreakdown[sessionID] = breakdown
	a.mu.Unlock()

	if a.historiesDir == "" {
		return
	}
	paths := a.historyDiskPaths(sessionID)
	if err := writeCompressedHistory(paths[0], filtered); err != nil {
		log.Printf("saveHistory: failed to write %s: %v", paths[0], err)
		return
	}
	if err := os.Remove(paths[1]); err != nil && !errors.Is(err, os.ErrNotExist) {
		log.Printf("saveHistory: failed to remove legacy %s: %v", paths[1], err)
	}
}

func (a *App) restoreSavedHistoryBreakdown(sessionID string) {
	if sessionID == "" {
		return
	}
	a.mu.Lock()
	history := cloneChatMessages(a.histories[sessionID])
	if history == nil {
		history = cloneChatMessages(a.loadHistoryLocked(sessionID))
	}
	if len(history) == 0 {
		delete(a.liveBreakdown, sessionID)
	} else {
		a.liveBreakdown[sessionID] = computeLiveBreakdown(history)
	}
	a.mu.Unlock()
}

func writeCompressedHistory(diskPath string, messages []openai.ChatCompletionMessage) error {
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
	encodeErr := json.NewEncoder(zw).Encode(messages)
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
	if err := os.Rename(tmpPath, diskPath); err != nil {
		// Windows may reject replacing an existing destination. Move the old
		// valid file aside, install the completed temp, and roll back on failure.
		backupPath := diskPath + ".bak"
		_ = os.Remove(backupPath)
		if backupErr := os.Rename(diskPath, backupPath); backupErr != nil {
			return err
		}
		if retryErr := os.Rename(tmpPath, diskPath); retryErr != nil {
			_ = os.Rename(backupPath, diskPath)
			return retryErr
		}
		_ = os.Remove(backupPath)
	}
	committed = true
	return nil
}

func (a *App) loadHistoryLocked(sessionID string) []openai.ChatCompletionMessage {
	if a.historiesDir == "" {
		return nil
	}
	paths := a.historyDiskPaths(sessionID)
	var messages []openai.ChatCompletionMessage
	loaded := false
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
		data, readErr := io.ReadAll(io.LimitReader(source, maxSavedHistoryJSONBytes+1))
		if zr != nil {
			_ = zr.Close()
		}
		_ = file.Close()
		if readErr != nil || len(data) > maxSavedHistoryJSONBytes || json.Unmarshal(data, &messages) != nil {
			continue
		}
		loaded = true
		break
	}
	if !loaded {
		return nil
	}
	messages = trimSavedHistory(sanitizeHistoryMessages(messages))
	a.histories[sessionID] = cloneChatMessages(messages)
	return messages
}

func historyMessageTokens(message openai.ChatCompletionMessage) int {
	tokens := estimateMessageBodyTokens(message)
	for _, call := range message.ToolCalls {
		tokens += estimateTokensFromText(call.Function.Name)
		tokens += estimateTokensFromText(call.Function.Arguments)
	}
	return tokens
}

func trimSavedHistory(messages []openai.ChatCompletionMessage) []openai.ChatCompletionMessage {
	if len(messages) == 0 {
		return nil
	}
	total := 0
	for _, message := range messages {
		total += historyMessageTokens(message)
	}
	if total <= maxSavedHistoryTokens {
		return messages
	}

	// Start only at a user message so an assistant tool call and all of its
	// tool results remain an intact model-protocol sequence. If the newest turn
	// alone exceeds the budget, keep it whole rather than creating orphans.
	running := 0
	start := len(messages)
	lastUser := -1
	for index := len(messages) - 1; index >= 0; index-- {
		running += historyMessageTokens(messages[index])
		if messages[index].Role != openai.ChatMessageRoleUser {
			continue
		}
		lastUser = index
		if running <= maxSavedHistoryTokens {
			start = index
			continue
		}
		break
	}
	if start == len(messages) {
		if lastUser >= 0 {
			start = lastUser
		} else {
			return messages
		}
	}
	return messages[start:]
}

func sanitizeHistoryMessages(messages []openai.ChatCompletionMessage) []openai.ChatCompletionMessage {
	filtered := make([]openai.ChatCompletionMessage, 0, len(messages))
	for _, original := range messages {
		// Synthesized image-input messages are transient: drop them so saved
		// history never carries "images were provided" text without the actual
		// images (the base64 payloads are not persisted).
		if isImageInjectionMessage(&original) || original.Role == openai.ChatMessageRoleSystem {
			continue
		}
		m := original
		if len(m.MultiContent) > 0 {
			m.Content = textFromMultiContent(m.MultiContent)
			m.MultiContent = nil
		}
		if strings.TrimSpace(m.Content) == "" && len(m.ToolCalls) == 0 && m.Role != openai.ChatMessageRoleTool {
			continue
		}
		m.ToolCalls = append([]openai.ToolCall(nil), m.ToolCalls...)
		// Histories written before the truncation repair may still carry a
		// tool call whose arguments were cut off mid-stream, or a function
		// name repeated N times by a relay that re-sent the name in every
		// streaming delta, or a name that is the concatenation of two
		// different tool names produced by a relay that sent multiple
		// tool_calls with the same Index; providers that parse tool_calls
		// server-side would keep rejecting every request for the session,
		// so repair them on load as well.
		for i := range m.ToolCalls {
			m.ToolCalls[i].Function.Name = collapseRepeatedName(m.ToolCalls[i].Function.Name)
			m.ToolCalls[i].Function.Arguments = repairTruncatedToolCallArguments(m.ToolCalls[i].Function.Arguments)
		}
		// 丢弃拼接工具名的 tool_call 和截断参数标记的 tool_call：
		// 两者都不会通过服务商校验，repairDanglingToolCalls 会自动清除
		// 对应的孤儿 tool 结果消息。
		if len(m.ToolCalls) > 0 {
			kept := make([]openai.ToolCall, 0, len(m.ToolCalls))
			for _, call := range m.ToolCalls {
				if isConcatenatedKnownToolNames(call.Function.Name) {
					continue
				}
				if isTruncatedArgsMarker([]byte(call.Function.Arguments)) {
					continue
				}
				kept = append(kept, call)
			}
			m.ToolCalls = kept
		}
		filtered = append(filtered, m)
	}
	return repairDanglingToolCalls(filtered)
}

// repairDanglingToolCalls enforces the tool-call pairing invariant: every
// assistant tool_call must be followed by exactly one tool message carrying
// its ID, and every tool message must answer a still-pending tool_call.
// Histories written when a run panicked between appending the assistant
// tool_calls message and appending its tool results (or by older builds that
// hit the same window) otherwise poison the session permanently — providers
// reject every request with 400 because the dangling tool_calls can never be
// answered. Unanswered tool_calls are stripped (the assistant text is kept);
// orphan and duplicate tool messages are dropped.
func repairDanglingToolCalls(messages []openai.ChatCompletionMessage) []openai.ChatCompletionMessage {
	out := make([]openai.ChatCompletionMessage, 0, len(messages))
	// pending maps toolCallID -> index in out of the assistant message that
	// declared it. An entry is removed once its tool result arrives.
	pending := make(map[string]int)
	// closeTurn strips every still-pending (never answered) tool_call from
	// the assistant message that declared it. It runs whenever the current
	// assistant turn ends: the next assistant/user message, or the end of the
	// history.
	closeTurn := func() {
		if len(pending) == 0 {
			return
		}
		dangling := make(map[string]bool, len(pending))
		for id := range pending {
			dangling[id] = true
		}
		byIndex := make(map[int]bool)
		for _, idx := range pending {
			byIndex[idx] = true
		}
		for idx := range byIndex {
			kept := make([]openai.ToolCall, 0, len(out[idx].ToolCalls))
			for _, call := range out[idx].ToolCalls {
				if !dangling[call.ID] {
					kept = append(kept, call)
				}
			}
			updated := out[idx]
			updated.ToolCalls = kept
			out[idx] = updated
		}
		pending = make(map[string]int)
	}
	for _, m := range messages {
		switch m.Role {
		case openai.ChatMessageRoleAssistant:
			closeTurn()
			out = append(out, m)
			for _, call := range m.ToolCalls {
				pending[call.ID] = len(out) - 1
			}
		case openai.ChatMessageRoleTool:
			if _, answered := pending[m.ToolCallID]; !answered {
				// Orphan tool result (no pending call, or a duplicate result
				// for an already-answered call) — providers reject these too.
				continue
			}
			delete(pending, m.ToolCallID)
			out = append(out, m)
		default:
			closeTurn()
			out = append(out, m)
		}
	}
	closeTurn()
	// An assistant message whose tool_calls all dangled and whose text is
	// empty carries no information; drop it entirely.
	final := make([]openai.ChatCompletionMessage, 0, len(out))
	for _, m := range out {
		if m.Role == openai.ChatMessageRoleAssistant && len(m.ToolCalls) == 0 && strings.TrimSpace(m.Content) == "" {
			continue
		}
		final = append(final, m)
	}
	return final
}

func textFromMultiContent(parts []openai.ChatMessagePart) string {
	var b strings.Builder
	imageCount := 0
	for _, part := range parts {
		switch part.Type {
		case openai.ChatMessagePartTypeText:
			if strings.TrimSpace(part.Text) != "" {
				if b.Len() > 0 {
					b.WriteString("\n")
				}
				b.WriteString(part.Text)
			}
		case openai.ChatMessagePartTypeImageURL:
			imageCount++
		}
	}
	if imageCount > 0 {
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		fmt.Fprintf(&b, "[%d image attachment(s) omitted from saved history]", imageCount)
	}
	return b.String()
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

	// Load the complete history, preferring the in-memory run source and
	// falling back to disk. loadHistoryLocked already holds a.mu internally;
	// we snapshot the messages here so no lock is held while we inspect them.
	a.mu.Lock()
	var messages []openai.ChatCompletionMessage
	if cached, ok := a.histories[sessionID]; ok && len(cached) > 0 {
		messages = cloneChatMessages(cached)
	} else {
		messages = a.loadHistoryLocked(sessionID)
	}
	a.mu.Unlock()
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

	// If the located user turn is the first message in history, there is
	// nothing before it to keep; treat that as "keep everything up to it".
	// Otherwise keep everything strictly before the target user turn. If a
	// tool-call assistant turn immediately precedes it, back up to the user
	// turn that opened that round so the truncated prefix is an intact
	// protocol sequence.

	// Keep everything strictly before the target user turn. If a tool-call
	// assistant turn immediately precedes it, back up to the user turn that
	// opened that round so the truncated prefix is an intact protocol sequence.
	cut := targetIndex
	if cut > 0 && messages[cut-1].Role == openai.ChatMessageRoleTool {
		for j := cut - 1; j >= 0; j-- {
			if messages[j].Role == openai.ChatMessageRoleUser {
				cut = j
				break
			}
		}
	}
	truncated := messages[:cut]

	a.saveHistory(sessionID, truncated)
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

