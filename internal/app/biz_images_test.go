// SPDX-License-Identifier: GPL-3.0-only
package app

import (
	"bytes"
	"errors"
	"image"
	"image/jpeg"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGeneratedImageOriginalFilenames(t *testing.T) {
	t.Parallel()
	a := newIsolatedChatTestApp(t)
	var jpg bytes.Buffer
	if err := jpeg.Encode(&jpg, image.NewRGBA(image.Rect(0, 0, 3, 2)), nil); err != nil {
		t.Fatal(err)
	}
	names := make(map[string]bool)
	for _, tc := range []struct {
		ext  string
		data []byte
	}{
		{".png", imageTestPNG(t)},
		{".jpg", jpg.Bytes()},
	} {
		for range 2 {
			meta, err := a.saveGeneratedImage(tc.data, GeneratedImage{SessionID: "originals"})
			if err != nil {
				t.Fatal(err)
			}
			name := generatedImageFilename(meta.Ref, meta.Mime)
			if filepath.Ext(name) != tc.ext || names[name] {
				t.Fatalf("wrong or reused download filename: %q", name)
			}
			names[name] = true
			path := filepath.Join(a.imagesDir(), imageSessionKey(meta.SessionID), name)
			if data, err := os.ReadFile(path); err != nil || !bytes.Equal(data, tc.data) {
				t.Fatalf("original bytes not preserved in %s: %v", name, err)
			}
			// Reopening the library must preserve both references and names.
			reopened := newAppState(a.AppDataDir())
			data, loaded, err := reopened.readGeneratedImage(meta.Ref)
			if err != nil || !bytes.Equal(data, tc.data) || loaded.Ref != meta.Ref || loaded.URL != meta.URL || generatedImageFilename(loaded.Ref, loaded.Mime) != name {
				t.Fatalf("stored original changed after reopening: %v", err)
			}
		}
	}
	ref := "image:" + strings.Repeat("a", 64) + "/" + strings.Repeat("b", 32)
	if name := generatedImageFilename(ref, "image/webp"); name != strings.Repeat("b", 32)+".webp" {
		t.Fatalf("wrong WebP download filename: %q", name)
	}
}

func TestGeneratedImageLegacyMigration(t *testing.T) {
	t.Parallel()
	for _, collision := range []bool{false, true} {
		t.Run(map[bool]string{false: "migrate on read", true: "keep existing original"}[collision], func(t *testing.T) {
			a := newIsolatedChatTestApp(t)
			original := imageTestPNG(t)
			meta, err := a.saveGeneratedImage(original, GeneratedImage{SessionID: "legacy"})
			if err != nil {
				t.Fatal(err)
			}
			base := filepath.Join(a.imagesDir(), filepath.FromSlash(strings.TrimPrefix(meta.Ref, "image:")))
			if err := os.Rename(base+".png", base+".img"); err != nil {
				t.Fatal(err)
			}
			want := original
			if collision {
				want = append(append([]byte(nil), original...), 0)
				if err := os.WriteFile(base+".png", want, 0600); err != nil {
					t.Fatal(err)
				}
			}
			data, loaded, err := a.readGeneratedImage(meta.Ref)
			if err != nil || !bytes.Equal(data, want) || loaded.URL != meta.URL {
				t.Fatalf("read=%v %v", loaded, err)
			}
			if disk, err := os.ReadFile(base + ".png"); err != nil || !bytes.Equal(disk, want) {
				t.Fatalf("current original=%v", err)
			}
			if collision {
				if disk, err := os.ReadFile(base + ".img"); err != nil || !bytes.Equal(disk, original) {
					t.Fatalf("legacy original lost: %v", err)
				}
			} else if _, err := os.Stat(base + ".img"); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("legacy file not migrated: %v", err)
			}
			if again, _, err := a.readGeneratedImage(meta.Ref); err != nil || !bytes.Equal(again, want) {
				t.Fatalf("repeat read: %v", err)
			}
		})
	}
}
