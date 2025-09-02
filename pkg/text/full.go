package text

import (
	"context"
	"fmt"
	"os"
	"path"

	"github.com/baalimago/clai/internal"
	priv_models "github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/text"
	"github.com/baalimago/clai/pkg/text/models"
)

// FullResponse text querier, as opposed to returning a stream or something
type FullResponse interface {
	Setup(context.Context) error

	// Query the underlying llm with some propt. Will cancel on context cancel.
	Query(context.Context, models.Chat) (models.Chat, error)
}

type publicQuerier struct {
	conf    text.Configurations
	chat    models.Chat
	querier priv_models.ChatQuerier
}

func NewFullResponseQuerier(c models.Configurations) FullResponse {
	return &publicQuerier{
		conf: pubConfigToInternal(c),
	}
}

func internalToolsToString(in []models.ToolName) (ret []string) {
	for _, s := range in {
		ret = append(ret, string(s))
	}
	return
}

func pubConfigToInternal(c models.Configurations) text.Configurations {
	claiDir := path.Join(c.ConfigDir, "clai")

	return text.Configurations{
		Model:               c.Model,
		SystemPrompt:        c.SystemPrompt,
		UseTools:            true,
		ConfigDir:           claiDir,
		TokenWarnLimit:      300000,
		ToolOutputRuneLimit: 30000,
		SaveReplyAsConv:     true,
		Stream:              true,
		UseProfile:          "",
		ProfilePath:         "",
		Tools:               internalToolsToString(c.InternalTools),
	}
}

func (pq *publicQuerier) Setup(ctx context.Context) error {
	if _, err := os.Stat(pq.conf.ConfigDir); os.IsNotExist(err) {
		os.Mkdir(pq.conf.ConfigDir, 0o755)
	}
	mcpServersDir := path.Join(pq.conf.ConfigDir, "mcpServers")
	if _, err := os.Stat(mcpServersDir); os.IsNotExist(err) {
		os.Mkdir(mcpServersDir, 0o755)
	}
	conversationsDir := path.Join(pq.conf.ConfigDir, "conversations")
	if _, err := os.Stat(mcpServersDir); os.IsNotExist(err) {
		os.Mkdir(conversationsDir, 0o755)
	}
	querier, err := internal.CreateTextQuerier(ctx, pq.conf)
	if err != nil {
		return fmt.Errorf("publicQuerier.Setup failed to CreateTextQuerier: %v", err)
	}
	tq, isChatQuerier := querier.(priv_models.ChatQuerier)
	if !isChatQuerier {
		return fmt.Errorf("failed to cast Querier using model: '%v' to TextQuerier, cannot proceed", pq.conf.Model)
	}
	pq.querier = tq
	return nil
}

func (pq *publicQuerier) Query(ctx context.Context, inpChat models.Chat) (models.Chat, error) {
	err := pq.Setup(ctx)
	if err != nil {
		return models.Chat{}, fmt.Errorf("pq.Query failed to Setup clone: %v", err)
	}
	return pq.querier.TextQuery(ctx, inpChat)
}
