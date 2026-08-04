package app

// Section 6: Read (was read.go + read_bridge.go)
// App-owned read orchestration that binds internal/tools/read text extraction
// to workspace path resolution, parallel batch reads, and the bounded-preview
// result shape used by the chat loop.

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"ally-dev/internal/tools/read"
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
	maxReadRangeLines          = 10000
	changedLineMaxOutputLines  = 12
	changedLineTextBudgetBytes = 50 * 1024
)

type readPreviewResult struct {
	Content       string
	RawContent    string
	TotalLines    int
	StartLine     int
	EndLine       int
	NextStartLine int
	Truncated     bool
	RangeStatus   string
	EmptyRange    bool
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
		Path:          result.Path,
		Content:       content,
		Text:          result.Text,
		Kind:          result.Kind,
		ContentFormat: contentFormat,
		Type:          result.Type,
		Editable:      result.Editable,
		StartLine:     result.StartLine,
		EndLine:       result.EndLine,
		NextStartLine: result.NextStartLine,
		Version:       result.Version,
		Size:          result.Size,
		TotalLines:    result.TotalLines,
		LineEnding:    result.LineEnding,
		Truncated:     result.Truncated,
		RangeStatus:   result.RangeStatus,
		EmptyRange:    result.EmptyRange,
		Sheets:        result.Sheets,
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

func (a *App) readFileWithConfig(cfg ConfigState, req ReadFileRequest) (ReadFileResult, error) {
	if shouldExtractDocumentInRead(req.Path) {
		return a.readDocumentAsReadFileWithConfig(cfg, req)
	}
	path, err := resolveReadPath(cfg, req.Path)
	if err != nil {
		return ReadFileResult{}, err
	}
	data, info, err := readTextFile(path)
	if err != nil {
		return ReadFileResult{}, err
	}
	text, ending := normalizeText(data)
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
	return ReadFileResult{
		Path:          displayPathForConfig(cfg, path),
		Content:       preview.Content,
		RawContent:    preview.RawContent,
		Kind:          "text",
		ContentFormat: "line_numbers",
		Editable:      true,
		StartLine:     preview.StartLine,
		EndLine:       preview.EndLine,
		NextStartLine: preview.NextStartLine,
		TotalLines:    preview.TotalLines,
		SHA256:        hashBytes(data),
		Version:       hashVersion(data),
		Size:          info.Size(),
		LineEnding:    ending,
		Truncated:     preview.Truncated,
		RangeStatus:   preview.RangeStatus,
		EmptyRange:    preview.EmptyRange,
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
	if startLine <= 0 {
		startLine = 1
	}
	if req.EndLine > 0 && req.EndLine < startLine {
		// Models occasionally reverse an explicit range. Normalize it before
		// the EOF check so 100..20 on a 50-line file safely becomes 20..50.
		req.EndLine, startLine = startLine, req.EndLine
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
	if req.EndLine > 0 {
		baseEnd = req.EndLine
	} else if req.LineCount > 0 {
		baseEnd = startLine + req.LineCount - 1
	} else if req.ContextBefore > 0 || req.ContextAfter > 0 {
		baseEnd = startLine
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

	rangeStartOffset := lineStartOffset(content, start)
	lineOffset := rangeStartOffset
	completeRawEnd := rangeStartOffset
	var b strings.Builder
	if budgetBytes > 0 {
		b.Grow(min(budgetBytes, len(content)-rangeStartOffset))
	}
	actualEnd := start - 1
	budgetLimited := false
	partialFirstLine := false
	rawContent := ""
	for lineNum := start; lineNum <= end; lineNum++ {
		lineEnd := len(content)
		nextOffset := len(content)
		if rel := strings.IndexByte(content[lineOffset:], '\n'); rel >= 0 {
			lineEnd = lineOffset + rel
			nextOffset = lineEnd + 1
		}
		prefix := strconv.Itoa(lineNum) + ": "
		separatorBytes := 0
		if b.Len() > 0 {
			separatorBytes = 1
		}
		needed := separatorBytes + len(prefix) + lineEnd - lineOffset
		if budgetBytes > 0 && b.Len()+needed > budgetBytes {
			budgetLimited = true
			if b.Len() == 0 {
				remaining := budgetBytes
				if len(prefix) > remaining {
					b.WriteString(prefix[:remaining])
					remaining = 0
				} else {
					b.WriteString(prefix)
					remaining -= len(prefix)
				}
				linePreview := utf8Prefix(content[lineOffset:lineEnd], remaining)
				b.WriteString(linePreview)
				rawContent = utf8Prefix(content[lineOffset:lineEnd], budgetBytes)
				partialFirstLine = len(rawContent) < lineEnd-lineOffset
				actualEnd = start
			}
			break
		}
		if separatorBytes != 0 {
			b.WriteByte('\n')
		}
		b.WriteString(prefix)
		b.WriteString(content[lineOffset:lineEnd])
		actualEnd = lineNum
		completeRawEnd = nextOffset
		lineOffset = nextOffset
	}
	result := b.String()
	if !partialFirstLine && actualEnd >= start {
		rawContent = content[rangeStartOffset:completeRawEnd]
	}

	nextStartLine := 0
	requestedFullFile := req.EndLine == 0 && req.LineCount == 0 && req.ContextBefore == 0 && req.ContextAfter == 0
	pagedRequest := req.LineCount > 0
	if actualEnd < total && (budgetLimited || rangeLimited || pagedRequest || requestedFullFile) {
		nextStartLine = actualEnd + 1
		result += fmt.Sprintf("\n\n[Showing lines %d-%d of %d. Use startLine=%d to continue.]", start, actualEnd, total, nextStartLine)
	}

	status := "ok"
	if budgetLimited || rangeLimited {
		status = "truncated"
	}
	return readPreviewResult{
		Content:       result,
		RawContent:    rawContent,
		TotalLines:    total,
		StartLine:     start,
		EndLine:       actualEnd,
		NextStartLine: nextStartLine,
		Truncated:     nextStartLine > 0 || budgetLimited || rangeLimited,
		RangeStatus:   status,
	}, nil
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
