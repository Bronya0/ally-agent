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
	"sort"
	"strconv"
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
		reports = append(reports, validateJSONFiles(checkCtx, jsonFiles))
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
type batchValidationBudgetContextKey struct{}

// Stage validation runs serially after concurrent writes finish. Only checks
// consume this shared budget; writes must not exhaust it.
type batchValidationBudget struct{ remaining time.Duration }

// validateChangedFilesForCall honors deferred stage validation and its shared
// time budget. An empty planned path set suppresses inline checks while peers
// are still writing. Direct tool calls validate their own paths as usual.
func (a *App) validateChangedFilesForCall(ctx context.Context, cfg ConfigState, paths []string) string {
	if planned, ok := ctx.Value(batchValidationPathsContextKey{}).([]string); ok {
		paths = planned
	}
	if budget, ok := ctx.Value(batchValidationBudgetContextKey{}).(*batchValidationBudget); ok && budget != nil && len(paths) > 0 {
		started := time.Now()
		checkCtx, cancel := context.WithTimeout(ctx, budget.remaining)
		defer func() {
			cancel()
			budget.remaining -= time.Since(started)
		}()
		ctx = checkCtx
	}
	return a.validateChangedFiles(ctx, cfg, paths)
}

// planBatchValidation spreads the local edit/create calls of one tool batch
// over validation units so each unit is checked exactly once per stage: the
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
		if _, ok := seen[key]; ok || isGeneratedValidationFile(roots, abs) {
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

func isGeneratedValidationFile(roots []string, abs string) bool {
	for _, root := range roots {
		if insideRoot(root, abs) {
			rel, err := filepath.Rel(root, abs)
			if err == nil && !isGeneratedValidationPath(rel) {
				return false
			}
		}
	}
	return true
}

func validateJSONFiles(ctx context.Context, files []validationFile) validationReport {
	checked := 0
	for _, file := range files {
		if report, handled := validationSkipReport("JSON", ctx.Err()); handled {
			return report
		}
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
		if !json.Valid(data) {
			var value any
			err := json.Unmarshal(data, &value)
			detail := err.Error()
			var syntax *json.SyntaxError
			if errors.As(err, &syntax) {
				line, column := 1, 1
				offset := int(syntax.Offset)
				if offset > 0 && syntax.Error() != "unexpected end of JSON input" {
					offset--
				}
				for _, ch := range string(data[:offset]) {
					if ch == '\n' {
						line, column = line+1, 1
					} else {
						column++
					}
				}
				detail = fmt.Sprintf("%d:%d: %s", line, column, detail)
			}
			return validationReport{
				label:  "JSON",
				detail: file.display + ":" + detail,
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
	program, prefix, ok := findPythonCommand()
	if !ok {
		return validationReport{label: "Python", skipped: true, detail: "未找到 python/python3/py"}
	}
	tempDir, err := os.MkdirTemp("", "ally-python-check-")
	if err != nil {
		return validationReport{label: "Python", skipped: true, detail: "无法创建临时字节码目录"}
	}
	// 统一出口：删除前断言目标在系统临时根内（不得逃逸到上级）。
	defer func() { _ = removeTempTree(tempDir) }()
	paths := make([]string, 0, len(files))
	for _, file := range files {
		paths = append(paths, file.abs)
	}
	args := append([]string{}, prefix...)
	args = append(args, "-I", "-S", "-B", "-c", `import sys
required = tuple(map(int, sys.argv[1].split('.')))
if sys.version_info[:2] < required:
 print('Python %d.%d 低于项目要求 %s，已跳过' % (*sys.version_info[:2], sys.argv[1]))
 sys.exit(2)
for p in sys.argv[2:]:
 with open(p,'rb') as f: compile(f.read(),p,'exec')
`, pythonValidationMinimum(root, files))
	args = append(args, paths...)
	output, err := runValidationCommand(ctx, tempDir, program, args...)
	if err != nil {
		if report, handled := validationSkipReport("Python 语法", err); handled {
			return report
		}
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 2 {
			return validationReport{label: "Python 语法", skipped: true, detail: compactValidationOutput(output)}
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
	// Compile in one trusted Node process. Neither parser executes the source;
	// ambiguous .js files may use CJS or ESM syntax without guessing from text.
	args := []string{"--no-warnings", "--experimental-vm-modules", "-e", `
const fs = require('node:fs'), vm = require('node:vm'), path = require('node:path');
for (const file of process.argv.slice(1)) {
  const source = fs.readFileSync(file, 'utf8').replace(/^#![^\r\n]*/, '');
  const ext = path.extname(file).toLowerCase();
  try {
    if (ext === '.mjs') new vm.SourceTextModule(source, { identifier: file });
    else {
      try { vm.compileFunction(source, ['exports', 'require', 'module', '__filename', '__dirname'], { filename: file }); }
      catch (error) {
        if (ext === '.cjs') throw error;
        new vm.SourceTextModule(source, { identifier: file });
      }
    }
  } catch (error) { console.error(file + ': ' + error.stack); process.exit(1); }
}`}
	for _, file := range files {
		args = append(args, file.abs)
	}
	output, runErr := runValidationCommand(ctx, filepath.Dir(files[0].abs), program, args...)
	if runErr != nil {
		if report, handled := validationSkipReport("JavaScript 语法", runErr); handled {
			return report
		}
		return validationReport{label: "JavaScript 语法", detail: compactValidationOutput(outputOrError(output, runErr))}
	}
	return validationReport{label: fmt.Sprintf("JavaScript 语法 %d 个文件", len(files)), passed: true}
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
	{
		args := append([]string{"vet", "-p=2"}, packageList...)
		output, runErr := runValidationCommand(ctx, root, goProgram, args...)
		if runErr != nil {
			// Missing modules / go.sum entries are dependency problems,
			// not code the agent broke.
			if goDependencyIssue(output) {
				return validationReport{label: "Go vet", skipped: true, detail: "依赖解析失败，已跳过"}
			}
			if report, handled := validationSkipReport("Go vet", runErr); handled {
				return report
			}
			// vet reports whole-package findings; only surface errors that
			// point at files touched by this edit so pre-existing issues
			// elsewhere are not blamed on the change.
			filtered := filterGoVetOutput(output, relPaths)
			if filtered == "" {
				filtered = compactValidationOutput(outputOrError(output, runErr))
				// Compiler/type errors use a vet: prefix and may be caused by
				// an edited API. Ordinary findings in untouched files are not
				// attributable to this edit, so retain them as skipped detail.
				if strings.Contains(output, ".go:") && !strings.Contains(output, "vet: ") && !strings.Contains(output, "vet.exe: ") {
					return validationReport{label: "Go vet", skipped: true, detail: "未改动文件中的诊断，未归因于本次改动：\n" + filtered}
				}
			}
			return validationReport{label: "Go vet", detail: filtered}
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
	// Materialize the "path:" prefixes once: the inner loop runs per output
	// line, and the map iteration order cannot affect the result (it only
	// decides whether this line is kept).
	prefixes := make([]string, 0, len(relPaths))
	for rel := range relPaths {
		prefixes = append(prefixes, rel+":")
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
		for _, prefix := range prefixes {
			if strings.HasPrefix(normalized, prefix) {
				kept = append(kept, line)
				break
			}
		}
	}
	return compactValidationOutput(strings.Join(kept, "\n"))
}

func validateTypeScriptFiles(ctx context.Context, root string, tsFiles, vueFiles []validationFile) []validationReport {
	node, err := exec.LookPath("node")
	if err != nil {
		return []validationReport{{label: "Vue/TypeScript 语法", skipped: true, detail: "未找到 node"}}
	}
	// Parse only changed sources: no project graph, tsconfig plugins, type
	// checking or incremental output. One process per installed parser pair.
	type parserGroup struct {
		ts, vue string
		files   []validationFile
	}
	groups := map[string]*parserGroup{}
	var reports []validationReport
	for _, file := range append(append([]validationFile{}, tsFiles...), vueFiles...) {
		dir := filepath.Dir(file.abs)
		ts := findNodeValidationModule(dir, root, "typescript/lib/typescript.js")
		vue := ""
		if file.ext == ".vue" {
			vue = findNodeValidationModule(dir, root, "@vue/compiler-sfc/dist/compiler-sfc.cjs.js")
		}
		if (file.ext != ".vue" && ts == "") || (file.ext == ".vue" && vue == "") {
			reports = append(reports, validationReport{label: file.display, skipped: true, detail: "未找到语法解析器"})
			continue
		}
		key := ts + "\x00" + vue
		if groups[key] == nil {
			groups[key] = &parserGroup{ts: ts, vue: vue}
		}
		groups[key].files = append(groups[key].files, file)
	}
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		group := groups[key]
		args := []string{"--no-warnings", "--experimental-vm-modules", "-e", nodeTypedSyntaxScript, group.ts, group.vue}
		for _, file := range group.files {
			args = append(args, file.abs)
		}
		output, runErr := runValidationCommand(ctx, root, node, args...)
		label := fmt.Sprintf("Vue/TypeScript 语法 %d 个文件", len(group.files))
		if runErr != nil {
			if report, handled := validationSkipReport(label, runErr); handled {
				reports = append(reports, report)
			} else {
				reports = append(reports, validationReport{label: label, detail: compactValidationOutput(outputOrError(output, runErr))})
			}
		} else if strings.TrimSpace(output) != "" {
			// The parser explicitly reports unsupported external scripts or a
			// missing TS parser; never label partially checked files as passed.
			reports = append(reports, validationReport{label: label, skipped: true, detail: compactValidationOutput(output)})
		} else {
			reports = append(reports, validationReport{label: label, passed: true})
		}
	}
	return reports
}

const nodeTypedSyntaxScript = `
const fs = require('node:fs'), vm = require('node:vm');
const [tsPath, vuePath, ...files] = process.argv.slice(1);
const ts = tsPath ? require(tsPath) : null, vue = vuePath ? require(vuePath) : null;
let failed = false;
function failure(message) { failed = true; console.error(message); }
function script(source, file, lang) {
  if (ts) {
    const kind = lang === 'tsx' ? ts.ScriptKind.TSX : lang === 'jsx' ? ts.ScriptKind.JSX : lang === 'ts' ? ts.ScriptKind.TS : ts.ScriptKind.JS;
    const parsed = ts.createSourceFile(file, source, ts.ScriptTarget.Latest, false, kind);
    for (const d of parsed.parseDiagnostics) {
      const p = parsed.getLineAndCharacterOfPosition(d.start || 0);
      failure(file + ':' + (p.line+1) + ':' + (p.character+1) + ': ' + ts.flattenDiagnosticMessageText(d.messageText, '\n'));
    }
  } else if (lang === 'ts' || lang === 'tsx' || lang === 'jsx') console.log(file + ': 缺少 TypeScript 语法解析器，已跳过脚本');
  else new vm.SourceTextModule(source, { identifier: file });
}
for (const file of files) {
  try {
    const source = fs.readFileSync(file, 'utf8');
    if (!file.toLowerCase().endsWith('.vue')) { script(source, file, file.toLowerCase().endsWith('.tsx') ? 'tsx' : 'ts'); continue; }
    const parsed = vue.parse(source, { filename: file });
    for (const error of parsed.errors) failure(file + ': ' + (error.message || error));
    const d = parsed.descriptor;
    for (const block of [d.script, d.scriptSetup]) if (block) {
      if (block.src) console.log(file + ': 外部脚本未检查');
      else script(block.content, file, block.lang || 'js');
    }
    if (d.template) {
      if (d.template.src || (d.template.lang && d.template.lang !== 'html')) console.log(file + ': 外部或预处理模板未检查');
      else {
        const template = vue.compileTemplate({ source: d.template.content, filename: file, id: 'ally-syntax', compilerOptions: { expressionPlugins: ['typescript'] } });
        for (const error of template.errors) failure(file + ': ' + (error.message || error));
      }
    }
  } catch (error) { failure(file + ': ' + error.stack); }
}
if (failed) process.exit(1);
`

func findNodeValidationModule(dir, root, module string) string {
	for current := filepath.Clean(dir); ; current = filepath.Dir(current) {
		candidate := filepath.Join(current, "node_modules", filepath.FromSlash(module))
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
		if current == filepath.Clean(root) || filepath.Dir(current) == current {
			return ""
		}
	}
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
	defer func() { _ = removeTempTree(tempDir) }()
	args := []string{"-J-Duser.language=en", "-J-Duser.country=US", "-proc:none", "-encoding", "UTF-8", "-d", tempDir}
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
	"expected",      // '(' expected, ';' expected, <identifier> expected, ...
	"illegal start", // illegal start of expression/type
	"illegal character",
	"reached end of file while parsing",
	"unclosed", // unclosed string/character literal/comment
	"not a statement",
	"without", // 'catch' without 'try', 'else' without 'if', ...
	"outside", // return/break/continue outside ...
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

// Read literal language-version declarations only; never execute project config.
// Ignore upper bounds/exclusions since they do not establish a minimum version.
func pythonValidationMinimum(root string, files []validationFile) string {
	major, minor := 3, 0
	consider := func(value string) {
		parts := strings.Split(strings.TrimSpace(value), ".")
		if len(parts) < 2 {
			return
		}
		m, err := strconv.Atoi(parts[0])
		n, minorErr := strconv.Atoi(parts[1])
		if err == nil && minorErr == nil && (m > major || (m == major && n > minor)) {
			major, minor = m, n
		}
	}
	seen := map[string]bool{}
	for _, file := range files {
		for _, name := range []string{".python-version", "pyproject.toml"} {
			path := nearestFile(filepath.Dir(file.abs), root, name)
			if path == "" || seen[path] {
				continue
			}
			seen[path] = true
			data, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			if name == ".python-version" {
				fields := strings.Fields(string(data))
				if len(fields) > 0 {
					consider(fields[0])
				}
				continue
			}
			inProject := false
			for _, line := range strings.Split(string(data), "\n") {
				line = strings.TrimSpace(line)
				if strings.HasPrefix(line, "[") {
					section, _, _ := strings.Cut(line, "#")
					inProject = strings.TrimSpace(section) == "[project]"
					continue
				}
				key, value, ok := strings.Cut(line, "=")
				if !inProject || !ok || strings.Trim(strings.TrimSpace(key), "\"'") != "requires-python" {
					continue
				}
				value = strings.TrimSpace(value)
				if len(value) < 2 || (value[0] != '\'' && value[0] != '"') {
					continue
				}
				value, _, ok = strings.Cut(value[1:], value[:1])
				if !ok {
					continue
				}
				for _, spec := range strings.Split(value, ",") {
					spec = strings.TrimSpace(spec)
					for _, op := range []string{">=", "==", "~=", ">"} {
						if strings.HasPrefix(spec, op) {
							consider(strings.TrimSpace(strings.TrimPrefix(spec, op)))
							break
						}
					}
				}
			}
		}
	}
	return fmt.Sprintf("%d.%d", major, minor)
}

func findPythonCommand() (string, []string, bool) {
	// Never automatically execute an interpreter supplied by the repository.
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
	cmd.Env = validationCommandEnv()
	cmd.WaitDelay = 250 * time.Millisecond
	hideCommandWindow(cmd)
	cmd.Stdout = buf
	cmd.Stderr = buf
	err := cmd.Run()
	if ctxErr := ctx.Err(); ctxErr != nil {
		return buf.String(), ctxErr
	}
	return buf.String(), err
}

// Validation must not inherit interpreter preloads, compiler wrappers or
// network/toolchain auto-download settings from the interactive shell.
func validationCommandEnv() []string {
	blocked := map[string]bool{
		"NODE_OPTIONS": true, "PYTHONPATH": true, "PYTHONHOME": true,
		"JAVA_TOOL_OPTIONS": true, "JDK_JAVA_OPTIONS": true, "JDK_JAVAC_OPTIONS": true, "_JAVA_OPTIONS": true,
		"GOFLAGS": true, "GOTOOLCHAIN": true, "GOPROXY": true, "GOSUMDB": true, "GOENV": true,
	}
	env := make([]string, 0, len(os.Environ())+4)
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if !blocked[strings.ToUpper(key)] {
			env = append(env, entry)
		}
	}
	return append(env, "GOTOOLCHAIN=local", "GOPROXY=off", "GOSUMDB=off", "GOENV=off")
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
