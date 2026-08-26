package audio

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// TranscribeConfig is the transcribe verb's section of audioConfig.json.
type TranscribeConfig struct {
	Model        string `json:"model"`
	OutputFormat string `json:"output-format"`
	Parallelism  int    `json:"parallelism"`
}

// Configurations is the audioConfig.json schema.
type Configurations struct {
	Transcribe TranscribeConfig `json:"transcribe"`
}

var Default = Configurations{
	Transcribe: TranscribeConfig{
		Model:        "whisper-1",
		OutputFormat: string(FormatVTT),
		Parallelism:  3,
	},
}

// MockTranscriber returns deterministic segments without network access,
// mirroring the text 'test' mock model for e2e tests. Diarized adds speaker
// labels, emulating a diarize-capable model.
type MockTranscriber struct {
	Diarized bool
}

func (m MockTranscriber) Transcribe(_ context.Context, filePath string) ([]Segment, error) {
	if _, err := os.Stat(filePath); err != nil {
		return nil, fmt.Errorf("failed to stat audio file: %w", err)
	}
	segs := []Segment{
		{Start: 0, End: 1500 * time.Millisecond, Text: "mock transcription"},
		{Start: 1500 * time.Millisecond, End: 3 * time.Second, Text: "of an audio file"},
	}
	if m.Diarized {
		segs[0].Speaker = "A"
		segs[1].Speaker = "B"
	}
	return segs, nil
}

// TranscribeQuerier implements models.Querier: transcribe (splitting when
// oversized), render, write the transcript to Out (stdout in production).
type TranscribeQuerier struct {
	Splitter *Splitter
	FilePath string
	Format   OutputFormat
	Out      io.Writer
	// Cleanup removes temp input (stdin '-' mode); always runs after Query
	Cleanup func()
}

func (q *TranscribeQuerier) Query(ctx context.Context) error {
	if q.Cleanup != nil {
		defer q.Cleanup()
	}
	segs, err := q.Splitter.Transcribe(ctx, q.FilePath)
	if err != nil {
		return fmt.Errorf("failed to transcribe %v: %w", q.FilePath, err)
	}
	rendered, err := Render(segs, q.Format)
	if err != nil {
		return fmt.Errorf("failed to render transcript: %w", err)
	}
	if rendered != "" && !strings.HasSuffix(rendered, "\n") {
		rendered += "\n"
	}
	out := q.Out
	if out == nil {
		out = os.Stdout
	}
	if _, err := io.WriteString(out, rendered); err != nil {
		return fmt.Errorf("failed to write transcript: %w", err)
	}
	return nil
}

// SuppressCompletionNotification keeps the completion bell off stdout so
// piped transcripts stay clean.
func (q *TranscribeQuerier) SuppressCompletionNotification() bool {
	return true
}
