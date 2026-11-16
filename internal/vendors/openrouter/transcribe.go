package openrouter

import (
	"context"
	"fmt"
	"strings"

	"github.com/baalimago/clai/internal/audio/generic"
	"github.com/baalimago/clai/internal/diag"
)

const TranscribeURL = "https://openrouter.ai/api/v1/audio/transcriptions"

// DiarizeSuffix asks an OpenRouter model for speaker labels (architecture/audio.md).
const DiarizeSuffix = "+diarize"

var TranscriberDefault = Transcriber{
	Model: "or:openai/whisper-1",
}

type Transcriber struct {
	generic.Transcriber
	Model       string `json:"model"`
	diarization generic.Diarization
}

func (t *Transcriber) Setup() error {
	err := t.Transcriber.Setup("OPENROUTER_API_KEY", TranscribeURL, "DEBUG_OPENROUTER")
	if err != nil {
		return diag.Errorf("failed to setup transcriber: %w", err)
	}
	slug, diarize := strings.CutSuffix(strings.TrimPrefix(t.Model, "or:"), DiarizeSuffix)
	if strings.TrimSpace(slug) == "" {
		return fmt.Errorf("openrouter transcribe model %q names no model, want or:<slug> or or:<slug>%v", t.Model, DiarizeSuffix)
	}
	if strings.Contains(strings.ToLower(slug), DiarizeSuffix) {
		return fmt.Errorf("openrouter transcribe model %q: %v must end the name, as in or:<slug>[:variant]%v", t.Model, DiarizeSuffix, DiarizeSuffix)
	}
	t.Transcriber.Model = slug
	t.Transcriber.Format = generic.ResponseFormatVerboseJSON
	t.Transcriber.ExtraHeaders = map[string]string{
		"HTTP-Referer":       "clai",
		"X-OpenRouter-Title": "clai",
	}
	t.diarization = generic.DiarizationNone
	if diarize {
		t.Transcriber.Fields = map[string]string{"diarize": "true"}
		t.diarization = generic.DiarizationRequestLocal
	}
	return nil
}

// Diarization reports request-local speaker labels for a +diarize model.
func (t *Transcriber) Diarization() generic.Diarization { return t.diarization }

func (t *Transcriber) Transcribe(ctx context.Context, filePath string) ([]generic.Segment, error) {
	if t.diarization != generic.DiarizationRequestLocal {
		return t.Transcriber.Transcribe(ctx, filePath)
	}
	body, err := t.Transcriber.Request(ctx, filePath)
	if err != nil {
		return nil, err
	}
	return ParseDiarized(body)
}
