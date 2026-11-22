package agent

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	priv_models "github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/runenv"
	"github.com/baalimago/clai/internal/text"
)

// TestAgentRunInstallsNoManPage: see architecture/manpage.md, First-run install.
func TestAgentRunInstallsNoManPage(t *testing.T) {
	root := t.TempDir()
	home, data, bin := filepath.Join(root, "home"), filepath.Join(root, "data"), filepath.Join(root, "prefix", "bin")
	for _, dir := range []string{home, data, bin} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(exe, filepath.Join(bin, "clai")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", data)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	var seen []runenv.Env
	a := New(WithPrompt("hi"))
	a.cfgDir = t.TempDir()
	a.stdin, a.stdinIsTerminal = strings.NewReader("\n"), true
	a.querierCreator = func(ctx context.Context, _ text.Configurations) (priv_models.Querier, error) {
		return envQuerier{seen: &seen}, nil
	}
	if err := a.Setup(context.Background()); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if _, err := a.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == "man" || d.Name() == "man1") {
			t.Errorf("an agent run created %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
