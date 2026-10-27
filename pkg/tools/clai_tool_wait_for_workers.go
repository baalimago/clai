package tools

import (
	"bytes"
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"time"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// ClaiWaitForWorkers waits for the clai workers no earlier wait returned. On
// timeout it interrupts the ones still running and still returns the replies
// of those that finished.
var ClaiWaitForWorkers = &claiWaitForWorkersTool{}

type claiWaitForWorkersTool struct{}

func (t *claiWaitForWorkersTool) Specification() pub_models.Specification {
	return pub_models.Specification{
		Name:        "clai_wait_for_workers",
		Description: "Wait for the clai workers started since the last wait to finish, and return each one's reply (its stdout) once. Takes a timeout in seconds. On timeout, interrupts the workers still running and reports them as TIMED OUT, still returning the replies of those that finished. A failed worker also shows the end of its stderr. Each worker's full log, including its tool-call transcript, is written to disk and its path returned.",
		Inputs: &pub_models.InputSchema{
			Type:     "object",
			Required: []string{"timeout_seconds"},
			Properties: map[string]pub_models.ParameterObject{
				"timeout_seconds": {
					Type:        "number",
					Description: "Maximum time to wait in seconds before timing out.",
				},
			},
		},
	}
}

func (t *claiWaitForWorkersTool) Call(input pub_models.Input) (string, error) {
	start := time.Now()

	rawTimeout, ok := input["timeout_seconds"]
	if !ok {
		return "", fmt.Errorf("missing timeout_seconds")
	}

	var timeoutSeconds float64
	switch v := rawTimeout.(type) {
	case int:
		timeoutSeconds = float64(v)
	case int32:
		timeoutSeconds = float64(v)
	case int64:
		timeoutSeconds = float64(v)
	case float32:
		timeoutSeconds = float64(v)
	case float64:
		timeoutSeconds = v
	case string:
		// Best-effort parsing
		var parsed float64
		_, err := fmt.Sscanf(v, "%f", &parsed)
		if err != nil {
			return "", fmt.Errorf("failed to parse timeout_seconds '%v' as number: %w", v, err)
		}
		timeoutSeconds = parsed
	default:
		return "", fmt.Errorf("unsupported type for timeout_seconds: %T", rawTimeout)
	}

	if timeoutSeconds <= 0 {
		return "", fmt.Errorf("timeout_seconds must be > 0, got %v", timeoutSeconds)
	}

	// Snapshot the runs no earlier wait has returned
	claiRunsMu.Lock()
	localRuns := make(map[string]*claiProcess, len(claiRuns))
	for id, p := range claiRuns {
		if !p.collected {
			localRuns[id] = p
		}
	}
	claiRunsMu.Unlock()

	if len(localRuns) == 0 {
		elapsed := time.Since(start)
		return fmt.Sprintf("No active workers (waited %s)", elapsed), nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutSeconds*float64(time.Second)))
	defer cancel()

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	timedOut := false
	for !timedOut {
		allDone := true

		claiRunsMu.Lock()
		for _, p := range localRuns {
			if !p.done {
				allDone = false
				break
			}
		}
		claiRunsMu.Unlock()

		if allDone {
			break
		}

		select {
		case <-ctx.Done():
			timedOut = true
		case <-ticker.C:
		}
	}

	elapsed := time.Since(start)

	// Build aggregated result and mirror each worker's output to a temp file.
	claiRunsMu.Lock()
	defer claiRunsMu.Unlock()

	var buf bytes.Buffer
	if timedOut {
		fmt.Fprintf(&buf, "Timed out after %s (timeout %v seconds); interrupted the workers still running\n\n", elapsed, timeoutSeconds)
	} else {
		fmt.Fprintf(&buf, "All workers completed in %s\n\n", elapsed)
	}

	for _, id := range slices.Sorted(maps.Keys(localRuns)) {
		p := localRuns[id]
		if !p.done {
			if p.cmd != nil && p.cmd.Process != nil {
				_ = p.cmd.Process.Signal(os.Interrupt)
			}
			p.collected = true
			// Its output is still being written, so point at the log
			// instead of reading the live buffer.
			fmt.Fprintf(&buf, "Worker %s:\nStatus: TIMED OUT (interrupted)\nPartial stdout log: %s\n\n", id, workerLogPath(id, "stdout"))
			continue
		}
		p.collected = true
		status := "COMPLETED"
		if p.exitCode != 0 || p.err != nil {
			status = "FAILED"
		}

		// Create a temp file per worker and dump stdout/stderr there.
		// We include the worker id in the pattern for easier inspection.
		pattern := fmt.Sprintf("clai-worker-%s-*.log", id)
		f, err := os.CreateTemp(os.TempDir(), pattern)
		if err != nil {
			fmt.Fprintf(&buf, "Worker %s: failed to create temp file: %v\n", id, err)
		} else {
			// Best-effort write; if it fails, we still show what we can in-memory.
			_, _ = fmt.Fprintf(f, "Exit Code: %d\nStatus: %s\n", p.exitCode, status)
			if p.err != nil {
				_, _ = fmt.Fprintf(f, "Error: %v\n", p.err)
			}
			_, _ = fmt.Fprintf(f, "Stdout:\n%s\n", p.stdout.String())
			_, _ = fmt.Fprintf(f, "Stderr:\n%s\n", p.stderr.String())
			_ = f.Close()

			fmt.Fprintf(&buf, "Worker %s log (stdout and the stderr transcript) written to: %s\n", id, filepath.Clean(f.Name()))
		}

		fmt.Fprintf(&buf, "Worker %s:\n", id)
		fmt.Fprintf(&buf, "Status: %s (exit code: %d)\n", status, p.exitCode)
		if p.err != nil {
			fmt.Fprintf(&buf, "Error: %v\n", p.err)
		}
		fmt.Fprintf(&buf, "Stdout:\n%s\n", p.stdout.String())
		// A completed worker's stderr is its tool transcript, kept in the log;
		// a failed worker's stderr says why it failed.
		if status == "FAILED" {
			fmt.Fprintf(&buf, "Stderr (last %d bytes):\n%s\n", failedStderrTail, tail(p.stderr.String(), failedStderrTail))
		}
		fmt.Fprintln(&buf)
	}

	return buf.String(), nil
}

// failedStderrTail bounds how much of a failed worker's stderr reaches the
// parent; the full stream stays in the worker's log.
const failedStderrTail = 2000

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
