package audio

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/baalimago/clai/internal/audio/generic"
	"github.com/baalimago/clai/internal/diag"
	"github.com/baalimago/clai/internal/modelalias"
	"github.com/baalimago/clai/internal/runenv"
)

// TranscribeConfig is the transcribe verb's section of audioConfig.json.
type TranscribeConfig struct {
	Model        string `json:"model"`
	OutputFormat string `json:"output-format"`
	// Parallelism caps the parallel requests of a split file; 0, absent or
	// negative is unset (architecture/audio.md, Configuration)
	Parallelism int `json:"parallelism"`
	// Request budgets for calibrated diarization; zero means the default,
	// negative is a load-time error (see architecture/audio.md)
	MaxRequestBytes   int64 `json:"max-request-bytes"`
	MaxRequestSeconds int   `json:"max-request-seconds"`
	// MaxVoices caps the people a run names when nobody is named; 0 names
	// nobody by default, negative is a load-time error
	MaxVoices int `json:"max-voices"`
}

// MaxVoicesError refuses a negative transcribe.max-voices.
type MaxVoicesError struct{ Value int }

func (e *MaxVoicesError) Error() string {
	return fmt.Sprintf("transcribe.max-voices must not be negative, got: %v; 0 names nobody unless -voices or the tool call names people", e.Value)
}

func checkMaxVoices(c TranscribeConfig) error {
	if c.MaxVoices < 0 {
		return &MaxVoicesError{Value: c.MaxVoices}
	}
	return nil
}

// Budgets are the resolved request limits every calibrated request is
// checked against before upload.
type Budgets struct {
	MaxRequestBytes    int64
	MaxRequestDuration time.Duration
}

// ResolveBudgets applies the zero-means-default, negative-is-error rule.
func ResolveBudgets(c TranscribeConfig) (Budgets, error) {
	b := Budgets{
		MaxRequestBytes:    Default.Transcribe.MaxRequestBytes,
		MaxRequestDuration: time.Duration(Default.Transcribe.MaxRequestSeconds) * time.Second,
	}
	switch {
	case c.MaxRequestBytes < 0:
		return Budgets{}, fmt.Errorf("transcribe.max-request-bytes must not be negative, got: %v", c.MaxRequestBytes)
	case c.MaxRequestSeconds < 0:
		return Budgets{}, fmt.Errorf("transcribe.max-request-seconds must not be negative, got: %v", c.MaxRequestSeconds)
	}
	if c.MaxRequestBytes > 0 {
		b.MaxRequestBytes = c.MaxRequestBytes
	}
	if c.MaxRequestSeconds > 0 {
		b.MaxRequestDuration = time.Duration(c.MaxRequestSeconds) * time.Second
	}
	return b, nil
}

// Configurations is the audioConfig.json schema.
type Configurations struct {
	Transcribe TranscribeConfig `json:"transcribe"`
	// ModelAliases maps a verb such as "fast" to the ordered transcription
	// models a run tries in its place. A model the table does not name runs as
	// itself (architecture/model-aliases.md).
	ModelAliases modelalias.Table `json:"model-aliases"`
	Capture      CaptureConfig    `json:"capture"`
}

var Default = Configurations{
	Transcribe: TranscribeConfig{
		Model:             "whisper-1",
		OutputFormat:      string(FormatVTT),
		MaxRequestBytes:   MaxRequestBytes,
		MaxRequestSeconds: 1400,
		MaxVoices:         12,
	},
	// One ladder, one model per transcribe route, so a machine with either
	// key still transcribes (architecture/model-aliases.md).
	ModelAliases: modelalias.Table{
		"fast": {"whisper-1", "or:openai/whisper-1"},
	},
	Capture: CaptureConfig{
		Dir:            "~/Recordings",
		MaxSeconds:     5400,
		SilenceTimeout: 600,
		SilencePeakDB:  -40,
		Sinks:          "default",
		Mic:            new(true),
	},
}

// MockTranscriber returns deterministic segments without network access,
// mirroring the text 'test' mock model for e2e tests. Diarized adds speaker
// labels, emulating a diarize-capable model. Delay spreads the answer over
// that long, each segment heard in turn, as a streaming provider answers.
type MockTranscriber struct {
	Diarized bool
	Delay    time.Duration
	// script, when set, answers the run's calls in turn.
	script *mockScript
}

// mockScriptEnv names a JSON file of replies the mock transcriber answers a
// run's Transcribe calls with, one reply per call in turn.
const mockScriptEnv = "CLAI_MOCK_TRANSCRIBE_SCRIPT"

// MockScriptError says the mock transcriber's script cannot be used: Path is
// the value of CLAI_MOCK_TRANSCRIBE_SCRIPT, Err why.
type MockScriptError struct {
	Path string
	Err  error
}

func (e *MockScriptError) Error() string {
	return fmt.Sprintf("%v=%q is not a script of replies, a JSON array of replies, each an array of {start, end, speaker, text}: %v", mockScriptEnv, e.Path, e.Err)
}

func (e *MockScriptError) Unwrap() error { return e.Err }

// mockScript is one transcription run's replies and how many it gave.
type mockScript struct {
	mu      sync.Mutex
	replies [][]Segment
	given   int
	// naming holds the replies kept for the naming orders not yet asked.
	naming map[Order]int
}

// loadMockScript reads CLAI_MOCK_TRANSCRIBE_SCRIPT; unset is no script.
func loadMockScript() (*mockScript, error) {
	path := os.Getenv(mockScriptEnv)
	if path == "" {
		return nil, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, &MockScriptError{Path: path, Err: err}
	}
	var replies [][]renderSegment
	if err := json.Unmarshal(raw, &replies); err != nil {
		return nil, &MockScriptError{Path: path, Err: err}
	}
	script := &mockScript{replies: make([][]Segment, len(replies))}
	for i, reply := range replies {
		for _, s := range reply {
			script.replies[i] = append(script.replies[i], Segment{
				Start: generic.SecondsToDuration(s.Start), End: generic.SecondsToDuration(s.End), Speaker: s.Speaker, Text: s.Text,
			})
		}
	}
	return script, nil
}

// next is the reply of the run's next call; false once every reply was given.
// The naming's two requests, sent at once, take the next two replies in
// order, forward then reversed, whichever of them arrives first.
func (s *mockScript) next(path string) ([]Segment, bool) {
	if s == nil {
		return nil, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	at := s.given
	if order, ok := namingOrderOf(path); ok {
		// An order already given starts the next naming's pair.
		if _, kept := s.naming[order]; !kept {
			s.naming = map[Order]int{OrderForward: s.given, OrderReversed: s.given + 1}
			s.given += 2
		}
		at = s.naming[order]
		if delete(s.naming, order); len(s.naming) == 0 {
			s.naming = nil
		}
	} else if at < len(s.replies) {
		s.given++
	}
	if at >= len(s.replies) {
		return nil, false
	}
	return slices.Clone(s.replies[at]), true
}

// mockDelayEnv slows the mock transcriber, so a test can watch a
// transcription's progress.
const mockDelayEnv = "CLAI_MOCK_TRANSCRIBE_DELAY"

func mockTranscribeDelay() (time.Duration, error) {
	raw := os.Getenv(mockDelayEnv)
	if raw == "" {
		return 0, nil
	}
	delay, err := time.ParseDuration(raw)
	if err != nil {
		return 0, diag.Errorf("%v=%q is not a duration: %w", mockDelayEnv, raw, err)
	}
	if delay < 0 {
		return 0, fmt.Errorf("%v=%q must not be negative", mockDelayEnv, raw)
	}
	return delay, nil
}

// Diarization emulates the diarize family's anchored labels.
func (m MockTranscriber) Diarization() generic.Diarization {
	if m.Diarized {
		return generic.DiarizationAnchored
	}
	return generic.DiarizationNone
}

func (m MockTranscriber) Transcribe(ctx context.Context, filePath string) ([]Segment, error) {
	if _, err := os.Stat(filePath); err != nil {
		return nil, diag.Errorf("failed to stat audio file: %w", err)
	}
	if reply, scripted := m.script.next(filePath); scripted {
		return reply, nil
	}
	segs := []Segment{
		{Start: 0, End: 1500 * time.Millisecond, Text: "mock transcription"},
		{Start: 1500 * time.Millisecond, End: 3 * time.Second, Text: "of an audio file"},
	}
	if m.Diarized {
		segs[0].Speaker = "A"
		segs[1].Speaker = "B"
	}
	if m.Delay <= 0 {
		return segs, nil
	}
	for _, seg := range segs {
		select {
		case <-ctx.Done():
			return nil, diag.Errorf("mock transcription aborted: %w", ctx.Err())
		case <-time.After(m.Delay / time.Duration(len(segs))):
		}
		generic.ReportHeard(ctx, seg.End)
	}
	return segs, nil
}

// TranscribeQuerier implements models.Querier: transcribe (splitting when
// oversized), name the speakers the voice library knows, render, write the
// transcript to Out (stdout in production).
type TranscribeQuerier struct {
	Splitter *Splitter
	FilePath string
	Format   OutputFormat
	Out      io.Writer
	// known is whom the transcript is named after; the zero value names
	// nobody.
	known knownVoices
}

func (q *TranscribeQuerier) Query(ctx context.Context) error {
	segs, err := q.Splitter.Transcribe(ctx, q.FilePath)
	if err != nil {
		return diag.Errorf("failed to transcribe %v: %w", q.FilePath, err)
	}
	rendered, warnings, err := q.render(ctx, segs)
	if err != nil {
		return diag.Errorf("failed to render transcript: %w", err)
	}
	if rendered != "" && !strings.HasSuffix(rendered, "\n") {
		rendered += "\n"
	}
	out := q.Out
	if out == nil {
		out = runenv.From(ctx).Stdout()
	}
	if _, err := io.WriteString(out, rendered); err != nil {
		return diag.Errorf("failed to write transcript: %w", err)
	}
	for _, msg := range warnings {
		runenv.Warnf(ctx, runenv.SubsystemAudio, "%s", msg)
	}
	return nil
}

// render is the plain rendering when no voice is selected, else the
// transcript named after the selection and the warnings of its naming.
func (q *TranscribeQuerier) render(ctx context.Context, segs []Segment) (string, []string, error) {
	if q.known.selection.Empty() {
		rendered, err := Render(segs, q.Format)
		return rendered, nil, err
	}
	naming := q.known.name(ctx, q.Splitter, q.FilePath, segs)
	rendered, err := renderNaming(naming, q.Format)
	if !q.known.warn {
		return rendered, nil, err
	}
	return rendered, namingWarnings(naming.Report), err
}

// SuppressCompletionNotification keeps the completion bell off stdout so
// piped transcripts stay clean.
func (q *TranscribeQuerier) SuppressCompletionNotification() bool {
	return true
}
