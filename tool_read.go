package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unicode/utf8"
)

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
		return &BatchReadResult{Files: results}, nil
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
	return &BatchReadResult{Files: results}, nil
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
	content := result.RawContent
	contentFormat := "raw"
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
		return DocumentReadResult{}, errors.New("path is a directory")
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
		text, err = extractDocxText(fullPath)
	case ".pptx":
		text, err = extractPptxText(fullPath)
	case ".xlsx":
		text, sheets, err = extractXlsxText(fullPath, req.Sheet)
	case ".pdf":
		text, err = extractPDFTextBestEffort(fullPath)
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
