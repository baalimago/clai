package openrouter

import (
	"fmt"
	"strings"

	"github.com/baalimago/clai/internal/audio/generic"
)

const TranscribeURL = "https://openrouter.ai/api/v1/audio/transcriptions"

var TranscriberDefault = Transcriber{
	Model: "or:openai/whisper-1",
}

type Transcriber struct {
	generic.Transcriber
	Model string `json:"model"`
}

func (t *Transcriber) Setup() error {
	err := t.Transcriber.Setup("OPENROUTER_API_KEY", TranscribeURL, "DEBUG_OPENROUTER")
	if err != nil {
		return fmt.Errorf("failed to setup transcriber: %w", err)
	}
	t.Transcriber.Model = strings.TrimPrefix(t.Model, "or:")
	t.Transcriber.ExtraHeaders = map[string]string{
		"HTTP-Referer":       "clai",
		"X-OpenRouter-Title": "clai",
	}
	return nil
}
