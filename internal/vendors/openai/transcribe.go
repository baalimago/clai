package openai

import (
	"strings"

	"github.com/baalimago/clai/internal/audio/generic"
	"github.com/baalimago/clai/internal/diag"
)

const TranscribeURL = "https://api.openai.com/v1/audio/transcriptions"

var TranscriberDefault = Transcriber{
	Model: "whisper-1",
}

type Transcriber struct {
	generic.Transcriber
	Model string `json:"model"`
}

// ResponseFormatFor picks the richest format the model accepts; the
// gpt-transcribe family rejects everything but json and text (architecture/audio.md).
func ResponseFormatFor(model string) generic.ResponseFormat {
	switch {
	case strings.Contains(model, "diarize"):
		return generic.ResponseFormatDiarizedJSON
	case strings.Contains(model, "whisper"):
		return generic.ResponseFormatVerboseJSON
	default:
		return generic.ResponseFormatJSON
	}
}

func (t *Transcriber) Setup() error {
	err := t.Transcriber.Setup("OPENAI_API_KEY", TranscribeURL, "DEBUG_OPENAI")
	if err != nil {
		return diag.Errorf("failed to setup transcriber: %w", err)
	}
	t.Transcriber.Model = t.Model
	t.Transcriber.Format = ResponseFormatFor(t.Model)
	return nil
}
