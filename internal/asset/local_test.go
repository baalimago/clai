package asset_test

import (
	"errors"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baalimago/clai/internal/asset"
)

// readmeImageLimit is the README parameter "Largest local image file read".
const readmeImageLimit = 20 << 20

func TestReadmeParameters(t *testing.T) {
	if asset.LargestLocalImage != readmeImageLimit {
		t.Fatalf("LargestLocalImage = %d, want the README's 20 MiB", asset.LargestLocalImage)
	}
	want := map[string]string{"image/png": "png", "image/jpeg": "jpg", "image/gif": "gif", "image/webp": "webp"}
	got := asset.MIMEExtensions()
	if !maps.Equal(got, want) {
		t.Fatalf("MIMEExtensions() = %v, want the README parameter %v", got, want)
	}
	got["image/tiff"] = "tif"
	delete(got, "image/png")
	if again := asset.MIMEExtensions(); !maps.Equal(again, want) {
		t.Fatalf("a caller's edit leaked into MIMEExtensions(): %v", again)
	}
}

func writeLocal(t *testing.T, name string, data []byte, perm fs.FileMode) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	if err := os.Chmod(path, perm); err != nil {
		t.Fatalf("chmod %s: %v", path, err)
	}
	return path
}

func TestReadLocalImage(t *testing.T) {
	t.Run("image", func(t *testing.T) {
		for _, tc := range []struct {
			name, mime string
			data       []byte
		}{
			{"a.png", "image/png", pngBytes(t, 40)},
			{"b.bin", "image/jpeg", jpegBytes(t)},
			{"c.txt", "image/gif", gifBytes(t)},
			{"d", "image/webp", webpBytes()},
		} {
			path := writeLocal(t, tc.name, tc.data, 0o644)
			img, err := asset.ReadLocalImage(path)
			if err != nil {
				t.Fatalf("ReadLocalImage(%s): %v", tc.name, err)
			}
			if string(img.Data) != string(tc.data) || img.MIME != tc.mime {
				t.Fatalf("ReadLocalImage(%s) = %d bytes %q, want %d bytes %q", tc.name, len(img.Data), img.MIME, len(tc.data), tc.mime)
			}
		}
	})

	t.Run("failures name the path", func(t *testing.T) {
		missing := filepath.Join(t.TempDir(), "absent.png")
		dir := t.TempDir()
		text := writeLocal(t, "notes.png", []byte("plain text, not pixels"), 0o644)
		for _, tc := range []struct {
			name, path string
			want       error
		}{
			{"missing", missing, fs.ErrNotExist},
			{"directory", dir, fs.ErrInvalid},
			{"not an image", text, asset.ErrNotImage},
		} {
			_, err := asset.ReadLocalImage(tc.path)
			if !errors.Is(err, tc.want) {
				t.Fatalf("%s: err = %v, want %v", tc.name, err, tc.want)
			}
			if !strings.Contains(err.Error(), tc.path) {
				t.Fatalf("%s: err %q does not name %s", tc.name, err, tc.path)
			}
		}
	})

	t.Run("unreadable", func(t *testing.T) {
		path := writeLocal(t, "locked.png", pngBytes(t, 41), 0)
		_, err := asset.ReadLocalImage(path)
		if !errors.Is(err, fs.ErrPermission) || !strings.Contains(err.Error(), path) {
			t.Fatalf("err = %v, want a permission error naming %s", err, path)
		}
	})

	t.Run("too large before the read", func(t *testing.T) {
		path := writeLocal(t, "huge.png", pngBytes(t, 42), 0o644)
		if err := os.Truncate(path, readmeImageLimit+1); err != nil {
			t.Fatalf("truncate: %v", err)
		}
		// Write-only: a read before the size check fails with a permission error instead.
		if err := os.Chmod(path, 0o200); err != nil {
			t.Fatalf("chmod: %v", err)
		}
		_, err := asset.ReadLocalImage(path)
		var tooLarge *asset.ImageTooLargeError
		if !errors.As(err, &tooLarge) {
			t.Fatalf("err = %v, want *asset.ImageTooLargeError", err)
		}
		if want := (asset.ImageTooLargeError{Path: path, Size: readmeImageLimit + 1, Limit: readmeImageLimit}); *tooLarge != want {
			t.Fatalf("ImageTooLargeError = %+v, want %+v", *tooLarge, want)
		}
	})
}
