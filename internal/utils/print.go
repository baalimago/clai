package utils

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"unicode/utf8"

	pub_models "github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/go_away_boilerplate/pkg/table"
)

const MaxShortenedNewlines = 5

// UpdateMessageTerminalMetadata updates the terminal metadata. Meaning the lineCount, to eventually
// clear the terminal
func UpdateMessageTerminalMetadata(msg string, line *string, lineCount *int, termWidth int) {
	if termWidth <= 0 {
		termWidth = 1
	}

	newlineSplit := strings.Split(*line+msg, "\n")
	*lineCount = 0

	for _, segment := range newlineSplit {
		if len(segment) == 0 {
			*lineCount++
			continue
		}

		runeCount := utf8.RuneCountInString(segment)
		fullLines := runeCount / termWidth
		if runeCount%termWidth > 0 {
			fullLines++
		}
		*lineCount += fullLines
	}

	if *lineCount == 0 {
		*lineCount = 1
	}

	lastSegment := newlineSplit[len(newlineSplit)-1]
	if len(lastSegment) > termWidth {
		lastWords := strings.Split(lastSegment, " ")
		lastWord := lastWords[len(lastWords)-1]
		if len(lastWord) > termWidth {
			*line = lastWord[len(lastWord)-termWidth:]
		} else {
			*line = lastWord
		}
	} else {
		*line = lastSegment
	}
}

// AttemptPrettyPrint by first checking if the glow command is available, and if so, pretty print the chat message.
// If not found, simply print the message as is.
// If the message has ReasoningContent, it is rendered with reasoning color before the main content.
//
// If w is nil, os.Stdout is used.
func AttemptPrettyPrint(w io.Writer, chatMessage pub_models.Message, username string, raw bool) error {
	if w == nil {
		w = os.Stdout
	}

	content := chatMessage.Content

	if raw {
		if chatMessage.ReasoningContent != "" {
			fmt.Fprintln(w, "[thinking]")
			fmt.Fprintln(w, chatMessage.ReasoningContent)
			fmt.Fprintln(w, "[/thinking]")
		}
		fmt.Fprintln(w, content)
		return nil
	}

	role := chatMessage.Role
	if chatMessage.Role == "user" {
		role = username
	}

	// Respect NO_COLOR.
	if table.NoColor() {
		if chatMessage.ReasoningContent != "" {
			if _, err := fmt.Fprintf(w, "[thinking]\n%v\n[/thinking]\n%v: %v\n", chatMessage.ReasoningContent, role, content); err != nil {
				return fmt.Errorf("write chat message with reasoning: %w", err)
			}
			return nil
		}
		if _, err := fmt.Fprintf(w, "%v: %v\n", role, content); err != nil {
			return fmt.Errorf("write chat message: %w", err)
		}
		return nil
	}

	roleCol := RoleColor(chatMessage.Role)
	coloredRole := table.Colorize(roleCol, role)

	cmd := exec.Command("glow", "--version")
	if err := cmd.Run(); err != nil {
		// No glow: print with ANSI coloring.
		if chatMessage.ReasoningContent != "" {
			reasoningCol := RoleColor("reasoning")
			if _, err := fmt.Fprintf(w, "%v:\n%v\n", coloredRole,
				table.Colorize(reasoningCol, "[thinking]\n"+chatMessage.ReasoningContent+"\n[/thinking]\n"+content)); err != nil {
				return fmt.Errorf("write chat message (no glow, reasoning): %w", err)
			}
			return nil
		}
		if _, err := fmt.Fprintf(w, "%v: %v\n", coloredRole, content); err != nil {
			return fmt.Errorf("write chat message (no glow): %w", err)
		}
		return nil
	}

	// Glow available: print reasoning with ANSI coloring, then run glow on content.
	if _, err := fmt.Fprintf(w, "%v:", coloredRole); err != nil {
		return fmt.Errorf("write role prefix: %w", err)
	}

	if chatMessage.ReasoningContent != "" {
		reasoningCol := RoleColor("reasoning")
		if _, err := fmt.Fprintf(w, "\n%v", table.Colorize(reasoningCol, "[thinking]\n"+chatMessage.ReasoningContent+"\n[/thinking]")); err != nil {
			return fmt.Errorf("write reasoning content: %w", err)
		}
	}

	termWidth, err := table.TermWidth()
	if err != nil {
		return fmt.Errorf("get terminal width for glow: %w", err)
	}
	glowWidth := max(termWidth-5, 1)

	cmd = exec.Command("glow", "-w", strconv.Itoa(glowWidth))
	inp := content
	// For some reason glow hides specifically <thikning>. So, replace it to [thinking]
	inp = strings.ReplaceAll(inp, "<thinking>", "[thinking]")
	inp = strings.ReplaceAll(inp, "</thinking>", "[/thinking]")
	cmd.Stdin = bytes.NewBufferString(inp)
	cmd.Stdout = w
	cmd.Stderr = w
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("run glow: %w", err)
	}
	return nil
}

// ShortenedOutput returns a shortened version of the output
func ShortenedOutput(out string, maxShortenedNewlines int) string {
	maxTokens := 20
	maxRunes := 100
	outSplit := strings.Split(out, " ")
	outNewlineSplit := strings.Split(out, "\n")
	firstTokens := GetFirstTokens(outSplit, maxTokens)
	amRunes := utf8.RuneCountInString(out)
	if len(firstTokens) < maxTokens && len(outNewlineSplit) < maxShortenedNewlines && amRunes < maxRunes {
		return out
	}
	firstTokensStr := strings.Join(firstTokens, " ")
	amLeft := len(outSplit) - maxTokens
	abbreviationType := "tokens"
	if len(outNewlineSplit) > maxShortenedNewlines {
		firstTokensStr = strings.Join(GetFirstTokens(outNewlineSplit, maxShortenedNewlines), "\n")
		amLeft = len(outNewlineSplit) - maxShortenedNewlines
		abbreviationType = "lines"
		return fmt.Sprintf("%v\n...[and %v more %v]", firstTokensStr, amLeft, abbreviationType)
	}
	if amRunes > maxRunes {
		return fmt.Sprintf("%v\n...[and %v more runes]", out[:maxRunes], amRunes-maxRunes)
	}
	return fmt.Sprintf("%v\n...[and %v more %v]", firstTokensStr, amLeft, abbreviationType)
}

func PrepareDisplayMessage(msg pub_models.Message) pub_models.Message {
	display := msg
	if display.Role == "tool" && !strings.Contains(display.Content, "mcp_") {
		display.Content = ShortenedOutput(display.Content, MaxShortenedNewlines)
		return display
	}
	if display.Role == "assistant" && display.ReasoningContent == "" {
		display.Content = ShortenedOutput(display.Content, MaxShortenedNewlines)
	}
	return display
}
