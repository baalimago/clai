package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/audio/voices"
)

// menuKeyboard is a terminal's keyboard: it gives one answer per read, so an
// answer is typed when the run asks for it. At the read after the last
// answer the user sends sig instead, and types nothing more.
type menuKeyboard struct {
	answers []string
	sig     os.Signal
	signals scriptedSignals
	ended   chan struct{}
	reads   int
}

func (k *menuKeyboard) Read(p []byte) (int, error) {
	n := k.reads
	k.reads++
	if n < len(k.answers) {
		return copy(p, k.answers[n]+"\n"), nil
	}
	if k.sig != nil && n == len(k.answers) {
		k.signals.ch <- k.sig
	}
	<-k.ended
	return 0, io.EOF
}

// menuRun runs args on the composition with stdin, the keyboard k, and
// stdout reported as terminals.
func menuRun(t *testing.T, args string, k *menuKeyboard) cliRun {
	t.Helper()
	k.signals, k.ended = newScriptedSignals(), make(chan struct{})
	defer close(k.ended)
	return runProcess(t, args, func(p *process, _ *Deps) {
		p.stdin, p.stdinTerminal, p.stdoutTerminal = k, true, true
		p.signals, p.forceExit = k.signals, func() {}
	})
}

// seedMenuLibrary is Lorentz with three confirmed clips and Syrus with one,
// under the run's config directory.
func seedMenuLibrary(t *testing.T) *voices.Library {
	t.Helper()
	useConfigDir(t)
	at := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	library := voices.NewLibrary(os.Getenv("CLAI_CONFIG_DIR"), func() time.Time {
		at = at.Add(time.Second)
		return at
	})
	for i, clip := range []struct {
		name    string
		seconds float64
	}{{"Lorentz", 8}, {"Lorentz", 6}, {"Lorentz", 4}, {"Syrus", 5}} {
		inspection := voices.Inspection{FLAC: fmt.Appendf(nil, "fLaC menu %d", i), Seconds: clip.seconds, SpeechShare: 1, MeanDBFS: -20}
		if _, err := library.Enrol(voices.Enrolment{Name: clip.name, Inspection: inspection, Status: voices.Confirmed, Source: voices.SourceAdd}); err != nil {
			t.Fatalf("enrol %s: %v", clip.name, err)
		}
	}
	return library
}

// TestRun_InterruptAtAMenuQuestion is Wire.6: on the real composition an
// interrupt at a menu question leaves the menu as q does, in silence, and
// every other signal stops the run as it always has.
func TestRun_InterruptAtAMenuQuestion(t *testing.T) {
	for _, walk := range []struct {
		name, args string
		typed      []string
	}{
		{"at the people level", "audio voices list", nil},
		{"at the delete question", "a v l lorentz 2", []string{"d"}},
	} {
		t.Run("Wire.6 an interrupt "+walk.name+" leaves as q", func(t *testing.T) {
			library := seedMenuLibrary(t)
			left := menuRun(t, walk.args, &menuKeyboard{answers: append(walk.typed, "q")})
			got := menuRun(t, walk.args, &menuKeyboard{answers: walk.typed, sig: os.Interrupt})
			if got != left || got.status != 0 || got.stderr != "" || got.stdout == "" {
				t.Fatalf("the interrupt left\n%+v\nwant what q leaves, exit 0 and a silent stderr:\n%+v", got, left)
			}
			if listed, err := library.List(); err != nil || len(listed) != 2 || len(listed[0].Clips) != 3 {
				t.Fatalf("after the interrupt the library is %+v (%v), want Lorentz with 3 clips", listed, err)
			}
		})
	}
	t.Run("a SIGTERM at a menu question stops the run as today", func(t *testing.T) {
		seedMenuLibrary(t)
		assertInterrupted(t, menuRun(t, "audio voices list", &menuKeyboard{sig: syscall.SIGTERM}), 143)
	})
	t.Run("an interrupt while a clip plays stops the run as today", func(t *testing.T) {
		seedMenuLibrary(t)
		// The player plays until it is killed; the read after p is the watch
		// for the Enter that stops a clip, which is no menu question.
		bin := t.TempDir()
		if err := os.WriteFile(filepath.Join(bin, "play"), []byte("#!/bin/sh\nexec sleep 30\n"), 0o755); err != nil {
			t.Fatalf("write the player: %v", err)
		}
		t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
		assertInterrupted(t, menuRun(t, "audio voices list lorentz 0", &menuKeyboard{answers: []string{"p"}, sig: os.Interrupt}), 130)
	})
	t.Run("an interrupt during a transcription stops the run as today", func(t *testing.T) {
		useConfigDir(t)
		t.Setenv("CLAI_MOCK_TRANSCRIBE_DELAY", "20s")
		recording := filepath.Join(t.TempDir(), "meeting.wav")
		if err := os.WriteFile(recording, []byte("RIFF-fake-wav"), 0o644); err != nil {
			t.Fatal(err)
		}
		signals := newScriptedSignals()
		stderr := &signalOnOutput{marker: "transcribing", signals: signals, sig: os.Interrupt}
		got := runProcess(t, "audio transcribe -am test "+recording, func(p *process, _ *Deps) {
			p.stdinTerminal, p.stdoutTerminal, p.stderr = true, true, stderr
			p.signals, p.forceExit = signals, func() {}
		})
		const asAt1550dec = "transcribing meeting.wav · 1 request · 0.0 MB\nwarn [clai] shutting down gracefully signal=interrupt\nmock transcription aborted: context canceled\nerror: interrupted by signal: interrupt\n"
		if got.status != 130 || got.stdout != "" || stderr.String() != asAt1550dec {
			t.Fatalf("got %+v with stderr %q, want exit 130 and stderr %q", got, stderr.String(), asAt1550dec)
		}
	})
}
