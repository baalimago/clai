package tools

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path"
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
				"profile": {
					Type:        "string",
					Description: "Profile to use fur the run. May be any profile listed by clai_tool_list_profiles.",
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

	var args string
	args, isOk := argsRaw.(string)
	if !isOk {
		return nil, fmt.Errorf("failed to cast data of type: '%T' to string. Data: '%v'", argsRaw, argsRaw)
	}

	argsSplit := strings.Split(args, " ")

	// Automaticall append q to assume text query if noting else is listed
	if !slices.Contains(argsSplit, "q") && !slices.Contains(argsSplit, "query") {
		argsSplit = append([]string{"q"}, argsSplit...)
	}

	if len(argsSplit) > 1 {
		ret = append([]string{"-r"}, argsSplit...)
	}

	profileRaw, ok := input["profile"]
	if !ok {
		// No attempt to select profile, all good lets return
		return ret, nil
	}
	profile, ok := profileRaw.(string)
	if !ok {
		return nil, fmt.Errorf("failed to cast data of type: '%T' to string. Data: '%v'", profileRaw, profileRaw)
	}

	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get user cache dir: %w", err)
	}
	profiles, err := loadDynProfiles(cacheDir)
	if err != nil {
		return nil, fmt.Errorf("failed to load profiles: %v", err)
	}
	found := false
	for _, p := range profiles {
		if p.Name == profile {
			ret = append([]string{"-prp", path.Join(cacheDir, "clai", "dynProfiles", fmt.Sprintf("%v.json", profile))}, ret...)
			found = true
			break
		}
	}
	if !found {
		return nil, fmt.Errorf("failed to find profile: '%v'", profile)
	}
	return ret, nil
}

func (t *claiRunTool) Call(input pub_models.Input) (string, error) {
	runID := generateRunID()

	argsSplit, err := t.setupFlags(input)
	if err != nil {
		return "", fmt.Errorf("failed to setup automatic clai run flags: %w", err)
	}

	ancli.Okf("now running: %v %v", ClaiBinaryPath, argsSplit)

	cmd := exec.Command(ClaiBinaryPath, argsSplit...)
	cmd.Env = append(os.Environ(), "NO_COLOR=true")
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	process := &claiProcess{
		cmd:    cmd,
		stdout: stdout,
		stderr: stderr,
	}

	claiRunsMu.Lock()
	claiRuns[runID] = process
	claiRunsMu.Unlock()

	if err := cmd.Start(); err != nil {
		process.done = true
		process.err = err
		process.exitCode = -1
		return "", fmt.Errorf("failed to start process: %w", err)
	}

	go func() {
		err := cmd.Wait()
		claiRunsMu.Lock()
		defer claiRunsMu.Unlock()

		process.done = true
		process.err = err
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				process.exitCode = exitErr.ExitCode()
			} else {
				process.exitCode = -1
			}
		} else {
			process.exitCode = 0
		}
	}()

	return runID, nil
}
