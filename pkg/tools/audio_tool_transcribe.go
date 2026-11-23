package tools

import (
	"context"
	"fmt"
	"strings"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

type AudioTranscribeTool pub_models.Specification

var audioTranscribeFormats = []string{"text", "vtt", "srt", "json"}

// AudioTranscribeCall is one audio_transcribe call, as the engine gets it.
type AudioTranscribeCall struct {
	FilePath     string
	OutputFormat string
	// Voices is the call's voices argument; empty when the model gave none.
	Voices []string
}

// AudioTranscribeEngine is injected by the clai runtime (mode-as-tool bridge):
// the tool layer holds no transcription logic and pkg/tools stays free of
// engine dependencies.
var AudioTranscribeEngine func(ctx context.Context, call AudioTranscribeCall) (string, error)

// AudioTranscribe names the speakers the voice library knows. A call that
// gives voices is named after those people and nobody else; a name the
// library does not hold never fails the call.
var AudioTranscribe = AudioTranscribeTool{
	Name:        "audio_transcribe",
	Description: "Transcribe a local audio file to text. Returns the transcript; timestamps and speaker labels are included for output formats that carry them. When a long file is split, speaker labels carry the chunk number (1-A, 2-A), and labels from different chunks are not known to be the same person. Speakers whose voice is in the user's voice library are named instead of labelled; pass voices to say whom to expect. In text, vtt and srt a name that comes from a clip the user has not confirmed is written '<name> (unconfirmed)'. With output_format json and known voices the result is {\"segments\": [...], \"naming\": {...}}: a named segment carries voice {clip, status}, and naming says who was named and why anyone was not. JSON is pretty-printed with one segment object per indented block for jq and other line-oriented inspection. Default output_format is 'text'.",
	Inputs: &pub_models.InputSchema{
		Type: "object",
		Properties: map[string]pub_models.ParameterObject{
			"file_path": {
				Type:        "string",
				Description: "Path to the local audio file to transcribe.",
			},
			"output_format": {
				Type:        "string",
				Description: "Transcript output format. JSON is newline-formatted for jq parsing. Default is 'text'.",
				Enum:        &audioTranscribeFormats,
			},
			"voices": {
				Type:        "array",
				Description: "Optional. The names of the people expected in the recording, as the voice library holds them, for example [\"Lorentz\", \"Christoph\"]. Only they are named. Leave it out to name everyone the library knows.",
				Items:       &pub_models.ParameterObject{Type: "string"},
			},
		},
		Required: []string{"file_path"},
	},
}

// audioTranscribeVoicesError refuses a voices argument that is not an array
// of strings, before anything is transcribed.
type audioTranscribeVoicesError struct{}

func (audioTranscribeVoicesError) Error() string {
	return "voices must be an array of strings, the names of the people expected in the recording"
}

func (a AudioTranscribeTool) Call(input pub_models.Input) (string, error) {
	return a.CallWithContext(context.Background(), input)
}

// CallWithContext runs the engine on ctx, so its progress and notices reach
// the run's streams.
func (a AudioTranscribeTool) CallWithContext(ctx context.Context, input pub_models.Input) (string, error) {
	filePath, ok := input["file_path"].(string)
	if !ok || filePath == "" {
		return "", fmt.Errorf("file_path must be a non-empty string")
	}
	outputFormat := "text"
	if raw, exists := input["output_format"]; exists {
		formatStr, ok := raw.(string)
		if !ok {
			return "", fmt.Errorf("output_format must be a string, one of: %v", audioTranscribeFormats)
		}
		outputFormat = formatStr
	}
	voices, err := audioTranscribeVoices(input["voices"])
	if err != nil {
		return "", err
	}
	if AudioTranscribeEngine == nil {
		return "", fmt.Errorf("audio transcription engine is not wired into this runtime")
	}
	return AudioTranscribeEngine(ctx, AudioTranscribeCall{FilePath: filePath, OutputFormat: outputFormat, Voices: voices})
}

// audioTranscribeVoices reads the voices argument: absent or null is no
// name, and a blank name is no name either.
func audioTranscribeVoices(raw any) ([]string, error) {
	var names []string
	switch v := raw.(type) {
	case nil:
		return nil, nil
	case []string:
		names = v
	case []any:
		for _, item := range v {
			name, ok := item.(string)
			if !ok {
				return nil, audioTranscribeVoicesError{}
			}
			names = append(names, name)
		}
	default:
		return nil, audioTranscribeVoicesError{}
	}
	var kept []string
	for _, name := range names {
		if name = strings.TrimSpace(name); name != "" {
			kept = append(kept, name)
		}
	}
	return kept, nil
}

func (a AudioTranscribeTool) Specification() pub_models.Specification {
	return pub_models.Specification(a)
}
