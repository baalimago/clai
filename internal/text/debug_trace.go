package text

import (
	"context"

	"github.com/baalimago/clai/internal/runenv"
)

func traceChatf(ctx context.Context, format string, args ...any) {
	runenv.Debugf(ctx, runenv.SubsystemChat, "[DEBUG_CHAT] "+format, args...)
}

// traceSummaryf is a diagnostic, so traces never enter a -r answer stream.
func traceSummaryf(ctx context.Context, format string, args ...any) {
	runenv.Debugf(ctx, runenv.SubsystemSummary, "[DEBUG_SUMMARY] "+format, args...)
}
