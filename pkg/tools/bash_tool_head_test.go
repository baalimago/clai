package tools

import (
	"os"
	"path/filepath"
	"testing"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

type countedToolCase struct {
	name     string
	input    pub_models.Input
	expected string
}

func TestHeadToolCall(t *testing.T) {
	file := writeLineFixture(t)
	assertCountedToolCalls(t, Head, []countedToolCase{
		{"default", pub_models.Input{"file": file}, "1\n2\n3\n4\n5\n6\n7\n8\n9\n10\n"},
		{"lines", pub_models.Input{"file": file, "lines": float64(2)}, "1\n2\n"},
		{"bytes", pub_models.Input{"file": file, "bytes": float64(3)}, "1\n2"},
	})
}

func TestHeadToolSpecification(t *testing.T) {
	var _ HeadTool = Head
	if got := Head.Specification().Name; got != "head" {
		t.Fatalf("name = %q, want head", got)
	}
}

func TestHeadToolRejectsInvalidInput(t *testing.T) {
	assertCountedToolRejects(t, Head, writeLineFixture(t))
}

func assertCountedToolCalls(t *testing.T, tool pub_models.LLMTool, cases []countedToolCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := tool.Call(tc.input)
			if err != nil {
				t.Fatalf("call failed: %v", err)
			}
			if out != tc.expected {
				t.Fatalf("output = %q, want %q", out, tc.expected)
			}
		})
	}
}

func assertCountedToolRejects(t *testing.T, tool pub_models.LLMTool, file string) {
	t.Helper()
	for name, input := range map[string]pub_models.Input{
		"missing file":      {},
		"lines and bytes":   {"file": file, "lines": float64(1), "bytes": float64(1)},
		"fractional lines":  {"file": file, "lines": 1.5},
		"overflowing lines": {"file": file, "lines": 1e19},
		"non-numeric bytes": {"file": file, "bytes": "3"},
		"absent file":       {"file": filepath.Join(t.TempDir(), "missing")},
	} {
		if _, err := tool.Call(input); err == nil {
			t.Errorf("%s: accepted %s", tool.Specification().Name, name)
		}
	}
}

func writeLineFixture(t *testing.T) string {
	t.Helper()
	file := filepath.Join(t.TempDir(), "lines.txt")
	if err := os.WriteFile(file, []byte("1\n2\n3\n4\n5\n6\n7\n8\n9\n10\n11\n12\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return file
}
