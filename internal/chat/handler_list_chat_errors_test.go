package chat

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/jsonl"
	"github.com/baalimago/clai/internal/runenv"
	"github.com/baalimago/clai/internal/utils"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// failWriteAt fails every Write once the first remaining successful writes are
// spent. It lets one table walk every wrapped-write error branch of a renderer.
type failWriteAt struct{ remaining int }

func (w *failWriteAt) Write(p []byte) (int, error) {
	if w.remaining <= 0 {
		return 0, errors.New("synthetic write failure")
	}
	w.remaining--
	return len(p), nil
}

func TestPrintChatInfoCommonPropagatesEveryWriteFailure(t *testing.T) {
	confDir := t.TempDir()
	if err := utils.CreateConfigDir(confDir); err != nil {
		t.Fatalf("CreateConfigDir: %v", err)
	}
	t.Setenv("CLAI_CONFIG_DIR", confDir)

	created := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	msgs := []pub_models.Message{
		{Role: "user", Content: "hi"},
		{Role: "tool", Content: "t"},
		{Role: "system", Content: "s"},
		{Role: "assistant", Content: "a"},
	}
	cases := []struct {
		name    string
		chat    pub_models.Chat
		foreign bool
	}{
		{"native", pub_models.Chat{ID: "native", Created: created, Messages: msgs}, false},
		{"native group key", pub_models.Chat{ID: "grouped", Created: created, GroupKey: "g", Messages: msgs}, false},
		{"foreign", pub_models.Chat{ID: "", Source: "anthropic", SourceID: "sess", Created: created, Messages: msgs}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cq := &ChatHandler{}
			seen := map[string]struct{}{}
			for k := 0; ; k++ {
				if k > 64 {
					t.Fatalf("write budget exceeded; renderer never completed")
				}
				w := &failWriteAt{remaining: k}
				var err error
				if tc.foreign {
					err = cq.printChatInfoForeign(w, tc.chat, "")
				} else {
					err = cq.printChatInfo(w, tc.chat, "")
				}
				if err == nil {
					break
				}
				seen[err.Error()] = struct{}{}
			}
			if len(seen) < 14 {
				t.Fatalf("expected at least 14 distinct write errors, got %d: %v", len(seen), seen)
			}
		})
	}
}

func TestEditorEditString(t *testing.T) {
	t.Run("EDITOR unset", func(t *testing.T) {
		t.Setenv("EDITOR", "")
		if _, err := editorEditString(context.Background(), "x"); err == nil {
			t.Fatal("expected error when EDITOR is not set")
		}
	})

	t.Run("editor rewrites the temp file", func(t *testing.T) {
		script := filepath.Join(t.TempDir(), "editor.sh")
		if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf 'edited\\n' > \"$1\"\n"), 0o755); err != nil {
			t.Fatalf("WriteFile editor: %v", err)
		}
		t.Setenv("EDITOR", script)
		got, err := editorEditString(context.Background(), "original")
		if err != nil {
			t.Fatalf("editorEditString: %v", err)
		}
		if got != "edited\n" {
			t.Fatalf("edited content = %q, want %q", got, "edited\n")
		}
	})

	t.Run("editor exits non-zero", func(t *testing.T) {
		t.Setenv("EDITOR", "false")
		if _, err := editorEditString(context.Background(), "x"); err == nil {
			t.Fatal("expected error when the editor command fails")
		}
	})

	t.Run("-json refuses before the editor starts", func(t *testing.T) {
		marker := filepath.Join(t.TempDir(), "started")
		script := filepath.Join(t.TempDir(), "editor.sh")
		if err := os.WriteFile(script, []byte("#!/bin/sh\ntouch \""+marker+"\"\n"), 0o755); err != nil {
			t.Fatalf("WriteFile editor: %v", err)
		}
		t.Setenv("EDITOR", script)
		ctx := runenv.With(context.Background(), runenv.New(runenv.Config{Format: jsonl.FormatJSON}))
		_, err := editorEditString(ctx, "x")
		var refused runenv.EditorRefusedError
		if !errors.As(err, &refused) || refused.Path != "" {
			t.Fatalf("err = %v, want the editor refusal without a path", err)
		}
		if _, statErr := os.Stat(marker); !os.IsNotExist(statErr) {
			t.Fatalf("the editor started under -json, stat err = %v", statErr)
		}
	})
}
