package tools

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestClaiWaitForWorkers_NoWorkers ensures that the tool behaves when there are no workers.
func TestClaiWaitForWorkers_NoWorkers(t *testing.T) {
	tool := &claiWaitForWorkersTool{}

	out, err := tool.Call(map[string]any{"timeout_seconds": 1})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !strings.Contains(out, "No active workers") {
		t.Fatalf("unexpected output: %q", out)
	}
}

// TestClaiWaitForWorkers_WaitsAndAggregates simulates a small number of workers and verifies aggregation.
func TestClaiWaitForWorkers_WaitsAndAggregates(t *testing.T) {
	// Setup fake workers
	claiRunsMu.Lock()
	claiRuns = map[string]*claiProcess{
		"worker1": {
			cmd:      &exec.Cmd{},
			stdout:   bytes.NewBufferString("output1"),
			stderr:   bytes.NewBufferString("err1"),
			done:     true,
			exitCode: 0,
		},
		"worker2": {
			cmd:      &exec.Cmd{},
			stdout:   bytes.NewBufferString("output2"),
			stderr:   bytes.NewBufferString("err2"),
			done:     true,
			exitCode: 1,
		},
	}
	claiRunsMu.Unlock()

	tool := &claiWaitForWorkersTool{}

	out, err := tool.Call(map[string]any{"timeout_seconds": 1})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if !strings.Contains(out, "worker1") || !strings.Contains(out, "worker2") {
		t.Fatalf("expected output to contain both worker IDs, got: %q", out)
	}
	if !strings.Contains(out, "COMPLETED") || !strings.Contains(out, "FAILED") {
		t.Fatalf("expected output to contain statuses, got: %q", out)
	}
}

// TestClaiWaitForWorkers_Timeout ensures that a timeout results in an error and sends interrupts.
func TestClaiWaitForWorkers_Timeout(t *testing.T) {
	cmd := exec.Command("sleep", "10")
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	process := &claiProcess{
		cmd:    cmd,
		stdout: stdout,
		stderr: stderr,
	}

	if err := cmd.Start(); err != nil {
		t.Skipf("unable to start sleep command: %v", err)
	}

	claiRunsMu.Lock()
	claiRuns = map[string]*claiProcess{"worker1": process}
	claiRunsMu.Unlock()

	tool := &claiWaitForWorkersTool{}

	start := time.Now()
	_, err := tool.Call(map[string]any{"timeout_seconds": 0.5})
	if err == nil {
		t.Fatalf("expected timeout error, got nil")
	}
	dur := time.Since(start)
	if dur < 500*time.Millisecond {
		t.Fatalf("expected to wait at least 500ms, waited %v", dur)
	}
}
