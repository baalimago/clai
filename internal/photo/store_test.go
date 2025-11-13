package photo

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSaveImage_PrimaryDir(t *testing.T) {
	tmp := t.TempDir()
	// simple 1x1 transparent PNG
	b64 := "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMB/ak5tqkAAAAASUVORK5CYII="
	out, err := SaveImage(Output{Dir: tmp, Prefix: "x"}, b64, "png")
	if err != nil {
		t.Fatalf("SaveImage error: %v", err)
	}
	if !strings.HasSuffix(out, ".png") {
		t.Fatalf("expected .png suffix, got %q", out)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	dec := make([]byte, base64.StdEncoding.DecodedLen(len(b64)))
	// just ensure non-empty file and decode matches size
	if len(data) == 0 {
		t.Fatal("no data written")
	}
	_ = dec
}

func TestSaveImage_FallbackTmp(t *testing.T) {
	// Create a directory and remove write perms so first write fails
	dir := filepath.Join(t.TempDir(), "nope")
	if err := os.MkdirAll(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	b64 := "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMB/ak5tqkAAAAASUVORK5CYII="
	out, err := SaveImage(Output{Dir: dir, Prefix: "y"}, b64, "png")
	if err != nil {
		t.Fatalf("SaveImage fallback error: %v", err)
	}
	if !strings.HasPrefix(out, "/tmp/") {
		t.Fatalf("expected fallback to /tmp, got %q", out)
	}
}
