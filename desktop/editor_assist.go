// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/egoist/mygo/ui/editor"
)

const (
	maxAssistIndexBytes = 2 << 20
	maxGoParseBytes     = 512 << 10
	maxAssistWords      = 600
)

type editorAssistIndex struct {
	version uint64
	words   []editor.Completion
	symbols map[string]editorAssistSymbol
	goFile  bool
}

type editorAssistSymbol struct {
	name, signature, documentation string
	position                       editor.Pos
	kind                           editor.CompletionKind
}

func buildEditorAssistIndex(path, source string, version uint64) *editorAssistIndex {
	idx := &editorAssistIndex{version: version, goFile: strings.EqualFold(filepath.Ext(path), ".go")}
	if len(source) <= maxAssistIndexBytes {
		idx.words = collectAssistWords(path, source)
	} else if idx.goFile {
		idx.words = collectAssistWords(path, "")
	}
	if !idx.goFile || len(source) > maxGoParseBytes {
		return idx
	}

	fset := token.NewFileSet()
	file, _ := parser.ParseFile(fset, path, source, parser.ParseComments|parser.AllErrors)
	if file == nil {
		return idx
	}
	idx.symbols = make(map[string]editorAssistSymbol)
	add := func(name string, pos token.Pos, kind editor.CompletionKind, signature, docs string) {
		if name == "" || len(idx.symbols) >= 1200 {
			return
		}
		p := fset.Position(pos)
		if p.Line <= 0 {
			return
		}
		name = strings.Clone(name)
		idx.symbols[name] = editorAssistSymbol{
			name: name, position: editor.Pos{Line: p.Line - 1, Col: p.Column - 1}, kind: kind,
			signature: compactAssistText(signature, 220), documentation: compactAssistText(docs, 900),
		}
	}
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			sigEnd := fset.Position(d.Name.End()).Offset
			if d.Body != nil {
				sigEnd = fset.Position(d.Body.Pos()).Offset
			}
			start := fset.Position(d.Pos()).Offset
			signature := sourceRange(source, start, sigEnd)
			docs := ""
			if d.Doc != nil {
				docs = d.Doc.Text()
			}
			kind := editor.CompletionFunction
			if d.Recv != nil {
				kind = editor.CompletionMethod
			}
			add(d.Name.Name, d.Name.Pos(), kind, signature, docs)
		case *ast.GenDecl:
			declDocs := ""
			if d.Doc != nil {
				declDocs = d.Doc.Text()
			}
			for _, spec := range d.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					docs := declDocs
					if s.Doc != nil {
						docs = s.Doc.Text()
					}
					signature := "type " + sourceRange(source, fset.Position(s.Name.Pos()).Offset, fset.Position(s.End()).Offset)
					add(s.Name.Name, s.Name.Pos(), editor.CompletionType, signature, docs)
				case *ast.ValueSpec:
					docs := declDocs
					if s.Doc != nil {
						docs = s.Doc.Text()
					}
					kind := editor.CompletionVariable
					if d.Tok == token.CONST {
						kind = editor.CompletionConstant
					}
					for _, name := range s.Names {
						add(name.Name, name.Pos(), kind, sourceRange(source, fset.Position(s.Pos()).Offset, fset.Position(s.End()).Offset), docs)
					}
				}
			}
		}
	}
	return idx
}

func sourceRange(source string, start, end int) string {
	start = max(0, min(start, len(source)))
	end = max(start, min(end, len(source)))
	return strings.TrimSpace(source[start:end])
}

func compactAssistText(s string, limit int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > limit {
		s = s[:limit]
		for !utf8.ValidString(s) {
			s = s[:len(s)-1]
		}
		s += "…"
	}
	return strings.Clone(s)
}

func collectAssistWords(path, source string) []editor.Completion {
	words := make(map[string]editor.Completion, 128)
	add := func(name string, kind editor.CompletionKind) {
		if len(name) < 2 || len(name) > 96 || len(words) >= maxAssistWords {
			return
		}
		if _, exists := words[name]; !exists {
			name = strings.Clone(name)
			words[name] = editor.Completion{Label: name, Kind: kind}
		}
	}
	if strings.EqualFold(filepath.Ext(path), ".go") {
		for _, kw := range []string{"append", "break", "case", "chan", "clear", "const", "continue", "default", "defer", "else", "fallthrough", "for", "func", "go", "goto", "if", "import", "interface", "map", "package", "range", "return", "select", "struct", "switch", "type", "var", "bool", "byte", "complex64", "complex128", "error", "float32", "float64", "int", "int8", "int16", "int32", "int64", "rune", "string", "uint", "uint8", "uint16", "uint32", "uint64", "uintptr", "any", "comparable", "close", "copy", "delete", "imag", "len", "make", "max", "min", "new", "panic", "print", "println", "real", "recover"} {
			kind := editor.CompletionKeyword
			if strings.Contains(" bool byte complex64 complex128 error float32 float64 int int8 int16 int32 int64 rune string uint uint8 uint16 uint32 uint64 uintptr any comparable ", " "+kw+" ") {
				kind = editor.CompletionType
			} else if strings.Contains(" append clear close copy delete imag len make max min new panic print println real recover ", " "+kw+" ") {
				kind = editor.CompletionFunction
			}
			add(kw, kind)
		}
	}

	lang := editor.LanguageFor(path)
	if lang != nil && lang.Tokenizer != nil {
		state := uint64(0)
		var tokens []editor.Token
		for _, line := range strings.Split(source, "\n") {
			tokens, state = lang.Tokenizer.Tokenize(line, state, tokens[:0])
			start := 0
			for _, tok := range tokens {
				end := max(start, min(tok.End, len(line)))
				kind, ok := completionKindForToken(tok.Kind)
				if ok {
					add(line[start:end], kind)
				}
				start = end
			}
		}
	} else {
		for start := 0; start < len(source) && len(words) < maxAssistWords; {
			r, size := utf8.DecodeRuneInString(source[start:])
			if !isAssistIdentStart(r) {
				start += max(1, size)
				continue
			}
			end := start + size
			for end < len(source) {
				next, n := utf8.DecodeRuneInString(source[end:])
				if !isAssistIdentContinue(next) {
					break
				}
				end += n
			}
			add(source[start:end], editor.CompletionVariable)
			start = end
		}
	}

	labels := make([]string, 0, len(words))
	for label := range words {
		labels = append(labels, label)
	}
	sort.Strings(labels)
	out := make([]editor.Completion, 0, len(labels))
	for _, label := range labels {
		out = append(out, words[label])
	}
	return out
}

func completionKindForToken(kind editor.TokenKind) (editor.CompletionKind, bool) {
	switch kind {
	case editor.TokenKeyword:
		return editor.CompletionKeyword, true
	case editor.TokenType, editor.TokenTag:
		return editor.CompletionType, true
	case editor.TokenFunction:
		return editor.CompletionFunction, true
	case editor.TokenProperty, editor.TokenAttribute:
		return editor.CompletionField, true
	case editor.TokenConstant:
		return editor.CompletionConstant, true
	case editor.TokenVariable:
		return editor.CompletionVariable, true
	default:
		return editor.CompletionText, false
	}
}

func isAssistIdentStart(r rune) bool    { return r == '_' || unicode.IsLetter(r) }
func isAssistIdentContinue(r rune) bool { return isAssistIdentStart(r) || unicode.IsDigit(r) }

func assistWordAt(snapshot editor.Snapshot, offset int) (string, int, int) {
	if snapshot.Len() == 0 {
		return "", 0, 0
	}
	offset = max(0, min(offset, snapshot.Len()-1))
	start, end := offset, offset
	for start > 0 {
		from := max(0, start-utf8.UTFMax)
		r, size := utf8.DecodeLastRuneInString(snapshot.Slice(from, start))
		if r == utf8.RuneError || !isAssistIdentContinue(r) {
			break
		}
		start -= size
	}
	for end < snapshot.Len() {
		r, size := utf8.DecodeRuneInString(snapshot.Slice(end, min(snapshot.Len(), end+utf8.UTFMax)))
		if r == utf8.RuneError || !isAssistIdentContinue(r) {
			break
		}
		end += size
	}
	if start == end || !isAssistIdentStart(firstRune(snapshot.Slice(start, end))) {
		return "", start, end
	}
	return snapshot.Slice(start, end), start, end
}

func firstRune(s string) rune {
	r, _ := utf8.DecodeRuneInString(s)
	return r
}

func (a *desktop) configureEditorAssist(root string, file *workspaceFile) {
	if file == nil || file.Editor == nil || file.Editor.Buffer == nil {
		return
	}
	st := file.Editor
	st.OnCompletion = func(req editor.CompletionRequest) {
		if !a.workspaceFileCurrent(root, file) {
			return
		}
		snapshot := st.Buffer.Snapshot()
		if snapshot.Version() != req.Version {
			st.ShowCompletions(-1, nil)
			return
		}
		if req.Prefix == "" && !req.Explicit {
			st.ShowCompletions(-1, nil)
			return
		}
		items := make(map[string]editor.Completion, 720)
		if file.Assist != nil {
			for _, item := range file.Assist.words {
				items[item.Label] = item
			}
			for _, symbol := range file.Assist.symbols {
				items[symbol.name] = editor.Completion{
					Label: symbol.name, Detail: symbol.signature, Documentation: symbol.documentation, Kind: symbol.kind,
				}
			}
		}
		local := collectLocalAssistWords(snapshot, snapshot.LineOf(req.Offset))
		for _, item := range local {
			items[item.Label] = item
		}
		completions := make([]editor.Completion, 0, len(items))
		for _, item := range items {
			completions = append(completions, item)
		}
		sort.Slice(completions, func(i, j int) bool { return completions[i].Label < completions[j].Label })
		st.ShowCompletions(req.Offset-len(req.Prefix), completions)
	}
	st.OnHover = func(req editor.HoverRequest) {
		if !a.workspaceFileCurrent(root, file) {
			return
		}
		snapshot := st.Buffer.Snapshot()
		if snapshot.Version() != req.Version {
			return
		}
		name, start, end := assistWordAt(snapshot, req.Offset)
		if name == "" {
			return
		}
		text := "标识符 · " + filepath.Base(file.Path) + ":" + strconv.Itoa(snapshot.LineOf(start)+1)
		if strings.EqualFold(filepath.Ext(file.Path), ".go") {
			if idx := a.editorAssistForSnapshot(file, snapshot); idx != nil {
				if symbol, ok := idx.symbols[name]; ok {
					text = symbol.signature
					if symbol.documentation != "" {
						text += "\n" + symbol.documentation
					}
				} else if builtin := goBuiltinHover(name); builtin != "" {
					text = builtin
				}
			} else if builtin := goBuiltinHover(name); builtin != "" {
				text = builtin
			}
		}
		st.ShowHover(start, end, text, nil)
	}
	st.OnDefinition = func(offset int) {
		if !a.workspaceFileCurrent(root, file) || !strings.EqualFold(filepath.Ext(file.Path), ".go") {
			return
		}
		snapshot := st.Buffer.Snapshot()
		name, _, _ := assistWordAt(snapshot, offset)
		if name == "" {
			return
		}
		if idx := a.editorAssistForSnapshot(file, snapshot); idx != nil {
			if symbol, ok := idx.symbols[name]; ok {
				st.GoTo(symbol.position)
			}
		}
	}
}

func collectLocalAssistWords(snapshot editor.Snapshot, currentLine int) []editor.Completion {
	out := make(map[string]editor.Completion)
	first, last := max(0, currentLine-80), min(snapshot.LineCount(), currentLine+81)
	for lineNumber := first; lineNumber < last; lineNumber++ {
		line := snapshot.Line(lineNumber)
		for start := 0; start < len(line); {
			r, size := utf8.DecodeRuneInString(line[start:])
			if !isAssistIdentStart(r) {
				start += max(1, size)
				continue
			}
			end := start + size
			for end < len(line) {
				next, n := utf8.DecodeRuneInString(line[end:])
				if !isAssistIdentContinue(next) {
					break
				}
				end += n
			}
			name := strings.Clone(line[start:end])
			if len(name) >= 2 && len(name) <= 96 {
				out[name] = editor.Completion{Label: name, Kind: editor.CompletionVariable}
			}
			start = end
		}
	}
	items := make([]editor.Completion, 0, len(out))
	for _, item := range out {
		items = append(items, item)
	}
	return items
}

var goBuiltinHoverText = map[string]string{
	"append":  "append(s []T, values ...T) []T\n向切片追加元素，并返回可能扩容后的切片。",
	"cap":     "cap(v) int\n返回数组、切片、通道或指针数组的容量。",
	"clear":   "clear(m) / clear(s)\n清空 map，或将切片元素置为零值。",
	"close":   "close(ch)\n关闭通道；接收方可通过第二个返回值判断通道是否已关闭。",
	"copy":    "copy(dst, src) int\n复制切片元素，返回实际复制的数量。",
	"delete":  "delete(m, key)\n从 map 删除键；键不存在时不做操作。",
	"len":     "len(v) int\n返回字符串、数组、切片、map 或通道的长度。",
	"make":    "make(T, size...) T\n创建并初始化 slice、map 或 channel。",
	"max":     "max(x, y, ...)\n返回有序类型参数中的最大值。",
	"min":     "min(x, y, ...)\n返回有序类型参数中的最小值。",
	"new":     "new(T) *T\n分配一个 T 零值并返回其指针。",
	"panic":   "panic(v)\n开始 panic 流程；当前 goroutine 的 defer 仍会运行。",
	"recover": "recover() any\n在 deferred 函数中捕获当前 goroutine 的 panic。",
}

func goBuiltinHover(name string) string { return goBuiltinHoverText[name] }

func (a *desktop) editorAssistForSnapshot(file *workspaceFile, snapshot editor.Snapshot) *editorAssistIndex {
	if file.Assist != nil && file.Assist.version == snapshot.Version() {
		return file.Assist
	}
	if snapshot.Len() > maxGoParseBytes {
		return nil
	}
	index := buildEditorAssistIndex(file.Path, snapshot.String(), snapshot.Version())
	file.Assist = index
	return index
}

func (a *desktop) workspaceFileCurrent(root string, file *workspaceFile) bool {
	return !a.closing && file != nil && a.workspaceFiles[workspaceKey(root)] == file
}

func (a *desktop) releaseWorkspaceFile(root string) {
	key := workspaceKey(root)
	a.workspacePreview.releaseWorkspace(key)
	file := a.workspaceFiles[key]
	if file == nil {
		return
	}
	delete(a.workspaceFiles, key)
	releaseWorkspaceFileValue(file)
}

func releaseWorkspaceFileValue(file *workspaceFile) {
	if file == nil {
		return
	}
	if st := file.Editor; st != nil {
		st.OnCompletion, st.OnHover, st.OnDefinition = nil, nil, nil
		st.OnGutterClick, st.ContextMenu, st.OnInput = nil, nil, nil
		st.RenderFindBar, st.RenderGoToLine = nil, nil
		st.RenderCompletions, st.RenderHover = nil, nil
		st.GutterMarks, st.LineBackgrounds, st.TextBackgrounds = nil, nil, nil
	}
	file.PreviewPath = ""
	file.PreviewHTML = ""
	file.PreviewError = ""
	file.MarkupPreview = false
	file.Assist = nil
	file.Document = nil
	file.DocumentSheet = ""
	file.Editor = nil
}

func (a *desktop) releaseWorkspaceFiles() {
	a.workspacePreview.release()
	a.closeFileInfo()
	for key, file := range a.workspaceFiles {
		delete(a.workspaceFiles, key)
		releaseWorkspaceFileValue(file)
	}
	a.workspaceFiles = nil
}
