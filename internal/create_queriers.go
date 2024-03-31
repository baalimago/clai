package internal

import (
	"fmt"
	"os"
	"strings"

	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/openai"
	"github.com/baalimago/clai/internal/photo"
	"github.com/baalimago/clai/internal/text"
)

// CreateTextQuerier by checking the model for which vendor to use, then initiating
// a TextQuerier
func CreateTextQuerier(conf text.Configurations) (models.Querier, error) {
	if strings.Contains(conf.Model, "gpt") {
		return openai.NewTextQuerier(conf)
	}
	return nil, fmt.Errorf("failed to find querier for model: %v", conf.Model)
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

	if strings.Contains(conf.Model, "dall") {
		dalleQuerier, err := openai.NewPhotoQuerier(conf)
		if err != nil {
			return nil, fmt.Errorf("failed to create DallE querier: %w", err)
		}
		return dalleQuerier, nil
	}

	return nil, fmt.Errorf("failed to find text querier for model: %v", conf.Model)
}
