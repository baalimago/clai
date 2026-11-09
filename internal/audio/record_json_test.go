package audio

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/capture"
	"github.com/baalimago/clai/internal/jsonl"
	"github.com/baalimago/clai/internal/runenv"
	"github.com/baalimago/clai/internal/stdio"
	pkgtools "github.com/baalimago/clai/pkg/tools"
)

// stateRecorder reports states through OnState, then records until the stop
// channel closes, or fails with err.
type stateRecorder struct {
	recordStub
	states []capture.CaptureState
}

func (s *stateRecorder) Record(_ context.Context, req capture.Request) (capture.Recording, error) {
	for _, state := range s.states {
		req.OnState(state)
	}
	if s.err != nil {
		return capture.Recording{}, s.err
	}
	if req.Stop == nil {
		return capture.Recording{}, errors.New("no stop channel: input would not stop the recording")
	}
	select {
	case <-req.Stop:
	case <-time.After(5 * time.Second):
		return capture.Recording{}, errors.New("input did not stop the recording")
	}
	return capture.Recording{Path: "/r/a.wav", StoppedBecause: "enter"}, nil
}

type progressRecord struct {
	Type   string `json:"type"`
	Title  string `json:"title"`
	Row    string `json:"row"`
	State  string `json:"state"`
	Detail string `json:"detail"`
}

func jsonRecordEnv(stdin io.Reader, stdout, stderr io.Writer) runenv.Env {
	return runenv.New(runenv.Config{
		Streams: stdio.New(stdio.Config{Format: jsonl.FormatJSON, Stdin: stdin, Stdout: stdout, Stderr: stderr, StdinTerminal: true, StderrTerminal: true}),
		Format:  jsonl.FormatJSON,
	})
}

func progressRecords(t *testing.T, stderr string) []progressRecord {
	t.Helper()
	if strings.ContainsAny(stderr, "\r\x1b") {
		t.Fatalf("stderr draws: %q", stderr)
	}
	var records []progressRecord
	for line := range strings.SplitSeq(strings.TrimSuffix(stderr, "\n"), "\n") {
		var r progressRecord
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatalf("stderr line %q is not JSON: %v", line, err)
		}
		if r.Type != "progress" || r.Title != recordProgressTitle {
			t.Fatalf("stderr line %q is not an audio_record progress record", line)
		}
		records = append(records, r)
	}
	return records
}

func TestRecordText_JSONProgress(t *testing.T) {
	t.Run("one record per state change, stopped by input", func(t *testing.T) {
		dir := t.TempDir()
		setAudioConfig(t, map[string]any{"dir": dir, "max-seconds": 0, "silence-timeout": 0, "silence-peak-db": -40, "sinks": "none"})
		var out, errb bytes.Buffer
		env := jsonRecordEnv(enterAfter(50*time.Millisecond), &out, &errb)
		ctx := runenv.With(context.Background(), env)
		rec := &stateRecorder{states: []capture.CaptureState{capture.StateSilent, capture.StateSilent, capture.StateSound, capture.StateSound, capture.StateSilent}}

		result, err := recordText(ctx, pkgtools.AudioRecordCall{}, rec, env.Stdin(), env.StdinIsTerminal())
		if err != nil {
			t.Fatalf("recordText: %v", err)
		}
		if err := env.Streams().Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
		if !strings.Contains(result, "stopped_because: enter") {
			t.Fatalf("result = %q, want the input stop", result)
		}
		// R3-03: the calibration acknowledgement is a calibrate prompt
		// record, shown before the recording starts.
		first, rest, _ := strings.Cut(errb.String(), "\n")
		var ask struct {
			Type, Kind, Text string
			Choices          []string
		}
		if err := json.Unmarshal([]byte(first), &ask); err != nil || ask.Type != "prompt" || ask.Kind != "calibrate" ||
			len(ask.Choices) != 1 || ask.Choices[0] != "a" || strings.ContainsAny(ask.Text, "·[]") {
			t.Fatalf("first stderr record = %q (%v), want a plain calibrate prompt with choice a", first, err)
		}
		got := progressRecords(t, rest)
		want := []struct{ state, detail string }{
			{"start", dir + " · stops: [Enter]"},
			{"update", "silent · threshold -40 dB"},
			{"update", "sound · threshold -40 dB"},
			{"update", "silent · threshold -40 dB"},
			{"done", ""},
		}
		if len(got) != len(want) {
			t.Fatalf("records = %+v, want %d", got, len(want))
		}
		for i, w := range want {
			if got[i].State != w.state || got[i].Detail != w.detail {
				t.Fatalf("record %d = %+v, want state %q detail %q", i, got[i], w.state, w.detail)
			}
		}
		if out.Len() != 0 {
			t.Fatalf("stdout = %q, want empty", out.String())
		}
	})

	t.Run("a failed recording ends failed", func(t *testing.T) {
		setAudioConfig(t, map[string]any{"dir": t.TempDir(), "calibrated": true, "sinks": "none"})
		var errb bytes.Buffer
		env := jsonRecordEnv(strings.NewReader(""), io.Discard, &errb)
		ctx := runenv.With(context.Background(), env)
		rec := &stateRecorder{err: errors.New("ffmpeg exited")}

		if _, err := recordText(ctx, pkgtools.AudioRecordCall{}, rec, env.Stdin(), false); err == nil {
			t.Fatal("recordText: want the recorder's error")
		}
		if err := env.Streams().Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
		got := progressRecords(t, errb.String())
		if len(got) != 2 || got[0].State != "start" || got[1].State != "failed" || got[1].Detail != "ffmpeg exited" {
			t.Fatalf("records = %+v, want start then failed", got)
		}
	})
}
