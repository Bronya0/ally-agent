// Package command holds pure parsing/analysis helpers for command safety
// inspection: shell redirection target extraction, absolute-path candidate
// detection, and risk-pattern matching. Nothing here may depend on App state,
// ConfigState, *App receivers, or codedToolError — callers feed in a command
// string and receive structured analysis. App-owned orchestration (workspace
// boundary checks, error wrapping with E_* codes) stays in internal/app.
package command

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	goruntime "runtime"
	"strings"
)

// RiskPattern describes one detected high-risk command operation.
type RiskPattern struct {
	Reason string
}

var (
	// WinPathRE matches Windows-style absolute paths in a command string.
	WinPathRE = regexp.MustCompile(`(?i)\b[A-Z]:[\\/][^\s"'<>|;&()]+`)
	// UnixPathRE matches Unix-style absolute paths in a command string.
	UnixPathRE = regexp.MustCompile(`(?i)(?:^|[\s"'=])(/[^\s"'<>|;&()]+)`)
)

// MayModifyOutsidePath reports whether semantic analysis found an explicit
// path operand that the command may mutate.
func MayModifyOutsidePath(command string) bool {
	return len(MutationPathTargets(command)) > 0
}

// ContainsExplicitDeleteCommand reports whether the command invokes a shell
// deletion verb (rm, del, remove-item, etc.).
func ContainsExplicitDeleteCommand(command string) bool {
	for _, invocation := range Invocations(command) {
		if deletionKind(invocation) != deletionNone {
			return true
		}
	}
	return false
}

// IsAllowedDeleteContext reports whether the command's deletion verb is in an
// allowed managed-tool context (git rm, docker rm, kubectl delete, etc.).
func IsAllowedDeleteContext(command string) bool {
	found := false
	for _, invocation := range Invocations(command) {
		kind := deletionKind(invocation)
		if kind == deletionNone {
			continue
		}
		found = true
		if kind == deletionRaw {
			return false
		}
	}
	return found
}

// MatchRiskPattern returns the first RiskPattern that matches the command, or
// nil if none match. Used by both local and remote command safety checks.
func MatchRiskPattern(command string) *RiskPattern {
	for _, invocation := range Invocations(command) {
		if risk := invocationRisk(invocation); risk != nil {
			return risk
		}
	}
	for _, target := range ShellRedirectionTargets(command) {
		if isBlockDevicePath(target) {
			return &RiskPattern{Reason: "直接写入块设备"}
		}
	}
	if forkBombRE.MatchString(stripQuotedContent(command)) {
		return &RiskPattern{Reason: "fork炸弹"}
	}
	return nil
}

// ShellRedirectionTargets extracts the literal target paths of `>` / `>>` /
// `>&` redirections in `command`. File-descriptor redirections (>&2, &-) and
// the null device are returned as-is; callers filter them with
// IsShellNullDevice.
func ShellRedirectionTargets(command string) []string {
	targets := []string{}
	inSingle := false
	inDouble := false
	escaped := false
	for i := 0; i < len(command); i++ {
		c := command[i]
		if escaped {
			escaped = false
			continue
		}
		if inSingle {
			if c == '\'' {
				inSingle = false
			}
			continue
		}
		if inDouble {
			if c == '\\' {
				escaped = true
				continue
			}
			if c == '"' {
				inDouble = false
			}
			continue
		}
		switch c {
		case '\\':
			escaped = true
		case '\'':
			inSingle = true
		case '"':
			inDouble = true
		case '<':
			if i+1 < len(command) && command[i+1] == '<' {
				if end, _, ok := scanHeredoc(command, i); ok {
					i = end - 1
					continue
				}
				if i+2 < len(command) && command[i+2] == '<' {
					if end, ok := scanHereStringWord(command, i+3); ok {
						i = end - 1
						continue
					}
				}
			}
		case '>':
			if i > 0 && command[i-1] == '<' {
				continue
			}
			next := i + 1
			if next < len(command) && command[next] == '>' {
				next++
			}
			if next < len(command) && command[next] == '|' {
				next++
			}
			target, end := parseShellRedirectionTarget(command, next)
			if target != "" && !IsShellFDRedirectionTarget(target) {
				targets = append(targets, target)
			}
			if end > i {
				i = end - 1
			}
		}
	}
	return targets
}

func parseShellRedirectionTarget(command string, start int) (string, int) {
	i := start
	for i < len(command) && (command[i] == ' ' || command[i] == '\t' || command[i] == '\r' || command[i] == '\n') {
		i++
	}
	if i >= len(command) {
		return "", i
	}
	quote := byte(0)
	if command[i] == '\'' || command[i] == '"' {
		quote = command[i]
		i++
	}
	var b strings.Builder
	escaped := false
	for i < len(command) {
		c := command[i]
		if escaped {
			b.WriteByte(c)
			escaped = false
			i++
			continue
		}
		if c == '\\' {
			escaped = true
			i++
			continue
		}
		if quote != 0 {
			if c == quote {
				i++
				break
			}
			b.WriteByte(c)
			i++
			continue
		}
		if c == ' ' || c == '\t' || c == '\r' || c == '\n' || c == ';' || c == '&' || c == '|' || c == '(' || c == ')' {
			break
		}
		b.WriteByte(c)
		i++
	}
	return b.String(), i
}

// IsShellFDRedirectionTarget reports whether a redirection target is a
// file-descriptor form (&2, &-, &1) rather than a file path.
func IsShellFDRedirectionTarget(target string) bool {
	value := strings.TrimSpace(target)
	if strings.HasPrefix(value, "&") {
		value = strings.TrimPrefix(value, "&")
		if value == "-" {
			return true
		}
		for _, c := range value {
			if c < '0' || c > '9' {
				return false
			}
		}
		return value != ""
	}
	return false
}

// IsShellNullDevice reports whether a path is the null device on either
// platform (/dev/null, NUL, $null).
func IsShellNullDevice(path string) bool {
	value := strings.ToLower(strings.TrimSpace(strings.Trim(path, `"'`)))
	value = strings.TrimSuffix(value, ":")
	return value == "/dev/null" || value == "nul" || value == `\\.\nul` || value == "$null"
}

// ResolveCommandLiteralPath resolves a literal redirection target path against
// `workspaceRoot`. It refuses values containing variables, globs, command
// substitution, or command separators. MSYS2 drive paths (/c/...) are
// normalized to Windows paths (C:\...) on Windows.
//
// ok is false when the value cannot be statically resolved.
func ResolveCommandLiteralPath(value, workspaceRoot string) (string, bool) {
	value = strings.TrimSpace(strings.Trim(value, `"'`))
	if value == "" || strings.ContainsAny(value, "$%*?[]{}"+"`") || strings.HasPrefix(value, "&") {
		return "", false
	}
	if goruntime.GOOS == "windows" && len(value) >= 3 && value[0] == '/' && IsASCIILetter(value[1]) && value[2] == '/' {
		value = string(ToUpperByte(value[1])) + ":\\" + value[3:]
	} else {
		value = filepath.FromSlash(value)
	}
	if !filepath.IsAbs(value) {
		value = filepath.Join(workspaceRoot, value)
	}
	return filepath.Clean(value), true
}

// PathExists reports whether a path exists on disk (including symlinks whose
// target is missing).
func PathExists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil || !errors.Is(err, os.ErrNotExist)
}

// AbsolutePathCandidates extracts Windows-style and Unix-style absolute paths
// from `command`. On Windows, MSYS2 drive paths (/c/...) are converted to
// Windows paths (C:\...). The bare `/c` form (cmd.exe /c option) is excluded.
func AbsolutePathCandidates(command string) []string {
	candidates := []string{}
	for _, match := range WinPathRE.FindAllString(command, -1) {
		value := strings.TrimRight(match, `.,:;`)
		if value != "" {
			candidates = append(candidates, value)
		}
	}
	for _, match := range UnixPathRE.FindAllStringSubmatch(command, -1) {
		value := match[1]
		value = strings.Trim(value, ` "'`)
		value = strings.TrimRight(value, `.,:;`)
		if value == "" || strings.HasPrefix(value, "//") {
			continue
		}
		if goruntime.GOOS == "windows" {
			if len(value) >= 3 && value[0] == '/' && IsASCIILetter(value[1]) && value[2] == '/' {
				value = string(ToUpperByte(value[1])) + ":\\" + value[3:]
			} else if len(value) == 2 && value[0] == '/' && IsASCIILetter(value[1]) {
				continue
			}
		}
		candidates = append(candidates, filepath.FromSlash(value))
	}
	return candidates
}

// IsASCIILetter reports whether b is an ASCII letter (a-z, A-Z).
func IsASCIILetter(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

// ToUpperByte returns the ASCII uppercase form of b if it is a lowercase
// ASCII letter; otherwise it returns b unchanged.
func ToUpperByte(b byte) byte {
	if b >= 'a' && b <= 'z' {
		return b - 32
	}
	return b
}
