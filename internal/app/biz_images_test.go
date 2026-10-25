// SPDX-License-Identifier: GPL-3.0-only
package app

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/jpeg"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGeneratedImageOriginalFilenames(t *testing.T) {
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
	for _, mode := range []string{"normal", "cancelled", "collision"} {
		t.Run(mode, func(t *testing.T) {
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
			if data, loaded, err := a.readGeneratedImage(meta.Ref); err != nil || !bytes.Equal(data, original) || loaded.URL != meta.URL {
				t.Fatalf("legacy reference cannot be read before migration: %v", err)
			}
			orphan := filepath.Join(filepath.Dir(base), strings.Repeat("c", 32)+".img")
			if err := os.WriteFile(orphan, original, 0o600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if mode == "cancelled" {
				cancel()
			}
			// A partially migrated library may contain both names. Never
			// overwrite an existing target or discard the legacy original.
			collision := append(append([]byte(nil), original...), 0)
			if mode == "collision" {
				if err := os.WriteFile(base+".png", collision, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			err = a.migrateLegacyImageFiles(ctx)
			if mode == "cancelled" {
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("expected cancellation, got %v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if mode == "normal" {
				if _, err := os.Stat(base + ".img"); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("legacy name remains after migration: %v", err)
				}
				if data, loaded, err := a.readGeneratedImage(meta.Ref); err != nil || !bytes.Equal(data, original) || loaded.Ref != meta.Ref || loaded.URL != meta.URL {
					t.Fatalf("migration changed the original or its reference: %v", err)
				}
				if err := a.migrateLegacyImageFiles(ctx); err != nil {
					t.Fatalf("repeated migration failed: %v", err)
				}
			} else {
				if data, err := os.ReadFile(base + ".img"); err != nil || !bytes.Equal(data, original) {
					t.Fatalf("migration discarded a legacy original: %v", err)
				}
				if mode == "collision" {
					if data, err := os.ReadFile(base + ".png"); err != nil || !bytes.Equal(data, collision) {
						t.Fatalf("migration overwrote an existing target: %v", err)
					}
				}
			}
			if data, err := os.ReadFile(orphan); err != nil || !bytes.Equal(data, original) {
				t.Fatalf("migration changed an incomplete record: %v", err)
			}
		})
	}
}
