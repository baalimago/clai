package tools

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func TestJQToolCall(t *testing.T) {
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq is not installed")
	}
	file := filepath.Join(t.TempDir(), "input.json")
	if err := os.WriteFile(file, []byte(`[{"speaker":"A"},{"speaker":"B"},{"speaker":"A"}]`), 0o600); err != nil {
		t.Fatal(err)
	}

	out, err := JQ.Call(pub_models.Input{
		"file":      file,
		"filter":    `group_by(.speaker) | map({speaker: .[0].speaker, count: length})`,
		"compact":   true,
		"sort_keys": true,
	})
	if err != nil {
		t.Fatalf("jq failed: %v", err)
	}
	if out != `[{"count":2,"speaker":"A"},{"count":1,"speaker":"B"}]`+"\n" {
		t.Fatalf("jq output = %q", out)
	}
}

func TestJQToolRawAndSlurp(t *testing.T) {
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq is not installed")
	}
	file := filepath.Join(t.TempDir(), "values.json")
	if err := os.WriteFile(file, []byte("{\"name\":\"first\"}\n{\"name\":\"second\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	out, err := JQ.Call(pub_models.Input{"file": file, "filter": `map(.name)[]`, "raw_output": true, "slurp": true})
	if err != nil {
		t.Fatalf("jq failed: %v", err)
	}
	if out != "first\nsecond\n" {
		t.Fatalf("jq output = %q", out)
	}
}

func TestJQToolSpecification(t *testing.T) {
	if got := JQ.Specification().Name; got != "jq" {
		t.Fatalf("name = %q, want jq", got)
	}
}

func TestJQToolValidationAndErrors(t *testing.T) {
	if _, err := JQ.Call(pub_models.Input{"file": "x"}); err == nil {
		t.Fatal("expected missing filter error")
	}
	if _, err := JQ.Call(pub_models.Input{"file": "x", "filter": ".", "compact": "yes"}); err == nil {
		t.Fatal("expected compact type error")
	}
	if _, err := exec.LookPath("jq"); err == nil {
		file := filepath.Join(t.TempDir(), "bad.json")
		if err := os.WriteFile(file, []byte("not json"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := JQ.Call(pub_models.Input{"file": file, "filter": "."}); err == nil {
			t.Fatal("expected jq parse error")
		}
	}
}

func TestJQToolArgumentOrderIsPortable(t *testing.T) {
	binDir := t.TempDir()
	argsFile := filepath.Join(t.TempDir(), "args")
	fake := filepath.Join(binDir, "jq")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$ARGS_FILE\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)
	t.Setenv("ARGS_FILE", argsFile)

	for _, tc := range []struct {
		name   string
		filter string
		want   []string
	}{
		{"flags before filter, -- before file", ".", []string{"-c", "-r", ".", "--", "-in.json"}},
		{"leading dash filter is not an option", "-1", []string{"-c", "-r", " -1", "--", "-in.json"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := JQ.Call(pub_models.Input{"file": "-in.json", "filter": tc.filter, "compact": true, "raw_output": true})
			if err != nil {
				t.Fatalf("JQ.Call(): %v", err)
			}
			data, err := os.ReadFile(argsFile)
			if err != nil {
				t.Fatal(err)
			}
			got := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
			if strings.Join(got, "|") != strings.Join(tc.want, "|") {
				t.Fatalf("args: got %q want %q", got, tc.want)
			}
		})
	}
}
