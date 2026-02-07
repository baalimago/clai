package chat

import (
	"fmt"
	"io"
	"unicode/utf8"

	"github.com/baalimago/clai/internal/utils"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func messageContentLen(m pub_models.Message) int {
	if m.Content != "" {
		return utf8.RuneCountInString(m.Content)
	}
	l := 0
	for _, p := range m.ContentParts {
		if p.Text != "" {
			l += utf8.RuneCountInString(p.Text)
		}
	}
	return l
}

func messagePreview(m pub_models.Message) string {
	if m.Content != "" {
		return m.Content
	}
	parts := ""
	for _, p := range m.ContentParts {
		if p.Text == "" {
			continue
		}
		if parts != "" {
			parts += " "
		}
		parts += p.Text
	}
	return parts
}

func printChatObfuscated(w io.Writer, ch pub_models.Chat, raw bool) error {
	msgCount := len(ch.Messages)
	prettyStart := 0
	if msgCount > 6 {
		prettyStart = msgCount - 6
	}

	for i, m := range ch.Messages {
		lenRunes := messageContentLen(m)
		prefix := fmt.Sprintf("[#%-4d r: %-9s l: %05d]: ", i, m.Role, lenRunes)

		// Old messages: oneliners, width-truncated, no pretty print.
		if i < prettyStart {
			preview := messagePreview(m)
			trunc, err := utils.WidthAppropriateStringTrunc(preview, prefix, 20)
			if err != nil {
				return fmt.Errorf("truncate message preview: %w", err)
			}
			if _, err := fmt.Fprintln(w, trunc); err != nil {
				return fmt.Errorf("write obfuscated chat line: %w", err)
			}
			continue
		}

		// The last 4 messages: pretty print but shorten content.
		m2 := m
		// Leave full length for most recent message
		if i < len(ch.Messages)-1 {
			m2.Content = utils.ShortenedOutput(messagePreview(m2), 3)
			m2.ContentParts = nil
		}
		if err := utils.AttemptPrettyPrint(w, m2, "user", raw); err != nil {
			return fmt.Errorf("pretty print chat message: %w", err)
		}
		if i != len(ch.Messages)-1 {
			continue
		}

	}
	return nil
}
