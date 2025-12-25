package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func TestFindTool_Call(t *testing.T) {
	tmp := t.TempDir()
	os.WriteFile(filepath.Join(tmp, "a.txt"), []byte("hi"), 0o644)
	os.WriteFile(filepath.Join(tmp, "b.log"), []byte("bye"), 0o644)
	out, err := Find.Call(pub_models.Input{"directory": tmp, "name": "*.txt"})
	if err != nil {
		t.Fatalf("find failed: %v", err)
	}
	if !strings.Contains(out, "a.txt") {
		t.Errorf("expected to find a.txt, got %q", out)
	}
}

func TestFindTool_BadType(t *testing.T) {
	if _, err := Find.Call(pub_models.Input{"directory": 123}); err == nil {
		t.Error("expected error for bad directory type")
	}
}
