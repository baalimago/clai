package text

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/baalimago/clai/internal/models"
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
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
	"github.com/baalimago/go_away_boilerplate/pkg/misc"
)

func selectTextQuerier(ctx context.Context, conf Configurations) (models.Querier, bool, error) {
	var q models.Querier
	found := false

	if conf.Model == "test" || conf.Model == "mock_test" {
		found = true
		qTmp, err := NewQuerier(ctx, conf, new(vendors.Mock{}))
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
		qTmp, err := NewQuerier(ctx, conf, &defaultCpy)
		if err != nil {
			return nil, found, fmt.Errorf("failed to create text querier: %w", err)
		}
		q = &qTmp
	}

	if strings.Contains(conf.Model, "claude") {
		found = true
		defaultCpy := anthropic.Default
		defaultCpy.Model = conf.Model
		qTmp, err := NewQuerier(ctx, conf, &defaultCpy)
		if err != nil {
			return nil, found, fmt.Errorf("failed to create text querier: %w", err)
		}
		q = &qTmp
	}

	if strings.HasPrefix(conf.Model, "or:") {
		found = true
		defaultCpy := openrouter.Default
		defaultCpy.Model = conf.Model
		qTmp, err := NewQuerier(ctx, conf, &defaultCpy)
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
		qTmp, err := NewQuerier(ctx, conf, &defaultCpy)
		if err != nil {
			return nil, found, fmt.Errorf("failed to create text querier: %w", err)
		}
		q = &qTmp
	}

	if strings.Contains(conf.Model, "deepseek") {
		found = true
		defaultCpy := deepseek.Default
		defaultCpy.Model = conf.Model
		qTmp, err := NewQuerier(ctx, conf, &defaultCpy)
		if err != nil {
			return nil, found, fmt.Errorf("failed to create text querier: %w", err)
		}
		q = &qTmp
	}

	if strings.Contains(conf.Model, "mercury") {
		found = true
		defaultCpy := inception.Default
		defaultCpy.Model = conf.Model
		qTmp, err := NewQuerier(ctx, conf, &defaultCpy)
		if err != nil {
			return nil, found, fmt.Errorf("failed to create text querier: %w", err)
		}
		q = &qTmp
	}

	if strings.Contains(conf.Model, "grok") {
		found = true
		defaultCpy := xai.Default
		defaultCpy.Model = conf.Model
		qTmp, err := NewQuerier(ctx, conf, &defaultCpy)
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
		qTmp, err := NewQuerier(ctx, conf, &defaultCpy)
		if err != nil {
			return nil, false, fmt.Errorf("failed to create text querier: %w", err)
		}
		q = &qTmp
	}

	if strings.Contains(conf.Model, "gemini") {
		found = true
		defaultCpy := gemini.Default
		defaultCpy.Model = conf.Model
		qTmp, err := NewQuerier(ctx, conf, &defaultCpy)
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
		qTmp, err := NewQuerier(ctx, conf, &defaultCpy)
		if err != nil {
			return nil, found, fmt.Errorf("failed to create text querier: %w", err)
		}
		q = &qTmp
	} else if strings.HasPrefix(conf.Model, "novita:") {
		found = true
		defaultCpy := novita.Default
		defaultCpy.Model = conf.Model[7:]
		qTmp, err := NewQuerier(ctx, conf, &defaultCpy)
		if err != nil {
			return nil, found, fmt.Errorf("failed to create text querier: %w", err)
		}
		q = &qTmp
	} else if strings.HasPrefix(conf.Model, "berget:") {
		found = true
		defaultCpy := berget.Default
		defaultCpy.Model = conf.Model[7:]
		qTmp, err := NewQuerier(ctx, conf, &defaultCpy)
		if err != nil {
			return nil, found, fmt.Errorf("failed to create text querier: %w", err)
		}
		q = &qTmp
	}
	return q, found, nil
}

// CreateQuerier by checking the model for which vendor to use, then initiating
// a TextQuerier
func CreateQuerier(ctx context.Context, conf Configurations) (models.Querier, error) {
	q, found, err := selectTextQuerier(ctx, conf)
	if err != nil {
		return nil, fmt.Errorf("failed to select text querier: %w", err)
	}
	if !found {
		return nil, fmt.Errorf("failed to find text querier for model: %v", conf.Model)
	}

	if misc.Truthy(os.Getenv("DEBUG")) {
		ancli.PrintOK(fmt.Sprintf("type of querier: %T\n", q))
	}
	return q, nil
}
