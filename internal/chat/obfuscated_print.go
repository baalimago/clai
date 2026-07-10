package chat

import (
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/baalimago/clai/internal/utils"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// messageDisplayText returns human-facing text for a message. Assistant tool-call
// turns are persisted with an empty Content (only ToolCalls) so the model never
// learns the "Call: ..." format; for display we reconstruct a readable line from
// the calls. This is display-only — Message.String() is deliberately left untouched
// because it also feeds conversation search and agent results.
func messageDisplayText(m pub_models.Message) string {
	if s := m.String(); s != "" {
		return s
	}
	if len(m.ToolCalls) > 0 {
		parts := make([]string, 0, len(m.ToolCalls))
		for _, c := range m.ToolCalls {
			parts = append(parts, c.PrettyPrint())
		}
		return strings.Join(parts, "\n")
	}
	return ""
}

func messageContentLen(m pub_models.Message) int {
	return utf8.RuneCountInString(messageDisplayText(m))
}

func printChatObfuscated(w io.Writer, ch pub_models.Chat, raw bool) error {
	msgCount := len(ch.Messages)
	prettyStart := 0
	if msgCount > 6 {
		prettyStart = msgCount - 6
	}

	for i, m := range ch.Messages {
		lenRunes := messageContentLen(m)

		// Old messages: oneliners, width-truncated, no pretty print.
		if i < prettyStart {
			// Everything inside [] is primary, except role value which matches AttemptPrettyPrint.
			prefix := utils.Colorize(utils.ThemePrimaryColor(), fmt.Sprintf("[#%-3d r: ", i)) +
				utils.Colorize(utils.RoleColor(m.Role), fmt.Sprintf("%-9s", m.Role)) +
				utils.Colorize(utils.ThemePrimaryColor(), fmt.Sprintf(" l: %5d]: ", lenRunes))

			trunc, err := utils.WidthAppropriateStringTruncColored(
				messageDisplayText(m), prefix, "", utils.ThemeBreadtextColor(), 5,
			)
			if err != nil {
				return fmt.Errorf("truncate message preview: %w", err)
			}
			if _, err := fmt.Fprintln(w, trunc); err != nil {
				return fmt.Errorf("write obfuscated chat line: %w", err)
			}
			continue
		}

		// The last 6 messages: pretty print, reconstructing tool-call turns. Only the
		// most recent message keeps full length.
		m2 := m
		disp := messageDisplayText(m2)
		if i < len(ch.Messages)-1 {
			disp = utils.ShortenedOutput(disp, 3)
		}
		m2.Content = disp
		m2.ContentParts = nil
		if err := utils.AttemptPrettyPrint(w, m2, "user", raw); err != nil {
			return fmt.Errorf("pretty print chat message: %w", err)
		}
	}
	return nil
}
