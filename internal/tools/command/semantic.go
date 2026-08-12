package command

import (
	"regexp"
	"strings"
)

// Invocation is one executable command extracted from a shell command line.
// Name is normalized to a lowercase basename without a Windows .exe suffix.
type Invocation struct {
	Name string
	Args []string
}

const (
	deletionRaw     = -1
	deletionNone    = 0
	deletionManaged = 1
)

var forkBombRE = regexp.MustCompile(`(?i):\s*\(\s*\)\s*\{[^}]*:\s*\|\s*:\s*&[^}]*}\s*;\s*:`)

// Invocations performs deliberately small shell-aware parsing. It recognizes
// command separators, quotes, common wrappers, and nested shell scripts. It is
// not an executor and intentionally does not expand variables.
func Invocations(commandLine string) []Invocation {
	return invocations(commandLine, 0)
}

func invocations(commandLine string, depth int) []Invocation {
	if depth > 4 {
		return nil
	}
	segments := splitCommandSegments(commandLine)
	result := make([]Invocation, 0, len(segments))
	for _, words := range segments {
		invocation, ok := invocationFromWords(words)
		if !ok {
			continue
		}
		result = append(result, invocation)
		for _, nested := range nestedInvocations(invocation, depth) {
			result = append(result, nested)
		}
	}
	for _, script := range commandSubstitutions(commandLine) {
		result = append(result, invocations(script, depth+1)...)
	}
	return result
}

func splitCommandSegments(commandLine string) [][]string {
	segments := [][]string{}
	words := []string{}
	var word strings.Builder
	quote := byte(0)
	escaped := false
	started := false

	flushWord := func() {
		if !started {
			return
		}
		words = append(words, word.String())
		word.Reset()
		started = false
	}
	flushSegment := func() {
		flushWord()
		if len(words) > 0 {
			segments = append(segments, words)
			words = nil
		}
	}

	for i := 0; i < len(commandLine); i++ {
		c := commandLine[i]
		if escaped {
			word.WriteByte(c)
			started = true
			escaped = false
			continue
		}
		if quote != 0 {
			if c == quote {
				quote = 0
				started = true
				continue
			}
			if c == '\\' && quote == '"' {
				escaped = true
				started = true
				continue
			}
			word.WriteByte(c)
			started = true
			continue
		}

		switch c {
		case '\\':
			// Preserve Windows path separators. A backslash only escapes shell
			// syntax or whitespace here.
			if i+1 < len(commandLine) && strings.ContainsRune(" \t\r\n\\\"';&|(){}<>$`", rune(commandLine[i+1])) {
				escaped = true
			} else {
				word.WriteByte(c)
				started = true
			}
		case '\'', '"':
			quote = c
			started = true
		case ' ', '\t', '\r':
			flushWord()
		case '\n', ';', '&', '|', '(', ')', '{', '}':
			flushSegment()
		case '<', '>':
			if c == '<' {
				if end, _, ok := scanHeredoc(commandLine, i); ok {
					i = end - 1
					continue
				}
				if i+2 < len(commandLine) && commandLine[i+1] == '<' && commandLine[i+2] == '<' {
					if end, ok := scanHereStringWord(commandLine, i+3); ok {
						i = end - 1
						continue
					}
				}
			}
			if started && isDecimal(word.String()) {
				word.Reset()
				started = false
			} else {
				flushWord()
			}
			for i+1 < len(commandLine) && strings.ContainsRune("<>|&", rune(commandLine[i+1])) {
				i++
			}
			for i+1 < len(commandLine) && (commandLine[i+1] == ' ' || commandLine[i+1] == '\t') {
				i++
			}
			_, end := parseShellRedirectionTarget(commandLine, i+1)
			if end > i+1 {
				i = end - 1
			}
		default:
			word.WriteByte(c)
			started = true
		}
	}
	if escaped {
		word.WriteByte('\\')
	}
	flushSegment()
	return segments
}

func invocationFromWords(words []string) (Invocation, bool) {
	for len(words) > 0 && isEnvironmentAssignment(words[0]) {
		words = words[1:]
	}
	for len(words) > 0 {
		name := normalizeCommandName(words[0])
		switch name {
		case "sudo":
			words = skipWrapperOptions(words[1:], map[string]bool{
				"-u": true, "--user": true, "-g": true, "--group": true,
				"-h": true, "--host": true, "-p": true, "--prompt": true,
				"-c": true, "--close-from": true,
			})
		case "env":
			words = words[1:]
			for len(words) > 0 && (strings.HasPrefix(words[0], "-") || isEnvironmentAssignment(words[0])) {
				words = words[1:]
			}
		case "command", "builtin", "nohup":
			words = skipWrapperOptions(words[1:], nil)
		case "nice":
			words = skipWrapperOptions(words[1:], map[string]bool{"-n": true, "--adjustment": true})
		case "timeout":
			words = skipWrapperOptions(words[1:], map[string]bool{
				"-s": true, "--signal": true, "-k": true, "--kill-after": true,
			})
			// timeout 的时长是第一个位置参数（如 5、5s、infinity），跳过它。
			// 无时长时 timeout 自身报错不执行内部命令，因此跳一个位置参数
			// 即使误跳也是无害方向（命令本就不会运行）。
			if len(words) > 0 && !strings.HasPrefix(words[0], "-") {
				words = words[1:]
			}
		case "stdbuf":
			// 支持 -oL 粘连形式（本身以 - 开头，整词跳过）与 -o L 分离形式。
			words = skipWrapperOptions(words[1:], map[string]bool{
				"-i": true, "--input": true, "-o": true, "--output": true,
				"-e": true, "--error": true,
			})
		case "setsid":
			words = skipWrapperOptions(words[1:], nil)
		case "ionice":
			words = skipWrapperOptions(words[1:], map[string]bool{
				"-c": true, "--class": true, "-n": true, "--classdata": true,
				"-p": true, "--pid": true,
			})
		default:
			if name == "" {
				return Invocation{}, false
			}
			return Invocation{Name: name, Args: append([]string(nil), words[1:]...)}, true
		}
	}
	return Invocation{}, false
}

func skipWrapperOptions(words []string, optionsWithValue map[string]bool) []string {
	for len(words) > 0 && strings.HasPrefix(words[0], "-") {
		option := strings.ToLower(words[0])
		words = words[1:]
		if optionsWithValue != nil && optionsWithValue[option] && len(words) > 0 {
			words = words[1:]
		}
	}
	return words
}

func nestedInvocations(invocation Invocation, depth int) []Invocation {
	if script, ok := nestedShellScript(invocation); ok {
		return invocations(script, depth+1)
	}
	if invocation.Name == "eval" || invocation.Name == "invoke-expression" || invocation.Name == "iex" {
		if len(invocation.Args) > 0 {
			return invocations(strings.Join(invocation.Args, " "), depth+1)
		}
		return nil
	}
	if invocation.Name == "xargs" {
		args := skipWrapperOptions(invocation.Args, map[string]bool{
			"-a": true, "--arg-file": true, "-d": true, "--delimiter": true,
			"-e": true, "-i": true, "-l": true, "-n": true,
			"--max-args": true, "-s": true, "--max-chars": true,
			"-p": false, "-r": false, "-t": false,
		})
		if nested, ok := invocationFromWords(args); ok {
			return []Invocation{nested}
		}
	}
	if invocation.Name == "find" {
		for i, arg := range invocation.Args {
			if (arg == "-exec" || arg == "-execdir") && i+1 < len(invocation.Args) {
				end := len(invocation.Args)
				for j := i + 1; j < len(invocation.Args); j++ {
					if invocation.Args[j] == ";" || invocation.Args[j] == "+" {
						end = j
						break
					}
				}
				if nested, ok := invocationFromWords(invocation.Args[i+1 : end]); ok {
					return []Invocation{nested}
				}
			}
		}
	}
	if invocation.Name == "busybox" {
		if nested, ok := invocationFromWords(invocation.Args); ok {
			return []Invocation{nested}
		}
	}
	return nil
}

func commandSubstitutions(commandLine string) []string {
	result := []string{}
	quote := byte(0)
	escaped := false
	for i := 0; i < len(commandLine); i++ {
		c := commandLine[i]
		if escaped {
			escaped = false
			continue
		}
		if c == '\\' {
			escaped = true
			continue
		}
		if quote == '\'' {
			if c == '\'' {
				quote = 0
			}
			continue
		}
		if c == '\'' {
			quote = '\''
			continue
		}
		if c == '"' {
			if quote == '"' {
				quote = 0
			} else if quote == 0 {
				quote = '"'
			}
			continue
		}
		if c == '<' && i+1 < len(commandLine) && commandLine[i+1] == '<' {
			// Quoted heredoc bodies are literal data: command substitutions inside
			// them are never executed, so skip the whole body.
			if end, quoted, ok := scanHeredoc(commandLine, i); ok && quoted {
				i = end - 1
				continue
			}
		}
		if c == '`' {
			end := i + 1
			for end < len(commandLine) && commandLine[end] != '`' {
				if commandLine[end] == '\\' && end+1 < len(commandLine) {
					end += 2
					continue
				}
				end++
			}
			if end < len(commandLine) {
				result = append(result, commandLine[i+1:end])
				i = end
			}
			continue
		}
		if c == '$' && i+1 < len(commandLine) && commandLine[i+1] == '(' {
			start := i + 2
			depth := 1
			innerQuote := byte(0)
			end := start
			for ; end < len(commandLine); end++ {
				current := commandLine[end]
				if innerQuote != 0 {
					if current == innerQuote {
						innerQuote = 0
					}
					continue
				}
				if current == '\'' || current == '"' {
					innerQuote = current
					continue
				}
				switch current {
				case '(':
					depth++
				case ')':
					depth--
					if depth == 0 {
						result = append(result, commandLine[start:end])
						i = end
						end = len(commandLine)
					}
				}
			}
		}
	}
	return result
}

func nestedShellScript(invocation Invocation) (string, bool) {
	var flags []string
	switch invocation.Name {
	case "bash", "sh", "zsh", "dash", "ksh":
		flags = []string{"-c"}
	case "cmd":
		flags = []string{"/c", "/k"}
	case "powershell", "pwsh":
		flags = []string{"-command", "-c", "/c"}
	default:
		return "", false
	}
	for i, arg := range invocation.Args {
		for _, flag := range flags {
			if strings.EqualFold(arg, flag) && i+1 < len(invocation.Args) {
				return invocation.Args[i+1], true
			}
		}
	}
	return "", false
}

func normalizeCommandName(value string) string {
	value = strings.TrimSpace(strings.Trim(value, `"'`))
	value = strings.ReplaceAll(value, "\\", "/")
	if slash := strings.LastIndex(value, "/"); slash >= 0 {
		value = value[slash+1:]
	}
	value = strings.ToLower(value)
	return strings.TrimSuffix(value, ".exe")
}

func isEnvironmentAssignment(value string) bool {
	equals := strings.IndexByte(value, '=')
	if equals <= 0 {
		return false
	}
	for i, c := range value[:equals] {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '_' || (i > 0 && c >= '0' && c <= '9')) {
			return false
		}
	}
	return true
}

func isDecimal(value string) bool {
	if value == "" {
		return false
	}
	for _, c := range value {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func deletionKind(invocation Invocation) int {
	switch invocation.Name {
	case "rm", "unlink", "rmdir", "del", "erase", "rd", "remove-item", "ri":
		return deletionRaw
	case "find":
		if findDeletes(invocation.Args) {
			return deletionRaw
		}
	case "rsync":
		if rsyncDeletes(invocation.Args) {
			return deletionRaw
		}
	case "git":
		if firstCommandArg(invocation.Args) == "rm" {
			return deletionManaged
		}
	case "docker":
		args := commandArgs(invocation.Args)
		if len(args) > 0 && args[0] == "rm" {
			return deletionManaged
		}
		if len(args) > 1 && containsString([]string{"container", "image", "volume", "network"}, args[0]) && args[1] == "rm" {
			return deletionManaged
		}
	case "kubectl", "minikube":
		if firstCommandArg(invocation.Args) == "delete" {
			return deletionManaged
		}
	case "npm", "pnpm":
		if containsString([]string{"uninstall", "remove"}, firstCommandArg(invocation.Args)) {
			return deletionManaged
		}
	case "yarn":
		if firstCommandArg(invocation.Args) == "remove" {
			return deletionManaged
		}
	}
	return deletionNone
}

func findDeletes(args []string) bool {
	for _, arg := range args {
		if strings.EqualFold(arg, "-delete") {
			return true
		}
	}
	return false
}

func rsyncDeletes(args []string) bool {
	for _, arg := range args {
		if strings.HasPrefix(strings.ToLower(arg), "--delete") {
			return true
		}
	}
	return false
}

func commandArgs(args []string) []string {
	result := make([]string, 0, len(args))
	for _, arg := range args {
		if strings.HasPrefix(arg, "-") {
			continue
		}
		result = append(result, strings.ToLower(arg))
	}
	return result
}

func firstCommandArg(args []string) string {
	values := commandArgs(args)
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func invocationRisk(invocation Invocation) *RiskPattern {
	switch {
	case invocation.Name == "mkfs" || strings.HasPrefix(invocation.Name, "mkfs."):
		return &RiskPattern{Reason: "格式化文件系统"}
	case invocation.Name == "shutdown":
		return &RiskPattern{Reason: "系统关机命令"}
	case invocation.Name == "reboot":
		return &RiskPattern{Reason: "系统重启命令"}
	case invocation.Name == "poweroff":
		return &RiskPattern{Reason: "系统断电命令"}
	case invocation.Name == "chmod" && chmodRemovesAllPermissions(invocation.Args):
		return &RiskPattern{Reason: "移除所有文件权限"}
	case invocation.Name == "dd" && ddWritesBlockDevice(invocation.Args):
		return &RiskPattern{Reason: "通过 dd 直接写入块设备"}
	case (invocation.Name == "cp" || invocation.Name == "copy") && copiesEndlessDevice(invocation.Args):
		return &RiskPattern{Reason: "从无限设备覆写文件"}
	}
	return nil
}

func chmodRemovesAllPermissions(args []string) bool {
	for _, arg := range args {
		value := strings.TrimPrefix(strings.ToLower(arg), "--mode=")
		if value == "000" || value == "0000" {
			return true
		}
	}
	return false
}

func ddWritesBlockDevice(args []string) bool {
	for _, arg := range args {
		if strings.HasPrefix(strings.ToLower(arg), "of=") && isBlockDevicePath(arg[3:]) {
			return true
		}
	}
	return false
}

func copiesEndlessDevice(args []string) bool {
	positionals := positionalArgs(args)
	return len(positionals) > 1 && (strings.EqualFold(positionals[0], "/dev/zero") || strings.EqualFold(positionals[0], "/dev/random") || strings.EqualFold(positionals[0], "/dev/urandom"))
}

func isBlockDevicePath(value string) bool {
	value = strings.ToLower(strings.TrimSpace(strings.Trim(value, `"'`)))
	return strings.HasPrefix(value, "/dev/sd") ||
		strings.HasPrefix(value, "/dev/nvme") ||
		strings.HasPrefix(value, "/dev/vd") ||
		strings.HasPrefix(value, "/dev/disk") ||
		strings.HasPrefix(value, `\\.\physicaldrive`)
}

func stripQuotedContent(value string) string {
	var result strings.Builder
	quote := byte(0)
	escaped := false
	for i := 0; i < len(value); i++ {
		c := value[i]
		if c == '<' && i+1 < len(value) && value[i+1] == '<' {
			// Quoted heredoc bodies are literal data; skip them so their
			// contents are not misread as fork-bomb or other risky patterns.
			if end, quoted, ok := scanHeredoc(value, i); ok && quoted {
				i = end - 1
				continue
			}
		}
		if escaped {
			escaped = false
			if quote == 0 {
				result.WriteByte(' ')
			}
			continue
		}
		if c == '\\' {
			escaped = true
			if quote == 0 {
				result.WriteByte(c)
			}
			continue
		}
		if quote != 0 {
			if c == quote {
				quote = 0
			}
			result.WriteByte(' ')
			continue
		}
		if c == '\'' || c == '"' {
			quote = c
			result.WriteByte(' ')
			continue
		}
		result.WriteByte(c)
	}
	return result.String()
}

// MutationPathTargets returns explicit operands that an invocation may create
// or modify. Read-only source operands are deliberately excluded.
func MutationPathTargets(commandLine string) []string {
	targets := []string{}
	for _, invocation := range Invocations(commandLine) {
		targets = append(targets, mutationTargets(invocation)...)
	}
	return targets
}

func mutationTargets(invocation Invocation) []string {
	switch invocation.Name {
	case "cp", "copy", "mv", "move", "install", "ren", "rename", "copy-item", "move-item", "rename-item":
		if values := optionValues(invocation.Args, "-t", "--target-directory", "-destination"); len(values) > 0 {
			return values
		}
		return lastPositional(invocation.Args)
	case "touch":
		return touchTargets(invocation.Args)
	case "mkdir", "md", "tee", "new-item":
		return positionalArgs(invocation.Args)
	case "chmod", "chown":
		args := positionalArgs(invocation.Args)
		if len(args) > 1 {
			return args[1:]
		}
	case "attrib":
		result := []string{}
		for _, arg := range invocation.Args {
			if !strings.HasPrefix(arg, "+") && !strings.HasPrefix(arg, "-") {
				result = append(result, arg)
			}
		}
		return result
	case "truncate":
		return lastPositional(invocation.Args)
	case "sed", "perl":
		if hasInPlaceFlag(invocation.Args) {
			return inPlaceFileArgs(invocation.Args)
		}
	case "tar":
		if tarExtracts(invocation.Args) {
			if values := optionValues(invocation.Args, "-c", "--directory"); len(values) > 0 {
				return values
			}
			return []string{"."}
		}
	case "unzip":
		if unzipExtracts(invocation.Args) {
			if values := optionValues(invocation.Args, "-d"); len(values) > 0 {
				return values
			}
			return []string{"."}
		}
	case "7z", "7za", "7zz":
		if firstCommandArg(invocation.Args) == "x" || firstCommandArg(invocation.Args) == "e" {
			for _, arg := range invocation.Args {
				if len(arg) > 2 && strings.EqualFold(arg[:2], "-o") {
					return []string{arg[2:]}
				}
			}
			return []string{"."}
		}
	case "set-content", "add-content", "out-file", "clear-content", "new-file":
		if values := optionValues(invocation.Args, "-path", "-literalpath", "-filepath"); len(values) > 0 {
			return values
		}
		return firstPositional(invocation.Args)
	case "dd":
		for _, arg := range invocation.Args {
			if strings.HasPrefix(strings.ToLower(arg), "of=") {
				return []string{arg[3:]}
			}
		}
	}
	return nil
}

func positionalArgs(args []string) []string {
	result := []string{}
	for _, arg := range args {
		if arg == "--" {
			continue
		}
		if strings.HasPrefix(arg, "-") || (strings.HasPrefix(arg, "/") && len(arg) == 2) {
			continue
		}
		result = append(result, arg)
	}
	return result
}

func touchTargets(args []string) []string {
	result := []string{}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if containsString([]string{"-d", "--date", "-r", "--reference", "-t", "--time"}, strings.ToLower(arg)) {
			i++
			continue
		}
		if strings.HasPrefix(arg, "-") {
			continue
		}
		result = append(result, arg)
	}
	return result
}

func firstPositional(args []string) []string {
	values := positionalArgs(args)
	if len(values) == 0 {
		return nil
	}
	return values[:1]
}

func lastPositional(args []string) []string {
	values := positionalArgs(args)
	if len(values) == 0 {
		return nil
	}
	return values[len(values)-1:]
}

func inPlaceFileArgs(args []string) []string {
	values := positionalArgs(args)
	if len(values) < 2 {
		return nil
	}
	return values[1:]
}

func hasInPlaceFlag(args []string) bool {
	for _, arg := range args {
		lower := strings.ToLower(arg)
		if lower == "-i" || strings.HasPrefix(lower, "-i.") || strings.HasPrefix(lower, "--in-place") {
			return true
		}
	}
	return false
}

func tarExtracts(args []string) bool {
	for _, arg := range args {
		lower := strings.ToLower(arg)
		if lower == "--extract" || lower == "-x" || (strings.HasPrefix(lower, "-") && !strings.HasPrefix(lower, "--") && strings.Contains(lower[1:], "x")) {
			return true
		}
	}
	return false
}

func unzipExtracts(args []string) bool {
	for _, arg := range args {
		switch strings.ToLower(arg) {
		case "-l", "-t", "-p", "-z", "-v":
			return false
		}
	}
	return true
}

func optionValues(args []string, names ...string) []string {
	result := []string{}
	for i, arg := range args {
		for _, name := range names {
			if strings.EqualFold(arg, name) && i+1 < len(args) {
				result = append(result, args[i+1])
			}
		}
	}
	return result
}

// scanHeredoc recognizes a heredoc start at command[i] (`<<word`/`<<-word`)
// and returns the index just past the body terminator line, plus whether the
// delimiter is quoted (body fully literal). ok is false when the start is not
// a heredoc or the terminator cannot be located.
func scanHeredoc(command string, i int) (end int, quoted bool, ok bool) {
	if i+1 >= len(command) || command[i+1] != '<' {
		return 0, false, false
	}
	j := i + 2
	stripTabs := false
	if j < len(command) && command[j] == '-' {
		stripTabs = true
		j++
	}
	for j < len(command) && (command[j] == ' ' || command[j] == '\t') {
		j++
	}
	var delim strings.Builder
	if j < len(command) && (command[j] == '\'' || command[j] == '"') {
		q := command[j]
		quoted = true
		j++
		for j < len(command) && command[j] != q {
			if command[j] == '\\' && q == '"' && j+1 < len(command) {
				j++
			}
			delim.WriteByte(command[j])
			j++
		}
		if j >= len(command) {
			return 0, false, false
		}
		j++
	} else {
		for j < len(command) && command[j] != '\n' && command[j] != ' ' && command[j] != '\t' && command[j] != '\r' {
			if command[j] == '\\' && !quoted {
				quoted = true
				j++
				if j >= len(command) {
					return 0, false, false
				}
				continue
			}
			delim.WriteByte(command[j])
			j++
		}
	}
	if delim.Len() == 0 {
		return 0, false, false
	}
	word := delim.String()
	lineStart := j
	for lineStart < len(command) {
		nl := strings.IndexByte(command[lineStart:], '\n')
		if nl < 0 {
			return 0, false, false
		}
		lineEnd := lineStart + nl
		line := strings.TrimSuffix(command[lineStart:lineEnd], "\r")
		if stripTabs {
			line = strings.TrimLeft(line, "\t")
		}
		if line == word {
			return lineEnd + 1, quoted, true
		}
		lineStart = lineEnd + 1
	}
	return 0, false, false
}

// scanHereStringWord returns the index just past the here-string word starting
// at command[start] (right after `<<<`). Quoted words are skipped as a unit;
// bare words end at whitespace, newline, or a command separator.
func scanHereStringWord(command string, start int) (int, bool) {
	j := start
	for j < len(command) && (command[j] == ' ' || command[j] == '\t') {
		j++
	}
	if j >= len(command) {
		return 0, false
	}
	if command[j] == '\'' || command[j] == '"' {
		q := command[j]
		j++
		for j < len(command) && command[j] != q {
			if command[j] == '\\' && j+1 < len(command) {
				j++
			}
			j++
		}
		if j < len(command) {
			j++
		}
		return j, true
	}
	for j < len(command) && command[j] != '\n' && command[j] != ';' && command[j] != '&' && command[j] != '|' && command[j] != ' ' && command[j] != '\t' {
		j++
	}
	return j, true
}
