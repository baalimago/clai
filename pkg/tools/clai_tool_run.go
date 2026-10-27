package tools

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"unicode"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

var (
	claiRunsMu sync.Mutex
	claiRuns   = make(map[string]*claiProcess)
	// Since the user called clai, we assume it's on path. If not, a bit of trouble
	ClaiBinaryPath = "clai"
)

type claiProcess struct {
	cmd    *exec.Cmd
	stdout *bytes.Buffer
	stderr *bytes.Buffer
	done   bool
	// collected marks a worker clai_wait_for_workers has already returned.
	collected bool
	exitCode  int
	err       error
}

func generateRunID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// ClaiRun - Spawn clai subprocess
var ClaiRun = &claiRunTool{}

type claiRunTool struct{}

func (t *claiRunTool) Specification() pub_models.Specification {
	return pub_models.Specification{
		Name:        "clai_run",
		Description: "Run clai as a worker. Spawns a non-blocking sub-process with an associated run-id; collect its reply with clai_wait_for_workers. The worker sees nothing of this conversation, so give it a self-contained brief. Its stdout holds only its reply; its tool calls go to its log.",
		Inputs: &pub_models.InputSchema{
			Type:     "object",
			Required: []string{"args"},
			Properties: map[string]pub_models.ParameterObject{
				"args": {
					Type:        "string",
					Description: "Arguments to pass to clai: flags, then q, then the prompt, e.g. `-cm gpt-5 -t rg,cat q <brief>`. Everything after q is passed verbatim as one prompt, newlines and quotes included.",
				},
			},
		},
	}
}

func (t *claiRunTool) setupFlags(input pub_models.Input) ([]string, error) {
	argsRaw, ok := input["args"]
	if !ok {
		return nil, fmt.Errorf("missing args")
	}

	var args []string
	hasSubcommand := false
	switch v := argsRaw.(type) {
	case string:
		args, hasSubcommand = splitWorkerArgs(v)
	case []any:
		for i, el := range v {
			s, isString := el.(string)
			if !isString {
				return nil, fmt.Errorf("args[%d] has type '%T', want a string", i, el)
			}
			args = append(args, s)
		}
		hasSubcommand = slices.Contains(args, "q") || slices.Contains(args, "query")
	default:
		return nil, fmt.Errorf("failed to cast data of type: '%T' to string. Data: '%v'", argsRaw, argsRaw)
	}

	// Automatically prepend q to assume text query if nothing else is listed
	if !hasSubcommand {
		args = append([]string{"q"}, args...)
	}

	if len(args) <= 1 {
		return []string{}, nil
	}
	// The parent reads a worker's stdout as its answer, so the worker's
	// tool calls and their output go to its stderr log instead, and no model
	// call is spent labelling a throwaway conversation.
	return append([]string{"-r", "-reply-only", "-summarize=false"}, args...), nil
}

// splitWorkerArgs splits the flags before the q (or query) subcommand on
// whitespace and keeps everything after it as one verbatim prompt, so a
// worker brief keeps its newlines, quotes and apostrophes. A q only counts as
// the subcommand when every token before it is a flag or a flag's value;
// without one, the whole string is split on whitespace and q is prepended.
func splitWorkerArgs(raw string) ([]string, bool) {
	rest := raw
	var flags []string
	for {
		rest = strings.TrimLeftFunc(rest, unicode.IsSpace)
		if rest == "" {
			return strings.Fields(raw), false
		}
		end := strings.IndexFunc(rest, unicode.IsSpace)
		if end < 0 {
			end = len(rest)
		}
		tok := rest[:end]
		if tok == "q" || tok == "query" {
			prompt := strings.TrimSpace(rest[end:])
			if prompt == "" {
				return append(flags, tok), true
			}
			return append(flags, tok, prompt), true
		}
		isValue := len(flags) > 0 && strings.HasPrefix(flags[len(flags)-1], "-")
		if !strings.HasPrefix(tok, "-") && !isValue {
			return strings.Fields(raw), false
		}
		flags = append(flags, tok)
		rest = rest[end:]
	}
}

func (t *claiRunTool) Call(input pub_models.Input) (string, error) {
	// Validate and parse input arguments
	argsSplit, err := t.setupFlags(input)
	if err != nil {
		return "", fmt.Errorf("failed to setup automatic clai run flags: %w", err)
	}

	// Generate unique identifier for this run
	runID := generateRunID()

	// Create temporary files for stdout and stderr with run ID
	stdoutFile, stderrFile, err := createTempOutputFiles(runID)
	if err != nil {
		return "", fmt.Errorf("failed to create temporary output files: %w", err)
	}

	// Create command with environment configuration
	cmd := exec.Command(ClaiBinaryPath, argsSplit...)
	cmd.Env = append(os.Environ(), "NO_COLOR=true")

	// Setup output buffers and multi-writers to write to both buffer and file
	stdoutBuffer := &bytes.Buffer{}
	stderrBuffer := &bytes.Buffer{}
	cmd.Stdout = io.MultiWriter(stdoutBuffer, stdoutFile)
	cmd.Stderr = io.MultiWriter(stderrBuffer, stderrFile)

	// Initialize process tracking structure
	process := &claiProcess{
		cmd:    cmd,
		stdout: stdoutBuffer,
		stderr: stderrBuffer,
		done:   false,
	}

	// Register process in global map
	claiRunsMu.Lock()
	claiRuns[runID] = process
	claiRunsMu.Unlock()

	// Start the subprocess
	if err := cmd.Start(); err != nil {
		// Mark as done and record error on startup failure
		process.done = true
		process.err = err
		process.exitCode = -1
		stdoutFile.Close()
		stderrFile.Close()
		return "", fmt.Errorf("failed to start clai process: %w", err)
	}

	// Spawn goroutine to wait for process completion and close files
	go t.waitForProcessCompletion(process, stdoutFile, stderrFile)

	return runID, nil
}

// waitForProcessCompletion blocks until the process finishes and records its exit status.
func (t *claiRunTool) waitForProcessCompletion(process *claiProcess, stdoutFile, stderrFile *os.File) {
	err := process.cmd.Wait()

	// Close files once the process has finished
	stdoutFile.Close()
	stderrFile.Close()

	claiRunsMu.Lock()
	defer claiRunsMu.Unlock()

	process.done = true
	process.err = err

	// Extract exit code from error if available
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			process.exitCode = exitErr.ExitCode()
		} else {
			process.exitCode = -1
		}
	} else {
		process.exitCode = 0
	}
}

// workerLogPath is where clai_run mirrors a worker's stdout or stderr.
func workerLogPath(runID, stream string) string {
	return filepath.Join(os.TempDir(), fmt.Sprintf("clai-worker-%s-%s.log", runID, stream))
}

// createTempOutputFiles creates temporary files for stdout and stderr with the run ID.
func createTempOutputFiles(runID string) (*os.File, *os.File, error) {
	stdoutPath := workerLogPath(runID, "stdout")
	stdoutFile, err := os.Create(stdoutPath)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create stdout file: %w", err)
	}

	stderrPath := workerLogPath(runID, "stderr")
	stderrFile, err := os.Create(stderrPath)
	if err != nil {
		stdoutFile.Close()
		return nil, nil, fmt.Errorf("failed to create stderr file: %w", err)
	}

	return stdoutFile, stderrFile, nil
}
