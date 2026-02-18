package tools

import (
	"bytes"
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"time"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// ClaiWaitForWorkers waits for all current clai workers to finish.
// On timeout, it sends an interrupt signal to all running subprocesses.
// On success, it returns a tool response containing the output for each worker.
var ClaiWaitForWorkers = &claiWaitForWorkersTool{}

type claiWaitForWorkersTool struct{}

func (t *claiWaitForWorkersTool) Specification() pub_models.Specification {
	return pub_models.Specification{
		Name:        "clai_wait_for_workers",
		Description: "Wait for all current clai workers to finish. Takes a timeout in seconds. On timeout, cancels ongoing subprocesses by sending interrupt signal. On success, returns output for each worker, how long it took, and where their outputs are stored on disk.",
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

	// Snapshot current runs
	claiRunsMu.Lock()
	localRuns := make(map[string]*claiProcess, len(claiRuns))
	maps.Copy(localRuns, claiRuns)
	claiRunsMu.Unlock()

	if len(localRuns) == 0 {
		elapsed := time.Since(start)
		return fmt.Sprintf("No active workers (waited %s)", elapsed), nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutSeconds*float64(time.Second)))
	defer cancel()

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
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
			// Timeout: send interrupt to all still-running processes
			claiRunsMu.Lock()
			for _, p := range localRuns {
				if !p.done && p.cmd != nil && p.cmd.Process != nil {
					_ = p.cmd.Process.Signal(os.Interrupt)
				}
			}
			claiRunsMu.Unlock()
			elapsed := time.Since(start)
			return "", fmt.Errorf("timeout waiting for workers (%v seconds, elapsed %s); sent interrupt to running processes", timeoutSeconds, elapsed)
		case <-ticker.C:
		}
	}

	elapsed := time.Since(start)

	// Build aggregated result and mirror each worker's output to a temp file.
	claiRunsMu.Lock()
	defer claiRunsMu.Unlock()

	var buf bytes.Buffer
	fmt.Fprintf(&buf, "All workers completed in %s\n\n", elapsed)

	for id, p := range localRuns {
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

			fmt.Fprintf(&buf, "Worker %s log written to: %s\n", id, filepath.Clean(f.Name()))
		}

		fmt.Fprintf(&buf, "Worker %s:\n", id)
		fmt.Fprintf(&buf, "Status: %s (exit code: %d)\n", status, p.exitCode)
		if p.err != nil {
			fmt.Fprintf(&buf, "Error: %v\n", p.err)
		}
		fmt.Fprintf(&buf, "Stdout:\n%s\n", p.stdout.String())
		fmt.Fprintf(&buf, "Stderr:\n%s\n", p.stderr.String())
		fmt.Fprintln(&buf)
	}

	return buf.String(), nil
}
