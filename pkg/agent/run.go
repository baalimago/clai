package agent

import (
	"context"
	"fmt"
	"time"

	"github.com/baalimago/clai/pkg/text/models"
)

func (a *Agent) Run(ctx context.Context) error {
	now := time.Now()
	c := models.Chat{
		Created: now,
		ID:      fmt.Sprintf("%v_agent-%v", now, a.name),
		Messages: []models.Message{
			{
				Role:    "user",
				Content: a.prompt,
			},
		},
	}
	_, err := a.querier.TextQuery(ctx, c)
	if err != nil {
		return fmt.Errorf("failed to TextQuery: %w", err)
	}
	return nil
}
