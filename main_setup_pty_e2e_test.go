//go:build linux

package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

// setupPTYHelperEnv gates the child entrypoint of the interactive setup PTY
// e2e test (see TestSetupPTYHelper).
const setupPTYHelperEnv = "CLAI_SETUP_PTY_HELPER"

// TestSetupPTYHelper is the child entrypoint of
// Test_e2e_setup_announcement_survives_interactive_wizard. The parent spawns
// this test binary on a pseudo-terminal with setupPTYHelperEnv set; the
// helper then runs the real CLI setup path (united config migration +
// wizard) on that terminal, exactly like a user would. Without the env it is
// a no-op, so the regular test suite never enters the wizard.
func TestSetupPTYHelper(t *testing.T) {
	if os.Getenv(setupPTYHelperEnv) != "1" {
		return
	}
	os.Exit(run([]string{"s"}))
}

// openPTY allocates a Linux pseudo-terminal pair and returns the master and
// slave ends. The parent keeps the master; the slave becomes the child's
// controlling terminal via SysProcAttr.
func openPTY(t *testing.T) (master, slave *os.File) {
	t.Helper()
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("open /dev/ptmx: %v", err)
	}
	// Unlock the slave and learn its number.
	var unlock int32
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, master.Fd(), syscall.TIOCSPTLCK, uintptr(unsafe.Pointer(&unlock))); errno != 0 {
		master.Close()
		t.Fatalf("TIOCSPTLCK: %v", errno)
	}
	var n int32
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, master.Fd(), syscall.TIOCGPTN, uintptr(unsafe.Pointer(&n))); errno != 0 {
		master.Close()
		t.Fatalf("TIOCGPTN: %v", errno)
	}
	slave, err = os.OpenFile(fmt.Sprintf("/dev/pts/%d", n), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		master.Close()
		t.Fatalf("open pty slave: %v", err)
	}
	return master, slave
}

// ptyScreen models the visible terminal screen for the ANSI sequences the
// wizard emits: CR, LF, ESC[K (clear to end of line), ESC[1A (cursor up one),
// and SGR color codes (ESC[...m). It reconstructs what a user would see so
// tests assert on screen content instead of raw escape bytes.
type ptyScreen struct {
	lines [][]rune
	row   int
	col   int
}

func newPTYScreen() *ptyScreen {
	return &ptyScreen{lines: [][]rune{{}}}
}

func (s *ptyScreen) write(b []byte) {
	for i := 0; i < len(b); i++ {
		switch c := b[i]; {
		case c == '\r':
			s.col = 0
		case c == '\n':
			s.row++
			if s.row == len(s.lines) {
				s.lines = append(s.lines, []rune{})
			}
		case c == 0x1b:
			i = s.escape(b, i)
		default:
			s.putRune(rune(c))
		}
	}
}

// escape consumes the escape sequence starting at b[i] (b[i] == 0x1b) and
// returns the index of its final byte. Only the sequences the table library
// emits are interpreted; everything else is skipped as a parameter sequence.
func (s *ptyScreen) escape(b []byte, i int) int {
	if i+1 >= len(b) || b[i+1] != '[' {
		return i
	}
	j := i + 2
	for ; j < len(b); j++ {
		switch {
		case b[j] == 'K': // clear to end of line
			if s.col < len(s.lines[s.row]) {
				s.lines[s.row] = s.lines[s.row][:s.col]
			}
			return j
		case b[j] == 'A': // cursor up one
			if s.row > 0 {
				s.row--
			}
			return j
		case b[j] >= '0' && b[j] <= '9' || b[j] == ';' || b[j] == '?':
			// parameter byte; keep scanning
		default:
			// terminator (m, H, J, ...) or unknown: skip the sequence
			return j
		}
	}
	return j - 1
}

func (s *ptyScreen) putRune(r rune) {
	for len(s.lines[s.row]) <= s.col {
		s.lines[s.row] = append(s.lines[s.row], ' ')
	}
	s.lines[s.row][s.col] = r
	s.col++
}

func (s *ptyScreen) String() string {
	parts := make([]string, 0, len(s.lines))
	for _, l := range s.lines {
		parts = append(parts, string(l))
	}
	return strings.Join(parts, "\n")
}

// runSetupOnPTY spawns the current test binary (which runs the real CLI setup
// path in helper mode) on a fresh pseudo-terminal, feeds it the given
// keystrokes, and returns the raw transcript, the reconstructed final screen,
// and the exit status.
func runSetupOnPTY(t *testing.T, confDir, keystrokes string) (transcript, screen string, status int) {
	t.Helper()
	master, slave := openPTY(t)
	defer master.Close()

	cmd := exec.Command(os.Args[0], "-test.run=TestSetupPTYHelper")
	cmd.Env = append(os.Environ(), "CLAI_CONFIG_DIR="+confDir, setupPTYHelperEnv+"=1")
	cmd.Stdin = slave
	cmd.Stdout = slave
	cmd.Stderr = slave
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if err := cmd.Start(); err != nil {
		slave.Close()
		t.Fatalf("start helper on PTY: %v", err)
	}
	slave.Close() // the parent keeps only the master

	if _, err := master.WriteString(keystrokes); err != nil {
		t.Fatalf("write keystrokes: %v", err)
	}

	done := make(chan error, 1)
	raw := make(chan string, 1)
	go func() { done <- cmd.Wait() }()
	go func() {
		b, _ := io.ReadAll(master)
		raw <- string(b)
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("wizard exited with error: %v", err)
		}
	case <-time.After(30 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("wizard did not exit within 30s")
	}
	transcript = <-raw
	screenObj := newPTYScreen()
	screenObj.write([]byte(transcript))
	return transcript, screenObj.String(), cmd.ProcessState.ExitCode()
}

// Test_e2e_setup_announcement_survives_interactive_wizard proves through a
// real pseudo-terminal that the config-upgrade announcement printed before
// the setup wizard behaves as documented: it is the first thing on screen
// when the wizard opens, and it survives a single-table session (immediate
// quit) because the announcement block's trailing blank line absorbs the
// table library's one-line clear overshoot (ClearTermTo clears upTo+1
// lines). During deeper navigation each table Run() exit clears one line
// above its frame header, so the announcement may scroll off after several
// table sessions; the ordering contract (announcement before the first
// wizard frame) still holds in the raw transcript.
func Test_e2e_setup_announcement_survives_interactive_wizard(t *testing.T) {
	// Each subtest needs a fresh config dir: the first run migrates
	// textConfig.json in place, so a second run would have nothing to
	// announce.
	newConfDir := func(t *testing.T) string {
		t.Helper()
		confDir := setupMainTestConfigDir(t)
		// Downgrade textConfig.json to the pre-stoploss schema so the united
		// migration fires and prints the announcement.
		writeJSONFileAny(t, filepath.Join(confDir, "textConfig.json"), map[string]any{
			"model": "test",
		})
		return confDir
	}

	t.Run("immediate quit keeps the announcement on the final screen", func(t *testing.T) {
		_, screen, status := runSetupOnPTY(t, newConfDir(t), "q\n")
		if status != 0 {
			t.Fatalf("expected exit 0, got %d. final screen:\n%s", status, screen)
		}
		if !strings.Contains(screen, "added new field(s) to textConfig.json:") {
			t.Fatalf("expected the upgrade announcement visible on the final screen, got:\n%s", screen)
		}
	})

	t.Run("full navigation keeps the announcement before the wizard", func(t *testing.T) {
		transcript, _, status := runSetupOnPTY(t, newConfDir(t), "0\n0\nb\nq\n")
		if status != 0 {
			t.Fatalf("expected exit 0, got %d. transcript:\n%s", status, transcript)
		}
		annIdx := strings.Index(transcript, "added new field(s) to textConfig.json:")
		headerIdx := strings.Index(transcript, "Setup categories")
		if annIdx < 0 || headerIdx < 0 || annIdx > headerIdx {
			t.Fatalf("expected the announcement before the wizard frame, got:\n%s", transcript)
		}
		// The wizard really navigated: config list and preview were reached.
		if !strings.Contains(transcript, "Configs in general config") || !strings.Contains(transcript, "Selected config preview:") {
			t.Fatalf("expected the wizard navigation to reach the config list and preview, got:\n%s", transcript)
		}
	})
}
