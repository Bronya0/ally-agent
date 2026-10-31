package app

import (
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"unicode/utf16"
)

func TestImportedConfigDecodesSupportedTextEncodings(t *testing.T) {
	const text = `{"name":"中文配置","models":[],"mcpServers":{},"nodes":[]}`
	variants := map[string][]byte{"utf8": []byte(text), "utf8-bom": append([]byte{0xef, 0xbb, 0xbf}, []byte(text)...)}
	for _, le := range []bool{true, false} {
		var order binary.ByteOrder = binary.BigEndian
		name, bom := "utf16be", []byte{0xfe, 0xff}
		if le {
			order, name, bom = binary.LittleEndian, "utf16le", []byte{0xff, 0xfe}
		}
		var data []byte
		for _, unit := range utf16.Encode([]rune(text)) {
			var encoded [2]byte
			order.PutUint16(encoded[:], unit)
			data = append(data, encoded[:]...)
		}
		variants[name] = data
		variants[name+"-bom"] = append(bom, data...)
	}
	for name, data := range variants {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			got, err := (&App{}).ReadLocalTextFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if got != text || !json.Valid([]byte(got)) {
				t.Fatalf("invalid or changed import: %q", got)
			}
		})
	}
}

func TestImportedConfigRejectsInvalidTextAndOversize(t *testing.T) {
	for _, data := range [][]byte{{0xff}, {0xff, 0xfe, 0x01}, make([]byte, (2<<20)+1)} {
		path := filepath.Join(t.TempDir(), "config.json")
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := (&App{}).ReadLocalTextFile(path); err == nil {
			t.Fatal("invalid import accepted")
		}
	}
}
