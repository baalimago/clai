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
	resetClaiRuns(t)
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
	resetClaiRuns(t)
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
	if !strings.Contains(out, "output1") || !strings.Contains(out, "output2") {
		t.Fatalf("expected both replies, got: %q", out)
	}
	if strings.Contains(out, "err1") {
		t.Fatalf("a completed worker's stderr is its transcript and must stay in its log, got: %q", out)
	}
	if !strings.Contains(out, "err2") {
		t.Fatalf("a failed worker's stderr explains the failure, got: %q", out)
	}
}

// TestClaiWaitForWorkers_ReportsEachWorkerOnce pins that a second wait, e.g.
// after a verification round, does not repeat workers it already returned.
func TestClaiWaitForWorkers_ReportsEachWorkerOnce(t *testing.T) {
	resetClaiRuns(t)
	claiRunsMu.Lock()
	claiRuns = map[string]*claiProcess{
		"first": {cmd: &exec.Cmd{}, stdout: bytes.NewBufferString("first reply"), stderr: &bytes.Buffer{}, done: true},
	}
	claiRunsMu.Unlock()
	tool := &claiWaitForWorkersTool{}

	if _, err := tool.Call(map[string]any{"timeout_seconds": 1}); err != nil {
		t.Fatalf("first wait: %v", err)
	}
	claiRunsMu.Lock()
	claiRuns["second"] = &claiProcess{cmd: &exec.Cmd{}, stdout: bytes.NewBufferString("second reply"), stderr: &bytes.Buffer{}, done: true}
	claiRunsMu.Unlock()

	out, err := tool.Call(map[string]any{"timeout_seconds": 1})
	if err != nil {
		t.Fatalf("second wait: %v", err)
	}
	if strings.Contains(out, "first reply") || !strings.Contains(out, "second reply") {
		t.Fatalf("second wait must return only the new worker, got: %q", out)
	}

	out, err = tool.Call(map[string]any{"timeout_seconds": 1})
	if err != nil {
		t.Fatalf("third wait: %v", err)
	}
	if !strings.Contains(out, "No active workers") {
		t.Fatalf("expected no workers left, got: %q", out)
	}
}

// TestClaiWaitForWorkers_Timeout pins that a timeout interrupts the workers
// still running and still returns the replies of those that finished.
func TestClaiWaitForWorkers_Timeout(t *testing.T) {
	resetClaiRuns(t)
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
	go ClaiRun.waitForProcessCompletion(process, createTempFile(t), createTempFile(t))

	claiRunsMu.Lock()
	claiRuns = map[string]*claiProcess{
		"slow": process,
		"fast": {cmd: &exec.Cmd{}, stdout: bytes.NewBufferString("fast reply"), stderr: &bytes.Buffer{}, done: true},
	}
	claiRunsMu.Unlock()

	tool := &claiWaitForWorkersTool{}

	start := time.Now()
	out, err := tool.Call(map[string]any{"timeout_seconds": 0.5})
	if err != nil {
		t.Fatalf("a timeout must still return the finished workers, got err: %v", err)
	}
	dur := time.Since(start)
	if dur < 500*time.Millisecond {
		t.Fatalf("expected to wait at least 500ms, waited %v", dur)
	}
	if !strings.Contains(out, "fast reply") {
		t.Fatalf("expected the finished worker's reply, got: %q", out)
	}
	if !strings.Contains(out, "slow") || !strings.Contains(out, "TIMED OUT") {
		t.Fatalf("expected the running worker to be reported as timed out, got: %q", out)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		claiRunsMu.Lock()
		done := process.done
		claiRunsMu.Unlock()
		if done {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the timed-out worker was not interrupted")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
