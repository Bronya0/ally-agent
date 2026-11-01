// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var (
	labeledButtonCall = regexp.MustCompile(`\b(?:cardFlat|cardFlatBusy|cardFlatKey|cardPrimary|cardDanger|flat|flatKey|flatBusy|ghost|primary|primaryBusy|button|buttonParts|buttonWithState|Action|actionParts|disclosure)\s*\(`)
	buttonLabelText   = regexp.MustCompile(`"([^"\\]*)"`)
	drawnTextCall     = regexp.MustCompile(`ui\.Text\(c(?:\.Key\([^)]*\))?, "([^"\\]*)"\)`)
)

// duplicatedLabelDraw reports the 1-based lines where a button that draws its
// own label has that same text drawn again as a child. MyGo's ButtonBase.Label
// is only the accessible name, but the app's button factories (uikit.Button and
// therefore flat/cardFlat/primary/ghost/disclosure) draw it as visible text, so
// a second ui.Text of the same string shows it twice: 详情 详情 ⌄.
func duplicatedLabelDraw(lines []string) []int {
	var out []int
	for i, line := range lines {
		m := drawnTextCall.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		text := m[1]
		for j := i - 1; j >= 0 && j >= i-20; j-- {
			if !labeledButtonCall.MatchString(lines[j]) {
				continue
			}
			// The nearest preceding button is the container this text sits in.
			// A row of its own without a .Children between them is not a child.
			same := false
			for _, lit := range buttonLabelText.FindAllStringSubmatch(lines[j], -1) {
				if lit[1] == text {
					same = true
					break
				}
			}
			nested := false
			for k := j; k < i; k++ {
				if strings.Contains(lines[k], ".Children(func") {
					nested = true
					break
				}
			}
			if same && nested {
				out = append(out, i+1)
			}
			break
		}
	}
	return out
}

// A button named by its own label appears twice in the text list: once as the
// accessible name and once as the drawn text. Three means the label was drawn
// twice, which is why the service card showed 详情 详情 ⌄, and why only
// disclosure() may render a labeled button with a chevron.
func TestNoButtonDrawsItsOwnLabelTwice(t *testing.T) {
	sample := []string{
		`b := cardFlat(c, "详情").AlignSelf(ui.Start)`,
		`b.Children(func() {`,
		`	ui.Text(c, "详情").FontSize(fsSmall)`,
		`})`,
	}
	if got := duplicatedLabelDraw(sample); len(got) != 1 {
		t.Fatalf("detector missed the known duplicate: %v", got)
	}

	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	scanned := 0
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		raw, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		scanned++
		if sites := duplicatedLabelDraw(strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")); len(sites) > 0 {
			t.Errorf("%s draws a button's own label again at line(s) %v", name, sites)
		}
	}
	if scanned < 20 {
		t.Fatalf("only %d source files scanned, the glob did not cover the package", scanned)
	}
}
