package tools

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

const (
	asyncStatusRunning   = "running"
	asyncStatusSucceeded = "succeeded"
	asyncStatusFailed    = "failed"
	asyncStatusCancelled = "cancelled"

	asyncLogPreviewBytes = 4096
	asyncCancelGrace     = 500 * time.Millisecond
)

var (
	asyncCmdManager    = newAsyncCmdManager()
	asyncSpawnObserver func(string)
)

type asyncCmdManagerImpl struct {
	mu   sync.RWMutex
	cmds map[string]*asyncCmd
}

type asyncCmd struct {
	mu              sync.RWMutex
	cmdID           string
	toolName        string
	status          string
	startedAt       time.Time
	finishedAt      *time.Time
	pid             int
	command         string
	args            []string
	cwd             string
	stdoutLogPath   string
	stderrLogPath   string
	stdout          *previewBuffer
	stderr          *previewBuffer
	cmd             *exec.Cmd
	cancel          context.CancelFunc
	done            chan struct{}
	cancelRequested bool
	cancelSent      bool
	exitCode        *int
	errText         *string
}

type previewBuffer struct {
	mu        sync.RWMutex
	buf       bytes.Buffer
	truncated bool
}

type AsyncCmdSnapshot struct {
	CmdID  string `json:"async_cmd_id"`
	Status string `json:"status"`
}

type asyncCmdStatus struct {
	CmdID      string  `json:"async_cmd_id"`
	Status     string  `json:"status"`
	StartedAt  string  `json:"started_at"`
	FinishedAt *string `json:"finished_at"`
	PID        *int    `json:"pid"`
	ExitCode   *int    `json:"exit_code"`
	Error      *string `json:"error"`
}

type asyncLogStream struct {
	Preview   string `json:"preview"`
	Truncated bool   `json:"truncated"`
	LogPath   string `json:"log_path"`
}

type asyncCmdLogs struct {
	CmdID  string         `json:"async_cmd_id"`
	Status string         `json:"status"`
	Stdout asyncLogStream `json:"stdout"`
	Stderr asyncLogStream `json:"stderr"`
}

type asyncCmdAwait struct {
	Result    string           `json:"result"`
	AsyncCmds []asyncCmdStatus `json:"async_cmds"`
}

type asyncCmdRunSpec struct {
	Command string
	Args    []string
	CWD     string
	Env     map[string]string
}

func newAsyncCmdManager() *asyncCmdManagerImpl {
	return &asyncCmdManagerImpl{cmds: map[string]*asyncCmd{}}
}

func (b *previewBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	remaining := asyncLogPreviewBytes - b.buf.Len()
	if remaining > 0 {
		if len(p) > remaining {
			_, _ = b.buf.Write(p[:remaining])
			b.truncated = true
		} else {
			_, _ = b.buf.Write(p)
		}
	} else if len(p) > 0 {
		b.truncated = true
	}
	return len(p), nil
}

func (b *previewBuffer) Snapshot() (string, bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.buf.String(), b.truncated
}

func generateAsyncCmdID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return "async_cmd_" + hex.EncodeToString(b)
}

func (m *asyncCmdManagerImpl) Spawn(parent context.Context, toolName string, spec asyncCmdRunSpec) (*asyncCmd, error) {
	if spec.Command == "" {
		return nil, errors.New("command must not be empty")
	}
	cmdID := generateAsyncCmdID()
	stdoutPath := filepath.Join(os.TempDir(), fmt.Sprintf("clai-async-cmd-%s-stdout.log", cmdID))
	stderrPath := filepath.Join(os.TempDir(), fmt.Sprintf("clai-async-cmd-%s-stderr.log", cmdID))
	stdoutFile, err := os.Create(stdoutPath)
	if err != nil {
		return nil, fmt.Errorf("create stdout log: %w", err)
	}
	stderrFile, err := os.Create(stderrPath)
	if err != nil {
		_ = stdoutFile.Close()
		return nil, fmt.Errorf("create stderr log: %w", err)
	}

	cmdCtx, cancel := context.WithCancel(parent)
	cmd := exec.CommandContext(cmdCtx, spec.Command, spec.Args...)
	cmd.Dir = spec.CWD
	cmd.Env = mergeEnv(spec.Env)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	stdoutPreview := &previewBuffer{}
	stderrPreview := &previewBuffer{}
	cmd.Stdout = io.MultiWriter(stdoutFile, stdoutPreview)
	cmd.Stderr = io.MultiWriter(stderrFile, stderrPreview)

	if err := cmd.Start(); err != nil {
		_ = stdoutFile.Close()
		_ = stderrFile.Close()
		cancel()
		_ = os.Remove(stdoutPath)
		_ = os.Remove(stderrPath)
		return nil, fmt.Errorf("start async command: %w", err)
	}
	cmdHandle := &asyncCmd{
		cmdID:         cmdID,
		toolName:      toolName,
		status:        asyncStatusRunning,
		startedAt:     time.Now().UTC(),
		pid:           cmd.Process.Pid,
		command:       spec.Command,
		args:          append([]string(nil), spec.Args...),
		cwd:           spec.CWD,
		stdoutLogPath: stdoutPath,
		stderrLogPath: stderrPath,
		stdout:        stdoutPreview,
		stderr:        stderrPreview,
		cmd:           cmd,
		cancel:        cancel,
		done:          make(chan struct{}),
	}

	m.mu.Lock()
	m.cmds[cmdID] = cmdHandle
	m.mu.Unlock()

	go m.waitForCmd(cmdHandle, stdoutFile, stderrFile)
	go func() {
		<-parent.Done()
		_, _ = m.Cancel(cmdID)
	}()

	return cmdHandle, nil
}

func (m *asyncCmdManagerImpl) waitForCmd(cmd *asyncCmd, stdoutFile, stderrFile *os.File) {
	err := cmd.cmd.Wait()
	_ = stdoutFile.Close()
	_ = stderrFile.Close()
	now := time.Now().UTC()
	cmd.mu.Lock()
	defer cmd.mu.Unlock()
	defer close(cmd.done)
	cmd.finishedAt = &now
	if cmd.exitCode == nil {
		if err == nil {
			code := 0
			cmd.exitCode = &code
		} else {
			code := exitCodeFromErr(err)
			cmd.exitCode = &code
		}
	}
	if err != nil {
		msg := err.Error()
		cmd.errText = &msg
	}
	if isTerminal(cmd.status) {
		return
	}
	if cmd.cancelRequested && cmd.cancelSent {
		cmd.status = asyncStatusCancelled
		return
	}
	if err != nil {
		cmd.status = asyncStatusFailed
		return
	}
	cmd.status = asyncStatusSucceeded
}

func (m *asyncCmdManagerImpl) get(cmdID string) (*asyncCmd, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	cmd, ok := m.cmds[cmdID]
	if !ok {
		return nil, fmt.Errorf("async_cmd not found: %s", cmdID)
	}
	return cmd, nil
}

func (m *asyncCmdManagerImpl) Cancel(cmdID string) (*asyncCmd, error) {
	cmd, err := m.get(cmdID)
	if err != nil {
		return nil, err
	}
	cmd.mu.Lock()
	if isTerminal(cmd.status) {
		cmd.mu.Unlock()
		return cmd, nil
	}
	cmd.cancelRequested = true
	proc := cmd.cmd.Process
	cmd.mu.Unlock()

	sentSignal := proc != nil && syscall.Kill(-proc.Pid, syscall.SIGINT) == nil

	cmd.mu.Lock()
	cmd.cancelSent = sentSignal
	cmd.mu.Unlock()
	select {
	case <-cmd.done:
	case <-time.After(asyncCancelGrace):
		if proc != nil {
			_ = syscall.Kill(-proc.Pid, syscall.SIGKILL)
		}
		select {
		case <-cmd.done:
		case <-time.After(asyncCancelGrace):
			cmd.cancel()
		}
	}

	cmd.mu.Lock()
	if !isTerminal(cmd.status) {
		now := time.Now().UTC()
		cmd.finishedAt = &now
		cmd.status = asyncStatusCancelled
	}
	cmd.mu.Unlock()
	return cmd, nil
}

func (m *asyncCmdManagerImpl) Await(ctx context.Context, cmdIDs []string) (string, []*asyncCmd, error) {
	cmds := make([]*asyncCmd, 0, len(cmdIDs))
	for _, cmdID := range cmdIDs {
		cmd, err := m.get(cmdID)
		if err != nil {
			return "", nil, err
		}
		cmds = append(cmds, cmd)
	}
	for {
		allDone := true
		for _, cmd := range cmds {
			cmd.mu.RLock()
			done := isTerminal(cmd.status)
			cmd.mu.RUnlock()
			if !done {
				allDone = false
				break
			}
		}
		if allDone {
			return "completed", cmds, nil
		}
		select {
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return "timed_out", cmds, nil
			}
			return "cancelled_by_session", cmds, nil
		case <-time.After(25 * time.Millisecond):
		}
	}
}

func (j *asyncCmd) statusResponse() asyncCmdStatus {
	j.mu.RLock()
	defer j.mu.RUnlock()
	var finished *string
	if j.finishedAt != nil {
		tmp := j.finishedAt.Format(time.RFC3339Nano)
		finished = &tmp
	}
	var pid *int
	if j.pid != 0 {
		tmp := j.pid
		pid = &tmp
	}
	return asyncCmdStatus{
		CmdID:      j.cmdID,
		Status:     j.status,
		StartedAt:  j.startedAt.Format(time.RFC3339Nano),
		FinishedAt: finished,
		PID:        pid,
		ExitCode:   cloneIntPtr(j.exitCode),
		Error:      cloneStringPtr(j.errText),
	}
}

func (j *asyncCmd) logsResponse() asyncCmdLogs {
	j.mu.RLock()
	cmdID := j.cmdID
	status := j.status
	stdoutPath := j.stdoutLogPath
	stderrPath := j.stderrLogPath
	j.mu.RUnlock()
	stdoutPreview, stdoutTrunc := j.stdout.Snapshot()
	stderrPreview, stderrTrunc := j.stderr.Snapshot()
	return asyncCmdLogs{
		CmdID:  cmdID,
		Status: status,
		Stdout: asyncLogStream{Preview: stdoutPreview, Truncated: stdoutTrunc, LogPath: stdoutPath},
		Stderr: asyncLogStream{Preview: stderrPreview, Truncated: stderrTrunc, LogPath: stderrPath},
	}
}

func isTerminal(status string) bool {
	return status == asyncStatusSucceeded || status == asyncStatusFailed || status == asyncStatusCancelled
}

func mergeEnv(overrides map[string]string) []string {
	if len(overrides) == 0 {
		return os.Environ()
	}
	env := map[string]string{}
	for _, entry := range os.Environ() {
		parts := bytes.SplitN([]byte(entry), []byte("="), 2)
		if len(parts) == 2 {
			env[string(parts[0])] = string(parts[1])
		}
	}
	maps.Copy(env, overrides)
	ret := make([]string, 0, len(env))
	for k, v := range env {
		ret = append(ret, k+"="+v)
	}
	return ret
}

func exitCodeFromErr(err error) int {
	if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
		return exitErr.ExitCode()
	}
	return -1
}

func cloneIntPtr(in *int) *int {
	if in == nil {
		return nil
	}
	v := *in
	return &v
}

func cloneStringPtr(in *string) *string {
	if in == nil {
		return nil
	}
	v := *in
	return &v
}

func mustJSONString(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("marshal json response: %w", err)
	}
	return string(b), nil
}

func ResetAsyncCmdManagerForTests() {
	asyncCmdManager = newAsyncCmdManager()
	asyncSpawnObserver = nil
	claiRunsMu.Lock()
	claiRuns = make(map[string]*claiProcess)
	claiRunsMu.Unlock()
}

func AsyncCmdSnapshotForTests() map[string]AsyncCmdSnapshot {
	asyncCmdManager.mu.RLock()
	defer asyncCmdManager.mu.RUnlock()
	ret := make(map[string]AsyncCmdSnapshot, len(asyncCmdManager.cmds))
	for id, cmd := range asyncCmdManager.cmds {
		cmd.mu.RLock()
		ret[id] = AsyncCmdSnapshot{CmdID: id, Status: cmd.status}
		cmd.mu.RUnlock()
	}
	return ret
}

func SpawnAsyncCmdForTests(ctx context.Context, command string, args []string, cwd string, env map[string]string) (string, error) {
	cmd, err := asyncCmdManager.Spawn(ctx, "test", asyncCmdRunSpec{
		Command: command,
		Args:    args,
		CWD:     cwd,
		Env:     env,
	})
	if err != nil {
		return "", err
	}
	return cmd.cmdID, nil
}

func SetAsyncSpawnObserverForTests(fn func(string)) {
	asyncSpawnObserver = fn
}

var (
	AsyncCmdRun    = &asyncCmdRunTool{}
	AsyncCmdStatus = &asyncCmdStatusTool{}
	AsyncCmdLogs   = &asyncCmdLogsTool{}
	AsyncCmdAwait  = &asyncCmdAwaitTool{}
	AsyncCmdCancel = &asyncCmdCancelTool{}
)

type (
	asyncCmdRunTool    struct{}
	asyncCmdStatusTool struct{}
	asyncCmdLogsTool   struct{}
	asyncCmdAwaitTool  struct{}
	asyncCmdCancelTool struct{}
)

func (t *asyncCmdRunTool) CallWithContext(ctx context.Context, input pub_models.Input) (string, error) {
	return callAsyncCmdRun(ctx, t.Specification().Name, input)
}

func (t *asyncCmdRunTool) Specification() pub_models.Specification {
	return pub_models.Specification{
		Name:        "async_cmd_run",
		Description: "Start a subprocess asynchronously without waiting for completion. Executes the command directly, not through an implicit shell.",
		Inputs: &pub_models.InputSchema{
			Type:     "object",
			Required: []string{"command"},
			Properties: map[string]pub_models.ParameterObject{
				"command": {Type: "string", Description: "Executable path or name."},
				"args":    {Type: "array", Description: "Already-tokenized arguments.", Items: &pub_models.ParameterObject{Type: "string"}},
				"cwd":     {Type: "string", Description: "Optional working directory."},
				"env":     {Type: "object", Description: "Optional environment variable overrides."},
			},
		},
	}
}

func (t *asyncCmdRunTool) Call(input pub_models.Input) (string, error) {
	return callAsyncCmdRun(context.Background(), t.Specification().Name, input)
}

func callAsyncCmdRun(ctx context.Context, toolName string, input pub_models.Input) (string, error) {
	command, ok := input["command"].(string)
	if !ok || command == "" {
		return "", fmt.Errorf("command must be a non-empty string")
	}
	args, err := parseStringSlice(input["args"])
	if err != nil {
		return "", fmt.Errorf("args: %w", err)
	}
	cwd := ""
	if raw, ok := input["cwd"]; ok {
		cwd, ok = raw.(string)
		if !ok {
			return "", fmt.Errorf("cwd must be a string")
		}
	}
	env, err := parseStringMap(input["env"])
	if err != nil {
		return "", fmt.Errorf("env: %w", err)
	}
	cmd, err := asyncCmdManager.Spawn(ctx, toolName, asyncCmdRunSpec{
		Command: command,
		Args:    args,
		CWD:     cwd,
		Env:     env,
	})
	if err != nil {
		return "", err
	}
	if asyncSpawnObserver != nil {
		asyncSpawnObserver(cmd.cmdID)
	}
	return mustJSONString(struct {
		CmdID         string `json:"async_cmd_id"`
		Status        string `json:"status"`
		PID           int    `json:"pid"`
		StdoutLogPath string `json:"stdout_log_path"`
		StderrLogPath string `json:"stderr_log_path"`
	}{
		CmdID:         cmd.cmdID,
		Status:        asyncStatusRunning,
		PID:           cmd.pid,
		StdoutLogPath: cmd.stdoutLogPath,
		StderrLogPath: cmd.stderrLogPath,
	})
}

func (t *asyncCmdStatusTool) CallWithContext(_ context.Context, input pub_models.Input) (string, error) {
	return t.Call(input)
}

func (t *asyncCmdStatusTool) Specification() pub_models.Specification {
	return singleAsyncCmdSpec("async_cmd_status", "Return the current structured status of an async command.")
}

func (t *asyncCmdStatusTool) Call(input pub_models.Input) (string, error) {
	cmd, err := asyncCmdFromInput(input)
	if err != nil {
		return "", err
	}
	return mustJSONString(cmd.statusResponse())
}

func (t *asyncCmdLogsTool) CallWithContext(_ context.Context, input pub_models.Input) (string, error) {
	return t.Call(input)
}

func (t *asyncCmdLogsTool) Specification() pub_models.Specification {
	return singleAsyncCmdSpec("async_cmd_logs", "Return bounded stdout/stderr previews plus log file paths.")
}

func (t *asyncCmdLogsTool) Call(input pub_models.Input) (string, error) {
	cmd, err := asyncCmdFromInput(input)
	if err != nil {
		return "", err
	}
	return mustJSONString(cmd.logsResponse())
}

func (t *asyncCmdAwaitTool) CallWithContext(ctx context.Context, input pub_models.Input) (string, error) {
	return callAsyncCmdAwait(ctx, input)
}

func (t *asyncCmdAwaitTool) Specification() pub_models.Specification {
	return pub_models.Specification{
		Name:        "async_cmd_await",
		Description: "Wait for one or more explicit async command IDs to reach terminal state.",
		Inputs: &pub_models.InputSchema{
			Type:     "object",
			Required: []string{"async_cmd_ids", "timeout_seconds"},
			Properties: map[string]pub_models.ParameterObject{
				"async_cmd_ids":   {Type: "array", Description: "Explicit async command IDs.", Items: &pub_models.ParameterObject{Type: "string"}},
				"timeout_seconds": {Type: "number", Description: "Bounded wait timeout in seconds."},
			},
		},
	}
}

func (t *asyncCmdAwaitTool) Call(input pub_models.Input) (string, error) {
	return callAsyncCmdAwait(context.Background(), input)
}

func callAsyncCmdAwait(ctx context.Context, input pub_models.Input) (string, error) {
	ids, err := parseRequiredStringSlice(input, "async_cmd_ids")
	if err != nil {
		return "", err
	}
	timeoutSeconds, err := parseTimeoutSeconds(input["timeout_seconds"])
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSeconds*float64(time.Second)))
	defer cancel()
	result, cmds, err := asyncCmdManager.Await(ctx, ids)
	if err != nil {
		return "", err
	}
	resp := asyncCmdAwait{Result: result, AsyncCmds: make([]asyncCmdStatus, 0, len(cmds))}
	for _, cmd := range cmds {
		resp.AsyncCmds = append(resp.AsyncCmds, cmd.statusResponse())
	}
	return mustJSONString(resp)
}

func (t *asyncCmdCancelTool) CallWithContext(_ context.Context, input pub_models.Input) (string, error) {
	return t.Call(input)
}

func (t *asyncCmdCancelTool) Specification() pub_models.Specification {
	return singleAsyncCmdSpec("async_cmd_cancel", "Request cancellation of a running async command.")
}

func (t *asyncCmdCancelTool) Call(input pub_models.Input) (string, error) {
	cmdID, err := requiredCmdID(input)
	if err != nil {
		return "", err
	}
	cmd, err := asyncCmdManager.Cancel(cmdID)
	if err != nil {
		return "", err
	}
	return mustJSONString(cmd.statusResponse())
}

func asyncCmdFromInput(input pub_models.Input) (*asyncCmd, error) {
	cmdID, err := requiredCmdID(input)
	if err != nil {
		return nil, err
	}
	return asyncCmdManager.get(cmdID)
}

func singleAsyncCmdSpec(name, desc string) pub_models.Specification {
	return pub_models.Specification{
		Name:        name,
		Description: desc,
		Inputs: &pub_models.InputSchema{
			Type:     "object",
			Required: []string{"async_cmd_id"},
			Properties: map[string]pub_models.ParameterObject{
				"async_cmd_id": {Type: "string", Description: "Stable async command identifier."},
			},
		},
	}
}

func mustCmdID(input pub_models.Input) string {
	cmdID, ok := input["async_cmd_id"].(string)
	if !ok || cmdID == "" {
		return ""
	}
	return cmdID
}

func requiredCmdID(input pub_models.Input) (string, error) {
	cmdID := mustCmdID(input)
	if cmdID == "" {
		return "", fmt.Errorf("async_cmd_id must be a non-empty string")
	}
	return cmdID, nil
}

func parseStringSlice(raw any) ([]string, error) {
	if raw == nil {
		return nil, nil
	}
	switch cast := raw.(type) {
	case []string:
		return append([]string(nil), cast...), nil
	case []any:
		ret := make([]string, 0, len(cast))
		for _, item := range cast {
			s, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("must contain only strings")
			}
			ret = append(ret, s)
		}
		return ret, nil
	default:
		return nil, fmt.Errorf("must be an array of strings")
	}
}

func parseRequiredStringSlice(input pub_models.Input, key string) ([]string, error) {
	raw, ok := input[key]
	if !ok {
		return nil, fmt.Errorf("%s is required", key)
	}
	ret, err := parseStringSlice(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", key, err)
	}
	if len(ret) == 0 {
		return nil, fmt.Errorf("%s must not be empty", key)
	}
	return ret, nil
}

func parseStringMap(raw any) (map[string]string, error) {
	if raw == nil {
		return nil, nil
	}
	switch cast := raw.(type) {
	case map[string]string:
		return cast, nil
	case map[string]any:
		ret := map[string]string{}
		for k, v := range cast {
			s, ok := v.(string)
			if !ok {
				return nil, fmt.Errorf("must contain only string values")
			}
			ret[k] = s
		}
		return ret, nil
	default:
		return nil, fmt.Errorf("must be an object with string values")
	}
}

func parseTimeoutSeconds(raw any) (float64, error) {
	switch v := raw.(type) {
	case int:
		return float64(v), nil
	case int32:
		return float64(v), nil
	case int64:
		return float64(v), nil
	case float32:
		return float64(v), nil
	case float64:
		return v, nil
	default:
		return 0, fmt.Errorf("timeout_seconds must be numeric")
	}
}
