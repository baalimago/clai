package chat

// labelFor is the one display label of a conversation: its title when it has
// one, else its first user message.
func labelFor(title, firstUserMessage string) string {
	if title != "" {
		return title
	}
	return firstUserMessage
}
