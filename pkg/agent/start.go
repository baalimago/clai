package agent

import (
	"context"
	"fmt"
	"time"

	"github.com/baalimago/clai/pkg/text/models"
)

func (a *agent) do(ctx context.Context) {
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
	a.querier.TextQuery(ctx, c)
}

func (a *agent) Start(ctx context.Context, interval time.Duration) error {
	t := time.NewTicker(interval)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			a.do(ctx)
		}
	}
}
