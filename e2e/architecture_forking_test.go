package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baalimago/clai/e2e/e2etest"
)

func Test_architecture_noFutureForking(t *testing.T) {
	dir := filepath.Join(e2etest.ModuleRoot(t), "architecture")
	files, err := filepath.Glob(filepath.Join(dir, "*.md"))
	if err != nil || len(files) == 0 {
		t.Fatalf("architecture notes: %v, %d files", err, len(files))
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, banned := range []string{"Forking (future)", "documented but not implemented"} {
			if strings.Contains(string(b), banned) {
				t.Errorf("%s still says %q", filepath.Base(f), banned)
			}
		}
	}
	for file, wants := range map[string][]string{
		"continue-from-claudex.md": {"clai chat fork", "`SourceID = <parent-chat-id>`", "never written"},
		"chat-groups.md":           {"clai chat fork", "inherits the parent's GroupKey", "own spend"},
	} {
		b, err := os.ReadFile(filepath.Join(dir, file))
		if err != nil || len(b) == 0 {
			t.Fatalf("read %s: %v (%d bytes)", file, err, len(b))
		}
		for _, want := range wants {
			if !strings.Contains(string(b), want) {
				t.Errorf("%s lacks %q", file, want)
			}
		}
	}
}
