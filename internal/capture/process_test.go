//go:build linux || darwin

package capture

import (
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// signalWitnessDir names the directory the wrapper and its child report to.
const signalWitnessDir = "CAPTURE_SIGNAL_WITNESS_DIR"

// wrapperScript is a recorder like the one of a machine whose ffmpeg cannot
// take a signal: the wrapper is the one that is told to stop, and it ends its
// child another way.
const wrapperScript = `trap 'echo wrapper > "$` + signalWitnessDir + `/wrapper-interrupted"; kill -TERM "$child"' INT
"$1" -test.run='^TestSignalWitness$' >/dev/null 2>&1 &
child=$!
while ! wait "$child"; do
  kill -0 "$child" 2>/dev/null || exit 0
done
`

// TestSignalWitness is the wrapper's child in the tests below, and nothing
// when run as a test: it reports an interrupt that reaches it. A shell starts
// its background jobs with SIGINT ignored; Notify makes it arrive.
func TestSignalWitness(t *testing.T) {
	dir := os.Getenv(signalWitnessDir)
	if dir == "" {
		return
	}
	signals := make(chan os.Signal, 2)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	if err := os.WriteFile(filepath.Join(dir, "ready"), []byte(strconv.Itoa(os.Getpid())), 0o644); err != nil {
		t.Fatalf("report ready: %v", err)
	}
	select {
	case sig := <-signals:
		if sig != os.Interrupt {
			break
		}
		if err := os.WriteFile(filepath.Join(dir, "child-interrupted"), nil, 0o644); err != nil {
			t.Fatalf("report the interrupt: %v", err)
		}
	case <-time.After(20 * time.Second):
	}
	// The wrapper waits for this process: end it without the test binary's
	// own teardown.
	os.Exit(0)
}

// startWrapper starts the wrapper in its own process group, so its child is
// the one other member, and returns once the child takes signals.
func startWrapper(t *testing.T) (*exec.Cmd, string) {
	t.Helper()
	dir := t.TempDir()
	script := filepath.Join(dir, "wrapper.sh")
	if err := os.WriteFile(script, []byte(wrapperScript), 0o644); err != nil {
		t.Fatalf("write the wrapper: %v", err)
	}
	cmd := exec.Command("/bin/sh", script, os.Args[0])
	cmd.Env = append(os.Environ(), signalWitnessDir+"="+dir)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start the wrapper: %v", err)
	}
	t.Cleanup(func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) })

	deadline := time.Now().Add(15 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(dir, "ready")); err == nil {
			return cmd, dir
		}
		if time.Now().After(deadline) {
			t.Fatal("the wrapper's child never took signals")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func reported(t *testing.T, dir, name string) bool {
	t.Helper()
	_, err := os.Stat(filepath.Join(dir, name))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stat %s: %v", name, err)
	}
	return err == nil
}

// TestInterruptProcess_ReachesTheWrapperAlone pins the stop of a platform
// without process groups to signal: the wrapper is interrupted, its child is
// not, although both share one group.
func TestInterruptProcess_ReachesTheWrapperAlone(t *testing.T) {
	cmd, dir := startWrapper(t)

	if err := interruptProcess(cmd.Process); err != nil {
		t.Fatalf("interruptProcess: %v", err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("the wrapper ended with %v, want it to finish by itself", err)
	}
	if !reported(t, dir, "wrapper-interrupted") {
		t.Fatal("the wrapper was not interrupted")
	}
	if reported(t, dir, "child-interrupted") {
		t.Fatal("the wrapper's child was interrupted, want the wrapper alone")
	}
}

// TestInterruptGroup_ReachesTheWrappersChild is the other half: the group
// stop of Linux and macOS interrupts the wrapper's child as well, which is
// what the test above must not do.
func TestInterruptGroup_ReachesTheWrappersChild(t *testing.T) {
	cmd, dir := startWrapper(t)

	if err := interruptGroup(cmd.Process); err != nil {
		t.Fatalf("interruptGroup: %v", err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("the wrapper ended with %v, want it to finish by itself", err)
	}
	if !reported(t, dir, "wrapper-interrupted") || !reported(t, dir, "child-interrupted") {
		t.Fatal("want the wrapper and its child interrupted together")
	}
}

// TestKillProcess_EndsAProcessThatIgnoresTheInterrupt pins the bound of the
// same platforms: the kill ends the started process, and only that one.
func TestKillProcess_EndsAProcessThatIgnoresTheInterrupt(t *testing.T) {
	cmd, dir := startWrapper(t)
	pid, err := os.ReadFile(filepath.Join(dir, "ready"))
	if err != nil {
		t.Fatalf("read the child's pid: %v", err)
	}
	child, err := strconv.Atoi(strings.TrimSpace(string(pid)))
	if err != nil {
		t.Fatalf("the child's pid: %v", err)
	}

	if err := killProcess(cmd.Process); err != nil {
		t.Fatalf("killProcess: %v", err)
	}
	err = cmd.Wait()
	if exit, ok := errors.AsType[*exec.ExitError](err); !ok || exit.Sys().(syscall.WaitStatus).Signal() != syscall.SIGKILL {
		t.Fatalf("the wrapper ended with %v, want it killed", err)
	}
	if err := syscall.Kill(child, 0); err != nil {
		t.Fatalf("the wrapper's child: %v, want it left running", err)
	}
}
