package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	goruntime "runtime"
	"strings"
)

var (
	deleteCommandRE = regexp.MustCompile(`(?i)(^|[\s;&|()])(?:rm|unlink|rmdir|del|erase|rd|remove-item|ri)\b`)
	winPathRE       = regexp.MustCompile(`(?i)\b[A-Z]:[\\/][^\s"'<>|;&()]+`)
	unixPathRE      = regexp.MustCompile(`(?i)(?:^|[\s"'=])(/[^\s"'<>|;&()]+)`)

	allowedDeleteREs = []*regexp.Regexp{
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

var commandRiskPatterns = []struct {
	re     *regexp.Regexp
	reason string
}{
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

// checkCommandSafety inspects commands for high-risk patterns and routes
// explicit deletion through delete_path, where workspace and OS guards apply.
// roots[0] 是主工作区（命令的默认 cwd），其余为会话级附加根目录。
func checkCommandSafety(req CommandRequest, roots []string) error {
	cmd := req.Command
	if containsExplicitDeleteCommand(cmd) && !isAllowedDeleteContext(cmd) {
		return codedToolError("E_COMMAND_BLOCKED", fmt.Errorf("安全围栏已拦截：run_command 不允许直接执行文件删除命令。\n原因：shell 删除命令可能绕过工作区边界、系统目录和 .git 保护。\n处理方式：请改用 delete_path 工具，由专用工具检查目标路径和递归范围。\n被拦截的命令：%s", cmd))
	}
	if risk := firstExistingOutsideMutationTarget(cmd, roots); risk != nil {
		return codedToolError("E_PATH_OUTSIDE", fmt.Errorf("安全围栏已拦截：命令可能修改工作区外的受保护目标。\n原因：%s。\n检测到的目标：%s\n允许的操作：读取工作区外路径、写入 /dev/null 等空设备、创建不存在的新路径。\n禁止的操作：覆盖、追加、移动、改权限或以其他方式修改已经存在的工作区外文件或目录。\n允许写入的根目录：\n%s\n被拦截的命令：%s", risk.Reason, risk.Path, formatAllowedRoots(roots), cmd))
	}
	for _, r := range commandRiskPatterns {
		if r.re.MatchString(cmd) {
			return codedToolError("E_COMMAND_BLOCKED", fmt.Errorf("高危命令拒绝: 检测到%s - 命令已被安全围栏拦截。\n如需执行此操作，请手动在终端中执行。\n被拦截的命令: %s", r.reason, cmd))
		}
	}
	return nil
}

var outsidePathMutationPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(^|[\s;&|()])(?:cp|copy|mv|move|touch|mkdir|md|install|chmod|chown|attrib|ren|rename|truncate|tee)\b`),
	regexp.MustCompile(`(?i)(^|[\s;&|()])(?:sed|perl)\s+-[^\r\n;&|]*i\b`),
	regexp.MustCompile(`(?i)(^|[\s;&|()])(?:python(?:3)?|node|ruby|php)\b`),
	regexp.MustCompile(`(?i)\b(?:Set|Add|Out|New|Copy|Move|Rename|Remove|Clear)-(?:Content|Item|File)\b`),
	regexp.MustCompile(`(?i)\b(?:WriteAllText|WriteAllBytes|OpenWrite|FileStream)\b`),
	regexp.MustCompile(`(?i)(^|[\s;&|()])(?:tar\s+[^\r\n;&|]*-[^\r\n;&|]*x|unzip\b)`),
}

func commandMayModifyOutsidePath(command string) bool {
	for _, pattern := range outsidePathMutationPatterns {
		if pattern.MatchString(command) {
			return true
		}
	}
	return false
}

// firstExistingOutsideMutationTarget describes an outside-write risk that the
// UI can explain directly. Creating a new literal outside path is allowed,
// while changing an existing path or using an unresolved redirection target
// remains blocked. Harmless sinks such as /dev/null are ignored.
type outsideMutationRisk struct {
	Path   string
	Reason string
}

func firstExistingOutsideMutationTarget(command string, roots []string) *outsideMutationRisk {
	if len(roots) == 0 {
		return nil
	}
	primaryRoot := roots[0]
	for _, target := range shellRedirectionTargets(command) {
		if isShellNullDevice(target) {
			continue
		}
		path, ok := resolveCommandLiteralPath(target, primaryRoot)
		if !ok {
			return &outsideMutationRisk{
				Path:   target,
				Reason: "重定向目标包含变量、通配符或命令替换，执行前无法确认真实写入位置",
			}
		}
		if insideAnyRoot(roots, path) || insideAllyAgentDir(path) {
			continue
		}
		if pathExists(path) {
			return &outsideMutationRisk{
				Path:   filepath.ToSlash(path),
				Reason: "重定向目标已经存在，继续执行可能覆盖或追加其内容",
			}
		}
	}

	if !commandMayModifyOutsidePath(command) {
		return nil
	}
	for _, candidate := range absolutePathCandidates(command) {
		if isShellNullDevice(candidate) {
			continue
		}
		clean := filepath.Clean(candidate)
		if insideAnyRoot(roots, clean) || insideAllyAgentDir(clean) {
			continue
		}
		if pathExists(clean) {
			return &outsideMutationRisk{
				Path:   filepath.ToSlash(clean),
				Reason: "命令包含写入、移动、改权限或原地修改操作，并引用了已经存在的工作区外路径",
			}
		}
	}
	return nil
}

func shellRedirectionTargets(command string) []string {
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
			if target != "" && !isShellFDRedirectionTarget(target) {
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

func isShellFDRedirectionTarget(target string) bool {
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

func isShellNullDevice(path string) bool {
	value := strings.ToLower(strings.TrimSpace(strings.Trim(path, `"'`)))
	value = strings.TrimSuffix(value, ":")
	return value == "/dev/null" || value == "nul" || value == `\\.\nul` || value == "$null"
}

func resolveCommandLiteralPath(value, workspaceRoot string) (string, bool) {
	value = strings.TrimSpace(strings.Trim(value, `"'`))
	if value == "" || strings.ContainsAny(value, "$%*?[]{}"+"`") || strings.HasPrefix(value, "&") {
		return "", false
	}
	if goruntime.GOOS == "windows" && len(value) >= 3 && value[0] == '/' && isASCIILetter(value[1]) && value[2] == '/' {
		value = string(toUpperByte(value[1])) + ":\\" + value[3:]
	} else {
		value = filepath.FromSlash(value)
	}
	if !filepath.IsAbs(value) {
		value = filepath.Join(workspaceRoot, value)
	}
	return filepath.Clean(value), true
}

func pathExists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil || !errors.Is(err, os.ErrNotExist)
}

func isAllowedDeleteContext(command string) bool {
	for _, re := range allowedDeleteREs {
		if re.MatchString(command) {
			return true
		}
	}
	return false
}

func validateRemoteCommandSafety(cmd string) error {
	for _, r := range commandRiskPatterns {
		if r.re.MatchString(cmd) {
			return codedToolError("E_COMMAND_BLOCKED", fmt.Errorf("高危命令拒绝: 检测到%s - 命令已被安全围栏拦截。\n如需执行此操作，请手动在终端中执行。\n被拦截的命令: %s", r.reason, cmd))
		}
	}
	return nil
}

func containsExplicitDeleteCommand(command string) bool {
	return deleteCommandRE.MatchString(command)
}

func firstAbsolutePathOutsideWorkspace(command string, workspaceRoot string) string {
	root := filepath.Clean(workspaceRoot)
	for _, candidate := range absolutePathCandidates(command) {
		if candidate == "" {
			continue
		}
		clean := filepath.Clean(candidate)
		if !insideRoot(root, clean) && !insideAllyAgentDir(clean) {
			return filepath.ToSlash(clean)
		}
	}
	return ""
}

func absolutePathCandidates(command string) []string {
	candidates := []string{}
	for _, match := range winPathRE.FindAllString(command, -1) {
		value := strings.TrimRight(match, `.,:;`)
		if value != "" {
			candidates = append(candidates, value)
		}
	}
	// On Windows with bash (Git Bash / MSYS2), commands may use Unix-style
	// paths such as /c/Users or /tmp. Always check unixPathRE on Windows too,
	// converting MSYS2 drive paths (/c/...) to Windows paths (C:\...) so they
	// can be compared against the workspace root.
	for _, match := range unixPathRE.FindAllStringSubmatch(command, -1) {
		value := match[1]
		value = strings.Trim(value, ` "'`)
		value = strings.TrimRight(value, `.,:;`)
		if value == "" || strings.HasPrefix(value, "//") {
			continue
		}
		if goruntime.GOOS == "windows" {
			// Convert MSYS2 drive paths /c/... → C:\...
			if len(value) >= 3 && value[0] == '/' && isASCIILetter(value[1]) && value[2] == '/' {
				value = string(toUpperByte(value[1])) + ":\\" + value[3:]
			} else if len(value) == 2 && value[0] == '/' && isASCIILetter(value[1]) {
				// A bare /c is commonly the option used by cmd.exe /c, not an
				// MSYS2 drive path. Drive roots remain detectable as /c/.
				continue
			}
		}
		candidates = append(candidates, filepath.FromSlash(value))
	}
	return candidates
}

func isASCIILetter(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

func toUpperByte(b byte) byte {
	if b >= 'a' && b <= 'z' {
		return b - 32
	}
	return b
}
