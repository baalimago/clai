package tools

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
	"time"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// resetClaiRuns isolates the global run registry so tests do not observe each
// other's workers.
func resetClaiRuns(t *testing.T) {
	t.Helper()
	clear := func() {
		claiRunsMu.Lock()
		claiRuns = map[string]*claiProcess{}
		claiRunsMu.Unlock()
	}
	clear()
	t.Cleanup(clear)
}

// stubClaiBinary points the clai_run/clai_help tools at a script so no test
// depends on the real clai binary being on PATH.
func stubClaiBinary(t *testing.T, body string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "clai")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}
	old := ClaiBinaryPath
	ClaiBinaryPath = path
	t.Cleanup(func() { ClaiBinaryPath = old })
}

// awaitRun blocks until the tracked worker for runID reports done, then reads
// its result under the registry lock.
func awaitRun(t *testing.T, runID string) (stdout, stderr string, exitCode int, runErr error) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		claiRunsMu.Lock()
		p, ok := claiRuns[runID]
		done := ok && p.done
		claiRunsMu.Unlock()
		if !ok {
			t.Fatalf("run %q was never registered", runID)
		}
		if done {
			claiRunsMu.Lock()
			stdout, stderr = p.stdout.String(), p.stderr.String()
			exitCode, runErr = p.exitCode, p.err
			claiRunsMu.Unlock()
			return stdout, stderr, exitCode, runErr
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("run %q never completed", runID)
	return "", "", 0, nil
}

func TestClaiRunSetupFlags(t *testing.T) {
	testCases := []struct {
		name    string
		input   pub_models.Input
		want    []string
		wantErr bool
	}{
		{name: "missing args", input: pub_models.Input{}, wantErr: true},
		{name: "non-string args", input: pub_models.Input{"args": 5}, wantErr: true},
		{
			name:  "plain words gain the q subcommand and -r",
			input: pub_models.Input{"args": "hello world"},
			want:  []string{"-r", "q", "hello", "world"},
		},
		{
			name:  "an explicit q subcommand is kept",
			input: pub_models.Input{"args": "q foo"},
			want:  []string{"-r", "q", "foo"},
		},
		{
			name:  "an explicit query subcommand is kept",
			input: pub_models.Input{"args": "query foo"},
			want:  []string{"-r", "query", "foo"},
		},
		{
			name:  "a lone subcommand yields no flags",
			input: pub_models.Input{"args": "q"},
			want:  []string{},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ClaiRun.setupFlags(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("setupFlags succeeded, want an error")
				}
				return
			}
			if err != nil {
				t.Fatalf("setupFlags: %v", err)
			}
			if !slices.Equal(got, tc.want) {
				t.Fatalf("flags = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestClaiRunCall(t *testing.T) {
	t.Run("records output, exit code and reachable result", func(t *testing.T) {
		resetClaiRuns(t)
		stubClaiBinary(t, `echo "worker stdout"; echo "worker stderr" >&2`)

		runID, err := ClaiRun.Call(pub_models.Input{"args": "do the thing"})
		if err != nil {
			t.Fatalf("Call: %v", err)
		}
		stdout, stderr, exitCode, runErr := awaitRun(t, runID)
		if runErr != nil || exitCode != 0 {
			t.Fatalf("exit = %d, err = %v, want 0, nil", exitCode, runErr)
		}
		if stdout != "worker stdout\n" || stderr != "worker stderr\n" {
			t.Fatalf("stdout = %q, stderr = %q", stdout, stderr)
		}

		status, err := ClaiCheck.Call(pub_models.Input{"run_id": runID})
		if err != nil || status != "COMPLETED" {
			t.Fatalf("ClaiCheck = %q, %v; want COMPLETED, nil", status, err)
		}
		result, err := ClaiResult.Call(pub_models.Input{"run_id": runID})
		if err != nil {
			t.Fatalf("ClaiResult: %v", err)
		}
		for _, want := range []string{"Exit Code: 0", "worker stdout", "worker stderr"} {
			if !bytes.Contains([]byte(result), []byte(want)) {
				t.Errorf("result %q lacks %q", result, want)
			}
		}
	})

	t.Run("a failing subprocess is reported as FAILED", func(t *testing.T) {
		resetClaiRuns(t)
		stubClaiBinary(t, "exit 3")

		runID, err := ClaiRun.Call(pub_models.Input{"args": "q boom"})
		if err != nil {
			t.Fatalf("Call: %v", err)
		}
		_, _, exitCode, _ := awaitRun(t, runID)
		if exitCode != 3 {
			t.Fatalf("exit code = %d, want 3", exitCode)
		}
		if status, _ := ClaiCheck.Call(pub_models.Input{"run_id": runID}); status != "FAILED" {
			t.Fatalf("ClaiCheck = %q, want FAILED", status)
		}
	})

	t.Run("a missing binary is a start error", func(t *testing.T) {
		resetClaiRuns(t)
		old := ClaiBinaryPath
		ClaiBinaryPath = filepath.Join(t.TempDir(), "absent")
		t.Cleanup(func() { ClaiBinaryPath = old })

		if _, err := ClaiRun.Call(pub_models.Input{"args": "q hi"}); err == nil {
			t.Fatal("Call succeeded with a missing binary")
		}
	})

	t.Run("bad input is rejected before spawning", func(t *testing.T) {
		resetClaiRuns(t)
		if _, err := ClaiRun.Call(pub_models.Input{}); err == nil {
			t.Fatal("Call succeeded without args")
		}
	})

	t.Run("worker logs land in temp files", func(t *testing.T) {
		resetClaiRuns(t)
		stubClaiBinary(t, `echo "logged"`)
		runID, err := ClaiRun.Call(pub_models.Input{"args": "q log"})
		if err != nil {
			t.Fatalf("Call: %v", err)
		}
		awaitRun(t, runID)

		logPath := filepath.Join(os.TempDir(), "clai-worker-"+runID+"-stdout.log")
		t.Cleanup(func() { _ = os.Remove(logPath) })
		raw, err := os.ReadFile(logPath)
		if err != nil {
			t.Fatalf("read worker log: %v", err)
		}
		if !bytes.Contains(raw, []byte("logged")) {
			t.Fatalf("worker log = %q, want the subprocess stdout", raw)
		}
	})
}

func TestCreateTempOutputFilesFailsWhenTempDirIsUnusable(t *testing.T) {
	t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "does-not-exist"))
	if _, _, err := createTempOutputFiles("deadbeef"); err == nil {
		t.Fatal("createTempOutputFiles succeeded with an unusable TMPDIR")
	}
}

func TestWaitForProcessCompletionNonExitError(t *testing.T) {
	resetClaiRuns(t)
	process := &claiProcess{cmd: exec.Command("true"), stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}}
	stdoutFile := createTempFile(t)
	stderrFile := createTempFile(t)

	ClaiRun.waitForProcessCompletion(process, stdoutFile, stderrFile)

	if !process.done {
		t.Error("process not marked done")
	}
	if process.err == nil || process.exitCode != -1 {
		t.Errorf("err = %v, exitCode = %d; want a non-exit error and -1", process.err, process.exitCode)
	}
}

func createTempFile(t *testing.T) *os.File {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "log")
	if err != nil {
		t.Fatalf("temp file: %v", err)
	}
	return f
}

func TestClaiCheckStates(t *testing.T) {
	testCases := []struct {
		name    string
		process *claiProcess
		want    string
	}{
		{name: "running", process: &claiProcess{done: false, exitCode: 0}, want: "RUNNING"},
		{name: "completed", process: &claiProcess{done: true, exitCode: 0}, want: "COMPLETED"},
		{name: "failed exit code", process: &claiProcess{done: true, exitCode: 2}, want: "FAILED"},
		{name: "failed wait error", process: &claiProcess{done: true, exitCode: 0, err: errors.New("boom")}, want: "FAILED"},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			resetClaiRuns(t)
			claiRunsMu.Lock()
			claiRuns["run-1"] = tc.process
			claiRunsMu.Unlock()

			got, err := ClaiCheck.Call(pub_models.Input{"run_id": "run-1"})
			if err != nil {
				t.Fatalf("ClaiCheck: %v", err)
			}
			if got != tc.want {
				t.Fatalf("status = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestClaiRunIDValidation(t *testing.T) {
	resetClaiRuns(t)
	testCases := []struct {
		name  string
		input pub_models.Input
	}{
		{name: "missing run_id", input: pub_models.Input{}},
		{name: "non-string run_id", input: pub_models.Input{"run_id": 7}},
		{name: "unknown run_id", input: pub_models.Input{"run_id": "ghost"}},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ClaiCheck.Call(tc.input); err == nil {
				t.Errorf("ClaiCheck accepted %s", tc.name)
			}
			if _, err := ClaiResult.Call(tc.input); err == nil {
				t.Errorf("ClaiResult accepted %s", tc.name)
			}
		})
	}
}

func TestClaiHelpCall(t *testing.T) {
	t.Run("returns the subprocess output", func(t *testing.T) {
		stubClaiBinary(t, `echo "usage text"`)
		out, err := ClaiHelp.Call(pub_models.Input{})
		if err != nil {
			t.Fatalf("ClaiHelp: %v", err)
		}
		if !bytes.Contains([]byte(out), []byte("usage text")) {
			t.Fatalf("output = %q, want the stub's stdout", out)
		}
	})

	t.Run("a failing subprocess is an error that keeps the output", func(t *testing.T) {
		stubClaiBinary(t, `echo "partial"; exit 1`)
		out, err := ClaiHelp.Call(pub_models.Input{})
		if err == nil {
			t.Fatal("ClaiHelp succeeded on a failing subprocess")
		}
		if !bytes.Contains([]byte(out), []byte("partial")) {
			t.Fatalf("output = %q, want the captured partial output", out)
		}
	})
}

func TestInternalOnlyToolsRejectDirectCalls(t *testing.T) {
	testCases := []struct {
		name string
		tool pub_models.LLMTool
	}{
		{name: "load_skill", tool: LoadSkill},
		{name: "search_conversations", tool: SearchConversations},
		{name: "inspect_conversation", tool: InspectConversation},
		{name: "read_message", tool: ReadMessage},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := tc.tool.Call(pub_models.Input{}); err == nil {
				t.Fatalf("%s accepted a direct call, want an internal-only error", tc.name)
			}
		})
	}
}
