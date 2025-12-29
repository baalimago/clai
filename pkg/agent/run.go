package agent

import (
	"context"
	"fmt"
	"time"

	"github.com/baalimago/clai/pkg/text/models"
)

// Run the agent using some context. Will return the last system message, or an error.
func (a *Agent) Run(ctx context.Context) (string, error) {
	now := time.Now()
	err := a.Setup(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to reset/re-Setup querier: %w", err)
	}
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
	c, err = a.querier.TextQuery(ctx, c)
	if err != nil {
		return "", fmt.Errorf("failed to TextQuery: %w", err)
	}
	msg, _, err := c.LastOfRole("system")
	if err != nil {
		return "", fmt.Errorf("failed to get last message of system role: %w", err)
	}
	return msg.String(), nil
}
