package tools

import (
	"os"
	"testing"
)

func TestSedTool_Call(t *testing.T) {
	const fileName = "test_sed.txt"
	initial := "apple\nbanana\napple pie\n"
	err := os.WriteFile(fileName, []byte(initial), 0o644)
	if err != nil {
		t.Fatalf("setup failed: %v", err)
	}
	defer os.Remove(fileName)

	_, err = Sed.Call(Input{
		"file_path": fileName,
		"pattern":   "apple",
		"repl":      "orange",
	})
	if err != nil {
		t.Fatalf("sed failed: %v", err)
	}

	result, err := os.ReadFile(fileName)
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}

	expected := "orange\nbanana\norange pie\n"
	if string(result) != expected {
		t.Errorf("unexpected output: got\n%q\nwant\n%q", string(result), expected)
	}
}

func TestSedTool_Range(t *testing.T) {
	const fileName = "test_sed_range.txt"
	initial := "foo\nfoo\nfoo\nfoo\n"
	err := os.WriteFile(fileName, []byte(initial), 0o644)
	if err != nil {
		t.Fatalf("setup failed: %v", err)
	}
	defer os.Remove(fileName)

	_, err = Sed.Call(Input{
		"file_path":  fileName,
		"pattern":    "foo",
		"repl":       "bar",
		"start_line": 2,
		"end_line":   3,
	})
	if err != nil {
		t.Fatalf("sed with range failed: %v", err)
	}

	result, err := os.ReadFile(fileName)
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}

	expected := "foo\nbar\nbar\nfoo\n"
	if string(result) != expected {
		t.Errorf("unexpected output: got\n%q\nwant\n%q", string(result), expected)
	}
}
