package remote

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/baalimago/clai/e2e/e2etest"
)

const stepRetryCrashBody = "upstream connect error or disconnect/reset before headers. reset reason: connection timeout"

// stepRetryProvider answers 503 to the first failures requests, then a reply.
// It seeds a config dir whose model gpt-stub talks to it.
func stepRetryProvider(t *testing.T, failures int32) (string, *atomic.Int32) {
	t.Helper()
	original := http.DefaultTransport
	http.DefaultTransport = e2etest.LoopbackOnly{Base: original}
	t.Cleanup(func() { http.DefaultTransport = original })

	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if requests.Add(1) <= failures {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(stepRetryCrashBody))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"THE-REPLY\"}}]}\n\ndata: [DONE]\n\n")
	}))
	t.Cleanup(server.Close)

	confDir := e2etest.SetupMainTestConfigDir(t)
	t.Setenv("OPENAI_API_KEY", "stub-key")
	conf, err := json.Marshal(map[string]any{"model": "gpt-stub", "url": server.URL + "/v1/chat/completions"})
	if err != nil {
		t.Fatalf("marshal model config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(confDir, "openai_gpt_gpt-stub.json"), conf, 0o644); err != nil {
		t.Fatalf("write model config: %v", err)
	}
	return confDir, &requests
}

func recordStepRetryWaits(t *testing.T) *[]time.Duration {
	t.Helper()
	var waits []time.Duration
	original := harness.Deps.StepRetry.Wait
	harness.Deps.StepRetry.Wait = func(_ context.Context, d time.Duration) error {
		waits = append(waits, d)
		return nil
	}
	t.Cleanup(func() { harness.Deps.StepRetry.Wait = original })
	return &waits
}

func runStepRetry(t *testing.T, args string) (string, string, int) {
	t.Helper()
	var status int
	stdout, stderr := e2etest.CaptureStdoutStderr(t, func() {
		status = harness.Run(strings.Split(args, " "))
	})
	return stdout, stderr, status
}

// TestStepRetry_WorkerReplyStaysClean pins why a retry exists for fleets: a
// -reply-only worker hands its lead stdout, so a retried 503 must leave nothing
// there but the reply.
func TestStepRetry_WorkerReplyStaysClean(t *testing.T) {
	_, requests := stepRetryProvider(t, 1)
	waits := recordStepRetryWaits(t)

	stdout, stderr, status := runStepRetry(t, "-cm gpt-stub -r -reply-only q hi")

	if status != 0 {
		t.Fatalf("status = %d, stderr = %q", status, stderr)
	}
	if got := strings.TrimSpace(stdout); got != "THE-REPLY" {
		t.Fatalf("stdout = %q, want the reply alone", stdout)
	}
	for _, want := range []string{"retry 1/4 in 1s", "gpt-stub", "provider unavailable (503)"} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("stderr = %q, want %q", stderr, want)
		}
	}
	if requests.Load() != 2 {
		t.Fatalf("requests = %d, want 2", requests.Load())
	}
	if !slices.Equal(*waits, []time.Duration{time.Second}) {
		t.Fatalf("waits = %v, want [1s]", *waits)
	}
}

// TestStepRetry_LoopSurvivesThe503ThatEndedIt is the crash this exists for:
// one 503 ended a -loop run.
func TestStepRetry_LoopSurvivesThe503ThatEndedIt(t *testing.T) {
	_, requests := stepRetryProvider(t, 1)

	_, stderr, _ := runStepRetry(t, "-cm gpt-stub -r -loop -max-iterations 1 q hi")

	if strings.Contains(stderr, "provider unavailable, status: 503") {
		t.Fatalf("the loop ended on the 503: %q", stderr)
	}
	if !strings.Contains(stderr, "retry 1/4 in 1s") {
		t.Fatalf("stderr = %q, want a retry", stderr)
	}
	if requests.Load() < 2 {
		t.Fatalf("requests = %d, want the failed request resent", requests.Load())
	}
}

func TestStepRetry_GivesUpOnTheAgreedSchedule(t *testing.T) {
	_, requests := stepRetryProvider(t, 1000)
	waits := recordStepRetryWaits(t)

	_, stderr, status := runStepRetry(t, "-cm gpt-stub -r q hi")

	if status == 0 {
		t.Fatalf("status = 0, want a failure; stderr = %q", stderr)
	}
	if requests.Load() != 5 {
		t.Fatalf("requests = %d, want 5", requests.Load())
	}
	want := []time.Duration{time.Second, 10 * time.Second, 30 * time.Second, 60 * time.Second}
	if !slices.Equal(*waits, want) {
		t.Fatalf("waits = %v, want %v", *waits, want)
	}
}

// TestStepRetry_AliasStillFallsBackWhenItGivesUp pins the order: retries first,
// then the next model, because the failed run produced nothing.
func TestStepRetry_AliasStillFallsBackWhenItGivesUp(t *testing.T) {
	confDir, requests := stepRetryProvider(t, 1000)
	writeTextConfig(t, confDir, "strong", map[string][]string{"strong": {"gpt-stub", "test"}})

	_, stderr, status := runStepRetry(t, "-r q hi")

	if status != 0 {
		t.Fatalf("status = %d, stderr = %q", status, stderr)
	}
	if requests.Load() != 5 {
		t.Fatalf("requests = %d, want 5 before the next model", requests.Load())
	}
	if notice := fallbackNotice(t, stderr); !strings.Contains(notice, "gpt-stub") {
		t.Fatalf("notice = %q, want it to name gpt-stub", notice)
	}
}
