package app

import (
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	GrillMode     bool     `json:"grillMode,omitempty"`
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
	GrillMode     bool             `json:"grillMode,omitempty"`
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
					GrillMode:   entry.GrillMode,
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
			snapshot.GrillMode = entry.GrillMode
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
		GrillMode:   snapshot.GrillMode,
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
		GrillMode:     snapshot.GrillMode,
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
