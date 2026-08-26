package openai

import (
	"fmt"

	"github.com/baalimago/clai/internal/audio/generic"
)

const TranscribeURL = "https://api.openai.com/v1/audio/transcriptions"

var TranscriberDefault = Transcriber{
	Model: "whisper-1",
}

type Transcriber struct {
	generic.Transcriber
	Model string `json:"model"`
}

func (t *Transcriber) Setup() error {
	err := t.Transcriber.Setup("OPENAI_API_KEY", TranscribeURL, "DEBUG_OPENAI")
	if err != nil {
		return fmt.Errorf("failed to setup transcriber: %w", err)
	}
	t.Transcriber.Model = t.Model
	return nil
}
