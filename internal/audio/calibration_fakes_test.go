package audio

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// scriptedRunner emulates ffprobe/ffmpeg for the calibration pipeline: it
// parses the arguments the planner and assembler pass and writes the files
// they expect, so the tests never need ffmpeg installed.
type scriptedRunner struct {
	mu       sync.Mutex
	calls    [][]string
	duration string
	// silences is the stderr the silencedetect pass returns
	silences string
	// encodedBytes is the size of the "encoded" file; 0 means 1 KiB per second
	encodedBytes int64
	// failOn makes the first ffmpeg call containing this arg fail with stderr
	failOn     string
	failStderr string
	// blockOn makes the ffmpeg call containing this arg wait for ctx
	blockOn  string
	probeErr error
	// fill is the byte written to extracted PCM; distinguishes sources in tests
	fill byte
}

var atrimRe = regexp.MustCompile(`atrim=start=([0-9.]+):end=([0-9.]+)`)

func (r *scriptedRunner) LookPath(name string) (string, error) { return "/usr/bin/" + name, nil }

func (r *scriptedRunner) Run(ctx context.Context, name string, args ...string) (string, string, error) {
	r.mu.Lock()
	r.calls = append(r.calls, append([]string{name}, args...))
	r.mu.Unlock()
	joined := strings.Join(args, " ")
	if name == "ffprobe" {
		if r.probeErr != nil {
			return "", "ffprobe: boom", r.probeErr
		}
		return r.duration, "", nil
	}
	if r.blockOn != "" && strings.Contains(joined, r.blockOn) {
		<-ctx.Done()
		return "", "", ctx.Err()
	}
	if r.failOn != "" && strings.Contains(joined, r.failOn) {
		return "", r.failStderr, fmt.Errorf("exit status 1")
	}
	switch {
	case strings.Contains(joined, "silencedetect"):
		return "", r.silences, nil
	case strings.Contains(joined, "atrim="):
		m := atrimRe.FindStringSubmatch(joined)
		start, _ := strconv.ParseFloat(m[1], 64)
		end, _ := strconv.ParseFloat(m[2], 64)
		n := samplesOf(time.Duration((end - start) * float64(time.Second)))
		// Content depends on the source interval so distinct requests hash
		// differently, as real audio would
		pattern := []byte(m[1] + ":" + m[2])
		buf := make([]byte, n*2)
		for i := range buf {
			buf[i] = pattern[i%len(pattern)] ^ r.fill
		}
		return "", "", os.WriteFile(args[len(args)-1], buf, 0o600)
	case strings.Contains(joined, "libmp3lame"):
		size := r.encodedBytes
		if size == 0 {
			in, err := os.Stat(args[indexOf(args, "-i")+1])
			if err != nil {
				return "", "", err
			}
			size = in.Size() / (2 * sampleRate) * 1024
		}
		return "", "", os.WriteFile(args[len(args)-1], make([]byte, size), 0o600)
	}
	return "", "", nil
}

func (r *scriptedRunner) count(name, containing string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, c := range r.calls {
		if c[0] == name && strings.Contains(strings.Join(c[1:], " "), containing) {
			n++
		}
	}
	return n
}

func indexOf(args []string, want string) int {
	for i, a := range args {
		if a == want {
			return i
		}
	}
	return -1
}

// silenceLines renders silencedetect stderr for the given [start,end] pairs.
func silenceLines(pairs ...float64) string {
	var b strings.Builder
	for i := 0; i+1 < len(pairs); i += 2 {
		fmt.Fprintf(&b, "[silencedetect @ 0x1] silence_start: %.3f\n", pairs[i])
		fmt.Fprintf(&b, "[silencedetect @ 0x1] silence_end: %.3f | silence_duration: %.3f\n", pairs[i+1], pairs[i+1]-pairs[i])
	}
	return b.String()
}

func defaultBudgets() Budgets {
	b, err := ResolveBudgets(Default.Transcribe)
	if err != nil {
		panic(err)
	}
	return b
}
