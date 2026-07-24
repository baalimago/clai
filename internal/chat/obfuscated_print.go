package chat

import (
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/baalimago/clai/internal/utils"
	pub_models "github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/go_away_boilerplate/pkg/table"
)

// messageDisplayText returns human-facing text for a message.
// Reasoning content is included when present, wrapped in [thinking]…[/thinking].
// Assistant tool-call turns are persisted with an empty Content (only ToolCalls)
// so the model never learns the "Call: ..." format; for display we reconstruct
// a readable line from the calls.
func messageDisplayText(m pub_models.Message) string {
	var parts []string
	if m.ReasoningContent != "" {
		parts = append(parts, "[thinking]\n"+m.ReasoningContent+"\n[/thinking]")
	}
	if s := m.String(); s != "" {
		parts = append(parts, s)
	}
	if len(parts) > 0 {
		return strings.Join(parts, "\n")
	}
	if len(m.ToolCalls) > 0 {
		tcParts := make([]string, 0, len(m.ToolCalls))
		for _, c := range m.ToolCalls {
			tcParts = append(tcParts, c.PrettyPrint())
		}
		return strings.Join(tcParts, "\n")
	}
	return ""
}

func messageContentLen(m pub_models.Message) int {
	return utf8.RuneCountInString(messageDisplayText(m))
}

const (
	headTruncated = 3 // messages after first user to show truncated
	tailTruncated = 3 // messages before last to show truncated
	bridgeSample  = 3 // obfuscated 1-liners in the middle bridge
	minForGap     = headTruncated + bridgeSample + tailTruncated + 3
	// Need at least: 1 (first user) + headTruncated + bridgeSample + tailTruncated + 1 (last) = 11
)

func printChatObfuscated(w io.Writer, ch pub_models.Chat, raw bool) error {
	msgs := ch.Messages
	msgCount := len(msgs)
	if msgCount == 0 {
		return nil
	}

	// Find first user message index.
	firstUserIdx := -1
	for i, m := range msgs {
		if m.Role == "user" {
			firstUserIdx = i
			break
		}
	}
	if firstUserIdx == -1 {
		firstUserIdx = 0
	}

	useGap := msgCount > minForGap

	// Preamble: messages before the first user — shown truncated.
	for i := 0; i < firstUserIdx; i++ {
		m := msgs[i]
		disp := messageDisplayText(m)
		disp = utils.ShortenedOutput(disp, 3)
		m.Content = disp
		m.ContentParts = nil
		m.ReasoningContent = ""
		fmt.Fprintf(w, "%s ", table.Colorize(utils.TableTheme().Primary, fmt.Sprintf("[#%-3d]", i)))
		if err := utils.AttemptPrettyPrint(w, m, "user", raw); err != nil {
			return fmt.Errorf("pretty print preamble message %d: %w", i, err)
		}
	}

	// Section 1: First user message — full pretty print.
	m0 := msgs[firstUserIdx]
	disp0 := messageDisplayText(m0)
	m0.Content = disp0
	m0.ContentParts = nil
	m0.ReasoningContent = ""
	fmt.Fprintf(w, "%s ", table.Colorize(utils.TableTheme().Primary, fmt.Sprintf("[#%-3d]", firstUserIdx)))
	if err := utils.AttemptPrettyPrint(w, m0, "user", raw); err != nil {
		return fmt.Errorf("pretty print first user message: %w", err)
	}

	headEnd := firstUserIdx + 1 + headTruncated
	if headEnd > msgCount-1 {
		headEnd = msgCount - 1
	}

	// Section 2: Head window — truncated messages after first user.
	for i := firstUserIdx + 1; i < headEnd; i++ {
		m := msgs[i]
		disp := messageDisplayText(m)
		disp = utils.ShortenedOutput(disp, 3)
		m.Content = disp
		m.ContentParts = nil
		m.ReasoningContent = ""
		fmt.Fprintf(w, "%s ", table.Colorize(utils.TableTheme().Primary, fmt.Sprintf("[#%-3d]", i)))
		if err := utils.AttemptPrettyPrint(w, m, "user", raw); err != nil {
			return fmt.Errorf("pretty print head message %d: %w", i, err)
		}
	}

	if useGap {
		tailStart := msgCount - tailTruncated - 1 // last message is separate

		// Section 3: Middle bridge — 3 obfuscated 1-liners.
		bridgeStart := headEnd
		bridgeEnd := bridgeStart + bridgeSample
		if bridgeEnd > tailStart {
			bridgeEnd = tailStart
		}
		for i := bridgeStart; i < bridgeEnd; i++ {
			m := msgs[i]
			lenRunes := messageContentLen(m)
			prefix := table.Colorize(utils.TableTheme().Primary, fmt.Sprintf("[#%-3d r: ", i)) +
				table.Colorize(utils.RoleColor(m.Role), fmt.Sprintf("%-9s", m.Role)) +
				table.Colorize(utils.TableTheme().Primary, fmt.Sprintf(" l: %5d]: ", lenRunes))
			trunc, err := table.WidthAppropriateStringTruncColored(
				messageDisplayText(m), prefix, "", utils.TableTheme().Breadtext, 5,
			)
			if err != nil {
				return fmt.Errorf("truncate bridge preview: %w", err)
			}
			if _, err := fmt.Fprintln(w, trunc); err != nil {
				return fmt.Errorf("write bridge line: %w", err)
			}
		}

		// Gap label.
		gapCount := tailStart - bridgeEnd
		if gapCount > 0 {
			gapLine := table.Colorize(utils.TableTheme().Secondary, fmt.Sprintf("\n            ... and %d more entries\n", gapCount))
			if _, err := fmt.Fprintln(w, gapLine); err != nil {
				return fmt.Errorf("write gap label: %w", err)
			}
		}

		// Section 4: Tail window — 3 truncated messages before last.
		for i := tailStart; i < msgCount-1; i++ {
			m := msgs[i]
			disp := messageDisplayText(m)
			disp = utils.ShortenedOutput(disp, 3)
			m.Content = disp
			m.ContentParts = nil
			m.ReasoningContent = ""
			fmt.Fprintf(w, "%s ", table.Colorize(utils.TableTheme().Primary, fmt.Sprintf("[#%-3d]", i)))
			if err := utils.AttemptPrettyPrint(w, m, "user", raw); err != nil {
				return fmt.Errorf("pretty print tail message %d: %w", i, err)
			}
		}
	} else {
		// No gap: print remaining messages truncated, except the last.
		for i := headEnd; i < msgCount-1; i++ {
			m := msgs[i]
			disp := messageDisplayText(m)
			disp = utils.ShortenedOutput(disp, 3)
			m.Content = disp
			m.ContentParts = nil
			m.ReasoningContent = ""
			fmt.Fprintf(w, "%s ", table.Colorize(utils.TableTheme().Primary, fmt.Sprintf("[#%-3d]", i)))
			if err := utils.AttemptPrettyPrint(w, m, "user", raw); err != nil {
				return fmt.Errorf("pretty print message %d: %w", i, err)
			}
		}
	}

	// Section 5: Last message — full pretty print.
	last := msgs[msgCount-1]
	dispLast := messageDisplayText(last)
	last.Content = dispLast
	last.ContentParts = nil
	last.ReasoningContent = ""
	fmt.Fprintf(w, "%s ", table.Colorize(utils.TableTheme().Primary, fmt.Sprintf("[#%-3d]", msgCount-1)))
	if err := utils.AttemptPrettyPrint(w, last, "user", raw); err != nil {
		return fmt.Errorf("pretty print last message: %w", err)
	}

	return nil
}
