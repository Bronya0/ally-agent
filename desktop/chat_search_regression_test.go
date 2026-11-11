// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
	"github.com/egoist/mygo/ui/markdown/parser"
)

func TestChatSearchRegexpPreservesOriginalContext(t *testing.T) {
	for _, tc := range []struct{ text, query string }{
		{"foofoo", "^foo"}, {"foofoo", `\bfoo`}, {"foofoo", "foo$"},
		{"foo\nfoo", "(?m)^foo"}, {"foo\nbar", "foo\\nbar"},
	} {
		t.Run(tc.query, func(t *testing.T) {
			s := &chatSearch{Query: tc.query, Regexp: true, MatchCase: true}
			re := regexp.MustCompile(tc.query)
			var want [][2]int
			for _, loc := range re.FindAllStringIndex(tc.text, -1) {
				want = append(want, [2]int{loc[0], loc[1]})
			}
			if got := chatSearchSpans(tc.text, s, re, chatSearchMaxMatches); !reflect.DeepEqual(got, want) {
				t.Fatalf("matches=%v want=%v", got, want)
			}
		})
	}
}

func TestChatSearchLimitsRejectedRegexpCandidates(t *testing.T) {
	s := &chatSearch{Query: "a|$", Regexp: true, WholeWord: true}
	conv := searchTranscript(searchMessage("assistant", strings.Repeat("a", chatSearchMaxRegexCandidates+20)))
	matches, truncated := chatSearchComputeBounded(conv, s)
	if len(matches) != 0 || !truncated || !s.candidateTruncated {
		t.Fatal("candidate exhaustion was reported as a complete search")
	}
	s.Query = "z"
	if _, truncated := chatSearchComputeBounded(conv, s); truncated || s.candidateTruncated {
		t.Fatal("the next complete scan retained candidate exhaustion")
	}
}

func TestChatSearchLiteralFoldAndNonOverlappingRanges(t *testing.T) {
	for _, tc := range []struct {
		text, query string
		want        [][2]int
	}{
		{"Μ μ µ", "µ", [][2]int{{0, 2}, {3, 5}, {6, 8}}},
		{"aaaaa", "aa", [][2]int{{0, 2}, {2, 4}}},
		{"aaab aaaB", "AAAB", [][2]int{{0, 4}, {5, 9}}},
	} {
		s := &chatSearch{Query: tc.query}
		if got := chatSearchSpans(tc.text, s, nil, 400); !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("%q/%q: matches=%v want=%v", tc.text, tc.query, got, tc.want)
		}
	}
}

func TestChatSearchHighlightsOnlyMessageMatches(t *testing.T) {
	source := "needle first\n\nneedle second"
	s := &chatSearch{Query: "^needle", Regexp: true}
	s.cache = chatSearchCompute(searchTranscript(searchMessage("assistant", source)), s)
	doc := parser.Parse(source)
	marked := 0
	for i := 0; i < doc.Len(); i++ {
		if _, spans, base, ok := chatSearchBlockSpans(doc, i, 0, s); ok {
			marked++
			if base+spans[0][0] != 0 {
				t.Fatal("a trimmed paragraph invented an anchored match")
			}
		}
	}
	if marked != 1 {
		t.Fatalf("marked %d blocks, want one", marked)
	}
}

func TestChatSearchMultilineCodeMatchProjectsOntoBothLines(t *testing.T) {
	source := "```go\nfoo\nbar\n```\n"
	doc := parser.Parse(source)
	s := &chatSearch{Query: "foo\\nbar", Regexp: true, frameDoc: doc}
	s.cache = chatSearchCompute(searchTranscript(searchMessage("assistant", source)), s)
	lines, starts, spans, ok := chatSearchCodeLines(s, 0, &markdown.CodeBlockView{Index: 1})
	if !ok || len(lines) != 2 || len(spans[0]) != 1 || len(spans[1]) != 1 || spans[0][0] != [2]int{0, 3} || spans[1][0] != [2]int{0, 3} {
		t.Fatalf("lines=%v starts=%v spans=%v drawn=%v", lines, starts, spans, ok)
	}
}

func TestChatSearchBlankCodeLinesRespectBudgets(t *testing.T) {
	for _, count := range []int{chatSearchCodeMaxLines + 1, chatSearchCodeMaxBytes + 1} {
		source := "```\nneedle\n" + strings.Repeat("\n", count) + "```\n"
		doc := parser.Parse(source)
		s := &chatSearch{Query: "needle", frameDoc: doc}
		s.cache = chatSearchCompute(searchTranscript(searchMessage("assistant", source)), s)
		if _, _, _, ok := chatSearchCodeLines(s, 0, &markdown.CodeBlockView{Index: 1}); ok {
			t.Fatal("blank lines bypassed the highlighted-code budget")
		}
	}
}

func TestChatSearchBarFitsNarrowTranscript(t *testing.T) {
	conv := &conversation{ID: "find", Workspace: t.TempDir()}
	a := &desktop{page: "chat", prefs: defaultPrefs(), tabs: []*conversation{conv}}
	conv.uiState().Search.Open = true
	tester := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(a.theme())
		c.DisableAnimations()
		ui.Column(c).FillWidth().Children(func() { a.chatSearchBar(c, conv) })
	}, 400, 200)
	for _, label := range []string{"上一个匹配", "下一个匹配", "关闭查找（Esc）"} {
		bounds, ok := tester.Find(label)
		if !ok || bounds.W < 24 || bounds.H < 24 {
			t.Fatalf("control %q is clipped: %+v", label, bounds)
		}
	}
}

func TestChatSearchOpeningClosesComposerPickers(t *testing.T) {
	conv := &conversation{ID: "find", Workspace: t.TempDir()}
	a := &desktop{page: "chat", tabs: []*conversation{conv}}
	st := conv.uiState()
	st.CmdOpen, st.Mention.Open, st.SessionsOpen = true, true, true
	a.openChatSearch(conv)
	if !st.Search.Open || st.CmdOpen || st.Mention.Open || st.SessionsOpen {
		t.Fatal("a composer picker retained keyboard ownership over transcript find")
	}
}

func TestChatSearchOldCodeHooksArePrunedWithMarkdown(t *testing.T) {
	conv := &conversation{ID: "find", Workspace: t.TempDir(), Messages: restoredHistory(120)}
	a := &desktop{page: "chat", prefs: defaultPrefs(), tabs: []*conversation{conv}}
	a.markdownOptions(conv, 0)
	ui.NewTester(func(c *ui.Context) {
		c.SetTheme(a.theme())
		c.DisableAnimations()
		a.chatMessages(c, conv, 900)
	}, 900, 600)
	if conv.MD[0] != nil || conv.MDCodeBlock[0] != nil {
		t.Fatal("off-screen Markdown retained its code renderer and captured caches")
	}
}

func TestFileNameDialogEnterRunsValidation(t *testing.T) {
	conv := &conversation{ID: "files", Workspace: t.TempDir()}
	a := &desktop{tabs: []*conversation{conv}, prefs: defaultPrefs()}
	a.filesF.NewOpen = true
	tester := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(a.theme())
		c.DisableAnimations()
		a.filePromptDialog(c)
	}, 700, 400)
	if err := tester.Click("文件名称"); err != nil {
		t.Fatal(err)
	}
	tester.Type("bad/name")
	tester.Key(0, ui.KeyEnter)
	if a.filesF.PromptErr == "" || !a.filesF.NewOpen || a.filesF.NewName != "bad/name" {
		t.Fatal("Enter did not submit the current name through dialog validation")
	}
}

func TestChatSearchJumpShowsOldMessageAndDeepCodeLine(t *testing.T) {
	for _, scenario := range []string{"old-message", "deep-code"} {
		t.Run(scenario, func(t *testing.T) {
			conv := &conversation{ID: "jump", Workspace: t.TempDir()}
			if scenario == "old-message" {
				conv.Messages = restoredHistory(150)
				conv.Messages[1]["content"] = "needle"
			} else {
				code := "```go\n" + strings.Repeat("no match\n", 150) + "needle\n" + strings.Repeat("no match\n", 150) + "```\n"
				conv.Messages = []map[string]any{searchMessage("user", "code please"), searchMessage("assistant", code)}
			}
			a := &desktop{page: "chat", prefs: defaultPrefs(), tabs: []*conversation{conv}}
			st := conv.uiState()
			st.Search.Query = "needle"
			a.openChatSearch(conv)
			var hit, viewport ui.Rect
			tester := ui.NewTester(func(c *ui.Context) {
				c.SetTheme(a.theme())
				c.DisableAnimations()
				a.chatColumn(c, 900)
				viewport = conv.Scroll.Bounds(c)
				if st.Search.blockHandle != nil {
					hit = st.Search.blockHandle.Bounds(c)
				}
			}, 900, 600)
			tester.Advance(100 * time.Millisecond)
			tester.Frame()
			if st.Search.reveal || st.Follow || hit.H <= 0 || hit.Y < viewport.Y || hit.Y+hit.H > viewport.Y+viewport.H {
				t.Fatalf("jump did not show its hit: hit=%+v viewport=%+v reveal=%v follow=%v", hit, viewport, st.Search.reveal, st.Follow)
			}
		})
	}
}
