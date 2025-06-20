package models

import (
	"context"
	"testing"
	"time"

	"github.com/baalimago/go_away_boilerplate/pkg/testboil"
)

// These tests are used in other places of code, an attempt at generic testing
// to ensure implementation standards are kept
func Querier_Context_Test(t *testing.T, q Querier) {
	testboil.ReturnsOnContextCancel(t, func(ctx context.Context) {
		q.Query(ctx)
	}, time.Second)
}

func ChatQuerier_Test(t *testing.T, q ChatQuerier) {
	testboil.ReturnsOnContextCancel(t, func(ctx context.Context) {
		q.TextQuery(ctx, Chat{})
	}, time.Second)
}

func StreamCompleter_Test(t *testing.T, s StreamCompleter) {
	testboil.ReturnsOnContextCancel(t, func(ctx context.Context) {
		s.StreamCompletions(ctx, Chat{})
	}, time.Second)
}
