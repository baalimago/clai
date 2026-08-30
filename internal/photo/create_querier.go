package photo

import (
	"fmt"
	"os"
	"strings"

	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/vendors/gemini"
	"github.com/baalimago/clai/internal/vendors/openai"
)

// CreateQuerier routes the model to a photo vendor querier.
func CreateQuerier(conf Configurations) (models.Querier, error) {
	if err := ValidateOutputType(conf.Output.Type); err != nil {
		return nil, err
	}

	if conf.Output.Type == LOCAL {
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
