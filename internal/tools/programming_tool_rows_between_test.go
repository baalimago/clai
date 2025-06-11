package tools

import (
	"os"
	"testing"
)

func TestRowsBetweenTool_Call(t *testing.T) {
	const fileName = "test_rows_between.txt"
	initial := "one\ntwo\nthree\nfour\nfive\n"
	err := os.WriteFile(fileName, []byte(initial), 0o644)
	if err != nil {
		t.Fatalf("setup failed: %v", err)
	}
	defer os.Remove(fileName)

	cases := []struct {
		start, end int
		expected   string
	}{
		{1, 3, "1: one\n2: two\n3: three"},
		{2, 4, "2: two\n3: three\n4: four"},
		{4, 5, "4: four\n5: five"},
		{3, 3, "3: three"},
	}

	for _, tc := range cases {
		got, err := RowsBetween.Call(Input{
			"file_path":  fileName,
			"start_line": tc.start,
			"end_line":   tc.end,
		})
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if got != tc.expected {
			t.Errorf("unexpected output: got %q want %q (start=%d, end=%d)", got, tc.expected, tc.start, tc.end)
		}
	}
}

func TestRowsBetweenTool_BadInputs(t *testing.T) {
	_, err := RowsBetween.Call(Input{"file_path": "nonexistent.txt", "start_line": 1, "end_line": 3})
	if err == nil {
		t.Error("expected error for missing file")
	}

	_, err = RowsBetween.Call(Input{"file_path": "", "start_line": 1, "end_line": 3})
	if err == nil {
		t.Error("expected error for missing file_path")
	}
	_, err = RowsBetween.Call(Input{"file_path": "test_rows_between.txt", "start_line": -2, "end_line": 3})
	if err == nil {
		t.Error("expected error for bad start_line")
	}
	_, err = RowsBetween.Call(Input{"file_path": "test_rows_between.txt", "start_line": 4, "end_line": 2})
	if err == nil {
		t.Error("expected error for inverted lines")
	}
}
