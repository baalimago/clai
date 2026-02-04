package internal

import (
	"context"
	"errors"
	"fmt"

	"github.com/baalimago/clai/internal/chat"
	"github.com/baalimago/clai/internal/models"
)

// dreQuerier implements models.Querier for the `dre` command.
// It prints the last message from the directory-scoped conversation bound
// to the current working directory.
type dreQuerier struct {
	raw bool
}

func (q dreQuerier) Query(ctx context.Context) error {
	_ = ctx
	if err := chat.Replay(q.raw, true); err != nil {
		return fmt.Errorf("dre: %w", err)
	}
	return nil
}

var _ models.Querier = (*dreQuerier)(nil)

func setupDRE(mode Mode, postFlagConf Configurations, _ []string) (models.Querier, error) {
	if mode != DRE {
		return nil, errors.New("setupDRE: unexpected mode")
	}
	return &dreQuerier{raw: postFlagConf.PrintRaw}, nil
}
