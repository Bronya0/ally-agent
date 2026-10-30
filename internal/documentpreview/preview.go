// Package documentpreview reads bounded, offline content previews. It never
// extracts archives to disk, follows links, evaluates formulas or loads images.
package documentpreview

import (
	"archive/zip"
	"context"
	"encoding/binary"
	"encoding/xml"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	MaxFileBytes  = 32 << 20
	maxXMLBytes   = 4 << 20
	maxTextBytes  = 512 << 10
	maxEntries    = 4096
	maxParagraphs = 2000
	maxSheets     = 8
	maxRows       = 300
	maxColumns    = 32
)

var ErrLimit = errors.New("document exceeds preview limit")
var slots = make(chan struct{}, 1)

type Paragraph struct {
	Text    string
	Heading int
}
type Row struct {
	Number int
	Values []string
}
type Sheet struct {
	Name    string
	Rows    []Row
	Columns int
}
type Document struct {
	Paragraphs []Paragraph
	Sheets     []Sheet
	Truncated  bool
}

func Supported(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".docx", ".xlsx":
		return true
	}
	return false
}

// Only one archive can be decoded at once, including across workspace changes.
// Canceled callers waiting for the slot retain no archive or preview buffers.
func Load(ctx context.Context, name string) (*Document, error) {
	select {
	case slots <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-slots }()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if info.Size() > MaxFileBytes {
		return nil, ErrLimit
	}
	if err := checkDirectory(f, info.Size()); err != nil {
		return nil, err
	}
	z, err := zip.NewReader(f, info.Size())
	if err != nil {
		return nil, err
	}
	if len(z.File) > maxEntries {
		return nil, ErrLimit
	}
	a := archive{ctx: ctx, files: make(map[string]*zip.File, len(z.File)), remaining: 12 << 20}
	for _, entry := range z.File {
		a.files[entry.Name] = entry
	}
	if strings.EqualFold(filepath.Ext(name), ".docx") {
		return a.word()
	}
	if strings.EqualFold(filepath.Ext(name), ".xlsx") {
		return a.workbook()
	}
	return nil, errors.New("unsupported document")
}

// Bound the central directory BEFORE archive/zip allocates one File per entry.
// ZIP64/multi-volume packages fall back to properties rather than expanding.
func checkDirectory(f *os.File, size int64) error {
	if size < 22 {
		return zip.ErrFormat
	}
	n := min(size, 65557)
	buf := make([]byte, n)
	if _, err := f.ReadAt(buf, size-n); err != nil {
		return err
	}
	for i := len(buf) - 22; i >= 0; i-- {
		if string(buf[i:i+4]) != "PK\x05\x06" {
			continue
		}
		if i+22+int(binary.LittleEndian.Uint16(buf[i+20:])) != len(buf) {
			continue
		}
		entries := binary.LittleEndian.Uint16(buf[i+10:])
		if entries > maxEntries || binary.LittleEndian.Uint16(buf[i+4:]) != 0 || binary.LittleEndian.Uint16(buf[i+6:]) != 0 || binary.LittleEndian.Uint32(buf[i+12:]) > 2<<20 {
			return ErrLimit
		}
		return nil
	}
	return zip.ErrFormat
}

type archive struct {
	ctx       context.Context
	files     map[string]*zip.File
	remaining int64
}
type budgetReader struct {
	a *archive
	r io.Reader
}

func (r budgetReader) Read(p []byte) (int, error) {
	if err := r.a.ctx.Err(); err != nil {
		return 0, err
	}
	if r.a.remaining <= 0 {
		return 0, ErrLimit
	}
	n, err := r.r.Read(p[:min(int64(len(p)), r.a.remaining)])
	r.a.remaining -= int64(n)
	return n, err
}
func (a *archive) xml(name string, visit func(xml.Token) (bool, error)) error {
	f := a.files[name]
	if f == nil {
		return errors.New("document content is missing")
	}
	if f.UncompressedSize64 > maxXMLBytes {
		return ErrLimit
	}
	r, err := f.Open()
	if err != nil {
		return err
	}
	defer r.Close()
	d := xml.NewDecoder(budgetReader{a, io.LimitReader(r, maxXMLBytes+1)})
	depth := 0
	for {
		if err := a.ctx.Err(); err != nil {
			return err
		}
		t, err := d.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		switch t.(type) {
		case xml.StartElement:
			depth++
			if depth > 128 {
				return ErrLimit
			}
		case xml.EndElement:
			depth--
		}
		stop, err := visit(t)
		if err != nil {
			return err
		}
		if stop {
			return nil
		}
	}
}
func attr(e xml.StartElement, name string) string {
	for _, a := range e.Attr {
		if a.Name.Local == name {
			return a.Value
		}
	}
	return ""
}

func (a *archive) word() (*Document, error) {
	doc := &Document{}
	var text strings.Builder
	heading, textDepth, paragraphDepth, bytesLeft := 0, 0, 0, maxTextBytes
	err := a.xml("word/document.xml", func(tok xml.Token) (bool, error) {
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "p":
				paragraphDepth++
				if paragraphDepth == 1 {
					text.Reset()
					heading = 0
				}
			case "pStyle":
				style := strings.ToLower(attr(t, "val"))
				if strings.HasPrefix(style, "heading") {
					heading, _ = strconv.Atoi(strings.TrimPrefix(style, "heading"))
				}
				if style == "title" {
					heading = 1
				}
			case "t":
				textDepth++
			case "tab":
				if paragraphDepth > 0 {
					text.WriteByte('\t')
					bytesLeft--
				}
			case "br":
				if paragraphDepth > 0 {
					text.WriteByte('\n')
					bytesLeft--
				}
			}
		case xml.EndElement:
			if t.Name.Local == "t" {
				textDepth--
			}
			if t.Name.Local == "p" {
				paragraphDepth--
				if paragraphDepth == 0 && text.Len() > 0 {
					doc.Paragraphs = append(doc.Paragraphs, Paragraph{text.String(), min(6, max(0, heading))})
					text.Reset()
				}
			}
		case xml.CharData:
			if textDepth > 0 && paragraphDepth > 0 {
				if len(t) > bytesLeft {
					doc.Truncated = true
					return true, nil
				}
				text.Write(t)
				bytesLeft -= len(t)
			}
		}
		if bytesLeft <= 0 || len(doc.Paragraphs) >= maxParagraphs {
			doc.Truncated = true
			return true, nil
		}
		return false, nil
	})
	if text.Len() > 0 && doc.Truncated {
		doc.Paragraphs = append(doc.Paragraphs, Paragraph{text.String(), heading})
	}
	return doc, err
}

func (a *archive) workbook() (*Document, error) {
	type sheetRef struct{ name, id string }
	var refs []sheetRef
	doc := &Document{}
	err := a.xml("xl/workbook.xml", func(tok xml.Token) (bool, error) {
		if e, ok := tok.(xml.StartElement); ok && e.Name.Local == "sheet" && attr(e, "state") != "hidden" && attr(e, "state") != "veryHidden" {
			if len(attr(e, "name")) > 256 || len(attr(e, "id")) > 256 {
				return false, ErrLimit
			}
			if len(refs) >= maxSheets {
				doc.Truncated = true
				return true, nil
			}
			refs = append(refs, sheetRef{attr(e, "name"), attr(e, "id")})
		}
		return false, nil
	})
	if err != nil {
		return nil, err
	}
	rels := map[string]string{}
	err = a.xml("xl/_rels/workbook.xml.rels", func(tok xml.Token) (bool, error) {
		if e, ok := tok.(xml.StartElement); ok && e.Name.Local == "Relationship" && attr(e, "TargetMode") != "External" {
			if len(rels) >= maxEntries {
				return false, ErrLimit
			}
			target := attr(e, "Target")
			if strings.HasPrefix(target, "/") {
				target = strings.TrimPrefix(target, "/")
			} else {
				target = path.Join("xl", target)
			}
			if strings.HasPrefix(target, "xl/") {
				rels[attr(e, "Id")] = target
			}
		}
		return false, nil
	})
	if err != nil {
		return nil, err
	}
	var shared []string
	if a.files["xl/sharedStrings.xml"] != nil {
		var value strings.Builder
		inText, remaining := false, maxTextBytes
		err = a.xml("xl/sharedStrings.xml", func(tok xml.Token) (bool, error) {
			switch t := tok.(type) {
			case xml.StartElement:
				if t.Name.Local == "si" {
					value.Reset()
				}
				if t.Name.Local == "t" {
					inText = true
				}
			case xml.CharData:
				if inText {
					if len(t) > remaining {
						return false, ErrLimit
					}
					value.Write(t)
					remaining -= len(t)
				}
			case xml.EndElement:
				if t.Name.Local == "t" {
					inText = false
				}
				if t.Name.Local == "si" {
					if len(shared) >= 20000 {
						return false, ErrLimit
					}
					shared = append(shared, value.String())
				}
			}
			return false, nil
		})
		if err != nil {
			return nil, err
		}
	}
	remaining := maxTextBytes
	for _, ref := range refs {
		sheet, cut, err := a.sheet(rels[ref.id], ref.name, shared, &remaining)
		if err != nil {
			return nil, err
		}
		doc.Truncated = doc.Truncated || cut
		doc.Sheets = append(doc.Sheets, sheet)
		if remaining <= 0 {
			doc.Truncated = true
			break
		}
	}
	return doc, nil
}

func (a *archive) sheet(name, label string, shared []string, remaining *int) (Sheet, bool, error) {
	s := Sheet{Name: label}
	cut := false
	row := Row{}
	column, typ, inValue := -1, "", false
	var value strings.Builder
	err := a.xml(name, func(tok xml.Token) (bool, error) {
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "row":
				if len(s.Rows) >= maxRows {
					cut = true
					return true, nil
				}
				number, _ := strconv.Atoi(attr(t, "r"))
				if number <= 0 {
					number = len(s.Rows) + 1
				}
				row = Row{Number: number}
				column = -1
			case "c":
				column++
				if ref := attr(t, "r"); ref != "" {
					column = ColumnIndex(ref)
				}
				typ = attr(t, "t")
				value.Reset()
				if column >= maxColumns {
					cut = true
				}
			case "v", "t":
				inValue = true
			}
		case xml.CharData:
			if inValue && column >= 0 && column < maxColumns {
				if value.Len()+len(t) > 8192 {
					cut = true
					return false, nil
				}
				value.Write(t)
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "v", "t":
				inValue = false
			case "c":
				if column < 0 || column >= maxColumns {
					break
				}
				v := value.String()
				if typ == "s" {
					index, err := strconv.Atoi(v)
					if err == nil && index >= 0 && index < len(shared) {
						v = shared[index]
					} else {
						v = ""
					}
				}
				if typ == "b" {
					if v == "1" {
						v = "TRUE"
					} else {
						v = "FALSE"
					}
				}
				if len(v) > *remaining {
					cut = true
					return true, nil
				}
				*remaining -= len(v)
				for len(row.Values) <= column {
					row.Values = append(row.Values, "")
				}
				row.Values[column] = v
				s.Columns = max(s.Columns, column+1)
			case "row":
				s.Rows = append(s.Rows, row)
			}
		}
		return false, nil
	})
	return s, cut, err
}

func ColumnIndex(ref string) int {
	n := 0
	for _, r := range ref {
		if r < 'A' || r > 'Z' {
			break
		}
		n = n*26 + int(r-'A'+1)
		if n > maxColumns {
			return maxColumns
		}
	}
	return n - 1
}
func ColumnName(index int) string {
	name := ""
	for index++; index > 0; index = (index - 1) / 26 {
		name = string(rune('A'+(index-1)%26)) + name
	}
	return name
}
