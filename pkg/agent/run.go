package agent

import (
	"context"
	"fmt"
	"time"

	"github.com/baalimago/clai/pkg/text/models"
)

// Query the agent, taking Chat and returning mutaded Chat
func (a *Agent) Query(ctx context.Context, chat models.Chat) (models.Chat, error) {
	c, err := a.querier.TextQuery(ctx, chat)
	if err != nil {
		return models.Chat{}, fmt.Errorf("Agent.TextQuery: %w", err)
	}
	return c, nil
}

// Run the agent using some context. Will return the last system message, or an error.
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
	c, err := a.Query(ctx, c)
	if err != nil {
		return "", fmt.Errorf("Agent.Run: %w", err)
	}
	msg, _, err := c.LastOfRole("system")
	if err != nil {
		return "", fmt.Errorf("failed to get last message of system role: %w", err)
	}
	return msg.String(), nil
}
