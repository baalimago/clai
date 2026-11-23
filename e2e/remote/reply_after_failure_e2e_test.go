package remote

import (
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
)

// A fresh query whose run failed before any answer is still saved, and -re
// retries with its prompt (worklog 2026-11-17-previous-query-pointer, D9).
func TestReply_RetriesTheFailedPrompt(t *testing.T) {
	var mu sync.Mutex
	var bodies []string
	stubProvider(t, func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(b))
		n := len(bodies)
		mu.Unlock()
		if n == 1 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"bad request"}}`))
			return
		}
		writeStubReply(w)
	})

	_, stderr, status := runStepRetry(t, "-cm gpt-stub -r q the-failed-prompt")
	if status == 0 {
		t.Fatalf("first query status = 0, want a failure; stderr = %q", stderr)
	}
	stdout, stderr, status := runStepRetry(t, "-cm gpt-stub -r -re q and-more")
	if status != 0 {
		t.Fatalf("-re status = %d, stderr = %q", status, stderr)
	}
	if got := strings.TrimSpace(stdout); got != "THE-REPLY" {
		t.Fatalf("stdout = %q, want the reply", stdout)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 2 {
		t.Fatalf("requests = %d, want 2", len(bodies))
	}
	if !strings.Contains(bodies[1], "the-failed-prompt") || !strings.Contains(bodies[1], "and-more") {
		t.Fatalf("-re request = %s, want it to carry the failed prompt and the new one", bodies[1])
	}
}
