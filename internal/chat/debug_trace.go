package chat

import (
	"fmt"

	"github.com/baalimago/clai/internal/debugflags"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
)

func debugChatEnabled() bool {
	return debugflags.Enabled("CHAT")
}

func traceChatf(format string, args ...any) {
	if !debugChatEnabled() {
		return
	}
	ancli.PrintOK(fmt.Sprintf("[DEBUG_CHAT] "+format+"\n", args...))
}

// debugDirscopef prints dirscope internals when DEBUG_DIRSCOPE (or plain
// DEBUG) is truthy. Recording and search stay silent by default; this is the
// opt-in detail layer for binding/history/search behaviour.
func debugDirscopef(format string, args ...any) {
	if !debugflags.Enabled("DIRSCOPE") {
		return
	}
	ancli.PrintOK(fmt.Sprintf("[DEBUG_DIRSCOPE] "+format+"\n", args...))
}
