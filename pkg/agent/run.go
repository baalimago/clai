package agent

import (
	"context"
	"fmt"
	"time"

	"github.com/baalimago/clai/pkg/text/models"
)

func (a *Agent) Run(ctx context.Context) (string, error) {
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
	c, err := a.querier.TextQuery(ctx, c)
	if err != nil {
		return "", fmt.Errorf("failed to TextQuery: %w", err)
	}
	msg, _, err := c.LastOfRole("system")
	if err != nil {
		return "", fmt.Errorf("failed to get last message of system role: %w", err)
	}
	return msg.String(), nil
}
