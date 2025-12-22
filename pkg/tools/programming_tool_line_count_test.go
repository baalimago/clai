package tools

import (
	"os"
	"testing"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func TestLineCountTool_Call(t *testing.T) {
	const fileName = "test_line_count.txt"
	content := "one\ntwo\nthree\n"
	if err := os.WriteFile(fileName, []byte(content), 0o644); err != nil {
		t.Fatalf("setup failed: %v", err)
	}
	defer os.Remove(fileName)

	out, err := LineCount.Call(pub_models.Input{"file_path": fileName})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "3" {
		t.Errorf("unexpected output: got %q want \"3\"", out)
	}
}

func TestLineCountTool_BadInputs(t *testing.T) {
	if _, err := LineCount.Call(pub_models.Input{"file_path": 123}); err == nil {
		t.Error("expected error for bad file_path type")
	}
	if _, err := LineCount.Call(pub_models.Input{"file_path": "no_such_file.txt"}); err == nil {
		t.Error("expected error for missing file")
	}
}
