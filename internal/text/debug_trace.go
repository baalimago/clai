package text

import (
	"os"

	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
	"github.com/baalimago/go_away_boilerplate/pkg/misc"
)

func debugChatEnabled() bool {
	return misc.Truthy(os.Getenv("DEBUG_CHAT")) || misc.Truthy(os.Getenv("DEBUG"))
}

func traceChatf(format string, args ...any) {
	if !debugChatEnabled() {
		return
	}
	ancli.Noticef("[DEBUG_CHAT] "+format+"\n", args...)
}
