//go:build linux

package e2e

import (
	"encoding/pem"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/remote"
)

// runRemoteOnPTY runs this test binary as clai on a pseudo-terminal, as a
// person at a shell would, answers the trust question with answer, and
// returns what the terminal showed, what reached stdout, and the exit
// status. stdout is a pipe, as in `clai -prp … q … | jq`: the question
// must reach the terminal and stay out of the output. The child has no
// injected client: it trusts the fake hub's certificate through
// SSL_CERT_FILE, like any https origin.
func runRemoteOnPTY(t *testing.T, env remoteE2E, args []string, answer string) (screen, stdout string, status int) {
	t.Helper()
	certFile := filepath.Join(t.TempDir(), "hub.pem")
	cert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: env.hub.srv.Certificate().Raw})
	if err := os.WriteFile(certFile, cert, 0o600); err != nil {
		t.Fatalf("write hub certificate: %v", err)
	}
	master, slave := openPTY(t)
	defer master.Close()

	cmd := exec.Command(os.Args[0], args...)
	environ := slices.DeleteFunc(os.Environ(), func(kv string) bool { return strings.HasPrefix(kv, "TTY=") })
	gorace := strings.TrimSpace(os.Getenv("GORACE") + " atexit_sleep_ms=0")
	cmd.Env = append(environ,
		cliModeEnv+"=1",
		"CLAI_CONFIG_DIR="+env.confDir,
		"CLAI_CACHE_DIR="+env.cacheDir,
		"CLAI_CLOUD_URL="+env.hub.srv.URL,
		"SSL_CERT_FILE="+certFile,
		"NO_PROXY=127.0.0.1,localhost",
		"GORACE="+gorace,
	)
	var out strings.Builder
	cmd.Stdin = slave
	cmd.Stdout = &out
	cmd.Stderr = slave
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if err := cmd.Start(); err != nil {
		slave.Close()
		t.Fatalf("start clai on PTY: %v", err)
	}
	slave.Close()

	if _, err := master.WriteString(answer + "\n"); err != nil {
		t.Fatalf("answer the trust question: %v", err)
	}
	done := make(chan error, 1)
	raw := make(chan string, 1)
	go func() { done <- cmd.Wait() }()
	go func() {
		b, _ := io.ReadAll(master)
		raw <- string(b)
	}()
	select {
	case <-done:
	case <-time.After(25 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("clai did not exit within 25s")
	}
	return <-raw, out.String(), cmd.ProcessState.ExitCode()
}

func Test_REMOTE_terminalAsksBeforeTheFirstRun(t *testing.T) {
	t.Run("yes trusts and runs", func(t *testing.T) {
		env := setupRemoteE2E(t)
		screen, stdout, status := runRemoteOnPTY(t, env, []string{"-r", "-prp", "cchub:ada/bot", "q", "hello"}, "y")
		if status != 0 || stdout != "hello\n" {
			t.Fatalf("status %d stdout %q, screen:\n%s", status, stdout, screen)
		}
		for _, want := range []string{"▸ cchub:ada/bot · fetched · runs on your machine", "  sha256   " + digestOf(remoteBotV1), "▸ model · test", "▸ prompt · 16 characters", "  │ You are bot one.", "? Trust cchub:ada/bot? [y/N] "} {
			if !strings.Contains(screen, want) {
				t.Fatalf("screen lacks %q:\n%s", want, screen)
			}
		}
		if !strings.Contains(env.trusted(t), digestOf(remoteBotV1)) {
			t.Fatalf("yes did not record trust: %s", env.trusted(t))
		}
		env.assertCacheHolds(t, digestOf(remoteBotV1))
	})
	t.Run("no refuses with exit 3", func(t *testing.T) {
		env := setupRemoteE2E(t)
		screen, stdout, status := runRemoteOnPTY(t, env, []string{"-r", "-prp", "cchub:ada/bot", "q", "hello"}, "n")
		if status != remote.ExitRefused || stdout != "" || !strings.Contains(screen, "was not trusted") {
			t.Fatalf("status %d stdout %q, screen:\n%s", status, stdout, screen)
		}
		if env.trusted(t) != "" {
			t.Fatalf("no recorded trust: %s", env.trusted(t))
		}
	})
}
