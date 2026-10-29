package app

import (
	"bytes"
	"debug/pe"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

func TestWindowsUpdateRejectsOtherCPU(t *testing.T) {
	for _, tc := range []struct {
		arch    string
		machine uint16
	}{{"amd64", pe.IMAGE_FILE_MACHINE_AMD64}, {"arm64", pe.IMAGE_FILE_MACHINE_ARM64}} {
		t.Run(tc.arch, func(t *testing.T) {
			var image bytes.Buffer
			dos := make([]byte, 128)
			copy(dos, "MZ")
			binary.LittleEndian.PutUint32(dos[60:], 128)
			image.Write(dos)
			image.Write([]byte{'P', 'E', 0, 0})
			write := func(v any) {
				if err := binary.Write(&image, binary.LittleEndian, v); err != nil {
					t.Fatal(err)
				}
			}
			write(pe.FileHeader{Machine: tc.machine, NumberOfSections: 1, SizeOfOptionalHeader: 240, Characteristics: pe.IMAGE_FILE_EXECUTABLE_IMAGE})
			write(pe.OptionalHeader64{Magic: 0x20b, AddressOfEntryPoint: 0x1000, NumberOfRvaAndSizes: 16})
			write(pe.SectionHeader32{Name: [8]byte{'.', 't', 'e', 'x', 't'}, VirtualSize: 1, VirtualAddress: 0x1000, SizeOfRawData: 512, PointerToRawData: 512, Characteristics: pe.IMAGE_SCN_MEM_EXECUTE})
			data := make([]byte, 1024)
			copy(data, image.Bytes())
			path := filepath.Join(t.TempDir(), "Ally.exe")
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			if err := validateStagedExecutableForArch(path, tc.arch); err != nil {
				t.Fatal(err)
			}
			other := "amd64"
			if tc.arch == other {
				other = "arm64"
			}
			if err := validateStagedExecutableForArch(path, other); err == nil {
				t.Fatal("accepted an update for another CPU")
			}
		})
	}
}
