package tools

import (
	"context"
	"fmt"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// AudioSourcesEngine is injected by the clai runtime. It returns the text a
// model reads. The tool layer holds no discovery logic.
var AudioSourcesEngine func(ctx context.Context) (string, error)

type AudioSourcesTool pub_models.Specification

// AudioSources lists this machine's capture sources.
var AudioSources = AudioSourcesTool{
	Name:        "audio_sources",
	Description: "List the audio inputs this machine can record now: the built-in microphone and every system output whose monitor can be captured. The first line says whether system audio can be captured. Each further line is '<kind> \"<name>\" [<label>]', where <name> is what audio_record's devices argument takes, and a trailing ' default' marks the source a role selection picks. Call this before audio_record to learn the names.",
	Inputs: &pub_models.InputSchema{
		Type:       "object",
		Required:   make([]string, 0),
		Properties: map[string]pub_models.ParameterObject{},
	},
}

func (a AudioSourcesTool) Call(input pub_models.Input) (string, error) {
	return a.CallWithContext(context.Background(), input)
}

func (a AudioSourcesTool) CallWithContext(ctx context.Context, input pub_models.Input) (string, error) {
	if AudioSourcesEngine == nil {
		return "", fmt.Errorf("audio discovery is not available in this runtime")
	}
	return AudioSourcesEngine(ctx)
}

func (a AudioSourcesTool) Specification() pub_models.Specification {
	return pub_models.Specification(a)
}
