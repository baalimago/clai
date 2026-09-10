package text

import (
	"fmt"
	"os"

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

// traceSummaryf writes to stderr so traces never enter a -r answer stream.
func traceSummaryf(format string, args ...any) {
	if !debugflags.Enabled("SUMMARY") {
		return
	}
	fmt.Fprintf(os.Stderr, "[DEBUG_SUMMARY] "+format+"\n", args...)
}
