package documentpreview

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func archiveFixture(t *testing.T, ext string, files map[string]string) string {
	t.Helper()
	name := filepath.Join(t.TempDir(), "sample"+ext)
	f, err := os.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	z := zip.NewWriter(f)
	for name, content := range files {
		w, err := z.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err = z.Close(); err != nil {
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	return name
}

func TestWordPreviewReadsRunsHeadingsAndClosesArchive(t *testing.T) {
	name := archiveFixture(t, ".docx", map[string]string{"word/document.xml": `<w:document xmlns:w="word"><w:body><w:p><w:pPr><w:pStyle w:val="Heading1"/></w:pPr><w:r><w:t>文档标题</w:t></w:r></w:p><w:p><w:r><w:t>hello &amp; </w:t></w:r><w:r><w:t>world</w:t><w:tab/><w:t>内容</w:t><w:br/><w:t>下一行</w:t></w:r></w:p></w:body></w:document>`})
	doc, err := Load(context.Background(), name)
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Paragraphs) != 2 || doc.Paragraphs[0].Heading != 1 || doc.Paragraphs[1].Text != "hello & world\t内容\n下一行" {
		t.Fatalf("unexpected preview: %+v", doc)
	}
	// A decoded preview must not retain the zip/file handle on Windows.
	if err := os.Rename(name, name+".closed"); err != nil {
		t.Fatal(err)
	}
}

func workbookFixture(sheet string) map[string]string {
	return map[string]string{
		"xl/workbook.xml":            `<workbook xmlns:r="relationships"><sheets><sheet name="销售数据" sheetId="1" r:id="rId7"/></sheets></workbook>`,
		"xl/_rels/workbook.xml.rels": `<Relationships><Relationship Id="rId7" Target="worksheets/custom.xml"/></Relationships>`,
		"xl/sharedStrings.xml":       `<sst><si><t>产品</t></si><si><r><t>总</t></r><r><t>数</t></r></si></sst>`,
		"xl/worksheets/custom.xml":   sheet,
	}
}

func TestSpreadsheetPreviewSparseCellsAndCachedFormulas(t *testing.T) {
	name := archiveFixture(t, ".xlsx", workbookFixture(`<worksheet><sheetData><row r="3"><c r="A3" t="s"><v>0</v></c><c r="C3" t="inlineStr"><is><t>名称</t></is></c></row><row r="1048576"><c r="A1048576" t="s"><v>1</v></c><c r="B1048576"><f>1+2</f><v>3</v></c><c r="D1048576" t="b"><v>1</v></c><c r="XFD1048576"><v>huge coordinate</v></c></row></sheetData></worksheet>`))
	doc, err := Load(context.Background(), name)
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Sheets) != 1 || !doc.Truncated {
		t.Fatalf("sheets=%+v", doc)
	}
	s := doc.Sheets[0]
	if s.Name != "销售数据" || len(s.Rows) != 2 || s.Rows[1].Number != 1048576 || s.Columns != 4 {
		t.Fatalf("sparse coordinates expanded memory: %+v", s)
	}
	if strings.Join(s.Rows[0].Values, "|") != "产品||名称" || strings.Join(s.Rows[1].Values, "|") != "总数|3||TRUE" {
		t.Fatalf("values=%+v", s.Rows)
	}
}

func TestDocumentPreviewLimitsAndCancellation(t *testing.T) {
	t.Run("paragraphs", func(t *testing.T) {
		name := archiveFixture(t, ".docx", map[string]string{"word/document.xml": "<document>" + strings.Repeat("<p><r><t>内容</t></r></p>", maxParagraphs+10) + "</document>"})
		doc, err := Load(context.Background(), name)
		if err != nil || !doc.Truncated || len(doc.Paragraphs) != maxParagraphs {
			t.Fatalf("limit: doc=%+v err=%v", doc, err)
		}
	})
	t.Run("rows", func(t *testing.T) {
		name := archiveFixture(t, ".xlsx", workbookFixture("<worksheet><sheetData>"+strings.Repeat("<row><c><v>1</v></c></row>", maxRows+10)+"</sheetData></worksheet>"))
		doc, err := Load(context.Background(), name)
		if err != nil || !doc.Truncated || len(doc.Sheets[0].Rows) != maxRows {
			t.Fatalf("row limit: err=%v", err)
		}
	})
	t.Run("expanded XML", func(t *testing.T) {
		name := archiveFixture(t, ".docx", map[string]string{"word/document.xml": strings.Repeat("x", maxXMLBytes+1)})
		if _, err := Load(context.Background(), name); !errors.Is(err, ErrLimit) {
			t.Fatalf("expanded XML limit: %v", err)
		}
	})
	t.Run("XML nesting", func(t *testing.T) {
		name := archiveFixture(t, ".docx", map[string]string{"word/document.xml": strings.Repeat("<p>", 140) + strings.Repeat("</p>", 140)})
		if _, err := Load(context.Background(), name); !errors.Is(err, ErrLimit) {
			t.Fatalf("XML depth limit: %v", err)
		}
	})
	t.Run("central directory", func(t *testing.T) {
		files := map[string]string{}
		for i := 0; i <= maxEntries; i++ {
			files[fmt.Sprint(i)] = ""
		}
		name := archiveFixture(t, ".docx", files)
		if _, err := Load(context.Background(), name); !errors.Is(err, ErrLimit) {
			t.Fatalf("entry limit: %v", err)
		}
	})
	t.Run("canceled queued load", func(t *testing.T) {
		slots <- struct{}{}
		defer func() { <-slots }()
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := Load(ctx, "unused.docx"); !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled: %v", err)
		}
	})
}
