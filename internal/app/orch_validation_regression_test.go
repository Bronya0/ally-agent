// SPDX-License-Identifier: GPL-3.0-only
package app

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	openai "github.com/sashabaranov/go-openai"
)

// validationGoCache 是所有校验用例共享的临时 Go 构建缓存目录。每次自动校验都
// 用一份全新 GOCACHE 时，`go vet` 要冷编译一遍标准库（实测 1s 级，而热缓存只有
// 0.2s 级）。共享目录仍然是本进程新建的临时目录，绝不落到用户真实的
// ~/.cache/go-build，隔离性不变。
var validationGoCache struct {
	once    sync.Once
	root    string
	goCache string
	goMod   string
}

func validationGoCachePaths() (goCache, goMod string) {
	validationGoCache.once.Do(func() {
		dir, err := os.MkdirTemp("", "ally-validation-gocache-")
		if err != nil {
			return
		}
		validationGoCache.root = dir
		validationGoCache.goCache = filepath.Join(dir, "cache")
		validationGoCache.goMod = filepath.Join(dir, "modules")
	})
	return validationGoCache.goCache, validationGoCache.goMod
}

// cleanupValidationGoCache removes the shared throwaway cache; TestMain calls it.
func cleanupValidationGoCache() {
	if validationGoCache.root != "" {
		_ = os.RemoveAll(validationGoCache.root)
	}
}

func validationSandbox(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, key := range []string{"HOME", "USERPROFILE", "TEMP", "TMP", "TMPDIR"} {
		t.Setenv(key, dir)
	}
	goCache, goMod := validationGoCachePaths()
	if goCache == "" {
		// 共享目录建不出来时退回用例私有缓存：宁可慢，也不能碰用户缓存。
		goCache, goMod = filepath.Join(dir, "cache"), filepath.Join(dir, "modules")
	}
	t.Setenv("GOCACHE", goCache)
	t.Setenv("GOMODCACHE", goMod)
	t.Setenv("GOWORK", "off")
	root := filepath.Join(dir, "workspace")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestValidationPythonDoesNotImportWorkspaceModules(t *testing.T) {
	root := validationSandbox(t)
	if _, _, ok := findPythonCommand(); !ok {
		t.Skip("Python unavailable")
	}
	writeToolTestFile(t, root, "py_compile.py", "raise RuntimeError('WORKSPACE_MODULE_EXECUTED')\n")
	writeToolTestFile(t, root, "sitecustomize.py", "raise RuntimeError('WORKSPACE_MODULE_EXECUTED')\n")
	t.Setenv("PYTHONPATH", root)
	writeToolTestFile(t, root, "good.py", "value = 1\n")
	writeToolTestFile(t, root, "bad.py", "def broken(:\n pass\n")
	for _, tc := range []struct {
		path   string
		passed bool
	}{{"good.py", true}, {"bad.py", false}} {
		r := validatePythonFiles(context.Background(), root, collectValidationFiles([]string{root}, []string{tc.path}))
		if r.passed != tc.passed || r.skipped || strings.Contains(r.detail, "WORKSPACE_MODULE_EXECUTED") {
			t.Fatalf("%s: %+v", tc.path, r)
		}
	}
}

func TestValidationJSONReportsLineAndColumn(t *testing.T) {
	for _, tc := range []struct{ name, source, location, message string }{
		{"invalid character", "{\n  \"value\": @\n}\n", "2:12", "invalid character '@'"},
		{"empty", "", "1:1", "unexpected end of JSON input"},
		{"truncated", "{\n", "2:1", "unexpected end of JSON input"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := validationSandbox(t)
			writeToolTestFile(t, root, "bad.json", tc.source)
			r := validateJSONFiles(context.Background(), collectValidationFiles([]string{root}, []string{"bad.json"}))
			if r.passed || r.skipped || !strings.Contains(r.detail, "bad.json:"+tc.location+":") || !strings.Contains(r.detail, tc.message) {
				t.Fatalf("missing JSON diagnostic location: %+v", r)
			}
		})
	}
}

func TestValidationPythonProjectMinimum(t *testing.T) {
	for _, tc := range []struct{ name, versionFile, project, want string }{
		{"none", "", "", "3.0"},
		{"pyenv patch", "3.12.2\n", "", "3.12"},
		{"lower bound", "", "[project]\nrequires-python = '>=3.12, <4'\n", "3.12"},
		{"upper bound", "", "[project]\nrequires-python = '<3.12'\n", "3.0"},
		{"both", "3.11\n", "[project]\nrequires-python = '~=3.13.0'\n", "3.13"},
		{"other section", "", "[tool.example]\nrequires-python = '>=3.99'\n", "3.0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := validationSandbox(t)
			writeToolTestFile(t, root, ".python-version", tc.versionFile)
			writeToolTestFile(t, root, "pyproject.toml", tc.project)
			if got := pythonValidationMinimum(root, []validationFile{{abs: filepath.Join(root, "nested", "source.py")}}); got != tc.want {
				t.Fatalf("minimum = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestValidationPythonSkipsOlderInterpreter(t *testing.T) {
	root := validationSandbox(t)
	if _, _, ok := findPythonCommand(); !ok {
		t.Skip("Python unavailable")
	}
	// A future language level must be skipped before attempting compilation.
	writeToolTestFile(t, root, ".python-version", "99.0\n")
	writeToolTestFile(t, root, "new_syntax.py", "type X = int\n")
	r := validatePythonFiles(context.Background(), root, collectValidationFiles([]string{root}, []string{"new_syntax.py"}))
	if !r.skipped || r.passed || !strings.Contains(r.detail, "低于项目要求 99.0") {
		t.Fatalf("outdated interpreter reported syntax failure: %+v", r)
	}
}

func TestValidationJavaScriptBatchParsesWithoutExecuting(t *testing.T) {
	root := validationSandbox(t)
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("Node unavailable")
	}
	marker := filepath.Join(root, "preload.marker")
	writeToolTestFile(t, root, "preload.cjs", "require('node:fs').writeFileSync("+strconv.Quote(marker)+",'executed');\n")
	t.Setenv("NODE_OPTIONS", "--require="+strconv.Quote(filepath.Join(root, "preload.cjs")))
	writeToolTestFile(t, root, "good.cjs", "throw new Error('SOURCE_EXECUTED'); return;\n")
	writeToolTestFile(t, root, "good.mjs", "import item from './missing.mjs'; await item; throw new Error('SOURCE_EXECUTED');\n")
	writeToolTestFile(t, root, "module.js", "export const item = await Promise.resolve(1);\n")
	writeToolTestFile(t, root, "bad.cjs", "const value = ; // outside a module\n")
	valid := []string{"good.cjs", "good.mjs", "module.js"}
	r := validateJavaScriptFiles(context.Background(), collectValidationFiles([]string{root}, valid))
	if !r.passed {
		t.Fatalf("valid batch: %+v", r)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("preload executed: %v", err)
	}
	r = validateJavaScriptFiles(context.Background(), collectValidationFiles([]string{root}, append(valid, "bad.cjs")))
	if r.passed || r.skipped || !strings.Contains(r.detail, "bad.cjs") {
		t.Fatalf("invalid batch: %+v", r)
	}
}

func TestValidationGoReportsErrorsInCallers(t *testing.T) {
	root := validationSandbox(t)
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("Go unavailable")
	}
	writeToolTestFile(t, root, "go.mod", "module example.invalid/check\ngo 1.23\n")
	writeToolTestFile(t, root, "api.go", "package check\nfunc Value() string { return \"changed\" }\n")
	writeToolTestFile(t, root, "caller.go", "package check\nvar Count int = Value()\n")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	r := validateGoFiles(ctx, root, collectValidationFiles([]string{root}, []string{"api.go"}))
	if r.passed || r.skipped || !strings.Contains(r.detail, "caller.go") {
		t.Fatalf("lost caller error: %+v", r)
	}
}

func TestValidationGoSkipsUntouchedVetFindings(t *testing.T) {
	root := validationSandbox(t)
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("Go unavailable")
	}
	writeToolTestFile(t, root, "go.mod", "module example.invalid/check\ngo 1.23\n")
	writeToolTestFile(t, root, "api.go", "package check\nfunc Value() int { return 1 }\n")
	writeToolTestFile(t, root, "untouched.go", "package check\ntype Data struct { Value int `json:broken` }\n")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	r := validateGoFiles(ctx, root, collectValidationFiles([]string{root}, []string{"api.go"}))
	if !r.skipped || r.passed || !strings.Contains(r.detail, "untouched.go") {
		t.Fatalf("untouched vet finding blamed on edit: %+v", r)
	}
}

func TestValidationJavaIgnoresInjectedLocale(t *testing.T) {
	root := validationSandbox(t)
	if _, err := exec.LookPath("javac"); err != nil {
		t.Skip("javac unavailable")
	}
	t.Setenv("JAVA_TOOL_OPTIONS", "-Duser.language=zh -Duser.country=CN")
	writeToolTestFile(t, root, "Broken.java", "public class Broken { void method( { }\n")
	r := validateJavaFiles(context.Background(), collectValidationFiles([]string{root}, []string{"Broken.java"}))
	if r.passed || r.skipped || !strings.Contains(r.detail, "Broken.java") {
		t.Fatalf("lost syntax error: %+v", r)
	}
}

func TestStageValidationChecksSuccessfulWritesWhenPeerFails(t *testing.T) {
	root := validationSandbox(t)
	writeToolTestFile(t, root, "go.mod", "module example.com/check\n\ngo 1.22\n")
	writeToolTestFile(t, root, "second.go", "package check\n")
	calls := []openai.ToolCall{
		{Function: openai.FunctionCall{Name: "create", Arguments: `{"path":"first.go","content":"package check\nfunc Broken( {\n"}`}},
		{Function: openai.FunctionCall{Name: "create", Arguments: `{"path":"second.go","content":"package check\n"}`}},
	}
	a := NewApp()
	enabled := true
	cfg := ConfigState{Workspace: root, AutoValidationGo: &enabled}
	results := make([]toolResult, len(calls))
	a.executeStagedToolBatch(context.Background(), cfg, calls, nil,
		func(ctx context.Context, _ int, call openai.ToolCall) toolResult {
			return a.executeTool(ctx, cfg, "", call.Function.Name, []byte(call.Function.Arguments))
		}, func(i int, _ openai.ToolCall, r toolResult, _ int64) { results[i] = r })
	if !results[0].OK || results[1].OK {
		t.Fatalf("expected successful first write and rejected overwrite: %+v", results)
	}
	edit, ok := results[0].Data.(EditResult)
	if !ok || !strings.Contains(edit.Validation, "自动校验失败") || !strings.Contains(edit.Validation, "first.go") {
		t.Fatalf("successful write lost its validation when its peer failed: %+v", results[0])
	}
}

func TestValidationTypeScriptUsesParserWithoutProjectBuild(t *testing.T) {
	root := validationSandbox(t)
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("Node unavailable")
	}
	writeToolTestFile(t, root, "node_modules/typescript/lib/typescript.js", `
exports.ScriptKind = { TS: 1, TSX: 2, JS: 3, JSX: 4 };
exports.ScriptTarget = { Latest: 99 };
exports.createSourceFile = (file, source) => ({parseDiagnostics: source.includes('BROKEN') ? [{messageText:'bad syntax', start:0}] : [], getLineAndCharacterOfPosition: () => ({line:0,character:0})});
exports.flattenDiagnosticMessageText = text => text;
`)
	writeToolTestFile(t, root, "good.ts", "const value: number = 1;\n")
	writeToolTestFile(t, root, "bad.ts", "BROKEN\n")
	writeToolTestFile(t, root, "bad-too.ts", "BROKEN\n")
	for _, tc := range []struct {
		path   string
		passed bool
	}{{"good.ts", true}, {"bad.ts", false}} {
		reports := validateTypeScriptFiles(context.Background(), root, collectValidationFiles([]string{root}, []string{tc.path}), nil)
		if len(reports) != 1 || reports[0].passed != tc.passed || reports[0].skipped {
			t.Fatalf("%s: %+v", tc.path, reports)
		}
	}
	reports := validateTypeScriptFiles(context.Background(), root, collectValidationFiles([]string{root}, []string{"bad.ts", "bad-too.ts"}), nil)
	if len(reports) != 1 || reports[0].passed || reports[0].skipped || !strings.Contains(reports[0].detail, "bad.ts") || !strings.Contains(reports[0].detail, "bad-too.ts") {
		t.Fatalf("batch lost diagnostics after first error: %+v", reports)
	}
}

func TestValidationBudgetDoesNotCancelWrites(t *testing.T) {
	root := validationSandbox(t)
	a := NewApp()
	enabled := true
	cfg := ConfigState{Workspace: root, AutoValidationJSON: &enabled}
	call := openai.ToolCall{Function: openai.FunctionCall{Name: "create", Arguments: `{"path":"bad.json","content":"{"}`}}
	ctx := context.WithValue(context.Background(), batchValidationBudgetContextKey{}, &batchValidationBudget{})
	r := a.executeTool(ctx, cfg, "", call.Function.Name, []byte(call.Function.Arguments))
	if !r.OK {
		t.Fatalf("write canceled by check deadline: %+v", r)
	}
	edit, ok := r.Data.(EditResult)
	if !ok || !strings.Contains(edit.Validation, "超时") {
		t.Fatalf("expected skipped check: %+v", r)
	}
}

func TestValidationIgnoresOnlyGeneratedPathsInsideWorkspace(t *testing.T) {
	base := validationSandbox(t)
	root := filepath.Join(base, "build")
	writeToolTestFile(t, root, "source.json", "{}")
	writeToolTestFile(t, root, "dist/output.json", "{}")
	files := collectValidationFiles([]string{root}, []string{"source.json", "dist/output.json"})
	if len(files) != 1 || files[0].display != "source.json" {
		t.Fatalf("workspace name excluded source: %+v", files)
	}
}
