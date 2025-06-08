package tools

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCatTool_Call(t *testing.T) {
	tmp := t.TempDir()
	f := filepath.Join(tmp, "file.txt")
	if err := os.WriteFile(f, []byte("hello\nworld"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := Cat.Call(Input{"file": f})
	if err != nil {
		t.Fatalf("cat failed: %v", err)
	}
	if out != "hello\nworld" {
		t.Errorf("unexpected output: %q", out)
	}
}

func TestCatTool_BadType(t *testing.T) {
	if _, err := Cat.Call(Input{"file": 123}); err == nil {
		t.Error("expected error for bad file type")
	}
}
