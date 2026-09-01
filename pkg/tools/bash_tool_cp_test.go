package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func TestCpTool_Call(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	destination := filepath.Join(root, "destination")
	if err := os.MkdirAll(filepath.Join(source, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "nested", "file.txt"), []byte("copied"), 0o640); err != nil {
		t.Fatal(err)
	}

	out, err := Cp.Call(pub_models.Input{
		"source":      source,
		"destination": destination,
	})
	if err != nil {
		t.Fatalf("Cp.Call(): %v", err)
	}
	got, err := os.ReadFile(filepath.Join(destination, "nested", "file.txt"))
	if err != nil {
		t.Fatalf("read copied file: %v", err)
	}
	if string(got) != "copied" {
		t.Fatalf("copied content: got %q", got)
	}
	if !strings.Contains(out, source) || !strings.Contains(out, destination) {
		t.Fatalf("output does not identify copy: %q", out)
	}
}

func TestCpTool_ValidatesPreserve(t *testing.T) {
	_, err := Cp.Call(pub_models.Input{
		"source":      "source",
		"destination": "destination",
		"preserve":    "yes",
	})
	if err == nil || !strings.Contains(err.Error(), "preserve must be a boolean") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCpTool_Specification(t *testing.T) {
	spec := Cp.Specification()
	if spec.Name != "cp" {
		t.Fatalf("spec name: got %q want cp", spec.Name)
	}
	if spec.Inputs == nil || len(spec.Inputs.Required) != 2 {
		t.Fatalf("unexpected input schema: %+v", spec.Inputs)
	}
}
