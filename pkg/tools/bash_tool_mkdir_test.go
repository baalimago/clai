package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func TestMkdirTool_Call(t *testing.T) {
	tempDir := t.TempDir()

	t.Run("creates nested directory", func(t *testing.T) {
		targetDir := filepath.Join(tempDir, "a", "b", "c")

		out, err := Mkdir.Call(pub_models.Input{
			"directory": targetDir,
		})
		if err != nil {
			t.Fatalf("Mkdir.Call(): %v", err)
		}

		info, err := os.Stat(targetDir)
		if err != nil {
			t.Fatalf("stat created directory: %v", err)
		}
		if !info.IsDir() {
			t.Fatalf("expected %q to be a directory", targetDir)
		}
		if !strings.Contains(out, targetDir) {
			t.Fatalf("expected output to mention created path, got %q", out)
		}
	})

	t.Run("idempotent when directory already exists", func(t *testing.T) {
		targetDir := filepath.Join(tempDir, "already-there")
		if err := os.MkdirAll(targetDir, 0o755); err != nil {
			t.Fatalf("seed directory: %v", err)
		}

		_, err := Mkdir.Call(pub_models.Input{
			"directory": targetDir,
		})
		if err != nil {
			t.Fatalf("Mkdir.Call() on existing dir: %v", err)
		}
	})

	t.Run("errors when directory input missing", func(t *testing.T) {
		_, err := Mkdir.Call(pub_models.Input{})
		if err == nil {
			t.Fatal("expected error for missing directory input")
		}
		if !strings.Contains(err.Error(), "directory must be a string") {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

func TestMkdirTool_Specification(t *testing.T) {
	spec := Mkdir.Specification()
	if spec.Name != "mkdir" {
		t.Fatalf("spec name: got %q want %q", spec.Name, "mkdir")
	}
	if spec.Inputs == nil {
		t.Fatal("expected inputs schema")
	}
	if len(spec.Inputs.Required) != 1 || spec.Inputs.Required[0] != "directory" {
		t.Fatalf("required fields: got %v want [directory]", spec.Inputs.Required)
	}
}
