package audio

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/utils"
	"github.com/baalimago/clai/internal/vendors/openai"
	"github.com/baalimago/clai/internal/vendors/openrouter"
	pkgtools "github.com/baalimago/clai/pkg/tools"
)

// createSplitter routes the model to a transcriber vendor by explicit
// prefix/substring per the audio strategy and wraps it in the split/stitch
// orchestrator. Shared by the subcommand querier and the audio_transcribe tool.
func createSplitter(conf Configurations) (*Splitter, error) {
	model := conf.Transcribe.Model
	var transcriber Transcriber
	switch {
	case strings.HasPrefix(model, "test") || strings.HasPrefix(model, "mock_test"):
		transcriber = MockTranscriber{Diarized: strings.Contains(model, "diarize")}
	case strings.HasPrefix(model, "or:"):
		vendorCpy := openrouter.TranscriberDefault
		vendorCpy.Model = model
		if err := vendorCpy.Setup(); err != nil {
			return nil, fmt.Errorf("failed to setup openrouter transcriber: %w", err)
		}
		transcriber = &vendorCpy
	case strings.Contains(model, "whisper") || strings.Contains(model, "transcribe"):
		vendorCpy := openai.TranscriberDefault
		vendorCpy.Model = model
		if err := vendorCpy.Setup(); err != nil {
			return nil, fmt.Errorf("failed to setup openai transcriber: %w", err)
		}
		transcriber = &vendorCpy
	default:
		return nil, fmt.Errorf("failed to find audio transcriber for model: '%v', supported routes: 'or:' prefix (OpenRouter), model containing 'whisper' or 'transcribe' (OpenAI), 'test'/'mock_test' (mock)", model)
	}
	splitter := NewSplitter(transcriber, ExecRunner{})
	splitter.Model = model
	if conf.Transcribe.Parallelism > 0 {
		splitter.Parallelism = conf.Transcribe.Parallelism
	}
	return splitter, nil
}

// CreateQuerier validates the output format, routes the transcriber and
// returns the transcript-printing querier.
func CreateQuerier(conf Configurations, filePath string, cleanup func()) (models.Querier, error) {
	format, err := ParseOutputFormat(conf.Transcribe.OutputFormat)
	if err != nil {
		return nil, err
	}
	splitter, err := createSplitter(conf)
	if err != nil {
		return nil, err
	}
	return &TranscribeQuerier{
		Splitter: splitter,
		FilePath: filePath,
		Format:   format,
		Cleanup:  cleanup,
	}, nil
}

// audioTranscribeEngine backs the audio_transcribe built-in tool: config →
// querier path → rendered transcript string. Wired via init below so
// pkg/tools carries no engine dependencies (mode-as-tool bridge).
func transcribeEngine(filePath, outputFormat string) (string, error) {
	confDir, err := utils.GetClaiConfigDir()
	if err != nil {
		return "", fmt.Errorf("failed to find config dir: %w", err)
	}
	aConf, err := utils.LoadConfigFromFile(confDir, "audioConfig.json", nil, &Default)
	if err != nil {
		return "", fmt.Errorf("failed to load configs: %w", err)
	}
	if outputFormat == "" {
		outputFormat = "text"
	}
	outputFormat = applyTranscribeOverrides(&aConf, outputFormat)
	format, err := ParseOutputFormat(outputFormat)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(filePath); err != nil {
		return "", fmt.Errorf("failed to find audio file: %w", err)
	}
	splitter, err := createSplitter(aConf)
	if err != nil {
		return "", err
	}
	segs, err := splitter.Transcribe(context.Background(), filePath)
	if err != nil {
		return "", fmt.Errorf("failed to transcribe %v: %w", filePath, err)
	}
	return Render(segs, format)
}

func init() {
	pkgtools.AudioTranscribeEngine = transcribeEngine
}
