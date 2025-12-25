package tools

import (
	"fmt"
	"os/exec"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

type FFProbeTool pub_models.Specification

var FFProbe = FFProbeTool{
	Name:        "ffprobe",
	Description: "Analyze multimedia files and extract metadata using ffprobe. Provides detailed information about video, audio, and container formats.",
	Inputs: &pub_models.InputSchema{
		Type: "object",
		Properties: map[string]pub_models.ParameterObject{
			"file": {
				Type:        "string",
				Description: "The multimedia file to analyze.",
			},
			"format": {
				Type:        "string",
				Description: "Output format: json, xml, csv, flat, ini, or default. Default is 'default'.",
			},
			"showFormat": {
				Type:        "boolean",
				Description: "Show format/container information.",
			},
			"showStreams": {
				Type:        "boolean",
				Description: "Show stream information (video, audio, subtitle tracks).",
			},
			"showFrames": {
				Type:        "boolean",
				Description: "Show frame information (use with caution on large files).",
			},
			"selectStreams": {
				Type:        "string",
				Description: "Select specific streams (e.g., 'v:0' for first video stream, 'a:0' for first audio stream).",
			},
			"showEntries": {
				Type:        "string",
				Description: "Show only specific entries (e.g., 'format=duration,size' or 'stream=codec_name,width,height').",
			},
		},
		Required: []string{"file"},
	},
}

func (f FFProbeTool) Call(input pub_models.Input) (string, error) {
	file, ok := input["file"].(string)
	if !ok {
		return "", fmt.Errorf("file must be a string")
	}

	args := []string{"-hide_banner"}

	// Set output format
	if input["format"] != nil {
		format, ok := input["format"].(string)
		if !ok {
			return "", fmt.Errorf("format must be a string")
		}
		switch format {
		case "json", "xml", "csv", "flat", "ini":
			args = append(args, "-of", format)
		case "default":
			// Use default format, no additional args needed
		default:
			return "", fmt.Errorf("unsupported format: %s. Supported formats: json, xml, csv, flat, ini, default", format)
		}
	}

	// Show format information
	if input["showFormat"] != nil {
		showFormat, ok := input["showFormat"].(bool)
		if !ok {
			return "", fmt.Errorf("showFormat must be a boolean")
		}
		if showFormat {
			args = append(args, "-show_format")
		}
	}

	// Show streams information
	if input["showStreams"] != nil {
		showStreams, ok := input["showStreams"].(bool)
		if !ok {
			return "", fmt.Errorf("showStreams must be a boolean")
		}
		if showStreams {
			args = append(args, "-show_streams")
		}
	}

	// Show frames information
	if input["showFrames"] != nil {
		showFrames, ok := input["showFrames"].(bool)
		if !ok {
			return "", fmt.Errorf("showFrames must be a boolean")
		}
		if showFrames {
			args = append(args, "-show_frames")
		}
	}

	// Select specific streams
	if input["selectStreams"] != nil {
		selectStreams, ok := input["selectStreams"].(string)
		if !ok {
			return "", fmt.Errorf("selectStreams must be a string")
		}
		args = append(args, "-select_streams", selectStreams)
	}

	// Show specific entries
	if input["showEntries"] != nil {
		showEntries, ok := input["showEntries"].(string)
		if !ok {
			return "", fmt.Errorf("showEntries must be a string")
		}
		args = append(args, "-show_entries", showEntries)
	}

	// Add the file as the last argument
	args = append(args, file)

	cmd := exec.Command("ffprobe", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("failed to run ffprobe: %w, output: %v", err, string(output))
	}

	return string(output), nil
}

func (f FFProbeTool) Specification() pub_models.Specification {
	return pub_models.Specification(FFProbe)
}
