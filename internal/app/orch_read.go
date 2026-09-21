// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package app

// Section 6: Read (was read.go + read_bridge.go)
// App-owned read orchestration that binds internal/tools/read text extraction
// to workspace path resolution, parallel batch reads, and the bounded-preview
// result shape used by the chat loop.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	openai "github.com/sashabaranov/go-openai"

	toolshared "ally-dev/internal/tools/shared"
)

// Read-range types shared between app.go's read preview helpers and the
// model-facing read tool dispatcher. They live here (not in internal/tools/read)
// because they describe the app-owned bounded-preview result shape used by the
// chat loop, not the pure file-reading algorithm in internal/tools/read.

type readRangeRequest struct {
	StartLine     int
	EndLine       int
	LineCount     int
	ContextBefore int
	ContextAfter  int
}

const (
	maxReadRangeLines          = toolshared.MaxReadRangeLines
	maxReadLineChars           = toolshared.MaxReadLineChars
	changedLineMaxOutputLines  = 12
	changedLineTextBudgetBytes = 50 * 1024
	maxReportedTruncatedLines  = 256
)

type readPreviewResult struct {
	Content               string
	RawContent            string
	TotalLines            int
	StartLine             int
	EndLine               int
	NextStartLine         int
	Truncated             bool
	TruncatedLines        []int
	TruncatedLinesOmitted bool
	RangeStatus           string
	EmptyRange            bool
}

// runReadCache avoids sending an identical read payload to the model more than
// once during a chat run. It caches the hash of what the model actually received
// for one file range — the returned text plus any injected image data — and omits
// the payload when a later read of the same range would send exactly the same
// bytes, while always reporting the freshly read version token.
//
// Correctness rests on that comparison, not on the key: every read still hits
// disk, so a stale key can only cost a missed de-duplication, never hide changed
// content. invalidateRunReadCache (a command that may rewrite files, a successful
// write/edit) is therefore defence in depth — it forces re-delivery, it is not
// what keeps the model from acting on stale content.
type runReadCache struct {
	mu      sync.Mutex
	entries map[string]string // key (resolvedPath#range) -> payload SHA-256 hex
}

const runReadCacheMaxEntries = 64

func newRunReadCache() *runReadCache {
	return &runReadCache{entries: make(map[string]string)}
}

func fileRangeCacheKey(resolvedPath string, req ReadFileRequest) string {
	cleanPath := filepath.Clean(filepath.ToSlash(resolvedPath))
	return fmt.Sprintf("%s#%d:%d:%d:%d:%d", cleanPath, req.StartLine, req.EndLine, req.LineCount, req.ContextBefore, req.ContextAfter)
}

// hashText hashes the payload parts the model would receive for one read, joined
// with a NUL separator (neither part can contain one). Hashing the text alone
// would treat a replaced image with an identical text notice — same file name and
// byte size — as unchanged and silently drop the new picture.
func hashText(parts ...string) string {
	h := sha256.New()
	for i, part := range parts {
		if i > 0 {
			h.Write([]byte{0})
		}
		h.Write([]byte(part))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// putLocked records one key -> content hash entry, evicting arbitrary entries
// while the cache is at its entry budget (the cache is a best-effort token
// saver, so which entry is dropped does not matter). The caller must already
// hold c.mu: runReadCache.mu is a plain Mutex and NOT reentrant, so putLocked
// must never be called through the locking put from inside a locked region —
// that self-deadlocks.
func (c *runReadCache) putLocked(key string, contentHash string) {
	for len(c.entries) >= runReadCacheMaxEntries {
		for k := range c.entries {
			delete(c.entries, k)
			break
		}
	}
	c.entries[key] = contentHash
}

// put is the locking entry point for callers outside the read path.
func (c *runReadCache) put(key string, contentHash string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.putLocked(key, contentHash)
}

func (c *runReadCache) invalidate() {
	c.mu.Lock()
	clear(c.entries)
	c.mu.Unlock()
}

func invalidateRunReadCache(ctx context.Context) {
	if cache, ok := ctx.Value(runReadCacheContextKey{}).(*runReadCache); ok {
		cache.invalidate()
	}
}

type pendingReadItem struct {
	path string
	req  ReadFileRequest
}

func collectPendingBatchReads(req BatchReadRequest) ([]pendingReadItem, error) {
	pathCount := len(req.Paths) + len(req.Files)
	if strings.TrimSpace(req.Path) != "" {
		pathCount++
	}
	if pathCount == 0 {
		return nil, errors.New("read requires at least one path or file")
	}
	if pathCount > 20 {
		return nil, errors.New("too many files; max 20 per batch")
	}

	type batchReadKey struct {
		Path      string
		StartLine int
		EndLine   int
	}

	seen := map[batchReadKey]bool{}
	readKey := func(path string, readReq ReadFileRequest) batchReadKey {
		return batchReadKey{
			Path:      filepath.ToSlash(filepath.Clean(path)),
			StartLine: readReq.StartLine,
			EndLine:   readReq.EndLine,
		}
	}
	addIfNotSeen := func(key batchReadKey) bool {
		if seen[key] {
			return false
		}
		seen[key] = true
		return true
	}

	pending := make([]pendingReadItem, 0, pathCount)
	if strings.TrimSpace(req.Path) != "" {
		fileReq := ReadFileRequest{
			Path:      req.Path,
			StartLine: req.StartLine,
			EndLine:   req.EndLine,
		}
		if addIfNotSeen(readKey(req.Path, fileReq)) {
			pending = append(pending, pendingReadItem{path: req.Path, req: fileReq})
		}
	}
	for _, p := range req.Paths {
		fileReq := ReadFileRequest{
			Path:      p,
			StartLine: req.StartLine,
			EndLine:   req.EndLine,
		}
		if addIfNotSeen(readKey(p, fileReq)) {
			pending = append(pending, pendingReadItem{path: p, req: fileReq})
		}
	}
	for _, file := range req.Files {
		fileReq := ReadFileRequest{
			Path:      file.Path,
			StartLine: file.StartLine,
			EndLine:   file.EndLine,
		}
		if fileReq.StartLine == 0 {
			fileReq.StartLine = req.StartLine
		}
		if fileReq.EndLine == 0 {
			fileReq.EndLine = req.EndLine
		}
		if addIfNotSeen(readKey(file.Path, fileReq)) {
			pending = append(pending, pendingReadItem{path: file.Path, req: fileReq})
		}
	}
	return pending, nil
}

func (c *runReadCache) read(a *App, cfg ConfigState, req BatchReadRequest) (*BatchReadResult, error) {
	result, err := a.batchReadFilesWithConfig(cfg, req)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	for i := range result.Files {
		item := &result.Files[i]
		if item.Error != "" {
			continue
		}
		resolvedPath, resErr := resolveReadPath(cfg, item.Path)
		if resErr != nil {
			resolvedPath = item.Path
		}
		key := fileRangeCacheKey(resolvedPath, item.Req)
		contentHash := hashText(item.Content, item.DataURL)

		if prevHash, ok := c.entries[key]; ok && prevHash == contentHash {
			item.Content = ""
			item.Text = ""
			item.DataURL = ""
			item.Reused = true
		} else {
			c.putLocked(key, contentHash)
		}
	}
	return result, nil
}

func (a *App) batchReadFilesWithConfig(cfg ConfigState, req BatchReadRequest) (*BatchReadResult, error) {
	pending, err := collectPendingBatchReads(req)
	if err != nil {
		return nil, err
	}

	results := make([]BatchReadResultItem, len(pending))
	if len(pending) <= 1 {
		// Fast path: 0 or 1 file — no goroutine overhead.
		for i, p := range pending {
			results[i] = a.batchReadOneWithConfig(cfg, p.path, p.req)
		}
		return filteredBatchReadResult(results), nil
	}
	// Parallel path: cap concurrency to 4 (matches the non-file tool batch
	// limit in runChat). 20 files / 4 concurrent ≈ 5 rounds; well below the
	// 30s tool timeout budget even on slow disks. Result slot is written by
	// exactly one goroutine per index, so no mutex is needed.
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)
	for i, p := range pending {
		wg.Add(1)
		sem <- struct{}{}
		go func(idx int, path string, fileReq ReadFileRequest) {
			defer wg.Done()
			defer func() { <-sem }()
			results[idx] = a.batchReadOneWithConfig(cfg, path, fileReq)
		}(i, p.path, p.req)
	}
	wg.Wait()
	return filteredBatchReadResult(results), nil
}

// filteredBatchReadResult silently drops paths that cannot represent readable
// files. Models occasionally mix directories or stale/non-existent paths into
// files[]. Keeping them out of the result also keeps those expected misses out
// of the UI, while all meaningful read failures remain visible. Filtering is
// done in place to avoid another result-sized allocation; the unused tail is
// cleared so large successful contents are not retained through duplicate
// slice slots.
func filteredBatchReadResult(results []BatchReadResultItem) *BatchReadResult {
	kept := results[:0]
	for _, result := range results {
		if result.ErrorCode == "E_PATH_NOT_FOUND" || result.ErrorCode == "E_IS_DIRECTORY" {
			continue
		}
		kept = append(kept, result)
	}
	clear(results[len(kept):])
	return &BatchReadResult{Files: kept}
}

func batchReadErrorCode(err error) string {
	if code := toolErrorCode(err); code != "" {
		return code
	}
	if errors.Is(err, fs.ErrNotExist) {
		return "E_PATH_NOT_FOUND"
	}
	return ""
}

func (a *App) batchReadOneWithConfig(cfg ConfigState, path string, req ReadFileRequest) BatchReadResultItem {
	result, readErr := a.readFileWithConfig(cfg, req)
	if readErr != nil {
		return BatchReadResultItem{Req: req, Path: path, Error: readErr.Error(), ErrorCode: batchReadErrorCode(readErr)}
	}
	content := result.Content
	contentFormat := result.ContentFormat
	return BatchReadResultItem{
		Req:                   req,
		Path:                  result.Path,
		Content:               content,
		Text:                  result.Text,
		Kind:                  result.Kind,
		ContentFormat:         contentFormat,
		Type:                  result.Type,
		Editable:              result.Editable,
		StartLine:             result.StartLine,
		EndLine:               result.EndLine,
		NextStartLine:         result.NextStartLine,
		Version:               result.Version,
		Size:                  result.Size,
		TotalLines:            result.TotalLines,
		LineEnding:            result.LineEnding,
		Truncated:             result.Truncated,
		TruncatedLines:        result.TruncatedLines,
		TruncatedLinesOmitted: result.TruncatedLinesOmitted,
		RangeStatus:           result.RangeStatus,
		EmptyRange:            result.EmptyRange,
		DataURL:               result.DataURL,
	}
}

// imageMimeFromHeader detects a supported image type from magic bytes. It
// returns "" when the data is not one of the supported image formats. The
// read tool uses this instead of trusting the file extension so renamed or
// extension-less image files still work.
func imageMimeFromHeader(data []byte) string {
	if len(data) >= 8 && bytes.Equal(data[:8], []byte("\x89PNG\r\n\x1a\n")) {
		return "image/png"
	}
	if len(data) >= 3 && bytes.Equal(data[:3], []byte("\xFF\xD8\xFF")) {
		return "image/jpeg"
	}
	if len(data) >= 6 && (bytes.Equal(data[:6], []byte("GIF87a")) || bytes.Equal(data[:6], []byte("GIF89a"))) {
		return "image/gif"
	}
	if len(data) >= 12 && bytes.Equal(data[:4], []byte("RIFF")) && bytes.Equal(data[8:12], []byte("WEBP")) {
		return "image/webp"
	}
	if len(data) >= 2 && bytes.Equal(data[:2], []byte("BM")) {
		return "image/bmp"
	}
	return ""
}

// imageDataURL builds a data:image/<mime>;base64,... URL for the raw bytes.
// The returned value is empty when the file exceeds maxReadImageBytes so
// oversized images degrade to a text notice instead of blowing up context.
func imageDataURL(mime string, data []byte) string {
	if len(data) > maxReadImageBytes {
		return ""
	}
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)
}

// injectableImageMime reports whether the MIME type can be sent as a
// data:image URL to every supported provider (OpenAI Chat, Responses,
// Anthropic). BMP is deliberately excluded: OpenAI Chat rejects image/bmp
// with a 400, so a BMP read stays a metadata notice with no DataURL.
func injectableImageMime(mime string) bool {
	switch mime {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
		return true
	default:
		return false
	}
}

// readImageWithConfig reads an image file and returns a ReadFileResult whose
// Kind is "image", Content is a short text notice, and DataURL carries the
// base64 data URL for multimodal model input. Non-editable.
func (a *App) readImageWithConfig(cfg ConfigState, path string, req ReadFileRequest, mime string) (ReadFileResult, error) {
	// Stat before reading: os.ReadFile has no size limit, so a huge file whose
	// first bytes look like an image would be pulled into memory in full and
	// then discarded by the maxReadImageBytes guard — one plain read call could
	// take the whole process down. The size also feeds the notice, so an
	// oversized image still reports its real size.
	info, err := os.Stat(path)
	if err != nil {
		return ReadFileResult{}, err
	}
	name := filepath.Base(path)
	notice := fmt.Sprintf("[Image: %s (%s, %d bytes)]", name, strings.ToUpper(strings.TrimPrefix(mime, "image/")), info.Size())
	var dataURL string
	switch {
	case !injectableImageMime(mime):
		notice += " (format not supported for image input)"
	case info.Size() > maxReadImageBytes:
		notice += " (too large to send as image input)"
	default:
		data, err := os.ReadFile(path)
		if err != nil {
			return ReadFileResult{}, err
		}
		dataURL = imageDataURL(mime, data)
	}
	// Hash by streaming: the bytes above are deliberately not kept for an
	// oversized image, and the version token is part of the result contract
	// (same value the in-memory path would produce).
	sha256Hex, version, err := hashFileAndVersion(path)
	if err != nil {
		return ReadFileResult{}, err
	}
	return ReadFileResult{
		Path:          displayPathForConfig(cfg, path),
		Content:       notice,
		Text:          notice,
		Kind:          "image",
		ContentFormat: "image",
		Type:          strings.TrimPrefix(mime, "image/"),
		Editable:      false,
		SHA256:        sha256Hex,
		Version:       version,
		Size:          info.Size(),
		DataURL:       dataURL,
	}, nil
}

// imageInjectionMarker prefixes the text lead of the synthesized image-input
// message so the rest of the pipeline can recognize and strip it:
//   - the main loop / sub-agent remove the previous turn's injection before
//     appending the next one (images are single-turn context, not history);
//   - sanitizeHistoryMessages drops the message entirely so saved history never
//     contains "images were provided" text without the actual images.
//
// The NUL prefix cannot appear in normal model/user text.
const imageInjectionMarker = "\x00ally-image-input\x00"

// isImageInjectionMessage reports whether m is a synthesized image-input
// message created by readImageInjectionMessage.
func isImageInjectionMessage(m *openai.ChatCompletionMessage) bool {
	if m == nil || m.Role != openai.ChatMessageRoleUser || len(m.MultiContent) == 0 {
		return false
	}
	first := m.MultiContent[0]
	return first.Type == openai.ChatMessagePartTypeText && strings.HasPrefix(first.Text, imageInjectionMarker)
}

// readImageInjectionMessage builds a user message that carries image files
// read by the preceding tool batch into multimodal model context. Returns nil
// when the batch contains no readable image DataURLs.
//
// The message is appended right after the tool-result messages, so every
// provider adapter sees the images in a user turn immediately following the
// tool results (Anthropic merges it into the same user turn; OpenAI/Responses
// accept a new user message). The text lead carries imageInjectionMarker so
// the message is recognized as transient (see isImageInjectionMessage).
func readImageInjectionMessage(results []readImageCandidate) *openai.ChatCompletionMessage {
	var parts []openai.ChatMessagePart
	var names []string
	for _, r := range results {
		if r.DataURL == "" {
			continue
		}
		names = append(names, r.Path)
		parts = append(parts, openai.ChatMessagePart{
			Type: openai.ChatMessagePartTypeImageURL,
			ImageURL: &openai.ChatMessageImageURL{
				URL:    r.DataURL,
				Detail: openai.ImageURLDetailAuto,
			},
		})
	}
	if len(parts) == 0 {
		return nil
	}
	lead := imageInjectionMarker + "The following image file(s) were read by the tool call(s) above and are provided as image input:\n" + strings.Join(names, "\n")
	parts = append([]openai.ChatMessagePart{{Type: openai.ChatMessagePartTypeText, Text: lead}}, parts...)
	return &openai.ChatCompletionMessage{Role: openai.ChatMessageRoleUser, MultiContent: parts}
}

// readImageCandidate is one image file to inject into model context.
type readImageCandidate struct {
	Path    string
	DataURL string
}

// collectReadImages extracts image DataURLs from a completed read tool result.
// Non-read tools and read failures contribute nothing.
func collectReadImages(name string, result *toolResult) []readImageCandidate {
	if result == nil || !result.OK || result.Data == nil {
		return nil
	}
	switch name {
	case "read":
	default:
		return nil
	}
	var r BatchReadResult
	if !decodeToolData(result.Data, &r) {
		return nil
	}
	var out []readImageCandidate
	for _, f := range r.Files {
		if f.DataURL != "" {
			out = append(out, readImageCandidate{Path: f.Path, DataURL: f.DataURL})
		}
	}
	return out
}

func (a *App) readFileWithConfig(cfg ConfigState, req ReadFileRequest) (ReadFileResult, error) {
	path, err := resolveReadPath(cfg, req.Path)
	if err != nil {
		return ReadFileResult{}, err
	}
	// Detect images by content before the text read (which rejects binary).
	// Only probe when the extension plausibly matches an image so non-image
	// files keep the single-pass text path. The probe reads only the first 12
	// bytes (enough for every magic number below) so a large fake image (e.g.
	// a 1GB text file named .png) costs one tiny read instead of a full read
	// plus the text read.
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp":
		f, openErr := os.Open(path)
		if openErr != nil {
			return ReadFileResult{}, openErr
		}
		probe := make([]byte, 12)
		n, readErr := f.Read(probe)
		f.Close()
		if readErr != nil && readErr != io.EOF {
			return ReadFileResult{}, readErr
		}
		if mime := imageMimeFromHeader(probe[:n]); mime != "" {
			return a.readImageWithConfig(cfg, path, req, mime)
		}
		// Fall through: not actually an image, treat as text.
	}
	// Office/PDF documents are deliberately not parsed by the read tool. Fail
	// fast with a coded, actionable error so the model converts the file to
	// Markdown with the anydoc skill and reads the converted output instead of
	// receiving a generic E_BINARY_FILE failure.
	if documentExtensionInRead(path) {
		return ReadFileResult{}, codedToolError("E_DOCUMENT_UNSUPPORTED",
			fmt.Errorf("%s is an office/PDF document; read only supports plain text files. Convert it to Markdown first: load the anydoc skill (e.g. npx -y @firecrawl/anydoc %s -o converted.md), then read converted.md", filepath.Base(path), filepath.Base(path)))
	}
	data, info, err := readTextFile(path)
	if err != nil {
		return ReadFileResult{}, err
	}
	text, ending, _ := normalizeText(data)
	preview, err := formatLineNumberReadPreviewRangeWithBudget(text, readRangeRequest{
		StartLine:     req.StartLine,
		EndLine:       req.EndLine,
		LineCount:     req.LineCount,
		ContextBefore: req.ContextBefore,
		ContextAfter:  req.ContextAfter,
	}, maxToolOutput)
	if err != nil {
		return ReadFileResult{}, err
	}
	// Hash once; the previous code called hashBytes + hashVersion separately,
	// hashing the same data twice (10 MB file ≈ 20-30 ms per SHA-256 pass).
	sha256Hex, version := hashBytesAndVersion(data)
	return ReadFileResult{
		Path:                  displayPathForConfig(cfg, path),
		Content:               preview.Content,
		RawContent:            preview.RawContent,
		Kind:                  "text",
		ContentFormat:         "line_numbers",
		Editable:              true,
		StartLine:             preview.StartLine,
		EndLine:               preview.EndLine,
		NextStartLine:         preview.NextStartLine,
		TotalLines:            preview.TotalLines,
		SHA256:                sha256Hex,
		Version:               version,
		Size:                  info.Size(),
		LineEnding:            ending,
		Truncated:             preview.Truncated,
		TruncatedLines:        preview.TruncatedLines,
		TruncatedLinesOmitted: preview.TruncatedLinesOmitted,
		RangeStatus:           preview.RangeStatus,
		EmptyRange:            preview.EmptyRange,
	}, nil
}

// documentExtensionInRead reports whether path looks like an office/PDF
// document the read tool deliberately does not parse. Reading one returns a
// coded E_DOCUMENT_UNSUPPORTED error pointing at the anydoc skill instead of a
// generic binary-file failure.
func documentExtensionInRead(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".docx", ".pptx", ".xlsx", ".pdf":
		return true
	default:
		return false
	}
}

func countPlainTextLines(text string) int {
	if text == "" {
		return 0
	}
	lines := strings.Count(text, "\n") + 1
	if strings.HasSuffix(text, "\n") {
		lines--
	}
	return lines
}

func formatLineNumberReadPreviewRangeWithBudget(content string, req readRangeRequest, budgetBytes int) (readPreviewResult, error) {
	if req.LineCount > 0 && req.EndLine > 0 {
		return readPreviewResult{}, errors.New("lineCount and endLine are mutually exclusive")
	}
	if req.ContextBefore < 0 || req.ContextAfter < 0 {
		return readPreviewResult{}, errors.New("contextBefore/contextAfter must be non-negative")
	}

	tailRequest := req.StartLine < 0
	if tailRequest {
		if req.StartLine < -maxReadRangeLines {
			return readPreviewResult{}, fmt.Errorf("negative startLine must be between -%d and -1", maxReadRangeLines)
		}
		if req.EndLine != 0 || req.LineCount != 0 || req.ContextBefore != 0 || req.ContextAfter != 0 {
			return readPreviewResult{}, errors.New("negative startLine cannot be combined with endLine, lineCount, or context ranges")
		}
	}

	if len(content) == 0 {
		return readPreviewResult{
			Content:     "File is empty. Use create with overwrite=true to write content.",
			TotalLines:  0,
			StartLine:   1,
			EndLine:     0,
			RangeStatus: "empty_file",
			EmptyRange:  true,
		}, nil
	}

	// Count visible lines without strings.Split. A split would allocate one
	// string header per line (about 16 MiB for one million short lines) even
	// when the caller requests only a tiny range near EOF.
	total := countPlainTextLines(content)

	startLine := req.StartLine
	if tailRequest {
		tailCount := -startLine
		startLine = total - tailCount + 1
		if startLine < 1 {
			startLine = 1
		}
	} else {
		if startLine <= 0 {
			startLine = 1
		}
		if req.EndLine > 0 && req.EndLine < startLine {
			// Models occasionally reverse an explicit range. Normalize it before
			// the EOF check so 100..20 on a 50-line file safely becomes 20..50.
			req.EndLine, startLine = startLine, req.EndLine
		}
	}
	if startLine > total {
		return readPreviewResult{
			Content:     fmt.Sprintf("startLine %d is beyond end of file (%d lines total).", startLine, total),
			TotalLines:  total,
			StartLine:   startLine,
			EndLine:     0,
			RangeStatus: "beyond_eof",
			EmptyRange:  true,
		}, nil
	}

	baseEnd := total
	if !tailRequest {
		switch {
		case req.EndLine > 0:
			baseEnd = req.EndLine
		case req.LineCount > 0:
			baseEnd = startLine + req.LineCount - 1
		case req.ContextBefore > 0 || req.ContextAfter > 0:
			baseEnd = startLine
		}
	}

	start := startLine - req.ContextBefore
	if start < 1 {
		start = 1
	}
	end := baseEnd + req.ContextAfter
	if end > total {
		end = total
	}
	if end < start {
		return readPreviewResult{
			Content:     fmt.Sprintf("Requested range %d-%d is empty.", start, end),
			TotalLines:  total,
			StartLine:   start,
			EndLine:     0,
			RangeStatus: "empty_range",
			EmptyRange:  true,
		}, nil
	}

	rangeLimited := false
	if end-start+1 > maxReadRangeLines {
		end = start + maxReadRangeLines - 1
		rangeLimited = true
	}

	var rangeStartOffset int
	if tailRequest {
		rangeStartOffset = lineStartOffsetFromTail(content, total, start)
	} else {
		rangeStartOffset = lineStartOffset(content, start)
	}
	lineOffset := rangeStartOffset
	var numbered strings.Builder
	if budgetBytes > 0 {
		numbered.Grow(min(budgetBytes, len(content)-rangeStartOffset))
	}

	rawBudget := budgetBytes
	if rawBudget <= 0 {
		rawBudget = maxToolOutput
	}
	var raw strings.Builder
	raw.Grow(min(rawBudget, len(content)-rangeStartOffset))
	rawBytes := 0
	appendRaw := func(line string, newline bool) {
		if rawBytes >= rawBudget {
			return
		}
		remaining := rawBudget - rawBytes
		if newline && len(line)+1 <= remaining {
			raw.WriteString(line)
			raw.WriteByte('\n')
			rawBytes += len(line) + 1
			return
		}
		prefix := utf8Prefix(line, remaining)
		raw.WriteString(prefix)
		rawBytes += len(prefix)
	}

	actualEnd := start - 1
	budgetLimited := false
	truncatedLines := make([]int, 0, 2)
	truncatedLinesOmitted := false
	recordTruncatedLine := func(lineNum int) {
		if len(truncatedLines) < maxReportedTruncatedLines {
			truncatedLines = append(truncatedLines, lineNum)
		} else {
			truncatedLinesOmitted = true
		}
	}

	for lineNum := start; lineNum <= end; lineNum++ {
		lineEnd := len(content)
		nextOffset := len(content)
		if rel := strings.IndexByte(content[lineOffset:], '\n'); rel >= 0 {
			lineEnd = lineOffset + rel
			nextOffset = lineEnd + 1
		}
		lineContent := content[lineOffset:lineEnd]
		renderedLine, rawLine, lineWasTruncated := truncateReadLine(lineContent, maxReadLineChars)

		var prefixBuf [12]byte
		prefixStr := strconv.AppendInt(prefixBuf[:0], int64(lineNum), 10)
		prefixLen := len(prefixStr) + 2 // "N" + ": "
		separatorBytes := 0
		if numbered.Len() > 0 {
			separatorBytes = 1
		}
		needed := separatorBytes + prefixLen + len(renderedLine)
		if budgetBytes > 0 && numbered.Len()+needed > budgetBytes {
			budgetLimited = true
			if numbered.Len() == 0 {
				remaining := budgetBytes
				if remaining >= len(prefixStr) {
					numbered.Write(prefixStr)
					remaining -= len(prefixStr)
					if remaining >= 2 {
						numbered.WriteString(": ")
						remaining -= 2
					} else if remaining > 0 {
						numbered.WriteByte(':')
						remaining = 0
					}
				} else if remaining > 0 {
					numbered.Write(prefixStr[:remaining])
					remaining = 0
				}
				numbered.WriteString(utf8Prefix(renderedLine, remaining))
				raw.Reset()
				raw.WriteString(utf8Prefix(rawLine, rawBudget))
				rawBytes = raw.Len()
				actualEnd = start
				if lineWasTruncated {
					recordTruncatedLine(lineNum)
				}
			}
			break
		}

		if separatorBytes != 0 {
			numbered.WriteByte('\n')
		}
		numbered.Write(prefixStr)
		numbered.WriteString(": ")
		numbered.WriteString(renderedLine)
		appendRaw(rawLine, nextOffset > lineEnd)
		actualEnd = lineNum
		lineOffset = nextOffset
		if lineWasTruncated {
			recordTruncatedLine(lineNum)
		}
	}

	result := numbered.String()
	rawContent := raw.String()

	nextStartLine := 0
	requestedFullFile := req.EndLine == 0 && req.LineCount == 0 && req.ContextBefore == 0 && req.ContextAfter == 0 && !tailRequest
	pagedRequest := req.LineCount > 0
	if actualEnd < total && (budgetLimited || rangeLimited || pagedRequest || requestedFullFile) {
		nextStartLine = actualEnd + 1
		result += fmt.Sprintf("\n\n[Showing lines %d-%d of %d. Use startLine=%d to continue.]", start, actualEnd, total, nextStartLine)
	}

	status := "ok"
	if budgetLimited || rangeLimited || len(truncatedLines) > 0 || truncatedLinesOmitted {
		status = "truncated"
	}
	return readPreviewResult{
		Content:               result,
		RawContent:            rawContent,
		TotalLines:            total,
		StartLine:             start,
		EndLine:               actualEnd,
		NextStartLine:         nextStartLine,
		Truncated:             nextStartLine > 0 || budgetLimited || rangeLimited || len(truncatedLines) > 0 || truncatedLinesOmitted,
		TruncatedLines:        truncatedLines,
		TruncatedLinesOmitted: truncatedLinesOmitted,
		RangeStatus:           status,
	}, nil
}

// lineStartOffsetFromTail locates a visible line by scanning backwards. It
// avoids materializing line indexes and makes negative startLine reads cheap for
// files with many lines: one forward count plus one bounded-memory reverse scan.
func lineStartOffsetFromTail(content string, totalLines, startLine int) int {
	if startLine <= 1 {
		return 0
	}
	needed := totalLines - startLine + 1
	if needed <= 0 {
		return len(content)
	}
	index := len(content) - 1
	if index >= 0 && content[index] == '\n' {
		index--
	}
	found := 0
	for ; index >= 0; index-- {
		if content[index] != '\n' {
			continue
		}
		found++
		if found == needed {
			return index + 1
		}
	}
	return 0
}

// truncateReadLine keeps a bounded, UTF-8-valid preview of a single line. The
// scan stops as soon as the line exceeds the cap, so short lines pay only one
// pass and very long lines do not allocate a proportional copy.
func truncateReadLine(line string, maxChars int) (rendered, raw string, truncated bool) {
	if maxChars <= 0 || line == "" {
		return line, line, false
	}
	const marker = "..."
	const markerChars = 3
	keepChars := maxChars - markerChars
	if keepChars <= 0 {
		return marker, "", true
	}
	cutByte := -1
	count := 0
	for index := range line {
		count++
		if count == keepChars+1 {
			cutByte = index
		}
		if count > maxChars {
			if cutByte < 0 {
				return marker, "", true
			}
			return line[:cutByte] + marker, line[:cutByte], true
		}
	}
	return line, line, false
}

// lineStartOffset returns the byte offset of a visible 1-based line. Callers
// validate lineNum against countPlainTextLines first. It performs one linear
// scan and allocates no per-line metadata.
func lineStartOffset(content string, lineNum int) int {
	if lineNum <= 1 {
		return 0
	}
	offset := 0
	for current := 1; current < lineNum; current++ {
		rel := strings.IndexByte(content[offset:], '\n')
		if rel < 0 {
			return len(content)
		}
		offset += rel + 1
	}
	return offset
}

func utf8Prefix(text string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(text) <= maxBytes {
		return text
	}
	cut := maxBytes
	for cut > 0 && !utf8.ValidString(text[:cut]) {
		cut--
	}
	return text[:cut]
}
