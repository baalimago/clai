package internal

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/baalimago/clai/internal/chat"
	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/photo"
	"github.com/baalimago/clai/internal/text"
	"github.com/baalimago/clai/internal/vendors/anthropic"
	"github.com/baalimago/clai/internal/vendors/deepseek"
	"github.com/baalimago/clai/internal/vendors/inception"
	"github.com/baalimago/clai/internal/vendors/mistral"
	"github.com/baalimago/clai/internal/vendors/novita"
	"github.com/baalimago/clai/internal/vendors/ollama"
	"github.com/baalimago/clai/internal/vendors/openai"
	"github.com/baalimago/clai/internal/vendors/xai"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
	"github.com/baalimago/go_away_boilerplate/pkg/misc"
)

func selectTextQuerier(ctx context.Context, conf text.Configurations) (models.Querier, bool, error) {
	var q models.Querier
	found := false

	if strings.Contains(conf.Model, "claude") {
		found = true
		defaultCpy := anthropic.Default
		// The model determines where to check for the config using
		// cfgdir/vendor_model_version.json. If it doesn't find it,
		// it will use the default to create a new config with this
		// path and the default values. In there, the model needs to be
		// the configured model (not the default one)
		defaultCpy.Model = conf.Model
		qTmp, err := text.NewQuerier(ctx, conf, &defaultCpy)
		if err != nil {
			return nil, found, fmt.Errorf("failed to create text querier: %w", err)
		}
		q = &qTmp
	}

	if strings.Contains(conf.Model, "gpt") {
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
		configDir, _ := os.UserConfigDir()
		chatQ, err := chat.New(tq,
			configDir,
			conf.PostProccessedPrompt,
			conf.InitialPrompt.Messages,
			chat.NotCyclicalImport{
				UseTools:   conf.UseTools,
				UseProfile: conf.UseProfile,
				Model:      conf.Model,
			},
			conf.Raw)
		if err != nil {
			return nil, fmt.Errorf("failed to create chat querier: %w", err)
		}
		q = chatQ
	}
	return q, nil
}

func NewPhotoQuerier(conf photo.Configurations) (models.Querier, error) {
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

	return nil, fmt.Errorf("failed to find photo querier for model: %v", conf.Model)
}
