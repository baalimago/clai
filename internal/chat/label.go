package chat

import "github.com/baalimago/clai/internal/utils"

// labelFor is the one display label of a conversation, as one terminal row: its title
// when it has one, else its first user message.
func labelFor(title, firstUserMessage string) string {
	if title != "" {
		return utils.SanitizeTerminalRow(title)
	}
	return utils.SanitizeTerminalRow(firstUserMessage)
}
