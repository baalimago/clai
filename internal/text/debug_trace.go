package text

import (
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
	ancli.Noticef("[DEBUG_CHAT] "+format+"\n", args...)
}
