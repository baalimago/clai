package voices_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/audio/voices"
	"github.com/baalimago/clai/internal/audio/voices/voicestest"
)

const source = "source.wav"

// unique adds a little noise, so that no two stretches of samples are alike.
func unique(samples []int16) []int16 {
	x := uint32(1)
	for i := range samples {
		x = x*1664525 + 1013904223
		samples[i] += int16(x >> 26)
	}
	return samples
}

// clipOf is the clip the fake ffmpeg encodes from samples.
func clipOf(samples []int16) []byte {
	clip, _ := binary.Append([]byte("fLaC"), binary.LittleEndian, samples)
	return clip
}

// answers is a Runner whose programs' answers a test changes.
type answers struct {
	*voicestest.Runner
	rewrite func(program, stdout string) string
	before  func(program string, args []string)
}

func (r answers) Run(ctx context.Context, program string, args ...string) (string, string, error) {
	if r.before != nil {
		r.before(program, args)
	}
	stdout, stderr, err := r.Runner.Run(ctx, program, args...)
	if r.rewrite != nil {
		stdout = r.rewrite(program, stdout)
	}
	return stdout, stderr, err
}

// lengthUnknown makes ffprobe give no duration, as it does for a stream.
func lengthUnknown(_, stdout string) string {
	if before, rest, found := strings.Cut(stdout, `"duration":"`); found {
		return before + `"duration":"N/A` + rest[strings.Index(rest, `"`):]
	}
	return stdout
}

func TestInspect_CutsToTheSample(t *testing.T) {
	samples := unique(voicestest.Tone(6, -20))
	for _, rewrite := range []func(string, string) string{nil, lengthUnknown} {
		inspector := voices.Inspector{Runner: answers{Runner: voicestest.NewRunner(), rewrite: rewrite}}
		inspector.Runner.(answers).Serve(source, samples)
		for cut, clip := range map[voices.Cut][]int16{
			{From: 1200 * time.Millisecond, To: 5 * time.Second}: samples[19200:80000],
			{From: 2 * time.Second}:                              samples[32000:],
			{}:                                                   samples,
		} {
			got, err := inspector.Inspect(context.Background(), source, cut)
			want := voices.Inspection{FLAC: clipOf(clip), Seconds: float64(len(clip)) / 16000, SpeechShare: 1, MeanDBFS: got.MeanDBFS}
			if err != nil || !reflect.DeepEqual(got, want) || math.Abs(got.MeanDBFS+20) > 0.1 {
				t.Fatalf("%+v: %v s at %v dBFS, %v; want the %d samples of the cut", cut, got.Seconds, got.MeanDBFS, err, len(clip))
			}
		}
		for cut, want := range map[voices.Cut]string{
			{From: -time.Second, To: 5 * time.Second}:      "the cut from -1 s to 5 s selects nothing from a file of 6 s",
			{From: 4 * time.Second, To: 4 * time.Second}:   "the cut from 4 s to 4 s selects nothing from a file of 6 s",
			{From: 5 * time.Second, To: 3 * time.Second}:   "the cut from 5 s to 3 s selects nothing from a file of 6 s",
			{From: 25 * time.Second}:                       "the cut from 25 s to the end selects nothing from a file of 6 s",
			{From: 1500 * time.Millisecond, To: time.Hour}: "the cut from 1.5 s to 3600 s selects nothing from a file of 6 s",
		} {
			got, err := inspector.Inspect(context.Background(), source, cut)
			if refusal := errAs[*voices.CutError](t, err); refusal.Cut != cut || refusal.Length != 6*time.Second || err.Error() != want || got.FLAC != nil {
				t.Fatalf("%+v: %v, want %s", cut, err, want)
			}
		}
	}
}

func TestInspect_StoredClipComesBackByteForByte(t *testing.T) {
	file := filepath.Join(t.TempDir(), "clip.flac")
	stored := clipOf(voicestest.Speech(3))
	put(t, file, string(stored))
	runner := voicestest.NewRunner()
	got, err := voices.Inspector{Runner: runner}.Inspect(context.Background(), file, voices.Cut{})
	encoded := slices.ContainsFunc(runner.Calls(), func(call voicestest.Call) bool { return slices.Contains(call.Args, "flac") })
	if err != nil || !bytes.Equal(got.FLAC, stored) || got.Seconds != 3 || encoded {
		t.Fatalf("a stored clip inspected whole: %v s, %v, encoded again: %v; want its own bytes", got.Seconds, err, encoded)
	}
}

func TestInspect_Limits(t *testing.T) {
	tone := func(seconds float64) []int16 { return voicestest.Tone(seconds, -20) }
	clipped := func(run int) []int16 {
		samples := tone(3)
		for i := range run {
			samples[1000+i] = []int16{math.MaxInt16, math.MinInt16}[i%2]
		}
		return samples
	}
	// hum is loud enough to be speech once in a while and quiet otherwise.
	hum := func(amplitude int16) []int16 {
		samples := make([]int16, 3*16000)
		for i := range samples {
			samples[i] = amplitude * int16(1-2*(i%2))
			if i%4000 == 0 {
				samples[i] = 600
			}
		}
		return samples
	}
	everything := voicestest.Silence(2)
	everything[16000], everything[16001], everything[16002] = math.MaxInt16, math.MaxInt16, math.MaxInt16
	for _, tc := range []struct {
		name    string
		samples []int16
		want    string
	}{
		{"3 s is long enough", tone(3), "3 s, speech 1, clipped 0, -20.0 dBFS"},
		{"10 s is short enough", tone(10), "10 s, speech 1, clipped 0, -20.0 dBFS"},
		{"too short", tone(1.5), "[length] clip too short: 1.5 s, a clip is 3–10 s"},
		{"a sample short of 3 s", tone(2.999), "[length] clip too short: 2.999 s, a clip is 3–10 s"},
		{"a sample over 10 s", tone(10.001), "[length] clip too long: 10.001 s, a clip is 3–10 s"},
		{"60% speech is enough", slices.Concat(tone(3), voicestest.Silence(2)), "5 s, speech 0.6, clipped 0, -22.2 dBFS"},
		{"59% speech", slices.Concat(tone(2.95), voicestest.Silence(2.05)), "[speech] too little speech: 59% of the clip is speech, a clip needs 60%"},
		{"a pause under 0.3 s is speech", slices.Concat(tone(2), voicestest.Silence(0.2), tone(2)), "4.2 s, speech 1, clipped 0, -20.2 dBFS"},
		{"a tone under -35 dBFS is no speech", voicestest.Tone(3, -36), "[speech] too little speech: 0% of the clip is speech, a clip needs 60%"},
		{"two samples at full scale", clipped(2), "3 s, speech 1, clipped 2, -20.0 dBFS"},
		{"three in a row", clipped(3), "[clipping] clipped: 3 consecutive samples at full scale, a clip allows 2"},
		{"loud enough", hum(347), "3 s, speech 1, clipped 0, -39.5 dBFS"},
		{"too quiet", hum(309), "[loudness] too quiet: mean volume -40.5 dBFS, a clip needs -40 dBFS"},
		{"every check failed", everything, "[length speech clipping loudness] clip too short: 2.0 s, a clip is 3–10 s; " +
			"too little speech: 0% of the clip is speech, a clip needs 60%; clipped: 3 consecutive samples at full scale, a clip allows 2; " +
			"too quiet: mean volume -40.3 dBFS, a clip needs -40 dBFS"},
	} {
		runner := voicestest.NewRunner()
		runner.Serve(source, tc.samples)
		got, err := voices.Inspector{Runner: runner}.Inspect(context.Background(), source, voices.Cut{})
		text := fmt.Sprintf("%v s, speech %v, clipped %d, %.1f dBFS", got.Seconds, got.SpeechShare, got.ClippedRun, got.MeanDBFS)
		if refusal, refused := errors.AsType[*voices.ClipQualityError](err); refused && reflect.DeepEqual(got, voices.Inspection{}) {
			text = fmt.Sprint(refusal.Failed, " ", err)
		}
		if text != tc.want {
			t.Errorf("%s: %s (%v)\nwant %s", tc.name, text, err, tc.want)
		}
	}
}

func TestInspect_MissingToolStopsBeforeAnythingRuns(t *testing.T) {
	for want, missing := range map[string][]string{"ffmpeg": {"ffprobe", "ffmpeg"}, "ffprobe": {"ffprobe"}} {
		runner := voicestest.NewRunner()
		runner.Serve(source, voicestest.Speech(3))
		runner.Missing(missing...)
		_, err := voices.Inspector{Runner: runner}.Inspect(context.Background(), source, voices.Cut{})
		if got := errAs[*voices.MissingToolError](t, err); got.Tool != want || err.Error() != want+" is not installed; inspecting a clip needs ffmpeg and ffprobe" || len(runner.Calls()) != 0 {
			t.Fatalf("without %v: %v after %v", missing, err, runner.Calls())
		}
	}
}

func TestInspect_FileThatIsNoAudio(t *testing.T) {
	dir := t.TempDir()
	file, locked := filepath.Join(dir, "meeting.wav"), filepath.Join(dir, "locked")
	put(t, filepath.Join(locked, ".keep"), "")
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
	// A case scripts stderr, or one program's stdout, or the temporary directory.
	for name, tc := range map[string]struct {
		stderr, program, stdout, tmp, detail string
		cause                                error
	}{
		"no such file":                             {detail: file + ": No such file or directory"},
		"the program's last line, of text only":    {stderr: "Opening\n\x00Invalid data\xff " + strings.Repeat("x", 300), detail: "Invalid data " + strings.Repeat("x", 227)},
		"a file with no audio stream":              {program: "ffprobe", stdout: `{"streams":[],"format":{}}`, detail: "no audio stream"},
		"a description that cannot be read":        {program: "ffprobe", stdout: "Input #0", detail: "ffprobe gave no readable description"},
		"decoded audio that ends in half a sample": {program: "ffmpeg", stdout: "x", detail: "ffmpeg's decoded audio ends in half a sample"},
		"no temporary directory":                   {tmp: filepath.Join(dir, "absent"), cause: fs.ErrNotExist},
		"a temporary directory it cannot remove":   {tmp: locked, cause: fs.ErrPermission},
	} {
		runner := answers{Runner: voicestest.NewRunner()}
		runner.rewrite = func(program, stdout string) string {
			if program == tc.program {
				return tc.stdout
			}
			return stdout
		}
		runner.before = func(_ string, args []string) {
			if tc.tmp != "" && slices.Contains(args, "flac") {
				ok(t, os.Chmod(locked, 0o555))
			}
		}
		if tc.stderr != "" {
			runner.Fail(file, tc.stderr)
		} else if tc.program != "" || tc.tmp != "" {
			runner.Serve(file, voicestest.Speech(3))
		}
		t.Setenv("TMPDIR", tc.tmp)
		got, err := voices.Inspector{Runner: runner}.Inspect(context.Background(), file, voices.Cut{})
		refusal := errAs[*voices.SourceError](t, err)
		if tc.cause != nil {
			tc.detail = refusal.Detail
		}
		if refusal.File != file || refusal.Detail != tc.detail || err.Error() != `read "`+file+`" as audio: `+tc.detail || tc.cause != nil && !errors.Is(err, tc.cause) || got.FLAC != nil {
			t.Errorf("%s: %v\nwant the detail %q and the cause %v", name, err, tc.detail, tc.cause)
		}
	}
}

// cancelled inspects a file whose programs hang, and cancels at their first
// run.
func cancelled(runner *voicestest.Runner, file string) error {
	runner.Hang(file)
	ctx, cancel := context.WithCancel(context.Background())
	hanging := answers{Runner: runner, before: func(string, []string) { cancel() }}
	_, err := voices.Inspector{Runner: hanging}.Inspect(ctx, file, voices.Cut{})
	return err
}

func TestInspect_EndsWithItsContext(t *testing.T) {
	if err := cancelled(voicestest.NewRunner(), source); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled while a program runs: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	runner := voicestest.NewRunner()
	runner.Serve(source, voicestest.Speech(3))
	if _, err := (voices.Inspector{Runner: runner}).Inspect(ctx, source, voices.Cut{}); !errors.Is(err, context.Canceled) || len(runner.Calls()) != 0 {
		t.Fatalf("cancelled before the call: %v after %v", err, runner.Calls())
	}
}

func TestInspect_LeavesNoFileAndItsErrorsCarryNoAudio(t *testing.T) {
	e := newEnv(t)
	t.Setenv("TMPDIR", filepath.Join(e.cfg, "tmp"))
	put(t, filepath.Join(e.cfg, "tmp", ".keep"), "")
	speech := unique(voicestest.Speech(4))
	runner := voicestest.NewRunner()
	meeting, quiet, notes := filepath.Join(e.cfg, "meeting.wav"), filepath.Join(e.cfg, "quiet.wav"), filepath.Join(e.cfg, "notes.txt")
	runner.Serve(meeting, speech)
	runner.Serve(quiet, voicestest.Silence(4))
	runner.Fail(notes, "Invalid data found when processing input")
	before := e.files()

	inspector := voices.Inspector{Runner: runner}
	accepted, err := inspector.Inspect(context.Background(), meeting, voices.Cut{From: time.Second})
	if err != nil || accepted.Seconds != 3 {
		t.Fatalf("Inspect = %v s, %v", accepted.Seconds, err)
	}
	_, refused := inspector.Inspect(context.Background(), quiet, voices.Cut{})
	_, unreadable := inspector.Inspect(context.Background(), notes, voices.Cut{})
	e.unchanged(before)
	audio := clipOf(speech)
	for _, err := range []error{refused, unreadable, cancelled(runner, meeting)} {
		if err == nil {
			t.Fatal("an inspection that should fail returned no error")
		}
		for i := 0; i+8 <= len(audio); i += 64 {
			if strings.Contains(err.Error(), string(audio[i:i+8])) {
				t.Fatalf("the error %q carries audio", err)
			}
		}
	}
}
