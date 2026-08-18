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
	"strings"
	"time"
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
			jsonFiles = append(jsonFiles, file)
		case ".py":
			pythonFiles = append(pythonFiles, file)
		case ".js", ".jsx", ".mjs", ".cjs":
			jsFiles = append(jsFiles, file)
		case ".go":
			goFiles = append(goFiles, file)
		case ".ts", ".tsx", ".d.ts":
			tsFiles = append(tsFiles, file)
		case ".vue":
			vueFiles = append(vueFiles, file)
		case ".java":
			javaFiles = append(javaFiles, file)
		}
	}

	if len(jsonFiles) > 0 {
		reports = append(reports, validateJSONFiles(jsonFiles))
	}
	if len(pythonFiles) > 0 {
		reports = append(reports, validatePythonFiles(checkCtx, pythonFiles))
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
	for _, file := range files {
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
	return validationReport{label: fmt.Sprintf("JSON %d 个文件", len(files)), passed: true}
}

func validatePythonFiles(ctx context.Context, files []validationFile) validationReport {
	program, prefix, ok := findPythonCommand()
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
		return validationReport{label: "Python 语法", detail: compactValidationOutput(outputOrError(output, err))}
	}
	return validationReport{label: fmt.Sprintf("Python 语法 %d 个文件", len(files)), passed: true}
}

func validateJavaScriptFiles(ctx context.Context, files []validationFile) validationReport {
	program, err := exec.LookPath("node")
	if err != nil {
		return validationReport{label: "JavaScript 语法", skipped: true, detail: "未找到 node"}
	}
	for _, file := range files {
		output, runErr := runValidationCommand(ctx, filepath.Dir(file.abs), program, "--check", file.abs)
		if runErr != nil {
			return validationReport{label: "JavaScript 语法", detail: file.display + ": " + compactValidationOutput(outputOrError(output, runErr))}
		}
	}
	return validationReport{label: fmt.Sprintf("JavaScript 语法 %d 个文件", len(files)), passed: true}
}

func validateGoFiles(ctx context.Context, root string, files []validationFile) validationReport {
	gofmt, err := exec.LookPath("gofmt")
	if err == nil {
		args := []string{"-d"}
		for _, file := range files {
			args = append(args, file.abs)
		}
		output, runErr := runValidationCommand(ctx, root, gofmt, args...)
		if runErr != nil {
			return validationReport{label: "Go 格式", detail: compactValidationOutput(outputOrError(output, runErr))}
		}
		if strings.TrimSpace(output) != "" {
			return validationReport{label: "Go 格式", detail: "gofmt -d 检测到未格式化代码:\n" + compactValidationOutput(output)}
		}
	} else {
		return validationReport{label: "Go 格式", skipped: true, detail: "未找到 gofmt"}
	}

	goProgram, err := exec.LookPath("go")
	if err != nil {
		return validationReport{label: "Go vet", skipped: true, detail: "未找到 go"}
	}
	packages := make(map[string]struct{})
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
			return validationReport{label: "Go vet " + pkg, detail: compactValidationOutput(outputOrError(output, runErr))}
		}
	}
	return validationReport{label: fmt.Sprintf("Go 格式与 vet %d 个包", len(packageList)), passed: true}
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
		name := "TypeScript tsc"
		program, prefix, ok := findNodeProjectTool(filepath.Dir(config), root, "tsc")
		if project.vue {
			name = "Vue vue-tsc"
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
			reports = append(reports, validationReport{label: name, detail: compactValidationOutput(outputOrError(output, runErr))})
		} else {
			reports = append(reports, validationReport{label: name, passed: true})
		}
	}
	return reports
}

func validateJavaFiles(ctx context.Context, files []validationFile) validationReport {
	program, err := exec.LookPath("javac")
	if err != nil {
		return validationReport{label: "Java 编译", skipped: true, detail: "未找到 javac"}
	}
	tempDir, err := os.MkdirTemp("", "ally-java-check-")
	if err != nil {
		return validationReport{label: "Java 编译", skipped: true, detail: "无法创建临时输出目录"}
	}
	defer os.RemoveAll(tempDir)
	args := []string{"-proc:none", "-encoding", "UTF-8", "-d", tempDir}
	for _, file := range files {
		args = append(args, file.abs)
	}
	output, runErr := runValidationCommand(ctx, filepath.Dir(files[0].abs), program, args...)
	if runErr != nil {
		return validationReport{label: "Java 编译", detail: compactValidationOutput(outputOrError(output, runErr))}
	}
	return validationReport{label: fmt.Sprintf("Java 编译 %d 个文件", len(files)), passed: true}
}

func findPythonCommand() (string, []string, bool) {
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
