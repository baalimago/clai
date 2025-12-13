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

	pub_models "github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
)

var (
	claiRunsMu sync.Mutex
	claiRuns   = make(map[string]*claiProcess)
	// Since the user called clai, we assume it's on path. If not, a bit of trouble
	ClaiBinaryPath = "clai"
)

type claiProcess struct {
	cmd      *exec.Cmd
	stdout   *bytes.Buffer
	stderr   *bytes.Buffer
	done     bool
	exitCode int
	err      error
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
		Description: "Run clai using a list of arguments. Spawns a non-blocking sub-process with an associated run-id.",
		Inputs: &pub_models.InputSchema{
			Type:     "object",
			Required: []string{"args"},
			Properties: map[string]pub_models.ParameterObject{
				"args": {
					Type:        "string",
					Description: "Arguments to pass to clai",
				},
			},
		},
	}
}

func (t *claiRunTool) setupFlags(input pub_models.Input) ([]string, error) {
	ret := make([]string, 0)
	argsRaw, ok := input["args"]
	if !ok {
		return nil, fmt.Errorf("missing args")
	}

	args, isOk := argsRaw.(string)
	if !isOk {
		return nil, fmt.Errorf("failed to cast data of type: '%T' to string. Data: '%v'", argsRaw, argsRaw)
	}

	argsSplit := strings.Split(args, " ")

	// Automatically append q to assume text query if nothing else is listed
	if !slices.Contains(argsSplit, "q") && !slices.Contains(argsSplit, "query") {
		argsSplit = append([]string{"q"}, argsSplit...)
	}

	if len(argsSplit) > 1 {
		ret = append([]string{"-r"}, argsSplit...)
	}

	return ret, nil
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

	// Log the temporary file paths for output tracking
	ancli.Okf("stdout log written to: %s", stdoutFile.Name())
	ancli.Okf("stderr log written to: %s", stderrFile.Name())

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

// createTempOutputFiles creates temporary files for stdout and stderr with the run ID.
func createTempOutputFiles(runID string) (*os.File, *os.File, error) {
	tempDir := os.TempDir()

	stdoutPath := filepath.Join(tempDir, fmt.Sprintf("clai-worker-%s-stdout.log", runID))
	stdoutFile, err := os.Create(stdoutPath)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create stdout file: %w", err)
	}

	stderrPath := filepath.Join(tempDir, fmt.Sprintf("clai-worker-%s-stderr.log", runID))
	stderrFile, err := os.Create(stderrPath)
	if err != nil {
		stdoutFile.Close()
		return nil, nil, fmt.Errorf("failed to create stderr file: %w", err)
	}

	return stdoutFile, stderrFile, nil
}
