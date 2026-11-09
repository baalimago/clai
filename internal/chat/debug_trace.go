package chat

import (
	"context"

	"github.com/baalimago/clai/internal/runenv"
)

func debugChatEnabled(ctx context.Context) bool {
	return runenv.DebugEnabled(ctx, runenv.SubsystemChat)
}

func traceChatf(ctx context.Context, format string, args ...any) {
	runenv.Debugf(ctx, runenv.SubsystemChat, format, args...)
}

// debugDirscopef reports dirscope internals at debug level: the opt-in detail
// layer for binding/history/search behaviour.
func debugDirscopef(ctx context.Context, format string, args ...any) {
	runenv.Debugf(ctx, runenv.SubsystemDirscope, format, args...)
}
