// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
// Package read holds the read tool's pure helpers: bounded text reads, line
// ending normalization, line splitting, SHA-256 / Crockford Base32 version
// tokens, and atomic file writes via temp-sibling rename.
//
// Nothing here may depend on App state, ConfigState, or any *App receiver.
// Errors that need a stable tool error code (E_BINARY_FILE, E_FILE_TOO_LARGE,
// E_VERSION_REQUIRED, E_BAD_VERSION, E_EXISTS) are wrapped using the shared
// internal/tools/shared package so callers can extract codes uniformly.
package read

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	toolerrors "ally-dev/internal/tools/shared"
)

// MaxReadBytes caps how much a single ReadTextFile call will read. Mirrors the
// historical app.maxReadFileBytes constant so callers can reference it by name.
const MaxReadBytes = 32 * 1024 * 1024

// ReadTextFile reads a text file at path, enforcing size and binary guards.
// UTF-8 is returned as-is; UTF-16 LE/BE (with or without a BOM) is transcoded
// to UTF-8 so the caller always works with UTF-8 bytes. Returns the bytes,
// file info, and an error. Error codes:
//   - E_IS_DIRECTORY: path is a directory
//   - E_FILE_TOO_LARGE: file exceeds MaxReadBytes
//   - E_BINARY_FILE: file contains NUL bytes, has a UTF-32 BOM, or is
//     malformed/non-text UTF-16
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
	data, err = DecodeTextBytes(data)
	if err != nil {
		return nil, nil, err
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

// DecodeTextBytes decodes raw file bytes into UTF-8 text bytes. UTF-16 LE/BE
// input (with or without a BOM) is transcoded to UTF-8; UTF-8 input is
// returned unchanged. It is the single text-encoding boundary shared by the
// local and remote read pipelines, so both accept the same encodings and
// reject the same binaries. Input without a recognizable BOM is probed by
// NUL-byte parity: ASCII text in UTF-16 keeps NULs consistently on one side
// of each 16-bit unit, while real binary data spreads them evenly. Error
// codes:
//   - E_BINARY_FILE: data contains NUL bytes, starts with a UTF-32 BOM, has
//     odd UTF-16 byte length, or decodes to content that is not text-like
//   - E_NOT_UTF8: data is not valid UTF-8 after decoding
func DecodeTextBytes(data []byte) ([]byte, error) {
	if len(data) >= 4 {
		if data[0] == 0xFF && data[1] == 0xFE && data[2] == 0x00 && data[3] == 0x00 {
			return nil, toolerrors.New("E_BINARY_FILE", errors.New("UTF-32 files are not supported"))
		}
		if data[0] == 0x00 && data[1] == 0x00 && data[2] == 0xFE && data[3] == 0xFF {
			return nil, toolerrors.New("E_BINARY_FILE", errors.New("UTF-32 files are not supported"))
		}
	}
	if len(data) >= 2 {
		if data[0] == 0xFF && data[1] == 0xFE {
			return decodeUTF16(data[2:], true)
		}
		if data[0] == 0xFE && data[1] == 0xFF {
			return decodeUTF16(data[2:], false)
		}
	}
	if le, be := probeUTF16(data); le || be {
		return decodeUTF16(data, le)
	}
	if hasNUL(data) {
		return nil, toolerrors.New("E_BINARY_FILE", errors.New("binary file is not supported"))
	}
	if !utf8.Valid(data) {
		return nil, toolerrors.New("E_NOT_UTF8", errors.New("file is not valid UTF-8"))
	}
	return data, nil
}

// probeUTF16 reports whether data's NUL bytes are strongly biased toward one
// side of each 16-bit unit, which indicates BOM-less UTF-16 text (LE when the
// low byte is nonzero for ASCII, BE when the high byte is). A 4:1 skew
// threshold keeps uniform binary NULs from being misread as UTF-16, and at
// least two NUL bytes are required so a lone NUL in otherwise-UTF-8 content
// is not silently re-paired into garbage.
func probeUTF16(data []byte) (le, be bool) {
	n := len(data) / 2 * 2
	if n < 4 {
		return false, false
	}
	even, odd := 0, 0
	for i := 0; i < n; i += 2 {
		if data[i] == 0 {
			even++
		}
		if data[i+1] == 0 {
			odd++
		}
	}
	if even+odd < 2 {
		return false, false
	}
	if odd > even*4 {
		return true, false // UTF-16LE: ASCII occupies the low byte
	}
	if even > odd*4 {
		return false, true // UTF-16BE: ASCII occupies the high byte
	}
	return false, false
}

// decodeUTF16 transcodes UTF-16 code units (already stripped of any BOM) to
// UTF-8. Odd byte length is rejected (E_BINARY_FILE) instead of silently
// dropping a byte. Unpaired surrogates decode as U+FFFD. The decoded result
// must still look like text: binary whose 16-bit pairs happen to skew NULs
// (e.g. PCM samples) otherwise slips past the post-decode NUL guard.
func decodeUTF16(data []byte, littleEndian bool) ([]byte, error) {
	if len(data)%2 != 0 {
		return nil, toolerrors.New("E_BINARY_FILE", errors.New("malformed UTF-16: odd byte length"))
	}
	units := make([]uint16, len(data)/2)
	for i := 0; i < len(data); i += 2 {
		if littleEndian {
			units[i/2] = uint16(data[i]) | uint16(data[i+1])<<8
		} else {
			units[i/2] = uint16(data[i])<<8 | uint16(data[i+1])
		}
	}
	decoded := string(utf16.Decode(units))
	if !plausibleText(decoded) {
		return nil, toolerrors.New("E_BINARY_FILE", errors.New("decoded content is not text"))
	}
	return []byte(decoded), nil
}

// plausibleText reports whether s looks like text rather than binary: control
// characters other than tab, LF, CR, and FF must stay at or below 10% of all
// runes. This stops NUL-skewed or BOM-prefixed binary that decodes to control
// bytes from passing the binary guard as garbage text. Content that decodes
// to printable characters (e.g. 16-bit PCM sampled values) is inherently
// indistinguishable from UTF-16 text and is accepted.
func plausibleText(s string) bool {
	controls := 0
	for _, r := range s {
		if r < 0x20 && r != '\t' && r != '\n' && r != '\r' && r != 0x0C {
			controls++
		}
	}
	if controls == 0 {
		return true
	}
	total := utf8.RuneCountInString(s)
	return total > 0 && controls*10 <= total
}

// NormalizeText normalizes CRLF/CR to LF, strips a leading UTF-8 BOM, and
// reports the original dominant line ending ("LF" or "CRLF") plus whether a
// BOM was present so callers can preserve both on write.
//
// The BOM is stripped so read previews and edit matching never expose the
// invisible U+FEFF prefix to the model (the model cannot copy it into
// oldText). Writes must use EncodeText to restore it.
func NormalizeText(data []byte) (string, string, bool) {
	s := string(data)
	ending := "LF"
	if strings.Contains(s, "\r\n") {
		ending = "CRLF"
	}
	hadBOM := false
	if strings.HasPrefix(s, "\uFEFF") {
		// TrimPrefix removes the full 3-byte UTF-8 sequence; byte slicing
		// (s[1:]) would leave the trailing 0xBB 0xBF bytes behind.
		s = strings.TrimPrefix(s, "\uFEFF")
		hadBOM = true
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return s, ending, hadBOM
}

// EncodeText re-encodes LF to the requested ending ("LF" or "CRLF") and
// re-prepends the UTF-8 BOM when the original had one. Used when writing
// edited or created files back to disk so round-trips preserve byte shape.
func EncodeText(text, ending string, hadBOM bool) []byte {
	if hadBOM {
		text = "\uFEFF" + text
	}
	return EncodeLineEnding(text, ending)
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
