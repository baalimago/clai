package jsonl_test

import (
	"bytes"
	"testing"

	"github.com/baalimago/clai/internal/jsonl"
)

func TestChildOutputRecordKeepsTheChildsText(t *testing.T) {
	var out bytes.Buffer
	record := jsonl.ChildOutputRecord("mcp_fs", "stderr", "spin \x1b[31mred\x1b[0m\a\rdone \xff")
	if err := jsonl.NewEncoder(&out).Encode(record); err != nil {
		t.Fatalf("Encode: %v", err)
	}
	want := `{"type":"child_output","source":"mcp_fs","stream":"stderr","text":"spin \u001b[31mred\u001b[0m\u0007\rdone ` + "\ufffd" + `"}` + "\n"
	if got := out.String(); got != want {
		t.Fatalf("record = %s, want %s", got, want)
	}
}
