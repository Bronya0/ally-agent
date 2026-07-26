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

// RiskPattern describes a single high-risk command pattern (e.g. rm -rf /).
type RiskPattern struct {
	RE     *regexp.Regexp
	Reason string
}

// RiskPatterns is the list of always-blocked command patterns.
var RiskPatterns = []RiskPattern{
	{regexp.MustCompile(`(?i)rm\s+-rf\s+/\s*$`), "递归删除文件系统根目录"},
	{regexp.MustCompile(`(?i)rm\s+-rf\s+/\*`), "递归删除文件系统根目录（通配符）"},
	{regexp.MustCompile(`(?i)rm\s+-rf\s+~\b`), "递归删除用户主目录"},
	{regexp.MustCompile(`(?i)rm\s+-rf\s+/home/`), "递归删除 /home 目录"},
	{regexp.MustCompile(`(?i)rm\s+-rf\s+/Users/`), "递归删除 /Users 目录"},
	{regexp.MustCompile(`(?i)rm\s+-rf\s+/root\b`), "递归删除 root 用户目录"},
	{regexp.MustCompile(`(?i)\bmkfs\b`), "格式化文件系统"},
	{regexp.MustCompile(`(?i)\bdd\s+if=`), "通过 dd 直接写入磁盘"},
	{regexp.MustCompile(`(?i)\bdd\s+of=`), "通过 dd 直接写入磁盘"},
	{regexp.MustCompile(`(?i)\bshutdown\b`), "系统关机命令"},
	{regexp.MustCompile(`(?i)\breboot\b`), "系统重启命令"},
	{regexp.MustCompile(`(?i)\bpoweroff\b`), "系统断电命令"},
	{regexp.MustCompile(`(?i)sudo\s+rm`), "提权递归删除"},
	{regexp.MustCompile(`(?i)\bcp\s+/dev/zero\b`), "覆写磁盘数据"},
	{regexp.MustCompile(`(?i):\(\s*\)\s*\{`), "fork炸弹"},
	{regexp.MustCompile(`(?i)\bchmod\s+0[0-7]{2}\b`), "移除所有文件权限"},
	{regexp.MustCompile(`(?i)>\s+/dev/sd`), "直接写入块设备"},
}

var (
	// DeleteCommandRE matches explicit shell deletion commands.
	DeleteCommandRE = regexp.MustCompile(`(?i)(^|[\s;&|()])(?:rm|unlink|rmdir|del|erase|rd|remove-item|ri)\b`)
	// WinPathRE matches Windows-style absolute paths in a command string.
	WinPathRE = regexp.MustCompile(`(?i)\b[A-Z]:[\\/][^\s"'<>|;&()]+`)
	// UnixPathRE matches Unix-style absolute paths in a command string.
	UnixPathRE = regexp.MustCompile(`(?i)(?:^|[\s"'=])(/[^\s"'<>|;&()]+)`)

	// AllowedDeleteREs lists commands whose `rm`/`delete` semantics are
	// managed by the tool itself (git rm, docker rm, etc.) and should not
	// trip the explicit-delete fence.
	AllowedDeleteREs = []*regexp.Regexp{
		regexp.MustCompile(`(?i)\bgit\s+rm\b`),
		regexp.MustCompile(`(?i)\bdocker\s+rm\b`),
		regexp.MustCompile(`(?i)\bdocker\s+(container|image|volume|network)\s+rm\b`),
		regexp.MustCompile(`(?i)\bkubectl\s+delete\b`),
		regexp.MustCompile(`(?i)\bminikube\s+delete\b`),
		regexp.MustCompile(`(?i)\bnpm\s+(uninstall|remove)\b`),
		regexp.MustCompile(`(?i)\bpnpm\s+(uninstall|remove)\b`),
		regexp.MustCompile(`(?i)\byarn\s+remove\b`),
	}
)

var outsidePathMutationPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(^|[\s;&|()])(?:cp|copy|mv|move|touch|mkdir|md|install|chmod|chown|attrib|ren|rename|truncate|tee)\b`),
	regexp.MustCompile(`(?i)(^|[\s;&|()])(?:sed|perl)\s+-[^\r\n;&|]*i\b`),
	regexp.MustCompile(`(?i)(^|[\s;&|()])(?:python(?:3)?|node|ruby|php)\b`),
	regexp.MustCompile(`(?i)\b(?:Set|Add|Out|New|Copy|Move|Rename|Remove|Clear)-(?:Content|Item|File)\b`),
	regexp.MustCompile(`(?i)\b(?:WriteAllText|WriteAllBytes|OpenWrite|FileStream)\b`),
	regexp.MustCompile(`(?i)(^|[\s;&|()])(?:tar\s+[^\r\n;&|]*-[^\r\n;&|]*x|unzip\b)`),
}

// MayModifyOutsidePath reports whether the command uses a verb that can mutate
// an existing path (cp/mv/sed -i/tar -x/WriteAllText/etc.).
func MayModifyOutsidePath(command string) bool {
	for _, pattern := range outsidePathMutationPatterns {
		if pattern.MatchString(command) {
			return true
		}
	}
	return false
}

// ContainsExplicitDeleteCommand reports whether the command invokes a shell
// deletion verb (rm, del, remove-item, etc.).
func ContainsExplicitDeleteCommand(command string) bool {
	return DeleteCommandRE.MatchString(command)
}

// IsAllowedDeleteContext reports whether the command's deletion verb is in an
// allowed managed-tool context (git rm, docker rm, kubectl delete, etc.).
func IsAllowedDeleteContext(command string) bool {
	for _, re := range AllowedDeleteREs {
		if re.MatchString(command) {
			return true
		}
	}
	return false
}

// MatchRiskPattern returns the first RiskPattern that matches the command, or
// nil if none match. Used by both local and remote command safety checks.
func MatchRiskPattern(command string) *RiskPattern {
	for i := range RiskPatterns {
		if RiskPatterns[i].RE.MatchString(command) {
			return &RiskPatterns[i]
		}
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
