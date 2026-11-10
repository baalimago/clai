package agent

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	priv_models "github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/runenv"
	"github.com/baalimago/clai/internal/text"
	"github.com/baalimago/clai/pkg/diagnostic"
	"github.com/baalimago/clai/pkg/text/models"
)

type envQuerier struct {
	priv_models.ChatQuerier
	seen *[]runenv.Env
}

func (q envQuerier) TextQuery(ctx context.Context, chat models.Chat) (models.Chat, error) {
	*q.seen = append(*q.seen, runenv.From(ctx))
	return models.Chat{Messages: append(chat.Messages, models.Message{Role: "assistant", Content: "ok"})}, nil
}

// TestAgentRunEnv pins R3-07: a run whose caller supplies no run environment
// reads the process input with its terminal state, so audio_record stops on
// Enter as it does in the CLI, and stays silent (F3); a caller's own
// environment is kept.
func TestAgentRunEnv(t *testing.T) {
	newAgent := func(seen *[]runenv.Env) Agent {
		a := New(WithPrompt("hi"))
		a.cfgDir = t.TempDir()
		a.stdin, a.stdinIsTerminal = strings.NewReader("\n"), true
		a.querierCreator = func(ctx context.Context, _ text.Configurations) (priv_models.Querier, error) {
			*seen = append(*seen, runenv.From(ctx))
			return envQuerier{seen: seen}, nil
		}
		return a
	}

	t.Run("without a caller environment", func(t *testing.T) {
		var seen []runenv.Env
		a := newAgent(&seen)
		if err := a.Setup(context.Background()); err != nil {
			t.Fatalf("Setup: %v", err)
		}
		if _, err := a.Run(context.Background()); err != nil {
			t.Fatalf("Run: %v", err)
		}
		if len(seen) != 2 {
			t.Fatalf("saw %d environments, want setup and query", len(seen))
		}
		for _, env := range seen {
			if !env.StdinIsTerminal() {
				t.Fatal("the run does not see the process input's terminal state")
			}
			if env.Enabled("", diagnostic.LevelError) || env.JSON() {
				t.Fatal("the attached environment reports diagnostics or encodes JSON; the library must stay silent")
			}
			if n, _ := io.WriteString(env.Stdout(), "x"); n != 1 {
				t.Fatal("stdout of the attached environment is not a silent sink")
			}
		}
		if line, err := io.ReadAll(seen[1].Stdin()); err != nil || string(line) != "\n" {
			t.Fatalf("the query reads %q, %v; want the process input", line, err)
		}
	})

	t.Run("a caller environment is kept", func(t *testing.T) {
		var seen []runenv.Env
		a := newAgent(&seen)
		var out bytes.Buffer
		ctx := runenv.With(context.Background(), runenv.Text(strings.NewReader("mine"), &out, io.Discard))
		if err := a.Setup(ctx); err != nil {
			t.Fatalf("Setup: %v", err)
		}
		if _, err := a.Run(ctx); err != nil {
			t.Fatalf("Run: %v", err)
		}
		for _, env := range seen {
			if env.StdinIsTerminal() {
				t.Fatal("the caller's environment was replaced")
			}
		}
		if got, _ := io.ReadAll(seen[0].Stdin()); string(got) != "mine" {
			t.Fatalf("stdin = %q, want the caller's", got)
		}
	})
}
