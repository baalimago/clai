package testenv

import (
	"flag"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/baalimago/go_away_boilerplate/pkg/testboil"
)

type lockedBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func TestTimeoutFlag_readsTheTimeoutTheGoCommandGaveTheBinary(t *testing.T) {
	got, err := timeoutFlag()
	if err != nil {
		t.Fatalf("timeoutFlag: %v", err)
	}
	want, err := time.ParseDuration(flag.Lookup("test.timeout").Value.String())
	if err != nil {
		t.Fatalf("parse the test.timeout flag: %v", err)
	}
	if got != want {
		t.Fatalf("timeoutFlag() = %v, want the flag's %v", got, want)
	}
}

func TestSample_measuresTheRunningBinary(t *testing.T) {
	start := time.Now().Add(-2 * time.Second)

	got, err := sample(start, 30*time.Second)

	if runtime.GOOS == "linux" && err != nil {
		t.Fatalf("sample: %v", err)
	}
	if got.wall < 2*time.Second || got.timeout != 30*time.Second {
		t.Fatalf("wall %v within timeout %v, want at least 2s within 30s", got.wall, got.timeout)
	}
	if got.procs != runtime.GOMAXPROCS(0) || got.cpus != runtime.NumCPU() {
		t.Fatalf("procs %d of %d CPUs, want %d of %d", got.procs, got.cpus, runtime.GOMAXPROCS(0), runtime.NumCPU())
	}
	if got.cpu <= 0 {
		t.Fatalf("cpu = %v, want the time this binary has already burnt", got.cpu)
	}
	if got.goroutines < 1 {
		t.Fatalf("goroutines = %d, want at least this one", got.goroutines)
	}
	if runtime.GOOS == "linux" && !got.loadKnown {
		t.Fatal("load unknown on linux, want it read from /proc/loadavg")
	}
	if runtime.GOOS == "linux" && !got.waitKnown {
		t.Fatal("run-queue wait unknown on linux, want it summed from /proc/self/task")
	}
}

func TestRunQueueWait_sumsTheThreadsWaitFieldAndSkipsThreadsThatExited(t *testing.T) {
	tasks := t.TempDir()
	for name, stat := range map[string]string{"11": "100 2000000000 5\n", "12": "50 500000000 2\n"} {
		if err := os.MkdirAll(filepath.Join(tasks, name), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(tasks, name, "schedstat"), []byte(stat), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(tasks, "13"), 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := runQueueWait(tasks)
	if err != nil || got != 2500*time.Millisecond {
		t.Fatalf("runQueueWait = %v, %v; want 2.5s", got, err)
	}

	if err := os.WriteFile(filepath.Join(tasks, "13", "schedstat"), []byte("not numbers\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runQueueWait(tasks); err == nil || !strings.Contains(err.Error(), "thread 13") {
		t.Fatalf("runQueueWait(garbage) error = %v, want the thread named", err)
	}
	if _, err := runQueueWait(filepath.Join(tasks, "missing")); err == nil || !strings.Contains(err.Error(), "list threads") {
		t.Fatalf("runQueueWait(missing dir) error = %v, want the listing failure", err)
	}
}

func TestLoadAverageAt_readsTheFirstFieldAndNamesWhatItCannotRead(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "loadavg")
	if err := os.WriteFile(good, []byte("24.31 18.20 11.07 3/2210 41517\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := loadAverageAt(good)
	if err != nil || got != 24.31 {
		t.Fatalf("loadAverageAt(good) = %v, %v; want 24.31", got, err)
	}

	bad := filepath.Join(dir, "garbage")
	if err := os.WriteFile(bad, []byte("not a load\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadAverageAt(bad); err == nil || !strings.Contains(err.Error(), `"not"`) {
		t.Fatalf("loadAverageAt(garbage) error = %v, want the field it could not parse", err)
	}
	if _, err := loadAverageAt(filepath.Join(dir, "missing")); err == nil || !strings.Contains(err.Error(), "read load average") {
		t.Fatalf("loadAverageAt(missing) error = %v, want the read failure", err)
	}
}

func TestVerdict_String_namesAnUnknownValue(t *testing.T) {
	if got := verdict(42).String(); got != "verdict(42)" {
		t.Fatalf("verdict(42).String() = %q", got)
	}
}

func TestBudget_verdict(t *testing.T) {
	twoProcs := budget{wall: 29 * time.Second, procs: 2, cpus: 22}
	for _, tc := range []struct {
		name      string
		cpu, wait time.Duration
		waitKnown bool
		load      float64
		loadKnown bool
		want      verdict
	}{
		{"all of its processors, no waiting", 56 * time.Second, 300 * time.Millisecond, true, 1.2, true, busy},
		{"half its processors, half the run waiting", 29 * time.Second, 27 * time.Second, true, 18.6, true, starved},
		{"most of its processors but a third of the run waiting", 40 * time.Second, 10 * time.Second, true, 3, true, starved},
		{"waiting just over a quarter of the run", 4 * time.Second, 7300 * time.Millisecond, true, 2, true, starved},
		{"waiting just under a quarter of the run", 4 * time.Second, 7200 * time.Millisecond, true, 2, true, idle},
		{"little CPU, no waiting, quiet host", 4 * time.Second, 200 * time.Millisecond, true, 2.1, true, idle},
		{"little CPU, no waiting, loaded host", 4 * time.Second, 200 * time.Millisecond, true, 30, true, idle},
		{"wait unknown, load at the CPU count", 4 * time.Second, 0, false, 22, true, starved},
		{"wait unknown, quiet host", 4 * time.Second, 0, false, 2.1, true, idle},
		{"wait unknown, most of its processors", 40 * time.Second, 0, false, 2.1, true, busy},
		{"nothing measured", 4 * time.Second, 0, false, 0, false, waiting},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := twoProcs
			b.cpu, b.wait, b.waitKnown, b.load, b.loadKnown = tc.cpu, tc.wait, tc.waitKnown, tc.load, tc.loadKnown
			if got := b.verdict(); got != tc.want {
				t.Fatalf("verdict() = %v, want %v", got, tc.want)
			}
		})
	}
	if got := (budget{}).verdict(); got != waiting {
		t.Fatalf("a budget with no wall time has verdict %v, want %v", got, waiting)
	}
}

func TestBudget_String_leadsWithTheVerdictAndNamesEveryFigure(t *testing.T) {
	b := budget{
		wall: 29 * time.Second, timeout: 30 * time.Second, cpu: 29500 * time.Millisecond,
		wait: 27 * time.Second, waitKnown: true,
		procs: 2, cpus: 22, load: 18.6, loadKnown: true,
		goroutines: 1203, gcs: 312, gcPause: 812 * time.Millisecond,
	}
	want := "testenv: starved: the binary's threads waited for a CPU, the host is oversubscribed; " +
		"29.0s of the 30s budget gone with 29.5s CPU of 58.0s available, 27.0s waiting for a CPU " +
		"(GOMAXPROCS 2, 22 CPUs, load 18.6); 1203 goroutines; 312 GCs paused 812ms"
	testboil.FailTestIfDiff(t, b.String(), want)

	b.waitKnown, b.loadKnown = false, false
	got := b.String()
	for _, want := range []string{"testenv: waiting:", "run-queue wait unknown", "load unknown"} {
		if !strings.Contains(got, want) {
			t.Fatalf("String() with nothing but CPU measured = %q, want %q in it", got, want)
		}
	}
}

func TestArmDeadlineReport_writesOneLineBeforeTheDeadline(t *testing.T) {
	var out lockedBuffer
	report := armDeadlineReport(&out, time.Now(), 1100*time.Millisecond, time.Second)
	t.Cleanup(func() { report.stop() })

	testboil.CheckTrueWithinTimeout(t, func(t *testing.T) bool {
		return strings.Contains(out.String(), "\n")
	}, 2*time.Second, 5*time.Millisecond)

	got := out.String()
	if strings.Count(got, "testenv: ") != 1 {
		t.Fatalf("report = %q, want exactly one testenv line", got)
	}
	if !strings.Contains(got, "of the 1.1s budget gone") {
		t.Fatalf("report = %q, want the budget named", got)
	}
	if got := report.stop(); got != alreadyFired {
		t.Fatalf("stop() after the report = %v, want %v", got, alreadyFired)
	}
}

func TestArmDeadlineReport_stopDisarmsAPendingReport(t *testing.T) {
	var out lockedBuffer
	report := armDeadlineReport(&out, time.Now(), time.Hour, time.Second)

	if got := report.stop(); got != disarmed {
		t.Fatalf("stop() = %v, want %v", got, disarmed)
	}
	if got := out.String(); got != "" {
		t.Fatalf("a disarmed report wrote %q", got)
	}
}

func TestArmDeadlineReport_armsNothingWithoutRoomBeforeTheDeadline(t *testing.T) {
	var out lockedBuffer
	for _, tc := range []struct {
		name    string
		start   time.Time
		timeout time.Duration
	}{
		{"timeout disabled", time.Now(), 0},
		{"timeout no longer than the margin", time.Now(), time.Second},
		{"deadline already within the margin", time.Now().Add(-time.Minute), 30 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			report := armDeadlineReport(&out, tc.start, tc.timeout, time.Second)
			if got := report.stop(); got != neverArmed {
				t.Fatalf("stop() = %v, want %v", got, neverArmed)
			}
		})
	}
	if got := out.String(); got != "" {
		t.Fatalf("an unarmed report wrote %q", got)
	}
}
