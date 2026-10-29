// SPDX-License-Identifier: GPL-3.0-only
package app

import (
	"bytes"
	"debug/macho"
	"debug/pe"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

func validateStagedExecutable(path string) error {
	return validateStagedExecutableForArch(path, runtime.GOARCH)
}

func validateStagedExecutableForArch(path, arch string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("staged executable is not regular")
	}
	file, err := pe.Open(path)
	if err != nil {
		return fmt.Errorf("invalid PE: %w", err)
	}
	defer file.Close()
	expected := uint16(pe.IMAGE_FILE_MACHINE_AMD64)
	if arch == "arm64" {
		expected = pe.IMAGE_FILE_MACHINE_ARM64
	} else if arch != "amd64" {
		return errors.New("unsupported Windows update architecture")
	}
	header, ok := file.OptionalHeader.(*pe.OptionalHeader64)
	if !ok || file.Machine != expected || file.Characteristics&pe.IMAGE_FILE_EXECUTABLE_IMAGE == 0 || file.Characteristics&pe.IMAGE_FILE_DLL != 0 || header.AddressOfEntryPoint == 0 {
		return errors.New("staged executable is not an executable Windows image for this CPU")
	}
	validEntry := false
	for _, section := range file.Sections {
		if int64(section.Offset)+int64(section.Size) > info.Size() {
			return errors.New("truncated PE section")
		}
		if section.Characteristics&pe.IMAGE_SCN_MEM_EXECUTE != 0 && header.AddressOfEntryPoint >= section.VirtualAddress && uint64(header.AddressOfEntryPoint) < uint64(section.VirtualAddress)+uint64(section.VirtualSize) {
			validEntry = true
		}
	}
	if !validEntry {
		return errors.New("PE entry point is outside executable sections")
	}
	return nil
}

func validateStagedDMG(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() < 1024*1024 {
		return errors.New("invalid DMG file")
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	trailer := make([]byte, 512)
	if _, err := file.ReadAt(trailer, info.Size()-512); err != nil {
		return err
	}
	if !bytes.Equal(trailer[:4], []byte("koly")) || binary.BigEndian.Uint32(trailer[4:8]) != 4 || binary.BigEndian.Uint32(trailer[8:12]) != 512 {
		return errors.New("invalid UDIF trailer")
	}
	limit := uint64(info.Size() - 512)
	for _, pair := range [][2]int{{24, 32}, {40, 48}, {216, 224}} {
		offset := binary.BigEndian.Uint64(trailer[pair[0] : pair[0]+8])
		size := binary.BigEndian.Uint64(trailer[pair[1] : pair[1]+8])
		if offset > limit || size > limit-offset {
			return errors.New("DMG data range exceeds archive")
		}
	}
	return nil
}

func validateMacAppBundle(path string) error { return validateMacUpdateBundle(path, "") }

func validateMacUpdateBundle(path, tag string) error {
	root, err := os.OpenRoot(path)
	if err != nil {
		return err
	}
	defer root.Close()
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("app bundle must be a real directory")
	}
	for _, name := range []string{"Contents/Info.plist", "Contents/MacOS/Ally"} {
		if err := updateNoLinks(root, filepath.FromSlash(name), false); err != nil {
			return err
		}
	}
	exe := filepath.Join(path, "Contents", "MacOS", "Ally")
	exeInfo, err := os.Lstat(exe)
	if err != nil {
		return err
	}
	if !exeInfo.Mode().IsRegular() || exeInfo.Mode().Perm()&0o111 == 0 {
		return errors.New("main executable is not executable")
	}
	if err := validateUpdateMachO(exe); err != nil {
		return err
	}
	if runtime.GOOS != "darwin" {
		return errors.New("bundle metadata validation requires macOS")
	}
	data, err := exec.Command("/usr/bin/plutil", "-convert", "json", "-o", "-", filepath.Join(path, "Contents", "Info.plist")).Output()
	if err != nil {
		return fmt.Errorf("invalid bundle metadata: %w", err)
	}
	var metadata map[string]any
	if err := json.Unmarshal(data, &metadata); err != nil {
		return err
	}
	if metadata["CFBundleIdentifier"] != "io.github.bronya0.ally" || metadata["CFBundleExecutable"] != "Ally" || metadata["CFBundlePackageType"] != "APPL" {
		return errors.New("update bundle is not Ally")
	}
	if tag != "" && (metadata["CFBundleShortVersionString"] != strings.TrimPrefix(tag, "v") || metadata["CFBundleVersion"] != strings.TrimPrefix(tag, "v")) {
		return errors.New("bundle version does not match requested update")
	}
	count := 0
	var total int64
	return filepath.WalkDir(path, func(name string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		count++
		if count > maxUpdateZipEntries {
			return errors.New("too many bundle entries")
		}
		rel, err := filepath.Rel(path, name)
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			// Frameworks use internal links. Root rejects absolute and escaping links.
			_, err := root.Stat(rel)
			return err
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return errors.New("unsupported bundle file type")
		}
		total += info.Size()
		if total > maxUpdateExtractBytes {
			return errors.New("bundle exceeds expanded size limit")
		}
		return nil
	})
}

func validateUpdateMachO(path string) error {
	valid := func(file *macho.File) bool {
		expected := macho.CpuAmd64
		if runtime.GOARCH == "arm64" {
			expected = macho.CpuArm64
		}
		return file.Type == macho.TypeExec && file.Cpu == expected && len(file.Loads) > 0
	}
	fat, err := macho.OpenFat(path)
	if err == nil {
		defer fat.Close()
		for _, arch := range fat.Arches {
			if valid(arch.File) {
				return nil
			}
		}
		return errors.New("bundle has no executable for this CPU")
	}
	file, err := macho.Open(path)
	if err != nil {
		return fmt.Errorf("invalid Mach-O: %w", err)
	}
	defer file.Close()
	if !valid(file) {
		return errors.New("bundle main file is not a compatible executable")
	}
	return nil
}
