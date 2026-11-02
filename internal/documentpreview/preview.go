// Package documentpreview reads bounded, offline Word document previews. It
// never extracts archives to disk, follows links or loads images.
package documentpreview

import (
	"archive/zip"
	"context"
	"encoding/binary"
	"encoding/xml"
	"errors"
	"io"
	"os"
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
)

var ErrLimit = errors.New("document exceeds preview limit")
var slots = make(chan struct{}, 1)

type Paragraph struct {
	Text    string
	Heading int
}
type Document struct {
	Paragraphs []Paragraph
	Truncated  bool
}

func Supported(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".docx":
		return true
	}
	return false
}

// Only one archive can be decoded at once, including across workspace changes.
// Canceled callers waiting for the slot retain no archive or preview buffers.
func Load(ctx context.Context, name string) (*Document, error) {
	if !Supported(name) {
		return nil, errors.New("unsupported document")
	}
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
