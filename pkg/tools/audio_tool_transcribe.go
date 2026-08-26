package tools

import (
	"fmt"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

type AudioTranscribeTool pub_models.Specification

var audioTranscribeFormats = []string{"text", "vtt", "srt", "json"}

var AudioTranscribe = AudioTranscribeTool{
	Name:        "audio_transcribe",
	Description: "Transcribe a local audio file to text. Returns the transcript; timestamps and speaker labels are included for output formats that carry them. Default output_format is 'text'.",
	Inputs: &pub_models.InputSchema{
		Type: "object",
		Properties: map[string]pub_models.ParameterObject{
			"file_path": {
				Type:        "string",
				Description: "Path to the local audio file to transcribe.",
			},
			"output_format": {
				Type:        "string",
				Description: "Transcript output format. Default is 'text'.",
				Enum:        &audioTranscribeFormats,
			},
		},
		Required: []string{"file_path"},
	},
}

// AudioTranscribeEngine is injected by the clai runtime (mode-as-tool bridge):
// the tool layer holds no transcription logic and pkg/tools stays free of
// engine dependencies.
var AudioTranscribeEngine func(filePath, outputFormat string) (string, error)

func (a AudioTranscribeTool) Call(input pub_models.Input) (string, error) {
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
	if AudioTranscribeEngine == nil {
		return "", fmt.Errorf("audio transcription engine is not wired into this runtime")
	}
	return AudioTranscribeEngine(filePath, outputFormat)
}

func (a AudioTranscribeTool) Specification() pub_models.Specification {
	return pub_models.Specification(a)
}
