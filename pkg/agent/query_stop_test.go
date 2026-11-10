package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/baalimago/clai/pkg/text/models"
)

// cancellingTool cancels the query's context when the model calls it.
type cancellingTool struct{ cancel context.CancelFunc }

func (c *cancellingTool) Call(models.Input) (string, error) {
	c.cancel()
	return "the tool ran", nil
}

func (c *cancellingTool) Specification() models.Specification {
	return models.Specification{Name: "stop_now", Description: "stops the run", Inputs: &models.InputSchema{Type: "object", Required: []string{}}}
}

// stopAgent is an agent whose model is served by handler.
func stopAgent(t *testing.T, handler http.HandlerFunc, opts ...Option) (Agent, string) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		handler(w, r)
	}))
	t.Cleanup(server.Close)
	t.Setenv("OPENAI_API_KEY", "stub-key")
	cfgDir := filepath.Join(t.TempDir(), "clai")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatalf("create config dir: %v", err)
	}
	conf, err := json.Marshal(map[string]any{"model": "gpt-stop", "url": server.URL + "/v1/chat/completions"})
	if err != nil {
		t.Fatalf("marshal model config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "openai_gpt_gpt-stop.json"), conf, 0o644); err != nil {
		t.Fatalf("write model config: %v", err)
	}
	a := New(append([]Option{WithConfigDir(cfgDir), WithModel("gpt-stop")}, opts...)...)
	if err := a.Setup(t.Context()); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	return a, cfgDir
}

func savedConversations(t *testing.T, cfgDir string) string {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(cfgDir, "conversations", "*.json"))
	if err != nil {
		t.Fatalf("glob conversations: %v", err)
	}
	var all strings.Builder
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		all.Write(raw)
	}
	return all.String()
}

// TestQueryStoppedByItsContext pins that a Query whose context is cancelled
// or past its deadline returns an error matching the context's, never a nil
// error with a truncated reply, and that what arrived is persisted.
func TestQueryStoppedByItsContext(t *testing.T) {
	t.Run("cancelled during a tool call", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		a, cfgDir := stopAgent(t, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\"stop_now\",\"arguments\":\"{}\"}}]}}]}\n\n"+
				"data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n")
		}, WithTools([]models.LLMTool{&cancellingTool{cancel: cancel}}))
		_, err := a.Query(ctx, models.Chat{ID: "stopped-by-cancel", Messages: []models.Message{{Role: "user", Content: "go"}}})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Query returned %v, want an error matching context.Canceled", err)
		}
		if strings.Contains(err.Error(), "transport failure") {
			t.Fatalf("the stop is reported as a transport failure: %v", err)
		}
		if saved := savedConversations(t, cfgDir); !strings.Contains(saved, "the tool ran") {
			t.Fatalf("the tool result that arrived before the stop is not persisted: %s", saved)
		}
	})
	t.Run("past its deadline", func(t *testing.T) {
		a, _ := stopAgent(t, func(_ http.ResponseWriter, r *http.Request) {
			<-r.Context().Done()
		})
		ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
		defer cancel()
		_, err := a.Query(ctx, models.Chat{ID: "stopped-by-deadline", Messages: []models.Message{{Role: "user", Content: "go"}}})
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Query returned %v, want an error matching context.DeadlineExceeded", err)
		}
	})
}
