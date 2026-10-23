package tools

import (
	"bytes"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

const readmeImageLimit = 20 << 20

func viewImagePNG(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewGray(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

func writeFile(t *testing.T, path string, data []byte) string {
	t.Helper()
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

// CallParts.VI1, AC3
func TestViewImage_Returns(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	data := viewImagePNG(t)
	abs := writeFile(t, filepath.Join(home, "cat.png"), data)

	parts, err := ViewImage.CallParts(t.Context(), pub_models.Input{"path": "~/cat.png"})
	if err != nil {
		t.Fatalf("CallParts: %v", err)
	}
	if len(parts) != 2 {
		t.Fatalf("parts = %#v, want a TextPart and an ImagePart", parts)
	}
	img, ok := parts[1].(pub_models.ImagePart)
	if !ok {
		t.Fatalf("parts[1] = %T, want ImagePart", parts[1])
	}
	want := pub_models.ImagePart{Origin: pub_models.OriginTool, MIME: "image/png", Data: data}
	if !reflect.DeepEqual(img, want) {
		t.Fatalf("image = %+v, want pending %+v", img, want)
	}
	handle, err := img.Handle()
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if got, wantText := parts[0], (pub_models.TextPart{Text: "image " + handle + " from " + abs}); got != wantText {
		t.Fatalf("parts[0] = %#v, want %#v", got, wantText)
	}

	out, err := ViewImage.Call(pub_models.Input{"path": abs})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if wantOut := "image " + handle + " from " + abs + "\n[image " + handle + "]"; out != wantOut {
		t.Fatalf("Call = %q, want %q", out, wantOut)
	}
}

// CallParts.VI2
func TestViewImage_Missing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent.png")
	_, err := ViewImage.CallParts(t.Context(), pub_models.Input{"path": path})
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("err = %v, want fs.ErrNotExist", err)
	}
	if !strings.Contains(err.Error(), path) {
		t.Fatalf("err = %v, want it to name %s", err, path)
	}

	dir := t.TempDir()
	_, err = ViewImage.CallParts(t.Context(), pub_models.Input{"path": dir})
	if !errors.Is(err, fs.ErrInvalid) || !strings.Contains(err.Error(), dir) {
		t.Fatalf("directory err = %v, want fs.ErrInvalid naming %s", err, dir)
	}
}

// CallParts.VI3
func TestViewImage_NotImage(t *testing.T) {
	path := writeFile(t, filepath.Join(t.TempDir(), "notes.png"), []byte("plain text, not pixels\n"))
	_, err := ViewImage.CallParts(t.Context(), pub_models.Input{"path": path})
	if !errors.Is(err, ErrNotImage) {
		t.Fatalf("err = %v, want tools.ErrNotImage", err)
	}
	if !strings.Contains(err.Error(), path) {
		t.Fatalf("err = %v, want it to name %s", err, path)
	}
}

// CallParts.VI4
func TestViewImage_TooLarge(t *testing.T) {
	path := writeFile(t, filepath.Join(t.TempDir(), "huge.png"), viewImagePNG(t))
	if err := os.Truncate(path, readmeImageLimit+1); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	// Write-only: a read before the size check fails with a permission error instead.
	if err := os.Chmod(path, 0o200); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	_, err := ViewImage.CallParts(t.Context(), pub_models.Input{"path": path})
	var tooLarge *ImageTooLargeError
	if !errors.As(err, &tooLarge) {
		t.Fatalf("err = %v, want *tools.ImageTooLargeError", err)
	}
	want := ImageTooLargeError{Path: path, Size: readmeImageLimit + 1, Limit: readmeImageLimit}
	if *tooLarge != want {
		t.Fatalf("err = %+v, want %+v", *tooLarge, want)
	}
}

// CallParts.VI5, I6
func TestViewImage_NoStore(t *testing.T) {
	config := t.TempDir()
	t.Setenv("CLAI_CONFIG_DIR", config)
	path := writeFile(t, filepath.Join(t.TempDir(), "cat.png"), viewImagePNG(t))

	if _, err := ViewImage.CallParts(t.Context(), pub_models.Input{"path": path}); err != nil {
		t.Fatalf("CallParts: %v", err)
	}
	if _, err := os.Stat(filepath.Join(config, "conversations", "assets")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("stat assets dir: %v, want it absent", err)
	}
	entries, err := os.ReadDir(config)
	if err != nil {
		t.Fatalf("read config dir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("config dir holds %v, want nothing written", entries)
	}
}

// Specification.1
func TestViewImage_Schema(t *testing.T) {
	const schema = `{
  "name": "view_image",
  "description": "Look at a local image file (png, jpeg, gif, webp). Returns the image so you can see it.",
  "input_schema": {
    "type": "object",
    "required": ["path"],
    "properties": {
      "path": {"type": "string", "description": "Path to the image file. '~' expands to the home directory."}
    }
  }
}`
	got, err := json.Marshal(ViewImage.Specification())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var gotAny, wantAny any
	if err := json.Unmarshal(got, &gotAny); err != nil {
		t.Fatalf("unmarshal got: %v", err)
	}
	if err := json.Unmarshal([]byte(schema), &wantAny); err != nil {
		t.Fatalf("unmarshal want: %v", err)
	}
	if !reflect.DeepEqual(gotAny, wantAny) {
		t.Fatalf("Specification() = %s, want %s", got, schema)
	}
}

func TestViewImage_PathInput(t *testing.T) {
	for _, in := range []pub_models.Input{{}, {"path": ""}, {"path": 7}} {
		_, err := ViewImage.CallParts(t.Context(), in)
		if !errors.Is(err, errViewImagePath) {
			t.Fatalf("CallParts(%v) err = %v, want errViewImagePath", in, err)
		}
		if _, callErr := ViewImage.Call(in); callErr == nil || callErr.Error() != err.Error() {
			t.Fatalf("Call(%v) err = %v, want %v", in, callErr, err)
		}
	}
}
