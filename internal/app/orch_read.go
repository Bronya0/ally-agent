package app

// Section 6: Read (was read.go + read_bridge.go)
// App-owned read orchestration that binds internal/tools/read text extraction
// to workspace path resolution, parallel batch reads, and the bounded-preview
// result shape used by the chat loop.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
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

	"ally-dev/internal/tools/read"
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

// runReadCache avoids sending the same read payload to the model more than once
// during a chat run. It is deliberately run-scoped: later user turns may need a
// fresh version after external changes. A successful write or command clears it.
type runReadCache struct {
	mu        sync.Mutex
	entries   map[string]runReadCacheEntry
	totalSize int
}

type runReadCacheEntry struct {
	result *BatchReadResult
	size   int
}

const (
	runReadCacheMaxEntries = 32
	runReadCacheMaxBytes   = 8 * 1024 * 1024
)

func newRunReadCache() *runReadCache {
	return &runReadCache{entries: make(map[string]runReadCacheEntry)}
}

func (c *runReadCache) read(a *App, cfg ConfigState, req BatchReadRequest) (*BatchReadResult, error) {
	keyBytes, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	key := string(keyBytes)
	c.mu.Lock()
	if previous, ok := c.entries[key]; ok {
		c.mu.Unlock()
		return reusedBatchReadResult(previous.result), nil
	}
	c.mu.Unlock()
	// Read outside the lock: batch reads are already parallelized internally,
	// and holding the cache mutex across disk I/O would serialize unrelated
	// reads within the same run.
	result, err := a.batchReadFilesWithConfig(cfg, req)
	if err != nil {
		return nil, err
	}
	// Only cache fully successful reads: a missing path can appear later in the
	// same run, and a retryable read error should not be hidden.
	for _, file := range result.Files {
		if file.Error != "" {
			return result, nil
		}
	}
	c.store(key, result)
	return result, nil
}

// store caches a fully successful read, evicting arbitrary entries until the
// entry-count and byte budgets fit. The cache is a best-effort token saver, so
// dropping entries under pressure is acceptable. DataURL bytes are counted
// toward the byte budget so a batch of large images cannot silently blow past
// runReadCacheMaxBytes.
func (c *runReadCache) store(key string, result *BatchReadResult) {
	size := 0
	for _, file := range result.Files {
		size += len(file.Content) + len(file.Text) + len(file.DataURL)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.entries[key]; ok {
		return
	}
	for (len(c.entries) >= runReadCacheMaxEntries || c.totalSize+size > runReadCacheMaxBytes) && len(c.entries) > 0 {
		for k, e := range c.entries {
			delete(c.entries, k)
			c.totalSize -= e.size
			break
		}
	}
	c.entries[key] = runReadCacheEntry{result: result, size: size}
	c.totalSize += size
}

func (c *runReadCache) invalidate() {
	c.mu.Lock()
	clear(c.entries)
	c.totalSize = 0
	c.mu.Unlock()
}

func invalidateRunReadCache(ctx context.Context) {
	if cache, ok := ctx.Value(runReadCacheContextKey{}).(*runReadCache); ok {
		cache.invalidate()
	}
}

func reusedBatchReadResult(previous *BatchReadResult) *BatchReadResult {
	files := make([]BatchReadResultItem, len(previous.Files))
	for i, file := range previous.Files {
		file.Content = ""
		file.Text = ""
		// Images were already injected into the model context on the first
		// read; clearing DataURL prevents a second injection for the same
		// cached payload.
		file.DataURL = ""
		file.Reused = true
		files[i] = file
	}
	return &BatchReadResult{Files: files}
}

func (a *App) BatchReadFiles(req BatchReadRequest) (*BatchReadResult, error) {
	return a.batchReadFilesWithConfig(a.effectiveConfig(ConfigState{}), req)
}

func (a *App) batchReadFilesWithConfig(cfg ConfigState, req BatchReadRequest) (*BatchReadResult, error) {
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
		Sheet     string
		MaxChars  int
	}

	// Deduplicate only truly identical effective read requests.
	seen := map[batchReadKey]bool{}
	readKey := func(path string, readReq ReadFileRequest) batchReadKey {
		return batchReadKey{
			Path:      filepath.ToSlash(filepath.Clean(path)),
			StartLine: readReq.StartLine,
			EndLine:   readReq.EndLine,
			Sheet:     readReq.Sheet,
			MaxChars:  readReq.MaxChars,
		}
	}
	addIfNotSeen := func(key batchReadKey) bool {
		if seen[key] {
			return false
		}
		seen[key] = true
		return true
	}

	// Collect (path, fileReq) pairs in request order, then execute in
	// parallel. Parallel reads are safe: read is purely read-only,
	// does not touch fileOpsMu, and each file's result is written to its
	// own slot in a pre-allocated results slice — no cross-file sharing.
	// The previous serial loop serialized N file opens + reads; with 20
	// files on a slow disk this was the dominant per-read cost.
	type pendingRead struct {
		path string
		req  ReadFileRequest
	}
	pending := make([]pendingRead, 0, pathCount)
	if strings.TrimSpace(req.Path) != "" {
		fileReq := ReadFileRequest{
			Path:      req.Path,
			StartLine: req.StartLine,
			EndLine:   req.EndLine,
			Sheet:     req.Sheet,
			MaxChars:  req.MaxChars,
		}
		if addIfNotSeen(readKey(req.Path, fileReq)) {
			pending = append(pending, pendingRead{path: req.Path, req: fileReq})
		}
	}
	for _, p := range req.Paths {
		fileReq := ReadFileRequest{
			Path:      p,
			StartLine: req.StartLine,
			EndLine:   req.EndLine,
			Sheet:     req.Sheet,
			MaxChars:  req.MaxChars,
		}
		if addIfNotSeen(readKey(p, fileReq)) {
			pending = append(pending, pendingRead{path: p, req: fileReq})
		}
	}
	for _, file := range req.Files {
		fileReq := ReadFileRequest{
			Path:      file.Path,
			StartLine: file.StartLine,
			EndLine:   file.EndLine,
			Sheet:     file.Sheet,
			MaxChars:  file.MaxChars,
		}
		if fileReq.StartLine == 0 {
			fileReq.StartLine = req.StartLine
		}
		if fileReq.EndLine == 0 {
			fileReq.EndLine = req.EndLine
		}
		if fileReq.Sheet == "" {
			fileReq.Sheet = req.Sheet
		}
		if fileReq.MaxChars == 0 {
			fileReq.MaxChars = req.MaxChars
		}
		if addIfNotSeen(readKey(file.Path, fileReq)) {
			pending = append(pending, pendingRead{path: file.Path, req: fileReq})
		}
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
		return BatchReadResultItem{Path: path, Error: readErr.Error(), ErrorCode: batchReadErrorCode(readErr)}
	}
	content := result.Content
	contentFormat := result.ContentFormat
	if result.Kind == "document" {
		content = result.Content
		contentFormat = "plain"
	}
	return BatchReadResultItem{
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
		Sheets:                result.Sheets,
		DataURL:               result.DataURL,
	}
}

// ── Document Read ────────────────────────────────────────

func (a *App) readDocumentWithConfig(cfg ConfigState, req DocumentReadRequest) (DocumentReadResult, error) {
	if strings.TrimSpace(req.Path) == "" {
		return DocumentReadResult{}, errors.New("path is required")
	}
	fullPath, err := resolveReadPath(cfg, req.Path)
	if err != nil {
		return DocumentReadResult{}, err
	}
	info, err := os.Stat(fullPath)
	if err != nil {
		return DocumentReadResult{}, err
	}
	if info.IsDir() {
		return DocumentReadResult{}, codedToolError("E_IS_DIRECTORY", errors.New("path is a directory"))
	}
	maxChars := req.MaxChars
	if maxChars <= 0 {
		maxChars = 60000
	}
	if maxChars > 200000 {
		maxChars = 200000
	}
	ext := strings.ToLower(filepath.Ext(fullPath))
	var text string
	var sheets []string
	switch ext {
	case ".docx":
		text, err = read.ExtractDocxText(fullPath)
	case ".pptx":
		text, err = read.ExtractPptxText(fullPath)
	case ".xlsx":
		text, sheets, err = read.ExtractXlsxText(fullPath, req.Sheet)
	case ".pdf":
		text, err = read.ExtractPDFTextBestEffort(fullPath)
	case ".txt", ".md", ".json", ".csv", ".log":
		data, readErr := os.ReadFile(fullPath)
		if readErr != nil {
			err = readErr
		} else if !utf8.Valid(data) {
			err = errors.New("file is not valid UTF-8")
		} else {
			text = string(data)
		}
	default:
		return DocumentReadResult{}, fmt.Errorf("unsupported document type: %s", ext)
	}
	if err != nil {
		return DocumentReadResult{}, err
	}
	truncated := false
	if len(text) > maxChars {
		text = text[:maxChars]
		truncated = true
	}
	return DocumentReadResult{
		Path:      displayPathForConfig(cfg, fullPath),
		Type:      strings.TrimPrefix(ext, "."),
		Text:      text,
		Sheets:    sheets,
		Truncated: truncated,
	}, nil
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
	data, err := os.ReadFile(path)
	if err != nil {
		return ReadFileResult{}, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return ReadFileResult{}, err
	}
	name := filepath.Base(path)
	notice := fmt.Sprintf("[Image: %s (%s, %d bytes)]", name, strings.ToUpper(strings.TrimPrefix(mime, "image/")), len(data))
	var dataURL string
	switch {
	case !injectableImageMime(mime):
		notice += " (format not supported for image input)"
	case len(data) > maxReadImageBytes:
		notice += " (too large to send as image input)"
	default:
		dataURL = imageDataURL(mime, data)
	}
	return ReadFileResult{
		Path:          displayPathForConfig(cfg, path),
		Content:       notice,
		Text:          notice,
		Kind:          "image",
		ContentFormat: "image",
		Type:          strings.TrimPrefix(mime, "image/"),
		Editable:      false,
		SHA256:        hashBytes(data),
		Version:       hashVersion(data),
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

// stripImageInjectionMessages removes synthesized image-input messages from the
// slice in place (order preserved).
func stripImageInjectionMessages(messages []openai.ChatCompletionMessage) []openai.ChatCompletionMessage {
	out := messages[:0]
	for _, m := range messages {
		if isImageInjectionMessage(&m) {
			continue
		}
		out = append(out, m)
	}
	return out
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
	case "read", "batch_read", "read_file":
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
	if shouldExtractDocumentInRead(req.Path) {
		return a.readDocumentAsReadFileWithConfig(cfg, req)
	}
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

func shouldExtractDocumentInRead(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".docx", ".pptx", ".xlsx", ".pdf":
		return true
	default:
		return false
	}
}

func (a *App) readDocumentAsReadFileWithConfig(cfg ConfigState, req ReadFileRequest) (ReadFileResult, error) {
	doc, err := a.readDocumentWithConfig(cfg, DocumentReadRequest{
		Path:     req.Path,
		Sheet:    req.Sheet,
		MaxChars: req.MaxChars,
	})
	if err != nil {
		return ReadFileResult{}, err
	}
	fullPath, err := resolveReadPath(cfg, req.Path)
	if err != nil {
		return ReadFileResult{}, err
	}
	info, err := os.Stat(fullPath)
	if err != nil {
		return ReadFileResult{}, err
	}
	sha, err := hashFileSHA256(fullPath)
	if err != nil {
		return ReadFileResult{}, err
	}
	version, err := versionFromSHA256Hex(sha)
	if err != nil {
		return ReadFileResult{}, err
	}
	totalLines := countPlainTextLines(doc.Text)
	return ReadFileResult{
		Path:          doc.Path,
		Content:       doc.Text,
		RawContent:    doc.Text,
		Text:          doc.Text,
		Kind:          "document",
		ContentFormat: "plain",
		Type:          doc.Type,
		Editable:      false,
		StartLine:     1,
		EndLine:       totalLines,
		TotalLines:    totalLines,
		SHA256:        sha,
		Version:       version,
		Size:          info.Size(),
		Truncated:     doc.Truncated,
		RangeStatus:   "document",
		Sheets:        doc.Sheets,
	}, nil
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
			Content:     "File is empty. Use create_file with overwrite=true to write content.",
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

func formatNumberedLine(lineNum int, line string, width int) string {
	return strconv.Itoa(lineNum) + ": " + line
}
