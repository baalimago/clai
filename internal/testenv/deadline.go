package testenv

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"time"
)

// reportMargin is how long before the -test.timeout alarm the deadline report
// is written: late enough that a passing binary never prints it, early enough
// to land above the alarm's panic.
const reportMargin = time.Second

// busyShare is the fraction of the processor time GOMAXPROCS offered over the
// run above which the binary itself, not the host, spent the budget.
const busyShare = 0.6

// starvedShare is the fraction of the wall time the binary's threads may spend
// on a run queue before the host, not the binary, is what spent the budget. The
// one-minute load average lags a 30 s gate too much to tell: on 2026-10-28 three
// binaries at half their processors read load 17 to 19 on 22 CPUs.
const starvedShare = 0.25

// taskDir lists the process's threads; each thread's schedstat holds the time
// it ran, the time it waited on a run queue, both in nanoseconds, and its
// timeslice count.
const taskDir = "/proc/self/task"

// verdict is why a binary is about to outlive its timeout.
type verdict int

const (
	busy verdict = iota
	starved
	idle
	waiting
)

func (v verdict) String() string {
	switch v {
	case busy:
		return "busy: the binary ran on its processors with little waiting, so its tests exceed the budget"
	case starved:
		return "starved: the binary's threads waited for a CPU, the host is oversubscribed"
	case idle:
		return "idle: the binary waited on something other than CPU, a hang, a sleep or an external process"
	case waiting:
		return "waiting: the binary waited for CPU or for an event, host load unknown"
	}
	return fmt.Sprintf("verdict(%d)", int(v))
}

// budget is what a binary has used of its timeout at one instant.
type budget struct {
	wall, timeout time.Duration
	cpu           time.Duration
	wait          time.Duration
	waitKnown     bool
	procs, cpus   int
	load          float64
	loadKnown     bool
	goroutines    int
	gcs           int64
	gcPause       time.Duration
}

// available is the processor time GOMAXPROCS offered over the run.
func (b budget) available() time.Duration { return time.Duration(b.procs) * b.wall }

func (b budget) verdict() verdict {
	switch {
	case b.waitKnown && b.wait >= max(time.Second, time.Duration(starvedShare*float64(b.wall))):
		return starved
	case !b.waitKnown && b.loadKnown && b.load >= float64(b.cpus):
		return starved
	}
	if avail := b.available(); avail > 0 && float64(b.cpu) >= busyShare*float64(avail) {
		return busy
	}
	if !b.waitKnown && !b.loadKnown {
		return waiting
	}
	return idle
}

func (b budget) String() string {
	wait := "run-queue wait unknown"
	if b.waitKnown {
		wait = fmt.Sprintf("%.1fs waiting for a CPU", b.wait.Seconds())
	}
	load := "unknown"
	if b.loadKnown {
		load = strconv.FormatFloat(b.load, 'f', 1, 64)
	}
	return fmt.Sprintf("testenv: %s; %.1fs of the %s budget gone with %.1fs CPU of %.1fs available, %s (GOMAXPROCS %d, %d CPUs, load %s); %d goroutines; %d GCs paused %s",
		b.verdict(), b.wall.Seconds(), b.timeout, b.cpu.Seconds(), b.available().Seconds(), wait,
		b.procs, b.cpus, load, b.goroutines, b.gcs, b.gcPause.Round(time.Millisecond))
}

// sample measures the binary. The error names what could not be measured,
// which reads as zero CPU or an unknown load in the budget.
func sample(start time.Time, timeout time.Duration) (budget, error) {
	var gc debug.GCStats
	debug.ReadGCStats(&gc)
	b := budget{
		wall:       time.Since(start),
		timeout:    timeout,
		procs:      runtime.GOMAXPROCS(0),
		cpus:       runtime.NumCPU(),
		goroutines: runtime.NumGoroutine(),
		gcs:        gc.NumGC,
		gcPause:    gc.PauseTotal,
	}
	cpu, cpuErr := processCPU()
	b.cpu = cpu
	wait, waitErr := runQueueWait(taskDir)
	if waitErr == nil {
		b.wait, b.waitKnown = wait, true
	}
	load, loadErr := loadAverageAt(loadAveragePath)
	if loadErr == nil {
		b.load, b.loadKnown = load, true
	}
	return b, errors.Join(cpuErr, waitErr, loadErr)
}

// runQueueWait sums the run-queue wait of the process's live threads from a
// /proc/<pid>/task-shaped directory; a thread that exits mid-read is skipped.
func runQueueWait(taskDir string) (time.Duration, error) {
	threads, err := os.ReadDir(taskDir)
	if err != nil {
		return 0, fmt.Errorf("list threads: %w", err)
	}
	var wait time.Duration
	for _, thread := range threads {
		raw, err := os.ReadFile(filepath.Join(taskDir, thread.Name(), "schedstat"))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return 0, fmt.Errorf("read schedstat of thread %s: %w", thread.Name(), err)
		}
		fields := strings.Fields(string(raw))
		if len(fields) < 2 {
			return 0, fmt.Errorf("schedstat of thread %s has %d fields, want 3", thread.Name(), len(fields))
		}
		ns, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			return 0, fmt.Errorf("parse run-queue wait of thread %s: %w", thread.Name(), err)
		}
		wait += time.Duration(ns)
	}
	return wait, nil
}

// loadAveragePath is where Linux publishes the load averages.
const loadAveragePath = "/proc/loadavg"

// loadAverageAt reads the one-minute load average from a /proc/loadavg-shaped file.
func loadAverageAt(path string) (float64, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, fmt.Errorf("read load average: %w", err)
	}
	first, _, _ := strings.Cut(strings.TrimSpace(string(raw)), " ")
	load, err := strconv.ParseFloat(first, 64)
	if err != nil {
		return 0, fmt.Errorf("parse load average %q: %w", first, err)
	}
	return load, nil
}

// timeoutFlag is the -test.timeout the go command gave the binary.
func timeoutFlag() (time.Duration, error) {
	if !flag.Parsed() {
		flag.Parse()
	}
	f := flag.Lookup("test.timeout")
	if f == nil {
		return 0, errors.New("the binary has no test.timeout flag")
	}
	getter, ok := f.Value.(flag.Getter)
	if !ok {
		return 0, fmt.Errorf("test.timeout flag value %T is not a flag.Getter", f.Value)
	}
	d, ok := getter.Get().(time.Duration)
	if !ok {
		return 0, fmt.Errorf("test.timeout flag holds %T, want a duration", getter.Get())
	}
	return d, nil
}

// disarm is what stopping a deadline report found.
type disarm int

const (
	neverArmed disarm = iota
	disarmed
	alreadyFired
)

type deadlineReport struct{ timer *time.Timer }

func (r *deadlineReport) stop() disarm {
	switch {
	case r.timer == nil:
		return neverArmed
	case r.timer.Stop():
		return disarmed
	}
	return alreadyFired
}

// armDeadlineReport writes one budget line to w margin before start+timeout
// when that moment is still ahead; a disabled timeout arms nothing.
func armDeadlineReport(w io.Writer, start time.Time, timeout, margin time.Duration) *deadlineReport {
	wait := timeout - margin - time.Since(start)
	if timeout <= 0 || wait <= 0 {
		return &deadlineReport{}
	}
	return &deadlineReport{timer: time.AfterFunc(wait, func() {
		b, err := sample(start, timeout)
		line := b.String()
		if err != nil {
			line += "; unmeasured: " + err.Error()
		}
		fmt.Fprintln(w, line)
	})}
}
