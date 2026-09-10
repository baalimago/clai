package summary

import (
	"fmt"
	"os"
	"strings"
)

// EnvSummarizer is the operator kill switch for the real conversation
// summarizer: "off" refuses to build it in any process (worklog
// 2026-09-09-conversation-summaries, D32). Tests never reach this
// constructor through the CLI unless their fixture opts in: the root test
// binary swaps main.go's injected constructor for a refusing one.
const EnvSummarizer = "CLAI_SUMMARIZER"

func allowed() error {
	if strings.EqualFold(strings.TrimSpace(os.Getenv(EnvSummarizer)), "off") {
		return fmt.Errorf("conversation summarizer disabled by %s=off", EnvSummarizer)
	}
	return nil
}
