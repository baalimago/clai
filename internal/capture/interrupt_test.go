//go:build linux || darwin

package capture

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestRecord_InterruptFinalizes pins the graceful stop of a cancelled call:
// the recorder asks ffmpeg to finish at once, ffmpeg finalizes the header, and
// the file stays at its place with every sample written until then.
func TestRecord_InterruptFinalizes(t *testing.T) {
	fakeFFmpeg(t)
	t.Setenv("FAKE_FFMPEG_RECORD_MODE", "growing")
	req := recordRequest(t)
	req.MaxDuration = 10 * time.Second
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cancelled := make(chan time.Time, 1)
	go cancelOnSamples(t, req.Dir, func() {
		cancelled <- time.Now()
		cancel()
	})

	rec, err := recorder{finalizeGrace: 5 * time.Second}.Record(ctx, req)
	stopTook := time.Since(<-cancelled)

	interrupted := requireInterrupted(t, err)
	if interrupted.Path != rec.Path || interrupted.Finish != FinishFinalized || interrupted.Quiet {
		t.Fatalf("interrupted = %+v, want %q finalized by ffmpeg", interrupted, rec.Path)
	}
	if rec.StoppedBecause != StopInterrupt || rec.Finish != FinishFinalized {
		t.Fatalf("recording = %+v, want an interrupt that ffmpeg finalized", rec)
	}
	if want := "recording interrupted: saved " + rec.Path; err.Error() != want {
		t.Fatalf("error = %q, want %q", err, want)
	}
	requireSettledWAV(t, rec.Path)
	requireOnlyFile(t, req.Dir, rec.Path)
	// The fake finalizes in 50 ms. A stop that waits for a terminal signal
	// first, as the recorder did while ffmpeg shared its process group, takes
	// a second longer.
	if stopTook > 700*time.Millisecond {
		t.Fatalf("the stop took %v, want the interrupt sent at once", stopTook)
	}
}

// TestRecord_InterruptKillsAnIgnoringRecorder pins the bound: a recorder that
// ignores the interrupt is killed after the grace, and the samples it wrote
// are kept behind a header the recorder repairs.
func TestRecord_InterruptKillsAnIgnoringRecorder(t *testing.T) {
	fakeFFmpeg(t)
	t.Setenv("FAKE_FFMPEG_RECORD_MODE", "stubborn")
	req := recordRequest(t)
	req.MaxDuration = 10 * time.Second
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cancelled := make(chan time.Time, 1)
	go cancelOnSamples(t, req.Dir, func() {
		cancelled <- time.Now()
		cancel()
	})

	rec, err := recorder{finalizeGrace: 100 * time.Millisecond}.Record(ctx, req)
	stopTook := time.Since(<-cancelled)

	interrupted := requireInterrupted(t, err)
	if interrupted.Path != rec.Path || interrupted.Finish != FinishRepaired {
		t.Fatalf("interrupted = %+v, want %q with a repaired header", interrupted, rec.Path)
	}
	if !strings.Contains(err.Error(), "saved "+rec.Path) || !strings.Contains(err.Error(), "killed") {
		t.Fatalf("error = %q, want the saved path and the kill", err)
	}
	if stopTook < 100*time.Millisecond || stopTook > 900*time.Millisecond {
		t.Fatalf("the stop took %v, want the 100ms grace and then the kill", stopTook)
	}
	if samples := requireSettledWAV(t, rec.Path); samples == 0 {
		t.Fatalf("the repaired recording holds no sample")
	}
	requireOnlyFile(t, req.Dir, rec.Path)
}

// TestRecord_OtherStopsKillAnIgnoringRecorder pins the same bound for a stop
// that is not a cancellation: the call succeeds with the repaired file.
func TestRecord_OtherStopsKillAnIgnoringRecorder(t *testing.T) {
	fakeFFmpeg(t)
	t.Setenv("FAKE_FFMPEG_RECORD_MODE", "stubborn")
	req := recordRequest(t)
	req.MaxDuration = 200 * time.Millisecond

	started := time.Now()
	rec, err := recorder{finalizeGrace: 100 * time.Millisecond}.Record(context.Background(), req)
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	if took := time.Since(started); took > 2*time.Second {
		t.Fatalf("Record took %v, want the duration stop, the grace and the kill", took)
	}
	if rec.StoppedBecause != StopMaxSeconds || rec.Finish != FinishRepaired {
		t.Fatalf("recording = %+v, want max_seconds with a repaired header", rec)
	}
	requireSettledWAV(t, rec.Path)
}

// TestRecord_InterruptKeepsAQuietRecording pins the one difference between a
// cancelled recording and every other stop: it is kept although no sample
// reached the silence threshold, and the error says so.
func TestRecord_InterruptKeepsAQuietRecording(t *testing.T) {
	fakeFFmpeg(t)
	t.Setenv("FAKE_FFMPEG_RECORD_MODE", "silent")
	req := recordRequest(t)
	req.MaxDuration = 10 * time.Second
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go cancelOnSamples(t, req.Dir, cancel)

	rec, err := recorder{finalizeGrace: time.Second}.Record(ctx, req)
	interrupted := requireInterrupted(t, err)
	if interrupted.Path != rec.Path || !interrupted.Quiet || interrupted.Finish != FinishFinalized {
		t.Fatalf("interrupted = %+v, want the quiet recording kept at %q", interrupted, rec.Path)
	}
	if !strings.Contains(err.Error(), "no sample reached the silence threshold") {
		t.Fatalf("error = %q, want it to name the quiet recording", err)
	}
	requireSettledWAV(t, rec.Path)
}

// TestRecord_InterruptWithNothingWritten pins the cancelled call that has
// nothing to keep: it still reports the cancellation, names the loss, and
// leaves no file behind.
func TestRecord_InterruptWithNothingWritten(t *testing.T) {
	fakeFFmpeg(t)
	t.Setenv("FAKE_FFMPEG_RECORD_MODE", "stubborn_empty")
	req := recordRequest(t)
	req.MaxDuration = 10 * time.Second
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go cancelOnPartFile(t, req.Dir, cancel)

	rec, err := recorder{finalizeGrace: 100 * time.Millisecond}.Record(ctx, req)
	interrupted := requireInterrupted(t, err)
	if interrupted.Path != "" || interrupted.Lost == nil || rec.Finish != "" {
		t.Fatalf("interrupted = %+v, recording = %+v, want nothing saved and the reason", interrupted, rec)
	}
	if !strings.HasPrefix(err.Error(), "recording interrupted: nothing saved: ") {
		t.Fatalf("error = %q, want it to say that nothing was saved", err)
	}
	entries, readErr := os.ReadDir(req.Dir)
	if readErr != nil || len(entries) != 0 {
		t.Fatalf("entries = %v, err = %v, want an empty directory", entries, readErr)
	}
}

// TestStartFFmpeg_OwnProcessGroup pins who may stop ffmpeg: a terminal sends
// Ctrl-C to clai's process group, and ffmpeg is not in it.
func TestStartFFmpeg_OwnProcessGroup(t *testing.T) {
	fakeFFmpeg(t)
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Fatal(err)
	}
	cmd, stderr, err := startFFmpeg(ffmpeg, []Source{{Kind: Mic, Name: "mic", Default: true}}, filepath.Join(t.TempDir(), "out.wav"), -40)
	if err != nil {
		t.Fatalf("startFFmpeg: %v", err)
	}
	procDone := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(procDone)
	}()
	t.Cleanup(func() {
		requestStop(cmd, procDone, time.Second)
		_ = stderr.Close()
	})

	group, err := syscall.Getpgid(cmd.Process.Pid)
	if err != nil {
		t.Fatalf("Getpgid: %v", err)
	}
	if group != cmd.Process.Pid || group == syscall.Getpgrp() {
		t.Fatalf("ffmpeg is in group %d, want its own group %d and not this process's %d", group, cmd.Process.Pid, syscall.Getpgrp())
	}
}

// TestStartFFmpeg_FlushesEveryPacket pins the flag that makes a killed
// recording worth repairing: without it ffmpeg keeps seconds of samples in
// memory.
func TestStartFFmpeg_FlushesEveryPacket(t *testing.T) {
	fakeFFmpeg(t)
	log := filepath.Join(t.TempDir(), "args")
	t.Setenv("FAKE_FFMPEG_ARGS_LOG", log)
	req := recordRequest(t)
	req.MaxDuration = 100 * time.Millisecond
	if _, err := New().Record(context.Background(), req); err != nil {
		t.Fatalf("Record: %v", err)
	}
	raw, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Fields(string(raw))
	flag := slices.Index(args, "-flush_packets")
	format := slices.Index(args, "-f")
	for i, arg := range args {
		if arg == "wav" && i > 0 && args[i-1] == "-f" {
			format = i - 1
		}
	}
	if flag < 0 || args[flag+1] != "1" || flag > format {
		t.Fatalf("args = %q, want -flush_packets 1 among the output options", args)
	}
}

// TestRequestStop_Bounds pins both ends of the grace: a process that ignores
// the interrupt is killed after it, and one that never started a group is
// still reached.
func TestRequestStop_Bounds(t *testing.T) {
	script := filepath.Join(t.TempDir(), "ignore.sh")
	body := "#!/bin/sh\ntrap '' INT TERM\nwhile :; do sleep 0.01; done\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(script)
	ownProcessGroup(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start child: %v", err)
	}
	procDone := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(procDone)
	}()
	time.Sleep(50 * time.Millisecond)

	started := time.Now()
	requestStop(cmd, procDone, 100*time.Millisecond)
	took := time.Since(started)
	if took < 100*time.Millisecond || took > 900*time.Millisecond {
		t.Fatalf("requestStop took %v, want the 100ms grace and then the kill", took)
	}
	select {
	case <-procDone:
	default:
		t.Fatal("requestStop returned while the process runs")
	}
	if state := cmd.ProcessState; state == nil || state.Sys().(syscall.WaitStatus).Signal() != syscall.SIGKILL {
		t.Fatalf("process state = %v, want killed", state)
	}
}

func TestInterruptedError(t *testing.T) {
	cause := errors.New("disk full")
	lost := &InterruptedError{Lost: cause}
	if !errors.Is(lost, ErrInterrupted) || !errors.Is(lost, cause) {
		t.Fatalf("error %v must be ErrInterrupted and hold its cause", lost)
	}
	if got, want := lost.Error(), "recording interrupted: nothing saved: disk full"; got != want {
		t.Fatalf("error = %q, want %q", got, want)
	}
	saved := &InterruptedError{Path: "/r/a.wav", Finish: FinishRepaired, Quiet: true}
	want := "recording interrupted: saved /r/a.wav; ffmpeg did not finish in time and was killed, so the last moments may be missing; no sample reached the silence threshold"
	if got := saved.Error(); got != want {
		t.Fatalf("error = %q, want %q", got, want)
	}
	if errors.Is(saved, ErrNoAudio) {
		t.Fatalf("a saved recording must not report ErrNoAudio")
	}
}

func requireInterrupted(t *testing.T, err error) *InterruptedError {
	t.Helper()
	var interrupted *InterruptedError
	if !errors.As(err, &interrupted) || !errors.Is(err, ErrInterrupted) {
		t.Fatalf("error = %v, want an *InterruptedError", err)
	}
	return interrupted
}

// requireSettledWAV fails unless both header sizes of the WAV match the file,
// which is what a player needs, and returns the number of samples.
func requireSettledWAV(t *testing.T, path string) int {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read recording: %v", err)
	}
	if len(raw) < 44 || string(raw[:4]) != "RIFF" || string(raw[8:12]) != "WAVE" || string(raw[36:40]) != "data" {
		t.Fatalf("recording %q is not the fixture's WAV: % x", path, raw[:min(len(raw), 44)])
	}
	riff := int(binary.LittleEndian.Uint32(raw[4:8]))
	data := int(binary.LittleEndian.Uint32(raw[40:44]))
	if riff != len(raw)-8 || data != len(raw)-44 || data%2 != 0 {
		t.Fatalf("header sizes riff=%d data=%d do not match the %d bytes on disk", riff, data, len(raw))
	}
	return data / 2
}

func requireOnlyFile(t *testing.T, dir, path string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || filepath.Join(dir, entries[0].Name()) != path {
		t.Fatalf("entries = %v, want only %q", entries, path)
	}
}

// cancelOnSamples cancels once the part file holds samples, which the fixture
// writes after it installed its signal handling.
func cancelOnSamples(t *testing.T, dir string, cancel func()) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		matches, _ := filepath.Glob(filepath.Join(dir, "*.wav.part"))
		if len(matches) > 0 {
			if info, err := os.Stat(matches[0]); err == nil && info.Size() >= 48 {
				cancel()
				return
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Errorf("no samples appeared in %q", dir)
	cancel()
}

func hasRIFFHeader(path string) (bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer file.Close()
	var header [4]byte
	if _, err := io.ReadFull(file, header[:]); err != nil {
		return false, err
	}
	return string(header[:]) == "RIFF", nil
}
