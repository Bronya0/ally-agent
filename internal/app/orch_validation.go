// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	openai "github.com/sashabaranov/go-openai"
)

const (
	autoValidationTimeout     = 12 * time.Second
	autoValidationOutputBytes = 8 * 1024
	maxValidationFileBytes    = 16 * 1024 * 1024
)

type validationFile struct {
	abs     string
	display string
	ext     string
}

type validationReport struct {
	label   string
	detail  string
	passed  bool
	skipped bool
}

func attachValidation(data any, message string) any {
	if strings.TrimSpace(message) == "" {
		return data
	}
	switch result := data.(type) {
	case EditResult:
		result.Validation = message
		return result
	case *EditResult:
		if result != nil {
			result.Validation = message
		}
		return result
	case MultiEditResult:
		result.Validation = message
		return result
	case *MultiEditResult:
		if result != nil {
			result.Validation = message
		}
		return result
	default:
		return data
	}
}

// validateChangedFiles runs only cheap, deterministic checks for files that
// were just written. It returns one human-readable string because this result
// is primarily an instruction to the model, not a second tool protocol.
func (a *App) validateChangedFiles(ctx context.Context, cfg ConfigState, paths []string) string {
	if len(paths) == 0 {
		return ""
	}
	if ctx == nil {
		ctx = context.Background()
	}
	roots, err := workspaceRoots(cfg)
	if err != nil || len(roots) == 0 {
		return "自动校验跳过：无法确定工作区根目录。"
	}
	root := roots[0]
	files := collectValidationFiles(roots, paths)
	if len(files) == 0 {
		return ""
	}

	checkCtx, cancel := context.WithTimeout(ctx, autoValidationTimeout)
	defer cancel()
	reports := make([]validationReport, 0, 8)

	jsonFiles, pythonFiles, jsFiles, javaFiles := make([]validationFile, 0), make([]validationFile, 0), make([]validationFile, 0), make([]validationFile, 0)
	goFiles, tsFiles, vueFiles := make([]validationFile, 0), make([]validationFile, 0), make([]validationFile, 0)
	for _, file := range files {
		switch file.ext {
		case ".json":
			if validationEnabled(cfg.AutoValidationJSON) {
				jsonFiles = append(jsonFiles, file)
			}
		case ".py":
			if validationEnabled(cfg.AutoValidationPython) {
				pythonFiles = append(pythonFiles, file)
			}
		// .jsx deliberately excluded: node --check cannot parse JSX.
		case ".js", ".mjs", ".cjs":
			if validationEnabled(cfg.AutoValidationJavaScript) {
				jsFiles = append(jsFiles, file)
			}
		case ".go":
			if validationEnabled(cfg.AutoValidationGo) {
				goFiles = append(goFiles, file)
			}
		case ".ts", ".tsx", ".d.ts":
			if validationEnabled(cfg.AutoValidationTypeScript) {
				tsFiles = append(tsFiles, file)
			}
		case ".vue":
			if validationEnabled(cfg.AutoValidationVue) {
				vueFiles = append(vueFiles, file)
			}
		case ".java":
			if validationEnabled(cfg.AutoValidationJava) {
				javaFiles = append(javaFiles, file)
			}
		}
	}

	if len(jsonFiles) > 0 {
		reports = append(reports, validateJSONFiles(jsonFiles))
	}
	if len(pythonFiles) > 0 {
		reports = append(reports, validatePythonFiles(checkCtx, root, pythonFiles))
	}
	if len(jsFiles) > 0 {
		reports = append(reports, validateJavaScriptFiles(checkCtx, jsFiles))
	}
	if len(goFiles) > 0 {
		reports = append(reports, validateGoFiles(checkCtx, root, goFiles))
	}
	if len(tsFiles) > 0 || len(vueFiles) > 0 {
		reports = append(reports, validateTypeScriptFiles(checkCtx, root, tsFiles, vueFiles)...)
	}
	if len(javaFiles) > 0 {
		reports = append(reports, validateJavaFiles(checkCtx, javaFiles))
	}
	return formatValidationReports(reports)
}

// validationEnabled defaults to off: nil (legacy configs and fresh installs)
// means the language check is disabled until the user opts in per language.
func validationEnabled(flag *bool) bool {
	return flag != nil && *flag
}

// batchValidationPathsContextKey carries the display paths one tool call must
// validate, as planned by planBatchValidation for its tool batch.
type batchValidationPathsContextKey struct{}

// validateChangedFilesForCall validates one tool call's changed files under
// the tool batch's validation plan. Directory-level checks (go vet per
// package, tsc/vue-tsc per tsconfig project) would otherwise run once per
// edit call, so the plan defers a unit to the last batch call touching it and
// hands that call the expanded path set; a call whose paths were all absorbed
// by a later call validates nothing. Without a plan in ctx (sub-agent steps
// before wiring, direct calls) it behaves exactly like validateChangedFiles
// on the given paths.
func (a *App) validateChangedFilesForCall(ctx context.Context, cfg ConfigState, paths []string) string {
	if planned, ok := ctx.Value(batchValidationPathsContextKey{}).([]string); ok {
		paths = planned
	}
	return a.validateChangedFiles(ctx, cfg, paths)
}

// planBatchValidation spreads the local edit/create calls of one tool batch
// over validation units so each unit is checked exactly once per batch: the
// last call in tool-call index order that touches a unit validates it with
// every path sharing that unit, keeping the per-validator output filters
// complete. Units are a Go package directory, a tsconfig project, or a single
// file for per-file checks; later same-path calls cannot execute because the
// write-conflict policy skips them, so conflicted call indexes must be passed
// in skip and never own a unit. The returned map holds the display paths per
// call index — an empty slice means the call validates nothing because all
// its paths were absorbed by a later executing call. Calls that are not local
// edit/create, or whose arguments fail to parse, get no entry and keep the
// plain per-call behavior.
func planBatchValidation(roots []string, calls []openai.ToolCall, skip map[int]bool) map[int][]string {
	if len(roots) == 0 {
		return nil
	}
	root := roots[0]
	type unit struct {
		owner int
		paths []string
	}
	units := map[string]*unit{}
	parsed := map[int][]string{}
	for i, call := range calls {
		if skip[i] {
			continue
		}
		paths := localMutationPathsForValidation(call)
		if len(paths) == 0 {
			continue
		}
		parsed[i] = paths
		for _, p := range paths {
			abs, err := safeJoin(roots, p)
			if err != nil {
				continue
			}
			abs = filepath.Clean(abs)
			key := "file:" + strings.ToLower(abs)
			switch strings.ToLower(filepath.Ext(abs)) {
			case ".go":
				key = "go:" + strings.ToLower(filepath.Dir(abs))
			case ".ts", ".tsx", ".d.ts", ".vue":
				key = "ts:" + nearestFile(filepath.Dir(abs), root, "tsconfig.json")
			}
			u := units[key]
			if u == nil {
				u = &unit{}
				units[key] = u
			}
			u.owner = i
			u.paths = append(u.paths, p)
		}
	}
	if len(parsed) == 0 {
		return nil
	}
	plan := make(map[int][]string, len(parsed))
	for i := range parsed {
		plan[i] = []string{}
	}
	for _, u := range units {
		plan[u.owner] = append(plan[u.owner], u.paths...)
	}
	return plan
}

// localMutationPathsForValidation extracts the workspace display paths one
// tool call would write for validation planning. Only local edit and create
// run model-facing validation today; unparseable or truncated arguments
// yield no paths so the call falls back to plain per-call validation.
func localMutationPathsForValidation(call openai.ToolCall) []string {
	switch normalizeToolName(call.Function.Name) {
	case "edit":
		var req FileTextEdits
		if json.Unmarshal([]byte(call.Function.Arguments), &req) != nil || strings.TrimSpace(req.Path) == "" {
			return nil
		}
		return []string{req.Path}
	case "create":
		var req struct {
			Path string `json:"path"`
		}
		if json.Unmarshal([]byte(call.Function.Arguments), &req) != nil || strings.TrimSpace(req.Path) == "" {
			return nil
		}
		return []string{req.Path}
	}
	return nil
}

func collectValidationFiles(roots []string, paths []string) []validationFile {
	seen := make(map[string]struct{}, len(paths))
	files := make([]validationFile, 0, len(paths))
	for _, raw := range paths {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		abs, err := safeJoin(roots, raw)
		if err != nil {
			continue
		}
		abs = filepath.Clean(abs)
		key := strings.ToLower(abs)
		if _, ok := seen[key]; ok || isGeneratedValidationPath(abs) {
			continue
		}
		info, err := os.Stat(abs)
		if err != nil || info.IsDir() || info.Size() > maxValidationFileBytes {
			continue
		}
		seen[key] = struct{}{}
		files = append(files, validationFile{
			abs:     abs,
			display: filepath.ToSlash(raw),
			ext:     strings.ToLower(filepath.Ext(abs)),
		})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].abs < files[j].abs })
	return files
}

func isGeneratedValidationPath(path string) bool {
	for _, part := range strings.Split(filepath.ToSlash(filepath.Clean(path)), "/") {
		switch strings.ToLower(part) {
		case ".git", "node_modules", "vendor", "dist", "build", "coverage", "out", "target":
			return true
		}
	}
	return false
}

func validateJSONFiles(files []validationFile) validationReport {
	checked := 0
	for _, file := range files {
		// tsconfig/jsconfig files are JSONC (comments allowed); a plain
		// JSON parse would always mis-report them.
		if isJSONCFileName(file.abs) {
			continue
		}
		checked++
		data, err := os.ReadFile(file.abs)
		if err != nil {
			return validationReport{label: "JSON", detail: file.display + ": " + err.Error()}
		}
		data = bytesTrimUTF8BOM(data)
		var value any
		if err := json.Unmarshal(data, &value); err != nil {
			return validationReport{
				label:  "JSON",
				detail: file.display + ": JSON 语法错误: " + compactValidationOutput(err.Error()),
			}
		}
	}
	if checked == 0 {
		return validationReport{label: "JSON", skipped: true, detail: "仅 JSONC 文件，已跳过"}
	}
	return validationReport{label: fmt.Sprintf("JSON %d 个文件", checked), passed: true}
}

// isJSONCFileName reports whether the file is a TypeScript-style config that
// legitimately allows comments and trailing commas.
func isJSONCFileName(path string) bool {
	name := strings.ToLower(filepath.Base(path))
	return strings.HasPrefix(name, "tsconfig") || strings.HasPrefix(name, "jsconfig")
}

func validatePythonFiles(ctx context.Context, root string, files []validationFile) validationReport {
	program, prefix, ok := findPythonCommand(root)
	if !ok {
		return validationReport{label: "Python", skipped: true, detail: "未找到 python/python3/py"}
	}
	tempDir, err := os.MkdirTemp("", "ally-python-check-")
	if err != nil {
		return validationReport{label: "Python", skipped: true, detail: "无法创建临时字节码目录"}
	}
	defer os.RemoveAll(tempDir)
	paths := make([]string, 0, len(files))
	for _, file := range files {
		paths = append(paths, file.abs)
	}
	args := append([]string{}, prefix...)
	args = append(args, "-c", "import os,py_compile,sys\nout=sys.argv[1]\nfor i,p in enumerate(sys.argv[2:]): py_compile.compile(p, cfile=os.path.join(out,str(i)+'.pyc'), doraise=True)", tempDir)
	args = append(args, paths...)
	output, err := runValidationCommand(ctx, filepath.Dir(files[0].abs), program, args...)
	if err != nil {
		if report, handled := validationSkipReport("Python 语法", err); handled {
			return report
		}
		return validationReport{label: "Python 语法", detail: compactValidationOutput(outputOrError(output, err))}
	}
	return validationReport{label: fmt.Sprintf("Python 语法 %d 个文件", len(files)), passed: true}
}

func validateJavaScriptFiles(ctx context.Context, files []validationFile) validationReport {
	program, err := exec.LookPath("node")
	if err != nil {
		return validationReport{label: "JavaScript 语法", skipped: true, detail: "未找到 node"}
	}
	checked := 0
	for _, file := range files {
		output, runErr := runValidationCommand(ctx, filepath.Dir(file.abs), program, "--check", file.abs)
		if runErr != nil {
			// node --check parses plain CommonJS/ESM only. Module-format
			// and JSX diagnostics say nothing about the edit, so treat
			// them as "cannot verify" instead of failures.
			if isJSFormatOnlyError(output) {
				continue
			}
			if report, handled := validationSkipReport("JavaScript 语法", runErr); handled {
				return report
			}
			return validationReport{label: "JavaScript 语法", detail: file.display + ": " + compactValidationOutput(outputOrError(output, runErr))}
		}
		checked++
	}
	if checked == 0 {
		return validationReport{label: "JavaScript 语法", skipped: true, detail: "模块格式无法静态判断，已跳过"}
	}
	return validationReport{label: fmt.Sprintf("JavaScript 语法 %d 个文件", checked), passed: true}
}

// isJSFormatOnlyError reports whether the node --check output only indicates
// module-format/JSX limitations of the checker rather than real syntax errors.
func isJSFormatOnlyError(output string) bool {
	lower := strings.ToLower(output)
	for _, pattern := range []string{
		"outside a module",          // ESM parsed as CJS
		"unexpected token 'export'", // export in CJS
		"token '<'",                 // JSX in a plain .js file
		"await is only valid",       // top-level await in CJS
		"top-level await",
	} {
		if strings.Contains(lower, pattern) {
			return true
		}
	}
	return false
}

func validateGoFiles(ctx context.Context, root string, files []validationFile) validationReport {
	goProgram, err := exec.LookPath("go")
	if err != nil {
		return validationReport{label: "Go vet", skipped: true, detail: "未找到 go"}
	}
	packages := make(map[string]struct{})
	relPaths := make(map[string]struct{}, len(files))
	for _, file := range files {
		rel, relErr := filepath.Rel(root, filepath.Dir(file.abs))
		if relErr != nil || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		pkg := "."
		if rel != "." {
			pkg = "./" + filepath.ToSlash(rel)
		}
		packages[pkg] = struct{}{}
		if fileRel, relErr := filepath.Rel(root, file.abs); relErr == nil {
			relPaths[filepath.ToSlash(fileRel)] = struct{}{}
		}
	}
	if len(packages) == 0 {
		return validationReport{label: "Go vet", skipped: true, detail: "改动文件不在主工作区内"}
	}
	packageList := make([]string, 0, len(packages))
	for pkg := range packages {
		packageList = append(packageList, pkg)
	}
	sort.Strings(packageList)
	for _, pkg := range packageList {
		output, runErr := runValidationCommand(ctx, root, goProgram, "vet", pkg)
		if runErr != nil {
			// Missing modules / go.sum entries are dependency problems,
			// not code the agent broke.
			if goDependencyIssue(output) {
				return validationReport{label: "Go vet", skipped: true, detail: "依赖解析失败，已跳过"}
			}
			if report, handled := validationSkipReport("Go vet "+pkg, runErr); handled {
				return report
			}
			// vet reports whole-package findings; only surface errors that
			// point at files touched by this edit so pre-existing issues
			// elsewhere are not blamed on the change.
			filtered := filterGoVetOutput(output, relPaths)
			if filtered == "" {
				continue
			}
			return validationReport{label: "Go vet " + pkg, detail: filtered}
		}
	}
	return validationReport{label: fmt.Sprintf("Go vet %d 个包", len(packageList)), passed: true}
}

// goDependencyIssue reports whether go vet failed because of module/dependency
// resolution rather than code errors.
func goDependencyIssue(output string) bool {
	for _, pattern := range []string{
		"no required module provides",
		"cannot find main module",
		"cannot find package",
		"is not in GOROOT",
		"missing go.sum entry",
		"outside available modules",
	} {
		if strings.Contains(output, pattern) {
			return true
		}
	}
	return false
}

// filterGoVetOutput keeps only diagnostics whose file path matches one of the
// changed files (slash-separated, relative to the module root). vet prints an
// OS-native separator, an optional "./" prefix, and a "vet(.exe): " prefix in
// failing-tool output; all normalized here.
func filterGoVetOutput(output string, relPaths map[string]struct{}) string {
	if len(relPaths) == 0 {
		return ""
	}
	var kept []string
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		normalized := strings.ReplaceAll(line, "\\", "/")
		normalized = strings.TrimPrefix(normalized, "vet.exe: ")
		normalized = strings.TrimPrefix(normalized, "vet: ")
		normalized = strings.TrimPrefix(normalized, "./")
		for rel := range relPaths {
			if strings.HasPrefix(normalized, rel+":") {
				kept = append(kept, line)
				break
			}
		}
	}
	return compactValidationOutput(strings.Join(kept, "\n"))
}

type typeScriptProject struct {
	config string
	vue    bool
}

func validateTypeScriptFiles(ctx context.Context, root string, tsFiles, vueFiles []validationFile) []validationReport {
	projects := make(map[string]*typeScriptProject)
	all := append(append([]validationFile{}, tsFiles...), vueFiles...)
	for _, file := range all {
		config := nearestFile(filepath.Dir(file.abs), root, "tsconfig.json")
		if config == "" {
			continue
		}
		project := projects[config]
		if project == nil {
			project = &typeScriptProject{config: config}
			projects[config] = project
		}
		if file.ext == ".vue" {
			project.vue = true
		}
	}
	if len(projects) == 0 {
		if len(vueFiles) > 0 {
			return []validationReport{{label: "Vue/TypeScript", skipped: true, detail: "未找到 tsconfig.json"}}
		}
		return []validationReport{{label: "TypeScript", skipped: true, detail: "未找到 tsconfig.json"}}
	}
	reports := make([]validationReport, 0, len(projects))
	configs := make([]string, 0, len(projects))
	for config := range projects {
		configs = append(configs, config)
	}
	sort.Strings(configs)
	for _, config := range configs {
		project := projects[config]
		name := "TypeScript 语法"
		program, prefix, ok := findNodeProjectTool(filepath.Dir(config), root, "tsc")
		if project.vue {
			name = "Vue 语法"
			program, prefix, ok = findNodeProjectTool(filepath.Dir(config), root, "vue-tsc")
		}
		if !ok {
			reports = append(reports, validationReport{label: name, skipped: true, detail: "未找到校验工具"})
			continue
		}
		args := append([]string{}, prefix...)
		args = append(args, "--noEmit", "--pretty", "false", "--project", config)
		output, runErr := runValidationCommand(ctx, filepath.Dir(config), program, args...)
		if runErr != nil {
			if report, handled := validationSkipReport(name, runErr); handled {
				reports = append(reports, report)
				continue
			}
			// A whole-project type check fails on pre-existing type errors,
			// missing node_modules, etc. Only syntax-level errors (TS1xxx)
			// in files touched by this edit are worth reporting.
			filtered := filterTypeScriptSyntaxErrors(output, filepath.Dir(config), all)
			if filtered == "" {
				reports = append(reports, validationReport{label: name, skipped: true, detail: "改动文件无语法错误（其余错误已忽略）"})
				continue
			}
			reports = append(reports, validationReport{label: name, detail: filtered})
		} else {
			reports = append(reports, validationReport{label: name, passed: true})
		}
	}
	return reports
}

var typeScriptDiagnosticPattern = regexp.MustCompile(`^(.+?)\(\d+,\d+\): error TS1\d{3}:`)

// filterTypeScriptSyntaxErrors keeps only syntax-level diagnostics (error codes
// TS1000–TS1999) that point at one of the changed files. Semantic type errors
// (TS2xxx) and errors in untouched files are dropped: they are usually
// pre-existing or dependency-related and would be blamed on the edit.
func filterTypeScriptSyntaxErrors(output, configDir string, files []validationFile) string {
	changed := make(map[string]struct{}, len(files))
	for _, file := range files {
		changed[strings.ToLower(filepath.Clean(file.abs))] = struct{}{}
	}
	var kept []string
	for _, line := range strings.Split(output, "\n") {
		m := typeScriptDiagnosticPattern.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		path := filepath.Clean(m[1])
		if !filepath.IsAbs(path) {
			path = filepath.Join(configDir, path)
		}
		if _, ok := changed[strings.ToLower(path)]; !ok {
			continue
		}
		kept = append(kept, strings.TrimSpace(line))
	}
	return compactValidationOutput(strings.Join(kept, "\n"))
}

func validateJavaFiles(ctx context.Context, files []validationFile) validationReport {
	program, err := exec.LookPath("javac")
	if err != nil {
		return validationReport{label: "Java 语法", skipped: true, detail: "未找到 javac"}
	}
	tempDir, err := os.MkdirTemp("", "ally-java-check-")
	if err != nil {
		return validationReport{label: "Java 语法", skipped: true, detail: "无法创建临时输出目录"}
	}
	defer os.RemoveAll(tempDir)
	args := []string{"-proc:none", "-encoding", "UTF-8", "-d", tempDir}
	for _, file := range files {
		args = append(args, file.abs)
	}
	output, runErr := runValidationCommand(ctx, filepath.Dir(files[0].abs), program, args...)
	if runErr != nil {
		if report, handled := validationSkipReport("Java 语法", runErr); handled {
			return report
		}
		// javac without a classpath always fails on imports; only surface
		// parse-level syntax errors and ignore dependency/type errors.
		filtered := filterJavaSyntaxErrors(output)
		if filtered == "" {
			return validationReport{label: "Java 语法", skipped: true, detail: "编译失败但无语法错误（依赖或类型错误，已忽略）"}
		}
		return validationReport{label: "Java 语法", detail: filtered}
	}
	return validationReport{label: fmt.Sprintf("Java 语法 %d 个文件", len(files)), passed: true}
}

// javaSyntaxErrorPatterns matches parse-level javac diagnostics ("';'
// expected", "reached end of file while parsing", ...) while excluding
// attribution-stage errors such as "cannot find symbol" or "package x does
// not exist" that merely reflect the missing classpath.
var javaSyntaxErrorPatterns = []string{
	"expected",        // '(' expected, ';' expected, <identifier> expected, ...
	"illegal start",   // illegal start of expression/type
	"illegal character",
	"reached end of file while parsing",
	"unclosed",        // unclosed string/character literal/comment
	"not a statement",
	"without",         // 'catch' without 'try', 'else' without 'if', ...
	"outside",         // return/break/continue outside ...
	"illegal underscore",
	"variable declaration not allowed here",
}

func filterJavaSyntaxErrors(output string) string {
	var kept []string
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		idx := strings.Index(line, "error:")
		if idx < 0 {
			continue
		}
		message := strings.ToLower(line[idx:])
		for _, pattern := range javaSyntaxErrorPatterns {
			if strings.Contains(message, pattern) {
				kept = append(kept, line)
				break
			}
		}
	}
	return compactValidationOutput(strings.Join(kept, "\n"))
}

func findPythonCommand(root string) (string, []string, bool) {
	// Prefer a project-local virtualenv: the system interpreter may be older
	// than the project's language level and would mis-report valid syntax.
	for _, dir := range []string{".venv", "venv"} {
		for _, candidate := range []string{
			filepath.Join(root, dir, "Scripts", "python.exe"),
			filepath.Join(root, dir, "bin", "python"),
			filepath.Join(root, dir, "bin", "python3"),
		} {
			if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
				return candidate, nil, true
			}
		}
	}
	for _, name := range []string{"python", "python3"} {
		if path, err := exec.LookPath(name); err == nil {
			return path, nil, true
		}
	}
	if path, err := exec.LookPath("py"); err == nil {
		return path, []string{"-3"}, true
	}
	return "", nil, false
}

func findNodeProjectTool(dir, root, name string) (string, []string, bool) {
	node, nodeErr := exec.LookPath("node")
	if nodeErr == nil {
		for current := filepath.Clean(dir); ; current = filepath.Dir(current) {
			candidates := []string{}
			switch name {
			case "tsc":
				candidates = []string{
					filepath.Join(current, "node_modules", "typescript", "bin", "tsc"),
					filepath.Join(current, "node_modules", "typescript", "bin", "tsc.js"),
				}
			case "vue-tsc":
				candidates = []string{
					filepath.Join(current, "node_modules", "vue-tsc", "bin", "vue-tsc.js"),
				}
			}
			for _, candidate := range candidates {
				if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
					return node, []string{candidate}, true
				}
			}
			if current == root || filepath.Dir(current) == current {
				break
			}
		}
	}
	if program, err := exec.LookPath(name); err == nil && !strings.HasSuffix(strings.ToLower(program), ".cmd") && !strings.HasSuffix(strings.ToLower(program), ".bat") {
		return program, nil, true
	}
	return "", nil, false
}

func nearestFile(dir, root, name string) string {
	for current := filepath.Clean(dir); ; current = filepath.Dir(current) {
		candidate := filepath.Join(current, name)
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return filepath.Clean(candidate)
		}
		if current == root || filepath.Dir(current) == current {
			return ""
		}
	}
}

func runValidationCommand(ctx context.Context, dir, program string, args ...string) (string, error) {
	buf := &limitedBuffer{limit: autoValidationOutputBytes}
	cmd := exec.CommandContext(ctx, program, args...)
	cmd.Dir = dir
	hideCommandWindow(cmd)
	cmd.Stdout = buf
	cmd.Stderr = buf
	err := cmd.Run()
	if ctxErr := ctx.Err(); ctxErr != nil {
		return buf.String(), ctxErr
	}
	return buf.String(), err
}

func outputOrError(output string, err error) string {
	output = strings.TrimSpace(output)
	if output != "" {
		return output
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "超时"
	}
	if errors.Is(err, context.Canceled) {
		return "已取消"
	}
	return err.Error()
}

// validationSkipReport maps timeouts and cancellations to skipped reports so
// slow tooling is never surfaced to the model as a validation failure.
func validationSkipReport(label string, err error) (validationReport, bool) {
	if errors.Is(err, context.DeadlineExceeded) {
		return validationReport{label: label, skipped: true, detail: "校验超时，已跳过"}, true
	}
	if errors.Is(err, context.Canceled) {
		return validationReport{label: label, skipped: true, detail: "已取消"}, true
	}
	return validationReport{}, false
}

func compactValidationOutput(output string) string {
	output = strings.TrimSpace(output)
	if len(output) > autoValidationOutputBytes {
		output = output[:autoValidationOutputBytes] + "..."
	}
	return output
}

func formatValidationReports(reports []validationReport) string {
	var passed, failed, skipped []string
	for _, report := range reports {
		switch {
		case report.skipped:
			skipped = append(skipped, report.label+"（"+report.detail+"）")
		case report.passed:
			passed = append(passed, report.label)
		default:
			failed = append(failed, report.label+": "+report.detail)
		}
	}
	if len(failed) > 0 {
		message := "自动校验失败（文件已写入）：\n- " + strings.Join(failed, "\n- ")
		if len(skipped) > 0 {
			message += "\n自动校验跳过：" + strings.Join(skipped, "；")
		}
		return message
	}
	if len(passed) > 0 {
		message := "自动校验通过：" + strings.Join(passed, "；")
		if len(skipped) > 0 {
			message += "；部分跳过：" + strings.Join(skipped, "；")
		}
		return message
	}
	if len(skipped) > 0 {
		return "自动校验跳过：" + strings.Join(skipped, "；")
	}
	return ""
}

func bytesTrimUTF8BOM(data []byte) []byte {
	if len(data) >= 3 && data[0] == 0xef && data[1] == 0xbb && data[2] == 0xbf {
		return data[3:]
	}
	return data
}
