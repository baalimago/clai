package tools

import (
	"testing"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func TestTailToolCall(t *testing.T) {
	file := writeLineFixture(t)
	assertCountedToolCalls(t, Tail, []countedToolCase{
		{"default", pub_models.Input{"file": file}, "3\n4\n5\n6\n7\n8\n9\n10\n11\n12\n"},
		{"lines", pub_models.Input{"file": file, "lines": float64(2)}, "11\n12\n"},
		{"bytes", pub_models.Input{"file": file, "bytes": float64(3)}, "12\n"},
	})
}

func TestTailToolSpecification(t *testing.T) {
	var _ TailTool = Tail
	if got := Tail.Specification().Name; got != "tail" {
		t.Fatalf("name = %q, want tail", got)
	}
}

func TestTailToolRejectsInvalidInput(t *testing.T) {
	assertCountedToolRejects(t, Tail, writeLineFixture(t))
}
