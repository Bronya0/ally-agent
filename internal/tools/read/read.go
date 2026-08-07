// Package read holds the read tool's pure helpers: bounded text reads, line
// ending normalization, line splitting, SHA-256 / Crockford Base32 version
// tokens, atomic file writes via temp-sibling rename, and document text
// extraction (.docx/.pptx/.xlsx/.pdf).
//
// Nothing here may depend on App state, ConfigState, or any *App receiver.
// Errors that need a stable tool error code (E_BINARY_FILE, E_FILE_TOO_LARGE,
// E_VERSION_REQUIRED, E_BAD_VERSION, E_EXISTS) are wrapped using the shared
// internal/tools/shared package so callers can extract codes uniformly.
package read

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	toolerrors "ally-dev/internal/tools/shared"
)

// MaxReadBytes caps how much a single ReadTextFile call will read. Mirrors the
// historical app.maxReadFileBytes constant so callers can reference it by name.
const MaxReadBytes = 10 * 1024 * 1024

// ReadTextFile reads a UTF-8 text file at path, enforcing size and binary
// guards. Returns the raw bytes, file info, and an error. Error codes:
//   - E_IS_DIRECTORY: path is a directory
//   - E_FILE_TOO_LARGE: file exceeds MaxReadBytes
//   - E_BINARY_FILE: file contains NUL bytes
//   - E_NOT_UTF8: file is not valid UTF-8
func ReadTextFile(path string) ([]byte, os.FileInfo, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, nil, err
	}
	if info.IsDir() {
		return nil, nil, toolerrors.New("E_IS_DIRECTORY", errors.New("path is a directory"))
	}
	if info.Size() > MaxReadBytes {
		return nil, nil, toolerrors.Newf("E_FILE_TOO_LARGE", "file is too large: %d bytes", info.Size())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	if hasNUL(data) {
		return nil, nil, toolerrors.New("E_BINARY_FILE", errors.New("binary file is not supported"))
	}
	if !utf8.Valid(data) {
		return nil, nil, toolerrors.New("E_NOT_UTF8", errors.New("file is not valid UTF-8"))
	}
	return data, info, nil
}

func hasNUL(data []byte) bool {
	for _, b := range data {
		if b == 0 {
			return true
		}
	}
	return false
}

// NormalizeText normalizes CRLF/CR to LF and reports the original dominant
// line ending ("LF" or "CRLF") so callers can preserve it on write.
func NormalizeText(data []byte) (string, string) {
	s := string(data)
	ending := "LF"
	if strings.Contains(s, "\r\n") {
		ending = "CRLF"
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return s, ending
}

// EncodeLineEnding re-encodes LF to the requested ending ("LF" or "CRLF"),
// first normalizing any stray CR. Used when writing files back to disk.
func EncodeLineEnding(text, ending string) []byte {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	if ending == "CRLF" {
		text = strings.ReplaceAll(text, "\n", "\r\n")
	}
	return []byte(text)
}

// SplitLines splits text on \n and reports whether it ended with a trailing
// newline. An empty input returns ([], false). A trailing newline is not
// represented as an extra empty element.
func SplitLines(text string) ([]string, bool) {
	trailing := strings.HasSuffix(text, "\n")
	lines := strings.Split(text, "\n")
	if trailing && len(lines) > 0 {
		lines = lines[:len(lines)-1]
	}
	if len(lines) == 1 && lines[0] == "" && !trailing {
		return []string{}, false
	}
	return lines, trailing
}

// HashBytes returns the lowercase hex SHA-256 digest of data.
func HashBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// HashVersion returns the 6-character Crockford Base32 version token derived
// from the SHA-256 of data. Used as the optimistic-concurrency token for
// model-facing read/edit operations.
func HashVersion(data []byte) string {
	sum := sha256.Sum256(data)
	return VersionFromSHA256(sum[:])
}

// HashBytesAndVersion computes SHA-256 once and returns both the lowercase hex
// digest and the 6-character Crockford Base32 version token. Use this when a
// caller needs both values for the same data — calling HashBytes + HashVersion
// separately would hash the data twice, which is a measurable cost on the read
// hot path (10 MB file ≈ 20-30 ms per SHA-256 pass).
func HashBytesAndVersion(data []byte) (sha256Hex, version string) {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), VersionFromSHA256(sum[:])
}

// VersionFromSHA256 derives the 6-character Crockford Base32 version token
// from a raw 32-byte SHA-256 digest. Callers that already have the digest
// (e.g. they hex-decoded it from an external source) can use this instead of
// recomputing HashVersion.
func VersionFromSHA256(sum []byte) string {
	const alphabet = "0123456789abcdefghjkmnpqrstvwxyz"
	var out [6]byte
	for i := range out {
		bit := i * 5
		byteIndex := bit / 8
		shift := 11 - (bit % 8)
		value := uint16(sum[byteIndex]) << 8
		if byteIndex+1 < len(sum) {
			value |= uint16(sum[byteIndex+1])
		}
		out[i] = alphabet[(value>>shift)&31]
	}
	return string(out[:])
}

// ValidateVersion returns a coded error if version is empty or not a valid
// 6-character Crockford Base32 token. Error codes:
//   - E_VERSION_REQUIRED: empty/whitespace version
//   - E_BAD_VERSION: wrong length or non-Base32 characters
func ValidateVersion(version string) error {
	if strings.TrimSpace(version) == "" {
		return toolerrors.New("E_VERSION_REQUIRED", errors.New("version is required; read the file with read and pass its version"))
	}
	if !IsValidVersion(version) {
		return toolerrors.New("E_BAD_VERSION", errors.New("version must be exactly 6 Crockford Base32 characters"))
	}
	return nil
}

// IsValidVersion reports whether value is exactly 6 Crockford Base32
// characters (case-insensitive).
func IsValidVersion(value string) bool {
	if len(value) != 6 {
		return false
	}
	const alphabet = "0123456789abcdefghjkmnpqrstvwxyz"
	for _, c := range strings.ToLower(value) {
		if !strings.ContainsRune(alphabet, c) {
			return false
		}
	}
	return true
}

// IsSHA256Hex reports whether value is a 64-character lowercase/uppercase hex
// string.
func IsSHA256Hex(value string) bool {
	if len(value) != 64 {
		return false
	}
	for i := 0; i < len(value); i++ {
		c := value[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
			return false
		}
	}
	return true
}

// EvalExistingPrefix resolves the longest existing prefix of target through
// symlinks and returns the fully resolved path. Useful when target may not
// exist yet but its parent chain contains symlinks.
func EvalExistingPrefix(target string) (string, error) {
	clean := filepath.Clean(target)
	existing := clean
	for {
		if _, err := os.Lstat(existing); err == nil {
			resolved, err := filepath.EvalSymlinks(existing)
			if err != nil {
				return "", err
			}
			rel, err := filepath.Rel(existing, clean)
			if err != nil {
				return "", err
			}
			if rel == "." {
				return filepath.Clean(resolved), nil
			}
			return filepath.Clean(filepath.Join(resolved, rel)), nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			return "", os.ErrNotExist
		}
		existing = parent
	}
}

// SafeWriteFile writes data to path atomically via a temp sibling and rename,
// creating parent directories as needed. perm is applied to the temp file; 0
// defaults to 0o644.
func SafeWriteFile(path string, data []byte, perm os.FileMode) error {
	return SafeWriteFileWithDir(path, data, perm, true)
}

// SafeWriteFileWithDir is like SafeWriteFile but lets the caller opt out of
// parent-directory creation via mkdirs=false.
func SafeWriteFileWithDir(path string, data []byte, perm os.FileMode, mkdirs bool) error {
	if mkdirs {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
	}
	return SafeWritePreparedFile(path, data, perm)
}

// SafeWriteNewFile writes data to path only if path does not already exist.
// Returns E_EXISTS (coded) if the destination is present.
func SafeWriteNewFile(path string, data []byte, perm os.FileMode) error {
	tmpName, err := writeTempSibling(path, data, perm, false)
	if err != nil {
		return err
	}
	defer os.Remove(tmpName)
	if err := os.Link(tmpName, path); err != nil {
		if errors.Is(err, os.ErrExist) {
			return toolerrors.New("E_EXISTS", fmt.Errorf("file already exists: %s", path))
		}
		return err
	}
	return nil
}

// SafeWritePreparedFile overwrites path atomically via temp sibling + rename.
// Caller is responsible for parent directory existence.
func SafeWritePreparedFile(path string, data []byte, perm os.FileMode) error {
	tmpName, err := writeTempSibling(path, data, perm, false)
	if err != nil {
		return err
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	cleanup = false
	return nil
}

func writeTempSibling(path string, data []byte, perm os.FileMode, mkdirs bool) (string, error) {
	if mkdirs {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return "", err
		}
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".agent-write-*")
	if err != nil {
		return "", err
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()
	if perm == 0 {
		perm = 0o644
	}
	_ = tmp.Chmod(perm)
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return "", err
	}
	_ = tmp.Sync()
	if err := tmp.Close(); err != nil {
		return "", err
	}
	cleanup = false
	return tmpName, nil
}

// ModeOf returns the permission bits of path, or 0o644 if it cannot be
// stat'd. Used by edits that want to preserve the existing file mode.
func ModeOf(path string) os.FileMode {
	info, err := os.Stat(path)
	if err != nil {
		return 0o644
	}
	return info.Mode().Perm()
}

// --- Document text extraction (.docx/.pptx/.xlsx/.pdf) ---

// ExtractDocxText extracts readable text from a .docx file.
func ExtractDocxText(filePath string) (string, error) {
	return extractZipXMLText(filePath, func(name string) bool {
		return name == "word/document.xml" || strings.HasPrefix(name, "word/header") || strings.HasPrefix(name, "word/footer")
	})
}

// ExtractPptxText extracts readable text from a .pptx file.
func ExtractPptxText(filePath string) (string, error) {
	return extractZipXMLText(filePath, func(name string) bool {
		return strings.HasPrefix(name, "ppt/slides/slide") && strings.HasSuffix(name, ".xml")
	})
}

func extractZipXMLText(filePath string, include func(string) bool) (string, error) {
	zr, err := zip.OpenReader(filePath)
	if err != nil {
		return "", err
	}
	defer zr.Close()
	files := append([]*zip.File(nil), zr.File...)
	sort.Slice(files, func(i, j int) bool { return files[i].Name < files[j].Name })
	var b strings.Builder
	for _, f := range files {
		if !include(f.Name) {
			continue
		}
		part, err := extractOOXMLTextPart(f)
		if err != nil {
			return "", err
		}
		part = strings.TrimSpace(part)
		if part != "" {
			if b.Len() > 0 {
				b.WriteString("\n\n")
			}
			b.WriteString(part)
		}
	}
	if b.Len() == 0 {
		return "", errors.New("no readable text found")
	}
	return b.String(), nil
}

func extractOOXMLTextPart(f *zip.File) (string, error) {
	rc, err := f.Open()
	if err != nil {
		return "", err
	}
	defer rc.Close()
	dec := xml.NewDecoder(rc)
	var b strings.Builder
	var inText bool
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "t":
				inText = true
			case "p":
				if b.Len() > 0 && !strings.HasSuffix(b.String(), "\n") {
					b.WriteByte('\n')
				}
			}
		case xml.EndElement:
			if t.Name.Local == "t" {
				inText = false
			}
		case xml.CharData:
			if inText {
				b.Write([]byte(t))
			}
		}
	}
	return CompactDocumentText(b.String()), nil
}

// ExtractXlsxText extracts text from a .xlsx file. If sheetSelector is empty,
// all sheets are extracted; otherwise it selects by 1-based index or name.
// Returns the extracted text and the list of sheet names.
func ExtractXlsxText(filePath, sheetSelector string) (string, []string, error) {
	zr, err := zip.OpenReader(filePath)
	if err != nil {
		return "", nil, err
	}
	defer zr.Close()
	shared, _ := readSharedStrings(zr.File)
	sheetFiles := []*zip.File{}
	for _, f := range zr.File {
		if strings.HasPrefix(f.Name, "xl/worksheets/sheet") && strings.HasSuffix(f.Name, ".xml") {
			sheetFiles = append(sheetFiles, f)
		}
	}
	sort.Slice(sheetFiles, func(i, j int) bool { return sheetFiles[i].Name < sheetFiles[j].Name })
	if len(sheetFiles) == 0 {
		return "", nil, errors.New("no worksheets found")
	}
	sheetNames := make([]string, len(sheetFiles))
	for i := range sheetFiles {
		sheetNames[i] = fmt.Sprintf("Sheet%d", i+1)
	}
	selected := -1
	if strings.TrimSpace(sheetSelector) != "" {
		if n, convErr := strconv.Atoi(strings.TrimSpace(sheetSelector)); convErr == nil && n >= 1 && n <= len(sheetFiles) {
			selected = n - 1
		} else {
			for i, name := range sheetNames {
				if strings.EqualFold(name, strings.TrimSpace(sheetSelector)) {
					selected = i
					break
				}
			}
		}
		if selected < 0 {
			return "", sheetNames, fmt.Errorf("sheet not found: %s", sheetSelector)
		}
	}
	var b strings.Builder
	for i, f := range sheetFiles {
		if selected >= 0 && selected != i {
			continue
		}
		rows, err := readWorksheetRows(f, shared)
		if err != nil {
			return "", sheetNames, err
		}
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString("## ")
		b.WriteString(sheetNames[i])
		b.WriteByte('\n')
		for _, row := range rows {
			b.WriteString(strings.Join(row, "\t"))
			b.WriteByte('\n')
		}
	}
	return CompactDocumentText(b.String()), sheetNames, nil
}

func readSharedStrings(files []*zip.File) ([]string, error) {
	for _, f := range files {
		if f.Name != "xl/sharedStrings.xml" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		defer rc.Close()
		dec := xml.NewDecoder(rc)
		var result []string
		var b strings.Builder
		var inText bool
		for {
			tok, err := dec.Token()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return nil, err
			}
			switch t := tok.(type) {
			case xml.StartElement:
				if t.Name.Local == "si" {
					b.Reset()
				}
				if t.Name.Local == "t" {
					inText = true
				}
			case xml.EndElement:
				if t.Name.Local == "t" {
					inText = false
				}
				if t.Name.Local == "si" {
					result = append(result, b.String())
				}
			case xml.CharData:
				if inText {
					b.Write([]byte(t))
				}
			}
		}
		return result, nil
	}
	return nil, nil
}

func readWorksheetRows(f *zip.File, shared []string) ([][]string, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	dec := xml.NewDecoder(rc)
	var rows [][]string
	var current []string
	var cellType string
	var inValue bool
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "row":
				current = []string{}
			case "c":
				cellType = ""
				for _, attr := range t.Attr {
					if attr.Name.Local == "t" {
						cellType = attr.Value
						break
					}
				}
			case "v", "t":
				inValue = true
			}
		case xml.EndElement:
			if t.Name.Local == "row" && len(current) > 0 {
				rows = append(rows, current)
			}
			if t.Name.Local == "v" || t.Name.Local == "t" {
				inValue = false
			}
		case xml.CharData:
			if inValue {
				value := string([]byte(t))
				if cellType == "s" {
					if idx, convErr := strconv.Atoi(value); convErr == nil && idx >= 0 && idx < len(shared) {
						value = shared[idx]
					}
				}
				current = append(current, value)
			}
		}
	}
	return rows, nil
}

// ExtractPDFTextBestEffort performs a best-effort plain-text extraction from a
// PDF file. It only handles plain-text PDFs; scanned or compressed PDFs may
// need OCR and will return an error.
func ExtractPDFTextBestEffort(filePath string) (string, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return "", err
	}
	if len(data) > 8*1024*1024 {
		data = data[:8*1024*1024]
	}
	re := regexp.MustCompile(`\((?:\\.|[^\\)])*\)`)
	matches := re.FindAll(data, 20000)
	var parts []string
	for _, m := range matches {
		if len(m) < 2 {
			continue
		}
		s := string(m[1 : len(m)-1])
		s = strings.NewReplacer(`\(`, "(", `\)`, ")", `\\`, `\`, `\n`, "\n", `\r`, "\n", `\t`, "\t").Replace(s)
		s = strings.TrimSpace(s)
		if s != "" {
			parts = append(parts, s)
		}
	}
	if len(parts) == 0 {
		return "", errors.New("no readable PDF text found; scanned or compressed PDFs may need OCR")
	}
	return CompactDocumentText(strings.Join(parts, " ")), nil
}

// CompactDocumentText collapses runs of whitespace within each line and drops
// empty lines.
func CompactDocumentText(text string) string {
	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.Join(strings.Fields(line), " ")
		if line != "" {
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}
