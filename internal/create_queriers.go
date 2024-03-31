package internal

import (
	"fmt"
	"os"
	"strings"

	"github.com/baalimago/clai/internal/chat"
	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/openai"
	"github.com/baalimago/clai/internal/photo"
	"github.com/baalimago/clai/internal/text"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
	"github.com/baalimago/go_away_boilerplate/pkg/misc"
)

// CreateTextQuerier by checking the model for which vendor to use, then initiating
// a TextQuerier
func CreateTextQuerier(conf text.Configurations) (models.Querier, error) {
	var q models.Querier
	if strings.Contains(conf.Model, "gpt") {
		qTmp, err := openai.NewTextQuerier(conf)
		if err != nil {
			return nil, fmt.Errorf("failed to create GPT querier: %w", err)
		}
		q = qTmp
	}

	if misc.Truthy(os.Getenv("DEBUG")) {
		ancli.PrintOK(fmt.Sprintf("chat mode: %v\n", conf.ChatMode))
	}
	if conf.ChatMode {
		tq, isTextQuerier := q.(models.ChatQuerier)
		if !isTextQuerier {
			return nil, fmt.Errorf("failed to cast Querier using model: '%v' to TextQuerier, cannot proceed to chat", conf.Model)
		}
		chatQ, err := chat.New(tq, conf.PostProccessedPrompt)
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

	if strings.Contains(conf.Model, "dall") {
		dalleQuerier, err := openai.NewPhotoQuerier(conf)
		if err != nil {
			return nil, fmt.Errorf("failed to create DallE querier: %w", err)
		}
		return dalleQuerier, nil
	}

	return nil, fmt.Errorf("failed to find text querier for model: %v", conf.Model)
}
