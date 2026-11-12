// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package command

import (
	"bytes"
	"path"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// LiteralWriteTargetsWithCD resolves targets in simple POSIX command sequences
// against explicit cd operands. Paths remain relative to the initial directory
// when it is unknown (as on a remote host). No local filesystem or home lookup
// is used to resolve remote paths. Complex control flow keeps the existing
// best-effort literal analysis rather than trying to execute the shell.
func LiteralWriteTargetsWithCD(commandLine string) []WriteTarget {
	file, err := parseShellFile(commandLine)
	if err != nil {
		return LiteralWriteTargets(commandLine)
	}
	var result []WriteTarget
	appendTargets := func(node syntax.Node, cwd string) {
		var source bytes.Buffer
		if err := syntax.NewPrinter().Print(&source, node); err != nil {
			return
		}
		for _, target := range LiteralWriteTargets(source.String()) {
			if cwd != "" && !path.IsAbs(target.Path) && !strings.HasPrefix(target.Path, "~") {
				target.Path = path.Join(cwd, target.Path)
			}
			result = append(result, target)
		}
	}
	var visit func(*syntax.Stmt, string) string
	visit = func(stmt *syntax.Stmt, cwd string) string {
		switch cmd := stmt.Cmd.(type) {
		case *syntax.BinaryCmd:
			if cmd.Op == syntax.AndStmt {
				for _, redir := range stmt.Redirs {
					appendTargets(&syntax.Stmt{Redirs: []*syntax.Redirect{redir}}, cwd)
				}
				return visit(cmd.Y, visit(cmd.X, cwd))
			}
		case *syntax.Subshell:
			for _, redir := range stmt.Redirs {
				appendTargets(&syntax.Stmt{Redirs: []*syntax.Redirect{redir}}, cwd)
			}
			inner := cwd
			for _, child := range cmd.Stmts {
				inner = visit(child, inner)
			}
			return cwd
		case *syntax.Block:
			for _, redir := range stmt.Redirs {
				appendTargets(&syntax.Stmt{Redirs: []*syntax.Redirect{redir}}, cwd)
			}
			for _, child := range cmd.Stmts {
				cwd = visit(child, cwd)
			}
			return cwd
		}
		appendTargets(stmt, cwd)
		if call, ok := stmt.Cmd.(*syntax.CallExpr); ok && len(call.Args) > 0 && shellWordValue(call.Args[0]) == "cd" {
			args := call.Args[1:]
			if len(args) > 0 && shellWordValue(args[0]) == "--" {
				args = args[1:]
			}
			if len(args) == 0 {
				return "~"
			}
			if len(args) == 1 {
				dir := shellWordValue(args[0])
				if dir != "" && dir != "-" && !strings.ContainsAny(dir, "$%*?[]{}"+"`") {
					if path.IsAbs(dir) || strings.HasPrefix(dir, "~") {
						return dir
					}
					return path.Join(cwd, dir)
				}
			}
			return ""
		}
		return cwd
	}
	cwd := ""
	for _, stmt := range file.Stmts {
		if stmt.Background {
			visit(stmt, cwd)
		} else {
			cwd = visit(stmt, cwd)
		}
	}
	return result
}

// astInvocations is the primary shell-analysis path. The legacy scanner remains
// only as a compatibility fallback for command lines the parser cannot accept
// (notably incomplete PowerShell/cmd fragments).
func astInvocations(commandLine string) []Invocation {
	return astInvocationsDepth(commandLine, 0)
}

func astInvocationsDepth(commandLine string, depth int) []Invocation {
	if depth > 4 {
		return nil
	}
	file, err := parseShellFile(commandLine)
	if err != nil {
		return invocations(commandLine, depth)
	}

	result := make([]Invocation, 0, len(file.Stmts))
	syntax.Walk(file, func(node syntax.Node) bool {
		call, ok := node.(*syntax.CallExpr)
		if !ok || len(call.Args) == 0 {
			return true
		}
		words := make([]string, 0, len(call.Args))
		for _, arg := range call.Args {
			words = append(words, shellWordValue(arg))
		}
		invocation, ok := invocationFromWords(words)
		if !ok {
			return true
		}
		result = append(result, invocation)
		result = append(result, astNestedInvocations(invocation, depth)...)
		return true
	})
	return result
}

func parseShellFile(commandLine string) (file *syntax.File, err error) {
	defer func() {
		if recoverValue := recover(); recoverValue != nil {
			file = nil
			err = shellParsePanic{value: recoverValue}
		}
	}()
	return syntax.NewParser().Parse(strings.NewReader(commandLine), "")
}

type shellParsePanic struct {
	value any
}

func (e shellParsePanic) Error() string { return "shell parser panic" }

func astNestedInvocations(invocation Invocation, depth int) []Invocation {
	if script, ok := nestedShellScript(invocation); ok {
		return astInvocationsDepth(script, depth+1)
	}
	if invocation.Name == "eval" || invocation.Name == "invoke-expression" || invocation.Name == "iex" {
		if len(invocation.Args) > 0 {
			return astInvocationsDepth(strings.Join(invocation.Args, " "), depth+1)
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
			return append([]Invocation{nested}, astNestedInvocations(nested, depth)...)
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
					return append([]Invocation{nested}, astNestedInvocations(nested, depth)...)
				}
			}
		}
	}
	if invocation.Name == "busybox" {
		if nested, ok := invocationFromWords(invocation.Args); ok {
			return append([]Invocation{nested}, astNestedInvocations(nested, depth)...)
		}
	}
	return nil
}

// astRedirectionTargets classifies every literal redirection target the shell
// parser can see: write targets (`>` / `>>` / `&>` / `<>`) and input targets
// (`<`). File-descriptor duplication (`>&1`, `<&0`) and heredocs are skipped:
// the first has no path, the second carries its content inline.
func astRedirectionTargets(commandLine string) (writes, reads []string, ok bool) {
	file, err := parseShellFile(commandLine)
	if err != nil {
		return nil, nil, false
	}
	syntax.Walk(file, func(node syntax.Node) bool {
		redirection, ok := node.(*syntax.Redirect)
		if !ok || redirection.Word == nil || redirection.Hdoc != nil {
			return true
		}
		// DplOut is file-descriptor duplication (`>&1`/`>&-`), not a file
		// path. File writes using `&>` use the separate RdrAll operator.
		target := shellWordValue(redirection.Word)
		if target == "" {
			return true
		}
		if redirection.Op == syntax.DplOut && IsShellFDRedirectionTarget("&"+target) {
			return true
		}
		if IsShellFDRedirectionTarget(target) {
			return true
		}
		if isWriteRedirection(redirection.Op) {
			writes = append(writes, target)
		}
		if redirection.Op == syntax.RdrIn {
			reads = append(reads, target)
		}
		return true
	})
	return writes, reads, true
}

func isWriteRedirection(op syntax.RedirOperator) bool {
	switch op {
	case syntax.RdrOut, syntax.AppOut, syntax.RdrInOut,
		syntax.RdrClob, syntax.AppClob,
		syntax.RdrAll, syntax.RdrAllClob, syntax.AppAll, syntax.AppAllClob,
		syntax.DplOut:
		return true
	default:
		return false
	}
}

// shellWordValue returns the shell's decoded value for literal and quoted
// parts, while retaining source syntax for expansions. This keeps static paths
// usable on Windows and still lets ResolveCommandLiteralPath reject $vars,
// globs, and command substitutions.
func shellWordValue(word *syntax.Word) string {
	if word == nil {
		return ""
	}
	var b strings.Builder
	for _, part := range word.Parts {
		appendShellWordPart(&b, part)
	}
	return b.String()
}

func appendShellWordPart(b *strings.Builder, part syntax.WordPart) {
	switch part := part.(type) {
	case *syntax.Lit:
		b.WriteString(part.Value)
	case *syntax.SglQuoted:
		b.WriteString(part.Value)
	case *syntax.DblQuoted:
		for _, nested := range part.Parts {
			appendShellWordPart(b, nested)
		}
	default:
		var source bytes.Buffer
		if err := syntax.NewPrinter().Print(&source, part); err == nil {
			b.Write(source.Bytes())
		}
	}
}
