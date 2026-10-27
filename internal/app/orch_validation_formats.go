// SPDX-License-Identifier: GPL-3.0-only
package app

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"go.yaml.in/yaml/v4"
	"golang.org/x/net/html/charset"
)

type unsupportedDataSyntax struct{ reason string }

func (e unsupportedDataSyntax) Error() string { return e.reason }

// isHelmTemplate only skips templates inside a chart, not every directory named templates.
func isHelmTemplate(path string, roots []string) bool {
	for dir := filepath.Dir(path); ; dir = filepath.Dir(dir) {
		if strings.EqualFold(filepath.Base(dir), "templates") {
			chart := filepath.Join(filepath.Dir(dir), "Chart.yaml")
			for _, root := range roots {
				if insideRoot(root, chart) {
					info, err := os.Stat(chart)
					if err == nil && !info.IsDir() {
						return true
					}
				}
			}
		}
		if filepath.Dir(dir) == dir {
			return false
		}
	}
}

// Data format checks parse only: no project scripts, schemas or external entities.
func validateDataFiles(ctx context.Context, label string, files []validationFile, roots []string, parse func(context.Context, []byte) error) validationReport {
	checked, skipped := 0, 0
	for _, file := range files {
		if label == "YAML" && isHelmTemplate(file.abs, roots) {
			skipped++
			continue
		}
		if report, handled := validationSkipReport(label, ctx.Err()); handled {
			return report
		}
		data, err := os.ReadFile(file.abs)
		if err == nil {
			err = parse(ctx, bytesTrimUTF8BOM(data))
		}
		if report, handled := validationSkipReport(label, err); handled {
			return report
		}
		var unsupported unsupportedDataSyntax
		if errors.As(err, &unsupported) {
			skipped++
			continue
		}
		if err != nil {
			return validationReport{label: label, detail: file.display + ": " + err.Error()}
		}
		checked++
	}
	if skipped > 0 {
		return validationReport{label: label, skipped: true, detail: fmt.Sprintf("已检查 %d 个文件，跳过 %d 个不支持的模板或格式", checked, skipped)}
	}
	return validationReport{label: fmt.Sprintf("%s %d 个文件", label, checked), passed: true}
}

func validateYAMLSyntax(ctx context.Context, data []byte) error {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		// Decode nodes without expanding aliases or coercing mapping keys to Go types.
		var doc yaml.Node
		err := decoder.Decode(&doc)
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

func validateXMLSyntax(ctx context.Context, data []byte) error {
	// encoding/xml cannot read UTF-16/32 byte streams before seeing the XML
	// declaration. Skip them rather than report valid documents as malformed.
	if bytes.HasPrefix(data, []byte{0xff, 0xfe}) || bytes.HasPrefix(data, []byte{0xfe, 0xff}) ||
		bytes.HasPrefix(data, []byte{0, 0, 0xfe, 0xff}) || bytes.HasPrefix(data, []byte{0, 0, 0, '<'}) ||
		bytes.HasPrefix(data, []byte{0, '<'}) || bytes.HasPrefix(data, []byte{'<', 0}) {
		return unsupportedDataSyntax{reason: "UTF-16/32 XML is not supported"}
	}
	// An XML declaration may specify a non-UTF-8 encoding. The XML parser
	// still owns the document; this only supplies a local decoder.
	decoder := xml.NewDecoder(bytes.NewReader(data))
	decoder.CharsetReader = func(label string, input io.Reader) (io.Reader, error) {
		converted, err := charset.NewReaderLabel(label, input)
		if err != nil {
			return nil, unsupportedDataSyntax{reason: "unsupported XML encoding"}
		}
		return converted, nil
	}
	depth, roots := 0, 0
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		token, err := decoder.Token()
		if err == io.EOF {
			if roots != 1 {
				return fmt.Errorf("XML requires exactly one root element")
			}
			return nil
		}
		if err != nil {
			var unsupported unsupportedDataSyntax
			if errors.As(err, &unsupported) {
				return unsupported
			}
			var syntaxErr *xml.SyntaxError
			if errors.As(err, &syntaxErr) && strings.Contains(syntaxErr.Msg, "invalid character entity") && bytes.Contains(data, []byte("<!DOCTYPE")) {
				// encoding/xml does not resolve DTD-declared entities.
				return unsupportedDataSyntax{reason: "XML DTD entities are not supported"}
			}
			return err
		}
		switch token := token.(type) {
		case xml.StartElement:
			if depth == 0 {
				roots++
				if roots > 1 {
					return fmt.Errorf("XML has multiple root elements")
				}
			}
			depth++
		case xml.EndElement:
			depth--
		case xml.CharData:
			if depth == 0 && len(bytes.TrimSpace(token)) != 0 {
				return fmt.Errorf("XML has text outside the root element")
			}
		}
	}
}
