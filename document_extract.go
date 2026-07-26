package main

import (
	"archive/zip"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

func extractDocxText(filePath string) (string, error) {
	return extractZipXMLText(filePath, func(name string) bool {
		return name == "word/document.xml" || strings.HasPrefix(name, "word/header") || strings.HasPrefix(name, "word/footer")
	})
}

func extractPptxText(filePath string) (string, error) {
	return extractZipXMLText(filePath, func(name string) bool {
		return strings.HasPrefix(name, "ppt/slides/slide") && strings.HasSuffix(name, ".xml")
	})
}

func extractZipXMLText(filePath string, include func(string) bool) (string, error) {
	zr, err := zip.OpenReader(filePath)
	if err != nil {
		return "", err
	}
	defer zr.Close()
	files := append([]*zip.File(nil), zr.File...)
	sort.Slice(files, func(i, j int) bool { return files[i].Name < files[j].Name })
	var b strings.Builder
	for _, f := range files {
		if !include(f.Name) {
			continue
		}
		part, err := extractOOXMLTextPart(f)
		if err != nil {
			return "", err
		}
		part = strings.TrimSpace(part)
		if part != "" {
			if b.Len() > 0 {
				b.WriteString("\n\n")
			}
			b.WriteString(part)
		}
	}
	if b.Len() == 0 {
		return "", errors.New("no readable text found")
	}
	return b.String(), nil
}

func extractOOXMLTextPart(f *zip.File) (string, error) {
	rc, err := f.Open()
	if err != nil {
		return "", err
	}
	defer rc.Close()
	dec := xml.NewDecoder(rc)
	var b strings.Builder
	var inText bool
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "t":
				inText = true
			case "p":
				if b.Len() > 0 && !strings.HasSuffix(b.String(), "\n") {
					b.WriteByte('\n')
				}
			}
		case xml.EndElement:
			if t.Name.Local == "t" {
				inText = false
			}
		case xml.CharData:
			if inText {
				b.Write([]byte(t))
			}
		}
	}
	return compactDocumentText(b.String()), nil
}

func extractXlsxText(filePath, sheetSelector string) (string, []string, error) {
	zr, err := zip.OpenReader(filePath)
	if err != nil {
		return "", nil, err
	}
	defer zr.Close()
	shared, _ := readSharedStrings(zr.File)
	sheetFiles := []*zip.File{}
	for _, f := range zr.File {
		if strings.HasPrefix(f.Name, "xl/worksheets/sheet") && strings.HasSuffix(f.Name, ".xml") {
			sheetFiles = append(sheetFiles, f)
		}
	}
	sort.Slice(sheetFiles, func(i, j int) bool { return sheetFiles[i].Name < sheetFiles[j].Name })
	if len(sheetFiles) == 0 {
		return "", nil, errors.New("no worksheets found")
	}
	sheetNames := make([]string, len(sheetFiles))
	for i := range sheetFiles {
		sheetNames[i] = fmt.Sprintf("Sheet%d", i+1)
	}
	selected := -1
	if strings.TrimSpace(sheetSelector) != "" {
		if n, convErr := strconv.Atoi(strings.TrimSpace(sheetSelector)); convErr == nil && n >= 1 && n <= len(sheetFiles) {
			selected = n - 1
		} else {
			for i, name := range sheetNames {
				if strings.EqualFold(name, strings.TrimSpace(sheetSelector)) {
					selected = i
					break
				}
			}
		}
		if selected < 0 {
			return "", sheetNames, fmt.Errorf("sheet not found: %s", sheetSelector)
		}
	}
	var b strings.Builder
	for i, f := range sheetFiles {
		if selected >= 0 && selected != i {
			continue
		}
		rows, err := readWorksheetRows(f, shared)
		if err != nil {
			return "", sheetNames, err
		}
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString("## ")
		b.WriteString(sheetNames[i])
		b.WriteByte('\n')
		for _, row := range rows {
			b.WriteString(strings.Join(row, "\t"))
			b.WriteByte('\n')
		}
	}
	return compactDocumentText(b.String()), sheetNames, nil
}

func readSharedStrings(files []*zip.File) ([]string, error) {
	for _, f := range files {
		if f.Name != "xl/sharedStrings.xml" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		defer rc.Close()
		dec := xml.NewDecoder(rc)
		var result []string
		var b strings.Builder
		var inText bool
		for {
			tok, err := dec.Token()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return nil, err
			}
			switch t := tok.(type) {
			case xml.StartElement:
				if t.Name.Local == "si" {
					b.Reset()
				}
				if t.Name.Local == "t" {
					inText = true
				}
			case xml.EndElement:
				if t.Name.Local == "t" {
					inText = false
				}
				if t.Name.Local == "si" {
					result = append(result, b.String())
				}
			case xml.CharData:
				if inText {
					b.Write([]byte(t))
				}
			}
		}
		return result, nil
	}
	return nil, nil
}

func readWorksheetRows(f *zip.File, shared []string) ([][]string, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	dec := xml.NewDecoder(rc)
	var rows [][]string
	var current []string
	var cellType string
	var inValue bool
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "row":
				current = []string{}
			case "c":
				cellType = ""
				for _, attr := range t.Attr {
					if attr.Name.Local == "t" {
						cellType = attr.Value
						break
					}
				}
			case "v", "t":
				inValue = true
			}
		case xml.EndElement:
			if t.Name.Local == "row" && len(current) > 0 {
				rows = append(rows, current)
			}
			if t.Name.Local == "v" || t.Name.Local == "t" {
				inValue = false
			}
		case xml.CharData:
			if inValue {
				value := string([]byte(t))
				if cellType == "s" {
					if idx, convErr := strconv.Atoi(value); convErr == nil && idx >= 0 && idx < len(shared) {
						value = shared[idx]
					}
				}
				current = append(current, value)
			}
		}
	}
	return rows, nil
}

func extractPDFTextBestEffort(filePath string) (string, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return "", err
	}
	if len(data) > 8*1024*1024 {
		data = data[:8*1024*1024]
	}
	re := regexp.MustCompile(`\((?:\\.|[^\\)])*\)`)
	matches := re.FindAll(data, 20000)
	var parts []string
	for _, m := range matches {
		if len(m) < 2 {
			continue
		}
		s := string(m[1 : len(m)-1])
		s = strings.NewReplacer(`\(`, "(", `\)`, ")", `\\`, `\`, `\n`, "\n", `\r`, "\n", `\t`, "\t").Replace(s)
		s = strings.TrimSpace(s)
		if s != "" {
			parts = append(parts, s)
		}
	}
	if len(parts) == 0 {
		return "", errors.New("no readable PDF text found; scanned or compressed PDFs may need OCR")
	}
	return compactDocumentText(strings.Join(parts, " ")), nil
}

func compactDocumentText(text string) string {
	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.Join(strings.Fields(line), " ")
		if line != "" {
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}
