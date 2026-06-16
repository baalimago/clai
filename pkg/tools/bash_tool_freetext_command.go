package tools

import (
	"bytes"
	"fmt"
	"os/exec"
	"strconv"
	"sync/atomic"
	"syscall"
	"time"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

type FreetextCmdTool pub_models.Specification

type cmdTimeoutState struct {
	timedOut             atomic.Bool
	stoppedByInterrupt   atomic.Bool
	hardKilledAfterTimer atomic.Bool
}

const cmdDescription = "Run any entered string as a terminal command. Blocking operation. Use only for non-blocking commands, note the timeout."

var (
	defaultCmdTimeout   = 60 * time.Second
	defaultCmdKillAfter = 90 * time.Second
)

var (
	FreetextCmd = FreetextCmdTool{
		Name:        "freetext_command",
		Description: cmdDescription,
		Inputs: &pub_models.InputSchema{
			Type: "object",
			Properties: map[string]pub_models.ParameterObject{
				"command": {
					Type:        "string",
					Description: "The freetext comand. May be any string. Will return error on non-zero exit code. ",
				},
				"timeout_seconds": {
					Type:        "number",
					Description: "Optional timeout in seconds. Defaults to 60 seconds.",
				},
			},
			Required: []string{"command"},
		},
	}
	Cmd = FreetextCmdTool{
		Name:        "cmd",
		Description: cmdDescription,
		Inputs: &pub_models.InputSchema{
			Type: "object",
			Properties: map[string]pub_models.ParameterObject{
				"command": {
					Type:        "string",
					Description: "The freetext comand. May be any string. Will return error on non-zero exit code.",
				},
				"timeout_seconds": {
					Type:        "number",
					Description: "Optional timeout in seconds. Defaults to 60 seconds.",
				},
			},
			Required: []string{"command"},
		},
	}
)

func (r FreetextCmdTool) Call(input pub_models.Input) (string, error) {
	freetextCmd, ok := input["command"].(string)
	if !ok {
		return "", fmt.Errorf("read command input: command must be a string")
	}
	if freetextCmd == "" {
		return "", fmt.Errorf("validate command input: command must not be empty")
	}

	timeout, err := parseOptionalCmdTimeout(input["timeout_seconds"])
	if err != nil {
		return "", err
	}
	cmd := exec.Command("sh", "-c", freetextCmd)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf
	killAfter := cmdKillAfter(timeout)
	killDelay := max(killAfter-timeout, 0)
	state := &cmdTimeoutState{}
	if startErr := cmd.Start(); startErr != nil {
		return "", fmt.Errorf("run freetext command %q: %w", freetextCmd, startErr)
	}
	pid := cmd.Process.Pid
	timeoutTimer := time.AfterFunc(timeout, func() {
		state.timedOut.Store(true)
		_ = syscall.Kill(-pid, syscall.SIGINT)
		time.AfterFunc(killDelay, func() {
			if processGroupAlive(pid) {
				state.hardKilledAfterTimer.Store(true)
				_ = syscall.Kill(-pid, syscall.SIGKILL)
			}
		})
	})
	defer timeoutTimer.Stop()
	err = cmd.Wait()
	output := append(stdoutBuf.Bytes(), stderrBuf.Bytes()...)
	didTimeout := state.timedOut.Load()

	if exitErr, ok := err.(*exec.ExitError); ok {
		if status, ok := exitErr.Sys().(syscall.WaitStatus); ok {
			if state.timedOut.Load() {
				didTimeout = true
			}
			if status.Signaled() && status.Signal() == syscall.SIGKILL {
				state.hardKilledAfterTimer.Store(true)
			}
		}
	}
	if didTimeout && !state.hardKilledAfterTimer.Load() {
		state.stoppedByInterrupt.Store(true)
	}
	if err != nil {
		if didTimeout {
			return "", fmt.Errorf("run freetext command %q: %s Output: %s", freetextCmd, timeoutMessage(timeout, killAfter, state), string(output))
		}
		return "", fmt.Errorf("run freetext command %q: %w, output: %v", freetextCmd, err, string(output))
	}
	if didTimeout {
		return "", fmt.Errorf("run freetext command %q: %s Output: %s", freetextCmd, timeoutMessage(timeout, killAfter, state), string(output))
	}
	return string(output), nil
}

func cmdKillAfter(timeout time.Duration) time.Duration {
	if defaultCmdKillAfter < timeout {
		return timeout
	}
	return defaultCmdKillAfter
}

func timeoutMessage(timeout, killAfter time.Duration, state *cmdTimeoutState) string {
	shutdownState := "the command stopped after interrupt"
	if state.hardKilledAfterTimer.Load() {
		shutdownState = "the command required a hard-kill"
	}
	return fmt.Sprintf(
		"command timed out after %s. Interrupt was sent. Final shutdown result: %s. Hard-kill deadline was %s since start. Use async_cmd_run for longer-running commands.",
		formatSeconds(timeout),
		shutdownState,
		formatSeconds(killAfter),
	)
}

func formatSeconds(d time.Duration) string {
	return strconv.FormatFloat(d.Seconds(), 'f', -1, 64) + " seconds"
}

func processGroupAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(-pid, 0)
	return err == nil || err == syscall.EPERM
}

func parseOptionalCmdTimeout(raw any) (time.Duration, error) {
	if raw == nil {
		return defaultCmdTimeout, nil
	}
	seconds, err := parseTimeoutSeconds(raw)
	if err != nil {
		return 0, fmt.Errorf("timeout_seconds: %w", err)
	}
	if seconds <= 0 {
		return 0, fmt.Errorf("timeout_seconds must be > 0")
	}
	return time.Duration(seconds * float64(time.Second)), nil
}

func (r FreetextCmdTool) Specification() pub_models.Specification {
	return pub_models.Specification(r)
}
