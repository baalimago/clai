package internal

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/baalimago/clai/internal/audio"
	"github.com/baalimago/clai/internal/chat"
	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/photo"
	"github.com/baalimago/clai/internal/text"
	"github.com/baalimago/clai/internal/utils"
	"github.com/baalimago/clai/internal/vendors"
	"github.com/baalimago/clai/internal/vendors/anthropic"
	"github.com/baalimago/clai/internal/vendors/berget"
	"github.com/baalimago/clai/internal/vendors/deepseek"
	"github.com/baalimago/clai/internal/vendors/gemini"
	"github.com/baalimago/clai/internal/vendors/huggingface"
	"github.com/baalimago/clai/internal/vendors/inception"
	"github.com/baalimago/clai/internal/vendors/mistral"
	"github.com/baalimago/clai/internal/vendors/novita"
	"github.com/baalimago/clai/internal/vendors/ollama"
	"github.com/baalimago/clai/internal/vendors/openai"
	"github.com/baalimago/clai/internal/vendors/openrouter"
	"github.com/baalimago/clai/internal/vendors/xai"
	"github.com/baalimago/clai/internal/video"
	pkgtools "github.com/baalimago/clai/pkg/tools"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
	"github.com/baalimago/go_away_boilerplate/pkg/misc"
)

func selectTextQuerier(ctx context.Context, conf text.Configurations) (models.Querier, bool, error) {
	var q models.Querier
	found := false

	if conf.Model == "test" || conf.Model == "mock_test" {
		found = true
		qTmp, err := text.NewQuerier(ctx, conf, new(vendors.Mock{}))
		if err != nil {
			return nil, false, fmt.Errorf("failed to create test querier: %w", err)
		}
		q = &qTmp
	}

	// Explicit prefix routing: avoids accidental matches (e.g. model name contains "gpt").
	if strings.HasPrefix(conf.Model, "hf:") || strings.HasPrefix(conf.Model, "huggingface:") {
		found = true
		defaultCpy := huggingface.DefaultChat
		modelName := strings.TrimPrefix(conf.Model, "hf:")
		modelName = strings.TrimPrefix(modelName, "huggingface:")
		defaultCpy.Model = modelName
		qTmp, err := text.NewQuerier(ctx, conf, &defaultCpy)
		if err != nil {
			return nil, found, fmt.Errorf("failed to create text querier: %w", err)
		}
		q = &qTmp
	}

	if strings.Contains(conf.Model, "claude") {
		found = true
		defaultCpy := anthropic.Default
		defaultCpy.Model = conf.Model
		qTmp, err := text.NewQuerier(ctx, conf, &defaultCpy)
		if err != nil {
			return nil, found, fmt.Errorf("failed to create text querier: %w", err)
		}
		q = &qTmp
	}

	if strings.HasPrefix(conf.Model, "or:") {
		found = true
		defaultCpy := openrouter.Default
		defaultCpy.Model = conf.Model
		qTmp, err := text.NewQuerier(ctx, conf, &defaultCpy)
		if err != nil {
			return nil, found, fmt.Errorf("failed to create text querier: %w", err)
		}
		q = &qTmp
		return q, found, nil
	}

	if strings.Contains(conf.Model, "gpt") && !strings.HasPrefix(conf.Model, "or:") {
		found = true
		defaultCpy := openai.GptDefault
		defaultCpy.Model = conf.Model
		qTmp, err := text.NewQuerier(ctx, conf, &defaultCpy)
		if err != nil {
			return nil, found, fmt.Errorf("failed to create text querier: %w", err)
		}
		q = &qTmp
	}

	if strings.Contains(conf.Model, "deepseek") {
		found = true
		defaultCpy := deepseek.Default
		defaultCpy.Model = conf.Model
		qTmp, err := text.NewQuerier(ctx, conf, &defaultCpy)
		if err != nil {
			return nil, found, fmt.Errorf("failed to create text querier: %w", err)
		}
		q = &qTmp
	}

	if strings.Contains(conf.Model, "mercury") {
		found = true
		defaultCpy := inception.Default
		defaultCpy.Model = conf.Model
		qTmp, err := text.NewQuerier(ctx, conf, &defaultCpy)
		if err != nil {
			return nil, found, fmt.Errorf("failed to create text querier: %w", err)
		}
		q = &qTmp
	}

	if strings.Contains(conf.Model, "grok") {
		found = true
		defaultCpy := xai.Default
		defaultCpy.Model = conf.Model
		qTmp, err := text.NewQuerier(ctx, conf, &defaultCpy)
		if err != nil {
			return nil, found, fmt.Errorf("failed to create text querier: %w", err)
		}
		q = &qTmp
	}

	if strings.Contains(conf.Model, "mistral") ||
		strings.Contains(conf.Model, "mixtral") ||
		strings.Contains(conf.Model, "codestral") ||
		strings.Contains(conf.Model, "devstral") {
		found = true
		defaultCpy := mistral.Default
		defaultCpy.Model = conf.Model
		qTmp, err := text.NewQuerier(ctx, conf, &defaultCpy)
		if err != nil {
			return nil, false, fmt.Errorf("failed to create text querier: %w", err)
		}
		q = &qTmp
	}

	if strings.Contains(conf.Model, "gemini") {
		found = true
		defaultCpy := gemini.Default
		defaultCpy.Model = conf.Model
		qTmp, err := text.NewQuerier(ctx, conf, &defaultCpy)
		if err != nil {
			return nil, false, fmt.Errorf("failed to create text querier: %w", err)
		}
		q = &qTmp
	}

	// process before mistral, in case we want to use mistral for ollama
	if strings.HasPrefix(conf.Model, "ollama:") || conf.Model == "ollama" {
		found = true
		defaultCpy := ollama.Default
		if len(conf.Model) > 7 {
			defaultCpy.Model = conf.Model[7:]
		}
		qTmp, err := text.NewQuerier(ctx, conf, &defaultCpy)
		if err != nil {
			return nil, found, fmt.Errorf("failed to create text querier: %w", err)
		}
		q = &qTmp
	} else if strings.HasPrefix(conf.Model, "novita:") {
		found = true
		defaultCpy := novita.Default
		defaultCpy.Model = conf.Model[7:]
		qTmp, err := text.NewQuerier(ctx, conf, &defaultCpy)
		if err != nil {
			return nil, found, fmt.Errorf("failed to create text querier: %w", err)
		}
		q = &qTmp
	} else if strings.HasPrefix(conf.Model, "berget:") {
		found = true
		defaultCpy := berget.Default
		defaultCpy.Model = conf.Model[7:]
		qTmp, err := text.NewQuerier(ctx, conf, &defaultCpy)
		if err != nil {
			return nil, found, fmt.Errorf("failed to create text querier: %w", err)
		}
		q = &qTmp
	}
	return q, found, nil
}

// CreateTextQuerier by checking the model for which vendor to use, then initiating
// a TextQuerier
func CreateTextQuerier(ctx context.Context, conf text.Configurations) (models.Querier, error) {
	q, found, err := selectTextQuerier(ctx, conf)
	if err != nil {
		return nil, fmt.Errorf("failed to select text querier: %w", err)
	}
	if !found {
		return nil, fmt.Errorf("failed to find text querier for model: %v", conf.Model)
	}

	if misc.Truthy(os.Getenv("DEBUG")) {
		ancli.PrintOK(fmt.Sprintf("chat mode: %v, type of querier: %T\n", conf.ChatMode, q))
	}
	if conf.ChatMode {
		tq, isTextQuerier := q.(models.ChatQuerier)
		if !isTextQuerier {
			return nil, fmt.Errorf("failed to cast Querier using model: '%v' to TextQuerier, cannot proceed to chat", conf.Model)
		}
		configDir, _ := utils.GetClaiConfigDir()
		chatQ, err := chat.New(
			tq,
			configDir,
			conf.PostProccessedPrompt,
			conf.InitialChat.Messages,
			chat.NotCyclicalImport{
				UseTools:   conf.UseTools,
				UseProfile: conf.UseProfile,
				Model:      conf.Model,
			},
			conf.Raw,
			conf.Out,
			conf.InitialChat.Queries,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to create chat querier: %w", err)
		}
		q = chatQ
	}
	return q, nil
}

func CreatePhotoQuerier(conf photo.Configurations) (models.Querier, error) {
	if err := photo.ValidateOutputType(conf.Output.Type); err != nil {
		return nil, err
	}

	if conf.Output.Type == photo.LOCAL {
		if _, err := os.Stat(conf.Output.Dir); os.IsNotExist(err) {
			return nil, fmt.Errorf("failed to find photo output directory: %w", err)
		}
	}

	if strings.Contains(conf.Model, "dall-e") || strings.Contains(conf.Model, "gpt") {
		q, err := openai.NewPhotoQuerier(conf)
		if err != nil {
			return nil, fmt.Errorf("failed to create dall-e photo querier: %w", err)
		}
		return q, nil
	}

	if strings.Contains(conf.Model, "gemini") {
		q, err := gemini.NewPhotoQuerier(conf)
		if err != nil {
			return nil, fmt.Errorf("failed to create gemini photo querier: %w", err)
		}
		return q, nil
	}

	return nil, fmt.Errorf("failed to find photo querier for model: %v", conf.Model)
}

// createAudioSplitter routes the model to a transcriber vendor by explicit
// prefix/substring per the audio strategy and wraps it in the split/stitch
// orchestrator. Shared by the subcommand querier and the audio_transcribe tool.
func createAudioSplitter(conf audio.Configurations) (*audio.Splitter, error) {
	model := conf.Transcribe.Model
	var transcriber audio.Transcriber
	switch {
	case strings.HasPrefix(model, "test") || strings.HasPrefix(model, "mock_test"):
		transcriber = audio.MockTranscriber{Diarized: strings.Contains(model, "diarize")}
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
	splitter := audio.NewSplitter(transcriber, audio.ExecRunner{})
	splitter.Model = model
	if conf.Transcribe.Parallelism > 0 {
		splitter.Parallelism = conf.Transcribe.Parallelism
	}
	return splitter, nil
}

// CreateAudioQuerier validates the output format, routes the transcriber and
// returns the transcript-printing querier.
func CreateAudioQuerier(conf audio.Configurations, filePath string, cleanup func()) (models.Querier, error) {
	format, err := audio.ParseOutputFormat(conf.Transcribe.OutputFormat)
	if err != nil {
		return nil, err
	}
	splitter, err := createAudioSplitter(conf)
	if err != nil {
		return nil, err
	}
	return &audio.TranscribeQuerier{
		Splitter: splitter,
		FilePath: filePath,
		Format:   format,
		Cleanup:  cleanup,
	}, nil
}

// audioTranscribeEngine backs the audio_transcribe built-in tool: config →
// querier path → rendered transcript string. Wired via init below so
// pkg/tools carries no engine dependencies (mode-as-tool bridge).
func audioTranscribeEngine(filePath, outputFormat string) (string, error) {
	confDir, err := utils.GetClaiConfigDir()
	if err != nil {
		return "", fmt.Errorf("failed to find config dir: %w", err)
	}
	aConf, err := utils.LoadConfigFromFile(confDir, "audioConfig.json", nil, &audio.Default)
	if err != nil {
		return "", fmt.Errorf("failed to load configs: %w", err)
	}
	if outputFormat == "" {
		outputFormat = "text"
	}
	format, err := audio.ParseOutputFormat(outputFormat)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(filePath); err != nil {
		return "", fmt.Errorf("failed to find audio file: %w", err)
	}
	splitter, err := createAudioSplitter(aConf)
	if err != nil {
		return "", err
	}
	segs, err := splitter.Transcribe(context.Background(), filePath)
	if err != nil {
		return "", fmt.Errorf("failed to transcribe %v: %w", filePath, err)
	}
	return audio.Render(segs, format)
}

func init() {
	pkgtools.AudioTranscribeEngine = audioTranscribeEngine
}

func CreateVideoQuerier(conf video.Configurations) (models.Querier, error) {
	if err := video.ValidateOutputType(conf.Output.Type); err != nil {
		return nil, err
	}

	if conf.Output.Type == video.LOCAL {
		if _, err := os.Stat(conf.Output.Dir); os.IsNotExist(err) {
			err = os.MkdirAll(conf.Output.Dir, 0o755)
			if err != nil {
				return nil, fmt.Errorf("failed to find or create video output directory: %w", err)
			}
		}
	}

	if strings.Contains(conf.Model, "sora") {
		q, err := openai.NewVideoQuerier(conf)
		if err != nil {
			return nil, fmt.Errorf("failed to create sora video querier: %w", err)
		}
		return q, nil
	}

	return nil, fmt.Errorf("failed to find video querier for model: %v", conf.Model)
}
