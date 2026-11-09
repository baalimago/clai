package setup

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/baalimago/clai/internal/jsonl"
	"github.com/baalimago/clai/internal/runenv"
	"github.com/baalimago/clai/internal/stdio"
)

type promptRecord struct {
	Type, Kind, Text string
	Choices          []string
}

// jsonSetupRun runs fn under the JSON format with answers on stdin and
// returns the stdout records.
func jsonSetupRun(t *testing.T, answers string, fn func(ctx context.Context) error) []promptRecord {
	t.Helper()
	var out bytes.Buffer
	env := runenv.New(runenv.Config{
		Streams: stdio.New(stdio.Config{Format: jsonl.FormatJSON, Stdin: strings.NewReader(answers), Stdout: &out, Stderr: io.Discard}),
		Format:  jsonl.FormatJSON,
	})
	if err := fn(runenv.With(context.Background(), env)); err != nil {
		t.Fatalf("run: %v", err)
	}
	if err := env.Streams().Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	var records []promptRecord
	for line := range strings.SplitSeq(strings.TrimSuffix(out.String(), "\n"), "\n") {
		var r promptRecord
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatalf("stdout line %q is not a record: %v", line, err)
		}
		records = append(records, r)
	}
	return records
}

// TestSetupQuestions_jsonPrompts pins R3-03: under -json each question the
// setup wizard prints is a prompt record, a confirmation of kind confirm and
// a selection or entry of kind select, with the answers as its choices.
func TestSetupQuestions_jsonPrompts(t *testing.T) {
	if Input != nil {
		t.Fatal("Input is set; the wizard would not read the run's stdin")
	}
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "a.json")
	if err := os.WriteFile(cfgPath, []byte(`{}`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	t.Run("action", func(t *testing.T) {
		recs := jsonSetupRun(t, "c\n", func(ctx context.Context) error {
			_, err := queryForAction(ctx, []action{conf, del})
			return err
		})
		if len(recs) != 1 || recs[0].Type != "prompt" || recs[0].Kind != "select" || !slices.Equal(recs[0].Choices, []string{"[c]onfigure", "[d]el"}) {
			t.Fatalf("records = %+v, want one select prompt", recs)
		}
	})
	t.Run("copy name", func(t *testing.T) {
		recs := jsonSetupRun(t, "b\n", func(ctx context.Context) error {
			_, err := actionCopy(ctx, config{name: "a.json", filePath: cfgPath})
			return err
		})
		if len(recs) < 1 || recs[0].Type != "prompt" || recs[0].Kind != "select" || recs[0].Text != "Enter name for copy" {
			t.Fatalf("records = %+v, want the name question first", recs)
		}
	})
	t.Run("map and value", func(t *testing.T) {
		recs := jsonSetupRun(t, "a\nk\nv\nd\n", func(ctx context.Context) error {
			_, err := editMap(ctx, "env", map[string]any{}, dir)
			return err
		})
		var kinds, texts []string
		for _, r := range recs {
			if r.Type != "prompt" {
				t.Fatalf("records = %+v, want only prompts", recs)
			}
			kinds, texts = append(kinds, r.Kind), append(texts, r.Text)
		}
		if !slices.Equal(texts, []string{"Map 'env' keys: []", "New key", "Value", "Map 'env' keys: [k]"}) || slices.ContainsFunc(kinds, func(k string) bool { return k != "select" }) {
			t.Fatalf("records = %+v", recs)
		}
		if !slices.Equal(recs[0].Choices, []string{"[a]dd", "[u]pdate", "[r]emove", "[d]one"}) {
			t.Fatalf("map choices = %v", recs[0].Choices)
		}
	})
	t.Run("delete confirmation", func(t *testing.T) {
		recs := jsonSetupRun(t, "y\n", func(ctx context.Context) error {
			return actionRemove(ctx, config{name: "a.json", filePath: cfgPath})
		})
		if len(recs) < 1 || recs[0].Type != "prompt" || recs[0].Kind != "confirm" || !slices.Equal(recs[0].Choices, []string{"y", "n"}) || strings.Contains(recs[0].Text, "[y/n]") {
			t.Fatalf("records = %+v, want a confirm prompt first", recs)
		}
		if _, err := os.Stat(cfgPath); !os.IsNotExist(err) {
			t.Fatalf("the answer y did not delete the file: %v", err)
		}
	})
}
