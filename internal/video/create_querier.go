package video

import (
	"fmt"
	"os"
	"strings"

	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/vendors/openai"
)

// CreateQuerier routes the model to a video vendor querier.
func CreateQuerier(conf Configurations) (models.Querier, error) {
	if err := ValidateOutputType(conf.Output.Type); err != nil {
		return nil, err
	}

	if conf.Output.Type == LOCAL {
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
