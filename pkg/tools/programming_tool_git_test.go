package tools

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func setupRepo(t *testing.T) string {
	dir := t.TempDir()
	cmd := exec.Command("git", "init")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init failed: %v, %s", err, out)
	}
	// Configure user
	exec.Command("git", "-C", dir, "config", "user.email", "test@example.com").Run()
	exec.Command("git", "-C", dir, "config", "user.name", "tester").Run()

	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello"), 0o644)
	exec.Command("git", "-C", dir, "add", "a.txt").Run()
	exec.Command("git", "-C", dir, "commit", "-m", "first").Run()

	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello world"), 0o644)
	exec.Command("git", "-C", dir, "commit", "-am", "second").Run()
	return dir
}

func TestGitTool_Log(t *testing.T) {
	repo := setupRepo(t)
	out, err := Git.Call(pub_models.Input{"operation": "log", "n": 1, "range": "HEAD", "dir": repo})
	if err != nil {
		t.Fatalf("git log failed: %v", err)
	}
	if !strings.Contains(out, "second") {
		t.Errorf("expected log to contain second commit, got %q", out)
	}
}

func TestGitTool_Diff(t *testing.T) {
	repo := setupRepo(t)
	out, err := Git.Call(pub_models.Input{"operation": "diff", "range": "HEAD~1..HEAD", "file": "a.txt", "dir": repo})
	if err != nil {
		t.Fatalf("git diff failed: %v", err)
	}
	if !strings.Contains(out, "hello world") {
		t.Errorf("unexpected diff output: %q", out)
	}
}

func TestGitTool_Status(t *testing.T) {
	repo := setupRepo(t)
	os.WriteFile(filepath.Join(repo, "b.txt"), []byte("x"), 0o644)
	out, err := Git.Call(pub_models.Input{"operation": "status", "dir": repo})
	if err != nil {
		t.Fatalf("git status failed: %v", err)
	}
	if !strings.Contains(out, "?? b.txt") {
		t.Errorf("expected status to show untracked file, got %q", out)
	}
}

func TestGitTool_RangeHoldsSeveralArguments(t *testing.T) {
	repo := setupRepo(t)
	out, err := Git.Call(pub_models.Input{"operation": "diff", "range": "--stat  HEAD~1...HEAD", "dir": repo})
	if err != nil {
		t.Fatalf("git diff --stat failed: %v", err)
	}
	if !strings.Contains(out, "a.txt |") {
		t.Errorf("expected a diffstat, got %q", out)
	}
}

func TestGitTool_RangeKeepsAQuotedArgumentWhole(t *testing.T) {
	repo := setupRepo(t)
	out, err := Git.Call(pub_models.Input{"operation": "log", "n": 1, "range": `"--format=%s by %an" HEAD`, "dir": repo})
	if err != nil {
		t.Fatalf("git log failed: %v", err)
	}
	if strings.TrimSpace(out) != "second by tester" {
		t.Errorf("want %q, got %q", "second by tester", out)
	}
}

func TestGitTool_RangeErrors(t *testing.T) {
	repo := setupRepo(t)
	for name, r := range map[string]string{
		"unterminated quote": `"--format=%s HEAD`,
		"output flag":        "--output=leak.txt HEAD~1",
		"output flag split":  "--output leak.txt",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Git.Call(pub_models.Input{"operation": "diff", "range": r, "dir": repo}); err == nil {
				t.Fatalf("range %q: want an error", r)
			}
			if _, err := os.Stat(filepath.Join(repo, "leak.txt")); err == nil {
				t.Fatalf("range %q wrote a file", r)
			}
		})
	}
}
