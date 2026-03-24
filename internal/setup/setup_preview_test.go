package setup

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/baalimago/clai/internal/utils"
)

func TestSelectConfigItem_PreviewsSelectedItemBeforeActionPrompt(t *testing.T) {
	t.Helper()

	tmpFile, err := os.CreateTemp(t.TempDir(), "preview-*.json")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer tmpFile.Close()

	content := map[string]any{"model": "gpt-test", "temperature": 0.7}
	b, err := json.Marshal(content)
	if err != nil {
		t.Fatalf("failed to marshal config: %v", err)
	}
	if _, err := tmpFile.Write(b); err != nil {
		t.Fatalf("failed to write temp config: %v", err)
	}

	inputs := []string{"0", "b"}
	inputIdx := 0
	restoreInput := utils.UseReadUserInputForTests(func() (string, error) {
		if inputIdx >= len(inputs) {
			return "", io.EOF
		}
		ret := inputs[inputIdx]
		inputIdx++
		return ret, nil
	})
	defer restoreInput()

	origStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("failed to create stdout pipe: %v", err)
	}
	os.Stdout = w
	defer func() {
		os.Stdout = origStdout
	}()

	err = selectConfigItem(
		setupCategory{name: "model files", itemActions: []action{conf}},
		[]config{{name: "preview.json", filePath: tmpFile.Name()}},
	)

	_ = w.Close()
	var out bytes.Buffer
	if _, copyErr := io.Copy(&out, r); copyErr != nil {
		t.Fatalf("failed to capture stdout: %v", copyErr)
	}

	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(out.String(), "\"model\": \"gpt-test\"") {
		t.Fatalf("expected selected item preview in output, got: %q", out.String())
	}
	if !strings.Contains(out.String(), "Choose action ([c]onfigure):") {
		t.Fatalf("expected action prompt in output, got: %q", out.String())
	}
}
